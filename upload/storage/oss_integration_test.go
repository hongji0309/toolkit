package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/hongji0309/toolkit/upload"
)

// 集成测试连接真实 OSS，读取项目根目录的 oss-test.yaml（可用 OSS_TEST_CONFIG 指定路径），文件不存在时跳过
// 只读写 bucket 中 toolkit-it/ 前缀下的文件，测试结束后删除

type ossTestConfig struct {
	Region          string `yaml:"region"`
	Bucket          string `yaml:"bucket"`
	AccessKeyID     string `yaml:"access_key_id"`
	AccessKeySecret string `yaml:"access_key_secret"`
	Private         bool   `yaml:"private"`
	Domain          string `yaml:"domain"`
	Endpoint        string `yaml:"endpoint"`
}

func (c ossTestConfig) ossConfig() OSSConfig {
	return OSSConfig{
		Region:          c.Region,
		Endpoint:        c.Endpoint,
		Bucket:          c.Bucket,
		AccessKeyID:     c.AccessKeyID,
		AccessKeySecret: c.AccessKeySecret,
		Domain:          c.Domain,
		Private:         c.Private,
	}
}

func loadOSSTestConfig(t *testing.T) ossTestConfig {
	t.Helper()
	name := os.Getenv("OSS_TEST_CONFIG")
	if name == "" {
		name = filepath.Join("..", "..", "oss-test.yaml")
	}
	data, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("未找到 %s，跳过 OSS 集成测试", name)
	}
	if err != nil {
		t.Fatal(err)
	}
	var cfg ossTestConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Region == "" || cfg.Bucket == "" || cfg.AccessKeyID == "" || cfg.AccessKeySecret == "" {
		t.Skip("oss-test.yaml 缺少 region、bucket 或 AccessKey，跳过 OSS 集成测试")
	}
	return cfg
}

// integrationOSS 返回连接真实 OSS 的存储，并在测试结束后删除 keys 中记录的文件
func integrationOSS(t *testing.T, cfg OSSConfig) (*OSS, *[]string) {
	t.Helper()
	s, err := NewOSS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.Delete(ctx, keys...); err != nil {
			t.Errorf("清理测试文件失败: %v", err)
		}
	})
	return s, &keys
}

// pngData PNG 文件头，足够被识别为 image/png
var pngData = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 100)...)

func integrationDir(name string) string {
	return "toolkit-it/" + time.Now().Format("20060102-150405") + "/" + name
}

