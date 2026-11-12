package admin

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cathome5/host/config"
)

// appearanceCreateID 发一次新建并取回生成的配置身份。
func appearanceCreateID(t *testing.T, deps *Deps, body string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	deps.handleAppearanceApisPost(rec, httptest.NewRequest("POST", "/api/v1/appearance-apis", strings.NewReader(body)))
	var got struct {
		OK           bool   `json:"ok"`
		AppearanceID string `json:"appearanceId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("新建回执解析失败: %v（原始 %s）", err, rec.Body.String())
	}
	if !got.OK || got.AppearanceID == "" {
		t.Fatalf("新建回执异常: %s", rec.Body.String())
	}
	return got.AppearanceID
}

// TestAppearanceApisGetEmpty 空池——version + 空数组（无 ok 字段，与 C# 同形）。
func TestAppearanceApisGetEmpty(t *testing.T) {
	deps, _ := newTestDeps(t)
	rec := httptest.NewRecorder()
	deps.handleAppearanceApisGet(rec, httptest.NewRequest("GET", "/api/v1/appearance-apis", nil))
	want := `{"version":1,"items":[]}`
	if got := rec.Body.String(); got != want {
		t.Fatalf("空池读取 = %s\n预期 = %s", got, want)
	}
}

// TestAppearanceApisCreateFirstBecomesDefault 首条目自动成默认（唯一外观必然被跟随默认的猫消费）。
func TestAppearanceApisCreateFirstBecomesDefault(t *testing.T) {
	deps, _ := newTestDeps(t)
	id := appearanceCreateID(t, deps, `{"displayName":"默认外观","petKind":"cat","ttsStyle":"zf_072","roundSummary":true,"summaryAudio":true}`)
	rec := httptest.NewRecorder()
	deps.handleAppearanceApisGet(rec, httptest.NewRequest("GET", "/api/v1/appearance-apis", nil))
	want := `{"version":1,"items":[{"appearanceId":"` + id + `","displayName":"默认外观","petKind":"cat","ttsStyle":"zf_072","roundSummary":true,"summaryAudio":true,"isDefault":true}]}`
	if got := rec.Body.String(); got != want {
		t.Fatalf("池读取 = %s\n预期 = %s", got, want)
	}
}

// TestAppearanceApisCreateRejects 入口面零容忍——非 JSON / 名称为空。
func TestAppearanceApisCreateRejects(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"非 JSON", `not-json`, `{"ok":false,"error":"body 非 JSON"}`},
		{"名称空", `{"displayName":""}`, `{"ok":false,"error":"displayName 为空"}`},
	}
	for _, testCase := range cases {
		deps, _ := newTestDeps(t)
		rec := httptest.NewRecorder()
		deps.handleAppearanceApisPost(rec, httptest.NewRequest("POST", "/api/v1/appearance-apis", strings.NewReader(testCase.body)))
		if got := rec.Body.String(); got != testCase.want {
			t.Fatalf("%s：回执 = %s\n预期 = %s", testCase.name, got, testCase.want)
		}
	}
}

// TestAppearanceApisEditKeepsDefault 编辑保默认标记（池级属性不被条目内容覆盖）。
func TestAppearanceApisEditKeepsDefault(t *testing.T) {
	deps, _ := newTestDeps(t)
	id := appearanceCreateID(t, deps, `{"displayName":"旧名"}`)
	rec := httptest.NewRecorder()
	deps.handleAppearanceApisEdit(rec, httptest.NewRequest("POST", "/api/v1/appearance-apis/edit",
		strings.NewReader(`{"appearanceId":"`+id+`","displayName":"新名","petKind":"dog"}`)))
	if got := rec.Body.String(); got != `{"ok":true,"appearanceId":"`+id+`"}` {
		t.Fatalf("编辑回执 = %s", got)
	}
	pool := config.LoadAppearancePool(deps.appearancePoolPath())
	if len(pool) != 1 || pool[0].DisplayName != "新名" || pool[0].PetKind != "dog" || !pool[0].IsDefault {
		t.Fatalf("编辑后条目不符: %+v", pool)
	}
	// 未命中 / 非法身份
	rec = httptest.NewRecorder()
	deps.handleAppearanceApisEdit(rec, httptest.NewRequest("POST", "/api/v1/appearance-apis/edit",
		strings.NewReader(`{"appearanceId":"11111111-1111-1111-1111-111111111111","displayName":"x"}`)))
	if got := rec.Body.String(); got != `{"ok":false,"error":"外观不存在"}` {
		t.Fatalf("未命中回执 = %s", got)
	}
	rec = httptest.NewRecorder()
	deps.handleAppearanceApisEdit(rec, httptest.NewRequest("POST", "/api/v1/appearance-apis/edit",
		strings.NewReader(`{"appearanceId":"bad","displayName":"x"}`)))
	if got := rec.Body.String(); got != `{"ok":false,"error":"appearanceId 非法"}` {
		t.Fatalf("非法身份回执 = %s", got)
	}
}

// TestAppearanceApisDefaultAndDelete 设默认唯一 / 删除命中与未命中。
func TestAppearanceApisDefaultAndDelete(t *testing.T) {
	deps, _ := newTestDeps(t)
	first := appearanceCreateID(t, deps, `{"displayName":"甲"}`)
	second := appearanceCreateID(t, deps, `{"displayName":"乙"}`)
	rec := httptest.NewRecorder()
	deps.handleAppearanceApisDefault(rec, httptest.NewRequest("POST", "/api/v1/appearance-apis/default",
		strings.NewReader(`{"appearanceId":"`+second+`"}`)))
	if got := rec.Body.String(); got != `{"ok":true}` {
		t.Fatalf("设默认回执 = %s", got)
	}
	pool := config.LoadAppearancePool(deps.appearancePoolPath())
	for _, cfg := range pool {
		if cfg.AppearanceID == second && !cfg.IsDefault {
			t.Fatalf("目标未成默认: %+v", cfg)
		}
		if cfg.AppearanceID == first && cfg.IsDefault {
			t.Fatalf("旧默认未清除: %+v", cfg)
		}
	}
	rec = httptest.NewRecorder()
	deps.handleAppearanceApisDelete(rec, httptest.NewRequest("POST", "/api/v1/appearance-apis/delete",
		strings.NewReader(`{"appearanceId":"`+first+`"}`)))
	if got := rec.Body.String(); got != `{"ok":true,"appearanceId":"`+first+`"}` {
		t.Fatalf("删除回执 = %s", got)
	}
	rec = httptest.NewRecorder()
	deps.handleAppearanceApisDelete(rec, httptest.NewRequest("POST", "/api/v1/appearance-apis/delete",
		strings.NewReader(`{"appearanceId":"`+first+`"}`)))
	if got := rec.Body.String(); got != `{"ok":false,"error":"外观不存在"}` {
		t.Fatalf("删除未命中回执 = %s", got)
	}
}

// TestAppearancePoolOrderMatchesGuidCompareTo 池序 = Guid.CompareTo 序（非文本序）。
//
// 0x8000_0000 起首的 GUID 作为 int32 是负数——排在 0 起首之前；文本序恰好相反。
func TestAppearancePoolOrderMatchesGuidCompareTo(t *testing.T) {
	deps, _ := newTestDeps(t)
	seed := `[
  {"schemaVersion":1,"appearanceId":"00000000-0000-0000-0000-000000000002","displayName":"零号","petKind":"","ttsStyle":"","roundSummary":false,"summaryAudio":false,"isDefault":true},
  {"schemaVersion":1,"appearanceId":"80000000-0000-0000-0000-000000000001","displayName":"负号","petKind":"","ttsStyle":"","roundSummary":false,"summaryAudio":false,"isDefault":false}
]`
	if err := os.WriteFile(filepath.Join(deps.DataRoot, "config", "appearance.json"), []byte(seed), 0o644); err != nil {
		t.Fatalf("写池失败: %v", err)
	}
	pool := config.LoadAppearancePool(deps.appearancePoolPath())
	if len(pool) != 2 {
		t.Fatalf("池条目数 = %d", len(pool))
	}
	if pool[0].DisplayName != "负号" || pool[1].DisplayName != "零号" {
		t.Fatalf("池序不符 Guid.CompareTo: %s / %s", pool[0].DisplayName, pool[1].DisplayName)
	}
}

// TestHostRestartRequestAndWithdraw 重启登记——停机态置位 / 撤销后复位（执行面未接线时的唯一出口）。
func TestHostRestartRequestAndWithdraw(t *testing.T) {
	deps, _ := newTestDeps(t)
	state := NewRestartState(nil)
	deps.Restart = state
	rec := httptest.NewRecorder()
	deps.handleHostRestartPost(rec, httptest.NewRequest("POST", "/api/v1/host-restart", strings.NewReader(`{"mode":"host"}`)))
	want := `{"ok":true,"mode":"host","note":"已登记——全局空闲后执行重启（闸门超时会自动撤销）"}`
	if got := rec.Body.String(); got != want {
		t.Fatalf("重启回执 = %s\n预期 = %s", got, want)
	}
	if !state.Requested() {
		t.Fatal("登记后停机态未置位")
	}
	state.withdraw()
	if state.Requested() {
		t.Fatal("撤销后停机态未复位")
	}
	// 非法档位
	rec = httptest.NewRecorder()
	deps.handleHostRestartPost(rec, httptest.NewRequest("POST", "/api/v1/host-restart", strings.NewReader(`{"mode":"nope"}`)))
	if got := rec.Body.String(); got != `{"ok":false,"error":"mode 非法（只接受 host / incr / full）: nope"}` {
		t.Fatalf("非法档位回执 = %s", got)
	}
	// 非 JSON
	rec = httptest.NewRecorder()
	deps.handleHostRestartPost(rec, httptest.NewRequest("POST", "/api/v1/host-restart", strings.NewReader(`x`)))
	if got := rec.Body.String(); got != `{"ok":false,"error":"body 非 JSON"}` {
		t.Fatalf("非 JSON 回执 = %s", got)
	}
}
