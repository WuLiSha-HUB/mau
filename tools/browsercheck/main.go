package main

// browsercheck —— BrowserCat 服务层实机验证（起内核 → 打开页面 → 读 / 取值 / 截图 / 页签 → 关闭）。
//
// 用法：browsercheck -kernel <msedge.exe 全路径> -data-root <数据根> [-url <测试页>]
// 边界：只写数据根下 browser/<catId>/；验证后调 Shutdown 清理实例。

import (
	"flag"
	"fmt"
	"os"
	"time"

	"cathome5/mau/bricks"
)

// main 是验证入口。
func main() {
	kernel := flag.String("kernel", "", "浏览器内核路径（msedge.exe / chrome.exe）")
	dataRoot := flag.String("data-root", "", "数据根（其下 browser/<catId>/）")
	targetURL := flag.String("url", "https://example.com", "测试页")
	flag.Parse()
	if *kernel == "" || *dataRoot == "" {
		fmt.Println("usage: browsercheck -kernel <path> -data-root <dir>")
		os.Exit(2)
	}
	logf := func(level int, message string) {
		fmt.Printf("  [log%d] %s\n", level, message)
	}
	svc := bricks.NewBrowserService(*dataRoot, func() string { return *kernel }, logf)
	catID := "browsercheck"
	started := time.Now()
	fmt.Println("== Open ==")
	fmt.Println(svc.Open(catID, *targetURL))
	fmt.Println("== Read text ==")
	fmt.Println(svc.Read(catID, "text"))
	fmt.Println("== Eval ==")
	fmt.Println(svc.Eval(catID, "document.title"))
	fmt.Println("== Shot ==")
	fmt.Println(svc.Shot(catID, false))
	fmt.Println("== Tabs list ==")
	fmt.Println(svc.Tabs(catID, "list", "", ""))
	fmt.Println("== Close ==")
	svc.Close(catID)
	fmt.Println("== Shutdown ==")
	svc.Shutdown()
	fmt.Printf("elapsed=%dms\n", time.Since(started).Milliseconds())
}
