package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"cathome5/host/config"
	"cathome5/host/inject"
	"cathome5/host/session"
	"cathome5/host/toolpool"
	"cathome5/mau/fs"
)

// 本文件是会话指令路由——对齐 mau:CatHome4/CatHome4.Admin/AdminService.InputRoutes.cs 的前缀 / 全等分派。
//
// 已落地：`Chat ` 投递、`session.new` / `session.new <内容>` 重建（P3-d d3-a）、
// `cat.list` / `cat.info` 状态查询（d3-b）、`cat.*` / `catcfg.*` 管理族（c-6：单猫形态）与 `delay.*`（见 delaycommand.go）。
// 未落地：session clear / session count / note.* / session.rollback / session.fork / reload（归 P3-d d3 尾部）。

// sessionNewPrefix session.new 带初始消息的前缀（含尾空格，C# line.Substring(12) 同口径）。
const sessionNewPrefix = "session.new "

// catCfgGetPrefix / catCfgSetPrefix 每猫配置指令前缀（含尾空格——对齐 C# line.Substring(12).Trim()）。
const (
	catCfgGetPrefix = "cat.cfg.get "
	catCfgSetPrefix = "cat.cfg.set "
)

// commandRouter 是会话指令路由——多猫形态（d-1：cat.* 全族按猫寻址；`Chat ` 投递默认猫）。
type commandRouter struct {
	// DataRoot 数据根（cat.cfg / cat-default.cfg 读取）
	DataRoot string
	// Workspace 受控根配置（注入条目寻址）
	Workspace *config.WorkspaceConfig
	// Registry 工具注册表（声明面重算）
	Registry *toolpool.Registry
	// Pool 工具池（注入报告工具组映射——OwnerFlow 数据源）
	Pool *toolpool.Pool
	// Session 默认会话（majordomo——`Chat ` 投递目标）
	Session *session.Session
	// CatID 默认猫 key
	CatID string
	// DisplayName 显示名兜底（cat.cfg 读取失败时）
	DisplayName string
	// Running 主干运行态（单猫回落口径；多猫走 Cats 注册表）
	Running bool
	// Port 主干监听端口（cat.list / cat.info 端口字段）
	Port int
	// Cats 多猫注册表（nil = 单猫形态——测试 / 未接线场景按 majordomo 语义回落）
	Cats *catRegistry
	// Logf 出声回调（nil = 静默）
	Logf func(level int, message, category string)
	// AutoMount 消息自动挂载（nil = 不挂载）——「上工/开工 <项目>」命中即接管投递
	AutoMount func(content string) bool
	// AutoMountForCat 每猫端口 Chat 消息自动挂载（nil = 不挂载）——返回 (已挂载, 前文≤1 可带消息重建)
	AutoMountForCat func(catID string, sess *session.Session, content string) (bool, bool)
	// AutoMountOnCatChat cat.chat 投递前自动挂载（nil = 不挂载）——只落盘不重建
	AutoMountOnCatChat func(catID, content string)
	// Apply 猫配置生效面（cat.cfg.set / catcfg.apply 落盘后调用；nil = 只落盘不生效）
	Apply func(catKey string, cfg *config.CatCfg)
	// ValidateToolNames 工具名单校验（池外名剔除；nil = 原样保留）
	ValidateToolNames func(raw string) (string, []string)
	// ValidateEnabledRoots 根白名单校验（workspace 强制 + 池子集）
	ValidateEnabledRoots func(ids []string) (kept, removed []string, poolUnavailable bool)
	// FilterInjectPaths 注入路径过滤（nil = 按 config.IsValidInjectPath 过滤）
	FilterInjectPaths func(items []string) []string
	// pendingMu 保护 pendingRebuild
	pendingMu sync.Mutex
	// pendingRebuild 挂起的会话重建请求（cat key → 请求；Idle 时消费，轮内不重建）
	pendingRebuild map[string]pendingRebuildRequest
}

// pendingRebuildRequest 是挂起的会话重建请求——重复登记不覆盖首次请求（C# ChatSession._pendingRebuild 同语义）。
type pendingRebuildRequest struct {
	// pending 是否有挂起请求
	pending bool
	// first 重建后注入的初始消息（空 = 只重建）
	first string
	// origin 初始消息来源锚（组派单用 group:<组>:<seq>:delegation）
	origin string
}

// log 出声（nil 安全）。
func (r *commandRouter) log(level int, message, category string) {
	if r.Logf != nil {
		r.Logf(level, message, category)
	}
}

// Handle 处理一条指令——true = 已受理；false = 未识别（调用方回「指令未识别」）。
func (r *commandRouter) Handle(text string) bool {
	// 停机态——一切新输入 / 重建请求拒收（自动挂载也一并挡住：重建会冲掉在途收尾）
	if session.HostRestarting() {
		r.log(2, "宿主重启中——本条输入未受理", "RESTART")
		return false
	}
	switch {
	case strings.HasPrefix(text, "Chat "):
		// 对话输入路由——投递目标会话（与 C# InputRoutes 首项同形）
		// 「上工/开工 <项目>」先尝试自动挂载：前文 ≤1 条时挂载 + 带消息重建接管投递，
		// 否则只落盘挂载、消息按普通输入投递（对齐 C# RouteChat / TryAutoMountOnMessage）
		content := strings.TrimSpace(strings.TrimPrefix(text, "Chat "))
		if content == "" {
			return false
		}
		if r.AutoMount != nil && r.AutoMount(content) {
			return true
		}
		return r.Session.PostUser(content, "user", "", "")
	case text == "cat.list":
		return r.catList()
	case text == "cat.info":
		return r.catInfo()
	case text == "session.new":
		return r.sessionNew("")
	case strings.HasPrefix(text, sessionNewPrefix):
		return r.sessionNew(strings.TrimSpace(strings.TrimPrefix(text, sessionNewPrefix)))
	case strings.HasPrefix(text, catCfgGetPrefix):
		return r.catCfgGetCommand(strings.TrimSpace(strings.TrimPrefix(text, catCfgGetPrefix)))
	case strings.HasPrefix(text, catCfgSetPrefix):
		return r.catCfgSetCommand(strings.TrimSpace(strings.TrimPrefix(text, catCfgSetPrefix)))
	case strings.HasPrefix(text, "cat.") || strings.HasPrefix(text, "catcfg."):
		// 管理指令族（cat.* / catcfg.*）——与工具桥同一执行体（C# InputRoutes 段6 RouteAdminLine）
		result := r.Execute(text)
		level := 1
		if strings.HasPrefix(result, "ERR|") {
			level = 2
		}
		r.log(level, "cat 指令: "+result, "CMD")
		return true
	case strings.HasPrefix(text, "delay."):
		// 延迟指令族——任意线程直执（C# InputRoutes 段7 RouteDelayCommand）
		return r.HandleDelay(text)
	}
	return false
}

