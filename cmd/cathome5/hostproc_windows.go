//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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
