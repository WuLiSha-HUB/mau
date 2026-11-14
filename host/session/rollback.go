package session

import "strconv"

// Rollback 回滚——从指定正式回复节点重新开始（该节点后消息全部丢弃）。
//
// 对齐 C# ChatSession.Rollback（CatHome4.Core/ChatSession.cs:3317）：
// 仅 Idle；msgIndex 指向 assistant 正式回复（Content 非空——工具声明轮天然排除）。
// 执行：截断前文（共用实现）→ 轮级统计快照重置 → 视图层截断 → Note 清空 → 会话重置事件 → 元数据落盘。
// 会话级 token 累计不动（回滚属同会话延续，不归零）。
func (s *Session) Rollback(msgIndex int) string {
	if !s.IsIdle() {
		return "ERR|ROLLBACK_BUSY|会话忙——回滚仅空闲时执行"
	}
	all := s.Context.Messages()
	if msgIndex < 0 || msgIndex >= len(all) {
		return "ERR|ROLLBACK_INDEX|节点索引越界: " + strconv.Itoa(msgIndex)
	}
	target := all[msgIndex]
	if target.Role != RoleAssistant || target.Content == "" {
		return "ERR|ROLLBACK_NODE|节点不是正式回复（仅 assistant 回复可作切点）"
	}
	// [段1] 截断前文到切点——共用实现（含落盘与轮级计数复位）
	s.TruncateMessages(msgIndex + 1)
	// [段2] 轮级统计快照重置——新起点零统计起算（会话级累计不动）
	s.mu.Lock()
	s.roundTokens = SessionTokens{}
	s.mu.Unlock()
	// [段3] 视图层截断——与真实前文同批（未截断即把已回滚的消息原样重发）
	if s.Views != nil {
		s.Views.TruncateFrom(msgIndex)
	}
	// [段4] Note 任务清空——防旧任务自动拉起新轮
	s.Note.Clear()
	// [段5] 前端通知——会话重置（帧轮广播全量帧：前端持久区清空后按截断后的视图层整体重绘）
	s.PushSessionReset()
	// [段6] 元数据落盘——前文条数 / 前文长度读面同步（重启后仍是截断后的值）
	s.SaveMeta()
	message := "已回滚到节点 " + strconv.Itoa(msgIndex) + "（保留 " + strconv.Itoa(msgIndex+1) + " 条消息）"
	logAt(LogModuleSession, 1, message, LogCategoryChat)
	return "rollback | " + message
}
