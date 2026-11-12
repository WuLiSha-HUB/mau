package session

import (
	"math"
	"strconv"
	"strings"

	"cathome5/host/toolpool"
)

// BuildToolCard 构建工具卡载荷——name / arguments / result / toolIndex / toolTotal /
// order / durMs / queueMs / runMs 九字段的唯一拼装处。
//
// result 传 nil = 先行「进行中」卡（不写该字段，前端按处理中渲染）；
// 传空串 = 合法空结果（写字段）——两者语义不同，不可合并。
func BuildToolCard(name, arguments string, result *string, index, total int, durMs, queueMs, runMs int64) string {
	var b strings.Builder
	b.WriteString(`{"name":`)
	b.WriteString(jsonStr(name))
	b.WriteString(`,"arguments":`)
	b.WriteString(jsonStr(arguments))
	if result != nil {
		b.WriteString(`,"result":`)
		b.WriteString(jsonStr(*result))
	}
	b.WriteString(`,"toolIndex":`)
	b.WriteString(strconv.Itoa(index))
	b.WriteString(`,"toolTotal":`)
	b.WriteString(strconv.Itoa(total))
	b.WriteString(`,"order":`)
	b.WriteString(jsonStr(toolpool.OrderText(name)))
	b.WriteString(`,"durMs":`)
	b.WriteString(strconv.FormatInt(durMs, 10))
	b.WriteString(`,"queueMs":`)
	b.WriteString(strconv.FormatInt(queueMs, 10))
	b.WriteString(`,"runMs":`)
	b.WriteString(strconv.FormatInt(runMs, 10))
	b.WriteString("}")
	return b.String()
}

// BuildReason 构建思考块载荷——text / durMs / chars / cps 四字段的唯一拼装处。
//
// chars = 正文长度；cps = 字符数 ÷ 思考用时 × 1000（保留 1 位）；durMs ≤ 0 时取 -1。
func BuildReason(text string, durMs int64) string {
	chars := len([]rune(text))
	cps := -1.0
	if durMs > 0 {
		cps = math.Round(float64(chars)*1000.0/float64(durMs)*10) / 10
	}
	var b strings.Builder
	b.WriteString(`{"text":`)
	b.WriteString(jsonStr(text))
	b.WriteString(`,"durMs":`)
	b.WriteString(strconv.FormatInt(durMs, 10))
	b.WriteString(`,"chars":`)
	b.WriteString(strconv.Itoa(chars))
	b.WriteString(`,"cps":`)
	b.WriteString(formatCps(cps))
	b.WriteString("}")
	return b.String()
}

// JSONString 编码 JSON 字符串字面量——与 C# JsonUtil.Serialize 的转义面逐字节同构。
//
// 口径（实测 C# UnsafeRelaxedJsonEscaping）：引号 / 反斜杠 / 控制字符转义；BMP 内非 ASCII 原样直出；
// 非 BMP 码点输出 UTF-16 代理对（\uD83D\uDD34 形态）；不转义 HTML 敏感字符。
func JSONString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
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
			switch {
			case r < 0x20:
				b.WriteString(`\u`)
				b.WriteString(hex4(uint16(r)))
			case r > 0xFFFF:
				value := r - 0x10000
				hi := uint16(0xD800 + (value >> 10))
				lo := uint16(0xDC00 + (value & 0x3FF))
				b.WriteString(`\u`)
				b.WriteString(hex4(hi))
				b.WriteString(`\u`)
				b.WriteString(hex4(lo))
			default:
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// hex4 四字节大写十六进制（System.Text.Json 同形）。
func hex4(value uint16) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{
		digits[(value>>12)&0xF],
		digits[(value>>8)&0xF],
		digits[(value>>4)&0xF],
		digits[value&0xF],
	})
}

// jsonStr 编码 JSON 字符串字面量——单一实现在 JSONString（同一转义口径）。
func jsonStr(s string) string { return JSONString(s) }

// JSONFragment 规整「已是合法 JSON 的文本片段」——与 C# JsonElement 重序列化同口径。
//
// 口径：片段内的非 BMP 码点输出 UTF-16 代理对转义（\uD83D\uDD34 形态），其余字符逐字原样。
// 用途：原样透传的 JSON 片段（assistant tool_calls / tools[].parameters）——C# 侧这些片段经
// JsonDocument 解析后由同一 encoder 重写，non-BMP 一律转义；Go 侧直接透传会在 emoji 处
// 差一处字节（c-7 判例：首个差异偏移 928,350）。
func JSONFragment(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r > 0xFFFF {
			value := r - 0x10000
			hi := uint16(0xD800 + (value >> 10))
			lo := uint16(0xDC00 + (value & 0x3FF))
			b.WriteString(`\u`)
			b.WriteString(hex4(hi))
			b.WriteString(`\u`)
			b.WriteString(hex4(lo))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// itoa 整数转字符串（小工具）。
func itoa(v int) string { return strconv.Itoa(v) }

// itoa64 长整数转字符串。
func itoa64(v int64) string { return strconv.FormatInt(v, 10) }

// formatCps 保留 1 位的数值文本；-1 原样输出为 -1（不做小数补位）。
func formatCps(v float64) string {
	if v == math.Trunc(v) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}
