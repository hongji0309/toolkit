package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"

	"github.com/hongji0309/toolkit/upload"
)

var _ upload.Storage = (*OSS)(nil)

// deleteBatchSize OSS 批量删除单次最多 1000 个
const deleteBatchSize = 1000

// OSSConfig 阿里云 OSS 配置
type OSSConfig struct {
	// Region 地域，如 cn-hangzhou，V2 SDK 使用 V4 签名，必填
	Region string
	// Endpoint 可选，为空时按 Region 生成 oss-{Region}.aliyuncs.com
	Endpoint string
	// Internal 使用内网地址 oss-{Region}-internal.aliyuncs.com，服务部署在同地域 ECS 时可节省流量费；Endpoint 不为空时忽略
	Internal        bool
	Bucket          string
	AccessKeyID     string
	AccessKeySecret string
	// Domain 访问域名，如绑定的 CDN 域名 https://cdn.example.com；为空时使用 https://{Bucket}.oss-{Region}.aliyuncs.com
	Domain string
	// Private 私有 bucket，URL 返回带签名的临时地址；签名地址使用 Endpoint 生成，不使用 Domain
	Private bool
	// SignExpire 签名地址有效期，小于等于 0 时为 1 小时
	SignExpire time.Duration
}

// OSS 基于阿里云 OSS 的文件存储
type OSS struct {
	client     *oss.Client
	uploader   *oss.Uploader
	bucket     string
	domain     string
	private    bool
	signExpire time.Duration
}

func NewOSS(cfg OSSConfig) (*OSS, error) {
	return newOSS(cfg, nil)
}

// newOSS configure 用于测试时调整 SDK 配置，如指向本地模拟服务
func newOSS(cfg OSSConfig, configure func(*oss.Config)) (*OSS, error) {
	if cfg.Region == "" || cfg.Bucket == "" {
		return nil, errors.New("storage: oss region and bucket are required")
	}

	ossCfg := oss.LoadDefaultConfig().
		WithRegion(cfg.Region).
		WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.AccessKeySecret))
	if cfg.Endpoint != "" {
		ossCfg = ossCfg.WithEndpoint(cfg.Endpoint)
	} else if cfg.Internal {
		ossCfg = ossCfg.WithUseInternalEndpoint(true)
	}
	if configure != nil {
		configure(ossCfg)
	}
	client := oss.NewClient(ossCfg)

	domain := strings.TrimRight(cfg.Domain, "/")
	if domain == "" {
		domain = "https://" + cfg.Bucket + ".oss-" + cfg.Region + ".aliyuncs.com"
	}
	signExpire := cfg.SignExpire
	if signExpire <= 0 {
		signExpire = time.Hour
	}
	return &OSS{
		client:     client,
		uploader:   client.NewUploader(),
		bucket:     cfg.Bucket,
		domain:     domain,
		private:    cfg.Private,
		signExpire: signExpire,
	}, nil
}

// Put 长度已知（可 Seek）且小于分片大小时用 PutObject 一次上传，否则由 SDK 分片流式上传，不需要先落盘
func (s *OSS) Put(ctx context.Context, key string, r io.Reader, opts upload.PutOptions) (*upload.Object, error) {
	request := &oss.PutObjectRequest{
		Bucket: oss.Ptr(s.bucket),
		Key:    oss.Ptr(key),
	}
	if opts.ContentType != "" {
		request.ContentType = oss.Ptr(opts.ContentType)
	}

	body, size, err := measure(r)
	if err != nil {
		return nil, err
	}
	if _, err := s.uploader.UploadFrom(ctx, request, body); err != nil {
		return nil, err
	}

	url, err := s.URL(ctx, key)
	if err != nil {
		return nil, err
	}
	return &upload.Object{
		Key:         key,
		URL:         url,
		Size:        size(),
		ContentType: opts.ContentType,
	}, nil
}

// Get 文件不存在时返回 upload.ErrNotFound
func (s *OSS) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	result, err := s.client.GetObject(ctx, &oss.GetObjectRequest{
		Bucket: oss.Ptr(s.bucket),
		Key:    oss.Ptr(key),
	})
	if isNotFound(err) {
		return nil, upload.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return result.Body, nil
}

func (s *OSS) Delete(ctx context.Context, keys ...string) error {
	for len(keys) > 0 {
		n := min(len(keys), deleteBatchSize)
		objects := make([]oss.DeleteObject, n)
		for i, key := range keys[:n] {
			objects[i] = oss.DeleteObject{Key: oss.Ptr(key)}
		}
		if _, err := s.client.DeleteMultipleObjects(ctx, &oss.DeleteMultipleObjectsRequest{
			Bucket:  oss.Ptr(s.bucket),
			Objects: objects,
			Quiet:   true,
		}); err != nil {
			return err
		}
		keys = keys[n:]
	}
	return nil
}

func (s *OSS) Exists(ctx context.Context, key string) (bool, error) {
	return s.client.IsObjectExist(ctx, s.bucket, key)
}

func (s *OSS) URL(ctx context.Context, key string) (string, error) {
	if !s.private {
		return s.domain + "/" + key, nil
	}
	result, err := s.client.Presign(ctx, &oss.GetObjectRequest{
		Bucket: oss.Ptr(s.bucket),
		Key:    oss.Ptr(key),
	}, oss.PresignExpires(s.signExpire))
	if err != nil {
		return "", err
	}
	return result.URL, nil
}

func isNotFound(err error) bool {
	var serviceErr *oss.ServiceError
	return errors.As(err, &serviceErr) && serviceErr.StatusCode == http.StatusNotFound
}

// measure 返回上传用的 reader 和获取已上传字节数的函数
// 可 Seek 的 reader 原样返回，SDK 据此得知长度；否则包一层计数
func measure(r io.Reader) (io.Reader, func() int64, error) {
	if seeker, ok := r.(io.Seeker); ok {
		cur, err := seeker.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, nil, err
		}
		end, err := seeker.Seek(0, io.SeekEnd)
		if err != nil {
			return nil, nil, err
		}
		if _, err := seeker.Seek(cur, io.SeekStart); err != nil {
			return nil, nil, err
		}
		return r, func() int64 { return end - cur }, nil
	}
	cr := &countingReader{r: r}
	return cr, func() int64 { return cr.n }, nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
