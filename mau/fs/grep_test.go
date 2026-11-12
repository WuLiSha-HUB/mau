package fs

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile 在根内落一个文件（自动建父目录）。
func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestGrepFacts 覆盖命中 / 忽略目录 / 上下文 / 截断自证。
func TestGrepFacts(t *testing.T) {
	svc, root, _ := newService(t)
	writeFile(t, root, "a.txt", "0123456789hello0123456789\nno hit\n")
	writeFile(t, root, "sub/b.txt", "hello\n")
	writeFile(t, root, "node_modules/c.txt", "hello\n")
	writeFile(t, root, "bin/d.txt", "hello\n")

	facts, err := svc.Grep("w:", "hello", "*", 200)
	if err != nil {
		t.Fatalf("Grep 失败: %v", err)
	}
	if facts.HitTotal != 2 {
		t.Fatalf("HitTotal = %d，期望 2", facts.HitTotal)
	}
	if len(facts.Hits) != 2 {
		t.Fatalf("Hits = %d，期望 2", len(facts.Hits))
	}
	if facts.IgnoredDirs != 2 {
		t.Fatalf("IgnoredDirs = %d，期望 2", facts.IgnoredDirs)
	}
	if got := facts.Hits[0].Context; got != "0123456789hello0123456789" {
		t.Fatalf("上下文 = %q", got)
	}
	if facts.Hits[0].Line != 1 || facts.Hits[0].RelativePath != "a.txt" {
		t.Fatalf("首条 = %+v", facts.Hits[0])
	}

	facts, err = svc.Grep("w:", "hello", "*", 1)
	if err != nil {
		t.Fatalf("Grep 失败: %v", err)
	}
	if len(facts.Hits) != 1 || facts.HitTotal != 2 {
		t.Fatalf("截断口径 = Hits %d / HitTotal %d，期望 1 / 2", len(facts.Hits), facts.HitTotal)
	}
}

// TestGrepCJKContext 中文上下文按 rune 切——不切坏 UTF-8。
func TestGrepCJKContext(t *testing.T) {
	svc, root, _ := newService(t)
	content := "一二三四五六七八九十关键词甲乙丙丁戊己庚辛壬癸"
	writeFile(t, root, "zh.txt", content)

	facts, err := svc.Grep("w:", "关键词", "*", 200)
	if err != nil {
		t.Fatalf("Grep 失败: %v", err)
	}
	if len(facts.Hits) != 1 {
		t.Fatalf("Hits = %d，期望 1", len(facts.Hits))
	}
	if got := facts.Hits[0].Context; got != content {
		t.Fatalf("上下文 = %q，期望 %q", got, content)
	}
}

// TestGrepPatternFilter 文件名过滤生效——只扫 *.txt。
func TestGrepPatternFilter(t *testing.T) {
	svc, root, _ := newService(t)
	writeFile(t, root, "a.txt", "needle\n")
	writeFile(t, root, "b.md", "needle\n")

	facts, err := svc.Grep("w:", "needle", "*.txt", 200)
	if err != nil {
		t.Fatalf("Grep 失败: %v", err)
	}
	if facts.HitTotal != 1 || len(facts.Hits) != 1 || facts.Hits[0].RelativePath != "a.txt" {
		t.Fatalf("过滤结果 = %+v", facts)
	}
}

// TestGrepBadParams 参数面拒绝——空关键词 / limit 越界。
func TestGrepBadParams(t *testing.T) {
	svc, _, _ := newService(t)
	if _, err := svc.Grep("w:", "", "*", 200); err == nil {
		t.Fatal("空关键词应报错")
	}
	if _, err := svc.Grep("w:", "x", "*", 0); err == nil {
		t.Fatal("limit=0 应报错")
	}
}
