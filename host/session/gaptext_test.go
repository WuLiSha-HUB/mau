package session

import (
	"testing"
)

// TestToolRoundGapText 工具轮间隙文本——模型先说一句再调工具时落 gap_text 块。
//
// 口径对齐 C# ChatSession.cs:2888-2892（工具轮 seal 处按助手文本非空补块，
// 先于思考段收口）：前端历史重建与 QQBot 转发消费该块。
func TestToolRoundGapText(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	sess.SetStreamer(&fakeStreamer{scripts: [][]StreamEvent{
		{
			{Kind: StreamText, Text: "先说一句"},
			{Kind: StreamToolCalls, Text: `[{"id":"call_1","type":"function","function":{"name":"time","arguments":"{}"}}]`},
			{Kind: StreamDone},
		},
		{
			{Kind: StreamText, Text: "完成"},
			{Kind: StreamDone},
		},
	}})
	if !sess.PostUser("hi", "user", "", "") {
		t.Fatal("用户消息未受理")
	}
	pumpUntilIdle(t, sess)

	var gap *ViewBlock
	for _, block := range sess.Views.Blocks() {
		if block.RenderType == TypeGapText {
			gap = block
			break
		}
	}
	if gap == nil {
		t.Fatalf("工具轮应落 gap_text 块（现有 %d 块）", len(sess.Views.Blocks()))
	}
	if gap.Payload != `{"text":"先说一句"}` {
		t.Fatalf("gap 载荷不符: %s", gap.Payload)
	}
	if gap.MsgIndex != -1 {
		t.Fatalf("gap 块非前文派生（MsgIndex 应为 -1）: %d", gap.MsgIndex)
	}
}

// TestToolRoundNoGapText 无间隙文本——模型直接调工具时不落 gap_text 块。
func TestToolRoundNoGapText(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	sess.SetStreamer(&fakeStreamer{scripts: [][]StreamEvent{
		{
			{Kind: StreamToolCalls, Text: `[{"id":"call_1","type":"function","function":{"name":"time","arguments":"{}"}}]`},
			{Kind: StreamDone},
		},
		{
			{Kind: StreamText, Text: "完成"},
			{Kind: StreamDone},
		},
	}})
	if !sess.PostUser("hi", "user", "", "") {
		t.Fatal("用户消息未受理")
	}
	pumpUntilIdle(t, sess)

	for _, block := range sess.Views.Blocks() {
		if block.RenderType == TypeGapText {
			t.Fatalf("空文本工具轮不应落 gap_text: %s", block.Payload)
		}
	}
}
