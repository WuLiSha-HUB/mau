package config

import "os"

// PortBand 是机器级端口空间的分区（开发区向下 / 部署区向上），两个区的端口
// 集合互不相交——开发区实例与部署区实例可同时运行。
//
// 对齐 C# 版 CatHome4.Contracts.PortBand：段位是既定设计值，不接受外部拼装。
type PortBand struct {
	// Area 区标识（dev / deploy）
	Area string
	// MainPort 主端口——HTTP 外观层（app.cfg:http.port 显式值优先于本值）
	MainPort int
	// FamilyFrom 端口族起点——majordomo 独立端口与每猫端口由此起扫
	FamilyFrom int
	// FamilyTo 端口族终点（含）——全占即分配失败，不越界到对方区段
	FamilyTo int
	// FrontendTestPort 前端测试服务端口（html/tests/server.js）
	FrontendTestPort int
}

// ResolvePortBand 解析端口段。
//
//	开发区：主端口 8079、端口族 8078 向下至 8070、前端测试 8069
//	部署区：主端口 8080、端口族 8081 向上至 8180、前端测试 8099
func ResolvePortBand(devArea bool) PortBand {
	if devArea {
		return PortBand{Area: "dev", MainPort: 8079, FamilyFrom: 8078, FamilyTo: 8070, FrontendTestPort: 8069}
	}
	return PortBand{Area: "deploy", MainPort: 8080, FamilyFrom: 8081, FamilyTo: 8180, FrontendTestPort: 8099}
}

// FamilyStep 返回端口族扫描步进：起点小于终点向上 +1，否则向下 -1。
func (b PortBand) FamilyStep() int {
	if b.FamilyFrom <= b.FamilyTo {
		return 1
	}
	return -1
}

// ResolveArea 判定是否开发区实例。
//
// 判据与 C# 同源（exe 是否位于仓库树内），并支持显式覆盖：
//  1. CAT5_AREA 环境变量（dev / deploy）——对拍与部署逃逸口
//  2. 可执行文件目录向上能找到 Mau.sln → 开发区
//
// 施工仓（WorkSpace:CatHome5）不在 Mau 仓库树内，默认落部署段；对拍时用
// CAT5_AREA=dev 显式切段。
func ResolveArea() bool {
	switch os.Getenv("CAT5_AREA") {
	case "dev":
		return true
	case "deploy":
		return false
	}
	return FindRepoRoot(ExeDir()) != ""
}
