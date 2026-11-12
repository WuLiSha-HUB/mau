package toolpool

import (
	"os"
	"testing"
)

// TestLoadRealBricks 真实 Bricks 索引聚合（环境变量 CAT5_BRICKS_INDEX 指定；未设置跳过）。
//
// 守卫生效面：20 个工具组定义积木全部解析成功、零缺陷、零空定义。
func TestLoadRealBricks(t *testing.T) {
	index := os.Getenv("CAT5_BRICKS_INDEX")
	if index == "" {
		// 本机默认位置（不存在则跳过——不把机器布局变成测试硬依赖）
		candidate := `D:\Mau\Mau\Bricks\index.json`
		if _, err := os.Stat(candidate); err == nil {
			index = candidate
		}
	}
	if index == "" {
		t.Skip("未定位 Bricks 索引——跳过真实索引验证")
	}
	groups, err := LoadFromBricks(index)
	if err != nil {
		t.Fatalf("真实索引聚合失败: %v", err)
	}
	if len(groups) < 15 {
		t.Fatalf("组数 = %d，期望 ≥15（工具组定义积木）", len(groups))
	}
	for _, group := range groups {
		if group.JSON == "" {
			t.Fatalf("组 %s 定义为空（解析失败）", group.Name)
		}
	}
	RebuildAll(groups, BuiltinToolsJSON())
	pool := Current()
	if defects := pool.Defects(); len(defects) != 0 {
		t.Fatalf("真实索引装配缺陷 %d 条: %v", len(defects), defects)
	}
	if pool.MemberCount() <= 11 {
		t.Fatalf("池内工具 = %d，期望 >11（内置 11 + 组工具）", pool.MemberCount())
	}
	// 执行序未登记——C# 侧既有缺口（2026-11-04 日志：ts-* 5 件；GroupCat 8 件为后续新增，
	// 两侧同为「落默认档 0 + WARN 出声」）。守卫：只允许这 13 件，超出即移植漂移。
	knownGaps := map[string]bool{
		"ts-check": true, "ts-list": true, "ts-read": true, "ts-find-ref": true, "ts-patch": true,
		"group-info": true, "group-inbox": true, "group-post": true, "group-delegate": true,
		"group-newsession": true, "group-catinfo": true, "group-catcfg-get": true, "group-catcfg-set": true,
	}
	for _, name := range FindUnregistered(pool.AllNames()) {
		if !knownGaps[name] {
			t.Fatalf("执行序未登记超出 C# 既有缺口: %s", name)
		}
	}
}
