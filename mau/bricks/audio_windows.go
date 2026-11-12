//go:build windows

package bricks

// AUDIO 播放实现（Windows）——winmm waveOut 共享模式，音量在 PCM 数据上乘系数。
//
// 对齐 C# WasapiPlayer 的对外语义：起播成功即返回（播放异步进行），失败返回 error 由服务出声。
// 与 C# 的差异（已知项，记 L3）：只支持 PCM 16bit 源；共享模式由系统混音器重采样。

import (
	"encoding/binary"
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

var (
	audioWinmm                = syscall.NewLazyDLL("winmm.dll")
	procAudioWaveOutOpen      = audioWinmm.NewProc("waveOutOpen")
	procAudioWaveOutClose     = audioWinmm.NewProc("waveOutClose")
	procAudioWaveOutPrepare   = audioWinmm.NewProc("waveOutPrepareHeader")
	procAudioWaveOutUnprepare = audioWinmm.NewProc("waveOutUnprepareHeader")
	procAudioWaveOutWrite     = audioWinmm.NewProc("waveOutWrite")
	procAudioWaveOutGetPos    = audioWinmm.NewProc("waveOutGetPosition")
)

// waveMapper 是 WAVE_MAPPER（设备 id -1）。
const waveMapper = ^uintptr(0)

// timeSamples 是 MMTIME 的 TIME_SAMPLES 类型。
const timeSamples = 2

// waveHeader 对应 WAVEHDR（x64 布局 48 字节）。
type waveHeader struct {
	data          *byte
	bufferLength  uint32
	bytesRecorded uint32
	user          uintptr
	flags         uint32
	loops         uint32
	next          uintptr
	reserved      uintptr
}

// playWAVFile 播放一个 WAV 文件（阻塞到播完或超时）。
func playWAVFile(path string, volume float64) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	wav, err := parseWAV(raw)
	if err != nil {
		return err
	}
	pcm := toStereo16(wav, volume)
	if len(pcm) == 0 {
		return nil
	}
	return waveOutPlay(pcm, wav.sampleRate, 2, 16)
}

// waveFormatBytes 构造 WAVEFORMATEX 的 18 字节序列——手工序列化避免结构体对齐歧义。
func waveFormatBytes(sampleRate, channels, bits int) []byte {
	buf := make([]byte, 18)
	binary.LittleEndian.PutUint16(buf[0:], 1) // WAVE_FORMAT_PCM
	binary.LittleEndian.PutUint16(buf[2:], uint16(channels))
	binary.LittleEndian.PutUint32(buf[4:], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[8:], uint32(sampleRate*channels*bits/8))
	binary.LittleEndian.PutUint16(buf[12:], uint16(channels*bits/8))
	binary.LittleEndian.PutUint16(buf[14:], uint16(bits))
	binary.LittleEndian.PutUint16(buf[16:], 0) // cbSize
	return buf
}

// waveOutPlay 打开设备 → 写缓冲 → 轮询播完 → 关闭。
func waveOutPlay(pcm []byte, sampleRate, channels, bits int) error {
	format := waveFormatBytes(sampleRate, channels, bits)
	var handle uintptr
	r, _, _ := procAudioWaveOutOpen.Call(
		uintptr(unsafe.Pointer(&handle)),
		waveMapper,
		uintptr(unsafe.Pointer(&format[0])),
		0, 0, 0,
	)
	if r != 0 {
		return fmt.Errorf("waveOutOpen 失败（MMSYSERR %d——无音频设备或格式不支持）", r)
	}
	defer procAudioWaveOutClose.Call(handle)

	header := &waveHeader{data: &pcm[0], bufferLength: uint32(len(pcm))}
	headerSize := unsafe.Sizeof(*header)
	if r, _, _ := procAudioWaveOutPrepare.Call(handle, uintptr(unsafe.Pointer(header)), headerSize); r != 0 {
		return fmt.Errorf("waveOutPrepareHeader 失败（MMSYSERR %d）", r)
	}
	defer procAudioWaveOutUnprepare.Call(handle, uintptr(unsafe.Pointer(header)), headerSize)
	if r, _, _ := procAudioWaveOutWrite.Call(handle, uintptr(unsafe.Pointer(header)), headerSize); r != 0 {
		return fmt.Errorf("waveOutWrite 失败（MMSYSERR %d）", r)
	}

	bytesPerFrame := channels * bits / 8
	frames := uint32(len(pcm) / bytesPerFrame)
	if frames == 0 || sampleRate <= 0 {
		return nil
	}
	playDuration := time.Duration(float64(frames)/float64(sampleRate)*float64(time.Second)) + 5*time.Second
	deadline := time.Now().Add(playDuration)
	// MMTIME 按 16 字节缓冲（x64 下 union 含 SMPTE 子结构）——只读 TIME_SAMPLES 的 sample 字段。
	var position [16]byte
	for time.Now().Before(deadline) {
		binary.LittleEndian.PutUint32(position[0:], timeSamples)
		r, _, _ := procAudioWaveOutGetPos.Call(handle, uintptr(unsafe.Pointer(&position[0])), uintptr(len(position)))
		if r == 0 && binary.LittleEndian.Uint32(position[4:8]) >= frames {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	// 超时按完成处理——不阻塞同 Key 后续队列（超时本身记不到日志是已知边界）。
	return nil
}
