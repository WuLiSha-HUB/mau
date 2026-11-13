package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	maufs "cathome5/mau/fs"

	"cathome5/host/config"
)

// 本文件是段 12「项目挂载」管理面——前文注入清单的「项目段」增删。
//
// 权威实现 = `mau:CatHome4/CatHome4.Admin/AdminService.Inject.cs`：路径 / 查询参数 /
// 响应 JSON 字段与顺序逐条对齐（前端零改动是硬约束）。语义：
//   - 挂载 = 该项目 Job 三件 + L3 段 + 档案四件写进该猫 injectList，并（未开始对话时）立即重建前文
//   - 取消 = 摘掉上一次挂载留下的条目后再重建
//   - 取数 = 项目清单与最后改动时间在**点击时**才扫盘（不在会话启动路径上）
const (
	// projectSpecPrefix 项目档案条目前缀——识别与移除上一次挂载留下的条目
	projectSpecPrefix = "ccbp:Project/"
	// jobSpecPrefix 项目 Job 条目前缀
	jobSpecPrefix = "ccbp:L2/Job/"
	// l3SpecPrefix L3 易遗漏事项条目前缀（挂载时移除上一次挂载留下的 L3 索引条目）
	l3SpecPrefix = "ccbp:L3/"
	// workspaceSpecPrefix workspace 独立立项的项目档前缀——档案就在工作区根下同名目录，不进 CCBP
	workspaceSpecPrefix = "workspace:"
)

// injectProjectItem 是候选项目条目（projects 数组元素）。
type injectProjectItem struct {
	// Name 项目名（目录名）
	Name string `json:"name"`
	// LastWrite 最后改动时刻（本地时间 yyyy-MM-ddTHH:mm:ss）
	LastWrite string `json:"lastWrite"`
	// Files 项目目录下文件总数（递归）
	Files int `json:"files"`
}

// injectProjectsResponse 是 GET /api/v1/inject/projects 成功回执（键序 ok / root / projects）。
type injectProjectsResponse struct {
	// OK 恒 true
	OK bool `json:"ok"`
	// Root 解析后的 Project 根绝对路径
	Root string `json:"root"`
	// Projects 候选项目清单（最后改动倒序）
	Projects []injectProjectItem `json:"projects"`
}

// injectFailResponse 是段 12 的失败回执（键序 ok / reason——C# 该面统一用 reason 而非 error）。
type injectFailResponse struct {
	// OK 恒 false
	OK bool `json:"ok"`
	// Reason 失败原因（原文直读）
	Reason string `json:"reason"`
}

// injectPreviewResponse 是 GET /api/v1/inject/preview 回执（键序 ok / injectList / expert / mount）。
type injectPreviewResponse struct {
	// OK 恒 true
	OK bool `json:"ok"`
	// InjectList 重算后的注入清单——**JSON 数组文本**（C# JsonUtil.Array 把数组编码成字符串值，前端按字符串再解析；逐字对齐）
	InjectList string `json:"injectList"`
	// Expert 生效的专家模式
	Expert bool `json:"expert"`
	// Mount 目标项目名（空 = 取消挂载）
	Mount string `json:"mount"`
}

// injectProjectWriteResponse 是 POST /api/v1/inject/project 回执（键序 ok / msg / injectList）。
type injectProjectWriteResponse struct {
	// OK 恒 true
	OK bool `json:"ok"`
	// Msg 人读摘要（含重建 / 专家语料 / 补件提示）
	Msg string `json:"msg"`
	// InjectList 落盘后的注入清单——同 preview，JSON 数组文本
	InjectList string `json:"injectList"`
}

// injectAutoResponse 是自动识别开关回执（键序 ok / cat / on）。
type injectAutoResponse struct {
	// OK 读取或写入是否成功
	OK bool `json:"ok"`
	// Cat 猫寻址键
	Cat string `json:"cat"`
	// On 自动识别开关现值
	On bool `json:"on"`
}

// injectAutoGetFailResponse 是 GET /api/v1/inject/auto 的失败回执（C# 该面用 error 键）。
type injectAutoGetFailResponse struct {
	// OK 恒 false
	OK bool `json:"ok"`
	// Error 失败原因
	Error string `json:"error"`
}

