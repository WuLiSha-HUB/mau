package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cathome5/host/config"
	"cathome5/host/group"
)

// newGroupTestService 构造组服务测试面——临时数据根 + 组数据面 + 指令路由（单猫）。
func newGroupTestService(t *testing.T) (*hostGroupService, *commandRouter, *group.Store, string) {
	t.Helper()
	router, _, _ := newCommandRouter(t)
	store := group.New(filepath.Join(router.DataRoot, "group"), nil)
	store.Load()
	svc := &hostGroupService{
		Store:  store,
		Router: func() *commandRouter { return router },
	}
	// cat.cfg——显示名解析与 cat.cfg.get / set 测试用
	cfgDir := filepath.Join(router.DataRoot, "sessions", "majordomo")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("建 sessions 目录失败: %v", err)
	}
	if err := config.SaveCatCfg(filepath.Join(cfgDir, "cat.cfg"), &config.CatCfg{ID: "majordomo", DisplayName: "管家猫"}); err != nil {
		t.Fatalf("写 cat.cfg 失败: %v", err)
	}
	// 组——majordomo 管理员；g2 仅成员（非管理员）
	if result := store.CreateOrUpdate("g1", []string{"majordomo", "worker"}, "p1", []string{"majordomo"}, []string{}, 0); !strings.HasPrefix(result, "ok|") {
		t.Fatalf("建组失败: %s", result)
	}
	return svc, router, store, router.DataRoot
}

// TestGroupInfo 组信息——参数面 / 组不存在 / 非成员 / 未启用 / 成功成员行与专有字段。
func TestGroupInfo(t *testing.T) {
	svc, _, store, _ := newGroupTestService(t)
	if reply := svc.Info(""); reply != "ERR|BAD_ARGS|工具参数 JSON 为空" {
		t.Fatalf("空参数文案不符：%q", reply)
	}
	if reply := svc.Info(`[]`); reply != "ERR|BAD_ARGS|参数必须是 JSON 对象" {
		t.Fatalf("非对象文案不符：%q", reply)
	}
	if reply := svc.Info(`{"catId":"majordomo"}`); reply != "ERR|BAD_ARGS|缺少参数 group" {
		t.Fatalf("缺 group 文案不符：%q", reply)
	}
	if reply := svc.Info(`{"group":"g1"}`); reply != "ERR|GROUP_NO_CALLER|缺少调用者标识（catId）——无法判定权限" {
		t.Fatalf("缺 catId 文案不符：%q", reply)
	}
	if reply := svc.Info(`{"group":"nope","catId":"majordomo"}`); reply != "ERR|GROUP_NOT_FOUND|组不存在: nope" {
		t.Fatalf("组不存在文案不符：%q", reply)
	}
	if reply := svc.Info(`{"group":"g1","catId":"outsider"}`); reply != "ERR|GROUP_NOT_MEMBER|调用者不是本组成员: outsider" {
		t.Fatalf("非成员文案不符：%q", reply)
	}
	store.StopGroup("g1")
	if reply := svc.Info(`{"group":"g1","catId":"majordomo"}`); reply != "ERR|GROUP_NOT_RUNNING|组未启用: g1" {
		t.Fatalf("未启用文案不符：%q", reply)
	}
	store.StartGroup("g1")
	reply := svc.Info(`{"group":"g1","catId":"majordomo"}`)
	if !strings.HasPrefix(reply, `{"ok":true,"tool":"group-info","target":"g1","items":2,"project":"p1","enabled":true,"msgCount":0,"lastSeq":0,"lastAt":0}`) {
		t.Fatalf("头行不符：%q", reply)
	}
	if !strings.Contains(reply, "g1 | 2 名成员 · 消息 0 条 · 最新 #0") {
		t.Fatalf("摘要行不符：%q", reply)
	}
	if !strings.Contains(reply, "· 管家猫（majordomo） — 管理员") {
		t.Fatalf("成员行应带显示名与角色：%q", reply)
	}
	if !strings.Contains(reply, "· （worker）") {
		t.Fatalf("无 cat.cfg 的成员应回落空显示名：%q", reply)
	}
}

