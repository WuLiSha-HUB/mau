package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDelayQueueAddFails 登记失败面——缺归属 / 空内容 / 非法时刻三条文案逐字。
func TestDelayQueueAddFails(t *testing.T) {
	q := NewDelayQueue("")
	cases := []struct {
		name    string
		catKey  string
		content string
		dueAt   int64
		want    string
	}{
		{"缺归属", "", "内容", 1000, "ERR|DELAY_NO_CAT|延迟条目缺少归属会话"},
		{"空内容", "cat", "", 1000, "ERR|BAD_ARGS|延迟条目内容为空"},
		{"非法时刻", "cat", "内容", 0, "ERR|BAD_ARGS|延迟条目触发时刻非法"},
	}
	for _, item := range cases {
		if got := q.Add(item.catKey, item.content, "timer", item.dueAt, 0, false); got != item.want {
			t.Fatalf("%s: got %q want %q", item.name, got, item.want)
		}
	}
}

// TestDelayQueueAddSuccess 登记成功——回执 JSON 键序与字段逐字；条目片段八字段。
func TestDelayQueueAddSuccess(t *testing.T) {
	q := NewDelayQueue("")
	q.SetNowProvider(func() int64 { return 1_800_000_000_000 })
	got := q.Add("majordomo", "巡检", "timer", 1_800_000_600_000, 600_000, true)
	wantPrefix := `{"ok":true,"id":1,"dueAt":1800000600000,"loop":true,"due":"`
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("回执键序 / 字段错误: %s", got)
	}
	if !strings.HasSuffix(got, `"}`) {
		t.Fatalf("回执未闭合: %s", got)
	}
	fragment := q.BuildEntriesFragment("majordomo")
	for _, want := range []string{
		`{"id":1,"content":"巡检","dueAt":1800000600000,"createdAt":1800000000000,`,
		`"source":"timer","loop":true,"intervalMs":600000,"fired":0}`,
	} {
		if !strings.Contains(fragment, want) {
			t.Fatalf("条目片段缺 %q: %s", want, fragment)
		}
	}
	if empty := q.BuildEntriesFragment("other"); empty != "[]" {
		t.Fatalf("他猫过滤应为空数组: %s", empty)
	}
}

// TestDelayQueueLimit 单会话上限 50——第 51 条拒绝并出声。
func TestDelayQueueLimit(t *testing.T) {
	q := NewDelayQueue("")
	for i := 0; i < DelayMaxPerSession; i++ {
		if got := q.Add("cat", "内容", "timer", int64(i+1)*1000, 0, false); strings.HasPrefix(got, "ERR|") {
			t.Fatalf("第 %d 条不应被拒: %s", i+1, got)
		}
	}
	got := q.Add("cat", "内容", "timer", 999999, 0, false)
	if got != "ERR|DELAY_LIMIT|本会话待触发条目已达上限 50（可取消后再登记）" {
		t.Fatalf("上限文案不符: %s", got)
	}
}

// TestDelayQueuePump 到点投递 / 未受理保留 / loop 重排三态。
func TestDelayQueuePump(t *testing.T) {
	q := NewDelayQueue("")
	now := int64(1_000_000)
	q.SetNowProvider(func() int64 { return now })
	q.Add("cat", "一次性", "timer", 1_000_500, 0, false)
	q.Add("cat", "循环", "timer", 1_000_500, 300_000, true)
	q.Add("cat", "未到点", "timer", 2_000_000, 0, false)

	// 未到点——零投递
	if delivered := q.Pump(func(string, string, string) bool { return true }); delivered != 0 {
		t.Fatalf("未到点不应投递: %d", delivered)
	}
	now = 1_000_500
	var got []string
	reject := true
	delivered := q.Pump(func(catKey, content, source string) bool {
		if reject && content == "一次性" {
			return false // 首次未受理——保留重投
		}
		got = append(got, content)
		return true
	})
	if delivered != 1 || len(got) != 1 || got[0] != "循环" {
		t.Fatalf("首次泵应只投递循环条: delivered=%d got=%v", delivered, got)
	}
	// 未受理的一次性条保留；循环条已按「投递时刻 + 时长」重排
	reject = false
	now = 1_000_600
	delivered = q.Pump(func(catKey, content, source string) bool {
		got = append(got, content)
		return true
	})
	if delivered != 1 || got[len(got)-1] != "一次性" {
		t.Fatalf("重投一次性条失败: delivered=%d got=%v", delivered, got)
	}
	fragment := q.BuildEntriesFragment("cat")
	if !strings.Contains(fragment, `"content":"循环","dueAt":1300500`) || !strings.Contains(fragment, `"fired":1`) {
		t.Fatalf("循环条未按投递时刻重排: %s", fragment)
	}
	if !strings.Contains(fragment, `"content":"未到点"`) {
		t.Fatalf("未到点条目不应丢失: %s", fragment)
	}
}

