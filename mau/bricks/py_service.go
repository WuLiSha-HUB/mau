package bricks

// PyService —— py 执行服务（PyCat py-* 三件消费）。
//
// 语义对齐 C# Mau.Runtime.PyService（745 行）+ PyService.Run.cs（323 行）：
//   - Run：短命脚本「起-跑-退」——脚本路径须在可写受控根内 + 解释器解析 + 超时杀树 +
//     输出单路截断 60000 字符 + 回执八键固定序
//   - List：可写受控根内 *.py 递归扫描（跳过依赖与构建目录，上限 200，去重忽略大小写）
//   - Service：常驻服务 start / stop / status（进程池，stdio 严格协议）
//   - Invoke：服务调用（未启动按需拉起；回执浅合并 restarted）
//   - Autostart：按 py.service.<name>.follow=true 拉起（宿主启动链调用）
//
// 解释器读 py.python_path（服务级 py.service.<name>.python 可覆盖）；未配置即明示不可用，不回落 PATH。
// 服务脚本与扫描目录只认可写受控根（对齐 C# IsUnderWritableRoot / InRoots）。
// 协议核心在 stdio_host.go（与 ts 共用）；服务锚自愈与 Enumerate 未接（交接 §3.4）。

import (
	"bufio"
	"io"
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

const (
	// pyMaxOutputChars 是单路输出截断上限（字符；对齐 C# MaxOutputChars）。
	pyMaxOutputChars = 60000
	// pyDefaultTimeoutMs 是默认超时毫秒（与工具执行层统一口径）。
	pyDefaultTimeoutMs = 120000
	// pyMaxScanScripts 是脚本扫描上限（对齐 C# found.Count >= 200）。
	pyMaxScanScripts = 200
)

// pySkippedSegments 是扫描跳过的路径片段（依赖与构建目录不进清单，对齐 C# IsSkippedPath）。
var pySkippedSegments = []string{
	`\.venv\`, `\venv\`, `\node_modules\`, `\bin\`, `\obj\`, `\.git\`, `\__pycache__\`, `\site-packages\`,
}

// PyService 是 py 执行服务——短命脚本执行 + 常驻服务池。
type PyService struct {
	// roots 可写受控根（脚本 / 目录归属校验面；只收 writable=true）
	roots []fs.Root
	// cfg 配置读取（py.* 段；nil = 全部按未配置口径）
	cfg func(key string) string
	// values 落盘配置全量（首次出现序；nil = 无法枚举服务声明）
	values func() [][2]string
	// logf 日志出口（nil = 不出声）
	logf func(level int, message string)
	// defaultScanDir 默认脚本扫描面（mau 根下 tools；空 = 无 mau 根）
	defaultScanDir string

	// mu 串行锁——服务池与调用纯串行（C# lock (_gate) 同语义）
	mu sync.Mutex
	// servers 服务池（服务名 → 协议宿主）
	servers map[string]*stdioHost
}

// NewPyService 构造 py 执行服务。
//
// roots 取受控根全表（只保留 writable 根——与 C# 构造同口径）；
// cfg 读配置有效值；values 取落盘配置全量（Autostart 枚举 py.service.<name>.script 用）。
func NewPyService(roots []fs.Root, cfg func(key string) string, values func() [][2]string, logf func(level int, message string)) *PyService {
	writable := make([]fs.Root, 0, len(roots))
	scanDir := ""
	for _, root := range roots {
		if !root.Writable || root.Path == "" {
			continue
		}
		writable = append(writable, root)
		if scanDir == "" && strings.EqualFold(root.ID, "mau") {
			scanDir = filepath.Join(root.Path, "tools")
		}
	}
	return &PyService{
		roots:          writable,
		cfg:            cfg,
		values:         values,
		logf:           logf,
		defaultScanDir: scanDir,
		servers:        map[string]*stdioHost{},
	}
}

// Run 执行一个 py 脚本（短命进程）——起-跑-退。
//
// 参数：script（必填）/ args（可选，原样追加）/ timeout_ms（可选）。
// 不回传 cwd（工具定义未暴露——参数面零容忍，工作目录固定为脚本所在目录）。
func (s *PyService) Run(argsJSON string) string {
	scriptArg := Get(argsJSON, "script")
	if scriptArg == "" || scriptArg == ParseFail {
		return "ERR|PY_BAD_ARGS|缺少参数 script"
	}
	script, errText := s.resolveRunScript(scriptArg)
	if errText != "" {
		return errText
	}
	python, errText := s.resolvePython("")
	if errText != "" {
		return errText
	}
	extra := Get(argsJSON, "args")
	if extra == ParseFail {
		extra = ""
	}
	timeoutMs := parsePyTimeout(Get(argsJSON, "timeout_ms"))
	workDir := filepath.Dir(script)

	// [段1] 命令行组装——-u 后接脚本，args 按空白追加（含空格参数需调用方自行拆分）
	argv := make([]string, 0, 2)
	argv = append(argv, "-u", script)
	if trimmed := strings.TrimSpace(extra); trimmed != "" {
		argv = append(argv, strings.Fields(trimmed)...)
	}
	cmd := exec.Command(python, argv...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if stat, statErr := os.Stat(workDir); statErr == nil && stat.IsDir() {
		cmd.Dir = workDir
	}

	// [段2] 启动 + 异步读流（同步读大输出会死锁）
	started := time.Now()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "ERR|PY_LAUNCH|" + err.Error()
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "ERR|PY_LAUNCH|" + err.Error()
	}
	if err := cmd.Start(); err != nil {
		return "ERR|PY_LAUNCH|" + err.Error()
	}
	outBuf := &cappedText{cap: pyMaxOutputChars}
	errBuf := &cappedText{cap: pyMaxOutputChars}
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		scanCappedLines(stdout, outBuf)
	}()
	go func() {
		defer readers.Done()
		scanCappedLines(stderr, errBuf)
	}()

	// [段3] 超时等待——超时杀进程树（短命进程无「不杀」语义——留着就是僵尸）
	timeout := false
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	select {
	case <-exited:
	case <-time.After(time.Duration(timeoutMs) * time.Millisecond):
		timeout = true
		killCmdTree(cmd)
		<-exited
	}
	readers.Wait()
	elapsed := time.Since(started).Milliseconds()

	exitCode := -1
	if !timeout && cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return pyRunJSON(exitCode, timeout, outBuf.String(), errBuf.String(), script, elapsed)
}

// List 列出可用脚本——默认扫 mau 根下 tools（py 工具线约定面；跳过依赖与构建目录）。
func (s *PyService) List(argsJSON string) string {
	dirRaw := ""
	if argsJSON != "" {
		dirRaw = Get(argsJSON, "dir")
	}
	if dirRaw == ParseFail {
		return "ERR|PY_BAD_ARGS|参数 JSON 解析失败"
	}
	startDir := ""
	if strings.TrimSpace(dirRaw) != "" {
		resolved, errText := s.resolveDirectory(dirRaw)
		if errText != "" {
			return errText
		}
		startDir = resolved
	} else {
		if s.defaultScanDir == "" {
			return "ERR|PY_NOT_CONFIGURED|默认脚本面缺失（无 mau 根）——请显式给 dir"
		}
		startDir = s.defaultScanDir
	}
	found := make([]string, 0, 16)
	seen := map[string]bool{}
	collectPyScripts(startDir, &found, seen)
	return pyListJSON(found)
}

// Service 服务控制——start（拉起并等 ready）/ stop（优雅退出 + 强杀兜底）/ status（查进程态）。
func (s *PyService) Service(argsJSON string) string {
	name := Get(argsJSON, "name")
	action := Get(argsJSON, "action")
	if name == "" || name == ParseFail {
		return "ERR|PY_BAD_ARGS|缺少参数 name"
	}
	if action == "" || action == ParseFail {
		return "ERR|PY_BAD_ARGS|缺少参数 action（start|stop|status）"
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	existing := s.servers[name]

	switch action {
	case "status":
		return pyServiceStatusJSON(name, existing)
	case "stop":
		if existing == nil {
			return pyServiceJSON(name, "stopped", "服务未启动", false)
		}
		stopErr := existing.stop()
		delete(s.servers, name)
		if stopErr != "" {
			return stopErr
		}
		return pyServiceJSON(name, "stopped", "", false)
	case "start":
		// [段1] start——已存活即幂等返回
		if existing != nil && existing.alive() {
			return pyServiceJSON(name, "running", "服务已在运行", true)
		}
		host, errText := s.buildServerLocked(name)
		if errText != "" {
			return errText
		}
		if startErr := host.start(); startErr != "" {
			delete(s.servers, name)
			return startErr
		}
		return pyServiceJSON(name, "running", "", true)
	}
	return "ERR|PY_BAD_ARGS|action 非法值: " + action + "（start|stop|status）"
}

// Invoke 服务调用——按 stdio 严格协议发一行 JSON、读一行 JSON（单服务纯串行）。
//
// 未启动则按需拉起（follow 语义的按需分支）；进程崩溃则重拉并在回执标注 restarted。
func (s *PyService) Invoke(name, payload string) string {
	if s == nil {
		return "ERR|PY_UNAVAILABLE|服务不可用（py）"
	}
	if name == "" {
		return "ERR|PY_BAD_ARGS|缺少参数 name"
	}
	if payload == "" {
		return "ERR|PY_BAD_ARGS|缺少参数 payload（请求体 JSON 文本）"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	host := s.servers[name]
	if host == nil {
		built, errText := s.buildServerLocked(name)
		if errText != "" {
			return errText
		}
		host = built
	}
	return host.invoke(payload)
}

// Autostart 按配置拉起 follow=true 的服务——服务清单自落盘配置扫描得出
// （键 py.service.<name>.script 存在即视为已声明服务）。
func (s *PyService) Autostart() string {
	if s == nil {
		return ""
	}
	if s.values == nil {
		return "ERR|PY_NOT_CONFIGURED|配置存储未就绪——无法读取 py 服务声明"
	}
	names := []string{}
	seen := map[string]bool{}
	for _, kv := range s.values() {
		key := kv[0]
		if !strings.HasPrefix(key, "py.service.") || !strings.HasSuffix(key, ".script") {
			continue
		}
		name := key[len("py.service.") : len(key)-len(".script")]
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	started := []string{}
	skipped := []string{}
	failed := []string{}
	for _, name := range names {
		if !strings.EqualFold(s.cfgValue("py.service."+name+".follow"), "true") {
			skipped = append(skipped, name)
			continue
		}
		reply := s.Service(`{"name":` + jsonString(name) + `,"action":"start"}`)
		if strings.HasPrefix(reply, "ERR|") {
			failed = append(failed, name)
			s.log(2, "服务自启失败（"+name+"）: "+reply)
			continue
		}
		started = append(started, name)
	}
	return `{"started":` + jsonRelaxedArray(started) + `,"skipped":` + jsonRelaxedArray(skipped) + `,"errors":` + jsonRelaxedArray(failed) + `}`
}

// Shutdown 停止全部服务进程——宿主退出时统一清理，不留孤儿。
func (s *PyService) Shutdown() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, host := range s.servers {
		host.stop()
		s.log(1, "py 服务已停止："+name)
	}
	s.servers = map[string]*stdioHost{}
}

// Alive 报告指定服务的进程是否存活。
func (s *PyService) Alive(name string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	host := s.servers[name]
	return host != nil && host.alive()
}

// buildServerLocked 解析服务配置并装配协议宿主——调用方持锁。
func (s *PyService) buildServerLocked(name string) (*stdioHost, string) {
	script, errText := s.resolveServiceScript(name)
	if errText != "" {
		return nil, errText
	}
	python, errText := s.resolvePython(name)
	if errText != "" {
		return nil, errText
	}
	host := &stdioHost{
		name:      name,
		errPrefix: "PY",
		logf:      s.logf,
		prefix:    []string{"-u"},
		exe:       python,
		script:    script,
		args:      strings.Fields(s.cfgValue("py.service." + name + ".args")),
		home:      s.cfgValue("py.service." + name + ".home"),
		timeoutMs: parsePyTimeout(s.cfgValue("py.service." + name + ".timeout_ms")),
	}
	s.servers[name] = host
	return host, ""
}

// resolveRunScript 解析短命脚本路径——必须在可写受控根内且存在。
func (s *PyService) resolveRunScript(raw string) (string, string) {
	base, _ := os.Getwd()
	full, _, code, msg, ok := fs.ResolveOwnedRoot(raw, s.roots, base)
	if !ok {
		if code == fs.ErrOutOfScope {
			return "", "ERR|PY_OUT_OF_ROOT|脚本超出受控根范围：" + full
		}
		return "", "ERR|PY_BAD_PATH|脚本路径非法: " + msg
	}
	if _, err := os.Stat(full); err != nil {
		return "", "ERR|PY_SCRIPT_NOT_FOUND|脚本不存在：" + full
	}
	return full, ""
}

// resolveServiceScript 解析服务入口脚本——读配置 py.service.<name>.script（受控根寻址）。
func (s *PyService) resolveServiceScript(name string) (string, string) {
	scriptRaw := s.cfgValue("py.service." + name + ".script")
	if scriptRaw == "" {
		return "", "ERR|PY_SERVICE_NOT_CONFIGURED|服务未配置：" + name + "——请在配置面填写 py.service." + name + ".script"
	}
	base, _ := os.Getwd()
	full, _, code, msg, ok := fs.ResolveOwnedRoot(scriptRaw, s.roots, base)
	if !ok {
		if code == fs.ErrOutOfScope {
			return "", "ERR|PY_SCRIPT_OUTSIDE_ROOT|服务脚本超出受控根：" + full + "——只允许可写受控根内的脚本"
		}
		return "", "ERR|PY_BAD_PATH|服务脚本路径非法: " + msg
	}
	if _, err := os.Stat(full); err != nil {
		return "", "ERR|PY_SCRIPT_NOT_FOUND|服务脚本不存在：" + full
	}
	return full, ""
}

// resolveDirectory 解析扫描目录——必须在可写受控根内且存在。
func (s *PyService) resolveDirectory(raw string) (string, string) {
	base, _ := os.Getwd()
	full, _, code, msg, ok := fs.ResolveOwnedRoot(raw, s.roots, base)
	if !ok {
		if code == fs.ErrOutOfScope {
			return "", "ERR|PY_OUT_OF_ROOT|目录超出受控根范围：" + full
		}
		return "", "ERR|PY_BAD_PATH|目录路径非法: " + msg
	}
	if stat, err := os.Stat(full); err != nil || !stat.IsDir() {
		return "", "ERR|PY_DIR_NOT_FOUND|目录不存在：" + full
	}
	return full, ""
}

// resolvePython 解析解释器路径——服务级覆盖优先，回退全局；未配置 / 路径不存在即明示 ERR。
func (s *PyService) resolvePython(serviceName string) (string, string) {
	path := ""
	if serviceName != "" {
		path = s.cfgValue("py.service." + serviceName + ".python")
	}
	if path == "" {
		path = s.cfgValue("py.python_path")
	}
	if path == "" {
		return "", "ERR|PY_NOT_CONFIGURED|Python 解释器未配置——请在配置面填写 py.python_path（python.exe 全路径；获取方式 py -0p）"
	}
	if _, err := os.Stat(path); err != nil {
		return "", "ERR|PY_PYTHON_NOT_FOUND|配置的解释器路径不存在：" + path + "——请更新 py.python_path"
	}
	return path, ""
}

// cfgValue 读配置并去首尾空白（路径 / 数值 / 声明类字段）。
func (s *PyService) cfgValue(key string) string {
	if s.cfg == nil {
		return ""
	}
	return strings.TrimSpace(s.cfg(key))
}

// log 日志出口——nil 安全。
func (s *PyService) log(level int, message string) {
	if s.logf != nil {
		s.logf(level, message)
	}
}

// parsePyTimeout 解析超时——非法 / 空值取默认；只夹上限（对齐 C# PyService.ParseTimeout）。
func parsePyTimeout(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return pyDefaultTimeoutMs
	}
	if value > pyDefaultTimeoutMs {
		return pyDefaultTimeoutMs
	}
	return value
}

// collectPyScripts 递归收集 *.py——跳过依赖与构建目录，上限 200，按路径去重（忽略大小写）。
func collectPyScripts(dir string, found *[]string, seen map[string]bool) {
	if len(*found) >= pyMaxScanScripts {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		full := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			if isSkippedPyPath(full) {
				continue
			}
			collectPyScripts(full, found, seen)
			if len(*found) >= pyMaxScanScripts {
				return
			}
			continue
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".py") {
			continue
		}
		if isSkippedPyPath(full) {
			continue
		}
		key := strings.ToLower(full)
		if seen[key] {
			continue
		}
		seen[key] = true
		*found = append(*found, full)
		if len(*found) >= pyMaxScanScripts {
			return
		}
	}
}

// isSkippedPyPath 扫描跳过判定——依赖与构建目录不进清单（对齐 C# IsSkippedPath）。
func isSkippedPyPath(path string) bool {
	lower := strings.ToLower(path)
	for _, segment := range pySkippedSegments {
		if strings.Contains(lower, segment) {
			return true
		}
	}
	return false
}

// scanCappedLines 逐行读流并累积（每行追加换行，达上限即停——对齐 C# AppendCapped）。
func scanCappedLines(pipe io.Reader, buffer *cappedText) {
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		buffer.appendLine(scanner.Text())
	}
}

// cappedText 是带上限的文本累积器——上限按 UTF-16 码元计（对齐 C# StringBuilder.Length）。
type cappedText struct {
	// builder 累积文本
	builder strings.Builder
	// units 已累积码元数
	units int
	// cap 上限码元数
	cap int
}

// appendLine 追加一行（已达上限即丢弃本行——对齐 C# 先判后追加）。
func (c *cappedText) appendLine(line string) {
	if c.units >= c.cap {
		return
	}
	c.builder.WriteString(line)
	c.builder.WriteByte('\n')
	for _, r := range line {
		if r > 0xFFFF {
			c.units += 2
		} else {
			c.units++
		}
	}
	c.units++
}

// String 返回累积文本。
func (c *cappedText) String() string { return c.builder.String() }

// countPyLines 行数统计——空串 0；否则 \n 计数 + 1（与前端 split 同口径）。
func countPyLines(text string) int {
	if text == "" {
		return 0
	}
	return 1 + strings.Count(text, "\n")
}

// pyRunJSON 组装 Run 回执——八键固定序：exit / timeout / stdout / stderr / stdoutLines / stderrLines / elapsedMs / script。
func pyRunJSON(exitCode int, timeout bool, stdout, stderr, script string, elapsedMs int64) string {
	var b strings.Builder
	b.WriteString(`{"exit":`)
	b.WriteString(strconv.Itoa(exitCode))
	b.WriteString(`,"timeout":`)
	b.WriteString(strconv.FormatBool(timeout))
	b.WriteString(`,"stdout":`)
	b.WriteString(jsonRelaxedString(stdout))
	b.WriteString(`,"stderr":`)
	b.WriteString(jsonRelaxedString(stderr))
	b.WriteString(`,"stdoutLines":`)
	b.WriteString(strconv.Itoa(countPyLines(stdout)))
	b.WriteString(`,"stderrLines":`)
	b.WriteString(strconv.Itoa(countPyLines(stderr)))
	b.WriteString(`,"elapsedMs":`)
	b.WriteString(strconv.FormatInt(elapsedMs, 10))
	b.WriteString(`,"script":`)
	b.WriteString(jsonRelaxedString(script))
	b.WriteByte('}')
	return b.String()
}

// pyListJSON 组装 List 回执——{count, scripts} 固定序。
func pyListJSON(scripts []string) string {
	return `{"count":` + strconv.Itoa(len(scripts)) + `,"scripts":` + jsonRelaxedArray(scripts) + `}`
}

// pyServiceJSON 组装服务态回执——四键固定序：name / state / alive / note（对齐 C# ServiceJson）。
func pyServiceJSON(name, state, note string, alive bool) string {
	return `{"name":` + jsonRelaxedString(name) + `,"state":` + jsonRelaxedString(state) +
		`,"alive":` + strconv.FormatBool(alive) + `,"note":` + jsonRelaxedString(note) + `}`
}

// pyServiceStatusJSON 组装服务态描述——未启动 / 存活 / 已退出（含连续失败计数）。
func pyServiceStatusJSON(name string, host *stdioHost) string {
	if host == nil {
		return pyServiceJSON(name, "stopped", "服务未启动", false)
	}
	if host.alive() {
		return pyServiceJSON(name, "running", "", true)
	}
	return pyServiceJSON(name, "stopped", "进程已退出（连续失败 "+strconv.Itoa(host.failures())+" 次）", false)
}

// jsonRelaxedArray 渲染字符串数组字面量（元素按 jsonRelaxedString 转义——对齐 C# JsonUtil.Serialize）。
func jsonRelaxedArray(items []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(jsonRelaxedString(item))
	}
	b.WriteByte(']')
	return b.String()
}
