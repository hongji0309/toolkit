package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hongji0309/toolkit/upload"
)

func newTestLocal(t *testing.T) (*Local, string) {
	t.Helper()
	root := t.TempDir()
	s, err := NewLocal(LocalConfig{Root: root, BaseURL: "https://static.test/uploads/"})
	if err != nil {
		t.Fatal(err)
	}
	return s, root
}

func TestLocalPutGetDelete(t *testing.T) {
	s, root := newTestLocal(t)
	ctx := context.Background()
	key := "images/2026/10/21/a.png"

	obj, err := s.Put(ctx, key, strings.NewReader("hello"), upload.PutOptions{ContentType: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	if obj.URL != "https://static.test/uploads/"+key || obj.Size != 5 {
		t.Errorf("obj = %+v", obj)
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(key)))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("perm = %v", info.Mode().Perm())
	}

	rc, err := s.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	if string(data) != "hello" {
		t.Errorf("data = %q", data)
	}

	if err := s.Delete(ctx, key, "not/exist.png"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Exists(ctx, key); ok {
		t.Error("file should be deleted")
	}
	if _, err := s.Get(ctx, key); !errors.Is(err, upload.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestLocalInvalidKey(t *testing.T) {
	s, _ := newTestLocal(t)
	for _, key := range []string{"", ".", "..", "../a.png", "a/../../b.png", "/etc/passwd", "a//b.png", "a/", `a\b.png`} {
		if _, err := s.Put(context.Background(), key, strings.NewReader("x"), upload.PutOptions{}); !errors.Is(err, upload.ErrInvalidKey) {
			t.Errorf("key %q: err = %v", key, err)
		}
	}
}

// failReader 读到一半返回错误，模拟超出大小限制
type failReader struct{ n int }

func (r *failReader) Read(p []byte) (int, error) {
	if r.n > 0 {
		r.n = 0
		return copy(p, "partial"), nil
	}
	return 0, upload.ErrFileTooLarge
}

func TestLocalPutFailedLeavesNoFile(t *testing.T) {
	s, root := newTestLocal(t)
	key := "files/a.txt"
	if _, err := s.Put(context.Background(), key, &failReader{n: 1}, upload.PutOptions{}); !errors.Is(err, upload.ErrFileTooLarge) {
		t.Fatalf("err = %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "files"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("leftover files: %v", entries)
	}
}
