package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tbHarness 是 timeback 测试夹具——内存前文 + 捕获落盘 + 捕获系统注入。
type tbHarness struct {
	tb       *Timeback
	msgs     []Message
	persists int
	posted   []string
}

// newTBHarness 建立夹具（归档目录为空则不建归档实例）。
func newTBHarness(t *testing.T, archiveDir string) *tbHarness {
	t.Helper()
	h := &tbHarness{msgs: nil}
	h.tb = NewTimeback("tb-session")
	h.tb.Messages = func() []Message { return h.msgs }
	h.tb.ReplaceMessages = func(next []Message) { h.msgs = next }
	h.tb.Persist = func(next []Message) { h.persists++ }
	h.tb.Tokens = func() int64 { return 0 }
	h.tb.PostSystem = func(kind, content, imagesJSON string) string {
		h.posted = append(h.posted, kind+"|"+content)
		return "OK"
	}
	h.tb.InfoSnapshot = func() string { return "快照:tb-session" }
	if archiveDir != "" {
		h.tb.Archive = NewTimebackArchive(archiveDir)
	}
	return h
}

// user 构造人工消息。
func user(text string, ts int64) Message {
	return Message{Role: RoleUser, Content: text, CreatedAt: ts}
}

// decl 构造 assistant 工具调用声明。
func decl(toolName string, ts int64) Message {
	return Message{Role: RoleAssistant, ToolCallsJSON: `[{"id":"x","function":{"name":"` + toolName + `","arguments":"{}"}}]`, CreatedAt: ts}
}

// toolResult 构造工具结果。
func toolResult(name, content string, ts int64) Message {
	return Message{Role: RoleTool, ToolName: name, Content: content, ToolCallID: "x", CreatedAt: ts}
}

// TestTimebackStartShape start 回执只留结构化头（规范迁入域规范卡）。
func TestTimebackStartShape(t *testing.T) {
	h := newTBHarness(t, "")
	h.msgs = []Message{user("开始取证", 1), decl("timeback-start", 2)}
	got := h.tb.Start(TimebackTextSearch, "跨轮取证")
	want := `{"ok":true,"tool":"timeback-start","target":"0","anchor":1,"type":"text_search","purpose":"跨轮取证"}`
	if got != want {
		t.Fatalf("start 回执不符:\n实得 %s\n期望 %s", got, want)
	}
	if !h.tb.Active() {
		t.Fatal("start 后应活跃")
	}
	if got := h.tb.Start(TimebackTextSearch, "再来一次"); got != "ERR|TIMEBACK_NESTED|已有未闭合作用域 #0——先 back 再 start" {
		t.Fatalf("嵌套 start 回执不符: %s", got)
	}
}

// TestTimebackStartValidation 参数面校验。
func TestTimebackStartValidation(t *testing.T) {
	h := newTBHarness(t, "")
	h.msgs = []Message{user("x", 1), decl("timeback-start", 2)}
	if got := h.tb.ExecuteStart(`{"type":"bogus","purpose":"p"}`); got != "ERR|TIMEBACK_ARGS|start 需要合法 type（域类型）: browser_vision | image_vision | text_search | code_review | code_write——收到「bogus」" {
		t.Fatalf("非法类型回执不符: %s", got)
	}
	if got := h.tb.ExecuteStart(`{"type":"text_search"}`); got != "ERR|TIMEBACK_ARGS|start 需要 purpose（用途标签）" {
		t.Fatalf("缺 purpose 回执不符: %s", got)
	}
	if got := h.tb.ExecuteStart(`{"type":"text_search","purpose":"p","extra":1}`); got != "ERR|TIMEBACK_ARGS|未知参数: extra（支持 type / purpose）" {
		t.Fatalf("未知参数回执不符: %s", got)
	}
	if got := h.tb.ExecuteStart(`{"type":1,"purpose":"p"}`); got != "ERR|TIMEBACK_ARGS|type 需为字符串" {
		t.Fatalf("type 非字符串回执不符: %s", got)
	}
	h.msgs = nil
	if got := h.tb.ExecuteStart(`{"type":"text_search","purpose":"p"}`); got != "ERR|TIMEBACK_ANCHOR|前文为空——无锚点可记" {
		t.Fatalf("空前文回执不符: %s", got)
	}
}

