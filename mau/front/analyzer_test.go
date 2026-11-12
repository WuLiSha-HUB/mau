package front

import "testing"

// TestAnalyzeUnboundedLoop E300——无事件子图上的环且环上无积木动作。
func TestAnalyzeUnboundedLoop(t *testing.T) {
	doc := &Doc{
		StateMachines: []StateMachine{{Name: "S_A", States: []string{"Idle", "Done"}, Line: 1}},
		Wires: []Wire{
			{Name: "T_1", Line: 2,
				Conditions: []Condition{{IsStateAssert: true, StateName: "S_A", StateValue: "Idle"}},
				Results:    []Result{{StateName: "S_A", StateValue: "Done"}}},
			{Name: "T_2", Line: 3,
				Conditions: []Condition{{IsStateAssert: true, StateName: "S_A", StateValue: "Done"}},
				Results:    []Result{{StateName: "S_A", StateValue: "Idle"}}},
		},
	}
	res := Analyze(doc)
	mustHaveDiag(t, res.Errors, "E300")
}

// TestAnalyzeEventDrivenLoopAllowed 含传感器条件的环不算无界环。
func TestAnalyzeEventDrivenLoopAllowed(t *testing.T) {
	doc := &Doc{
		StateMachines: []StateMachine{{Name: "S_A", States: []string{"Idle", "Done"}, Line: 1}},
		Sensors:       []Sensor{{Name: "P_Ev", Passive: true, Line: 1}},
		Wires: []Wire{
			{Name: "T_1", Line: 2,
				Conditions: []Condition{{IsStateAssert: true, StateName: "S_A", StateValue: "Idle"}, {SensorName: "P_Ev"}},
				Results:    []Result{{StateName: "S_A", StateValue: "Done"}}},
			{Name: "T_2", Line: 3,
				Conditions: []Condition{{IsStateAssert: true, StateName: "S_A", StateValue: "Done"}, {SensorName: "P_Ev"}},
				Results:    []Result{{StateName: "S_A", StateValue: "Idle"}}},
		},
	}
	res := Analyze(doc)
	if haveDiag(res.Errors, "E300") {
		t.Fatalf("事件驱动环不应报 E300: %+v", res.Errors)
	}
}

// TestAnalyzeUnreachable E301——初始态与外生目标都到不了的状态。
func TestAnalyzeUnreachable(t *testing.T) {
	doc := &Doc{
		StateMachines: []StateMachine{{Name: "S_A", States: []string{"Idle", "Orphan"}, Line: 1}},
		Wires: []Wire{
			{Name: "T_1", Line: 2,
				Conditions: []Condition{{IsStateAssert: true, StateName: "S_A", StateValue: "Idle"}},
				Results:    []Result{{StateName: "S_A", StateValue: "Idle"}}},
		},
	}
	res := Analyze(doc)
	mustHaveDiag(t, res.Errors, "E301")
}

// TestAnalyzeNoRecovery E302——失败侧目标进不了任何终态。
func TestAnalyzeNoRecovery(t *testing.T) {
	doc := &Doc{
		StateMachines: []StateMachine{{Name: "S_A", States: []string{"Idle", "Stuck", "Fail"}, Line: 1}},
		Sensors:       []Sensor{{Name: "P_Ev", Passive: true, Line: 1}},
		Wires: []Wire{
			{Name: "T_1", Line: 2,
				Conditions: []Condition{{IsStateAssert: true, StateName: "S_A", StateValue: "Idle"}},
				Results:    []Result{{StateName: "S_A", StateValue: "Idle"}, {StateName: "S_A", StateValue: "Stuck"}}},
			{Name: "T_2", Line: 3,
				Conditions: []Condition{{IsStateAssert: true, StateName: "S_A", StateValue: "Stuck"}, {SensorName: "P_Ev"}},
				Results:    []Result{{StateName: "S_A", StateValue: "Stuck"}}},
		},
	}
	res := Analyze(doc)
	mustHaveDiag(t, res.Errors, "E302")
}

// TestAnalyzeCriticalPathReport 关键路径报告非空。
func TestAnalyzeCriticalPathReport(t *testing.T) {
	doc := &Doc{
		StateMachines: []StateMachine{{Name: "S_A", States: []string{"A", "B", "C"}, Line: 1}},
		Sensors:       []Sensor{{Name: "P_Ev", Passive: true, Line: 1}},
		Wires: []Wire{
			{Name: "T_1", Line: 2,
				Conditions: []Condition{{IsStateAssert: true, StateName: "S_A", StateValue: "A"}, {SensorName: "P_Ev"}},
				Results:    []Result{{StateName: "S_A", StateValue: "B"}}},
			{Name: "T_2", Line: 3,
				Conditions: []Condition{{IsStateAssert: true, StateName: "S_A", StateValue: "B"}, {SensorName: "P_Ev"}},
				Results:    []Result{{StateName: "S_A", StateValue: "C"}}},
		},
	}
	res := Analyze(doc)
	if len(res.Reports) != 1 {
		t.Fatalf("报告数 = %d，期望 1: %+v", len(res.Reports), res.Reports)
	}
	if res.Reports[0] == "" {
		t.Fatalf("报告为空")
	}
}

// TestAnalyzeEmpty 空 IR 按 E300 拒绝（与 C# 同口径）。
func TestAnalyzeEmpty(t *testing.T) {
	res := Analyze(nil)
	mustHaveDiag(t, res.Errors, "E300")
}
