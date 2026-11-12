package session

// timeback 浏览器生命周期钩子测试——开锚预热 / 回收关闭。

import (
	"strings"
	"testing"
)

// fakeBrowserLifecycle 记录浏览器生命周期钩子调用。
type fakeBrowserLifecycle struct {
	// prepares 预热调用记录（catId）
	prepares []string
	// closes 关闭调用记录（catId）
	closes []string
}

// Prepare 记录预热。
func (f *fakeBrowserLifecycle) Prepare(catID string) { f.prepares = append(f.prepares, catID) }

// Close 记录关闭。
func (f *fakeBrowserLifecycle) Close(catID string) { f.closes = append(f.closes, catID) }

// TestTimebackBrowserLifecycle 验证 browser_vision 开锚预热 / 回收关闭 / 其它类型不预热。
func TestTimebackBrowserLifecycle(t *testing.T) {
	h := newTBHarness(t, "")
	lifecycle := &fakeBrowserLifecycle{}
	h.tb.Browser = lifecycle
	h.msgs = []Message{user("看页面", 1), decl("timeback-start", 2)}
	if got := h.tb.Start(TimebackBrowserVision, "页面取证"); !strings.HasPrefix(got, `{"ok":true,"tool":"timeback-start"`) {
		t.Fatalf("start 回执不符: %s", got)
	}
	if len(lifecycle.prepares) != 1 || lifecycle.prepares[0] != "tb-session" {
		t.Fatalf("开锚未预热浏览器: %+v", lifecycle.prepares)
	}
	// 非 browser_vision 类型不预热
	other := newTBHarness(t, "")
	otherLifecycle := &fakeBrowserLifecycle{}
	other.tb.Browser = otherLifecycle
	other.msgs = []Message{user("查文本", 1), decl("timeback-start", 2)}
	other.tb.Start(TimebackTextSearch, "文本检索")
	if len(otherLifecycle.prepares) != 0 {
		t.Fatalf("text_search 不应预热浏览器: %+v", otherLifecycle.prepares)
	}
	// 回收 → 关闭
	h.msgs = append(h.msgs, toolResult("browser-open", "ok", 3), decl("timeback-back", 4))
	if got := h.tb.Back("findings"); !strings.HasPrefix(got, `{"ok":true,"tool":"timeback-back"`) {
		t.Fatalf("back 回执不符: %s", got)
	}
	h.tb.Apply()
	if len(lifecycle.closes) != 1 || lifecycle.closes[0] != "tb-session" {
		t.Fatalf("回收未关闭浏览器: %+v", lifecycle.closes)
	}
	if len(otherLifecycle.closes) != 0 {
		t.Fatalf("未开锚的会话不应关闭浏览器: %+v", otherLifecycle.closes)
	}
}