// TestTimebackBackDeletesRange 跨轮取证——区间删除后只留两次调用对与结论。
func TestTimebackBackDeletesRange(t *testing.T) {
	dir := t.TempDir()
	h := newTBHarness(t, dir)
	h.msgs = []Message{user("开始取证", 1), decl("timeback-start", 2)}
	h.tb.Start(TimebackTextSearch, "跨轮取证")
	// start 结果 + 区间内：域规范卡（系统注入）+ info 调用对
	h.msgs = append(h.msgs,
		toolResult("timeback-start", `{"ok":true}`, 3),
		Message{Role: RoleUser, Content: SysInjectPrefix + "域规范卡", CreatedAt: 4},
		decl("info", 5),
		toolResult("info", "info 结果", 6),
	)
	backDecl := decl("timeback-back", 7)
	h.msgs = append(h.msgs, backDecl)
	got := h.tb.Back("结论：目录 85 个")
	if !strings.Contains(got, `"items":3`) {
		t.Fatalf("释放条数预算应为 3（卡 + info 调用对）: %s", got)
	}
	if !strings.Contains(got, `"tokens":0`) || !strings.Contains(got, `"grew":0`) {
		t.Fatalf("前文长度快照字段缺失: %s", got)
	}
	if !strings.Contains(got, "📉 释放 3 条前文") {
		t.Fatalf("情报行缺失: %s", got)
	}
	if !strings.Contains(got, "🔴 回读规范（收域第一步）") {
		t.Fatalf("回读规范缺失: %s", got)
	}
	// 结果回填后追加 back 结果，再执行批后回收
	h.msgs = append(h.msgs, toolResult("timeback-back", got, 8))
	removed := h.tb.Apply()
	if removed != 3 {
		t.Fatalf("实际删除应为 3，实得 %d", removed)
	}
	if len(h.msgs) != 5 {
		t.Fatalf("回收后条数应为 5（user + 两次调用对），实得 %d", len(h.msgs))
	}
	// 两次调用声明保留
	if h.msgs[1].ToolCallsJSON == "" || h.msgs[3].ToolCallsJSON == "" {
		t.Fatalf("两次调用声明应保留: %+v", h.msgs)
	}
	if h.tb.Active() {
		t.Fatal("回收后作用域应关闭")
	}
	// 归档文件落盘——首行 meta + info 快照 + 3 条被删消息
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var archiveName string
	for _, entry := range files {
		if strings.HasSuffix(entry.Name(), ".jsonl") {
			archiveName = entry.Name()
		}
	}
	if archiveName == "" {
		t.Fatal("应写出归档文件")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, archiveName))
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 1+1+3 {
		t.Fatalf("归档行数应为 5，实得 %d: %s", len(lines), string(raw))
	}
	if !strings.Contains(lines[0], `"t":"timeback"`) || !strings.Contains(lines[0], `"n":3`) {
		t.Fatalf("首行 meta 不符: %s", lines[0])
	}
	if lines[1] != "快照:tb-session" {
		t.Fatalf("次行 info 快照不符: %s", lines[1])
	}
	for i := 2; i < len(lines); i++ {
		if !strings.Contains(lines[i], `"t":"m"`) {
			t.Fatalf("被删消息行应有 t=m 标记: %s", lines[i])
		}
	}
}

