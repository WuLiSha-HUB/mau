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

// TestResolveMountLevel 项目挂载三档归一——显式 level 优先，兼容位推导，非法回落专家。
func TestResolveMountLevel(t *testing.T) {
	cases := []struct {
		name string
		cfg  *CatCfg
		want int
	}{
		{"空档位+expert真→专家", &CatCfg{ProjectMountExpert: true}, MountLevelExpert},
		{"空档位+expert假→普通", &CatCfg{ProjectMountExpert: false}, MountLevelNormal},
		{"普通优先于expert真", &CatCfg{ProjectMountLevel: MountLevelNormal, ProjectMountExpert: true}, MountLevelNormal},
		{"精通优先", &CatCfg{ProjectMountLevel: MountLevelMaster, ProjectMountExpert: true}, MountLevelMaster},
		{"越界回落专家", &CatCfg{ProjectMountLevel: 9}, MountLevelExpert},
		{"负档位回落专家", &CatCfg{ProjectMountLevel: -1, ProjectMountExpert: false}, MountLevelExpert},
		{"nil配置→专家", nil, MountLevelExpert},
	}
	for _, c := range cases {
		if got := ResolveMountLevel(c.cfg); got != c.want {
			t.Fatalf("%s: ResolveMountLevel=%d，期望 %d", c.name, got, c.want)
		}
	}
}

// TestProjectMountLevelOmitempty 新字段 0 不落盘（旧 cat.cfg 字节形态不变），非 0 落盘且读回。
func TestProjectMountLevelOmitempty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cat.cfg")
	cfg := &CatCfg{ID: "cat-x", MountAuto: true, ProjectMountExpert: true}
	if err := SaveCatCfg(path, cfg); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "projectMountLevel") {
		t.Fatalf("0 档位不应落盘：%s", raw)
	}
	cfg.ProjectMountLevel = MountLevelMaster
	if err := SaveCatCfg(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCatCfg(path)
	if err != nil || loaded.ProjectMountLevel != MountLevelMaster {
		t.Fatalf("精通档读回不符：%v / %+v", err, loaded)
	}
}
