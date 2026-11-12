package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// NoteMaxLength 是认路注释的最大长度——超长截断（前端限 20 字，宿主侧兜底）。
const NoteMaxLength = 20

// RootEntry 是一个受控根条目（id + 路径 + 可写标志 + 认路注释）。
type RootEntry struct {
	// ID 根标识——命名空间寻址前缀（ccbp:L1/Tree.md 的 ccbp）
	ID string
	// Path 规范绝对路径
	Path string
	// Writable 是否允许写入
	Writable bool
	// Note 认路注释（≤20 字）
	Note string
}

// InjectEntry 是一条注入清单条目。
type InjectEntry struct {
	// File 文件寻址——id:relative（映射受控根）或纯路径
	File string
	// Optional 缺失是否可跳过（true 缺失仅警告）
	Optional bool
	// Label 来源标注
	Label string
}

// WorkspaceConfig 是 workspace.json 的解析结果。
type WorkspaceConfig struct {
	// Roots 受控根列表（有序；首根为相对路径解析基准）
	Roots []RootEntry
	// Inject 注入清单（有序）
	Inject []InjectEntry
	// LoadWarnings 加载告警——不可达的非常驻根（跳过不注册）
	LoadWarnings []string
	// LoadDegrades 加载降级——已在加载期自动处理的配置 / 环境问题（code|state|detail）
	LoadDegrades []string
}

// workspaceFile 是 workspace.json 的磁盘形态。
type workspaceFile struct {
	Roots []struct {
		ID       string `json:"id"`
		Path     string `json:"path"`
		Writable *bool  `json:"writable"`
		Note     string `json:"note"`
	} `json:"roots"`
	Inject []struct {
		File     string `json:"file"`
		Optional bool   `json:"optional"`
		Label    string `json:"label"`
	} `json:"inject"`
}

// LoadWorkspace 加载 workspace.json；永不返回错误——配置异常一律降级为默认单根。
//
// 语义与 C# WorkspaceConfig.Load 一致：
//   - 文件缺失 / roots 空 → 默认单根（workspace = defaultRoot，可写）
//   - 读取失败 → 单根 + CFG_UNREADABLE
//   - JSON 损坏 → 备份坏档 + 单根 + CFG_CORRUPT
//   - 常驻根（workspace / runtime）不可达 → 重建目录；失败回落 defaultRoot
//   - 非常驻根不可达 → 跳过 + LoadWarnings
func LoadWorkspace(path, defaultRoot string) *WorkspaceConfig {
	cfg := &WorkspaceConfig{}
	if path == "" {
		cfg.Roots = singleRoot(defaultRoot)
		return cfg
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		cfg.Roots = singleRoot(defaultRoot)
		if os.IsNotExist(err) {
			return cfg
		}
		cfg.LoadDegrades = append(cfg.LoadDegrades, "CFG_UNREADABLE|fallback|"+path+" | "+err.Error())
		return cfg
	}
	var file workspaceFile
	if err := json.Unmarshal(StripBOM(raw), &file); err != nil {
		backup := backupCorrupt(path)
		note := ""
		if backup != "" {
			note = "（坏档备份 " + backup + "）"
		}
		cfg.Roots = singleRoot(defaultRoot)
		cfg.LoadDegrades = append(cfg.LoadDegrades, "CFG_CORRUPT|fallback|"+path+note+" | "+err.Error())
		return cfg
	}
	for i, item := range file.Roots {
		if item.ID == "" || item.Path == "" {
			cfg.LoadDegrades = append(cfg.LoadDegrades, "ROOT_ITEM_INVALID|empty|roots["+itoa(i)+"] 缺 id/path")
			continue
		}
		norm, err := NormalizeRoot(item.Path)
		if err != nil {
			cfg.LoadDegrades = append(cfg.LoadDegrades, "ROOT_ITEM_INVALID|empty|roots["+itoa(i)+"] 路径非法："+item.Path+" | "+err.Error())
			continue
		}
		writable := true
		if item.Writable != nil {
			writable = *item.Writable
		}
		if !dirExists(norm) {
			resident := strings.EqualFold(item.ID, "workspace") || strings.EqualFold(item.ID, "runtime")
			if !resident {
				cfg.LoadWarnings = append(cfg.LoadWarnings, "根不可达已跳过: "+item.ID+" → "+norm)
				continue
			}
			if err := os.MkdirAll(norm, 0o755); err == nil {
				cfg.LoadDegrades = append(cfg.LoadDegrades, "ROOT_REBUILT|rebuilt|常驻根不存在已重建："+item.ID+" → "+norm)
			} else if fallback, ferr := NormalizeRoot(defaultRoot); ferr == nil {
				cfg.LoadDegrades = append(cfg.LoadDegrades, "ROOT_UNREACHABLE|fallback|常驻根不可达（"+item.ID+" → "+norm+"），已回落 "+fallback)
				norm = fallback
			} else {
				cfg.LoadDegrades = append(cfg.LoadDegrades, "ROOT_UNREACHABLE|unavailable|常驻根不可达且无可写回落："+item.ID+" → "+norm)
			}
		}
		cfg.Roots = append(cfg.Roots, RootEntry{
			ID:       item.ID,
			Path:     norm,
			Writable: writable,
			Note:     TruncateNote(item.Note),
		})
	}
	if len(cfg.Roots) == 0 {
		cfg.Roots = singleRoot(defaultRoot)
		return cfg
	}
	for _, item := range file.Inject {
		if item.File == "" {
			continue
		}
		cfg.Inject = append(cfg.Inject, InjectEntry{File: item.File, Optional: item.Optional, Label: item.Label})
	}
	return cfg
}

