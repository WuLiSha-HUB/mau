package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// viewBlockTag 块行标记——JSONL 唯一行型（{"t":"b",…ViewBlock 全字段}）。
const viewBlockTag = "b"

// viewInjectReportType 注入报告条目型（普通块；读面取最后一条；ts=0 恒排最前）。
const viewInjectReportType = "inject_report"

// pendingTool 是待配对工具条目——assistant 工具调用登记，tool 结果到达时生成工具卡条目。
type pendingTool struct {
	// ToolCallID 工具调用 ID——与 tool 消息配对锚点
	ToolCallID string
	// Name 工具名
	Name string
	// Arguments 参数摘要（≤200）
	Arguments string
	// Index 并发序号（1-based——tool_calls 数组顺序）
	Index int
	// Total 并发总数（同批 tool_calls 数组长度）
	Total int
	// Ts 声明时刻（Unix 毫秒）——工具卡条目时间戳基准
	Ts int64
}

// toolCallEntry 单条 tool_calls 声明——登记与顶尾补差共用的解析形态（OpenAI wire）。
type toolCallEntry struct {
	// ID 工具调用 ID——与 tool 消息的 ToolCallID 配对
	ID string `json:"id"`
	// Function 调用函数面
	Function struct {
		// Name 工具名
		Name string `json:"name"`
		// Arguments 参数 JSON 文本（原样）
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ViewStore 是会话视图存储——视图层持久区（与真实前文并列，载入即权威）。
//
// 条目在事件发生点一次性定稿落盘（JSONL 每块 append 一行——写入序即权威序）；载入逐行读回，不从前文重建。
// v2 契约：持久即持久——无键面 / 无换手 / 无移出；残留面只余顶尾补差与对账哨兵，且不得改写已有条目。
type ViewStore struct {
	// mu 保护容器（HTTP 线程读全量 · 主线程写）
	mu sync.Mutex
	// path 视图文件路径——sessions/<id>/<id>.view.jsonl
	path string
	// blocks 内存视图条目——按生成序（真实前文 append 序）
	blocks []*ViewBlock
	// pendingTools 待配对工具
	pendingTools []pendingTool
	// roundSums 轮末统计条目（roundsum / roundsummary）
	roundSums []*ViewBlock
	// gapTexts 间隙文本条目（工具轮 seal 文本）
	gapTexts []*ViewBlock
	// errors 错误条目（LLM 错误气泡）
	errors []*ViewBlock
	// retries 重试过程条目（retry 气泡）
	retries []*ViewBlock
	// lastTs 最近条目时间戳——单调补差基准
	lastTs int64
	// round 当前轮号（条目业务定位字段）
	round int
	// OnBlockAppended 建条通知（持久区「建条即推」的单一出口；nil = 无消费方）
	OnBlockAppended func(*ViewBlock)
}

// NewViewStore 建立会话视图存储。
func NewViewStore(path string) *ViewStore { return &ViewStore{path: path} }

// Path 视图文件路径（诊断用）。
func (v *ViewStore) Path() string { return v.path }

// SetRound 设当前轮号（此后建条目的 round 字段取值）。
func (v *ViewStore) SetRound(round int) {
	v.mu.Lock()
	v.round = round
	v.mu.Unlock()
}

// SetInjectReport 注入报告条目——普通块（ts=0 恒排最前；覆写语义——同型旧块移除）。
func (v *ViewStore) SetInjectReport(payloadJSON string) {
	v.mu.Lock()
	v.removeInjectReportLocked()
	if payloadJSON == "" {
		v.mu.Unlock()
		v.Rewrite()
		return
	}
	block := &ViewBlock{Timestamp: 0, RenderType: viewInjectReportType, Payload: payloadJSON, MsgIndex: -1, Round: v.round}
	v.blocks = append([]*ViewBlock{block}, v.blocks...)
	line := viewBlockLine(block)
	v.mu.Unlock()
	v.appendLine(line)
}

// GetInjectReport 注入报告 JSON——读（空串 = 无注入报告）。
func (v *ViewStore) GetInjectReport() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	for i := len(v.blocks) - 1; i >= 0; i-- {
		if v.blocks[i].RenderType == viewInjectReportType {
			return v.blocks[i].Payload
		}
	}
	return ""
}

// removeInjectReportLocked 移除注入报告块（调用方持锁）。
func (v *ViewStore) removeInjectReportLocked() {
	kept := make([]*ViewBlock, 0, len(v.blocks))
	for _, block := range v.blocks {
		if block.RenderType == viewInjectReportType {
			continue
		}
		kept = append(kept, block)
	}
	v.blocks = kept
}

// Blocks 内存视图条目——按时间戳升序归并（全量帧数据源）。
func (v *ViewStore) Blocks() []*ViewBlock {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.mergeLocked()
}

// mergeLocked 合并面——五类块源按时间戳升序归并（同一坐标系；同刻取先声明源）。
func (v *ViewStore) mergeLocked() []*ViewBlock {
	sources := [][]*ViewBlock{v.blocks, v.gapTexts, v.roundSums, v.errors, v.retries}
	total := 0
	for _, source := range sources {
		total += len(source)
	}
	merged := make([]*ViewBlock, 0, total)
	cursors := make([]int, len(sources))
	for len(merged) < total {
		best := -1
		bestTs := int64(0)
		for si, source := range sources {
			if cursors[si] >= len(source) {
				continue
			}
			ts := source[cursors[si]].Timestamp
			if best < 0 || ts < bestTs {
				best = si
				bestTs = ts
			}
		}
		merged = append(merged, sources[best][cursors[best]])
		cursors[best]++
	}
	return merged
}

// OnUserMessage 真实前文 append 钩子——用户消息 → user 条目（人工路径专属）。
func (v *ViewStore) OnUserMessage(msg Message, msgIndex int, origin string) {
	payload := `{"text":` + jsonStr(msg.Content)
	if origin != "" {
		payload += `,"origin":` + jsonStr(origin)
	}
	payload += "}"
	v.appendDerived(msg, TypeUser, payload, msgIndex, 0)
}

// OnSystemMessage 真实前文 append 钩子——系统注入消息 → systemautomessage 条目（载荷携带原始来源族名）。
func (v *ViewStore) OnSystemMessage(msg Message, msgIndex int, source, origin string) {
	payload := `{"text":` + jsonStr(msg.Content) + `,"source":` + jsonStr(source)
	if origin != "" {
		payload += `,"origin":` + jsonStr(origin)
	}
	payload += "}"
	v.appendDerived(msg, TypeSystemAutoMessage, payload, msgIndex, 0)
}

// OnAssistantText 真实前文 append 钩子——assistant 纯文本回复 → reason 条目（有思考时）+ text 条目。
func (v *ViewStore) OnAssistantText(msg Message, msgIndex int, reasonDurMs int64) {
	if msg.ReasoningContent != "" {
		v.appendDerived(msg, TypeReason, BuildReason(msg.ReasoningContent, reasonDurMs), msgIndex, 0)
	}
	v.appendDerived(msg, TypeText, `{"text":`+jsonStr(msg.Content)+`}`, msgIndex, 0)
}

// OnAssistantToolCalls 真实前文 append 钩子——assistant 工具调用声明 → reason 条目（有思考时）+ 登记待配对工具。
func (v *ViewStore) OnAssistantToolCalls(msg Message, msgIndex int, reasonDurMs int64) {
	if msg.ReasoningContent != "" {
		v.appendDerived(msg, TypeReason, BuildReason(msg.ReasoningContent, reasonDurMs), msgIndex, 0)
	}
	v.registerPendingTools(msg.ToolCallsJSON, msg.CreatedAt)
}

// OnToolResult 真实前文 append 钩子——tool 结果 → 配对生成工具卡条目（孤立 tool 丢弃）。
func (v *ViewStore) OnToolResult(msg Message, msgIndex int, durMs, queueMs, runMs int64) {
	v.mu.Lock()
	target := -1
	for i := len(v.pendingTools) - 1; i >= 0; i-- {
		if v.pendingTools[i].ToolCallID == msg.ToolCallID {
			target = i
			break
		}
	}
	if target < 0 {
		v.mu.Unlock()
		return // 孤立 tool 丢弃
	}
	pending := v.pendingTools[target]
	v.pendingTools = append(v.pendingTools[:target], v.pendingTools[target+1:]...)
	v.mu.Unlock()
	name := pending.Name
	if name == "" {
		name = msg.ToolName
	}
	// 视图层报错中文注释——真实前文保持原文，仅视图条目追加中文注释
	viewResult := ApplyErrorNote(msg.Content)
	payload := BuildToolCard(name, pending.Arguments, &viewResult, pending.Index, pending.Total, durMs, queueMs, runMs)
	v.appendDerived(msg, TypeToolCard, payload, msgIndex, pending.Ts)
}

// appendDerived 生成前文派生视图条目——时间戳取消息 CreatedAt（工具卡可指定时间戳覆盖）。
func (v *ViewStore) appendDerived(msg Message, renderType, payloadJSON string, msgIndex int, timestampOverride int64) {
	ts := msg.CreatedAt
	if timestampOverride > 0 {
		ts = timestampOverride
	}
	block := v.newBlock(renderType, payloadJSON, ts, msgIndex)
	v.mu.Lock()
	v.blocks = append(v.blocks, block)
	v.mu.Unlock()
	v.appendLine(viewBlockLine(block))
	v.emit(block)
}

// AppendRoundSummary 追加轮末统计条目——roundsum（Token 消耗 + 工具次数 + 总耗时 + 用时）。
func (v *ViewStore) AppendRoundSummary(payloadJSON string, timestamp int64) {
	block := v.newBlock(TypeRoundSum, payloadJSON, timestamp, -1)
	v.mu.Lock()
	v.roundSums = append(v.roundSums, block)
	v.mu.Unlock()
	v.appendLine(viewBlockLine(block))
	v.emit(block)
}

// AppendRoundSummaryNote 追加轮末总结条目——roundsummary（LLMSimpleRequest 产出一句话总结）。
//
// 与 roundsum 同属轮末条目族（同容器承载、type 区分——复用车、落盘、合并、补差全链）；写入即落盘。
// text 为总结文本（失败侧为 ERR| 文本——失败可见）；timestamp = roundsum ts + 1（固定插在轮末统计之后）；
// audioName 为拼接后单文件名（仅供前端点播，空 = 不写该字段）。
func (v *ViewStore) AppendRoundSummaryNote(text string, timestamp int64, audioName string) {
	if text == "" {
		return
	}
	payload := `{"text":` + jsonStr(text)
	if audioName != "" {
		payload += `,"audio":` + jsonStr(audioName)
	}
	payload += "}"
	block := v.newBlock(TypeRoundSummary, payload, timestamp, -1)
	v.mu.Lock()
	v.roundSums = append(v.roundSums, block)
	v.mu.Unlock()
	v.appendLine(viewBlockLine(block))
	v.emit(block)
}

// AppendGapText 追加间隙文本条目——工具轮 seal 文本（模型调用工具前说的话）。
func (v *ViewStore) AppendGapText(content string, timestamp int64) {
	if content == "" {
		return
	}
	block := v.newBlock(TypeGapText, `{"text":`+jsonStr(content)+`}`, timestamp, -1)
	v.mu.Lock()
	v.gapTexts = append(v.gapTexts, block)
	v.mu.Unlock()
	v.appendLine(viewBlockLine(block))
	v.emit(block)
}

// AppendError 追加错误条目——LLM 错误气泡（非真实前文派生；写入即落盘）。
func (v *ViewStore) AppendError(text string, timestamp int64) {
	if text == "" {
		return
	}
	block := v.newBlock(TypeError, `{"text":`+jsonStr(text)+`}`, timestamp, -1)
	v.mu.Lock()
	v.errors = append(v.errors, block)
	v.mu.Unlock()
	v.appendLine(viewBlockLine(block))
	v.emit(block)
}

// AppendRetry 追加重试条目——retry 气泡（只增不改）。
func (v *ViewStore) AppendRetry(payloadJSON string, timestamp int64) {
	block := v.newBlock(TypeRetry, payloadJSON, timestamp, -1)
	v.mu.Lock()
	v.retries = append(v.retries, block)
	v.mu.Unlock()
	v.appendLine(viewBlockLine(block))
	v.emit(block)
}

// newBlock 建条单点——时间戳单调化 + 业务定位字段就位。
func (v *ViewStore) newBlock(renderType, payloadJSON string, timestamp int64, msgIndex int) *ViewBlock {
	v.mu.Lock()
	if timestamp <= v.lastTs {
		timestamp = v.lastTs + 1
	}
	v.lastTs = timestamp
	round := v.round
	v.mu.Unlock()
	return &ViewBlock{Timestamp: timestamp, RenderType: renderType, Payload: payloadJSON, MsgIndex: msgIndex, Round: round}
}

// emit 建条通知（无消费方零动作）。
func (v *ViewStore) emit(block *ViewBlock) {
	if v.OnBlockAppended != nil {
		v.OnBlockAppended(block)
	}
}

// registerPendingTools 登记待配对工具——解析 tool_calls JSON 数组（解析失败空登记）。
func (v *ViewStore) registerPendingTools(toolCallsJSON string, declaredTs int64) {
	if toolCallsJSON == "" {
		return
	}
	var calls []toolCallEntry
	if err := json.Unmarshal([]byte(toolCallsJSON), &calls); err != nil {
		logAt(LogModuleViewStore, 2, "工具卡登记解析失败，按空登记: "+err.Error(), LogCategorySys)
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for i, call := range calls {
		v.pendingTools = append(v.pendingTools, pendingTool{
			ToolCallID: call.ID,
			Name:       call.Function.Name,
			Arguments:  call.Function.Arguments,
			Index:      i + 1,
			Total:      len(calls),
			Ts:         declaredTs,
		})
	}
}

// Clear 清空视图层——session.new 清前文时同步。
func (v *ViewStore) Clear() {
	v.mu.Lock()
	v.blocks = nil
	v.pendingTools = nil
	v.gapTexts = nil
	v.roundSums = nil
	v.errors = nil
	v.retries = nil
	v.mu.Unlock()
	v.Rewrite()
}

// TruncateFrom 视图层截断——回滚：丢弃前文索引大于切点的条目，与真实前文截断同批物化。
//
// 判定：前文派生条目（MsgIndex ≥ 0）按索引；非前文派生条目（-1）按时间戳界限。
func (v *ViewStore) TruncateFrom(msgIndex int) {
	v.mu.Lock()
	dropMinTs := int64(-1)
	kept := make([]*ViewBlock, 0, len(v.blocks))
	for _, block := range v.blocks {
		if block.MsgIndex > msgIndex {
			if dropMinTs < 0 || block.Timestamp < dropMinTs {
				dropMinTs = block.Timestamp
			}
			continue
		}
		kept = append(kept, block)
	}
	v.blocks = kept
	if dropMinTs >= 0 {
		v.gapTexts = dropBlocksFrom(v.gapTexts, dropMinTs)
		v.roundSums = dropBlocksFrom(v.roundSums, dropMinTs)
		v.errors = dropBlocksFrom(v.errors, dropMinTs)
		v.retries = dropBlocksFrom(v.retries, dropMinTs)
	}
	v.pendingTools = nil
	v.mu.Unlock()
	v.Rewrite()
}

// UpdateMessageContent 按消息索引改写派生块载荷——编辑前文后视图与真实前文对齐（返回改写块数）。
//
// kind 限定改写面：text → user / systemautomessage / text 块；reason → reason 块；
// result → toolcard 块。同索引多块并存（reason + text）时按 kind 各改各的，互不串写。
// 改写后整表重写（编辑是 append-only 的合法例外）；未命中形态（非对象 / 无该字段）不计入。
func (v *ViewStore) UpdateMessageContent(msgIndex int, kind, content string) int {
	field := ""
	switch kind {
	case "text", "reason":
		field = "text"
	case "result":
		field = "result"
	default:
		return 0
	}
	v.mu.Lock()
	changed := 0
	for _, block := range v.blocks {
		if block.MsgIndex != msgIndex {
			continue
		}
		if !viewBlockMatchesKind(block.RenderType, kind) {
			continue
		}
		if payload, ok := payloadWithField(block.Payload, field, content); ok {
			block.Payload = payload
			changed++
		}
	}
	v.mu.Unlock()
	if changed > 0 {
		v.Rewrite()
	}
	return changed
}

// viewBlockMatchesKind 编辑面 → 块型判定（同索引多块并存时按 kind 分流）。
func viewBlockMatchesKind(renderType, kind string) bool {
	switch kind {
	case "text":
		return renderType == TypeUser || renderType == TypeSystemAutoMessage || renderType == TypeText
	case "reason":
		return renderType == TypeReason
	case "result":
		return renderType == TypeToolCard
	}
	return false
}

// TruncateAt 视图层按时间戳截断——非前文派生块（轮结算 / 错误 / 重试 / 间隙文本）作切点。
//
// 保留面：Timestamp 严格早于 cutoff 的全部块（五类块源同口径）；重写落盘。
func (v *ViewStore) TruncateAt(cutoff int64) {
	v.mu.Lock()
	v.blocks = dropBlocksFrom(v.blocks, cutoff)
	v.gapTexts = dropBlocksFrom(v.gapTexts, cutoff)
	v.roundSums = dropBlocksFrom(v.roundSums, cutoff)
	v.errors = dropBlocksFrom(v.errors, cutoff)
	v.retries = dropBlocksFrom(v.retries, cutoff)
	v.pendingTools = nil
	v.mu.Unlock()
	v.Rewrite()
}

// payloadWithField 替换载荷单字段——其余字段按原样（RawMessage）保留。
func payloadWithField(payload, field, value string) (string, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		return "", false
	}
	if _, exists := obj[field]; !exists {
		return "", false
	}
	obj[field] = json.RawMessage(jsonStr(value))
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(obj); err != nil {
		return "", false
	}
	return strings.TrimSuffix(buf.String(), "\n"), true
}

// dropBlocksFrom 丢弃时间戳不早于界限的条目——截断用（保留面 = 界限之前）。
func dropBlocksFrom(list []*ViewBlock, cutoff int64) []*ViewBlock {
	kept := make([]*ViewBlock, 0, len(list))
	for _, block := range list {
		if block.Timestamp >= cutoff {
			continue
		}
		kept = append(kept, block)
	}
	return kept
}

// CopyFrom 视图层播种——fork（session.fork）：把源视图存储的保留面（≤ 切点）复制进本存储，与新猫播种前文同步。
//
// 保留口径与 TruncateFrom 同源：前文派生条目（MsgIndex ≥ 0）保留索引 ≤ msgIndex 者；被丢弃区间的最小时间戳
// 作界限，非前文派生条目（-1）只保留界限之前的（时间戳单调化保证全局严格递增）。
// 条目按引用搬入（只读沿用，不复制对象）；注入报告为普通块随面搬入（MsgIndex=-1 不参与索引判定，天然保留）；
// 待配对工具清空（播种后由新前文重新配对）；lastTs 取保留面最大时间戳后写盘。源存储不受影响（单向复制）。
// round 不随播种（新会话起点由调用方设定——与 C# 基准同口径）。
// nil 源与自身复制按空操作返回（Go 侧防护：自身复制会先清空再搬空；C# 基准无此判定）。
func (v *ViewStore) CopyFrom(source *ViewStore, msgIndex int) {
	if source == nil || source == v {
		return
	}
	// 源快照——两存储各持一把锁，先取一次全量快照，搬迁期间的源面变动不进本轮
	source.mu.Lock()
	srcBlocks := append([]*ViewBlock(nil), source.blocks...)
	srcGapTexts := append([]*ViewBlock(nil), source.gapTexts...)
	srcRoundSums := append([]*ViewBlock(nil), source.roundSums...)
	srcErrors := append([]*ViewBlock(nil), source.errors...)
	srcRetries := append([]*ViewBlock(nil), source.retries...)
	source.mu.Unlock()

	dropMinTs := int64(-1)
	blocks := make([]*ViewBlock, 0, len(srcBlocks))
	for _, block := range srcBlocks {
		if block.MsgIndex > msgIndex {
			if dropMinTs < 0 || block.Timestamp < dropMinTs {
				dropMinTs = block.Timestamp
			}
			continue
		}
		blocks = append(blocks, block)
	}
	v.mu.Lock()
	v.blocks = blocks
	v.gapTexts = copyBlocksBefore(srcGapTexts, dropMinTs)
	v.roundSums = copyBlocksBefore(srcRoundSums, dropMinTs)
	v.errors = copyBlocksBefore(srcErrors, dropMinTs)
	v.retries = copyBlocksBefore(srcRetries, dropMinTs)
	v.pendingTools = nil
	maxTs := int64(0)
	for _, list := range [][]*ViewBlock{v.blocks, v.gapTexts, v.roundSums, v.errors, v.retries} {
		for _, block := range list {
			if block.Timestamp > maxTs {
				maxTs = block.Timestamp
			}
		}
	}
	v.lastTs = maxTs
	v.mu.Unlock()
	v.Rewrite()
}

// copyBlocksBefore 保留时间戳早于界限的条目——播种用（复制引用，不改对象）。cutoff < 0 = 无被丢弃区间（全保留）。
func copyBlocksBefore(source []*ViewBlock, cutoff int64) []*ViewBlock {
	kept := make([]*ViewBlock, 0, len(source))
	for _, block := range source {
		if cutoff >= 0 && block.Timestamp >= cutoff {
			continue
		}
		kept = append(kept, block)
	}
	return kept
}

// AuditEntries 对账哨兵——按索引存在性与角色一致性核对（不一致出声；不修改任何条目）。
func (v *ViewStore) AuditEntries(messages []Message, sample int) int {
	v.mu.Lock()
	blocks := make([]*ViewBlock, len(v.blocks))
	copy(blocks, v.blocks)
	v.mu.Unlock()
	checkedCount := 0
	bad := 0
	for _, block := range blocks {
		if block.MsgIndex < 0 {
			continue
		}
		if sample > 0 && checkedCount >= sample {
			break
		}
		checkedCount++
		if block.MsgIndex >= len(messages) {
			bad++
			logAt(LogModuleViewStore, 3, "对账哨兵：条目来源索引越界（类型 "+block.RenderType+" / 消息 "+itoa(block.MsgIndex)+" / 前文 "+itoa(len(messages))+"）", LogCategorySys)
			continue
		}
		if !viewRoleMatches(block.RenderType, messages[block.MsgIndex]) {
			bad++
			logAt(LogModuleViewStore, 3, "对账哨兵：条目来源角色不符（类型 "+block.RenderType+" / 消息 "+itoa(block.MsgIndex)+"）", LogCategorySys)
		}
	}
	return bad
}

// viewRoleMatches 对账角色一致性判定——条目类型与前文消息角色是否相符。
func viewRoleMatches(renderType string, msg Message) bool {
	switch renderType {
	case TypeUser, TypeSystemAutoMessage:
		return msg.Role == RoleUser
	case TypeText, TypeReason:
		return msg.Role == RoleAssistant
	case TypeToolCard:
		return msg.Role == RoleTool
	}
	return true
}

// AppendTailMissing 顶尾补差——载入后按真实前文补齐视图缺口（append-only；不接线，调用面另定）。
//
// 语义依据：Project/CH4/design-ch4-protocol.md §10.2 / §10.3（载入期受限重建面——缺口由顶尾补差 +
// 对账哨兵处置）与 design-ch4-protocol-chat.md §12.5（reason.durMs=-1 = 载入顶尾补差路径）。
// C# 基准无实现（SessionViewStore.cs:16 仅一行注释）——本方法按设计语义自定：
//   - 覆盖集合 = blocks 容器中 MsgIndex >= 0 的索引（前文派生面）；已覆盖索引不补；
//   - 跳过 system 角色（不进视图）；user → user 块（人工路径形态，origin 空）；
//   - assistant → reason（有 reasoning，durMs=-1）+ text（纯文本路径）/ 登记工具配对（tool_calls 路径）；
//   - tool → toolcard（名称 / 参数 / 序号 / 总数从最近匹配的 tool_calls 声明解析；三时长 -1 未记录）；
//     配不上对则跳过（与实时钩子「孤立 tool 丢弃」同口径）；
//   - 时间戳取消息 CreatedAt（工具卡取声明消息 CreatedAt），经 newBlock 单调化；不改写已有条目。
//
// 返回补齐的视图条目数；重复调用幂等（补上的条目进入覆盖集合）。
func (v *ViewStore) AppendTailMissing(messages []Message) int {
	v.mu.Lock()
	covered := make(map[int]bool, len(v.blocks))
	for _, block := range v.blocks {
		if block.MsgIndex >= 0 {
			covered[block.MsgIndex] = true
		}
	}
	v.mu.Unlock()

	added := 0
	for index := range messages {
		msg := messages[index]
		if msg.Role == RoleSystem || covered[index] {
			continue
		}
		switch msg.Role {
		case RoleUser:
			payload := `{"text":` + jsonStr(msg.Content) + `}`
			v.appendDerived(msg, TypeUser, payload, index, 0)
			added++
		case RoleAssistant:
			if msg.ReasoningContent != "" {
				v.appendDerived(msg, TypeReason, BuildReason(msg.ReasoningContent, -1), index, 0)
				added++
			}
			if msg.ToolCallsJSON != "" {
				// 工具声明路径——实时钩子只登记配对（工具卡由 tool 消息落地）
				v.registerPendingTools(msg.ToolCallsJSON, msg.CreatedAt)
				continue
			}
			v.appendDerived(msg, TypeText, `{"text":`+jsonStr(msg.Content)+`}`, index, 0)
			added++
		case RoleTool:
			call, callIndex, callTotal, declaredAt, ok := findToolCallBefore(messages, index, msg.ToolCallID)
			if !ok {
				continue // 孤立 tool 丢弃——实时钩子同口径
			}
			name := call.Function.Name
			if name == "" {
				name = msg.ToolName
			}
			viewResult := ApplyErrorNote(msg.Content)
			payload := BuildToolCard(name, call.Function.Arguments, &viewResult, callIndex, callTotal, -1, -1, -1)
			v.appendDerived(msg, TypeToolCard, payload, index, declaredAt)
			added++
		}
	}
	return added
}

// findToolCallBefore 反向查找工具结果之前匹配的 tool_calls 声明——顶尾补差专用。
//
// 载入后 pendingTools 为空，配对信息只能回前文解析；返回匹配声明、并发序号（1-based）、
// 同批总数与声明消息 CreatedAt（工具卡时间戳基准）。未命中 ok=false。
func findToolCallBefore(messages []Message, index int, toolCallID string) (toolCallEntry, int, int, int64, bool) {
	if toolCallID == "" {
		return toolCallEntry{}, 0, 0, 0, false
	}
	for i := index - 1; i >= 0; i-- {
		if messages[i].Role != RoleAssistant || messages[i].ToolCallsJSON == "" {
			continue
		}
		var calls []toolCallEntry
		if err := json.Unmarshal([]byte(messages[i].ToolCallsJSON), &calls); err != nil {
			continue
		}
		for k := range calls {
			if calls[k].ID == toolCallID {
				return calls[k], k + 1, len(calls), messages[i].CreatedAt, true
			}
		}
	}
	return toolCallEntry{}, 0, 0, 0, false
}

// Load 载入视图层——逐行读回 JSONL（末行残缺丢弃、非末行坏行跳过，各自出声）。
func (v *ViewStore) Load() {
	raw, err := os.ReadFile(v.path)
	if err != nil {
		if !os.IsNotExist(err) {
			logAt(LogModuleViewStore, 3, "视图载入失败（按空视图继续——本会话历史将空白）: "+err.Error(), LogCategorySys)
		}
		return
	}
	lines := strings.Split(string(raw), "\n")
	lastIndex := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			lastIndex = i
			break
		}
	}
	v.mu.Lock()
	v.blocks = nil
	v.gapTexts = nil
	v.roundSums = nil
	v.errors = nil
	v.retries = nil
	v.pendingTools = nil
	v.mu.Unlock()
	maxTs := int64(0)
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		block, ok := parseViewBlockLine(line)
		if ok {
			v.addLoadedBlock(block)
			if block.Timestamp > maxTs {
				maxTs = block.Timestamp
			}
			continue
		}
		if i == lastIndex {
			logAt(LogModuleViewStore, 2, "视图末行残缺——该行丢弃（无块信息）", LogCategorySys)
		} else {
			logAt(LogModuleViewStore, 2, "视图第 "+itoa(i+1)+" 行损坏——跳过（其余照常加载）", LogCategorySys)
		}
	}
	v.mu.Lock()
	v.lastTs = maxTs
	v.mu.Unlock()
}

