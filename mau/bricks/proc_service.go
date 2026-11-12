package bricks

// ProcService 是进程槽服务——proc 工具组（proc-run / proc-start / proc-stop / proc-list）的执行面。
//
// 语义对齐 C# Mau.Runtime.ProcService：
//   - 与 ts 服务（解释器 + 脚本，stdio 严格协议）分工：本服务管任意受控根内 exe——
//     同步跑到退出（Run）与脱手起进程（Start）。
//   - 三条硬边界（实现在服务侧，不靠纪律）：① 可执行文件必须落在可写受控根内；
//     ② 宿主自身（CatHome4.exe）与 SetUp.exe 硬拦（重启走 restart-*，部署走 mau-setup）；
//     ③ 台账落盘（runtime 目录 procs.json）——起过什么、pid 多少、活没活，proc-list 可查。
//   - 脱手语义：Start 起的进程不随宿主退出清理（属调用方资产，与 ts 服务池相反）。
//
// 参数面零容忍（JsonArgs 口径）：未知键 / 缺必填一律 ERR|BAD_ARGS；服务层错误一律
// ERR|PROC_*|<消息>，失败原因对 Dog 可见（与 C# 同口径）。

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"cathome5/mau/fs"
)

// 服务常量——与 C# ProcService 同值。
const (
	// procDefaultRunTimeoutMs 同步执行默认超时毫秒。
	procDefaultRunTimeoutMs = 30000
	// procMaxRunTimeoutMs 超时上限毫秒——与工具执行层统一 120 秒口径。
	procMaxRunTimeoutMs = 120000
	// procDefaultReadyTimeoutMs 就绪探测默认超时毫秒。
	procDefaultReadyTimeoutMs = 15000
	// procMaxOutputChars 单路输出截断上限（UTF-16 码元——对齐 C# MaxOutputChars）。
	procMaxOutputChars = 60000
	// procForbiddenHostExe 硬拦的可执行文件名——宿主自身（重启走 restart-*）。
	procForbiddenHostExe = "CatHome4.exe"
	// procForbiddenSetupExe 硬拦的可执行文件名——一键部署链（部署走 mau-setup）。
	procForbiddenSetupExe = "SetUp.exe"
	// procLedgerFileName 台账文件名。
	procLedgerFileName = "procs.json"
)

// ProcService 是进程槽执行面——同步执行 / 脱手起进程 / 停进程 / 台账清单。
type ProcService struct {
	// roots 可写受控根条目表（构造时过滤——可执行文件路径校验面）
	roots []fs.Root
	// runtimeDir 台账落点目录（runtime；空 = 回落系统临时目录并出声）
	runtimeDir string
	// logf 日志出口（level 0=debug 1=info 2=warn 3=err；nil = 不出声）
	logf func(level int, message string)
	// mu 台账读写锁——同一时刻只有一路读写（对齐 C# lock (_gate)）
	mu sync.Mutex
}

// NewProcService 构造服务——只收 writable=true 的受控根（对齐 C# ProcService 构造口径）。
//
// runtimeDir 为台账落点（数据根下 runtime）；logf 为日志出口（nil = 不出声）。
func NewProcService(roots []fs.Root, runtimeDir string, logf func(level int, message string)) *ProcService {
	writable := make([]fs.Root, 0, len(roots))
	for _, root := range roots {
		if !root.Writable || root.Path == "" {
			continue
		}
		writable = append(writable, root)
	}
	return &ProcService{roots: writable, runtimeDir: runtimeDir, logf: logf}
}

// report 出声（logf 为 nil 时静默——日志出口缺失不阻断业务）。
func (s *ProcService) report(level int, message string) {
	if s == nil || s.logf == nil {
		return
	}
	s.logf(level, message)
}

