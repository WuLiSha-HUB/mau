package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"cathome5/host/config"
)

// TestDeriveProbeURLs 端点推导——去 /chat/completions 得模型基址，站点根再去 /v1。
func TestDeriveProbeURLs(t *testing.T) {
	cases := []struct {
		endpoint   string
		modelsURL  string
		siteOrigin string
	}{
		{"https://api.example.com/v1/chat/completions", "https://api.example.com/v1/models", "https://api.example.com"},
		{"https://api.example.com/v1/chat/completions/", "https://api.example.com/v1/models", "https://api.example.com"},
		{" https://api.example.com/v1 ", "https://api.example.com/v1/models", "https://api.example.com"},
		{"https://api.example.com/v1", "https://api.example.com/v1/models", "https://api.example.com"},
		{"https://api.example.com", "https://api.example.com/models", "https://api.example.com"},
		{"", "/models", ""},
	}
	for _, tc := range cases {
		modelsURL, siteOrigin := deriveProbeURLs(tc.endpoint)
		if modelsURL != tc.modelsURL || siteOrigin != tc.siteOrigin {
			t.Fatalf("deriveProbeURLs(%q) = (%q, %q)，期望 (%q, %q)", tc.endpoint, modelsURL, siteOrigin, tc.modelsURL, tc.siteOrigin)
		}
	}
}

// TestProbeModelsMock 模型探三态——200 数组 / 404 / 非 JSON / 无 data 数组。
func TestProbeModelsMock(t *testing.T) {
	section := probeModels(newProbeServer(`{"data":[{"id":"m1","owned_by":"o","supported_endpoint_types":["openai"]}]}`, 200), "sk-test")
	if !section.OK || section.HTTPStatus != 200 || section.Error != "" || section.Payload == nil {
		t.Fatalf("200 数组：意外结果 %+v", section)
	}
	items, ok := section.Payload.([]llmProbeModelItem)
	if !ok || len(items) != 1 || items[0].ID != "m1" || items[0].OwnedBy != "o" {
		t.Fatalf("200 数组：payload 不符 %+v", section.Payload)
	}

	section = probeModels(newProbeServer(`{"error":"nope"}`, 404), "")
	if section.OK || section.HTTPStatus != 404 || !strings.HasPrefix(section.Error, "HTTP 404 ") {
		t.Fatalf("404：意外结果 %+v", section)
	}

	section = probeModels(newProbeServer(`not-json`, 200), "")
	if section.OK || !strings.HasPrefix(section.Error, "响应非 JSON：") {
		t.Fatalf("非 JSON：意外结果 %+v", section)
	}

	section = probeModels(newProbeServer(`{"nope":1}`, 200), "")
	if section.OK || section.Error != "响应无 data 数组" {
		t.Fatalf("无 data 数组：意外结果 %+v", section)
	}
}

// TestProbeSiteMock 站点探——公告字段剔除；404 / 无 data 对象。
func TestProbeSiteMock(t *testing.T) {
	base := newProbeServer(`{"data":{"a":1,"announcements":"x","announcements_enabled":true}}`, 200)
	section := probeSite(base)
	if !section.OK {
		t.Fatalf("200：意外结果 %+v", section)
	}
	fields, ok := section.Payload.(map[string]any)
	if !ok {
		t.Fatalf("200：payload 形态不符 %T", section.Payload)
	}
	if _, exists := fields["announcements"]; exists {
		t.Fatal("200：announcements 未剔除")
	}
	if _, exists := fields["announcements_enabled"]; exists {
		t.Fatal("200：announcements_enabled 未剔除")
	}
	if fields["a"] != float64(1) {
		t.Fatalf("200：data 字段不符 %+v", fields)
	}

	section = probeSite(newProbeServer(`{}`, 404))
	if section.OK || !strings.HasPrefix(section.Error, "HTTP 404 ") {
		t.Fatalf("404：意外结果 %+v", section)
	}

	section = probeSite(newProbeServer(`{"data":[]}`, 200))
	if section.OK || section.Error != "响应无 data 对象" {
		t.Fatalf("无 data 对象：意外结果 %+v", section)
	}
}

// TestProbePricingMock 定价探——六键齐全 / 缺字段 null / 404。
func TestProbePricingMock(t *testing.T) {
	base := newProbeServer(`{"data":{"m1":1},"group_ratio":{"default":1},"usable_group":{"default":1},"vendors":[{"name":"v"}],"supported_endpoint":{"openai":true},"auto_groups":["default"]}`, 200)
	section := probePricing(base)
	if !section.OK {
		t.Fatalf("200：意外结果 %+v", section)
	}
	payload, ok := section.Payload.(llmPricingPayload)
	if !ok {
		t.Fatalf("200：payload 形态不符 %T", section.Payload)
	}
	if payload.Models == nil || payload.GroupRatio == nil || payload.UsableGroup == nil || payload.Vendors == nil || payload.SupportedEndpoint == nil || payload.AutoGroups == nil {
		t.Fatalf("200：六键未齐全 %+v", payload)
	}

	section = probePricing(newProbeServer(`{}`, 200))
	if !section.OK {
		t.Fatalf("空对象：期望 ok（C# 侧解析成功即 ok），实际 %+v", section)
	}
	if empty, ok := section.Payload.(llmPricingPayload); !ok || empty.Models != nil {
		t.Fatalf("空对象：期望字段全 null，实际 %+v", section.Payload)
	}

	section = probePricing(newProbeServer(`{}`, 404))
	if section.OK || !strings.HasPrefix(section.Error, "HTTP 404 ") {
		t.Fatalf("404：意外结果 %+v", section)
	}
}

