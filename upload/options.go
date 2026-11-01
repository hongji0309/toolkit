package upload

import (
	"path"
	"strings"
)

// Options 上传规则
type Options struct {
	// Dir 存储路径前缀，如 images、files
	Dir string
	// MaxSize 单个文件最大字节数，小于等于 0 时不限制
	MaxSize int64
	// AllowedTypes MIME 白名单，支持 image/* 通配；为空时允许除 HTML、XML 以外的所有类型
	AllowedTypes []string
}

// ImageOptions 图片上传，最大 10MB
var ImageOptions = Options{
	Dir:          "images",
	MaxSize:      10 << 20,
	AllowedTypes: []string{"image/jpeg", "image/png", "image/gif", "image/webp", "image/bmp"},
}

// FileOptions 普通文件上传，最大 100MB
var FileOptions = Options{
	Dir:     "files",
	MaxSize: 100 << 20,
}

// deniedTypes 浏览器会当作页面解析的类型，可能导致 XSS，始终禁止
// SVG 会被识别为 text/xml，也在此列
var deniedTypes = map[string]bool{
	"text/html": true,
	"text/xml":  true,
}

// deniedExts 即使内容类型合法，也不允许使用的扩展名，避免 Nginx 等按扩展名把文件当作页面或脚本
var deniedExts = map[string]bool{
	".html": true, ".htm": true, ".xhtml": true, ".shtml": true, ".svg": true, ".svgz": true, ".xml": true,
	".js": true, ".mjs": true, ".php": true, ".jsp": true, ".asp": true, ".aspx": true, ".cgi": true,
}

// typeExts 能可靠识别的类型使用固定扩展名，忽略用户传入的扩展名
var typeExts = map[string]string{
	"image/jpeg":      ".jpg",
	"image/png":       ".png",
	"image/gif":       ".gif",
	"image/webp":      ".webp",
	"image/bmp":       ".bmp",
	"application/pdf": ".pdf",
}

func (o Options) allowed(contentType string) bool {
	if deniedTypes[contentType] {
		return false
	}
	if len(o.AllowedTypes) == 0 {
		return true
	}
	for _, t := range o.AllowedTypes {
		if prefix, ok := strings.CutSuffix(t, "/*"); ok {
			if strings.HasPrefix(contentType, prefix+"/") {
				return true
			}
		} else if t == contentType {
			return true
		}
	}
	return false
}

// extension 优先按内容类型确定扩展名；无法确定时沿用原始文件名的扩展名，不安全或不规范时返回空
func extension(contentType, filename string) string {
	if ext, ok := typeExts[contentType]; ok {
		return ext
	}
	ext := strings.ToLower(path.Ext(strings.ReplaceAll(filename, "\\", "/")))
	if len(ext) < 2 || len(ext) > 10 || deniedExts[ext] {
		return ""
	}
	for _, c := range ext[1:] {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return ""
		}
	}
	return ext
}
