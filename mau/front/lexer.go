package front

import (
	"fmt"
	"strings"
)

// Kind 是词法 Token 的种类。数值本身无语义——Go 侧自定义，不沿用 C# TokenIds。
type Kind int

const (
	// KindEOF 是流末哨兵——恒为最后一个 Token。
	KindEOF Kind = iota
	// KindName 是专有名词（'...'）或裸 @ 词。
	KindName
	// KindStr 是字符串字面量（"..."，值不含引号）。
	KindStr
	// KindNum 是数字字面量（[0-9.]+）。
	KindNum
	// KindWord 是参数区裸词（t / par / !）——括号外不产生。
	KindWord
	// KindSection 是段首标记 §。
	KindSection
	// KindDeclare 是多字符字形 :=。
	KindDeclare
	// KindEq 是 =。
	KindEq
	// KindColon 是 :。
	KindColon
	// KindIn 是 ⇐（或 ASCII 形态 <-）。
	KindIn
	// KindSample 是 ↻。
	KindSample
	// KindArrow 是 →。
	KindArrow
	// KindBranch 是 |。
	KindBranch
	// KindAnd 是 &。
	KindAnd
	// KindSetOpen 是 {。
	KindSetOpen
	// KindSetClose 是 }。
	KindSetClose
	// KindSep 是 ,。
	KindSep
	// KindParamOpen 是 [。
	KindParamOpen
	// KindParamClose 是 ]。
	KindParamClose
	// KindCapture 是 >。
	KindCapture
	// KindCmdIn 是 ⇚。
	KindCmdIn
)

