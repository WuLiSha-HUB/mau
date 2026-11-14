package main

import (
	"path/filepath"
	"strings"
	"testing"

	"cathome5/host/config"
	"cathome5/host/session"
	"cathome5/host/toolpool"
)

// newCommandRouter 构造单猫形态的路由（会话 + 延迟队列；日志收集可选）。
func newCommandRouter(t *testing.T) (*commandRouter, *session.DelayQueue, *[]string) {
	t.Helper()
	dir := t.TempDir()
	queue := session.NewDelayQueue(filepath.Join(dir, "delays.json"))
	ctx := session.NewContext()
	store := session.NewSessionStore(filepath.Join(dir, "majordomo.jsonl"))
	views := session.NewViewStore(filepath.Join(dir, "majordomo.view.jsonl"))
	sess := session.NewSession("majordomo", ctx, store, views)
	sess.SetDelayQueue(queue)
	logs := &[]string{}
	router := &commandRouter{
		DataRoot:    dir,
		CatID:       "majordomo",
		DisplayName: "majordomo",
		Session:     sess,
		Pool:        &toolpool.Pool{},
		Registry:    &toolpool.Registry{},
		Running:     true,
		Port:        8079,
		Logf: func(level int, message, category string) {
			*logs = append(*logs, category+"|"+message)
		},
	}
	return router, queue, logs
}

// TestCommandRouterDelay 延迟指令族——合法登记 / 解析失败面 / 改时刻 / 取消 / 循环 / 列表 / 未识别。
func TestCommandRouterDelay(t *testing.T) {
	router, queue, _ := newCommandRouter(t)
	queue.SetNowProvider(func() int64 { return 1_000_000 })

	if !router.HandleDelay("delay.add|60|提醒一下") {
		t.Fatal("delay.add 应识别")
	}
	if fragment := queue.BuildEntriesFragment("majordomo"); !strings.Contains(fragment, `"content":"提醒一下"`) {
		t.Fatalf("登记未入队: %s", fragment)
	}
	cases := []struct {
		name string
		line string
		want string
	}{
		{"缺分隔符", "delay.add|60", "ERR|BAD_ARGS|delay.add 需要 <秒>|<内容>"},
		{"秒数非法", "delay.add|abc|x", "ERR|BAD_ARGS|秒数非法: abc"},
		{"秒数为负", "delay.add|-1|x", "ERR|BAD_ARGS|秒数不能为负"},
		{"秒数过大", "delay.add|315360001|x", "ERR|BAD_ARGS|秒数过大（上限 315360000）"},
		{"内容为空", "delay.add|60|   ", "ERR|BAD_ARGS|延迟条目内容为空"},
		{"addat 分隔符", "delay.addat|123", "ERR|BAD_ARGS|delay.addat 需要 <unixms>|<内容>"},
		{"addat 非法时刻", "delay.addat|abc|x", "ERR|BAD_ARGS|时刻非法: abc"},
		{"addat 非正时刻", "delay.addat|0|x", "ERR|BAD_ARGS|时刻非法（需 Unix 毫秒）"},
		{"addatloop 过去时刻", "delay.addatloop|995000|x", "ERR|BAD_ARGS|循环条目需要未来的触发时刻（当前跨度 -5000ms）"},
		{"set 分隔符", "delay.set|1", "ERR|BAD_ARGS|delay.set 需要 <id>|<unixms>"},
		{"set 序号非法", "delay.set|abc|1000", "ERR|BAD_ARGS|条目序号非法: abc"},
		{"set 时刻非法", "delay.set|1|abc", "ERR|BAD_ARGS|时刻非法: abc"},
		{"cancel 序号非法", "delay.cancel|abc", "ERR|BAD_ARGS|条目序号非法: abc"},
		{"loop 分隔符", "delay.loop|1", "ERR|BAD_ARGS|delay.loop 需要 <id>|<0|1>"},
		{"loop 标记非法", "delay.loop|1|2", "ERR|BAD_ARGS|loop 标记需为 0 或 1（当前 2）"},
		{"未识别", "delay.bogus", "ERR|BAD_ARGS|未知延迟指令（支持 delay.add|<秒>|<内容> · delay.addat|<unixms>|<内容> · delay.addatloop|<unixms>|<内容> · delay.set|<id>|<unixms> · delay.cancel|<id> · delay.loop|<id>|<0|1> · delay.list）"},
	}
	for _, item := range cases {
		if got := router.executeDelay(item.line); got != item.want {
			t.Fatalf("%s: got %q want %q", item.name, got, item.want)
		}
	}
	// 绝对时刻登记 + 改时刻 + 循环开关 + 取消（id=1 为上面 add 的条目）
	if got := router.executeDelay("delay.addat|1600000|绝对提醒"); !strings.HasPrefix(got, `{"ok":true,"id":2`) {
		t.Fatalf("addat 登记失败: %q", got)
	}
	if got := router.executeDelay("delay.set|2|2000000"); got != "ok|#2 due="+session.FormatDelayTime(2_000_000) {
		t.Fatalf("改时刻失败: %q", got)
	}
	if got := router.executeDelay("delay.loop|1|1"); got != "ok|#1 循环开" {
		t.Fatalf("循环开失败: %q", got)
	}
	if got := router.executeDelay("delay.cancel|2"); got != "ok|#2 已取消" {
		t.Fatalf("取消失败: %q", got)
	}
	list := router.executeDelay("delay.list")
	if !strings.HasPrefix(list, "延迟队列：1 条（本会话）") || !strings.Contains(list, "🔁") || !strings.Contains(list, "提醒一下") {
		t.Fatalf("delay.list 文本不符: %q", list)
	}
}

