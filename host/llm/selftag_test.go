package llm

import (
	"net/http/httptest"
	"strings"
	"testing"

	"cathome5/host/session"
)

// TestIsSelfAPIs 端点判定——回环地址 + 18100 端口才成立（对齐 C# IsSelfApis）。
func TestIsSelfAPIs(t *testing.T) {
	cases := []struct {
		endpoint string
		want     bool
	}{
		{"http://127.0.0.1:18100/v1/chat/completions", true},
		{"http://127.0.0.2:18100/cs/v1/chat/completions", true},
		{"http://localhost:18100/v1", true},
		{"http://[::1]:18100/v1", true},
		{"https://api.deepseek.com/v1/chat/completions", false},
		{"http://127.0.0.1:8080/v1", false},
		{"http://10.0.0.5:18100/v1", false},
		{"http://127.0.0.1/v1", false},
		{"", false},
		{"not-a-url", false},
	}
	for _, item := range cases {
		if got := isSelfAPIs(item.endpoint); got != item.want {
			t.Fatalf("isSelfAPIs(%q) = %v，期望 %v", item.endpoint, got, item.want)
		}
	}
}

// TestApplySelfTagHeaders 自家 apis——八头齐备且值与身份一致。
func TestApplySelfTagHeaders(t *testing.T) {
	endpoint := "http://127.0.0.1:18100/v1/chat/completions"
	request := httptest.NewRequest("POST", endpoint, nil)
	tag := session.SelfTag{
		CatID:     "majordomo",
		CatName:   "Majordomo",
		SessionID: "sid-1",
		Project:   "CatHome5",
		MountMode: "expert",
		Jobs:      "CatHome,CatHome5",
		ClientID:  "cat-home5/0.7.13",
	}
	if !ApplySelfTag(request, endpoint, &tag, "chat") {
		t.Fatal("自家 apis 应附加自报头")
	}
	want := map[string]string{
		"x-cat-id":             "majordomo",
		"x-cat-name":           "Majordomo",
		"x-cat-session":        "sid-1",
		"x-project-mount":      "CatHome5",
		"x-project-mount-mode": "expert",
		"x-project-jobs":       "CatHome,CatHome5",
		"x-cat-channel":        "chat",
		"x-client-id":          "cat-home5/0.7.13",
	}
	for name, value := range want {
		if got := request.Header.Get(name); got != value {
			t.Fatalf("头 %s = %q，期望 %q", name, got, value)
		}
	}
}

// TestApplySelfTagForeignEndpoint 第三方端点——一个字节不加。
func TestApplySelfTagForeignEndpoint(t *testing.T) {
	request := httptest.NewRequest("POST", "https://api.deepseek.com/v1/chat/completions", nil)
	tag := session.SelfTag{CatID: "majordomo", CatName: "Majordomo", SessionID: "sid-1", Project: "CatHome5"}
	if ApplySelfTag(request, "https://api.deepseek.com/v1/chat/completions", &tag, "chat") {
		t.Fatal("第三方端点不应附加自报头")
	}
	if len(request.Header) != 0 {
		t.Fatalf("第三方端点头面被改动: %v", request.Header)
	}
}

// TestApplySelfTagUnmounted 未挂载——猫身份三头照发，挂载三头整组不发。
func TestApplySelfTagUnmounted(t *testing.T) {
	endpoint := "http://127.0.0.1:18100/v1"
	request := httptest.NewRequest("POST", endpoint, nil)
	tag := session.SelfTag{CatID: "majordomo", CatName: "Majordomo", SessionID: "sid-1"}
	ApplySelfTag(request, endpoint, &tag, "chat")
	for _, name := range []string{"x-project-mount", "x-project-mount-mode", "x-project-jobs"} {
		if got := request.Header.Get(name); got != "" {
			t.Fatalf("未挂载不应发 %s（实际 %q）", name, got)
		}
	}
	for _, name := range []string{"x-cat-id", "x-cat-name", "x-cat-session", "x-cat-channel", "x-client-id"} {
		if got := request.Header.Get(name); got == "" {
			t.Fatalf("未挂载仍应发 %s", name)
		}
	}
}

// TestCleanSelfTagValue 头值净化——控制字符、非 ASCII 百分号编码、120 截断、空值不发。
func TestCleanSelfTagValue(t *testing.T) {
	if got := cleanSelfTagValue("  中文 猫  "); got != "%E4%B8%AD%E6%96%87%20%E7%8C%AB" {
		t.Fatalf("非 ASCII 百分号编码不符: %q", got)
	}
	if got := cleanSelfTagValue("a\r\nb"); got != "a  b" {
		t.Fatalf("控制字符应替换为空格: %q", got)
	}
	if got := cleanSelfTagValue("cat  home"); got != "cat  home" {
		t.Fatalf("ASCII 原样保留（空格不编码）: %q", got)
	}
	long := strings.Repeat("a", 200)
	if got := cleanSelfTagValue(long); len(got) != selfTagValueMax {
		t.Fatalf("截断长度 %d，期望 %d", len(got), selfTagValueMax)
	}
	if got := cleanSelfTagValue("  \r\n\t "); got != "" {
		t.Fatalf("纯空白应为空串: %q", got)
	}
}

// TestApplySelfTagIdempotent 同名头已存在时不覆盖（重试复用不叠加）。
func TestApplySelfTagIdempotent(t *testing.T) {
	endpoint := "http://127.0.0.1:18100/v1"
	request := httptest.NewRequest("POST", endpoint, nil)
	request.Header.Set("x-cat-id", "既有值")
	tag := session.SelfTag{CatID: "majordomo"}
	ApplySelfTag(request, endpoint, &tag, "chat")
	if got := request.Header.Get("x-cat-id"); got != "既有值" {
		t.Fatalf("既有头被覆盖: %q", got)
	}
}

// TestApplySelfTagNilTag 积木出口（tag 为 nil）——只发通道与默认产品标识，猫身份与挂载整组不发。
//
// 对齐 C# LlmSelfTag.Apply 的 tag==null 分支（llm.simple_request 等无猫上下文的运行时出口）。
func TestApplySelfTagNilTag(t *testing.T) {
	endpoint := "http://127.0.0.1:18100/v1/chat/completions"
	request := httptest.NewRequest("POST", endpoint, nil)
	if !ApplySelfTag(request, endpoint, nil, "brick-simple") {
		t.Fatal("自家 apis 应附加自报头")
	}
	if got := request.Header.Get("x-cat-channel"); got != "brick-simple" {
		t.Fatalf("通道头 = %q", got)
	}
	if got := request.Header.Get("x-client-id"); got != session.DefaultClientID {
		t.Fatalf("默认客户端标识 = %q，期望 %q（C# tag==null 分支同形）", got, session.DefaultClientID)
	}
	for _, name := range []string{"x-cat-id", "x-cat-name", "x-cat-session", "x-project-mount", "x-project-mount-mode", "x-project-jobs"} {
		if got := request.Header.Get(name); got != "" {
			t.Fatalf("tag 为 nil 时不应发 %s（实际 %q）", name, got)
		}
	}
	foreign := httptest.NewRequest("POST", "https://api.deepseek.com/v1", nil)
	if ApplySelfTag(foreign, "https://api.deepseek.com/v1", nil, "brick-simple") {
		t.Fatal("第三方端点不应附加自报头")
	}
}
