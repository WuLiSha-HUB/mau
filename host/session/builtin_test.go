package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newBuiltinSession 建立接入落盘面的会话（pack / delay 数据面按用例单独接线）。
func newBuiltinSession(t *testing.T) *Session {
	t.Helper()
	dir := withSessionDirs(t)
	return newTestSession(t, dir, "cat-builtin")
}

// TestExecutePackArgs pack 参数面——缺 key / 未知参数 / 非对象 / 参数损坏四条文案。
func TestExecutePackArgs(t *testing.T) {
	s := newBuiltinSession(t)
	cases := []struct{ args, want string }{
		{`{}`, "ERR|BAD_ARGS|缺少参数 key（pack 需要包 key）"},
		{`{"catId":"cat-builtin"}`, "ERR|BAD_ARGS|缺少参数 key（pack 需要包 key）"},
		{`{"foo":1}`, "ERR|BAD_ARGS|未知参数: foo（支持 key）"},
		{`[]`, "ERR|BAD_ARGS|参数必须是 JSON 对象"},
	}
	for _, item := range cases {
		if got := s.ExecuteBuiltin("pack", item.args); got != item.want {
			t.Fatalf("args=%s got=%q want=%q", item.args, got, item.want)
		}
	}
	if got := s.ExecuteBuiltin("pack", `{坏`); !strings.HasPrefix(got, "ERR|BAD_ARGS|参数 JSON 解析失败: ") {
		t.Fatalf("参数损坏文案不符: %s", got)
	}
}

// TestExecutePackNoProvider 数据面未接线——具名 ERR（不静默）。
func TestExecutePackNoProvider(t *testing.T) {
	s := newBuiltinSession(t)
	if got := s.ExecuteBuiltin("pack", `{"key":"overwork"}`); got != "ERR|PACK_NO_PROVIDER|加载包数据面未注入（宿主未接线）" {
		t.Fatalf("未接线文案不符: %s", got)
	}
}

// TestExecutePackPayloadError 数据面错误直返（三道门文案由 provider 产出）。
func TestExecutePackPayloadError(t *testing.T) {
	s := newBuiltinSession(t)
	s.SetPackProvider(func(catKey, key string) string {
		return `{"ok":false,"error":"ERR|PACK_KEY_NOT_FOUND|包不存在: x | 池内可用: (空池)"}`
	}, nil, nil)
	want := "ERR|PACK_KEY_NOT_FOUND|包不存在: x | 池内可用: (空池)"
	if got := s.ExecuteBuiltin("pack", `{"key":"x"}`); got != want {
		t.Fatalf("错误直返不符: %s", got)
	}
	s.SetPackProvider(func(catKey, key string) string { return "not-json" }, nil, nil)
	if got := s.ExecuteBuiltin("pack", `{"key":"x"}`); !strings.HasPrefix(got, "ERR|PACK_BAD_PAYLOAD|包数据面返回解析失败: ") {
		t.Fatalf("坏载荷文案不符: %s", got)
	}
}

// TestExecutePackSuccess pack 成功链——目录一级 *.md 升序展开 + 头 / 正文逐字。
func TestExecutePackSuccess(t *testing.T) {
	s := newBuiltinSession(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "b.md"), []byte("第二"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("第一"), 0o644)
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("忽略"), 0o644)
	s.SetPackProvider(
		func(catKey, key string) string {
			return `{"ok":true,"key":"overwork","desc":"测试包","paths":[` + jsonStr(dir) + `]}`
		},
		func(path string) (string, error) { return path, nil },
		func(absPath string) (string, error) {
			data, err := os.ReadFile(absPath)
			return string(data), err
		},
	)
	result := s.ExecuteBuiltin("pack", `{"key":"overwork"}`)
	if !strings.HasPrefix(result, `{"ok":true,"tool":"pack","target":"overwork","items":2,"chars":4}`) {
		t.Fatalf("pack 头不符: %s", result)
	}
	if !strings.Contains(result, "✅ PACK overwork | 测试包 | 2 件 / 4 字符") {
		t.Fatalf("正文摘要行不符: %s", result)
	}
	first := strings.Index(result, "===== "+dir+"/a.md =====\n第一")
	second := strings.Index(result, "===== "+dir+"/b.md =====\n第二")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("目录展开顺序 / 标签不符:\n%s", result)
	}
	if strings.Contains(result, "c.txt") {
		t.Fatalf("非 .md 文件不应展开: %s", result)
	}
}

