package bricks

import (
	"strings"

	"cathome5/mau/runtime"
)

// DataBoxSetStr 复现 data.box_set_str——写 DataBox 数据区 str 值（薄壳转发）。
//
// 返回 "true"（C# 恒 true）；ok = DataBox 存在。
func DataBoxSetStr(db *runtime.DataBox, scope, key, value string) (string, bool) {
	if db == nil {
		return "false", false
	}
	db.Set(scope, key, value)
	return "true", true
}

// DataBoxGetStr 复现 data.box_get_str——读 DataBox 数据区 str 值。
//
// 返回 (值, 是否命中)——Key 不存在或类型不是 string 一律未命中（C# TryGet<string> 同口径）。
func DataBoxGetStr(db *runtime.DataBox, scope, key string) (string, bool) {
	if db == nil {
		return "", false
	}
	value, ok := db.Get(scope, key)
	if !ok {
		return "", false
	}
	text, isStr := value.(string)
	if !isStr {
		return "", false
	}
	return text, true
}

// SelfDescAdd 复现 self.desc.add——追加实体自述行（已有行保留，末尾追加；每参一行，空参跳过）。
func SelfDescAdd(db *runtime.DataBox, flowID int64, lines ...string) (string, bool) {
	if db == nil {
		return "false", false
	}
	scope := itoa64(flowID)
	kept := make([]string, 0, 4)
	if existing, ok := db.Get(scope, "self_desc"); ok {
		if arr, isArr := existing.([]string); isArr {
			kept = append(kept, arr...)
		}
	}
	for _, line := range lines {
		if line != "" {
			kept = append(kept, line)
		}
	}
	db.Set(scope, "self_desc", kept)
	return "true", true
}

// LogWrite 复现 log.write——写一条结构化日志（内存总账 + 可选磁盘持久化，模块名固定 CH4）。
//
// level ≤ 0 归一为 INFO（与 LogStore.Add 同口径）；level 非法 / 日志出口未注入走失败侧。
func LogWrite(logf func(level int, message, category string), text, level, category string) (string, bool) {
	if logf == nil {
		return "false", false
	}
	lvl, ok := parseLogLevel(level)
	if !ok {
		return "false", false
	}
	logf(lvl, text, category)
	return "true", true
}

// parseLogLevel 解析日志级别——允许负号（level ≤ 0 由日志面归一为 INFO）。
func parseLogLevel(raw string) (int, bool) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return 0, false
	}
	neg := false
	if text[0] == '-' {
		neg = true
		text = text[1:]
	}
	if text == "" {
		return 0, false
	}
	value := 0
	for _, r := range text {
		if r < '0' || r > '9' {
			return 0, false
		}
		value = value*10 + int(r-'0')
		if value > 1<<30 {
			return 0, false
		}
	}
	if neg {
		value = -value
	}
	return value, true
}
