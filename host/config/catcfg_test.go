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

// TestResolveMountLevel 项目挂载三档归一——显式 level 优先，全局 mount.level 次之，兼容位推导，非法回落专家。
func TestResolveMountLevel(t *testing.T) {
	defer SetDefaultMountLevel(0) // 全局投影是进程态——用例结束清干净，避免污染其它测试
	cases := []struct {
		name   string
		cfg    *CatCfg
		global int
		want   int
	}{
		{"空档位+expert真→专家", &CatCfg{ProjectMountExpert: true}, 0, MountLevelExpert},
		{"空档位+expert假→普通", &CatCfg{ProjectMountExpert: false}, 0, MountLevelNormal},
		{"普通优先于expert真", &CatCfg{ProjectMountLevel: MountLevelNormal, ProjectMountExpert: true}, 0, MountLevelNormal},
		{"精通优先", &CatCfg{ProjectMountLevel: MountLevelMaster, ProjectMountExpert: true}, 0, MountLevelMaster},
		{"越界回落专家", &CatCfg{ProjectMountLevel: 9}, 0, MountLevelExpert},
		{"负档位回落专家", &CatCfg{ProjectMountLevel: -1, ProjectMountExpert: false}, 0, MountLevelExpert},
		{"nil配置→专家", nil, 0, MountLevelExpert},
		{"全局普通覆盖expert真", &CatCfg{ProjectMountExpert: true}, MountLevelNormal, MountLevelNormal},
		{"全局精通覆盖expert假", &CatCfg{ProjectMountExpert: false}, MountLevelMaster, MountLevelMaster},
		{"每猫显式优先于全局精通", &CatCfg{ProjectMountLevel: MountLevelNormal, ProjectMountExpert: true}, MountLevelMaster, MountLevelNormal},
		{"nil配置+全局专家→专家", nil, MountLevelExpert, MountLevelExpert},
	}
	for _, c := range cases {
		SetDefaultMountLevel(c.global)
		if got := ResolveMountLevel(c.cfg); got != c.want {
			t.Fatalf("%s: ResolveMountLevel=%d，期望 %d", c.name, got, c.want)
		}
	}
}

// TestMountLevelSetting 全局挂载档位的配置值解析与进程内投影同步。
func TestMountLevelSetting(t *testing.T) {
	defer SetDefaultMountLevel(0)
	cases := []struct {
		value string
		want  int
	}{
		{"0", 0}, {"1", 1}, {"2", 2}, {"3", 3},
		{" 3 ", 3}, {"", 0}, {"4", 0}, {"-1", 0}, {"high", 0},
	}
	for _, c := range cases {
		if got := MountLevelFromSetting(c.value); got != c.want {
			t.Fatalf("MountLevelFromSetting(%q)=%d，期望 %d", c.value, got, c.want)
		}
	}
	// 投影：合法值写入可读回，非法值清零
	SetDefaultMountLevel(MountLevelMaster)
	if got := DefaultMountLevel(); got != MountLevelMaster {
		t.Fatalf("投影读回=%d，期望 %d", got, MountLevelMaster)
	}
	SetDefaultMountLevel(9)
	if got := DefaultMountLevel(); got != 0 {
		t.Fatalf("非法值应清零，实得 %d", got)
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
