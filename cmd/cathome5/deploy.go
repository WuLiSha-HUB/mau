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

	"cathome5/host/admin"
)

// 本文件是 CAT5 部署链（D2）——同 exe 子命令 `cathome5 deploy prepare|deploy|relaunch`。
//
// 语义对齐 ccbp:Project/CatHome5/design-deploy.md §四（三模式与报告）与 §五（五决策点定案）：
//   - prepare：go build / vet / test + 收集 html / corpus / Bricks / config / csbridge → 暂存区
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
	// csBridgeExeName cs 桥可执行文件名（与 mau/bricks/cs_service.go 的候选链同名）
	csBridgeExeName = "MauCsBridge.exe"
)

// staleRescanDelay 陈旧实例清退后的复扫窗口——外部监督者（并发协议面板 auto）可能在毫秒级重新拉起实例。
//
// 可变（非 const）：单测置 0 去掉等待。
var staleRescanDelay = 600 * time.Millisecond

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
	// CsBridge cs 桥产物源目录（空 = <repo>/host/csbridge/bin/Debug/net10.0）
	CsBridge string
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
	// Area 区覆盖（relaunch 起新实例用；空 = deploy）
	Area string
	// Page 页面覆盖（relaunch 起新实例用；空 = 管理面板）
	Page string
	// PushIn 重启后附加说明串（relaunch —— 由宿主 restart 请求透传，随回执注入新宿主）
	PushIn string
	// Report 报告路径覆盖（空 = 单点解析：target → staging → target 父目录）
	Report string
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

// deployReport 部署报告（单点路径：target → staging → target 父目录；见 resolveReportPath）。
type deployReport struct {
	// TS 报告时刻
	TS string `json:"ts"`
	// Mode prepare / deploy / relaunch
	Mode string `json:"mode"`
	// OK 整体成败
	OK bool `json:"ok"`
	// ExitCode 退出码四态
	ExitCode int `json:"exitCode"`
	// Complete 终版标记（false = 前置版——自启前先落盘，读者任何时刻都能读到文件）
	Complete bool `json:"complete"`
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
	// reportPath 报告单点路径（runDeploy 起算一次——写入与回执引用同一路径）
	reportPath string
	// startedAt 本次执行起算时刻（重启用时 = 接力起算 → 新宿主拉起前一刻）
	startedAt time.Time
	// switchedAt 本次原子切换完成时刻（零值 = 运行区未替换）
	//
	// 判据分界：晚于它创建的宿主实例加载的是新产物（承载服务可跳过自启）；
	// 早于它创建的实例映射旧映像（陈旧——必须清退，否则报告 exit0 而内存跑旧代码）。
	switchedAt time.Time
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
	r.startedAt = time.Now()
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
	if r.o.Repo == "" {
		return fmt.Errorf("未定位施工仓（prepare 需 go.mod 作为构建 cwd）——用 --repo 或环境变量 CAT5_REPO 指定")
	}
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
		name   string
		flag   string
		target string
		dst    string
	}{
		{"html", r.o.HTML, "html", filepath.Join(staging, "html")},
		{"corpus", r.o.Corpus, filepath.Join("corpus", "ch4"), filepath.Join(staging, "corpus", "ch4")},
		{"Bricks", r.o.Bricks, "Bricks", filepath.Join(staging, "Bricks")},
		{"config", r.o.ConfigSrc, "config", filepath.Join(staging, "config")},
	} {
		src, err := r.resolveProductSource(item.name, item.flag, item.target)
		if err != nil {
			return err
		}
		if err := os.RemoveAll(item.dst); err != nil {
			return fmt.Errorf("清理旧产物失败（%s）: %w", item.name, err)
		}
		if err := copyTree(src, item.dst); err != nil {
			return fmt.Errorf("复制失败（%s）: %w", item.name, err)
		}
		files, _ := countTree(item.dst)
		r.add(item.name, true, src+" → "+item.dst+"（"+strconv.Itoa(files)+" 文件）")
	}
	r.artifacts["exe"] = exePath
	r.artifacts["version"] = version
	// [段4] cs 桥产物——部署包自持桥（部署实例 cs-* 可用）；缺失出声跳过（不阻断主部署链）
	if err := r.collectCsBridge(staging); err != nil {
		return err
	}
	return nil
}

