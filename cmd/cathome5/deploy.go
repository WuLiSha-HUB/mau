package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// 本文件是 CAT5 部署链（D2）——同 exe 子命令 `cathome5 deploy prepare|deploy|relaunch`。
//
// 语义对齐 ccbp:Project/CatHome5/design-deploy.md §四（三模式与报告）与 §五（五决策点定案）：
//   - prepare：go build / vet / test + 收集 html / corpus / Bricks / config → 暂存区
//   - deploy：暂存区 → <target>_new → 原子切换（target→target_old，_new→target）
//   - relaunch：等旧 pid 退出（≤120s）→ 按模式 prepare / 搬运 / 原地 → 起新实例 → 探活 → 失败回退
//   - 报告：<target>/deploy-report-<ts>.json；退出码四态 0/2/3/1
//
// 探活判据（实测对齐）：GET /api/v1/snapshot 200（协议 version=3）+ 首页正文含目标版本号
// （snapshot 的 version 是协议版本，产品版本经静态页 __V__ 替换暴露——设计文档 §四 的「version 字段」按此落地）。

const (
	// deployExitOK 替换 + 自启成功
	deployExitOK = 0
	// deployExitRolledBack 已回退（旧版本继续运行）
	deployExitRolledBack = 2
	// deployExitNotReplaced 未替换，以现有版本运行
	deployExitNotReplaced = 3
	// deployExitManual 需人工介入
	deployExitManual = 1
)

// deployOptions 是三模式的共用参数（未用到的模式忽略多余参数）。
type deployOptions struct {
	// Target 运行区目录
	Target string
	// Repo 施工仓根（cwd = 此处执行 go 构建与测试）
	Repo string
	// Version 版本覆盖（空 = 读 <repo>/version.txt）
	Version string
	// Staging 暂存区（空 = <target>_staging）
	Staging string
	// HTML 前端源目录
	HTML string
	// Corpus 语料源目录（ch4）
	Corpus string
	// Bricks 积木源目录
	Bricks string
	// ConfigSrc 配置模板源目录
	ConfigSrc string
	// GoExe go 可执行文件（空 = PATH 或 tools/go）
	GoExe string
	// Mode relaunch 模式：full / incr / host
	Mode string
	// WaitPID 起新实例前等待退出的旧进程 pid（0 = 不等）
	WaitPID int
	// Port 起新实例的端口（0 = 用部署区段默认）
	Port int
	// DataRoot 起新实例的数据根（空 = 默认锚定）
	DataRoot string
	// SkipTests prepare 跳过 go test
	SkipTests bool
	// ProbeTimeout 探活超时（秒）
	ProbeTimeout int
}

// deployStep 报告步骤。
type deployStep struct {
	// Name 步骤名
	Name string `json:"name"`
	// OK 是否通过
	OK bool `json:"ok"`
	// Detail 明细
	Detail string `json:"detail"`
}

// deployReport 部署报告（<target>/deploy-report-<ts>.json）。
type deployReport struct {
	// TS 报告时刻
	TS string `json:"ts"`
	// Mode prepare / deploy / relaunch
	Mode string `json:"mode"`
	// OK 整体成败
	OK bool `json:"ok"`
	// ExitCode 退出码四态
	ExitCode int `json:"exitCode"`
	// Steps 步骤明细
	Steps []deployStep `json:"steps"`
	// Artifacts 产物位置
	Artifacts map[string]string `json:"artifacts"`
}

// deployRun 是一次部署执行的上下文。
type deployRun struct {
	// o 参数
	o deployOptions
	// mode 子命令
	mode string
	// steps 已执行步骤
	steps []deployStep
	// artifacts 产物
	artifacts map[string]string
}

// add 记一步。
func (r *deployRun) add(name string, ok bool, detail string) {
	r.steps = append(r.steps, deployStep{Name: name, OK: ok, Detail: detail})
	fmt.Println("[" + name + "] " + detail)
}

// stagingDir 暂存区路径。
func (r *deployRun) stagingDir() string {
	if r.o.Staging != "" {
		return r.o.Staging
	}
	return r.o.Target + "_staging"
}

// version 版本号——flag > <repo>/version.txt。
func (r *deployRun) version() (string, error) {
	if r.o.Version != "" {
		return r.o.Version, nil
	}
	raw, err := os.ReadFile(filepath.Join(r.o.Repo, "version.txt"))
	if err != nil {
		return "", fmt.Errorf("读取 version.txt 失败: %w", err)
	}
	v := strings.TrimSpace(strings.TrimPrefix(string(raw), "\ufeff"))
	if v == "" {
		return "", fmt.Errorf("version.txt 为空")
	}
	return v, nil
}

