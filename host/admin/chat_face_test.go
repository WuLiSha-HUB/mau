package admin

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"cathome5/host/config"
	"cathome5/host/session"
)

// newChatFaceStore 构造带默认 + 备用两条端点的临时池。
func newChatFaceStore(t *testing.T) (*config.LlmAPIStore, string, string) {
	t.Helper()
	dir := t.TempDir()
	store := config.LoadLlmAPIStore(filepath.Join(dir, "llm-api.json"), filepath.Join(dir, "llm-api.cfg"))
	primaryID := "11111111-2222-3333-4444-555555555555"
	backupID := "66666666-7777-8888-9999-aaaaaaaaaaaa"
	if err := store.Save(config.LlmAPIConfig{SchemaVersion: 1, APIConfigID: primaryID, DisplayName: "主站", APIType: "openai", Endpoint: "https://primary.example.com/v1/chat/completions", DefaultModel: "m-primary"}, "sk-primary"); err != nil {
		t.Fatalf("主站写入失败：%v", err)
	}
	// 首条自动成默认
	if err := store.Save(config.LlmAPIConfig{SchemaVersion: 1, APIConfigID: backupID, DisplayName: "备站", APIType: "openai", Endpoint: "https://backup.example.com/v1/chat/completions", DefaultModel: "m-backup"}, "sk-backup"); err != nil {
		t.Fatalf("备站写入失败：%v", err)
	}
	if !store.SetBackup(backupID) {
		t.Fatalf("备站标记失败")
	}
	return store, primaryID, backupID
}

// TestBuildAPIRoleJSON 端点角色信息——主要 / 备用 / 覆盖三态。
func TestBuildAPIRoleJSON(t *testing.T) {
	store, primaryID, _ := newChatFaceStore(t)
	sess := session.NewSession("", session.NewContext(), nil, nil)

	payload := decodePayload(t, BuildAPIRoleJSON(sess, store, ""))
	if payload["role"] != "主要" || payload["primary"] != "m-primary" || payload["backup"] != "m-backup" {
		t.Fatalf("主要态不符 %+v", payload)
	}
	if payload["activeName"] != "主站" || payload["activeModel"] != "m-primary" {
		t.Fatalf("主要态生效面不符 %+v", payload)
	}

	sess.ToggleAPIRole()
	payload = decodePayload(t, BuildAPIRoleJSON(sess, store, primaryID))
	if payload["role"] != "备用" || payload["activeName"] != "备站" || payload["activeModel"] != "m-backup" {
		t.Fatalf("备用态不符 %+v", payload)
	}

	sess.SetModelOverride("m-override")
	payload = decodePayload(t, BuildAPIRoleJSON(sess, store, primaryID))
	if payload["override"] != "m-override" || payload["activeModel"] != "m-override" {
		t.Fatalf("覆盖态不符 %+v", payload)
	}

	// 池未绑定——字段为空串（前端回落占位符）
	payload = decodePayload(t, BuildAPIRoleJSON(nil, nil, ""))
	if payload["role"] != "主要" || payload["primary"] != "" || payload["backup"] != "" {
		t.Fatalf("空池态不符 %+v", payload)
	}
}

// TestBuildEffortJSON 思考强度读面——本猫覆盖 + 全局值。
func TestBuildEffortJSON(t *testing.T) {
	dir := t.TempDir()
	writeCatCfg(t, dir, "majordomo", "high")
	deps := &Deps{DataRoot: dir}
	payload := decodePayload(t, deps.BuildEffortJSON("majordomo"))
	if payload["effort"] != "high" || payload["global"] == "" {
		t.Fatalf("读面不符 %+v", payload)
	}
	// 非法 cat / 空覆盖
	payload = decodePayload(t, deps.BuildEffortJSON("bad/key"))
	if payload["effort"] != "" {
		t.Fatalf("非法 cat 期望空 effort，实际 %+v", payload)
	}
}

