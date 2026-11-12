package bricks

// stdioHost —— stdio 常驻服务进程宿主（C# Mau.Runtime.StdioServiceHost 的 Go 平移）。
//
// ts（node）与 py（python）共用同一协议核心，两者只在「配置解析 + 错误码前缀 + 启动前缀」上有差异：
//   - 严格协议：stdout 一行 JSON；首行 ready 握手（内容不校验，非 §EOF§ 即视为就绪）；stderr 只归日志
//   - 超时不杀进程、清残留（迟到回执按 id 丢弃；下次调用重来）
//   - 进程崩溃下次调用重拉，回执浅合并 restarted
//   - 连续拉起失败达上限 → <PREFIX>_UNAVAILABLE（需重启恢复）
//
// 并发：本结构不带锁——调用方（TsService / PyService）持自己的锁串行化。

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	// stdioEOF 是读线程结束哨兵（对齐 C# StdioServiceHost 的 §EOF§）。
	stdioEOF = "§EOF§"
	// stdioMaxLaunchFailures 是连续拉起失败默认上限——达上限置不可用态（防崩溃风暴）。
	stdioMaxLaunchFailures = 5
)

// stdioHost 是常驻服务进程宿主。
type stdioHost struct {
	// name 服务名（错误文案与日志标签）
	name string
	// errPrefix 错误码前缀（NODE / PY）
	errPrefix string
	// logf 日志出口（level 0=debug 1=info 2=warn 3=err；nil = 不出声）
	logf func(level int, message string)
	// maxLaunchFailures 连续拉起失败上限（0 = 默认 5）
	maxLaunchFailures int

	// prefix 解释器前置参数（py 的 -u；node 无）
	prefix []string
	// exe 解释器全路径
	exe string
	// script 入口脚本全路径
	script string
	// args 附加启动参数
	args []string
	// home 环境目录（--home 传入；空 = 不传）
	home string
	// timeoutMs 单次调用 / 就绪握手超时毫秒
	timeoutMs int
	// env 追加环境变量（测试替身注入用；生产为空）
	env []string

	// cmd 当前进程（nil = 未拉起）
	cmd *exec.Cmd
	// stdin 协议写入端
	stdin io.WriteCloser
	// lines 协议行队列（stdout 读线程喂入）
	lines chan string
	// failCount 连续拉起失败计数
	failCount int
	// dead 进程已退出标志（Wait 线程置位）
	dead int32
	// done 进程退出通知
	done chan struct{}
}

// alive 报告进程是否存活。
func (h *stdioHost) alive() bool {
	return h != nil && h.cmd != nil && atomic.LoadInt32(&h.dead) == 0
}

// processID 返回当前进程号（未拉起为 0）。
func (h *stdioHost) processID() int {
	if h == nil || h.cmd == nil || h.cmd.Process == nil {
		return 0
	}
	return h.cmd.Process.Pid
}

// failures 返回连续拉起失败计数（status 回执用）。
func (h *stdioHost) failures() int {
	if h == nil {
		return 0
	}
	return h.failCount
}

