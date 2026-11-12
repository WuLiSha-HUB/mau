package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestViewStoreDerived 前文派生——user / systemautomessage / text / reason 四条路径。
func TestViewStoreDerived(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.view.jsonl")
	vs := NewViewStore(path)
	vs.OnUserMessage(Message{Role: RoleUser, Content: "你好", CreatedAt: 100}, 0, "")
	vs.OnSystemMessage(Message{Role: RoleUser, Content: "[systemauto]注入", CreatedAt: 101}, 1, SysKindSleep, "group:g:1:user")
	vs.OnAssistantText(Message{Role: RoleAssistant, Content: "答", ReasoningContent: "想", CreatedAt: 102}, 2, 500)
	got := vs.Blocks()
	if len(got) != 4 {
		t.Fatalf("应生成 4 块（user / sys / reason / text），实得 %d: %+v", len(got), got)
	}
	if got[0].RenderType != TypeUser || got[0].Payload != `{"text":"你好"}` {
		t.Fatalf("user 块不符: %+v", got[0])
	}
	if got[1].RenderType != TypeSystemAutoMessage {
		t.Fatalf("sys 块类型不符: %+v", got[1])
	}
	if !strings.Contains(got[1].Payload, `"source":"sleep"`) || !strings.Contains(got[1].Payload, `"origin":"group:g:1:user"`) {
		t.Fatalf("sys 载荷应带 source / origin: %s", got[1].Payload)
	}
	if got[2].RenderType != TypeReason || !strings.Contains(got[2].Payload, `"durMs":500`) {
		t.Fatalf("reason 块不符: %+v", got[2])
	}
	if got[3].RenderType != TypeText || got[3].Payload != `{"text":"答"}` {
		t.Fatalf("text 块不符: %+v", got[3])
	}
	// 时间戳单调化——同 CreatedAt 的 reason/text 不得同刻
	if got[2].Timestamp >= got[3].Timestamp {
		t.Fatalf("时间戳应单调递增: %d / %d", got[2].Timestamp, got[3].Timestamp)
	}
}

// TestViewStoreToolCard 工具卡配对——声明登记 + 结果回填生成 toolcard；孤立结果丢弃。
func TestViewStoreToolCard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.view.jsonl")
	vs := NewViewStore(path)
	toolCalls := `[{"id":"a","function":{"name":"text-read","arguments":"{\"path\":\"a\"}"}},{"id":"b","function":{"name":"text-grep","arguments":"{}"}}]`
	vs.OnAssistantToolCalls(Message{Role: RoleAssistant, ToolCallsJSON: toolCalls, CreatedAt: 200}, 1, -1)
	vs.OnToolResult(Message{Role: RoleTool, ToolCallID: "a", ToolName: "text-read", Content: "ERR|BAD_PATH|no such host", CreatedAt: 201}, 2, 12, 3, 9)
	vs.OnToolResult(Message{Role: RoleTool, ToolCallID: "zombie", ToolName: "x", Content: "孤立", CreatedAt: 202}, 3, 0, 0, 0)
	blocks := vs.Blocks()
	if len(blocks) != 1 {
		t.Fatalf("应只生成 1 张工具卡（孤立结果丢弃），实得 %d: %+v", len(blocks), blocks)
	}
	card := blocks[0]
	if card.RenderType != TypeToolCard || card.MsgIndex != 2 {
		t.Fatalf("工具卡定位不符: %+v", card)
	}
	if !strings.Contains(card.Payload, `"name":"text-read"`) || !strings.Contains(card.Payload, `"toolIndex":1`) || !strings.Contains(card.Payload, `"toolTotal":2`) {
		t.Fatalf("工具卡载荷不符: %s", card.Payload)
	}
	// 视图层报错中文注释——真实前文不改，仅视图条目追加
	if !strings.Contains(card.Payload, "域名解析失败") {
		t.Fatalf("ERR 结果应追加中文注释: %s", card.Payload)
	}
	if card.Timestamp != 200 {
		t.Fatalf("工具卡时间戳应取声明时刻: %d", card.Timestamp)
	}
}

