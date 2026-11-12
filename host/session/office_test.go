package session

import (
	"strings"
	"testing"
)

// postedCall 是一次工单提交记录。
type postedCall struct {
	name     string
	argsJSON string
	timeout  int64
}

// fakeOffice 是工具工单面的测试替身——按脚本返回观测态。
type fakeOffice struct {
	posted []postedCall
	nextID int64
	// script 每次 State 调用依次取一条；耗尽后沿用最后一条
	script []ToolOfficeState
	// calls State 调用计数
	calls int
}

// Post 记录提交并返回工单号。
func (f *fakeOffice) Post(name, argsJSON string, timeoutFrames int64) (int64, string) {
	f.nextID++
	f.posted = append(f.posted, postedCall{name: name, argsJSON: argsJSON, timeout: timeoutFrames})
	return f.nextID, ""
}

// State 按脚本返回观测态。
func (f *fakeOffice) State(officeID int64) ToolOfficeState {
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

// TestToolOfficeClosed 工单闭合——Post 载荷 / 认领观测 / 回执逐字回填。
func TestToolOfficeClosed(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	office := &fakeOffice{script: []ToolOfficeState{
		{Status: 1, ClaimFrame: 5},
		{Status: 2, ClaimFrame: 5, Result: "回执正文", HasResult: true},
	}}
	sess.SetToolOffice(office)
	sess.SetStreamer(&fakeStreamer{scripts: [][]StreamEvent{
		{
			{Kind: StreamToolCalls, Text: `[{"id":"1","type":"function","function":{"name":"text-read","arguments":"{\"path\":\"a.txt\"}"}}]`},
			{Kind: StreamDone},
		},
		{
			{Kind: StreamText, Text: "完成"},
			{Kind: StreamDone},
		},
	}})
	if !sess.PostUser("读文件", "user", "", "") {
		t.Fatal("用户消息未受理")
	}
	pumpUntilIdle(t, sess)

	if len(office.posted) != 1 || office.posted[0].name != "text-read" {
		t.Fatalf("工单提交不符: %+v", office.posted)
	}
	if !strings.Contains(office.posted[0].argsJSON, `"path"`) || office.posted[0].timeout != toolOfficeTimeoutFrames {
		t.Fatalf("载荷或时限不符: %+v", office.posted[0])
	}
	if got := lastToolResult(sess); got != "回执正文" {
		t.Fatalf("回执未回填: %q", got)
	}
}

// TestToolOfficeMissingResult 回执缺 result——诚实 ERR（不静默空串）。
func TestToolOfficeMissingResult(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	office := &fakeOffice{script: []ToolOfficeState{{Status: 2, ClaimFrame: 1}}}
	sess.SetToolOffice(office)
	sess.SetStreamer(&fakeStreamer{scripts: [][]StreamEvent{
		{
			{Kind: StreamToolCalls, Text: `[{"id":"1","type":"function","function":{"name":"text-read","arguments":"{}"}}]`},
			{Kind: StreamDone},
		},
		{
			{Kind: StreamText, Text: "完成"},
			{Kind: StreamDone},
		},
	}})
	if !sess.PostUser("读文件", "user", "", "") {
		t.Fatal("用户消息未受理")
	}
	pumpUntilIdle(t, sess)
	if got := lastToolResult(sess); got != "ERR|EMPTY_RESULT|工单回执无 result" {
		t.Fatalf("缺 result 回执不符: %q", got)
	}
}

// TestToolOfficeTimeout 工单超时——走 flushToolCard 的 OA_TIMEOUT 文案。
func TestToolOfficeTimeout(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	office := &fakeOffice{script: []ToolOfficeState{{Status: 3}}}
	sess.SetToolOffice(office)
	sess.SetStreamer(&fakeStreamer{scripts: [][]StreamEvent{
		{
			{Kind: StreamToolCalls, Text: `[{"id":"1","type":"function","function":{"name":"text-read","arguments":"{}"}}]`},
			{Kind: StreamDone},
		},
		{
			{Kind: StreamText, Text: "完成"},
			{Kind: StreamDone},
		},
	}})
	if !sess.PostUser("读文件", "user", "", "") {
		t.Fatal("用户消息未受理")
	}
	pumpUntilIdle(t, sess)
	if got := lastToolResult(sess); !strings.HasPrefix(got, "ERR|OA_TIMEOUT|工单超时（时限内无回执）: text-read") {
		t.Fatalf("超时回执不符: %q", got)
	}
}

// lastToolResult 取末条工具回执正文。
func lastToolResult(sess *Session) string {
	text := ""
	for _, msg := range sess.Context.Messages() {
		if msg.Role == RoleTool {
			text = msg.Content
		}
	}
	return text
}
