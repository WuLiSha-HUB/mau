package session

import "strings"

// 本文件是宿主重启链的会话侧契约（对齐 C# ChatSession.RestartRequest / IsHostRestarting / IsIdle）：
//
//	T1 请求登记——工具批结果收集完成 → 重启族工具且回执非 ERR → 登记请求 + 置停机态（本轮照常收尾）；
//	T2 停机态——新输入 / 系统注入 / 继续请求拒收（PendingQueue 的 isHostRestarting 判定消费）；
//	T3 闸门——宿主主循环据 IsIdle（本文件）+ Flow 空闲 + OA 零在途 判全局 Idle 后执行接力。
//
// 装配层注入两个钩子（进程级单例——单猫形态与 HostRestarting 同模式）；未注入 = 重启链不启用。

// RestartRequestPayload 读取重启请求载荷（装配层注入——DataBox 全局盒 host_restart_request）。
//
// 返回 JSON 文本（{"mode":…,"target":…,"push":…}）；空 = 无请求。
var RestartRequestPayload = func() string { return "" }

// RestartRequestRegister 登记重启请求（装配层注入——置停机态；参数为请求载荷 JSON）。
var RestartRequestRegister = func(requestJSON string) {}

// registerRestartFromBatch 重启族工具成功回执 → 登记重启请求（T1 检测单点）。
//
// 判据：工具名在重启族 + 回执非空且非 ERR| 前缀；命中即置本轮「工具主动 done」并交登记钩子（置停机态）。
func (s *Session) registerRestartFromBatch(dogs []*toolDog) {
	for _, dog := range dogs {
		if dog == nil || !IsRestartTool(dog.Name) {
			continue
		}
		if dog.Result == "" || strings.HasPrefix(dog.Result, "ERR|") {
			continue
		}
		payload := RestartRequestPayload()
		if payload == "" {
			payload = "{}"
		}
		r := s.roundState()
		r.mu.Lock()
		r.toolDone = true
		r.mu.Unlock()
		RestartRequestRegister(payload)
	}
}

// IsIdle 会话空闲判定——无在途轮次且无待处理条目（宿主重启闸门 T3 的会话面判据）。
//
// 判据与 C# ChatSession.IsIdle 同义：粗粒度相位 = Idle 且两队列均为空；
// 在途轮次由停机态自然收尾——闸门等的是在途，不是等人。
func (s *Session) IsIdle() bool {
	r := s.roundState()
	r.mu.Lock()
	phase := r.phase
	r.mu.Unlock()
	if phase != chatPhaseIdle {
		return false
	}
	return s.pendingCount() == 0
}
