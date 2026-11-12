// Package bricks 的配置桥积木——config.bridge（配置自改四工具）与 catcfg.bridge（每猫配置两工具）。
//
// 行为逐条移植 mau:Bricks/PACK/BRIK-PACK-004_config.bridge.cs 与 BRIK-PACK-005_catcfg.bridge.cs：
// 参数面零容忍 → 读面单一出口（schema 声明序 + 未声明落盘键）→ 写入唯一实现（SetChecked / ResetToDefault）
// → 每猫配置翻译成内核指令行（cat.cfg.get / cat.cfg.set）后由宿主指令服务执行。
package bricks

import (
	"strconv"
	"strings"
	"unicode/utf16"
)

// ConfigItem 是配置声明项（读面元数据；对应 C# ConfigSchema.Item）。
type ConfigItem struct {
	// Key 配置键
	Key string
	// File 文件归属
	File string
	// Default schema 默认值
	Default string
	// Desc 描述
	Desc string
	// EmptyDesc 空值语义文案
	EmptyDesc string
	// Type 值类型
	Type string
	// Min 数值下限
	Min string
	// Max 数值上限
	Max string
	// Sensitive 敏感标志
	Sensitive bool
	// Writable 可写标志
	Writable bool
}

// ConfigEntry 是配置有效值（读面统一结构；对应 C# ConfigEffective.Entry）。
type ConfigEntry struct {
	// Key 配置键
	Key string
	// Value 有效值（未掩码）
	Value string
	// Source 来源：file / env / default
	Source string
	// Default schema 默认值（未声明为空串）
	Default string
	// Desc 描述
	Desc string
	// EmptyDesc 空值语义文案
	EmptyDesc string
	// Type 值类型
	Type string
	// Min 数值下限
	Min string
	// Max 数值上限
	Max string
	// Declared 是否在 schema 声明
	Declared bool
	// Writable 可写标志（未声明恒 false）
	Writable bool
	// Sensitive 敏感标志
	Sensitive bool
}

// ConfigService 是配置面窄接口——宿主把 config.Store / config.Schema 适配成本接口（组合根单点）。
type ConfigService interface {
	// AllEntries 全项解析（schema 声明序 + 未声明落盘键追加尾部）
	AllEntries() []ConfigEntry
	// Resolve 单项解析（三层兜底 file → env → default）
	Resolve(key string) ConfigEntry
	// Values 落盘显式值（首次出现序）
	Values() [][2]string
	// SchemaItems schema 声明项（声明序；nil = schema 未绑定）
	SchemaItems() []ConfigItem
	// DeclareConfig 声明新键（模板 + 运行副本同步写）；registerNote 是段注册说明（空 = 无）
	DeclareConfig(key, desc string) (file string, newSegment bool, registerNote, errText string)
	// SetChecked 受控写入（白名单 + 值域 + 原子写 + 回滚）
	SetChecked(key, value string) (bool, string)
	// ResetToDefault 还原默认（key 空 = 全群 writable）
	ResetToDefault(key string) (bool, string)
}

// ConfigBridge 配置工具桥——method 白名单 + argsJson 展平参数（list / get / set / reset）。
func ConfigBridge(svc ConfigService, method, argsJSON string) (string, bool) {
	allowed := ""
	required := ""
	switch method {
	case "list":
	case "get":
		allowed = "key"
		required = "key"
	case "set":
		allowed = "key value declare desc"
		required = "key value"
	case "reset":
		allowed = "key"
	default:
		return "ERR|UNKNOWN_METHOD|config." + method, false
	}
	if bad := Validate(argsJSON, allowed, required, "", ""); bad != "" {
		return bad, false
	}
	if svc == nil {
		return "ERR|CONFIG_NO_STORE|宿主未注入 ConfigStore", false
	}
	switch method {
	case "list":
		return configList(svc), true
	case "get":
		return configGet(svc, Get(argsJSON, "key")), true
	case "set":
		declare := strings.EqualFold(Get(argsJSON, "declare"), "true")
		return configSet(svc, Get(argsJSON, "key"), Get(argsJSON, "value"), declare, Get(argsJSON, "desc")), true
	}
	return configReset(svc, Get(argsJSON, "key")), true
}

