package session

import (
	"sort"
	"strings"
	"sync"
)

// liveTypes 临时区 type 白名单（四值）。
var liveTypes = []string{TypeToolrun, TypeThinksse, TypeReplysse, TypeEmpty}

// ViewBus 是会话视图出口——chat 快照协议 v2（state / persist / live 三段）。
//
// 全量帧每次现取（单一真相源在视图存储），本类不持副本；排序在推送侧完成。
// 「何时推、推给谁」归 HTTP 外观层；本类只回答「当前状态是什么」。
//
// 并发：C# 侧帧泵与建块同在主线程（无锁）；Go 侧建块可发生在后台流 goroutine
// （重试气泡等），帧泵在 Pump goroutine——全部公开入口走互斥锁。
type ViewBus struct {
	// mu 保护全部字段（建块线程写 · 帧泵线程读写）
	mu sync.Mutex
	// attached 推送面就绪（宿主外观层已附加）
	attached bool
	// persist 持久区全表（降级路径：全量数据源未注入时使用；无移除面）
	persist []*ViewBlock
	// fullSource 全量数据源（会话侧注入的视图存储合成出口）
	fullSource func() []*ViewBlock
	// pendingAppend 待推追加（取走即清）
	pendingAppend []*ViewBlock
	// fullPending 全量待发标记（连接建立之外的重置置位）
	fullPending bool
	// liveType 临时区 type（四值白名单之一；初始 empty）
	liveType string
	// liveContext 临时区 context（整段覆盖）
	liveContext string
	// liveDirty 临时区变化标记
	liveDirty bool
	// stateJSON 状态段 JSON
	stateJSON string
	// stateDirty 状态段变化标记
	stateDirty bool
	// logf 出声回调（nil = 静默）
	logf func(level int, message, category string)
}

// NewViewBus 构造空视图总线（state 初值 {}，live 初值 empty）。
func NewViewBus() *ViewBus {
	return &ViewBus{liveType: TypeEmpty, stateJSON: "{}"}
}

// SetLogf 注入出声回调（非法 type 等失败面可见）。
func (v *ViewBus) SetLogf(fn func(level int, message, category string)) {
	v.mu.Lock()
	v.logf = fn
	v.mu.Unlock()
}

// Attach 附加宿主推送面（就绪后各入口才生效）。
func (v *ViewBus) Attach() {
	v.mu.Lock()
	v.attached = true
	v.mu.Unlock()
}

// Ready 推送面是否就绪。
func (v *ViewBus) Ready() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.attached
}

// AttachSource 注入全量数据源（会话侧接线一次）。
func (v *ViewBus) AttachSource(fn func() []*ViewBlock) {
	v.mu.Lock()
	v.fullSource = fn
	v.mu.Unlock()
}

// HasPendingContent 是否仍有待推内容（不含 state 段——state 含每帧变化值）。
func (v *ViewBus) HasPendingContent() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.attached {
		return false
	}
	return v.fullPending || len(v.pendingAppend) > 0 || v.liveDirty
}

// SetLive 临时区重写（唯一入口）——整段覆盖；type 白名单外按 empty 落并出声。
func (v *ViewBus) SetLive(liveType, context string) {
	v.mu.Lock()
	if !v.attached {
		v.mu.Unlock()
		return
	}
	invalid := ""
	known := false
	for _, item := range liveTypes {
		if item == liveType {
			known = true
			break
		}
	}
	if !known {
		invalid = liveType
		liveType = TypeEmpty
		context = ""
	}
	if liveType != v.liveType || context != v.liveContext {
		v.liveType = liveType
		v.liveContext = context
		v.liveDirty = true
	}
	logf := v.logf
	v.mu.Unlock()
	if invalid != "" && logf != nil {
		shown := invalid
		if shown == "" {
			shown = "(空)"
		}
		logf(2, "临时区 type 非法（按 empty 落）: "+shown, "SYS")
	}
}

// SetState 状态段更新（整段比对，相同则零动作）。
func (v *ViewBus) SetState(json string) {
	value := json
	if value == "" {
		value = "{}"
	}
	v.mu.Lock()
	if value == v.stateJSON {
		v.mu.Unlock()
		return
	}
	v.stateJSON = value
	v.stateDirty = true
	v.mu.Unlock()
}

// BuildFull 构建全量帧载荷并清空待推标记。
func (v *ViewBus) BuildFull() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.buildFullLocked()
}

