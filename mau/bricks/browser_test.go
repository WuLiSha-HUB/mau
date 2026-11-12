package bricks

// 浏览器服务与积木单测——参数面 / 内核解析 / 端口锚 / 渲染辅助 / CDP 帧往返。

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newPipeWS 建立一条无握手 WebSocket 连接（客户端侧 + 服务端裸连接）。
func newPipeWS() (*wsConn, net.Conn) {
	clientSide, serverSide := net.Pipe()
	return &wsConn{conn: clientSide, reader: bufio.NewReaderSize(clientSide, 1<<16)}, serverSide
}

// readClientFrame 读客户端帧（掩码解码）——测试辅助。
func readClientFrame(conn net.Conn) (byte, []byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return 0, nil, err
	}
	opcode := header[0] & 0x0F
	masked := header[1]&0x80 != 0
	length := int64(header[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(conn, ext[:]); err != nil {
			return 0, nil, err
		}
		length = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(conn, ext[:]); err != nil {
			return 0, nil, err
		}
		length = int64(binary.BigEndian.Uint64(ext[:]))
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(conn, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return opcode, payload, nil
}

// writeServerFrame 写服务端帧（不掩码）——测试辅助。
func writeServerFrame(conn net.Conn, opcode byte, payload []byte) error {
	frame := []byte{0x80 | opcode}
	length := len(payload)
	switch {
	case length <= 125:
		frame = append(frame, byte(length))
	case length <= 0xFFFF:
		frame = append(frame, 126, byte(length>>8), byte(length))
	default:
		frame = append(frame, 127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(length))
		frame = append(frame, ext[:]...)
	}
	frame = append(frame, payload...)
	_, err := conn.Write(frame)
	return err
}

// TestCDPCallRoundTrip 验证 id 匹配 + 事件消息丢弃。
func TestCDPCallRoundTrip(t *testing.T) {
	ws, server := newPipeWS()
	defer func() { _ = ws.Close(); _ = server.Close() }()
	cdp := &cdpClient{conn: ws}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			opcode, payload, err := readClientFrame(server)
			if err != nil {
				return
			}
			if opcode != 0x1 {
				continue
			}
			var request struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			_ = json.Unmarshal(payload, &request)
			// 先发一条事件消息（无 id），再发响应——客户端必须忽略事件
			_ = writeServerFrame(server, 0x1, []byte(`{"method":"Page.frameNavigated","params":{}}`))
			response := `{"id":` + strconv.Itoa(request.ID) + `,"result":{"value":"ok"}}`
			if err := writeServerFrame(server, 0x1, []byte(response)); err != nil {
				return
			}
			return
		}
	}()
	root, err := cdp.Call("Runtime.evaluate", `{"expression":"1"}`, 3000)
	if err != nil {
		t.Fatalf("Call 失败: %v", err)
	}
	if !strings.Contains(string(root), `"value":"ok"`) {
		t.Fatalf("响应不符: %s", string(root))
	}
	<-done
}

// TestCDPCallError 验证 error 响应转错误文本。
func TestCDPCallError(t *testing.T) {
	ws, server := newPipeWS()
	defer func() { _ = ws.Close(); _ = server.Close() }()
	cdp := &cdpClient{conn: ws}
	go func() {
		if _, _, err := readClientFrame(server); err != nil {
			return
		}
		_ = writeServerFrame(server, 0x1, []byte(`{"id":1,"error":{"code":-32601,"message":"method not found"}}`))
	}()
	_, err := cdp.Call("Nope.method", "{}", 3000)
	if err == nil || !strings.Contains(err.Error(), "CDP Nope.method 失败") {
		t.Fatalf("错误文本不符: %v", err)
	}
}

