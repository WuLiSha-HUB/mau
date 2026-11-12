//go:build !windows

package bricks

// 浏览器内核进程辅助（非 Windows）——占位实现（本支宿主只跑 Windows）。
//
// 保留 cross-compile 可编：无 taskkill / tasklist，进程清理退化为 proc.Kill。

import (
	"os"
	"os/exec"
)

// prepareBrowserCommand 非 Windows 无需隐藏窗口。
func prepareBrowserCommand(cmd *exec.Cmd) {}

// killProcessTree 杀进程（无进程树语义；失败静默）。
func killProcessTree(pid int, proc *os.Process) {
	if proc != nil {
		_ = proc.Kill()
	}
}

// processImageName 非 Windows 不做进程名查询（自愈按无进程处理）。
func processImageName(pid int) string { return "" }
