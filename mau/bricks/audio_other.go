//go:build !windows

package bricks

// AUDIO 播放实现（非 Windows）——明示不可用，不静默回落。

import "errors"

// playWAVFile 播放一个 WAV 文件——非 Windows 平台不支持。
func playWAVFile(path string, volume float64) error {
	return errors.New("音频播放仅支持 Windows（当前平台不可用）")
}
