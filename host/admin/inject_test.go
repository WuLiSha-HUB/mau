package admin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cathome5/host/config"
	"cathome5/host/session"
)

// writeTestFile 写一个测试文件（自动建父目录）。
func writeTestFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("建目录失败 %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatalf("写文件失败 %s: %v", path, err)
	}
}

// seedFullProject 造一个件齐的项目——档案四件 + Job 三件 + L3 段（readme + 一份专家段）。
func seedFullProject(t *testing.T, root, name string) {
	t.Helper()
	for _, doc := range []string{"design-status.md", "glossary.md", "project.md", "todo.md"} {
		writeTestFile(t, filepath.Join(root, "Project", name, doc), "# "+name+" "+doc)
	}
	for _, job := range []string{"exp.md", "skill.md", "tools.md"} {
		writeTestFile(t, filepath.Join(root, "L2", "Job", name, job), "# "+name+" "+job)
	}
	writeTestFile(t, filepath.Join(root, "L3", name, "readme.md"), "# "+name+" 现场手册")
	writeTestFile(t, filepath.Join(root, "L3", name, "A-域.md"), strings.Repeat("x", 2048))
}

// TestInjectProjectsScan 候选项目清单——CCBP 项目 + workspace 独立立项，按最后改动倒序。
func TestInjectProjectsScan(t *testing.T) {
	deps, root := newTestDeps(t)
	seedFullProject(t, root, "Demo")
	seedFullProject(t, root, "Other")
	// workspace 独立立项（根下带 project.md 的目录）+ 工具区工程（无 project.md 不参与）
	writeTestFile(t, filepath.Join(root, "BOSS直聘", "project.md"), "# BOSS直聘")
	writeTestFile(t, filepath.Join(root, "BOSS直聘", "todo.md"), "# todo")
	writeTestFile(t, filepath.Join(root, "tools", "SomeTool", "project.md"), "# 工具区")

	rec := do(t, deps, "GET", "/api/v1/inject/projects", "")
	if rec.Code != 200 {
		t.Fatalf("状态码 %d: %s", rec.Code, rec.Body.String())
	}
	order := objectKeyOrder(t, rec.Body.Bytes())
	if strings.Join(order, ",") != "ok,root,projects" {
		t.Fatalf("键序不符: %v", order)
	}
	var payload struct {
		OK       bool                `json:"ok"`
		Root     string              `json:"root"`
		Projects []injectProjectItem `json:"projects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !payload.OK || payload.Root == "" {
		t.Fatalf("回执异常: %+v", payload)
	}
	names := make([]string, 0, len(payload.Projects))
	for _, item := range payload.Projects {
		names = append(names, item.Name)
	}
	joined := strings.Join(names, ",")
	for _, want := range []string{"Demo", "Other", "BOSS直聘"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("清单缺 %s: %s", want, joined)
		}
	}
	if strings.Contains(joined, "SomeTool") {
		t.Fatalf("工具区工程不应进入候选清单: %s", joined)
	}
	// config / sessions / tools 这些非项目目录不得出现（无 project.md）
	if strings.Contains(joined, "config") || strings.Contains(joined, "sessions") {
		t.Fatalf("非项目目录混入: %s", joined)
	}
	// 文件数自证——Project/Demo 目录下 4 件档案（Job 三件在 L2/Job、L3 段在 L3，不在项目目录内）
	for _, item := range payload.Projects {
		if item.Name == "Demo" && item.Files != 4 {
			t.Fatalf("Demo 文件数 %d ≠ 4", item.Files)
		}
		if item.Name == "Demo" && len(item.LastWrite) != 19 {
			t.Fatalf("lastWrite 形态不符: %s", item.LastWrite)
		}
	}
	// 倒序：最新改动在前（BOSS直聘 最后写入，应排在前两名内）
	if len(names) >= 2 && names[0] != "BOSS直聘" && names[1] != "BOSS直聘" {
		t.Fatalf("倒序不符: %v", names)
	}
}

// TestBuildInjectListWithMount 清单重算——剥旧挂载条目 + 保序 + 追加 Job 三件 / L3 段 / 档案四件。
func TestBuildInjectListWithMount(t *testing.T) {
	deps, root := newTestDeps(t)
	seedFullProject(t, root, "Demo")
	seedFullProject(t, root, "Old")
	current := []string{
		"ccbp:AGENTS.md",
		"ccbp:Project/Old/project.md",
		"ccbp:L2/Job/Old/exp.md",
		"ccbp:L3/Old",
		"ccbp:L3/Old/readme.md",
		"ccbp:L2/SOUL.md",
	}
	// 专家组：旧挂载条目全剥，新项目四段按序追加
	next := deps.buildInjectListWithMount(current, "Demo", "Old", true, nil)
	want := []string{
		"ccbp:AGENTS.md",
		"ccbp:L2/SOUL.md",
		"ccbp:L2/Job/Demo/exp.md",
		"ccbp:L2/Job/Demo/skill.md",
		"ccbp:L2/Job/Demo/tools.md",
		"ccbp:L3/Demo",
		"ccbp:Project/Demo/design-status.md",
		"ccbp:Project/Demo/glossary.md",
		"ccbp:Project/Demo/project.md",
		"ccbp:Project/Demo/todo.md",
	}
	if strings.Join(next, " | ") != strings.Join(want, " | ") {
		t.Fatalf("专家组清单不符:\n实际 %v\n期望 %v", next, want)
	}
	// 关模式：L3 只挂 readme.md 单件
	next = deps.buildInjectListWithMount(current, "Demo", "Old", false, nil)
	if !contains(next, "ccbp:L3/Demo/readme.md") || contains(next, "ccbp:L3/Demo") {
		t.Fatalf("关模式 L3 段不符: %v", next)
	}
	// 取消挂载（project 空）：只剥旧条目，不追加
	next = deps.buildInjectListWithMount(current, "", "Old", true, nil)
	if strings.Join(next, " | ") != "ccbp:AGENTS.md | ccbp:L2/SOUL.md" {
		t.Fatalf("取消挂载清单不符: %v", next)
	}
	// 模板勾选关掉的挂载段——off 命中即不追加（摘除照旧）
	next = deps.buildInjectListWithMount(current, "Demo", "Old", true, map[string]bool{
		"job": true, "l3": true, "glossary": true,
	})
	wantOff := []string{
		"ccbp:AGENTS.md",
		"ccbp:L2/SOUL.md",
		"ccbp:Project/Demo/design-status.md",
		"ccbp:Project/Demo/project.md",
		"ccbp:Project/Demo/todo.md",
	}
	if strings.Join(next, " | ") != strings.Join(wantOff, " | ") {
		t.Fatalf("关段清单不符:\n实际 %v\n期望 %v", next, wantOff)
	}
}

// TestWorkspaceProjectDocShape workspace 独立立项的档案条目形态判定。
func TestWorkspaceProjectDocShape(t *testing.T) {
	cases := []struct {
		spec    string
		project string
		want    bool
	}{
		{"workspace:BOSS直聘/todo.md", "BOSS直聘", true},
		{"workspace:BOSS直聘/project.md", "BOSS直聘", true},
		{"workspace:tools/SomeTool/project.md", "SomeTool", false},
		{"workspace:BOSS直聘/other.md", "BOSS直聘", false},
		{"workspace:Other/todo.md", "BOSS直聘", false},
		{"ccbp:Project/BOSS直聘/todo.md", "BOSS直聘", false},
	}
	for _, item := range cases {
		if got := isWorkspaceProjectDoc(item.spec, item.project); got != item.want {
			t.Fatalf("isWorkspaceProjectDoc(%q, %q) = %v，期望 %v", item.spec, item.project, got, item.want)
		}
	}
}

// TestMountExpertText 专家段文案——有料给千 token 估值，缺位出声。
func TestMountExpertText(t *testing.T) {
	if got := mountExpertText(0); got != " · 专家 +0k(缺位)" {
		t.Fatalf("缺位文案不符: %q", got)
	}
	// 6288 字节 = 6.14 KB / 3.07 ≈ 2.0k
	if got := mountExpertText(6288); got != " · 专家 +2.0k" {
		t.Fatalf("有料文案不符: %q", got)
	}
}

// TestBuildJobList Job 清单提取——前缀匹配 + 首段目录 + 去重保序。
func TestBuildJobList(t *testing.T) {
	list := []string{"ccbp:AGENTS.md", "ccbp:L2/Job/Demo/exp.md", "ccbp:L2/Job/Demo/skill.md", "ccbp:L2/Job/Mau/tools.md"}
	if got := BuildJobList(list); got != "Demo,Mau" {
		t.Fatalf("Job 清单不符: %q", got)
	}
	if got := BuildJobList(nil); got != "" {
		t.Fatalf("空清单应为空串: %q", got)
	}
}

// TestApplyInjectProjectSyncsSelfTag 挂载 / 取消挂载刷新自报身份——项目 / 模式 / Job 清单三组。
func TestApplyInjectProjectSyncsSelfTag(t *testing.T) {
	deps, root := newTestDeps(t)
	seedFullProject(t, root, "Demo")
	deps.Session = session.NewSession("", session.NewContext(), nil, nil)
	deps.Session.CatKey = "majordomo"
	deps.Session.SetDisplayName("主干")

	deps.applyInjectProject("majordomo", "Demo", true, true, false)
	tag := deps.Session.SelfTag()
	if tag.Project != "Demo" || tag.MountMode != "expert" || tag.Jobs != "Demo" {
		t.Fatalf("挂载后自报身份不符: %+v", tag)
	}
	if tag.CatID != "majordomo" || tag.CatName != "主干" {
		t.Fatalf("猫身份字段不符: %+v", tag)
	}
	// 切 readme 模式——模式头随挂载面刷新
	deps.applyInjectProject("majordomo", "Demo", false, true, false)
	if got := deps.Session.SelfTag(); got.MountMode != "readme" || got.Project != "Demo" {
		t.Fatalf("readme 模式未刷新: %+v", got)
	}
	// 取消挂载——三项整组清空（适配层按空跳过）
	deps.applyInjectProject("majordomo", "", false, false, false)
	if got := deps.Session.SelfTag(); got.Project != "" || got.MountMode != "" || got.Jobs != "" {
		t.Fatalf("取消挂载后应清空三项: %+v", got)
	}
}

// TestInjectAutoEndpoints 自动识别开关——读 / 写 / 错误面三态。
func TestInjectAutoEndpoints(t *testing.T) {
	deps, root := newTestDeps(t)
	// 读面：默认（cat 空 = 主干）
	rec := do(t, deps, "GET", "/api/v1/inject/auto", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":true`) || !strings.Contains(rec.Body.String(), `"cat":"majordomo"`) {
		t.Fatalf("默认读面异常: %d %s", rec.Code, rec.Body.String())
	}
	order := objectKeyOrder(t, rec.Body.Bytes())
	if strings.Join(order, ",") != "ok,cat,on" {
		t.Fatalf("读面键序不符: %v", order)
	}
	// 读面：cat 非法（含分隔符）
	rec = do(t, deps, "GET", "/api/v1/inject/auto?cat=a/b", "")
	if !strings.Contains(rec.Body.String(), `"error":"cat 参数非法: a/b"`) {
		t.Fatalf("非法 cat 未出声: %s", rec.Body.String())
	}
	// 读面：cat.cfg 不存在
	rec = do(t, deps, "GET", "/api/v1/inject/auto?cat=nobody", "")
	if !strings.Contains(rec.Body.String(), `"error":"cat.cfg 不存在: nobody"`) {
		t.Fatalf("缺档未出声: %s", rec.Body.String())
	}
	// 写面：开 → 落盘 true
	rec = do(t, deps, "POST", "/api/v1/inject/auto", `{"on":true}`)
	if !strings.Contains(rec.Body.String(), `"on":true`) {
		t.Fatalf("写入失败: %s", rec.Body.String())
	}
	cfg, err := config.LoadCatCfg(filepath.Join(root, "sessions", "majordomo", "cat.cfg"))
	if err != nil || !cfg.MountAuto {
		t.Fatalf("mountAuto 未落盘: %+v %v", cfg, err)
	}
	// 写面：关 → 落盘 false；未提交字段保留（persona 不动）
	rec = do(t, deps, "POST", "/api/v1/inject/auto", `{"on":false}`)
	if !strings.Contains(rec.Body.String(), `"on":false`) {
		t.Fatalf("关闭失败: %s", rec.Body.String())
	}
	cfg, _ = config.LoadCatCfg(filepath.Join(root, "sessions", "majordomo", "cat.cfg"))
	if cfg.MountAuto || cfg.Persona != "旧人设" {
		t.Fatalf("落盘异常（mountAuto / persona）: %+v", cfg)
	}
	// 写面：cat 非法 → reason 键（C# 该面用 reason）
	rec = do(t, deps, "POST", "/api/v1/inject/auto", `{"cat":"a/b","on":true}`)
	if !strings.Contains(rec.Body.String(), `"reason":"cat 参数非法: a/b"`) {
		t.Fatalf("写面非法 cat 未出声: %s", rec.Body.String())
	}
	// 写面：非 majordomo 一律「未找到猫」（多猫面归 P5；C# POST 面只对主干报 cat.cfg 不存在）
	rec = do(t, deps, "POST", "/api/v1/inject/auto", `{"cat":"nobody","on":true}`)
	if !strings.Contains(rec.Body.String(), `"reason":"未找到猫：nobody"`) {
		t.Fatalf("写面未知猫未出声: %s", rec.Body.String())
	}
}

