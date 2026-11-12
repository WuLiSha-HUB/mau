package runtime

import "sync"

// Prop 是审计属性（有序键值对）。
type Prop struct {
	Key   string
	Value string
}

// Event 是状态迁移与导线触发的原子记录。
type Event struct {
	Seq      int64
	Frame    int64
	Source   string
	Category string
	Props    []Prop
}

// Trace 是内存审计流——对应 C# 的 AuditStore（trace.* 不落盘）。
type Trace struct {
	mu     sync.Mutex
	seq    int64
	events []Event
}

// NewTrace 创建空审计流。
func NewTrace() *Trace {
	return &Trace{events: make([]Event, 0, 256)}
}

// Record 记录一条事件。
func (t *Trace) Record(source, category string, frame int64, props ...Prop) {
	t.mu.Lock()
	t.seq++
	t.events = append(t.events, Event{
		Seq:      t.seq,
		Frame:    frame,
		Source:   source,
		Category: category,
		Props:    props,
	})
	t.mu.Unlock()
}

// Snapshot 返回事件副本（时间序）。
func (t *Trace) Snapshot() []Event {
	t.mu.Lock()
	out := make([]Event, len(t.events))
	copy(out, t.events)
	t.mu.Unlock()
	return out
}

// Find 按类别前缀过滤（观测与对拍用）。
func (t *Trace) Find(prefix string) []Event {
	all := t.Snapshot()
	out := make([]Event, 0, len(all))
	for _, ev := range all {
		if len(ev.Category) >= len(prefix) && ev.Category[:len(prefix)] == prefix {
			out = append(out, ev)
		}
	}
	return out
}

// PropOf 取属性值（缺失返回空串）。
func PropOf(ev Event, key string) string {
	for _, p := range ev.Props {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}
