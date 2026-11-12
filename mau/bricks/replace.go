package bricks

import (
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/dlclark/regexp2"

	"cathome5/mau/fs"
)

// replaceMode 匹配原语——对齐 C# TextMatchMode：exact / ignore_case 为字面匹配，regex 走 .NET 正则语义。
type replaceMode int

const (
	replaceExact replaceMode = iota
	replaceIgnoreCase
	replaceRegex
)

const (
	// replaceSpanWarnLines 是 regex 单次匹配跨度的行数出声阈值。
	replaceSpanWarnLines = 10
	// replaceSpanWarnChars 是 regex 单次匹配跨度的字符数出声阈值。
	replaceSpanWarnChars = 1000
	// replaceDiffWindow 差异窗口切片长度——期望 / 实际各取差异点前后 ≤ N 字符。
	replaceDiffWindow = 10
	// replaceOldScopeChars 原文摘要首行截断长度。
	replaceOldScopeChars = 60
	// replaceRegexTimeout 正则超时——对齐 C# TextAlgorithms 的 TimeSpan.FromSeconds(5)。
	replaceRegexTimeout = 5 * time.Second
)

// replaceEntityTokens 实体形态标记——锚点未命中时判定是否提示实体写法。
var replaceEntityTokens = []string{"&lt;", "&gt;", "&amp;", "&quot;"}

// replaceSpan 命中跨度——rune 起始索引 + rune 长度（BMP 文本上与 C# UTF-16 索引一致）。
type replaceSpan struct {
	Index  int
	Length int
}

