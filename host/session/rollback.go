package session

import "strconv"

// Rollback 回滚——以指定前文节点为切点截断（切点之后的视图与前文一并丢弃）。
//
// 切点语义按角色（Go 侧扩展：C# 基准只允许 assistant 正式回复）：
//   - assistant 正式回复（Content 非空）→ 保留该条：从这条回复继续对话
//   - assistant 工具声明轮（Content 空 + tool_calls）→ 丢弃该条：保留会留下无回执的声明
//   - tool 结果 → 保留该条：与声明成对，同批缺失回执由结构修复补占位
//   - user 消息（含系统注入）→ 丢弃该条：等价于「这条输入还没发出去」
//   - system 提示词 → 拒绝（前文首条必须保留）
//
// 执行：截断前文（共用实现）→ 轮级统计快照重置 → 视图层同批截断 → Note 清空 →
// 会话重置事件 → 元数据落盘。会话级 token 累计不动（回滚属同会话延续，不归零）。
func (s *Session) Rollback(msgIndex int) string {
	if !s.IsIdle() {
		return "ERR|ROLLBACK_BUSY|会话忙——回滚仅空闲时执行"
	}
	all := s.Context.Messages()
	if msgIndex < 0 || msgIndex >= len(all) {
		return "ERR|ROLLBACK_INDEX|节点索引越界: " + strconv.Itoa(msgIndex)
	}
	target := all[msgIndex]
	keep := msgIndex + 1
	note := "已回滚到节点 " + strconv.Itoa(msgIndex) + "（保留 " + strconv.Itoa(keep) + " 条消息）"
	switch {
	case target.Role == RoleSystem:
		return "ERR|ROLLBACK_NODE|系统提示词不可作切点"
	case target.Role == RoleUser:
		keep = msgIndex
		note = "已撤销该输入及其后（保留 " + strconv.Itoa(keep) + " 条消息）"
	case target.Role == RoleAssistant && target.Content == "" && target.ToolCallsJSON != "":
		keep = msgIndex
		note = "工具声明轮不可保留——已回退到其之前（保留 " + strconv.Itoa(keep) + " 条消息）"
	}
	return s.truncateTo(keep, keep-1, 0, "rollback", note)
}

// RollbackAt 回滚到指定视图块之前——非前文派生块（轮结算 / 错误 / 重试 / 间隙文本）的切点。
//
// 语义：丢弃该块及之后的所有视图块；前文保留 CreatedAt 严格早于该块时间戳的消息前缀
// （前文派生块的 ts 取消息 CreatedAt，同刻按已出现处理——回滚点即该块出现之前）。
func (s *Session) RollbackAt(blockTs int64) string {
	if !s.IsIdle() {
		return "ERR|ROLLBACK_BUSY|会话忙——回滚仅空闲时执行"
	}
	if blockTs <= 0 {
		return "ERR|ROLLBACK_INDEX|时间戳非法: " + strconv.FormatInt(blockTs, 10)
	}
	keep := s.Context.CountBefore(blockTs)
	return s.truncateTo(keep, -1, blockTs, "rollbackat",
		"已回退到该块之前（保留 "+strconv.Itoa(keep)+" 条消息）")
}

// truncateTo 截断执行体——回滚与按时间戳回滚共用。
//
// keep = 保留的前文条数；cutIndex = 视图层按索引截断的切点；cutTs > 0 时视图按时间戳截断。
func (s *Session) truncateTo(keep, cutIndex int, cutTs int64, tag, note string) string {
	all := s.Context.Messages()
	if keep < 0 {
		keep = 0
	}
	if keep > len(all) {
		keep = len(all)
	}
	// 保底：前文首条为 system 时必须保留（否则系统提示词缓存与前文一起丢）
	if keep == 0 && len(all) > 0 && all[0].Role == RoleSystem {
		keep = 1
		note += " · 系统提示词已保留"
	}
	// [段1] 截断前文到切点——共用实现（含落盘与轮级计数复位）
	s.TruncateMessages(keep)
	// [段2] 轮级统计快照重置——新起点零统计起算（会话级累计不动）
	s.mu.Lock()
	s.roundTokens = SessionTokens{}
	s.mu.Unlock()
	// [段3] 视图层截断——与真实前文同批（未截断即把已回滚的消息原样重发）
	if s.Views != nil {
		if cutTs > 0 {
			s.Views.TruncateAt(cutTs)
		} else {
			s.Views.TruncateFrom(cutIndex)
		}
	}
	// [段4] Note 任务清空——防旧任务自动拉起新轮
	s.Note.Clear()
	// [段5] 前端通知——会话重置（帧轮广播全量帧：前端持久区清空后按截断后的视图层整体重绘）
	s.PushSessionReset()
	// [段6] 元数据落盘——前文条数 / 前文长度读面同步（重启后仍是截断后的值）
	s.SaveMeta()
	logAt(LogModuleSession, 1, note, LogCategoryChat)
	return tag + " | " + note
}
