package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"cathome5/host/session"
)

// 默认输出上限——显式传入（不带 max_tokens 时上游按自家默认截断）。
const defaultMaxTokens = 131072

// 输出上限合法上界。
const maxMaxTokens = 131072

// 默认兜底模型名。
const fallbackModel = "deepseek-v4-flash"

// 请求重试上限（同端点重试 3 次，共 4 次尝试）。
const maxRetries = 3

// Client 是 DeepSeek 兼容流式客户端——端点 / 模型 / 密钥实时解析（改配置即时生效）。
type Client struct {
	// Pool 端点池（可 nil——回落环境变量）
	Pool *Pool
	// APIConfigID 猫绑定身份（空 / 全零 = 跟随默认端点）
	//
	// 🔴 运行时只经 SetBinding 写（HTTP 线程写 / 轮执行读，加锁保护）；
	// 构造期直接赋值是安全的（尚无并发读者）。
	APIConfigID string
	// ModelOverride 会话级模型覆盖（空 = 跟随端点默认）
	ModelOverride string
	// ReasoningEffort 每猫思考强度覆盖（空 = 跟随全局）——写入口径同 APIConfigID
	ReasoningEffort string
	// Settings 全局配置读取（键 → 值；nil = 全默认）
	Settings func(key string) string
	// Failover 故障转移判定钩子（nil = 不启用；返回 true 表示已切换、可重发）
	Failover func(errorText string) bool
	// SelfTag 入站自报身份提供器（nil = 不发自报头；仅自家 apis——回环 + 18100——生效）
	SelfTag func() session.SelfTag
	// HTTP 客户端（nil = 默认 60 秒超时）
	HTTP *http.Client
	// mu 绑定面读写锁——APIConfigID / ReasoningEffort 的跨线程读写
	mu sync.RWMutex
	// maxTokensWarned 非法 max_tokens 已告警标记（进程内一次）
	maxTokensWarned bool
	// failoverRole 故障转移目标角色文本（默认「备用」）
	failoverRole string
}

// SetBinding 切换运行时绑定（端点身份 + 思考强度）——catcfg.apply 的生效半边。
//
// 写面在 HTTP 线程、读面在轮执行线程：经锁写入，读者下一次解析即见新值
// （不改 Pool / Settings，只改本次运行的猫级覆盖）。
func (c *Client) SetBinding(apiConfigID, reasoningEffort string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.APIConfigID = apiConfigID
	c.ReasoningEffort = reasoningEffort
}

// bindingID 读当前端点身份（加锁）。
func (c *Client) bindingID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.APIConfigID
}

// effortOverride 读当前思考强度覆盖（加锁）。
func (c *Client) effortOverride() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ReasoningEffort
}

// Params 解析本次请求的生成参数。
func (c *Client) Params() Params {
	return Params{
		Model:           c.model(),
		MaxTokens:       c.maxTokens(),
		Thinking:        c.thinkingEnabled(),
		ReasoningEffort: c.effectiveEffort(),
	}
}

// BaseURL 解析完整 chat completions 端点（实时）。
func (c *Client) BaseURL() string {
	if config := c.target(); config != nil && config.Endpoint != "" {
		return DeriveChatEndpoint(config.Endpoint)
	}
	if env := strings.TrimSpace(os.Getenv("MAU_LLM_BASE_URL")); env != "" {
		return DeriveChatEndpoint(env)
	}
	return ""
}

// APIKey 解析密钥（secrets → 环境变量）。
func (c *Client) APIKey() string {
	if config := c.target(); config != nil {
		if secret := c.Pool.Secret(config.APIConfigID); secret != "" {
			return secret
		}
	}
	if env := strings.TrimSpace(os.Getenv("MAU_LLM_API_KEY")); env != "" {
		return env
	}
	return strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
}

// target 解析目标端点配置——空身份 = 默认端点；显式身份未命中 = nil（回落环境变量）。
func (c *Client) target() *APIConfig {
	if c.Pool == nil {
		return nil
	}
	binding := c.bindingID()
	if IsZeroGUID(binding) {
		return c.Pool.ResolveDefault()
	}
	return c.Pool.TryGet(binding)
}

// model 模型名——会话覆盖 → 端点默认 → 兜底。
func (c *Client) model() string {
	if c.ModelOverride != "" {
		return c.ModelOverride
	}
	if config := c.target(); config != nil && config.DefaultModel != "" {
		return config.DefaultModel
	}
	return fallbackModel
}

// setting 读全局配置（键 → 值；Settings 未接线 = 空）。
func (c *Client) setting(key string) string {
	if c.Settings == nil {
		return ""
	}
	return c.Settings(key)
}

