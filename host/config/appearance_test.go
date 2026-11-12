package config

// 外观池解析面单测——轮末总结开关与总结出声配置的生效口径（A247/A265）。

import "testing"

// testAppearancePool 构造两档外观池——默认条目全开，显式条目全关。
func testAppearancePool() []AppearanceConfig {
	return []AppearanceConfig{
		{
			AppearanceID: ParseGUID("11111111-1111-1111-1111-111111111111"),
			DisplayName:  "默认外观",
			TtsStyle:     "zf_xiaoxiao",
			RoundSummary: true,
			SummaryAudio: true,
			IsDefault:    true,
		},
		{
			AppearanceID: ParseGUID("22222222-2222-2222-2222-222222222222"),
			DisplayName:  "静默外观",
			TtsStyle:     "",
			RoundSummary: false,
			SummaryAudio: false,
			IsDefault:    false,
		},
	}
}

// TestResolveSummaryAudio 总结出声配置——默认跟随 / 显式绑定 / 引用失效 / 空池四态。
func TestResolveSummaryAudio(t *testing.T) {
	pool := testAppearancePool()
	cases := []struct {
		name         string
		appearanceID string
		wantEnabled  bool
		wantVoice    string
	}{
		{"零值跟随默认", "", true, "zf_xiaoxiao"},
		{"零 GUID 跟随默认", "00000000-0000-0000-0000-000000000000", true, "zf_xiaoxiao"},
		{"非法身份跟随默认", "not-a-guid", true, "zf_xiaoxiao"},
		{"显式绑定静默条目", "22222222-2222-2222-2222-222222222222", false, ""},
		{"显式绑定已删条目", "33333333-3333-3333-3333-333333333333", false, ""},
	}
	for _, c := range cases {
		enabled, voice := ResolveSummaryAudio(pool, c.appearanceID)
		if enabled != c.wantEnabled || voice != c.wantVoice {
			t.Fatalf("%s: got (%v, %q) want (%v, %q)", c.name, enabled, voice, c.wantEnabled, c.wantVoice)
		}
	}
	if enabled, voice := ResolveSummaryAudio(nil, ""); enabled || voice != "" {
		t.Fatalf("空池应保守关闭: (%v, %q)", enabled, voice)
	}
}

// TestResolveRoundSummary 轮末总结开关——与出声配置同一套生效解析，不复用出声音色。
func TestResolveRoundSummary(t *testing.T) {
	pool := testAppearancePool()
	if !ResolveRoundSummary(pool, "") {
		t.Fatalf("零值应跟随默认条目（开）")
	}
	if ResolveRoundSummary(pool, "22222222-2222-2222-2222-222222222222") {
		t.Fatalf("显式绑定静默条目应为关")
	}
	if ResolveRoundSummary(pool, "33333333-3333-3333-3333-333333333333") {
		t.Fatalf("引用失效应保守关闭")
	}
	if ResolveRoundSummary(nil, "") {
		t.Fatalf("空池应保守关闭")
	}
}
