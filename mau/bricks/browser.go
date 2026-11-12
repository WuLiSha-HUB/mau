package bricks

// browser 六件积木——BrowserCat 工具执行面（A110 本地浏览器能力）。
//
// 语义对齐 C# BRIK-BROWSER-001..006：参数面零容忍 → 服务调用 → A214 结构化头 + 摘要行 + 正文。
// 服务层错误（ERR| 前缀）按工具成功返回——让 Dog 拿到失败原因；参数面错误按工具失败返回。
// 域门禁（browser-* 域内专用 / browser-headful 域外专属）归宿主派发面判定，积木不重复判定。

import (
	"strconv"
	"strings"
)

// 工具名常量（A214 头的 tool 字段）。
const (
	toolBrowserOpen    = "browser-open"
	toolBrowserRead    = "browser-read"
	toolBrowserEval    = "browser-eval"
	toolBrowserShot    = "browser-shot"
	toolBrowserTabs    = "browser-tabs"
	toolBrowserHeadful = "browser-headful"
)

// BrowserOpen 执行 browser-open 工具——打开前端页面并等待加载完成。
func BrowserOpen(svc *BrowserService, argsJSON string) (string, bool) {
	if badArgs := Validate(argsJSON, "url", "url", "", ""); badArgs != "" {
		return badArgs, false
	}
	catID := Get(argsJSON, "catId")
	targetURL := Get(argsJSON, "url")
	if targetURL == "" {
		return "ERR|BAD_ARGS|缺少参数 url", false
	}
	if svc == nil {
		return "ERR|BROWSER_NO_SERVICE|宿主未注入 IBrowserService", false
	}
	body := svc.Open(catID, targetURL)
	if strings.HasPrefix(body, "ERR|") {
		return body, true
	}
	chars := utf16Units(body)
	head := MetaHeadFields(toolBrowserOpen, true, targetURL, -1, [][2]any{{"chars", chars}})
	result := head + "\n" + targetURL + " | " + strconv.Itoa(chars) + " 字"
	if body != "" {
		result += "\n" + body
	}
	return result, true
}

// BrowserRead 执行 browser-read 工具——读取当前页面（text / ax / links），产物落盘 + 摘要。
func BrowserRead(svc *BrowserService, argsJSON string) (string, bool) {
	if badArgs := Validate(argsJSON, "mode", "", "mode", "text|ax|links"); badArgs != "" {
		return badArgs, false
	}
	catID := Get(argsJSON, "catId")
	mode := Get(argsJSON, "mode")
	if mode == "" {
		mode = "text"
	}
	if svc == nil {
		return "ERR|BROWSER_NO_SERVICE|宿主未注入 IBrowserService", false
	}
	body := svc.Read(catID, mode)
	if strings.HasPrefix(body, "ERR|") {
		return body, true
	}
	chars := utf16Units(body)
	head := MetaHeadFields(toolBrowserRead, true, "", -1, [][2]any{{"mode", mode}, {"chars", chars}})
	result := head + "\n读 " + strconv.Itoa(chars) + " 字"
	if body != "" {
		result += "\n" + body
	}
	return result, true
}

// BrowserEval 执行 browser-eval 工具——在当前页面执行 JS 表达式（小直返 / 大落盘）。
func BrowserEval(svc *BrowserService, argsJSON string) (string, bool) {
	if badArgs := Validate(argsJSON, "expression", "expression", "", ""); badArgs != "" {
		return badArgs, false
	}
	catID := Get(argsJSON, "catId")
	expression := Get(argsJSON, "expression")
	if expression == "" {
		return "ERR|BAD_ARGS|缺少参数 expression", false
	}
	if svc == nil {
		return "ERR|BROWSER_NO_SERVICE|宿主未注入 IBrowserService", false
	}
	body := svc.Eval(catID, expression)
	if strings.HasPrefix(body, "ERR|") {
		return body, true
	}
	chars := utf16Units(body)
	head := MetaHeadFields(toolBrowserEval, true, "", -1, [][2]any{{"chars", chars}})
	result := head + "\n取值 " + strconv.Itoa(chars) + " 字"
	if body != "" {
		result += "\n" + body
	}
	return result, true
}

// BrowserShot 执行 browser-shot 工具——截图当前页面（PNG 落盘，返回绝对路径）。
func BrowserShot(svc *BrowserService, argsJSON string) (string, bool) {
	if badArgs := Validate(argsJSON, "full", "", "", ""); badArgs != "" {
		return badArgs, false
	}
	catID := Get(argsJSON, "catId")
	fullPage := Get(argsJSON, "full") == "true"
	if svc == nil {
		return "ERR|BROWSER_NO_SERVICE|宿主未注入 IBrowserService", false
	}
	body := svc.Shot(catID, fullPage)
	if strings.HasPrefix(body, "ERR|") {
		return body, true
	}
	chars := utf16Units(body)
	head := MetaHeadFields(toolBrowserShot, true, "", -1, [][2]any{{"full", fullPage}, {"chars", chars}})
	result := head + "\n截图 · " + strconv.Itoa(chars) + " 字"
	if body != "" {
		result += "\n" + body
	}
	return result, true
}

// BrowserTabs 执行 browser-tabs 工具——页签管理（list / new / select / close）。
func BrowserTabs(svc *BrowserService, argsJSON string) (string, bool) {
	if badArgs := Validate(argsJSON, "action target url", "", "action", "list|new|select|close"); badArgs != "" {
		return badArgs, false
	}
	catID := Get(argsJSON, "catId")
	action := Get(argsJSON, "action")
	target := Get(argsJSON, "target")
	url := Get(argsJSON, "url")
	if svc == nil {
		return "ERR|BROWSER_NO_SERVICE|宿主未注入 IBrowserService", false
	}
	body := svc.Tabs(catID, action, target, url)
	if strings.HasPrefix(body, "ERR|") {
		return body, true
	}
	chars := utf16Units(body)
	head := MetaHeadFields(toolBrowserTabs, true, action, -1, [][2]any{{"chars", chars}})
	result := head + "\n" + action + " | " + strconv.Itoa(chars) + " 字"
	if body != "" {
		result += "\n" + body
	}
	return result, true
}

// BrowserHeadful 执行 browser-headful 工具——有头登录实例（open 起窗口 / close 关窗口）。
func BrowserHeadful(svc *BrowserService, argsJSON string) (string, bool) {
	if badArgs := Validate(argsJSON, "action url", "", "", ""); badArgs != "" {
		return badArgs, false
	}
	catID := Get(argsJSON, "catId")
	action := Get(argsJSON, "action")
	url := Get(argsJSON, "url")
	if action == "" {
		action = "open"
	}
	if svc == nil {
		return "ERR|BROWSER_NO_SERVICE|宿主未注入 IBrowserLifecycle", false
	}
	body := svc.Headful(catID, action, url)
	if strings.HasPrefix(body, "ERR|") {
		return body, true
	}
	chars := utf16Units(body)
	head := MetaHeadFields(toolBrowserHeadful, true, action, -1, [][2]any{{"chars", chars}})
	result := head + "\n" + action + " | " + strconv.Itoa(chars) + " 字"
	if body != "" {
		result += "\n" + body
	}
	return result, true
}
