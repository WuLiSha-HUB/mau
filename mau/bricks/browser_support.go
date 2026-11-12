package bricks

// 浏览器服务辅助——CDP 预算 / 取值 / 端口锚 / 进程清理 / 产物渲染。

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// browserBudgetRemaining 预算剩余（毫秒）——下限 200，保证轮询能拿到一次有效切片。
func browserBudgetRemaining(started time.Time, budgetMs int64) int {
	remaining := budgetMs - time.Since(started).Milliseconds()
	if remaining < 200 {
		return 200
	}
	return int(remaining)
}

// browserBudgetSlice 预算切片（毫秒）——取剩余与单次调用上限的较小者。
func browserBudgetSlice(started time.Time, budgetMs int64, capMs int) int {
	remaining := browserBudgetRemaining(started, budgetMs)
	if remaining < capMs {
		return remaining
	}
	return capMs
}

// safeEval 取值兜底——超时不抛（已判完可读状态后的标题 / 地址取值不该让整单失败）。
func safeEval(cdp *cdpClient, expression string) string {
	value, err := evalRaw(cdp, expression, browserCallTimeoutMs)
	if err != nil {
		return ""
	}
	return value
}

// evalRaw 执行 JS 表达式并取回值——非字符串按 JSON 文本返回。
func evalRaw(cdp *cdpClient, expression string, timeoutMs int) (string, error) {
	response, err := cdp.Call("Runtime.evaluate", `{"expression":`+jsonString(expression)+`,"returnByValue":true}`, timeoutMs)
	if err != nil {
		return "", err
	}
	var root struct {
		Result struct {
			Result struct {
				Value json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"result"`
	}
	if json.Unmarshal(response, &root) != nil {
		return "", nil
	}
	value := root.Result.Result.Value
	if len(value) == 0 {
		return "", nil
	}
	var text string
	if json.Unmarshal(value, &text) == nil {
		return text, nil
	}
	return string(value), nil
}

// waitReady 等页面进入可读状态——interactive（DOM 就绪）或 complete（含全部子资源）。
//
// 判据取 interactive：complete 要等全部子资源（图片 / 字体 / 分析脚本）落地，外网站首访可达数十秒，
// 而 DOM 在此前早已就绪——读正文 / 结构 / 截图都不需要等子资源。
func waitReady(cdp *cdpClient, timeoutMs int) (string, error) {
	started := time.Now()
	state := ""
	for time.Since(started).Milliseconds() < int64(timeoutMs) {
		remaining := timeoutMs - int(time.Since(started).Milliseconds())
		slice := browserEvalSliceMs
		if remaining < slice {
			slice = remaining
		}
		if slice < 200 {
			slice = 200
		}
		value, err := evalRaw(cdp, "document.readyState", slice)
		if err != nil {
			if isCDPTimeout(err) {
				// 单次 eval 无响应——按「打不开」处置：预算到点即判，不把 20 秒拖成无限等待
				return "timeout", nil
			}
			return state, err
		}
		state = value
		if state == "interactive" || state == "complete" {
			return state, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return state, nil
}

// buildAxSlim 无障碍树精简——只留 role: name（剔除 ignored 与空名节点）。
func buildAxSlim(response json.RawMessage) string {
	var root struct {
		Result struct {
			Nodes []struct {
				Ignored bool `json:"ignored"`
				Role    struct {
					Value json.RawMessage `json:"value"`
				} `json:"role"`
				Name struct {
					Value json.RawMessage `json:"value"`
				} `json:"name"`
			} `json:"nodes"`
		} `json:"result"`
	}
	if json.Unmarshal(response, &root) != nil {
		return ""
	}
	var b strings.Builder
	for _, node := range root.Result.Nodes {
		if node.Ignored {
			continue
		}
		role := axText(node.Role.Value)
		name := axText(node.Name.Value)
		if name == "" {
			continue
		}
		b.WriteString(role + ": " + name + "\n")
	}
	return b.String()
}

// axText 取 AX 节点字段文本——{type, value} 结构取 value（字符串解包，其余按 JSON 原文）。
func axText(value json.RawMessage) string {
	if len(value) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(value, &text) == nil {
		return text
	}
	return string(value)
}

// nextOutPath 生成产物落盘路径——out/<时间戳>-<用途>.<扩展名>。
func nextOutPath(browser *catBrowser, purpose, ext string) string {
	stamp := time.Now().Format("20060102_150405_000")
	return filepath.Join(browser.OutDir, stamp+"-"+purpose+"."+ext)
}

// browserPreview 摘要预览——头部截断（截断时标注总量）。
func browserPreview(text string, max int) string {
	total := utf16Units(text)
	if total <= max {
		return text
	}
	return truncateUTF16Units(text, max) + "\n…[截断 共 " + strconv.Itoa(total) + " 字符]"
}

// buildLinkPreview 链接面预览——解析 JSON 取前 N 条（锚文本 → 地址）。
func buildLinkPreview(linksJSON string) string {
	var rows []struct {
		T string `json:"t"`
		H string `json:"h"`
	}
	if json.Unmarshal([]byte(linksJSON), &rows) != nil {
		return ""
	}
	var b strings.Builder
	total := len(rows)
	for i := 0; i < total && i < browserLinkPreviewCount; i++ {
		label := rows[i].T
		if label == "" {
			label = "(无锚文本)"
		}
		b.WriteString("- " + label + " → " + rows[i].H + "\n")
	}
	if total > browserLinkPreviewCount {
		b.WriteString("…[共 " + strconv.Itoa(total) + " 条，完整清单见落盘文件]")
	}
	return b.String()
}

// pngSize 读 PNG 尺寸——IHDR 宽高（非 PNG 或读取失败返回 "?"）。
func pngSize(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return "?"
	}
	defer func() { _ = file.Close() }()
	head := make([]byte, 24)
	if _, err := io.ReadFull(file, head); err != nil {
		return "?"
	}
	width := int(int32(binary.BigEndian.Uint32(head[16:20])))
	height := int(int32(binary.BigEndian.Uint32(head[20:24])))
	return strconv.Itoa(width) + "x" + strconv.Itoa(height)
}

// browserHTTPCall DevTools HTTP 端点调用——GET / PUT（页签新建需 PUT）。
func browserHTTPCall(target string, put bool) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	var resp *http.Response
	var err error
	if put {
		req, reqErr := http.NewRequest(http.MethodPut, target, nil)
		if reqErr != nil {
			return "", reqErr
		}
		resp, err = client.Do(req)
	} else {
		resp, err = client.Get(target)
	}
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if readErr != nil {
		return "", readErr
	}
	return string(body), nil
}

// extractJSONString 从 JSON 文本取顶层字符串字段（解析失败 / 缺字段返回空串）。
func extractJSONString(jsonText, key string) string {
	if jsonText == "" {
		return ""
	}
	var root map[string]json.RawMessage
	if json.Unmarshal([]byte(jsonText), &root) != nil {
		return ""
	}
	raw, ok := root[key]
	if !ok {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

// readAnchorPort 读端口锚文件（首行端口号）——缺失 / 非正整数 / 读取异常一律返回 0。
func readAnchorPort(file string) int {
	raw, err := os.ReadFile(file)
	if err != nil {
		return 0
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return 0
	}
	port, convErr := strconv.Atoi(strings.TrimSpace(lines[0]))
	if convErr != nil || port <= 0 {
		return 0
	}
	return port
}

// clearPortAnchor 清理端口锚文件——域回收 / 实例关闭后调用（锚只在实例存活期间有意义）。
func clearPortAnchor(file string, logf func(int, string)) {
	if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
		logf(2, "端口锚清理失败: "+err.Error())
	}
}

// waitPortFile 等 DevToolsActivePort 出现并解析端口——首行为端口号。
func waitPortFile(portFile string, timeoutMs int) int {
	started := time.Now()
	for time.Since(started).Milliseconds() < int64(timeoutMs) {
		if port := readAnchorPort(portFile); port > 0 {
			return port
		}
		time.Sleep(50 * time.Millisecond)
	}
	return 0
}

// browserPortAlive CDP 端口存活探测（静默——轮询等待期大量失败属预期）。
func browserPortAlive(port int) bool {
	if port <= 0 {
		return false
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/json/version")
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return false
	}
	return len(body) > 0
}

// waitPortClosed 等旧实例端口失效——Browser.close 异步生效：不等待就起新进程会撞上未退出的旧实例。
func waitPortClosed(port, timeoutMs int, logf func(int, string)) {
	if port <= 0 {
		return
	}
	started := time.Now()
	for time.Since(started).Milliseconds() < int64(timeoutMs) {
		if !browserPortAlive(port) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	logf(2, "等待旧实例端口关闭超时: port="+strconv.Itoa(port))
}

// closeBrowserProcess 关闭浏览器进程——发 Browser.close 令浏览器自行退出，并等端口失效再返回。
//
// 为什么不靠进程句柄：启动器进程写完 DevToolsActivePort 即退出；真正持有 profile 的 browser
// 进程既不在这条句柄链上、也不在任何 pid 文件里——只有 CDP 端点认得它。
func closeBrowserProcess(port int, logf func(int, string)) {
	if port <= 0 {
		return
	}
	sendBrowserClose(port, logf)
	waitPortClosed(port, 5000, logf)
}

// sendBrowserClose 发 Browser.close——连 browser 级 CDP 端点令浏览器自行退出（连带全部子进程）。
func sendBrowserClose(port int, logf func(int, string)) {
	version, err := browserHTTPCall("http://127.0.0.1:"+strconv.Itoa(port)+"/json/version", false)
	if err != nil || version == "" {
		return
	}
	wsURL := extractJSONString(version, "webSocketDebuggerUrl")
	if wsURL == "" {
		return
	}
	ws, dialErr := wsDial(wsURL, 15*time.Second)
	if dialErr != nil {
		logf(1, "Browser.close 未能发出（实例可能已退出）: "+dialErr.Error())
		return
	}
	cdp := &cdpClient{conn: ws}
	if _, callErr := cdp.Call("Browser.close", "{}", browserCallTimeoutMs); callErr != nil {
		// Browser.close 一生效，浏览器立即退出、连接随之断开——握手未完成属预期，不是失败
		logf(1, "Browser.close 已发出（连接随实例退出关闭，预期）: "+callErr.Error())
	} else {
		logf(1, "浏览器进程已关闭（CDP Browser.close）: port="+strconv.Itoa(port))
	}
	_ = ws.Close()
}

// cleanupBrowser 清理实例——关浏览器进程 + 释放 CDP 连接 + 进程句柄兜底。
func cleanupBrowser(browser *catBrowser, logf func(int, string)) {
	// [段1] 关浏览器进程——CDP Browser.close 是唯一可靠手段（启动器进程句柄早已退出）
	closeBrowserProcess(browser.Port, logf)
	// [段2] 释放 CDP 连接
	if browser.ws != nil {
		_ = browser.ws.Close()
		browser.ws = nil
		browser.cdp = nil
	}
	// [段3] 进程句柄兜底（句柄仍有效且进程活着时才需要）
	if browser.proc != nil {
		_ = browser.proc.Kill()
	}
}

// selfHealBrowser 启动前自愈——读 kernel.pid，若对应进程仍在且是内核进程，则杀其进程树。
//
// 动机：Chromium detach——宿主退出不带走子进程；孤儿实例持续持有 profile，
// 会让新实例把命令行转交旧实例后退出、DevToolsActivePort 永不出现。
func selfHealBrowser(pidFile string, logf func(int, string)) {
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if convErr != nil || pid <= 0 {
		return
	}
	name := strings.TrimSuffix(processImageName(pid), ".exe")
	if name != "msedge" && name != "chrome" {
		return
	}
	logf(2, "自愈：清理残留浏览器内核实例 pid="+strconv.Itoa(pid))
	killProcessTree(pid, nil)
}
