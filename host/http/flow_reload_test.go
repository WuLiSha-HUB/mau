package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFlowReloadRouteUnbound 锁住「未绑定即不挂载」——条件注册语义（不是 handler 内判空返回 404）。
func TestFlowReloadRouteUnbound(t *testing.T) {
	server := New(Options{})
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/flow/reload", strings.NewReader(`{"name":"TextCat"}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未绑定时应 404，实际 %d（body=%s）", rec.Code, rec.Body.String())
	}
}

// TestFlowReloadRoutePassesThrough 锁住「HTTP 只是第二入口」——name/mode 原样交执行体，回执原样回。
func TestFlowReloadRoutePassesThrough(t *testing.T) {
	server := New(Options{})
	var gotName, gotMode string
	server.BindFlowReloader(func(name, mode string) string {
		gotName, gotMode = name, mode
		return `{"ok":true,"group":"` + name + `","mode":"` + mode + `","gen":1}`
	})
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/flow/reload",
		strings.NewReader(`{"name":"TextCat","mode":"carry"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	if gotName != "TextCat" || gotMode != "carry" {
		t.Fatalf("参数透传不符：name=%q mode=%q", gotName, gotMode)
	}
	if !strings.Contains(rec.Body.String(), `"gen":1`) {
		t.Fatalf("回执应原样透传，实际 %s", rec.Body.String())
	}
}

// TestFlowReloadRouteRejectsMissingName 锁住「缺组名直接拒」——不猜一个默认组。
func TestFlowReloadRouteRejectsMissingName(t *testing.T) {
	server := New(Options{})
	server.BindFlowReloader(func(name, mode string) string {
		t.Fatalf("缺 name 不应进执行体（name=%q）", name)
		return ""
	})
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/flow/reload", strings.NewReader(`{"mode":"fresh"}`)))
	if !strings.Contains(rec.Body.String(), "BAD_ARGS") {
		t.Fatalf("应回 BAD_ARGS，实际 %s", rec.Body.String())
	}
}

// TestFlowReloadRouteBadBody 锁住「非 JSON 体出声」——不静默当空参。
func TestFlowReloadRouteBadBody(t *testing.T) {
	server := New(Options{})
	server.BindFlowReloader(func(name, mode string) string { return `{"ok":true}` })
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/flow/reload", strings.NewReader(`not-json`)))
	if !strings.Contains(rec.Body.String(), "BAD_BODY") {
		t.Fatalf("应回 BAD_BODY，实际 %s", rec.Body.String())
	}
}