// addLoadedBlock 载入分流——按条目型归入对应容器（缺省前文派生面）。
func (v *ViewStore) addLoadedBlock(block *ViewBlock) {
	v.mu.Lock()
	defer v.mu.Unlock()
	switch block.RenderType {
	case TypeGapText:
		v.gapTexts = append(v.gapTexts, block)
	case TypeRoundSum, TypeRoundSummary:
		v.roundSums = append(v.roundSums, block)
	case TypeError:
		v.errors = append(v.errors, block)
	case TypeRetry:
		v.retries = append(v.retries, block)
	case viewInjectReportType:
		v.removeInjectReportLocked()
		v.blocks = append([]*ViewBlock{block}, v.blocks...)
	default:
		v.blocks = append(v.blocks, block)
	}
}

// Rewrite 全量重写视图文件（原子）——清空 / 截断场景唯一写通道。
func (v *ViewStore) Rewrite() {
	v.mu.Lock()
	all := v.mergeLocked()
	var b strings.Builder
	for _, block := range all {
		b.WriteString(viewBlockLine(block))
		b.WriteByte('\n')
	}
	v.mu.Unlock()
	if dir := filepath.Dir(v.path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			logAt(LogModuleViewStore, 3, "视图重写失败: "+err.Error(), LogCategorySys)
			return
		}
	}
	tmp := v.path + "." + NewSessionID() + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		logAt(LogModuleViewStore, 3, "视图重写失败: "+err.Error(), LogCategorySys)
		return
	}
	if err := os.Rename(tmp, v.path); err != nil {
		os.Remove(tmp)
		logAt(LogModuleViewStore, 3, "视图重写失败: "+err.Error(), LogCategorySys)
	}
}