// HandleFor 按猫处理一条指令——每猫独立对话端口（d-1b）的指令面。
//
// 形态对齐 C# `HttpHostOptions.Dispatcher = line => DispatchInput(CatInputTarget(cat), line)`：
// `Chat ` / `session.new` 落该猫会话；`cat.*` / `catcfg.*` / `delay.*` 与管理面同一执行体（不另写一份）。
// 「上工/开工 <项目>」按猫自动挂载（d-1 遗留收口）——前文 ≤1 条时挂载 + 带消息重建接管投递，
// 已有对话则只落盘、消息按普通输入投递（对齐 C# RouteChat → TryAutoMountOnMessage 按猫目标版）。
func (r *commandRouter) HandleFor(catID string, sess *session.Session, text string) bool {
	if session.HostRestarting() {
		r.log(2, "宿主重启中——本条输入未受理（cat="+catID+"）", "RESTART")
		return false
	}
	switch {
	case strings.HasPrefix(text, "Chat "):
		content := strings.TrimSpace(strings.TrimPrefix(text, "Chat "))
		if content == "" || sess == nil {
			return false
		}
		if r.AutoMountForCat != nil {
			if mounted, fresh := r.AutoMountForCat(catID, sess, content); mounted && fresh {
				return r.sessionNewFor(catID, sess, content, "")
			}
		}
		return sess.PostUser(content, "user", "", "")
	case text == "session.new":
		return r.sessionNewFor(catID, sess, "", "")
	case strings.HasPrefix(text, sessionNewPrefix):
		return r.sessionNewFor(catID, sess, strings.TrimSpace(strings.TrimPrefix(text, sessionNewPrefix)), "")
	}
	return r.Handle(text)
}

// sessionNew 执行默认猫会话重建——重读 cat.cfg → 重算声明面 → 构建注入面 → 清前文 / 视图 / 统计。
//
// 顺序对齐 C# ChatBridge.HandleSessionNew：声明面 → 注入提示词 → 清前文 → 留档 → 清视图 → 注入报告 → 重置事件。
func (r *commandRouter) sessionNew(firstMessage string) bool {
	return r.sessionNewFor(r.CatID, r.Session, firstMessage, "")
}

// sessionNewFor 执行指定猫的会话重建（带初始消息来源锚）——origin 随初始消息投递（组派单用 group:<组>:<seq>:delegation）。
func (r *commandRouter) sessionNewFor(catID string, sess *session.Session, firstMessage, origin string) bool {
	if sess == nil {
		return false
	}
	cfg := r.loadCfgFor(catID)
	specs := r.rebuildSpecsFor(catID, sess, cfg)
	prompt, report, count := r.buildInjectSurface(cfg, specs)
	summary := sess.Rebuild(session.SessionNewOptions{
		DisplayName:      cfg.DisplayName,
		ToolSpecs:        specs,
		SystemPrompt:     prompt,
		InjectReportJSON: report,
		InjectCount:      count,
	})
	if firstMessage != "" {
		if sess.PostUser(firstMessage, "user", "", origin) {
			summary += " · 已注入初始消息 " + strconv.Itoa(utf8.RuneCountInString(firstMessage)) + " 字"
		} else {
			summary += " · 初始消息未受理"
			r.log(2, "会话重建后初始消息未受理: cat="+catID, "CHAT")
		}
	}
	r.log(1, summary, "CHAT")
	return true
}

// loadCfgFor 读取目标猫配置——失败按默认值 + 出声（不阻断重建）。
func (r *commandRouter) loadCfgFor(catID string) *config.CatCfg {
	path := filepath.Join(r.DataRoot, "sessions", catID, "cat.cfg")
	cfg, err := config.LoadCatCfg(path)
	if err != nil {
		r.log(2, "session.new 读取 cat.cfg 失败（按默认值）: "+err.Error(), "CHAT")
		return &config.CatCfg{ID: catID, DisplayName: catID, MountAuto: true, ProjectMountExpert: true}
	}
	if cfg.ID == "" {
		cfg.ID = catID
	}
	return cfg
}

// rebuildSpecsFor 重算声明面——授权面按最新名单实时刷新，声明面按名单裁剪。
func (r *commandRouter) rebuildSpecsFor(catID string, sess *session.Session, cfg *config.CatCfg) []session.ToolSpec {
	authorized := applySessionAuth(r.Registry, sess, catID, cfg.ToolNames)
	defs := r.Registry.FilterToolSpecs(authorized, toolpool.IsDefaultCatKey(catID))
	specs := make([]session.ToolSpec, 0, len(defs))
	for _, def := range defs {
		specs = append(specs, session.ToolSpec{Name: def.Name, Description: def.Description, ParametersJSON: def.ParametersJSON})
	}
	return specs
}

// buildInjectSurface 构建注入面——提示词 + 注入报告 JSON + 清单条数。
//
// baseRole 三路：cat-default.cfg baseRole → 内置 FallbackBaseRole；persona / injectList 取 cat.cfg。
func (r *commandRouter) buildInjectSurface(cfg *config.CatCfg, specs []session.ToolSpec) (prompt, report string, count int) {
	svc := &fs.Service{Roots: fsRoots(r.Workspace)}
	resolve := func(spec string) (string, error) { return svc.Resolve(spec, false) }
	readText := func(abs string) (string, error) { return svc.ReadTextAuto(abs) }
	logf := inject.Logf(r.Logf)
	baseRole := config.FallbackBaseRole
	if tpl, err := config.LoadCatDefault(filepath.Join(r.DataRoot, "config", "cat-default.cfg")); err == nil && tpl != nil {
		baseRole = strings.TrimSpace(tpl.BaseRole)
	}
	result := inject.BuildPrompt(resolve, readText, baseRole, cfg.Persona, cfg.InjectList, logf)
	mountDesc := inject.ProjectSummaryOf(resolve, cfg.ProjectMount, logf)
	report = inject.BuildReportJSON(result.Files, cfg.InjectList, specs, r.Pool.BuildOwnerFlowMap(),
		cfg.ProjectMount, cfg.ProjectMountExpert, cfg.MountAuto, mountDesc)
	return result.Prompt, report, len(cfg.InjectList)
}

// applySessionAuth 重算会话授权面——名单落盘即改本次运行的可调工具集（注入面仍新会话刷新）。
//
// 返回授权名集（调用方据此裁剪声明面）。三判定与 C# IsToolAllowed 同序：特权名 → 注册表 → 名单。
func applySessionAuth(reg *toolpool.Registry, sess *session.Session, catKey, toolNames string) []string {
	includePrivileged := toolpool.IsDefaultCatKey(catKey)
	authorized := reg.ResolveToolNames(toolNames, includePrivileged)
	allowed := make(map[string]bool, len(authorized))
	for _, name := range authorized {
		allowed[name] = true
	}
	sess.SetAuthorizer(func(name string) bool {
		if reg.IsPrivilegedName(name) && !sess.IsDefaultSession() {
			return false
		}
		if _, ok := reg.Find(name); !ok {
			return false
		}
		return allowed[name]
	})
	return authorized
}

