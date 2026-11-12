package bricks

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cathome5/mau/runtime"
)

// waitLLMDone 轮询等待后台请求结算（超时即测试失败——不允许「不确定就放过」）。
func waitLLMDone(t *testing.T, svc *LLMSimpleService, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := svc.SimpleReady(id); ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待 requestId=%s 结算超时", id)
}

// TestLLMSimpleNoService 三件积木在服务未注入时回具名错误码。
func TestLLMSimpleNoService(t *testing.T) {
	const want = "ERR|LLM_NO_SERVICE|宿主未注入 LLMSimpleService"
	if text, ok := LLMSimpleRequest(nil, "e", "k", "m", "", "", "1"); ok || text != want {
		t.Fatalf("llm.simple_request 未注入 = (%q,%v)，期望 (%q,false)", text, ok, want)
	}
	if text, ok := LLMSimpleReady(nil, "1"); ok || text != want {
		t.Fatalf("llm.simple_ready 未注入 = (%q,%v)，期望 (%q,false)", text, ok, want)
	}
	if text, ok := LLMSimpleRead(nil, "1"); ok || text != want {
		t.Fatalf("llm.simple_read 未注入 = (%q,%v)，期望 (%q,false)", text, ok, want)
	}
}

// TestLLMSimpleRequestBadRequestID requestId 非法 → false 且零副作用（不落盒）。
func TestLLMSimpleRequestBadRequestID(t *testing.T) {
	box := runtime.NewDataBox()
	svc := NewLLMSimpleService(box, nil, nil)
	for _, id := range []string{"", "0", "abc", "-3", "  "} {
		if _, ok := svc.SimpleRequest("http://127.0.0.1:1", "k", "m", "", "", id); ok {
			t.Fatalf("requestId=%q 应拒绝", id)
		}
	}
	if entries := box.Capture(); len(entries) != 0 {
		t.Fatalf("非法 requestId 不应落盒，实际 %d 条", len(entries))
	}
}

// TestLLMSimpleRequestMissingArgs 缺 endpoint/apikey/model → 失败盒 + 日志 2 级 + false。
func TestLLMSimpleRequestMissingArgs(t *testing.T) {
	type sample struct {
		endpoint, apikey, model, id string
	}
	samples := []sample{
		{"", "k", "m", "11"},
		{"http://127.0.0.1:1", "", "m", "12"},
		{"http://127.0.0.1:1", "k", "", "13"},
	}
	for _, item := range samples {
		box := runtime.NewDataBox()
		var logs []string
		svc := NewLLMSimpleService(box, nil, func(level int, message string) {
			logs = append(logs, message)
		})
		text, ok := svc.SimpleRequest(item.endpoint, item.apikey, item.model, "", "", item.id)
		if ok || text != "false" {
			t.Fatalf("缺参应 false，实际 (%q,%v)", text, ok)
		}
		wantReply := "ERR|BAD_ARGS|一次性 LLM 请求缺 endpoint/apikey/model"
		if got := box.GetString("global", "llm_simple_reply:"+item.id); got != wantReply {
			t.Fatalf("reply 盒 = %q，期望 %q", got, wantReply)
		}
		if got := box.GetString("global", "llm_simple_done:"+item.id); got != "1" {
			t.Fatalf("done 盒 = %q，期望 \"1\"", got)
		}
		if len(logs) != 1 || !strings.Contains(logs[0], "requestId="+item.id) {
			t.Fatalf("日志 = %v，期望一条含 requestId", logs)
		}
	}
}

