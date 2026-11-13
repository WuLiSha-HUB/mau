package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cathome5/host/admin"
	"cathome5/host/config"
	"cathome5/host/session"
	"cathome5/mau/runtime"
)

// 本文件是 D3 宿主内自更新链的宿主侧执行面（对齐 design-ch4-host-restart §三 T3–T5）：
//
//	T3 闸门——停机态 + 全局 Idle（会话 / 工具组 Flow / OA 在途 / 视图待推）→ 执行接力；
//	T4 接力——起部署继电器（同 exe deploy relaunch，detached）→ 确认进程已起 → 收尾窗口 → 退出；
//	T5 由继电器承担（等旧 pid → prepare/deploy → 起新实例 → 探活 → 失败回退，见 deploy.go）。
//
// 闸门上限 30 s + 卡点出声（A262 同因）：停机态无上限 = 无限期拒收输入；失败一律撤销并回执注入。

const (
	// restartGateWait 闸门等待上限——判据持续不满足即撤销重启态并出声。
	restartGateWait = 30 * time.Second
	// restartGateWarnInterval 闸门卡点出声间隔（失败必须可见）。
	restartGateWarnInterval = 5 * time.Second
	// restartRelayProbeWindow 接力进程存活确认窗口（先确认接力者已起，再自杀）。
	restartRelayProbeWindow = time.Second
	// restartFlushWindow 退出前收尾窗口——让最后一帧视图 / 日志写出（SSE 收尾）。
	restartFlushWindow = 700 * time.Millisecond
)

// restartGate 是宿主重启闸门（主循环每帧调用；launched 后不再执行）。
type restartGate struct {
	// state 重启登记面
	state *admin.RestartState
	// sess 默认会话（失败回执注入目标）
	sess *session.Session
	// sessionsFn 全部已建会话（多猫面 Idle 判据；nil = 只判 sess）
	sessionsFn func() []*session.Session
	// runner 帧序宿主（Flow 空闲判据）
	runner *runtime.Runner
	// oa 工单撮合器（在途判据）
	oa *runtime.OA
	// workspace 受控根（mauout5 / workspace 解析）
	workspace *config.WorkspaceConfig
	// dataRoot 数据根（透传新实例）
	dataRoot string
	// port 监听端口（透传新实例）
	port int
	// area 区（dev / deploy——透传新实例）
	area string
	// page 页面形态（chat.html = 对话页实例——透传）
	page string
	// logf 观测出口
	logf func(level int, message, category string)
	// waitStart 闸门等待起点（零值 = 未开始）
	waitStart time.Time
	// warnAt 上一次卡点出声时刻
	warnAt time.Time
	// launched 接力已拉起（退出通道已触发——防二次执行）
	launched bool
}

// newRestartGate 构造重启闸门（装配层一次）。
func newRestartGate(state *admin.RestartState, sess *session.Session, runner *runtime.Runner, oa *runtime.OA,
	workspace *config.WorkspaceConfig, dataRoot string, port int, area, page string,
	logf func(level int, message, category string)) *restartGate {
	return &restartGate{
		state: state, sess: sess, runner: runner, oa: oa, workspace: workspace,
		dataRoot: dataRoot, port: port, area: area, page: page, logf: logf,
	}
}

// Tick 推进闸门——返回 true = 接力已拉起，主循环应走退出通道。
func (g *restartGate) Tick() bool {
	if g == nil || g.launched || g.state == nil {
		return false
	}
	req, ok := g.state.Snapshot()
	if !ok {
		g.waitStart = time.Time{}
		g.warnAt = time.Time{}
		return false
	}
	if g.waitStart.IsZero() {
		g.waitStart = time.Now()
	}
	blocker := g.describeBlocker()
	if blocker == "" {
		if err := g.execute(req); err != nil {
			reason := "重启接力未拉起——" + err.Error()
			g.state.Withdraw(reason)
			g.notifyAbort(reason)
			g.waitStart = time.Time{}
			g.warnAt = time.Time{}
			return false
		}
		g.launched = true
		g.log(1, "重启闸门通过——接力已拉起，宿主收尾退出")
		return true
	}
	waited := time.Since(g.waitStart)
	if waited >= restartGateWait {
		reason := "重启闸门等待超时（" + strconv.Itoa(int(waited.Seconds())) + " s，上限 " +
			strconv.Itoa(int(restartGateWait.Seconds())) + " s）——卡点：" + blocker
		g.state.Withdraw(reason)
		g.notifyAbort(reason)
		g.waitStart = time.Time{}
		g.warnAt = time.Time{}
		return false
	}
	if g.warnAt.IsZero() || time.Since(g.warnAt) >= restartGateWarnInterval {
		g.warnAt = time.Now()
		g.log(2, "重启闸门等待中（"+strconv.Itoa(int(waited.Seconds()))+" s / 上限 "+
			strconv.Itoa(int(restartGateWait.Seconds()))+" s）——卡点："+blocker)
	}
	return false
}

