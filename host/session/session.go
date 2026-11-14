package session

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// ToolSpec 是会话注入面工具声明——wire 声明三字段。
type ToolSpec struct {
	// Name 工具名
	Name string
	// Description 语义化描述（含 order 注记）
	Description string
	// ParametersJSON 参数 JSON Schema
	ParametersJSON string
}

// IsPrivilegedTool 特权工具判定——由装配层注入（读注册面组级标记；空 = 非特权）。
var IsPrivilegedTool = func(name string) bool { return false }

// SessionMetaPathProvider 会话元数据落点解析——猫 key → 元数据文件绝对路径（空 = 不持久化）。
var SessionMetaPathProvider func(catID string) string

// APIRole 会话端点角色面——C# ChatSession._apiRole 的窄投射（实现 = llm.EndpointRole）。
//
// 角色状态只有一份（组合根共享同一实例）：会话读它渲染角色文本，LLM 客户端读它解析目标端点。
type APIRole interface {
	// EffectiveUseBackup 生效角色——true=当前走备用站（窗口期内以窗口角色为准）
	EffectiveUseBackup() bool
	// ToggleManual 手动对调——返回切换后的角色（true=备用）
	ToggleManual() bool
}

// Session 是宿主会话实体——前文 / 视图 / 注入面 / Note / timeback / 待处理队列的聚合。
//
// 职责边界：本实体只承载「会话自身状态与其文本面」；LLM 请求、工具批、相位推进归 P3-c。
type Session struct {
	// mu 保护注入面与统计面（HTTP 线程读 · 主线程写）
	mu sync.Mutex
	// CatKey 猫 key（寻址主键——会话目录名）
	CatKey string
	// DisplayName 会话显示名
	DisplayName string
	// Context 真实前文容器
	Context *Context
	// Store 前文落盘
	Store *SessionStore
	// Views 视图存储
	Views *ViewStore
	// ViewBus 视图出口（state / persist / live 三段；会话重置经此广播——未 Attach 时只记待发标记）
	ViewBus *ViewBus
	// Note 任务追踪
	Note *NoteTracker
	// Timeback 上下文作用域
	Timeback *Timeback
	// Pending 待处理队列（SetCatKey 后可用）
	Pending *PendingQueue
	// round 轮执行状态（P3-c：相位 / 流槽 / 工具批 / 统计）
	round *roundState
	// metaStore 元数据落盘器（懒解析）
	metaStore *SessionMetaStore
	// metaResolved 元数据落盘器解析标志
	metaResolved bool
	// sessionID 会话实例 ID（session.new 生成；显示用）
	sessionID string
	// createdAt 会话创建时刻（Unix 毫秒）
	createdAt int64
	// contextTokens 实时前文长度（最近一次请求 prompt）
	contextTokens int64
	// persistedContextTokens 落盘快照（最后一次请求边界值）
	persistedContextTokens int64
	// persistedLastActiveAt 最后活跃时刻落盘快照
	persistedLastActiveAt int64
	// contextChars 前文总字符数（轮末 / 恢复时刷新）
	contextChars int64
	// tools 注入面会话快照（会话创建时冻结；仅 session.new 更换）
	tools []ToolSpec
	// petKind 桌宠种类（外观池解析；空 = 无桌宠——前端按空值不渲染）
	petKind string
	// clientID 客户端标识（入站自报 x-client-id；装配期写入）
	clientID string
	// selfProject 自报项目挂载名（空 = 未挂载）
	selfProject string
	// selfMountMode 自报挂载模式（expert / readme；未挂载为空）
	selfMountMode string
	// selfJobs 自报 Job 清单（逗号分隔；未挂载为空）
	selfJobs string
	// clientCountFn 连接数提供器（state 段 conn.clients 数据源；nil = 0）
	clientCountFn func() int
	// imagePathResolver 图片路径规范化器（根寻址 → 绝对路径；nil = 原样透传）
	imagePathResolver func(path string) string
	// delayNoted 延迟队列未实现出声（state 段 delay 空口径——一次性）
	delayNoted sync.Once
	// sessionTokens 会话级累计三项
	sessionTokens SessionTokens
	// roundTokens 轮级累计三项（最后一次请求边界态）
	roundTokens SessionTokens
	// roundPhases 本轮六态累计毫秒（最后一次请求边界态）
	roundPhases SessionPhases
	// roundElapsedMs 本轮耗时毫秒（最后一次请求边界态）
	roundElapsedMs int64
	// roundRequests 本轮请求次数
	roundRequests int64
	// roundTools 本轮工具调用次数
	roundTools int64
	// apiRole 会话端点角色面（C# ChatSession._apiRole 的窄投射；nil = 未接线——角色按「主要」处理）
	apiRole APIRole
	// modelOverride 会话级临时模型（空=跟随端点默认；不回写端点池配置）
	modelOverride string
	// roundSummary 轮末总结服务状态（开关 / 在途工单 / 本轮缓冲）
	roundSummary roundSummaryState
	// Delays 延迟队列（timer 排程注入 + state.delay 段数据源；nil = 未接线——timer 明示、状态段空 entries）
	Delays *DelayQueue
	// packPayload 加载包数据面（catKey + key → 结果 JSON；nil = 未接线）
	packPayload func(catKey, key string) string
	// packResolver 加载包路径解析（受控根展开；nil = 未接线）
	packResolver func(path string) (string, error)
	// packReader 加载包文本读取（编码探测；nil = 未接线）
	packReader func(absPath string) (string, error)
	// fullCtx 完整前文留档（nil = 未挂载——视图回落内存前文）
	fullCtx *FullContextStore
	// ctxCharsPerToken 会话实测字符/token 比值（最近一次请求的 prompt token 反推；0 = 无实测）
	ctxCharsPerToken float64
	// ctxPatrolAt 上次前文巡检时刻（节流基准；零值 = 从未巡检）
	ctxPatrolAt time.Time
	// ctxRebuildFailed 上次前文重建失败标志——失败后巡检回落节流窗口（防每请求重试刷日志）
	ctxRebuildFailed bool
	// ctxPatrolInterval 前文巡检节流窗口（生产 60 秒；测试注入 0 = 每帧巡检）
	ctxPatrolInterval time.Duration
}

