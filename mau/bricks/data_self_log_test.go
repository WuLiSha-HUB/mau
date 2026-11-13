package bricks

import (
	"testing"

	"cathome5/mau/runtime"
)

// TestDataBoxSetStr 写数据区——落盘可回读；DataBox 缺失走失败侧。
func TestDataBoxSetStr(t *testing.T) {
	db := runtime.NewDataBox()
	if value, ok := DataBoxSetStr(db, "sys", "k", "v"); !ok || value != "true" {
		t.Fatalf("写入应成功，得 %q/%v", value, ok)
	}
	if got, hit := db.Get("sys", "k"); !hit || got != "v" {
		t.Fatalf("回读不符: %q/%v", got, hit)
	}
	if _, ok := DataBoxSetStr(nil, "sys", "k", "v"); ok {
		t.Fatal("DataBox 缺失应走失败侧")
	}
}

// TestDataBoxGetStr 读数据区——命中 / 缺失 / 类型非 string 三种口径。
func TestDataBoxGetStr(t *testing.T) {
	db := runtime.NewDataBox()
	db.Set("sys", "k", "v")
	if value, ok := DataBoxGetStr(db, "sys", "k"); !ok || value != "v" {
		t.Fatalf("命中应为 v/true，得 %q/%v", value, ok)
	}
	if _, ok := DataBoxGetStr(db, "sys", "missing"); ok {
		t.Fatal("缺失应未命中")
	}
	db.Set("sys", "num", 42)
	if _, ok := DataBoxGetStr(db, "sys", "num"); ok {
		t.Fatal("非 string 类型应未命中（TryGet<string> 口径）")
	}
	if _, ok := DataBoxGetStr(nil, "sys", "k"); ok {
		t.Fatal("DataBox 缺失应走失败侧")
	}
}

// TestSelfDescAdd 追加自述——已有行保留 + 末尾追加；空参跳过；类型不符从空开始。
func TestSelfDescAdd(t *testing.T) {
	db := runtime.NewDataBox()
	if value, ok := SelfDescAdd(db, 7, "第一行", "", "第三行"); !ok || value != "true" {
		t.Fatalf("首次追加应成功，得 %q/%v", value, ok)
	}
	if value, ok := SelfDescAdd(db, 7, "第四行"); !ok || value != "true" {
		t.Fatalf("再次追加应成功，得 %q/%v", value, ok)
	}
	raw, hit := db.Get("7", "self_desc")
	lines, isLines := raw.([]string)
	if !hit || !isLines {
		t.Fatalf("自述盒缺失或类型不符: %#v", raw)
	}
	if len(lines) != 3 || lines[0] != "第一行" || lines[1] != "第三行" || lines[2] != "第四行" {
		t.Fatalf("追加结果不符: %#v", lines)
	}
	db.Set("8", "self_desc", "非数组")
	if _, ok := SelfDescAdd(db, 8, "重置后"); !ok {
		t.Fatal("类型不符应可继续追加")
	}
	raw, _ = db.Get("8", "self_desc")
	if lines, _ := raw.([]string); len(lines) != 1 || lines[0] != "重置后" {
		t.Fatalf("类型不符应从空开始，得 %#v", raw)
	}
	if _, ok := SelfDescAdd(nil, 7, "x"); ok {
		t.Fatal("DataBox 缺失应走失败侧")
	}
}

// TestLogWrite 结构化日志——交出口收到原样参数；级别非法 / 未注入走失败侧。
func TestLogWrite(t *testing.T) {
	var gotLevel int
	var gotText, gotCategory string
	logf := func(level int, message, category string) {
		gotLevel, gotText, gotCategory = level, message, category
	}
	if value, ok := LogWrite(logf, "消息正文", "2", "OA"); !ok || value != "true" {
		t.Fatalf("写入应成功，得 %q/%v", value, ok)
	}
	if gotLevel != 2 || gotText != "消息正文" || gotCategory != "OA" {
		t.Fatalf("出口参数不符: %d/%q/%q", gotLevel, gotText, gotCategory)
	}
	if value, ok := LogWrite(logf, "负级别", "-1", ""); !ok || value != "true" || gotLevel != -1 {
		t.Fatalf("负级别应原样透传（归一在日志面），得 %q/%v/%d", value, ok, gotLevel)
	}
	if _, ok := LogWrite(nil, "x", "1", ""); ok {
		t.Fatal("未注入应走失败侧")
	}
	if _, ok := LogWrite(logf, "x", "", ""); ok {
		t.Fatal("空级别应失败")
	}
	if _, ok := LogWrite(logf, "x", "1.5", ""); ok {
		t.Fatal("非整数级别应失败")
	}
}

// TestParseLogLevel 级别解析——空 / 非数字拒绝；负号保留。
func TestParseLogLevel(t *testing.T) {
	if _, ok := parseLogLevel(""); ok {
		t.Fatal("空串应拒绝")
	}
	if _, ok := parseLogLevel("x"); ok {
		t.Fatal("非数字应拒绝")
	}
	if value, ok := parseLogLevel(" 3 "); !ok || value != 3 {
		t.Fatalf("正常解析不符: %d/%v", value, ok)
	}
	if value, ok := parseLogLevel("-2"); !ok || value != -2 {
		t.Fatalf("负号解析不符: %d/%v", value, ok)
	}
	if _, ok := parseLogLevel("-"); ok {
		t.Fatal("仅负号应拒绝")
	}
}
