package toolpool

import "testing"

// buildTestRegistry 造一个三工具注册表：普通 / 特权 / 受限。
func buildTestRegistry(t *testing.T) *Registry {
	t.Helper()
	groups := []GroupSource{
		{Name: "TextCat", JSON: `{"group":"TextCat","tools":[{"name":"text-read","description":"读取文件","parameters":{"type":"object","properties":{}}}]}`},
		{Name: "GroupCat", JSON: `{"group":"GroupCat","tools":[{"name":"group-catcfg-set","description":"组内配置写","parameters":{"type":"object","properties":{}}}]}`},
		{Name: "Majordomo", JSON: `{"group":"Majordomo","privileged":true,"tools":[{"name":"majordomo-cmd","description":"宿主管理指令","parameters":{"type":"object","properties":{}}}]}`},
	}
	RebuildAll(groups, `{"group":"","tools":[]}`)
	pool := Current()
	return NewRegistry(pool.BuildSpecs(), pool.BuildOwnerFlowMap(), pool.BuildPrivilegedMap())
}

// TestConfigurableAndFallback 可配置清单去特权、全量保底去受限。
func TestConfigurableAndFallback(t *testing.T) {
	reg := buildTestRegistry(t)
	configurable := reg.GetConfigurableToolNames()
	if containsName(configurable, "majordomo-cmd") {
		t.Fatalf("可配置清单不应含特权工具: %v", configurable)
	}
	fallback := reg.FallbackConfigurableNames()
	if containsName(fallback, "group-catcfg-set") {
		t.Fatalf("全量保底不应含受限工具: %v", fallback)
	}
	if len(fallback) != 1 || fallback[0] != "text-read" {
		t.Fatalf("全量保底 = %v，期望 [text-read]", fallback)
	}
}

// TestResolveToolNames 名单解析三条保底路径与特权并入。
func TestResolveToolNames(t *testing.T) {
	reg := buildTestRegistry(t)
	// 空名单 + 含特权：保底（去受限）+ 特权
	got := reg.ResolveToolNames("", true)
	if !containsName(got, "text-read") || !containsName(got, "majordomo-cmd") || containsName(got, "group-catcfg-set") {
		t.Fatalf("空名单 + 含特权 = %v", got)
	}
	// 空名单 + 去特权：只有可配置保底
	got = reg.ResolveToolNames("", false)
	if containsName(got, "majordomo-cmd") {
		t.Fatalf("非默认猫不应含特权: %v", got)
	}
	// 显式名单命中
	got = reg.ResolveToolNames("text-read", false)
	if len(got) != 1 || got[0] != "text-read" {
		t.Fatalf("显式名单 = %v，期望 [text-read]", got)
	}
	// 名单全非法 → 保底
	got = reg.ResolveToolNames("nope,nada", false)
	if len(got) != 1 || got[0] != "text-read" {
		t.Fatalf("全非法名单 = %v，期望保底 [text-read]", got)
	}
	// "*" → 保底
	got = reg.ResolveToolNames("*", true)
	if !containsName(got, "majordomo-cmd") {
		t.Fatalf("通配 + 含特权 = %v", got)
	}
}

// TestFilterToolSpecs 声明面裁剪：特权面按 includePrivileged，全空保底。
func TestFilterToolSpecs(t *testing.T) {
	reg := buildTestRegistry(t)
	specs := reg.FilterToolSpecs([]string{"text-read"}, false)
	if len(specs) != 1 || specs[0].Name != "text-read" {
		t.Fatalf("裁剪 = %v", specs)
	}
	specs = reg.FilterToolSpecs(nil, true)
	if len(specs) != 1 || specs[0].Name != "majordomo-cmd" {
		t.Fatalf("空名单 + 含特权 = %v（非空不走保底，只剩特权面）", specs)
	}
	specs = reg.FilterToolSpecs(nil, false)
	if len(specs) != 2 {
		t.Fatalf("空名单 + 去特权全空保底 = %d 件，期望 2: %v", len(specs), specs)
	}
	for _, spec := range specs {
		if spec.Name == "majordomo-cmd" {
			t.Fatalf("非默认猫全量保底不应含特权: %v", specs)
		}
	}
}

// TestResolveCatAuthorized 默认猫读默认名单、多猫读自身名单。
func TestResolveCatAuthorized(t *testing.T) {
	reg := buildTestRegistry(t)
	// 默认猫（majordomo）：默认串 + 特权并入
	got := reg.ResolveCatAuthorizedFromCfg("majordomo", "text-read", "group-catcfg-set")
	if !containsName(got, "text-read") || !containsName(got, "majordomo-cmd") {
		t.Fatalf("默认猫授权集 = %v", got)
	}
	// 多猫：读 catRaw，去特权
	got = reg.ResolveCatAuthorizedFromCfg("coder", "text-read", "")
	if len(got) != 1 || got[0] != "text-read" {
		t.Fatalf("多猫空名单 = %v，期望保底去特权 [text-read]", got)
	}
	// IsDefaultCatKey
	if !IsDefaultCatKey("") || !IsDefaultCatKey("majordomo") || IsDefaultCatKey("coder") {
		t.Fatalf("IsDefaultCatKey 判定错误")
	}
}
