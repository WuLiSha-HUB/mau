package session

import (
	"encoding/json"
	"fmt"
	"strings"
)

// metaField 是结构化头附加字段——键序 = 声明序（对齐 C# ToolMetaHead 的字典插入序）。
type metaField struct {
	// Key 字段名
	Key string
	// Value 字段值（string / int / int64 / bool）
	Value any
}

// metaHead 构建工具结构化返回体（A214）——首行单行 JSON 头 + 正文摘要行。
//
// 头字段恒定 ok / tool；target 空串省略、items 负值省略；附加字段按声明序追加。
// 正文为空时只返回头（不产出尾随空行）。转义口径与 C# JsonUtil 一致（中文直出、不转义 HTML 敏感字符）。
func metaHead(tool string, ok bool, target string, items int, fields []metaField, body string) string {
	var b strings.Builder
	b.WriteString(`{"ok":`)
	if ok {
		b.WriteString("true")
	} else {
		b.WriteString("false")
	}
	b.WriteString(`,"tool":`)
	b.WriteString(jsonStr(tool))
	if target != "" {
		b.WriteString(`,"target":`)
		b.WriteString(jsonStr(target))
	}
	if items >= 0 {
		b.WriteString(`,"items":`)
		b.WriteString(itoa(items))
	}
	for _, field := range fields {
		b.WriteString(`,"`)
		b.WriteString(field.Key)
		b.WriteString(`":`)
		b.WriteString(metaValue(field.Value))
	}
	b.WriteByte('}')
	if body == "" {
		return b.String()
	}
	return b.String() + "\n" + body
}

// metaValue 结构化头字段值序列化（标量分派）。
func metaValue(value any) string {
	switch typed := value.(type) {
	case string:
		return jsonStr(typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case int:
		return itoa(typed)
	case int64:
		return itoa64(typed)
	}
	return jsonStr("")
}

// orderedField 是有序对象键值对——参数面按 JSON 属性序判定（错误面文案与 C# 枚举序一致）。
type orderedField struct {
	// Key 键名
	Key string
	// Value 原始值 JSON
	Value json.RawMessage
}

// parseOrderedObject 按源序解析 JSON 对象——保留键序（map 会丢序，错误面因此不可复现）。
func parseOrderedObject(src string) ([]orderedField, error) {
	dec := json.NewDecoder(strings.NewReader(src))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, isDelim := tok.(json.Delim)
	if !isDelim || delim != '{' {
		return nil, fmt.Errorf("非对象形态")
	}
	fields := make([]orderedField, 0, 4)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, isKey := keyTok.(string)
		if !isKey {
			return nil, fmt.Errorf("键名非字符串")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		fields = append(fields, orderedField{Key: key, Value: raw})
	}
	return fields, nil
}
