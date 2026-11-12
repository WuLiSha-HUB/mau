package bricks

import (
	"strconv"
	"strings"
)

// 工具名常量（A214 头的 tool 字段）。
const (
	toolHTTPGet   = "http-get"
	toolHTTPPost  = "http-post"
	toolHTTPProbe = "http-probe"
)

// HTTPGet 执行 http-get 工具——取回状态码 / 响应头 / 正文（正文按上限截断，截断事实可见）。
//
// 语义对齐 C# HttpGetBrick：HTTP 4xx/5xx 属有效响应仍算成功；连不上算失败。
func HTTPGet(argsJSON string) (string, bool) {
	if badArgs := Validate(argsJSON, "url headers timeout_ms follow max_body", "url", "", ""); badArgs != "" {
		return badArgs, false
	}
	target := Get(argsJSON, "url")
	if target == ParseFail {
		return "ERR|BAD_ARGS|工具参数 JSON 解析失败（LLM 生成参数可能被截断——超长内容请分段写入）", false
	}
	if target == "" {
		return "ERR|BAD_ARGS|缺少参数 url", false
	}
	timeoutMs := 30000
	if raw := Get(argsJSON, "timeout_ms"); raw != "" {
		parsed, ok := atoiArg(raw)
		if !ok {
			return "ERR|BAD_ARGS|参数 timeout_ms 非整数: " + raw, false
		}
		timeoutMs = parsed
	}
	follow := true
	if raw := Get(argsJSON, "follow"); raw != "" {
		parsed, ok := boolArg(raw)
		if !ok {
			return "ERR|BAD_ARGS|参数 follow 非布尔: " + raw, false
		}
		follow = parsed
	}
	maxBody := 4000
	if raw := Get(argsJSON, "max_body"); raw != "" {
		parsed, ok := atoiArg(raw)
		if !ok {
			return "ERR|BAD_ARGS|参数 max_body 非整数: " + raw, false
		}
		maxBody = parsed
	}
	facts, err := HTTPSend(HTTPRequestSpec{
		URL:       target,
		Method:    "GET",
		Headers:   Get(argsJSON, "headers"),
		TimeoutMs: timeoutMs,
		Follow:    follow,
		MaxBody:   maxBody,
	})
	if err != nil {
		return "ERR|InvalidDataException|" + err.Error(), false
	}
	if facts.Unreachable {
		return "ERR|HTTP_UNREACHABLE|" + facts.Failure, false
	}
	var b strings.Builder
	b.WriteString(statusText(facts))
	b.WriteString(" · ")
	b.WriteString(formatN0(int64(facts.BodyChars)))
	b.WriteString(" 字符 · ")
	b.WriteString(strconv.FormatInt(facts.ElapsedMs, 10))
	b.WriteString(" ms")
	b.WriteString("\n请求: GET ")
	b.WriteString(facts.URL)
	b.WriteString("\n响应: ")
	b.WriteString(strconv.Itoa(facts.Status))
	b.WriteString(" ")
	b.WriteString(facts.ReasonPhrase)
	if facts.ContentType != "" {
		b.WriteString(" · ")
		b.WriteString(facts.ContentType)
	}
	if facts.ContentLength >= 0 {
		b.WriteString(" · ")
		b.WriteString(formatN0(facts.ContentLength))
		b.WriteString(" 字节")
	}
	appendHTTPFooter(&b, facts, maxBody)
	fields := [][2]any{
		{"status", facts.Status},
		{"chars", facts.BodyChars},
		{"elapsed_ms", facts.ElapsedMs},
	}
	return MetaHeadFields(toolHTTPGet, true, target, -1, fields) + "\n" + target + " | " + b.String(), true
}

