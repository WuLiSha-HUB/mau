package runtime

import "testing"

// fakeFlow 是注册表测试替身——Tick 记帧，Status 回固定四柱。
type fakeFlow struct {
	frames int64
}

// Tick 记录帧号。
func (f *fakeFlow) Tick(frame int64) { f.frames = frame }

// Status 返回四柱快照。
func (f *fakeFlow) Status() FlowStatus {
	return FlowStatus{
		Frame:      f.frames,
		StateLines: []string{"S_X=Idle"},
		Wires:      []WireStatus{{Name: "T_Init", Busy: false, LastTrigger: 1}},
	}
}

// TestEntriesProjection 校验注册表投影——ID 序 / 名字 / 类型名 / 类别 / 状态副本。
func TestEntriesProjection(t *testing.T) {
	runner := NewRunner(NewDataBox(), nil, nil)
	first := &fakeFlow{}
	second := &fakeFlow{}
	if id := runner.RegisterFlow(first, "TextCat"); id != 1 {
		t.Fatalf("首个注册 ID = %d，期望 1", id)
	}
	if id := runner.RegisterFlow(second, "FileCat"); id != 2 {
		t.Fatalf("第二个注册 ID = %d，期望 2", id)
	}
	if entries := runner.Entries(); len(entries) != 2 || entries[0].Status.Frame != 0 {
		t.Fatalf("首帧前投影不符：%+v", entries)
	}
	runner.Tick()
	runner.Tick()

	entries := runner.Entries()
	if len(entries) != 2 {
		t.Fatalf("注册表条目 = %d，期望 2", len(entries))
	}
	if entries[0].Name != "TextCat" || entries[1].Name != "FileCat" {
		t.Fatalf("注册顺序不符：%q / %q", entries[0].Name, entries[1].Name)
	}
	if entries[0].ID != 1 || entries[1].ID != 2 {
		t.Fatalf("ID 不符：%d / %d", entries[0].ID, entries[1].ID)
	}
	if entries[0].TypeName != "fakeFlow" || entries[0].Kind != "flow" {
		t.Fatalf("类型名/类别不符：%q / %q", entries[0].TypeName, entries[0].Kind)
	}
	if entries[0].Status.Frame != 2 {
		t.Fatalf("状态副本帧号 = %d，期望 2", entries[0].Status.Frame)
	}
	if len(entries[0].Status.Wires) != 1 || entries[0].Status.Wires[0].Name != "T_Init" {
		t.Fatalf("状态副本导线不符：%+v", entries[0].Status.Wires)
	}
}
