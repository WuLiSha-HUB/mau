package config

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// 本文件是全局配置的写面——对齐 C# `Mau.Runtime/ConfigStore.cs` 的
// Set / Save / SetChecked 三段，加 schema 驱动的段注册（C# Program.Resolve.cs
// RegisterSchemaSegments）。
//
// 写面三步口径（与端点池存储同规）：归一（调用方负责）→ 校验（SetChecked）→ 原子写（Save）。

// KeyPrefix 提取键前缀段——"ui.chat_font_size" → "ui"；无句点或空前缀返回空串（归主文件）。
func KeyPrefix(key string) string {
	dot := strings.IndexByte(key, '.')
	if dot <= 0 {
		return ""
	}
	return key[:dot]
}

// RegisterSchema 按 schema 的「键前缀段 → 文件归属」注册段（对齐 C# RegisterSchemaSegments）。
//
// 宿主不再硬编码段清单：新增段 = 模板加项，零代码改动。同前缀映射到不同文件 → 取首个。
// 返回注册说明（段数 + 冲突数），由调用方记日志——本包不持有日志出口。
func (s *Store) RegisterSchema(schema *Schema) string {
	if s == nil || schema == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	segments := make(map[string]string)
	conflicts := 0
	for i := range schema.Items {
		item := &schema.Items[i]
		if item.File == "" {
			continue
		}
		segment := KeyPrefix(item.Key)
		if segment == "" {
			continue
		}
		path := filepath.Join(s.dir, item.File)
		if prev, seen := segments[segment]; seen {
			if !strings.EqualFold(prev, path) {
				conflicts++
			}
			continue
		}
		segments[segment] = path
	}
	for segment, path := range segments {
		s.fileByPrefix[segment] = path
	}
	note := "config.seg | schema 驱动段注册: " + strconv.Itoa(len(segments)) + " 段"
	if conflicts > 0 {
		note += " | 前缀冲突 " + strconv.Itoa(conflicts) + " 处（取首个）"
	}
	return note
}

// Set 设置内存值（内存生效，Save 落盘）——新键追加到首次出现序末尾。
func (s *Store) Set(key, value string) {
	if s == nil || key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = make(map[string]string)
	}
	if _, seen := s.values[key]; !seen {
		s.order = append(s.order, key)
	}
	s.values[key] = value
}