// TestLLMSimpleSuccess 成功链——请求体逐字 / 三个固定头 / 结果落盒。
func TestLLMSimpleSuccess(t *testing.T) {
	var gotBody, gotAuth, gotSession, gotContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		gotAuth = r.Header.Get("Authorization")
		gotSession = r.Header.Get("x-opencode-session")
		gotContentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"你好，世界"}}]}`)
	}))
	defer server.Close()

	box := runtime.NewDataBox()
	var logs []string
	svc := NewLLMSimpleService(box, nil, func(level int, message string) {
		logs = append(logs, message)
	})
	if text, ok := svc.SimpleRequest(server.URL, "sk-test", "deepseek-v4", "你是助手", "在吗", "7"); !ok || text != "true" {
		t.Fatalf("启动 = (%q,%v)，期望 (\"true\",true)", text, ok)
	}
	waitLLMDone(t, svc, "7")
	if got := box.GetString("global", "llm_simple_reply:7"); got != "你好，世界" {
		t.Fatalf("reply = %q", got)
	}
	// 请求体逐字——model / stream / messages 序 + system 在前 + 中文直出（不转 \uXXXX）
	wantBody := `{"model":"deepseek-v4","stream":false,"messages":[{"role":"system","content":"你是助手"},{"role":"user","content":"在吗"}]}`
	if gotBody != wantBody {
		t.Fatalf("请求体 = %q\n期望 = %q", gotBody, wantBody)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotSession != "cat-home4-simple" {
		t.Fatalf("x-opencode-session = %q（该固定值换掉即换上游缓存槽）", gotSession)
	}
	if gotContentType != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", gotContentType)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "一次性请求完成：requestId=7 · 5 字符") {
		t.Fatalf("日志 = %v", logs)
	}
}

// TestLLMSimpleParseText 解析五态——文案逐字对齐 C# ParseText。
func TestLLMSimpleParseText(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"非 JSON", "<html>", "ERR|PARSE|响应 JSON 解析失败"},
		{"非对象", `[1,2]`, "ERR|EMPTY_RESULT|响应非对象"},
		{"无 choices", `{"id":"x"}`, "ERR|EMPTY_RESULT|响应无 choices"},
		{"choices 空数组", `{"choices":[]}`, "ERR|EMPTY_RESULT|响应无 choices"},
		{"无 message", `{"choices":[{"text":"x"}]}`, "ERR|EMPTY_RESULT|响应无 message"},
		{"空内容", `{"choices":[{"message":{"content":""}}]}`, "ERR|EMPTY_CONTENT|模型返回空内容"},
		{"成功", `{"choices":[{"message":{"content":"ok"}}]}`, "ok"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := parseLLMSimpleText(item.body); got != item.want {
				t.Fatalf("解析 = %q，期望 %q", got, item.want)
			}
		})
	}
}

// TestLLMSimpleHTTPError 非 2xx → ERR|HTTP_<码>|<截断300>（解析类失败也走「完成」0 级日志）。
func TestLLMSimpleHTTPError(t *testing.T) {
	long := strings.Repeat("错", 400)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, long)
	}))
	defer server.Close()

	box := runtime.NewDataBox()
	var levels []int
	svc := NewLLMSimpleService(box, nil, func(level int, message string) {
		levels = append(levels, level)
	})
	if _, ok := svc.SimpleRequest(server.URL, "k", "m", "", "", "21"); !ok {
		t.Fatal("应受理")
	}
	waitLLMDone(t, svc, "21")
	got := box.GetString("global", "llm_simple_reply:21")
	want := "ERR|HTTP_500|" + strings.Repeat("错", 300) + "..."
	if got != want {
		t.Fatalf("HTTP 500 回执长度 %d，期望 %d；前 40 字 = %q", len([]rune(got)), len([]rune(want)), firstRunes(got, 40))
	}
	if len(levels) != 1 || levels[0] != 0 {
		t.Fatalf("日志级别 = %v，期望 [0]（解析类失败走完成级）", levels)
	}
}

// TestLLMSimpleTransportError 连接失败 → ERR|HttpRequestException|… + 日志 3 级。
func TestLLMSimpleTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := server.URL
	server.Close()

	box := runtime.NewDataBox()
	var levels []int
	svc := NewLLMSimpleService(box, nil, func(level int, message string) {
		levels = append(levels, level)
	})
	if _, ok := svc.SimpleRequest(deadURL, "k", "m", "", "", "31"); !ok {
		t.Fatal("应受理（失败在后台）")
	}
	waitLLMDone(t, svc, "31")
	got := box.GetString("global", "llm_simple_reply:31")
	if !strings.HasPrefix(got, "ERR|HttpRequestException|") {
		t.Fatalf("连接失败回执 = %q", got)
	}
	if len(levels) != 1 || levels[0] != 3 {
		t.Fatalf("日志级别 = %v，期望 [3]", levels)
	}
}

// TestClassifyLLMError 超时映射 TaskCanceledException（C# 同族）。
func TestClassifyLLMError(t *testing.T) {
	name, _ := classifyLLMError(context.DeadlineExceeded)
	if name != "TaskCanceledException" {
		t.Fatalf("deadline 分类 = %q", name)
	}
	name, _ = classifyLLMError(io.ErrUnexpectedEOF)
	if name != "HttpRequestException" {
		t.Fatalf("普通错误分类 = %q", name)
	}
}

// TestBuildLLMURL 端点补全三态。
func TestBuildLLMURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://api.x/v1", "https://api.x/v1/chat/completions"},
		{"https://api.x/v1/", "https://api.x/v1/chat/completions"},
		{"https://api.x/v1/chat/completions", "https://api.x/v1/chat/completions"},
		{"https://api.x/v1/Chat/Completions", "https://api.x/v1/Chat/Completions"},
		{"  https://api.x/v1  ", "https://api.x/v1/chat/completions"},
	}
	for _, item := range cases {
		if got := buildLLMURL(item.in); got != item.want {
			t.Fatalf("buildLLMURL(%q) = %q，期望 %q", item.in, got, item.want)
		}
	}
}

// TestBuildLLMSimpleBody 请求体转义口径——中文直出 / 短转义 / 控制字符 \u00xx 小写。
func TestBuildLLMSimpleBody(t *testing.T) {
	body := buildLLMSimpleBody("m", "引号\"与\\反斜杠", "换行\n与\u001f控制")
	want := `{"model":"m","stream":false,"messages":[{"role":"system","content":"引号\"与\\反斜杠"},{"role":"user","content":"换行\n与\u001f控制"}]}`
	if body != want {
		t.Fatalf("请求体 = %s\n期望 = %s", body, want)
	}
}

// TestLLMSimpleReadyRead 就绪与取数三态。
func TestLLMSimpleReadyRead(t *testing.T) {
	box := runtime.NewDataBox()
	svc := NewLLMSimpleService(box, nil, nil)
	if _, ok := svc.SimpleReady("41"); ok {
		t.Fatal("未落盒应报未就绪")
	}
	if text, ok := svc.SimpleRead("41"); ok || text != "" {
		t.Fatalf("未落盒取数 = (%q,%v)", text, ok)
	}
	box.Set("global", "llm_simple_done:41", "1")
	box.Set("global", "llm_simple_reply:41", "ERR|EMPTY_RESULT|响应无 choices")
	if text, ok := svc.SimpleReady("41"); !ok || text != "true" {
		t.Fatalf("已结算应 true，实际 (%q,%v)", text, ok)
	}
	if text, ok := svc.SimpleRead("41"); !ok || text != "ERR|EMPTY_RESULT|响应无 choices" {
		t.Fatalf("失败文本也应可读，实际 (%q,%v)", text, ok)
	}
	// 空串不是有效结果（语料分叉依据）
	box.Set("global", "llm_simple_reply:41", "")
	if _, ok := svc.SimpleRead("41"); ok {
		t.Fatal("空串应报不可读")
	}
	// requestId 非法
	if _, ok := svc.SimpleReady("x"); ok {
		t.Fatal("非法 requestId 应 false")
	}
}

// firstRunes 取前 n 个字符（失败信息预览用）。
func firstRunes(text string, n int) string {
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	return string(runes[:n])
}
