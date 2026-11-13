package toolset

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cathome5/mau/bricks"
	"cathome5/mau/fs"
	"cathome5/mau/runtime"
)

// corpusDirFlag 是真实语料目录（`go test ./mau/toolset -args -corpus <dir>` 传入）。
var corpusDirFlag = flag.String("corpus", "", "真实语料目录（TextCat/FileCat 加载验收用）")

// goodCorpus 是最小可执行语料——一条 text-read 线（claim / args / exec / reply / abort）。
const goodCorpus = `§ 'S_Read' = { 'Idle', 'Claimed', 'Ready', 'Done', 'Failed' }
§ 'P_ReadOpen' ↻ [1]: 'oa.is_open'["TOOL", "text-read"] > @rdOpen
§ 'T_ReadClaim' : @rdOpen & 'S_Read' = 'Idle' → 'oa.claim_next_simple'["TOOL", "text-read"] > @rdOffice | 'S_Read' = 'Claimed' | 'S_Read' = 'Idle'
§ 'T_ReadArgs' : 'S_Read' = 'Claimed' → 'oa.get_str'[@rdOffice, "args"] > @rdArgs | 'self.desc'["text-read 执行中", "", ""] | 'S_Read' = 'Ready' | 'S_Read' = 'Failed'
§ 'T_ReadExec' [t=1200, par] : 'S_Read' = 'Ready' → 'text.read'[@rdArgs] > @rdRes | 'S_Read' = 'Done' | 'S_Read' = 'Failed'
§ 'T_ReadReply' : 'S_Read' = 'Done' → 'oa.complete_str'[@rdOffice, @rdRes] | 'self.desc'["空闲待命", "", ""] | 'S_Read' = 'Idle' | 'S_Read' = 'Idle'
§ 'T_ReadAbort' : 'S_Read' = 'Failed' → 'oa.complete_str'[@rdOffice, @rdRes] | 'S_Read' = 'Idle' | 'S_Read' = 'Idle'
`

// badCorpus 引用不存在的积木——应在加载期跳过。
const badCorpus = `§ 'S_X' = { 'Idle' }
§ 'T_X' : 'S_X' = 'Idle' → 'nope.missing'["a"] | 'S_X' = 'Idle' | 'S_X' = 'Idle'
`

// writeGroup 写一个语料组到临时目录。
func writeGroup(t *testing.T, root, dir, name, body string) {
	t.Helper()
	target := filepath.Join(root, dir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, name), []byte(body), 0o644); err != nil {
		t.Fatalf("写语料失败: %v", err)
	}
}

// TestLoadGroupsGate 组加载门禁——积木全齐才注册；缺积木 / 无料目录逐条跳过并出声。
func TestLoadGroupsGate(t *testing.T) {
	root := t.TempDir()
	writeGroup(t, root, "GoodCat", "good_cat.mau", goodCorpus)
	writeGroup(t, root, "BadCat", "bad_cat.mau", badCorpus)
	if err := os.MkdirAll(filepath.Join(root, "EmptyCat"), 0o755); err != nil {
		t.Fatalf("建空目录失败: %v", err)
	}
	set := BuildBrickSet(&fs.Service{}, runtime.NewOA(nil), runtime.NewDataBox(), BrickOptions{})
	var logs []string
	groups, skipped := LoadGroups(root, "", set, func(level int, message string) {
		logs = append(logs, message)
	})
	if len(groups) != 1 || groups[0].Name != "GoodCat" {
		t.Fatalf("应只注册 GoodCat: groups=%+v skipped=%v logs=%v", groups, skipped, logs)
	}
	if len(skipped) != 2 {
		t.Fatalf("跳过项不符: %v", skipped)
	}
	if len(logs) < 2 {
		t.Fatalf("跳过未出声: %v", logs)
	}
}

// TestLoadGroupsRealCorpus 真实语料加载——TextCat / FileCat 零诊断且注册成功（需 CAT5_CORPUS_DIR）。
func TestLoadGroupsRealCorpus(t *testing.T) {
	corpus := *corpusDirFlag
	if corpus == "" {
		corpus = os.Getenv("CAT5_CORPUS_DIR")
	}
	if corpus == "" {
		t.Skip("未设 CAT5_CORPUS_DIR / -corpus——跳过真实语料加载")
	}
	index := filepath.Join(filepath.Dir(filepath.Dir(corpus)), "Bricks", "index.json")
	set := BuildBrickSet(&fs.Service{}, runtime.NewOA(nil), runtime.NewDataBox(), BrickOptions{})
	groups, skipped := LoadGroups(corpus, index, set, func(level int, message string) {
		t.Logf("L%d %s", level, message)
	})
	found := map[string]bool{}
	for _, group := range groups {
		found[group.Name] = true
	}
	for _, want := range []string{"TextCat", "FileCat"} {
		if !found[want] {
			t.Fatalf("组 %s 未注册（跳过项 %v）", want, skipped)
		}
	}
}

