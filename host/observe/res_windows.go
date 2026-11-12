//go:build windows

package observe

import (
	"syscall"
	"unsafe"
)

// processMemoryCountersEx 是 PROCESS_MEMORY_COUNTERS_EX 的 Go 形态。
type processMemoryCountersEx struct {
	Cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
	PrivateUsage               uintptr
}

var (
	psapiDLL                 = syscall.NewLazyDLL("psapi.dll")
	procGetProcessMemoryInfo = psapiDLL.NewProc("GetProcessMemoryInfo")
)

// readProcessMemory 读本体进程工作集与私有字节；不可读返回 0（失败可见由调用方空白处理）。
func readProcessMemory() (workingSet, privateBytes int64) {
	var counters processMemoryCountersEx
	counters.Cb = uint32(unsafe.Sizeof(counters))
	handle, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0, 0
	}
	ret, _, _ := procGetProcessMemoryInfo.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&counters)),
		uintptr(counters.Cb),
	)
	if ret == 0 {
		return 0, 0
	}
	return int64(counters.WorkingSetSize), int64(counters.PrivateUsage)
}
