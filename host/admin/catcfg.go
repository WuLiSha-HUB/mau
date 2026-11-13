package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"cathome5/host/config"
	"cathome5/host/session"
)

// catConfigResponse 是 GET /api/v1/cat-config 的响应体（字段序与 C# 匿名对象逐条对齐）。
type catConfigResponse struct {
	// OK 读面恒 true
	OK bool `json:"ok"`
	// Cat 猫寻址键
	Cat string `json:"cat"`
	// DisplayName 显示名
	DisplayName string `json:"displayName"`
	// Type 会话 Type 标签
	Type string `json:"type"`
	// APIConfigID 端点身份（全零 = 跟随默认）
	APIConfigID string `json:"apiConfigId"`
	// Persona 角色段
	Persona string `json:"persona"`
	// ToolNames 工具名单原始串
	ToolNames string `json:"toolNames"`
	// InjectList 注入清单
	InjectList []string `json:"injectList"`
	// ProjectMount 当前挂载项目
	ProjectMount string `json:"projectMount"`
	// ProjectMountExpert 专家模式
	ProjectMountExpert bool `json:"projectMountExpert"`
	// Packs 加载包挂载清单
	Packs []string `json:"packs"`
	// DBLibs 库白名单
	DBLibs []string `json:"dbLibs"`
	// AllLibs 库池清单
	AllLibs []libItem `json:"allLibs"`
	// DBLibsAvailable 库池是否可取
	DBLibsAvailable bool `json:"dbLibsAvailable"`
	// AllPacks 加载包池
	AllPacks []config.PackOption `json:"allPacks"`
	// UserInjectIdleOnly 人工输入仅 Idle 注入
	UserInjectIdleOnly bool `json:"userInjectIdleOnly"`
	// ReasoningEffort 本猫思考强度（空 = 跟随全局）
	ReasoningEffort string `json:"reasoningEffort"`
	// AppearanceID 外观身份（全零 = 跟随默认）
	AppearanceID string `json:"appearanceId"`
	// ThemeColor 主题色索引
	ThemeColor int `json:"themeColor"`
	// RoundSummary 轮末总结生效值
	RoundSummary bool `json:"roundSummary"`
	// DefaultReasoningEffort 全局思考强度
	DefaultReasoningEffort string `json:"defaultReasoningEffort"`
	// EnabledRoots 启用根 id 清单
	EnabledRoots []string `json:"enabledRoots"`
	// RootsEffective 实际可用根 id（enabledRoots 经池过滤后的结果；空清单 = 仅常驻根）
	RootsEffective []string `json:"rootsEffective"`
	// AllRoots 全局根池
	AllRoots []rootItem `json:"allRoots"`
	// AllToolNames 可配置工具名清单
	AllToolNames []string `json:"allToolNames"`
	// AllTools 配置面勾选清单
	AllTools []toolItem `json:"allTools"`
	// DefectGroups 工具组定义缺陷
	DefectGroups []defectItem `json:"defectGroups"`
	// StaleToolNames 失效工具名
	StaleToolNames []string `json:"staleToolNames"`
	// APIOptions 端点下拉清单
	APIOptions []apiOption `json:"apiOptions"`
	// AppearanceOptions 外观下拉清单
	AppearanceOptions []config.AppearanceOption `json:"appearanceOptions"`
}

// catConfigWriteResponse 是 POST /api/v1/cat-config 的回执（取落盘实况，不取提交值）。
type catConfigWriteResponse struct {
	// OK 受理标志
	OK bool `json:"ok"`
	// Cat 猫寻址键
	Cat string `json:"cat"`
	// ToolNames 落盘后的工具名单
	ToolNames string `json:"toolNames"`
	// RemovedToolNames 被剔除的池外工具名
	RemovedToolNames []string `json:"removedToolNames"`
	// DBLibs 落盘后的库白名单
	DBLibs []string `json:"dbLibs"`
	// EnabledRoots 落盘后的启用根
	EnabledRoots []string `json:"enabledRoots"`
	// RemovedEnabledRoots 被剔除的池外根 id
	RemovedEnabledRoots []string `json:"removedEnabledRoots"`
	// RootsPoolUnavailable 根池不可用
	RootsPoolUnavailable bool `json:"rootsPoolUnavailable"`
}