// catEntries 全猫条目（含默认猫）——注册表存在时取实况；nil 回落单猫语义（测试 / 未接线场景）。
func (r *commandRouter) catEntries() []*catInstance {
	if r.Cats != nil {
		return r.Cats.entries()
	}
	return []*catInstance{{
		id:          r.CatID,
		displayName: r.catDisplayName(),
		session:     r.Session,
		running:     r.Running,
		port:        r.Port,
	}}
}

// catInstanceOf 单猫寻址（含默认猫）——nil 回落单猫语义（majordomo / 本会话 key / 默认会话显示名命中）。
func (r *commandRouter) catInstanceOf(key string) *catInstance {
	if r.Cats != nil {
		return r.Cats.findAny(key)
	}
	if key == "majordomo" || key == r.CatID || (r.Session != nil && key == r.Session.DisplayNameSnapshot()) {
		return &catInstance{id: r.CatID, displayName: r.catDisplayName(), session: r.Session, running: r.Running, port: r.Port}
	}
	return nil
}

// catStateText 运行态文本——「静默」/「运行中 :<port>」/「运行中」（port=0 = 无独立监听）。
func catStateText(inst *catInstance) string {
	if !inst.running {
		return "静默"
	}
	if inst.port > 0 {
		return "运行中 :" + strconv.Itoa(inst.port)
	}
	return "运行中"
}

// catList 输出全猫清单——对齐 C# HandleCatList（主干前置 + 多猫注册表）。
func (r *commandRouter) catList() bool {
	r.log(1, "cat 指令: "+r.buildCatListText(), "CMD")
	return true
}

// buildCatListText 构建全猫清单文本（cat.list 指令与 host.command 共用同一实现）。
//
// 行分隔用 CRLF：C# Environment.NewLine 在 Windows 上即 \r\n——日志回执逐字对齐按此判。
func (r *commandRouter) buildCatListText() string {
	entries := r.catEntries()
	var sb strings.Builder
	sb.WriteString("cat.list | " + strconv.Itoa(len(entries)) + " 只猫")
	sb.WriteString("\r\n")
	for i, inst := range entries {
		if i > 0 {
			sb.WriteString("\r\n")
		}
		sb.WriteString("  " + inst.id + " | " + inst.displayName + " | " + catStateText(inst))
		if toolpool.IsDefaultCatKey(inst.id) {
			sb.WriteString(" | 主干会话（禁停禁删）")
		}
	}
	return sb.String()
}

// catInfo 输出全猫状态统计——对齐 C# HandleCatInfo（整块分类 JSON：缩进 + 中文直显 + CRLF）。
//
// 字段面 = 运行面六键（id/name/type/special/running/port）+ 会话态全量
// （Session.StateEntry 单一投影源，与快照 sessions 段同源）。
func (r *commandRouter) catInfo() bool {
	r.log(1, "cat 指令: "+r.buildCatInfoJSON(), "CMD")
	return true
}

// buildCatInfoJSON 构建全猫状态统计 JSON（cat.info 指令与 host.catinfo 共用同一实现）。
func (r *commandRouter) buildCatInfoJSON() string {
	entries := r.catEntries()
	list := make([]catInfoEntry, 0, len(entries))
	for _, inst := range entries {
		list = append(list, r.buildCatInfoEntry(inst))
	}
	payload := catInfoResponse{
		OK:    true,
		Tool:  "cat.info",
		Time:  catInfoTime{Now: time.Now().Format("2006-01-02 15:04:05")},
		Count: len(list),
		Cats:  list,
	}
	return indentedJSON(payload)
}

// buildCatInfoEntry 构建单猫状态条目——cat.info / group-catinfo / GET /api/v1/cats 同源
// （对齐 C# BuildCatInfoEntry 单一实现：会话缺失只输出运行面字段，不补空值）。
func (r *commandRouter) buildCatInfoEntry(inst *catInstance) catInfoEntry {
	entry := catInfoEntry{
		ID:      inst.id,
		Name:    inst.displayName,
		Type:    inst.typ,
		Special: toolpool.IsDefaultCatKey(inst.id),
		Running: inst.running,
		Port:    inst.port,
	}
	if inst.session == nil {
		return entry
	}
	state := inst.session.StateEntry()
	runMs := map[string]int64{}
	if table, ok := state["runMs"].(map[string]int64); ok {
		runMs = table
	}
	entry.Phase = valueText(state["phase"])
	entry.RunState = valueText(state["runState"])
	entry.RunMs = catInfoRunMs{
		Idle:  0,
		Wait:  runMs["wait"],
		Link:  runMs["link"],
		Think: runMs["think"],
		Tool:  runMs["tool"],
		Run:   runMs["run"],
		Reply: runMs["reply"],
	}
	entry.Requests = valueInt64(state["requests"])
	entry.Round = valueInt64(state["round"])
	entry.MsgCount = valueInt(state["msgCount"])
	entry.Pending = valueInt(state["pending"])
	entry.NoteActive = valueBool(state["noteActive"])
	entry.ContextCount = valueInt(state["contextCount"])
	entry.Context = valueInt64(state["context"])
	entry.ContextChars = valueInt64(state["contextChars"])
	entry.LastActiveAt = valueInt64(state["lastActiveAt"])
	entry.SessionPrompt = valueInt64(state["sessionPrompt"])
	entry.SessionCompletion = valueInt64(state["sessionCompletion"])
	entry.SessionCacheHit = valueInt64(state["sessionCacheHit"])
	entry.SessionMiss = valueInt64(state["sessionMiss"])
	entry.SessionRate = valueFloat(state["sessionRate"])
	return entry
}

// catInfoTime 是 cat.info 的 time 段（对齐 C# new { now = DateTime.Now.ToString(...) }）。
type catInfoTime struct {
	// Now 当前时刻（yyyy-MM-dd HH:mm:ss）
	Now string `json:"now"`
}

// catInfoRunMs 是 runMs 段——七态累计毫秒（idle 恒 0，空闲不计时）。
type catInfoRunMs struct {
	// Idle 空闲
	Idle int64 `json:"idle"`
	// Wait 等待
	Wait int64 `json:"wait"`
	// Link 连接
	Link int64 `json:"link"`
	// Think 思考
	Think int64 `json:"think"`
	// Tool 工具
	Tool int64 `json:"tool"`
	// Run 运行
	Run int64 `json:"run"`
	// Reply 回复
	Reply int64 `json:"reply"`
}

