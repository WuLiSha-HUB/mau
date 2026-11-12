package session

import (
	"strings"
	"testing"
)

// TestViewBusLiveWhitelist 临时区白名单与非法值出声。
func TestViewBusLiveWhitelist(t *testing.T) {
	bus := NewViewBus()
	bus.Attach()
	var logs []string
	bus.SetLogf(func(level int, message, category string) { logs = append(logs, message) })
	bus.SetLive(TypeReplysse, "abc")
	if _, ok := bus.TryTakeFrame(); !ok {
		t.Fatalf("先落一个合法 live 供对照")
	}
	bus.SetLive("bogus", "x")
	if len(logs) != 1 || !strings.Contains(logs[0], "非法") {
		t.Fatalf("非法 type 应出声: %v", logs)
	}
	if frame, ok := bus.TryTakeFrame(); !ok || !strings.Contains(frame, `"type":"empty"`) {
		t.Fatalf("非法 type 应按 empty 落: %s", frame)
	}
	// 相同值零动作——初值即 empty/""，非法回落与之相同即不产帧
	bus.SetLive("bogus", "x")
	if _, ok := bus.TryTakeFrame(); ok {
		t.Fatalf("非法 type 回落 empty 且原值为 empty 时不应产帧")
	}
}

// TestViewBusFrameModes 全量 / 追加 / 增量三形态。
func TestViewBusFrameModes(t *testing.T) {
	bus := NewViewBus()
	bus.Attach()
	bus.SetState(`{"phase":"idle"}`)
	bus.PushPersist(&ViewBlock{Timestamp: 20, RenderType: TypeUser, Payload: `{"text":"b"}`, MsgIndex: 1})
	bus.PushPersist(&ViewBlock{Timestamp: 10, RenderType: TypeUser, Payload: `{"text":"a"}`, MsgIndex: 0})
	frame, ok := bus.TryTakeFrame()
	if !ok || !strings.Contains(frame, `"mode":"append"`) {
		t.Fatalf("首帧应为追加模式: %s", frame)
	}
	// 全量帧按时间戳升序
	full := bus.BuildFull()
	first := strings.Index(full, `"text":"a"`)
	second := strings.Index(full, `"text":"b"`)
	if first < 0 || second < 0 || first > second {
		t.Fatalf("全量帧排序错误: %s", full)
	}
	if !strings.Contains(full, `"v":2`) || !strings.Contains(full, `"mode":"full"`) {
		t.Fatalf("全量帧结构错误: %s", full)
	}
	// 重置 → 全量待发
	bus.ResetAll()
	if _, ok := bus.TryTakeFrame(); !ok {
		t.Fatalf("重置后应产全量帧")
	}
	if bus.HasPendingContent() {
		t.Fatalf("取帧后不应再有待推内容")
	}
}

// TestViewBusUnattached 未接线时全部入口静默。
func TestViewBusUnattached(t *testing.T) {
	bus := NewViewBus()
	bus.SetLive(TypeThinksse, "x")
	bus.PushPersist(&ViewBlock{Timestamp: 1, RenderType: TypeUser})
	if _, ok := bus.TryTakeFrame(); ok {
		t.Fatalf("未接线不应产帧")
	}
	if bus.HasPendingContent() {
		t.Fatalf("未接线不应有待推内容")
	}
	if bus.Ready() {
		t.Fatalf("未接线不应 Ready")
	}
}

// TestViewBusHasPendingContent 内容待推判定不含 state 段。
func TestViewBusHasPendingContent(t *testing.T) {
	bus := NewViewBus()
	bus.Attach()
	bus.SetState(`{"phase":"idle"}`)
	if bus.HasPendingContent() {
		t.Fatalf("仅 state 变化不算内容待推")
	}
	bus.SetLive(TypeReplysse, "abc")
	if !bus.HasPendingContent() {
		t.Fatalf("live 变化应算内容待推")
	}
	bus.ResetPending()
	if bus.HasPendingContent() {
		t.Fatalf("复位后不应有待推内容")
	}
}

// TestSysInject 系统注入判定与幂等前缀。
func TestSysInject(t *testing.T) {
	if !IsKnownSysKind(SysKindSystemAuto) || !IsKnownSysKind(SysKindTimer) || IsKnownSysKind("bogus") {
		t.Fatalf("来源值域判定错误")
	}
	if IsSysSource("") || IsSysSource(SourceUser) || !IsSysSource(SysKindSleep) {
		t.Fatalf("sys 路径判定错误")
	}
	once := BuildSysInjectText("内容")
	if once != SysInjectPrefix+"内容" {
		t.Fatalf("套前缀 = %q", once)
	}
	if twice := BuildSysInjectText(once); twice != once {
		t.Fatalf("前缀应幂等: %q", twice)
	}
	if !IsSysInjectText(once) || IsSysInjectText("内容") {
		t.Fatalf("行头判定错误")
	}
	if MessageBlockKind(SysKindTimer) != TypeSystemAutoMessage || MessageBlockKind(SourceUser) != TypeUser {
		t.Fatalf("块类型分派错误")
	}
}