// TestInjectAutoPerCat 多猫写面——按 CatLookup 寻址翻转落盘 / 回执键序 / 未命中文案（C# 逐字）。
//
// 寻址语义（id 精确 / 显示名 / 前缀唯一 / 不含 majordomo）归 cats.findCat（cats_test.go 覆盖）；
// 本测试守 Deps 层契约：命中即写该猫 cat.cfg.mountAuto、回执 cat 用请求键、日志用显示名、未命中具名拒绝。
func TestInjectAutoPerCat(t *testing.T) {
	deps, root := newTestDeps(t)
	seedCatSession(t, deps, "cat001", "测试猫", true, "")
	logs := captureLogs(deps)

	// 按 id 寻址：关 → 落盘 false + 键序 ok,cat,on
	rec := do(t, deps, "POST", "/api/v1/inject/auto", `{"cat":"cat001","on":false}`)
	if rec.Code != 200 {
		t.Fatalf("状态码 %d: %s", rec.Code, rec.Body.String())
	}
	order := objectKeyOrder(t, rec.Body.Bytes())
	if strings.Join(order, ",") != "ok,cat,on" {
		t.Fatalf("写面键序不符: %v", order)
	}
	if !strings.Contains(rec.Body.String(), `"cat":"cat001"`) || !strings.Contains(rec.Body.String(), `"on":false`) {
		t.Fatalf("回执不符: %s", rec.Body.String())
	}
	if cfg := loadCatCfgOf(t, deps, "cat001"); cfg.MountAuto {
		t.Fatalf("mountAuto 未落盘翻转: %+v", cfg)
	}
	if !hasLog(logs, "自动识别开关：cat=测试猫 on=false") {
		t.Fatalf("日志未用显示名: %v", *logs)
	}
	// GET 回读同值（按 id——读面直拼 cat.cfg 路径，C# 同口径）
	rec = do(t, deps, "GET", "/api/v1/inject/auto?cat=cat001", "")
	if !strings.Contains(rec.Body.String(), `"on":false`) {
		t.Fatalf("读面回读不一致: %s", rec.Body.String())
	}
	// 按显示名寻址：开 → 落盘 true；未提交字段保留
	rec = do(t, deps, "POST", "/api/v1/inject/auto", `{"cat":"测试猫","on":true}`)
	if !strings.Contains(rec.Body.String(), `"on":true`) {
		t.Fatalf("按显示名写入失败: %s", rec.Body.String())
	}
	if cfg := loadCatCfgOf(t, deps, "cat001"); !cfg.MountAuto || cfg.ToolNames != "text-read" {
		t.Fatalf("落盘异常（mountAuto / toolNames）: %+v", cfg)
	}
	// 未命中猫 → C# 逐字（reason 键 + 全角冒号）
	rec = do(t, deps, "POST", "/api/v1/inject/auto", `{"cat":"nobody","on":true}`)
	if !strings.Contains(rec.Body.String(), `"reason":"未找到猫：nobody"`) {
		t.Fatalf("未命中猫文案不符: %s", rec.Body.String())
	}
	// 主干分支不受影响（直拼路径 + 日志用 majordomo）
	rec = do(t, deps, "POST", "/api/v1/inject/auto", `{"cat":"majordomo","on":false}`)
	if !strings.Contains(rec.Body.String(), `"on":false`) {
		t.Fatalf("主干分支异常: %s", rec.Body.String())
	}
	cfg, err := config.LoadCatCfg(filepath.Join(root, "sessions", "majordomo", "cat.cfg"))
	if err != nil || cfg.MountAuto {
		t.Fatalf("主干 mountAuto 未落盘: %+v %v", cfg, err)
	}
}

