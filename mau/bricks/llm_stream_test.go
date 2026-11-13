package bricks

import (
	"strings"
	"testing"
	"time"

	"cathome5/mau/runtime"
)

// streamStep 是脚本化流式事件的一步。
type streamStep struct {
	kind LLMStreamKind
	text string
}

// scriptedStreamRuntime 按脚本回放事件——记录收到的消息参数（消费在后台 goroutine，字段读取靠 db 锁同步）。
type scriptedStreamRuntime struct {
	events     []streamStep
	calls      int
	gotSystem  string
	gotContent string
	gotSession string
}

// ChatStream 回放脚本后返回（模拟实现方收口）。
func (s *scriptedStreamRuntime) ChatStream(system, content, sessionID string, onEvent func(kind LLMStreamKind, text string)) {
	s.calls++
	s.gotSystem = system
	s.gotContent = content
	s.gotSession = sessionID
	for _, step := range s.events {
		onEvent(step.kind, step.text)
	}
}

// gateStreamRuntime 进入后阻塞，直到测试放行——用于断言启动清理（清理发生在 go 之前）。
type gateStreamRuntime struct {
	// entered 已进入 ChatStream（清理已生效）
	entered chan struct{}
	// release 放行回放
	release chan struct{}
}

// ChatStream 进入即通报并等待放行，随后以 Done 收口。
func (g *gateStreamRuntime) ChatStream(system, content, sessionID string, onEvent func(kind LLMStreamKind, text string)) {
	close(g.entered)
	<-g.release
	onEvent(LLMStreamDone, "")
}

// panicStreamRuntime 回调前 panic——验证异常兜底（段5）。
type panicStreamRuntime struct{}

// ChatStream 直接 panic。
func (p *panicStreamRuntime) ChatStream(system, content, sessionID string, onEvent func(kind LLMStreamKind, text string)) {
	panic("boom")
}

// waitStreamDone 等待段盒结算（后台 goroutine 异步）。
func waitStreamDone(t *testing.T, db *runtime.DataBox, seg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if db.GetString("global", "llm_done:"+seg) == "1" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("等待流式结算超时（llm_done:%s 未置 1）", seg)
}

// TestLLMStreamStartSuccess 正常流——增量累积 / 计数 / 完成标志 / 消息参数透传。
func TestLLMStreamStartSuccess(t *testing.T) {
	db := runtime.NewDataBox()
	rt := &scriptedStreamRuntime{events: []streamStep{
		{kind: LLMStreamText, text: "he"},
		{kind: LLMStreamReasoning, text: "think"},
		{kind: LLMStreamText, text: "llo"},
		{kind: LLMStreamDone, text: ""},
	}}
	value, ok := LLMStreamStart(rt, db, 7, "sys", "content", nil)
	if !ok || value != "" {
		t.Fatalf("启动应成功且盒值为空串，得 %q/%v", value, ok)
	}
	waitStreamDone(t, db, "7")
	// C# 同口径：Text 与 Reasoning 都累积进完整回复（full.Append 不区分通道）
	if got := db.GetString("global", "llm_reply:7"); got != "hethinkllo" {
		t.Fatalf("完整回复 = %q，期望 hethinkllo", got)
	}
	if got := db.GetString("global", "llm_chunk:7"); got != "llo" {
		t.Fatalf("最新增量 = %q，期望 llo", got)
	}
	if v, hit := db.Get("global", "llm_chunk_count:7"); !hit || v != int64(3) {
		t.Fatalf("分片序号 = %#v/%v，期望 3", v, hit)
	}
	if rt.gotSystem != "sys" || rt.gotContent != "content" || rt.gotSession != "7" {
		t.Fatalf("消息参数透传不符: %q/%q/%q", rt.gotSystem, rt.gotContent, rt.gotSession)
	}
}

