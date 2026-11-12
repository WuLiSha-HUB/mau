package bricks

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cathome5/mau/fs"
)

// TestTsBricksArgs 五件参数面 + 模式判定 + 服务未注入。
func TestTsBricksArgs(t *testing.T) {
	// 未知参数
	if got, ok := TsCheck(nil, nil, `{"path":"a.ts","bogus":1}`); ok || !strings.HasPrefix(got, "ERR|BAD_ARGS|未知参数: bogus") {
		t.Fatalf("ts.check 未知参数: %s ok=%v", got, ok)
	}
	// 缺必填
	if got, ok := TsCheck(nil, nil, `{}`); ok || got != "ERR|BAD_ARGS|缺参数 path（必填：path）" {
		t.Fatalf("ts.check 缺 path: %s ok=%v", got, ok)
	}
	// 参数 JSON 非法
	if got, ok := TsCheck(nil, nil, `{`); ok || !strings.HasPrefix(got, "ERR|BAD_ARGS|参数 JSON 解析失败") {
		t.Fatalf("ts.check 非法 JSON: %s ok=%v", got, ok)
	}
	// 服务未注入（svc 为 nil 时路径原样透传）
	if got, ok := TsCheck(nil, nil, `{"path":"a.ts"}`); ok || got != "ERR|TS_NO_SERVICE|宿主未注入 INodeService" {
		t.Fatalf("ts.check 未注入: %s ok=%v", got, ok)
	}
	if got, ok := TsList(nil, nil, `{"file":"a.ts","kind":"class"}`); ok || got != "ERR|TS_NO_SERVICE|宿主未注入 INodeService" {
		t.Fatalf("ts.list 未注入: %s ok=%v", got, ok)
	}
	if got, ok := TsRead(nil, nil, `{"file":"a.ts"}`); ok || got != "ERR|TS_NO_SERVICE|宿主未注入 INodeService" {
		t.Fatalf("ts.read 未注入: %s ok=%v", got, ok)
	}
	if got, ok := TsFindRef(nil, nil, `{"file":"a.ts","symbol":"A"}`); ok || got != "ERR|TS_NO_SERVICE|宿主未注入 INodeService" {
		t.Fatalf("ts.findRef 未注入: %s ok=%v", got, ok)
	}
	// 模式二选一——同时给 / 都不给 / 符号模式 body 空
	if got, ok := TsPatch(nil, nil, `{"file":"a.ts","symbol":"A","old":"x","new":"y"}`); ok ||
		got != "ERR|TS_BAD_ARGS|须二选一——符号模式（symbol + body）或替换模式（old + new）" {
		t.Fatalf("ts.patch 双模式: %s ok=%v", got, ok)
	}
	if got, ok := TsPatch(nil, nil, `{"file":"a.ts"}`); ok ||
		got != "ERR|TS_BAD_ARGS|须二选一——符号模式（symbol + body）或替换模式（old + new）" {
		t.Fatalf("ts.patch 无模式: %s ok=%v", got, ok)
	}
	if got, ok := TsPatch(nil, nil, `{"file":"a.ts","symbol":"A"}`); ok ||
		got != "ERR|TS_BAD_ARGS|符号模式下 body 不可为空" {
		t.Fatalf("ts.patch 空 body: %s ok=%v", got, ok)
	}
	if got, ok := TsPatch(nil, nil, `{"file":"a.ts","old":"x","new":"y"}`); ok || got != "ERR|TS_NO_SERVICE|宿主未注入 INodeService" {
		t.Fatalf("ts.patch 未注入: %s ok=%v", got, ok)
	}
}

