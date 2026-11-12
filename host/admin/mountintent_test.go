package admin

import (
	"path/filepath"
	"strings"
	"testing"

	"cathome5/host/config"
	"cathome5/host/session"
)

// TestParseMountIntent 挂载意图解析——触发词 / 首行 / 名字段边界 / 别名 / 路径形态 / 多项目 / 未识别。
func TestParseMountIntent(t *testing.T) {
	deps, root := newTestDeps(t)
	seedFullProject(t, root, "Demo")
	seedFullProject(t, root, "Other")
	seedFullProject(t, root, "ClashVerge")

	cases := []struct {
		name    string
		content string
		project string
		source  string
		raw     string
		extras  string
	}{
		{"精确命中", "上工 Demo", "Demo", "exact", "Demo", ""},
		{"开工同义", "开工 Other", "Other", "exact", "Other", ""},
		{"只看首行", "上工 Demo\n上工 Other", "Demo", "exact", "Demo", ""},
		{"名字后接句读", "上工 Demo，先读交接文件", "Demo", "exact", "Demo", ""},
		{"名字为空不算意图", "上工。任务卡：xxx", "", "", "", ""},
		{"无触发词", "帮我看下 Demo 的进度", "", "", "", ""},
		{"别名", "开工 clash", "ClashVerge", "alias", "clash", ""},
		{"受控根形态取末段", "上工 ccbp:Project/Demo", "Demo", "exact", "ccbp:Project/Demo", ""},
		{"多项目", "上工 Demo和Other", "Demo", "exact", "Demo", "Other"},
		{"模糊不成不猜", "上工 demx", "", "", "", ""},
		{"未识别出声素材", "上工 zzz不存在", "", "miss", "zzz不存在", ""},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			intent := deps.parseMountIntent(item.content)
			if intent.Project != item.project {
				t.Fatalf("Project=%q 期望 %q（raw=%q source=%q）", intent.Project, item.project, intent.Raw, intent.Source)
			}
			if item.project != "" && intent.Source != item.source {
				t.Fatalf("Source=%q 期望 %q", intent.Source, item.source)
			}
			if item.raw != "" && intent.Raw != item.raw {
				t.Fatalf("Raw=%q 期望 %q", intent.Raw, item.raw)
			}
			if strings.Join(intent.Extras, ",") != item.extras {
				t.Fatalf("Extras=%v 期望 %q", intent.Extras, item.extras)
			}
		})
	}
}

// TestParseMountIntentFuzzy 模糊命中——token 与目录名互相包含且唯一才成立（多候选判未解析）。
func TestParseMountIntentFuzzy(t *testing.T) {
	deps, root := newTestDeps(t)
	seedFullProject(t, root, "ApiSiteAnalyzer")
	// token 包含目录名（analyzer 是目录名的后缀）→ 唯一命中，source=fuzzy
	intent := deps.parseMountIntent("上工 apisiteanalyzerx")
	if intent.Project != "ApiSiteAnalyzer" || intent.Source != "fuzzy" {
		t.Fatalf("唯一模糊命中失败：project=%q source=%q", intent.Project, intent.Source)
	}
	// 多候选——两个目录名都包含 token 时判未解析（宁可不挂，不错挂）
	seedFullProject(t, root, "FooBar")
	seedFullProject(t, root, "FooBaz")
	multi := deps.parseMountIntent("上工 foo")
	if multi.Project != "" {
		t.Fatalf("多候选应判未解析，实得 project=%q", multi.Project)
	}
}

// TestTryAutoMountOnMessageGuards mountAuto 消费面的四条前置判据——开关关闭 / 已挂载项目不挂。
func TestTryAutoMountOnMessageGuards(t *testing.T) {
	deps, root := newTestDeps(t)
	seedFullProject(t, root, "Demo")
	deps.Session = &session.Session{}

	// 开关关闭 → 不挂（消息按普通输入投递）
	setMountFlags(t, deps, false, "")
	if deps.TryAutoMountOnMessage("上工 Demo") {
		t.Fatal("mountAuto=false 时不应接管投递")
	}
	// 已挂载项目 → 不重挂
	setMountFlags(t, deps, true, "Other")
	if deps.TryAutoMountOnMessage("上工 Demo") {
		t.Fatal("已挂载项目时不应接管投递")
	}
	// 项目不存在 → 落盘失败，不接管
	setMountFlags(t, deps, true, "")
	if deps.TryAutoMountOnMessage("上工 zzz不存在") {
		t.Fatal("项目不存在时不应接管投递")
	}
	if cfg := loadTestCfg(t, deps); cfg.ProjectMount != "" {
		t.Fatalf("挂载失败却落盘了 projectMount=%q", cfg.ProjectMount)
	}
}

// setMountFlags 改主干 cat.cfg 的 mountAuto / projectMount（构造判据前置态）。
func setMountFlags(t *testing.T, deps *Deps, auto bool, mount string) {
	t.Helper()
	path := filepath.Join(deps.DataRoot, "sessions", "majordomo", "cat.cfg")
	cfg, err := config.LoadCatCfg(path)
	if err != nil || cfg == nil {
		t.Fatalf("读 cat.cfg 失败: %v", err)
	}
	cfg.MountAuto = auto
	cfg.ProjectMount = mount
	if err := config.SaveCatCfg(path, cfg); err != nil {
		t.Fatalf("写 cat.cfg 失败: %v", err)
	}
}

// loadTestCfg 读主干 cat.cfg（判据回读用）。
func loadTestCfg(t *testing.T, deps *Deps) *config.CatCfg {
	t.Helper()
	cfg, err := config.LoadCatCfg(filepath.Join(deps.DataRoot, "sessions", "majordomo", "cat.cfg"))
	if err != nil || cfg == nil {
		t.Fatalf("读 cat.cfg 失败: %v", err)
	}
	return cfg
}

// TestTryAutoMountOnMessageFirstTurn 前文 ≤1 条——挂载落盘 + Dispatch 带消息重建（接管投递）。
func TestTryAutoMountOnMessageFirstTurn(t *testing.T) {
	deps, root := newTestDeps(t)
	seedFullProject(t, root, "Demo")
	deps.Session = &session.Session{}
	setMountFlags(t, deps, true, "")
	dispatched := []string{}
	deps.Dispatch = func(command string) bool {
		dispatched = append(dispatched, command)
		return true
	}
	if !deps.TryAutoMountOnMessage("上工 Demo") {
		t.Fatal("前文 ≤1 条时应接管投递")
	}
	cfg := loadTestCfg(t, deps)
	if cfg.ProjectMount != "Demo" {
		t.Fatalf("未落盘挂载：projectMount=%q", cfg.ProjectMount)
	}
	for _, want := range []string{"ccbp:L2/Job/Demo/exp.md", "ccbp:L3/Demo/readme.md", "ccbp:Project/Demo/project.md"} {
		found := false
		for _, item := range cfg.InjectList {
			if item == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("清单缺 %s：%v", want, cfg.InjectList)
		}
	}
	if len(dispatched) != 1 || dispatched[0] != "session.new 上工 Demo" {
		t.Fatalf("重建投递不符：%v", dispatched)
	}
	if cfg.ProjectMountAt == 0 {
		t.Fatal("前文 ≤1 条应记录自动取消计时起点")
	}
}
