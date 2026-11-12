package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"cathome5/host/session"
)

// newTestClient 建立指向测试服务器的客户端（端点经环境变量注入）。
func newTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	t.Setenv("MAU_LLM_BASE_URL", server.URL+"/v1")
	t.Setenv("MAU_LLM_API_KEY", "sk-test")
	client := &Client{Settings: func(string) string { return "" }, HTTP: server.Client()}
	return client
}

// collectClient 跑一次流并收集事件。
func collectClient(client *Client) []Event {
	var events []Event
	client.Stream(context.Background(), []session.Message{{Role: session.RoleUser, Content: "hi"}}, nil, "cat", func(ev Event) {
		events = append(events, ev)
	})
	return events
}

// TestClient_Success 成功流——请求头与流翻译。
func TestClient_Success(t *testing.T) {
	var gotAuth, gotSession, gotAccept string
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotSession = r.Header.Get("x-opencode-session")
		gotAccept = r.Header.Get("Accept")
		builder := &strings.Builder{}
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			builder.Write(buf[:n])
			if err != nil {
				break
			}
		}
		gotBody = builder.String()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	client := newTestClient(t, server)
	events := collectClient(client)
	if got := kinds(events); got != "text,done" {
		t.Fatalf("事件序列: %s", got)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("Authorization 头: %s", gotAuth)
	}
	if gotSession != "cat-home4-cat" {
		t.Fatalf("x-opencode-session 头: %s", gotSession)
	}
	if gotAccept != "text/event-stream" {
		t.Fatalf("Accept 头: %s", gotAccept)
	}
	if !strings.Contains(gotBody, `"stream":true`) || !strings.Contains(gotBody, `"stream_options":{"include_usage":true}`) {
		t.Fatalf("请求体缺少流式参数: %s", gotBody)
	}
}

// TestClient_HTTP400NoRetry 4xx 不重试——单次请求 + 具名错误。
func TestClient_HTTP400NoRetry(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"bad"}}`))
	}))
	defer server.Close()
	client := newTestClient(t, server)
	events := collectClient(client)
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("4xx 不应重试，实际请求 %d 次", calls)
	}
	if kinds(events) != "error" || events[0].Text != "ERR|invalid_request_error|bad" {
		t.Fatalf("错误事件: %+v", events)
	}
}

// TestClient_HTTP500Retries 5xx 重试至上限——3 次重试 + 最终错误。
func TestClient_HTTP500Retries(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`oops`))
	}))
	defer server.Close()
	client := newTestClient(t, server)
	events := collectClient(client)
	if atomic.LoadInt32(&calls) != 4 {
		t.Fatalf("5xx 应重试 3 次（共 4 次），实际 %d 次", calls)
	}
	sequence := kinds(events)
	want := "retrying,retryresume,retrying,retryresume,retrying,retryresume,error"
	if sequence != want {
		t.Fatalf("重试事件序列: %s", sequence)
	}
	if !strings.HasPrefix(events[len(events)-1].Text, "ERR|HTTP_500") {
		t.Fatalf("最终错误文本: %s", events[len(events)-1].Text)
	}
}
