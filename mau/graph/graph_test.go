package graph

import (
	"os"
	"testing"
	"time"

	"cathome5/mau/front"
	"cathome5/mau/runtime"
)

// envOr 读环境变量，缺省回落默认值。
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// loadTextCat 读真实语料并解析——四柱必须零诊断。
func loadTextCat(t *testing.T) *front.Doc {
	t.Helper()
	src, err := os.ReadFile(envOr("MAU_TEXT_CAT", `D:\Mau\Mau\corpus\ch4\TextCat\text_cat.mau`))
	if err != nil {
		t.Skipf("跳过：语料不可读（%v）", err)
	}
	toks, diags := front.Lex(string(src))
	if len(diags) != 0 {
		t.Fatalf("词法诊断: %+v", diags)
	}
	doc := front.Parse(toks)
	if !doc.Success() {
		t.Fatalf("语法诊断: %+v", doc.Diags)
	}
	return doc
}

// stubBricks 构造 TextCat 读链的桩积木；readOK 控制执行成败。
func stubBricks(readOK bool) BrickSet {
	return BrickSet{
		"oa.is_open": func(args []string) (string, bool) {
			if len(args) >= 2 && args[1] == "text-read" {
				return "true", true
			}
			return "", false
		},
		"oa.claim_next_simple": func([]string) (string, bool) { return "9001", true },
		"oa.get_str":           func([]string) (string, bool) { return `{"path":"x.txt"}`, true },
		"text.read":            func([]string) (string, bool) { return "hello", readOK },
		"text.write":           func([]string) (string, bool) { return "ok", true },
		"text.append":          func([]string) (string, bool) { return "ok", true },
		"text.replace":         func([]string) (string, bool) { return "ok", true },
		"text.read_lines":      func([]string) (string, bool) { return "lines", true },
		"text.read_between":    func([]string) (string, bool) { return "between", true },
		"text.grep":            func([]string) (string, bool) { return "grep", true },
		"oa.complete_str":      func([]string) (string, bool) { return "ok", true },
		"self.desc":            func([]string) (string, bool) { return "", true },
	}
}

// newRunner 构造最小帧序宿主。
func newRunner() *runtime.Runner {
	trace := runtime.NewTrace()
	return runtime.NewRunner(runtime.NewDataBox(), runtime.NewOA(trace), trace)
}

// TestBuildTextCat 验证图结构——四柱计数与文档序。
func TestBuildTextCat(t *testing.T) {
	doc := loadTextCat(t)
	g := Build(doc, stubBricks(true))

	if got := len(g.order); got != 36 {
		t.Fatalf("导线 = %d，期望 36", got)
	}
	if got := len(g.sensors); got != 7 {
		t.Fatalf("传感器 = %d，期望 7", got)
	}
	status := g.Status()
	if got := len(status.StateLines); got != 7 {
		t.Fatalf("状态行 = %d，期望 7", got)
	}
	if g.State("S_Read") != "Init" {
		t.Fatalf("初始态 = %s，期望 Init（首个状态）", g.State("S_Read"))
	}
	for _, sm := range doc.StateMachines {
		if g.State(sm.Name) != sm.States[0] {
			t.Fatalf("%s 初始态 = %s，期望 %s", sm.Name, g.State(sm.Name), sm.States[0])
		}
	}
}

// TestTextCatReadChain 跑完整读链——Init → Idle → Ready → Idle。
//
// 时序与 C# 探针一致：帧 3 才收异步结果（T_ReadExec 是 par 导线）。
func TestTextCatReadChain(t *testing.T) {
	doc := loadTextCat(t)
	g := Build(doc, stubBricks(true))
	runner := newRunner()
	runner.RegisterFlow(g, "TextCat")

	if got := g.State("S_Read"); got != "Init" {
		t.Fatalf("初始态 = %s，期望 Init", got)
	}
	runner.Tick()
	if got := g.State("S_Read"); got != "Idle" {
		t.Fatalf("帧1 后 = %s，期望 Idle（T_Init）", got)
	}
	runner.Tick()
	if got := g.State("S_Read"); got != "Ready" {
		t.Fatalf("帧2 后 = %s，期望 Ready（Claim → Args → Exec 启动）", got)
	}
	if g.Box("@rdOpen") != "true" {
		t.Fatalf("rdOpen = %q，期望 true（传感器帧1 落盒）", g.Box("@rdOpen"))
	}
	time.Sleep(20 * time.Millisecond)
	runner.Tick()
	if got := g.State("S_Read"); got != "Idle" {
		t.Fatalf("帧3 后 = %s，期望 Idle（Exec 结果 → Reply）", got)
	}
	if g.Box("@rdRes") != "hello" {
		t.Fatalf("@rdRes = %q，期望 hello", g.Box("@rdRes"))
	}
	fired := 0
	for _, w := range g.Status().Wires {
		if w.LastTrigger != 0 {
			fired++
		}
	}
	if fired < 5 {
		t.Fatalf("触发导线 = %d，期望 ≥5（Init/Claim/Args/Exec/Reply）", fired)
	}
}

