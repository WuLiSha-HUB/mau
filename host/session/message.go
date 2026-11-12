package session

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Role 是消息角色——OpenAI 兼容四 role（system/user/assistant/tool）。
//
// 数值与 C# LlmRole 枚举一致（0/1/2/3）——落盘 jsonl 的 "Role" 字段是整数（C# 默认枚举序列化）。
type Role int

// 四角色常量。
const (
	// RoleSystem 系统提示词
	RoleSystem Role = 0
	// RoleUser 用户消息
	RoleUser Role = 1
	// RoleAssistant 助手消息（可携带 tool_calls / reasoning_content）
	RoleAssistant Role = 2
	// RoleTool 工具结果（必须携带 ToolCallID）
	RoleTool Role = 3
)

// Message 是厂商无关单条 LLM 消息——OpenAI 兼容消息序列的元素。
//
// 字段与 C# LlmMessage 一一对齐（含字段序——落盘行与 C# 逐字节同构是数据面互换的前提）。
type Message struct {
	// Role 消息角色
	Role Role
	// Content 消息正文；无正文时空串
	Content string
	// ToolCallID 工具结果对应的调用 ID（RoleTool 时有效）
	ToolCallID string
	// ToolName 工具名称（RoleTool 时有效）
	ToolName string
	// ToolCallsJSON assistant 工具调用 JSON 数组（OpenAI wire tool_calls 原样）
	ToolCallsJSON string
	// ReasoningContent assistant 思考内容（工具调用轮次必须回传）
	ReasoningContent string
	// ImagesJSON user 消息附件引用——JSON 数组字符串，元素为图片绝对路径；空 = 无附件
	ImagesJSON string
	// CreatedAt 创建时间戳（Unix 毫秒；视图排序键——真实时序权威）
	CreatedAt int64
}

// BuildMessageLine 序列化消息行——LlmMessage 全字段 + t=m 标记（字段序与 C# 一致）。
func BuildMessageLine(msg Message) string {
	var b strings.Builder
	b.WriteString(`{"Role":`)
	b.WriteString(strconv.Itoa(int(msg.Role)))
	b.WriteString(`,"Content":`)
	b.WriteString(jsonStr(msg.Content))
	b.WriteString(`,"ToolCallId":`)
	b.WriteString(jsonStr(msg.ToolCallID))
	b.WriteString(`,"ToolName":`)
	b.WriteString(jsonStr(msg.ToolName))
	b.WriteString(`,"ToolCallsJson":`)
	b.WriteString(jsonStr(msg.ToolCallsJSON))
	b.WriteString(`,"ReasoningContent":`)
	b.WriteString(jsonStr(msg.ReasoningContent))
	b.WriteString(`,"ImagesJson":`)
	b.WriteString(jsonStr(msg.ImagesJSON))
	b.WriteString(`,"CreatedAt":`)
	b.WriteString(itoa64(msg.CreatedAt))
	b.WriteString(`,"t":"m"}`)
	return b.String()
}

// ParseMessageLine 解析消息行——未知字段（t）忽略；字段缺失补齐空串。
//
// ok=false 表示该行无法构成消息（非对象 / 无 Role 键 / 解析失败）——调用方按坏行处置。
func ParseMessageLine(line string) (Message, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return Message{}, false
	}
	roleRaw, exists := raw["Role"]
	if !exists {
		return Message{}, false
	}
	var role int
	if err := json.Unmarshal(roleRaw, &role); err != nil {
		return Message{}, false
	}
	return Message{
		Role:             Role(role),
		Content:          rawString(raw, "Content"),
		ToolCallID:       rawString(raw, "ToolCallId"),
		ToolName:         rawString(raw, "ToolName"),
		ToolCallsJSON:    rawString(raw, "ToolCallsJson"),
		ReasoningContent: rawString(raw, "ReasoningContent"),
		ImagesJSON:       rawString(raw, "ImagesJson"),
		CreatedAt:        rawInt64(raw, "CreatedAt"),
	}, true
}

// rawString 取对象字符串字段（缺失 / 非字符串 = 空串）。
func rawString(obj map[string]json.RawMessage, key string) string {
	value, exists := obj[key]
	if !exists {
		return ""
	}
	var s string
	if err := json.Unmarshal(value, &s); err != nil {
		return ""
	}
	return s
}

// rawInt64 取对象整数字段（缺失 / 非数字 = 0）。
func rawInt64(obj map[string]json.RawMessage, key string) int64 {
	value, exists := obj[key]
	if !exists {
		return 0
	}
	var n int64
	if err := json.Unmarshal(value, &n); err != nil {
		return 0
	}
	return n
}

// lineTag 读行型标记 t（非对象 / 无该键 = 空串）。
func lineTag(line string) string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return ""
	}
	return rawString(raw, "t")
}

// Clone 复制消息（值语义——工具批与回收按副本改写，避免切片别名串写）。
func (m Message) Clone() Message { return m }

// IsHumanUserMessage 域内人工输入判定——回卷时唯一保留的域内消息类型。
//
// 判据：role=user 且正文不带系统注入行头——系统注入与图片注入一律经 BuildSysInjectText
// 单点套前缀，故该前缀即「非人工」的前文判据。
func IsHumanUserMessage(msg Message) bool {
	if msg.Role != RoleUser {
		return false
	}
	return !strings.HasPrefix(msg.Content, SysInjectPrefix)
}
