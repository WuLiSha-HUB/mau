package session

import (
	"strings"
	"testing"
	"time"
)

// 本文件测「排空闸门」的会话面（drain freeze）：
//   - 停机态不启动新轮（排队消息保留）；
//   - 在途工具批跑完 + 回填后冻结（不再发下一轮 LLM）；
//   - 闸门撤销自动解冻继续；
//   - 登记会话豁免冻结（本轮照常收尾）。

// roundPhaseOf 取会话粗粒度相位（测试辅助）。
func roundPhaseOf(s *Session) int {
	r := s.roundState()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.phase
}

// withRestartFlag 替换停机态钩子（测试隔离——返回可变的置位函数）。
func withRestartFlag(t *testing.T) *bool {
	t.Helper()
	prev := HostRestarting
	flag := false
	HostRestarting = func() bool { return flag }
	t.Cleanup(func() { HostRestarting = prev })
	return &flag
}

// pumpUntilPhase 推进到指定相位（上限 200 帧，每帧 2ms）。
func pumpUntilPhase(t *testing.T, sess *Session, phase int) bool {
	t.Helper()
	for i := 0; i < 200; i++ {
		if roundPhaseOf(sess) == phase {
			return true
		}
		sess.Pump()
		time.Sleep(2 * time.Millisecond)
	}
	return roundPhaseOf(sess) == phase
}

// TestPumpIdle_BlocksNewRoundWhenRestarting 停机态——排队消息不启动新轮（人工队列跨重启保留）。
func TestPumpIdle_BlocksNewRoundWhenRestarting(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	streamer := &fakeStreamer{scripts: [][]StreamEvent{{
		{Kind: StreamText, Text: "答"},
		{Kind: StreamDone},
	}}}
	sess.SetStreamer(streamer)
	if !sess.PostUser("hi", "user", "", "") {
		t.Fatal("用户消息未受理")
	}
	flag := withRestartFlag(t)
	*flag = true
	for i := 0; i < 5; i++ {
		sess.Pump()
	}
	if streamer.calls != 0 {
		t.Fatalf("停机态不应发起 LLM 请求（calls=%d）", streamer.calls)
	}
	if got := roundPhaseOf(sess); got != chatPhaseIdle {
		t.Fatalf("停机态相位应保持 Idle: %d", got)
	}
	if user, _ := sess.Pending.Len(); user != 1 {
		t.Fatalf("排队消息应保留（跨重启）: %d", user)
	}
}

// TestPumpToolBatch_FreezeAtBatchEnd 批末冻结——在途批跑完 + 回填后不再发起下一轮 LLM。
func TestPumpToolBatch_FreezeAtBatchEnd(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	office := &fakeOffice{script: []ToolOfficeState{
		{Status: 2, ClaimFrame: 1, Result: "回执正文", HasResult: true},
	}}
	sess.SetToolOffice(office)
	streamer := &fakeStreamer{scripts: [][]StreamEvent{
		{
			{Kind: StreamToolCalls, Text: `[{"id":"1","type":"function","function":{"name":"text-read","arguments":"{}"}}]`},
			{Kind: StreamDone},
		},
		{
			{Kind: StreamText, Text: "完成"},
			{Kind: StreamDone},
		},
	}}
	sess.SetStreamer(streamer)
	prevOwner, _ := RestartOwnerCatKey.Load().(string)
	t.Cleanup(func() { RestartOwnerCatKey.Store(prevOwner) })
	RestartOwnerCatKey.Store("other-cat")
	flag := withRestartFlag(t)

	if !sess.PostUser("跑", "user", "", "") {
		t.Fatal("用户消息未受理")
	}
	if !pumpUntilPhase(t, sess, chatPhaseToolBatchRunning) {
		t.Fatalf("未进入工具批相位: %d", roundPhaseOf(sess))
	}
	if len(office.posted) != 1 {
		t.Fatalf("工单应已提交: %d", len(office.posted))
	}
	// 停机态置位——批完成帧即冻结（在途批不中断，自然结算）
	*flag = true
	for i := 0; i < 50 && !sess.IsFrozenForRestart(); i++ {
		sess.Pump()
		time.Sleep(2 * time.Millisecond)
	}
	if !sess.IsFrozenForRestart() {
		t.Fatal("批末应冻结")
	}
	if streamer.calls != 1 {
		t.Fatalf("冻结后不应发起下一轮 LLM（calls=%d）", streamer.calls)
	}
	if got := lastToolResult(sess); got != "回执正文" {
		t.Fatalf("批结果应在冻结前回填: %q", got)
	}
	if sess.IsIdle() {
		t.Fatal("冻结会话不应报 Idle（闸门另行认 IsFrozenForRestart）")
	}
	if got := roundPhaseOf(sess); got != chatPhaseToolBatchRunning {
		t.Fatalf("冻结相位应停在工具批: %d", got)
	}
}