// start 拉起进程并等 ready 握手——调用方持锁。
func (h *stdioHost) start() string {
	limit := h.maxLaunchFailures
	if limit <= 0 {
		limit = stdioMaxLaunchFailures
	}
	if h.failCount >= limit {
		return h.errText("UNAVAILABLE", "服务连续拉起失败达上限（"+h.name+"）——需重启恢复")
	}
	// [段0] 清残留回执——上次进程留下的 §EOF§ / 迟到行不得被本次 ready 握手读成「启动即退出」
	drainLines(h.lines)

	argv := make([]string, 0, len(h.prefix)+len(h.args)+3)
	argv = append(argv, h.prefix...)
	argv = append(argv, h.script)
	argv = append(argv, h.args...)
	if h.home != "" {
		argv = append(argv, "--home", h.home)
	}
	cmd := exec.Command(h.exe, argv...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if dir := filepath.Dir(h.script); dir != "" && dir != "." {
		if stat, statErr := os.Stat(dir); statErr == nil && stat.IsDir() {
			cmd.Dir = dir
		}
	}
	if len(h.env) > 0 {
		cmd.Env = append(os.Environ(), h.env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		h.failCount++
		return h.errText("LAUNCH", err.Error())
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		h.failCount++
		return h.errText("LAUNCH", err.Error())
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		h.failCount++
		return h.errText("LAUNCH", err.Error())
	}
	if err := cmd.Start(); err != nil {
		h.failCount++
		return h.errText("LAUNCH", err.Error())
	}

	h.cmd = cmd
	h.stdin = stdin
	h.lines = make(chan string, 64)
	h.done = make(chan struct{})
	atomic.StoreInt32(&h.dead, 0)

	lines := h.lines
	done := h.done
	go h.readStdout(stdout, lines)
	go h.readStderr(stderr)
	go func() {
		_ = cmd.Wait()
		atomic.StoreInt32(&h.dead, 1)
		close(done)
	}()

	// [段1] ready 握手——首行必须出现（严格协议；内容不校验，对齐 C# 口径）
	timeout := time.Duration(h.timeoutMs) * time.Millisecond
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case first := <-lines:
		if first == stdioEOF {
			h.failCount++
			h.kill()
			return h.errText("START_FAILED", "服务进程启动即退出（"+h.name+"）——请检查脚本与依赖")
		}
		h.failCount = 0
		return ""
	case <-timer.C:
		h.failCount++
		h.kill()
		return h.errText("READY_TIMEOUT", "服务启动未返回 ready（"+h.name+"，"+strconv.Itoa(h.timeoutMs)+" ms）")
	}
}

// stop 停止进程——关 stdin 等退出，5 秒未退强杀进程树。
func (h *stdioHost) stop() string {
	if h == nil {
		return ""
	}
	cmd := h.cmd
	if cmd == nil {
		return ""
	}
	if h.stdin != nil {
		_ = h.stdin.Close()
	}
	if h.done != nil {
		select {
		case <-h.done:
		case <-time.After(5 * time.Second):
			killCmdTree(cmd)
			select {
			case <-h.done:
			case <-time.After(3 * time.Second):
			}
		}
	} else {
		killCmdTree(cmd)
	}
	h.cmd = nil
	h.stdin = nil
	atomic.StoreInt32(&h.dead, 1)
	return ""
}

// kill 杀进程树并清句柄。
func (h *stdioHost) kill() {
	if h.cmd != nil {
		killCmdTree(h.cmd)
	}
	h.cmd = nil
	h.stdin = nil
	atomic.StoreInt32(&h.dead, 1)
}

// invoke 执行一次服务调用——发一行 JSON、读一行 JSON（调用方持锁 = 纯串行）。
//
// 未启动则按需拉起；进程崩溃重拉并在回执标注 restarted（内层对象浅合并）。
// 服务层回执原样返回（调用方按 JSON 解析）；失败面一律 ERR|<PREFIX>_* 文本。
func (h *stdioHost) invoke(payload string) string {
	if h == nil {
		return "ERR|SERVICE_UNAVAILABLE|服务不可用"
	}
	restarted := false
	if !h.alive() {
		if errText := h.start(); errText != "" {
			return errText
		}
		restarted = true
	}
	// [段1] 清残留——超时中断的上一次回执（串行语义下只可能属于上一次）
	drainLines(h.lines)
	if h.stdin == nil {
		return h.errText("NO_PROCESS", "服务进程不可用："+h.name)
	}
	if _, err := io.WriteString(h.stdin, payload+"\n"); err != nil {
		h.failCount++
		return h.errText("WRITE", "写入服务失败（"+h.name+"）: "+err.Error())
	}

	// [段2] 收回执——id 不符即上一次调用的迟到回执，丢弃续读
	requestID := extractJSONID(payload)
	deadline := time.Now().Add(time.Duration(h.timeoutMs) * time.Millisecond)
	line := ""
	for {
		remain := time.Until(deadline)
		if remain < 0 {
			remain = 0
		}
		timer := time.NewTimer(remain)
		select {
		case got := <-h.lines:
			timer.Stop()
			if got == stdioEOF {
				// 与 C# 同口径：EOF 哨兵按普通行返回（读线程结束），调用方可见失败
				line = got
				h.log(2, "服务读线程结束（"+h.name+"）——进程已退出")
				h.failCount++
				if restarted {
					return mergeRestartedReply(line)
				}
				return line
			}
			line = got
		case <-timer.C:
			// 超时不杀——清残留（本次回执迟到即弃），下次调用重来
			drainLines(h.lines)
			h.failCount++
			return h.errText("TIMEOUT", "服务调用超时（"+h.name+"，"+strconv.Itoa(h.timeoutMs)+" ms；进程未杀）")
		}
		if requestID == "" {
			break
		}
		replyID := extractJSONID(line)
		if replyID == "" || replyID == requestID {
			break
		}
		// 迟到回执——出声后丢弃（错配比超时更危险：调用方会把它当本次结果）
		h.log(2, "丢弃迟到回执（"+h.name+"）：请求 id="+requestID+"，回执 id="+replyID)
		line = ""
	}
	h.failCount = 0
	if restarted {
		return mergeRestartedReply(line)
	}
	return line
}

// errText 组装 ERR|<前缀>_<码>|<消息>。
func (h *stdioHost) errText(code, message string) string {
	return "ERR|" + h.errPrefix + "_" + code + "|" + message
}

// readStdout 协议读线程——逐行入队，EOF 追加哨兵。
func (h *stdioHost) readStdout(pipe io.Reader, lines chan string) {
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		lines <- scanner.Text()
	}
	lines <- stdioEOF
}

// readStderr 日志读线程——stderr 不进协议面。
func (h *stdioHost) readStderr(pipe io.Reader) {
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 16*1024), 1024*1024)
	for scanner.Scan() {
		h.log(1, "["+h.name+"] "+scanner.Text())
	}
}

// log 日志出口——nil 安全（测试替身不传 logger）。
func (h *stdioHost) log(level int, message string) {
	if h.logf != nil {
		h.logf(level, message)
	}
}

// drainLines 清空协议行队列（非阻塞）。
func drainLines(lines chan string) {
	if lines == nil {
		return
	}
	for {
		select {
		case <-lines:
			continue
		default:
			return
		}
	}
}

// extractJSONID 取协议行里的 id 文本——非 JSON / 缺 id / 非字符串返回空串。
func extractJSONID(text string) string {
	if text == "" || text == stdioEOF {
		return ""
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &obj); err != nil {
		return ""
	}
	raw, exists := obj["id"]
	if !exists {
		return ""
	}
	var id string
	if err := json.Unmarshal(raw, &id); err != nil {
		return ""
	}
	return id
}

// mergeRestartedReply 首次拉起回执装配——内层为 JSON 对象时浅合并 restarted 字段（保键序）；
// 非对象形态退回包装（回执原文受保护）。
func mergeRestartedReply(reply string) string {
	trimmed := strings.TrimSpace(reply)
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") && len(trimmed) >= 2 {
		inner := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
		if inner == "" {
			return `{"restarted":true}`
		}
		return "{" + inner + `,"restarted":true}`
	}
	return `{"restarted":true,"reply":` + jsonString(reply) + `}`
}

// killCmdTree 杀进程树——taskkill /T /F（子进程一并终止；杀不掉静默：进程可能已退出）。
func killCmdTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	killProcessTree(cmd.Process.Pid, cmd.Process)
}
