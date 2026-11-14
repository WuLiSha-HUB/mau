package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// CatCfg 是每猫配置（sessions/<id>/cat.cfg）。
//
// 字段序对齐 C# CatCfgData 的落盘序（磁盘形态为单行紧凑 JSON）；running / port 属运行态
// （读容忍、写面排除——omitempty 保证零值不落盘，与 C# 一致）。
type CatCfg struct {
	// ID 会话 ID（目录名）
	ID string `json:"id"`
	// DisplayName 显示名
	DisplayName string `json:"displayName"`
	// Type 会话 Type 标签（空 = 默认组）
	Type string `json:"type"`
	// APIConfigID LLM API 配置身份（空 = 内置默认）
	APIConfigID string `json:"apiConfigId"`
	// Persona 角色段（system prompt 尾追加）
	Persona string `json:"persona"`
	// ToolNames 工具名单（逗号清单；空 = 全量保底）
	ToolNames string `json:"toolNames"`
	// InjectList 前文注入清单（id:相对路径）
	InjectList []string `json:"injectList"`
	// ProjectMount 当前挂载项目名（空 = 未挂载）
	ProjectMount string `json:"projectMount"`
	// ProjectMountExpert 专家模式（缺省 true）
	ProjectMountExpert bool `json:"projectMountExpert"`
	// ProjectMountAt 挂载时刻（Unix 毫秒，0 = 不计时）
	ProjectMountAt int64 `json:"projectMountAt"`
	// MountAuto 自动识别开关（缺省 true）
	MountAuto bool `json:"mountAuto"`
	// EnabledRoots 启用根 id 清单（空 = 全量；workspace 强制）
	EnabledRoots []string `json:"enabledRoots"`
	// Packs 加载包挂载清单
	Packs []string `json:"packs"`
	// DBLibs 库白名单
	DBLibs []string `json:"dbLibs"`
	// UserInjectIdleOnly 人工输入仅 Idle 注入（缺省 false）
	UserInjectIdleOnly bool `json:"userInjectIdleOnly"`
	// AppearanceID 外观配置身份（空 = 默认外观）
	AppearanceID string `json:"appearanceId"`
	// ReasoningEffort 思考强度（空 = 跟随全局）
	ReasoningEffort string `json:"reasoningEffort"`
	// ThemeColor 主题色索引（0 = 默认白）
	ThemeColor int `json:"themeColor"`
	// FollowDefaultTools 跟随新猫默认模板的工具清单（true=模板改动广播到本猫）
	FollowDefaultTools bool `json:"followDefaultTools"`
	// FollowDefaultInject 跟随新猫默认模板的前文注入（true=模板改动广播到本猫）
	FollowDefaultInject bool `json:"followDefaultInject"`
	// FollowDefaultRoots 跟随新猫默认模板的受控根（true=模板改动广播到本猫）
	FollowDefaultRoots bool `json:"followDefaultRoots"`
	// Running 运行态（读容忍，写面排除）
	Running bool `json:"running,omitempty"`
	// Port 监听端口（读容忍，写面排除；0 = 静默态）
	Port int `json:"port,omitempty"`
}

