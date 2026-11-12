package bricks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPRequestSpec 是 http-* 三件的入参——积木层解析工具参数后填装，服务层只认本结构。
//
// 对齐 C# Mau.Runtime.HttpRequestSpec（字段语义逐一对应）。
type HTTPRequestSpec struct {
	// URL 目标地址（http / https 绝对地址）
	URL string
	// Method 请求方法（GET / POST）
	Method string
	// Body 请求正文（POST 用；GET 忽略）
	Body string
	// ContentType 请求正文类型（空 = text/plain; charset=utf-8）
	ContentType string
	// Headers 附加请求头——每行一条 `Name: value`（空行忽略）
	Headers string
	// TimeoutMs 超时毫秒（1..120000）
	TimeoutMs int
	// Follow 是否自动跟随重定向（true = 跟到终点并报最终地址）
	Follow bool
	// MaxBody 正文最大字符数（0 = 不取正文，只取响应头）
	MaxBody int
}

// HTTPFacts 是 HTTP 响应事实——状态 / 头 / 正文 / 耗时 / 重定向终点。
//
// 连不上（DNS / 拒绝 / 超时）也走本结构（Unreachable + Failure）——让积木层统一出文案，
// 服务层不抛业务异常（error 只留给参数面）。
type HTTPFacts struct {
	// URL 请求地址（原样回显）
	URL string
	// FinalURL 最终地址（跟随重定向后；未跟随 = 原地址）
	FinalURL string
	// Method 请求方法
	Method string
	// Unreachable 是否连不上
	Unreachable bool
	// Failure 连不上的原因（可达时为空串）
	Failure string
	// Status HTTP 状态码（0 = 未拿到响应）
	Status int
	// ReasonPhrase 状态短语（如 OK / Not Found）
	ReasonPhrase string
	// ContentType 响应 Content-Type（原样）
	ContentType string
	// ContentLength 响应体字节数（-1 = 未声明）
	ContentLength int64
	// Body 正文（已按 MaxBody 截断；MaxBody=0 时为空串）
	Body string
	// BodyChars 正文总字符数（UTF-16 码元——对齐 C# string.Length；截断前）
	BodyChars int
	// Truncated 正文是否被截断
	Truncated bool
	// ElapsedMs 耗时毫秒
	ElapsedMs int64
	// Location Location 响应头（重定向目标；无则空串）
	Location string
	// Redirected 是否发生了重定向（最终地址与原地址不同）
	Redirected bool
	// UsedProxy 本次请求是否经系统代理
	UsedProxy bool
}

// HTTPSend 发送一次 HTTP 请求并取回响应事实。
//
// 返回的 error 只表达参数面非法（对齐 C# InvalidDataException）——网络故障归 Unreachable。
func HTTPSend(spec HTTPRequestSpec) (HTTPFacts, error) {
	if err := validateHTTPSpec(spec); err != nil {
		return HTTPFacts{}, err
	}
	req, err := buildHTTPRequest(spec)
	if err != nil {
		return HTTPFacts{}, err
	}
	facts := HTTPFacts{URL: spec.URL, Method: spec.Method, FinalURL: spec.URL, ContentLength: -1}
	facts.UsedProxy = proxyApplies(spec.URL)
	client := &http.Client{
		Timeout:   time.Duration(spec.TimeoutMs) * time.Millisecond,
		Transport: newHTTPTransport(),
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if spec.Follow {
				return nil
			}
			// 不跟随——把最后一跳响应原样交给调用方（Location 头可见）
			return http.ErrUseLastResponse
		},
	}
	start := time.Now()
	resp, err := client.Do(req)
	facts.ElapsedMs = time.Since(start).Milliseconds()
	if err != nil {
		facts.Unreachable = true
		facts.Failure = httpFailureText(err, spec.TimeoutMs)
		return facts, nil
	}
	defer func() { _ = resp.Body.Close() }()
	facts.Status = resp.StatusCode
	facts.ReasonPhrase = httpReasonPhrase(resp.Status, resp.StatusCode)
	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		facts.ContentType = contentType
	}
	facts.ContentLength = resp.ContentLength
	if resp.Request != nil && resp.Request.URL != nil {
		facts.FinalURL = resp.Request.URL.String()
	}
	facts.Redirected = facts.FinalURL != spec.URL
	if location := resp.Header.Get("Location"); location != "" {
		if parsed, parseErr := resp.Request.URL.Parse(location); parseErr == nil {
			facts.Location = parsed.String()
		} else {
			facts.Location = location
		}
	}
	if spec.MaxBody > 0 {
		raw, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			// 读正文阶段的失败与「连不上」同口径——回执要给调用方一条明确原因
			facts.Unreachable = true
			facts.Failure = "连接失败：" + readErr.Error()
			return facts, nil
		}
		text := string(raw)
		facts.BodyChars = utf16Units(text)
		if facts.BodyChars > spec.MaxBody {
			facts.Body = truncateUTF16Units(text, spec.MaxBody)
			facts.Truncated = true
		} else {
			facts.Body = text
		}
	}
	return facts, nil
}