// TestProbeTransportError 传输异常——连接拒绝按 HttpRequestException 留痕。
func TestProbeTransportError(t *testing.T) {
	section := probeModels("http://127.0.0.1:1/v1/models", "")
	if section.OK || !strings.HasPrefix(section.Error, "HttpRequestException|") {
		t.Fatalf("连接拒绝：意外结果 %+v", section)
	}
}

// TestHandleLlmApisProbeFace 端到端——参数面 + 三探报告 + 密钥只判 hasKey。
func TestHandleLlmApisProbeFace(t *testing.T) {
	deps := &Deps{}
	// 参数面——body 非 JSON / apiConfigId 非法 / 配置池未绑定 / 配置不存在
	assertProbeErr(t, deps, "not-json", "body 非 JSON")
	assertProbeErr(t, deps, `{"apiConfigId":"x"}`, "apiConfigId 非法")
	assertProbeErr(t, deps, `{"apiConfigId":"00000000-0000-0000-0000-000000000000"}`, "apiConfigId 非法")
	assertProbeErr(t, deps, `{"apiConfigId":"11111111-2222-3333-4444-555555555555"}`, "配置池未绑定")

	// mock 站点——models / status / pricing 三端点
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("models 探未带 Bearer 头：%q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"m1","owned_by":"o"}]}`))
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("site 探不应带 Bearer 头：%q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"data":{"version":"v1","announcements":"公告"}}`))
	})
	mux.HandleFunc("/api/pricing", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"m1":1},"group_ratio":{"default":1}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	dir := t.TempDir()
	store := config.LoadLlmAPIStore(filepath.Join(dir, "llm-api.json"), filepath.Join(dir, "llm-api.cfg"))
	// 空池——配置不存在
	assertProbeErr(t, &Deps{LLMStore: store}, `{"apiConfigId":"11111111-2222-3333-4444-555555555555"}`, "配置不存在")
	cfg := config.LlmAPIConfig{SchemaVersion: 1, APIConfigID: "11111111-2222-3333-4444-555555555555", DisplayName: "探针池", APIType: "openai", Endpoint: server.URL + "/v1/chat/completions", DefaultModel: "m1"}
	if err := store.Save(cfg, "sk-secret"); err != nil {
		t.Fatalf("池写入失败：%v", err)
	}
	deps.LLMStore = store

	report := callProbe(t, deps, `{"apiConfigId":"`+cfg.APIConfigID+`"}`)
	if report["ok"] != true {
		t.Fatalf("端到端：ok 期望 true，实际 %+v", report)
	}
	if report["hasKey"] != true {
		t.Fatalf("端到端：hasKey 期望 true")
	}
	if report["apiConfigId"] != cfg.APIConfigID {
		t.Fatalf("端到端：apiConfigId 不符 %v", report["apiConfigId"])
	}
	if report["modelsUrl"] != server.URL+"/v1/models" || report["siteOrigin"] != server.URL {
		t.Fatalf("端到端：端点推导不符 modelsUrl=%v siteOrigin=%v", report["modelsUrl"], report["siteOrigin"])
	}
	models := report["models"].(map[string]any)
	if models["ok"] != true {
		t.Fatalf("端到端：模型探未通过 %+v", models)
	}
	site := report["site"].(map[string]any)
	if site["ok"] != true {
		t.Fatalf("端到端：站点探未通过 %+v", site)
	}
	sitePayload := site["payload"].(map[string]any)
	if _, exists := sitePayload["announcements"]; exists {
		t.Fatal("端到端：announcements 未剔除")
	}
	pricing := report["pricing"].(map[string]any)
	if pricing["ok"] != true {
		t.Fatalf("端到端：定价探未通过 %+v", pricing)
	}
	if strings.Contains(strings.ToLower(strings.Join(toStrings(report), ",")), "sk-secret") {
		t.Fatal("端到端：报告泄露密钥")
	}
}

// assertProbeErr 断言参数面错误回执。
func assertProbeErr(t *testing.T, deps *Deps, body, wantError string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/llm-apis/probe", strings.NewReader(body))
	deps.handleLlmApisProbe(recorder, request)
	var reply map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &reply); err != nil {
		t.Fatalf("回执非 JSON：%v（%s）", err, recorder.Body.String())
	}
	if reply["ok"] != false || reply["error"] != wantError {
		t.Fatalf("期望 error=%q，实际 %+v", wantError, reply)
	}
}

// callProbe 调一次探针端点并解析报告。
func callProbe(t *testing.T, deps *Deps, body string) map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/llm-apis/probe", strings.NewReader(body))
	deps.handleLlmApisProbe(recorder, request)
	var report map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
		t.Fatalf("报告非 JSON：%v（%s）", err, recorder.Body.String())
	}
	return report
}

// newProbeServer 起一个固定响应的 mock 站点（返回根 URL）。
func newProbeServer(body string, status int) string {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	return server.URL
}

// toStrings 报告扁平化（找密钥泄露用）。
func toStrings(value any) []string {
	out := []string{}
	switch v := value.(type) {
	case string:
		out = append(out, v)
	case map[string]any:
		for _, item := range v {
			out = append(out, toStrings(item)...)
		}
	case []any:
		for _, item := range v {
			out = append(out, toStrings(item)...)
		}
	}
	return out
}