// resolveProductSource 产物源解析——显式 flag > 运行区现有（沿用）> 报错。
//
// 顺序理由：仓库当前不自持 html / corpus / Bricks / config 源（2026-11-14 实测仓库根无这四个目录，
// `mau/bricks` 是 Go 包不是积木源），运行区那份就是唯一真相；沿用它是保真——
// 运行区里手改过的前端（如 chat/blocks/text.js）不会被一个空源顶掉。
// 显式 flag 仍可覆盖：跨机部署 / 从别处取源 / 想把运行区当基线重建时用。
func (r *deployRun) resolveProductSource(name, explicit, targetRel string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if r.o.Target != "" && targetRel != "" {
		candidate := filepath.Join(r.o.Target, targetRel)
		if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("源目录缺失（%s）：未给 --%s，运行区 %s 下也没有 %s——显式指定该 flag 或先建运行区",
		name, strings.ToLower(name), r.o.Target, targetRel)
}

// resolveCsBridgeSource 桥产物源目录——显式 --csbridge > repo 内产物 > 运行区现有（沿用）。
//
// 与四个产物源同口径（resolveProductSource）：仓库不自持桥产物时，运行区那份就是唯一真相。
// 差异保留：桥「缺失」仍不阻断主部署链（只出声跳过）——只改 Go 面时桥本就不重编。
func (r *deployRun) resolveCsBridgeSource() string {
	if r.o.CsBridge != "" {
		return r.o.CsBridge
	}
	repoSide := filepath.Join(r.o.Repo, "host", "csbridge", "bin", "Debug", "net10.0")
	if _, err := os.Stat(filepath.Join(repoSide, csBridgeExeName)); err == nil {
		return repoSide
	}
	if r.o.Target != "" {
		targetSide := filepath.Join(r.o.Target, "csbridge")
		if _, err := os.Stat(filepath.Join(targetSide, csBridgeExeName)); err == nil {
			return targetSide
		}
	}
	return repoSide
}

// collectCsBridge 收集 cs 桥产物到暂存区 csbridge/（部署实例起服走 <exeDir>/csbridge/ 候选链）。
//
// 缺失出声跳过（桥是 .NET 侧产物，未编桥 / 只改 Go 面属常见中间态）——不阻断主部署链；
// 部署实例此时 cs-* 按既有明示语义回 CSHARP_NO_BRIDGE，可用 --csbridge 指定源目录。
func (r *deployRun) collectCsBridge(staging string) error {
	src := r.resolveCsBridgeSource()
	if _, err := os.Stat(filepath.Join(src, csBridgeExeName)); err != nil {
		r.add("csbridge", false, "跳过——桥产物缺失（"+src+"）；部署实例 cs-* 将回 CSHARP_NO_BRIDGE（可 --csbridge 指定）")
		return nil
	}
	dst := filepath.Join(staging, "csbridge")
	if err := os.RemoveAll(dst); err != nil {
		return fmt.Errorf("清理旧产物失败（csbridge）: %w", err)
	}
	if err := copyTree(src, dst); err != nil {
		return fmt.Errorf("复制失败（csbridge）: %w", err)
	}
	files, _ := countTree(dst)
	r.add("csbridge", true, src+" → "+dst+"（"+strconv.Itoa(files)+" 文件）")
	r.artifacts["csbridge"] = dst
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
		// 重试 20×500 ms = 10 s 窗口（瞬时占用重试即过；常驻占用重试到底再报，附带次数）
		if err := renameWithRetry(r.o.Target, targetOld, 20, 500*time.Millisecond); err != nil {
			return fmt.Errorf("切换失败（target→_old，已重试 20×500 ms）: %w；%s", err, cwdHolderDiagnostics(r.o.Target))
		}
		r.add("switch", true, r.o.Target+" → "+targetOld)
	}
	if err := renameWithRetry(targetNew, r.o.Target, 10, 500*time.Millisecond); err != nil {
		// 换回旧版本，避免目标目录缺失
		if _, statErr := os.Stat(targetOld); statErr == nil {
			_ = os.Rename(targetOld, r.o.Target)
		}
		return fmt.Errorf("切换失败（_new→target，已重试 10×500 ms）: %w；%s", err, cwdHolderDiagnostics(r.o.Target))
	}
	// 切换完成时刻 = 新产物在 target 路径可见的那一瞬——自启前检的判据分界（见 relaunch 段4c）。
	r.switchedAt = time.Now()
	r.add("switch", true, targetNew+" → "+r.o.Target+"（版本 "+version+"）")
	r.artifacts["version"] = version
	return nil
}

