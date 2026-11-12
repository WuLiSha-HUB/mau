package bricks

// ps_exec —— PowerShell 执行积木（BRIK-PS-001 的 Go 复现）。
//
// 两段职责：
//  ① 词法拦截（业务规则归积木层）：最小面（单命令语句）/ 写文件 / 读文件 / 列目录 /
//     重定向 / 起进程 / 控制台等待——git 前缀仅豁免写文件与列目录词表。
//  ② 执行面：EncodedCommand（UTF-16LE → Base64，免转义）/ UTF-8 输出内建前缀 /
//     受控根 PSDrive 前缀 / 超时杀进程树 / CLIXML 与 ANSI 清洗 / A214 结构化头。

import (
	"bytes"
	"encoding/base64"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"

	"cathome5/mau/fs"
)

const (
	// psMaxStdoutChars stdout 截断上限（回传 LLM 上下文防爆）
	psMaxStdoutChars = 16384
	// psMaxStderrChars stderr 截断上限（排错入口保留较短）
	psMaxStderrChars = 8192
	// psDefaultTimeoutMs 默认超时毫秒
	psDefaultTimeoutMs = 30000
	// psMaxTimeoutMs 超时上限毫秒（与工单/后台执行层统一 120 秒口径）
	psMaxTimeoutMs = 120000
)

// PsExec 复现 ps.exec——先词法拦截（业务规则归积木层），通过后交执行面。
//
// shell：powershell（默认解释器 Windows PowerShell 5.1）/ powershell7（读 config ps.pwsh_path）。
func PsExec(svc *fs.Service, pwshPath func() string, argsJSON, shell string) (string, bool) {
	if argsJSON == "" {
		return "ERR|PS_BAD_ARGS|缺少参数 command", false
	}
	command := Get(argsJSON, "command")
	if command != "" && command != ParseFail {
		if block := detectForbidden(command); block != "" {
			return block, false
		}
	}
	return psExecRun(svc, pwshPath, argsJSON, shell)
}

// detectForbidden 词法拦截检查——空串=放行。
func detectForbidden(command string) string {
	// [段1] git 前缀豁免——仅豁免写文件 / 列目录词表（命令内路径文本会误伤）；最小面与重定向照常
	isGit := isGitCommand(command)
	// [段2] 最小面检查
	if minimal := detectNonMinimal(command); minimal != "" {
		return minimal
	}
	// [段3] 写文件 cmdlet 族（大小写不敏感 + 单词边界）；git 豁免
	if !isGit {
		writeCmdlets := []string{
			"Set-Content", "Add-Content", "Out-File", "New-Item", "Copy-Item",
			"Move-Item", "Remove-Item", "Rename-Item", "Clear-Content",
			"Set-ItemProperty", "Set-Content-Encoding", "Export-Csv", "Export-Clixml",
			"File.WriteAllText", "File.WriteAllLines", "File.WriteAllBytes",
			"File.AppendAllText", "File.Copy", "File.Move", "File.Delete", "File.Create",
			"IO.File.WriteAllText", "IO.File.WriteAllLines", "IO.File.WriteAllBytes",
		}
		for _, w := range writeCmdlets {
			if containsWord(command, w) {
				return "ERR|PS_WRITE_FORBIDDEN|命令含文件写语义（" + w + "）——文件操作请走 text-* 读写工具（text-write/append/replace/move/delete）"
			}
		}
		// [段3b] 读文件语义——文件内容读取一律走 text-*（字节级读取例外保留）
		readApis := []string{
			"File.ReadAllText", "File.ReadAllLines", "File.OpenText", "File.OpenRead", "File.Open",
			"IO.File.ReadAllText", "IO.File.ReadAllLines", "IO.File.OpenText", "IO.File.OpenRead",
			"Get-Content",
		}
		for _, w := range readApis {
			if containsWord(command, w) {
				return "ERR|PS_READ_FILE_FORBIDDEN|命令含文件读取语义（" + w + "）——文件内容读取请走 text-* 工具（text-read / text-read_between / text-read_lines）"
			}
		}
		// [段3c] 目录列举语义——段首命令位判定（参数名 / 路径中的同形词不误伤）；git 豁免同族
		listCmdlets := []string{"Get-ChildItem", "gci", "ls", "dir"}
		for _, w := range listCmdlets {
			if startsWithWord(strings.TrimLeft(command, " \t\r\n"), w) {
				return listForbidden(w)
			}
		}
		if startsWithWord(strings.TrimLeft(command, " \t\r\n"), "tree") {
			return listForbidden("tree")
		}
	}
	// [段4] 重定向操作符——语义判定（引号内文本 / 箭头 / 比较符放行）
	if hasRedirectOperator(command) {
		return "ERR|PS_WRITE_FORBIDDEN|命令含重定向操作符 > ——文件操作请走 text-* 读写工具"
	}
	// [段5] Start-Process / Start-Job——无交互控制台主循环不 pump
	if containsWord(command, "Start-Process") || containsWord(command, "Start-Job") {
		return "ERR|PS_START_FORBIDDEN|命令含 Start-Process/Start-Job——禁止在工具内启动新进程（宿主进程管理面）"
	}
	// [段6] ReadKey / ReadLine / Read-Host——自动化环境控制台等待会死锁
	if containsWord(command, "ReadKey") || containsWord(command, "ReadLine") || containsWord(command, "Read-Host") {
		return "ERR|PS_READ_FORBIDDEN|命令含 ReadKey/ReadLine/Read-Host——自动化环境控制台等待会死锁"
	}
	return ""
}