// appendLine 追加一行到视图文件——开-写-关（失败不阻断会话，记 ERR）。
func (v *ViewStore) appendLine(line string) {
	if line == "" {
		return
	}
	if dir := filepath.Dir(v.path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			logAt(LogModuleViewStore, 3, "视图增量落盘失败（本条未落盘）: "+err.Error(), LogCategorySys)
			return
		}
	}
	file, err := os.OpenFile(v.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		logAt(LogModuleViewStore, 3, "视图增量落盘失败（本条未落盘）: "+err.Error(), LogCategorySys)
		return
	}
	defer file.Close()
	if _, err := file.WriteString(line + "\n"); err != nil {
		logAt(LogModuleViewStore, 3, "视图增量落盘失败（本条未落盘）: "+err.Error(), LogCategorySys)
	}
}

// viewBlockLine 构建块行——ViewBlock 全字段 + 行型标记（t=b）。
func viewBlockLine(block *ViewBlock) string {
	return `{"Timestamp":` + itoa64(block.Timestamp) +
		`,"RenderType":` + jsonStr(block.RenderType) +
		`,"Payload":` + jsonStr(block.Payload) +
		`,"MsgIndex":` + itoa(block.MsgIndex) +
		`,"Round":` + itoa(block.Round) +
		`,"t":"b"}`
}

// parseViewBlockLine 解析一行——块行（t=b）；失败返回 false（坏行——调用方按位置出警）。
func parseViewBlockLine(line string) (*ViewBlock, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return nil, false
	}
	if rawString(raw, "t") != viewBlockTag {
		return nil, false
	}
	return &ViewBlock{
		Timestamp:  rawInt64(raw, "Timestamp"),
		RenderType: rawString(raw, "RenderType"),
		Payload:    rawString(raw, "Payload"),
		MsgIndex:   int(rawInt64(raw, "MsgIndex")),
		Round:      int(rawInt64(raw, "Round")),
	}, true
}
