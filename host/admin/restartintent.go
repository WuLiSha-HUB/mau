package admin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// 本文件是宿主重启链的**盘上现场**（data:runtime/restart-intent.json）——
// 重启是进程级不可逆动作，过程必须留痕（失败必须可见）：
//
//	registered → launching → done / failed / stale
//
//   - registered：登记面受理（工具回执 / 面板按钮同一入口）——请求已排队，等全局 Idle 闸门；
//   - launching：闸门已起接力者（含 relayPid / oldPid）——旧宿主即将退出；
//   - done：新宿主启动时结算（newPid / version / 总耗时）——本次重启链完整走完；
//   - failed：闸门超时 / 接力拉起失败 / 目标占用（reason 出声）——宿主继续服务，运行区未动；
//   - stale：intent 停在 registered / launching 而没人接手（relay 超时退出后人工启动）——
//     重启链未完成，**不得据此续跑会话**（只有受控重启才续）。
//
// 与 `--majordomopush` 的分工：push 是「一次性会话注入」（收件人看得见）；本文件是「盘上审计面」
// （下一轮 / 人工能查「上一次重启到哪一步」）。两者由同一份部署报告派生，不互相替代。

// 重启现场阶段常量（单一真相源——写入方与读取方共用）。
const (
	// RestartPhaseRegistered 登记已受理（等待全局 Idle 闸门）
	RestartPhaseRegistered = "registered"
	// RestartPhaseLaunching 接力已拉起（旧宿主即将退出）
	RestartPhaseLaunching = "launching"
	// RestartPhaseDone 新宿主已接手并结算
	RestartPhaseDone = "done"
	// RestartPhaseFailed 未执行（闸门超时 / 接力失败 / 目标占用）
	RestartPhaseFailed = "failed"
	// RestartPhaseStale 现场停在中间态且无接力回执启动（重启链未完成）
	RestartPhaseStale = "stale"
)

// RestartIntent 是一次重启的盘上现场（单文件、单条——新一轮登记即覆盖）。
type RestartIntent struct {
	// Phase 阶段（registered / launching / done / failed / stale）
	Phase string `json:"phase"`
	// Mode 档位（full / incr / host）
	Mode string `json:"mode"`
	// Target 目标运行区绝对路径（空 = 登记时未指定，执行时解析）
	Target string `json:"target"`
	// Push 附加说明串（随部署报告注入本会话，可空）
	Push string `json:"push,omitempty"`
	// OldPID 发起重启的旧宿主 pid（launching 起有效）
	OldPID int `json:"oldPid,omitempty"`
	// RelayPID 部署接力者 pid（launching 起有效）
	RelayPID int `json:"relayPid,omitempty"`
	// NewPID 结算时的新宿主 pid（done 有效）
	NewPID int `json:"newPid,omitempty"`
	// Version 结算时的运行区版本（done 有效）
	Version string `json:"version,omitempty"`
	// DeployExit 接力部署退出码（0 替换成功 / 2 已回退 / 3 未替换 / 1 需人工）——relay 结算时写
	// 不用 omitempty：0 是「部署成功」的正面值，必须显式出现（否则现场只能靠 deployReported 推断）
	DeployExit int `json:"deployExit"`
	// DeployReported 部署结果已上报（false = 旧 relay 未写 / 未走到结算——零值 0 与「未写」不可区分）
	DeployReported bool `json:"deployReported,omitempty"`
	// Reason 失败 / stale 原因（出声面——空 = 无）
	Reason string `json:"reason,omitempty"`
	// RequestedAt 登记时刻（Unix 毫秒）
	RequestedAt int64 `json:"requestedAt"`
	// UpdatedAt 最后写入时刻（Unix 毫秒）
	UpdatedAt int64 `json:"updatedAt"`
	// ElapsedMs 登记到结算的总耗时（done 有效）
	ElapsedMs int64 `json:"elapsedMs,omitempty"`
}

// RestartIntentStore 重启现场读写（单文件；原子写 = 临时文件 + 同卷 rename）。
type RestartIntentStore struct {
	// path 现场文件绝对路径
	path string
	// mu 串行化读写（登记面与闸门同进程不同 goroutine）
	mu sync.Mutex
}

// NewRestartIntentStore 构造现场读写器（path 空 = 不可用，各写方法返回错误）。
func NewRestartIntentStore(path string) *RestartIntentStore {
	return &RestartIntentStore{path: path}
}

