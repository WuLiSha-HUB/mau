package front

import (
	"strconv"
	"strings"
)

// Validate 做 IR 级语义校验——名称唯一 / 引用完整 / 积木与盒子规则。
//
// 诊断码：E200 重名 · E201 传感器引用不存在 · E202 状态机引用不存在 ·
// E203 状态值不存在 · E204 主动传感器积木缺失 · E206 Command key 重名 ·
// E400 未知积木 · E401 参数数量 · E402 参数类别 / 盒子类型 · E405 无输出端口 ·
// E406 捕获类型不支持 · E407 读无写源 · E408 重复写源。
//
// bricks 为 nil 时按空索引处理（所有积木引用报 E400）。返回本次新增诊断条数。
func Validate(doc *Doc, bricks *BrickTable) int {
	if doc == nil {
		return 0
	}
	before := len(doc.Diags)
	// [段1] 名称索引 + 唯一性（跨四柱重名 = 构筑期错误）
	kinds := make(map[string]string)
	for _, sm := range doc.StateMachines {
		checkName(sm.Name, "状态机", sm.Line, kinds, doc)
	}
	for _, s := range doc.Sensors {
		checkName(s.Name, "传感器", s.Line, kinds, doc)
	}
	for _, w := range doc.Wires {
		checkName(w.Name, "导线", w.Line, kinds, doc)
	}
	for _, sl := range doc.Slots {
		checkName(sl.Name, "槽", sl.Line, kinds, doc)
	}
	// [段2] 引用完整性 + Command key 唯一性
	checkCmdKeys(doc)
	checkReferences(doc)
	// [段3] 主动传感器积木存在性（解析层已保证非空——双保险）
	for _, s := range doc.Sensors {
		if !s.Passive && s.BrickName == "" {
			doc.Diags = append(doc.Diags, Diag{Code: "E204", Line: s.Line, Msg: "主动传感器 '" + s.Name + "' 缺采样积木"})
		}
	}
	// [段4] 盒子表构建——写源收集（写源唯一 / 类型白名单）
	boxTypes := buildBoxTypes(doc, bricks)
	// [段5] 积木门禁——导线动作 + 主动传感器采样（E4xx）
	checkBricks(doc, bricks, boxTypes)
	// [段6] 盒子规则——读需写源 + 参数类型匹配
	checkBoxes(doc, bricks, boxTypes)
	return len(doc.Diags) - before
}

// checkName 校验单元名唯一——同单元与跨单元都不可重名。
func checkName(name, kind string, line int, kinds map[string]string, doc *Doc) {
	if existing, ok := kinds[name]; ok {
		doc.Diags = append(doc.Diags, Diag{Code: "E200", Line: line, Msg: "名称重名: '" + name + "'——已是" + existing + "（当前声明为" + kind + "）"})
		return
	}
	kinds[name] = kind
}

// checkCmdKeys 校验 Command key 唯一——一 key 一传感器。
func checkCmdKeys(doc *Doc) {
	keys := make(map[string]string)
	for _, s := range doc.Sensors {
		if !s.IsCmd || s.CmdKey == "" {
			continue
		}
		if existing, ok := keys[s.CmdKey]; ok {
			doc.Diags = append(doc.Diags, Diag{Code: "E206", Line: s.Line, Msg: "Command key 重名: '" + s.CmdKey + "'——已属于传感器 '" + existing + "'（当前声明为 '" + s.Name + "'）"})
			continue
		}
		keys[s.CmdKey] = s.Name
	}
}

// checkReferences 校验导线条件 / 结果引用的传感器与状态存在。
func checkReferences(doc *Doc) {
	sensors := make(map[string]bool, len(doc.Sensors))
	for _, s := range doc.Sensors {
		sensors[s.Name] = true
	}
	stateValues := make(map[string]map[string]bool, len(doc.StateMachines))
	for _, sm := range doc.StateMachines {
		values := make(map[string]bool, len(sm.States))
		for _, st := range sm.States {
			values[st] = true
		}
		stateValues[sm.Name] = values
	}
	for _, w := range doc.Wires {
		for _, c := range w.Conditions {
			if c.IsStateAssert {
				checkStateRef(w.Name, w.Line, c.StateName, c.StateValue, stateValues, doc)
				continue
			}
			if c.IsBoxAssert {
				continue
			}
			if !sensors[c.SensorName] {
				doc.Diags = append(doc.Diags, Diag{Code: "E201", Line: w.Line, Msg: "导线 '" + w.Name + "' 条件引用不存在的传感器: '" + c.SensorName + "'"})
			}
		}
		for _, r := range w.Results {
			checkStateRef(w.Name, w.Line, r.StateName, r.StateValue, stateValues, doc)
		}
	}
}

