package session

import (
	"os"
	"strings"
	"testing"
)

// TestRollbackTruncatesContextViewAndStore 回滚成功路径——前文 / 视图 / 落盘三方同批截断。
func TestRollbackTruncatesContextViewAndStore(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSessionInSessions(t, dir, "cat-rollback")
	s.Context.SetSystemPrompt("系统词")
	s.Context.AddUserMessage("问题一")
	s.Context.AddAssistantMessage("回答一")
	s.Context.AddUserMessage("问题二")
	s.Context.AddAssistantMessage("回答二")
	// 视图块——user → user 块；assistant → text 块（system 不产块）
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

	// 切点 = 索引 2（回答一）→ 保留 3 条
	result := s.Rollback(2)
	if !strings.HasPrefix(result, "rollback | ") {
		t.Fatalf("回滚应成功: %q", result)
	}
	if got := s.Context.Count(); got != 3 {
		t.Fatalf("前文应截断到 3 条: %d", got)
	}
	if blocks := s.Views.Blocks(); len(blocks) != 2 {
		t.Fatalf("视图应截断到 2 块（system 不产块）: %d", len(blocks))
	}
	// 视图文件重载仍是 2 块（截断同批落盘）
	reloaded := NewViewStore(s.Views.Path())
	reloaded.Load()
	if blocks := reloaded.Blocks(); len(blocks) != 2 {
		t.Fatalf("视图截断应落盘: %d", len(blocks))
	}
	// 前文落盘 = meta 行 + 3 消息行
	raw, err := os.ReadFile(s.Store.Path())
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) != "" {
			lines++
		}
	}
	if lines != 4 {
		t.Fatalf("前文落盘应 4 行（meta + 3 消息）: %d", lines)
	}
	// 前文长度归零（读面不显示回滚点之前的旧值）
	if got := s.ContextTokens(); got != 0 {
		t.Fatalf("前文长度应归零: %d", got)
	}
}

// TestRollbackRejects 三类拒绝——system 切点 / 索引越界 / 会话忙（校验顺序先忙后索引）。
func TestRollbackRejects(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSessionInSessions(t, dir, "cat-rollback-reject")
	s.Context.SetSystemPrompt("系统词")
	s.Context.AddUserMessage("问题一")
	s.Context.AddAssistantMessage("回答一")

	if got := s.Rollback(0); !strings.HasPrefix(got, "ERR|ROLLBACK_NODE|") {
		t.Fatalf("system 节点应拒绝: %q", got)
	}
	if got := s.Rollback(99); !strings.HasPrefix(got, "ERR|ROLLBACK_INDEX|") {
		t.Fatalf("越界应拒绝: %q", got)
	}
	r := s.roundState()
	r.mu.Lock()
	r.phase = chatPhaseLlmRunning
	r.mu.Unlock()
	if got := s.Rollback(2); !strings.HasPrefix(got, "ERR|ROLLBACK_BUSY|") {
		t.Fatalf("会话忙应拒绝: %q", got)
	}
	// 拒绝不落任何改动——前文仍是 3 条
	if got := s.Context.Count(); got != 3 {
		t.Fatalf("拒绝路径不应改前文: %d", got)
	}
}

// TestRollbackClearsNote 回滚清空 Note 计划——防旧任务自动拉起新轮。
func TestRollbackClearsNote(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSessionInSessions(t, dir, "cat-rollback-note")
	s.Context.SetSystemPrompt("系统词")
	s.Context.AddAssistantMessage("回答")
	s.Note.Execute(`{"action":"set","content":["甲","乙"]}`)
	if tasks, _, _ := s.Note.Snapshot(); len(tasks) != 2 {
		t.Fatalf("Note 计划未写入: %v", tasks)
	}
	if got := s.Rollback(1); !strings.HasPrefix(got, "rollback | ") {
		t.Fatalf("回滚应成功: %q", got)
	}
	if tasks, _, _ := s.Note.Snapshot(); len(tasks) != 0 {
		t.Fatalf("回滚应清空 Note: %v", tasks)
	}
}

// TestRollbackUserRevokesInput user 切点——丢弃该条及之后（等价于「这条输入还没发出去」）。
func TestRollbackUserRevokesInput(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSessionInSessions(t, dir, "cat-rollback-user")
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
	if got := s.Rollback(3); !strings.HasPrefix(got, "rollback | ") {
		t.Fatalf("user 切点应受理: %q", got)
	}
	if got := s.Context.Count(); got != 3 {
		t.Fatalf("应丢弃问题二及其后（保留 3 条）: %d", got)
	}
	// 视图：问题一 / 回答一两块保留（问题二 / 回答二两块丢弃）
	if blocks := s.Views.Blocks(); len(blocks) != 2 {
		t.Fatalf("视图应保留 2 块: %d", len(blocks))
	}
}

// TestRollbackToolKeepsResult tool 切点——保留该回执；工具声明轮切点退到其之前。
func TestRollbackToolKeepsResult(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSessionInSessions(t, dir, "cat-rollback-tool")
	s.Context.SetSystemPrompt("系统词")
	s.Context.AddUserMessage("查一下")
	s.Context.AddAssistantToolCalls(`[{"id":"c1","function":{"name":"text-read","arguments":"{}"}}]`, "思考")
	s.Context.AddToolResult("c1", "text-read", "结果")
	s.Context.AddAssistantMessage("回答")
	if got := s.Rollback(3); !strings.HasPrefix(got, "rollback | ") {
		t.Fatalf("tool 切点应受理: %q", got)
	}
	if got := s.Context.Count(); got != 4 {
		t.Fatalf("tool 切点应保留 4 条（声明与回执成对）: %d", got)
	}
	if got := s.Rollback(2); !strings.Contains(got, "工具声明轮不可保留") {
		t.Fatalf("工具声明轮切点应退到其之前: %q", got)
	}
	if got := s.Context.Count(); got != 2 {
		t.Fatalf("工具声明轮切点应保留 2 条: %d", got)
	}
}

// TestRollbackAtTimestamp 按块时间戳回滚——非前文派生块切点只清界面块，前文按时刻截断。
func TestRollbackAtTimestamp(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSessionInSessions(t, dir, "cat-rollback-at")
	s.Context.SetSystemPrompt("系统词")
	s.Context.AddUserMessage("问题一")
	s.Context.AddAssistantMessage("回答一")
	msgs := s.Context.Messages()
	for i, msg := range msgs {
		switch msg.Role {
		case RoleUser:
			s.Views.OnUserMessage(msg, i, "")
		case RoleAssistant:
			s.Views.OnAssistantText(msg, i, 0)
		}
	}
	lastTs := msgs[len(msgs)-1].CreatedAt
	s.Views.AppendRoundSummary(`{"text":"轮末"}`, lastTs+5)
	if blocks := s.Views.Blocks(); len(blocks) != 3 {
		t.Fatalf("前置：应有 3 块: %d", len(blocks))
	}
	if got := s.RollbackAt(lastTs + 5); !strings.HasPrefix(got, "rollbackat | ") {
		t.Fatalf("按时间戳回滚应受理: %q", got)
	}
	if got := s.Context.Count(); got != len(msgs) {
		t.Fatalf("前文应全保留（消息时刻早于切点）: %d", got)
	}
	if blocks := s.Views.Blocks(); len(blocks) != 2 {
		t.Fatalf("视图应只丢轮末块: %d", len(blocks))
	}
	if got := s.RollbackAt(0); !strings.HasPrefix(got, "ERR|ROLLBACK_INDEX|") {
		t.Fatalf("时间戳 0 应拒绝: %q", got)
	}
}
