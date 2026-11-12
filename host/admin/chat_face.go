package admin

// 对话页面数据源三件——端点角色 / 模型清单 / 思考强度（主端口条件注册版与 C# Admin 域同源）。
//
// 对齐 C#：BuildApiRoleJson（AdminService.Handlers.cs:303）· BuildApiModelsJsonAsync
// （AdminService.ApiModel.cs:31）· BuildEffortJson / SetEffortJson（AdminService.Handlers.cs:310 / 331）。
// 端点池未绑定 / 无备用端点时对应字段为空串（前端回落占位符——不静默显示假值）。

import (
	"encoding/json"
	"strings"

	"cathome5/host/config"
	"cathome5/host/session"
)

// apiRolePayload 端点角色信息（字段序对齐 C# GetApiRoleJson 的 JsonUtil.Object 顺序）。
type apiRolePayload struct {
	// Role 当前角色（主要 / 备用）
	Role string `json:"role"`
	// Primary 主要站模型名
	Primary string `json:"primary"`
	// Backup 备用站模型名
	Backup string `json:"backup"`
	// PrimaryName 主要站端点名
	PrimaryName string `json:"primaryName"`
	// BackupName 备用站端点名
	BackupName string `json:"backupName"`
	// ActiveName 当前生效端点名
	ActiveName string `json:"activeName"`
	// ActiveModel 当前生效模型名
	ActiveModel string `json:"activeModel"`
	// Override 会话级临时模型（空 = 跟随端点默认）
	Override string `json:"override"`
}

// BuildAPIRoleJSON 端点角色信息 JSON——GET /api/v1/api-role 数据源（对话页 [API:端点名[模型]] 标签）。
func BuildAPIRoleJSON(sess *session.Session, store *config.LlmAPIStore, apiConfigID string) string {
	role := "主要"
	override := ""
	if sess != nil {
		role = sess.APIRoleText()
		override = sess.ModelOverride()
	}
	primaryModel := ""
	primaryName := ""
	backupModel := ""
	backupName := ""
	if store != nil {
		if primary := resolvePrimaryConfig(store, apiConfigID); primary != nil {
			primaryModel = primary.DefaultModel
			primaryName = primary.DisplayName
		}
		if backup := resolveBackupConfig(store); backup != nil {
			backupModel = backup.DefaultModel
			backupName = backup.DisplayName
		}
	}
	useBackup := role == "备用"
	activeName := primaryName
	activeModel := primaryModel
	if useBackup {
		activeName = backupName
		activeModel = backupModel
	}
	if override != "" {
		activeModel = override
	}
	return jsonText(apiRolePayload{
		Role:        role,
		Primary:     primaryModel,
		Backup:      backupModel,
		PrimaryName: primaryName,
		BackupName:  backupName,
		ActiveName:  activeName,
		ActiveModel: activeModel,
		Override:    override,
	})
}

// apiModelsPayload 模型清单（字段序对齐 C# BuildApiModelsJsonAsync）。
type apiModelsPayload struct {
	// OK 探取成功
	OK bool `json:"ok"`
	// APIConfigID 目标端点身份
	APIConfigID string `json:"apiConfigId,omitempty"`
	// EndpointName 端点显示名
	EndpointName string `json:"endpointName,omitempty"`
	// Current 端点默认模型
	Current string `json:"current,omitempty"`
	// Models 模型名清单（沿用上游顺序——不排序）
	Models []string `json:"models,omitempty"`
	// Error 失败原因
	Error string `json:"error,omitempty"`
}

// BuildAPIModelsJSON 对话页模型清单 JSON——GET /api/v1/api-models 数据源。
//
// 目标站 = 当前生效端点（角色决定：备用态走备用站）；清单由上游 GET /models 实时探得。
func BuildAPIModelsJSON(store *config.LlmAPIStore, apiConfigID string, roleBackup bool, modelOverride string) string {
	if store == nil {
		return jsonText(apiModelsPayload{OK: false, Error: "配置池未绑定"})
	}
	id := resolveEffectiveConfigID(store, apiConfigID, roleBackup)
	if id == "" {
		return jsonText(apiModelsPayload{OK: false, Error: "当前生效端点未解析（池未绑定或无端点）"})
	}
	cfg, ok := store.TryGet(id)
	if !ok {
		return jsonText(apiModelsPayload{OK: false, Error: "端点配置不存在"})
	}
	modelsURL, _ := deriveProbeURLs(cfg.Endpoint)
	models := probeModelIDs(modelsURL, store.Secret(id))
	if models == nil {
		return jsonText(apiModelsPayload{OK: false, Error: "模型清单探取失败（上游无响应或格式不符）"})
	}
	return jsonText(apiModelsPayload{
		OK:           true,
		APIConfigID:  id,
		EndpointName: cfg.DisplayName,
		Current:      cfg.DefaultModel,
		Models:       models,
	})
}