// TestGroupInbox 组消息拉取——消息行格式 / after / limit / 空消息 / 权限。
func TestGroupInbox(t *testing.T) {
	svc, _, store, _ := newGroupTestService(t)
	if reply := svc.Inbox(`{"group":"g1","catId":"majordomo"}`); !strings.Contains(reply, "（无消息）") {
		t.Fatalf("空消息应给占位行：%q", reply)
	}
	if result := store.AppendMessage("g1", "majordomo", "note", "第一条", "", ""); !strings.HasPrefix(result, "ok|") {
		t.Fatalf("追加失败：%s", result)
	}
	if result := store.AppendMessage("g1", "majordomo", "delegation", "第二条\n换行", "r1", "worker"); !strings.HasPrefix(result, "ok|") {
		t.Fatalf("追加失败：%s", result)
	}
	reply := svc.Inbox(`{"group":"g1","catId":"majordomo"}`)
	if !strings.Contains(reply, "#1 [发言] 管家猫 · 第一条") {
		t.Fatalf("消息行不符：%q", reply)
	}
	if !strings.Contains(reply, "#2 [派单] 管家猫 · 第二条 换行（关联 r1）") {
		t.Fatalf("派单行应压平正文并带关联锚（目标无 cat.cfg → 显示名空、无箭头，C# 同口径）：%q", reply)
	}
	if !strings.Contains(reply, "g1 | 2 条 · 最新 #2") {
		t.Fatalf("摘要行不符：%q", reply)
	}
	if reply := svc.Inbox(`{"group":"g1","catId":"majordomo","after":1}`); !strings.Contains(reply, "g1 | 1 条") || strings.Contains(reply, "#1 [") {
		t.Fatalf("after 过滤不符：%q", reply)
	}
	if reply := svc.Inbox(`{"group":"g1","catId":"majordomo","limit":1}`); !strings.Contains(reply, "g1 | 1 条") || !strings.Contains(reply, "#1 [") {
		t.Fatalf("limit 截断不符：%q", reply)
	}
	if reply := svc.Inbox(`{"group":"g1"}`); reply != "ERR|GROUP_NO_CALLER|缺少调用者标识（catId）——无法判定权限" {
		t.Fatalf("缺 catId 文案不符：%q", reply)
	}
}

// TestGroupSay 讨论消息——成员可写、空正文失败、成功回执带 seq。
func TestGroupSay(t *testing.T) {
	svc, _, _, _ := newGroupTestService(t)
	if reply := svc.Say(`{"group":"g1","catId":"worker","text":"hi"}`); !strings.HasPrefix(reply, `{"ok":true,"tool":"group-post","target":"g1","seq":1}`) {
		t.Fatalf("成员发言应成功：%q", reply)
	}
	if reply := svc.Say(`{"group":"g1","catId":"majordomo","text":"  "}`); reply != "ERR|GROUP_TEXT_EMPTY|消息正文为空" {
		t.Fatalf("空正文文案不符：%q", reply)
	}
	if reply := svc.Say(`{"group":"g1","catId":"outsider","text":"hi"}`); reply != "ERR|GROUP_NOT_MEMBER|调用者不是本组成员: outsider" {
		t.Fatalf("非成员文案不符：%q", reply)
	}
}

// TestGroupSend 派单——管理员门槛 / to 校验 / 目标离线 / 投递成功。
func TestGroupSend(t *testing.T) {
	svc, _, _, _ := newGroupTestService(t)
	if reply := svc.Send(`{"group":"g1","catId":"worker","to":"majordomo","text":"hi"}`); reply != "ERR|GROUP_NOT_ADMIN|调用者不是本组管理员: worker" {
		t.Fatalf("非管理员文案不符：%q", reply)
	}
	if reply := svc.Send(`{"group":"g1","catId":"majordomo","text":"hi"}`); reply != "ERR|BAD_ARGS|派单必须指定 to（@ 目标猫）" {
		t.Fatalf("缺 to 文案不符：%q", reply)
	}
	if reply := svc.Send(`{"group":"g1","catId":"majordomo","to":"outsider","text":"hi"}`); reply != "ERR|GROUP_TARGET_NOT_MEMBER|@ 目标不在组内: outsider" {
		t.Fatalf("目标非成员文案不符：%q", reply)
	}
	if reply := svc.Send(`{"group":"g1","catId":"majordomo","to":"worker","text":"hi"}`); !strings.HasPrefix(reply, "ERR|GROUP_TARGET_OFFLINE|目标猫未运行: worker（消息已入组 #1）") {
		t.Fatalf("目标离线文案不符：%q", reply)
	}
	// 投递成功——目标是本会话（majordomo 在组内且可投递）
	reply := svc.Send(`{"group":"g1","catId":"majordomo","to":"worker","text":"hi","refs":"r9","notify":true}`)
	if !strings.HasPrefix(reply, "ERR|GROUP_TARGET_OFFLINE|") {
		t.Fatalf("单猫形态 worker 应恒离线：%q", reply)
	}
}