// TestSetEffortJSON 思考强度写面——合法 / 非法 / 清空 / Apply 生效回调。
func TestSetEffortJSON(t *testing.T) {
	dir := t.TempDir()
	writeCatCfg(t, dir, "majordomo", "")
	applied := ""
	deps := &Deps{DataRoot: dir, Apply: func(catKey string, cfg *config.CatCfg) {
		applied = catKey + "=" + cfg.ReasoningEffort
	}}
	payload := decodePayload(t, deps.SetEffortJSON("majordomo", "low"))
	if payload["ok"] != true || payload["effort"] != "low" || applied != "majordomo=low" {
		t.Fatalf("写面不符 %+v（applied=%q）", payload, applied)
	}
	payload = decodePayload(t, deps.SetEffortJSON("majordomo", "bogus"))
	if payload["ok"] != false || !strings.Contains(payload["error"].(string), "reasoningEffort 非法") {
		t.Fatalf("非法值回执不符 %+v", payload)
	}
	payload = decodePayload(t, deps.SetEffortJSON("majordomo", ""))
	if payload["ok"] != true || payload["effort"] != "" {
		t.Fatalf("清空回执不符 %+v", payload)
	}
	payload = decodePayload(t, deps.SetEffortJSON("bad/key", "low"))
	if payload["ok"] != false {
		t.Fatalf("非法 cat 期望失败，实际 %+v", payload)
	}
}

// TestBuildAPIModelsJSON 模型清单——成功 / 无端点 / 上游失败三态。
func TestBuildAPIModelsJSON(t *testing.T) {
	base := newProbeServer(`{"data":[{"id":"m1"},{"id":"m2"}]}`, 200)
	store, primaryID, _ := newChatFaceStore(t)
	// 把主站端点指到 mock（直接改池文件再重载）
	cfg := config.LlmAPIConfig{SchemaVersion: 1, APIConfigID: primaryID, DisplayName: "主站", APIType: "openai", Endpoint: base + "/v1/chat/completions", DefaultModel: "m1"}
	if err := store.Save(cfg, ""); err != nil {
		t.Fatalf("主站改写失败：%v", err)
	}
	payload := decodePayload(t, BuildAPIModelsJSON(store, primaryID, false, ""))
	if payload["ok"] != true || payload["current"] != "m1" {
		t.Fatalf("成功态不符 %+v", payload)
	}
	models := payload["models"].([]any)
	if len(models) != 2 || models[0] != "m1" || models[1] != "m2" {
		t.Fatalf("模型清单不符 %+v", models)
	}

	// 空池——当前生效端点未解析
	empty := config.LoadLlmAPIStore(filepath.Join(t.TempDir(), "llm-api.json"), filepath.Join(t.TempDir(), "llm-api.cfg"))
	payload = decodePayload(t, BuildAPIModelsJSON(empty, "", false, ""))
	if payload["ok"] != false || !strings.Contains(payload["error"].(string), "当前生效端点未解析") {
		t.Fatalf("空池回执不符 %+v", payload)
	}

	// 上游 500——探取失败
	failBase := newProbeServer(`{"error":"boom"}`, 500)
	if err := store.Save(config.LlmAPIConfig{SchemaVersion: 1, APIConfigID: primaryID, DisplayName: "主站", APIType: "openai", Endpoint: failBase + "/v1/chat/completions", DefaultModel: "m1"}, ""); err != nil {
		t.Fatalf("主站改写失败：%v", err)
	}
	payload = decodePayload(t, BuildAPIModelsJSON(store, primaryID, false, ""))
	if payload["ok"] != false || !strings.Contains(payload["error"].(string), "模型清单探取失败") {
		t.Fatalf("上游失败回执不符 %+v", payload)
	}
}

// writeCatCfg 写一份最小 cat.cfg（思考强度字段）。
func writeCatCfg(t *testing.T, dataRoot, catKey, effort string) {
	t.Helper()
	path := filepath.Join(dataRoot, "sessions", catKey, "cat.cfg")
	cfg := &config.CatCfg{ID: catKey, DisplayName: catKey, ReasoningEffort: effort}
	if err := config.SaveCatCfg(path, cfg); err != nil {
		t.Fatalf("cat.cfg 写入失败：%v", err)
	}
}

// decodePayload 解析 JSON 回执为 map。
func decodePayload(t *testing.T, raw string) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("回执非 JSON：%v（%s）", err, raw)
	}
	return payload
}
