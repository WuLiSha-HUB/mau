package main

// checks_admin —— 管理端 A1 段（段 6 外观池 / 段 7b 用户态 / 段 8 打开数据目录 / 段 9 图片 / 段 10 音频 / 宿主重启）。
//
// 覆盖口径：读面逐条 JSON 对比 + 写面「错误面」逐条对比（非法输入零磁盘写入）。
// 两侧共用同一数据根——写面成功路径不出现在本器（同文件互写会漂移）；
// 两条副作用端点（打开数据目录 / 宿主重启）两侧都不调用：会弹资源管理器 / 会重启 C# 实例。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// adminCheckClass 是对拍报告里的分类名。
const adminCheckClass = "admin-A1"

// adminFixtureImage 是取图对拍夹具（内容不参与校验——只借文件名与扩展名）。
var adminFixtureImage = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x50, 0x33, 0x41}

// adminFixtureName 是夹具文件名（缓存目录裸名分支）。
const adminFixtureName = "p3a-ab-fixture.png"

// fetchBothPOST 抓同一 POST 的两侧响应并落盘原始证据。
func fetchBothPOST(outDir, name string, goSide, csSide *sideState, path, body, contentType string, timeout time.Duration) (httpResult, httpResult) {
	goRes := doPost(goSide.base, path, body, contentType, timeout)
	csRes := doPost(csSide.base, path, body, contentType, timeout)
	saveRaw(outDir, "cat5", name, goRes)
	saveRaw(outDir, "cs", name, csRes)
	return goRes, csRes
}

// checkAdminGET 读面一条——200/200 + JSON 全等。
func checkAdminGET(rep *report, opts options, goSide, csSide *sideState, item, path string) {
	goRes, csRes := fetchBoth(opts.outDir, item+".json", goSide, csSide, path, 15*time.Second)
	if !require200(rep, adminCheckClass, "GET "+path, goRes, csRes) {
		return
	}
	goV := parseSideJSON(rep, adminCheckClass, "cat5", goRes)
	csV := parseSideJSON(rep, adminCheckClass, "cs", csRes)
	if goV == nil || csV == nil {
		return
	}
	if jsonEqual(goV, csV) {
		rep.add(adminCheckClass, item+"（JSON 全等）", stPass, clip(dumpJ(goV)))
		return
	}
	rep.add(adminCheckClass, item+"（JSON 全等）", stDiff, firstDiff(goV, csV, ""))
}

// checkAdminErrorPOST 写面错误面一条——200/200 + JSON 全等（零磁盘写入）。
func checkAdminErrorPOST(rep *report, opts options, goSide, csSide *sideState, item, path, body, contentType string) {
	goRes, csRes := fetchBothPOST(opts.outDir, item+".json", goSide, csSide, path, body, contentType, 15*time.Second)
	if !require200(rep, adminCheckClass, "POST "+path+"（非法输入）", goRes, csRes) {
		return
	}
	goV := parseSideJSON(rep, adminCheckClass, "cat5", goRes)
	csV := parseSideJSON(rep, adminCheckClass, "cs", csRes)
	if goV == nil || csV == nil {
		return
	}
	if jsonEqual(goV, csV) {
		rep.add(adminCheckClass, item+"（错误面全等）", stPass, clip(dumpJ(goV)))
		return
	}
	rep.add(adminCheckClass, item+"（错误面全等）", stDiff, firstDiff(goV, csV, ""))
}

// checkAdmin404 只读面未命中一条——两侧同状态码（404/404）。
func checkAdmin404(rep *report, opts options, goSide, csSide *sideState, item, path string) {
	goRes, csRes := fetchBoth(opts.outDir, item+".bin", goSide, csSide, path, 15*time.Second)
	if !goRes.ok() || !csRes.ok() {
		rep.add(adminCheckClass, "GET "+path, stFail, fmt.Sprintf("抓取失败 cat5=%s cs=%s", describeStatus(goRes), describeStatus(csRes)))
		return
	}
	if goRes.status == csRes.status {
		rep.add(adminCheckClass, item, stPass, statusPair(goRes, csRes))
		return
	}
	rep.add(adminCheckClass, item, stDiff, statusPair(goRes, csRes))
}

// checkAdminImage 取图夹具一条——状态码 + Content-Type + Cache-Control 语义 + 字节全等。
func checkAdminImage(rep *report, opts options, goSide, csSide *sideState, item, path string, wantNoCache bool) {
	goRes, csRes := fetchBoth(opts.outDir, item+".bin", goSide, csSide, path, 15*time.Second)
	if !require200(rep, adminCheckClass, "GET "+path, goRes, csRes) {
		return
	}
	goType := goRes.contentTypeBase()
	csType := csRes.contentTypeBase()
	if goType == csType {
		rep.add(adminCheckClass, item+" Content-Type", stPass, goType)
	} else {
		rep.add(adminCheckClass, item+" Content-Type", stDiff, goType+" / "+csType)
	}
	goCache, csCache := goRes.headerValue("Cache-Control"), csRes.headerValue("Cache-Control")
	goNoCache := strings.Contains(goCache, "no-cache")
	csNoCache := strings.Contains(csCache, "no-cache")
	if goNoCache == csNoCache && goNoCache == wantNoCache {
		rep.add(adminCheckClass, item+" 缓存语义", stPass, goCache)
	} else {
		rep.add(adminCheckClass, item+" 缓存语义", stDiff, fmt.Sprintf("cat5=%q cs=%q（预期 noCache=%v）", goCache, csCache, wantNoCache))
	}
	if len(goRes.body) == len(csRes.body) && string(goRes.body) == string(csRes.body) {
		rep.add(adminCheckClass, item+" 字节", stPass, fmt.Sprintf("%d 字节", len(goRes.body)))
	} else {
		rep.add(adminCheckClass, item+" 字节", stDiff, fmt.Sprintf("cat5=%d 字节 cs=%d 字节", len(goRes.body), len(csRes.body)))
	}
}

