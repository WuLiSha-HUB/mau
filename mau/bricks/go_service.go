package bricks

// GoService —— go 工具链执行服务（GoCat go-* 四件消费）。
//
// 语义：宿主首个 Go 工具组——C# 时代无对应实现（无 C# 基准可对齐），口径按 Go 官方工具链自定：
//   - Build：`go build`——编译裁决唯一权威（支持 -o 产物 / 附加参数）
//   - Test：`go test`——跑测（filter / -count=1 / -v；缓存命中显示 cached）
//   - Vet：`go vet`——官方静态检查
//   - Fmt：`gofmt -l`（check，零写入）/ `gofmt -w`（apply）——🔴 gofmt 是唯一形态
//
// 工具链路径读配置 go.path（go.exe 全路径；gofmt.exe 取同目录）；未配置即明示不可用，不回落 PATH——
// 本机 go.exe 在 workspace:tools/go/bin 下且不在 PATH 上（实测 `go version` 报 CommandNotFound），
// 回落探测只会把错误藏起来。
//
// 工作目录 dir 可选：空 = 首个含 go.mod 的可写根（本支即 mau 根 = Go 仓库根）；
// 显式给则按受控根寻址解析（`mau:` / `mau:cmd/cathome5` / 绝对路径），越界报 GO_OUT_OF_ROOT。
//
// 输出限幅 20000 字符/路（比 py 的 60000 更紧：go test 输出通常几十行，而超长回执进前文是
// G20 判例的翻车面）；超限即截断并置 truncated=true——不静默。

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cathome5/mau/fs"
)

const (
	// goMaxOutputChars 是单路输出截断上限（字符）——超限置 truncated 并在正文尾留提示。
	goMaxOutputChars = 20000
	// goDefaultTimeoutMs 是默认超时毫秒（与工具执行层统一口径：120 s 上限）。
	goDefaultTimeoutMs = 120000
	// goMaxTimeoutMs 是超时上限毫秒（不放开——工具执行层统一 120 s）。
	goMaxTimeoutMs = 120000
	// goDefaultTarget 是默认包模式（全仓）。
	goDefaultTarget = "./..."
)

// GoService 是 go 工具链执行服务——同步 exec + 超时杀树 + 输出限幅。
type GoService struct {
	// roots 可写受控根（dir / output 归属校验面；只收 writable=true）
	roots []fs.Root
	// cfg 配置读取（go.* 段；nil = 全部按未配置口径）
	cfg func(key string) string
	// logf 日志出口（nil = 不出声）
	logf func(level int, message string)
	// defaultWorkDir 默认工作目录（首个含 go.mod 的可写根；空 = 无命中，调 dir 必填）
	defaultWorkDir string
}

// NewGoService 构造 go 工具链执行服务。
//
// roots 取受控根全表（只保留 writable 根）；defaultWorkDir 按「根序第一个含 go.mod 的目录」判定——
// 不深扫子目录（本支 Go 仓库根 = 受控根 mau，其下即 go.mod）。
func NewGoService(roots []fs.Root, cfg func(key string) string, logf func(level int, message string)) *GoService {
	writable := make([]fs.Root, 0, len(roots))
	defaultDir := ""
	for _, root := range roots {
		if !root.Writable || root.Path == "" {
			continue
		}
		writable = append(writable, root)
		if defaultDir == "" {
			if stat, err := os.Stat(filepath.Join(root.Path, "go.mod")); err == nil && !stat.IsDir() {
				defaultDir = root.Path
			}
		}
	}
	return &GoService{
		roots:          writable,
		cfg:            cfg,
		logf:           logf,
		defaultWorkDir: defaultDir,
	}
}

