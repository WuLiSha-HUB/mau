package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
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
// `cat.list` / `cat.info` 状态查询（d3-b）。
// 未落地：session clear / session count（归 P3-d d3 尾部）、cat.new / start / stop / delete（多猫面）、delay.* 等。

// sessionNewPrefix session.new 带初始消息的前缀（含尾空格，C# line.Substring(12) 同口径）。
const sessionNewPrefix = "session.new "

// catCfgGetPrefix / catCfgSetPrefix 每猫配置指令前缀（含尾空格——对齐 C# line.Substring(12).Trim()）。
const (
	catCfgGetPrefix = "cat.cfg.get "
	catCfgSetPrefix = "cat.cfg.set "
)

// commandRouter 是会话指令路由——单会话形态（多猫分派归 P3-d 尾部）。
type commandRouter struct {
	// DataRoot 数据根（cat.cfg / cat-default.cfg 读取）
	DataRoot string
	// Workspace 受控根配置（注入条目寻址）
	Workspace *config.WorkspaceConfig
	// Registry 工具注册表（声明面重算）
	Registry *toolpool.Registry
	// Pool 工具池（注入报告工具组映射——OwnerFlow 数据源）
	Pool *toolpool.Pool
	// Session 目标会话
	Session *session.Session
	// CatID 猫 key
	CatID string
	// DisplayName 显示名兜底（cat.cfg 读取失败时）
	DisplayName string
	// Running 主干运行态（cat.list / cat.info 运行面字段——服务在跑才应答指令）
	Running bool
	// Port 主干监听端口（cat.list / cat.info 端口字段）
	Port int
	// Logf 出声回调（nil = 静默）
	Logf func(level int, message, category string)
	// AutoMount 消息自动挂载（nil = 不挂载）——「上工/开工 <项目>」命中即接管投递
	AutoMount func(content string) bool
	// Apply 猫配置生效面（cat.cfg.set 落盘后调用；nil = 只落盘不生效）
	Apply func(catKey string, cfg *config.CatCfg)
	// ValidateToolNames 工具名单校验（池外名剔除；nil = 原样保留）
	ValidateToolNames func(raw string) (string, []string)
	// ValidateEnabledRoots 根白名单校验（workspace 强制 + 池子集）
	ValidateEnabledRoots func(ids []string) (kept, removed []string, poolUnavailable bool)
	// FilterInjectPaths 注入路径过滤（nil = 按 config.IsValidInjectPath 过滤）
	FilterInjectPaths func(items []string) []string
}

// log 出声（nil 安全）。
func (r *commandRouter) log(level int, message, category string) {
	if r.Logf != nil {
		r.Logf(level, message, category)
	}
}

// Handle 处理一条指令——true = 已受理；false = 未识别（调用方回「指令未识别」）。
func (r *commandRouter) Handle(text string) bool {
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
	}
	return false
}

// sessionNew 执行会话重建——重读 cat.cfg → 重算声明面 → 构建注入面 → 清前文 / 视图 / 统计。
//
// 顺序对齐 C# ChatBridge.HandleSessionNew：声明面 → 注入提示词 → 清前文 → 留档 → 清视图 → 注入报告 → 重置事件。
func (r *commandRouter) sessionNew(firstMessage string) bool {
	cfg := r.loadCfg()
	specs := r.rebuildSpecs(cfg)
	prompt, report, count := r.buildInjectSurface(cfg, specs)
	summary := r.Session.Rebuild(session.SessionNewOptions{
		DisplayName:      cfg.DisplayName,
		ToolSpecs:        specs,
		SystemPrompt:     prompt,
		InjectReportJSON: report,
		InjectCount:      count,
	})
	if firstMessage != "" {
		if r.Session.PostUser(firstMessage, "user", "", "") {
			summary += " · 已注入初始消息 " + strconv.Itoa(utf8.RuneCountInString(firstMessage)) + " 字"
		} else {
			summary += " · 初始消息未受理"
			r.log(2, "会话重建后初始消息未受理: cat="+r.CatID, "CHAT")
		}
	}
	r.log(1, summary, "CHAT")
	return true
}

// loadCfg 读取目标猫配置——失败按默认值 + 出声（不阻断重建）。
func (r *commandRouter) loadCfg() *config.CatCfg {
	path := filepath.Join(r.DataRoot, "sessions", r.CatID, "cat.cfg")
	cfg, err := config.LoadCatCfg(path)
	if err != nil {
		r.log(2, "session.new 读取 cat.cfg 失败（按默认值）: "+err.Error(), "CHAT")
		return &config.CatCfg{ID: r.CatID, DisplayName: r.DisplayName}
	}
	if cfg.ID == "" {
		cfg.ID = r.CatID
	}
	return cfg
}

