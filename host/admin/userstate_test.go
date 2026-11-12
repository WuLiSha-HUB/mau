package admin

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cathome5/host/config"
)

// userSchema 造只含用户态三项的最小 schema（写面 SetChecked 的白名单来源）。
//
// 走 LoadSchema 而非字面量构造——Schema 的键索引在加载期建立，字面量构造查不到。
func userSchema(t *testing.T) *config.Schema {
	t.Helper()
	path := filepath.Join(t.TempDir(), "schema.json")
	body := `{"items":[
		{"key":"user.current","file":"user.cfg","default":"莎","sensitive":false,"writable":true,"type":"string","values":["莎","Rushiera"],"desc":"当前用户"},
		{"key":"user.qq_accept_nonuser","file":"user.cfg","default":"false","sensitive":false,"writable":true,"type":"bool","desc":"接受非用户指令"},
		{"key":"user.qq_ids","file":"user.cfg","default":"","sensitive":false,"writable":true,"type":"string","desc":"群内身份"}
	]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("写 schema 失败: %v", err)
	}
	schema, err := config.LoadSchema(path)
	if err != nil {
		t.Fatalf("加载 schema 失败: %v", err)
	}
	return schema
}

// newUserStateDeps 造带用户态 schema 的管理面数据源。
func newUserStateDeps(t *testing.T) *Deps {
	t.Helper()
	deps, _ := newTestDeps(t)
	deps.Schema = userSchema(t)
	deps.Store.RegisterSchema(deps.Schema)
	return deps
}

// TestUserStateGetDefaults 缺省态——当前用户回落档案首位 / 开关关 / 身份空。
func TestUserStateGetDefaults(t *testing.T) {
	deps := newUserStateDeps(t)
	rec := httptest.NewRecorder()
	deps.handleUserStateGet(rec, httptest.NewRequest("GET", "/api/v1/user-state", nil))
	want := `{"ok":true,"current":"莎","acceptNonUser":false,"qqIds":"","users":[` +
		`{"name":"莎","desc":"源实例——CatHome 4 与 CCBP 知识网络的搭建者"},` +
		`{"name":"Rushiera","desc":"莎的朋友——本支主理（2026-09-25 起）"}]}`
	if got := rec.Body.String(); got != want {
		t.Fatalf("缺省用户态 = %s\n预期 = %s", got, want)
	}
}

// TestUserStatePostWritesAndReads 字段级写入——三字段落盘后读面跟随（身份清单归一去空白）。
func TestUserStatePostWritesAndReads(t *testing.T) {
	deps := newUserStateDeps(t)
	rec := httptest.NewRecorder()
	deps.handleUserStatePost(rec, httptest.NewRequest("POST", "/api/v1/user-state",
		strings.NewReader(`{"current":"Rushiera","acceptNonUser":true,"qqIds":" a , b ,"}`)))
	want := `{"ok":true,"current":"Rushiera","acceptNonUser":true,"qqIds":"a,b"}`
	if got := rec.Body.String(); got != want {
		t.Fatalf("写回执 = %s\n预期 = %s", got, want)
	}
	rec = httptest.NewRecorder()
	deps.handleUserStateGet(rec, httptest.NewRequest("GET", "/api/v1/user-state", nil))
	if !strings.Contains(rec.Body.String(), `"current":"Rushiera","acceptNonUser":true,"qqIds":"a,b"`) {
		t.Fatalf("读面未跟随写入: %s", rec.Body.String())
	}
}

// TestUserStatePostPartialKeepsOthers 未提交字段逐字保留（qqIds 提交空串 = 清空）。
func TestUserStatePostPartialKeepsOthers(t *testing.T) {
	deps := newUserStateDeps(t)
	rec := httptest.NewRecorder()
	deps.handleUserStatePost(rec, httptest.NewRequest("POST", "/api/v1/user-state",
		strings.NewReader(`{"current":"Rushiera","qqIds":"x,y"}`)))
	if got := rec.Body.String(); got != `{"ok":true,"current":"Rushiera","acceptNonUser":false,"qqIds":"x,y"}` {
		t.Fatalf("首写回执 = %s", got)
	}
	rec = httptest.NewRecorder()
	deps.handleUserStatePost(rec, httptest.NewRequest("POST", "/api/v1/user-state",
		strings.NewReader(`{"qqIds":""}`)))
	if got := rec.Body.String(); got != `{"ok":true,"current":"Rushiera","acceptNonUser":false,"qqIds":""}` {
		t.Fatalf("清空回执 = %s", got)
	}
}

// TestUserStatePostRejects 未知用户 / 非 JSON。
func TestUserStatePostRejects(t *testing.T) {
	deps := newUserStateDeps(t)
	rec := httptest.NewRecorder()
	deps.handleUserStatePost(rec, httptest.NewRequest("POST", "/api/v1/user-state",
		strings.NewReader(`{"current":"路人"}`)))
	if got := rec.Body.String(); got != `{"ok":false,"error":"未知用户: 路人"}` {
		t.Fatalf("未知用户回执 = %s", got)
	}
	rec = httptest.NewRecorder()
	deps.handleUserStatePost(rec, httptest.NewRequest("POST", "/api/v1/user-state", strings.NewReader(`x`)))
	if got := rec.Body.String(); got != `{"ok":false,"error":"body 非 JSON"}` {
		t.Fatalf("非 JSON 回执 = %s", got)
	}
}