// HTTPPost 执行 http-post 工具——带正文与 Content-Type，取回状态码 / 响应头 / 正文。
func HTTPPost(argsJSON string) (string, bool) {
	if badArgs := Validate(argsJSON, "url body content_type headers timeout_ms follow max_body", "url", "", ""); badArgs != "" {
		return badArgs, false
	}
	target := Get(argsJSON, "url")
	if target == ParseFail {
		return "ERR|BAD_ARGS|工具参数 JSON 解析失败（LLM 生成参数可能被截断——超长内容请分段写入）", false
	}
	if target == "" {
		return "ERR|BAD_ARGS|缺少参数 url", false
	}
	timeoutMs := 30000
	if raw := Get(argsJSON, "timeout_ms"); raw != "" {
		parsed, ok := atoiArg(raw)
		if !ok {
			return "ERR|BAD_ARGS|参数 timeout_ms 非整数: " + raw, false
		}
		timeoutMs = parsed
	}
	follow := true
	if raw := Get(argsJSON, "follow"); raw != "" {
		parsed, ok := boolArg(raw)
		if !ok {
			return "ERR|BAD_ARGS|参数 follow 非布尔: " + raw, false
		}
		follow = parsed
	}
	maxBody := 4000
	if raw := Get(argsJSON, "max_body"); raw != "" {
		parsed, ok := atoiArg(raw)
		if !ok {
			return "ERR|BAD_ARGS|参数 max_body 非整数: " + raw, false
		}
		maxBody = parsed
	}
	body := Get(argsJSON, "body")
	contentType := Get(argsJSON, "content_type")
	facts, err := HTTPSend(HTTPRequestSpec{
		URL:         target,
		Method:      "POST",
		Body:        body,
		ContentType: contentType,
		Headers:     Get(argsJSON, "headers"),
		TimeoutMs:   timeoutMs,
		Follow:      follow,
		MaxBody:     maxBody,
	})
	if err != nil {
		return "ERR|InvalidDataException|" + err.Error(), false
	}
	if facts.Unreachable {
		return "ERR|HTTP_UNREACHABLE|" + facts.Failure, false
	}
	var b strings.Builder
	b.WriteString(statusText(facts))
	b.WriteString(" · ")
	b.WriteString(formatN0(int64(facts.BodyChars)))
	b.WriteString(" 字符 · ")
	b.WriteString(strconv.FormatInt(facts.ElapsedMs, 10))
	b.WriteString(" ms")
	b.WriteString("\n请求: POST ")
	b.WriteString(facts.URL)
	b.WriteString("（正文 ")
	b.WriteString(formatN0(int64(utf16Units(body))))
	b.WriteString(" 字符")
	if body != "" {
		shownType := contentType
		if shownType == "" {
			shownType = "text/plain; charset=utf-8"
		}
		b.WriteString(" · ")
		b.WriteString(shownType)
	}
	b.WriteString("）")
	b.WriteString("\n响应: ")
	b.WriteString(strconv.Itoa(facts.Status))
	b.WriteString(" ")
	b.WriteString(facts.ReasonPhrase)
	if facts.ContentType != "" {
		b.WriteString(" · ")
		b.WriteString(facts.ContentType)
	}
	if facts.ContentLength >= 0 {
		b.WriteString(" · ")
		b.WriteString(formatN0(facts.ContentLength))
		b.WriteString(" 字节")
	}
	appendHTTPFooter(&b, facts, maxBody)
	fields := [][2]any{
		{"status", facts.Status},
		{"chars", facts.BodyChars},
		{"elapsed_ms", facts.ElapsedMs},
	}
	return MetaHeadFields(toolHTTPPost, true, target, -1, fields) + "\n" + target + " | " + b.String(), true
}