// NewSession 建立会话实体（catKey 为空时先构造、SetCatKey 后补齐落盘面）。
func NewSession(catKey string, ctx *Context, store *SessionStore, views *ViewStore) *Session {
	s := &Session{
		CatKey:   catKey,
		Context:  ctx,
		Store:    store,
		Views:    views,
		ViewBus:  NewViewBus(),
		Note:     NewNoteTracker(),
		Timeback: NewTimeback(catKey),
		// 客户端标识兜底——装配层按产品版本覆盖（C# 默认 cat-home4 同口径）
		clientID: DefaultClientID,
		// 前文巡检节流窗口——生产 60 秒（测试注入 0 = 每帧巡检）
		ctxPatrolInterval: contextPatrolInterval,
	}
	s.wireTimeback()
	if catKey != "" {
		s.attachCatKey(catKey)
	}
	return s
}

// wireTimeback 接线 timeback 的依赖面（前文 / 落盘 / token 读数 / 系统注入口）。
func (s *Session) wireTimeback() {
	s.Timeback.Messages = func() []Message { return s.Context.Messages() }
	s.Timeback.ReplaceMessages = func(next []Message) { s.Context.ReplaceMessages(next) }
	s.Timeback.Persist = func(next []Message) {
		if s.Store != nil {
			s.Store.Rewrite(next)
		}
	}
	s.Timeback.Tokens = func() int64 { return s.ContextTokensKnown() }
	s.Timeback.PostSystem = func(kind, content, imagesJSON string) string {
		if s.Pending == nil {
			return "ERR|BAD_ARGS|待处理队列未就绪"
		}
		return s.Pending.PostSystem(kind, content, imagesJSON)
	}
}

// SetCatKey 设置猫 key——首次设置时补齐落盘面（前文标识 / 待处理队列 / 元数据恢复）。
func (s *Session) SetCatKey(catKey string) {
	if catKey == "" {
		return
	}
	s.CatKey = catKey
	s.Timeback.CatKey = catKey
	s.attachCatKey(catKey)
}

