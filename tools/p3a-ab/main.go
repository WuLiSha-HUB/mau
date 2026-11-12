package main

// p3a-ab —— CatHome5 P3-a 实机对拍器（第一版）。
//
// 做两件事：
//   1. 起两侧实例：C# 基线（CatHome4.exe，独立数据副本 + http.port 覆盖）与 CAT5（cathome5.exe）；
//      两侧共用同一份数据根（默认 CatTemp/cat5-data），逐项抓取 8 类响应。
//   2. 归一化后逐条勾选：config / snapshot / logs / static / SSE / 404 / 错误面 / 运行目录文件。
//
// 边界：只写本仓（WorkSpace:CatHome5/）；C# 侧只读执行，不触碰运行中的 CH4（8080/8081）。

import (
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const toolVersion = "0.1.0"

// options 是运行参数。
type options struct {
	dataRoot       string
	htmlRoot       string
	goExe          string
	csExe          string
	goPort         int
	csPort         int
	outDir         string
	spawn          bool
	goBase         string
	csBase         string
	startupTimeout time.Duration
	keep           bool
}

// sideState 是一侧的运行信息。
type sideState struct {
	name       string
	base       string
	pid        int
	proc       *launched
	runDirs    []string
	primaryDir string
	startupDir string
	stdoutPath string
}

// main 是入口。
func main() {
	opts := parseFlags()
	outDir := opts.outDir
	if outDir == "" {
		root := projectRoot()
		outDir = filepath.Join(root, "tools", "p3a-ab", "reports", time.Now().Format("20060102_150405"))
	}
	opts.outDir = outDir
	_ = os.MkdirAll(outDir, 0o755)
	rep := newReport(outDir)
	mark(outDir, "start tool="+toolVersion)
	defer func() {
		textPath, err := rep.flush()
		if err != nil {
			fmt.Println("报告落盘失败:", err)
		} else {
			fmt.Println("报告:", textPath)
		}
		mark(outDir, "done green="+strconv.FormatBool(rep.green()))
	}()

	fmt.Printf("p3a-ab %s | 数据根 %s | html %s\n", toolVersion, opts.dataRoot, opts.htmlRoot)
	rep.metaSet("time", time.Now().Format("2006-01-02 15:04:05"))
	rep.metaSet("data_root", opts.dataRoot)
	rep.metaSet("html_root", opts.htmlRoot)

	if opts.spawn {
		if err := preflight(opts); err != nil {
			rep.add("preflight", "输入面", stFail, err.Error())
			return
		}
	}

	var goSide, csSide *sideState
	var err error
	if opts.spawn {
		goSide, csSide, err = runSpawned(opts, rep)
		if err != nil {
			rep.add("spawn", "起服", stFail, err.Error())
			return
		}
	} else {
		goSide = &sideState{name: "cat5", base: strings.TrimRight(opts.goBase, "/")}
		csSide = &sideState{name: "cs", base: strings.TrimRight(opts.csBase, "/")}
	}
	if goSide == nil || csSide == nil {
		rep.add("spawn", "起服", stFail, "有一侧未就绪")
		return
	}
	rep.metaSet("cat5", goSide.base+" pid="+strconv.Itoa(goSide.pid))
	rep.metaSet("cs", csSide.base+" pid="+strconv.Itoa(csSide.pid))
	rep.metaSet("run_go", goSide.primaryDir)
	rep.metaSet("run_cs", csSide.primaryDir)

	waitDepsSettled(rep, goSide, csSide, 20*time.Second)
	runAllChecks(rep, opts, goSide, csSide)

	if opts.spawn && !opts.keep {
		mark(outDir, "stopping")
		if goSide.proc != nil {
			if stopErr := goSide.proc.stop(); stopErr != nil {
				rep.add("spawn", "停 CAT5", stFail, stopErr.Error())
			} else {
				rep.add("spawn", "停 CAT5", stPass, "已退出")
			}
		}
		if csSide.proc != nil {
			if stopErr := csSide.proc.stop(); stopErr != nil {
				rep.add("spawn", "停 C# 基线", stFail, stopErr.Error())
			} else {
				rep.add("spawn", "停 C# 基线", stPass, "已退出")
			}
		}
	}
}

// parseFlags 解析参数。
func parseFlags() options {
	root := projectRoot()
	var o options
	flag.StringVar(&o.dataRoot, "data-root", `D:\Mau\WorkSpace\tools\CatTemp\cat5-data`, "两侧共用数据根")
	flag.StringVar(&o.htmlRoot, "html-root", `D:\Mau\MauOut\html`, "静态页根（两侧同源）")
	flag.StringVar(&o.goExe, "go-exe", filepath.Join(root, "bin", "cathome5.exe"), "CAT5 可执行文件")
	flag.StringVar(&o.csExe, "cs-exe", `D:\Mau\MauOut\CatHome4.exe`, "C# 基线可执行文件")
	flag.IntVar(&o.goPort, "go-port", 0, "CAT5 监听端口（0=9180 起自动选空闲）")
	flag.IntVar(&o.csPort, "cs-port", 0, "C# 监听端口（0=自动选空闲）")
	flag.StringVar(&o.outDir, "out", "", "报告目录（默认 tools/p3a-ab/reports/<ts>）")
	flag.BoolVar(&o.spawn, "spawn", true, "true=自行起两侧；false=只对拍 --go-base/--cs-base")
	flag.StringVar(&o.goBase, "go-base", "", "已运行 CAT5 基址（--spawn=false）")
	flag.StringVar(&o.csBase, "cs-base", "", "已运行 C# 基址（--spawn=false）")
	flag.DurationVar(&o.startupTimeout, "startup-timeout", 90*time.Second, "单侧起服等待上限")
	flag.BoolVar(&o.keep, "keep", false, "true=对拍后不停止进程（调试）")
	flag.Parse()
	return o
}

// projectRoot 取施工仓根（exe 在 bin/ 下时取上一级；否则取当前目录）。
func projectRoot() string {
	exe, err := os.Executable()
	if err != nil {
		wd, _ := os.Getwd()
		return wd
	}
	dir := filepath.Dir(exe)
	if strings.EqualFold(filepath.Base(dir), "bin") {
		return filepath.Dir(dir)
	}
	return dir
}

// preflight 起服前检查。
func preflight(opts options) error {
	if _, err := os.Stat(opts.dataRoot); err != nil {
		return fmt.Errorf("数据根不存在: %s", opts.dataRoot)
	}
	if _, err := os.Stat(filepath.Join(opts.dataRoot, "config", "schema.json")); err != nil {
		return fmt.Errorf("数据根缺少 config/schema.json: %s", opts.dataRoot)
	}
	if _, err := os.Stat(filepath.Join(opts.htmlRoot, "index.html")); err != nil {
		return fmt.Errorf("html 根缺少 index.html: %s", opts.htmlRoot)
	}
	if _, err := os.Stat(opts.goExe); err != nil {
		return fmt.Errorf("CAT5 可执行文件不存在: %s", opts.goExe)
	}
	if _, err := os.Stat(opts.csExe); err != nil {
		return fmt.Errorf("C# 基线可执行文件不存在: %s", opts.csExe)
	}
	return nil
}

// runSpawned 起两侧并等待就绪。
func runSpawned(opts options, rep *report) (*sideState, *sideState, error) {
	if opts.goPort == 0 {
		opts.goPort = pickPort(9180)
	}
	if opts.csPort == 0 {
		opts.csPort = pickPort(opts.goPort + 1)
		if opts.csPort == opts.goPort {
			opts.csPort = pickPort(opts.goPort + 1)
		}
	}
	mark(opts.outDir, fmt.Sprintf("ports go=%d cs=%d", opts.goPort, opts.csPort))
	ensureSchemaTemplate(opts, rep)

	// 数据副本的 app.cfg 写 http.port（C# 启动读取）——结束后恢复
	appCfg := filepath.Join(opts.dataRoot, "config", "app.cfg")
	origCfg, readErr := os.ReadFile(appCfg)
	if readErr != nil {
		return nil, nil, fmt.Errorf("读 app.cfg 失败: %w", readErr)
	}
	if err := os.WriteFile(appCfg, setCfgValue(origCfg, "http.port", strconv.Itoa(opts.csPort)), 0o644); err != nil {
		return nil, nil, fmt.Errorf("写 app.cfg 失败: %w", err)
	}
	defer func() {
		if err := os.WriteFile(appCfg, origCfg, 0o644); err != nil {
			fmt.Println("app.cfg 恢复失败:", err)
		}
	}()

	runsDir := filepath.Join(opts.dataRoot, "runs")
	before := listDirs(runsDir)
	env := processEnv(map[string]string{"CH4_DATA_ROOT": opts.dataRoot})

	// C# 基线先起（它会校准 workspace.json 固定根，CAT5 随后读到同一份）
	mark(opts.outDir, "spawn cs")
	csProc, err := launchProcess("cs", opts.csExe, nil, filepath.Dir(opts.csExe), env, opts.outDir)
	if err != nil {
		return nil, nil, fmt.Errorf("起 C# 基线失败: %w", err)
	}
	csBase, waitErr := waitReady(csProc, "http://127.0.0.1:"+strconv.Itoa(opts.csPort), opts.startupTimeout)
	if waitErr != nil {
		_ = csProc.stop()
		return nil, nil, fmt.Errorf("C# 基线未就绪: %w", waitErr)
	}
	csSide := &sideState{name: "cs", base: csBase, pid: csProc.pid, proc: csProc, stdoutPath: csProc.stdoutPath}
	csSide.runDirs, csSide.primaryDir, csSide.startupDir = attributeRunDirs(runsDir, before, 3*time.Second)
	mark(opts.outDir, "cs ready "+csBase+" runs="+strings.Join(csSide.runDirs, ";"))

	// CAT5
	before = listDirs(runsDir)
	mark(opts.outDir, "spawn cat5")
	goArgs := []string{"--data-root", opts.dataRoot, "--html-root", opts.htmlRoot, "--area", "dev", "--port", strconv.Itoa(opts.goPort)}
	goProc, err := launchProcess("cat5", opts.goExe, goArgs, filepath.Dir(opts.goExe), env, opts.outDir)
	if err != nil {
		_ = csProc.stop()
		return nil, nil, fmt.Errorf("起 CAT5 失败: %w", err)
	}
	goBase := "http://127.0.0.1:" + strconv.Itoa(opts.goPort)
	if waitErr2 := waitReadySimple(goProc, goBase, opts.startupTimeout); waitErr2 != nil {
		_ = goProc.stop()
		_ = csProc.stop()
		return nil, nil, fmt.Errorf("CAT5 未就绪: %w", waitErr2)
	}
	goSide := &sideState{name: "cat5", base: goBase, pid: goProc.pid, proc: goProc, stdoutPath: goProc.stdoutPath}
	goSide.runDirs, goSide.primaryDir, goSide.startupDir = attributeRunDirs(runsDir, before, 3*time.Second)
	mark(opts.outDir, "cat5 ready "+goBase+" runs="+strings.Join(goSide.runDirs, ";"))
	return goSide, csSide, nil
}

// ensureSchemaTemplate 把 C# 部署区的 schema 模板同步到 CAT5 exe 目录。
//
// 对齐部署形态：C# 的模板在 MauOut/config/（exe 同级）；CAT5 的部署件同样应带
// config/schema.json.example——本步为对拍环境补齐该部署件，避免把「部署缺件」
// 误判成两侧行为差异（SCHEMA_TEMPLATE_MISSING 启动降级）。
func ensureSchemaTemplate(opts options, rep *report) {
	src := filepath.Join(filepath.Dir(opts.csExe), "config", "schema.json.example")
	dstDir := filepath.Join(filepath.Dir(opts.goExe), "config")
	dst := filepath.Join(dstDir, "schema.json.example")
	if fileExists(dst) {
		rep.add("spawn", "schema 模板", stPass, dst)
		return
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		rep.add("spawn", "schema 模板", stInfo, "源缺失，跳过: "+src)
		return
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		rep.add("spawn", "schema 模板", stFail, err.Error())
		return
	}
	if err := os.WriteFile(dst, raw, 0o644); err != nil {
		rep.add("spawn", "schema 模板", stFail, err.Error())
		return
	}
	rep.add("spawn", "schema 模板", stPass, "已布设 "+dst)
}

// setCfgValue 在 cfg 文本里设置一行键值（保留其它行）。
func setCfgValue(raw []byte, key, value string) []byte {
	lines := strings.Split(string(raw), "\n")
	found := false
	for i, line := range lines {
		trim := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if strings.HasPrefix(trim, key+"=") {
			lines[i] = key + "=" + value
			found = true
		}
	}
	if !found {
		lines = append(lines, key+"="+value)
	}
	return []byte(strings.Join(lines, "\n"))
}

// pickPort 从 start 起找第一个空闲端口（IPv4 + IPv6 回环都可用）。
func pickPort(start int) int {
	for port := start; port < start+200; port++ {
		if portFree(port) {
			return port
		}
	}
	return start
}

// portFree 判定端口在双栈回环上可绑。
func portFree(port int) bool {
	if port < 1024 || port > 65535 {
		return false
	}
	if ln, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(port)); err == nil {
		_ = ln.Close()
	} else {
		return false
	}
	if ln, err := net.Listen("tcp6", "[::1]:"+strconv.Itoa(port)); err == nil {
		_ = ln.Close()
	}
	return true
}

