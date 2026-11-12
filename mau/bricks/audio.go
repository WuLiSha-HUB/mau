package bricks

// AUDIO 两件积木——AudioCat 工具执行面（对齐 C# BRIK-AUDIO-001 / 002）。
//
// 语义：
//   - audio.play：参数面零容忍 → 播放服务入队（立即返回；调度在服务内）；
//     成功回执 "OK|已入队"；服务层 ERR| 文本按工具失败返回。
//   - audio.speak：text 必填 → 调 py 服务合成（kokoro）→ 取产物路径 → 入队播放；
//     成功回执 "OK|<产物路径>"。py 服务未注入按 "ERR|PY_NO_SERVICE|宿主未注入 IPyService"
//     （C# 的 nil 分支同文案）；服务层 ERR| 文本按工具失败返回。

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
)

// PyInvoker 是 py 服务调用面（C# IPyService.Invoke 的等价物）——nil = 未注入。
type PyInvoker interface {
	// Invoke 调 py 服务：name=服务名（kokoro），payloadJSON=载荷 JSON 字符串；返回回执 JSON 或 "ERR|" 文本。
	Invoke(name, payloadJSON string) string
}

// AudioTTSConfig 是 audio.speak 的宿主依赖面（nil 字段按「未配置」口径兜底）。
type AudioTTSConfig struct {
	// Voice 读默认音色（audio.voice，含 schema 默认兜底）
	Voice func() string
	// OutDir 读 TTS 产物目录（py.service.kokoro.out_dir；空 = 系统临时目录）
	OutDir func() string
	// Resolve 根寻址展开（out 参数；失败原样返回）
	Resolve func(string) string
	// Py py 服务（nil = 未注入——audio.speak 报 PY_NO_SERVICE）
	Py PyInvoker
}

// AudioPlay 执行 audio.play 工具——播放本地 WAV（入队即返回）。
func AudioPlay(service *AudioService, argsJSON string) (string, bool) {
	if badArgs := Validate(argsJSON, "key path volume", "path", "", ""); badArgs != "" {
		return badArgs, false
	}
	path := Get(argsJSON, "path")
	if path == "" {
		return "ERR|BAD_ARGS|缺少参数 path", false
	}
	key := Get(argsJSON, "key")
	if key == ParseFail {
		key = ""
	}
	// 音量——未指定（-1）交由服务读全局配置 audio.volume（design-mau-audio §五）
	volume := -1.0
	if volText := Get(argsJSON, "volume"); volText != "" && volText != ParseFail {
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(volText), 64); err == nil {
			volume = parsed
		}
	}
	if service == nil {
		return "ERR|AUDIO_NO_SERVICE|宿主未注入 IAudioService", false
	}
	if reply := service.Enqueue(key, path, volume); reply != "" {
		return reply, false
	}
	return "OK|已入队", true
}

// AudioSpeak 执行 audio.speak 工具——文本转语音并播放（合成同步往返，播放入队即返回）。
func AudioSpeak(cfg AudioTTSConfig, service *AudioService, argsJSON string) (string, bool) {
	if badArgs := Validate(argsJSON, "text voice speed key out", "text", "", ""); badArgs != "" {
		return badArgs, false
	}
	text := Get(argsJSON, "text")
	if text == "" || text == ParseFail {
		return "ERR|BAD_ARGS|缺少参数 text", false
	}
	voice := cleanAudioArg(Get(argsJSON, "voice"))
	if voice == "" && cfg.Voice != nil {
		voice = cleanAudioArg(cfg.Voice())
	}
	key := cleanAudioArg(Get(argsJSON, "key"))
	outArg := cleanAudioArg(Get(argsJSON, "out"))
	speed := 1.0
	if raw := Get(argsJSON, "speed"); raw != "" && raw != ParseFail {
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil && parsed > 0 {
			speed = parsed
		}
	}
	// 产物路径——显式 out 按根寻址展开；缺省落 out_dir / 系统临时目录
	outPath := ""
	if outArg != "" {
		outPath = outArg
		if cfg.Resolve != nil {
			if expanded := cfg.Resolve(outArg); expanded != "" {
				outPath = expanded
			}
		}
	} else {
		outPath = buildTTSOutPath(cfg.OutDir)
	}

	// [段1] 合成——py 服务同步往返（C# 的 nil 分支先在服务解析处报缺）
	if cfg.Py == nil {
		return "ERR|PY_NO_SERVICE|宿主未注入 IPyService", false
	}
	payload := map[string]any{"id": "speak", "op": "speak", "text": text, "speed": speed, "out": outPath}
	if voice != "" {
		payload["voice"] = voice
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "ERR|TTS_FAIL|载荷构造失败: " + err.Error(), false
	}
	reply := cfg.Py.Invoke("kokoro", string(payloadJSON))
	if reply == "" {
		return "ERR|TTS_FAIL|Kokoro 服务无回执", false
	}
	if strings.HasPrefix(reply, "ERR|") {
		return reply, false
	}

	// [段2] 回执解析——ok / out / error
	var res struct {
		Ok    bool   `json:"ok"`
		Out   string `json:"out"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(reply), &res); err != nil {
		return "ERR|TTS_FAIL|Kokoro 回执不可解析: " + reply, false
	}
	if !res.Ok {
		errText := res.Error
		if errText == "" {
			errText = reply
		}
		return "ERR|TTS_FAIL|" + errText, false
	}
	produced := res.Out
	if produced == "" {
		produced = outPath
	}
	if produced == "" {
		return "ERR|TTS_FAIL|产物不存在: " + produced, false
	}
	if _, statErr := os.Stat(produced); statErr != nil {
		return "ERR|TTS_FAIL|产物不存在: " + produced, false
	}

	// [段3] 播放——入队即返回
	if service == nil {
		return "ERR|AUDIO_NO_SERVICE|宿主未注入 IAudioService", false
	}
	if enqueue := service.Enqueue(key, produced, -1); enqueue != "" {
		return enqueue, false
	}
	return "OK|" + produced, true
}

// cleanAudioArg 归一取值——解析失败标记 / 空转空串。
func cleanAudioArg(value string) string {
	if value == ParseFail {
		return ""
	}
	return value
}
