// Package admin 复现宿主管理端（C# AdminRoutes 全表）的路由与处理器。
//
// 装配与执行分离：Register 只登记「路径 + 方法 + 处理器」，数据面在各分部文件。
// 端点契约以 `mau:CatHome4/CatHome4.Admin/AdminRoutes.cs` 为规格书——路径 / 方法 /
// 查询参数 / 响应 JSON 字段与顺序逐条对齐（前端零改动是硬约束）。
package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"cathome5/host/config"
	"cathome5/host/group"
	"cathome5/host/session"
	"cathome5/host/toolpool"
	"cathome5/mau/db"
)

// Deps 是管理面数据源与句柄。
type Deps struct {
	// DataRoot 数据根（config / sessions / secrets / sessions_old 的父目录）
	DataRoot string
	// Schema 配置声明表
	Schema *config.Schema
	// Store 全局配置存储
	Store *config.Store
	// Workspace 受控根与注入清单
	Workspace *config.WorkspaceConfig
	// Registry 工具注册表（工具清单 / 特权标记 / 失效名判定）
	Registry *toolpool.Registry
	// Pool 工具池快照（allTools 的组与执行序来源）
	Pool *toolpool.Pool
	// Groups 组数据面（配队段 13 数据源；nil = 组端点按未接线失败）
	Groups *group.Store
	// LLMStore 端点池存储（管理面读写与请求面同一实例——写入即时可见）
	LLMStore *config.LlmAPIStore
	// DB 条目库服务（段 11 四端点 + allLibs 库池的数据源；nil = 服务未绑定）
	DB *db.Service
	// Session 默认会话（单猫形态回退——多猫面按 cat 走 SessionFor）
	Session *session.Session
	// SessionFor 按猫 key 取会话（含 majordomo / 显示名 / id 前缀唯一；nil = 未命中）。
	// 多猫钩子（main 注入 cats.sessionByKey）；nil 时回退单猫判定（默认猫 key / 显示名命中 d.Session）
	SessionFor func(catKey string) *session.Session
	// CatLookup 多猫寻址（id 精确 / 显示名 / id 前缀唯一）——返回 id / 显示名 / 运行态 / 端口 / 会话 / 命中。
	// nil 时回退单猫判定（默认猫 key / 显示名命中）；d-1c 管理面多猫编辑的寻址单点
	CatLookup func(catKey string) (id, name string, running bool, port int, sess *session.Session, ok bool)
	// CatID 默认会话 key（majordomo）
	CatID string
	// DisplayName 默认会话显示名
	DisplayName string
	// Running 默认会话运行态（真机恒 true——服务在跑才应答本端点）
	Running bool
	// Port 默认会话监听端口
	Port int
	// Logf 观测日志出口（nil = 丢弃）
	Logf func(category string, level int, message string)
	// Apply 每猫配置落盘后的运行时生效回调（授权面实时 / 注入面新会话；nil = 不生效）
	Apply func(catKey string, cfg *config.CatCfg)
	// Dispatch 会话指令投递口（session.new 重建等；nil = 不重建，只落盘新会话生效）
	Dispatch func(command string) bool
	// Restart 宿主重启请求登记面（nil = 端点按未接线失败）
	Restart *RestartState
	// OpenDir 打开目录通道（nil = 调 explorer.exe；单测注入替身不弹窗）
	OpenDir func(path string) error
	// GroupHostPort 组宿主端口查询（nil = 未接线——组轻宿主未起，恒 0）
	GroupHostPort func(name string) int
	// GroupHostRunning 组宿主运行态查询（nil = 未接线，恒 false）
	GroupHostRunning func(name string) bool
	// GroupHostStart 组宿主拉起（nil = 不拉起；返回是否成功——已运行幂等 true）
	GroupHostStart func(name string) bool
	// GroupHostStop 组宿主停止（nil = 不停止）
	GroupHostStop func(name string)
	// CatKnown 目标猫可达判定（组投递门禁；nil = 仅默认会话可按 key / 显示名命中）
	CatKnown func(catKey string) bool
	// CatDeliver 目标猫投递（组派单 / 订阅；nil = 投给默认会话）
	CatDeliver func(catKey, content, origin string) bool
}

