package llm

import (
	"strings"
	"testing"
)

// collect 跑一次翻译并收集事件。
func collect(stream string) []Event {
	var events []Event
	TranslateSSE(strings.NewReader(stream), func(ev Event) { events = append(events, ev) })
	return events
}

// kinds 事件类别序列文本（便于逐条断言）。
func kinds(events []Event) string {
	names := []string{"reasoning", "text", "toolcallsstart", "toolcalls", "usage", "finish", "done", "retrying", "retryresume", "failover", "error"}
	parts := make([]string, 0, len(events))
	for _, ev := range events {
		parts = append(parts, names[ev.Kind])
	}
	return strings.Join(parts, ",")
}

// TestTranslateSSE_Normal 正常流——增量 → tool_calls 聚合 → finish → usage → done。
func TestTranslateSSE_Normal(t *testing.T) {
	stream := `data: {"choices":[{"delta":{"reasoning_content":"想"}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"content":"你"}}]}` + "\n\n" +
		": heartbeat\n\n" +
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"time","arguments":"{"}}]}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]}}]}` + "\n\n" +
		`data: {"choices":[{"finish_reason":"stop","delta":{}}],"usage":{"prompt_tokens":10,"completion_tokens":3,"prompt_cache_hit_tokens":4}}` + "\n\n" +
		"data: [DONE]\n\n"
	events := collect(stream)
	if got := kinds(events); got != "reasoning,text,toolcallsstart,finish,usage,toolcalls,done" {
		t.Fatalf("事件序列不符: %s", got)
	}
	if events[0].Text != "想" || events[1].Text != "你" {
		t.Fatalf("增量文本不符: %+v", events[:2])
	}
	wantCalls := `[{"id":"c1","type":"function","function":{"name":"time","arguments":"{}"}}]`
	if events[5].Text != wantCalls {
		t.Fatalf("tool_calls 聚合不符:\n got: %s\nwant: %s", events[5].Text, wantCalls)
	}
	if events[3].Text != "stop" {
		t.Fatalf("finish_reason 不符: %s", events[3].Text)
	}
	if events[4].Text != `{"prompt":10,"completion":3,"cacheHit":4}` {
		t.Fatalf("usage 不符: %s", events[4].Text)
	}
}

// TestTranslateSSE_Empty 空流——无 [DONE] → STREAM_CLOSED。
func TestTranslateSSE_Empty(t *testing.T) {
	events := collect("")
	if got := kinds(events); got != "error" {
		t.Fatalf("空流事件序列: %s", got)
	}
	if !strings.HasPrefix(events[0].Text, "ERR|STREAM_CLOSED") {
		t.Fatalf("空流错误文本: %s", events[0].Text)
	}
}

// TestTranslateSSE_Truncated 截断流——有增量无 [DONE] → 只产 STREAM_CLOSED。
func TestTranslateSSE_Truncated(t *testing.T) {
	events := collect("data: {\"choices\":[{\"delta\":{\"content\":\"半\"}}]}\n\n")
	if got := kinds(events); got != "text,error" {
		t.Fatalf("截断流事件序列: %s", got)
	}
	if !strings.HasPrefix(events[1].Text, "ERR|STREAM_CLOSED") {
		t.Fatalf("截断流错误文本: %s", events[1].Text)
	}
}

// TestTranslateSSE_HeartbeatOnly 心跳流——注释与空行不产事件，正常 [DONE] 只产 Done。
func TestTranslateSSE_HeartbeatOnly(t *testing.T) {
	events := collect(": ping\n\n: ping\n\ndata: [DONE]\n\n")
	if got := kinds(events); got != "done" {
		t.Fatalf("心跳流事件序列: %s", got)
	}
}

// TestTranslateSSE_UsageOnlyFrame 独立 usage 尾帧——choices 空数组也解析 usage。
func TestTranslateSSE_UsageOnlyFrame(t *testing.T) {
	stream := "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":0,\"prompt_tokens_details\":{\"cached_tokens\":2}}}\n\n" +
		"data: [DONE]\n\n"
	events := collect(stream)
	if got := kinds(events); got != "usage,done" {
		t.Fatalf("usage 尾帧事件序列: %s", got)
	}
	if events[0].Text != `{"prompt":5,"completion":0,"cacheHit":2}` {
		t.Fatalf("usage 解析: %s", events[0].Text)
	}
}

// TestTranslateSSE_MalformedFrame 畸形帧——跳过不产事件，流仍以 [DONE] 正常收尾。
func TestTranslateSSE_MalformedFrame(t *testing.T) {
	stream := "data: {not-json}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n" +
		"data: [DONE]\n\n"
	events := collect(stream)
	if got := kinds(events); got != "text,done" {
		t.Fatalf("畸形帧事件序列: %s", got)
	}
}
