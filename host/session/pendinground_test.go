package session

import (
	"path/filepath"
	"testing"
)

// TestPendingRoundLifecycle 轮级续跑现场——轮首落盘 / attempts 上限 1 / 轮末清除。
func TestPendingRoundLifecycle(t *testing.T) {
	dir := t.TempDir()
	PendingRoundPathProvider = func(catID string) string {
		return filepath.Join(dir, catID+".pending-round.json")
	}
	t.Cleanup(func() { PendingRoundPathProvider = nil })

	sess, _ := newRoundTestSession(t)
	if _, ok := LoadPendingRound(sess.CatKey); ok {
		t.Fatal("未落盘时不应有现场")
	}
	sess.MarkRoundPending()
	info, ok := LoadPendingRound(sess.CatKey)
	if !ok {
		t.Fatal("轮首应落盘")
	}
	if info.CatKey != sess.CatKey || info.Attempts != 0 || info.Phase != chatPhaseIdle {
		t.Fatalf("现场字段不符: %+v", info)
	}
	if info.StartedAt == 0 || info.UpdatedAt == 0 {
		t.Fatalf("时间戳未写入: %+v", info)
	}
	// 续跑一次 → attempts 落盘为 1
	resumeInfo, ok := sess.TakePendingRoundResume()
	if !ok || resumeInfo.Attempts != 1 {
		t.Fatalf("首次续跑应成功: ok=%v info=%+v", ok, resumeInfo)
	}
	if saved, ok := LoadPendingRound(sess.CatKey); !ok || saved.Attempts != 1 {
		t.Fatalf("attempts 应已落盘: ok=%v info=%+v", ok, saved)
	}
	// 第二次 → 上限 1 拒绝
	if _, ok := sess.TakePendingRoundResume(); ok {
		t.Fatal("attempts 上限 1——第二次应拒绝")
	}
	// 清除 + 幂等
	sess.ClearPendingRound()
	if _, ok := LoadPendingRound(sess.CatKey); ok {
		t.Fatal("轮末应清除现场")
	}
	sess.ClearPendingRound()
}

// TestPendingRoundNoProvider 未接线 = 不落盘且不 panic（单测 / 无数据根形态）。
func TestPendingRoundNoProvider(t *testing.T) {
	PendingRoundPathProvider = nil
	t.Cleanup(func() { PendingRoundPathProvider = nil })
	sess, _ := newRoundTestSession(t)
	sess.MarkRoundPending()
	if _, ok := LoadPendingRound("cat"); ok {
		t.Fatal("未接线不应有现场")
	}
	sess.ClearPendingRound()
}

// TestPendingRoundPhaseName 相位名——已知四态给中文名，未知值给序号（不造语义）。
func TestPendingRoundPhaseName(t *testing.T) {
	cases := map[int]string{
		chatPhaseIdle:             "空闲",
		chatPhaseLlmRunning:       "LLM 请求中",
		chatPhaseToolBatchRunning: "工具批执行中",
		chatPhaseDone:             "轮末收尾",
		9:                         "相位 9",
	}
	for phase, want := range cases {
		if got := PendingRoundPhaseName(phase); got != want {
			t.Fatalf("相位 %d 名称 = %q，期望 %q", phase, got, want)
		}
	}
}
