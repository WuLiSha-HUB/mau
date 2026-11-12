package front

import (
	"os"
	"testing"
)

// TestLexGlyphs 覆盖 v3-default 外观表——15 个单字符 + 2 个多字符字形。
func TestLexGlyphs(t *testing.T) {
	src := "§ = : ⇐ ↻ → | & { } , [ ] > ⇚ := <-"
	toks, diags := Lex(src)
	if len(diags) != 0 {
		t.Fatalf("词法诊断: %+v", diags)
	}
	want := []Kind{
		KindSection, KindEq, KindColon, KindIn, KindSample, KindArrow,
		KindBranch, KindAnd, KindSetOpen, KindSetClose, KindSep,
		KindParamOpen, KindParamClose, KindCapture, KindCmdIn,
		KindDeclare, KindIn, KindEOF,
	}
	if len(toks) != len(want) {
		t.Fatalf("Token 数 = %d，期望 %d：%v", len(toks), len(want), toks)
	}
	for i, w := range want {
		if toks[i].Kind != w {
			t.Fatalf("第 %d 个 Token = %s，期望 %s", i, toks[i].Kind, w)
		}
	}
}

// TestLexBOM 确认 UTF-8 BOM 被剥离——语料文件可能带 BOM。
func TestLexBOM(t *testing.T) {
	toks, diags := Lex("\ufeff§ 'S_A' = { 'A' }")
	if len(diags) != 0 {
		t.Fatalf("词法诊断: %+v", diags)
	}
	if toks[0].Kind != KindSection {
		t.Fatalf("首 Token = %s，期望 Section——BOM 未剥离", toks[0].Kind)
	}
}

// TestLexErrors 覆盖词法四码：E001 非法字符 / E002 名称 / E003 字符串 / E004 参数未闭合。
func TestLexErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		code string
	}{
		{"E001 非法字符", "%", "E001"},
		{"E002 名称字符非法", "'bad name'", "E002"},
		{"E002 名称未闭合", "'abc", "E002"},
		{"E003 字符串未闭合", "\"abc", "E003"},
		{"E004 参数未闭合", "[1", "E004"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, diags := Lex(c.src)
			if len(diags) == 0 {
				t.Fatalf("期望 %s，实际无诊断", c.code)
			}
			if diags[0].Code != c.code {
				t.Fatalf("诊断码 = %s，期望 %s（%+v）", diags[0].Code, c.code, diags)
			}
		})
	}
}

