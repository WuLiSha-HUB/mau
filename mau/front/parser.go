package front

import (
	"strconv"
	"strings"
)

// Parse 把 Token 流解析为四柱 IR——按 § 分段，按段首 Name 前缀分派单元。
//
// 单元前缀：S_ 状态机 / P_ 传感器 / T_ 导线 / R_ 槽；其它前缀报 E100。
func Parse(tokens []Token) *Doc {
	doc := &Doc{}
	if len(tokens) == 0 {
		doc.Diags = append(doc.Diags, Diag{Code: "E101", Line: 1, Msg: "空 Token 流"})
		return doc
	}
	for _, section := range splitSections(tokens, doc) {
		parseSection(section, doc)
	}
	return doc
}

// splitSections 按 § 切段——段不含 § 自身；第一个 § 之前的非注释 token 报 E101。
func splitSections(tokens []Token, doc *Doc) [][]Token {
	var sections [][]Token
	var current []Token
	started := false
	for _, t := range tokens {
		switch t.Kind {
		case KindSection:
			if started {
				sections = append(sections, current)
			}
			current = nil
			started = true
		case KindEOF:
			if started {
				sections = append(sections, current)
			}
			return sections
		default:
			if !started {
				doc.Diags = append(doc.Diags, Diag{Code: "E101", Line: t.Line, Msg: "段外 token——声明必须以 § 段首标记开头"})
				continue
			}
			current = append(current, t)
		}
	}
	if started {
		sections = append(sections, current)
	}
	return sections
}

// parseSection 解析单段——空段忽略，其余按段首名前缀分派。
func parseSection(section []Token, doc *Doc) {
	if len(section) == 0 {
		return
	}
	first := section[0]
	if first.Kind != KindName {
		doc.Diags = append(doc.Diags, Diag{Code: "E100", Line: first.Line, Msg: "段首必须是专有名词（'S_...'/'P_...'/'T_...'/'R_...'）"})
		return
	}
	switch {
	case strings.HasPrefix(first.Value, "S_"):
		parseStateMachine(first, section, doc)
	case strings.HasPrefix(first.Value, "P_"):
		parseSensor(first, section, doc)
	case strings.HasPrefix(first.Value, "T_"):
		parseWire(first, section, doc)
	case strings.HasPrefix(first.Value, "R_"):
		parseSlot(first, section, doc)
	default:
		doc.Diags = append(doc.Diags, Diag{Code: "E100", Line: first.Line, Msg: "未知单元前缀: '" + first.Value + "'——只支持 S_（状态机）/ P_（传感器）/ T_（导线）/ R_（槽）"})
	}
}

// parseStateMachine 解析状态机——§ 'S_X' = { 'A', 'B' }。
func parseStateMachine(first Token, s []Token, doc *Doc) {
	if len(s) < 5 || s[1].Kind != KindEq || s[2].Kind != KindSetOpen {
		doc.Diags = append(doc.Diags, Diag{Code: "E102", Line: first.Line, Msg: "状态机格式: § 'S_X' = { 'A', 'B' }——缺 = 或 {"})
		return
	}
	def := StateMachine{Name: first.Value, Line: first.Line}
	closed := false
	for i := 3; i < len(s); i++ {
		if s[i].Kind == KindSetClose {
			closed = true
			break
		}
		if s[i].Kind == KindSep {
			continue
		}
		if s[i].Kind != KindName {
			doc.Diags = append(doc.Diags, Diag{Code: "E102", Line: s[i].Line, Msg: "状态集合内必须是专有名词——元素非法"})
			return
		}
		if containsString(def.States, s[i].Value) {
			doc.Diags = append(doc.Diags, Diag{Code: "E102", Line: s[i].Line, Msg: "状态重复: '" + s[i].Value + "'"})
			return
		}
		def.States = append(def.States, s[i].Value)
	}
	if !closed {
		doc.Diags = append(doc.Diags, Diag{Code: "E102", Line: first.Line, Msg: "状态集合未闭合——缺少 }"})
		return
	}
	if len(def.States) == 0 {
		doc.Diags = append(doc.Diags, Diag{Code: "E102", Line: first.Line, Msg: "状态集合为空"})
		return
	}
	doc.StateMachines = append(doc.StateMachines, def)
}

