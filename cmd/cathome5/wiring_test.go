package main

import (
	"os"
	"path/filepath"
	"testing"

	"cathome5/host/session"
)

// TestReconcileViewOnLoad 载入期受限重建——视图缺口按前文补差（append-only）且重复调用幂等、文件字节不变。
func TestReconcileViewOnLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "majordomo.view.jsonl")
	views := session.NewViewStore(path)
	// 视图只有首条 user 块；前文 3 条 → 应补 2 块（user + text）
	first := session.Message{Role: session.RoleUser, Content: "一", CreatedAt: 100}
	views.OnUserMessage(first, 0, "")
	messages := []session.Message{
		first,
		{Role: session.RoleAssistant, Content: "答", CreatedAt: 200},
		{Role: session.RoleUser, Content: "二", CreatedAt: 300},
	}

	added, bad := reconcileViewOnLoad(views, messages)
	if added != 2 || bad != 0 {
		t.Fatalf("应补 2 块且零报告，实得 added=%d bad=%d", added, bad)
	}
	if got := len(views.Blocks()); got != 3 {
		t.Fatalf("补差后应 3 块，实得 %d", got)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("补差应落盘: %v", err)
	}
	// 幂等——重复调用零补、文件字节不变
	if added2, bad2 := reconcileViewOnLoad(views, messages); added2 != 0 || bad2 != 0 {
		t.Fatalf("重复调用应零补零报告，实得 added=%d bad=%d", added2, bad2)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	if string(after) != string(again) {
		t.Fatalf("幂等后文件字节应不变")
	}
}

// TestReconcileViewOnLoadEmpty 空前文——零补零报告，视图文件不产生。
func TestReconcileViewOnLoadEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.view.jsonl")
	views := session.NewViewStore(path)
	added, bad := reconcileViewOnLoad(views, nil)
	if added != 0 || bad != 0 {
		t.Fatalf("空前文应零补零报告，实得 added=%d bad=%d", added, bad)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("空前文不应产生视图文件: %v", err)
	}
}
