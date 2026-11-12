package bricks

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cathome5/mau/fs"
)

// TestPyBricksArgs 三件参数面 + 服务未注入（C# 文案逐字；参数校验全在服务层）。
func TestPyBricksArgs(t *testing.T) {
	root := t.TempDir()
	svc := NewPyService([]fs.Root{{ID: "ws", Path: root, Writable: true}}, pyMapConfig{}.get, nil, nil)

	// 空参——C# 积木层文案（先判空，再看服务是否注入）
	if got, ok := PyRun(nil, ""); ok || got != "ERR|PY_BAD_ARGS|缺少参数 script" {
		t.Fatalf("py.run 空参: %s ok=%v", got, ok)
	}
	if got, ok := PyServiceControl(nil, ""); ok || got != "ERR|PY_BAD_ARGS|缺少参数 name" {
		t.Fatalf("py.service 空参: %s ok=%v", got, ok)
	}
	// py.list 空参合法（走默认扫描面；nil 服务先行报未注入）
	if got, ok := PyList(nil, ""); ok || got != "ERR|PY_NO_SERVICE|宿主未注入 IPyService" {
		t.Fatalf("py.list 空参: %s ok=%v", got, ok)
	}
	// 未知键忽略——C# py 三件无 JsonArgs.Validate，未知键不进服务层判定面
	if got, ok := PyRun(svc, `{"script":"ws:missing.py","bogus":1}`); !ok || !strings.HasPrefix(got, "ERR|PY_SCRIPT_NOT_FOUND|脚本不存在：") {
		t.Fatalf("py.run 未知参数应忽略: %s ok=%v", got, ok)
	}
	// 缺必填（服务层文案，不是 Validate 的「缺参数 X（必填：X）」）
	if got, ok := PyRun(svc, `{}`); !ok || got != "ERR|PY_BAD_ARGS|缺少参数 script" {
		t.Fatalf("py.run 缺 script: %s ok=%v", got, ok)
	}
	if got, ok := PyServiceControl(svc, `{"name":"x"}`); !ok || got != "ERR|PY_BAD_ARGS|缺少参数 action（start|stop|status）" {
		t.Fatalf("py.service 缺 action: %s ok=%v", got, ok)
	}
	if got, ok := PyServiceControl(svc, `{"action":"status"}`); !ok || got != "ERR|PY_BAD_ARGS|缺少参数 name" {
		t.Fatalf("py.service 缺 name: %s ok=%v", got, ok)
	}
	// 枚举非法（PY_BAD_ARGS 前缀，由服务层回——C# PyService.Service 同文案）
	if got, ok := PyServiceControl(svc, `{"name":"x","action":"restart"}`); !ok ||
		got != "ERR|PY_BAD_ARGS|action 非法值: restart（start|stop|status）" {
		t.Fatalf("py.service 非法 action: %s ok=%v", got, ok)
	}
	// 参数 JSON 非法——按缺参处理（C# JsonArgs.Get 解析失败回 §PARSE_FAIL§）
	if got, ok := PyRun(svc, `{`); !ok || got != "ERR|PY_BAD_ARGS|缺少参数 script" {
		t.Fatalf("py.run 非法 JSON: %s ok=%v", got, ok)
	}
	// 服务未注入（参数判空已过后）
	if got, ok := PyRun(nil, `{"script":"a.py"}`); ok || got != "ERR|PY_NO_SERVICE|宿主未注入 IPyService" {
		t.Fatalf("py.run 未注入: %s ok=%v", got, ok)
	}
	if got, ok := PyList(nil, `{"dir":"a"}`); ok || got != "ERR|PY_NO_SERVICE|宿主未注入 IPyService" {
		t.Fatalf("py.list 未注入: %s ok=%v", got, ok)
	}
	if got, ok := PyServiceControl(nil, `{"name":"x","action":"status"}`); ok || got != "ERR|PY_NO_SERVICE|宿主未注入 IPyService" {
		t.Fatalf("py.service 未注入: %s ok=%v", got, ok)
	}
}