// relaunch 等旧进程 → 按模式准备 → 起新实例 → 探活 → 失败回退（退出码四态）。
//
// 对齐 CH4 T5：prepare 未通过 → 跳过 deploy、运行区保持现状（新宿主仍起来，版本不变）；
// 已替换但新版本起不来 → 回退 _old；新版本与回退版本均起不来 → 需人工介入。
func (r *deployRun) relaunch() (exitCode int) {
	// 部署结论写进重启现场（relay 侧最后一步）——重启链走完 ≠ 运行区被替换，
	// 两者必须分开可查（2026-11-14 17:11 判例：exit=3 未替换 + intent done）。
	// 单一来源 = 部署报告 exitCode：先落终版报告，intent 按报告回写（不再拿内存推测值写 intent——
	// 0.36.8 判例：stale 场景 intent deployExit=0 而部署报告 exitCode=3，两处口径不一致）。
	defer func() {
		r.writeReport("relaunch", exitCode, true)
		r.reportDeployResult()
	}()
	mode := r.o.Mode
	if mode == "" {
		mode = "incr"
	}
	if mode != "full" && mode != "incr" && mode != "host" {
		r.add("mode", false, "mode 非法（full / incr / host）: "+mode)
		return deployExitManual
	}
	// [段1] 等旧 pid 退出（≤120s）——未退出 = 旧宿主仍在服务，中止且不触碰运行区
	if r.o.WaitPID > 0 {
		if err := waitProcessExit(r.o.WaitPID, 120*time.Second); err != nil {
			r.add("wait", false, err.Error())
			// 失败留盘由收尾 defer 统一承担（writeReport complete=true）——报告面必须能区分「部分成功」与「完全成功」。
			return deployExitNotReplaced
		}
		r.add("wait", true, "旧进程 "+strconv.Itoa(r.o.WaitPID)+" 已退出")
		// 句柄释放 settle——旧宿主的子进程与其自身句柄在进程消失后仍需片刻才完全关闭；
		// 不给窗口直接改名会撞「being used by another process」（2026-11-14 17:11 判例）
		time.Sleep(1500 * time.Millisecond)
		r.add("settle", true, "等句柄释放 1.5 s（旧宿主子进程 / 目录句柄）")
	}
	// [段1b] 同目录残留实例清扫（CH4 SetUp/Program.Relaunch.cs 段1b / KillHostsInDirectory 对位）——
	//        只有发起方 pid 会自退；历次失败自启 / 手动启动的实例仍持 dll 与目录 cwd → 原子切换改名必失败，
	//        它们都是待替换运行区里的旧实例，属部署链自身的清理面（只清「可执行路径落在 target 下」的实例）。
	//        先优雅关（≤2 s）、再强杀（≤3 s）、有强杀则 settle 1 s；命中与处置进报告 step，失败不阻断。
	// host 模式不做清扫：不替换运行区、不需要改名；且该模式下运行区里的实例可能就是用户手动起的那个。
	if mode != "host" {
		r.sweepStrayHosts()
		// 第二面：py / ts / browser 服务进程（cwd 落运行区 = 目录级句柄）——宿主 cwd 已改数据根后，
		// 仍可能留着旧宿主拉起的子进程（browser 内核 detach / py 服务崩溃残留），rename 必被它们挡住。
		// 判据两条同时成立才处置：映像名在服务清单（不碰用户进程）+ cwd 落在运行区。
		r.sweepStrayServices()
	}
	// [段2] prepare 全链——仅 full（失败即不替换运行区）
	prepOK := true
	if mode == "full" {
		if err := r.prepare(); err != nil {
			prepOK = false
			r.add("prepare", false, err.Error())
		}
	} else {
		r.add("prepare", true, "跳过（mode="+mode+"）")
	}
	// [段2.5] 切换前复扫——段1b 的清扫与 rename 之间隔着整个 prepare（数十秒）：外部监督者
	// （并发协议面板 auto=true）会在此期间把离线的宿主自动拉起（2026-11-15 判例：12:31 登记重启 →
	// 12:32:06 面板拉起 pid 37772 持运行区 → rename 必失败）。清扫窗口必须紧邻 rename。
	if mode != "host" {
		r.sweepStrayHostsBeforeSwitch()
		r.sweepStrayServicesBeforeSwitch()
	}
	// [段3] 替换运行区——full：prepare 通过才 deploy；incr：纯搬运；host：跳过（原地重启）
	replaced := false
	switch mode {
	case "full":
		if prepOK {
			if err := r.deploy(); err != nil {
				r.add("deploy", false, err.Error())
			} else {
				replaced = true
			}
		} else {
			r.add("deploy", true, "跳过（prepare 未通过——运行区未替换）")
		}
	case "incr":
		if err := r.deploy(); err != nil {
			r.add("deploy", false, err.Error())
		} else {
			replaced = true
		}
	case "host":
		r.add("mode", true, "host——原地起新实例（不替换产物）")
	}
	// [段4] 拼回执注入串 + 前置报告（回执由部署链生成——非 LLM 自述）
	version := ""
	if v, err := readVersionFile(filepath.Join(r.o.Target, "version.txt")); err == nil {
		version = v
	} else {
		r.add("version", false, err.Error())
	}
	push := buildRestartPush(mode, prepOK, replaced, version, r.o.Target, r.o.PushIn, r.ensureReportPath(), time.Since(r.startedAt))
	r.writeReport("relaunch", -1, false)
	// [段4-intent] 登记部署报告路径——必须在起新实例之前：新宿主启动即 Settle，
	// 现场要能指到「本次部署的报告」（部署结论的单一来源 = 报告 exitCode）。
	// 只登记路径、不写预测值：0.36.8 判例（stale 场景 intent deployExit=0 / 报告 exitCode=3）
	// 的根因就是拿内存推测值先写结论；未收尾时现场保持 deployReported=false（未上报 ≠ 成功）。
	r.reportDeployReportPath()
	// [段4c] 自启前检（CH4 1.8.266 同因）——目标运行区已有宿主实例在跑：跳过自启，不新增第二个实例。
	//        判例 2026-11-13：deploy 原子切换失败后自启又拉起一个，运行区并存两个宿主（服务假象 + dll 持锁）。
	//        🔴 判例 2026-11-16（G32 假绿）：「路径落在运行区」不等于「跑的是新产物」——外部监督者
	//        （并发协议面板 auto=true，pid 10700）在 switch 前从旧目录拉起 pid 33316，切换后其映像路径仍显示
	//        target\cathome5.exe，内存里却是旧映像；上一版按「有实例」跳过自启 → 报告 exit0 而行为照旧。
	//        判据：进程创建时刻 vs 本次切换完成时刻（段3 记录的 switchedAt）——早于切换的一律清退。
	bootOK := false
	bootPID := 0
	if r.autostartPrecheck() {
		// [段5] 起新实例（带 --majordomopush）+ 探活
		bootOK, bootPID = r.bootAndProbe(version, push)
	} else {
		bootOK = true
	}
	// [段4d] 自启自检——本次拉起的新实例创建时刻必须晚于切换完成时刻（否则映射的是旧映像）。
	r.verifyBootedFromNewArtifact(bootPID)
	// [段6] 回退——仅本次已替换时 _old 才是有效回退源（最坏 = 原地重启）
	if !bootOK && replaced {
		return r.rollback(version, "自启 / 探活失败")
	}
	if !bootOK {
		r.add("fallback", false, "无有效回退源（本次未替换运行区）——需人工介入")
		return deployExitManual
	}
	if replaced || mode == "host" {
		return deployExitOK
	}
	return deployExitNotReplaced
}

