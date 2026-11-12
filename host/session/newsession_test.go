package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestSessionInSessions 建立标准数据根布局下的会话——sessions/<id>/ 结构（归档落点推导依赖它）。
func newTestSessionInSessions(t *testing.T, dir, catKey string) *Session {
	t.Helper()
	sessionDir := filepath.Join(dir, "sessions", catKey)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := NewContext()
	store := NewSessionStore(filepath.Join(sessionDir, catKey+".jsonl"))
	views := NewViewStore(filepath.Join(sessionDir, catKey+".view.jsonl"))
	return NewSession(catKey, ctx, store, views)
}

// TestRebuildClearsContextAndView 重建半边——清前文只留 system / 视图清空 / 注入报告落视图。
func TestRebuildClearsContextAndView(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSessionInSessions(t, dir, "cat-rebuild")
	s.SetDisplayName("猫甲")
	// 旧会话内容——前文两轮 + 视图两块
	s.Context.AddUserMessage("旧问题")
	s.Context.AddAssistantMessage("旧回复")
	s.Views.Load()
	s.Views.OnUserMessage(s.Context.Messages()[0], 0, "")

	oldID := s.SessionInstanceID()
	s.SetContextTokens(1234)

	summary := s.Rebuild(SessionNewOptions{
		DisplayName:      "猫甲",
		ToolSpecs:        []ToolSpec{{Name: "text-read", Description: "读", ParametersJSON: "{}"}},
		SystemPrompt:     "基础角色。\r\n\r\n【角色设定】猫娘",
		InjectReportJSON: `{"total":0}`,
		InjectCount:      2,
	})
	if summary != "会话已重建：注入 2 个知识文件，前文已清空" {
		t.Fatalf("摘要不符: %q", summary)
	}
	// 前文——只留 system
	messages := s.Context.Messages()
	if len(messages) != 1 || messages[0].Role != RoleSystem || messages[0].Content != "基础角色。\r\n\r\n【角色设定】猫娘" {
		t.Fatalf("前文未清或系统提示词不符: %+v", messages)
	}
	// 视图——只剩注入报告块
	blocks := s.Views.Blocks()
	if len(blocks) != 1 || blocks[0].RenderType != viewInjectReportType || blocks[0].Payload != `{"total":0}` {
		t.Fatalf("视图未清或注入报告未落: %+v", blocks)
	}
	if blocks[0].Timestamp != 0 || blocks[0].MsgIndex != -1 {
		t.Fatalf("注入报告块应 ts=0 / msgIndex=-1: %+v", blocks[0])
	}
	// 统计重置
	if s.SessionInstanceID() == oldID {
		t.Fatalf("会话实例 ID 未更新")
	}
	if s.ContextTokens() != 0 {
		t.Fatalf("前文长度未归零: %d", s.ContextTokens())
	}
	// 视图文件落盘——一行注入报告
	raw, err := os.ReadFile(s.Views.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"inject_report"`) {
		t.Fatalf("视图文件未落注入报告: %s", raw)
	}
}

// TestRebuildArchiveBeforeClear 归档在视图清空前——旧 user 消息进留档；新注入报告不进档。
func TestRebuildArchiveBeforeClear(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSessionInSessions(t, dir, "cat-archive")
	s.SetDisplayName("猫甲")
	s.Views.Load()
	userMsg := s.Context.AddUserMessage("必须进档的旧问题")
	s.Views.OnUserMessage(*userMsg, 0, "")
	s.Context.AddAssistantMessage("必须进档的旧回复")
	s.Views.OnAssistantText(s.Context.Messages()[1], 1, 0)

	s.Rebuild(SessionNewOptions{
		ToolSpecs:        nil,
		SystemPrompt:     "SYS",
		InjectReportJSON: `{"total":1}`,
		InjectCount:      1,
	})

	archives := FindLatestArchive(filepath.Join(dir, "sessions_old"), "cat-archive")
	if archives == "" {
		t.Fatalf("归档未生成")
	}
	raw, err := os.ReadFile(archives)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "必须进档的旧问题") || !strings.Contains(text, "必须进档的旧回复") {
		t.Fatalf("归档未包含旧会话内容:\n%s", text)
	}
	if strings.Contains(text, `"total":1`) {
		t.Fatalf("新注入报告不应进留档:\n%s", text)
	}
	// 归档后视图已清空 → 只剩新注入报告
	blocks := s.Views.Blocks()
	if len(blocks) != 1 || blocks[0].RenderType != viewInjectReportType {
		t.Fatalf("重建后视图应为单条注入报告: %+v", blocks)
	}
}

// TestRebuildPushReset 重置事件——ViewBus 置全量待发标记（未 Attach 时只记标记）。
func TestRebuildPushReset(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSessionInSessions(t, dir, "cat-reset")
	s.ViewBus.persist = []*ViewBlock{{RenderType: TypeUser}}
	s.ViewBus.pendingAppend = []*ViewBlock{{RenderType: TypeUser}}

	s.Rebuild(SessionNewOptions{SystemPrompt: "SYS"})
	if s.ViewBus.persist != nil || s.ViewBus.pendingAppend != nil || !s.ViewBus.fullPending {
		t.Fatalf("重置后应清两区并置全量待发: %+v", s.ViewBus)
	}
	// 未 Attach → TryTakeFrame 不外发
	if _, ok := s.ViewBus.TryTakeFrame(); ok {
		t.Fatalf("未 Attach 时不应出帧")
	}
}

// TestRebuildDisplayNameKeep 显示名空 = 保持原值。
func TestRebuildDisplayNameKeep(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSessionInSessions(t, dir, "cat-name")
	s.SetDisplayName("旧名")
	s.Rebuild(SessionNewOptions{DisplayName: "", SystemPrompt: "SYS"})
	if got := s.DisplayNameSnapshot(); got != "旧名" {
		t.Fatalf("空显示名应保持原值，got=%q", got)
	}
	s.Rebuild(SessionNewOptions{DisplayName: "新名", SystemPrompt: "SYS"})
	if got := s.DisplayNameSnapshot(); got != "新名" {
		t.Fatalf("显示名未更新，got=%q", got)
	}
}