// injectAutoPostFailResponse 是 POST /api/v1/inject/auto 的失败回执（C# 该面用 reason 键）。
type injectAutoPostFailResponse struct {
	// OK 恒 false
	OK bool `json:"ok"`
	// Reason 失败原因
	Reason string `json:"reason"`
}

// resolveSpec 受控根寻址解析——spec → 绝对路径；解析失败返回空串（不阻断，由调用方判空）。
func (d *Deps) resolveSpec(spec string) string {
	if d.Workspace == nil || strings.TrimSpace(spec) == "" {
		return ""
	}
	svc := &maufs.Service{Roots: d.Workspace.FSRoots()}
	full, err := svc.Resolve(spec, false)
	if err != nil {
		return ""
	}
	return full
}

// scopeRoot 解析一个受控根（裸 id）为绝对路径——失败返回空串。
func (d *Deps) scopeRoot(rootID string) string {
	return d.resolveSpec(rootID)
}

// handleInjectProjectsGet 服务 GET /api/v1/inject/projects——候选项目清单（点击时才扫盘）。
func (d *Deps) handleInjectProjectsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, d.buildInjectProjects())
}

// buildInjectProjects 扫盘构建候选项目清单——CCBP 项目 + workspace 独立立项（同口径去重）。
//
// 返回 injectProjectsResponse（成功）或 injectFailResponse（Project 根不可解析）。
func (d *Deps) buildInjectProjects() any {
	root := d.resolveSpec(strings.TrimSuffix(projectSpecPrefix, "/"))
	if root == "" {
		return injectFailResponse{OK: false, Reason: "Project 根不可解析：" + root}
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return injectFailResponse{OK: false, Reason: "Project 根不可解析：" + root}
	}
	rows := make([]injectProjectItem, 0, 64)
	entries, err := os.ReadDir(root)
	if err != nil {
		return injectFailResponse{OK: false, Reason: "Project 根不可解析：" + root}
	}
	listed := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		item := d.scanProjectRow(filepath.Join(root, name), name)
		rows = append(rows, item)
		listed[strings.ToLower(name)] = true
	}
	// workspace 独立立项——档案就在工作区根下同名目录、不进 CCBP：判据 = 目录下有 project.md
	if wsRoot := d.scopeRoot("workspace"); wsRoot != "" {
		if wsEntries, err := os.ReadDir(wsRoot); err == nil {
			for _, entry := range wsEntries {
				if !entry.IsDir() {
					continue
				}
				name := entry.Name()
				if strings.HasPrefix(name, ".") || listed[strings.ToLower(name)] {
					continue
				}
				dir := filepath.Join(wsRoot, name)
				if _, statErr := os.Stat(filepath.Join(dir, "project.md")); statErr != nil {
					continue
				}
				rows = append(rows, d.scanProjectRow(dir, name))
			}
		}
	}
	// 排序：最后改动时间倒序（最新在前）；并列按名字升序（确定性——C# 同值下顺序未定义）
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].LastWrite == rows[j].LastWrite {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].LastWrite > rows[j].LastWrite
	})
	return injectProjectsResponse{OK: true, Root: root, Projects: rows}
}

// scanProjectRow 统计单个项目目录——目录 mtime 与递归最新文件 mtime 取大者 + 文件总数。
func (d *Deps) scanProjectRow(dir, name string) injectProjectItem {
	latest := time.Time{}
	if info, err := os.Stat(dir); err == nil {
		latest = info.ModTime()
	}
	files := 0
	if err := walkProjectFiles(dir, &latest, &files); err != nil {
		// 扫盘失败必须出声——静默会表现为「时间停在上一次」
		d.logInject(2, "项目扫盘失败："+dir+"（"+err.Error()+"）")
	}
	return injectProjectItem{Name: name, LastWrite: latest.Format("2006-01-02T15:04:05"), Files: files}
}

// walkProjectFiles 递归累计文件数与最新 mtime（子目录不可读即失败返回，由调用方出声）。
func walkProjectFiles(dir string, latest *time.Time, files *int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		full := filepath.Join(dir, entry.Name())
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		if entry.IsDir() {
			if walkErr := walkProjectFiles(full, latest, files); walkErr != nil {
				return walkErr
			}
			continue
		}
		if info.ModTime().After(*latest) {
			*latest = info.ModTime()
		}
		*files = *files + 1
	}
	return nil
}

