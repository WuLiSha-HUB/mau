package session

import (
	"strings"
	"testing"
)

// TestExecuteHostReloadArgs host-reload 参数面——cat / name 别名 / mode 透传 / 缺组名拒绝。
func TestExecuteHostReloadArgs(t *testing.T) {
	s := newBuiltinSession(t)
	var gotName, gotMode string
	s.SetFlowRuntime(func(name, mode string) string {
		gotName, gotMode = name, mode
		return "OK|RELOADED"
	}, func() string { return "FLOWS|组 1" })

	cases := []struct{ args, wantName, wantMode string }{
		{`{"cat":"TextCat"}`, "TextCat", ""},
		{`{"cat":"TextCat","mode":"carry"}`, "TextCat", "carry"},
		{`{"name":"FileCat","mode":"drain"}`, "FileCat", "drain"},
	}
	for _, item := range cases {
		gotName, gotMode = "", ""
		if got := s.ExecuteBuiltin("host-reload", item.args); got != "OK|RELOADED" {
			t.Fatalf("args=%s got=%q", item.args, got)
		}
		if gotName != item.wantName || gotMode != item.wantMode {
			t.Fatalf("args=%s 透传不符：name=%q mode=%q", item.args, gotName, gotMode)
		}
	}
	// 缺组名——不猜默认
	if got := s.ExecuteBuiltin("host-reload", `{"mode":"fresh"}`); !strings.Contains(got, "BAD_ARGS") {
		t.Fatalf("缺 cat 应回 BAD_ARGS，实际 %q", got)
	}
	// 参数损坏
	if got := s.ExecuteBuiltin("host-reload", `{坏`); !strings.Contains(got, "BAD_ARGS") {
		t.Fatalf("参数损坏应回 BAD_ARGS，实际 %q", got)
	}
}

// TestExecuteHostReloadUnavailable 未接线——具名 ERR（不静默当成功）。
func TestExecuteHostReloadUnavailable(t *testing.T) {
	s := newBuiltinSession(t)
	if got := s.ExecuteBuiltin("host-reload", `{"cat":"TextCat"}`); !strings.HasPrefix(got, "ERR|FLOW_RELOAD_UNAVAILABLE|") {
		t.Fatalf("未接线文案不符: %s", got)
	}
	if got := s.ExecuteBuiltin("host-flows", `{}`); !strings.HasPrefix(got, "ERR|FLOW_LIST_UNAVAILABLE|") {
		t.Fatalf("未接线文案不符: %s", got)
	}
}

// TestExecuteHostFlows 接线后原样回观测文本。
func TestExecuteHostFlows(t *testing.T) {
	s := newBuiltinSession(t)
	s.SetFlowRuntime(func(name, mode string) string { return "" }, func() string { return "FLOWS|组 1" })
	if got := s.ExecuteBuiltin("host-flows", `{}`); got != "FLOWS|组 1" {
		t.Fatalf("观测文本应原样回，实际 %q", got)
	}
}