// TestCDPCallTimeout 验证无响应时超时判定（cdpTimeoutError）。
func TestCDPCallTimeout(t *testing.T) {
	ws, server := newPipeWS()
	defer func() { _ = ws.Close(); _ = server.Close() }()
	cdp := &cdpClient{conn: ws}
	go func() {
		if _, _, err := readClientFrame(server); err != nil {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}()
	_, err := cdp.Call("Page.navigate", "{}", 150)
	if err == nil || !isCDPTimeout(err) {
		t.Fatalf("应判超时: %v", err)
	}
	if !strings.Contains(err.Error(), "超时（150ms 无响应）") {
		t.Fatalf("超时文本不符: %v", err)
	}
}

// TestEvalRawParse 验证 Runtime.evaluate 结果解析（字符串解包 / 非字符串原文）。
func TestEvalRawParse(t *testing.T) {
	cases := []struct {
		name     string
		response string
		want     string
	}{
		{"字符串", `{"id":1,"result":{"result":{"value":"hello"}}}`, "hello"},
		{"数字", `{"id":1,"result":{"result":{"value":42}}}`, "42"},
		{"对象", `{"id":1,"result":{"result":{"value":{"a":1}}}}`, `{"a":1}`},
		{"缺值", `{"id":1,"result":{"result":{}}}`, ""},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			ws, server := newPipeWS()
			defer func() { _ = ws.Close(); _ = server.Close() }()
			cdp := &cdpClient{conn: ws}
			go func() {
				if _, _, err := readClientFrame(server); err != nil {
					return
				}
				_ = writeServerFrame(server, 0x1, []byte(item.response))
			}()
			got, err := evalRaw(cdp, "1", 3000)
			if err != nil {
				t.Fatalf("evalRaw 失败: %v", err)
			}
			if got != item.want {
				t.Fatalf("取值不符: got=%q want=%q", got, item.want)
			}
		})
	}
}

// TestWaitReady 验证就绪轮询读到 complete 即返回。
func TestWaitReady(t *testing.T) {
	ws, server := newPipeWS()
	defer func() { _ = ws.Close(); _ = server.Close() }()
	cdp := &cdpClient{conn: ws}
	go func() {
		for {
			if _, _, err := readClientFrame(server); err != nil {
				return
			}
			_ = writeServerFrame(server, 0x1, []byte(`{"id":1,"result":{"result":{"value":"complete"}}}`))
		}
	}()
	state, err := waitReady(cdp, 3000)
	if err != nil {
		t.Fatalf("waitReady 失败: %v", err)
	}
	if state != "complete" {
		t.Fatalf("状态不符: %s", state)
	}
}

// TestBrowserOpenArgs 验证 browser-open 参数面（必填 / 未知键 / 无服务）。
func TestBrowserOpenArgs(t *testing.T) {
	if got, ok := BrowserOpen(nil, `{}`); ok || !strings.Contains(got, "缺参数 url") {
		t.Fatalf("缺 url 应失败: ok=%v got=%q", ok, got)
	}
	if got, ok := BrowserOpen(nil, `{"url":"https://x","bogus":1}`); ok || !strings.Contains(got, "未知参数: bogus") {
		t.Fatalf("未知参数应失败: ok=%v got=%q", ok, got)
	}
	if got, ok := BrowserOpen(nil, `{"url":"https://x"}`); ok || got != "ERR|BROWSER_NO_SERVICE|宿主未注入 IBrowserService" {
		t.Fatalf("无服务应失败: ok=%v got=%q", ok, got)
	}
}

// TestBrowserReadMode 验证 browser-read 模式枚举。
func TestBrowserReadMode(t *testing.T) {
	if got, ok := BrowserRead(nil, `{"mode":"bad"}`); ok || !strings.Contains(got, "mode 非法值") {
		t.Fatalf("非法 mode 应失败: ok=%v got=%q", ok, got)
	}
	if got, ok := BrowserRead(nil, `{}`); ok || got != "ERR|BROWSER_NO_SERVICE|宿主未注入 IBrowserService" {
		t.Fatalf("无服务应失败: ok=%v got=%q", ok, got)
	}
}

// TestBrowserEvalArgs 验证 browser-eval 必填。
func TestBrowserEvalArgs(t *testing.T) {
	if got, ok := BrowserEval(nil, `{}`); ok || !strings.Contains(got, "缺参数 expression") {
		t.Fatalf("缺 expression 应失败: ok=%v got=%q", ok, got)
	}
}

