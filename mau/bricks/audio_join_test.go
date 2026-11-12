package bricks

// 音频拼接面单测——参数面 / 重采样帧数 / 头字段 / 失败面（A265）。

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildTestWAV 构造一段测试 WAV（PCM 16bit，指定采样率与声道数）。
func buildTestWAV(sampleRate, channels int, samples []int16) []byte {
	pcm := make([]byte, len(samples)*2)
	for i, v := range samples {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(v))
	}
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+len(pcm)))
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(sampleRate*channels*2))
	binary.LittleEndian.PutUint16(header[32:34], uint16(channels*2))
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(len(pcm)))
	return append(header, pcm...)
}

// writeTestSource 落一段源 WAV 并返回路径。
func writeTestSource(t *testing.T, dir, name string, sampleRate, channels int, samples []int16) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buildTestWAV(sampleRate, channels, samples), 0o644); err != nil {
		t.Fatalf("写源 WAV 失败: %v", err)
	}
	return path
}

// TestAudioServiceJoinBadArgs 参数面与源缺失——逐条 ERR 文本（C# Join 同文案）。
func TestAudioServiceJoinBadArgs(t *testing.T) {
	service := NewAudioService(nil, nil, nil)
	dir := t.TempDir()
	out := filepath.Join(dir, "joined.wav")
	if got := service.Join(nil, out); got != "ERR|BAD_ARGS|paths 为空" {
		t.Fatalf("空 paths 回执不符: %s", got)
	}
	if got := service.Join([]string{"a.wav"}, ""); got != "ERR|BAD_ARGS|outPath 为空" {
		t.Fatalf("空 outPath 回执不符: %s", got)
	}
	missing := filepath.Join(dir, "missing.wav")
	if got := service.Join([]string{missing}, out); got != "ERR|NOT_FOUND|源文件不存在: "+missing {
		t.Fatalf("源缺失回执不符: %s", got)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatalf("失败路径不应写出目标文件")
	}
}

// TestAudioServiceJoinResample 三段不同采样率 / 声道数拼接——输出统一 44.1 kHz / 16bit / 2ch，帧数按插值口径。
func TestAudioServiceJoinResample(t *testing.T) {
	service := NewAudioService(nil, nil, nil)
	dir := t.TempDir()
	// 段 1：22050 Hz 单声道 1000 帧 → 2000 帧
	mono := make([]int16, 1000)
	for i := range mono {
		mono[i] = 1000
	}
	first := writeTestSource(t, dir, "a.wav", 22050, 1, mono)
	// 段 2：16000 Hz 立体声 800 帧 → 2205 帧（2205 恰为整数，不走 ToEven 边界）
	stereo := make([]int16, 800*2)
	for i := 0; i < 800; i++ {
		stereo[i*2] = 2000
		stereo[i*2+1] = -2000
	}
	second := writeTestSource(t, dir, "b.wav", 16000, 2, stereo)
	// 段 3：44100 Hz 立体声 500 帧 → 原样 500 帧
	third := make([]int16, 500*2)
	for i := 0; i < 500; i++ {
		third[i*2] = 3000
		third[i*2+1] = 3000
	}
	last := writeTestSource(t, dir, "c.wav", 44100, 2, third)

	out := filepath.Join(dir, "nested", "joined.wav")
	if got := service.Join([]string{first, second, last}, out); got != "" {
		t.Fatalf("拼接应成功: %s", got)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("读产物失败: %v", err)
	}
	wav, err := parseWAV(raw)
	if err != nil {
		t.Fatalf("产物不可解析: %v", err)
	}
	if wav.sampleRate != 44100 || wav.channels != 2 || wav.bits != 16 {
		t.Fatalf("产物格式不符: %d Hz / %dch / %dbit", wav.sampleRate, wav.channels, wav.bits)
	}
	wantFrames := 2000 + 2205 + 500
	if frames := len(wav.pcm) / 4; frames != wantFrames {
		t.Fatalf("帧数不符: got %d want %d", frames, wantFrames)
	}
	// 段 1 单声道复制到双声道——首帧左右同值
	if got := readSample16(wav.pcm, 0); got != 1000 {
		t.Fatalf("首帧左声道不符: %d", got)
	}
	if got := readSample16(wav.pcm, 1); got != 1000 {
		t.Fatalf("首帧右声道应为左声道复制: %d", got)
	}
	// 段 2 起点（第 2000 帧）左右各自的信号
	if got := readSample16(wav.pcm, 2000*2); got != 2000 {
		t.Fatalf("段 2 左声道不符: %d", got)
	}
	if got := readSample16(wav.pcm, 2000*2+1); got != -2000 {
		t.Fatalf("段 2 右声道不符: %d", got)
	}
}

