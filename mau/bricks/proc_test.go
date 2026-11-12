package bricks

// ProcCat 四件积木与进程槽服务单测——参数面 / 路径边界 / 台账 / 实机执行。
//
// 实机用例以系统目录作可写受控根（cmd.exe 在根内），只跑短命进程；
// 环境缺 SystemRoot 时跳过实机用例（参数面与台账用例不依赖系统目录）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"cathome5/mau/fs"
)

// procTestSystemRoot 取系统目录（cmd.exe 所在），缺失返回空串。
func procTestSystemRoot() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		return ""
	}
	dir := filepath.Join(root, "System32")
	if info, err := os.Stat(filepath.Join(dir, "cmd.exe")); err != nil || info.IsDir() {
		return ""
	}
	return dir
}

// procTestMethod 按方法名分派（四件统一测参数面）。
func procTestMethod(svc *ProcService, method, argsJSON string) string {
	switch method {
	case "run":
		return svc.Run(argsJSON)
	case "start":
		return svc.Start(argsJSON)
	case "stop":
		return svc.Stop(argsJSON)
	case "list":
		return svc.List(argsJSON)
	}
	return ""
}

// TestProcBricksArgs 四件积木参数面 + 服务未注入。
func TestProcBricksArgs(t *testing.T) {
	if out, ok := ProcRun(nil, ""); ok || out != "ERR|PROC_BAD_ARGS|缺少参数 path" {
		t.Fatalf("ProcRun 空载荷 = %q / %v", out, ok)
	}
	if out, ok := ProcStart(nil, ""); ok || out != "ERR|PROC_BAD_ARGS|缺少参数 path" {
		t.Fatalf("ProcStart 空载荷 = %q / %v", out, ok)
	}
	if out, ok := ProcStop(nil, ""); ok || out != "ERR|PROC_BAD_ARGS|缺少参数 id" {
		t.Fatalf("ProcStop 空载荷 = %q / %v", out, ok)
	}
	if out, ok := ProcRun(nil, `{"path":"x"}`); ok || out != "ERR|PROC_NO_SERVICE|宿主未注入 IProcService" {
		t.Fatalf("ProcRun 无服务 = %q / %v", out, ok)
	}
	if out, ok := ProcStart(nil, `{"path":"x"}`); ok || out != "ERR|PROC_NO_SERVICE|宿主未注入 IProcService" {
		t.Fatalf("ProcStart 无服务 = %q / %v", out, ok)
	}
	if out, ok := ProcStop(nil, `{"id":"1"}`); ok || out != "ERR|PROC_NO_SERVICE|宿主未注入 IProcService" {
		t.Fatalf("ProcStop 无服务 = %q / %v", out, ok)
	}
	if out, ok := ProcList(nil, ""); ok || out != "ERR|PROC_NO_SERVICE|宿主未注入 IProcService" {
		t.Fatalf("ProcList 无服务 = %q / %v", out, ok)
	}
}

// TestProcServiceArgs 服务参数面——未知键 / 缺必填 / window / ready 三态。
func TestProcServiceArgs(t *testing.T) {
	sysDir := procTestSystemRoot()
	if sysDir == "" {
		t.Skip("系统目录不可用——跳过服务参数面（依赖真实 exe 路径校验）")
	}
	svc := NewProcService([]fs.Root{{ID: "system", Path: sysDir, Writable: true}}, t.TempDir(), nil)
	cases := []struct{ method, args, want string }{
		{"run", "", "ERR|BAD_ARGS|工具参数 JSON 解析失败（LLM 生成参数可能被截断——超长内容请分段写入）"},
		{"run", `{"path":"system:cmd.exe","bogus":1}`, "ERR|BAD_ARGS|未知参数: bogus（支持 path args cwd timeout_ms）"},
		{"run", `{}`, "ERR|BAD_ARGS|缺参数 path（必填：path）"},
		{"start", `{"path":"system:cmd.exe","window":"bogus"}`, "ERR|PROC_BAD_ARGS|window 非法值: bogus（hidden | normal）"},
		{"start", `{"path":"system:cmd.exe","ready":"port"}`, "ERR|PROC_BAD_ARGS|ready 非法形态: port（第一版只支持 port:<n>）"},
		{"start", `{"path":"system:cmd.exe","ready":"port:70000"}`, "ERR|PROC_BAD_ARGS|ready 端口非法: port:70000"},
		{"stop", `{}`, "ERR|BAD_ARGS|缺参数 id（必填：id）"},
		{"stop", `{"id":"nope"}`, "ERR|PROC_NOT_FOUND|台账内无此 id 且非数字 pid：nope"},
		{"list", `{"bogus":1}`, "ERR|BAD_ARGS|未知参数: bogus（本工具无参数）"},
	}
	for _, c := range cases {
		if got := procTestMethod(svc, c.method, c.args); got != c.want {
			t.Fatalf("%s %s = %q，期望 %q", c.method, c.args, got, c.want)
		}
	}
}

