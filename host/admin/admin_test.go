package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cathome5/host/config"
	"cathome5/host/toolpool"
)

// newTestDeps 造一套最小管理面数据源（临时数据根 + 两条工具声明）。
func newTestDeps(t *testing.T) (*Deps, string) {
	t.Helper()
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(root, "sessions", "majordomo"), 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	cfg := &config.CatCfg{
		ID:          "majordomo",
		DisplayName: "主干",
		APIConfigID: config.ZeroGUIDText,
		Persona:     "旧人设",
		ToolNames:   "text-read",
		InjectList:  []string{"ccbp:AGENTS.md"},
	}
	if err := config.SaveCatCfg(filepath.Join(root, "sessions", "majordomo", "cat.cfg"), cfg); err != nil {
		t.Fatalf("写 cat.cfg 失败: %v", err)
	}
	tpl := &config.CatDefault{BaseRole: "基础", DefaultToolNames: "text-read"}
	if err := config.SaveCatDefault(filepath.Join(configDir, "cat-default.cfg"), tpl); err != nil {
		t.Fatalf("写 cat-default.cfg 失败: %v", err)
	}
	pool := filepath.Join(configDir, "packs.json")
	if err := os.WriteFile(pool, []byte(`{"packs":{"overwork":{"desc":"收工加载包","paths":["ccbp:L2"]}}}`), 0o644); err != nil {
		t.Fatalf("写 packs.json 失败: %v", err)
	}
	registry := toolpool.NewRegistry([]toolpool.Def{
		{Name: "text-read", Group: "TextCat"},
		{Name: "file-tree", Group: "FileCat"},
		{Name: "majordomo-cmd", Group: "Majordomo", Privileged: true},
	}, nil, map[string]bool{"majordomo-cmd": true})
	store := config.LoadStore(configDir)
	schema := &config.Schema{}
	deps := &Deps{
		DataRoot:    root,
		Schema:      schema,
		Store:       store,
		Workspace:   &config.WorkspaceConfig{Roots: []config.RootEntry{{ID: "workspace", Path: root, Writable: true}, {ID: "ccbp", Path: root, Writable: true}}},
		Registry:    registry,
		LLMStore:    config.LoadLlmAPIStore(filepath.Join(configDir, "llm-api.json"), filepath.Join(root, "secrets", "llm-api.cfg")),
		CatID:       "majordomo",
		DisplayName: "主干",
		Running:     true,
		Port:        8079,
	}
	return deps, root
}

// do 发一次请求（mux 装配同生产路径）。
func do(t *testing.T, deps *Deps, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, deps)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// objectKeyOrder 取顶层对象的键序（保序判定用）。
func objectKeyOrder(t *testing.T, body []byte) []string {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		t.Fatalf("响应不是 JSON 对象: %s", string(body))
	}
	keys := make([]string, 0, 32)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			t.Fatalf("取键失败: %v", err)
		}
		key, _ := keyToken.(string)
		keys = append(keys, key)
		var skip json.RawMessage
		if err := decoder.Decode(&skip); err != nil {
			t.Fatalf("跳值失败: %v", err)
		}
	}
	return keys
}

// TestCatConfigGetKeyOrder 响应字段与顺序逐条对齐 C# HandleCatConfigGet。
func TestCatConfigGetKeyOrder(t *testing.T) {
	deps, _ := newTestDeps(t)
	rec := do(t, deps, http.MethodGet, "/api/v1/cat-config?cat=majordomo", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d", rec.Code)
	}
	expected := []string{
		"ok", "cat", "displayName", "type", "apiConfigId", "persona", "toolNames", "injectList",
		"projectMount", "projectMountExpert", "packs", "dbLibs", "allLibs", "dbLibsAvailable",
		"allPacks", "userInjectIdleOnly", "reasoningEffort", "appearanceId", "themeColor",
		"roundSummary", "defaultReasoningEffort", "enabledRoots", "allRoots", "allToolNames",
		"allTools", "defectGroups", "staleToolNames", "apiOptions", "appearanceOptions",
	}
	keys := objectKeyOrder(t, rec.Body.Bytes())
	if strings.Join(keys, ",") != strings.Join(expected, ",") {
		t.Fatalf("字段序不符:\n实得 %v\n期望 %v", keys, expected)
	}
}

