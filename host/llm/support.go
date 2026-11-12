package llm

import (
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Logf 是 LLM 面日志出口——装配层注入（默认丢弃；与 C# LogStore.Add 同字段面）。
var Logf func(level int, message string)

// logAt 写一条日志（未接线 = 丢弃——日志面不阻断请求）。
func logAt(level int, message string) {
	if Logf != nil {
		Logf(level, message)
	}
}

// itoa 整数转字符串。
func itoa(v int) string { return fmt.Sprintf("%d", v) }

// 图片内联上限——32 MiB（视觉 API 通用上限）。
const maxInlineImageBytes = 32 * 1024 * 1024

// 外部图片 URL 长度上限——超长改走本地路径。
const maxImageURLLength = 8192

// 本机抓取超时。
const imageFetchTimeout = 15 * time.Second

// imageFetchClient 本机抓取客户端（回环 / 私网图片）。
var imageFetchClient = &http.Client{Timeout: imageFetchTimeout}

// ResolveImagePayload 构建 image_url 串——本地路径 / 回环与私网 URL 内联 data URL；公网 URL 直传。
//
// 返回（url, 空串）成功；（空串, ERR|...）失败——失败原文进 text 块，不阻断请求。
func ResolveImagePayload(imagePath string) (string, string) {
	if strings.TrimSpace(imagePath) == "" {
		return "", "ERR|BAD_ARGS|缺少图片路径"
	}
	trimmed := strings.TrimSpace(imagePath)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		if len(trimmed) > maxImageURLLength {
			return "", "ERR|IMAGE_URL_TOO_LONG|外部图片 URL 超长（>" + itoa(maxImageURLLength) + " 字符——请改用本地路径）"
		}
		parsed, err := url.Parse(trimmed)
		if err != nil || parsed.Host == "" {
			return "", "ERR|IMAGE_URL_INVALID|图片 URL 无法解析: " + trimmed
		}
		if !isLocalOnlyHost(parsed) {
			return trimmed, ""
		}
		return fetchLocalImage(trimmed)
	}
	info, err := os.Stat(trimmed)
	if err != nil || info.IsDir() {
		return "", "ERR|IMAGE_NOT_FOUND|图片文件不存在: " + trimmed
	}
	if info.Size() > maxInlineImageBytes {
		return "", "ERR|IMAGE_TOO_LARGE|图片超过 32MiB 内联上限（" + itoa(int(info.Size())) + " 字节——请压缩或改用 Files API）"
	}
	raw, err := os.ReadFile(trimmed)
	if err != nil {
		return "", "ERR|IMAGE_READ|图片读取失败: " + err.Error()
	}
	mime := SniffImageMIME(raw)
	if mime == "" {
		mime = GuessImageMIME(trimmed)
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), ""
}

// isLocalOnlyHost 判定 URL 主机是否只在本机可达（回环 / 私网 / 链路本地）。
func isLocalOnlyHost(parsed *url.URL) bool {
	host := parsed.Hostname()
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return true
	}
	address := net.ParseIP(host)
	if address == nil {
		return false
	}
	if v4 := address.To4(); v4 != nil {
		return v4[0] == 10 ||
			(v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31) ||
			(v4[0] == 192 && v4[1] == 168) ||
			(v4[0] == 169 && v4[1] == 254) ||
			v4[0] == 127
	}
	if address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() {
		return true
	}
	return len(address) == net.IPv6len && (address[0]&0xFE) == 0xFC
}

// fetchLocalImage 本机抓取回环 / 私网图片并内联为 data URL——失败具名报错，不回落直传。
func fetchLocalImage(target string) (string, string) {
	resp, err := imageFetchClient.Get(target)
	if err != nil {
		return "", "ERR|IMAGE_FETCH|本机抓取图片异常: " + err.Error() + "（" + target + "）"
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "ERR|IMAGE_FETCH|本机抓取图片失败（HTTP " + itoa(resp.StatusCode) + "）: " + target
	}
	limited := io.LimitReader(resp.Body, maxInlineImageBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return "", "ERR|IMAGE_FETCH|本机抓取图片异常: " + err.Error() + "（" + target + "）"
	}
	if len(raw) == 0 {
		return "", "ERR|IMAGE_FETCH|本机抓取图片返回空内容: " + target
	}
	if len(raw) > maxInlineImageBytes {
		return "", "ERR|IMAGE_TOO_LARGE|本机抓取到的图片超过 32MiB 内联上限: " + target
	}
	mime := SniffImageMIME(raw)
	if mime == "" {
		mime = normalizeMediaType(resp.Header.Get("Content-Type"))
	}
	if mime == "" {
		return "", "ERR|IMAGE_MIME|本机抓取到的内容不是可识别的图片（" + target + "）"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), ""
}

// normalizeMediaType 响应头 media type 归一——只接受 image/* 前缀。
func normalizeMediaType(value string) string {
	trimmed := strings.TrimSpace(value)
	if idx := strings.Index(trimmed, ";"); idx >= 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "image/") {
		return strings.ToLower(trimmed)
	}
	return ""
}

// SniffImageMIME 图片魔数判定——优先于扩展名与响应头。
func SniffImageMIME(raw []byte) string {
	if len(raw) < 12 {
		return ""
	}
	switch {
	case raw[0] == 0x89 && raw[1] == 0x50 && raw[2] == 0x4E && raw[3] == 0x47:
		return "image/png"
	case raw[0] == 0xFF && raw[1] == 0xD8 && raw[2] == 0xFF:
		return "image/jpeg"
	case raw[0] == 0x47 && raw[1] == 0x49 && raw[2] == 0x46 && raw[3] == 0x38:
		return "image/gif"
	case raw[0] == 0x52 && raw[1] == 0x49 && raw[2] == 0x46 && raw[3] == 0x46 &&
		raw[8] == 0x57 && raw[9] == 0x45 && raw[10] == 0x42 && raw[11] == 0x50:
		return "image/webp"
	case raw[0] == 0x42 && raw[1] == 0x4D:
		return "image/bmp"
	}
	return ""
}

// GuessImageMIME 扩展名推断——不可识别回落 image/jpeg（官方口径：格式由内容判断）。
func GuessImageMIME(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return "image/jpeg"
}
