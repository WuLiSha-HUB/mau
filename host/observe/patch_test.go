package observe

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// patchTestBuilder 构造测试用 Builder——各段可由测试替换（Frame 固定 42）。
func patchTestBuilder() *Builder {
	return &Builder{
		Frame:    func() int64 { return 42 },
		Cats:     func() []Cat { return []Cat{} },
		Sessions: func() []any { return []any{} },
		Tools:    func() []Tool { return []Tool{} },
		Boxes:    func() []Box { return []Box{} },
		OA:       func() (int, int, int, int) { return 0, 0, 0, 0 },
	}
}

// patchKeys 解析 patch JSON 的顶层键集合。
func patchKeys(t *testing.T, patch string) map[string]json.RawMessage {
	t.Helper()
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(patch), &payload); err != nil {
		t.Fatalf("patch 不是合法 JSON: %v | %s", err, patch)
	}
	return payload
}

// TestBuildPatchFirstFrameFullSegments 首帧带 cats / sessions / deps / oa / res；
// boxes 无变化不出现；frame 取全局帧号。
func TestBuildPatchFirstFrameFullSegments(t *testing.T) {
	b := patchTestBuilder()
	patch := b.BuildPatch()
	if patch == "" {
		t.Fatal("首帧应非空")
	}
	payload := patchKeys(t, patch)
	if string(payload["frame"]) != "42" {
		t.Fatalf("frame 应为 42，得到 %s", payload["frame"])
	}
	for _, key := range []string{"cats", "sessions", "deps", "oa", "res"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("首帧缺段 %s: %s", key, patch)
		}
	}
	if _, ok := payload["boxes"]; ok {
		t.Fatalf("boxes 无变化不应出现: %s", patch)
	}
}

// TestBuildPatchIdleSilent 无变化零推送（res 1 秒节拍内）。
func TestBuildPatchIdleSilent(t *testing.T) {
	b := patchTestBuilder()
	_ = b.BuildPatch()
	if again := b.BuildPatch(); again != "" {
		t.Fatalf("无变化应零推送，得到: %s", again)
	}
}

// TestBuildPatchSessionsChange 会话段变化即带 sessions。
func TestBuildPatchSessionsChange(t *testing.T) {
	b := patchTestBuilder()
	sessions := []any{map[string]any{"phase": "Idle"}}
	b.Sessions = func() []any { return sessions }
	_ = b.BuildPatch()
	sessions = []any{map[string]any{"phase": "LlmRunning"}}
	patch := b.BuildPatch()
	if !strings.Contains(patch, `"sessions"`) {
		t.Fatalf("sessions 变化应带该段: %s", patch)
	}
}

// TestBuildPatchBoxesDiff boxes 新增进 set、消失进 del。
func TestBuildPatchBoxesDiff(t *testing.T) {
	b := patchTestBuilder()
	boxes := []Box{{Scope: "demo", Key: "a", T: "s", Value: "1"}}
	b.Boxes = func() []Box { return boxes }
	_ = b.BuildPatch()
	boxes = append(boxes, Box{Scope: "demo", Key: "b", T: "s", Value: "2"})
	payload := patchKeys(t, b.BuildPatch())
	boxesRaw, ok := payload["boxes"]
	if !ok {
		t.Fatal("boxes 新增应带该段")
	}
	if !strings.Contains(string(boxesRaw), `"set"`) || !strings.Contains(string(boxesRaw), `"b"`) {
		t.Fatalf("set 应含新增条目: %s", boxesRaw)
	}
	boxes = boxes[:1]
	payload = patchKeys(t, b.BuildPatch())
	boxesRaw, ok = payload["boxes"]
	if !ok {
		t.Fatal("boxes 删除应带该段")
	}
	if !strings.Contains(string(boxesRaw), `"del"`) || !strings.Contains(string(boxesRaw), `"b"`) {
		t.Fatalf("del 应含移除条目: %s", boxesRaw)
	}
}

// TestBuildPatchInternalBoxIgnored 内部盒（global.frame）变化不进 diff。
func TestBuildPatchInternalBoxIgnored(t *testing.T) {
	b := patchTestBuilder()
	boxes := []Box{{Scope: "global", Key: "frame", T: "n", Value: 1}}
	b.Boxes = func() []Box { return boxes }
	_ = b.BuildPatch()
	boxes = []Box{{Scope: "global", Key: "frame", T: "n", Value: 2}}
	if patch := b.BuildPatch(); patch != "" {
		t.Fatalf("内部盒变化不应推送: %s", patch)
	}
}

// TestBuildPatchResCadence res 按 1 秒节拍强制刷新（到点即带）。
func TestBuildPatchResCadence(t *testing.T) {
	b := patchTestBuilder()
	_ = b.BuildPatch()
	if patch := b.BuildPatch(); patch != "" {
		t.Fatalf("1 秒内不应重复带 res: %s", patch)
	}
	b.patch.lastResPush = time.Now().Add(-2 * time.Second)
	payload := patchKeys(t, b.BuildPatch())
	if _, ok := payload["res"]; !ok {
		t.Fatal("到点应带 res 段")
	}
}

// TestDepsStableJSONIgnoresProbedAt 稳定视图不含 probedAt（探测时刻变化不算内容变化）。
func TestDepsStableJSONIgnoresProbedAt(t *testing.T) {
	a := depsStableJSON([]DepResult{{Level: "required", Name: "go", State: "ok", Detail: "1.26", ProbedAt: 1}})
	b := depsStableJSON([]DepResult{{Level: "required", Name: "go", State: "ok", Detail: "1.26", ProbedAt: 999}})
	if a != b {
		t.Fatalf("probedAt 不应进入稳定视图: %s != %s", a, b)
	}
	if strings.Contains(a, "probedAt") {
		t.Fatalf("稳定视图不应含 probedAt 键: %s", a)
	}
}

// TestIsInternalBox 内部实现盒判定面。
func TestIsInternalBox(t *testing.T) {
	cases := []struct {
		scope string
		key   string
		want  bool
	}{
		{"log", "entries", true},
		{"log", "sync", true},
		{"global", "frame", true},
		{"demo", "a", false},
		{"log", "other", false},
	}
	for _, c := range cases {
		if got := isInternalBox(c.scope, c.key); got != c.want {
			t.Fatalf("isInternalBox(%q,%q)=%v 期望 %v", c.scope, c.key, got, c.want)
		}
	}
}