// ResetPending 待推标记复位（宿主推送面失效时调用；只清标记不动内容）。
func (v *ViewBus) ResetPending() {
	v.mu.Lock()
	v.pendingAppend = nil
	v.liveDirty = false
	v.stateDirty = false
	v.fullPending = false
	v.mu.Unlock()
}

// TryTakeFrame 取帧——无变化返回 false（零字节）；全量待发优先。
func (v *ViewBus) TryTakeFrame() (string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.attached {
		return "", false
	}
	if v.fullPending {
		v.fullPending = false
		return v.buildFullLocked(), true
	}
	if len(v.pendingAppend) == 0 && !v.stateDirty && !v.liveDirty {
		return "", false
	}
	parts := make([]string, 0, 3)
	if len(v.pendingAppend) > 0 {
		appended := v.pendingAppend
		v.pendingAppend = nil
		items := make([]string, 0, len(appended))
		for _, block := range appended {
			items = append(items, block.ItemJSON())
		}
		parts = append(parts, `"persist":{"mode":"append","items":[`+strings.Join(items, ",")+`]}`)
	}
	if v.stateDirty {
		v.stateDirty = false
		parts = append(parts, `"state":`+v.stateJSONText())
	}
	if v.liveDirty {
		v.liveDirty = false
		parts = append(parts, `"live":`+v.liveJSON())
	}
	return "{" + strings.Join(parts, ",") + "}", true
}

// PushPersist 持久块记入——只增，记入追加队列。
func (v *ViewBus) PushPersist(block *ViewBlock) {
	if block == nil {
		return
	}
	v.mu.Lock()
	if !v.attached {
		v.mu.Unlock()
		return
	}
	v.persist = append(v.persist, block)
	v.pendingAppend = append(v.pendingAppend, block)
	v.mu.Unlock()
}

// ResetAll 状态清空——持久区与临时区全清并置全量待发标记（会话重置即新会话）。
func (v *ViewBus) ResetAll() {
	v.mu.Lock()
	v.persist = nil
	v.pendingAppend = nil
	v.liveType = TypeEmpty
	v.liveContext = ""
	v.fullPending = true
	v.mu.Unlock()
}

// BuildFullReadOnly 构建全量帧但不重置待推标记（只读观测面）。
//
// 多实例共享同一会话时（管理面板 + 默认猫对话端口）：推送方是唯一取帧者（TryTakeFrame），
// 观测方取首帧不得吃掉对方尚未取走的追加块 / 临时区帧——否则对话页会丢块。
func (v *ViewBus) BuildFullReadOnly() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	blocks := v.fullBlocksLocked()
	items := make([]string, 0, len(blocks))
	for _, block := range blocks {
		items = append(items, block.ItemJSON())
	}
	return `{"v":2,"state":` + v.stateJSONText() +
		`,"persist":{"mode":"full","items":[` + strings.Join(items, ",") + `]}` +
		`,"live":` + v.liveJSON() + `}`
}

// buildFullLocked 构建全量帧（调用方持锁）。
func (v *ViewBus) buildFullLocked() string {
	blocks := v.fullBlocksLocked()
	items := make([]string, 0, len(blocks))
	for _, block := range blocks {
		items = append(items, block.ItemJSON())
	}
	v.pendingAppend = nil
	v.liveDirty = false
	v.stateDirty = false
	v.fullPending = false
	return `{"v":2,"state":` + v.stateJSONText() +
		`,"persist":{"mode":"full","items":[` + strings.Join(items, ",") + `]}` +
		`,"live":` + v.liveJSON() + `}`
}

// fullBlocksLocked 全量块集——单一真相源优先（注入的数据源），未注入时回落内部累积；时间戳升序（调用方持锁）。
func (v *ViewBus) fullBlocksLocked() []*ViewBlock {
	var all []*ViewBlock
	if v.fullSource != nil {
		all = v.fullSource()
	}
	if all == nil {
		all = v.persist
	}
	out := make([]*ViewBlock, len(all))
	copy(out, all)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp < out[j].Timestamp })
	return out
}

// stateJSONText 状态段 JSON（空回落空对象）。
func (v *ViewBus) stateJSONText() string {
	if v.stateJSON == "" {
		return "{}"
	}
	return v.stateJSON
}

// liveJSON 临时区 JSON——{"type":…,"context":…}。
func (v *ViewBus) liveJSON() string {
	return `{"type":` + jsonStr(v.liveType) + `,"context":` + jsonStr(v.liveContext) + `}`
}
