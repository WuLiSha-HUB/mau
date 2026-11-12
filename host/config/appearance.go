package config

import (
	"bytes"
	"encoding/json"
	"os"
	"sort"
	"strings"
)

// AppearanceConfig 是一份外观配置（Data/config/appearance.json 条目）。
//
// 一条目 = 一套外观预设：桌宠种类 / TTS 风格 / 轮末总结 / 总结成音频四字段同属。
type AppearanceConfig struct {
	// SchemaVersion 结构版本（恒 1）
	SchemaVersion int `json:"schemaVersion"`
	// AppearanceID 外观配置身份（GUID 文本）
	AppearanceID string `json:"appearanceId"`
	// DisplayName 用户可见名称
	DisplayName string `json:"displayName"`
	// PetKind 桌宠种类——素材名；空 = 无桌宠
	PetKind string `json:"petKind"`
	// TtsStyle TTS 风格槽位；空 = 无
	TtsStyle string `json:"ttsStyle"`
	// RoundSummary 轮末总结开关
	RoundSummary bool `json:"roundSummary"`
	// SummaryAudio 总结成音频开关
	SummaryAudio bool `json:"summaryAudio"`
	// IsDefault 默认外观标记
	IsDefault bool `json:"isDefault"`
}

// AppearanceOption 是外观下拉项。
type AppearanceOption struct {
	// AppearanceID 外观配置身份
	AppearanceID string `json:"appearanceId"`
	// DisplayName 显示名
	DisplayName string `json:"displayName"`
	// IsDefault 默认标记
	IsDefault bool `json:"isDefault"`
}

// LoadAppearancePool 读取外观池——文件缺失 / 损坏 = 空池（不阻断宿主）。
//
// 条目按 C# `Guid.CompareTo` 语义排序（与 CH_AppearanceConfigStore.GetAll 同序——
// 两侧 GET /api/v1/appearance-apis 的 items 顺序逐条一致）。
func LoadAppearancePool(path string) []AppearanceConfig {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var list []AppearanceConfig
	if err := json.Unmarshal(StripBOM(raw), &list); err != nil {
		return nil
	}
	for i := range list {
		list[i].AppearanceID = ParseGUID(list[i].AppearanceID)
	}
	SortAppearancePool(list)
	return list
}

// BuildAppearanceOptions 池 → 下拉项（池序）。
func BuildAppearanceOptions(pool []AppearanceConfig) []AppearanceOption {
	out := make([]AppearanceOption, 0, len(pool))
	for _, cfg := range pool {
		out = append(out, AppearanceOption{AppearanceID: cfg.AppearanceID, DisplayName: cfg.DisplayName, IsDefault: cfg.IsDefault})
	}
	return out
}

// resolveEffectiveAppearance 解析猫的生效外观条目——appearanceId 零值跟随默认外观（池内 isDefault 条目）；
// 显式绑定而条目已删 / 池空 = 未见（调用方按保守值处置，与 C# ResolveEffectiveAppearance 同口径）。
func resolveEffectiveAppearance(pool []AppearanceConfig, appearanceID string) (AppearanceConfig, bool) {
	target := ParseGUID(appearanceID)
	if target == "" || IsZeroGUID(target) {
		for _, cfg := range pool {
			if cfg.IsDefault {
				return cfg, true
			}
		}
		return AppearanceConfig{}, false
	}
	for _, cfg := range pool {
		if cfg.AppearanceID == target {
			return cfg, true
		}
	}
	return AppearanceConfig{}, false
}

// ResolveRoundSummary 解析猫的轮末总结生效值——无生效外观 = false（保守值）。
func ResolveRoundSummary(pool []AppearanceConfig, appearanceID string) bool {
	cfg, ok := resolveEffectiveAppearance(pool, appearanceID)
	if !ok {
		return false
	}
	return cfg.RoundSummary
}

// ResolveSummaryAudio 解析猫的总结自动出声配置——返回 [启用, 音色槽位]；
// 无生效外观 = 保守关闭（不出声）；音色空 = 服务默认音色（与 C# AdminService.ResolveSummaryAudio 同口径）。
func ResolveSummaryAudio(pool []AppearanceConfig, appearanceID string) (bool, string) {
	cfg, ok := resolveEffectiveAppearance(pool, appearanceID)
	if !ok {
		return false, ""
	}
	return cfg.SummaryAudio, cfg.TtsStyle
}

// SortAppearancePool 按 C# Guid.CompareTo 语义排序（a → b → c → 尾 8 字节；前三段按有符号整数比较）。
func SortAppearancePool(pool []AppearanceConfig) {
	sort.SliceStable(pool, func(i, j int) bool {
		return compareGUIDText(pool[i].AppearanceID, pool[j].AppearanceID) < 0
	})
}

// compareGUIDText 复现 Guid.CompareTo——非法身份按文本序兜底。
func compareGUIDText(left, right string) int {
	a := compactGUID(left)
	b := compactGUID(right)
	if len(a) != 32 || len(b) != 32 {
		return strings.Compare(left, right)
	}
	if cmp := compareGUIDSegment(a[0:8], b[0:8], 32); cmp != 0 {
		return cmp
	}
	if cmp := compareGUIDSegment(a[8:12], b[8:12], 16); cmp != 0 {
		return cmp
	}
	if cmp := compareGUIDSegment(a[12:16], b[12:16], 16); cmp != 0 {
		return cmp
	}
	return strings.Compare(a[16:], b[16:])
}

// compareGUIDSegment 段比较——按位数解释为有符号整数（对齐 Guid 的 int32 / int16 字段语义）。
func compareGUIDSegment(left, right string, bits int) int {
	lv := parseGUIDSegment(left, bits)
	rv := parseGUIDSegment(right, bits)
	switch {
	case lv < rv:
		return -1
	case lv > rv:
		return 1
	}
	return 0
}

// parseGUIDSegment 十六进制段 → 有符号整数。
func parseGUIDSegment(text string, bits int) int64 {
	var value int64
	for i := 0; i < len(text); i++ {
		value = value<<4 | int64(hexValue(text[i]))
	}
	if bits == 32 && value >= 0x80000000 {
		value -= 0x100000000
	}
	if bits == 16 && value >= 0x8000 {
		value -= 0x10000
	}
	return value
}

// hexValue 十六进制字符取值（非法字符按 0——身份已归一，非十六进制不会到达这里）。
func hexValue(ch byte) int {
	switch {
	case ch >= '0' && ch <= '9':
		return int(ch - '0')
	case ch >= 'a' && ch <= 'f':
		return int(ch-'a') + 10
	case ch >= 'A' && ch <= 'F':
		return int(ch-'A') + 10
	}
	return 0
}

// SaveAppearancePool 原子写外观池（缩进 JSON + 中文直出，对齐 C# WriteIndented + UnsafeRelaxedJsonEscaping）。
func SaveAppearancePool(path string, pool []AppearanceConfig) error {
	if pool == nil {
		pool = []AppearanceConfig{}
	}
	body, err := encodeAppearancePool(pool)
	if err != nil {
		return err
	}
	return writeAtomic(path, body)
}

// encodeAppearancePool 序列化池数组（两空格缩进 / 无尾换行）。
func encodeAppearancePool(pool []AppearanceConfig) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(pool); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
