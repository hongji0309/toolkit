package upload

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"path"
	"strings"
	"time"
)

// sniffLen http.DetectContentType 最多读取的字节数
const sniffLen = 512

// Uploader 在 Storage 之上做大小、类型校验，并生成存储路径
type Uploader struct {
	storage Storage
	opts    Options
	now     func() time.Time
}

func NewUploader(storage Storage, opts Options) *Uploader {
	return &Uploader{storage: storage, opts: opts, now: time.Now}
}

// Upload 上传文件，类型按文件内容识别，filename 仅用于记录原始文件名和推断扩展名
func (u *Uploader) Upload(ctx context.Context, filename string, r io.Reader) (*Object, error) {
	head := make([]byte, sniffLen)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	if n == 0 {
		return nil, ErrEmptyFile
	}
	head = head[:n]

	contentType := mediaType(http.DetectContentType(head))
	if !u.opts.allowed(contentType) {
		return nil, ErrTypeNotAllowed
	}

	body := io.MultiReader(bytes.NewReader(head), r)
	var limited *limitReader
	if u.opts.MaxSize > 0 {
		limited = &limitReader{r: body, remaining: u.opts.MaxSize}
		body = limited
	}

	key, err := u.newKey(extension(contentType, filename))
	if err != nil {
		return nil, err
	}
	obj, err := u.storage.Put(ctx, key, body, PutOptions{ContentType: contentType})
	if err != nil {
		// Storage 可能包装了读取错误，这里统一识别为超出大小限制
		if limited != nil && limited.exceeded() {
			return nil, ErrFileTooLarge
		}
		return nil, err
	}
	obj.Filename = baseName(filename)
	return obj, nil
}

// UploadFileHeader 上传 multipart 表单中的文件，如 gin 的 c.FormFile 返回值
func (u *Uploader) UploadFileHeader(ctx context.Context, fh *multipart.FileHeader) (*Object, error) {
	if u.opts.MaxSize > 0 && fh.Size > u.opts.MaxSize {
		return nil, ErrFileTooLarge
	}
	f, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return u.Upload(ctx, fh.Filename, f)
}

// newKey 生成 {Dir}/{yyyy}/{mm}/{dd}/{随机ID}{ext}
func (u *Uploader) newKey(ext string) (string, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return "", err
	}
	return path.Join(u.opts.Dir, u.now().Format("2006/01/02"), hex.EncodeToString(id)+ext), nil
}

// mediaType 去掉参数部分，如 "text/plain; charset=utf-8" 返回 "text/plain"
func mediaType(contentType string) string {
	t, _, _ := strings.Cut(contentType, ";")
	return strings.TrimSpace(t)
}

// baseName 兼容 Windows 浏览器上传的完整路径
func baseName(filename string) string {
	if filename == "" {
		return ""
	}
	return path.Base(strings.ReplaceAll(filename, "\\", "/"))
}

// limitReader 读取超过 remaining 字节时返回 ErrFileTooLarge
type limitReader struct {
	r         io.Reader
	remaining int64
}

func (l *limitReader) Read(p []byte) (int, error) {
	if l.exceeded() {
		return 0, ErrFileTooLarge
	}
	// 多读 1 字节，用来判断是否超出限制
	if int64(len(p)) > l.remaining+1 {
		p = p[:l.remaining+1]
	}
	n, err := l.r.Read(p)
	l.remaining -= int64(n)
	if l.exceeded() {
		return n, ErrFileTooLarge
	}
	return n, err
}

func (l *limitReader) exceeded() bool {
	return l.remaining < 0
}