// TestInjectPreview 挂载预览——只读，不落盘；cat 空按新猫模板算。
func TestInjectPreview(t *testing.T) {
	deps, root := newTestDeps(t)
	seedFullProject(t, root, "Demo")
	before, err := os.ReadFile(filepath.Join(root, "sessions", "majordomo", "cat.cfg"))
	if err != nil {
		t.Fatalf("读 cat.cfg 失败: %v", err)
	}
	rec := do(t, deps, "GET", "/api/v1/inject/preview?cat=majordomo&project=Demo&expert=true", "")
	if rec.Code != 200 {
		t.Fatalf("状态码 %d: %s", rec.Code, rec.Body.String())
	}
	order := objectKeyOrder(t, rec.Body.Bytes())
	if strings.Join(order, ",") != "ok,injectList,expert,mount" {
		t.Fatalf("键序不符: %v", order)
	}
	var payload injectPreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !payload.OK || !payload.Expert || payload.Mount != "Demo" {
		t.Fatalf("回执异常: %+v", payload)
	}
	// injectList 是 JSON 数组文本（C# JsonUtil.Array 产出字符串值——前端按字符串再解析）
	list := decodeListJSON(t, payload.InjectList)
	for _, want := range []string{"ccbp:L2/Job/Demo/exp.md", "ccbp:L3/Demo", "ccbp:Project/Demo/todo.md", "ccbp:AGENTS.md"} {
		if !contains(list, want) {
			t.Fatalf("预览清单缺 %s: %v", want, list)
		}
	}
	after, err := os.ReadFile(filepath.Join(root, "sessions", "majordomo", "cat.cfg"))
	if err != nil {
		t.Fatalf("读 cat.cfg 失败: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("预览改了落盘（必须只读）")
	}
	// expert=false → L3 只挂 readme 单件
	rec = do(t, deps, "GET", "/api/v1/inject/preview?cat=majordomo&project=Demo&expert=false", "")
	var readmePayload injectPreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &readmePayload); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	readmeList := decodeListJSON(t, readmePayload.InjectList)
	if !contains(readmeList, "ccbp:L3/Demo/readme.md") || contains(readmeList, "ccbp:L3/Demo") {
		t.Fatalf("关模式预览不符: %v", readmeList)
	}
	// cat 空 → 模板清单（无 defaultInjectList → 空基底）
	rec = do(t, deps, "GET", "/api/v1/inject/preview?project=Demo", "")
	var tplPayload injectPreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &tplPayload); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !tplPayload.Expert {
		t.Fatalf("无猫时应沿用缺省专家模式 true: %+v", tplPayload)
	}
	if contains(decodeListJSON(t, tplPayload.InjectList), "ccbp:AGENTS.md") {
		t.Fatalf("模板基底不应含该猫条目: %s", tplPayload.InjectList)
	}
	// cat 空 → 模板「启用集」：未打勾（defaultInjectDisabled）的条目不进预览——
	// 预览清单会拼进派活提示词的「前文已挂载」栏，列出未注入的条目 = 告诉猫「已读」而实际没有。
	tplWithDisabled := &config.CatDefault{
		BaseRole:              "基础",
		DefaultToolNames:      "text-read",
		DefaultInjectList:     []string{"ccbp:AGENTS.md", "ccbp:L2/Worker/relic", "ccbp:L1"},
		DefaultInjectDisabled: []string{"ccbp:L2/Worker/relic"},
	}
	if err := config.SaveCatDefault(filepath.Join(root, "config", "cat-default.cfg"), tplWithDisabled); err != nil {
		t.Fatalf("写 cat-default.cfg 失败: %v", err)
	}
	rec = do(t, deps, "GET", "/api/v1/inject/preview?project=Demo", "")
	var enabledPayload injectPreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &enabledPayload); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	enabledList := decodeListJSON(t, enabledPayload.InjectList)
	if !contains(enabledList, "ccbp:AGENTS.md") || !contains(enabledList, "ccbp:L1") {
		t.Fatalf("预览缺模板启用条目: %v", enabledList)
	}
	if contains(enabledList, "ccbp:L2/Worker/relic") {
		t.Fatalf("预览列了未打勾（disabled）的条目: %v", enabledList)
	}
	// @mount:l3@Demo —— 挂载段开关：勾掉后挂 Demo 不追加 L3 段（带项目名只对该项目生效）
	tplWithDisabled.DefaultInjectList = append(tplWithDisabled.DefaultInjectList, "@mount:l3@Demo")
	tplWithDisabled.DefaultInjectDisabled = append(tplWithDisabled.DefaultInjectDisabled, "@mount:l3@Demo")
	if err := config.SaveCatDefault(filepath.Join(root, "config", "cat-default.cfg"), tplWithDisabled); err != nil {
		t.Fatalf("写 cat-default.cfg 失败: %v", err)
	}
	rec = do(t, deps, "GET", "/api/v1/inject/preview?project=Demo&expert=true", "")
	var offPayload injectPreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &offPayload); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	offList := decodeListJSON(t, offPayload.InjectList)
	if contains(offList, "ccbp:L3/Demo") {
		t.Fatalf("@mount:l3@Demo 未生效（L3 段仍追加）: %v", offList)
	}
	if !contains(offList, "ccbp:Project/Demo/todo.md") {
		t.Fatalf("关 L3 不应连带关档案四件: %v", offList)
	}
	if contains(offList, "@mount:l3@Demo") {
		t.Fatalf("挂载段开关位混进了前文清单: %v", offList)
	}
}

