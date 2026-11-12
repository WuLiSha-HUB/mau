package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeStreamer 按脚本逐条回放事件（每次调用取下一份脚本）。
type fakeStreamer struct {
	// scripts 每次请求的事件序列
	scripts [][]StreamEvent
	// calls 已消费脚本数
	calls int
}

// Stream 实现 Streamer。
func (f *fakeStreamer) Stream(ctx context.Context, messages []Message, tools []ToolSpec, userID string, emit func(StreamEvent)) {
	if f.calls >= len(f.scripts) {
		emit(StreamEvent{Kind: StreamError, Text: "ERR|FAKE_EXHAUSTED|脚本已用尽"})
		return
	}
	script := f.scripts[f.calls]
	f.calls++
	for _, ev := range script {
		emit(ev)
	}
}

// newTestSession 建立隔离的测试会话（临时目录 + 不落 pending）。
func newRoundTestSession(t *testing.T) (*Session, string) {
	t.Helper()
	ConfigurePendingStore("")
	dir := t.TempDir()
	SessionMetaPathProvider = func(catID string) string {
		return filepath.Join(dir, catID+".session.json")
	}
	t.Cleanup(func() { SessionMetaPathProvider = nil })
	store := NewSessionStore(filepath.Join(dir, "cat.jsonl"))
	ctx := NewContext()
	ctx.SetSystemPrompt("SYS")
	views := NewViewStore(filepath.Join(dir, "cat.view.jsonl"))
	sess := NewSession("cat", ctx, store, views)
	sess.SetAuthorizer(func(name string) bool { return true })
	sess.SetBuiltinExecutor(func(name string) bool { return name == "time" || name == "Note" || name == "random" }, sess.ExecuteBuiltin)
	return sess, dir
}

// pumpUntilIdle 推进到轮次结束（首次离开 Idle 后再回 Idle）。
func pumpUntilIdle(t *testing.T, sess *Session) {
	t.Helper()
	left := false
	for i := 0; i < 500; i++ {
		sess.Pump()
		time.Sleep(5 * time.Millisecond)
		phase := sess.PhaseText()
		if phase != "Idle" {
			left = true
			continue
		}
		if left {
			return
		}
	}
	t.Fatalf("pump 未收敛（phase=%s）", sess.PhaseText())
}

// TestRound_TextReply 纯文本轮——前文追加 + roundsum 载荷 + session.json 结算字段。
func TestRound_TextReply(t *testing.T) {
	sess, dir := newRoundTestSession(t)
	sess.SetStreamer(&fakeStreamer{scripts: [][]StreamEvent{{
		{Kind: StreamReasoning, Text: "想"},
		{Kind: StreamText, Text: "你好"},
		{Kind: StreamFinish, Text: "stop"},
		{Kind: StreamUsage, Text: `{"prompt":10,"completion":3,"cacheHit":4}`},
		{Kind: StreamDone},
	}}})
	if !sess.PostUser("hi", "user", "", "") {
		t.Fatal("用户消息未受理")
	}
	pumpUntilIdle(t, sess)

	msgs := sess.Context.Messages()
	last := msgs[len(msgs)-1]
	if last.Role != RoleAssistant || last.Content != "你好" {
		t.Fatalf("末条消息不符: %+v", last)
	}
	// 视图落盘——roundsum 块在
	raw, err := os.ReadFile(filepath.Join(dir, "cat.view.jsonl"))
	if err != nil {
		t.Fatalf("视图文件缺失: %v", err)
	}
	if !strings.Contains(string(raw), `"RenderType":"roundsum"`) {
		t.Fatalf("roundsum 块未落盘: %s", string(raw))
	}
	// session.json 结算字段
	metaRaw, err := os.ReadFile(filepath.Join(dir, "cat.session.json"))
	if err != nil {
		t.Fatalf("元数据文件缺失: %v", err)
	}
	var meta SessionMeta
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		t.Fatalf("元数据解析失败: %v", err)
	}
	if meta.RoundTokens.Prompt != 10 || meta.RoundTokens.Completion != 3 || meta.RoundTokens.CacheHit != 4 {
		t.Fatalf("RoundTokens 不符: %+v", meta.RoundTokens)
	}
	if meta.RoundRequests != 1 || meta.RoundTools != 0 {
		t.Fatalf("RoundRequests/RoundTools 不符: %d/%d", meta.RoundRequests, meta.RoundTools)
	}
	if meta.RoundElapsedMs < 0 {
		t.Fatalf("RoundElapsedMs 非法: %d", meta.RoundElapsedMs)
	}
	if meta.RoundPhases.Link < 0 || meta.RoundPhases.Reply < 0 {
		t.Fatalf("RoundPhases 非法: %+v", meta.RoundPhases)
	}
}

