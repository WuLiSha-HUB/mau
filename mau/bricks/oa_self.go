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

// OaPost 复现 oa.post——上架工单（空双字典载荷随单生成，挂单方 set_str 逐 Key 写参数）。
//
// 返回新 OfficeId 文本（OA 缺失 / 超时帧数非法为 "0"）；ok 表示是否上架成功。
func OaPost(oa *runtime.OA, owner int64, officeType, officeName, timeoutTicks string) (string, bool) {
	if oa == nil {
		return "0", false
	}
	ticks, ok := parseNonNegative(timeoutTicks)
	if !ok {
		return "0", false
	}
	return itoa64(oa.Post(owner, officeType, officeName, ticks)), true
}

// OaSetStr 复现 oa.set_str——写入工单请求载荷 str 值（仅 Open + 挂单方本人可操作）。
//
// 返回 "true" / "false"（C# bool 返回值 = 写入结果）；ok = OA 服务存在。
func OaSetStr(oa *runtime.OA, owner int64, officeID, key, value string) (string, bool) {
	if oa == nil {
		return "false", false
	}
	id, ok := parseOfficeID(officeID)
	if !ok {
		return "false", true
	}
	if oa.SetStr(id, owner, key, value) {
		return "true", true
	}
	return "false", true
}

// OaIsClosed 复现 oa.is_closed——查询工单是否 Closed（挂单方轮询收结果）。
func OaIsClosed(oa *runtime.OA, officeID string) (string, bool) {
	if oa == nil {
		return "false", false
	}
	id, ok := parseOfficeID(officeID)
	if !ok {
		return "false", true
	}
	state, exists := oa.StatusOf(id)
	if exists && state == runtime.OfficeClosed {
		return "true", true
	}
	return "false", true
}

// OaIsDone 复现 oa.is_done——终态判断（Closed 或 Timeout 都算完成；判断语义）。
//
// officeId ≤ 0（未建单）恒 false；OA 缺失走失败侧。
func OaIsDone(oa *runtime.OA, officeID string) (string, bool) {
	if oa == nil {
		return "false", false
	}
	id, ok := parseOfficeID(officeID)
	if !ok {
		return "false", true
	}
	state, exists := oa.StatusOf(id)
	if exists && (state == runtime.OfficeClosed || state == runtime.OfficeTimeout) {
		return "true", true
	}
	return "false", true
}

// OaGetResultStr 复现 oa.get_result_str——读取工单回执 result 键（Closed 后的 Result 载荷）。
//
// 返回回执文本 + 是否命中 result 键（Key 不存在为空串 / false）。
func OaGetResultStr(oa *runtime.OA, officeID string) (string, bool) {
	if oa == nil {
		return "", false
	}
	id, ok := parseOfficeID(officeID)
	if !ok {
		return "", false
	}
	office := oa.GetOffice(id)
	if office.Result == nil {
		return "", false
	}
	value, hit := office.Result["result"]
	return value, hit
}

// parseNonNegative 解析非负十进制整数——空 / 含非数字 / 超界返回 false。
func parseNonNegative(raw string) (int64, bool) {
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
		if value > 1<<40 {
			return 0, false
		}
	}
	return value, true
}
