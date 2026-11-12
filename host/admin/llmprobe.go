package admin

// LLM API 池连通性三探——POST /api/v1/llm-apis/probe（配置页「测试」按钮数据源）。
//
// 逐字对齐 C# `CatHome4.Admin/AdminService.LlmProbe.cs`（490 行）：
// 三探 = GET <base>/models（Bearer——验证 Key 有效）+ GET <origin>/api/status（免 Key 站点信息）
// + GET <origin>/api/pricing（免 Key 定价与分组）；ok = 模型探通过；三段各自独立成败
// （上游非 new-api 站时站点两探 404，不拖垮模型探——面板按段分块展示）。
// 端点推导：去尾 /chat/completions 得模型基址；站点根再去尾 /v1。
// 单探 15 s；站点信息剔除 announcements / announcements_enabled（会话性内容不进报告）。
// 密钥只判 hasKey，不回显。传输异常文本 <类型>|<消息>（类型名按 C# 常见类名映射——语言方言）。

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"cathome5/host/config"
	"cathome5/host/llm"
)

// llmProbeTimeout 单探超时（C# LlmProbeTimeoutSeconds = 15）。
const llmProbeTimeout = 15 * time.Second

// llmProbeSiteExcludes 站点信息探针剔除项——公告属会话性内容，不进测试报告（Rushiera 2026-10-01 定）。
var llmProbeSiteExcludes = []string{"announcements", "announcements_enabled"}

// llmProbeClient 探针共享 HTTP 客户端——客户端级超时关闭，改由单次请求 context 控制（分探计时与超时互不干扰）。
var llmProbeClient = &http.Client{}

// llmProbeSection 是探针分段结果——成败 + 状态码 + 错误 + 结构化载荷。
type llmProbeSection struct {
	// OK 本段是否可用（HTTP 200 且响应形态符合预期）
	OK bool `json:"ok"`
	// HTTPStatus HTTP 状态码（0 = 传输异常未取到响应）
	HTTPStatus int `json:"httpStatus"`
	// Error 失败原因（成功时为空串）
	Error string `json:"error"`
	// Payload 结构化载荷（模型段 = 数组 / 站点段与定价段 = 对象；失败 = null）
	Payload any `json:"payload"`
}

// llmProbeModelItem 是模型清单条目（字段序对齐 C# 匿名对象 id / ownedBy / endpointTypes）。
type llmProbeModelItem struct {
	// ID 模型 id
	ID string `json:"id"`
	// OwnedBy 归属方
	OwnedBy string `json:"ownedBy"`
	// EndpointTypes 支持的端点类型（原样透传——缺失 = null）
	EndpointTypes any `json:"endpointTypes"`
}

// llmPricingPayload 是定价探针载荷（字段序对齐 C# 字典插入序）。
type llmPricingPayload struct {
	// Models 模型倍率（上游 data）
	Models any `json:"models"`
	// GroupRatio 分组倍率
	GroupRatio any `json:"groupRatio"`
	// UsableGroup 可用分组
	UsableGroup any `json:"usableGroup"`
	// Vendors 供应商
	Vendors any `json:"vendors"`
	// SupportedEndpoint 支持的端点
	SupportedEndpoint any `json:"supportedEndpoint"`
	// AutoGroups 自动分组
	AutoGroups any `json:"autoGroups"`
}

// llmProbeReport 是探针报告（字段序对齐 C# HandleLlmApisProbe 匿名对象）。
type llmProbeReport struct {
	// OK 模型探通过（Key 可用判据）
	OK bool `json:"ok"`
	// APIConfigID 配置身份（D 形态）
	APIConfigID string `json:"apiConfigId"`
	// DisplayName 显示名
	DisplayName string `json:"displayName"`
	// Endpoint 池配置 endpoint
	Endpoint string `json:"endpoint"`
	// DefaultModel 默认模型
	DefaultModel string `json:"defaultModel"`
	// HasKey 是否已配置密钥（密钥不回显）
	HasKey bool `json:"hasKey"`
	// ModelsURL 模型清单 URL
	ModelsURL string `json:"modelsUrl"`
	// SiteOrigin 站点根 URL
	SiteOrigin string `json:"siteOrigin"`
	// ProbedAt 探测时刻（yyyy-MM-dd HH:mm:ss）
	ProbedAt string `json:"probedAt"`
	// ElapsedMs 三探总耗时（毫秒）
	ElapsedMs int `json:"elapsedMs"`
	// Models 模型探段
	Models llmProbeSection `json:"models"`
	// Site 站点探段
	Site llmProbeSection `json:"site"`
	// Pricing 定价探段
	Pricing llmProbeSection `json:"pricing"`
}

