package llm

import (
	"context"

	"cathome5/host/session"
)

// SessionStreamer 把 Client 适配为会话层流式执行面（事件类型数值同序，零成本转换）。
type SessionStreamer struct {
	// Client 底层客户端
	Client *Client
}

// Stream 实现 session.Streamer。
func (s *SessionStreamer) Stream(ctx context.Context, messages []session.Message, tools []session.ToolSpec, userID string, emit func(session.StreamEvent)) {
	if s == nil || s.Client == nil {
		emit(session.StreamEvent{Kind: session.StreamError, Text: "ERR|LLM_NOT_WIRED|LLM 客户端未接线"})
		return
	}
	s.Client.Stream(ctx, messages, tools, userID, func(ev Event) {
		emit(session.StreamEvent{Kind: session.StreamKind(ev.Kind), Text: ev.Text})
	})
}

// 编译期断言——事件类别数值与 llm 侧同序（跨包转换依赖该不变量）。
var _ session.Streamer = (*SessionStreamer)(nil)
