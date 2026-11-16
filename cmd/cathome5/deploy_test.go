package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestResolveCsBridgeSource 桥源目录解析——显式覆盖优先，空取 <repo>/host/csbridge/bin/Debug/net10.0。
func TestResolveCsBridgeSource(t *testing.T) {
	r := &deployRun{o: deployOptions{Repo: `D:\Mau\WorkSpace\CatHome5`}}
	want := filepath.Join(`D:\Mau\WorkSpace\CatHome5`, "host", "csbridge", "bin", "Debug", "net10.0")
	if got := r.resolveCsBridgeSource(); got != want {
		t.Fatalf("默认源目录应为 %q，got %q", want, got)
	}
	override := `D:\somewhere\csbridge`
	r = &deployRun{o: deployOptions{Repo: `D:\Mau\WorkSpace\CatHome5`, CsBridge: override}}
	if got := r.resolveCsBridgeSource(); got != override {
		t.Fatalf("显式覆盖应优先，got %q", got)
	}
}

// TestCollectCsBridge 桥产物收集——缺失出声跳过（不阻断），存在整棵复制到暂存区 csbridge/。
func TestCollectCsBridge(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "bridge-bin")
	// 缺失：step 出声、返回 nil、不建目标目录、artifacts 不登记
	r := &deployRun{o: deployOptions{CsBridge: src}, artifacts: map[string]string{}}
	if err := r.collectCsBridge(staging); err != nil {
		t.Fatalf("缺失不应阻断：%v", err)
	}
	if len(r.steps) != 1 || r.steps[0].Name != "csbridge" || r.steps[0].OK {
		t.Fatalf("缺失应记一条 ok=false 的 csbridge 步骤：%+v", r.steps)
	}
	if _, err := os.Stat(filepath.Join(staging, "csbridge")); !os.IsNotExist(err) {
		t.Fatalf("缺失时不应创建目标目录")
	}
	if _, exists := r.artifacts["csbridge"]; exists {
		t.Fatalf("缺失时不应登记产物")
	}
	// 存在：整棵复制（含子目录）、step 通过、产物登记
	if err := os.MkdirAll(filepath.Join(src, "BuildHost-netcore"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, csBridgeExeName), []byte("exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "MauCsBridge.dll"), []byte("dll"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "BuildHost-netcore", "Microsoft.Build.dll"), []byte("mb"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = &deployRun{o: deployOptions{CsBridge: src}, artifacts: map[string]string{}}
	if err := r.collectCsBridge(staging); err != nil {
		t.Fatalf("收集失败：%v", err)
	}
	if len(r.steps) != 1 || !r.steps[0].OK {
		t.Fatalf("存在应记一条 ok=true 的 csbridge 步骤：%+v", r.steps)
	}
	if got := r.artifacts["csbridge"]; got != filepath.Join(staging, "csbridge") {
		t.Fatalf("产物登记应为暂存区 csbridge/，got %q", got)
	}
	for _, name := range []string{
		csBridgeExeName,
		"MauCsBridge.dll",
		filepath.Join("BuildHost-netcore", "Microsoft.Build.dll"),
	} {
		if _, err := os.Stat(filepath.Join(staging, "csbridge", name)); err != nil {
			t.Fatalf("复制应含 %q：%v", name, err)
		}
	}
	// 重跑幂等（先清理再复制，不残留旧文件）
	if err := os.WriteFile(filepath.Join(staging, "csbridge", "stale.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = &deployRun{o: deployOptions{CsBridge: src}, artifacts: map[string]string{}}
	if err := r.collectCsBridge(staging); err != nil {
		t.Fatalf("重跑失败：%v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "csbridge", "stale.txt")); !os.IsNotExist(err) {
		t.Fatalf("重跑应清理旧产物")
	}
}

// TestRelaunchSkipsAutostartWhenHostRunning 自启前检（CH4 1.8.266 同因）——运行区已有宿主实例：
// 跳过自启、不新增第二个实例（退出码 0，报告出「自启跳过」步骤）；无实例时才走起实例路径。
func TestRelaunchSkipsAutostartWhenHostRunning(t *testing.T) {
	prev := hostScanFindInDirDetail
	t.Cleanup(func() { hostScanFindInDirDetail = prev })
	hostScanFindInDirDetail = func(dir string) ([]hostProcess, error) {
		return []hostProcess{{PID: 4321}, {PID: 4322}}, nil
	}
	// 段1b 清扫不参与本用例（target 为临时目录——mock 空结果，不依赖本机进程表）
	prevKill := hostKillInDir
	t.Cleanup(func() { hostKillInDir = prevKill })
	hostKillInDir = func(dir string) (hostSweepResult, error) { return hostSweepResult{}, nil }
	target := filepath.Join(t.TempDir(), "MauOut5")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "version.txt"), []byte("0.35.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &deployRun{o: deployOptions{Target: target, Mode: "host"}, mode: "relaunch"}
	if code := r.relaunch(); code != deployExitOK {
		t.Fatalf("已有实例应视为服务已承载（退出码 0），got %d（steps=%+v）", code, r.steps)
	}
	skipSeen := false
	for _, step := range r.steps {
		if step.Name == "start" || step.Name == "probe" {
			t.Fatalf("跳过自启后不应再起实例 / 探活：%+v", r.steps)
		}
		if step.Name == "自启跳过（运行区已有实例 pid 4321,4322）" && step.OK {
			skipSeen = true
		}
	}
	if !skipSeen {
		t.Fatalf("报告缺「自启跳过」步骤：%+v", r.steps)
	}
	// 对照：无实例 → 运行区缺 exe → 起实例失败 → 需人工（证明上面是「跳过」而不是「碰巧没起」）
	hostScanFindInDirDetail = func(dir string) ([]hostProcess, error) { return nil, nil }
	r = &deployRun{o: deployOptions{Target: target, Mode: "host"}, mode: "relaunch"}
	if code := r.relaunch(); code != deployExitManual {
		t.Fatalf("无实例且运行区缺 exe 应需人工，got %d", code)
	}
}

// TestSweepStrayHostsReports 段1b 清扫——命中记 step（优雅 / 强杀分列）；无命中 / 扫描失败不记（stdout 出声）；有失败 ok=false。
func TestSweepStrayHostsReports(t *testing.T) {
	prev := hostKillInDir
	t.Cleanup(func() { hostKillInDir = prev })
	target := filepath.Join(t.TempDir(), "MauOut5")
	// 命中：优雅 1 + 强杀 1 → ok=true，明细含计数与 pid 清单
	hostKillInDir = func(dir string) (hostSweepResult, error) {
		return hostSweepResult{PIDs: []int{4321, 4322}, Graceful: 1, Killed: 1}, nil
	}
	r := &deployRun{o: deployOptions{Target: target}}
	r.sweepStrayHosts()
	if len(r.steps) != 1 || r.steps[0].Name != "清扫残留" || !r.steps[0].OK {
		t.Fatalf("命中应记一条 ok=true 的清扫残留步骤：%+v", r.steps)
	}
	for _, want := range []string{"另结束 2 个", "pid 4321,4322", "优雅关 1", "强杀 1"} {
		if !strings.Contains(r.steps[0].Detail, want) {
			t.Fatalf("步骤明细缺 %q：%s", want, r.steps[0].Detail)
		}
	}
	// 失败面：ok=false 且明细标失败数
	hostKillInDir = func(dir string) (hostSweepResult, error) {
		return hostSweepResult{PIDs: []int{4321}, Killed: 1, Failed: 1}, nil
	}
	r = &deployRun{o: deployOptions{Target: target}}
	r.sweepStrayHosts()
	if len(r.steps) != 1 || r.steps[0].OK || !strings.Contains(r.steps[0].Detail, "失败 1 个") {
		t.Fatalf("有失败应记 ok=false 并标失败数：%+v", r.steps)
	}
	// 空命中 / 扫描失败：都记 step——报告面必须能区分「清过且干净」与「压根没查」
	hostKillInDir = func(dir string) (hostSweepResult, error) { return hostSweepResult{}, nil }
	r = &deployRun{o: deployOptions{Target: target}}
	r.sweepStrayHosts()
	if len(r.steps) != 1 || r.steps[0].Name != "清扫残留" || !r.steps[0].OK || !strings.Contains(r.steps[0].Detail, "无同目录宿主实例") {
		t.Fatalf("空命中应记一条 ok=true 的清扫残留步骤：%+v", r.steps)
	}
	hostKillInDir = func(dir string) (hostSweepResult, error) { return hostSweepResult{}, fmt.Errorf("tasklist 失败") }
	r = &deployRun{o: deployOptions{Target: target}}
	r.sweepStrayHosts()
	if len(r.steps) != 1 || r.steps[0].Name != "清扫残留" || r.steps[0].OK || !strings.Contains(r.steps[0].Detail, "tasklist 失败") {
		t.Fatalf("扫描失败应记一条 ok=false 的清扫残留步骤：%+v", r.steps)
	}
	// 切换前复扫——同一套判据，step 名带「切换前」区分两轮（段1b 与 rename 之间的 prepare 窗口）
	hostKillInDir = func(dir string) (hostSweepResult, error) { return hostSweepResult{}, nil }
	r = &deployRun{o: deployOptions{Target: target}}
	r.sweepStrayHostsBeforeSwitch()
	if len(r.steps) != 1 || r.steps[0].Name != "清扫残留（切换前）" || !r.steps[0].OK {
		t.Fatalf("切换前复扫应记带标签的步骤：%+v", r.steps)
	}
}

// TestSplitHostsBySwitchTime 切换时刻分界——早于切换 = 陈旧（旧映像），晚于 / 等于 / 时刻不可读 = 承载。
func TestSplitHostsBySwitchTime(t *testing.T) {
	switchAt := time.Date(2026, 11, 16, 17, 54, 45, 0, time.Local)
	cases := []struct {
		name  string
		host  hostProcess
		stale bool
	}{
		{"切换前创建", hostProcess{PID: 33316, CreatedAt: switchAt.Add(-3 * time.Second)}, true},
		{"切换后创建", hostProcess{PID: 40001, CreatedAt: switchAt.Add(2 * time.Second)}, false},
		{"切换同刻", hostProcess{PID: 40002, CreatedAt: switchAt}, false},
		{"创建时刻不可读", hostProcess{PID: 40003}, false},
	}
	for _, tc := range cases {
		fresh, stale := splitHostsBySwitchTime([]hostProcess{tc.host}, switchAt)
		if tc.stale {
			if len(stale) != 1 || len(fresh) != 0 {
				t.Fatalf("%s：应判陈旧，got fresh=%v stale=%v", tc.name, fresh, stale)
			}
			continue
		}
		if len(fresh) != 1 || len(stale) != 0 {
			t.Fatalf("%s：应判承载，got fresh=%v stale=%v", tc.name, fresh, stale)
		}
	}
	// 未替换运行区（switchedAt 零值）——没有「新产物」可比，全部按承载（沿用旧语义）
	fresh, stale := splitHostsBySwitchTime([]hostProcess{{PID: 1, CreatedAt: switchAt.Add(-time.Hour)}}, time.Time{})
	if len(fresh) != 1 || len(stale) != 0 {
		t.Fatalf("零值切换时刻应全部按承载：fresh=%v stale=%v", fresh, stale)
	}
}

// TestAutostartPrecheckSweepsStaleHost G32 判例——切换前创建的实例即使路径在运行区也必须清退；
// 清退后无承载实例 → 要求起新实例（不能按「已有实例」跳过自启，否则报告 exit0 而跑旧代码）。
func TestAutostartPrecheckSweepsStaleHost(t *testing.T) {
	prevScan, prevKill, prevDelay := hostScanFindInDirDetail, hostKillByPID, staleRescanDelay
	t.Cleanup(func() { hostScanFindInDirDetail, hostKillByPID, staleRescanDelay = prevScan, prevKill, prevDelay })
	staleRescanDelay = 0
	switchAt := time.Now()
	calls := 0
	hostScanFindInDirDetail = func(dir string) ([]hostProcess, error) {
		calls++
		if calls == 1 {
			return []hostProcess{{PID: 33316, CreatedAt: switchAt.Add(-3 * time.Second)}}, nil
		}
		return nil, nil
	}
	var killed []int
	hostKillByPID = func(pids []int) hostSweepResult {
		killed = append(killed, pids...)
		return hostSweepResult{PIDs: pids, Killed: len(pids)}
	}
	r := &deployRun{o: deployOptions{Target: t.TempDir()}, switchedAt: switchAt}
	if !r.autostartPrecheck() {
		t.Fatalf("陈旧实例清退后应要求起新实例（steps=%+v）", r.steps)
	}
	if len(killed) != 1 || killed[0] != 33316 {
		t.Fatalf("应定向清退 pid 33316，got %v", killed)
	}
	if len(r.steps) != 1 || r.steps[0].Name != "陈旧实例清退（切换前创建）" || !r.steps[0].OK ||
		!strings.Contains(r.steps[0].Detail, "pid 33316") || !strings.Contains(r.steps[0].Detail, "旧产物") {
		t.Fatalf("应记陈旧实例清退步骤（含 pid 与依据）：%+v", r.steps)
	}
}

// TestAutostartPrecheckAdoptsSupervisorInstance 陈旧实例清退后复扫——监督者已拉起新实例（创建晚于切换）
// 时直接接手，不再起第二个（双实例）。
func TestAutostartPrecheckAdoptsSupervisorInstance(t *testing.T) {
	prevScan, prevKill, prevDelay := hostScanFindInDirDetail, hostKillByPID, staleRescanDelay
	t.Cleanup(func() { hostScanFindInDirDetail, hostKillByPID, staleRescanDelay = prevScan, prevKill, prevDelay })
	staleRescanDelay = 0
	switchAt := time.Now()
	calls := 0
	hostScanFindInDirDetail = func(dir string) ([]hostProcess, error) {
		calls++
		if calls == 1 {
			return []hostProcess{{PID: 33316, CreatedAt: switchAt.Add(-3 * time.Second)}}, nil
		}
		return []hostProcess{{PID: 40001, CreatedAt: switchAt.Add(time.Second)}}, nil
	}
	hostKillByPID = func(pids []int) hostSweepResult { return hostSweepResult{PIDs: pids, Killed: len(pids)} }
	r := &deployRun{o: deployOptions{Target: t.TempDir()}, switchedAt: switchAt}
	if r.autostartPrecheck() {
		t.Fatalf("复扫到新产物实例应跳过自启（steps=%+v）", r.steps)
	}
	adopted := false
	for _, step := range r.steps {
		if step.Name == "自启跳过（运行区已有实例 pid 40001）" && step.OK && strings.Contains(step.Detail, "晚于本次切换") {
			adopted = true
		}
	}
	if !adopted {
		t.Fatalf("应记「自启跳过」并标明创建晚于切换：%+v", r.steps)
	}
}

// TestAutostartPrecheckNoSwitchKeepsOldSemantics 未替换运行区（host 模式）——没有「新产物」可比，
// 运行区实例一律按承载处理（沿用旧语义，不误杀）。
func TestAutostartPrecheckNoSwitchKeepsOldSemantics(t *testing.T) {
	prevScan := hostScanFindInDirDetail
	t.Cleanup(func() { hostScanFindInDirDetail = prevScan })
	hostScanFindInDirDetail = func(dir string) ([]hostProcess, error) {
		return []hostProcess{{PID: 4321, CreatedAt: time.Now().Add(-time.Hour)}}, nil
	}
	r := &deployRun{o: deployOptions{Target: t.TempDir()}}
	if r.autostartPrecheck() {
		t.Fatalf("host 模式应跳过自启（steps=%+v）", r.steps)
	}
	if len(r.steps) != 1 || r.steps[0].Name != "自启跳过（运行区已有实例 pid 4321）" || !r.steps[0].OK {
		t.Fatalf("应记自启跳过步骤：%+v", r.steps)
	}
}
