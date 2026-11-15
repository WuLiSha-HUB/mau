//go:build windows

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"cathome5/host/admin"
)

// TestReadProcessCWD 真实链路——子进程以指定目录为 cwd，PEB 读取应命中同一路径。
//
// 判据不成立即整个「cwd 取证面」不可用（清扫与归因都建在它上面）。
func TestReadProcessCWD(t *testing.T) {
	dir := t.TempDir()
	child := exec.Command("ping.exe", "-n", "60", "127.0.0.1")
	child.Dir = dir
	if err := child.Start(); err != nil {
		t.Skipf("起子进程失败（跳过真实链路判据）: %v", err)
	}
	defer func() {
		_ = child.Process.Kill()
		_, _ = child.Process.Wait()
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cwd := readProcessCWD(uint32(child.Process.Pid)); pathWithin(cwd, dir) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("读不到子进程 cwd（pid=%d，期望落在 %s）", child.Process.Pid, dir)
}

// TestRenameFailureDiagnosticsNameCWDHolder 目录被 cwd 持有时 rename 必失败——归因文本要点名 pid + 映像名。
//
// 判例 2026-11-15：失败时刻的占用者在取证时已不存在 → 只能盲试重启；归因面必须第一时间留下名字。
func TestRenameFailureDiagnosticsNameCWDHolder(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "MauOut")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	child := exec.Command("ping.exe", "-n", "60", "127.0.0.1")
	child.Dir = target
	if err := child.Start(); err != nil {
		t.Skipf("起子进程失败（跳过真实链路判据）: %v", err)
	}
	defer func() {
		_ = child.Process.Kill()
		_, _ = child.Process.Wait()
	}()
	// 等子进程 cwd 生效（PEB 初始化在子进程里异步完成——读到 cwd 再判 rename）
	time.Sleep(500 * time.Millisecond)
	cwd := readProcessCWD(uint32(child.Process.Pid))
	if !pathWithin(cwd, target) {
		t.Fatalf("子进程 cwd 未落在目标目录（cwd=%q target=%q）", cwd, target)
	}
	// 目录被 cwd 持有 → rename 必失败（判据由 rename_probe 实测：WinError 32）
	err := os.Rename(target, target+"_old")
	if err == nil {
		_ = os.Rename(target+"_old", target)
		t.Fatalf("cwd 持有目录时 rename 应失败——本机未复现（cwd=%q pid=%d）", cwd, child.Process.Pid)
	}
	detail := cwdHolderDiagnostics(target)
	if !strings.Contains(detail, "pid ") {
		t.Fatalf("归因应点名 cwd 持有者 pid：%s", detail)
	}
	if !strings.Contains(strings.ToLower(detail), "ping.exe") {
		t.Fatalf("归因应点名 cwd 持有者映像名：%s", detail)
	}
	if !strings.Contains(strings.ToLower(detail), strings.ToLower(target)) {
		t.Fatalf("归因应带 cwd 路径：%s", detail)
	}
}

// TestListCWDStraysFilter 清扫面判据——映像名 ∈ 服务清单 且 cwd 落运行区；用户进程不碰。
func TestListCWDStraysFilter(t *testing.T) {
	prev := procCWDList
	t.Cleanup(func() { procCWDList = prev })
	dir := filepath.Join(t.TempDir(), "MauOut")
	procCWDList = func() ([]procCWDInfo, error) {
		return []procCWDInfo{
			{PID: 101, Name: "python.exe", CWD: dir},
			{PID: 102, Name: "msedge.exe", CWD: filepath.Join(dir, "html")},
			{PID: 103, Name: "python.exe", CWD: `D:\elsewhere`},
			{PID: 104, Name: "notepad.exe", CWD: dir},
			{PID: 105, Name: "cathome5.exe", CWD: dir},
			{PID: 106, Name: "NODE.EXE", CWD: dir},
		}, nil
	}
	strays, err := listCWDStraysInDir(dir)
	if err != nil {
		t.Fatalf("枚举失败: %v", err)
	}
	got := make([]int, 0, len(strays))
	for _, proc := range strays {
		got = append(got, proc.PID)
	}
	want := []int{101, 102, 106}
	if len(got) != len(want) {
		t.Fatalf("命中集不符：got %v want %v（用户进程 / 宿主不得入列）", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("命中集不符：got %v want %v", got, want)
		}
	}
}

