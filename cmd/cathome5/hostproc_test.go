package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHostPathUnder 路径归属判据——分隔符边界 + 大小写不敏感（同名前缀目录不算）。
func TestHostPathUnder(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "MauOut5")
	cases := []struct {
		exe  string
		want bool
	}{
		{filepath.Join(target, "cathome5.exe"), true},
		{filepath.Join(target, "sub", "cathome5.exe"), true},
		{filepath.Join(dir, "MauOut5_old", "cathome5.exe"), false},
		{filepath.Join(dir, "MauOut5x", "cathome5.exe"), false},
		{filepath.Join(dir, "cathome5.exe"), false},
	}
	for _, c := range cases {
		if got := hostPathUnder(c.exe, target); got != c.want {
			t.Fatalf("hostPathUnder(%q, %q) = %v，期望 %v", c.exe, target, got, c.want)
		}
	}
	// 大小写不敏感（Windows 路径语义）
	if !hostPathUnder(strings.ToUpper(filepath.Join(target, "CATHOME5.EXE")), target) {
		t.Fatal("路径判定应大小写不敏感")
	}
}

// TestListHostProcessesSmoke 枚举冒烟——本机可用（tasklist + 路径查询）且不含当前进程。
func TestListHostProcessesSmoke(t *testing.T) {
	hosts, err := listHostProcesses(nil)
	if err != nil {
		t.Fatalf("宿主进程枚举失败：%v", err)
	}
	for _, host := range hosts {
		if host.PID == os.Getpid() {
			t.Fatalf("枚举不应包含当前进程 pid %d", host.PID)
		}
		if !strings.EqualFold(filepath.Base(host.Exe), hostImageName) {
			t.Fatalf("枚举只应返回 %s，got %q", hostImageName, host.Exe)
		}
	}
}

// TestWaitProcessGoneMS 进程退出判据——当前进程在短窗口内不退（false）；不存在的 pid 视为已退出（true）。
func TestWaitProcessGoneMS(t *testing.T) {
	if waitProcessGoneMS(os.Getpid(), 100) {
		t.Fatal("当前进程不应在 100 ms 内被判为已退出")
	}
	if !waitProcessGoneMS(1<<30, 100) {
		t.Fatal("不存在的 pid 应视为已退出")
	}
}

// TestSweepHostsInDirectorySmoke 清扫冒烟——空目录命中 0（真实枚举，不结束任何进程）。
func TestSweepHostsInDirectorySmoke(t *testing.T) {
	res, err := sweepHostsInDirectoryImpl(t.TempDir())
	if err != nil {
		t.Fatalf("清扫枚举失败：%v", err)
	}
	if len(res.PIDs) != 0 || res.Graceful != 0 || res.Killed != 0 || res.Failed != 0 {
		t.Fatalf("空目录不应命中实例：%+v", res)
	}
}