// Build 执行 go build——编译裁决唯一权威。
//
// 参数：dir（可选）/ path（包模式，默认 ./...）/ output（-o 产物路径）/ args（附加参数原样追加）/ timeout_ms。
func (s *GoService) Build(argsJSON string) string {
	if argsJSON == "" {
		argsJSON = "{}"
	}
	if bad := Validate(argsJSON, "dir path output args timeout_ms", "", "", ""); bad != "" {
		return bad
	}
	exe, errText := s.resolveGo()
	if errText != "" {
		return errText
	}
	workDir, errText := s.resolveWorkDir(Get(argsJSON, "dir"))
	if errText != "" {
		return errText
	}
	target := strings.TrimSpace(Get(argsJSON, "path"))
	if target == "" || target == ParseFail {
		target = goDefaultTarget
	}
	argv := []string{"build"}
	if extra := strings.TrimSpace(Get(argsJSON, "args")); extra != "" && extra != ParseFail {
		argv = append(argv, strings.Fields(extra)...)
	}
	if output := strings.TrimSpace(Get(argsJSON, "output")); output != "" && output != ParseFail {
		resolved, resolveErr := s.resolveOutput(output)
		if resolveErr != "" {
			return resolveErr
		}
		argv = append(argv, "-o", resolved)
	}
	argv = append(argv, target)
	return s.run("go-build", exe, argv, workDir, s.timeout(Get(argsJSON, "timeout_ms")))
}

// Test 执行 go test——跑测（缓存命中显示 cached，不是没跑）。
//
// 参数：dir / path / filter（-run 正则）/ count1（-count=1 强制重跑）/ verbose（-v）/ args / timeout_ms。
func (s *GoService) Test(argsJSON string) string {
	if argsJSON == "" {
		argsJSON = "{}"
	}
	if bad := Validate(argsJSON, "dir path filter count1 verbose args timeout_ms", "", "", ""); bad != "" {
		return bad
	}
	// 参数面先判完再碰环境——布尔非法值指向调用方（与「未配置指向环境」分开）
	verbose, boolErr := parseBoolField(argsJSON, "verbose")
	if boolErr != "" {
		return boolErr
	}
	count1, boolErr := parseBoolField(argsJSON, "count1")
	if boolErr != "" {
		return boolErr
	}
	exe, errText := s.resolveGo()
	if errText != "" {
		return errText
	}
	workDir, errText := s.resolveWorkDir(Get(argsJSON, "dir"))
	if errText != "" {
		return errText
	}
	target := strings.TrimSpace(Get(argsJSON, "path"))
	if target == "" || target == ParseFail {
		target = goDefaultTarget
	}
	argv := []string{"test"}
	if verbose {
		argv = append(argv, "-v")
	}
	if count1 {
		argv = append(argv, "-count=1")
	}
	if filter := strings.TrimSpace(Get(argsJSON, "filter")); filter != "" && filter != ParseFail {
		argv = append(argv, "-run", filter)
	}
	if extra := strings.TrimSpace(Get(argsJSON, "args")); extra != "" && extra != ParseFail {
		argv = append(argv, strings.Fields(extra)...)
	}
	argv = append(argv, target)
	return s.run("go-test", exe, argv, workDir, s.timeout(Get(argsJSON, "timeout_ms")))
}

// parseBoolField 解析布尔参数——缺省 false；非 true/false 即 BAD_ARGS（不静默忽略，§1.16 ②）。
func parseBoolField(argsJSON, key string) (bool, string) {
	raw := strings.TrimSpace(Get(argsJSON, key))
	if raw == "" || raw == ParseFail {
		return false, ""
	}
	value, ok := boolArg(raw)
	if !ok {
		return false, "ERR|GO_BAD_ARGS|" + key + " 非法值: " + raw + "（true|false）"
	}
	return value, ""
}

// Vet 执行 go vet——官方静态检查（并发/时序问题不靠它兜，见 L2/Job/Go tools 铁律）。
//
// 参数：dir / path / args / timeout_ms。
func (s *GoService) Vet(argsJSON string) string {
	if argsJSON == "" {
		argsJSON = "{}"
	}
	if bad := Validate(argsJSON, "dir path args timeout_ms", "", "", ""); bad != "" {
		return bad
	}
	exe, errText := s.resolveGo()
	if errText != "" {
		return errText
	}
	workDir, errText := s.resolveWorkDir(Get(argsJSON, "dir"))
	if errText != "" {
		return errText
	}
	target := strings.TrimSpace(Get(argsJSON, "path"))
	if target == "" || target == ParseFail {
		target = goDefaultTarget
	}
	argv := []string{"vet"}
	if extra := strings.TrimSpace(Get(argsJSON, "args")); extra != "" && extra != ParseFail {
		argv = append(argv, strings.Fields(extra)...)
	}
	argv = append(argv, target)
	return s.run("go-vet", exe, argv, workDir, s.timeout(Get(argsJSON, "timeout_ms")))
}