// sessionFor 按猫 key 取会话——多猫钩子优先（main 注入 cats.sessionByKey）；
// nil 时回退单猫判定（majordomo / 默认猫 key / 显示名命中默认会话，其余 nil）。
func (d *Deps) sessionFor(catKey string) *session.Session {
	if d.SessionFor != nil {
		return d.SessionFor(catKey)
	}
	if catKey == "" {
		return nil
	}
	if catKey == "majordomo" || catKey == d.CatID || (d.DisplayName != "" && catKey == d.DisplayName) {
		return d.Session
	}
	return nil
}

// catLookup 多猫寻址——CatLookup 钩子优先（id 精确 / 显示名 / id 前缀唯一）；
// nil 时回退单猫判定（默认猫 key / 显示名命中）。
func (d *Deps) catLookup(catKey string) (string, string, bool, int, *session.Session, bool) {
	if d.CatLookup != nil {
		return d.CatLookup(catKey)
	}
	if catKey == "majordomo" || catKey == d.CatID || (d.DisplayName != "" && catKey == d.DisplayName) {
		return d.CatID, d.DisplayName, d.Running, d.Port, d.Session, true
	}
	return "", "", false, 0, nil, false
}

// handleCatConfigGet 服务 GET /api/v1/cat-config?cat=<id|majordomo>。
func (d *Deps) handleCatConfigGet(w http.ResponseWriter, r *http.Request) {
	catKey := r.URL.Query().Get("cat")
	if catKey == "" {
		writeErr(w, "cat 参数为空")
		return
	}
	if !config.IsSafeCatKey(catKey) {
		writeErr(w, "cat 参数非法: "+catKey)
		return
	}
	cfg, err := config.LoadCatCfg(d.catCfgPath(catKey))
	if err != nil {
		writeErr(w, "cat.cfg 不存在: "+catKey)
		return
	}
	libs, libsAvailable := d.allLibs()
	appearancePool := config.LoadAppearancePool(d.appearancePoolPath())
	writeJSON(w, catConfigResponse{
		OK:                     true,
		Cat:                    catKey,
		DisplayName:            cfg.DisplayName,
		Type:                   cfg.Type,
		APIConfigID:            cfg.APIConfigID,
		Persona:                cfg.Persona,
		ToolNames:              cfg.ToolNames,
		InjectList:             cfg.InjectList,
		ProjectMount:           cfg.ProjectMount,
		ProjectMountExpert:     cfg.ProjectMountExpert,
		Packs:                  cfg.Packs,
		DBLibs:                 cfg.DBLibs,
		AllLibs:                libs,
		DBLibsAvailable:        libsAvailable,
		AllPacks:               d.allPacks(),
		UserInjectIdleOnly:     cfg.UserInjectIdleOnly,
		ReasoningEffort:        cfg.ReasoningEffort,
		AppearanceID:           cfg.AppearanceID,
		ThemeColor:             cfg.ThemeColor,
		RoundSummary:           config.ResolveRoundSummary(appearancePool, cfg.AppearanceID),
		DefaultReasoningEffort: d.defaultReasoningEffort(),
		EnabledRoots:           cfg.EnabledRoots,
		RootsEffective:         d.effectiveRootIDs(cfg.EnabledRoots),
		AllRoots:               d.allRoots(),
		AllToolNames:           d.allToolNames(),
		AllTools:               d.allTools(),
		DefectGroups:           d.defectGroups(),
		StaleToolNames:         d.staleToolNames(cfg.ToolNames),
		APIOptions:             d.apiOptions(),
		AppearanceOptions:      d.buildAppearanceOptions(appearancePool),
	})
}

// effectiveRootIDs 实际可用根 id——enabledRoots 经全局池过滤后的结果（空清单 = 仅常驻根 workspace/runtime）。
// 与 catFSRegistry.Rebuild 同源（config.FilterRoots），面板看到的与实际工具面一致。
func (d *Deps) effectiveRootIDs(enabled []string) []string {
	if d.Workspace == nil {
		return []string{}
	}
	return config.RootIDs(config.FilterRoots(d.Workspace.FSRoots(), enabled))
}

// catRootsEffective 读该猫 cat.cfg 的 enabledRoots 并算实际根集——读失败按空白名单（失败默认收紧）。
func (d *Deps) catRootsEffective(catID string) []string {
	var enabled []string
	if cfg, err := config.LoadCatCfg(filepath.Join(d.DataRoot, "sessions", catID, "cat.cfg")); err == nil && cfg != nil {
		enabled = cfg.EnabledRoots
	}
	return d.effectiveRootIDs(enabled)
}