// TestTextCatFailureBranch 执行失败走 Failed 分支并回执后回 Idle。
func TestTextCatFailureBranch(t *testing.T) {
	doc := loadTextCat(t)
	g := Build(doc, stubBricks(false))
	runner := newRunner()
	runner.RegisterFlow(g, "TextCat")

	runner.Tick()
	runner.Tick()
	time.Sleep(20 * time.Millisecond)
	runner.Tick()
	if got := g.State("S_Read"); got != "Idle" {
		t.Fatalf("失败链帧3 后 = %s，期望 Idle（Failed → Abort）", got)
	}
	if got := g.Box("@rdRes"); got != "hello" {
		t.Fatalf("@rdRes = %q，期望 hello", got)
	}
}

// TestWireTimeout 验证 [t=N] 时限——慢积木在超时后走失败分支。
func TestWireTimeout(t *testing.T) {
	doc := &front.Doc{
		StateMachines: []front.StateMachine{{Name: "S_A", States: []string{"Idle", "Done", "Failed"}}},
		Wires: []front.Wire{{
			Name:       "T_Slow",
			Timeout:    2,
			Parallel:   true,
			Conditions: []front.Condition{{IsStateAssert: true, StateName: "S_A", StateValue: "Idle"}},
			Actions:    []front.WireAction{{BrickName: "slow"}},
			Results: []front.Result{
				{StateName: "S_A", StateValue: "Done"},
				{StateName: "S_A", StateValue: "Failed"},
			},
		}},
	}
	bricks := BrickSet{
		"slow": func([]string) (string, bool) {
			time.Sleep(200 * time.Millisecond)
			return "late", true
		},
	}
	g := Build(doc, bricks)
	for i := 1; i <= 5; i++ {
		g.Tick(int64(i))
	}
	if got := g.State("S_A"); got != "Failed" {
		t.Fatalf("超时后 = %s，期望 Failed", got)
	}
}

// TestSensorThrottle 验证 [N] 节流——每 2 帧探测一次。
func TestSensorThrottle(t *testing.T) {
	doc := &front.Doc{
		StateMachines: []front.StateMachine{{Name: "S_A", States: []string{"Idle", "Done"}}},
		Sensors: []front.Sensor{{
			Name: "P_X", EveryFrames: 2, BrickName: "probe", CaptureTarget: "@x",
		}},
		Wires: []front.Wire{{
			Name:       "T_A",
			Conditions: []front.Condition{{IsBoxAssert: true, BoxName: "@x"}},
			Actions:    []front.WireAction{{BrickName: "mark"}},
			Results:    []front.Result{{StateName: "S_A", StateValue: "Done"}},
		}},
	}
	count := 0
	bricks := BrickSet{
		"probe": func([]string) (string, bool) { count++; return "true", true },
		"mark":  func([]string) (string, bool) { return "", true },
	}
	g := Build(doc, bricks)
	for i := 1; i <= 5; i++ {
		g.TickSensors(int64(i))
	}
	if count != 3 {
		t.Fatalf("探测次数 = %d，期望 3（帧 1/3/5）", count)
	}
}

// TestSensorNameCondition 验证裸传感器名条件形态。
func TestSensorNameCondition(t *testing.T) {
	doc := &front.Doc{
		StateMachines: []front.StateMachine{{Name: "S_A", States: []string{"Idle", "Done"}}},
		Sensors: []front.Sensor{{
			Name: "P_X", Passive: true, CaptureTarget: "@x",
		}},
		Wires: []front.Wire{{
			Name:       "T_A",
			Conditions: []front.Condition{{SensorName: "P_X"}},
			Actions:    []front.WireAction{{BrickName: "mark"}},
			Results:    []front.Result{{StateName: "S_A", StateValue: "Done"}},
		}},
	}
	g := Build(doc, BrickSet{"mark": func([]string) (string, bool) { return "", true }})
	g.rt.Boxes["@x"] = "true"
	g.Tick(1)
	if got := g.State("S_A"); got != "Done" {
		t.Fatalf("传感器名条件未命中: S_A = %s，期望 Done", got)
	}
}

// TestTextCatTraceSequence 对拍 C# trace.state 序列——导线名 + 目标状态 + 顺序。
//
// C# 实测（交接报告 §1.2 / MauProbe 20 帧）：
//
//	frame 1  T_Init        → S_Read=Idle
//	frame 2  T_ReadClaim   → S_Read=Claimed
//	frame 2  T_ReadArgs    → S_Read=Ready
//	frame 3  T_ReadExec    → S_Read=Done
//	frame 3  T_ReadReply   → S_Read=Idle
func TestTextCatTraceSequence(t *testing.T) {
	doc := loadTextCat(t)
	g := Build(doc, stubBricks(true))
	runner := newRunner()
	runner.RegisterFlow(g, "TextCat")

	runner.Tick()
	runner.Tick()
	time.Sleep(20 * time.Millisecond)
	runner.Tick()

	want := []string{
		"T_Init:S_Read=Idle",
		"T_ReadClaim:S_Read=Claimed",
		"T_ReadArgs:S_Read=Ready",
		"T_ReadExec:S_Read=Done",
		"T_ReadReply:S_Read=Idle",
	}
	got := make([]string, 0, len(g.rt.Trace))
	for _, e := range g.rt.Trace {
		got = append(got, e.Wire+":"+e.State+"="+e.To)
	}
	if len(got) < len(want) {
		t.Fatalf("迁移序列 = %v，期望至少 %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("第 %d 条 = %s，期望 %s（全序列 %v）", i, got[i], w, got)
		}
	}
}
