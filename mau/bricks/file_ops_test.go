package bricks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cathome5/mau/fs"
)

// fileSvc 建带文件树的临时根服务——a.md / b.txt / noext / z / sub / empty / bin / .git。
func fileSvc(t *testing.T) (*fs.Service, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "w")
	write := func(rel, content string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.md", "a")
	write("b.txt", "b")
	write("noext", "n")
	write("z", "z")
	write("sub/c.md", "c")
	write("sub/deep/d.txt", "d")
	write("empty/keep.txt", "k")
	write("bin/skip.txt", "s")
	write(".git/HEAD", "ref: refs/heads/main\n")
	write(".git/refs/heads/main", "abcdef1234567890abcdef1234567890abcdef12\n")
	return &fs.Service{Roots: []fs.Root{{ID: "w", Path: root, Writable: true}}}, root
}

// TestFileTreeDefault 目录树——BFS 两层 + .git 特征 + 忽略计数。
func TestFileTreeDefault(t *testing.T) {
	svc, _ := fileSvc(t)
	out, ok := FileTree(svc, `{"path":"w:"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	want := `{"ok":true,"tool":"file-tree","target":"w:","items":11}` + "\n" +
		`w: | 11 条` + "\n" +
		"a.md\nb.txt\nempty/\nnoext\nsub/\nz\nempty\\keep.txt\nsub\\c.md\nsub\\deep/\n" +
		`[git] 存在 .git（.git——HEAD: refs/heads/main → abcdef1）` + "\n" +
		`[skip] 2 个忽略目录（.git/bin/obj/node_modules 等）未扫描——条目在其内已跳过`
	if out != want {
		t.Fatalf("回执不符\n实得: %q\n期望: %q", out, want)
	}
}

// TestFileTreeDepthLimit 层级 + 截断——git 行不占 limit 余量。
func TestFileTreeDepthLimit(t *testing.T) {
	svc, _ := fileSvc(t)
	out, ok := FileTree(svc, `{"path":"w:","depth":1,"limit":3}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	want := `{"ok":true,"tool":"file-tree","target":"w:","items":5}` + "\n" +
		`w: | 5 条` + "\n" +
		"a.md\nb.txt\nempty/\n" +
		`[skip] 2 个忽略目录（.git/bin/obj/node_modules 等）未扫描——条目在其内已跳过` + "\n" +
		`[截断] 共 6 条，已列 3 条——提高 limit 至 ≥6，或收窄 path / 降 depth 可看全`
	if out != want {
		t.Fatalf("回执不符\n实得: %q\n期望: %q", out, want)
	}
}

// TestFileTreeErrors 参数与路径错误。
func TestFileTreeErrors(t *testing.T) {
	svc, _ := fileSvc(t)
	if out, ok := FileTree(svc, `{"path":"w:","depth":0}`); ok || out != `ERR|ArgumentOutOfRangeException|Specified argument was out of the range of valid values. (Parameter 'depth')` {
		t.Fatalf("depth=0 回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileTree(svc, `{"path":"w:","limit":0}`); ok || out != `ERR|ArgumentOutOfRangeException|Specified argument was out of the range of valid values. (Parameter 'depth')` {
		t.Fatalf("limit=0 回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileTree(svc, `{"path":"w:","depth":"abc"}`); ok || out != "ERR|BAD_ARGS|参数 depth 非整数: abc" {
		t.Fatalf("depth 非整数回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileTree(svc, `{"path":"w:nope"}`); ok || out != "ERR|DirectoryNotFoundException|Tree root was not found." {
		t.Fatalf("缺失根回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileTree(nil, `{"path":"w:"}`); ok || out != "ERR|FS_NO_SERVICE|宿主未注入 FileSystemService" {
		t.Fatalf("服务缺失回执 = %q ok=%v", out, ok)
	}
}

// TestFileFindPatterns glob 语义与计数——*.md / **\/\*.txt / *.* / 递归开关 / limit 截断 / 单字符。
func TestFileFindPatterns(t *testing.T) {
	svc, _ := fileSvc(t)
	cases := []struct {
		name string
		args string
		want string
	}{
		{
			"md",
			`{"dir":"w:","pattern":"*.md"}`,
			`{"ok":true,"tool":"file-find","target":"w:","items":3}` + "\n" +
				`w: | 3 条` + "\n" +
				"a.md\nsub\\c.md\n" +
				`[skip] 2 个忽略目录（.git/bin/obj/node_modules 等）未扫描——条目在其内已跳过`,
		},
		{
			"双星",
			`{"dir":"w:","pattern":"**/*.txt"}`,
			`{"ok":true,"tool":"file-find","target":"w:","items":4}` + "\n" +
				`w: | 4 条` + "\n" +
				"b.txt\nempty\\keep.txt\nsub\\deep\\d.txt\n" +
				`[skip] 2 个忽略目录（.git/bin/obj/node_modules 等）未扫描——条目在其内已跳过`,
		},
		{
			"星点星",
			`{"dir":"w:","pattern":"*.*"}`,
			`{"ok":true,"tool":"file-find","target":"w:","items":8}` + "\n" +
				`w: | 8 条` + "\n" +
				"a.md\nb.txt\nempty\\keep.txt\nnoext\nsub\\c.md\nsub\\deep\\d.txt\nz\n" +
				`[skip] 2 个忽略目录（.git/bin/obj/node_modules 等）未扫描——条目在其内已跳过`,
		},
		{
			"不递归",
			`{"dir":"w:","pattern":"*.txt","recursive":"false"}`,
			`{"ok":true,"tool":"file-find","target":"w:","items":1}` + "\n" +
				`w: | 1 条` + "\n" +
				"b.txt",
		},
		{
			"截断",
			`{"dir":"w:","pattern":"*.txt","limit":"1"}`,
			`{"ok":true,"tool":"file-find","target":"w:","items":3}` + "\n" +
				`w: | 3 条` + "\n" +
				"b.txt\n" +
				`[skip] 2 个忽略目录（.git/bin/obj/node_modules 等）未扫描——条目在其内已跳过` + "\n" +
				`[截断] 共 3 条，已列 1 条——提高 limit 至 ≥3，或收窄 dir / pattern 可看全`,
		},
		{
			"单字符",
			`{"dir":"w:","pattern":"?"}`,
			`{"ok":true,"tool":"file-find","target":"w:","items":2}` + "\n" +
				`w: | 2 条` + "\n" +
				"z\n" +
				`[skip] 2 个忽略目录（.git/bin/obj/node_modules 等）未扫描——条目在其内已跳过`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, ok := FileFind(svc, c.args)
			if !ok {
				t.Fatalf("执行失败: %s", out)
			}
			if out != c.want {
				t.Fatalf("回执不符\n实得: %q\n期望: %q", out, c.want)
			}
		})
	}
}

// TestFileFindErrors 参数与路径错误。
func TestFileFindErrors(t *testing.T) {
	svc, root := fileSvc(t)
	if out, ok := FileFind(svc, `{"dir":"w:","pattern":" "}`); ok || out != `ERR|ArgumentException|Find parameters are invalid. (Parameter 'pattern')` {
		t.Fatalf("空白 pattern 回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileFind(svc, `{"dir":"w:","limit":"0"}`); ok || out != `ERR|ArgumentException|Find parameters are invalid. (Parameter 'pattern')` {
		t.Fatalf("limit=0 回执 = %q ok=%v", out, ok)
	}
	missing := filepath.Join(root, "nope")
	want := "ERR|DirectoryNotFoundException|Could not find a part of the path '" + missing + "'."
	if out, ok := FileFind(svc, `{"dir":"w:nope"}`); ok || out != want {
		t.Fatalf("缺失目录回执 = %q，期望 %q", out, want)
	}
	if out, ok := FileFind(svc, `{"dir":"w:","limit":"abc"}`); ok || out != "ERR|BAD_ARGS|参数 limit 非整数: abc" {
		t.Fatalf("limit 非整数回执 = %q ok=%v", out, ok)
	}
}

// TestFileMoveSuccess 文件 / 目录移动——父目录自动创建、目标已存在拒绝、源缺失。
func TestFileMoveSuccess(t *testing.T) {
	svc, root := fileSvc(t)
	out, ok := FileMove(svc, `{"src":"w:a.md","dest":"w:moved/a.md"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	want := `{"ok":true,"tool":"file-move","target":"w:a.md"}` + "\n" + "w:a.md → w:moved/a.md"
	if out != want {
		t.Fatalf("回执不符\n实得: %q\n期望: %q", out, want)
	}
	if raw, err := os.ReadFile(filepath.Join(root, "moved", "a.md")); err != nil || string(raw) != "a" {
		t.Fatalf("目标文件 = %q err=%v", string(raw), err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.md")); !os.IsNotExist(err) {
		t.Fatalf("源文件仍存在: %v", err)
	}
	// 目标已存在
	if out, ok := FileMove(svc, `{"src":"w:sub/c.md","dest":"w:moved/a.md"}`); ok || out != "ERR|IOException|Move destination already exists." {
		t.Fatalf("目标已存在回执 = %q ok=%v", out, ok)
	}
	// 源缺失
	if out, ok := FileMove(svc, `{"src":"w:nope.txt","dest":"w:x/nope.txt"}`); ok || out != "ERR|FileNotFoundException|Move source was not found." {
		t.Fatalf("源缺失回执 = %q ok=%v", out, ok)
	}
	// 目录整棵移动
	if out, ok := FileMove(svc, `{"src":"w:sub","dest":"w:deep/sub2"}`); !ok {
		t.Fatalf("目录移动失败: %s", out)
	}
	if raw, err := os.ReadFile(filepath.Join(root, "deep", "sub2", "deep", "d.txt")); err != nil || string(raw) != "d" {
		t.Fatalf("目录移动后文件 = %q err=%v", string(raw), err)
	}
}

// TestFileMoveBadArgs 参数面。
func TestFileMoveBadArgs(t *testing.T) {
	svc, _ := fileSvc(t)
	if out, ok := FileMove(svc, `{"src":"w:a.md"}`); ok || out != "ERR|BAD_ARGS|缺参数 dest（必填：src dest）" {
		t.Fatalf("缺参回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileMove(svc, `{"src":"w:a.md","dest":"w:b.md","zzz":1}`); ok || out != "ERR|BAD_ARGS|未知参数: zzz（支持 src dest）" {
		t.Fatalf("未知参数回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileMove(nil, `{"src":"w:a.md","dest":"w:b.md"}`); ok || out != "ERR|FS_NO_SERVICE|宿主未注入 FileSystemService" {
		t.Fatalf("服务缺失回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileMove(svc, `{"src":"w:a.md","dest":"w:b.md","x":"y"}`); ok || !strings.Contains(out, "未知参数: x") {
		t.Fatalf("未知参数 2 回执 = %q ok=%v", out, ok)
	}
}
