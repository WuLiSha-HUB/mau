package runtime

import "testing"

// TestOAOpenKeepsOneClaimOpportunity 锁住「Open 态至少保留一帧认领机会」。
func TestOAOpenKeepsOneClaimOpportunity(t *testing.T) {
	oa := NewOA(nil)
	id := oa.Post(1, "TOOL", "x", 3)
	for i := 0; i < 3; i++ {
		oa.Tick()
	}
	if st, ok := oa.StatusOf(id); !ok || st != OfficeOpen {
		t.Fatalf("Open 应保留三帧认领机会，实际 %v（ok=%v）", st, ok)
	}
	oa.Tick()
	if st, _ := oa.StatusOf(id); st != OfficeTimeout {
		t.Fatalf("第 4 帧应超时，实际 %v", st)
	}
}

// TestOAWorkTimeoutIsInclusive 锁住「Work 达时限即超时」。
func TestOAWorkTimeoutIsInclusive(t *testing.T) {
	oa := NewOA(nil)
	id := oa.Post(1, "TOOL", "x", 3)
	oa.Tick()
	oa.Tick()
	if got := oa.ClaimBatch(7, []int64{id}); len(got) != 1 || got[0] != id {
		t.Fatalf("认领失败: %v", got)
	}
	// ClaimFrame=2；tick 5 时 elapsed=3 >= 3 → 超时。
	oa.Tick()
	oa.Tick()
	if st, _ := oa.StatusOf(id); st != OfficeWork {
		t.Fatalf("第 4 帧不应超时，实际 %v", st)
	}
	oa.Tick()
	if st, _ := oa.StatusOf(id); st != OfficeTimeout {
		t.Fatalf("第 5 帧应超时，实际 %v", st)
	}
}

// TestOARelistReturnsToOpen 锁住「Relist 回 Open，不设 Failed」。
func TestOARelistReturnsToOpen(t *testing.T) {
	oa := NewOA(nil)
	id := oa.Post(1, "TOOL", "x", 10)
	oa.Tick()
	oa.ClaimBatch(7, []int64{id})
	if !oa.Relist(id, 7) {
		t.Fatal("Relist 应成功")
	}
	if st, _ := oa.StatusOf(id); st != OfficeOpen {
		t.Fatalf("Relist 后应为 Open，实际 %v", st)
	}
}

// TestCubeTotalExpiry 锁住 Cube 的帧步进边界。
func TestCubeTotalExpiry(t *testing.T) {
	c := NewCube(2, CubeTotal)
	c.Start()
	c.TickFrame()
	c.TickFrame()
	if c.IsExpired() {
		t.Fatal("第 2 帧不应过期")
	}
	c.TickFrame()
	if !c.IsExpired() {
		t.Fatal("第 3 帧应过期")
	}
}

// TestInboxDrainIsolatesItems 锁住 Inbox 排空语义。
func TestInboxDrainIsolatesItems(t *testing.T) {
	box := &Inbox[int]{}
	box.Enqueue(1)
	box.Enqueue(2)
	got := make([]int, 0, 2)
	box.Drain(func(v int) { got = append(got, v) })
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("排空结果不符: %v", got)
	}
	if box.Len() != 0 {
		t.Fatalf("排空后应为空，实际 %d", box.Len())
	}
}

// TestTraceKeepsOrder 锁住事件时间序。
func TestTraceKeepsOrder(t *testing.T) {
	tr := NewTrace()
	tr.Record("Flow", "trace.fire", 1, Prop{"wire", "A"})
	tr.Record("Flow", "trace.state", 1, Prop{"wire", "A"})
	evs := tr.Find("trace.")
	if len(evs) != 2 || evs[0].Seq >= evs[1].Seq {
		t.Fatalf("事件序不符: %+v", evs)
	}
}
