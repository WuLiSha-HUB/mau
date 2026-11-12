package main

// checks_json —— config / snapshot / logs / 错误面。

import (
	"fmt"
	"strings"
	"time"
)

// configFields 是配置项字段（C# 匿名对象与 Go struct 的同序九字段）。
var configFields = []string{"key", "value", "source", "declared", "file", "default", "writable", "desc", "emptyDesc"}

// checkConfig 对拍 GET /api/v1/config（同数据目录 → 同值同结构）。
func checkConfig(rep *report, opts options, goSide, csSide *sideState) {
	const cls = "config"
	goRes, csRes := fetchBoth(opts.outDir, "config.json", goSide, csSide, "/api/v1/config", 10*time.Second)
	if !require200(rep, cls, "GET /api/v1/config", goRes, csRes) {
		return
	}
	goV := parseSideJSON(rep, cls, "cat5", goRes)
	csV := parseSideJSON(rep, cls, "cs", csRes)
	if goV == nil || csV == nil {
		return
	}
	// version
	gVer, _ := goV.get("version").intVal()
	cVer, _ := csV.get("version").intVal()
	if gVer == cVer {
		rep.add(cls, "version", stPass, fmt.Sprintf("%d/%d", gVer, cVer))
	} else {
		rep.add(cls, "version", stDiff, fmt.Sprintf("%d/%d", gVer, cVer))
	}
	goItems := goV.get("items")
	csItems := csV.get("items")
	if goItems == nil || csItems == nil || goItems.kind != 'a' || csItems.kind != 'a' {
		rep.add(cls, "items 段", stFail, "缺失或非数组")
		return
	}
	gN, cN := len(goItems.arr), len(csItems.arr)
	if gN == cN {
		rep.add(cls, "条目数", stPass, fmt.Sprintf("%d 项", gN))
	} else {
		rep.add(cls, "条目数", stDiff, fmt.Sprintf("cat5=%d cs=%d", gN, cN))
	}
	if gN == 44 && cN == 44 {
		rep.add(cls, "条目数=44（判据）", stPass, "44/44")
	} else {
		rep.add(cls, "条目数=44（判据）", stDiff, fmt.Sprintf("cat5=%d cs=%d（schema 现行 44）", gN, cN))
	}
	// 键序 + 逐项九字段
	gKeys := make([]string, 0, gN)
	cKeys := make([]string, 0, cN)
	for _, it := range goItems.arr {
		if it.kind == 'o' && len(it.keys) > 0 {
			gKeys = append(gKeys, it.get("key").str)
		}
	}
	for _, it := range csItems.arr {
		if it.kind == 'o' && len(it.keys) > 0 {
			cKeys = append(cKeys, it.get("key").str)
		}
	}
	if sameKeys(gKeys, cKeys) {
		rep.add(cls, "键集合与顺序", stPass, fmt.Sprintf("%d 键", len(gKeys)))
	} else {
		rep.add(cls, "键集合与顺序", stDiff, "首个不同: "+firstKeyDiff(gKeys, cKeys))
	}
	mismatch := 0
	notes := make([]string, 0, 6)
	for i := 0; i < gN && i < cN; i++ {
		gIt, cIt := goItems.arr[i], csItems.arr[i]
		if !sameKeys(objectKeys(gIt), objectKeys(cIt)) {
			mismatch++
			if len(notes) < 6 {
				notes = append(notes, fmt.Sprintf("[%d] 字段序 %s != %s", i, keysText(objectKeys(gIt)), keysText(objectKeys(cIt))))
			}
			continue
		}
		for _, f := range configFields {
			if !jsonEqual(gIt.get(f), cIt.get(f)) {
				mismatch++
				if len(notes) < 6 {
					notes = append(notes, fmt.Sprintf("[%s].%s: %s != %s", gIt.get("key").str, f, shortVal(gIt.get(f)), shortVal(cIt.get(f))))
				}
				break
			}
		}
	}
	if mismatch == 0 {
		rep.add(cls, "逐项九字段", stPass, fmt.Sprintf("%d 项 × 9 字段全等", gN))
	} else {
		rep.add(cls, "逐项九字段", stDiff, fmt.Sprintf("%d 项不一致；%s", mismatch, strings.Join(notes, "；")))
	}
}

