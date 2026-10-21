package upload

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// memStorage 测试用的内存存储
type memStorage struct {
	files map[string][]byte
}

func newMemStorage() *memStorage {
	return &memStorage{files: make(map[string][]byte)}
}

func (s *memStorage) Put(ctx context.Context, key string, r io.Reader, opts PutOptions) (*Object, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	s.files[key] = data
	return &Object{Key: key, URL: "https://cdn.test/" + key, Size: int64(len(data)), ContentType: opts.ContentType}, nil
}

func (s *memStorage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	data, ok := s.files[key]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *memStorage) Delete(ctx context.Context, keys ...string) error {
	for _, key := range keys {
		delete(s.files, key)
	}
	return nil
}

func (s *memStorage) Exists(ctx context.Context, key string) (bool, error) {
	_, ok := s.files[key]
	return ok, nil
}

func (s *memStorage) URL(ctx context.Context, key string) (string, error) {
	return "https://cdn.test/" + key, nil
}

// pngData PNG 文件头，足够被 http.DetectContentType 识别
var pngData = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 100)...)

func newTestUploader(opts Options) (*Uploader, *memStorage) {
	s := newMemStorage()
	u := NewUploader(s, opts)
	u.now = func() time.Time { return time.Date(2026, 10, 21, 0, 0, 0, 0, time.Local) }
	return u, s
}

func TestUploadImage(t *testing.T) {
	u, s := newTestUploader(ImageOptions)

	// 扩展名按内容类型确定，用户传入的 .jpeg 被忽略
	obj, err := u.Upload(context.Background(), `C:\Users\me\photo.jpeg`, bytes.NewReader(pngData))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(obj.Key, "images/2026/10/21/") || !strings.HasSuffix(obj.Key, ".png") {
		t.Errorf("key = %q", obj.Key)
	}
	if obj.ContentType != "image/png" || obj.Size != int64(len(pngData)) || obj.Filename != "photo.jpeg" {
		t.Errorf("obj = %+v", obj)
	}
	if !bytes.Equal(s.files[obj.Key], pngData) {
		t.Error("stored data mismatch")
	}
}

func TestUploadTypeNotAllowed(t *testing.T) {
	u, _ := newTestUploader(ImageOptions)
	_, err := u.Upload(context.Background(), "a.png", strings.NewReader("plain text"))
	if !errors.Is(err, ErrTypeNotAllowed) {
		t.Errorf("err = %v", err)
	}
}

func TestUploadDeniedTypes(t *testing.T) {
	u, _ := newTestUploader(FileOptions)
	for _, content := range []string{
		"<!DOCTYPE html><html><script>alert(1)</script></html>",
		`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
	} {
		if _, err := u.Upload(context.Background(), "a.txt", strings.NewReader(content)); !errors.Is(err, ErrTypeNotAllowed) {
			t.Errorf("content %q: err = %v", content, err)
		}
	}
}

func TestUploadDeniedExtension(t *testing.T) {
	u, _ := newTestUploader(FileOptions)
	obj, err := u.Upload(context.Background(), "a.php", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(obj.Key, ".") {
		t.Errorf("key = %q, want no extension", obj.Key)
	}
}

func TestUploadKeepExtension(t *testing.T) {
	u, _ := newTestUploader(FileOptions)
	obj, err := u.Upload(context.Background(), "report.CSV", strings.NewReader("a,b\n1,2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(obj.Key, ".csv") {
		t.Errorf("key = %q", obj.Key)
	}
}

func TestUploadTooLarge(t *testing.T) {
	opts := FileOptions
	opts.MaxSize = 1000
	u, s := newTestUploader(opts)

	if _, err := u.Upload(context.Background(), "a.txt", strings.NewReader(strings.Repeat("a", 1000))); err != nil {
		t.Fatalf("exactly MaxSize: %v", err)
	}
	if _, err := u.Upload(context.Background(), "a.txt", strings.NewReader(strings.Repeat("a", 1001))); !errors.Is(err, ErrFileTooLarge) {
		t.Errorf("err = %v", err)
	}
	if len(s.files) != 1 {
		t.Errorf("stored %d files, want 1", len(s.files))
	}
}

func TestUploadEmpty(t *testing.T) {
	u, _ := newTestUploader(FileOptions)
	if _, err := u.Upload(context.Background(), "a.txt", strings.NewReader("")); !errors.Is(err, ErrEmptyFile) {
		t.Errorf("err = %v", err)
	}
}

func TestAllowedWildcard(t *testing.T) {
	opts := Options{AllowedTypes: []string{"image/*"}}
	if !opts.allowed("image/webp") {
		t.Error("image/webp should be allowed")
	}
	if opts.allowed("application/pdf") {
		t.Error("application/pdf should not be allowed")
	}
}
