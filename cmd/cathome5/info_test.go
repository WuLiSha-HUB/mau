package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cathome5/host/config"
	"cathome5/host/session"
	maudb "cathome5/mau/db"
)

// newInfoSession 建立 info 段构造用的最小会话——catKey 空 = 不碰落盘面（无 meta / 前文 / 视图）。
func newInfoSession(t *testing.T) *session.Session {
	t.Helper()
	return session.NewSession("", session.NewContext(), nil, nil)
}

// newInfoWorkspace 建立受控根表——ids 逐个成为根（db 根的可见性只看 id，不看路径）。
func newInfoWorkspace(ids ...string) *config.WorkspaceConfig {
	cfg := &config.WorkspaceConfig{}
	for _, id := range ids {
		cfg.Roots = append(cfg.Roots, config.RootEntry{ID: id, Path: filepath.Join("roots", id), Writable: true})
	}
	return cfg
}

// newInfoDBService 建立临时库根上的条目库服务（不出系统临时目录）。
func newInfoDBService(t *testing.T) (*maudb.Service, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "db")
	svc, err := maudb.New(root)
	if err != nil {
		t.Fatalf("建库服务失败: %v", err)
	}
	return svc, root
}

// infoDBBlock 解出 info 输出的 db 段——整块必须是合法 JSON（info 契约本身也在此受检）。
func infoDBBlock(t *testing.T, payload string) map[string]json.RawMessage {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &root); err != nil {
		t.Fatalf("info 输出不是合法 JSON: %v\n%s", err, payload)
	}
	raw, exists := root["db"]
	if !exists {
		t.Fatalf("info 缺少 db 段:\n%s", payload)
	}
	var block map[string]json.RawMessage
	if err := json.Unmarshal(raw, &block); err != nil {
		t.Fatalf("db 段不是合法 JSON 对象: %v\n%s", err, string(raw))
	}
	return block
}

// infoBlockBool 取布尔字段（缺失 / 类型不符即失败）。
func infoBlockBool(t *testing.T, block map[string]json.RawMessage, key string) bool {
	t.Helper()
	raw, exists := block[key]
	if !exists {
		t.Fatalf("db 段缺少 %s 键: %v", key, block)
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("db 段 %s 不是布尔值: %v", key, err)
	}
	return value
}

// infoBlockString 取字符串字段（缺失 / 类型不符即失败）。
func infoBlockString(t *testing.T, block map[string]json.RawMessage, key string) string {
	t.Helper()
	raw, exists := block[key]
	if !exists {
		t.Fatalf("db 段缺少 %s 键: %v", key, block)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("db 段 %s 不是字符串: %v", key, err)
	}
	return value
}

// infoBlockLibs 取 libs 数组（成功态必须存在）。
func infoBlockLibs(t *testing.T, block map[string]json.RawMessage) []map[string]any {
	t.Helper()
	raw, exists := block["libs"]
	if !exists {
		t.Fatalf("db 段缺少 libs 键: %v", block)
	}
	var libs []map[string]any
	if err := json.Unmarshal(raw, &libs); err != nil {
		t.Fatalf("db 段 libs 不是合法数组: %v", err)
	}
	return libs
}

// TestBuildInfoJSONDBLibs 服务已绑定：libs / summary / rootVisible 齐备且条目计数正确。
func TestBuildInfoJSONDBLibs(t *testing.T) {
	maudb.ClearPolicy()
	t.Cleanup(maudb.ClearPolicy)
	svc, _ := newInfoDBService(t)
	maudb.RegisterCat("majordomo", true, nil)
	if res := svc.Create("demo", "majordomo"); !res.OK {
		t.Fatalf("建库失败: %s %s", res.Code, res.Message)
	}
	if res := svc.Put("demo", "条目一", "正文", "majordomo"); !res.OK {
		t.Fatalf("写条目失败: %s %s", res.Code, res.Message)
	}

	payload := buildInfoJSON(newInfoSession(t), newInfoWorkspace("workspace", "db"), "majordomo", "majordomo", svc)
	t.Logf("info 输出（--v 可见，供卡面留样）: %s", payload)
	block := infoDBBlock(t, payload)
	if ok := infoBlockBool(t, block, "ok"); !ok {
		t.Fatalf("db.ok 应为 true: %v", block)
	}
	summary := infoBlockString(t, block, "summary")
	if !strings.Contains(summary, "库 1") {
		t.Fatalf("summary 未自证库数: %q", summary)
	}
	libs := infoBlockLibs(t, block)
	if len(libs) != 1 {
		t.Fatalf("libs 应 1 条，实得 %d: %v", len(libs), libs)
	}
	if id, _ := libs[0]["id"].(string); id != "demo" {
		t.Fatalf("libs[0].id 应为 demo，实得 %v", libs[0]["id"])
	}
	if entries, _ := libs[0]["entries"].(float64); entries != 1 {
		t.Fatalf("libs[0].entries 应为 1，实得 %v", libs[0]["entries"])
	}
	if !infoBlockBool(t, block, "rootVisible") {
		t.Fatalf("受控根表含 db 根时 rootVisible 应为 true")
	}
}

