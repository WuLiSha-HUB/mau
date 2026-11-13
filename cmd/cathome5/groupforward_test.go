package main

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"cathome5/host/group"
)

// TestGroupForwardBuildSummary 汇总正文——组名 / 区间头 / 类型标签 / 箭头 / 关联 / 换行压平 / 空发方。
func TestGroupForwardBuildSummary(t *testing.T) {
	svc, _, _, _ := newGroupTestService(t)
	f := &groupForwarder{svc: svc}
	batch := []group.Message{
		{Seq: 3, From: "majordomo", Type: "note", Text: "第一条"},
		{Seq: 4, From: "majordomo", To: "worker", Type: "delegation", Text: "多行\n正文", Refs: "r1"},
		{Seq: 5, From: "", Type: "receipt", Text: "回执"},
	}
	want := "[来自组:g1]\n" +
		"（订阅汇总 · 未读 3 条 · #3–#5）" +
		"\n#3 [发言] 管家猫 · 第一条" +
		"\n#4 [派单] 管家猫 → @worker · 多行 正文（关联 r1）" +
		"\n#5 [回执] 人 · 回执"
	if got := f.buildSummary("g1", batch); got != want {
		t.Fatalf("汇总正文不符：\n got=%q\nwant=%q", got, want)
	}
}

// TestGroupForwardPush 静默窗口到期——注入一条 user 消息（origin 第 4 段 auto）并推进游标。
func TestGroupForwardPush(t *testing.T) {
	svc, router, store, _ := newGroupTestService(t)
	if result := store.CreateOrUpdate("g1", []string{"majordomo", "worker"}, "p1", []string{"majordomo"}, []string{"majordomo"}, 60); !strings.HasPrefix(result, "ok|") {
		t.Fatalf("改订阅失败：%s", result)
	}
	if result := store.AppendMessage("g1", "majordomo", "note", "第一条", "", ""); !strings.HasPrefix(result, "ok|") {
		t.Fatalf("追加失败：%s", result)
	}
	if result := store.AppendMessage("g1", "worker", "note", "第二条", "", ""); !strings.HasPrefix(result, "ok|") {
		t.Fatalf("追加失败：%s", result)
	}
	info := store.GetGroup("g1")
	f := &groupForwarder{svc: svc, now: func() int64 { return info.LastAt + 61*1000 }}
	f.Pump()
	msgs := router.Session.Pending.TakeAll(true)
	if len(msgs) != 1 {
		t.Fatalf("应注入 1 条，实得 %d", len(msgs))
	}
	if msgs[0].Source != "user" {
		t.Fatalf("source 应为 user：%q", msgs[0].Source)
	}
	if msgs[0].Origin != "group:g1:2:auto" {
		t.Fatalf("origin 不符：%q", msgs[0].Origin)
	}
	if !strings.Contains(msgs[0].Content, "（订阅汇总 · 未读 2 条 · #1–#2）") {
		t.Fatalf("区间头不符：%q", msgs[0].Content)
	}
	if cursor := store.GetSubscriberCursor("g1", "majordomo"); cursor != 2 {
		t.Fatalf("游标应推进到 2，实得 %d", cursor)
	}
}

// TestGroupForwardIdleWindow 静默窗口未到期——不注入、不推进游标。
func TestGroupForwardIdleWindow(t *testing.T) {
	svc, router, store, _ := newGroupTestService(t)
	if result := store.CreateOrUpdate("g1", []string{"majordomo", "worker"}, "p1", []string{"majordomo"}, []string{"majordomo"}, 60); !strings.HasPrefix(result, "ok|") {
		t.Fatalf("改订阅失败：%s", result)
	}
	if result := store.AppendMessage("g1", "majordomo", "note", "第一条", "", ""); !strings.HasPrefix(result, "ok|") {
		t.Fatalf("追加失败：%s", result)
	}
	info := store.GetGroup("g1")
	f := &groupForwarder{svc: svc, now: func() int64 { return info.LastAt + 10*1000 }}
	f.Pump()
	if msgs := router.Session.Pending.TakeAll(true); len(msgs) != 0 {
		t.Fatalf("窗口未到期不应注入，实得 %d 条", len(msgs))
	}
	if cursor := store.GetSubscriberCursor("g1", "majordomo"); cursor != 0 {
		t.Fatalf("窗口未到期不应推进游标，实得 %d", cursor)
	}
}