// TestPyPathAndPythonErrors 路径面 + 解释器面错误文案。
func TestPyPathAndPythonErrors(t *testing.T) {
	root := t.TempDir()
	readonlyRoot := filepath.Join(root, "ro")
	_ = os.MkdirAll(readonlyRoot, 0o755)
	roots := []fs.Root{
		{ID: "ws", Path: root, Writable: true},
		{ID: "ro", Path: readonlyRoot, Writable: false},
	}
	scriptPath := filepath.Join(root, "a.py")
	_ = os.WriteFile(scriptPath, []byte("print(1)\n"), 0o644)

	// 只读根不在可写表内——按未知根拒绝（C# 只认 writable entries）
	svc := NewPyService(roots, pyMapConfig{}.get, nil, nil)
	if got := svc.Run(`{"script":"ro:a.py"}`); !strings.HasPrefix(got, "ERR|PY_BAD_PATH|脚本路径非法: ") {
		t.Fatalf("只读根脚本: %s", got)
	}
	// 越界
	if got := svc.Run(`{"script":"D:\\outside\\a.py"}`); !strings.HasPrefix(got, "ERR|PY_OUT_OF_ROOT|脚本超出受控根范围：") {
		t.Fatalf("越界脚本: %s", got)
	}
	// 不存在
	if got := svc.Run(`{"script":"ws:missing.py"}`); !strings.HasPrefix(got, "ERR|PY_SCRIPT_NOT_FOUND|脚本不存在：") {
		t.Fatalf("脚本不存在: %s", got)
	}
	// 解释器未配置
	if got := svc.Run(`{"script":"ws:a.py"}`); got != "ERR|PY_NOT_CONFIGURED|Python 解释器未配置——请在配置面填写 py.python_path（python.exe 全路径；获取方式 py -0p）" {
		t.Fatalf("未配置解释器: %s", got)
	}
	// 解释器路径不存在
	svc = NewPyService(roots, pyMapConfig{"py.python_path": filepath.Join(root, "python.exe")}.get, nil, nil)
	if got := svc.Run(`{"script":"ws:a.py"}`); !strings.HasPrefix(got, "ERR|PY_PYTHON_NOT_FOUND|配置的解释器路径不存在：") {
		t.Fatalf("解释器不存在: %s", got)
	}
	// 服务脚本未配置
	if got := svc.Service(`{"name":"probe","action":"start"}`); got != "ERR|PY_SERVICE_NOT_CONFIGURED|服务未配置：probe——请在配置面填写 py.service.probe.script" {
		t.Fatalf("服务脚本未配置: %s", got)
	}
	// 服务脚本越界
	svc = NewPyService(roots, pyMapConfig{
		"py.python_path":          filepath.Join(root, "python.exe"),
		"py.service.probe.script": `D:\outside\srv.py`,
	}.get, nil, nil)
	if got := svc.Service(`{"name":"probe","action":"start"}`); !strings.HasPrefix(got, "ERR|PY_SCRIPT_OUTSIDE_ROOT|服务脚本超出受控根：") {
		t.Fatalf("服务脚本越界: %s", got)
	}
	// status（未启动）
	if got := svc.Service(`{"name":"probe","action":"status"}`); got != `{"name":"probe","state":"stopped","alive":false,"note":"服务未启动"}` {
		t.Fatalf("status 未启动: %s", got)
	}
	// stop（未启动）
	if got := svc.Service(`{"name":"probe","action":"stop"}`); got != `{"name":"probe","state":"stopped","alive":false,"note":"服务未启动"}` {
		t.Fatalf("stop 未启动: %s", got)
	}
}

// TestPyListScan 脚本扫描——跳过依赖与构建目录、按上限收敛、受控根寻址。
func TestPyListScan(t *testing.T) {
	root := t.TempDir()
	files := []string{
		"a.py",
		"nested/b.py",
		"bin/skip.py",
		"node_modules/skip.py",
		"__pycache__/skip.py",
		"note.txt",
	}
	for _, name := range files {
		full := filepath.Join(root, "scan", filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte("x"), 0o644)
	}
	svc := NewPyService([]fs.Root{{ID: "ws", Path: root, Writable: true}}, pyMapConfig{}.get, nil, nil)
	reply := svc.List(`{"dir":"ws:scan"}`)
	var got struct {
		Count   int      `json:"count"`
		Scripts []string `json:"scripts"`
	}
	if err := json.Unmarshal([]byte(reply), &got); err != nil {
		t.Fatalf("list 回执不可解析: %s（%v）", reply, err)
	}
	if got.Count != 2 || len(got.Scripts) != 2 {
		t.Fatalf("扫描结果 count=%d scripts=%v", got.Count, got.Scripts)
	}
	if !strings.HasSuffix(got.Scripts[0], "a.py") {
		t.Fatalf("首条应为 a.py: %v", got.Scripts)
	}
	// 目录不存在
	if reply := svc.List(`{"dir":"ws:nope"}`); !strings.HasPrefix(reply, "ERR|PY_DIR_NOT_FOUND|目录不存在：") {
		t.Fatalf("目录不存在: %s", reply)
	}
	// 无 mau 根且未给 dir
	empty := NewPyService(nil, pyMapConfig{}.get, nil, nil)
	if reply := empty.List("{}"); reply != "ERR|PY_NOT_CONFIGURED|默认脚本面缺失（无 mau 根）——请显式给 dir" {
		t.Fatalf("默认扫描面缺失: %s", reply)
	}
}