// TestExpandTsPath 受控根寻址展开——根寻址 / 未知根 / 越界。
func TestExpandTsPath(t *testing.T) {
	root := t.TempDir()
	svc := &fs.Service{Roots: []fs.Root{{ID: "ws", Path: root, Writable: true}}, Base: root}
	full, errText := expandTsPath(svc, "ws:src/a.ts")
	if errText != "" {
		t.Fatalf("根寻址展开失败: %s", errText)
	}
	want := filepath.Join(root, "src", "a.ts")
	if !strings.EqualFold(full, want) {
		t.Fatalf("展开结果: %s want %s", full, want)
	}
	if _, errText := expandTsPath(svc, "nope:a.ts"); errText != "ERR|TS_BAD_PATH|路径解析失败（nope:a.ts）: "+fs.ErrUnknownID {
		t.Fatalf("未知根错误文案: %s", errText)
	}
	if _, errText := expandTsPath(svc, `D:\outside\a.ts`); errText != "ERR|TS_BAD_PATH|路径解析失败（D:\\outside\\a.ts）: "+fs.ErrOutOfScope {
		t.Fatalf("越界错误文案: %s", errText)
	}
	// svc nil——原样透传（C# WorkspaceConfig 未注入同口径）
	if full, errText := expandTsPath(nil, "rel/a.ts"); errText != "" || full != "rel/a.ts" {
		t.Fatalf("svc nil 透传: %s %s", full, errText)
	}
}

// mapConfig 是配置读取替身。
type mapConfig map[string]string

// Config 读配置。
func (m mapConfig) get(key string) string { return m[key] }

// TestTsServiceConfigErrors 配置面错误五段——未配置解释器 / 未配置脚本 / 越界 / 只读根 / 脚本不存在。
func TestTsServiceConfigErrors(t *testing.T) {
	root := t.TempDir()
	roots := []fs.Root{{ID: "ws", Path: root, Writable: true}, {ID: "ro", Path: filepath.Join(root, "ro"), Writable: false}}
	_ = os.MkdirAll(filepath.Join(root, "ro"), 0o755)

	// 解释器未配置
	svc := NewTsService(roots, mapConfig{}.get, nil)
	if got := svc.Start(); got != "ERR|NODE_NOT_CONFIGURED|Node 解释器未配置——请在配置面填写 node.node_path（node.exe 全路径；获取方式 where node）" {
		t.Fatalf("未配置解释器: %s", got)
	}
	// 解释器路径不存在
	svc = NewTsService(roots, mapConfig{"node.node_path": filepath.Join(root, "node.exe")}.get, nil)
	if got := svc.Start(); got != "ERR|NODE_NODE_NOT_FOUND|配置的解释器路径不存在："+filepath.Join(root, "node.exe")+"——请更新 node.node_path" {
		t.Fatalf("解释器不存在: %s", got)
	}
	// 造一个存在的解释器占位文件 + 脚本未配置
	nodePath := filepath.Join(root, "node.exe")
	_ = os.WriteFile(nodePath, []byte("stub"), 0o644)
	svc = NewTsService(roots, mapConfig{"node.node_path": nodePath}.get, nil)
	if got := svc.Start(); got != "ERR|NODE_SERVICE_NOT_CONFIGURED|服务未配置：ts——请在配置面填写 node.service.ts.script" {
		t.Fatalf("脚本未配置: %s", got)
	}
	// 脚本越界（受控根外）
	svc = NewTsService(roots, mapConfig{
		"node.node_path":             nodePath,
		"node.service.ts.script":     `D:\outside\srv.js`,
		"node.service.ts.timeout_ms": "5000",
	}.get, nil)
	if got := svc.Start(); !strings.HasPrefix(got, "ERR|NODE_SCRIPT_OUTSIDE_ROOT|服务脚本超出受控根：") {
		t.Fatalf("脚本越界: %s", got)
	}
	// 脚本落在只读根
	readonlyScript := filepath.Join(root, "ro", "srv.js")
	_ = os.WriteFile(readonlyScript, []byte("//"), 0o644)
	svc = NewTsService(roots, mapConfig{
		"node.node_path":         nodePath,
		"node.service.ts.script": "ro:srv.js",
	}.get, nil)
	if got := svc.Start(); !strings.HasPrefix(got, "ERR|NODE_SCRIPT_OUTSIDE_ROOT|服务脚本超出受控根：") {
		t.Fatalf("只读根脚本: %s", got)
	}
	// 脚本不存在
	svc = NewTsService(roots, mapConfig{
		"node.node_path":         nodePath,
		"node.service.ts.script": "ws:missing.js",
	}.get, nil)
	if got := svc.Start(); !strings.HasPrefix(got, "ERR|NODE_SCRIPT_NOT_FOUND|服务脚本不存在：") {
		t.Fatalf("脚本不存在: %s", got)
	}
}

