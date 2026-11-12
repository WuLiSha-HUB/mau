package llm

import (
	"testing"
	"time"
)

// TestEndpointRole_ManualToggle 手动对调——翻手动角色，窗口清空。
func TestEndpointRole_ManualToggle(t *testing.T) {
	role := &EndpointRole{}
	if role.EffectiveUseBackup() {
		t.Fatalf("初始角色期望主要")
	}
	if role.ToggleManual() != true || !role.EffectiveUseBackup() {
		t.Fatalf("首次对调期望备用")
	}
	if role.ToggleManual() != false || role.EffectiveUseBackup() {
		t.Fatalf("二次对调期望主要")
	}
}

// TestEndpointRole_FailoverWindow 自动窗口——开窗生效 / 手动清窗 / 到期回手动态。
func TestEndpointRole_FailoverWindow(t *testing.T) {
	role := &EndpointRole{}
	role.SetFailover(true)
	if !role.EffectiveUseBackup() {
		t.Fatalf("窗口内期望备用")
	}
	// 窗口内手动切换——清窗，手动角色取窗口角色取反 → 主要
	if role.ToggleManual() != false {
		t.Fatalf("窗口内手动切换期望主要")
	}
	if role.EffectiveUseBackup() {
		t.Fatalf("清窗后期望手动角色主要")
	}
	// 手动切回备用后再开「切回主要」窗口——窗口内按窗口角色
	role.ToggleManual()
	role.SetFailover(false)
	if role.EffectiveUseBackup() {
		t.Fatalf("窗口内期望主要（窗口角色 false）")
	}
	// 到期回手动态（同包可改内部字段模拟过期）
	role.mu.Lock()
	role.failoverUntil = time.Now().Add(-time.Second)
	role.mu.Unlock()
	if !role.EffectiveUseBackup() {
		t.Fatalf("窗口到期期望回手动态（备用）")
	}
}

// TestEndpointRole_NilSafe nil 角色面零动作（不 panic、恒主要）。
func TestEndpointRole_NilSafe(t *testing.T) {
	var role *EndpointRole
	if role.EffectiveUseBackup() || role.ToggleManual() {
		t.Fatalf("nil 角色期望零动作")
	}
	role.SetFailover(true)
	if role.EffectiveUseBackup() {
		t.Fatalf("nil 角色恒主要")
	}
}
