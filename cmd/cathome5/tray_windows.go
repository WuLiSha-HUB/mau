//go:build windows

package main

import (
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

// 本文件是托盘常驻面（对齐 mau:CatHome4/Program.Tray.cs 的托盘分支）：
// 隐藏窗口 + Shell_NotifyIcon + 右键菜单（开机自启 / 打开管理面板 / 打开数据目录 / 退出）。
//
// 形态（边界决策 6——Go 托盘允许降级）：
//   - 无 WinForms 总控窗——左键单击 = 默认浏览器打开管理面板（「托盘 + 浏览器面板」形态）；
//   - 只在交互模式启用；--run 等非交互模式不起托盘；
//   - 托盘线程不触碰业务状态：退出 / 自启切换只经 channel 交回主线程；菜单勾选态读配置（Store 读锁安全）；
//   - 任一步失败返回错误由调用方记降级（托盘不可用不拖垮宿主）。
//
// 线程契约：窗口创建 + 消息循环固定在同一个 LockOSThread goroutine（Win32 窗口线程亲和）。
// 进程级单托盘：WndProc 是全局回调，控制器经 trayActive 间接寻址。

const (
	// wmNull 空消息（菜单关闭后的惯例补发）
	wmNull = 0x0000
	// wmDestroy 窗口销毁
	wmDestroy = 0x0002
	// wmCommand 命令消息（外部探针 / 菜单双路径共用）
	wmCommand = 0x0111
	// wmUser WM_USER 基准
	wmUser = 0x0400
	// wmTrayIcon 托盘回调消息（Shell_NotifyIcon uCallbackMessage）
	wmTrayIcon = wmUser + 1
	// wmLButtonUp 左键抬起
	wmLButtonUp = 0x0202
	// wmRButtonUp 右键抬起
	wmRButtonUp = 0x0205

	// nimAdd / nimDelete Shell_NotifyIcon 动作
	nimAdd    = 0x00000000
	nimDelete = 0x00000002
	// nifMessage / nifIcon / nifTip NOTIFYICONDATA 有效字段标志
	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	// mfString / mfChecked / mfSeparator 菜单项标志
	mfString    = 0x00000000
	mfChecked   = 0x00000008
	mfSeparator = 0x00000800
	// tpmRightButton / tpmReturnCmd TrackPopupMenu 标志
	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100

	// trayMenuAutoStart / trayMenuPanel / trayMenuDataDir / trayMenuExit 菜单项 id
	// （TrackPopupMenu 返回值与 WM_COMMAND 共用一套——外部探针可 PostMessage 模拟点击）
	trayMenuAutoStart = 1
	trayMenuPanel     = 2
	trayMenuDataDir   = 3
	trayMenuExit      = 4

	// trayClassName 托盘窗口类名（探针按类名查找）
	trayClassName = "CatHome5TrayWnd"
)

var (
	// user32DLL / shell32DLL / kernel32DLL 托盘依赖 DLL
	user32DLL   = syscall.NewLazyDLL("user32.dll")
	shell32DLL  = syscall.NewLazyDLL("shell32.dll")
	kernel32DLL = syscall.NewLazyDLL("kernel32.dll")

	// procRegisterClassExW RegisterClassExW
	procRegisterClassExW = user32DLL.NewProc("RegisterClassExW")
	// procCreateWindowExW CreateWindowExW
	procCreateWindowExW = user32DLL.NewProc("CreateWindowExW")
	// procDefWindowProcW DefWindowProcW
	procDefWindowProcW = user32DLL.NewProc("DefWindowProcW")
	// procDestroyWindow DestroyWindow
	procDestroyWindow = user32DLL.NewProc("DestroyWindow")
	// procGetMessageW GetMessageW
	procGetMessageW = user32DLL.NewProc("GetMessageW")
	// procTranslateMessage TranslateMessage
	procTranslateMessage = user32DLL.NewProc("TranslateMessage")
	// procDispatchMessageW DispatchMessageW
	procDispatchMessageW = user32DLL.NewProc("DispatchMessageW")
	// procPostQuitMessage PostQuitMessage
	procPostQuitMessage = user32DLL.NewProc("PostQuitMessage")
	// procPostMessageW PostMessageW
	procPostMessageW = user32DLL.NewProc("PostMessageW")
	// procCreatePopupMenu CreatePopupMenu
	procCreatePopupMenu = user32DLL.NewProc("CreatePopupMenu")
	// procAppendMenuW AppendMenuW
	procAppendMenuW = user32DLL.NewProc("AppendMenuW")
	// procTrackPopupMenu TrackPopupMenu
	procTrackPopupMenu = user32DLL.NewProc("TrackPopupMenu")
	// procDestroyMenu DestroyMenu
	procDestroyMenu = user32DLL.NewProc("DestroyMenu")
	// procGetCursorPos GetCursorPos
	procGetCursorPos = user32DLL.NewProc("GetCursorPos")
	// procSetForegroundWindow SetForegroundWindow
	procSetForegroundWindow = user32DLL.NewProc("SetForegroundWindow")
	// procLoadIconW LoadIconW
	procLoadIconW = user32DLL.NewProc("LoadIconW")
	// procShellNotifyIconW Shell_NotifyIconW
	procShellNotifyIconW = shell32DLL.NewProc("Shell_NotifyIconW")
	// procShellExecuteW ShellExecuteW
	procShellExecuteW = shell32DLL.NewProc("ShellExecuteW")
	// procGetModuleHandleW GetModuleHandleW
	procGetModuleHandleW = kernel32DLL.NewProc("GetModuleHandleW")

	// trayWndProcCallback WndProc 全局回调（保持引用防 GC 回收）
	trayWndProcCallback = syscall.NewCallback(trayWndProcFn)
	// trayActive 进程唯一托盘控制器（WndProc 间接寻址）
	trayActive *trayController
)

// wndClassEx 是 WNDCLASSEXW。
type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   syscall.Handle
	Icon       syscall.Handle
	Cursor     syscall.Handle
	Background syscall.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     syscall.Handle
}

