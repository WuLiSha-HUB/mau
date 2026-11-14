package admin

import (
	"path/filepath"
	"testing"
)

// TestRestartIntentLifecycle 重启现场——registered → launching → done 三阶段 + 已结算不再改写。
func TestRestartIntentLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime", "restart-intent.json")
	store := NewRestartIntentStore(path)
	if _, ok := store.Read(); ok {
		t.Fatal("无文件时不应有现场")
	}
	if err := store.Registered("incr", "", "备注"); err != nil {
		t.Fatalf("registered 写盘失败: %v", err)
	}
	intent, ok := store.Read()
	if !ok || intent.Phase != RestartPhaseRegistered || intent.Mode != "incr" || intent.Push != "备注" {
		t.Fatalf("registered 现场不符: ok=%v %+v", ok, intent)
	}
	if intent.RequestedAt == 0 || intent.UpdatedAt == 0 {
		t.Fatalf("时间戳未写入: %+v", intent)
	}
	if err := store.Launching(100, 200); err != nil {
		t.Fatalf("launching 写盘失败: %v", err)
	}
	intent, _ = store.Read()
	if intent.Phase != RestartPhaseLaunching || intent.OldPID != 100 || intent.RelayPID != 200 {
		t.Fatalf("launching 现场不符: %+v", intent)
	}
	if intent.Mode != "incr" || intent.Push != "备注" {
		t.Fatalf("launching 应保留登记载荷: %+v", intent)
	}
	settled, changed := store.Settle(RestartPhaseDone, "0.36.4", "", 999)
	if !changed || settled.Phase != RestartPhaseDone || settled.NewPID != 999 || settled.Version != "0.36.4" {
		t.Fatalf("done 结算不符: changed=%v %+v", changed, settled)
	}
	if settled.ElapsedMs < 0 {
		t.Fatalf("耗时不应为负: %+v", settled)
	}
	// 已结算——再调不复写（保留第一次结论）
	if _, changed := store.Settle(RestartPhaseStale, "", "x", 1); changed {
		t.Fatal("已结算现场不应再改写")
	}
}

