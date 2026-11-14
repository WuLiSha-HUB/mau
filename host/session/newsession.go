package session

import "strconv"

// 本文件复现 session.new 的重建半边——对齐 mau:CatHome4/CatHome4.Core/ChatBridge.cs 的
// HandleSessionNew（归档半边在 archive.go，注入面构建在 host/inject）。
//
// 顺序即语义：SetToolSpecs → SetDisplayName → SetSystemPrompt → Clear → ResetStats →
// Store.Rewrite → ArchiveLegacy（视图留档，清空前）→ ClearView → SetInjectReport → PushSessionReset。

// SessionNewOptions 是会话重建参数——注入面产物由调用方（宿主装配层）构建后传入。
type SessionNewOptions struct {
	// DisplayName 新显示名（空 = 保持原值；对齐 C# DisplayNameProvider 未接线时保持原值）
	DisplayName string
	// ToolSpecs 工具声明面（M3：session.new 才拉取最新声明面）
	ToolSpecs []ToolSpec
	// SystemPrompt 注入提示词全文（inject.BuildPrompt 的 Prompt）
	SystemPrompt string
	// InjectReportJSON 注入报告 JSON（inject.BuildReportJSON 产物）
	InjectReportJSON string
	// InjectCount 注入清单条数（日志摘要口径——C# 按 injectList.Length 计数）
	InjectCount int
}

// Rebuild 执行会话重建——清前文 / 视图清空 / 统计重置 / 注入报告落视图 / 重置事件。
//
// 返回日志摘要行（与 C#「会话已重建：注入 N 个知识文件，前文已清空」同形）。
// 归档失败不阻断重建（ArchiveLegacy 自身返回空串，调用面按空处理）。
func (s *Session) Rebuild(opts SessionNewOptions) string {
	// M3 新会话生效——注入面同步最新声明面
	s.SetToolSpecs(opts.ToolSpecs)
	// A201 显示名按 cat.cfg 现值刷新
	if opts.DisplayName != "" {
		s.SetDisplayName(opts.DisplayName)
	}
	// 完整前文定稿——当前份转历史 + 份数轮转（清前文之前：留档保留完整旧前文）
	s.FinalizeFullContext()
	// 注入提示词 → 系统提示词；清前文（system 保留）
	s.Context.SetSystemPrompt(opts.SystemPrompt)
	s.Context.Clear()
	// E3 真实 usage 统计——新会话零统计起算
	s.ResetStats()
	// 前文落盘重写（空前文 = 只留 system）
	if s.Store != nil {
		s.Store.Rewrite(s.Context.Messages())
	}
	// A87 旧会话留档——清空视图前导出（user / 正式回复 / 加载报告 / 每轮结算）
	if s.Views != nil {
		s.Views.ArchiveLegacy(s.CatKey, s.DisplayNameSnapshot())
	}
	// F4 视图——session.new 清前文 → 视图随生命周期清空
	s.ClearView()
	// 注入报告——逐文件结果持久化进视图（普通块：ts=0 居首）
	s.SetInjectReport(opts.InjectReportJSON)
	// 问题一修复——会话重置显式事件（前端收到后清空气泡再拉 history）
	s.PushSessionReset()
	return "会话已重建：注入 " + strconv.Itoa(opts.InjectCount) + " 个知识文件，前文已清空"
}

// DisplayNameSnapshot 显示名快照（并发安全读）。
func (s *Session) DisplayNameSnapshot() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.DisplayName
}

// ClearView 清空视图层——session.new 清前文时同步（视图文件重写为空）。
func (s *Session) ClearView() {
	if s.Views != nil {
		s.Views.Clear()
	}
}

// SetInjectReport 注入报告落视图——覆写语义（同型旧块先移除；空串 = 清除）。
func (s *Session) SetInjectReport(payloadJSON string) {
	if s.Views != nil {
		s.Views.SetInjectReport(payloadJSON)
	}
}

// PushSessionReset 会话重置显式事件——视图两区全清 + 置全量待发标记。
//
// 推送面未接（ViewBus 未 Attach）时只记待发标记，不外发——语义与 C# ViewBus.ResetAll 一致。
func (s *Session) PushSessionReset() {
	if s.ViewBus != nil {
		s.ViewBus.ResetAll()
	}
}
