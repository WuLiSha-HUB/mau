package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const declareTemplate = `{
  "_说明": "测试模板",
  "items": [
    { "key": "ui.font_scale", "file": "ui.cfg", "default": "100", "sensitive": false, "writable": true, "type": "int", "desc": "字号" }
  ]
}
`

// TestDeclareWritesTemplateAndRuntime 声明新键——模板与运行副本同步插入 + 内存立即可读。
func TestDeclareWritesTemplateAndRuntime(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "schema.json.example")
	runtimePath := filepath.Join(dir, "schema.json")
	for _, path := range []string{templatePath, runtimePath} {
		if err := os.WriteFile(path, []byte(declareTemplate), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := LoadSchema(runtimePath)
	if err != nil {
		t.Fatal(err)
	}
	schema.TemplatePath = templatePath

	file, newSegment, errText := schema.Declare("ui.new_flag", "测试项")
	if errText != "" {
		t.Fatalf("声明失败: %s", errText)
	}
	if file != "ui.cfg" || newSegment {
		t.Fatalf("归属不符: file=%q newSegment=%v", file, newSegment)
	}
	if item := schema.Find("ui.new_flag"); item == nil || !item.Writable || item.Type != "string" || item.Desc != "测试项" {
		t.Fatalf("内存项不符: %+v", item)
	}
	raw, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, `"key": "ui.new_flag"`) {
		t.Fatalf("模板未插入: %s", text)
	}
	var parsed struct {
		Items []SchemaItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("模板 JSON 损坏: %v", err)
	}
	if len(parsed.Items) != 2 {
		t.Fatalf("模板项数不符: %d", len(parsed.Items))
	}
	runtimeRaw, err := os.ReadFile(runtimePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(runtimeRaw) != text {
		t.Fatal("运行副本应与模板逐字节一致")
	}

	// 新段——文件归属按「段名.cfg」推断
	file, newSegment, errText = schema.Declare("newseg.k", "")
	if errText != "" || file != "newseg.cfg" || !newSegment {
		t.Fatalf("新段声明: file=%q newSegment=%v err=%s", file, newSegment, errText)
	}
	if schema.Find("newseg.k").Desc != "由 config-set declare 声明（schema 派生段）" {
		t.Fatal("空描述应回落固定文案")
	}

	// 失败面
	if _, _, errText := schema.Declare("novdot", ""); !strings.Contains(errText, "键缺少段前缀") {
		t.Fatalf("缺点号: %s", errText)
	}
	if _, _, errText := schema.Declare("a b.c", ""); !strings.Contains(errText, "键名只允许字母 / 数字 / 下划线") {
		t.Fatalf("非法名: %s", errText)
	}
	if _, _, errText := schema.Declare("ui.new_flag", ""); !strings.Contains(errText, "配置键已声明") {
		t.Fatalf("重复声明: %s", errText)
	}
	schema.TemplatePath = ""
	if _, _, errText := schema.Declare("ui.other", ""); !strings.Contains(errText, "schema 模板未定位") {
		t.Fatalf("模板缺失: %s", errText)
	}
}

// TestDeclarePreservesBOM 模板带 BOM 时双写保留 BOM。
func TestDeclarePreservesBOM(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "schema.json.example")
	runtimePath := filepath.Join(dir, "schema.json")
	body := append([]byte{0xEF, 0xBB, 0xBF}, []byte(declareTemplate)...)
	for _, path := range []string{templatePath, runtimePath} {
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := LoadSchema(runtimePath)
	if err != nil {
		t.Fatal(err)
	}
	schema.TemplatePath = templatePath
	if _, _, errText := schema.Declare("ui.bom_flag", ""); errText != "" {
		t.Fatalf("声明失败: %s", errText)
	}
	raw, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !hasUTF8BOM(raw) {
		t.Fatal("模板 BOM 丢失")
	}
}

// TestAddFileRegistersMissingFile 新段映射在文件不存在时也注册——随后 Save 落到该文件。
func TestAddFileRegistersMissingFile(t *testing.T) {
	dir := t.TempDir()
	store := LoadStore(dir)
	path := filepath.Join(dir, "zz.cfg")
	store.AddFile("zz", path)
	store.Set("zz.k", "1")
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("新段文件未落盘: %v", err)
	}
	if string(raw) != "zz.k=1\r\n" {
		t.Fatalf("落盘字节不符: %q", string(raw))
	}
}

// TestResetToDefault 单项 / 全群 / 只读拒绝 / 未声明拒绝。
func TestResetToDefault(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ui.cfg"), []byte("ui.font_scale=250\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := LoadStore(dir)
	schema := &Schema{Items: []SchemaItem{
		{Key: "ui.font_scale", File: "ui.cfg", Default: "100", Writable: true},
		{Key: "llm.thinking", File: "llm.cfg", Default: "enabled", Writable: false},
	}}
	store.RegisterSchema(schema)

	if ok, reason := store.ResetToDefault("ui.font_scale", schema); !ok {
		t.Fatalf("单项还原失败: %s", reason)
	}
	if value, _ := store.TryGet("ui.font_scale"); value != "100" {
		t.Fatalf("还原值不符: %q", value)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "ui.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "ui.font_scale=100") {
		t.Fatalf("落盘未还原: %q", string(raw))
	}
	if ok, reason := store.ResetToDefault("llm.thinking", schema); ok || !strings.Contains(reason, "只读配置项") {
		t.Fatalf("只读应拒: %v %s", ok, reason)
	}
	if ok, reason := store.ResetToDefault("x.y", schema); ok || !strings.Contains(reason, "配置键未在 schema 声明") {
		t.Fatalf("未声明应拒: %v %s", ok, reason)
	}
	if ok, reason := store.ResetToDefault("", schema); !ok {
		t.Fatalf("全群还原失败: %s", reason)
	}
	if count := countWritableSchema(schema); count != 1 {
		t.Fatalf("可写计数不符: %d", count)
	}
}

// countWritableSchema 测试侧可写项计数。
func countWritableSchema(schema *Schema) int {
	n := 0
	for i := range schema.Items {
		if schema.Items[i].Writable {
			n++
		}
	}
	return n
}
