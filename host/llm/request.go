// Package llm 复现宿主 LLM 面：请求体序列化、SSE 帧翻译与端点解析。
//
// 边界：本包只做「wire 面」——消息数组与 tools 数组按 C# DeepSeekLlmRuntime 的
// 键序逐字节同构（判据 1 请求等价）；会话状态、工具批与关轮归 host/session。
package llm

import (
	"encoding/json"
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
	var b strings.Builder
	b.WriteString(`{"model":`)
	b.WriteString(JSONString(params.Model))
	b.WriteString(`,"messages":[`)
	for i, msg := range messages {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(buildWireMessage(msg))
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
	switch msg.Role {
	case session.RoleSystem:
		return `{"role":"system","content":` + JSONString(msg.Content) + `}`
	case session.RoleUser:
		if msg.ImagesJSON != "" {
			return buildUserMessageWithImages(msg)
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
	var text strings.Builder
	text.WriteString(msg.Content)
	var failures []string
	var urls []string
	var refs []string
	if err := json.Unmarshal([]byte(msg.ImagesJSON), &refs); err != nil {
		failures = append(failures, "ERR|IMAGE_REF_PARSE|附件引用解析失败: "+err.Error())
	}
	for _, ref := range refs {
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

// JSONString 编码 JSON 字符串字面量——委托会话层单点实现（与 C# JsonUtil 逐字节同构）。
func JSONString(s string) string { return session.JSONString(s) }
