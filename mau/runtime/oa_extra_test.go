package runtime

import (
	"strings"
	"testing"
)

// TestGetOfficeCopy 副本语义——GetOffice 返回副本，改副本不影响 OA 本体。
func TestGetOfficeCopy(t *testing.T) {
	oa := NewOA(nil)
	id := oa.Post(1, "TOOL", "text-read", 1200)
	_ = oa.SetStr(id, 1, "args", "payload")
	copyOffice := oa.GetOffice(id)
	copyOffice.Data["args"] = "改过"
	if value, _ := oa.GetStr(id, "args"); value != "payload" {
		t.Fatalf("副本修改污染了本体: %q", value)
	}
	// 不存在的工单返回 Timeout 哨兵
	missing := oa.GetOffice(999)
	if missing.Status != OfficeTimeout || missing.Data == nil || missing.Result == nil {
		t.Fatalf("缺失工单哨兵不符: %+v", missing)
	}
}

// TestCancelOpenOnly 取消语义——仅 Open + 本人；取消后工单不可再认领。
func TestCancelOpenOnly(t *testing.T) {
	oa := NewOA(nil)
	id := oa.Post(1, "TOOL", "text-read", 1200)
	if oa.Cancel(id, 2) {
		t.Fatal("非挂单方不应取消成功")
	}
	if !oa.Cancel(id, 1) {
		t.Fatal("挂单方取消失败")
	}
	if _, ok := oa.StatusOf(id); ok {
		t.Fatal("取消后工单应不存在")
	}
	id2 := oa.Post(1, "TOOL", "text-read", 1200)
	_ = oa.ClaimBatch(9, []int64{id2})
	if oa.Cancel(id2, 1) {
		t.Fatal("已认领工单不应可取消")
	}
}

// TestCompleteSummaryAndLog Complete 的审计摘要与四态日志——ints:0,strs:N + 五处出声。
func TestCompleteSummaryAndLog(t *testing.T) {
	trace := NewTrace()
	oa := NewOA(trace)
	var logs []string
	oa.Log = func(level int, message string) { logs = append(logs, message) }

	id := oa.Post(1, "TOOL", "text-read", 3)
	_ = oa.SetStr(id, 1, "args", `{"path":"a"}`)
	_ = oa.ClaimBatch(1, []int64{id})
	oa.Complete(id, 1, map[string]string{"result": "回执"})

	summary := ""
	for _, ev := range trace.Find("oa.complete") {
		for _, prop := range ev.Props {
			if prop.Key == "result" {
				summary = prop.Value
			}
		}
	}
	if summary != "ints:0,strs:1" {
		t.Fatalf("审计摘要不符: %q", summary)
	}
	joined := strings.Join(logs, "\n")
	for _, want := range []string{"已提交", "载荷 args", "认领", "已完成"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("日志缺 %q: %s", want, joined)
		}
	}

	// 超时结算出声——时限 1 帧
	timeoutID := oa.Post(1, "TOOL", "file-tree", 1)
	oa.Tick()
	oa.Tick()
	if state, _ := oa.StatusOf(timeoutID); state != OfficeTimeout {
		t.Fatalf("超时未结算: %v", state)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "超时结算") {
		t.Fatalf("超时日志缺失: %v", logs)
	}
}
