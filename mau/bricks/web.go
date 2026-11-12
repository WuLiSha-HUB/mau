package bricks

// 工具名常量（A214 头的 tool 字段）。
const (
	toolWebFetch     = "web-fetch"
	toolWebFetchJobs = "web-fetch-jobs"
)

// WebFetch 执行 web.fetch 工具——推入下载任务（登记即返回，非阻塞）。
//
// 语义对齐 C# WebFetchBrick：服务层返回的 ERR| 文本按「工具成功 + 回执」处理（Dog 可见失败原因）。
func WebFetch(svc *WebFetchService, argsJSON string) (string, bool) {
	if badArgs := Validate(argsJSON, "url file_name", "url", "", ""); badArgs != "" {
		return badArgs, false
	}
	catKey := Get(argsJSON, "catId")
	target := Get(argsJSON, "url")
	fileName := Get(argsJSON, "file_name")
	if svc == nil {
		return "ERR|DOWNLOAD_NO_SERVICE|宿主未注入 IWebFetchService", false
	}
	body := svc.Fetch(catKey, target, fileName)
	if hasERREPrefix(body) {
		return body, true
	}
	fields := [][2]any{{"chars", utf16Units(body)}}
	return MetaHeadFields(toolWebFetch, true, target, -1, fields) + "\n" + body, true
}

// WebFetchJobs 执行 web.fetch.jobs 工具——任务管理（list / status / cancel）。
func WebFetchJobs(svc *WebFetchService, argsJSON string) (string, bool) {
	if badArgs := Validate(argsJSON, "action id", "", "action", "list|status|cancel"); badArgs != "" {
		return badArgs, false
	}
	action := Get(argsJSON, "action")
	id := Get(argsJSON, "id")
	if svc == nil {
		return "ERR|DOWNLOAD_NO_SERVICE|宿主未注入 IWebFetchService", false
	}
	body := svc.Jobs(action, id)
	if hasERREPrefix(body) {
		return body, true
	}
	fields := [][2]any{{"chars", utf16Units(body)}}
	return MetaHeadFields(toolWebFetchJobs, true, action, -1, fields) + "\n" + body, true
}

// hasERREPrefix 判定服务层回执是否为错误文本。
func hasERREPrefix(body string) bool {
	return len(body) >= 4 && body[:4] == "ERR|"
}
