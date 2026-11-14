package session

import (
	"os"
	"strings"
	"testing"
)

// TestEditMessageUpdatesContextViewAndStore 编辑成功路径——前文 / 视图载荷 / 落盘三方同步。
func TestEditMessageUpdatesContextViewAndStore(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSessionInSessions(t, dir, "cat-edit")
	s.Context.SetSystemPrompt("系统词")
	s.Context.AddUserMessage("旧问题")
	s.Context.AddAssistantMessage("旧回答")
	msgs := s.Context.Messages()
	for i, msg := range msgs {
		switch msg.Role {
		case RoleUser:
			s.Views.OnUserMessage(msg, i, "")
		case RoleAssistant:
			s.Views.OnAssistantText(msg, i, 0)
		}
	}
	s.Store.Rewrite(msgs)

	if got := s.EditMessage(1, "新问题"); !strings.HasPrefix(got, "edit | ") {
		t.Fatalf("编辑应成功: %q", got)
	}
	if got := s.Context.Messages()[1].Content; got != "新问题" {
		t.Fatalf("前文未更新: %q", got)
	}
	blocks := s.Views.Blocks()
	if len(blocks) != 2 || !strings.Contains(blocks[0].Payload, "新问题") {
		t.Fatalf("视图载荷未更新: %+v", blocks)
	}
	raw, err := os.ReadFile(s.Store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "新问题") {
		t.Fatalf("前文落盘未更新: %s", string(raw))
	}
	// 视图文件重载仍是新内容（编辑同批重写落盘）
	reloaded := NewViewStore(s.Views.Path())
	reloaded.Load()
	if blocks := reloaded.Blocks(); len(blocks) != 2 || !strings.Contains(blocks[0].Payload, "新问题") {
		t.Fatalf("视图落盘未更新: %+v", blocks)
	}
}

// TestEditMessageRejects 编辑拒绝——system / 工具声明轮 / 空正文 / 越界 / 忙。
func TestEditMessageRejects(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSessionInSessions(t, dir, "cat-edit-reject")
	s.Context.SetSystemPrompt("系统词")
	s.Context.AddUserMessage("问题")
	s.Context.AddAssistantToolCalls(`[{"id":"c1","function":{"name":"text-read","arguments":"{}"}}]`, "")
	s.Context.AddToolResult("c1", "text-read", "结果")

	if got := s.EditMessage(0, "x"); !strings.HasPrefix(got, "ERR|EDIT_NODE|") {
		t.Fatalf("system 应拒绝: %q", got)
	}
	if got := s.EditMessage(2, "x"); !strings.HasPrefix(got, "ERR|EDIT_NODE|") {
		t.Fatalf("工具声明轮应拒绝: %q", got)
	}
	if got := s.EditMessage(1, ""); !strings.HasPrefix(got, "ERR|EDIT_EMPTY|") {
		t.Fatalf("空正文应拒绝: %q", got)
	}
	if got := s.EditMessage(99, "x"); !strings.HasPrefix(got, "ERR|EDIT_INDEX|") {
		t.Fatalf("越界应拒绝: %q", got)
	}
	r := s.roundState()
	r.mu.Lock()
	r.phase = chatPhaseLlmRunning
	r.mu.Unlock()
	if got := s.EditMessage(1, "x"); !strings.HasPrefix(got, "ERR|EDIT_BUSY|") {
		t.Fatalf("忙应拒绝: %q", got)
	}
	// 拒绝不落任何改动——前文仍是 4 条
	if got := s.Context.Count(); got != 4 {
		t.Fatalf("拒绝路径不应改前文: %d", got)
	}
}

