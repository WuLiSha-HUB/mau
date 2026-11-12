package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCondRoutesNotBound 条件面未注入（管理面板实例）——11 条路由全部 404（构造期不挂载）。
func TestCondRoutesNotBound(t *testing.T) {
	server := New(Options{})
	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/context"},
		{http.MethodGet, "/api/v1/keyinfo"},
		{http.MethodGet, "/api/v1/fullctx"},
		{http.MethodGet, "/api/v1/effort"},
		{http.MethodPost, "/api/v1/effort"},
		{http.MethodPost, "/api/v1/api-role/toggle"},
		{http.MethodPost, "/api/v1/api-model"},
		{http.MethodGet, "/api/v1/inject/projects"},
		{http.MethodPost, "/api/v1/inject/project"},
		{http.MethodGet, "/api/v1/inject/auto"},
		{http.MethodPost, "/api/v1/inject/auto"},
	}
	for _, route := range routes {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(route.method, route.path, nil)
		server.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s %s 期望 404，实际 %d", route.method, route.path, recorder.Code)
		}
	}
}

// TestCondContextRoute 条件面注入后——?max= 解析（缺省 / 非法 = 0，正数 = 上限）。
func TestCondContextRoute(t *testing.T) {
	server := New(Options{})
	gotMax := -1
	server.BindContext(func(max int) string {
		gotMax = max
		return `{"ok":true}`
	})
	server.BindKeyInfo(func(max int) string { return `{"ok":true}` })
	server.BindFullContext(func(max int) string { return `{"ok":true}` })

	cases := []struct {
		query string
		want  int
	}{
		{"", 0},
		{"?max=5", 5},
		{"?max=abc", 0},
		{"?max=-3", 0},
	}
	for _, tc := range cases {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/context"+tc.query, nil)
		server.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%q 期望 200，实际 %d", tc.query, recorder.Code)
		}
		if gotMax != tc.want {
			t.Fatalf("%q 期望 max=%d，实际 %d", tc.query, tc.want, gotMax)
		}
	}
}

// TestEffortPostBody 思考强度写面——非法 body 零容忍；合法 body 交 setter。
func TestEffortPostBody(t *testing.T) {
	server := New(Options{})
	server.BindEffort(func() string { return `{"effort":"","global":"high"}` }, func(value string) string {
		return `{"ok":true,"effort":` + JSONQuote(value) + `}`
	})
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/effort", strings.NewReader("bad")))
	if !strings.Contains(recorder.Body.String(), "body 非 JSON 或缺少 value 字段") {
		t.Fatalf("非法 body 回执不符：%s", recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/effort", strings.NewReader(`{"value":"low"}`)))
	if !strings.Contains(recorder.Body.String(), `"effort":"low"`) {
		t.Fatalf("合法 body 回执不符：%s", recorder.Body.String())
	}
}

// TestAPIModelPostBody 临时模型写面——非法 body 零容忍。
func TestAPIModelPostBody(t *testing.T) {
	server := New(Options{})
	server.BindAPIModel(func(model string) string { return `{"ok":true,"model":` + JSONQuote(model) + `}` })
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/api-model", strings.NewReader(`{"nope":1}`)))
	if !strings.Contains(recorder.Body.String(), "body 非 JSON 或缺少 model 字段") {
		t.Fatalf("非法 body 回执不符：%s", recorder.Body.String())
	}
}

// TestInjectProjectPostBody 项目挂载写面——非法 body 回执键 = reason。
func TestInjectProjectPostBody(t *testing.T) {
	server := New(Options{})
	server.BindInject(
		func() string { return `{"ok":true}` },
		func(cat, project string, expert *bool) string {
			return `{"ok":true,"cat":` + JSONQuote(cat) + `}`
		},
		func(cat string) string { return `{"ok":true,"cat":"majordomo","on":true}` },
		func(cat string, on *bool) string { return `{"ok":true,"cat":"majordomo","on":true}` },
	)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/inject/project", strings.NewReader("bad")))
	if !strings.Contains(recorder.Body.String(), `"reason":"body 非 JSON"`) {
		t.Fatalf("非法 body 回执不符：%s", recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/inject/project", strings.NewReader(`{"cat":"majordomo"}`)))
	if !strings.Contains(recorder.Body.String(), `"cat":"majordomo"`) {
		t.Fatalf("合法 body 回执不符：%s", recorder.Body.String())
	}
}

// JSONQuote 测试用 JSON 字符串转义（简单引号替换即可）。
func JSONQuote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
