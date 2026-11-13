package bricks

import (
	"fmt"
	"strings"

	"cathome5/mau/runtime"
)

// LLMStreamKind 是流式事件类别——C# LlmStreamKind 的子集，只保留流式语料消费的四类。
//
// 其余事件（工具调用 / usage / finish / 重试 / 端点切换）对流式语料无意义：
// 适配层直接丢弃，积木侧不建立对应常量（避免「声明了但没人处理」的死枚举）。
type LLMStreamKind int

// 四类流式事件。
const (
	// LLMStreamText 回复增量（choices[].delta.content）
	LLMStreamText LLMStreamKind = iota
	// LLMStreamReasoning 思考增量（choices[].delta.reasoning_content）
	LLMStreamReasoning
	// LLMStreamDone 正常结束（实现方须保证与 LLMStreamError 二选一收口）
	LLMStreamDone
	// LLMStreamError 失败终止（text 携带 ERR|码|详情）
	LLMStreamError
)

// LLMStreamRuntime 是 llm.stream 的流式执行面窄接口——由组合根注入（宿主 LLM 客户端薄适配）。
//
// 契约：单轮流式对话，onEvent 按到达序回调；实现方必须保证以 LLMStreamDone 或 LLMStreamError
// 收口（含未接线 / 传输失败路径）——积木侧对「无收口事件」另有 STREAM_END 兜底，但那是防御不是契约。
// 回调可能在任意 goroutine 上执行；积木侧消费方不重入 ChatStream。
type LLMStreamRuntime interface {
	// ChatStream 发起单轮流式对话——system / content 两条消息，sessionID 为上游缓存隔离键。
	ChatStream(system, content, sessionID string, onEvent func(kind LLMStreamKind, text string))
}

// LLMStreamStart 复现 llm.stream——流式启动器（BRIK-LLM-002）。
//
// 主线程捕获 flowID 后立即返回（开始信号 = 本积木调用成功）；后台 goroutine 消费流式增量，
// 写本实例段盒 llm_chunk:<f>（最新增量）/ llm_chunk_count:<f>（分片序号）/
// llm_reply:<f>（完整回复或 ERR|）/ llm_done:<f>（"1"=已结算）。
//
// 启动清理（会话边界信号清理，flowID > 0 时）：
//   - llm_chunk_count:<f> 归零 · llm_done:<f> 清空 · llm_chunk_seen:<f> Remove
//   - 失效语料探测盒 hasDone / hasChunk 走 Remove 而非 Set false——盒不存在 = 未采样 = 条件天然为假
//
// 盒值恒空串（对齐 C# out reply 占位——完成文本经段盒 llm_reply 取）；ok = 启动成功。
func LLMStreamStart(rt LLMStreamRuntime, db *runtime.DataBox, flowID int64, system, content string, logf func(level int, message string)) (string, bool) {
	if rt == nil || db == nil {
		return "", false
	}
	seg := itoa64(flowID)
	if flowID > 0 {
		db.Set("global", "llm_chunk_count:"+seg, int64(0))
		db.Set("global", "llm_done:"+seg, "")
		db.Remove("global", "llm_chunk_seen:"+seg)
		// 上一会话残留的传感器采样信号——同帧误触发防护（C# 同注释口径）
		db.Remove(seg, "hasDone")
		db.Remove(seg, "hasChunk")
	}
	go consumeLLMStream(rt, db, seg, system, content, logf)
	return "", true
}

// consumeLLMStream 后台消费流——增量写本实例段盒；完成 / 失败置 llm_done:<f> 并落日志结算行。
//
// 与 C# ConsumeStream 五段一一对应：增量（段1）→ 完成（段2）→ 失败（段3）→ 流意外结束（段4）→ 异常兜底（段5）。
func consumeLLMStream(rt LLMStreamRuntime, db *runtime.DataBox, seg, system, content string, logf func(level int, message string)) {
	var full strings.Builder
	count := int64(0)
	textCount := int64(0)
	reasonCount := int64(0)
	closed := false
	defer func() {
		if rec := recover(); rec != nil {
			// 段5：异常兜底——错误可见性（类型名取 Go 方言 %T，与 TEMP_EXEC 同口径）
			if closed {
				return
			}
			typeName := fmt.Sprintf("%T", rec)
			message := fmt.Sprint(rec)
			db.Set("global", "llm_reply:"+seg, "ERR|"+typeName+"|"+message)
			db.Set("global", "llm_done:"+seg, "1")
			if logf != nil {
				logf(3, "模型流式回复异常（"+typeName+"）："+trimLLMText(message, 200))
			}
			closed = true
		}
	}()
	rt.ChatStream(system, content, seg, func(kind LLMStreamKind, text string) {
		if closed {
			return
		}
		switch kind {
		case LLMStreamText, LLMStreamReasoning:
			// 段1：增量——累积 + 段盒覆盖写（实时数据面在盒，分片不进日志）
			full.WriteString(text)
			count++
			db.Set("global", "llm_chunk:"+seg, text)
			db.Set("global", "llm_chunk_count:"+seg, count)
			if kind == LLMStreamText {
				textCount++
			} else {
				reasonCount++
			}
		case LLMStreamDone:
			// 段2：完成——完整回复落段盒 + 完成标志 + 结算日志（字符数按 UTF-16 码元，对齐 C# string.Length）
			reply := full.String()
			db.Set("global", "llm_reply:"+seg, reply)
			db.Set("global", "llm_done:"+seg, "1")
			if logf != nil {
				logf(0, "模型流式回复完成："+itoa64(count)+" 分片（文本 "+itoa64(textCount)+
					" / 思考 "+itoa64(reasonCount)+"），共 "+itoa64(int64(utf16Units(reply)))+" 字符")
			}
			closed = true
		case LLMStreamError:
			// 段3：失败——错误文本落段盒 + 完成标志（失败可见性：ERR| 前缀由实现方保证）
			db.Set("global", "llm_reply:"+seg, text)
			db.Set("global", "llm_done:"+seg, "1")
			if logf != nil {
				logf(3, "模型流式回复失败："+trimLLMText(text, 200))
			}
			closed = true
		}
	})
	if !closed {
		// 段4：流意外结束（实现方未按契约收口——兜底防御）
		db.Set("global", "llm_reply:"+seg, "ERR|STREAM_END|流意外结束")
		db.Set("global", "llm_done:"+seg, "1")
	}
}