// TestInjectProjectPost 挂载写面——成功路径落盘 + 会话重建投递 + 取消。
func TestInjectProjectPost(t *testing.T) {
	deps, root := newTestDeps(t)
	seedFullProject(t, root, "Demo")
	dispatched := []string{}
	deps.Dispatch = func(command string) bool {
		dispatched = append(dispatched, command)
		return true
	}
	rec := do(t, deps, "POST", "/api/v1/inject/project", `{"cat":"majordomo","project":"Demo","expert":false}`)
	if rec.Code != 200 {
		t.Fatalf("状态码 %d: %s", rec.Code, rec.Body.String())
	}
	order := objectKeyOrder(t, rec.Body.Bytes())
	if strings.Join(order, ",") != "ok,msg,injectList" {
		t.Fatalf("键序不符: %v", order)
	}
	var payload injectProjectWriteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !payload.OK || !strings.HasPrefix(payload.Msg, "已挂载 Demo——会话重建中") {
		t.Fatalf("回执异常: %+v", payload)
	}
	if !strings.HasSuffix(payload.Msg, " · L3 仅 readme") {
		t.Fatalf("关模式文案不符: %s", payload.Msg)
	}
	if len(dispatched) != 1 || dispatched[0] != "session.new" {
		t.Fatalf("未投递会话重建: %v", dispatched)
	}
	cfg, err := config.LoadCatCfg(filepath.Join(root, "sessions", "majordomo", "cat.cfg"))
	if err != nil {
		t.Fatalf("读 cat.cfg 失败: %v", err)
	}
	if cfg.ProjectMount != "Demo" || cfg.ProjectMountExpert || cfg.ProjectMountAt <= 0 {
		t.Fatalf("挂载字段落盘异常: %+v", cfg)
	}
	if !contains(cfg.InjectList, "ccbp:L3/Demo/readme.md") || contains(cfg.InjectList, "ccbp:L3/Demo") {
		t.Fatalf("清单 L3 段不符: %v", cfg.InjectList)
	}
	// 取消挂载
	rec = do(t, deps, "POST", "/api/v1/inject/project", `{"cat":"majordomo","project":""}`)
	if !strings.Contains(rec.Body.String(), `"msg":"已取消 Project 挂载——会话重建中"`) {
		t.Fatalf("取消文案不符: %s", rec.Body.String())
	}
	cfg, _ = config.LoadCatCfg(filepath.Join(root, "sessions", "majordomo", "cat.cfg"))
	if cfg.ProjectMount != "" {
		t.Fatalf("取消后 projectMount 未清: %+v", cfg)
	}
	for _, item := range cfg.InjectList {
		if strings.HasPrefix(item, "ccbp:L2/Job/Demo/") || strings.HasPrefix(item, "ccbp:Project/") {
			t.Fatalf("取消后残留挂载条目: %v", cfg.InjectList)
		}
	}
}

