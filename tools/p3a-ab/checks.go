package main

// checks —— 8 类响应的对拍入口与公共件。

import (
	"fmt"
	"time"
)

// fetchBoth 抓同一路径的两侧响应并落盘原始证据。
func fetchBoth(outDir, name string, goSide, csSide *sideState, path string, timeout time.Duration) (httpResult, httpResult) {
	goRes := doGet(goSide.base, path, timeout)
	csRes := doGet(csSide.base, path, timeout)
	saveRaw(outDir, "cat5", name, goRes)
	saveRaw(outDir, "cs", name, csRes)
	return goRes, csRes
}

// require200 判两侧均 200；返回是否可继续。
func require200(rep *report, class, item string, goRes, csRes httpResult) bool {
	if !goRes.ok() || !csRes.ok() {
		rep.add(class, item, stFail, fmt.Sprintf("抓取失败 cat5=%s cs=%s", describeStatus(goRes), describeStatus(csRes)))
		return false
	}
	if goRes.status != 200 || csRes.status != 200 {
		rep.add(class, item, stFail, fmt.Sprintf("状态码 cat5=%d cs=%d", goRes.status, csRes.status))
		return false
	}
	rep.add(class, item, stPass, "200/200")
	return true
}

// parseSideJSON 解析单侧 JSON；失败记 FAIL。
func parseSideJSON(rep *report, class, sideName string, res httpResult) *jval {
	v, err := parseJSON(res.body)
	if err != nil {
		rep.add(class, "JSON 解析["+sideName+"]", stFail, err.Error())
		return nil
	}
	return v
}

// statusPair 描述两侧状态码。
func statusPair(goRes, csRes httpResult) string {
	return fmt.Sprintf("cat5=%d cs=%d", goRes.status, csRes.status)
}

// runAllChecks 跑 8 类对拍。
func runAllChecks(rep *report, opts options, goSide, csSide *sideState) {
	checkConfig(rep, opts, goSide, csSide)
	checkSnapshot(rep, opts, goSide, csSide)
	checkLogs(rep, opts, goSide, csSide)
	checkStatic(rep, opts, goSide, csSide)
	checkGoChatPage(rep, opts)
	checkSSE(rep, opts, goSide, csSide)
	checkViewSSE(rep, opts, goSide, csSide)
	checkNotFound(rep, opts, goSide, csSide)
	checkErrorFace(rep, opts, goSide, csSide)
	checkRunDirs(rep, opts, goSide, csSide)
	checkAdmin(rep, opts, goSide, csSide)
	checkAdminA2(rep, opts, goSide, csSide)
}
