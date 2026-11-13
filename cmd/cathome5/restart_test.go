package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cathome5/host/admin"
	"cathome5/host/config"
	"cathome5/host/session"
	"cathome5/mau/runtime"
)

// TestCommandRouterExecute 宿主指令服务面——前缀白名单 / cat.* 单猫族 / 停机态 / 未识别清单。
func TestCommandRouterExecute(t *testing.T) {
	router := &commandRouter{DataRoot: t.TempDir(), CatID: "majordomo", DisplayName: "majordomo", Running: true, Port: 8079}
	if out := router.Execute(""); !strings.HasPrefix(out, "ERR|BAD_ARGS|") {
		t.Fatalf("空指令应拒绝：%q", out)
	}
	if out := router.Execute("session.new"); !strings.HasPrefix(out, "ERR|BAD_PREFIX|") {
		t.Fatalf("越界前缀应拒绝：%q", out)
	}
	if out := router.Execute("cat.list"); !strings.Contains(out, "cat.list | 1 只猫") || !strings.Contains(out, "运行中 :8079") {
		t.Fatalf("cat.list 回执不符：%q", out)
	}
	if out := router.Execute("cat.info"); !strings.Contains(out, `"tool": "cat.info"`) || !strings.Contains(out, `"id": "majordomo"`) {
		t.Fatalf("cat.info 回执不符：%q", out)
	}
	if out := router.Execute("cat.new X"); !strings.HasPrefix(out, "ERR|NOT_IMPLEMENTED|") {
		t.Fatalf("cat.new 应明示未实现：%q", out)
	}
	if out := router.Execute("cat.pause majordomo"); !strings.Contains(out, "不可自查") {
		t.Fatalf("cat.pause 自锁文案不符：%q", out)
	}
	if out := router.Execute("cat.stop other"); !strings.Contains(out, "未找到猫: other") {
		t.Fatalf("cat.stop 未找到猫文案不符：%q", out)
	}
	if out := router.Execute("cat.chat majordomo 你好"); !strings.Contains(out, "不可自查") {
		t.Fatalf("cat.chat 自锁文案不符：%q", out)
	}
	if out := router.Execute("cat.cfg.get majordomo"); !strings.HasPrefix(out, "cat.cfg.get | cat.cfg 不存在") {
		t.Fatalf("cat.cfg.get 缺档文案不符：%q", out)
	}
	if out := router.Execute("catcfg.apply majordomo"); !strings.HasPrefix(out, "catcfg.apply | cat.cfg 不存在") {
		t.Fatalf("catcfg.apply 缺档文案不符：%q", out)
	}
	if out := router.Execute("cat.bogus"); !strings.Contains(out, "cat.* 指令未识别") || !strings.Contains(out, "catcfg.apply <key>") {
		t.Fatalf("未识别指令应附用法清单：%q", out)
	}
	// cat.new-session——单猫置待消费标志
	if out := router.Execute("cat.new-session majordomo"); !strings.Contains(out, "已请求新会话: majordomo") {
		t.Fatalf("cat.new-session 回执不符：%q", out)
	}
	if !router.pendingRebuild["majordomo"].pending {
		t.Fatal("cat.new-session 应置待消费标志")
	}
	// 停机态——统一拒收
	prev := session.HostRestarting
	session.HostRestarting = func() bool { return true }
	t.Cleanup(func() { session.HostRestarting = prev })
	if out := router.Execute("cat.list"); !strings.HasPrefix(out, "ERR|HOST_RESTARTING|") {
		t.Fatalf("停机态应拒收：%q", out)
	}
}

// TestRestartGateBlocker 全局 Idle 判据——会话排队 / OA 在途两态。
func TestRestartGateBlocker(t *testing.T) {
	dir := t.TempDir()
	prevRestart := session.HostRestarting
	prevPending := session.PendingStoreDir()
	session.HostRestarting = func() bool { return false }
	session.ConfigurePendingStore(dir)
	t.Cleanup(func() {
		session.HostRestarting = prevRestart
		session.ConfigurePendingStore(prevPending)
	})
	ctx := session.NewContext()
	store := session.NewSessionStore(filepath.Join(dir, "majordomo.jsonl"))
	views := session.NewViewStore(filepath.Join(dir, "majordomo.view.jsonl"))
	sess := session.NewSession("majordomo", ctx, store, views)
	db := runtime.NewDataBox()
	oa := runtime.NewOA(nil)
	runner := runtime.NewRunner(db, oa, nil)
	state := admin.NewRestartState(nil)
	gate := newRestartGate(state, sess, runner, oa, &config.WorkspaceConfig{}, dir, 8079, "dev", "", nil)
	if blocker := gate.describeBlocker(); blocker != "" {
		t.Fatalf("空宿主应可执行接力，got %q", blocker)
	}
	// 会话排队 → 卡点
	if !sess.PostUser("hi", "user", "", "") {
		t.Fatal("投递失败")
	}
	if blocker := gate.describeBlocker(); !strings.Contains(blocker, "非 Idle") {
		t.Fatalf("排队应卡点，got %q", blocker)
	}
	sess.Pending.TakeAll(true)
	// OA 在途 → 卡点
	oa.Post(1, "TOOL", "text-read", 1200)
	if blocker := gate.describeBlocker(); !strings.Contains(blocker, "OA 在途") {
		t.Fatalf("OA 在途应卡点，got %q", blocker)
	}
}

