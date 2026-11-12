package observe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLogRingCapacityTail 环形容量与尾部截取。
func TestLogRingCapacityTail(t *testing.T) {
	s := NewLogStore(3)
	for i := 1; i <= 5; i++ {
		s.Add("M", LevelInfo, "m"+itoa(i), "SYS", "", "", 0, int64(i), true)
	}
	all := s.Snapshot(0)
	if len(all) != 3 {
		t.Fatalf("环形应只留 3 条，实际 %d", len(all))
	}
	if all[0].Message != "m3" || all[2].Message != "m5" {
		t.Fatalf("环形内容 = %q..%q", all[0].Message, all[2].Message)
	}
	tail := s.Snapshot(2)
	if len(tail) != 2 || tail[0].Message != "m4" || tail[1].Message != "m5" {
		t.Fatalf("tail=2 = %+v", tail)
	}
}

// TestLogSnapshotFromCursor 增量游标：已丢弃区间收敛到最旧条目，末端游标不回退。
func TestLogSnapshotFromCursor(t *testing.T) {
	s := NewLogStore(3)
	for i := 1; i <= 5; i++ {
		s.Add("M", LevelInfo, "m"+itoa(i), "SYS", "", "", 0, int64(i), true)
	}
	got, end := s.SnapshotFrom(0)
	if end != 5 || len(got) != 3 || got[0].Message != "m3" {
		t.Fatalf("from=0 → end=%d len=%d first=%q", end, len(got), got[0].Message)
	}
	got, end = s.SnapshotFrom(4)
	if end != 5 || len(got) != 1 || got[0].Message != "m5" {
		t.Fatalf("from=4 → end=%d len=%d", end, len(got))
	}
	got, end = s.SnapshotFrom(5)
	if end != 5 || len(got) != 0 {
		t.Fatalf("from=5 → end=%d len=%d", end, len(got))
	}
}

// TestLogsArrayJSONFields 快照 logs 段六字段 + trace 审计过滤。
func TestLogsArrayJSONFields(t *testing.T) {
	s := NewLogStore(8)
	s.Add("Mod", LevelWarn, "hello", "CMD", "audit.cmd.set", "", 0, 7, true)
	s.Add("Mod", LevelInfo, "trace", "SYS", "audit.flow.trace.state", "", 0, 8, true)
	got := s.LogsArrayJSON(10)
	want := `[{"time":`
	if !strings.HasPrefix(got, want) {
		t.Fatalf("logs 数组 = %s", got)
	}
	if strings.Count(got, `"time"`) != 1 {
		t.Fatalf("trace 审计应被过滤，实际 = %s", got)
	}
	for _, field := range []string{`"frame":7`, `"level":"WARN"`, `"category":"CMD"`, `"module":"Mod"`, `"message":"hello"`} {
		if !strings.Contains(got, field) {
			t.Fatalf("缺字段 %s → %s", field, got)
		}
	}
	if strings.Contains(got, `"type"`) {
		t.Fatalf("快照 logs 段不应含 type 字段 → %s", got)
	}
}

// TestLogsResponseJSONFilter /api/v1/logs 七字段 + cat 精确 + type 前缀 + 时间序。
func TestLogsResponseJSONFilter(t *testing.T) {
	s := NewLogStore(16)
	s.Add("A", LevelInfo, "first", "OA", "audit.oa.post", "", 0, 1, true)
	s.Add("B", LevelErr, "second", "OA", "audit.oa.claim", "", 0, 2, true)
	s.Add("C", LevelInfo, "third", "SYS", "audit.oa.post", "", 0, 3, true)
	got := s.LogsResponseJSON(10, "OA", "audit.oa.")
	if !strings.HasPrefix(got, `{"version":1,"logs":[`) {
		t.Fatalf("响应头 = %s", got)
	}
	if !strings.Contains(got, `"type":"audit.oa.post"`) || !strings.Contains(got, `"type":"audit.oa.claim"`) {
		t.Fatalf("type 字段缺失 = %s", got)
	}
	if strings.Contains(got, `"third"`) {
		t.Fatalf("cat=SYS 不应命中 OA 过滤 = %s", got)
	}
	firstIdx := strings.Index(got, `"first"`)
	secondIdx := strings.Index(got, `"second"`)
	if firstIdx < 0 || secondIdx < 0 || firstIdx > secondIdx {
		t.Fatalf("应按时间序（旧→新）= %s", got)
	}
}

// TestFormatLine 行格式（含 payload k=v 摘要与截断）。
func TestFormatLine(t *testing.T) {
	entry := Entry{Time: "2026-11-11 10:00:00", Frame: 42, Level: LevelErr, Category: "OA",
		Module: "M", Message: "boom", Type: "audit.oa.settle", Payload: `[{"k":"officeId","v":"7"}]`}
	got := FormatLine(entry)
	want := "2026-11-11 10:00:00 | F42 | ERR | [OA] | boom | #audit.oa.settle | officeId=7"
	if got != want {
		t.Fatalf("行 = %q\n期望 %q", got, want)
	}
	// 非 k/v 载荷回落原文截断
	long := strings.Repeat("x", 200)
	entry.Payload = long
	got = FormatLine(entry)
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("长载荷应截断 = %q", got[len(got)-20:])
	}
}

// TestLogProjections 三文件投影：log_all 全量 / oa_all 只 OA / err_all 只 ERR。
func TestLogProjections(t *testing.T) {
	dir := t.TempDir()
	s := NewLogStore(16)
	if errText := s.ConfigureRuns(dir); errText != "" {
		t.Fatalf("ConfigureRuns 失败: %s", errText)
	}
	defer s.Close()
	s.Add("M", LevelInfo, "plain", "SYS", "", "", 0, 1, false)
	s.Add("M", LevelWarn, "oa-line", "OA", "", "", 0, 2, false)
	s.Add("M", LevelErr, "err-line", "SYS", "", "", 0, 3, false)
	s.Add("M", LevelErr, "skip", "SYS", "", "", 0, 4, true) // skipDisk 不落盘

	logAll := readTestFile(t, filepath.Join(dir, "log_all.txt"))
	if !strings.Contains(logAll, "plain") || !strings.Contains(logAll, "oa-line") || !strings.Contains(logAll, "err-line") {
		t.Fatalf("log_all 缺行 = %s", logAll)
	}
	if strings.Contains(logAll, "skip") {
		t.Fatalf("skipDisk 行不应落盘 = %s", logAll)
	}
	oaAll := readTestFile(t, filepath.Join(dir, "oa_all.txt"))
	if !strings.Contains(oaAll, "oa-line") || strings.Contains(oaAll, "plain") {
		t.Fatalf("oa_all = %s", oaAll)
	}
	errAll := readTestFile(t, filepath.Join(dir, "err_all.txt"))
	if !strings.Contains(errAll, "err-line") || strings.Contains(errAll, "oa-line") {
		t.Fatalf("err_all = %s", errAll)
	}
}

// readTestFile 读测试文件（失败即停）。
func readTestFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	return string(raw)
}