// attachCatKey 落盘面补齐（幂等）。
func (s *Session) attachCatKey(catKey string) {
	if s.Store != nil {
		s.Store.SetSessionID(catKey)
	}
	if s.Pending == nil {
		s.Pending = NewPendingQueue(catKey)
	}
	s.LoadMeta()
}

// SessionInstanceID 会话实例 ID（显示用）。
func (s *Session) SessionInstanceID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

// SessionCreatedAt 会话创建时刻（Unix 毫秒）。
func (s *Session) SessionCreatedAt() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createdAt
}

// SetDisplayName 设置会话显示名（空 = 保持原值）。
func (s *Session) SetDisplayName(name string) {
	if name == "" {
		return
	}
	s.mu.Lock()
	s.DisplayName = name
	s.mu.Unlock()
}

// IsDefaultSession 默认会话判定——特权面（majordomo-*）可见与可调的授权基准。
func (s *Session) IsDefaultSession() bool {
	s.mu.Lock()
	catKey := s.CatKey
	displayName := s.DisplayName
	s.mu.Unlock()
	if catKey == "majordomo" {
		return true
	}
	return catKey == "" && displayName == "majordomo"
}

// SetToolSpecs 更新工具注入面——session.new 时从最新 cat.cfg 重裁剪后调用。
//
// 特权面在非默认会话单点剔除（与授权面拦截构成双面）；快照随新会话同步落盘。
func (s *Session) SetToolSpecs(specs []ToolSpec) {
	isDefault := s.IsDefaultSession()
	s.mu.Lock()
	s.tools = filterPrivilegedSpecs(specs, isDefault)
	s.mu.Unlock()
	s.SaveMeta()
}

// DeclaredToolNames 会话注入面工具名——快照派生（info tools_drift 左值）。
func (s *Session) DeclaredToolNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, len(s.tools))
	for i, spec := range s.tools {
		names[i] = spec.Name
	}
	return names
}

// ToolSpecs 注入面快照副本。
func (s *Session) ToolSpecs() []ToolSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ToolSpec, len(s.tools))
	copy(out, s.tools)
	return out
}

// filterPrivilegedSpecs 特权面可见性过滤——非默认会话剔除全部特权工具。
func filterPrivilegedSpecs(specs []ToolSpec, isDefaultSession bool) []ToolSpec {
	if specs == nil || isDefaultSession {
		return specs
	}
	kept := make([]ToolSpec, 0, len(specs))
	for _, spec := range specs {
		if IsPrivilegedTool != nil && IsPrivilegedTool(spec.Name) {
			continue
		}
		kept = append(kept, spec)
	}
	return kept
}

// AppendInjection 消息注入落前文 + 视图——路径分派（sys → systemautomessage + 固定前缀；人工 → user）。
func (s *Session) AppendInjection(content, source, imagesJSON, origin string) {
	text := content
	isSys := IsSysSource(source)
	if isSys {
		text = BuildSysInjectText(text)
	}
	var msg *Message
	if imagesJSON != "" {
		msg = s.Context.AddUserMessageWithImages(text, imagesJSON)
	} else {
		msg = s.Context.AddUserMessage(text)
	}
	if msg == nil {
		return
	}
	// 落盘 + 视图派生（消息索引 = 当前末位）
	if s.Store != nil {
		s.Store.Append(*msg)
	}
	msgIndex := s.Context.Count() - 1
	if s.Views != nil {
		if isSys {
			s.Views.OnSystemMessage(*msg, msgIndex, source, origin)
		} else {
			s.Views.OnUserMessage(*msg, msgIndex, origin)
			// 轮末总结——本轮 user 文本入缓冲（全部 user 按序；sys 注入不计入）
			s.appendRoundSummaryUser(text)
		}
	}
}

// DrainPendingIntoRound 轮首取尽——两队列按到达序取尽并插入前文（FIFO）。
//
// includeUser=false 时只取 sys 队列（人工输入空闲注入开关开启）。返回 true = 有插入（调用方据此续轮）。
func (s *Session) DrainPendingIntoRound(includeUser bool) bool {
	if s.Pending == nil {
		return false
	}
	return s.Pending.DrainIntoRound(includeUser, func(msg PendingMessage) {
		s.AppendInjection(msg.Content, msg.Source, msg.ImagesJSON, msg.Origin)
	})
}