// autostartPrecheck 自启前检（段4c）——返回 true = 无承载实例，需要起新实例。
//
// 三步：① 按「创建时刻 vs 切换完成时刻」把运行区实例分成承载（新产物）/ 陈旧（旧映像）；
// ② 陈旧实例定向清退（它们占着「已有实例」的名义，实际跑的是被替换掉的旧代码）；
// ③ 清退后复扫一次——外部监督者可能在实例离线瞬间拉起新实例（创建晚于切换 = 新产物），直接接手。
func (r *deployRun) autostartPrecheck() bool {
	hosts, scanErr := hostScanFindInDirDetail(r.o.Target)
	if scanErr != nil {
		fmt.Println("[自启前检] 宿主进程扫描失败（按无实例继续）: " + scanErr.Error())
	}
	for _, host := range hosts {
		if host.CreatedAt.IsZero() {
			fmt.Println("[自启前检] pid " + strconv.Itoa(host.PID) + " 创建时刻不可读（按承载处理，不误杀无法取证的进程）")
		}
	}
	fresh, stale := splitHostsBySwitchTime(hosts, r.switchedAt)
	if len(stale) > 0 {
		swept := hostKillByPID(hostPIDsOf(stale))
		detail := "另结束 " + strconv.Itoa(swept.Graceful+swept.Killed) + " 个切换前创建的同目录宿主实例（pid " +
			joinHostPIDs(swept.PIDs) + "；优雅关 " + strconv.Itoa(swept.Graceful) + " / 强杀 " +
			strconv.Itoa(swept.Killed) + "）——映像早于本次切换，跑的是旧产物"
		if swept.Failed > 0 {
			detail += "；失败 " + strconv.Itoa(swept.Failed) + " 个（见日志）"
		}
		r.add("陈旧实例清退（切换前创建）", swept.Failed == 0, detail)
		time.Sleep(staleRescanDelay)
		if rescan, err := hostScanFindInDirDetail(r.o.Target); err == nil {
			if adopted, _ := splitHostsBySwitchTime(rescan, r.switchedAt); len(adopted) > 0 {
				fresh = adopted
			}
		}
	}
	if len(fresh) == 0 {
		return true
	}
	hostList := joinHostPIDs(hostPIDsOf(fresh))
	fmt.Println("[部署] 运行区已有宿主实例在跑（pid " + hostList + "）——跳过自启，不新增第二个实例。")
	detail := "服务由已有实例承载；本次未新增进程"
	if !r.switchedAt.IsZero() {
		detail = "服务由已有实例承载（" + describeHostCreatedAt(fresh) + "，晚于本次切换 " +
			r.switchedAt.Format("15:04:05") + "——运行新产物）；本次未新增进程"
	}
	r.add("自启跳过（运行区已有实例 pid "+hostList+"）", true, detail)
	return false
}

// splitHostsBySwitchTime 按切换完成时刻分界实例——fresh = 承载新产物（可跳过自启），stale = 陈旧（须清退）。
//
// switchedAt 为零值 = 本次未替换运行区（host 模式 / deploy 失败）：没有「新产物」可比，全部按承载处理
// （沿用旧语义——此时跳过自启不构成假绿）。创建时刻不可读 = 无法取证，按承载处理（不误杀）。
func splitHostsBySwitchTime(hosts []hostProcess, switchedAt time.Time) (fresh, stale []hostProcess) {
	if switchedAt.IsZero() {
		return hosts, nil
	}
	for _, host := range hosts {
		if host.CreatedAt.IsZero() || !host.CreatedAt.Before(switchedAt) {
			fresh = append(fresh, host)
			continue
		}
		stale = append(stale, host)
	}
	return fresh, stale
}

// hostPIDsOf 取进程记录的 pid 列表（报告与清扫入参）。
func hostPIDsOf(hosts []hostProcess) []int {
	pids := make([]int, 0, len(hosts))
	for _, host := range hosts {
		pids = append(pids, host.PID)
	}
	return pids
}

// describeHostCreatedAt 承载实例的创建时刻描述（多实例取首个；不可读明示）。
func describeHostCreatedAt(hosts []hostProcess) string {
	if len(hosts) == 0 {
		return "创建时刻未知"
	}
	if hosts[0].CreatedAt.IsZero() {
		return "pid " + strconv.Itoa(hosts[0].PID) + " 创建时刻不可读"
	}
	return "pid " + strconv.Itoa(hosts[0].PID) + " 创建 " + hosts[0].CreatedAt.Format("15:04:05")
}

