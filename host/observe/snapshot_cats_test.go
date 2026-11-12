package observe

import "testing"

// TestBuildBoxesFiltersInternal 校验 boxes 段的内部实现盒过滤（C# IsInternalBox 同口径）。
func TestBuildBoxesFiltersInternal(t *testing.T) {
	b := &Builder{Boxes: func() []Box {
		return []Box{
			{Scope: "global", Key: "frame", T: "n", Value: int64(3)},
			{Scope: "log", Key: "entries", T: "o", Value: "null"},
			{Scope: "log", Key: "sync", T: "o", Value: "null"},
			{Scope: "log", Key: "filePath", T: "s", Value: "D:\\x.log"},
			{Scope: "1", Key: "self_desc", T: "o", Value: "[[]string]"},
		}
	}}
	boxes := b.buildBoxes()
	if len(boxes) != 2 {
		t.Fatalf("过滤结果 = %d 条，期望 2：%+v", len(boxes), boxes)
	}
	if boxes[0].Key != "filePath" || boxes[1].Key != "self_desc" {
		t.Fatalf("保留条目不符：%+v", boxes)
	}
}

// TestCatJSONShape 校验 cats 条目 JSON 形态——id 为数字、desc 恒在（空时为 []）。
func TestCatJSONShape(t *testing.T) {
	cat := Cat{
		Name:   "TextCat",
		ID:     1,
		Type:   "Graph",
		Kind:   "flow",
		Status: &CatStatus{StateLines: []string{}, Sensors: []SensorValue{}, Slots: []SlotValue{}, Wires: []WireStatus{}},
		Desc:   []string{},
	}
	got := marshalJSON(cat)
	want := `{"name":"TextCat","id":1,"type":"Graph","kind":"flow","faulted":false,"faultReason":"","status":{"stateLines":[],"sensors":[],"slots":[],"wires":[]},"desc":[]}`
	if got != want {
		t.Fatalf("cats 条目 JSON 不符：\n got = %s\nwant = %s", got, want)
	}
}
