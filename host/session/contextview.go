package session

// 前文条目视图三件——对话页弹层数据源（GET /api/v1/context · /api/v1/keyinfo · /api/v1/fullctx）。
//
// 对齐 C#：BuildContextView（ChatBridge，条目源 = 会话消息序列）· BuildKeyInfoView（视图层
// 旧会话留档同源四部分：加载报告 / user 消息 / 正式回复 / 每轮结算）· BuildFullContextView
// （完整前文视图；Go 侧无独立留档层，条目源 = 内存前文——与 context 同源，已知差异）。
// 条目形态三面同构：i/role/tool/chars/time/truncated/preview/content（前端复用同一套渲染）。

import (
	"encoding/json"
	"strconv"
	"strings"
)

// contextItemLimit 前文条目正文上限（C# ContextItemLimit / ItemContentLimit = 8000 字符）。
const contextItemLimit = 8000

// contextPreviewLimit 前文条目摘要上限（C# ContextPreviewLimit = 120 字符）。
const contextPreviewLimit = 120

// defaultContextRatio 默认 token 折算比值（C# A93 缺省 2.4——无实测时用且 estimated=true）。
const defaultContextRatio = 2.4

// RoleName 消息角色名（OpenAI 兼容小写四 role——前端样式类锚）。
func RoleName(role Role) string {
	switch role {
	case RoleSystem:
		return "system"
	case RoleUser:
		return "user"
	case RoleAssistant:
		return "assistant"
	}
	return "tool"
}

// MergeBody 四字段合并正文——content / tool_calls / reasoning / images 按序拼接（空字段跳过、非空字段间一个换行）。
//
// 单点实现：前文条目（/api/v1/context）与完整前文条目（/api/v1/fullctx）共用同一口径
// （对齐 C# FullContextStore.MergeBody）。
func MergeBody(msg Message) string {
	body := msg.Content
	body = mergeBodyPart(body, msg.ToolCallsJSON)
	body = mergeBodyPart(body, msg.ReasoningContent)
	body = mergeBodyPart(body, msg.ImagesJSON)
	return body
}

// mergeBodyPart 正文拼接——非空才拼。
func mergeBodyPart(body, part string) string {
	if part == "" {
		return body
	}
	if body != "" {
		return body + "\n" + part
	}
	return part
}

// BuildContextView 前文条目视图 JSON——GET /api/v1/context 数据源。
//
// max = 返回条目上限（>0 且小于总数时取尾部 + 恒含首条；0 = 不限）。
func (s *Session) BuildContextView(max int) string {
	msgs := s.Context.Messages()
	total := len(msgs)
	start := 0
	if max > 0 && total > max {
		start = total - max
	}
	totalChars := 0
	for _, msg := range msgs {
		totalChars += utf16Len(MergeBody(msg))
	}
	items := make([]string, 0, total-start+1)
	if start > 0 {
		// 首条恒在窗口内——前文头部（system 注入块）不因尾部窗口而不可见（C# 2026-10-02 判例）
		items = append(items, buildContextItem(msgs, 0))
	}
	for i := start; i < total; i++ {
		items = append(items, buildContextItem(msgs, i))
	}
	var b strings.Builder
	b.WriteString(`{"ok":true,"sessionId":`)
	b.WriteString(jsonStr(s.SessionInstanceID()))
	b.WriteString(`,"count":`)
	b.WriteString(strconv.Itoa(total))
	b.WriteString(`,"start":`)
	b.WriteString(strconv.Itoa(start + 1))
	b.WriteString(`,"shown":`)
	b.WriteString(strconv.Itoa(len(items)))
	b.WriteString(`,"chars":`)
	b.WriteString(strconv.Itoa(totalChars))
	b.WriteString(`,"ctxTokens":`)
	b.WriteString(strconv.FormatInt(s.ContextTokensKnown(), 10))
	b.WriteString(`,"items":[`)
	b.WriteString(strings.Join(items, ","))
	b.WriteString("]}")
	return b.String()
}

