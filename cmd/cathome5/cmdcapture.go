package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// 本文件是非交互 CLI 输出双写（对齐 mau:CatHome4/Program.Shell.cs 的 EnableCmdCapture）：
// `--run` 模式下控制台输出原样保留 + 同步落盘 `<数据根>/runs/cmd_<时间戳>.txt`。
//
// 语义与 C# 一致：
//   - 🔴 保留原 stdout——父进程显式重定向（proc-run / SetUp）的捕获链路不受影响，本文件只增落盘副本；
//   - 写失败不阻断宿主——stderr 首次出声并停尝试（失败必须可见，不静默刷屏）；
//   - 写操作加锁——日志行来自多线程（观测 sink / 会话层 / HTTP 面）。

// consoleOut 是进程级控制台输出目标——默认 os.Stdout；非交互模式替换为「stdout + 落盘」双写器。
var consoleOut io.Writer = os.Stdout

// outln 输出一行到控制台（双写模式下副本同步落盘）。
func outln(args ...any) {
	fmt.Fprintln(consoleOut, args...)
}

// cmdCapture 是落盘副本写者（文件 + 写锁 + 首次失败留痕）。
type cmdCapture struct {
	// mu 写锁（跨线程写安全）
	mu sync.Mutex
	// file 落盘文件（Close 后置 nil——之后写入静默丢弃）
	file *os.File
	// path 落盘路径（回执 / 日志用）
	path string
	// failed 首次写失败后停尝试（防失败刷屏）
	failed bool
}

// enableCmdCapture 创建 runs/cmd_<时间戳>.txt 并把 consoleOut 切到双写。
// 返回 nil 表示启用失败（调用方继续走 stdout，stderr 已出声）。
func enableCmdCapture(dataRoot string) *cmdCapture {
	runDir := filepath.Join(dataRoot, "runs")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "[CMD] 输出落盘启用失败（目录不可建）："+err.Error())
		return nil
	}
	path := filepath.Join(runDir, "cmd_"+time.Now().Format("20060102_150405")+".txt")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[CMD] 输出落盘启用失败："+err.Error())
		return nil
	}
	capture := &cmdCapture{file: file, path: path}
	consoleOut = &captureTee{primary: os.Stdout, capture: capture}
	return capture
}

// Path 返回落盘文件路径（空 = 未启用）。
func (c *cmdCapture) Path() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.path
}

// write 写副本——首次失败后停尝试并向 stderr 出声（不吞错、不刷屏）。
func (c *cmdCapture) write(p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.file == nil || c.failed {
		return
	}
	if _, err := c.file.Write(p); err != nil {
		c.failed = true
		fmt.Fprintln(os.Stderr, "[CMD] 输出落盘写失败（后续不再尝试）："+err.Error())
	}
}

// Close 关闭落盘文件（退出收尾；可重入）。
func (c *cmdCapture) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.file != nil {
		_ = c.file.Close()
		c.file = nil
	}
}

// captureTee 双写器——主输出 + 落盘副本（多线程写安全）。
type captureTee struct {
	// primary 主输出（stdout / 父进程重定向管道）
	primary io.Writer
	// capture 落盘副本
	capture *cmdCapture
}

// Write 写两路——主输出错误照常返回；副本错误不影响主链路（内部已留痕）。
func (t *captureTee) Write(p []byte) (int, error) {
	n, err := t.primary.Write(p)
	t.capture.write(p)
	return n, err
}
