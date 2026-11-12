package main

// jval —— 保留对象键序的 JSON 值树。
//
// 对拍要判「同结构」（字段与顺序），map 会丢序；因此自己解析
// （json.Decoder 的 Token 流），并附归一化 / 深比较 / 保序序列化。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// jval 是 JSON 值节点；kind 取值 o/a/s/n/b/z。
type jval struct {
	kind    byte
	keys    []string
	obj     map[string]*jval
	arr     []*jval
	str     string
	num     string
	boolean bool
}

// parseJSON 解析 JSON 为保序值树（数字保留原文，避免精度损失）。
func parseJSON(raw []byte) (*jval, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := decodeJVal(dec)
	if err != nil {
		return nil, err
	}
	return v, nil
}

// decodeJVal 递归解码一个值。
func decodeJVal(dec *json.Decoder) (*jval, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch byte(t) {
		case '{':
			out := &jval{kind: 'o', obj: make(map[string]*jval)}
			for dec.More() {
				keyTok, keyErr := dec.Token()
				if keyErr != nil {
					return nil, keyErr
				}
				key, ok := keyTok.(string)
				if !ok {
					return nil, fmt.Errorf("对象键不是字符串: %v", keyTok)
				}
				child, childErr := decodeJVal(dec)
				if childErr != nil {
					return nil, childErr
				}
				out.keys = append(out.keys, key)
				out.obj[key] = child
			}
			if _, endErr := dec.Token(); endErr != nil {
				return nil, endErr
			}
			return out, nil
		case '[':
			out := &jval{kind: 'a'}
			for dec.More() {
				child, childErr := decodeJVal(dec)
				if childErr != nil {
					return nil, childErr
				}
				out.arr = append(out.arr, child)
			}
			if _, endErr := dec.Token(); endErr != nil {
				return nil, endErr
			}
			return out, nil
		}
		return nil, fmt.Errorf("未预期的分隔符: %v", t)
	case string:
		return &jval{kind: 's', str: t}, nil
	case json.Number:
		return &jval{kind: 'n', num: t.String()}, nil
	case bool:
		return &jval{kind: 'b', boolean: t}, nil
	case nil:
		return &jval{kind: 'z'}, nil
	}
	return nil, fmt.Errorf("未预期的 token: %v", tok)
}

// get 取对象字段（非对象 / 缺失返回 nil）。
func (v *jval) get(key string) *jval {
	if v == nil || v.kind != 'o' {
		return nil
	}
	return v.obj[key]
}

// at 取数组元素（越界返回 nil）。
func (v *jval) at(i int) *jval {
	if v == nil || v.kind != 'a' || i < 0 || i >= len(v.arr) {
		return nil
	}
	return v.arr[i]
}

