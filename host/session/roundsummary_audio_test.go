package session

// 轮末总结出声面单测——音频名派生 / 启用判定 / 执行体回调（A247/A265）。

import (
	"strings"
	"testing"
	"time"
)

// fakeSummarySpeaker 是播报执行体的测试替身——记录每次调用。
type fakeSummarySpeaker struct {
	// calls 调用记录（catKey / displayName / voice / text / audioName）
	calls [][5]string
}

// Speak 记录一次播报调用。
func (f *fakeSummarySpeaker) Speak(catKey, displayName, voice, text, audioName string) {
	f.calls = append(f.calls, [5]string{catKey, displayName, voice, text, audioName})
}

// TestBuildSummaryAudioName 音频名形状——时间戳按 yyyyMMdd_HHmmss_fff + 猫 key 清洗。
func TestBuildSummaryAudioName(t *testing.T) {
	ts := time.Date(2026, 11, 12, 23, 45, 6, 78*int(time.Millisecond), time.Local).UnixMilli()
	name := BuildSummaryAudioName("majordomo", ts)
	if name != "summary_20261112_234506_078_majordomo.wav" {
		t.Fatalf("音频名不符: %s", name)
	}
	cases := []struct {
		raw  string
		want string
	}{
		{"", "cat"},
		{"majordomo", "majordomo"},
		{"a/b:c d", "abcd"},
		{"猫猫", "cat"},
		{"cat-home_5", "cat-home_5"},
	}
	for _, c := range cases {
		if got := safeNameSegment(c.raw); got != c.want {
			t.Fatalf("片段清洗不符: %q → %q want %q", c.raw, got, c.want)
		}
	}
}

// TestRoundSummarySpeakOnClosed 非 ERR 收口——块 payload 带 audio 字段，执行体收到同一名字。
func TestRoundSummarySpeakOnClosed(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	office := &fakeSummaryOffice{script: []ToolOfficeState{
		{Status: 2, Result: "我把文件读完了", HasResult: true},
	}}
	speaker := &fakeSummarySpeaker{}
	sess.SetRoundSummaryOffice(office)
	sess.SetRoundSummary(true)
	sess.SetRoundSummaryLlm(func() []string { return []string{"e", "k", "m"} })
	sess.SetRoundSummaryAudioProvider(func(catKey string) []string { return []string{"1", "zf_x"} })
	sess.SetRoundSummarySpeaker(speaker)
	sess.appendRoundSummaryUser("读文件")
	sess.PostRoundSummary(5001)
	sess.pumpRoundSummary()

	wantName := BuildSummaryAudioName(sess.CatKey, 5001)
	blocks := sess.Views.Blocks()
	if len(blocks) != 1 {
		t.Fatalf("应落一块: %+v", blocks)
	}
	if blocks[0].Payload != `{"text":"我把文件读完了","audio":"`+wantName+`"}` {
		t.Fatalf("载荷不符: %s", blocks[0].Payload)
	}
	if len(speaker.calls) != 1 {
		t.Fatalf("执行体应收到一次调用: %+v", speaker.calls)
	}
	call := speaker.calls[0]
	if call[0] != sess.CatKey || call[1] != "cat" || call[2] != "zf_x" || call[3] != "我把文件读完了" || call[4] != wantName {
		t.Fatalf("执行体参数不符: %+v", call)
	}
}

// TestRoundSummarySpeakSkips 不出声三态——ERR 结果 / 开关关 / 提供者未接线。
func TestRoundSummarySpeakSkips(t *testing.T) {
	// 态 1：ERR 结果——不派生音频名、不写 audio 字段、不调执行体
	sess, _ := newRoundTestSession(t)
	speaker := &fakeSummarySpeaker{}
	sess.SetRoundSummaryOffice(&fakeSummaryOffice{script: []ToolOfficeState{{Status: 2}}})
	sess.SetRoundSummary(true)
	sess.SetRoundSummaryLlm(func() []string { return []string{"e", "k", "m"} })
	sess.SetRoundSummaryAudioProvider(func(catKey string) []string { return []string{"1", ""} })
	sess.SetRoundSummarySpeaker(speaker)
	sess.appendRoundSummaryUser("u")
	sess.PostRoundSummary(6001)
	sess.pumpRoundSummary()
	blocks := sess.Views.Blocks()
	if len(blocks) != 1 || strings.Contains(blocks[0].Payload, `"audio"`) {
		t.Fatalf("ERR 结果不应写 audio 字段: %+v", blocks)
	}
	if len(speaker.calls) != 0 {
		t.Fatalf("ERR 结果不应调执行体: %+v", speaker.calls)
	}

	// 态 2：外观开关关——块 payload 仍带 audio（与 C# 同形：字段派生与开关解耦），但不调执行体
	sess2, _ := newRoundTestSession(t)
	speaker2 := &fakeSummarySpeaker{}
	sess2.SetRoundSummaryOffice(&fakeSummaryOffice{script: []ToolOfficeState{{Status: 2, Result: "做完了", HasResult: true}}})
	sess2.SetRoundSummary(true)
	sess2.SetRoundSummaryLlm(func() []string { return []string{"e", "k", "m"} })
	sess2.SetRoundSummaryAudioProvider(func(catKey string) []string { return []string{"0", "zf_x"} })
	sess2.SetRoundSummarySpeaker(speaker2)
	sess2.appendRoundSummaryUser("u")
	sess2.PostRoundSummary(6002)
	sess2.pumpRoundSummary()
	blocks2 := sess2.Views.Blocks()
	if len(blocks2) != 1 || !strings.Contains(blocks2[0].Payload, `"audio":"summary_`) {
		t.Fatalf("开关关时 payload 仍应带 audio 字段（C# 同形）: %+v", blocks2)
	}
	if len(speaker2.calls) != 0 {
		t.Fatalf("开关关不应调执行体: %+v", speaker2.calls)
	}

	// 态 3：提供者未接线——不 panic、不调执行体
	sess3, _ := newRoundTestSession(t)
	speaker3 := &fakeSummarySpeaker{}
	sess3.SetRoundSummaryOffice(&fakeSummaryOffice{script: []ToolOfficeState{{Status: 2, Result: "做完了", HasResult: true}}})
	sess3.SetRoundSummary(true)
	sess3.SetRoundSummaryLlm(func() []string { return []string{"e", "k", "m"} })
	sess3.SetRoundSummarySpeaker(speaker3)
	sess3.appendRoundSummaryUser("u")
	sess3.PostRoundSummary(6003)
	sess3.pumpRoundSummary()
	if len(speaker3.calls) != 0 {
		t.Fatalf("提供者未接线不应调执行体: %+v", speaker3.calls)
	}
}

// TestRoundSummarySpeakName 播报名解析——majordomo 固定中文 / 显示名优先 / 回落猫 key。
func TestRoundSummarySpeakName(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	if got := sess.resolveSpeakName(); got != "cat" {
		t.Fatalf("显示名空应回落猫 key: %s", got)
	}
	sess.DisplayName = "小猫"
	if got := sess.resolveSpeakName(); got != "小猫" {
		t.Fatalf("应优先显示名: %s", got)
	}
	sess.CatKey = "majordomo"
	if got := sess.resolveSpeakName(); got != "大管家" {
		t.Fatalf("majordomo 应固定中文播报名: %s", got)
	}
}