// TestSweepStrayServicesReports 服务清扫 step——命中 / 空命中 / 扫描失败都记报告（可区分「清过」与「没查」）。
func TestSweepStrayServicesReports(t *testing.T) {
	prev := cwdStrayKillInDir
	t.Cleanup(func() { cwdStrayKillInDir = prev })
	target := filepath.Join(t.TempDir(), "MauOut")
	// 命中
	cwdStrayKillInDir = func(dir string) (cwdSweepResult, error) {
		return cwdSweepResult{
			Procs:  []procCWDInfo{{PID: 321, Name: "python.exe", CWD: dir}, {PID: 322, Name: "msedge.exe", CWD: dir}},
			Killed: 2,
		}, nil
	}
	r := &deployRun{o: deployOptions{Target: target}}
	r.sweepStrayServices()
	if len(r.steps) != 1 || r.steps[0].Name != "清扫残留（服务）" || !r.steps[0].OK {
		t.Fatalf("命中应记一条 ok=true 的服务清扫步骤：%+v", r.steps)
	}
	for _, want := range []string{"另结束 2 个服务进程", "pid 321 python.exe", "pid 322 msedge.exe"} {
		if !strings.Contains(r.steps[0].Detail, want) {
			t.Fatalf("步骤明细缺 %q：%s", want, r.steps[0].Detail)
		}
	}
	// 失败面
	cwdStrayKillInDir = func(dir string) (cwdSweepResult, error) {
		return cwdSweepResult{Procs: []procCWDInfo{{PID: 321, Name: "python.exe", CWD: dir}}, Killed: 1, Failed: 1}, nil
	}
	r = &deployRun{o: deployOptions{Target: target}}
	r.sweepStrayServices()
	if len(r.steps) != 1 || r.steps[0].OK || !strings.Contains(r.steps[0].Detail, "失败 1 个") {
		t.Fatalf("有失败应记 ok=false 并标失败数：%+v", r.steps)
	}
	// 空命中 / 扫描失败
	cwdStrayKillInDir = func(dir string) (cwdSweepResult, error) { return cwdSweepResult{}, nil }
	r = &deployRun{o: deployOptions{Target: target}}
	r.sweepStrayServices()
	if len(r.steps) != 1 || !r.steps[0].OK || !strings.Contains(r.steps[0].Detail, "无 py/ts/browser 服务进程") {
		t.Fatalf("空命中应记 ok=true 且自证干净：%+v", r.steps)
	}
	cwdStrayKillInDir = func(dir string) (cwdSweepResult, error) { return cwdSweepResult{}, os.ErrPermission }
	r = &deployRun{o: deployOptions{Target: target}}
	r.sweepStrayServices()
	if len(r.steps) != 1 || r.steps[0].OK || !strings.Contains(r.steps[0].Detail, "扫描失败") {
		t.Fatalf("扫描失败应记 ok=false：%+v", r.steps)
	}
	// 切换前复扫——同一套判据，step 名带「切换前」区分两轮
	cwdStrayKillInDir = func(dir string) (cwdSweepResult, error) { return cwdSweepResult{}, nil }
	r = &deployRun{o: deployOptions{Target: target}}
	r.sweepStrayServicesBeforeSwitch()
	if len(r.steps) != 1 || r.steps[0].Name != "清扫残留（服务·切换前）" || !r.steps[0].OK {
		t.Fatalf("切换前复扫应记带标签的步骤：%+v", r.steps)
	}
}