// TestCommandRouterDelayLogging 日志口径——「延迟指令: 行 → 结果」，ERR 升 2 级。
func TestCommandRouterDelayLogging(t *testing.T) {
	router, _, logs := newCommandRouter(t)
	if !router.Handle("delay.list") {
		t.Fatal("delay.list 应受理")
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "DELAY|延迟指令: delay.list → 延迟队列：空（本会话无待触发条目）") {
		t.Fatalf("成功日志不符: %v", *logs)
	}
	*logs = nil
	router.Handle("delay.add|abc|x")
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "DELAY|延迟指令: delay.add|abc|x → ERR|BAD_ARGS|秒数非法: abc") {
		t.Fatalf("失败日志不符: %v", *logs)
	}
}

// TestCommandRouterDelayPrefix 工具桥白名单——delay.* 不进 host.command（C# ExecuteAdminCommand 同口径）。
func TestCommandRouterDelayPrefix(t *testing.T) {
	router, _, _ := newCommandRouter(t)
	got := router.Execute("delay.list")
	if !strings.HasPrefix(got, "ERR|BAD_PREFIX|仅接受 cat.* / catcfg.* 指令") {
		t.Fatalf("delay 不应进工具桥白名单: %q", got)
	}
}

// TestCommandRouterCatTexts cat.* 文案——停止 / 删除自锁、start 已运行、usage 与 C# 逐字。
func TestCommandRouterCatTexts(t *testing.T) {
	router, _, _ := newCommandRouter(t)
	cases := []struct {
		line string
		want string
	}{
		{"cat.stop majordomo", "cat.stop | majordomo 为特殊会话——强制自启，不可停止"},
		{"cat.delete majordomo", "cat.delete | majordomo 为特殊会话——不可删除"},
		{"cat.pause majordomo", "cat.pause | majordomo 为特殊会话——不可自查（主干会话不能对自己下中止指令）"},
		{"cat.continue majordomo", "cat.continue | majordomo 为特殊会话——不可自查（主干会话不能对自己下继续指令）"},
		{"cat.chat majordomo 你好", "cat.chat | majordomo 为特殊会话——不可自查（主干会话不能对自己投递对话）"},
		{"cat.stop other", "cat.stop | 未找到猫: other"},
		{"cat.delete other", "cat.delete | 未找到猫: other"},
		{"cat.start other", "cat.start | 未找到猫: other"},
		{"cat.start majordomo", "cat.start | 已在运行: majordomo :8079"},
		{"cat.new X", "ERR|NOT_IMPLEMENTED|多猫面未接线（cat.new）"},
		{"cat.session.new other", "cat.session.new | ERR|CAT_OFFLINE|猫未运行: other"},
	}
	for _, item := range cases {
		if got := router.Execute(item.line); got != item.want {
			t.Fatalf("%s: got %q want %q", item.line, got, item.want)
		}
	}
	// 裸 cat.start / cat.stop 不匹配带空格分支——按未识别 + 用法（C# 同形）
	for _, line := range []string{"cat.start", "cat.stop", "cat.delete"} {
		if got := router.Execute(line); !strings.Contains(got, "cat.* 指令未识别") {
			t.Fatalf("%s 应走未识别清单: %q", line, got)
		}
	}
	usage := router.Execute("cat.bogus")
	for _, want := range []string{
		"cat.* 指令未识别: cat.bogus",
		"cat.new <显示名> | 新建猫（静默态，待 cat.start）",
		"cat.start <key> | 分配端口 + 拉起 HttpHost",
		"catcfg.apply <key> | 每猫配置运行时生效",
	} {
		if !strings.Contains(usage, want) {
			t.Fatalf("usage 缺 %q: %s", want, usage)
		}
	}
	if strings.Contains(usage, "单猫形态未实现") {
		t.Fatalf("usage 不应带实现状态注解: %s", usage)
	}
}

