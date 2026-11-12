package session

import "testing"

// TestNoteEmptyState 无计划文本与结构化头（items 省略）。
func TestNoteEmptyState(t *testing.T) {
	n := NewNoteTracker()
	got := n.Execute("")
	want := `{"ok":true,"tool":"Note","state":"empty"}` + "\n" + `[Note] 暂无计划。用 action=set + content=任务1\n任务2 来创建。`
	if got != want {
		t.Fatalf("空计划回执不符:\n实得 %q\n期望 %q", got, want)
	}
}

// TestNoteSetAndAdvance set 写入 / 推进 / 末条提示 / 全完成。
func TestNoteSetAndAdvance(t *testing.T) {
	n := NewNoteTracker()
	got := n.Execute(`{"action":"set","content":["a","b"]}`)
	want := `{"ok":true,"tool":"Note","items":2,"state":"progress","index":1,"done":0,"remain":2,"last":false}` +
		"\n" + "[Note] 第1/2条  已完成0  待完成2\n任务目标：a"
	if got != want {
		t.Fatalf("set 回执不符:\n实得 %q\n期望 %q", got, want)
	}
	got = n.Execute("")
	want = `{"ok":true,"tool":"Note","items":2,"state":"progress","index":2,"done":1,"remain":1,"last":true}` +
		"\n" + "[Note] 第2/2条  已完成1  待完成1\n任务目标：b（已是最后一条需求，完成后可结束本轮）"
	if got != want {
		t.Fatalf("推进回执不符:\n实得 %q\n期望 %q", got, want)
	}
	got = n.Execute("")
	want = `{"ok":true,"tool":"Note","items":2,"state":"done"}` + "\n" + "[Note] 🎉 全部 2 条任务已完成！"
	if got != want {
		t.Fatalf("完成回执不符:\n实得 %q\n期望 %q", got, want)
	}
	if tasks, _, _ := n.Snapshot(); len(tasks) != 0 {
		t.Fatalf("全完成后应清空内存态: %v", tasks)
	}
}

// TestNoteRejectOverwriteWithoutForce 未完成时无 force 拒绝覆盖并回报剩余条数。
func TestNoteRejectOverwriteWithoutForce(t *testing.T) {
	n := NewNoteTracker()
	n.Execute(`{"action":"set","content":["a","b"]}`)
	n.Execute("")
	got := n.Execute(`{"action":"set","content":["c"]}`)
	want := "[Note] ⚠️ 还有 1 条未完成。用 force=true 强制覆盖，或用 Note() 继续推进。"
	if got != want {
		t.Fatalf("拒绝覆盖回执不符: %q", got)
	}
	got = n.Execute(`{"action":"set","content":["c"],"force":true}`)
	if got == want {
		t.Fatal("force=true 应覆盖")
	}
	if tasks, _, _ := n.Snapshot(); len(tasks) != 1 || tasks[0] != "c" {
		t.Fatalf("覆盖后计划不符: %v", tasks)
	}
}

// TestNoteContentShape 形态校验（A129 零容忍）。
func TestNoteContentShape(t *testing.T) {
	n := NewNoteTracker()
	cases := []string{
		`{"action":"set","content":"a"}`,
		`{"action":"set","content":[]}`,
		`{"action":"set","content":["a",2]}`,
		`{"action":"set","content":["a","  "]}`,
	}
	prefix := "ERR|BAD_ARGS|Note content 形态非法——"
	for _, args := range cases {
		got := n.Execute(args)
		if len(got) <= len(prefix) || got[:len(prefix)] != prefix {
			t.Fatalf("形态非法应报错（%s）: %s", args, got)
		}
	}
	if got := n.Execute(`{"action":"set","content":"a"}`); got != prefix+"content 须为字符串数组（原生 JSON array）——收到 String；正确形态 content=[\"任务1\",\"任务2\"]" {
		t.Fatalf("非数组文案不符: %s", got)
	}
	if got := n.Execute(`{"action":"set","content":["a",2]}`); got != prefix+"content 第 2 项须为字符串——收到 Number；正确形态 content=[\"任务1\",\"任务2\"]" {
		t.Fatalf("元素类型文案不符: %s", got)
	}
}

// TestNoteBrokenJSONAdvances 参数 JSON 损坏按无参数推进（不抛）。
func TestNoteBrokenJSONAdvances(t *testing.T) {
	n := NewNoteTracker()
	n.Execute(`{"action":"set","content":["a","b"]}`)
	got := n.Execute(`{"action":"set",`)
	if got == "" || got[:len("ERR|")] == "ERR|" {
		t.Fatalf("损坏 JSON 应按无参推进: %s", got)
	}
	if _, current, done := n.Snapshot(); current != 1 || done != 1 {
		t.Fatalf("应推进一条: current=%d done=%d", current, done)
	}
}

// TestNoteStateJSON 状态段 note 字段形态。
func TestNoteStateJSON(t *testing.T) {
	n := NewNoteTracker()
	if got := n.BuildJSON(); got != `{"tasks":[],"current":0,"done":0,"justCompleted":false}` {
		t.Fatalf("空态 JSON 不符: %s", got)
	}
	n.Execute(`{"action":"set","content":["甲","乙"]}`)
	if got := n.BuildJSON(); got != `{"tasks":["甲","乙"],"current":0,"done":0,"justCompleted":false}` {
		t.Fatalf("计划态 JSON 不符: %s", got)
	}
	n.Execute("")
	n.Execute("")
	if got := n.BuildJSON(); got != `{"tasks":[],"current":0,"done":0,"justCompleted":true}` {
		t.Fatalf("完成态 JSON 不符: %s", got)
	}
}

// TestNoteRestore 落盘恢复（索引越界夹取）。
func TestNoteRestore(t *testing.T) {
	n := NewNoteTracker()
	n.Restore([]string{"a", "b"}, 9, 1)
	if _, current, _ := n.Snapshot(); current != 1 {
		t.Fatalf("索引应夹取到末条: %d", current)
	}
	n.Restore(nil, 0, 0)
	if got := n.BuildJSON(); got != `{"tasks":["a","b"],"current":1,"done":1,"justCompleted":false}` {
		t.Fatalf("空计划不应改内存态（C# 同口径：Tasks 空即返回）: %s", got)
	}
}
