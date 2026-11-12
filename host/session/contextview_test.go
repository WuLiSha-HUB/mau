package session

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildContextView 前文条目视图——头部字段 / 首条恒在窗口 / UTF-16 长度。
func TestBuildContextView(t *testing.T) {
	ctx := NewContext()
	ctx.AddUserMessage("你好")
	ctx.AddAssistantMessage("回复😀")
	sess := NewSession("", ctx, nil, nil)

	view := decodeView(t, sess.BuildContextView(0))
	if view["count"] != float64(2) || view["start"] != float64(1) || view["shown"] != float64(2) {
		t.Fatalf("不限窗口：头部不符 %+v", view)
	}
	items := view["items"].([]any)
	second := items[1].(map[string]any)
	// 「回复😀」= 2 码元 + emoji 代理对 2 码元 = 4（C# string.Length 口径）
	if second["chars"] != float64(4) {
		t.Fatalf("chars 期望 UTF-16 长度 4，实际 %v", second["chars"])
	}
	if second["role"] != "assistant" {
		t.Fatalf("role 期望 assistant，实际 %v", second["role"])
	}

	// max=1：取尾部 1 条 + 恒含首条（i=1 与 i=2 都在窗口内）
	view = decodeView(t, sess.BuildContextView(1))
	if view["start"] != float64(2) || view["shown"] != float64(2) {
		t.Fatalf("max=1：头部不符 %+v", view)
	}
	items = view["items"].([]any)
	if items[0].(map[string]any)["i"] != float64(1) || items[1].(map[string]any)["i"] != float64(2) {
		t.Fatalf("max=1：窗口条目不符 %+v", items)
	}
}

// TestBuildKeyInfoView 关键信息视图——report / user / reply / roundsum 四类条目。
func TestBuildKeyInfoView(t *testing.T) {
	dir := t.TempDir()
	views := NewViewStore(filepath.Join(dir, "v.jsonl"))
	views.SetInjectReport(`{"files":[{"file":"a.md","status":"ok","chars":10}],"total":1,"ok":1,"missing":0,"failed":0,"toolGroups":[]}`)
	views.OnUserMessage(Message{Role: RoleUser, Content: "你好", CreatedAt: 111}, 0, "")
	views.OnAssistantText(Message{Role: RoleAssistant, Content: "回复", CreatedAt: 222}, 1, 0)
	views.AppendRoundSummary(`{"data":{"prompt":10,"completion":5,"cacheHit":2,"toolCount":1,"requests":1,"elapsedMs":1500}}`, 333)

	ctx := NewContext()
	sess := NewSession("", ctx, nil, views)
	view := decodeView(t, sess.BuildKeyInfoView(0))
	if view["count"] != float64(4) {
		t.Fatalf("count 期望 4（report + user + reply + roundsum），实际 %v", view["count"])
	}
	items := view["items"].([]any)
	if items[0].(map[string]any)["role"] != "report" {
		t.Fatalf("首条期望 report，实际 %+v", items[0])
	}
	if !strings.Contains(items[0].(map[string]any)["content"].(string), "注入清单：1 个文件") {
		t.Fatalf("report 正文不符 %q", items[0].(map[string]any)["content"])
	}
	if items[1].(map[string]any)["role"] != "user" || items[2].(map[string]any)["role"] != "reply" {
		t.Fatalf("user / reply 顺序不符 %+v", items)
	}
	if items[3].(map[string]any)["role"] != "roundsum" {
		t.Fatalf("末条期望 roundsum，实际 %+v", items[3])
	}
	if !strings.Contains(items[3].(map[string]any)["content"].(string), "用时 1.5 秒") {
		t.Fatalf("roundsum 行不符 %q", items[3].(map[string]any)["content"])
	}
}

// TestBuildFullContextView 完整前文视图——无实测回落缺省 ratio（estimated=true）。
func TestBuildFullContextView(t *testing.T) {
	ctx := NewContext()
	ctx.AddUserMessage(strings.Repeat("a", 100))
	sess := NewSession("", ctx, nil, nil)
	view := decodeView(t, sess.BuildFullContextView(0))
	if view["ok"] != true || view["count"] != float64(1) || view["chars"] != float64(100) {
		t.Fatalf("头部不符 %+v", view)
	}
	if view["ratio"] != float64(2.4) || view["estimated"] != true {
		t.Fatalf("ratio / estimated 不符 %+v", view)
	}
	// tokens = 100 ÷ 2.4 = 41.67 → 41
	if view["tokens"] != float64(41) {
		t.Fatalf("tokens 期望 41，实际 %v", view["tokens"])
	}
}

// fakeAPIRole 端点角色桩——session 包测试不依赖 llm 包（依赖方向 llm → session，反向会成环）。
type fakeAPIRole struct {
	// backup 当前角色（true=备用）
	backup bool
}

// EffectiveUseBackup 生效角色。
func (f *fakeAPIRole) EffectiveUseBackup() bool { return f.backup }

// ToggleManual 手动对调。
func (f *fakeAPIRole) ToggleManual() bool {
	f.backup = !f.backup
	return f.backup
}

// TestAPIRoleAndModelOverride 会话级端点角色与临时模型。
func TestAPIRoleAndModelOverride(t *testing.T) {
	sess := NewSession("", NewContext(), nil, nil)
	// 未接线角色面——恒主要、切换不翻转（C# ToggleApiRole 的 _apiRole == null 同判）
	if sess.APIRoleText() != "主要" || sess.APIRoleBackup() {
		t.Fatalf("未接线角色期望主要")
	}
	if role := sess.ToggleAPIRole(); role != "主要" {
		t.Fatalf("未接线切换期望主要（不翻转），实际 %q", role)
	}
	// 接线后——手动对调翻转
	sess.SetAPIRole(&fakeAPIRole{})
	if sess.APIRoleText() != "主要" || sess.APIRoleBackup() {
		t.Fatalf("接线初始角色期望主要")
	}
	if role := sess.ToggleAPIRole(); role != "备用" || !sess.APIRoleBackup() {
		t.Fatalf("首次对调期望备用，实际 %q", role)
	}
	if role := sess.ToggleAPIRole(); role != "主要" {
		t.Fatalf("二次对调期望主要，实际 %q", role)
	}
	if value := sess.SetModelOverride("  m1  "); value != "m1" || sess.ModelOverride() != "m1" {
		t.Fatalf("模型覆盖不符 %q / %q", value, sess.ModelOverride())
	}
	if value := sess.SetModelOverride(""); value != "" || sess.ModelOverride() != "" {
		t.Fatalf("清空覆盖不符 %q", value)
	}
}

// decodeView 解析视图 JSON 为 map。
func decodeView(t *testing.T, raw string) map[string]any {
	t.Helper()
	var view map[string]any
	if err := json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatalf("视图非 JSON：%v（%s）", err, raw)
	}
	return view
}