// TestBrowserTabsArgs 验证 browser-tabs 动作枚举与 target 必填。
func TestBrowserTabsArgs(t *testing.T) {
	if got, ok := BrowserTabs(nil, `{"action":"bad"}`); ok || !strings.Contains(got, "action 非法值") {
		t.Fatalf("非法 action 应失败: ok=%v got=%q", ok, got)
	}
	if got, ok := BrowserTabs(nil, `{"action":"select"}`); ok || got != "ERR|BROWSER_NO_SERVICE|宿主未注入 IBrowserService" {
		t.Fatalf("无服务应失败: ok=%v got=%q", ok, got)
	}
}

// TestBrowserHeadfulArgs 验证 browser-headful 动作面 + 无服务。
func TestBrowserHeadfulArgs(t *testing.T) {
	if got, ok := BrowserHeadful(nil, `{"action":"bad"}`); ok || got != "ERR|BROWSER_NO_SERVICE|宿主未注入 IBrowserLifecycle" {
		t.Fatalf("无服务应失败: ok=%v got=%q", ok, got)
	}
	if got, ok := BrowserHeadful(nil, `{"action":"open","bogus":1}`); ok || !strings.Contains(got, "未知参数: bogus") {
		t.Fatalf("未知参数应失败: ok=%v got=%q", ok, got)
	}
}

// TestBrowserKernelMissing 验证内核未配置 / 路径不存在的明示错误。
func TestBrowserKernelMissing(t *testing.T) {
	dir := t.TempDir()
	svc := NewBrowserService(dir, func() string { return "" }, nil)
	got := svc.Open("cat1", "https://example.com")
	if !strings.HasPrefix(got, "ERR|BROWSER_KERNEL_MISSING|") {
		t.Fatalf("缺配置应报 KERNEL_MISSING: %q", got)
	}
	missing := filepath.Join(dir, "nope.exe")
	svc = NewBrowserService(dir, func() string { return missing }, nil)
	got = svc.Open("cat1", "https://example.com")
	if !strings.HasPrefix(got, "ERR|BROWSER_KERNEL_NOT_FOUND|") {
		t.Fatalf("路径不存在应报 KERNEL_NOT_FOUND: %q", got)
	}
}

// TestBrowserServiceArgs 验证服务层参数面（catId / url / mode / expression / action）。
func TestBrowserServiceArgs(t *testing.T) {
	svc := NewBrowserService(t.TempDir(), func() string { return "" }, nil)
	if got := svc.Open("cat1", ""); got != "ERR|BROWSER_ARGS|缺少参数 url" {
		t.Fatalf("空 url: %q", got)
	}
	if got := svc.Open("cat1", "ftp://x"); !strings.Contains(got, "url 仅支持 http(s)") {
		t.Fatalf("非 http: %q", got)
	}
	if got := svc.Open("", "https://x"); got != "ERR|BROWSER_ARGS|缺少 catId" {
		t.Fatalf("空 catId: %q", got)
	}
	if got := svc.Read("cat1", "bad"); !strings.Contains(got, "mode 非法（text / ax / links）") {
		t.Fatalf("非法 mode: %q", got)
	}
	if got := svc.Eval("cat1", ""); got != "ERR|BROWSER_ARGS|缺少参数 expression" {
		t.Fatalf("空 expression: %q", got)
	}
	if got := svc.Tabs("cat1", "bad", "", ""); !strings.Contains(got, "action 非法（list / new / select / close）") {
		t.Fatalf("非法 action: %q", got)
	}
	if got := svc.Tabs("cat1", "select", "", ""); !strings.Contains(got, "需要参数 target") {
		t.Fatalf("缺 target: %q", got)
	}
	if got := svc.Headful("", "open", ""); got != "ERR|BROWSER_ARGS|缺少 catId" {
		t.Fatalf("空 catId: %q", got)
	}
	if got := svc.Headful("cat1", "bad", ""); !strings.Contains(got, "action 非法（open / close）") {
		t.Fatalf("非法 action: %q", got)
	}
	if got := svc.Headful("cat1", "open", "ftp://x"); !strings.Contains(got, "url 仅支持 http(s)") {
		t.Fatalf("非 http: %q", got)
	}
}

