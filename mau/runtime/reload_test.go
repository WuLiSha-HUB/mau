package runtime

import "testing"

// fakeSensorFlow 在既有 fakeFlow 之上实现 SensorLoop（传感器列表重建测试用）。
type fakeSensorFlow struct {
	fakeFlow
}

// TickSensors 空实现。
func (f *fakeSensorFlow) TickSensors(frame int64) {}

// TestReplaceFlowKeepsID 锁住「替换复用原 ID」——OA 归属靠它不断裂。
func TestReplaceFlowKeepsID(t *testing.T) {
	runner := NewRunner(NewDataBox(), NewOA(nil), nil)
	first := runner.RegisterFlow(&fakeFlow{}, "GroupA")
	runner.RegisterFlow(&fakeFlow{}, "GroupB")

	id, ok := runner.ReplaceFlow("GroupA", &fakeFlow{})
	if !ok || id != first {
		t.Fatalf("替换应复用 ID %d，实际 id=%d ok=%v", first, id, ok)
	}
	if got, exists := runner.FlowID("GroupA"); !exists || got != first {
		t.Fatalf("FlowID 应仍为 %d，实际 %d（exists=%v）", first, got, exists)
	}
	// 注册序不变——GroupA 仍在首位
	names := runner.Names()
	if len(names) != 2 || names[0] != "GroupA" || names[1] != "GroupB" {
		t.Fatalf("注册序应保序，实际 %v", names)
	}
}

// TestReplaceFlowRebuildsSensors 锁住「传感器的独立列表按新图重建」——不重建会让旧传感器继续被驱动。
func TestReplaceFlowRebuildsSensors(t *testing.T) {
	runner := NewRunner(NewDataBox(), NewOA(nil), nil)
	runner.RegisterFlow(&fakeSensorFlow{}, "GroupA")
	runner.mu.RLock()
	before := len(runner.sensors)
	runner.mu.RUnlock()
	if before != 1 {
		t.Fatalf("注册带传感器实体后应有一条传感器记录，实际 %d", before)
	}
	// 换成不带 SensorLoop 的实现——传感器项必须摘掉
	runner.ReplaceFlow("GroupA", &fakeFlow{})
	runner.mu.RLock()
	after := len(runner.sensors)
	runner.mu.RUnlock()
	if after != 0 {
		t.Fatalf("替换为无传感器实体后应摘空，实际 %d", after)
	}
	// 换回带传感器的实现——必须重新装上
	runner.ReplaceFlow("GroupA", &fakeSensorFlow{})
	runner.mu.RLock()
	again := len(runner.sensors)
	runner.mu.RUnlock()
	if again != 1 {
		t.Fatalf("替换为带传感器实体后应重新安装，实际 %d", again)
	}
}

// TestReplaceFlowUnknown 锁住「替换不存在的组回 false」——不静默新增。
func TestReplaceFlowUnknown(t *testing.T) {
	runner := NewRunner(NewDataBox(), NewOA(nil), nil)
	if _, ok := runner.ReplaceFlow("Missing", &fakeFlow{}); ok {
		t.Fatal("替换不存在的组应回 false")
	}
	if len(runner.Names()) != 0 {
		t.Fatalf("不应注册任何组，实际 %v", runner.Names())
	}
}

// TestUnregisterFlowKeepsOrder 锁住「三处同步摘除且保序」——交换位会让观测面整列错位。
func TestUnregisterFlowKeepsOrder(t *testing.T) {
	runner := NewRunner(NewDataBox(), NewOA(nil), nil)
	first := runner.RegisterFlow(&fakeFlow{}, "GroupA")
	second := runner.RegisterFlow(&fakeFlow{}, "GroupB")
	third := runner.RegisterFlow(&fakeFlow{}, "GroupC")

	id, ok := runner.UnregisterFlow("GroupB")
	if !ok || id != second {
		t.Fatalf("摘除应回被摘 ID %d，实际 %d（ok=%v）", second, id, ok)
	}
	names := runner.Names()
	if len(names) != 2 || names[0] != "GroupA" || names[1] != "GroupC" {
		t.Fatalf("摘除后应保序剩 A/C，实际 %v", names)
	}
	// ID 不复用：新注册拿到新的自增 ID
	fresh := runner.RegisterFlow(&fakeFlow{}, "GroupD")
	if fresh != third+1 {
		t.Fatalf("新注册应取 nextID（%d），实际 %d", third+1, fresh)
	}
	if first == second || second == third {
		t.Fatalf("注册 ID 应唯一：%d / %d / %d", first, second, third)
	}
}

// TestFlowByNameProjectsStatus 锁住「按名投影读最近一帧状态副本」。
func TestFlowByNameProjectsStatus(t *testing.T) {
	runner := NewRunner(NewDataBox(), NewOA(nil), nil)
	runner.RegisterFlow(&fakeFlow{}, "GroupA")
	info, ok := runner.FlowByName("GroupA")
	if !ok || info.Name != "GroupA" || info.Kind != "flow" {
		t.Fatalf("投影不符：%+v ok=%v", info, ok)
	}
	if _, ok := runner.FlowByName("Missing"); ok {
		t.Fatal("不存在的组应回 false")
	}
}