// verifyBootedFromNewArtifact 自启自检（段4d）——新实例创建时刻必须晚于切换完成时刻。
//
// 判据单向：早于切换 = 映射旧映像（不可能来自本次自启，出现即说明有外部实例混入）；
// 只在实际起了新实例（pid > 0）且本次确实替换了运行区时判——其余形态没有「新产物」可对。
func (r *deployRun) verifyBootedFromNewArtifact(pid int) {
	if pid <= 0 || r.switchedAt.IsZero() {
		return
	}
	created, err := queryProcessCreationTime(uint32(pid))
	if err != nil {
		r.add("自启自检", false, "pid "+strconv.Itoa(pid)+" 创建时刻不可读: "+err.Error())
		return
	}
	ok := !created.Before(r.switchedAt)
	detail := "pid " + strconv.Itoa(pid) + " 创建 " + created.Format("15:04:05") + " vs 切换 " +
		r.switchedAt.Format("15:04:05") + "——"
	if ok {
		detail += "运行新产物"
	} else {
		detail += "创建早于切换，映射旧映像"
	}
	r.add("自启自检", ok, detail)
}

// reportDeployResult 把接力部署结论写进重启现场（relay 侧）——部署报告是唯一来源。
//
// 现场路径 = <data-root>/runtime/restart-intent.json，与宿主侧 NewRestartIntentStore 同源；
// data-root 为空（人工直接跑 deploy）/ 无现场（非重启链）时跳过并出声。
// 值不是内存里的 exitCode，而是「刚落盘的终版报告里的 exitCode」——两处口径永远一致。
func (r *deployRun) reportDeployResult() {
	if strings.TrimSpace(r.o.DataRoot) == "" {
		r.add("intent", false, "跳过（未传 --data-root——无重启现场可写）")
		return
	}
	path := r.ensureReportPath()
	report, ok := readDeployReport(path)
	if !ok || !report.Complete {
		r.add("intent", false, "跳过：部署报告未终版 / 不可读（"+path+"）——deployExit 保持未上报")
		return
	}
	store := admin.NewRestartIntentStore(filepath.Join(r.o.DataRoot, "runtime", "restart-intent.json"))
	if _, exists := store.Read(); !exists {
		r.add("intent", true, "跳过（无重启现场——人工部署）")
		return
	}
	if err := store.SetDeployResult(report.ExitCode, path); err != nil {
		r.add("intent", false, "部署结论落盘失败: "+err.Error())
		return
	}
	r.add("intent", true, "部署结论已写重启现场：deployExit="+strconv.Itoa(report.ExitCode)+"（来源：部署报告 "+filepath.Base(path)+"）")
}

// reportDeployReportPath 登记本次部署报告路径（relay 段4-intent）——只记路径、不写结论。
//
// 写结论时点报告还是前置版（complete=false）——此时写 exitCode 只能靠猜；
// 现场保持 deployReported=false，等收尾按终版报告回写（未上报 ≠ 成功）。
func (r *deployRun) reportDeployReportPath() {
	if strings.TrimSpace(r.o.DataRoot) == "" {
		r.add("intent", false, "跳过（未传 --data-root——无重启现场可写）")
		return
	}
	store := admin.NewRestartIntentStore(filepath.Join(r.o.DataRoot, "runtime", "restart-intent.json"))
	if _, exists := store.Read(); !exists {
		r.add("intent", true, "跳过（无重启现场——人工部署）")
		return
	}
	path := r.ensureReportPath()
	if err := store.SetDeployReport(path); err != nil {
		r.add("intent", false, "重启现场登记部署报告路径失败: "+err.Error())
		return
	}
	r.add("intent", true, "重启现场已登记部署报告路径："+filepath.Base(path)+"（部署结论收尾按报告 exitCode 回写）")
}

// readDeployReport 读部署报告（intent 部署结论的单一来源）。
func readDeployReport(path string) (deployReport, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return deployReport{}, false
	}
	var report deployReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return deployReport{}, false
	}
	return report, true
}

// sweepStrayHosts 同目录残留实例清扫（段1b 第一面）——dll 锁必须先释放，准备链才跑得顺。
func (r *deployRun) sweepStrayHosts() { r.sweepStrayHostsLabeled("清扫残留") }

// sweepStrayHostsBeforeSwitch 切换前复扫（段2.5）——段1b 与 rename 之间隔着整个 prepare（数十秒），
// 外部监督者（并发协议面板 auto=true）可能在此期间自动拉起宿主；清扫窗口必须紧邻 rename。
func (r *deployRun) sweepStrayHostsBeforeSwitch() {
	r.sweepStrayHostsLabeled("清扫残留（切换前）")
}

// sweepStrayHostsLabeled 清扫实现的标签变体（同一套判据，报告 step 名区分两轮）。
func (r *deployRun) sweepStrayHostsLabeled(step string) {
	swept, err := hostKillInDir(r.o.Target)
	if err != nil {
		r.add(step, false, "宿主进程扫描失败（按无实例继续）: "+err.Error())
		return
	}
	if len(swept.PIDs) == 0 {
		r.add(step, true, "运行区无同目录宿主实例")
		return
	}
	detail := "另结束 " + strconv.Itoa(swept.Graceful+swept.Killed) + " 个同目录残留宿主实例（pid " +
		joinHostPIDs(swept.PIDs) + "；优雅关 " + strconv.Itoa(swept.Graceful) + " / 强杀 " + strconv.Itoa(swept.Killed) + "）"
	if swept.Failed > 0 {
		r.add(step, false, detail+"；失败 "+strconv.Itoa(swept.Failed)+" 个（见日志）")
		return
	}
	r.add(step, true, detail)
}

