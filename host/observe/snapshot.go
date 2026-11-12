package observe

import (
	"os"
	"sync/atomic"
)

// SnapshotVersion 是快照协议版本（对齐 C# BuildSnapshotJson 的 version = 3）。
const SnapshotVersion = 3

// Cat 是一条 Flow 注册表条目（快照 cats 段——不是猫列表）。
//
// 猫列表走 /api/v1/cats；cats 段是 Runtime 真实截面（FlowStatusV3 四柱映射）。
type Cat struct {
	// Name 注册名
	Name string `json:"name"`
	// ID 注册 ID（数字——对齐 C# FlowEntry.Id）
	ID int64 `json:"id"`
	// Type 类型名
	Type string `json:"type"`
	// Kind 类别
	Kind string `json:"kind"`
	// Faulted 是否加载失败
	Faulted bool `json:"faulted"`
	// FaultReason 失败原因（空串 = 正常）
	FaultReason string `json:"faultReason"`
	// Status 四柱状态（faulted / 无观测面时为 null）
	Status *CatStatus `json:"status"`
	// Desc 实体自述（多行自由形态；无自述输出空数组——对齐 C# desc 字段恒在）
	Desc []string `json:"desc"`
}

// CatStatus 是 Flow 状态四柱（FlowStatusV3 的快照投影）。
type CatStatus struct {
	// StateLines 状态行
	StateLines []string `json:"stateLines"`
	// Sensors 传感器值
	Sensors []SensorValue `json:"sensors"`
	// Slots 槽位可用度
	Slots []SlotValue `json:"slots"`
	// Wires 导线状态
	Wires []WireStatus `json:"wires"`
}

// SensorValue 是一条传感器值。
type SensorValue struct {
	// Name 名称
	Name string `json:"name"`
	// Value 值
	Value string `json:"value"`
}

// SlotValue 是一条槽位可用度。
type SlotValue struct {
	// Name 名称
	Name string `json:"name"`
	// Available 可用
	Available bool `json:"available"`
	// Capacity 容量
	Capacity int `json:"capacity"`
}

// WireStatus 是一条导线状态。
type WireStatus struct {
	// Name 名称
	Name string `json:"name"`
	// Busy 是否在途
	Busy bool `json:"busy"`
	// LastTriggerFrame 最近触发帧
	LastTriggerFrame int64 `json:"lastTriggerFrame"`
	// TimedOut 是否超时
	TimedOut bool `json:"timedOut"`
}

// Tool 是工具注册表条目（快照 tools 段）。
type Tool struct {
	// Name 工具名
	Name string `json:"name"`
	// Group 归属 Flow
	Group string `json:"group"`
	// Builtin 内置标记
	Builtin bool `json:"builtin"`
}

// Box 是 DataBox 条目（快照 boxes 段）。
type Box struct {
	// Scope 盒子域
	Scope string `json:"scope"`
	// Key 盒子键
	Key string `json:"key"`
	// T 类型标记（b/n/s/o）
	T string `json:"t"`
	// Value 值（复杂对象摘要化）
	Value any `json:"value"`
}

// OA 是快照 oa 段——工单四态计数。
type OA struct {
	// Open 待认领
	Open int `json:"open"`
	// Work 已认领
	Work int `json:"work"`
	// Closed 已完成
	Closed int `json:"closed"`
	// Timeout 已超时
	Timeout int `json:"timeout"`
}

// Snapshot 是全量快照（协议 v1.2：十二字段）。
type Snapshot struct {
	// Version 协议版本
	Version int `json:"version"`
	// PID 进程 ID
	PID int `json:"pid"`
	// Frame 全局帧号
	Frame int64 `json:"frame"`
	// Cats Flow 注册表条目
	Cats []Cat `json:"cats"`
	// Sessions 会话状态条目
	Sessions []any `json:"sessions"`
	// OA 工单四态计数
	OA OA `json:"oa"`
	// Tools 工具注册表条目
	Tools []Tool `json:"tools"`
	// Boot 启动健康段
	Boot BootSegment `json:"boot"`
	// Boxes DataBox 截面
	Boxes []Box `json:"boxes"`
	// Deps 外部依赖探测结果
	Deps []DepResult `json:"deps"`
	// Res 资源占用
	Res Res `json:"res"`
	// Logs 日志条目（includeLogs=false 时为空数组；GET ?logs=N 时动态替换）
	Logs []LogItem `json:"logs"`
}

// FrameCounter 是进程级帧号（对齐 C# FlowRunner.GlobalFrame 的单调递增语义）。
//
// P3-a 无 FlowRunner——Pump 每 250ms 推进一帧，日志与快照共享同一计数器。
type FrameCounter struct {
	v atomic.Int64
}