// setSessionsProvider 注入多会话列表（多猫面——全局 Idle 判据遍历全部已建会话；nil = 只判默认会话）。
func (g *restartGate) setSessionsProvider(fn func() []*session.Session) {
	if g == nil {
		return
	}
	g.sessionsFn = fn
}

// describeBlocker 全局 Idle 判据——空串 = 可执行接力；否则逐项列出未就绪卡点。
func (g *restartGate) describeBlocker() string {
	parts := make([]string, 0, 4)
	sessions := []*session.Session{}
	if g.sessionsFn != nil {
		sessions = g.sessionsFn()
	}
	if len(sessions) == 0 && g.sess != nil {
		sessions = append(sessions, g.sess)
	}
	for _, sess := range sessions {
		if sess == nil || sess.IsIdle() {
			continue
		}
		name, _, _ := sess.GetRunState()
		parts = append(parts, "会话「"+sess.DisplayName+"」非 Idle（运行态 "+name+"）")
	}
	if g.runner != nil {
		for _, entry := range g.runner.Entries() {
			busy := make([]string, 0, 2)
			for _, line := range entry.Status.StateLines {
				if !strings.HasSuffix(line, "=Idle") {
					busy = append(busy, line)
				}
			}
			if len(busy) > 0 {
				parts = append(parts, "Flow「"+entry.Name+"」非 Idle（"+strings.Join(busy, " ")+"）")
			}
		}
	}
	if open, work := g.oa.InFlight(); open+work > 0 {
		parts = append(parts, "OA 在途（待认领 "+strconv.Itoa(open)+" / 执行中 "+strconv.Itoa(work)+"）")
	}
	for _, sess := range sessions {
		if sess != nil && sess.ViewBus != nil && sess.ViewBus.HasPendingContent() {
			parts = append(parts, "视图推送仍有待推内容（"+sess.DisplayName+"）")
		}
	}
	return strings.Join(parts, "；")
}

// notifyAbort 失败回执注入——重启未执行原因投给默认会话（system 来源；失败必须可见）。
func (g *restartGate) notifyAbort(reason string) {
	if g.sess == nil {
		return
	}
	text := "[宿主自更新] 重启未执行\n原因: " + reason + "\n宿主继续运行（运行区未改动）。"
	if reply := g.sess.PostSystemMessage(session.SysKindSystemAuto, text, ""); reply != "OK" {
		g.log(2, "重启失败回执注入未受理："+reply)
	}
}

