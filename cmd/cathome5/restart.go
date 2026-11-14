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
	// intent 重启盘上现场（nil = 不落盘——单测实例）
	intent *admin.RestartIntentStore
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

// setIntentStore 接线重启盘上现场（装配层一次；nil = 不落盘）。
func (g *restartGate) setIntentStore(store *admin.RestartIntentStore) {
	if g == nil {
		return
	}
	g.intent = store
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
	blocker := g.describeBlockerFor(req.Scope)
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
	return g.describeBlockerFor("")
}

// describeBlockerFor 按闸门范围列卡点——scope=self 只判发起会话（其它猫 / 其它 Flow 非 Idle 可越过）。
//
// 分级判据（为什么这样分）：
//   - 硬等（任何 scope）：发起会话自身非 Idle（打断自己会丢本轮）、OA 在途（会丢工具回执）、
//     发起会话视图待推（会丢最后一帧）；
//   - 可越过（scope=self）：其它会话非 Idle、其它 Flow 非 Idle —— 0.36.4 起有 pendingRound 续跑兜底，
//     且停机态已拒收新输入（判否现场：Note 引擎与「所有会话同时 Idle」互斥，闸门永不通过）。
func (g *restartGate) describeBlockerFor(scope string) string {
	onlySelf := scope == "self"
	parts := make([]string, 0, 4)
	sessions := []*session.Session{}
	if onlySelf {
		if g.sess != nil {
			sessions = append(sessions, g.sess)
		}
	} else if g.sessionsFn != nil {
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
	if !onlySelf && g.runner != nil {
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
		// 回落：当前运行实例所在目录——面板请求不带 target 时的默认运行区；
		// 无 mauout5 根的环境（MauOut 直跑形态）同样可用（2026-11-14 修：此前一律报「缺少目标运行区」，
		// 面板「重启宿主」按钮在未配 mauout5 根的实例上永远失败）。
		target = filepath.Dir(exe)
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
	// 盘上现场——接力已起即写 launching（旧宿主即将退出；失败由 Tick 的 Withdraw 覆盖写 failed）
	if g.intent != nil {
		if err := g.intent.Launching(os.Getpid(), relayPID); err != nil {
			g.log(2, "重启现场落盘失败（launching）: "+err.Error())
		}
	}
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
	if dir := resolveRepoRoot(); dir != "" {
		return dir
	}
	if g.workspace != nil {
		for _, root := range g.workspace.Roots {
			if !strings.EqualFold(root.ID, "workspace") {
				continue
			}
			// 常见名优先（现行 Mau / 历史 CatHome5），再扫一级子目录——判定认 go.mod 内容，不认目录名
			for _, name := range []string{"Mau", "CatHome5"} {
				candidate := filepath.Join(root.Path, name)
				if isCathome5Repo(candidate) {
					return candidate
				}
			}
			if found := findRepoInWorkspace(root.Path); found != "" {
				return found
			}
		}
	}
	return ""
}

// resolveRepoRoot 包级施工仓解析——CAT5_REPO > exe 向上 go.mod > cwd 向上 go.mod（找不到返回空）。
//
// 与 restartGate.resolveRepo 共用同一判据（isCathome5Repo），区别只在于本函数不依赖 workspace 根表，
// 供 deploy CLI 等无 workspace 上下文的入口使用。
func resolveRepoRoot() string {
	if value := strings.TrimSpace(os.Getenv("CAT5_REPO")); value != "" {
		if isCathome5Repo(value) {
			return value
		}
		return ""
	}
	if exe, err := os.Executable(); err == nil {
		if dir := findRepoUpward(filepath.Dir(exe), 8); dir != "" {
			return dir
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		if dir := findRepoUpward(cwd, 8); dir != "" {
			return dir
		}
	}
	return ""
}

// findRepoUpward 自 dir 起向上查找施工仓（含自身，最多 maxDepth 级）。
func findRepoUpward(dir string, maxDepth int) string {
	for i := 0; i <= maxDepth; i++ {
		if isCathome5Repo(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// findRepoInWorkspace 在 workspace 根下一级子目录里找施工仓（按 go.mod 内容判定，不认目录名）。
func findRepoInWorkspace(root string) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(root, entry.Name())
		if isCathome5Repo(candidate) {
			return candidate
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

// settleRestartIntent 启动期结算重启现场——只改写停在 registered / launching 的现场。
//
// 判据：本次启动带接力回执（--majordomopush）= 受控重启走完（done）；
// 否则现场停在中间态 = 重启链未完成（relay 超时 / 探活失败 / 人工强杀在登记后）→ stale 且出声。
// 已结算的现场（done / failed / stale）不动——保留第一次结论，供事后审计。
func settleRestartIntent(store *admin.RestartIntentStore, pushText, version string, newPID int) (admin.RestartIntent, bool) {
	if store == nil {
		return admin.RestartIntent{}, false
	}
	current, ok := store.Read()
	if !ok {
		return admin.RestartIntent{}, false
	}
	if current.Phase != admin.RestartPhaseRegistered && current.Phase != admin.RestartPhaseLaunching {
		return current, false
	}
	if strings.TrimSpace(pushText) != "" {
		return store.Settle(admin.RestartPhaseDone, version, "", newPID)
	}
	return store.Settle(admin.RestartPhaseStale, version,
		"重启链未完成（本次启动无接力回执——relay 可能超时退出 / 探活失败；运行区未替换或状态未知）", newPID)
}

// restartIntentSummary 现场摘要行（启动日志用——一行说清上一次重启到哪一步）。
func restartIntentSummary(intent admin.RestartIntent) string {
	parts := []string{"重启现场: " + intent.Phase, "mode=" + intent.Mode}
	if intent.Target != "" {
		parts = append(parts, "target="+intent.Target)
	}
	if intent.OldPID > 0 {
		parts = append(parts, "oldPid="+strconv.Itoa(intent.OldPID))
	}
	if intent.RelayPID > 0 {
		parts = append(parts, "relayPid="+strconv.Itoa(intent.RelayPID))
	}
	if intent.NewPID > 0 {
		parts = append(parts, "newPid="+strconv.Itoa(intent.NewPID))
	}
	if intent.Version != "" {
		parts = append(parts, "version="+intent.Version)
	}
	if intent.ElapsedMs > 0 {
		parts = append(parts, "用时 "+strconv.FormatInt(intent.ElapsedMs/1000, 10)+" s")
	}
	if intent.DeployReported {
		if intent.DeployExit == 0 {
			parts = append(parts, "部署 OK")
		} else {
			parts = append(parts, "部署未替换（deployExit="+strconv.Itoa(intent.DeployExit)+"）")
		}
	}
	if intent.Reason != "" {
		parts = append(parts, "原因: "+intent.Reason)
	}
	return strings.Join(parts, " · ")
}

// buildResumeText 续跑注入文本——给断点信息 + 「先核对后继续」纪律（不重头复述、不重复写操作）。
func buildResumeText(info session.PendingRound) string {
	started := time.UnixMilli(info.StartedAt).Format("2006-01-02 15:04:05")
	return "[宿主重启后续跑] 上一轮在「" + session.PendingRoundPhaseName(info.Phase) + "」阶段被宿主重启中断" +
		"（轮次 " + strconv.FormatInt(info.Round, 10) + " · 起于 " + started +
		" · 轮首前文 " + strconv.Itoa(info.DeclIndex) + " 条）。前文完整保留——先核对已完成的动作" +
		"（工具回执 / Note / 待办 / 已落盘产物），再从断点继续完成本轮；不要重头复述、不要重复已完成的写操作。" +
		"完成或判定无需继续时按常规收尾。"
}

// resumePendingRounds 重启后自动续跑在途轮——仅受控重启（带接力回执的实例），上限 1 次。
//
// 三种处置：可续 → 注入「继续」并计 resumed；已续过一次 → 出声 + 注入提示（要求人工判断）计 skipped；
// 非受控重启（崩溃 / 人工启动）→ 只出声不续（防重试死循环）。
func resumePendingRounds(cats *catRegistry, controlled bool, logf func(level int, message string)) (resumed, skipped int) {
	if cats == nil {
		return 0, 0
	}
	for _, sess := range cats.sessions() {
		if sess == nil {
			continue
		}
		info, ok := session.LoadPendingRound(sess.CatKey)
		if !ok {
			continue
		}
		phase := session.PendingRoundPhaseName(info.Phase)
		if !controlled {
			skipped++
			logf(2, "存在未完成的轮（cat="+sess.CatKey+" · 相位 "+phase+" · 尝试 "+strconv.Itoa(info.Attempts)+
				" 次）——本次启动非受控重启，按定案不自动续跑（防重试死循环）")
			continue
		}
		if info.Attempts >= 1 {
			skipped++
			logf(2, "存在未完成的轮（cat="+sess.CatKey+" · 相位 "+phase+
				"）——已自动续跑过一次，按上限 1 次停手，交人工判断")
			reply := sess.PostSystemMessage(session.SysKindRestart,
				"[重启续跑] 上一轮已自动续跑过一次仍未完成（相位 "+phase+"）——按上限停手，请人工确认后续动作。", "")
			if reply != "OK" {
				logf(2, "续跑上限提示注入未受理: "+reply)
			}
			continue
		}
		resumeInfo, ok := sess.TakePendingRoundResume()
		if !ok {
			skipped++
			logf(2, "续跑现场写盘失败（attempts）: cat="+sess.CatKey+"——本轮不自动续跑")
			continue
		}
		if !sess.PostUser(buildResumeText(resumeInfo), session.SourceUser, "", "restart-resume") {
			skipped++
			logf(2, "续跑注入未受理（队列拒绝）: cat="+sess.CatKey)
			continue
		}
		resumed++
		logf(1, "重启续跑已注入（cat="+sess.CatKey+" · 相位 "+phase+" · 轮次 "+
			strconv.FormatInt(resumeInfo.Round, 10)+" · 尝试 "+strconv.Itoa(resumeInfo.Attempts)+"）")
	}
	return resumed, skipped
}
