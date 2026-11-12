package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LlmAPIConfig 是一条 LLM 端点配置（Data/config/llm-api.json 条目）。
//
// 字段序与 C# CH_LlmApiConfig 声明序一致（序列化字节对齐）；apiKey 不入普通配置——
// 密钥单独存 Data/secrets/llm-api.cfg（GUID → key 映射）。
type LlmAPIConfig struct {
	// SchemaVersion 结构版本（恒 1）
	SchemaVersion int `json:"schemaVersion"`
	// APIConfigID 配置身份（GUID 文本 D 形态）
	APIConfigID string `json:"apiConfigId"`
	// DisplayName 显示名
	DisplayName string `json:"displayName"`
	// APIType 协议类型（dsf4.1 / newapi / ds，小写存储）
	APIType string `json:"apiType"`
	// Endpoint 端点（base 或完整 chat completions 路径）
	Endpoint string `json:"endpoint"`
	// DefaultModel 默认模型名
	DefaultModel string `json:"defaultModel"`
	// IsDefault 默认端点标记（唯一）
	IsDefault bool `json:"isDefault"`
	// IsBackup 备用端点标记（唯一；与默认互斥）
	IsBackup bool `json:"isBackup"`
}

// LlmAPIStore 是端点池存储——普通配置与密钥分离，写入走原子替换 + .last 兜底。
type LlmAPIStore struct {
	// configPath 普通配置文件（llm-api.json）
	configPath string
	// secretPath 密钥文件（secrets/llm-api.cfg）
	secretPath string
	// configs 内存态配置（加载序 = 身份排序；新增追加在尾部，与 C# Save 同形）
	configs []LlmAPIConfig
	// secrets 密钥映射（键为 32 位无短横形态）
	secrets map[string]string
	// lastError 最近一次读取失败原因（空 = 正常）
	lastError string
}

// LoadLlmAPIStore 读取端点池——主文件损坏时回落 .last 副本；两源皆废按空池运行（出声）。
func LoadLlmAPIStore(configPath, secretPath string) *LlmAPIStore {
	store := &LlmAPIStore{configPath: configPath, secretPath: secretPath, secrets: map[string]string{}}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			store.lastError = "llm-api.json 读取失败: " + err.Error()
		}
	} else if configs, parseErr := parseLlmAPIConfigs(raw); parseErr != nil {
		store.lastError = "llm-api.json 解析失败: " + parseErr.Error()
		if backup, backupErr := os.ReadFile(configPath + ".last"); backupErr == nil {
			if recovered, recoverErr := parseLlmAPIConfigs(backup); recoverErr == nil {
				store.configs = recovered
				store.lastError = ""
			}
		}
	} else {
		store.configs = configs
	}
	sort.SliceStable(store.configs, func(i, j int) bool {
		return store.configs[i].APIConfigID < store.configs[j].APIConfigID
	})
	if raw, err := os.ReadFile(secretPath); err == nil {
		var doc map[string]string
		if err := json.Unmarshal(StripBOM(raw), &doc); err == nil {
			for key, value := range doc {
				store.secrets[compactGUID(key)] = value
			}
		} else {
			store.lastError = "llm-api.cfg 解析失败: " + err.Error()
		}
	}
	return store
}

// parseLlmAPIConfigs 解析配置数组并逐条归一校验（任一非法整份拒绝——与 C# 校验器同口径）。
func parseLlmAPIConfigs(raw []byte) ([]LlmAPIConfig, error) {
	var list []LlmAPIConfig
	if err := json.Unmarshal(StripBOM(raw), &list); err != nil {
		return nil, err
	}
	for i := range list {
		if err := normalizeLlmAPIConfig(&list[i]); err != nil {
			return nil, fmt.Errorf("第 %d 条非法: %w", i+1, err)
		}
	}
	return list, nil
}