// validateHTTPSpec 参数面校验——非法即返回错误（不回落默认值）。
func validateHTTPSpec(spec HTTPRequestSpec) error {
	if spec.URL == "" {
		return errors.New("缺少参数 url")
	}
	lowered := strings.ToLower(spec.URL)
	if !strings.HasPrefix(lowered, "http://") && !strings.HasPrefix(lowered, "https://") {
		return errors.New("url 须为 http:// 或 https:// 绝对地址: " + spec.URL)
	}
	if spec.TimeoutMs < 1 || spec.TimeoutMs > 120000 {
		return fmt.Errorf("timeout_ms 须在 1..120000 之间: %d", spec.TimeoutMs)
	}
	if spec.MaxBody < 0 {
		return fmt.Errorf("max_body 不得为负: %d", spec.MaxBody)
	}
	if spec.Method != "GET" && spec.Method != "POST" {
		return errors.New("method 只支持 GET / POST: " + spec.Method)
	}
	return nil
}

// buildHTTPRequest 构造请求消息——正文与请求头解析都在这里（非法即错误，不静默丢头）。
func buildHTTPRequest(spec HTTPRequestSpec) (*http.Request, error) {
	var body io.Reader
	if spec.Body != "" && spec.Method != "GET" {
		body = strings.NewReader(spec.Body)
	}
	req, err := http.NewRequest(spec.Method, spec.URL, body)
	if err != nil {
		return nil, errors.New("url 无法解析: " + spec.URL + "（" + err.Error() + "）")
	}
	if body != nil {
		contentType := spec.ContentType
		if contentType == "" {
			contentType = "text/plain; charset=utf-8"
		}
		req.Header.Set("Content-Type", contentType)
	}
	if err := applyHTTPHeaders(req, spec.Headers); err != nil {
		return nil, err
	}
	return req, nil
}

// applyHTTPHeaders 附加请求头——每行一条 `Name: value`；格式非法即错误（不静默丢头）。
//
// Content-Type / Content-Length 特殊处置与 C# 同口径：前者要求本次请求有正文，后者一律拒绝。
func applyHTTPHeaders(req *http.Request, headers string) error {
	if headers == "" {
		return nil
	}
	normalized := strings.ReplaceAll(headers, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	for _, rawLine := range strings.Split(normalized, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			return errors.New("请求头格式非法（须为每行 `Name: value`）: " + line)
		}
		name := strings.TrimSpace(line[:colon])
		value := strings.TrimSpace(line[colon+1:])
		if name == "" {
			return errors.New("请求头名称为空: " + line)
		}
		switch {
		case strings.EqualFold(name, "Content-Type"):
			if req.Body == nil || req.Body == http.NoBody {
				return errors.New("本次请求没有正文，Content-Type 无意义（GET 无正文；POST 请用 content_type 参数）: " + line)
			}
			req.Header.Set("Content-Type", value)
		case strings.EqualFold(name, "Content-Length"):
			return errors.New("Content-Length 由请求体自动计算——不要手工指定: " + line)
		default:
			req.Header.Add(name, value)
		}
	}
	return nil
}

// httpFailureText 连不上原因的文案——超时单独成句，其余归「连接失败」。
func httpFailureText(err error, timeoutMs int) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("超时（%d ms 内未完成）", timeoutMs)
	}
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Sprintf("超时（%d ms 内未完成）", timeoutMs)
	}
	message := err.Error()
	if urlErr, ok := err.(*url.Error); ok && urlErr.Err != nil {
		message = urlErr.Err.Error()
	}
	return "连接失败：" + message
}

// httpReasonPhrase 从 `200 OK` 形态的状态行取短语；无短语返回空串。
func httpReasonPhrase(statusLine string, statusCode int) string {
	prefix := fmt.Sprintf("%d ", statusCode)
	if strings.HasPrefix(statusLine, prefix) {
		return statusLine[len(prefix):]
	}
	if index := strings.IndexByte(statusLine, ' '); index >= 0 {
		return statusLine[index+1:]
	}
	return ""
}

// utf16Units 字符串的 UTF-16 码元数——对齐 C# string.Length 口径。
func utf16Units(text string) int {
	units := 0
	for _, r := range text {
		if r > 0xFFFF {
			units += 2
			continue
		}
		units++
	}
	return units
}

// truncateUTF16Units 按 UTF-16 码元截断——截断点落在代理对中间时以替换符收尾。
func truncateUTF16Units(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	var b strings.Builder
	units := 0
	for _, r := range text {
		width := 1
		if r > 0xFFFF {
			width = 2
		}
		if units+width > limit {
			return b.String()
		}
		b.WriteRune(r)
		units += width
	}
	return b.String()
}

// newHTTPTransport 每次调用新建传输层——Follow / 代理逐次不同，复用收益小于状态串味风险。
func newHTTPTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 proxyForURL,
		MaxIdleConns:          4,
		IdleConnTimeout:       15 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 0,
	}
}

// proxyApplies 判定目标地址是否经系统代理（回执的 UsedProxy 字段）。
func proxyApplies(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	proxy, err := proxyForURL(&http.Request{URL: parsed})
	return err == nil && proxy != nil
}
