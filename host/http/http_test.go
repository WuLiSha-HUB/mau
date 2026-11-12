package http

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cathome5/host/config"
)

func TestNextFrameFormat(t *testing.T) {
	c := newSSEClient(nil, nil, nil)
	if got := c.NextFrame("snapshot", "{}"); got != "id: 1\nevent: snapshot\ndata: {}\n\n" {
		t.Fatalf("帧 = %q", got)
	}
	if got := c.NextFrame("state", "{}"); got != "id: 2\nevent: state\ndata: {}\n\n" {
		t.Fatalf("第二帧 = %q", got)
	}
}

func TestParseTopics(t *testing.T) {
	if parseTopics("") != nil {
		t.Fatal("空参数应返回 nil（全量）")
	}
	if parseTopics(" , ") != nil {
		t.Fatal("全空白应返回 nil")
	}
	set := parseTopics("snapshot, state ,view")
	if len(set) != 3 || !set["snapshot"] || !set["state"] || !set["view"] {
		t.Fatalf("订阅集合 = %v", set)
	}
}

func TestSSESubscriptionFilter(t *testing.T) {
	c := newSSEClient(nil, nil, map[string]bool{"snapshot": true})
	c.send("state", "{}")
	if len(c.queue) != 0 {
		t.Fatal("未订阅事件不应入队")
	}
	c.send("snapshot", "{}")
	if len(c.queue) != 1 {
		t.Fatal("订阅事件应入队")
	}
}

