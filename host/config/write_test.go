package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testSchema 构造含各类型的 schema（与运行区模板同形的最小子集）。
func testSchema() *Schema {
	items := []SchemaItem{
		{Key: "ui.font_scale", File: "ui.cfg", Default: "100", Writable: true, Type: "int", Min: "50", Max: "300", Desc: "字号"},
		{Key: "llm.thinking", File: "llm.cfg", Default: "enabled", Writable: true, Type: "string", Values: []string{"enabled", "disabled"}, Desc: "思考"},
		{Key: "app.autostart", File: "app.cfg", Default: "false", Writable: true, Type: "bool", Desc: "自启"},
		{Key: "llm.reasoning_effort", File: "llm.cfg", Default: "high", Sensitive: true, Writable: false, Type: "string", Desc: "只读项"},
	}
	s := &Schema{Items: items, byKey: make(map[string]*SchemaItem, len(items))}
	for i := range s.Items {
		s.byKey[s.Items[i].Key] = &s.Items[i]
	}
	return s
}

// TestSetCheckedWhitelistAndRange 覆盖白名单 / 只读 / 值域三道拒绝（文案逐字对齐 C#）。
func TestSetCheckedWhitelistAndRange(t *testing.T) {
	dir := t.TempDir()
	store := LoadStore(dir)
	store.RegisterSchema(testSchema())
	schema := testSchema()

	cases := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{"未声明键", "ui.unknown", "1", "配置键未在 schema 声明: ui.unknown"},
		{"只读项", "llm.reasoning_effort", "low", "只读配置项: llm.reasoning_effort"},
		{"整型非数", "ui.font_scale", "abc", "配置项 ui.font_scale 需要整数: abc"},
		{"低于下限", "ui.font_scale", "10", "配置项 ui.font_scale 低于下限 50"},
		{"超出上限", "ui.font_scale", "999", "配置项 ui.font_scale 超出上限 300"},
		{"枚举未命中", "llm.thinking", "maybe", "配置项 llm.thinking 只接受以下值之一: enabled / disabled"},
		{"布尔非法", "app.autostart", "yes", "配置项 app.autostart 需要布尔值: yes"},
		{"掩码回写", "llm.thinking", "****", "配置项 llm.thinking 只接受以下值之一: enabled / disabled"},
	}
	for _, testCase := range cases {
		ok, reason := store.SetChecked(testCase.key, testCase.value, schema)
		if ok {
			t.Fatalf("%s：预期拒绝，实际通过", testCase.name)
		}
		if reason != testCase.want {
			t.Fatalf("%s：文案不符\n预期: %s\n实际: %s", testCase.name, testCase.want, reason)
		}
	}
}

// TestSetCheckedMaskReject 掩码回写拒绝（schema 值域放行后仍被掩码闸拦）。
func TestSetCheckedMaskReject(t *testing.T) {
	dir := t.TempDir()
	store := LoadStore(dir)
	schema := &Schema{Items: []SchemaItem{{Key: "vision.api_key", File: "vision.cfg", Writable: true, Type: "string"}}}
	schema.byKey = map[string]*SchemaItem{"vision.api_key": &schema.Items[0]}
	ok, reason := store.SetChecked("vision.api_key", "sk-****-abcd", schema)
	if ok || reason != "value 含掩码标记——请输入真实值" {
		t.Fatalf("掩码回写应拒绝，实际 ok=%v reason=%s", ok, reason)
	}
}

// TestSetCheckedPersistsPerSegment 成功写入落到段文件（ui.cfg / llm.cfg），无前缀键落主文件。
func TestSetCheckedPersistsPerSegment(t *testing.T) {
	dir := t.TempDir()
	store := LoadStore(dir)
	schema := testSchema()
	store.RegisterSchema(schema)

	if ok, reason := store.SetChecked("ui.font_scale", "120", schema); !ok {
		t.Fatalf("ui.font_scale 写入失败: %s", reason)
	}
	if ok, reason := store.SetChecked("llm.thinking", "disabled", schema); !ok {
		t.Fatalf("llm.thinking 写入失败: %s", reason)
	}

	uiBody, err := os.ReadFile(filepath.Join(dir, "ui.cfg"))
	if err != nil {
		t.Fatalf("ui.cfg 未落盘: %v", err)
	}
	if !strings.Contains(string(uiBody), "ui.font_scale=120") {
		t.Fatalf("ui.cfg 内容不符: %q", string(uiBody))
	}
	llmBody, err := os.ReadFile(filepath.Join(dir, "llm.cfg"))
	if err != nil {
		t.Fatalf("llm.cfg 未落盘: %v", err)
	}
	if !strings.Contains(string(llmBody), "llm.thinking=disabled") {
		t.Fatalf("llm.cfg 内容不符: %q", string(llmBody))
	}
	// 内存即时可见（同一实例读面立刻读到新值——无需重载）
	if value, ok := store.TryGet("ui.font_scale"); !ok || value != "120" {
		t.Fatalf("内存值未更新: %q %v", value, ok)
	}
	if entry := store.Resolve(schema, "ui.font_scale"); entry.Source != "file" || entry.Value != "120" {
		t.Fatalf("Resolve 未读到新值: %+v", entry)
	}
}

// TestSetCheckedRollback 落盘失败回滚内存（目标路径被目录占位 → 原子写失败）。
func TestSetCheckedRollback(t *testing.T) {
	dir := t.TempDir()
	// 用同名目录占位，让 ui.cfg 的原子写必然失败
	if err := os.MkdirAll(filepath.Join(dir, "ui.cfg"), 0o755); err != nil {
		t.Fatalf("建占位目录失败: %v", err)
	}
	store := LoadStore(dir)
	store.Set("ui.font_scale", "100")
	schema := testSchema()
	store.RegisterSchema(schema)

	ok, reason := store.SetChecked("ui.font_scale", "150", schema)
	if ok {
		t.Fatalf("落盘失败应回滚并报错")
	}
	if !strings.HasPrefix(reason, "写入失败: ") {
		t.Fatalf("失败文案不符: %s", reason)
	}
	if value, _ := store.TryGet("ui.font_scale"); value != "100" {
		t.Fatalf("回滚后内存值应为旧值 100，实际 %q", value)
	}
}

// TestValidateLengthCap 值长度上限 1024。
func TestValidateLengthCap(t *testing.T) {
	schema := testSchema()
	long := strings.Repeat("a", 1025)
	if ok, reason := schema.Validate("llm.thinking", long); ok || reason != "配置值过长（>1024）" {
		t.Fatalf("超长值应拒绝，实际 ok=%v reason=%s", ok, reason)
	}
	// 枚举忽略大小写命中（值原样通过，不做归一）
	if ok, reason := schema.Validate("llm.thinking", "ENABLED"); !ok {
		t.Fatalf("枚举应忽略大小写命中，实际 %s", reason)
	}
}

// TestRegisterSchemaSegments 段注册按 schema 的 file 归属（新建段也能落对文件）。
func TestRegisterSchemaSegments(t *testing.T) {
	dir := t.TempDir()
	store := LoadStore(dir)
	note := store.RegisterSchema(testSchema())
	if !strings.Contains(note, "schema 驱动段注册: 3 段") {
		t.Fatalf("段数不符: %s", note)
	}
	// ui.cfg 文件不存在也应注册出归属（写面创建）
	if got := store.fileByPrefix["ui"]; got != filepath.Join(dir, "ui.cfg") {
		t.Fatalf("ui 段归属不符: %s", got)
	}
}
