package main

// checks_http —— static / SSE 首帧 / 404 面。

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// versionQueryPattern 匹配静态页里的 cache-busting 版本参数（?v=1.8.262 → ?v=<V>）。
var versionQueryPattern = regexp.MustCompile(`\?v=([^"'&\s]+)`)

// normalizeIndexBody 归一化主页正文里的版本号：?v= 参数 + __V__ 替换后的裸版本串。
func normalizeIndexBody(body []byte) []byte {
	s := string(body)
	if m := versionQueryPattern.FindStringSubmatch(s); m != nil {
		v := m[1]
		s = strings.ReplaceAll(s, "?v="+v, "?v=<V>")
		s = strings.ReplaceAll(s, v, "<V>")
	}
	return []byte(s)
}

// checkStatic 对拍静态页与静态资源（两侧共用同一 html 根）。
func checkStatic(rep *report, opts options, goSide, csSide *sideState) {
	const cls = "static"
	// 主页
	goRes, csRes := fetchBoth(opts.outDir, "index.html", goSide, csSide, "/", 10*time.Second)
	if require200(rep, cls, "GET /", goRes, csRes) {
		gBody := normalizeIndexBody(goRes.body)
		cBody := normalizeIndexBody(csRes.body)
		if string(gBody) == string(cBody) {
			rep.add(cls, "主页正文（__V__ 归一）", stPass, fmt.Sprintf("%d 字节", len(gBody)))
		} else {
			rep.add(cls, "主页正文（__V__ 归一）", stDiff, "字节不同: "+firstByteDiff(gBody, cBody))
		}
		gCT, cCT := goRes.contentTypeBase(), csRes.contentTypeBase()
		if gCT == cCT {
			rep.add(cls, "主页 Content-Type", stPass, gCT)
		} else {
			rep.add(cls, "主页 Content-Type", stDiff, gCT+" != "+cCT)
		}
		gCC, cCC := goRes.headerValue("Cache-Control"), csRes.headerValue("Cache-Control")
		if gCC == cCC {
			rep.add(cls, "主页 Cache-Control", stPass, gCC)
		} else {
			rep.add(cls, "主页 Cache-Control", stDiff, gCC+" != "+cCC)
		}
	}
	// 静态资源
	checkAsset(rep, opts, goSide, csSide, "css/style.css", "text/css")
	checkAsset(rep, opts, goSide, csSide, "js/app.js", "application/javascript")
	checkAsset(rep, opts, goSide, csSide, "favicon.ico", "image/x-icon")
}

// checkAsset 对拍单个静态资源（字节级）。
func checkAsset(rep *report, opts options, goSide, csSide *sideState, path, wantMime string) {
	const cls = "static"
	name := strings.ReplaceAll(path, "/", "_")
	goRes, csRes := fetchBoth(opts.outDir, name, goSide, csSide, "/"+path, 10*time.Second)
	if !goRes.ok() || !csRes.ok() {
		rep.add(cls, path, stFail, describeStatus(goRes)+" / "+describeStatus(csRes))
		return
	}
	if goRes.status != csRes.status {
		rep.add(cls, path, stFail, statusPair(goRes, csRes))
		return
	}
	if goRes.status != 200 {
		rep.add(cls, path, stPass, fmt.Sprintf("两侧同状态 %d（资源未提供）", goRes.status))
		return
	}
	if string(goRes.body) == string(csRes.body) {
		rep.add(cls, path+" 字节", stPass, fmt.Sprintf("%d 字节", len(goRes.body)))
	} else {
		rep.add(cls, path+" 字节", stDiff, firstByteDiff(goRes.body, csRes.body))
	}
	gCT, cCT := goRes.contentTypeBase(), csRes.contentTypeBase()
	if gCT == cCT && gCT == wantMime {
		rep.add(cls, path+" MIME", stPass, gCT)
	} else {
		rep.add(cls, path+" MIME", stDiff, fmt.Sprintf("%s/%s（期望 %s）", gCT, cCT, wantMime))
	}
	gCC, cCC := goRes.headerValue("Cache-Control"), csRes.headerValue("Cache-Control")
	if gCC == cCC {
		rep.add(cls, path+" Cache-Control", stPass, gCC)
	} else {
		rep.add(cls, path+" Cache-Control", stDiff, gCC+" != "+cCC)
	}
}

// firstByteDiff 描述首个字节差异。
func firstByteDiff(a, b []byte) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return fmt.Sprintf("偏移 %d: %q != %q（长度 %d/%d）", i, byteContext(a, i), byteContext(b, i), len(a), len(b))
		}
	}
	return fmt.Sprintf("长度 %d != %d", len(a), len(b))
}

// byteContext 取差异位置附近文本。
func byteContext(b []byte, i int) string {
	start := i - 20
	if start < 0 {
		start = 0
	}
	end := i + 20
	if end > len(b) {
		end = len(b)
	}
	return string(b[start:end])
}