// configList 配置全览——schema 声明全项 + 未声明落盘键；敏感键掩码。
func configList(svc ConfigService) string {
	items := svc.AllEntries()
	var body strings.Builder
	writable := 0
	sensitive := 0
	for _, entry := range items {
		value := entry.Value
		if entry.Sensitive && value != "" {
			value = "****"
		}
		body.WriteString(entry.Key)
		body.WriteByte('=')
		body.WriteString(value)
		body.WriteString(" | 来源=")
		body.WriteString(entry.Source)
		if entry.Declared {
			body.WriteString(" | 默认=")
			body.WriteString(entry.Default)
			body.WriteString(" | ")
			if entry.Writable {
				body.WriteString("可写")
			} else {
				body.WriteString("只读")
			}
			if entry.EmptyDesc != "" {
				body.WriteString(" | ")
				body.WriteString(entry.EmptyDesc)
			}
			if entry.Type != "" {
				body.WriteString(" | 类型=")
				body.WriteString(entry.Type)
				if entry.Min != "" {
					body.WriteString("[" + entry.Min + "," + entry.Max + "]")
				}
			}
		}
		body.WriteString(" | ")
		body.WriteString(entry.Desc)
		body.WriteString("\r\n")
	}
	for _, entry := range items {
		if entry.Declared && entry.Writable {
			writable++
		}
		if entry.Sensitive {
			sensitive++
		}
	}
	head := MetaHeadFields("config-list", true, "", len(items), [][2]any{
		{"writable", writable},
		{"sensitive", sensitive},
		{"sections", strings.Join(configSegments(svc), " / ")},
	})
	summary := itoa(len(items)) + " 项 · 可写 " + itoa(writable) + " · 敏感 " + itoa(sensitive)
	return head + "\n" + summary + "\n" + trimConfigResult(body.String(), 20000)
}

// configGet 单项查询——未声明且未落盘 → ERR|NOT_FOUND（含段 / 键清单）；未声明但落盘 → 输出并标注。
func configGet(svc ConfigService, key string) string {
	if key == "" {
		return "ERR|BAD_ARGS|缺少参数 key"
	}
	entry := svc.Resolve(key)
	if !entry.Declared && entry.Source == "" {
		return "ERR|NOT_FOUND|" + describeMissing(svc, key)
	}
	value := entry.Value
	if entry.Sensitive && value != "" {
		value = "****"
	}
	var sb strings.Builder
	sb.WriteString(key)
	sb.WriteByte('=')
	sb.WriteString(value)
	sb.WriteString(" | 来源=")
	sb.WriteString(entry.Source)
	if entry.Declared {
		sb.WriteString(" | 默认=")
		sb.WriteString(entry.Default)
		sb.WriteString(" | ")
		if entry.Writable {
			sb.WriteString("可写")
		} else {
			sb.WriteString("只读")
		}
		if entry.EmptyDesc != "" {
			sb.WriteString(" | ")
			sb.WriteString(entry.EmptyDesc)
		}
	}
	sb.WriteString(" | ")
	sb.WriteString(entry.Desc)
	head := MetaHeadFields("config-get", true, key, -1, [][2]any{
		{"source", entry.Source},
		{"declared", entry.Declared},
		{"writable", entry.Writable},
		{"sensitive", entry.Sensitive},
	})
	return head + "\n" + key + " | 来源 " + entry.Source + "\n" + sb.String()
}

// configSet 白名单写（唯一实现 SetChecked）；declare=true 时未声明键先入 schema 再写入。
func configSet(svc ConfigService, key, value string, declare bool, desc string) string {
	if key == "" {
		return "ERR|BAD_ARGS|缺少参数 key"
	}
	declared := false
	for _, item := range svc.SchemaItems() {
		if item.Key == key {
			declared = true
			break
		}
	}
	declareNote := ""
	if !declared {
		if !declare {
			return "ERR|CONFIG_REJECT|配置键未在 schema 声明: " + key + "——新建请加 declare=true（可配 desc=说明）"
		}
		if svc.SchemaItems() == nil {
			return "ERR|CONFIG_DECLARE|schema 未绑定"
		}
		file, _, registerNote, errText := svc.DeclareConfig(key, desc)
		if errText != "" {
			return "ERR|CONFIG_DECLARE|" + errText
		}
		declareNote = " · 已声明（段文件 " + file + "）" + registerNote
	} else if declare {
		declareNote = " · 键已声明，declare 忽略"
	}
	if ok, reason := svc.SetChecked(key, value); !ok {
		return "ERR|CONFIG_REJECT|" + reason
	}
	sensitive := svc.Resolve(key).Sensitive
	display := value
	if sensitive {
		display = "****"
	}
	head := MetaHeadFields("config-set", true, key, -1, [][2]any{
		{"sensitive", sensitive},
		{"declaredNow", !declared},
	})
	return head + "\n" + key + " | 已更新 = " + display + declareNote
}

// configReset 还原默认——key 空 = 全群 writable 项。
func configReset(svc ConfigService, key string) string {
	if ok, reason := svc.ResetToDefault(key); !ok {
		return "ERR|CONFIG_REJECT|" + reason
	}
	all := key == ""
	resetCount := 1
	if all {
		resetCount = countWritable(svc.SchemaItems())
	}
	scope := "single"
	if all {
		scope = "all"
	}
	target := key
	if all {
		target = ""
	}
	head := MetaHeadFields("config-reset", true, target, resetCount, [][2]any{{"scope", scope}})
	if all {
		return head + "\n" + "全部可写配置项 | 已还原 " + itoa(resetCount) + " 项"
	}
	return head + "\n" + key + " | 已还原"
}

