package http

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"cathome5/host/config"
)

// writeJSON 写 JSON 响应——关闭 HTML 转义（对齐 System.Text.Json 的中文直出），
// 去掉编码器追加的尾换行（对齐 C# JsonUtil.Serialize 的紧凑输出）。
func writeJSON(w http.ResponseWriter, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		http.Error(w, `{"ok":false,"error":"json encode failed"}`, http.StatusInternalServerError)
		return
	}
	body := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// handleSnapshot 服务 GET /api/v1/snapshot?logs=N。
//
// 快照体由 Pump 维护的缓存提供；?logs=N 用精确字符串替换把 `"logs":[]`
// 换成最近 N 条日志（与 C# ReplaceLogsSection 同口径）。
func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	body := s.currentSnapshot()
	count := parseLogsQuery(r)
	if count > 0 && s.logsArrayFn != nil {
		if logs := s.logsArrayFn(count); logs != "" {
			body = replaceLogsSection(body, logs)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

// handleStream 服务 GET /api/v1/stream——SSE 连接。
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	client := newSSEClient(w, flusher, parseTopics(r.URL.Query().Get("topics")))
	s.addClient(client)
	defer s.removeClient(client)
	client.serve(r)
}

// handleCommand 服务 POST /api/v1/command——会话指令入口（投递即回执，结果异步可见）。
func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": "指令读取失败: " + err.Error()})
		return
	}
	text := extractCommandText(body)
	if s.commandFn == nil {
		writeJSON(w, map[string]any{"ok": false, "error": "command not implemented"})
		return
	}
	ok := s.commandFn(text)
	seq := atomic.AddInt64(&s.cmdSeq, 1)
	frame := int64(0)
	if s.frameFn != nil {
		frame = s.frameFn()
	}
	cmdID := "cmd-" + strconv.FormatInt(seq, 10)
	if ok {
		payload := `{"ok":true,"cmdId":` + jsonStr(cmdID) + `,"frame":` + strconv.FormatInt(frame, 10) + `}`
		s.Broadcast("cmd", payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
		return
	}
	payload := `{"ok":false,"cmdId":` + jsonStr(cmdID) + `,"frame":` + strconv.FormatInt(frame, 10) + `,"error":` + jsonStr("指令未识别: "+text) + `}`
	s.Broadcast("cmd", payload)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(payload))
}

// extractCommandText 从请求体提取指令文本——协议 §5.1 {"text":"..."}；损坏体按原文处理。
func extractCommandText(body []byte) string {
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		return payload.Text
	}
	return strings.TrimSpace(string(body))
}

// jsonStr 编码 JSON 字符串字面量（关闭 HTML 转义）。
func jsonStr(text string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(text)
	return strings.TrimSuffix(buf.String(), "\n")
}

// logsResponse 是 /api/v1/logs 的响应体。
type logsResponse struct {
	Version int   `json:"version"`
	Logs    []any `json:"logs"`
}

// handleLogs 服务 GET /api/v1/logs?n=&cat=&type=。
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	n := 200
	if parsed, err := strconv.Atoi(r.URL.Query().Get("n")); err == nil && parsed > 0 {
		n = parsed
	}
	if n > 2000 {
		n = 2000
	}
	cat := r.URL.Query().Get("cat")
	typePrefix := r.URL.Query().Get("type")
	if s.logsQueryFn == nil {
		writeJSON(w, logsResponse{Version: 1, Logs: []any{}})
		return
	}
	body := s.logsQueryFn(n, cat, typePrefix)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

// configItem 是 /api/v1/config 的单项（与 C# 匿名对象同字段同序）。
type configItem struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Source    string `json:"source"`
	Declared  bool   `json:"declared"`
	File      string `json:"file"`
	Default   string `json:"default"`
	Writable  bool   `json:"writable"`
	Desc      string `json:"desc"`
	EmptyDesc string `json:"emptyDesc"`
}

// configResponse 是 /api/v1/config 的响应体。
type configResponse struct {
	Version int          `json:"version"`
	Items   []configItem `json:"items"`
}

// handleConfigGet 服务 GET /api/v1/config。
func (s *Server) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	items := make([]configItem, 0)
	if s.store != nil {
		for _, entry := range s.store.BuildAll(s.schema) {
			value := entry.Value
			if entry.Sensitive {
				value = maskSecret(value)
			}
			items = append(items, configItem{
				Key:       entry.Key,
				Value:     value,
				Source:    entry.Source,
				Declared:  entry.Declared,
				File:      entry.File,
				Default:   entry.Default,
				Writable:  entry.Writable,
				Desc:      entry.Desc,
				EmptyDesc: entry.EmptyDesc,
			})
		}
	}
	writeJSON(w, configResponse{Version: 1, Items: items})
}

