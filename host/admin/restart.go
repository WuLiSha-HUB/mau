package admin

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"cathome5/host/session"
)

// 本文件复现管理端「宿主重启」端点——POST /api/v1/host-restart。
//
// 契约对齐 `mau:CatHome4/CatHome4.Admin/AdminService.Handlers.cs:HandleHostRestartPost`：
// 只登记请求 + 置停机态，本处不起进程（接力由主循环闸门在全局空闲后执行，闸门超时自动撤销）。
// Go 侧差异（P5 外壳未接线）：闸门到点一律撤销并出声——停机态无上限会无限期拒收输入（A262 同因）。

// restartGateWait 是闸门等待上限——判据持续不满足即撤销重启态。
const restartGateWait = 30 * time.Second

// restartResponse 是 host-restart 回执（ok / mode / note 三键）。
type restartResponse struct {
	// OK 恒 true（受理即回 true；执行结果由重启回执另行注入）
	OK bool `json:"ok"`
	// Mode 生效档位（host / incr / full）
	Mode string `json:"mode"`
	// Note 语义说明（原文直读——与 C# 同串）
	Note string `json:"note"`
}

// RestartState 是宿主重启请求登记面（停机态单一真相源）。
type RestartState struct {
	// mu 保护 mode / push / requested / timer
	mu sync.Mutex
	// mode 生效档位
	mode string
	// push 重启后附加说明串
	push string
	// requested 是否处于停机态（true = 新需求一律拒收）
	requested bool
	// timer 闸门超时撤销定时器
	timer *time.Timer
	// logf 观测日志出口
	logf func(level int, message string)
}

// NewRestartState 构造重启登记面，并把停机态判定接到会话层钩子。
//
// 装配层唯一调用点：cmd/cathome5 起服链——会话层据此在新需求入队 / 视图推送时拒收。
func NewRestartState(logf func(level int, message string)) *RestartState {
	state := &RestartState{logf: logf}
	session.HostRestarting = state.Requested
	return state
}

// Requested 停机态判定（会话层钩子消费）。
func (s *RestartState) Requested() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requested
}

// Request 登记重启请求——置停机态 + 起闸门超时定时器。
func (s *RestartState) Request(mode, push string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.mode = mode
	s.push = push
	s.requested = true
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(restartGateWait, func() { s.withdraw() })
	s.mu.Unlock()
	s.log(1, "管理面板请求宿主重启（mode="+mode+"）——等待全局空闲后执行")
}

// withdraw 撤销停机态并出声（执行面未接线时这是唯一出口——不留静默拒收）。
func (s *RestartState) withdraw() {
	s.mu.Lock()
	if !s.requested {
		s.mu.Unlock()
		return
	}
	mode := s.mode
	s.requested = false
	s.timer = nil
	s.mu.Unlock()
	s.log(2, "重启请求已撤销（闸门超时 "+restartGateWait.String()+"，mode="+mode+"）——"+
		"执行面归 P5 外壳（整机切换未接线）；宿主按当前进程继续服务")
}

// log 记一条 CONFIG 类观测日志。
func (s *RestartState) log(level int, message string) {
	if s == nil || s.logf == nil {
		return
	}
	s.logf(level, message)
}

// handleHostRestartPost 服务 POST /api/v1/host-restart——登记重启请求（body: {mode?, push?}）。
func (d *Deps) handleHostRestartPost(w http.ResponseWriter, r *http.Request) {
	fields, ok := readBodyFields(r)
	if !ok {
		writeErr(w, "body 非 JSON")
		return
	}
	mode := "host"
	if value := strings.TrimSpace(fieldString(fields, "mode")); value != "" {
		mode = value
	}
	push := "管理面板请求重启"
	if value := strings.TrimSpace(fieldString(fields, "push")); value != "" {
		push = value
	}
	if mode != "host" && mode != "incr" && mode != "full" {
		writeErr(w, "mode 非法（只接受 host / incr / full）: "+mode)
		return
	}
	if d.Restart == nil {
		writeErr(w, "重启面未绑定")
		return
	}
	d.Restart.Request(mode, push)
	writeJSON(w, restartResponse{OK: true, Mode: mode, Note: "已登记——全局空闲后执行重启（闸门超时会自动撤销）"})
}
