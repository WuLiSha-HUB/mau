package main

// 轮末总结播报执行体单测——三段链 / 拼接回落 / 失败面 / 文本归一（A247/A265）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSummaryPy 是 py 服务的测试替身——回显请求里的 out 路径，并记录每次 payload。
type fakeSummaryPy struct {
	// replies 依次返回的回执；耗尽后按回显模式应答
	replies []string
	// payloads 每次调用的载荷 JSON
	payloads []string
	// names 每次调用的服务名
	names []string
	// calls 调用计数
	calls int
}

// Invoke 记录调用并返回回执。
func (f *fakeSummaryPy) Invoke(name, payloadJSON string) string {
	f.names = append(f.names, name)
	f.payloads = append(f.payloads, payloadJSON)
	index := f.calls
	f.calls++
	if index < len(f.replies) {
		return f.replies[index]
	}
	var payload struct {
		Out string `json:"out"`
	}
	_ = json.Unmarshal([]byte(payloadJSON), &payload)
	reply, _ := json.Marshal(map[string]any{"ok": true, "out": payload.Out})
	return string(reply)
}

// fakeSummaryAudio 是音频服务的测试替身——记录 Join / Enqueue 调用。
type fakeSummaryAudio struct {
	// joinErr Join 的返回串（空 = 成功）
	joinErr string
	// joined 每次 Join 的源段列表
	joined [][]string
	// joinOut 每次 Join 的目标路径
	joinOut []string
	// queued 每次 Enqueue 的路径
	queued []string
	// queueKeys 每次 Enqueue 的键
	queueKeys []string
}

// Join 记录拼接调用。
func (f *fakeSummaryAudio) Join(paths []string, outPath string) string {
	f.joined = append(f.joined, append([]string(nil), paths...))
	f.joinOut = append(f.joinOut, outPath)
	return f.joinErr
}

// Enqueue 记录入队调用。
func (f *fakeSummaryAudio) Enqueue(key, path string, volume float64) string {
	f.queueKeys = append(f.queueKeys, key)
	f.queued = append(f.queued, path)
	return ""
}

// newTestSpeaker 构造带临时目录与提示音的播报执行体。
func newTestSpeaker(t *testing.T, py *fakeSummaryPy, audio *fakeSummaryAudio, withChime bool) (*summarySpeaker, string) {
	t.Helper()
	base := t.TempDir()
	if withChime {
		chimeDir := filepath.Join(base, "assets", "audio")
		if err := os.MkdirAll(chimeDir, 0o755); err != nil {
			t.Fatalf("建提示音目录失败: %v", err)
		}
		if err := os.WriteFile(filepath.Join(chimeDir, "roundsummary_chime.wav"), []byte("chime"), 0o644); err != nil {
			t.Fatalf("写提示音失败: %v", err)
		}
	}
	outDir := filepath.Join(base, "tts")
	speaker := newSummarySpeaker(py, audio, func(key string) string {
		switch key {
		case "py.service.kokoro.out_dir":
			return outDir
		case "audio.voice":
			return "zf_global"
		}
		return ""
	}, nil)
	speaker.baseDir = func() string { return base }
	return speaker, outDir
}