// sweepStrayServices 服务进程清扫（段1b 第二面）——py / ts / browser 子进程以运行区为 cwd 时
// 持目录级句柄（宿主 cwd 已改数据根，但旧宿主拉起的子进程生命周期可能长于宿主），原子切换改名必失败。
// 只清「映像名在服务清单 + cwd 落运行区」的进程（不碰用户进程）；
// 无命中 / 扫描失败也记报告 step——报告面必须能区分「清过且干净」与「压根没查」。
func (r *deployRun) sweepStrayServices() { r.sweepStrayServicesLabeled("清扫残留（服务）") }

// sweepStrayServicesBeforeSwitch 切换前复扫（段2.5）——理由同 sweepStrayHostsBeforeSwitch。
func (r *deployRun) sweepStrayServicesBeforeSwitch() {
	r.sweepStrayServicesLabeled("清扫残留（服务·切换前）")
}

// sweepStrayServicesLabeled 服务清扫实现的标签变体。
func (r *deployRun) sweepStrayServicesLabeled(step string) {
	swept, err := cwdStrayKillInDir(r.o.Target)
	if err != nil {
		r.add(step, false, "服务进程扫描失败（按无命中继续）: "+err.Error())
		return
	}
	if len(swept.Procs) == 0 {
		r.add(step, true, "运行区无 py/ts/browser 服务进程（cwd 命中）")
		return
	}
	detail := "另结束 " + strconv.Itoa(swept.Killed) + " 个服务进程（" + describeProcCWDList(swept.Procs, 10) + "）"
	if swept.Failed > 0 {
		r.add(step, false, detail+"；失败 "+strconv.Itoa(swept.Failed)+" 个（见日志）")
		return
	}
	r.add(step, true, detail)
}

