package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cathome5/host/config"
	"cathome5/host/session"
)

// newTestCatRegistry 构造带桩装配的多猫注册表（每猫独立会话文件；数据根用临时目录）。
func newTestCatRegistry(t *testing.T) (*catRegistry, string) {
	t.Helper()
	dir := t.TempDir()
	reg := newCatRegistry(dir)
	reg.assemble = func(catID string) *sessionWiring {
		ctx := session.NewContext()
		store := session.NewSessionStore(filepath.Join(dir, "sessions", catID, catID+".jsonl"))
		views := session.NewViewStore(filepath.Join(dir, "sessions", catID, catID+".view.jsonl"))
		sess := session.NewSession(catID, ctx, store, views)
		return &sessionWiring{Session: sess, CatID: catID, DisplayName: catID}
	}
	return reg, dir
}

// TestNewSessionID 猫 key 生成——纯数字（.NET ticks 形态，17 位以上）且递增。
func TestNewSessionID(t *testing.T) {
	first := newSessionID()
	second := newSessionID()
	for _, value := range []string{first, second} {
		if len(value) < 17 {
			t.Fatalf("id 长度不足 17 位：%q", value)
		}
		for _, ch := range value {
			if ch < '0' || ch > '9' {
				t.Fatalf("id 应为纯数字：%q", value)
			}
		}
	}
	if first == second {
		t.Fatalf("两次生成应不同：%q", first)
	}
}

// TestCatRegistryFind 猫寻址——id 精确 / 显示名精确 / id 前缀唯一 / 前缀歧义 / 不含 majordomo。
func TestCatRegistryFind(t *testing.T) {
	reg, _ := newTestCatRegistry(t)
	reg.setMajor(&sessionWiring{CatID: "majordomo", DisplayName: "majordomo"}, "majordomo", 8079)
	reg.cats = append(reg.cats,
		&catInstance{id: "1750000000000000001", displayName: "小白"},
		&catInstance{id: "1750000000000000002", displayName: "小黑"},
	)
	if inst := reg.findCat("1750000000000000001"); inst == nil || inst.displayName != "小白" {
		t.Fatalf("id 精确寻址失败：%+v", inst)
	}
	if inst := reg.findCat("小黑"); inst == nil || inst.id != "1750000000000000002" {
		t.Fatalf("显示名寻址失败：%+v", inst)
	}
	if inst := reg.findCat("175000000000000000"); inst != nil {
		t.Fatalf("前缀歧义应返回 nil：%+v", inst)
	}
	if inst := reg.findCat("1750000000000000001"); inst == nil {
		t.Fatal("唯一前缀应命中")
	}
	if inst := reg.findCat("majordomo"); inst != nil {
		t.Fatal("findCat 不应包含 majordomo（C# _cats 同口径）")
	}
	if inst := reg.findAny("majordomo"); inst == nil || inst.port != 8079 {
		t.Fatalf("findAny 应命中 majordomo：%+v", inst)
	}
	if inst := reg.findDeliveryCat("小白"); inst == nil {
		t.Fatal("findDeliveryCat 多猫应命中")
	}
	if inst := reg.findDeliveryCat("majordomo"); inst == nil {
		t.Fatal("findDeliveryCat 默认会话薄壳应命中")
	}
	if inst := reg.findDeliveryCat("nobody"); inst != nil {
		t.Fatal("findDeliveryCat 未命中应 nil")
	}
}

// TestNextDefaultCatName 默认猫名——「默认小猫」→「默认小猫(1)」…（大小写不敏感去重）。
func TestNextDefaultCatName(t *testing.T) {
	reg, _ := newTestCatRegistry(t)
	if name := reg.nextDefaultName(); name != "默认小猫" {
		t.Fatalf("空表默认名不符：%q", name)
	}
	reg.cats = append(reg.cats, &catInstance{id: "1", displayName: "默认小猫"})
	if name := reg.nextDefaultName(); name != "默认小猫(1)" {
		t.Fatalf("占用后默认名不符：%q", name)
	}
	reg.cats = append(reg.cats, &catInstance{id: "2", displayName: "默认小猫(1)"})
	if name := reg.nextDefaultName(); name != "默认小猫(2)" {
		t.Fatalf("连续占用默认名不符：%q", name)
	}
}

