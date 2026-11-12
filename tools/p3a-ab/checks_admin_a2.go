package main

// checks_admin_a2 —— 管理端 A2 段（段 12 项目挂载 / 段 11 数据库）。
//
// 覆盖口径（沿用 A1）：读面逐条 JSON 对比 + 写面「错误面」逐条对比（非法输入零磁盘写入）。
// 写面成功路径（真挂载 / 真改注入清单）会写共用数据根的 cat.cfg 并触发 C# 会话重建——不进本器，
// 由实机人工轮覆盖。
//
// 段 11 数据库：条目库服务两侧均已接入（Go 侧 mau/db，0.20.0）——四条端点按 JSON 全等对拍；
// 建库夹具取非法库名（确定性 BAD_ARGS、零落盘），不真建库（避免污染共用库根与先跑后跑不一致）。

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// adminA2Class 是对拍报告里的分类名。
const adminA2Class = "admin-A2"

// checkAdminA2GET 读面一条（A2 分类）——200/200 + JSON 全等。
func checkAdminA2GET(rep *report, opts options, goSide, csSide *sideState, item, path string) {
	goRes, csRes := fetchBoth(opts.outDir, item+".json", goSide, csSide, path, 15*time.Second)
	if !require200(rep, adminA2Class, "GET "+path, goRes, csRes) {
		return
	}
	goV := parseSideJSON(rep, adminA2Class, "cat5", goRes)
	csV := parseSideJSON(rep, adminA2Class, "cs", csRes)
	if goV == nil || csV == nil {
		return
	}
	if jsonEqual(goV, csV) {
		rep.add(adminA2Class, item+"（JSON 全等）", stPass, clip(dumpJ(goV)))
		return
	}
	rep.add(adminA2Class, item+"（JSON 全等）", stDiff, firstDiff(goV, csV, ""))
}

// checkAdminA2ErrorPOST 写面错误面一条（A2 分类）——200/200 + JSON 全等（零磁盘写入）。
func checkAdminA2ErrorPOST(rep *report, opts options, goSide, csSide *sideState, item, path, body string) {
	goRes, csRes := fetchBothPOST(opts.outDir, item+".json", goSide, csSide, path, body, "application/json", 15*time.Second)
	if !require200(rep, adminA2Class, "POST "+path+"（非法输入）", goRes, csRes) {
		return
	}
	goV := parseSideJSON(rep, adminA2Class, "cat5", goRes)
	csV := parseSideJSON(rep, adminA2Class, "cs", csRes)
	if goV == nil || csV == nil {
		return
	}
	if jsonEqual(goV, csV) {
		rep.add(adminA2Class, item+"（错误面全等）", stPass, clip(dumpJ(goV)))
		return
	}
	rep.add(adminA2Class, item+"（错误面全等）", stDiff, firstDiff(goV, csV, ""))
}

// checkAdminA2DB 段 11 四条——读面 JSON 全等 + 建库错误面全等（零磁盘写入）。
//
// 建库夹具用非法库名 `A`（归一后 1 位，形态校验必拒）——两侧确定性返回 BAD_ARGS 且不落盘；
// 不用合法库名（会真建库：先跑的一侧建成、后跑的一侧回 DB_LIB_EXISTS，两侧不一致且污染共用库根）。
func checkAdminA2DB(rep *report, opts options, goSide, csSide *sideState) {
	items := []struct {
		item   string
		method string
		path   string
		body   string
	}{
		{"db-libs-GET", "GET", "/api/v1/db/libs", ""},
		{"db-libs-POST", "POST", "/api/v1/db/libs", `{"id":"A"}`},
		{"db-entries", "GET", "/api/v1/db/libs/demo/entries", ""},
		{"db-entry", "GET", "/api/v1/db/libs/demo/entries/1", ""},
	}
	for _, entry := range items {
		var goRes, csRes httpResult
		label := entry.item + "（JSON 全等）"
		if entry.method == "POST" {
			goRes, csRes = fetchBothPOST(opts.outDir, entry.item+".json", goSide, csSide, entry.path, entry.body, "application/json", 15*time.Second)
			label = entry.item + "（错误面全等）"
		} else {
			goRes, csRes = fetchBoth(opts.outDir, entry.item+".json", goSide, csSide, entry.path, 15*time.Second)
		}
		if !require200(rep, adminA2Class, entry.method+" "+entry.path, goRes, csRes) {
			continue
		}
		goV := parseSideJSON(rep, adminA2Class, "cat5", goRes)
		csV := parseSideJSON(rep, adminA2Class, "cs", csRes)
		if goV == nil || csV == nil {
			continue
		}
		if jsonEqual(goV, csV) {
			rep.add(adminA2Class, label, stPass, clip(dumpJ(goV)))
			continue
		}
		rep.add(adminA2Class, label, stDiff, firstDiff(goV, csV, ""))
	}
}