// TestBuildInfoJSONDBLibsAuthorizationFiltered 授权面：未授权库不出现，摘要显式自证隐藏数。
func TestBuildInfoJSONDBLibsAuthorizationFiltered(t *testing.T) {
	maudb.ClearPolicy()
	t.Cleanup(maudb.ClearPolicy)
	svc, _ := newInfoDBService(t)
	// 建库走空 catID（无猫上下文 = 全放），据此绕开授权面先备数据
	for _, lib := range []string{"public", "private"} {
		if res := svc.Create(lib, ""); !res.OK {
			t.Fatalf("建库失败 %s: %s %s", lib, res.Code, res.Message)
		}
	}
	maudb.RegisterCat("worker", false, []string{"public"})

	payload := buildInfoJSON(newInfoSession(t), newInfoWorkspace("db"), "worker", "worker", svc)
	block := infoDBBlock(t, payload)
	if ok := infoBlockBool(t, block, "ok"); !ok {
		t.Fatalf("db.ok 应为 true: %v", block)
	}
	libs := infoBlockLibs(t, block)
	if len(libs) != 1 {
		t.Fatalf("授权面应只放 public，实得 %d 条: %v", len(libs), libs)
	}
	if id, _ := libs[0]["id"].(string); id != "public" {
		t.Fatalf("libs[0].id 应为 public，实得 %v", libs[0]["id"])
	}
	if summary := infoBlockString(t, block, "summary"); !strings.Contains(summary, "未授权已隐藏") {
		t.Fatalf("摘要未显式自证隐藏库: %q", summary)
	}
}

// TestBuildInfoJSONDBUnbound 服务未绑定：ok=false + 具名 reason，且不伪造 summary / libs。
func TestBuildInfoJSONDBUnbound(t *testing.T) {
	maudb.ClearPolicy()
	t.Cleanup(maudb.ClearPolicy)

	payload := buildInfoJSON(newInfoSession(t), newInfoWorkspace("workspace"), "worker", "worker", nil)
	block := infoDBBlock(t, payload)
	if ok := infoBlockBool(t, block, "ok"); ok {
		t.Fatalf("服务未绑定时 db.ok 应为 false: %v", block)
	}
	if reason := infoBlockString(t, block, "reason"); reason != "数据库服务未绑定" {
		t.Fatalf("reason 应为「数据库服务未绑定」，实得 %q", reason)
	}
	if _, exists := block["summary"]; exists {
		t.Fatalf("服务未绑定时不得输出 summary: %v", block)
	}
	if _, exists := block["libs"]; exists {
		t.Fatalf("服务未绑定时不得输出 libs: %v", block)
	}
	if infoBlockBool(t, block, "rootVisible") {
		t.Fatalf("无 db 根时 rootVisible 应为 false")
	}
}

// TestBuildInfoJSONDBIndexFailure 读面失败：ok=false + 错误码进 reason（不静默省略该段）。
func TestBuildInfoJSONDBIndexFailure(t *testing.T) {
	maudb.ClearPolicy()
	t.Cleanup(maudb.ClearPolicy)
	svc, root := newInfoDBService(t)
	maudb.RegisterCat("majordomo", true, nil)
	if res := svc.Create("demo", "majordomo"); !res.OK {
		t.Fatalf("建库失败: %s %s", res.Code, res.Message)
	}
	if err := os.WriteFile(filepath.Join(root, "_index.json"), []byte("{坏档"), 0o644); err != nil {
		t.Fatalf("写坏注册表失败: %v", err)
	}

	payload := buildInfoJSON(newInfoSession(t), newInfoWorkspace("db"), "majordomo", "majordomo", svc)
	block := infoDBBlock(t, payload)
	if ok := infoBlockBool(t, block, "ok"); ok {
		t.Fatalf("注册表损坏时 db.ok 应为 false: %v", block)
	}
	if reason := infoBlockString(t, block, "reason"); !strings.HasPrefix(reason, "DB_INDEX_UNREADABLE") {
		t.Fatalf("reason 应以 DB_INDEX_UNREADABLE 起头，实得 %q", reason)
	}
}
