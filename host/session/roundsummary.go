package session

import (
	"strings"
	"sync"
)

// 本文件复现 C# 轮末总结服务——`ChatSession.PostRoundSummary` / `PumpRoundSummary` /
// `BuildRoundSummaryContent` / `BuildRoundSummaryPromptHead`（`mau:CatHome4/CatHome4.Core/ChatSession.cs:3722-3834`）。
//
// 链路：轮末 closeRound（roundsum 落盘之后）→ PostRoundSummary（投 SVC/LLMSimpleRequest 工单）
// → Pump 每帧 pumpRoundSummary 回收 → AppendRoundSummaryNote 落视图块（ts = roundsum ts + 1）。
// 对端：语料 `mau:corpus/ch4/LLMSimpleRequest/llm_simple_request.mau`（五键载荷 → 回执 result）。
//
// 边界：总结自动出声（C# A247/A265 SpeakRoundSummary）未接线——音频面（audio.Join / audio.Enqueue）未落，
// audioName 恒空 = 块 payload 不写 audio 字段（不承诺前端点播）。

const (
	// RoundSummaryOwnerID 轮末总结工单挂单方 ID——独立于工具工单（owner=1），避免 RemoveByOwner 互扰。
	RoundSummaryOwnerID int64 = 2
	// RoundSummaryTimeoutFrames 总结工单超时帧上限（2400 帧 = 240 秒，100ms/帧——一次性 LLM 调用）。
	RoundSummaryTimeoutFrames int64 = 2400
	// RoundSummaryPromptTail 后段提示词——拼在待总结内容之后（固定分隔符，界定待总结范围）。
	RoundSummaryPromptTail = "\n——以上是本轮的全部往来（用户输入与你自己的回复按序合并）。请用第一人称向用户交代这一轮你做了什么、结果如何。"
)

// RoundSummaryOffice 是轮末总结工单面（组合根适配——会话层不依赖运行时实现）。
type RoundSummaryOffice interface {
	// Post 上架总结工单，返回工单号（0 = 未受理）。
	Post(officeName string, timeoutFrames int64) int64
	// SetStr 写入工单载荷。
	SetStr(officeID int64, key, value string) bool
	// State 读工单观测态（0=Open 1=Work 2=Closed 3=Timeout）。
	State(officeID int64) ToolOfficeState
	// RemoveByOwner 回收本挂单方全部工单（C# OA.RemoveByOwner 同语义）。
	RemoveByOwner()
}

// roundSummaryState 轮末总结服务的会话态——开关 / 在途工单 / 本轮缓冲。
type roundSummaryState struct {
	// mu 保护本组字段（Pump 主线程与注入线程并发访问）
	mu sync.Mutex
	// enabled 开关（外观池条目 roundSummary——catcfg.apply 即时应用）
	enabled bool
	// office 工单面（装配期注入；nil = 未接线）
	office RoundSummaryOffice
	// llm 配置提供者——返回 [endpoint, apikey, model]；nil / 短数组 = 本轮不出总结
	llm func() []string
	// officeID 在途工单（0 = 无）
	officeID int64
	// pendingTs 总结块时间戳（= roundsum ts + 1——固定插在轮末统计之后）
	pendingTs int64
	// userBuf 本轮 user 文本缓冲（全部 user 按序；sys 注入不计入）
	userBuf strings.Builder
	// textBuf 本轮 text 缓冲（全部 text 按序：工具轮间隙文本与最终回复同收）
	textBuf strings.Builder
}

// SetRoundSummary 设置轮末总结开关——catcfg.apply 即时应用（值源 = 外观配置池条目）。
func (s *Session) SetRoundSummary(value bool) {
	rs := &s.roundSummary
	rs.mu.Lock()
	rs.enabled = value
	rs.mu.Unlock()
}

// RoundSummaryEnabled 读轮末总结开关现值。
func (s *Session) RoundSummaryEnabled() bool {
	rs := &s.roundSummary
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.enabled
}

// SetRoundSummaryOffice 注入总结工单面（组合根构造会话时调用；nil = 不出总结）。
func (s *Session) SetRoundSummaryOffice(office RoundSummaryOffice) {
	rs := &s.roundSummary
	rs.mu.Lock()
	rs.office = office
	rs.mu.Unlock()
}

// SetRoundSummaryLlm 注入总结 LLM 配置提供者——返回 [endpoint, apikey, model]。
//
// 未接线 / 返回空数组 → 本轮不出总结（不静默放行、不误报成功——与 C# RoundSummaryLlmProvider 同语义）。
func (s *Session) SetRoundSummaryLlm(fn func() []string) {
	rs := &s.roundSummary
	rs.mu.Lock()
	rs.llm = fn
	rs.mu.Unlock()
}

// appendRoundSummaryUser 本轮 user 文本入缓冲（sys 注入不计入——调用方按 isSys 分派；空文本不落）。
func (s *Session) appendRoundSummaryUser(text string) {
	if text == "" {
		return
	}
	rs := &s.roundSummary
	rs.mu.Lock()
	rs.userBuf.WriteString(text)
	rs.userBuf.WriteString("\n")
	rs.mu.Unlock()
}