// TestViewStorePersistAndLoad 落盘行格式 + 载入分流 + 归并序。
func TestViewStorePersistAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v3.view.jsonl")
	vs := NewViewStore(path)
	vs.OnUserMessage(Message{Role: RoleUser, Content: "一", CreatedAt: 10}, 0, "")
	vs.AppendGapText("先说一句", 20)
	vs.AppendRoundSummary(`{"data":{"prompt":1}}`, 30)
	vs.AppendError("ERR|X|boom", 40)
	vs.AppendRetry(`{"state":"retry"}`, 50)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	first := strings.Split(string(raw), "\n")[0]
	if !strings.HasPrefix(first, `{"Timestamp":10,"RenderType":"user","Payload":"{\"text\":\"一\"}","MsgIndex":0,"Round":0,"t":"b"}`) {
		t.Fatalf("块行格式不符: %s", first)
	}

	loaded := NewViewStore(path)
	loaded.Load()
	blocks := loaded.Blocks()
	if len(blocks) != 5 {
		t.Fatalf("载入应为 5 块，实得 %d", len(blocks))
	}
	wantOrder := []string{TypeUser, TypeGapText, TypeRoundSum, TypeError, TypeRetry}
	for i, want := range wantOrder {
		if blocks[i].RenderType != want {
			t.Fatalf("归并序不符（第 %d 块 = %s，期望 %s）", i, blocks[i].RenderType, want)
		}
	}
}

// TestViewStoreTruncate 回滚截断——前文派生按索引、非前文派生按时间戳界限。
func TestViewStoreTruncate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v4.view.jsonl")
	vs := NewViewStore(path)
	vs.OnUserMessage(Message{Role: RoleUser, Content: "一", CreatedAt: 10}, 0, "")
	vs.OnUserMessage(Message{Role: RoleUser, Content: "二", CreatedAt: 20}, 1, "")
	vs.AppendGapText("间隙", 30)
	vs.TruncateFrom(0)
	blocks := vs.Blocks()
	if len(blocks) != 1 || blocks[0].MsgIndex != 0 {
		t.Fatalf("截断后应只留索引 0 的块: %+v", blocks)
	}
	// 写盘同步——重载仍是 1 块
	reloaded := NewViewStore(path)
	reloaded.Load()
	if len(reloaded.Blocks()) != 1 {
		t.Fatalf("截断应落盘: %d", len(reloaded.Blocks()))
	}
}

// TestViewStoreClear 清空同步落盘。
func TestViewStoreClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v5.view.jsonl")
	vs := NewViewStore(path)
	vs.OnUserMessage(Message{Role: RoleUser, Content: "一", CreatedAt: 10}, 0, "")
	vs.Clear()
	raw, _ := os.ReadFile(path)
	if strings.TrimSpace(string(raw)) != "" {
		t.Fatalf("清空后文件应为空: %q", string(raw))
	}
}

// TestViewStoreInjectReport 注入报告普通块——置首（ts=0）与覆写语义。
func TestViewStoreInjectReport(t *testing.T) {
	vs := NewViewStore(filepath.Join(t.TempDir(), "v6.view.jsonl"))
	vs.OnUserMessage(Message{Role: RoleUser, Content: "一", CreatedAt: 10}, 0, "")
	vs.SetInjectReport(`{"status":"ok"}`)
	blocks := vs.Blocks()
	if len(blocks) != 2 || blocks[0].RenderType != viewInjectReportType || blocks[0].Timestamp != 0 {
		t.Fatalf("注入报告应排最前: %+v", blocks)
	}
	vs.SetInjectReport(`{"status":"second"}`)
	if got := vs.GetInjectReport(); got != `{"status":"second"}` {
		t.Fatalf("覆写语义不符: %s", got)
	}
	if len(vs.Blocks()) != 2 {
		t.Fatalf("覆写后仍只应有一份报告: %d", len(vs.Blocks()))
	}
}

// TestViewStoreRoundSummaryNote 轮末总结条目——与 roundsum 同容器、type 区分、audio 可选、空文本不落。
func TestViewStoreRoundSummaryNote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v7.view.jsonl")
	vs := NewViewStore(path)
	vs.AppendRoundSummary(`{"data":{"prompt":1}}`, 100)
	vs.AppendRoundSummaryNote("本轮做了 X", 101, "summary-1.wav")
	vs.AppendRoundSummaryNote("", 102, "")
	blocks := vs.Blocks()
	if len(blocks) != 2 {
		t.Fatalf("应 2 块（roundsum + roundsummary；空文本不落），实得 %d: %+v", len(blocks), blocks)
	}
	if blocks[1].RenderType != TypeRoundSummary || blocks[1].MsgIndex != -1 {
		t.Fatalf("总结块型 / 定位不符: %+v", blocks[1])
	}
	if blocks[1].Payload != `{"text":"本轮做了 X","audio":"summary-1.wav"}` {
		t.Fatalf("总结载荷不符: %s", blocks[1].Payload)
	}
	vs.AppendRoundSummaryNote("失败侧", 103, "")
	if got := vs.Blocks()[2].Payload; got != `{"text":"失败侧"}` {
		t.Fatalf("无音频不应写 audio 字段: %s", got)
	}
	// 载入分流——roundsummary 与 roundsum 同容器（归并序保持）
	reloaded := NewViewStore(path)
	reloaded.Load()
	got := reloaded.Blocks()
	if len(got) != 3 || got[1].RenderType != TypeRoundSummary {
		t.Fatalf("载入分流不符: %+v", got)
	}
}

