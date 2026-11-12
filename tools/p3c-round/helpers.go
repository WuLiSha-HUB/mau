package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---- IO 基础 ----

// readAll 读完请求体。
func readAll(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer func() { _ = r.Body.Close() }()
	return io.ReadAll(io.LimitReader(r.Body, 64<<20))
}

// copyDataRoot 复制数据根——跳过 runs（历史日志不参与对拍）。
func copyDataRoot(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "runs" {
			continue
		}
		srcPath := filepath.Join(src, name)
		dstPath := filepath.Join(dst, name)
		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

// copyDir 递归复制目录。
func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

// copyFile 复制单文件。
func copyFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, raw, 0o644)
}

// ---- 配置补齐 ----

// mockGUID 是 mock 端点身份（空 GUID 会被两侧配置面判非法）。
const mockGUID = "11111111-1111-1111-1111-111111111111"

// patchLLMConfig 把副本的端点池与密钥指向 mock（配置零 key，密钥单独落 secrets）。
func patchLLMConfig(dataRoot, baseURL string) error {
	pool := []map[string]any{{
		"schemaVersion": 1,
		"apiConfigId":   mockGUID,
		"displayName":   "p3c-mock",
		"apiType":       "dsf4.1",
		"endpoint":      baseURL,
		"defaultModel":  "mock-model",
		"isDefault":     true,
		"isBackup":      false,
	}}
	raw, err := json.MarshalIndent(pool, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dataRoot, "config", "llm-api.json"), append(raw, '\n'), 0o644); err != nil {
		return err
	}
	secrets := map[string]string{"11111111111111111111111111111111": "sk-mock"}
	rawSecrets, err := json.MarshalIndent(secrets, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataRoot, "secrets", "llm-api.cfg"), append(rawSecrets, '\n'), 0o644)
}