// run 按模式执行。
func (r *deployRun) run() int {
	r.artifacts = map[string]string{
		"target":  r.o.Target,
		"staging": r.stagingDir(),
	}
	switch r.mode {
	case "prepare":
		if err := r.prepare(); err != nil {
			r.add("prepare", false, err.Error())
			return deployExitManual
		}
		return deployExitOK
	case "deploy":
		if err := r.deploy(); err != nil {
			r.add("deploy", false, err.Error())
			return deployExitNotReplaced
		}
		return deployExitOK
	case "relaunch":
		return r.relaunch()
	}
	r.add("mode", false, "未知模式: "+r.mode)
	return deployExitManual
}

// prepare 构建 + 测试 + 收集产物到暂存区。
func (r *deployRun) prepare() error {
	version, err := r.version()
	if err != nil {
		return err
	}
	staging := r.stagingDir()
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return fmt.Errorf("创建暂存区失败: %w", err)
	}
	goExe, err := r.resolveGo()
	if err != nil {
		return err
	}
	r.add("version", true, version)
	// [段1] 构建——版本经 ldflags 注入（版本单点 = version.txt）
	exePath := filepath.Join(staging, "cathome5.exe")
	if out, err := runGo(goExe, r.o.Repo, "build", "-ldflags", "-X main.version="+version, "-o", exePath, "./cmd/cathome5"); err != nil {
		return fmt.Errorf("go build 失败: %v（%s）", err, out)
	}
	r.add("build", true, exePath)
	// [段2] 静态检查 + 跑测（可跳）
	if out, err := runGo(goExe, r.o.Repo, "vet", "./..."); err != nil {
		return fmt.Errorf("go vet 失败: %v（%s）", err, out)
	}
	r.add("vet", true, "退出 0")
	if r.o.SkipTests {
		r.add("test", true, "跳过（--skip-tests）")
	} else {
		out, err := runGo(goExe, r.o.Repo, "test", "./...", "-count=1")
		if err != nil {
			return fmt.Errorf("go test 失败: %v（%s）", err, out)
		}
		r.add("test", true, "全部包通过")
	}
	// [段3] 产物收集——部署包自持 corpus / Bricks / html / config（决策 3）
	if err := os.WriteFile(filepath.Join(staging, "version.txt"), []byte(version+"\r\n"), 0o644); err != nil {
		return fmt.Errorf("写 version.txt 失败: %w", err)
	}
	for _, item := range []struct {
		name string
		src  string
		dst  string
	}{
		{"html", r.o.HTML, filepath.Join(staging, "html")},
		{"corpus", r.o.Corpus, filepath.Join(staging, "corpus", "ch4")},
		{"Bricks", r.o.Bricks, filepath.Join(staging, "Bricks")},
		{"config", r.o.ConfigSrc, filepath.Join(staging, "config")},
	} {
		if item.src == "" {
			continue
		}
		if _, err := os.Stat(item.src); err != nil {
			return fmt.Errorf("源目录不可达（%s）: %s", item.name, item.src)
		}
		if err := os.RemoveAll(item.dst); err != nil {
			return fmt.Errorf("清理旧产物失败（%s）: %w", item.name, err)
		}
		if err := copyTree(item.src, item.dst); err != nil {
			return fmt.Errorf("复制失败（%s）: %w", item.name, err)
		}
		files, _ := countTree(item.dst)
		r.add(item.name, true, item.src+" → "+item.dst+"（"+strconv.Itoa(files)+" 文件）")
	}
	r.artifacts["exe"] = exePath
	r.artifacts["version"] = version
	return nil
}

