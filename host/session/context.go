package session

import (
	"encoding/json"
	"strings"
	"sync"
)

// Context 是会话前文容器——消息历史 + 系统提示词缓存 + 最后变动时刻。
//
// 语义对齐 C# Mau.Runtime.ChatContext：system 提示词独立缓存永久保留；user/assistant/tool 按序追加；
// 结构修复走 ReplaceMessages（system 唯一 / tool 配对补全 / 孤立 tool 丢弃）。
// 并发：C# 侧相位与上下文仅主线程推进；Go 侧加锁保护（HTTP 线程读只读投影）。
type Context struct {
	mu sync.RWMutex
	// history 消息历史（按时间顺序；首条为 system）
	history []Message
	// systemPrompt 系统提示词缓存（Clear 时恢复）
	systemPrompt string
	// lastChangeAt 最后一次前文变动时刻（Unix 毫秒；0=从未变动）
	lastChangeAt int64
	// onAppend 消息追加回调（完整前文留档采集挂接点；nil = 未挂接）——解锁后调用，不在锁内执行
	onAppend func()
}

// NewContext 建立空会话上下文。
func NewContext() *Context { return &Context{history: make([]Message, 0, 64)} }

// nowStamp 当前时刻（Unix 毫秒）——测试可替换的时钟不影响此处（消息 CreatedAt 由调用方给）。
func nowStamp() int64 { return nowMillis() }

// SetSystemPrompt 设置系统提示词——替换已有 system 消息（若存在）。
func (c *Context) SetSystemPrompt(prompt string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.systemPrompt = prompt
	kept := make([]Message, 0, len(c.history)+1)
	for _, msg := range c.history {
		if msg.Role != RoleSystem {
			kept = append(kept, msg)
		}
	}
	if prompt != "" {
		kept = append([]Message{newMessage(RoleSystem, prompt)}, kept...)
	}
	c.history = kept
	c.markChangedLocked()
}

// AddUserMessage 追加用户消息——返回追加的消息（空文本不入上下文）。
func (c *Context) AddUserMessage(text string) *Message {
	return c.AddUserMessageWithImages(text, "")
}

// AddUserMessageWithImages 追加用户消息（带图片附件引用）——文本与附件皆空 = 不入上下文。
func (c *Context) AddUserMessageWithImages(text, imagesJSON string) *Message {
	if text == "" && imagesJSON == "" {
		return nil
	}
	c.mu.Lock()
	msg := newMessage(RoleUser, text)
	msg.ImagesJSON = imagesJSON
	c.history = append(c.history, msg)
	c.markChangedLocked()
	c.mu.Unlock()
	c.fireAppend()
	return &msg
}

// AddAssistantMessage 追加助手文本回复。
func (c *Context) AddAssistantMessage(text string) *Message {
	c.mu.Lock()
	c.history = append(c.history, newMessage(RoleAssistant, text))
	c.markChangedLocked()
	msg := &c.history[len(c.history)-1]
	c.mu.Unlock()
	c.fireAppend()
	return msg
}

// AddAssistantToolCalls 追加助手工具调用声明——tool_calls JSON 原样 + 思考内容（回传铁律）。
//
// 空 tool_calls = 不入上下文。
func (c *Context) AddAssistantToolCalls(toolCallsJSON, reasoning string) *Message {
	if toolCallsJSON == "" {
		return nil
	}
	c.mu.Lock()
	msg := newMessage(RoleAssistant, "")
	msg.ToolCallsJSON = toolCallsJSON
	msg.ReasoningContent = reasoning
	c.history = append(c.history, msg)
	c.markChangedLocked()
	c.mu.Unlock()
	c.fireAppend()
	return &msg
}

// AddToolResult 追加工具结果——与调用 ID 配对；同一 ToolCallID 只入册一次（幂等，重复返回 nil）。
func (c *Context) AddToolResult(toolCallID, toolName, result string) *Message {
	c.mu.Lock()
	for i := len(c.history) - 1; i >= 0; i-- {
		existing := c.history[i]
		if existing.Role == RoleTool && existing.ToolCallID == toolCallID {
			c.mu.Unlock()
			return nil
		}
	}
	msg := newMessage(RoleTool, result)
	msg.ToolCallID = toolCallID
	msg.ToolName = toolName
	c.history = append(c.history, msg)
	c.markChangedLocked()
	c.mu.Unlock()
	c.fireAppend()
	return &msg
}

// Messages 获取消息数组副本——直接传给 LLM API。
func (c *Context) Messages() []Message {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Message, len(c.history))
	copy(out, c.history)
	return out
}

// Count 消息条数。
func (c *Context) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.history)
}

// LastChangeAt 最后一次前文变动时刻（Unix 毫秒；0=从未变动）。
func (c *Context) LastChangeAt() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastChangeAt
}

// Clear 清除历史——保留系统提示词。
func (c *Context) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.history = make([]Message, 0, 64)
	if c.systemPrompt != "" {
		c.history = append(c.history, newMessage(RoleSystem, c.systemPrompt))
	}
	c.markChangedLocked()
}