// Run 同步执行一个 exe 到退出（起-跑-退）——跑测用；超时杀整棵进程树。
//
// 回执 JSON：exit / timeout / stdout / stderr / stdoutLines / stderrLines / truncated / elapsedMs / path。
func (s *ProcService) Run(argsJSON string) string {
	if bad := Validate(argsJSON, "path args cwd timeout_ms", "path", "", ""); bad != "" {
		return bad
	}
	pathArg := Get(argsJSON, "path")
	if pathArg == "" || pathArg == ParseFail {
		return "ERR|PROC_BAD_ARGS|缺少参数 path（受控根内 .exe 路径）"
	}
	base, _ := os.Getwd()
	exe, pathErr := s.resolveExe(pathArg, base)
	if pathErr != "" {
		return pathErr
	}
	argsRaw := Get(argsJSON, "args")
	cwd := Get(argsJSON, "cwd")
	timeoutMs := procParseTimeout(Get(argsJSON, "timeout_ms"), procDefaultRunTimeoutMs, procMaxRunTimeoutMs)

	cmd := exec.Command(exe)
	attr := &syscall.SysProcAttr{HideWindow: true}
	procApplyArgs(cmd, exe, argsRaw, attr, s.report)
	cmd.SysProcAttr = attr
	if cwd != "" && cwd != ParseFail {
		if info, err := os.Stat(cwd); err == nil && info.IsDir() {
			cmd.Dir = cwd
		}
	}

	outW := newProcLineCapper(procMaxOutputChars)
	errW := newProcLineCapper(procMaxOutputChars)
	cmd.Stdout = outW
	cmd.Stderr = errW

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return "ERR|PROC_LAUNCH|" + procErrorName(err) + "|" + err.Error()
	}
	exit := 0
	timeout := false
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	select {
	case waitErr := <-waitDone:
		exit = exitCodeOf(cmd, waitErr)
	case <-time.After(time.Duration(timeoutMs) * time.Millisecond):
		timeout = true
		killTree(cmd)
		<-waitDone
		exit = -1
	}
	outW.flush()
	errW.flush()
	elapsed := time.Since(start).Milliseconds()
	outText := outW.text()
	errText := errW.text()

	var b strings.Builder
	b.WriteString(`{"exit":`)
	b.WriteString(strconv.Itoa(exit))
	b.WriteString(`,"timeout":`)
	b.WriteString(strconv.FormatBool(timeout))
	b.WriteString(`,"stdout":`)
	b.WriteString(jsonString(outText))
	b.WriteString(`,"stderr":`)
	b.WriteString(jsonString(errText))
	b.WriteString(`,"stdoutLines":`)
	b.WriteString(strconv.Itoa(procCountLines(outText)))
	b.WriteString(`,"stderrLines":`)
	b.WriteString(strconv.Itoa(procCountLines(errText)))
	b.WriteString(`,"truncated":`)
	b.WriteString(strconv.FormatBool(utf16Len(outText) >= procMaxOutputChars || utf16Len(errText) >= procMaxOutputChars))
	b.WriteString(`,"elapsedMs":`)
	b.WriteString(strconv.FormatInt(elapsed, 10))
	b.WriteString(`,"path":`)
	b.WriteString(jsonString(exe))
	b.WriteByte('}')
	return b.String()
}