// intVal 取整数值。
func (v *jval) intVal() (int64, bool) {
	if v == nil || v.kind != 'n' {
		return 0, false
	}
	n, err := strconv.ParseInt(v.num, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// kindName 给节点类型名（报告文案用）。
func (v *jval) kindName() string {
	if v == nil {
		return "missing"
	}
	switch v.kind {
	case 'o':
		return "object"
	case 'a':
		return "array"
	case 's':
		return "string"
	case 'n':
		return "number"
	case 'b':
		return "bool"
	default:
		return "null"
	}
}

// writeJVal 保序序列化（用于归一化证据落盘）。
func writeJVal(b *strings.Builder, v *jval) {
	if v == nil {
		b.WriteString("null")
		return
	}
	switch v.kind {
	case 'o':
		b.WriteByte('{')
		for i, k := range v.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(strconv.Quote(k))
			b.WriteByte(':')
			writeJVal(b, v.obj[k])
		}
		b.WriteByte('}')
	case 'a':
		b.WriteByte('[')
		for i, it := range v.arr {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJVal(b, it)
		}
		b.WriteByte(']')
	case 's':
		b.WriteString(strconv.Quote(v.str))
	case 'n':
		b.WriteString(v.num)
	case 'b':
		if v.boolean {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	default:
		b.WriteString("null")
	}
}

// dumpJ 输出紧凑 JSON 文本。
func dumpJ(v *jval) string {
	var b strings.Builder
	writeJVal(&b, v)
	return b.String()
}

// dumpIndent 输出 2 空格缩进 JSON（证据文件可读版）。
func dumpIndent(v *jval) string {
	var b strings.Builder
	writeIndent(&b, v, 0)
	return b.String()
}

// writeIndent 缩进写值。
func writeIndent(b *strings.Builder, v *jval, depth int) {
	pad := func(n int) { b.WriteString(strings.Repeat("  ", n)) }
	if v == nil {
		b.WriteString("null")
		return
	}
	switch v.kind {
	case 'o':
		if len(v.keys) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		for i, k := range v.keys {
			pad(depth + 1)
			b.WriteString(strconv.Quote(k))
			b.WriteString(": ")
			writeIndent(b, v.obj[k], depth+1)
			if i < len(v.keys)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		pad(depth)
		b.WriteByte('}')
	case 'a':
		if len(v.arr) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[\n")
		for i, it := range v.arr {
			pad(depth + 1)
			writeIndent(b, it, depth+1)
			if i < len(v.arr)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		pad(depth)
		b.WriteByte(']')
	default:
		writeJVal(b, v)
	}
}

// jsonEqual 深比较（对象比键序；数字按数值比）。
func jsonEqual(a, b *jval) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case 'o':
		if len(a.keys) != len(b.keys) {
			return false
		}
		for i, k := range a.keys {
			if b.keys[i] != k {
				return false
			}
			if !jsonEqual(a.obj[k], b.obj[k]) {
				return false
			}
		}
		return true
	case 'a':
		if len(a.arr) != len(b.arr) {
			return false
		}
		for i := range a.arr {
			if !jsonEqual(a.arr[i], b.arr[i]) {
				return false
			}
		}
		return true
	case 's':
		return a.str == b.str
	case 'n':
		af, aerr := strconv.ParseFloat(a.num, 64)
		bf, berr := strconv.ParseFloat(b.num, 64)
		if aerr == nil && berr == nil {
			return af == bf
		}
		return a.num == b.num
	case 'b':
		return a.boolean == b.boolean
	default:
		return true
	}
}

// firstDiff 返回首个差异的路径与取值描述（无差异返回空串）。
func firstDiff(a, b *jval, path string) string {
	if a == nil || b == nil {
		if a == nil && b == nil {
			return ""
		}
		return path + ": " + kindOf(a) + " != " + kindOf(b)
	}
	if a.kind != b.kind {
		return path + ": 类型 " + a.kindName() + " != " + b.kindName()
	}
	switch a.kind {
	case 'o':
		if len(a.keys) != len(b.keys) {
			return path + ": 字段数 " + strconv.Itoa(len(a.keys)) + " != " + strconv.Itoa(len(b.keys))
		}
		for i, k := range a.keys {
			if b.keys[i] != k {
				return path + ": 键序 " + k + " != " + b.keys[i]
			}
			if d := firstDiff(a.obj[k], b.obj[k], path+"."+k); d != "" {
				return d
			}
		}
		return ""
	case 'a':
		if len(a.arr) != len(b.arr) {
			return path + ": 元素数 " + strconv.Itoa(len(a.arr)) + " != " + strconv.Itoa(len(b.arr))
		}
		for i := range a.arr {
			if d := firstDiff(a.arr[i], b.arr[i], path+"["+strconv.Itoa(i)+"]"); d != "" {
				return d
			}
		}
		return ""
	case 's':
		if a.str != b.str {
			return path + ": " + clip(a.str) + " != " + clip(b.str)
		}
		return ""
	case 'n':
		if !jsonEqual(a, b) {
			return path + ": " + a.num + " != " + b.num
		}
		return ""
	case 'b':
		if a.boolean != b.boolean {
			return path + ": " + strconv.FormatBool(a.boolean) + " != " + strconv.FormatBool(b.boolean)
		}
		return ""
	default:
		return ""
	}
}

// kindOf 描述节点（差异文案用）。
func kindOf(v *jval) string {
	if v == nil {
		return "missing"
	}
	if v.kind == 's' {
		return strconv.Quote(clip(v.str))
	}
	return v.kindName() + "(" + dumpJ(v) + ")"
}

// clip 截断长文本（差异文案用）。
func clip(s string) string {
	r := []rune(s)
	if len(r) > 80 {
		return string(r[:80]) + "…"
	}
	return s
}

// timePattern 是日志时间戳格式（yyyy-MM-dd HH:mm:ss）。
var timePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`)

// normalizeJ 归一化动态值（返回副本；path 记录字段路径，数组元素记 "[]"）。
func normalizeJ(v *jval, path []string) *jval {
	if v == nil {
		return nil
	}
	switch v.kind {
	case 'o':
		out := &jval{kind: 'o', obj: make(map[string]*jval, len(v.keys))}
		for _, k := range v.keys {
			out.keys = append(out.keys, k)
			out.obj[k] = normalizeJ(v.obj[k], appendPath(path, k))
		}
		return out
	case 'a':
		out := &jval{kind: 'a', arr: make([]*jval, 0, len(v.arr))}
		for _, it := range v.arr {
			out.arr = append(out.arr, normalizeJ(it, appendPath(path, "[]")))
		}
		return out
	}
	last := ""
	if len(path) > 0 {
		last = path[len(path)-1]
	}
	switch {
	case last == "pid" || last == "frame" || last == "probedAt" || last == "lastActiveAt":
		return numZero()
	case last == "module" && (v.str == "CatHome4" || v.str == "CatHome5"):
		return &jval{kind: 's', str: "CatHome*"}
	case last == "time" && timePattern.MatchString(v.str):
		return &jval{kind: 's', str: "<time>"}
	case pathIs(path, "res", "t"):
		return numZero()
	case pathHasPrefix(path, "res", "proc") && v.kind == 'n':
		return numZero()
	case pathHasPrefix(path, "res", "py") && v.kind == 'n':
		return numZero()
	case pathHasPrefix(path, "res", "node") && v.kind == 'n':
		return numZero()
	case pathHasPrefix(path, "res", "gpu") && v.kind == 'n':
		return numZero()
	case pathHasPrefix(path, "res", "loop") && v.kind == 'n':
		return numZero()
	case pathIs(path, "boot", "sink"):
		return &jval{kind: 's', str: "<sink>"}
	case pathIs(path, "boot", "reportPath"):
		return &jval{kind: 's', str: "<reportPath>"}
	case pathHasPrefix(path, "boot", "degraded") && last == "at":
		return &jval{kind: 's', str: "<at>"}
	}
	return v
}

// numZero 造一个数值 0。
func numZero() *jval { return &jval{kind: 'n', num: "0"} }

// appendPath 复制路径并追加一段。
func appendPath(path []string, seg string) []string {
	out := make([]string, len(path)+1)
	copy(out, path)
	out[len(path)] = seg
	return out
}

// pathIs 判定路径精确等于给定段序列。
func pathIs(path []string, segs ...string) bool {
	if len(path) != len(segs) {
		return false
	}
	for i := range segs {
		if path[i] != segs[i] {
			return false
		}
	}
	return true
}

// pathHasPrefix 判定路径前缀。
func pathHasPrefix(path []string, segs ...string) bool {
	if len(path) < len(segs) {
		return false
	}
	for i := range segs {
		if path[i] != segs[i] {
			return false
		}
	}
	return true
}

// objectKeys 取对象键序（非对象给 nil）。
func objectKeys(v *jval) []string {
	if v == nil || v.kind != 'o' {
		return nil
	}
	return v.keys
}

// sameKeys 判定两组键完全一致（含顺序）。
func sameKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// keysText 键清单文本（报告用）。
func keysText(keys []string) string { return strings.Join(keys, ",") }
