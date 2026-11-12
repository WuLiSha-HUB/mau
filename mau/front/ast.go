// Package front 是 .mau 语料的前端：词法（Lexer）、语法（Parser）与四柱 AST。
//
// 语法口径对齐 C# 版 MauLexerV3 / MauParserV3（v3-default 外观表）：
// 单元类型由名前缀推导，零单元关键字——S_ 状态机 / P_ 传感器 / T_ 导线 / R_ 槽。
// 本包只做词法与语法；语义门禁（E2xx / E3xx / E4xx）归 validator（P1b-2）。
//
// 参数片段口径：BrickArgs 存语料原文片段——字符串保留双引号（"TOOL"）、
// 盒引用保留 @ 前缀（@rdOffice）、数字保留原文。引号是刻意保留的：
// 它区分字符串字面量与盒引用（同 C# CollectArgs 口径）。
package front

// Doc 是一份 .mau 语料解析后的四柱 IR——对应 C# MauDocV3。
type Doc struct {
	StateMachines []StateMachine
	Sensors       []Sensor
	Wires         []Wire
	Slots         []Slot
	Diags         []Diag
}

// Success 报告解析全程零诊断。
func (d *Doc) Success() bool {
	return len(d.Diags) == 0
}

// Diag 是一条诊断——词法 E001–E004 / 语法 E100–E105。
type Diag struct {
	Code string
	Line int
	Msg  string
}

// StateMachine 是状态机单元——§ 'S_X' = { 'A', 'B' }。
type StateMachine struct {
	Name   string
	States []string
	Line   int
}

// Sensor 是传感器单元——§ 'P_X' ⇐ / ⇚ / ↻。
type Sensor struct {
	Name          string
	Passive       bool
	IsCmd         bool
	CmdKey        string
	EveryFrames   int
	BrickName     string
	BrickArgs     []string
	CaptureTarget string
	TrueActions   []SensorAction
	FalseActions  []SensorAction
	Line          int
}

// SensorAction 是传感器分支上的一次积木调用。
type SensorAction struct {
	BrickName string
	BrickArgs []string
}

// Wire 是导线单元——§ 'T_X' [t=N, par] : 条件 → 动作 | 结果。
type Wire struct {
	Name          string
	Timeout       int
	Parallel      bool
	Logging       bool
	Conditions    []Condition
	BrickName     string // 单动作兼容字段：Actions[0] 的镜像
	BrickArgs     []string
	CaptureTarget string
	Actions       []WireAction
	Results       []Result
	Line          int
}

// WireAction 是导线动作区的一次积木调用。
type WireAction struct {
	BrickName     string
	BrickArgs     []string
	CaptureTarget string
	Line          int
}

// Condition 是导线条件区的一项——状态断言 / 盒断言 / 传感器名。
type Condition struct {
	IsStateAssert bool
	IsBoxAssert   bool
	BoxName       string
	SensorName    string
	StateName     string
	StateValue    string
}

// Result 是导线结果区的一项目标状态。
type Result struct {
	StateName  string
	StateValue string
}

// Slot 是槽单元——§ 'R_X' : 容量。
type Slot struct {
	Name     string
	Capacity int
	Line     int
}