// TestPyStdioProtocol 替身成功链——ready 握手 / 回执 id / restarted 浅合并 / stop。
func TestPyStdioProtocol(t *testing.T) {
	svc := NewPyService(nil, nil, nil, nil)
	svc.mu.Lock()
	svc.servers["probe"] = pyTestHost("")
	svc.mu.Unlock()

	reply := svc.Invoke("probe", `{"id":"r1","op":"ping"}`)
	if !strings.Contains(reply, `"id":"r1"`) || !strings.Contains(reply, `"ok":true`) {
		t.Fatalf("协议回执: %s", reply)
	}
	// 首次拉起浅合并 restarted
	if !strings.Contains(reply, `"restarted":true`) {
		t.Fatalf("首次回执应标注 restarted: %s", reply)
	}
	reply = svc.Invoke("probe", `{"id":"r2","op":"echo"}`)
	if !strings.Contains(reply, `"id":"r2"`) || strings.Contains(reply, `"restarted":true`) {
		t.Fatalf("第二次回执: %s", reply)
	}
	if !svc.Alive("probe") {
		t.Fatalf("服务应存活")
	}
	if got := svc.Service(`{"name":"probe","action":"stop"}`); got != `{"name":"probe","state":"stopped","alive":false,"note":""}` {
		t.Fatalf("stop 回执: %s", got)
	}
	if svc.Alive("probe") {
		t.Fatalf("stop 后应不存活")
	}
	// 服务名缺失 / 载荷缺失（Invoke 入口参数面）
	if got := svc.Invoke("", "{}"); got != "ERR|PY_BAD_ARGS|缺少参数 name" {
		t.Fatalf("Invoke 缺 name: %s", got)
	}
	if got := svc.Invoke("probe", ""); got != "ERR|PY_BAD_ARGS|缺少参数 payload（请求体 JSON 文本）" {
		t.Fatalf("Invoke 缺 payload: %s", got)
	}
}

// TestPyHostFailureCodes 失败码——PY_START_FAILED（启动即退）/ PY_TIMEOUT（超时不杀）。
func TestPyHostFailureCodes(t *testing.T) {
	host := pyTestHost("exit")
	host.timeoutMs = 3000
	if got := host.start(); !strings.HasPrefix(got, "ERR|PY_START_FAILED|服务进程启动即退出（probe）") {
		t.Fatalf("启动即退: %s", got)
	}

	host = pyTestHost("silent")
	// 先以充足预算完成 ready 握手，再把调用超时压到 200 ms——否则量到的是启动握手超时（PY_READY_TIMEOUT）
	host.timeoutMs = 5000
	if errText := host.start(); errText != "" {
		t.Fatalf("静默替身启动失败: %s", errText)
	}
	host.timeoutMs = 200
	if got := host.invoke(`{"id":"r1"}`); !strings.HasPrefix(got, "ERR|PY_TIMEOUT|服务调用超时（probe，200 ms；进程未杀）") {
		t.Fatalf("超时: %s", got)
	}
	// 超时不杀——进程仍存活
	if !host.alive() {
		t.Fatalf("超时不应杀进程")
	}
	host.stop()
}