// bootAndProbe 起新实例并探活——成功返回 (true, pid)。
func (r *deployRun) bootAndProbe(version, push string) (bool, int) {
	cmd, err := r.startInstance(push)
	if err != nil {
		r.add("start", false, err.Error())
		return false, 0
	}
	r.add("start", true, "pid="+strconv.Itoa(cmd.Process.Pid)+" · 版本 "+version)
	if err := r.probe(version); err != nil {
		r.add("probe", false, err.Error())
		_ = cmd.Process.Kill()
		return false, cmd.Process.Pid
	}
	r.add("probe", true, "snapshot 200 + 首页版本 "+version)
	r.artifacts["pid"] = strconv.Itoa(cmd.Process.Pid)
	return true, cmd.Process.Pid
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
	fallbackPush := "[宿主自更新] 新版本启动失败——已回退旧版本。\n原因: " + reason
	cmd, err := r.startInstance(fallbackPush)
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
//
// push 非空时以 --majordomopush 传给新宿主（T6 回执注入——一次性 system 消息）。
// area / page / port / data-root 透传：重启后的实例与重启前同形（默认 area=deploy）。
func (r *deployRun) startInstance(push string) (*exec.Cmd, error) {
	exe := filepath.Join(r.o.Target, "cathome5.exe")
	if _, err := os.Stat(exe); err != nil {
		return nil, fmt.Errorf("运行区缺 cathome5.exe: %s", exe)
	}
	area := r.o.Area
	if area == "" {
		area = "deploy"
	}
	args := []string{"--area", area}
	if r.o.Port > 0 {
		args = append(args, "--port", strconv.Itoa(r.o.Port))
	}
	if r.o.DataRoot != "" {
		args = append(args, "--data-root", r.o.DataRoot)
	}
	if r.o.Page != "" {
		args = append(args, "--page", r.o.Page)
	}
	if push != "" {
		args = append(args, "--majordomopush", push)
	}
	cmd := exec.Command(exe, args...)
	// 工作目录**不落运行区**（2026-11-15 判例）：cwd 在运行区会被子进程继承，
	// 下一次 target→_old 原子改名撞共享冲突（重试 20×500 ms 不过，部署恒 exit 3）。
	// 宿主不依赖 cwd——路径面全走受控根与显式参数，故落数据根（不可用则运行区父目录）。
	cmd.Dir = startInstanceWorkDir(r.o.Target, r.o.DataRoot)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000008}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// startInstanceWorkDir 解析新实例的工作目录——不落运行区（避免下一次原子改名自锁）。
//
// 优先数据根（不在运行区内、不随部署替换）；数据根不可用退运行区父目录。
func startInstanceWorkDir(target, dataRoot string) string {
	if dataRoot != "" {
		if info, err := os.Stat(dataRoot); err == nil && info.IsDir() {
			return dataRoot
		}
	}
	return filepath.Dir(target)
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

// cwdHolderDiagnostics rename 失败归因——列出 cwd 落在 dir 内的进程（最多 10 条，指名 pid / 映像名 / cwd）。
//
// 目录级句柄（进程 cwd）是文件句柄诊断的盲区：改名报 being used by another process 时，
// 报告必须能直接回答「谁锁着它」（判例 2026-11-15：占用者未抓到 → 只能盲试重启）。
func cwdHolderDiagnostics(dir string) string {
	list, err := procCWDList()
	if err != nil {
		return "cwd 归因扫描失败（" + err.Error() + "）"
	}
	holders := make([]procCWDInfo, 0, 4)
	for _, proc := range list {
		if pathWithin(proc.CWD, dir) {
			holders = append(holders, proc)
		}
	}
	if len(holders) == 0 {
		return "cwd 归因：运行区内无进程工作目录指向（持有者可能是文件句柄——见同目录宿主 / 桥 / 服务清扫面）"
	}
	return "cwd 归因：运行区内进程工作目录持有者 " + describeProcCWDList(holders, 10)
}

// renameWithRetry 同卷改名带重试——Windows 上目录改名要求目录内全部句柄关闭：
// 旧宿主刚退出时句柄释放有延迟，其子进程（node / python / dotnet）也可能短暂持有 target 内文件。
//
// 判例 2026-11-14 17:11：`deploy relaunch --mode incr` 段 3 rename target→_old 报
// `The process cannot access the file because it is being used by another process`（wait 已通过、
// sweep 无命中）→ 部署未替换。重试把「瞬时占用」与「常驻占用」分开：前者重试即过，后者重试到底再报。
func renameWithRetry(src, dst string, attempts int, wait time.Duration) error {
	var lastErr error
	for i := 0; i < attempts; i++ {
		if err := os.Rename(src, dst); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if i+1 < attempts {
			time.Sleep(wait)
		}
	}
	return lastErr
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
//
// 调用方是 waitProcessExit 的 500ms 轮询——不加 CREATE_NO_WINDOW 就是「每半秒一个黑框」。
func processAlive(pid int) bool {
	cmd := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/FO", "CSV", "/NH")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := cmd.Output()
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
//
// 控制台必须隐藏：本进程是 GUI 子系统（无控制台），不设 CreationFlags 时 Windows 会为
// go.exe 分配一个新的控制台窗口——输出被 CombinedOutput 吞掉，于是表现为「黑框一闪、什么
// 都看不到」。go test 还会为每个包起一个测试二进制（Go 21 包 ≈ 21 个黑框连弹）。
// CREATE_NO_WINDOW（0x08000000）分配不可见控制台，子进程继承它，孙进程也不再弹。
func runGo(goExe, dir string, args ...string) (string, error) {
	cmd := exec.Command(goExe, args...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
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

// resolveReportPath 报告落点单点解析（p5-4 消费面统一）——target 存在 → target；
// 否则 staging 存在 → staging；仍无 → target 父目录（prepare 首跑 target 与 staging 均未创建的唯一兜底）。
func (r *deployRun) resolveReportPath() string {
	if r.o.Report != "" {
		return r.o.Report
	}
	dir := r.o.Target
	if _, err := os.Stat(dir); err != nil {
		staging := r.stagingDir()
		if _, statErr := os.Stat(staging); statErr == nil {
			dir = staging
		} else {
			dir = filepath.Dir(r.o.Target)
		}
	}
	return filepath.Join(dir, "deploy-report-"+time.Now().Format("20060102_150405")+".json")
}

// ensureReportPath 取报告路径（一次解析——写入与回执引用同一路径）。
func (r *deployRun) ensureReportPath() string {
	if r.reportPath == "" {
		r.reportPath = r.resolveReportPath()
	}
	return r.reportPath
}

// writeReport 落报告 JSON——单点路径（ensureReportPath）；complete=false 为前置版（自启前先落盘）。
func (r *deployRun) writeReport(mode string, code int, complete bool) {
	path := r.ensureReportPath()
	if r.artifacts == nil {
		r.artifacts = map[string]string{}
	}
	r.artifacts["report"] = path
	report := deployReport{
		TS:        time.Now().Format("2006-01-02 15:04:05"),
		Mode:      mode,
		OK:        code == deployExitOK || code == deployExitRolledBack,
		ExitCode:  code,
		Complete:  complete,
		Steps:     r.steps,
		Artifacts: r.artifacts,
	}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Println("报告序列化失败:", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Println("报告目录创建失败:", err, path)
		return
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		fmt.Println("报告写入失败:", err, path)
		return
	}
	fmt.Println("报告:", path)
}

// buildRestartPush 拼回执注入串（T6）——模式 / 结论 / 版本 / 部署 / 自启 / 用时 / 产物 / 报告 / 说明。
//
// 由部署链生成（非 LLM 自述）；新宿主经 --majordomopush 一次性注入 Majordomo 会话。
func buildRestartPush(mode string, prepOK, replaced bool, version, target, pushIn, reportPath string, elapsed time.Duration) string {
	exeTs := ""
	if info, err := os.Stat(filepath.Join(target, "cathome5.exe")); err == nil {
		exeTs = info.ModTime().Format("2006-01-02 15:04:05")
	}
	prepText := "跳过"
	if mode == "full" {
		if prepOK {
			prepText = "OK"
		} else {
			prepText = "FAIL"
		}
	}
	depText := "跳过"
	if mode != "host" {
		switch {
		case mode == "full" && !prepOK:
			depText = "跳过" // prepare 未通过 → 跳过 deploy（运行区未替换）
		case replaced:
			depText = "OK"
		default:
			depText = "FAIL"
		}
	}
	bootText := "现有版本（运行区未替换）"
	if replaced {
		bootText = "新版本"
	} else if mode == "host" {
		bootText = "原地重启（运行区未替换）"
	}
	result := "OK"
	reasons := make([]string, 0, 2)
	if !prepOK {
		result = "FAIL"
		reasons = append(reasons, "prepare 未通过")
	}
	if mode != "host" && !replaced {
		result = "FAIL"
		reasons = append(reasons, "deploy 未通过")
	}
	if len(reasons) > 0 {
		result += " | " + strings.Join(reasons, " | ")
	}
	versionNote := ""
	if mode != "full" {
		versionNote = "（本链不自增——显示产物区既有版本，自增点仅 prepare）"
	}
	lines := []string{
		"[宿主自更新] 重启完成",
		"模式: " + mode,
		"结果: " + result,
		"版本: " + version + versionNote,
		"部署: prepare " + prepText + " · deploy " + depText,
		"自启: " + bootText,
		"重启用时: " + strconv.FormatFloat(elapsed.Seconds(), 'f', 1, 64) + " s（接力起算）",
		"产物: cathome5.exe " + exeTs,
	}
	if reportPath != "" {
		lines = append(lines, "报告: "+reportPath+"（前置版先落盘；终版 complete=true 在收尾时覆盖）")
	}
	if pushIn != "" {
		lines = append(lines, "说明: "+pushIn)
	}
	return strings.Join(lines, "\n")
}

// runDeploy 子命令入口——由 main 在 flag.Parse 前分派。
func runDeploy(args []string) int {
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	o := deployOptions{
		Target: defaultDeployTarget(),
		Repo:   resolveRepoRoot(),
		// 产物源缺省全部留空——prepare 按「显式 flag > 运行区现有」解析（见 resolveProductSource）：
		// 仓库当前不自持 html / corpus / Bricks / config 源，运行区那份就是唯一真相（2026-11-14 实测）。
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
	r.writeReport(mode, code, true)
	fmt.Println("deploy " + mode + " 退出码 " + strconv.Itoa(code))
	return code
}

// defaultDeployTarget 运行区默认位置——CAT5_DEPLOY_TARGET > 当前 exe 所在运行区 > D:\Mau\MauOut5。
//
// 「exe 所在运行区」判据：同目录有 version.txt + html/，且目录名不是切换链中间态
// （_staging / _new / _old / _relay）。运行实例里跑 `deploy prepare` 不带 --target 即可命中。
func defaultDeployTarget() string {
	if value := os.Getenv("CAT5_DEPLOY_TARGET"); value != "" {
		return value
	}
	if exe, err := os.Executable(); err == nil {
		if dir := filepath.Dir(exe); isRuntimeDir(dir) {
			return dir
		}
	}
	return `D:\Mau\MauOut5`
}

// isRuntimeDir 判定目录是否为运行区——version.txt 与 html/ 齐备，且不是同卷切换链的中间态。
func isRuntimeDir(dir string) bool {
	base := strings.ToLower(filepath.Base(dir))
	for _, suffix := range []string{"_staging", "_new", "_old", "_relay"} {
		if strings.HasSuffix(base, suffix) {
			return false
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "version.txt")); err != nil {
		return false
	}
	if info, err := os.Stat(filepath.Join(dir, "html")); err != nil || !info.IsDir() {
		return false
	}
	return true
}

// registerDeployFlags 注册部署参数（子命令可前可后，解析前统一注册一次）。
func registerDeployFlags(fs *flag.FlagSet, o *deployOptions) {
	fs.StringVar(&o.Target, "target", o.Target, "运行区目录（默认 CAT5_DEPLOY_TARGET > exe 所在运行区 > D:\\Mau\\MauOut5）")
	fs.StringVar(&o.Repo, "repo", o.Repo, "施工仓根（go 构建与测试的 cwd；默认 CAT5_REPO > exe/cwd 向上 go.mod）")
	fs.StringVar(&o.Version, "version", "", "版本覆盖（空 = 读 <repo>/version.txt）")
	fs.StringVar(&o.Staging, "staging", "", "暂存区（空 = <target>_staging）")
	fs.StringVar(&o.HTML, "html", o.HTML, "前端源目录（空 = 沿用运行区现有 html/）")
	fs.StringVar(&o.Corpus, "corpus", o.Corpus, "语料源目录（空 = 沿用运行区现有 corpus/ch4）")
	fs.StringVar(&o.Bricks, "bricks", o.Bricks, "积木源目录（空 = 沿用运行区现有 Bricks/）")
	fs.StringVar(&o.ConfigSrc, "config", o.ConfigSrc, "配置模板源目录（空 = 沿用运行区现有 config/）")
	fs.StringVar(&o.CsBridge, "csbridge", "", "cs 桥产物源目录（空 = <repo>/host/csbridge/bin/Debug/net10.0）")
	fs.StringVar(&o.GoExe, "go", "", "go 可执行文件（空 = PATH / tools/go）")
	fs.StringVar(&o.Mode, "mode", "incr", "relaunch 模式：full / incr / host")
	fs.IntVar(&o.WaitPID, "wait-pid", 0, "起新实例前等待退出的旧进程 pid")
	fs.IntVar(&o.Port, "port", 0, "起新实例端口（0 = 部署区段默认 8080）")
	fs.StringVar(&o.DataRoot, "data-root", "", "起新实例的数据根（空 = 默认锚定）")
	fs.StringVar(&o.Area, "area", "", "区覆盖（dev / deploy；空 = deploy）")
	fs.StringVar(&o.Page, "page", "", "页面覆盖（chat.html = 对话页实例；空 = 管理面板）")
	fs.StringVar(&o.PushIn, "push", "", "重启后附加说明串（随部署回执注入新宿主会话）")
	fs.StringVar(&o.Report, "report", "", "报告路径覆盖（空 = 单点解析 target → staging → target 父目录）")
	fs.BoolVar(&o.SkipTests, "skip-tests", false, "prepare 跳过 go test")
	fs.IntVar(&o.ProbeTimeout, "probe-timeout", 30, "探活超时秒数")
}
