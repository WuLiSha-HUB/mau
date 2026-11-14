// Package llm 复现宿主 LLM 面：请求体序列化、SSE 帧翻译与端点解析。
//
// 边界：本包只做「wire 面」——消息数组与 tools 数组按 C# DeepSeekLlmRuntime 的
// 键序逐字节同构（判据 1 请求等价）；会话状态、工具批与关轮归 host/session。
package llm

import (
	"encoding/json"
	"net/url"
	"os"
	"strings"

	"cathome5/host/session"
)

// Params 是一次流式请求的生成参数——来源见 Settings（模型 / 上限 / 思考模式 / 推理强度）。
type Params struct {
	// Model 模型名（会话覆盖 → 端点池 defaultModel → 兜底）
	Model string
	// MaxTokens 输出上限（显式传入，不吃上游默认）
	MaxTokens int
	// Thinking 思考模式开关
	Thinking bool
	// ReasoningEffort 推理强度（low / high / max；仅 Thinking=true 时携带）
	ReasoningEffort string
}

// BuildChatRequestBody 构造流式对话请求体——键序与 C# BuildChatRequestBody 逐字节同构。
//
// 键序：model / messages / stream / max_tokens / stream_options / thinking /
// [reasoning_effort] / [tools] / [user_id]；空 tools 省略字段，空 userID 省略字段。
func BuildChatRequestBody(messages []session.Message, tools []session.ToolSpec, params Params, userID string) string {
	return buildChatRequestBody(messages, tools, params, userID, nil)
}

// buildChatRequestBody 预算 / 降级共用实现——keeps 命中的消息按「保留末尾 N 张」展开图片
// （缺省 = 全展开；0 = 全摘）。
func buildChatRequestBody(messages []session.Message, tools []session.ToolSpec, params Params, userID string, keeps map[int]int) string {
	var b strings.Builder
	b.WriteString(`{"model":`)
	b.WriteString(JSONString(params.Model))
	b.WriteString(`,"messages":[`)
	for i, msg := range messages {
		if i > 0 {
			b.WriteByte(',')
		}
		keep := -1
		if keeps != nil {
			if value, ok := keeps[i]; ok {
				keep = value
			}
		}
		b.WriteString(buildWireMessageMode(msg, keep))
	}
	b.WriteString(`],"stream":true`)
	b.WriteString(`,"max_tokens":`)
	b.WriteString(itoa(params.MaxTokens))
	b.WriteString(`,"stream_options":{"include_usage":true}`)
	if params.Thinking {
		b.WriteString(`,"thinking":{"type":"enabled"}`)
		b.WriteString(`,"reasoning_effort":`)
		b.WriteString(JSONString(params.ReasoningEffort))
	} else {
		b.WriteString(`,"thinking":{"type":"disabled"}`)
	}
	wireTools := buildWireTools(tools)
	if wireTools != "" {
		b.WriteString(`,"tools":`)
		b.WriteString(wireTools)
	}
	if userID != "" {
		b.WriteString(`,"user_id":`)
		b.WriteString(JSONString(userID))
	}
	b.WriteString("}")
	return b.String()
}

// buildWireMessage 单条消息序列化——四角色分派（C# 同构）。
func buildWireMessage(msg session.Message) string {
	return buildWireMessageMode(msg, -1)
}

// buildWireMessageMode 单条消息序列化——keepImages < 0 全展开；0 全摘；N 保留末尾 N 张。
func buildWireMessageMode(msg session.Message, keepImages int) string {
	switch msg.Role {
	case session.RoleSystem:
		return `{"role":"system","content":` + JSONString(msg.Content) + `}`
	case session.RoleUser:
		if msg.ImagesJSON != "" {
			return buildUserMessageWithImagesLimit(msg, keepImages)
		}
		return `{"role":"user","content":` + JSONString(msg.Content) + `}`
	case session.RoleAssistant:
		return buildAssistantMessage(msg)
	case session.RoleTool:
		content := msg.Content
		if content == "" {
			content = "(no output)"
		}
		return `{"role":"tool","tool_call_id":` + JSONString(msg.ToolCallID) + `,"content":` + JSONString(content) + `}`
	}
	// 未知角色——空对象（C# 对未匹配 role 不产出对象；此处保持数组元素位不偏移并出声）
	logAt(2, "LLM 请求消息角色未知（按空对象输出）: "+itoa(int(msg.Role)))
	return `{}`
}

