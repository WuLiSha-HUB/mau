// Package graph 把 front.Doc（四柱 AST）构建成可执行的运行时图。
//
// 分层：front（语法）← graph（图）→ interp（导线驱动）/ runtime（机制核）。
// 积木执行体不在本包——由调用方注入 BrickSet（P2 提供真实实现）。
package graph

import (
	"sort"
	"strings"

	"cathome5/mau/front"
	"cathome5/mau/interp"
	"cathome5/mau/runtime"
)

// BrickFunc 是积木执行体——参数已求值（字符串已剥引号、盒引用已解引用）。
type BrickFunc func(args []string) (value string, ok bool)

// BrickSet 是积木注册表——名字 → 执行体。
type BrickSet map[string]BrickFunc

// Runtime 是图的运行态——状态机当前态 + 盒子 + 帧号 + 迁移序列。
type Runtime struct {
	States map[string]string
	Boxes  map[string]string
	Frame  int64
	Trace  []TraceEvent
}

// TraceEvent 是一条状态迁移记录——与 C# trace.state 同口径（导线 + 目标状态）。
type TraceEvent struct {
	Frame int64
	Wire  string
	State string
	To    string
}

// State 读状态机当前态。
func (r *Runtime) State(name string) string { return r.States[name] }

// Box 读盒值。
func (r *Runtime) Box(key string) string { return r.Boxes[key] }

// Graph 是一份语料的运行时图——导线表序即文档序。
type Graph struct {
	rt      *Runtime
	bricks  BrickSet
	order   []string
	wires   map[string]*interp.Wire
	states  map[string]*interp.WireState
	sensors []*sensorRuntime
	// gen 本图代际（每次热重载替换 +1——观测面与回投判据）
	gen int64
	// stateSet 状态机名 → 合法状态集（carry 迁移的合法性判据）
	stateSet map[string][]string
}

// sensorRuntime 是主动传感器运行态。
type sensorRuntime struct {
	def      *front.Sensor
	lastTick int64
	primed   bool
}

// Build 把四柱 AST 构造成运行时图——状态机取首个状态为初始态。
func Build(doc *front.Doc, bricks BrickSet) *Graph {
	g := &Graph{
		rt:       &Runtime{States: map[string]string{}, Boxes: map[string]string{}},
		bricks:   bricks,
		wires:    map[string]*interp.Wire{},
		states:   map[string]*interp.WireState{},
		stateSet: map[string][]string{},
	}
	for i := range doc.StateMachines {
		sm := &doc.StateMachines[i]
		if len(sm.States) > 0 {
			g.rt.States[sm.Name] = sm.States[0]
			g.stateSet[sm.Name] = append([]string{}, sm.States...)
		}
	}
	for i := range doc.Sensors {
		g.sensors = append(g.sensors, &sensorRuntime{def: &doc.Sensors[i]})
	}
	for i := range doc.Wires {
		def := &doc.Wires[i]
		g.order = append(g.order, def.Name)
		g.wires[def.Name] = g.buildWire(def)
		g.states[def.Name] = interp.NewWireState(int64(def.Timeout))
	}
	return g
}

// buildWire 把一条导线定义绑定为可执行闭包。
func (g *Graph) buildWire(def *front.Wire) *interp.Wire {
	// 参数在主线程（Consume）预求值——后台 Execute 不再读共享 map
	var prepared [][]string
	return &interp.Wire{
		Name:          def.Name,
		Async:         def.Parallel,
		TimeoutFrames: int64(def.Timeout),
		Condition:     func() bool { return g.holds(def) },
		Consume: func() {
			prepared = make([][]string, len(def.Actions))
			for i := range def.Actions {
				prepared[i] = g.evalArgs(def.Actions[i].BrickArgs)
			}
		},
		Execute:   func(int64) interp.Capture { return g.execWire(def, prepared) },
		OnResult:  func(c interp.Capture) { g.applyCapture(def, c) },
		OnTimeout: func() { g.applyResult(def, false) },
	}
}

