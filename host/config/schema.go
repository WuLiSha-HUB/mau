package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SchemaItem 是 schema 声明中的一项配置。
//
// 字段对齐 C# 版 ConfigSchema.Item（mau:config/schema.json.example）：key / file /
// default / sensitive / writable 必有，type / min / max / values / empty_desc 可选。
// min / max 在模板里是字符串形态，保持原样（前端按字符串渲染）。
type SchemaItem struct {
	// Key 配置键（带文件前缀段，如 llm.reasoning_effort）
	Key string `json:"key"`
	// File 文件归属（如 llm.cfg）
	File string `json:"file"`
	// Default schema 默认值
	Default string `json:"default"`
	// Sensitive 敏感标志（回显掩码）
	Sensitive bool `json:"sensitive"`
	// Writable 可写标志（写面白名单）
	Writable bool `json:"writable"`
	// Type 值类型（string / int / bool）
	Type string `json:"type,omitempty"`
	// Min 数值下限（字符串形态，保持模板原文）
	Min string `json:"min,omitempty"`
	// Max 数值上限（字符串形态，保持模板原文）
	Max string `json:"max,omitempty"`
	// Values 枚举值域（string 类）
	Values []string `json:"values,omitempty"`
	// EmptyDesc 空值语义文案
	EmptyDesc string `json:"empty_desc,omitempty"`
	// Desc 描述
	Desc string `json:"desc"`
}

// Schema 是配置声明表——按声明序保存，另建键索引供 O(1) 查找。
type Schema struct {
	// Items 声明项（保持模板顺序）
	Items []SchemaItem
	byKey map[string]*SchemaItem
	// SourcePath 运行副本路径（Load 时记录；声明通道据此写回）
	SourcePath string
	// TemplatePath schema 模板路径（宿主注入；声明通道同时改写模板与运行副本，避免启动期模板同步覆盖）
	TemplatePath string
}

// Find 按键查声明项；未声明返回 nil。
func (s *Schema) Find(key string) *SchemaItem {
	if s == nil {
		return nil
	}
	if s.byKey != nil {
		return s.byKey[key]
	}
	// 索引缺失（字面量构造）——线性兜底，不静默当「未声明」
	for i := range s.Items {
		if s.Items[i].Key == key {
			return &s.Items[i]
		}
	}
	return nil
}

// schemaFile 是 schema.json 的磁盘形态——_说明字段是模板注释，解析时忽略。
type schemaFile struct {
	Items []SchemaItem `json:"items"`
}

// LoadSchema 读取并解析 schema.json；文件缺失或损坏返回错误（调用方决定降级）。
func LoadSchema(path string) (*Schema, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 schema 失败: %w", err)
	}
	var file schemaFile
	if err := json.Unmarshal(StripBOM(raw), &file); err != nil {
		return nil, fmt.Errorf("解析 schema 失败: %w", err)
	}
	s := &Schema{Items: file.Items, byKey: make(map[string]*SchemaItem, len(file.Items)), SourcePath: path}
	for i := range s.Items {
		s.byKey[s.Items[i].Key] = &s.Items[i]
	}
	return s, nil
}

// IsSensitive 按键名兜底判定敏感（api_key / secret / token 系，大小写不敏感）。
func (s *Schema) IsSensitive(key string) bool {
	if item := s.Find(key); item != nil {
		return item.Sensitive
	}
	return IsSecretKey(key)
}

// SyncResult 是 schema 同步的三种结局。
type SyncResult struct {
	// Template 命中的模板路径（空 = 两源皆缺）
	Template string
	// Outcome seed / up-to-date / overwritten / template-missing / failed
	Outcome string
	// Detail 补充说明（备份路径 / 错误文本）
	Detail string
}

