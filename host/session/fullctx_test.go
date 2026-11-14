package session

// 完整前文留档测试——格式往返 / 增量一致 / 视图口径 / 轮转 / 头部漂移守卫 / 采集触发。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fullCtxRig 留档测试装配——临时目录 + 可变消息序列 + 份数/比值读取器（节流关闭）。
type fullCtxRig struct {
	// dir 落点目录
	dir string
	// msgs 采集源消息序列
	msgs []Message
	// keep 份数读取器返回值
	keep int
	// ratio 比值读取器返回值（0 = 无实测）
	ratio float64
	// store 被测留档
	store *FullContextStore
}

// newFullCtxRig 建立测试装配。
func newFullCtxRig(t *testing.T) *fullCtxRig {
	t.Helper()
	rig := &fullCtxRig{dir: t.TempDir(), keep: 3}
	rig.store = NewFullContextStore(
		rig.dir,
		"cat-a",
		func() []Message { return rig.msgs },
		func() int { return rig.keep },
		func() float64 { return rig.ratio },
	)
	rig.store.interval = 0
	return rig
}

// TestFullContextFormatRoundTrip 采集产出格式契约 + 剥离还原（四字段 / 段头 / 头部行）。
func TestFullContextFormatRoundTrip(t *testing.T) {
	rig := newFullCtxRig(t)
	rig.msgs = []Message{
		{Role: RoleSystem, Content: "系统提示", CreatedAt: 11},
		{Role: RoleUser, Content: "你好😀", CreatedAt: 22},
		{Role: RoleAssistant, ToolCallsJSON: `[{"id":"c1"}]`, ReasoningContent: "想想", CreatedAt: 33},
		{Role: RoleTool, Content: "结果", ToolCallID: "c1", ToolName: "tool-a", CreatedAt: 44},
	}
	rig.store.Capture(true)
	if rig.store.path == "" {
		t.Fatal("首次采集未创建留档文件")
	}
	text, err := readFullCtxFile(rig.store.path)
	if err != nil {
		t.Fatalf("留档读取失败：%v", err)
	}
	if !strings.HasPrefix(text, fullCtxHeaderVersion+"\n") {
		t.Fatalf("文件头版本行不符：%q", headRunes(text, 40))
	}
	if !strings.Contains(text, "# cat=cat-a session=") || !strings.Contains(text, " source=default") {
		t.Fatalf("文件头字段不符：%q", headRunes(text, 200))
	}
	// 段头四属性恒全写（空值写空）
	if !strings.Contains(text, "[[CH4-CTX role=system time=11 toolcallid= tool=]]") {
		t.Fatalf("段头属性不完整：%q", headRunes(text, 300))
	}
	// 空字段整块不出现（assistant 无 content）
	if strings.Contains(text, "[[content 0]]") {
		t.Fatalf("空字段块不应出现")
	}
	entries, err := ParseFullContext(text)
	if err != nil {
		t.Fatalf("剥离失败：%v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("条目数期望 4，实际 %d", len(entries))
	}
	if entries[1].Content != "你好😀" || entries[1].Role != "user" || entries[1].Time != 22 {
		t.Fatalf("第二条还原不符 %+v", entries[1])
	}
	if entries[2].ToolCalls != `[{"id":"c1"}]` || entries[2].Reasoning != "想想" {
		t.Fatalf("assistant 字段还原不符 %+v", entries[2])
	}
	if entries[3].ToolCallID != "c1" || entries[3].ToolName != "tool-a" || entries[3].Content != "结果" {
		t.Fatalf("tool 条目还原不符 %+v", entries[3])
	}
}

// TestFullContextIncrementalAppend 连续采集——落盘结果恒等于全量构建文本（增量与重写同口径）。
func TestFullContextIncrementalAppend(t *testing.T) {
	rig := newFullCtxRig(t)
	// 头部 tokens 位数在两轮之间保持不变（41 → 50），走「头部原地刷新 + 尾部追加」路径
	rig.msgs = []Message{
		{Role: RoleSystem, Content: strings.Repeat("a", 100), CreatedAt: 1},
	}
	rig.store.Capture(true)
	first, err := readFullCtxFile(rig.store.path)
	if err != nil {
		t.Fatalf("首轮读取失败：%v", err)
	}
	rig.msgs = append(rig.msgs, Message{Role: RoleUser, Content: strings.Repeat("b", 20), CreatedAt: 2})
	rig.store.Capture(true)
	second, err := readFullCtxFile(rig.store.path)
	if err != nil {
		t.Fatalf("次轮读取失败：%v", err)
	}
	want := rig.store.buildText(rig.msgs)
	if second != want {
		t.Fatalf("增量结果与全量文本不一致：len %d vs %d", len(second), len(want))
	}
	if !strings.HasPrefix(second, "# CH4-FULLCTX v1\n") {
		t.Fatalf("增量后文件头丢失：%q", headRunes(second, 40))
	}
	// 尾部确实追加（首轮末条仍在，且数量增长）
	if len(second) <= len(first) {
		t.Fatalf("次轮长度应增长：%d → %d", len(first), len(second))
	}
	entries, err := ParseFullContext(second)
	if err != nil || len(entries) != 2 {
		t.Fatalf("增量后剥离失败（%v）或条目数不符：%d", err, len(entries))
	}
}

// TestFullContextHeadDriftGuard 正文首段不一致（前文丢头 / system 被替换）——拒绝全量重写，留档原文保留。
func TestFullContextHeadDriftGuard(t *testing.T) {
	rig := newFullCtxRig(t)
	rig.msgs = []Message{
		{Role: RoleSystem, Content: "旧系统提示", CreatedAt: 1},
		{Role: RoleUser, Content: "问题", CreatedAt: 2},
	}
	rig.store.Capture(true)
	before, err := readFullCtxFile(rig.store.path)
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	rig.msgs[0].Content = "新系统提示"
	rig.store.Capture(true)
	after, err := readFullCtxFile(rig.store.path)
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	if after != before {
		t.Fatalf("头部漂移应拒绝重写——留档被覆写")
	}
	if !rig.store.headDriftWarned {
		t.Fatalf("头部漂移应置告警标志（出声一次）")
	}
}

// TestFullContextBuildView 视图——条目源 = 留档文本（path 非空 / 缺省 ratio / tokens 折算）。
func TestFullContextBuildView(t *testing.T) {
	rig := newFullCtxRig(t)
	rig.msgs = []Message{
		{Role: RoleUser, Content: strings.Repeat("a", 100), CreatedAt: 1},
		{Role: RoleAssistant, Content: "回复", CreatedAt: 2},
	}
	view := decodeView(t, rig.store.BuildView(0))
	if view["ok"] != true || view["count"] != float64(2) {
		t.Fatalf("视图头部不符 %+v", view)
	}
	if view["path"] == "" || view["session"] != "cat-a" {
		t.Fatalf("视图路径 / 会话不符 %+v", view)
	}
	if view["ratio"] != float64(2.4) || view["estimated"] != true {
		t.Fatalf("无实测应回落缺省 2.4 且 estimated=true：%+v", view)
	}
	if view["chars"] != float64(102) { // 100 + 「回复」2
		t.Fatalf("chars 期望 102，实际 %v", view["chars"])
	}
	// tokens = 102 ÷ 2.4 = 42.5 → 43（四舍五入远离零）
	if view["tokens"] != float64(43) {
		t.Fatalf("tokens 期望 43，实际 %v", view["tokens"])
	}
	items := view["items"].([]any)
	if items[0].(map[string]any)["role"] != "user" || items[1].(map[string]any)["role"] != "assistant" {
		t.Fatalf("条目角色不符 %+v", items)
	}

	// 实测比值生效（estimated=false）
	rig.ratio = 3.0
	view = decodeView(t, rig.store.BuildView(0))
	if view["ratio"] != float64(3) || view["estimated"] != false {
		t.Fatalf("实测比值未生效 %+v", view)
	}
	if view["tokens"] != float64(34) { // 102 ÷ 3
		t.Fatalf("实测 tokens 期望 34，实际 %v", view["tokens"])
	}
}

// TestFullContextFinalizeRotate 定稿——当前份转历史 + 按份数轮转（最旧消失）。
func TestFullContextFinalizeRotate(t *testing.T) {
	rig := newFullCtxRig(t)
	rig.keep = 2
	// 造三份历史（文件名时戳序早于当前）
	for _, stamp := range []string{"20260101_000001", "20260101_000002", "20260101_000003"} {
		path := filepath.Join(rig.dir, "cat-a_"+stamp+".txt")
		if err := writeFullCtxFile(path, "# CH4-FULLCTX v1\n# cat=cat-a session="+stamp+"\n"); err != nil {
			t.Fatalf("造历史份失败：%v", err)
		}
	}
	rig.msgs = []Message{{Role: RoleUser, Content: "定稿内容", CreatedAt: 1}}
	done := rig.store.Finalize()
	if done == "" {
		t.Fatal("定稿应返回留档路径")
	}
	files, err := os.ReadDir(rig.dir)
	if err != nil {
		t.Fatalf("目录读取失败：%v", err)
	}
	names := make([]string, 0, len(files))
	for _, entry := range files {
		names = append(names, entry.Name())
	}
	if len(names) != 2 {
		t.Fatalf("轮转后应保留 2 份，实际 %d：%v", len(names), names)
	}
	// 最旧两份被销毁，最新（本次定稿）在场
	for _, name := range names {
		if name == "cat-a_20260101_000001.txt" || name == "cat-a_20260101_000002.txt" {
			t.Fatalf("最旧份未被销毁：%v", names)
		}
	}
	// 定稿后状态复位（下次采集新建一份）
	if rig.store.path != "" || rig.store.stamp != "" {
		t.Fatalf("定稿后路径 / 时戳未复位")
	}
}

// TestFullContextKeepZero 份数 0 = 不销毁（保留全部历史份——止血出口）。
func TestFullContextKeepZero(t *testing.T) {
	rig := newFullCtxRig(t)
	rig.keep = 0
	for _, stamp := range []string{"20260101_000001", "20260101_000002"} {
		if err := writeFullCtxFile(filepath.Join(rig.dir, "cat-a_"+stamp+".txt"), "x"); err != nil {
			t.Fatalf("造历史份失败：%v", err)
		}
	}
	rig.msgs = []Message{{Role: RoleUser, Content: "内容", CreatedAt: 1}}
	rig.store.Finalize()
	files, err := os.ReadDir(rig.dir)
	if err != nil {
		t.Fatalf("目录读取失败：%v", err)
	}
	if len(files) != 3 {
		t.Fatalf("keep=0 应保留全部（3 份），实际 %d", len(files))
	}
}

// TestResolveFullCtxKeep 份数解析——合法值与非法回落（不静默接受越界值）。
func TestResolveFullCtxKeep(t *testing.T) {
	cases := map[string]int{
		"":    3,
		"0":   0,
		"3":   3,
		"20":  20,
		"21":  3,
		"-1":  3,
		"abc": 3,
	}
	for raw, want := range cases {
		if got := ResolveFullCtxKeep(raw); got != want {
			t.Fatalf("ResolveFullCtxKeep(%q) 期望 %d，实际 %d", raw, want, got)
		}
	}
}

// TestContextAppendTriggersCapture 消息入前文即触发采集——挂载后无需手动调用。
func TestContextAppendTriggersCapture(t *testing.T) {
	ctx := NewContext()
	sess := NewSession("cat-a", ctx, nil, nil)
	store := NewFullContextStore(t.TempDir(), "cat-a", ctx.Messages, func() int { return 3 }, func() float64 { return 0 })
	store.interval = 0
	sess.SetFullContext(store)
	ctx.AddUserMessage("触发采集")
	if store.path == "" {
		t.Fatal("消息追加未触发采集")
	}
	text, err := readFullCtxFile(store.path)
	if err != nil {
		t.Fatalf("留档读取失败：%v", err)
	}
	if !strings.Contains(text, "触发采集") {
		t.Fatalf("留档未含追加消息：%q", headRunes(text, 300))
	}
}

// TestPatrolFullContext 请求前巡检——头一致零动作 / 头漂移按留档重建 / 巡检时刻落基准。
//
// 消费点：launchLlm（round.go）每次请求前调用；判据见 design-ch4-ctx-rebuild §二。
func TestPatrolFullContext(t *testing.T) {
	dir := t.TempDir()
	ctx := NewContext()
	store := NewSessionStore(filepath.Join(dir, "s.jsonl"))
	views := NewViewStore(filepath.Join(dir, "s.view.jsonl"))
	sess := NewSession("cat-a", ctx, store, views)
	full := NewFullContextStore(dir, "cat-a", ctx.Messages, func() int { return 3 }, func() float64 { return 0 })
	full.interval = 0
	sess.SetFullContext(full)
	sess.ctxPatrolInterval = 0 // 关闭巡检节流：每次调用都查

	base := []Message{
		{Role: RoleSystem, Content: "系统提示"},
		{Role: RoleUser, Content: "第一条"},
		{Role: RoleAssistant, Content: "回复一"},
	}
	ctx.ReplaceMessages(base)
	full.Capture(true)

	// [1] 头一致 → 原样返回，前文不动
	same := sess.PatrolFullContext(ctx.Messages())
	if len(same) != len(base) || len(ctx.Messages()) != len(base) {
		t.Fatalf("头一致不应改动前文：巡检返回 %d 条 / 前文 %d 条", len(same), len(ctx.Messages()))
	}

	// [2] 头漂移（前文丢头）→ 按留档重建
	ctx.ReplaceMessages([]Message{
		{Role: RoleSystem, Content: "系统提示（变体）"},
		{Role: RoleUser, Content: "新的一轮"},
	})
	rebuilt := sess.PatrolFullContext(ctx.Messages())
	if len(rebuilt) != len(base) {
		t.Fatalf("重建后应回到留档 %d 条，得到 %d 条：%+v", len(base), len(rebuilt), rebuilt)
	}
	if ctx.Messages()[0].Content != "系统提示" {
		t.Fatalf("前文未被替换为留档内容：%q", ctx.Messages()[0].Content)
	}

	// [3] 节流基准已落——后续请求按窗口节流
	if sess.ctxPatrolAt.IsZero() {
		t.Fatal("巡检时刻未记录（节流基准缺失）")
	}
}