// holds 求值条件区——状态断言 / 盒断言 / 传感器名三类。
func (g *Graph) holds(def *front.Wire) bool {
	for _, c := range def.Conditions {
		switch {
		case c.IsStateAssert:
			if g.rt.States[c.StateName] != c.StateValue {
				return false
			}
		case c.IsBoxAssert:
			if !g.truthy(c.BoxName) {
				return false
			}
		default:
			if !g.sensorTruthy(c.SensorName) {
				return false
			}
		}
	}
	return true
}

// execWire 顺序执行动作区——任一动作失败即短路；捕获只收集不落盒。
func (g *Graph) execWire(def *front.Wire, prepared [][]string) interp.Capture {
	value := ""
	caps := map[string]string{}
	for i := range def.Actions {
		a := &def.Actions[i]
		fn, ok := g.bricks[a.BrickName]
		if !ok {
			return interp.Capture{Ok: false, Value: "ERR|UNKNOWN_BRICK|" + a.BrickName, Boxes: caps}
		}
		var args []string
		if i < len(prepared) {
			args = prepared[i]
		}
		out, good := fn(args)
		value = out
		// 失败侧也落盒——Abort 导线要靠它拿 ERR 文本（语料：失败侧也回执）
		if a.CaptureTarget != "" {
			caps[a.CaptureTarget] = out
		}
		if !good {
			return interp.Capture{Ok: false, Value: out, Boxes: caps}
		}
	}
	return interp.Capture{Ok: true, Value: value, Boxes: caps}
}

// applyCapture 在主线程应用执行结果——先落盒，再应用结果区。
func (g *Graph) applyCapture(def *front.Wire, c interp.Capture) {
	for k, v := range c.Boxes {
		g.rt.Boxes[k] = v
	}
	g.applyResult(def, c.Ok)
}

// applyResult 应用结果区——成功取首条，失败取次条（缺次条时回落首条）。
func (g *Graph) applyResult(def *front.Wire, ok bool) {
	if len(def.Results) == 0 {
		return
	}
	r := def.Results[0]
	if !ok && len(def.Results) > 1 {
		r = def.Results[1]
	}
	old, exists := g.rt.States[r.StateName]
	if !exists {
		return
	}
	if old == r.StateValue {
		return
	}
	g.rt.States[r.StateName] = r.StateValue
	g.rt.Trace = append(g.rt.Trace, TraceEvent{Frame: g.rt.Frame, Wire: def.Name, State: r.StateName, To: r.StateValue})
}

// evalArgs 求值参数——"..." 剥引号 / @盒 解引用 / 其它原样。
func (g *Graph) evalArgs(raw []string) []string {
	out := make([]string, len(raw))
	for i, a := range raw {
		t := strings.TrimSpace(a)
		switch {
		case len(t) >= 2 && t[0] == '"' && t[len(t)-1] == '"':
			out[i] = t[1 : len(t)-1]
		case strings.HasPrefix(t, "@"):
			out[i] = g.rt.Boxes[t]
		default:
			out[i] = t
		}
	}
	return out
}

// truthy 判盒值真——"true" / "1" 为真。
func (g *Graph) truthy(key string) bool {
	v := g.rt.Boxes[key]
	return v == "true" || v == "1"
}

// sensorTruthy 按传感器名求值——取该传感器的捕获盒。
func (g *Graph) sensorTruthy(name string) bool {
	for _, s := range g.sensors {
		if s.def.Name == name && s.def.CaptureTarget != "" {
			return g.truthy(s.def.CaptureTarget)
		}
	}
	return false
}

// Tick 每帧驱动全部导线——表序即文档序。
func (g *Graph) Tick(frame int64) {
	g.rt.Frame = frame
	for _, name := range g.order {
		g.states[name].Run(g.wires[name], frame)
	}
}

// TickSensors 驱动主动传感器——按 EveryFrames 节流；被动传感器不驱动。
func (g *Graph) TickSensors(frame int64) {
	for _, s := range g.sensors {
		if s.def.Passive {
			continue
		}
		if s.primed && s.def.EveryFrames > 0 && frame-s.lastTick < int64(s.def.EveryFrames) {
			continue
		}
		s.primed = true
		s.lastTick = frame
		g.probe(s)
	}
}

