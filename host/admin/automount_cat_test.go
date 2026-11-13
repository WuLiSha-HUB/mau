package admin

import (
	"path/filepath"
	"strings"
	"testing"

	"cathome5/host/config"
	"cathome5/host/session"
)

// seedCatSession 造一只多猫会话 + cat.cfg，并把 SessionFor / CatLookup 双钩子接到它。
func seedCatSession(t *testing.T, deps *Deps, id, name string, auto bool, mount string) *session.Session {
	t.Helper()
	cfg := &config.CatCfg{
		ID:           id,
		DisplayName:  name,
		APIConfigID:  config.ZeroGUIDText,
		ToolNames:    "text-read",
		InjectList:   []string{"ccbp:AGENTS.md"},
		MountAuto:    auto,
		ProjectMount: mount,
	}
	if err := config.SaveCatCfg(filepath.Join(deps.DataRoot, "sessions", id, "cat.cfg"), cfg); err != nil {
		t.Fatalf("写 cat.cfg 失败: %v", err)
	}
	sess := session.NewSession(id, session.NewContext(), nil, nil)
	sess.SetDisplayName(name)
	deps.SessionFor = func(catKey string) *session.Session {
		if catKey == id || catKey == name {
			return sess
		}
		if catKey == "majordomo" {
			return deps.Session
		}
		return nil
	}
	deps.CatLookup = func(catKey string) (string, string, bool, int, *session.Session, bool) {
		if catKey == id || catKey == name {
			return id, name, true, 8080, sess, true
		}
		return "", "", false, 0, nil, false
	}
	return sess
}

// loadCatCfgOf 读指定猫 cat.cfg（判据回读用）。
func loadCatCfgOf(t *testing.T, deps *Deps, id string) *config.CatCfg {
	t.Helper()
	cfg, err := config.LoadCatCfg(filepath.Join(deps.DataRoot, "sessions", id, "cat.cfg"))
	if err != nil || cfg == nil {
		t.Fatalf("读 cat.cfg 失败（cat=%s）: %v", id, err)
	}
	return cfg
}

// captureLogs 接管管理面日志出口——返回累积指针（判据 = 日志文本逐字）。
func captureLogs(deps *Deps) *[]string {
	logs := &[]string{}
	deps.Logf = func(category string, level int, message string) {
		*logs = append(*logs, category+"|"+message)
	}
	return logs
}

// hasLog 日志清单包含指定片段（逐条线性扫）。
func hasLog(logs *[]string, fragment string) bool {
	for _, line := range *logs {
		if strings.Contains(line, fragment) {
			return true
		}
	}
	return false
}

// TestTryAutoMountForCatFresh 每猫端口 Chat 自动挂载——前文 ≤1 条：挂载落盘 + fresh=true（调用方带消息重建）。
func TestTryAutoMountForCatFresh(t *testing.T) {
	deps, root := newTestDeps(t)
	seedFullProject(t, root, "Demo")
	sess := seedCatSession(t, deps, "cat001", "测试猫", true, "")
	logs := captureLogs(deps)

	mounted, fresh := deps.TryAutoMountForCat("cat001", sess, "上工 Demo")
	if !mounted || !fresh {
		t.Fatalf("前文 ≤1 条应挂载且 fresh：mounted=%v fresh=%v", mounted, fresh)
	}
	cfg := loadCatCfgOf(t, deps, "cat001")
	if cfg.ProjectMount != "Demo" {
		t.Fatalf("未落盘挂载：projectMount=%q", cfg.ProjectMount)
	}
	for _, want := range []string{"ccbp:L2/Job/Demo/exp.md", "ccbp:L3/Demo/readme.md", "ccbp:Project/Demo/project.md"} {
		if !contains(cfg.InjectList, want) {
			t.Fatalf("清单缺 %s：%v", want, cfg.InjectList)
		}
	}
	if cfg.ProjectMountAt == 0 {
		t.Fatal("前文 ≤1 条应记录自动取消计时起点")
	}
	if !hasLog(logs, "消息自动挂载（首条）：cat=cat001 project=Demo") {
		t.Fatalf("首条挂载日志缺失：%v", *logs)
	}
}

// TestTryAutoMountForCatExistingTalk 每猫端口 Chat 自动挂载——前文 >1 条：只落盘不重建（fresh=false）。
func TestTryAutoMountForCatExistingTalk(t *testing.T) {
	deps, root := newTestDeps(t)
	seedFullProject(t, root, "Demo")
	sess := seedCatSession(t, deps, "cat002", "二猫", true, "")
	sess.Context.AddUserMessage("你好")
	sess.Context.AddAssistantMessage("在")
	logs := captureLogs(deps)

	mounted, fresh := deps.TryAutoMountForCat("cat002", sess, "上工 Demo")
	if !mounted || fresh {
		t.Fatalf("前文 >1 条应 mounted=true fresh=false：mounted=%v fresh=%v", mounted, fresh)
	}
	cfg := loadCatCfgOf(t, deps, "cat002")
	if cfg.ProjectMount != "Demo" {
		t.Fatalf("未落盘挂载：projectMount=%q", cfg.ProjectMount)
	}
	if cfg.ProjectMountAt != 0 {
		t.Fatal("前文 >1 条不应记录自动取消计时起点")
	}
	if !hasLog(logs, "消息自动挂载（不重建）：cat=cat002 project=Demo") {
		t.Fatalf("不重建日志缺失：%v", *logs)
	}
}

