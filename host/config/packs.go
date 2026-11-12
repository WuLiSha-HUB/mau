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

// GetMountedPacks 猫级挂载清单——cat.cfg packs 字段（缺失 / 损坏 = 未挂载任何包）。
func GetMountedPacks(dataRoot, catKey string) []string {
	if catKey == "" {
		return nil
	}
	cfg, err := LoadCatCfg(filepath.Join(dataRoot, "sessions", catKey, "cat.cfg"))
	if err != nil || cfg == nil {
		return nil
	}
	return cfg.Packs
}

// BuildPackPayload 包解析入口——pack 内置工具数据面（catKey + packKey → 结果 JSON）。
//
// 三道门：池内存在 → 本猫已挂载 → 返回路径清单（读取与展开归会话层，与前文注入同源）。
// 成功：{"ok":true,"key":"..","desc":"..","paths":[".."]}；失败：{"ok":false,"error":"ERR|..."}。
// 逐字对齐 C# `AdminService.Packs.cs` 的 BuildPackPayload。
func BuildPackPayload(dataRoot, catKey, packKey string) string {
	pool := LoadPackPool(PackPoolPath(dataRoot))
	available := make([]string, 0, len(pool))
	for _, def := range pool {
		available = append(available, def.Key)
	}
	availableText := strings.Join(available, ",")
	if availableText == "" {
		availableText = "(空池)"
	}
	var target *PackDefinition
	for i := range pool {
		if pool[i].Key == packKey {
			target = &pool[i]
			break
		}
	}
	if target == nil {
		return `{"ok":false,"error":` + jsonString("ERR|PACK_KEY_NOT_FOUND|包不存在: "+packKey+" | 池内可用: "+availableText) + `}`
	}
	mounted := GetMountedPacks(dataRoot, catKey)
	isMounted := false
	for _, key := range mounted {
		if key == packKey {
			isMounted = true
			break
		}
	}
	if !isMounted {
		mountedText := strings.Join(mounted, ",")
		if mountedText == "" {
			mountedText = "(未挂载)"
		}
		return `{"ok":false,"error":` + jsonString("ERR|PACK_NOT_MOUNTED|包未挂载: "+packKey+" | 本猫已挂载: "+mountedText+" | 池内可用: "+availableText) + `}`
	}
	if len(target.Paths) == 0 {
		return `{"ok":false,"error":` + jsonString("ERR|PACK_EMPTY|包无路径清单: "+packKey) + `}`
	}
	var b strings.Builder
	b.WriteString(`{"ok":true,"key":`)
	b.WriteString(jsonString(target.Key))
	b.WriteString(`,"desc":`)
	b.WriteString(jsonString(target.Desc))
	b.WriteString(`,"paths":[`)
	for i, path := range target.Paths {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(jsonString(path))
	}
	b.WriteString(`]}`)
	return b.String()
}