// TestDelayQueueLoadSave 落盘与加载往返——条目 / 序号续接 / 坏文件按空表。
func TestDelayQueueLoadSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "delays.json")
	q := NewDelayQueue(path)
	q.SetNowProvider(func() int64 { return 1_000_000 })
	q.Add("cat", "第一条", "timer", 1_500_000, 0, false)
	q.Add("cat", "第二条", "delay", 1_600_000, 60_000, true)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("落盘失败: %v", err)
	}
	if !strings.Contains(string(raw), `"version":1`) || !strings.Contains(string(raw), `"nextId":3`) {
		t.Fatalf("落盘头字段不符: %s", raw)
	}

	restored := NewDelayQueue(path)
	restored.Load()
	fragment := restored.BuildEntriesFragment("cat")
	if !strings.Contains(fragment, "第一条") || !strings.Contains(fragment, "第二条") {
		t.Fatalf("加载丢条目: %s", fragment)
	}
	// 序号续接——新条目 ID = 3
	got := restored.Add("cat", "第三条", "timer", 1_700_000, 0, false)
	if !strings.Contains(got, `"id":3`) {
		t.Fatalf("序号未续接: %s", got)
	}

	broken := filepath.Join(dir, "broken.json")
	os.WriteFile(broken, []byte("{坏"), 0o644)
	q2 := NewDelayQueue(broken)
	q2.Load()
	if fragment := q2.BuildEntriesFragment("cat"); fragment != "[]" {
		t.Fatalf("坏文件应按空表处理: %s", fragment)
	}
}

// TestDelayQueueNilSafe nil 队列片段为空数组（状态段兜底路径）。
func TestDelayQueueNilSafe(t *testing.T) {
	var q *DelayQueue
	if got := q.BuildEntriesFragment("cat"); got != "[]" {
		t.Fatalf("nil 队列应返回空数组: %s", got)
	}
}

// TestDelayQueueEditOps 改时刻 / 循环开关 / 取消三面——越权 / 不存在 / 非法时刻逐字。
func TestDelayQueueEditOps(t *testing.T) {
	q := NewDelayQueue("")
	q.SetNowProvider(func() int64 { return 1_000_000 })
	q.Add("cat", "条目", "delay", 1_500_000, 60_000, false)
	if got := q.SetDueAt("cat", 1, 1_700_000); got != "ok|#1 due="+FormatDelayTime(1_700_000) {
		t.Fatalf("改时刻回执不符: %q", got)
	}
	if got := q.SetDueAt("cat", 1, 0); got != "ERR|BAD_ARGS|触发时刻非法" {
		t.Fatalf("非法时刻文案不符: %q", got)
	}
	if got := q.SetDueAt("other", 1, 1_800_000); got != "ERR|DELAY_NOT_FOUND|条目不属于本会话: #1" {
		t.Fatalf("越权文案不符: %q", got)
	}
	if got := q.SetDueAt("cat", 99, 1_800_000); got != "ERR|DELAY_NOT_FOUND|条目不存在: #99" {
		t.Fatalf("不存在文案不符: %q", got)
	}
	if got := q.SetLoop("cat", 1, true); got != "ok|#1 循环开" {
		t.Fatalf("循环开回执不符: %q", got)
	}
	if got := q.SetLoop("cat", 1, false); got != "ok|#1 循环关" {
		t.Fatalf("循环关回执不符: %q", got)
	}
	if got := q.Cancel("cat", 1); got != "ok|#1 已取消" {
		t.Fatalf("取消回执不符: %q", got)
	}
	if got := q.Cancel("cat", 1); got != "ERR|DELAY_NOT_FOUND|条目不存在: #1" {
		t.Fatalf("重复取消文案不符: %q", got)
	}
}

// TestDelayQueueSetLoopNeedsInterval 无循环时长的条目不允许开 loop（关 loop 不校验）。
func TestDelayQueueSetLoopNeedsInterval(t *testing.T) {
	q := NewDelayQueue("")
	q.Add("cat", "一次性", "delay", 1_500_000, 0, false)
	if got := q.SetLoop("cat", 1, true); got != "ERR|BAD_ARGS|条目缺少循环时长（重新登记时给定时长）: #1" {
		t.Fatalf("缺时长文案不符: %q", got)
	}
	if got := q.SetLoop("cat", 1, false); got != "ok|#1 循环关" {
		t.Fatalf("关 loop 回执不符: %q", got)
	}
}

// TestDelayQueueListText delay.list 文本——空态 / 条目行 / 循环标 / 他猫隔离。
func TestDelayQueueListText(t *testing.T) {
	q := NewDelayQueue("")
	if got := q.BuildListText("cat"); got != "延迟队列：空（本会话无待触发条目）" {
		t.Fatalf("空态文案不符: %q", got)
	}
	q.SetNowProvider(func() int64 { return 1_000_000 })
	q.Add("cat", "巡检", "delay", 1_600_000, 60_000, false)
	want := "延迟队列：1 条（本会话）\n  #1 | " + FormatDelayTime(1_600_000) + " | delay | 巡检"
	if got := q.BuildListText("cat"); got != want {
		t.Fatalf("条目行不符:\ngot  %q\nwant %q", got, want)
	}
	if got := q.SetLoop("cat", 1, true); got != "ok|#1 循环开" {
		t.Fatalf("循环开回执不符: %q", got)
	}
	wantLoop := "延迟队列：1 条（本会话）\n  #1 | " + FormatDelayTime(1_600_000) + " | delay 🔁 | 巡检"
	if got := q.BuildListText("cat"); got != wantLoop {
		t.Fatalf("循环标不符:\ngot  %q\nwant %q", got, wantLoop)
	}
	if got := q.BuildListText("other"); got != "延迟队列：空（本会话无待触发条目）" {
		t.Fatalf("他猫应隔离: %q", got)
	}
}
