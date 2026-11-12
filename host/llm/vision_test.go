package llm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cathome5/host/config"
)

// visionTestID 测试端点身份（合法 GUID）。
const visionTestID = "11111111-2222-3333-4444-555555555555"

// newTestVisionService 构造视觉服务——临时端点池 + 配置存储 + 日志收集。
func newTestVisionService(t *testing.T, endpoint, model, apiKey string, settings map[string]string) (*VisionService, *[]string) {
	t.Helper()
	dir := t.TempDir()
	poolJSON := `[{"schemaVersion":1,"apiConfigId":"` + visionTestID +
		`","displayName":"test","apiType":"deepseek","endpoint":"` + endpoint +
		`","defaultModel":"` + model + `","isDefault":true,"isBackup":false}]`
	if err := os.WriteFile(filepath.Join(dir, "llm-api.json"), []byte(poolJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	secretJSON := `{"` + visionTestID + `":"` + apiKey + `"}`
	if err := os.WriteFile(filepath.Join(dir, "llm-api.cfg"), []byte(secretJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	llmStore := config.LoadLlmAPIStore(filepath.Join(dir, "llm-api.json"), filepath.Join(dir, "llm-api.cfg"))
	store := config.LoadStore(t.TempDir())
	for key, value := range settings {
		store.Set(key, value)
	}
	logs := make([]string, 0, 4)
	svc := NewVisionService(store, &config.Schema{}, llmStore, func(level int, message string) {
		logs = append(logs, message)
	})
	return svc, &logs
}

// writeTestPNG 写一张最小 PNG 头文件（魔数判定用）。
func writeTestPNG(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "test.png")
	raw := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x00}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestVisionServiceConfigErrors 配置解析五段错误面。
func TestVisionServiceConfigErrors(t *testing.T) {
	svc, _ := newTestVisionService(t, "http://127.0.0.1:1/v1", "test-model", "sk-test", nil)
	// 1) 未配置
	if got := svc.Analyze("D:/x.png", ""); !strings.HasPrefix(got, "ERR|API_NOT_CONFIGURED|视觉 API 未配置") {
		t.Fatalf("未配置: %s", got)
	}
	// 2) 非法 GUID
	svc.store.Set("vision.api_config_id", "not-a-guid")
	if got := svc.Analyze("D:/x.png", ""); !strings.HasPrefix(got, "ERR|API_NOT_CONFIGURED|视觉 API 配置无效") {
		t.Fatalf("非法 GUID: %s", got)
	}
	// 3) 池中不存在
	svc.store.Set("vision.api_config_id", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if got := svc.Analyze("D:/x.png", ""); !strings.HasPrefix(got, "ERR|API_NOT_CONFIGURED|视觉 API 配置不存在") {
		t.Fatalf("池中不存在: %s", got)
	}
	// 4) 缺 key
	noKey, _ := newTestVisionService(t, "http://127.0.0.1:1/v1", "test-model", "", nil)
	noKey.store.Set("vision.api_config_id", visionTestID)
	if got := noKey.Analyze("D:/x.png", ""); !strings.HasPrefix(got, "ERR|API_NOT_CONFIGURED|视觉 API 密钥未配置") {
		t.Fatalf("缺 key: %s", got)
	}
}

// TestVisionServiceAnalyzeSuccess 成功链路——请求体结构 + 头部 + usage 日志 + 返回文本。
func TestVisionServiceAnalyzeSuccess(t *testing.T) {
	var gotBody, gotAuth, gotContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		var decoded map[string]json.RawMessage
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		_ = json.Unmarshal([]byte(raw), &decoded)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"图中有一只猫"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
	}))
	defer server.Close()

	svc, logs := newTestVisionService(t, server.URL, "test-model", "sk-test", map[string]string{
		"vision.endpoint": server.URL + "/chat/completions",
	})
	svc.store.Set("vision.api_config_id", visionTestID)
	imagePath := writeTestPNG(t, t.TempDir())

	got := svc.Analyze(imagePath, "这是什么？")
	if got != "图中有一只猫" {
		t.Fatalf("返回文本: %s", got)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("Authorization: %s", gotAuth)
	}
	if !strings.HasPrefix(gotContentType, "application/json") {
		t.Fatalf("Content-Type: %s", gotContentType)
	}
	// 请求体结构——model / messages 内容块 / detail / stream
	var body struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ImageURL struct {
					URL    string `json:"url"`
					Detail string `json:"detail"`
				} `json:"image_url"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
		t.Fatalf("请求体解析失败: %v\n%s", err, gotBody)
	}
	if body.Model != "test-model" || body.Stream {
		t.Fatalf("model/stream 不符: %+v", body)
	}
	if len(body.Messages) != 1 || body.Messages[0].Role != "user" || len(body.Messages[0].Content) != 2 {
		t.Fatalf("messages 结构: %+v", body.Messages)
	}
	text := body.Messages[0].Content[0]
	if text.Type != "text" || !strings.HasPrefix(text.Text, "这是什么？\n\n"+visionOutputFormatSpec) {
		t.Fatalf("提示词组装: %+v", text)
	}
	img := body.Messages[0].Content[1]
	if img.Type != "image_url" || !strings.HasPrefix(img.ImageURL.URL, "data:image/png;base64,") || img.ImageURL.Detail != "auto" {
		t.Fatalf("图片块: %+v", img)
	}
	// usage 日志
	found := false
	for _, line := range *logs {
		if line == "图像识别消耗：prompt=10|completion=5" {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺少 usage 日志: %v", *logs)
	}
}

// TestVisionServiceDefaultPrompt 空 question 走内置默认提示词。
func TestVisionServiceDefaultPrompt(t *testing.T) {
	gotBody := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()
	svc, _ := newTestVisionService(t, server.URL, "m", "k", map[string]string{"vision.endpoint": server.URL})
	svc.store.Set("vision.api_config_id", visionTestID)
	if got := svc.Analyze(writeTestPNG(t, t.TempDir()), ""); got != "ok" {
		t.Fatalf("返回: %s", got)
	}
	if !strings.Contains(gotBody, visionDefaultPrompt) {
		t.Fatalf("空 question 应走默认提示词: %s", gotBody)
	}
}

// TestVisionServiceEmptyResult 空输出——EMPTY_RESULT 附响应头诊断。
func TestVisionServiceEmptyResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":""}}]}`))
	}))
	defer server.Close()
	svc, _ := newTestVisionService(t, server.URL, "m", "k", map[string]string{"vision.endpoint": server.URL})
	svc.store.Set("vision.api_config_id", visionTestID)
	got := svc.Analyze(writeTestPNG(t, t.TempDir()), "问")
	if !strings.HasPrefix(got, "ERR|EMPTY_RESULT|图像识别响应无输出文本（响应头 300 字符: ") {
		t.Fatalf("EMPTY_RESULT: %s", got)
	}
}

