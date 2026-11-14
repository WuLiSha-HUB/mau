package toolset

import (
	"strings"
	"testing"
	"time"

	"cathome5/mau/fs"
	"cathome5/mau/runtime"
)

// goodCorpusV2 在最小语料上多一条导线——重载后导线数应 +1（同代可辨）。
const goodCorpusV2 = goodCorpus + `
§ 'T_Extra' : 'S_Read' = 'Idle' → 'self.desc'["额外线", "", ""] | 'S_Read' = 'Idle' | 'S_Read' = 'Idle'
`

// newReloadFixture 造一套热重载现场——临时语料目录 + 积木表 + Runner + Reloader（已注册初版并 Prime）。
func newReloadFixture(t *testing.T, body string) (*Reloader, *runtime.Runner, *runtime.OA, string) {
	t.Helper()
	root := t.TempDir()
	writeGroup(t, root, "GoodCat", "good_cat.mau", body)
	set := BuildBrickSet(&fs.Service{}, runtime.NewOA(nil), runtime.NewDataBox(), BrickOptions{})
	oa := runtime.NewOA(nil)
	runner := runtime.NewRunner(runtime.NewDataBox(), oa, nil)
	group, reason := LoadGroup(root, "GoodCat", "", set, nil)
	if reason != "" {
		t.Fatalf("初版语料应通过门禁，实际跳过原因: %s", reason)
	}
	runner.RegisterFlow(group.Graph, group.Name)
	reloader := NewReloader(root, "", set, runner, oa, nil)
	reloader.Prime([]Group{group})
	return reloader, runner, oa, root
}

// TestReloadFreshSwapsGraph 锁住 fresh 主链——门禁过 → 换图 → gen+1、hash 更新、导线按新语料重建。
func TestReloadFreshSwapsGraph(t *testing.T) {
	reloader, runner, _, root := newReloadFixture(t, goodCorpus)
	before, ok := runner.FlowByName("GoodCat")
	if !ok {
		t.Fatal("初版组应已注册")
	}
	// 改写语料：多一条导线
	writeGroup(t, root, "GoodCat", "good_cat.mau", goodCorpusV2)
	result := reloader.Reload("GoodCat", ModeFresh)
	if result.Error != "" {
		t.Fatalf("fresh 重载不应失败：%s", result.Error)
	}
	if result.Gen != 1 {
		t.Fatalf("首次重载 gen 应为 1，实际 %d", result.Gen)
	}
	// 下一帧后投影才有状态副本（ReplaceFlow 会重置——观测面按新图重收）
	runner.Tick()
	after, _ := runner.FlowByName("GoodCat")
	if len(after.Status.Wires) != result.Wires {
		t.Fatalf("注册表投影导线数（%d）应与新图一致（%d）", len(after.Status.Wires), result.Wires)
	}
	if result.CorpusHash == "" || reloader.Hash("GoodCat") != result.CorpusHash {
		t.Fatalf("语料哈希应随重载更新：result=%q stored=%q", result.CorpusHash, reloader.Hash("GoodCat"))
	}
	if after.ID != before.ID {
		t.Fatalf("重载应复用 ID %d，实际 %d", before.ID, after.ID)
	}
}

// TestReloadGateRejectsBadCorpus 锁住「门禁不过 = 现状未改」——坏语料不得换掉在跑的图。
func TestReloadGateRejectsBadCorpus(t *testing.T) {
	reloader, runner, _, root := newReloadFixture(t, goodCorpus)
	before, _ := runner.FlowByName("GoodCat")
	writeGroup(t, root, "GoodCat", "good_cat.mau", badCorpus)
	result := reloader.Reload("GoodCat", ModeFresh)
	if !strings.Contains(result.Error, "RELOAD_GATE") {
		t.Fatalf("坏语料应被门禁拒绝，实际 %q", result.Error)
	}
	after, ok := runner.FlowByName("GoodCat")
	if !ok || after.ID != before.ID {
		t.Fatalf("门禁失败后旧图应仍在：%+v ok=%v", after, ok)
	}
	if reloader.Gen("GoodCat") != 0 {
		t.Fatalf("门禁失败不得推进代际，实际 gen=%d", reloader.Gen("GoodCat"))
	}
}

// TestReloadCarryKeepsState 锁住 carry 语义——同名状态机取值合法则沿用；fresh 则回初始态。
func TestReloadCarryKeepsState(t *testing.T) {
	reloader, runner, _, _ := newReloadFixture(t, goodCorpus)
	// 直接改旧图状态（同包测试只经导出面：RestoreStates 会校验合法集）
	snapshot := map[string]string{"S_Read": "Done"}
	restored := restoreStates(runner, "GoodCat", snapshot)
	if restored != 1 {
		t.Fatalf("旧图应接受合法状态 Done，实际恢复 %d 条", restored)
	}
	result := reloader.Reload("GoodCat", ModeCarry)
	if result.Error != "" {
		t.Fatalf("carry 重载不应失败：%s", result.Error)
	}
	if result.Carried < 1 {
		t.Fatalf("carry 应迁移至少一条状态，实际 %d", result.Carried)
	}
	if state := readState(runner, "GoodCat", "S_Read"); state != "Done" {
		t.Fatalf("carry 后应保留 Done，实际 %q", state)
	}
	// 再跑一次 fresh——状态回初始
	if result := reloader.Reload("GoodCat", ModeFresh); result.Error != "" {
		t.Fatalf("fresh 重载不应失败：%s", result.Error)
	}
	if state := readState(runner, "GoodCat", "S_Read"); state != "Idle" {
		t.Fatalf("fresh 后应回初始态 Idle，实际 %q", state)
	}
}

