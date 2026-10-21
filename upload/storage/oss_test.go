package storage

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"

	"github.com/hongji0309/toolkit/upload"
)

// fakeOSS 模拟 OSS 的路径风格接口，支持简单上传、分片上传、下载、HEAD 和批量删除
type fakeOSS struct {
	mu           sync.Mutex
	objects      map[string][]byte
	contentTypes map[string]string
	parts        map[string]map[int][]byte // uploadId -> partNumber -> data
	putCount     int                       // 简单上传次数
	nextID       int
}

func newFakeOSS() *fakeOSS {
	return &fakeOSS{
		objects:      make(map[string][]byte),
		contentTypes: make(map[string]string),
		parts:        make(map[string]map[int][]byte),
	}
}

func (f *fakeOSS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// 路径风格：/{bucket}/{key}
	_, key, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	q := r.URL.Query()

	switch {
	case r.Method == http.MethodPost && q.Has("delete"):
		var req struct {
			Objects []struct {
				Key string `xml:"Key"`
			} `xml:"Object"`
		}
		_ = xml.NewDecoder(r.Body).Decode(&req)
		for _, o := range req.Objects {
			delete(f.objects, o.Key)
		}
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><DeleteResult></DeleteResult>`)

	case r.Method == http.MethodPost && q.Has("uploads"):
		f.nextID++
		id := strconv.Itoa(f.nextID)
		f.parts[id] = make(map[int][]byte)
		f.contentTypes[key] = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><InitiateMultipartUploadResult><Bucket>b</Bucket><Key>%s</Key><UploadId>%s</UploadId></InitiateMultipartUploadResult>`, key, id)

	case r.Method == http.MethodPut && q.Has("uploadId"):
		data, _ := io.ReadAll(r.Body)
		n, _ := strconv.Atoi(q.Get("partNumber"))
		f.parts[q.Get("uploadId")][n] = data
		w.Header().Set("ETag", fmt.Sprintf(`"part-%d"`, n))

	case r.Method == http.MethodPost && q.Has("uploadId"):
		parts := f.parts[q.Get("uploadId")]
		nums := make([]int, 0, len(parts))
		for n := range parts {
			nums = append(nums, n)
		}
		sort.Ints(nums)
		var buf bytes.Buffer
		for _, n := range nums {
			buf.Write(parts[n])
		}
		f.objects[key] = buf.Bytes()
		delete(f.parts, q.Get("uploadId"))
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><CompleteMultipartUploadResult><Bucket>b</Bucket><Key>%s</Key><ETag>"done"</ETag></CompleteMultipartUploadResult>`, key)

	case r.Method == http.MethodDelete && q.Has("uploadId"):
		delete(f.parts, q.Get("uploadId"))
		w.WriteHeader(http.StatusNoContent)

	case r.Method == http.MethodPut:
		data, _ := io.ReadAll(r.Body)
		f.objects[key] = data
		f.contentTypes[key] = r.Header.Get("Content-Type")
		f.putCount++
		w.Header().Set("ETag", `"put"`)

	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		data, ok := f.objects[key]
		if !ok {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			if r.Method == http.MethodGet {
				fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code><Message>The specified key does not exist.</Message></Error>`)
			}
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Content-Type", f.contentTypes[key])
		if r.Method == http.MethodGet {
			_, _ = w.Write(data)
		}

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func newTestOSS(t *testing.T, cfg OSSConfig) (*OSS, *fakeOSS) {
	t.Helper()
	fake := newFakeOSS()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)

	cfg.Region = "cn-hangzhou"
	cfg.Bucket = "test-bucket"
	cfg.AccessKeyID = "ak"
	cfg.AccessKeySecret = "sk"
	s, err := newOSS(cfg, func(c *oss.Config) {
		c.WithEndpoint(srv.URL).WithUsePathStyle(true)
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, fake
}

func TestOSSPutSeekable(t *testing.T) {
	s, fake := newTestOSS(t, OSSConfig{Domain: "https://cdn.test/"})
	ctx := context.Background()
	key := "images/2026/10/21/a.png"

	obj, err := s.Put(ctx, key, bytes.NewReader([]byte("hello")), upload.PutOptions{ContentType: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	if obj.URL != "https://cdn.test/"+key || obj.Size != 5 {
		t.Errorf("obj = %+v", obj)
	}
	if fake.putCount != 1 || string(fake.objects[key]) != "hello" || fake.contentTypes[key] != "image/png" {
		t.Errorf("putCount = %d, data = %q, contentType = %q", fake.putCount, fake.objects[key], fake.contentTypes[key])
	}
}

// Uploader 传入的是 io.MultiReader，长度未知，走分片上传
func TestOSSPutStream(t *testing.T) {
	s, fake := newTestOSS(t, OSSConfig{})
	ctx := context.Background()
	key := "files/a.bin"
	data := bytes.Repeat([]byte("0123456789"), int(oss.DefaultPartSize*2/10+1)) // 超过 2 个分片

	obj, err := s.Put(ctx, key, io.MultiReader(bytes.NewReader(data)), upload.PutOptions{ContentType: "application/octet-stream"})
	if err != nil {
		t.Fatal(err)
	}
	if obj.Size != int64(len(data)) {
		t.Errorf("size = %d, want %d", obj.Size, len(data))
	}
	if obj.URL != "https://test-bucket.oss-cn-hangzhou.aliyuncs.com/"+key {
		t.Errorf("url = %q", obj.URL)
	}
	if fake.putCount != 0 || !bytes.Equal(fake.objects[key], data) {
		t.Errorf("putCount = %d, stored %d bytes", fake.putCount, len(fake.objects[key]))
	}
}

func TestOSSPutReadError(t *testing.T) {
	s, fake := newTestOSS(t, OSSConfig{})
	if _, err := s.Put(context.Background(), "files/a.txt", &failReader{n: 1}, upload.PutOptions{}); err == nil {
		t.Fatal("want error")
	}
	if len(fake.objects) != 0 || len(fake.parts) != 0 {
		t.Errorf("objects = %d, pending uploads = %d", len(fake.objects), len(fake.parts))
	}
}

func TestOSSGetExistsDelete(t *testing.T) {
	s, fake := newTestOSS(t, OSSConfig{})
	ctx := context.Background()
	fake.objects["a.txt"] = []byte("hello")
	fake.objects["b.txt"] = []byte("world")

	rc, err := s.Get(ctx, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	if string(data) != "hello" {
		t.Errorf("data = %q", data)
	}

	if ok, err := s.Exists(ctx, "a.txt"); err != nil || !ok {
		t.Errorf("exists = %v, %v", ok, err)
	}
	if ok, err := s.Exists(ctx, "missing.txt"); err != nil || ok {
		t.Errorf("missing exists = %v, %v", ok, err)
	}
	if _, err := s.Get(ctx, "missing.txt"); !errors.Is(err, upload.ErrNotFound) {
		t.Errorf("err = %v", err)
	}

	if err := s.Delete(ctx, "a.txt", "b.txt"); err != nil {
		t.Fatal(err)
	}
	if len(fake.objects) != 0 {
		t.Errorf("objects left: %d", len(fake.objects))
	}
}

func TestOSSPrivateURL(t *testing.T) {
	s, _ := newTestOSS(t, OSSConfig{Private: true})
	url, err := s.URL(context.Background(), "images/a.png")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(url, "/test-bucket/images/a.png?") || !strings.Contains(url, "x-oss-signature=") {
		t.Errorf("url = %q", url)
	}
}