// execute 执行接力（T4）——校验运行区 → 准备接力可执行文件 → 起继电器 → 确认存活 → 收尾窗口。
func (g *restartGate) execute(req admin.RestartRequest) error {
	mode := req.Mode
	if mode == "" {
		mode = "full"
	}
	if mode != "full" && mode != "incr" && mode != "host" {
		return fmt.Errorf("重启模式非法: %s（支持 full / incr / host）", mode)
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("取可执行路径失败: %w", err)
	}
	target := strings.TrimSpace(req.Target)
	if target == "" {
		target = g.resolveMauOut5()
	}
	if target == "" {
		return fmt.Errorf("重启请求缺少目标运行区（target 未提供且受控根 mauout5 未配置）")
	}
	if abs, absErr := filepath.Abs(target); absErr == nil {
		target = abs
	}
	// 判例 2026-11-13（CH4 1.8.266 同因）——目标运行区已被另一宿主实例占用：改名必失败（60×500 ms 全败）
	// 且本宿主会被拉下线。源头拦住：检出即撤销重启态、宿主不下线；不自动杀「可能是另一只正在服务的宿主」。
	if foreign := hostScanForeignInTarget(target, g.logf); foreign != "" {
		return fmt.Errorf("检测到另一宿主实例正在使用目标运行区：%s——请先停止该实例再重启（宿主继续服务，已撤销重启态）", foreign)
	}
	// 模式前置校验——验证不过即不动运行区（对齐 CH4「验证不过即不动运行区」）
	switch mode {
	case "host":
		if _, statErr := os.Stat(filepath.Join(target, "cathome5.exe")); statErr != nil {
			return fmt.Errorf("目标运行区缺 cathome5.exe：%s", target)
		}
	case "incr":
		staging := target + "_staging"
		for _, name := range []string{"cathome5.exe", "version.txt"} {
			if _, statErr := os.Stat(filepath.Join(staging, name)); statErr != nil {
				return fmt.Errorf("增量重启暂存区缺 %s（先跑 deploy prepare）：%s", name, staging)
			}
		}
	case "full":
		if g.resolveRepo() == "" {
			return fmt.Errorf("未找到施工仓（full 模式需 go.mod——可用 CAT5_REPO 指定）")
		}
	}
	relayExe, err := prepareRelayExecutable(exe, target)
	if err != nil {
		return fmt.Errorf("准备接力可执行文件失败: %w", err)
	}
	args := []string{
		"deploy", "relaunch",
		"--wait-pid", strconv.Itoa(os.Getpid()),
		"--target", target,
		"--mode", mode,
		"--port", strconv.Itoa(g.port),
		"--data-root", g.dataRoot,
	}
	if g.area != "" {
		args = append(args, "--area", g.area)
	}
	if g.page != "" {
		args = append(args, "--page", g.page)
	}
	if mode == "full" {
		args = append(args, "--repo", g.resolveRepo())
	}
	if push := strings.TrimSpace(req.Push); push != "" {
		args = append(args, "--push", push)
	}
	cmd := exec.Command(relayExe, args...)
	cmd.Dir = filepath.Dir(relayExe)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000008}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("接力进程启动失败: %w", err)
	}
	relayPID := cmd.Process.Pid
	// 顺序铁律：先确认接力者已起，再自杀——反序即无人拉起（T4）
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.Now().Add(restartRelayProbeWindow)
	for time.Now().Before(deadline) {
		select {
		case waitErr := <-done:
			return fmt.Errorf("接力进程启动后立即退出（exit=%v）", waitErr)
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
	g.log(1, "重启接力已拉起（mode="+mode+"，pid="+strconv.Itoa(relayPID)+"）→ 目标 "+target)
	// SSE 收尾窗口——让最后一帧视图 / 日志写出（等价 C# FlushAllHosts(800)）
	time.Sleep(restartFlushWindow)
	return nil
}

// resolveMauOut5 解析受控根 mauout5 路径（空 = 未配置）。
func (g *restartGate) resolveMauOut5() string {
	if g.workspace == nil {
		return ""
	}
	for _, root := range g.workspace.Roots {
		if strings.EqualFold(root.ID, "mauout5") {
			return root.Path
		}
	}
	return ""
}

// resolveRepo 解析 CAT5 施工仓根——CAT5_REPO > exe 向上 go.mod > workspace/CatHome5。
func (g *restartGate) resolveRepo() string {
	if value := strings.TrimSpace(os.Getenv("CAT5_REPO")); value != "" {
		if isCathome5Repo(value) {
			return value
		}
		return ""
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for i := 0; i < 8; i++ {
			if isCathome5Repo(dir) {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if g.workspace != nil {
		for _, root := range g.workspace.Roots {
			if !strings.EqualFold(root.ID, "workspace") {
				continue
			}
			candidate := filepath.Join(root.Path, "CatHome5")
			if isCathome5Repo(candidate) {
				return candidate
			}
		}
	}
	return ""
}

// isCathome5Repo 判定目录是否为 CAT5 施工仓（go.mod 含 module cathome5）。
func isCathome5Repo(dir string) bool {
	raw, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	return strings.Contains(string(raw), "module cathome5")
}

// prepareRelayExecutable 准备接力可执行文件——宿主自身就在运行区时复制到 <target>_relay
// （运行中的 exe 会锁住目录：直接以 target 内 exe 跑继电器会让原子切换改名必失败）。
func prepareRelayExecutable(exe, target string) (string, error) {
	exeDir := filepath.Dir(exe)
	if !strings.EqualFold(filepath.Clean(exeDir), filepath.Clean(target)) {
		return exe, nil
	}
	relayDir := target + "_relay"
	if err := os.MkdirAll(relayDir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(relayDir, "cathome5.exe")
	_ = os.Remove(dst)
	if err := copyFile(exe, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// copyFile 整文件复制（覆盖写——接力副本每次重启刷新）。
func copyFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, raw, 0o755)
}

// log 记一条 RESTART 类观测日志。
func (g *restartGate) log(level int, message string) {
	if g == nil || g.logf == nil {
		return
	}
	g.logf(level, message, "RESTART")
}
