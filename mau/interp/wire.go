package interp

import (
	"cathome5/mau/runtime"
)

// Capture 是导线执行结果（携代际号回投）。
//
// Boxes 是本次执行要落的盒——后台执行时只收集，由主线程 OnResult 统一写回，
// 避免后台 goroutine 与主线程并发读写共享 map。
type Capture struct {
	Gen   int64
	Ok    bool
	Value string
	Boxes map[string]string
}

// Wire 是一条导线：条件 → 消费 → 执行。
type Wire struct {
	Name          string
	Condition     func() bool
	Consume       func()
	Execute       func(frame int64) Capture
	Async         bool
	TimeoutFrames int64
	OnFire        func(frame int64)
	OnResult      func(c Capture)
	OnTimeout     func()
}

// WireState 是导线的运行态——忙门、代际、时限、回投队列。
type WireState struct {
	Busy      bool
	Gen       int64
	LastFrame int64
	TimedOut  bool
	cube      *runtime.Cube
	inbox     *runtime.Inbox[Capture]
}

// NewWireState 创建导线运行态。
func NewWireState(timeoutFrames int64) *WireState {
	if timeoutFrames <= 0 {
		timeoutFrames = 1200
	}
	return &WireState{
		cube:  runtime.NewCube(timeoutFrames, runtime.CubeTotal),
		inbox: &runtime.Inbox[Capture]{},
	}
}

// Run 驱动一条导线——忙门防重入、代际防串结果、时限丢弃迟到结果。
func (s *WireState) Run(w *Wire, frame int64) {
	// 忙门：在途时只推进 Cube 与排空回投。
	if s.Busy {
		s.cube.TickFrame()
		if s.cube.IsExpired() {
			s.Busy = false
			s.TimedOut = true
			s.Gen++
			s.cube.Reset()
			if w.OnTimeout != nil {
				w.OnTimeout()
			}
		}
	}
	s.inbox.Drain(func(c Capture) {
		if c.Gen != s.Gen {
			return
		}
		s.Busy = false
		timedOut := s.cube.IsExpired()
		s.cube.Complete()
		if timedOut {
			s.TimedOut = true
			if w.OnTimeout != nil {
				w.OnTimeout()
			}
			return
		}
		if w.OnResult != nil {
			w.OnResult(c)
		}
	})

	if s.Busy {
		return
	}
	if !w.Condition() {
		return
	}
	if w.Consume != nil {
		w.Consume()
	}
	s.LastFrame = frame
	s.Gen++
	gen := s.Gen
	if w.OnFire != nil {
		w.OnFire(frame)
	}

	if w.Async {
		s.Busy = true
		s.cube.Start()
		go func() {
			cap := w.Execute(frame)
			cap.Gen = gen
			s.inbox.Enqueue(cap)
		}()
		return
	}

	s.cube.Start()
	cap := w.Execute(frame)
	s.cube.Complete()
	cap.Gen = gen
	if w.OnResult != nil {
		w.OnResult(cap)
	}
}

// Status 返回导线观测态。
func (s *WireState) Status(name string) runtime.WireStatus {
	return runtime.WireStatus{
		Name:        name,
		Busy:        s.Busy,
		LastTrigger: s.LastFrame,
		TimedOut:    s.TimedOut,
	}
}
