//go:build windows

package bricks

// 浏览器内核进程辅助（Windows）——隐藏窗口 / 进程树强杀 / 进程名查询。
//
// Chromium 启动后 detach：启动器进程写完 DevToolsActivePort 即退出，真正的 browser 进程
// 持有 profile 与调试端口——进程句柄只作兜底，主清理手段是 CDP Browser.close。

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// prepareBrowserCommand 隐藏控制台窗口（对齐 C# CreateNoWindow）。
func prepareBrowserCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// killProcessTree 杀进程树——taskkill /T /F（子进程一并终止；失败静默，进程可能已退出）。
func killProcessTree(pid int, proc *os.Process) {
	if pid > 0 {
		killer := exec.Command("taskkill.exe", "/T", "/F", "/PID", strconv.Itoa(pid))
		prepareBrowserCommand(killer)
		_ = killer.Run()
	}
	if proc != nil {
		_ = proc.Kill()
	}
}

// processImageName 取进程映像名（tasklist CSV 首字段；无匹配 / 失败返回空串）。
func processImageName(pid int) string {
	if pid <= 0 {
		return ""
	}
	cmd := exec.Command("tasklist.exe", "/FI", "PID eq "+strconv.Itoa(pid), "/FO", "CSV", "/NH")
	prepareBrowserCommand(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(string(out))
	if !strings.HasPrefix(text, `"`) {
		// 无匹配时 tasklist 回本地化 INFO 行（"INFO:" / "信息:"）——按空处理
		return ""
	}
	if end := strings.Index(text[1:], `"`); end >= 0 {
		return strings.ToLower(text[1 : 1+end])
	}
	return ""
}
