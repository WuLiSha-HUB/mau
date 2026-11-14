package session

import (
	"strconv"
	"time"
)

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

// PatrolFullContext 请求前巡检——前文首段与留档不一致则按留档重建（design-ch4-ctx-rebuild §二）。
//
// 触发面：launchLlm 前（每请求一次，经 ctxPatrolInterval 节流——生产 60 秒，测试注入 0 = 每次都查）。
// 判据：留档头 == 前文头 → 零动作（回滚截掉的是尾部，头不变——不会被误判为丢头，回滚不撤销）；
// 不一致 → 留档剥离重建 → 替换前文 + 落盘重写 + 留档对齐。
// 失败语义：置 ctxRebuildFailed 并在**首次**失败出声（防每请求刷日志），节流窗口内不再重试；任一次成功即复位。
// 返回本次请求应使用的消息序列（未重建 = 入参原样）。
func (s *Session) PatrolFullContext(messages []Message) []Message {
	s.mu.Lock()
	store := s.fullCtx
	last := s.ctxPatrolAt
	interval := s.ctxPatrolInterval
	s.mu.Unlock()
	if store == nil || s.Context == nil {
		return messages
	}
	if now := time.Now(); !last.IsZero() && interval > 0 && now.Sub(last) < interval {
		return messages
	}
	s.mu.Lock()
	s.ctxPatrolAt = time.Now()
	s.mu.Unlock()
	if store.HeadMatches(messages) {
		s.mu.Lock()
		s.ctxRebuildFailed = false
		s.mu.Unlock()
		return messages
	}
	rebuilt, err := store.TryRebuild(messages)
	if err != nil || len(rebuilt) == 0 {
		s.mu.Lock()
		alreadyFailed := s.ctxRebuildFailed
		s.ctxRebuildFailed = true
		s.mu.Unlock()
		if !alreadyFailed {
			detail := "前文巡检：留档重建失败，本轮沿用现有前文"
			if err != nil {
				detail += "（" + err.Error() + "）"
			}
			logAt(fullCtxModule, 2, detail, LogCategoryChat)
		}
		return messages
	}
	// [段1] 替换前文——ReplaceMessages 走结构修复面（幂等）
	s.Context.ReplaceMessages(rebuilt)
	// [段2] 前文落盘重写——重建是 append-only 的合法例外（与 TruncateMessages 同口径）
	if s.Store != nil {
		s.Store.Rewrite(s.Context.Messages())
	}
	// [段3] 留档对齐——立即强制采集一次（重建后头部一致，后续增量以新头为锚）
	store.Capture(true)
	s.mu.Lock()
	s.ctxRebuildFailed = false
	s.contextTokens = 0
	s.persistedContextTokens = 0
	s.mu.Unlock()
	logAt(fullCtxModule, 1, "前文巡检：留档头与前文不一致——已按留档重建（"+strconv.Itoa(len(rebuilt))+" 条）", LogCategoryChat)
	return rebuilt
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
