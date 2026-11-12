package bricks

// AUDIO 播放服务——per-Key 串行 + 组间并发（对齐 C# Mau.Audio.AudioService）。
//
// 语义（design-mau-audio §四 / §五）：
//   - Enqueue 是主线程唯一动作：起播或按 Key 排队，立即返回（只进不出，完成信号不回业务）；
//   - 同 Key 先来后到串行，不同 Key 并发；
//   - volume < 0 时读全局配置 audio.volume（百分比 0..200，夹取后 /100）；
//   - 起播失败只记日志并前进队列（Enqueue 已返回空串——失败可见靠日志，C# 同行为）。
//
// 播放实现按平台拆分：audio_windows.go（waveOut）/ audio_other.go（明示不可用）。

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AudioDefaultKey 是空 Key 的归一值——不带 Key 的所有音频自动串行（对齐 C# AudioService.DefaultKey）。
const AudioDefaultKey = "_default"

// audioItem 是一个待播条目（路径 + 会话音量；volume < 0 = 用全局配置）。
type audioItem struct {
	path   string
	volume float64
}

// AudioService 音频播放服务——调度状态自洽，主线程只做入队。
type AudioService struct {
	mu      sync.Mutex
	playing map[string]bool
	queues  map[string][]audioItem

	resolve  func(string) string          // 根寻址展开（nil = 原样返回）
	cfgGet   func(key, def string) string // 读配置（nil = 一律用 def）
	logf     func(level int, message string)
	playerFn func(path string, volume float64) error // 播放实现（测试可替换；nil = 平台默认）
}

// NewAudioService 构造播放服务。
//
// resolve：根寻址展开（失败原样返回，最终由 File.Exists 判定）；
// cfgGet：配置读面（key, 缺省值）→ 值；
// logf：失败出声出口（nil = 不出声）。
func NewAudioService(resolve func(string) string, cfgGet func(key, def string) string, logf func(level int, message string)) *AudioService {
	if resolve == nil {
		resolve = func(path string) string { return path }
	}
	service := &AudioService{
		playing: make(map[string]bool),
		queues:  make(map[string][]audioItem),
		resolve: resolve,
		cfgGet:  cfgGet,
		logf:    logf,
	}
	service.playerFn = playWAVFile
	return service
}

// SetPlayer 替换播放实现（测试注入；nil 恢复平台默认）。
func (s *AudioService) SetPlayer(fn func(path string, volume float64) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fn == nil {
		s.playerFn = playWAVFile
		return
	}
	s.playerFn = fn
}

// Enqueue 入队——某 Key 无在播则立即起播，否则按 Key 排队（先来后到），立即返回。
//
// 已受理返回空串；失败返回 "ERR|CODE|说明"（可见，不静默）。
func (s *AudioService) Enqueue(key, path string, volume float64) string {
	if path == "" {
		return "ERR|BAD_ARGS|path 为空"
	}
	resolved := s.expandPath(path)
	if info, err := os.Stat(resolved); err != nil || info.IsDir() {
		return "ERR|NOT_FOUND|文件不存在: " + resolved
	}
	k := key
	if k == "" {
		k = AudioDefaultKey
	}
	s.mu.Lock()
	if s.playing[k] {
		s.queues[k] = append(s.queues[k], audioItem{path: resolved, volume: volume})
		s.mu.Unlock()
		return ""
	}
	s.playing[k] = true
	s.mu.Unlock()
	go s.playLoop(k, audioItem{path: resolved, volume: volume})
	return ""
}

// ActiveKeyCount 活跃 Key 数——诊断哨兵（当前有音频在播的 Key 数）。
func (s *AudioService) ActiveKeyCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.playing)
}

// expandPath 根寻址展开（幂等；非根寻址 / 展开失败原样返回）。
func (s *AudioService) expandPath(path string) string {
	if s.resolve == nil {
		return path
	}
	if expanded := s.resolve(path); expanded != "" {
		return expanded
	}
	return path
}

// playLoop 单 Key 播放循环——播完一个取下一个，队列空则复位该 Key。
func (s *AudioService) playLoop(key string, first audioItem) {
	item := first
	for {
		s.playOne(key, item)
		s.mu.Lock()
		queue := s.queues[key]
		if len(queue) == 0 {
			delete(s.queues, key)
			delete(s.playing, key)
			s.mu.Unlock()
			return
		}
		item = queue[0]
		s.queues[key] = queue[1:]
		s.mu.Unlock()
	}
}

