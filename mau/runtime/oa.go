package runtime

import "sync"

// OfficeState 是工单四态——没有 Failed。
type OfficeState int

const (
	// OfficeOpen 待认领。
	OfficeOpen OfficeState = iota
	// OfficeWork 已认领。
	OfficeWork
	// OfficeClosed 已完成。
	OfficeClosed
	// OfficeTimeout 已超时。
	OfficeTimeout
)

// Office 是一张工单（11 字段的功能等价形态）。
type Office struct {
	ID            int64
	Type          string
	Name          string
	Owner         int64
	Status        OfficeState
	Worker        int64
	PostFrame     int64
	ClaimFrame    int64
	TimeoutFrames int64
	Data          map[string]string
	Result        map[string]string
}

// OA 是主线程工单撮合器——四态、九法、超时不对称。
type OA struct {
	mu           sync.Mutex
	offices      map[int64]*Office
	nextID       int64
	tick         int64
	version      int64
	totalDone    int64
	totalTimeout int64
	totalRelist  int64
	trace        *Trace
	// FrameProvider 取宿主当前帧（对应 C# Audit.Record 的 frame=-1 语义）。
	FrameProvider func() int64
	// Log 是 OA 专属日志出口（level 0=INFO / 2=WARN；category=OA）——组合根接日志面。
	Log func(level int, message string)
}

// NewOA 创建空 OA。
func NewOA(trace *Trace) *OA {
	return &OA{
		offices: make(map[int64]*Office),
		nextID:  1,
		trace:   trace,
	}
}

// Post 上架工单，返回 OfficeId。
func (o *OA) Post(owner int64, officeType, name string, timeoutFrames int64) int64 {
	o.mu.Lock()
	id := o.nextID
	o.nextID++
	o.offices[id] = &Office{
		ID:            id,
		Type:          officeType,
		Name:          name,
		Owner:         owner,
		Status:        OfficeOpen,
		PostFrame:     o.tick,
		TimeoutFrames: timeoutFrames,
		Data:          make(map[string]string),
		Result:        make(map[string]string),
	}
	o.version++
	o.mu.Unlock()
	if o.Log != nil {
		o.Log(0, "工单 #"+itoa(id)+" 已提交："+officeType+"/"+name+"（超时 "+itoa(timeoutFrames)+" 帧）")
	}
	if o.trace != nil {
		o.trace.Record("OA", "oa.post", o.traceFrame(),
			Prop{"officeId", itoa(id)}, Prop{"ownerId", itoa(owner)},
			Prop{"type", officeType}, Prop{"name", name}, Prop{"timeout", itoa(timeoutFrames)})
	}
	return id
}

