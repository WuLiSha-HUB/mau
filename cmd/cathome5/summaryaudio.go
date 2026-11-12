package main

// 轮末总结播报执行体——C# `ChatSession.Meta.cs` 的 `RunSummarySpeak` / `SpeakOne` / `NormalizeForSpeech`
// / `ResolveSpeakName` / `ResolveSummaryAudioDir` / `ResolveChimePath` 的 Go 平移（A247/A265）。
//
// 三段链：提示音（部署包固定资产）→ 播报名（全局音色）→ 正文（外观音色）；
// 三段各自合成临时 WAV → audio.Join 拼成单文件（块 payload 的 audio 名同源）→ audio.Enqueue 入队。
// 拼接失败回落逐段串行播放（可听性优先）；任一段失败只出声，不影响其它段与已落块。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"cathome5/mau/bricks"
)

// RoundSummaryAudioKey 轮末总结播放键——同键串行（多条总结不叠音；C# RoundSummaryAudioKey 同值）。
const RoundSummaryAudioKey = "roundsummary"

// digitHan 阿拉伯数字 → 汉字（逐位读音）。
var digitHan = []rune("零一二三四五六七八九")

// dottedNumberPattern 点分数字（版本号形态）——逐位转汉字前先定位。
var dottedNumberPattern = regexp.MustCompile(`\d+(?:\.\d+)+`)

// speechReplacements 播报同音替换表——只收明确读错的词（Kokoro 的 ZHG2P 按分词选音）。
//
// 词表外一律不动；新增词目须先实测确认读错（判例：「重来 / 重做 / 重跑」读 zhòng，「重启 / 重新 / 重量 / 重要」正确）。
var speechReplacements = [][2]string{
	{"重来", "虫来"},
	{"重做", "虫做"},
	{"重跑", "虫跑"},
}

// summarySegmentSeq 中间段合成序号——避免同毫秒同名（临时文件，拼接成功后删除）。
var summarySegmentSeq int64

// summaryAudioSink 是播报所需的音频服务窄面——Join 拼接 / Enqueue 入队（*bricks.AudioService 满足）。
type summaryAudioSink interface {
	Join(paths []string, outPath string) string
	Enqueue(key, path string, volume float64) string
}

// summarySpeaker 轮末总结播报执行体（组合根装配——持有 py / 音频服务与本机配置读面）。
type summarySpeaker struct {
	// py py 服务（kokoro 合成面；nil = 跳过并出声）
	py bricks.PyInvoker
	// audio 音频服务窄面（Join 拼接 / Enqueue 入队；nil = 跳过并出声）
	audio summaryAudioSink
	// cfgGet 全局配置读取（含 schema 默认兜底；nil = 一律空值）
	cfgGet func(key string) string
	// logf 日志出口（level, message；nil = 不出声）
	logf func(level int, message string)
	// baseDir 部署根（提示音 assets/audio/roundsummary_chime.wav 的基准；nil = exe 所在目录）
	baseDir func() string
}

// newSummarySpeaker 构造播报执行体。
func newSummarySpeaker(py bricks.PyInvoker, audio summaryAudioSink, cfgGet func(key string) string, logf func(level int, message string)) *summarySpeaker {
	return &summarySpeaker{py: py, audio: audio, cfgGet: cfgGet, logf: logf}
}

// Speak 合成并播放总结音频——后台执行（不阻塞主线程；失败只记日志、不影响轮次）。
//
// 实现 RoundSummarySpeaker（host/session 窄接口）。
func (sp *summarySpeaker) Speak(catKey, displayName, voice, text, audioName string) {
	if sp == nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				sp.log(3, "轮末总结播报异常: "+fmt.Sprint(r))
			}
		}()
		sp.runSummarySpeak(catKey, displayName, voice, text, audioName)
	}()
}

// runSummarySpeak 播报执行体——三段合成后拼接成单文件再入队（A265）。
func (sp *summarySpeaker) runSummarySpeak(catKey, displayName, voice, text, audioName string) {
	if sp.py == nil {
		sp.log(2, "轮末总结播报跳过——IPyService 未注入")
		return
	}
	if sp.audio == nil {
		sp.log(2, "轮末总结播报跳过——IAudioService 未注入")
		return
	}
	// [段0] 提示音——固定资产（钟声）；缺失由 resolveChimePath 记 WARN 后跳过
	segments := make([]string, 0, 3)
	temps := make([]string, 0, 2)
	if chime := sp.resolveChimePath(); chime != "" {
		segments = append(segments, chime)
	}
	// [段1] 播报名——全局默认音色（先告诉听众是谁在说）
	intro := ""
	if displayName != "" {
		intro = displayName + "说"
	}
	if intro != "" {
		if introFile := sp.speakOne(intro, sp.resolveGlobalVoice(), catKey); introFile != "" {
			segments = append(segments, introFile)
			temps = append(temps, introFile)
		}
	}
	// [段2] 正文——该猫外观音色
	if bodyFile := sp.speakOne(text, voice, catKey); bodyFile != "" {
		segments = append(segments, bodyFile)
		temps = append(temps, bodyFile)
	}
	if len(segments) == 0 {
		sp.log(2, "轮末总结播报跳过——无可播段")
		return
	}
	// [段3] 拼接成单文件——块 payload 与前端点播共用此文件（A265）
	dir := sp.resolveSummaryAudioDir()
	if dir == "" {
		sp.log(2, "轮末总结播报跳过——产物目录未配置（py.service.kokoro.out_dir）")
		return
	}
	joined := filepath.Join(dir, audioName)
	if joinErr := sp.audio.Join(segments, joined); joinErr != "" {
		// 拼接失败——回落逐段串行播放（可听性优先）；单文件资产此时不存在，前端点播 404 即如实不可用
		sp.log(2, "轮末总结拼接失败，回落逐段播放: "+joinErr)
		for _, segment := range segments {
			sp.audio.Enqueue(RoundSummaryAudioKey, segment, -1)
		}
		return
	}
	for _, temp := range temps {
		sp.deleteQuiet(temp)
	}
	if enqueue := sp.audio.Enqueue(RoundSummaryAudioKey, joined, -1); enqueue != "" {
		sp.log(2, "轮末总结播放入队失败: "+enqueue)
	}
}