// firstKeyDiff 描述两组键的首个差异。
func firstKeyDiff(a, b []string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return fmt.Sprintf("第 %d 键 %q != %q", i, a[i], b[i])
		}
	}
	return fmt.Sprintf("长度 %d != %d", len(a), len(b))
}

// shortVal 值短文本。
func shortVal(v *jval) string {
	if v == nil {
		return "missing"
	}
	if v.kind == 's' {
		return clip(v.str)
	}
	return clip(dumpJ(v))
}

// checkSnapshot 对拍 GET /api/v1/snapshot（含 ?logs=5）。
func checkSnapshot(rep *report, opts options, goSide, csSide *sideState) {
	const cls = "snapshot"
	goRes, csRes := fetchBoth(opts.outDir, "snapshot.json", goSide, csSide, "/api/v1/snapshot", 10*time.Second)
	if !require200(rep, cls, "GET /api/v1/snapshot", goRes, csRes) {
		return
	}
	goV := parseSideJSON(rep, cls, "cat5", goRes)
	csV := parseSideJSON(rep, cls, "cs", csRes)
	if goV == nil || csV == nil {
		return
	}
	saveNormalized(opts.outDir, "cat5", "snapshot", normalizeJ(goV, nil))
	saveNormalized(opts.outDir, "cs", "snapshot", normalizeJ(csV, nil))

	// 顶层字段与顺序
	gKeys := objectKeys(goV)
	cKeys := objectKeys(csV)
	if sameKeys(gKeys, cKeys) {
		rep.add(cls, "顶层字段与顺序", stPass, keysText(gKeys))
	} else {
		rep.add(cls, "顶层字段与顺序", stDiff, "cat5="+keysText(gKeys)+" cs="+keysText(cKeys))
	}
	// version / pid / frame
	gVer, _ := goV.get("version").intVal()
	cVer, _ := csV.get("version").intVal()
	if gVer == 3 && cVer == 3 {
		rep.add(cls, "version", stPass, "3/3")
	} else {
		rep.add(cls, "version", stDiff, fmt.Sprintf("%d/%d", gVer, cVer))
	}
	if gPid, ok1 := goV.get("pid").intVal(); ok1 && gPid > 0 {
		if cPid, ok2 := csV.get("pid").intVal(); ok2 && cPid > 0 {
			rep.add(cls, "pid", stPass, fmt.Sprintf("%d/%d", gPid, cPid))
		} else {
			rep.add(cls, "pid", stFail, "cs 非正数")
		}
	} else {
		rep.add(cls, "pid", stFail, "cat5 非正数")
	}
	if gFrame, ok1 := goV.get("frame").intVal(); ok1 {
		if cFrame, ok2 := csV.get("frame").intVal(); ok2 {
			rep.add(cls, "frame", stPass, fmt.Sprintf("%d/%d", gFrame, cFrame))
		} else {
			rep.add(cls, "frame", stFail, "cs 非数字")
		}
	} else {
		rep.add(cls, "frame", stFail, "cat5 非数字")
	}
	// 段类型 + 已知差额计数（cats / boxes 已接线：结构判 PASS/DIFF，数量差异记 KNOWN）
	sectionKinds(rep, cls, goV, csV, "cats", "array", "Flow 注册表数量（Go 逐组推进中：已注册组 vs C# 24 Flow）")
	checkCatsShape(rep, cls, goV, csV)
	sectionKinds(rep, cls, goV, csV, "sessions", "array", "会话层（P3-b 未接）")
	sectionKinds(rep, cls, goV, csV, "tools", "array", "工具注册表（P3-b 未接）")
	checkToolsItems(rep, cls, goV, csV)
	sectionKinds(rep, cls, goV, csV, "boxes", "array", "DataBox 数量（Go 侧盒子面随组注册增长）")
	checkBoxesShape(rep, cls, goV, csV)
	// oa 四态
	goOA, csOA := goV.get("oa"), csV.get("oa")
	wantOA := []string{"open", "work", "closed", "timeout"}
	if goOA != nil && csOA != nil && sameKeys(objectKeys(goOA), wantOA) && sameKeys(objectKeys(csOA), wantOA) {
		allZero := true
		equal := true
		for _, k := range wantOA {
			if !jsonEqual(goOA.get(k), csOA.get(k)) {
				equal = false
			}
			if n, ok := goOA.get(k).intVal(); !ok || n != 0 {
				allZero = false
			}
			if n, ok := csOA.get(k).intVal(); !ok || n != 0 {
				allZero = false
			}
		}
		if equal {
			rep.add(cls, "oa 四态", stPass, "open/work/closed/timeout 同值")
		} else if allZero {
			rep.add(cls, "oa 四态", stPass, "全零")
		} else {
			rep.add(cls, "oa 四态", stKnown, "工单计数差异（P3-b 未接 OA 消费面）")
		}
	} else {
		rep.add(cls, "oa 四态", stDiff, "字段缺失或键序不符")
	}
	// boot / deps：归一化后深比较
	goNorm := normalizeJ(goV, nil)
	csNorm := normalizeJ(csV, nil)
	compareSection(rep, cls, "boot", goNorm.get("boot"), csNorm.get("boot"))
	compareSection(rep, cls, "deps", goNorm.get("deps"), csNorm.get("deps"))
	// res 形状
	goResSec, csResSec := goV.get("res"), csV.get("res")
	wantRes := []string{"t", "proc", "py", "node", "gpu", "loop"}
	if goResSec != nil && csResSec != nil && sameKeys(objectKeys(goResSec), wantRes) && sameKeys(objectKeys(csResSec), wantRes) {
		wantProc := []string{"ws", "private", "heap", "committed", "g0", "g1", "g2", "loh"}
		if sameKeys(objectKeys(goResSec.get("proc")), wantProc) && sameKeys(objectKeys(csResSec.get("proc")), wantProc) {
			rep.add(cls, "res 段形状", stPass, "t/proc/py/node/gpu/loop + proc 八项")
		} else {
			rep.add(cls, "res 段形状", stDiff, "proc 键序不符")
		}
	} else {
		rep.add(cls, "res 段形状", stDiff, "res 键序不符")
	}
	rep.add(cls, "res 动态数值", stKnown, "工作集/堆/GPU/loop 天然不同（Go 无分代堆、无主循环；见 §3.3）")
	// ?logs=5
	checkSnapshotLogs(rep, opts, goSide, csSide, goV, csV)
	rep.add(cls, "sessions 内容", stKnown, "会话状态段只比类型与计数（P3-b 剩余面）")
}