// handleInjectPreviewGet 服务 GET /api/v1/inject/preview?cat=&project=&expert=——只读，不落盘。
func (d *Deps) handleInjectPreviewGet(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	catKey := query.Get("cat")
	project := query.Get("project")
	expert, expertSet := parseExpertParam(query.Get("expert"))

	var current []string
	oldMount := ""
	curExpert := true
	if catKey != "" {
		if cfg, err := config.LoadCatCfg(d.catCfgPath(catKey)); err == nil && cfg != nil {
			current = cfg.InjectList
			oldMount = cfg.ProjectMount
			curExpert = cfg.ProjectMountExpert
		}
	}
	if current == nil {
		// 无 cat（或该猫无 cat.cfg）→ 按新猫模板算，与 cat.new 的继承口径一致
		current = []string{}
		if tpl, err := config.LoadCatDefault(d.catDefaultPath()); err == nil && tpl != nil && tpl.DefaultInjectList != nil {
			current = tpl.DefaultInjectList
		}
	}
	useExpert := curExpert
	if expertSet {
		useExpert = expert
	}
	next := d.buildInjectListWithMount(current, project, oldMount, useExpert)
	writeJSON(w, injectPreviewResponse{OK: true, InjectList: compactListJSON(next), Expert: useExpert, Mount: project})
}

// parseExpertParam 解析 expert 查询参数——只认 true / false（其余 = 未指定，沿用现值）。
func parseExpertParam(raw string) (bool, bool) {
	if strings.EqualFold(raw, "true") {
		return true, true
	}
	if strings.EqualFold(raw, "false") {
		return false, true
	}
	return false, false
}

// handleInjectProjectPost 服务 POST /api/v1/inject/project——body {cat, project, expert}。
//
// 写面口径：归一（trim）→ 校验（猫 / 项目存在性）→ 重算清单 → 原子写 cat.cfg →（未开始对话时）立即重建。
func (d *Deps) handleInjectProjectPost(w http.ResponseWriter, r *http.Request) {
	fields, ok := readBodyFields(r)
	if !ok {
		writeJSON(w, injectFailResponse{OK: false, Reason: "body 解析失败：body 非 JSON"})
		return
	}
	catKey := fieldString(fields, "cat")
	project := fieldString(fields, "project")
	expert, expertSet := parseExpertField(fields)
	resp, _ := d.applyInjectProject(catKey, project, expert, expertSet, true)
	writeJSON(w, resp)
}

// parseExpertField 解析 body.expert——只认布尔（缺失 / 非布尔 = 沿用现值）。
func parseExpertField(fields map[string]json.RawMessage) (bool, bool) {
	raw, present := fields["expert"]
	if !present {
		return false, false
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, false
	}
	return value, true
}

// applyInjectProject 项目挂载执行体——校验猫与项目 → 补件 → 重算清单 → 落盘 →（未开始对话时）重建。
//
// 多猫面归 P5：当前只有主干会话，非 majordomo 一律按「未找到猫」如实回执（与 C# 单猫形态同形）。
// rebuild=false 只落盘并回传新清单，由调用方决定重建方式（消息自动挂载用——避免先重建再投消息的竞态）。
func (d *Deps) applyInjectProject(catKey, project string, expert bool, expertSet bool, rebuild bool) (any, []string) {
	proj := strings.TrimSpace(project)
	if catKey != "majordomo" {
		d.logInject(2, "项目挂载失败：未找到猫 "+(catKey))
		return injectFailResponse{OK: false, Reason: "未找到猫：" + catKey}, nil
	}
	return d.applyInjectProjectDefault(proj, expert, expertSet, rebuild)
}