// TestCatRuntimeReadWrite 运行态文件——写入 / 读取 / 移除 / 损坏按空表。
func TestCatRuntimeReadWrite(t *testing.T) {
	reg, dir := newTestCatRegistry(t)
	reg.setRuntime("cat-a", true, 0)
	reg.setRuntime("cat-b", false, 0)
	loaded := loadCatRuntime(dir, nil)
	if entry, ok := loaded["cat-a"]; !ok || !entry.Running {
		t.Fatalf("cat-a 运行态丢失：%+v", loaded)
	}
	if entry, ok := loaded["cat-b"]; !ok || entry.Running {
		t.Fatalf("cat-b 运行态不符：%+v", loaded)
	}
	reg.removeRuntime("cat-a")
	loaded = loadCatRuntime(dir, nil)
	if _, ok := loaded["cat-a"]; ok {
		t.Fatalf("cat-a 应已移除：%+v", loaded)
	}
	// 损坏文件——按空表处理（不阻断启动）
	if err := os.WriteFile(catRuntimePath(dir), []byte("{bad json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadCatRuntime(dir, nil); len(got) != 0 {
		t.Fatalf("损坏文件应按空表：%+v", got)
	}
}

// TestCatCreateWritesCfg cat.new 存储面——模板继承 / 默认命名 / cat.cfg 落盘 / 会话装配 / 注册表可见。
func TestCatCreateWritesCfg(t *testing.T) {
	reg, dir := newTestCatRegistry(t)
	// 默认模板——验证新猫继承
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	tpl := &config.CatDefault{
		BaseRole:          "基座",
		DefaultPersona:    "模板角色",
		DefaultToolNames:  "text-read,file-tree",
		DefaultInjectList: []string{"ccbp:L0/SOUL.md"},
		DefaultPacks:      []string{"overwork"},
	}
	if err := config.SaveCatDefault(filepath.Join(dir, "config", "cat-default.cfg"), tpl); err != nil {
		t.Fatal(err)
	}
	inst, errText := reg.createCat("")
	if errText != "" || inst == nil {
		t.Fatalf("建猫失败：%q", errText)
	}
	if inst.displayName != "默认小猫" {
		t.Fatalf("默认命名不符：%q", inst.displayName)
	}
	if len(inst.id) < 17 {
		t.Fatalf("id 形态不符：%q", inst.id)
	}
	if inst.session == nil {
		t.Fatal("cat.new 应装配会话（C# CreateCatEntry 同口径）")
	}
	cfgPath := filepath.Join(dir, "sessions", inst.id, "cat.cfg")
	cfg, err := config.LoadCatCfg(cfgPath)
	if err != nil || cfg == nil {
		t.Fatalf("cat.cfg 未落盘：%v", err)
	}
	if cfg.DisplayName != "默认小猫" || cfg.Persona != "模板角色" || cfg.ToolNames != "text-read,file-tree" {
		t.Fatalf("模板继承不符：%+v", cfg)
	}
	if len(cfg.InjectList) != 1 || cfg.InjectList[0] != "ccbp:L0/SOUL.md" {
		t.Fatalf("injectList 继承不符：%+v", cfg.InjectList)
	}
	if len(cfg.Packs) != 1 || cfg.Packs[0] != "overwork" {
		t.Fatalf("packs 继承不符：%+v", cfg.Packs)
	}
	// 第二只猫——默认命名顺延
	second, errText := reg.createCat("")
	if errText != "" || second.displayName != "默认小猫(1)" {
		t.Fatalf("第二只猫命名不符：%q / %q", second.displayName, errText)
	}
	if got := len(reg.entries()); got != 2 {
		t.Fatalf("注册表应 2 条（无 majordomo）：%d", got)
	}
}

// TestCatLoadOnBoot 启动扫描——sessions/*/cat.cfg 全部加载；running 猫补建会话，静默猫仅可见。
func TestCatLoadOnBoot(t *testing.T) {
	reg, dir := newTestCatRegistry(t)
	mkCat := func(id, name string) {
		cfg := &config.CatCfg{ID: id, DisplayName: name}
		if err := config.SaveCatCfg(filepath.Join(dir, "sessions", id, "cat.cfg"), cfg); err != nil {
			t.Fatal(err)
		}
	}
	mkCat("100000000000000001", "静默猫")
	mkCat("100000000000000002", "运行猫")
	// 运行态文件——id2 running
	saveCatRuntime(dir, map[string]catRuntimeEntry{
		"100000000000000002": {Running: true, Port: 0},
	}, nil)
	reg.loadOnBoot()
	if got := len(reg.entries()); got != 2 {
		t.Fatalf("启动扫描应加载 2 只猫：%d", got)
	}
	if inst := reg.findCat("静默猫"); inst == nil || inst.session != nil {
		t.Fatalf("静默猫不应建会话：%+v", inst)
	}
	if inst := reg.findCat("运行猫"); inst == nil || inst.session == nil || !inst.running {
		t.Fatalf("运行猫应补建会话：%+v", inst)
	}
}

// TestCatStartStopDelete 生命周期——start 置运行态 / 重复 start 文案 / stop 释放 / delete 删目录。
func TestCatStartStopDelete(t *testing.T) {
	reg, dir := newTestCatRegistry(t)
	inst, errText := reg.createCat("小测")
	if errText != "" {
		t.Fatalf("建猫失败：%q", errText)
	}
	// 新建为静默态——stop 直接回「已是静默态」
	if got := reg.stopCat(inst.id); !strings.Contains(got, "已是静默态") {
		t.Fatalf("静默态 stop 文案不符：%q", got)
	}
	if got := reg.startCat(inst.id); !strings.Contains(got, "已启动") {
		t.Fatalf("start 回执不符：%q", got)
	}
	if got := reg.startCat(inst.id); !strings.Contains(got, "已在运行") {
		t.Fatalf("重复 start 文案不符：%q", got)
	}
	if entry := loadCatRuntime(dir, nil)[inst.id]; !entry.Running {
		t.Fatalf("运行态未落盘：%+v", entry)
	}
	if got := reg.stopCat(inst.id); !strings.Contains(got, "已停止") {
		t.Fatalf("stop 回执不符：%q", got)
	}
	if entry := loadCatRuntime(dir, nil)[inst.id]; entry.Running {
		t.Fatalf("停止后运行态应为 false：%+v", entry)
	}
	// 再启动——会话应从磁盘重建
	if got := reg.startCat(inst.id); !strings.Contains(got, "已启动") {
		t.Fatalf("二次 start 回执不符：%q", got)
	}
	if inst.session == nil {
		t.Fatal("二次 start 应重建会话")
	}
	if got := reg.deleteCat(inst.id); !strings.Contains(got, "已销毁") {
		t.Fatalf("delete 回执不符：%q", got)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "sessions", inst.id)); !os.IsNotExist(statErr) {
		t.Fatalf("delete 后目录应消失：%v", statErr)
	}
	if got := len(reg.entries()); got != 0 {
		t.Fatalf("delete 后注册表应为空：%d", got)
	}
	if _, ok := loadCatRuntime(dir, nil)[inst.id]; ok {
		t.Fatal("delete 后运行态条目应摘除")
	}
}