// Start 脱手起一个常驻进程并登记台账——起面板 / 游戏 / 外部工具用。
//
// window=hidden 只保证「不弹控制台」（对齐 C#：GUI 程序自身窗口不由此参数控制）；
// ready=port:<n> 时轮询 127.0.0.1:<n> 直到连通或超时——超时不杀进程，回执 ready=false。
// 回执 JSON：id / pid / started / ready / port / elapsedMs / path。
func (s *ProcService) Start(argsJSON string) string {
	if bad := Validate(argsJSON, "path args cwd window ready ready_timeout_ms", "path", "", ""); bad != "" {
		return bad
	}
	pathArg := Get(argsJSON, "path")
	if pathArg == "" || pathArg == ParseFail {
		return "ERR|PROC_BAD_ARGS|缺少参数 path（受控根内 .exe 路径）"
	}
	base, _ := os.Getwd()
	exe, pathErr := s.resolveExe(pathArg, base)
	if pathErr != "" {
		return pathErr
	}
	argsRaw := Get(argsJSON, "args")
	cwd := Get(argsJSON, "cwd")
	window := Get(argsJSON, "window")
	if window == "" || window == ParseFail {
		window = "hidden"
	}
	if window != "hidden" && window != "normal" {
		return "ERR|PROC_BAD_ARGS|window 非法值: " + window + "（hidden | normal）"
	}
	readyArg := Get(argsJSON, "ready")
	readyPort := 0
	if readyArg != "" && readyArg != ParseFail {
		if !strings.HasPrefix(readyArg, "port:") {
			return "ERR|PROC_BAD_ARGS|ready 非法形态: " + readyArg + "（第一版只支持 port:<n>）"
		}
		parsed, err := strconv.Atoi(readyArg[5:])
		if err != nil || parsed <= 0 || parsed > 65535 {
			return "ERR|PROC_BAD_ARGS|ready 端口非法: " + readyArg
		}
		readyPort = parsed
	}
	readyTimeoutMs := procParseTimeout(Get(argsJSON, "ready_timeout_ms"), procDefaultReadyTimeoutMs, procMaxRunTimeoutMs)

	cmd := exec.Command(exe)
	attr := &syscall.SysProcAttr{HideWindow: window == "hidden"}
	procApplyArgs(cmd, exe, argsRaw, attr, s.report)
	cmd.SysProcAttr = attr
	if cwd != "" && cwd != ParseFail {
		if info, err := os.Stat(cwd); err == nil && info.IsDir() {
			cmd.Dir = cwd
		}
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return "ERR|PROC_LAUNCH|" + procErrorName(err) + "|" + err.Error()
	}
	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	// 脱手：不阻塞等待；回收句柄的 Wait 走后台（不杀进程、不影响台账）
	go func() { _ = cmd.Wait() }()
	ready := false
	if readyPort > 0 {
		ready = procWaitPort(readyPort, readyTimeoutMs)
	}
	elapsed := time.Since(start).Milliseconds()

	entry := procEntry{
		ID:        "p" + strconv.Itoa(pid),
		Path:      exe,
		Args:      procRawOrEmpty(argsRaw),
		Cwd:       procRawOrEmpty(cwd),
		Pid:       pid,
		StartedAt: time.Now().Format("2006-01-02 15:04:05"),
		Window:    window,
		ReadyPort: readyPort,
		Ready:     ready,
	}
	s.mu.Lock()
	ledger := s.loadLedgerLocked()
	ledger = append(ledger, entry)
	s.saveLedgerLocked(ledger)
	s.mu.Unlock()

	var b strings.Builder
	b.WriteString(`{"id":`)
	b.WriteString(jsonString(entry.ID))
	b.WriteString(`,"pid":`)
	b.WriteString(strconv.Itoa(pid))
	b.WriteString(`,"started":`)
	b.WriteString(strconv.FormatBool(pid > 0))
	b.WriteString(`,"ready":`)
	b.WriteString(strconv.FormatBool(ready))
	b.WriteString(`,"port":`)
	b.WriteString(strconv.Itoa(readyPort))
	b.WriteString(`,"elapsedMs":`)
	b.WriteString(strconv.FormatInt(elapsed, 10))
	b.WriteString(`,"path":`)
	b.WriteString(jsonString(exe))
	b.WriteByte('}')
	return b.String()
}

