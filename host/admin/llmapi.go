package admin

import (
	"crypto/rand"
	"fmt"
	"net/http"

	"cathome5/host/config"
)

// llmAPIItem 是端点池列表项（字段序对齐 C# HandleLlmApisGet 的匿名对象）。
type llmAPIItem struct {
	// APIConfigID 配置身份
	APIConfigID string `json:"apiConfigId"`
	// DisplayName 显示名
	DisplayName string `json:"displayName"`
	// APIType 协议类型
	APIType string `json:"apiType"`
	// Endpoint 端点
	Endpoint string `json:"endpoint"`
	// DefaultModel 默认模型
	DefaultModel string `json:"defaultModel"`
	// IsDefault 默认标记
	IsDefault bool `json:"isDefault"`
	// IsBackup 备用标记
	IsBackup bool `json:"isBackup"`
	// APIKey 掩码后的密钥（空 = 未配置）
	APIKey string `json:"apiKey"`
	// HasKey 是否已配置密钥
	HasKey bool `json:"hasKey"`
}

// llmAPIListResponse 是 GET /api/v1/llm-apis 的响应体。
type llmAPIListResponse struct {
	// Version 协议版本（恒 1）
	Version int `json:"version"`
	// Items 配置项列表
	Items []llmAPIItem `json:"items"`
}

// llmAPISaveResponse 是新建 / 编辑 / 删除的回执。
type llmAPISaveResponse struct {
	// OK 受理标志
	OK bool `json:"ok"`
	// APIConfigID 目标配置身份
	APIConfigID string `json:"apiConfigId"`
}

// handleLlmApisGet 服务 GET /api/v1/llm-apis——池列表（key 掩码；hasKey 供前端判断是否已配置）。
func (d *Deps) handleLlmApisGet(w http.ResponseWriter, r *http.Request) {
	items := make([]llmAPIItem, 0, 8)
	if d.LLMStore != nil {
		for _, cfg := range d.LLMStore.Configs() {
			secret := d.LLMStore.Secret(cfg.APIConfigID)
			shown := ""
			if secret != "" {
				shown = config.MaskAPIKey(secret)
			}
			items = append(items, llmAPIItem{
				APIConfigID:  cfg.APIConfigID,
				DisplayName:  cfg.DisplayName,
				APIType:      cfg.APIType,
				Endpoint:     cfg.Endpoint,
				DefaultModel: cfg.DefaultModel,
				IsDefault:    cfg.IsDefault,
				IsBackup:     cfg.IsBackup,
				APIKey:       shown,
				HasKey:       secret != "",
			})
		}
	}
	writeJSON(w, llmAPIListResponse{Version: 1, Items: items})
}

// handleLlmApisPost 服务 POST /api/v1/llm-apis——新建配置。
func (d *Deps) handleLlmApisPost(w http.ResponseWriter, r *http.Request) {
	fields, ok := readBodyFields(r)
	if !ok {
		writeErr(w, "body 非 JSON")
		return
	}
	displayName := fieldString(fields, "displayName")
	apiType := fieldString(fields, "apiType")
	endpoint := fieldString(fields, "endpoint")
	defaultModel := fieldString(fields, "defaultModel")
	apiKey := fieldString(fields, "apiKey")
	if displayName == "" {
		writeErr(w, "displayName 为空")
		return
	}
	if endpoint == "" {
		writeErr(w, "endpoint 为空")
		return
	}
	if d.LLMStore == nil {
		writeErr(w, "配置池未绑定")
		return
	}
	if apiType == "" {
		apiType = "deepseek"
	}
	cfg := config.LlmAPIConfig{
		SchemaVersion: 1,
		APIConfigID:   newGUID(),
		DisplayName:   displayName,
		APIType:       apiType,
		Endpoint:      endpoint,
		DefaultModel:  defaultModel,
	}
	if err := d.LLMStore.Save(cfg, apiKey); err != nil {
		writeErr(w, err.Error())
		return
	}
	d.log(1, "LLM API 池新增配置「"+displayName+"」（"+cfg.APIConfigID+"）")
	writeJSON(w, llmAPISaveResponse{OK: true, APIConfigID: cfg.APIConfigID})
}

