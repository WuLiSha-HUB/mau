package bricks

import (
	"encoding/json"
	"strings"

	"cathome5/mau/runtime"
)

// HOST 三件积木——Majordomo 工具组（宿主自更新登记 / 管理指令执行 / 全猫状态统计）。
//
// 对齐 C# BRIK-HOST-001..003：
//   - host.restart——参数零容忍 + 模式值域 + 运行模式闸门 + 请求落 DataBox 全局盒（只登记不执行）；
//   - host.command——一行管理指令交宿主统一执行出口（cat.* / catcfg.*），回执原文返回；
//   - host.catinfo——内核指令 cat.info 回执原样返回（整块分类 JSON，无「头 + 正文」两段）。
//
// 职责铁律：积木不碰进程、不碰文件系统、不碰宿主生命周期——进程动作全部由宿主闸门与部署继电器承担。
// 权限面由会话工具面承担（工具定义积木组级 privileged 声明）——本组积木不做权限判定、不判调用者身份。

// hostRestartRequestKey 重启请求载荷盒键（与宿主侧闸门同源）。
const hostRestartRequestKey = "host_restart_request"

// hostRunModeKey 运行模式标记盒键（"interactive" = 常驻主循环；缺省 / 其它 = 无闸门可等）。
const hostRunModeKey = "host_run_mode"

// HostRestart 登记宿主重启请求——参数校验 → 模式值域 → 运行模式闸门 → 落 DataBox 全局盒。
//
// mode 由工具名承载（语料线常量入参：full / incr / host），不进参数面。
// 返回 true = 请求已登记（result 为 A214 头 + 摘要行 + 语义正文）。
func HostRestart(db *runtime.DataBox, argsJSON, mode string) (string, bool) {
	if mode != "full" && mode != "incr" && mode != "host" {
		return "ERR|BAD_ARGS|重启模式非法: " + mode + "（支持 full / incr / host）", false
	}
	if badArgs := Validate(argsJSON, "target push scope", "", "", ""); badArgs != "" {
		return badArgs, false
	}
	if db == nil {
		return "ERR|HOSTCMD_NO_SERVICE|宿主数据中台未注入（宿主未接线）", false
	}
	// 运行模式闸门——非常驻交互模式无常驻主循环可等全局 Idle 闸门（对齐 C# host_run_mode 盒）
	if runMode := db.GetString("global", hostRunModeKey); runMode != "interactive" {
		return "ERR|RESTART_UNSUPPORTED|当前运行模式无常驻主循环可等闸门——仅常驻交互模式支持宿主自更新", false
	}
	target := Get(argsJSON, "target")
	push := Get(argsJSON, "push")
	// 闸门判据范围（Go 侧出口）——空 / all = 全局 Idle（既有语义）；self = 只等发起会话自身。
	// 存在理由：Note 引擎（未完成计划强制拉起）与「所有会话同时 Idle」互斥，闸门在 Note 活跃猫存在时永不通过
	// （判否现场 `ccbp:L3/Mau/F-推理现场/20261114_restart-gate-global-idle/`）。self 只越过「别的猫在途轮」。
	scope := strings.TrimSpace(Get(argsJSON, "scope"))
	if scope != "" && scope != "all" && scope != "self" {
		return "ERR|BAD_ARGS|scope 非法: " + scope + "（只接受 all / self；空 = all）", false
	}
	// 归一载荷——只落声明面字段（catId 为宿主注入保留键，不入请求）
	payload := `{"mode":` + jsonString(mode) + `,"target":` + jsonString(target) +
		`,"push":` + jsonString(push) + `,"scope":` + jsonString(scope) + `}`
	db.Set("global", hostRestartRequestKey, payload)
	targetShow := target
	if targetShow == "" {
		targetShow = "受控根 mauout5"
	}
	gated := "全局空闲"
	if scope == "self" {
		gated = "本会话空闲（scope=self）"
	}
	body := ""
	switch mode {
	case "incr":
		body = "宿主增量重启请求已登记（纯搬运产物区 + 重启，不编译不测试；目标运行区: " + targetShow +
			"）。宿主在" + gated + "后接力搬运并重启，结果回执将自动注入本会话。"
	case "host":
		body = "宿主原地重启请求已登记（不编译、不覆盖；目标运行区: " + targetShow +
			"）。宿主在" + gated + "后借接力者原地重启，结果回执将自动注入本会话。"
	default:
		body = "宿主全链重启请求已登记（prepare + 原子切换 + 重启；目标运行区: " + targetShow +
			"）。宿主在" + gated + "后接力部署并重启，结果回执将自动注入本会话。"
	}
	if scope == "self" {
		body += "（scope=self：不等待其它猫在途轮——被打断的在途轮由新宿主 pendingRound 续跑接回）"
	}
	head := `{"ok":true,"tool":` + jsonString(restartToolName(mode)) +
		`,"mode":` + jsonString(mode) +
		`,"target":` + jsonString(targetShow) +
		`,"push":` + boolText(push != "") +
		`,"scope":` + jsonString(scope) + `}`
	return head + "\n" + targetShow + " | " + mode + " 重启请求已登记" + "\n" + body, true
}

