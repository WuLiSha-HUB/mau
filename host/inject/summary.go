package inject

import (
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// 本文件复现项目一句话摘要——对齐 mau:CatHome4/CatHome4.Core/ProjectSummary.cs。
//
// 提取优先级：① 文件头 `> 定位：` ② 文件头 `> 类型：` ③「## …目的…」节首段首句；三路皆空 → 空串
// （前端不显示描述，不编造）。两者皆按 ` / ` 截首字段、去 `**` 粗体、截 MaxChars 字。

// ProjectSummaryMaxChars 摘要最大字数——超出截断并补省略号。
const ProjectSummaryMaxChars = 90

// projectSummaryTailFields 档案头一行里的后续字段名——用于识别 `/ 版本：` 这类字段边界
// （正文里的斜杠不误切）。
var projectSummaryTailFields = []string{
	"版本", "主人格", "加载 Job", "路径", "建立", "创建", "缘起", "项目依赖", "L3 引用",
	"仓库入口", "收工", "数据根", "本支主控", "上游基线", "类型",
}

// ProjectSummaryOf 取项目一句话摘要——读 `ccbp:Project/<project>/project.md` 并抽取。
//
// 边界：只读；文件缺失静默回空（同 C# File.Exists 分支），读取失败出声（静默回空会让
// 「档案没写」与「读失败」不可区分）。
func ProjectSummaryOf(resolve Resolver, project string, logf Logf) string {
	if project == "" {
		return ""
	}
	if resolve == nil {
		return ""
	}
	root, err := resolve("ccbp:Project")
	if err != nil || root == "" {
		return ""
	}
	path := filepath.Join(root, project, "project.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logf.log(2, "项目摘要读取失败："+project+"（"+err.Error()+"）", "INJECT")
		}
		return ""
	}
	return ExtractProjectSummary(strings.Split(string(normalizeSummaryText(raw)), "\n"))
}

// normalizeSummaryText 去 UTF-8 BOM 并统一换行（对齐 C# File.ReadAllLines 的行切分）。
func normalizeSummaryText(raw []byte) string {
	text := string(raw)
	text = strings.TrimPrefix(text, "\uFEFF")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

// ExtractProjectSummary 纯解析——project.md 全文行 → 一句话摘要（单测入口，不碰文件系统）。
func ExtractProjectSummary(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	// 文档头 = 第一个 `---` 之前（档案头的 `> 定位：` / `> 类型：` 都在这段里）
	headEnd := len(lines)
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			headEnd = i
			break
		}
	}
	if located := headerField(lines, headEnd, "定位"); located != "" {
		return located
	}
	if typed := headerField(lines, headEnd, "类型"); typed != "" {
		return typed
	}
	return purposeSentence(lines)
}

// isTailFieldAt 位置处是否为「后续字段名」——跳过空格后逐项前缀比对。
func isTailFieldAt(value string, start int) bool {
	runes := []rune(value)
	i := start
	for i < len(runes) && runes[i] == ' ' {
		i++
	}
	for _, tail := range projectSummaryTailFields {
		tailRunes := []rune(tail)
		if i+len(tailRunes) > len(runes) {
			continue
		}
		match := true
		for k, r := range tailRunes {
			if runes[i+k] != r {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// fieldBoundary 字段边界——返回首个边界位置（rune 下标，按 UTF-16 语义近似）；无边界 = -1。
//
// 边界两类：`·`，或 ` / 版本：` 这类 `/ + 已知字段名`。括号内的分隔符不算边界。
func fieldBoundary(value string) int {
	runes := []rune(value)
	depth := 0
	for i, r := range runes {
		switch r {
		case '（', '(':
			depth++
			continue
		case '）', ')':
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth > 0 {
			continue
		}
		if r == '·' {
			return i
		}
		if r == '/' && isTailFieldAt(value, i+1) {
			return i
		}
	}
	return -1
}

// headerField 档案头字段抽取——`> 定位：…` / `> 类型：…`，按字段边界截掉后续字段。
func headerField(lines []string, end int, field string) string {
	tag := field + "："
	tagHalf := field + ":"
	for i := 0; i < end && i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, ">") {
			continue
		}
		at := strings.Index(line, tag)
		tagLen := len(tag)
		if at < 0 {
			at = strings.Index(line, tagHalf)
			tagLen = len(tagHalf)
		}
		if at < 0 {
			continue
		}
		value := cleanSummary(line[at+tagLen:])
		if cut := fieldBoundary(value); cut > 0 {
			value = string([]rune(value)[:cut])
		}
		value = strings.TrimSpace(firstSentence(value))
		clamped := clampSummary(value)
		// 破折号只作兜底：整句超长时退到「定性句——具体做法」的前半句
		if clamped != value {
			if dash := strings.Index(value, "——"); dash >= 0 {
				if utf8.RuneCountInString(value[:dash]) >= 8 {
					return clampSummary(strings.TrimSpace(value[:dash]))
				}
			}
		}
		return clamped
	}
	return ""
}

// purposeSentence 「## …目的…」节首段首句——跳过表格 / 列表 / 引用 / 代码块行，过短句继续往下找。
func purposeSentence(lines []string) string {
	head := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") && strings.Contains(trimmed, "目的") {
			head = i
			break
		}
	}
	if head < 0 {
		return ""
	}
	for i := head + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, ">") || strings.HasPrefix(trimmed, "|") ||
			strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "- ") ||
			strings.HasPrefix(trimmed, "* ") {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			break
		}
		sentence := clampSummary(firstSentence(cleanSummary(trimmed)))
		if utf8.RuneCountInString(sentence) >= 6 {
			return sentence
		}
	}
	return ""
}

// firstSentence 取首句——遇中文句号 / 冒号 / 分号即截断（括号内的句读不算边界）。
func firstSentence(text string) string {
	depth := 0
	for i, r := range text {
		switch r {
		case '（', '(':
			depth++
			continue
		case '）', ')':
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth > 0 {
			continue
		}
		if r == '。' || r == '：' || r == '；' {
			return text[:i]
		}
	}
	return text
}

// cleanSummary 清洗——去 Markdown 粗体标记与首尾空白。
func cleanSummary(text string) string {
	return strings.TrimSpace(strings.ReplaceAll(text, "**", ""))
}

// clampSummary 截断到 MaxChars——超出补省略号；截断点落在未闭合括号里时回退到左括号前。
//
// 字数口径按 rune 计（BMP 内与 C# string.Length 一致；项目档案为中文，不涉代理对）。
func clampSummary(text string) string {
	runes := []rune(text)
	if len(runes) <= ProjectSummaryMaxChars {
		return text
	}
	cut := ProjectSummaryMaxChars
	open := lastIndexRune(runes[:cut], '（')
	if half := lastIndexRune(runes[:cut], '('); half > open {
		open = half
	}
	closePos := lastIndexRune(runes[:cut], '）')
	if half := lastIndexRune(runes[:cut], ')'); half > closePos {
		closePos = half
	}
	if open > closePos {
		cut = open
	}
	return string(runes[:cut]) + "…"
}

// lastIndexRune 在 rune 切片内从后向前找字符——返回下标，未命中 = -1。
func lastIndexRune(runes []rune, target rune) int {
	for i := len(runes) - 1; i >= 0; i-- {
		if runes[i] == target {
			return i
		}
	}
	return -1
}
