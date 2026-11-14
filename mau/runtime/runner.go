package runtime

import (
	"reflect"
	"strconv"
	"sync"
)

// Flow 是生成物的驱动契约。
type Flow interface {
	// Tick 每帧驱动——帧号由 Runner 注入。
	Tick(frame int64)
	// Status 返回四柱快照（状态机 / 导线）。
	Status() FlowStatus
}

// SensorLoop 是主动传感器壳——独立起点，帧驱动。
type SensorLoop interface {
	TickSensors(frame int64)
}

// WireStatus 是导线运行态。
type WireStatus struct {
	Name        string
	Busy        bool
	LastTrigger int64
	TimedOut    bool
}

// FlowStatus 是四柱快照的最小形态。
type FlowStatus struct {
	Frame      int64
	StateLines []string
	Wires      []WireStatus
}

// Runner 是帧序宿主——冻结、排空、分发、驱动、传感器、OA 结算。
type Runner struct {
	mu      sync.RWMutex
	frame   int64
	flows   []flowEntry
	status  []FlowStatus
	sensors []sensorEntry
	inbox   *Inbox[func()]
	oa      *OA
	trace   *Trace
	db      *DataBox
	nextID  int64
}

type flowEntry struct {
	ID       int64
	Flow     Flow
	Name     string
	TypeName string
	Kind     string
}

// FlowEntryInfo 是一条已注册实体的对外投影（注册表快照——只读值）。
type FlowEntryInfo struct {
	// ID 注册 ID（注册顺序）
	ID int64
	// Name 注册名
	Name string
	// TypeName 实例类型名（注册时消解——对齐 C# FlowEntry.TypeName）
	TypeName string
	// Kind 类别（Go 侧注册项恒 "flow"）
	Kind string
	// Status 最近一帧的四柱快照（驱动 goroutine 收集的值副本）
	Status FlowStatus
}

// flowTypeName 取实体实例的裸类型名（对齐 C# GetType().Name；指针剥一层）。
func flowTypeName(flow Flow) string {
	t := reflect.TypeOf(flow)
	if t == nil {
		return ""
	}
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t.Name()
}

type sensorEntry struct {
	FlowID int64
	Loop   SensorLoop
}

// NewRunner 创建帧序宿主。
func NewRunner(db *DataBox, oa *OA, trace *Trace) *Runner {
	return &Runner{
		inbox:  &Inbox[func()]{},
		oa:     oa,
		trace:  trace,
		db:     db,
		nextID: 1,
	}
}

// Frame 返回当前帧号。
func (r *Runner) Frame() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.frame
}

// RegisterFlow 注册实体——分配 ID；实现 SensorLoop 的自动入传感器列表。
func (r *Runner) RegisterFlow(flow Flow, name string) int64 {
	r.mu.Lock()
	id := r.nextID
	r.nextID++
	r.flows = append(r.flows, flowEntry{ID: id, Flow: flow, Name: name, TypeName: flowTypeName(flow), Kind: "flow"})
	r.status = append(r.status, FlowStatus{})
	if loop, ok := flow.(SensorLoop); ok {
		r.sensors = append(r.sensors, sensorEntry{FlowID: id, Loop: loop})
	}
	r.mu.Unlock()
	if r.trace != nil {
		r.trace.Record("FlowRunner", "flow.register", -1,
			Prop{"flowId", itoa(id)}, Prop{"name", name}, Prop{"kind", "flow"})
	}
	return id
}

// FlowID 按注册名查 ID（未注册回 0, false）。
func (r *Runner) FlowID(name string) (int64, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := range r.flows {
		if r.flows[i].Name == name {
			return r.flows[i].ID, true
		}
	}
	return 0, false
}

// FlowByName 按注册名取注册表投影（含最近一帧状态副本）。
func (r *Runner) FlowByName(name string) (FlowEntryInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := range r.flows {
		if r.flows[i].Name != name {
			continue
		}
		entry := r.flows[i]
		info := FlowEntryInfo{ID: entry.ID, Name: entry.Name, TypeName: entry.TypeName, Kind: entry.Kind}
		if i < len(r.status) {
			info.Status = r.status[i]
		}
		return info, true
	}
	return FlowEntryInfo{}, false
}

// FlowObject 按注册名取实体接口（热重载侧做类型断言用——如 *graph.Graph 的状态快照）。
func (r *Runner) FlowObject(name string) (Flow, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := range r.flows {
		if r.flows[i].Name == name {
			return r.flows[i].Flow, true
		}
	}
	return nil, false
}

// Names 返回注册名列表（注册序即 ID 升序）。
func (r *Runner) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.flows))
	for i := range r.flows {
		out[i] = r.flows[i].Name
	}
	return out
}