// probe 执行一次传感器探测——落盒，并按真/假分支执行动作。
func (g *Graph) probe(s *sensorRuntime) {
	fn, ok := g.bricks[s.def.BrickName]
	if !ok {
		return
	}
	out, good := fn(g.evalArgs(s.def.BrickArgs))
	if s.def.CaptureTarget != "" {
		g.rt.Boxes[s.def.CaptureTarget] = out
	}
	actions := s.def.TrueActions
	if !good {
		actions = s.def.FalseActions
	}
	for i := range actions {
		a := &actions[i]
		afn, found := g.bricks[a.BrickName]
		if !found {
			continue
		}
		_, _ = afn(g.evalArgs(a.BrickArgs))
	}
}

// Gen 返回本图代际——替换时由调用方推进（观测面直读）。
func (g *Graph) Gen() int64 { return g.gen }

// SetGen 设置本图代际。
func (g *Graph) SetGen(gen int64) { g.gen = gen }

// WireCount 返回导线条数（观测面）。
func (g *Graph) WireCount() int { return len(g.order) }

// StateCount 返回状态机个数（观测面）。
func (g *Graph) StateCount() int { return len(g.rt.States) }

// BoxCount 返回私有盒条数（观测面）。
func (g *Graph) BoxCount() int { return len(g.rt.Boxes) }

// BusyWires 返回在途导线名（后台执行未回投）——drain 迁移判据。
func (g *Graph) BusyWires() []string {
	busy := make([]string, 0, 4)
	for _, name := range g.order {
		if state, ok := g.states[name]; ok && state.Busy {
			busy = append(busy, name)
		}
	}
	return busy
}

// SnapshotStates 拷贝状态机当前态（carry 迁移的源）。
func (g *Graph) SnapshotStates() map[string]string {
	out := make(map[string]string, len(g.rt.States))
	for name, value := range g.rt.States {
		out[name] = value
	}
	return out
}

// SnapshotBoxes 拷贝私有盒（carry 迁移的源）。
func (g *Graph) SnapshotBoxes() map[string]string {
	out := make(map[string]string, len(g.rt.Boxes))
	for key, value := range g.rt.Boxes {
		out[key] = value
	}
	return out
}

// RestoreStates 按名恢复状态机当前态——只收「新图存在该状态机且取值在其合法集内」的项。
//
// 返回实际恢复条数；被拒项静默跳过（carry 是尽力而为，不是强一致——新图没有的状态无处可放）。
func (g *Graph) RestoreStates(snapshot map[string]string) int {
	restored := 0
	for name, value := range snapshot {
		if _, exists := g.rt.States[name]; !exists {
			continue
		}
		legal := false
		for _, state := range g.stateSet[name] {
			if state == value {
				legal = true
				break
			}
		}
		if !legal {
			continue
		}
		g.rt.States[name] = value
		restored++
	}
	return restored
}

// RestoreBoxes 恢复私有盒（carry）——返回实际写入条数。
func (g *Graph) RestoreBoxes(snapshot map[string]string) int {
	for key, value := range snapshot {
		g.rt.Boxes[key] = value
	}
	return len(snapshot)
}

// State 读状态机当前态。
func (g *Graph) State(name string) string { return g.rt.States[name] }

// Box 读盒值。
func (g *Graph) Box(key string) string { return g.rt.Boxes[key] }

// Status 返回四柱快照——状态行 + 导线运行态。
func (g *Graph) Status() runtime.FlowStatus {
	lines := make([]string, 0, len(g.rt.States))
	for name := range g.rt.States {
		lines = append(lines, name+"="+g.rt.States[name])
	}
	sort.Strings(lines)
	wires := make([]runtime.WireStatus, 0, len(g.order))
	for _, name := range g.order {
		wires = append(wires, g.states[name].Status(name))
	}
	return runtime.FlowStatus{Frame: g.rt.Frame, StateLines: lines, Wires: wires}
}
