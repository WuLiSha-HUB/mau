package session

import "strconv"

// EditMessage 编辑指定前文消息的正文——只改不重发。
//
// 可编辑面：user（含系统注入）/ assistant 正式回复 / tool 结果。
// 不可编辑：system 提示词、assistant 工具声明轮（tool_calls 与回执成对，改声明会让配对失真）。
//
// 执行：前文替换 → 前文重写落盘（append-only 的合法例外）→ 视图载荷同步 →
// 留档对齐 → 元数据刷新 → 全量帧重绘（前端按新载荷整体重绘）。
func (s *Session) EditMessage(msgIndex int, content string) string {
	if !s.IsIdle() {
		return "ERR|EDIT_BUSY|会话忙——编辑仅空闲时执行"
	}
	if content == "" {
		return "ERR|EDIT_EMPTY|内容为空——编辑需要新正文"
	}
	all := s.Context.Messages()
	if msgIndex < 0 || msgIndex >= len(all) {
		return "ERR|EDIT_INDEX|节点索引越界: " + strconv.Itoa(msgIndex)
	}
	target := all[msgIndex]
	if target.Role == RoleSystem {
		return "ERR|EDIT_NODE|系统提示词不可编辑"
	}
	if target.Role == RoleAssistant && target.ToolCallsJSON != "" {
		return "ERR|EDIT_NODE|工具调用声明不可编辑（声明与回执成对）"
	}
	if !s.Context.SetMessageContent(msgIndex, content) {
		return "ERR|EDIT_INDEX|节点索引越界: " + strconv.Itoa(msgIndex)
	}
	// [段1] 前文重写落盘——编辑是 append-only 的合法例外
	if s.Store != nil {
		s.Store.Rewrite(s.Context.Messages())
	}
	// [段2] 视图载荷同步——user / systemautomessage / text 改 text；tool 改 result（按块型分流，不串写）
	kind := "text"
	if target.Role == RoleTool {
		kind = "result"
	}
	changed := 0
	if s.Views != nil {
		changed = s.Views.UpdateMessageContent(msgIndex, kind, content)
	}
	// [段3] 留档对齐——前文是重建唯一源，改完必须重采
	s.captureFullContext()
	// [段4] 元数据刷新 + 全量帧重绘
	s.SaveMeta()
	s.PushSessionReset()
	message := "已编辑节点 " + strconv.Itoa(msgIndex) + "（视图块 " + strconv.Itoa(changed) + "）"
	logAt(LogModuleSession, 1, message, LogCategoryChat)
	return "edit | " + message
}

// EditReasoning 编辑指定 assistant 消息的思考正文——只改 reason 块（不碰正文与工具声明）。
//
// 可编辑面：assistant 且有思考内容的消息。思考内容与正文同索引（同一条消息派生两块），
// 编辑按块型分流：本方法只改 reason 块，EditMessage 只改 text / result 块。
func (s *Session) EditReasoning(msgIndex int, content string) string {
	if !s.IsIdle() {
		return "ERR|EDIT_BUSY|会话忙——编辑仅空闲时执行"
	}
	all := s.Context.Messages()
	if msgIndex < 0 || msgIndex >= len(all) {
		return "ERR|EDIT_INDEX|节点索引越界: " + strconv.Itoa(msgIndex)
	}
	target := all[msgIndex]
	if target.Role != RoleAssistant {
		return "ERR|EDIT_NODE|只有助手消息的思考可编辑"
	}
	if target.ReasoningContent == "" {
		return "ERR|EDIT_NODE|该消息没有思考内容"
	}
	if !s.Context.SetMessageReasoning(msgIndex, content) {
		return "ERR|EDIT_INDEX|节点索引越界: " + strconv.Itoa(msgIndex)
	}
	// [段1] 前文重写落盘——编辑是 append-only 的合法例外
	if s.Store != nil {
		s.Store.Rewrite(s.Context.Messages())
	}
	// [段2] 视图载荷同步——只改 reason 块（同索引的 text 块不动）
	changed := 0
	if s.Views != nil {
		changed = s.Views.UpdateMessageContent(msgIndex, "reason", content)
	}
	// [段3] 留档对齐 + 元数据刷新 + 全量帧重绘
	s.captureFullContext()
	s.SaveMeta()
	s.PushSessionReset()
	message := "已编辑思考节点 " + strconv.Itoa(msgIndex) + "（视图块 " + strconv.Itoa(changed) + "）"
	logAt(LogModuleSession, 1, message, LogCategoryChat)
	return "editreason | " + message
}

