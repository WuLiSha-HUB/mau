package main

import (
	"path/filepath"
	"strings"
	"testing"

	"cathome5/host/session"
)

// TestCommandRouterSessionRollback 会话回滚指令——受理面 / 默认猫 / 每猫端口目标会话。
func TestCommandRouterSessionRollback(t *testing.T) {
	router, _, logs := newCommandRouter(t)
	sess := router.Session
	sess.Context.SetSystemPrompt("系统词")
	sess.Context.AddUserMessage("问题一")
	sess.Context.AddAssistantMessage("回答一")
	sess.Context.AddUserMessage("问题二")
	sess.Context.AddAssistantMessage("回答二")

	if !router.Handle("session.rollback 2") {
		t.Fatal("session.rollback 应识别")
	}
	if got := sess.Context.Count(); got != 3 {
		t.Fatalf("回滚后前文应为 3 条: %d", got)
	}
	// 回执进日志——结果面不在命令返回值里，日志是唯一可见通道
	found := false
	for _, line := range *logs {
		if strings.Contains(line, "rollback | 已回滚到节点 2") {
			found = true
		}
	}
	if !found {
		t.Fatalf("回滚摘要未进日志: %v", *logs)
	}

	// 参数非法——已识别（受理），失败进日志
	if !router.Handle("session.rollback x") {
		t.Fatal("参数非法仍属已识别指令")
	}
	// 裸词不吞——前缀含尾空格
	if router.Handle("session.rollbackx 1") {
		t.Fatal("裸词不应被回滚分支吞掉")
	}

	// 每猫端口——作用于传入会话，不落默认猫
	sess.Context.AddUserMessage("问题三")
	if got := sess.Context.Count(); got != 4 {
		t.Fatalf("默认猫准备态应为 4 条: %d", got)
	}
	dir := t.TempDir()
	ctx := session.NewContext()
	store := session.NewSessionStore(filepath.Join(dir, "cat-b.jsonl"))
	views := session.NewViewStore(filepath.Join(dir, "cat-b.view.jsonl"))
	other := session.NewSession("cat-b", ctx, store, views)
	other.Context.SetSystemPrompt("系统词")
	other.Context.AddUserMessage("问题一")
	other.Context.AddAssistantMessage("回答一")
	other.Context.AddUserMessage("问题二")
	other.Context.AddAssistantMessage("回答二")
	if !router.HandleFor("cat-b", other, "session.rollback 2") {
		t.Fatal("每猫端口回滚应识别")
	}
	if got := other.Context.Count(); got != 3 {
		t.Fatalf("每猫端口回滚应作用于本会话: %d", got)
	}
	if got := sess.Context.Count(); got != 4 {
		t.Fatalf("默认猫会话不应被每猫端口指令改动: %d", got)
	}
}