// TestProcServiceResolveExe 路径边界——未知根 / 越界 / 非 exe / 不存在 / 硬拦宿主与 SetUp。
func TestProcServiceResolveExe(t *testing.T) {
	sysDir := procTestSystemRoot()
	if sysDir == "" {
		t.Skip("系统目录不可用——跳过路径边界用例")
	}
	svc := NewProcService([]fs.Root{
		{ID: "system", Path: sysDir, Writable: true},
		{ID: "readonly", Path: t.TempDir(), Writable: false},
	}, t.TempDir(), nil)
	cases := []struct{ args, wantPrefix string }{
		{`{"path":"nope:x.exe"}`, "ERR|PROC_BAD_PATH|路径非法: 未知受控根 id: nope"},
		{`{"path":"C:\\Windows\\explorer.exe"}`, "ERR|PROC_OUT_OF_ROOT|可执行文件超出受控根范围："},
		{`{"path":"system:drivers"}`, "ERR|PROC_BAD_PATH|第一版只支持 .exe："},
		{`{"path":"system:nope.exe"}`, "ERR|PROC_NOT_FOUND|文件不存在："},
	}
	for _, c := range cases {
		if got := svc.Run(c.args); !strings.HasPrefix(got, c.wantPrefix) {
			t.Fatalf("run %s = %q，期望前缀 %q", c.args, got, c.wantPrefix)
		}
	}
	// 硬拦面——临时可写根内放同名空文件（Stat 过再判硬拦）
	forbiddenRoot := t.TempDir()
	for _, name := range []string{"CatHome4.exe", "SetUp.exe"} {
		if err := os.WriteFile(filepath.Join(forbiddenRoot, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("准备硬拦文件失败: %v", err)
		}
	}
	blocked := NewProcService([]fs.Root{{ID: "workspace", Path: forbiddenRoot, Writable: true}}, t.TempDir(), nil)
	for _, name := range []string{"CatHome4.exe", "SetUp.exe"} {
		got := blocked.Run(`{"path":"workspace:` + name + `"}`)
		want := "ERR|PROC_FORBIDDEN|" + name + " 不可经 proc 工具起——宿主重启走 restart-*，部署走 mau-setup"
		if got != want {
			t.Fatalf("硬拦 %s = %q，期望 %q", name, got, want)
		}
	}
}

// TestProcServiceLedger 台账落盘 / 读取 / 坏条目自愈 / List 形状。
func TestProcServiceLedger(t *testing.T) {
	runtimeDir := t.TempDir()
	svc := NewProcService([]fs.Root{{ID: "workspace", Path: t.TempDir(), Writable: true}}, runtimeDir, nil)
	// 空台账
	if got := svc.List(""); got != `{"count":0,"alive":0,"procs":[]}` {
		t.Fatalf("空台账 List = %q", got)
	}
	// 写入两条（一条坏条目）
	raw := `[{"ID":"p100","Path":"C:\\x\\a.exe","Args":"","Cwd":"","Pid":100,"StartedAt":"2026-11-12 01:00:00","Window":"hidden","ReadyPort":0,"Ready":false},` +
		`{"ID":"","Path":"","Args":"","Cwd":"","Pid":0,"StartedAt":"","Window":"hidden","ReadyPort":0,"Ready":false}]`
	if err := os.WriteFile(filepath.Join(runtimeDir, procLedgerFileName), []byte(raw), 0o644); err != nil {
		t.Fatalf("写台账失败: %v", err)
	}
	got := svc.List("")
	var payload struct {
		Count int `json:"count"`
		Alive int `json:"alive"`
		Procs []struct {
			ID     string `json:"id"`
			Pid    int    `json:"pid"`
			Alive  bool   `json:"alive"`
			Path   string `json:"path"`
			Window string `json:"window"`
		} `json:"procs"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("List 回执非 JSON: %v / %q", err, got)
	}
	if payload.Count != 1 || len(payload.Procs) != 1 || payload.Procs[0].ID != "p100" {
		t.Fatalf("坏条目未自愈或条目丢失: %+v", payload)
	}
	if payload.Procs[0].Pid != 100 || payload.Procs[0].Alive {
		t.Fatalf("条目字段不符: %+v", payload.Procs[0])
	}
	// 损坏台账——按空表
	if err := os.WriteFile(filepath.Join(runtimeDir, procLedgerFileName), []byte("{bad"), 0o644); err != nil {
		t.Fatalf("写坏台账失败: %v", err)
	}
	if got := svc.List(""); got != `{"count":0,"alive":0,"procs":[]}` {
		t.Fatalf("坏台账 List = %q", got)
	}
}

// TestProcServiceRunReal 实机同步执行——cmd.exe /c echo hello。
func TestProcServiceRunReal(t *testing.T) {
	sysDir := procTestSystemRoot()
	if sysDir == "" {
		t.Skip("系统目录不可用——跳过实机执行用例")
	}
	svc := NewProcService([]fs.Root{{ID: "system", Path: sysDir, Writable: true}}, t.TempDir(), nil)
	got := svc.Run(`{"path":"system:cmd.exe","args":"[\"/c\",\"echo hello\"]"}`)
	var payload struct {
		Exit        int    `json:"exit"`
		Timeout     bool   `json:"timeout"`
		Stdout      string `json:"stdout"`
		Stderr      string `json:"stderr"`
		StdoutLines int    `json:"stdoutLines"`
		Truncated   bool   `json:"truncated"`
		ElapsedMs   int64  `json:"elapsedMs"`
		Path        string `json:"path"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("Run 回执非 JSON: %v / %q", err, got)
	}
	if payload.Exit != 0 || payload.Timeout {
		t.Fatalf("Run 退出异常: %+v", payload)
	}
	if !strings.Contains(payload.Stdout, "hello") || payload.StdoutLines < 1 {
		t.Fatalf("Run 输出不符: %q / lines=%d", payload.Stdout, payload.StdoutLines)
	}
	if payload.Truncated || payload.Path == "" {
		t.Fatalf("Run 截断/路径字段不符: %+v", payload)
	}
}

// TestProcServiceStartListStopReal 实机脱手起进程 → List 活体 → Stop 强杀。
func TestProcServiceStartListStopReal(t *testing.T) {
	sysDir := procTestSystemRoot()
	if sysDir == "" {
		t.Skip("系统目录不可用——跳过实机进程槽用例")
	}
	svc := NewProcService([]fs.Root{{ID: "system", Path: sysDir, Writable: true}}, t.TempDir(), nil)
	started := svc.Start(`{"path":"system:cmd.exe","args":"[\"/c\",\"ping -n 30 127.0.0.1\"]","window":"hidden"}`)
	var startPayload struct {
		ID      string `json:"id"`
		Pid     int    `json:"pid"`
		Started bool   `json:"started"`
		Ready   bool   `json:"ready"`
	}
	if err := json.Unmarshal([]byte(started), &startPayload); err != nil {
		t.Fatalf("Start 回执非 JSON: %v / %q", err, started)
	}
	if !startPayload.Started || startPayload.Pid <= 0 || startPayload.ID != "p"+strconv.Itoa(startPayload.Pid) {
		t.Fatalf("Start 回执不符: %+v", startPayload)
	}
	defer func() {
		// 兜底清理（用例失败时也不留进程）
		svc.Stop(`{"id":"` + startPayload.ID + `","force":true}`)
	}()
	time.Sleep(200 * time.Millisecond)
	list := svc.List("")
	if !strings.Contains(list, `"alive":1`) || !strings.Contains(list, startPayload.ID) {
		t.Fatalf("List 未反映活体进程: %q", list)
	}
	stopped := svc.Stop(`{"id":"` + startPayload.ID + `","force":true}`)
	var stopPayload struct {
		ID      string `json:"id"`
		Pid     int    `json:"pid"`
		Stopped bool   `json:"stopped"`
		Mode    string `json:"mode"`
	}
	if err := json.Unmarshal([]byte(stopped), &stopPayload); err != nil {
		t.Fatalf("Stop 回执非 JSON: %v / %q", err, stopped)
	}
	if !stopPayload.Stopped || stopPayload.Mode != "kill" || stopPayload.Pid != startPayload.Pid {
		t.Fatalf("Stop 回执不符: %+v", stopPayload)
	}
	// 台账条目已移除
	if got := svc.List(""); got != `{"count":0,"alive":0,"procs":[]}` {
		t.Fatalf("Stop 后台账未清理: %q", got)
	}
}

// TestProcServiceRunTimeout 实机超时——限 200ms 跑长命令，回执 timeout=true / exit=-1。
func TestProcServiceRunTimeout(t *testing.T) {
	sysDir := procTestSystemRoot()
	if sysDir == "" {
		t.Skip("系统目录不可用——跳过超时用例")
	}
	svc := NewProcService([]fs.Root{{ID: "system", Path: sysDir, Writable: true}}, t.TempDir(), nil)
	got := svc.Run(`{"path":"system:cmd.exe","args":"[\"/c\",\"ping -n 30 127.0.0.1\"]","timeout_ms":200}`)
	var payload struct {
		Exit      int   `json:"exit"`
		Timeout   bool  `json:"timeout"`
		ElapsedMs int64 `json:"elapsedMs"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("超时回执非 JSON: %v / %q", err, got)
	}
	if !payload.Timeout || payload.Exit != -1 || payload.ElapsedMs < 200 {
		t.Fatalf("超时回执不符: %+v", payload)
	}
}

// TestProcServicePortProbe 端口探测——未监听端口在短超时内回 false。
func TestProcServicePortProbe(t *testing.T) {
	if procWaitPort(1, 300) {
		t.Fatal("端口 1 不应就绪")
	}
}