// catInfoEntry 是单猫状态条目——运行面六键 + 会话态十七键（键序对齐 C# BuildCatInfoEntry + BuildStateEntry）。
type catInfoEntry struct {
	// ID 猫 key
	ID string `json:"id"`
	// Name 显示名（主干恒 majordomo）
	Name string `json:"name"`
	// Type 猫类型（主干空串）
	Type string `json:"type"`
	// Special 主干特殊会话标记（禁停禁删）
	Special bool `json:"special"`
	// Running 运行态
	Running bool `json:"running"`
	// Port 监听端口（0 = 未监听）
	Port int `json:"port"`
	// Phase 粗粒度轮相位
	Phase string `json:"phase"`
	// RunState 细粒度运行态
	RunState string `json:"runState"`
	// RunMs 七态累计毫秒
	RunMs catInfoRunMs `json:"runMs"`
	// Requests 本轮 API 请求次数
	Requests int64 `json:"requests"`
	// Round 当前工具轮次
	Round int64 `json:"round"`
	// MsgCount 前文条数
	MsgCount int `json:"msgCount"`
	// Pending 待处理队列条数
	Pending int `json:"pending"`
	// NoteActive Note 计划是否在跑
	NoteActive bool `json:"noteActive"`
	// ContextCount 前文条数（与快照同源）
	ContextCount int `json:"contextCount"`
	// Context 已知最新前文长度（请求级实时优先）
	Context int64 `json:"context"`
	// ContextChars 前文总字符数
	ContextChars int64 `json:"contextChars"`
	// LastActiveAt 最近前文变动时刻（Unix 毫秒）
	LastActiveAt int64 `json:"lastActiveAt"`
	// SessionPrompt 会话级输入 token 累计
	SessionPrompt int64 `json:"sessionPrompt"`
	// SessionCompletion 会话级输出 token 累计
	SessionCompletion int64 `json:"sessionCompletion"`
	// SessionCacheHit 会话级缓存命中累计
	SessionCacheHit int64 `json:"sessionCacheHit"`
	// SessionMiss 会话级未命中累计
	SessionMiss int64 `json:"sessionMiss"`
	// SessionRate 会话级缓存命中率（0..1）
	SessionRate float64 `json:"sessionRate"`
}

// catInfoResponse 是 cat.info 的整块返回体（键序对齐 C# ok / tool / time / count / cats）。
type catInfoResponse struct {
	// OK 恒 true
	OK bool `json:"ok"`
	// Tool 工具名
	Tool string `json:"tool"`
	// Time 当前时刻段
	Time catInfoTime `json:"time"`
	// Count 猫数量
	Count int `json:"count"`
	// Cats 每猫状态条目
	Cats []catInfoEntry `json:"cats"`
}

// indentedJSON 序列化 info 系返回体——2 空格缩进 + 中文直显 + CRLF（对齐 C# InfoJsonOptions）。
//
// 尾换行剥除（JsonSerializer.Serialize 不留尾换行）；结构换行转 CRLF（实测 C# 输出为 \r\n）。
func indentedJSON(v any) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(v); err != nil {
		return "null"
	}
	text := strings.TrimSuffix(buf.String(), "\n")
	return strings.ReplaceAll(text, "\n", "\r\n")
}

// valueText 会话态字段取值——字符串断言（非字符串 = 空串）。
func valueText(v any) string {
	text, _ := v.(string)
	return text
}

// valueInt64 会话态字段取值——整型断言（int / int64 / float64 三态）。
func valueInt64(v any) int64 {
	switch value := v.(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	}
	return 0
}

// valueInt 会话态字段取值——int 形态（int / int64 两态）。
func valueInt(v any) int {
	return int(valueInt64(v))
}

// valueBool 会话态字段取值——布尔断言（非布尔 = false）。
func valueBool(v any) bool {
	flag, _ := v.(bool)
	return flag
}

// valueFloat 会话态字段取值——浮点断言（int / int64 / float64 三态）。
func valueFloat(v any) float64 {
	switch value := v.(type) {
	case float64:
		return value
	case int:
		return float64(value)
	case int64:
		return float64(value)
	}
	return 0
}

// catsJSON 构建 GET /api/v1/cats 响应体——对齐 C# BuildCatsJson（version + cats[]；主干前置）。
//
// 条目 = catInfoEntry 全字段 + 行内编辑三字段（apiConfigId / enabledRoots / reasoningEffort）；
// enabledRoots 空表恒输出 []（C# string[0] 同形，非 null）。紧凑单行 + 中文直出（JsonUtil.Serialize 同形）。
func (r *commandRouter) catsJSON() string {
	entries := r.catEntries()
	list := make([]catsEntry, 0, len(entries))
	for _, inst := range entries {
		name := inst.displayName
		apiConfigID := ""
		enabledRoots := []string{}
		reasoningEffort := ""
		path := filepath.Join(r.DataRoot, "sessions", inst.id, "cat.cfg")
		if cfg, err := config.LoadCatCfg(path); err == nil && cfg != nil {
			if cfg.DisplayName != "" {
				name = cfg.DisplayName
			}
			apiConfigID = cfg.APIConfigID
			if cfg.EnabledRoots != nil {
				enabledRoots = cfg.EnabledRoots
			}
			reasoningEffort = cfg.ReasoningEffort
		}
		entry := catsEntry{
			catInfoEntry:    r.buildCatInfoEntry(inst),
			APIConfigID:     apiConfigID,
			EnabledRoots:    enabledRoots,
			ReasoningEffort: reasoningEffort,
		}
		entry.Name = name
		list = append(list, entry)
	}
	return compactJSON(catsResponse{Version: 1, Cats: list})
}

// catsEntry 是单猫列表条目——状态条目全字段 + 行内编辑三字段（键序对齐 C# BuildCatsJson 追加载荷）。
type catsEntry struct {
	catInfoEntry
	// APIConfigID LLM API 配置身份（空 = 跟随全局默认）
	APIConfigID string `json:"apiConfigId"`
	// EnabledRoots 启用受控根清单（空 = 空白名单；与 cat-config 同口径）
	EnabledRoots []string `json:"enabledRoots"`
	// ReasoningEffort 思考强度覆盖（空 = 跟随全局）
	ReasoningEffort string `json:"reasoningEffort"`
}

// catsResponse 是 GET /api/v1/cats 的返回体（键序 version / cats）。
type catsResponse struct {
	// Version 响应版本（恒 1）
	Version int `json:"version"`
	// Cats 猫列表条目
	Cats []catsEntry `json:"cats"`
}

// compactJSON 序列化紧凑单行 JSON——中文直出 + 不转义 HTML 字符（对齐 C# JsonUtil.Serialize）。
func compactJSON(v any) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return "null"
	}
	return strings.TrimRight(buf.String(), "\r\n")
}

// fsRoots 受控根配置 → fs 服务根表。
func fsRoots(workspace *config.WorkspaceConfig) []fs.Root {
	if workspace == nil {
		return nil
	}
	out := make([]fs.Root, 0, len(workspace.Roots))
	for _, root := range workspace.Roots {
		out = append(out, fs.Root{ID: root.ID, Path: root.Path, Writable: root.Writable})
	}
	return out
}