// TestEditMessageToolResult 编辑工具回执——toolcard 载荷 result 同步。
func TestEditMessageToolResult(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSessionInSessions(t, dir, "cat-edit-tool")
	s.Context.SetSystemPrompt("系统词")
	s.Context.AddUserMessage("查")
	call := `[{"id":"c1","function":{"name":"text-read","arguments":"{}"}}]`
	s.Context.AddAssistantToolCalls(call, "")
	s.Context.AddToolResult("c1", "text-read", "旧结果")
	msgs := s.Context.Messages()
	for i, msg := range msgs {
		switch msg.Role {
		case RoleUser:
			s.Views.OnUserMessage(msg, i, "")
		case RoleAssistant:
			s.Views.OnAssistantToolCalls(msg, i, 0)
		case RoleTool:
			s.Views.OnToolResult(msg, i, 0, 0, 0)
		}
	}
	if got := s.EditMessage(3, "新结果"); !strings.HasPrefix(got, "edit | ") {
		t.Fatalf("编辑工具回执应成功: %q", got)
	}
	found := false
	for _, block := range s.Views.Blocks() {
		if block.RenderType == TypeToolCard && strings.Contains(block.Payload, "新结果") {
			found = true
		}
	}
	if !found {
		t.Fatalf("工具卡载荷未更新: %+v", s.Views.Blocks())
	}
}

// TestResendMessageTruncatesAndDelivers 编辑重发——丢弃该条及之后并重新投递。
func TestResendMessageTruncatesAndDelivers(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSessionInSessions(t, dir, "cat-resend")
	s.Context.SetSystemPrompt("系统词")
	s.Context.AddUserMessage("旧问题")
	s.Context.AddAssistantMessage("旧回答")
	msgs := s.Context.Messages()
	for i, msg := range msgs {
		switch msg.Role {
		case RoleUser:
			s.Views.OnUserMessage(msg, i, "")
		case RoleAssistant:
			s.Views.OnAssistantText(msg, i, 0)
		}
	}
	if got := s.ResendMessage(1, "新问题"); !strings.HasPrefix(got, "resend | ") {
		t.Fatalf("重发应成功: %q", got)
	}
	if got := s.Context.Count(); got != 1 {
		t.Fatalf("应截断到 system: %d", got)
	}
	if blocks := s.Views.Blocks(); len(blocks) != 0 {
		t.Fatalf("视图应清空派生块: %d", len(blocks))
	}
	if user, _ := s.Pending.Len(); user != 1 {
		t.Fatalf("待处理队列应收到新消息: %d", user)
	}
}

// TestResendMessageRejects 重发拒绝——非 user / 系统注入 / 忙。
func TestResendMessageRejects(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSessionInSessions(t, dir, "cat-resend-reject")
	s.Context.SetSystemPrompt("系统词")
	s.Context.AddUserMessage("问题")
	s.Context.AddAssistantMessage("回答")
	s.Context.AddUserMessage(SysInjectPrefix + " 系统注入")

	if got := s.ResendMessage(2, "x"); !strings.HasPrefix(got, "ERR|EDIT_NODE|") {
		t.Fatalf("assistant 应拒绝: %q", got)
	}
	if got := s.ResendMessage(0, "x"); !strings.HasPrefix(got, "ERR|EDIT_NODE|") {
		t.Fatalf("system 应拒绝: %q", got)
	}
	if got := s.ResendMessage(3, "x"); !strings.HasPrefix(got, "ERR|EDIT_NODE|") {
		t.Fatalf("系统注入消息应拒绝: %q", got)
	}
	r := s.roundState()
	r.mu.Lock()
	r.phase = chatPhaseLlmRunning
	r.mu.Unlock()
	if got := s.ResendMessage(1, "x"); !strings.HasPrefix(got, "ERR|EDIT_BUSY|") {
		t.Fatalf("忙应拒绝: %q", got)
	}
}

// TestEditReasoningUpdatesReasonBlock 编辑思考——只改 reason 块，同索引 text 块不动。
func TestEditReasoningUpdatesReasonBlock(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSessionInSessions(t, dir, "cat-edit-reason")
	s.Context.SetSystemPrompt("系统词")
	s.Context.AddUserMessage("问题")
	s.Context.AddAssistantMessage("回答")
	s.Context.SetMessageReasoning(2, "旧思考")
	msgs := s.Context.Messages()
	for i, msg := range msgs {
		switch msg.Role {
		case RoleUser:
			s.Views.OnUserMessage(msg, i, "")
		case RoleAssistant:
			s.Views.OnAssistantText(msg, i, 0)
		}
	}
	if got := s.EditReasoning(2, "新思考"); !strings.HasPrefix(got, "editreason | ") {
		t.Fatalf("编辑思考应成功: %q", got)
	}
	if got := s.Context.Messages()[2].ReasoningContent; got != "新思考" {
		t.Fatalf("思考未更新: %q", got)
	}
	reasonOK, textOK := false, false
	for _, block := range s.Views.Blocks() {
		if block.RenderType == TypeReason && strings.Contains(block.Payload, "新思考") {
			reasonOK = true
		}
		if block.RenderType == TypeText && strings.Contains(block.Payload, "回答") {
			textOK = true
		}
	}
	if !reasonOK || !textOK {
		t.Fatalf("视图块串写或未更新: %+v", s.Views.Blocks())
	}
	if got := s.EditReasoning(1, "x"); !strings.HasPrefix(got, "ERR|EDIT_NODE|") {
		t.Fatalf("user 消息应拒绝: %q", got)
	}
}

