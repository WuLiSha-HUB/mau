package config

import "testing"

// TestResolveTplEnabledInject 模板启用集——禁用条目剔除 + 挂载段开关位（@mount:）不进继承。
func TestResolveTplEnabledInject(t *testing.T) {
	tpl := &CatDefault{
		DefaultInjectList: []string{
			"ccbp:SOUL.md",
			"ccbp:L2/Worker/relic",
			"@mount:l3@Mau",
			"ccbp:L1",
		},
		DefaultInjectDisabled: []string{"ccbp:L2/Worker/relic"},
	}
	got := ResolveTplEnabledInject(tpl)
	want := []string{"ccbp:SOUL.md", "ccbp:L1"}
	if len(got) != len(want) {
		t.Fatalf("启用集不符: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("启用集不符: %v", got)
		}
	}
	if got := ResolveTplEnabledInject(nil); len(got) != 0 {
		t.Fatalf("nil 模板应返回空: %v", got)
	}
	if got := ResolveTplEnabledInject(&CatDefault{}); len(got) != 0 {
		t.Fatalf("空清单应返回空: %v", got)
	}
}