// ── cat.cfg.* 指令（每猫配置读写——catcfg.bridge 的宿主指令服务面）──
//
// 语义逐条对齐 mau:CatHome4/CatHome4.Admin/CatHostService.Cats.cs 的 HandleCatCfgGet / HandleCatCfgSet：
// 读面 FormatCatCfg 字段序全量输出（不掩码——cat.cfg 不含密钥）；写面字段级合并写（读现值 → 改单字段 → 全量写回）
// → 落盘 → 生效 → 写后读回对照（回执取落盘实况，不是「已提交」）。

// Execute 执行一条宿主管理指令并返回回执文本（host.command 积木的宿主指令服务面）。
//
// 三面同源（CLI / HTTP 面板 / 工具桥）——前缀白名单 cat.* / catcfg.*（越界 ERR|BAD_PREFIX）；
// 停机态拒收（ERR|HOST_RESTARTING）；内核分派逐条对齐 C# CatHostService.HandleCatCommand
// （单猫形态：majordomo 可寻址，其余 key 按「未找到猫」；多猫面归 P5 d-1）。
func (r *commandRouter) Execute(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "ERR|BAD_ARGS|指令为空"
	}
	if !strings.HasPrefix(trimmed, "cat.") && !strings.HasPrefix(trimmed, "catcfg.") {
		return "ERR|BAD_PREFIX|仅接受 cat.* / catcfg.* 指令（收到: " + trimmed + "）"
	}
	if session.HostRestarting() {
		return "ERR|HOST_RESTARTING|宿主重启中——指令未受理: " + trimmed
	}
	switch {
	case trimmed == "cat.list":
		return r.buildCatListText()
	case trimmed == "cat.info":
		return r.buildCatInfoJSON()
	case trimmed == "cat.new" || strings.HasPrefix(trimmed, "cat.new "):
		return r.catNewCommand(strings.TrimSpace(strings.TrimPrefix(trimmed, "cat.new")))
	case strings.HasPrefix(trimmed, "cat.start "):
		return r.catStartCommand(strings.TrimSpace(strings.TrimPrefix(trimmed, "cat.start ")))
	case strings.HasPrefix(trimmed, "cat.stop "):
		return r.catStopCommand(strings.TrimSpace(strings.TrimPrefix(trimmed, "cat.stop ")))
	case strings.HasPrefix(trimmed, "cat.delete "):
		return r.catDeleteCommand(strings.TrimSpace(strings.TrimPrefix(trimmed, "cat.delete ")))
	case strings.HasPrefix(trimmed, "cat.session.new "):
		return r.catSessionNewCommand(strings.TrimSpace(strings.TrimPrefix(trimmed, "cat.session.new ")))
	case strings.HasPrefix(trimmed, "cat.pause"):
		return r.catPauseCommand(strings.TrimSpace(strings.TrimPrefix(trimmed, "cat.pause")))
	case strings.HasPrefix(trimmed, "cat.continue"):
		return r.catContinueCommand(strings.TrimSpace(strings.TrimPrefix(trimmed, "cat.continue")))
	case strings.HasPrefix(trimmed, "cat.chat "):
		return r.catChatCommand(strings.TrimSpace(strings.TrimPrefix(trimmed, "cat.chat ")))
	case strings.HasPrefix(trimmed, "cat.new-session "):
		return r.catNewSessionCommand(strings.TrimSpace(strings.TrimPrefix(trimmed, "cat.new-session ")))
	case strings.HasPrefix(trimmed, catCfgGetPrefix):
		return r.catCfgGet(strings.TrimSpace(strings.TrimPrefix(trimmed, catCfgGetPrefix)))
	case strings.HasPrefix(trimmed, catCfgSetPrefix):
		return r.catCfgSet(strings.TrimSpace(strings.TrimPrefix(trimmed, catCfgSetPrefix)))
	case strings.HasPrefix(trimmed, "catcfg.apply "):
		return r.catCfgApply(strings.TrimSpace(strings.TrimPrefix(trimmed, "catcfg.apply ")))
	}
	return "cat.* 指令未识别: " + trimmed + "\n" + catCommandUsage()
}

// catCommandUsage cat.* 指令族用法清单（错 key 自解释——对齐 C# CatCommandUsage）。
func catCommandUsage() string {
	lines := []string{
		"cat.* / catcfg.* 可用指令:",
		"  cat.list | 全部猫（id/显示名/运行态:端口）",
		"  cat.info | 全猫状态统计（运行态/端口/流式态/前文长度/最近活跃——整块 JSON）",
		"  cat.new <显示名> | 新建猫（静默态，待 cat.start）",
		"  cat.start <key> | 分配端口 + 拉起 HttpHost",
		"  cat.stop <key> | 停止（majordomo 禁停）",
		"  cat.delete <key> | 销毁（majordomo 禁删）",
		"  cat.pause <key> | 中止当前回合",
		"  cat.continue <key> | 继续（当前前文再发一次请求）",
		"  cat.chat <key> <内容> | 内核对话投递",
		"  cat.new-session <key> | 按猫请求新会话（清前文 + 重新注入）",
		"  cat.cfg.get <key> | 该猫 cat.cfg 全量字段",
		"  cat.cfg.set <key> <字段> <值> | 字段级合并写（未提交字段保留）",
		"  catcfg.apply <key> | 每猫配置运行时生效",
	}
	return strings.Join(lines, "\r\n")
}

// catNewCommand cat.new——建猫（ticks id + 默认命名 + 模板继承 + 会话装配；静默态待 cat.start）。
func (r *commandRouter) catNewCommand(name string) string {
	if r.Cats == nil {
		return "ERR|NOT_IMPLEMENTED|多猫面未接线（cat.new）"
	}
	inst, errText := r.Cats.createCat(name)
	if errText != "" {
		return errText
	}
	return "cat.new | id=" + inst.id + " | name=" + inst.displayName + " | 静默态（cat.start 启动）"
}

// catStartCommand cat.start——按猫寻址启动会话（对齐 C# HandleCatStart 的会话层语义）。
//
// 形态差异：Go 单端口设计下无每猫独立对话端口（port=0），见 L3 A 域「单进程单端口」。
func (r *commandRouter) catStartCommand(key string) string {
	if key == "" {
		return "cat.start | 用法: cat.start <key>"
	}
	if r.Cats != nil {
		return r.Cats.startCat(key)
	}
	if key != r.CatID {
		return "cat.start | 未找到猫: " + key
	}
	if r.Running {
		return "cat.start | 已在运行: " + r.catDisplayName() + " :" + strconv.Itoa(r.Port)
	}
	return "ERR|NOT_IMPLEMENTED|多猫面未接线（cat.start）"
}