// setCfgValue 在 cfg 文本里设置一行键值（保留其它行）。
func setCfgValue(path, key, value string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(raw), "\n")
	found := false
	for i, line := range lines {
		trim := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if strings.HasPrefix(trim, key+"=") {
			lines[i] = key + "=" + value
			found = true
		}
	}
	if !found {
		lines = append(lines, key+"="+value)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

// ---- 端口与就绪 ----

// pickPort 从 start 起找第一个空闲端口。
func pickPort(start int) int {
	for port := start; port < start+200; port++ {
		if portFree(port) {
			return port
		}
	}
	return start
}

// portFree 判定端口可绑。
func portFree(port int) bool {
	if port < 1024 || port > 65535 {
		return false
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// waitReady 轮询快照端点就绪。
func waitReady(proc *launched, base string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if proc != nil && !proc.stillRunning() {
			return fmt.Errorf("进程已退出；stderr=%s", tailFile(proc.stderrPath, 800))
		}
		if probeReady(base) {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("等待 %s 就绪超时", base)
}

// probeReady 判定快照端点已可解析。
func probeReady(base string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(base + "/api/v1/snapshot")
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false
	}
	var payload map[string]any
	return json.Unmarshal(raw, &payload) == nil && len(payload) >= 10
}

// postCommand 投递一条用户指令（{"text": ...}）。
func postCommand(base, text string) bool {
	body := `{"text":` + mustJSON(text) + `}`
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(base+"/api/v1/command", "application/json", strings.NewReader(body))
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return false
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var payload struct {
		OK bool `json:"ok"`
	}
	return json.Unmarshal(raw, &payload) == nil && payload.OK
}

// mustJSON 编码 JSON 字符串。
func mustJSON(text string) string {
	raw, _ := json.Marshal(text)
	return string(raw)
}

// tailFile 取文件尾部（错误诊断）。
func tailFile(path string, max int) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(raw) > max {
		raw = raw[len(raw)-max:]
	}
	return strings.TrimSpace(string(raw))
}

// ---- 结算面读取 ----

// metaLite 是 session.json 的对拍子集。
type metaLite struct {
	RoundTokens   tokenTriple `json:"RoundTokens"`
	RoundRequests int64       `json:"RoundRequests"`
	RoundTools    int64       `json:"RoundTools"`
}

// tokenTriple 是 token 三项。
type tokenTriple struct {
	Prompt     int64 `json:"Prompt"`
	CacheHit   int64 `json:"CacheHit"`
	Completion int64 `json:"Completion"`
}

// readMeta 读取元数据子集（缺失 / 损坏 = nil）。
func readMeta(path string) *metaLite {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var meta metaLite
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil
	}
	return &meta
}

// waitRoundSettled 等请求结算落盘（元数据文件在投递后写入过）。
func waitRoundSettled(path string, since time.Time, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && info.ModTime().After(since) {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("等待 %s 在 %s 之后写入超时", path, since.Format("15:04:05"))
}

// lastRoundSum 取最后一条 roundsum 载荷（无 = 空串）。
func lastRoundSum(viewPath string) string {
	raw, err := os.ReadFile(viewPath)
	if err != nil {
		return ""
	}
	result := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var block struct {
			RenderType string `json:"RenderType"`
			Payload    string `json:"Payload"`
		}
		if err := json.Unmarshal([]byte(line), &block); err != nil {
			continue
		}
		if block.RenderType == "roundsum" {
			result = block.Payload
		}
	}
	return result
}

// compareRoundSum 比对 roundsum 载荷——字段值 + phases 键集；返回不一致字段名（空 = 一致）。
func compareRoundSum(csSum, goSum string) string {
	var csPayload, goPayload map[string]any
	if err := json.Unmarshal([]byte(csSum), &csPayload); err != nil {
		return "cs 载荷解析失败: " + err.Error()
	}
	if err := json.Unmarshal([]byte(goSum), &goPayload); err != nil {
		return "go 载荷解析失败: " + err.Error()
	}
	csData, _ := csPayload["data"].(map[string]any)
	goData, _ := goPayload["data"].(map[string]any)
	if csData == nil || goData == nil {
		return "data 段缺失"
	}
	keys := []string{"prompt", "completion", "cacheHit", "miss", "done", "toolCount", "requests"}
	for _, key := range keys {
		if fmt.Sprint(csData[key]) != fmt.Sprint(goData[key]) {
			return key
		}
	}
	csPhases, _ := csData["phases"].(map[string]any)
	goPhases, _ := goData["phases"].(map[string]any)
	if len(csPhases) != len(goPhases) {
		return "phases 键数"
	}
	for key := range csPhases {
		if _, ok := goPhases[key]; !ok {
			return "phases." + key
		}
	}
	return ""
}

// firstDiff 逐字节比对——返回首个差异偏移（-1 = 相同）。
func firstDiff(left, right []byte) int {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for i := 0; i < limit; i++ {
		if left[i] != right[i] {
			return i
		}
	}
	if len(left) != len(right) {
		return limit
	}
	return -1
}

// diffContext 差异上下文（前后各 240 字节——定位字段用）。
func diffContext(left, right []byte, offset int) string {
	start := offset - 240
	if start < 0 {
		start = 0
	}
	end := offset + 240
	leftText := excerpt(left, start, end)
	rightText := excerpt(right, start, end)
	return "offset=" + strconv.Itoa(offset) +
		"\n--- cs ---\n" + leftText +
		"\n--- go ---\n" + rightText + "\n"
}

// excerpt 取字节区间文本。
func excerpt(raw []byte, start, end int) string {
	if start > len(raw) {
		start = len(raw)
	}
	if end > len(raw) {
		end = len(raw)
	}
	return string(raw[start:end])
}

// cleanupRegistry 是失败退出前的清理钩子（os.Exit 不跑 defer）。
var cleanupRegistry []func()

// addCleanup 登记清理钩子。
func addCleanup(fn func()) { cleanupRegistry = append(cleanupRegistry, fn) }

// runCleanup 执行清理钩子（逆序；幂等）。
func runCleanup() {
	for i := len(cleanupRegistry) - 1; i >= 0; i-- {
		cleanupRegistry[i]()
	}
}

// ---- 请求头对拍 ----

// headerMustMatch 是必须逐字一致的请求头——自报身份六项 + 传输基线四项。
var headerMustMatch = []string{
	"x-cat-id",
	"x-cat-name",
	"x-cat-channel",
	"x-project-mount",
	"x-project-mount-mode",
	"x-project-jobs",
	"x-opencode-session",
	"authorization",
	"accept",
	"content-type",
}

// headerKnownDiff 是「必然不同」的头——记录实况，不计 FAIL。
//
// x-cat-session 是会话世代 ID（两侧各自生成）；x-client-id 是产品 / 版本标识（cat-home4/… vs cat-home5/…）；
// user-agent 两侧传输层默认值不同（.NET 不发 / Go 发 Go-http-client）。
var headerKnownDiff = []string{"x-cat-session", "x-client-id", "user-agent"}

// compareRequestHeaders 比对两侧请求头——mustMatch 逐项相等；knownDiff 记实况。
func compareRequestHeaders(report *reportFile, csHeader, goHeader http.Header) {
	for _, name := range headerMustMatch {
		csValue := csHeader.Get(name)
		goValue := goHeader.Get(name)
		if csValue == "" || goValue == "" {
			report.add("FAIL", "请求头缺失 "+name+"（cs="+headerQuote(csValue)+" go="+headerQuote(goValue)+"）")
			continue
		}
		if csValue != goValue {
			report.add("FAIL", "请求头不一致 "+name+"（cs="+csValue+" go="+goValue+"）")
			continue
		}
		report.add("PASS", "请求头一致 "+name+"="+csValue)
	}
	for _, name := range headerKnownDiff {
		report.add("INFO", "请求头预期差异 "+name+"：cs="+headerQuote(csHeader.Get(name))+" go="+headerQuote(goHeader.Get(name)))
	}
}

// headerQuote 头值展示——空值显式标 (空)。
func headerQuote(value string) string {
	if value == "" {
		return "(空)"
	}
	return value
}

// ---- 报告 ----

// reportFile 是逐行报告。
type reportFile struct {
	path  string
	lines []string
	fails int
}

// add 追加一行。
func (r *reportFile) add(tag, text string) {
	r.lines = append(r.lines, tag+" | "+text)
	if tag == "FAIL" {
		r.fails++
	}
	fmt.Printf("%s | %s\n", tag, text)
}

// hasFail 是否存在失败项。
func (r *reportFile) hasFail() bool { return r.fails > 0 }

// flush 落盘。
func (r *reportFile) flush() {
	_ = os.WriteFile(r.path, []byte(strings.Join(r.lines, "\r\n")+"\r\n"), 0o644)
}

// mark 过程状态行。
func mark(outDir, format string, args ...any) {
	line := time.Now().Format("15:04:05.000") + " | " + fmt.Sprintf(format, args...)
	f, err := os.OpenFile(filepath.Join(outDir, "status.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(line + "\r\n")
	_ = f.Close()
	fmt.Println("[status]", line)
}

// sortedKeys 排序键集（诊断用）。
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// itoa 整数转字符串。
func itoa(v int) string { return strconv.Itoa(v) }

// sha256Hex 计算 SHA-256 摘要（请求体字节级对账用）。
func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ---- emoji fixture（c-7 non-BMP 转义对拍） ----

// emojiFixtureRows 是 non-BMP 转义对拍语料——user / assistant tool_calls + reasoning / tool 回执 / assistant 正文。
//
// 四处正文与 tool_calls.arguments 都带 U+1F534（🔴，non-BMP）：C# 侧请求体把它写成 UTF-16
// 代理对转义（\uD83D\uDD34），Go 侧必须同形（c-7 判例：tool_calls 原样透传差一处字节）。
func emojiFixtureRows() []map[string]any {
	created := time.Now().UnixMilli()
	toolCalls := `[{"id":"call_fixture_emoji01","type":"function","function":{"name":"text-replace",` +
		`"arguments":"{\"path\": \"fixture.txt\", \"old\": \"a\", \"new\": \"// 🔴 emoji fixture\"}"}}]`
	return []map[string]any{
		{"Role": 1, "Content": "emoji fixture 用户输入 🔴", "ToolCallId": "", "ToolName": "", "ToolCallsJson": "", "ReasoningContent": "", "ImagesJson": "", "CreatedAt": created, "t": "m"},
		{"Role": 2, "Content": "", "ToolCallId": "", "ToolName": "", "ToolCallsJson": toolCalls, "ReasoningContent": "emoji fixture 思考 🔴", "ImagesJson": "", "CreatedAt": created + 1, "t": "m"},
		{"Role": 3, "Content": "emoji fixture 工具回执 🔴", "ToolCallId": "call_fixture_emoji01", "ToolName": "text-replace", "ToolCallsJson": "", "ReasoningContent": "", "ImagesJson": "", "CreatedAt": created + 2, "t": "m"},
		{"Role": 2, "Content": "emoji fixture 助手正文 🔴", "ToolCallId": "", "ToolName": "", "ToolCallsJson": "", "ReasoningContent": "", "ImagesJson": "", "CreatedAt": created + 3, "t": "m"},
	}
}

// injectEmojiFixture 向副本会话尾部追加 emoji fixture 消息（两侧副本注入同一串——对拍前提）。
func injectEmojiFixture(dataRoot string) error {
	path := filepath.Join(dataRoot, "sessions", "majordomo", "majordomo.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	for _, row := range emojiFixtureRows() {
		raw, err := json.Marshal(row)
		if err != nil {
			return err
		}
		if _, err := f.WriteString(string(raw) + "\n"); err != nil {
			return err
		}
	}
	return nil
}
