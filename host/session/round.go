package session

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"
)

// StreamKind 是流式事件类别——数值与 llm.EventKind 一一对应（装配层做零成本转换）。
type StreamKind int

// 流式事件类别常量（与 C# LlmStreamKind 同序）。
const (
	// StreamReasoning 思考增量
	StreamReasoning StreamKind = iota
	// StreamText 回复增量
	StreamText
	// StreamToolCallsStart 工具决策流首帧
	StreamToolCallsStart
	// StreamToolCalls 工具调用完整列表
	StreamToolCalls
	// StreamUsage token 统计
	StreamUsage
	// StreamFinish finish_reason
	StreamFinish
	// StreamDone 正常结束
	StreamDone
	// StreamRetrying 传输层重试开始
	StreamRetrying
	// StreamRetryResume 退避结束、重发开始
	StreamRetryResume
	// StreamFailover 端点切换
	StreamFailover
	// StreamError 错误
	StreamError
)

// StreamEvent 是一条流式事件。
type StreamEvent struct {
	// Kind 事件类别
	Kind StreamKind
	// Text 载荷
	Text string
}

// Streamer 是流式对话执行面——消费侧接口（实现见 host/llm）。
type Streamer interface {
	// Stream 发起一次流式对话，事件按到达序回调（错误事件化，不抛）
	Stream(ctx context.Context, messages []Message, tools []ToolSpec, userID string, emit func(StreamEvent))
}

// 七态运行态常量。
const (
	// PhaseIdle 空闲（不计时）
	PhaseIdle = 0
	// PhaseWait 等待（本地退避）
	PhaseWait = 1
	// PhaseLink 链路（远端等待）
	PhaseLink = 2
	// PhaseThink 思考（远端流）
	PhaseThink = 3
	// PhaseTool 工具决策流（远端流）
	PhaseTool = 4
	// PhaseRun 执行（本地）
	PhaseRun = 5
	// PhaseReply 回复（远端流）
	PhaseReply = 6
	// PhaseCount 态数
	PhaseCount = 7
)

// PhaseNames 是七态名表（索引对应）。
var PhaseNames = [PhaseCount]string{"idle", "wait", "link", "think", "tool", "run", "reply"}

// 粗粒度轮相位。
const (
	// chatPhaseIdle 空闲
	chatPhaseIdle = iota
	// chatPhaseLlmRunning LLM 流消费中
	chatPhaseLlmRunning
	// chatPhaseToolBatchRunning 工具批执行中
	chatPhaseToolBatchRunning
	// chatPhaseDone 轮末收尾
	chatPhaseDone
)

// 轮级超时与阈值（C# 帧口径换算：100ms/帧）。
const (
	// llmTimeout 单次请求等待上限（30000 帧 ≈ 50 分钟）
	llmTimeout = 50 * time.Minute
	// toolBatchWait 工具批等待上限（1500 帧 = 150 秒）
	toolBatchWait = 150 * time.Second
	// toolOfficeTimeoutFrames 工具工单时限（1200 帧 = 120 秒；C# ToolOrderDog 同值）
	toolOfficeTimeoutFrames = int64(1200)
	// missAlertThreshold 单次请求未命中缓存告警阈值
	missAlertThreshold = 100000
	// autoContinueMax 上游异常后自动继续上限
	autoContinueMax = 12
	// autoContinueDelay 自动继续间隔
	autoContinueDelay = 15 * time.Second
)

