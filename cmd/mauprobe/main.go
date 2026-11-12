package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"cathome5/mau/interp"
	"cathome5/mau/runtime"
)

type readState string

const (
	stInit    readState = "Init"
	stIdle    readState = "Idle"
	stClaimed readState = "Claimed"
	stReady   readState = "Ready"
	stDone    readState = "Done"
	stFailed  readState = "Failed"
)

// textCat 是 C# FL_TextCat 读链的 Go 等价形态（P1a 合成语料）。
// 只复刻机制核所需的最小导线集：Init / Claim / Args / Exec / Reply + 主动传感器。
type textCat struct {
	db     *runtime.DataBox
	oa     *runtime.OA
	trace  *runtime.Trace
	flowID int64

	read        readState
	claimOffice int64
	readOpenAt  int64
	curFrame    int64

	wires  []*interp.Wire
	states []*interp.WireState
}

func newTextCat(db *runtime.DataBox, oa *runtime.OA, trace *runtime.Trace) *textCat {
	f := &textCat{
		db:    db,
		oa:    oa,
		trace: trace,
		read:  stInit,
	}
	scope := func() string { return strconv.FormatInt(f.flowID, 10) }

	f.wires = []*interp.Wire{
		{
			Name:      "T_Init",
			Condition: func() bool { return f.read == stInit },
			Execute: func(frame int64) interp.Capture {
				f.db.Set(scope(), "self_desc", []string{"空闲待命"})
				return interp.Capture{Ok: true}
			},
			OnFire: func(frame int64) {
				f.trace.Record("Flow", "trace.fire", frame,
					runtime.Prop{Key: "wire", Value: "T_Init"},
					runtime.Prop{Key: "frame", Value: strconv.FormatInt(frame, 10)})
			},
			OnResult: func(c interp.Capture) {
				f.read = stIdle
				f.state("T_Init", "S_Read=Idle")
			},
		},
		{
			Name: "T_ReadClaim",
			Condition: func() bool {
				return f.db.GetBool(scope(), "rdOpen") && f.read == stIdle
			},
			Execute: func(frame int64) interp.Capture {
				open := f.oa.ListOpen("TOOL", []string{"text-read"})
				ids := make([]int64, 0, len(open))
				for _, off := range open {
					ids = append(ids, off.ID)
				}
				claimed := f.oa.ClaimBatch(f.flowID, ids)
				if len(claimed) > 0 {
					f.claimOffice = claimed[0]
					return interp.Capture{Ok: true}
				}
				return interp.Capture{Ok: false}
			},
			OnFire: func(frame int64) {
				f.trace.Record("Flow", "trace.fire", frame,
					runtime.Prop{Key: "wire", Value: "T_ReadClaim"},
					runtime.Prop{Key: "frame", Value: strconv.FormatInt(frame, 10)})
			},
			OnResult: func(c interp.Capture) {
				if c.Ok {
					f.read = stClaimed
					f.state("T_ReadClaim", "S_Read=Claimed")
				} else {
					f.read = stIdle
				}
			},
		},
		{
			Name:      "T_ReadArgs",
			Condition: func() bool { return f.read == stClaimed },
			Execute: func(frame int64) interp.Capture {
				args, _ := f.oa.GetStr(f.claimOffice, "args")
				f.db.Set(scope(), "rdArgs", args)
				return interp.Capture{Ok: true, Value: args}
			},
			OnFire: func(frame int64) {
				f.trace.Record("Flow", "trace.fire", frame,
					runtime.Prop{Key: "wire", Value: "T_ReadArgs"},
					runtime.Prop{Key: "frame", Value: strconv.FormatInt(frame, 10)})
			},
			OnResult: func(c interp.Capture) {
				f.read = stReady
				f.state("T_ReadArgs", "S_Read=Ready")
			},
		},
		{
			Name:      "T_ReadExec",
			Async:     true,
			Condition: func() bool { return f.read == stReady },
			Execute: func(frame int64) interp.Capture {
				path := extractPath(f.db.GetString(scope(), "rdArgs"))
				if path == "" {
					return interp.Capture{Ok: false, Value: "ERR|BAD_ARGS|缺少 path"}
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return interp.Capture{Ok: false, Value: "ERR|" + err.Error()}
				}
				return interp.Capture{Ok: true, Value: string(data)}
			},
			OnFire: func(frame int64) {
				f.trace.Record("Flow", "trace.fire", frame,
					runtime.Prop{Key: "wire", Value: "T_ReadExec"},
					runtime.Prop{Key: "frame", Value: strconv.FormatInt(frame, 10)})
			},
			OnResult: func(c interp.Capture) {
				f.db.Set(scope(), "rdRes", c.Value)
				if c.Ok {
					f.read = stDone
					f.state("T_ReadExec", "S_Read=Done")
				} else {
					f.read = stFailed
					f.state("T_ReadExec", "S_Read=Failed")
				}
			},
			OnTimeout: func() {
				f.read = stFailed
				f.state("T_ReadExec", "S_Read=Failed")
			},
		},
		{
			Name:      "T_ReadReply",
			Condition: func() bool { return f.read == stDone },
			Execute: func(frame int64) interp.Capture {
				ok := f.oa.Complete(f.claimOffice, f.flowID, map[string]string{
					"result": f.db.GetString(scope(), "rdRes"),
				})
				return interp.Capture{Ok: ok}
			},
			OnFire: func(frame int64) {
				f.trace.Record("Flow", "trace.fire", frame,
					runtime.Prop{Key: "wire", Value: "T_ReadReply"},
					runtime.Prop{Key: "frame", Value: strconv.FormatInt(frame, 10)})
			},
			OnResult: func(c interp.Capture) {
				f.read = stIdle
				f.state("T_ReadReply", "S_Read=Idle")
			},
		},
	}

	f.states = make([]*interp.WireState, len(f.wires))
	for i := range f.states {
		f.states[i] = interp.NewWireState(1200)
	}
	return f
}