// deploy 暂存区 → target_new → 原子切换。
func (r *deployRun) deploy() error {
	staging := r.stagingDir()
	for _, name := range []string{"cathome5.exe", "version.txt"} {
		if _, err := os.Stat(filepath.Join(staging, name)); err != nil {
			return fmt.Errorf("暂存区缺 %s（先跑 deploy prepare）", name)
		}
	}
	version, err := readVersionFile(filepath.Join(staging, "version.txt"))
	if err != nil {
		return err
	}
	targetNew := r.o.Target + "_new"
	targetOld := r.o.Target + "_old"
	if err := os.RemoveAll(targetNew); err != nil {
		return fmt.Errorf("清理 %s 失败: %w", targetNew, err)
	}
	if err := copyTree(staging, targetNew); err != nil {
		return fmt.Errorf("复制到 %s 失败: %w", targetNew, err)
	}
	r.add("stage", true, staging+" → "+targetNew)
	// 原子切换：同卷改名（target→_old，_new→target）
	if _, err := os.Stat(r.o.Target); err == nil {
		if err := os.RemoveAll(targetOld); err != nil {
			return fmt.Errorf("清理 %s 失败: %w", targetOld, err)
		}
		if err := os.Rename(r.o.Target, targetOld); err != nil {
			return fmt.Errorf("切换失败（target→_old）: %w", err)
		}
		r.add("switch", true, r.o.Target+" → "+targetOld)
	}
	if err := os.Rename(targetNew, r.o.Target); err != nil {
		// 换回旧版本，避免目标目录缺失
		if _, statErr := os.Stat(targetOld); statErr == nil {
			_ = os.Rename(targetOld, r.o.Target)
		}
		return fmt.Errorf("切换失败（_new→target）: %w", err)
	}
	r.add("switch", true, targetNew+" → "+r.o.Target+"（版本 "+version+"）")
	r.artifacts["version"] = version
	return nil
}

// relaunch 等旧进程 → 按模式准备 → 起新实例 → 探活 → 失败回退。
func (r *deployRun) relaunch() int {
	mode := r.o.Mode
	if mode == "" {
		mode = "incr"
	}
	// [段1] 等旧 pid 退出（≤120s）
	if r.o.WaitPID > 0 {
		if err := waitProcessExit(r.o.WaitPID, 120*time.Second); err != nil {
			r.add("wait", false, err.Error())
			return deployExitNotReplaced
		}
		r.add("wait", true, "旧进程 "+strconv.Itoa(r.o.WaitPID)+" 已退出")
	}
	// [段2] 按模式准备产物
	switch mode {
	case "full":
		if err := r.prepare(); err != nil {
			r.add("prepare", false, err.Error())
			return deployExitNotReplaced
		}
		if err := r.deploy(); err != nil {
			r.add("deploy", false, err.Error())
			return deployExitNotReplaced
		}
	case "incr":
		if err := r.deploy(); err != nil {
			r.add("deploy", false, err.Error())
			return deployExitNotReplaced
		}
	case "host":
		r.add("mode", true, "host——原地起新实例（不替换产物）")
	default:
		r.add("mode", false, "mode 非法（full / incr / host）: "+mode)
		return deployExitManual
	}
	// [段3] 起新实例 + 探活
	version, err := readVersionFile(filepath.Join(r.o.Target, "version.txt"))
	if err != nil {
		r.add("version", false, err.Error())
		return deployExitManual
	}
	cmd, err := r.startInstance()
	if err != nil {
		r.add("start", false, err.Error())
		return r.rollback(version, "起新实例失败")
	}
	r.add("start", true, "pid="+strconv.Itoa(cmd.Process.Pid)+" · 版本 "+version)
	if err := r.probe(version); err != nil {
		r.add("probe", false, err.Error())
		_ = cmd.Process.Kill()
		return r.rollback(version, "探活失败: "+err.Error())
	}
	r.add("probe", true, "snapshot 200 + 首页版本 "+version)
	r.artifacts["pid"] = strconv.Itoa(cmd.Process.Pid)
	return deployExitOK
}

// rollback 回退旧版本并起旧实例。
func (r *deployRun) rollback(failedVersion, reason string) int {
	targetOld := r.o.Target + "_old"
	if _, err := os.Stat(targetOld); err != nil {
		r.add("rollback", false, "无旧版本可回退（"+reason+"）")
		return deployExitManual
	}
	targetFailed := r.o.Target + "_failed"
	_ = os.RemoveAll(targetFailed)
	if err := os.Rename(r.o.Target, targetFailed); err != nil {
		r.add("rollback", false, "target→_failed 失败: "+err.Error())
		return deployExitManual
	}
	if err := os.Rename(targetOld, r.o.Target); err != nil {
		r.add("rollback", false, "_old→target 失败: "+err.Error())
		return deployExitManual
	}
	oldVersion, _ := readVersionFile(filepath.Join(r.o.Target, "version.txt"))
	r.add("rollback", true, "已回退到 "+oldVersion+"（失败版本 "+failedVersion+" 留在 "+targetFailed+"）")
	cmd, err := r.startInstance()
	if err != nil {
		r.add("start-old", false, err.Error())
		return deployExitManual
	}
	if err := r.probe(oldVersion); err != nil {
		r.add("probe-old", false, err.Error())
		return deployExitManual
	}
	r.add("probe-old", true, "回退实例可用 · pid="+strconv.Itoa(cmd.Process.Pid))
	return deployExitRolledBack
}