// TestCatListWithRegistry cat.list 多猫文本——majordomo 前置 + 每猫一行 + 状态文本。
func TestCatListWithRegistry(t *testing.T) {
	reg, _ := newTestCatRegistry(t)
	reg.setMajor(&sessionWiring{CatID: "majordomo", DisplayName: "majordomo"}, "majordomo", 8079)
	reg.cats = append(reg.cats,
		&catInstance{id: "100", displayName: "小白", running: true},
		&catInstance{id: "200", displayName: "小黑"},
	)
	router := &commandRouter{DataRoot: t.TempDir(), CatID: "majordomo", DisplayName: "majordomo", Session: nil, Cats: reg}
	text := router.buildCatListText()
	if !strings.HasPrefix(text, "cat.list | 3 只猫") {
		t.Fatalf("头行不符：%q", text)
	}
	if !strings.Contains(text, "  majordomo | majordomo | 运行中 :8079 | 主干会话（禁停禁删）") {
		t.Fatalf("majordomo 行不符：%q", text)
	}
	if !strings.Contains(text, "  100 | 小白 | 运行中") {
		t.Fatalf("运行猫行不符：%q", text)
	}
	if !strings.Contains(text, "  200 | 小黑 | 静默") {
		t.Fatalf("静默猫行不符：%q", text)
	}
}
