//go:build !windows

package fs

import (
	"os"
	"time"
)

// creationTime 非 Windows 回落修改时间（本支宿主仅 Windows；保留编译面）。
func creationTime(info os.FileInfo) time.Time {
	return info.ModTime()
}