func (f *textCat) state(wire, to string) {
	f.trace.Record("Flow", "trace.state", f.curFrame,
		runtime.Prop{Key: "wire", Value: wire},
		runtime.Prop{Key: "to", Value: to},
		runtime.Prop{Key: "frame", Value: strconv.FormatInt(f.curFrame, 10)})
}

// Tick 每帧驱动全部导线（帧内顺序即表序）。
func (f *textCat) Tick(frame int64) {
	f.curFrame = frame
	for i, w := range f.wires {
		f.states[i].Run(w, frame)
	}
}

// TickSensors 主动传感器壳——每帧探测开放工单。
func (f *textCat) TickSensors(frame int64) {
	if frame-f.readOpenAt < 1 {
		return
	}
	f.readOpenAt = frame
	ok := len(f.oa.ListOpen("TOOL", []string{"text-read"})) > 0
	f.db.Set(strconv.FormatInt(f.flowID, 10), "rdOpen", ok)
}

// Status 返回四柱快照最小形态。
func (f *textCat) Status() runtime.FlowStatus {
	wires := make([]runtime.WireStatus, 0, len(f.wires))
	for i, w := range f.wires {
		wires = append(wires, f.states[i].Status(w.Name))
	}
	return runtime.FlowStatus{
		Frame:      f.curFrame,
		StateLines: []string{"S_Read=" + string(f.read)},
		Wires:      wires,
	}
}

func extractPath(args string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return ""
	}
	s, _ := m["path"].(string)
	return s
}

func main() {
	frames := 20
	if len(os.Args) > 1 {
		if n, err := strconv.Atoi(os.Args[1]); err == nil {
			frames = n
		}
	}

	trace := runtime.NewTrace()
	db := runtime.NewDataBox()
	oa := runtime.NewOA(trace)
	runner := runtime.NewRunner(db, oa, trace)
	oa.FrameProvider = runner.Frame

	flow := newTextCat(db, oa, trace)
	flow.flowID = runner.RegisterFlow(flow, "TextCat")
	fmt.Printf("flowId=%d\n", flow.flowID)

	ownerID := int64(9001)
	officeID := oa.Post(ownerID, "TOOL", "text-read", 1200)
	oa.SetStr(officeID, ownerID, "args", `{"path":"D:/Mau/WorkSpace/CatHome5/tools/mau-probe/Program.cs"}`)
	fmt.Printf("officeId=%d\n", officeID)

	for i := 0; i < frames; i++ {
		runner.Tick()
		time.Sleep(100 * time.Millisecond)
	}

	fire := 0
	transition := 0
	for _, ev := range trace.Snapshot() {
		if strings.HasPrefix(ev.Category, "trace.fire") {
			fire++
		} else if strings.HasPrefix(ev.Category, "trace.state") {
			transition++
		}
		if strings.HasPrefix(ev.Category, "trace.") || strings.HasPrefix(ev.Category, "oa.") {
			fmt.Printf("%s|frame=%d|%s\n", ev.Category, ev.Frame, propsText(ev))
		}
	}
	fmt.Printf("trace.fire=%d trace.state=%d\n", fire, transition)

	status := flow.Status()
	fmt.Printf("status.frame=%d\n", status.Frame)
	for _, line := range status.StateLines {
		fmt.Printf("state|%s\n", line)
	}
	for _, w := range status.Wires {
		if w.LastTrigger != 0 {
			fmt.Printf("wire|%s|last=%d|busy=%t\n", w.Name, w.LastTrigger, w.Busy)
		}
	}
}

func propsText(ev runtime.Event) string {
	var b strings.Builder
	for i, p := range ev.Props {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(p.Key)
		b.WriteByte('=')
		b.WriteString(p.Value)
	}
	return b.String()
}