// TestEditMessageAndRewind 编辑并回滚——改内容后截断到该条（保留改后内容，丢弃其后）。
func TestEditMessageAndRewind(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSessionInSessions(t, dir, "cat-edit-rewind")
	s.Context.SetSystemPrompt("系统词")
	s.Context.AddUserMessage("问题一")
	s.Context.AddAssistantMessage("回答一")
	s.Context.AddUserMessage("问题二")
	s.Context.AddAssistantMessage("回答二")
	msgs := s.Context.Messages()
	for i, msg := range msgs {
		switch msg.Role {
		case RoleUser:
			s.Views.OnUserMessage(msg, i, "")
		case RoleAssistant:
			s.Views.OnAssistantText(msg, i, 0)
		}
	}
	if got := s.EditMessageAndRewind(2, "改后的回答一"); !strings.HasPrefix(got, "editrewind | ") {
		t.Fatalf("编辑并回滚应成功: %q", got)
	}
	if got := s.Context.Count(); got != 3 {
		t.Fatalf("应保留 3 条（system + 问题一 + 改后回答一）: %d", got)
	}
	if got := s.Context.Messages()[2].Content; got != "改后的回答一" {
		t.Fatalf("内容未更新: %q", got)
	}
	blocks := s.Views.Blocks()
	if len(blocks) != 2 {
		t.Fatalf("视图应保留 2 块: %d", len(blocks))
	}
	if !strings.Contains(blocks[1].Payload, "改后的回答一") {
		t.Fatalf("视图载荷未更新: %+v", blocks[1])
	}
	reloaded := NewViewStore(s.Views.Path())
	reloaded.Load()
	if got := len(reloaded.Blocks()); got != 2 {
		t.Fatalf("视图截断应落盘: %d", got)
	}
}

// TestEditReasoningAndRewind 编辑思考并回滚——思考更新且其后消息丢弃。
func TestEditReasoningAndRewind(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSessionInSessions(t, dir, "cat-edit-reason-rewind")
	s.Context.SetSystemPrompt("系统词")
	s.Context.AddUserMessage("问题")
	s.Context.AddAssistantMessage("回答")
	s.Context.SetMessageReasoning(2, "旧思考")
	s.Context.AddUserMessage("追问")
	s.Context.AddAssistantMessage("再答")
	msgs := s.Context.Messages()
	for i, msg := range msgs {
		switch msg.Role {
		case RoleUser:
			s.Views.OnUserMessage(msg, i, "")
		case RoleAssistant:
			s.Views.OnAssistantText(msg, i, 0)
		}
	}
	if got := s.EditReasoningAndRewind(2, "新思考"); !strings.HasPrefix(got, "editreasonrewind | ") {
		t.Fatalf("编辑思考并回滚应成功: %q", got)
	}
	if got := s.Context.Count(); got != 3 {
		t.Fatalf("应保留 3 条: %d", got)
	}
	if got := s.Context.Messages()[2].ReasoningContent; got != "新思考" {
		t.Fatalf("思考未更新: %q", got)
	}
	reasonOK := false
	for _, block := range s.Views.Blocks() {
		if block.RenderType == TypeReason && strings.Contains(block.Payload, "新思考") {
			reasonOK = true
		}
	}
	if !reasonOK {
		t.Fatalf("思考块载荷未更新: %+v", s.Views.Blocks())
	}
}
