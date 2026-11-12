package session

import (
	"strings"
	"testing"
)

// summaryCall 是一次总结工单提交记录。
type summaryCall struct {
	name    string
	timeout int64
}

// summaryStr 是一次载荷写入记录。
type summaryStr struct {
	officeID int64
	key      string
	value    string
}

// fakeSummaryOffice 是轮末总结工单面的测试替身——按脚本返回观测态。
type fakeSummaryOffice struct {
	// posted 提交记录
	posted []summaryCall
	// strs 载荷写入记录
	strs []summaryStr
	// nextID 工单号发生器
	nextID int64
	// removed RemoveByOwner 调用次数
	removed int
	// script 每次 State 调用依次取一条；耗尽后沿用最后一条
	script []ToolOfficeState
	// calls State 调用计数
	calls int
}

// Post 记录提交并返回工单号。
func (f *fakeSummaryOffice) Post(officeName string, timeoutFrames int64) int64 {
	f.nextID++
	f.posted = append(f.posted, summaryCall{name: officeName, timeout: timeoutFrames})
	return f.nextID
}

// SetStr 记录载荷写入。
func (f *fakeSummaryOffice) SetStr(officeID int64, key, value string) bool {
	f.strs = append(f.strs, summaryStr{officeID: officeID, key: key, value: value})
	return true
}

// State 按脚本返回观测态。
func (f *fakeSummaryOffice) State(officeID int64) ToolOfficeState {
	if len(f.script) == 0 {
		return ToolOfficeState{}
	}
	index := f.calls
	if index >= len(f.script) {
		index = len(f.script) - 1
	}
	f.calls++
	return f.script[index]
}

// RemoveByOwner 记录回收次数。
func (f *fakeSummaryOffice) RemoveByOwner() { f.removed++ }

// TestRoundSummaryPost 轮末发单——工单名 / 超时帧 / 五键载荷 / 提示词与内容逐字。
func TestRoundSummaryPost(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	office := &fakeSummaryOffice{}
	sess.SetRoundSummaryOffice(office)
	sess.SetRoundSummary(true)
	sess.SetRoundSummaryLlm(func() []string { return []string{"http://127.0.0.1:1/v1", "sk-test", "m1"} })
	sess.appendRoundSummaryUser("你好")
	sess.appendRoundSummaryText("我做了 A")
	sess.PostRoundSummary(2000)

	if len(office.posted) != 1 || office.posted[0].name != "LLMSimpleRequest" || office.posted[0].timeout != RoundSummaryTimeoutFrames {
		t.Fatalf("发单不符: %+v", office.posted)
	}
	want := []struct{ key, value string }{
		{"endpoint", "http://127.0.0.1:1/v1"},
		{"apikey", "sk-test"},
		{"model", "m1"},
	}
	if len(office.strs) != 5 {
		t.Fatalf("应写五键，实得 %d: %+v", len(office.strs), office.strs)
	}
	for i, item := range want {
		if office.strs[i].key != item.key || office.strs[i].value != item.value {
			t.Fatalf("第 %d 键不符: %+v", i, office.strs[i])
		}
	}
	if office.strs[3].key != "system" || !strings.Contains(office.strs[3].value, "你是「cat」") {
		t.Fatalf("system 提示词不符: %+v", office.strs[3])
	}
	if office.strs[4].key != "content" || office.strs[4].value != "你好\n我做了 A\n"+RoundSummaryPromptTail {
		t.Fatalf("content 不符: %q", office.strs[4].value)
	}
	rs := &sess.roundSummary
	rs.mu.Lock()
	pendingTs, officeID := rs.pendingTs, rs.officeID
	rs.mu.Unlock()
	if pendingTs != 2000 || officeID != 1 {
		t.Fatalf("会话态不符: pendingTs=%d officeID=%d", pendingTs, officeID)
	}
}

// TestRoundSummaryPostSkips 不发单五态——开关关 / 配置 nil / 配置短 / 配置空值 / 无内容。
func TestRoundSummaryPostSkips(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		llm     []string
		user    string
		text    string
	}{
		{"开关关", false, []string{"e", "k", "m"}, "u", "t"},
		{"配置 nil", true, nil, "u", "t"},
		{"配置短", true, []string{"e", "k"}, "u", "t"},
		{"配置空值", true, []string{"e", "", "m"}, "u", "t"},
		{"无内容", true, []string{"e", "k", "m"}, "", ""},
	}
	for _, c := range cases {
		sess, _ := newRoundTestSession(t)
		office := &fakeSummaryOffice{}
		sess.SetRoundSummaryOffice(office)
		sess.SetRoundSummary(c.enabled)
		sess.SetRoundSummaryLlm(func() []string { return c.llm })
		sess.appendRoundSummaryUser(c.user)
		sess.appendRoundSummaryText(c.text)
		sess.PostRoundSummary(1)
		if len(office.posted) != 0 {
			t.Fatalf("%s: 不应发单: %+v", c.name, office.posted)
		}
	}
}

