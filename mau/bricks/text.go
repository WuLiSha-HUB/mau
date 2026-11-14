package bricks

import (
	"strconv"
	"strings"

	"cathome5/mau/fs"
)

// badParse 是参数 JSON 解析失败的统一文案（与 C# 逐字一致）。
const badParse = "ERR|BAD_ARGS|工具参数 JSON 解析失败（LLM 生成参数可能被截断——超长内容请分段写入）"

// TextRead 复现 text-read——自动编码读取，20K 截断。
func TextRead(svc *fs.Service, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "path", "path", "", ""); bad != "" {
		return bad, false
	}
	path := Get(argsJSON, "path")
	if path == ParseFail {
		return badParse, false
	}
	if path == "" {
		return "ERR|BAD_ARGS|缺少参数 path", false
	}
	content, err := svc.ReadTextAuto(path)
	if err != nil {
		return fs.ErrText(err), false
	}
	lines := countLines(content)
	return MetaHead("text-read", true, path, lines) + "\n" + path + " | " + strconv.Itoa(lines) + " 行" + "\n" + trimResult(content, 20000), true
}

// TextWrite 复现 text-write——自动编码覆写。
func TextWrite(svc *fs.Service, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "path content", "path content", "", ""); bad != "" {
		return bad, false
	}
	path := Get(argsJSON, "path")
	content := Get(argsJSON, "content")
	if path == ParseFail || content == ParseFail {
		return badParse, false
	}
	if path == "" {
		return "ERR|BAD_ARGS|缺少参数 path", false
	}
	style, err := svc.WriteTextAuto(path, content)
	if err != nil {
		return fs.ErrText(err), false
	}
	lines := countLines(content)
	return MetaHead("text-write", true, path, lines) + "\n" + path + " | 已覆写 · " + strconv.Itoa(lines) + " 行 · " + style, true
}

// TextAppend 复现 text-append——自动编码追加。
func TextAppend(svc *fs.Service, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "path content", "path content", "", ""); bad != "" {
		return bad, false
	}
	path := Get(argsJSON, "path")
	content := Get(argsJSON, "content")
	if path == ParseFail || content == ParseFail {
		return badParse, false
	}
	if path == "" {
		return "ERR|BAD_ARGS|缺少参数 path", false
	}
	style, err := svc.AppendTextAuto(path, content)
	if err != nil {
		return fs.ErrText(err), false
	}
	lines := countLines(content)
	return MetaHead("text-append", true, path, lines) + "\n" + path + " | 已追加 · " + strconv.Itoa(lines) + " 行 · " + style, true
}

// TextReadLines 复现 text-read_lines——行号区间读取（1 起，end=0 至文件尾）。
func TextReadLines(svc *fs.Service, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "path start end", "path", "", ""); bad != "" {
		return bad, false
	}
	path := Get(argsJSON, "path")
	if path == ParseFail {
		return badParse, false
	}
	if path == "" {
		return "ERR|BAD_ARGS|缺少参数 path", false
	}
	start, end := 1, 0
	startRaw := Get(argsJSON, "start")
	endRaw := Get(argsJSON, "end")
	if startRaw != "" {
		n, err := strconv.Atoi(startRaw)
		if err != nil {
			return "ERR|BAD_ARGS|参数 start 非整数: " + startRaw, false
		}
		start = n
	}
	if endRaw != "" {
		n, err := strconv.Atoi(endRaw)
		if err != nil {
			return "ERR|BAD_ARGS|参数 end 非整数: " + endRaw, false
		}
		end = n
	}
	if start < 1 {
		return "ERR|BAD_ARGS|参数 start 必须 ≥1", false
	}
	body, err := svc.ReadLinesAuto(path, start, end)
	if err != nil {
		return fs.ErrText(err), false
	}
	lines := countLines(body)
	body = limitSingleLineJSON(body, maxSingleLineJSONChars)
	return MetaHead("text-read_lines", true, path, lines) + "\n" + path + " | " + strconv.Itoa(lines) + " 行" + "\n" + body, true
}

