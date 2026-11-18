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
	"cathome5/host/session"
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
	// skillPackageSpec 精通档额外挂载的 skill package 整目录条目——放开「仅 _index.md」限制。
	// 与基线里的 `ccbp:templates/skill package/_index.md` 单件不同：本条是整目录（BuildPrompt 展开全部 *.md）。
	// 取消挂载 / 切档时按整条精确摘除（不带文件名后缀），不碰基线的 _index.md。
	skillPackageSpec = "ccbp:templates/skill package"
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

// injectPreviewResponse 是 GET /api/v1/inject/preview 回执（键序 ok / injectList / expert / mount / level）。
type injectPreviewResponse struct {
	// OK 恒 true
	OK bool `json:"ok"`
	// InjectList 重算后的注入清单——**JSON 数组文本**（C# JsonUtil.Array 把数组编码成字符串值，前端按字符串再解析；逐字对齐）
	InjectList string `json:"injectList"`
	// Expert 生效的专家模式（兼容位：level>=2 → true）
	Expert bool `json:"expert"`
	// Mount 目标项目名（空 = 取消挂载）
	Mount string `json:"mount"`
	// Level 生效的项目挂载档位（1=普通 2=专家 3=精通）
	Level int `json:"level"`
}

// injectProjectWriteResponse 是 POST /api/v1/inject/project 回执（键序 ok / msg / injectList / rebuilt）。
type injectProjectWriteResponse struct {
	// OK 恒 true
	OK bool `json:"ok"`
	// Msg 人读摘要（含重建 / 专家语料 / 补件提示）
	Msg string `json:"msg"`
	// InjectList 落盘后的注入清单——同 preview，JSON 数组文本
	InjectList string `json:"injectList"`
	// Rebuilt 本次调用是否已同步重建会话——true = 前文已按新清单重注入，
	// 调用方不必再发 cat.new-session（否则同一份清单会被编译两遍，每只猫白等一次重建）
	Rebuilt bool `json:"rebuilt"`
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
	level, levelSet := parseMountLevel(query.Get("level"), query.Get("expert"))

	var current []string
	oldMount := ""
	curLevel := config.MountLevelExpert
	if catKey != "" {
		if cfg, err := config.LoadCatCfg(d.catCfgPath(catKey)); err == nil && cfg != nil {
			current = cfg.InjectList
			oldMount = cfg.ProjectMount
			curLevel = config.ResolveMountLevel(cfg)
		}
	}
	if current == nil {
		// 无 cat（或该猫无 cat.cfg）→ 按新猫模板「启用集」算，与 cat.new 的继承口径一致。
		// 🔴 不能直接用 DefaultInjectList：未打勾的条目在 DefaultInjectDisabled 里，cat.new 不继承它们，
		// 预览也不能列出来——预览清单会拼进派活提示词的「前文已挂载」栏，列了等于告诉猫「已读」，实际没注入。
		current = []string{}
		if tpl, err := config.LoadCatDefault(d.catDefaultPath()); err == nil && tpl != nil {
			current = config.ResolveTplEnabledInject(tpl)
		}
	}
	useLevel := curLevel
	if levelSet {
		useLevel = level
	}
	next := d.buildInjectListWithMount(current, project, oldMount, useLevel, d.mountOffFor(project))
	writeJSON(w, injectPreviewResponse{OK: true, InjectList: compactListJSON(next), Expert: useLevel >= config.MountLevelExpert, Mount: project, Level: useLevel})
}

// parseMountLevel 解析档位参数——level 优先（1/2/3），无 level 时回落旧 expert 布尔（true→专家 / false→普通）。
// 两者都不给 = 未指定（沿用现值）。
func parseMountLevel(levelRaw, expertRaw string) (level int, set bool) {
	if strings.TrimSpace(levelRaw) != "" {
		if parsed, err := strconv.Atoi(strings.TrimSpace(levelRaw)); err == nil {
			if parsed >= config.MountLevelNormal && parsed <= config.MountLevelMaster {
				return parsed, true
			}
		}
	}
	if strings.EqualFold(expertRaw, "true") {
		return config.MountLevelExpert, true
	}
	if strings.EqualFold(expertRaw, "false") {
		return config.MountLevelNormal, true
	}
	return config.MountLevelExpert, false
}

