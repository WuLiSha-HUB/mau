package bricks

import (
	"testing"

	"cathome5/mau/runtime"
)

// newOA 建一个无 trace 的空 OA（日志面不接——测试只看语义）。
func newOA() *runtime.OA { return runtime.NewOA(nil) }

// TestOAIsOpen 探测语义——无单 false / 有单 true / OA 缺失走失败侧。
func TestOAIsOpen(t *testing.T) {
	oa := newOA()
	if value, ok := OaIsOpen(oa, "TOOL", "text-read"); !ok || value != "false" {
		t.Fatalf("空 OA 探测应为 false/true，得 %q/%v", value, ok)
	}
	id := oa.Post(1, "TOOL", "text-read", 1200)
	value, ok := OaIsOpen(oa, "TOOL", "text-read,file-tree")
	if !ok || value != "true" {
		t.Fatalf("有单探测应为 true/true，得 %q/%v", value, ok)
	}
	if _, ok := OaIsOpen(nil, "TOOL", "text-read"); ok {
		t.Fatal("OA 缺失应走失败侧")
	}
	_ = id
}

// TestOAClaimNextSimple 认领语义——首个成功返回 ID；重复认领失败；worker 落到 OA。
func TestOAClaimNextSimple(t *testing.T) {
	oa := newOA()
	id := oa.Post(1, "TOOL", "file-tree", 1200)
	value, ok := OaClaimNextSimple(oa, 7, "TOOL", "file-tree")
	if !ok || value != itoa64(id) {
		t.Fatalf("认领应得工单 %d，得 %q/%v", id, value, ok)
	}
	office := oa.GetOffice(id)
	if office.Status != runtime.OfficeWork || office.Worker != 7 {
		t.Fatalf("认领后状态不符: %+v", office)
	}
	if _, ok := OaClaimNextSimple(oa, 7, "TOOL", "file-tree"); ok {
		t.Fatal("重复认领应失败")
	}
}

// TestOAGetStrAndComplete 载荷读取与完成回执——get_str 判 Key 存在；complete_str 恒 true。
func TestOAGetStrAndComplete(t *testing.T) {
	oa := newOA()
	id := oa.Post(1, "TOOL", "text-read", 1200)
	if !oa.SetStr(id, 1, "args", `{"path":"a.txt"}`) {
		t.Fatal("载荷写入失败")
	}
	if _, ok := OaClaimNextSimple(oa, 9, "TOOL", "text-read"); !ok {
		t.Fatal("认领失败")
	}
	value, ok := OaGetStr(oa, itoa64(id), "args")
	if !ok || value != `{"path":"a.txt"}` {
		t.Fatalf("载荷读取不符: %q/%v", value, ok)
	}
	if _, ok := OaGetStr(oa, itoa64(id), "missing"); ok {
		t.Fatal("缺失 Key 应返回 false")
	}
	if _, ok := OaGetStr(oa, "0", "args"); ok {
		t.Fatal("非法工单号应返回 false")
	}
	if value, ok := OaCompleteStr(oa, 9, itoa64(id), "回执正文"); !ok || value != "true" {
		t.Fatalf("完成回执应恒 true，得 %q/%v", value, ok)
	}
	office := oa.GetOffice(id)
	if office.Status != runtime.OfficeClosed || office.Result["result"] != "回执正文" {
		t.Fatalf("完成态不符: %+v", office)
	}
}

// TestSelfDesc 自述写入——每参一行，空参跳过，scope = flowID。
func TestSelfDesc(t *testing.T) {
	db := runtime.NewDataBox()
	if _, ok := SelfDesc(db, 3, "空闲待命", "", "附注"); !ok {
		t.Fatal("自述写入失败")
	}
	value, ok := db.Get("3", "self_desc")
	if !ok {
		t.Fatal("自述盒缺失")
	}
	lines, isLines := value.([]string)
	if !isLines || len(lines) != 2 || lines[0] != "空闲待命" || lines[1] != "附注" {
		t.Fatalf("自述内容不符: %#v", value)
	}
	if _, ok := SelfDesc(nil, 3, "x"); ok {
		t.Fatal("DataBox 缺失应走失败侧")
	}
}

// TestParseOfficeID 工单号解析——空 / 非数字 / 非正数一律拒绝。
func TestParseOfficeID(t *testing.T) {
	if _, ok := parseOfficeID(""); ok {
		t.Fatal("空串应拒绝")
	}
	if _, ok := parseOfficeID("0"); ok {
		t.Fatal("0 应拒绝")
	}
	if _, ok := parseOfficeID("12a"); ok {
		t.Fatal("非数字应拒绝")
	}
	value, ok := parseOfficeID("42")
	if !ok || value != 42 {
		t.Fatalf("正常解析不符: %d/%v", value, ok)
	}
}