// ReplaceFlow 原位替换一个已注册实体——热重载的换句柄面。
//
// 三条语义（缺一条就会静默错位）：
//   - ID 复用：不推进 nextID，OA 归属不断裂（工单 owner 即 Flow ID）；
//   - 状态清零：status 同下标重置（下一帧由 Tick 按新图重新收集）；
//   - 传感器重建：sensors 是独立列表，按新图重新安装（旧表项必须摘）。
//
// 调用方须与 Tick 串行——主循环内直调，或经 PostToMain 投递（并发替换会与段 3 的状态收集竞争）。
func (r *Runner) ReplaceFlow(name string, flow Flow) (int64, bool) {
	if flow == nil {
		return 0, false
	}
	r.mu.Lock()
	id := int64(0)
	found := false
	for i := range r.flows {
		if r.flows[i].Name != name {
			continue
		}
		id = r.flows[i].ID
		r.flows[i].Flow = flow
		r.flows[i].TypeName = flowTypeName(flow)
		if i < len(r.status) {
			r.status[i] = FlowStatus{}
		}
		found = true
		break
	}
	if found {
		r.rebuildSensorsLocked()
	}
	r.mu.Unlock()
	if found && r.trace != nil {
		r.trace.Record("FlowRunner", "flow.replace", -1,
			Prop{"flowId", itoa(id)}, Prop{"name", name})
	}
	return id, found
}

// UnregisterFlow 摘除一个已注册实体——flows / status / sensors 三处同步摘除。
//
// 保序摘除（不交换位）——注册序是观测面与 ID 序的锚，交换位会让整列错位。
func (r *Runner) UnregisterFlow(name string) (int64, bool) {
	r.mu.Lock()
	idx := -1
	for i := range r.flows {
		if r.flows[i].Name == name {
			idx = i
			break
		}
	}
	id := int64(0)
	if idx >= 0 {
		id = r.flows[idx].ID
		r.flows = append(r.flows[:idx], r.flows[idx+1:]...)
		if idx < len(r.status) {
			r.status = append(r.status[:idx], r.status[idx+1:]...)
		}
		r.rebuildSensorsLocked()
	}
	r.mu.Unlock()
	if idx >= 0 && r.trace != nil {
		r.trace.Record("FlowRunner", "flow.unregister", -1,
			Prop{"flowId", itoa(id)}, Prop{"name", name})
	}
	return id, idx >= 0
}

// rebuildSensorsLocked 按当前 flows 重建传感器列表（调用方须持写锁）。
func (r *Runner) rebuildSensorsLocked() {
	sensors := make([]sensorEntry, 0, len(r.flows))
	for i := range r.flows {
		if loop, ok := r.flows[i].Flow.(SensorLoop); ok {
			sensors = append(sensors, sensorEntry{FlowID: r.flows[i].ID, Loop: loop})
		}
	}
	r.sensors = sensors
}

// CurrentFlowID 是驱动中的 Flow ID（对应 C# FlowContext）。
var CurrentFlowID int64

// Tick 每帧驱动——六段帧序。
func (r *Runner) Tick() {
	r.mu.Lock()
	r.frame++
	frame := r.frame
	flows := make([]flowEntry, len(r.flows))
	copy(flows, r.flows)
	sensors := make([]sensorEntry, len(r.sensors))
	copy(sensors, r.sensors)
	r.mu.Unlock()

	r.db.Set("global", "frame", frame)

	// 段1：Inbox 排空——单回调异常不中断帧。
	r.inbox.Drain(func(action func()) {
		defer func() {
			if rec := recover(); rec != nil {
				if r.trace != nil {
					r.trace.Record("FlowRunner", "log.error", frame, Prop{"message", "inbox panic"})
				}
			}
		}()
		action()
	})

	// 段2：指令分发（P1a 无指令总线——空实现）。

	// 段3：实体帧驱动（驱动后立即收集状态副本——观测面只读值副本，不跨 goroutine 读图内状态）。
	statuses := make([]FlowStatus, len(flows))
	for i, entry := range flows {
		CurrentFlowID = entry.ID
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					if r.trace != nil {
						r.trace.Record("FlowRunner", "log.error", frame,
							Prop{"message", "flow panic"}, Prop{"flowId", itoa(entry.ID)})
					}
				}
				CurrentFlowID = 0
			}()
			entry.Flow.Tick(frame)
		}()
		statuses[i] = safeStatus(entry.Flow)
	}
	r.mu.Lock()
	r.status = statuses
	r.mu.Unlock()

	// 段3b：传感器壳。
	for _, entry := range sensors {
		CurrentFlowID = entry.FlowID
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					if r.trace != nil {
						r.trace.Record("FlowRunner", "log.error", frame,
							Prop{"message", "sensor panic"}, Prop{"flowId", itoa(entry.FlowID)})
					}
				}
				CurrentFlowID = 0
			}()
			entry.Loop.TickSensors(frame)
		}()
	}

	// 段4：OA 超时结算。
	if r.oa != nil {
		r.oa.Tick()
	}
}

// safeStatus 取实体四柱状态——单实体异常不中断帧（零值回落）。
func safeStatus(flow Flow) (status FlowStatus) {
	defer func() {
		if recover() != nil {
			status = FlowStatus{}
		}
	}()
	return flow.Status()
}

// Entries 返回注册表快照（按注册顺序即 ID 升序）。
//
// Status 是最近一帧的状态副本——状态只允许驱动 goroutine 写，观测面读值副本。
func (r *Runner) Entries() []FlowEntryInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]FlowEntryInfo, len(r.flows))
	for i := range r.flows {
		entry := r.flows[i]
		info := FlowEntryInfo{ID: entry.ID, Name: entry.Name, TypeName: entry.TypeName, Kind: entry.Kind}
		if i < len(r.status) {
			info.Status = r.status[i]
		}
		out[i] = info
	}
	return out
}

// PostToMain 后台→主线程投递——回调在下一帧执行。
func (r *Runner) PostToMain(action func()) {
	r.inbox.Enqueue(action)
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}
