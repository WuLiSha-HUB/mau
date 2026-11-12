package session

import (
	"strings"
	"testing"
)

// TestContextAppendRules 追加规则——空文本不入上下文 / 工具结果幂等 / 空 tool_calls 不入上下文。
func TestContextAppendRules(t *testing.T) {
	c := NewContext()
	if c.AddUserMessage("") != nil {
		t.Fatal("空文本不应入上下文")
	}
	if c.AddUserMessageWithImages("", "") != nil {
		t.Fatal("文本与附件皆空不应入上下文")
	}
	if c.AddUserMessageWithImages("", `["D:/a.png"]`) == nil {
		t.Fatal("有附件无正文应入上下文")
	}
	if c.AddAssistantToolCalls("", "思考") != nil {
		t.Fatal("空 tool_calls 不应入上下文")
	}
	if c.AddToolResult("t1", "text-read", "结果") == nil {
		t.Fatal("首次工具结果应入上下文")
	}
	if c.AddToolResult("t1", "text-read", "重复") != nil {
		t.Fatal("同一 ToolCallID 只入册一次")
	}
	if c.Count() != 2 {
		t.Fatalf("上下文条数应为 2，实得 %d", c.Count())
	}
}

// TestContextReplaceRepairs 结构修复——system 唯一 / 孤立 tool 丢弃 / 声明缺结果补占位。
func TestContextReplaceRepairs(t *testing.T) {
	c := NewContext()
	messages := []Message{
		{Role: RoleSystem, Content: "第一 system"},
		{Role: RoleSystem, Content: "第二 system"},
		{Role: RoleUser, Content: "问"},
		{Role: RoleAssistant, ToolCallsJSON: `[{"id":"a","function":{"name":"text-read"}},{"id":"b","function":{"name":"text-grep"}}]`},
		{Role: RoleTool, ToolCallID: "a", ToolName: "text-read", Content: "结果 A"},
		{Role: RoleTool, ToolCallID: "zombie", ToolName: "x", Content: "孤立结果"},
		{Role: RoleAssistant, Content: "答"},
	}
	c.ReplaceMessages(messages)
	got := c.Messages()
	// [system][user][assistant 声明][tool a][占位 tool b][assistant 答]
	if len(got) != 6 {
		t.Fatalf("修复后条数应为 6，实得 %d: %+v", len(got), got)
	}
	if got[0].Content != "第一 system" {
		t.Fatalf("system 应取第一条: %q", got[0].Content)
	}
	if got[2].ToolCallsJSON == "" {
		t.Fatal("声明应保留")
	}
	if got[3].ToolCallID != "a" {
		t.Fatalf("真实结果应保留: %+v", got[3])
	}
	if got[4].ToolCallID != "b" || !strings.Contains(got[4].Content, "[系统自动修复]") {
		t.Fatalf("缺失结果应按声明序补占位: %+v", got[4])
	}
}

// TestContextReplaceDropsBrokenDecl 声明解析失败降级纯文本（保留 content）。
func TestContextReplaceDropsBrokenDecl(t *testing.T) {
	c := NewContext()
	c.ReplaceMessages([]Message{
		{Role: RoleAssistant, Content: "正文", ToolCallsJSON: "不是 JSON"},
	})
	got := c.Messages()
	if len(got) != 1 || got[0].ToolCallsJSON != "" || got[0].Content != "正文" {
		t.Fatalf("坏声明应降级纯文本: %+v", got)
	}
}

// TestContextClearKeepsSystem 清空保留系统提示词。
func TestContextClearKeepsSystem(t *testing.T) {
	c := NewContext()
	c.SetSystemPrompt("系统提示")
	c.AddUserMessage("问")
	c.Clear()
	got := c.Messages()
	if len(got) != 1 || got[0].Role != RoleSystem || got[0].Content != "系统提示" {
		t.Fatalf("清空后应只留 system: %+v", got)
	}
}
