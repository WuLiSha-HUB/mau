package runtime

import "testing"

// TestRemoveEntry 删除数据区条目——命中返回 true；重复删除 / 缺 scope 静默 false；其余键不受影响。
func TestRemoveEntry(t *testing.T) {
	db := NewDataBox()
	db.Set("global", "k", "v")
	db.Set("global", "keep", "x")

	if !db.Remove("global", "k") {
		t.Fatal("首次删除应返回 true")
	}
	if _, ok := db.Get("global", "k"); ok {
		t.Fatal("删除后不应再命中")
	}
	if db.Remove("global", "k") {
		t.Fatal("重复删除应返回 false（不存在静默成功）")
	}
	if db.Remove("missing", "k") {
		t.Fatal("scope 缺失应返回 false")
	}
	if v, ok := db.Get("global", "keep"); !ok || v != "x" {
		t.Fatalf("同 scope 其它键应不受影响: %#v/%v", v, ok)
	}
}

// TestRemoveThenCapture 删除的条目不出现在截面里——观测面与读面同源。
func TestRemoveThenCapture(t *testing.T) {
	db := NewDataBox()
	db.Set("2", "hasDone", true)
	db.Set("2", "other", "x")
	db.Remove("2", "hasDone")

	entries := db.Capture()
	if len(entries) != 1 || entries[0].Key != "other" {
		t.Fatalf("截面应只剩 other，得 %#v", entries)
	}
}