// handleLlmApisEdit 服务 POST /api/v1/llm-apis/edit——编辑配置（apiKey 空 = 保留原 key）。
func (d *Deps) handleLlmApisEdit(w http.ResponseWriter, r *http.Request) {
	fields, ok := readBodyFields(r)
	if !ok {
		writeErr(w, "body 非 JSON")
		return
	}
	apiConfigID := fieldString(fields, "apiConfigId")
	displayName := fieldString(fields, "displayName")
	apiType := fieldString(fields, "apiType")
	endpoint := fieldString(fields, "endpoint")
	defaultModel := fieldString(fields, "defaultModel")
	apiKey := fieldString(fields, "apiKey")
	parsed := config.ParseGUID(apiConfigID)
	if parsed == "" || config.IsZeroGUID(parsed) {
		writeErr(w, "apiConfigId 非法")
		return
	}
	if displayName == "" {
		writeErr(w, "displayName 为空")
		return
	}
	if endpoint == "" {
		writeErr(w, "endpoint 为空")
		return
	}
	if d.LLMStore == nil {
		writeErr(w, "配置池未绑定")
		return
	}
	cfg := config.LlmAPIConfig{
		SchemaVersion: 1,
		APIConfigID:   parsed,
		DisplayName:   displayName,
		APIType:       apiType,
		Endpoint:      endpoint,
		DefaultModel:  defaultModel,
	}
	if err := d.LLMStore.Save(cfg, apiKey); err != nil {
		writeErr(w, err.Error())
		return
	}
	keyNote := "保持原值"
	if apiKey != "" {
		keyNote = "已更换"
	}
	d.log(1, "LLM API 池「"+displayName+"」已更新（"+parsed+"），密钥"+keyNote)
	writeJSON(w, llmAPISaveResponse{OK: true, APIConfigID: parsed})
}

// handleLlmApisDelete 服务 POST /api/v1/llm-apis/delete——删除配置（普通配置 + key 同删）。
func (d *Deps) handleLlmApisDelete(w http.ResponseWriter, r *http.Request) {
	fields, ok := readBodyFields(r)
	if !ok {
		writeErr(w, "body 非 JSON")
		return
	}
	parsed := config.ParseGUID(fieldString(fields, "apiConfigId"))
	if parsed == "" || config.IsZeroGUID(parsed) {
		writeErr(w, "apiConfigId 非法")
		return
	}
	if d.LLMStore == nil {
		writeErr(w, "配置池未绑定")
		return
	}
	if !d.LLMStore.Delete(parsed) {
		writeErr(w, "配置不存在")
		return
	}
	d.log(1, "LLM API 池配置已删除："+parsed)
	writeJSON(w, llmAPISaveResponse{OK: true, APIConfigID: parsed})
}

// handleLlmApisDefault 服务 POST /api/v1/llm-apis/default——设为默认端点（唯一默认）。
func (d *Deps) handleLlmApisDefault(w http.ResponseWriter, r *http.Request) {
	fields, ok := readBodyFields(r)
	if !ok {
		writeErr(w, "body 非 JSON")
		return
	}
	parsed := config.ParseGUID(fieldString(fields, "apiConfigId"))
	if parsed == "" || config.IsZeroGUID(parsed) {
		writeErr(w, "apiConfigId 无效")
		return
	}
	if d.LLMStore == nil {
		writeErr(w, "配置池未绑定")
		return
	}
	if !d.LLMStore.SetDefault(parsed) {
		writeErr(w, "配置不存在")
		return
	}
	d.log(1, "LLM API 池已设为默认："+parsed)
	writeJSON(w, okResponse{OK: true})
}

// handleLlmApisBackup 服务 POST /api/v1/llm-apis/backup——设为 / 取消备用端点（唯一备用）。
func (d *Deps) handleLlmApisBackup(w http.ResponseWriter, r *http.Request) {
	fields, ok := readBodyFields(r)
	if !ok {
		writeErr(w, "body 非 JSON")
		return
	}
	raw := fieldString(fields, "apiConfigId")
	parsed := ""
	if raw != "" {
		parsed = config.ParseGUID(raw)
		if parsed == "" {
			writeErr(w, "apiConfigId 无效")
			return
		}
	}
	if d.LLMStore == nil {
		writeErr(w, "配置池未绑定")
		return
	}
	if !d.LLMStore.SetBackup(parsed) {
		writeErr(w, "配置不存在或已是默认端点（默认与备用互斥）")
		return
	}
	if parsed == "" {
		d.log(1, "LLM API 池已取消备用端点")
	} else {
		d.log(1, "LLM API 池已设为备用："+parsed)
	}
	writeJSON(w, okResponse{OK: true})
}

// newGUID 生成随机 GUID（v4 形态 D 文本）。
func newGUID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return ""
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		raw[0], raw[1], raw[2], raw[3], raw[4], raw[5], raw[6], raw[7],
		raw[8], raw[9], raw[10], raw[11], raw[12], raw[13], raw[14], raw[15])
}
