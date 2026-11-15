package session

import (
	"strings"
	"testing"
)

// TestLimitToolResult 入前文限幅——短文本原样 / 超限截断并明示 / 截断点落在字符边界。
func TestLimitToolResult(t *testing.T) {
	if got := limitToolResult("短结果"); got != "短结果" {
		t.Fatalf("短结果不应改: %q", got)
	}
	big := strings.Repeat("字", maxToolResultChars+1)
	got := limitToolResult(big)
	idx := strings.LastIndex(got, "\n…[截断")
	if idx < 0 {
		t.Fatalf("截断提示缺失")
	}
	if !strings.Contains(got[idx:], "工具结果共 200001 字符，入前文保留前 200000 字符") {
		t.Fatalf("截断提示数字不符: %q", got[idx:])
	}
	if utf16Len(got[:idx]) != maxToolResultChars {
		t.Fatalf("截断保留长度不符: %d", utf16Len(got[:idx]))
	}
}

// TestLimitPackContent 单件限幅——短件原样 / 超限截断并明示。
func TestLimitPackContent(t *testing.T) {
	if got := limitPackContent("短件"); got != "短件" {
		t.Fatalf("短件不应改: %q", got)
	}
	big := strings.Repeat("x", maxPackFileChars+10)
	got := limitPackContent(big)
	idx := strings.LastIndex(got, "\n…[截断")
	if idx < 0 {
		t.Fatalf("单件截断提示缺失")
	}
	if !strings.Contains(got[idx:], "本件共 50010 字符，已注入前 50000 字符") {
		t.Fatalf("单件截断提示数字不符: %q", got[idx:])
	}
	if len(got[:idx]) != maxPackFileChars {
		t.Fatalf("单件保留长度不符: %d", len(got[:idx]))
	}
}