// checkToolsItems 工具段逐项比较——name / group / builtin 三字段全等。
func checkToolsItems(rep *report, cls string, goV, csV *jval) {
	g, c := goV.get("tools"), csV.get("tools")
	if g == nil || c == nil || g.kind != 'a' || c.kind != 'a' {
		return
	}
	if len(g.arr) != len(c.arr) {
		rep.add(cls, "tools 逐项", stDiff, fmt.Sprintf("计数 cat5=%d cs=%d", len(g.arr), len(c.arr)))
		return
	}
	mismatch := 0
	first := ""
	for i := 0; i < len(g.arr); i++ {
		if jsonEqual(g.arr[i], c.arr[i]) {
			continue
		}
		mismatch++
		if first == "" {
			first = fmt.Sprintf("[%d] cat5=%s cs=%s", i, dumpJ(g.arr[i]), dumpJ(c.arr[i]))
		}
	}
	if mismatch == 0 {
		rep.add(cls, "tools 逐项", stPass, fmt.Sprintf("%d 项 × name/group/builtin 全等", len(g.arr)))
		return
	}
	rep.add(cls, "tools 逐项", stDiff, fmt.Sprintf("%d 项不一致；首差 %s", mismatch, first))
}

// catsEntryFields 是 cats 条目字段序（C# AppendFlowJson 匿名对象插入序）。
var catsEntryFields = []string{"name", "id", "type", "kind", "faulted", "faultReason", "status", "desc"}