// buildContextItem 单条前文条目——i / role / tool / chars / time / truncated / preview / content。
func buildContextItem(msgs []Message, index int) string {
	msg := msgs[index]
	full := MergeBody(msg)
	truncated := utf16Len(full) > contextItemLimit
	body := full
	if truncated {
		body = truncateUTF16(full, contextItemLimit)
	}
	var b strings.Builder
	b.WriteString(`{"i":`)
	b.WriteString(strconv.Itoa(index + 1))
	b.WriteString(`,"role":`)
	b.WriteString(jsonStr(RoleName(msg.Role)))
	b.WriteString(`,"tool":`)
	b.WriteString(jsonStr(msg.ToolName))
	b.WriteString(`,"chars":`)
	b.WriteString(strconv.Itoa(utf16Len(full)))
	b.WriteString(`,"time":`)
	b.WriteString(strconv.FormatInt(msg.CreatedAt, 10))
	b.WriteString(`,"truncated":`)
	b.WriteString(strconv.FormatBool(truncated))
	b.WriteString(`,"preview":`)
	b.WriteString(jsonStr(previewText(full, contextPreviewLimit)))
	b.WriteString(`,"content":`)
	b.WriteString(jsonStr(body))
	b.WriteString("}")
	return b.String()
}

// BuildKeyInfoView 关键信息视图 JSON——GET /api/v1/keyinfo 数据源。
//
// 内容 = 旧会话留档同源四部分（加载报告 / user 消息 / 正式回复 / 每轮结算）；
// 思考、工具卡、间隙文本、错误不进（与 C# 同口径）。
func (s *Session) BuildKeyInfoView(max int) string {
	all := make([]keyInfoItem, 0, 64)
	if report := buildInjectReportText(s.Views); report != "" {
		all = append(all, keyInfoItem{role: "report", time: 0, content: report})
	}
	if s.Views != nil {
		for _, block := range s.Views.Blocks() {
			switch block.RenderType {
			case TypeRoundSum, TypeRoundSummary:
				all = append(all, keyInfoItem{role: "roundsum", time: block.Timestamp, content: buildRoundSumLine(block.Payload)})
			case TypeText:
				all = append(all, keyInfoItem{role: "reply", time: block.Timestamp, content: blockText(block.Payload)})
			case TypeUser, TypeSystemAutoMessage:
				all = append(all, keyInfoItem{role: "user", time: block.Timestamp, content: blockText(block.Payload)})
			}
		}
	}
	total := len(all)
	totalChars := 0
	for i := range all {
		totalChars += utf16Len(all[i].content)
	}
	start := 0
	if max > 0 && total > max {
		start = total - max
	}
	items := make([]string, 0, total-start)
	for i := start; i < total; i++ {
		items = append(items, buildKeyInfoItem(i+1, all[i]))
	}
	var b strings.Builder
	b.WriteString(`{"ok":true,"count":`)
	b.WriteString(strconv.Itoa(total))
	b.WriteString(`,"start":`)
	b.WriteString(strconv.Itoa(start + 1))
	b.WriteString(`,"shown":`)
	b.WriteString(strconv.Itoa(len(items)))
	b.WriteString(`,"chars":`)
	b.WriteString(strconv.Itoa(totalChars))
	b.WriteString(`,"items":[`)
	b.WriteString(strings.Join(items, ","))
	b.WriteString("]}")
	return b.String()
}

// keyInfoItem 关键信息条目——类别（report / user / reply / roundsum）+ 时刻 + 正文。
type keyInfoItem struct {
	// role 条目类别
	role string
	// time 条目时刻（Unix 毫秒；0 = 无时刻）
	time int64
	// content 条目正文
	content string
}

// buildKeyInfoItem 单条关键信息条目——形态与 context 条目同构（前端同一套渲染）。
func buildKeyInfoItem(index int, item keyInfoItem) string {
	full := item.content
	truncated := utf16Len(full) > contextItemLimit
	body := full
	if truncated {
		body = truncateUTF16(full, contextItemLimit)
	}
	var b strings.Builder
	b.WriteString(`{"i":`)
	b.WriteString(strconv.Itoa(index))
	b.WriteString(`,"role":`)
	b.WriteString(jsonStr(item.role))
	b.WriteString(`,"time":`)
	b.WriteString(strconv.FormatInt(item.time, 10))
	b.WriteString(`,"chars":`)
	b.WriteString(strconv.Itoa(utf16Len(full)))
	b.WriteString(`,"truncated":`)
	b.WriteString(strconv.FormatBool(truncated))
	b.WriteString(`,"preview":`)
	b.WriteString(jsonStr(previewText(full, contextPreviewLimit)))
	b.WriteString(`,"content":`)
	b.WriteString(jsonStr(body))
	b.WriteString("}")
	return b.String()
}

// BuildFullContextView 完整前文视图 JSON——GET /api/v1/fullctx 数据源。
//
// 挂载留档（SetFullContext）时从 sessions_ctx 文本读——落盘权威 = 留档，视图只是读取面，
// 读取前强制采集一次（展示面不受 30 秒节流滞后影响）；未挂载回落内存前文视图。
func (s *Session) BuildFullContextView(max int) string {
	s.mu.Lock()
	store := s.fullCtx
	s.mu.Unlock()
	if store != nil {
		return store.BuildView(max)
	}
	return s.buildMemoryFullContextView(max)
}