// TestParseUnits 覆盖四柱最小形态——状态机 / 传感器 / 导线 / 槽。
func TestParseUnits(t *testing.T) {
	src := "§ 'S_A' = { 'Idle', 'Done' }\n" +
		"§ 'P_X' ↻ [1]: 'oa.is_open'[\"TOOL\", \"text-read\"] > @open\n" +
		"§ 'T_A' [t=1200, par] : @open & 'S_A' = 'Idle' → 'text.read'[@args] > @res | 'S_A' = 'Done'\n" +
		"§ 'R_Q' : 8\n"
	toks, diags := Lex(src)
	if len(diags) != 0 {
		t.Fatalf("词法诊断: %+v", diags)
	}
	doc := Parse(toks)
	if !doc.Success() {
		t.Fatalf("语法诊断: %+v", doc.Diags)
	}
	if len(doc.StateMachines) != 1 || len(doc.Sensors) != 1 || len(doc.Wires) != 1 || len(doc.Slots) != 1 {
		t.Fatalf("四柱计数错: sm=%d sensor=%d wire=%d slot=%d",
			len(doc.StateMachines), len(doc.Sensors), len(doc.Wires), len(doc.Slots))
	}

	sm := doc.StateMachines[0]
	if sm.Name != "S_A" || len(sm.States) != 2 || sm.States[0] != "Idle" || sm.States[1] != "Done" {
		t.Fatalf("状态机字段错: %+v", sm)
	}

	sensor := doc.Sensors[0]
	if sensor.Name != "P_X" || sensor.Passive || sensor.EveryFrames != 1 {
		t.Fatalf("传感器壳字段错: %+v", sensor)
	}
	if sensor.BrickName != "oa.is_open" || len(sensor.BrickArgs) != 2 ||
		sensor.BrickArgs[0] != "\"TOOL\"" || sensor.BrickArgs[1] != "\"text-read\"" {
		t.Fatalf("传感器积木字段错: %+v", sensor)
	}
	if sensor.CaptureTarget != "@open" {
		t.Fatalf("传感器捕获目标 = %q，期望 @open", sensor.CaptureTarget)
	}

	wire := doc.Wires[0]
	if wire.Timeout != 1200 || !wire.Parallel || wire.Logging {
		t.Fatalf("导线属性错: %+v", wire)
	}
	if len(wire.Conditions) != 2 {
		t.Fatalf("条件数 = %d，期望 2", len(wire.Conditions))
	}
	if c := wire.Conditions[0]; !c.IsBoxAssert || c.BoxName != "@open" {
		t.Fatalf("条件[0] = %+v，期望盒断言 @open", c)
	}
	if c := wire.Conditions[1]; !c.IsStateAssert || c.StateName != "S_A" || c.StateValue != "Idle" {
		t.Fatalf("条件[1] = %+v，期望状态断言 S_A = Idle", c)
	}
	if len(wire.Actions) != 1 {
		t.Fatalf("动作数 = %d，期望 1", len(wire.Actions))
	}
	if a := wire.Actions[0]; a.BrickName != "text.read" || len(a.BrickArgs) != 1 || a.BrickArgs[0] != "@args" || a.CaptureTarget != "@res" {
		t.Fatalf("动作[0] = %+v", a)
	}
	// 单动作兼容字段
	if wire.BrickName != "text.read" || wire.CaptureTarget != "@res" {
		t.Fatalf("单动作镜像错: %+v", wire)
	}
	if len(wire.Results) != 1 || wire.Results[0].StateName != "S_A" || wire.Results[0].StateValue != "Done" {
		t.Fatalf("结果区 = %+v", wire.Results)
	}

	slot := doc.Slots[0]
	if slot.Name != "R_Q" || slot.Capacity != 8 {
		t.Fatalf("槽 = %+v", slot)
	}
}

// TestParsePassiveSensor 覆盖被动传感器两形态——⇐ 与 ⇚。
func TestParsePassiveSensor(t *testing.T) {
	src := "§ 'P_A' ⇐\n§ 'P_B' ⇚ \"cmd.ping\"\n"
	toks, diags := Lex(src)
	if len(diags) != 0 {
		t.Fatalf("词法诊断: %+v", diags)
	}
	doc := Parse(toks)
	if !doc.Success() {
		t.Fatalf("语法诊断: %+v", doc.Diags)
	}
	if len(doc.Sensors) != 2 {
		t.Fatalf("传感器数 = %d，期望 2", len(doc.Sensors))
	}
	if s := doc.Sensors[0]; !s.Passive || s.IsCmd {
		t.Fatalf("⇐ 传感器 = %+v", s)
	}
	if s := doc.Sensors[1]; !s.Passive || !s.IsCmd || s.CmdKey != "cmd.ping" {
		t.Fatalf("⇚ 传感器 = %+v", s)
	}
}

// TestParseErrors 覆盖语法五码。
func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		code string
	}{
		{"E100 未知前缀", "§ 'X_A' = { 'A' }", "E100"},
		{"E101 段外 token", "'S_A' = { 'A' }", "E101"},
		{"E102 状态机格式", "§ 'S_A' = 'A'", "E102"},
		{"E103 导线缺箭头", "§ 'T_A' : 'S_A' = 'Idle' | 'S_A' = 'Done'", "E103"},
		{"E104 槽格式", "§ 'R_Q' : 0", "E104"},
		{"E105 传感器形态", "§ 'P_A' : 'x'", "E105"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			toks, _ := Lex(c.src)
			doc := Parse(toks)
			if doc.Success() {
				t.Fatalf("期望 %s，实际零诊断", c.code)
			}
			if doc.Diags[0].Code != c.code {
				t.Fatalf("诊断码 = %s，期望 %s（%+v）", doc.Diags[0].Code, c.code, doc.Diags)
			}
		})
	}
}

