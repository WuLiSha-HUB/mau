package bricks

import (
	"testing"

	"cathome5/mau/runtime"
)

// TestOaPost 上架语义——返回新 ID 并落字段；OA 缺失 / 超时帧数非法一律失败。
func TestOaPost(t *testing.T) {
	oa := newOA()
	value, ok := OaPost(oa, 5, "TOOL", "text-read", "1200")
	if !ok {
		t.Fatal("上架应成功")
	}
	id, valid := parseOfficeID(value)
	if !valid {
		t.Fatalf("OfficeId 非法: %q", value)
	}
	office := oa.GetOffice(id)
	if office.Status != runtime.OfficeOpen || office.Owner != 5 || office.Type != "TOOL" ||
		office.Name != "text-read" || office.TimeoutFrames != 1200 {
		t.Fatalf("上架字段不符: %+v", office)
	}
	if _, ok := OaPost(nil, 5, "TOOL", "text-read", "1200"); ok {
		t.Fatal("OA 缺失应走失败侧")
	}
	if _, ok := OaPost(oa, 5, "TOOL", "text-read", ""); ok {
		t.Fatal("空超时帧数应失败")
	}
	if _, ok := OaPost(oa, 5, "TOOL", "text-read", "12a"); ok {
		t.Fatal("非法超时帧数应失败")
	}
}

// TestOaSetStr 载荷写入——Open + 本人成功；被认领后拒绝；服务缺失走失败侧。
func TestOaSetStr(t *testing.T) {
	oa := newOA()
	id := oa.Post(5, "TOOL", "text-read", 1200)
	if value, ok := OaSetStr(oa, 5, itoa64(id), "path", "a.txt"); !ok || value != "true" {
		t.Fatalf("本人写入应成功，得 %q/%v", value, ok)
	}
	if got, hit := oa.GetStr(id, "path"); !hit || got != "a.txt" {
		t.Fatalf("载荷未落: %q/%v", got, hit)
	}
	if value, ok := OaSetStr(oa, 6, itoa64(id), "path", "b.txt"); !ok || value != "false" {
		t.Fatalf("非挂单方应被拒，得 %q/%v", value, ok)
	}
	if _, ok := OaClaimNextSimple(oa, 9, "TOOL", "text-read"); !ok {
		t.Fatal("认领失败")
	}
	if value, ok := OaSetStr(oa, 5, itoa64(id), "path", "c.txt"); !ok || value != "false" {
		t.Fatalf("已认领应拒绝写入，得 %q/%v", value, ok)
	}
	if _, ok := OaSetStr(nil, 5, itoa64(id), "path", "x"); ok {
		t.Fatal("OA 缺失应走失败侧")
	}
	if value, ok := OaSetStr(oa, 5, "0", "path", "x"); !ok || value != "false" {
		t.Fatalf("非法工单号应回 false/true，得 %q/%v", value, ok)
	}
}

// TestOaIsClosed 闭合查询——Open false / Closed true / 服务缺失失败侧。
func TestOaIsClosed(t *testing.T) {
	oa := newOA()
	id := oa.Post(5, "TOOL", "text-read", 1200)
	if value, ok := OaIsClosed(oa, itoa64(id)); !ok || value != "false" {
		t.Fatalf("Open 单应回 false/true，得 %q/%v", value, ok)
	}
	oa.ClaimBatch(9, []int64{id})
	oa.Complete(id, 9, map[string]string{"result": "done"})
	if value, ok := OaIsClosed(oa, itoa64(id)); !ok || value != "true" {
		t.Fatalf("Closed 单应回 true/true，得 %q/%v", value, ok)
	}
	if _, ok := OaIsClosed(nil, itoa64(id)); ok {
		t.Fatal("OA 缺失应走失败侧")
	}
	if value, ok := OaIsClosed(oa, "0"); !ok || value != "false" {
		t.Fatalf("非法工单号应回 false/true，得 %q/%v", value, ok)
	}
}

// TestOaIsDone 终态判断——Closed / Timeout 均真；Open / Work 假；未建单假。
func TestOaIsDone(t *testing.T) {
	oa := newOA()
	first := oa.Post(5, "TOOL", "text-read", 1200)
	second := oa.Post(5, "TOOL", "text-read", 1)
	if value, ok := OaIsDone(oa, itoa64(first)); !ok || value != "false" {
		t.Fatalf("Open 单应回 false/true，得 %q/%v", value, ok)
	}
	oa.ClaimBatch(9, []int64{first})
	if value, _ := OaIsDone(oa, itoa64(first)); value != "false" {
		t.Fatalf("Work 单应回 false，得 %q", value)
	}
	oa.Complete(first, 9, map[string]string{"result": "done"})
	if value, _ := OaIsDone(oa, itoa64(first)); value != "true" {
		t.Fatalf("Closed 单应回 true，得 %q", value)
	}
	// second 的 Open 超时：> timeoutFrames（1 帧）——Tick 两次即超时。
	oa.Tick()
	oa.Tick()
	if state, _ := oa.StatusOf(second); state != runtime.OfficeTimeout {
		t.Fatalf("second 应超时，得 %v", state)
	}
	if value, _ := OaIsDone(oa, itoa64(second)); value != "true" {
		t.Fatalf("Timeout 单应回 true，得 %q", value)
	}
	if value, ok := OaIsDone(oa, "0"); !ok || value != "false" {
		t.Fatalf("未建单应回 false/true，得 %q/%v", value, ok)
	}
	if _, ok := OaIsDone(nil, itoa64(first)); ok {
		t.Fatal("OA 缺失应走失败侧")
	}
}

// TestOaGetResultStr 回执读取——完成前未命中；完成后取 result；缺失键 false。
func TestOaGetResultStr(t *testing.T) {
	oa := newOA()
	id := oa.Post(5, "TOOL", "text-read", 1200)
	if value, ok := OaGetResultStr(oa, itoa64(id)); ok || value != "" {
		t.Fatalf("完成前应未命中，得 %q/%v", value, ok)
	}
	oa.ClaimBatch(9, []int64{id})
	oa.Complete(id, 9, map[string]string{"result": "回执正文"})
	if value, ok := OaGetResultStr(oa, itoa64(id)); !ok || value != "回执正文" {
		t.Fatalf("完成后应取回执正文，得 %q/%v", value, ok)
	}
	if _, ok := OaGetResultStr(oa, "0"); ok {
		t.Fatal("非法工单号应未命中")
	}
	if _, ok := OaGetResultStr(nil, itoa64(id)); ok {
		t.Fatal("OA 缺失应走失败侧")
	}
}

// TestParseNonNegative 非负整数解析——空 / 负数 / 非数字 / 超界一律拒绝。
func TestParseNonNegative(t *testing.T) {
	if _, ok := parseNonNegative(""); ok {
		t.Fatal("空串应拒绝")
	}
	if _, ok := parseNonNegative("-1"); ok {
		t.Fatal("负数应拒绝")
	}
	if _, ok := parseNonNegative("1.5"); ok {
		t.Fatal("小数应拒绝")
	}
	if value, ok := parseNonNegative(" 1200 "); !ok || value != 1200 {
		t.Fatalf("正常解析不符: %d/%v", value, ok)
	}
	if value, ok := parseNonNegative("0"); !ok || value != 0 {
		t.Fatalf("零应接受: %d/%v", value, ok)
	}
}