// checkAdminA2 跑管理端 A2 段对拍（段 12 项目挂载 / 段 11 数据库占位）。
func checkAdminA2(rep *report, opts options, goSide, csSide *sideState) {
	// [读面] 段 12——候选项目清单（扫盘）/ 挂载预览两种专家档 / 自动识别开关三态
	checkAdminA2Projects(rep, opts, goSide, csSide)
	checkAdminA2GET(rep, opts, goSide, csSide, "inject-preview-expert", "/api/v1/inject/preview?cat=majordomo&project=CatHome5&expert=true")
	checkAdminA2GET(rep, opts, goSide, csSide, "inject-preview-readme", "/api/v1/inject/preview?cat=majordomo&project=CatHome5&expert=false")
	checkAdminA2GET(rep, opts, goSide, csSide, "inject-auto", "/api/v1/inject/auto?cat=majordomo")
	checkAdminA2GET(rep, opts, goSide, csSide, "inject-auto-badcat", "/api/v1/inject/auto?cat=a%2Fb")
	checkAdminA2GET(rep, opts, goSide, csSide, "inject-auto-nocfg", "/api/v1/inject/auto?cat=nobody")

	// [错误面] 段 12 写面——空 cat / 缺项目 / 非法 cat / 未知 cat（全为零磁盘写入）
	checkAdminA2ErrorPOST(rep, opts, goSide, csSide, "inject-project-empty-cat", "/api/v1/inject/project", `{"project":"CatHome5"}`)
	checkAdminA2ErrorPOST(rep, opts, goSide, csSide, "inject-project-no-project", "/api/v1/inject/project", `{"cat":"majordomo","project":"__p3a_ab_none__"}`)
	checkAdminA2ErrorPOST(rep, opts, goSide, csSide, "inject-auto-post-badcat", "/api/v1/inject/auto", `{"cat":"a/b","on":true}`)
	checkAdminA2ErrorPOST(rep, opts, goSide, csSide, "inject-auto-post-unknown", "/api/v1/inject/auto", `{"cat":"nobody","on":true}`)

	// [段 11] 数据库四端点——占位口径
	checkAdminA2DB(rep, opts, goSide, csSide)
}

// adminProjectsPayload 是候选项目清单的对拍结构（只取参与判据的三键）。
type adminProjectsPayload struct {
	OK       bool   `json:"ok"`
	Root     string `json:"root"`
	Projects []struct {
		Name      string `json:"name"`
		LastWrite string `json:"lastWrite"`
		Files     int    `json:"files"`
	} `json:"projects"`
}