// TestSnakeCase 目录名转蛇形。
func TestSnakeCase(t *testing.T) {
	cases := map[string]string{
		"TextCat":          "text_cat",
		"FileCat":          "file_cat",
		"LLMSimpleRequest": "llm_simple_request",
	}
	for input, want := range cases {
		if got := snakeCase(input); got != want {
			t.Fatalf("snakeCase(%q)=%q，期望 %q", input, got, want)
		}
	}
}

// toolsetStreamStep 是接线测试的流式事件一步。
type toolsetStreamStep struct {
	kind bricks.LLMStreamKind
	text string
}

// toolsetStreamRuntime 是 llm.stream 接线测试的假执行面——按脚本回放后返回。
type toolsetStreamRuntime struct {
	events []toolsetStreamStep
}

// ChatStream 回放脚本。
func (f *toolsetStreamRuntime) ChatStream(system, content, sessionID string, onEvent func(kind bricks.LLMStreamKind, text string)) {
	for _, step := range f.events {
		onEvent(step.kind, step.text)
	}
}

// TestIncrementBricksWiring 增量积木接线——注册闭包可执行（llm 流式五件 / probe 三件 / log.write 出口）。
//
// 与 bricks 包单测的分工：单测验语义，本测试验「BuildBrickSet → 闭包 → 服务 / 日志出口」这条接线。
func TestIncrementBricksWiring(t *testing.T) {
	db := runtime.NewDataBox()
	var logLines []string
	set := BuildBrickSet(&fs.Service{}, runtime.NewOA(nil), db, BrickOptions{
		LLMStream: &toolsetStreamRuntime{events: []toolsetStreamStep{
			{kind: bricks.LLMStreamText, text: "hi"},
			{kind: bricks.LLMStreamDone},
		}},
		Log: func(level int, message, category string) {
			logLines = append(logLines, category+"|"+message)
		},
	})
	// probe 三件——单值主端口
	if value, ok := set["probe.source"]([]string{"5"}); !ok || value != "probe-5" {
		t.Fatalf("probe.source 不符: %q/%v", value, ok)
	}
	if value, ok := set["probe.sink"]([]string{"abc", "3"}); !ok || value != "abc:3" {
		t.Fatalf("probe.sink 不符: %q/%v", value, ok)
	}
	if value, ok := set["probe.sink_slow"]([]string{"x", "0"}); !ok || value != "x" {
		t.Fatalf("probe.sink_slow 不符: %q/%v", value, ok)
	}
	// log.write 出口——BrickOptions.Log 已接线（非 nil 即成功侧）
	if value, ok := set["log.write"]([]string{"日志正文", "1", "TEST"}); !ok || value != "true" {
		t.Fatalf("log.write 不符: %q/%v", value, ok)
	}
	if len(logLines) != 1 || logLines[0] != "TEST|日志正文" {
		t.Fatalf("日志出口未收到: %#v", logLines)
	}
	// llm 流式五件——当前 flow 置 11 后走全链（启动 → 结算 → 取数）
	oldFlow := runtime.CurrentFlowID
	runtime.CurrentFlowID = 11
	defer func() { runtime.CurrentFlowID = oldFlow }()
	if value, ok := set["llm.stream"]([]string{"sys", "content"}); !ok || value != "" {
		t.Fatalf("llm.stream 不符: %q/%v", value, ok)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if value, _ := set["llm.done_ready"](nil); value == "true" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("llm.done_ready 未置位（2s）")
		}
		time.Sleep(time.Millisecond)
	}
	if value, ok := set["llm.chunk_ready"](nil); !ok || value != "true" {
		t.Fatalf("llm.chunk_ready 不符: %q/%v", value, ok)
	}
	if value, ok := set["llm.chunk_read"](nil); !ok || value != "hi" {
		t.Fatalf("llm.chunk_read 不符: %q/%v", value, ok)
	}
	if value, ok := set["llm.reply_read"](nil); !ok || value != "hi" {
		t.Fatalf("llm.reply_read 不符: %q/%v", value, ok)
	}
}