// TestUnfreeze_ResumeAfterWithdraw 闸门撤销——解冻后本轮继续（补发下一轮 LLM 并收尾）。
func TestUnfreeze_ResumeAfterWithdraw(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	office := &fakeOffice{script: []ToolOfficeState{
		{Status: 2, ClaimFrame: 1, Result: "回执正文", HasResult: true},
	}}
	sess.SetToolOffice(office)
	streamer := &fakeStreamer{scripts: [][]StreamEvent{
		{
			{Kind: StreamToolCalls, Text: `[{"id":"1","type":"function","function":{"name":"text-read","arguments":"{}"}}]`},
			{Kind: StreamDone},
		},
		{
			{Kind: StreamText, Text: "完成"},
			{Kind: StreamDone},
		},
	}}
	sess.SetStreamer(streamer)
	prevOwner, _ := RestartOwnerCatKey.Load().(string)
	t.Cleanup(func() { RestartOwnerCatKey.Store(prevOwner) })
	RestartOwnerCatKey.Store("other-cat")
	flag := withRestartFlag(t)

	if !sess.PostUser("跑", "user", "", "") {
		t.Fatal("用户消息未受理")
	}
	if !pumpUntilPhase(t, sess, chatPhaseToolBatchRunning) {
		t.Fatalf("未进入工具批相位: %d", roundPhaseOf(sess))
	}
	*flag = true
	for i := 0; i < 50 && !sess.IsFrozenForRestart(); i++ {
		sess.Pump()
		time.Sleep(2 * time.Millisecond)
	}
	if !sess.IsFrozenForRestart() {
		t.Fatal("批末应冻结")
	}
	// 闸门撤销（停机态解除）→ 自动解冻并继续
	*flag = false
	pumpUntilIdle(t, sess)
	if sess.IsFrozenForRestart() {
		t.Fatal("解冻后冻结标记应清除")
	}
	if streamer.calls != 2 {
		t.Fatalf("解冻后应补发下一轮 LLM（calls=%d）", streamer.calls)
	}
	msgs := sess.Context.Messages()
	last := msgs[len(msgs)-1]
	if last.Role != RoleAssistant || last.Content != "完成" {
		t.Fatalf("解冻后应正常收尾: %+v", last)
	}
}

// TestFreeze_OwnerExempt 登记会话豁免——停机态下本次轮照常收尾（闸门等的是它 Idle）。
func TestFreeze_OwnerExempt(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	streamer := &fakeStreamer{scripts: [][]StreamEvent{{
		{Kind: StreamText, Text: "已登记"},
		{Kind: StreamDone},
	}}}
	sess.SetStreamer(streamer)
	prevOwner, _ := RestartOwnerCatKey.Load().(string)
	t.Cleanup(func() { RestartOwnerCatKey.Store(prevOwner) })
	RestartOwnerCatKey.Store("cat") // newRoundTestSession 的会话 key
	flag := withRestartFlag(t)

	if !sess.PostUser("重启", "user", "", "") {
		t.Fatal("用户消息未受理")
	}
	// 轮已启动（流同步完成）后置停机态——登记会话本轮照常收尾
	sess.Pump()
	*flag = true
	pumpUntilIdle(t, sess)
	if sess.IsFrozenForRestart() {
		t.Fatal("登记会话不应冻结")
	}
	if streamer.calls != 1 {
		t.Fatalf("登记会话应照常收尾（calls=%d）", streamer.calls)
	}
	msgs := sess.Context.Messages()
	last := msgs[len(msgs)-1]
	if last.Role != RoleAssistant || !strings.Contains(last.Content, "已登记") {
		t.Fatalf("登记会话应有最终回复: %+v", last)
	}
}
