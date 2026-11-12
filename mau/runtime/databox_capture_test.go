package runtime

import "testing"

// TestCaptureSorted 校验数据区截面——排序确定（scope 字典序 + key 字典序）与值原样。
func TestCaptureSorted(t *testing.T) {
	db := NewDataBox()
	db.Set("2", "b", true)
	db.Set("10", "a", "x")
	db.Set("2", "a", int64(7))

	entries := db.Capture()
	if len(entries) != 3 {
		t.Fatalf("截面条目 = %d，期望 3", len(entries))
	}
	want := []Entry{
		{Scope: "10", Key: "a"},
		{Scope: "2", Key: "a"},
		{Scope: "2", Key: "b"},
	}
	for i := range want {
		if entries[i].Scope != want[i].Scope || entries[i].Key != want[i].Key {
			t.Fatalf("第 %d 条 = %s/%s，期望 %s/%s", i, entries[i].Scope, entries[i].Key, want[i].Scope, want[i].Key)
		}
	}
	if v, ok := entries[1].Value.(int64); !ok || v != 7 {
		t.Fatalf("值类型丢失：%#v", entries[1].Value)
	}
}