// TestCommandRouterCatSessionNewBusy 跨猫会话重建——非空闲拒重建（C# CAT_BUSY 文案）。
func TestCommandRouterCatSessionNewBusy(t *testing.T) {
	dir := t.TempDir()
	prev := session.PendingStoreDir()
	session.ConfigurePendingStore(dir)
	t.Cleanup(func() { session.ConfigurePendingStore(prev) })

	router, _, _ := newCommandRouter(t)
	if !router.Session.PostUser("hi", "user", "", "") {
		t.Fatal("投递失败")
	}
	got := router.Execute("cat.session.new majordomo")
	if got != "cat.session.new | ERR|CAT_BUSY|猫忙——仅在空闲时可重建: majordomo" {
		t.Fatalf("忙态文案不符: %q", got)
	}
	if got := router.Execute("cat.session.new"); !strings.Contains(got, "cat.* 指令未识别") {
		t.Fatalf("裸 cat.session.new 应走未识别: %q", got)
	}
}

// TestCommandRouterHTTPRoutes 主端口指令面——cat.* / catcfg.* 走 Execute 并记 cat 指令日志；停机态拒收。
func TestCommandRouterHTTPRoutes(t *testing.T) {
	router, _, logs := newCommandRouter(t)
	if !router.Handle("cat.pause majordomo") {
		t.Fatal("cat.pause 应受理")
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "CMD|cat 指令: cat.pause | majordomo 为特殊会话") {
		t.Fatalf("cat.* 日志不符: %v", *logs)
	}
	*logs = nil
	if !router.Handle("catcfg.apply other") {
		t.Fatal("catcfg.apply 应受理")
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "CMD|cat 指令: catcfg.apply | 猫不存在: other") {
		t.Fatalf("catcfg.* 日志不符: %v", *logs)
	}

	prev := session.HostRestarting
	session.HostRestarting = func() bool { return true }
	t.Cleanup(func() { session.HostRestarting = prev })
	if router.Handle("delay.list") {
		t.Fatal("停机态应拒收 delay.*")
	}
	if router.Handle("cat.pause majordomo") {
		t.Fatal("停机态应拒收 cat.*")
	}
}

// TestHandleForAutoMountWiring 每猫端口 Chat 自动挂载接线——hook 判定后按 fresh 分流（fresh=false 普通投递）。
func TestHandleForAutoMountWiring(t *testing.T) {
	router, _, _ := newCommandRouter(t)
	mounted := ""
	router.AutoMountForCat = func(catID string, sess *session.Session, content string) (bool, bool) {
		mounted = catID + "|" + content
		return true, false
	}
	if !router.HandleFor("catX", router.Session, "Chat 上工 Demo") {
		t.Fatal("应受理")
	}
	if mounted != "catX|上工 Demo" {
		t.Fatalf("自动挂载未按猫触发：%q", mounted)
	}
	// fresh=false → 按普通输入投递（正文已剥前缀，入待处理队列一条用户消息）
	userCount, _ := router.Session.Pending.Len()
	if userCount != 1 {
		t.Fatalf("普通投递应入队 1 条用户消息，实得 %d", userCount)
	}
	// 空内容不接管
	if router.HandleFor("catX", router.Session, "Chat   ") {
		t.Fatal("空内容不应受理")
	}
}