// TestInjectProjectPostErrors 挂载写面错误面——未找到猫 / 项目不存在 / body 非 JSON。
func TestInjectProjectPostErrors(t *testing.T) {
	deps, root := newTestDeps(t)
	seedFullProject(t, root, "Demo")
	// cat 空 → 未找到猫
	rec := do(t, deps, "POST", "/api/v1/inject/project", `{"project":"Demo"}`)
	if !strings.Contains(rec.Body.String(), `"reason":"未找到猫："`) {
		t.Fatalf("空 cat 未出声: %s", rec.Body.String())
	}
	// 多猫 key（P5 前一律未找到）
	rec = do(t, deps, "POST", "/api/v1/inject/project", `{"cat":"nobody","project":"Demo"}`)
	if !strings.Contains(rec.Body.String(), `"reason":"未找到猫：nobody"`) {
		t.Fatalf("未知猫未出声: %s", rec.Body.String())
	}
	// 项目不存在
	rec = do(t, deps, "POST", "/api/v1/inject/project", `{"cat":"majordomo","project":"Nope"}`)
	if !strings.Contains(rec.Body.String(), `"reason":"项目不存在：Nope"`) {
		t.Fatalf("缺项目未出声: %s", rec.Body.String())
	}
	// body 非 JSON
	rec = do(t, deps, "POST", "/api/v1/inject/project", `not-json`)
	if !strings.Contains(rec.Body.String(), `"ok":false`) || !strings.Contains(rec.Body.String(), `"reason":"body 解析失败`) {
		t.Fatalf("非 JSON body 未出声: %s", rec.Body.String())
	}
}