// checkStateRef 校验状态引用——状态机存在 + 状态值在其集合内。
func checkStateRef(wireName string, line int, stateName, stateValue string, stateValues map[string]map[string]bool, doc *Doc) {
	values, ok := stateValues[stateName]
	if !ok {
		doc.Diags = append(doc.Diags, Diag{Code: "E202", Line: line, Msg: "导线 '" + wireName + "' 引用不存在的状态机: '" + stateName + "'"})
		return
	}
	if !values[stateValue] {
		doc.Diags = append(doc.Diags, Diag{Code: "E203", Line: line, Msg: "导线 '" + wireName + "' 引用状态机 '" + stateName + "' 中不存在的状态值: '" + stateValue + "'"})
	}
}

// buildBoxTypes 收集盒子写源——导线捕获（按积木输出类型）+ 主动传感器捕获（bool）。
func buildBoxTypes(doc *Doc, bricks *BrickTable) map[string]string {
	boxTypes := make(map[string]string)
	for _, w := range doc.Wires {
		for _, a := range w.Actions {
			if a.CaptureTarget != "" {
				collectBoxWriter(boxTypes, a.CaptureTarget, a.BrickName, a.Line, bricks, doc)
			}
		}
	}
	for _, s := range doc.Sensors {
		if s.Passive || s.CaptureTarget == "" {
			continue
		}
		if _, ok := boxTypes[s.CaptureTarget]; ok {
			doc.Diags = append(doc.Diags, Diag{Code: "E408", Line: s.Line, Msg: "盒子 '" + s.CaptureTarget + "' 重复写源——一个 Key 恰一个写入点"})
			continue
		}
		boxTypes[s.CaptureTarget] = "bool"
	}
	return boxTypes
}

// collectBoxWriter 登记单写源——重复写 E408 / 无输出端口 E405 / 类型不支持 E406。
func collectBoxWriter(boxTypes map[string]string, key, brickName string, line int, bricks *BrickTable, doc *Doc) {
	if _, ok := boxTypes[key]; ok {
		doc.Diags = append(doc.Diags, Diag{Code: "E408", Line: line, Msg: "盒子 '" + key + "' 重复写源——一个 Key 恰一个写入点（数据全局性铁律）"})
		return
	}
	entry, ok := bricks.Find(brickName)
	if !ok {
		// 未知积木——类型信任 string（E400 由 checkBricks 报）
		boxTypes[key] = "string"
		return
	}
	if len(entry.OutputTypes) < 1 {
		doc.Diags = append(doc.Diags, Diag{Code: "E405", Line: line, Msg: "捕获目标 '" + key + "' 但积木 '" + brickName + "' 无输出端口"})
		return
	}
	captureType := entry.OutputTypes[0]
	if captureType == "" {
		captureType = "string"
	}
	switch captureType {
	case "string", "long", "int", "bool":
		boxTypes[key] = captureType
	default:
		doc.Diags = append(doc.Diags, Diag{Code: "E406", Line: line, Msg: "捕获类型 '" + captureType + "' 不支持——白名单 string/long/int/bool"})
	}
}

// checkBricks 校验积木调用——导线动作 + 主动传感器采样与分支动作。
func checkBricks(doc *Doc, bricks *BrickTable, boxTypes map[string]string) {
	for _, w := range doc.Wires {
		for _, a := range w.Actions {
			checkBrickCall(a.BrickName, a.BrickArgs, a.Line, bricks, boxTypes, doc)
		}
	}
	for _, s := range doc.Sensors {
		if !s.Passive && s.BrickName != "" {
			checkBrickCall(s.BrickName, s.BrickArgs, s.Line, bricks, boxTypes, doc)
		}
		for _, a := range s.TrueActions {
			checkBrickCall(a.BrickName, a.BrickArgs, s.Line, bricks, boxTypes, doc)
		}
		for _, a := range s.FalseActions {
			checkBrickCall(a.BrickName, a.BrickArgs, s.Line, bricks, boxTypes, doc)
		}
	}
}