func TestHandleIndexCacheBust(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(`<link href="/css/a.css?v=__V__">`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(Options{HTMLRoot: dir, Version: "9.9.9"})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	if got := rec.Body.String(); got != `<link href="/css/a.css?v=9.9.9">` {
		t.Fatalf("body = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != noCacheHeader {
		t.Fatalf("缓存头 = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html" {
		t.Fatalf("Content-Type = %q", got)
	}
}

func TestHandleIndexChatPage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "chat.html"), []byte("chat"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(Options{HTMLRoot: dir, ChatPage: "chat.html"})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Body.String() != "chat" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestHandleIndexMissing(t *testing.T) {
	s := New(Options{HTMLRoot: t.TempDir()})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(rec.Body.String(), "未找到") {
		t.Fatalf("缺失页应给明文提示：%q", rec.Body.String())
	}
}

func TestStaticServeAndCache(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "css"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "css", "a.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(Options{HTMLRoot: dir})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/css/a.css", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "body{}" {
		t.Fatalf("css 响应 = %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != noCacheHeader {
		t.Fatalf("css 缓存头 = %q", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("Content-Type") != "text/css" {
		t.Fatalf("css Content-Type = %q", rec.Header().Get("Content-Type"))
	}
}

func TestFontsLongCache(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "fonts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fonts", "a.ttf"), []byte("ttf"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(Options{HTMLRoot: dir})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/fonts/a.ttf", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("fonts 状态码 = %d", rec.Code)
	}
	if rec.Header().Get("Cache-Control") != fontCacheHeader {
		t.Fatalf("fonts 缓存头 = %q", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("Content-Type") != "font/ttf" {
		t.Fatalf("fonts Content-Type = %q", rec.Header().Get("Content-Type"))
	}
}

func TestStaticTraversalBlocked(t *testing.T) {
	dir := t.TempDir()
	htmlRoot := filepath.Join(dir, "html")
	if err := os.MkdirAll(filepath.Join(htmlRoot, "css"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "outside.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(Options{HTMLRoot: htmlRoot})
	req := httptest.NewRequest("GET", "/css/x", nil)
	req.SetPathValue("path", "../../outside.txt")
	rec := httptest.NewRecorder()
	s.handleStatic("css", "text/css")(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("穿越应 404，实际 %d", rec.Code)
	}
}

func TestSnapshotLogsReplace(t *testing.T) {
	s := New(Options{})
	s.snapshotCache.Store(`{"version":3,"cats":[],"logs":[]}`)
	s.BindLogs(func(n int) string { return `[{"a":1}]` }, nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/snapshot?logs=5", nil))
	if got := rec.Body.String(); got != `{"version":3,"cats":[],"logs":[{"a":1}]}` {
		t.Fatalf("快照 body = %q", got)
	}
}

func TestSnapshotEmptyLogsNoReplace(t *testing.T) {
	s := New(Options{})
	s.snapshotCache.Store(`{"version":3,"logs":[]}`)
	// 日志为空时构建器返回空串——不替换，保持 "logs":[]
	s.BindLogs(func(n int) string { return "" }, nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/snapshot", nil))
	if got := rec.Body.String(); got != `{"version":3,"logs":[]}` {
		t.Fatalf("空日志不应替换：%q", got)
	}
}

func TestConfigGetShape(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "llm.cfg"), []byte("llm.reasoning_effort=high\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	schemaPath := filepath.Join(dir, "schema.json")
	body := `{"items":[
		{"key":"llm.reasoning_effort","file":"llm.cfg","default":"high","sensitive":false,"writable":true,"type":"string","desc":"思考强度"},
		{"key":"llm.max_tokens","file":"llm.cfg","default":"131072","sensitive":false,"writable":true,"type":"int","min":"1","max":"131072","desc":"上限"}
	]}`
	if err := os.WriteFile(schemaPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	schema, err := config.LoadSchema(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{})
	s.BindConfig(schema, config.LoadStore(dir))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config", nil))
	got := rec.Body.String()
	for _, want := range []string{
		`"version":1`,
		`"key":"llm.reasoning_effort"`,
		`"value":"high"`,
		`"source":"file"`,
		`"declared":true`,
		`"writable":true`,
		`"emptyDesc":""`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("config 响应缺少 %s：%s", want, got)
		}
	}
	if strings.Contains(got, `"type"`) || strings.Contains(got, `"min"`) {
		t.Fatalf("config 读面不应输出 type/min：%s", got)
	}
}

func TestConfigGetEmptyWhenUnbound(t *testing.T) {
	s := New(Options{})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config", nil))
	if got := rec.Body.String(); got != `{"version":1,"items":[]}` {
		t.Fatalf("未绑定响应 = %q", got)
	}
}

func TestConditionalRoutesAbsent(t *testing.T) {
	s := New(Options{})
	for _, path := range []string{"/api/v1/cats", "/api/v1/api-role", "/api/v1/api-models"} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s 未绑定应 404，实际 %d", path, rec.Code)
		}
	}
}

func TestConditionalRoutesPresent(t *testing.T) {
	s := New(Options{})
	s.BindCats(func() string { return `{"version":1,"cats":[]}` })
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cats", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != `{"version":1,"cats":[]}` {
		t.Fatalf("cats 响应 = %d %q", rec.Code, rec.Body.String())
	}
}

func TestLogsUnboundEmpty(t *testing.T) {
	s := New(Options{})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/logs?n=5", nil))
	if got := rec.Body.String(); got != `{"version":1,"logs":[]}` {
		t.Fatalf("未绑定 logs = %q", got)
	}
}

func TestWriteJSONNoHTMLEscape(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, map[string]string{"k": "<a&b>"})
	if got := rec.Body.String(); got != `{"k":"<a&b>"}` {
		t.Fatalf("writeJSON = %q", got)
	}
}

func TestMaskSecret(t *testing.T) {
	if maskSecret("") != "" || maskSecret("short") != "****" || maskSecret("123456789") != "1234****6789" {
		t.Fatal("掩码口径不符")
	}
}

// configPostFixture 构造带 schema 与段文件的最小配置面（写面测试共用）。
func configPostFixture(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.json")
	body := `{"items":[
		{"key":"llm.max_tokens","file":"llm.cfg","default":"131072","sensitive":false,"writable":true,"type":"int","min":"1","max":"131072","desc":"上限"},
		{"key":"llm.thinking","file":"llm.cfg","default":"enabled","sensitive":false,"writable":true,"type":"string","values":["enabled","disabled"],"desc":"思考"},
		{"key":"vision.api_key","file":"vision.cfg","default":"","sensitive":true,"writable":true,"type":"string","desc":"视觉密钥"}
	]}`
	if err := os.WriteFile(schemaPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	schema, err := config.LoadSchema(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	store := config.LoadStore(dir)
	store.RegisterSchema(schema)
	s := New(Options{})
	s.BindConfig(schema, store)
	s.BindFrame(func() int64 { return 42 })
	return s, dir
}

// TestConfigPostSuccess 成功回执字段序 ok/key/value/frame（对齐 C# 匿名对象序）。
func TestConfigPostSuccess(t *testing.T) {
	s, dir := configPostFixture(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/config",
		strings.NewReader(`{"key":"llm.thinking","value":"disabled"}`)))
	want := `{"ok":true,"key":"llm.thinking","value":"disabled","frame":42}`
	if got := rec.Body.String(); got != want {
		t.Fatalf("回执 = %s\n预期 = %s", got, want)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "llm.cfg"))
	if err != nil {
		t.Fatalf("落盘失败: %v", err)
	}
	if !strings.Contains(string(raw), "llm.thinking=disabled") {
		t.Fatalf("落盘内容不符: %q", string(raw))
	}
	// 写面 → 读面同源：同实例 GET 立即可见新值
	recGet := httptest.NewRecorder()
	s.ServeHTTP(recGet, httptest.NewRequest("GET", "/api/v1/config", nil))
	if !strings.Contains(recGet.Body.String(), `"value":"disabled"`) {
		t.Fatalf("读面未反映写入: %s", recGet.Body.String())
	}
}

// TestConfigPostTokenKeyMasked 含 token 字样的键回显掩码（C# IsSecretKey 同口径——
// llm.max_tokens 命中 token 子串，短值全掩码 ****；两侧同形，不是缺陷）。
func TestConfigPostTokenKeyMasked(t *testing.T) {
	s, _ := configPostFixture(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/config",
		strings.NewReader(`{"key":"llm.max_tokens","value":"4096"}`)))
	want := `{"ok":true,"key":"llm.max_tokens","value":"****","frame":42}`
	if got := rec.Body.String(); got != want {
		t.Fatalf("回执 = %s\n预期 = %s", got, want)
	}
}

// TestConfigPostSecretMaskedEcho 敏感键回显掩码（不回明文）。
func TestConfigPostSecretMaskedEcho(t *testing.T) {
	s, _ := configPostFixture(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/config",
		strings.NewReader(`{"key":"vision.api_key","value":"sk-1234567890abcdef"}`)))
	want := `{"ok":true,"key":"vision.api_key","value":"sk-1****cdef","frame":42}`
	if got := rec.Body.String(); got != want {
		t.Fatalf("敏感键回执 = %s\n预期 = %s", got, want)
	}
}

// TestConfigPostRejects 四道前置拒绝 + schema 拒绝（文案逐字对齐 C#）。
func TestConfigPostRejects(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"非 JSON", `not-json`, `{"ok":false,"error":"key 为空"}`},
		{"key 空", `{"key":"","value":"1"}`, `{"ok":false,"error":"key 为空"}`},
		{"value 空", `{"key":"llm.max_tokens","value":""}`, `{"ok":false,"error":"value 为空"}`},
		{"掩码回写", `{"key":"llm.max_tokens","value":"12****34"}`, `{"ok":false,"error":"value 含掩码标记——请输入真实值"}`},
		{"未声明键", `{"key":"ui.unknown","value":"1"}`, `{"ok":false,"error":"配置键未在 schema 声明: ui.unknown"}`},
		{"值域超限", `{"key":"llm.max_tokens","value":"999999"}`, `{"ok":false,"error":"配置项 llm.max_tokens 超出上限 131072"}`},
	}
	for _, testCase := range cases {
		s, _ := configPostFixture(t)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/config", strings.NewReader(testCase.body)))
		if got := rec.Body.String(); got != testCase.want {
			t.Fatalf("%s：回执 = %s\n预期 = %s", testCase.name, got, testCase.want)
		}
	}
}

// TestConfigPostUnboundStore 存储未绑定（壳形态）。
func TestConfigPostUnboundStore(t *testing.T) {
	s := New(Options{})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/config",
		strings.NewReader(`{"key":"llm.max_tokens","value":"1"}`)))
	if got := rec.Body.String(); got != `{"ok":false,"error":"配置存储未绑定"}` {
		t.Fatalf("未绑定回执 = %s", got)
	}
}