// applyInjectProjectDefault 主干（majordomo）项目挂载执行体——cat.cfg 合并写 +（未开始对话时）session.new。
func (d *Deps) applyInjectProjectDefault(project string, expert bool, expertSet bool, rebuild bool) (any, []string) {
	path := d.catCfgPath("majordomo")
	cfg, err := config.LoadCatCfg(path)
	if err != nil || cfg == nil {
		d.logInject(2, "项目挂载失败：majordomo cat.cfg 不可读（"+path+"）")
		return injectFailResponse{OK: false, Reason: "cat.cfg 不存在：majordomo"}, nil
	}
	if project != "" && !d.projectDirExists(project) {
		d.logInject(2, "项目挂载失败：项目不存在 "+project+"（cat=majordomo）")
		return injectFailResponse{OK: false, Reason: "项目不存在：" + project}, nil
	}
	// 缺件骨架先补齐再算清单——否则清单按「文件存在才加」会漏掉刚生成的件
	warnings := []string{}
	made := d.ensureSkeletonsOnMount(project, &warnings)
	for _, warning := range warnings {
		d.logInject(2, "挂载补件（cat=majordomo）："+warning)
	}
	oldMount := cfg.ProjectMount
	// 专家模式——未显式指定则沿用 cat.cfg 现值（缺省 true）
	if expertSet {
		cfg.ProjectMountExpert = expert
	}
	next := d.buildInjectListWithMount(cfg.InjectList, project, oldMount, cfg.ProjectMountExpert)
	cfg.InjectList = next
	cfg.ProjectMount = project
	msgCount := d.sessionMessageCount()
	// 自动取消计时起点——只在「挂好还没开始对话」时计时
	if project != "" && msgCount <= 1 {
		cfg.ProjectMountAt = time.Now().UnixMilli()
	} else {
		cfg.ProjectMountAt = 0
	}
	if err := config.SaveCatCfg(path, cfg); err != nil {
		d.logInject(2, "项目挂载失败：cat.cfg 写入失败（"+err.Error()+"）")
		return injectFailResponse{OK: false, Reason: "cat.cfg 写入失败: " + err.Error()}, nil
	}
	// 自报身份同步——项目 / 模式 / Job 清单进请求附加头容器（未挂载 = 空，适配层整组不发）
	if sess := d.sessionFor("majordomo"); sess != nil {
		sess.SetSelfTagMount(project, MountModeText(project, cfg.ProjectMountExpert), BuildJobList(next))
	}
	// 会话未开始对话 → 立即重建（等同新会话重注入）；已有对话 → 只落盘，下个新会话生效
	fresh := msgCount <= 1
	rebuilt := fresh && rebuild && d.Dispatch != nil
	if rebuilt {
		d.Dispatch("session.new")
	}
	msg := ""
	if project == "" {
		if rebuilt {
			msg = "已取消 Project 挂载——会话重建中"
		} else {
			msg = "已取消 Project 挂载——新会话生效"
		}
	} else {
		if rebuilt {
			msg = "已挂载 " + project + "——会话重建中"
		} else {
			msg = "已挂载 " + project + "——新会话生效"
		}
		if cfg.ProjectMountExpert {
			msg = msg + mountExpertText(d.measureL3ExtraBytes(project))
		} else {
			msg = msg + " · L3 仅 readme"
		}
	}
	if len(made) > 0 {
		msg = msg + " · 补件 " + strconv.Itoa(len(made)) + " 件"
	}
	projectText := project
	if projectText == "" {
		projectText = "(空)"
	}
	d.logInject(1, "项目挂载：cat=majordomo project="+projectText+" injectList="+strconv.Itoa(len(next)))
	return injectProjectWriteResponse{OK: true, Msg: msg, InjectList: compactListJSON(next)}, next
}

