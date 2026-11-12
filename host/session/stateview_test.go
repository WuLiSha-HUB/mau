package session

import (
	"strings"
	"sync"
	"testing"
)

// TestBuildStateJSONShape 状态段十一项字段面与顺序（对齐 C# BuildStateJson）。
func TestBuildStateJSONShape(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSession(t, dir, "cat-state")
	s.SetDisplayName("猫甲")
	s.SetPetKind("livo")
	s.Note.Execute(`{"action":"set","content":["a","b"]}`)

	state := s.BuildStateJSON()
	order := []string{
		`{"sessionId":"cat-state"`, `"runState":"idle"`,
		`"runMs":{"idle":0,"wait":`, `"requests":0`,
		`"note":{"tasks":["a","b"],"current":0,"done":0,"justCompleted":false}`,
		`"delay":{"entries":[]}`, `"pending":{"entries":[]}`,
		`"conn":{"server":"ok","clients":0}`,
		`"tokens":{"prompt":0,"completion":0,"cacheHit":0,"miss":0,"rate":0,"context":0,"count":0,`,
		`"meta":{"catId":"cat-state","displayName":"猫甲","sessionId":`,
		`"pet":{"kind":"livo"}}`,
	}
	pos := 0
	for _, want := range order {
		idx := strings.Index(state, want)
		if idx < 0 {
			t.Fatalf("状态段缺片段 %q:\n%s", want, state)
		}
		if idx < pos {
			t.Fatalf("状态段字段顺序错位（%q）:\n%s", want, state)
		}
		pos = idx
	}
	// tokens 十二键全量
	for _, key := range []string{"sessionPrompt", "sessionCompletion", "sessionCacheHit", "sessionMiss", "sessionRate"} {
		if !strings.Contains(state, `"`+key+`":`) {
			t.Fatalf("tokens 段缺 %s:\n%s", key, state)
		}
	}
	// meta 七键全量
	for _, key := range []string{"catId", "displayName", "sessionId", "createdAt", "lastActiveAt", "contextCount", "contextChars"} {
		if !strings.Contains(state, `"`+key+`":`) {
			t.Fatalf("meta 段缺 %s:\n%s", key, state)
		}
	}
}

// TestBuildStateJSONTokens 轮级 token 两口径——Idle 回落落盘快照，非 Idle 取实时累计。
func TestBuildStateJSONTokens(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSession(t, dir, "cat-tokens")
	r := s.roundState()
	r.mu.Lock()
	r.phase = chatPhaseLlmRunning
	r.usagePrompt = 100
	r.usageCacheHit = 40
	r.usageCompletion = 7
	r.mu.Unlock()
	state := s.BuildStateJSON()
	if !strings.Contains(state, `"tokens":{"prompt":100,"completion":7,"cacheHit":40,"miss":60,"rate":0.4,`) {
		t.Fatalf("非 Idle 应取实时累计:\n%s", state)
	}
	r.mu.Lock()
	r.phase = chatPhaseIdle
	r.mu.Unlock()
	state = s.BuildStateJSON()
	if !strings.Contains(state, `"tokens":{"prompt":0,"completion":0,"cacheHit":0,"miss":0,"rate":0,`) {
		t.Fatalf("Idle 应回落落盘边界快照:\n%s", state)
	}
}

// TestBuildStateJSONDelayNote delay 段空口径且只出声一次。
func TestBuildStateJSONDelayNote(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSession(t, dir, "cat-delay")
	var mu sync.Mutex
	var logs []string
	prevLogf := Logf
	Logf = func(module string, level int, message, category string) {
		mu.Lock()
		logs = append(logs, message)
		mu.Unlock()
	}
	t.Cleanup(func() { Logf = prevLogf })
	s.BuildStateJSON()
	s.BuildStateJSON()
	mu.Lock()
	defer mu.Unlock()
	count := 0
	for _, line := range logs {
		if strings.Contains(line, "延迟队列未实现") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("delay 空口径应只出声一次，实际 %d 次: %v", count, logs)
	}
}

// TestAttachConnInfo conn 段——连接数提供器与停机态。
func TestAttachConnInfo(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSession(t, dir, "cat-conn")
	s.AttachConnInfo(func() int { return 3 })
	if state := s.BuildStateJSON(); !strings.Contains(state, `"conn":{"server":"ok","clients":3}`) {
		t.Fatalf("连接数未进状态段:\n%s", state)
	}
	prevRestart := HostRestarting
	HostRestarting = func() bool { return true }
	t.Cleanup(func() { HostRestarting = prevRestart })
	if state := s.BuildStateJSON(); !strings.Contains(state, `"conn":{"server":"stopping","clients":3}`) {
		t.Fatalf("停机态未进状态段:\n%s", state)
	}
}

// TestViewFullAndFrame 全量帧 / 增量帧两路径——建块即推，取走即清。
func TestViewFullAndFrame(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSession(t, dir, "cat-view")
	s.AttachView()
	if !s.ViewBus.Ready() {
		t.Fatalf("AttachView 后总线应就绪")
	}
	s.Views.AppendGapText("工具轮间隙", 1000)
	frame := s.TakeViewFrameJSON()
	if !strings.Contains(frame, `"persist":{"mode":"append"`) || !strings.Contains(frame, "工具轮间隙") {
		t.Fatalf("增量帧应含追加块:\n%s", frame)
	}
	if !strings.Contains(frame, `"state":`) {
		t.Fatalf("增量帧应含状态段:\n%s", frame)
	}
	if again := s.TakeViewFrameJSON(); again != "" {
		t.Fatalf("无变化应零字节，实际:\n%s", again)
	}
	full := s.BuildViewFullJSON()
	if !strings.Contains(full, `"v":2`) || !strings.Contains(full, `"mode":"full"`) || !strings.Contains(full, "工具轮间隙") {
		t.Fatalf("全量帧结构错误:\n%s", full)
	}
}

// TestViewResetAllFullPending 重置置全量待发——下一次取帧为全量帧（重置路径 = 会话层 ClearView + PushSessionReset）。
func TestViewResetAllFullPending(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSession(t, dir, "cat-reset")
	s.AttachView()
	s.Views.AppendGapText("旧块", 1000)
	s.ClearView()
	s.PushSessionReset()
	frame := s.TakeViewFrameJSON()
	if !strings.Contains(frame, `"mode":"full"`) || strings.Contains(frame, "旧块") {
		t.Fatalf("重置后应推空全量帧:\n%s", frame)
	}
}
