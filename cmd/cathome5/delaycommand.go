package main

import (
	"strconv"
	"strings"

	"cathome5/host/session"
)

// 本文件是延迟指令族（delay.*）——逐条对齐 mau:CatHome4/CatHome4.Core/DelayCommand.cs。
//
// 通道：POST /api/v1/command（C# InputRoutes 段7：RouteDelayCommand → DelayCommand.Handle(target.Session.Id)）。
// 分隔符 |：首个 | 前为 head（Trim 后），其后全段为 tail（内容可含 |，无需转义）。
// 结果文本写 DELAY 日志（HTTP 通道只回投递回执——前端列表以 state 段 delay 字段为准）。

// delayMaxSecondsAhead 相对时长上限（秒）——10 年（防溢出；人工登记不设业务上限）。
const delayMaxSecondsAhead = 315360000

// HandleDelay 识别并执行 delay.* 指令——true = 已识别（无论成败）。
//
// 日志口径对齐 C# DelayCommand.Handle：「延迟指令: <行> → <结果>」，ERR| 前缀升 2 级。
func (r *commandRouter) HandleDelay(line string) bool {
	if !strings.HasPrefix(line, "delay.") {
		return false
	}
	result := r.executeDelay(line)
	level := 1
	if strings.HasPrefix(result, "ERR|") {
		level = 2
	}
	r.log(level, "延迟指令: "+line+" → "+result, "DELAY")
	return true
}

// executeDelay 指令分发——前缀识别 + 参数解析（逐条对齐 C# DelayCommand.Execute）。
func (r *commandRouter) executeDelay(line string) string {
	queue := r.delayQueue()
	if queue == nil {
		return "ERR|DELAY_NO_SERVICE|延迟队列未接线（宿主未注入）"
	}
	catKey := r.delayCatKey()
	switch {
	case line == "delay.list":
		return queue.BuildListText(catKey)
	case strings.HasPrefix(line, "delay.add|"):
		return delayAddRelative(queue, catKey, line)
	case strings.HasPrefix(line, "delay.addat|"):
		return delayAddAbsolute(queue, catKey, line)
	case strings.HasPrefix(line, "delay.addatloop|"):
		return delayAddAbsoluteLoop(queue, catKey, line)
	case strings.HasPrefix(line, "delay.set|"):
		return delaySetDue(queue, catKey, line)
	case strings.HasPrefix(line, "delay.cancel|"):
		return delayCancel(queue, catKey, line)
	case strings.HasPrefix(line, "delay.loop|"):
		return delaySetLoop(queue, catKey, line)
	}
	return "ERR|BAD_ARGS|未知延迟指令（支持 delay.add|<秒>|<内容> · delay.addat|<unixms>|<内容> · delay.addatloop|<unixms>|<内容> · delay.set|<id>|<unixms> · delay.cancel|<id> · delay.loop|<id>|<0|1> · delay.list）"
}

// delayAddRelative delay.add|<秒>|<内容>——相对时长登记（0 = 立即；已过时刻按立即注入处理）。
func delayAddRelative(q *session.DelayQueue, catKey, line string) string {
	head, tail, ok := splitDelayPair(line[len("delay.add|"):])
	if !ok {
		return "ERR|BAD_ARGS|delay.add 需要 <秒>|<内容>"
	}
	seconds, err := strconv.ParseInt(head, 10, 64)
	if err != nil {
		return "ERR|BAD_ARGS|秒数非法: " + head
	}
	if seconds < 0 {
		return "ERR|BAD_ARGS|秒数不能为负"
	}
	if seconds > delayMaxSecondsAhead {
		return "ERR|BAD_ARGS|秒数过大（上限 " + strconv.FormatInt(delayMaxSecondsAhead, 10) + "）"
	}
	if strings.TrimSpace(tail) == "" {
		return "ERR|BAD_ARGS|延迟条目内容为空"
	}
	dueAt := q.Now() + seconds*1000
	return q.Add(catKey, tail, "delay", dueAt, seconds*1000, false)
}

