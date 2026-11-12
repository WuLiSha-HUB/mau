//go:build !windows

package observe

// readProcessMemory 非 Windows 平台无 PSAPI——返回 0（宿主只在 Windows 运行，此面仅保编译）。
func readProcessMemory() (workingSet, privateBytes int64) {
	return 0, 0
}