// buildAssistantMessage assistant 序列化——tool_calls 规整透传（non-BMP 转义）+ reasoning_content 回传铁律。
//
// 有 tool_calls 必带 reasoning_content（含空串）；无 tool_calls 但思考非空也带。
func buildAssistantMessage(msg session.Message) string {
	var b strings.Builder
	b.WriteString(`{"role":"assistant","content":`)
	b.WriteString(JSONString(msg.Content))
	hasTools := msg.ToolCallsJSON != ""
	if hasTools {
		b.WriteString(`,"tool_calls":`)
		b.WriteString(session.JSONFragment(msg.ToolCallsJSON))
	}
	if hasTools || msg.ReasoningContent != "" {
		b.WriteString(`,"reasoning_content":`)
		b.WriteString(JSONString(msg.ReasoningContent))
	}
	b.WriteString("}")
	return b.String()
}

// buildUserMessageWithImages user 消息（带图片附件）——content 为内容块数组。
//
// 单图解析失败不阻断请求，失败信息并入 text 块（失败必须可见）；
// 无可用图片且正文为空 → text 兜底 "(图片)"。
func buildUserMessageWithImages(msg session.Message) string {
	return buildUserMessageWithImagesLimit(msg, -1)
}

// buildUserMessageWithImagesLimit 按张数限额展开图片——keep < 0 = 全展开；0 = 全摘（纯占位）；
// 0 < keep < len(refs) = 只展开末尾 keep 张（保留最新），其余在 text 内计数说明。
//
// 判据：省略必须可见（text 块写清省略张数与原因），不让模型以为「本来就没图」。
func buildUserMessageWithImagesLimit(msg session.Message, keep int) string {
	refs, parseError := parseImageRefs(msg)
	if keep < 0 || keep > len(refs) {
		keep = len(refs)
	}
	omitted := len(refs) - keep
	var text strings.Builder
	text.WriteString(msg.Content)
	var failures []string
	var urls []string
	if parseError != "" {
		failures = append(failures, parseError)
	}
	for _, ref := range refs[len(refs)-keep:] {
		if ref == "" {
			continue
		}
		url, errText := ResolveImagePayload(ref)
		if errText != "" {
			failures = append(failures, errText)
			continue
		}
		urls = append(urls, url)
	}
	if omitted > 0 {
		text.WriteString("\n\n" + imageDropNoticePrefix + itoa(omitted) + imageDropNoticeSuffix)
	}
	if len(failures) > 0 {
		text.WriteString("\n\n[图片引用失效]")
		for _, failure := range failures {
			text.WriteString("\n")
			text.WriteString(failure)
		}
	}
	body := text.String()
	if body == "" {
		body = "(图片)"
	}
	var b strings.Builder
	b.WriteString(`{"role":"user","content":[{"type":"text","text":`)
	b.WriteString(JSONString(body))
	b.WriteString(`}`)
	for _, url := range urls {
		b.WriteString(`,{"type":"image_url","image_url":{"url":`)
		b.WriteString(JSONString(url))
		b.WriteString(`}}`)
	}
	b.WriteString("]}")
	return b.String()
}