// configWriteOK 是 POST /api/v1/config 的成功回执（字段序对齐 C# 匿名对象 ok/key/value/frame）。
type configWriteOK struct {
	OK    bool   `json:"ok"`
	Key   string `json:"key"`
	Value string `json:"value"`
	Frame int64  `json:"frame"`
}

// configWriteErr 是 POST /api/v1/config 的失败回执（ok/error 两字段，HTTP 200）。
type configWriteErr struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

// handleConfigPost 服务 POST /api/v1/config——全局配置写面（body: {"key":"...","value":"..."}）。
//
// 契约对齐 `mau:CatHome4/CatHome4.Http/HttpHost.Config.cs` HandleConfigPost：
// body 解析失败按空 key/value 处理 → key 空 / value 空 / 掩码标记三道前置拒绝 →
// ConfigStore.SetChecked（schema 白名单 + 值域校验 + 掩码拒绝 + 原子写回滚）→
// 成功回执 key/value/frame（敏感键回显掩码，与 GET 同规）。
func (s *Server) handleConfigPost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeJSON(w, configWriteErr{OK: false, Error: "指令读取失败: " + err.Error()})
		return
	}
	var payload struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	key := ""
	value := ""
	if err := json.Unmarshal(body, &payload); err == nil {
		key = payload.Key
		value = payload.Value
	}
	if key == "" {
		writeJSON(w, configWriteErr{OK: false, Error: "key 为空"})
		return
	}
	if value == "" {
		writeJSON(w, configWriteErr{OK: false, Error: "value 为空"})
		return
	}
	if strings.Contains(value, "****") {
		writeJSON(w, configWriteErr{OK: false, Error: "value 含掩码标记——请输入真实值"})
		return
	}
	if s.store == nil {
		writeJSON(w, configWriteErr{OK: false, Error: "配置存储未绑定"})
		return
	}
	if ok, reason := s.store.SetChecked(key, value, s.schema); !ok {
		writeJSON(w, configWriteErr{OK: false, Error: reason})
		return
	}
	frame := int64(0)
	if s.frameFn != nil {
		frame = s.frameFn()
	}
	echo := value
	if config.IsSecretKey(key) {
		echo = maskSecret(value)
	}
	writeJSON(w, configWriteOK{OK: true, Key: key, Value: echo, Frame: frame})
}

// handleCats 服务 GET /api/v1/cats。
func (s *Server) handleCats(w http.ResponseWriter, r *http.Request) {
	if s.catsBuilder == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(s.catsBuilder()))
}

// handleDepsRefresh 服务 POST /api/v1/deps/refresh。
func (s *Server) handleDepsRefresh(w http.ResponseWriter, r *http.Request) {
	if s.depsRefresh == nil {
		http.NotFound(w, r)
		return
	}
	s.depsRefresh()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// handleLLMProbe 服务 POST /api/v1/llm/probe。
func (s *Server) handleLLMProbe(w http.ResponseWriter, r *http.Request) {
	if s.llmProbe == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(s.llmProbe()))
}

// handleAPIRole 服务 GET /api/v1/api-role。
func (s *Server) handleAPIRole(w http.ResponseWriter, r *http.Request) {
	if s.apiRoleBuilder == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(s.apiRoleBuilder()))
}

// handleAPIModels 服务 GET /api/v1/api-models。
func (s *Server) handleAPIModels(w http.ResponseWriter, r *http.Request) {
	if s.apiModelsBuilder == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(s.apiModelsBuilder()))
}

// parseLogsQuery 解析 ?logs=N——缺省 / 非法回退 200，上限 2000。
func parseLogsQuery(r *http.Request) int {
	count := 200
	if parsed, err := strconv.Atoi(r.URL.Query().Get("logs")); err == nil && parsed > 0 {
		count = parsed
	}
	if count > 2000 {
		count = 2000
	}
	return count
}

// replaceLogsSection 把快照 JSON 的 `"logs":[]` 精确替换为日志数组。
func replaceLogsSection(jsonBody, logsJSON string) string {
	marker := `"logs":[]`
	idx := strings.Index(jsonBody, marker)
	if idx < 0 {
		return jsonBody
	}
	return jsonBody[:idx] + `"logs":` + logsJSON + jsonBody[idx+len(marker):]
}

// maskSecret 掩码敏感值（前4 + **** + 后4；≤8 全 ****）。
func maskSecret(value string) string {
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= 8 {
		return "****"
	}
	return string(runes[:4]) + "****" + string(runes[len(runes)-4:])
}
