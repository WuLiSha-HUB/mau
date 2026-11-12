package session

import "testing"

// TestSelfTagMountClear 取消挂载——项目 / 模式 / 清单三项整组清空。
func TestSelfTagMountClear(t *testing.T) {
	s := NewSession("", NewContext(), nil, nil)
	s.CatKey = "majordomo"
	s.DisplayName = "Majordomo"
	s.sessionID = "sid-1"
	s.SetClientID("cat-home5/0.7.13")
	s.SetSelfTagMount("CatHome5", "expert", "CatHome,CatHome5")
	tag := s.SelfTag()
	if tag.CatID != "majordomo" || tag.CatName != "Majordomo" || tag.SessionID != "sid-1" {
		t.Fatalf("猫身份字段不符: %+v", tag)
	}
	if tag.Project != "CatHome5" || tag.MountMode != "expert" || tag.Jobs != "CatHome,CatHome5" {
		t.Fatalf("挂载字段不符: %+v", tag)
	}
	if tag.ClientID != "cat-home5/0.7.13" {
		t.Fatalf("客户端标识不符: %q", tag.ClientID)
	}
	s.SetSelfTagMount("", "expert", "CatHome")
	tag = s.SelfTag()
	if tag.Project != "" || tag.MountMode != "" || tag.Jobs != "" {
		t.Fatalf("取消挂载应清空三项: %+v", tag)
	}
}

// TestSelfTagDefaultClientID 默认客户端标识——装配层未覆盖时仍非空（C# 默认 cat-home4 同口径）。
func TestSelfTagDefaultClientID(t *testing.T) {
	s := NewSession("", NewContext(), nil, nil)
	if got := s.SelfTag().ClientID; got != "cat-home5" {
		t.Fatalf("默认客户端标识 %q，期望 cat-home5", got)
	}
	s.SetClientID("")
	if got := s.SelfTag().ClientID; got != "cat-home5" {
		t.Fatalf("空值不应覆盖默认: %q", got)
	}
}