// TestExecutePackAtomicReject 任一失败整包拒绝——文件不存在 / 目录内无 .md 两态。
func TestExecutePackAtomicReject(t *testing.T) {
	s := newBuiltinSession(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("第一"), 0o644)
	missing := filepath.Join(dir, "missing")
	emptyDir := t.TempDir()
	s.SetPackProvider(
		func(catKey, key string) string {
			return `{"ok":true,"key":"kit","desc":"","paths":[` + jsonStr(dir) + `,` + jsonStr(missing) + `,` + jsonStr(emptyDir) + `]}`
		},
		func(path string) (string, error) { return path, nil },
		func(absPath string) (string, error) {
			data, err := os.ReadFile(absPath)
			return string(data), err
		},
	)
	result := s.ExecuteBuiltin("pack", `{"key":"kit"}`)
	if !strings.HasPrefix(result, "ERR|PACK_LOAD_FAILED|kit 加载失败（整包未注入）:") {
		t.Fatalf("整包拒绝头不符: %s", result)
	}
	if !strings.Contains(result, missing+" | 文件不存在") {
		t.Fatalf("缺文件失败明细: %s", result)
	}
	if !strings.Contains(result, emptyDir+" | 目录内无 .md 文件") {
		t.Fatalf("空目录失败明细: %s", result)
	}
}

// TestExecuteSleepArgs sleep 参数面——未知 / 负值 / 非整数 / 时长区间。
func TestExecuteSleepArgs(t *testing.T) {
	s := newBuiltinSession(t)
	cases := []struct{ args, want string }{
		{`{"hours":1,"foo":2}`, "ERR|BAD_ARGS|未知参数: foo（支持 hours / minutes / seconds）"},
		{`{"seconds":-1}`, "ERR|BAD_ARGS|seconds 不能为负"},
		{`{"seconds":1.5}`, "ERR|BAD_ARGS|seconds 需为整数"},
		{`{}`, "ERR|BAD_ARGS|sleep 时长需在 1..99 秒之间（当前 0 秒；更长等待请用 timer）"},
		{`{"seconds":100}`, "ERR|BAD_ARGS|sleep 时长需在 1..99 秒之间（当前 100 秒；更长等待请用 timer）"},
	}
	for _, item := range cases {
		if got := s.ExecuteBuiltin("sleep", item.args); got != item.want {
			t.Fatalf("args=%s got=%q want=%q", item.args, got, item.want)
		}
	}
}

// TestExecuteSleepSuspendAndPump sleep 挂起 → 帧推进到点闭合（startBatch + pumpSleepWait 全链）。
func TestExecuteSleepSuspendAndPump(t *testing.T) {
	s := newBuiltinSession(t)
	s.SetBuiltinExecutor(func(name string) bool { return true }, s.ExecuteBuiltin)
	dog := &toolDog{ToolCallID: "c1", Name: "sleep", ArgsJSON: `{"seconds":1}`}
	r := s.roundState()
	r.mu.Lock()
	r.dogs = []*toolDog{dog}
	r.batches = [][]*toolDog{{dog}}
	r.mu.Unlock()
	s.startBatch(0)
	if dog.IsClosed {
		t.Fatalf("sleep 挂起时工具单不得闭合")
	}
	if dog.Result != sleepSuspendMark {
		t.Fatalf("挂起标记不符: %s", dog.Result)
	}
	r.mu.Lock()
	dueFrame := r.sleepDueFrame
	r.mu.Unlock()
	if dueFrame != 10 {
		t.Fatalf("等待帧数应为 10，实际 %d", dueFrame)
	}
	for i := int64(1); i <= dueFrame; i++ {
		r.mu.Lock()
		r.phaseFrames = i
		r.mu.Unlock()
		s.pumpSleepWait()
	}
	if !dog.IsClosed || dog.Result == sleepSuspendMark {
		t.Fatalf("到点应闭合并补结果: closed=%v result=%s", dog.IsClosed, dog.Result)
	}
	if !strings.HasPrefix(dog.Result, `{"ok":true,"tool":"sleep"`) || !strings.Contains(dog.Result, "等待 | 1 秒（10 帧）") {
		t.Fatalf("sleep 回执不符: %s", dog.Result)
	}
	r.mu.Lock()
	left := r.sleepDueFrame
	r.mu.Unlock()
	if left != -1 {
		t.Fatalf("到点后等待帧应复位 -1，实际 %d", left)
	}
}

