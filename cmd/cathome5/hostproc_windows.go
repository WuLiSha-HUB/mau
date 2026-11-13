//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// 本文件是「同机宿主实例探测」的 Windows 执行面——对齐 CH4 1.8.266 重启链双硬化：
//
//	① 宿主重启闸门前置（mau:CatHome4/Program.Restart.cs DetectForeignHostInTarget）：
//	   目标运行区已被另一宿主实例占用 → 撤销重启态、宿主继续服务（改名必失败，且本宿主会被拉下线）；
//	② 部署 relaunch 自启前检（mau:SetUp/Program.Relaunch.cs FindHostsInDirectory）：
//	   运行区已有实例在跑 → 跳过自启，不新增第二个实例（服务假象 + dll 持锁）。
//
// 判据：进程映像名 = cathome5.exe 且可执行路径落在目标目录下（大小写不敏感、按分隔符边界）。
// 单进程信息不可读时跳过该进程（权限差异不构成占用证据）；枚举失败按「无占用」继续并出声（不静默）。

// hostImageName 宿主进程映像名（tasklist IMAGENAME 过滤值）。
const hostImageName = "cathome5.exe"

// hostProcess 一条宿主进程记录。
type hostProcess struct {
	// PID 进程号
	PID int
	// Exe 可执行文件全路径
	Exe string
}

var (
	// hostScanForeignInTarget 重启闸门前置探测（单测可替换——生产 = Windows API 枚举）。
	hostScanForeignInTarget = detectForeignHostInTargetImpl
	// hostScanFindInDir 部署自启前检枚举（单测可替换）。
	hostScanFindInDir = findHostsInDirectoryImpl
	// procQueryFullProcessImageNameW kernel32.dll——取进程映像全路径。
	procQueryFullProcessImageNameW = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryFullProcessImageNameW")
)

// detectForeignHostInTargetImpl 探测目标运行区是否已被另一宿主实例占用——返回占用者描述（空 = 无占用）。
func detectForeignHostInTargetImpl(target string, logf func(level int, message, category string)) string {
	hosts, err := listHostProcesses(logf)
	if err != nil {
		if logf != nil {
			logf(2, "重复宿主实例检测失败（进程枚举异常，按无占用继续）: "+err.Error(), "RESTART")
		}
		return ""
	}
	for _, host := range hosts {
		if hostPathUnder(host.Exe, target) {
			return "pid " + strconv.Itoa(host.PID) + "（" + host.Exe + "）"
		}
	}
	return ""
}

// findHostsInDirectoryImpl 枚举目标运行区内的宿主实例（只读——不结束进程；pid 归属由调用方判定）。
func findHostsInDirectoryImpl(dir string) ([]int, error) {
	hosts, err := listHostProcesses(nil)
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0, 2)
	for _, host := range hosts {
		if hostPathUnder(host.Exe, dir) {
			pids = append(pids, host.PID)
		}
	}
	return pids, nil
}

// listHostProcesses 枚举本机宿主进程（映像名过滤 + 全路径查询；排除当前进程——继电器自身不是占用者）。
func listHostProcesses(logf func(level int, message, category string)) ([]hostProcess, error) {
	pids, err := listHostPIDs()
	if err != nil {
		return nil, err
	}
	self := os.Getpid()
	hosts := make([]hostProcess, 0, len(pids))
	for _, pid := range pids {
		if pid == self {
			continue
		}
		exe, queryErr := queryProcessImagePath(uint32(pid))
		if queryErr != nil {
			// 单进程信息不可读——跳过（权限差异不构成占用证据）；出声不静默
			if logf != nil {
				logf(2, "重复宿主实例检测：进程 "+strconv.Itoa(pid)+" 信息不可读（跳过）: "+queryErr.Error(), "RESTART")
			}
			continue
		}
		if !strings.EqualFold(filepath.Base(exe), hostImageName) {
			continue
		}
		hosts = append(hosts, hostProcess{PID: pid, Exe: exe})
	}
	return hosts, nil
}