// waitReady 轮询就绪（支持从 C# stdout 读实际端口——主端口被占时它会回退）。
func waitReady(l *launched, preferredBase string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	base := strings.TrimRight(preferredBase, "/")
	for time.Now().Before(deadline) {
		if !l.stillRunning() {
			return "", fmt.Errorf("进程已退出；stderr=%s", tailFile(l.stderrPath, 800))
		}
		if probeReady(base) {
			return base, nil
		}
		if actual := actualPortFromStdout(l.stdoutPath); actual > 0 {
			candidate := "http://127.0.0.1:" + strconv.Itoa(actual)
			if candidate != base && probeReady(candidate) {
				return candidate, nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return "", fmt.Errorf("等待 %s 超时；stdout=%s", preferredBase, tailFile(l.stdoutPath, 800))
}

// waitReadySimple 轮询就绪（CAT5 端口固定）。
func waitReadySimple(l *launched, base string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !l.stillRunning() {
			return fmt.Errorf("进程已退出；stderr=%s", tailFile(l.stderrPath, 800))
		}
		if probeReady(base) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("等待 %s 超时；stdout=%s", base, tailFile(l.stdoutPath, 800))
}

// waitDepsSettled 等两侧依赖探测收敛（14 条）后再做快照对拍——IO 类探测是后台单飞。
func waitDepsSettled(rep *report, goSide, csSide *sideState, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for {
		gn, gok := depsCount(goSide.base)
		cn, cok := depsCount(csSide.base)
		if gok && cok && gn >= 14 && cn >= 14 {
			rep.add("snapshot", "deps 就绪", stPass, fmt.Sprintf("cat5=%d cs=%d", gn, cn))
			return
		}
		if time.Now().After(deadline) {
			rep.add("snapshot", "deps 就绪", stInfo, fmt.Sprintf("等待超时 cat5=%d cs=%d（按现状对拍）", gn, cn))
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// depsCount 取快照 deps 段条数。
func depsCount(base string) (int, bool) {
	res := doGet(base, "/api/v1/snapshot", 2*time.Second)
	if !res.ok() || res.status != 200 {
		return 0, false
	}
	v, err := parseJSON(res.body)
	if err != nil {
		return 0, false
	}
	deps := v.get("deps")
	if deps == nil || deps.kind != 'a' {
		return 0, false
	}
	return len(deps.arr), true
}

// probeReady 判定快照端点已可解析（空体/未装配不算就绪）。
func probeReady(base string) bool {
	res := doGet(base, "/api/v1/snapshot", 2*time.Second)
	if !res.ok() || res.status != 200 {
		return false
	}
	v, err := parseJSON(res.body)
	if err != nil {
		return false
	}
	return len(objectKeys(v)) >= 10
}

// actualPortFromStdout 从 C# 启动行解析实际监听端口。
func actualPortFromStdout(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	text := string(raw)
	marker := "HTTP 外观层就绪: http://127.0.0.1:"
	idx := strings.LastIndex(text, marker)
	if idx < 0 {
		return 0
	}
	rest := text[idx+len(marker):]
	end := strings.IndexAny(rest, " \r\n|")
	if end <= 0 {
		return 0
	}
	port, convErr := strconv.Atoi(rest[:end])
	if convErr != nil {
		return 0
	}
	return port
}

// attributeRunDirs 找出本次启动新建的 runs 目录（含日志主目录与启动诊断目录）。
func attributeRunDirs(root string, before map[string]bool, wait time.Duration) (dirs []string, primary, startup string) {
	seen := map[string]bool{}
	deadline := time.Now().Add(wait)
	for {
		entries, err := os.ReadDir(root)
		if err == nil {
			for _, e := range entries {
				if !e.IsDir() || before[e.Name()] || seen[e.Name()] {
					continue
				}
				seen[e.Name()] = true
				dirs = append(dirs, e.Name())
			}
		}
		sort.Strings(dirs)
		primary, startup = "", ""
		for _, name := range dirs {
			full := filepath.Join(root, name)
			if primary == "" && fileExists(filepath.Join(full, "log_all.txt")) {
				primary = full
			}
			if startup == "" && fileExists(filepath.Join(full, "startup.log")) {
				startup = full
			}
		}
		if primary != "" && startup != "" {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if primary == "" && len(dirs) > 0 {
		primary = filepath.Join(root, dirs[len(dirs)-1])
	}
	return dirs, primary, startup
}

// listDirs 列出目录下的子目录名集合。
func listDirs(path string) map[string]bool {
	out := map[string]bool{}
	entries, err := os.ReadDir(path)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			out[e.Name()] = true
		}
	}
	return out
}

// fileExists 判定文件存在。
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// tailFile 取文件尾部（错误诊断用）。
func tailFile(path string, max int) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(raw) > max {
		raw = raw[len(raw)-max:]
	}
	return strings.TrimSpace(string(raw))
}

// mark 追加状态行（proc 起服后的过程观测面）。
func mark(outDir, line string) {
	if outDir == "" {
		return
	}
	_ = os.MkdirAll(outDir, 0o755)
	f, err := os.OpenFile(filepath.Join(outDir, "status.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(time.Now().Format("15:04:05.000") + " | " + line + "\r\n")
	_ = f.Close()
	fmt.Println("[status]", line)
}