// catStatusFields 是 status 四柱字段序。
var catStatusFields = []string{"stateLines", "sensors", "slots", "wires"}

// catWireFields 是导线条目字段序。
var catWireFields = []string{"name", "busy", "lastTriggerFrame", "timedOut"}

// catSensorFields 是传感器条目字段序（两侧恒空数组；非空时按此校验）。
var catSensorFields = []string{"name", "value"}

// catSlotFields 是槽位条目字段序。
var catSlotFields = []string{"name", "available", "capacity"}

// checkCatsShape 校验 cats 段结构——条目字段序 / 类型 / status 四柱子结构。
//
// 数量差异不判 DIFF：Go 侧逐组推进中（2 组 vs C# 24 Flow），数量由 sectionKinds 记 KNOWN。
func checkCatsShape(rep *report, cls string, goV, csV *jval) {
	g, c := goV.get("cats"), csV.get("cats")
	if g == nil || c == nil || g.kind != 'a' || c.kind != 'a' {
		rep.add(cls, "cats 条目结构", stDiff, "段缺失或非数组")
		return
	}
	problems := make([]string, 0, 6)
	checkSide := func(side string, arr []*jval) {
		for i, it := range arr {
			if it == nil || it.kind != 'o' || !sameKeys(objectKeys(it), catsEntryFields) {
				problems = append(problems, fmt.Sprintf("%s[%d] 条目字段序不符", side, i))
				continue
			}
			if _, ok := it.get("id").intVal(); !ok {
				problems = append(problems, fmt.Sprintf("%s[%d].id 非数字", side, i))
			}
			if it.get("faulted").kind != 'b' {
				problems = append(problems, fmt.Sprintf("%s[%d].faulted 非布尔", side, i))
			}
			problems = append(problems, checkCatStatus(it.get("status"), side, i)...)
		}
	}
	checkSide("cat5", g.arr)
	checkSide("cs", c.arr)
	if len(problems) == 0 {
		rep.add(cls, "cats 条目结构", stPass, fmt.Sprintf("cat5=%d · cs=%d 条 × 八字段 + status 四柱（含导线四字段）", len(g.arr), len(c.arr)))
		return
	}
	if len(problems) > 6 {
		problems = problems[:6]
	}
	rep.add(cls, "cats 条目结构", stDiff, strings.Join(problems, "；"))
}

