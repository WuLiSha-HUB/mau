package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"cathome5/host/config"
)

// 本文件复现管理端段 5「受控根编辑面」两件——GET / POST /api/v1/workspace。
//
// 契约对齐：
//
//	读面   `mau:CatHome4/CatHome4.Admin/AdminService.Handlers.cs:1046`（HandleWorkspaceGet）
//	写面   `mau:CatHome4/CatHome4.Admin/AdminService.Handlers.cs:1288`（HandleWorkspacePost）
//	校验   `mau:CatHome4/CatHome4.Admin/AdminService.Roots.cs` §段3（ValidateRootsInput / WriteWorkspaceRoots）
//
// 生效语义：落盘 + **重启生效**（roots 是启动一次性读取，运行期解析面继续用启动快照）。
// 读面例外：每次请求按 workspace.json 重读——保存后读面立即跟随；文件缺失 / 损坏时回落启动快照。
// （C# 读内存实例，重启前不回显；本项目按判据「保存后读面跟随」改为磁盘优先。）

// workspaceGetResponse 是 GET /api/v1/workspace 的响应体（字段序对齐 C# new { ok, roots }）。
type workspaceGetResponse struct {
	// OK 恒 true
	OK bool `json:"ok"`
	// Roots 根条目（固定根三列以固定表为准）
	Roots []rootItem `json:"roots"`
}

// workspacePostResponse 是写面回执（字段序对齐 C# new { ok, roots = N }）。
type workspacePostResponse struct {
	// OK 恒 true
	OK bool `json:"ok"`
	// Roots 本次写入的根条目数
	Roots int `json:"roots"`
}

// workspaceRootInput 是写面提交条目（body roots[] 元素；ID 已归一为小写）。
type workspaceRootInput struct {
	// ID 根标识（小写）
	ID string
	// Path 根路径（原值；路径锁定根会被系统值覆盖）
	Path string
	// Writable 可写标志（固定根取系统定义）
	Writable bool
	// Note 认路注释（固定根取系统定义）
	Note string
}

// workspacePath 受控根配置路径。
func (d *Deps) workspacePath() string {
	return filepath.Join(d.DataRoot, "config", "workspace.json")
}

// workspaceRootsForEdit 编辑面根表——磁盘优先（保存后立即跟随），缺失 / 空 / 损坏回落启动快照。
func (d *Deps) workspaceRootsForEdit() []config.RootEntry {
	if roots, ok := config.LoadWorkspaceRootsForEdit(d.workspacePath()); ok && len(roots) > 0 {
		return roots
	}
	if d.Workspace == nil {
		return []config.RootEntry{}
	}
	return d.Workspace.Roots
}

// handleWorkspaceGet 服务 GET /api/v1/workspace——受控根编辑面数据源。
func (d *Deps) handleWorkspaceGet(w http.ResponseWriter, r *http.Request) {
	if d.Workspace == nil {
		writeErr(w, "工作区配置未绑定")
		return
	}
	entries := d.workspaceRootsForEdit()
	items := make([]rootItem, 0, len(entries))
	for _, root := range entries {
		item := rootItem{ID: root.ID, Path: root.Path, Writable: root.Writable, Note: root.Note}
		if def, ok := findFixedRoot(root.ID); ok {
			item.Writable = def.Writable
			item.Note = def.Note
			item.FixedRoot = true
		}
		items = append(items, item)
	}
	writeJSON(w, workspaceGetResponse{OK: true, Roots: items})
}

