package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestSealReasonStreamClearsLive 思考段终结写空态（A196：写 empty 即清面板）。
func TestSealReasonStreamClearsLive(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	sess.AttachView()
	sess.ViewBus.SetLive(TypeThinksse, "想一半")
	if frame := sess.TakeViewFrameJSON(); !strings.Contains(frame, `"type":"thinksse"`) {
		t.Fatalf("前置：live 应为 thinksse: %s", frame)
	}
	sess.sealReasonStream()
	frame := sess.TakeViewFrameJSON()
	if !strings.Contains(frame, `"live":{"type":"empty","context":""}`) {
		t.Fatalf("seal 后 live 应为空态: %s", frame)
	}
}

// TestEnterPhaseLeaveThinkSealsLive 离开 think 态即收口（唯一态转移出口）。
func TestEnterPhaseLeaveThinkSealsLive(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	sess.AttachView()
	sess.enterPhase(PhaseThink)
	sess.ViewBus.SetLive(TypeThinksse, "思考中")
	sess.TakeViewFrameJSON()
	sess.enterPhase(PhaseReply)
	frame := sess.TakeViewFrameJSON()
	if !strings.Contains(frame, `"live":{"type":"empty","context":""}`) {
		t.Fatalf("离开 think 态应写空态: %s", frame)
	}
	// 同态重复进入不产生副作用（同态提前返回）
	sess.ViewBus.SetLive(TypeReplysse, "回复")
	sess.TakeViewFrameJSON()
	sess.enterPhase(PhaseReply)
	if frame, ok := sess.ViewBus.TryTakeFrame(); ok {
		t.Fatalf("同态进入不应产帧: %s", frame)
	}
}

// TestConsumeStreamEventLiveStates 流式两态投影——思考 / 回复各自累计全文整段覆盖。
func TestConsumeStreamEventLiveStates(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	sess.AttachView()
	sess.consumeStreamEvent(StreamEvent{Kind: StreamReasoning, Text: "想"})
	frame := sess.TakeViewFrameJSON()
	if !strings.Contains(frame, `"type":"thinksse"`) || !strings.Contains(frame, `"context":"想"`) {
		t.Fatalf("思考增量应投影 thinksse: %s", frame)
	}
	sess.consumeStreamEvent(StreamEvent{Kind: StreamReasoning, Text: "继续"})
	frame = sess.TakeViewFrameJSON()
	if !strings.Contains(frame, `"context":"想继续"`) {
		t.Fatalf("思考上下文应为累计全文: %s", frame)
	}
	// 回复增量——先离开 think（seal 空态），再整段覆盖为 replysse
	sess.consumeStreamEvent(StreamEvent{Kind: StreamText, Text: "你好"})
	frame = sess.TakeViewFrameJSON()
	if !strings.Contains(frame, `"type":"replysse"`) || !strings.Contains(frame, `"context":"你好"`) {
		t.Fatalf("回复增量应投影 replysse: %s", frame)
	}
}