// normalizeLlmAPIConfig 归一文本字段并校验必填项（身份 / 名称 / 类型 / 端点 / 模型 / 绝对 URL）。
func normalizeLlmAPIConfig(cfg *LlmAPIConfig) error {
	cfg.DisplayName = strings.TrimSpace(cfg.DisplayName)
	cfg.APIType = strings.ToLower(strings.TrimSpace(cfg.APIType))
	cfg.Endpoint = strings.TrimSpace(cfg.Endpoint)
	cfg.DefaultModel = strings.TrimSpace(cfg.DefaultModel)
	if cfg.SchemaVersion != 1 {
		return errors.New("schemaVersion 必须为 1")
	}
	id := ParseGUID(cfg.APIConfigID)
	if id == "" || IsZeroGUID(id) {
		return errors.New("apiConfigId 非法")
	}
	cfg.APIConfigID = id
	if cfg.DisplayName == "" || cfg.APIType == "" || cfg.Endpoint == "" || cfg.DefaultModel == "" {
		return errors.New("displayName / apiType / endpoint / defaultModel 均不可为空")
	}
	parsed, err := url.Parse(cfg.Endpoint)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return errors.New("endpoint 非法（须为绝对 URL）")
	}
	return nil
}

// Configs 返回配置副本（身份序）。
func (s *LlmAPIStore) Configs() []LlmAPIConfig {
	if s == nil {
		return nil
	}
	out := make([]LlmAPIConfig, len(s.configs))
	copy(out, s.configs)
	return out
}

// TryGet 按身份取配置副本。
func (s *LlmAPIStore) TryGet(id string) (LlmAPIConfig, bool) {
	if s == nil {
		return LlmAPIConfig{}, false
	}
	target := ParseGUID(id)
	if target == "" {
		return LlmAPIConfig{}, false
	}
	for _, cfg := range s.configs {
		if cfg.APIConfigID == target {
			return cfg, true
		}
	}
	return LlmAPIConfig{}, false
}

// Secret 取配置对应密钥（未命中空串）。
func (s *LlmAPIStore) Secret(id string) string {
	if s == nil {
		return ""
	}
	return s.secrets[compactGUID(id)]
}

// LastError 最近一次读取失败原因（空 = 正常）。
func (s *LlmAPIStore) LastError() string {
	if s == nil {
		return ""
	}
	return s.lastError
}

// Save 新增或替换一份配置；apiKey 非空时同步写入密钥（空 = 保留原密钥）。
//
// 语义对齐 C# CH_LlmApiConfigStore.Save：默认 / 备用标记只归 SetDefault / SetBackup 专管，
// 替换普通字段时保留原标记；池内第一条配置自动成为默认端点。
func (s *LlmAPIStore) Save(cfg LlmAPIConfig, apiKey string) error {
	if err := normalizeLlmAPIConfig(&cfg); err != nil {
		return err
	}
	replaced := false
	for i := range s.configs {
		if s.configs[i].APIConfigID == cfg.APIConfigID {
			cfg.IsDefault = s.configs[i].IsDefault
			cfg.IsBackup = s.configs[i].IsBackup
			s.configs[i] = cfg
			replaced = true
			break
		}
	}
	if !replaced {
		if len(s.configs) == 0 {
			cfg.IsDefault = true
		}
		s.configs = append(s.configs, cfg)
	}
	if err := s.saveConfigs(); err != nil {
		return err
	}
	if strings.TrimSpace(apiKey) != "" {
		s.secrets[compactGUID(cfg.APIConfigID)] = strings.TrimSpace(apiKey)
		if err := s.saveSecrets(); err != nil {
			return err
		}
	}
	return nil
}

// SetDefault 设为默认端点（清其他默认；目标兼带备用标记时一并清除）。
func (s *LlmAPIStore) SetDefault(id string) bool {
	target := ParseGUID(id)
	if target == "" || IsZeroGUID(target) {
		return false
	}
	found := false
	for i := range s.configs {
		if s.configs[i].APIConfigID == target {
			s.configs[i].IsDefault = true
			s.configs[i].IsBackup = false
			found = true
			continue
		}
		s.configs[i].IsDefault = false
	}
	if !found {
		return false
	}
	return s.saveConfigs() == nil
}

// SetBackup 设为备用端点（唯一备用；目标为默认端点返回 false；空身份 = 取消备用）。
func (s *LlmAPIStore) SetBackup(id string) bool {
	target := ParseGUID(id)
	if id == "" || IsZeroGUID(target) {
		for i := range s.configs {
			s.configs[i].IsBackup = false
		}
		return s.saveConfigs() == nil
	}
	if target == "" {
		return false
	}
	found := false
	for i := range s.configs {
		if s.configs[i].APIConfigID == target {
			if s.configs[i].IsDefault {
				return false
			}
			s.configs[i].IsBackup = true
			found = true
			continue
		}
		s.configs[i].IsBackup = false
	}
	if !found {
		return false
	}
	return s.saveConfigs() == nil
}