// handleLlmApisProbe 服务 POST /api/v1/llm-apis/probe——连通性三探。
func (d *Deps) handleLlmApisProbe(w http.ResponseWriter, r *http.Request) {
	// [段1] 参数解析与配置解析
	fields, ok := readBodyFields(r)
	if !ok {
		writeErr(w, "body 非 JSON")
		return
	}
	rawID := fieldString(fields, "apiConfigId")
	parsed := config.ParseGUID(rawID)
	if parsed == "" || config.IsZeroGUID(parsed) {
		writeErr(w, "apiConfigId 非法")
		return
	}
	if d.LLMStore == nil {
		writeErr(w, "配置池未绑定")
		return
	}
	cfg, exists := d.LLMStore.TryGet(parsed)
	if !exists {
		writeErr(w, "配置不存在")
		return
	}
	apiKey := d.LLMStore.Secret(parsed)
	// [段2] 端点推导——chat 端点 → 模型基址（去 /chat/completions）+ 站点根（去 /v1）
	modelsURL, siteOrigin := deriveProbeURLs(cfg.Endpoint)
	// [段3] 三探串行——各段独立成败与错误留痕
	startedAt := time.Now()
	modelsSection := probeModels(modelsURL, apiKey)
	siteSection := probeSite(siteOrigin)
	pricingSection := probePricing(siteOrigin)
	elapsedMs := int(time.Since(startedAt).Milliseconds())
	// [段4] 报告组装与落盘日志
	d.log(1, "LLM API 池测试「"+cfg.DisplayName+"」：模型探 "+probeText(modelsSection.OK)+" / 站点探 "+probeText(siteSection.OK)+" / 定价探 "+probeText(pricingSection.OK))
	writeJSON(w, llmProbeReport{
		OK:           modelsSection.OK,
		APIConfigID:  parsed,
		DisplayName:  cfg.DisplayName,
		Endpoint:     cfg.Endpoint,
		DefaultModel: cfg.DefaultModel,
		HasKey:       apiKey != "",
		ModelsURL:    modelsURL,
		SiteOrigin:   siteOrigin,
		ProbedAt:     time.Now().Format("2006-01-02 15:04:05"),
		ElapsedMs:    elapsedMs,
		Models:       modelsSection,
		Site:         siteSection,
		Pricing:      pricingSection,
	})
}

// probeText 成败文案（日志行用）。
func probeText(ok bool) string {
	if ok {
		return "通过"
	}
	return "失败"
}

// deriveProbeURLs 端点推导——池配置 endpoint → 模型清单 URL + 站点根 URL。
//
// 规则：先去尾 /chat/completions 得模型基址；站点根再去尾 /v1（new-api 站点面挂在根下）。
func deriveProbeURLs(endpoint string) (modelsURL, siteOrigin string) {
	trimmed := strings.TrimSpace(endpoint)
	trimmed = strings.TrimRight(trimmed, "/")
	baseURL := trimmed
	const chatSuffix = "/chat/completions"
	if strings.HasSuffix(baseURL, chatSuffix) {
		baseURL = baseURL[:len(baseURL)-len(chatSuffix)]
	}
	modelsURL = baseURL + "/models"
	siteOrigin = baseURL
	if strings.HasSuffix(siteOrigin, "/v1") {
		siteOrigin = siteOrigin[:len(siteOrigin)-len("/v1")]
	}
	return modelsURL, siteOrigin
}

// probeModels 模型清单探针——GET /v1/models（带 Bearer Key）。ok = HTTP 200 且响应含 data 数组。
func probeModels(url, apiKey string) llmProbeSection {
	section := llmProbeSection{}
	response := probeGET(url, apiKey)
	section.HTTPStatus = response.HTTPStatus
	if response.Error != "" {
		section.Error = response.Error
		return section
	}
	if response.HTTPStatus != 200 {
		section.Error = "HTTP " + itoa(response.HTTPStatus) + " " + trimProbeBody(response.Body)
		return section
	}
	var doc any
	if err := json.Unmarshal([]byte(response.Body), &doc); err != nil {
		section.Error = "响应非 JSON：" + err.Error()
		return section
	}
	root, isObject := doc.(map[string]any)
	if !isObject {
		section.Error = "响应无 data 数组"
		return section
	}
	data, isArray := root["data"].([]any)
	if !isArray {
		section.Error = "响应无 data 数组"
		return section
	}
	items := make([]llmProbeModelItem, 0, len(data))
	for _, raw := range data {
		item, _ := raw.(map[string]any)
		items = append(items, llmProbeModelItem{
			ID:            getProbeString(item, "id"),
			OwnedBy:       getProbeString(item, "owned_by"),
			EndpointTypes: item["supported_endpoint_types"],
		})
	}
	section.Payload = items
	section.OK = true
	return section
}

// probeSite 站点信息探针——GET /api/status（免 Key）。公告段剔除（会话性内容不进报告）。
func probeSite(origin string) llmProbeSection {
	section := llmProbeSection{}
	if origin == "" {
		section.Error = "端点为空"
		return section
	}
	response := probeGET(origin+"/api/status", "")
	section.HTTPStatus = response.HTTPStatus
	if response.Error != "" {
		section.Error = response.Error
		return section
	}
	if response.HTTPStatus != 200 {
		section.Error = "HTTP " + itoa(response.HTTPStatus) + " " + trimProbeBody(response.Body)
		return section
	}
	var doc any
	if err := json.Unmarshal([]byte(response.Body), &doc); err != nil {
		section.Error = "响应非 JSON：" + err.Error()
		return section
	}
	root, isObject := doc.(map[string]any)
	if !isObject {
		section.Error = "响应无 data 对象"
		return section
	}
	data, isObject := root["data"].(map[string]any)
	if !isObject {
		section.Error = "响应无 data 对象"
		return section
	}
	fields := make(map[string]any, len(data))
	for name, value := range data {
		if isProbeSiteExcluded(name) {
			continue
		}
		fields[name] = value
	}
	section.Payload = fields
	section.OK = true
	return section
}

