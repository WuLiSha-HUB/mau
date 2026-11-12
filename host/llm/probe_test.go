package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestProbeEndpoint_Success 哨兵词命中——请求头与请求体逐字对齐 C# BuildProbeBody。
func TestProbeEndpoint_Success(t *testing.T) {
	var gotBody, gotContentType, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"PONG"}}]}`))
	}))
	defer server.Close()
	if !ProbeEndpoint(context.Background(), server.URL+"/v1/chat/completions", "sk-p", "m1") {
		t.Fatalf("期望探针通过")
	}
	if gotAuth != "Bearer sk-p" {
		t.Fatalf("Authorization 头: %s", gotAuth)
	}
	if gotContentType != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type 头: %s", gotContentType)
	}
	for _, want := range []string{`"stream":false`, `"max_tokens":8`, "Reply with exactly one word: PONG", `"model":"m1"`} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("请求体缺少 %q：%s", want, gotBody)
		}
	}
}

// TestProbeEndpoint_Failures 失败面——非 2xx / 非 JSON / 缺哨兵词 / 空 choices / 空端点。
func TestProbeEndpoint_Failures(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"non2xx", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }},
		{"badJSON", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("oops")) }},
		{"noToken", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hello"}}]}`))
		}},
		{"noChoices", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"choices":[]}`)) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.handler)
			defer server.Close()
			if ProbeEndpoint(context.Background(), server.URL, "", "") {
				t.Fatalf("期望探针失败（%s）", tc.name)
			}
		})
	}
	if ProbeEndpoint(context.Background(), "", "", "") {
		t.Fatalf("空端点期望失败")
	}
}

// TestProbeEndpoint_TimeoutOrCancel 超时 / 取消——一律 false 且按 ctx 及时退出。
func TestProbeEndpoint_TimeoutOrCancel(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer server.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if ProbeEndpoint(ctx, server.URL, "", "") {
		t.Fatalf("超时期望失败")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("探针未按 ctx 超时退出: %v", elapsed)
	}
}
