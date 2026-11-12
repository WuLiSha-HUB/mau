// Package fetest 复现宿主的前端测试服务附属——启动时确保本区段端口上有
// CatHome4/html/tests/server.js 在跑（未监听才拉起），宿主退出时结束拉起的子进程。
//
// 对齐 C# Program.EnsureFrontendTestService / StartFrontendNode / KillFrontendChildren：
//   - 端口已监听 → 跳过（多实例保护——开发区与部署区端口集合互不相交，不跨区复用）；
//   - 测试目录解析：显式覆盖 → workspace 的 mau 根（源码区）→ exe 向上找 Mau.sln → exe 同级 html/tests；
//   - node_modules 存在才拉起——缺失记降级可见（C# 走 npm install 自动部署，Go 侧不自举安装，见 Result 注释）；
//   - 进程 cwd 固定系统临时目录（node 长期持有运行区目录句柄会让部署的原子切换改名失败，判例 2026-11-07）；
//   - 注入 FE_HOST_PID（server.js 守望宿主：宿主被强杀时 node 两个轮询周期内自退）。
package fetest

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"cathome5/host/config"
)

// Ensure 的结果状态。
const (
	// StateListening 本区段端口已有服务在监听——跳过拉起（多实例保护）
	StateListening = "listening"
	// StateStarted 已拉起 node server.js
	StateStarted = "started"
	// StateNoDir 未解析到含 server.js 的测试目录（不影响主功能，C# 同样静默返回）
	StateNoDir = "no-dir"
	// StateMissingDeps 测试目录缺 node_modules——C# 会 npm install 后拉起；
	// Go 侧不自举安装（属部署链，P5 才做），记降级让人可处置
	StateMissingDeps = "missing-deps"
	// StateFailed 探测或启动异常
	StateFailed = "failed"
)

// nodeHideWindow 让 node 子进程不弹控制台窗口（对齐 C# CreateNoWindow=true）。
// CREATE_NO_WINDOW（0x08000000）保证无控制台；HideWindow 兜底 GUI 形态。
const nodeHideWindow = 0x08000000

// Config 是前端测试服务附件的装配参数。
type Config struct {
	// Port 前端测试端口（区段值：dev 8069 / deploy 8099）
	Port int
	// Root 显式测试目录覆盖（CAT5_FE_TEST_ROOT——对齐 C# CH4_REPO_ROOT 逃逸口）
	Root string
	// MauRoot workspace 的 mau 根（源码区候选：<mau>/CatHome4/html/tests）
	MauRoot string
	// ExeDir 可执行文件目录（部署区兜底候选：<exeDir>/html/tests）
	ExeDir string
}

// Result 是 Ensure 的回执——State 决定调用方出日志还是记降级。
type Result struct {
	// State 结果状态（State* 常量）
	State string
	// Detail 人读明细（测试目录 / 错误文本 / 端口）
	Detail string
	// Service 本宿主拉起的 node 子进程（nil = 未拉起）
	Service *Service
}

// Service 表示本宿主拉起的前端测试服务子进程。
type Service struct {
	cmd *exec.Cmd
}

// Ensure 探测端口并在未监听时拉起 node server.js。
//
// 返回的 Service 非 nil 时，调用方须在宿主退出路径调用 Stop。
func Ensure(cfg Config) Result {
	if cfg.Port <= 0 {
		return Result{State: StateFailed, Detail: "前端测试端口未配置"}
	}
	if portListening(cfg.Port) {
		return Result{State: StateListening, Detail: "127.0.0.1:" + strconv.Itoa(cfg.Port)}
	}
	dir := ResolveDir(cfg)
	if dir == "" {
		return Result{State: StateNoDir, Detail: "未找到含 server.js 的测试目录"}
	}
	if info, err := os.Stat(filepath.Join(dir, "node_modules")); err != nil || !info.IsDir() {
		return Result{State: StateMissingDeps, Detail: dir + "（node_modules 不存在）"}
	}
	cmd := exec.Command("node", filepath.Join(dir, "server.js"))
	// cwd 中立化——server.js 全程只用 __dirname，不依赖 cwd（判例 2026-11-07）
	cmd.Dir = os.TempDir()
	cmd.Env = append(os.Environ(),
		"FE_TEST_PORT="+strconv.Itoa(cfg.Port),
		"FE_HOST_PID="+strconv.Itoa(os.Getpid()),
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: nodeHideWindow}
	if err := cmd.Start(); err != nil {
		return Result{State: StateFailed, Detail: "node 启动失败：" + err.Error()}
	}
	// 回收子进程句柄（node 常驻；宿主退出时由 Stop / watchdog 结束）
	go func() { _ = cmd.Wait() }()
	return Result{State: StateStarted, Detail: dir, Service: &Service{cmd: cmd}}
}

// ResolveDir 解析前端测试目录（含 server.js 才算命中）；空串 = 无可用目录。
//
// 锚定顺序对齐 C# ResolveFrontendTestDir 的语义（显式覆盖 → 源码区 → 部署区），
// 机制按 Go 侧布局：workspace 的 mau 根优先于「exe 向上找 Mau.sln」。
func ResolveDir(cfg Config) string {
	candidates := make([]string, 0, 4)
	if cfg.Root != "" {
		candidates = append(candidates, cfg.Root)
	}
	if cfg.MauRoot != "" {
		candidates = append(candidates, filepath.Join(cfg.MauRoot, "CatHome4", "html", "tests"))
	}
	if repo := config.FindRepoRoot(cfg.ExeDir); repo != "" {
		candidates = append(candidates, filepath.Join(repo, "CatHome4", "html", "tests"))
	}
	if cfg.ExeDir != "" {
		candidates = append(candidates, filepath.Join(cfg.ExeDir, "html", "tests"))
	}
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "server.js")); err == nil {
			return dir
		}
	}
	return ""
}

// portListening 探测本机回环端口是否已监听（300ms 上限——对齐 C# TcpClient 探测）。
func portListening(port int) bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Stop 结束拉起的 node 进程树（node → vitest 子进程）——对齐 C# proc.Kill(true)。
//
// 与 server.js 的 FE_HOST_PID 守望互补：守望管宿主被强杀，本方法管退出即释放。
func (s *Service) Stop() {
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return
	}
	pid := strconv.Itoa(s.cmd.Process.Pid)
	kill := exec.Command("taskkill", "/T", "/F", "/PID", pid)
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: nodeHideWindow}
	_ = kill.Run()
	_ = s.cmd.Process.Kill()
}
