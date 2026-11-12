package fs

import "testing"

// TestMatchFilePatternWin32 文件名匹配——Win32 语义（基点分名 / 扩展两段；尾 * 吸收扩展名；尾 ? 可匹配零字符）。
func TestMatchFilePatternWin32(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"*", "a.md", true},
		{"*", "noext", true},
		{"*.*", "a.md", true},
		{"*.*", "noext", true},
		{"*.md", "a.md", true},
		{"*.MD", "a.md", true},
		{"*.TXT", "a.txt", true},
		{"?", "a", true},
		{"?", "z", true},
		{"?", "a.md", false},
		{"?.md", "a.md", true},
		{"a?md", "a.md", false},
		{"noext?", "noext", true},
		{"*.", "noext", true},
		{"*.", "a.md", false},
		{"a*", "a.md", true},
		{"n*", "noext", true},
		{"a.md?", "a.md", true},
	}
	for _, c := range cases {
		if got := matchFilePattern(c.pattern, c.name); got != c.want {
			t.Fatalf("matchFilePattern(%q, %q) = %v，期望 %v", c.pattern, c.name, got, c.want)
		}
	}
}