// handleInjectProjectPost 服务 POST /api/v1/inject/project——body {cat, project, level}（兼容旧 expert 布尔）。
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
	level, levelSet := parseMountLevelField(fields)
	resp, _ := d.applyInjectProject(catKey, project, level, levelSet, true)
	writeJSON(w, resp)
}

// parseMountLevelField 解析 body.level——数值档位（1/2/3）优先；无 level 时回落 body.expert 布尔。
// 缺值 / 非法值 = 未指定（沿用现值）。
func parseMountLevelField(fields map[string]json.RawMessage) (int, bool) {
	if raw, present := fields["level"]; present {
		var parsed int
		if err := json.Unmarshal(raw, &parsed); err == nil && parsed >= config.MountLevelNormal && parsed <= config.MountLevelMaster {
			return parsed, true
		}
	}
	if raw, present := fields["expert"]; present {
		var value bool
		if err := json.Unmarshal(raw, &value); err == nil {
			if value {
				return config.MountLevelExpert, true
			}
			return config.MountLevelNormal, true
		}
	}
	return config.MountLevelExpert, false
}

// applyInjectProject 项目挂载执行体——校验猫与项目 → 补件 → 重算清单 → 落盘 →（未开始对话时）重建。
//
// majordomo 走默认猫专用分支（静态面同步）；其余按 CatLookup 寻址（多猫分支——对齐 C# ApplyInjectProject：
// id 精确 / 显示名 / id 前缀唯一；未命中 / 停机两句文案逐字）。
// rebuild=false 只落盘并回传新清单，由调用方决定重建方式（消息自动挂载用——避免先重建再投消息的竞态）。
func (d *Deps) applyInjectProject(catKey, project string, level int, levelSet bool, rebuild bool) (any, []string) {
	proj := strings.TrimSpace(project)
	if catKey == "majordomo" {
		return d.applyInjectProjectDefault(proj, level, levelSet, rebuild)
	}
	id, name, _, _, sess, ok := d.catLookup(catKey)
	if !ok {
		d.logInject(2, "项目挂载失败：未找到猫 "+catKey)
		return injectFailResponse{OK: false, Reason: "未找到猫：" + catKey}, nil
	}
	if sess == nil {
		d.logInject(2, "项目挂载失败：猫已停机 "+name)
		return injectFailResponse{OK: false, Reason: "猫已停机（无会话）：" + name}, nil
	}
	return d.applyInjectProjectCat(id, name, sess, proj, level, levelSet, rebuild)
}

// applyMountLevel 把显式档位写进 cat.cfg——levelSet=false 时沿用现值（兼容位也要保持同步）。
// 返回生效档位（后续清单 / 文案 / 自报头一律用本值）。
func applyMountLevel(cfg *config.CatCfg, level int, levelSet bool) int {
	if levelSet {
		cfg.ProjectMountLevel = config.NormalizeMountLevel(level)
		cfg.ProjectMountExpert = cfg.ProjectMountLevel >= config.MountLevelExpert
	}
	return config.ResolveMountLevel(cfg)
}

// measureMountExtraBytes 三档额外语料字节——与前端 irExtraK 同口径（3.07 KB/k）：
// 普通=0；专家=L3 一级 *.md 除 readme；精通=L3 递归全部文件除 readme + skill package 本目录除 _index.md。
func (d *Deps) measureMountExtraBytes(project string, level int) int64 {
	if project == "" || level == config.MountLevelNormal {
		return 0
	}
	total := d.measureL3ExtraBytes(project) // 专家：一级 *.md 除 readme
	if level == config.MountLevelMaster {
		// 精通：L3 递归补子目录文件（measureL3ExtraBytes 只统计一级）+ skill package 本目录除 _index.md
		if l3Dir := d.resolveSpec(l3SpecPrefix + project); l3Dir != "" && dirExists(l3Dir) {
			_ = filepath.WalkDir(l3Dir, func(path string, entry os.DirEntry, err error) error {
				if err != nil || entry.IsDir() || strings.EqualFold(entry.Name(), "readme.md") {
					return nil
				}
				rel, relErr := filepath.Rel(l3Dir, path)
				if relErr != nil || !strings.ContainsRune(filepath.ToSlash(rel), '/') {
					return nil // 一级文件已被 measureL3ExtraBytes 计入，只补子目录
				}
				if info, infoErr := entry.Info(); infoErr == nil {
					total += info.Size()
				}
				return nil
			})
		}
		if skillDir := d.resolveSpec(skillPackageSpec); skillDir != "" && dirExists(skillDir) {
			if entries, err := os.ReadDir(skillDir); err == nil {
				for _, entry := range entries {
					if entry.IsDir() || strings.EqualFold(entry.Name(), "_index.md") {
						continue
					}
					if info, err := entry.Info(); err == nil {
						total += info.Size()
					}
				}
			}
		}
	}
	return total
}

