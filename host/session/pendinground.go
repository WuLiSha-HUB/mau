package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// 本文件是**轮级续跑现场**（pendingRound）——解决「宿主重启打断在途轮，重启后需人手说继续」。
//
// 生成面：轮首（resetRoundCounters 末尾）写 `sessions/<cat>/<cat>.pending-round.json`；
// 清除面：轮末终态（closeRound 正常收尾 / pauseFinalize 用户中止）删除；
// 消费面：新宿主启动时扫描（仅受控重启场景——见 cmd/cathome5 启动段），续跑一次。
//
// 三条边界（与「崩溃不自动续」的定案一致）：
//  1. 只有「受控重启」才续跑——崩溃 / 人工强杀留下的现场只出声，不自动重发（防重试死循环）；
//  2. 上限 1 次——attempts 落盘，续过一次后再重启即停手并出声（连续两次失败属结构缺陷）；
//  3. 只续「轮已开始但未终态」——轮末清零的轮次不会留下现场（不误触发）。

// PendingRoundPathProvider 轮续跑现场落点解析——catID → 绝对路径（空 = 不持久化，测试默认）。
var PendingRoundPathProvider func(catID string) string

// PendingRound 是一次轮级中断的盘上现场。
type PendingRound struct {
	// CatKey 猫 key（会话目录名）
	CatKey string `json:"cat"`
	// Round 落盘时已进行的工具轮次（0 = 首个请求阶段）
	Round int64 `json:"round"`
	// Phase 落盘时相位（chatPhase* 常量）
	Phase int `json:"phase"`
	// Attempts 已续跑次数（上限 1——超过即停手出声）
	Attempts int `json:"attempts"`
	// DeclIndex 轮首前文条数（续跑后可供人工对照：断点是否在前文里）
	DeclIndex int `json:"declIndex"`
	// StartedAt 轮首时刻（Unix 毫秒）
	StartedAt int64 `json:"startedAt"`
	// UpdatedAt 最后写入时刻（Unix 毫秒）
	UpdatedAt int64 `json:"updatedAt"`
}

// PendingRoundPhaseName 相位名——续跑文本与日志用（未知值给序号，不造语义）。
func PendingRoundPhaseName(phase int) string {
	switch phase {
	case chatPhaseIdle:
		return "空闲"
	case chatPhaseLlmRunning:
		return "LLM 请求中"
	case chatPhaseToolBatchRunning:
		return "工具批执行中"
	case chatPhaseDone:
		return "轮末收尾"
	}
	return "相位 " + itoa(phase)
}

// LoadPendingRound 读取某猫的续跑现场——不存在 / 解析失败返回 false。
func LoadPendingRound(catKey string) (PendingRound, bool) {
	if PendingRoundPathProvider == nil || catKey == "" {
		return PendingRound{}, false
	}
	path := PendingRoundPathProvider(catKey)
	if path == "" {
		return PendingRound{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return PendingRound{}, false
	}
	var info PendingRound
	if err := json.Unmarshal(raw, &info); err != nil {
		return PendingRound{}, false
	}
	if info.CatKey == "" {
		info.CatKey = catKey
	}
	return info, true
}

// writePendingRound 原子落盘（临时文件 + 同卷 rename；目录不存在即建）。
func writePendingRound(info PendingRound) error {
	if PendingRoundPathProvider == nil || info.CatKey == "" {
		return nil
	}
	path := PendingRoundPathProvider(info.CatKey)
	if path == "" {
		return nil
	}
	body, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(body, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// clearPendingRound 删除现场（不存在不算错——轮末清除幂等）。
func clearPendingRound(catKey string) error {
	if PendingRoundPathProvider == nil || catKey == "" {
		return nil
	}
	path := PendingRoundPathProvider(catKey)
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// MarkRoundPending 轮首写现场——记轮次 / 相位 / 前文锚（写盘失败必须可见）。
func (s *Session) MarkRoundPending() {
	if s == nil {
		return
	}
	r := s.roundState()
	r.mu.Lock()
	round := r.round
	phase := r.phase
	started := r.roundStartedAt
	r.mu.Unlock()
	if started.IsZero() {
		started = time.Now()
	}
	declIndex := 0
	if s.Context != nil {
		declIndex = s.Context.Count()
	}
	info := PendingRound{
		CatKey:    s.CatKey,
		Round:     round,
		Phase:     phase,
		Attempts:  0,
		DeclIndex: declIndex,
		StartedAt: started.UnixMilli(),
		UpdatedAt: time.Now().UnixMilli(),
	}
	if err := writePendingRound(info); err != nil {
		logAt(LogModuleHost, 2, "轮续跑现场落盘失败: cat="+s.CatKey+" | "+err.Error(), LogCategoryRestart)
	}
}

// ClearPendingRound 轮末清现场——正常收尾 / 用户中止都走这里（中止不是重启，不续跑）。
func (s *Session) ClearPendingRound() {
	if s == nil {
		return
	}
	if err := clearPendingRound(s.CatKey); err != nil {
		logAt(LogModuleHost, 2, "轮续跑现场清除失败: cat="+s.CatKey+" | "+err.Error(), LogCategoryRestart)
	}
}

// TakePendingRoundResume 取一次续跑资格——attempts 上限 1；返回 true = 可以续跑。
//
// 语义：attempts 先 +1 并落盘再返回——即使续跑后宿主再次重启，也不会第二次自动重发
// （上限判据以盘上为准，不依赖内存）。
func (s *Session) TakePendingRoundResume() (PendingRound, bool) {
	if s == nil {
		return PendingRound{}, false
	}
	info, ok := LoadPendingRound(s.CatKey)
	if !ok {
		return PendingRound{}, false
	}
	if info.Attempts >= 1 {
		return info, false
	}
	info.Attempts++
	info.UpdatedAt = time.Now().UnixMilli()
	if err := writePendingRound(info); err != nil {
		logAt(LogModuleHost, 2, "轮续跑现场落盘失败（attempts）: cat="+s.CatKey+" | "+err.Error(), LogCategoryRestart)
		return info, false
	}
	return info, true
}