// buildAppearanceOptions 外观下拉清单（池序）。
func (d *Deps) buildAppearanceOptions(pool []config.AppearanceConfig) []config.AppearanceOption {
	return config.BuildAppearanceOptions(pool)
}

// handleCatConfigPost 服务 POST /api/v1/cat-config——字段级合并写（缺省保留磁盘现值）。
func (d *Deps) handleCatConfigPost(w http.ResponseWriter, r *http.Request) {
	fields, ok := readBodyFields(r)
	if !ok {
		writeErr(w, "body 非 JSON")
		return
	}
	catKey := fieldString(fields, "cat")

	// themeColor 形态先校验（非负整数；非法出声，不静默回落默认色）
	themeColorValue := 0
	themeColorFormError := false
	if raw, present := fields["themeColor"]; present {
		var parsed int
		if err := json.Unmarshal(raw, &parsed); err != nil || parsed < 0 {
			themeColorFormError = true
		} else {
			themeColorValue = parsed
		}
	}
	toolNames, toolNamesFormError := readToolNamesField(fields)
	if themeColorFormError {
		writeErr(w, "themeColor 非法（非负整数）")
		return
	}
	if toolNamesFormError != "" {
		writeErr(w, toolNamesFormError)
		return
	}
	if catKey == "" {
		writeErr(w, "cat 为空")
		return
	}
	if !config.IsSafeCatKey(catKey) {
		writeErr(w, "cat 参数非法: "+catKey)
		return
	}
	// 多猫寻址（d-1c）——majordomo 恒可；其余按 CatLookup（id / 显示名 / id 前缀唯一）
	if catKey != "majordomo" {
		if _, _, _, _, _, found := d.catLookup(catKey); !found {
			writeErr(w, "猫不存在: "+catKey)
			return
		}
	}
	cfg, err := config.LoadCatCfg(d.catCfgPath(catKey))
	if err != nil {
		writeErr(w, "cat.cfg 不存在: "+catKey)
		return
	}

	// apiConfigId——空 / 全零 = 清空回默认端点；显式非法值拒绝；字段缺省保留旧值
	if _, present := fields["apiConfigId"]; present {
		trimmed := strings.TrimSpace(fieldString(fields, "apiConfigId"))
		if trimmed == "" || config.IsZeroGUID(trimmed) {
			cfg.APIConfigID = config.ZeroGUIDText
		} else {
			parsed := config.ParseGUID(trimmed)
			if parsed == "" {
				writeErr(w, "apiConfigId 非法")
				return
			}
			cfg.APIConfigID = parsed
		}
	}
	if _, present := fields["displayName"]; present {
		trimmed := strings.TrimSpace(fieldString(fields, "displayName"))
		if trimmed == "" {
			writeErr(w, "displayName 不能为空")
			return
		}
		cfg.DisplayName = trimmed
	}
	if _, present := fields["userInjectIdleOnly"]; present {
		cfg.UserInjectIdleOnly = fieldBool(fields, "userInjectIdleOnly")
	}
	if _, present := fields["reasoningEffort"]; present {
		raw := strings.TrimSpace(fieldString(fields, "reasoningEffort"))
		normalized := config.NormalizeReasoningEffort(raw)
		if normalized == "" && raw != "" {
			writeErr(w, "reasoningEffort 非法（low/high/max/空）")
			return
		}
		cfg.ReasoningEffort = normalized
	}
	if _, present := fields["appearanceId"]; present {
		trimmed := strings.TrimSpace(fieldString(fields, "appearanceId"))
		if trimmed == "" || config.IsZeroGUID(trimmed) {
			cfg.AppearanceID = config.ZeroGUIDText
		} else {
			parsed := config.ParseGUID(trimmed)
			if parsed == "" {
				writeErr(w, "appearanceId 非法")
				return
			}
			cfg.AppearanceID = parsed
		}
	}
	if _, present := fields["themeColor"]; present {
		cfg.ThemeColor = themeColorValue
	}
	if _, present := fields["persona"]; present {
		cfg.Persona = fieldString(fields, "persona")
	}
	if _, present := fields["type"]; present {
		cfg.Type = strings.TrimSpace(fieldString(fields, "type"))
	}
	removedToolNames := []string{}
	if _, present := fields["toolNames"]; present {
		var removed []string
		cfg.ToolNames, removed = d.validateToolNames(toolNames)
		removedToolNames = removed
	}
	if _, present := fields["injectList"]; present {
		cfg.InjectList = filterInjectPaths(fieldStringArray(fields, "injectList"), d)
	}
	if _, present := fields["packs"]; present {
		cfg.Packs = trimItems(fieldStringArray(fields, "packs"))
	}
	if _, present := fields["dbLibs"]; present {
		cfg.DBLibs = trimItems(fieldStringArray(fields, "dbLibs"))
	}
	removedEnabledRoots := []string{}
	rootsPoolUnavailable := false
	if _, present := fields["enabledRoots"]; present {
		kept, removed, unavailable := d.validateEnabledRoots(fieldStringArray(fields, "enabledRoots"))
		cfg.EnabledRoots = kept
		removedEnabledRoots = removed
		rootsPoolUnavailable = unavailable
	}
	// 点亮即同步——新开的跟随立刻把模板现值拉过来（之后由 catcfg.tplbroadcast 维持）；熄灭只改开关，现值保持。
	// 位置须在 toolNames / injectList / enabledRoots 显式写之后：本块是「跟随开关」的语义补写，不应被同行载荷覆盖。
	_, followToolsPresent := fields["followDefaultTools"]
	_, followInjectPresent := fields["followDefaultInject"]
	_, followRootsPresent := fields["followDefaultRoots"]
	followToolsOn := false
	followInjectOn := false
	followRootsOn := false
	if followToolsPresent {
		cfg.FollowDefaultTools = fieldBool(fields, "followDefaultTools")
		followToolsOn = cfg.FollowDefaultTools
	}
	if followInjectPresent {
		cfg.FollowDefaultInject = fieldBool(fields, "followDefaultInject")
		followInjectOn = cfg.FollowDefaultInject
	}
	if followRootsPresent {
		cfg.FollowDefaultRoots = fieldBool(fields, "followDefaultRoots")
		followRootsOn = cfg.FollowDefaultRoots
	}
	if (followToolsPresent && followToolsOn) || (followInjectPresent && followInjectOn) || (followRootsPresent && followRootsOn) {
		if tpl, err := config.LoadCatDefault(d.catDefaultPath()); err == nil && tpl != nil {
			if followToolsPresent && followToolsOn {
				cfg.ToolNames, _ = d.validateToolNames(tpl.DefaultToolNames)
			}
			if followInjectPresent && followInjectOn {
				cfg.InjectList = config.ResolveTplEnabledInject(tpl)
			}
			if followRootsPresent && followRootsOn {
				kept, removed, unavailable := d.resolveTplEnabledRoots(tpl)
				cfg.EnabledRoots = kept
				removedEnabledRoots = removed
				rootsPoolUnavailable = unavailable
			}
		}
	}
	if err := config.SaveCatCfg(d.catCfgPath(catKey), cfg); err != nil {
		writeErr(w, "cat.cfg 写入失败: "+err.Error())
		return
	}
	if d.Apply != nil {
		d.Apply(catKey, cfg)
	}
	d.log(1, "猫配置已受理："+catKey+"（工具面 "+cfg.ToolNames+"）")
	writeJSON(w, catConfigWriteResponse{
		OK:                   true,
		Cat:                  catKey,
		ToolNames:            cfg.ToolNames,
		RemovedToolNames:     removedToolNames,
		DBLibs:               cfg.DBLibs,
		EnabledRoots:         cfg.EnabledRoots,
		RemovedEnabledRoots:  removedEnabledRoots,
		RootsPoolUnavailable: rootsPoolUnavailable,
	})
}

