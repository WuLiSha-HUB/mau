package llm

import (
	"sync"
	"time"
)

// FailoverSeconds 故障转移窗口时长（秒）——C# LlmEndpointRole.FailoverSeconds 逐字。
const FailoverSeconds = 180

// EndpointRole 会话级 LLM 端点角色状态——与 C# Mau.Runtime.LlmEndpointRole 逐字段等价。
//
// 两态来源：① 自动故障转移（窗口内走切换目标，180 秒到期自动回手动态）
// ② 手动切换（仅本会话有效——持久到再次点击，不自动回退）。
// 角色只决定「本次会话消费池内哪个站」，不改变池自身的默认 / 备用标记。
type EndpointRole struct {
	// mu 保护三字段（轮执行线程写窗口 · HTTP 线程读角色）
	mu sync.Mutex
	// manualBackup 手动角色——true=用户点击切到备用（持久；仅本会话有效）
	manualBackup bool
	// failoverBackup 自动故障转移角色——窗口内生效的切换目标（true=备用）
	failoverBackup bool
	// failoverUntil 故障转移窗口截止时刻——到期后窗口失效，回手动态
	failoverUntil time.Time
}

// EffectiveUseBackup 生效角色——是否使用备用站。
//
// 窗口有效期内以窗口角色为准，否则以手动角色为准（C# EffectiveUseBackup 同判）。
func (r *EndpointRole) EffectiveUseBackup() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Now().Before(r.failoverUntil) {
		return r.failoverBackup
	}
	return r.manualBackup
}

// SetFailover 登记一次自动故障转移——开窗 + 指定窗口角色（手动角色保留，窗口到期后重新生效）。
func (r *EndpointRole) SetFailover(useBackup bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failoverBackup = useBackup
	r.failoverUntil = time.Now().Add(FailoverSeconds * time.Second)
}

// ToggleManual 手动切换——对调主要 / 备用（清自动窗口：用户意志优先；仅本会话有效）。
//
// 返回切换后的角色（true=备用）。
func (r *EndpointRole) ToggleManual() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current := r.manualBackup
	if time.Now().Before(r.failoverUntil) {
		current = r.failoverBackup
	}
	r.failoverUntil = time.Time{}
	r.manualBackup = !current
	return r.manualBackup
}
