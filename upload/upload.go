package upload

import (
	"context"
	"errors"
	"io"
)

var (
	// ErrNotFound 文件不存在
	ErrNotFound = errors.New("upload: file not found")
	// ErrEmptyFile 文件内容为空
	ErrEmptyFile = errors.New("upload: empty file")
	// ErrFileTooLarge 文件超过大小限制
	ErrFileTooLarge = errors.New("upload: file too large")
	// ErrTypeNotAllowed 文件类型不允许上传
	ErrTypeNotAllowed = errors.New("upload: file type not allowed")
	// ErrInvalidKey 存储路径不合法，如包含 ".." 或以 "/" 开头
	ErrInvalidKey = errors.New("upload: invalid key")
)

// Storage 文件存储，key 为存储路径，使用 "/" 分隔，如 images/2026/10/21/xxx.png
type Storage interface {
	// Put 写入文件，key 已存在时覆盖
	Put(ctx context.Context, key string, r io.Reader, opts PutOptions) (*Object, error)
	// Get 文件不存在时返回 ErrNotFound，调用方负责 Close
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete 文件不存在时忽略
	Delete(ctx context.Context, keys ...string) error
	Exists(ctx context.Context, key string) (bool, error)
	// URL 返回访问地址；私有存储返回带签名的临时地址
	URL(ctx context.Context, key string) (string, error)
}

// PutOptions 写入参数
type PutOptions struct {
	ContentType string
}

// Object 上传结果，业务表建议保存 Key，访问时再通过 Storage.URL 获取地址
type Object struct {
	Key         string
	URL         string
	Size        int64
	ContentType string
	// Filename 原始文件名，仅用于展示，不参与存储路径
	Filename string
}