// TestLLMStreamStartResetSegment 启动清理——count 归零 / done 清空 / seen 与探测盒 Remove（不是写 false）。
func TestLLMStreamStartResetSegment(t *testing.T) {
	db := runtime.NewDataBox()
	db.Set("global", "llm_chunk_count:7", int64(5))
	db.Set("global", "llm_done:7", "1")
	db.Set("global", "llm_chunk_seen:7", int64(5))
	db.Set("7", "hasDone", true)
	db.Set("7", "hasChunk", true)
	rt := &gateStreamRuntime{entered: make(chan struct{}), release: make(chan struct{})}
	if _, ok := LLMStreamStart(rt, db, 7, "s", "c", nil); !ok {
		t.Fatal("启动应成功")
	}
	<-rt.entered
	if v, hit := db.Get("global", "llm_chunk_count:7"); !hit || v != int64(0) {
		t.Fatalf("计数应归零，得 %#v/%v", v, hit)
	}
	if v := db.GetString("global", "llm_done:7"); v != "" {
		t.Fatalf("完成标志应清空，得 %q", v)
	}
	if _, hit := db.Get("global", "llm_chunk_seen:7"); hit {
		t.Fatal("seen 盒应被 Remove")
	}
	if _, hit := db.Get("7", "hasDone"); hit {
		t.Fatal("hasDone 应被 Remove（不是写 false）")
	}
	if _, hit := db.Get("7", "hasChunk"); hit {
		t.Fatal("hasChunk 应被 Remove（不是写 false）")
	}
	close(rt.release)
	waitStreamDone(t, db, "7")
}

// TestLLMStreamStartErrorEvent 失败事件——ERR| 文本落 llm_reply + 完成标志置位。
func TestLLMStreamStartErrorEvent(t *testing.T) {
	db := runtime.NewDataBox()
	rt := &scriptedStreamRuntime{events: []streamStep{{kind: LLMStreamError, text: "ERR|TRANSPORT|x"}}}
	if _, ok := LLMStreamStart(rt, db, 3, "s", "c", nil); !ok {
		t.Fatal("启动应成功")
	}
	waitStreamDone(t, db, "3")
	if got := db.GetString("global", "llm_reply:3"); got != "ERR|TRANSPORT|x" {
		t.Fatalf("失败文本 = %q", got)
	}
}

// TestLLMStreamStartStreamEnd 无收口事件——STREAM_END 兜底（段4）。
func TestLLMStreamStartStreamEnd(t *testing.T) {
	db := runtime.NewDataBox()
	rt := &scriptedStreamRuntime{}
	if _, ok := LLMStreamStart(rt, db, 4, "s", "c", nil); !ok {
		t.Fatal("启动应成功")
	}
	waitStreamDone(t, db, "4")
	if got := db.GetString("global", "llm_reply:4"); got != "ERR|STREAM_END|流意外结束" {
		t.Fatalf("兜底文本 = %q", got)
	}
}

// TestLLMStreamStartPanic 异常兜底——ERR|<类型>| 前缀 + 完成标志（段5）。
func TestLLMStreamStartPanic(t *testing.T) {
	db := runtime.NewDataBox()
	if _, ok := LLMStreamStart(&panicStreamRuntime{}, db, 5, "s", "c", nil); !ok {
		t.Fatal("启动应成功")
	}
	waitStreamDone(t, db, "5")
	got := db.GetString("global", "llm_reply:5")
	if !strings.HasPrefix(got, "ERR|") || !strings.Contains(got, "boom") {
		t.Fatalf("异常文本不符: %q", got)
	}
}

// TestLLMStreamStartNoService 执行面 / DataBox 缺失——走失败侧且零副作用。
func TestLLMStreamStartNoService(t *testing.T) {
	db := runtime.NewDataBox()
	if _, ok := LLMStreamStart(nil, db, 7, "s", "c", nil); ok {
		t.Fatal("执行面缺失应走失败侧")
	}
	if _, ok := LLMStreamStart(nil, nil, 7, "s", "c", nil); ok {
		t.Fatal("DataBox 缺失应走失败侧")
	}
}

// TestLLMStreamStartFlowIDZero 无 flow 上下文——跳过清理但仍启动（seg="0"，C# 同口径）。
func TestLLMStreamStartFlowIDZero(t *testing.T) {
	db := runtime.NewDataBox()
	db.Set("global", "llm_chunk_count:0", int64(9))
	rt := &scriptedStreamRuntime{events: []streamStep{{kind: LLMStreamDone, text: ""}}}
	if _, ok := LLMStreamStart(rt, db, 0, "s", "c", nil); !ok {
		t.Fatal("无 flow 上下文也应启动（与 C# 一致）")
	}
	waitStreamDone(t, db, "0")
	if v, _ := db.Get("global", "llm_chunk_count:0"); v != int64(9) {
		t.Fatalf("flowId ≤ 0 不做启动清理，计数应保持 9，得 %#v", v)
	}
}