// TestErrorNoteApply 报错中文注释——中文消息不加注释、无 ERR 原样、CRLF 保真。
func TestErrorNoteApply(t *testing.T) {
	if got := ApplyErrorNote("ERR|BAD_PATH|The system cannot find the file specified."); !strings.HasSuffix(got, "（系统找不到指定文件）") {
		t.Fatalf("应追加中文注释: %s", got)
	}
	if got := ApplyErrorNote("ERR|BAD_ARGS|路径不存在"); got != "ERR|BAD_ARGS|路径不存在" {
		t.Fatalf("中文消息不应加注释: %s", got)
	}
	if got := ApplyErrorNote("没有报错"); got != "没有报错" {
		t.Fatalf("无 ERR 应原样: %s", got)
	}
	got := ApplyErrorNote("行一\r\nERR|X|timed out")
	if !strings.Contains(got, "行一\r\n") || !strings.HasSuffix(got, "（操作超时）") {
		t.Fatalf("换行应保真且命中映射: %q", got)
	}
}

// TestViewStoreAppendTailMissing 顶尾补差——前文 5 条 / 视图 2 条 → 补 3 块；幂等；已有条目字节不变。
func TestViewStoreAppendTailMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v8.view.jsonl")
	vs := NewViewStore(path)
	toolCalls := `[{"id":"a","function":{"name":"text-read","arguments":"{\"path\":\"a\"}"}}]`
	messages := []Message{
		{Role: RoleUser, Content: "一", CreatedAt: 100},
		{Role: RoleAssistant, Content: "答一", CreatedAt: 200},
		{Role: RoleUser, Content: "二", CreatedAt: 300},
		{Role: RoleAssistant, ToolCallsJSON: toolCalls, ReasoningContent: "想一想", CreatedAt: 400},
		{Role: RoleTool, ToolCallID: "a", ToolName: "text-read", Content: "ERR|BAD_PATH|The system cannot find the path specified.", CreatedAt: 500},
	}
	// 已落视图两条（覆盖索引 0 / 1）
	vs.OnUserMessage(messages[0], 0, "")
	vs.OnAssistantText(messages[1], 1, 5)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if got := vs.AppendTailMissing(messages); got != 3 {
		t.Fatalf("应补 3 块（user + reason + toolcard），实得 %d", got)
	}
	blocks := vs.Blocks()
	if len(blocks) != 5 {
		t.Fatalf("补后应 5 块，实得 %d: %+v", len(blocks), blocks)
	}
	// 补差序：user(2) → reason(3) → toolcard(4)
	if blocks[2].RenderType != TypeUser || blocks[2].MsgIndex != 2 || blocks[2].Payload != `{"text":"二"}` {
		t.Fatalf("user 补差不符: %+v", blocks[2])
	}
	if blocks[3].RenderType != TypeReason || blocks[3].MsgIndex != 3 {
		t.Fatalf("reason 补差不符: %+v", blocks[3])
	}
	if !strings.Contains(blocks[3].Payload, `"durMs":-1`) || !strings.Contains(blocks[3].Payload, `"cps":-1`) {
		t.Fatalf("载入补差思考时长应为 -1（未记录）: %s", blocks[3].Payload)
	}
	card := blocks[4]
	if card.RenderType != TypeToolCard || card.MsgIndex != 4 {
		t.Fatalf("工具卡补差不符: %+v", card)
	}
	if !strings.Contains(card.Payload, `"name":"text-read"`) || !strings.Contains(card.Payload, `"toolIndex":1`) ||
		!strings.Contains(card.Payload, `"toolTotal":1`) || !strings.Contains(card.Payload, `"durMs":-1`) {
		t.Fatalf("工具卡载荷不符: %s", card.Payload)
	}
	if !strings.Contains(card.Payload, "（系统找不到指定路径）") {
		t.Fatalf("工具卡应追加中文注释: %s", card.Payload)
	}
	if card.Timestamp != 401 {
		t.Fatalf("工具卡时间戳应取声明时刻并经单调化（400→401）: %d", card.Timestamp)
	}
	// 已有条目字节不变——文件前缀保持，新行只在尾部追加
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(after), string(before)) || len(after) <= len(before) {
		t.Fatalf("补差应 append-only（已有条目字节不变）")
	}
	// 幂等——再跑一次零补
	if got := vs.AppendTailMissing(messages); got != 0 {
		t.Fatalf("重复调用应 0 块，实得 %d", got)
	}
	if len(vs.Blocks()) != 5 {
		t.Fatalf("幂等后仍应 5 块，实得 %d", len(vs.Blocks()))
	}
}