// SyncSchema 把模板同步进运行区 Data/config/schema.json。
//
// 语义与 C# EnsureSchemaSync 一致：运行区副本是派生件，不是用户资产。
//   - 模板两源候选：可执行目录/config/schema.json.example → 仓库根/config/schema.json.example
//   - 目标缺失 → 种子复制
//   - 逐字节相同 → 零写入
//   - 内容不同 → 备份 .bak 后整体覆盖
//   - 两源皆缺 → 保持现状并出声（不静默）
func SyncSchema(configDir string) SyncResult {
	target := filepath.Join(configDir, "schema.json")
	template := ""
	for _, candidate := range []string{
		filepath.Join(ExeDir(), "config", "schema.json.example"),
		filepath.Join(FindRepoRoot(ExeDir()), "config", "schema.json.example"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			template = candidate
			break
		}
	}
	if template == "" {
		return SyncResult{Outcome: "template-missing", Detail: "保持现有 schema（声明面可能过期，请检查 config/）"}
	}
	same, existErr := sameBytes(template, target)
	if existErr != nil {
		// 目标不可读（缺失视同不同，走复制）
		if os.IsNotExist(existErr) {
			if err := os.MkdirAll(configDir, 0o755); err != nil {
				return SyncResult{Template: template, Outcome: "failed", Detail: err.Error()}
			}
			if err := copyFile(template, target); err != nil {
				return SyncResult{Template: template, Outcome: "failed", Detail: err.Error()}
			}
			return SyncResult{Template: template, Outcome: "seed", Detail: template + " → " + target}
		}
		return SyncResult{Template: template, Outcome: "failed", Detail: existErr.Error()}
	}
	if same {
		return SyncResult{Template: template, Outcome: "up-to-date"}
	}
	backup := target + ".bak"
	if err := copyFile(target, backup); err != nil {
		return SyncResult{Template: template, Outcome: "failed", Detail: "备份失败: " + err.Error()}
	}
	if err := copyFile(template, target); err != nil {
		return SyncResult{Template: template, Outcome: "failed", Detail: "覆盖失败: " + err.Error()}
	}
	return SyncResult{Template: template, Outcome: "overwritten", Detail: "旧文件备份 " + backup}
}

// sameBytes 判定两文件逐字节相同；返回错误表示目标不可读。
func sameBytes(left, right string) (bool, error) {
	l, err := os.ReadFile(left)
	if err != nil {
		return false, err
	}
	r, err := os.ReadFile(right)
	if err != nil {
		return false, err
	}
	if len(l) != len(r) {
		return false, nil
	}
	for i := range l {
		if l[i] != r[i] {
			return false, nil
		}
	}
	return true, nil
}

// copyFile 复制文件（目标父目录自动创建）。
func copyFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, raw, 0o644)
}

// Declare 声明新配置项——内存 schema 立即生效 + 模板与运行副本同步插入（对齐 C# ConfigSchema.Declare）。
//
// 约束：键须带段前缀（形如 x.key）；段已有声明项则沿用其文件归属，否则按「段名.cfg」推断。
// 模板路径为空 / 不存在时失败——只写运行副本会被下次启动的模板同步覆盖（声明丢失）。
// 返回 (归属文件名, 是否新建段, 失败原因)。
func (s *Schema) Declare(key, desc string) (fileName string, newSegment bool, errText string) {
	if s == nil {
		return "", false, "schema 未绑定"
	}
	if key == "" {
		return "", false, "key 为空"
	}
	dot := strings.IndexByte(key, '.')
	if dot <= 0 || dot == len(key)-1 {
		return "", false, "键缺少段前缀（形如 ui.font_scale）"
	}
	segment := key[:dot]
	if !isSimpleName(segment) || !isSimpleName(key[dot+1:]) {
		return "", false, "键名只允许字母 / 数字 / 下划线（段与键名各一段）: " + key
	}
	if s.Find(key) != nil {
		return "", false, "配置键已声明: " + key
	}

	// [段1] 文件归属——段已声明则沿用其文件，否则按「段名.cfg」推断
	file := ""
	segmentKnown := false
	for i := range s.Items {
		if !strings.EqualFold(KeyPrefix(s.Items[i].Key), segment) {
			continue
		}
		segmentKnown = true
		if file == "" {
			file = s.Items[i].File
		}
	}
	if file == "" {
		file = segment + ".cfg"
	}
	newSegment = !segmentKnown

	// [段2] 模板与运行副本文本插入（同一份内容写两处）
	if s.TemplatePath == "" {
		return "", false, "schema 模板未定位（TemplatePath 空或文件不存在）——声明通道需要模板"
	}
	if _, err := os.Stat(s.TemplatePath); err != nil {
		return "", false, "schema 模板未定位（TemplatePath 空或文件不存在）——声明通道需要模板"
	}
	if s.SourcePath == "" {
		return "", false, "schema 运行副本路径未知（Load 未记录 SourcePath）"
	}
	raw, err := os.ReadFile(s.TemplatePath)
	if err != nil {
		return "", false, "schema 模板读取失败: " + err.Error()
	}
	hasBOM := hasUTF8BOM(raw)
	updated := insertSchemaItem(string(StripBOM(raw)), buildSchemaItemLine(key, file, desc))
	if updated == "" {
		return "", false, "schema 模板结构不识别（未找到 items 数组闭合）: " + s.TemplatePath
	}
	body := []byte(updated)
	if hasBOM {
		body = append([]byte{0xEF, 0xBB, 0xBF}, body...)
	}
	if err := os.WriteFile(s.TemplatePath, body, 0o644); err != nil {
		return "", false, "schema 模板写入失败: " + err.Error()
	}
	if err := os.WriteFile(s.SourcePath, body, 0o644); err != nil {
		return "", false, "schema 运行副本写入失败: " + err.Error()
	}

	// [段3] 内存项——声明后立即可读可写（无需重启）
	s.Items = append(s.Items, SchemaItem{
		Key:       key,
		File:      file,
		Default:   "",
		Writable:  true,
		Type:      "string",
		Desc:      schemaDeclareDesc(desc),
		Sensitive: false,
	})
	// append 可能移动底层数组——索引全量重建（旧指针失效）
	s.byKey = make(map[string]*SchemaItem, len(s.Items))
	for i := range s.Items {
		s.byKey[s.Items[i].Key] = &s.Items[i]
	}
	return file, newSegment, ""
}

