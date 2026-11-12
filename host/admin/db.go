package admin

// 本文件是段 11「数据库（Smriti）」管理面——库清单 / 建库 / 条目查询 / 条目全文四端点。
//
// 权威实现 = `mau:CatHome4/CatHome4.Admin/AdminService.Db.cs`；写入一律走 db-* 工具面
// （本面只读 + 建库，杜绝两套写路径）。
//
// 服务实例经 Deps.DB 取用（组合根装配；nil = 库根不可用，走启动降级面）。
// 管理面无猫上下文（catID 传空 = 全放），与 C# `service.ListLibs("")` 同口径。

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

const (
	// dbServiceUnavailableCode 服务未绑定机器码（与 C# DbServiceUnavailable 同码）
	dbServiceUnavailableCode = "DB_SERVICE_UNAVAILABLE"
	// dbServiceUnavailableMessage 服务未绑定原因（与 C# 同文案）
	dbServiceUnavailableMessage = "数据库服务未绑定——库根不可用（见启动健康页）"
)

// dbServiceUnavailableResponse 是段 11 的服务未绑定回执（键序 ok / code / message）。
type dbServiceUnavailableResponse struct {
	// OK 恒 false（服务未绑定）
	OK bool `json:"ok"`
	// Code 机器码
	Code string `json:"code"`
	// Message 中文原因
	Message string `json:"message"`
}

// dbResponse 是段 11 的成功回执（键序 ok / message / data / badLines，对齐 C# 匿名对象）。
type dbResponse struct {
	// OK 恒 true（失败走 dbErrorResponse）
	OK bool `json:"ok"`
	// Message 动作摘要
	Message string `json:"message"`
	// Data 载荷（服务层产出的 JSON 对象）
	Data json.RawMessage `json:"data"`
	// BadLines 跳过的坏行数
	BadLines int `json:"badLines"`
}

// dbErrorResponse 是段 11 的失败回执（键序 ok / code / message）。
type dbErrorResponse struct {
	// OK 恒 false
	OK bool `json:"ok"`
	// Code 机器码（服务层错误码 / BAD_ARGS）
	Code string `json:"code"`
	// Message 中文原因
	Message string `json:"message"`
}

// writeDbUnavailable 写「服务未绑定」回执并出声。
func (d *Deps) writeDbUnavailable(w http.ResponseWriter, op string) {
	d.log(2, "数据库端点未绑定服务（"+op+"）：受控根 db 不可用——按 DB_SERVICE_UNAVAILABLE 回执")
	writeJSON(w, dbServiceUnavailableResponse{OK: false, Code: dbServiceUnavailableCode, Message: dbServiceUnavailableMessage})
}

// writeDbFailure 写服务层失败回执（机器码 + 中文原因）。
func writeDbFailure(w http.ResponseWriter, code, message string) {
	writeJSON(w, dbErrorResponse{OK: false, Code: code, Message: message})
}

// writeDbSuccess 写服务层成功回执——载荷必须是 JSON 对象（不可读即 DB_IO_FAIL，不静默空列表）。
func (d *Deps) writeDbSuccess(w http.ResponseWriter, message, data string, badLines int) {
	var payload map[string]json.RawMessage
	if data == "" || json.Unmarshal([]byte(data), &payload) != nil {
		d.log(2, "数据库端点载荷解析失败："+data)
		writeDbFailure(w, "DB_IO_FAIL", "服务载荷解析失败")
		return
	}
	writeJSON(w, dbResponse{OK: true, Message: message, Data: json.RawMessage(data), BadLines: badLines})
}

// handleDbLibsGet 服务 GET /api/v1/db/libs——库清单（注册表登记的库 + 活跃条目计数）。
func (d *Deps) handleDbLibsGet(w http.ResponseWriter, r *http.Request) {
	if d.DB == nil {
		d.writeDbUnavailable(w, "db/libs GET")
		return
	}
	result := d.DB.ListLibs("")
	if !result.OK {
		writeDbFailure(w, result.Code, result.Message)
		return
	}
	d.writeDbSuccess(w, result.Message, result.Data, result.BadLines)
}

// handleDbLibsPost 服务 POST /api/v1/db/libs——新建库（body: {"id":"<libId>"}）。
func (d *Deps) handleDbLibsPost(w http.ResponseWriter, r *http.Request) {
	if d.DB == nil {
		d.writeDbUnavailable(w, "db/libs POST")
		return
	}
	body, err := readAll(r)
	if err != nil {
		writeDbFailure(w, "BAD_ARGS", "请求体读取失败: "+err.Error())
		return
	}
	var request struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		writeDbFailure(w, "BAD_ARGS", "请求体解析失败: "+err.Error())
		return
	}
	result := d.DB.Create(request.ID, "")
	if !result.OK {
		writeDbFailure(w, result.Code, result.Message)
		return
	}
	d.writeDbSuccess(w, result.Message, result.Data, result.BadLines)
}

// handleDbEntriesGet 服务 GET /api/v1/db/libs/{lib}/entries——条目列表（与 db-query 同语义）。
func (d *Deps) handleDbEntriesGet(w http.ResponseWriter, r *http.Request) {
	if d.DB == nil {
		d.writeDbUnavailable(w, "db/libs/{lib}/entries")
		return
	}
	lib := r.PathValue("lib")
	keyword := r.URL.Query().Get("keyword")
	limitText := r.URL.Query().Get("limit")
	limit := 0
	if limitText != "" {
		parsed, ok := tryParseAdminInt(limitText)
		if !ok {
			writeDbFailure(w, "BAD_ARGS", "limit 非法（数字）: "+limitText)
			return
		}
		limit = parsed
	}
	offsetText := r.URL.Query().Get("offset")
	offset := 0
	if offsetText != "" {
		parsed, ok := tryParseAdminInt(offsetText)
		if !ok || parsed < 0 {
			writeDbFailure(w, "BAD_ARGS", "offset 非法（非负整数）: "+offsetText)
			return
		}
		offset = parsed
	}
	order := r.URL.Query().Get("order")
	result := d.DB.Query(lib, keyword, limit, offset, order, "")
	if !result.OK {
		writeDbFailure(w, result.Code, result.Message)
		return
	}
	d.writeDbSuccess(w, result.Message, result.Data, result.BadLines)
}

// handleDbEntryGet 服务 GET /api/v1/db/libs/{lib}/entries/{entryId}——条目全文（与 db-get 按 ID 同语义）。
func (d *Deps) handleDbEntryGet(w http.ResponseWriter, r *http.Request) {
	if d.DB == nil {
		d.writeDbUnavailable(w, "db/libs/{lib}/entries/{entryId}")
		return
	}
	lib := r.PathValue("lib")
	entryID := r.PathValue("entryId")
	result := d.DB.Get(lib, "id", entryID, "")
	if !result.OK {
		writeDbFailure(w, result.Code, result.Message)
		return
	}
	d.writeDbSuccess(w, result.Message, result.Data, result.BadLines)
}

// tryParseAdminInt 整数解析——对齐 .NET int.TryParse（首尾空白容忍 + 正负号 + 溢出拒绝）。
func tryParseAdminInt(text string) (int, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, false
	}
	value, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, false
	}
	return value, true
}
