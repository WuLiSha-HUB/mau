package http

// 对话页条件面——主端口 9 条条件路由的处理器（对齐 C# HttpHost.cs:378-596）。
//
// 全部为构造期条件注册（builder 为 nil 即不挂载，不退化成 handler 内按猫过滤）；
// 数据面由组合根绑定（管理面板实例不注入 → 404，与 C# 管理端口同构）。
// 前三条（context / keyinfo / fullctx）共用 ?max= 解析（ReadMaxQuery 同口径）。

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

// readMaxQuery 解析 ?max=N——缺省 / 非法 / ≤0 = 0（不限，回传全部）；正数 = 该上限（不夹取）。
//
// context / keyinfo / fullctx 三处同源（C# ReadMaxQuery 同口径）。
func readMaxQuery(r *http.Request) int {
	if parsed, err := strconv.Atoi(r.URL.Query().Get("max")); err == nil && parsed > 0 {
		return parsed
	}
	return 0
}

// readBodyFields 读取请求体字段表——保留字段存在性（body 非 JSON = false）。
func readBodyFields(r *http.Request) (map[string]json.RawMessage, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return nil, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, false
	}
	return fields, true
}

// fieldString 取字符串字段（缺字段 / 非字符串 = 空串）。
func fieldString(fields map[string]json.RawMessage, key string) string {
	raw, ok := fields[key]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

// fieldBoolPtr 取可空布尔字段——缺失 / 非布尔返回 nil（沿用现值语义）。
func fieldBoolPtr(fields map[string]json.RawMessage, key string) *bool {
	raw, ok := fields[key]
	if !ok {
		return nil
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	return &value
}

// handleFlowReload 语料热重载（POST /api/v1/flow/reload）——body {"name":"TextCat","mode":"fresh|carry|drain"}。
//
// 回执由组合根产（与 host-reload 工具同一执行面）——HTTP 只是第二个入口，不是第二份实现。
func (s *Server) handleFlowReload(w http.ResponseWriter, r *http.Request) {
	if s.flowReloader == nil {
		writeText(w, `{"ok":false,"error":"FLOW_RELOAD_UNAVAILABLE","message":"语料热重载面未接线"}`)
		return
	}
	fields, ok := readBodyFields(r)
	if !ok {
		writeText(w, `{"ok":false,"error":"BAD_BODY","message":"请求体不是 JSON"}`)
		return
	}
	name := fieldString(fields, "name")
	if name == "" {
		name = fieldString(fields, "cat")
	}
	if name == "" {
		writeText(w, `{"ok":false,"error":"BAD_ARGS","message":"缺 name（组名——GET /api/v1/snapshot 的 cats 段可查）"}`)
		return
	}
	writeText(w, s.flowReloader(name, fieldString(fields, "mode")))
}

// writeText 写 JSON 文本响应（回执由构建器自产——与 C# Results.Text(json, "application/json") 同形）。
func writeText(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(text))
}

// handleContext 服务 GET /api/v1/context——前文条目视图（?max=N 上限；缺省不限）。
func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	if s.contextBuilder == nil {
		http.NotFound(w, r)
		return
	}
	writeText(w, s.contextBuilder(readMaxQuery(r)))
}

// handleKeyInfo 服务 GET /api/v1/keyinfo——关键信息视图。
func (s *Server) handleKeyInfo(w http.ResponseWriter, r *http.Request) {
	if s.keyInfoBuilder == nil {
		http.NotFound(w, r)
		return
	}
	writeText(w, s.keyInfoBuilder(readMaxQuery(r)))
}

// handleFullContext 服务 GET /api/v1/fullctx——完整前文视图。
func (s *Server) handleFullContext(w http.ResponseWriter, r *http.Request) {
	if s.fullContextBuilder == nil {
		http.NotFound(w, r)
		return
	}
	writeText(w, s.fullContextBuilder(readMaxQuery(r)))
}

// handleEffortGet 服务 GET /api/v1/effort——思考强度（本猫覆盖 + 全局值）。
func (s *Server) handleEffortGet(w http.ResponseWriter, r *http.Request) {
	if s.effortBuilder == nil {
		http.NotFound(w, r)
		return
	}
	writeText(w, s.effortBuilder())
}

