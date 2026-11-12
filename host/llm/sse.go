package llm

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// EventKind 是流式事件类别——与 C# LlmStreamKind 一一对应。
type EventKind int

// 事件类别常量。
const (
	// EventReasoning 思考增量
	EventReasoning EventKind = iota
	// EventText 回复增量
	EventText
	// EventToolCallsStart 工具决策流首帧（每个请求一次）
	EventToolCallsStart
	// EventToolCalls 工具调用完整列表（流末一次）
	EventToolCalls
	// EventUsage token 统计（每请求一次）
	EventUsage
	// EventFinish finish_reason（流末一次）
	EventFinish
	// EventDone 正常结束
	EventDone
	// EventRetrying 传输层重试开始
	EventRetrying
	// EventRetryResume 退避结束、重发开始
	EventRetryResume
	// EventFailover 端点切换
	EventFailover
	// EventError 错误（ERR| 前缀）
	EventError
)

// Event 是一条流式事件。
type Event struct {
	// Kind 事件类别
	Kind EventKind
	// Text 载荷（错误原文 / 增量文本 / usage JSON / finish_reason）
	Text string
}

// TranslateSSE 逐帧翻译 SSE 流——增量事件按到达序回调（流式语义，不缓存整段）。
//
// 语义与 C# TranslateSse 逐条对齐：
//   - [DONE] 为唯一可信终止（忽略余帧）
//   - usage 与 finish_reason 每帧尝试、流末各产一次（双发供应商以最后一份为准）
//   - 帧内产出序 Reasoning → Text；首个 tool_calls 增量帧产一次 ToolCallsStart
//   - 未以 [DONE] 结束 → STREAM_CLOSED 错误事件，且不再产 Finish/Usage/ToolCalls
//   - 正常结束：Finish（若有）→ Usage（若有）→ ToolCalls（若有）→ Done
func TranslateSSE(r io.Reader, emit func(Event)) {
	drain := newSSEDrainer(r)
	done := false
	lastFinishReason := ""
	lastUsage := ""
	agg := newToolCallAggregator()
	for {
		data, ok := drain.next()
		if !ok {
			break
		}
		if data == "[DONE]" {
			done = true
			break
		}
		usageJSON := ""
		if parseUsage(data, &usageJSON) && usageJSON != "" {
			lastUsage = usageJSON
		}
		finishReason, hasFinish := parseFinishReason(data)
		if hasFinish && finishReason != "" {
			lastFinishReason = finishReason
		}
		text, reasoning, parsed := parseDelta(data)
		if parsed {
			if reasoning != "" {
				emit(Event{Kind: EventReasoning, Text: reasoning})
			}
			if text != "" {
				emit(Event{Kind: EventText, Text: text})
			}
			before := agg.count()
			agg.accumulate(data)
			if agg.count() > before {
				emit(Event{Kind: EventToolCallsStart, Text: ""})
			}
		}
	}
	if !done {
		emit(Event{Kind: EventError, Text: "ERR|STREAM_CLOSED|SSE 流未以 [DONE] 结束"})
		return
	}
	if lastFinishReason != "" {
		emit(Event{Kind: EventFinish, Text: lastFinishReason})
	}
	if lastUsage != "" {
		emit(Event{Kind: EventUsage, Text: lastUsage})
	}
	if calls := agg.build(); calls != "" {
		emit(Event{Kind: EventToolCalls, Text: calls})
	}
	emit(Event{Kind: EventDone, Text: ""})
}

// sseDrainer 是 SSE 行解析器——空行分隔帧、多行 data 以 "\n" 拼接、注释行忽略。
type sseDrainer struct {
	reader *bufio.Reader
	done   bool
}

// newSSEDrainer 建立解析器（64 KiB 单行上限——工具参数分片可能较长）。
func newSSEDrainer(r io.Reader) *sseDrainer {
	return &sseDrainer{reader: bufio.NewReaderSize(r, 64*1024)}
}

// next 取下一帧 data 载荷——ok=false 表示流结束。
func (d *sseDrainer) next() (string, bool) {
	if d.done {
		return "", false
	}
	var data strings.Builder
	hasData := false
	for {
		line, err := d.reader.ReadString('\n')
		if err != nil {
			d.done = true
			if line == "" {
				if hasData {
					return data.String(), true
				}
				return "", false
			}
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if hasData {
				return data.String(), true
			}
			if d.done {
				return "", false
			}
			continue
		}
		// 注释行（心跳）——忽略
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			value := strings.TrimPrefix(line, "data:")
			value = strings.TrimPrefix(value, " ")
			if hasData {
				data.WriteString("\n")
			}
			data.WriteString(value)
			hasData = true
		}
		if d.done {
			if hasData {
				return data.String(), true
			}
			return "", false
		}
	}
}