// playOne 播一个条目——失败只出声（对齐 C# StartPlay 的 catch 分支：不改变已返回的回执）。
func (s *AudioService) playOne(key string, item audioItem) {
	s.mu.Lock()
	player := s.playerFn
	s.mu.Unlock()
	if player == nil {
		return
	}
	volume := s.ResolveVolume(item.volume)
	if err := player(item.path, volume); err != nil {
		s.logFail(key, "起播失败: "+err.Error())
	}
}

// ResolveVolume 解析音量——requested < 0 用全局配置 audio.volume（0..200 夹取 /100）；否则原值。
func (s *AudioService) ResolveVolume(requested float64) float64 {
	if requested >= 0 {
		return requested
	}
	pct := 100.0
	if s.cfgGet != nil {
		if text := s.cfgGet("audio.volume", "100"); text != "" {
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(text), 64); err == nil {
				pct = parsed
			}
		}
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 200 {
		pct = 200
	}
	return pct / 100
}

// logFail 失败出声（AGENTS §一-4——失败必须可见）。
func (s *AudioService) logFail(key, message string) {
	if s.logf != nil {
		s.logf(3, "["+key+"] "+message)
	}
}

// ── WAV 解析与转换（纯函数，便于单测）──

// wavData 是解析后的 PCM 16bit 波形。
type wavData struct {
	sampleRate int
	channels   int
	bits       int
	pcm        []byte
}

// parseWAV 解析 RIFF/WAVE——只接受 PCM 16bit（与 Join 的「仅支持 16bit 源」同口径）。
//
// 截断容忍：data 块声明长度超出文件时按实际剩余字节取；未知块跳过。
func parseWAV(data []byte) (*wavData, error) {
	if len(data) < 12 {
		return nil, errors.New("WAV 头不完整")
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, errors.New("非 RIFF/WAVE 文件")
	}
	wav := &wavData{}
	pos := 12
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		body := pos + 8
		if size < 0 || body+size > len(data) {
			size = len(data) - body
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, errors.New("fmt 块不完整")
			}
			tag := binary.LittleEndian.Uint16(data[body : body+2])
			channels := int(binary.LittleEndian.Uint16(data[body+2 : body+4]))
			sampleRate := int(binary.LittleEndian.Uint32(data[body+4 : body+8]))
			bits := int(binary.LittleEndian.Uint16(data[body+14 : body+16]))
			if tag != 1 {
				return nil, fmt.Errorf("仅支持 PCM 格式（formatTag=%d）", tag)
			}
			if bits != 16 {
				return nil, fmt.Errorf("仅支持 16bit 源（%dbit）", bits)
			}
			if channels <= 0 || sampleRate <= 0 {
				return nil, errors.New("WAV 格式非法（声道 / 采样率为零）")
			}
			wav.channels = channels
			wav.sampleRate = sampleRate
			wav.bits = bits
		case "data":
			if wav.channels == 0 {
				return nil, errors.New("data 块先于 fmt 块")
			}
			wav.pcm = data[body : body+size]
		}
		pos = body + size
		if pos%2 == 1 {
			pos++
		}
	}
	if wav.channels == 0 || wav.pcm == nil {
		return nil, errors.New("缺少 fmt/data 块")
	}
	return wav, nil
}

// toStereo16 单声道复制到双声道并应用音量（0..2）——超界样本夹取，不溢出。
func toStereo16(wav *wavData, volume float64) []byte {
	if wav == nil || wav.channels <= 0 {
		return nil
	}
	frames := len(wav.pcm) / (2 * wav.channels)
	out := make([]byte, frames*4)
	for i := 0; i < frames; i++ {
		for c := 0; c < 2; c++ {
			src := c
			if src >= wav.channels {
				src = 0
			}
			off := (i*wav.channels + src) * 2
			raw := int16(binary.LittleEndian.Uint16(wav.pcm[off : off+2]))
			scaled := int(math.Round(float64(raw) * volume))
			if scaled > math.MaxInt16 {
				scaled = math.MaxInt16
			}
			if scaled < math.MinInt16 {
				scaled = math.MinInt16
			}
			binary.LittleEndian.PutUint16(out[(i*2+c)*2:], uint16(int16(scaled)))
		}
	}
	return out
}

// buildTTSOutPath 生成 TTS 产物路径——out_dir 配置优先，缺省落系统临时目录 cat-home4-tts。
func buildTTSOutPath(outDir func() string) string {
	dir := ""
	if outDir != nil {
		dir = strings.TrimSpace(outDir())
	}
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "cat-home4-tts")
	}
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "tts_"+time.Now().Format("20060102_150405.000")+".wav")
}