// handleEffortPost 服务 POST /api/v1/effort——body {"value":"low|high|max|"}（空串=清空覆盖）。
//
// 入口面零容忍：非法 body 拒绝，不静默清空。
func (s *Server) handleEffortPost(w http.ResponseWriter, r *http.Request) {
	if s.effortSetter == nil {
		http.NotFound(w, r)
		return
	}
	fields, ok := readBodyFields(r)
	if !ok {
		writeText(w, `{"ok":false,"error":"body 非 JSON 或缺少 value 字段"}`)
		return
	}
	raw, exists := fields["value"]
	var value string
	if !exists || json.Unmarshal(raw, &value) != nil {
		writeText(w, `{"ok":false,"error":"body 非 JSON 或缺少 value 字段"}`)
		return
	}
	writeText(w, s.effortSetter(value))
}

// handleAPIRoleToggle 服务 POST /api/v1/api-role/toggle——手动对调端点角色（仅本会话有效）。
func (s *Server) handleAPIRoleToggle(w http.ResponseWriter, r *http.Request) {
	if s.apiRoleToggler == nil {
		http.NotFound(w, r)
		return
	}
	writeText(w, s.apiRoleToggler())
}

// handleAPIModelPost 服务 POST /api/v1/api-model——body {"model":"..."}（空串=清空覆盖）。
//
// 入口面零容忍：非法 body 拒绝。
func (s *Server) handleAPIModelPost(w http.ResponseWriter, r *http.Request) {
	if s.apiModelSetter == nil {
		http.NotFound(w, r)
		return
	}
	fields, ok := readBodyFields(r)
	if !ok {
		writeText(w, `{"ok":false,"error":"body 非 JSON 或缺少 model 字段"}`)
		return
	}
	raw, exists := fields["model"]
	var model string
	if !exists || json.Unmarshal(raw, &model) != nil {
		writeText(w, `{"ok":false,"error":"body 非 JSON 或缺少 model 字段"}`)
		return
	}
	writeText(w, s.apiModelSetter(model))
}

// handleInjectProjects 服务 GET /api/v1/inject/projects——候选项目清单。
func (s *Server) handleInjectProjects(w http.ResponseWriter, r *http.Request) {
	if s.injectProjectsBuilder == nil {
		http.NotFound(w, r)
		return
	}
	writeText(w, s.injectProjectsBuilder())
}

// handleInjectProjectPost 服务 POST /api/v1/inject/project——body {cat, project, expert}。
//
// 非法 body 回执键 = reason（C# 主端口版口径）。
func (s *Server) handleInjectProjectPost(w http.ResponseWriter, r *http.Request) {
	if s.injectProjectSetter == nil {
		http.NotFound(w, r)
		return
	}
	fields, ok := readBodyFields(r)
	if !ok {
		writeText(w, `{"ok":false,"reason":"body 非 JSON"}`)
		return
	}
	writeText(w, s.injectProjectSetter(fieldString(fields, "cat"), fieldString(fields, "project"), fieldBoolPtr(fields, "expert")))
}

// handleInjectAutoGet 服务 GET /api/v1/inject/auto——自动识别开关读面（?cat=）。
func (s *Server) handleInjectAutoGet(w http.ResponseWriter, r *http.Request) {
	if s.injectAutoGetter == nil {
		http.NotFound(w, r)
		return
	}
	writeText(w, s.injectAutoGetter(r.URL.Query().Get("cat")))
}

// handleInjectAutoPost 服务 POST /api/v1/inject/auto——body {cat, on}（字段级合并写）。
func (s *Server) handleInjectAutoPost(w http.ResponseWriter, r *http.Request) {
	if s.injectAutoSetter == nil {
		http.NotFound(w, r)
		return
	}
	fields, ok := readBodyFields(r)
	if !ok {
		writeText(w, `{"ok":false,"reason":"body 非 JSON"}`)
		return
	}
	writeText(w, s.injectAutoSetter(fieldString(fields, "cat"), fieldBoolPtr(fields, "on")))
}