// toolsetGroupStub 是 GroupService 的接线测试桩——记录最后调用的方法，回固定成功回执。
type toolsetGroupStub struct {
	// method 最近一次调用的方法名
	method string
}

// Info 记录 group.info。
func (s *toolsetGroupStub) Info(argsJSON string) string {
	s.method = "Info"
	return `{"ok":true,"tool":"group-info"}`
}

// Inbox 记录 group.inbox。
func (s *toolsetGroupStub) Inbox(argsJSON string) string {
	s.method = "Inbox"
	return `{"ok":true,"tool":"group-inbox"}`
}

// Say 记录 group.say。
func (s *toolsetGroupStub) Say(argsJSON string) string {
	s.method = "Say"
	return `{"ok":true,"tool":"group-post"}`
}

// Send 记录 group.send。
func (s *toolsetGroupStub) Send(argsJSON string) string {
	s.method = "Send"
	return `{"ok":true,"tool":"group-delegate"}`
}

// NewSession 记录 group.new_session。
func (s *toolsetGroupStub) NewSession(argsJSON string) string {
	s.method = "NewSession"
	return `{"ok":true,"tool":"group-newsession"}`
}

// CatInfo 记录 group.catinfo。
func (s *toolsetGroupStub) CatInfo(argsJSON string) string {
	s.method = "CatInfo"
	return `{"ok":true,"tool":"group-catinfo"}`
}

// CatCfgGet 记录 group.catcfg_get。
func (s *toolsetGroupStub) CatCfgGet(argsJSON string) string {
	s.method = "CatCfgGet"
	return `{"ok":true,"tool":"group-catcfg-get"}`
}

// CatCfgSet 记录 group.catcfg_set。
func (s *toolsetGroupStub) CatCfgSet(argsJSON string) string {
	s.method = "CatCfgSet"
	return `{"ok":true,"tool":"group-catcfg-set"}`
}

// TestGroupBricksWiring GROUP 八件接线——BuildBrickSet 闭包 → 服务；nil 服务报 GROUP_NO_SERVICE。
//
// 与 bricks 包单测的分工：单测验薄壳语义，本测试验「BuildBrickSet → 闭包 → 服务」这条接线。
func TestGroupBricksWiring(t *testing.T) {
	names := []string{"group.info", "group.inbox", "group.say", "group.send", "group.new_session", "group.catinfo", "group.catcfg_get", "group.catcfg_set"}
	args := []string{`{"group":"g1","catId":"majordomo"}`}
	// nil 服务——八件统一 GROUP_NO_SERVICE（GroupCat 缺件时的诚实失败面）
	set := BuildBrickSet(&fs.Service{}, runtime.NewOA(nil), runtime.NewDataBox(), BrickOptions{})
	for _, name := range names {
		brick, exists := set[name]
		if !exists {
			t.Fatalf("%s 未注册", name)
		}
		if value, ok := brick(args); ok || value != "ERR|GROUP_NO_SERVICE|群组服务未注入（宿主未接线）" {
			t.Fatalf("%s 未注入服务应报 GROUP_NO_SERVICE：%q/%v", name, value, ok)
		}
	}
	// 注入服务——闭包按件透传到对应方法
	svc := &toolsetGroupStub{}
	set = BuildBrickSet(&fs.Service{}, runtime.NewOA(nil), runtime.NewDataBox(), BrickOptions{Group: svc})
	cases := map[string]string{
		"group.info":        "Info",
		"group.inbox":       "Inbox",
		"group.say":         "Say",
		"group.send":        "Send",
		"group.new_session": "NewSession",
		"group.catinfo":     "CatInfo",
		"group.catcfg_get":  "CatCfgGet",
		"group.catcfg_set":  "CatCfgSet",
	}
	for _, name := range names {
		brick := set[name]
		value, ok := brick(args)
		if !ok || !strings.HasPrefix(value, `{"ok":true`) {
			t.Fatalf("%s 成功回执不符：%q/%v", name, value, ok)
		}
		if svc.method != cases[name] {
			t.Fatalf("%s 应调 %s，实调 %s", name, cases[name], svc.method)
		}
	}
}
