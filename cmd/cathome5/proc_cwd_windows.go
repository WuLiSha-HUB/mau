//go:build windows

package main

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// 本文件是「进程工作目录」取证面——部署链两处消费：
//
//	① 清扫面（relaunch 段1b 第二面）：py / ts / browser 服务进程以运行区目录为 cwd 时持
//	   目录级句柄，原子切换 rename target→_old 必失败（判例 2026-11-15「rename 自锁」同族：
//	   宿主 cwd 已改数据根，但旧宿主拉起的子进程仍继承运行区 cwd，生命周期长于宿主）；
//	② 归因面（rename 失败）：把 cwd 落在运行区内的进程写进报告 step detail（pid + 映像名 + cwd），
//	   失败报告能直接回答「谁锁着它」——不靠重试盲试。
//
// 判定链：tasklist 枚举 pid/映像名 → OpenProcess(QUERY_INFORMATION|VM_READ) →
// NtQueryInformationProcess(ProcessBasicInformation) 取 PEB → ProcessParameters →
// RTL_USER_PROCESS_PARAMETERS.CurrentDirectory（UNICODE_STRING；WOW64 走 32 位 PEB）。
// 与 CH4 C# 判例同源（mau:raw/mau-RushieraChange/SetUp/Program.Relaunch.cs QueryCwdHolders）。
//
// 读不到（权限 / 进程已退出 / 结构不符）跳过该进程——诊断只报读得到的，不猜；枚举失败出声不静默。

// procCWDInfo 一条进程工作目录记录。
type procCWDInfo struct {
	// PID 进程号
	PID int
	// Name 映像名（如 python.exe）
	Name string
	// CWD 工作目录（PEB CurrentDirectory 原样）
	CWD string
}

// cwdServiceImages 服务进程清扫面——部署链自己拉起的 py / ts / browser 执行体映像名（大小写不敏感）。
//
// 只清这些映像，且必须同时满足「cwd 落在运行区」——两条一起构成「不碰用户进程」判据：
// 用户自己的浏览器 / 解释器工作目录不在运行区；宿主（cathome5.exe）与 cs 桥另有 strayImageNames 面。
var cwdServiceImages = []string{
	"python.exe",   // py 服务（kokoro 等常驻）/ py.run
	"pythonw.exe",  // 无控制台 python 宿主
	"node.exe",     // ts 服务 / node 常驻服务
	"msedge.exe",   // browser-* 内核（Edge 系）
	"chrome.exe",   // browser-* 内核（Chromium 系）
	"chromium.exe", // browser-* 内核（自持 Chromium 构建）
}

const (
	// processQueryInformation PROCESS_QUERY_INFORMATION（读 PEB 所需）
	processQueryInformation = 0x0400
	// processVMRead PROCESS_VM_READ（跨进程读内存）
	processVMRead = 0x0010
	// processWow64Information ProcessWow64Information（NtQueryInformationProcess 26 号信息类——取 32 位 PEB）
	processWow64Information = 26
)

var (
	// procNtQueryInformationProcess ntdll!NtQueryInformationProcess——取 PEB 地址
	procNtQueryInformationProcess = syscall.NewLazyDLL("ntdll.dll").NewProc("NtQueryInformationProcess")
	// procReadProcessMemory kernel32!ReadProcessMemory——读目标进程内存
	procReadProcessMemory = syscall.NewLazyDLL("kernel32.dll").NewProc("ReadProcessMemory")
	// procCWDList 全进程工作目录枚举（单测可替换——替换后不依赖本机进程表）
	procCWDList = listProcessCWDsImpl
)

// processBasicInformation PROCESS_BASIC_INFORMATION（NtQueryInformationProcess 0 号信息类）。
//
// 字段顺序即 ABI：x64 下 PebBaseAddress 因 8 字节对齐落在偏移 8，x86 落在偏移 4（Go 编译器自动对齐）。
type processBasicInformation struct {
	// ExitStatus 退出状态（NTSTATUS）
	ExitStatus uint32
	// PebBaseAddress 进程环境块地址
	PebBaseAddress uintptr
	// AffinityMask 亲和掩码
	AffinityMask uintptr
	// BasePriority 基础优先级
	BasePriority int32
	// UniqueProcessID 进程 id
	UniqueProcessID uintptr
	// InheritedFromUniqueProcessID 父进程 id
	InheritedFromUniqueProcessID uintptr
}

// listProcessCWDsImpl 枚举本机全部进程的工作目录——读不到的进程跳过（只报读得到的）。
func listProcessCWDsImpl() ([]procCWDInfo, error) {
	out, err := exec.Command("tasklist", "/FO", "CSV", "/NH").Output()
	if err != nil {
		return nil, err
	}
	self := os.Getpid()
	list := make([]procCWDInfo, 0, 128)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "\"") {
			continue
		}
		fields := strings.Split(line, "\",\"")
		if len(fields) < 2 {
			continue
		}
		name := strings.Trim(fields[0], "\"")
		pid, convErr := strconv.Atoi(strings.Trim(fields[1], "\""))
		if convErr != nil || pid <= 0 || pid == self {
			continue
		}
		cwd := readProcessCWD(uint32(pid))
		if cwd == "" {
			continue
		}
		list = append(list, procCWDInfo{PID: pid, Name: name, CWD: cwd})
	}
	return list, nil
}