// isGitCommand git 前缀判定——仅豁免写文件 / 列目录词表。
func isGitCommand(command string) bool {
	trimmed := strings.TrimLeft(command, " \t\r\n")
	if len(trimmed) >= 4 && strings.EqualFold(trimmed[:4], "git ") {
		return true
	}
	if len(trimmed) >= 8 && strings.EqualFold(trimmed[:8], "git.exe ") {
		return true
	}
	return false
}

// detectNonMinimal 最小面检查——一次调用只允许一个命令语句（引号感知扫描）。
func detectNonMinimal(command string) string {
	trimmed := strings.TrimLeft(command, " \t\r\n")
	keywords := []string{
		"foreach", "for", "while", "do", "until", "if", "elseif", "else", "switch",
		"try", "catch", "finally", "function", "filter", "param", "trap",
		"begin", "process", "end", "break", "continue", "return", "throw",
	}
	for _, w := range keywords {
		if startsWithWord(trimmed, w) {
			return "ERR|PS_BLOCK_FORBIDDEN|命令为过程语句（" + w + "）——最小面规则：一次调用只发一个命令，不要流程控制；需要判断请取数后自行推理"
		}
	}
	codeCmds := []string{
		"Invoke-Expression", "iex", "Invoke-Command", "Add-Type", "New-Object", "Set-Item", "New-ItemProperty",
	}
	for _, w := range codeCmds {
		if startsWithWord(trimmed, w) {
			return "ERR|PS_CODE_FORBIDDEN|命令为代码注入形态（" + w + "）——powershell 仅支持单行指令（命令 + 字面量参数）"
		}
	}
	if startsWithAssignment(trimmed) {
		return "ERR|PS_ASSIGN_FORBIDDEN|命令为变量赋值——最小面规则：不允许过程状态；请直接调用命令并用其输出"
	}
	inSingle, inDouble := false, false
	for i := 0; i < len(command); i++ {
		c := command[i]
		if inSingle {
			if c == '\'' {
				if i+1 < len(command) && command[i+1] == '\'' {
					i++
					continue
				}
				inSingle = false
			}
			continue
		}
		if inDouble {
			if c == '`' {
				i++
				continue
			}
			if c == '"' {
				inDouble = false
			}
			continue
		}
		if c == '\'' {
			inSingle = true
			continue
		}
		if c == '"' {
			inDouble = true
			continue
		}
		if c == '`' {
			i++
			continue
		}
		if c == ';' {
			return multiErr(";")
		}
		if c == '|' {
			if i+1 < len(command) && command[i+1] == '|' {
				return multiErr("||")
			}
			return "ERR|PS_PIPE_FORBIDDEN|命令含管道（|）——最小面规则：不要输出后处理；请直接调用取数命令，用完整输出自行判断"
		}
		if c == '&' && i+1 < len(command) && command[i+1] == '&' {
			return multiErr("&&")
		}
		if c == '{' || c == '}' {
			return "ERR|PS_BLOCK_FORBIDDEN|命令含块标记（" + string(c) + "）——最小面规则：不要脚本块；请拆成多次工具调用"
		}
		if c == '[' || c == ']' || c == '(' || c == ')' || c == '$' {
			return "ERR|PS_CODE_FORBIDDEN|命令含代码形态（" + string(c) + "）——powershell 仅支持单行指令（命令 + 字面量参数）：禁类型引用 / 方法调用 / 表达式 / 变量"
		}
		if c == '\n' || c == '\r' {
			tailOnly := true
			for j := i + 1; j < len(command); j++ {
				if !isSpaceByte(command[j]) {
					tailOnly = false
					break
				}
			}
			if tailOnly {
				break
			}
			return multiErr("换行")
		}
	}
	return ""
}