// catStopCommand cat.stop——多猫注册表停止（majordomo 禁停）。
func (r *commandRouter) catStopCommand(key string) string {
	if key == "" {
		return "cat.stop | 用法: cat.stop <key>"
	}
	if r.Cats != nil {
		return r.Cats.stopCat(key)
	}
	if key == "majordomo" || key == r.CatID {
		return "cat.stop | majordomo 为特殊会话——强制自启，不可停止"
	}
	return "cat.stop | 未找到猫: " + key
}

// catDeleteCommand cat.delete——多猫注册表销毁（majordomo 禁删）。
func (r *commandRouter) catDeleteCommand(key string) string {
	if key == "" {
		return "cat.delete | 用法: cat.delete <key>"
	}
	if r.Cats != nil {
		return r.Cats.deleteCat(key)
	}
	if key == "majordomo" || key == r.CatID {
		return "cat.delete | majordomo 为特殊会话——不可删除"
	}
	return "cat.delete | 未找到猫: " + key
}

// catPauseCommand cat.pause——按猫投递中止（majordomo 不可自查）。
func (r *commandRouter) catPauseCommand(key string) string {
	if key == "" {
		return "cat.pause | 用法: cat.pause <key>"
	}
	if key == "majordomo" {
		return "cat.pause | majordomo 为特殊会话——不可自查（主干会话不能对自己下中止指令）"
	}
	inst := r.catInstanceOf(key)
	if inst == nil {
		return "cat.pause | 未找到猫: " + key
	}
	if inst.session == nil {
		return "cat.pause | 猫已停机（无会话）: " + inst.displayName
	}
	inst.session.RequestPause()
	return "cat.pause | 已投递中止: " + inst.displayName
}

// catContinueCommand cat.continue——按猫投递继续（majordomo 不可自查）。
func (r *commandRouter) catContinueCommand(key string) string {
	if key == "" {
		return "cat.continue | 用法: cat.continue <key>"
	}
	if key == "majordomo" {
		return "cat.continue | majordomo 为特殊会话——不可自查（主干会话不能对自己下继续指令）"
	}
	inst := r.catInstanceOf(key)
	if inst == nil {
		return "cat.continue | 未找到猫: " + key
	}
	if inst.session == nil {
		return "cat.continue | 猫已停机（无会话）: " + inst.displayName
	}
	inst.session.RequestContinue()
	return "cat.continue | 已投递继续: " + inst.displayName
}

// catDisplayName 显示名兜底（C# cat.DisplayName 同口径）。
func (r *commandRouter) catDisplayName() string {
	if r.DisplayName != "" {
		return r.DisplayName
	}
	return r.CatID
}

// catSessionNewCommand 跨猫会话重建（C# RouteAdminLine 的 cat.session.new 分支）——目标须空闲。
func (r *commandRouter) catSessionNewCommand(rest string) string {
	body := strings.TrimSpace(rest)
	if body == "" {
		return "cat.session.new | 用法: cat.session.new <key> [初始消息]"
	}
	key := body
	first := ""
	if sp := strings.IndexByte(body, ' '); sp >= 0 {
		key = strings.TrimSpace(body[:sp])
		first = strings.TrimSpace(body[sp+1:])
	}
	result := r.rebuildCatSession(key, first, "")
	if !strings.HasPrefix(result, "ok|") {
		return "cat.session.new | " + result
	}
	tail := "（无初始消息）"
	if first != "" {
		tail = "（初始消息 " + strconv.Itoa(utf16Length(first)) + " 字）"
	}
	return "cat.session.new | 已重建会话: " + key + tail
}

// catChatCommand cat.chat——按猫投递对话（majordomo 不可自查）。
//
// 投递前自动挂载（C# CatHostService.Cats.cs:383 → AdminService.AutoMountForCat）：
// 「上工/开工 <项目>」命中原样挂载并落盘，当前会话不动（清单下个新会话生效）；失败出声，不影响投递。
func (r *commandRouter) catChatCommand(rest string) string {
	space := strings.IndexByte(rest, ' ')
	if space <= 0 {
		return "cat.chat | 用法: cat.chat <key> <内容>"
	}
	key := strings.TrimSpace(rest[:space])
	content := strings.TrimSpace(rest[space+1:])
	if content == "" {
		return "cat.chat | 内容不能为空"
	}
	if key == "majordomo" {
		return "cat.chat | majordomo 为特殊会话——不可自查（主干会话不能对自己投递对话）"
	}
	inst := r.catInstanceOf(key)
	if inst == nil {
		return "cat.chat | 未找到猫: " + key
	}
	if inst.session == nil {
		return "cat.chat | 猫已停机（无会话）: " + inst.displayName
	}
	if r.AutoMountOnCatChat != nil {
		r.AutoMountOnCatChat(inst.id, content)
	}
	inst.session.PostUser(content, "user", "", "")
	return "cat.chat | 已投递: " + inst.displayName
}

// catNewSessionCommand 按猫请求新会话——置待消费标志（Idle 时重建，轮内不重建）。
func (r *commandRouter) catNewSessionCommand(key string) string {
	if key == "" {
		return "cat.new-session | 用法: cat.new-session <key>"
	}
	if key == "majordomo" {
		r.requestSessionRebuild("majordomo", "", "")
		return "cat.new-session | 已请求新会话: majordomo"
	}
	inst := r.catInstanceOf(key)
	if inst == nil {
		return "cat.new-session | 未找到猫: " + key
	}
	if inst.session == nil {
		return "cat.new-session | 猫已停机（无会话）: " + inst.displayName
	}
	r.requestSessionRebuild(inst.id, "", "")
	return "cat.new-session | 已请求新会话: " + inst.displayName
}

// requestSessionRebuild 登记会话重建——轮末 / Idle 生效；重复登记返回 false（不覆盖首次请求）。
//
// 对齐 C# ChatSession.RequestSessionRebuild：首个请求的 first / origin 保留，后续登记一律拒绝。
func (r *commandRouter) requestSessionRebuild(catKey, firstMessage, origin string) bool {
	if catKey == "" {
		catKey = r.CatID
	}
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	if r.pendingRebuild == nil {
		r.pendingRebuild = map[string]pendingRebuildRequest{}
	}
	if _, exists := r.pendingRebuild[catKey]; exists {
		return false
	}
	r.pendingRebuild[catKey] = pendingRebuildRequest{pending: true, first: firstMessage, origin: origin}
	r.log(1, "会话重建已登记（轮末生效）: cat="+catKey, "CHAT")
	return true
}

// rebuildCatSession 跨猫会话重建——目标须空闲；供 Group 工具面与指令面共用。
//
// 对齐 C# AdminService.RebuildCatSession：寻址 → 空闲判据 → 重建（带初始消息与来源锚）。
func (r *commandRouter) rebuildCatSession(key, firstMessage, origin string) string {
	useKey := strings.TrimSpace(key)
	if useKey == "" {
		return "ERR|CAT_KEY_EMPTY|目标猫 key 不能为空"
	}
	inst := r.catInstanceOf(useKey)
	if inst == nil || inst.session == nil {
		return "ERR|CAT_OFFLINE|猫未运行: " + useKey
	}
	if !inst.session.IsIdle() {
		return "ERR|CAT_BUSY|猫忙——仅在空闲时可重建: " + useKey
	}
	r.sessionNewFor(inst.id, inst.session, firstMessage, origin)
	return "ok|" + useKey
}

