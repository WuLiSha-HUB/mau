package bricks

// AUDIO 拼接面——多段 WAV 合成单文件（A265：轮末总结三段成一条）。
//
// 语义对齐 C# `Mau.Audio.AudioService.Join` / `WriteJoined` / `ToStereo44100`：
//   - 源路径逐段根寻址展开并验存在，缺一段整单失败（ERR|NOT_FOUND，不跳过）；
//   - 目标目录不存在则创建；覆盖写；
//   - 统一重采样到 44.1 kHz / 16bit / 立体声：单声道复制到双声道，采样率线性插值；
//   - 取整按 C# `Math.Round(double)` 默认的 ToEven 口径（逐字节对拍的前提）；
//   - 失败返回 "ERR|CODE|说明"（可见，不静默）——既有播放面不受影响。
//
// 与 C# 的实现差异（如实记录）：C# 走 WaveFileWriter 流式写，Go 侧一次性缓冲后写出——
// 播报音频体积小（C# ReadAll 注释同判据），换取纯函数可测。

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

// Join 多段 WAV 拼接——按顺序读入，统一重采样到 44.1 kHz / 16bit / 2ch 后写成单文件。
//
// paths：源 WAV 路径（按播放顺序）；outPath：目标 WAV（覆盖写，目录不存在则创建）。
// 已写出返回空串；失败返回 "ERR|CODE|说明"。
func (s *AudioService) Join(paths []string, outPath string) string {
	if len(paths) == 0 {
		return "ERR|BAD_ARGS|paths 为空"
	}
	if outPath == "" {
		return "ERR|BAD_ARGS|outPath 为空"
	}
	resolved := make([]string, 0, len(paths))
	for _, one := range paths {
		full := s.expandPath(one)
		info, err := os.Stat(full)
		if err != nil || info.IsDir() {
			return "ERR|NOT_FOUND|源文件不存在: " + full
		}
		resolved = append(resolved, full)
	}
	if dir := filepath.Dir(outPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "ERR|JOIN_FAIL|拼接失败: " + err.Error()
		}
	}
	if err := writeJoinedWAV(resolved, outPath); err != nil {
		return "ERR|JOIN_FAIL|拼接失败: " + err.Error()
	}
	return ""
}

// writeJoinedWAV 拼接写出体——逐段解析并把转换后的 PCM 追加到同一缓冲，最后一次落盘。
func writeJoinedWAV(paths []string, outPath string) error {
	var pcm []byte
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		wav, err := parseWAV(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		pcm = append(pcm, resampleStereo44100(wav)...)
	}
	return writePCM16WAV(outPath, pcm)
}

// resampleStereo44100 任意 16bit 源 → 44.1 kHz / 16bit / 2ch PCM——单声道复制到双声道，采样率线性插值。
func resampleStereo44100(wav *wavData) []byte {
	if wav == nil || wav.sampleRate <= 0 {
		return nil
	}
	channels := wav.channels
	if channels <= 0 {
		channels = 1
	}
	frames := len(wav.pcm) / 2 / channels
	if frames <= 0 {
		return nil
	}
	dstFrames := int(math.RoundToEven(float64(frames) * 44100.0 / float64(wav.sampleRate)))
	out := make([]byte, dstFrames*4)
	for i := 0; i < dstFrames; i++ {
		position := float64(i) * float64(wav.sampleRate) / 44100.0
		first := int(position)
		if first >= frames {
			first = frames - 1
		}
		second := first + 1
		if second >= frames {
			second = frames - 1
		}
		frac := position - float64(first)
		for c := 0; c < 2; c++ {
			src := c
			if src >= channels {
				src = 0
			}
			v0 := readSample16(wav.pcm, first*channels+src)
			v1 := readSample16(wav.pcm, second*channels+src)
			value := int(math.RoundToEven(float64(v0) + float64(v1-v0)*frac))
			offset := (i*2 + c) * 2
			binary.LittleEndian.PutUint16(out[offset:], uint16(int16(value)))
		}
	}
	return out
}

// readSample16 读一个 16bit 小端样本——越界返回 0（C# ReadShort 同口径）。
func readSample16(buf []byte, sampleIndex int) int16 {
	offset := sampleIndex * 2
	if offset < 0 || offset+1 >= len(buf) {
		return 0
	}
	return int16(binary.LittleEndian.Uint16(buf[offset : offset+2]))
}

// writePCM16WAV 写 44 字节标准头 + PCM 数据（44.1 kHz / 16bit / 2ch）。
func writePCM16WAV(outPath string, pcm []byte) error {
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+len(pcm)))
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)         // formatTag = PCM
	binary.LittleEndian.PutUint16(header[22:24], 2)         // 2ch
	binary.LittleEndian.PutUint32(header[24:28], 44100)     // 采样率
	binary.LittleEndian.PutUint32(header[28:32], 44100*2*2) // byte rate
	binary.LittleEndian.PutUint16(header[32:34], 4)         // block align
	binary.LittleEndian.PutUint16(header[34:36], 16)        // bits
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(len(pcm)))
	body := make([]byte, 0, len(header)+len(pcm))
	body = append(body, header...)
	body = append(body, pcm...)
	return os.WriteFile(outPath, body, 0o644)
}
