package main

// p3c-round —— P3-c 请求级对拍器（请求等价 / roundsum / session.json 结算）。
//
// 做四件事：
//  1. 从基准数据根复制两份独立数据副本（Go / C# 各一份，互不共享会话文件）
//  2. 把两份副本的 LLM 端点指向同一个本地 mock（记录请求体 + 回放固定 SSE）
//  3. 两侧各起一个宿主实例（独立端口），投递同一句用户消息，各跑一轮
//  4. 逐字节比对两侧首个请求体，并比对 roundsum 载荷与 session.json 结算字段
//
// 边界：只写本仓与临时数据副本；C# 侧只读执行；不触碰运行中的 CH4（8080/8081）。

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const toolVersion = "0.1.0"

// options 是运行参数。
type options struct {
	baseData string
	htmlRoot string
	goExe    string
	csExe    string
	goPort   int
	csPort   int
	outDir   string
	timeout  time.Duration
	keepData bool
	// fixtureEmoji 注入 emoji fixture 消息（c-7 non-BMP 转义对拍）
	fixtureEmoji bool
	// fixtureTool 工具 fixture 对拍（首轮 mock 回 tool_call → 次轮终文；比第 2 个请求体）
	fixtureTool bool
	// mockAddr mock 监听地址——自家 apis 判定要求「回环 + 18100」，随机端口抓不到自报头
	mockAddr string
}

// captured 是一次请求抓取（请求体 + 请求头）。
type captured struct {
	body   []byte
	header http.Header
}

// mockServer 是记录请求体与请求头的 SSE 回放服务（按侧分路径记录）。
type mockServer struct {
	server   *http.Server
	mu       sync.Mutex
	requests map[string][]captured
	// fixtureTool 首轮回 tool_call（工具 fixture 对拍）；false = 固定终文
	fixtureTool bool
}

