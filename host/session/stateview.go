package session

import (
	"math"
	"strconv"
)

// BuildStateJSON 状态段整段 JSON——与 C# ChatSession.BuildStateJson 逐字段同构（十一项）：
//
//	sessionId / runState / runMs{七键} / requests / note / delay / pending /
//	conn{server,clients} / tokens{十二键} / meta{七键} / pet{kind}
//
// 口径：
//   - 轮级 token——Idle 态回落最近落盘边界快照（roundTokens），其余态取实时累计；
//   - 前文长度——ContextTokensKnown（请求级实时值优先，回落落盘快照）；
//   - delay 段 = 延迟队列按本猫过滤的待触发条目数组（未接线 = 空 entries 并出声一次），不伪造队列内容；
//   - conn.server 取宿主重启停机态（停机中 = stopping），clients 取连接数提供器（未注入 = 0）。
func (s *Session) BuildStateJSON() string {
	name, phases, requests := s.GetRunState()
	roundTokens := s.roundTokenSnapshot()
	sessionTokens := s.sessionTokensValue()
	s.delayNoted.Do(func() {
		if s.Delays == nil {
			logAt(LogModuleSession, 1, "state.delay 段按空 entries 输出——延迟队列未接线（宿主装配缺失）", LogCategoryChat)
		}
	})
	lastActive := s.LastActiveAtKnown()
	chars := computeContextChars(s.Context.Messages())
	count := int64(s.Context.Count())
	s.mu.Lock()
	catID := s.CatKey
	displayName := s.DisplayName
	sessionID := s.sessionID
	createdAt := s.createdAt
	petKind := s.petKind
	delays := s.Delays
	s.mu.Unlock()
	delayJSON := `{"entries":[]}`
	if delays != nil {
		delayJSON = `{"entries":` + delays.BuildEntriesFragment(catID) + `}`
	}
	server := "ok"
	if isHostRestarting() {
		server = "stopping"
	}
	return `{"sessionId":` + jsonStr(catID) +
		`,"runState":` + jsonStr(name) +
		`,"runMs":{"idle":0,"wait":` + itoa64(phases.Wait) +
		`,"link":` + itoa64(phases.Link) +
		`,"think":` + itoa64(phases.Think) +
		`,"tool":` + itoa64(phases.Tool) +
		`,"run":` + itoa64(phases.Run) +
		`,"reply":` + itoa64(phases.Reply) + `}` +
		`,"requests":` + itoa64(requests) +
		`,"note":` + s.Note.BuildJSON() +
		`,"delay":` + delayJSON +
		`,"pending":` + s.BuildPendingFragment() +
		`,"conn":{"server":` + jsonStr(server) + `,"clients":` + itoa(s.clientCount()) + `}` +
		`,"tokens":{"prompt":` + itoa64(roundTokens.Prompt) +
		`,"completion":` + itoa64(roundTokens.Completion) +
		`,"cacheHit":` + itoa64(roundTokens.CacheHit) +
		`,"miss":` + itoa64(roundTokens.Miss()) +
		`,"rate":` + rateJSON(roundTokens) +
		`,"context":` + itoa64(s.ContextTokensKnown()) +
		`,"count":` + itoa64(count) +
		`,"sessionPrompt":` + itoa64(sessionTokens.Prompt) +
		`,"sessionCompletion":` + itoa64(sessionTokens.Completion) +
		`,"sessionCacheHit":` + itoa64(sessionTokens.CacheHit) +
		`,"sessionMiss":` + itoa64(sessionTokens.Miss()) +
		`,"sessionRate":` + rateJSON(sessionTokens) + `}` +
		`,"meta":{"catId":` + jsonStr(catID) +
		`,"displayName":` + jsonStr(displayName) +
		`,"sessionId":` + jsonStr(sessionID) +
		`,"createdAt":` + itoa64(createdAt) +
		`,"lastActiveAt":` + itoa64(lastActive) +
		`,"contextCount":` + itoa64(count) +
		`,"contextChars":` + itoa64(chars) + `}` +
		`,"pet":{"kind":` + jsonStr(petKind) + `}}`
}

// roundTokenSnapshot 轮级 token 三项——Idle 取最近落盘边界快照，其余态取实时累计（C# 同口径）。
func (s *Session) roundTokenSnapshot() SessionTokens {
	r := s.roundState()
	r.mu.Lock()
	idle := r.phase == chatPhaseIdle
	live := SessionTokens{Prompt: r.usagePrompt, CacheHit: r.usageCacheHit, Completion: r.usageCompletion}
	r.mu.Unlock()
	if !idle {
		return live
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.roundTokens
}

// rateJSON 命中率数值文本——cacheHit ÷ prompt 保留四位（C# Math.Round(rate, 4) 同口径；分母 0 = 0）。
func rateJSON(tokens SessionTokens) string {
	if tokens.Prompt <= 0 {
		return "0"
	}
	value := math.Round(float64(tokens.CacheHit)/float64(tokens.Prompt)*10000) / 10000
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// BuildViewFullJSON 全量视图帧——连接建立首帧（state 现取 + persist full + live）。
func (s *Session) BuildViewFullJSON() string {
	s.ViewBus.SetState(s.BuildStateJSON())
	return s.ViewBus.BuildFull()
}

// BuildViewFullJSONReadOnly 全量视图帧（只读）——多实例共享会话时的观测首帧：
// 内容与 BuildViewFullJSON 一致，但不消耗待推标记（取帧者仍是唯一推送实例）。
func (s *Session) BuildViewFullJSONReadOnly() string {
	s.ViewBus.SetState(s.BuildStateJSON())
	return s.ViewBus.BuildFullReadOnly()
}

// TakeViewFrameJSON 增量视图帧——每帧取（state 现取 + 追加 / 增量）；无变化返回空串（零字节）。
func (s *Session) TakeViewFrameJSON() string {
	s.ViewBus.SetState(s.BuildStateJSON())
	frame, ok := s.ViewBus.TryTakeFrame()
	if !ok {
		return ""
	}
	return frame
}

// AttachView 附加视图推送面——宿主外观层就位后调用一次：
// 总线就绪 + 持久块「建条即推」+ 全量数据源（视图存储是唯一权威，全量帧每次现取）。
func (s *Session) AttachView() {
	s.ViewBus.SetLogf(func(level int, message, category string) {
		logAt(LogModuleViewStore, level, message, category)
	})
	s.ViewBus.Attach()
	if s.Views != nil {
		s.Views.OnBlockAppended = s.ViewBus.PushPersist
		s.ViewBus.AttachSource(s.Views.Blocks)
	}
}

// AttachConnInfo 注入连接数提供器——state 段 conn.clients 数据源（未注入 = 0）。
func (s *Session) AttachConnInfo(fn func() int) {
	s.mu.Lock()
	s.clientCountFn = fn
	s.mu.Unlock()
}

// clientCount 当前连接数（提供器缺失 = 0）。
func (s *Session) clientCount() int {
	s.mu.Lock()
	fn := s.clientCountFn
	s.mu.Unlock()
	if fn == nil {
		return 0
	}
	return fn()
}

// SetPetKind 设置桌宠种类——外观池解析产物（空 = 无桌宠，前端按空值不渲染）。
func (s *Session) SetPetKind(kind string) {
	s.mu.Lock()
	s.petKind = kind
	s.mu.Unlock()
}

// PetKind 桌宠种类（测试与诊断读面）。
func (s *Session) PetKind() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.petKind
}
