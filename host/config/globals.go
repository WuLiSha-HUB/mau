package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Store 是通用键值配置存储（多文件槽）。
//
// 文件格式与 C# ConfigStore 一致：每行 key=value；# 开头为注释；空行跳过；
// 坏行跳过。多文件按「键前缀段 → 文件」注册（llm. → llm.cfg）。
type Store struct {
	values       map[string]string
	fileByPrefix map[string]string
	order        []string
	// dir 配置目录（写面按段分组落盘的锚点）
	dir string
	// mainPath 主文件（无前缀段键的落点——对齐 C# ConfigStore.Load(llm.cfg) 的主文件语义）
	mainPath string
	// mu 读写锁——写面（POST /api/v1/config）与读面（轮执行 / 快照）并发
	mu sync.RWMutex
}

// LoadStore 加载配置目录下的全部 *.cfg（cat-default.cfg 除外——它是 JSON 资产）。
func LoadStore(configDir string) *Store {
	s := &Store{
		values:       make(map[string]string),
		fileByPrefix: make(map[string]string),
		dir:          configDir,
		mainPath:     filepath.Join(configDir, "llm.cfg"),
	}
	entries, err := os.ReadDir(configDir)
	if err != nil {
		return s
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".cfg") {
			continue
		}
		if strings.EqualFold(e.Name(), "cat-default.cfg") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		prefix := strings.TrimSuffix(name, filepath.Ext(name))
		s.addFile(prefix, filepath.Join(configDir, name))
	}
	return s
}

// addFile 装载一个配置文件并注册前缀映射（AddFile 的装载期别名）。
func (s *Store) addFile(prefix, path string) {
	s.AddFile(prefix, path)
}

// TryGet 读落盘显式值。
func (s *Store) TryGet(key string) (string, bool) {
	if s == nil || s.values == nil {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.values[key]
	return v, ok
}

// All 返回全部键值（按首次出现序——对齐 C# Dictionary 的插入序遍历）。
func (s *Store) All() [][2]string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([][2]string, 0, len(s.order))
	for _, k := range s.order {
		if v, ok := s.values[k]; ok {
			out = append(out, [2]string{k, v})
		}
	}
	return out
}

// Entry 是配置有效值解析结果（读面统一结构）。
type Entry struct {
	// Key 配置键
	Key string
	// Value 有效值（未掩码）
	Value string
	// Source 来源：file / env / default
	Source string
	// Declared 是否在 schema 声明
	Declared bool
	// File 文件归属（未声明为空串）
	File string
	// Default schema 默认值（未声明为空串）
	Default string
	// Writable 可写标志（未声明恒 false）
	Writable bool
	// Sensitive 敏感标志
	Sensitive bool
	// Desc 描述
	Desc string
	// EmptyDesc 空值语义文案
	EmptyDesc string
	// Type 值类型
	Type string
	// Min 数值下限
	Min string
	// Max 数值上限
	Max string
}

// UndeclaredDesc 是未声明键的固定描述文案。
const UndeclaredDesc = "未在 schema 声明（只读；声明后可见可改）"

// EnvName 返回键对应的环境变量名；无兜底返回空串。
func EnvName(key string) string {
	switch key {
	case "llm.thinking":
		return "MAU_LLM_THINKING"
	case "llm.reasoning_effort":
		return "MAU_LLM_REASONING_EFFORT"
	case "llm.max_tokens":
		return "MAU_LLM_MAX_TOKENS"
	}
	return ""
}

// Resolve 三层解析单一出口：file → env → default。
func (s *Store) Resolve(schema *Schema, key string) Entry {
	entry := Entry{Key: key}
	if s != nil {
		if v, ok := s.TryGet(key); ok {
			entry.Value = v
			entry.Source = "file"
		}
	}
	if entry.Source == "" {
		if envName := EnvName(key); envName != "" {
			if raw := os.Getenv(envName); raw != "" {
				entry.Value = raw
				entry.Source = "env"
			}
		}
	}
	item := schema.Find(key)
	if item != nil {
		entry.Declared = true
		entry.File = item.File
		entry.Default = item.Default
		entry.Writable = item.Writable
		entry.Sensitive = item.Sensitive
		entry.Desc = item.Desc
		entry.EmptyDesc = item.EmptyDesc
		entry.Type = item.Type
		entry.Min = item.Min
		entry.Max = item.Max
		if entry.Source == "" {
			entry.Value = item.Default
			entry.Source = "default"
		}
		return entry
	}
	entry.Declared = false
	entry.Writable = false
	if schema != nil {
		entry.Sensitive = schema.IsSensitive(key)
	} else {
		entry.Sensitive = IsSecretKey(key)
	}
	entry.Desc = UndeclaredDesc
	return entry
}

// BuildAll 解析全项：schema 声明序 + 落盘未声明键（键升序追加尾部）。
func (s *Store) BuildAll(schema *Schema) []Entry {
	var out []Entry
	if schema != nil {
		for i := range schema.Items {
			out = append(out, s.Resolve(schema, schema.Items[i].Key))
		}
	}
	if s != nil {
		for _, pair := range s.All() {
			if schema.Find(pair[0]) != nil {
				continue
			}
			out = append(out, s.Resolve(schema, pair[0]))
		}
	}
	return out
}

// IsSecretKey 按键名兜底判定敏感（api_key / secret / token 系）。
func IsSecretKey(key string) bool {
	lower := strings.ToLower(key)
	return strings.Contains(lower, "api_key") || strings.Contains(lower, "secret") || strings.Contains(lower, "token")
}

// MaskSecret 掩码敏感值：前4 + **** + 后4；长度 ≤8 全 ****；空值原样。
func MaskSecret(value string) string {
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= 8 {
		return "****"
	}
	return string(runes[:4]) + "****" + string(runes[len(runes)-4:])
}
