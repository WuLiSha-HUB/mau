//go:build windows

// Command trayprobe 托盘探针——按窗口类名查找 CatHome5 托盘窗口并投递菜单命令。
//
// 用途（外壳验收）：-action find 探测托盘窗口存在性（0 退出码）/ exit 模拟菜单「退出」/
// toggle 模拟菜单「开机自启」——WndProc 的 WM_COMMAND 与 TrackPopupMenu 返回值是同一执行路径，
// 因此外部投递与手工点击等价（消费点实锤）。
package main

import (
	"flag"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var (
	// user32DLL 窗口 API 所在 DLL
	user32DLL = syscall.NewLazyDLL("user32.dll")

	// procFindWindowW FindWindowW
	procFindWindowW = user32DLL.NewProc("FindWindowW")
	// procPostMessageW PostMessageW
	procPostMessageW = user32DLL.NewProc("PostMessageW")
)

const (
	// wmCommand 命令消息（与托盘 WndProc 的 WM_COMMAND 分支同源）
	wmCommand = 0x0111
	// trayMenuAutoStart / trayMenuExit 菜单项 id（与托盘实现共用一套）
	trayMenuAutoStart = 1
	trayMenuExit      = 4
)

// main 探针入口。
func main() {
	className := flag.String("class", "CatHome5TrayWnd", "托盘窗口类名")
	action := flag.String("action", "find", "动作：find / exit / toggle")
	flag.Parse()

	classPtr, err := syscall.UTF16PtrFromString(*className)
	if err != nil {
		fmt.Fprintln(os.Stderr, "类名转换失败:", err)
		os.Exit(1)
	}
	hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(classPtr)), 0)
	if hwnd == 0 {
		fmt.Println("没找到托盘窗口：" + *className)
		os.Exit(2)
	}
	fmt.Printf("托盘窗口：0x%X\n", hwnd)
	var id uint32
	switch *action {
	case "find":
		os.Exit(0)
	case "exit":
		id = trayMenuExit
	case "toggle":
		id = trayMenuAutoStart
	default:
		fmt.Fprintln(os.Stderr, "未知动作:", *action)
		os.Exit(1)
	}
	ret, _, _ := procPostMessageW.Call(hwnd, wmCommand, uintptr(id), 0)
	if ret == 0 {
		fmt.Fprintln(os.Stderr, "PostMessage 失败")
		os.Exit(1)
	}
	fmt.Printf("已投递 WM_COMMAND id=%d\n", id)
}