// checkBrickCall 校验单次积木调用——E400 未知 / E401 参数数量 / E402 参数类别。
func checkBrickCall(name string, args []string, line int, bricks *BrickTable, boxTypes map[string]string, doc *Doc) {
	entry, ok := bricks.Find(name)
	if !ok {
		doc.Diags = append(doc.Diags, Diag{Code: "E400", Line: line, Msg: "未知积木: '" + name + "'——不在 Bricks/index.json 索引中"})
		return
	}
	if len(args) != len(entry.InputTypes) {
		doc.Diags = append(doc.Diags, Diag{Code: "E401", Line: line, Msg: "积木 '" + name + "' 参数数量不符——期望 " + itoa(len(entry.InputTypes)) + " 个，实际 " + itoa(len(args)) + " 个"})
		return
	}
	for i, raw := range args {
		trimmed := strings.TrimSpace(raw)
		actualKind := argKind(raw)
		if actualKind == "name" && isBoxRef(boxTypes, trimmed) {
			// 盒子 Key 引用——存在性与类型匹配归 checkBoxes 统一查
			continue
		}
		if actualKind == "name" && trimmed != "" && trimmed[0] != '@' && contractKind(entry.InputTypes[i]) == "str" {
			// 裸词 B1 豁免——全局盒（无 @ 前缀）写源在语料外，类型信任 string
			continue
		}
		expectedKind := contractKind(entry.InputTypes[i])
		if actualKind == "" || actualKind != expectedKind {
			doc.Diags = append(doc.Diags, Diag{Code: "E402", Line: line, Msg: "积木 '" + name + "' 第 " + itoa(i+1) + " 参数类别不符——期望 " + entry.InputTypes[i] + "（" + expectedKind + "），实际 '" + raw + "'（" + actualKind + "）"})
		}
	}
}

// checkBoxes 校验盒子规则——读需写源（E407）+ 参数类型匹配（E402）。
func checkBoxes(doc *Doc, bricks *BrickTable, boxTypes map[string]string) {
	for _, w := range doc.Wires {
		for _, a := range w.Actions {
			checkBoxArgs(a.BrickArgs, a.BrickName, a.Line, bricks, boxTypes, doc)
		}
		for _, c := range w.Conditions {
			if c.IsBoxAssert {
				checkBoxRead(c.BoxName, w.Line, boxTypes, doc)
			}
		}
	}
	for _, s := range doc.Sensors {
		if s.Passive {
			continue
		}
		for _, a := range s.TrueActions {
			checkBoxArgs(a.BrickArgs, a.BrickName, s.Line, bricks, boxTypes, doc)
		}
		for _, a := range s.FalseActions {
			checkBoxArgs(a.BrickArgs, a.BrickName, s.Line, bricks, boxTypes, doc)
		}
	}
}

// checkBoxArgs 校验单调用参数——盒子读需写源 + 类型匹配。
func checkBoxArgs(args []string, brickName string, line int, bricks *BrickTable, boxTypes map[string]string, doc *Doc) {
	entry, ok := bricks.Find(brickName)
	if !ok {
		return
	}
	for i, raw := range args {
		arg := strings.TrimSpace(raw)
		if argKind(arg) != "name" || !isBoxRef(boxTypes, arg) {
			continue
		}
		boxType, ok := boxTypes[arg]
		if !ok {
			// B1 豁免——全局盒（无 @ 前缀）写源在语料外
			if arg == "" || arg[0] == '@' {
				doc.Diags = append(doc.Diags, Diag{Code: "E407", Line: line, Msg: "盒子 '" + arg + "' 读无写源——先有捕获落盒（> @key / > key）才能引用"})
			}
			continue
		}
		inType := ""
		if i < len(entry.InputTypes) {
			inType = entry.InputTypes[i]
		}
		if inType != boxType {
			doc.Diags = append(doc.Diags, Diag{Code: "E402", Line: line, Msg: "盒子 '" + arg + "' 类型 '" + boxType + "' 与积木 '" + brickName + "' 第 " + itoa(i+1) + " 参数类型 '" + inType + "' 不匹配"})
		}
	}
}

// checkBoxRead 校验盒子判真引用——读需写源。
func checkBoxRead(key string, line int, boxTypes map[string]string, doc *Doc) {
	if _, ok := boxTypes[key]; ok {
		return
	}
	// B1 豁免——全局盒（无 @ 前缀）写源在语料外
	if key == "" || key[0] == '@' {
		doc.Diags = append(doc.Diags, Diag{Code: "E407", Line: line, Msg: "盒子 '" + key + "' 读无写源——先有捕获落盒（> @key / > key）才能引用"})
	}
}

// argKind 判定参数原文类别——"..." → str / 纯数字 → num / 其它 → name。
func argKind(raw string) string {
	t := strings.TrimSpace(raw)
	if len(t) >= 2 && t[0] == '"' {
		return "str"
	}
	if isNumberText(t) {
		return "num"
	}
	return "name"
}

// contractKind 把契约类型映射为类别——string → str / int,long,bool → num / string[] → name。
func contractKind(t string) string {
	switch t {
	case "string":
		return "str"
	case "int", "long", "bool":
		return "num"
	case "string[]":
		return "name"
	}
	return ""
}

// isBoxRef 判定参数是否盒子引用——@ 前缀私有盒，或已登记写源的全局盒。
func isBoxRef(boxTypes map[string]string, arg string) bool {
	if arg != "" && arg[0] == '@' {
		return true
	}
	_, ok := boxTypes[arg]
	return ok
}

// itoa 是 strconv.Itoa 的短名。
func itoa(n int) string {
	return strconv.Itoa(n)
}