// TextReadBetween 复现 text-read_between——锚点区间读取（str1 空 = 文件头 / str2 空 = 文件尾）。
func TextReadBetween(svc *fs.Service, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "path str1 str2", "path", "", ""); bad != "" {
		return bad, false
	}
	path := Get(argsJSON, "path")
	if path == ParseFail {
		return badParse, false
	}
	if path == "" {
		return "ERR|BAD_ARGS|缺少参数 path", false
	}
	str1 := Get(argsJSON, "str1")
	str2 := Get(argsJSON, "str2")
	if str1 == ParseFail || str2 == ParseFail {
		return badParse, false
	}
	unified, err := svc.ReadTextFull(path)
	if err != nil {
		return fs.ErrText(err), false
	}
	a1 := strings.ReplaceAll(str1, "\r\n", "\n")
	a2 := strings.ReplaceAll(str2, "\r\n", "\n")
	start := 0
	if a1 != "" {
		idx := findOccurrences(unified, a1)
		if len(idx) == 0 {
			return "ERR|ANCHOR_NOT_FOUND|起始锚点未找到: " + a1 + escapeHint(a1), false
		}
		if len(idx) > 1 {
			return "ERR|ANCHOR_AMBIGUOUS|起始锚点多次出现: " + a1, false
		}
		start = idx[0] + len([]rune(a1))
	}
	end := len([]rune(unified))
	if a2 != "" {
		idx := findOccurrences(unified, a2)
		if len(idx) == 0 {
			return "ERR|ANCHOR_NOT_FOUND|结束锚点未找到: " + a2 + escapeHint(a2), false
		}
		if len(idx) > 1 {
			return "ERR|ANCHOR_AMBIGUOUS|结束锚点多次出现: " + a2, false
		}
		end = idx[0]
	}
	if end < start {
		return "ERR|ANCHOR_ORDER|结束锚点位于起始锚点之前", false
	}
	body := string([]rune(unified)[start:end])
	lines := countLines(body)
	body = limitSingleLineJSON(body, maxSingleLineJSONChars)
	return MetaHead("text-read_between", true, path, lines) + "\n" + path + " | " + strconv.Itoa(lines) + " 行" + "\n" + body, true
}

// countLines 行数——换行符数 + 1。
func countLines(text string) int {
	return strings.Count(text, "\n") + 1
}

// trimResult 超长截断——保留头部 + 截断提示。
func trimResult(text string, max int) string {
	r := []rune(text)
	if len(r) <= max {
		return text
	}
	return string(r[:max]) + "\n…[截断: 共 " + strconv.Itoa(len(r)) + " 字符，仅保留前 " + strconv.Itoa(max) + "]"
}

// maxSingleLineJSONChars 单行 JSON 限幅上限——与 trimResult 的 text-read 口径一致（20000 字符）。
const maxSingleLineJSONChars = 20000

// limitSingleLineJSON 单行 JSON 限幅——正文只有一行且该行是 JSON 对象/数组时，截断到 max 并加提示。
//
// 只治「单行」：多行内容（含多行 JSON）原样返回——多行能按行区间 / 锚点区间分段读，
// 单行没有分段手段：一份 5 MB 的单行 JSON，读「第 1 行」就是整份进前文。
// 形态判定取「无换行 + 首字符是 { 或 [」，不做 JSON 解析——大文件不做二次解析，片段也照样限。
// 出口：提示里给可执行路径（脚本抽字段 / 先转多行副本），不留下「只能干瞪眼」的死角。
func limitSingleLineJSON(body string, max int) string {
	line := body
	// text-read_lines 的正文带「N: 」行号前缀——剥掉再判形态（单行文件恒为 "1: "）
	if idx := strings.Index(body, ": "); idx > 0 && idx <= 4 {
		if _, err := strconv.Atoi(body[:idx]); err == nil {
			line = body[idx+2:]
		}
	}
	if line == "" || (line[0] != '{' && line[0] != '[') {
		return body
	}
	if strings.ContainsAny(line, "\r\n") {
		return body
	}
	r := []rune(body)
	if len(r) <= max {
		return body
	}
	return string(r[:max]) + "\n…[截断: 单行 JSON 共 " + strconv.Itoa(len(r)) + " 字符，仅保留前 " + strconv.Itoa(max) + "——单行无法分段读；要看全请用脚本抽字段，或先转成多行副本再按行区间读]"
}

// findOccurrences 非重叠精确子串匹配——返回 rune 起始索引。
func findOccurrences(text, target string) []int {
	var out []int
	if target == "" {
		return out
	}
	hay := []rune(text)
	needle := []rune(target)
	for i := 0; i+len(needle) <= len(hay); {
		match := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			out = append(out, i)
			i += len(needle)
			continue
		}
		i++
	}
	return out
}

// escapeHint 锚点含转义字符时的报错引导后缀。
func escapeHint(s string) string {
	if strings.ContainsAny(s, "\\\"") {
		return "｜提示：锚点按标准 JSON 语义——字面反斜杠写成两个反斜杠、字面引号写成反斜杠加引号；锚点取 text-read 的原文行。"
	}
	return ""
}
