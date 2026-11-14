package bricks

import (
	"strings"
	"testing"
)

// fakeVision 固定返回的视觉服务替身。
type fakeVision struct {
	lastPath     string
	lastQuestion string
	body         string
}

// Analyze 记录入参并返回固定文本。
func (f *fakeVision) Analyze(imagePath, question string) string {
	f.lastPath = imagePath
	f.lastQuestion = question
	return f.body
}

// TestImageAnalyzeArgs 参数面 + 服务面 + A214 头形状。
func TestImageAnalyzeArgs(t *testing.T) {
	// 未知参数
	if got, ok := ImageAnalyze(&fakeVision{}, `{"path":"a.png","bogus":1}`); ok || !strings.HasPrefix(got, "ERR|BAD_ARGS|未知参数: bogus") {
		t.Fatalf("未知参数: %s ok=%v", got, ok)
	}
	// 缺必填
	if got, ok := ImageAnalyze(&fakeVision{}, `{}`); ok || got != "ERR|BAD_ARGS|缺参数 path（必填：path）" {
		t.Fatalf("缺 path: %s ok=%v", got, ok)
	}
	// 服务未注入
	if got, ok := ImageAnalyze(nil, `{"path":"a.png"}`); ok || got != "ERR|VISION_NO_SERVICE|宿主未注入 IVisionService" {
		t.Fatalf("未注入: %s ok=%v", got, ok)
	}
	// 成功——头字段序 question / chars + 摘要行 + 正文
	svc := &fakeVision{body: "分析结果"}
	got, ok := ImageAnalyze(svc, `{"path":"a.png","question":"what is this"}`)
	if !ok {
		t.Fatalf("成功路径应返回 ok: %s", got)
	}
	wantHead := `{"ok":true,"tool":"image-analyze","target":"a.png","question":"what is this","chars":4}`
	if !strings.HasPrefix(got, wantHead) {
		t.Fatalf("头不符:\n%s\nwant prefix: %s", got, wantHead)
	}
	if !strings.Contains(got, "\na.png | 4 字\n分析结果") {
		t.Fatalf("摘要行/正文不符: %s", got)
	}
	if svc.lastPath != "a.png" || svc.lastQuestion != "what is this" {
		t.Fatalf("服务入参: %s %s", svc.lastPath, svc.lastQuestion)
	}
	// 中文值按 System.Text.Json 默认编码器转义（\uXXXX 大写十六进制）——C# MetaHead 同口径
	cnHead := `{"ok":true,"tool":"image-analyze","target":"a.png","question":"\u8FD9\u662F\u4EC0\u4E48\uFF1F","chars":4}`
	if got, _ := ImageAnalyze(svc, `{"path":"a.png","question":"这是什么？"}`); !strings.HasPrefix(got, cnHead) {
		t.Fatalf("中文 question 转义不符: %s", got)
	}
	// 服务层 ERR|——按工具成功返回（失败原因对 Dog 可见）
	if got, ok := ImageAnalyze(&fakeVision{body: "ERR|API_NOT_CONFIGURED|视觉 API 未配置"}, `{"path":"a.png"}`); !ok || got != "ERR|API_NOT_CONFIGURED|视觉 API 未配置" {
		t.Fatalf("服务层错误: %s ok=%v", got, ok)
	}
}

// fakeImageGate 图片通道可用性替身。
type fakeImageGate struct {
	reason string
	down   bool
}

// ImageChannelDown 返回固定降级态。
func (f fakeImageGate) ImageChannelDown() (string, bool) { return f.reason, f.down }

// TestImageInject 登记回执——头 + 摘要行；参数面零容忍；通道降级期拒绝。
func TestImageInject(t *testing.T) {
	if got, ok := ImageInject(nil, `{"path":""}`); ok || got != "ERR|BAD_ARGS|缺参数 path（必填：path）" {
		t.Fatalf("缺 path: %s ok=%v", got, ok)
	}
	if got, ok := ImageInject(nil, `{"bogus":1}`); ok || !strings.HasPrefix(got, "ERR|BAD_ARGS|未知参数: bogus") {
		t.Fatalf("未知参数: %s ok=%v", got, ok)
	}
	got, ok := ImageInject(nil, `{"path":"D:/a.png"}`)
	if !ok {
		t.Fatalf("成功路径应返回 ok: %s", got)
	}
	want := `{"ok":true,"tool":"image-inject","target":"D:/a.png"}` + "\n" + "D:/a.png | 已登记（宿主批后段注入）"
	if got != want {
		t.Fatalf("回执不符:\n%s\nwant:\n%s", got, want)
	}
	// 通道降级期——直接拒绝 + 绕行提示（不发登记回执）
	downGot, downOK := ImageInject(fakeImageGate{reason: "ERR|TRANSPORT|x", down: true}, `{"path":"D:/a.png"}`)
	if downOK || !strings.HasPrefix(downGot, "ERR|VISION_UNAVAILABLE|") {
		t.Fatalf("降级期应拒绝: %s ok=%v", downGot, downOK)
	}
	if !strings.Contains(downGot, "请绕行") {
		t.Fatalf("降级回执应带绕行提示: %s", downGot)
	}
	// 通道可用——照常登记
	if okGot, okOK := ImageInject(fakeImageGate{down: false}, `{"path":"D:/a.png"}`); !okOK || okGot != want {
		t.Fatalf("通道可用应照常登记: %s ok=%v", okGot, okOK)
	}
}