// TestAudioServiceJoinRejectsBadSource 源格式不合法——整单失败（ERR|JOIN_FAIL，不跳过坏段）。
func TestAudioServiceJoinRejectsBadSource(t *testing.T) {
	service := NewAudioService(nil, nil, nil)
	dir := t.TempDir()
	good := writeTestSource(t, dir, "good.wav", 44100, 2, []int16{1, 1, 2, 2})
	badPath := filepath.Join(dir, "bad.wav")
	if err := os.WriteFile(badPath, []byte("not a wav"), 0o644); err != nil {
		t.Fatalf("写坏源失败: %v", err)
	}
	out := filepath.Join(dir, "joined.wav")
	got := service.Join([]string{good, badPath}, out)
	if !strings.HasPrefix(got, "ERR|JOIN_FAIL|拼接失败: ") {
		t.Fatalf("坏源回执不符: %s", got)
	}
	// 仅支持 16bit——8bit 源同样整单失败
	eightBit := filepath.Join(dir, "8bit.wav")
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], 36)
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], 1)
	binary.LittleEndian.PutUint32(header[24:28], 8000)
	binary.LittleEndian.PutUint32(header[28:32], 8000)
	binary.LittleEndian.PutUint16(header[32:34], 1)
	binary.LittleEndian.PutUint16(header[34:36], 8)
	copy(header[36:40], "data")
	if err := os.WriteFile(eightBit, header, 0o644); err != nil {
		t.Fatalf("写 8bit 源失败: %v", err)
	}
	if got := service.Join([]string{eightBit}, out); !strings.HasPrefix(got, "ERR|JOIN_FAIL|拼接失败: ") {
		t.Fatalf("8bit 源回执不符: %s", got)
	}
}

// TestResampleStereo44100Interpolation 重采样口径——同采样率直通、降采样线性插值、帧数按 ToEven 取整。
func TestResampleStereo44100Interpolation(t *testing.T) {
	// 44100 直通：样本原样保留
	direct := &wavData{sampleRate: 44100, channels: 1, bits: 16, pcm: make([]byte, 2*2)}
	firstSample := int16(1234)
	secondSample := int16(-4321)
	binary.LittleEndian.PutUint16(direct.pcm[0:], uint16(firstSample))
	binary.LittleEndian.PutUint16(direct.pcm[2:], uint16(secondSample))
	out := resampleStereo44100(direct)
	if len(out) != 2*4 {
		t.Fatalf("直通帧数不符: %d 字节", len(out))
	}
	if got := readSample16(out, 0); got != 1234 {
		t.Fatalf("直通首帧不符: %d", got)
	}
	if got := readSample16(out, 1); got != 1234 {
		t.Fatalf("直通右声道应为复制: %d", got)
	}
	if got := readSample16(out, 2); got != -4321 {
		t.Fatalf("直通次帧不符: %d", got)
	}
	// 22050 → 44100：帧数翻倍，中间样本按线性插值
	up := make([]int16, 2)
	up[0] = 0
	up[1] = 1000
	pcmBytes := make([]byte, 4)
	binary.LittleEndian.PutUint16(pcmBytes[0:], uint16(up[0]))
	binary.LittleEndian.PutUint16(pcmBytes[2:], uint16(up[1]))
	upsampled := resampleStereo44100(&wavData{sampleRate: 22050, channels: 1, bits: 16, pcm: pcmBytes})
	if frames := len(upsampled) / 4; frames != 4 {
		t.Fatalf("22050→44100 帧数不符: %d", frames)
	}
	// 第 2 帧 pos = 2*22050/44100 = 1.0 → 取第 2 个样本
	if got := readSample16(upsampled, 2*2); got != 1000 {
		t.Fatalf("插值第 3 帧不符: %d", got)
	}
	// 第 1 帧 pos = 0.5 → 0 + (1000-0)*0.5 = 500
	if got := readSample16(upsampled, 1*2); got != 500 {
		t.Fatalf("插值第 2 帧不符: %d", got)
	}
	// ToEven 边界：1 帧 @ 8000 Hz → round(1*44100/8000)=round(5.5125)=6
	short := resampleStereo44100(&wavData{sampleRate: 8000, channels: 1, bits: 16, pcm: pcmBytes[:2]})
	if frames := len(short) / 4; frames != int(math.RoundToEven(44100.0/8000.0)) {
		t.Fatalf("ToEven 取整不符: %d", frames)
	}
}
