package bricks

import (
	"strings"
	"testing"
)

// stubGroupService 是 GroupService 的测试桩——记录调用方法 / 参数并回预设文本。
type stubGroupService struct {
	// reply 预设回执
	reply string
	// method 最近一次调用的方法名
	method string
	// args 最近一次调用的参数
	args string
}

// Info 记录 group.info 调用。
func (s *stubGroupService) Info(argsJSON string) string {
	s.method, s.args = "Info", argsJSON
	return s.reply
}

// Inbox 记录 group.inbox 调用。
func (s *stubGroupService) Inbox(argsJSON string) string {
	s.method, s.args = "Inbox", argsJSON
	return s.reply
}

// Say 记录 group.say 调用。
func (s *stubGroupService) Say(argsJSON string) string {
	s.method, s.args = "Say", argsJSON
	return s.reply
}

// Send 记录 group.send 调用。
func (s *stubGroupService) Send(argsJSON string) string {
	s.method, s.args = "Send", argsJSON
	return s.reply
}

// NewSession 记录 group.new_session 调用。
func (s *stubGroupService) NewSession(argsJSON string) string {
	s.method, s.args = "NewSession", argsJSON
	return s.reply
}

// CatInfo 记录 group.catinfo 调用。
func (s *stubGroupService) CatInfo(argsJSON string) string {
	s.method, s.args = "CatInfo", argsJSON
	return s.reply
}

// CatCfgGet 记录 group.catcfg_get 调用。
func (s *stubGroupService) CatCfgGet(argsJSON string) string {
	s.method, s.args = "CatCfgGet", argsJSON
	return s.reply
}

// CatCfgSet 记录 group.catcfg_set 调用。
func (s *stubGroupService) CatCfgSet(argsJSON string) string {
	s.method, s.args = "CatCfgSet", argsJSON
	return s.reply
}

// TestGroupBricksNoService GROUP 八件——服务未注入一律 GROUP_NO_SERVICE（C# 同字面量）。
func TestGroupBricksNoService(t *testing.T) {
	cases := []struct {
		name string
		call func(string) (string, bool)
	}{
		{"group.info", func(a string) (string, bool) { return GroupInfo(nil, a) }},
		{"group.inbox", func(a string) (string, bool) { return GroupInbox(nil, a) }},
		{"group.say", func(a string) (string, bool) { return GroupSay(nil, a) }},
		{"group.send", func(a string) (string, bool) { return GroupSend(nil, a) }},
		{"group.new_session", func(a string) (string, bool) { return GroupNewSession(nil, a) }},
		{"group.catinfo", func(a string) (string, bool) { return GroupCatInfo(nil, a) }},
		{"group.catcfg_get", func(a string) (string, bool) { return GroupCatCfgGet(nil, a) }},
		{"group.catcfg_set", func(a string) (string, bool) { return GroupCatCfgSet(nil, a) }},
	}
	for _, tc := range cases {
		reply, ok := tc.call(`{"group":"g1","catId":"majordomo"}`)
		if ok || reply != "ERR|GROUP_NO_SERVICE|群组服务未注入（宿主未接线）" {
			t.Fatalf("%s 未注入服务应报 GROUP_NO_SERVICE：%q/%v", tc.name, reply, ok)
		}
	}
}

// TestGroupBricksEmptyResult GROUP 八件——空回执一律 EMPTY_RESULT（不静默当成功）。
func TestGroupBricksEmptyResult(t *testing.T) {
	svc := &stubGroupService{reply: ""}
	if reply, ok := GroupInfo(svc, `{}`); ok || reply != "ERR|EMPTY_RESULT|群组服务无回执" {
		t.Fatalf("空回执应报 EMPTY_RESULT：%q/%v", reply, ok)
	}
	if reply, ok := GroupCatCfgSet(svc, `{}`); ok || reply != "ERR|EMPTY_RESULT|群组服务无回执" {
		t.Fatalf("空回执应报 EMPTY_RESULT：%q/%v", reply, ok)
	}
}

// TestGroupBricksPassThrough GROUP 八件——ERR| 原文回传且 ok=false；成功回执 ok=true 且参数透传。
func TestGroupBricksPassThrough(t *testing.T) {
	fail := &stubGroupService{reply: "ERR|GROUP_NOT_FOUND|组不存在: g1"}
	if reply, ok := GroupInbox(fail, `{"group":"g1"}`); ok || reply != fail.reply {
		t.Fatalf("ERR| 应原样回传且 ok=false：%q/%v", reply, ok)
	}
	if fail.method != "Inbox" || fail.args != `{"group":"g1"}` {
		t.Fatalf("参数未透传：%q/%q", fail.method, fail.args)
	}
	okSvc := &stubGroupService{reply: `{"ok":true,"tool":"group-info"}`}
	if reply, ok := GroupInfo(okSvc, `{"group":"g1"}`); !ok || !strings.HasPrefix(reply, `{"ok":true`) {
		t.Fatalf("成功回执应 ok=true：%q/%v", reply, ok)
	}
}

// TestGroupBricksMethodRouting GROUP 八件——积木名到服务方法的映射（BRIK-GROUP-001..008 逐件）。
func TestGroupBricksMethodRouting(t *testing.T) {
	cases := []struct {
		want string
		call func(*stubGroupService) (string, bool)
	}{
		{"Info", func(s *stubGroupService) (string, bool) { return GroupInfo(s, "{}") }},
		{"Inbox", func(s *stubGroupService) (string, bool) { return GroupInbox(s, "{}") }},
		{"Say", func(s *stubGroupService) (string, bool) { return GroupSay(s, "{}") }},
		{"Send", func(s *stubGroupService) (string, bool) { return GroupSend(s, "{}") }},
		{"NewSession", func(s *stubGroupService) (string, bool) { return GroupNewSession(s, "{}") }},
		{"CatInfo", func(s *stubGroupService) (string, bool) { return GroupCatInfo(s, "{}") }},
		{"CatCfgGet", func(s *stubGroupService) (string, bool) { return GroupCatCfgGet(s, "{}") }},
		{"CatCfgSet", func(s *stubGroupService) (string, bool) { return GroupCatCfgSet(s, "{}") }},
	}
	for _, tc := range cases {
		svc := &stubGroupService{reply: "ok"}
		tc.call(svc)
		if svc.method != tc.want {
			t.Fatalf("积木应调 %s，实调 %s", tc.want, svc.method)
		}
	}
}