// TestResolveReportPath 报告落点单点——target > staging > 父目录 > 显式覆盖。
func TestResolveReportPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "MauOut5")
	// target 与 staging 都不存在 → 父目录
	r := &deployRun{o: deployOptions{Target: target}}
	if got := filepath.Dir(r.resolveReportPath()); got != dir {
		t.Fatalf("兜底应落父目录：%q", got)
	}
	// staging 存在 → staging
	if err := os.MkdirAll(target+"_staging", 0o755); err != nil {
		t.Fatal(err)
	}
	r = &deployRun{o: deployOptions{Target: target}}
	if got := filepath.Dir(r.resolveReportPath()); got != target+"_staging" {
		t.Fatalf("应落暂存区：%q", got)
	}
	// target 存在 → target（优先级最高）
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	r = &deployRun{o: deployOptions{Target: target}}
	if got := filepath.Dir(r.resolveReportPath()); got != target {
		t.Fatalf("应落运行区：%q", got)
	}
	// 显式 --report 优先
	explicit := filepath.Join(dir, "custom", "report.json")
	r = &deployRun{o: deployOptions{Target: target, Report: explicit}}
	if got := r.resolveReportPath(); got != explicit {
		t.Fatalf("显式路径应优先：%q", got)
	}
	// ensureReportPath 只解析一次
	r = &deployRun{o: deployOptions{Target: target}}
	first := r.ensureReportPath()
	if second := r.ensureReportPath(); second != first {
		t.Fatalf("报告路径应单点解析：%q != %q", first, second)
	}
}

// TestBuildRestartPush 回执注入串——full 失败 / host 原地 / 说明与报告行。
func TestBuildRestartPush(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cathome5.exe"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	push := buildRestartPush("full", false, false, "0.25.0", dir, "备注", filepath.Join(dir, "report.json"), 2500*time.Millisecond)
	for _, want := range []string{
		"[宿主自更新] 重启完成",
		"模式: full",
		"结果: FAIL | prepare 未通过 | deploy 未通过",
		"版本: 0.25.0",
		"部署: prepare FAIL · deploy 跳过",
		"自启: 现有版本（运行区未替换）",
		"重启用时: 2.5 s（接力起算）",
		"产物: cathome5.exe ",
		"报告: ",
		"说明: 备注",
	} {
		if !strings.Contains(push, want) {
			t.Fatalf("注入串缺 %q：\n%s", want, push)
		}
	}
	host := buildRestartPush("host", true, false, "0.25.0", dir, "", "", 0)
	for _, want := range []string{"结果: OK", "部署: prepare 跳过 · deploy 跳过", "自启: 原地重启（运行区未替换）"} {
		if !strings.Contains(host, want) {
			t.Fatalf("host 注入串缺 %q：\n%s", want, host)
		}
	}
}

// TestRestartGateAbortsOnForeignHost 闸门前置（CH4 1.8.266 同因）——目标运行区已有宿主实例：
// 撤销重启态并出声（点名占用者），宿主不下线、不拉起接力。
func TestRestartGateAbortsOnForeignHost(t *testing.T) {
	prevDetect := hostScanForeignInTarget
	t.Cleanup(func() { hostScanForeignInTarget = prevDetect })
	hostScanForeignInTarget = func(target string, logf func(level int, message, category string)) string {
		return `pid 4242（D:\tmp\MauOut5\cathome5.exe）`
	}
	prevRestart := session.HostRestarting
	t.Cleanup(func() { session.HostRestarting = prevRestart })
	var logs []string
	state := admin.NewRestartState(func(level int, message string) { logs = append(logs, message) })
	gate := newRestartGate(state, nil, nil, runtime.NewOA(nil), &config.WorkspaceConfig{}, t.TempDir(), 8079, "dev", "", func(level int, message, category string) {
		logs = append(logs, message)
	})
	state.Request("host", filepath.Join(t.TempDir(), "MauOut5"), "")
	if gate.Tick() {
		t.Fatal("检出目标运行区占用者时不应拉起接力")
	}
	if _, ok := state.Snapshot(); ok {
		t.Fatal("重启态应被撤销（宿主继续服务）")
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "检测到另一宿主实例正在使用目标运行区：pid 4242") {
		t.Fatalf("撤销原因应点名占用者：\n%s", joined)
	}
	if !strings.Contains(joined, "请先停止该实例再重启") {
		t.Fatalf("撤销原因应给出处置：\n%s", joined)
	}
}
