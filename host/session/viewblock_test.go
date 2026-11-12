package session

import (
	"strings"
	"testing"
)

// TestBuildToolCard 工具卡九字段与 result 三态。
func TestBuildToolCard(t *testing.T) {
	empty := ""
	card := BuildToolCard("text-read", `{"path":"a"}`, nil, 1, 2, 12, 3, 9)
	wantOrder := `"order":"-1"`
	if !strings.Contains(card, wantOrder) {
		t.Fatalf("order 字段缺失或错误: %s", card)
	}
	if strings.Contains(card, `"result"`) {
		t.Fatalf("先行卡不应写 result: %s", card)
	}
	card = BuildToolCard("text-read", `{"path":"a"}`, &empty, 1, 1, 0, -1, -1)
	if !strings.Contains(card, `"result":""`) {
		t.Fatalf("空结果应写 result 字段: %s", card)
	}
	keys := []string{`"name"`, `"arguments"`, `"result"`, `"toolIndex"`, `"toolTotal"`, `"order"`, `"durMs"`, `"queueMs"`, `"runMs"`}
	last := -1
	for _, key := range keys {
		idx := strings.Index(card, key)
		if idx <= last {
			t.Fatalf("字段序不符（%s 位置 %d <= %d）: %s", key, idx, last, card)
		}
		last = idx
	}
}

// TestBuildReason 思考块计数与 cps。
func TestBuildReason(t *testing.T) {
	out := BuildReason("abcdef", 1000)
	if !strings.Contains(out, `"chars":6`) || !strings.Contains(out, `"cps":6`) {
		t.Fatalf("cps 计算错误: %s", out)
	}
	out = BuildReason("abc", -1)
	if !strings.Contains(out, `"cps":-1`) {
		t.Fatalf("durMs=-1 时 cps 应为 -1: %s", out)
	}
	out = BuildReason("中文文本", 2000)
	if !strings.Contains(out, `"chars":4`) {
		t.Fatalf("chars 按字符数（非字节）: %s", out)
	}
}

// TestViewBlockItemJSON 条目序列化五字段与 payload null 回落。
func TestViewBlockItemJSON(t *testing.T) {
	block := &ViewBlock{Timestamp: 1700000000000, RenderType: TypeUser, Payload: `{"text":"hi"}`, MsgIndex: 3, Round: 2}
	want := `{"type":"user","ts":1700000000000,"msgIndex":3,"round":2,"payload":{"text":"hi"}}`
	if got := block.ItemJSON(); got != want {
		t.Fatalf("条目 = %s，期望 %s", got, want)
	}
	block.Payload = ""
	if got := block.ItemJSON(); !strings.Contains(got, `"payload":null`) {
		t.Fatalf("空载荷应落 null: %s", got)
	}
}

// TestIsPersistType 持久族判定——临时族与 empty 不入表。
func TestIsPersistType(t *testing.T) {
	for _, rt := range []string{TypeUser, TypeSystemAutoMessage, TypeText, TypeReason, TypeToolCard, TypeRoundSum, TypeRoundSummary} {
		if !IsPersistType(rt) {
			t.Fatalf("%s 应为持久族", rt)
		}
	}
	for _, rt := range []string{TypeThinksse, TypeReplysse, TypeToolrun, TypeEmpty} {
		if IsPersistType(rt) {
			t.Fatalf("%s 不应为持久族", rt)
		}
	}
}