// TestTsServiceProtocol 协议往返——测试二进制自身充当 stdio 服务替身（ready 握手 + 回执 id 对齐）。
func TestTsServiceProtocol(t *testing.T) {
	svc := NewTsService(nil, nil, nil)
	svc.mu.Lock()
	svc.resolved = true
	svc.exe = os.Args[0]
	svc.script = "-test.run=TestTsHelperProcess"
	svc.timeoutMs = 5000
	svc.env = []string{"TS_HELPER=1"}
	svc.mu.Unlock()

	reply := svc.Invoke(`{"id":"r1","op":"ping"}`)
	if !strings.Contains(reply, `"id":"r1"`) || !strings.Contains(reply, `"ok":true`) {
		t.Fatalf("协议回执: %s", reply)
	}
	reply = svc.Invoke(`{"id":"r2","op":"echo","value":"中"}`)
	if !strings.Contains(reply, `"id":"r2"`) || !strings.Contains(reply, `"op":"echo"`) {
		t.Fatalf("第二次回执: %s", reply)
	}
	if !svc.Alive() {
		t.Fatalf("服务应存活")
	}
	svc.Stop()
	if svc.Alive() {
		t.Fatalf("Stop 后应不存活")
	}
}

// TestTsHelperProcess 是子进程替身——TS_HELPER=1 时读 stdin 逐行回 JSON。
func TestTsHelperProcess(t *testing.T) {
	if os.Getenv("TS_HELPER") != "1" {
		t.Skip("helper process only")
	}
	out := bufio.NewWriter(os.Stdout)
	fmt.Fprintln(out, `{"event":"ready","pid":1,"version":"helper"}`)
	out.Flush()
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req struct {
			ID string `json:"id"`
			Op string `json:"op"`
		}
		_ = json.Unmarshal([]byte(scanner.Text()), &req)
		reply, _ := json.Marshal(map[string]any{"id": req.ID, "ok": true, "op": req.Op})
		fmt.Fprintln(out, string(reply))
		out.Flush()
	}
	os.Exit(0)
}

// TestExtractJSONID id 提取——字符串 / 数字 / 非 JSON。
func TestExtractJSONID(t *testing.T) {
	if got := extractJSONID(`{"id":"abc","op":"x"}`); got != "abc" {
		t.Fatalf("字符串 id: %s", got)
	}
	if got := extractJSONID(`{"id":7}`); got != "" {
		t.Fatalf("数字 id 应取空: %s", got)
	}
	if got := extractJSONID("not json"); got != "" {
		t.Fatalf("非 JSON: %s", got)
	}
	if got := extractJSONID(stdioEOF); got != "" {
		t.Fatalf("EOF 哨兵: %s", got)
	}
}

// TestMergeRestartedReply restarted 浅合并——保键序 / 非对象包装。
func TestMergeRestartedReply(t *testing.T) {
	got := mergeRestartedReply(`{"ok":true,"id":"r1"}`)
	if got != `{"ok":true,"id":"r1","restarted":true}` {
		t.Fatalf("对象合并: %s", got)
	}
	if got := mergeRestartedReply(`{}`); got != `{"restarted":true}` {
		t.Fatalf("空对象: %s", got)
	}
	got = mergeRestartedReply("plain")
	if got != `{"restarted":true,"reply":"plain"}` {
		t.Fatalf("非对象包装: %s", got)
	}
}

// TestTsTimeoutFromConfig 超时夹取。
func TestTsTimeoutFromConfig(t *testing.T) {
	cases := map[string]int{
		"":       tsDefaultTimeoutMs,
		"abc":    tsDefaultTimeoutMs,
		"0":      tsDefaultTimeoutMs,
		"-5":     tsDefaultTimeoutMs,
		"999":    tsMinTimeoutMs,
		"5000":   5000,
		"120001": tsMaxTimeoutMs,
	}
	for raw, want := range cases {
		if got := tsTimeoutFromConfig(raw); got != want {
			t.Fatalf("timeout %q: %d want %d", raw, got, want)
		}
	}
}
