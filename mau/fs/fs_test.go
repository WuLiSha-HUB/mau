package fs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newService 建临时根表——root1 可写 / root2 只读。
func newService(t *testing.T) (*Service, string, string) {
	t.Helper()
	base := t.TempDir()
	root1 := filepath.Join(base, "w")
	root2 := filepath.Join(base, "r")
	if err := os.MkdirAll(root1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root2, 0o755); err != nil {
		t.Fatal(err)
	}
	return &Service{Roots: []Root{
		{ID: "w", Path: root1, Writable: true},
		{ID: "r", Path: root2, Writable: false},
	}}, root1, root2
}

// TestResolveRootAddress 根寻址形态。
func TestResolveRootAddress(t *testing.T) {
	svc, root1, _ := newService(t)
	got, err := svc.Resolve("w:sub/x.txt", false)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	want := filepath.Join(root1, "sub", "x.txt")
	if !strings.EqualFold(got, want) {
		t.Fatalf("解析 = %s，期望 %s", got, want)
	}
}

// TestResolveBareRoot 裸根名 = 根目录。
func TestResolveBareRoot(t *testing.T) {
	svc, root1, _ := newService(t)
	got, err := svc.Resolve("w", false)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !strings.EqualFold(got, root1) {
		t.Fatalf("解析 = %s，期望 %s", got, root1)
	}
}

// TestResolveUnknownID 未知根 id。
func TestResolveUnknownID(t *testing.T) {
	svc, _, _ := newService(t)
	_, err := svc.Resolve("nope:x", false)
	if err == nil || !strings.Contains(err.Error(), ErrUnknownID) {
		t.Fatalf("期望 %s，实际 %v", ErrUnknownID, err)
	}
}

// TestResolveOutOfScope 根外绝对路径。
func TestResolveOutOfScope(t *testing.T) {
	svc, _, _ := newService(t)
	_, err := svc.Resolve(filepath.Join(t.TempDir(), "outsider.txt"), false)
	if err == nil || !strings.Contains(err.Error(), ErrOutOfScope) {
		t.Fatalf("期望 %s，实际 %v", ErrOutOfScope, err)
	}
}

// TestResolveReadOnly 只读根写入拒绝。
func TestResolveReadOnly(t *testing.T) {
	svc, _, root2 := newService(t)
	_, err := svc.Resolve(filepath.Join(root2, "a.txt"), true)
	if err == nil || !strings.Contains(err.Error(), "read-only root") {
		t.Fatalf("期望只读拒绝，实际 %v", err)
	}
	if _, err := svc.Resolve(filepath.Join(root2, "a.txt"), false); err != nil {
		t.Fatalf("只读根读取应放行: %v", err)
	}
}

// TestResolveEmpty 空路径。
func TestResolveEmpty(t *testing.T) {
	svc, _, _ := newService(t)
	_, err := svc.Resolve("  ", false)
	if err == nil || !strings.Contains(err.Error(), "Path is empty.") {
		t.Fatalf("期望 Path is empty.，实际 %v", err)
	}
}

// TestWriteStyleByContract 新建文件编码契约。
func TestWriteStyleByContract(t *testing.T) {
	svc, _, _ := newService(t)
	cases := []struct {
		name string
		want string
	}{
		{"a.md", "UTF-8 BOM / LF"},
		{"a.cs", "UTF-8 BOM / CRLF"},
		{"a.js", "UTF-8 / CRLF"},
		{"a.txt", "UTF-8 / LF"},
	}
	for _, c := range cases {
		style, err := svc.WriteTextAuto("w:"+c.name, "x")
		if err != nil {
			t.Fatalf("%s 写入失败: %v", c.name, err)
		}
		if style != c.want {
			t.Fatalf("%s 风格 = %s，期望 %s", c.name, style, c.want)
		}
	}
}

// TestWritePreserveExisting 既有文件 BOM / 换行保真。
func TestWritePreserveExisting(t *testing.T) {
	svc, root1, _ := newService(t)
	raw := []byte("a\r\nb\r\n")
	if err := os.WriteFile(filepath.Join(root1, "keep.txt"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	style, err := svc.WriteTextAuto("w:keep.txt", "x\ny\n")
	if err != nil {
		t.Fatal(err)
	}
	if style != "UTF-8 / CRLF" {
		t.Fatalf("须保真 CRLF，实际 %s", style)
	}
	got, _ := os.ReadFile(filepath.Join(root1, "keep.txt"))
	if string(got) != "x\r\ny\r\n" {
		t.Fatalf("落盘 = %q，期望 CRLF 归一", string(got))
	}
}

// TestReadLines 行号区间读取。
func TestReadLines(t *testing.T) {
	svc, root1, _ := newService(t)
	if err := os.WriteFile(filepath.Join(root1, "l.txt"), []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, err := svc.ReadLinesAuto("w:l.txt", 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if body != "2: b\n3: c" {
		t.Fatalf("读行 = %q", body)
	}
	if _, err := svc.ReadLinesAuto("w:l.txt", 0, 0); err == nil {
		t.Fatal("start=0 应报错")
	}
}

// TestBomStrippedOnRead 读侧剥 BOM。
func TestBomStrippedOnRead(t *testing.T) {
	svc, root1, _ := newService(t)
	raw := append([]byte{0xEF, 0xBB, 0xBF}, []byte("hello")...)
	if err := os.WriteFile(filepath.Join(root1, "b.md"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	text, err := svc.ReadTextAuto("w:b.md")
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello" {
		t.Fatalf("读 = %q，期望去掉 BOM", text)
	}
}

// TestResolveVolumeRoot 卷根作为受控根时子路径仍归属（前缀拼接不重复分隔符）。
func TestResolveVolumeRoot(t *testing.T) {
	volume := filepath.VolumeName(t.TempDir())
	if volume == "" {
		t.Skip("无卷根环境")
	}
	root := volume + string(os.PathSeparator)
	svc := &Service{Roots: []Root{{ID: "v", Path: root, Writable: false}}}
	probe := filepath.Join(root, "Windows", "System32")
	got, err := svc.Resolve(probe, false)
	if err != nil {
		t.Fatalf("卷根应命中: %v", err)
	}
	if !strings.EqualFold(got, probe) {
		t.Fatalf("解析 = %s，期望 %s", got, probe)
	}
}