// TestViewStoreAppendTailMissingSkips 顶尾补差跳过面——system 角色不进视图；孤立 tool 结果丢弃。
func TestViewStoreAppendTailMissingSkips(t *testing.T) {
	vs := NewViewStore(filepath.Join(t.TempDir(), "v9.view.jsonl"))
	messages := []Message{
		{Role: RoleSystem, Content: "系统提示词", CreatedAt: 100},
		{Role: RoleTool, ToolCallID: "orphan", ToolName: "text-read", Content: "孤立", CreatedAt: 200},
	}
	if got := vs.AppendTailMissing(messages); got != 0 {
		t.Fatalf("system / 孤立 tool 不应补块，实得 %d", got)
	}
	if len(vs.Blocks()) != 0 {
		t.Fatalf("不应产生条目: %+v", vs.Blocks())
	}
}

// TestViewStoreAppendTailMissingText 顶尾补差纯文本路径 + 载入后工具配对（pendingTools 空，回前文解析）。
func TestViewStoreAppendTailMissingText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v10.view.jsonl")
	vs := NewViewStore(path)
	messages := []Message{
		{Role: RoleSystem, Content: "系统", CreatedAt: 100},
		{Role: RoleUser, Content: "问", CreatedAt: 200},
		{Role: RoleAssistant, Content: "答", ReasoningContent: "想", CreatedAt: 300},
	}
	if got := vs.AppendTailMissing(messages); got != 3 {
		t.Fatalf("纯文本路径应补 3 块（user + reason + text），实得 %d", got)
	}
	blocks := vs.Blocks()
	if blocks[2].RenderType != TypeText || blocks[2].Payload != `{"text":"答"}` {
		t.Fatalf("text 补差不符: %+v", blocks[2])
	}

	// 载入后工具配对——assistant 已落视图、tool 缺块；pendingTools 经 Load 清空，配对只能回前文解析
	toolPath := filepath.Join(t.TempDir(), "v11.view.jsonl")
	toolCalls := `[{"id":"a","function":{"name":"text-read","arguments":"{}"}}]`
	writer := NewViewStore(toolPath)
	writer.OnAssistantToolCalls(Message{Role: RoleAssistant, ToolCallsJSON: toolCalls, ReasoningContent: "想", CreatedAt: 400}, 0, -1)
	loaded := NewViewStore(toolPath)
	loaded.Load()
	wm := []Message{
		{Role: RoleAssistant, ToolCallsJSON: toolCalls, ReasoningContent: "想", CreatedAt: 400},
		{Role: RoleTool, ToolCallID: "a", ToolName: "text-read", Content: "ok", CreatedAt: 500},
	}
	if got := loaded.AppendTailMissing(wm); got != 1 {
		t.Fatalf("载入后应补 1 张工具卡，实得 %d", got)
	}
	all := loaded.Blocks()
	if len(all) != 2 {
		t.Fatalf("载入后应 2 块（reason + toolcard），实得 %d: %+v", len(all), all)
	}
	card := all[1]
	if card.RenderType != TypeToolCard || !strings.Contains(card.Payload, `"name":"text-read"`) {
		t.Fatalf("载入后工具卡不符: %+v", card)
	}
}

