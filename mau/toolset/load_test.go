package toolset

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

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
