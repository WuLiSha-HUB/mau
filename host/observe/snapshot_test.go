package observe

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSnapshotFields 全量快照字段面（协议 v1.2 十二字段）与空数组字面量。
func TestSnapshotFields(t *testing.T) {
	frame := &FrameCounter{}
	frame.Next()
	b := &Builder{
		Frame: frame.Current,
		OA:    func() (int, int, int, int) { return 1, 2, 3, 4 },
	}
	raw := b.Build(false)
	if !strings.Contains(raw, `"logs":[]`) {
		t.Fatalf("空日志段必须是字面量 [](ReplaceLogsSection 依赖) = %s", raw)
	}
	var snap map[string]any
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		t.Fatalf("快照不是合法 JSON: %v\n%s", err, raw)
	}
	if v, ok := snap["version"].(float64); !ok || v != 3 {
		t.Fatalf("version = %v", snap["version"])
	}
	if pid, ok := snap["pid"].(float64); !ok || pid <= 0 {
		t.Fatalf("pid = %v", snap["pid"])
	}
	if f, ok := snap["frame"].(float64); !ok || f != 1 {
		t.Fatalf("frame = %v", snap["frame"])
	}
	for _, key := range []string{"cats", "sessions", "tools", "boxes", "deps", "logs"} {
		arr, ok := snap[key].([]any)
		if !ok {
			t.Fatalf("%s 不是数组：%T %v", key, snap[key], snap[key])
		}
		if len(arr) != 0 {
			t.Fatalf("%s 期望空数组，实际 %v", key, arr)
		}
	}
	oa, ok := snap["oa"].(map[string]any)
	if !ok || oa["open"] != float64(1) || oa["work"] != float64(2) || oa["closed"] != float64(3) || oa["timeout"] != float64(4) {
		t.Fatalf("oa = %v", snap["oa"])
	}
	boot, ok := snap["boot"].(map[string]any)
	if !ok || boot["ok"] != true || boot["count"] != float64(0) {
		t.Fatalf("boot = %v", snap["boot"])
	}
	if degraded, ok := boot["degraded"].([]any); !ok || len(degraded) != 0 {
		t.Fatalf("boot.degraded = %v", boot["degraded"])
	}
	res, ok := snap["res"].(map[string]any)
	if !ok {
		t.Fatalf("res 段缺失 = %v", snap["res"])
	}
	for _, key := range []string{"t", "proc", "py", "node", "gpu", "loop"} {
		if _, ok := res[key]; !ok {
			t.Fatalf("res.%s 缺失 = %v", key, res)
		}
	}
	if _, ok := snap["res"].(map[string]any)["proc"].(map[string]any)["ws"]; !ok {
		t.Fatalf("res.proc.ws 缺失 = %v", res["proc"])
	}
	if _, ok := snap["res"].(map[string]any)["gpu"].(map[string]any)["available"]; !ok {
		t.Fatalf("res.gpu.available 缺失 = %v", res["gpu"])
	}
}

// TestSnapshotLogsAndBoot includeLogs 分支（50 条上限）与 boot 段降级条目。
func TestSnapshotLogsAndBoot(t *testing.T) {
	logs := NewLogStore(128)
	for i := 0; i < 60; i++ {
		logs.Add("M", LevelInfo, "m"+itoa(i), "SYS", "", "", 0, int64(i), true)
	}
	boot := NewBootRegistry()
	boot.Report("CFG_CORRUPT", KindData, "配置面", StateFallback, "坏档", "修", "S1")
	b := &Builder{Logs: logs, Boot: boot}
	raw := b.Build(true)
	var snap map[string]any
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		t.Fatalf("快照 JSON 非法: %v", err)
	}
	items := snap["logs"].([]any)
	if len(items) != 50 {
		t.Fatalf("logs 上限应为 50，实际 %d", len(items))
	}
	first := items[0].(map[string]any)
	for _, key := range []string{"time", "frame", "level", "category", "module", "message"} {
		if _, ok := first[key]; !ok {
			t.Fatalf("logs[0] 缺字段 %s = %v", key, first)
		}
	}
	bootSeg := snap["boot"].(map[string]any)
	if bootSeg["ok"] != false || bootSeg["count"] != float64(1) {
		t.Fatalf("boot 段 = %v", bootSeg)
	}
	if !strings.Contains(bootSeg["summary"].(string), "CFG_CORRUPT") {
		t.Fatalf("boot.summary = %v", bootSeg["summary"])
	}
}

// TestSnapshotNilBuilder 全 nil 构建器不 panic 且字段面齐备。
func TestSnapshotNilBuilder(t *testing.T) {
	b := &Builder{}
	raw := b.Build(false)
	var snap map[string]any
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		t.Fatalf("JSON 非法: %v", err)
	}
	if snap["frame"] != float64(0) {
		t.Fatalf("nil 帧源应为 0，实际 %v", snap["frame"])
	}
	if _, ok := snap["boot"].(map[string]any); !ok {
		t.Fatalf("nil Boot 也应有 boot 段 = %v", snap["boot"])
	}
}

// TestFrameCounter 帧号单调递增。
func TestFrameCounter(t *testing.T) {
	f := &FrameCounter{}
	if f.Current() != 0 {
		t.Fatalf("初始帧 = %d", f.Current())
	}
	if got := f.Next(); got != 1 {
		t.Fatalf("Next = %d", got)
	}
	if got := f.Current(); got != 1 {
		t.Fatalf("Current = %d", got)
	}
}