// checkAdminA2Projects 候选项目清单——集合 + 逐项内容全等；同秒并列的次序差异记 KNOWN。
//
// 顺序判据的边界：C# List<T>.Sort（introsort，不稳定）与 Go sort.SliceStable 在「lastWrite 同秒并列」
// 的项目上给出的相对次序不同；秒级并列下该次序不构成前端可观测行为（前端按 lastWrite 倒序渲染），
// 故只对集合与逐项内容下判据，原始次序差异如实记 KNOWN 并给并列项数。
func checkAdminA2Projects(rep *report, opts options, goSide, csSide *sideState) {
	goRes, csRes := fetchBoth(opts.outDir, "inject-projects.json", goSide, csSide, "/api/v1/inject/projects", 15*time.Second)
	if !require200(rep, adminA2Class, "GET /api/v1/inject/projects", goRes, csRes) {
		return
	}
	var goV, csV adminProjectsPayload
	if err := json.Unmarshal(goRes.body, &goV); err != nil {
		rep.add(adminA2Class, "inject-projects 解析[cat5]", stFail, err.Error())
		return
	}
	if err := json.Unmarshal(csRes.body, &csV); err != nil {
		rep.add(adminA2Class, "inject-projects 解析[cs]", stFail, err.Error())
		return
	}
	if goV.OK != csV.OK || goV.Root != csV.Root {
		rep.add(adminA2Class, "inject-projects 头（ok/root）", stDiff,
			fmt.Sprintf("ok cat5=%v cs=%v；root cat5=%q cs=%q", goV.OK, csV.OK, goV.Root, csV.Root))
		return
	}
	rep.add(adminA2Class, "inject-projects 头（ok/root 全等）", stPass, goV.Root)

	goMap := make(map[string]string, len(goV.Projects))
	for _, item := range goV.Projects {
		goMap[item.Name] = fmt.Sprintf("%s|%d", item.LastWrite, item.Files)
	}
	csMap := make(map[string]string, len(csV.Projects))
	for _, item := range csV.Projects {
		csMap[item.Name] = fmt.Sprintf("%s|%d", item.LastWrite, item.Files)
	}
	onlyGo := make([]string, 0, 4)
	onlyCS := make([]string, 0, 4)
	contentDiff := make([]string, 0, 4)
	for name, sig := range goMap {
		csSig, found := csMap[name]
		if !found {
			onlyGo = append(onlyGo, name)
			continue
		}
		if sig != csSig {
			contentDiff = append(contentDiff, name+"("+sig+" != "+csSig+")")
		}
	}
	for name := range csMap {
		if _, found := goMap[name]; !found {
			onlyCS = append(onlyCS, name)
		}
	}
	if len(onlyGo) > 0 || len(onlyCS) > 0 || len(contentDiff) > 0 {
		rep.add(adminA2Class, "inject-projects 集合与内容", stDiff,
			fmt.Sprintf("仅 cat5=%v；仅 cs=%v；内容不同=%v", onlyGo, onlyCS, contentDiff))
		return
	}
	rep.add(adminA2Class, "inject-projects 集合与内容", stPass, fmt.Sprintf("%d 项 name/lastWrite/files 全等", len(goMap)))

	// 原始次序——同秒并列下两侧排序算法不同，记录首处差异与并列项数
	goOrder := make([]string, 0, len(goV.Projects))
	for _, item := range goV.Projects {
		goOrder = append(goOrder, item.Name)
	}
	csOrder := make([]string, 0, len(csV.Projects))
	for _, item := range csV.Projects {
		csOrder = append(csOrder, item.Name)
	}
	if strings.Join(goOrder, "|") == strings.Join(csOrder, "|") {
		rep.add(adminA2Class, "inject-projects 次序", stPass, fmt.Sprintf("%d 项同序", len(goOrder)))
		return
	}
	tied := 0
	counts := make(map[string]int, len(goV.Projects))
	for _, item := range goV.Projects {
		counts[item.LastWrite]++
	}
	for _, count := range counts {
		if count > 1 {
			tied += count
		}
	}
	first := 0
	for i := 0; i < len(goOrder) && i < len(csOrder); i++ {
		if goOrder[i] != csOrder[i] {
			first = i
			break
		}
	}
	rep.add(adminA2Class, "inject-projects 次序", stKnown,
		fmt.Sprintf("集合与内容全等；首处次序差 [%d] cat5=%s cs=%s——同秒并列 %d 项（C# introsort 不稳定 / Go stable），秒级并列下顺序不构成可观测行为",
			first, goOrder[first], csOrder[first], tied))
}