// TestReloadDrainPending 锁住 drain 两阶段——有在途即登记 pending，归零后由 TickPending 收尾。
func TestReloadDrainPending(t *testing.T) {
	reloader, runner, oa, _ := newReloadFixture(t, goodCorpus)
	id, ok := runner.FlowID("GoodCat")
	if !ok {
		t.Fatal("初版组应有 ID")
	}
	// 造一张该组名下的在途工单（owner = flowID）
	oa.Post(id, "TOOL", "text-read", 100)
	if open, work := oa.InFlightByOwner(id); open+work != 1 {
		t.Fatalf("应有一张在途工单，实际 open=%d work=%d", open, work)
	}
	result := reloader.Reload("GoodCat", ModeDrain)
	if result.Error != "" {
		t.Fatalf("drain 登记不应失败：%s", result.Error)
	}
	if !result.Pending || !reloader.PendingOf("GoodCat") {
		t.Fatalf("有在途时应登记 pending：%+v", result)
	}
	if reloader.Gen("GoodCat") != 0 {
		t.Fatalf("pending 未收尾前不得推进代际，实际 %d", reloader.Gen("GoodCat"))
	}
	// 归零后一帧收尾
	oa.RemoveByOwner(id)
	reloader.TickPending()
	if reloader.PendingOf("GoodCat") {
		t.Fatal("归零后 pending 应清空")
	}
	if reloader.Gen("GoodCat") != 1 {
		t.Fatalf("pending 收尾后 gen 应为 1，实际 %d", reloader.Gen("GoodCat"))
	}
}

// TestReloadDrainTimeoutForcesSwap 锁住「超时强制换并出声」——不静默卡住。
func TestReloadDrainTimeoutForcesSwap(t *testing.T) {
	reloader, runner, oa, _ := newReloadFixture(t, goodCorpus)
	reloader.DrainTimeout = 10 * time.Millisecond
	id, _ := runner.FlowID("GoodCat")
	oa.Post(id, "TOOL", "text-read", 100)
	if result := reloader.Reload("GoodCat", ModeDrain); !result.Pending {
		t.Fatalf("应登记 pending，实际 %+v", result)
	}
	time.Sleep(20 * time.Millisecond)
	reloader.TickPending()
	if reloader.PendingOf("GoodCat") {
		t.Fatal("超时后应强制换图并清 pending")
	}
	if reloader.Gen("GoodCat") != 1 {
		t.Fatalf("超时强制换图后 gen 应为 1，实际 %d", reloader.Gen("GoodCat"))
	}
}

// TestReloadUnknownMode 锁住「未知模式直接拒绝」——不猜默认。
func TestReloadUnknownMode(t *testing.T) {
	reloader, _, _, _ := newReloadFixture(t, goodCorpus)
	result := reloader.Reload("GoodCat", "hot")
	if !strings.Contains(result.Error, "RELOAD_MODE") {
		t.Fatalf("未知模式应被拒绝，实际 %q", result.Error)
	}
}

// TestReloadInitializesNewGroup 锁住「不存在的组按新增注册」——热重载也是新组的加载入口。
func TestReloadInitializesNewGroup(t *testing.T) {
	root := t.TempDir()
	writeGroup(t, root, "GoodCat", "good_cat.mau", goodCorpus)
	set := BuildBrickSet(&fs.Service{}, runtime.NewOA(nil), runtime.NewDataBox(), BrickOptions{})
	runner := runtime.NewRunner(runtime.NewDataBox(), runtime.NewOA(nil), nil)
	reloader := NewReloader(root, "", set, runner, nil, nil)
	result := reloader.Reload("GoodCat", ModeFresh)
	if result.Error != "" {
		t.Fatalf("新组注册不应失败：%s", result.Error)
	}
	if !result.Registered {
		t.Fatalf("首次加载应标记 Registered：%+v", result)
	}
	if _, ok := runner.FlowID("GoodCat"); !ok {
		t.Fatal("新组应已注册进 Runner")
	}
}

// restoreStates 经导出面改旧图状态（测试用——同包也只能走 Graph 的导出方法）。
func restoreStates(runner *runtime.Runner, name string, snapshot map[string]string) int {
	flow, ok := runner.FlowObject(name)
	if !ok {
		return 0
	}
	typed, isGraph := flow.(stateRestorer)
	if !isGraph {
		return 0
	}
	return typed.RestoreStates(snapshot)
}

// readState 读当前图的状态机取值。
func readState(runner *runtime.Runner, name, machine string) string {
	flow, ok := runner.FlowObject(name)
	if !ok {
		return ""
	}
	typed, isGraph := flow.(stateReader)
	if !isGraph {
		return ""
	}
	return typed.State(machine)
}

// stateRestorer / stateReader 是测试侧对 *graph.Graph 的能力断言（避免测试文件直接依赖具体类型名）。
type stateRestorer interface {
	RestoreStates(map[string]string) int
}

type stateReader interface {
	State(string) string
}
