package http

// fetest_test —— 前端测试转发：保真 / 上游失败 / 端口未配置。

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// upstreamPort 从 httptest.Server 的 URL 取端口。
func upstreamPort(t *testing.T, rawURL string) int {
	t.Helper()
	idx := strings.LastIndex(rawURL, ":")
	if idx < 0 {
		t.Fatalf("URL 无端口: %s", rawURL)
	}
	port, err := strconv.Atoi(rawURL[idx+1:])
	if err != nil {
		t.Fatalf("URL 端口非法: %s", rawURL)
	}
	return port
}

// freePort 取一个当前空闲的回环端口（监听后立即释放）。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("取空闲端口失败: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// TestFrontendTestProxyPassThrough 转发保真——上游 body 原样回传，Content-Type application/json。
func TestFrontendTestProxyPassThrough(t *testing.T) {
	const upstreamBody = `{"ok":true,"code":0,"stdout":"fixed-body","stderr":""}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("上游方法 = %q", r.Method)
		}
		if r.URL.Path != "/api/test/unit" {
			t.Errorf("上游路径 = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(upstreamBody))
	}))
	defer upstream.Close()

	s := New(Options{FrontendTestPort: upstreamPort(t, upstream.URL)})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/frontend-test", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	if got := rec.Body.String(); got != upstreamBody {
		t.Fatalf("body = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
}

// TestFrontendTestProxyUpstream500 上游 500 也回 200 + 原样 body——
// C# Results.Text 不携带上游状态码，Vitest 失败时端点仍是 200。
func TestFrontendTestProxyUpstream500(t *testing.T) {
	const upstreamBody = `{"ok":false,"code":1,"stdout":"","stderr":"1 failed"}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(upstreamBody))
	}))
	defer upstream.Close()

	s := New(Options{FrontendTestPort: upstreamPort(t, upstream.URL)})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/frontend-test", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d（应恒 200）", rec.Code)
	}
	if got := rec.Body.String(); got != upstreamBody {
		t.Fatalf("body = %q", got)
	}
}

// TestFrontendTestProxyFailure 上游不可达——200 + ok:false + 非空 error。
func TestFrontendTestProxyFailure(t *testing.T) {
	s := New(Options{FrontendTestPort: freePort(t)})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/frontend-test", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	var payload struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("回执非 JSON: %v（body=%q）", err, rec.Body.String())
	}
	if payload.OK {
		t.Fatal("ok 应为 false")
	}
	if payload.Error == "" {
		t.Fatal("error 不应为空")
	}
}

// TestFrontendTestPortUnset 端口未配置——回 ok:false，不发上游请求。
func TestFrontendTestPortUnset(t *testing.T) {
	s := New(Options{})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/frontend-test", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	if got := rec.Body.String(); got != `{"ok":false,"error":"frontend test port not configured"}` {
		t.Fatalf("body = %q", got)
	}
}