// mountLevelText 三档挂载文案——普通 L3 仅 readme / 专家 +k / 精通 +k（均按 3.07 KB/k 估值）。
func (d *Deps) mountLevelText(project string, level int) string {
	extraBytes := d.measureMountExtraBytes(project, level)
	switch level {
	case config.MountLevelNormal:
		return " · 普通（L3 仅 readme）"
	case config.MountLevelMaster:
		if extraBytes <= 0 {
			return " · 精通 +0k(缺位)"
		}
		return " · 精通 +" + strconv.FormatFloat(float64(extraBytes)/1024.0/3.07, 'f', 1, 64) + "k"
	default:
		return mountExpertText(extraBytes)
	}
}

// applyInjectProjectCat 多猫挂载执行体——cat.cfg 合并写 + 自报身份同步 +（未开始对话时）按猫重建。
//
// 语义逐条对齐 C# ApplyInjectProject 多猫分支：项目存在性 → 补件 → 专家模式 → 清单重算 →
// ProjectMountAt 计时起点 → 落盘 → SetSelfTagMount →（fresh && rebuild）目标猫 session.new。
func (d *Deps) applyInjectProjectCat(catID, displayName string, sess *session.Session, project string, level int, levelSet bool, rebuild bool) (any, []string) {
	path := d.catCfgPath(catID)
	cfg, err := config.LoadCatCfg(path)
	if err != nil || cfg == nil {
		d.logInject(2, "项目挂载失败：cat.cfg 不可读（"+path+"）")
		return injectFailResponse{OK: false, Reason: "cat.cfg 不存在：" + catID}, nil
	}
	if project != "" && !d.projectDirExists(project) {
		d.logInject(2, "项目挂载失败：项目不存在 "+project+"（cat="+displayName+"）")
		return injectFailResponse{OK: false, Reason: "项目不存在：" + project}, nil
	}
	// 缺件骨架先补齐再算清单——否则清单按「文件存在才加」会漏掉刚生成的件
	warnings := []string{}
	made := d.ensureSkeletonsOnMount(project, &warnings)
	for _, warning := range warnings {
		d.logInject(2, "挂载补件（cat="+displayName+"）："+warning)
	}
	oldMount := cfg.ProjectMount
	useLevel := applyMountLevel(cfg, level, levelSet)
	next := d.buildInjectListWithMount(cfg.InjectList, project, oldMount, useLevel, d.mountOffFor(project))
	cfg.InjectList = next
	cfg.ProjectMount = project
	msgCount := sessionMessageCountOf(sess)
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
	sess.SetSelfTagMount(project, MountModeText(project, useLevel), BuildJobList(next))
	// 会话未开始对话 → 立即重建（等同新会话重注入）；已有对话 → 只落盘，下个新会话生效
	fresh := msgCount <= 1
	rebuilt := fresh && rebuild
	if rebuilt && d.DispatchForCat != nil {
		d.DispatchForCat(catID, "session.new")
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
		msg = msg + d.mountLevelText(project, useLevel)
	}
	if len(made) > 0 {
		msg = msg + " · 补件 " + strconv.Itoa(len(made)) + " 件"
	}
	projectText := project
	if projectText == "" {
		projectText = "(空)"
	}
	d.logInject(1, "项目挂载：cat="+displayName+" project="+projectText+" level="+strconv.Itoa(useLevel)+" injectList="+strconv.Itoa(len(next)))
	return injectProjectWriteResponse{OK: true, Msg: msg, InjectList: compactListJSON(next), Rebuilt: rebuilt}, next
}