// thinkingEnabled 思考模式——全局配置 → 环境变量 → 默认 enabled。
func (c *Client) thinkingEnabled() bool {
	value := c.setting("llm.thinking")
	if value == "" {
		value = strings.TrimSpace(os.Getenv("MAU_LLM_THINKING"))
	}
	if value == "" {
		value = "enabled"
	}
	return strings.ToLower(strings.TrimSpace(value)) == "enabled"
}

// effectiveEffort 推理强度——每猫覆盖优先（合法才用）→ 全局三层 → 默认 high。
func (c *Client) effectiveEffort() string {
	if override := c.effortOverride(); override != "" {
		normalized := strings.ToLower(strings.TrimSpace(override))
		if normalized == "low" || normalized == "high" || normalized == "max" {
			return normalized
		}
	}
	value := c.setting("llm.reasoning_effort")
	if value == "" {
		value = strings.TrimSpace(os.Getenv("MAU_LLM_REASONING_EFFORT"))
	}
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "low" || normalized == "high" || normalized == "max" {
		return normalized
	}
	return "high"
}

// maxTokens 单次输出上限——全局配置 → 环境变量 → 默认；非法回落默认并告警一次。
func (c *Client) maxTokens() int {
	value := c.setting("llm.max_tokens")
	if value == "" {
		value = strings.TrimSpace(os.Getenv("MAU_LLM_MAX_TOKENS"))
	}
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return defaultMaxTokens
	}
	parsed, err := strconv.Atoi(normalized)
	if err == nil && parsed > 0 && parsed <= maxMaxTokens {
		return parsed
	}
	if !c.maxTokensWarned {
		c.maxTokensWarned = true
		logAt(2, "llm.max_tokens 非法（"+normalized+"）——回落 "+itoa(defaultMaxTokens))
	}
	return defaultMaxTokens
}

// httpClient 取 HTTP 客户端（默认 60 秒超时）。
func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

// Stream 发起流式对话——事件按到达序回调；错误全部事件化（不抛）。
//
// 重试语义与 C# ChatStream 一致：传输错误 / 429 / 5xx 重试至多 3 次；
// 无回应超时不重试（直接进故障转移判定）；4xx 不重试；流中断不重试（事件化给上层续传）。
func (c *Client) Stream(ctx context.Context, messages []session.Message, tools []session.ToolSpec, userID string, emit func(Event)) {
	body := BuildChatRequestBody(messages, tools, c.Params(), userID)
	retryCount := 0
	for {
		endpoint := c.BaseURL()
		if endpoint == "" {
			emit(Event{Kind: EventError, Text: "ERR|TRANSPORT|未配置 LLM 端点（llm-api.json 无默认端点且 MAU_LLM_BASE_URL 为空）"})
			return
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
		if err != nil {
			emit(Event{Kind: EventError, Text: "ERR|TRANSPORT|*url.Error|" + err.Error()})
			return
		}
		request.Header.Set("Authorization", "Bearer "+c.APIKey())
		request.Header.Set("Accept", "text/event-stream")
		request.Header.Set("Content-Type", "application/json; charset=utf-8")
		if strings.Contains(strings.ToLower(endpoint), "mewbius.com") {
			request.Header.Set("User-Agent", "WorkBuddy/1.0")
		}
		sessionKey := "default"
		if userID != "" {
			sessionKey = userID
		}
		request.Header.Set("x-opencode-session", "cat-home4-"+sessionKey)
		// 自家 apis 入站自报——只加新头；上一行缓存隔离键原样保留（换它 = 换上游缓存槽）
		if c.SelfTag != nil {
			tag := c.SelfTag()
			ApplySelfTag(request, endpoint, &tag, "chat")
		}

		response, sendErr := c.httpClient().Do(request)
		if sendErr != nil {
			if ctx.Err() != nil {
				return
			}
			netError := transportErrorText(sendErr, c.httpClient().Timeout)
			noReplyTimeout := strings.HasPrefix(netError, "ERR|TRANSPORT|Timeout")
			if !noReplyTimeout && retryCount < maxRetries {
				retryCount++
				logAt(2, "LLM 请求传输失败，自动重试（"+itoa(retryCount)+"/"+itoa(maxRetries)+"）："+trimText(netError, 200))
				emit(Event{Kind: EventRetrying, Text: "RETRY|" + itoa(retryCount) + "/" + itoa(maxRetries) + "|" + trimText(netError, 200)})
				if !sleepWithContext(ctx, retryDelay(retryCount-1)) {
					return
				}
				emit(Event{Kind: EventRetryResume, Text: ""})
				continue
			}
			if c.tryFailover(netError, emit) {
				retryCount = 0
				continue
			}
			emit(Event{Kind: EventError, Text: netError})
			return
		}

		if response.StatusCode < 200 || response.StatusCode >= 300 {
			status := response.StatusCode
			retryable := status == 429 || status >= 500
			if retryable && retryCount < maxRetries {
				retryCount++
				logAt(2, "LLM 请求返回 HTTP "+itoa(status)+"，自动重试（"+itoa(retryCount)+"/"+itoa(maxRetries)+"）")
				closeBody(response)
				emit(Event{Kind: EventRetrying, Text: "RETRY|" + itoa(retryCount) + "/" + itoa(maxRetries) + "|HTTP " + itoa(status)})
				if !sleepWithContext(ctx, retryDelay(retryCount-1)) {
					return
				}
				emit(Event{Kind: EventRetryResume, Text: ""})
				continue
			}
			raw, readErr := io.ReadAll(response.Body)
			closeBody(response)
			if readErr != nil {
				if ctx.Err() != nil {
					return
				}
				emit(Event{Kind: EventError, Text: "ERR|TRANSPORT|*url.Error|" + readErr.Error()})
				return
			}
			httpError := ParseErrorText(status, string(raw))
			if c.tryFailover(httpError, emit) {
				retryCount = 0
				continue
			}
			emit(Event{Kind: EventError, Text: httpError})
			return
		}

		TranslateSSE(response.Body, emit)
		closeBody(response)
		return
	}
}

// tryFailover 故障转移判定——钩子返回 true 时产出 Failover + RetryResume 事件。
func (c *Client) tryFailover(errorText string, emit func(Event)) bool {
	if c.Failover == nil || !c.Failover(errorText) {
		return false
	}
	role := c.failoverRole
	if role == "" {
		role = "备用"
	}
	emit(Event{Kind: EventFailover, Text: "FAILOVER|" + role + "|" + trimText(errorText, 200)})
	emit(Event{Kind: EventRetryResume, Text: ""})
	return true
}

// transportErrorText 传输错误文本——超时与其它错误分档。
func transportErrorText(err error, timeout time.Duration) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		seconds := int(timeout / time.Second)
		return "ERR|TRANSPORT|Timeout|请求发出后 " + itoa(seconds) + " 秒无回应"
	}
	name := "TransportError"
	if urlErr != nil {
		name = "HttpRequestException"
	}
	return "ERR|TRANSPORT|" + name + "|" + err.Error()
}