// TestParseTextCat 用真实语料做消费点验收。
//
// 判据来源：C# 生成物 public/src/TextCat/FL_TextCat.cs 第 2 行
// 「四柱: 状态机 7 / 传感器 7 / 导线 36 / 槽 0」。
func TestParseTextCat(t *testing.T) {
	path := os.Getenv("MAU_TEXT_CAT")
	if path == "" {
		path = `D:\Mau\Mau\corpus\ch4\TextCat\text_cat.mau`
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("跳过：语料不可读（%v）——设 MAU_TEXT_CAT 指向 text_cat.mau", err)
	}
	toks, diags := Lex(string(src))
	if len(diags) != 0 {
		t.Fatalf("词法诊断: %+v", diags)
	}
	doc := Parse(toks)
	if !doc.Success() {
		t.Fatalf("语法诊断: %+v", doc.Diags)
	}

	if got := len(doc.StateMachines); got != 7 {
		t.Fatalf("状态机 = %d，期望 7", got)
	}
	if got := len(doc.Sensors); got != 7 {
		t.Fatalf("传感器 = %d，期望 7", got)
	}
	if got := len(doc.Wires); got != 36 {
		t.Fatalf("导线 = %d，期望 36", got)
	}
	if got := len(doc.Slots); got != 0 {
		t.Fatalf("槽 = %d，期望 0", got)
	}

	wantStates := map[string]int{
		"S_Read":    6,
		"S_Write":   5,
		"S_Append":  5,
		"S_Replace": 5,
		"S_Lines":   5,
		"S_Between": 5,
		"S_Grep":    5,
	}
	for _, sm := range doc.StateMachines {
		want, ok := wantStates[sm.Name]
		if !ok {
			t.Fatalf("未预期的状态机: %s", sm.Name)
		}
		if len(sm.States) != want {
			t.Fatalf("%s 状态数 = %d，期望 %d", sm.Name, len(sm.States), want)
		}
	}

	wantSensors := []string{
		"P_ReadOpen", "P_WriteOpen", "P_AppendOpen", "P_ReplaceOpen",
		"P_LinesOpen", "P_BetweenOpen", "P_GrepOpen",
	}
	for i, want := range wantSensors {
		s := doc.Sensors[i]
		if s.Name != want {
			t.Fatalf("传感器[%d] = %s，期望 %s", i, s.Name, want)
		}
		if s.Passive || s.EveryFrames != 1 || s.BrickName != "oa.is_open" {
			t.Fatalf("%s 字段错: %+v", want, s)
		}
	}

	// 抽验 read 线五导线——结构完整性与属性
	byName := make(map[string]Wire, len(doc.Wires))
	for _, w := range doc.Wires {
		byName[w.Name] = w
	}
	if w, ok := byName["T_Init"]; !ok || len(w.Actions) != 1 || len(w.Results) != 2 || w.Timeout != 0 {
		t.Fatalf("T_Init = %+v", w)
	}
	if w, ok := byName["T_ReadExec"]; !ok || w.Timeout != 1200 || !w.Parallel ||
		w.BrickName != "text.read" || w.CaptureTarget != "@rdRes" || len(w.Results) != 2 {
		t.Fatalf("T_ReadExec = %+v", w)
	}
	if w, ok := byName["T_ReadClaim"]; !ok || len(w.Conditions) != 2 ||
		w.Conditions[0].BoxName != "@rdOpen" || w.Conditions[1].StateValue != "Idle" ||
		w.Actions[0].BrickName != "oa.claim_next_simple" || len(w.Results) != 2 {
		t.Fatalf("T_ReadClaim = %+v", w)
	}
}
