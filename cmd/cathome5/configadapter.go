package main

import (
	"path/filepath"
	"strings"

	"cathome5/host/config"
	"cathome5/mau/bricks"
)

// configServiceAdapter 把全局配置存储与 schema 适配成积木层配置窄接口（config.bridge 的宿主依赖）。
//
// 单点：读面走 config.Store.Resolve/BuildAll（与 GET /api/v1/config 同源），
// 写面走 SetChecked / ResetToDefault（与 POST /api/v1/config 同源），声明走 Schema.Declare。
type configServiceAdapter struct {
	// store 配置存储（nil = 不可用——config-list 空表）
	store *config.Store
	// schema 配置声明（nil = 未绑定）
	schema *config.Schema
}

// AllEntries 全项解析——schema 声明序 + 未声明落盘键。
func (a *configServiceAdapter) AllEntries() []bricks.ConfigEntry {
	out := []bricks.ConfigEntry{}
	if a == nil || a.store == nil {
		return out
	}
	for _, entry := range a.store.BuildAll(a.schema) {
		out = append(out, toBrickEntry(entry))
	}
	return out
}

// Resolve 单项解析——三层兜底 file → env → default。
func (a *configServiceAdapter) Resolve(key string) bricks.ConfigEntry {
	if a == nil || a.store == nil {
		return bricks.ConfigEntry{Key: key}
	}
	return toBrickEntry(a.store.Resolve(a.schema, key))
}

// Values 落盘显式值（首次出现序）。
func (a *configServiceAdapter) Values() [][2]string {
	if a == nil || a.store == nil {
		return nil
	}
	return a.store.All()
}

// SchemaItems schema 声明项（声明序；nil = schema 未绑定）。
func (a *configServiceAdapter) SchemaItems() []bricks.ConfigItem {
	if a == nil || a.schema == nil {
		return nil
	}
	out := make([]bricks.ConfigItem, 0, len(a.schema.Items))
	for _, item := range a.schema.Items {
		out = append(out, bricks.ConfigItem{
			Key:       item.Key,
			File:      item.File,
			Default:   item.Default,
			Desc:      item.Desc,
			EmptyDesc: item.EmptyDesc,
			Type:      item.Type,
			Min:       item.Min,
			Max:       item.Max,
			Sensitive: item.Sensitive,
			Writable:  item.Writable,
		})
	}
	return out
}

// DeclareConfig 声明新键——模板与运行副本同步写；新段即时注册段文件映射。
func (a *configServiceAdapter) DeclareConfig(key, desc string) (file string, newSegment bool, registerNote, errText string) {
	if a == nil || a.schema == nil {
		return "", false, "", "schema 未绑定"
	}
	file, newSegment, errText = a.schema.Declare(key, desc)
	if errText != "" {
		return "", false, "", errText
	}
	if !newSegment {
		return file, false, "", ""
	}
	dot := strings.IndexByte(key, '.')
	if dot <= 0 {
		return file, true, "", ""
	}
	segment := key[:dot]
	dir := filepath.Dir(a.schema.SourcePath)
	if dir == "" || dir == "." {
		return file, true, " · 新段目录未知——重启后自动注册", ""
	}
	if a.store != nil {
		a.store.AddFile(segment, filepath.Join(dir, file))
		return file, true, " · 新段已即时注册", ""
	}
	return file, true, " · 新段目录未知——重启后自动注册", ""
}

// SetChecked 受控写入（白名单 + 值域 + 原子写 + 回滚）。
func (a *configServiceAdapter) SetChecked(key, value string) (bool, string) {
	if a == nil || a.store == nil {
		return false, "ConfigStore 不可用"
	}
	return a.store.SetChecked(key, value, a.schema)
}

// ResetToDefault 还原默认（key 空 = 全群 writable）。
func (a *configServiceAdapter) ResetToDefault(key string) (bool, string) {
	if a == nil || a.store == nil {
		return false, "schema 未绑定"
	}
	return a.store.ResetToDefault(key, a.schema)
}

// toBrickEntry 配置有效值 → 积木层结构。
func toBrickEntry(entry config.Entry) bricks.ConfigEntry {
	return bricks.ConfigEntry{
		Key:       entry.Key,
		Value:     entry.Value,
		Source:    entry.Source,
		Default:   entry.Default,
		Desc:      entry.Desc,
		EmptyDesc: entry.EmptyDesc,
		Type:      entry.Type,
		Min:       entry.Min,
		Max:       entry.Max,
		Declared:  entry.Declared,
		Writable:  entry.Writable,
		Sensitive: entry.Sensitive,
	}
}