// applyInjectProjectDefault 主干（majordomo）项目挂载执行体——cat.cfg 合并写 +（未开始对话时）session.new。
func (d *Deps) applyInjectProjectDefault(project string, level int, levelSet bool, rebuild bool) (any, []string) {
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
	useLevel := applyMountLevel(cfg, level, levelSet)
	next := d.buildInjectListWithMount(cfg.InjectList, project, oldMount, useLevel, d.mountOffFor(project))
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
		sess.SetSelfTagMount(project, MountModeText(project, useLevel), BuildJobList(next))
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
		msg = msg + d.mountLevelText(project, useLevel)
	}
	if len(made) > 0 {
		msg = msg + " · 补件 " + strconv.Itoa(len(made)) + " 件"
	}
	projectText := project
	if projectText == "" {
		projectText = "(空)"
	}
	d.logInject(1, "项目挂载：cat=majordomo project="+projectText+" level="+strconv.Itoa(useLevel)+" injectList="+strconv.Itoa(len(next)))
	return injectProjectWriteResponse{OK: true, Msg: msg, InjectList: compactListJSON(next), Rebuilt: rebuilt}, next
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
	return sessionMessageCountOf(d.sessionFor("majordomo"))
}

// sessionMessageCountOf 指定会话前文条数（无会话 / 无上下文按 0——与 C# cat.Session == null 同口径）。
func sessionMessageCountOf(sess *session.Session) int {
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

// skillPackageBaselineSpec 是新猫默认模板基线里已挂的 skill package 索引单件——精通档展开时要跳过它（防重复），
// 摘除时要保护它（它不是挂载留下的，是基线固定组成）。
const skillPackageBaselineSpec = skillPackageSpec + "/_index.md"

// expandSpecFiles 递归展开目录下全部文件为注入 spec（prefix + "/" + 相对路径，统一正斜杠）。
// accept 非 nil 时按文件名过滤（nil = 全部文件，含子目录附件）；单个子项不可读按缺失跳过，不阻断。
// 结果按 spec 小写字典序稳定排序——注入清单顺序确定，消费点可对账。
func expandSpecFiles(prefix, dir string, accept func(string) bool) []string {
	out := []string{}
	root := filepath.Clean(dir)
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if accept != nil && !accept(entry.Name()) {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		out = append(out, strings.TrimRight(prefix, "/")+"/"+strings.ReplaceAll(rel, "\\", "/"))
		return nil
	})
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// expandSpecFilesFlat 仅展开目录下本层文件（不递归子目录）——spec = prefix + "/" + 文件名。
// 用途：skill package 精通档只挂本目录 35 件，其下子目录（若有）不挂。accept 非 nil 时按名过滤。
func expandSpecFilesFlat(prefix, dir string, accept func(string) bool) []string {
	out := []string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if accept != nil && !accept(entry.Name()) {
			continue
		}
		out = append(out, strings.TrimRight(prefix, "/")+"/"+entry.Name())
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// buildInjectListWithMount 注入清单重算——移除上一次挂载留下的条目，按需追加 Job 三件 + L3 段 + 档案四件 + 精通增强段。
//
// 顺序对齐 C#（并发协议侧统一于 2026-11-07）：Job 三件（exp / skill / tools）→ L3 段 → 档案四件
// （design-status / glossary / project / todo）→ 精通档 skill package 展开。保序：未命中移除条件的条目逐字保留原位。
//
// level 三档（config.MountLevel*）：
//
//	普通(1)：L3 仅 readme.md 单件
//	专家(2)：L3 整目录条目（BuildPrompt 展开一级 *.md）
//	精通(3)：L3/<项目> 递归全部文件（展开成逐条文件 spec）+ skill package 整目录（除基线 _index.md 单件）
//
// 🔴 off = 模板勾选关掉的挂载段（见 mountOffFor）——关掉的段不追加（摘除逻辑不受影响）。
func (d *Deps) buildInjectListWithMount(current []string, project, mountedProject string, level int, off map[string]bool) []string {
	oldMount := strings.TrimSpace(mountedProject)
	list := make([]string, 0, len(current)+16)
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
		// L3 段所有形态都摘：整目录条目（专家）/ readme.md 单件（普通）/ 递归子文件（精通）
		if oldMount != "" && (item == l3SpecPrefix+oldMount || strings.HasPrefix(item, l3SpecPrefix+oldMount+"/")) {
			continue
		}
		// 精通档 skill package 展开条目——精确按前缀摘，但保护基线 _index.md 单件
		if oldMount != "" && strings.HasPrefix(item, skillPackageSpec+"/") && item != skillPackageBaselineSpec {
			continue
		}
		list = append(list, item)
	}
	if project == "" {
		return list
	}
	// ① Job 三件
	if !off["job"] {
		if jobDir := d.resolveSpec(jobSpecPrefix + project); jobDir != "" && dirExists(jobDir) {
			for _, name := range []string{"exp.md", "skill.md", "tools.md"} {
				if fileExists(filepath.Join(jobDir, name)) {
					list = append(list, jobSpecPrefix+project+"/"+name)
				}
			}
		}
	}
	// ② L3 段——Job 段之下、项目档案之上
	if !off["l3"] {
		switch level {
		case config.MountLevelNormal:
			spec := l3SpecPrefix + project + "/readme.md"
			if path := d.resolveSpec(spec); path != "" && fileExists(path) {
				list = append(list, spec)
			}
		case config.MountLevelMaster:
			// 精通：递归全部文件（含子目录 F-推理现场/ 下的 record.md / json 附件）
			if dir := d.resolveSpec(l3SpecPrefix + project); dir != "" && dirExists(dir) {
				list = append(list, expandSpecFiles(l3SpecPrefix+project, dir, nil)...)
			}
		default: // 专家（含兼容缺省）
			spec := l3SpecPrefix + project
			if dir := d.resolveSpec(spec); dir != "" && dirExists(dir) {
				list = append(list, spec)
			}
		}
	}
	// ③ 项目档案四件
	if projDocSpec := d.projectDocSpec(project); projDocSpec != "" {
		projDir := d.resolveSpec(strings.TrimSuffix(projDocSpec, "/"))
		if projDir != "" && dirExists(projDir) {
			for _, name := range []string{"design-status.md", "glossary.md", "project.md", "todo.md"} {
				if off[strings.TrimSuffix(name, ".md")] {
					continue
				}
				if fileExists(filepath.Join(projDir, name)) {
					list = append(list, projDocSpec+name)
				}
			}
		}
	}
	// ④ 精通增强段：skill package 仅本目录（放开仅 _index.md，不递归其子目录）——展开为逐条文件 spec，跳过基线已有的 _index.md
	if level == config.MountLevelMaster && !off["skill"] {
		if dir := d.resolveSpec(skillPackageSpec); dir != "" && dirExists(dir) {
			for _, spec := range expandSpecFilesFlat(skillPackageSpec, dir, nil) {
				if spec == skillPackageBaselineSpec {
					continue
				}
				list = append(list, spec)
			}
		}
	}
	return list
}

// mountOffFor 模板勾选关掉的挂载段——`@mount:<段>[@<项目名>]` 落在 defaultInjectDisabled 里即关闭该段：
// 不带项目名对所有项目生效，带项目名只对该项目生效（项目名不区分大小写）。
func (d *Deps) mountOffFor(project string) map[string]bool {
	off := map[string]bool{}
	tpl, err := config.LoadCatDefault(d.catDefaultPath())
	if err != nil || tpl == nil {
		return off
	}
	for _, raw := range tpl.DefaultInjectDisabled {
		seg, pid := parseMountSwitch(raw)
		if seg == "" {
			continue
		}
		if pid == "" || strings.EqualFold(pid, strings.TrimSpace(project)) {
			off[seg] = true
		}
	}
	return off
}

// parseMountSwitch 解析 `@mount:<段>[@<项目名>]`——非开关条目返回空段。
func parseMountSwitch(raw string) (seg, project string) {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(s), config.MountSwitchPrefix) {
		return "", ""
	}
	rest := strings.TrimSpace(s[len(config.MountSwitchPrefix):])
	if rest == "" {
		return "", ""
	}
	if i := strings.Index(rest, "@"); i >= 0 {
		return strings.ToLower(strings.TrimSpace(rest[:i])), strings.TrimSpace(rest[i+1:])
	}
	return strings.ToLower(rest), ""
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

// MountModeText 挂载模式文本——未挂载为空（适配层整组不发），挂载态 normal / expert / master。
//
// 三档自报头值：普通=normal / 专家=expert / 精通=master（适配层按此整组发或裁剪）。
func MountModeText(project string, level int) string {
	if project == "" {
		return ""
	}
	switch level {
	case config.MountLevelNormal:
		return "normal"
	case config.MountLevelMaster:
		return "master"
	default:
		return "expert"
	}
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