// Delete 删除一份配置与对应密钥（默认配置可删——删除不复活）。
func (s *LlmAPIStore) Delete(id string) bool {
	target := ParseGUID(id)
	if target == "" {
		return false
	}
	kept := make([]LlmAPIConfig, 0, len(s.configs))
	removed := false
	for _, cfg := range s.configs {
		if cfg.APIConfigID == target {
			removed = true
			continue
		}
		kept = append(kept, cfg)
	}
	if !removed {
		return false
	}
	s.configs = kept
	if err := s.saveConfigs(); err != nil {
		return false
	}
	if _, ok := s.secrets[compactGUID(target)]; ok {
		delete(s.secrets, compactGUID(target))
		_ = s.saveSecrets()
	}
	return true
}

// saveConfigs 原子写普通配置；写前把当前有效副本留为 .last（损坏恢复源）。
func (s *LlmAPIStore) saveConfigs() error {
	sort.SliceStable(s.configs, func(i, j int) bool {
		return s.configs[i].APIConfigID < s.configs[j].APIConfigID
	})
	body, err := json.MarshalIndent(s.configs, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicWithBackup(s.configPath, body)
}

// saveSecrets 原子写密钥文件（键为 32 位无短横形态，按身份排序）。
func (s *LlmAPIStore) saveSecrets() error {
	keys := make([]string, 0, len(s.secrets))
	for key := range s.secrets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	doc := make(map[string]string, len(keys))
	for _, key := range keys {
		doc[key] = s.secrets[key]
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicWithBackup(s.secretPath, body)
}

// DeriveChatEndpoint 端点推导——已含 /chat/completions 原样；其余拼尾（空入参 → 空串）。
func DeriveChatEndpoint(endpoint string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if trimmed == "" {
		return ""
	}
	if strings.HasSuffix(trimmed, "/chat/completions") {
		return trimmed
	}
	return trimmed + "/chat/completions"
}

// MaskAPIKey 密钥掩码——保留头尾各 4 字符；长度 ≤8 全掩（与 C# MaskApiKey 同口径）。
func MaskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// ParseGUID 归一 GUID 文本为 D 形态（小写 + 短横）；非法返回空串。
func ParseGUID(value string) string {
	raw := strings.TrimSpace(value)
	raw = strings.TrimPrefix(raw, "{")
	raw = strings.TrimSuffix(raw, "}")
	if raw == "" {
		return ""
	}
	digits := make([]byte, 0, 32)
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if ch == '-' || ch == ' ' || ch == '\t' {
			continue
		}
		if !isHexDigit(ch) {
			return ""
		}
		digits = append(digits, ch)
	}
	if len(digits) != 32 {
		return ""
	}
	lower := strings.ToLower(string(digits))
	return lower[0:8] + "-" + lower[8:12] + "-" + lower[12:16] + "-" + lower[16:20] + "-" + lower[20:32]
}

// compactGUID 压成 32 位无短横形态（密钥文件键形态）。
func compactGUID(value string) string {
	return strings.ReplaceAll(ParseGUID(value), "-", "")
}

// IsZeroGUID 空身份判定（全零 / 非法 / 空串皆视为「跟随默认」）。
func IsZeroGUID(value string) bool {
	normalized := compactGUID(value)
	if normalized == "" {
		return true
	}
	for i := 0; i < len(normalized); i++ {
		if normalized[i] != '0' {
			return false
		}
	}
	return true
}

// ZeroGUIDText 全零 GUID 文本（写入 cat.cfg 的空身份口径）。
const ZeroGUIDText = "00000000-0000-0000-0000-000000000000"

// isHexDigit 十六进制字符判定。
func isHexDigit(ch byte) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}

// writeAtomicWithBackup 原子写文件——先把有效旧副本留为 .last，再写临时文件并替换。
func writeAtomicWithBackup(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if previous, err := os.ReadFile(path); err == nil && len(previous) > 0 {
		_ = os.WriteFile(path+".last", previous, 0o644)
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