// appendRoundSummaryText 本轮 text 入缓冲（空文本不落——C# 同判据）。
func (s *Session) appendRoundSummaryText(text string) {
	if text == "" {
		return
	}
	rs := &s.roundSummary
	rs.mu.Lock()
	rs.textBuf.WriteString(text)
	rs.textBuf.WriteString("\n")
	rs.mu.Unlock()
}

// resetRoundSummaryBuffers 轮首清空缓冲——本轮独立累积（与 C# ResetRoundCounters 同点）。
func (s *Session) resetRoundSummaryBuffers() {
	rs := &s.roundSummary
	rs.mu.Lock()
	rs.userBuf.Reset()
	rs.textBuf.Reset()
	rs.mu.Unlock()
}

// BuildRoundSummaryContent 本轮待总结内容——user 缓冲 + text 缓冲；无内容返回空串，否则追加后段提示词。
func (s *Session) BuildRoundSummaryContent() string {
	rs := &s.roundSummary
	rs.mu.Lock()
	body := rs.userBuf.String() + rs.textBuf.String()
	rs.mu.Unlock()
	if body == "" {
		return ""
	}
	return body + RoundSummaryPromptTail
}

// BuildRoundSummaryPromptHead 前段提示词——按猫身份生成的第一人称视角（向用户交代，不做第三方总结）。
func (s *Session) BuildRoundSummaryPromptHead() string {
	name := s.DisplayName
	if name == "" {
		name = s.CatKey
	}
	return "你是「" + name + "」。这一轮你刚做完事，现在向用户亲口交代你做了什么、结果如何。" +
		"第一人称（我 / 我这边），口语，像当面说话；只说自己的动作与结果，不复述用户的要求，" +
		"不用「用户」「助手」「该轮」这类旁观措辞；一句话 20-50 字；不要客套、不要分点、不要引号。" +
		"这句话会被直接念出来，数字与缩写按读音写：版本号、编号、端口号逐位写成汉字（1.8.152 写成 一点八点一五二，8080 写成 八零八零），" +
		"只有表示数量时才用阿拉伯数字（3 个文件）；技术缩写拆成大写单字母加空格（CUDA 写成 C U D A）。"
}

// PostRoundSummary 轮末调度：组 prompt 并投 LLMSimpleRequest 服务工单。
//
// 开关开 + 配置可用 + 有内容时才发；工单 ID 记入会话态，由 Pump 每帧轮询回收（谁发谁收）。
func (s *Session) PostRoundSummary(insertTs int64) {
	rs := &s.roundSummary
	rs.mu.Lock()
	rs.officeID = 0
	rs.pendingTs = insertTs
	enabled := rs.enabled
	office := rs.office
	llmFn := rs.llm
	rs.mu.Unlock()
	if !enabled || office == nil {
		return
	}
	var llm []string
	if llmFn != nil {
		llm = llmFn()
	}
	if len(llm) < 3 || llm[0] == "" || llm[1] == "" || llm[2] == "" {
		return
	}
	content := s.BuildRoundSummaryContent()
	if content == "" {
		return
	}
	officeID := office.Post("LLMSimpleRequest", RoundSummaryTimeoutFrames)
	if officeID <= 0 {
		logAt(LogModuleHost, 2, "轮末总结发单失败（OA.Post 未受理）: cat="+s.CatKey, LogCategoryChat)
		return
	}
	office.SetStr(officeID, "endpoint", llm[0])
	office.SetStr(officeID, "apikey", llm[1])
	office.SetStr(officeID, "model", llm[2])
	office.SetStr(officeID, "system", s.BuildRoundSummaryPromptHead())
	office.SetStr(officeID, "content", content)
	rs.mu.Lock()
	rs.officeID = officeID
	rs.mu.Unlock()
}

// pumpRoundSummary 在途工单回收（每帧）——闭合即取回执注入总结块；超时亦出声（失败可见）。
func (s *Session) pumpRoundSummary() {
	rs := &s.roundSummary
	rs.mu.Lock()
	officeID := rs.officeID
	office := rs.office
	pendingTs := rs.pendingTs
	rs.mu.Unlock()
	if officeID <= 0 || office == nil {
		return
	}
	state := office.State(officeID)
	// 2=Closed 3=Timeout——其余态继续等（C# 判据同序）
	if state.Status != 2 && state.Status != 3 {
		return
	}
	rs.mu.Lock()
	rs.officeID = 0
	rs.mu.Unlock()
	result := ""
	if state.HasResult {
		result = state.Result
	}
	office.RemoveByOwner()
	if state.Status == 3 {
		result = "ERR|TIMEOUT|轮末总结超时"
	}
	if result == "" {
		result = "ERR|EMPTY|轮末总结无可读回执"
	}
	ts := pendingTs
	if ts <= 0 {
		ts = s.ViewTimestamp()
	}
	// audioName 恒空——总结自动出声未接线（音频面未落；不写 audio 字段即不承诺点播）
	if s.Views != nil {
		s.Views.AppendRoundSummaryNote(result, ts, "")
	}
}
