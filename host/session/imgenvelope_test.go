package session

import "testing"

// TestImageEnvelopeBuild 组装包裹 + 正文与空正文形态。
func TestImageEnvelopeBuild(t *testing.T) {
	got := BuildImageEnvelope([]string{" D:/a.png ", "", "D:/b b.png"}, 92, "看这个")
	want := "[image-open]\n图片92-1：D:/a.png\n图片92-2：D:/b b.png\n[image-end]\n\n看这个"
	if got != want {
		t.Fatalf("组装不符:\n实得 %q\n期望 %q", got, want)
	}
	if got := BuildImageEnvelope(nil, 92, "只有正文"); got != "只有正文" {
		t.Fatalf("无图片应原样返回正文: %q", got)
	}
	if got := BuildImageEnvelope([]string{"D:/a.png"}, 7, ""); got != "[image-open]\n图片7-1：D:/a.png\n[image-end]" {
		t.Fatalf("空正文不应留多余空行: %q", got)
	}
}

// TestImageEnvelopeApplyToLine Chat 指令行套用与不成立原样返回。
func TestImageEnvelopeApplyToLine(t *testing.T) {
	if got := ApplyImageEnvelopeToLine("Chat 看这个", []string{"D:/a.png"}, 3); got != "Chat [image-open]\n图片3-1：D:/a.png\n[image-end]\n\n看这个" {
		t.Fatalf("Chat 行套用不符: %q", got)
	}
	if got := ApplyImageEnvelopeToLine("普通消息", []string{"D:/a.png"}, 3); got != "普通消息" {
		t.Fatalf("非 Chat 行应原样返回: %q", got)
	}
	if got := ApplyImageEnvelopeToLine("Chat 看这个", nil, 3); got != "Chat 看这个" {
		t.Fatalf("无图片应原样返回: %q", got)
	}
}

// TestImageEnvelopeParse 严格门解析——段头 + 条目 + 段尾齐备。
func TestImageEnvelopeParse(t *testing.T) {
	text := "先说一句\n[image-open]\n图片92-1：D:/a.png\n图片92-2：D:/b b.png\n[image-end]\n\n后说一句"
	items, before, after, ok := ParseImageEnvelope(text)
	if !ok {
		t.Fatal("合法包裹应命中")
	}
	if len(items) != 2 || items[0].Ref != "92-1" || items[1].Path != "D:/b b.png" {
		t.Fatalf("条目不符: %+v", items)
	}
	if before != "先说一句" || after != "后说一句" {
		t.Fatalf("前后段不符: %q / %q", before, after)
	}
}

// TestImageEnvelopeParseStrict 内部空行 / 零条目 / 缺段尾 → 不命中；继续向后找下一处段头。
func TestImageEnvelopeParseStrict(t *testing.T) {
	if _, _, _, ok := ParseImageEnvelope("[image-open]\n\n图片1-1：a\n[image-end]"); ok {
		t.Fatal("内部空行应不命中")
	}
	if _, _, _, ok := ParseImageEnvelope("[image-open]\n[image-end]"); ok {
		t.Fatal("零条目应不命中")
	}
	if _, _, _, ok := ParseImageEnvelope("[image-open]\n图片1-1：a"); ok {
		t.Fatal("缺段尾应不命中")
	}
	text := "[image-open]\n坏行\n[image-end]\n[image-open]\n图片2-1：b\n[image-end]"
	items, _, _, ok := ParseImageEnvelope(text)
	if !ok || len(items) != 1 || items[0].Ref != "2-1" {
		t.Fatalf("应跳过不成立候选继续向后找: ok=%v items=%+v", ok, items)
	}
}

// TestImageEnvelopeFenced 围栏包住时吞并紧邻围栏行；围栏外不吞。
func TestImageEnvelopeFenced(t *testing.T) {
	fenced := "```\n[image-open]\n图片1-1：a\n[image-end]\n```\n尾巴"
	_, before, after, ok := ParseImageEnvelope(fenced)
	if !ok || before != "" || after != "尾巴" {
		t.Fatalf("围栏内包裹应吞并围栏行: ok=%v before=%q after=%q", ok, before, after)
	}
	plain := "```\ncode\n```\n[image-open]\n图片1-1：a\n[image-end]"
	_, before, _, ok = ParseImageEnvelope(plain)
	if !ok || before != "```\ncode\n```" {
		t.Fatalf("围栏外包裹不应吞并围栏行: ok=%v before=%q", ok, before)
	}
}

// TestImageEnvelopeParseEmpty 空文本不命中且 before 为空。
func TestImageEnvelopeParseEmpty(t *testing.T) {
	items, before, after, ok := ParseImageEnvelope("")
	if ok || items != nil || before != "" || after != "" {
		t.Fatalf("空文本应不命中: %v %q %q %v", items, before, after, ok)
	}
	if _, before, _, ok := ParseImageEnvelope("没有包裹"); ok || before != "没有包裹" {
		t.Fatalf("无包裹时 before 应为原文 Trim: %q", before)
	}
}