// Stop 停进程——优雅（close 主窗口 + 等待 3 秒）→ 强杀（整棵进程树）兜底。
//
// id 传台账 id（p<pid>）或 pid 数字；force=true 跳过优雅直接强杀。
// 回执 JSON：id / pid / stopped / mode（graceful | kill | none）。
func (s *ProcService) Stop(argsJSON string) string {
	if bad := Validate(argsJSON, "id force", "id", "", ""); bad != "" {
		return bad
	}
	idArg := Get(argsJSON, "id")
	if idArg == "" || idArg == ParseFail {
		return "ERR|PROC_BAD_ARGS|缺少参数 id（台账 id 或 pid）"
	}
	force := strings.EqualFold(Get(argsJSON, "force"), "true")
	pid := 0
	entryID := idArg
	if strings.HasPrefix(idArg, "p") {
		pid, _ = strconv.Atoi(idArg[1:])
	} else {
		pid, _ = strconv.Atoi(idArg)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ledger := s.loadLedgerLocked()
	if pid <= 0 {
		for _, entry := range ledger {
			if entry.ID == idArg {
				pid = entry.Pid
				entryID = entry.ID
				break
			}
		}
	}
	if pid <= 0 {
		return "ERR|PROC_NOT_FOUND|台账内无此 id 且非数字 pid：" + idArg
	}
	if !procProcessAlive(pid) {
		ledger = procRemoveEntry(ledger, pid)
		s.saveLedgerLocked(ledger)
		var b strings.Builder
		b.WriteString(`{"id":`)
		b.WriteString(jsonString(entryID))
		b.WriteString(`,"pid":`)
		b.WriteString(strconv.Itoa(pid))
		b.WriteString(`,"stopped":false,"mode":"none","note":"进程已不存在"}`)
		return b.String()
	}
	mode := "graceful"
	stopped := false
	if !force {
		if procGracefulStop(pid) {
			stopped = procWaitExit(pid, 3000)
		}
	}
	if !stopped {
		mode = "kill"
		procKillTree(pid)
		stopped = procWaitExit(pid, 5000)
	}
	if stopped {
		ledger = procRemoveEntry(ledger, pid)
		s.saveLedgerLocked(ledger)
	}
	var b strings.Builder
	b.WriteString(`{"id":`)
	b.WriteString(jsonString(entryID))
	b.WriteString(`,"pid":`)
	b.WriteString(strconv.Itoa(pid))
	b.WriteString(`,"stopped":`)
	b.WriteString(strconv.FormatBool(stopped))
	b.WriteString(`,"mode":`)
	b.WriteString(jsonString(mode))
	b.WriteByte('}')
	return b.String()
}

// List 台账清单——逐条给路径 / pid / 起时刻 / 活体 / 就绪端口；进程已不存在的条目 alive=false。
//
// 回执 JSON：count / alive / procs 数组。
func (s *ProcService) List(argsJSON string) string {
	if argsJSON != "" {
		if bad := Validate(argsJSON, "", "", "", ""); bad != "" {
			return bad
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ledger := s.loadLedgerLocked()
	var rows strings.Builder
	aliveCount := 0
	for index, entry := range ledger {
		alive := procProcessAlive(entry.Pid)
		if alive {
			aliveCount++
		}
		if index > 0 {
			rows.WriteByte(',')
		}
		rows.WriteString(`{"id":`)
		rows.WriteString(jsonString(entry.ID))
		rows.WriteString(`,"pid":`)
		rows.WriteString(strconv.Itoa(entry.Pid))
		rows.WriteString(`,"alive":`)
		rows.WriteString(strconv.FormatBool(alive))
		rows.WriteString(`,"path":`)
		rows.WriteString(jsonString(entry.Path))
		rows.WriteString(`,"args":`)
		rows.WriteString(jsonString(entry.Args))
		rows.WriteString(`,"cwd":`)
		rows.WriteString(jsonString(entry.Cwd))
		rows.WriteString(`,"window":`)
		rows.WriteString(jsonString(entry.Window))
		rows.WriteString(`,"startedAt":`)
		rows.WriteString(jsonString(entry.StartedAt))
		rows.WriteString(`,"readyPort":`)
		rows.WriteString(strconv.Itoa(entry.ReadyPort))
		rows.WriteString(`,"ready":`)
		rows.WriteString(strconv.FormatBool(entry.Ready))
		rows.WriteByte('}')
	}
	var b strings.Builder
	b.WriteString(`{"count":`)
	b.WriteString(strconv.Itoa(len(ledger)))
	b.WriteString(`,"alive":`)
	b.WriteString(strconv.Itoa(aliveCount))
	b.WriteString(`,"procs":[`)
	b.WriteString(rows.String())
	b.WriteString(`]}`)
	return b.String()
}

// procEntry 是台账条目——一次 proc-start 一条（对齐 C# ProcEntry 字段式模型，字段名保持 PascalCase）。
type procEntry struct {
	// ID 台账 id（p<pid>）
	ID string
	// Path 可执行文件全路径
	Path string
	// Args 命令行参数（原样文本或 JSON 数组原文）
	Args string
	// Cwd 工作目录（空=默认）
	Cwd string
	// Pid 进程 pid
	Pid int
	// StartedAt 起时刻（yyyy-MM-dd HH:mm:ss）
	StartedAt string
	// Window 窗口形态（hidden / normal）
	Window string
	// ReadyPort 就绪探测端口（0=未指定）
	ReadyPort int
	// Ready 起时是否探测到就绪
	Ready bool
}

// ledgerPath 台账落点——runtime 目录下 procs.json；目录未注入时回落系统临时目录并出声。
func (s *ProcService) ledgerPath() string {
	dir := s.runtimeDir
	if dir == "" {
		dir = os.TempDir()
		s.report(2, "运行期目录未注入，台账回落系统临时目录: "+dir)
	}
	return filepath.Join(dir, procLedgerFileName)
}

// loadLedgerLocked 读台账——文件不存在或损坏返回空表（损坏出声，不静默）；坏条目自愈。
func (s *ProcService) loadLedgerLocked() []procEntry {
	path := s.ledgerPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []procEntry{}
		}
		s.report(3, "台账读取失败（按空表处理）: "+err.Error())
		return []procEntry{}
	}
	if strings.TrimSpace(string(data)) == "" {
		return []procEntry{}
	}
	var list []procEntry
	if err := json.Unmarshal(data, &list); err != nil {
		s.report(3, "台账读取失败（按空表处理）: "+err.Error())
		return []procEntry{}
	}
	// 坏条目自愈——pid / id 空（历史版本字段丢失写成 [{}]）不参与展示；出声不静默
	clean := make([]procEntry, 0, len(list))
	dropped := 0
	for _, entry := range list {
		if entry.Pid <= 0 || entry.ID == "" {
			dropped++
			continue
		}
		clean = append(clean, entry)
	}
	if dropped > 0 {
		s.report(2, "台账含 "+strconv.Itoa(dropped)+" 条无效记录（pid/id 空），已忽略")
	}
	return clean
}

// saveLedgerLocked 写台账——临时文件 + 覆盖换名（半写不可见）。
func (s *ProcService) saveLedgerLocked(ledger []procEntry) {
	path := s.ledgerPath()
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			s.report(3, "台账目录创建失败: "+err.Error())
			return
		}
	}
	if ledger == nil {
		ledger = []procEntry{}
	}
	data, err := json.Marshal(ledger)
	if err != nil {
		s.report(3, "台账序列化失败: "+err.Error())
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		s.report(3, "台账写入失败: "+err.Error())
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		s.report(3, "台账写入失败: "+err.Error())
	}
}

