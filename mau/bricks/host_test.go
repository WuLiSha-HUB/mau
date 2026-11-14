package bricks

import (
	"strings"
	"testing"

	"cathome5/mau/runtime"
)

// TestHostRestart 重启登记三态：运行模式闸门 / 模式值域 / 参数面 / 成功落盒。
func TestHostRestart(t *testing.T) {
	db := runtime.NewDataBox()
	// 未标记运行模式——非常驻交互形态按 RESTART_UNSUPPORTED 明示
	out, ok := HostRestart(db, `{"catId":"majordomo"}`, "full")
	if ok || !strings.HasPrefix(out, "ERR|RESTART_UNSUPPORTED|") {
		t.Fatalf("未标记运行模式应拒绝，got ok=%v out=%q", ok, out)
	}
	SetInteractiveRunMode(db)
	// 模式值域
	if out, ok = HostRestart(db, `{"catId":"majordomo"}`, "bogus"); ok || !strings.Contains(out, "重启模式非法") {
		t.Fatalf("非法模式应拒绝，got ok=%v out=%q", ok, out)
	}
	// 参数面零容忍
	if out, ok = HostRestart(db, `{"bogus":1}`, "full"); ok || !strings.Contains(out, "未知参数: bogus") {
		t.Fatalf("未知参数应拒绝，got ok=%v out=%q", ok, out)
	}
	// 成功——A214 头键序 + 摘要行 + 载荷落盒
	out, ok = HostRestart(db, `{"target":"D:\\Mau\\MauOut5","push":"说明"}`, "incr")
	if !ok {
		t.Fatalf("登记应成功，got %q", out)
	}
	wantHead := `{"ok":true,"tool":"restart-incr","mode":"incr","target":"D:\\Mau\\MauOut5","push":true,"scope":""}`
	if !strings.HasPrefix(out, wantHead+"\n") {
		t.Fatalf("A214 头不符：\n got %q\nwant %q", out, wantHead)
	}
	if !strings.Contains(out, "D:\\Mau\\MauOut5 | incr 重启请求已登记") {
		t.Fatalf("摘要行缺失：%q", out)
	}
	payload := db.GetString("global", "host_restart_request")
	mode, target, push, scope := DecodeRestartRequest(payload)
	if mode != "incr" || target != `D:\Mau\MauOut5` || push != "说明" || scope != "" {
		t.Fatalf("载荷落盒不符：mode=%q target=%q push=%q scope=%q", mode, target, push, scope)
	}
	// scope=self——载荷带 scope + 头部带 scope + 正文给出不等待语义
	out, ok = HostRestart(db, `{"catId":"majordomo","scope":"self"}`, "incr")
	if !ok || !strings.Contains(out, `"scope":"self"`) || !strings.Contains(out, "不等待其它猫在途轮") {
		t.Fatalf("scope=self 回执不符：ok=%v out=%q", ok, out)
	}
	if _, _, _, gotScope := DecodeRestartRequest(db.GetString("global", "host_restart_request")); gotScope != "self" {
		t.Fatalf("scope 应落盒为 self，got %q", gotScope)
	}
	// scope 值域——非法拒绝
	if out, ok := HostRestart(db, `{"scope":"all-cats"}`, "full"); ok || !strings.Contains(out, "scope 非法") {
		t.Fatalf("非法 scope 应拒绝，got ok=%v out=%q", ok, out)
	}
	// 空 target——摘要按受控根兜底文案
	out, ok = HostRestart(db, `{"catId":"majordomo"}`, "host")
	if !ok || !strings.Contains(out, "受控根 mauout5") {
		t.Fatalf("空 target 兜底文案不符：ok=%v out=%q", ok, out)
	}
}

// TestHostRestartNilDB 数据中台未注入——具名 ERR。
func TestHostRestartNilDB(t *testing.T) {
	out, ok := HostRestart(nil, `{"catId":"majordomo"}`, "full")
	if ok || !strings.HasPrefix(out, "ERR|HOSTCMD_NO_SERVICE|") {
		t.Fatalf("nil db 应具名拒绝，got ok=%v out=%q", ok, out)
	}
}

