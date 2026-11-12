// Package session 复现宿主会话层的块契约：视图块、视图卡载荷、视图总线（chat 快照协议 v2）。
//
// 对齐 C# 版 CatHome4.Core 的 ViewBlock / ViewCardPayload / ViewBus / ChatSession 系统注入面：
//   - 视图块 5 字段（ts / type / payload / msgIndex / round）——协议条目无同步标识面
//   - 推送侧排序（时间戳升序），前端零排序
//   - 三段结构 state / persist / live；live 是状态投影（整段覆盖，不是事件流）
package session

// 渲染类型常量（12 种——持久族 9 + 临时族 3）。
const (
	// TypeUser 人工消息块（持久族）
	TypeUser = "user"
	// TypeSystemAutoMessage 系统注入块（持久族——与 user 平级；A219）
	TypeSystemAutoMessage = "systemautomessage"
	// TypeText assistant 纯文本回复（持久族）
	TypeText = "text"
	// TypeGapText 间隙文本（持久族）
	TypeGapText = "gap_text"
	// TypeReason 思考块（持久族）
	TypeReason = "reason"
	// TypeToolCard 工具卡（持久族）
	TypeToolCard = "toolcard"
	// TypeRetry 重试块（持久族）
	TypeRetry = "retry"
	// TypeError 错误块（持久族）
	TypeError = "error"
	// TypeInjectReport 注入报告（持久族）
	TypeInjectReport = "inject_report"
	// TypeRoundSum 轮次结算（持久族）
	TypeRoundSum = "roundsum"
	// TypeRoundSummary 轮末总结（持久族——LLMSimpleRequest 产出一句话总结；与 roundsum 同容器承载）
	TypeRoundSummary = "roundsummary"
	// TypeThinksse 流式思考（临时族——不进持久表）
	TypeThinksse = "thinksse"
	// TypeReplysse 流式回复（临时族——不进持久表）
	TypeReplysse = "replysse"
	// TypeToolrun 未完成工具（临时族——不进持久表）
	TypeToolrun = "toolrun"
	// TypeEmpty 段级全空占位（不入本表）
	TypeEmpty = "empty"
)

// ViewBlock 是视图层最小单元——并列架构：块在事件发生点一次性定稿。
type ViewBlock struct {
	// Timestamp 创建时间戳（Unix 毫秒；前文派生取消息 CreatedAt，独立块取生成时刻）
	Timestamp int64
	// RenderType 渲染类型（见常量组）
	RenderType string
	// Payload 渲染载荷（JSON 字符串，按类型结构不同）
	Payload string
	// MsgIndex 前文消息索引（-1 = 非前文派生）
	MsgIndex int
	// Round 所属轮次（0 = 无轮，如加载阶段）
	Round int
}

// IsPersistType 判定持久族（落盘）；临时族不进表。
func IsPersistType(renderType string) bool {
	switch renderType {
	case TypeUser, TypeSystemAutoMessage, TypeText, TypeGapText, TypeReason, TypeToolCard, TypeRetry, TypeError, TypeInjectReport, TypeRoundSum, TypeRoundSummary:
		return true
	}
	return false
}

// ItemJSON 序列化持久条目——{type, ts, msgIndex, round, payload}（无同步标识面）。
//
// payload 为空按 null 落（与 C# ItemJson 同口径）。
func (b *ViewBlock) ItemJSON() string {
	payload := b.Payload
	if payload == "" {
		payload = "null"
	}
	return `{"type":` + jsonStr(b.RenderType) +
		`,"ts":` + itoa64(b.Timestamp) +
		`,"msgIndex":` + itoa(b.MsgIndex) +
		`,"round":` + itoa(b.Round) +
		`,"payload":` + payload + `}`
}
