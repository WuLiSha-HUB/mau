package session

import (
	"strings"
	"testing"
)

// TestRoundFailoverView 端点切换事件——retry 块（state=failover）落视图（自动故障转移面）。
func TestRoundFailoverView(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	sess.SetStreamer(&fakeStreamer{scripts: [][]StreamEvent{{
		{Kind: StreamFailover, Text: "FAILOVER|备用|ERR|TRANSPORT|Timeout|请求发出后 60 秒无回应"},
		{Kind: StreamText, Text: "恢复"},
		{Kind: StreamFinish, Text: "stop"},
		{Kind: StreamDone},
	}}})
	if !sess.PostUser("hi", "user", "", "") {
		t.Fatal("用户消息未受理")
	}
	pumpUntilIdle(t, sess)
	var retryBlocks []*ViewBlock
	for _, block := range sess.Views.Blocks() {
		if block.RenderType == TypeRetry {
			retryBlocks = append(retryBlocks, block)
		}
	}
	if len(retryBlocks) != 1 {
		t.Fatalf("期望 1 个 retry 块，实际 %d", len(retryBlocks))
	}
	payload := retryBlocks[0].Payload
	if !strings.Contains(payload, `"state":"failover"`) ||
		!strings.Contains(payload, `"role":"备用"`) ||
		!strings.Contains(payload, `"text":"ERR|TRANSPORT|Timeout|请求发出后 60 秒无回应"`) {
		t.Fatalf("failover 载荷不符: %s", payload)
	}
}

// TestToggleAPIRoleView 手动切换——retry 块（原因空）+ 角色文本（C# PushApiRoleView 同形）。
func TestToggleAPIRoleView(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	sess.SetAPIRole(&fakeAPIRole{})
	if role := sess.ToggleAPIRole(); role != "备用" {
		t.Fatalf("手动切换期望备用，实际 %q", role)
	}
	blocks := sess.Views.Blocks()
	if len(blocks) != 1 || blocks[0].RenderType != TypeRetry {
		t.Fatalf("期望 1 个 retry 块，实际 %d", len(blocks))
	}
	if blocks[0].Payload != `{"state":"failover","role":"备用","text":""}` {
		t.Fatalf("手动切换载荷不符: %s", blocks[0].Payload)
	}
}