// checkSSE 对拍 SSE 首帧（四行：id / event / data / 空行）。
func checkSSE(rep *report, opts options, goSide, csSide *sideState) {
	const cls = "sse"
	goRes, goText := doStream(goSide.base, "/api/v1/stream?topics=snapshot", 3*time.Second)
	csRes, csText := doStream(csSide.base, "/api/v1/stream?topics=snapshot", 3*time.Second)
	saveRaw(opts.outDir, "cat5", "stream.txt", httpResult{status: goRes.status, header: goRes.header, body: []byte(goText), err: goRes.err})
	saveRaw(opts.outDir, "cs", "stream.txt", httpResult{status: csRes.status, header: csRes.header, body: []byte(csText), err: csRes.err})
	if !goRes.ok() || !csRes.ok() {
		rep.add(cls, "连接", stFail, describeStatus(goRes)+" / "+describeStatus(csRes))
		return
	}
	if goRes.status != 200 || csRes.status != 200 {
		rep.add(cls, "状态码", stFail, statusPair(goRes, csRes))
		return
	}
	rep.add(cls, "状态码", stPass, "200/200")
	if goRes.contentTypeBase() == "text/event-stream" && csRes.contentTypeBase() == "text/event-stream" {
		rep.add(cls, "Content-Type", stPass, "text/event-stream")
	} else {
		rep.add(cls, "Content-Type", stDiff, goRes.contentTypeBase()+" != "+csRes.contentTypeBase())
	}
	checkSSEFrame(rep, "cat5", goText)
	checkSSEFrame(rep, "cs", csText)
}

// checkSSEFrame 校验单侧首帧四行结构。
func checkSSEFrame(rep *report, side, text string) {
	const cls = "sse"
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) < 4 {
		rep.add(cls, "首帧四行["+side+"]", stFail, fmt.Sprintf("仅 %d 行: %q", len(lines), clip(text)))
		return
	}
	if lines[0] != "id: 1" {
		rep.add(cls, "首帧 id["+side+"]", stDiff, lines[0])
	} else {
		rep.add(cls, "首帧 id["+side+"]", stPass, "id: 1")
	}
	if lines[1] != "event: snapshot" {
		rep.add(cls, "首帧 event["+side+"]", stDiff, lines[1])
	} else {
		rep.add(cls, "首帧 event["+side+"]", stPass, "event: snapshot")
	}
	if !strings.HasPrefix(lines[2], "data: ") {
		rep.add(cls, "首帧 data 行["+side+"]", stFail, clip(lines[2]))
		return
	}
	data := strings.TrimPrefix(lines[2], "data: ")
	v, err := parseJSON([]byte(data))
	if err != nil {
		rep.add(cls, "首帧 data JSON["+side+"]", stFail, err.Error())
		return
	}
	keys := objectKeys(v)
	want := []string{"version", "pid", "frame", "cats", "sessions", "oa", "tools", "boot", "boxes", "deps", "res", "logs"}
	if sameKeys(keys, want) {
		rep.add(cls, "首帧 data 字段["+side+"]", stPass, "十二字段")
	} else {
		rep.add(cls, "首帧 data 字段["+side+"]", stDiff, keysText(keys))
	}
	if len(lines) >= 4 && lines[3] != "" {
		rep.add(cls, "首帧结束空行["+side+"]", stDiff, "第 4 行非空")
	} else {
		rep.add(cls, "首帧结束空行["+side+"]", stPass, "四行齐备")
	}
}

// checkNotFound 对拍 404 面（未注册条件路由 / 缺失静态资源）。
func checkNotFound(rep *report, opts options, goSide, csSide *sideState) {
	const cls = "404"
	goRes, csRes := fetchBoth(opts.outDir, "notfound_api_role.txt", goSide, csSide, "/api/v1/api-role", 10*time.Second)
	if !goRes.ok() || !csRes.ok() {
		rep.add(cls, "GET /api/v1/api-role（未注册条件路由）", stFail, describeStatus(goRes)+" / "+describeStatus(csRes))
	} else if goRes.status == 404 && csRes.status == 404 {
		bodyNote := "两侧 404"
		if string(goRes.body) != string(csRes.body) {
			bodyNote += "；体形态不同（" + bodyShape(goRes.body) + " / " + bodyShape(csRes.body) + "）"
			rep.add(cls, "GET /api/v1/api-role（未注册条件路由）", stKnown, bodyNote+"——前端不消费未注册路由")
		} else {
			rep.add(cls, "GET /api/v1/api-role（未注册条件路由）", stPass, bodyNote)
		}
	} else {
		rep.add(cls, "GET /api/v1/api-role（未注册条件路由）", stDiff, statusPair(goRes, csRes))
	}
	// 条件注册对照：C# 主端口注册 cats（catsBuilder 非空），Go P3-a 未绑定
	goCats, csCats := fetchBoth(opts.outDir, "cats.json", goSide, csSide, "/api/v1/cats", 10*time.Second)
	if goCats.ok() && csCats.ok() {
		if goCats.status == csCats.status {
			rep.add(cls, "GET /api/v1/cats（条件注册对照）", stPass, statusPair(goCats, csCats))
		} else {
			rep.add(cls, "GET /api/v1/cats（条件注册对照）", stKnown, fmt.Sprintf("%s——P3-b 未绑定 catsBuilder", statusPair(goCats, csCats)))
		}
	}
	// 静态缺失资源
	goMiss, csMiss := fetchBoth(opts.outDir, "notfound_css.txt", goSide, csSide, "/css/nope.css", 10*time.Second)
	if goMiss.ok() && csMiss.ok() && goMiss.status == 404 && csMiss.status == 404 {
		rep.add(cls, "GET /css/nope.css（缺失资源）", stPass, "404/404")
	} else {
		rep.add(cls, "GET /css/nope.css（缺失资源）", stDiff, statusPair(goMiss, csMiss))
	}
}

// bodyShape 描述 404 体形态。
func bodyShape(b []byte) string {
	if len(b) == 0 {
		return "空体"
	}
	return fmt.Sprintf("%d 字节文本", len(b))
}