// listHostPIDs 按映像名枚举本机进程 pid（tasklist CSV；无匹配时回本地化 INFO 行——非引号开头，跳过）。
func listHostPIDs() ([]int, error) {
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq "+hostImageName, "/FO", "CSV", "/NH").Output()
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0, 2)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "\"") {
			continue
		}
		fields := strings.Split(line, "\",\"")
		if len(fields) < 2 {
			continue
		}
		pid, convErr := strconv.Atoi(strings.Trim(fields[1], "\""))
		if convErr != nil {
			continue
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

// queryProcessImagePath 取进程可执行文件全路径（PROCESS_QUERY_LIMITED_INFORMATION + QueryFullProcessImageNameW）。
func queryProcessImagePath(pid uint32) (string, error) {
	const processQueryLimitedInformation = 0x1000
	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, pid)
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(handle)
	buf := make([]uint16, 4096)
	size := uint32(len(buf))
	ret, _, callErr := procQueryFullProcessImageNameW.Call(
		uintptr(handle),
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if ret == 0 {
		return "", callErr
	}
	return syscall.UTF16ToString(buf[:size]), nil
}

// hostPathUnder 判断可执行路径是否落在目录下（大小写不敏感；目录取绝对路径 + 分隔符边界）。
func hostPathUnder(exe, dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	prefix := strings.TrimRight(abs, `\/`)
	if prefix == "" {
		return false
	}
	prefix += string(os.PathSeparator)
	return strings.HasPrefix(strings.ToLower(exe), strings.ToLower(prefix))
}

// joinHostPIDs pid 列表转文本（逗号分隔——报告 / 日志用）。
func joinHostPIDs(pids []int) string {
	parts := make([]string, 0, len(pids))
	for _, pid := range pids {
		parts = append(parts, strconv.Itoa(pid))
	}
	return strings.Join(parts, ",")
}

// ---------- 同目录残留实例清扫（部署 relaunch 段1b）----------

// hostSweepResult 同目录残留宿主实例的清扫结果。
type hostSweepResult struct {
	// PIDs 命中并处置的实例（枚举序）
	PIDs []int
	// Graceful 正常关闭（WM_CLOSE 投递 + 2 s 内退出）数
	Graceful int
	// Killed 强制结束数（含优雅关 2 s 未退的降级）
	Killed int
	// Failed 处置失败数（进程可能仍存活）
	Failed int
}

// 清扫用 Windows 常量与等待窗口（对齐 CH4 KillHostsInDirectory：优雅 2 s / 强杀 3 s / settle 1 s）。
const (
	// wmClose WM_CLOSE
	wmClose = 0x0010
	// gwOwner GetWindow 的 GW_OWNER（主窗口判据之一）
	gwOwner = 4
	// processSynchronize PROCESS_SYNCHRONIZE（等进程退出）
	processSynchronize = 0x00100000
	// processTerminate PROCESS_TERMINATE（强杀）
	processTerminate = 0x0001
	// errorInvalidParameter ERROR_INVALID_PARAMETER（OpenProcess 对不存在 pid 的返回）
	errorInvalidParameter = 87
	// closeMainWindowWaitMS 优雅关等待窗口（2 s）
	closeMainWindowWaitMS = 2000
	// killWaitMS 强杀等待窗口（3 s）
	killWaitMS = 3000
)

var (
	// hostKillInDir 部署 relaunch 残留实例清扫（单测可替换）。
	hostKillInDir = sweepHostsInDirectoryImpl
	// procEnumWindows 枚举顶层窗口（user32DLL / procPostMessageW 复用 tray_windows.go 的声明）。
	procEnumWindows = user32DLL.NewProc("EnumWindows")
	// procGetWindowThreadProcessID 窗口归属进程。
	procGetWindowThreadProcessID = user32DLL.NewProc("GetWindowThreadProcessId")
	// procIsWindowVisible 窗口可见性。
	procIsWindowVisible = user32DLL.NewProc("IsWindowVisible")
	// procGetWindow 取 owner 窗口（GW_OWNER）。
	procGetWindow = user32DLL.NewProc("GetWindow")
	// procGetWindowTextLengthW 窗口标题长度。
	procGetWindowTextLengthW = user32DLL.NewProc("GetWindowTextLengthW")
	// enumMainWindowState EnumWindows 回调的临时状态（包级——回调只创建一次）。
	enumMainWindowState struct {
		// pid 目标进程
		pid int
		// found 命中的主窗口句柄（0 = 未命中）
		found uintptr
	}
	// enumMainWindowCallback EnumWindows 回调——命中目标进程主窗口即记录并停止枚举。
	enumMainWindowCallback = syscall.NewCallback(func(hwnd uintptr, lparam uintptr) uintptr {
		var wndPID uint32
		procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&wndPID)))
		if int(wndPID) != enumMainWindowState.pid {
			return 1
		}
		if visible, _, _ := procIsWindowVisible.Call(hwnd); visible == 0 {
			return 1
		}
		if owner, _, _ := procGetWindow.Call(hwnd, gwOwner); owner != 0 {
			return 1
		}
		if titleLen, _, _ := procGetWindowTextLengthW.Call(hwnd); titleLen == 0 {
			return 1
		}
		enumMainWindowState.found = hwnd
		return 0
	})
)

