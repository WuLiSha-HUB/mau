package bricks

// CsService —— cs 桥执行面（CsCat 13 条认领线消费）。
//
// A 方案（2026-11-12 拍板）：宿主按需拉起 MauCsBridge.exe——
//   - 首次调用探 <base>/health；不通则按候选路径找桥 exe → 用受控根表拼 --root id=path
//     → 拉起 → 轮询 /health 就绪（超时按不可用）；成功缓存复用
//   - 复用已存在实例前校验 /health 的 roots 与当前根表一致；不一致按不可用报错（不杀外部进程）
//   - 端口固定 8130（CAT5_CS_BRIDGE_URL 可覆盖）；不加 config 键（不动 C# schema 模板）
//   - 宿主退出关停自管子进程；复用到的外部实例不动
//   - 服务缺失 / 拉不起 → ERR|CSHARP_NO_BRIDGE|…（沿用 C# 同码面）
//
// 回执语义对齐 C# BRIK-PACK-003：{ok,result} → (result, ok)，不二次包装、不做方法白名单
// （未知方法由桥回 ERR|UNKNOWN_METHOD）。
//
// 线程模型：整次调用串行（Roslyn 工作区不做并发假设）——与 TsService 同口径。

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"cathome5/mau/fs"
)

const (
	// csDefaultBridgeURL 是桥的默认监听地址（端口固定 8130）。
	csDefaultBridgeURL = "http://127.0.0.1:8130"
	// csDefaultBridgePort 是拉起桥时的默认端口。
	csDefaultBridgePort = "8130"
	// csHealthProbeTimeoutMs 是单次 /health 探测超时（复用判定——已起实例应立即应答）。
	csHealthProbeTimeoutMs = 2000
	// csLaunchBudgetMs 是拉起后等 /health 就绪的预算（实测起桥 724 ms，留足余量）。
	csLaunchBudgetMs = 20000
	// csLaunchPollMs 是拉起后的轮询间隔。
	csLaunchPollMs = 200
	// csInvokeTimeoutMs 是单次 /invoke 超时（对齐 OA 工单 1200 帧 = 120 s）。
	csInvokeTimeoutMs = 120000
	// csMaxLaunchFailures 是连续拉起失败上限——达上限置不可用态（防崩溃风暴）。
	csMaxLaunchFailures = 3
	// csBridgeExeName 是桥可执行文件名。
	csBridgeExeName = "MauCsBridge.exe"
)

// CsService 是 cs 桥的进程宿主——首次调用按需拉起，跨调用复用同一实例。
type CsService struct {
	// roots 受控根表（拉起点桥时拼 --root id=path；复用校验面）
	roots []fs.Root
	// logf 日志出口（level 0=debug 1=info 2=warn 3=err；nil = 不出声）
	logf func(level int, message string)

	// mu 串行锁——连接管理 + 单次调用（Roslyn 工作区不做并发假设）
	mu sync.Mutex
	// client HTTP 客户端（无全局超时——每次请求按 ctx 控制；Proxy=nil：只打回环，不经代理）
	client *http.Client
	// baseURL 生效的桥地址（首次解析后缓存）
	baseURL string
	// ready health 已通过（缓存复用）
	ready bool
	// cmd 自管子进程（nil = 未拉起 / 复用外部实例）
	cmd *exec.Cmd
	// done 自管进程退出通知
	done chan struct{}
	// dead 自管进程已退出标志
	dead int32
	// failCount 连续拉起失败计数
	failCount int
	// closed 宿主已停机（不再拉起新进程）
	closed bool
}

// NewCsService 构造 cs 桥宿主。
//
// roots 取受控根全表（拉起点桥时逐根拼 --root id=path）；logf 日志出口可空。
func NewCsService(roots []fs.Root, logf func(level int, message string)) *CsService {
	return &CsService{
		roots:  roots,
		logf:   logf,
		client: &http.Client{Transport: &http.Transport{Proxy: nil}},
	}
}