// TextReplace 复现 C# BRIK-TEXT-004 text-replace——exact / ignore_case 唯一锚点替换，
// all / regex 全部匹配替换并原子写回；NotFound 带差异字节定位，Ambiguous 带候选行；
// new 传删除标记「黑暗剑+22」按空文本落盘（等价删除，四模式通用），空 new 一律拒绝；
// 成功回执附自证四件（原文摘要 / 行数差 / 结构行计数 / 窗口范围）。
//
// 正则面用 regexp2（.NET 语义：Unicode \w / 环视 / 相同替换展开与零长匹配枚举），
// 编译期异常名对齐 RegexParseException，匹配超时对齐 RegexMatchTimeoutException。
func TextReplace(svc *fs.Service, argsJSON string) (string, bool) {
	// [参数面] 声明面口径零容忍——未知 / 缺值 / 非法 mode 一律 ERR|BAD_ARGS（catId 保留键放行）
	if bad := Validate(argsJSON, "path old new mode", "path old new", "mode", "exact|ignore_case|all|regex"); bad != "" {
		return bad, false
	}
	path := Get(argsJSON, "path")
	oldText := Get(argsJSON, "old")
	newText := Get(argsJSON, "new")
	if path == ParseFail || oldText == ParseFail || newText == ParseFail {
		return badParse, false
	}
	if path == "" || oldText == "" {
		return "ERR|BAD_ARGS|缺少参数 path 或 old", false
	}
	// [删除语义] new 面——空值 / 缺值一律拒绝（区分显式删除与漏传）；删除标记按空文本落盘
	if newText == "" {
		return "ERR|BAD_ARGS|缺参数 new——替换文本不可为空；删除匹配文本请传删除标记「" + DeleteKey + "」", false
	}
	deleteMode := newText == DeleteKey
	if deleteMode {
		newText = ""
	}
	mode := Get(argsJSON, "mode")
	if mode == "" {
		mode = "exact"
	}
	matchMode := replaceExact
	switch mode {
	case "ignore_case":
		matchMode = replaceIgnoreCase
	case "regex":
		matchMode = replaceRegex
	}
	if svc == nil {
		return "ERR|FS_NO_SERVICE|宿主未注入 FileSystemService", false
	}
	// [IO] 读取（编码内建——去 BOM）→ 换行归一供匹配
	content, err := svc.ReadTextAuto(path)
	if err != nil {
		return fs.ErrText(err), false
	}
	unified := strings.ReplaceAll(content, "\r\n", "\n")
	target := strings.ReplaceAll(oldText, "\r\n", "\n")
	// [匹配] 算法面——命中跨度（索引 + 长度）；正则编译失败对齐 RegexParseException
	matcher, matchErr := newReplaceMatcher(target, matchMode)
	if matchErr != nil {
		return "ERR|RegexParseException|" + matchErr.Error(), false
	}
	spans, spanErr := matcher.find(unified)
	if spanErr != nil {
		return "ERR|RegexMatchTimeoutException|" + spanErr.Error(), false
	}
	if len(spans) == 0 {
		// [未命中] 差异点定位 + 文案（本积木）
		diffIndex, common := locateBestMatch(unified, target)
		expected := sliceRunes(target, common, replaceDiffWindow)
		actual := sliceRunes(unified, diffIndex, replaceDiffWindow)
		result := "ERR|ANCHOR_NOT_FOUND|第 " + strconv.Itoa(diffIndex) + " 字节 期望「" + expected + "」实际「" + actual + "」"
		if containsAny(target, replaceEntityTokens) || containsAny(actual, replaceEntityTokens) {
			result = result + "｜提示：疑似实体写法——锚点直接写裸符号 < > &；若文件本身是实体文本，锚点写双写形态 &amp;lt;"
		}
		if hasEscapeChar(target) || hasEscapeChar(actual) {
			result = result + "｜提示：锚点按标准 JSON 语义——字面反斜杠写成两个反斜杠、字面引号写成反斜杠加引号；锚点取 text-read 的原文行，勿把源码里的字符串结束引号当作已转义引号。"
		}
		return result, false
	}
	if mode != "regex" && mode != "all" && len(spans) > 1 {
		return "ERR|ANCHOR_AMBIGUOUS|锚点出现 " + strconv.Itoa(len(spans)) + " 次，候选行: " + strings.Join(lineNumbersOf(unified, spans), ", "), false
	}
	// [替换] 算法面——全部匹配替换（regex 支持捕获组展开）
	replaced, replaceErr := matcher.replaceAll(unified, newText, spans)
	if replaceErr != nil {
		return "ERR|RegexMatchTimeoutException|" + replaceErr.Error(), false
	}
	// [IO] 写回（既有文件保真——BOM / 换行）
	if _, err := svc.WriteTextAuto(path, replaced); err != nil {
		return fs.ErrText(err), false
	}
	deleteNote := ""
	if deleteMode {
		deleteNote = "——本次 New 为空（删除标记「" + DeleteKey + "」）：删除匹配的 Old 串"
	}
	spanNote := ""
	if mode == "regex" {
		maxChars := 0
		maxIndex := 0
		totalChars := 0
		for _, sp := range spans {
			totalChars = totalChars + sp.Length
			if sp.Length > maxChars {
				maxChars = sp.Length
				maxIndex = sp.Index
			}
		}
		maxLines := countLinesOf(sliceRunes(unified, maxIndex, maxChars))
		spanNote = " · 匹配跨度 最长 " + strconv.Itoa(maxLines) + " 行 / " + strconv.Itoa(maxChars) + " 字符 · 命中合计 " + strconv.Itoa(totalChars) + " 字符"
		if maxLines >= replaceSpanWarnLines || maxChars >= replaceSpanWarnChars {
			spanNote = spanNote + " ⚠ 单次匹配跨度过大——请核对是否吞入相邻内容"
		}
	}
	lineNote := ""
	if mode == "all" && len(spans) > 0 {
		lines := lineNumbersOf(unified, spans)
		lineNote = " · 命中行 " + strings.Join(lines, ", ")
		if len(lines) < len(spans) {
			lineNote = lineNote + "…（前 " + strconv.Itoa(len(lines)) + " / 共 " + strconv.Itoa(len(spans)) + "）"
		}
	}
	// [自证面] 行数差 + 结构行计数 + 窗口范围——三件都在回执里出声（丢行 / 吃结构行必须当场可见）
	shiftNote := buildLineShiftNote(target, newText, deleteMode, len(spans))
	oldNote := buildOldScopeNote(target)
	structNote := buildStructMarkNote(target, newText)
	// 结构化返回体（A214）——首行 JSON 头（target/items）+ 正文摘要行 + 目标段
	snippet := snippetAround(replaced, spans[0].Index)
	result := MetaHead("text-replace", true, path, len(spans)) + "\n" + path + " | 替换 " + strconv.Itoa(len(spans)) + " 处" + spanNote + lineNote + deleteNote + shiftNote + structNote + oldNote + "\n" + snippet + buildSnippetScopeNote(replaced, snippet)
	return result, true
}

// replaceMatcher 匹配器——字面模式持 rune 锚点，正则模式持 .NET 语义引擎。
type replaceMatcher struct {
	mode   replaceMode
	needle []rune
	re     *regexp2.Regexp
}

// newReplaceMatcher 构造匹配器——regex 编译失败返回错误（调用方对齐 RegexParseException）。
func newReplaceMatcher(pattern string, mode replaceMode) (*replaceMatcher, error) {
	m := &replaceMatcher{mode: mode}
	if mode == replaceRegex {
		re, err := regexp2.Compile(pattern, regexp2.None)
		if err != nil {
			return nil, err
		}
		re.MatchTimeout = replaceRegexTimeout
		m.re = re
		return m, nil
	}
	m.needle = []rune(pattern)
	return m, nil
}