// TestRestartIntentFailed 撤销面——failed 带原因，载荷保留便于对照。
func TestRestartIntentFailed(t *testing.T) {
	store := NewRestartIntentStore(filepath.Join(t.TempDir(), "restart-intent.json"))
	if err := store.Failed("无现场时不应写"); err != nil {
		t.Fatalf("无现场 failed 不应报错: %v", err)
	}
	if _, ok := store.Read(); ok {
		t.Fatal("无现场时 failed 不应造文件")
	}
	if err := store.Registered("full", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Failed("闸门等待超时（30 s）"); err != nil {
		t.Fatal(err)
	}
	intent, ok := store.Read()
	if !ok || intent.Phase != RestartPhaseFailed || intent.Reason != "闸门等待超时（30 s）" {
		t.Fatalf("failed 现场不符: ok=%v %+v", ok, intent)
	}
	if intent.Mode != "full" {
		t.Fatalf("failed 应保留 mode: %+v", intent)
	}
	// stale 结算——launching 停在中间态
	if err := store.Registered("host", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Launching(7, 8); err != nil {
		t.Fatal(err)
	}
	stale, changed := store.Settle(RestartPhaseStale, "", "无接力回执启动", 0)
	if !changed || stale.Phase != RestartPhaseStale || stale.Reason == "" {
		t.Fatalf("stale 结算不符: changed=%v %+v", changed, stale)
	}
}

// TestRestartStateWritesIntent 登记面 / 撤销面落盘——Request 写 registered，Withdraw 写 failed。
func TestRestartStateWritesIntent(t *testing.T) {
	store := NewRestartIntentStore(filepath.Join(t.TempDir(), "restart-intent.json"))
	state := NewRestartState(nil)
	state.SetIntentStore(store)
	state.Request("incr", "", "从工具登记")
	intent, ok := store.Read()
	if !ok || intent.Phase != RestartPhaseRegistered || intent.Mode != "incr" || intent.Push != "从工具登记" {
		t.Fatalf("Request 应写 registered: ok=%v %+v", ok, intent)
	}
	state.Withdraw("接力启动失败")
	intent, ok = store.Read()
	if !ok || intent.Phase != RestartPhaseFailed || intent.Reason != "接力启动失败" {
		t.Fatalf("Withdraw 应写 failed: ok=%v %+v", ok, intent)
	}
}

// TestRestartStateScope 闸门范围归一——self 保留；空 / all / 非法一律回到全局（空串）。
func TestRestartStateScope(t *testing.T) {
	state := NewRestartState(nil)
	state.RequestScoped("incr", "", "", "self")
	if req, ok := state.Snapshot(); !ok || req.Scope != "self" {
		t.Fatalf("self 应保留：ok=%v %+v", ok, req)
	}
	state.Withdraw("测试结束")
	state.RequestScoped("incr", "", "", "all")
	if req, ok := state.Snapshot(); !ok || req.Scope != "" {
		t.Fatalf("all 应归一为空（全局语义）：ok=%v %+v", ok, req)
	}
	state.Withdraw("测试结束")
	state.RequestScoped("incr", "", "", "bogus")
	if req, ok := state.Snapshot(); !ok || req.Scope != "" {
		t.Fatalf("非法值应归一为空：ok=%v %+v", ok, req)
	}
}

// TestRestartIntentDeployResult 部署结论落盘——done 只表示重启链走完，部署未替换必须带原因出声。
//
// 判例 2026-11-14 17:11：deploy exit=3（运行区未替换）+ intent done → 被误读成部署成功。
func TestRestartIntentDeployResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart-intent.json")
	store := NewRestartIntentStore(path)
	// 无现场时写部署结论——不报错、不造文件
	if err := store.SetDeployResult(3); err != nil {
		t.Fatalf("无现场写入不应报错: %v", err)
	}
	if _, ok := store.Read(); ok {
		t.Fatal("无现场时不应造文件")
	}
	// 部署失败：exit=3 → done 带原因
	if err := store.Registered("incr", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Launching(11, 22); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeployResult(3); err != nil {
		t.Fatal(err)
	}
	intent, _ := store.Read()
	if !intent.DeployReported || intent.DeployExit != 3 {
		t.Fatalf("部署结论未落盘: %+v", intent)
	}
	settled, changed := store.Settle(RestartPhaseDone, "0.36.4", "", 33)
	if !changed || settled.Phase != RestartPhaseDone {
		t.Fatalf("done 结算不符: changed=%v %+v", changed, settled)
	}
	if settled.Reason == "" {
		t.Fatalf("部署未替换的 done 必须带原因（失败可见）: %+v", settled)
	}
	// 已结算现场——部署结论允许补写终值（phase 不动），done + 未替换时 reason 必须补齐
	if err := store.SetDeployResult(2); err != nil {
		t.Fatalf("已结算后写入不应报错: %v", err)
	}
	after, _ := store.Read()
	if after.DeployExit != 2 || after.Phase != RestartPhaseDone {
		t.Fatalf("已结算现场应允许补写 deployExit 且 phase 不变: %+v", after)
	}
	if after.Reason == "" {
		t.Fatalf("补写未替换结论后 reason 必须可见: %+v", after)
	}
	// 部署成功：exit=0 → done 不带原因
	okPath := filepath.Join(t.TempDir(), "restart-intent.json")
	okStore := NewRestartIntentStore(okPath)
	if err := okStore.Registered("incr", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := okStore.Launching(44, 55); err != nil {
		t.Fatal(err)
	}
	if err := okStore.SetDeployResult(0); err != nil {
		t.Fatal(err)
	}
	okSettled, changed := okStore.Settle(RestartPhaseDone, "0.36.4", "", 66)
	if !changed || okSettled.Reason != "" {
		t.Fatalf("部署成功的 done 不应带原因: changed=%v %+v", changed, okSettled)
	}
}