// parseSensor 解析传感器——⇐ 被动 / ⇚ 指令 / ↻ 主动采样。
func parseSensor(first Token, s []Token, doc *Doc) {
	sensor := Sensor{Name: first.Value, Line: first.Line}
	if len(s) < 2 {
		doc.Diags = append(doc.Diags, Diag{Code: "E105", Line: first.Line, Msg: "传感器格式: § 'P_X' ⇐ / ⇚ [key] / ↻ [N]: 积木[参数] > 盒"})
		return
	}
	i := 2
	switch s[1].Kind {
	case KindIn:
		sensor.Passive = true
	case KindCmdIn:
		sensor.Passive = true
		sensor.IsCmd = true
		if i >= len(s) || s[i].Kind != KindStr {
			doc.Diags = append(doc.Diags, Diag{Code: "E105", Line: first.Line, Msg: "指令传感器缺少命令名——⇚ 后需字符串"})
			return
		}
		sensor.CmdKey = s[i].Value
		i++
	case KindSample:
		var ok bool
		if i, ok = parseSensorSample(s, i, &sensor, doc); !ok {
			return
		}
	default:
		doc.Diags = append(doc.Diags, Diag{Code: "E105", Line: first.Line, Msg: "传感器缺少形态标记——⇐ / ⇚ / ↻"})
		return
	}
	if i < len(s) {
		doc.Diags = append(doc.Diags, Diag{Code: "E105", Line: s[i].Line, Msg: "传感器尾部有多余 token"})
		return
	}
	doc.Sensors = append(doc.Sensors, sensor)
}

// parseSensorSample 解析主动传感器主体——[N] : 积木[参数] > 盒 → 分支动作。
func parseSensorSample(s []Token, i int, sensor *Sensor, doc *Doc) (int, bool) {
	if i < len(s) && s[i].Kind == KindParamOpen {
		frames, next, ok := parseFrames(s, i)
		if !ok {
			doc.Diags = append(doc.Diags, Diag{Code: "E105", Line: s[i].Line, Msg: "采样间隔格式: [N]"})
			return i, false
		}
		sensor.EveryFrames = frames
		i = next
	}
	if i >= len(s) || s[i].Kind != KindColon {
		doc.Diags = append(doc.Diags, Diag{Code: "E105", Line: sensor.Line, Msg: "采样传感器缺少 :——§ 'P_X' ↻ [N]: 积木"})
		return i, false
	}
	i++
	if i >= len(s) || s[i].Kind != KindName {
		doc.Diags = append(doc.Diags, Diag{Code: "E105", Line: sensor.Line, Msg: "传感器缺少探测积木名"})
		return i, false
	}
	sensor.BrickName = s[i].Value
	i++
	if i < len(s) && s[i].Kind == KindParamOpen {
		args, next, ok := collectArgs(s, i)
		if !ok {
			doc.Diags = append(doc.Diags, Diag{Code: "E105", Line: s[i].Line, Msg: "传感器参数区格式错"})
			return i, false
		}
		sensor.BrickArgs = args
		i = next
	}
	if i < len(s) && s[i].Kind == KindCapture {
		if i+1 >= len(s) || s[i+1].Kind != KindName {
			doc.Diags = append(doc.Diags, Diag{Code: "E105", Line: s[i].Line, Msg: "捕获缺少目标盒——> 后需 @盒"})
			return i, false
		}
		sensor.CaptureTarget = s[i+1].Value
		i += 2
	}
	if i < len(s) && s[i].Kind == KindArrow {
		i++
		var ok bool
		if i, ok = parseSensorActions(s, i, sensor, doc); !ok {
			return i, false
		}
	}
	return i, true
}

// parseSensorActions 解析传感器分支动作——真分支 | 假分支（最多两段）。
func parseSensorActions(s []Token, i int, sensor *Sensor, doc *Doc) (int, bool) {
	falseSide := false
	for i < len(s) {
		if s[i].Kind == KindBranch {
			if falseSide {
				doc.Diags = append(doc.Diags, Diag{Code: "E105", Line: s[i].Line, Msg: "传感器分支只支持真/假两段"})
				return i, false
			}
			falseSide = true
			i++
			continue
		}
		if s[i].Kind != KindName {
			doc.Diags = append(doc.Diags, Diag{Code: "E105", Line: s[i].Line, Msg: "传感器动作区必须是积木名"})
			return i, false
		}
		act := SensorAction{BrickName: s[i].Value}
		i++
		if i < len(s) && s[i].Kind == KindParamOpen {
			args, next, ok := collectArgs(s, i)
			if !ok {
				doc.Diags = append(doc.Diags, Diag{Code: "E105", Line: s[i].Line, Msg: "传感器动作参数区格式错"})
				return i, false
			}
			act.BrickArgs = args
			i = next
		}
		if falseSide {
			sensor.FalseActions = append(sensor.FalseActions, act)
		} else {
			sensor.TrueActions = append(sensor.TrueActions, act)
		}
	}
	return i, true
}