// multiErr 多段拦截文案——统一指引。
func multiErr(token string) string {
	return "ERR|PS_MULTI_FORBIDDEN|命令含多段（" + token + "）——最小面规则：一次调用只发一个命令；请拆成多次工具调用（同一轮可并发提交）"
}

// listForbidden 目录列举拦截文案。
func listForbidden(hit string) string {
	return "ERR|PS_LIST_FORBIDDEN|命令含目录列举语义（" + hit + "）——列目录请使用 file-tree 工具（按文件名查找用 file-find）"
}

// startsWithWord 段首词匹配——关键字作为独立单词出现在开头。
func startsWithWord(text, word string) bool {
	if len(text) < len(word) || !strings.EqualFold(text[:len(word)], word) {
		return false
	}
	if len(text) == len(word) {
		return true
	}
	return !isWordByte(text[len(word)])
}

// startsWithAssignment 段首变量赋值判定——变量名后跟单个等号（双等号不算）。
func startsWithAssignment(text string) bool {
	if len(text) == 0 || text[0] != '$' {
		return false
	}
	i := 1
	for i < len(text) && isVariableByte(text[i]) {
		i++
	}
	if i == 1 {
		return false
	}
	for i < len(text) && isSpaceByte(text[i]) {
		i++
	}
	if i >= len(text) || text[i] != '=' {
		return false
	}
	if i+1 < len(text) && text[i+1] == '=' {
		return false
	}
	return true
}

// hasRedirectOperator 重定向检测——只认作为重定向操作符出现的 >（引号感知 + 语义豁免）。
func hasRedirectOperator(command string) bool {
	inSingle, inDouble := false, false
	for i := 0; i < len(command); i++ {
		c := command[i]
		if inSingle {
			if c == '\'' {
				if i+1 < len(command) && command[i+1] == '\'' {
					i++
					continue
				}
				inSingle = false
			}
			continue
		}
		if inDouble {
			if c == '`' {
				i++
				continue
			}
			if c == '"' {
				inDouble = false
			}
			continue
		}
		if c == '\'' {
			inSingle = true
			continue
		}
		if c == '"' {
			inDouble = true
			continue
		}
		if c != '>' {
			continue
		}
		prev := prevNonSpace(command, i)
		next := byte(0)
		if i+1 < len(command) {
			next = command[i+1]
		}
		if prev == '-' || prev == '=' || prev == '<' {
			continue
		}
		if next == '=' {
			continue
		}
		// n>&m 流合并（2>&1 等）——控制台流操作，非文件写
		if next == '&' && i+2 < len(command) {
			stream := command[i+2]
			if stream == '*' || (stream >= '0' && stream <= '9') {
				continue
			}
		}
		return true
	}
	return false
}

// prevNonSpace 前一非空白字节（行首/全空白返回 0）。
func prevNonSpace(text string, index int) byte {
	for i := index - 1; i >= 0; i-- {
		if !isSpaceByte(text[i]) {
			return text[i]
		}
	}
	return 0
}