// buildMemoryFullContextView 内存前文视图——未挂载留档时的回退面（条目源 = 内存前文）。
//
// tokens 按 chars ÷ ratio 折算，ratio 有实测（本会话最近请求 ctxTokens / chars）用实测，
// 否则缺省 2.4 且 estimated=true——与 C# 前端标注语义一致。
func (s *Session) buildMemoryFullContextView(max int) string {
	msgs := s.Context.Messages()
	total := len(msgs)
	start := 0
	if max > 0 && total > max {
		start = total - max
	}
	totalChars := 0
	for _, msg := range msgs {
		totalChars += utf16Len(MergeBody(msg))
	}
	ratio := defaultContextRatio
	estimated := true
	if tokens := s.ContextTokensKnown(); tokens > 0 && totalChars > 0 {
		// A93 实测反推：ratio = 字符数 ÷ token 数（每 token 字符数，缺省 2.4）
		ratio = float64(totalChars) / float64(tokens)
		estimated = false
	}
	items := make([]string, 0, total-start+1)
	if start > 0 {
		items = append(items, buildContextItem(msgs, 0))
	}
	for i := start; i < total; i++ {
		items = append(items, buildContextItem(msgs, i))
	}
	tokens := int64(float64(totalChars) / ratio)
	var b strings.Builder
	b.WriteString(`{"ok":true,"session":`)
	b.WriteString(jsonStr(s.CatKey))
	b.WriteString(`,"path":"","ratio":`)
	b.WriteString(strconv.FormatFloat(roundTo(ratio, 2), 'f', -1, 64))
	b.WriteString(`,"estimated":`)
	b.WriteString(strconv.FormatBool(estimated))
	b.WriteString(`,"count":`)
	b.WriteString(strconv.Itoa(total))
	b.WriteString(`,"start":`)
	b.WriteString(strconv.Itoa(start + 1))
	b.WriteString(`,"shown":`)
	b.WriteString(strconv.Itoa(len(items)))
	b.WriteString(`,"chars":`)
	b.WriteString(strconv.Itoa(totalChars))
	b.WriteString(`,"tokens":`)
	b.WriteString(strconv.FormatInt(tokens, 10))
	b.WriteString(`,"items":[`)
	b.WriteString(strings.Join(items, ","))
	b.WriteString("]}")
	return b.String()
}

// blockText 视图块正文——user / text 块取载荷 text（解析失败返回空串，不静默丢块）。
func blockText(payloadJSON string) string {
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return ""
	}
	return payload.Text
}

// buildInjectReportText 加载报告正文——逐文件清单 + 工具组清单（无报告 = 空串；解析失败降级为原文）。
//
// 对齐 C# BuildInjectReportText = AppendInjectReportBody + Trim（正文实现单点 = 留档同款 appendInjectReportBody）。
func buildInjectReportText(views *ViewStore) string {
	if views == nil {
		return ""
	}
	report := views.GetInjectReport()
	if report == "" {
		return ""
	}
	var sb strings.Builder
	appendInjectReportBody(&sb, report)
	return strings.TrimSpace(sb.String())
}

// previewText 条目摘要——单行化后取首 limit 码元（超出追加 "…"）。
func previewText(body string, limit int) string {
	if body == "" {
		return ""
	}
	flat := strings.ReplaceAll(body, "\r", " ")
	flat = strings.ReplaceAll(flat, "\n", " ")
	if utf16Len(flat) <= limit {
		return flat
	}
	return truncateUTF16(flat, limit) + "…"
}

// utf16Len 按 UTF-16 码元计长（对齐 C# string.Length）。
func utf16Len(text string) int {
	n := 0
	for _, r := range text {
		if r > 0xFFFF {
			n += 2
			continue
		}
		n++
	}
	return n
}

// truncateUTF16 按 UTF-16 码元截断（不追加省略号——调用方决定）。
func truncateUTF16(text string, limit int) string {
	units := 0
	for index, r := range text {
		width := 1
		if r > 0xFFFF {
			width = 2
		}
		if units+width > limit {
			return text[:index]
		}
		units += width
	}
	return text
}

// roundTo 浮点四舍五入到指定小数位（C# Math.Round 同向——远离零）。
func roundTo(value float64, digits int) float64 {
	factor := 1.0
	for i := 0; i < digits; i++ {
		factor *= 10
	}
	if value >= 0 {
		return float64(int64(value*factor+0.5)) / factor
	}
	return float64(int64(value*factor-0.5)) / factor
}