// HTTPProbe 执行 http-probe 工具——只取响应头（不下载正文）。
//
// 不可达是探测结论（仍算成功）；只有参数面非法才算失败——对齐 C# HttpProbeBrick。
func HTTPProbe(argsJSON string) (string, bool) {
	if badArgs := Validate(argsJSON, "url timeout_ms follow", "url", "", ""); badArgs != "" {
		return badArgs, false
	}
	target := Get(argsJSON, "url")
	if target == ParseFail {
		return "ERR|BAD_ARGS|工具参数 JSON 解析失败（LLM 生成参数可能被截断——超长内容请分段写入）", false
	}
	if target == "" {
		return "ERR|BAD_ARGS|缺少参数 url", false
	}
	timeoutMs := 10000
	if raw := Get(argsJSON, "timeout_ms"); raw != "" {
		parsed, ok := atoiArg(raw)
		if !ok {
			return "ERR|BAD_ARGS|参数 timeout_ms 非整数: " + raw, false
		}
		timeoutMs = parsed
	}
	// 探测默认不跟随——要看第一跳的真实状态码（要看终点再加 follow=true）
	follow := false
	if raw := Get(argsJSON, "follow"); raw != "" {
		parsed, ok := boolArg(raw)
		if !ok {
			return "ERR|BAD_ARGS|参数 follow 非布尔: " + raw, false
		}
		follow = parsed
	}
	facts, err := HTTPSend(HTTPRequestSpec{
		URL:       target,
		Method:    "GET",
		TimeoutMs: timeoutMs,
		Follow:    follow,
		MaxBody:   0,
	})
	if err != nil {
		return "ERR|InvalidDataException|" + err.Error(), false
	}
	var b strings.Builder
	if facts.Unreachable {
		b.WriteString("不可达 · ")
		b.WriteString(strconv.FormatInt(facts.ElapsedMs, 10))
		b.WriteString(" ms")
		b.WriteString("\n请求: GET ")
		b.WriteString(facts.URL)
		b.WriteString("（只取响应头）")
		b.WriteString("\n原因: ")
		b.WriteString(facts.Failure)
		fields := [][2]any{
			{"status", 0},
			{"elapsed_ms", facts.ElapsedMs},
		}
		return MetaHeadFields(toolHTTPProbe, true, target, -1, fields) + "\n" + target + " | " + b.String(), true
	}
	b.WriteString(statusText(facts))
	b.WriteString(" · ")
	b.WriteString(strconv.FormatInt(facts.ElapsedMs, 10))
	b.WriteString(" ms")
	b.WriteString("\n请求: GET ")
	b.WriteString(facts.URL)
	b.WriteString("（只取响应头）")
	b.WriteString("\n响应: ")
	b.WriteString(strconv.Itoa(facts.Status))
	b.WriteString(" ")
	b.WriteString(facts.ReasonPhrase)
	if facts.ContentType != "" {
		b.WriteString(" · ")
		b.WriteString(facts.ContentType)
	}
	if facts.ContentLength >= 0 {
		b.WriteString(" · ")
		b.WriteString(formatN0(facts.ContentLength))
		b.WriteString(" 字节")
	}
	if facts.UsedProxy {
		b.WriteString("\n[代理] 经本机系统代理——代理自身故障也会回 502/504，不一定是目标站点")
	}
	if facts.Redirected {
		b.WriteString("\n[重定向] 已跟随 → ")
		b.WriteString(facts.FinalURL)
	} else if facts.Location != "" {
		b.WriteString("\n[重定向] 未跟随（follow=false）· Location: ")
		b.WriteString(facts.Location)
		b.WriteString("\n[提示] 加 follow=true 可跟到终点看最终状态码")
	}
	fields := [][2]any{
		{"status", facts.Status},
		{"elapsed_ms", facts.ElapsedMs},
	}
	return MetaHeadFields(toolHTTPProbe, true, target, -1, fields) + "\n" + target + " | " + b.String(), true
}

// appendHTTPFooter 追加代理 / 重定向 / 截断 / 正文四段（http-get 与 http-post 共用）。
func appendHTTPFooter(b *strings.Builder, facts HTTPFacts, maxBody int) {
	if facts.UsedProxy {
		b.WriteString("\n[代理] 经本机系统代理——代理自身故障也会回 502/504，不一定是目标站点")
	}
	if facts.Redirected {
		b.WriteString("\n[重定向] 已跟随 → ")
		b.WriteString(facts.FinalURL)
	} else if facts.Location != "" {
		b.WriteString("\n[重定向] 未跟随（follow=false）· Location: ")
		b.WriteString(facts.Location)
	}
	if facts.Truncated {
		b.WriteString("\n[截断] 正文 ")
		b.WriteString(formatN0(int64(facts.BodyChars)))
		b.WriteString(" 字符，已列 ")
		b.WriteString(formatN0(int64(maxBody)))
		b.WriteString(" 字符（max_body 提额可看全 / 0 = 只取响应头）")
	}
	if maxBody == 0 {
		b.WriteString("\n[正文] 未读取（max_body=0——只取响应头）")
		return
	}
	b.WriteString("\n正文:")
	b.WriteString("\n")
	b.WriteString(facts.Body)
}

// statusText 状态展示——`200 OK` / `404 Not Found`（无短语时只给码）。
func statusText(facts HTTPFacts) string {
	if facts.ReasonPhrase == "" {
		return strconv.Itoa(facts.Status)
	}
	return strconv.Itoa(facts.Status) + " " + facts.ReasonPhrase
}

// atoiArg 解析整型参数——对齐 int.TryParse（允许首尾空白与前导正负号）。
func atoiArg(raw string) (int, bool) {
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, false
	}
	return parsed, true
}

// formatN0 千分位整数渲染——对齐 C# `ToString("N0")`。
func formatN0(value int64) string {
	negative := value < 0
	if negative {
		value = -value
	}
	digits := strconv.FormatInt(value, 10)
	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}
	for i := 0; i < len(digits); i++ {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(digits[i])
	}
	return b.String()
}