// checkCatStatus 校验单条 status 四柱子结构，返回问题清单。
func checkCatStatus(status *jval, side string, idx int) []string {
	if status == nil || status.kind != 'o' || !sameKeys(objectKeys(status), catStatusFields) {
		return []string{fmt.Sprintf("%s[%d].status 字段序不符", side, idx)}
	}
	problems := make([]string, 0, 2)
	for _, f := range catStatusFields {
		if sec := status.get(f); sec == nil || sec.kind != 'a' {
			problems = append(problems, fmt.Sprintf("%s[%d].status.%s 非数组", side, idx, f))
		}
	}
	if len(problems) > 0 {
		return problems
	}
	for j, s := range status.get("sensors").arr {
		if s == nil || s.kind != 'o' || !sameKeys(objectKeys(s), catSensorFields) {
			problems = append(problems, fmt.Sprintf("%s[%d].sensors[%d] 字段序不符", side, idx, j))
			break
		}
	}
	for j, s := range status.get("slots").arr {
		if s == nil || s.kind != 'o' || !sameKeys(objectKeys(s), catSlotFields) {
			problems = append(problems, fmt.Sprintf("%s[%d].slots[%d] 字段序不符", side, idx, j))
			break
		}
	}
	for j, w := range status.get("wires").arr {
		if w == nil || w.kind != 'o' || !sameKeys(objectKeys(w), catWireFields) {
			problems = append(problems, fmt.Sprintf("%s[%d].wires[%d] 字段序不符", side, idx, j))
			break
		}
		if w.get("busy").kind != 'b' {
			problems = append(problems, fmt.Sprintf("%s[%d].wires[%d].busy 非布尔", side, idx, j))
			break
		}
	}
	return problems
}

// boxFields 是 boxes 条目字段序（C# BuildBoxEntry 匿名对象插入序）。
var boxFields = []string{"scope", "key", "t", "value"}

// checkBoxesShape 校验 boxes 段结构——条目字段序 / t 标记取值 / value 类型与 t 一致。
//
// 数量差异不判 DIFF：Go 侧盒子面随组注册增长（self_desc / *Open 盒），由 sectionKinds 记 KNOWN。
func checkBoxesShape(rep *report, cls string, goV, csV *jval) {
	g, c := goV.get("boxes"), csV.get("boxes")
	if g == nil || c == nil || g.kind != 'a' || c.kind != 'a' {
		rep.add(cls, "boxes 条目结构", stDiff, "段缺失或非数组")
		return
	}
	problems := make([]string, 0, 6)
	checkSide := func(side string, arr []*jval) {
		for i, it := range arr {
			if it == nil || it.kind != 'o' || !sameKeys(objectKeys(it), boxFields) {
				problems = append(problems, fmt.Sprintf("%s[%d] 条目字段序不符", side, i))
				continue
			}
			t := it.get("t").str
			value := it.get("value")
			wantKind := byte('s')
			switch t {
			case "b":
				wantKind = 'b'
			case "n":
				wantKind = 'n'
			case "s", "o":
				wantKind = 's'
			default:
				problems = append(problems, fmt.Sprintf("%s[%d].t 非法值 %q", side, i, t))
				continue
			}
			if value == nil || value.kind != wantKind {
				problems = append(problems, fmt.Sprintf("%s[%d] t=%s 值类型 %s 不符", side, i, t, kindOf(value)))
			}
		}
	}
	checkSide("cat5", g.arr)
	checkSide("cs", c.arr)
	if len(problems) == 0 {
		rep.add(cls, "boxes 条目结构", stPass, fmt.Sprintf("cat5=%d · cs=%d 条 × 四字段（t 与 value 类型一致）", len(g.arr), len(c.arr)))
		return
	}
	if len(problems) > 6 {
		problems = problems[:6]
	}
	rep.add(cls, "boxes 条目结构", stDiff, strings.Join(problems, "；"))
}

// sectionKinds 检查段类型并按已知差额报告。
func sectionKinds(rep *report, cls string, goV, csV *jval, name, wantKind, reason string) {
	g, c := goV.get(name), csV.get(name)
	if g == nil || c == nil || g.kindName() != wantKind || c.kindName() != wantKind {
		rep.add(cls, name+" 段", stDiff, "类型不符 "+kindOf(g)+"/"+kindOf(c))
		return
	}
	gN, cN := len(g.arr), len(c.arr)
	if gN == cN {
		rep.add(cls, name+" 段", stPass, fmt.Sprintf("%s × %d", wantKind, gN))
	} else {
		rep.add(cls, name+" 段", stKnown, fmt.Sprintf("cat5=%d cs=%d —— %s", gN, cN, reason))
	}
}