// ParseErrorText 解析错误响应——官方 error{type,message} / 代理外层 type 双形态兜底。
func ParseErrorText(statusCode int, raw string) string {
	errorType := ""
	message := ""
	if raw != "" {
		if typeValue, messageValue, ok := parseErrorJSON(raw); ok {
			errorType = typeValue
			message = messageValue
		} else {
			message = trimText(raw, 200)
		}
	}
	if errorType == "" {
		errorType = "HTTP_" + itoa(statusCode)
	}
	detail := message
	if detail == "" {
		detail = raw
	}
	return "ERR|" + errorType + "|" + trimText(detail, 200)
}

// parseErrorJSON 解析错误对象——返回（type, message, 成功）。
func parseErrorJSON(raw string) (string, string, bool) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return "", "", false
	}
	if nested, exists := root["error"]; exists {
		var inner map[string]json.RawMessage
		if err := json.Unmarshal(nested, &inner); err == nil {
			return rawStringField(inner, "type"), rawStringField(inner, "message"), true
		}
	}
	if value, exists := root["type"]; exists {
		return rawToString(value), "", true
	}
	return "", "", false
}

// rawStringField 取对象字符串字段（缺失 / 非字符串 = 空串）。
func rawStringField(obj map[string]json.RawMessage, key string) string {
	value, exists := obj[key]
	if !exists {
		return ""
	}
	return rawToString(value)
}

// rawToString 原始 JSON 值转字符串（非字符串 = 空串）。
func rawToString(value json.RawMessage) string {
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return ""
	}
	return text
}

// trimText 截断文本（按字符——C# Substring 的字符面等价）。
func trimText(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max])
}

// retryDelay 重试退避——递增（500ms × (attempt+1)，上限 2s）。
func retryDelay(attempt int) time.Duration {
	ms := 500 * (attempt + 1)
	if ms > 2000 {
		ms = 2000
	}
	return time.Duration(ms) * time.Millisecond
}

// sleepWithContext 可取消等待——ctx 取消返回 false。
func sleepWithContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// closeBody 关闭响应体（错误忽略——读取阶段已结束）。
func closeBody(response *http.Response) {
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
}