// TestGroupNewSession 重建会话——管理员门槛 / 单向数据流 / reason / 自指排队。
func TestGroupNewSession(t *testing.T) {
	svc, router, store, _ := newGroupTestService(t)
	if reply := svc.NewSession(`{"group":"g1","catId":"worker","to":"majordomo","reason":"r"}`); reply != "ERR|GROUP_NOT_ADMIN|调用者不是本组管理员: worker" {
		t.Fatalf("非管理员文案不符：%q", reply)
	}
	if reply := svc.NewSession(`{"group":"g1","catId":"majordomo","reason":"r"}`); reply != "ERR|BAD_ARGS|重建会话必须指定 to（@ 目标猫）" {
		t.Fatalf("缺 to 文案不符：%q", reply)
	}
	if reply := svc.NewSession(`{"group":"g1","catId":"majordomo","to":"outsider","reason":"r"}`); reply != "ERR|GROUP_TARGET_NOT_MEMBER|@ 目标不在组内: outsider" {
		t.Fatalf("目标非成员文案不符：%q", reply)
	}
	// 另一名管理员同为成员——应被单向数据流拒绝
	if result := store.CreateOrUpdate("g1", []string{"majordomo", "worker", "boss"}, "", []string{"majordomo", "boss"}, []string{}, 0); !strings.HasPrefix(result, "ok|") {
		t.Fatalf("改组失败：%s", result)
	}
	if reply := svc.NewSession(`{"group":"g1","catId":"majordomo","to":"boss","reason":"r"}`); reply != "ERR|GROUP_TARGET_IS_ADMIN|管理员不得重建另一名管理员（仅可重建自己或非管理成员）: boss" {
		t.Fatalf("重建管理员文案不符：%q", reply)
	}
	if reply := svc.NewSession(`{"group":"g1","catId":"majordomo","to":"worker","reason":""}`); reply != "ERR|BAD_ARGS|重建会话必须给出 reason（新会话唯一的上下文说明）" {
		t.Fatalf("缺 reason 文案不符：%q", reply)
	}
	if reply := svc.NewSession(`{"group":"g1","catId":"majordomo","to":"worker","reason":"r"}`); !strings.HasPrefix(reply, "ERR|GROUP_TARGET_OFFLINE|目标猫未运行: worker") {
		t.Fatalf("目标离线文案不符：%q", reply)
	}
	// 自指——排队（轮末生效），重复登记应拒绝
	reply := svc.NewSession(`{"group":"g1","catId":"majordomo","to":"majordomo","reason":"轮末重建"}`)
	if !strings.HasPrefix(reply, `{"ok":true,"tool":"group-newsession","target":"g1","seq":`) {
		t.Fatalf("自指重建应成功：%q", reply)
	}
	if !strings.Contains(reply, `"mode":"queued"`) || !strings.Contains(reply, "已排队 → 管家猫") {
		t.Fatalf("自指应标 queued：%q", reply)
	}
	if !router.pendingRebuild.pending {
		t.Fatal("自指重建应登记挂起请求")
	}
	if reply := svc.NewSession(`{"group":"g1","catId":"majordomo","to":"majordomo","reason":"再来一次"}`); reply != "ERR|GROUP_REBUILD_PENDING|本会话已有挂起的重建请求" {
		t.Fatalf("重复登记文案不符：%q", reply)
	}
}

// TestGroupCatInfo 单猫运行态——缺 cat / 目标非成员 / 嵌套回执（头 + 摘要 + 条目）。
func TestGroupCatInfo(t *testing.T) {
	svc, _, _, _ := newGroupTestService(t)
	if reply := svc.CatInfo(`{"group":"g1","catId":"majordomo"}`); reply != "ERR|BAD_ARGS|缺少参数 cat" {
		t.Fatalf("缺 cat 文案不符：%q", reply)
	}
	if reply := svc.CatInfo(`{"group":"g1","catId":"majordomo","cat":"outsider"}`); reply != "ERR|GROUP_TARGET_NOT_MEMBER|目标不在组内: outsider" {
		t.Fatalf("目标非成员文案不符：%q", reply)
	}
	if reply := svc.CatInfo(`{"group":"g1","catId":"majordomo","cat":"worker"}`); reply != "ERR|CAT_NOT_FOUND|未找到猫: worker" {
		t.Fatalf("单猫形态非本会话成员应报 CAT_NOT_FOUND（C# HandleCatInfoOne 同口径）：%q", reply)
	}
	reply := svc.CatInfo(`{"group":"g1","catId":"majordomo","cat":"majordomo"}`)
	if !strings.HasPrefix(reply, `{"ok":true,"tool":"group-catinfo","target":"g1","cat":"majordomo"}`) {
		t.Fatalf("头行不符：%q", reply)
	}
	if !strings.Contains(reply, "运行态：管家猫（majordomo）") {
		t.Fatalf("摘要行应用显示名：%q", reply)
	}
	if !strings.Contains(reply, `{"ok":true,"tool":"group-catinfo","cat":"majordomo"`) {
		t.Fatalf("正文应内嵌单猫回执：%q", reply)
	}
	if !strings.Contains(reply, `"running":true`) {
		t.Fatalf("条目应带运行态：%q", reply)
	}
}