// TestAnchorPort 验证端口锚读写。
func TestAnchorPort(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "port.txt")
	if port := readAnchorPort(file); port != 0 {
		t.Fatalf("缺失应为 0: %d", port)
	}
	if err := os.WriteFile(file, []byte("9222\r\n/devtools/browser/abc\r\n"), 0o644); err != nil {
		t.Fatalf("写锚失败: %v", err)
	}
	if port := readAnchorPort(file); port != 9222 {
		t.Fatalf("首行端口解析不符: %d", port)
	}
	if err := os.WriteFile(file, []byte("abc"), 0o644); err != nil {
		t.Fatalf("写锚失败: %v", err)
	}
	if port := readAnchorPort(file); port != 0 {
		t.Fatalf("非数字应为 0: %d", port)
	}
	clearPortAnchor(file, func(int, string) {})
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("锚未清理: %v", err)
	}
}

// TestWaitPortFile 验证端口文件轮询。
func TestWaitPortFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "DevToolsActivePort")
	go func() {
		time.Sleep(120 * time.Millisecond)
		_ = os.WriteFile(file, []byte("9300\n"), 0o644)
	}()
	if port := waitPortFile(file, 3000); port != 9300 {
		t.Fatalf("端口轮询不符: %d", port)
	}
}

// TestBrowserPreview 验证摘要预览与截断标注。
func TestBrowserPreview(t *testing.T) {
	short := strings.Repeat("a", 10)
	if got := browserPreview(short, 20); got != short {
		t.Fatalf("短文本不应截断: %q", got)
	}
	long := strings.Repeat("a", 30)
	got := browserPreview(long, 20)
	if !strings.Contains(got, "…[截断 共 30 字符]") {
		t.Fatalf("截断标注缺失: %q", got)
	}
}

// TestBuildLinkPreview 验证链接面预览（前 20 条 + 总数标注）。
func TestBuildLinkPreview(t *testing.T) {
	rows := make([]map[string]string, 0, 25)
	for i := 0; i < 25; i++ {
		rows = append(rows, map[string]string{"t": "标题", "h": "https://x"})
	}
	payload, _ := json.Marshal(rows)
	got := buildLinkPreview(string(payload))
	if !strings.Contains(got, "…[共 25 条，完整清单见落盘文件]") {
		t.Fatalf("总数标注缺失: %q", got)
	}
	if strings.Count(got, "- 标题 → https://x") != 20 {
		t.Fatalf("预览条数不符: %d", strings.Count(got, "- 标题 → https://x"))
	}
	empty := buildLinkPreview(`[{"t":"","h":"https://y"}]`)
	if !strings.Contains(empty, "(无锚文本)") {
		t.Fatalf("空锚文本占位缺失: %q", empty)
	}
}

// TestPngSize 验证 PNG 尺寸读取（IHDR 宽高）。
func TestPngSize(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "x.png")
	head := make([]byte, 24)
	copy(head, []byte{0x89, 'P', 'N', 'G'})
	binary.BigEndian.PutUint32(head[16:20], 1440)
	binary.BigEndian.PutUint32(head[20:24], 900)
	if err := os.WriteFile(file, head, 0o644); err != nil {
		t.Fatalf("写 PNG 失败: %v", err)
	}
	if got := pngSize(file); got != "1440x900" {
		t.Fatalf("尺寸不符: %q", got)
	}
	if got := pngSize(filepath.Join(dir, "missing.png")); got != "?" {
		t.Fatalf("缺失文件应回 ?: %q", got)
	}
}

