package bricks

import "strings"

// GROUP 八件积木——GroupCat 工具执行面（对齐 C# Bricks/GROUP/BRIK-GROUP-001..008）。
//
// 语义：薄壳只做「服务未注入报 GROUP_NO_SERVICE → 空回执报 EMPTY_RESULT → 交服务层」；
// 权限（组成员 / 组管理员）与参数校验归宿主实现（IGroupService），Runtime 不认识。
// 工具名映射（2026-11-05 莎裁 β，积木名为内部实现名不改）：
//
//	group-info=Info · group-inbox=Inbox · group-post=Say · group-delegate=Send
//	group-newsession=NewSession · group-catinfo=CatInfo · group-catcfg-get=CatCfgGet · group-catcfg-set=CatCfgSet
//
// 回执格式（A214 头 / 摘要行 / 正文行，或 ERR|）由服务层按 C# 出口逐字拼装。

// GroupService 群组服务面（对齐 C# Mau.Runtime.IGroupService）——八方法各收工具参数 JSON、回文本。
//
// 宿主注入实现（cmd/cathome5 的 hostGroupService）；nil = 未接线（八件积木一律 GROUP_NO_SERVICE）。
type GroupService interface {
	// Info 组信息——组行（成员 / 管理员 / 统计）。
	Info(argsJSON string) string
	// Inbox 拉取消息——按 after / limit 取组内消息流。
	Inbox(argsJSON string) string
	// Say 发讨论消息（note）——只写组消息流，不投递。
	Say(argsJSON string) string
	// Send 派单（delegation）——写组消息 + 投递目标猫（同一方法内完成）。
	Send(argsJSON string) string
	// NewSession 重建会话——目标须空闲；目标为调用者自己时挂起到轮末。
	NewSession(argsJSON string) string
	// CatInfo 单猫运行态统计——范围限本组成员。
	CatInfo(argsJSON string) string
	// CatCfgGet 每猫配置读取——范围限本组成员。
	CatCfgGet(argsJSON string) string
	// CatCfgSet 每猫配置写入——仅组管理员发起且目标不含调用者自己。
	CatCfgSet(argsJSON string) string
}

// groupShell 统一薄壳——服务寻址 + 空回执保护 + ERR| 判定（C# 八件逐字同构）。
//
// 返回 (回执文本, 是否成功)；成功判定 = 回执非空且不以 ERR| 开头。
func groupShell(svc GroupService, call func(GroupService, string) string, argsJSON string) (string, bool) {
	if svc == nil {
		return "ERR|GROUP_NO_SERVICE|群组服务未注入（宿主未接线）", false
	}
	reply := call(svc, argsJSON)
	if reply == "" {
		return "ERR|EMPTY_RESULT|群组服务无回执", false
	}
	return reply, !strings.HasPrefix(reply, "ERR|")
}

// GroupInfo 执行 group.info——组信息读取（BRIK-GROUP-001）。
func GroupInfo(svc GroupService, argsJSON string) (string, bool) {
	return groupShell(svc, GroupService.Info, argsJSON)
}

// GroupInbox 执行 group.inbox——组消息拉取（BRIK-GROUP-002）。
func GroupInbox(svc GroupService, argsJSON string) (string, bool) {
	return groupShell(svc, GroupService.Inbox, argsJSON)
}

// GroupSay 执行 group.say——发讨论消息（BRIK-GROUP-003）。
func GroupSay(svc GroupService, argsJSON string) (string, bool) {
	return groupShell(svc, GroupService.Say, argsJSON)
}

// GroupSend 执行 group.send——派单并唤醒目标猫（BRIK-GROUP-004）。
func GroupSend(svc GroupService, argsJSON string) (string, bool) {
	return groupShell(svc, GroupService.Send, argsJSON)
}

// GroupNewSession 执行 group.new_session——重建目标猫会话（BRIK-GROUP-005）。
func GroupNewSession(svc GroupService, argsJSON string) (string, bool) {
	return groupShell(svc, GroupService.NewSession, argsJSON)
}

// GroupCatInfo 执行 group.catinfo——单猫运行态统计（BRIK-GROUP-006）。
func GroupCatInfo(svc GroupService, argsJSON string) (string, bool) {
	return groupShell(svc, GroupService.CatInfo, argsJSON)
}

// GroupCatCfgGet 执行 group.catcfg_get——每猫配置读取（BRIK-GROUP-007）。
func GroupCatCfgGet(svc GroupService, argsJSON string) (string, bool) {
	return groupShell(svc, GroupService.CatCfgGet, argsJSON)
}

// GroupCatCfgSet 执行 group.catcfg_set——每猫配置写入（BRIK-GROUP-008）。
func GroupCatCfgSet(svc GroupService, argsJSON string) (string, bool) {
	return groupShell(svc, GroupService.CatCfgSet, argsJSON)
}