// TestLLMChunkReady 分块探测——新增 → true 并推进 seen；count < seen 归零检测；缺失 / 无上下文 false。
func TestLLMChunkReady(t *testing.T) {
	db := runtime.NewDataBox()
	if value, ok := LLMChunkReady(nil, 7); ok || value != "false" {
		t.Fatalf("DataBox 缺失应走失败侧，得 %q/%v", value, ok)
	}
	if value, ok := LLMChunkReady(db, 0); !ok || value != "false" {
		t.Fatalf("无 flow 上下文应 false/ok，得 %q/%v", value, ok)
	}
	if value, ok := LLMChunkReady(db, 7); !ok || value != "false" {
		t.Fatalf("计数缺失应 false/ok，得 %q/%v", value, ok)
	}
	db.Set("global", "llm_chunk_count:7", int64(3))
	if value, ok := LLMChunkReady(db, 7); !ok || value != "true" {
		t.Fatalf("新增应 true，得 %q/%v", value, ok)
	}
	if seen, _ := db.Get("global", "llm_chunk_seen:7"); seen != int64(3) {
		t.Fatalf("seen 应推进到 3，得 %#v", seen)
	}
	if value, _ := LLMChunkReady(db, 7); value != "false" {
		t.Fatalf("无新增应 false，得 %q", value)
	}
	db.Set("global", "llm_chunk_count:7", int64(5))
	if value, _ := LLMChunkReady(db, 7); value != "true" {
		t.Fatalf("继续新增应 true，得 %q", value)
	}
	// 新流式会话：count 归零后小于 seen —— seen 归零对齐后再判
	db.Set("global", "llm_chunk_count:7", int64(2))
	if value, _ := LLMChunkReady(db, 7); value != "true" {
		t.Fatalf("count<seen 应归零重判为 true，得 %q", value)
	}
	if seen, _ := db.Get("global", "llm_chunk_seen:7"); seen != int64(2) {
		t.Fatalf("归零后 seen 应为 2，得 %#v", seen)
	}
	// 类型不符（非 int64）按缺失处理
	db.Set("global", "llm_chunk_count:8", "3")
	if value, _ := LLMChunkReady(db, 8); value != "false" {
		t.Fatalf("类型不符应按缺失处理，得 %q", value)
	}
}

// TestLLMDoneReady 完成探测——"1" 为真；空串 / 缺失 / 无上下文为假。
func TestLLMDoneReady(t *testing.T) {
	db := runtime.NewDataBox()
	if _, ok := LLMDoneReady(nil, 7); ok {
		t.Fatal("DataBox 缺失应走失败侧")
	}
	if value, ok := LLMDoneReady(db, -1); !ok || value != "false" {
		t.Fatalf("无 flow 上下文应 false/ok，得 %q/%v", value, ok)
	}
	if value, _ := LLMDoneReady(db, 7); value != "false" {
		t.Fatalf("缺失应 false，得 %q", value)
	}
	db.Set("global", "llm_done:7", "")
	if value, _ := LLMDoneReady(db, 7); value != "false" {
		t.Fatalf("空串应 false，得 %q", value)
	}
	db.Set("global", "llm_done:7", "1")
	if value, _ := LLMDoneReady(db, 7); value != "true" {
		t.Fatalf("置 1 应 true，得 %q", value)
	}
}

// TestLLMReplyReadAndChunkRead 段盒取数——非空命中；空串 / 缺失 / 无上下文未命中。
func TestLLMReplyReadAndChunkRead(t *testing.T) {
	db := runtime.NewDataBox()
	db.Set("global", "llm_reply:7", "完整回复")
	db.Set("global", "llm_chunk:7", "增量")
	db.Set("global", "llm_reply:8", "")
	db.Set("global", "llm_chunk:8", "")

	if value, ok := LLMReplyRead(db, 7); !ok || value != "完整回复" {
		t.Fatalf("reply 命中不符: %q/%v", value, ok)
	}
	if value, ok := LLMChunkRead(db, 7); !ok || value != "增量" {
		t.Fatalf("chunk 命中不符: %q/%v", value, ok)
	}
	if _, ok := LLMReplyRead(db, 8); ok {
		t.Fatal("空串 reply 应未命中（走失败侧）")
	}
	if _, ok := LLMChunkRead(db, 8); ok {
		t.Fatal("空串 chunk 应未命中（走失败侧）")
	}
	if _, ok := LLMReplyRead(db, 9); ok {
		t.Fatal("缺失 reply 应未命中")
	}
	if _, ok := LLMChunkRead(db, 0); ok {
		t.Fatal("无上下文应未命中")
	}
	if _, ok := LLMReplyRead(nil, 7); ok {
		t.Fatal("DataBox 缺失应走失败侧")
	}
	if _, ok := LLMChunkRead(nil, 7); ok {
		t.Fatal("DataBox 缺失应走失败侧")
	}
}