// LoadCatCfg 读取每猫配置；文件缺失或 JSON 损坏返回错误。
//
// 缺省值预置与 C# CatCfgData 字段初始化器同口径：mountAuto / projectMountExpert 缺省 true
// （文件未写该键 = 取缺省；显式 false 照实覆盖——json.Unmarshal 只覆盖出现过的键）。
func LoadCatCfg(path string) (*CatCfg, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := CatCfg{MountAuto: true, ProjectMountExpert: true}
	if err := json.Unmarshal(StripBOM(raw), &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// SaveCatCfg 原子写每猫配置——紧凑单行 JSON（中文直出，与 C# JsonUtil.Serialize 同字节形态）。
func SaveCatCfg(path string, cfg *CatCfg) error {
	body, err := marshalCompact(cfg)
	if err != nil {
		return err
	}
	return writeAtomic(path, body)
}

// FallbackBaseRole 内置基础角色段——模板缺失时回退（行为不倒退；逐字对齐 C# CatHostService.FallbackBaseRole）。
const FallbackBaseRole = "你是 MajorDomoCat——CH4 自举宿主的管理员对话中枢（P8.5 会话配置化）。"

// CatDefault 是新猫默认模板（config/cat-default.cfg）。
type CatDefault struct {
	// BaseRole 基础角色段
	BaseRole string `json:"baseRole"`
	// DefaultPersona 默认角色设定
	DefaultPersona string `json:"defaultPersona"`
	// DefaultToolNames 默认工具名单
	DefaultToolNames string `json:"defaultToolNames"`
	// DefaultInjectList 默认注入清单
	DefaultInjectList []string `json:"defaultInjectList"`
	// DefaultPacks 默认加载包
	DefaultPacks []string `json:"defaultPacks"`
	// DefaultEnabledRoots 默认根白名单（null = 未配置，与空数组区分）
	DefaultEnabledRoots []string `json:"defaultEnabledRoots"`
	// DefaultUserInjectIdleOnly 默认人工输入仅 Idle 注入
	DefaultUserInjectIdleOnly bool `json:"defaultUserInjectIdleOnly"`
	// DefaultInjectDisabled 默认注入禁用清单（条目留在池里可再勾回，但不注入）
	DefaultInjectDisabled []string `json:"defaultInjectDisabled"`
}

// MountSwitchPrefix 模板前文清单里的「项目挂载段开关」前缀。
//
// 形态 `@mount:<段>[@<项目名>]`，段 = job / l3 / design-status / glossary / project / todo。
// 只作勾选位——不进新猫继承集（见 ResolveTplEnabledInject）；挂载时按勾选状态决定该段挂不挂
// （不带项目名 = 对所有项目生效，带 @项目名 = 只对该项目生效）。
const MountSwitchPrefix = "@mount:"

// ResolveTplEnabledInject 默认前文「启用集」——DefaultInjectList 排除 DefaultInjectDisabled，
// 并剔除挂载段开关位（MountSwitchPrefix）。
//
// 新猫继承、跟随广播、点亮即同步三处共用本出口（单点真相源，对齐 C# ResolveTplEnabledInject）。
func ResolveTplEnabledInject(tpl *CatDefault) []string {
	if tpl == nil || len(tpl.DefaultInjectList) == 0 {
		return []string{}
	}
	disabled := make(map[string]bool, len(tpl.DefaultInjectDisabled))
	for _, path := range tpl.DefaultInjectDisabled {
		if path != "" {
			disabled[path] = true
		}
	}
	enabled := make([]string, 0, len(tpl.DefaultInjectList))
	for _, path := range tpl.DefaultInjectList {
		if path == "" || disabled[path] {
			continue
		}
		// 挂载段开关位只服务项目挂载过滤，不是基线前文——继承时剔除
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(path)), MountSwitchPrefix) {
			continue
		}
		enabled = append(enabled, path)
	}
	return enabled
}

// LoadCatDefault 读取新猫默认模板；文件缺失或 JSON 损坏返回错误。
func LoadCatDefault(path string) (*CatDefault, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg CatDefault
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// SaveCatDefault 原子写新猫默认模板——紧凑单行 JSON。
func SaveCatDefault(path string, cfg *CatDefault) error {
	body, err := marshalCompact(cfg)
	if err != nil {
		return err
	}
	return writeAtomic(path, body)
}

// IsSafeCatKey 判定 catKey 可安全拼入 sessions 目录——单层目录名，不承载任何路径语义。
func IsSafeCatKey(key string) bool {
	if key == "" || len(key) > 128 || key == "." || key == ".." {
		return false
	}
	for i := 0; i < len(key); i++ {
		ch := key[i]
		if ch < ' ' {
			return false
		}
		switch ch {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return false
		}
	}
	return true
}

// IsValidInjectPath 注入路径合法性——只接受完整路径（绝对路径 / id: 命名空间）。
//
// 相对路径、空 id、非法字符一律拒绝（不录入；调用方出声）。
func IsValidInjectPath(path string) bool {
	if path == "" {
		return false
	}
	if isRootedPath(path) {
		return true
	}
	colon := strings.Index(path, ":")
	if colon <= 0 || colon >= len(path)-1 {
		return false
	}
	for i := 0; i < colon; i++ {
		ch := path[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' {
			continue
		}
		return false
	}
	return true
}

// isRootedPath 绝对路径判定（盘符 / UNC / 根斜杠——对齐 Windows Path.IsPathRooted）。
func isRootedPath(path string) bool {
	if len(path) >= 3 {
		if isASCIIAlpha(path[0]) && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
			return true
		}
	}
	if path[0] == '\\' || path[0] == '/' {
		return true
	}
	return false
}

// isASCIIAlpha ASCII 字母判定。
func isASCIIAlpha(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

// NormalizeReasoningEffort 思考强度归一——low / high / max 之外一律空（跟随全局）。
func NormalizeReasoningEffort(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "low", "high", "max":
		return normalized
	}
	return ""
}

// marshalCompact 序列化为紧凑 JSON（中文直出——关闭 HTML 转义，无尾换行）。
func marshalCompact(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// writeAtomic 原子写文件（同目录临时文件 + 替换）。
func writeAtomic(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