// TestSummarySpeakerFullChain 三段链——提示音 / 播报名 / 正文各一段，拼接后入队，临时段清理。
func TestSummarySpeakerFullChain(t *testing.T) {
	py := &fakeSummaryPy{}
	audio := &fakeSummaryAudio{}
	speaker, outDir := newTestSpeaker(t, py, audio, true)
	speaker.runSummarySpeak("cat", "小猫", "zf_body", "版本 1.8.152 重来", "summary_x.wav")

	if len(py.names) != 2 {
		t.Fatalf("合成调用应为两段（播报名 + 正文）: %+v", py.names)
	}
	for _, name := range py.names {
		if name != "kokoro" {
			t.Fatalf("合成服务名应为 kokoro: %s", name)
		}
	}
	var intro struct {
		ID    string  `json:"id"`
		Op    string  `json:"op"`
		Text  string  `json:"text"`
		Voice string  `json:"voice"`
		Speed float64 `json:"speed"`
		Out   string  `json:"out"`
	}
	if err := json.Unmarshal([]byte(py.payloads[0]), &intro); err != nil {
		t.Fatalf("播报名载荷不可解析: %v", err)
	}
	if intro.ID != "summary" || intro.Op != "speak" || intro.Text != "小猫说" || intro.Voice != "zf_global" || intro.Speed != 1.0 {
		t.Fatalf("播报名载荷不符: %+v", intro)
	}
	var body struct {
		Text  string `json:"text"`
		Voice string `json:"voice"`
	}
	if err := json.Unmarshal([]byte(py.payloads[1]), &body); err != nil {
		t.Fatalf("正文载荷不可解析: %v", err)
	}
	if body.Text != "版本 一点八点一五二 虫来" || body.Voice != "zf_body" {
		t.Fatalf("正文载荷不符: %+v", body)
	}

	if len(audio.joined) != 1 {
		t.Fatalf("Join 应调用一次: %+v", audio.joined)
	}
	if len(audio.joined[0]) != 3 {
		t.Fatalf("三段应全部进拼接: %+v", audio.joined[0])
	}
	if !strings.HasSuffix(audio.joined[0][0], "roundsummary_chime.wav") {
		t.Fatalf("首段应为提示音: %s", audio.joined[0][0])
	}
	if audio.joinOut[0] != filepath.Join(outDir, "summary_x.wav") {
		t.Fatalf("拼接目标不符: %s", audio.joinOut[0])
	}
	if len(audio.queued) != 1 || audio.queued[0] != audio.joinOut[0] || audio.queueKeys[0] != RoundSummaryAudioKey {
		t.Fatalf("入队不符: %+v / %+v", audio.queueKeys, audio.queued)
	}
	// 临时段已清理（播报名 + 正文两段）
	if _, err := os.Stat(audio.joined[0][1]); err == nil {
		t.Fatalf("临时段应已删除: %s", audio.joined[0][1])
	}
}

// TestSummarySpeakerJoinFallback 拼接失败——回落逐段串行播放（单文件资产不承诺）。
func TestSummarySpeakerJoinFallback(t *testing.T) {
	py := &fakeSummaryPy{}
	audio := &fakeSummaryAudio{joinErr: "ERR|JOIN_FAIL|拼接失败: 坏源"}
	speaker, _ := newTestSpeaker(t, py, audio, true)
	speaker.runSummarySpeak("cat", "小猫", "", "正文", "summary_y.wav")
	if len(audio.joined) != 1 {
		t.Fatalf("Join 应尝试一次: %+v", audio.joined)
	}
	if len(audio.queued) != 3 {
		t.Fatalf("回落应逐段入队三段: %+v", audio.queued)
	}
	for _, key := range audio.queueKeys {
		if key != RoundSummaryAudioKey {
			t.Fatalf("回落入队键不符: %s", key)
		}
	}
}

