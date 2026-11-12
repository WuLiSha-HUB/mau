// Package bricks 复现 Mau 积木层的工具执行面——参数面零容忍 + A214 结构化头 + 结果正文。
package bricks

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf16"
)

// ParseFail 是参数解析失败哨兵——与 C# JsonArgs 同字面量。
const ParseFail = "§PARSE_FAIL§"

// DeleteKey 是 text-replace 的删除标记。
const DeleteKey = "黑暗剑+22"

// Validate 参数校验——未知键 / 必填缺值 / 枚举非法一律报错；catId 为宿主保留键。
func Validate(argsJSON, allowed, required, enumName, enumValues string) string {
	if argsJSON == "" {
		return "ERR|BAD_ARGS|工具参数 JSON 解析失败（LLM 生成参数可能被截断——超长内容请分段写入）"
	}
	var anyValue any
	if err := json.Unmarshal([]byte(argsJSON), &anyValue); err != nil {
		return "ERR|BAD_ARGS|参数 JSON 解析失败: " + err.Error()
	}
	obj, isObj := anyValue.(map[string]any)
	if !isObj {
		return "ERR|BAD_ARGS|参数必须是 JSON 对象"
	}
	// [段1] 未知键拦截
	for name := range obj {
		if name == "catId" {
			continue
		}
		if !containsToken(allowed, name) {
			supported := "（本工具无参数）"
			if allowed != "" {
				supported = "（支持 " + allowed + "）"
			}
			return "ERR|BAD_ARGS|未知参数: " + name + supported
		}
	}
	// [段2] 必填校验——字符串空值视同缺失
	if required != "" {
		for _, key := range strings.Split(required, " ") {
			if key == "" {
				continue
			}
			v, exists := obj[key]
			if !exists {
				return "ERR|BAD_ARGS|缺参数 " + key + "（必填：" + required + "）"
			}
			if s, isStr := v.(string); isStr && s == "" {
				return "ERR|BAD_ARGS|缺参数 " + key + "（必填：" + required + "）"
			}
		}
	}
	// [段3] 枚举校验——空值放行
	if enumName != "" {
		if raw, exists := obj[enumName]; exists {
			if s, isStr := raw.(string); isStr && s != "" && !containsPipe(enumValues, s) {
				return "ERR|BAD_ARGS|" + enumName + " 非法值: " + s + "（" + enumValues + "）"
			}
		}
	}
	return ""
}

// Get 展平取参——不存在返回空串；非字符串返回 JSON 原文；解析失败返回 ParseFail。
func Get(argsJSON, key string) string {
	if argsJSON == "" {
		return ParseFail
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(argsJSON), &raw); err != nil {
		var anyValue any
		if json.Unmarshal([]byte(argsJSON), &anyValue) != nil {
			return ParseFail
		}
		// 非对象载荷视同无参数
		return ""
	}
	v, exists := raw[key]
	if !exists {
		return ""
	}
	var s string
	if err := json.Unmarshal(v, &s); err == nil {
		return s
	}
	return string(v)
}

// MetaHead 结构化元数据头（A214）——恒定 ok / tool + 主来源 target + 主计数 items。
func MetaHead(tool string, ok bool, target string, items int) string {
	var b strings.Builder
	b.WriteString(`{"ok":`)
	if ok {
		b.WriteString("true")
	} else {
		b.WriteString("false")
	}
	b.WriteString(`,"tool":`)
	b.WriteString(jsonString(tool))
	if target != "" {
		b.WriteString(`,"target":`)
		b.WriteString(jsonString(target))
	}
	if items >= 0 {
		b.WriteString(`,"items":`)
		b.WriteString(strconv.Itoa(items))
	}
	b.WriteByte('}')
	return b.String()
}