// TestVisionServiceHTTPError HTTP 错误——error.message / error.type 双形态兜底。
func TestVisionServiceHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad image","type":"invalid_request_error"}}`))
	}))
	defer server.Close()
	svc, _ := newTestVisionService(t, server.URL, "m", "k", map[string]string{"vision.endpoint": server.URL})
	svc.store.Set("vision.api_config_id", visionTestID)
	if got := svc.Analyze(writeTestPNG(t, t.TempDir()), "问"); got != "ERR|HTTP_400|bad image" {
		t.Fatalf("HTTP 错误: %s", got)
	}
}

// TestVisionServiceImageNotFound 图片不存在——IMAGE_NOT_FOUND（配置有效但不触网）。
func TestVisionServiceImageNotFound(t *testing.T) {
	svc, _ := newTestVisionService(t, "http://127.0.0.1:1/v1", "m", "k", map[string]string{"vision.endpoint": "http://127.0.0.1:1/chat/completions"})
	svc.store.Set("vision.api_config_id", visionTestID)
	got := svc.Analyze(filepath.Join(t.TempDir(), "missing.png"), "问")
	if !strings.HasPrefix(got, "ERR|IMAGE_NOT_FOUND|") {
		t.Fatalf("图片不存在: %s", got)
	}
}
