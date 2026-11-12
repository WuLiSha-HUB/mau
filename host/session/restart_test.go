package session

import "testing"

// TestSessionIsIdle 空闲判据——排队消息 / 取尽 / 新会话三态（宿主重启闸门 T3 会话面）。
func TestSessionIsIdle(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSession(t, dir, "cat-idle")
	if !s.IsIdle() {
		t.Fatal("新会话应为 Idle（无在途轮次无排队）")
	}
	if !s.PostUser("hi", "user", "", "") {
		t.Fatal("投递失败")
	}
	if s.IsIdle() {
		t.Fatal("有排队消息不应为 Idle")
	}
	s.Pending.TakeAll(true)
	if !s.IsIdle() {
		t.Fatal("取尽后应回 Idle")
	}
}

// TestRestartHooks 重启链会话钩子——载荷读取 / 登记回调可注入（装配层契约）。
func TestRestartHooks(t *testing.T) {
	prevPayload := RestartRequestPayload
	prevRegister := RestartRequestRegister
	t.Cleanup(func() {
		RestartRequestPayload = prevPayload
		RestartRequestRegister = prevRegister
	})
	RestartRequestPayload = func() string { return `{"mode":"host"}` }
	got := ""
	RestartRequestRegister = func(payload string) { got = payload }
	RestartRequestRegister(RestartRequestPayload())
	if got != `{"mode":"host"}` {
		t.Fatalf("登记钩子未接到载荷：%q", got)
	}
}

// TestIsRestartToolList 重启族工具清单（请求判定单点）。
func TestIsRestartToolList(t *testing.T) {
	for _, name := range []string{"restart-full", "restart-incr", "restart-host"} {
		if !IsRestartTool(name) {
			t.Fatalf("%s 应识别为重启族", name)
		}
	}
	if IsRestartTool("majordomo-cmd") || IsRestartTool("") {
		t.Fatal("非重启族工具误判")
	}
}

// TestRegisterRestartFromBatch 批次检测（T1）——ERR 回执 / 非重启族不触发，成功回执登记并置 toolDone。
func TestRegisterRestartFromBatch(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSession(t, dir, "cat-restart")
	prevPayload := RestartRequestPayload
	prevRegister := RestartRequestRegister
	t.Cleanup(func() {
		RestartRequestPayload = prevPayload
		RestartRequestRegister = prevRegister
	})
	RestartRequestPayload = func() string { return `{"mode":"host","target":"D:\\Mau\\MauOut5"}` }
	got := ""
	RestartRequestRegister = func(payload string) { got = payload }
	// ERR 回执——不算重启请求
	s.registerRestartFromBatch([]*toolDog{{Name: "restart-host", Result: "ERR|BAD_ARGS|坏参数"}})
	if got != "" {
		t.Fatalf("ERR 回执不应登记：%q", got)
	}
	// 非重启族工具——不触发
	s.registerRestartFromBatch([]*toolDog{{Name: "text-read", Result: `{"ok":true}`}})
	if got != "" {
		t.Fatalf("非重启族不应登记：%q", got)
	}
	// 成功回执——登记 + toolDone
	s.registerRestartFromBatch([]*toolDog{{Name: "restart-host", Result: `{"ok":true}`}})
	if got != `{"mode":"host","target":"D:\\Mau\\MauOut5"}` {
		t.Fatalf("应原样登记载荷：%q", got)
	}
	r := s.roundState()
	r.mu.Lock()
	toolDone := r.toolDone
	r.mu.Unlock()
	if !toolDone {
		t.Fatal("命中重启族应置本轮工具主动 done")
	}
}
