package session

import (
	"encoding/json"
	"math/rand"
	"strings"
	"time"
)

// SetInfoProvider 接线 info 内置工具的数据源（nil = 明示不可用）。
func (s *Session) SetInfoProvider(fn func() string) {
	r := s.roundState()
	r.mu.Lock()
	r.infoProvider = fn
	r.mu.Unlock()
}

// ExecuteBuiltin 内置工具统一执行入口——会话内直执（Note / time / random / info / timeback）。
//
// 作用域锁定：Note / sleep / timer 在 timeback 作用域存活期一律拒绝（与区间删除语义冲突）。
func (s *Session) ExecuteBuiltin(name, argsJSON string) string {
	if gate := s.Timeback.CheckBuiltinTool(name); gate != "" {
		return gate
	}
	switch name {
	case "Note":
		return s.Note.Execute(argsJSON)
	case "time":
		nowText := time.Now().Format("2006-01-02 15:04:05")
		return metaHead("time", true, "", -1, []metaField{{"ts", nowText}}, "时间 | "+nowText)
	case "random":
		return s.executeRandom(argsJSON)
	case "info":
		r := s.roundState()
		r.mu.Lock()
		provider := r.infoProvider
		r.mu.Unlock()
		if provider == nil {
			return "ERR|INFO_UNAVAILABLE|信息面未接线"
		}
		return provider()
	case "timeback-start":
		return s.Timeback.ExecuteStart(argsJSON)
	case "timeback-back":
		return s.Timeback.ExecuteBack(argsJSON)
	}
	return "ERR|UNKNOWN_BUILTIN|未知内置工具: " + name
}

// executeRandom random 执行体——生成 [min, max) 随机整数。
func (s *Session) executeRandom(argsJSON string) string {
	min := 0
	max := 0
	hasMin := false
	hasMax := false
	if strings.HasPrefix(argsJSON, "{") {
		var root map[string]json.RawMessage
		if err := json.Unmarshal([]byte(argsJSON), &root); err != nil {
			return "ERR|BAD_ARGS|random 参数损坏"
		}
		if raw, exists := root["min"]; exists {
			var value int
			if err := json.Unmarshal(raw, &value); err == nil {
				min = value
				hasMin = true
			}
		}
		if raw, exists := root["max"]; exists {
			var value int
			if err := json.Unmarshal(raw, &value); err == nil {
				max = value
				hasMax = true
			}
		}
	}
	if !hasMin || !hasMax || min >= max {
		return "ERR|BAD_ARGS|random 需要 min < max 的整数参数"
	}
	picked := min + rand.Intn(max-min)
	return metaHead("random", true, "", -1, []metaField{
		{"min", min},
		{"max", max},
		{"value", picked},
	}, "随机 | "+itoa(min)+".."+itoa(max)+" = "+itoa(picked))
}