// TestRoundSummaryPumpClosed 在途回收——未闭合不落块；闭合取回执落 roundsummary（ts = pendingTs）。
func TestRoundSummaryPumpClosed(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	office := &fakeSummaryOffice{script: []ToolOfficeState{
		{Status: 1},
		{Status: 2, Result: "我把文件读完了", HasResult: true},
	}}
	sess.SetRoundSummaryOffice(office)
	sess.SetRoundSummary(true)
	sess.SetRoundSummaryLlm(func() []string { return []string{"e", "k", "m"} })
	sess.appendRoundSummaryUser("读文件")
	sess.PostRoundSummary(2001)
	sess.pumpRoundSummary()
	if blocks := sess.Views.Blocks(); len(blocks) != 0 {
		t.Fatalf("未闭合不应落块: %+v", blocks)
	}
	sess.pumpRoundSummary()
	blocks := sess.Views.Blocks()
	if len(blocks) != 1 || blocks[0].RenderType != TypeRoundSummary {
		t.Fatalf("落块不符: %+v", blocks)
	}
	if blocks[0].Payload != `{"text":"我把文件读完了"}` {
		t.Fatalf("载荷不符（audioName 恒空不写 audio 字段）: %s", blocks[0].Payload)
	}
	if blocks[0].Timestamp != 2001 {
		t.Fatalf("时间戳应为 pendingTs: %d", blocks[0].Timestamp)
	}
	if office.removed != 1 {
		t.Fatalf("RemoveByOwner 应调用一次: %d", office.removed)
	}
	// 回收后无在途——再 Pump 不再落块
	sess.pumpRoundSummary()
	if got := len(sess.Views.Blocks()); got != 1 {
		t.Fatalf("重复 Pump 不应再落块: %d", got)
	}
}

// TestRoundSummaryPumpFailure 失败侧可见——超时与空回执均落 ERR| 文本。
func TestRoundSummaryPumpFailure(t *testing.T) {
	cases := []struct {
		name string
		feed []ToolOfficeState
		want string
	}{
		{"超时", []ToolOfficeState{{Status: 3}}, `{"text":"ERR|TIMEOUT|轮末总结超时"}`},
		{"空回执", []ToolOfficeState{{Status: 2}}, `{"text":"ERR|EMPTY|轮末总结无可读回执"}`},
	}
	for _, c := range cases {
		sess, _ := newRoundTestSession(t)
		office := &fakeSummaryOffice{script: c.feed}
		sess.SetRoundSummaryOffice(office)
		sess.SetRoundSummary(true)
		sess.SetRoundSummaryLlm(func() []string { return []string{"e", "k", "m"} })
		sess.appendRoundSummaryUser("u")
		sess.PostRoundSummary(3000)
		sess.pumpRoundSummary()
		blocks := sess.Views.Blocks()
		if len(blocks) != 1 || blocks[0].Payload != c.want {
			t.Fatalf("%s: 载荷不符: %+v", c.name, blocks)
		}
	}
}

// TestRoundSummaryBuffers 缓冲累积——人工输入入 user 缓冲、sys 注入不入、轮首清零。
func TestRoundSummaryBuffers(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	sess.AppendInjection("人工输入", "", "", "")
	if got := sess.BuildRoundSummaryContent(); got != "人工输入\n"+RoundSummaryPromptTail {
		t.Fatalf("user 缓冲不符: %q", got)
	}
	sess.AppendInjection("系统注入", "systemauto", "", "")
	if got := sess.BuildRoundSummaryContent(); strings.Contains(got, "系统注入") {
		t.Fatalf("sys 注入不应计入: %q", got)
	}
	sess.appendRoundSummaryText("回复正文")
	if got := sess.BuildRoundSummaryContent(); !strings.Contains(got, "回复正文") {
		t.Fatalf("text 缓冲不符: %q", got)
	}
	sess.resetRoundSummaryBuffers()
	if got := sess.BuildRoundSummaryContent(); got != "" {
		t.Fatalf("轮首清零后应为空: %q", got)
	}
}

// TestRoundSummaryPromptHead 前段提示词——显示名优先、空回落猫 key、关键约束在文。
func TestRoundSummaryPromptHead(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	if got := sess.BuildRoundSummaryPromptHead(); !strings.Contains(got, "你是「cat」") {
		t.Fatalf("应回落猫 key: %s", got)
	}
	sess.SetDisplayName("大小姐")
	got := sess.BuildRoundSummaryPromptHead()
	if !strings.Contains(got, "你是「大小姐」") {
		t.Fatalf("显示名未生效: %s", got)
	}
	for _, want := range []string{"第一人称", "一句话 20-50 字", "不要客套、不要分点、不要引号", "C U D A"} {
		if !strings.Contains(got, want) {
			t.Fatalf("提示词缺关键句 %q: %s", want, got)
		}
	}
}