// handleWorkspacePost 服务 POST /api/v1/workspace——受控根落盘（body: {roots:[{id,path,writable,note}]}）。
//
// 固定命名根 id 是白名单：不可新增 / 改名 / 删减，note 与 writable 由系统定义覆盖，
// 只可改 path（data 根 path 由数据根锁定）。校验失败一律 200 + ok:false（前端按 ok 判）。
func (d *Deps) handleWorkspacePost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeErr(w, "body 非 JSON")
		return
	}
	var payload struct {
		Roots []struct {
			ID       string `json:"id"`
			Path     string `json:"path"`
			Writable *bool  `json:"writable"`
			Note     string `json:"note"`
		} `json:"roots"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeErr(w, "body 非 JSON")
		return
	}
	inputs := make([]workspaceRootInput, 0, len(payload.Roots))
	for _, item := range payload.Roots {
		input := workspaceRootInput{
			ID:       normalizeRootID(item.ID),
			Path:     strings.TrimSpace(item.Path),
			Writable: true,
			Note:     config.TruncateNote(item.Note),
		}
		if item.Writable != nil {
			input.Writable = *item.Writable
		}
		inputs = append(inputs, input)
	}
	if message := d.validateRootsInput(inputs, d.loadExistingRootPaths()); message != "" {
		writeErr(w, message)
		return
	}
	entries := make([]config.RootEntry, 0, len(inputs))
	for _, input := range inputs {
		entries = append(entries, config.RootEntry{ID: input.ID, Path: input.Path, Writable: input.Writable, Note: input.Note})
	}
	if err := config.SaveWorkspaceRoots(d.workspacePath(), entries); err != nil {
		d.log(3, "受控根写入失败："+err.Error())
		writeErr(w, err.Error())
		return
	}
	d.log(1, "工作区已保存："+itoa(len(entries))+" 个根（重启生效）")
	writeJSON(w, workspacePostResponse{OK: true, Roots: len(entries)})
}

// validateRootsInput 受控根提交校验——返回空串 = 通过（口径同 C# ValidateRootsInput）。
//
// 顺序即语义：id 非空 / 字符集 / PS 驱动器保留名 / 重名 → path 非空 / 绝对 / 存在或可创建
// → 固定根三列归一与路径锁定 → 固定根齐备。
func (d *Deps) validateRootsInput(inputs []workspaceRootInput, existing map[string]string) string {
	if len(inputs) == 0 {
		return "roots 为空（固定命名根不可移除）"
	}
	ids := make([]string, 0, len(inputs))
	for i := range inputs {
		input := &inputs[i]
		if input.ID == "" {
			return "roots[" + itoa(i) + "] id 为空"
		}
		if !isValidRootIDChars(input.ID) {
			return "roots[" + itoa(i) + "] id 非法（仅小写字母/数字）: " + input.ID
		}
		if isReservedPsDriveName(input.ID) {
			return "roots[" + itoa(i) + "] id 与 PowerShell 内置驱动器同名（ps 根寻址会失效）: " + input.ID
		}
		for _, seen := range ids {
			if strings.EqualFold(seen, input.ID) {
				return "roots id 重复（大小写不敏感）: " + input.ID
			}
		}
		ids = append(ids, input.ID)
		if input.Path == "" {
			return "roots[" + itoa(i) + "] path 为空"
		}
		if !filepath.IsAbs(input.Path) {
			return "roots[" + itoa(i) + "] path 非绝对路径: " + input.Path
		}
		if !dirExists(input.Path) {
			// 存量容留——与现存同 id 根 path 等值（规范化后）时不要求可达：
			// 目录离线 / 搬迁后仍可原样提交以移除或改路径，不构成改根死锁。
			retained := false
			if old, ok := existing[input.ID]; ok && old != "" {
				if normalizePathForCompare(input.Path) == normalizePathForCompare(old) {
					retained = true
				}
			}
			if !retained {
				// A306 保存面放宽——目录不存在时按需创建（填一个还没建的目录不该被挡在门外）
				if err := os.MkdirAll(input.Path, 0o755); err != nil {
					return "roots[" + itoa(i) + "] 目录不存在且无法创建: " + input.Path + "（" + err.Error() + "）"
				}
			}
		}
		def, ok := findFixedRoot(input.ID)
		if !ok {
			continue
		}
		// 固定列系统权威——note / writable 取定义值（提交值不参与判定，不构成保存死锁）
		input.Note = def.Note
		input.Writable = def.Writable
		// A273 / A321——仅 data 的 path 由系统计算；db 起 path 可配置
		if locked := d.resolveFixedRootPath(def.ID); locked != "" {
			if normalizePathForCompare(input.Path) != normalizePathForCompare(locked) {
				return "系统根 " + def.ID + " 的路径固定为 " + normalizePathForCompare(locked) + "，不可修改"
			}
			input.Path = locked
		}
	}
	for _, def := range fixedRoots {
		found := false
		for _, id := range ids {
			if strings.EqualFold(id, def.ID) {
				found = true
				break
			}
		}
		if !found {
			return "缺少系统根 " + def.ID + "（固定命名根不可移除）"
		}
	}
	return ""
}

// resolveFixedRootPath 路径锁定根的系统值——仅 data = 数据根；其余根（含 db）返回空串。
func (d *Deps) resolveFixedRootPath(id string) string {
	if strings.TrimSpace(d.DataRoot) == "" {
		return ""
	}
	if normalizeRootID(id) == "data" {
		return d.DataRoot
	}
	return ""
}

// loadExistingRootPaths 现存根 path 映射（id 小写 → path 原值）——存量容留判定用。
// 文件缺失 / 损坏返回空映射（按无容留处理，出声）。
func (d *Deps) loadExistingRootPaths() map[string]string {
	out := make(map[string]string, 8)
	raw, err := os.ReadFile(d.workspacePath())
	if err != nil {
		if !os.IsNotExist(err) {
			d.log(2, "现存根表读取失败（按无存量容留处理）: "+err.Error())
		}
		return out
	}
	var file struct {
		Roots []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"roots"`
	}
	if err := json.Unmarshal(config.StripBOM(raw), &file); err != nil {
		d.log(2, "现存根表读取失败（按无存量容留处理）: "+err.Error())
		return out
	}
	for _, item := range file.Roots {
		id := normalizeRootID(item.ID)
		if id == "" || item.Path == "" {
			continue
		}
		out[id] = item.Path
	}
	return out
}

// normalizeRootID 根 id 归一——去空白 + 大写转小写（写面统一小写；读面按小写识别）。
func normalizeRootID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

// isValidRootIDChars 根 id 字符集校验——仅小写字母 + 数字（下划线退役）。
func isValidRootIDChars(id string) bool {
	if id == "" {
		return false
	}
	for _, ch := range id {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			continue
		}
		return false
	}
	return true
}

// reservedPsDriveNames PS 内置驱动器保留名（Get-PSDrive 实测并集 5.1 + 7）。
var reservedPsDriveNames = []string{
	"alias", "cert", "env", "function", "hkcu", "hklm", "variable", "wsman", "temp",
}

// isReservedPsDriveName PS 保留驱动器名判定——固定名或单字母盘符（a-z）。
func isReservedPsDriveName(id string) bool {
	norm := normalizeRootID(id)
	if len(norm) == 1 && norm[0] >= 'a' && norm[0] <= 'z' {
		return true
	}
	for _, name := range reservedPsDriveNames {
		if name == norm {
			return true
		}
	}
	return false
}

// normalizePathForCompare 路径比较用规范形（绝对化 + 去尾分隔符；失败回落原值）。
func normalizePathForCompare(path string) string {
	if normalized, err := config.NormalizeRoot(path); err == nil {
		return normalized
	}
	return path
}

// dirExists 判定目录是否存在。
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