// compareSection 归一化后深比较一个段。
func compareSection(rep *report, cls, name string, g, c *jval) {
	if jsonEqual(g, c) {
		rep.add(cls, name+" 段（归一化）", stPass, "结构 / 值全等")
		return
	}
	rep.add(cls, name+" 段（归一化）", stDiff, firstDiff(g, c, name))
}

// checkSnapshotLogs 检查 ?logs=5 的日志段。
func checkSnapshotLogs(rep *report, opts options, goSide, csSide *sideState, goSnap, csSnap *jval) {
	const cls = "snapshot"
	goRes, csRes := fetchBoth(opts.outDir, "snapshot_logs5.json", goSide, csSide, "/api/v1/snapshot?logs=5", 10*time.Second)
	if !require200(rep, cls, "GET /api/v1/snapshot?logs=5", goRes, csRes) {
		return
	}
	goV := parseSideJSON(rep, cls, "cat5", goRes)
	csV := parseSideJSON(rep, cls, "cs", csRes)
	if goV == nil || csV == nil {
		return
	}
	goLogs, csLogs := goV.get("logs"), csV.get("logs")
	if goLogs == nil || csLogs == nil || goLogs.kind != 'a' || csLogs.kind != 'a' {
		rep.add(cls, "?logs=N 替换", stFail, "logs 段缺失或非数组")
		return
	}
	if len(goLogs.arr) == 5 && len(csLogs.arr) == 5 {
		rep.add(cls, "?logs=N 替换（N=5）", stPass, "5/5 条")
	} else {
		gN, cN := len(goLogs.arr), len(csLogs.arr)
		if gN > 0 && cN > 0 && gN <= 5 && cN <= 5 {
			rep.add(cls, "?logs=N 替换（N=5）", stKnown, fmt.Sprintf("cat5=%d cs=%d（≤5）—— C# 内存总账含 skipDisk trace 条目；Snapshot(N) 先取 N 条原始再过滤 trace，N=5 可不足 5；两侧算法同构（P3-b 接 FlowRunner 后自然一致）", gN, cN))
		} else {
			rep.add(cls, "?logs=N 替换（N=5）", stDiff, fmt.Sprintf("cat5=%d cs=%d", gN, cN))
		}
	}
	want := []string{"time", "frame", "level", "category", "module", "message"}
	if shapeLogItems(goLogs, want) && shapeLogItems(csLogs, want) {
		rep.add(cls, "快照日志字段（六字段）", stPass, keysText(want))
	} else {
		rep.add(cls, "快照日志字段（六字段）", stDiff, "字段序或类型不符")
	}
}

// shapeLogItems 校验日志数组每项字段序与基本类型。
func shapeLogItems(logs *jval, want []string) bool {
	if logs == nil || logs.kind != 'a' {
		return false
	}
	for _, it := range logs.arr {
		if it.kind != 'o' || !sameKeys(objectKeys(it), want) {
			return false
		}
		for _, f := range want {
			if it.get(f) == nil {
				return false
			}
		}
		if _, ok := it.get("frame").intVal(); !ok {
			return false
		}
		if !timePattern.MatchString(it.get("time").str) {
			return false
		}
	}
	return true
}