// delayAddAbsolute delay.addat|<unixms>|<内容>——绝对时刻登记（前端改时刻 / 精确设定）。
func delayAddAbsolute(q *session.DelayQueue, catKey, line string) string {
	head, tail, ok := splitDelayPair(line[len("delay.addat|"):])
	if !ok {
		return "ERR|BAD_ARGS|delay.addat 需要 <unixms>|<内容>"
	}
	dueAt, err := strconv.ParseInt(head, 10, 64)
	if err != nil {
		return "ERR|BAD_ARGS|时刻非法: " + head
	}
	if dueAt <= 0 {
		return "ERR|BAD_ARGS|时刻非法（需 Unix 毫秒）"
	}
	if strings.TrimSpace(tail) == "" {
		return "ERR|BAD_ARGS|延迟条目内容为空"
	}
	// 循环时长 = 登记时刻到绝对时刻的跨度（loop 打开时按此重排；已过时刻按 0 记）
	span := dueAt - q.Now()
	if span < 0 {
		span = 0
	}
	return q.Add(catKey, tail, "delay", dueAt, span, false)
}

// delayAddAbsoluteLoop delay.addatloop|<unixms>|<内容>——绝对时刻 + 循环登记（循环时长 = 登记跨度）。
func delayAddAbsoluteLoop(q *session.DelayQueue, catKey, line string) string {
	head, tail, ok := splitDelayPair(line[len("delay.addatloop|"):])
	if !ok {
		return "ERR|BAD_ARGS|delay.addatloop 需要 <unixms>|<内容>"
	}
	dueAt, err := strconv.ParseInt(head, 10, 64)
	if err != nil {
		return "ERR|BAD_ARGS|时刻非法: " + head
	}
	if dueAt <= 0 {
		return "ERR|BAD_ARGS|时刻非法（需 Unix 毫秒）"
	}
	if strings.TrimSpace(tail) == "" {
		return "ERR|BAD_ARGS|延迟条目内容为空"
	}
	span := dueAt - q.Now()
	if span <= 0 {
		return "ERR|BAD_ARGS|循环条目需要未来的触发时刻（当前跨度 " + strconv.FormatInt(span, 10) + "ms）"
	}
	return q.Add(catKey, tail, "delay", dueAt, span, true)
}

// delaySetDue delay.set|<id>|<unixms>——改触发时刻。
func delaySetDue(q *session.DelayQueue, catKey, line string) string {
	head, tail, ok := splitDelayPair(line[len("delay.set|"):])
	if !ok {
		return "ERR|BAD_ARGS|delay.set 需要 <id>|<unixms>"
	}
	id, err := strconv.ParseInt(head, 10, 64)
	if err != nil {
		return "ERR|BAD_ARGS|条目序号非法: " + head
	}
	dueText := strings.TrimSpace(tail)
	dueAt, err := strconv.ParseInt(dueText, 10, 64)
	if err != nil {
		return "ERR|BAD_ARGS|时刻非法: " + dueText
	}
	return q.SetDueAt(catKey, id, dueAt)
}

// delayCancel delay.cancel|<id>——取消条目。
func delayCancel(q *session.DelayQueue, catKey, line string) string {
	idText := strings.TrimSpace(line[len("delay.cancel|"):])
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		return "ERR|BAD_ARGS|条目序号非法: " + idText
	}
	return q.Cancel(catKey, id)
}

// delaySetLoop delay.loop|<id>|<0|1>——切换循环标记（loop=true 需条目已有循环时长）。
func delaySetLoop(q *session.DelayQueue, catKey, line string) string {
	head, tail, ok := splitDelayPair(line[len("delay.loop|"):])
	if !ok {
		return "ERR|BAD_ARGS|delay.loop 需要 <id>|<0|1>"
	}
	id, err := strconv.ParseInt(head, 10, 64)
	if err != nil {
		return "ERR|BAD_ARGS|条目序号非法: " + head
	}
	mark := strings.TrimSpace(tail)
	if mark != "0" && mark != "1" {
		return "ERR|BAD_ARGS|loop 标记需为 0 或 1（当前 " + mark + "）"
	}
	return q.SetLoop(catKey, id, mark == "1")
}

// splitDelayPair 拆两段——首个 | 前为 head（Trim 后），其后全段为 tail（内容可含 |）。
func splitDelayPair(rest string) (head, tail string, ok bool) {
	sep := strings.IndexByte(rest, '|')
	if sep < 0 {
		return "", "", false
	}
	return strings.TrimSpace(rest[:sep]), rest[sep+1:], true
}

// delayQueue 取本会话的延迟队列（nil = 未接线）。
func (r *commandRouter) delayQueue() *session.DelayQueue {
	if r.Session == nil {
		return nil
	}
	return r.Session.DelayQueue()
}

// delayCatKey 延迟条目归属键——会话猫 key（C# 用 target.Session.Id 同口径），空则回落路由 CatID。
func (r *commandRouter) delayCatKey() string {
	if r.Session != nil && r.Session.CatKey != "" {
		return r.Session.CatKey
	}
	return r.CatID
}