// ReplaceMessages 以外部历史替换当前上下文（重启恢复 / 回卷 / 重建）。
//
// 结构修复：system 唯一（取第一条）、tool 无配对 ID 丢弃、声明无结果按声明序在结果块末尾补占位。
// 恢复导入不刷新 LastChangeAt（「距今」问的是猫多久没干活，恢复不是干活）。
func (c *Context) ReplaceMessages(messages []Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.history = make([]Message, 0, len(messages))
	c.systemPrompt = ""
	if messages == nil {
		return
	}
	// [段1] 全量声明 + 全量结果收集——两遍扫描（声明集 = 配对锚；结果集 = 消费面）
	declaredNames := make(map[string]string, len(messages))
	resultIDs := make(map[string]bool, len(messages))
	normalized := make([]Message, len(messages))
	for i, msg := range messages {
		normalized[i] = msg
		if msg.Role == RoleAssistant && msg.ToolCallsJSON != "" {
			for _, pair := range parseToolCallDecls(msg.ToolCallsJSON) {
				declaredNames[pair.ID] = pair.Name
			}
		}
		if msg.Role == RoleTool && msg.ToolCallID != "" {
			resultIDs[msg.ToolCallID] = true
		}
	}
	// [段2] 顺序构建——声明无结果 → 占位补全；结果无声明 → 丢弃
	placed := make(map[string]bool, len(messages))
	pendingRepair := make([]toolDecl, 0, 4)
	for _, msg := range normalized {
		if msg.Role != RoleTool && len(pendingRepair) > 0 {
			c.appendMissingToolResultsLocked(pendingRepair)
			pendingRepair = pendingRepair[:0]
		}
		switch msg.Role {
		case RoleSystem:
			if c.systemPrompt == "" {
				c.systemPrompt = msg.Content
				c.history = append(c.history, msg)
			}
		case RoleAssistant:
			// tool_calls 解析失败 → 降级纯文本（保留 content/reasoning）
			if msg.ToolCallsJSON != "" && len(parseToolCallDecls(msg.ToolCallsJSON)) == 0 {
				msg.ToolCallsJSON = ""
			}
			c.history = append(c.history, msg)
			if msg.ToolCallsJSON != "" {
				for _, pair := range parseToolCallDecls(msg.ToolCallsJSON) {
					if !resultIDs[pair.ID] {
						pendingRepair = append(pendingRepair, pair)
					}
				}
			}
		case RoleTool:
			if msg.ToolCallID == "" {
				continue
			}
			if _, declared := declaredNames[msg.ToolCallID]; !declared {
				continue
			}
			if placed[msg.ToolCallID] {
				continue
			}
			placed[msg.ToolCallID] = true
			c.history = append(c.history, msg)
		default:
			c.history = append(c.history, msg)
		}
	}
	if len(pendingRepair) > 0 {
		c.appendMissingToolResultsLocked(pendingRepair)
	}
}

// appendMissingToolResultsLocked 补占位工具结果——声明位无真实结果时补配对消息。
//
// 追加序 = 声明序，落位在真实结果之后：结构即天然数据流（声明序 = 执行序 = 外观序）。
func (c *Context) appendMissingToolResultsLocked(pending []toolDecl) {
	for _, item := range pending {
		msg := newMessage(RoleTool, "[系统自动修复] 该工具调用未返回结果（宿主中断）——结果不可知")
		msg.ToolCallID = item.ID
		msg.ToolName = item.Name
		c.history = append(c.history, msg)
	}
}

// toolDecl 是工具调用声明（ID → 工具名）。
type toolDecl struct {
	// ID 调用 ID
	ID string
	// Name 工具名
	Name string
}

// parseToolCallDecls 解析 assistant tool_calls JSON——提取调用 ID → 工具名（保持数组声明序）。
//
// 防御式：解析失败 / 非数组返回空表。
func parseToolCallDecls(toolCallsJSON string) []toolDecl {
	result := make([]toolDecl, 0, 2)
	if toolCallsJSON == "" {
		return result
	}
	var calls []struct {
		ID       string `json:"id"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal([]byte(toolCallsJSON), &calls); err != nil {
		logAt(LogModuleViewStore, 2, "主机声明解析失败，返回空表: "+err.Error(), LogCategorySys)
		return result
	}
	seen := make(map[string]bool, len(calls))
	for _, call := range calls {
		id := strings.TrimSpace(call.ID)
		if id == "" {
			continue
		}
		// 同 ID 重复声明——保留第一条（声明序由数组序保证）
		if seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, toolDecl{ID: id, Name: call.Function.Name})
	}
	return result
}

// SetOnAppend 设置消息追加回调（nil = 清除）——完整前文留档的采集挂接点。
//
// 回调在解锁后调用：回调内会读取前文（Messages），在锁内执行会与 RWMutex 重入相撞。
func (c *Context) SetOnAppend(fn func()) {
	c.mu.Lock()
	c.onAppend = fn
	c.mu.Unlock()
}

// fireAppend 触发消息追加回调（未挂接时零动作）。
func (c *Context) fireAppend() {
	c.mu.RLock()
	hook := c.onAppend
	c.mu.RUnlock()
	if hook != nil {
		hook()
	}
}

// markChangedLocked 标记前文变动（调用方持锁）。
func (c *Context) markChangedLocked() {
	c.lastChangeAt = nowStamp()
}

// newMessage 建立已初始化消息——角色 + 正文 + 创建时刻。
func newMessage(role Role, content string) Message {
	return Message{Role: role, Content: content, CreatedAt: nowStamp()}
}