// compactListJSON 清单序列化为紧凑 JSON 数组文本——C# 侧 JsonUtil.Array 产出字符串值，
// 前端按「字符串再解析」消费（实测 C# 回执 injectList 是 string，不是 array）：逐字对齐。
func compactListJSON(list []string) string {
	if list == nil {
		list = []string{}
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(list); err != nil {
		return "[]"
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// sessionMessageCount 当前会话前文条数（无会话按 0——与 C# DefaultSession == null 同口径）。
func (d *Deps) sessionMessageCount() int {
	sess := d.sessionFor("majordomo")
	if sess == nil || sess.Context == nil {
		return 0
	}
	return sess.Context.Count()
}

// projectDocSpec 项目档案条目前缀——先认 ccbp:Project/<名>，再回落 workspace:<名>（两处都不存在 = 空串）。
func (d *Deps) projectDocSpec(project string) string {
	pid := strings.TrimSpace(project)
	if pid == "" {
		return ""
	}
	ccbpSpec := projectSpecPrefix + pid + "/"
	if dir := d.resolveSpec(strings.TrimSuffix(ccbpSpec, "/")); dir != "" && dirExists(dir) {
		return ccbpSpec
	}
	wsSpec := workspaceSpecPrefix + pid + "/"
	if dir := d.resolveSpec(strings.TrimSuffix(wsSpec, "/")); dir != "" && dirExists(dir) {
		return wsSpec
	}
	return ""
}

// projectDirExists 项目目录存在性判定（判据单点在 projectDocSpec）。
func (d *Deps) projectDirExists(project string) bool {
	return d.projectDocSpec(project) != ""
}

// isWorkspaceProjectDoc 是否 workspace 独立立项的项目档条目——形态 workspace:<项目名>/<四件名>（二级路径）。
//
// 三级路径 workspace:tools/<工程>/project.md 不算（工具区工程的项目档不参与项目挂载摘除）。
func isWorkspaceProjectDoc(spec, project string) bool {
	if spec == "" || project == "" {
		return false
	}
	if !strings.HasPrefix(strings.ToLower(spec), workspaceSpecPrefix) {
		return false
	}
	rest := strings.ReplaceAll(spec[len(workspaceSpecPrefix):], "\\", "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		return false
	}
	if !strings.EqualFold(parts[0], project) {
		return false
	}
	switch strings.ToLower(parts[1]) {
	case "design-status.md", "glossary.md", "project.md", "todo.md":
		return true
	}
	return false
}

// buildInjectListWithMount 注入清单重算——移除上一次挂载留下的条目，按需追加 Job 三件 + L3 段 + 档案四件。
//
// 顺序对齐 C#（并发协议侧统一于 2026-11-07）：Job 三件（exp / skill / tools）→ L3 段 → 档案四件
// （design-status / glossary / project / todo）。保序：未命中移除条件的条目逐字保留原位。
func (d *Deps) buildInjectListWithMount(current []string, project, mountedProject string, expert bool) []string {
	oldMount := strings.TrimSpace(mountedProject)
	list := make([]string, 0, len(current)+8)
	for _, raw := range current {
		item := raw
		if item == "" {
			continue
		}
		if strings.HasPrefix(item, projectSpecPrefix) {
			continue
		}
		// workspace 独立立项的项目档四件——旧挂载留下的条目同口径摘掉
		if oldMount != "" && isWorkspaceProjectDoc(item, oldMount) {
			continue
		}
		if oldMount != "" && strings.HasPrefix(item, jobSpecPrefix+oldMount+"/") {
			continue
		}
		// L3 段两种形态都要摘：专家模式挂目录条目（无尾斜杠），关模式挂 readme.md 单件
		if oldMount != "" && item == l3SpecPrefix+oldMount {
			continue
		}
		if oldMount != "" && strings.HasPrefix(item, l3SpecPrefix+oldMount+"/") {
			continue
		}
		list = append(list, item)
	}
	if project == "" {
		return list
	}
	// ① Job 三件
	if jobDir := d.resolveSpec(jobSpecPrefix + project); jobDir != "" && dirExists(jobDir) {
		for _, name := range []string{"exp.md", "skill.md", "tools.md"} {
			if fileExists(filepath.Join(jobDir, name)) {
				list = append(list, jobSpecPrefix+project+"/"+name)
			}
		}
	}
	// ② L3 段——Job 段之下、项目档案之上：专家模式=整目录（一级 *.md 按名展开）；关=只挂 readme.md
	if expert {
		spec := l3SpecPrefix + project
		if dir := d.resolveSpec(spec); dir != "" && dirExists(dir) {
			list = append(list, spec)
		}
	} else {
		spec := l3SpecPrefix + project + "/readme.md"
		if path := d.resolveSpec(spec); path != "" && fileExists(path) {
			list = append(list, spec)
		}
	}
	// ③ 项目档案四件
	if projDocSpec := d.projectDocSpec(project); projDocSpec != "" {
		projDir := d.resolveSpec(strings.TrimSuffix(projDocSpec, "/"))
		if projDir != "" && dirExists(projDir) {
			for _, name := range []string{"design-status.md", "glossary.md", "project.md", "todo.md"} {
				if fileExists(filepath.Join(projDir, name)) {
					list = append(list, projDocSpec+name)
				}
			}
		}
	}
	return list
}

// BuildJobList 从注入清单提取 Job 清单——条目前缀 ccbp:L2/Job/，取其后第一段目录名，去重保序。
//
// 🔴 单点规则：装配初值（cmd/cathome5）、挂载 / 取消挂载刷新、自报头取值共用本函数；
// 语义对齐 C# AdminService.BuildJobList——禁第二份实现。
func BuildJobList(list []string) string {
	jobs := make([]string, 0, 4)
	seen := make(map[string]bool, 4)
	for _, item := range list {
		if !strings.HasPrefix(strings.ToLower(item), strings.ToLower(jobSpecPrefix)) {
			continue
		}
		rest := item[len(jobSpecPrefix):]
		name := rest
		if cut := strings.Index(rest, "/"); cut >= 0 {
			name = rest[:cut]
		}
		if name != "" && !seen[name] {
			seen[name] = true
			jobs = append(jobs, name)
		}
	}
	return strings.Join(jobs, ",")
}

// MountModeText 挂载模式文本——未挂载为空（适配层整组不发），挂载态 expert / readme。
//
// 语义对齐 C#：默认猫分支 project.Length == 0 ? "" : (ProjectMountExpert ? "expert" : "readme")。
func MountModeText(project string, expert bool) string {
	if project == "" {
		return ""
	}
	if expert {
		return "expert"
	}
	return "readme"
}

// ensureSkeletonsOnMount 挂载补件——三类缺件：项目档两件 / 项目 Job 三件 / L3 readme；已存在即跳过。
//
// 返回本次新建条目清单；模板缺失与写失败经 warnings 出声（不静默）。
func (d *Deps) ensureSkeletonsOnMount(project string, warnings *[]string) []string {
	made := []string{}
	pid := strings.TrimSpace(project)
	if pid == "" {
		return made
	}
	templatesDir := d.resolveSpec("ccbp:templates")
	if templatesDir == "" || !dirExists(templatesDir) {
		*warnings = append(*warnings, "ccbp 根不可解析——模板目录未知，本次不补骨架")
		return made
	}
	// ① 项目档两件——目录不存在不建（档案必须已存在，否则挂载本身已被拒）
	projDocSpec := d.projectDocSpec(pid)
	if projDocSpec != "" {
		projDir := d.resolveSpec(strings.TrimSuffix(projDocSpec, "/"))
		if projDir != "" && dirExists(projDir) {
			d.generateSkeleton(templatesDir, "skeleton-design-status.md", filepath.Join(projDir, "design-status.md"), "{pid}", pid, &made, warnings, projDocSpec+"design-status.md")
			d.generateSkeleton(templatesDir, "skeleton-glossary.md", filepath.Join(projDir, "glossary.md"), "{pid}", pid, &made, warnings, projDocSpec+"glossary.md")
		}
	}
	// ② 项目 Job 三件——目录不存在连目录一起建（同并发协议面板口径）
	jobDir := d.resolveSpec(jobSpecPrefix + pid)
	if jobDir != "" {
		if err := os.MkdirAll(jobDir, 0o755); err != nil {
			*warnings = append(*warnings, "建目录失败 L2/Job/"+pid+"："+err.Error())
			jobDir = ""
		}
		if jobDir != "" {
			d.generateSkeleton(templatesDir, "skeleton-job-skill.md", filepath.Join(jobDir, "skill.md"), "{job}", pid, &made, warnings, "L2/Job/"+pid+"/skill.md")
			d.generateSkeleton(templatesDir, "skeleton-job-exp.md", filepath.Join(jobDir, "exp.md"), "{job}", pid, &made, warnings, "L2/Job/"+pid+"/exp.md")
			d.generateSkeleton(templatesDir, "skeleton-job-tools.md", filepath.Join(jobDir, "tools.md"), "{job}", pid, &made, warnings, "L2/Job/"+pid+"/tools.md")
		}
	}
	// ③ L3 易遗漏事项索引——目录不存在连目录一起建；存在即跳过
	l3Dir := d.resolveSpec(l3SpecPrefix + pid)
	if l3Dir != "" {
		if err := os.MkdirAll(l3Dir, 0o755); err != nil {
			*warnings = append(*warnings, "建目录失败 L3/"+pid+"："+err.Error())
			l3Dir = ""
		}
		if l3Dir != "" {
			d.generateSkeleton(templatesDir, "skeleton-l3-readme.md", filepath.Join(l3Dir, "readme.md"), "{pid}", pid, &made, warnings, "L3/"+pid+"/readme.md")
		}
	}
	return made
}

// generateSkeleton 按模板生成一个骨架文件——目标已存在即跳过；模板缺失 / 写失败出声。
func (d *Deps) generateSkeleton(templatesDir, templateName, targetPath, token, value string, made *[]string, warnings *[]string, label string) {
	if fileExists(targetPath) {
		return
	}
	raw, err := os.ReadFile(filepath.Join(templatesDir, templateName))
	if err != nil {
		*warnings = append(*warnings, "SKELETON_TEMPLATE_MISSING: 模板不存在 "+templateName+"（"+label+" 未生成）")
		return
	}
	text := strings.ReplaceAll(string(config.StripBOM(raw)), token, value)
	if err := os.WriteFile(targetPath, append([]byte{0xEF, 0xBB, 0xBF}, []byte(text)...), 0o644); err != nil {
		*warnings = append(*warnings, "SKELETON_FAIL: "+label+" 生成失败——"+err.Error())
		return
	}
	*made = append(*made, label)
}

// measureL3ExtraBytes L3 专家段额外语料字节——一级 *.md 之和（readme.md 不计入）。
func (d *Deps) measureL3ExtraBytes(project string) int64 {
	if project == "" {
		return 0
	}
	dir := d.resolveSpec(l3SpecPrefix + project)
	if dir == "" || !dirExists(dir) {
		return 0
	}
	total := int64(0)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			continue
		}
		if strings.EqualFold(entry.Name(), "readme.md") {
			continue
		}
		if info, err := entry.Info(); err == nil {
			total = total + info.Size()
		}
	}
	return total
}

// mountExpertText 专家段文案——有料给「专家 +15.9k」，缺位给「专家 +0k(缺位)」。
//
// 换算 3.07 KB/k token：标定自 C# 1.8.242 真机双读数（319.8KB≈104k / 405.9KB≈133k）。
func mountExpertText(extraBytes int64) string {
	if extraBytes <= 0 {
		return " · 专家 +0k(缺位)"
	}
	k := float64(extraBytes) / 1024.0 / 3.07
	return " · 专家 +" + strconv.FormatFloat(k, 'f', 1, 64) + "k"
}

// handleInjectAutoGet 服务 GET /api/v1/inject/auto?cat=——读每猫自动识别开关（缺省 true）。
//
// 实现单点 = InjectAutoJSON（主端口条件注册版共用，不造第二份规则）。
func (d *Deps) handleInjectAutoGet(w http.ResponseWriter, r *http.Request) {
	writeRawJSON(w, d.InjectAutoJSON(r.URL.Query().Get("cat")))
}

// handleInjectAutoPost 服务 POST /api/v1/inject/auto——字段级合并写 cat.cfg.mountAuto。
//
// 关 = 只认手动挂载；开 = 消息里「上工/开工 X」自动挂 + 空闲自动取消。
// 实现单点 = InjectAutoSetJSON（主端口条件注册版共用）。
func (d *Deps) handleInjectAutoPost(w http.ResponseWriter, r *http.Request) {
	fields, ok := readBodyFields(r)
	if !ok {
		writeJSON(w, injectAutoPostFailResponse{OK: false, Reason: "body 解析失败"})
		return
	}
	var on *bool
	if raw, present := fields["on"]; present {
		var value bool
		if err := json.Unmarshal(raw, &value); err == nil {
			on = &value
		}
	}
	writeRawJSON(w, d.InjectAutoSetJSON(fieldString(fields, "cat"), on))
}

// fileExists 判定文件存在（非目录）。
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// logInject 记一条 INJECT 面观测日志（类别与 C# 项目挂载面同名；nil 出口静默丢弃）。
func (d *Deps) logInject(level int, message string) {
	if d.Logf != nil {
		d.Logf("INJECT", level, message)
	}
}