// checkLogs 对拍 GET /api/v1/logs（七字段 + 过滤）。
func checkLogs(rep *report, opts options, goSide, csSide *sideState) {
	const cls = "logs"
	goRes, csRes := fetchBoth(opts.outDir, "logs_n5.json", goSide, csSide, "/api/v1/logs?n=5", 10*time.Second)
	if !require200(rep, cls, "GET /api/v1/logs?n=5", goRes, csRes) {
		return
	}
	goV := parseSideJSON(rep, cls, "cat5", goRes)
	csV := parseSideJSON(rep, cls, "cs", csRes)
	if goV == nil || csV == nil {
		return
	}
	gVer, _ := goV.get("version").intVal()
	cVer, _ := csV.get("version").intVal()
	if gVer == 1 && cVer == 1 {
		rep.add(cls, "version", stPass, "1/1")
	} else {
		rep.add(cls, "version", stDiff, fmt.Sprintf("%d/%d", gVer, cVer))
	}
	goLogs, csLogs := goV.get("logs"), csV.get("logs")
	want := []string{"time", "frame", "level", "category", "module", "type", "message"}
	if shapeLogItems(goLogs, want) && shapeLogItems(csLogs, want) {
		rep.add(cls, "日志字段（七字段）", stPass, keysText(want))
	} else {
		rep.add(cls, "日志字段（七字段）", stDiff, "字段序或类型不符")
	}
	if n := logCount(goLogs); n > 0 && n <= 5 {
		if m := logCount(csLogs); m > 0 && m <= 5 {
			rep.add(cls, "条数上限", stPass, fmt.Sprintf("%d/%d（≤5）", n, m))
		} else {
			rep.add(cls, "条数上限", stFail, fmt.Sprintf("cs=%d", m))
		}
	} else {
		rep.add(cls, "条数上限", stFail, fmt.Sprintf("cat5=%d", n))
	}
	// cat=SYS 过滤
	goRes2, csRes2 := fetchBoth(opts.outDir, "logs_sys.json", goSide, csSide, "/api/v1/logs?n=5&cat=SYS", 10*time.Second)
	if require200(rep, cls, "GET /api/v1/logs?cat=SYS", goRes2, csRes2) {
		if gV := parseSideJSON(rep, cls, "cat5", goRes2); gV != nil {
			if cV := parseSideJSON(rep, cls, "cs", csRes2); cV != nil {
				if allCategory(gV.get("logs"), "SYS") && allCategory(cV.get("logs"), "SYS") {
					rep.add(cls, "cat 过滤", stPass, "两侧均为 SYS")
				} else {
					rep.add(cls, "cat 过滤", stDiff, "出现非 SYS 条目")
				}
			}
		}
	}
}

// logCount 取日志条数。
func logCount(v *jval) int {
	if v == nil || v.kind != 'a' {
		return -1
	}
	return len(v.arr)
}

// allCategory 校验日志数组的 category 全为给定值（空数组视为真）。
func allCategory(v *jval, cat string) bool {
	if v == nil || v.kind != 'a' {
		return false
	}
	for _, it := range v.arr {
		c := it.get("category")
		if c == nil || c.str != cat {
			return false
		}
	}
	return true
}

// checkErrorFace 对拍非法参数面。
func checkErrorFace(rep *report, opts options, goSide, csSide *sideState) {
	const cls = "error"
	checkLogsLimit(rep, opts, goSide, csSide, "logs?n=abc（非法→默认）", "/api/v1/logs?n=abc", 200)
	checkLogsLimit(rep, opts, goSide, csSide, "logs?n=99999（上限夹取）", "/api/v1/logs?n=99999", 2000)
	checkLogsLimit(rep, opts, goSide, csSide, "logs?n=-5（负数→默认）", "/api/v1/logs?n=-5", 200)
	checkSnapshotLimit(rep, opts, goSide, csSide, "snapshot?logs=abc（非法→默认）", "/api/v1/snapshot?logs=abc", 200)
	checkSnapshotLimit(rep, opts, goSide, csSide, "snapshot?logs=99999（上限夹取）", "/api/v1/snapshot?logs=99999", 2000)
	// POST 非法 body——两侧都应 ok=false（占位面差异记 KNOWN）
	postPair(rep, opts, goSide, csSide, "POST /api/v1/command（非 JSON）", "/api/v1/command", "not-json", "P3-b 占位：两侧 ok=false，错误文案不同")
	postPair(rep, opts, goSide, csSide, "POST /api/v1/config（非 JSON）", "/api/v1/config", "not-json", "P3-d 占位：两侧 ok=false，校验文案不同")
}