// TestPyRunReplyJSON Run 回执八键固定序 + 行数边界。
func TestPyRunReplyJSON(t *testing.T) {
	reply := pyRunJSON(0, false, "a\nb\n", "", `D:\x\a.py`, 12)
	want := `{"exit":0,"timeout":false,"stdout":"a\nb\n","stderr":"","stdoutLines":3,"stderrLines":0,"elapsedMs":12,"script":"D:\\x\\a.py"}`
	if reply != want {
		t.Fatalf("回执字节:\n got %s\nwant %s", reply, want)
	}
	cases := map[string]int{"": 0, "a": 1, "a\n": 2, "a\nb": 2, "\n": 2}
	for text, wantLines := range cases {
		if got := countPyLines(text); got != wantLines {
			t.Fatalf("countLines(%q)=%d want %d", text, got, wantLines)
		}
	}
	// 截断：达上限后不再追加（上限按 UTF-16 码元）
	buffer := &cappedText{cap: 3}
	buffer.appendLine("ab") // 2 + 1 = 3
	buffer.appendLine("cd") // 已达上限，丢弃
	if got := buffer.String(); got != "ab\n" {
		t.Fatalf("截断结果: %q", got)
	}
	// 中文按单码元、emoji 按双码元
	buffer = &cappedText{cap: 2}
	buffer.appendLine("🔴") // 2 码元 + 换行 → 3
	if got := buffer.String(); got != "🔴\n" {
		t.Fatalf("emoji 截断: %q", got)
	}
}

// TestPyAutostart follow 自启——声明面枚举 + 未配置服务记 errors。
func TestPyAutostart(t *testing.T) {
	svc := NewPyService(nil, pyMapConfig{}.get, nil, nil)
	if got := svc.Autostart(); got != "ERR|PY_NOT_CONFIGURED|配置存储未就绪——无法读取 py 服务声明" {
		t.Fatalf("配置存储未就绪: %s", got)
	}
	values := func() [][2]string {
		return [][2]string{
			{"py.python_path", `D:\x\python.exe`},
			{"py.service.kokoro.script", "mau:tools/kokoro.py"},
			{"py.service.kokoro.follow", "false"},
		}
	}
	svc = NewPyService(nil, pyMapConfig{"py.service.kokoro.follow": "false"}.get, values, nil)
	if got := svc.Autostart(); got != `{"started":[],"skipped":["kokoro"],"errors":[]}` {
		t.Fatalf("follow=false 应跳过: %s", got)
	}
	svc = NewPyService(nil, pyMapConfig{"py.service.kokoro.follow": "true"}.get, values, nil)
	if got := svc.Autostart(); got != `{"started":[],"skipped":[],"errors":["kokoro"]}` {
		t.Fatalf("follow=true 但无解释器应记 errors: %s", got)
	}
}

// pyMapConfig 是配置读取替身。
type pyMapConfig map[string]string

// get 读配置。
func (m pyMapConfig) get(key string) string { return m[key] }

// pyTestHost 构造指向本测试二进制的 stdio 替身宿主（mode："" 正常 / exit 立退 / silent 不回话）。
func pyTestHost(mode string) *stdioHost {
	host := &stdioHost{
		name:      "probe",
		errPrefix: "PY",
		exe:       os.Args[0],
		script:    "-test.run=TestPyHelperProcess",
		timeoutMs: 5000,
		env:       []string{"PY_HELPER=1"},
	}
	if mode != "" {
		host.env = append(host.env, "PY_HELPER_MODE="+mode)
	}
	return host
}

// TestPyHelperProcess 是子进程替身——PY_HELPER=1 时首行 ready，逐行回 JSON。
func TestPyHelperProcess(t *testing.T) {
	if os.Getenv("PY_HELPER") != "1" {
		t.Skip("helper process only")
	}
	switch os.Getenv("PY_HELPER_MODE") {
	case "exit":
		os.Exit(0)
	case "silent":
		// 先完成 ready 握手，再只读不回——用于量「调用超时」而不是「启动握手超时」
		fmt.Fprintln(os.Stdout, `{"event":"ready","pid":1}`)
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	out := bufio.NewWriter(os.Stdout)
	fmt.Fprintln(out, `{"event":"ready","pid":1}`)
	out.Flush()
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal([]byte(scanner.Text()), &req)
		reply, _ := json.Marshal(map[string]any{"id": req.ID, "ok": true})
		fmt.Fprintln(out, string(reply))
		out.Flush()
	}
	os.Exit(0)
}