// TestRefreshToolLiveProjectsPendingDogs 工具运行态投影——未完成集合 / 全量序号 / 进行中无 result。
func TestRefreshToolLiveProjectsPendingDogs(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	sess.AttachView()
	r := sess.roundState()
	r.mu.Lock()
	r.dogs = []*toolDog{
		{ToolCallID: "c1", Name: "text-read", ArgsJSON: `{"path":"a"}`, IsClosed: true, Result: "ok"},
		{ToolCallID: "c2", Name: "cs-build", ArgsJSON: `{"path":"b"}`},
		{ToolCallID: "c3", Name: "git-status", ArgsJSON: `{"path":"c"}`},
	}
	r.mu.Unlock()
	sess.refreshToolLive()
	frame := sess.TakeViewFrameJSON()
	if !strings.Contains(frame, `"type":"toolrun"`) {
		t.Fatalf("live 应为 toolrun: %s", frame)
	}
	var envelope struct {
		Live struct {
			Type    string `json:"type"`
			Context string `json:"context"`
		} `json:"live"`
	}
	if err := json.Unmarshal([]byte(frame), &envelope); err != nil {
		t.Fatalf("帧解析失败: %v", err)
	}
	var cards []map[string]any
	if err := json.Unmarshal([]byte(envelope.Live.Context), &cards); err != nil {
		t.Fatalf("live context 不是 JSON 数组: %v（context=%s）", err, envelope.Live.Context)
	}
	if len(cards) != 2 {
		t.Fatalf("未完成工具应为 2 张（闭合狗排除）: %d（%s）", len(cards), envelope.Live.Context)
	}
	if cards[0]["name"] != "cs-build" || cards[1]["name"] != "git-status" {
		t.Fatalf("声明序错误: %v", cards)
	}
	if cards[0]["toolIndex"].(float64) != 2 || cards[0]["toolTotal"].(float64) != 3 {
		t.Fatalf("序号应按全量口径（2/3）: %v", cards[0])
	}
	if _, exists := cards[0]["result"]; exists {
		t.Fatalf("进行中卡不应写 result 字段: %v", cards[0])
	}
	if cards[0]["durMs"].(float64) != -1 || cards[0]["queueMs"].(float64) != -1 || cards[0]["runMs"].(float64) != -1 {
		t.Fatalf("时长三值应为 -1: %v", cards[0])
	}
	// 全部闭合 → 空数组（前端零元素；收尾点随后写 empty）
	r.mu.Lock()
	r.dogs[1].IsClosed = true
	r.dogs[2].IsClosed = true
	r.mu.Unlock()
	sess.refreshToolLive()
	frame = sess.TakeViewFrameJSON()
	if !strings.Contains(frame, `"context":"[]"`) {
		t.Fatalf("全闭合 context 应为 []: %s", frame)
	}
}

// TestRoundLiveLifecycle 端到端——一轮工具对话里 live 出现 toolrun，收尾归空。
//
// 说明：fakeStreamer 在流 goroutine 内瞬时回放全部事件，thinksse / replysse 的中间态
// 在两次取帧之间被覆盖（真实端点逐帧到达时才可见）——两态由 TestConsumeStreamEventLiveStates 覆盖。
func TestRoundLiveLifecycle(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	sess.AttachView()
	sess.SetStreamer(&fakeStreamer{scripts: [][]StreamEvent{
		{
			{Kind: StreamReasoning, Text: "思考"},
			{Kind: StreamText, Text: "先说一句"},
			{Kind: StreamToolCalls, Text: `[{"id":"call_1","type":"function","function":{"name":"time","arguments":"{}"}}]`},
			{Kind: StreamDone},
		},
		{
			{Kind: StreamText, Text: "完成"},
			{Kind: StreamDone},
		},
	}})
	sess.PostUser("hi", "user", "", "")
	seen := map[string]bool{}
	lastType := ""
	for i := 0; i < 500; i++ {
		sess.Pump()
		if frame := sess.TakeViewFrameJSON(); frame != "" {
			var envelope struct {
				Live struct {
					Type    string `json:"type"`
					Context string `json:"context"`
				} `json:"live"`
			}
			if err := json.Unmarshal([]byte(frame), &envelope); err == nil && envelope.Live.Type != "" {
				seen[envelope.Live.Type] = true
				lastType = envelope.Live.Type
			}
		}
		time.Sleep(3 * time.Millisecond)
		if sess.PhaseText() == "Idle" && seen[TypeToolrun] {
			break
		}
	}
	for _, want := range []string{TypeToolrun} {
		if !seen[want] {
			t.Fatalf("live 未出现 %s（实际 %v）", want, seen)
		}
	}
	if lastType != TypeEmpty {
		t.Fatalf("轮末 live 应为空态，实际 %s", lastType)
	}
}