// singleRoot 构造默认单根（workspace = defaultRoot，可写）。
func singleRoot(defaultRoot string) []RootEntry {
	norm := defaultRoot
	if n, err := NormalizeRoot(defaultRoot); err == nil {
		norm = n
	}
	return []RootEntry{{ID: "workspace", Path: norm, Writable: true}}
}

// NormalizeRoot 规范化根路径——绝对化 + 去尾分隔符（保盘根）。
func NormalizeRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errRootEmpty
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(abs)
	if clean == filepath.VolumeName(clean)+string(os.PathSeparator) {
		return clean, nil
	}
	return strings.TrimRight(clean, "/\\"), nil
}

// TruncateNote 去空白 + 超长截断到 NoteMaxLength。
func TruncateNote(note string) string {
	trimmed := strings.TrimSpace(note)
	if len([]rune(trimmed)) <= NoteMaxLength {
		return trimmed
	}
	return string([]rune(trimmed)[:NoteMaxLength])
}

// backupCorrupt 备份损坏的配置文件；失败返回空串（不阻断）。
func backupCorrupt(path string) string {
	backup := path + ".corrupt-" + time.Now().Format("20060102-150405")
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if err := os.WriteFile(backup, raw, 0o644); err != nil {
		return ""
	}
	return backup
}

// dirExists 判定目录是否存在。
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// itoa 小整数转字符串（避免为降级文案引入 strconv 依赖面）。
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}

// errRootEmpty 是空根路径错误。
var errRootEmpty = errString("Workspace root is empty.")

// errString 是最简错误类型。
type errString string

// Error 实现 error。
func (e errString) Error() string { return string(e) }

// workspaceWriteRoot 是 roots 写面条目的磁盘形态（字段序对齐 C# WriteWorkspaceRoots：id / path / writable / note）。
type workspaceWriteRoot struct {
	// ID 根标识
	ID string `json:"id"`
	// Path 根路径（落盘统一正斜杠）
	Path string `json:"path"`
	// Writable 可写标志
	Writable bool `json:"writable"`
	// Note 认路注释（空则不落字段，同 C#）
	Note string `json:"note,omitempty"`
}

// LoadWorkspaceRootsForEdit 读受控根编辑面——按 workspace.json 磁盘原样返回全部根条目。
//
// 与 LoadWorkspace 的差异：不跳过不可达的非常驻根。启动快照跳过它们（工具面不注册），
// 编辑面若也跳过，前端保存就会把坏根永久删掉——而修正坏路径正是编辑面的用途。
// 第二返回值 false = 文件缺失 / 不可解析——调用方回落启动快照。
func LoadWorkspaceRootsForEdit(path string) ([]RootEntry, bool) {
	if path == "" {
		return nil, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var file workspaceFile
	if err := json.Unmarshal(StripBOM(raw), &file); err != nil {
		return nil, false
	}
	roots := make([]RootEntry, 0, len(file.Roots))
	for _, item := range file.Roots {
		if item.ID == "" || item.Path == "" {
			continue
		}
		norm := item.Path
		if normalized, err := NormalizeRoot(item.Path); err == nil {
			norm = normalized
		}
		writable := true
		if item.Writable != nil {
			writable = *item.Writable
		}
		roots = append(roots, RootEntry{ID: item.ID, Path: norm, Writable: writable, Note: TruncateNote(item.Note)})
	}
	return roots, true
}

// SaveWorkspaceRoots 写回 workspace.json 的 roots 段（保留 inject 段；原子写）。
//
// 语义对齐 C# WriteWorkspaceRoots：
//   - 旧档 inject 段原样保留；旧档存在但读不出 inject → 拒绝覆盖（宁可失败，不静默丢注入清单）
//   - path 统一正斜杠落盘（C# / Go 两侧读面同值，数据面可互换）
func SaveWorkspaceRoots(path string, roots []RootEntry) error {
	injectRaw := "[]"
	if raw, err := os.ReadFile(path); err == nil {
		var old struct {
			Inject json.RawMessage `json:"inject"`
		}
		if err := json.Unmarshal(StripBOM(raw), &old); err != nil {
			return errString("注入清单读取失败（拒绝覆盖，避免丢 inject）: " + err.Error())
		}
		if len(old.Inject) > 0 && string(old.Inject) != "null" {
			injectRaw = string(old.Inject)
		}
	} else if !os.IsNotExist(err) {
		return errString("注入清单读取失败（拒绝覆盖，避免丢 inject）: " + err.Error())
	}
	var buf strings.Builder
	buf.WriteString(`{"roots":[`)
	for i, root := range roots {
		if i > 0 {
			buf.WriteString(",")
		}
		item, err := marshalCompact(workspaceWriteRoot{
			ID:       root.ID,
			Path:     strings.ReplaceAll(root.Path, `\`, "/"),
			Writable: root.Writable,
			Note:     root.Note,
		})
		if err != nil {
			return errString("落盘失败: " + err.Error())
		}
		buf.Write(item)
	}
	buf.WriteString(`],"inject":`)
	buf.WriteString(injectRaw)
	buf.WriteString(`}`)
	if err := writeAtomic(path, []byte(buf.String())); err != nil {
		return errString("落盘失败: " + err.Error())
	}
	return nil
}