// TestHostCommand 管理指令三态：参数面 / 服务缺失 / ERR 直返 / 成功包装。
func TestHostCommand(t *testing.T) {
	if out, ok := HostCommand(nil, ""); ok || !strings.Contains(out, "工具参数 JSON 解析失败") {
		t.Fatalf("空参数应拒绝，got ok=%v out=%q", ok, out)
	}
	if out, ok := HostCommand(nil, `{"bogus":1}`); ok || !strings.Contains(out, "未知参数: bogus（支持 cmd）") {
		t.Fatalf("未知参数应拒绝，got ok=%v out=%q", ok, out)
	}
	if out, ok := HostCommand(nil, `{"catId":"majordomo"}`); ok || !strings.Contains(out, "缺少参数 cmd") {
		t.Fatalf("缺 cmd 应拒绝，got ok=%v out=%q", ok, out)
	}
	if out, ok := HostCommand(nil, `{"cmd":"cat.list"}`); ok || !strings.HasPrefix(out, "ERR|HOSTCMD_NO_SERVICE|") {
		t.Fatalf("服务未注入应具名拒绝，got ok=%v out=%q", ok, out)
	}
	// ERR 回执原样返回（不包装头）
	execute := func(line string) string {
		if line == "cat.stop majordomo" {
			return "ERR|HOST_RESTARTING|宿主重启中——指令未受理"
		}
		return "cat.list | 1 只猫"
	}
	if out, ok := HostCommand(execute, `{"cmd":"cat.stop majordomo"}`); ok || !strings.HasPrefix(out, "ERR|HOST_RESTARTING|") {
		t.Fatalf("ERR 回执应直返，got ok=%v out=%q", ok, out)
	}
	// 成功——A214 头 + 摘要行 + 回执原文
	out, ok := HostCommand(execute, `{"cmd":"cat.list"}`)
	if !ok {
		t.Fatalf("成功调用失败：%q", out)
	}
	want := `{"ok":true,"tool":"majordomo-cmd"}` + "\n" + "宿主指令已执行" + "\n" + "cat.list | 1 只猫"
	if out != want {
		t.Fatalf("成功回执不符：\n got %q\nwant %q", out, want)
	}
}

// TestHostCatInfo 全猫状态：空参放行 / 未知参数拒绝 / 服务缺失 / 回执直返。
func TestHostCatInfo(t *testing.T) {
	reply := "{\r\n  \"ok\": true\r\n}"
	// 空参 = 无参数（C# 同口径）
	execute := func(line string) string {
		if line != "cat.info" {
			t.Errorf("应执行 cat.info，got %q", line)
		}
		return reply
	}
	out, ok := HostCatInfo(execute, "")
	if !ok || out != reply {
		t.Fatalf("空参应按原样返回，got ok=%v out=%q", ok, out)
	}
	out, ok = HostCatInfo(execute, `{"catId":"majordomo"}`)
	if !ok || out != reply {
		t.Fatalf("catId 保留键应放行，got ok=%v out=%q", ok, out)
	}
	if out, ok := HostCatInfo(execute, `{"bogus":1}`); ok || !strings.Contains(out, "未知参数: bogus（本工具无参数）") {
		t.Fatalf("未知参数应拒绝，got ok=%v out=%q", ok, out)
	}
	if out, ok := HostCatInfo(nil, ""); ok || !strings.HasPrefix(out, "ERR|HOSTCMD_NO_SERVICE|") {
		t.Fatalf("服务未注入应具名拒绝，got ok=%v out=%q", ok, out)
	}
	// ERR 回执——按工具失败返回
	if out, ok := HostCatInfo(func(string) string { return "ERR|EMPTY_RESULT|宿主指令无回执" }, ""); ok || !strings.HasPrefix(out, "ERR|EMPTY_RESULT|") {
		t.Fatalf("ERR 回执应按失败返回，got ok=%v out=%q", ok, out)
	}
}

// TestDecodeRestartRequest 载荷解析——四字段 / 空串 / 非法 JSON / 缺字段。
func TestDecodeRestartRequest(t *testing.T) {
	mode, target, push, scope := DecodeRestartRequest(`{"mode":"incr","target":"D:\\x","push":"p","scope":"self"}`)
	if mode != "incr" || target != `D:\x` || push != "p" || scope != "self" {
		t.Fatalf("四字段解析不符：%q %q %q %q", mode, target, push, scope)
	}
	if mode, target, push, scope := DecodeRestartRequest(""); mode != "" || target != "" || push != "" || scope != "" {
		t.Fatalf("空串应按零值：%q %q %q %q", mode, target, push, scope)
	}
	if mode, target, push, scope := DecodeRestartRequest("not-json"); mode != "" || target != "" || push != "" || scope != "" {
		t.Fatalf("非法 JSON 应按零值：%q %q %q %q", mode, target, push, scope)
	}
	if mode, _, _, scope := DecodeRestartRequest(`{"target":"x"}`); mode != "" || scope != "" {
		t.Fatalf("缺 mode/scope 应为空：%q %q", mode, scope)
	}
}
