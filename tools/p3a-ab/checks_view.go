package main

// checks_view —— 视图首帧对拍（v2 契约）与 chat 页形态自证。

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// checkViewSSE 视图首帧——v2 契约：连接建立推一次全量（v / state / persist / live），
// 无变化零推送；条目字段面 = persist.items[] 五键（type/ts/msgIndex/round/payload）。
//
// C# 基线是管理面板实例（ViewFullBuilder 只在每猫对话端口实例绑定），`topics=view`
// 不推首帧——此时记 KNOWN，本用例退化为 Go 侧自证；两侧对比留待对话端口基线。
func checkViewSSE(rep *report, opts options, goSide, csSide *sideState) {
	const cls = "sse"
	goRes, goText := doStream(goSide.base, "/api/v1/stream?topics=view", 3*time.Second)
	csRes, csText := doStream(csSide.base, "/api/v1/stream?topics=view", 3*time.Second)
	saveRaw(opts.outDir, "cat5", "stream_view.txt", httpResult{status: goRes.status, header: goRes.header, body: []byte(goText), err: goRes.err})
	saveRaw(opts.outDir, "cs", "stream_view.txt", httpResult{status: csRes.status, header: csRes.header, body: []byte(csText), err: csRes.err})
	if !goRes.ok() || goRes.status != 200 {
		rep.add(cls, "view 状态码[cat5]", stFail, describeStatus(goRes))
		return
	}
	rep.add(cls, "view 状态码[cat5]", stPass, "200")
	goEvent, goData := firstSSEFrame(goText)
	if goEvent != "view" {
		rep.add(cls, "view 首帧 event[cat5]", stDiff, "event="+goEvent)
		return
	}
	rep.add(cls, "view 首帧 event[cat5]", stPass, "view")
	checkViewPayload(rep, cls, "cat5", goData)
	if !csRes.ok() || csRes.status != 200 || strings.TrimSpace(csText) == "" {
		rep.add(cls, "view 首帧[cs]", stKnown, "C# 管理面板实例不推 view 首帧（ViewFullBuilder 只在每猫对话端口实例绑定）——Go 侧自证，两侧对比留待对话端口基线")
		return
	}
	csEvent, csData := firstSSEFrame(csText)
	if csEvent != "view" {
		rep.add(cls, "view 首帧 event[cs]", stDiff, "event="+csEvent)
		return
	}
	rep.add(cls, "view 首帧 event[cs]", stPass, "view")
	checkViewPayload(rep, cls, "cs", csData)
}

// checkViewPayload 单侧视图首帧载荷检查（结构 + 键序 + 条目字段面）。
func checkViewPayload(rep *report, cls, side, data string) {
	payload, err := parseJSON([]byte(data))
	if err != nil {
		rep.add(cls, "view data JSON["+side+"]", stFail, err.Error())
		return
	}
	addSingleKeysCheck(rep, cls, "view 顶层四键["+side+"]", objectKeys(payload), []string{"v", "state", "persist", "live"})
	addSingleValueCheck(rep, cls, "view v["+side+"]", childValue(payload, "v"), "2")
	wantState := []string{"sessionId", "runState", "runMs", "requests", "note", "delay", "pending", "conn", "tokens", "meta", "pet"}
	addSingleKeysCheck(rep, cls, "view state 十一键["+side+"]", objectKeys(childValue(payload, "state")), wantState)
	addSingleValueCheck(rep, cls, "view persist.mode["+side+"]", childValue(childValue(payload, "persist"), "mode"), "full")
	items := childArray(childValue(payload, "persist"), "items")
	rep.add(cls, "view 条目数["+side+"]", stInfo, fmt.Sprintf("%d 条", len(items)))
	if len(items) > 0 {
		addSingleKeysCheck(rep, cls, "view 首条键集["+side+"]", objectKeys(items[0]), []string{"type", "ts", "msgIndex", "round", "payload"})
	}
	addSingleValueCheck(rep, cls, "view live.type["+side+"]", childValue(childValue(payload, "live"), "type"), "empty")
}