// checkLogsLimit 校验日志条数上限行为。
func checkLogsLimit(rep *report, opts options, goSide, csSide *sideState, item, path string, limit int) {
	const cls = "error"
	goRes, csRes := fetchBoth(opts.outDir, "err_"+slug(item)+".json", goSide, csSide, path, 10*time.Second)
	if !require200(rep, cls, item, goRes, csRes) {
		return
	}
	gN, cN := -1, -1
	if gV := parseSideJSON(rep, cls, "cat5", goRes); gV != nil {
		gN = logCount(gV.get("logs"))
	}
	if cV := parseSideJSON(rep, cls, "cs", csRes); cV != nil {
		cN = logCount(cV.get("logs"))
	}
	if gN < 0 || cN < 0 {
		rep.add(cls, item, stFail, "logs 段缺失")
		return
	}
	if gN <= limit && cN <= limit {
		rep.add(cls, item, stPass, fmt.Sprintf("%d/%d（≤%d）", gN, cN, limit))
	} else {
		rep.add(cls, item, stFail, fmt.Sprintf("%d/%d（上限 %d）", gN, cN, limit))
	}
}

// checkSnapshotLimit 校验快照日志条数上限行为。
func checkSnapshotLimit(rep *report, opts options, goSide, csSide *sideState, item, path string, limit int) {
	const cls = "error"
	goRes, csRes := fetchBoth(opts.outDir, "err_"+slug(item)+".json", goSide, csSide, path, 10*time.Second)
	if !require200(rep, cls, item, goRes, csRes) {
		return
	}
	gN, cN := -1, -1
	if gV := parseSideJSON(rep, cls, "cat5", goRes); gV != nil {
		gN = logCount(gV.get("logs"))
	}
	if cV := parseSideJSON(rep, cls, "cs", csRes); cV != nil {
		cN = logCount(cV.get("logs"))
	}
	if gN < 0 || cN < 0 {
		rep.add(cls, item, stFail, "logs 段缺失")
		return
	}
	if gN <= limit && cN <= limit {
		rep.add(cls, item, stPass, fmt.Sprintf("%d/%d（≤%d）", gN, cN, limit))
	} else {
		rep.add(cls, item, stFail, fmt.Sprintf("%d/%d（上限 %d）", gN, cN, limit))
	}
}

// postPair 对拍 POST 非法 body 的错误面。
func postPair(rep *report, opts options, goSide, csSide *sideState, item, path, body, knownNote string) {
	const cls = "error"
	goRes := doPost(goSide.base, path, body, "application/json", 10*time.Second)
	csRes := doPost(csSide.base, path, body, "application/json", 10*time.Second)
	saveRaw(opts.outDir, "cat5", "err_"+slug(item)+".json", goRes)
	saveRaw(opts.outDir, "cs", "err_"+slug(item)+".json", csRes)
	if !goRes.ok() || !csRes.ok() || goRes.status != 200 || csRes.status != 200 {
		rep.add(cls, item, stFail, statusPair(goRes, csRes))
		return
	}
	gV := parseSideJSON(rep, cls, "cat5", goRes)
	cV := parseSideJSON(rep, cls, "cs", csRes)
	if gV == nil || cV == nil {
		return
	}
	gOk, cOk := gV.get("ok"), cV.get("ok")
	if gOk != nil && cOk != nil && gOk.kind == 'b' && cOk.kind == 'b' && !gOk.boolean && !cOk.boolean {
		rep.add(cls, item, stKnown, "两侧 ok=false —— "+knownNote)
	} else {
		rep.add(cls, item, stDiff, "ok 字段不一致")
	}
}

// slug 把检查项文本转为文件名片段。
func slug(s string) string {
	r := strings.NewReplacer(" ", "_", "/", "_", "?", "_", "=", "_", "（", "_", "）", "_", "→", "_")
	return r.Replace(s)
}