// procRemoveEntry 按 pid 移除台账条目。
func procRemoveEntry(ledger []procEntry, pid int) []procEntry {
	out := ledger[:0]
	for _, entry := range ledger {
		if entry.Pid == pid {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// resolveExe 可执行文件路径校验——可写受控根内 + .exe 后缀 + 存在 + 硬拦宿主与 SetUp。
//
// 归一口径对齐 C# ResolveExe：展开失败 → PROC_BAD_PATH；越界（含只读根）→ PROC_OUT_OF_ROOT。
func (s *ProcService) resolveExe(path, base string) (string, string) {
	if s == nil || len(s.roots) == 0 {
		return "", "ERR|PROC_NO_SERVICE|可写受控根为空——进程槽不可用"
	}
	full, _, code, msg, ok := fs.ResolveOwnedRoot(path, s.roots, base)
	if !ok {
		if code == fs.ErrOutOfScope {
			return "", "ERR|PROC_OUT_OF_ROOT|可执行文件超出受控根范围：" + full
		}
		return "", "ERR|PROC_BAD_PATH|路径非法: " + msg
	}
	if !strings.EqualFold(filepath.Ext(full), ".exe") {
		return "", "ERR|PROC_BAD_PATH|第一版只支持 .exe：" + full
	}
	if _, err := os.Stat(full); err != nil {
		return "", "ERR|PROC_NOT_FOUND|文件不存在：" + full
	}
	fileName := filepath.Base(full)
	if strings.EqualFold(fileName, procForbiddenHostExe) || strings.EqualFold(fileName, procForbiddenSetupExe) {
		return "", "ERR|PROC_FORBIDDEN|" + fileName + " 不可经 proc 工具起——宿主重启走 restart-*，部署走 mau-setup"
	}
	return full, ""
}

// procApplyArgs 应用命令行参数——JSON 数组逐项进参数数组（免转义）；纯文本原样进命令行。
//
// 纯文本形态用 SysProcAttr.CmdLine（Windows CreateProcess 语义：保留调用方给的原文），
// 与 C# ProcessStartInfo.Arguments 的原样透传一致。
func procApplyArgs(cmd *exec.Cmd, exe, argsRaw string, attr *syscall.SysProcAttr, report func(level int, message string)) {
	if argsRaw == "" || argsRaw == ParseFail {
		return
	}
	trimmed := strings.TrimSpace(argsRaw)
	if strings.HasPrefix(trimmed, "[") {
		var items []json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &items); err == nil {
			args := make([]string, 0, len(items))
			for _, item := range items {
				var text string
				if err := json.Unmarshal(item, &text); err == nil {
					args = append(args, text)
					continue
				}
				args = append(args, string(item))
			}
			cmd.Args = append(cmd.Args, args...)
			return
		}
		if report != nil {
			report(2, "args 数组解析失败，按纯文本传参")
		}
	}
	quoted := exe
	if strings.ContainsAny(exe, " \t") {
		quoted = `"` + exe + `"`
	}
	attr.CmdLine = quoted + " " + argsRaw
}

// procParseTimeout 解析超时毫秒——空 / 非法用默认值，超上限截到上限。
func procParseTimeout(raw string, defaultMs, maxMs int) int {
	if raw == "" || raw == ParseFail {
		return defaultMs
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed <= 0 {
		return defaultMs
	}
	if parsed > maxMs {
		return maxMs
	}
	return parsed
}

// procRawOrEmpty 台账字段归一——解析失败哨兵按空串落盘（对齐 C# §PARSE_FAIL§ 处置）。
func procRawOrEmpty(raw string) string {
	if raw == ParseFail {
		return ""
	}
	return raw
}

// procCountLines 行数统计——空串 0；否则 \n 计数 + 1（对齐 C# CountLines）。
func procCountLines(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

// procErrorName 取 .NET 异常类名口径——启动失败归 Win32Exception，其余归 Exception。
func procErrorName(err error) string {
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return "Win32Exception"
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return "Win32Exception"
	}
	return "Exception"
}

// procProcessAlive 进程存活判定——tasklist CSV 首字段带引号即存在（无匹配回本地化 INFO 行）。
func procProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	cmd := exec.Command("tasklist.exe", "/FI", "PID eq "+strconv.Itoa(pid), "/FO", "CSV", "/NH")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(out)), `"`)
}