// TestCatChatAutoMountWiring cat.chat 投递前挂载钩子——先挂载再投递（Cats.cs:383 顺序）。
func TestCatChatAutoMountWiring(t *testing.T) {
	dir := t.TempDir()
	queue := session.NewDelayQueue(filepath.Join(dir, "delays.json"))
	sess := session.NewSession("catA", session.NewContext(), nil, nil)
	sess.SetDelayQueue(queue)
	order := []string{}
	router := &commandRouter{
		DataRoot:    dir,
		CatID:       "catA",
		DisplayName: "A猫",
		Session:     sess,
		Pool:        &toolpool.Pool{},
		Registry:    &toolpool.Registry{},
		Logf:        func(level int, message, category string) {},
		AutoMountOnCatChat: func(catID, content string) {
			order = append(order, "mount:"+catID+"|"+content)
		},
	}
	got := router.Execute("cat.chat catA 上工 Demo")
	if !strings.HasPrefix(got, "cat.chat | 已投递: A猫") {
		t.Fatalf("cat.chat 回执不符：%q", got)
	}
	if len(order) != 1 || order[0] != "mount:catA|上工 Demo" {
		t.Fatalf("自动挂载未先于投递：%v", order)
	}
	if userCount, _ := sess.Pending.Len(); userCount != 1 {
		t.Fatalf("投递应入队 1 条用户消息，实得 %d", userCount)
	}
}

// TestCatCfgSetFollowAndBroadcast 全跟随三分支 + 模板广播——点亮同步 / 熄灭保持 / 广播生效。
func TestCatCfgSetFollowAndBroadcast(t *testing.T) {
	router, _, _ := newCommandRouter(t)
	tpl := &config.CatDefault{
		DefaultToolNames:      "text-read",
		DefaultInjectList:     []string{"ccbp:A.md", "ccbp:B.md"},
		DefaultInjectDisabled: []string{"ccbp:B.md"},
		DefaultEnabledRoots:   []string{"ccbp", "bad-root"},
	}
	if err := config.SaveCatDefault(filepath.Join(router.DataRoot, "config", "cat-default.cfg"), tpl); err != nil {
		t.Fatalf("写模板失败: %v", err)
	}
	cfgPath := filepath.Join(router.DataRoot, "sessions", "majordomo", "cat.cfg")
	if err := config.SaveCatCfg(cfgPath, &config.CatCfg{ID: "majordomo", DisplayName: "majordomo"}); err != nil {
		t.Fatalf("写 cat.cfg 失败: %v", err)
	}
	router.ValidateEnabledRoots = func(ids []string) ([]string, []string, bool) {
		kept := []string{"workspace"}
		removed := []string{}
		for _, id := range ids {
			if id == "ccbp" {
				kept = append(kept, id)
				continue
			}
			removed = append(removed, id)
		}
		return kept, removed, false
	}
	if got := router.catCfgSet("majordomo followDefaultTools true"); !strings.Contains(got, "已更新") {
		t.Fatalf("工具跟随写入失败: %s", got)
	}
	cfg, _ := config.LoadCatCfg(cfgPath)
	if !cfg.FollowDefaultTools || cfg.ToolNames != "text-read" {
		t.Fatalf("工具跟随未同步模板: %+v", cfg)
	}
	if got := router.catCfgSet("majordomo followDefaultInject true"); !strings.Contains(got, "前文注入已同步模板启用集（1 行）") {
		t.Fatalf("前文跟随回执不符: %s", got)
	}
	cfg, _ = config.LoadCatCfg(cfgPath)
	if !cfg.FollowDefaultInject || strings.Join(cfg.InjectList, ",") != "ccbp:A.md" {
		t.Fatalf("前文启用集未排除 disabled: %+v", cfg.InjectList)
	}
	if got := router.catCfgSet("majordomo followDefaultRoots true"); !strings.Contains(got, "受控根已同步模板（2 项）") {
		t.Fatalf("受控根跟随回执不符: %s", got)
	}
	cfg, _ = config.LoadCatCfg(cfgPath)
	if !cfg.FollowDefaultRoots || strings.Join(cfg.EnabledRoots, ",") != "workspace,ccbp" {
		t.Fatalf("受控根未同步: %v", cfg.EnabledRoots)
	}
	// 模板改动 → 广播同步到跟随猫
	tpl.DefaultToolNames = "file-tree"
	tpl.DefaultInjectList = []string{"ccbp:C.md"}
	tpl.DefaultInjectDisabled = nil
	if err := config.SaveCatDefault(filepath.Join(router.DataRoot, "config", "cat-default.cfg"), tpl); err != nil {
		t.Fatalf("改模板失败: %v", err)
	}
	got := router.Execute("catcfg.tplbroadcast")
	if !strings.Contains(got, "1 只猫已跟随更新") {
		t.Fatalf("广播回执不符: %s", got)
	}
	cfg, _ = config.LoadCatCfg(cfgPath)
	if cfg.ToolNames != "file-tree" || strings.Join(cfg.InjectList, ",") != "ccbp:C.md" {
		t.Fatalf("广播未同步: %+v", cfg)
	}
	// 非法值出声
	if got := router.catCfgSet("majordomo followDefaultTools maybe"); !strings.Contains(got, "非法值") {
		t.Fatalf("非法值未拒绝: %s", got)
	}
}