// traceFrame 取宿主当前帧（未注入 provider 时回落 OA 自身帧号）。
func (o *OA) traceFrame() int64 {
	if o.FrameProvider != nil {
		return o.FrameProvider()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.tick
}

// SetStr 写入请求载荷——仅 Open + 本人。
func (o *OA) SetStr(officeID, owner int64, key, value string) bool {
	o.mu.Lock()
	off, ok := o.offices[officeID]
	valid := ok && off.Status == OfficeOpen && off.Owner == owner
	if valid {
		off.Data[key] = value
		o.version++
	}
	o.mu.Unlock()
	if valid && o.Log != nil {
		o.Log(0, "工单 #"+itoa(officeID)+" 载荷 "+key+"："+value)
	}
	return valid
}

// GetStr 读取请求载荷——任何状态可读。
func (o *OA) GetStr(officeID int64, key string) (string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	off, ok := o.offices[officeID]
	if !ok {
		return "", false
	}
	v, ok := off.Data[key]
	return v, ok
}

// ListOpen 列出某大类下名称匹配的开放工单（按 ID 升序）。
func (o *OA) ListOpen(officeType string, names []string) []*Office {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]*Office, 0, len(o.offices))
	for _, off := range o.offices {
		if off.Status != OfficeOpen || off.Type != officeType {
			continue
		}
		if len(names) > 0 {
			hit := false
			for _, n := range names {
				if off.Name == n {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}
		out = append(out, cloneOffice(off))
	}
	sortOffices(out)
	return out
}

// ClaimBatch 按传入顺序认领——先到先得，不回滚已成功项。
func (o *OA) ClaimBatch(worker int64, ids []int64) []int64 {
	claimed := make([]int64, 0, len(ids))
	o.mu.Lock()
	for _, id := range ids {
		off, ok := o.offices[id]
		if !ok || off.Status != OfficeOpen {
			continue
		}
		off.Status = OfficeWork
		off.Worker = worker
		off.ClaimFrame = o.tick
		o.version++
		claimed = append(claimed, id)
	}
	o.mu.Unlock()
	if o.Log != nil {
		for _, id := range claimed {
			o.Log(0, "工单 #"+itoa(id)+" 已被 #"+itoa(worker)+" 认领")
		}
	}
	if o.trace != nil {
		for _, id := range claimed {
			o.trace.Record("OA", "oa.claim", o.traceFrame(),
				Prop{"officeId", itoa(id)}, Prop{"workerId", itoa(worker)}, Prop{"result", "claimed"})
		}
	}
	return claimed
}

// Complete 完成工单——仅 Work + 持单人。
func (o *OA) Complete(officeID, worker int64, result map[string]string) bool {
	o.mu.Lock()
	off, ok := o.offices[officeID]
	valid := ok && off.Status == OfficeWork && off.Worker == worker
	missing := !ok
	if valid {
		off.Status = OfficeClosed
		off.Result = result
		o.totalDone++
		o.version++
	}
	o.mu.Unlock()
	if o.Log != nil {
		switch {
		case missing:
			o.Log(2, "完成工单被拒：工单 #"+itoa(officeID)+" 不存在")
		case !valid:
			o.Log(2, "完成工单被拒：工单 #"+itoa(officeID)+" 状态或权限不符")
		default:
			o.Log(0, "工单 #"+itoa(officeID)+" 已完成（执行方 #"+itoa(worker)+"）")
		}
	}
	if valid && o.trace != nil {
		o.trace.Record("OA", "oa.complete", o.traceFrame(),
			Prop{"officeId", itoa(officeID)}, Prop{"workerId", itoa(worker)},
			Prop{"result", resultSummary(result)})
	}
	return valid
}

// Relist 重新开放——仅 Work + 持单人。
func (o *OA) Relist(officeID, worker int64) bool {
	o.mu.Lock()
	off, ok := o.offices[officeID]
	valid := ok && off.Status == OfficeWork && off.Worker == worker
	if valid {
		off.Status = OfficeOpen
		off.Worker = 0
		off.ClaimFrame = 0
		o.totalRelist++
		o.version++
	}
	o.mu.Unlock()
	return valid
}

// Tick 推进帧并结算超时——Open 用 >，Work 用 >=（Open 保留一帧认领机会）。
func (o *OA) Tick() {
	o.mu.Lock()
	o.tick++
	settled := make([]Event, 0, 4)
	for _, off := range o.offices {
		if off.Status == OfficeOpen {
			if o.tick-off.PostFrame > off.TimeoutFrames {
				off.Status = OfficeTimeout
				o.totalTimeout++
				o.version++
				settled = append(settled, Event{Frame: o.tick, Source: "OA", Category: "oa.settle",
					Props: []Prop{{Key: "officeId", Value: itoa(off.ID)}, {Key: "reason", Value: "open_timeout"}}})
			}
		} else if off.Status == OfficeWork {
			if o.tick-off.ClaimFrame >= off.TimeoutFrames {
				off.Status = OfficeTimeout
				o.totalTimeout++
				o.version++
				settled = append(settled, Event{Frame: o.tick, Source: "OA", Category: "oa.settle",
					Props: []Prop{{Key: "officeId", Value: itoa(off.ID)}, {Key: "reason", Value: "work_timeout"}}})
			}
		}
	}
	o.mu.Unlock()
	if o.Log != nil {
		for _, ev := range settled {
			id := ""
			for _, p := range ev.Props {
				if p.Key == "officeId" {
					id = p.Value
				}
			}
			o.Log(0, "工单 #"+id+" 超时结算")
		}
	}
	if o.trace != nil {
		for _, ev := range settled {
			o.trace.Record(ev.Source, ev.Category, ev.Frame, ev.Props...)
		}
	}
}

// GetOffice 取工单完整副本——不存在返回 Timeout 哨兵（对齐 C# GetOffice）。
func (o *OA) GetOffice(officeID int64) *Office {
	o.mu.Lock()
	defer o.mu.Unlock()
	if off, ok := o.offices[officeID]; ok {
		return cloneOffice(off)
	}
	return &Office{Status: OfficeTimeout, Data: map[string]string{}, Result: map[string]string{}}
}

// Cancel 取消挂单方自己尚未被认领的工单——仅 Open + 本人。
func (o *OA) Cancel(officeID, owner int64) bool {
	o.mu.Lock()
	off, ok := o.offices[officeID]
	valid := ok && off.Owner == owner && off.Status == OfficeOpen
	if valid {
		delete(o.offices, officeID)
		o.version++
	}
	o.mu.Unlock()
	if valid && o.Log != nil {
		o.Log(0, "工单 #"+itoa(officeID)+" 已取消（挂单方 #"+itoa(owner)+"）")
	}
	return valid
}

// StatusOf 查询工单状态。
func (o *OA) StatusOf(officeID int64) (OfficeState, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	off, ok := o.offices[officeID]
	if !ok {
		return OfficeOpen, false
	}
	return off.Status, true
}

// Counts 返回可观测计数（完成 / 超时 / 重挂 / 版本）。
func (o *OA) Counts() (done, timeout, relist, version int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.totalDone, o.totalTimeout, o.totalRelist, o.version
}

// InFlight 返回在途工单计数（待认领 / 执行中）——全局 Idle 闸门判据（宿主重启 T3）。
func (o *OA) InFlight() (open, work int) {
	if o == nil {
		return 0, 0
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, off := range o.offices {
		switch off.Status {
		case OfficeOpen:
			open++
		case OfficeWork:
			work++
		}
	}
	return open, work
}

func cloneOffice(off *Office) *Office {
	cp := *off
	cp.Data = make(map[string]string, len(off.Data))
	for k, v := range off.Data {
		cp.Data[k] = v
	}
	cp.Result = make(map[string]string, len(off.Result))
	for k, v := range off.Result {
		cp.Result[k] = v
	}
	return &cp
}

func sortOffices(list []*Office) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j-1].ID > list[j].ID; j-- {
			list[j-1], list[j] = list[j], list[j-1]
		}
	}
}

func resultSummary(result map[string]string) string {
	return "ints:0,strs:" + itoa(int64(len(result)))
}
