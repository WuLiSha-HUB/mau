package main

import (
	"context"

	"cathome5/host/llm"
	"cathome5/host/session"
	"cathome5/mau/bricks"
)

// llmStreamAdapter 把宿主 LLM 客户端适配为积木侧流式窄接口（llm.stream 的执行面）。
//
// 走宿主同一条链路（端点池 / 故障转移 / 自报头 / 缓存隔离键）——与 C# 积木
// TryResolve<ILlmRuntime> 取宿主运行时单例同构；不引入第二条 LLM 通道。
type llmStreamAdapter struct {
	// client 宿主 LLM 客户端（nil = 未接线，积木侧收 Error 事件走失败侧）
	client *llm.Client
}

// ChatStream 单轮流式对话——四类事件转换，其余（工具调用 / usage / finish / 重试 / 端点切换）丢弃。
//
// 事件类别数值在 host/llm 与 bricks 两侧独立定义，此处显式 switch 映射（不依赖数值同序）。
func (a *llmStreamAdapter) ChatStream(system, content, sessionID string, onEvent func(kind bricks.LLMStreamKind, text string)) {
	if a == nil || a.client == nil {
		onEvent(bricks.LLMStreamError, "ERR|LLM_NOT_WIRED|LLM 客户端未接线")
		return
	}
	messages := []session.Message{
		{Role: session.RoleSystem, Content: system},
		{Role: session.RoleUser, Content: content},
	}
	a.client.Stream(context.Background(), messages, nil, sessionID, func(ev llm.Event) {
		switch ev.Kind {
		case llm.EventText:
			onEvent(bricks.LLMStreamText, ev.Text)
		case llm.EventReasoning:
			onEvent(bricks.LLMStreamReasoning, ev.Text)
		case llm.EventDone:
			onEvent(bricks.LLMStreamDone, "")
		case llm.EventError:
			onEvent(bricks.LLMStreamError, ev.Text)
		}
	})
}

// 编译期断言——适配器实现积木侧窄接口。
var _ bricks.LLMStreamRuntime = (*llmStreamAdapter)(nil)