// readProcessCWD 读单个进程工作目录；读不到返回空串（权限 / 已退出 / 结构不符）。
func readProcessCWD(pid uint32) string {
	handle, err := syscall.OpenProcess(processQueryInformation|processVMRead, false, pid)
	if err != nil {
		return ""
	}
	defer syscall.CloseHandle(handle)
	var basic processBasicInformation
	var written uint32
	status, _, _ := procNtQueryInformationProcess.Call(
		uintptr(handle), 0,
		uintptr(unsafe.Pointer(&basic)),
		unsafe.Sizeof(basic),
		uintptr(unsafe.Pointer(&written)),
	)
	if status != 0 || basic.PebBaseAddress == 0 {
		return ""
	}
	peb := basic.PebBaseAddress
	ptrSize := unsafe.Sizeof(uintptr(0))
	// WOW64 进程：64 位 PEB 的 ProcessParameters 属于 64 位子系统，32 位进程的真实参数块在 32 位 PEB——
	// 经 ProcessWow64Information 取 32 位 PEB 地址，按 32 位结构偏移读。
	if ptrSize == 8 {
		var peb32 uintptr
		st, _, _ := procNtQueryInformationProcess.Call(
			uintptr(handle), processWow64Information,
			uintptr(unsafe.Pointer(&peb32)), unsafe.Sizeof(peb32), 0,
		)
		if st == 0 && peb32 != 0 {
			peb = peb32
			ptrSize = 4
		}
	}
	paramsOffset := uintptr(0x20)
	curOffset := uintptr(0x38)
	curSize := uintptr(16)
	if ptrSize == 4 {
		paramsOffset, curOffset, curSize = 0x10, 0x24, 8
	}
	params, ok := readRemotePointer(handle, peb+paramsOffset, ptrSize)
	if !ok || params == 0 {
		return ""
	}
	current, ok := readRemoteMemory(handle, params+curOffset, curSize)
	if !ok {
		return ""
	}
	length := int(binary.LittleEndian.Uint16(current[0:2]))
	if length <= 0 || length > 4096 || length%2 != 0 {
		return ""
	}
	var buffer uintptr
	if ptrSize == 4 {
		buffer = uintptr(binary.LittleEndian.Uint32(current[4:8]))
	} else {
		buffer = uintptr(binary.LittleEndian.Uint64(current[8:16]))
	}
	if buffer == 0 {
		return ""
	}
	raw, ok := readRemoteMemory(handle, buffer, uintptr(length))
	if !ok {
		return ""
	}
	return syscall.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(&raw[0])), length/2))
}

// readRemoteMemory 读目标进程内存（ReadProcessMemory 全量命中才算成功）。
func readRemoteMemory(handle syscall.Handle, addr, size uintptr) ([]byte, bool) {
	if size == 0 {
		return nil, false
	}
	buf := make([]byte, size)
	var read uintptr
	ret, _, _ := procReadProcessMemory.Call(
		uintptr(handle), addr,
		uintptr(unsafe.Pointer(&buf[0])), size,
		uintptr(unsafe.Pointer(&read)),
	)
	if ret == 0 || read != size {
		return nil, false
	}
	return buf, true
}

// readRemotePointer 读目标进程内一个指针（按目标位宽解码）。
func readRemotePointer(handle syscall.Handle, addr, ptrSize uintptr) (uintptr, bool) {
	raw, ok := readRemoteMemory(handle, addr, ptrSize)
	if !ok {
		return 0, false
	}
	if ptrSize == 4 {
		return uintptr(binary.LittleEndian.Uint32(raw)), true
	}
	return uintptr(binary.LittleEndian.Uint64(raw)), true
}

// describeProcCWDList 进程清单转文本（报告 / 日志用——pid + 映像名 + cwd，最多 limit 条）。
func describeProcCWDList(procs []procCWDInfo, limit int) string {
	parts := make([]string, 0, len(procs))
	for i, proc := range procs {
		if i >= limit {
			parts = append(parts, "…（其余省略）")
			break
		}
		parts = append(parts, "pid "+strconv.Itoa(proc.PID)+" "+proc.Name+" → "+proc.CWD)
	}
	return strings.Join(parts, "；")
}

// pathWithin 判断路径是否等于目录或落在其下（大小写不敏感）。
//
// 与 hostPathUnder 的分工：那个判「可执行文件在目录内」（严格前缀），本函数判「工作目录命中」——
// cwd 恰好等于运行区目录本身就是最典型的持有形态，必须算命中。
func pathWithin(path, dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	base := strings.TrimRight(abs, `\/`)
	if base == "" {
		return false
	}
	normalized := strings.TrimRight(path, `\/`)
	if strings.EqualFold(normalized, base) {
		return true
	}
	return strings.HasPrefix(strings.ToLower(normalized), strings.ToLower(base)+string(os.PathSeparator))
}

// isSweepExemptImage 判定映像是否属于部署链自身的清扫面（宿主 / cs 桥 / py / ts / browser）。
//
// 这些进程由 deploy 段1b 处置（有窗口、有回执）；重启前置检查只拦「清扫面之外的 cwd 持有者」——
// 判据与「不碰用户进程」同口径。
func isSweepExemptImage(name string) bool {
	if isCWDServiceImage(name) {
		return true
	}
	for _, image := range strayImageNames {
		if strings.EqualFold(name, image) {
			return true
		}
	}
	return false
}