// checkAdmin 跑管理端 A1 段对拍。
func checkAdmin(rep *report, opts options, goSide, csSide *sideState) {
	// [读面] 段 6 外观池 / 段 7b 用户态（同数据根 → 同值同结构）
	checkAdminGET(rep, opts, goSide, csSide, "appearance-apis", "/api/v1/appearance-apis")
	checkAdminGET(rep, opts, goSide, csSide, "user-state", "/api/v1/user-state")

	// [错误面] 写面非法输入——路由存在性 + 校验文案（零磁盘写入）
	checkAdminErrorPOST(rep, opts, goSide, csSide, "appearance-post-empty", "/api/v1/appearance-apis", `{}`, "application/json")
	checkAdminErrorPOST(rep, opts, goSide, csSide, "appearance-edit-badid", "/api/v1/appearance-apis/edit", `{"appearanceId":"bad"}`, "application/json")
	checkAdminErrorPOST(rep, opts, goSide, csSide, "appearance-delete-badid", "/api/v1/appearance-apis/delete", `{"appearanceId":"bad"}`, "application/json")
	checkAdminErrorPOST(rep, opts, goSide, csSide, "appearance-default-badid", "/api/v1/appearance-apis/default", `{"appearanceId":"bad"}`, "application/json")
	checkAdminErrorPOST(rep, opts, goSide, csSide, "userstate-unknown", "/api/v1/user-state", `{"current":"路人"}`, "application/json")
	checkAdminErrorPOST(rep, opts, goSide, csSide, "chat-images-badtype", "/api/v1/chat-images", "x", "text/plain")
	// mode 校验在校验链首步——非法档位不会置停机态（C# 侧同样先校验后登记）
	checkAdminErrorPOST(rep, opts, goSide, csSide, "host-restart-badmode", "/api/v1/host-restart", `{"mode":"bogus"}`, "application/json")

	// [读面] 段 9 取图——夹具图（写共用数据根，测后清理）
	imageDir := filepath.Join(opts.dataRoot, "chat-images")
	fixturePath := filepath.Join(imageDir, adminFixtureName)
	seeded := false
	if err := os.MkdirAll(imageDir, 0o755); err == nil {
		if err := os.WriteFile(fixturePath, adminFixtureImage, 0o644); err == nil {
			seeded = true
		}
	}
	if seeded {
		checkAdminImage(rep, opts, goSide, csSide, "cache-image 裸名", "/api/v1/cache-image/"+adminFixtureName, false)
		// 受控根寻址分支——夹具落在某个受控根内（两侧同 workspace.json → 同根表）
		if address, ok := adminRootAddress(opts.dataRoot, fixturePath); ok {
			checkAdminImage(rep, opts, goSide, csSide, "cache-image 根寻址", address, true)
		} else {
			rep.add(adminCheckClass, "cache-image 根寻址", stKnown, "workspace.json 未把夹具所在目录纳入任何受控根")
		}
		_ = os.Remove(fixturePath)
		_ = os.Remove(imageDir)
	} else {
		rep.add(adminCheckClass, "cache-image 夹具", stFail, "夹具落盘失败："+fixturePath)
	}

	// [读面] 未命中面——取图 / 音频
	checkAdmin404(rep, opts, goSide, csSide, "cache-image 未命中", "/api/v1/cache-image/p3a-ab-none.png")
	checkAdmin404(rep, opts, goSide, csSide, "audio 未命中", "/api/v1/audio/p3a-ab-none.wav")

	// [悬置] 两条副作用端点不在本器内调用
	rep.add(adminCheckClass, "open-data-dir / host-restart 调用面", stKnown,
		"两侧都不调用：会弹资源管理器 / 会重启 C# 实例（副作用不可回收）——由实机手工轮覆盖")
}

// adminRootAddress 把夹具绝对路径换算成 `<rootId>:<rel>` 寻址（取首个命中的受控根）。
func adminRootAddress(dataRoot, absPath string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(dataRoot, "config", "workspace.json"))
	if err != nil {
		return "", false
	}
	var file struct {
		Roots []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"roots"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return "", false
	}
	normalized := strings.ReplaceAll(absPath, `\`, "/")
	for _, root := range file.Roots {
		base := strings.TrimRight(strings.ReplaceAll(root.Path, `\`, "/"), "/")
		if root.ID == "" || base == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(normalized), strings.ToLower(base)+"/") {
			return "/api/v1/cache-image/" + root.ID + ":" + normalized[len(base)+1:], true
		}
	}
	return "", false
}
