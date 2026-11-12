package session

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"cathome5/host/toolpool"
)

// toolCallInfo 是 LLM 声明的一条工具调用（OpenAI wire 解析产物）。
type toolCallInfo struct {
	// ID tool_call_id（结果配对键）
	ID string
	// Name 工具名
	Name string
	// Arguments 参数 JSON（未注入 catId）
	Arguments string
	// Index 并发序号（1-based）
	Index int
	// Total 并发总数
	Total int
}

// toolDog 是工具单——单步工单载体（声明序入列 = 回填序）。
type toolDog struct {
	// ToolCallID 结果配对键
	ToolCallID string
	// Name 工具名
	Name string
	// ArgsJSON 参数整包 JSON（已注入 catId）
	ArgsJSON string
	// Order 执行序值（静态表裁决）
	Order int
	// Result 回执文本
	Result string
	// IsClosed 已闭合
	IsClosed bool
	// IsTimedOut 已超时
	IsTimedOut bool
	// StartedAtMs 派发时刻（Unix 毫秒；0 = 未派发）
	StartedAtMs int64
	// ClaimedAtMs 认领时刻（0 = 未观测——快单不造值）
	ClaimedAtMs int64
	// ClosedAtMs 闭合时刻
	ClosedAtMs int64
	// OfficeID 工单号（0 = 未上架）
	OfficeID int64
	// TimeoutFrames 工单时限（帧；0 = 未上架）
	TimeoutFrames int64
}

// ElapsedMs 运行时长——派发 → 闭合；未派发返回 -1（不造值）。
func (d *toolDog) ElapsedMs() int64 {
	if d.StartedAtMs <= 0 {
		return -1
	}
	end := d.ClosedAtMs
	if end <= 0 {
		end = nowMillis()
	}
	span := end - d.StartedAtMs
	if span < 0 {
		return -1
	}
	return span
}

// QueueMs 排队时长——派发 → 认领；未观测返回 -1。
func (d *toolDog) QueueMs() int64 {
	if d.StartedAtMs <= 0 || d.ClaimedAtMs <= 0 {
		return -1
	}
	span := d.ClaimedAtMs - d.StartedAtMs
	if span < 0 {
		return -1
	}
	return span
}

// RunMs 执行时长——认领 → 闭合；未观测返回 -1。
func (d *toolDog) RunMs() int64 {
	if d.ClaimedAtMs <= 0 {
		return -1
	}
	end := d.ClosedAtMs
	if end <= 0 {
		end = nowMillis()
	}
	span := end - d.ClaimedAtMs
	if span < 0 {
		return -1
	}
	return span
}