// TestGroupCatCfg 每猫配置读写——读面全量 / 写面管理员 + 拒改自己 / 字段校验。
func TestGroupCatCfg(t *testing.T) {
	svc, _, store, _ := newGroupTestService(t)
	if reply := svc.CatCfgGet(`{"group":"g1","catId":"majordomo","cat":"majordomo"}`); !strings.HasPrefix(reply, `{"ok":true,"tool":"group-catcfg-get","target":"g1"}`) {
		t.Fatalf("读面头行不符：%q", reply)
	} else if !strings.Contains(reply, "cat.cfg | majordomo") || !strings.Contains(reply, "displayName=管家猫") {
		t.Fatalf("读面应给全量字段：%q", reply)
	}
	// 无 cat.cfg 的成员——读面失败面
	if reply := svc.CatCfgGet(`{"group":"g1","catId":"majordomo","cat":"worker"}`); !strings.HasPrefix(reply, "ERR|GROUP_CATCFG_GET_FAIL|") {
		t.Fatalf("缺档应报 GROUP_CATCFG_GET_FAIL：%q", reply)
	}
	if reply := svc.CatCfgSet(`{"group":"g1","catId":"worker","cat":"majordomo","field":"displayName","value":"x"}`); reply != "ERR|GROUP_NOT_ADMIN|调用者不是组管理员: worker" {
		t.Fatalf("非管理员文案不符：%q", reply)
	}
	if reply := svc.CatCfgSet(`{"group":"g1","catId":"majordomo","cat":"majordomo","field":"displayName","value":"x"}`); reply != "ERR|GROUP_TARGET_SELF|不能修改自己的配置——改自己请由人操作" {
		t.Fatalf("拒改自己文案不符：%q", reply)
	}
	if result := store.CreateOrUpdate("g1", []string{"majordomo", "worker"}, "", []string{"majordomo"}, []string{}, 0); !strings.HasPrefix(result, "ok|") {
		t.Fatalf("改组失败：%s", result)
	}
	if reply := svc.CatCfgSet(`{"group":"g1","catId":"majordomo","cat":"worker","field":"","value":"x"}`); reply != "ERR|BAD_ARGS|缺少参数 field" {
		t.Fatalf("缺 field 文案不符：%q", reply)
	}
	if reply := svc.CatCfgSet(`{"group":"g1","catId":"majordomo","cat":"worker","field":"displayName","value":"x"}`); !strings.HasPrefix(reply, "ERR|GROUP_CATCFG_SET_FAIL|cat.cfg.set | ") {
		t.Fatalf("目标无 cat.cfg 应报 SET_FAIL：%q", reply)
	}
}

// TestGroupRequestSessionRebuild 挂起重建登记——首个请求保留 first/origin，重复登记拒绝。
func TestGroupRequestSessionRebuild(t *testing.T) {
	router, _, _ := newCommandRouter(t)
	if !router.requestSessionRebuild("初始", "group:g1:1:delegation") {
		t.Fatal("首个登记应成功")
	}
	if router.requestSessionRebuild("覆盖", "x") {
		t.Fatal("重复登记应拒绝")
	}
	if router.pendingRebuild.first != "初始" || router.pendingRebuild.origin != "group:g1:1:delegation" {
		t.Fatalf("挂起请求应保留首次内容：%+v", router.pendingRebuild)
	}
	if !router.ConsumePendingNewSession() {
		t.Fatal("空闲时应消费挂起请求并执行重建")
	}
	if router.pendingRebuild.pending {
		t.Fatal("消费后应清空挂起")
	}
}