// roundState 是会话的轮执行状态——相位 / 流槽 / 工具批 / 统计（主线程推进 + 后台流写）。
type roundState struct {
	// mu 保护轮状态（后台流消费与主线程 Pump 并发）
	mu sync.Mutex
	// streamer 流式执行面（nil = 不可用——错误可见）
	streamer Streamer
	// authorizer 授权面实时判定（nil = 回落会话快照）
	authorizer func(name string) bool
	// builtinExec 内置工具直执（nil = 无内置执行面）
	builtinExec func(name, argsJSON string) string
	// isBuiltin 内置工具判定（nil = 无内置）
	isBuiltin func(name string) bool
	// toolExec 非内置工具执行体（宿主直执面 host-* 用；nil = 不可用 → 诚实 ERR，不静默降级）
	toolExec func(name, argsJSON string) string
	// toolOffice 工具工单面（OA 消费侧；nil = 不可用 → 诚实 ERR）
	toolOffice ToolOffice
	// infoProvider info 内置工具数据源（nil = 明示不可用）
	infoProvider func() string
	// flowReloader host-reload 执行面（nil = 明示不可用——语料级热重载归机制面注入）
	flowReloader func(name, mode string) string
	// flowLister host-flows 执行面（nil = 明示不可用）
	flowLister func() string
	// cacheUserID 缓存隔离键（空 = 请求不带 user_id）
	cacheUserID string

	// phase 粗粒度轮相位
	phase int
	// phaseFrames 当前相位帧数（每 Pump 一次 +1）
	phaseFrames int64
	// phaseStartedAt 当前相位开始时刻（相位时长统计与 LLM 等待上限）
	phaseStartedAt time.Time
	// requestStartedAt 本次 LLM 请求起表（超时判据——相位切换不重置）
	requestStartedAt time.Time
	// pullCount 本轮 timeback 未闭合拉起次数（上限 3）
	pullCount int
	// phaseKind 七态当前态（-1 = 无活跃）
	phaseKind int
	// phaseStart 七态开始时刻
	phaseStart time.Time
	// phaseAccum 七态累计毫秒
	phaseAccum [PhaseCount]int64
	// reasonDurMs 本块思考用时（离开 think 态累计；推入视图时取走）
	reasonDurMs int64

	// round 当前工具轮次（0 起）
	round int64
	// roundStartedAt 本轮起算时刻
	roundStartedAt time.Time
	// roundPhases 本轮六态累计（请求结算落盘数据源）
	roundPhases SessionPhases
	// roundElapsedMs 本轮耗时（请求结算快照）
	roundElapsedMs int64

	// llmBusy LLM 后台运行中
	llmBusy bool
	// llmResultText 完整回复文本
	llmResultText string
	// llmReasoning 完整思考文本
	llmReasoning string
	// reasonAccum 思考流式累积
	reasonAccum strings.Builder
	// llmToolCallsJSON 聚合后的 tool_calls
	llmToolCallsJSON string
	// llmError 错误标志
	llmError bool
	// llmErrorText 错误原文
	llmErrorText string
	// streamClosedRetry STREAM_CLOSED 续传标志
	streamClosedRetry bool
	// finishReason 本次请求 finish_reason
	finishReason string

	// requestCount 本轮 API 请求次数
	requestCount int64
	// toolCallCount 本轮工具调用次数
	toolCallCount int64
	// toolDone 本轮工具主动 done
	toolDone bool

	// usagePrompt 本轮累计 prompt
	usagePrompt int64
	// usageCompletion 本轮累计 completion
	usageCompletion int64
	// usageCacheHit 本轮累计 cacheHit
	usageCacheHit int64

	// sawRetry 本轮发生过重试
	sawRetry bool
	// retryPushed 本轮推过 retry 视图
	retryPushed bool
	// retryAttempt 重试尝试序号
	retryAttempt string
	// retryMax 重试上限文本
	retryMax string
	// retryReason 重试原因
	retryReason string
	// sawFailover 本轮发生过端点切换
	sawFailover bool
	// emptyReplyRetry 空回复续传计数
	emptyReplyRetry int
	// autoContinueCount 连续自动继续计数
	autoContinueCount int
	// autoContinueDue 待自动继续时刻（零值 = 无待继续）
	autoContinueDue time.Time

	// paused 中止请求（HTTP 线程置位，主线程消费）
	paused bool
	// continueRequested 继续请求（HTTP 线程置位）
	continueRequested bool

	// cancel 本轮请求取消
	cancel context.CancelFunc
	// dogs 全量工具单（声明序 = 回填序）
	dogs []*toolDog
	// hostDogs 本批宿主延迟直执清单
	hostDogs []*toolDog
	// batches 分批计划
	batches [][]*toolDog
	// batchIndex 当前批序号（-1 = 无）
	batchIndex int
	// toolBatchActive 工具批执行中
	toolBatchActive bool
	// sleepDueFrame sleep 同轮等待到点帧（-1 = 无；帧计数口径 100ms/帧）
	sleepDueFrame int64
	// sleepReply sleep 到点回执（到点补入工具单结果）
	sleepReply string
}

// round 取轮状态（懒建，测试构造的 Session 也安全）。
func (s *Session) roundState() *roundState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.round == nil {
		s.round = &roundState{batchIndex: -1, phaseKind: -1, sleepDueFrame: -1}
	}
	return s.round
}

// SetStreamer 接线流式执行面。
func (s *Session) SetStreamer(streamer Streamer) {
	r := s.roundState()
	r.mu.Lock()
	r.streamer = streamer
	r.mu.Unlock()
}

// SetAuthorizer 接线授权面实时判定。
func (s *Session) SetAuthorizer(fn func(name string) bool) {
	r := s.roundState()
	r.mu.Lock()
	r.authorizer = fn
	r.mu.Unlock()
}

// SetBuiltinExecutor 接线内置工具直执与判定。
func (s *Session) SetBuiltinExecutor(isBuiltin func(name string) bool, exec func(name, argsJSON string) string) {
	r := s.roundState()
	r.mu.Lock()
	r.isBuiltin = isBuiltin
	r.builtinExec = exec
	r.mu.Unlock()
}

// SetToolExecutor 接线非内置工具执行体（OA 执行面；nil = 不可用）。
func (s *Session) SetToolExecutor(fn func(name, argsJSON string) string) {
	r := s.roundState()
	r.mu.Lock()
	r.toolExec = fn
	r.mu.Unlock()
}

// SetToolOffice 接线工具工单面（OA 消费侧；nil = 不可用 → 诚实 ERR）。
func (s *Session) SetToolOffice(office ToolOffice) {
	r := s.roundState()
	r.mu.Lock()
	r.toolOffice = office
	r.mu.Unlock()
}

// SetCacheUserID 设置缓存隔离键（请求体 user_id + x-opencode-session 头）。
func (s *Session) SetCacheUserID(value string) {
	r := s.roundState()
	r.mu.Lock()
	r.cacheUserID = value
	r.mu.Unlock()
}

// PostUser 用户消息入队——空输入零受理；Idle 时由 Pump 启动轮次。
func (s *Session) PostUser(content, source, imagesJSON, origin string) bool {
	if content == "" && imagesJSON == "" {
		return false
	}
	if source == "" {
		source = "user"
	}
	if s.Pending == nil {
		return false
	}
	return s.Pending.Post(content, source, imagesJSON, origin)
}

// PostSystemMessage 系统注入入口——类型校验 + 入 sys 队列（固定前缀在落前文单点套）。
func (s *Session) PostSystemMessage(kind, content, imagesJSON string) string {
	if s.Pending == nil {
		return "ERR|BAD_ARGS|待处理队列未就绪"
	}
	return s.Pending.PostSystem(kind, content, imagesJSON)
}