// startInstance 起部署区实例（detached，不随本进程退出）。
func (r *deployRun) startInstance() (*exec.Cmd, error) {
	exe := filepath.Join(r.o.Target, "cathome5.exe")
	if _, err := os.Stat(exe); err != nil {
		return nil, fmt.Errorf("运行区缺 cathome5.exe: %s", exe)
	}
	args := []string{"--area", "deploy"}
	if r.o.Port > 0 {
		args = append(args, "--port", strconv.Itoa(r.o.Port))
	}
	if r.o.DataRoot != "" {
		args = append(args, "--data-root", r.o.DataRoot)
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = r.o.Target
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000008}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// probe 探活——snapshot 200（协议 version=3）+ 首页含目标版本。
func (r *deployRun) probe(version string) error {
	port := r.o.Port
	if port == 0 {
		port = 8080
	}
	deadline := time.Now().Add(time.Duration(r.probeTimeoutSeconds()) * time.Second)
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	var lastErr error
	for time.Now().Before(deadline) {
		ok, err := probeOnce(base, version)
		if ok {
			return nil
		}
		lastErr = err
		time.Sleep(400 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("超时")
	}
	return fmt.Errorf("%s（%s）", base, lastErr.Error())
}

// probeTimeoutSeconds 探活超时（默认 30s）。
func (r *deployRun) probeTimeoutSeconds() int {
	if r.o.ProbeTimeout > 0 {
		return r.o.ProbeTimeout
	}
	return 30
}

// probeOnce 单次探活。
func probeOnce(base, version string) (bool, error) {
	snap, err := httpGet(base + "/api/v1/snapshot")
	if err != nil {
		return false, err
	}
	if !strings.Contains(snap, `"version":3`) {
		return false, fmt.Errorf("snapshot 协议版本不符")
	}
	home, err := httpGet(base + "/")
	if err != nil {
		return false, err
	}
	if !strings.Contains(home, "v="+version) && !strings.Contains(home, "__V__") {
		return false, fmt.Errorf("首页未携带版本 %s", version)
	}
	if strings.Contains(home, "__V__") {
		return false, fmt.Errorf("首页版本占位未替换")
	}
	return true, nil
}

// httpGet 取正文（3s 超时）。
func httpGet(url string) (string, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return string(raw), nil
}

// waitProcessExit 轮询等进程退出（tasklist 判活）。
func waitProcessExit(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("等待旧进程 %d 退出超时（%s）", pid, timeout)
}

// processAlive tasklist 判活（PID 精确匹配）。
func processAlive(pid int) bool {
	out, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/FO", "CSV", "/NH").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "\""+strconv.Itoa(pid)+"\"")
}

// resolveGo 解析 go 可执行文件——flag > PATH > 仓库同级 tools/go/bin/go.exe。
func (r *deployRun) resolveGo() (string, error) {
	if r.o.GoExe != "" {
		return r.o.GoExe, nil
	}
	if path, err := exec.LookPath("go"); err == nil {
		return path, nil
	}
	candidate := filepath.Join(filepath.Dir(filepath.Dir(r.o.Repo)), "tools", "go", "bin", "go.exe")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	// 施工仓同级 tools（<repo>/../tools/go/bin/go.exe）
	candidate = filepath.Join(r.o.Repo, "..", "tools", "go", "bin", "go.exe")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	return "", fmt.Errorf("未找到 go（--go 指定或加入 PATH）")
}

// runGo 执行 go 子命令并合并输出。
func runGo(goExe, dir string, args ...string) (string, error) {
	cmd := exec.Command(goExe, args...)
	cmd.Dir = dir
	raw, err := cmd.CombinedOutput()
	return string(raw), err
}

// readVersionFile 读 version.txt（去 BOM 与空白）。
func readVersionFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	v := strings.TrimSpace(strings.TrimPrefix(string(raw), "\ufeff"))
	if v == "" {
		return "", fmt.Errorf("%s 为空", path)
	}
	return v, nil
}

// copyTree 整棵复制（文件权限保留；重解析点按普通文件读，失败出声）。
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, info.Mode().Perm())
	})
}

// countTree 统计目录内文件数。
func countTree(root string) (int, error) {
	count := 0
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			count++
		}
		return nil
	})
	return count, err
}