// TestBuildAxSlim 验证无障碍树精简（ignored / 空名剔除）。
func TestBuildAxSlim(t *testing.T) {
	response := json.RawMessage(`{"result":{"nodes":[
		{"ignored":false,"role":{"value":"button"},"name":{"value":"提交"}},
		{"ignored":true,"role":{"value":"generic"},"name":{"value":"隐藏"}},
		{"ignored":false,"role":{"value":"textbox"},"name":{"value":""}}
	]}}`)
	got := buildAxSlim(response)
	if got != "button: 提交\n" {
		t.Fatalf("精简结果不符: %q", got)
	}
	if got := buildAxSlim(json.RawMessage(`{}`)); got != "" {
		t.Fatalf("缺 nodes 应为空: %q", got)
	}
}

// TestExtractJSONString 验证顶层字段提取。
func TestExtractJSONString(t *testing.T) {
	text := `{"id":"abc","n":1,"url":"https://x"}`
	if got := extractJSONString(text, "id"); got != "abc" {
		t.Fatalf("id 提取不符: %q", got)
	}
	if got := extractJSONString(text, "n"); got != "" {
		t.Fatalf("非字符串应为空: %q", got)
	}
	if got := extractJSONString("bad", "id"); got != "" {
		t.Fatalf("解析失败应为空: %q", got)
	}
}

// TestExtractBetween 验证锚点区间提取。
func TestExtractBetween(t *testing.T) {
	text := "ready=complete | title=标题 | url=https://x | ms=88"
	if got := extractBetween(text, "title=", " | url="); got != "标题" {
		t.Fatalf("title 提取不符: %q", got)
	}
	if got := extractBetween(text, "url=", " | ms="); got != "https://x" {
		t.Fatalf("url 提取不符: %q", got)
	}
	if got := extractBetween(text, "nope=", "|"); got != "" {
		t.Fatalf("缺起始锚应为空: %q", got)
	}
	if got := extractBetween(text, "ms=", "|"); got != "88" {
		t.Fatalf("缺结束锚应取到结尾: %q", got)
	}
}

// TestParseBrowserRowsInto 验证浏览器取值解析（标题必填 / 链接去重 / 上限）。
func TestParseBrowserRowsInto(t *testing.T) {
	payload := `[{"t":"A","u":"https://a","s":"sa"},{"t":"","u":"https://skip"},{"t":"B","u":"https://a"},{"t":"C","u":"https://c"}]`
	var list strings.Builder
	if got := parseBrowserRowsInto(payload, 8, &list); got != 2 {
		t.Fatalf("条数不符: %d", got)
	}
	if !strings.Contains(list.String(), "[1] A") || !strings.Contains(list.String(), "[2] C") {
		t.Fatalf("清单不符: %q", list.String())
	}
	if got := parseBrowserRowsInto("bad", 8, &list); got != 0 {
		t.Fatalf("非法 JSON 应为 0: %d", got)
	}
	if got := parseBrowserRowsInto(`[{"t":"A","u":"https://a"},{"t":"B","u":"https://b"}]`, 1, &list); got != 1 {
		t.Fatalf("上限未生效: %d", got)
	}
}

// TestCaptchaAndGoogleBlocked 验证验证码 / 谷歌拦截判定。
func TestCaptchaAndGoogleBlocked(t *testing.T) {
	if !isBrowserCaptcha("百度安全验证", "") {
		t.Fatal("标题命中应判验证码")
	}
	if !isBrowserCaptcha("", "https://wappass.baidu.com/x") {
		t.Fatal("wappass 应判验证码")
	}
	if isBrowserCaptcha("正常标题", "https://m.baidu.com/s") {
		t.Fatal("正常页不应判验证码")
	}
	if !isGoogleBlocked("https://www.google.com/sorry/index", "") {
		t.Fatal("/sorry 应判拦截")
	}
	if !isGoogleBlocked("", "Sorry...") || !isGoogleBlocked("", "异常流量") {
		t.Fatal("标题标记应判拦截")
	}
	if isGoogleBlocked("https://www.google.com/search?q=x", "x") {
		t.Fatal("正常页不应判拦截")
	}
}
