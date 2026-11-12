package llm

import (
	"strings"
	"testing"

	"cathome5/host/session"
)

// TestBuildChatRequestBody_Golden 请求体逐字节——键序 / 字段省略 / 空参数 schema 兜底。
func TestBuildChatRequestBody_Golden(t *testing.T) {
	messages := []session.Message{
		{Role: session.RoleSystem, Content: "SYS"},
		{Role: session.RoleUser, Content: "你好"},
		{Role: session.RoleAssistant, ToolCallsJSON: `[{"id":"a","type":"function","function":{"name":"time","arguments":"{}"}}]`},
		{Role: session.RoleTool, ToolCallID: "a"},
		{Role: session.RoleAssistant, Content: "done", ReasoningContent: "思考"},
	}
	tools := []session.ToolSpec{{Name: "time", Description: "当前时间"}}
	params := Params{Model: "m", MaxTokens: 100, Thinking: true, ReasoningEffort: "high"}
	body := BuildChatRequestBody(messages, tools, params, "majordomo")

	want := `{"model":"m","messages":[` +
		`{"role":"system","content":"SYS"},` +
		`{"role":"user","content":"你好"},` +
		`{"role":"assistant","content":"","tool_calls":[{"id":"a","type":"function","function":{"name":"time","arguments":"{}"}}],"reasoning_content":""},` +
		`{"role":"tool","tool_call_id":"a","content":"(no output)"},` +
		`{"role":"assistant","content":"done","reasoning_content":"思考"}` +
		`],"stream":true,"max_tokens":100,"stream_options":{"include_usage":true},` +
		`"thinking":{"type":"enabled"},"reasoning_effort":"high",` +
		`"tools":[{"type":"function","function":{"name":"time","description":"当前时间","parameters":{"type":"object","properties":{}}}}]` +
		`,"user_id":"majordomo"}`
	if body != want {
		t.Fatalf("请求体不一致\n got: %s\nwant: %s", body, want)
	}
}

// TestBuildChatRequestBody_NoThinking 思考关闭——不带 reasoning_effort；空 tools / userID 省略字段。
func TestBuildChatRequestBody_NoThinking(t *testing.T) {
	body := BuildChatRequestBody([]session.Message{{Role: session.RoleUser, Content: "hi"}}, nil, Params{Model: "m", MaxTokens: 8}, "")
	want := `{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true,"max_tokens":8,"stream_options":{"include_usage":true},"thinking":{"type":"disabled"}}`
	if body != want {
		t.Fatalf("请求体不一致\n got: %s\nwant: %s", body, want)
	}
}

// TestBuildChatRequestBody_ImageFailure 图片引用失效——text 块内可见失败，不阻断请求。
func TestBuildChatRequestBody_ImageFailure(t *testing.T) {
	messages := []session.Message{{
		Role:       session.RoleUser,
		Content:    "看图",
		ImagesJSON: `["D:/no/such/file.png"]`,
	}}
	body := BuildChatRequestBody(messages, nil, Params{Model: "m"}, "")
	if !strings.Contains(body, `"type":"text"`) || !strings.Contains(body, "[图片引用失效]") {
		t.Fatalf("失效引用未并入 text 块: %s", body)
	}
	if !strings.Contains(body, "ERR|IMAGE_NOT_FOUND") {
		t.Fatalf("失败原因未可见: %s", body)
	}
}

// TestDeriveChatEndpoint 端点推导——含尾路径原样，base 地址拼尾。
func TestDeriveChatEndpoint(t *testing.T) {
	cases := map[string]string{
		"https://api.example.com/v1":                     "https://api.example.com/v1/chat/completions",
		"https://api.example.com/v1/chat/completions":    "https://api.example.com/v1/chat/completions",
		"https://api.example.com/v1/chat/completions///": "https://api.example.com/v1/chat/completions",
		"": "",
	}
	for input, want := range cases {
		if got := DeriveChatEndpoint(input); got != want {
			t.Fatalf("DeriveChatEndpoint(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestParseErrorText 错误体双形态——error{type,message} / 外层 type / 原文兜底。
func TestParseErrorText(t *testing.T) {
	nested := ParseErrorText(401, `{"error":{"type":"authentication_error","message":"bad key"}}`)
	if nested != "ERR|authentication_error|bad key" {
		t.Fatalf("嵌套错误解析: %s", nested)
	}
	outer := ParseErrorText(502, `{"type":"upstream_unavailable"}`)
	if outer != `ERR|upstream_unavailable|{"type":"upstream_unavailable"}` {
		t.Fatalf("外层 type 解析: %s", outer)
	}
	fallback := ParseErrorText(500, `oops`)
	if fallback != "ERR|HTTP_500|oops" {
		t.Fatalf("兜底解析: %s", fallback)
	}
}