// Register 登记管理端第一批路由（段 1 / 段 2 / 段 4）。
func Register(mux *http.ServeMux, d *Deps) {
	// [段1] LLM API 池 CRUD
	mux.HandleFunc("GET /api/v1/llm-apis", d.handleLlmApisGet)
	mux.HandleFunc("POST /api/v1/llm-apis", d.handleLlmApisPost)
	mux.HandleFunc("POST /api/v1/llm-apis/edit", d.handleLlmApisEdit)
	mux.HandleFunc("POST /api/v1/llm-apis/delete", d.handleLlmApisDelete)
	mux.HandleFunc("POST /api/v1/llm-apis/default", d.handleLlmApisDefault)
	mux.HandleFunc("POST /api/v1/llm-apis/backup", d.handleLlmApisBackup)
	mux.HandleFunc("POST /api/v1/llm-apis/probe", d.handleLlmApisProbe)
	// [段2] 每猫配置读写 + 会话详情
	mux.HandleFunc("GET /api/v1/cat-config", d.handleCatConfigGet)
	mux.HandleFunc("POST /api/v1/cat-config", d.handleCatConfigPost)
	mux.HandleFunc("GET /api/v1/cat-detail", d.handleCatDetail)
	// [段4] 新猫默认模板
	mux.HandleFunc("GET /api/v1/cat-default", d.handleCatDefaultGet)
	mux.HandleFunc("POST /api/v1/cat-default", d.handleCatDefaultPost)
	// [段5] 受控根编辑面——读面（弹层数据源）+ 写面（固定根 id 白名单 + 只改 path + 原子写）
	mux.HandleFunc("GET /api/v1/workspace", d.handleWorkspaceGet)
	mux.HandleFunc("POST /api/v1/workspace", d.handleWorkspacePost)
	// [段7] 加载包池——池读 + 增改一体 + 删除
	mux.HandleFunc("GET /api/v1/packs", d.handlePacksGet)
	mux.HandleFunc("POST /api/v1/packs", d.handlePacksPost)
	mux.HandleFunc("POST /api/v1/packs/delete", d.handlePacksDelete)
	// [段6] 外观配置池——池列表 / 新建 / 编辑 / 删除 / 设为默认
	mux.HandleFunc("GET /api/v1/appearance-apis", d.handleAppearanceApisGet)
	mux.HandleFunc("POST /api/v1/appearance-apis", d.handleAppearanceApisPost)
	mux.HandleFunc("POST /api/v1/appearance-apis/edit", d.handleAppearanceApisEdit)
	mux.HandleFunc("POST /api/v1/appearance-apis/delete", d.handleAppearanceApisDelete)
	mux.HandleFunc("POST /api/v1/appearance-apis/default", d.handleAppearanceApisDefault)
	// [段7b] 用户态——当前用户 + QQ 非用户指令开关 + 群内身份清单
	mux.HandleFunc("GET /api/v1/user-state", d.handleUserStateGet)
	mux.HandleFunc("POST /api/v1/user-state", d.handleUserStatePost)
	// 宿主重启——只登记请求 + 置停机态（接力面归 P5 外壳；闸门超时撤销并出声）
	mux.HandleFunc("POST /api/v1/host-restart", d.handleHostRestartPost)
	// [段8] 打开数据目录——前端按钮（explorer.exe 打开持久化数据根）
	mux.HandleFunc("POST /api/v1/open-data-dir", d.handleOpenDataDir)
	// [段9] 对话图片附件——上传 + 取图（受控根寻址 / 根内绝对路径 / 缓存目录裸名）
	mux.HandleFunc("POST /api/v1/chat-images", d.handleChatImagesUpload)
	mux.HandleFunc("GET /api/v1/cache-image/{file...}", d.handleCacheImageGet)
	// [段10] 音频资产——轮末总结拼接音频取回（产物目录裸名 + .wav）
	mux.HandleFunc("GET /api/v1/audio/{file...}", d.handleAudioGet)
	// [段11] 数据库（Smriti）——库清单 / 建库 / 条目查询 / 条目全文（条目库服务未接入，占位回执归 P4）
	mux.HandleFunc("GET /api/v1/db/libs", d.handleDbLibsGet)
	mux.HandleFunc("POST /api/v1/db/libs", d.handleDbLibsPost)
	mux.HandleFunc("GET /api/v1/db/libs/{lib}/entries", d.handleDbEntriesGet)
	mux.HandleFunc("GET /api/v1/db/libs/{lib}/entries/{entryId}", d.handleDbEntryGet)
	// [段12] 项目挂载——前文注入清单的项目段增删（点击选择器时才扫盘取最新改动时间）
	mux.HandleFunc("GET /api/v1/inject/projects", d.handleInjectProjectsGet)
	mux.HandleFunc("GET /api/v1/inject/preview", d.handleInjectPreviewGet)
	mux.HandleFunc("POST /api/v1/inject/project", d.handleInjectProjectPost)
	mux.HandleFunc("GET /api/v1/inject/auto", d.handleInjectAutoGet)
	mux.HandleFunc("POST /api/v1/inject/auto", d.handleInjectAutoPost)
	// [段13] 配队（Group）数据面——组列表 / 建改 / 启停 / 删除 / 消息流 / 投递 / 宿主端口
	mux.HandleFunc("GET /api/v1/groups", d.handleGroupsGet)
	mux.HandleFunc("POST /api/v1/groups", d.handleGroupsPost)
	mux.HandleFunc("POST /api/v1/groups/enabled", d.handleGroupsEnabled)
	mux.HandleFunc("POST /api/v1/groups/delete", d.handleGroupsDelete)
	mux.HandleFunc("GET /api/v1/groups/{name}/messages", d.handleGroupsMessagesGet)
	mux.HandleFunc("POST /api/v1/groups/messages", d.handleGroupsMessagesPost)
	mux.HandleFunc("GET /api/v1/groups/host", d.handleGroupsHostGet)
}