// TestGroupForwardSubscriberOffline 订阅猫未运行——出声跳过且游标不推进（下次窗口重试）。
func TestGroupForwardSubscriberOffline(t *testing.T) {
	svc, _, store, _ := newGroupTestService(t)
	if result := store.CreateOrUpdate("g1", []string{"majordomo", "worker"}, "p1", []string{"majordomo"}, []string{"worker"}, 60); !strings.HasPrefix(result, "ok|") {
		t.Fatalf("改订阅失败：%s", result)
	}
	if result := store.AppendMessage("g1", "majordomo", "note", "第一条", "", ""); !strings.HasPrefix(result, "ok|") {
		t.Fatalf("追加失败：%s", result)
	}
	logged := []string{}
	svc.Logf = func(level int, message, category string) {
		logged = append(logged, strconv.Itoa(level)+"|"+category+"|"+message)
	}
	info := store.GetGroup("g1")
	f := &groupForwarder{svc: svc, now: func() int64 { return info.LastAt + 61*1000 }}
	f.Pump()
	if cursor := store.GetSubscriberCursor("g1", "worker"); cursor != 0 {
		t.Fatalf("订阅猫未运行不应推进游标，实得 %d", cursor)
	}
	found := false
	for _, line := range logged {
		if strings.Contains(line, "订阅推送跳过（订阅猫未运行）: g1 | worker") && strings.HasPrefix(line, "2|GROUP|") {
			found = true
		}
	}
	if !found {
		t.Fatalf("应出声跳过（WARN / GROUP），实得日志：%v", logged)
	}
}

// TestGroupForwardBatchCapAndThrottle 单批上限 100（剩余留待下一窗口）+ 1 秒节流。
func TestGroupForwardBatchCapAndThrottle(t *testing.T) {
	svc, router, store, _ := newGroupTestService(t)
	if result := store.CreateOrUpdate("g1", []string{"majordomo", "worker"}, "p1", []string{"majordomo"}, []string{"majordomo"}, 60); !strings.HasPrefix(result, "ok|") {
		t.Fatalf("改订阅失败：%s", result)
	}
	for i := 0; i < 105; i++ {
		if result := store.AppendMessage("g1", "majordomo", "note", "m"+strconv.Itoa(i), "", ""); !strings.HasPrefix(result, "ok|") {
			t.Fatalf("追加失败：%s", result)
		}
	}
	info := store.GetGroup("g1")
	f := &groupForwarder{svc: svc, now: func() int64 { return info.LastAt + 61*1000 }}
	f.Pump()
	msgs := router.Session.Pending.TakeAll(true)
	if len(msgs) != 1 {
		t.Fatalf("应注入 1 条汇总，实得 %d", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "（订阅汇总 · 未读 100 条 · #1–#100）") {
		t.Fatalf("单批上限 100 不符：%q", msgs[0].Content)
	}
	if cursor := store.GetSubscriberCursor("g1", "majordomo"); cursor != 100 {
		t.Fatalf("游标应停在 100，实得 %d", cursor)
	}
	// 1 秒节流——紧接着的第二帧被挡（游标不动）
	f.Pump()
	if cursor := store.GetSubscriberCursor("g1", "majordomo"); cursor != 100 {
		t.Fatalf("节流窗口内不应推进，实得 %d", cursor)
	}
	// 节流解除后推剩余 5 条
	f.lastPumpAt = time.Time{}
	f.Pump()
	msgs = router.Session.Pending.TakeAll(true)
	if len(msgs) != 1 || !strings.Contains(msgs[0].Content, "（订阅汇总 · 未读 5 条 · #101–#105）") {
		t.Fatalf("剩余区间应推 5 条：%v", msgs)
	}
	if cursor := store.GetSubscriberCursor("g1", "majordomo"); cursor != 105 {
		t.Fatalf("游标应推进到 105，实得 %d", cursor)
	}
}
