package observe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// waitIO 等待后台探测批次完成。
func waitIO(t *testing.T, p *DepProbe) {
	t.Helper()
	select {
	case <-p.ioWait:
	case <-time.After(3 * time.Second):
		t.Fatal("依赖探测后台批次超时")
	}
}

// TestDepsUnconfigured 配置全空时的条目面：未配置项直接定案，后台仍跑受控根与开发辅助。
func TestDepsUnconfigured(t *testing.T) {
	p := NewDepProbe(DepEnv{ConfigGet: func(string) string { return "" }})
	p.statFile = func(string) bool { return false }
	p.runCommand = func(string, []string, time.Duration) (string, bool) { return "", false }
	p.ioWait = make(chan struct{})
	p.Start()
	waitIO(t, p)

	byName := map[string]DepResult{}
	for _, r := range p.Snapshot() {
		byName[r.Name] = r
	}
	for _, name := range []string{"PowerShell 7", "Python", "Node.js", "浏览器内核", "搜索 API", "视觉 API"} {
		if got, ok := byName[name]; !ok || got.State != DepStateUnconfigured {
			t.Fatalf("%s 状态 = %+v", name, got)
		}
	}
	if got := byName["受控根表"]; got.State != DepStateUnusable {
		t.Fatalf("空根表应 unusable，实际 %+v", got)
	}
	if got := byName["自举链产物"]; got.State != DepStateUnconfigured || !strings.Contains(got.Detail, "未定位仓库根") {
		t.Fatalf("自举链产物 = %+v", got)
	}
}

// TestDepsSortOrder 排序：required → optional → dev，同级按名称。
func TestDepsSortOrder(t *testing.T) {
	p := NewDepProbe(DepEnv{ConfigGet: func(string) string { return "" }})
	p.statFile = func(string) bool { return true }
	p.runCommand = func(string, []string, time.Duration) (string, bool) { return "v1", true }
	p.ioWait = make(chan struct{})
	p.Start()
	waitIO(t, p)

	last := -1
	for _, r := range p.Snapshot() {
		order := levelOrder(r.Level)
		if order < last {
			t.Fatalf("排序错乱：%s(%s) 出现在更高级别条目之后", r.Name, r.Level)
		}
		last = order
	}
}

// TestDepsPathsProbe 路径探测：存在即就绪、缺失逐个列出。
func TestDepsPathsProbe(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "present.txt")
	if err := os.WriteFile(present, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.txt")
	p := NewDepProbe(DepEnv{
		ConfigGet: func(string) string { return "" },
		Roots:     []RootPath{{ID: "workspace", Path: dir}},
	})
	p.ioWait = make(chan struct{})
	p.Start()
	waitIO(t, p)
	byName := map[string]DepResult{}
	for _, r := range p.Snapshot() {
		byName[r.Name] = r
	}
	got := byName["受控根表"]
	if got.State != DepStateOK || !strings.Contains(got.Detail, "路径就绪（1 项）") {
		t.Fatalf("受控根表 = %+v", got)
	}

	// 缺失面
	job := depJob{level: DepLevelRequired, name: "受控根表", kind: "paths", paths: []string{present, missing}, probedAt: 1}
	res := p.runPathsProbe(job)
	if res.State != DepStateMissing || !strings.Contains(res.Detail, missing) {
		t.Fatalf("缺失探测 = %+v", res)
	}
}

// TestDepsCommandProbe 命令探测：首行版本 / 绝对路径缺失 / 执行失败。
func TestDepsCommandProbe(t *testing.T) {
	p := NewDepProbe(DepEnv{})
	p.statFile = func(string) bool { return true }
	p.runCommand = func(string, []string, time.Duration) (string, bool) {
		return "Python 3.12.1\nsecond line", true
	}
	job := depJob{level: DepLevelRequired, name: "Python", kind: "command", command: "python", probedAt: 1}
	res := p.runCommandJob(job)
	if res.State != DepStateOK || res.Detail != "Python 3.12.1" {
		t.Fatalf("命令探测 = %+v", res)
	}

	p.statFile = func(string) bool { return false }
	job.command = `D:\no\such\python.exe`
	if res := p.runCommandJob(job); res.State != DepStateMissing || !strings.Contains(res.Detail, "文件不存在") {
		t.Fatalf("绝对路径缺失 = %+v", res)
	}

	p.statFile = func(string) bool { return true }
	p.runCommand = func(string, []string, time.Duration) (string, bool) { return "", false }
	if res := p.runCommandJob(job); res.State != DepStateUnusable || !strings.Contains(res.Detail, "执行失败") {
		t.Fatalf("执行失败 = %+v", res)
	}
}

// TestDepsFileVersionProbe PE 版本探测三态。
func TestDepsFileVersionProbe(t *testing.T) {
	p := NewDepProbe(DepEnv{})
	p.statFile = func(string) bool { return true }
	job := depJob{level: DepLevelRequired, name: "浏览器内核", kind: "filever", paths: []string{"x.exe"}, probedAt: 1}
	p.fileVer = func(string) (string, bool) { return "120.0.1", true }
	if res := p.runFileVersionJob(job); res.State != DepStateOK || res.Detail != "120.0.1" {
		t.Fatalf("版本 = %+v", res)
	}
	p.fileVer = func(string) (string, bool) { return "", true }
	if res := p.runFileVersionJob(job); res.Detail != "文件就绪（无版本信息）" {
		t.Fatalf("无版本 = %+v", res)
	}
	p.fileVer = func(string) (string, bool) { return "", false }
	if res := p.runFileVersionJob(job); res.State != DepStateUnusable {
		t.Fatalf("读版本失败 = %+v", res)
	}
	p.statFile = func(string) bool { return false }
	if res := p.runFileVersionJob(job); res.State != DepStateMissing {
		t.Fatalf("文件缺失 = %+v", res)
	}
}

// TestExpandRootPath 受控根寻址展开与盘符豁免。
func TestExpandRootPath(t *testing.T) {
	roots := []RootPath{{ID: "workspace", Path: `D:\Mau\WorkSpace`}, {ID: "ccbp", Path: `D:\Mau\CatCatBigParty`}}
	got, ok := expandRootPath("ccbp:L1/Tree.md", roots)
	want := filepath.Join(`D:\Mau\CatCatBigParty`, filepath.FromSlash("L1/Tree.md"))
	if !ok || got != want {
		t.Fatalf("展开 = %q ok=%v，期望 %q", got, ok, want)
	}
	if got, ok := expandRootPath(`C:\Tools\x.exe`, roots); !ok || got != `C:\Tools\x.exe` {
		t.Fatalf("盘符形态应原样 = %q ok=%v", got, ok)
	}
	if _, ok := expandRootPath("nope:rel", roots); ok {
		t.Fatal("未知根应失败")
	}
}
