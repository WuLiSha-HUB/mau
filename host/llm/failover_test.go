package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestPool 写临时端点池（默认 + 备用两条）——池加载走真实解析链。
func writeTestPool(t *testing.T, defaultEndpoint, backupEndpoint string) *Pool {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "llm-api.json")
	raw := `[` +
		`{"schemaVersion":1,"apiConfigId":"11111111-1111-1111-1111-111111111111","displayName":"主站","apiType":"ds","endpoint":"` + defaultEndpoint + `","defaultModel":"m1","isDefault":true,"isBackup":false},` +
		`{"schemaVersion":1,"apiConfigId":"22222222-2222-2222-2222-222222222222","displayName":"备用站","apiType":"ds","endpoint":"` + backupEndpoint + `","defaultModel":"m2","isDefault":false,"isBackup":true}` +
		`]`
	if err := os.WriteFile(configPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("写池失败: %v", err)
	}
	store := LoadPool(configPath, filepath.Join(dir, "llm-api.cfg"))
	if store.Store().LastError() != "" {
		t.Fatalf("池加载失败: %s", store.Store().LastError())
	}
	return store
}

// writeSinglePool 只写默认站（无备用）。
func writeSinglePool(t *testing.T, defaultEndpoint string) *Pool {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "llm-api.json")
	raw := `[{"schemaVersion":1,"apiConfigId":"11111111-1111-1111-1111-111111111111","displayName":"主站","apiType":"ds","endpoint":"` +
		defaultEndpoint + `","defaultModel":"m1","isDefault":true,"isBackup":false}]`
	if err := os.WriteFile(configPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("写池失败: %v", err)
	}
	store := LoadPool(configPath, filepath.Join(dir, "llm-api.cfg"))
	if store.Store().LastError() != "" {
		t.Fatalf("池加载失败: %s", store.Store().LastError())
	}
	return store
}

// pongHandler 探针用假端点——命中哨兵词。
func pongHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"PONG"}}]}`))
	}
}

// failHandler 探针用假端点——指定非 2xx。
func failHandler(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }
}

// newFailoverClient 建带角色面的客户端。
func newFailoverClient(pool *Pool, binding string, role *EndpointRole) *Client {
	return &Client{Pool: pool, APIConfigID: binding, Role: role, Settings: func(string) string { return "" }}
}

// TestTryAutoFailover_NoRoleOrPool 不切态一：无角色面 / 无池。
func TestTryAutoFailover_NoRoleOrPool(t *testing.T) {
	pool := writeTestPool(t, "http://127.0.0.1:1/v1", "http://127.0.0.1:2/v1")
	if newFailoverClient(pool, "", nil).tryAutoFailover(context.Background(), "ERR|x") {
		t.Fatalf("无角色面期望不切")
	}
	if newFailoverClient(nil, "", &EndpointRole{}).tryAutoFailover(context.Background(), "ERR|x") {
		t.Fatalf("无池期望不切")
	}
}

// TestTryAutoFailover_MissingSideOrSame 不切态二：两侧缺一 / 同站。
func TestTryAutoFailover_MissingSideOrSame(t *testing.T) {
	single := writeSinglePool(t, "http://127.0.0.1:1/v1")
	if newFailoverClient(single, "", &EndpointRole{}).tryAutoFailover(context.Background(), "ERR|x") {
		t.Fatalf("无备用站期望不切")
	}
	// 同站：猫绑定身份指向备用条目 → 当前与候选解析到同一条配置
	both := writeTestPool(t, "http://127.0.0.1:1/v1", "http://127.0.0.1:2/v1")
	if newFailoverClient(both, "22222222-2222-2222-2222-222222222222", &EndpointRole{}).tryAutoFailover(context.Background(), "ERR|x") {
		t.Fatalf("同站期望不切")
	}
}

// TestTryAutoFailover_CurrentOK 不切态三：当前站可用（探测命中哨兵）——错误可能出在请求侧。
func TestTryAutoFailover_CurrentOK(t *testing.T) {
	current := httptest.NewServer(pongHandler())
	defer current.Close()
	backup := httptest.NewServer(failHandler(500))
	defer backup.Close()
	pool := writeTestPool(t, current.URL+"/v1", backup.URL+"/v1")
	role := &EndpointRole{}
	client := newFailoverClient(pool, "", role)
	if client.tryAutoFailover(context.Background(), "ERR|TRANSPORT|Timeout|请求发出后 60 秒无回应") {
		t.Fatalf("当前站可用期望不切")
	}
	if role.EffectiveUseBackup() {
		t.Fatalf("不切后角色不应变化")
	}
}

// TestTryAutoFailover_BothDown 不切态四：当前站与候选站均无回应。
func TestTryAutoFailover_BothDown(t *testing.T) {
	current := httptest.NewServer(failHandler(503))
	defer current.Close()
	backup := httptest.NewServer(failHandler(502))
	defer backup.Close()
	pool := writeTestPool(t, current.URL+"/v1", backup.URL+"/v1")
	role := &EndpointRole{}
	client := newFailoverClient(pool, "", role)
	if client.tryAutoFailover(context.Background(), "ERR|TRANSPORT|Timeout|x") {
		t.Fatalf("双方不可用期望不切")
	}
	if role.EffectiveUseBackup() {
		t.Fatalf("不切后角色不应变化")
	}
}

// TestTryAutoFailover_Switch 切换：当前站不可用 + 候选站可用 → 开窗 + 事件文本。
func TestTryAutoFailover_Switch(t *testing.T) {
	current := httptest.NewServer(failHandler(500))
	defer current.Close()
	backup := httptest.NewServer(pongHandler())
	defer backup.Close()
	pool := writeTestPool(t, current.URL+"/v1", backup.URL+"/v1")
	role := &EndpointRole{}
	client := newFailoverClient(pool, "", role)
	var events []Event
	if !client.tryFailover(context.Background(), "ERR|TRANSPORT|Timeout|请求发出后 60 秒无回应", func(ev Event) {
		events = append(events, ev)
	}) {
		t.Fatalf("期望切换")
	}
	if !role.EffectiveUseBackup() {
		t.Fatalf("切换后期望备用生效")
	}
	if len(events) != 2 || events[0].Kind != EventFailover || events[1].Kind != EventRetryResume {
		t.Fatalf("事件序列: %+v", events)
	}
	if !strings.HasPrefix(events[0].Text, "FAILOVER|备用|") {
		t.Fatalf("事件文本: %s", events[0].Text)
	}
	// 窗口生效期间：目标端点解析为备用站（target 走 role）
	if cfg := client.target(); cfg == nil || cfg.DisplayName != "备用站" {
		t.Fatalf("窗口内 target 期望备用站，实际 %+v", cfg)
	}
}
