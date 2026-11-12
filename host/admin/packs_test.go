package admin

import (
	"net/http/httptest"
	"strings"
	"testing"

	"cathome5/host/config"
)

// TestPacksGetShape 池读取——ok + packs[key,desc,paths]（池声明序）。
func TestPacksGetShape(t *testing.T) {
	deps, _ := newTestDeps(t)
	rec := httptest.NewRecorder()
	deps.handlePacksGet(rec, httptest.NewRequest("GET", "/api/v1/packs", nil))
	want := `{"ok":true,"packs":[{"key":"overwork","desc":"收工加载包","paths":["ccbp:L2"]}]}`
	if got := rec.Body.String(); got != want {
		t.Fatalf("池读取 = %s\n预期 = %s", got, want)
	}
}

// TestPacksPostCreateAndOverwrite 新建 + 同名覆盖（增改一体，count 不增）。
func TestPacksPostCreateAndOverwrite(t *testing.T) {
	deps, _ := newTestDeps(t)
	rec := httptest.NewRecorder()
	deps.handlePacksPost(rec, httptest.NewRequest("POST", "/api/v1/packs",
		strings.NewReader(`{"key":"demo","desc":"演示","paths":["ccbp:L1","ccbp:L2"]}`)))
	if got := rec.Body.String(); got != `{"ok":true,"key":"demo","count":2}` {
		t.Fatalf("新建回执 = %s", got)
	}
	// 同名覆盖——count 保持 2，desc 与 paths 更新
	rec = httptest.NewRecorder()
	deps.handlePacksPost(rec, httptest.NewRequest("POST", "/api/v1/packs",
		strings.NewReader(`{"key":"demo","desc":"演示2","paths":["ccbp:L3"]}`)))
	if got := rec.Body.String(); got != `{"ok":true,"key":"demo","count":2}` {
		t.Fatalf("覆盖回执 = %s", got)
	}
	pool := config.LoadPackPool(config.PackPoolPath(deps.DataRoot))
	if len(pool) != 2 {
		t.Fatalf("池条目数 = %d，预期 2", len(pool))
	}
	if pool[1].Desc != "演示2" || len(pool[1].Paths) != 1 || pool[1].Paths[0] != "ccbp:L3" {
		t.Fatalf("覆盖后条目不符: %+v", pool[1])
	}
}

// TestPacksPostRejects 校验面（body 非 JSON / key 空 / paths 空）。
func TestPacksPostRejects(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"非 JSON", `not-json`, `{"ok":false,"error":"body 非 JSON"}`},
		{"key 空", `{"key":"  ","paths":["a"]}`, `{"ok":false,"error":"key 为空"}`},
		{"paths 空", `{"key":"demo","paths":[]}`, `{"ok":false,"error":"paths 为空（包至少一个路径项）"}`},
		{"paths 全空白", `{"key":"demo","paths":["  ",""]}`, `{"ok":false,"error":"paths 为空（包至少一个路径项）"}`},
	}
	for _, testCase := range cases {
		deps, _ := newTestDeps(t)
		rec := httptest.NewRecorder()
		deps.handlePacksPost(rec, httptest.NewRequest("POST", "/api/v1/packs", strings.NewReader(testCase.body)))
		if got := rec.Body.String(); got != testCase.want {
			t.Fatalf("%s：回执 = %s\n预期 = %s", testCase.name, got, testCase.want)
		}
	}
}

// TestPacksDelete 删除命中与未命中。
func TestPacksDelete(t *testing.T) {
	deps, _ := newTestDeps(t)
	rec := httptest.NewRecorder()
	deps.handlePacksDelete(rec, httptest.NewRequest("POST", "/api/v1/packs/delete",
		strings.NewReader(`{"key":"overwork"}`)))
	if got := rec.Body.String(); got != `{"ok":true,"key":"overwork","count":0}` {
		t.Fatalf("删除回执 = %s", got)
	}
	rec = httptest.NewRecorder()
	deps.handlePacksDelete(rec, httptest.NewRequest("POST", "/api/v1/packs/delete",
		strings.NewReader(`{"key":"nope"}`)))
	if got := rec.Body.String(); got != `{"ok":false,"error":"包不存在: nope"}` {
		t.Fatalf("删除未命中回执 = %s", got)
	}
}

// TestPacksPoolRoundTrip 落盘可被读面解析回来（写入唯一出口与解析同构）。
func TestPacksPoolRoundTrip(t *testing.T) {
	deps, _ := newTestDeps(t)
	rec := httptest.NewRecorder()
	deps.handlePacksPost(rec, httptest.NewRequest("POST", "/api/v1/packs",
		strings.NewReader(`{"key":"round","desc":"往返","paths":["workspace:a","workspace:b"]}`)))
	pool := config.LoadPackPool(config.PackPoolPath(deps.DataRoot))
	if len(pool) != 2 || pool[1].Key != "round" || pool[1].Desc != "往返" || len(pool[1].Paths) != 2 {
		t.Fatalf("往返解析不符: %+v", pool)
	}
}
