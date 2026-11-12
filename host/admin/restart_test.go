package admin

import (
	"testing"

	"cathome5/host/session"
)

// TestRestartStateLifecycle 重启登记面——登记 / 快照 / 载荷解析 / 撤销 / 模式归一。
func TestRestartStateLifecycle(t *testing.T) {
	prev := session.HostRestarting
	t.Cleanup(func() { session.HostRestarting = prev })
	state := NewRestartState(nil)
	if state.Requested() {
		t.Fatal("初始不应处于停机态")
	}
	if _, ok := state.Snapshot(); ok {
		t.Fatal("初始不应有请求")
	}
	state.Request("incr", `D:\Mau\MauOut5`, "说明")
	if !state.Requested() {
		t.Fatal("登记后应进入停机态")
	}
	req, ok := state.Snapshot()
	if !ok || req.Mode != "incr" || req.Target != `D:\Mau\MauOut5` || req.Push != "说明" {
		t.Fatalf("请求快照不符：%+v ok=%v", req, ok)
	}
	// 载荷解析（会话批次检测消费）——mode 缺省 full
	state.RequestFromPayload(`{"target":"x","push":"p"}`)
	req, _ = state.Snapshot()
	if req.Mode != "full" || req.Target != "x" || req.Push != "p" {
		t.Fatalf("载荷解析不符：%+v", req)
	}
	// 模式归一——非法值按 host（管理面板端点已先校验，此处兜底）
	state.Request("bogus", "", "")
	req, _ = state.Snapshot()
	if req.Mode != "host" {
		t.Fatalf("非法模式应归一 host：%+v", req)
	}
	// 撤销——停机理与载荷同清
	state.Withdraw("测试撤销")
	if state.Requested() {
		t.Fatal("撤销后应退出停机态")
	}
	if _, ok := state.Snapshot(); ok {
		t.Fatal("撤销后不应再有请求")
	}
	// 重复撤销零动作（不 panic）
	state.Withdraw("重复撤销")
	// nil 安全
	var nilState *RestartState
	if nilState.Requested() {
		t.Fatal("nil 状态不应判定停机")
	}
	nilState.Request("host", "", "")
	nilState.Withdraw("noop")
	if _, ok := nilState.Snapshot(); ok {
		t.Fatal("nil 状态不应有请求")
	}
}
