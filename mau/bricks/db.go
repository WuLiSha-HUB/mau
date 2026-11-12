package bricks

// 条目库积木——DbCat 五件（db.put / db.query / db.get / db.update / db.delete）与
// DbAdmin 三件（dbAdmin.libs / dbAdmin.create / dbAdmin.drop）。
//
// 行为逐条移植 mau:Bricks/DB/BRIK-DB-00X_*.cs：参数面零容忍 → 服务注入检查（未注入报
// ERR|DB_NO_SERVICE）→ 服务层调用 → 回执文本由 Result.ToolText 单点产出（头 + 摘要 + 载荷）。
// 授权判定在服务层（唯一判定点）——积木只透传 catId（宿主保留键，参数面放行）。

import (
	"strconv"
	"strings"

	"cathome5/mau/db"
)

// DbPut 执行 db.put——新增条目（lib / title / body）。
func DbPut(svc *db.Service, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "lib title body", "lib title body", "", ""); bad != "" {
		return bad, false
	}
	lib := Get(argsJSON, "lib")
	title := Get(argsJSON, "title")
	body := Get(argsJSON, "body")
	if lib == ParseFail || title == ParseFail || body == ParseFail {
		return "ERR|BAD_ARGS|工具参数 JSON 解析失败（超长正文请精简后重试）", false
	}
	if svc == nil {
		return "ERR|DB_NO_SERVICE|宿主未注入 DbService", false
	}
	call := svc.Put(lib, title, body, Get(argsJSON, "catId"))
	return call.ToolText("db-put", lib), call.OK
}

// DbQuery 执行 db.query——查询条目（lib / keyword / limit / offset / order）。
func DbQuery(svc *db.Service, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "lib keyword limit offset order", "lib", "", ""); bad != "" {
		return bad, false
	}
	lib := Get(argsJSON, "lib")
	keyword := Get(argsJSON, "keyword")
	limitText := Get(argsJSON, "limit")
	offsetText := Get(argsJSON, "offset")
	order := Get(argsJSON, "order")
	if lib == ParseFail || keyword == ParseFail || limitText == ParseFail || offsetText == ParseFail || order == ParseFail {
		return "ERR|BAD_ARGS|工具参数 JSON 解析失败", false
	}
	limit := 0
	if limitText != "" {
		parsed, ok := parseIntStrict(limitText)
		if !ok {
			return "ERR|BAD_ARGS|limit 非整数: " + limitText, false
		}
		// [参数面] 越界即拒——非法值与「未传」必须可分（缺省只在 limitText 为空时生效）
		if parsed < 1 || parsed > 200 {
			return "ERR|BAD_ARGS|limit 越界（1–200）: " + limitText, false
		}
		limit = parsed
	}
	offset := 0
	if offsetText != "" {
		parsed, ok := parseIntStrict(offsetText)
		if !ok {
			return "ERR|BAD_ARGS|offset 非整数: " + offsetText, false
		}
		if parsed < 0 {
			return "ERR|BAD_ARGS|offset 不能为负: " + offsetText, false
		}
		offset = parsed
	}
	if svc == nil {
		return "ERR|DB_NO_SERVICE|宿主未注入 DbService", false
	}
	call := svc.Query(lib, keyword, limit, offset, order, Get(argsJSON, "catId"))
	return call.ToolText("db-query", lib), call.OK
}

// DbGet 执行 db.get——取单条全文（lib / mode / key）。
func DbGet(svc *db.Service, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "lib mode key", "lib key", "", ""); bad != "" {
		return bad, false
	}
	lib := Get(argsJSON, "lib")
	mode := Get(argsJSON, "mode")
	key := Get(argsJSON, "key")
	if lib == ParseFail || mode == ParseFail || key == ParseFail {
		return "ERR|BAD_ARGS|工具参数 JSON 解析失败", false
	}
	if svc == nil {
		return "ERR|DB_NO_SERVICE|宿主未注入 DbService", false
	}
	call := svc.Get(lib, mode, key, Get(argsJSON, "catId"))
	return call.ToolText("db-get", lib), call.OK
}

// DbUpdate 执行 db.update——更新正文（lib / title / body）。
func DbUpdate(svc *db.Service, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "lib title body", "lib title body", "", ""); bad != "" {
		return bad, false
	}
	lib := Get(argsJSON, "lib")
	title := Get(argsJSON, "title")
	body := Get(argsJSON, "body")
	if lib == ParseFail || title == ParseFail || body == ParseFail {
		return "ERR|BAD_ARGS|工具参数 JSON 解析失败（超长正文请精简后重试）", false
	}
	if svc == nil {
		return "ERR|DB_NO_SERVICE|宿主未注入 DbService", false
	}
	call := svc.Update(lib, title, body, Get(argsJSON, "catId"))
	return call.ToolText("db-update", lib), call.OK
}

// DbDelete 执行 db.delete——删除条目（lib / title）。
func DbDelete(svc *db.Service, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "lib title", "lib title", "", ""); bad != "" {
		return bad, false
	}
	lib := Get(argsJSON, "lib")
	title := Get(argsJSON, "title")
	if lib == ParseFail || title == ParseFail {
		return "ERR|BAD_ARGS|工具参数 JSON 解析失败", false
	}
	if svc == nil {
		return "ERR|DB_NO_SERVICE|宿主未注入 DbService", false
	}
	call := svc.Delete(lib, title, Get(argsJSON, "catId"))
	return call.ToolText("db-delete", lib), call.OK
}

// DbAdminLibs 执行 dbAdmin.libs——库级明细（无参数）。
func DbAdminLibs(svc *db.Service, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "", "", "", ""); bad != "" {
		return bad, false
	}
	if svc == nil {
		return "ERR|DB_NO_SERVICE|宿主未注入 DbService", false
	}
	call := svc.ListLibsDetail(Get(argsJSON, "catId"))
	return call.ToolText("dbAdmin-libs", ""), call.OK
}

// DbAdminCreate 执行 dbAdmin.create——新建库（lib）。
func DbAdminCreate(svc *db.Service, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "lib", "lib", "", ""); bad != "" {
		return bad, false
	}
	lib := Get(argsJSON, "lib")
	if svc == nil {
		return "ERR|DB_NO_SERVICE|宿主未注入 DbService", false
	}
	call := svc.Create(lib, Get(argsJSON, "catId"))
	return call.ToolText("dbAdmin-create", lib), call.OK
}

// DbAdminDrop 执行 dbAdmin.drop——删库（lib / confirm）。
func DbAdminDrop(svc *db.Service, argsJSON string) (string, bool) {
	if bad := Validate(argsJSON, "lib confirm", "lib confirm", "", ""); bad != "" {
		return bad, false
	}
	lib := Get(argsJSON, "lib")
	confirm := Get(argsJSON, "confirm")
	if svc == nil {
		return "ERR|DB_NO_SERVICE|宿主未注入 DbService", false
	}
	call := svc.DropLib(lib, confirm, Get(argsJSON, "catId"))
	return call.ToolText("dbAdmin-drop", lib), call.OK
}

// parseIntStrict 整数解析——对齐 .NET int.TryParse（首尾空白容忍 + 正负号 + 溢出拒绝）。
func parseIntStrict(text string) (int, bool) {
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
