package bricks

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cathome5/mau/fs"
)

// GitFacts 是一次 git 命令的执行事实——exit / stdout / stderr / 截断 / 耗时（展示文案由积木层组装）。
type GitFacts struct {
	// Repo 仓库工作目录（规范绝对路径）
	Repo string
	// Exit 进程退出码（0=成功；-1=启动失败或超时被杀）
	Exit int
	// Stdout 标准输出（按上限截断）
	Stdout string
	// Stderr 标准错误（按上限截断）
	Stderr string
	// Truncated 输出是否触达截断上限
	Truncated bool
	// ElapsedMs 耗时毫秒
	ElapsedMs int64
}

// GitService 是 git 命令执行面——纯命令执行 + 路径边界（对齐 C# GitService；展示文案归积木层）。
//
// 三条硬边界（实现在服务侧，不靠纪律）：
// ① 仓库路径必须落在受控根内（只读根允许读操作，写操作按根 Writable 拒绝）；
// ② 子命令与参数逐项进参数数组（免 shell 拼接 / 免转义）；
// ③ 输出按上限截断，截断事实随事实表可见（不静默丢内容）。
type GitService struct {
	// roots 受控根条目表（全量——读操作允许只读根，写操作按 Writable 判）
	roots []fs.Root
	// base 相对路径基准（对齐 C# Environment.CurrentDirectory；空 = 根表首根）
	base string
}

// gitMaxOutputChars 单路输出截断上限（UTF-16 码元——对齐 C# MaxOutputChars）。
const gitMaxOutputChars = 60000

// gitDefaultTimeoutMs 默认超时毫秒。
const gitDefaultTimeoutMs = 30000

// gitMaxTimeoutMs 超时上限毫秒——与工具执行层统一 120 秒口径。
const gitMaxTimeoutMs = 120000

// NewGitService 建立服务——roots 取全量受控根条目，base 为相对路径基准（空 = 首根）。
func NewGitService(roots []fs.Root, base string) *GitService {
	return &GitService{roots: append([]fs.Root{}, roots...), base: base}
}

// ResolveRepo 解析仓库工作目录——根寻址展开 + 归属校验 + 目录存在 + 写面只读根拒绝。
//
// 返回（规范绝对路径，错误文本）；错误文本空表示成功。错误码与文案逐字对齐 C# GitService。
func (s *GitService) ResolveRepo(path string, forWrite bool) (string, string) {
	if path == "" {
		return "", "ERR|GIT_BAD_ARGS|缺少参数 path（仓库工作目录）"
	}
	if s == nil {
		return "", "ERR|GIT_NO_SERVICE|宿主未注入 IGitService"
	}
	full, root, _, msg, ok := fs.ResolveOwnedRoot(path, s.roots, s.base)
	if !ok {
		return "", "ERR|GIT_BAD_PATH|路径非法: " + msg
	}
	info, err := os.Stat(full)
	if err != nil || !info.IsDir() {
		return "", "ERR|GIT_BAD_PATH|目录不存在：" + full
	}
	if forWrite && !root.Writable {
		return "", "ERR|GIT_ROOT_READONLY|只读根拒绝写操作：" + full + "（根 " + root.ID + " 不可写）"
	}
	return full, ""
}

// Run 执行一次 git 命令——repo 已由 ResolveRepo 解析；args 为 git 子命令与参数（不含 git 本体与 -C）。
//
// 超时（0=默认 30000 / 上限 120000）杀整棵进程树；非零退出只回事实，由积木层判工具失败。
func (s *GitService) Run(repo string, args []string, timeoutMs int) GitFacts {
	facts := GitFacts{Repo: repo}
	effective := timeoutMs
	if effective <= 0 {
		effective = gitDefaultTimeoutMs
	}
	if effective > gitMaxTimeoutMs {
		effective = gitMaxTimeoutMs
	}
	// 全局参数：禁分页器（防挂起）+ 中文路径直出（quotepath 默认转八进制）+ 指定仓库
	global := []string{"--no-pager", "-c", "core.quotepath=false", "-C", repo}
	cmd := exec.Command("git", append(global, args...)...)
	cmd.Dir = repo
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	outW := newCodeUnitCapper(gitMaxOutputChars)
	errW := newCodeUnitCapper(gitMaxOutputChars)
	cmd.Stdout = outW
	cmd.Stderr = errW
	start := time.Now()
	if err := cmd.Start(); err != nil {
		// git 不在 PATH / 起不来——按启动失败事实返回（积木层转 ERR）
		facts.Exit = -1
		facts.Stderr = "git 启动失败: Win32Exception|" + err.Error()
		facts.ElapsedMs = time.Since(start).Milliseconds()
		return facts
	}
	timeout := false
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	select {
	case err := <-waitDone:
		facts.Exit = exitCodeOf(cmd, err)
	case <-time.After(time.Duration(effective) * time.Millisecond):
		timeout = true
		killTree(cmd)
		<-waitDone
	}
	outW.flush()
	errW.flush()
	facts.Stdout = outW.text()
	facts.Stderr = errW.text()
	facts.ElapsedMs = time.Since(start).Milliseconds()
	facts.Truncated = utf16Len(facts.Stdout) >= gitMaxOutputChars || utf16Len(facts.Stderr) >= gitMaxOutputChars
	if timeout {
		facts.Exit = -1
		facts.Stderr = facts.Stderr + "\n[超时] git 未在 " + strconv.Itoa(effective) + " ms 内完成（已杀进程树）"
	}
	return facts
}

// codeUnitCapper 行收集器（UTF-16 码元口径）——按行拼接（行间 \n），达上限后丢弃后续行（对齐 C# AppendCapped）。
type codeUnitCapper struct {
	// buf 已收集正文
	buf strings.Builder
	// max 码元上限
	max int
	// units 已收集码元数
	units int
	// pending 未成行的半截字节
	pending []byte
}

// newCodeUnitCapper 建收集器。
func newCodeUnitCapper(max int) *codeUnitCapper {
	return &codeUnitCapper{max: max}
}

// Write 收字节流——按 \n 切行处理（\r 去尾）。
func (w *codeUnitCapper) Write(p []byte) (int, error) {
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
func (w *codeUnitCapper) flush() {
	if len(w.pending) > 0 {
		w.append(strings.TrimSuffix(string(w.pending), "\r"))
		w.pending = nil
	}
}

// text 取收集结果。
func (w *codeUnitCapper) text() string { return w.buf.String() }

// append 追加一行——达上限即停；整行放不下时按码元截断（对齐 C# AppendLine 后 sb.Length = cap）。
func (w *codeUnitCapper) append(line string) {
	if w.units >= w.max {
		return
	}
	prefix := ""
	if w.units > 0 {
		prefix = "\n"
	}
	need := utf16Len(prefix) + utf16Len(line)
	if w.units+need <= w.max {
		w.buf.WriteString(prefix)
		w.buf.WriteString(line)
		w.units += need
		return
	}
	remain := w.max - w.units - utf16Len(prefix)
	if remain <= 0 {
		w.units = w.max
		return
	}
	w.buf.WriteString(prefix)
	w.buf.WriteString(utf16Truncate(line, remain))
	w.units = w.max
}

// utf16Len 返回 UTF-16 码元数（对齐 C# string.Length）。
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
			continue
		}
		n++
	}
	return n
}

// utf16Truncate 按 UTF-16 码元截断（对齐 C# Substring(0, n)；不切代理对的一半）。
func utf16Truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	n := 0
	for i, r := range s {
		units := 1
		if r > 0xFFFF {
			units = 2
		}
		if n+units > max {
			return s[:i]
		}
		n += units
	}
	return s
}