// checkGoChatPage 抓 chat 页形态（`--page chat.html`）：`/` 即对话页、`/chat.html` 404。
//
// C# 侧 chat 形态由「每猫独立端口」承载（ServeChatPage 实例），与同一基线不同源；
// 本用例只自证 Go 侧契约（L3 C 域 §一：`/` 即对话页，`/chat.html` 不在静态路由内）。
func checkGoChatPage(rep *report, opts options) {
	const cls = "static"
	port := pickPort(9182)
	proc, err := launchProcess("cat5-chat", opts.goExe,
		[]string{"--data-root", opts.dataRoot, "--html-root", opts.htmlRoot, "--area", "dev", "--port", strconv.Itoa(port), "--page", "chat.html"},
		filepath.Dir(opts.goExe), processEnv(nil), opts.outDir)
	if err != nil {
		rep.add(cls, "chat 形态实例", stFail, err.Error())
		return
	}
	defer func() { _ = proc.stop() }()
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	if err := waitReadySimple(proc, base, opts.startupTimeout); err != nil {
		rep.add(cls, "chat 形态实例", stFail, err.Error())
		return
	}
	root := doGet(base, "/", 10*time.Second)
	if !root.ok() || root.status != 200 {
		rep.add(cls, "chat 形态 GET /", stFail, describeStatus(root))
		return
	}
	if strings.Contains(string(root.body), "<title>Cat Chat</title>") {
		rep.add(cls, "chat 形态 GET /", stPass, fmt.Sprintf("Cat Chat 对话页 · %d 字节", len(root.body)))
	} else {
		rep.add(cls, "chat 形态 GET /", stDiff, "正文不是对话页: "+clip(string(root.body)))
	}
	if root.contentTypeBase() == "text/html" {
		rep.add(cls, "chat 形态 Content-Type", stPass, "text/html")
	} else {
		rep.add(cls, "chat 形态 Content-Type", stDiff, root.contentTypeBase())
	}
	direct := doGet(base, "/chat.html", 10*time.Second)
	if direct.ok() && direct.status == 404 {
		rep.add(cls, "chat 形态 /chat.html 404", stPass, "404（不在静态路由内）")
	} else {
		rep.add(cls, "chat 形态 /chat.html 404", stDiff, describeStatus(direct))
	}
	_ = proc.stop()
}

// firstSSEFrame 取首个事件块（event: 与 data: 两行）。
func firstSSEFrame(text string) (string, string) {
	event, data := "", ""
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
			if event != "" {
				return event, data
			}
		}
	}
	return event, data
}

// childValue 取对象子值（非对象 / 缺失给 nil）。
func childValue(v *jval, key string) *jval {
	if v == nil || v.kind != 'o' {
		return nil
	}
	return v.obj[key]
}

// childArray 取对象子数组（缺失给 nil）。
func childArray(v *jval, key string) []*jval {
	child := childValue(v, key)
	if child == nil || child.kind != 'a' {
		return nil
	}
	return child.arr
}

// addSingleKeysCheck 单侧键序列比对并记录。
func addSingleKeysCheck(rep *report, cls, name string, keys, want []string) {
	if sameKeys(keys, want) {
		rep.add(cls, name, stPass, strings.Join(want, ","))
		return
	}
	rep.add(cls, name, stDiff, "got="+strings.Join(keys, ",")+" want="+strings.Join(want, ","))
}

// addSingleValueCheck 单侧值比对并记录。
func addSingleValueCheck(rep *report, cls, name string, value *jval, want string) {
	got := jvalText(value)
	if got == want {
		rep.add(cls, name, stPass, want)
		return
	}
	rep.add(cls, name, stDiff, "got="+got+" want="+want)
}

// jvalText 取标量文本（对象 / 数组给形状标记）。
func jvalText(v *jval) string {
	if v == nil {
		return "<nil>"
	}
	switch v.kind {
	case 's':
		return v.str
	case 'n':
		return v.num
	case 'b':
		return strconv.FormatBool(v.boolean)
	case 'z':
		return "null"
	case 'o':
		return "{...}"
	case 'a':
		return "[...]"
	}
	return "?"
}