// probePricing 定价与分组探针——GET /api/pricing（免 Key）。
//
// 取 data（模型倍率）+ group_ratio + usable_group + vendors + supported_endpoint + auto_groups；
// C# 侧解析成功即 ok（不检查根形态——字段缺失输出 null）。
func probePricing(origin string) llmProbeSection {
	section := llmProbeSection{}
	if origin == "" {
		section.Error = "端点为空"
		return section
	}
	response := probeGET(origin+"/api/pricing", "")
	section.HTTPStatus = response.HTTPStatus
	if response.Error != "" {
		section.Error = response.Error
		return section
	}
	if response.HTTPStatus != 200 {
		section.Error = "HTTP " + itoa(response.HTTPStatus) + " " + trimProbeBody(response.Body)
		return section
	}
	var doc any
	if err := json.Unmarshal([]byte(response.Body), &doc); err != nil {
		section.Error = "响应非 JSON：" + err.Error()
		return section
	}
	root, _ := doc.(map[string]any)
	section.Payload = llmPricingPayload{
		Models:            getProbeValue(root, "data"),
		GroupRatio:        getProbeValue(root, "group_ratio"),
		UsableGroup:       getProbeValue(root, "usable_group"),
		Vendors:           getProbeValue(root, "vendors"),
		SupportedEndpoint: getProbeValue(root, "supported_endpoint"),
		AutoGroups:        getProbeValue(root, "auto_groups"),
	}
	section.OK = true
	return section
}

// probeHTTPResponse 是探针 HTTP 原始结果——状态码 + 响应体 + 传输异常。
type probeHTTPResponse struct {
	// HTTPStatus HTTP 状态码（0 = 传输异常）
	HTTPStatus int
	// Body 响应体原文
	Body string
	// Error 传输异常描述（空 = 无异常）
	Error string
}

// probeGET 单次 GET 探针——带可选 Bearer 头；请求级超时；传输异常按类型名与消息留痕。
func probeGET(url, apiKey string) probeHTTPResponse {
	result := probeHTTPResponse{}
	if url == "" {
		result.Error = "URL 为空"
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), llmProbeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		result.Error = "UriFormatException|" + err.Error()
		return result
	}
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	// 自家 apis 入站自报（只对 127.0.0.1:18100 生效；不改任何缓存键——C# LlmSelfTag.Apply 同口径）
	llm.ApplySelfTag(request, url, nil, "models")
	response, err := llmProbeClient.Do(request)
	if err != nil {
		result.Error = probeTransportError(err)
		return result
	}
	defer func() { _ = response.Body.Close() }()
	body, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		result.Error = "IOException|" + readErr.Error()
		return result
	}
	result.HTTPStatus = response.StatusCode
	result.Body = string(body)
	return result
}

// probeTransportError 传输异常文本——类型名按 C# 常见类名映射（超时 / 其它）。
func probeTransportError(err error) string {
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "TaskCanceledException|" + err.Error()
	}
	return "HttpRequestException|" + err.Error()
}

// isProbeSiteExcluded 站点字段剔除判定——公告类字段不进测试报告。
func isProbeSiteExcluded(name string) bool {
	for _, exclude := range llmProbeSiteExcludes {
		if exclude == name {
			return true
		}
	}
	return false
}

// getProbeString 读取 JSON 对象字符串属性——防御式（缺字段 / 非字符串返回空串）。
func getProbeString(obj map[string]any, key string) string {
	if obj == nil {
		return ""
	}
	if text, ok := obj[key].(string); ok {
		return text
	}
	return ""
}

// getProbeValue 读取 JSON 对象属性——缺字段返回 nil（序列化为 null）。
func getProbeValue(obj map[string]any, key string) any {
	if obj == nil {
		return nil
	}
	return obj[key]
}

// trimProbeBody 截断响应体——失败摘要用（去换行；至多 200 码元 + "…"，UTF-16 口径对齐 C#）。
func trimProbeBody(body string) string {
	flat := strings.ReplaceAll(body, "\r", " ")
	flat = strings.ReplaceAll(flat, "\n", " ")
	return truncateUTF16(flat, 200)
}

// truncateUTF16 按 UTF-16 码元截断——超限追加 "…"（对齐 C# Substring(0, 200)）。
func truncateUTF16(text string, limit int) string {
	units := 0
	for index, r := range text {
		width := 1
		if r > 0xFFFF {
			width = 2
		}
		if units+width > limit {
			return text[:index] + "…"
		}
		units += width
	}
	return text
}
