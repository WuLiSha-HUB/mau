package bricks

import (
	"testing"
	"time"
)

// TestProbeSource 固定输出源——"probe-<ownerId>"；非法 ownerId 走失败侧。
func TestProbeSource(t *testing.T) {
	if value, ok := ProbeSource("5"); !ok || value != "probe-5" {
		t.Fatalf("输出不符: %q/%v", value, ok)
	}
	if value, ok := ProbeSource("  -2 "); !ok || value != "probe--2" {
		t.Fatalf("负数与空白应解析: %q/%v", value, ok)
	}
	if _, ok := ProbeSource("x"); ok {
		t.Fatal("非数字应走失败侧")
	}
	if _, ok := ProbeSource(""); ok {
		t.Fatal("空串应走失败侧")
	}
}

// TestProbeSink 多输入单输出——a + ":" + b；b 非整数走失败侧。
func TestProbeSink(t *testing.T) {
	if value, ok := ProbeSink("abc", "3"); !ok || value != "abc:3" {
		t.Fatalf("拼接不符: %q/%v", value, ok)
	}
	if value, ok := ProbeSink("", "-7"); !ok || value != ":-7" {
		t.Fatalf("负数解析不符: %q/%v", value, ok)
	}
	if _, ok := ProbeSink("abc", "1.5"); ok {
		t.Fatal("非整数应走失败侧")
	}
	if _, ok := ProbeSink("abc", ""); ok {
		t.Fatal("空串应走失败侧")
	}
}

// TestProbeSinkSlow 延迟探针——正延迟睡眠；0 / 负值不睡眠；非整数走失败侧。
func TestProbeSinkSlow(t *testing.T) {
	if value, ok := ProbeSinkSlow("task", "0"); !ok || value != "task" {
		t.Fatalf("零延迟不符: %q/%v", value, ok)
	}
	start := time.Now()
	if value, ok := ProbeSinkSlow("task", "30"); !ok || value != "task" {
		t.Fatalf("延迟输出不符: %q/%v", value, ok)
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Fatalf("30ms 延迟未见睡眠: %v", elapsed)
	}
	// 负值不睡眠（C# `if (delayMs > 0)` 同口径——负数不落无限等待分支）
	start = time.Now()
	if value, ok := ProbeSinkSlow("task", "-1"); !ok || value != "task" {
		t.Fatalf("负延迟不符: %q/%v", value, ok)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("负值不应睡眠: %v", elapsed)
	}
	if _, ok := ProbeSinkSlow("task", "abc"); ok {
		t.Fatal("非整数应走失败侧")
	}
}