// TestTimebackBackKeepsHumanMessages 域内人工输入保留；系统注入随域删除；预算扣掉保留条数。
func TestTimebackBackKeepsHumanMessages(t *testing.T) {
	h := newTBHarness(t, "")
	h.msgs = []Message{user("开始取证", 1), decl("timeback-start", 2)}
	h.tb.Start(TimebackTextSearch, "域内插话")
	h.msgs = append(h.msgs,
		toolResult("timeback-start", `{"ok":true}`, 3),
		Message{Role: RoleUser, Content: SysInjectPrefix + "域规范卡", CreatedAt: 4},
		decl("text-read", 5),
		toolResult("text-read", "读到的东西", 6),
		user("这里再看一眼", 7),
	)
	h.msgs = append(h.msgs, decl("timeback-back", 8))
	got := h.tb.Back("结论：甲")
	if !strings.Contains(got, `"items":3`) {
		t.Fatalf("预算应扣掉保留的人工输入（4 - 1）: %s", got)
	}
	h.msgs = append(h.msgs, toolResult("timeback-back", got, 9))
	removed := h.tb.Apply()
	if removed != 3 {
		t.Fatalf("实际删除应为 3，实得 %d", removed)
	}
	// [user 开始][start 声明][start 结果][人工输入][back 声明][back 结果]
	if len(h.msgs) != 6 {
		t.Fatalf("回收后条数应为 6，实得 %d", len(h.msgs))
	}
	if h.msgs[3].Content != "这里再看一眼" {
		t.Fatalf("人工输入应保留且落在 back 声明之前: %+v", h.msgs[3])
	}
	if h.msgs[4].ToolCallsJSON == "" {
		t.Fatalf("back 声明位置不符: %+v", h.msgs)
	}
}

// TestTimebackBackClearsBackReasoning back 声明思考换占位句；start 声明思考保留。
func TestTimebackBackClearsBackReasoning(t *testing.T) {
	h := newTBHarness(t, "")
	h.msgs = []Message{user("开始", 1)}
	startDecl := decl("timeback-start", 2)
	startDecl.ReasoningContent = "我需要开域"
	h.msgs = append(h.msgs, startDecl)
	h.tb.Start(TimebackTextSearch, "p")
	backDecl := decl("timeback-back", 3)
	backDecl.ReasoningContent = "域内断言"
	h.msgs = append(h.msgs, backDecl)
	h.tb.Back("结论")
	// 区间为空（同批开收）——走就地净化路径
	if removed := h.tb.Apply(); removed != 0 {
		t.Fatalf("区间为空不应删除，实得 %d", removed)
	}
	if h.msgs[1].ReasoningContent != "我需要开域" {
		t.Fatalf("start 声明思考应保留: %q", h.msgs[1].ReasoningContent)
	}
	if h.msgs[2].ReasoningContent != TimebackBackReasoningPlaceholder {
		t.Fatalf("back 声明思考应净化: %q", h.msgs[2].ReasoningContent)
	}
}

// TestTimebackErrors back 参数面与无域回执。
func TestTimebackErrors(t *testing.T) {
	h := newTBHarness(t, "")
	if got := h.tb.Back("x"); got != "ERR|TIMEBACK_NO_SCOPE|无未闭合作用域——back 无对象" {
		t.Fatalf("无域回执不符: %s", got)
	}
	h.msgs = []Message{user("x", 1), decl("timeback-start", 2)}
	h.tb.Start(TimebackCodeWrite, "p")
	if got := h.tb.Back(""); got != "ERR|TIMEBACK_ARGS|back 需要 findings（带回载荷）" {
		t.Fatalf("空 findings 回执不符: %s", got)
	}
	if got := h.tb.ExecuteBack(`{"findings":"f","x":1}`); got != "ERR|TIMEBACK_ARGS|未知参数: x（支持 findings）" {
		t.Fatalf("未知参数回执不符: %s", got)
	}
	if got := h.tb.ExecuteBack(`{"findings":2}`); got != "ERR|TIMEBACK_ARGS|findings 需为字符串" {
		t.Fatalf("findings 非字符串回执不符: %s", got)
	}
}

// TestTimebackToolStatus 台账状态列四态 + 明细。
func TestTimebackToolStatus(t *testing.T) {
	cases := map[string]string{
		"":                          "OK",
		"ERR|TIMEBACK_PROFILE|不在名单": "DENY",
		"ERR|TIMEBACK_LOCKED|x":     "DENY",
		"ERR|TOOL_FORBIDDEN|x":      "DENY",
		"ERR|BAD_PATH|x":            "ERR",
		"ROLLED_BACK":               "ERR",
		`{"ok":true,"tool":"x"}`:    "OK",
		`{"ok":false,"exit":1}`:     "FAIL(exit=1)",
		`{"ok":false}`:              "FAIL",
		"没有头":                       "OK",
		`{"tool":"x"}`:              "UNPARSED",
	}
	for input, want := range cases {
		if got := TimebackToolStatus(input); got != want {
			t.Fatalf("状态列不符（%q）: %s，期望 %s", input, got, want)
		}
	}
}