// TestSummarySpeakerSkipBranches 跳过分支——py / audio 未注入、产物目录未配置、无可播段、提示音缺失。
func TestSummarySpeakerSkipBranches(t *testing.T) {
	var logs []string
	record := func(level int, message string) { logs = append(logs, message) }
	hasLog := func(fragment string) bool {
		for _, line := range logs {
			if strings.Contains(line, fragment) {
				return true
			}
		}
		return false
	}

	// py 未注入
	speaker := newSummarySpeaker(nil, &fakeSummaryAudio{}, nil, record)
	speaker.runSummarySpeak("cat", "小猫", "", "正文", "a.wav")
	if !hasLog("IPyService 未注入") {
		t.Fatalf("py 未注入应出声: %+v", logs)
	}

	// audio 未注入
	logs = nil
	speaker = newSummarySpeaker(&fakeSummaryPy{}, nil, nil, record)
	speaker.runSummarySpeak("cat", "小猫", "", "正文", "a.wav")
	if !hasLog("IAudioService 未注入") {
		t.Fatalf("audio 未注入应出声: %+v", logs)
	}

	// 产物目录未配置——合成直接跳过，无可播段
	logs = nil
	py := &fakeSummaryPy{}
	audio := &fakeSummaryAudio{}
	speaker = newSummarySpeaker(py, audio, func(key string) string { return "" }, record)
	speaker.baseDir = func() string { return t.TempDir() }
	speaker.runSummarySpeak("cat", "小猫", "", "正文", "a.wav")
	if !hasLog("产物目录未配置") || !hasLog("无可播段") {
		t.Fatalf("目录未配置应出声: %+v", logs)
	}
	if len(py.names) != 0 || len(audio.joined) != 0 {
		t.Fatalf("目录未配置不应合成 / 拼接: %+v / %+v", py.names, audio.joined)
	}

	// 提示音缺失——只记 WARN，仍用两段拼接
	logs = nil
	py2 := &fakeSummaryPy{}
	audio2 := &fakeSummaryAudio{}
	speaker2, _ := newTestSpeaker(t, py2, audio2, false)
	speaker2.logf = record
	speaker2.runSummarySpeak("cat", "", "zf_body", "正文", "b.wav")
	if !hasLog("提示音缺失") {
		t.Fatalf("提示音缺失应出声: %+v", logs)
	}
	if len(audio2.joined) != 1 || len(audio2.joined[0]) != 1 {
		t.Fatalf("无播报名时仅正文一段: %+v", audio2.joined)
	}
}

// TestSummarySpeakerSynthFailure 合成失败面——ERR / 空回执 / 不可解析 / ok=false 均返回空串。
func TestSummarySpeakerSynthFailure(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		want  string
	}{
		{"ERR 回执", "ERR|PY_ERR|服务异常", "轮末总结合成失败: ERR|PY_ERR|服务异常"},
		{"空回执", "", "轮末总结合成失败: (无回执)"},
		{"不可解析", "not-json", "轮末总结合成回执不可解析: not-json"},
		{"ok=false", `{"ok":false,"error":"合成炸了"}`, "轮末总结合成失败: "},
	}
	for _, c := range cases {
		var logs []string
		py := &fakeSummaryPy{replies: []string{c.reply}}
		audio := &fakeSummaryAudio{}
		speaker, _ := newTestSpeaker(t, py, audio, false)
		speaker.logf = func(level int, message string) { logs = append(logs, message) }
		if got := speaker.speakOne("正文", "", "cat"); got != "" {
			t.Fatalf("%s: 失败应返回空串: %s", c.name, got)
		}
		found := false
		for _, line := range logs {
			if strings.HasPrefix(line, c.want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: 日志不符: %+v", c.name, logs)
		}
	}
}

// TestNormalizeForSpeech 文本归一——点分版本号逐位转汉字、同音替换、普通数字不动。
func TestNormalizeForSpeech(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"版本 1.8.152 已就绪", "版本 一点八点一五二 已就绪"},
		{"重来一次，重做，重跑", "虫来一次，虫做，虫跑"},
		{"3 个文件，端口 8080", "3 个文件，端口 8080"},
		{"无数字", "无数字"},
		{"", ""},
	}
	for _, c := range cases {
		if got := normalizeForSpeech(c.in); got != c.want {
			t.Fatalf("归一不符: %q → %q want %q", c.in, got, c.want)
		}
	}
}

// TestBuildSummarySegmentPath 临时段路径——目录内、tmp_summary_ 前缀、序号递增不重名。
func TestBuildSummarySegmentPath(t *testing.T) {
	dir := t.TempDir()
	first := buildSummarySegmentPath(dir)
	second := buildSummarySegmentPath(dir)
	if filepath.Dir(first) != dir || filepath.Dir(second) != dir {
		t.Fatalf("目录不符: %s / %s", first, second)
	}
	if !strings.HasPrefix(filepath.Base(first), "tmp_summary_") || !strings.HasSuffix(first, ".wav") {
		t.Fatalf("命名不符: %s", first)
	}
	if first == second {
		t.Fatalf("同毫秒序号应保证不重名: %s", first)
	}
}
