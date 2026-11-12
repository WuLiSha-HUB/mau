package observe

import (
	"strings"
	"testing"
)

// TestBootReportDedup 同 Code + Ability 累加次数，不重复列条。
func TestBootReportDedup(t *testing.T) {
	r := NewBootRegistry()
	r.Report("ROOT_UNREACHABLE", KindEnv, "受控根", StateFallback, "detail-1", "fix-1", "S2")
	r.Report("ROOT_UNREACHABLE", KindEnv, "受控根", StateFallback, "detail-2", "fix-2", "S2")
	r.Report("CFG_CORRUPT", KindData, "配置面", StateEmpty, "detail-3", "fix-3", "S1")
	if r.Count() != 2 {
		t.Fatalf("条目数 = %d，期望 2", r.Count())
	}
	entries := r.Snapshot()
	if entries[0].Count != 2 {
		t.Fatalf("去重 count = %d，期望 2", entries[0].Count)
	}
	if entries[0].Detail != "detail-1" {
		t.Fatalf("详情应保留首条，实际 = %q", entries[0].Detail)
	}
	if got, want := r.BuildSummaryLine(), "启动降级 2 项：ROOT_UNREACHABLE×2, CFG_CORRUPT"; got != want {
		t.Fatalf("摘要 = %q，期望 %q", got, want)
	}
}

// TestBootSegment 零降级与有降级两态的段结构。
func TestBootSegment(t *testing.T) {
	r := NewBootRegistry()
	seg := r.Segment()
	if !seg.OK || seg.Count != 0 || seg.Summary != "" || len(seg.Degraded) != 0 {
		t.Fatalf("零降级段 = %+v", seg)
	}
	r.SetSink(`D:\data\runs\1`)
	r.SetReportPath(`D:\data\runs\1\startup_report.json`)
	r.Report("HTML_ROOT_MISSING", KindEnv, "外观层", StateEmpty, "html 根未定位", "用 --html-root 指定", "S6")
	seg = r.Segment()
	if seg.OK || seg.Count != 1 || seg.Sink == "" || seg.ReportPath == "" {
		t.Fatalf("有降级段 = %+v", seg)
	}
	if !strings.Contains(seg.Summary, "HTML_ROOT_MISSING") {
		t.Fatalf("摘要 = %q", seg.Summary)
	}
	if seg.Degraded[0].Kind != KindEnv || seg.Degraded[0].Stage != "S6" || seg.Degraded[0].Count != 1 {
		t.Fatalf("降级条目 = %+v", seg.Degraded[0])
	}
}
