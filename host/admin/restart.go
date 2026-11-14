package admin

import (
	"net/http"
	"strings"
	"sync"

	"cathome5/host/session"
	"cathome5/mau/bricks"
)

// 本文件复现管理端「宿主重启」端点——POST /api/v1/host-restart。
//
// 契约对齐 `mau:CatHome4/CatHome4.Admin/AdminService.Handlers.cs:HandleHostRestartPost`：
// 只登记请求 + 置停机态；接力由主循环闸门在全局空闲后执行（T3），闸门超时自动撤销并出声。
// 执行面在 `cmd/cathome5/restart.go`（起部署继电器 → 观测收尾 → 退出），本文件只管登记面。

// restartResponse 是 host-restart 回执（ok / mode / note 三键）。
type restartResponse struct {
	// OK 恒 true（受理即回 true；执行结果由重启回执另行注入）
	OK bool `json:"ok"`
	// Mode 生效档位（host / incr / full）
	Mode string `json:"mode"`
	// Note 语义说明（原文直读——与 C# 同串）
	Note string `json:"note"`
}

// RestartRequest 是一次重启请求的载荷（mode / target / push / scope 四字段）。
type RestartRequest struct {
	// Mode 档位（full / incr / host）
	Mode string
	// Target 目标运行区绝对路径（空 = 执行时按受控根 mauout5 解析）
	Target string
	// Push 重启后附加说明串（随部署报告一起注入本会话，可空）
	Push string
	// Scope 闸门判据范围——""/"all" = 等全局 Idle（默认，既有语义）；"self" = 只等发起会话自身
	//
	// 为什么要有 self：Note 引擎（未完成计划强制拉起）与「所有会话同时 Idle」互斥——
	// 有 Note 活跃猫时闸门永不通过（判否现场 `ccbp:L3/Mau/F-推理现场/20261114_restart-gate-global-idle/`）。
	// self 越过的只是「别的猫在途轮」，硬等项（发起者自身 / OA 在途 / 视图待推）不变。
	Scope string
}

// RestartState 是宿主重启请求登记面（停机态单一真相源）。
type RestartState struct {
	// mu 保护 request / requested
	mu sync.Mutex
	// request 当前请求载荷（requested=true 时有效）
	request RestartRequest
	// requested 是否处于停机态（true = 新需求一律拒收）
	requested bool
	// intent 重启盘上现场（nil = 不落盘——单测 / 未配置实例）
	intent *RestartIntentStore
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

// SetIntentStore 接线重启盘上现场（装配层一次——登记 / 撤销即写盘）。
func (s *RestartState) SetIntentStore(store *RestartIntentStore) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.intent = store
	s.mu.Unlock()
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

// Request 登记重启请求——置停机态（重复登记以最后一次为准，出声覆盖）。
//
// 空 scope = 全局 Idle（既有语义）；"self" = 只等发起会话自身（见 RestartRequest.Scope 的判否现场）。
func (s *RestartState) Request(mode, target, push string) {
	s.RequestScoped(mode, target, push, "")
}

// RequestScoped 登记重启请求并指定闸门判据范围（scope：空 / all / self）。
func (s *RestartState) RequestScoped(mode, target, push, scope string) {
	if s == nil {
		return
	}
	if mode != "full" && mode != "incr" && mode != "host" {
		mode = "host"
	}
	if scope != "self" {
		scope = ""
	}
	s.mu.Lock()
	s.request = RestartRequest{Mode: mode, Target: target, Push: push, Scope: scope}
	s.requested = true
	store := s.intent
	s.mu.Unlock()
	if store != nil {
		if err := store.Registered(mode, target, push); err != nil {
			s.log(2, "重启现场落盘失败（registered）: "+err.Error())
		}
	}
	scopeText := "全局 Idle"
	if scope == "self" {
		scopeText = "仅发起会话 Idle（scope=self）"
	}
	s.log(1, "宿主重启请求已登记（mode="+mode+"，闸门等 "+scopeText+"）——本轮照常收尾（停机态拒收新输入）")
}

// RequestFromPayload 按请求载荷 JSON 登记（DataBox 全局盒 → 会话批次检测消费）。
func (s *RestartState) RequestFromPayload(payload string) {
	mode, target, push, scope := bricks.DecodeRestartRequest(payload)
	if mode == "" {
		mode = "full"
	}
	s.RequestScoped(mode, target, push, scope)
}

// Snapshot 取当前请求——requested=false 时返回零值。
func (s *RestartState) Snapshot() (RestartRequest, bool) {
	if s == nil {
		return RestartRequest{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.requested {
		return RestartRequest{}, false
	}
	return s.request, true
}

// Withdraw 撤销重启态——清停机理与请求载荷并出声（闸门超时 / 接力拉起失败的唯一出口）。
//
// 不清「人工队列」（跨重启保留）；调用方负责向会话注入失败回执（失败必须可见）。
func (s *RestartState) Withdraw(reason string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.requested {
		s.mu.Unlock()
		return
	}
	mode := s.request.Mode
	s.requested = false
	s.request = RestartRequest{}
	store := s.intent
	s.mu.Unlock()
	if store != nil {
		if err := store.Failed(reason); err != nil {
			s.log(2, "重启现场落盘失败（failed）: "+err.Error())
		}
	}
	s.log(2, "重启请求已撤销（mode="+mode+"）——"+reason)
}

// log 记一条 CONFIG 类观测日志。
func (s *RestartState) log(level int, message string) {
	if s == nil || s.logf == nil {
		return
	}
	s.logf(level, message)
}

// handleHostRestartPost 服务 POST /api/v1/host-restart——登记重启请求（body: {mode?, push?, scope?}）。
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
	scope := strings.TrimSpace(fieldString(fields, "scope"))
	if mode != "host" && mode != "incr" && mode != "full" {
		writeErr(w, "mode 非法（只接受 host / incr / full）: "+mode)
		return
	}
	if scope != "" && scope != "all" && scope != "self" {
		writeErr(w, "scope 非法（只接受 all / self）: "+scope)
		return
	}
	if d.Restart == nil {
		writeErr(w, "重启面未绑定")
		return
	}
	d.Restart.RequestScoped(mode, "", push, scope)
	note := "已登记——全局空闲后执行重启（闸门超时会自动撤销）"
	if scope == "self" {
		note = "已登记——发起会话空闲后执行重启（scope=self：不等待其它猫）"
	}
	writeJSON(w, restartResponse{OK: true, Mode: mode, Note: note})
}