// find 返回全部命中跨度——字面模式非重叠左到右扫描；正则模式按 .NET 枚举（含零长匹配）。
func (m *replaceMatcher) find(text string) ([]replaceSpan, error) {
	if text == "" {
		return nil, nil
	}
	if m.mode == replaceRegex {
		var spans []replaceSpan
		match, err := m.re.FindStringMatch(text)
		if err != nil {
			return nil, err
		}
		for match != nil {
			spans = append(spans, replaceSpan{Index: match.Index, Length: match.Length})
			match, err = m.re.FindNextMatch(match)
			if err != nil {
				return nil, err
			}
		}
		return spans, nil
	}
	if len(m.needle) == 0 {
		return nil, nil
	}
	hay := []rune(text)
	var spans []replaceSpan
	for i := 0; i+len(m.needle) <= len(hay); {
		matched := true
		for j := 0; j < len(m.needle); j++ {
			if m.mode == replaceIgnoreCase {
				if !runeEqualFold(hay[i+j], m.needle[j]) {
					matched = false
					break
				}
				continue
			}
			if hay[i+j] != m.needle[j] {
				matched = false
				break
			}
		}
		if matched {
			spans = append(spans, replaceSpan{Index: i, Length: len(m.needle)})
			i += len(m.needle)
			continue
		}
		i++
	}
	return spans, nil
}

// replaceAll 全部替换——字面模式按跨度拼接；正则模式走 .NET 替换展开（$1 / ${name} / $& / $$）。
func (m *replaceMatcher) replaceAll(text, replacement string, spans []replaceSpan) (string, error) {
	if text == "" {
		return text, nil
	}
	if m.mode == replaceRegex {
		return m.re.Replace(text, replacement, -1, -1)
	}
	if len(spans) == 0 {
		return text, nil
	}
	hay := []rune(text)
	var b strings.Builder
	last := 0
	for _, sp := range spans {
		b.WriteString(string(hay[last:sp.Index]))
		b.WriteString(replacement)
		last = sp.Index + sp.Length
	}
	b.WriteString(string(hay[last:]))
	return b.String(), nil
}

// runeEqualFold 单字符简单大小写折叠等价——对应 OrdinalIgnoreCase 的逐字符比较。
func runeEqualFold(a, b rune) bool {
	if a == b {
		return true
	}
	for f := unicode.SimpleFold(a); f != a; f = unicode.SimpleFold(f) {
		if f == b {
			return true
		}
	}
	return false
}

// locateBestMatch 差异点定位——取与目标串最长公共前缀的最佳对齐位（未命中时给出定位事实）。
func locateBestMatch(text, target string) (int, int) {
	hay := []rune(text)
	needle := []rune(target)
	if len(hay) == 0 || len(needle) == 0 {
		return 0, 0
	}
	first := needle[0]
	bestPos := 0
	bestCommon := -1
	scan := indexRuneFrom(hay, first, 0)
	for scan >= 0 {
		common := 0
		for common < len(needle) && scan+common < len(hay) && hay[scan+common] == needle[common] {
			common++
		}
		if common > bestCommon {
			bestCommon = common
			bestPos = scan
		}
		if common >= len(needle) {
			break
		}
		scan = indexRuneFrom(hay, first, scan+1)
	}
	if bestCommon < 0 {
		bestCommon = 0
	}
	return bestPos + bestCommon, bestCommon
}

// indexRuneFrom 从下标 from 起查找字符 r 的首次出现。
func indexRuneFrom(hay []rune, r rune, from int) int {
	for i := from; i < len(hay); i++ {
		if hay[i] == r {
			return i
		}
	}
	return -1
}

// sliceRunes 按 rune 下标截取（越界截断）。
func sliceRunes(text string, start, count int) string {
	if count <= 0 {
		return ""
	}
	r := []rune(text)
	if start < 0 {
		start = 0
	}
	if start >= len(r) {
		return ""
	}
	take := count
	if take > len(r)-start {
		take = len(r) - start
	}
	return string(r[start : start+take])
}

// lineNumbersOf 命中跨度 → 行号（1 起，前 5 个——Ambiguous / all 候选）。
func lineNumbersOf(content string, spans []replaceSpan) []string {
	r := []rune(content)
	var lines []string
	for i := 0; i < len(spans) && len(lines) < 5; i++ {
		line := 1
		limit := spans[i].Index
		if limit > len(r) {
			limit = len(r)
		}
		for j := 0; j < limit; j++ {
			if r[j] == '\n' {
				line++
			}
		}
		lines = append(lines, strconv.Itoa(line))
	}
	return lines
}

