package bricks

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestFileStatFile 文件状态回执——A214 头（items=1）+ 六行正文。
func TestFileStatFile(t *testing.T) {
	svc, root := fileSvc(t)
	out, ok := FileStat(svc, `{"path":"w:a.md"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	head := `{"ok":true,"tool":"file-stat","target":"w:a.md","items":1}`
	prefix := head + "\n" + "w:a.md | 路径: " + filepath.Join(root, "a.md")
	if !strings.HasPrefix(out, prefix) {
		t.Fatalf("回执头不符\n实得: %q\n期望前缀: %q", out, prefix)
	}
	if !strings.Contains(out, "\n类型: 文件") || !strings.Contains(out, "\n大小: 1 字节") {
		t.Fatalf("正文不符: %q", out)
	}
	if !regexp.MustCompile(`\n修改时间: \d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`).MatchString(out) {
		t.Fatalf("修改时间格式不符: %q", out)
	}
	if !regexp.MustCompile(`\n创建时间: \d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`).MatchString(out) {
		t.Fatalf("创建时间格式不符: %q", out)
	}
}

// TestFileStatDirectory 目录回执——递归统计（items=FileCount）与非递归提示（省略 items）。
func TestFileStatDirectory(t *testing.T) {
	svc, root := fileSvc(t)
	out, ok := FileStat(svc, `{"path":"w:sub","recursive":"true"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	head := `{"ok":true,"tool":"file-stat","target":"w:sub","items":2}`
	if !strings.HasPrefix(out, head+"\n") {
		t.Fatalf("递归头不符: %q", out)
	}
	if !strings.Contains(out, "\n[递归] 合计 2 字节（2.00 B）") && !strings.Contains(out, "\n[递归] 合计 2 字节") {
		t.Fatalf("递归行不符: %q", out)
	}
	if !strings.Contains(out, "· 文件 2 个 · 子目录 1 个") {
		t.Fatalf("递归计数不符: %q", out)
	}
	plain, ok := FileStat(svc, `{"path":"w:sub"}`)
	if !ok {
		t.Fatalf("非递归失败: %s", plain)
	}
	if !strings.HasPrefix(plain, `{"ok":true,"tool":"file-stat","target":"w:sub"}`+"\n") {
		t.Fatalf("非递归头不符（items 应省略）: %q", plain)
	}
	if !strings.Contains(plain, "\n[提示] 目录大小须递归统计——加 recursive=true 可拿合计字节 / 文件数 / 子目录数") {
		t.Fatalf("提示行不符: %q", plain)
	}
	_ = root
}

// TestFileStatErrors 参数与错误面。
func TestFileStatErrors(t *testing.T) {
	svc, _ := fileSvc(t)
	if out, ok := FileStat(svc, `{}`); ok || out != "ERR|BAD_ARGS|缺参数 path（必填：path）" {
		t.Fatalf("缺参回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileStat(svc, `{"path":"w:a.md","recursive":"yes"}`); ok || out != "ERR|BAD_ARGS|参数 recursive 非布尔: yes" {
		t.Fatalf("布尔回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileStat(nil, `{"path":"w:a.md"}`); ok || out != "ERR|FS_NO_SERVICE|宿主未注入 FileSystemService" {
		t.Fatalf("服务缺失回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileStat(svc, `{"path":"w:nope"}`); ok || out != "ERR|FileNotFoundException|Path was not found." {
		t.Fatalf("缺失回执 = %q ok=%v", out, ok)
	}
}

// TestFileHash 哈希回执——摘要行 + 四行正文。
func TestFileHash(t *testing.T) {
	svc, root := fileSvc(t)
	if err := os.WriteFile(filepath.Join(root, "abc.txt"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok := FileHash(svc, `{"path":"w:abc.txt"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	const digest = "BA7816BF8F01CFEA414140DE5DAE2223B00361A396177A9CB410FF61F20015AD"
	prefix := `{"ok":true,"tool":"file-hash","target":"w:abc.txt"}` + "\n" + "w:abc.txt | SHA256 " + digest
	if !strings.HasPrefix(out, prefix) {
		t.Fatalf("回执头不符\n实得: %q\n期望前缀: %q", out, prefix)
	}
	if !strings.Contains(out, "\n算法: SHA256") || !strings.Contains(out, "\n大小: 3 字节") {
		t.Fatalf("正文不符: %q", out)
	}
	if !strings.Contains(out, "\n摘要: "+digest) {
		t.Fatalf("摘要行不符: %q", out)
	}
	if !regexp.MustCompile(`\n耗时: \d+ ms$`).MatchString(out) {
		t.Fatalf("耗时行不符: %q", out)
	}
}

// TestFileHashErrors 目录 / 缺失 / 参数。
func TestFileHashErrors(t *testing.T) {
	svc, _ := fileSvc(t)
	if out, ok := FileHash(svc, `{"path":"w:sub"}`); ok || out != "ERR|InvalidDataException|Path is a directory (hash requires a file)." {
		t.Fatalf("目录回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileHash(svc, `{"path":"w:nope.txt"}`); ok || out != "ERR|FileNotFoundException|File was not found." {
		t.Fatalf("缺失回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileHash(nil, `{"path":"w:a.md"}`); ok || out != "ERR|FS_NO_SERVICE|宿主未注入 FileSystemService" {
		t.Fatalf("服务缺失回执 = %q ok=%v", out, ok)
	}
}

// TestFileDiffFiles 文件模式回执——相同（items=0）/ 不同（items=1）。
func TestFileDiffFiles(t *testing.T) {
	svc, root := fileSvc(t)
	if err := os.WriteFile(filepath.Join(root, "x2.txt"), []byte("abd"), 0o644); err != nil {
		t.Fatal(err)
	}
	// a.md 内容 "a"，与自身比对 → 相同
	out, ok := FileDiff(svc, `{"left":"w:a.md","right":"w:a.md"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	prefix := `{"ok":true,"tool":"file-diff","target":"w:a.md","items":0}` + "\n" +
		"w:a.md | 相同 · " + filepath.Join(root, "a.md") + " ↔ " + filepath.Join(root, "a.md")
	if !strings.HasPrefix(out, prefix) {
		t.Fatalf("相同回执不符\n实得: %q\n期望前缀: %q", out, prefix)
	}
	if !strings.Contains(out, "\n判定: 逐字节相同") {
		t.Fatalf("判定行不符: %q", out)
	}
	// a.md（1 字节）vs x2.txt（3 字节）→ 不同
	out, ok = FileDiff(svc, `{"left":"w:a.md","right":"w:x2.txt"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	if !strings.HasPrefix(out, `{"ok":true,"tool":"file-diff","target":"w:a.md","items":1}`+"\n") {
		t.Fatalf("不同头不符: %q", out)
	}
	if !strings.Contains(out, "\n判定: 内容不同（首个差异偏移 1 · 大小差 2 字节）") {
		t.Fatalf("差异判定不符: %q", out)
	}
	if !strings.Contains(out, "（1 字节）") || !strings.Contains(out, "（3 字节）") {
		t.Fatalf("大小行不符: %q", out)
	}
}

// TestFileDiffDirectory 目录模式回执——口径行 + 清单 + 逐字节开关。
func TestFileDiffDirectory(t *testing.T) {
	svc, root := fileSvc(t)
	write := func(rel, content string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("left/a.txt", "A")
	write("left/b.txt", "B")
	write("left/sub/c.txt", "C")
	write("right/a.txt", "A")
	write("right/b.txt", "BB")
	write("right/sub/c.txt", "X")
	write("right/d.txt", "D")
	out, ok := FileDiff(svc, `{"left":"w:left","right":"w:right"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	prefix := `{"ok":true,"tool":"file-diff","target":"w:left","items":2}` + "\n" +
		"w:left | 2 处差异 · " + filepath.Join(root, "left") + " ↔ " + filepath.Join(root, "right")
	if !strings.HasPrefix(out, prefix) {
		t.Fatalf("目录回执不符\n实得: %q\n期望前缀: %q", out, prefix)
	}
	for _, line := range []string{
		"\n判定: 存在 2 处差异",
		"\n汇总: 共 4 个文件 · 相同 2 · 差异 2",
		"\n[口径] 按「路径集合 + 大小」判定；同大小的 2 对未逐字节比对（加 content=true 可逐字节）",
		"\n差异清单:",
		"\n  不同  b.txt",
		"\n  仅右  d.txt",
	} {
		if !strings.Contains(out, line) {
			t.Fatalf("正文缺少 %q: %q", line, out)
		}
	}
	if strings.Contains(out, "sub/c.txt") {
		t.Fatalf("默认口径不应列同大小文件: %q", out)
	}
	// 逐字节口径
	out, ok = FileDiff(svc, `{"left":"w:left","right":"w:right","content":"true"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	if !strings.HasPrefix(out, `{"ok":true,"tool":"file-diff","target":"w:left","items":3}`+"\n") {
		t.Fatalf("逐字节头不符: %q", out)
	}
	for _, line := range []string{
		"\n[口径] 按「路径集合 + 大小 + 逐字节」判定（content=true）",
		"\n  不同  sub/c.txt",
	} {
		if !strings.Contains(out, line) {
			t.Fatalf("逐字节正文缺少 %q: %q", line, out)
		}
	}
}

// TestFileDiffTruncation 差异清单截断提示。
func TestFileDiffTruncation(t *testing.T) {
	svc, root := fileSvc(t)
	write := func(rel, content string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("one/a.txt", "a")
	write("one/b.txt", "b")
	write("two/a.txt", "AA")
	write("two/b.txt", "BB")
	out, ok := FileDiff(svc, `{"left":"w:one","right":"w:two","limit":"1"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	if !strings.Contains(out, "\n差异清单:") || !strings.Contains(out, "\n  不同  a.txt") {
		t.Fatalf("清单不符: %q", out)
	}
	if !strings.Contains(out, "\n[截断] 共 2 条差异，已列 1 条——提高 limit 至 ≥2 可看全") {
		t.Fatalf("截断行不符: %q", out)
	}
}

// TestFileDiffBadArgs 参数面。
func TestFileDiffBadArgs(t *testing.T) {
	svc, _ := fileSvc(t)
	if out, ok := FileDiff(svc, `{"left":"w:a.md"}`); ok || out != "ERR|BAD_ARGS|缺参数 right（必填：left right）" {
		t.Fatalf("缺参回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileDiff(svc, `{"left":"w:a.md","right":"w:b.txt","content":"yes"}`); ok || out != "ERR|BAD_ARGS|参数 content 非布尔: yes" {
		t.Fatalf("content 回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileDiff(svc, `{"left":"w:a.md","right":"w:b.txt","limit":"abc"}`); ok || out != "ERR|BAD_ARGS|参数 limit 非整数: abc" {
		t.Fatalf("limit 回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileDiff(nil, `{"left":"w:a.md","right":"w:b.txt"}`); ok || out != "ERR|FS_NO_SERVICE|宿主未注入 FileSystemService" {
		t.Fatalf("服务缺失回执 = %q ok=%v", out, ok)
	}
}

// TestGroupDigits 千分位与单位换算文案。
func TestGroupDigits(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{1234567, "1,234,567"},
		{-1234, "-1,234"},
	}
	for _, c := range cases {
		if got := groupDigits(c.in); got != c.want {
			t.Fatalf("groupDigits(%d) = %s，期望 %s", c.in, got, c.want)
		}
	}
	if got := statSizeText(1023); got != "1,023 字节" {
		t.Fatalf("statSizeText(1023) = %s", got)
	}
	if got := statSizeText(1024); got != "1,024 字节（1.00 KB）" {
		t.Fatalf("statSizeText(1024) = %s", got)
	}
	if got := statSizeText(1048576); got != "1,048,576 字节（1.00 MB）" {
		t.Fatalf("statSizeText(1048576) = %s", got)
	}
	if got := statSizeText(2048); got != "2,048 字节（2.00 KB）" {
		t.Fatalf("statSizeText(2048) = %s", got)
	}
}