// resolveTplEnabledRoots 默认受控根「启用集」——defaultEnabledRoots 经池校验（workspace 强制 + 池外剔除）。
//
// 新猫继承 / 跟随广播 / 点亮即同步三处共用本出口（对齐 C# ResolveTplEnabledRoots）。
func (d *Deps) resolveTplEnabledRoots(tpl *config.CatDefault) (kept, removed []string, poolUnavailable bool) {
	if tpl == nil || len(tpl.DefaultEnabledRoots) == 0 {
		return d.validateEnabledRoots(nil)
	}
	return d.validateEnabledRoots(tpl.DefaultEnabledRoots)
}

// readToolNamesField 读 toolNames——字符串与字符串数组两形态皆收；非法形态出声（不吞成空串）。
func readToolNamesField(fields map[string]json.RawMessage) (string, string) {
	raw, present := fields["toolNames"]
	if !present {
		return "", ""
	}
	var scalar string
	if err := json.Unmarshal(raw, &scalar); err == nil {
		return scalar, ""
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return "", "toolNames 形态非法（须为字符串或字符串数组）"
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		var value string
		if err := json.Unmarshal(item, &value); err != nil {
			return "", "toolNames 形态非法（数组元素须为字符串）"
		}
		if strings.TrimSpace(value) != "" {
			names = append(names, strings.TrimSpace(value))
		}
	}
	return join(names, ","), ""
}