// TestCatConfigPostMerge 字段级合并写——缺省字段逐字保留，提交字段落盘。
func TestCatConfigPostMerge(t *testing.T) {
	deps, root := newTestDeps(t)
	rec := do(t, deps, http.MethodPost, "/api/v1/cat-config", `{"cat":"majordomo","persona":"新人设"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d: %s", rec.Code, rec.Body.String())
	}
	cfg, err := config.LoadCatCfg(filepath.Join(root, "sessions", "majordomo", "cat.cfg"))
	if err != nil {
		t.Fatalf("重读失败: %v", err)
	}
	if cfg.Persona != "新人设" {
		t.Fatalf("persona 未更新: %q", cfg.Persona)
	}
	if cfg.DisplayName != "主干" || cfg.ToolNames != "text-read" {
		t.Fatalf("未提交字段被改写: display=%q tools=%q", cfg.DisplayName, cfg.ToolNames)
	}
	if len(cfg.InjectList) != 1 || cfg.InjectList[0] != "ccbp:AGENTS.md" {
		t.Fatalf("injectList 未逐字保留: %v", cfg.InjectList)
	}
}

// TestCatConfigPostToolNameFilter 池外工具名剔除并随回执出声。
func TestCatConfigPostToolNameFilter(t *testing.T) {
	deps, root := newTestDeps(t)
	rec := do(t, deps, http.MethodPost, "/api/v1/cat-config", `{"cat":"majordomo","toolNames":"text-read,dragons"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		OK               bool     `json:"ok"`
		ToolNames        string   `json:"toolNames"`
		RemovedToolNames []string `json:"removedToolNames"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析回执失败: %v", err)
	}
	if payload.ToolNames != "text-read" {
		t.Fatalf("落盘名单不符: %q", payload.ToolNames)
	}
	if len(payload.RemovedToolNames) != 1 || payload.RemovedToolNames[0] != "dragons" {
		t.Fatalf("剔除明细不符: %v", payload.RemovedToolNames)
	}
	cfg, _ := config.LoadCatCfg(filepath.Join(root, "sessions", "majordomo", "cat.cfg"))
	if cfg.ToolNames != "text-read" {
		t.Fatalf("磁盘名单不符: %q", cfg.ToolNames)
	}
}

// TestCatConfigPostRejects 显式非法值一律拒绝（displayName 空 / reasoningEffort 非法 / cat 穿越）。
func TestCatConfigPostRejects(t *testing.T) {
	deps, _ := newTestDeps(t)
	cases := []struct{ name, body, want string }{
		{"displayName 空", `{"cat":"majordomo","displayName":"  "}`, "displayName 不能为空"},
		{"reasoningEffort 非法", `{"cat":"majordomo","reasoningEffort":"ultra"}`, "reasoningEffort 非法"},
		{"cat 穿越", `{"cat":"../etc","persona":"x"}`, "cat 参数非法"},
		{"themeColor 非法", `{"cat":"majordomo","themeColor":-1}`, "themeColor 非法"},
	}
	for _, item := range cases {
		rec := do(t, deps, http.MethodPost, "/api/v1/cat-config", item.body)
		var payload struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("%s: 解析回执失败: %v", item.name, err)
		}
		if payload.OK || !strings.Contains(payload.Error, item.want) {
			t.Fatalf("%s: 期望拒绝（%s），实得 ok=%v error=%q", item.name, item.want, payload.OK, payload.Error)
		}
	}
}

// TestLlmApisCRUDFlow 端点池新建 → 列表 → 设默认 → 删除 的实机链路。
func TestLlmApisCRUDFlow(t *testing.T) {
	deps, root := newTestDeps(t)
	rec := do(t, deps, http.MethodPost, "/api/v1/llm-apis", `{"displayName":"甲","endpoint":"https://a.example.com/v1","defaultModel":"m1","apiKey":"sk-1234567890"}`)
	var created struct {
		OK          bool   `json:"ok"`
		APIConfigID string `json:"apiConfigId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("解析新建回执失败: %v (%s)", err, rec.Body.String())
	}
	if !created.OK || created.APIConfigID == "" {
		t.Fatalf("新建失败: %s", rec.Body.String())
	}

	list := do(t, deps, http.MethodGet, "/api/v1/llm-apis", "")
	var listed struct {
		Version int `json:"version"`
		Items   []struct {
			APIConfigID string `json:"apiConfigId"`
			APIKey      string `json:"apiKey"`
			HasKey      bool   `json:"hasKey"`
			IsDefault   bool   `json:"isDefault"`
		} `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatalf("解析列表失败: %v", err)
	}
	if listed.Version != 1 || len(listed.Items) != 1 {
		t.Fatalf("列表条数不符: %s", list.Body.String())
	}
	if !listed.Items[0].HasKey || listed.Items[0].APIKey != "sk-1****7890" {
		t.Fatalf("密钥掩码不符: %s", list.Body.String())
	}
	if !listed.Items[0].IsDefault {
		t.Fatal("池内首条配置应为默认端点")
	}

	def := do(t, deps, http.MethodPost, "/api/v1/llm-apis/default", `{"apiConfigId":"`+created.APIConfigID+`"}`)
	if !strings.Contains(def.Body.String(), `"ok":true`) {
		t.Fatalf("设默认失败: %s", def.Body.String())
	}

	del := do(t, deps, http.MethodPost, "/api/v1/llm-apis/delete", `{"apiConfigId":"`+created.APIConfigID+`"}`)
	if !strings.Contains(del.Body.String(), `"ok":true`) {
		t.Fatalf("删除失败: %s", del.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "config", "llm-api.json")); err != nil {
		t.Fatalf("删除后配置文件不应消失: %v", err)
	}
}

// TestCatDefaultGetKeyOrder 默认模板读面字段序（对齐 C# HandleCatDefaultGet）。
func TestCatDefaultGetKeyOrder(t *testing.T) {
	deps, _ := newTestDeps(t)
	rec := do(t, deps, http.MethodGet, "/api/v1/cat-default", "")
	expected := []string{
		"ok", "baseRole", "defaultPersona", "defaultToolNames", "defaultInjectList",
		"defaultPacks", "defaultUserInjectIdleOnly", "allToolNames", "allTools",
		"defectGroups", "staleToolNames", "allPacks",
	}
	keys := objectKeyOrder(t, rec.Body.Bytes())
	if strings.Join(keys, ",") != strings.Join(expected, ",") {
		t.Fatalf("字段序不符:\n实得 %v\n期望 %v", keys, expected)
	}
}

// TestCatDetailHeadKeys 详情读面头部键序（状态段随会话投影，只校验头部与尾部）。
func TestCatDetailHeadKeys(t *testing.T) {
	deps, _ := newTestDeps(t)
	rec := do(t, deps, http.MethodGet, "/api/v1/cat-detail?cat=majordomo", "")
	keys := objectKeyOrder(t, rec.Body.Bytes())
	head := []string{"id", "name", "type", "special", "running", "port"}
	for i, key := range head {
		if keys[i] != key {
			t.Fatalf("头部位 %d 期望 %s 实得 %s（全序 %v）", i, key, keys[i], keys)
		}
	}
	tail := []string{"ok", "cat", "isIdle", "lastUser", "lastReply"}
	if len(keys) < len(head)+len(tail) {
		t.Fatalf("键数不足: %v", keys)
	}
	for i, key := range tail {
		if got := keys[len(keys)-len(tail)+i]; got != key {
			t.Fatalf("尾部位 %d 期望 %s 实得 %s（全序 %v）", i, key, got, keys)
		}
	}
}