// probeModelIDs 模型名清单探针——GET {base}/models（带 Bearer Key）；失败返回 nil（不抛）。
func probeModelIDs(url, apiKey string) []string {
	if url == "" {
		return nil
	}
	response := probeGET(url, apiKey)
	if response.Error != "" || response.HTTPStatus != 200 {
		return nil
	}
	var doc any
	if err := json.Unmarshal([]byte(response.Body), &doc); err != nil {
		return nil
	}
	root, isObject := doc.(map[string]any)
	if !isObject {
		return nil
	}
	data, isArray := root["data"].([]any)
	if !isArray {
		return nil
	}
	names := make([]string, 0, len(data))
	for _, raw := range data {
		item, _ := raw.(map[string]any)
		if name := getProbeString(item, "id"); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// effortPayload 思考强度信息（{effort, global}）。
type effortPayload struct {
	// Effort 本猫覆盖（空 = 跟随全局）
	Effort string `json:"effort"`
	// Global 全局值
	Global string `json:"global"`
}

// effortSetPayload 思考强度写入回执（{ok, effort}）。
type effortSetPayload struct {
	// OK 受理标志
	OK bool `json:"ok"`
	// Effort 归一后的覆盖值（空 = 跟随全局）
	Effort string `json:"effort"`
}

// effortFailPayload 思考强度失败回执。
type effortFailPayload struct {
	// OK 恒 false
	OK bool `json:"ok"`
	// Error 失败原因
	Error string `json:"error"`
}

// BuildEffortJSON 思考强度信息 JSON——GET /api/v1/effort 数据源（本猫覆盖 + 全局值）。
func (d *Deps) BuildEffortJSON(catKey string) string {
	effort := ""
	if catKey != "" && config.IsSafeCatKey(catKey) {
		if cfg, err := config.LoadCatCfg(d.catCfgPath(catKey)); err == nil && cfg != nil {
			effort = config.NormalizeReasoningEffort(cfg.ReasoningEffort)
		}
	}
	return jsonText(effortPayload{Effort: effort, Global: d.defaultReasoningEffort()})
}

// SetEffortJSON 思考强度写入——POST /api/v1/effort 数据源（仅本猫覆盖，空 = 清空跟随全局）。
//
// 落盘原子写；运行时生效走既有 Apply 回调（与管理面 cat-config 同通道，不造第二份生效逻辑）。
func (d *Deps) SetEffortJSON(catKey, value string) string {
	if catKey == "" || !config.IsSafeCatKey(catKey) {
		return jsonText(effortFailPayload{OK: false, Error: "cat 参数非法"})
	}
	raw := strings.TrimSpace(value)
	normalized := config.NormalizeReasoningEffort(raw)
	if normalized == "" && raw != "" {
		return jsonText(effortFailPayload{OK: false, Error: "reasoningEffort 非法（low/high/max/空）"})
	}
	path := d.catCfgPath(catKey)
	cfg, err := config.LoadCatCfg(path)
	if err != nil || cfg == nil {
		return jsonText(effortFailPayload{OK: false, Error: "cat.cfg 不存在: " + catKey})
	}
	cfg.ReasoningEffort = normalized
	if err := config.SaveCatCfg(path, cfg); err != nil {
		return jsonText(effortFailPayload{OK: false, Error: "cat.cfg 写入失败: " + err.Error()})
	}
	if d.Apply != nil {
		d.Apply(catKey, cfg)
	}
	note := normalized
	if note == "" {
		note = "跟随全局"
	}
	d.log(1, "思考强度已受理："+catKey+"（"+note+"）")
	return jsonText(effortSetPayload{OK: true, Effort: normalized})
}

// resolvePrimaryConfig 解析主要站配置——猫绑定身份（空 = 默认端点语义；不受会话角色影响）。
func resolvePrimaryConfig(store *config.LlmAPIStore, apiConfigID string) *config.LlmAPIConfig {
	if store == nil {
		return nil
	}
	if config.IsZeroGUID(apiConfigID) {
		return resolveDefaultConfig(store)
	}
	if cfg, ok := store.TryGet(apiConfigID); ok {
		return &cfg
	}
	return nil
}

// resolveDefaultConfig 默认端点（IsDefault 标记）。
func resolveDefaultConfig(store *config.LlmAPIStore) *config.LlmAPIConfig {
	for _, cfg := range store.Configs() {
		if cfg.IsDefault {
			out := cfg
			return &out
		}
	}
	return nil
}

// resolveBackupConfig 备用端点（IsBackup 标记）。
func resolveBackupConfig(store *config.LlmAPIStore) *config.LlmAPIConfig {
	for _, cfg := range store.Configs() {
		if cfg.IsBackup {
			out := cfg
			return &out
		}
	}
	return nil
}

// resolveEffectiveConfigID 当前生效端点的配置身份——角色决定（备用态走备用站；无备用回落主要站）。
func resolveEffectiveConfigID(store *config.LlmAPIStore, apiConfigID string, roleBackup bool) string {
	if store == nil {
		return ""
	}
	if roleBackup {
		if backup := resolveBackupConfig(store); backup != nil {
			return backup.APIConfigID
		}
	}
	if primary := resolvePrimaryConfig(store, apiConfigID); primary != nil {
		return primary.APIConfigID
	}
	return ""
}