// Fmt 执行 gofmt——mode=check 干跑列差异（零写入）/ mode=apply 写盘。
//
// 参数：dir / path（文件或目录，目录递归；默认 "."）/ mode（check|apply，默认 check）/ timeout_ms。
// check 的回执正文是未格式化文件清单（空 = 全绿）；apply 的正文是改动文件清单。
func (s *GoService) Fmt(argsJSON string) string {
	if argsJSON == "" {
		argsJSON = "{}"
	}
	if bad := Validate(argsJSON, "dir path mode timeout_ms", "", "mode", "check|apply"); bad != "" {
		return bad
	}
	gofmt, errText := s.resolveGofmt()
	if errText != "" {
		return errText
	}
	workDir, errText := s.resolveWorkDir(Get(argsJSON, "dir"))
	if errText != "" {
		return errText
	}
	target := strings.TrimSpace(Get(argsJSON, "path"))
	if target == "" || target == ParseFail {
		target = "."
	}
	mode := strings.TrimSpace(Get(argsJSON, "mode"))
	if mode == "" || mode == ParseFail {
		mode = "check"
	}
	argv := []string{}
	if mode == "apply" {
		argv = append(argv, "-w")
	} else {
		argv = append(argv, "-l")
	}
	argv = append(argv, target)
	return s.run("go-fmt", gofmt, argv, workDir, s.timeout(Get(argsJSON, "timeout_ms")))
}