// TestTimebackWriteTarget 台账目标标识。
func TestTimebackWriteTarget(t *testing.T) {
	if got := TimebackWriteTarget(`{"path":"a/b/c.go"}`); got != "c.go" {
		t.Fatalf("路径应取末段: %s", got)
	}
	if got := TimebackWriteTarget(`{"class":"Foo","member":"Bar"}`); got != "Foo" {
		t.Fatalf("标识类原样: %s", got)
	}
	if got := TimebackWriteTarget(`{"command":"Test-Path ccbp:L1"}`); got != "Test-Path ccbp:L1" {
		t.Fatalf("powershell 取原文: %s", got)
	}
	if got := TimebackWriteTarget(`{}`); got != "—" {
		t.Fatalf("取不到应写破折号: %s", got)
	}
}

// TestTimebackWrites 台账内联与截断指针。
func TestTimebackWrites(t *testing.T) {
	h := newTBHarness(t, "")
	h.msgs = []Message{user("x", 1), decl("timeback-start", 2)}
	h.tb.Start(TimebackCodeWrite, "p")
	for i := 0; i < 22; i++ {
		h.tb.NoteWrite("text-read", `{"path":"a/b.go"}`, "")
	}
	h.tb.NoteWrite("timeback-start", "{}", "")
	if len(h.tb.Scope().WriteLog) != 22 {
		t.Fatalf("围栏两件不应入账: %d", len(h.tb.Scope().WriteLog))
	}
	text := h.tb.buildWrites()
	if !strings.Contains(text, "[本域工具台账 · 宿主记录 · 22 条 · 含只读与被拒]") {
		t.Fatalf("台账头不符: %s", text)
	}
	if !strings.Contains(text, "…（余 2 条见归档") {
		t.Fatalf("超限应给归档指针: %s", text)
	}
}

// TestTimebackNotice 25 事件状态自述。
func TestTimebackNotice(t *testing.T) {
	h := newTBHarness(t, "")
	h.msgs = []Message{user("x", 1), decl("timeback-start", 2)}
	h.tb.Start(TimebackTextSearch, "p")
	for i := 0; i < 24; i++ {
		h.tb.NoteEvent()
	}
	h.tb.FlushNotice()
	if len(h.posted) != 0 {
		t.Fatalf("未满 25 事件不应注入: %v", h.posted)
	}
	h.tb.NoteEvent()
	h.tb.FlushNotice()
	if len(h.posted) != 1 || !strings.Contains(h.posted[0], "已经历【25】条前文条目") {
		t.Fatalf("满 25 事件应注入一条: %v", h.posted)
	}
}

// TestTimebackCard 域规范卡五段 + 两 type 追加段。
func TestTimebackCard(t *testing.T) {
	card := TimebackCard(TimebackBrowserVision, "网页取证", 7, 12)
	for _, want := range []string{
		"（系统自动 · timeback #7 域规范）域已开：browser_vision · 用途「网页取证」· 锚点 12",
		"① 本域边界：网页取证",
		"② 域内可用：browser-open · browser-read",
		"③ 域内不可用：Note · sleep · timer（域内锁定）",
		"④ 回执骨架",
		"⑤ 建议长度：本域建议 ≤100 条前文条目",
		"⑥ 浏览器操作（本域专属）",
	} {
		if !strings.Contains(card, want) {
			t.Fatalf("卡缺段 %q:\n%s", want, card)
		}
	}
	if strings.Contains(card, "⑦ 看图操作") {
		t.Fatal("浏览器域不应带识图段")
	}
	if !strings.Contains(TimebackCard(TimebackImageVision, "p", 1, 2), "⑦ 看图操作（本域专属）") {
		t.Fatal("识图域应带识图段")
	}
}