// TestViewStoreCopyFrom 视图层播种（fork）——源保留面（≤ 切点）单向复制 + 界限口径 + 落盘往返。
func TestViewStoreCopyFrom(t *testing.T) {
	source := NewViewStore(filepath.Join(t.TempDir(), "fork-src.view.jsonl"))
	source.OnUserMessage(Message{Role: RoleUser, Content: "一", CreatedAt: 100}, 0, "")
	source.AppendGapText("间隙", 120)
	source.OnAssistantText(Message{Role: RoleAssistant, Content: "答", CreatedAt: 130}, 1, 5)
	source.OnUserMessage(Message{Role: RoleUser, Content: "二", CreatedAt: 200}, 2, "")
	source.AppendRoundSummary(`{"data":{"prompt":1}}`, 210)
	source.AppendError("ERR|X|boom", 220)
	source.SetInjectReport(`{"status":"ok"}`)
	source.registerPendingTools(`[{"id":"a","function":{"name":"text-read","arguments":"{}"}}]`, 230)
	if got := len(source.Blocks()); got != 7 {
		t.Fatalf("源面应 7 块（注入报告 + 3 前文派生 + gap + roundsum + error），实得 %d", got)
	}

	targetPath := filepath.Join(t.TempDir(), "fork-dst.view.jsonl")
	target := NewViewStore(targetPath)
	target.CopyFrom(source, 1)
	blocks := target.Blocks()
	if len(blocks) != 4 {
		t.Fatalf("播种面应 4 块（注入报告 + user / gap / text），实得 %d: %+v", len(blocks), blocks)
	}
	want := []string{viewInjectReportType, TypeUser, TypeGapText, TypeText}
	for i, typ := range want {
		if blocks[i].RenderType != typ {
			t.Fatalf("第 %d 块类型不符：%s（期望 %s）", i, blocks[i].RenderType, typ)
		}
	}
	if blocks[1].MsgIndex != 0 || blocks[3].MsgIndex != 1 {
		t.Fatalf("前文派生索引应原样沿用: %+v", blocks)
	}
	if got := target.GetInjectReport(); got != `{"status":"ok"}` {
		t.Fatalf("注入报告应随面搬入: %s", got)
	}
	// 界限口径——被丢弃区间起点（200）之后的非前文派生条目全部丢弃
	for _, block := range blocks {
		if block.RenderType == TypeRoundSum || block.RenderType == TypeError {
			t.Fatalf("界限之后的非前文派生条目不应搬入: %+v", block)
		}
	}
	// 源面不受影响——单向复制（块数 / 待配对 / lastTs 均不动）
	if got := len(source.Blocks()); got != 7 || len(source.pendingTools) != 1 || source.lastTs != 220 {
		t.Fatalf("播种应单向：源面块 %d / 待配对 %d / lastTs %d", got, len(source.pendingTools), source.lastTs)
	}
	// 播种面待配对清空 + lastTs 取保留面最大时间戳
	if len(target.pendingTools) != 0 {
		t.Fatalf("播种面待配对工具应清空: %d", len(target.pendingTools))
	}
	if target.lastTs != 130 {
		t.Fatalf("lastTs 应取保留面最大时间戳（130），实得 %d", target.lastTs)
	}
	target.AppendGapText("播种后新条目", 130)
	if got := target.Blocks()[4].Timestamp; got != 131 {
		t.Fatalf("播种后建条应单调接续（131），实得 %d", got)
	}
	// 落盘往返——播种面可读回
	reloaded := NewViewStore(targetPath)
	reloaded.Load()
	if got := reloaded.Blocks(); len(got) != 5 {
		t.Fatalf("播种面落盘应读回 5 块，实得 %d", len(got))
	}
}

// TestViewStoreCopyFromEdges 播种边界——nil 源 / 自身复制空操作；无被丢弃区间时非前文派生条目全保留。
func TestViewStoreCopyFromEdges(t *testing.T) {
	vs := NewViewStore(filepath.Join(t.TempDir(), "fork-edge.view.jsonl"))
	vs.OnUserMessage(Message{Role: RoleUser, Content: "一", CreatedAt: 10}, 0, "")
	vs.AppendError("ERR|X|boom", 20)
	vs.CopyFrom(nil, 0)
	if got := len(vs.Blocks()); got != 2 {
		t.Fatalf("nil 源应空操作，实得 %d 块", got)
	}
	vs.CopyFrom(vs, 0)
	if got := len(vs.Blocks()); got != 2 {
		t.Fatalf("自身复制应空操作（不得自清空），实得 %d 块", got)
	}

	other := NewViewStore(filepath.Join(t.TempDir(), "fork-edge2.view.jsonl"))
	other.OnUserMessage(Message{Role: RoleUser, Content: "一", CreatedAt: 10}, 0, "")
	other.AppendError("ERR|X|boom", 20)
	seed := NewViewStore(filepath.Join(t.TempDir(), "fork-edge3.view.jsonl"))
	seed.CopyFrom(other, 0)
	if got := len(seed.Blocks()); got != 2 {
		t.Fatalf("切点覆盖全部前文派生条目时应全保留，实得 %d 块", got)
	}
}
