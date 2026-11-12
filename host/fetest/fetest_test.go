package fetest

// fetest_test —— 目录锚定 / 端口探测跳过 / 依赖缺失降级（不起真实 node）。

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// writeServerJS 在 dir 下放置 server.js（模拟测试目录命中）。
func writeServerJS(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.js"), []byte("// stub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// listenPort 起一个保持监听的回环端口；返回端口与关闭函数。
func listenPort(t *testing.T) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln.Addr().(*net.TCPAddr).Port, func() { _ = ln.Close() }
}

// TestResolveDirExplicit 显式覆盖最优先。
func TestResolveDirExplicit(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "custom-tests")
	writeServerJS(t, dir)
	if got := ResolveDir(Config{Root: dir}); got != dir {
		t.Fatalf("解析 = %q，期望 %q", got, dir)
	}
}

// TestResolveDirMauRoot workspace 的 mau 根候选（<mau>/CatHome4/html/tests）。
func TestResolveDirMauRoot(t *testing.T) {
	mauRoot := t.TempDir()
	dir := filepath.Join(mauRoot, "CatHome4", "html", "tests")
	writeServerJS(t, dir)
	if got := ResolveDir(Config{MauRoot: mauRoot}); got != dir {
		t.Fatalf("解析 = %q，期望 %q", got, dir)
	}
}

// TestResolveDirExeFallback 部署区兜底（<exeDir>/html/tests）。
func TestResolveDirExeFallback(t *testing.T) {
	exeDir := t.TempDir()
	dir := filepath.Join(exeDir, "html", "tests")
	writeServerJS(t, dir)
	if got := ResolveDir(Config{ExeDir: exeDir}); got != dir {
		t.Fatalf("解析 = %q，期望 %q", got, dir)
	}
}

// TestResolveDirNone 无候选命中——空串。
func TestResolveDirNone(t *testing.T) {
	if got := ResolveDir(Config{ExeDir: t.TempDir()}); got != "" {
		t.Fatalf("解析 = %q，期望空串", got)
	}
}

// TestEnsureSkipsWhenListening 端口已监听——跳过拉起，不返回 Service。
func TestEnsureSkipsWhenListening(t *testing.T) {
	port, closeFn := listenPort(t)
	defer closeFn()
	res := Ensure(Config{Port: port})
	if res.State != StateListening {
		t.Fatalf("State = %q，期望 %q", res.State, StateListening)
	}
	if res.Service != nil {
		t.Fatal("已监听时不应返回 Service")
	}
}

// TestEnsureMissingDeps 有 server.js 无 node_modules——降级（不自举安装）。
func TestEnsureMissingDeps(t *testing.T) {
	dir := t.TempDir()
	writeServerJS(t, dir)
	// 取一个空闲端口（监听后立即释放，保证未被占用）
	port, closeFn := listenPort(t)
	closeFn()
	res := Ensure(Config{Port: port, Root: dir})
	if res.State != StateMissingDeps {
		t.Fatalf("State = %q，期望 %q（Detail=%s）", res.State, StateMissingDeps, res.Detail)
	}
	if res.Service != nil {
		t.Fatal("缺依赖时不应返回 Service")
	}
}

// TestEnsureNoDir 未解析到测试目录——StateNoDir。
func TestEnsureNoDir(t *testing.T) {
	port, closeFn := listenPort(t)
	closeFn()
	res := Ensure(Config{Port: port, ExeDir: t.TempDir()})
	if res.State != StateNoDir {
		t.Fatalf("State = %q，期望 %q（Detail=%s）", res.State, StateNoDir, res.Detail)
	}
}

// TestEnsureBadPort 端口未配置——StateFailed。
func TestEnsureBadPort(t *testing.T) {
	res := Ensure(Config{Port: 0})
	if res.State != StateFailed {
		t.Fatalf("State = %q，期望 %q", res.State, StateFailed)
	}
}

// TestPortListening 探测口径——自身监听为真、释放后为假。
func TestPortListening(t *testing.T) {
	port, closeFn := listenPort(t)
	if !portListening(port) {
		t.Fatalf("端口 %d 已监听却探不到", port)
	}
	closeFn()
	if portListening(port) {
		t.Fatalf("端口 %d 已释放却探测为监听", port)
	}
}
