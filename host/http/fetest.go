package http

// fetest —— 前端测试代理面：GET /api/v1/frontend-test 转发本区段的前端测试服务（Vitest）。

import (
	"io"
	"net/http"
	"strconv"
	"time"
)

// frontendTestTimeout 是转发请求的总超时——对齐 C# FrontendTestHttp.Timeout = 120s
// （Vitest 单次运行上限同为 120s，见 CatHome4/html/tests/server.js 的 TEST_TIMEOUT_MS）。
const frontendTestTimeout = 120 * time.Second

// frontendTestError 是转发失败回执——键序固定 ok → error（对齐 C# 拼装形态）。
type frontendTestError struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

// handleFrontendTest 服务 GET /api/v1/frontend-test——转发 Vitest。
//
// 对齐 C# CatHome4.Http/HttpHost.cs:628-641：
//   - GET http://127.0.0.1:<FrontendTestPort>/api/test/unit（E2E 已移除，只转发单元测试）；
//   - 成功：上游 body 原样回传，Content-Type application/json，状态码恒 200——
//     C# Results.Text 不携带上游状态码（Vitest 失败时上游 500，端点仍回 200）；
//   - 失败（连不上 / 读体失败）：{"ok":false,"error":"<错误文本>"}，状态码 200。
//
// 两侧错误文本必然不同（.NET 异常文本 vs Go 错误文本）——对拍按 KNOWN 记。
func (s *Server) handleFrontendTest(w http.ResponseWriter, r *http.Request) {
	port := s.opts.FrontendTestPort
	if port <= 0 {
		writeJSON(w, frontendTestError{OK: false, Error: "frontend test port not configured"})
		return
	}
	resp, err := s.frontendTestHTTP.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/api/test/unit")
	if err != nil {
		writeJSON(w, frontendTestError{OK: false, Error: err.Error()})
		return
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeJSON(w, frontendTestError{OK: false, Error: err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
