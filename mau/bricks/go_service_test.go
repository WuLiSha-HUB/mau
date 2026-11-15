package bricks

// GoService 测试——参数面 / 配置缺失 / 越界 / 回执键序。
//
// 🔴 不在单测里真跑 go build（机器路径依赖 + 动辄数十秒）：真链路验证靠工具调用（go-build 真调一次），
// 本文件只覆盖「不依赖外部进程」的判定面。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cathome5/mau/fs"
)

// goMapConfig 是 map 型配置读取面（键 → 值；缺失回空）。
type goMapConfig map[string]string

// get 读配置键。
func (m goMapConfig) get(key string) string { return m[key] }

// TestGoBricksNoService 四件薄壳——服务未注入即报 GO_NO_SERVICE。
func TestGoBricksNoService(t *testing.T) {
	cases := []struct {
		name string
		fn   func(*GoService, string) (string, bool)
	}{
		{"go.build", GoBuild},
		{"go.test", GoTest},
		{"go.vet", GoVet},
		{"go.fmt", GoFmt},
	}
	for _, c := range cases {
		if got, ok := c.fn(nil, "{}"); ok || got != "ERR|GO_NO_SERVICE|宿主未注入 GoService" {
			t.Fatalf("%s 未注入: %s ok=%v", c.name, got, ok)
		}
	}
}

// TestGoServiceNotConfigured 未配置 go.path——明示不可用文案（不回落 PATH）。
func TestGoServiceNotConfigured(t *testing.T) {
	svc := NewGoService([]fs.Root{{ID: "ws", Path: t.TempDir(), Writable: true}}, goMapConfig{}.get, nil)
	for _, got := range []string{svc.Build("{}"), svc.Test("{}"), svc.Vet("{}"), svc.Fmt("{}")} {
		if !strings.HasPrefix(got, "ERR|GO_NOT_CONFIGURED|") {
			t.Fatalf("未配置应报 GO_NOT_CONFIGURED: %s", got)
		}
	}
}

// TestGoServiceToolchainNotFound 配置了不存在的 go.exe——明示路径不存在。
func TestGoServiceToolchainNotFound(t *testing.T) {
	svc := NewGoService([]fs.Root{{ID: "ws", Path: t.TempDir(), Writable: true}},
		goMapConfig{"go.path": `X:\nowhere\go.exe`}.get, nil)
	if got := svc.Build("{}"); !strings.HasPrefix(got, "ERR|GO_TOOLCHAIN_NOT_FOUND|") {
		t.Fatalf("路径不存在应报 GO_TOOLCHAIN_NOT_FOUND: %s", got)
	}
	// gofmt 解析同样走 go.path——先报同一错误，不静默降级
	if got := svc.Fmt("{}"); !strings.HasPrefix(got, "ERR|GO_TOOLCHAIN_NOT_FOUND|") {
		t.Fatalf("gofmt 应随 go.path 报错: %s", got)
	}
}

// TestGoServiceArgsValidation 参数面零容忍——未知键 / 枚举非法一律 BAD_ARGS。
func TestGoServiceArgsValidation(t *testing.T) {
	svc := NewGoService([]fs.Root{{ID: "ws", Path: t.TempDir(), Writable: true}},
		goMapConfig{"go.path": `X:\nowhere\go.exe`}.get, nil)

	if got := svc.Build(`{"bogus":1}`); !strings.HasPrefix(got, "ERR|BAD_ARGS|未知参数: bogus") {
		t.Fatalf("未知参数应拦截: %s", got)
	}
	if got := svc.Test(`{"count1":"yes"}`); !strings.HasPrefix(got, "ERR|GO_BAD_ARGS|count1 非法值: yes") {
		t.Fatalf("count1 非布尔应拦在参数面: %s", got)
	}
	if got := svc.Fmt(`{"mode":"fix"}`); !strings.HasPrefix(got, "ERR|BAD_ARGS|mode 非法值: fix") {
		t.Fatalf("mode 枚举应拦截: %s", got)
	}
	if got := svc.Vet(`{`); !strings.HasPrefix(got, "ERR|BAD_ARGS|") {
		t.Fatalf("非法 JSON 应拦截: %s", got)
	}
}

