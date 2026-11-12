package config

import (
	"os"
	"path/filepath"
	"testing"
)

// newTestStore 造一个临时端点池存储。
func newTestStore(t *testing.T) *LlmAPIStore {
	t.Helper()
	dir := t.TempDir()
	return LoadLlmAPIStore(filepath.Join(dir, "llm-api.json"), filepath.Join(dir, "secrets", "llm-api.cfg"))
}

// TestLlmAPISavePreservesRoleFlags 编辑普通字段不得静默清默认 / 备用标记。
func TestLlmAPISavePreservesRoleFlags(t *testing.T) {
	store := newTestStore(t)
	cfg := LlmAPIConfig{SchemaVersion: 1, APIConfigID: "11111111-1111-1111-1111-111111111111", DisplayName: "甲", APIType: "dsf4.1", Endpoint: "https://a.example.com/v1", DefaultModel: "m1"}
	if err := store.Save(cfg, "sk-1234567890"); err != nil {
		t.Fatalf("新建失败: %v", err)
	}
	stored, ok := store.TryGet(cfg.APIConfigID)
	if !ok {
		t.Fatal("新建后未命中")
	}
	if !stored.IsDefault {
		t.Fatal("池内首条配置应自动成为默认端点")
	}
	cfg.DisplayName = "甲改"
	if err := store.Save(cfg, ""); err != nil {
		t.Fatalf("编辑失败: %v", err)
	}
	edited, _ := store.TryGet(cfg.APIConfigID)
	if !edited.IsDefault {
		t.Fatal("编辑普通字段后默认标记丢失")
	}
	if edited.DisplayName != "甲改" {
		t.Fatalf("显示名未更新: %q", edited.DisplayName)
	}
	if got := store.Secret(cfg.APIConfigID); got != "sk-1234567890" {
		t.Fatalf("空 apiKey 应保留原密钥，实得 %q", got)
	}
}

// TestLlmAPIRoleExclusivity 默认与备用互斥、备用唯一、取消备用。
func TestLlmAPIRoleExclusivity(t *testing.T) {
	store := newTestStore(t)
	first := LlmAPIConfig{SchemaVersion: 1, APIConfigID: "11111111-1111-1111-1111-111111111111", DisplayName: "甲", APIType: "dsf4.1", Endpoint: "https://a.example.com/v1", DefaultModel: "m1"}
	second := LlmAPIConfig{SchemaVersion: 1, APIConfigID: "22222222-2222-2222-2222-222222222222", DisplayName: "乙", APIType: "dsf4.1", Endpoint: "https://b.example.com/v1", DefaultModel: "m2"}
	if err := store.Save(first, ""); err != nil {
		t.Fatalf("新建甲失败: %v", err)
	}
	if err := store.Save(second, ""); err != nil {
		t.Fatalf("新建乙失败: %v", err)
	}
	if store.SetBackup(first.APIConfigID) {
		t.Fatal("默认端点不得兼为备用")
	}
	if !store.SetBackup(second.APIConfigID) {
		t.Fatal("备用设置失败")
	}
	if !store.SetDefault(second.APIConfigID) {
		t.Fatal("默认切换失败")
	}
	after, _ := store.TryGet(second.APIConfigID)
	if after.IsBackup {
		t.Fatal("设默认后应清除自身备用标记")
	}
	if !store.SetBackup("") {
		t.Fatal("取消备用失败")
	}
	for _, cfg := range store.Configs() {
		if cfg.IsBackup {
			t.Fatalf("取消备用后仍有备用标记: %s", cfg.APIConfigID)
		}
	}
}

// TestLlmAPIDeleteRemovesSecret 删除配置连带删除密钥。
func TestLlmAPIDeleteRemovesSecret(t *testing.T) {
	store := newTestStore(t)
	cfg := LlmAPIConfig{SchemaVersion: 1, APIConfigID: "33333333-3333-3333-3333-333333333333", DisplayName: "丙", APIType: "dsf4.1", Endpoint: "https://c.example.com/v1", DefaultModel: "m3"}
	if err := store.Save(cfg, "sk-abcdefghij"); err != nil {
		t.Fatalf("新建失败: %v", err)
	}
	if !store.Delete(cfg.APIConfigID) {
		t.Fatal("删除失败")
	}
	if store.Secret(cfg.APIConfigID) != "" {
		t.Fatal("删除配置后密钥仍在")
	}
	if _, ok := store.TryGet(cfg.APIConfigID); ok {
		t.Fatal("删除后仍可命中")
	}
}

// TestLlmAPIStoreRoundTrip 落盘后可重新读出（身份序 + 字段完整）。
func TestLlmAPIStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "llm-api.json")
	secretPath := filepath.Join(dir, "secrets", "llm-api.cfg")
	store := LoadLlmAPIStore(configPath, secretPath)
	higher := LlmAPIConfig{SchemaVersion: 1, APIConfigID: "99999999-9999-9999-9999-999999999999", DisplayName: "高", APIType: "NEWAPI", Endpoint: "https://d.example.com/v1", DefaultModel: "m4"}
	lower := LlmAPIConfig{SchemaVersion: 1, APIConfigID: "11111111-1111-1111-1111-111111111111", DisplayName: "低", APIType: "ds", Endpoint: "https://e.example.com/v1", DefaultModel: "m5"}
	if err := store.Save(higher, "k1"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := store.Save(lower, "k2"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	reloaded := LoadLlmAPIStore(configPath, secretPath)
	configs := reloaded.Configs()
	if len(configs) != 2 {
		t.Fatalf("重读条数不符: %d", len(configs))
	}
	if configs[0].APIConfigID != lower.APIConfigID {
		t.Fatalf("重读未按身份序: %s", configs[0].APIConfigID)
	}
	if configs[1].APIType != "newapi" {
		t.Fatalf("apiType 未归一为小写: %q", configs[1].APIType)
	}
	if reloaded.Secret(lower.APIConfigID) != "k2" {
		t.Fatal("密钥未落盘或未读回")
	}
	if _, err := os.Stat(configPath + ".last"); err != nil {
		t.Fatalf("应保留 .last 备份: %v", err)
	}
}

// TestMaskAPIKeyAndGUID 掩码与 GUID 归一的口径。
func TestMaskAPIKeyAndGUID(t *testing.T) {
	if got := MaskAPIKey("12345678"); got != "****" {
		t.Fatalf("≤8 应全掩，实得 %q", got)
	}
	if got := MaskAPIKey("sk-1234567890"); got != "sk-1****7890" {
		t.Fatalf("掩码口径不符: %q", got)
	}
	if got := ParseGUID("{216C744D-791D-4422-A996-93511D3985CB}"); got != "216c744d-791d-4422-a996-93511d3985cb" {
		t.Fatalf("GUID 归一失败: %q", got)
	}
	if got := ParseGUID("216c744d791d4422a99693511d3985cb"); got != "216c744d-791d-4422-a996-93511d3985cb" {
		t.Fatalf("无短横形态归一失败: %q", got)
	}
	if got := ParseGUID("not-a-guid"); got != "" {
		t.Fatalf("非法 GUID 应返回空串，实得 %q", got)
	}
	if !IsZeroGUID("00000000-0000-0000-0000-000000000000") {
		t.Fatal("全零 GUID 应判零值")
	}
}
