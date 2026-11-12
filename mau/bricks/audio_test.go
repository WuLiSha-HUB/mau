package bricks

// AudioCat 两件积木与播放服务单测——参数面 / 服务层错误面 / 队列调度 / WAV 解析 / 音量解析。
//
// 实机播放用例（Windows）用 0.2 s 静音 WAV；无声卡时起播失败只记日志、队列照常前进，
// 判据取「队列归零 + 不 panic」，与 C# 起播失败语义一致。

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// writeTestWAV 写一个最小 PCM WAV（16bit）。
func writeTestWAV(t *testing.T, path string, frames, channels, sampleRate int) {
	t.Helper()
	dataLen := frames * channels * 2
	buf := make([]byte, 44+dataLen)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataLen))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate*channels*2))
	binary.LittleEndian.PutUint16(buf[32:34], uint16(channels*2))
	binary.LittleEndian.PutUint16(buf[34:36], 16)
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataLen))
	// 静音（全 0）——不制造噪声
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatalf("写测试 WAV 失败: %v", err)
	}
}

// TestAudioPlayArgs audio.play 参数面 + 服务未注入。
func TestAudioPlayArgs(t *testing.T) {
	if out, ok := AudioPlay(nil, ""); ok || !strings.Contains(out, "ERR|BAD_ARGS|") {
		t.Fatalf("空载荷 = %q / %v", out, ok)
	}
	if out, ok := AudioPlay(nil, `{"path":""}`); ok || !strings.HasPrefix(out, "ERR|BAD_ARGS|") {
		t.Fatalf("空 path = %q / %v", out, ok)
	}
	if out, ok := AudioPlay(nil, `{"path":"a.wav","nope":1}`); ok || !strings.Contains(out, "未知参数: nope") {
		t.Fatalf("未知键 = %q / %v", out, ok)
	}
	if out, ok := AudioPlay(nil, `{"path":"a.wav"}`); ok || out != "ERR|AUDIO_NO_SERVICE|宿主未注入 IAudioService" {
		t.Fatalf("无服务 = %q / %v", out, ok)
	}
}

// TestAudioEnqueueErrors 服务层错误面——path 空 / 文件不存在 / 目录。
func TestAudioEnqueueErrors(t *testing.T) {
	svc := NewAudioService(nil, nil, nil)
	if out := svc.Enqueue("", "", 0); out != "ERR|BAD_ARGS|path 为空" {
		t.Fatalf("空 path = %q", out)
	}
	if out := svc.Enqueue("", filepath.Join(t.TempDir(), "missing.wav"), 0); !strings.HasPrefix(out, "ERR|NOT_FOUND|文件不存在: ") {
		t.Fatalf("不存在 = %q", out)
	}
	dir := t.TempDir()
	if out := svc.Enqueue("", dir, 0); !strings.HasPrefix(out, "ERR|NOT_FOUND|文件不存在: ") {
		t.Fatalf("目录 = %q", out)
	}
}

// TestAudioResolveVolume 音量解析——显式覆盖 / 配置夹取 / 缺省。
func TestAudioResolveVolume(t *testing.T) {
	cfg := func(value string) func(key, def string) string {
		return func(key, def string) string {
			if value == "" {
				return def
			}
			return value
		}
	}
	svc := NewAudioService(nil, cfg(""), nil)
	if got := svc.ResolveVolume(0.5); got != 0.5 {
		t.Fatalf("显式音量 = %v", got)
	}
	if got := svc.ResolveVolume(-1); got != 1.0 {
		t.Fatalf("缺省音量 = %v", got)
	}
	svc = NewAudioService(nil, cfg("150"), nil)
	if got := svc.ResolveVolume(-1); got != 1.5 {
		t.Fatalf("配置 150 = %v", got)
	}
	svc = NewAudioService(nil, cfg("250"), nil)
	if got := svc.ResolveVolume(-1); got != 2.0 {
		t.Fatalf("配置 250 夹取 = %v", got)
	}
	svc = NewAudioService(nil, cfg("-5"), nil)
	if got := svc.ResolveVolume(-1); got != 0 {
		t.Fatalf("配置 -5 夹取 = %v", got)
	}
}