// configSegments 段清单——schema 声明键前缀 ∪ 落盘键前缀（去重保序，忽略大小写）。
func configSegments(svc ConfigService) []string {
	segments := []string{}
	add := func(segment string) {
		if segment == "" {
			return
		}
		for _, existing := range segments {
			if strings.EqualFold(existing, segment) {
				return
			}
		}
		segments = append(segments, segment)
	}
	for _, item := range svc.SchemaItems() {
		add(segmentOf(item.Key))
	}
	for _, pair := range svc.Values() {
		add(segmentOf(pair[0]))
	}
	return segments
}

// describeMissing 缺键说明——分「段不存在」/「键不存在」，段存在时列段内键（上限 8 条）。
func describeMissing(svc ConfigService, key string) string {
	segments := configSegments(svc)
	segment := segmentOf(key)
	if segment == "" {
		return "键缺少段前缀（形如 ui.font_scale）——可用段: " + strings.Join(segments, " / ")
	}
	known := false
	for _, existing := range segments {
		if strings.EqualFold(existing, segment) {
			known = true
			break
		}
	}
	if !known {
		return "段不存在: " + segment + "——可用段: " + strings.Join(segments, " / ")
	}
	keys := []string{}
	for _, item := range svc.SchemaItems() {
		if strings.EqualFold(segmentOf(item.Key), segment) {
			keys = append(keys, item.Key)
		}
	}
	for _, pair := range svc.Values() {
		if !strings.EqualFold(segmentOf(pair[0]), segment) {
			continue
		}
		dup := false
		for _, existing := range keys {
			if existing == pair[0] {
				dup = true
				break
			}
		}
		if !dup {
			keys = append(keys, pair[0])
		}
	}
	var sb strings.Builder
	sb.WriteString("键不存在: ")
	sb.WriteString(key)
	sb.WriteString("——段 ")
	sb.WriteString(segment)
	sb.WriteString(" 现有键 ")
	sb.WriteString(itoa(len(keys)))
	sb.WriteString(" 个")
	if len(keys) > 0 {
		show := len(keys)
		if show > 8 {
			show = 8
		}
		sb.WriteString(": ")
		for i := 0; i < show; i++ {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(keys[i])
		}
		if len(keys) > show {
			sb.WriteString(" …")
		}
	}
	return sb.String()
}

// segmentOf 段名提取——键首点号前缀；无点号或点号在首位返回空串。
func segmentOf(key string) string {
	dot := strings.IndexByte(key, '.')
	if dot <= 0 {
		return ""
	}
	return key[:dot]
}

// countWritable 可写项计数——全群还原的规模量（schema 未绑定时 0）。
func countWritable(items []ConfigItem) int {
	n := 0
	for _, item := range items {
		if item.Writable {
			n++
		}
	}
	return n
}

// trimConfigResult 结果截断——超长文本保留头部 + 截断提示（按 UTF-16 码元计数，对齐 C# string.Length）。
func trimConfigResult(text string, max int) string {
	units := utf16.Encode([]rune(text))
	if len(units) <= max {
		return text
	}
	return string(utf16.Decode(units[:max])) + "\n…[截断: 共 " + itoa(len(units)) + " 字符，仅保留前 " + itoa(max) + "]"
}

// CatCfgBridge 每猫配置桥——结构化参数翻译成内核指令行（cat.cfg.get / cat.cfg.set）后交宿主指令服务。
func CatCfgBridge(command func(string) string, method, argsJSON string) (string, bool) {
	allowed := ""
	required := ""
	switch method {
	case "get":
		allowed = "cat"
		required = "cat"
	case "set":
		allowed = "cat field value"
		required = "cat field value"
	default:
		return "ERR|UNKNOWN_METHOD|catcfg." + method, false
	}
	if bad := Validate(argsJSON, allowed, required, "", ""); bad != "" {
		return bad, false
	}
	cat := Get(argsJSON, "cat")
	if command == nil {
		return "ERR|HOSTCMD_NO_SERVICE|宿主指令服务未注入（宿主未接线）", false
	}
	line := "cat.cfg.get " + cat
	tool := "config-cat-get"
	verb := "读取"
	if method == "set" {
		line = "cat.cfg.set " + cat + " " + Get(argsJSON, "field") + " " + Get(argsJSON, "value")
		tool = "config-cat-set"
		verb = "写入"
	}
	reply := command(line)
	if reply == "" {
		reply = "ERR|EMPTY_RESULT|宿主指令无回执"
	}
	// 错误面不动——失败仍走既有 ERR|CODE|消息（不包装头）
	if strings.HasPrefix(reply, "ERR|") {
		return reply, false
	}
	head := MetaHeadFields(tool, true, cat, -1, nil)
	return head + "\n" + cat + " | cat.cfg " + verb + "\n" + reply, true
}

// itoa 十进制渲染（配置桥内部用——避免与既有包级标识符重名）。
func itoa(v int) string {
	return strconv.Itoa(v)
}