// restartToolName 模式 → 工具名（A214 头 tool 字段）。
func restartToolName(mode string) string {
	switch mode {
	case "incr":
		return "restart-incr"
	case "host":
		return "restart-host"
	}
	return "restart-full"
}

// HostCommand 执行一行宿主管理指令——经宿主统一执行出口（与 CLI / HTTP 面板同源单内核）。
//
// 参数面：cmd（一行管理指令，必填）；未知参数 / 缺值一律 ERR|BAD_ARGS（入口面零容忍）。
// 宿主回执 ERR| 前缀原样返回（不包装头）；成功回 A214 头 + 摘要行 + 回执原文。
func HostCommand(execute func(string) string, argsJSON string) (string, bool) {
	if badArgs := Validate(argsJSON, "cmd", "", "", ""); badArgs != "" {
		return badArgs, false
	}
	cmd := strings.TrimSpace(Get(argsJSON, "cmd"))
	if cmd == "" {
		return "ERR|BAD_ARGS|缺少参数 cmd（一行管理指令，如 cat.list）", false
	}
	if execute == nil {
		return "ERR|HOSTCMD_NO_SERVICE|宿主指令服务未注入（宿主未接线）", false
	}
	reply := execute(cmd)
	if reply == "" {
		reply = "ERR|EMPTY_RESULT|宿主指令无回执"
	}
	if strings.HasPrefix(reply, "ERR|") {
		return reply, false
	}
	return MetaHead("majordomo-cmd", true, "", -1) + "\n" + "宿主指令已执行" + "\n" + reply, true
}

// HostCatInfo 取全猫状态统计——经宿主指令服务执行 cat.info，回执整块 JSON 原样返回。
//
// 参数面：零参数（catId 为宿主注入保留键放行）；未知参数 / 非对象形态一律 ERR|BAD_ARGS。
func HostCatInfo(execute func(string) string, argsJSON string) (string, bool) {
	if strings.TrimSpace(argsJSON) != "" {
		if badArgs := Validate(argsJSON, "", "", "", ""); badArgs != "" {
			return badArgs, false
		}
	}
	if execute == nil {
		return "ERR|HOSTCMD_NO_SERVICE|宿主指令服务未注入（宿主未接线）", false
	}
	reply := execute("cat.info")
	if reply == "" {
		reply = "ERR|EMPTY_RESULT|宿主指令无回执"
	}
	return reply, !strings.HasPrefix(reply, "ERR|")
}

// SetInteractiveRunMode 标记本进程为常驻交互模式——host.restart 的运行模式闸门数据源。
//
// 装配层在机制面构造时调用一次；无常驻主循环的宿主（未来非交互模式）不应调用。
func SetInteractiveRunMode(db *runtime.DataBox) {
	if db == nil {
		return
	}
	db.Set("global", hostRunModeKey, "interactive")
}

// DecodeRestartRequest 解析重启请求载荷——mode / target / push / scope 四字段（缺省空串）。
//
// 解析失败按空请求处理（调用方按 mode 空 = full 兜底——与 C# ReadJsonField 防御式同义）。
// scope 缺省（空）= 全局 Idle；"self" = 只等发起会话自身（Go 侧出口，C# 无此字段）。
func DecodeRestartRequest(payload string) (mode, target, push, scope string) {
	if strings.TrimSpace(payload) == "" {
		return "", "", "", ""
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &fields); err != nil {
		return "", "", "", ""
	}
	read := func(key string) string {
		raw, ok := fields[key]
		if !ok {
			return ""
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return ""
		}
		return value
	}
	return read("mode"), read("target"), read("push"), read("scope")
}
