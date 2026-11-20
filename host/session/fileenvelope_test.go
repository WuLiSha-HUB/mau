package session

import (
	"strings"
	"testing"
)

// TestBuildFileEnvelope 组装——段头 / 条目 / 段尾 + 空行 + 正文；无路径原样返回。
func TestBuildFileEnvelope(t *testing.T) {
	got := BuildFileEnvelope([]string{" D:/a.zip ", "D:/b.md"}, 7, "看这两个文件")
	want := "[file-open]\n文件7-1：D:/a.zip\n文件7-2：D:/b.md\n[file-end]\n\n看这两个文件"
	if got != want {
		t.Fatalf("组装 = %q", got)
	}
	if got := BuildFileEnvelope(nil, 7, "原样"); got != "原样" {
		t.Fatalf("无文件应原样 = %q", got)
	}
	if got := BuildFileEnvelope([]string{"D:/a.zip"}, 7, ""); got != "[file-open]\n文件7-1：D:/a.zip\n[file-end]" {
		t.Fatalf("无正文 = %q", got)
	}
}

// TestApplyFileEnvelopeToLine 指令行套用——Chat 行套包裹；非 Chat 行 / 无文件原样。
func TestApplyFileEnvelopeToLine(t *testing.T) {
	got := ApplyFileEnvelopeToLine("Chat 看这个", []string{"D:/a.zip"}, 3)
	want := "Chat [file-open]\n文件3-1：D:/a.zip\n[file-end]\n\n看这个"
	if got != want {
		t.Fatalf("套用 = %q", got)
	}
	if got := ApplyFileEnvelopeToLine("普通消息", []string{"D:/a.zip"}, 3); got != "普通消息" {
		t.Fatalf("非 Chat 行 = %q", got)
	}
	if got := ApplyFileEnvelopeToLine("Chat 无附件", nil, 3); got != "Chat 无附件" {
		t.Fatalf("无文件 = %q", got)
	}
}

// TestParseFileEnvelope 解析——严格门通过后返回条目与前后段。
func TestParseFileEnvelope(t *testing.T) {
	text := "前段说明\n\n[file-open]\n文件9-1：D:/a.zip\n[file-end]\n\n看这个"
	items, before, after, ok := ParseFileEnvelope(text)
	if !ok || len(items) != 1 || items[0].Ref != "9-1" || items[0].Path != "D:/a.zip" {
		t.Fatalf("解析 = %+v ok=%v", items, ok)
	}
	if before != "前段说明" || after != "看这个" {
		t.Fatalf("前后段 = %q / %q", before, after)
	}
}

// TestParseFileEnvelopeStrict 严格门——缺段尾 / 内部杂行 / 零条目均不成立。
func TestParseFileEnvelopeStrict(t *testing.T) {
	cases := []string{
		"[file-open]\n文件1-1：D:/a.zip",
		"[file-open]\n文件1-1：D:/a.zip\n说明行\n[file-end]",
		"[file-open]\n[file-end]",
		"文件1-1：D:/a.zip",
	}
	for _, text := range cases {
		if _, _, _, ok := ParseFileEnvelope(text); ok {
			t.Fatalf("应判不成立: %q", text)
		}
	}
}

// TestParseFileEnvelopeFenced 围栏吞并——包裹被 ``` 包住时命中且围栏行不残留。
func TestParseFileEnvelopeFenced(t *testing.T) {
	text := "```\n[file-open]\n文件1-1：D:/a.zip\n[file-end]\n```\n\n正文"
	items, before, after, ok := ParseFileEnvelope(text)
	if !ok || len(items) != 1 {
		t.Fatalf("围栏内应命中: %+v ok=%v", items, ok)
	}
	if before != "" || after != "正文" {
		t.Fatalf("前后段 = %q / %q", before, after)
	}
	if strings.Contains(before, "```") || strings.Contains(after, "```") {
		t.Fatalf("围栏行应被吞并: before=%q after=%q", before, after)
	}
}
