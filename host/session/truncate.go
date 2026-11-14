package session

// 本文件是前文截断的唯一共用实现——人工回滚（session.rollback）与后续共用路径从这里走。
//
// 对齐 C# ChatSession.TruncateMessages（CatHome4.Core/ChatSession.cs:3280）：
// 截断上下文 → 前文落盘重写（append-only 的合法例外）→ 轮级计数复位。
// 不含：会话级统计归零 / Note 处置 / 视图处置 / 前端通知——各路径语义不同，由调用方分头保留。

// TruncateMessages 前文截断到指定条数（保留 [0..keepCount-1]；越界夹取）。
func (s *Session) TruncateMessages(keepCount int) {
	all := s.Context.Messages()
	if keepCount < 0 {
		keepCount = 0
	}
	if keepCount > len(all) {
		keepCount = len(all)
	}
	keep := make([]Message, keepCount)
	copy(keep, all[:keepCount])
	// [段1] 截断上下文——ReplaceMessages 走结构修复面（幂等）
	s.Context.ReplaceMessages(keep)
	// [段2] 前文落盘重写——截断是 append-only 的合法例外（保存结构修复后的前文）
	if s.Store != nil {
		s.Store.Rewrite(s.Context.Messages())
	}
	// [段3] 轮级计数复位——新起点零统计起算（会话级累计不动：回滚属同会话延续）
	r := s.roundState()
	r.mu.Lock()
	r.usagePrompt = 0
	r.usageCompletion = 0
	r.usageCacheHit = 0
	r.toolCallCount = 0
	r.mu.Unlock()
	s.mu.Lock()
	s.contextTokens = 0
	s.persistedContextTokens = 0
	s.mu.Unlock()
}