// TestHandleForLocalCatControl 本地猫控——每猫端口无参 cat.pause / cat.continue 作用于本端口会话
// （对齐 C# InputRoutes RoutePause / RouteContinue + LocalCatControlOnly）；主入口裸 cat.pause 仍归管理族报用法。
func TestHandleForLocalCatControl(t *testing.T) {
	router, _, logs := newCommandRouter(t)
	sess := router.Session

	// 每猫端口·无参 cat.pause——受理 + 出声
	if !router.HandleFor("majordomo", sess, "cat.pause") {
		t.Fatal("每猫端口无参 cat.pause 应受理")
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "CMD|cat.pause | 已投递中止（本端口会话）: majordomo") {
		t.Fatalf("无参 cat.pause 日志不符: %v", *logs)
	}

	// 每猫端口·无参 cat.continue——受理 + 出声
	*logs = nil
	if !router.HandleFor("majordomo", sess, "cat.continue") {
		t.Fatal("每猫端口无参 cat.continue 应受理")
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "CMD|cat.continue | 已投递继续（本端口会话）: majordomo") {
		t.Fatalf("无参 cat.continue 日志不符: %v", *logs)
	}

	// 主入口·裸 cat.pause——仍归管理族报用法（无参不命中本地猫控分支）
	*logs = nil
	if !router.Handle("cat.pause") {
		t.Fatal("主入口裸 cat.pause 应归管理族受理")
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "cat.pause | 用法: cat.pause <key>") {
		t.Fatalf("主入口裸 cat.pause 应报用法: %v", *logs)
	}

	// 带 key 形式——全等不命中本地分支，仍走管理族（未找到猫）
	*logs = nil
	if !router.HandleFor("majordomo", sess, "cat.pause other") {
		t.Fatal("带 key 形式应归管理族受理")
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "cat.pause | 未找到猫: other") {
		t.Fatalf("带 key 形式文案不符: %v", *logs)
	}
}

// TestSplitInjectListPreservesSpaces 前文注入清单只按逗号拆——路径里的空格必须原样保留。
//
// 判例（2026-11-14）：injectList 曾与 toolNames 共用「逗号 + 空白」口径，
// "ccbp:templates/industry standards/sprout" 被劈成 "ccbp:templates/industry" 且后半截静默丢弃。
func TestSplitInjectListPreservesSpaces(t *testing.T) {
	got := splitInjectList("ccbp:SOUL.md, ccbp:templates/industry standards/sprout ,ccbp:L3/Mau,, ")
	want := []string{"ccbp:SOUL.md", "ccbp:templates/industry standards/sprout", "ccbp:L3/Mau"}
	if len(got) != len(want) {
		t.Fatalf("条目数不符：got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 项不符：got %q want %q", i, got[i], want[i])
		}
	}
	// 标识符类清单保持原口径（逗号 + 空白都拆）——两条口径不互相污染
	if items := splitCfgList("a, b\tc"); len(items) != 3 {
		t.Fatalf("标识符清单口径变了：%v", items)
	}
}