// parseToolCalls 解析 tool_calls JSON（OpenAI wire：{id, function:{name, arguments}}）。
//
// 解析失败 / 非数组 → 空列表（与发单侧同语义：空批立即收敛）。
func parseToolCalls(toolCallsJSON string) []toolCallInfo {
	if toolCallsJSON == "" {
		return nil
	}
	var raw []struct {
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	if err := json.Unmarshal([]byte(toolCallsJSON), &raw); err != nil {
		logAt(LogModuleHost, 3, "tool_calls 解析失败: "+err.Error(), LogCategoryTool)
		return nil
	}
	list := make([]toolCallInfo, 0, len(raw))
	for i, item := range raw {
		list = append(list, toolCallInfo{
			ID:        item.ID,
			Name:      item.Function.Name,
			Arguments: decodeArgEntities(item.Function.Name, item.Function.Arguments),
			Index:     i + 1,
			Total:     len(raw),
		})
	}
	return list
}

// enterToolBatch 工具批入口——声明解析 → 端到端门禁 → 分批 → 首批发单。
func (s *Session) enterToolBatch(toolCallsJSON string) {
	r := s.roundState()
	r.mu.Lock()
	r.toolBatchActive = true
	r.dogs = nil
	r.hostDogs = nil
	r.batches = nil
	r.batchIndex = -1
	r.mu.Unlock()

	calls := parseToolCalls(toolCallsJSON)
	// [P0] 批内 timeback-start / timeback-back 各至多 1 条
	startIndex := -1
	backIndex := -1
	duplicated := make([]bool, len(calls))
	for i, call := range calls {
		switch call.Name {
		case "timeback-start":
			if startIndex < 0 {
				startIndex = i
			} else {
				duplicated[i] = true
			}
		case "timeback-back":
			if backIndex < 0 {
				backIndex = i
			} else {
				duplicated[i] = true
			}
		}
	}
	_ = backIndex

	// [P1] start 前置同步直执（其余门禁读的是 start 之后的作用域终态）
	var startDog *toolDog
	if startIndex >= 0 {
		call := calls[startIndex]
		dog := &toolDog{ToolCallID: call.ID, Name: call.Name, ArgsJSON: s.injectCatID(call.Arguments)}
		if !s.isToolAllowed(call.Name) {
			dog.Result = "ERR|TOOL_FORBIDDEN|工具不在当前授权面: " + call.Name
			logAt(LogModuleHost, 2, "工具 "+call.Name+" 被拒绝：不在本会话声明面", LogCategoryTool)
		} else {
			dog.Result = s.executeBuiltin(call.Name, dog.ArgsJSON)
			r.mu.Lock()
			r.toolCallCount++
			r.mu.Unlock()
		}
		dog.IsClosed = true
		startDog = dog
	}

	// [P2] 按声明序建单（门禁：授权面 → timeback 三段）
	for i, call := range calls {
		if duplicated[i] {
			dog := &toolDog{ToolCallID: call.ID, Name: call.Name, ArgsJSON: call.Arguments}
			dog.Result = "ERR|TIMEBACK_ARGS|批内不允许多条 timeback-start / timeback-back 调用（各至多 × 1）——第 " + strconv.Itoa(i+1) + " 条被拒"
			dog.IsClosed = true
			r.mu.Lock()
			r.dogs = append(r.dogs, dog)
			r.mu.Unlock()
			logAt(LogModuleHost, 2, "timeback 批内重复调用被拒（第 "+strconv.Itoa(i+1)+" 条）", LogCategoryTimeback)
			continue
		}
		if i == startIndex {
			r.mu.Lock()
			r.dogs = append(r.dogs, startDog)
			r.mu.Unlock()
			continue
		}
		if !s.isToolAllowed(call.Name) {
			dog := &toolDog{ToolCallID: call.ID, Name: call.Name, ArgsJSON: call.Arguments}
			dog.Result = "ERR|TOOL_FORBIDDEN|工具不在当前授权面: " + call.Name
			dog.IsClosed = true
			r.mu.Lock()
			r.dogs = append(r.dogs, dog)
			r.mu.Unlock()
			logAt(LogModuleHost, 2, "工具 "+call.Name+" 被拒绝：不在本会话声明面", LogCategoryTool)
			continue
		}
		if gateText := s.Timeback.CheckTool(call.Name); gateText != "" {
			dog := &toolDog{ToolCallID: call.ID, Name: call.Name, ArgsJSON: call.Arguments}
			dog.Result = gateText
			dog.IsClosed = true
			r.mu.Lock()
			r.dogs = append(r.dogs, dog)
			r.mu.Unlock()
			logAt(LogModuleHost, 2, "工具 "+call.Name+" 被 timeback 门禁拒绝", LogCategoryTimeback)
			continue
		}
		args := s.injectCatID(call.Arguments)
		r.mu.Lock()
		r.toolCallCount++
		r.mu.Unlock()
		dog := &toolDog{ToolCallID: call.ID, Name: call.Name, ArgsJSON: args, Order: toolpool.ResolveOrder(call.Name)}
		r.mu.Lock()
		r.dogs = append(r.dogs, dog)
		r.mu.Unlock()
	}

	// [P3] 分批 + 首批启动
	s.buildBatches()
	r.mu.Lock()
	dogs := make([]*toolDog, len(r.dogs))
	copy(dogs, r.dogs)
	r.mu.Unlock()
	for _, dog := range dogs {
		if dog.IsClosed {
			s.flushToolCard(dog)
		}
	}
	r.mu.Lock()
	batchCount := len(r.batches)
	r.mu.Unlock()
	if batchCount > 0 {
		s.startBatch(0)
	}
	r.mu.Lock()
	r.phase = chatPhaseToolBatchRunning
	r.phaseFrames = 0
	r.mu.Unlock()
}

// buildBatches 分批计划——规则单点实装 toolpool.PlanBatches（已闭合单不入批）。
func (s *Session) buildBatches() {
	r := s.roundState()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batches = nil
	r.batchIndex = -1
	open := make([]*toolDog, 0, len(r.dogs))
	names := make([]string, 0, len(r.dogs))
	orders := make([]int, 0, len(r.dogs))
	for _, dog := range r.dogs {
		if dog.IsClosed {
			continue
		}
		open = append(open, dog)
		names = append(names, dog.Name)
		orders = append(orders, dog.Order)
	}
	plan := toolpool.PlanBatches(names, orders)
	for _, indexes := range plan {
		batch := make([]*toolDog, 0, len(indexes))
		for _, index := range indexes {
			batch = append(batch, open[index])
		}
		r.batches = append(r.batches, batch)
	}
}

// startBatch 批启动——host-* 延迟直执登记 / 内置直执 / 外部执行体。
func (s *Session) startBatch(index int) {
	r := s.roundState()
	r.mu.Lock()
	r.batchIndex = index
	r.hostDogs = nil
	r.phaseFrames = 0
	batch := r.batches[index]
	r.mu.Unlock()
	for _, dog := range batch {
		if dog.IsClosed {
			continue
		}
		dog.StartedAtMs = nowMillis()
		if strings.HasPrefix(dog.Name, "host-") {
			// 宿主延迟直执——本批末尾执行（同批构建类工具先完成产物落地）
			r.mu.Lock()
			r.hostDogs = append(r.hostDogs, dog)
			r.mu.Unlock()
			dog.IsClosed = true
			continue
		}
		if s.builtinTool(dog.Name) {
			dog.Result = s.executeBuiltin(dog.Name, dog.ArgsJSON)
			dog.IsClosed = true
			dog.ClosedAtMs = nowMillis()
			continue
		}
		// 非内置：OA 异步工单（Post → 组 Flow 认领执行 → Tick 收回执）
		r.mu.Lock()
		office := r.toolOffice
		r.mu.Unlock()
		if office == nil {
			dog.Result = "ERR|OA_POST_FAIL|工单提交失败（OA 不可用）: " + dog.Name
			dog.IsClosed = true
			dog.ClosedAtMs = nowMillis()
			logAt(LogModuleHost, 2, "工具 "+dog.Name+" 工单提交失败（OA 不可用）——诚实 ERR", LogCategoryTool)
			continue
		}
		officeID, errText := office.Post(dog.Name, dog.ArgsJSON, toolOfficeTimeoutFrames)
		if errText != "" {
			dog.Result = errText
			dog.IsClosed = true
			dog.ClosedAtMs = nowMillis()
			logAt(LogModuleHost, 2, "工具 "+dog.Name+" 工单提交失败——诚实 ERR", LogCategoryTool)
			continue
		}
		dog.OfficeID = officeID
		dog.TimeoutFrames = toolOfficeTimeoutFrames
	}
}

// pumpToolBatch 工具批推进——逐单轮询 / 批间推进 / 收集回填 / 续轮。
func (s *Session) pumpToolBatch() {
	r := s.roundState()
	r.mu.Lock()
	r.phaseFrames++
	batchIndex := r.batchIndex
	batchCount := len(r.batches)
	batch := []*toolDog(nil)
	if batchIndex >= 0 && batchIndex < batchCount {
		batch = r.batches[batchIndex]
	}
	hostDogs := append([]*toolDog(nil), r.hostDogs...)
	batchStartedAt := r.phaseStartedAt
	r.mu.Unlock()

	// 工单轮询——认领 / 闭合 / 超时三态（OA 由 100ms 机制 ticker 推进）
	r.mu.Lock()
	office := r.toolOffice
	r.mu.Unlock()
	for _, dog := range batch {
		s.pollOffice(office, dog)
	}

	allDone := true
	for _, dog := range batch {
		if !dog.IsClosed && !dog.IsTimedOut {
			allDone = false
			continue
		}
		if !containsDog(hostDogs, dog) {
			s.flushToolCard(dog)
		}
	}
	if !allDone && timeSinceMs(batchStartedAt) < toolBatchWait.Milliseconds() {
		return
	}
	// host-* 延迟直执
	for _, dog := range hostDogs {
		result := s.runHostTool(dog.Name, dog.ArgsJSON)
		if result == "" {
			result = "ERR|EMPTY_RESULT|工具执行无结果"
		}
		dog.Result = result
		dog.ClosedAtMs = nowMillis()
		s.flushToolCard(dog)
	}
	// 批间推进
	if batchIndex+1 < batchCount {
		s.startBatch(batchIndex + 1)
		return
	}
	// 收集——声明序回填
	r.mu.Lock()
	dogs := make([]*toolDog, len(r.dogs))
	copy(dogs, r.dogs)
	r.mu.Unlock()
	for _, dog := range dogs {
		if !dog.IsClosed && !dog.IsTimedOut {
			dog.IsTimedOut = true
		}
		if dog.IsTimedOut {
			dog.IsClosed = true
		}
		s.flushToolCard(dog)
		msg := s.Context.AddToolResult(dog.ToolCallID, dog.Name, dog.Result)
		if msg != nil {
			s.appendStoredMessage(*msg)
			s.Timeback.NoteEvent()
			s.Timeback.NoteWrite(dog.Name, dog.ArgsJSON, dog.Result)
			if s.Views != nil {
				s.Views.OnToolResult(*msg, s.Context.Count()-1, dog.ElapsedMs(), dog.QueueMs(), dog.RunMs())
			}
		}
	}
	r.mu.Lock()
	r.toolBatchActive = false
	r.mu.Unlock()
	// [段2d-0] 图片注入——本批 image-inject 登记合并为一条 user 注入消息（落在作用域区间内：back 回收时一并删除）
	s.FlushImageInjections()
	// timeback 回卷 / 规范卡 / 状态自述
	s.Timeback.Apply()
	s.Timeback.PostCard()
	s.Timeback.FlushNotice()
	// 忙时插话——有排队消息则同轮插入并续轮
	if s.DrainPendingIntoRound(true) {
		r.mu.Lock()
		r.round = 0
		r.mu.Unlock()
		s.launchLlm()
		return
	}
	r.mu.Lock()
	r.round++
	r.mu.Unlock()
	s.launchLlm()
}

// flushToolCard 工具卡定稿兜底——空结果补具名 ERR（终态卡由持久区按声明序落位）。
func (s *Session) flushToolCard(dog *toolDog) {
	if dog.Result != "" {
		return
	}
	if dog.IsTimedOut {
		dog.Result = "ERR|OA_TIMEOUT|工单超时（时限内无回执）: " + dog.Name + "——超时 ≠ 终止：只失去回执，宿主不中断已认领的执行，底层可能仍在跑；勿用同参数重试（可能重复执行），长任务请分片（拆成多次小批）或走断点续传"
		logAt(LogModuleHost, 2, "工具 "+dog.Name+" 超时（时限内无回执）——诚实 ERR", LogCategoryTool)
		return
	}
	dog.Result = "ERR|EMPTY_RESULT|工具执行无结果"
}

// isToolAllowed 授权面判定——注入的实时判定优先，未接线回落会话声明面快照。
func (s *Session) isToolAllowed(name string) bool {
	r := s.roundState()
	r.mu.Lock()
	authorizer := r.authorizer
	r.mu.Unlock()
	if authorizer != nil {
		return authorizer(name)
	}
	return s.ToolAllowed(name)
}

// builtinTool 内置工具判定。
func (s *Session) builtinTool(name string) bool {
	r := s.roundState()
	r.mu.Lock()
	fn := r.isBuiltin
	r.mu.Unlock()
	return fn != nil && fn(name)
}

// executeBuiltin 内置工具直执——未接线返回具名 ERR（不静默）。
func (s *Session) executeBuiltin(name, argsJSON string) string {
	r := s.roundState()
	r.mu.Lock()
	fn := r.builtinExec
	r.mu.Unlock()
	if fn == nil {
		return "ERR|BUILTIN_MISSING|内置工具执行面未接线: " + name
	}
	return fn(name, argsJSON)
}

// runHostTool 宿主延迟直执行体（未接线 = 具名 ERR）。
func (s *Session) runHostTool(name, argsJSON string) string {
	r := s.roundState()
	r.mu.Lock()
	fn := r.toolExec
	r.mu.Unlock()
	if fn == nil {
		return "ERR|EMPTY_RESULT|宿主直执面未接线: " + name
	}
	return fn(name, argsJSON)
}

// injectCatID 载荷注入猫 key——arguments 合并 catId 字段（非对象 / 解析失败原样透传）。
func (s *Session) injectCatID(arguments string) string {
	if arguments == "" {
		return arguments
	}
	fields, err := parseOrderedObject(arguments)
	if err != nil {
		return arguments
	}
	var b strings.Builder
	b.WriteString("{")
	hasCat := false
	for i, field := range fields {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(jsonStr(field.Key))
		b.WriteString(":")
		b.WriteString(string(field.Value))
		if field.Key == "catId" {
			hasCat = true
		}
	}
	if !hasCat {
		if len(fields) > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"catId":`)
		b.WriteString(jsonStr(s.CatKey))
	}
	b.WriteString("}")
	return b.String()
}

// containsDog 工具单是否在清单内。
func containsDog(list []*toolDog, target *toolDog) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

// timeSinceMs 从起点到现在的毫秒（零值 = 0）。
func timeSinceMs(start time.Time) int64 {
	if start.IsZero() {
		return 0
	}
	return time.Since(start).Milliseconds()
}
