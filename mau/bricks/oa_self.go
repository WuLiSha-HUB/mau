package bricks

import (
	"strings"

	"cathome5/mau/runtime"
)

// splitOfficeNames 拆候选名——逗号 / 分号分隔，空白裁剪，空项丢弃（对齐 C# Split(',', ';')）。
func splitOfficeNames(raw string) []string {
	out := make([]string, 0, 4)
	for _, item := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' }) {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

// OaIsOpen 复现 oa.is_open——某大类下候选名是否存在 Open 单（纯探测零副作用）。
//
// 探测本身成功即 ok=true（盒值 "true" / "false" 由 graph.truthy 判定）；
// OA 不可用时 ok=false（走传感器 FalseActions——诚实降级，不静默）。
func OaIsOpen(oa *runtime.OA, officeType, officeNames string) (string, bool) {
	if oa == nil {
		return "false", false
	}
	if len(oa.ListOpen(officeType, splitOfficeNames(officeNames))) > 0 {
		return "true", true
	}
	return "false", true
}

// OaClaimNextSimple 复现 oa.claim_next_simple——按候选名 ListOpen 后逐单认领，首个成功即返回。
//
// 返回值是 OfficeId 文本（无单为 "0"）；ok 表示是否接到单（false 走 FalseActions → 回 Idle）。
func OaClaimNextSimple(oa *runtime.OA, worker int64, officeType, officeNames string) (string, bool) {
	if oa == nil {
		return "0", false
	}
	for _, off := range oa.ListOpen(officeType, splitOfficeNames(officeNames)) {
		if claimed := oa.ClaimBatch(worker, []int64{off.ID}); len(claimed) > 0 {
			return itoa64(off.ID), true
		}
	}
	return "0", false
}

// OaGetStr 复现 oa.get_str——读工单载荷 / 回执 str 值；ok = Key 存在。
func OaGetStr(oa *runtime.OA, officeID, key string) (string, bool) {
	if oa == nil {
		return "", false
	}
	id, ok := parseOfficeID(officeID)
	if !ok {
		return "", false
	}
	return oa.GetStr(id, key)
}

// OaCompleteStr 复现 oa.complete_str——写 result 回执后置 Closed；OA 存在即 true（忽略 Complete 返回值）。
func OaCompleteStr(oa *runtime.OA, worker int64, officeID, result string) (string, bool) {
	if oa == nil {
		return "false", false
	}
	id, ok := parseOfficeID(officeID)
	if !ok {
		return "false", false
	}
	oa.Complete(id, worker, map[string]string{"result": result})
	return "true", true
}

// SelfDesc 复现 self.desc——覆盖写实体自述（每参一行，空参跳过）。
func SelfDesc(db *runtime.DataBox, flowID int64, lines ...string) (string, bool) {
	if db == nil {
		return "false", false
	}
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			kept = append(kept, line)
		}
	}
	db.Set(itoa64(flowID), "self_desc", kept)
	return "true", true
}

// parseOfficeID 解析工单号——空 / 非数字 / 非正数返回 false。
func parseOfficeID(raw string) (int64, bool) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return 0, false
	}
	var value int64
	for _, r := range text {
		if r < '0' || r > '9' {
			return 0, false
		}
		value = value*10 + int64(r-'0')
		if value > 1<<62 {
			return 0, false
		}
	}
	if value <= 0 {
		return 0, false
	}
	return value, true
}

// itoa64 十进制定点渲染（runtime 包的 itoa 不导出）。
func itoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	var buf [20]byte
	i := len(buf)
	for v != 0 {
		digit := v % 10
		if digit < 0 {
			digit = -digit
		}
		i--
		buf[i] = byte('0' + digit)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
