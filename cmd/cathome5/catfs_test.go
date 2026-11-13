package main

import (
	"testing"

	"cathome5/host/config"
)

// TestCatFSRegistryRebuild 猫级根表三态——空白名单（仅常驻根）/ 子集 / 载荷按 catId 取。
func TestCatFSRegistryRebuild(t *testing.T) {
	ws := &config.WorkspaceConfig{Roots: []config.RootEntry{
		{ID: "workspace", Path: t.TempDir()},
		{ID: "runtime", Path: t.TempDir()},
		{ID: "mau", Path: t.TempDir()},
		{ID: "ccbp", Path: t.TempDir()},
	}}
	reg := newCatFSRegistry(ws, nil)
	// 空白名单——只剩 workspace + runtime（失败默认收紧，不回落全量）
	reg.Rebuild("catA", &config.CatCfg{})
	svcA := reg.Resolve("cata")
	if svcA == nil || len(svcA.Roots) != 2 {
		t.Fatalf("空白名单应只剩常驻根：%+v", svcA)
	}
	// 子集——mau + 常驻根
	reg.Rebuild("catB", &config.CatCfg{EnabledRoots: []string{"mau"}})
	svcB := reg.Resolve("CATB")
	if svcB == nil || len(svcB.Roots) != 3 {
		t.Fatalf("子集根数不符：%+v", svcB)
	}
	// 池外 id——剔除后仍只剩常驻根
	reg.Rebuild("catC", &config.CatCfg{EnabledRoots: []string{"nope"}})
	if svcC := reg.Resolve("catC"); svcC == nil || len(svcC.Roots) != 2 {
		t.Fatalf("池外 id 应被剔除：%+v", svcC)
	}
	// ServiceFor 按载荷 catId 取；缺 catId 回退 nil（调用方回退全局）
	if svc := reg.ServiceFor(`{"catId":"catB","path":"x"}`); svc == nil {
		t.Fatal("ServiceFor 未命中 catB")
	}
	if svc := reg.ServiceFor(`{"path":"x"}`); svc != nil {
		t.Fatal("缺 catId 应返回 nil")
	}
	// Drop 后不可解析
	reg.Drop("catB")
	if reg.Resolve("catB") != nil {
		t.Fatal("Drop 后仍可解析")
	}
}