// rebuildSpecs 重算声明面——授权面按最新名单实时刷新，声明面按名单裁剪。
func (r *commandRouter) rebuildSpecs(cfg *config.CatCfg) []session.ToolSpec {
	authorized := applySessionAuth(r.Registry, r.Session, r.CatID, cfg.ToolNames)
	defs := r.Registry.FilterToolSpecs(authorized, toolpool.IsDefaultCatKey(r.CatID))
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

// catList 输出全猫清单——对齐 C# HandleCatList（头行 + 主干前置行；单猫形态恒一只）。
//
// 行分隔用 CRLF：C# Environment.NewLine 在 Windows 上即 \r\n——日志回执逐字对齐按此判。
func (r *commandRouter) catList() bool {
	state := "静默"
	if r.Running {
		state = "运行中 :" + strconv.Itoa(r.Port)
	}
	var sb strings.Builder
	sb.WriteString("cat.list | 1 只猫")
	sb.WriteString("\r\n")
	sb.WriteString("  majordomo | majordomo | " + state + " | 主干会话（禁停禁删）")
	result := sb.String()
	r.log(1, "cat 指令: "+result, "CMD")
	return true
}

// catInfo 输出全猫状态统计——对齐 C# HandleCatInfo（整块分类 JSON：缩进 + 中文直显 + CRLF）。
//
// 字段面 = 运行面六键（id/name/type/special/running/port）+ 会话态全量
// （Session.StateEntry 单一投影源，与快照 sessions 段同源）。
func (r *commandRouter) catInfo() bool {
	entry := r.buildCatInfoEntry("majordomo")
	payload := catInfoResponse{
		OK:    true,
		Tool:  "cat.info",
		Time:  catInfoTime{Now: time.Now().Format("2006-01-02 15:04:05")},
		Count: 1,
		Cats:  []catInfoEntry{entry},
	}
	r.log(1, "cat 指令: "+indentedJSON(payload), "CMD")
	return true
}

// buildCatInfoEntry 构建单猫状态条目——cat.info 与 GET /api/v1/cats 同源（对齐 C# BuildCatInfoEntry 单一实现）。
//
// name 由调用方给：cat.info 恒 "majordomo"（C# 同口径）；cats 端点取 cat.cfg.displayName。
func (r *commandRouter) buildCatInfoEntry(name string) catInfoEntry {
	entry := catInfoEntry{
		ID:      "majordomo",
		Name:    name,
		Type:    "",
		Special: true,
		Running: r.Running,
		Port:    r.Port,
	}
	if r.Session != nil {
		state := r.Session.StateEntry()
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
	}
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

// catsJSON 构建 GET /api/v1/cats 响应体——对齐 C# BuildCatsJson（version + cats[]）。
//
// 条目 = catInfoEntry 全字段 + 行内编辑三字段（apiConfigId / enabledRoots / reasoningEffort）；
// enabledRoots 空表恒输出 []（C# string[0] 同形，非 null）。紧凑单行 + 中文直出（JsonUtil.Serialize 同形）。
func (r *commandRouter) catsJSON() string {
	name := "majordomo"
	apiConfigID := ""
	enabledRoots := []string{}
	reasoningEffort := ""
	path := filepath.Join(r.DataRoot, "sessions", r.CatID, "cat.cfg")
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
		catInfoEntry:    r.buildCatInfoEntry(name),
		APIConfigID:     apiConfigID,
		EnabledRoots:    enabledRoots,
		ReasoningEffort: reasoningEffort,
	}
	return compactJSON(catsResponse{Version: 1, Cats: []catsEntry{entry}})
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

// Execute 执行一条指令并返回回执文本（catcfg.bridge 的宿主指令服务面——单猫形态只认 cat.cfg.*）。
func (r *commandRouter) Execute(line string) string {
	switch {
	case strings.HasPrefix(line, catCfgGetPrefix):
		return r.catCfgGet(strings.TrimSpace(strings.TrimPrefix(line, catCfgGetPrefix)))
	case strings.HasPrefix(line, catCfgSetPrefix):
		return r.catCfgSet(strings.TrimSpace(strings.TrimPrefix(line, catCfgSetPrefix)))
	}
	return "ERR|UNKNOWN_COMMAND|" + line
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

// resolveCatCfgTarget 猫寻址 → cat.cfg 路径（单猫形态：majordomo / 本会话 id 命中，其余未找到）。
func (r *commandRouter) resolveCatCfgTarget(key string) (id, path, errText string) {
	if key == "" {
		return "", "", "缺少猫寻址键"
	}
	if key == "majordomo" {
		id = "majordomo"
	} else if key == r.CatID {
		id = r.CatID
	} else {
		return "", "", "未找到猫: " + key + "（cat.list 查看全部）"
	}
	return id, filepath.Join(r.DataRoot, "sessions", id, "cat.cfg"), ""
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