// TestInjectMountSkeletons 挂载补件——缺件按模板生成（带 BOM），已有件一字不改。
func TestInjectMountSkeletons(t *testing.T) {
	deps, root := newTestDeps(t)
	writeTestFile(t, filepath.Join(root, "templates", "skeleton-design-status.md"), "# {pid} 兑现总账\n")
	writeTestFile(t, filepath.Join(root, "templates", "skeleton-glossary.md"), "# {pid} 术语\n")
	writeTestFile(t, filepath.Join(root, "templates", "skeleton-job-skill.md"), "# {job} 方法\n")
	writeTestFile(t, filepath.Join(root, "templates", "skeleton-job-exp.md"), "# {job} 语料\n")
	writeTestFile(t, filepath.Join(root, "templates", "skeleton-job-tools.md"), "# {job} 工具\n")
	writeTestFile(t, filepath.Join(root, "templates", "skeleton-l3-readme.md"), "# {pid} 现场手册\n")
	// 只有 project.md 的裸项目
	writeTestFile(t, filepath.Join(root, "Project", "Partial", "project.md"), "# Partial 原档")
	deps.Dispatch = func(command string) bool { return true }

	rec := do(t, deps, "POST", "/api/v1/inject/project", `{"cat":"majordomo","project":"Partial"}`)
	if !strings.Contains(rec.Body.String(), " · 补件 6 件") {
		t.Fatalf("补件计数不符: %s", rec.Body.String())
	}
	raw, err := os.ReadFile(filepath.Join(root, "Project", "Partial", "design-status.md"))
	if err != nil {
		t.Fatalf("补件未生成: %v", err)
	}
	if !strings.HasPrefix(string(raw), "\uFEFF# Partial 兑现总账") {
		t.Fatalf("骨架形态不符（BOM + 占位替换）: %q", string(raw))
	}
	// 已存在的 project.md 一字不改
	raw, err = os.ReadFile(filepath.Join(root, "Project", "Partial", "project.md"))
	if err != nil || string(raw) != "# Partial 原档" {
		t.Fatalf("已有件被改写: %q %v", string(raw), err)
	}
	// Job 三件 + L3 readme 一并补齐
	for _, path := range [][]string{
		{"L2", "Job", "Partial", "skill.md"},
		{"L2", "Job", "Partial", "exp.md"},
		{"L2", "Job", "Partial", "tools.md"},
		{"L3", "Partial", "readme.md"},
	} {
		if _, err := os.Stat(filepath.Join(append([]string{root}, path...)...)); err != nil {
			t.Fatalf("缺件未生成: %v（%v）", path, err)
		}
	}
}