// ConsumePendingNewSession 消费按猫新会话请求——仅 Idle 时执行（轮内不重建，避免冲掉在途回合）。
//
// 返回 true = 至少执行了一次重建。
func (r *commandRouter) ConsumePendingNewSession() bool {
	r.pendingMu.Lock()
	if len(r.pendingRebuild) == 0 {
		r.pendingMu.Unlock()
		return false
	}
	keys := make([]string, 0, len(r.pendingRebuild))
	for key := range r.pendingRebuild {
		keys = append(keys, key)
	}
	r.pendingMu.Unlock()
	consumed := false
	for _, key := range keys {
		inst := r.catInstanceOf(key)
		if inst == nil || inst.session == nil || !inst.session.IsIdle() {
			continue
		}
		r.pendingMu.Lock()
		pending, exists := r.pendingRebuild[key]
		if exists {
			delete(r.pendingRebuild, key)
		}
		r.pendingMu.Unlock()
		if !exists {
			continue
		}
		r.sessionNewFor(inst.id, inst.session, pending.first, pending.origin)
		consumed = true
	}
	return consumed
}

// catCfgApply catcfg.apply——重读 cat.cfg 并走与 cat.cfg.set 同一生效面（按猫寻址）。
func (r *commandRouter) catCfgApply(key string) string {
	if key == "" {
		return "catcfg.apply | 用法: catcfg.apply <key>"
	}
	inst := r.catInstanceOf(key)
	if inst == nil {
		return "catcfg.apply | 猫不存在: " + key
	}
	path := filepath.Join(r.DataRoot, "sessions", inst.id, "cat.cfg")
	cfg, err := config.LoadCatCfg(path)
	if err != nil || cfg == nil {
		return "catcfg.apply | cat.cfg 不存在: " + inst.id
	}
	if r.Apply != nil {
		r.Apply(inst.id, cfg)
	}
	return "catcfg.apply | " + inst.displayName + " | 已生效（前文项新会话生效）"
}

// catCfgGetCommand 指令入口（cat.cfg.get）——结果落 CMD 日志（与 cat.list / cat.info 同口径）。
func (r *commandRouter) catCfgGetCommand(key string) bool {
	r.log(1, "cat 指令: "+r.catCfgGet(key), "CMD")
	return true
}

// catCfgSetCommand 指令入口（cat.cfg.set）——结果落 CMD 日志。
func (r *commandRouter) catCfgSetCommand(rest string) bool {
	r.log(1, "cat 指令: "+r.catCfgSet(rest), "CMD")
	return true
}

// resolveCatCfgTarget 猫寻址 → cat.cfg 路径（多猫注册表；majordomo 亦按注册条目寻址）。
func (r *commandRouter) resolveCatCfgTarget(key string) (id, path, errText string) {
	if key == "" {
		return "", "", "缺少猫寻址键"
	}
	inst := r.catInstanceOf(key)
	if inst == nil {
		return "", "", "未找到猫: " + key + "（cat.list 查看全部）"
	}
	return inst.id, filepath.Join(r.DataRoot, "sessions", inst.id, "cat.cfg"), ""
}

// formatCatCfg cat.cfg 字段清单格式化——get / set 共用（写后读回对照用同一口径）。
// 行分隔 CRLF（C# StringBuilder.AppendLine 在 Windows 即 \r\n）；末行 themeColor 不带尾换行。
func formatCatCfg(id string, cfg *config.CatCfg) string {
	typeText := cfg.Type
	if typeText == "" {
		typeText = "（空）"
	}
	idleText := "false"
	if cfg.UserInjectIdleOnly {
		idleText = "true"
	}
	effortText := cfg.ReasoningEffort
	if effortText == "" {
		effortText = "（跟随全局）"
	}
	var sb strings.Builder
	sb.WriteString("cat.cfg | " + id + "\r\n")
	sb.WriteString("  id=" + cfg.ID + "\r\n")
	sb.WriteString("  displayName=" + cfg.DisplayName + "\r\n")
	sb.WriteString("  type=" + typeText + "\r\n")
	sb.WriteString("  apiConfigId=" + cfg.APIConfigID + "\r\n")
	sb.WriteString("  persona=" + cfg.Persona + "\r\n")
	sb.WriteString("  toolNames=" + cfg.ToolNames + "\r\n")
	sb.WriteString("  injectList=" + strings.Join(cfg.InjectList, ",") + "\r\n")
	sb.WriteString("  packs=" + strings.Join(cfg.Packs, ",") + "\r\n")
	sb.WriteString("  enabledRoots=" + strings.Join(cfg.EnabledRoots, ",") + "\r\n")
	sb.WriteString("  dbLibs=" + strings.Join(cfg.DBLibs, ",") + "\r\n")
	sb.WriteString("  userInjectIdleOnly=" + idleText + "\r\n")
	sb.WriteString("  appearanceId=" + cfg.AppearanceID + "\r\n")
	sb.WriteString("  reasoningEffort=" + effortText + "\r\n")
	sb.WriteString("  themeColor=" + strconv.Itoa(cfg.ThemeColor))
	return sb.String()
}

// catCfgGet cat.cfg.get——该猫 cat.cfg 全量字段；失败面以 "cat.cfg.get | " 前缀带回。
func (r *commandRouter) catCfgGet(key string) string {
	id, path, errText := r.resolveCatCfgTarget(key)
	if errText != "" {
		return "cat.cfg.get | " + errText
	}
	cfg, err := config.LoadCatCfg(path)
	if err != nil || cfg == nil {
		return "cat.cfg.get | cat.cfg 不存在: " + id
	}
	return formatCatCfg(id, cfg)
}

