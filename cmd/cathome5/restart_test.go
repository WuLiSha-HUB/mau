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

// TestSettleRestartIntent 启动期结算——受控重启写 done / 无接力回执写 stale / 已结算不动。
func TestSettleRestartIntent(t *testing.T) {
	dir := t.TempDir()
	store := admin.NewRestartIntentStore(filepath.Join(dir, "runtime", "restart-intent.json"))
	// 无现场 → 不结算
	if _, changed := settleRestartIntent(store, "回执", "0.36.4", 999); changed {
		t.Fatal("无现场不应结算")
	}
	// 受控重启（带 --majordomopush）→ done
	if err := store.Registered("incr", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Launching(100, 200); err != nil {
		t.Fatal(err)
	}
	settled, changed := settleRestartIntent(store, "[宿主自更新] 重启完成", "0.36.4", 999)
	if !changed || settled.Phase != admin.RestartPhaseDone {
		t.Fatalf("受控重启应结算 done: changed=%v %+v", changed, settled)
	}
	if settled.NewPID != 999 || settled.Version != "0.36.4" || settled.RelayPID != 200 {
		t.Fatalf("结算字段不符: %+v", settled)
	}
	// 已结算 → 不动
	if _, changed := settleRestartIntent(store, "", "0.36.4", 1000); changed {
		t.Fatal("已结算现场不应再改写")
	}
	// 停在 launching 且无回执 → stale（重启链未完成）
	if err := store.Registered("full", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Launching(100, 201); err != nil {
		t.Fatal(err)
	}
	stale, changed := settleRestartIntent(store, "", "0.36.3", 1001)
	if !changed || stale.Phase != admin.RestartPhaseStale || stale.Reason == "" {
		t.Fatalf("无接力回执应结算 stale: changed=%v %+v", changed, stale)
	}
	summary := restartIntentSummary(stale)
	for _, want := range []string{"重启现场: stale", "mode=full", "relayPid=201", "原因:"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("摘要缺 %q：%s", want, summary)
		}
	}
}

// TestResumePendingRounds 轮级续跑——非受控只出声 / 受控续跑一次 / 上限 1 次停手。
func TestResumePendingRounds(t *testing.T) {
	dir := t.TempDir()
	prevRestart := session.HostRestarting
	prevProvider := session.PendingRoundPathProvider
	prevPending := session.PendingStoreDir()
	session.HostRestarting = func() bool { return false }
	session.PendingRoundPathProvider = func(catID string) string {
		return filepath.Join(dir, catID+".pending-round.json")
	}
	session.ConfigurePendingStore(dir)
	t.Cleanup(func() {
		session.HostRestarting = prevRestart
		session.PendingRoundPathProvider = prevProvider
		session.ConfigurePendingStore(prevPending)
	})

	ctx := session.NewContext()
	store := session.NewSessionStore(filepath.Join(dir, "majordomo.jsonl"))
	views := session.NewViewStore(filepath.Join(dir, "majordomo.view.jsonl"))
	sess := session.NewSession("majordomo", ctx, store, views)
	cats := newCatRegistry(dir)
	cats.setMajor(&sessionWiring{CatID: "majordomo", Session: sess}, "majordomo", 8079)
	var logs []string
	logf := func(level int, message string) { logs = append(logs, message) }

	// 无现场 → 零动作
	if resumed, skipped := resumePendingRounds(cats, true, logf); resumed != 0 || skipped != 0 {
		t.Fatalf("无现场应零动作: resumed=%d skipped=%d", resumed, skipped)
	}
	// 有现场但非受控重启 → 只出声不续
	sess.MarkRoundPending()
	if resumed, skipped := resumePendingRounds(cats, false, logf); resumed != 0 || skipped != 1 {
		t.Fatalf("非受控应只出声: resumed=%d skipped=%d", resumed, skipped)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "非受控重启") {
		t.Fatalf("非受控出声缺文案：%v", logs)
	}
	if user, _ := sess.Pending.Len(); user != 0 {
		t.Fatalf("非受控不应注入消息: user=%d", user)
	}
	// 受控重启 → 续跑一次（attempts 落盘 1）
	logs = nil
	if resumed, skipped := resumePendingRounds(cats, true, logf); resumed != 1 || skipped != 0 {
		t.Fatalf("受控应续跑一次: resumed=%d skipped=%d logs=%v", resumed, skipped, logs)
	}
	if user, _ := sess.Pending.Len(); user != 1 {
		t.Fatalf("续跑应注入一条用户消息: user=%d", user)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "重启续跑已注入") {
		t.Fatalf("续跑出声缺文案：%v", logs)
	}
	sess.Pending.TakeAll(true)
	// 上限——attempts=1 再受控重启 → 跳过 + 注入人工提示
	logs = nil
	if resumed, skipped := resumePendingRounds(cats, true, logf); resumed != 0 || skipped != 1 {
		t.Fatalf("attempts 上限应跳过: resumed=%d skipped=%d", resumed, skipped)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "上限 1 次") {
		t.Fatalf("上限出声缺文案：%v", logs)
	}
	if _, sys := sess.Pending.Len(); sys != 1 {
		t.Fatalf("上限应注入一条 system 提示: sys=%d", sys)
	}
}

// TestRestartGateScopeSelf scope=self 只判发起会话——其它猫非 Idle 不构成卡点，OA 在途仍是硬等。
func TestRestartGateScopeSelf(t *testing.T) {
	dir := t.TempDir()
	prevRestart := session.HostRestarting
	prevPending := session.PendingStoreDir()
	session.HostRestarting = func() bool { return false }
	session.ConfigurePendingStore(dir)
	t.Cleanup(func() {
		session.HostRestarting = prevRestart
		session.ConfigurePendingStore(prevPending)
	})
	newSess := func(key string) *session.Session {
		ctx := session.NewContext()
		store := session.NewSessionStore(filepath.Join(dir, key+".jsonl"))
		views := session.NewViewStore(filepath.Join(dir, key+".view.jsonl"))
		sess := session.NewSession(key, ctx, store, views)
		sess.SetDisplayName(key)
		return sess
	}
	major := newSess("majordomo")
	other := newSess("R喵7")
	gate := newRestartGate(admin.NewRestartState(nil), major, nil, runtime.NewOA(nil), &config.WorkspaceConfig{}, dir, 8079, "dev", "", nil)
	gate.setSessionsProvider(func() []*session.Session { return []*session.Session{major, other} })

	// 其它猫排队 → 全局判据卡点；scope=self 只判发起者（Idle）→ 放行
	if !other.PostUser("hi", "user", "", "") {
		t.Fatal("投递失败")
	}
	if blocker := gate.describeBlockerFor(""); !strings.Contains(blocker, "R喵7") {
		t.Fatalf("全局判据应列出其它猫卡点，got %q", blocker)
	}
	if blocker := gate.describeBlockerFor("self"); blocker != "" {
		t.Fatalf("scope=self 不应等其它猫，got %q", blocker)
	}
	// 发起者自己排队 → self 也卡（打断自己会丢本轮）
	if !major.PostUser("hi", "user", "", "") {
		t.Fatal("投递失败")
	}
	if blocker := gate.describeBlockerFor("self"); !strings.Contains(blocker, "majordomo") {
		t.Fatalf("发起者非 Idle 应卡点，got %q", blocker)
	}
	major.Pending.TakeAll(true)
	other.Pending.TakeAll(true)
	// OA 在途——任何 scope 都硬等（会丢工具回执）
	oa := runtime.NewOA(nil)
	gate.oa = oa
	oa.Post(1, "TOOL", "text-read", 1200)
	if blocker := gate.describeBlockerFor("self"); !strings.Contains(blocker, "OA 在途") {
		t.Fatalf("OA 在途应硬等，got %q", blocker)
	}
}