// remove 移除内存值（回滚用）——同时移出首次出现序（对齐 C# remove 后 All 不含该键）。
func (s *Store) remove(key string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
	for i, k := range s.order {
		if k == key {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
}

// Save 按键前缀分组原子落盘（对齐 C# ConfigStore.Save）。
//
// 分组规则：前缀段命中 fileByPrefix → 该文件；否则落主文件（llm.cfg）。
// 每行 `key=value` + CRLF（C# StringBuilder.AppendLine 在 Windows 即 \r\n）、UTF-8 无 BOM。
// 注意：整文件重写——注释行与未登记的键顺序不保留（与 C# 同形）。
func (s *Store) Save() error {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	byTarget := make(map[string]*strings.Builder)
	targets := make([]string, 0, 8)
	for _, k := range s.order {
		value, ok := s.values[k]
		if !ok {
			continue
		}
		target := ""
		if prefix := KeyPrefix(k); prefix != "" {
			if mapped, hit := s.fileByPrefix[prefix]; hit && mapped != "" {
				target = mapped
			}
		}
		if target == "" {
			target = s.mainPath
		}
		if target == "" {
			continue
		}
		builder, seen := byTarget[target]
		if !seen {
			builder = &strings.Builder{}
			byTarget[target] = builder
			targets = append(targets, target)
		}
		builder.WriteString(k)
		builder.WriteByte('=')
		builder.WriteString(value)
		builder.WriteString("\r\n")
	}
	// 快照完成后即可释读锁——落盘用的是拷贝后的字节
	payloads := make(map[string][]byte, len(byTarget))
	for target, builder := range byTarget {
		payloads[target] = []byte(builder.String())
	}
	s.mu.RUnlock()

	// 固定顺序写（同一份内存状态两次落盘字节一致）
	sort.Strings(targets)
	for _, target := range targets {
		if err := writeAtomic(target, payloads[target]); err != nil {
			return err
		}
	}
	return nil
}

// SetChecked 受控配置写入——P8.5d 自改通道唯一实现（POST /api/v1/config 与 config-set 共用）。
//
// 校验链（顺序即语义，对齐 C# ConfigStore.SetChecked）：
// key 非空 → schema 声明 → writable 白名单 → 值域校验 → 掩码回写拒绝 → 旧值快照 → 写入落盘 → 失败回滚。
// 返回 (是否成功, 失败原因)；成功时原因为空串。
func (s *Store) SetChecked(key, value string, schema *Schema) (bool, string) {
	if key == "" {
		return false, "key 为空"
	}

	// [段1] schema 白名单 + 值域校验
	if schema != nil {
		item := schema.Find(key)
		if item == nil {
			return false, "配置键未在 schema 声明: " + key
		}
		if !item.Writable {
			return false, "只读配置项: " + key
		}
		if ok, reason := schema.Validate(key, value); !ok {
			return false, reason
		}
	}

	// [段2] 掩码回写拒绝——防掩码值覆盖真实值
	if strings.Contains(value, "****") {
		return false, "value 含掩码标记——请输入真实值"
	}

	// [段3] 旧值快照 + 写入 + 失败回滚（写入异常时内存与磁盘一致恢复）
	oldValue, hadOld := s.TryGet(key)
	s.Set(key, value)
	if err := s.Save(); err != nil {
		if hadOld {
			s.Set(key, oldValue)
		} else {
			s.remove(key)
		}
		if rollbackErr := s.Save(); rollbackErr != nil {
			// 回滚再失败——内存已恢复一致，磁盘差异在此显式带回（C# 侧记审计、文案不变；
			// 双失败场景 a4 不覆盖，取「失败更可见」）
			return false, "写入失败: " + err.Error() + "（回滚再失败: " + rollbackErr.Error() + "）"
		}
		return false, "写入失败: " + err.Error()
	}
	return true, ""
}

// Validate 值域校验——P8.5d 写入通道（SetChecked）前置校验。
//
// 规则（对齐 C# ConfigSchema.Validate）：值长度上限 1024（UTF-16 码元数）；
// type=int → 整数解析 + min/max 范围；type=bool → true/false/1/0；
// type=string/空 → 枚举值域（声明非空时忽略大小写命中其一）。
func (s *Schema) Validate(key, value string) (bool, string) {
	item := s.Find(key)
	if item == nil {
		return false, "配置键未在 schema 声明: " + key
	}
	if len(utf16.Encode([]rune(value))) > 1024 {
		return false, "配置值过长（>1024）"
	}

	switch item.Type {
	case "", "string":
		if len(item.Values) > 0 && !containsValue(item.Values, value) {
			return false, "配置项 " + key + " 只接受以下值之一: " + strings.Join(item.Values, " / ")
		}
		return true, ""
	case "int":
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return false, "配置项 " + key + " 需要整数: " + value
		}
		if item.Min != "" {
			if min, err := strconv.ParseInt(strings.TrimSpace(item.Min), 10, 64); err == nil && parsed < min {
				return false, "配置项 " + key + " 低于下限 " + item.Min
			}
		}
		if item.Max != "" {
			if max, err := strconv.ParseInt(strings.TrimSpace(item.Max), 10, 64); err == nil && parsed > max {
				return false, "配置项 " + key + " 超出上限 " + item.Max
			}
		}
		return true, ""
	case "bool":
		if value != "true" && value != "false" && value != "1" && value != "0" {
			return false, "配置项 " + key + " 需要布尔值: " + value
		}
		return true, ""
	}
	return false, "未知值类型: " + item.Type
}

// containsValue 枚举值域命中判定——忽略大小写（对齐 C# ContainsValue）。
func containsValue(values []string, value string) bool {
	for _, candidate := range values {
		if strings.EqualFold(candidate, value) {
			return true
		}
	}
	return false
}

// AddFile 追加配置文件槽——注册前缀映射并装载既有键值。
//
// 文件不存在也注册映射：新段声明（config-set declare=true）后立即可落盘（对齐 C# ConfigStore.AddFile）。
func (s *Store) AddFile(prefix, path string) {
	if s == nil || prefix == "" || path == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = make(map[string]string)
	}
	if raw, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(StripBOM(raw)), "\n") {
			line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			eq := strings.IndexByte(line, '=')
			if eq <= 0 {
				continue
			}
			key := strings.TrimSpace(line[:eq])
			value := strings.TrimSpace(line[eq+1:])
			if key == "" {
				continue
			}
			if _, seen := s.values[key]; !seen {
				s.order = append(s.order, key)
			}
			s.values[key] = value
		}
	}
	s.fileByPrefix[prefix] = path
	if s.dir == "" {
		s.dir = filepath.Dir(path)
	}
}

// ResetToDefault 配置还原默认——key 空 = 全群（仅 writable 项）；key 非空 = 单项。
//
// 语义（对齐 C# ConfigStore.ResetToDefault）：把值写为 schema default 并落盘；未声明 / 只读项拒绝；
// 落盘失败按快照回滚（内存与磁盘一致恢复）。
func (s *Store) ResetToDefault(key string, schema *Schema) (bool, string) {
	if s == nil || schema == nil {
		return false, "schema 未绑定"
	}
	// [段1] 目标集合——单项或全群 writable
	var targets []SchemaItem
	if key == "" {
		for i := range schema.Items {
			if schema.Items[i].Writable {
				targets = append(targets, schema.Items[i])
			}
		}
	} else {
		item := schema.Find(key)
		if item == nil {
			return false, "配置键未在 schema 声明: " + key
		}
		if !item.Writable {
			return false, "只读配置项: " + key
		}
		targets = append(targets, *item)
	}
	// [段2] 快照 + 逐项设默认 + 落盘（失败回滚）
	type snapshot struct {
		value  string
		hadOld bool
	}
	snaps := make([]snapshot, len(targets))
	for i, item := range targets {
		value, had := s.TryGet(item.Key)
		snaps[i] = snapshot{value: value, hadOld: had}
	}
	for _, item := range targets {
		s.Set(item.Key, item.Default)
	}
	if err := s.Save(); err != nil {
		for i, item := range targets {
			if snaps[i].hadOld {
				s.Set(item.Key, snaps[i].value)
			} else {
				s.remove(item.Key)
			}
		}
		if rollbackErr := s.Save(); rollbackErr != nil {
			return false, "还原失败: " + err.Error() + "（回滚再失败: " + rollbackErr.Error() + "）"
		}
		return false, "还原失败: " + err.Error()
	}
	return true, ""
}
