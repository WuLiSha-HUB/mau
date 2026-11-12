package http

import "time"

// SnapshotInterval 是快照重建周期（对齐 C# PumpLoop 的 SnapshotIntervalMs = 250ms）。
const SnapshotInterval = 250 * time.Millisecond

// Pump 周期重建快照缓存并推送增量帧，直到 Shutdown。
//
// 单 goroutine 消费——快照构建器与 patch 缓存不需要并发保护（对齐 C# 主线程构建语义）。
func (s *Server) Pump() {
	for {
		if s.waitStop(SnapshotInterval) {
			return
		}
		s.tick()
	}
}

// tick 重建快照缓存并推送增量帧（log / state / patch / view）。
//
// 推送面口径对齐 C# PumpMainThread + PushLogIncrements：
//   - log —— 日志增量批量合帧（数组载荷；trace 类由取数面过滤）
//   - state —— cats 变化即推（管理面板猫表格实时跟随；无变化零字节）
//   - patch —— 六段比对后的增量（无变化零推送——Idle 稳态静默）
//   - view —— 会话视图增量帧（state / persist append / live 三段可同帧；无变化零字节）
//
// 全量快照只更新缓存（GET /api/v1/snapshot 与 SSE 首帧补发取用），不逐帧广播。
func (s *Server) tick() {
	if s.tickHook != nil {
		s.tickHook()
	}
	if s.snapshotFn == nil {
		return
	}
	s.snapshotCache.Store(s.snapshotFn(false))
	s.pushLogIncrements()
	s.pushState()
	s.pushPatch()
	s.pushView()
}

// pushLogIncrements 推送日志增量帧（一轮的多条合帧为一个 log 事件，载荷为数组）。
func (s *Server) pushLogIncrements() {
	if s.logIncrementFn == nil {
		return
	}
	data, end := s.logIncrementFn(s.logCursor)
	s.logCursor = end
	if data != "" {
		s.Broadcast("log", data)
	}
}

// pushState 推送猫卡状态帧——cats 变化即推（无变化零字节）。
func (s *Server) pushState() {
	if s.catsBuilder == nil {
		return
	}
	cats := s.catsBuilder()
	if cats == "" {
		return
	}
	prev, _ := s.stateCache.Load().(string)
	if cats == prev {
		return
	}
	s.stateCache.Store(cats)
	s.Broadcast("state", cats)
}

// pushPatch 推送增量 patch 帧（无变化零推送）。
func (s *Server) pushPatch() {
	if s.patchFn == nil {
		return
	}
	if patch := s.patchFn(); patch != "" {
		s.Broadcast("patch", patch)
	}
}

// pushView 推送视图增量帧（state / persist / live 三段可同帧；无变化零字节）。
func (s *Server) pushView() {
	if s.viewFrameFn == nil {
		return
	}
	if frame := s.viewFrameFn(); frame != "" {
		s.Broadcast("view", frame)
	}
}