// TestAudioQueuePerKey 同 Key 串行排队 / 不同 Key 并发 / 队列归零。
func TestAudioQueuePerKey(t *testing.T) {
	dir := t.TempDir()
	wavA := filepath.Join(dir, "a.wav")
	wavB := filepath.Join(dir, "b.wav")
	wavC := filepath.Join(dir, "c.wav")
	writeTestWAV(t, wavA, 800, 1, 8000)
	writeTestWAV(t, wavB, 800, 1, 8000)
	writeTestWAV(t, wavC, 800, 1, 8000)

	release := make(chan struct{})
	var started int32
	svc := NewAudioService(nil, nil, nil)
	svc.SetPlayer(func(path string, volume float64) error {
		atomic.AddInt32(&started, 1)
		<-release // 阻塞到测试放行
		return nil
	})
	if out := svc.Enqueue("k1", wavA, 0); out != "" {
		t.Fatalf("入队 a = %q", out)
	}
	if out := svc.Enqueue("k1", wavB, 0); out != "" {
		t.Fatalf("入队 b = %q", out)
	}
	if out := svc.Enqueue("k2", wavC, 0); out != "" {
		t.Fatalf("入队 c = %q", out)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if svc.ActiveKeyCount() == 2 && atomic.LoadInt32(&started) == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := svc.ActiveKeyCount(); got != 2 {
		t.Fatalf("活跃 Key 数 = %d（期望 2）", got)
	}
	if got := atomic.LoadInt32(&started); got != 2 {
		t.Fatalf("起播数 = %d（期望 2——k1 一个 + k2 一个）", got)
	}
	close(release)
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if svc.ActiveKeyCount() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := svc.ActiveKeyCount(); got != 0 {
		t.Fatalf("队列未归零：活跃 Key 数 = %d", got)
	}
}

// TestAudioStartFailureVisible 起播失败只出声、不阻塞队列。
func TestAudioStartFailureVisible(t *testing.T) {
	dir := t.TempDir()
	wav := filepath.Join(dir, "x.wav")
	writeTestWAV(t, wav, 400, 1, 8000)
	var mu sync.Mutex
	var logs []string
	svc := NewAudioService(nil, nil, func(level int, message string) {
		mu.Lock()
		logs = append(logs, message)
		mu.Unlock()
	})
	svc.SetPlayer(func(path string, volume float64) error {
		return os.ErrPermission
	})
	if out := svc.Enqueue("", wav, 0); out != "" {
		t.Fatalf("入队 = %q", out)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if svc.ActiveKeyCount() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logs) == 0 || !strings.Contains(logs[0], "起播失败") {
		t.Fatalf("失败未出声：%v", logs)
	}
}

// TestParseWAV 解析面——合法 / 非 RIFF / 8bit / 缺 data。
func TestParseWAV(t *testing.T) {
	dir := t.TempDir()
	ok := filepath.Join(dir, "ok.wav")
	writeTestWAV(t, ok, 100, 2, 44100)
	raw, err := os.ReadFile(ok)
	if err != nil {
		t.Fatalf("读测试 WAV: %v", err)
	}
	wav, err := parseWAV(raw)
	if err != nil {
		t.Fatalf("合法 WAV 解析失败: %v", err)
	}
	if wav.channels != 2 || wav.sampleRate != 44100 || wav.bits != 16 {
		t.Fatalf("格式 = %+v", wav)
	}
	if _, err := parseWAV([]byte("not a wave file")); err == nil {
		t.Fatal("非 RIFF 应报错")
	}
	// 8bit：改 fmt 的 bitsPerSample 字段（偏移 34）
	bad := append([]byte(nil), raw...)
	binary.LittleEndian.PutUint16(bad[34:36], 8)
	if _, err := parseWAV(bad); err == nil || !strings.Contains(err.Error(), "16bit") {
		t.Fatalf("8bit 应报错，实际 %v", err)
	}
	// 缺 data：把 data 块 id 改成 "junk"
	junk := append([]byte(nil), raw...)
	copy(junk[36:40], "junk")
	if _, err := parseWAV(junk); err == nil {
		t.Fatal("缺 data 应报错")
	}
}

// TestToStereo16 单声道复制到双声道 + 音量夹取（不溢出）。
func TestToStereo16(t *testing.T) {
	wav := &wavData{channels: 1, sampleRate: 8000, bits: 16, pcm: make([]byte, 6)}
	// 三个样本：0x7FFF / -32768 / 16000
	binary.LittleEndian.PutUint16(wav.pcm[0:2], 0x7FFF)
	binary.LittleEndian.PutUint16(wav.pcm[2:4], 0x8000)
	binary.LittleEndian.PutUint16(wav.pcm[4:6], 16000)
	out := toStereo16(wav, 2.0)
	if len(out) != 12 {
		t.Fatalf("输出长度 = %d（期望 3 帧 × 4 字节）", len(out))
	}
	if got := int16(binary.LittleEndian.Uint16(out[0:2])); got != 32767 {
		t.Fatalf("正溢出夹取 = %d", got)
	}
	if got := int16(binary.LittleEndian.Uint16(out[2:4])); got != 32767 {
		t.Fatalf("右声道复制 = %d", got)
	}
	if got := int16(binary.LittleEndian.Uint16(out[4:6])); got != -32768 {
		t.Fatalf("负溢出夹取 = %d", got)
	}
	half := toStereo16(wav, 0.5)
	if got := int16(binary.LittleEndian.Uint16(half[8:10])); got != 8000 {
		t.Fatalf("半音量 = %d（期望 8000）", got)
	}
}

// TestAudioSpeakArgs audio.speak 参数面 / py 未注入 / out 根寻址展开。
func TestAudioSpeakArgs(t *testing.T) {
	if out, ok := AudioSpeak(AudioTTSConfig{}, nil, `{"text":""}`); ok || !strings.HasPrefix(out, "ERR|BAD_ARGS|") {
		t.Fatalf("空 text = %q / %v", out, ok)
	}
	if out, ok := AudioSpeak(AudioTTSConfig{}, nil, `{"text":"你好"}`); ok || out != "ERR|PY_NO_SERVICE|宿主未注入 IPyService" {
		t.Fatalf("无 py 服务 = %q / %v", out, ok)
	}
	// out 显式给定 → 根寻址展开后进入 py 载荷（用假 py 服务截获载荷）
	var captured string
	cfg := AudioTTSConfig{
		Resolve: func(path string) string { return "/expanded/" + path },
		Py:      pyInvokerFunc(func(name, payloadJSON string) string { captured = payloadJSON; return `{"ok":false,"error":"stop"}` }),
	}
	if out, ok := AudioSpeak(cfg, nil, `{"text":"你好","out":"wav:a.wav"}`); ok || !strings.Contains(out, "TTS_FAIL|stop") {
		t.Fatalf("py 失败 = %q / %v", out, ok)
	}
	if !strings.Contains(captured, "/expanded/wav:a.wav") || !strings.Contains(captured, `"op":"speak"`) {
		t.Fatalf("载荷 = %s", captured)
	}
}

// pyInvokerFunc 把函数适配成 PyInvoker。
type pyInvokerFunc func(name, payloadJSON string) string

// Invoke 调 py 服务（测试替身）。
func (f pyInvokerFunc) Invoke(name, payloadJSON string) string { return f(name, payloadJSON) }

// TestAudioSpeakSuccess audio.speak 成功链——py 回执 ok + 产物存在 + 入队。
func TestAudioSpeakSuccess(t *testing.T) {
	dir := t.TempDir()
	produced := filepath.Join(dir, "tts.wav")
	writeTestWAV(t, produced, 400, 1, 8000)
	svc := NewAudioService(nil, nil, nil)
	svc.SetPlayer(func(path string, volume float64) error { return nil })
	cfg := AudioTTSConfig{
		Voice: func() string { return "zf_072" },
		Py: pyInvokerFunc(func(name, payloadJSON string) string {
			if !strings.Contains(payloadJSON, "zf_072") {
				t.Fatalf("缺省音色未进载荷: %s", payloadJSON)
			}
			return `{"ok":true,"out":"` + strings.ReplaceAll(produced, `\`, `\\`) + `"}`
		}),
	}
	out, ok := AudioSpeak(cfg, svc, `{"text":"你好"}`)
	if !ok || out != "OK|"+produced {
		t.Fatalf("成功回执 = %q / %v", out, ok)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if svc.ActiveKeyCount() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestBuildTTSOutPath 产物路径——out_dir 优先 / 缺省临时目录。
func TestBuildTTSOutPath(t *testing.T) {
	dir := t.TempDir()
	got := buildTTSOutPath(func() string { return dir })
	if filepath.Dir(got) != dir || !strings.HasSuffix(got, ".wav") || !strings.HasPrefix(filepath.Base(got), "tts_") {
		t.Fatalf("out_dir 路径 = %q", got)
	}
	fallback := buildTTSOutPath(nil)
	if !strings.Contains(fallback, "cat-home4-tts") {
		t.Fatalf("缺省路径 = %q", fallback)
	}
}

// TestAudioPlayEndToEnd 实机播放——真实 waveOut 播 0.2 s 静音；无声卡时起播失败亦须队列归零。
func TestAudioPlayEndToEnd(t *testing.T) {
	dir := t.TempDir()
	wav := filepath.Join(dir, "e2e.wav")
	writeTestWAV(t, wav, 1600, 1, 8000) // 0.2 s
	var logs int32
	svc := NewAudioService(nil, nil, func(level int, message string) { atomic.AddInt32(&logs, 1) })
	if out, ok := AudioPlay(svc, `{"path":"`+strings.ReplaceAll(wav, `\`, `\\`)+`"}`); !ok || out != "OK|已入队" {
		t.Fatalf("实机播放 = %q / %v", out, ok)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if svc.ActiveKeyCount() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("播放未在 5s 内结束（活跃 Key 仍 %d，失败日志 %d 条）", svc.ActiveKeyCount(), atomic.LoadInt32(&logs))
}