// containsWord 单词边界包含检测——目标词作为独立单词出现。
func containsWord(text, word string) bool {
	idx := 0
	for {
		pos := strings.Index(strings.ToLower(text[idx:]), strings.ToLower(word))
		if pos < 0 {
			return false
		}
		idx += pos
		prevOK := idx == 0 || !isWordByte(text[idx-1])
		nextOK := idx+len(word) >= len(text) || !isWordByte(text[idx+len(word)])
		if prevOK && nextOK {
			return true
		}
		idx += len(word)
	}
}

// isWordByte 词字符判定——字母/数字/下划线。
func isWordByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// isVariableByte 变量名字符判定——字母/数字/下划线/冒号。
func isVariableByte(c byte) bool {
	return isWordByte(c) || c == ':'
}

// isSpaceByte 空白字节判定。
func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\v' || c == '\f'
}

// psExecRun 执行面——EncodedCommand 启动 → 超时/输出处理 → A214 结构化回执。
func psExecRun(svc *fs.Service, pwshPath func() string, argsJSON, shell string) (string, bool) {
	if argsJSON == "" {
		return "ERR|PS_BAD_ARGS|缺少参数 command", false
	}
	command := Get(argsJSON, "command")
	if command == "" || command == ParseFail {
		return "ERR|PS_BAD_ARGS|缺少参数 command", false
	}
	// [段0] 解释器解析——powershell7 线读配置 ps.pwsh_path（未配置/路径不存在 → 明示 ERR，不静默回落）
	exePath := "powershell.exe"
	if shell == "powershell7" {
		path, errText := resolvePwshPath(pwshPath)
		if errText != "" {
			return errText, false
		}
		exePath = path
	}
	// [段2] 前缀——UTF-8 输出内建 + 进度流静默；powershell7 追加纯文本渲染
	prefix := "[Console]::OutputEncoding=[Text.Encoding]::UTF8; $ProgressPreference='SilentlyContinue'; chcp 65001 > $null; "
	if shell == "powershell7" {
		prefix += "$PSStyle.OutputRendering='PlainText'; "
	}
	prefixed := prefix + buildRootDrivePrefix(argsJSON, svc) + command
	// [段3] EncodedCommand——UTF-16LE → Base64（免转义）
	encoded := base64.StdEncoding.EncodeToString(utf16LE(prefixed))
	cmd := exec.Command(exePath, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encoded)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if cwd := Get(argsJSON, "cwd"); cwd != "" && cwd != ParseFail {
		cmd.Dir = cwd
	}
	// [段4] 启动 + 行收集（按行拼接，行间 \n；超限丢后续行）
	outW := &lineCapper{max: psMaxStdoutChars}
	errW := &lineCapper{max: psMaxStderrChars}
	cmd.Stdout = outW
	cmd.Stderr = errW
	if err := cmd.Start(); err != nil {
		return "ERR|PS_LAUNCH|" + err.Error(), false
	}
	// [段5] 超时等待——超时杀进程树（taskkill /T /F，不留僵尸）
	timeoutMs := parseTimeout(Get(argsJSON, "timeout_ms"))
	timeout := false
	exitCode := 0
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	select {
	case err := <-waitDone:
		exitCode = exitCodeOf(cmd, err)
	case <-time.After(time.Duration(timeoutMs) * time.Millisecond):
		timeout = true
		killTree(cmd)
		<-waitDone
	}
	outW.flush()
	errW.flush()
	outText := outW.buf.String()
	errText := cleanErrorText(errW.buf.String())
	outTrunc := len(outText) >= psMaxStdoutChars
	errTrunc := len(errText) >= psMaxStderrChars
	if timeout {
		exitCode = -1
	}
	// [段6] 结构化回执——首行 JSON 元数据头 + 摘要行 + 正文（stdout + 换行 + stderr）
	stdoutLines := countLinesOf(outText)
	stderrLines := countLinesOf(errText)
	shellName := "powershell"
	if shell == "powershell7" {
		shellName = "powershell7"
	}
	bodyText := outText
	if errText != "" {
		bodyText = outText + "\n" + errText
	}
	fields := [][2]any{
		{"exit", exitCode},
		{"truncated", outTrunc || errTrunc},
		{"timeout", timeout},
		{"stdoutLines", stdoutLines},
		{"stderrLines", stderrLines},
	}
	head := MetaHeadFields(shellName, true, shellName, -1, fields)
	summary := shellName + " | 退出 " + strconv.Itoa(exitCode) + " · " + strconv.Itoa(stdoutLines) + " 行"
	return head + "\n" + summary + "\n" + bodyText, true
}