// notifyIconData 是 NOTIFYICONDATAW（Vista+ 全量形态）。
type notifyIconData struct {
	CbSize           uint32
	HWnd             syscall.Handle
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            syscall.Handle
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         syscall.GUID
	HBalloonIcon     syscall.Handle
}

// trayPoint 是 POINT。
type trayPoint struct {
	X int32
	Y int32
}

// trayMsg 是 MSG。
type trayMsg struct {
	HWnd    syscall.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      trayPoint
}

// trayController 托盘控制器——主线程构造（字段只读），托盘线程只经 channel 回传动作。
type trayController struct {
	// hwnd 托盘隐藏窗口句柄（托盘线程写，就绪后只读）
	hwnd syscall.Handle
	// exitCh 退出请求（菜单「退出」/ 外部探针 WM_COMMAND → 主线程优雅收尾）
	exitCh chan struct{}
	// toggleCh 自启切换请求（托盘线程 → 主线程执行配置 + 注册表写）
	toggleCh chan struct{}
	// version 版本串（tooltip）
	version string
	// area 区标识（tooltip）
	area string
	// dataRoot 数据目录（「打开数据目录」）
	dataRoot string
	// panelURL 管理面板地址（左键 / 菜单）
	panelURL string
	// autoStartOn 配置勾选态读取（Store 读锁安全）
	autoStartOn func() bool
	// logf 观测出口（失败可见）
	logf func(level int, message, category string)
}

// startTray 启动托盘——返回就绪后的控制器（窗口已建 + 图标已加；失败即错误）。
func startTray(t *trayController) (*trayController, error) {
	if t == nil {
		return nil, fmt.Errorf("托盘控制器为空")
	}
	trayActive = t
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		trayMessageLoop(t, ready)
	}()
	select {
	case err := <-ready:
		if err != nil {
			trayActive = nil
			return nil, err
		}
		return t, nil
	case <-time.After(3 * time.Second):
		trayActive = nil
		return nil, fmt.Errorf("托盘初始化超时（3 s）")
	}
}

// trayMessageLoop 创建隐藏窗口 + 托盘图标 + 消息循环（窗口线程主体）。
func trayMessageLoop(t *trayController, ready chan<- error) {
	hInstance, _, _ := procGetModuleHandleW.Call(0)
	className, err := syscall.UTF16PtrFromString(trayClassName)
	if err != nil {
		ready <- err
		return
	}
	wc := wndClassEx{
		Size:      uint32(unsafe.Sizeof(wndClassEx{})),
		WndProc:   trayWndProcCallback,
		Instance:  syscall.Handle(hInstance),
		ClassName: className,
	}
	if ret, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); ret == 0 {
		ready <- fmt.Errorf("RegisterClassExW 失败: %v", callErr)
		return
	}
	hwnd, _, callErr := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		0, 0,
		0, 0, 0, 0,
		0, 0, hInstance, 0,
	)
	if hwnd == 0 {
		ready <- fmt.Errorf("CreateWindowExW 失败: %v", callErr)
		return
	}
	t.hwnd = syscall.Handle(hwnd)
	icon, _, _ := procLoadIconW.Call(0, 32512) // IDI_APPLICATION 兜底（go build 无图标资源）
	nid := notifyIconData{
		CbSize:           uint32(unsafe.Sizeof(notifyIconData{})),
		HWnd:             t.hwnd,
		UID:              1,
		UFlags:           nifMessage | nifIcon | nifTip,
		UCallbackMessage: wmTrayIcon,
		HIcon:            syscall.Handle(icon),
	}
	tip := "CatHome5 " + t.version
	if t.area != "" {
		tip += "（" + t.area + "）"
	}
	copyTooltip(nid.SzTip[:], tip)
	if ret, _, notifyErr := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid))); ret == 0 {
		procDestroyWindow.Call(hwnd)
		ready <- fmt.Errorf("Shell_NotifyIcon 添加失败: %v", notifyErr)
		return
	}
	ready <- nil
	var msg trayMsg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if ret == 0 || int32(ret) == -1 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