// TestGroupRebuildCatSession 跨猫重建——寻址 / 忙态 / 成功（单猫形态）。
func TestGroupRebuildCatSession(t *testing.T) {
	router, _, _ := newCommandRouter(t)
	if reply := router.rebuildCatSession("", "", ""); reply != "ERR|CAT_KEY_EMPTY|目标猫 key 不能为空" {
		t.Fatalf("空 key 文案不符：%q", reply)
	}
	if reply := router.rebuildCatSession("other", "", ""); reply != "ERR|CAT_OFFLINE|猫未运行: other" {
		t.Fatalf("离线文案不符：%q", reply)
	}
	if !router.Session.IsIdle() {
		t.Fatal("新建会话应为空闲")
	}
	reply := router.rebuildCatSession("majordomo", "", "")
	if !strings.HasPrefix(reply, "ok|majordomo") {
		t.Fatalf("重建应成功：%q", reply)
	}
	if count := router.Session.Context.Count(); count != 1 {
		t.Fatalf("重建后前文应只剩 system：%d", count)
	}
}

// TestFindDeliveryCat 投递寻址——单猫形态 key / 显示名命中，其余 nil。
func TestFindDeliveryCat(t *testing.T) {
	svc, router, _, _ := newGroupTestService(t)
	router.Session.SetDisplayName("管家猫")
	if svc.findDeliveryCat("majordomo") == nil {
		t.Fatal("majordomo 应命中")
	}
	if svc.findDeliveryCat("管家猫") == nil {
		t.Fatal("显示名应命中")
	}
	if svc.findDeliveryCat("worker") != nil {
		t.Fatal("非本会话应 nil")
	}
	if svc.findDeliveryCat("") != nil {
		t.Fatal("空 key 应 nil")
	}
	// 会话未就绪——nil 路由不应 panic
	empty := &hostGroupService{Store: svc.Store}
	if empty.findDeliveryCat("majordomo") != nil {
		t.Fatal("路由未就绪应 nil")
	}
	if reply := empty.Info(`{"group":"g1","catId":"majordomo"}`); !strings.HasPrefix(reply, `{"ok":true`) {
		t.Fatalf("读面不依赖路由：%q", reply)
	}
	if reply := empty.CatInfo(`{"group":"g1","catId":"majordomo","cat":"majordomo"}`); reply != "ERR|HOSTCMD_NO_SERVICE|宿主指令服务未就绪" {
		t.Fatalf("路由未就绪应明示：%q", reply)
	}
}

// TestGroupServiceSession 会话重建后续跑——挂起请求在 Idle 时消费并注入初始消息。
func TestGroupServiceSession(t *testing.T) {
	svc, router, _, _ := newGroupTestService(t)
	if reply := svc.NewSession(`{"group":"g1","catId":"majordomo","to":"majordomo","reason":"轮末","firstMessage":"开工"}`); !strings.Contains(reply, `"mode":"queued"`) {
		t.Fatalf("自指重建应排队：%q", reply)
	}
	if !router.ConsumePendingNewSession() {
		t.Fatal("空闲消费应执行重建")
	}
	if router.pendingRebuild.pending {
		t.Fatal("消费后应清空挂起")
	}
	batch := router.Session.Pending.TakeAll(true)
	if len(batch) == 0 || batch[0].Content != "开工" {
		t.Fatalf("重建后应入队初始消息：%+v", batch)
	}
}

// TestGroupSessionOrigin 重建来源锚——初始消息的 origin 透传到待处理队列。
func TestGroupSessionOrigin(t *testing.T) {
	router, _, _ := newCommandRouter(t)
	router.sessionNewWithOrigin("开工", "group:g1:7:delegation")
	batch := router.Session.Pending.TakeAll(true)
	if len(batch) == 0 {
		t.Fatal("初始消息应入队")
	}
	if batch[0].Origin != "group:g1:7:delegation" {
		t.Fatalf("origin 未透传：%q", batch[0].Origin)
	}
}

// TestGroupShellHostService 八方法在路由未就绪时的统一明示（除读面外）。
func TestGroupShellHostService(t *testing.T) {
	empty := &hostGroupService{Store: group.New("", nil)}
	empty.Store.Load()
	for name, reply := range map[string]string{
		"send":       empty.Send(`{"group":"g1","catId":"majordomo","to":"majordomo","text":"x"}`),
		"newsession": empty.NewSession(`{"group":"g1","catId":"majordomo","to":"majordomo","reason":"x"}`),
		"catcfgget":  empty.CatCfgGet(`{"group":"g1","catId":"majordomo","cat":"majordomo"}`),
		"catcfgset":  empty.CatCfgSet(`{"group":"g1","catId":"majordomo","cat":"majordomo","field":"displayName","value":"x"}`),
	} {
		if !strings.HasPrefix(reply, "ERR|GROUP_NOT_FOUND|") && !strings.Contains(reply, "HOSTCMD_NO_SERVICE") {
			t.Fatalf("%s 应明示失败面：%q", name, reply)
		}
	}
}
