package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withSessionDirs 接管落盘面（pending 目录 / 元数据路径 / 特权钩子 / 停机态）并在收尾恢复。
func withSessionDirs(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prevPending := PendingStoreDir()
	prevRestart := HostRestarting
	prevMeta := SessionMetaPathProvider
	prevPriv := IsPrivilegedTool
	ConfigurePendingStore(dir)
	HostRestarting = func() bool { return false }
	SessionMetaPathProvider = func(catID string) string {
		return filepath.Join(dir, catID+".session.json")
	}
	IsPrivilegedTool = func(name string) bool { return name == "majordomo-cmd" }
	t.Cleanup(func() {
		ConfigurePendingStore(prevPending)
		HostRestarting = prevRestart
		SessionMetaPathProvider = prevMeta
		IsPrivilegedTool = prevPriv
	})
	return dir
}

// newTestSession 建立带落盘面的会话。
func newTestSession(t *testing.T, dir, catKey string) *Session {
	t.Helper()
	ctx := NewContext()
	store := NewSessionStore(filepath.Join(dir, catKey+".jsonl"))
	views := NewViewStore(filepath.Join(dir, catKey+".view.jsonl"))
	return NewSession(catKey, ctx, store, views)
}

// TestSessionMetaRoundTrip 元数据落盘与恢复（Tools / Note / 统计）。
func TestSessionMetaRoundTrip(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSession(t, dir, "cat-meta")
	s.SetDisplayName("猫甲")
	s.SetToolSpecs([]ToolSpec{{Name: "text-read", Description: "读", ParametersJSON: "{}"}})
	s.Note.Execute(`{"action":"set","content":["a","b"]}`)
	s.Note.Execute("")
	s.SaveMeta()

	raw, err := os.ReadFile(filepath.Join(dir, "cat-meta.session.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{`"CatId": "cat-meta"`, `"DisplayName": "猫甲"`, `"Name": "text-read"`, `"Tasks": [`, `"a",`, `"Current": 1`, `"Done": 1`} {
		if !strings.Contains(text, want) {
			t.Fatalf("元数据缺字段 %q:\n%s", want, text)
		}
	}

	restored := newTestSession(t, dir, "cat-meta")
	if got := restored.SessionInstanceID(); got == "" {
		t.Fatal("应恢复会话实例 ID")
	}
	if tasks, current, done := restored.Note.Snapshot(); len(tasks) != 2 || current != 1 || done != 1 {
		t.Fatalf("Note 应随元数据恢复: %v %d %d", tasks, current, done)
	}
}

// TestSessionToolSnapshot 注入面快照——特权过滤 / 落盘 / 退役剔除。
func TestSessionToolSnapshot(t *testing.T) {
	dir := withSessionDirs(t)
	specs := []ToolSpec{
		{Name: "text-read", ParametersJSON: "{}"},
		{Name: "majordomo-cmd", ParametersJSON: "{}"},
	}
	// 非默认会话：特权工具剔除
	other := newTestSession(t, dir, "cat-other")
	other.SetToolSpecs(specs)
	if got := other.DescribeInjection(); got != "text-read" {
		t.Fatalf("非默认会话应剔除特权工具: %s", got)
	}
	// 默认会话：原样保留
	def := newTestSession(t, dir, "majordomo")
	def.SetToolSpecs(specs)
	if got := def.DescribeInjection(); got != "text-read,majordomo-cmd" {
		t.Fatalf("默认会话应保留特权工具: %s", got)
	}
	// 快照解析——池内存在的保留，退役剔除
	loaded := ResolveSessionTools("majordomo", []ToolSpec{{Name: "text-read"}})
	if len(loaded) != 1 || loaded[0].Name != "text-read" {
		t.Fatalf("退役工具应剔除: %+v", loaded)
	}
	// 无快照 → 回落池面
	pool := []ToolSpec{{Name: "a"}, {Name: "b"}}
	if got := ResolveSessionTools("cat-none", pool); len(got) != 2 {
		t.Fatalf("无快照应回落池面: %+v", got)
	}
}

// TestSessionAppendInjection 注入落前文 + 视图路径分派。
func TestSessionAppendInjection(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSession(t, dir, "cat-inj")
	s.AppendInjection("人说的话", SourceUser, "", "")
	s.AppendInjection("系统话", SysKindTimer, "", "")
	msgs := s.Context.Messages()
	if len(msgs) != 2 {
		t.Fatalf("前文应为 2 条，实得 %d", len(msgs))
	}
	if msgs[0].Content != "人说的话" || msgs[1].Content != SysInjectPrefix+"系统话" {
		t.Fatalf("sys 注入应套固定前缀: %+v", msgs)
	}
	blocks := s.Views.Blocks()
	if blocks[0].RenderType != TypeUser || blocks[1].RenderType != TypeSystemAutoMessage {
		t.Fatalf("视图块类型分派不符: %+v", blocks)
	}
	// 落盘——前文 jsonl 含两条消息行
	loaded, ok := NewSessionStore(filepath.Join(dir, "cat-inj.jsonl")).TryLoad()
	if !ok || len(loaded) != 2 {
		t.Fatalf("前文应已落盘: ok=%v len=%d", ok, len(loaded))
	}
}

// TestSessionDrainPending 轮首取尽——双队列按到达序插入前文。
func TestSessionDrainPending(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSession(t, dir, "cat-drain")
	s.Pending.Post("人工一", SourceUser, "", "")
	s.Pending.PostSystem(SysKindSystemAuto, "系统一", "")
	s.Pending.Post("人工二", SourceUser, "", "")
	if !s.DrainPendingIntoRound(true) {
		t.Fatal("有排队消息应返回 true")
	}
	msgs := s.Context.Messages()
	if len(msgs) != 3 {
		t.Fatalf("应插入 3 条，实得 %d", len(msgs))
	}
	order := []string{msgs[0].Content, msgs[1].Content, msgs[2].Content}
	want := []string{"人工一", SysInjectPrefix + "系统一", "人工二"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("到达序不符: %v，期望 %v", order, want)
		}
	}
	if s.DrainPendingIntoRound(true) {
		t.Fatal("队列已空应返回 false")
	}
}