// TestTimebackProfile 白名单 fail-closed 与正交面。
func TestTimebackProfile(t *testing.T) {
	if !TimebackAllows(TimebackCodeWrite, "cs-build") || !TimebackAllows(TimebackCodeWrite, "text-write") {
		t.Fatal("code_write 应含 cs-build / text-write")
	}
	if TimebackAllows(TimebackCodeReview, "cs-build") {
		t.Fatal("code_review 不含跑测面")
	}
	if TimebackAllows(TimebackTextSearch, "image-inject") || TimebackAllows(TimebackBrowserVision, "image-inject") {
		t.Fatal("识图两件只属 image_vision")
	}
	if TimebackAllows("bogus", "text-read") {
		t.Fatal("未知名应全拒（fail-closed）")
	}
	if !TimebackNeedsBrowser(TimebackBrowserVision) || TimebackNeedsBrowser(TimebackImageVision) {
		t.Fatal("预热判据不符")
	}
	if TimebackAllows(TimebackTextSearch, "") {
		t.Fatal("空工具名应拒")
	}
}

// TestTimebackCheckTool 门禁三段判定与回执文案。
func TestTimebackCheckTool(t *testing.T) {
	h := newTBHarness(t, "")
	// 域外：域专属工具被拒
	if got := h.tb.CheckTool("browser-open"); got != "ERR|TIMEBACK_REQUIRED|browser-open 仅在 timeback 作用域内可用（非主干信息——用完回收）——先 start 开锚" {
		t.Fatalf("域外专属回执不符: %s", got)
	}
	if got := h.tb.CheckTool("image-inject"); !strings.HasPrefix(got, "ERR|TIMEBACK_REQUIRED|") {
		t.Fatalf("image-inject 域外应拒: %s", got)
	}
	if got := h.tb.CheckTool("browser-headful"); got != "" {
		t.Fatalf("browser-headful 是域外工具，应放行: %s", got)
	}
	h.msgs = []Message{user("x", 1), decl("timeback-start", 2)}
	h.tb.Start(TimebackTextSearch, "p")
	// 域内：名单外工具被拒（fail-closed）
	if got := h.tb.CheckTool("cs-build"); got != "ERR|TIMEBACK_PROFILE|cs-build 不在本域类型「text_search」的白名单内——域类型决定域内可用工具面；需用则先 back 回收、回主干调用" {
		t.Fatalf("名单外回执不符: %s", got)
	}
	if got := h.tb.CheckTool("text-read"); got != "" {
		t.Fatalf("名单内应放行: %s", got)
	}
	if got := h.tb.CheckTool("Note"); got != "" {
		t.Fatalf("Note 不归白名单管辖（交锁定面）: %s", got)
	}
	if got := h.tb.CheckTool("host-reload"); !strings.HasPrefix(got, "ERR|TIMEBACK_LOCKED|") {
		t.Fatalf("域内暴毙操作应拒: %s", got)
	}
	if got := h.tb.CheckBuiltinTool("sleep"); got != "ERR|TIMEBACK_LOCKED|timeback 作用域内 sleep 不可用——先 back 回收再调用" {
		t.Fatalf("内置锁定回执不符: %s", got)
	}
}

// TestTimebackArchiveCount 全局计数跨实例递增 + 读面最近 meta。
func TestTimebackArchiveCount(t *testing.T) {
	dir := t.TempDir()
	a := NewTimebackArchive(dir)
	if got := a.NextID(); got != 1 {
		t.Fatalf("首号应为 1，实得 %d", got)
	}
	b := NewTimebackArchive(dir)
	if got := b.NextID(); got != 2 {
		t.Fatalf("跨实例应续号，实得 %d", got)
	}
	record := &TimebackScopeRecord{ID: 2, CatKey: "tb", Type: "text_search", Purpose: "p", BackAt: 100, N: 1, Released: 1, Findings: "结论"}
	if _, ok := b.WriteScopeFile(record, "", []Message{user("被删", 9)}); !ok {
		t.Fatal("归档应写成功")
	}
	recent := b.ReadRecentMeta(5)
	if len(recent) != 1 || !strings.Contains(recent[0], `"id":2`) {
		t.Fatalf("读面不符: %v", recent)
	}
}