func readAll(t *testing.T, s upload.Storage, key string) []byte {
	t.Helper()
	rc, err := s.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// fetch 通过 HTTP 访问 URL，返回状态码和内容
func fetch(t *testing.T, url string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func TestOSSIntegrationImage(t *testing.T) {
	cfg := loadOSSTestConfig(t)
	s, keys := integrationOSS(t, cfg.ossConfig())
	ctx := context.Background()

	opts := upload.ImageOptions
	opts.Dir = integrationDir("images")
	u := upload.NewUploader(s, opts)

	obj, err := u.Upload(ctx, "photo.png", bytes.NewReader(pngData))
	if err != nil {
		t.Fatal(err)
	}
	*keys = append(*keys, obj.Key)
	t.Logf("key = %s, url = %s", obj.Key, obj.URL)

	if obj.ContentType != "image/png" || obj.Size != int64(len(pngData)) {
		t.Errorf("obj = %+v", obj)
	}
	if ok, err := s.Exists(ctx, obj.Key); err != nil || !ok {
		t.Fatalf("exists = %v, %v", ok, err)
	}
	if !bytes.Equal(readAll(t, s, obj.Key), pngData) {
		t.Error("下载内容与上传内容不一致")
	}

	// 返回的 URL 应能直接访问：公共读 bucket 为直链，私有 bucket 为签名地址
	status, data := fetch(t, obj.URL)
	if status != http.StatusOK || !bytes.Equal(data, pngData) {
		t.Errorf("访问 URL 返回 %d，内容一致 = %v；公共读 bucket 请确认读写权限为公共读，私有 bucket 请将 private 设为 true", status, bytes.Equal(data, pngData))
	}
}

// 超过分片大小的流走分片上传
func TestOSSIntegrationMultipart(t *testing.T) {
	cfg := loadOSSTestConfig(t)
	s, keys := integrationOSS(t, cfg.ossConfig())
	ctx := context.Background()

	data := make([]byte, 13<<20) // 13MB，3 个分片
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	opts := upload.FileOptions
	opts.Dir = integrationDir("files")
	u := upload.NewUploader(s, opts)

	start := time.Now()
	obj, err := u.Upload(ctx, "big.bin", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	*keys = append(*keys, obj.Key)
	t.Logf("上传 %d MB 用时 %v，key = %s", len(data)>>20, time.Since(start).Round(time.Millisecond), obj.Key)

	if obj.Size != int64(len(data)) {
		t.Errorf("size = %d, want %d", obj.Size, len(data))
	}
	if !bytes.Equal(readAll(t, s, obj.Key), data) {
		t.Error("下载内容与上传内容不一致")
	}
}

// 直接调用 Storage.Put，可 Seek 的 reader 走一次 PutObject
func TestOSSIntegrationPutSeekable(t *testing.T) {
	cfg := loadOSSTestConfig(t)
	s, keys := integrationOSS(t, cfg.ossConfig())
	ctx := context.Background()

	key := integrationDir("seekable") + "/hello.txt"
	*keys = append(*keys, key)
	obj, err := s.Put(ctx, key, bytes.NewReader([]byte("hello oss")), upload.PutOptions{ContentType: "text/plain"})
	if err != nil {
		t.Fatal(err)
	}
	if obj.Size != 9 {
		t.Errorf("size = %d", obj.Size)
	}
	if got := readAll(t, s, key); string(got) != "hello oss" {
		t.Errorf("data = %q", got)
	}
}

// 无论 bucket 是否公共读，签名地址都应能访问
func TestOSSIntegrationSignedURL(t *testing.T) {
	cfg := loadOSSTestConfig(t)
	signedCfg := cfg.ossConfig()
	signedCfg.Private = true
	signedCfg.SignExpire = time.Minute
	s, keys := integrationOSS(t, signedCfg)
	ctx := context.Background()

	key := integrationDir("signed") + "/hello.txt"
	*keys = append(*keys, key)
	obj, err := s.Put(ctx, key, bytes.NewReader([]byte("signed")), upload.PutOptions{ContentType: "text/plain"})
	if err != nil {
		t.Fatal(err)
	}
	status, data := fetch(t, obj.URL)
	if status != http.StatusOK || string(data) != "signed" {
		t.Errorf("访问签名地址返回 %d: %s", status, data)
	}
}

func TestOSSIntegrationTooLarge(t *testing.T) {
	cfg := loadOSSTestConfig(t)
	s, _ := integrationOSS(t, cfg.ossConfig())

	opts := upload.FileOptions
	opts.Dir = integrationDir("too-large")
	opts.MaxSize = 7 << 20 // 大于一个分片，确保已开始分片上传后再超限
	u := upload.NewUploader(s, opts)

	_, err := u.Upload(context.Background(), "big.bin", bytes.NewReader(make([]byte, 8<<20)))
	if !errors.Is(err, upload.ErrFileTooLarge) {
		t.Errorf("err = %v", err)
	}
}

func TestOSSIntegrationNotFoundAndDelete(t *testing.T) {
	cfg := loadOSSTestConfig(t)
	s, _ := integrationOSS(t, cfg.ossConfig())
	ctx := context.Background()

	key := integrationDir("delete") + "/a.txt"
	if _, err := s.Put(ctx, key, bytes.NewReader([]byte("x")), upload.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Exists(ctx, key); err != nil || ok {
		t.Errorf("删除后 exists = %v, %v", ok, err)
	}
	if _, err := s.Get(ctx, key); !errors.Is(err, upload.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	// 删除不存在的文件不报错
	if err := s.Delete(ctx, key); err != nil {
		t.Errorf("重复删除: %v", err)
	}
}