// fakeToolchain 造一对假 go.exe / gofmt.exe（只判存在——单测不真跑工具链）。
func fakeToolchain(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	goExe := filepath.Join(dir, "go.exe")
	if err := os.WriteFile(goExe, []byte("stub"), 0o644); err != nil {
		t.Fatalf("造 go.exe: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gofmt.exe"), []byte("stub"), 0o644); err != nil {
		t.Fatalf("造 gofmt.exe: %v", err)
	}
	return goExe
}

// TestGoServiceWorkDirOutOfRoot dir 越界——受控根之外的路径一律拒绝。
func TestGoServiceWorkDirOutOfRoot(t *testing.T) {
	root := t.TempDir()
	svc := NewGoService([]fs.Root{{ID: "ws", Path: root, Writable: true}},
		goMapConfig{"go.path": fakeToolchain(t)}.get, nil)
	if got := svc.Build(`{"dir":"D:\\Windows"}`); !strings.HasPrefix(got, "ERR|GO_OUT_OF_ROOT|") {
		t.Fatalf("越界应报 GO_OUT_OF_ROOT: %s", got)
	}
}

// TestGoServiceNoWorkDir 无含 go.mod 的可写根且未给 dir——明示需要 dir。
func TestGoServiceNoWorkDir(t *testing.T) {
	dir := t.TempDir()
	svc := NewGoService([]fs.Root{{ID: "ws", Path: dir, Writable: true}},
		goMapConfig{"go.path": fakeToolchain(t)}.get, nil)
	if svc.defaultWorkDir != "" {
		t.Fatalf("temp 目录不含 go.mod，默认工作目录应为空: %s", svc.defaultWorkDir)
	}
	if got := svc.Build("{}"); !strings.HasPrefix(got, "ERR|GO_NO_WORKDIR|") {
		t.Fatalf("无默认目录且未给 dir 应报 GO_NO_WORKDIR: %s", got)
	}
	// 显式 dir 通过目录解析后落到执行层（假 go.exe 起不来 → GO_LAUNCH，不是 BAD_PATH）
	if got := svc.Build(`{"dir":"ws:"}`); !strings.HasPrefix(got, "ERR|GO_LAUNCH|") {
		t.Fatalf("显式 dir 应通过目录解析并进执行层: %s", got)
	}
}

// TestGoRunJSONShape 回执键序与 ok 语义——ok 是「exit==0 且未超时」的正面陈述。
func TestGoRunJSONShape(t *testing.T) {
	okText := goRunJSON("go-build", `D:\repo`, `D:\go\bin\go.exe`, []string{"build", "./..."}, 0, false, "", "", false, 12)
	wantPrefix := `{"ok":true,"tool":"go-build","dir":"D:\\repo","exit":0,"timeout":false,`
	if !strings.HasPrefix(okText, wantPrefix) {
		t.Fatalf("回执键序不符: %s", okText)
	}
	if !strings.Contains(okText, `"argv":["go.exe","build","./..."]`) {
		t.Fatalf("argv 应含 exe basename 与参数: %s", okText)
	}
	failText := goRunJSON("go-test", `D:\repo`, `D:\go\bin\go.exe`, []string{"test", "./..."}, 1, false, "", "FAIL", false, 30)
	if !strings.HasPrefix(failText, `{"ok":false,`) {
		t.Fatalf("非零退出 ok 应为 false: %s", failText)
	}
	timeoutText := goRunJSON("go-test", `D:\repo`, `D:\go\bin\go.exe`, []string{"test", "./..."}, -1, true, "", "", false, 120000)
	if !strings.HasPrefix(timeoutText, `{"ok":false,`) || !strings.Contains(timeoutText, `"timeout":true`) {
		t.Fatalf("超时 ok 应为 false 且 timeout=true: %s", timeoutText)
	}
}