// ResetStats 重置统计——session.new 清前文后调用（新会话零统计起算）。
func (s *Session) ResetStats() {
	s.mu.Lock()
	s.sessionTokens = SessionTokens{}
	s.roundTokens = SessionTokens{}
	s.contextTokens = 0
	s.persistedContextTokens = 0
	s.ctxCharsPerToken = 0
	s.sessionID = NewSessionID()
	s.createdAt = nowMillis()
	s.mu.Unlock()
	s.SaveMeta()
}

// SetContextTokens 记录请求级实时前文长度（usage 帧消费点）。
func (s *Session) SetContextTokens(tokens int64) {
	s.mu.Lock()
	s.contextTokens = tokens
	s.mu.Unlock()
}

// ContextTokens 实时前文长度。
func (s *Session) ContextTokens() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.contextTokens
}

// ContextTokensKnown 已知最新前文长度——请求级实时值优先，未发起过请求回落落盘快照。
func (s *Session) ContextTokensKnown() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.contextTokens > 0 {
		return s.contextTokens
	}
	return s.persistedContextTokens
}

// LastActiveAtKnown 已知最后活跃时刻——内存前文变动优先，回落落盘快照，再回落创建时刻。
func (s *Session) LastActiveAtKnown() int64 {
	if last := s.Context.LastChangeAt(); last > 0 {
		return last
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.persistedLastActiveAt > 0 {
		return s.persistedLastActiveAt
	}
	return s.createdAt
}

// resolveMetaStore 元数据落盘器解析——按 provider 路径构造（一次解析缓存）。
func (s *Session) resolveMetaStore() *SessionMetaStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.metaResolved {
		return s.metaStore
	}
	s.metaResolved = true
	if SessionMetaPathProvider == nil || s.CatKey == "" {
		return nil
	}
	path := SessionMetaPathProvider(s.CatKey)
	if path == "" {
		return nil
	}
	s.metaStore = NewSessionMetaStore(path)
	return s.metaStore
}

// LoadMeta 载入会话元数据——启动恢复（文件缺失 = 首建：生成实例 ID + 创建时刻）。
func (s *Session) LoadMeta() {
	store := s.resolveMetaStore()
	var meta *SessionMeta
	if store != nil {
		meta = store.Load()
	}
	now := nowMillis()
	s.mu.Lock()
	if meta == nil {
		s.sessionID = NewSessionID()
		s.createdAt = now
		s.mu.Unlock()
		s.SaveMeta()
		return
	}
	s.sessionID = meta.SessionID
	if s.sessionID == "" {
		s.sessionID = NewSessionID()
	}
	s.createdAt = meta.CreatedAt
	s.persistedLastActiveAt = meta.LastActiveAt
	s.sessionTokens = meta.SessionTokens
	s.roundTokens = meta.RoundTokens
	s.persistedContextTokens = meta.ContextTokens
	s.roundRequests = meta.RoundRequests
	s.roundTools = meta.RoundTools
	s.roundPhases = meta.RoundPhases
	s.roundElapsedMs = meta.RoundElapsedMs
	s.contextChars = meta.ContextChars
	needToolsBackfill := len(meta.Tools) == 0
	s.mu.Unlock()
	s.Note.Restore(meta.Note.Tasks, int(meta.Note.Current), int(meta.Note.Done))
	// 旧档补写——快照缺失（升级前落的档）时以本次注入面为起点冻结
	if needToolsBackfill {
		s.SaveMeta()
	}
}

