package bricks

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"cathome5/mau/fs"
)

// fileSvcRecycle 建带回收站的临时服务——回收站落 w 根内 .recycle（不干扰 tree/find 用例的独立实例）。
func fileSvcRecycle(t *testing.T) (*fs.Service, string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "w")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	recycle := filepath.Join(root, ".recycle")
	svc, err := fs.NewService([]fs.Root{{ID: "w", Path: root, Writable: true}}, recycle, "")
	if err != nil {
		t.Fatalf("建服务失败: %v", err)
	}
	return svc, root, recycle
}

// TestFileDeleteFile 文件回收回执——A214 头 + 落点格式 + 体积尾巴。
func TestFileDeleteFile(t *testing.T) {
	svc, root, recycle := fileSvcRecycle(t)
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok := FileDelete(svc, `{"path":"w:a.md"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	head := `{"ok":true,"tool":"file-delete","target":"w:a.md"}`
	prefix := head + "\n" + "w:a.md → " + recycle + string(os.PathSeparator)
	suffix := "（文件 · 1 B）"
	if !strings.HasPrefix(out, prefix) || !strings.HasSuffix(out, suffix) {
		t.Fatalf("回执不符\n实得: %q\n前缀: %q\n后缀: %q", out, prefix, suffix)
	}
	middle := strings.TrimSuffix(strings.TrimPrefix(out, prefix), suffix)
	if !regexp.MustCompile(`^\d{8}T\d{13}Z-[0-9a-f]{32}-a\.md$`).MatchString(middle) {
		t.Fatalf("落点段 = %q，不符 <UTC 时间戳>-<GUID32>-<叶子> 格式", middle)
	}
	if _, statErr := os.Stat(filepath.Join(root, "a.md")); !os.IsNotExist(statErr) {
		t.Fatalf("源文件仍存在: %v", statErr)
	}
}

// TestFileDeleteDirectory 目录回收回执——文件数 / 子目录数 / 合计体积。
func TestFileDeleteDirectory(t *testing.T) {
	svc, root, recycle := fileSvcRecycle(t)
	if err := os.MkdirAll(filepath.Join(root, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "deep", "b.txt"), []byte("bcd"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok := FileDelete(svc, `{"path":"w:sub"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	prefix := `{"ok":true,"tool":"file-delete","target":"w:sub"}` + "\n" + "w:sub → " + recycle + string(os.PathSeparator)
	suffix := "（目录 · 文件 2 / 子目录 1 / 合计 4 B）"
	if !strings.HasPrefix(out, prefix) || !strings.HasSuffix(out, suffix) {
		t.Fatalf("回执不符\n实得: %q", out)
	}
	middle := strings.TrimSuffix(strings.TrimPrefix(out, prefix), suffix)
	if !regexp.MustCompile(`^\d{8}T\d{13}Z-[0-9a-f]{32}-sub$`).MatchString(middle) {
		t.Fatalf("落点段 = %q", middle)
	}
}

// TestFileDeleteErrors 参数面与错误面。
func TestFileDeleteErrors(t *testing.T) {
	svc, _, _ := fileSvcRecycle(t)
	if out, ok := FileDelete(svc, `{}`); ok || out != "ERR|BAD_ARGS|缺参数 path（必填：path）" {
		t.Fatalf("缺参回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileDelete(svc, `{"path":"w:a","zzz":1}`); ok || out != "ERR|BAD_ARGS|未知参数: zzz（支持 path）" {
		t.Fatalf("未知参数回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileDelete(nil, `{"path":"w:a"}`); ok || out != "ERR|FS_NO_SERVICE|宿主未注入 FileSystemService" {
		t.Fatalf("服务缺失回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileDelete(svc, `{"path":"w:"}`); ok || !strings.HasPrefix(out, "ERR|InvalidOperationException|Refusing to recycle a controlled root: ") {
		t.Fatalf("受控根回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileDelete(svc, `{"path":"w:nope.txt"}`); ok || out != "ERR|FileNotFoundException|Recycle target was not found." {
		t.Fatalf("缺失目标回执 = %q ok=%v", out, ok)
	}
}

// TestFileCopyFile 文件复制回执与错误面。
func TestFileCopyFile(t *testing.T) {
	svc, root := fileSvc(t)
	out, ok := FileCopy(svc, `{"src":"w:a.md","dest":"w:copied/a.md"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	want := `{"ok":true,"tool":"file-copy","target":"w:a.md"}` + "\n" + "w:a.md → w:copied/a.md"
	if out != want {
		t.Fatalf("回执不符\n实得: %q\n期望: %q", out, want)
	}
	if raw, err := os.ReadFile(filepath.Join(root, "copied", "a.md")); err != nil || string(raw) != "a" {
		t.Fatalf("目标内容 = %q err=%v", string(raw), err)
	}
	// 目标已存在
	if out, ok := FileCopy(svc, `{"src":"w:a.md","dest":"w:copied/a.md"}`); ok || out != "ERR|IOException|Copy destination already exists." {
		t.Fatalf("目标已存在回执 = %q ok=%v", out, ok)
	}
	// 源缺失
	if out, ok := FileCopy(svc, `{"src":"w:nope.txt","dest":"w:x/nope.txt"}`); ok || out != "ERR|FileNotFoundException|Copy source was not found." {
		t.Fatalf("源缺失回执 = %q ok=%v", out, ok)
	}
}

// TestFileCopyDirectory 目录整棵复制。
func TestFileCopyDirectory(t *testing.T) {
	svc, root := fileSvc(t)
	out, ok := FileCopy(svc, `{"src":"w:sub","dest":"w:copy2/sub"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	want := `{"ok":true,"tool":"file-copy","target":"w:sub"}` + "\n" + "w:sub → w:copy2/sub"
	if out != want {
		t.Fatalf("回执不符\n实得: %q\n期望: %q", out, want)
	}
	if raw, err := os.ReadFile(filepath.Join(root, "copy2", "sub", "deep", "d.txt")); err != nil || string(raw) != "d" {
		t.Fatalf("子目录文件 = %q err=%v", string(raw), err)
	}
}

// TestFileCopyBadArgs 参数面。
func TestFileCopyBadArgs(t *testing.T) {
	svc, _ := fileSvc(t)
	if out, ok := FileCopy(svc, `{"src":"w:a.md"}`); ok || out != "ERR|BAD_ARGS|缺参数 dest（必填：src dest）" {
		t.Fatalf("缺参回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileCopy(nil, `{"src":"w:a.md","dest":"w:b.md"}`); ok || out != "ERR|FS_NO_SERVICE|宿主未注入 FileSystemService" {
		t.Fatalf("服务缺失回执 = %q ok=%v", out, ok)
	}
}

// TestFileVersionNonPE 非 PE / 无版本资源错误面。
func TestFileVersionNonPE(t *testing.T) {
	svc, root := fileSvc(t)
	if err := os.WriteFile(filepath.Join(root, "plain.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, ok := FileVersion(svc, `{"path":"w:plain.txt"}`); ok || out != "ERR|InvalidDataException|Not a PE file (no version resource)." {
		t.Fatalf("非 PE 回执 = %q ok=%v", out, ok)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	svcExe := &fs.Service{Roots: []fs.Root{{ID: "x", Path: filepath.Dir(exe)}}}
	out, ok := FileVersion(svcExe, `{"path":"x:`+filepath.Base(exe)+`"}`)
	if ok || !strings.HasPrefix(out, "ERR|InvalidDataException|PE file has ") {
		t.Fatalf("无版本回执 = %q ok=%v", out, ok)
	}
}

// TestFileVersionRealPE 真实 PE——A214 头 + 六行正文（版本三件 / 修改时间 / 大小）。
func TestFileVersionRealPE(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅 Windows")
	}
	path := filepath.Join(os.Getenv("SystemRoot"), "System32", "kernel32.dll")
	if _, err := os.Stat(path); err != nil {
		t.Skip("kernel32.dll 不在")
	}
	svc := &fs.Service{Roots: []fs.Root{{ID: "sys", Path: filepath.Dir(path)}}}
	out, ok := FileVersion(svc, `{"path":"sys:kernel32.dll"}`)
	if !ok {
		t.Fatalf("执行失败: %s", out)
	}
	head := `{"ok":true,"tool":"file-version","target":"sys:kernel32.dll"}`
	prefix := head + "\n" + "sys:kernel32.dll | 路径: " + path
	if !strings.HasPrefix(out, prefix) {
		t.Fatalf("回执头不符\n实得: %q\n期望前缀: %q", out, prefix)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 7 {
		t.Fatalf("回执行数 = %d（期望 A214 头 + 六行正文）", len(lines))
	}
	body := strings.TrimPrefix(strings.Join(lines[1:], "\n"), "sys:kernel32.dll | ")
	for _, field := range []string{"路径: ", "\n版本: ", "\nFileVersion: ", "\nProductVersion: ", "\n修改时间: ", "\n大小: "} {
		if !strings.Contains(body, field) {
			t.Fatalf("正文缺少字段 %q: %q", field, body)
		}
	}
	if !regexp.MustCompile(`\n修改时间: \d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`).MatchString(body) {
		t.Fatalf("修改时间格式不符: %q", body)
	}
	if !regexp.MustCompile(`\n大小: \d+ 字节$`).MatchString(body) {
		t.Fatalf("大小行不符: %q", body)
	}
}

// TestFileVersionBadArgs 参数面。
func TestFileVersionBadArgs(t *testing.T) {
	svc, _ := fileSvc(t)
	if out, ok := FileVersion(svc, `{}`); ok || out != "ERR|BAD_ARGS|缺参数 path（必填：path）" {
		t.Fatalf("缺参回执 = %q ok=%v", out, ok)
	}
	if out, ok := FileVersion(nil, `{"path":"w:a.md"}`); ok || out != "ERR|FS_NO_SERVICE|宿主未注入 FileSystemService" {
		t.Fatalf("服务缺失回执 = %q ok=%v", out, ok)
	}
}