// lineCapper 行收集器——按行拼接（行间 \n），达上限后丢弃后续行（对齐 C# AppendCapped）。
type lineCapper struct {
	buf     strings.Builder
	max     int
	pending []byte
}

// Write 收字节流——按 \n 切行处理（\r 去尾）。
func (w *lineCapper) Write(p []byte) (int, error) {
	w.pending = append(w.pending, p...)
	for {
		idx := bytes.IndexByte(w.pending, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimSuffix(string(w.pending[:idx]), "\r")
		w.pending = w.pending[idx+1:]
		appendCapped(&w.buf, line, w.max)
	}
	return len(p), nil
}

// flush 收尾——末行无换行时补处理。
func (w *lineCapper) flush() {
	if len(w.pending) > 0 {
		appendCapped(&w.buf, strings.TrimSuffix(string(w.pending), "\r"), w.max)
		w.pending = nil
	}
}

// appendCapped 追加一行——上限截断（超限后丢弃后续）。
func appendCapped(sb *strings.Builder, line string, max int) {
	if sb.Len() >= max {
		return
	}
	if sb.Len() > 0 {
		sb.WriteByte('\n')
	}
	remain := max - sb.Len()
	if remain <= 0 {
		return
	}
	if len(line) <= remain {
		sb.WriteString(line)
		return
	}
	sb.WriteString(line[:remain])
}

// cleanErrorText 清洗错误文本——CLIXML 解包 + ANSI 剥离。
func cleanErrorText(raw string) string {
	if raw == "" {
		return raw
	}
	text := raw
	if objStart := strings.Index(text, "<Objs"); objStart >= 0 {
		var sb strings.Builder
		cursor := objStart
		for {
			openTag := strings.Index(text[cursor:], `<S S="`)
			if openTag < 0 {
				break
			}
			openTag += cursor
			contentStart := strings.IndexByte(text[openTag:], '>')
			if contentStart < 0 {
				break
			}
			contentStart += openTag
			contentEnd := strings.Index(text[contentStart+1:], "</S>")
			if contentEnd < 0 {
				break
			}
			contentEnd += contentStart + 1
			sb.WriteString(unescapeCliXML(text[contentStart+1 : contentEnd]))
			cursor = contentEnd + 4
		}
		if sb.Len() > 0 {
			text = sb.String()
		}
	}
	return stripAnsi(text)
}

// unescapeCliXML CLIXML 转义还原——_xNNNN_ 控制字符 + XML 实体。
func unescapeCliXML(text string) string {
	var sb strings.Builder
	i := 0
	for i < len(text) {
		if text[i] == '_' && i+6 < len(text) && text[i+1] == 'x' && text[i+6] == '_' {
			if code := hex4(text, i+2); code >= 0 {
				sb.WriteRune(rune(code))
				i += 7
				continue
			}
		}
		if text[i] == '&' {
			switch {
			case strings.HasPrefix(text[i:], "&lt;"):
				sb.WriteByte('<')
				i += 4
				continue
			case strings.HasPrefix(text[i:], "&gt;"):
				sb.WriteByte('>')
				i += 4
				continue
			case strings.HasPrefix(text[i:], "&amp;"):
				sb.WriteByte('&')
				i += 5
				continue
			case strings.HasPrefix(text[i:], "&quot;"):
				sb.WriteByte('"')
				i += 6
				continue
			case strings.HasPrefix(text[i:], "&apos;"):
				sb.WriteByte('\'')
				i += 6
				continue
			}
		}
		sb.WriteByte(text[i])
		i++
	}
	return sb.String()
}

// hex4 四位十六进制解析（非法返回 -1）。
func hex4(text string, start int) int {
	if start+4 > len(text) {
		return -1
	}
	value := 0
	for i := 0; i < 4; i++ {
		c := text[start+i]
		var digit int
		switch {
		case c >= '0' && c <= '9':
			digit = int(c - '0')
		case c >= 'a' && c <= 'f':
			digit = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			digit = int(c-'A') + 10
		default:
			return -1
		}
		value = value*16 + digit
	}
	return value
}

// stripAnsi ANSI 转义剥离——ESC[ 参数 终止字母 序列整体删除。
func stripAnsi(text string) string {
	var sb strings.Builder
	i := 0
	for i < len(text) {
		if text[i] == 0x1B && i+1 < len(text) && text[i+1] == '[' {
			j := i + 2
			for j < len(text) {
				c := text[j]
				if (c >= '0' && c <= '9') || c == ';' || c == '?' || c == ' ' {
					j++
					continue
				}
				break
			}
			if j < len(text) && ((text[j] >= 'A' && text[j] <= 'Z') || (text[j] >= 'a' && text[j] <= 'z')) {
				i = j + 1
				continue
			}
		}
		sb.WriteByte(text[i])
		i++
	}
	return sb.String()
}

// parseTimeout 超时解析——非法/空回退默认；超上限钳制。
func parseTimeout(raw string) int64 {
	if ms, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil && ms > 0 {
		if ms > psMaxTimeoutMs {
			return psMaxTimeoutMs
		}
		return ms
	}
	return psDefaultTimeoutMs
}

// exitCodeOf 取进程退出码（无状态回落 0）。
func exitCodeOf(cmd *exec.Cmd, waitErr error) int {
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	if waitErr != nil {
		return -1
	}
	return 0
}

// killTree 杀进程树——taskkill /T /F（杀不掉静默：进程可能已退出）。
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	killer := exec.Command("taskkill.exe", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	killer.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := killer.Start(); err == nil {
		_ = killer.Wait()
	}
	_ = cmd.Process.Kill()
}

// resolvePwshPath 解析 PowerShell 7 可执行路径——未配置 / 路径不存在 → 明示错误。
func resolvePwshPath(pwshPath func() string) (string, string) {
	path := ""
	if pwshPath != nil {
		path = strings.TrimSpace(pwshPath())
	}
	if path == "" {
		return "", "ERR|PS_PWSH_MISSING|PowerShell 7 未配置——请在配置面填写 ps.pwsh_path（pwsh.exe 全路径；获取方式 powershell -Command where.exe pwsh）"
	}
	if _, err := os.Stat(path); err != nil {
		return "", "ERR|PS_PWSH_NOT_FOUND|配置的 PowerShell 7 路径不存在：" + path + "——请更新 ps.pwsh_path"
	}
	return path, ""
}

// buildRootDrivePrefix 受控根注册为本次 PS 会话驱动器（New-PSDrive）——失败出声不中断。
func buildRootDrivePrefix(argsJSON string, svc *fs.Service) string {
	if svc == nil {
		return ""
	}
	catID := Get(argsJSON, "catId")
	if catID == "" || catID == ParseFail {
		return ""
	}
	var sb strings.Builder
	for _, root := range svc.Roots {
		safe := strings.ReplaceAll(root.Path, "'", "''")
		sb.WriteString("try { New-PSDrive -Name " + root.ID + " -PSProvider FileSystem -Root '" + safe + "' -ErrorAction Stop | Out-Null } catch { Write-Error ('根寻址注册失败: " + root.ID + " — ' + $_.Exception.Message) }; ")
	}
	return sb.String()
}

// utf16LE 字符串转 UTF-16LE 字节（EncodedCommand 通道）。
func utf16LE(text string) []byte {
	encoded := utf16.Encode([]rune(text))
	out := make([]byte, 0, len(encoded)*2)
	for _, unit := range encoded {
		out = append(out, byte(unit), byte(unit>>8))
	}
	return out
}
