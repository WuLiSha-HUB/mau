package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
)

// SessionTokens 是会话 token 三项——输入总量 / 命中缓存输入 / 输出。
//
// 未命中与命中率为派生值（不落盘——与「默认值不进落盘」同判据）。
type SessionTokens struct {
	// Prompt 输入 token 总量（含命中缓存部分）
	Prompt int64 `json:"Prompt"`
	// CacheHit 输入中命中缓存部分 token
	CacheHit int64 `json:"CacheHit"`
	// Completion 输出 token
	Completion int64 `json:"Completion"`
}

// Miss 未命中输入——派生值（prompt − cacheHit，不下穿 0）。
func (t SessionTokens) Miss() int64 {
	miss := t.Prompt - t.CacheHit
	if miss < 0 {
		return 0
	}
	return miss
}

// SessionPhases 是本轮六态累计毫秒——idle 不计时故不落盘。
type SessionPhases struct {
	// Wait 等待（本地退避）
	Wait int64 `json:"Wait"`
	// Link 链路（远端等待）
	Link int64 `json:"Link"`
	// Think 思考（远端流）
	Think int64 `json:"Think"`
	// Tool 工具（远端流）
	Tool int64 `json:"Tool"`
	// Run 执行（本地）
	Run int64 `json:"Run"`
	// Reply 回复（远端流）
	Reply int64 `json:"Reply"`
}

// SessionNote 是任务追踪（Note）持久化形态——空计划 = Tasks 空数组。
type SessionNote struct {
	// Tasks 任务列表（nil / 空 = 无计划）
	Tasks []string `json:"Tasks"`
	// Current 当前任务索引（0-based）
	Current int64 `json:"Current"`
	// Done 已完成任务数
	Done int64 `json:"Done"`
}

// SessionToolSpec 是工具面快照条目——会话工具头的持久化形态。
type SessionToolSpec struct {
	// Name 工具名（wire 声明名）
	Name string `json:"Name"`
	// Description 语义化描述（含 order 注记——会话创建时的原文，逐字节冻结）
	Description string `json:"Description"`
	// ParametersJSON 参数 JSON Schema（原样透传；空串 = 无参数工具）
	ParametersJSON string `json:"ParametersJson"`
}

// SessionMeta 是会话元数据——会话自身参数的持久化模型。
//
// 独立于 cat.cfg（每猫配置）· 前文 jsonl（真实前文）· view.jsonl（视图前文）。
type SessionMeta struct {
	// SessionID 会话实例 ID（session.new 生成；仅作显示与元数据定位）
	SessionID string `json:"SessionId"`
	// CatID 猫 key（寻址主键——会话目录名）
	CatID string `json:"CatId"`
	// DisplayName 会话显示名——会话创建时取自 cat.cfg
	DisplayName string `json:"DisplayName"`
	// CreatedAt 会话创建时刻（Unix 毫秒）
	CreatedAt int64 `json:"CreatedAt"`
	// LastActiveAt 最后活跃时刻（= 最后前文变动，含工具结果写入）
	LastActiveAt int64 `json:"LastActiveAt"`
	// ContextCount 前文条数
	ContextCount int64 `json:"ContextCount"`
	// ContextChars 前文总字符数（全消息 Content 长度之和）
	ContextChars int64 `json:"ContextChars"`
	// ContextTokens 前文长度（最近一次请求 prompt token）
	ContextTokens int64 `json:"ContextTokens"`
	// SessionTokens 会话级累计三项——New 到下一个 New
	SessionTokens SessionTokens `json:"SessionTokens"`
	// RoundTokens 轮级累计三项——最后一次请求边界态
	RoundTokens SessionTokens `json:"RoundTokens"`
	// RoundPhases 本轮六态累计毫秒——最后一次请求边界态（idle 不落盘）
	RoundPhases SessionPhases `json:"RoundPhases"`
	// RoundRequests 本轮 API 请求次数（= link 段数）
	RoundRequests int64 `json:"RoundRequests"`
	// RoundTools 本轮工具调用次数
	RoundTools int64 `json:"RoundTools"`
	// RoundElapsedMs 本轮起算 → 最后一次请求结算的耗时毫秒
	RoundElapsedMs int64 `json:"RoundElapsedMs"`
	// Note 任务追踪（Note）——未完成时随重启恢复并继续自动拉起
	Note SessionNote `json:"Note"`
	// Tools 工具面快照——非空 = 会话创建时冻结的工具面，重启恢复时沿用；空 = 旧档 / 首建
	Tools []SessionToolSpec `json:"Tools"`
}

// SessionMetaStore 是会话元数据落盘器——覆盖式原子写 + 读面兜底。
//
// 路径 sessions/<id>/<id>.session.json；文件缺失 = 未初始化；解析失败 = 回落默认 + 日志告警。
type SessionMetaStore struct {
	// path 元数据文件路径
	path string
}

// NewSessionMetaStore 建立元数据落盘器。
func NewSessionMetaStore(path string) *SessionMetaStore { return &SessionMetaStore{path: path} }

// Path 元数据文件路径（调用方诊断用）。
func (s *SessionMetaStore) Path() string { return s.path }

// Load 读取元数据——文件缺失返回 nil（未初始化）；解析失败返回 nil + 告警。
func (s *SessionMetaStore) Load() *SessionMeta {
	if s.path == "" {
		return nil
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if !os.IsNotExist(err) {
			logAt(LogModuleHost, 3, "会话元数据解析失败（按首建处理）: "+err.Error(), LogCategoryChat)
		}
		return nil
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var meta SessionMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		logAt(LogModuleHost, 3, "会话元数据解析失败（按首建处理）: "+err.Error(), LogCategoryChat)
		return nil
	}
	return &meta
}

// Save 写入元数据——覆盖式原子替换（临时文件写全量 → 移动）；失败出声不阻断会话。
func (s *SessionMetaStore) Save(meta *SessionMeta) {
	if s.path == "" || meta == nil {
		return
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(meta); err != nil {
		logAt(LogModuleHost, 3, "会话元数据落盘失败: "+err.Error(), LogCategoryChat)
		return
	}
	body := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	dir := filepath.Dir(s.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			logAt(LogModuleHost, 3, "会话元数据落盘失败: "+err.Error(), LogCategoryChat)
			return
		}
	}
	// 临时名唯一——本面写者跨线程（session.new + 流消费线程每请求结算）
	tmp := s.path + "." + NewSessionID() + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		logAt(LogModuleHost, 3, "会话元数据落盘失败: "+err.Error(), LogCategoryChat)
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		logAt(LogModuleHost, 3, "会话元数据落盘失败: "+err.Error(), LogCategoryChat)
	}
}
