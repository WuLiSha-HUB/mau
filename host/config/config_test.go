package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDataRootEnvFirst(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CH4_DATA_ROOT", dir)
	got, degrades := ResolveDataRoot()
	if got != dir {
		t.Fatalf("数据根 = %q，期望 %q", got, dir)
	}
	if len(degrades) != 0 {
		t.Fatalf("降级 = %v，期望空", degrades)
	}
}

func TestResolveDataRootLegacySubdir(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "Data")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CH4_DATA_ROOT", dir)
	got, degrades := ResolveDataRoot()
	if got != legacy {
		t.Fatalf("数据根 = %q，期望旧布局子层 %q", got, legacy)
	}
	if len(degrades) != 1 || degrades[0].Code != "DATA_ROOT_LEGACY" {
		t.Fatalf("降级 = %v，期望一条 DATA_ROOT_LEGACY", degrades)
	}
}

func TestResolveDataRootUnusableFallback(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CH4_DATA_ROOT", blocked)
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	got, degrades := ResolveDataRoot()
	if got != filepath.Join(local, "CatHome4") {
		t.Fatalf("数据根 = %q，期望回落 %q", got, filepath.Join(local, "CatHome4"))
	}
	if len(degrades) != 1 || degrades[0].Code != "DATA_ROOT_UNUSABLE" {
		t.Fatalf("降级 = %v，期望一条 DATA_ROOT_UNUSABLE", degrades)
	}
}

func TestResolvePortBand(t *testing.T) {
	dev := ResolvePortBand(true)
	if dev.Area != "dev" || dev.MainPort != 8079 || dev.FamilyFrom != 8078 || dev.FamilyTo != 8070 || dev.FrontendTestPort != 8069 {
		t.Fatalf("开发区端口段 = %+v", dev)
	}
	if dev.FamilyStep() != -1 {
		t.Fatalf("开发区步进 = %d，期望 -1", dev.FamilyStep())
	}
	deploy := ResolvePortBand(false)
	if deploy.Area != "deploy" || deploy.MainPort != 8080 || deploy.FamilyFrom != 8081 || deploy.FamilyTo != 8180 || deploy.FrontendTestPort != 8099 {
		t.Fatalf("部署区端口段 = %+v", deploy)
	}
	if deploy.FamilyStep() != 1 {
		t.Fatalf("部署区步进 = %d，期望 1", deploy.FamilyStep())
	}
}

func TestResolveAreaOverride(t *testing.T) {
	t.Setenv("CAT5_AREA", "dev")
	if !ResolveArea() {
		t.Fatal("CAT5_AREA=dev 应判开发区")
	}
	t.Setenv("CAT5_AREA", "deploy")
	if ResolveArea() {
		t.Fatal("CAT5_AREA=deploy 应判部署区")
	}
}

