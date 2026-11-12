package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// PackDefinition 是一条加载包定义（Data/config/packs.json 池条目）。
type PackDefinition struct {
	// Key 包 key——池内唯一索引（pack 工具入参）
	Key string
	// Desc 描述——info 展示用
	Desc string
	// Paths 路径清单——受控根 id: 前缀或绝对路径；目录项按一级 *.md 展开
	Paths []string
}

// PackOption 是配置面下拉项（key + 描述 + 路径项数）。
type PackOption struct {
	// Key 包 key
	Key string `json:"key"`
	// Desc 描述
	Desc string `json:"desc"`
	// PathCount 路径项数
	PathCount int `json:"pathCount"`
}

// LoadPackPool 读取包池——文件缺失 / 损坏 = 空池（不阻断宿主，失败出声由调用方登记）。
//
// 键序保持 JSON 声明序（C# 用 EnumerateObject 遍历，前端下拉按池序渲染）。
func LoadPackPool(path string) []PackDefinition {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parsePackPool(StripBOM(raw))
}

// parsePackPool 解析池文档 `{ "packs": { "key": { "desc": "...", "paths": [...] } } }`。
func parsePackPool(raw []byte) []PackDefinition {
	var doc struct {
		Packs json.RawMessage `json:"packs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Packs) == 0 {
		return nil
	}
	// 保序解析——json.Unmarshal 进 map 会丢声明序，这里走 token 流。
	decoder := json.NewDecoder(bytes.NewReader(doc.Packs))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil
	}
	pool := make([]PackDefinition, 0, 8)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return pool
		}
		key, _ := keyToken.(string)
		var entry struct {
			Desc  string   `json:"desc"`
			Paths []string `json:"paths"`
		}
		if err := decoder.Decode(&entry); err != nil {
			return pool
		}
		pool = append(pool, PackDefinition{Key: key, Desc: entry.Desc, Paths: entry.Paths})
	}
	return pool
}

// BuildPackOptions 池 → 配置面下拉项（路径项数缺省 0）。
func BuildPackOptions(pool []PackDefinition) []PackOption {
	out := make([]PackOption, 0, len(pool))
	for _, def := range pool {
		out = append(out, PackOption{Key: def.Key, Desc: def.Desc, PathCount: len(def.Paths)})
	}
	return out
}

// jsonString 编码 JSON 字符串字面量（关闭 HTML 转义——对齐 C# JsonUtil.Str）。
func jsonString(value string) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return `""`
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// PackPoolPath 取包池文件路径（Data/config/packs.json）。
func PackPoolPath(dataRoot string) string {
	return filepath.Join(dataRoot, "config", "packs.json")
}

// SerializePackPool 池 → JSON 文本（管理面写入唯一出口，保持池序）。
func SerializePackPool(pool []PackDefinition) []byte {
	var builder strings.Builder
	builder.WriteString(`{"packs":{`)
	for i, def := range pool {
		if i > 0 {
			builder.WriteString(",")
		}
		builder.WriteString(jsonString(def.Key))
		builder.WriteString(`:{"desc":`)
		builder.WriteString(jsonString(def.Desc))
		builder.WriteString(`,"paths":[`)
		for j, path := range def.Paths {
			if j > 0 {
				builder.WriteString(",")
			}
			builder.WriteString(jsonString(path))
		}
		builder.WriteString(`]}`)
	}
	builder.WriteString(`}}`)
	return []byte(builder.String())
}

// SavePackPool 原子写包池（同目录临时文件 + 替换）。
func SavePackPool(path string, pool []PackDefinition) error {
	return writeAtomic(path, SerializePackPool(pool))
}
