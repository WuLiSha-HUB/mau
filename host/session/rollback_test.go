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

// TestRollbackRejects 三类拒绝——非正式回复 / 索引越界 / 会话忙（校验顺序先忙后索引）。
func TestRollbackRejects(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSessionInSessions(t, dir, "cat-rollback-reject")
	s.Context.SetSystemPrompt("系统词")
	s.Context.AddUserMessage("问题一")
	s.Context.AddAssistantMessage("回答一")

	if got := s.Rollback(1); !strings.HasPrefix(got, "ERR|ROLLBACK_NODE|") {
		t.Fatalf("user 节点应拒绝: %q", got)
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