// Invoke 执行一次桥调用——method / argsJSON 原样透传 POST /invoke，回 {ok,result} → (result, ok)。
//
// 服务不可用（未找到桥 / 拉不起 / 根表不一致）→ ERR|CSHARP_NO_BRIDGE|…；
// 调用期传输失败（连接中断 / 非 200 / 回执不可解析）→ ERR|CSHARP_BRIDGE_IO|…。
func (s *CsService) Invoke(method, argsJSON string) (string, bool) {
	if s == nil {
		return "ERR|CSHARP_NO_BRIDGE|宿主未注入 ICSharpBridge（Mau.Development.MauRoslynBridge）", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if errText := s.ensureReadyLocked(); errText != "" {
		return errText, false
	}
	body := `{"method":` + jsonString(method) + `,"argsJson":` + jsonString(argsJSON) + `}`
	status, payload, err := s.doLocked(http.MethodPost, s.baseURL+"/invoke", body, csInvokeTimeoutMs*time.Millisecond)
	if err != nil {
		// 传输失败 → 下次调用重新探活（进程可能已退出）
		s.ready = false
		return "ERR|CSHARP_BRIDGE_IO|桥调用失败（" + method + "）: " + err.Error(), false
	}
	if status != http.StatusOK {
		return "ERR|CSHARP_BRIDGE_IO|桥回执 HTTP " + strconv.Itoa(status) + "：" + csPreview(string(payload), 300), false
	}
	var reply struct {
		Ok     bool   `json:"ok"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal(payload, &reply); err != nil {
		return "ERR|CSHARP_BRIDGE_IO|桥回执不可解析：" + csPreview(string(payload), 300), false
	}
	return reply.Result, reply.Ok
}

// Shutdown 宿主退出收尾——关停自管子进程（复用外部实例不动）。
func (s *CsService) Shutdown() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.killLocked()
}

// Alive 报告桥是否已就绪（诊断用——不触发拉起）。
func (s *CsService) Alive() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

// ensureReadyLocked 确保桥可用——探活复用 → 找 exe → 拉起 → 等就绪。调用方持锁。
func (s *CsService) ensureReadyLocked() string {
	if s.closed {
		return "ERR|CSHARP_NO_BRIDGE|宿主已停机——桥服务不再拉起"
	}
	if s.ready {
		return ""
	}
	if s.baseURL == "" {
		s.baseURL = csBridgeBaseURL()
	}
	// [段1] 先探已存在实例——能复用就不拉起
	if health, ok := s.probeHealthLocked(); ok {
		if errText := s.checkRootsLocked(health); errText != "" {
			return errText
		}
		s.ready = true
		s.log(1, "复用已存在的 cs 桥："+s.baseURL)
		return ""
	}
	// [段2] 找桥可执行文件
	exe, errText := csBridgeExecutable()
	if errText != "" {
		return errText
	}
	// [段3] 拉起（连续失败达上限即明示——不无限重试）
	if s.failCount >= csMaxLaunchFailures {
		return "ERR|CSHARP_NO_BRIDGE|桥服务连续拉起失败达上限（" + strconv.Itoa(csMaxLaunchFailures) + " 次）——需重启宿主恢复"
	}
	if launchErr := s.launchLocked(exe); launchErr != "" {
		s.failCount++
		return launchErr
	}
	// [段4] 等 /health 就绪——进程提前退出即失败
	if waitErr := s.waitReadyLocked(); waitErr != "" {
		s.failCount++
		s.killLocked()
		return waitErr
	}
	s.failCount = 0
	s.ready = true
	s.log(1, "cs 桥已拉起："+exe+"（"+s.baseURL+"）")
	return ""
}

// csBridgeHealth 是 /health 应答的必要字段面（roots 用于复用校验）。
type csBridgeHealth struct {
	// Ok 服务自述可用
	Ok bool `json:"ok"`
	// Service 服务名（mau-csbridge）
	Service string `json:"service"`
	// Roots 桥实例的受控根表
	Roots []struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	} `json:"roots"`
}

// probeHealthLocked 探一次 /health——连不上 / 非 200 / 不可解析一律按「未就绪」。
func (s *CsService) probeHealthLocked() (csBridgeHealth, bool) {
	health := csBridgeHealth{}
	status, payload, err := s.doLocked(http.MethodGet, s.baseURL+"/health", "", csHealthProbeTimeoutMs*time.Millisecond)
	if err != nil || status != http.StatusOK {
		return health, false
	}
	if err := json.Unmarshal(payload, &health); err != nil {
		return health, false
	}
	return health, true
}

// checkRootsLocked 校验桥实例根表与当前根表一致——不一致按不可用报错（不杀外部进程）。
func (s *CsService) checkRootsLocked(health csBridgeHealth) string {
	want := make(map[string]string, len(s.roots))
	for _, root := range s.roots {
		want[strings.ToLower(root.ID)] = csNormalizeRootPath(root.Path)
	}
	got := make(map[string]string, len(health.Roots))
	for _, root := range health.Roots {
		got[strings.ToLower(root.ID)] = csNormalizeRootPath(root.Path)
	}
	missing := make([]string, 0, len(want))
	mismatched := make([]string, 0, len(want))
	for id, path := range want {
		actual, exists := got[id]
		if !exists {
			missing = append(missing, id)
			continue
		}
		if actual != path {
			mismatched = append(mismatched, id)
		}
	}
	extra := make([]string, 0, len(got))
	for id := range got {
		if _, exists := want[id]; !exists {
			extra = append(extra, id)
		}
	}
	if len(missing) == 0 && len(mismatched) == 0 && len(extra) == 0 {
		return ""
	}
	sort.Strings(missing)
	sort.Strings(mismatched)
	sort.Strings(extra)
	parts := make([]string, 0, 3)
	if len(missing) > 0 {
		parts = append(parts, "缺 "+strings.Join(missing, ","))
	}
	if len(mismatched) > 0 {
		parts = append(parts, "路径不同 "+strings.Join(mismatched, ","))
	}
	if len(extra) > 0 {
		parts = append(parts, "多 "+strings.Join(extra, ","))
	}
	return "ERR|CSHARP_NO_BRIDGE|已存在的桥实例根表与当前不一致（" + strings.Join(parts, "；") +
		"）——请先停掉该实例，或设 CAT5_CS_BRIDGE_URL 指向其它端口（不接管外部进程）"
}

// csNormalizeRootPath 根路径归一（比较用）——正斜杠 + 去尾分隔符 + 小写（Windows 路径大小写不敏感）。
func csNormalizeRootPath(path string) string {
	normalized := filepath.ToSlash(strings.TrimSpace(path))
	normalized = strings.TrimRight(normalized, "/")
	return strings.ToLower(normalized)
}

// csBridgeBaseURL 解析桥地址——CAT5_CS_BRIDGE_URL 覆盖，否则默认 127.0.0.1:8130。
func csBridgeBaseURL() string {
	if raw := strings.TrimSpace(os.Getenv("CAT5_CS_BRIDGE_URL")); raw != "" {
		return strings.TrimRight(raw, "/")
	}
	return csDefaultBridgeURL
}

// csBridgeExecutable 按 A 方案候选链找桥可执行文件。
//
// 顺序：CAT5_CS_BRIDGE_EXE → <exeDir>/../host/csbridge/bin/Debug/net10.0/ →
// <exeDir>/csbridge/ → <cwd>/host/csbridge/bin/Debug/net10.0/。
func csBridgeExecutable() (string, string) {
	candidates := make([]string, 0, 4)
	if raw := strings.TrimSpace(os.Getenv("CAT5_CS_BRIDGE_EXE")); raw != "" {
		candidates = append(candidates, raw)
	}
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates,
			filepath.Join(exeDir, "..", "host", "csbridge", "bin", "Debug", "net10.0", csBridgeExeName),
			filepath.Join(exeDir, "csbridge", csBridgeExeName),
		)
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "host", "csbridge", "bin", "Debug", "net10.0", csBridgeExeName))
	}
	tried := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		full, err := filepath.Abs(candidate)
		if err != nil {
			full = candidate
		}
		tried = append(tried, full)
		if stat, statErr := os.Stat(full); statErr == nil && !stat.IsDir() {
			return full, ""
		}
	}
	return "", "ERR|CSHARP_NO_BRIDGE|未找到 " + csBridgeExeName + "（候选：" + strings.Join(tried, " · ") +
		"）——可设 CAT5_CS_BRIDGE_EXE 指定桥路径"
}

// launchLocked 拉起桥进程——参数 --port + 逐根 --root id=path。
func (s *CsService) launchLocked(exe string) string {
	args := make([]string, 0, 2+len(s.roots)*2)
	args = append(args, "--port", csBridgePort(s.baseURL))
	for _, root := range s.roots {
		args = append(args, "--root", root.ID+"="+root.Path)
	}
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Dir = filepath.Dir(exe)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "ERR|CSHARP_NO_BRIDGE|桥进程 stderr 管道创建失败：" + err.Error()
	}
	if err := cmd.Start(); err != nil {
		return "ERR|CSHARP_NO_BRIDGE|桥进程拉起失败：" + err.Error()
	}
	s.cmd = cmd
	s.done = make(chan struct{})
	atomic.StoreInt32(&s.dead, 0)
	go s.readStderr(stderr)
	go func(done chan struct{}) {
		_ = cmd.Wait()
		atomic.StoreInt32(&s.dead, 1)
		close(done)
	}(s.done)
	return ""
}

// readStderr 桥日志读线程——stderr 归日志面（不进协议）。
func (s *CsService) readStderr(pipe io.Reader) {
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 16*1024), 1024*1024)
	for scanner.Scan() {
		s.log(1, "[csbridge] "+scanner.Text())
	}
}

// waitReadyLocked 轮询 /health 直到就绪——进程提前退出或预算耗尽即失败。
func (s *CsService) waitReadyLocked() string {
	deadline := time.Now().Add(csLaunchBudgetMs * time.Millisecond)
	for {
		if health, ok := s.probeHealthLocked(); ok {
			// 自管进程的根表由我们拼出——仍做一次校验兜底（端口被旧实例占住时会露头）
			if errText := s.checkRootsLocked(health); errText != "" {
				return errText
			}
			return ""
		}
		if atomic.LoadInt32(&s.dead) == 1 {
			return "ERR|CSHARP_NO_BRIDGE|桥进程启动即退出——请检查 .NET 运行时与 " + csBridgeExeName + "（日志见上）"
		}
		if time.Now().After(deadline) {
			return "ERR|CSHARP_NO_BRIDGE|桥服务健康检查超时（" + strconv.Itoa(csLaunchBudgetMs) + " ms 内未就绪）"
		}
		time.Sleep(csLaunchPollMs * time.Millisecond)
	}
}

// killLocked 杀自管子进程并清句柄——外部实例（cmd 为 nil）不动。
func (s *CsService) killLocked() {
	if s.cmd == nil {
		s.ready = false
		return
	}
	cmd := s.cmd
	killCmdTree(cmd)
	if s.done != nil {
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
		}
	}
	s.cmd = nil
	s.done = nil
	atomic.StoreInt32(&s.dead, 1)
	s.ready = false
}

// csBridgePort 从桥地址取端口——解析失败回落默认 8130。
func csBridgePort(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return csDefaultBridgePort
	}
	if port := parsed.Port(); port != "" {
		return port
	}
	return csDefaultBridgePort
}

// doLocked 发一次 HTTP 请求——每次按 ctx 控超时，返回 (状态码, 正文, 传输错误)。
func (s *CsService) doLocked(method, target, body string, timeout time.Duration) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if readErr != nil {
		return resp.StatusCode, nil, readErr
	}
	return resp.StatusCode, payload, nil
}

// csPreview 截断预览——超限给「…（共 N 字）」尾注（UTF-16 码元口径）。
func csPreview(text string, limit int) string {
	trimmed := strings.TrimSpace(text)
	total := utf16Units(trimmed)
	if total <= limit {
		return trimmed
	}
	return truncateUTF16Units(trimmed, limit) + "…（共 " + strconv.Itoa(total) + " 字）"
}

// log 日志出口——nil 安全（测试替身不传 logger）。
func (s *CsService) log(level int, message string) {
	if s.logf != nil {
		s.logf(level, message)
	}
}
