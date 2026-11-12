package front

import (
	"os"
	"testing"
)

// testBricks 构造测试用积木索引。
func testBricks() *BrickTable {
	return NewBrickTable([]BrickContract{
		{Name: "b.out_str", InputTypes: []string{"string"}, OutputTypes: []string{"string"}},
		{Name: "b.out_long", InputTypes: []string{"string"}, OutputTypes: []string{"long"}},
		{Name: "b.no_out", InputTypes: []string{"string"}},
		{Name: "b.two", InputTypes: []string{"string", "long"}, OutputTypes: []string{"string"}},
		{Name: "b.arr_out", InputTypes: []string{"string"}, OutputTypes: []string{"string[]"}},
		{Name: "b.in_long", InputTypes: []string{"long"}, OutputTypes: []string{"string"}},
	})
}

// haveDiag 报告诊断集合中是否存在指定码。
func haveDiag(diags []Diag, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

// mustHaveDiag 断言指定诊断码存在。
func mustHaveDiag(t *testing.T, diags []Diag, code string) {
	t.Helper()
	if !haveDiag(diags, code) {
		t.Fatalf("期望诊断 %s，实际 %+v", code, diags)
	}
}

// TestValidateTextCat 用真实语料 + 真实积木索引做消费点验收。
//
// 判据：TextCat 语料在 C# 侧能通过校验与分析——Go 侧同样必须零诊断。
func TestValidateTextCat(t *testing.T) {
	corpus := envOr("MAU_TEXT_CAT", `D:\Mau\Mau\corpus\ch4\TextCat\text_cat.mau`)
	index := envOr("MAU_BRICK_INDEX", `D:\Mau\Mau\Bricks\index.json`)
	src, err := os.ReadFile(corpus)
	if err != nil {
		t.Skipf("跳过：语料不可读（%v）——设 MAU_TEXT_CAT 指向 text_cat.mau", err)
	}
	bricks, err := LoadBrickIndex(index)
	if err != nil {
		t.Skipf("跳过：积木索引不可读（%v）——设 MAU_BRICK_INDEX 指向 index.json", err)
	}
	toks, diags := Lex(string(src))
	if len(diags) != 0 {
		t.Fatalf("词法诊断: %+v", diags)
	}
	doc := Parse(toks)
	if !doc.Success() {
		t.Fatalf("语法诊断: %+v", doc.Diags)
	}
	if n := Validate(doc, bricks); n != 0 {
		t.Fatalf("校验诊断 %d 条: %+v", n, doc.Diags)
	}
	res := Analyze(doc)
	if !res.Success() {
		t.Fatalf("分析错误: %+v", res.Errors)
	}
	if len(res.Reports) == 0 {
		t.Fatalf("关键路径报告缺失")
	}
}

// TestValidateNames E200——跨四柱重名。
func TestValidateNames(t *testing.T) {
	doc := &Doc{
		StateMachines: []StateMachine{{Name: "S_A", States: []string{"Idle", "Done"}, Line: 1}},
		Sensors:       []Sensor{{Name: "S_A", Passive: true, Line: 2}},
	}
	Validate(doc, testBricks())
	mustHaveDiag(t, doc.Diags, "E200")
}

// TestValidateReferences E201 / E202 / E203——引用完整性。
func TestValidateReferences(t *testing.T) {
	doc := &Doc{
		StateMachines: []StateMachine{{Name: "S_A", States: []string{"Idle"}, Line: 1}},
		Wires: []Wire{
			{Name: "T_1", Line: 5, Conditions: []Condition{{SensorName: "P_Nope"}}, Results: []Result{{StateName: "S_A", StateValue: "Idle"}}},
			{Name: "T_2", Line: 6, Results: []Result{{StateName: "S_Nope", StateValue: "Idle"}}},
			{Name: "T_3", Line: 7, Results: []Result{{StateName: "S_A", StateValue: "Nope"}}},
		},
	}
	Validate(doc, testBricks())
	mustHaveDiag(t, doc.Diags, "E201")
	mustHaveDiag(t, doc.Diags, "E202")
	mustHaveDiag(t, doc.Diags, "E203")
}

// TestValidateSensors E204 / E206——传感器规则。
func TestValidateSensors(t *testing.T) {
	doc := &Doc{
		Sensors: []Sensor{
			{Name: "P_Active", Passive: false, BrickName: "", Line: 1},
			{Name: "P_CmdA", Passive: true, IsCmd: true, CmdKey: "cmd.x", Line: 2},
			{Name: "P_CmdB", Passive: true, IsCmd: true, CmdKey: "cmd.x", Line: 3},
		},
	}
	Validate(doc, testBricks())
	mustHaveDiag(t, doc.Diags, "E204")
	mustHaveDiag(t, doc.Diags, "E206")
}

// TestValidateBricks E400 / E401 / E402——积木调用门禁。
func TestValidateBricks(t *testing.T) {
	results := []Result{{StateName: "S_A", StateValue: "Idle"}}
	doc := &Doc{
		StateMachines: []StateMachine{{Name: "S_A", States: []string{"Idle"}, Line: 1}},
		Wires: []Wire{
			{Name: "T_1", Line: 2, Actions: []WireAction{{BrickName: "b.nope", Line: 2}}, Results: results},
			{Name: "T_2", Line: 3, Actions: []WireAction{{BrickName: "b.two", BrickArgs: []string{"\"x\""}, Line: 3}}, Results: results},
			{Name: "T_3", Line: 4, Actions: []WireAction{{BrickName: "b.out_long", BrickArgs: []string{"\"x\""}, Line: 4}}, Results: results},
			{Name: "T_4", Line: 5, Actions: []WireAction{{BrickName: "b.in_long", BrickArgs: []string{"\"x\""}, Line: 5}}, Results: results},
		},
	}
	Validate(doc, testBricks())
	mustHaveDiag(t, doc.Diags, "E400")
	mustHaveDiag(t, doc.Diags, "E401")
	mustHaveDiag(t, doc.Diags, "E402")
}

// TestValidateBoxes E405 / E406 / E407 / E408 / E402（盒子类型）——盒子规则。
func TestValidateBoxes(t *testing.T) {
	results := []Result{{StateName: "S_A", StateValue: "Idle"}}
	doc := &Doc{
		StateMachines: []StateMachine{{Name: "S_A", States: []string{"Idle"}, Line: 1}},
		Wires: []Wire{
			{Name: "T_1", Line: 2, Actions: []WireAction{{BrickName: "b.no_out", BrickArgs: []string{"\"x\""}, CaptureTarget: "@noOut", Line: 2}}, Results: results},
			{Name: "T_2", Line: 3, Actions: []WireAction{{BrickName: "b.arr_out", BrickArgs: []string{"\"x\""}, CaptureTarget: "@arr", Line: 3}}, Results: results},
			{Name: "T_3", Line: 4, Actions: []WireAction{{BrickName: "b.out_str", BrickArgs: []string{"@missing"}, Line: 4}}, Results: results},
			{Name: "T_4", Line: 5, Actions: []WireAction{
				{BrickName: "b.out_str", BrickArgs: []string{"\"x\""}, CaptureTarget: "@dup", Line: 5},
				{BrickName: "b.out_str", BrickArgs: []string{"\"y\""}, CaptureTarget: "@dup", Line: 5},
			}, Results: results},
			{Name: "T_5", Line: 6, Actions: []WireAction{{BrickName: "b.out_long", BrickArgs: []string{"\"x\""}, CaptureTarget: "@num", Line: 6}}, Results: results},
			{Name: "T_6", Line: 7, Actions: []WireAction{{BrickName: "b.out_str", BrickArgs: []string{"@num"}, Line: 7}}, Results: results},
		},
	}
	Validate(doc, testBricks())
	mustHaveDiag(t, doc.Diags, "E405")
	mustHaveDiag(t, doc.Diags, "E406")
	mustHaveDiag(t, doc.Diags, "E407")
	mustHaveDiag(t, doc.Diags, "E408")
	mustHaveDiag(t, doc.Diags, "E402")
}

// TestValidateBoxCondition E407——盒子判真条件读无写源。
func TestValidateBoxCondition(t *testing.T) {
	doc := &Doc{
		StateMachines: []StateMachine{{Name: "S_A", States: []string{"Idle", "Done"}, Line: 1}},
		Wires: []Wire{
			{Name: "T_1", Line: 2,
				Conditions: []Condition{{IsBoxAssert: true, BoxName: "@ghost"}, {IsStateAssert: true, StateName: "S_A", StateValue: "Idle"}},
				Results:    []Result{{StateName: "S_A", StateValue: "Done"}}},
		},
	}
	Validate(doc, testBricks())
	mustHaveDiag(t, doc.Diags, "E407")
}

// envOr 取环境变量，空则回落默认值。
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