// TestReportDeployResultFromReport 部署结论单一来源——intent 的 deployExit 必须取自终版部署报告 exitCode。
func TestReportDeployResultFromReport(t *testing.T) {
	dir := t.TempDir()
	store := admin.NewRestartIntentStore(filepath.Join(dir, "runtime", "restart-intent.json"))
	if err := store.Registered("incr", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Launching(11, 22); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(dir, "deploy-report-test.json")
	r := &deployRun{o: deployOptions{DataRoot: dir, Report: reportPath}, artifacts: map[string]string{}}
	// 前置版（complete=false）不构成结论——不上报
	r.writeReport("relaunch", -1, false)
	r.reportDeployResult()
	if intent, _ := store.Read(); intent.DeployReported {
		t.Fatalf("前置版报告不应上报部署结论：%+v", intent)
	}
	// 终版报告 exit=3 → intent 按报告回写（不是内存值 / 不是默认 0）
	r = &deployRun{o: deployOptions{DataRoot: dir, Report: reportPath}, artifacts: map[string]string{}}
	r.writeReport("relaunch", deployExitNotReplaced, true)
	r.reportDeployResult()
	intent, _ := store.Read()
	if !intent.DeployReported || intent.DeployExit != deployExitNotReplaced {
		t.Fatalf("部署结论应按报告回写：%+v", intent)
	}
	if intent.DeployReport != reportPath {
		t.Fatalf("报告路径应一并登记：%+v", intent)
	}
	// step 明细带来源，审计能对账
	found := false
	for _, step := range r.steps {
		if step.Name == "intent" && strings.Contains(step.Detail, "来源：部署报告") && strings.Contains(step.Detail, "deployExit=3") {
			found = true
		}
	}
	if !found {
		t.Fatalf("intent step 应写明来源与退出码：%+v", r.steps)
	}
}

// TestSettleRestartIntentBackfillsFromReport stale 场景按现场登记的报告回填 deployExit——
// 修复判例：intent deployExit=0 而部署报告 exitCode=3，两处口径不一致。
func TestSettleRestartIntentBackfillsFromReport(t *testing.T) {
	dir := t.TempDir()
	store := admin.NewRestartIntentStore(filepath.Join(dir, "runtime", "restart-intent.json"))
	if err := store.Registered("full", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Launching(11, 22); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(dir, "deploy-report-b.json")
	body, err := json.Marshal(deployReport{TS: "2026-11-15 12:00:00", Mode: "relaunch", OK: false, ExitCode: 3, Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeployReport(reportPath); err != nil {
		t.Fatal(err)
	}
	// 无接力回执 → stale；部署结论按报告回填（complete=true）
	stale, changed := settleRestartIntent(store, "", "0.36.9", 1001)
	if !changed || stale.Phase != admin.RestartPhaseStale {
		t.Fatalf("应结算 stale：changed=%v %+v", changed, stale)
	}
	if !stale.DeployReported || stale.DeployExit != 3 {
		t.Fatalf("stale 场景 deployExit 应按报告回填为 3：%+v", stale)
	}
	if stale.DeployReport != reportPath {
		t.Fatalf("报告路径应保留：%+v", stale)
	}
}

// TestRestartTargetBlockers 重启前置检查判据——清扫面之外的 cwd 持有者入列；宿主自身 / 清扫面 / 运行区外不入列。
func TestRestartTargetBlockers(t *testing.T) {
	prev := procCWDList
	t.Cleanup(func() { procCWDList = prev })
	target := filepath.Join(t.TempDir(), "MauOut")
	self := os.Getpid()
	procCWDList = func() ([]procCWDInfo, error) {
		return []procCWDInfo{
			{PID: self, Name: "cathome5.exe", CWD: target},
			{PID: 201, Name: "cathome5.exe", CWD: target},
			{PID: 202, Name: "MauCsBridge.exe", CWD: filepath.Join(target, "csbridge")},
			{PID: 203, Name: "python.exe", CWD: target},
			{PID: 204, Name: "Unity.exe", CWD: target},
			{PID: 205, Name: "notepad.exe", CWD: `D:\elsewhere`},
			{PID: 206, Name: "angelicarCore.exe", CWD: filepath.Join(target, "sub")},
		}, nil
	}
	blockers, scanErr := restartTargetBlockers(target)
	if scanErr != "" {
		t.Fatalf("扫描不应失败：%q", scanErr)
	}
	for _, want := range []string{"pid 204 Unity.exe", "pid 206 angelicarCore.exe"} {
		if !strings.Contains(blockers, want) {
			t.Fatalf("应点名 %q：%s", want, blockers)
		}
	}
	for _, unwanted := range []string{"pid 201", "pid 202", "pid 203", "pid 205", "pid " + strconv.Itoa(self)} {
		if strings.Contains(blockers, unwanted) {
			t.Fatalf("不应入列 %q（宿主自身 / 清扫面 / 运行区外）：%s", unwanted, blockers)
		}
	}
	// 无命中 = 空
	procCWDList = func() ([]procCWDInfo, error) { return []procCWDInfo{{PID: 301, Name: "python.exe", CWD: target}}, nil }
	if blockers, scanErr = restartTargetBlockers(target); blockers != "" || scanErr != "" {
		t.Fatalf("清扫面内进程不应阻断：%q / %q", blockers, scanErr)
	}
	// 扫描失败 = 可重试的空名单 + 出声文本
	procCWDList = func() ([]procCWDInfo, error) { return nil, os.ErrPermission }
	if blockers, scanErr = restartTargetBlockers(target); blockers != "" || scanErr == "" {
		t.Fatalf("扫描失败应出声且不阻断：%q / %q", blockers, scanErr)
	}
}
