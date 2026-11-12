package bricks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cathome5/mau/fs"
)

// grepSvc 建临时根服务。
func grepSvc(t *testing.T) (*fs.Service, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "w")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return &fs.Service{Roots: []fs.Root{{ID: "w", Path: root, Writable: true}}}, root
}

// TestTextGrepHit 命中态——头 + 摘要行 + 命中行。
func TestTextGrepHit(t *testing.T) {
	svc, root := grepSvc(t)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello world\nsecond hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok := TextGrep(svc, `{"dir":"w:","keyword":"hello"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	lines := strings.Split(out, "\n")
	if lines[0] != `{"ok":true,"tool":"text-grep","target":"w:","items":2}` {
		t.Fatalf("头 = %s", lines[0])
	}
	if lines[1] != "w: | 命中 2" {
		t.Fatalf("摘要 = %s", lines[1])
	}
	if lines[2] != "a.txt:1:hello world" {
		t.Fatalf("首条命中 = %s", lines[2])
	}
}

// TestTextGrepEmpty 无命中——只有头与摘要两行。
func TestTextGrepEmpty(t *testing.T) {
	svc, root := grepSvc(t)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("nothing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok := TextGrep(svc, `{"dir":"w:","keyword":"hello"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	if out != `{"ok":true,"tool":"text-grep","target":"w:","items":0}`+"\n"+"w: | 命中 0" {
		t.Fatalf("回执 = %q", out)
	}
}

// TestTextGrepBadArgs 参数面——缺参 / 未知参数 / limit 非整数。
func TestTextGrepBadArgs(t *testing.T) {
	svc, _ := grepSvc(t)
	if out, ok := TextGrep(svc, `{"dir":"w:"}`); ok || out != "ERR|BAD_ARGS|缺参数 keyword（必填：dir keyword）" {
		t.Fatalf("缺参回执 = %q ok=%v", out, ok)
	}
	if out, ok := TextGrep(svc, `{"dir":"w:","keyword":"x","foo":1}`); ok || out != "ERR|BAD_ARGS|未知参数: foo（支持 dir keyword pattern limit）" {
		t.Fatalf("未知参数回执 = %q ok=%v", out, ok)
	}
	if out, ok := TextGrep(svc, `{"dir":"w:","keyword":"x","limit":"abc"}`); ok || out != "ERR|BAD_ARGS|参数 limit 非整数: abc" {
		t.Fatalf("limit 回执 = %q ok=%v", out, ok)
	}
}

// TestTextGrepNestedPath 嵌套命中——相对路径用平台分隔符（对齐 C# Path.GetRelativePath，不转斜杠）。
func TestTextGrepNestedPath(t *testing.T) {
	svc, root := grepSvc(t)
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok := TextGrep(svc, `{"dir":"w:","keyword":"hello"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	want := "sub" + string(os.PathSeparator) + "a.txt:1:hello"
	if !strings.Contains(out, want) {
		t.Fatalf("回执 = %q，期望含 %q", out, want)
	}
}