// procGracefulStop 优雅停止——taskkill 不带 /F（对该进程发关闭请求；无窗口进程回非零）。
func procGracefulStop(pid int) bool {
	cmd := exec.Command("taskkill.exe", "/PID", strconv.Itoa(pid))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run() == nil
}

// procKillTree 强杀整棵进程树——taskkill /T /F（复用浏览器侧同一实现，失败静默：进程可能已退出）。
func procKillTree(pid int) {
	killProcessTree(pid, nil)
}

// procWaitExit 等待进程退出——轮询存活（100ms 间隔），超时返回 false。
func procWaitExit(pid, timeoutMs int) bool {
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for time.Now().Before(deadline) {
		if !procProcessAlive(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return !procProcessAlive(pid)
}

// procWaitPort 端口就绪轮询——127.0.0.1:<port> 连接成功即真；总超时到点返回 false。
func procWaitPort(port, timeoutMs int) bool {
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for time.Now().Before(deadline) {
		if procProbeTCP(port, 800) {
			return true
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}

// procProbeTCP 单次 TCP 连通探测。
func procProbeTCP(port, timeoutMs int) bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), time.Duration(timeoutMs)*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// procLineCapper 行收集器——按行拼接（行尾 \r\n，对齐 C# StringBuilder.AppendLine），
// 累计超上限即硬切到上限（对齐 C# AppendCapped 的 sb.Length = cap）。
type procLineCapper struct {
	// buf 已收集正文
	buf strings.Builder
	// max 码元上限
	max int
	// units 已收集码元数
	units int
	// pending 未成行的半截字节
	pending []byte
}

// newProcLineCapper 建收集器。
func newProcLineCapper(max int) *procLineCapper {
	return &procLineCapper{max: max}
}

// Write 收字节流——按 \n 切行处理（\r 去尾）。
func (w *procLineCapper) Write(p []byte) (int, error) {
	w.pending = append(w.pending, p...)
	for {
		idx := bytes.IndexByte(w.pending, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimSuffix(string(w.pending[:idx]), "\r")
		w.pending = w.pending[idx+1:]
		w.append(line)
	}
	return len(p), nil
}

// flush 收尾——末行无换行时补处理。
func (w *procLineCapper) flush() {
	if len(w.pending) > 0 {
		w.append(strings.TrimSuffix(string(w.pending), "\r"))
		w.pending = nil
	}
}

// text 取收集结果（无效 UTF-8 字节按替换符归一——对齐 .NET UTF8 解码）。
func (w *procLineCapper) text() string {
	return strings.ToValidUTF8(w.buf.String(), "\uFFFD")
}

// append 追加一行——行尾 \r\n；累计超上限即切到上限（可能切在行中间）。
func (w *procLineCapper) append(line string) {
	if w.units >= w.max {
		return
	}
	text := line + "\r\n"
	units := utf16Len(text)
	if w.units+units > w.max {
		w.buf.WriteString(utf16Truncate(text, w.max-w.units))
		w.units = w.max
		return
	}
	w.buf.WriteString(text)
	w.units += units
}