// buildWireTools tools 数组——OpenAI function 定义；parameters 规整透传（non-BMP 转义；空 = 空对象 schema）。
func buildWireTools(tools []session.ToolSpec) string {
	if len(tools) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[")
	for i, spec := range tools {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"type":"function","function":{"name":`)
		b.WriteString(JSONString(spec.Name))
		b.WriteString(`,"description":`)
		b.WriteString(JSONString(spec.Description))
		b.WriteString(`,"parameters":`)
		if spec.ParametersJSON != "" {
			b.WriteString(session.JSONFragment(spec.ParametersJSON))
		} else {
			b.WriteString(`{"type":"object","properties":{}}`)
		}
		b.WriteString("}}")
	}
	b.WriteString("]")
	return b.String()
}

// ── 请求体预算（图片省略保护）────────────────────────────────────────────

// maxRequestBodyBytes 主请求体安全上限——2026-11-14 实测本地网关 WorkBuddyShell
// （127.0.0.1:18100）：0.5 / 1 / 2 / 3 MB 正常响应，4 MB / 6 MB 直接断连（WinError 10053）。
const maxRequestBodyBytes = 3 * 1024 * 1024

// imageDropNoticePrefix / Suffix 图片省略占位——进 text 块（模型可见），中间夹省略张数。
const (
	imageDropNoticePrefix = "[图片已省略 "
	imageDropNoticeSuffix = " 张：请求体超过 3 MB 安全上限或视觉通道降级——图片内容未发送，请以文字线索继续；需要看图请减少同轮图片数或缩小图片]"
)

// imageDropNotice 全摘占位（1 张版本文案，兼容既有测试与日志）。
const imageDropNotice = "[图片已省略：请求体超过 3 MB 安全上限或视觉通道降级——图片内容未发送，请以文字线索继续；需要看图请减少同轮图片数或缩小图片]"

// BuildChatRequestBodyBudget 带预算的请求体构造——**按张**摘图（从最早的消息、最早的一张开始），
// 同一消息内保留最新；图片通道降级期全摘。返回（请求体, 省略的图片张数）。
//
// 判据：请求体 ≤ maxRequestBodyBytes；实测仍超（估算失真）→ 全摘兜底重构造一次。
// 为什么按张：一条注入消息可带多张图，整条摘会把「正在看的那张」也丢掉
// （2026-11-14 真链路实测：3 张同批注入 → 整条摘 → 一张不剩）。
func BuildChatRequestBodyBudget(messages []session.Message, tools []session.ToolSpec, params Params, userID string) (string, int) {
	refsByIndex := make(map[int][]string, len(messages))
	totalImages := 0
	estimated := len(buildChatRequestBody(messages, tools, params, userID, nilKeeps(messages)))
	for i, msg := range messages {
		refs, _ := parseImageRefs(msg)
		if len(refs) == 0 {
			continue
		}
		refsByIndex[i] = refs
		totalImages += len(refs)
		for _, ref := range refs {
			estimated += estimateImageRefBytes(ref)
		}
	}
	if totalImages == 0 {
		return buildChatRequestBody(messages, tools, params, userID, nil), 0
	}
	// keeps：消息下标 → 保留末尾张数（缺省 = 全留）
	keeps := make(map[int]int, len(refsByIndex))
	for i, refs := range refsByIndex {
		keeps[i] = len(refs)
	}
	if _, down := ImageChannelState.Down(); down {
		for i := range keeps {
			keeps[i] = 0
		}
	} else if estimated > maxRequestBodyBytes {
		// 逐张摘：从最早消息的第一张开始，同一消息保留最新
		for i := range messages {
			refs := refsByIndex[i]
			for k := 0; k < len(refs); k++ {
				keeps[i] = len(refs) - (k + 1)
				estimated -= estimateImageRefBytes(refs[k])
				if estimated <= maxRequestBodyBytes {
					break
				}
			}
			if estimated <= maxRequestBodyBytes {
				break
			}
		}
	}
	body := buildChatRequestBody(messages, tools, params, userID, keeps)
	if len(body) > maxRequestBodyBytes && keptImageCount(keeps) < totalImages {
		// 估算失真兜底：全摘重构造（宁可不发图，也不让连接被掐）
		for i := range keeps {
			keeps[i] = 0
		}
		body = buildChatRequestBody(messages, tools, params, userID, keeps)
	}
	return body, totalImages - keptImageCount(keeps)
}

// keptImageCount keeps 表里保留的总张数。
func keptImageCount(keeps map[int]int) int {
	count := 0
	for _, keep := range keeps {
		if keep > 0 {
			count += keep
		}
	}
	return count
}

// nilKeeps 全摘基线（构造「无图」体积用）——所有带图消息记 0。
func nilKeeps(messages []session.Message) map[int]int {
	keeps := make(map[int]int)
	for i, msg := range messages {
		if refs, _ := parseImageRefs(msg); len(refs) > 0 {
			keeps[i] = 0
		}
	}
	return keeps
}

// requestHasImages 消息序列里是否有图片引用——判定「本次请求是否真的带图」。
func requestHasImages(messages []session.Message) bool {
	for _, msg := range messages {
		if msg.ImagesJSON != "" {
			return true
		}
	}
	return false
}

// parseImageRefs 解析图片引用数组——返回（引用列表, 解析错误文本）。
func parseImageRefs(msg session.Message) ([]string, string) {
	if msg.ImagesJSON == "" {
		return nil, ""
	}
	var refs []string
	if err := json.Unmarshal([]byte(msg.ImagesJSON), &refs); err != nil {
		return nil, "ERR|IMAGE_REF_PARSE|附件引用解析失败: " + err.Error()
	}
	return refs, ""
}

// estimateImageRefBytes 单张图片展开体积估算——本地文件按 stat × 4/3（base64 膨胀）+ 头；
// 已内联的 data URL 按长度；本机回环 / 私网 URL 按 1 MB（会内联抓取，体积不可预知）；公网 URL 按长度。
func estimateImageRefBytes(ref string) int {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return 0
	}
	lower := strings.ToLower(ref)
	switch {
	case strings.HasPrefix(lower, "data:"):
		return len(ref)
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		if isLocalOnlyURL(ref) {
			return 1 * 1024 * 1024
		}
		return len(ref) + 64
	default:
		info, err := os.Stat(ref)
		if err != nil || info.IsDir() {
			return 256
		}
		return int(info.Size())*4/3 + 128
	}
}

// isLocalOnlyURL 判定图片 URL 是否本机可达（会内联抓取）；解析失败按公网处理。
func isLocalOnlyURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return false
	}
	return isLocalOnlyHost(parsed)
}

// JSONString 编码 JSON 字符串字面量——委托会话层单点实现（与 C# JsonUtil 逐字节同构）。
func JSONString(s string) string { return session.JSONString(s) }