// TestRound_ToolBatch 工具批——分批计划 / 声明序回填 / 被拒工具不进批 / 计数口径。
func TestRound_ToolBatch(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	sess.SetAuthorizer(func(name string) bool { return name != "dbAdmin-drop" })
	toolCalls := `[` +
		`{"id":"1","type":"function","function":{"name":"cs-build","arguments":"{\"path\":\"a\"}"}},` +
		`{"id":"2","type":"function","function":{"name":"time","arguments":"{}"}},` +
		`{"id":"3","type":"function","function":{"name":"cs-format","arguments":"{}"}},` +
		`{"id":"4","type":"function","function":{"name":"text-read","arguments":"{}"}},` +
		`{"id":"5","type":"function","function":{"name":"dbAdmin-drop","arguments":"{}"}},` +
		`{"id":"6","type":"function","function":{"name":"powershell","arguments":"{}"}}` +
		`]`
	sess.SetStreamer(&fakeStreamer{scripts: [][]StreamEvent{
		{
			{Kind: StreamToolCalls, Text: toolCalls},
			{Kind: StreamUsage, Text: `{"prompt":20,"completion":5,"cacheHit":0}`},
			{Kind: StreamDone},
		},
		{
			{Kind: StreamText, Text: "完成"},
			{Kind: StreamDone},
		},
	}})
	if !sess.PostUser("跑一下", "user", "", "") {
		t.Fatal("用户消息未受理")
	}
	pumpUntilIdle(t, sess)

	r := sess.roundState()
	r.mu.Lock()
	batches := r.batches
	toolCount := r.toolCallCount
	dogs := append([]*toolDog(nil), r.dogs...)
	r.mu.Unlock()

	// 分批规则：-1 只读批 [time,text-read] → 独占各批 [cs-build] [cs-format] → 构建批 [powershell]
	if len(batches) != 4 {
		t.Fatalf("批数不符: %d", len(batches))
	}
	if namesOf(batches[0]) != "time,text-read" {
		t.Fatalf("首批不符: %s", namesOf(batches[0]))
	}
	if namesOf(batches[1]) != "cs-build" || namesOf(batches[2]) != "cs-format" || namesOf(batches[3]) != "powershell" {
		t.Fatalf("独占/构建批不符: %s | %s | %s", namesOf(batches[1]), namesOf(batches[2]), namesOf(batches[3]))
	}
	if toolCount != 5 {
		t.Fatalf("工具计数应排除被拒工具: %d", toolCount)
	}
	if len(dogs) != 6 {
		t.Fatalf("工具单数不符: %d", len(dogs))
	}
	// 被拒工具不进批且回执逐字
	if !dogs[4].IsClosed || dogs[4].Result != "ERR|TOOL_FORBIDDEN|工具不在当前授权面: dbAdmin-drop" {
		t.Fatalf("被拒工具回执不符: %+v", dogs[4])
	}
	if !containsDog(batches[0], dogs[1]) || !containsDog(batches[0], dogs[3]) {
		t.Fatalf("只读批成员不符")
	}
	// time 内置直执成功
	if !strings.HasPrefix(dogs[1].Result, `{"ok":true,"tool":"time"`) {
		t.Fatalf("time 回执不符: %s", dogs[1].Result)
	}
	// 非内置无执行体 → 诚实 ERR
	if !strings.HasPrefix(dogs[3].Result, "ERR|OA_POST_FAIL|工单提交失败（OA 不可用）: text-read") {
		t.Fatalf("text-read 回执不符: %s", dogs[3].Result)
	}
	// 结果回填按 LLM 声明序
	var toolIDs []string
	for _, msg := range sess.Context.Messages() {
		if msg.Role == RoleTool {
			toolIDs = append(toolIDs, msg.ToolCallID)
		}
	}
	if strings.Join(toolIDs, ",") != "1,2,3,4,5,6" {
		t.Fatalf("结果回填序不符: %v", toolIDs)
	}
}