// TestTryAutoMountForCatGuards 四判据前置拒绝——开关关闭 / 已挂载 / 项目不存在 / 未知猫 / 会话不一致。
func TestTryAutoMountForCatGuards(t *testing.T) {
	deps, root := newTestDeps(t)
	seedFullProject(t, root, "Demo")

	// 开关关闭 → 不挂
	off := seedCatSession(t, deps, "cat003", "三猫", false, "")
	if mounted, _ := deps.TryAutoMountForCat("cat003", off, "上工 Demo"); mounted {
		t.Fatal("mountAuto=false 时不应挂载")
	}
	// 已挂载项目 → 不重挂
	on := seedCatSession(t, deps, "cat004", "四猫", true, "Other")
	if mounted, _ := deps.TryAutoMountForCat("cat004", on, "上工 Demo"); mounted {
		t.Fatal("已挂载项目时不应重挂")
	}
	// 项目不存在 → 落盘失败
	freshCat := seedCatSession(t, deps, "cat005", "五猫", true, "")
	if mounted, _ := deps.TryAutoMountForCat("cat005", freshCat, "上工 zzz不存在"); mounted {
		t.Fatal("项目不存在时不应挂载")
	}
	if cfg := loadCatCfgOf(t, deps, "cat005"); cfg.ProjectMount != "" {
		t.Fatalf("挂载失败却落盘了 projectMount=%q", cfg.ProjectMount)
	}
	// 未知猫 → 拒绝
	if mounted, _ := deps.TryAutoMountForCat("nobody", freshCat, "上工 Demo"); mounted {
		t.Fatal("未知猫不应挂载")
	}
	// 会话不一致（目标会话 ≠ 注册表会话）→ 拒绝（C# cat.Session != target.Session 同判）
	other := session.NewSession("cat005", session.NewContext(), nil, nil)
	if mounted, _ := deps.TryAutoMountForCat("cat005", other, "上工 Demo"); mounted {
		t.Fatal("会话不一致时不应挂载")
	}
	// 无触发词 → 不挂
	if mounted, _ := deps.TryAutoMountForCat("cat005", freshCat, "帮我看下 Demo 的进度"); mounted {
		t.Fatal("无挂载意图时不应挂载")
	}
}

// TestAutoMountForCatCatChat cat.chat 通道自动挂载——只落盘不重建（AutoMountForCat / Cats.cs:383 语义）。
func TestAutoMountForCatCatChat(t *testing.T) {
	deps, root := newTestDeps(t)
	seedFullProject(t, root, "Demo")
	sess := seedCatSession(t, deps, "cat006", "六猫", true, "")
	// 前文 >1 条也不影响：cat.chat 通道从不重建
	sess.Context.AddUserMessage("你好")
	sess.Context.AddAssistantMessage("在")
	logs := captureLogs(deps)

	deps.AutoMountForCat("cat006", "上工 Demo")
	cfg := loadCatCfgOf(t, deps, "cat006")
	if cfg.ProjectMount != "Demo" {
		t.Fatalf("未落盘挂载：projectMount=%q", cfg.ProjectMount)
	}
	if !hasLog(logs, "投递自动挂载（不重建）：cat=cat006 project=Demo") {
		t.Fatalf("投递自动挂载日志缺失：%v", *logs)
	}
	// 二次投递——已挂载即静默跳过（不重复挂 / 不重复出声）
	before := len(*logs)
	deps.AutoMountForCat("cat006", "上工 Demo")
	if len(*logs) != before {
		t.Fatalf("已挂载时不应再出声：%v", (*logs)[before:])
	}
	// 未识别意图——出声「挂载意图未识别（cat.chat）」
	seedCatSession(t, deps, "cat007", "七猫", true, "")
	deps.AutoMountForCat("cat007", "上工 zzz不存在")
	if !hasLog(logs, "挂载意图未识别（cat.chat）：token=zzz不存在") {
		t.Fatalf("未识别出声缺失：%v", *logs)
	}
}

// TestApplyInjectProjectMultiCat 挂载执行体多猫分支——cat 目标按寻址落盘，未知猫逐字拒绝。
func TestApplyInjectProjectMultiCat(t *testing.T) {
	deps, root := newTestDeps(t)
	seedFullProject(t, root, "Demo")
	sess := seedCatSession(t, deps, "cat008", "八猫", true, "")

	resp, next := deps.applyInjectProject("cat008", "Demo", false, false, false)
	if next == nil {
		t.Fatalf("多猫挂载失败：%+v", resp)
	}
	if cfg := loadCatCfgOf(t, deps, "cat008"); cfg.ProjectMount != "Demo" {
		t.Fatalf("未落盘：projectMount=%q", cfg.ProjectMount)
	}
	if tag := sess.SelfTag(); tag.Project != "Demo" {
		t.Fatalf("自报身份未同步：%+v", tag)
	}
	// 未知猫——C# 逐字
	resp, next = deps.applyInjectProject("nobody", "Demo", false, false, false)
	if next != nil {
		t.Fatal("未知猫不应挂载")
	}
	if fail, ok := resp.(injectFailResponse); !ok || fail.Reason != "未找到猫：nobody" {
		t.Fatalf("未找到猫文案不符：%+v", resp)
	}
}