// EditMessageAndRewind 编辑指定消息并回滚到该条之后——保留该条（改后内容），丢弃其后全部消息。
//
// 与 EditMessage 的区别只在收尾：改完内容即把前文 / 视图截断到该条，让会话从这条改好的内容继续
// （等价于「改完再回滚到本条」）。视图载荷先同步再截断——保留块拿到新内容，丢弃块随截断消失。
func (s *Session) EditMessageAndRewind(msgIndex int, content string) string {
	if !s.IsIdle() {
		return "ERR|EDIT_BUSY|会话忙——编辑仅空闲时执行"
	}
	if content == "" {
		return "ERR|EDIT_EMPTY|内容为空——编辑需要新正文"
	}
	all := s.Context.Messages()
	if msgIndex < 0 || msgIndex >= len(all) {
		return "ERR|EDIT_INDEX|节点索引越界: " + strconv.Itoa(msgIndex)
	}
	target := all[msgIndex]
	if target.Role == RoleSystem {
		return "ERR|EDIT_NODE|系统提示词不可编辑"
	}
	if target.Role == RoleAssistant && target.ToolCallsJSON != "" {
		return "ERR|EDIT_NODE|工具调用声明不可编辑（声明与回执成对）"
	}
	if !s.Context.SetMessageContent(msgIndex, content) {
		return "ERR|EDIT_INDEX|节点索引越界: " + strconv.Itoa(msgIndex)
	}
	kind := "text"
	if target.Role == RoleTool {
		kind = "result"
	}
	if s.Views != nil {
		s.Views.UpdateMessageContent(msgIndex, kind, content)
	}
	return s.truncateTo(msgIndex+1, msgIndex, 0, "editrewind",
		"已编辑并回滚到节点 "+strconv.Itoa(msgIndex)+"（保留 "+strconv.Itoa(msgIndex+1)+" 条消息）")
}

// EditReasoningAndRewind 编辑思考并回滚到该条之后——保留该条（改后思考），丢弃其后全部消息。
func (s *Session) EditReasoningAndRewind(msgIndex int, content string) string {
	if !s.IsIdle() {
		return "ERR|EDIT_BUSY|会话忙——编辑仅空闲时执行"
	}
	all := s.Context.Messages()
	if msgIndex < 0 || msgIndex >= len(all) {
		return "ERR|EDIT_INDEX|节点索引越界: " + strconv.Itoa(msgIndex)
	}
	target := all[msgIndex]
	if target.Role != RoleAssistant {
		return "ERR|EDIT_NODE|只有助手消息的思考可编辑"
	}
	if target.ReasoningContent == "" {
		return "ERR|EDIT_NODE|该消息没有思考内容"
	}
	if !s.Context.SetMessageReasoning(msgIndex, content) {
		return "ERR|EDIT_INDEX|节点索引越界: " + strconv.Itoa(msgIndex)
	}
	if s.Views != nil {
		s.Views.UpdateMessageContent(msgIndex, "reason", content)
	}
	return s.truncateTo(msgIndex+1, msgIndex, 0, "editreasonrewind",
		"已编辑思考并回滚到节点 "+strconv.Itoa(msgIndex)+"（保留 "+strconv.Itoa(msgIndex+1)+" 条消息）")
}

// ResendMessage 编辑 user 消息并从该点重发——丢弃该条及之后，以新内容重新投递给本轮泵。
//
// 语义与「回滚到该输入之前 + 以新内容重新发送」等价；重发后由新 user 块驱动前端流式切换。
func (s *Session) ResendMessage(msgIndex int, content string) string {
	if !s.IsIdle() {
		return "ERR|EDIT_BUSY|会话忙——编辑重发仅空闲时执行"
	}
	if content == "" {
		return "ERR|EDIT_EMPTY|内容为空——重发需要新正文"
	}
	all := s.Context.Messages()
	if msgIndex < 0 || msgIndex >= len(all) {
		return "ERR|EDIT_INDEX|节点索引越界: " + strconv.Itoa(msgIndex)
	}
	target := all[msgIndex]
	if target.Role != RoleUser {
		return "ERR|EDIT_NODE|只有用户消息可编辑重发"
	}
	if !IsHumanUserMessage(target) {
		return "ERR|EDIT_NODE|系统注入消息不可编辑重发"
	}
	// [段1] 丢弃该条及之后（内容以新文本重新投递）；截断日志按 resend 前缀出声
	s.truncateTo(msgIndex, msgIndex-1, 0, "resend",
		"已撤销该输入及其后（保留 "+strconv.Itoa(msgIndex)+" 条消息）")
	// [段2] 重新投递——Idle 已校验，泵在下一帧拉起本轮
	if !s.PostUser(content, "user", "", "") {
		logAt(LogModuleSession, 2, "重发未受理——待处理队列不可用", LogCategoryChat)
		return "ERR|EDIT_DELIVER|重发未受理（待处理队列不可用）"
	}
	message := "已用新内容重发节点 " + strconv.Itoa(msgIndex) + "（" + strconv.Itoa(len([]rune(content))) + " 字）"
	logAt(LogModuleSession, 1, message, LogCategoryChat)
	return "resend | " + message
}

// captureFullContext 强制采集一次完整前文留档——前文被改写后的重建基线。
func (s *Session) captureFullContext() {
	s.mu.Lock()
	store := s.fullCtx
	s.mu.Unlock()
	if store != nil {
		store.Capture(true)
	}
}