// RequestContinue 继续请求——Idle 时用当前前文重发一轮（不追加消息）。
func (s *Session) RequestContinue() {
	r := s.roundState()
	r.mu.Lock()
	r.continueRequested = true
	r.mu.Unlock()
}

// RequestPause 中止当前回合——后台流取消 + 主线程收尾。
func (s *Session) RequestPause() {
	r := s.roundState()
	r.mu.Lock()
	r.paused = true
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Pump 推进一帧——Idle 检查排队；Llm 轮询完成槽；工具批轮询；终态收尾。
func (s *Session) Pump() {
	// 轮末总结——在途工单回收（每帧；无在途零成本返回）
	s.pumpRoundSummary()
	r := s.roundState()
	r.mu.Lock()
	paused := r.paused
	phase := r.phase
	r.mu.Unlock()
	if paused {
		s.pauseFinalize()
		return
	}
	switch phase {
	case chatPhaseIdle:
		s.pumpIdle()
	case chatPhaseLlmRunning:
		s.pumpLlm()
	case chatPhaseToolBatchRunning:
		s.pumpToolBatch()
	case chatPhaseDone:
		s.closeRound()
	}
}

// pumpIdle 空闲推进——继续请求 / 自动继续到点 / 排队消息启动一轮。
func (s *Session) pumpIdle() {
	// 停机态丢弃在途系统注入——宿主即将重启，不再开新轮（人工队列跨重启保留）
	if s.Pending != nil {
		s.Pending.DropSysIfRestarting()
	}
	r := s.roundState()
	r.mu.Lock()
	continueRequested := r.continueRequested
	r.continueRequested = false
	due := r.autoContinueDue
	autoDue := !due.IsZero() && !time.Now().Before(due)
	r.mu.Unlock()

	if continueRequested {
		r.mu.Lock()
		r.autoContinueDue = time.Time{}
		r.mu.Unlock()
		s.startContinueRound()
		return
	}
	if autoDue && s.pendingCount() == 0 {
		r.mu.Lock()
		r.autoContinueDue = time.Time{}
		r.continueRequested = true
		count := r.autoContinueCount
		r.mu.Unlock()
		logAt(LogModuleHost, 1, "端点切换后自动继续（"+strconv.Itoa(count)+"/"+strconv.Itoa(autoContinueMax)+"）——延迟重发", LogCategoryLLM)
		return
	}
	if s.pendingCount() > 0 {
		s.startRoundFromPending()
	}
}

// pendingCount 待处理条目数（两队列合计）。
func (s *Session) pendingCount() int {
	if s.Pending == nil {
		return 0
	}
	user, sys := s.Pending.Len()
	return user + sys
}

// startRoundFromPending 轮首取尽——排队条目全部落前文后启动一轮。
func (s *Session) startRoundFromPending() {
	if s.pendingCount() == 0 {
		return
	}
	s.resetRoundCounters()
	r := s.roundState()
	r.mu.Lock()
	r.autoContinueCount = 0
	r.autoContinueDue = time.Time{}
	r.mu.Unlock()
	s.DrainPendingIntoRound(true)
	s.launchLlm()
}

// startContinueRound 继续轮——不追加消息，直接用当前前文发起请求。
func (s *Session) startContinueRound() {
	s.resetRoundCounters()
	s.launchLlm()
}

// resetRoundCounters 轮首计数与相位复位。
func (s *Session) resetRoundCounters() {
	r := s.roundState()
	r.mu.Lock()
	r.usagePrompt = 0
	r.usageCompletion = 0
	r.usageCacheHit = 0
	r.toolCallCount = 0
	r.requestCount = 0
	r.emptyReplyRetry = 0
	r.streamClosedRetry = false
	r.sawFailover = false
	r.sawRetry = false
	r.retryPushed = false
	r.retryAttempt = ""
	r.retryMax = ""
	r.retryReason = ""
	r.toolDone = false
	r.sleepDueFrame = -1
	r.sleepReply = ""
	r.roundStartedAt = time.Now()
	r.roundPhases = SessionPhases{}
	r.roundElapsedMs = 0
	r.phaseAccum = [PhaseCount]int64{}
	r.phaseKind = -1
	r.phaseStart = time.Time{}
	r.mu.Unlock()
	s.SetContextTokens(0)
	// 轮末总结——本轮缓冲清零（全部 user / 全部 text 按序累积）
	s.resetRoundSummaryBuffers()
	s.enterPhase(PhaseLink)
	// 轮级续跑现场——轮首落盘（宿主重启打断在途轮时，新宿主据此自动续跑一次）
	s.MarkRoundPending()
}

// launchLlm 启动 LLM 流——消息序列固化 + 后台消费 + 链路态。
func (s *Session) launchLlm() {
	r := s.roundState()
	messages := s.Context.Messages()
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	if r.cancel != nil {
		r.cancel()
	}
	r.cancel = cancel
	r.llmBusy = true
	r.llmResultText = ""
	r.llmReasoning = ""
	r.reasonAccum.Reset()
	r.reasonDurMs = 0
	r.llmToolCallsJSON = ""
	r.llmError = false
	r.llmErrorText = ""
	r.finishReason = ""
	r.paused = false
	r.phase = chatPhaseLlmRunning
	r.phaseFrames = 0
	r.requestStartedAt = time.Now()
	r.requestCount++
	streamer := r.streamer
	cacheUserID := r.cacheUserID
	tools := s.ToolSpecs()
	r.mu.Unlock()

	s.enterPhase(PhaseLink)
	if streamer == nil {
		r.mu.Lock()
		r.llmBusy = false
		r.llmError = true
		r.llmErrorText = "ERR|LLM_NOT_WIRED|LLM 流执行面未接线"
		r.mu.Unlock()
		return
	}
	go func() {
		defer func() { r.mu.Lock(); r.llmBusy = false; r.mu.Unlock() }()
		streamer.Stream(ctx, messages, tools, cacheUserID, func(ev StreamEvent) {
			s.consumeStreamEvent(ev)
		})
	}()
}

// consumeStreamEvent 消费一条流事件——写入轮槽 + 相位推进 + usage 结算。
func (s *Session) consumeStreamEvent(ev StreamEvent) {
	r := s.roundState()
	switch ev.Kind {
	case StreamText:
		r.mu.Lock()
		if r.sawRetry {
			r.sawRetry = false
			r.mu.Unlock()
			s.pushRetryView("resolved")
		} else {
			r.mu.Unlock()
		}
		r.mu.Lock()
		r.llmResultText += ev.Text
		replyText := r.llmResultText
		r.mu.Unlock()
		s.enterPhase(PhaseReply)
		// A196 临时区——状态投影：回复流当前全文整段覆盖（前端按 type 投给渲染结构）
		s.ViewBus.SetLive(TypeReplysse, replyText)
	case StreamReasoning:
		r.mu.Lock()
		if r.sawRetry {
			r.sawRetry = false
			r.mu.Unlock()
			s.pushRetryView("resolved")
		} else {
			r.mu.Unlock()
		}
		r.mu.Lock()
		r.reasonAccum.WriteString(ev.Text)
		reasonText := r.reasonAccum.String()
		r.mu.Unlock()
		s.enterPhase(PhaseThink)
		// A196 临时区——状态投影：思考流当前全文整段覆盖
		s.ViewBus.SetLive(TypeThinksse, reasonText)
	case StreamToolCallsStart:
		s.enterPhase(PhaseTool)
	case StreamToolCalls:
		r.mu.Lock()
		if r.sawRetry {
			r.sawRetry = false
			r.mu.Unlock()
			s.pushRetryView("resolved")
		} else {
			r.mu.Unlock()
		}
		r.mu.Lock()
		r.llmToolCallsJSON = ev.Text
		r.mu.Unlock()
	case StreamRetryResume:
		s.enterPhase(PhaseLink)
	case StreamRetrying:
		s.enterPhase(PhaseWait)
		attempt, maxValue, reason := parseRetryText(ev.Text)
		r.mu.Lock()
		r.sawRetry = true
		r.retryAttempt = attempt
		r.retryMax = maxValue
		r.retryReason = ApplyErrorNote(reason)
		r.mu.Unlock()
		s.pushRetryView("retrying")
	case StreamUsage:
		s.applyUsage(ev.Text)
	case StreamFinish:
		r.mu.Lock()
		r.finishReason = ev.Text
		r.mu.Unlock()
	case StreamDone:
		// 流式结束由整块 replace 表达——此处零动作
	case StreamFailover:
		r.mu.Lock()
		r.sawFailover = true
		r.mu.Unlock()
		// 端点切换——双打探针判定临时切换 / 手动对调：独立提示气泡（复用 retry 视图面，不静默）
		// 事件文本 FAILOVER|角色|原因（C# Split('|', 3) 同口径）
		foRole := "主要"
		foReason := ""
		if parts := strings.SplitN(ev.Text, "|", 3); len(parts) >= 3 {
			foRole = parts[1]
			foReason = parts[2]
		}
		s.PushAPIRoleView(foRole, foReason)
	case StreamError:
		r.mu.Lock()
		if r.paused {
			r.mu.Unlock()
			return
		}
		r.llmError = true
		r.llmErrorText = ev.Text
		if strings.HasPrefix(ev.Text, "ERR|STREAM_CLOSED") {
			r.streamClosedRetry = true
		}
		r.mu.Unlock()
	}
	// 结果槽落位（流结束后由 pumpLlm 读；此处持续同步便于观测）
	r.mu.Lock()
	r.llmReasoning = r.reasonAccum.String()
	r.mu.Unlock()
}

// parseRetryText 解析 RETRY|N/3|原因 文本。
func parseRetryText(text string) (string, string, string) {
	attempt := ""
	maxValue := ""
	reason := text
	sep1 := strings.Index(text, "|")
	if sep1 >= 0 {
		rest := text[sep1+1:]
		sep2 := strings.Index(rest, "|")
		if sep2 >= 0 {
			segment := rest[:sep2]
			parts := strings.SplitN(segment, "/", 2)
			attempt = parts[0]
			if len(parts) == 2 {
				maxValue = parts[1]
			}
			reason = rest[sep2+1:]
		}
	}
	return attempt, maxValue, reason
}

// pushRetryView 推一块 retry 视图（retrying / resolved / failed）。
func (s *Session) pushRetryView(state string) {
	if s.Views == nil {
		return
	}
	r := s.roundState()
	r.mu.Lock()
	payload := `{"state":` + jsonStr(state) + `,"attempt":` + jsonStr(r.retryAttempt) +
		`,"max":` + jsonStr(r.retryMax) + `,"text":` + jsonStr(r.retryReason) + `}`
	r.retryPushed = true
	r.mu.Unlock()
	s.Views.AppendRetry(payload, s.ViewTimestamp())
}

// resetRetryView 重试视图态清零。
func (s *Session) resetRetryView() {
	r := s.roundState()
	r.mu.Lock()
	r.retryPushed = false
	r.retryAttempt = ""
	r.retryMax = ""
	r.retryReason = ""
	r.sawRetry = false
	r.mu.Unlock()
}

// ViewTimestamp 当前视图时间戳（Unix 毫秒）——视图排序键。
func (s *Session) ViewTimestamp() int64 { return nowMillis() }

// applyUsage 消费 usage 帧——轮累计 / 会话累计 / 单次前文 / 元数据结算。
func (s *Session) applyUsage(usageJSON string) {
	if usageJSON == "" {
		return
	}
	var parsed SessionTokens
	if !parseUsageJSON(usageJSON, &parsed) {
		return
	}
	r := s.roundState()
	r.mu.Lock()
	r.usagePrompt += parsed.Prompt
	r.usageCompletion += parsed.Completion
	r.usageCacheHit += parsed.CacheHit
	round := SessionTokens{Prompt: r.usagePrompt, CacheHit: r.usageCacheHit, Completion: r.usageCompletion}
	requestCount := r.requestCount
	toolCount := r.toolCallCount
	accum := r.phaseAccum
	if r.phaseKind >= 0 && !r.phaseStart.IsZero() {
		ms := time.Since(r.phaseStart).Milliseconds()
		if ms < 0 {
			ms = 0
		}
		accum[r.phaseKind] += ms
	}
	phases := phasesFromAccum(accum)
	elapsed := int64(0)
	if !r.roundStartedAt.IsZero() {
		elapsed = time.Since(r.roundStartedAt).Milliseconds()
		if elapsed < 0 {
			elapsed = 0
		}
	}
	r.mu.Unlock()

	s.mu.Lock()
	s.sessionTokens.Prompt += parsed.Prompt
	s.sessionTokens.CacheHit += parsed.CacheHit
	s.sessionTokens.Completion += parsed.Completion
	s.roundTokens = round
	s.contextTokens = parsed.Prompt
	s.roundRequests = requestCount
	s.roundTools = toolCount
	s.roundPhases = phases
	s.roundElapsedMs = elapsed
	s.mu.Unlock()
	// A202 唯一常规落盘点——请求边界即物化点
	s.SaveMeta()
}

// parseUsageJSON 解析 usage 对象（prompt/completion/cacheHit）。
func parseUsageJSON(usageJSON string, out *SessionTokens) bool {
	var root map[string]any
	if err := json.Unmarshal([]byte(usageJSON), &root); err != nil {
		return false
	}
	out.Prompt = numberField(root, "prompt")
	out.Completion = numberField(root, "completion")
	out.CacheHit = numberField(root, "cacheHit")
	return true
}

// numberField 取数值字段（缺失 / 非数值 = 0）。
func numberField(root map[string]any, key string) int64 {
	value, exists := root[key]
	if !exists {
		return 0
	}
	switch v := value.(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}

// pumpLlm 轮询 LLM 完成槽——超时兜底 / 错误中止 / 续传 / 文本收尾 / 工具批入口。
func (s *Session) pumpLlm() {
	r := s.roundState()
	r.mu.Lock()
	r.phaseFrames++
	busy := r.llmBusy
	requestStartedAt := r.requestStartedAt
	r.mu.Unlock()
	if busy && time.Since(requestStartedAt) < llmTimeout {
		return
	}
	r.mu.Lock()
	if busy {
		r.llmBusy = false
		r.llmError = true
		r.llmErrorText = "ERR|LLM_TIMEOUT|LLM 调用超时（帧上限 30000）"
	}
	llmError := r.llmError
	streamClosed := r.streamClosedRetry
	paused := r.paused
	resultText := r.llmResultText
	reasoning := r.reasonAccum.String()
	toolCalls := r.llmToolCallsJSON
	r.mu.Unlock()

	if paused {
		s.pauseFinalize()
		return
	}
	if llmError {
		if streamClosed {
			s.retryEmptyReply("SSE 流中断（未以 [DONE] 结束）")
			return
		}
		s.abortRoundError()
		return
	}
	// 轮末总结——本轮 text 入缓冲（全部 text 按序：工具轮间隙文本与最终回复同收）
	s.appendRoundSummaryText(resultText)
	if reasoning != "" {
		s.Timeback.NoteEvent()
	}
	if toolCalls == "" {
		if strings.TrimSpace(resultText) == "" {
			s.retryEmptyReply("模型未生成回复文本")
			return
		}
		msg := s.Context.AddAssistantMessage(resultText)
		if msg != nil {
			s.appendStoredMessage(*msg)
			if s.Views != nil {
				s.Views.OnAssistantText(*msg, s.Context.Count()-1, s.takeReasonDuration())
			}
		}
		s.Timeback.NoteEvent()
		// A196 临时区——本轮收尾写空态（内容已由持久区承载）
		s.ViewBus.SetLive(TypeEmpty, "")
		if s.DrainPendingIntoRound(true) {
			r.mu.Lock()
			r.round = 0
			r.mu.Unlock()
			s.launchLlm()
			return
		}
		s.enterPhase(PhaseIdle)
		r.mu.Lock()
		r.phase = chatPhaseDone
		r.mu.Unlock()
		return
	}
	// 工具批入口——assistant 声明入上下文 + 视图
	msg := s.Context.AddAssistantToolCalls(toolCalls, s.llmReasoningValue())
	if msg != nil {
		s.appendStoredMessage(*msg)
		if s.Views != nil {
			s.Views.OnAssistantToolCalls(*msg, s.Context.Count()-1, s.takeReasonDuration())
		}
	}
	s.Timeback.NoteEvent()
	// 工具轮 seal——视图层补 gap text 块（全量外观真源：前端历史 / QQBot 转发消费）；空文本不写
	// 口径对齐 C# ChatSession.cs:2888-2892（先于 SealReasonStream——实时序：间隙文本在思考块收口前落盘）
	if s.Views != nil && resultText != "" {
		s.Views.AppendGapText(resultText, s.ViewTimestamp())
	}
	// 思考段整块——工具轮 seal：离开 think 态（内容由持久区 reason 块承载）+ 临时区复位
	s.sealReasonStream()
	s.ViewBus.SetLive(TypeEmpty, "")
	s.enterPhase(PhaseRun)
	s.enterToolBatch(toolCalls)
	// A196 临时区——工具运行态：未完成工具卡数组入 context（派发即写；完成即减）
	s.refreshToolLive()
}

// llmReasoningValue 当前思考全文。
func (s *Session) llmReasoningValue() string {
	r := s.roundState()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reasonAccum.String()
}

// retryEmptyReply 空回复续传——同上下文重发（无上限）。
func (s *Session) retryEmptyReply(reason string) {
	r := s.roundState()
	r.mu.Lock()
	r.emptyReplyRetry++
	r.streamClosedRetry = false
	r.retryAttempt = strconv.Itoa(r.emptyReplyRetry)
	r.retryMax = "∞"
	r.retryReason = ApplyErrorNote(reason)
	r.sawRetry = true
	r.mu.Unlock()
	logAt(LogModuleHost, 2, "空回复续传（"+reason+"）——同上下文重发", LogCategoryLLM)
	s.pushRetryView("retrying")
	s.launchLlm()
}

// abortRoundError 错误中止收尾——保留断点上下文 + 错误气泡；不 roundsum。
func (s *Session) abortRoundError() {
	s.sealReasonStream()
	s.phaseSettle()
	r := s.roundState()
	r.mu.Lock()
	errorText := r.llmErrorText
	retried := r.retryPushed
	sawFailover := r.sawFailover
	r.mu.Unlock()
	logAt(LogModuleHost, 2, "本轮运行态统计（中止）: "+s.buildRunStateSummary(), LogCategoryLLM)
	abortKind := "请求失败（未重试）"
	if retried {
		abortKind = "重试耗尽"
	}
	logAt(LogModuleHost, 3, "LLM 错误（"+abortKind+"——本轮中止，上下文保持断点）: "+truncateRunes(errorText, 300), LogCategoryLLM)
	if s.Views != nil {
		s.Views.AppendError(ApplyErrorNote(errorText), s.ViewTimestamp())
	}
	if retried {
		s.pushRetryView("failed")
	}
	r.mu.Lock()
	r.round = 0
	r.phase = chatPhaseIdle
	r.phaseFrames = 0
	r.llmBusy = false
	r.llmError = false
	r.llmErrorText = ""
	r.llmResultText = ""
	r.llmReasoning = ""
	r.llmToolCallsJSON = ""
	r.emptyReplyRetry = 0
	r.streamClosedRetry = false
	autoContinuable := sawFailover || retried
	if autoContinuable && r.autoContinueCount < autoContinueMax {
		r.autoContinueCount++
		r.autoContinueDue = time.Now().Add(autoContinueDelay)
	}
	r.mu.Unlock()
	s.resetRetryView()
}

// buildRunStateSummary 运行态摘要行（失败轮留档）。
func (s *Session) buildRunStateSummary() string {
	name, phases, requests := s.GetRunState()
	return "state=" + name +
		" wait=" + itoa64(phases.Wait) + "ms" +
		" link=" + itoa64(phases.Link) + "ms" +
		" think=" + itoa64(phases.Think) + "ms" +
		" tool=" + itoa64(phases.Tool) + "ms" +
		" run=" + itoa64(phases.Run) + "ms" +
		" reply=" + itoa64(phases.Reply) + "ms" +
		" requests=" + itoa64(requests)
}

// closeRound 轮末收尾——timeback / Note 拉起优先，roundsum 落盘，状态复位。
func (s *Session) closeRound() {
	if s.Timeback.Active() {
		r := s.roundState()
		r.mu.Lock()
		pull := r.pullCount
		r.mu.Unlock()
		if pull < 3 {
			r.mu.Lock()
			r.pullCount++
			r.mu.Unlock()
			s.phaseSettle()
			s.PostSystemMessage(SysKindSystemAuto, "先回收timeback域再结束工作", "")
			r.mu.Lock()
			r.round = 0
			r.phase = chatPhaseIdle
			r.mu.Unlock()
			return
		}
		r.mu.Lock()
		r.pullCount = 0
		r.mu.Unlock()
	}
	if remain, current, pull := notePullInfo(s.Note); pull {
		s.phaseSettle()
		s.PostSystemMessage(SysKindSystemAuto, "[Note 未完成] 剩余 "+strconv.Itoa(remain)+" 条\n当前任务："+current, "")
		r := s.roundState()
		r.mu.Lock()
		r.round = 0
		r.phase = chatPhaseIdle
		r.mu.Unlock()
		return
	}
	s.phaseSettle()
	sumTs := s.ViewTimestamp()
	if s.Views != nil {
		s.Views.AppendRoundSummary(s.BuildRoundSumJSON(), sumTs)
	}
	// 轮级续跑现场——正常终态即清（未终态的早退路径不清：timeback / Note 拉起会续轮）
	s.ClearPendingRound()
	// 轮末总结——轮末调度总结服务（开关开 + 配置可用；块固定插在 roundsum 之后 = ts + 1）
	s.PostRoundSummary(sumTs + 1)
	r := s.roundState()
	r.mu.Lock()
	r.round = 0
	r.phase = chatPhaseIdle
	r.phaseFrames = 0
	r.mu.Unlock()
	s.resetRetryView()
	r.mu.Lock()
	r.autoContinueCount = 0
	r.autoContinueDue = time.Time{}
	r.usagePrompt = 0
	r.usageCompletion = 0
	r.usageCacheHit = 0
	r.toolDone = false
	r.mu.Unlock()
	s.Note.Restore(nil, 0, 0)
}

// notePullInfo Note 自动拉起判定——剩余 ≥2 条才拉起（最后 1 条不拉起，全部完成天然跳过）。
func notePullInfo(note *NoteTracker) (int, string, bool) {
	if note == nil {
		return 0, "", false
	}
	tasks, current, _ := note.Snapshot()
	if len(tasks) == 0 || current+1 >= len(tasks) {
		return 0, "", false
	}
	return len(tasks) - current, tasks[current], true
}

// noteActive Note 是否有未完成计划。
func noteActive(note *NoteTracker) bool {
	if note == nil {
		return false
	}
	tasks, _, _ := note.Snapshot()
	return len(tasks) > 0
}

// pauseFinalize 中止收尾——取消流 / 工具批放弃 / 已完成保留。
func (s *Session) pauseFinalize() {
	r := s.roundState()
	r.mu.Lock()
	cancel := r.cancel
	r.cancel = nil
	r.paused = false
	r.phase = chatPhaseIdle
	r.phaseFrames = 0
	r.llmBusy = false
	r.toolBatchActive = false
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.phaseSettle()
	// A196 临时区——中断收尾写空态（未落持久块的内容随覆盖消失，不进前文 / 存档）
	s.sealReasonStream()
	s.resetRetryView()
	// 用户中止不是重启——现场清掉，下次启动不自动续跑被打断的轮
	s.ClearPendingRound()
}

// sealReasonStream 思考段终结——临时区写空态（A196 状态投影：写 empty 即清面板）。
//
// 思考内容由持久区 reason 整块承载。调用面：enterPhase 离开 think 态（唯一态转移出口）
// + 工具轮 seal + 错误中止 / 暂停收尾。重复调用幂等（同值零动作）。
func (s *Session) sealReasonStream() {
	s.ViewBus.SetLive(TypeEmpty, "")
}

// takeReasonDuration 取走本块思考用时。
func (s *Session) takeReasonDuration() int64 {
	r := s.roundState()
	r.mu.Lock()
	defer r.mu.Unlock()
	ms := r.reasonDurMs
	r.reasonDurMs = 0
	return ms
}

// enterPhase 运行态切换——结算旧态累计 + 进入新态（同态不重置起表）。
func (s *Session) enterPhase(kind int) {
	if kind < 0 || kind >= PhaseCount {
		return
	}
	r := s.roundState()
	now := time.Now()
	// leaveThink 离开 think 态标记——锁外统一收口临时区（C# PhaseEnter 同口径）
	leaveThink := false
	r.mu.Lock()
	if r.phaseKind == kind {
		r.mu.Unlock()
		return
	}
	if r.phaseKind == PhaseThink && kind != PhaseThink {
		leaveThink = true
	}
	if r.phaseKind >= 0 && !r.phaseStart.IsZero() {
		ms := now.Sub(r.phaseStart).Milliseconds()
		if ms < 0 {
			ms = 0
		}
		r.phaseAccum[r.phaseKind] += ms
		if r.phaseKind == PhaseThink {
			r.reasonDurMs += ms
		}
	}
	r.phaseKind = kind
	if kind == PhaseIdle {
		r.phaseStart = time.Time{}
	} else {
		r.phaseStart = now
	}
	r.phaseStartedAt = now
	r.mu.Unlock()
	// 锁外推——思考段终结（幂等：无在途思考流式时同值零动作）
	if leaveThink {
		s.sealReasonStream()
	}
}

// phaseSettle 结算当前运行态——累计落七态 + 态标记归零 + 快照留档。
func (s *Session) phaseSettle() {
	r := s.roundState()
	now := time.Now()
	r.mu.Lock()
	if r.phaseKind >= 0 && !r.phaseStart.IsZero() {
		ms := now.Sub(r.phaseStart).Milliseconds()
		if ms < 0 {
			ms = 0
		}
		r.phaseAccum[r.phaseKind] += ms
		if r.phaseKind == PhaseThink {
			r.reasonDurMs += ms
		}
		r.phaseKind = -1
		r.phaseStart = time.Time{}
	}
	r.roundPhases = phasesFromAccum(r.phaseAccum)
	elapsed := int64(0)
	if !r.roundStartedAt.IsZero() {
		elapsed = now.Sub(r.roundStartedAt).Milliseconds()
		if elapsed < 0 {
			elapsed = 0
		}
	}
	r.roundElapsedMs = elapsed
	r.mu.Unlock()
}

// rAccumSnapshot 取七态累计快照（含活跃态增量）。
func rAccumSnapshot(r *roundState) [PhaseCount]int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	accum := r.phaseAccum
	if r.phaseKind >= 0 && !r.phaseStart.IsZero() {
		ms := time.Since(r.phaseStart).Milliseconds()
		if ms < 0 {
			ms = 0
		}
		accum[r.phaseKind] += ms
	}
	return accum
}

// phasesFromAccum 七态累计 → 六态落盘形态（idle 不计时故不入）。
func phasesFromAccum(accum [PhaseCount]int64) SessionPhases {
	return SessionPhases{
		Wait:  accum[PhaseWait],
		Link:  accum[PhaseLink],
		Think: accum[PhaseThink],
		Tool:  accum[PhaseTool],
		Run:   accum[PhaseRun],
		Reply: accum[PhaseReply],
	}
}

// GetRunState 读运行态——态名 + 六态毫秒 + 请求次数。
func (s *Session) GetRunState() (string, SessionPhases, int64) {
	r := s.roundState()
	accum := rAccumSnapshot(r)
	r.mu.Lock()
	kind := r.phaseKind
	requests := r.requestCount
	r.mu.Unlock()
	name := "idle"
	if kind >= 0 && kind < PhaseCount {
		name = PhaseNames[kind]
	}
	return name, phasesFromAccum(accum), requests
}

// BuildRoundSumJSON 构建 roundsum 载荷——字段与 C# BuildRoundSumJson 逐字同构。
func (s *Session) BuildRoundSumJSON() string {
	r := s.roundState()
	r.mu.Lock()
	prompt := r.usagePrompt
	completion := r.usageCompletion
	cacheHit := r.usageCacheHit
	toolCount := r.toolCallCount
	toolDone := r.toolDone
	elapsed := int64(0)
	if !r.roundStartedAt.IsZero() {
		elapsed = time.Since(r.roundStartedAt).Milliseconds()
		if elapsed < 0 {
			elapsed = 0
		}
	}
	r.mu.Unlock()
	_, phases, requests := s.GetRunState()
	miss := prompt - cacheHit
	if miss < 0 {
		miss = 0
	}
	doneKind := "stream"
	if toolDone {
		doneKind = "tool"
	}
	return `{"type":"roundsum","data":{"prompt":` + itoa64(prompt) +
		`,"completion":` + itoa64(completion) +
		`,"cacheHit":` + itoa64(cacheHit) +
		`,"miss":` + itoa64(miss) +
		`,"done":` + jsonStr(doneKind) +
		`,"toolCount":` + itoa64(toolCount) +
		`,"requests":` + itoa64(requests) +
		`,"elapsedMs":` + itoa64(elapsed) +
		`,"phases":{"wait":` + itoa64(phases.Wait) +
		`,"link":` + itoa64(phases.Link) +
		`,"think":` + itoa64(phases.Think) +
		`,"tool":` + itoa64(phases.Tool) +
		`,"run":` + itoa64(phases.Run) +
		`,"reply":` + itoa64(phases.Reply) + `}}}`
}

// appendStoredMessage 追加消息落盘（前文 jsonl 增量）。
func (s *Session) appendStoredMessage(msg Message) {
	if s.Store != nil {
		s.Store.Append(msg)
	}
}

// StateEntry 会话状态条目——快照 sessions 段数据源（与 C# BuildStateEntry 同字段面）。
func (s *Session) StateEntry() map[string]any {
	runState, phases, requests := s.GetRunState()
	entry := map[string]any{
		"phase":             s.PhaseText(),
		"runState":          runState,
		"runMs":             phasesMap(phases),
		"requests":          requests,
		"round":             s.RoundValue(),
		"msgCount":          s.Context.Count(),
		"pending":           s.pendingCount(),
		"noteActive":        noteActive(s.Note),
		"contextCount":      s.Context.Count(),
		"context":           s.ContextTokensKnown(),
		"contextChars":      computeContextChars(s.Context.Messages()),
		"lastActiveAt":      s.LastActiveAtKnown(),
		"sessionPrompt":     s.sessionTokensValue().Prompt,
		"sessionCacheHit":   s.sessionTokensValue().CacheHit,
		"sessionCompletion": s.sessionTokensValue().Completion,
		"sessionMiss":       s.sessionTokensValue().Miss(),
		"sessionRate":       sessionRate(s.sessionTokensValue()),
	}
	return entry
}

// sessionRate 缓存命中率（0..1；prompt 为空 = 0）。
func sessionRate(tokens SessionTokens) float64 {
	if tokens.Prompt <= 0 {
		return 0
	}
	return float64(tokens.CacheHit) / float64(tokens.Prompt)
}

// phasesMap 六态毫秒 → 快照 runMs 段。
func phasesMap(p SessionPhases) map[string]int64 {
	return map[string]int64{
		"wait": p.Wait, "link": p.Link, "think": p.Think,
		"tool": p.Tool, "run": p.Run, "reply": p.Reply,
	}
}

// sessionTokensValue 会话累计三项。
func (s *Session) sessionTokensValue() SessionTokens {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionTokens
}

// RoundValue 当前工具轮次。
func (s *Session) RoundValue() int64 {
	r := s.roundState()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.round
}

// PhaseText 粗粒度轮相位文本（Idle / LlmRunning / ToolBatchRunning / Done）。
func (s *Session) PhaseText() string {
	r := s.roundState()
	r.mu.Lock()
	defer r.mu.Unlock()
	switch r.phase {
	case chatPhaseLlmRunning:
		return "LlmRunning"
	case chatPhaseToolBatchRunning:
		return "ToolBatchRunning"
	case chatPhaseDone:
		return "Done"
	}
	return "Idle"
}
