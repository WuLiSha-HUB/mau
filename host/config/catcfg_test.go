package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// TestCatCfgCacheKey 缓存隔离键字段——落盘含 cacheKey（非空）/ 空值 omitempty 不写（复活猫 B）。
func TestCatCfgCacheKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cat.cfg")
	cfg := &CatCfg{ID: "cat-1", DisplayName: "猫一", MountAuto: true, ProjectMountExpert: true, CacheKey: "proj-Mau"}
	if err := SaveCatCfg(path, cfg); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"cacheKey":"proj-Mau"`) {
		t.Fatalf("落盘应含 cacheKey：%s", raw)
	}
	loaded, err := LoadCatCfg(path)
	if err != nil || loaded.CacheKey != "proj-Mau" {
		t.Fatalf("读回 cacheKey 不符：%v / %+v", err, loaded)
	}
	// 空值 omitempty——不落盘（旧文件不因新增字段改变字节形态）
	loaded.CacheKey = ""
	if err := SaveCatCfg(path, loaded); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "cacheKey") {
		t.Fatalf("空 cacheKey 不应落盘：%s", raw)
	}
}