// newMock 启动 mock 服务——默认绑 127.0.0.2:18100：
// 自家 apis 判定要求回环地址 + 18100 端口（C# / Go 同口径），随机端口抓不到自报头；
// 127.0.0.2 与真服务（127.0.0.1:18100）同段不同址，互不冲突。
func newMock(addr string, fixtureTool bool) (*mockServer, string, string, error) {
	mock := &mockServer{requests: map[string][]captured{}, fixtureTool: fixtureTool}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", "", fmt.Errorf("绑定 mock 端点 %s 失败（自家 apis 判定要求回环 + 18100）：%w", addr, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/cs/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) { mock.handle("cs", w, r) })
	mux.HandleFunc("/go/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) { mock.handle("go", w, r) })
	mock.server = &http.Server{Handler: mux}
	go func() { _ = mock.server.Serve(listener) }()
	base := "http://" + listener.Addr().String()
	return mock, base + "/cs/v1", base + "/go/v1", nil
}

// handle 记录请求体与请求头并回放 SSE——fixture-tool 模式下首轮回 tool_call，之后回终文。
func (m *mockServer) handle(side string, w http.ResponseWriter, r *http.Request) {
	body, _ := readAll(r)
	m.mu.Lock()
	round := len(m.requests[side])
	m.requests[side] = append(m.requests[side], captured{body: body, header: r.Header.Clone()})
	m.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	frames := []string{
		`{"choices":[{"delta":{"reasoning_content":"想"}}]}`,
		`{"choices":[{"delta":{"content":"你好"}}]}`,
		`{"choices":[{"finish_reason":"stop","delta":{}}],"usage":{"prompt_tokens":10,"completion_tokens":3,"prompt_cache_hit_tokens":4}}`,
		`[DONE]`,
	}
	if m.fixtureTool && round == 0 {
		// 工具 fixture：首轮让模型声明一次 text-read（空参 → 两侧同文案的缺参 ERR），次轮终文
		frames = []string{
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_fixture_1","type":"function","function":{"name":"text-read","arguments":"{}"}}]}}]}`,
			`{"choices":[{"finish_reason":"tool_calls","delta":{}}],"usage":{"prompt_tokens":12,"completion_tokens":4}}`,
			`[DONE]`,
		}
	}
	for _, frame := range frames {
		_, _ = w.Write([]byte("data: " + frame + "\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// bodies 取某一侧已抓取请求体副本。
func (m *mockServer) bodies(side string) [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.requests[side]
	out := make([][]byte, len(list))
	for i, item := range list {
		out[i] = append([]byte(nil), item.body...)
	}
	return out
}

// headers 取某一侧已抓取请求头副本。
func (m *mockServer) headers(side string) []http.Header {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.requests[side]
	out := make([]http.Header, len(list))
	for i, item := range list {
		out[i] = item.header.Clone()
	}
	return out
}

// close 关闭 mock。
func (m *mockServer) close() {
	if m.server != nil {
		_ = m.server.Close()
	}
}

// main 是入口。
func main() {
	opts := parseFlags()
	if opts.outDir == "" {
		opts.outDir = filepath.Join(projectRoot(), "tools", "p3c-round", "reports", time.Now().Format("20060102_150405"))
	}
	_ = os.MkdirAll(opts.outDir, 0o755)
	report := &reportFile{path: filepath.Join(opts.outDir, "report.txt")}
	fail := func(format string, args ...any) {
		report.add("FAIL", fmt.Sprintf(format, args...))
		report.flush()
		runCleanup()
		fmt.Println("报告:", report.path)
		os.Exit(1)
	}
	report.add("META", "p3c-round "+toolVersion+" | 基准数据根 "+opts.baseData)
	if opts.fixtureTool {
		report.add("META", "模式：工具 fixture（首轮 tool_call → 次轮终文；比第 2 个请求体）")
	}

	// [段1] 数据副本
	goData := filepath.Join(opts.outDir, "data-go")
	csData := filepath.Join(opts.outDir, "data-cs")
	if err := copyDataRoot(opts.baseData, goData); err != nil {
		fail("复制 Go 数据副本失败: %v", err)
	}
	if err := copyDataRoot(opts.baseData, csData); err != nil {
		fail("复制 C# 数据副本失败: %v", err)
	}
	if !opts.keepData {
		addCleanup(func() {
			_ = os.RemoveAll(goData)
			_ = os.RemoveAll(csData)
		})
	}
	if opts.fixtureEmoji {
		if err := injectEmojiFixture(goData); err != nil {
			fail("注入 Go 副本 emoji fixture 失败: %v", err)
		}
		if err := injectEmojiFixture(csData); err != nil {
			fail("注入 C# 副本 emoji fixture 失败: %v", err)
		}
		report.add("INFO", "emoji fixture 已注入两侧副本（user + assistant tool_calls / reasoning + tool 回执 + assistant 正文）")
	}

	// [段2] mock 服务与端点指向
	mock, csURL, goURL, err := newMock(opts.mockAddr, opts.fixtureTool)
	if err != nil {
		fail("起 mock 服务失败: %v", err)
	}
	defer mock.close()
	if err := patchLLMConfig(goData, goURL); err != nil {
		fail("写 Go 端点配置失败: %v", err)
	}
	if err := patchLLMConfig(csData, csURL); err != nil {
		fail("写 C# 端点配置失败: %v", err)
	}
	report.add("PASS", "mock 端点 cs="+csURL+" go="+goURL)

	// [段3] 起两侧
	if opts.goPort == 0 {
		opts.goPort = pickPort(9180)
	}
	if opts.csPort == 0 {
		opts.csPort = pickPort(opts.goPort + 1)
	}
	if err := setCfgValue(filepath.Join(csData, "config", "app.cfg"), "http.port", strconv.Itoa(opts.csPort)); err != nil {
		fail("写 C# 端口失败: %v", err)
	}
	mark(opts.outDir, "ports go=%d cs=%d", opts.goPort, opts.csPort)

	csProc, err := launchProcess("cs", opts.csExe, nil, filepath.Dir(opts.csExe), processEnv(map[string]string{"CH4_DATA_ROOT": csData}), opts.outDir)
	if err != nil {
		fail("起 C# 基线失败: %v", err)
	}
	defer func() { _ = csProc.stop() }()
	addCleanup(func() { _ = csProc.stop() })
	csBase := "http://127.0.0.1:" + strconv.Itoa(opts.csPort)
	if err := waitReady(csProc, csBase, opts.timeout); err != nil {
		fail("C# 基线未就绪: %v", err)
	}
	report.add("PASS", "C# 基线就绪 "+csBase)

	goProc, err := launchProcess("cat5", opts.goExe,
		[]string{"--data-root", goData, "--html-root", opts.htmlRoot, "--area", "dev", "--port", strconv.Itoa(opts.goPort)},
		filepath.Dir(opts.goExe), processEnv(nil), opts.outDir)
	if err != nil {
		fail("起 CAT5 失败: %v", err)
	}
	defer func() { _ = goProc.stop() }()
	addCleanup(func() { _ = goProc.stop() })
	goBase := "http://127.0.0.1:" + strconv.Itoa(opts.goPort)
	if err := waitReady(goProc, goBase, opts.timeout); err != nil {
		fail("CAT5 未就绪: %v", err)
	}
	report.add("PASS", "CAT5 就绪 "+goBase)

	// [段4] 投递同一句用户消息
	csMetaPath := filepath.Join(csData, "sessions", "majordomo", "majordomo.session.json")
	goMetaPath := filepath.Join(goData, "sessions", "majordomo", "majordomo.session.json")
	postedAt := time.Now()
	message := "Chat 用一句话说明你收到了。"
	if !postCommand(csBase, message) {
		fail("C# 指令未受理")
	}
	if !postCommand(goBase, message) {
		fail("CAT5 指令未受理")
	}

	// [段5] 等两侧请求结算（RoundRequests 是轮级计数——以元数据文件写入时刻为判据）
	if err := waitRoundSettled(csMetaPath, postedAt, opts.timeout); err != nil {
		fail("C# 未完成请求结算: %v", err)
	}
	if err := waitRoundSettled(goMetaPath, postedAt, opts.timeout); err != nil {
		fail("CAT5 未完成请求结算: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	// [段6] 请求体比对——工具 fixture 模式比第 2 个请求体（含工具回执）
	csBodies := mock.bodies("cs")
	goBodies := mock.bodies("go")
	report.add("INFO", "抓取请求数 cs="+strconv.Itoa(len(csBodies))+" go="+strconv.Itoa(len(goBodies)))
	if len(csBodies) < 1 || len(goBodies) < 1 {
		fail("请求数不足（两侧各需至少 1 条）")
	}
	bodyIndex := 0
	if opts.fixtureTool {
		bodyIndex = 1
		if len(csBodies) < 2 || len(goBodies) < 2 {
			fail("工具 fixture 请求数不足（两侧各需 ≥2：首轮 tool_call + 次轮工具回执）cs=%d go=%d", len(csBodies), len(goBodies))
		}
		report.add("INFO", "工具 fixture 模式：比对第 2 个请求体（含工具回执）")
	}
	goBody, csBody := goBodies[bodyIndex], csBodies[bodyIndex]
	if diffOffset := firstDiff(csBody, goBody); diffOffset >= 0 {
		_ = os.WriteFile(filepath.Join(opts.outDir, "request-cs.json"), csBody, 0o644)
		_ = os.WriteFile(filepath.Join(opts.outDir, "request-go.json"), goBody, 0o644)
		_ = os.WriteFile(filepath.Join(opts.outDir, "request-diff.txt"), []byte(diffContext(csBody, goBody, diffOffset)), 0o644)
		fail("请求体不一致（首个差异偏移 %d；已落盘 request-cs.json / request-go.json / request-diff.txt）", diffOffset)
	}
	report.add("PASS", "请求体逐字节一致（"+strconv.Itoa(len(goBody))+" 字节）")
	_ = os.WriteFile(filepath.Join(opts.outDir, "request-cs.json"), csBody, 0o644)
	_ = os.WriteFile(filepath.Join(opts.outDir, "request-go.json"), goBody, 0o644)
	report.add("PASS", "请求体摘要 cs="+sha256Hex(csBody)+" go="+sha256Hex(goBody))

	// [段6b] 请求头比对——自报头（仅自家 apis 生效）+ 传输基线
	csHeaders := mock.headers("cs")
	goHeaders := mock.headers("go")
	compareRequestHeaders(report, csHeaders[0], goHeaders[0])

	// [段7] roundsum 与结算字段比对
	csView := filepath.Join(csData, "sessions", "majordomo", "majordomo.view.jsonl")
	goView := filepath.Join(goData, "sessions", "majordomo", "majordomo.view.jsonl")
	csSum := lastRoundSum(csView)
	goSum := lastRoundSum(goView)
	if csSum == "" || goSum == "" {
		report.add("FAIL", "roundsum 缺失（cs="+csSum+" go="+goSum+"）")
	} else if key := compareRoundSum(csSum, goSum); key != "" {
		report.add("FAIL", "roundsum 字段不一致: "+key+"\n  cs="+csSum+"\n  go="+goSum)
	} else {
		report.add("PASS", "roundsum 字段一致（"+csSum+"）")
	}

	csMeta := readMeta(filepath.Join(csData, "sessions", "majordomo", "majordomo.session.json"))
	goMeta := readMeta(filepath.Join(goData, "sessions", "majordomo", "majordomo.session.json"))
	if csMeta == nil || goMeta == nil {
		report.add("FAIL", "session.json 缺失")
	} else {
		if csMeta.RoundTokens != goMeta.RoundTokens {
			report.add("FAIL", fmt.Sprintf("RoundTokens 不一致 cs=%+v go=%+v", csMeta.RoundTokens, goMeta.RoundTokens))
		} else {
			report.add("PASS", fmt.Sprintf("RoundTokens 一致 %+v", goMeta.RoundTokens))
		}
		if csMeta.RoundRequests != goMeta.RoundRequests || csMeta.RoundTools != goMeta.RoundTools {
			report.add("FAIL", fmt.Sprintf("RoundRequests/Tools 不一致 cs=%d/%d go=%d/%d",
				csMeta.RoundRequests, csMeta.RoundTools, goMeta.RoundRequests, goMeta.RoundTools))
		} else {
			report.add("PASS", fmt.Sprintf("RoundRequests/Tools 一致 %d/%d", goMeta.RoundRequests, goMeta.RoundTools))
		}
		if opts.fixtureTool {
			if goMeta.RoundTools != 1 || csMeta.RoundTools != 1 {
				report.add("FAIL", fmt.Sprintf("RoundTools 应为 1（工具 fixture 单次调用）cs=%d go=%d", csMeta.RoundTools, goMeta.RoundTools))
			} else {
				report.add("PASS", "RoundTools=1（工具 fixture 单次调用）")
			}
		}
	}

	report.flush()
	runCleanup()
	fmt.Println("报告:", report.path)
	if report.hasFail() {
		os.Exit(1)
	}
}

// parseFlags 解析参数。
func parseFlags() options {
	root := projectRoot()
	var o options
	flag.StringVar(&o.baseData, "base-data", `D:\Mau\WorkSpace\tools\CatTemp\cat5-data`, "基准数据根（两侧各复制一份）")
	flag.StringVar(&o.htmlRoot, "html-root", `D:\Mau\MauOut\html`, "静态页根")
	flag.StringVar(&o.goExe, "go-exe", filepath.Join(root, "bin", "cathome5.exe"), "CAT5 可执行文件")
	flag.StringVar(&o.csExe, "cs-exe", `D:\Mau\MauOut\CatHome4.exe`, "C# 基线可执行文件")
	flag.IntVar(&o.goPort, "go-port", 0, "CAT5 端口（0=9180 起自动选空闲）")
	flag.IntVar(&o.csPort, "cs-port", 0, "C# 端口（0=自动选空闲）")
	flag.StringVar(&o.outDir, "out", "", "报告目录（默认 tools/p3c-round/reports/<ts>）")
	flag.DurationVar(&o.timeout, "timeout", 90*time.Second, "单阶段等待上限")
	flag.BoolVar(&o.keepData, "keep-data", false, "true=保留数据副本（默认成功后删除）")
	flag.BoolVar(&o.fixtureEmoji, "fixture-emoji", false, "true=向两侧副本注入 emoji fixture（non-BMP 转义对拍）")
	flag.BoolVar(&o.fixtureTool, "fixture-tool", false, "true=工具 fixture（首轮 mock 回 tool_call → 次轮终文；比第 2 个请求体 + RoundTools=1）")
	flag.StringVar(&o.mockAddr, "mock-addr", "127.0.0.2:18100", "mock 监听地址（自家 apis 判定要求回环 + 18100，随机端口抓不到自报头）")
	flag.Parse()
	return o
}

// projectRoot 取施工仓根（exe 在 bin/ 下时取上一级）。
func projectRoot() string {
	exe, err := os.Executable()
	if err != nil {
		wd, _ := os.Getwd()
		return wd
	}
	dir := filepath.Dir(exe)
	if strings.EqualFold(filepath.Base(dir), "bin") {
		return filepath.Dir(dir)
	}
	return dir
}