// run 同步执行一次工具链命令——起-跑-退，超时杀整棵进程树。
//
// 回执 JSON 固定序：ok / tool / dir / exit / timeout / stdout / stderr /
// stdoutLines / stderrLines / truncated / elapsedMs / argv。
// ok = exit==0 且未超时——它是「语义目标达成」的正面陈述，不是「跑过了」。
func (s *GoService) run(tool, exe string, argv []string, workDir string, timeoutMs int) string {
	cmd := exec.Command(exe, argv...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Dir = workDir

	outW := newProcLineCapper(goMaxOutputChars)
	errW := newProcLineCapper(goMaxOutputChars)
	cmd.Stdout = outW
	cmd.Stderr = errW

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return "ERR|GO_LAUNCH|" + err.Error()
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
	truncated := utf16Len(outText) >= goMaxOutputChars || utf16Len(errText) >= goMaxOutputChars

	return goRunJSON(tool, workDir, exe, argv, exit, timeout, outText, errText, truncated, elapsed)
}

// resolveGo 解析 go.exe——读配置 go.path；未配置 / 路径不存在即明示 ERR（不回落 PATH）。
func (s *GoService) resolveGo() (string, string) {
	path := s.cfgValue("go.path")
	if path == "" {
		return "", "ERR|GO_NOT_CONFIGURED|Go 工具链未配置——请在配置面填写 go.path（go.exe 全路径；获取方式 where go）"
	}
	if _, err := os.Stat(path); err != nil {
		return "", "ERR|GO_TOOLCHAIN_NOT_FOUND|配置的 go.exe 不存在：" + path + "——请更新 go.path"
	}
	return path, ""
}

// resolveGofmt 解析 gofmt.exe——取 go.path 同目录（Go 发行包内 gofmt 与 go 恒同目录）。
func (s *GoService) resolveGofmt() (string, string) {
	goExe, errText := s.resolveGo()
	if errText != "" {
		return "", errText
	}
	gofmt := filepath.Join(filepath.Dir(goExe), "gofmt.exe")
	if _, err := os.Stat(gofmt); err != nil {
		return "", "ERR|GO_TOOLCHAIN_NOT_FOUND|gofmt.exe 不在 go.path 同目录：" + gofmt
	}
	return gofmt, ""
}

// resolveWorkDir 解析工作目录——显式 dir 走受控根寻址；空 = 首个含 go.mod 的可写根。
func (s *GoService) resolveWorkDir(raw string) (string, string) {
	dirArg := strings.TrimSpace(raw)
	if dirArg != "" && dirArg != ParseFail {
		base, _ := os.Getwd()
		full, _, code, msg, ok := fs.ResolveOwnedRoot(dirArg, s.roots, base)
		if !ok {
			if code == fs.ErrOutOfScope {
				return "", "ERR|GO_OUT_OF_ROOT|目录超出受控根范围：" + full
			}
			return "", "ERR|GO_BAD_PATH|目录路径非法: " + msg
		}
		if stat, err := os.Stat(full); err != nil || !stat.IsDir() {
			return "", "ERR|GO_DIR_NOT_FOUND|目录不存在：" + full
		}
		return full, ""
	}
	if s.defaultWorkDir != "" {
		return s.defaultWorkDir, ""
	}
	return "", "ERR|GO_NO_WORKDIR|未指定 dir 且无含 go.mod 的可写根——请显式传 dir（受控根寻址，如 mau:）"
}

// resolveOutput 解析 -o 产物路径——带根前缀的按受控根解析，否则原样（相对 workDir）。
func (s *GoService) resolveOutput(raw string) (string, string) {
	if !strings.Contains(raw, ":") && !filepath.IsAbs(raw) {
		return raw, ""
	}
	base, _ := os.Getwd()
	full, _, code, msg, ok := fs.ResolveOwnedRoot(raw, s.roots, base)
	if !ok {
		if code == fs.ErrOutOfScope {
			return "", "ERR|GO_OUT_OF_ROOT|产物路径超出受控根范围：" + full
		}
		return "", "ERR|GO_BAD_PATH|产物路径非法: " + msg
	}
	return full, ""
}

// timeout 解析超时毫秒——空 / 非法取默认，只夹上限。
func (s *GoService) timeout(raw string) int {
	return procParseTimeout(raw, goDefaultTimeoutMs, goMaxTimeoutMs)
}

// cfgValue 读配置并去首尾空白。
func (s *GoService) cfgValue(key string) string {
	if s.cfg == nil {
		return ""
	}
	return strings.TrimSpace(s.cfg(key))
}

// goRunJSON 组装执行回执——十二键固定序（ok / tool / dir / exit / timeout / stdout / stderr /
// stdoutLines / stderrLines / truncated / elapsedMs / argv）。
func goRunJSON(tool, workDir, exe string, argv []string, exit int, timeout bool, stdout, stderr string, truncated bool, elapsedMs int64) string {
	var b strings.Builder
	b.WriteString(`{"ok":`)
	b.WriteString(strconv.FormatBool(!timeout && exit == 0))
	b.WriteString(`,"tool":`)
	b.WriteString(jsonString(tool))
	b.WriteString(`,"dir":`)
	b.WriteString(jsonString(workDir))
	b.WriteString(`,"exit":`)
	b.WriteString(strconv.Itoa(exit))
	b.WriteString(`,"timeout":`)
	b.WriteString(strconv.FormatBool(timeout))
	b.WriteString(`,"stdout":`)
	b.WriteString(jsonRelaxedString(stdout))
	b.WriteString(`,"stderr":`)
	b.WriteString(jsonRelaxedString(stderr))
	b.WriteString(`,"stdoutLines":`)
	b.WriteString(strconv.Itoa(procCountLines(stdout)))
	b.WriteString(`,"stderrLines":`)
	b.WriteString(strconv.Itoa(procCountLines(stderr)))
	b.WriteString(`,"truncated":`)
	b.WriteString(strconv.FormatBool(truncated))
	b.WriteString(`,"elapsedMs":`)
	b.WriteString(strconv.FormatInt(elapsedMs, 10))
	b.WriteString(`,"argv":`)
	b.WriteString(goArgvJSON(exe, argv))
	b.WriteByte('}')
	return b.String()
}

// goArgvJSON 把可执行文件名与参数表拼成 JSON 数组（可复跑判据：看到 argv 就能原样重跑）。
//
// [0] 只给 exe 的 basename——完整路径随机器变，basename + dir 足以复跑（go / gofmt）。
func goArgvJSON(exe string, argv []string) string {
	var b strings.Builder
	b.WriteByte('[')
	b.WriteString(jsonString(filepath.Base(exe)))
	for _, arg := range argv {
		b.WriteByte(',')
		b.WriteString(jsonString(arg))
	}
	b.WriteByte(']')
	return b.String()
}