// String 返回种类的调试名。
func (k Kind) String() string {
	names := [...]string{
		"EOF", "Name", "Str", "Num", "Word", "Section", "Declare", "Eq",
		"Colon", "In", "Sample", "Arrow", "Branch", "And", "SetOpen",
		"SetClose", "Sep", "ParamOpen", "ParamClose", "Capture", "CmdIn",
	}
	if int(k) < len(names) {
		return names[k]
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Token 是一个词法单元。
type Token struct {
	Kind  Kind
	Line  int
	Col   int
	Value string
}

// singleGlyph 是单字符字形表——v3-default 外观，15 项。
var singleGlyph = map[rune]Kind{
	'§': KindSection,
	'=': KindEq,
	':': KindColon,
	'⇐': KindIn,
	'↻': KindSample,
	'→': KindArrow,
	'|': KindBranch,
	'&': KindAnd,
	'{': KindSetOpen,
	'}': KindSetClose,
	',': KindSep,
	'[': KindParamOpen,
	']': KindParamClose,
	'>': KindCapture,
	'⇚': KindCmdIn,
}

// Lex 词法分析——返回 Token 流（末尾恒有 EOF）与诊断。
//
// 元素顺序：空白 → // 行注释 → '名称' → "字符串" → [参数区] → 裸数字 →
// 裸 @ 词 → 符号（多字符优先）→ 非法字符 E001。
func Lex(src string) ([]Token, []Diag) {
	// 剥 UTF-8 BOM——C# 侧由 StreamReader 自动剥离，Go 侧显式处理
	src = strings.TrimPrefix(src, "\ufeff")
	l := &lexer{src: []rune(src), line: 1, col: 1}
	l.run()
	return l.toks, l.diags
}

// lexer 是词法分析状态机。
type lexer struct {
	src   []rune
	pos   int
	line  int
	col   int
	toks  []Token
	diags []Diag
}

// run 扫描到源末尾。
func (l *lexer) run() {
	for l.pos < len(l.src) {
		r := l.src[l.pos]
		switch {
		case isSpace(r):
			l.advance()
		case r == '/' && l.peek(1) == '/':
			l.skipLineComment()
		case r == '\'':
			l.lexName()
		case r == '"':
			l.lexString()
		case r == '[':
			l.lexParam()
		case r >= '0' && r <= '9':
			l.lexNumber()
		case r == '@':
			l.lexAtWord()
		default:
			if !l.lexGlyph() {
				l.errorAt("E001", l.line, "非法字符 %s", glyphDesc(r))
				l.advance()
			}
		}
	}
	l.add(KindEOF, l.line, l.col, "")
}

// advance 消费一个 rune 并维护行列。
func (l *lexer) advance() rune {
	r := l.src[l.pos]
	l.pos++
	if r == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return r
}

// peek 看相对位置 off 的 rune（越界返回 0）。
func (l *lexer) peek(off int) rune {
	if l.pos+off >= len(l.src) {
		return 0
	}
	return l.src[l.pos+off]
}

// add 追加一个 Token。
func (l *lexer) add(k Kind, line, col int, v string) {
	l.toks = append(l.toks, Token{Kind: k, Line: line, Col: col, Value: v})
}

// errorAt 追加一条诊断。
func (l *lexer) errorAt(code string, line int, format string, args ...any) {
	l.diags = append(l.diags, Diag{Code: code, Line: line, Msg: fmt.Sprintf(format, args...)})
}

// skipLineComment 跳过 // 到行尾（不消费换行）。
func (l *lexer) skipLineComment() {
	for l.pos < len(l.src) && l.src[l.pos] != '\n' {
		l.advance()
	}
}

// lexName 读 '...' 专有名词——白名单 [A-Za-z0-9_.@]，@ 仅限首位。
func (l *lexer) lexName() {
	line, col := l.line, l.col
	l.advance() // 开引号
	start := l.pos
	for l.pos < len(l.src) {
		r := l.src[l.pos]
		if r == '\'' {
			v := string(l.src[start:l.pos])
			l.advance()
			if !validName(v) {
				l.errorAt("E002", line, "专有名词字符非法: '%s'——允许 [A-Za-z0-9_.@]，@ 仅限首位", v)
				return
			}
			l.add(KindName, line, col, v)
			return
		}
		if r == '\n' {
			break
		}
		l.advance()
	}
	l.errorAt("E002", line, "专有名词未闭合——缺少收尾 '")
}

// lexString 读 "..." 字符串——不跨行。
func (l *lexer) lexString() {
	line, col := l.line, l.col
	l.advance() // 开引号
	start := l.pos
	for l.pos < len(l.src) {
		r := l.src[l.pos]
		if r == '"' {
			v := string(l.src[start:l.pos])
			l.advance()
			l.add(KindStr, line, col, v)
			return
		}
		if r == '\n' {
			break
		}
		l.advance()
	}
	l.errorAt("E003", line, "字符串未闭合——缺少收尾 \"")
}

// lexNumber 读裸数字——[0-9.]+。
func (l *lexer) lexNumber() {
	line, col := l.line, l.col
	start := l.pos
	for l.pos < len(l.src) && isNumberRune(l.src[l.pos]) {
		l.advance()
	}
	l.add(KindNum, line, col, string(l.src[start:l.pos]))
}

// lexAtWord 读裸 @ 词——值含 @ 前缀。
func (l *lexer) lexAtWord() {
	line, col := l.line, l.col
	start := l.pos
	for l.pos < len(l.src) && isWordRune(l.src[l.pos]) {
		l.advance()
	}
	l.add(KindName, line, col, string(l.src[start:l.pos]))
}

// lexGlyph 尝试匹配符号——多字符优先（:= / <-），再单字符。
func (l *lexer) lexGlyph() bool {
	line, col := l.line, l.col
	if l.pos+1 < len(l.src) {
		switch string(l.src[l.pos : l.pos+2]) {
		case ":=":
			l.advance()
			l.advance()
			l.add(KindDeclare, line, col, ":=")
			return true
		case "<-":
			l.advance()
			l.advance()
			l.add(KindIn, line, col, "<-")
			return true
		}
	}
	r := l.src[l.pos]
	if k, ok := singleGlyph[r]; ok {
		l.advance()
		l.add(k, line, col, string(r))
		return true
	}
	return false
}

// lexParam 读参数区——[ ... ]，含独立子词法。
func (l *lexer) lexParam() {
	openLine, openCol := l.line, l.col
	l.advance() // '['
	l.add(KindParamOpen, openLine, openCol, "[")
	for l.pos < len(l.src) {
		r := l.src[l.pos]
		switch {
		case isSpace(r):
			l.advance()
		case r == ']':
			line, col := l.line, l.col
			l.advance()
			l.add(KindParamClose, line, col, "]")
			return
		case r == '!':
			line, col := l.line, l.col
			l.advance()
			l.add(KindWord, line, col, "!")
		case r == '=':
			line, col := l.line, l.col
			l.advance()
			l.add(KindEq, line, col, "=")
		case r == ',':
			line, col := l.line, l.col
			l.advance()
			l.add(KindSep, line, col, ",")
		case r == '"':
			l.lexString()
		case isWordRune(r):
			l.lexParamWord()
		default:
			l.errorAt("E001", l.line, "参数区非法字符 %s", glyphDesc(r))
			l.advance()
		}
	}
	l.errorAt("E004", openLine, "参数区未闭合——缺少 ]")
}

// lexParamWord 读参数区词——连读词字符，全为 [0-9.] 判 Num，否则 Word。
func (l *lexer) lexParamWord() {
	line, col := l.line, l.col
	start := l.pos
	for l.pos < len(l.src) && isWordRune(l.src[l.pos]) {
		l.advance()
	}
	v := string(l.src[start:l.pos])
	if isNumberText(v) {
		l.add(KindNum, line, col, v)
		return
	}
	l.add(KindWord, line, col, v)
}

// isSpace 报告是否空白 rune（含换行）。
func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\r' || r == '\n'
}

// isWordRune 报告是否词字符——[A-Za-z0-9_.@]。
func isWordRune(r rune) bool {
	return r >= 'a' && r <= 'z' ||
		r >= 'A' && r <= 'Z' ||
		r >= '0' && r <= '9' ||
		r == '_' || r == '.' || r == '@'
}

// isNumberRune 报告是否数字字符——[0-9.]。
func isNumberRune(r rune) bool {
	return r >= '0' && r <= '9' || r == '.'
}

// isNumberText 报告整串是否只由 [0-9.] 组成。
func isNumberText(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !isNumberRune(r) {
			return false
		}
	}
	return true
}

// validName 校验专有名词——@ 仅限首位。
func validName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '@' && i == 0 {
			continue
		}
		if !isWordRune(r) || r == '@' {
			return false
		}
	}
	return true
}

// glyphDesc 把 rune 描述为诊断文本——控制字符走 U+XXXX。
func glyphDesc(r rune) string {
	if r < 0x20 || r == 0x7f {
		return fmt.Sprintf("U+%04X", r)
	}
	return fmt.Sprintf("'%c'", r)
}