// catCfgSet cat.cfg.set——字段级合并写（未提交字段逐字保留）+ 生效 + 写后读回。
func (r *commandRouter) catCfgSet(rest string) string {
	if rest == "" {
		return "cat.cfg.set | 用法: cat.cfg.set <key> <字段> <值>"
	}
	sp1 := strings.IndexByte(rest, ' ')
	if sp1 <= 0 {
		return "cat.cfg.set | 用法: cat.cfg.set <key> <字段> <值>"
	}
	key := strings.TrimSpace(rest[:sp1])
	tail := rest[sp1+1:]
	sp2 := strings.IndexByte(tail, ' ')
	if sp2 <= 0 {
		return "cat.cfg.set | 用法: cat.cfg.set <key> <字段> <值>"
	}
	field := strings.TrimSpace(tail[:sp2])
	value := strings.TrimSpace(tail[sp2+1:])

	id, path, errText := r.resolveCatCfgTarget(key)
	if errText != "" {
		return "cat.cfg.set | " + errText
	}
	cfg, err := config.LoadCatCfg(path)
	if err != nil || cfg == nil {
		return "cat.cfg.set | cat.cfg 不存在: " + id
	}
	applied, immediate, errText := r.applyCatCfgField(cfg, field, value)
	if errText != "" {
		return "cat.cfg.set | " + errText
	}
	// [段2] 全量写回 + 生效面（与端点写面同源：授权面实时 + 端点绑定切换）
	if err := config.SaveCatCfg(path, cfg); err != nil {
		return "cat.cfg.set | " + id + " | " + applied + " 写入失败: " + err.Error()
	}
	if r.Apply != nil {
		r.Apply(id, cfg)
	}
	// [段3] 落盘实况——写后读回对照
	after, readErr := config.LoadCatCfg(path)
	if readErr != nil || after == nil {
		return "cat.cfg.set | " + id + " | " + applied + " 已写入，但读回失败（落盘异常——请核查 cat.cfg）"
	}
	var sb strings.Builder
	sb.WriteString("cat.cfg.set | " + id + " | " + applied + " 已更新\r\n")
	sb.WriteString("  [落盘] cat.cfg 已写回（未提交字段逐字保留）\r\n")
	if immediate {
		sb.WriteString("  [生效] 立即（LLM 端点已切换）\r\n")
	} else {
		sb.WriteString("  [生效] 前文项新会话生效（catcfg.apply 已入队）\r\n")
	}
	sb.WriteString(formatCatCfg(id, after))
	return sb.String()
}

// applyCatCfgField 单字段应用——白名单 + 类型 / 值域校验（非法一律报错退出，不静默回落）。
// 返回 (applied 文本, 是否立即生效, 错误文本)。
func (r *commandRouter) applyCatCfgField(cfg *config.CatCfg, field, value string) (string, bool, string) {
	switch field {
	case "displayName":
		if value == "" {
			return "", false, "displayName 不能为空"
		}
		cfg.DisplayName = value
		return "displayName=" + value, false, ""
	case "apiConfigId":
		trimmed := strings.TrimSpace(value)
		normalized := config.ZeroGUIDText
		if trimmed != "" && !config.IsZeroGUID(trimmed) {
			parsed := config.ParseGUID(trimmed)
			if parsed == "" {
				return "", false, "apiConfigId 非法: " + trimmed + "（空=默认端点）"
			}
			normalized = parsed
		}
		cfg.APIConfigID = normalized
		return "apiConfigId=" + normalized, true, ""
	case "type":
		cfg.Type = strings.TrimSpace(value)
		applied := "type=（已清空）"
		if cfg.Type != "" {
			applied = "type=" + cfg.Type
		}
		return applied, false, ""
	case "persona":
		cfg.Persona = value
		return "persona（" + strconv.Itoa(len(utf16.Encode([]rune(value)))) + " 字符）", false, ""
	case "toolNames":
		old := map[string]bool{}
		for _, name := range toolpool.SplitToolNameList(cfg.ToolNames) {
			old[name] = true
		}
		valid := value
		removed := []string{}
		if r.ValidateToolNames != nil {
			valid, removed = r.ValidateToolNames(value)
		}
		kept := make([]string, 0, 8)
		blocked := []string{}
		for _, name := range toolpool.SplitToolNameList(valid) {
			// 受限名单只拦新增（现值已持有的保留——一次无关字段保存不静默夺权）
			if toolpool.IsBlockedToolName(name) && !old[name] {
				blocked = append(blocked, name)
				continue
			}
			kept = append(kept, name)
		}
		cfg.ToolNames = strings.Join(kept, ",")
		applied := "toolNames=" + cfg.ToolNames
		if len(removed) > 0 {
			applied += "｜⚠️ 已剔除池外工具名 " + strconv.Itoa(len(removed)) + " 个：" + strings.Join(removed, ",")
		}
		if len(blocked) > 0 {
			applied += "｜⚠️ 已剔除受限工具名 " + strconv.Itoa(len(blocked)) + " 个：" + strings.Join(blocked, ",")
		}
		return applied, false, ""
	case "injectList":
		paths := splitCfgList(value)
		if r.FilterInjectPaths != nil {
			paths = r.FilterInjectPaths(paths)
		} else {
			kept := make([]string, 0, len(paths))
			for _, path := range paths {
				if config.IsValidInjectPath(path) {
					kept = append(kept, path)
				}
			}
			paths = kept
		}
		cfg.InjectList = paths
		return "injectList=" + strings.Join(paths, ","), false, ""
	case "packs":
		cfg.Packs = splitCfgList(value)
		return "packs=" + strings.Join(cfg.Packs, ","), false, ""
	case "dbLibs":
		cfg.DBLibs = splitCfgList(value)
		return "dbLibs=" + strings.Join(cfg.DBLibs, ","), false, ""
	case "userInjectIdleOnly":
		if strings.EqualFold(value, "true") {
			cfg.UserInjectIdleOnly = true
			return "userInjectIdleOnly=true", false, ""
		}
		if strings.EqualFold(value, "false") {
			cfg.UserInjectIdleOnly = false
			return "userInjectIdleOnly=false", false, ""
		}
		return "", false, "userInjectIdleOnly 非法值: " + value + "（true/false）"
	case "reasoningEffort":
		raw := strings.TrimSpace(value)
		normalized := config.NormalizeReasoningEffort(raw)
		if normalized == "" && raw != "" {
			return "", false, "reasoningEffort 非法值: " + value + "（low/high/max/空）"
		}
		cfg.ReasoningEffort = normalized
		applied := "reasoningEffort=（跟随全局）"
		if normalized != "" {
			applied = "reasoningEffort=" + normalized
		}
		return applied, false, ""
	case "appearanceId":
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || config.IsZeroGUID(trimmed) {
			cfg.AppearanceID = config.ZeroGUIDText
			return "appearanceId=（跟随默认外观）", false, ""
		}
		parsed := config.ParseGUID(trimmed)
		if parsed == "" {
			return "", false, "appearanceId 非法值: " + value + "（GUID 或空）"
		}
		cfg.AppearanceID = parsed
		return "appearanceId=" + parsed, false, ""
	case "themeColor":
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || parsed < 0 {
			return "", false, "themeColor 非法值: " + value + "（非负整数）"
		}
		cfg.ThemeColor = parsed
		return "themeColor=" + strconv.Itoa(parsed), false, ""
	}
	return "", false, "非法字段: " + field + "（可用: displayName/apiConfigId/persona/type/toolNames/injectList/packs/dbLibs/enabledRoots/userInjectIdleOnly/reasoningEffort/appearanceId/themeColor）"
}

// splitCfgList 逗号 / 空白分隔清单解析——去空项（对齐 C# SplitList：',', ' ', '\t'）。
func splitCfgList(value string) []string {
	return toolpool.SplitToolNameList(value)
}