// TestDbEndpointsPlaceholder 段 11 四端点——服务未接入按 DB_SERVICE_UNAVAILABLE 如实回执。
func TestDbEndpointsPlaceholder(t *testing.T) {
	deps, _ := newTestDeps(t)
	cases := []struct {
		method string
		target string
	}{
		{"GET", "/api/v1/db/libs"},
		{"POST", "/api/v1/db/libs"},
		{"GET", "/api/v1/db/libs/demo/entries"},
		{"GET", "/api/v1/db/libs/demo/entries/1"},
	}
	for _, item := range cases {
		rec := do(t, deps, item.method, item.target, `{"id":"demo"}`)
		if rec.Code != 200 {
			t.Fatalf("%s %s 状态码 %d", item.method, item.target, rec.Code)
		}
		order := objectKeyOrder(t, rec.Body.Bytes())
		if strings.Join(order, ",") != "ok,code,message" {
			t.Fatalf("%s 键序不符: %v", item.target, order)
		}
		if !strings.Contains(rec.Body.String(), `"code":"DB_SERVICE_UNAVAILABLE"`) {
			t.Fatalf("%s 占位回执不符: %s", item.target, rec.Body.String())
		}
	}
}

// decodeListJSON 解码「JSON 数组文本」字段——C# 回执该字段是字符串值（不是数组），前端再解析。
func decodeListJSON(t *testing.T, text string) []string {
	t.Helper()
	var list []string
	if err := json.Unmarshal([]byte(text), &list); err != nil {
		t.Fatalf("清单不是 JSON 数组文本: %q（%v）", text, err)
	}
	return list
}
