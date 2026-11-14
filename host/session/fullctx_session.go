package session

import "time"

// contextPatrolInterval 前文巡检节流窗口——生产 60 秒（测试注入 0 = 每帧巡检）。
const contextPatrolInterval = 60 * time.Second

// 完整前文留档的会话接线——挂载 / 采集 / 定稿 / 视图委托（design-ch4-fullctx）。
//
// 留档本体在 fullctx.go；本文件只管「会话什么时候触发它」：
// 消息入前文（Context 追加回调，30 秒节流）· 轮末（强制）· 会话定稿（清前文前）· 端点读取（强制）。

// SetFullContext 挂载完整前文留档（nil = 不启用——视图回落内存前文）。
//
// 挂载即接上 Context 追加回调：消息入前文触发采集（节流窗口内跳过，由轮末 / 定稿 / 端点读取兜底）。
func (s *Session) SetFullContext(store *FullContextStore) {
	s.mu.Lock()
	s.fullCtx = store
	s.mu.Unlock()
	if s.Context == nil {
		return
	}
	if store == nil {
		s.Context.SetOnAppend(nil)
		return
	}
	s.Context.SetOnAppend(func() { store.Capture(false) })
}

// CaptureFullContext 采集一次（force=true 忽略 30 秒节流窗口——轮末 / 定稿 / 端点读取用）。
func (s *Session) CaptureFullContext(force bool) {
	s.mu.Lock()
	store := s.fullCtx
	s.mu.Unlock()
	if store != nil {
		store.Capture(force)
	}
}

// FinalizeFullContext 会话定稿——当前份转历史 + 份数轮转（session.new 清前文之前调用）。
//
// 返回定稿文件绝对路径（空串 = 本会话无落盘 / 未挂载）。
func (s *Session) FinalizeFullContext() string {
	s.mu.Lock()
	store := s.fullCtx
	s.mu.Unlock()
	if store == nil {
		return ""
	}
	return store.Finalize()
}

// ContextCharsPerToken 本会话实测字符/token 比值（最近一次请求反推；0 = 无实测）。
//
// 消费面：留档头 token 折算 + GET /api/v1/fullctx 的 tokens 字段（与 C# ContextCharsPerToken 同源）。
func (s *Session) ContextCharsPerToken() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctxCharsPerToken
}