// parseWire 解析导线——§ 'T_X' [属性] : 条件 → 动作 | 结果。
func parseWire(first Token, s []Token, doc *Doc) {
	wire := Wire{Name: first.Value, Line: first.Line}
	i := 1
	if i < len(s) && s[i].Kind == KindParamOpen {
		next, ok := parseWireAttrs(s, i, &wire, doc)
		if !ok {
			return
		}
		i = next
	}
	if i >= len(s) || s[i].Kind != KindColon {
		doc.Diags = append(doc.Diags, Diag{Code: "E103", Line: first.Line, Msg: "导线缺少 :——§ 'T_X' : 条件 → 动作 | 结果"})
		return
	}
	i++
	arrow := findArrow(s, i)
	if arrow < 0 {
		doc.Diags = append(doc.Diags, Diag{Code: "E103", Line: first.Line, Msg: "导线缺少 →"})
		return
	}
	if !parseConditions(s, i, arrow, &wire, doc) {
		return
	}
	i = arrow + 1
	if i, ok := parseWireActions(s, i, &wire, doc); !ok {
		return
	} else if !parseWireResults(s, i, &wire, doc) {
		return
	}
	if wire.Parallel && len(wire.Actions) > 1 {
		doc.Diags = append(doc.Diags, Diag{Code: "E103", Line: first.Line, Msg: "par 导线只允许单动作"})
		return
	}
	doc.Wires = append(doc.Wires, wire)
}

// findArrow 在 [from, len) 内找第一个 →，找不到返回 -1。
func findArrow(s []Token, from int) int {
	for i := from; i < len(s); i++ {
		if s[i].Kind == KindArrow {
			return i
		}
	}
	return -1
}

// parseConditions 解析条件区——状态断言 / 盒断言 / 传感器名，& 分隔。
func parseConditions(s []Token, from, arrow int, wire *Wire, doc *Doc) bool {
	for i := from; i < arrow; {
		if s[i].Kind == KindAnd {
			i++
			continue
		}
		if s[i].Kind != KindName {
			doc.Diags = append(doc.Diags, Diag{Code: "E103", Line: s[i].Line, Msg: "条件区必须是状态断言 / 盒断言 / 传感器名"})
			return false
		}
		if i+2 < arrow && s[i+1].Kind == KindEq {
			wire.Conditions = append(wire.Conditions, Condition{IsStateAssert: true, StateName: s[i].Value, StateValue: s[i+2].Value})
			i += 3
			continue
		}
		if strings.HasPrefix(s[i].Value, "@") {
			wire.Conditions = append(wire.Conditions, Condition{IsBoxAssert: true, BoxName: s[i].Value})
		} else {
			wire.Conditions = append(wire.Conditions, Condition{SensorName: s[i].Value})
		}
		i++
	}
	return true
}

// parseWireActions 解析动作区——积木[参数] > 盒，遇结果区起始（Name 后跟 =）停下。
func parseWireActions(s []Token, i int, wire *Wire, doc *Doc) (int, bool) {
	for i < len(s) {
		if s[i].Kind == KindBranch {
			i++
			continue
		}
		if s[i].Kind != KindName {
			doc.Diags = append(doc.Diags, Diag{Code: "E103", Line: s[i].Line, Msg: "动作区必须是积木名或结果区状态"})
			return i, false
		}
		if i+1 < len(s) && s[i+1].Kind == KindEq {
			break
		}
		act := WireAction{BrickName: s[i].Value, Line: s[i].Line}
		i++
		if i < len(s) && s[i].Kind == KindParamOpen {
			args, next, ok := collectArgs(s, i)
			if !ok {
				doc.Diags = append(doc.Diags, Diag{Code: "E103", Line: s[i].Line, Msg: "动作参数区格式错"})
				return i, false
			}
			act.BrickArgs = args
			i = next
		}
		if i < len(s) && s[i].Kind == KindCapture {
			if i+1 >= len(s) || s[i+1].Kind != KindName {
				doc.Diags = append(doc.Diags, Diag{Code: "E103", Line: s[i].Line, Msg: "捕获缺少目标盒——> 后需 @盒"})
				return i, false
			}
			act.CaptureTarget = s[i+1].Value
			i += 2
		}
		wire.Actions = append(wire.Actions, act)
	}
	if len(wire.Actions) > 0 {
		// 单动作兼容：镜像 Actions[0]——生成器与解释器读这三个字段
		wire.BrickName = wire.Actions[0].BrickName
		wire.BrickArgs = wire.Actions[0].BrickArgs
		wire.CaptureTarget = wire.Actions[0].CaptureTarget
	}
	return i, true
}