// RegisterChatPageRoutes 对话页静态路由注册——chat.html 专用（不含管理 CRUD），可安全下发到每猫端口。
//
// 对齐 C# `AdminService.ChatImages.RegisterChatPageRoutes`：图片上传 / 取图 / 音频点播三件。
// 每猫独立对话端口（d-1b）经 BindAdmin 注入本注册面——管理端 RouteRegistrar 不随实例下发。
func RegisterChatPageRoutes(mux *http.ServeMux, d *Deps) {
	mux.HandleFunc("POST /api/v1/chat-images", d.handleChatImagesUpload)
	mux.HandleFunc("GET /api/v1/cache-image/{file...}", d.handleCacheImageGet)
	mux.HandleFunc("GET /api/v1/audio/{file...}", d.handleAudioGet)
}

// writeJSON 写 JSON 响应——关闭 HTML 转义（中文直出）、去尾换行（同 C# JsonUtil.Serialize 紧凑形态）。
func writeJSON(w http.ResponseWriter, v any) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		http.Error(w, `{"ok":false,"error":"json encode failed"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
}

// okResponse 是通用成功回执（字段序对齐 C# new { ok = true }）。
type okResponse struct {
	// OK 受理标志
	OK bool `json:"ok"`
}

// errResponse 是通用失败回执（字段序对齐 C# new { ok = false, error = "..." }）。
type errResponse struct {
	// OK 恒 false
	OK bool `json:"ok"`
	// Error 失败原因（原文直读）
	Error string `json:"error"`
}

// writeErr 写失败回执。
func writeErr(w http.ResponseWriter, message string) {
	writeJSON(w, errResponse{OK: false, Error: message})
}

// readBodyFields 读取请求体为字段表（保留字段存在性——字段级合并写的判据）。
func readBodyFields(r *http.Request) (map[string]json.RawMessage, bool) {
	body, err := readAll(r)
	if err != nil {
		return nil, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, false
	}
	return fields, true
}

// readAll 读请求体（上限 4 MiB）。
func readAll(r *http.Request) ([]byte, error) {
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(http.MaxBytesReader(nil, r.Body, 4<<20)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// fieldString 取字符串字段（缺字段 / 非字符串 = 空串）。
func fieldString(fields map[string]json.RawMessage, key string) string {
	raw, ok := fields[key]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

// fieldBool 取布尔字段（缺字段 / 非布尔 = false）。
func fieldBool(fields map[string]json.RawMessage, key string) bool {
	raw, ok := fields[key]
	if !ok {
		return false
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	return value
}

// fieldStringArray 取字符串数组字段（缺字段 / 非数组 = nil；元素非字符串跳过、空串跳过）。
func fieldStringArray(fields map[string]json.RawMessage, key string) []string {
	raw, ok := fields[key]
	if !ok {
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		var value string
		if err := json.Unmarshal(item, &value); err != nil {
			continue
		}
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

// catCfgPath 每猫配置路径。
func (d *Deps) catCfgPath(catKey string) string {
	return filepath.Join(d.DataRoot, "sessions", catKey, "cat.cfg")
}

// catDefaultPath 新猫默认模板路径。
func (d *Deps) catDefaultPath() string {
	return filepath.Join(d.DataRoot, "config", "cat-default.cfg")
}

// packPoolPath 加载包池路径。
func (d *Deps) packPoolPath() string {
	return filepath.Join(d.DataRoot, "config", "packs.json")
}

// appearancePoolPath 外观池路径。
func (d *Deps) appearancePoolPath() string {
	return filepath.Join(d.DataRoot, "config", "appearance.json")
}

// log 记一条观测日志（nil 出口静默丢弃）。
func (d *Deps) log(level int, message string) {
	if d.Logf != nil {
		d.Logf("CONFIG", level, message)
	}
}

// fixedRoot 是一条固定命名根定义（id / note / writable 三列固定）。
type fixedRoot struct {
	ID       string
	Note     string
	Writable bool
}

// fixedRoots 固定命名根表——与 C# FixedRoots 逐条对齐（读面标记 / 写面校验同源）。
var fixedRoots = []fixedRoot{
	{ID: "workspace", Note: "系统工作区（必选）", Writable: true},
	{ID: "mau", Note: "Mau 仓库（工具组仓库根）", Writable: true},
	{ID: "mauout", Note: "运行实例部署区（重启目标）", Writable: true},
	{ID: "ccbp", Note: "CCBP 知识网络根", Writable: true},
	{ID: "localgit", Note: "localGit 兜底仓（本地提交）", Writable: true},
	{ID: "data", Note: "CH4 应用数据（appdata）", Writable: false},
	{ID: "db", Note: "数据库（Smriti 条目库）", Writable: false},
}

// findFixedRoot 按 id 查固定根定义（id 先归一小写——读面磁盘值大小写不敏感）。
func findFixedRoot(id string) (fixedRoot, bool) {
	norm := normalizeRootID(id)
	for _, def := range fixedRoots {
		if def.ID == norm {
			return def, true
		}
	}
	return fixedRoot{}, false
}

// rootItem 是根池下拉项（allRoots 元素）。
type rootItem struct {
	// ID 根标识
	ID string `json:"id"`
	// Path 根路径
	Path string `json:"path"`
	// Writable 可写标志（固定根取固定值）
	Writable bool `json:"writable"`
	// Note 认路注释（固定根取固定文案）
	Note string `json:"note"`
	// FixedRoot 是否系统固定命名根
	FixedRoot bool `json:"fixedRoot"`
}

// allRoots 全局根池（workspace 强制必选；固定根三列以固定表为准）。
func (d *Deps) allRoots() []rootItem {
	out := make([]rootItem, 0, 8)
	if d.Workspace == nil {
		return out
	}
	for _, root := range d.Workspace.Roots {
		item := rootItem{ID: root.ID, Path: root.Path, Writable: root.Writable, Note: root.Note}
		if def, ok := findFixedRoot(root.ID); ok {
			item.Writable = def.Writable
			item.Note = def.Note
			item.FixedRoot = true
		}
		out = append(out, item)
	}
	return out
}

// toolItem 是配置面勾选清单条目（allTools 元素）。
type toolItem struct {
	// Name 工具名
	Name string `json:"name"`
	// Group 归属组
	Group string `json:"group"`
	// Privileged 特权标记（可见不可选）
	Privileged bool `json:"privileged"`
	// Order 执行序注记
	Order string `json:"order"`
}

// allTools 配置面勾选清单（全量 + 特权标记；渲染三态）。
func (d *Deps) allTools() []toolItem {
	out := make([]toolItem, 0, 128)
	if d.Registry == nil {
		return out
	}
	for _, entry := range d.Registry.All() {
		order := ""
		if d.Pool != nil {
			order = toolpool.OrderText(entry.Name)
		}
		out = append(out, toolItem{Name: entry.Name, Group: entry.OwnerFlow, Privileged: entry.Privileged, Order: order})
	}
	return out
}

// allToolNames 可配置工具名清单（全量剔除特权工具）。
func (d *Deps) allToolNames() []string {
	if d.Registry == nil {
		return []string{}
	}
	return d.Registry.GetConfigurableToolNames()
}

// defectItem 是工具组定义缺陷条目。
type defectItem struct {
	// Group 组名
	Group string `json:"group"`
	// Stage 缺陷阶段
	Stage string `json:"stage"`
	// Reason 原因
	Reason string `json:"reason"`
}

// defectGroups 工具组定义缺陷清单（不可用组在前端可见，不静默消失）。
func (d *Deps) defectGroups() []defectItem {
	out := make([]defectItem, 0, 4)
	if d.Pool == nil {
		return out
	}
	for _, defect := range d.Pool.Defects() {
		out = append(out, defectItem{Group: defect.Group, Stage: defect.Stage, Reason: defect.Reason})
	}
	return out
}

// staleToolNames 清单里已失效的工具名（cat.cfg 含而池无）。
func (d *Deps) staleToolNames(raw string) []string {
	if d.Registry == nil {
		return []string{}
	}
	stale := d.Registry.ResolveStaleToolNames(raw)
	if stale == nil {
		return []string{}
	}
	return stale
}

// apiOption 是端点下拉项。
type apiOption struct {
	// APIConfigID 配置身份
	APIConfigID string `json:"apiConfigId"`
	// DisplayName 显示名
	DisplayName string `json:"displayName"`
	// IsDefault 默认标记
	IsDefault bool `json:"isDefault"`
}

// apiOptions 端点池下拉清单。
func (d *Deps) apiOptions() []apiOption {
	out := make([]apiOption, 0, 8)
	if d.LLMStore == nil {
		return out
	}
	for _, cfg := range d.LLMStore.Configs() {
		out = append(out, apiOption{APIConfigID: cfg.APIConfigID, DisplayName: cfg.DisplayName, IsDefault: cfg.IsDefault})
	}
	return out
}

// appearanceOptions 外观池下拉清单。
func (d *Deps) appearanceOptions() []config.AppearanceOption {
	pool := config.LoadAppearancePool(d.appearancePoolPath())
	return config.BuildAppearanceOptions(pool)
}

// allPacks 加载包池下拉清单。
func (d *Deps) allPacks() []config.PackOption {
	return config.BuildPackOptions(config.LoadPackPool(d.packPoolPath()))
}

// libItem 是库池清单条目。
type libItem struct {
	// ID 库 id
	ID string `json:"id"`
	// Entries 活跃条目数
	Entries int `json:"entries"`
	// Broken 坏库标记
	Broken bool `json:"broken"`
}

// allLibs 库池清单——注册表登记的库（管理面无猫上下文：空 catID = 全放）。
//
// 服务未绑定 / 读失败 / 载荷不可读 → 不可取（available=false，调用方须出声，不静默空列表）。
func (d *Deps) allLibs() ([]libItem, bool) {
	if d.DB == nil {
		return []libItem{}, false
	}
	result := d.DB.ListLibs("")
	if !result.OK || result.Data == "" {
		return []libItem{}, false
	}
	var payload struct {
		Libs []struct {
			ID      string `json:"id"`
			Entries int    `json:"entries"`
			Broken  bool   `json:"broken"`
		} `json:"libs"`
	}
	if err := json.Unmarshal([]byte(result.Data), &payload); err != nil {
		return []libItem{}, false
	}
	out := make([]libItem, 0, len(payload.Libs))
	for _, entry := range payload.Libs {
		if entry.ID == "" {
			continue
		}
		out = append(out, libItem{ID: entry.ID, Entries: entry.Entries, Broken: entry.Broken})
	}
	return out, true
}

// defaultReasoningEffort 全局思考强度——llm.reasoning_effort → env → 默认 high。
func (d *Deps) defaultReasoningEffort() string {
	value := ""
	if d.Store != nil {
		value = d.Store.Resolve(d.Schema, "llm.reasoning_effort").Value
	}
	if normalized := config.NormalizeReasoningEffort(value); normalized != "" {
		return normalized
	}
	if env := os.Getenv("MAU_LLM_REASONING_EFFORT"); env != "" {
		if normalized := config.NormalizeReasoningEffort(env); normalized != "" {
			return normalized
		}
	}
	return "high"
}

// validateToolNames 工具名单写时校验——池外名剔除，合法名逗号重拼。
//
// 剔除明细经 removed 带回（调用方在响应面报告，不静默）。
func (d *Deps) validateToolNames(raw string) (string, []string) {
	removed := []string{}
	if raw == "" {
		return "", removed
	}
	if d.Registry == nil {
		return raw, removed
	}
	allowed := d.Registry.GetConfigurableToolNames()
	names := make([]string, 0, 16)
	for _, name := range toolpool.SplitToolNameList(raw) {
		if name == "*" || contains(allowed, name) {
			names = append(names, name)
			continue
		}
		removed = append(removed, name)
	}
	if len(removed) > 0 {
		d.log(2, "工具名单写时剔除 "+itoa(len(removed))+" 个池外工具名："+join(removed, ",")+"（工具池中不存在——名单以池为准）")
	}
	return join(names, ","), removed
}

// validateEnabledRoots 启用根校验——workspace 强制 + 全局池子集；非法 id 剔除、去重、保序。
func (d *Deps) validateEnabledRoots(ids []string) (kept []string, removed []string, poolUnavailable bool) {
	removed = []string{}
	if d.Workspace == nil || len(d.Workspace.Roots) == 0 {
		return []string{}, removed, true
	}
	pool := make([]string, 0, len(d.Workspace.Roots))
	for _, root := range d.Workspace.Roots {
		pool = append(pool, root.ID)
	}
	seen := make(map[string]bool, len(ids))
	kept = make([]string, 0, len(ids)+1)
	kept = append(kept, "workspace")
	seen["workspace"] = true
	for _, id := range ids {
		if seen[id] {
			continue
		}
		if !contains(pool, id) {
			removed = append(removed, id)
			continue
		}
		seen[id] = true
		kept = append(kept, id)
	}
	return kept, removed, false
}

// contains 线性查名（大小写敏感）。
func contains(list []string, name string) bool {
	for _, item := range list {
		if item == name {
			return true
		}
	}
	return false
}

// join 逗号拼接（空列表返回空串）。
func join(items []string, sep string) string {
	var buf bytes.Buffer
	for i, item := range items {
		if i > 0 {
			buf.WriteString(sep)
		}
		buf.WriteString(item)
	}
	return buf.String()
}

// itoa 整数转字符串。
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	digits := make([]byte, 0, 4)
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}

// ValidateToolNames 工具名单校验（导出——cat.cfg.set 指令与管理面写面共用同一实现）。
func (d *Deps) ValidateToolNames(raw string) (string, []string) {
	return d.validateToolNames(raw)
}

// ValidateEnabledRoots 根白名单校验（导出——cat.cfg.set 指令共用）。
func (d *Deps) ValidateEnabledRoots(ids []string) (kept, removed []string, poolUnavailable bool) {
	return d.validateEnabledRoots(ids)
}

// FilterInjectPaths 注入路径过滤（导出——cat.cfg.set 指令共用）。
func (d *Deps) FilterInjectPaths(items []string) []string {
	return filterInjectPaths(items, d)
}

// writeRawJSON 写已编码的 JSON 文本（构建器自产回执——与 writeJSON 同头部）。
func writeRawJSON(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(text))
}

// jsonText 编码紧凑 JSON 文本——与 writeJSON 同口径（关闭 HTML 转义、去尾换行）。
func jsonText(v any) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return `{"ok":false,"error":"json encode failed"}`
	}
	return string(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
}

// RegisterChatInjectRoutes 主端口对话页实例的项目挂载三件——与段 12 同一实现（C# 双注册同构）。
func RegisterChatInjectRoutes(mux *http.ServeMux, d *Deps) {
	mux.HandleFunc("GET /api/v1/inject/projects", d.handleInjectProjectsGet)
	mux.HandleFunc("POST /api/v1/inject/project", d.handleInjectProjectPost)
	mux.HandleFunc("GET /api/v1/inject/auto", d.handleInjectAutoGet)
	mux.HandleFunc("POST /api/v1/inject/auto", d.handleInjectAutoPost)
}

// InjectProjectsJSON 项目清单 JSON——主端口条件注册版数据源（同一实现，不造第二份规则）。
func (d *Deps) InjectProjectsJSON() string { return jsonText(d.buildInjectProjects()) }

// InjectProjectJSON 项目挂载写面 JSON——expert 为 nil = 沿用现值。
func (d *Deps) InjectProjectJSON(catKey, project string, expert *bool) string {
	expertValue := false
	if expert != nil {
		expertValue = *expert
	}
	resp, _ := d.applyInjectProject(catKey, project, expertValue, expert != nil, true)
	return jsonText(resp)
}

// InjectAutoJSON 自动识别开关读面 JSON（缺省 cat = majordomo）。
func (d *Deps) InjectAutoJSON(catKey string) string {
	if catKey == "" {
		catKey = "majordomo"
	}
	if !config.IsSafeCatKey(catKey) {
		return jsonText(injectAutoGetFailResponse{OK: false, Error: "cat 参数非法: " + catKey})
	}
	cfg, err := config.LoadCatCfg(d.catCfgPath(catKey))
	if err != nil || cfg == nil {
		return jsonText(injectAutoGetFailResponse{OK: false, Error: "cat.cfg 不存在: " + catKey})
	}
	return jsonText(injectAutoResponse{OK: true, Cat: catKey, On: cfg.MountAuto})
}

// InjectAutoSetJSON 自动识别开关写面 JSON——on 为 nil 时默认 true（C# 主端口版同口径）。
func (d *Deps) InjectAutoSetJSON(catKey string, on *bool) string {
	if catKey == "" {
		catKey = "majordomo"
	}
	value := true
	if on != nil {
		value = *on
	}
	if !config.IsSafeCatKey(catKey) {
		return jsonText(injectAutoPostFailResponse{OK: false, Reason: "cat 参数非法: " + catKey})
	}
	if catKey != "majordomo" {
		return jsonText(injectAutoPostFailResponse{OK: false, Reason: "未找到猫：" + catKey})
	}
	path := d.catCfgPath("majordomo")
	cfg, err := config.LoadCatCfg(path)
	if err != nil || cfg == nil {
		return jsonText(injectAutoPostFailResponse{OK: false, Reason: "cat.cfg 不存在: majordomo"})
	}
	cfg.MountAuto = value
	if err := config.SaveCatCfg(path, cfg); err != nil {
		d.logInject(2, "自动识别开关写入失败："+err.Error())
		return jsonText(injectAutoPostFailResponse{OK: false, Reason: "cat.cfg 写入失败: " + err.Error()})
	}
	d.logInject(1, "自动识别开关：cat=majordomo on="+strconv.FormatBool(value))
	return jsonText(injectAutoResponse{OK: true, Cat: catKey, On: value})
}
