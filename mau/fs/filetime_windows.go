//go:build windows

package fs

import (
	"os"
	"syscall"
	"time"
)

// creationTime 取文件 / 目录创建时间（Windows Win32FileAttributeData）。
func creationTime(info os.FileInfo) time.Time {
	if data, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return time.Unix(0, data.CreationTime.Nanoseconds())
	}
	return info.ModTime()
}
