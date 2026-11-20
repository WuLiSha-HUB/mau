package session

// 文件包裹分部——对话文件附件（任意类型）的前文形态，与 imgenvelope.go（图片包裹）**同构**。
//
// 形态：`[file-open]` + `文件<前文条数>-<批内序号>：<绝对路径>` + `[file-end]` + 空行 + 正文。
// 判据：严格门（段头 + ≥1 条目 + 段尾齐备，包裹内部无杂行）与围栏吞并规则与图片包裹一致——
// 两处解析逻辑同构，改判据时两处同改（各自有单测覆盖）。

import (
	"regexp"
	"strings"
)

// ChatFileItem 是对话文件包裹条目——编号（前文条数-批内序号）与文件绝对路径。
//
// 编号供渲染角标与模型指代（「文件92-1」）；路径供 Agent 侧 text-read / file-* 等工具直读。
type ChatFileItem struct {
	// Ref 编号文本（如 92-1）——前文条数-批内序号
	Ref string
	// Path 文件绝对路径（工具直读——不解析受控根寻址）
	Path string
}

// 文件包裹段标记——独占一行，大小写固定。
const (
	// FileOpenTag 段头
	FileOpenTag = "[file-open]"
	// FileEndTag 段尾
	FileEndTag = "[file-end]"
)

// fileItemRegex 条目行正则——文件<前文条数>-<批内序号>：<绝对路径>（全角冒号；路径取到行尾，可含空格）。
var fileItemRegex = regexp.MustCompile(`^文件(\d+)-(\d+)：(.+)$`)

// BuildFileEnvelope 组装包裹 + 正文——无有效路径时原样返回正文（零回归）。
//
// 包裹本体 = 段头 + 条目 + 段尾（每行独占）；正文以空行分隔，无正文不留多余空行。
func BuildFileEnvelope(files []string, contextCount int, body string) string {
	paths := normalizeImagePaths(files)
	if len(paths) == 0 {
		return body
	}
	var b strings.Builder
	b.WriteString(FileOpenTag)
	for i, path := range paths {
		b.WriteString("\n")
		b.WriteString(BuildFileItemLine(contextCount, i+1, path))
	}
	b.WriteString("\n")
	b.WriteString(FileEndTag)
	if body != "" {
		b.WriteString("\n\n")
		b.WriteString(body)
	}
	return b.String()
}

// ApplyFileEnvelopeToLine 指令行套用——`Chat <正文>` → `Chat <包裹+正文>`；非 Chat 行 / 无文件原样返回。
//
// 单一出口：Chat 前缀约定只在本函数处理，调用面不感知。
func ApplyFileEnvelopeToLine(line string, files []string, contextCount int) string {
	if line == "" || len(files) == 0 {
		return line
	}
	if !strings.HasPrefix(line, chatLinePrefix) {
		return line
	}
	body := line[len(chatLinePrefix):]
	return chatLinePrefix + BuildFileEnvelope(files, contextCount, body)
}

// BuildFileItemLine 条目行构造——组装与解析共用的格式唯一产出点。
func BuildFileItemLine(contextCount, index int, path string) string {
	return "文件" + itoa(contextCount) + "-" + itoa(index) + "：" + path
}

// ParseFileEnvelope 解析包裹——严格门：段头 + ≥1 条目 + 段尾齐备，包裹内部无杂行。
//
// 包裹可位于消息**任意位置**（行粒度）；前一候选不成立（缺段尾 / 内部杂行 / 零条目）继续向后找。
// 文件包裹与图片包裹同优先级——代码围栏内的段头照样判包裹，紧邻的围栏行不留在前后段。
// 不成立时：items 空、before = 原文 Trim、after = 空串（调用方按普通文本处理）。
func ParseFileEnvelope(text string) (items []ChatFileItem, before, after string, ok bool) {
	if text == "" {
		return nil, "", "", false
	}
	before = strings.TrimSpace(text)
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")
	// [段0] 围栏状态预扫——inCodeAt[f] = 第 f 行之前是否处于代码围栏内
	// 只用于「包裹是否被围栏包住」的吞并判定，不参与命中判定（文件渲染优先，围栏内照样命中）
	inCodeAt := make([]bool, len(lines))
	inCode := false
	for f := range lines {
		inCodeAt[f] = inCode
		if strings.HasPrefix(strings.TrimSpace(lines[f]), "```") {
			inCode = !inCode
		}
	}
	// [段1] 段头定位——任意位置扫描
	for s := range lines {
		if strings.TrimSpace(lines[s]) != FileOpenTag {
			continue
		}
		// [段2] 条目行——≥1；段尾之前不得出现空行或非条目行（严格门）
		found := make([]ChatFileItem, 0, 4)
		i := s + 1
		broken := false
		for i < len(lines) {
			t := strings.TrimSpace(lines[i])
			if t == FileEndTag {
				break
			}
			if t == "" {
				broken = true
				break
			}
			m := fileItemRegex.FindStringSubmatch(t)
			if m == nil {
				broken = true
				break
			}
			found = append(found, ChatFileItem{Ref: m[1] + "-" + m[2], Path: strings.TrimSpace(m[3])})
			i++
		}
		if broken || len(found) == 0 || i >= len(lines) {
			continue
		}
		// 围栏行吞并——仅当包裹被围栏包住时才吞紧邻的围栏行；包裹在围栏外时围栏行原样保留
		fenced := inCodeAt[s]
		beforeEnd := s
		if fenced && beforeEnd > 0 && strings.HasPrefix(strings.TrimSpace(lines[beforeEnd-1]), "```") {
			beforeEnd--
		}
		afterStart := i + 1
		if fenced && afterStart < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[afterStart]), "```") {
			afterStart++
		}
		return found,
			strings.TrimSpace(joinLines(lines, 0, beforeEnd)),
			strings.TrimSpace(joinLines(lines, afterStart, len(lines))),
			true
	}
	return nil, strings.TrimSpace(text), "", false
}
