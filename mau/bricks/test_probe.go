package bricks

import (
	"strconv"
	"strings"
	"time"
)

// ⚠ 多端口说明（probe.source）：C# 签名带第二输出端口 texts（string[]），Go 侧积木闭包是单值契约
// （graph.BrickFunc 只返回一个盒值），数组端口无表达手段——现无语料引用该端口，只实现主值 text。
// 待 Go 侧出现多端口需求时再扩契约（不在本轮范围，避免为无人消费的端口提前增实体）。

// ProbeSource 复现 probe.source——固定输出源（BRIK-TEST-001）。
//
// 主值 text = "probe-<ownerId>"；C# 第二端口 texts = [text, "extra"] 见文件头说明。
func ProbeSource(ownerID string) (string, bool) {
	id, ok := parseProbeInt64(ownerID)
	if !ok {
		return "", false
	}
	return "probe-" + itoa64(id), true
}

// ProbeSink 复现 probe.sink——消费输入输出结果（BRIK-TEST-002）。
//
// result = a + ":" + b（b 是整型入参——C# 为强类型 int，Go 侧字符串参数按十进制解析；
// 解析失败走失败侧，不静默拼 0——诚实失败）。
func ProbeSink(a, b string) (string, bool) {
	number, ok := parseProbeInt64(b)
	if !ok {
		return "", false
	}
	return a + ":" + itoa64(number), true
}

// ProbeSinkSlow 复现 probe.sink_slow——延迟探针（BRIK-TEST-003）。
//
// 睡眠 delayMs 毫秒后返回 a（模拟真实慢 IO——并发 / 滞留 / 超时语义压测）；
// delayMs ≤ 0 不睡眠（C# `if (delayMs > 0)` 同口径，负数不落 Thread.Sleep 的无限等待分支）。
func ProbeSinkSlow(a, delayMs string) (string, bool) {
	delay, ok := parseProbeInt64(delayMs)
	if !ok {
		return "", false
	}
	if delay > 0 {
		time.Sleep(time.Duration(delay) * time.Millisecond)
	}
	return a, true
}

// parseProbeInt64 解析有符号十进制整数（int64 界内）——空 / 含非数字 / 越界返回 false。
func parseProbeInt64(raw string) (int64, bool) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return 0, false
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}