// SaveMeta 落盘会话元数据——覆盖式原子写；失败出声不阻断。
func (s *Session) SaveMeta() {
	store := s.resolveMetaStore()
	if store == nil {
		return
	}
	chars := computeContextChars(s.Context.Messages())
	s.mu.Lock()
	s.contextChars = chars
	meta := &SessionMeta{
		SessionID:      s.sessionID,
		CatID:          s.CatKey,
		DisplayName:    s.DisplayName,
		CreatedAt:      s.createdAt,
		LastActiveAt:   s.lastActiveAtLocked(),
		ContextCount:   int64(s.Context.Count()),
		ContextChars:   chars,
		ContextTokens:  s.contextTokensKnownLocked(),
		SessionTokens:  s.sessionTokens,
		RoundTokens:    s.roundTokens,
		RoundPhases:    s.roundPhases,
		RoundRequests:  s.roundRequests,
		RoundTools:     s.roundTools,
		RoundElapsedMs: s.roundElapsedMs,
		Note:           buildPersistedNote(s.Note),
		Tools:          buildPersistedTools(s.tools),
	}
	s.mu.Unlock()
	store.Save(meta)
}

// lastActiveAtLocked 最后活跃时刻（调用方持锁）。
func (s *Session) lastActiveAtLocked() int64 {
	if active := s.Context.LastChangeAt(); active > 0 {
		return active
	}
	return s.createdAt
}

// contextTokensKnownLocked 已知前文长度（调用方持锁）。
func (s *Session) contextTokensKnownLocked() int64 {
	if s.contextTokens > 0 {
		return s.contextTokens
	}
	return s.persistedContextTokens
}

// computeContextChars 前文总字符数——全消息 Content 长度之和。
func computeContextChars(messages []Message) int64 {
	total := int64(0)
	for _, msg := range messages {
		total += int64(len([]rune(msg.Content)))
	}
	return total
}

// buildPersistedNote 构建 Note 持久化形态（空计划 = 空数组）。
func buildPersistedNote(note *NoteTracker) SessionNote {
	tasks, current, done := note.Snapshot()
	if tasks == nil {
		tasks = []string{}
	}
	return SessionNote{Tasks: tasks, Current: int64(current), Done: int64(done)}
}

// buildPersistedTools 构建工具面持久化形态（注入面为空 = nil）。
func buildPersistedTools(specs []ToolSpec) []SessionToolSpec {
	if len(specs) == 0 {
		return nil
	}
	tools := make([]SessionToolSpec, len(specs))
	for i, spec := range specs {
		tools[i] = SessionToolSpec{Name: spec.Name, Description: spec.Description, ParametersJSON: spec.ParametersJSON}
	}
	return tools
}

// ResolveSessionTools 解析会话工具面——**构造前**调用：持久化快照优先，无快照回落池面。
//
// 快照中池已不存在的工具名剔除并出声（工具退役——失败可见，不静默）。
func ResolveSessionTools(catID string, poolSpecs []ToolSpec) []ToolSpec {
	persisted := loadPersistedTools(catID)
	if len(persisted) == 0 {
		return poolSpecs
	}
	list := make([]ToolSpec, 0, len(persisted))
	dropped := 0
	for _, item := range persisted {
		if poolHasName(poolSpecs, item.Name) {
			list = append(list, ToolSpec{Name: item.Name, Description: item.Description, ParametersJSON: item.ParametersJSON})
			continue
		}
		dropped++
	}
	if dropped > 0 {
		logAt(LogModuleHost, 2, "会话工具面快照剔除非本猫当前工具面项 "+strconv.Itoa(dropped)+" 个（快照 ∩ 当前授权）: "+catID, "TOOL")
	}
	if len(list) == 0 {
		return poolSpecs
	}
	return list
}

// loadPersistedTools 读取持久化工具面快照（未接线 / 无文件 = nil）。
func loadPersistedTools(catID string) []SessionToolSpec {
	if SessionMetaPathProvider == nil || catID == "" {
		return nil
	}
	path := SessionMetaPathProvider(catID)
	if path == "" {
		return nil
	}
	meta := NewSessionMetaStore(path).Load()
	if meta == nil {
		return nil
	}
	return meta.Tools
}

// poolHasName 池面名字命中——快照条目是否仍在当前池中（退役剔除判据）。
func poolHasName(poolSpecs []ToolSpec, name string) bool {
	for _, spec := range poolSpecs {
		if spec.Name == name {
			return true
		}
	}
	return false
}

// BuildNoteJSON 状态段 note 字段。
func (s *Session) BuildNoteJSON() string { return s.Note.BuildJSON() }