// LLMChunkReady 复现 llm.chunk_ready——本实例分块探测（BRIK-LLM-003）。
//
// 读 llm_chunk_count:<f> 对比已消费序号 llm_chunk_seen:<f>；有新增 → 更新 seen + true。
// count < seen 视为新流式会话（llm.stream 已重置计数）→ seen 归零对齐后再判。
// flowID ≤ 0 / 计数盒缺失 → false（判断语义：值比较归宿主，盒值 "true"/"false" + ok 恒真）。
func LLMChunkReady(db *runtime.DataBox, flowID int64) (string, bool) {
	if db == nil {
		return "false", false
	}
	if flowID <= 0 {
		return "false", true
	}
	seg := itoa64(flowID)
	count, ok := dataBoxInt64(db, "global", "llm_chunk_count:"+seg)
	if !ok {
		return "false", true
	}
	seen := int64(0)
	if seenBox, hit := dataBoxInt64(db, "global", "llm_chunk_seen:"+seg); hit {
		seen = seenBox
	}
	if count < seen {
		seen = 0
		db.Set("global", "llm_chunk_seen:"+seg, int64(0))
	}
	if count > seen {
		db.Set("global", "llm_chunk_seen:"+seg, count)
		return "true", true
	}
	return "false", true
}

// LLMDoneReady 复现 llm.done_ready——本实例完成探测（BRIK-LLM-004）。
//
// llm_done:<f> == "1" → true（流式已完成或失败已结算——llm_reply 可读）。
func LLMDoneReady(db *runtime.DataBox, flowID int64) (string, bool) {
	if db == nil {
		return "false", false
	}
	if flowID <= 0 {
		return "false", true
	}
	if db.GetString("global", "llm_done:"+itoa64(flowID)) == "1" {
		return "true", true
	}
	return "false", true
}

// LLMReplyRead 复现 llm.reply_read——本实例完整回复取数（BRIK-LLM-005）。
//
// 键缺失 / 未完成 / 空串 / 上下文外 → ("", false)（空串走失败侧）；非空原文 + true。
func LLMReplyRead(db *runtime.DataBox, flowID int64) (string, bool) {
	return llmSegRead(db, "llm_reply:", flowID)
}

// LLMChunkRead 复现 llm.chunk_read——本实例最新分块取数（BRIK-LLM-006）。
//
// 键缺失 / 未启动 / 空串 / 上下文外 → ("", false)（空串走失败侧）；非空原文 + true。
func LLMChunkRead(db *runtime.DataBox, flowID int64) (string, bool) {
	return llmSegRead(db, "llm_chunk:", flowID)
}

// llmSegRead 读本实例段盒字符串——缺失 / 类型不符 / 空串一律未命中（C# TryGet<string> + Length > 0 同口径）。
func llmSegRead(db *runtime.DataBox, prefix string, flowID int64) (string, bool) {
	if db == nil || flowID <= 0 {
		return "", false
	}
	value, ok := db.Get("global", prefix+itoa64(flowID))
	if !ok {
		return "", false
	}
	text, isStr := value.(string)
	if !isStr || text == "" {
		return "", false
	}
	return text, true
}

// dataBoxInt64 读数据区 int64 值——缺失 / 类型不符返回 false（C# TryGet<long> 同口径）。
func dataBoxInt64(db *runtime.DataBox, scope, key string) (int64, bool) {
	value, ok := db.Get(scope, key)
	if !ok {
		return 0, false
	}
	number, isInt := value.(int64)
	if !isInt {
		return 0, false
	}
	return number, true
}