// Next 推进一帧并返回新值。
func (f *FrameCounter) Next() int64 { return f.v.Add(1) }

// Current 读取当前帧号。
func (f *FrameCounter) Current() int64 { return f.v.Load() }

// Builder 组装全量快照——数据源全部为 nil 容忍（缺口按空数组输出，不静默）。
type Builder struct {
	// Logs 日志总账（nil = 空日志段）
	Logs *LogStore
	// Boot 启动降级登记（nil = 零降级）
	Boot *BootRegistry
	// Deps 依赖探测（nil = 空 deps 段）
	Deps *DepProbe
	// Frame 帧号源（nil = 0）
	Frame func() int64
	// OA 工单四态计数（nil = 全 0）
	OA func() (open, work, closed, timeout int)
	// Cats Flow 注册表构建器（nil = 空）
	Cats func() []Cat
	// Sessions 会话状态构建器（nil = 空）
	Sessions func() []any
	// Tools 工具注册表构建器（nil = 空）
	Tools func() []Tool
	// Boxes DataBox 构建器（nil = 空）
	Boxes func() []Box

	// patch 是增量 diff 缓存（BuildPatch 独占；Pump 单 goroutine 语义）
	patch patchState
}

// Build 构建快照 JSON；includeLogs=true 时携带最近 50 条日志（C# BuildSnapshotJson 同口径）。
func (b *Builder) Build(includeLogs bool) string {
	snap := Snapshot{
		Version:  SnapshotVersion,
		PID:      os.Getpid(),
		Frame:    b.currentFrame(),
		Cats:     b.buildCats(),
		Sessions: b.buildSessions(),
		OA:       b.buildOA(),
		Tools:    b.buildTools(),
		Boxes:    b.buildBoxes(),
		Deps:     b.buildDeps(),
		Res:      CaptureRes(),
		Logs:     []LogItem{},
	}
	if b.Boot != nil {
		snap.Boot = b.Boot.Segment()
	} else {
		snap.Boot = BootSegment{OK: true, Count: 0, Summary: "", Sink: "", ReportPath: "", Degraded: []Degradation{}}
	}
	if includeLogs && b.Logs != nil {
		entries := b.Logs.Snapshot(50)
		items := make([]LogItem, 0, len(entries))
		for _, entry := range entries {
			if IsTraceAudit(entry) {
				continue
			}
			items = append(items, LogItem{
				Time:     entry.Time,
				Frame:    entry.Frame,
				Level:    LevelText(entry.Level),
				Category: entry.Category,
				Module:   entry.Module,
				Message:  entry.Message,
			})
		}
		snap.Logs = items
	}
	return marshalJSON(snap)
}

// currentFrame 取帧号（nil 源回落 0）。
func (b *Builder) currentFrame() int64 {
	if b.Frame == nil {
		return 0
	}
	return b.Frame()
}

// buildCats 构建 cats 段（nil 源给空数组——JSON 序列化为 [] 而非 null）。
func (b *Builder) buildCats() []Cat {
	if b.Cats == nil {
		return []Cat{}
	}
	if cats := b.Cats(); cats != nil {
		return cats
	}
	return []Cat{}
}

// buildSessions 构建 sessions 段。
func (b *Builder) buildSessions() []any {
	if b.Sessions == nil {
		return []any{}
	}
	if sessions := b.Sessions(); sessions != nil {
		return sessions
	}
	return []any{}
}

// buildOA 构建 oa 段。
func (b *Builder) buildOA() OA {
	if b.OA == nil {
		return OA{}
	}
	open, work, closed, timeout := b.OA()
	return OA{Open: open, Work: work, Closed: closed, Timeout: timeout}
}

// buildTools 构建 tools 段。
func (b *Builder) buildTools() []Tool {
	if b.Tools == nil {
		return []Tool{}
	}
	if tools := b.Tools(); tools != nil {
		return tools
	}
	return []Tool{}
}

// buildBoxes 构建 boxes 段（内部实现盒不进观测面——对齐 C# IsInternalBox 过滤）。
func (b *Builder) buildBoxes() []Box {
	if b.Boxes == nil {
		return []Box{}
	}
	boxes := b.Boxes()
	if boxes == nil {
		return []Box{}
	}
	kept := make([]Box, 0, len(boxes))
	for _, box := range boxes {
		if isInternalBox(box.Scope, box.Key) {
			continue
		}
		kept = append(kept, box)
	}
	return kept
}

// buildDeps 构建 deps 段。
func (b *Builder) buildDeps() []DepResult {
	if b.Deps == nil {
		return []DepResult{}
	}
	if deps := b.Deps.Snapshot(); deps != nil {
		return deps
	}
	return []DepResult{}
}