// parseWireResults 解析结果区——'S_X' = 'State' | 'S_X' 'State'。
func parseWireResults(s []Token, i int, wire *Wire, doc *Doc) bool {
	for i < len(s) {
		if s[i].Kind == KindBranch {
			i++
			continue
		}
		if s[i].Kind != KindName {
			doc.Diags = append(doc.Diags, Diag{Code: "E103", Line: s[i].Line, Msg: "结果区必须是状态机名"})
			return false
		}
		res := Result{StateName: s[i].Value}
		i++
		if i < len(s) && s[i].Kind == KindEq {
			i++
		}
		if i >= len(s) || s[i].Kind != KindName {
			doc.Diags = append(doc.Diags, Diag{Code: "E103", Line: s[i-1].Line, Msg: "结果缺少目标状态"})
			return false
		}
		res.StateValue = s[i].Value
		i++
		wire.Results = append(wire.Results, res)
	}
	if len(wire.Results) == 0 {
		doc.Diags = append(doc.Diags, Diag{Code: "E103", Line: wire.Line, Msg: "导线缺少结果区"})
		return false
	}
	return true
}

// parseWireAttrs 解析导线属性区——[t=N, par, !]。
func parseWireAttrs(s []Token, start int, wire *Wire, doc *Doc) (int, bool) {
	i := start + 1
	for i < len(s) {
		switch s[i].Kind {
		case KindParamClose:
			return i + 1, true
		case KindSep:
			i++
		case KindWord:
			switch s[i].Value {
			case "t":
				if i+2 >= len(s) || s[i+1].Kind != KindEq || s[i+2].Kind != KindNum {
					doc.Diags = append(doc.Diags, Diag{Code: "E103", Line: s[i].Line, Msg: "超时属性格式: t=N"})
					return i, false
				}
				n, err := strconv.Atoi(s[i+2].Value)
				if err != nil {
					doc.Diags = append(doc.Diags, Diag{Code: "E103", Line: s[i+2].Line, Msg: "超时值必须是整数: '" + s[i+2].Value + "'"})
					return i, false
				}
				wire.Timeout = n
				i += 3
			case "par":
				wire.Parallel = true
				i++
			case "!":
				wire.Logging = true
				i++
			default:
				doc.Diags = append(doc.Diags, Diag{Code: "E103", Line: s[i].Line, Msg: "未知导线属性: '" + s[i].Value + "'"})
				return i, false
			}
		default:
			doc.Diags = append(doc.Diags, Diag{Code: "E103", Line: s[i].Line, Msg: "导线属性区格式错"})
			return i, false
		}
	}
	doc.Diags = append(doc.Diags, Diag{Code: "E103", Line: wire.Line, Msg: "导线属性区未闭合——缺少 ]"})
	return i, false
}

// parseSlot 解析槽——§ 'R_X' : 容量（≥1）。
func parseSlot(first Token, s []Token, doc *Doc) {
	if len(s) < 3 || s[1].Kind != KindColon || s[2].Kind != KindNum {
		doc.Diags = append(doc.Diags, Diag{Code: "E104", Line: first.Line, Msg: "槽格式: § 'R_X' : 容量"})
		return
	}
	n, err := strconv.Atoi(s[2].Value)
	if err != nil || n < 1 {
		doc.Diags = append(doc.Diags, Diag{Code: "E104", Line: s[2].Line, Msg: "槽容量必须是正整数"})
		return
	}
	doc.Slots = append(doc.Slots, Slot{Name: first.Value, Capacity: n, Line: first.Line})
}

// parseFrames 解析采样间隔——[N]，取首个 Num。
func parseFrames(s []Token, start int) (frames, next int, ok bool) {
	for i := start + 1; i < len(s); i++ {
		switch s[i].Kind {
		case KindParamClose:
			return 0, 0, false
		case KindNum:
			n, err := strconv.Atoi(s[i].Value)
			if err != nil {
				return 0, 0, false
			}
			for j := i + 1; j < len(s); j++ {
				if s[j].Kind == KindParamClose {
					return n, j + 1, true
				}
			}
			return 0, 0, false
		}
	}
	return 0, 0, false
}

// collectArgs 收集参数区——Str 还原为 "原文"，Num / Word 拼原文，Sep 切分。
func collectArgs(s []Token, start int) (args []string, next int, ok bool) {
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			args = append(args, cur.String())
			cur.Reset()
		}
	}
	for i := start + 1; i < len(s); i++ {
		switch s[i].Kind {
		case KindParamClose:
			flush()
			return args, i + 1, true
		case KindSep:
			flush()
		case KindStr:
			cur.WriteByte('"')
			cur.WriteString(s[i].Value)
			cur.WriteByte('"')
		case KindNum, KindWord:
			cur.WriteString(s[i].Value)
		default:
			return nil, 0, false
		}
	}
	return nil, 0, false
}

// containsString 报告切片是否含目标串。
func containsString(items []string, target string) bool {
	for _, it := range items {
		if it == target {
			return true
		}
	}
	return false
}
