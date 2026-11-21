package session

import "testing"

// assertCharsConsistent 断言增量字符计数与全量基准一致（computeContextChars = 旧实现，作为唯一 oracle）。
func assertCharsConsistent(t *testing.T, c *Context, step string) {
	t.Helper()
	want := computeContextChars(c.Messages())
	if got := c.Chars(); got != want {
		t.Fatalf("%s：Chars()=%d，基准 computeContextChars=%d", step, got, want)
	}
}

// TestContextCharsIncremental 覆盖全部写入口的增量维护——每步与全量基准对照。
func TestContextCharsIncremental(t *testing.T) {
	c := NewContext()
	assertCharsConsistent(t, c, "空上下文")

	c.AddUserMessage("你好，猫娘")
	assertCharsConsistent(t, c, "追加用户消息")

	c.AddAssistantMessageWithReasoning("回复正文", "这段思考不计入字符数")
	assertCharsConsistent(t, c, "追加助手消息（reasoning 不计）")

	c.AddAssistantToolCalls(`[{"id":"call_1","function":{"name":"info"}}]`, "声明轮思考")
	assertCharsConsistent(t, c, "追加工具声明（正文空）")

	c.AddToolResult("call_1", "info", "工具回执正文")
	assertCharsConsistent(t, c, "追加工具结果")

	if dup := c.AddToolResult("call_1", "info", "重复结果不入册"); dup != nil {
		t.Fatalf("重复 ToolCallID 应返回 nil，实得 %+v", dup)
	}
	assertCharsConsistent(t, c, "重复工具结果（幂等）")

	if !c.SetMessageContent(0, "改过的用户正文") {
		t.Fatalf("SetMessageContent 未命中索引 0")
	}
	assertCharsConsistent(t, c, "编辑正文")

	if !c.SetMessageContent(0, "") {
		t.Fatalf("SetMessageContent 未命中索引 0（清空）")
	}
	assertCharsConsistent(t, c, "编辑正文（清空）")

	c.SetMessageReasoning(2, "更长的思考内容……")
	assertCharsConsistent(t, c, "编辑思考（不计入）")

	c.SetSystemPrompt("系统提示词")
	assertCharsConsistent(t, c, "设置系统提示词")

	c.Clear()
	assertCharsConsistent(t, c, "清空（保留系统提示词）")
}

// TestContextCharsReplaceMessages 历史替换后的重算——结构修复（补占位 / 丢孤立）也纳入计数。
func TestContextCharsReplaceMessages(t *testing.T) {
	c := NewContext()
	c.AddUserMessage("旧内容")

	orphan := newMessage(RoleTool, "孤立结果（无声明）")
	orphan.ToolCallID = "c9"
	msgs := []Message{
		newMessage(RoleSystem, "系统"),
		newMessage(RoleUser, "用户一句"),
		{Role: RoleAssistant, ToolCallsJSON: `[{"id":"c1","function":{"name":"info"}},{"id":"c2","function":{"name":"info"}}]`},
		newMessage(RoleTool, "只有 c1 的结果"),
		orphan,
	}
	msgs[3].ToolCallID = "c1"
	c.ReplaceMessages(msgs)
	assertCharsConsistent(t, c, "替换历史（c2 补占位 / c9 孤立丢弃）")

	c.ReplaceMessages(nil)
	assertCharsConsistent(t, c, "替换为空历史")
	if got := c.Chars(); got != 0 {
		t.Fatalf("空历史应为 0，实得 %d", got)
	}
}

// TestContextCharsToolResultLimit 入前文限幅后按实际落库正文计数。
func TestContextCharsToolResultLimit(t *testing.T) {
	c := NewContext()
	long := make([]rune, maxToolResultChars+50)
	for i := range long {
		long[i] = '猫'
	}
	c.AddToolResult("call_big", "text-read", string(long))
	assertCharsConsistent(t, c, "超长工具结果截断")

	msgs := c.Messages()
	if len(msgs) != 1 {
		t.Fatalf("期望 1 条工具结果，实得 %d 条", len(msgs))
	}
	if got := utf8Len(msgs[0].Content); got == utf8Len(string(long)) {
		t.Fatalf("超限结果未被限幅（%d 字符）", got)
	}
}

// TestContextCharsRuneSemantics 字符口径 = rune 数（中文 1 / emoji 1）。
func TestContextCharsRuneSemantics(t *testing.T) {
	c := NewContext()
	text := "中文" + "🐱" + "abc"
	c.AddUserMessage(text)
	want := int64(len([]rune(text)))
	if got := c.Chars(); got != want {
		t.Fatalf("Chars()=%d，期望 rune 数 %d", got, want)
	}
	assertCharsConsistent(t, c, "rune 口径")
}

// utf8Len 字符数（rune 数）——测试辅助。
func utf8Len(s string) int {
	count := 0
	for range s {
		count++
	}
	return count
}

// TestComputeContextCharsZeroAlloc 基准实现零分配——旧实现 len([]rune(Content)) 每次调用
// 会按字符数为每条消息分配 4 B 切片；这里是 P0 的分配面判据。
func TestComputeContextCharsZeroAlloc(t *testing.T) {
	msgs := make([]Message, 200)
	for i := range msgs {
		msgs[i] = newMessage(RoleUser, "中文内容测试 🐱 abc def")
	}
	allocs := testing.AllocsPerRun(100, func() { _ = computeContextChars(msgs) })
	if allocs != 0 {
		t.Fatalf("computeContextChars 期望零分配，实得 %.2f 次/调用", allocs)
	}
}

// TestContextCharsReadZeroAlloc 增量读面零分配——快照/状态面每 250ms 调用它。
func TestContextCharsReadZeroAlloc(t *testing.T) {
	c := NewContext()
	for i := 0; i < 200; i++ {
		c.AddUserMessage("中文内容测试 🐱 abc def")
	}
	allocs := testing.AllocsPerRun(100, func() { _ = c.Chars() })
	if allocs != 0 {
		t.Fatalf("Context.Chars() 期望零分配，实得 %.2f 次/调用", allocs)
	}
}
