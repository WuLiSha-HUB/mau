package main

// fetch —— HTTP 抓取与证据落盘。

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// httpResult 是一次 HTTP 抓取结果。
type httpResult struct {
	status int
	header http.Header
	body   []byte
	err    error
	took   time.Duration
}

// ok 判定抓取成功（无连接错误）。
func (r httpResult) ok() bool { return r.err == nil }

// headerValue 取响应头首值。
func (r httpResult) headerValue(name string) string {
	if r.header == nil {
		return ""
	}
	return r.header.Get(name)
}

// contentTypeBase 取 Content-Type 主类型（去参数段）。
func (r httpResult) contentTypeBase() string {
	ct := r.headerValue("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.TrimSpace(ct)
}

// newHTTPClient 造短超时客户端（对拍请求都不该慢）。
func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// doGet 发起 GET 并读取全文。
func doGet(base, path string, timeout time.Duration) httpResult {
	client := newHTTPClient(timeout)
	start := time.Now()
	resp, err := client.Get(strings.TrimRight(base, "/") + path)
	if err != nil {
		return httpResult{err: err, took: time.Since(start)}
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(resp.Body)
	return httpResult{status: resp.StatusCode, header: resp.Header, body: body, err: readErr, took: time.Since(start)}
}

// doPost 发起 POST（body 原样）。
func doPost(base, path, body, contentType string, timeout time.Duration) httpResult {
	client := newHTTPClient(timeout)
	start := time.Now()
	resp, err := client.Post(strings.TrimRight(base, "/")+path, contentType, bytes.NewBufferString(body))
	if err != nil {
		return httpResult{err: err, took: time.Since(start)}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, readErr := io.ReadAll(resp.Body)
	return httpResult{status: resp.StatusCode, header: resp.Header, body: raw, err: readErr, took: time.Since(start)}
}

// doStream 打开 SSE 并读首发帧（读到空行为止；超时也算已取——流本就不关）。
func doStream(base, path string, wait time.Duration) (httpResult, string) {
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path, nil)
	if err != nil {
		return httpResult{err: err}, ""
	}
	client := newHTTPClient(wait + time.Second)
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return httpResult{err: err, took: time.Since(start)}, ""
	}
	defer func() { _ = resp.Body.Close() }()
	buf := make([]byte, 0, 4096)
	chunk := make([]byte, 512)
	// 上限 4MB——view 首帧是全量视图（152 块 ≈ 700KB），256KB 上限会截断 data 行。
	for len(buf) < 4*1024*1024 {
		n, readErr := resp.Body.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			// 首个空行出现即视为取到首帧
			if idx := bytes.Index(buf, []byte("\n\n")); idx >= 0 {
				buf = buf[:idx+2]
				break
			}
		}
		if readErr != nil {
			break
		}
	}
	return httpResult{status: resp.StatusCode, header: resp.Header, body: buf, took: time.Since(start)}, string(buf)
}

// saveRaw 落盘原始响应（证据）。
func saveRaw(outDir, side, name string, res httpResult) string {
	if outDir == "" {
		return ""
	}
	dir := filepath.Join(outDir, "raw", side)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	ext := ".bin"
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".txt") {
		ext = ""
	}
	path := filepath.Join(dir, name+ext)
	if res.err != nil && len(res.body) == 0 {
		_ = os.WriteFile(path+".err", []byte(res.err.Error()), 0o644)
		return path + ".err"
	}
	if err := os.WriteFile(path, res.body, 0o644); err != nil {
		return ""
	}
	return path
}

// saveNormalized 落盘归一化 JSON 证据。
func saveNormalized(outDir, side, name string, v *jval) string {
	if outDir == "" || v == nil {
		return ""
	}
	dir := filepath.Join(outDir, "norm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	path := filepath.Join(dir, side+"-"+name+".json")
	if err := os.WriteFile(path, []byte(dumpIndent(v)), 0o644); err != nil {
		return ""
	}
	return path
}

// describeStatus 给状态码与耗时的短文本。
func describeStatus(res httpResult) string {
	if res.err != nil {
		return "ERR(" + res.err.Error() + ")"
	}
	return fmt.Sprintf("%d/%dms", res.status, res.took.Milliseconds())
}
