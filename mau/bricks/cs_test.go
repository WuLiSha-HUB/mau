package bricks

// Cs 桥单测——假 HTTP 上游覆盖：服务未注入 / 成功链路与方法透传 / ok=false 透传 /
// 连接失败（服务不可用）/ 根表不一致 / 就绪缓存 / 就绪后连接中断 / 地址与路径辅助函数。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"cathome5/mau/fs"
)

// fakeBridge 是假 cs 桥——记录探测与调用入参，按注入的应答回执。
type fakeBridge struct {
	// mu 保护计数与最近入参
	mu sync.Mutex
	// healthCalls /health 被探次数
	healthCalls int
	// invokeCalls /invoke 被调次数
	invokeCalls int
	// lastMethod 最近一次 /invoke 的 method
	lastMethod string
	// lastArgs 最近一次 /invoke 的 argsJson
	lastArgs string
	// replyOK /invoke 应答的 ok
	replyOK bool
	// replyResult /invoke 应答的 result
	replyResult string
	// healthRoots /health 应答的根表
	healthRoots []map[string]string
}

// ServeHTTP 实现假桥的两条路由。
func (f *fakeBridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/health":
		f.mu.Lock()
		f.healthCalls++
		roots := f.healthRoots
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "service": "mau-csbridge", "version": "test", "pid": 1,
			"roots": roots, "methods": []string{"check", "list"},
		})
	case "/invoke":
		var request struct {
			Method   string `json:"method"`
			ArgsJSON string `json:"argsJson"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		f.mu.Lock()
		f.invokeCalls++
		f.lastMethod = request.Method
		f.lastArgs = request.ArgsJSON
		ok, result := f.replyOK, f.replyResult
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": ok, "result": result})
	default:
		http.NotFound(w, r)
	}
}

// csTestRoots 是测试用受控根表（虚构路径——不触文件系统）。
func csTestRoots() []fs.Root {
	return []fs.Root{
		{ID: "workspace", Path: `D:\Test\WorkSpace`, Writable: true},
		{ID: "ccbp", Path: `D:\Test\CCBP`, Writable: true},
	}
}

// csRootsPayload 把根表转成 /health 的 roots 形态。
func csRootsPayload(roots []fs.Root) []map[string]string {
	out := make([]map[string]string, 0, len(roots))
	for _, root := range roots {
		out = append(out, map[string]string{"id": root.ID, "path": root.Path})
	}
	return out
}

// TestCSharpBridgeNilService 服务未注入——回执文案逐字对齐 C# 积木。
func TestCSharpBridgeNilService(t *testing.T) {
	text, ok := CSharpBridge(nil, "check", "{}")
	if ok {
		t.Fatalf("服务未注入应判失败，实得 ok=true")
	}
	want := "ERR|CSHARP_NO_BRIDGE|宿主未注入 ICSharpBridge（Mau.Development.MauRoslynBridge）"
	if text != want {
		t.Fatalf("回执文案不符\n got: %s\nwant: %s", text, want)
	}
}

// TestCSharpBridgeInvoke 成功链路——method / argsJson 原样透传，{ok,result} → (result, ok)。
func TestCSharpBridgeInvoke(t *testing.T) {
	roots := csTestRoots()
	fake := &fakeBridge{replyOK: true, replyResult: "OK|cs-check | 0 error", healthRoots: csRootsPayload(roots)}
	server := httptest.NewServer(fake)
	defer server.Close()
	t.Setenv("CAT5_CS_BRIDGE_URL", server.URL)

	svc := NewCsService(roots, nil)
	defer svc.Shutdown()
	args := `{"path":"ccbp:L1/Tree.md","full":"true"}`
	text, ok := svc.Invoke("find_ref", args)
	if !ok {
		t.Fatalf("成功链路应判成功，实得：%s", text)
	}
	if text != "OK|cs-check | 0 error" {
		t.Fatalf("result 应原样返回，实得：%s", text)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.lastMethod != "find_ref" {
		t.Fatalf("method 应透传，实得：%s", fake.lastMethod)
	}
	if fake.lastArgs != args {
		t.Fatalf("argsJson 应原样透传\n got: %s\nwant: %s", fake.lastArgs, args)
	}
}

// TestCSharpBridgePassthroughFailure ok=false 透传——失败文本按工具成功返回（Dog 可见原因）。
func TestCSharpBridgePassthroughFailure(t *testing.T) {
	roots := csTestRoots()
	fake := &fakeBridge{replyOK: false, replyResult: "ERR|UNKNOWN_METHOD|未知方法: nope", healthRoots: csRootsPayload(roots)}
	server := httptest.NewServer(fake)
	defer server.Close()
	t.Setenv("CAT5_CS_BRIDGE_URL", server.URL)

	svc := NewCsService(roots, nil)
	defer svc.Shutdown()
	text, ok := svc.Invoke("nope", "{}")
	if ok {
		t.Fatalf("ok=false 应判失败，实得 ok=true")
	}
	if text != "ERR|UNKNOWN_METHOD|未知方法: nope" {
		t.Fatalf("失败文本应原样透传，实得：%s", text)
	}
}

// TestCSharpBridgeUnavailable 服务不可用——端点不通 + 桥 exe 不存在 → CSHARP_NO_BRIDGE。
func TestCSharpBridgeUnavailable(t *testing.T) {
	t.Setenv("CAT5_CS_BRIDGE_URL", "http://127.0.0.1:1")
	t.Setenv("CAT5_CS_BRIDGE_EXE", filepath.Join(t.TempDir(), "missing-bridge.exe"))
	svc := NewCsService(csTestRoots(), nil)
	defer svc.Shutdown()
	text, ok := svc.Invoke("check", "{}")
	if ok {
		t.Fatalf("不可用应判失败，实得 ok=true")
	}
	if !strings.HasPrefix(text, "ERR|CSHARP_NO_BRIDGE|") {
		t.Fatalf("应为 CSHARP_NO_BRIDGE 前缀，实得：%s", text)
	}
	if !strings.Contains(text, csBridgeExeName) {
		t.Fatalf("文案应含桥文件名，实得：%s", text)
	}
}

// TestCSharpBridgeRootsMismatch 根表不一致——按不可用报错（不接管外部进程）。
func TestCSharpBridgeRootsMismatch(t *testing.T) {
	fake := &fakeBridge{
		replyOK:     true,
		replyResult: "OK|should-not-reach",
		healthRoots: []map[string]string{{"id": "workspace", "path": `D:\Other\Place`}},
	}
	server := httptest.NewServer(fake)
	defer server.Close()
	t.Setenv("CAT5_CS_BRIDGE_URL", server.URL)

	svc := NewCsService(csTestRoots(), nil)
	defer svc.Shutdown()
	text, ok := svc.Invoke("check", "{}")
	if ok {
		t.Fatalf("根表不一致应判失败，实得 ok=true")
	}
	if !strings.Contains(text, "根表与当前不一致") {
		t.Fatalf("文案应说明根表不一致，实得：%s", text)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.invokeCalls != 0 {
		t.Fatalf("根表不一致不得发起调用，实得 %d 次", fake.invokeCalls)
	}
}

// TestCSharpBridgeReadyCache 就绪缓存——第二次调用不再探 /health。
func TestCSharpBridgeReadyCache(t *testing.T) {
	roots := csTestRoots()
	fake := &fakeBridge{replyOK: true, replyResult: "OK", healthRoots: csRootsPayload(roots)}
	server := httptest.NewServer(fake)
	defer server.Close()
	t.Setenv("CAT5_CS_BRIDGE_URL", server.URL)

	svc := NewCsService(roots, nil)
	defer svc.Shutdown()
	if _, ok := svc.Invoke("check", "{}"); !ok {
		t.Fatalf("首调用应成功")
	}
	if _, ok := svc.Invoke("check", "{}"); !ok {
		t.Fatalf("次调用应成功")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.healthCalls != 1 {
		t.Fatalf("/health 应只探一次（就绪缓存），实得 %d 次", fake.healthCalls)
	}
	if fake.invokeCalls != 2 {
		t.Fatalf("/invoke 应调两次，实得 %d 次", fake.invokeCalls)
	}
}

// TestCSharpBridgeIOFailure 就绪后连接中断——归 CSHARP_BRIDGE_IO 且重置就绪态。
func TestCSharpBridgeIOFailure(t *testing.T) {
	roots := csTestRoots()
	fake := &fakeBridge{replyOK: true, replyResult: "OK", healthRoots: csRootsPayload(roots)}
	server := httptest.NewServer(fake)
	t.Setenv("CAT5_CS_BRIDGE_URL", server.URL)

	svc := NewCsService(roots, nil)
	defer svc.Shutdown()
	if _, ok := svc.Invoke("check", "{}"); !ok {
		t.Fatalf("首调用应成功")
	}
	server.Close()
	text, ok := svc.Invoke("check", "{}")
	if ok {
		t.Fatalf("连接中断应判失败，实得 ok=true")
	}
	if !strings.HasPrefix(text, "ERR|CSHARP_BRIDGE_IO|") {
		t.Fatalf("应为 CSHARP_BRIDGE_IO 前缀，实得：%s", text)
	}
	if svc.Alive() {
		t.Fatalf("连接中断后应重置就绪态")
	}
}

// TestCsBridgeHelpers 地址 / 端口 / 路径归一 / 预览截断。
func TestCsBridgeHelpers(t *testing.T) {
	if got := csBridgePort("http://127.0.0.1:9130"); got != "9130" {
		t.Fatalf("端口解析不符：%s", got)
	}
	if got := csBridgePort("http://127.0.0.1"); got != csDefaultBridgePort {
		t.Fatalf("缺端口应回落默认：%s", got)
	}
	if got := csBridgePort("http://[::1"); got != csDefaultBridgePort {
		t.Fatalf("非法地址应回落默认：%s", got)
	}
	if got := csNormalizeRootPath(`D:\Mau\WorkSpace\`); got != "d:/mau/workspace" {
		t.Fatalf("路径归一不符：%s", got)
	}
	if got := csNormalizeRootPath("/a/b/"); got != "/a/b" {
		t.Fatalf("路径归一不符：%s", got)
	}
	long := strings.Repeat("核", 10)
	if got := csPreview(long, 4); !strings.HasSuffix(got, "字）") {
		t.Fatalf("截断预览应带总字数尾注：%s", got)
	}
	if got := csPreview("短", 4); got != "短" {
		t.Fatalf("未超限应原样返回：%s", got)
	}
}

// TestCsBridgeBaseURLOverride 基址覆盖——CAT5_CS_BRIDGE_URL 优先（去尾斜杠）。
func TestCsBridgeBaseURLOverride(t *testing.T) {
	t.Setenv("CAT5_CS_BRIDGE_URL", "http://127.0.0.1:9130/")
	if got := csBridgeBaseURL(); got != "http://127.0.0.1:9130" {
		t.Fatalf("覆盖地址应去尾斜杠：%s", got)
	}
}
