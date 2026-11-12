package session

import (
	"strings"
	"testing"
)

// setTestDogs 注入本批工具单快照（测试直写轮状态）。
func setTestDogs(s *Session, dogs []*toolDog) {
	r := s.roundState()
	r.mu.Lock()
	r.dogs = dogs
	r.mu.Unlock()
}

// TestFlushImageInjectionsMerges 合并——本批多条 image-inject 成功调用合成一条 sys 注入消息。
func TestFlushImageInjectionsMerges(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSession(t, dir, "cat-img")
	setTestDogs(s, []*toolDog{
		{Name: "image-inject", ArgsJSON: `{"path":"D:/a.png","catId":"cat-img"}`, Result: `{"ok":true}`},
		{Name: "image-inject", ArgsJSON: `{"path":"D:/b.png"}`, Result: `{"ok":true}`},
		{Name: "text-read", ArgsJSON: `{"path":"D:/c.txt"}`, Result: "内容"},
	})
	s.FlushImageInjections()

	queued := s.Pending.TakeAll(true)
	if len(queued) != 1 {
		t.Fatalf("应合并为一条注入消息，实得 %d", len(queued))
	}
	msg := queued[0]
	if msg.Source != SysKindSystemAuto {
		t.Fatalf("来源应为 systemauto: %s", msg.Source)
	}
	if !strings.HasPrefix(msg.Content, "（系统自动 · 图片插入）以下图片已插入主干，可直接查看：") {
		t.Fatalf("注入文本头不符: %s", msg.Content)
	}
	for _, want := range []string{"\nD:/a.png", "\nD:/b.png"} {
		if !strings.Contains(msg.Content, want) {
			t.Fatalf("注入文本缺 %q: %s", want, msg.Content)
		}
	}
	if msg.ImagesJSON != `["D:/a.png","D:/b.png"]` {
		t.Fatalf("附件引用数组: %s", msg.ImagesJSON)
	}
}

// TestFlushImageInjectionsSkipsFailed 失败调用跳过——ERR| 回执不产生注入。
func TestFlushImageInjectionsSkipsFailed(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSession(t, dir, "cat-img-fail")
	setTestDogs(s, []*toolDog{
		{Name: "image-inject", ArgsJSON: `{"path":"D:/a.png"}`, Result: "ERR|BAD_ARGS|缺少参数 path"},
		{Name: "image-inject", ArgsJSON: `{"path":"D:/b.png"}`, Result: ""},
	})
	s.FlushImageInjections()
	if queued := s.Pending.TakeAll(true); len(queued) != 0 {
		t.Fatalf("失败 / 无回执不应注入: %+v", queued)
	}
}

// TestFlushImageInjectionsNoTarget 无 image-inject 调用——零动作。
func TestFlushImageInjectionsNoTarget(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSession(t, dir, "cat-img-none")
	setTestDogs(s, []*toolDog{
		{Name: "text-read", ArgsJSON: `{"path":"D:/c.txt"}`, Result: "内容"},
	})
	s.FlushImageInjections()
	if queued := s.Pending.TakeAll(true); len(queued) != 0 {
		t.Fatalf("无目标应零动作: %+v", queued)
	}
}

// TestFlushImageInjectionsRootResolve 根寻址规范化——`<rootId>:<rel>` → 绝对路径。
func TestFlushImageInjectionsRootResolve(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSession(t, dir, "cat-img-root")
	s.SetImagePathResolver(func(path string) string {
		if strings.HasPrefix(path, "workspace:") {
			return "D:\\Mau\\WorkSpace\\" + strings.TrimPrefix(path, "workspace:")
		}
		return ""
	})
	setTestDogs(s, []*toolDog{
		{Name: "image-inject", ArgsJSON: `{"path":"workspace:tools/a.png"}`, Result: `{"ok":true}`},
	})
	s.FlushImageInjections()
	queued := s.Pending.TakeAll(true)
	if len(queued) != 1 {
		t.Fatalf("应产生一条注入: %+v", queued)
	}
	if queued[0].ImagesJSON != `["D:\\Mau\\WorkSpace\\tools/a.png"]` {
		t.Fatalf("根寻址未规范化: %s", queued[0].ImagesJSON)
	}
}

// TestFlushImageInjectionsKeepsPlainPath resolver 缺失 / 解析失败——原样透传。
func TestFlushImageInjectionsKeepsPlainPath(t *testing.T) {
	dir := withSessionDirs(t)
	s := newTestSession(t, dir, "cat-img-plain")
	setTestDogs(s, []*toolDog{
		{Name: "image-inject", ArgsJSON: `{"path":"C:\\pic\\a.png"}`, Result: `{"ok":true}`},
		{Name: "image-inject", ArgsJSON: `{"path":"https://example.com/b.png"}`, Result: `{"ok":true}`},
	})
	s.FlushImageInjections()
	queued := s.Pending.TakeAll(true)
	if len(queued) != 1 {
		t.Fatalf("应产生一条注入: %+v", queued)
	}
	if queued[0].ImagesJSON != `["C:\\pic\\a.png","https://example.com/b.png"]` {
		t.Fatalf("普通路径应原样透传: %s", queued[0].ImagesJSON)
	}
}