// TestEnterToolBatch_TimebackGates timeback 三段门禁——回执逐字且不进批。
func TestEnterToolBatch_TimebackGates(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	sess.Timeback.Start(TimebackCodeReview, "审查")
	sess.enterToolBatch(`[` +
		`{"id":"1","type":"function","function":{"name":"image-inject","arguments":"{}"}},` +
		`{"id":"2","type":"function","function":{"name":"restart-full","arguments":"{}"}},` +
		`{"id":"3","type":"function","function":{"name":"timeback-start","arguments":"{}"}},` +
		`{"id":"4","type":"function","function":{"name":"timeback-start","arguments":"{}"}}` +
		`]`)
	r := sess.roundState()
	r.mu.Lock()
	dogs := append([]*toolDog(nil), r.dogs...)
	batches := len(r.batches)
	r.mu.Unlock()
	if batches != 0 {
		t.Fatalf("全拒批不应有批次: %d", batches)
	}
	if !strings.HasPrefix(dogs[0].Result, "ERR|TIMEBACK_PROFILE|image-inject 不在本域类型「code_review」的白名单内") {
		t.Fatalf("域类型门禁回执: %s", dogs[0].Result)
	}
	if !strings.HasPrefix(dogs[1].Result, "ERR|TIMEBACK_LOCKED|timeback 作用域内禁止会让进程或作用域当场失效的操作") {
		t.Fatalf("暴毙黑名单回执: %s", dogs[1].Result)
	}
	if !strings.Contains(dogs[3].Result, "批内不允许多条 timeback-start / timeback-back 调用（各至多 × 1）——第 4 条被拒") {
		t.Fatalf("批内重复回执: %s", dogs[3].Result)
	}
}

// TestEnterToolBatch_TimebackRequired 域外专属工具——作用域外拒绝。
func TestEnterToolBatch_TimebackRequired(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	sess.enterToolBatch(`[{"id":"1","type":"function","function":{"name":"browser-open","arguments":"{}"}}]`)
	r := sess.roundState()
	r.mu.Lock()
	dog := r.dogs[0]
	r.mu.Unlock()
	want := "ERR|TIMEBACK_REQUIRED|browser-open 仅在 timeback 作用域内可用（非主干信息——用完回收）——先 start 开锚"
	if dog.Result != want {
		t.Fatalf("域外回执不符:\n got: %s\nwant: %s", dog.Result, want)
	}
}

// TestBuildRoundSumJSON_Golden roundsum 载荷逐字——字段与 C# 同构。
func TestBuildRoundSumJSON_Golden(t *testing.T) {
	sess, _ := newRoundTestSession(t)
	r := sess.roundState()
	r.mu.Lock()
	r.usagePrompt = 100
	r.usageCacheHit = 40
	r.usageCompletion = 7
	r.toolCallCount = 3
	r.toolDone = true
	r.phaseAccum = [PhaseCount]int64{0, 11, 22, 33, 44, 55, 66}
	r.phaseKind = -1
	r.roundStartedAt = time.Now().Add(-2 * time.Second)
	r.mu.Unlock()
	payload := sess.BuildRoundSumJSON()
	if !strings.HasPrefix(payload, `{"type":"roundsum","data":{"prompt":100,"completion":7,"cacheHit":40,"miss":60,"done":"tool","toolCount":3,"requests":0,"elapsedMs":`) {
		t.Fatalf("roundsum 头不符: %s", payload)
	}
	if !strings.Contains(payload, `,"phases":{"wait":11,"link":22,"think":33,"tool":44,"run":55,"reply":66}}}`) {
		t.Fatalf("roundsum phases 不符: %s", payload)
	}
}

// namesOf 批次成员名串。
func namesOf(batch []*toolDog) string {
	names := make([]string, 0, len(batch))
	for _, dog := range batch {
		names = append(names, dog.Name)
	}
	return strings.Join(names, ",")
}