// TestExecuteSleepDuplicate 同批只允许一个 sleep——第二条拒绝并给出剩余帧数。
func TestExecuteSleepDuplicate(t *testing.T) {
	s := newBuiltinSession(t)
	if got := s.ExecuteBuiltin("sleep", `{"seconds":1}`); got != sleepSuspendMark {
		t.Fatalf("首条应挂起: %s", got)
	}
	if got := s.ExecuteBuiltin("sleep", `{"seconds":2}`); got != "ERR|BAD_ARGS|本批已有 sleep 在等待——同批只允许一个（剩余 10 帧）" {
		t.Fatalf("重复文案不符: %s", got)
	}
}

// TestPumpSleepWaitTimeout 批超时先到——诚实 ERR 收口（挂起标记不得泄给 LLM）。
func TestPumpSleepWaitTimeout(t *testing.T) {
	s := newBuiltinSession(t)
	dog := &toolDog{ToolCallID: "c1", Name: "sleep", ArgsJSON: `{}`}
	r := s.roundState()
	r.mu.Lock()
	r.dogs = []*toolDog{dog}
	r.batches = [][]*toolDog{{dog}}
	r.batchIndex = 0
	r.sleepDueFrame = 2000
	r.sleepReply = "不应使用"
	r.phaseFrames = toolBatchWaitFrames
	r.mu.Unlock()
	s.pumpSleepWait()
	if !dog.IsClosed {
		t.Fatalf("批超时应闭合 sleep 单")
	}
	if !strings.HasPrefix(dog.Result, "ERR|SLEEP_TIMEOUT|sleep 等待未到点即遇工具批超时（已等 1500 帧 / 需要 2000 帧）") {
		t.Fatalf("超时文案不符: %s", dog.Result)
	}
}

// TestExecuteTimerArgs timer 参数面——内容必填 / 未知 / 布尔 / 时长区间。
func TestExecuteTimerArgs(t *testing.T) {
	s := newBuiltinSession(t)
	cases := []struct{ args, want string }{
		{`{"seconds":10}`, "ERR|BAD_ARGS|timer 内容为空（content 必填——到点注入的指令文本）"},
		{`{"content":"  ","seconds":10}`, "ERR|BAD_ARGS|timer 内容为空（content 必填——到点注入的指令文本）"},
		{`{"content":"x","foo":1}`, "ERR|BAD_ARGS|未知参数: foo（支持 content / hours / minutes / seconds / loop）"},
		{`{"content":"x","loop":"yes"}`, "ERR|BAD_ARGS|loop 需为布尔"},
		{`{"content":"x","seconds":0}`, "ERR|BAD_ARGS|timer 时长需在 1..86400 秒之间（当前 0 秒）"},
		{`{"content":"x","seconds":86401}`, "ERR|BAD_ARGS|timer 时长需在 1..86400 秒之间（当前 86401 秒）"},
	}
	for _, item := range cases {
		if got := s.ExecuteBuiltin("timer", item.args); got != item.want {
			t.Fatalf("args=%s got=%q want=%q", item.args, got, item.want)
		}
	}
}

// TestExecuteTimerSuccess timer 成功链——固定时钟下头字段 / 正文 / 队列条目逐字。
func TestExecuteTimerSuccess(t *testing.T) {
	s := newBuiltinSession(t)
	dq := NewDelayQueue("")
	dq.SetNowProvider(func() int64 { return 2_000_000_000 })
	s.SetDelayQueue(dq)
	got := s.ExecuteBuiltin("timer", `{"content":"巡检","seconds":10,"loop":true}`)
	if !strings.HasPrefix(got, `{"ok":true,"tool":"timer","hours":0,"minutes":0,"seconds":10,"loop":true,"dueAt":2000010000}`) {
		t.Fatalf("timer 头不符: %s", got)
	}
	if !strings.Contains(got, "排程 | ") || !strings.Contains(got, "（10 秒后 · 循环（每 10 秒））") {
		t.Fatalf("timer 正文不符: %s", got)
	}
	fragment := dq.BuildEntriesFragment("cat-builtin")
	if !strings.Contains(fragment, `"content":"巡检","dueAt":2000010000`) ||
		!strings.Contains(fragment, `"source":"timer","loop":true,"intervalMs":10000`) {
		t.Fatalf("timer 未入队: %s", fragment)
	}
}

// TestExecuteTimerNoQueue 队列未接线——具名 ERR（不静默）。
func TestExecuteTimerNoQueue(t *testing.T) {
	s := newBuiltinSession(t)
	if got := s.ExecuteBuiltin("timer", `{"content":"x","seconds":10}`); got != "ERR|DELAY_NOT_WIRED|延迟队列未接线（宿主装配缺失）" {
		t.Fatalf("未接线文案不符: %s", got)
	}
}
