package session

import (
	"strings"
	"testing"
)

// TestHostDogsUseBuiltinSurface host-* 内置工具在批末走内置执行面（不落到未接线的宿主直执面）。
//
// 判例（2026-11-14 17:07 部署后实测）：`host-flows` 回 `ERR|EMPTY_RESULT|宿主直执面未接线`——
// host-* 前缀在 startBatch 被登记进 hostDogs，批末无条件走 runHostTool，而 toolExec 未接线
// （wiring 显式 SetToolExecutor(nil)）⇒ 已实现的内置执行体不可达。
func TestHostDogsUseBuiltinSurface(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	sess.SetBuiltinExecutor(
		func(name string) bool { return strings.HasPrefix(name, "host-") },
		func(name, argsJSON string) string {
			if name != "host-flows" {
				return "ERR|BAD_ARGS|unexpected: " + name
			}
			return "host-flows | 1 组"
		},
	)
	// 宿主直执面显式未接线——走错路径必回 EMPTY_RESULT
	sess.SetToolExecutor(nil)
	sess.SetStreamer(&fakeStreamer{scripts: [][]StreamEvent{
		{
			{Kind: StreamToolCalls, Text: `[{"id":"1","type":"function","function":{"name":"host-flows","arguments":"{}"}}]`},
			{Kind: StreamDone},
		},
		{
			{Kind: StreamText, Text: "完成"},
			{Kind: StreamDone},
		},
	}})
	if !sess.PostUser("查 flow", "user", "", "") {
		t.Fatal("用户消息未受理")
	}
	pumpUntilIdle(t, sess)
	r := sess.roundState()
	r.mu.Lock()
	dogs := append([]*toolDog(nil), r.dogs...)
	r.mu.Unlock()
	if len(dogs) != 1 {
		t.Fatalf("工具单数 = %d，期望 1", len(dogs))
	}
	if dogs[0].Result != "host-flows | 1 组" {
		t.Fatalf("host-* 应走内置执行面，got %q", dogs[0].Result)
	}
}

// TestHostDogsFallbackToHostSurface 非内置 host-* 仍走宿主直执面（未接线 → 具名 ERR，不静默）。
func TestHostDogsFallbackToHostSurface(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	sess.SetBuiltinExecutor(
		func(name string) bool { return name == "time" },
		sess.ExecuteBuiltin,
	)
	sess.SetToolExecutor(nil)
	sess.SetStreamer(&fakeStreamer{scripts: [][]StreamEvent{
		{
			{Kind: StreamToolCalls, Text: `[{"id":"1","type":"function","function":{"name":"host-custom","arguments":"{}"}}]`},
			{Kind: StreamDone},
		},
		{
			{Kind: StreamText, Text: "完成"},
			{Kind: StreamDone},
		},
	}})
	if !sess.PostUser("跑", "user", "", "") {
		t.Fatal("用户消息未受理")
	}
	pumpUntilIdle(t, sess)
	r := sess.roundState()
	r.mu.Lock()
	dogs := append([]*toolDog(nil), r.dogs...)
	r.mu.Unlock()
	if len(dogs) != 1 {
		t.Fatalf("工具单数 = %d，期望 1", len(dogs))
	}
	if !strings.HasPrefix(dogs[0].Result, "ERR|EMPTY_RESULT|宿主直执面未接线") {
		t.Fatalf("非内置 host-* 应回具名 ERR，got %q", dogs[0].Result)
	}
}
