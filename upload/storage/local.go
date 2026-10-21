package storage

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/hongji0309/toolkit/upload"
)

var _ upload.Storage = (*Local)(nil)

// LocalConfig 本地存储配置
type LocalConfig struct {
	// Root 文件存放根目录，不存在时自动创建
	Root string
	// BaseURL 访问地址前缀，如 https://static.example.com/uploads，需由 Nginx 等指向 Root
	BaseURL string
}

// Local 基于本地磁盘的文件存储，仅适用于单实例部署或挂载了共享盘的场景
type Local struct {
	root    string
	baseURL string
}

func NewLocal(cfg LocalConfig) (*Local, error) {
	if cfg.Root == "" {
		return nil, errors.New("storage: local root is required")
	}
	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Local{root: root, baseURL: strings.TrimRight(cfg.BaseURL, "/")}, nil
}

// Put 先写入同目录的临时文件再重命名，避免读到写了一半的文件
func (s *Local) Put(ctx context.Context, key string, r io.Reader, opts upload.PutOptions) (*upload.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name, err := s.filename(key)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return nil, err
	}
	size, err := io.Copy(tmp, r)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		// CreateTemp 创建的文件权限为 0600，Nginx 等其他用户需要可读
		err = os.Chmod(tmp.Name(), 0o644)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), name)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return nil, err
	}

	return &upload.Object{
		Key:         key,
		URL:         s.url(key),
		Size:        size,
		ContentType: opts.ContentType,
	}, nil
}

// Get 文件不存在时返回 upload.ErrNotFound
func (s *Local) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	name, err := s.filename(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, upload.ErrNotFound
	}
	return f, err
}

func (s *Local) Delete(ctx context.Context, keys ...string) error {
	for _, key := range keys {
		name, err := s.filename(key)
		if err != nil {
			return err
		}
		if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Local) Exists(ctx context.Context, key string) (bool, error) {
	name, err := s.filename(key)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return !info.IsDir(), nil
}

func (s *Local) URL(ctx context.Context, key string) (string, error) {
	if _, err := s.filename(key); err != nil {
		return "", err
	}
	return s.url(key), nil
}

func (s *Local) url(key string) string {
	return s.baseURL + "/" + key
}

// filename 将 key 转为磁盘路径；只接受规范的相对路径，防止通过 ".." 写到 Root 之外
func (s *Local) filename(key string) (string, error) {
	if key == "" || strings.Contains(key, "\\") || path.Clean("/" + key)[1:] != key {
		return "", upload.ErrInvalidKey
	}
	return filepath.Join(s.root, filepath.FromSlash(key)), nil
}