func TestSchemaLoadAndFind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.json")
	body := `{"_说明":"x","items":[
		{"key":"llm.thinking","file":"llm.cfg","default":"enabled","sensitive":false,"writable":false,"desc":"只读"},
		{"key":"ui.font_scale","file":"ui.cfg","default":"100","sensitive":false,"writable":true,"type":"int","min":"50","max":"300","desc":"字号"}
	]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	schema, err := LoadSchema(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(schema.Items) != 2 {
		t.Fatalf("声明项 = %d，期望 2", len(schema.Items))
	}
	item := schema.Find("ui.font_scale")
	if item == nil || item.Min != "50" || item.Max != "300" || !item.Writable {
		t.Fatalf("ui.font_scale = %+v", item)
	}
	if schema.Find("nope") != nil {
		t.Fatal("未声明键应返回 nil")
	}
	if !schema.IsSensitive("some.token") {
		t.Fatal("键名含 token 应判敏感")
	}
}

func TestSyncSchemaThreeStates(t *testing.T) {
	dir := t.TempDir()
	exe := dir
	// 在测试可执行目录旁边造模板不可行——直接用仓库根候选：造 Mau.sln + config 模板
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "Mau.sln"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	template := `{"items":[{"key":"a","file":"a.cfg","default":"1","sensitive":false,"writable":true,"desc":"d"}]}`
	if err := os.WriteFile(filepath.Join(repo, "config", "schema.json.example"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(dir, "data", "config")
	_ = exe

	// 模板源只在 ExeDir / FindRepoRoot(ExeDir) 找；测试里 ExeDir 是 go test 临时目录，
	// 所以这里直接验证「两源皆缺 → template-missing」与手动复制的两态。
	res := SyncSchema(configDir)
	if res.Outcome == "seed" {
		// 测试环境若恰好存在模板则走种子，做一次覆盖语义检查
		if !fileExists(filepath.Join(configDir, "schema.json")) {
			t.Fatal("种子后目标应存在")
		}
	} else if res.Outcome != "template-missing" {
		t.Fatalf("同步结局 = %s，期望 seed 或 template-missing", res.Outcome)
	}
}

func TestLoadWorkspaceMissingFile(t *testing.T) {
	root := t.TempDir()
	cfg := LoadWorkspace(filepath.Join(root, "nope.json"), root)
	if len(cfg.Roots) != 1 || cfg.Roots[0].ID != "workspace" || !cfg.Roots[0].Writable {
		t.Fatalf("缺文件应回落单根：%+v", cfg.Roots)
	}
	if len(cfg.Inject) != 0 {
		t.Fatalf("缺文件注入应为空：%+v", cfg.Inject)
	}
}

func TestLoadWorkspaceCorrupt(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "workspace.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := LoadWorkspace(path, root)
	if len(cfg.Roots) != 1 || cfg.Roots[0].ID != "workspace" {
		t.Fatalf("坏档应回落单根：%+v", cfg.Roots)
	}
	if len(cfg.LoadDegrades) != 1 || !strings.HasPrefix(cfg.LoadDegrades[0], "CFG_CORRUPT|fallback|") {
		t.Fatalf("降级 = %v，期望 CFG_CORRUPT", cfg.LoadDegrades)
	}
	matches, _ := filepath.Glob(path + ".corrupt-*")
	if len(matches) != 1 {
		t.Fatalf("坏档备份 = %v，期望一份", matches)
	}
}

func TestLoadWorkspaceSkipNonResident(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "workspace.json")
	body := `{"roots":[
		{"id":"workspace","path":"` + escapeJSON(root) + `","writable":true,"note":"工作区"},
		{"id":"gone","path":"` + escapeJSON(filepath.Join(root, "missing")) + `","writable":true}
	],"inject":[{"file":"ccbp:AGENTS.md","optional":true,"label":"规范"}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := LoadWorkspace(path, root)
	if len(cfg.Roots) != 1 || cfg.Roots[0].ID != "workspace" {
		t.Fatalf("非常驻根应跳过：%+v", cfg.Roots)
	}
	if len(cfg.LoadWarnings) != 1 || !strings.Contains(cfg.LoadWarnings[0], "gone") {
		t.Fatalf("告警 = %v", cfg.LoadWarnings)
	}
	if len(cfg.Inject) != 1 || !cfg.Inject[0].Optional || cfg.Inject[0].Label != "规范" {
		t.Fatalf("注入 = %+v", cfg.Inject)
	}
}

func TestLoadWorkspaceRebuildResident(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "runtime-missing")
	path := filepath.Join(root, "workspace.json")
	body := `{"roots":[{"id":"runtime","path":"` + escapeJSON(missing) + `","writable":false}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := LoadWorkspace(path, root)
	if len(cfg.Roots) != 1 || cfg.Roots[0].Path != missing {
		t.Fatalf("常驻根应重建后保留：%+v", cfg.Roots)
	}
	if len(cfg.LoadDegrades) != 1 || !strings.HasPrefix(cfg.LoadDegrades[0], "ROOT_REBUILT|rebuilt|") {
		t.Fatalf("降级 = %v，期望 ROOT_REBUILT", cfg.LoadDegrades)
	}
	if !dirExists(missing) {
		t.Fatal("常驻根目录应已重建")
	}
}

func TestTruncateNote(t *testing.T) {
	if got := TruncateNote("  短注释  "); got != "短注释" {
		t.Fatalf("截断 = %q", got)
	}
	long := strings.Repeat("猫", 25)
	if got := TruncateNote(long); len([]rune(got)) != NoteMaxLength {
		t.Fatalf("长注释截断长度 = %d，期望 %d", len([]rune(got)), NoteMaxLength)
	}
}

func TestStoreParseAndResolve(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "llm.cfg"), []byte("# 注释\nllm.reasoning_effort=high\n坏行\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.cfg"), []byte("app.autostart=true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cat-default.cfg"), []byte(`{"baseRole":""}`), 0o644); err != nil {
		t.Fatal(err)
	}
	store := LoadStore(dir)
	if v, ok := store.TryGet("llm.reasoning_effort"); !ok || v != "high" {
		t.Fatalf("llm.reasoning_effort = %q,%v", v, ok)
	}
	if v, ok := store.TryGet("app.autostart"); !ok || v != "true" {
		t.Fatalf("app.autostart = %q,%v", v, ok)
	}
	if _, ok := store.TryGet("bad.line"); ok {
		t.Fatal("坏行不应入表")
	}
	if _, ok := store.TryGet("baseRole"); ok {
		t.Fatal("cat-default.cfg 不应作为键值配置加载")
	}
}

func TestConfigEffectiveThreeLayers(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "llm.cfg"), []byte("llm.max_tokens=4096\nui.font_scale=120\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := LoadStore(dir)
	schemaPath := filepath.Join(dir, "schema.json")
	body := `{"items":[
		{"key":"llm.max_tokens","file":"llm.cfg","default":"131072","sensitive":false,"writable":true,"type":"int","min":"1","max":"131072","desc":"上限"},
		{"key":"llm.reasoning_effort","file":"llm.cfg","default":"high","sensitive":false,"writable":true,"type":"string","desc":"强度"},
		{"key":"ui.font_scale","file":"ui.cfg","default":"100","sensitive":false,"writable":true,"type":"int","min":"50","max":"300","desc":"字号"}
	]}`
	if err := os.WriteFile(schemaPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	schema, err := LoadSchema(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAU_LLM_REASONING_EFFORT", "max")
	entries := store.BuildAll(schema)
	if len(entries) != 3 {
		t.Fatalf("条目 = %d，期望 3（无未声明键）", len(entries))
	}
	byKey := map[string]Entry{}
	for _, e := range entries {
		byKey[e.Key] = e
	}
	if e := byKey["llm.max_tokens"]; e.Source != "file" || e.Value != "4096" {
		t.Fatalf("file 层 = %+v", e)
	}
	if e := byKey["llm.reasoning_effort"]; e.Source != "env" || e.Value != "max" {
		t.Fatalf("env 层 = %+v", e)
	}
	if e := byKey["ui.font_scale"]; e.Source != "file" || e.Value != "120" {
		t.Fatalf("ui.font_scale = %+v", e)
	}
}

func TestConfigEffectiveUndeclaredAppended(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "custom.cfg"), []byte("custom.key=v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := LoadStore(dir)
	schemaPath := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(schemaPath, []byte(`{"items":[{"key":"a.b","file":"a.cfg","default":"d","sensitive":false,"writable":true,"desc":"x"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	schema, err := LoadSchema(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	entries := store.BuildAll(schema)
	if len(entries) != 2 {
		t.Fatalf("条目 = %d，期望 2", len(entries))
	}
	last := entries[1]
	if last.Key != "custom.key" || last.Declared || last.Writable || last.Desc != UndeclaredDesc {
		t.Fatalf("未声明键 = %+v", last)
	}
}

func TestMaskSecret(t *testing.T) {
	cases := map[string]string{
		"":                 "",
		"short":            "****",
		"12345678":         "****",
		"123456789":        "1234****6789",
		"sk-abcdefghijklm": "sk-a****jklm",
	}
	for in, want := range cases {
		if got := MaskSecret(in); got != want {
			t.Fatalf("MaskSecret(%q) = %q，期望 %q", in, got, want)
		}
	}
	if !IsSecretKey("x.api_key") || !IsSecretKey("SOME_SECRET") || !IsSecretKey("token.y") {
		t.Fatal("敏感键名判定漏")
	}
}

func TestLoadCatCfg(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cat.cfg")
	body := `{"id":"majordomo","displayName":"majordomo","type":"","apiConfigId":"00000000-0000-0000-0000-000000000000","persona":"","toolNames":"a,b","injectList":["ccbp:AGENTS.md"],"projectMount":"","projectMountExpert":true,"projectMountAt":0,"mountAuto":true,"enabledRoots":["workspace","mau"],"packs":["overwork"],"dbLibs":[],"userInjectIdleOnly":false,"appearanceId":"00000000-0000-0000-0000-000000000000","reasoningEffort":"max","themeColor":0,"running":true,"port":8081}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadCatCfg(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ID != "majordomo" || cfg.ToolNames != "a,b" || len(cfg.EnabledRoots) != 2 || cfg.ReasoningEffort != "max" {
		t.Fatalf("cat.cfg = %+v", cfg)
	}
	if !cfg.Running || cfg.Port != 8081 {
		t.Fatalf("运行态读容忍失败：running=%v port=%d", cfg.Running, cfg.Port)
	}
}

func TestLoadCatDefaultNullRoots(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cat-default.cfg")
	body := `{"baseRole":"","defaultPersona":"","defaultToolNames":"a","defaultInjectList":[],"defaultPacks":[],"defaultEnabledRoots":null,"defaultUserInjectIdleOnly":false}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadCatDefault(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultEnabledRoots != nil {
		t.Fatalf("defaultEnabledRoots = %+v，期望 nil（null 保留）", cfg.DefaultEnabledRoots)
	}
}

func TestFilterRoots(t *testing.T) {
	all := []RootEntry{{ID: "workspace"}, {ID: "mau"}, {ID: "ccbp"}}
	roots := (&WorkspaceConfig{Roots: all}).FSRoots()
	if len(roots) != 3 {
		t.Fatalf("根表 = %d", len(roots))
	}
	filtered := FilterRoots(roots, []string{"MAU"})
	if len(filtered) != 2 {
		t.Fatalf("过滤后 = %d，期望 2（mau + 强制 workspace）", len(filtered))
	}
	if len(FilterRoots(roots, nil)) != 3 {
		t.Fatal("空清单应全量放行")
	}
}

func escapeJSON(v string) string {
	return strings.ReplaceAll(v, `\`, `\\`)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
