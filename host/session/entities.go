package session

import (
	"encoding/json"
	"strconv"
	"strings"
)

// entityDecodeExemptArgs 是内容面豁免名单——这些参数承载「写什么存什么」的正文语义。
var entityDecodeExemptArgs = []string{
	"content", "body", "code", "codes", "value", "new", "findings", "cmd", "command",
	"expression", "question", "text", "purpose", "push", "persona", "description", "note",
}

// isEntityDecodeExempt 判定内容面豁免参数。
func isEntityDecodeExempt(name string) bool {
	for _, item := range entityDecodeExemptArgs {
		if item == name {
			return true
		}
	}
	return false
}

// DecodeEntities HTML/XML 实体解码——命名实体（amp/lt/gt/quot/apos）+ 数字实体；只解一层。
func DecodeEntities(src string) string {
	if src == "" || !strings.Contains(src, "&") {
		return src
	}
	var b strings.Builder
	i := 0
	for i < len(src) {
		c := src[i]
		if c != '&' {
			b.WriteByte(c)
			i++
			continue
		}
		semi := strings.IndexByte(src[i+1:], ';')
		if semi < 0 {
			b.WriteByte(c)
			i++
			continue
		}
		semi += i + 1
		span := semi - i
		if span < 3 || span > 9 {
			b.WriteByte(c)
			i++
			continue
		}
		body := src[i+1 : semi]
		decoded, ok := decodeEntityBody(body)
		if !ok {
			b.WriteByte(c)
			i++
			continue
		}
		b.WriteString(decoded)
		i = semi + 1
	}
	return b.String()
}

// decodeEntityBody 单个实体体解码——未知实体返回 ok=false（原样保留）。
func decodeEntityBody(body string) (string, bool) {
	switch body {
	case "amp":
		return "&", true
	case "lt":
		return "<", true
	case "gt":
		return ">", true
	case "quot":
		return `"`, true
	case "apos":
		return "'", true
	}
	if len(body) > 1 && body[0] == '#' {
		hex := strings.HasPrefix(body, "#x") || strings.HasPrefix(body, "#X")
		digits := body[1:]
		if hex {
			digits = body[2:]
		}
		if digits == "" {
			return "", false
		}
		base := 10
		if hex {
			base = 16
		}
		value, err := strconv.ParseInt(digits, base, 32)
		if err != nil || value <= 0 {
			return "", false
		}
		return string(rune(value)), true
	}
	return "", false
}

// decodeArgEntities 工具参数实体解码——对象级逐属性处理，内容面豁免。
func decodeArgEntities(toolName, argsJSON string) string {
	if argsJSON == "" || !strings.Contains(argsJSON, "&") {
		return argsJSON
	}
	fields, err := parseOrderedObject(argsJSON)
	if err != nil {
		// 参数 JSON 非法——原样透传（下游按 BAD_ARGS 出声）
		return argsJSON
	}
	var changed []string
	rebuilt := make([]string, 0, len(fields))
	for _, field := range fields {
		var original string
		if err := json.Unmarshal(field.Value, &original); err != nil {
			rebuilt = append(rebuilt, jsonStr(field.Key)+":"+string(field.Value))
			continue
		}
		if isEntityDecodeExempt(field.Key) {
			rebuilt = append(rebuilt, jsonStr(field.Key)+":"+string(field.Value))
			continue
		}
		decoded := DecodeEntities(original)
		if decoded == original {
			rebuilt = append(rebuilt, jsonStr(field.Key)+":"+string(field.Value))
			continue
		}
		changed = append(changed, field.Key)
		rebuilt = append(rebuilt, jsonStr(field.Key)+":"+jsonStr(decoded))
	}
	if len(changed) == 0 {
		return argsJSON
	}
	logAt(LogModuleHost, 2, "工具参数实体解码: "+toolName+" · 字段 "+strings.Join(changed, ","), LogCategoryTool)
	return "{" + strings.Join(rebuilt, ",") + "}"
}