// writeReport 落报告 JSON——target 存在写 target，否则写父目录（prepare 首跑 target 尚未创建）。
func (r *deployRun) writeReport(mode string, code int) {
	report := deployReport{
		TS:        time.Now().Format("2006-01-02 15:04:05"),
		Mode:      mode,
		OK:        code == deployExitOK || code == deployExitRolledBack,
		ExitCode:  code,
		Steps:     r.steps,
		Artifacts: r.artifacts,
	}
	dir := r.o.Target
	if _, err := os.Stat(dir); err != nil {
		dir = r.stagingDir()
	}
	if _, err := os.Stat(dir); err != nil {
		dir = filepath.Dir(r.o.Target)
	}
	path := filepath.Join(dir, "deploy-report-"+time.Now().Format("20060102_150405")+".json")
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Println("报告序列化失败:", err)
		return
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		fmt.Println("报告写入失败:", err, path)
		return
	}
	fmt.Println("报告:", path)
}

// runDeploy 子命令入口——由 main 在 flag.Parse 前分派。
func runDeploy(args []string) int {
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	o := deployOptions{
		Target:       defaultDeployTarget(),
		Repo:         `D:\Mau\WorkSpace\CatHome5`,
		HTML:         `D:\Mau\Mau\CatHome4\html`,
		Corpus:       `D:\Mau\Mau\corpus\ch4`,
		Bricks:       `D:\Mau\Mau\Bricks`,
		ConfigSrc:    `D:\Mau\Mau\config`,
		ProbeTimeout: 30,
	}
	registerDeployFlags(fs, &o)
	// 子命令可前可后：`deploy prepare --target X`（flag 前）或 `deploy --target X prepare`（flag 后）
	mode := "relaunch"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		mode = args[0]
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return deployExitManual
	}
	if fs.NArg() > 0 {
		candidate := fs.Arg(0)
		if candidate == "prepare" || candidate == "deploy" || candidate == "relaunch" {
			mode = candidate
		}
	}
	switch mode {
	case "prepare", "deploy", "relaunch":
	default:
		fmt.Println("用法: cathome5 deploy prepare|deploy|relaunch [flags]")
		return deployExitManual
	}
	r := &deployRun{o: o, mode: mode}
	code := r.run()
	r.writeReport(mode, code)
	fmt.Println("deploy " + mode + " 退出码 " + strconv.Itoa(code))
	return code
}

// defaultDeployTarget 运行区默认位置（决策 2：D:\Mau\MauOut5）。
func defaultDeployTarget() string {
	if value := os.Getenv("CAT5_DEPLOY_TARGET"); value != "" {
		return value
	}
	return `D:\Mau\MauOut5`
}

// registerDeployFlags 注册部署参数（子命令可前可后，解析前统一注册一次）。
func registerDeployFlags(fs *flag.FlagSet, o *deployOptions) {
	fs.StringVar(&o.Target, "target", o.Target, "运行区目录（默认 D:\\Mau\\MauOut5）")
	fs.StringVar(&o.Repo, "repo", o.Repo, "施工仓根（go 构建与测试的 cwd）")
	fs.StringVar(&o.Version, "version", "", "版本覆盖（空 = 读 <repo>/version.txt）")
	fs.StringVar(&o.Staging, "staging", "", "暂存区（空 = <target>_staging）")
	fs.StringVar(&o.HTML, "html", o.HTML, "前端源目录")
	fs.StringVar(&o.Corpus, "corpus", o.Corpus, "语料源目录（ch4）")
	fs.StringVar(&o.Bricks, "bricks", o.Bricks, "积木源目录")
	fs.StringVar(&o.ConfigSrc, "config", o.ConfigSrc, "配置模板源目录")
	fs.StringVar(&o.GoExe, "go", "", "go 可执行文件（空 = PATH / tools/go）")
	fs.StringVar(&o.Mode, "mode", "incr", "relaunch 模式：full / incr / host")
	fs.IntVar(&o.WaitPID, "wait-pid", 0, "起新实例前等待退出的旧进程 pid")
	fs.IntVar(&o.Port, "port", 0, "起新实例端口（0 = 部署区段默认 8080）")
	fs.StringVar(&o.DataRoot, "data-root", "", "起新实例的数据根（空 = 默认锚定）")
	fs.BoolVar(&o.SkipTests, "skip-tests", false, "prepare 跳过 go test")
	fs.IntVar(&o.ProbeTimeout, "probe-timeout", 30, "探活超时秒数")
}