// Path 现场文件路径（空 = 未配置）。
func (s *RestartIntentStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Read 读取现场——不存在 / 解析失败返回 false（调用方按「无现场」处理）。
func (s *RestartIntentStore) Read() (RestartIntent, bool) {
	if s == nil || s.path == "" {
		return RestartIntent{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked()
}

// readLocked 读盘（调用方持锁）。
func (s *RestartIntentStore) readLocked() (RestartIntent, bool) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return RestartIntent{}, false
	}
	var intent RestartIntent
	if err := json.Unmarshal(raw, &intent); err != nil {
		return RestartIntent{}, false
	}
	if intent.Phase == "" {
		return RestartIntent{}, false
	}
	return intent, true
}

// write 原子落盘（临时文件 + rename；目录不存在即建）。
func (s *RestartIntentStore) write(intent RestartIntent) error {
	if s == nil || s.path == "" {
		return os.ErrNotExist
	}
	intent.UpdatedAt = time.Now().UnixMilli()
	body, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(body, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Registered 登记受理——写入请求载荷，stage=registered（覆盖上一条现场）。
func (s *RestartIntentStore) Registered(mode, target, push string) error {
	if s == nil || s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UnixMilli()
	return s.write(RestartIntent{
		Phase:       RestartPhaseRegistered,
		Mode:        mode,
		Target:      target,
		Push:        push,
		RequestedAt: now,
		UpdatedAt:   now,
	})
}

// Launching 接力已拉起——补 relayPid / oldPid（其余字段沿用登记面）。
func (s *RestartIntentStore) Launching(oldPID, relayPID int) error {
	if s == nil || s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	intent, ok := s.readLocked()
	if !ok {
		intent = RestartIntent{Phase: RestartPhaseRegistered, RequestedAt: time.Now().UnixMilli()}
	}
	intent.Phase = RestartPhaseLaunching
	intent.OldPID = oldPID
	intent.RelayPID = relayPID
	intent.Reason = ""
	return s.write(intent)
}

// SetDeployResult 接力部署结算——relay 写（起新实例之前预写一次，收尾时补写终值）。
//
// 为什么要有这一条：deploy 失败后 relay 仍会起新实例（旧宿主已退出，服务不能停），
// `phase=done` 只表示「重启链走完」，不含「运行区是否被替换」——2026-11-14 17:11 判例：
// exit=3 未替换 + intent done（`L3/Mau/F-推理现场/20261114_deploy-fail-masked-by-intent-done/`）。
//
// 两段写：起新实例之前（新宿主启动即 Settle 判 done，之后写会被旧判据挡掉——18:03 判例 deployExit 缺失）
// + 收尾补写终值（探活失败回退 2）。phase 本身不改写；done 且未替换时补 reason。
func (s *RestartIntentStore) SetDeployResult(exitCode int) error {
	if s == nil || s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	intent, ok := s.readLocked()
	if !ok {
		return nil
	}
	intent.DeployExit = exitCode
	intent.DeployReported = true
	// done 之后 relay 仍可能补写（终值 = 探活失败回退 / 人工介入）——原因同步补齐。
	if intent.Phase == RestartPhaseDone && exitCode != 0 && intent.Reason == "" {
		intent.Reason = "部署未替换（deployExit=" + strconv.Itoa(exitCode) + "）——运行区仍是旧版本"
	}
	return s.write(intent)
}

// Failed 未执行——宿主继续服务、运行区未动（reason 出声；现场保留请求载荷便于对照）。
func (s *RestartIntentStore) Failed(reason string) error {
	if s == nil || s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	intent, ok := s.readLocked()
	if !ok {
		return nil
	}
	intent.Phase = RestartPhaseFailed
	intent.Reason = reason
	return s.write(intent)
}

// Settle 新宿主启动时结算——phase=done（本次重启链走完）或 stale（无人接手）。
//
// 只在现场停在 registered / launching 时改写（已结算的现场不动——保留第一次结论）。
func (s *RestartIntentStore) Settle(phase, version, reason string, newPID int) (RestartIntent, bool) {
	if s == nil || s.path == "" {
		return RestartIntent{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	intent, ok := s.readLocked()
	if !ok {
		return RestartIntent{}, false
	}
	if intent.Phase != RestartPhaseRegistered && intent.Phase != RestartPhaseLaunching {
		return intent, false
	}
	intent.Phase = phase
	if reason != "" {
		intent.Reason = reason
	}
	if phase == RestartPhaseDone {
		intent.NewPID = newPID
		intent.Version = version
		if intent.RequestedAt > 0 {
			intent.ElapsedMs = time.Now().UnixMilli() - intent.RequestedAt
		}
		// 部署未替换 = 重启链走完但产物没换——done 保留（进程确实换了），原因必须带出（失败可见）。
		if intent.DeployReported && intent.DeployExit != 0 {
			if intent.Reason == "" {
				intent.Reason = "部署未替换（deployExit=" + strconv.Itoa(intent.DeployExit) + "）——运行区仍是旧版本"
			}
		}
	}
	if err := s.write(intent); err != nil {
		return intent, false
	}
	return intent, true
}