// parseDelta 解析增量——choices[0].delta.content / reasoning_content。
//
// 畸形帧返回 false（跳过不产事件）；choices 空数组返回 false（usage-only 尾帧）。
func parseDelta(data string) (string, string, bool) {
	if data == "" {
		return "", "", false
	}
	var root struct {
		Choices []struct {
			Delta struct {
				Content          *string `json:"content"`
				ReasoningContent *string `json:"reasoning_content"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(data), &root); err != nil {
		return "", "", false
	}
	if len(root.Choices) == 0 {
		return "", "", false
	}
	text := ""
	reasoning := ""
	if root.Choices[0].Delta.Content != nil {
		text = *root.Choices[0].Delta.Content
	}
	if root.Choices[0].Delta.ReasoningContent != nil {
		reasoning = *root.Choices[0].Delta.ReasoningContent
	}
	return text, reasoning, true
}

// parseFinishReason 解析 finish_reason（早退：帧内无该字段直接跳过）。
func parseFinishReason(data string) (string, bool) {
	if data == "" || !strings.Contains(data, `"finish_reason"`) {
		return "", false
	}
	var root struct {
		Choices []struct {
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(data), &root); err != nil {
		return "", false
	}
	if len(root.Choices) == 0 || root.Choices[0].FinishReason == nil {
		return "", false
	}
	value := *root.Choices[0].FinishReason
	if value == "" {
		return "", false
	}
	return value, true
}

// parseUsage 解析 usage——prompt / completion / cacheHit 双格式兼容，全零不产事件。
func parseUsage(data string, out *string) bool {
	if data == "" || !strings.Contains(data, `"usage"`) {
		return false
	}
	var root struct {
		Usage *struct {
			PromptTokens        *int64 `json:"prompt_tokens"`
			CompletionTokens    *int64 `json:"completion_tokens"`
			PromptCacheHitToken *int64 `json:"prompt_cache_hit_tokens"`
			PromptTokensDetail  *struct {
				CachedTokens *int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &root); err != nil || root.Usage == nil {
		return false
	}
	var prompt, completion, cacheHit int64
	if root.Usage.PromptTokens != nil {
		prompt = *root.Usage.PromptTokens
	}
	if root.Usage.CompletionTokens != nil {
		completion = *root.Usage.CompletionTokens
	}
	if root.Usage.PromptCacheHitToken != nil {
		cacheHit = *root.Usage.PromptCacheHitToken
	} else if root.Usage.PromptTokensDetail != nil && root.Usage.PromptTokensDetail.CachedTokens != nil {
		cacheHit = *root.Usage.PromptTokensDetail.CachedTokens
	}
	if prompt == 0 && completion == 0 && cacheHit == 0 {
		return false
	}
	*out = `{"prompt":` + itoa64(prompt) + `,"completion":` + itoa64(completion) + `,"cacheHit":` + itoa64(cacheHit) + `}`
	return true
}

// toolCallAggregator 按 index 聚合 tool_calls 增量（id/name 仅首帧；arguments 累积拼接）。
type toolCallAggregator struct {
	ids   map[int]string
	names map[int]string
	args  map[int]*strings.Builder
	order []int
}

// newToolCallAggregator 建立空聚合器。
func newToolCallAggregator() *toolCallAggregator {
	return &toolCallAggregator{
		ids:   map[int]string{},
		names: map[int]string{},
		args:  map[int]*strings.Builder{},
		order: make([]int, 0, 4),
	}
}

// count 聚合条目数。
func (a *toolCallAggregator) count() int { return len(a.order) }

// accumulate 吸收一帧的 tool_calls 增量（畸形帧跳过）。
func (a *toolCallAggregator) accumulate(data string) {
	var root struct {
		Choices []struct {
			Delta struct {
				ToolCalls []struct {
					Index    *int   `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(data), &root); err != nil {
		logAt(2, "SSE 畸形帧跳过: "+err.Error())
		return
	}
	if len(root.Choices) == 0 {
		return
	}
	for _, call := range root.Choices[0].Delta.ToolCalls {
		if call.Index == nil {
			continue
		}
		index := *call.Index
		builder, exists := a.args[index]
		if !exists {
			builder = &strings.Builder{}
			a.args[index] = builder
			a.ids[index] = ""
			a.names[index] = ""
			a.order = append(a.order, index)
		}
		if a.ids[index] == "" && call.ID != "" {
			a.ids[index] = call.ID
		}
		if a.names[index] == "" && call.Function.Name != "" {
			a.names[index] = call.Function.Name
		}
		if call.Function.Arguments != "" {
			builder.WriteString(call.Function.Arguments)
		}
	}
}

// build 聚合结果 → tool_calls JSON 数组（arguments 空串补 "{}"）。
func (a *toolCallAggregator) build() string {
	if len(a.order) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[")
	for i, index := range a.order {
		if i > 0 {
			b.WriteByte(',')
		}
		argsText := a.args[index].String()
		if argsText == "" {
			argsText = "{}"
		}
		b.WriteString(`{"id":`)
		b.WriteString(JSONString(a.ids[index]))
		b.WriteString(`,"type":"function","function":{"name":`)
		b.WriteString(JSONString(a.names[index]))
		b.WriteString(`,"arguments":`)
		b.WriteString(JSONString(argsText))
		b.WriteString("}}")
	}
	b.WriteString("]")
	return b.String()
}

// itoa64 长整数转字符串。
func itoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	var digits [20]byte
	pos := len(digits)
	for v > 0 {
		pos--
		digits[pos] = byte('0' + v%10)
		v /= 10
	}
	if negative {
		pos--
		digits[pos] = '-'
	}
	return string(digits[pos:])
}