// snippetAround 替换点附近窗口摘要——本行起向后 ≤3 行（与 C# 同实现：起点固定在本行首）。
func snippetAround(content string, index int) string {
	r := []rune(content)
	n := len(r)
	if index < 0 {
		index = 0
	}
	if index > n {
		index = n
	}
	lineStart := index
	lineEnd := index
	for lineStart > 0 && r[lineStart-1] != '\n' {
		lineStart--
	}
	for lineEnd < n && r[lineEnd] != '\n' {
		lineEnd++
	}
	start := lineStart
	for i := 0; i < 3 && start > 0; i++ {
		prev := lastIndexRune(r, '\n', start-1)
		if prev < 0 {
			start = 0
			break
		}
		start = prev + 1
	}
	end := lineEnd
	for i := 0; i < 3 && end < n; i++ {
		next := indexRuneFrom(r, '\n', end)
		if next < 0 {
			end = n
			break
		}
		end = next + 1
	}
	return string(r[start:end])
}

// lastIndexRune 在 hay[0..to] 内反向查找字符 r。
func lastIndexRune(hay []rune, r rune, to int) int {
	if to >= len(hay) {
		to = len(hay) - 1
	}
	for i := to; i >= 0; i-- {
		if hay[i] == r {
			return i
		}
	}
	return -1
}

// buildLineShiftNote 行数差自证——净删行必须出声；删除标记的显式删除只标注。
func buildLineShiftNote(oldText, newText string, deleteMode bool, matchCount int) string {
	before := countLinesOf(oldText)
	after := countLinesOf(newText)
	if before == after {
		return ""
	}
	scope := " · 行 "
	if matchCount > 1 {
		scope = " · 每次替换 行 "
	}
	note := scope + strconv.Itoa(before) + "→" + strconv.Itoa(after)
	if deleteMode {
		return note + "（删除标记·显式删除）"
	}
	if after < before {
		return note + " ⚠ 净删 " + strconv.Itoa(before-after) + " 行——锚点吃掉的每一行都要在 new 里交代"
	}
	return note
}

// countStructMarks 结构标记计数——表行 / 表格分隔行 / 标题 / 分节线（分隔行优先于表行判定）。
func countStructMarks(text string) [4]int {
	var marks [4]int
	if text == "" {
		return marks
	}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "|:--") || strings.HasPrefix(line, "|-") {
			marks[1]++
		} else if line[0] == '|' {
			marks[0]++
		}
		if strings.HasPrefix(line, "##") {
			marks[2]++
		}
		if line == "---" || line == "***" {
			marks[3]++
		}
	}
	return marks
}

// buildStructMarkNote 结构行守护——锚点内结构标记在 new 中减少即出声（告警不拒绝）。
func buildStructMarkNote(oldText, newText string) string {
	before := countStructMarks(oldText)
	after := countStructMarks(newText)
	names := [4]string{"表行", "表格分隔行", "标题", "分节线"}
	var parts []string
	for i := 0; i < len(before); i++ {
		if after[i] < before[i] {
			parts = append(parts, names[i]+" "+strconv.Itoa(before[i])+"→"+strconv.Itoa(after[i]))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " · 结构行 " + strings.Join(parts, " / ") + " ⚠ 锚点吃掉结构行而未重建——请核对 new 是否漏抄（表格需分隔行、章节需标题）"
}

// buildOldScopeNote 原文摘要——行数 + 首行（首行超 60 字符截断）。
func buildOldScopeNote(oldText string) string {
	first := firstLineOf(oldText)
	if r := []rune(first); len(r) > replaceOldScopeChars {
		first = string(r[:replaceOldScopeChars]) + "…"
	}
	return " · 原文 " + strconv.Itoa(countLinesOf(oldText)) + " 行 ｜ 首行：" + first
}

// firstLineOf 取首行——无换行则整串；空串返回空串。
func firstLineOf(text string) string {
	cut := strings.Index(text, "\n")
	if cut < 0 {
		return text
	}
	return text[:cut]
}

// buildSnippetScopeNote 窗口自证——窗口只显示本行起 ≤3 行；文件更长时显式说明。
func buildSnippetScopeNote(replaced, snippet string) string {
	total := countLinesOf(replaced)
	shown := countLinesOf(strings.TrimRight(snippet, "\n"))
	if total <= shown {
		return ""
	}
	return "…（文件共 " + strconv.Itoa(total) + " 行，此处只显示替换点前后 ≤3 行）\n"
}

// countLinesOf 行数——空串 0；否则换行数 + 1。
func countLinesOf(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

// hasEscapeChar 锚点 / 切片是否含转义字符（反斜杠或双引号——报错引导判据）。
func hasEscapeChar(text string) bool {
	return strings.ContainsAny(text, "\\\"")
}

// containsAny 是否包含任一子串（中性——tokens 由调用方给定）。
func containsAny(text string, tokens []string) bool {
	for _, token := range tokens {
		if token != "" && strings.Contains(text, token) {
			return true
		}
	}
	return false
}