// filterInjectPaths 注入路径过滤——只接受完整路径（非法不录入，逐条记日志）。
func filterInjectPaths(items []string, d *Deps) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if !config.IsValidInjectPath(trimmed) {
			d.log(2, "猫配置：注入路径被拒绝（非法格式）"+item)
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

// trimItems 去空白归一（保序、去空项）。
func trimItems(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

// catDefaultResponse 是 GET /api/v1/cat-default 的响应体。
type catDefaultResponse struct {
	// OK 读面恒 true
	OK bool `json:"ok"`
	// BaseRole 基础角色段
	BaseRole string `json:"baseRole"`
	// DefaultPersona 默认角色设定
	DefaultPersona string `json:"defaultPersona"`
	// DefaultToolNames 默认工具名单
	DefaultToolNames string `json:"defaultToolNames"`
	// DefaultInjectList 默认注入清单
	DefaultInjectList []string `json:"defaultInjectList"`
	// DefaultInjectDisabled 默认注入禁用清单（条目留在池里可再勾回，但不注入）
	DefaultInjectDisabled []string `json:"defaultInjectDisabled"`
	// DefaultPacks 默认加载包
	DefaultPacks []string `json:"defaultPacks"`
	// DefaultEnabledRoots 默认根白名单（null = 未配置，与空数组区分）
	DefaultEnabledRoots []string `json:"defaultEnabledRoots"`
	// DefaultUserInjectIdleOnly 默认人工输入仅 Idle 注入
	DefaultUserInjectIdleOnly bool `json:"defaultUserInjectIdleOnly"`
	// AllToolNames 可配置工具名清单
	AllToolNames []string `json:"allToolNames"`
	// AllTools 配置面勾选清单
	AllTools []toolItem `json:"allTools"`
	// DefectGroups 工具组定义缺陷
	DefectGroups []defectItem `json:"defectGroups"`
	// StaleToolNames 失效工具名
	StaleToolNames []string `json:"staleToolNames"`
	// AllPacks 加载包池
	AllPacks []config.PackOption `json:"allPacks"`
	// AllRoots 全局根池（模板受控根勾选面数据源）
	AllRoots []rootItem `json:"allRoots"`
}

// catDefaultWriteResponse 是 POST /api/v1/cat-default 的回执。
type catDefaultWriteResponse struct {
	// OK 受理标志
	OK bool `json:"ok"`
	// RemovedToolNames 被剔除的池外工具名
	RemovedToolNames []string `json:"removedToolNames"`
	// RemovedRoots 被剔除的池外根 id
	RemovedRoots []string `json:"removedRoots"`
	// RootsPoolUnavailable 根池不可用
	RootsPoolUnavailable bool `json:"rootsPoolUnavailable"`
}

// handleCatDefaultGet 服务 GET /api/v1/cat-default——新猫默认模板。
func (d *Deps) handleCatDefaultGet(w http.ResponseWriter, r *http.Request) {
	baseRole := ""
	defaultPersona := ""
	defaultToolNames := ""
	defaultInjectList := []string{}
	defaultPacks := []string{}
	defaultEnabledRoots := []string{}
	defaultInjectDisabled := []string{}
	defaultUserInjectIdleOnly := false
	if tpl, err := config.LoadCatDefault(d.catDefaultPath()); err == nil {
		baseRole = tpl.BaseRole
		defaultPersona = tpl.DefaultPersona
		defaultToolNames = tpl.DefaultToolNames
		if tpl.DefaultInjectList != nil {
			defaultInjectList = tpl.DefaultInjectList
		}
		if tpl.DefaultPacks != nil {
			defaultPacks = tpl.DefaultPacks
		}
		if tpl.DefaultEnabledRoots != nil {
			defaultEnabledRoots = tpl.DefaultEnabledRoots
		}
		if tpl.DefaultInjectDisabled != nil {
			defaultInjectDisabled = tpl.DefaultInjectDisabled
		}
		defaultUserInjectIdleOnly = tpl.DefaultUserInjectIdleOnly
	}
	writeJSON(w, catDefaultResponse{
		OK:                        true,
		BaseRole:                  baseRole,
		DefaultPersona:            defaultPersona,
		DefaultToolNames:          defaultToolNames,
		DefaultInjectList:         defaultInjectList,
		DefaultInjectDisabled:     defaultInjectDisabled,
		DefaultPacks:              defaultPacks,
		DefaultEnabledRoots:       defaultEnabledRoots,
		DefaultUserInjectIdleOnly: defaultUserInjectIdleOnly,
		AllToolNames:              d.allToolNames(),
		AllTools:                  d.allTools(),
		DefectGroups:              d.defectGroups(),
		StaleToolNames:            d.staleToolNames(defaultToolNames),
		AllPacks:                  d.allPacks(),
		AllRoots:                  d.allRoots(),
	})
}

// handleCatDefaultPost 服务 POST /api/v1/cat-default——写入新猫默认模板。
func (d *Deps) handleCatDefaultPost(w http.ResponseWriter, r *http.Request) {
	fields, ok := readBodyFields(r)
	if !ok {
		writeErr(w, "body 非 JSON")
		return
	}
	tpl := &config.CatDefault{
		BaseRole:                  fieldString(fields, "baseRole"),
		DefaultPersona:            fieldString(fields, "defaultPersona"),
		DefaultInjectList:         filterInjectPaths(fieldStringArray(fields, "defaultInjectList"), d),
		DefaultPacks:              trimItems(fieldStringArray(fields, "defaultPacks")),
		DefaultInjectDisabled:     trimItems(fieldStringArray(fields, "defaultInjectDisabled")),
		DefaultUserInjectIdleOnly: fieldBool(fields, "defaultUserInjectIdleOnly"),
	}
	var removed []string
	tpl.DefaultToolNames, removed = d.validateToolNames(fieldString(fields, "defaultToolNames"))
	// 默认受控根——与猫级同规：workspace 强制 + 全局池子集；剔除 / 池不可用出声（不静默）
	removedRoots := []string{}
	rootsPoolUnavailable := false
	tpl.DefaultEnabledRoots, removedRoots, rootsPoolUnavailable = d.validateEnabledRoots(fieldStringArray(fields, "defaultEnabledRoots"))
	if err := config.SaveCatDefault(d.catDefaultPath(), tpl); err != nil {
		writeErr(w, "cat-default.cfg 写入失败: "+err.Error())
		return
	}
	// 跟随广播——模板即改即同步（工具清单 / 前文注入 / 受控根）到 follow=true 的猫；生效仍为新会话
	if d.Dispatch != nil {
		d.Dispatch("catcfg.tplbroadcast")
	}
	d.log(1, "全局默认模板已保存（注入 "+itoa(len(tpl.DefaultInjectList))+" 条）")
	writeJSON(w, catDefaultWriteResponse{OK: true, RemovedToolNames: removed, RemovedRoots: removedRoots, RootsPoolUnavailable: rootsPoolUnavailable})
}

// catDetailResponse 是 GET /api/v1/cat-detail 的响应体。
//
// 头部六字段 + 会话状态字段（与 cat.info 同源）+ 详情三字段（末条用户 / 末条回复）。
// 状态字段来自会话投影（map），序列化走自定义 MarshalJSON 以保留「头部在前后加详情」的键序。
type catDetailResponse struct {
	// ID 会话 ID
	ID string `json:"id"`
	// Name 显示名
	Name string `json:"name"`
	// Type 会话 Type 标签
	Type string `json:"type"`
	// Special 主干标记
	Special bool `json:"special"`
	// Running 运行态
	Running bool `json:"running"`
	// Port 监听端口
	Port int `json:"port"`
	// State 会话状态字段（cat.info 同源；随机取用，序列化时按规范键序展开）
	State map[string]any `json:"-"`
	// OK 读面恒 true
	OK bool `json:"ok"`
	// Cat 会话 ID
	Cat string `json:"cat"`
	// RootsEffective 实际可用根 id（cat.cfg enabledRoots 经池过滤；空清单 = 仅常驻根）
	RootsEffective []string `json:"rootsEffective"`
	// IsIdle 是否空闲
	IsIdle bool `json:"isIdle"`
	// LastUser 末条用户消息
	LastUser *detailEntry `json:"lastUser"`
	// LastReply 末条回复
	LastReply *detailEntry `json:"lastReply"`
	// Archive 上一会话留档（无留档时整段不出现——与 C# 键存在性同形）
	Archive *detailArchive `json:"-"`
}

// detailArchive 是上一会话留档段（file / path / text 三字段）。
type detailArchive struct {
	// File 文件名
	File string `json:"file"`
	// Path 绝对路径
	Path string `json:"path"`
	// Text 留档全文
	Text string `json:"text"`
}

// stateKeyOrder 是会话状态字段的规范键序（与 ChatSession.BuildStateEntry 投影序一致）。
var stateKeyOrder = []string{
	"phase", "runState", "runMs", "requests", "round", "msgCount", "pending",
	"noteActive", "contextCount", "context", "contextChars", "lastActiveAt",
	"sessionPrompt", "sessionCacheHit", "sessionCompletion", "sessionMiss", "sessionRate",
}

// MarshalJSON 按「头部六字段 → 状态字段 → 详情三字段」的键序序列化。
func (c catDetailResponse) MarshalJSON() ([]byte, error) {
	writer := &orderedJSON{}
	writer.begin()
	if err := writer.field("id", c.ID); err != nil {
		return nil, err
	}
	if err := writer.field("name", c.Name); err != nil {
		return nil, err
	}
	if err := writer.field("type", c.Type); err != nil {
		return nil, err
	}
	if err := writer.field("special", c.Special); err != nil {
		return nil, err
	}
	if err := writer.field("running", c.Running); err != nil {
		return nil, err
	}
	if err := writer.field("port", c.Port); err != nil {
		return nil, err
	}
	for _, key := range stateKeyOrder {
		value, ok := c.State[key]
		if !ok {
			continue
		}
		if err := writer.field(key, value); err != nil {
			return nil, err
		}
	}
	if err := writer.field("ok", c.OK); err != nil {
		return nil, err
	}
	if err := writer.field("cat", c.Cat); err != nil {
		return nil, err
	}
	if err := writer.field("rootsEffective", c.RootsEffective); err != nil {
		return nil, err
	}
	if err := writer.field("isIdle", c.IsIdle); err != nil {
		return nil, err
	}
	if err := writer.field("lastUser", c.LastUser); err != nil {
		return nil, err
	}
	if err := writer.field("lastReply", c.LastReply); err != nil {
		return nil, err
	}
	if c.Archive != nil {
		if err := writer.field("archive", c.Archive); err != nil {
			return nil, err
		}
	}
	return writer.finish()
}

// orderedJSON 是保序 JSON 对象构造器（键序即调用序）。
type orderedJSON struct {
	// buf 输出缓冲
	buf bytes.Buffer
	// first 是否首个字段
	first bool
}

// begin 写入对象起始。
func (o *orderedJSON) begin() {
	o.buf.WriteByte('{')
	o.first = true
}

// field 写入一个键值对（值走标准 JSON 编码，关闭 HTML 转义）。
func (o *orderedJSON) field(key string, value any) error {
	keyJSON, err := marshalNoEscape(key)
	if err != nil {
		return err
	}
	valueJSON, err := marshalNoEscape(value)
	if err != nil {
		return err
	}
	if !o.first {
		o.buf.WriteByte(',')
	}
	o.first = false
	o.buf.Write(keyJSON)
	o.buf.WriteByte(':')
	o.buf.Write(valueJSON)
	return nil
}

// finish 写入对象结束并返回字节。
func (o *orderedJSON) finish() ([]byte, error) {
	o.buf.WriteByte('}')
	return o.buf.Bytes(), nil
}

// marshalNoEscape 标准 JSON 编码（关闭 HTML 转义，中文直出）。
func marshalNoEscape(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// detailEntry 是详情条目（本会话视图块真源）。
type detailEntry struct {
	// Text 正文
	Text string `json:"text"`
	// Source 来源（current = 本会话）
	Source string `json:"source"`
	// Time 时间戳（Unix 毫秒）
	Time int64 `json:"time"`
	// TimeText 时间文本（留档来源为档内文本；本会话为空）
	TimeText string `json:"timeText"`
}

// handleCatDetail 服务 GET /api/v1/cat-detail?cat=<key>——会话状态卡详情数据源。
//
// 单会话宿主：majordomo（或当前猫 key）命中本会话；其余 key 回「未找到猫」。
// 上一会话留档回落（sessions_old）随 P3-d 会话重建面落地——该面定义归档格式。
func (d *Deps) handleCatDetail(w http.ResponseWriter, r *http.Request) {
	catKey := r.URL.Query().Get("cat")
	if catKey == "" {
		writeErr(w, "cat 参数为空")
		return
	}
	if !config.IsSafeCatKey(catKey) {
		writeErr(w, "cat 参数非法: "+catKey)
		return
	}
	// 多猫寻址（d-1c）——special 走默认猫；其余按 CatLookup 取 id / 显示名 / 运行态 / 端口 / 会话
	special := catKey == "majordomo"
	id := catKey
	name := d.DisplayName
	running := d.Running
	port := d.Port
	if d.MajorPort != nil {
		// 默认猫对话端口（管理面板形态下独立分配）——与 GET /api/v1/cats 的 port 同源
		port = d.MajorPort()
	}
	var sess *session.Session
	if special {
		sess = d.sessionFor("majordomo")
	} else {
		gotID, gotName, gotRunning, gotPort, gotSess, found := d.catLookup(catKey)
		if !found {
			writeErr(w, "未找到猫: "+catKey)
			return
		}
		id, name, running, port, sess = gotID, gotName, gotRunning, gotPort, gotSess
	}
	resp := catDetailResponse{
		ID:      id,
		Name:    name,
		Type:    "",
		Special: special,
		Running: running,
		Port:    port,
		OK:      true,
		Cat:     id,
	}
	resp.RootsEffective = d.catRootsEffective(id)
	if sess == nil {
		resp.IsIdle = true
		writeJSON(w, resp)
		return
	}
	resp.State = sess.StateEntry()
	resp.IsIdle = sess.PhaseText() == "Idle"
	blocks := sess.Views.Blocks()
	resp.LastUser = findLastViewEntry(blocks, "user", d)
	resp.LastReply = findLastViewEntry(blocks, "text", d)
	// [段3] 上一会话回落——sessions_old 该猫最近一份留档（本会话无对应消息时取档内末条）
	archivePath := session.FindLatestArchive(filepath.Join(d.DataRoot, "sessions_old"), resp.ID)
	archiveText := ""
	if archivePath != "" {
		if raw, err := os.ReadFile(archivePath); err == nil {
			archiveText = string(raw)
		} else {
			// 读取失败不阻断详情（失败可见——WARN 日志 + 视为无留档）
			d.log(2, "会话留档读取失败: "+err.Error())
			archivePath = ""
		}
	}
	if resp.LastUser == nil && archiveText != "" {
		resp.LastUser = findLegacyEntry(archiveText, "用户")
	}
	if resp.LastReply == nil && archiveText != "" {
		resp.LastReply = findLegacyEntry(archiveText, "回复")
	}
	if archivePath != "" {
		resp.Archive = &detailArchive{File: filepath.Base(archivePath), Path: archivePath, Text: archiveText}
	}
	writeJSON(w, resp)
}

// findLegacyEntry 留档末条条目 → 详情条目（source=archive——时刻为档内文本，非 Unix 毫秒）。
func findLegacyEntry(markdown, title string) *detailEntry {
	timeText, content, ok := session.TryReadLastLegacyEntry(markdown, title)
	if !ok {
		return nil
	}
	return &detailEntry{Text: content, Source: "archive", Time: 0, TimeText: timeText}
}

// findLastViewEntry 倒序扫描末条指定类型视图块（内容为空 / 载荷解析失败则继续前一条）。
func findLastViewEntry(blocks []*session.ViewBlock, renderType string, d *Deps) *detailEntry {
	for i := len(blocks) - 1; i >= 0; i-- {
		block := blocks[i]
		if block == nil || block.RenderType != renderType {
			continue
		}
		content := readBlockContent(block.Payload, d)
		if content == "" {
			continue
		}
		return &detailEntry{Text: content, Source: "current", Time: block.Timestamp, TimeText: ""}
	}
	return nil
}

// readBlockContent 视图块载荷 → 正文（content 字段；解析失败 / 缺字段 = 空串）。
func readBlockContent(payload string, d *Deps) string {
	if payload == "" {
		return ""
	}
	var doc struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(payload), &doc); err != nil {
		d.log(2, "详情块载荷解析失败: "+err.Error())
		return ""
	}
	return strings.TrimSpace(doc.Content)
}