// sweepHostsInDirectoryImpl 结束目标运行区内的宿主实例——对齐 CH4 SetUp/Program.Relaunch.cs KillHostsInDirectory。
// 判例 2026-11-07：同一运行区并存多个宿主（历次部署失败自启 + 手动启动），发起方 pid 自退后其余实例仍持 dll
// 与目录 cwd → 原子切换改名必失败；它们都是待替换运行区里的旧实例，属部署链自身的清理面。
// 先请求正常关闭（主窗口 WM_CLOSE，≤2 s），仍在则强制结束（≤3 s）；有强杀则 settle 1 s。逐条出声。
func sweepHostsInDirectoryImpl(dir string) (hostSweepResult, error) {
	var res hostSweepResult
	pids, err := findHostsInDirectoryImpl(dir)
	if err != nil {
		return res, err
	}
	for _, pid := range pids {
		res.PIDs = append(res.PIDs, pid)
		if closeMainWindowGracefully(pid) && waitProcessGoneMS(pid, closeMainWindowWaitMS) {
			res.Graceful++
			fmt.Println("[部署] 残留宿主已正常退出：pid " + strconv.Itoa(pid))
			continue
		}
		if err := terminateProcessByPID(pid); err != nil {
			res.Failed++
			fmt.Println("[部署] 警告：结束残留宿主失败（pid " + strconv.Itoa(pid) + "）：" + err.Error())
			continue
		}
		waitProcessGoneMS(pid, killWaitMS)
		res.Killed++
		fmt.Println("[部署] 残留宿主已强制结束：pid " + strconv.Itoa(pid))
	}
	if res.Killed > 0 {
		time.Sleep(1 * time.Second)
	}
	return res, nil
}

// closeMainWindowGracefully 向进程主窗口投递 WM_CLOSE（.NET CloseMainWindow 近似：可见 + 无 owner + 有标题）。
// 返回 false = 无主窗口（调用方直接走强杀，不等优雅窗口——同 C# closed=false 分支）。
func closeMainWindowGracefully(pid int) bool {
	hwnd := mainWindowOfPID(pid)
	if hwnd == 0 {
		return false
	}
	posted, _, _ := procPostMessageW.Call(hwnd, wmClose, 0, 0)
	return posted != 0
}

// mainWindowOfPID 枚举顶层窗口找 pid 的主窗口（0 = 无）。
func mainWindowOfPID(pid int) uintptr {
	enumMainWindowState.pid = pid
	enumMainWindowState.found = 0
	procEnumWindows.Call(enumMainWindowCallback, 0)
	return enumMainWindowState.found
}

// waitProcessGoneMS 等进程退出（≤ms）；true = 已退出 / 不存在。
func waitProcessGoneMS(pid int, ms uint32) bool {
	handle, err := syscall.OpenProcess(processSynchronize, false, uint32(pid))
	if err != nil {
		if errno, ok := err.(syscall.Errno); ok && uint32(errno) == errorInvalidParameter {
			return true
		}
		return false
	}
	defer syscall.CloseHandle(handle)
	event, waitErr := syscall.WaitForSingleObject(handle, ms)
	return waitErr == nil && event == 0
}

// terminateProcessByPID 强制结束进程（已不存在视为成功）。
func terminateProcessByPID(pid int) error {
	handle, err := syscall.OpenProcess(processTerminate, false, uint32(pid))
	if err != nil {
		if errno, ok := err.(syscall.Errno); ok && uint32(errno) == errorInvalidParameter {
			return nil
		}
		return err
	}
	defer syscall.CloseHandle(handle)
	return syscall.TerminateProcess(handle, 1)
}
