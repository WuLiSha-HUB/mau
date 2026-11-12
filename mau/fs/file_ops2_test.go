package fs

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// newRecycleService 建带回收站的临时服务——回收站落在可写根内（root/.recycle）。
func newRecycleService(t *testing.T) (*Service, string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "w")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	recycle := filepath.Join(root, ".recycle")
	svc, err := NewService([]Root{{ID: "w", Path: root, Writable: true}}, recycle, "")
	if err != nil {
		t.Fatalf("建服务失败: %v", err)
	}
	return svc, root, recycle
}

// TestRecycleFile 文件回收——落点格式 + 体积统计 + 源消失。
func TestRecycleFile(t *testing.T) {
	svc, root, recycle := newRecycleService(t)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	outcome, err := svc.Recycle("w:a.txt")
	if err != nil {
		t.Fatalf("回收失败: %v", err)
	}
	if outcome.IsDirectory || outcome.FileCount != 1 || outcome.DirectoryCount != 0 || outcome.TotalBytes != 5 {
		t.Fatalf("统计 = %+v", outcome)
	}
	if !strings.EqualFold(filepath.Dir(outcome.Target), recycle) {
		t.Fatalf("落点根 = %s，期望 %s", filepath.Dir(outcome.Target), recycle)
	}
	pattern := regexp.MustCompile(`^\d{8}T\d{13}Z-[0-9a-f]{32}-a\.txt$`)
	if !pattern.MatchString(filepath.Base(outcome.Target)) {
		t.Fatalf("落点名 = %s，不符 <UTC 时间戳>-<GUID32>-<叶子> 格式", filepath.Base(outcome.Target))
	}
	if _, statErr := os.Stat(filepath.Join(root, "a.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("源文件仍存在: %v", statErr)
	}
	if raw, readErr := os.ReadFile(outcome.Target); readErr != nil || string(raw) != "hello" {
		t.Fatalf("回收站内容 = %q err=%v", string(raw), readErr)
	}
}

// TestRecycleDirectory 目录回收——整棵子树迁移 + 文件数 / 子目录数 / 合计字节。
func TestRecycleDirectory(t *testing.T) {
	svc, root, _ := newRecycleService(t)
	if err := os.MkdirAll(filepath.Join(root, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "deep", "b.txt"), []byte("bcd"), 0o644); err != nil {
		t.Fatal(err)
	}
	outcome, err := svc.Recycle("w:sub")
	if err != nil {
		t.Fatalf("回收失败: %v", err)
	}
	if !outcome.IsDirectory || outcome.FileCount != 2 || outcome.DirectoryCount != 1 || outcome.TotalBytes != 4 {
		t.Fatalf("统计 = %+v", outcome)
	}
	if raw, readErr := os.ReadFile(filepath.Join(outcome.Target, "deep", "b.txt")); readErr != nil || string(raw) != "bcd" {
		t.Fatalf("回收站结构 = %q err=%v", string(raw), readErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, "sub")); !os.IsNotExist(statErr) {
		t.Fatalf("源目录仍存在: %v", statErr)
	}
}

// TestRecycleControlledRoot 受控根本身不可回收。
func TestRecycleControlledRoot(t *testing.T) {
	svc, _, _ := newRecycleService(t)
	_, err := svc.Recycle("w:")
	if err == nil || !strings.HasPrefix(err.Error(), "Refusing to recycle a controlled root: ") {
		t.Fatalf("期望受控根拒绝，实际 %v", err)
	}
}

// TestRecycleMissing 目标不存在。
func TestRecycleMissing(t *testing.T) {
	svc, _, _ := newRecycleService(t)
	_, err := svc.Recycle("w:nope.txt")
	if err == nil || ErrText(err) != "ERR|FileNotFoundException|Recycle target was not found." {
		t.Fatalf("期望 FileNotFoundException，实际 %v", err)
	}
}

// TestRecycleReadOnlyRoot 只读根回收拒绝（写语义解析）。
func TestRecycleReadOnlyRoot(t *testing.T) {
	svc, _, root2 := newService(t)
	if err := os.WriteFile(filepath.Join(root2, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Recycle("r:a.txt")
	if err == nil || !strings.Contains(err.Error(), "read-only root") {
		t.Fatalf("期望只读拒绝，实际 %v", err)
	}
}

// TestRecycleNoRoot 未配置回收站——Go 侧兜底（C# 构造必填，运行期不可达）。
func TestRecycleNoRoot(t *testing.T) {
	svc, root, _ := newService(t)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Recycle("w:a.txt")
	if err == nil || !strings.Contains(err.Error(), "Recycle root is not configured.") {
		t.Fatalf("期望未配置提示，实际 %v", err)
	}
}

// TestCopyFile 文件复制——父目录自动创建 + 目标已存在拒绝 + 源缺失。
func TestCopyFile(t *testing.T) {
	svc, root, _ := newService(t)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.Copy("w:a.txt", "w:copied/a.txt"); err != nil {
		t.Fatalf("复制失败: %v", err)
	}
	if raw, readErr := os.ReadFile(filepath.Join(root, "copied", "a.txt")); readErr != nil || string(raw) != "abc" {
		t.Fatalf("目标内容 = %q err=%v", string(raw), readErr)
	}
	if err := svc.Copy("w:a.txt", "w:copied/a.txt"); err == nil || ErrText(err) != "ERR|IOException|Copy destination already exists." {
		t.Fatalf("目标已存在回执 = %v", err)
	}
	if err := svc.Copy("w:nope.txt", "w:x/nope.txt"); err == nil || ErrText(err) != "ERR|FileNotFoundException|Copy source was not found." {
		t.Fatalf("源缺失回执 = %v", err)
	}
}

// TestCopyDirectory 目录整棵复制（含非空子目录）。
func TestCopyDirectory(t *testing.T) {
	svc, root, _ := newService(t)
	if err := os.MkdirAll(filepath.Join(root, "src", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "deep", "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.Copy("w:src", "w:dst/sub"); err != nil {
		t.Fatalf("目录复制失败: %v", err)
	}
	if raw, readErr := os.ReadFile(filepath.Join(root, "dst", "sub", "deep", "b.txt")); readErr != nil || string(raw) != "b" {
		t.Fatalf("子目录文件 = %q err=%v", string(raw), readErr)
	}
	if err := svc.Copy("w:src", "w:dst/sub"); err == nil || ErrText(err) != "ERR|IOException|Copy destination already exists." {
		t.Fatalf("目标已存在回执 = %v", err)
	}
}

// TestCopyReadOnlyRootSource 只读根可作为源（读语义解析）。
func TestCopyReadOnlyRootSource(t *testing.T) {
	svc, _, root2 := newService(t)
	if err := os.WriteFile(filepath.Join(root2, "s.txt"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.Copy("r:s.txt", "w:copy.txt"); err != nil {
		t.Fatalf("只读根源复制失败: %v", err)
	}
	if raw, readErr := os.ReadFile(filepath.Join(svc.Roots[0].Path, "copy.txt")); readErr != nil || string(raw) != "s" {
		t.Fatalf("目标内容 = %q err=%v", string(raw), readErr)
	}
}

// TestVersionFactsNonPE 非 PE 文件明确报错。
func TestVersionFactsNonPE(t *testing.T) {
	svc, root, _ := newService(t)
	if err := os.WriteFile(filepath.Join(root, "plain.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := svc.VersionFacts("w:plain.txt")
	if err == nil || ErrText(err) != "ERR|InvalidDataException|Not a PE file (no version resource)." {
		t.Fatalf("非 PE 回执 = %v", err)
	}
}

// TestVersionFactsNoVersion 无版本资源 / 有资源读不出——不伪造版本。
func TestVersionFactsNoVersion(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{Roots: []Root{{ID: "x", Path: filepath.Dir(exe)}}}
	_, verr := svc.VersionFacts("x:" + filepath.Base(exe))
	if verr == nil {
		t.Fatal("Go 测试 exe 不应有可读版本")
	}
	text := ErrText(verr)
	if !strings.HasPrefix(text, "ERR|InvalidDataException|PE file has ") {
		t.Fatalf("错误面 = %s", text)
	}
	if !strings.Contains(text, "no version resource") && !strings.Contains(text, "no readable version facts") {
		t.Fatalf("错误面缺少「无资源 / 读不出」区分: %s", text)
	}
}

// TestVersionFactsRealPE 真实 PE（kernel32.dll）——版本三件 + 元数据。
func TestVersionFactsRealPE(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅 Windows")
	}
	path := filepath.Join(os.Getenv("SystemRoot"), "System32", "kernel32.dll")
	if _, err := os.Stat(path); err != nil {
		t.Skip("kernel32.dll 不在")
	}
	svc := &Service{Roots: []Root{{ID: "sys", Path: filepath.Dir(path)}}}
	facts, err := svc.VersionFacts("sys:kernel32.dll")
	if err != nil {
		t.Fatalf("版本读取失败: %v", err)
	}
	if facts.Version == "" || facts.FileVersion == "" {
		t.Fatalf("版本三件 = %+v", facts)
	}
	if facts.ProductVersion == "" && !facts.FallbackUsed {
		t.Fatalf("ProductVersion 空须标记回落: %+v", facts)
	}
	if facts.SizeBytes <= 0 || facts.LastWrite.IsZero() {
		t.Fatalf("元数据 = %+v", facts)
	}
}

// TestZeroVersionText 零值占位归一。
func TestZeroVersionText(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"", true},
		{"0", true},
		{"0.0", true},
		{"0.0.0.0", true},
		{" 0. 0\t", true},
		{"1.0", false},
		{"0.0.0.1", false},
		{"abc", false},
	}
	for _, c := range cases {
		if got := isZeroVersionText(c.text); got != c.want {
			t.Fatalf("isZeroVersionText(%q) = %v，期望 %v", c.text, got, c.want)
		}
	}
}