// speakOne 单段合成——失败出声即返回空串（不影响后续段）；只产临时文件，播放与拼接归调用方。
func (sp *summarySpeaker) speakOne(text, voice, catKey string) string {
	if text == "" {
		return ""
	}
	dir := sp.resolveSummaryAudioDir()
	if dir == "" {
		sp.log(2, "轮末总结播报跳过——产物目录未配置（py.service.kokoro.out_dir）")
		return ""
	}
	outPath := buildSummarySegmentPath(dir)
	payload := map[string]any{
		"id":    "summary",
		"op":    "speak",
		"text":  normalizeForSpeech(text),
		"speed": 1.0,
		"out":   outPath,
	}
	if voice != "" {
		payload["voice"] = voice
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		sp.log(2, "轮末总结合成载荷构造失败: "+err.Error())
		return ""
	}
	reply := sp.py.Invoke("kokoro", string(payloadJSON))
	if reply == "" {
		sp.log(2, "轮末总结合成失败: (无回执) | cat="+catKey)
		return ""
	}
	if strings.HasPrefix(reply, "ERR|") {
		sp.log(2, "轮末总结合成失败: "+reply+" | cat="+catKey)
		return ""
	}
	var result struct {
		Ok  bool   `json:"ok"`
		Out string `json:"out"`
	}
	if err := json.Unmarshal([]byte(reply), &result); err != nil {
		sp.log(2, "轮末总结合成回执不可解析: "+reply)
		return ""
	}
	if !result.Ok {
		sp.log(2, "轮末总结合成失败: "+reply)
		return ""
	}
	produced := result.Out
	if produced == "" {
		produced = outPath
	}
	return produced
}

// resolveSummaryAudioDir 总结音频产物目录——配置 py.service.kokoro.out_dir（未配置返回空串，不猜目录；缺失则创建）。
func (sp *summarySpeaker) resolveSummaryAudioDir() string {
	dir := ""
	if sp.cfgGet != nil {
		dir = strings.TrimSpace(sp.cfgGet("py.service.kokoro.out_dir"))
	}
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		sp.log(2, "轮末总结产物目录不可用: "+err.Error()+" | "+dir)
		return ""
	}
	return dir
}

// resolveGlobalVoice 全局默认音色——audio.voice（读面已含 schema 默认）；空 = 服务默认（zf_001）。
func (sp *summarySpeaker) resolveGlobalVoice() string {
	if sp.cfgGet == nil {
		return ""
	}
	return sp.cfgGet("audio.voice")
}

// resolveChimePath 总结提示音路径——部署包 assets/audio/roundsummary_chime.wav；缺失记 WARN 并跳过（不阻断播报）。
func (sp *summarySpeaker) resolveChimePath() string {
	base := ""
	if sp.baseDir != nil {
		base = sp.baseDir()
	}
	if base == "" {
		if exe, err := os.Executable(); err == nil {
			base = filepath.Dir(exe)
		}
	}
	if base == "" {
		return ""
	}
	path := filepath.Join(base, "assets", "audio", "roundsummary_chime.wav")
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path
	}
	sp.log(2, "轮末总结提示音缺失（assets/audio/roundsummary_chime.wav）——跳过提示音")
	return ""
}

// deleteQuiet 静默删文件——中间段合成产物清理（失败只记日志，不影响已完成的拼接与播放）。
func (sp *summarySpeaker) deleteQuiet(path string) {
	if err := os.Remove(path); err != nil {
		sp.log(2, "轮末总结临时段清理失败: "+err.Error()+" | "+path)
	}
}

// log 出声出口（nil 安全）。
func (sp *summarySpeaker) log(level int, message string) {
	if sp.logf != nil {
		sp.logf(level, message)
	}
}

// buildSummarySegmentPath 单段合成产物路径——临时文件名（拼接成功后删除，不入资产面）。
func buildSummarySegmentPath(dir string) string {
	now := time.Now()
	seq := atomic.AddInt64(&summarySegmentSeq, 1)
	stamp := now.Format("20060102_150405") + "_" + fmt.Sprintf("%03d", now.Nanosecond()/int(time.Millisecond))
	return filepath.Join(dir, "tmp_summary_"+stamp+"_"+strconv.FormatInt(seq, 10)+".wav")
}

// normalizeForSpeech 播报文本归一——同音替换后点分版本号逐位转汉字
// （cn2an 会把 1.8.152 的尾段按数值读成「一百五十二」；只处理点分数字，数量类数字保持原样由 G2P 按量词读）。
func normalizeForSpeech(text string) string {
	if text == "" {
		return text
	}
	replaced := text
	for _, pair := range speechReplacements {
		replaced = strings.ReplaceAll(replaced, pair[0], pair[1])
	}
	return dottedNumberPattern.ReplaceAllStringFunc(replaced, func(value string) string {
		var sb strings.Builder
		for _, c := range value {
			switch {
			case c == '.':
				sb.WriteString("点")
			case c >= '0' && c <= '9':
				sb.WriteRune(digitHan[int(c-'0')])
			default:
				sb.WriteRune(c)
			}
		}
		return sb.String()
	})
}