// jsonString 把字符串编码成 JSON 字面量——转义口径对齐 System.Text.Json 默认编码器：
// 控制字符 / 引号 / 反斜杠走短转义，非 ASCII 与 HTML 敏感字符（< > & ' +）走 \uXXXX（大写十六进制）。
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r > 0x7E || r == '<' || r == '>' || r == '&' || r == '\'' || r == '+' {
				if r > 0xFFFF {
					high, low := utf16.EncodeRune(r)
					writeUnicodeEscape(&b, high)
					writeUnicodeEscape(&b, low)
				} else {
					writeUnicodeEscape(&b, r)
				}
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// jsonRelaxedString 把字符串编码成 JSON 字面量——转义口径对齐 C# JsonUtil.Serialize（UnsafeRelaxedJsonEscaping）：
// 引号 / 反斜杠 / 控制字符走短转义，U+0000–U+001F 剩项走 \uXXXX（大写），
// BMP 内非 ASCII 与 HTML 敏感字符（< > & ' +）一律原样直出，非 BMP 码点输出 UTF-16 代理对。
//
// 两个口径按「被对齐的 C# 出口」选：回执头（JsonUtil.Str / TextUtil.JsonEscape 系）用 jsonString；
// 结构化回执（C# JsonUtil.Serialize 系——py 的 Run 八键 / List / 服务态四键 / Autostart）用本函数。
// 差异只在非 ASCII 与 HTML 敏感字符：jsonString 转义、本函数直出（C# 实测中文直显）。
func jsonRelaxedString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				writeUnicodeEscape(&b, r)
				continue
			}
			if r > 0xFFFF {
				high, low := utf16.EncodeRune(r)
				writeUnicodeEscape(&b, high)
				writeUnicodeEscape(&b, low)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// writeUnicodeEscape 写单个 \uXXXX 转义（大写十六进制）。
func writeUnicodeEscape(b *strings.Builder, r rune) {
	const hex = "0123456789ABCDEF"
	b.WriteString(`\u`)
	b.WriteByte(hex[(r>>12)&0xF])
	b.WriteByte(hex[(r>>8)&0xF])
	b.WriteByte(hex[(r>>4)&0xF])
	b.WriteByte(hex[r&0xF])
}

// containsToken 报告空格分隔清单里是否含目标。
func containsToken(list, target string) bool {
	if list == "" {
		return false
	}
	for _, it := range strings.Split(list, " ") {
		if it == target {
			return true
		}
	}
	return false
}

// containsPipe 报告竖线分隔清单里是否含目标。
func containsPipe(list, target string) bool {
	for _, it := range strings.Split(list, "|") {
		if it == target {
			return true
		}
	}
	return false
}

// boolArg 布尔参数解析——对齐 bool.TryParse（true/false 大小写不敏感 + 首尾空白）。
func boolArg(raw string) (bool, bool) {
	switch {
	case strings.EqualFold(strings.TrimSpace(raw), "true"):
		return true, true
	case strings.EqualFold(strings.TrimSpace(raw), "false"):
		return false, true
	}
	return false, false
}

// MetaHeadFields 结构化元数据头（A214）——ok / tool / target / items + 调用方字段（按插入序）。
//
// 字段值按类型分派：bool → true/false；int / int64 → 十进制；[]string → JSON 数组（逐元素 jsonString）；
// 其它按字符串转义。
func MetaHeadFields(tool string, ok bool, target string, items int, fields [][2]any) string {
	var b strings.Builder
	b.WriteString(`{"ok":`)
	if ok {
		b.WriteString("true")
	} else {
		b.WriteString("false")
	}
	b.WriteString(`,"tool":`)
	b.WriteString(jsonString(tool))
	if target != "" {
		b.WriteString(`,"target":`)
		b.WriteString(jsonString(target))
	}
	if items >= 0 {
		b.WriteString(`,"items":`)
		b.WriteString(strconv.Itoa(items))
	}
	for _, kv := range fields {
		b.WriteByte(',')
		b.WriteString(jsonString(kv[0].(string)))
		b.WriteByte(':')
		switch v := kv[1].(type) {
		case bool:
			if v {
				b.WriteString("true")
			} else {
				b.WriteString("false")
			}
		case int:
			b.WriteString(strconv.Itoa(v))
		case int64:
			b.WriteString(strconv.FormatInt(v, 10))
		case float64:
			b.WriteString(strconv.FormatFloat(v, 'f', -1, 64))
		case string:
			b.WriteString(jsonString(v))
		case []string:
			b.WriteByte('[')
			for i, item := range v {
				if i > 0 {
					b.WriteByte(',')
				}
				b.WriteString(jsonString(item))
			}
			b.WriteByte(']')
		default:
			b.WriteString("null")
		}
	}
	b.WriteByte('}')
	return b.String()
}
