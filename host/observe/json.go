package observe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// marshalJSON 序列化 JSON——关闭 HTML 转义（对齐 System.Text.Json 的中文直出），
// 去掉编码器追加的尾换行（对齐 C# JsonUtil.Serialize 的紧凑输出）。
func marshalJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		// 失败必须可见——调用方拿到 null 而不是半截 JSON
		fmt.Fprintln(os.Stderr, "[observe] JSON 序列化失败: "+err.Error())
		return "null"
	}
	return string(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
}

// payloadPair 是审计载荷的 k/v 对（C# SummarizePayload 的解析形态）。
type payloadPair struct {
	K *string `json:"k"`
	V *string `json:"v"`
}

// parsePayloadPairs 解析审计载荷 JSON 数组为 k=v 逗号拼接。
//
// 返回 ok=false 表示不是「k/v 对数组」形态——调用方回落原文截断。
// 空数组 / 全空 kv 视为解析成功但摘要为空（调用方按空处理）。
func parsePayloadPairs(payload string) (string, bool) {
	var pairs []payloadPair
	if err := json.Unmarshal([]byte(payload), &pairs); err != nil {
		return "", false
	}
	built := ""
	for _, p := range pairs {
		k, v := "", ""
		if p.K != nil {
			k = *p.K
		}
		if p.V != nil {
			v = *p.V
		}
		if built != "" {
			built += ", "
		}
		built += k + "=" + v
	}
	return built, true
}

// LogItem 是快照 logs 段与 SSE log 事件的条目（六字段——不含 type/payload）。
type LogItem struct {
	// Time 时间戳
	Time string `json:"time"`
	// Frame 帧号
	Frame int64 `json:"frame"`
	// Level 级别文本（INFO/WARN/ERR）
	Level string `json:"level"`
	// Category 类别
	Category string `json:"category"`
	// Module 模块名
	Module string `json:"module"`
	// Message 消息
	Message string `json:"message"`
}

// LogQueryItem 是 GET /api/v1/logs 的条目（七字段——多 type）。
type LogQueryItem struct {
	// Time 时间戳
	Time string `json:"time"`
	// Frame 帧号
	Frame int64 `json:"frame"`
	// Level 级别文本
	Level string `json:"level"`
	// Category 类别
	Category string `json:"category"`
	// Module 模块名
	Module string `json:"module"`
	// Type 审计事件名（非审计空串）
	Type string `json:"type"`
	// Message 消息
	Message string `json:"message"`
}

// logsResponse 是 GET /api/v1/logs 的响应体。
type logsResponse struct {
	// Version 协议版本（固定 1）
	Version int `json:"version"`
	// Logs 日志条目（旧→新）
	Logs []LogQueryItem `json:"logs"`
}

// LogsArrayJSON 构建快照 logs 段数组（?logs=N 替换用）——六字段、trace 过滤。
func (s *LogStore) LogsArrayJSON(limit int) string {
	entries := s.Snapshot(limit)
	items := make([]LogItem, 0, len(entries))
	for _, entry := range entries {
		if IsTraceAudit(entry) {
			continue
		}
		items = append(items, LogItem{
			Time:     entry.Time,
			Frame:    entry.Frame,
			Level:    LevelText(entry.Level),
			Category: entry.Category,
			Module:   entry.Module,
			Message:  entry.Message,
		})
	}
	return marshalJSON(items)
}

// IncrementArrayJSON 取 from 之后的日志增量为数组 JSON，并回报新游标。
//
// 对齐 C# PushLogIncrements 的取数面（SnapshotFrom + trace 过滤 + 批量合帧）：
// 同一轮的多条日志合成一个数组载荷；trace 类审计过滤后为空返回空串，
// 游标照常推进（原始条目已消费，不重复扫描）。序列化在锁外（SnapshotFrom 内已快照）。
func (s *LogStore) IncrementArrayJSON(from int) (string, int) {
	entries, end := s.SnapshotFrom(from)
	items := make([]LogItem, 0, len(entries))
	for _, entry := range entries {
		if IsTraceAudit(entry) {
			continue
		}
		items = append(items, LogItem{
			Time:     entry.Time,
			Frame:    entry.Frame,
			Level:    LevelText(entry.Level),
			Category: entry.Category,
			Module:   entry.Module,
			Message:  entry.Message,
		})
	}
	if len(items) == 0 {
		return "", end
	}
	return marshalJSON(items), end
}

// LogsResponseJSON 构建 GET /api/v1/logs 响应。
//
// 尾部倒序扫描——匹配 cat（精确）/ type（前缀）收集 limit 条后 reverse 回时间序。
// trace 类审计不进前端（只服务内存真源与 CLI 审计面）。
func (s *LogStore) LogsResponseJSON(limit int, cat, typePrefix string) string {
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}
	all := s.Snapshot(0)
	list := make([]LogQueryItem, 0, limit)
	for i := len(all) - 1; i >= 0 && len(list) < limit; i-- {
		entry := all[i]
		if IsTraceAudit(entry) {
			continue
		}
		if cat != "" && entry.Category != cat {
			continue
		}
		if typePrefix != "" && !hasPrefix(entry.Type, typePrefix) {
			continue
		}
		list = append(list, LogQueryItem{
			Time:     entry.Time,
			Frame:    entry.Frame,
			Level:    LevelText(entry.Level),
			Category: entry.Category,
			Module:   entry.Module,
			Type:     entry.Type,
			Message:  entry.Message,
		})
	}
	// 倒序收集——还原时间序（旧→新）
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	return marshalJSON(logsResponse{Version: 1, Logs: list})
}

// hasPrefix 字符串前缀判定（空前缀恒 false——调用方已判空）。
func hasPrefix(s, prefix string) bool {
	if len(prefix) > len(s) {
		return false
	}
	return s[:len(prefix)] == prefix
}