// schemaDeclareDesc 声明项描述——空则回落固定文案（对齐 C# BuildItemLine）。
func schemaDeclareDesc(desc string) string {
	if desc == "" {
		return "由 config-set declare 声明（schema 派生段）"
	}
	return desc
}

// buildSchemaItemLine 新项 JSON 行构造——缩进 4 空格，与模板既有排版一致。
func buildSchemaItemLine(key, file, desc string) string {
	return "    { \"key\": " + jsonQuote(key) + ", \"file\": " + jsonQuote(file) +
		", \"default\": \"\", \"sensitive\": false, \"writable\": true, \"type\": \"string\", \"desc\": " +
		jsonQuote(schemaDeclareDesc(desc)) + " }"
}

// insertSchemaItem 在 items 数组闭合行（"\n  ]"）前追加一项——补前项逗号 + 换行风格跟随原文件。
// 结构不识别返回空串（调用方出声，不静默写坏模板）。
func insertSchemaItem(text, itemLine string) string {
	if text == "" {
		return ""
	}
	closeAt := strings.LastIndex(text, "\n  ]")
	if closeAt < 0 {
		return ""
	}
	newline := "\n"
	if strings.Contains(text, "\r\n") {
		newline = "\r\n"
	}
	head := text[:closeAt]
	tail := text[closeAt:]
	last := len(head) - 1
	for last >= 0 {
		ch := head[last]
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			last--
			continue
		}
		break
	}
	if last >= 0 && head[last] != ',' && head[last] != '[' {
		head += ","
	}
	return head + newline + itemLine + tail
}

// isSimpleName 简易名判定——字母 / 数字 / 下划线，非空。
func isSimpleName(text string) bool {
	if text == "" {
		return false
	}
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' {
			continue
		}
		return false
	}
	return true
}

// hasUTF8BOM 判定文件头是否有 UTF-8 BOM。
func hasUTF8BOM(raw []byte) bool {
	return len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF
}

// jsonQuote JSON 字符串字面量——只转义引号 / 反斜杠 / 控制字符，非 ASCII 原样保留（与模板排版一致）。
func jsonQuote(text string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range text {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString(`\u`)
				const hex = "0123456789abcdef"
				b.WriteByte(hex[(r>>12)&0xF])
				b.WriteByte(hex[(r>>8)&0xF])
				b.WriteByte(hex[(r>>4)&0xF])
				b.WriteByte(hex[r&0xF])
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