// BuildPendingFragment pending 段 JSON（人工队列投影）。
func (s *Session) BuildPendingFragment() string {
	if s.Pending == nil {
		return `{"entries":[]}`
	}
	return s.Pending.BuildFragment()
}

// ToolAllowed 会话授权面判定——按注入面快照的白名单（实时判定归授权面，快照只判「是否在本会话声明面内」）。
func (s *Session) ToolAllowed(name string) bool {
	if name == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, spec := range s.tools {
		if spec.Name == name {
			return true
		}
	}
	return false
}

// DescribeInjection 注入面摘要——诊断用（名 + 描述首行）。
func (s *Session) DescribeInjection() string {
	specs := s.ToolSpecs()
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	return strings.Join(names, ",")
}

// SetAPIRole 接线会话端点角色面——与 LLM 客户端共享同一实例（组合根单点，C# ChatSession 持 _apiRole 引用同形）。
func (s *Session) SetAPIRole(role APIRole) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apiRole = role
}

// ToggleAPIRole 手动对调端点角色——仅本会话有效（清自动窗口：用户意志优先）；日志 + 视图提示 + 返回角色文本。
//
// 逐字对齐 C# ChatSession.ToggleApiRole：角色面未接线 → 返回「主要」不翻转（无日志、无视图）。
func (s *Session) ToggleAPIRole() string {
	s.mu.Lock()
	role := s.apiRole
	catKey := s.CatKey
	s.mu.Unlock()
	if role == nil {
		return "主要"
	}
	text := "主要"
	if role.ToggleManual() {
		text = "备用"
	}
	logAt("LLM", 1, "手动切换端点角色："+catKey+" → "+text+"（仅本会话有效）", LogCategoryLLM)
	s.PushAPIRoleView(text, "")
	return text
}

// APIRoleText 当前端点角色文本（主要/备用）——读共享角色面实时值（窗口到期自动回落）。
func (s *Session) APIRoleText() string {
	s.mu.Lock()
	role := s.apiRole
	s.mu.Unlock()
	if role != nil && role.EffectiveUseBackup() {
		return "备用"
	}
	return "主要"
}

// APIRoleBackup 备用态判定（true = 当前走备用站）。
func (s *Session) APIRoleBackup() bool {
	s.mu.Lock()
	role := s.apiRole
	s.mu.Unlock()
	return role != nil && role.EffectiveUseBackup()
}

// PushAPIRoleView 端点切换视图提示——复用 retry 视图面（C# PushApiRoleView 同形：独立气泡）。
//
// 消费方：手动切换（原因空）+ 自动故障转移（原因 = 触发错误摘要）。
func (s *Session) PushAPIRoleView(role, reason string) {
	if s.Views == nil {
		return
	}
	payload := `{"state":"failover","role":` + jsonStr(role) + `,"text":` + jsonStr(reason) + `}`
	s.Views.AppendRetry(payload, s.ViewTimestamp())
}

// SetModelOverride 会话级临时模型写入——空串 = 清空覆盖（跟随端点默认模型）；不回写端点池配置。
func (s *Session) SetModelOverride(model string) string {
	value := strings.TrimSpace(model)
	s.mu.Lock()
	s.modelOverride = value
	s.mu.Unlock()
	return value
}

// ModelOverride 会话级临时模型（空 = 跟随端点默认）。
func (s *Session) ModelOverride() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modelOverride
}

// SetDelayQueue 接线延迟队列（timer 排程 + state.delay 段数据源；nil = 未接线）。
func (s *Session) SetDelayQueue(queue *DelayQueue) {
	s.mu.Lock()
	s.Delays = queue
	s.mu.Unlock()
}

// DelayQueue 取延迟队列（nil = 未接线）——delay.* 指令族与 timer 共用的读面。
func (s *Session) DelayQueue() *DelayQueue {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Delays
}

// SetPackProvider 接线加载包数据面——池解析 + 路径展开 + 文本读取三件（nil = 未接线）。
func (s *Session) SetPackProvider(payload func(catKey, key string) string, resolve func(path string) (string, error), readText func(absPath string) (string, error)) {
	s.mu.Lock()
	s.packPayload = payload
	s.packResolver = resolve
	s.packReader = readText
	s.mu.Unlock()
}