// trayWndProcFn 托盘窗口消息处理——全局唯一（进程内单托盘）。
func trayWndProcFn(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	t := trayActive
	switch msg {
	case wmTrayIcon:
		if t == nil {
			return 0
		}
		switch uint32(lParam) & 0xFFFF {
		case wmLButtonUp:
			t.openPanel()
		case wmRButtonUp:
			t.showMenu(hwnd)
		}
		return 0
	case wmCommand:
		if t == nil {
			return 0
		}
		t.handleCommand(uint32(wParam) & 0xFFFF)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return ret
}

// handleCommand 菜单命令执行——TrackPopupMenu 返回值与外部 WM_COMMAND 单一执行路径。
func (t *trayController) handleCommand(id uint32) {
	switch id {
	case trayMenuAutoStart:
		if t.toggleCh != nil {
			select {
			case t.toggleCh <- struct{}{}:
			default:
			}
		}
	case trayMenuPanel:
		t.openPanel()
	case trayMenuDataDir:
		t.openDataDir()
	case trayMenuExit:
		t.requestExit()
	}
}

// showMenu 右键菜单——每次按当前配置重建（勾选态从配置读）。
func (t *trayController) showMenu(hwnd syscall.Handle) {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	autoFlags := uintptr(mfString)
	if t.autoStartOn != nil && t.autoStartOn() {
		autoFlags |= mfChecked
	}
	appendMenuItem(menu, autoFlags, trayMenuAutoStart, "开机自启")
	procAppendMenuW.Call(menu, mfSeparator, 0, 0)
	appendMenuItem(menu, mfString, trayMenuPanel, "打开管理面板")
	appendMenuItem(menu, mfString, trayMenuDataDir, "打开数据目录")
	procAppendMenuW.Call(menu, mfSeparator, 0, 0)
	appendMenuItem(menu, mfString, trayMenuExit, "退出")
	var pt trayPoint
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(uintptr(hwnd))
	ret, _, _ := procTrackPopupMenu.Call(
		menu,
		tpmReturnCmd|tpmRightButton,
		uintptr(pt.X), uintptr(pt.Y),
		0, uintptr(hwnd), 0,
	)
	// 菜单关闭后补发空消息（Win32 惯例——否则菜单可能残留）
	procPostMessageW.Call(uintptr(hwnd), wmNull, 0, 0)
	if ret != 0 {
		t.handleCommand(uint32(ret))
	}
}

// appendMenuItem 追加一个文本菜单项。
func appendMenuItem(menu, flags, id uintptr, text string) {
	label, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	procAppendMenuW.Call(menu, flags, id, uintptr(unsafe.Pointer(label)))
}

// requestExit 退出请求——通知主线程走优雅收尾；托盘线程退出消息循环。
func (t *trayController) requestExit() {
	if t.logf != nil {
		t.logf(1, "托盘菜单：退出", "TRAY")
	}
	select {
	case t.exitCh <- struct{}{}:
	default:
	}
	procPostQuitMessage.Call(0)
}

// openPanel 打开管理面板（默认浏览器）。
func (t *trayController) openPanel() {
	if t.panelURL == "" {
		return
	}
	openShellTarget(t.panelURL, "", t.logf)
}

// openDataDir 打开数据目录（资源管理器）。
func (t *trayController) openDataDir() {
	if t.dataRoot == "" {
		return
	}
	openShellTarget("explorer.exe", t.dataRoot, t.logf)
}

// openShellTarget 交 ShellExecuteW 打开目标（verb=open；返回值 ≤32 视为失败）。
func openShellTarget(target, params string, logf func(level int, message, category string)) {
	verb, _ := syscall.UTF16PtrFromString("open")
	file, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return
	}
	var paramsPtr *uint16
	if params != "" {
		paramsPtr, _ = syscall.UTF16PtrFromString(params)
	}
	ret, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(paramsPtr)),
		0,
		1, // SW_SHOWNORMAL
	)
	if ret <= 32 && logf != nil {
		logf(2, "打开失败（ShellExecute="+fmt.Sprint(ret)+"）："+target, "TRAY")
	}
}

// copyTooltip 把 tooltip 写入 szTip（UTF-16，超长截断并保证 NUL 结尾）。
func copyTooltip(dst []uint16, text string) {
	encoded, err := syscall.UTF16FromString(text)
	if err != nil || len(dst) == 0 {
		return
	}
	n := len(encoded)
	if n > len(dst) {
		n = len(dst)
	}
	copy(dst[:n], encoded[:n])
	dst[len(dst)-1] = 0
}
