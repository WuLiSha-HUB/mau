package toolpool

import "testing"

// TestComputeDrift 漂移三面判定：added 需全过，removed 按声明面差集。
func TestComputeDrift(t *testing.T) {
	reg := buildTestRegistry(t)
	declared := []string{"text-read", "group-catcfg-set"}
	authorized := []string{"text-read", "majordomo-cmd"}
	allowed := func(name string) bool { return name == "text-read" }
	drift := ComputeDrift(reg, declared, authorized, allowed)
	if drift.Empty() {
		t.Fatalf("漂移不应为空")
	}
	if len(drift.Added) != 0 {
		t.Fatalf("added = %v，majordomo-cmd 三面不过（非默认会话），不应报", drift.Added)
	}
	if len(drift.Removed) != 1 || drift.Removed[0] != "group-catcfg-set" {
		t.Fatalf("removed = %v，期望 [group-catcfg-set]", drift.Removed)
	}
	// 全过 → added 命中
	drift = ComputeDrift(reg, declared, authorized, func(string) bool { return true })
	if len(drift.Added) != 1 || drift.Added[0].Name != "majordomo-cmd" {
		t.Fatalf("added = %v，期望 [majordomo-cmd]", drift.Added)
	}
	// 一致 → 空漂移
	drift = ComputeDrift(reg, declared, declared, nil)
	if !drift.Empty() {
		t.Fatalf("同集漂移应为空: %+v", drift)
	}
}

// TestFirstSentence 首句截取与超长截断。
func TestFirstSentence(t *testing.T) {
	if got := FirstSentence("读取 UTF-8 文本文件（编码自动探测），返回完整内容", 60); got != "读取 UTF-8 文本文件（编码自动探测），返回完整内容" {
		t.Fatalf("无句读整句 = %q", got)
	}
	if got := FirstSentence("第一句。第二句。", 60); got != "第一句" {
		t.Fatalf("句读截取 = %q，期望 第一句", got)
	}
	if got := FirstSentence("abcdefghij", 4); got != "abcd…" {
		t.Fatalf("超长截断 = %q，期望 abcd…", got)
	}
	if got := FirstSentence("", 60); got != "" {
		t.Fatalf("空描述 = %q", got)
	}
}

// TestBuildArgKeys 参数键摘要：声明序 + 必填标星。
func TestBuildArgKeys(t *testing.T) {
	params := `{"type":"object","properties":{"path":{"type":"string"},"start":{"type":"integer"},"end":{"type":"integer"}},"required":["path"]}`
	if got := BuildArgKeys(params); got != "path*、start、end" {
		t.Fatalf("参数键 = %q，期望 path*、start、end", got)
	}
	if got := BuildArgKeys(`{"type":"object","properties":{}}`); got != "" {
		t.Fatalf("无参 = %q", got)
	}
	if got := BuildArgKeys("not-json"); got != "" {
		t.Fatalf("不可析 = %q", got)
	}
}

// TestBuiltinJSONShape 内置定义可解析且 11 件齐备。
func TestBuiltinJSONShape(t *testing.T) {
	RebuildAll(nil, BuiltinToolsJSON())
	pool := Current()
	names := pool.AllNames()
	if len(names) != 11 {
		t.Fatalf("内置 %d 件，期望 11: %v", len(names), names)
	}
	want := []string{"Note", "time", "random", "info", "host-reload", "host-flows", "pack", "sleep", "timer", "timeback-start", "timeback-back"}
	for i, name := range want {
		if names[i] != name {
			t.Fatalf("内置顺序第 %d = %s，期望 %s", i, names[i], name)
		}
	}
	if defects := pool.Defects(); len(defects) != 0 {
		t.Fatalf("内置解析缺陷: %v", defects)
	}
}
