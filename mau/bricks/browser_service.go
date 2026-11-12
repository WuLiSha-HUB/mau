package bricks

// 浏览器服务——browser-* 工具实现（BrowserCat 工具组 · A110）。
//
// 形态：浏览器内核 headless（Chromium 系：Edge / Chrome / Brave 等）+ 裸 CDP（零第三方依赖——
// net/http + 手写 WebSocket）。换内核 = 换配置 browser.kernel_path——Chromium 系协议同为 CDP，
// 无协议分支（Firefox / WebKit 不支持 CDP，需另写实现）。
//
// 每猫独立实例：<DataRoot>/browser/<catId>/profile（user-data-dir，持久）+ out（产物）。
// 生命周期域级——首次调用懒启动，timeback back 回收即关（会话侧 Close 钩子）；有头实例除外——
// 跨域保活（域内直接复用，仅显式 browser-headful close / open 换新时关闭）。
// 非主干信息：域门禁由宿主派发面判定（见 host/session），本服务不重复判定；同猫调用串行，跨猫互不干扰。

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 浏览器服务常量——对齐 C# CdpBrowserService 的预算与渲染口径。
const (
	// browserKernelPathKey 浏览器内核路径配置键（schema：browser.kernel_path · 文件 browser.cfg）
	browserKernelPathKey = "browser.kernel_path"
	// browserBootTimeoutMs 启动超时——等 DevToolsActivePort 出现
	browserBootTimeoutMs = 20000
	// browserNavTimeoutMs 导航预算——等 document.readyState 进入可读状态
	browserNavTimeoutMs = 20000
	// browserCallTimeoutMs 单次 CDP 调用上限（毫秒）——无上限时 WebSocket 可永久挂住
	browserCallTimeoutMs = 10000
	// browserEvalSliceMs 就绪轮询单次 eval 切片上限
	browserEvalSliceMs = 2000
	// browserSettleMs 加载静默期毫秒
	browserSettleMs = 800
	// browserViewportWidth 视口宽（Emulation 覆盖）
	browserViewportWidth = 1440
	// browserViewportHeight 视口高（Emulation 覆盖）
	browserViewportHeight = 900
	// browserInlineEvalChars eval 结果直返上限（超出落盘）
	browserInlineEvalChars = 4096
	// browserPreviewChars 摘要预览字符数
	browserPreviewChars = 1200
	// browserLinkPreviewCount 链接面预览条数
	browserLinkPreviewCount = 20
)

// BrowserService 是 browser-* 的执行面——本地 Chromium 系内核 + 裸 CDP。
type BrowserService struct {
	// DataRoot 数据根（其下 browser/<catId>/ 为 profile 与产物）
	DataRoot string
	// KernelPath 读配置 browser.kernel_path（每次启动时实时取；nil = 未配置口径）
	KernelPath func() string
	// Logf 日志出口（nil = 不出声）
	Logf func(level int, message string)
	// mu 实例表锁
	mu sync.Mutex
	// cats 每猫实例表
	cats map[string]*catBrowser
}

// NewBrowserService 构造浏览器服务（装配期一次；实例懒启动）。
func NewBrowserService(dataRoot string, kernelPath func() string, logf func(level int, message string)) *BrowserService {
	return &BrowserService{
		DataRoot:   dataRoot,
		KernelPath: kernelPath,
		Logf:       logf,
		cats:       make(map[string]*catBrowser),
	}
}

// log 出声（nil 出口即静默）。
func (s *BrowserService) log(level int, message string) {
	if s != nil && s.Logf != nil {
		s.Logf(level, message)
	}
}

// catBrowser 是单猫浏览器实例——进程 + CDP 连接 + 路径（Sync 保证同猫调用串行）。
type catBrowser struct {
	// Sync 同猫调用串行锁——单实例单连接，不开并发
	Sync sync.Mutex
	// CatID 猫 key
	CatID string
	// Port CDP 端口（DevToolsActivePort 发现）
	Port int
	// ProfileDir profile 目录（user-data-dir）
	ProfileDir string
	// OutDir 产物目录
	OutDir string
	// Headful 有头标记（browser-headful 起的实例 / 接管时按 UA 判别）
	Headful bool
	// TargetID 当前页签 id——操作面（Read / Eval / Shot）作用的 target
	TargetID string
	// cdp CDP 会话
	cdp *cdpClient
	// ws 当前页签 WebSocket 连接
	ws *wsConn
	// proc 启动器进程（Chromium detach 后可能已退出——只作兜底）
	proc *os.Process
}

// alive 存活判定——CDP 连接未显式关闭即可复用（不按进程句柄判活：启动器进程提前退出属常态）。
func (b *catBrowser) alive() bool {
	if b == nil || b.ws == nil {
		return false
	}
	return b.ws.isOpen()
}

// pageTarget 是页签（CDP page target）——DevTools HTTP 端点 /json/list 的条目。
type pageTarget struct {
	// ID target id（CDP 标识——select / close 用它定位）
	ID string
	// Title 页面标题
	Title string
	// URL 页面地址
	URL string
	// WsURL CDP WebSocket 地址（连接该页签用）
	WsURL string
}

// cdpClient 是极简 CDP 客户端——JSON-RPC over WebSocket（id 匹配；事件消息丢弃）。
type cdpClient struct {
	// conn 已连接的 WebSocket
	conn *wsConn
	// id 命令序号
	id int
}

// cdpTimeoutError 是 CDP 超时错误（对齐 C# TimeoutException 语义）。
type cdpTimeoutError struct {
	// Message 错误文本
	Message string
}

// Error 实现 error 接口。
func (e *cdpTimeoutError) Error() string { return e.Message }

// isCDPTimeout 报告错误是否 CDP 超时。
func isCDPTimeout(err error) bool {
	var timeout *cdpTimeoutError
	return errors.As(err, &timeout)
}

// Call 发送命令并等待同 id 响应——命令返回 error 时返回错误；超时返回 cdpTimeoutError。
func (c *cdpClient) Call(method, paramsJSON string, timeoutMs int) (json.RawMessage, error) {
	if paramsJSON == "" {
		paramsJSON = "{}"
	}
	c.id++
	id := c.id
	message := `{"id":` + strconv.Itoa(id) + `,"method":` + jsonString(method) + `,"params":` + paramsJSON + `}`
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	if err := c.conn.WriteText([]byte(message), time.Until(deadline)); err != nil {
		if wsTimeout(err) {
			return nil, &cdpTimeoutError{Message: "CDP " + method + " 发送超时（" + strconv.Itoa(timeoutMs) + "ms）"}
		}
		return nil, err
	}
	for {
		if time.Until(deadline) <= 0 {
			return nil, &cdpTimeoutError{Message: "CDP " + method + " 超时（" + strconv.Itoa(timeoutMs) + "ms 无响应）"}
		}
		text, err := c.conn.ReadMessage(deadline)
		if err != nil {
			if wsTimeout(err) {
				return nil, &cdpTimeoutError{Message: "CDP " + method + " 超时（" + strconv.Itoa(timeoutMs) + "ms 无响应）"}
			}
			return nil, err
		}
		var root map[string]json.RawMessage
		if json.Unmarshal([]byte(text), &root) != nil {
			continue
		}
		var responseID int
		idRaw, hasID := root["id"]
		if !hasID || json.Unmarshal(idRaw, &responseID) != nil || responseID != id {
			continue
		}
		if errRaw, hasErr := root["error"]; hasErr {
			return nil, errors.New("CDP " + method + " 失败: " + string(errRaw))
		}
		return json.RawMessage(text), nil
	}
}

// Open 打开页面并等待加载完成——返回状态摘要（标题 / 最终 URL / 状态 / 耗时）。
func (s *BrowserService) Open(catID, targetURL string) string {
	if targetURL == "" {
		return "ERR|BROWSER_ARGS|缺少参数 url"
	}
	if !isHTTPURL(targetURL) {
		return "ERR|BROWSER_ARGS|url 仅支持 http(s): " + targetURL
	}
	browser, errText := s.Ensure(catID)
	if browser == nil {
		return errText
	}
	browser.Sync.Lock()
	defer browser.Sync.Unlock()
	started := time.Now()
	if _, err := browser.cdp.Call("Page.navigate", `{"url":`+jsonString(targetURL)+`}`, browserBudgetSlice(started, browserNavTimeoutMs, browserCallTimeoutMs)); err != nil {
		if isCDPTimeout(err) {
			s.log(3, "browser.open 超时: "+err.Error())
			return "ERR|BROWSER_TIMEOUT|页面无法访问（CDP 无响应）: " + targetURL + " —— " + err.Error()
		}
		s.log(3, "browser.open 失败: "+err.Error())
		return "ERR|BROWSER_NAV|" + err.Error()
	}
	state, waitErr := waitReady(browser.cdp, browserBudgetRemaining(started, browserNavTimeoutMs))
	if waitErr != nil {
		s.log(3, "browser.open 失败: "+waitErr.Error())
		return "ERR|BROWSER_NAV|" + waitErr.Error()
	}
	if state != "interactive" && state != "complete" {
		return "ERR|BROWSER_TIMEOUT|页面无法访问（" + strconv.Itoa(browserNavTimeoutMs) + "ms 内未进入可读状态，readyState=" + state + "）: " + targetURL
	}
	time.Sleep(browserSettleMs * time.Millisecond)
	elapsed := time.Since(started).Milliseconds()
	title := safeEval(browser.cdp, "document.title")
	finalURL := safeEval(browser.cdp, "location.href")
	s.log(1, "browser.open "+catID+" "+finalURL+" "+strconv.FormatInt(elapsed, 10)+"ms")
	return "ready=" + state + " | title=" + title + " | url=" + finalURL + " | ms=" + strconv.FormatInt(elapsed, 10)
}

// Read 读取当前页面——mode: text（正文）/ ax（无障碍树精简）/ links（链接面）；产物落盘，返回摘要。
func (s *BrowserService) Read(catID, mode string) string {
	m := mode
	if m == "" {
		m = "text"
	}
	if m != "text" && m != "ax" && m != "links" {
		return "ERR|BROWSER_ARGS|mode 非法（text / ax / links）: " + m
	}
	browser, errText := s.Ensure(catID)
	if browser == nil {
		return errText
	}
	browser.Sync.Lock()
	defer browser.Sync.Unlock()
	if m == "text" {
		body, err := evalRaw(browser.cdp, "document.body ? document.body.innerText : ''", browserCallTimeoutMs)
		if err != nil {
			s.log(3, "browser.read 失败: "+err.Error())
			return "ERR|BROWSER_CDP|" + err.Error()
		}
		path := nextOutPath(browser, "read-text", "txt")
		if writeErr := os.WriteFile(path, []byte(body), 0o644); writeErr != nil {
			return "ERR|BROWSER_CDP|" + writeErr.Error()
		}
		return "mode=text | file=" + path + " | chars=" + strconv.Itoa(utf16Units(body)) + "\n" + browserPreview(body, browserPreviewChars)
	}
	if m == "links" {
		linksJSON, err := evalRaw(browser.cdp, browserLinksExpression, browserCallTimeoutMs)
		if err != nil {
			s.log(3, "browser.read 失败: "+err.Error())
			return "ERR|BROWSER_CDP|" + err.Error()
		}
		path := nextOutPath(browser, "read-links", "json")
		if writeErr := os.WriteFile(path, []byte(linksJSON), 0o644); writeErr != nil {
			return "ERR|BROWSER_CDP|" + writeErr.Error()
		}
		count := 0
		var rows []json.RawMessage
		if json.Unmarshal([]byte(linksJSON), &rows) == nil {
			count = len(rows)
		}
		return "mode=links | file=" + path + " | count=" + strconv.Itoa(count) + "\n" + browserPreview(buildLinkPreview(linksJSON), browserPreviewChars)
	}
	ax, err := browser.cdp.Call("Accessibility.getFullAXTree", "{}", browserCallTimeoutMs)
	if err != nil {
		s.log(3, "browser.read 失败: "+err.Error())
		return "ERR|BROWSER_CDP|" + err.Error()
	}
	slim := buildAxSlim(ax)
	path := nextOutPath(browser, "read-ax", "txt")
	if writeErr := os.WriteFile(path, []byte(slim), 0o644); writeErr != nil {
		return "ERR|BROWSER_CDP|" + writeErr.Error()
	}
	return "mode=ax | file=" + path + " | chars=" + strconv.Itoa(utf16Units(slim)) + "\n" + browserPreview(slim, browserPreviewChars)
}

// browserLinksExpression 是 links 观察面的提取表达式（a[href] → {t,h} 前 400 条）。
const browserLinksExpression = "JSON.stringify([...document.querySelectorAll('a[href]')].map(a => ({ t: (a.innerText || '').replace(/\\s+/g, ' ').trim().slice(0, 80), h: a.href })).slice(0, 400))"

// Eval 在当前页面执行 JS 表达式——小结果直返，大结果落盘。
func (s *BrowserService) Eval(catID, expression string) string {
	if expression == "" {
		return "ERR|BROWSER_ARGS|缺少参数 expression"
	}
	browser, errText := s.Ensure(catID)
	if browser == nil {
		return errText
	}
	browser.Sync.Lock()
	defer browser.Sync.Unlock()
	value, err := evalRaw(browser.cdp, expression, browserCallTimeoutMs)
	if err != nil {
		s.log(3, "browser.eval 失败: "+err.Error())
		return "ERR|BROWSER_CDP|" + err.Error()
	}
	chars := utf16Units(value)
	if chars <= browserInlineEvalChars {
		return "chars=" + strconv.Itoa(chars) + "\n" + value
	}
	path := nextOutPath(browser, "eval", "txt")
	if writeErr := os.WriteFile(path, []byte(value), 0o644); writeErr != nil {
		return "ERR|BROWSER_CDP|" + writeErr.Error()
	}
	return "chars=" + strconv.Itoa(chars) + " | file=" + path + "\n" + browserPreview(value, browserPreviewChars)
}

// Shot 截图当前页面——PNG 落盘，返回绝对路径（供 image-inject 直读）。
func (s *BrowserService) Shot(catID string, fullPage bool) string {
	browser, errText := s.Ensure(catID)
	if browser == nil {
		return errText
	}
	browser.Sync.Lock()
	defer browser.Sync.Unlock()
	params := `{"format":"png","captureBeyondViewport":` + strconv.FormatBool(fullPage) + `}`
	response, err := browser.cdp.Call("Page.captureScreenshot", params, browserCallTimeoutMs)
	if err != nil {
		s.log(3, "browser.shot 失败: "+err.Error())
		return "ERR|BROWSER_CDP|" + err.Error()
	}
	var root struct {
		Result struct {
			Data string `json:"data"`
		} `json:"result"`
	}
	b64 := ""
	if json.Unmarshal(response, &root) == nil {
		b64 = root.Result.Data
	}
	if b64 == "" {
		return "ERR|BROWSER_CDP|截图返回空数据"
	}
	raw, decodeErr := base64.StdEncoding.DecodeString(b64)
	if decodeErr != nil {
		return "ERR|BROWSER_CDP|" + decodeErr.Error()
	}
	path := nextOutPath(browser, "shot", "png")
	if writeErr := os.WriteFile(path, raw, 0o644); writeErr != nil {
		return "ERR|BROWSER_CDP|" + writeErr.Error()
	}
	size := pngSize(path)
	byteCount := int64(len(raw))
	if info, statErr := os.Stat(path); statErr == nil {
		byteCount = info.Size()
	}
	s.log(1, "browser.shot "+catID+" "+size+" "+strconv.FormatInt(byteCount, 10)+"B")
	return "file=" + path + " | full=" + strconv.FormatBool(fullPage) + " | px=" + size + " | bytes=" + strconv.FormatInt(byteCount, 10)
}

// Tabs 页签管理——list / new / select / close（DevTools HTTP 端点 + 当前页 WS 重连）。
func (s *BrowserService) Tabs(catID, action, target, url string) string {
	act := action
	if act == "" {
		act = "list"
	}
	if act != "list" && act != "new" && act != "select" && act != "close" {
		return "ERR|BROWSER_ARGS|action 非法（list / new / select / close）: " + act
	}
	if (act == "select" || act == "close") && target == "" {
		return "ERR|BROWSER_ARGS|" + act + " 需要参数 target（页签 id——先用 action=list 取）"
	}
	browser, errText := s.Ensure(catID)
	if browser == nil {
		return errText
	}
	browser.Sync.Lock()
	defer browser.Sync.Unlock()
	if act == "list" {
		return s.renderTabList(browser)
	}
	if act == "new" {
		if url != "" && !isHTTPURL(url) {
			return "ERR|BROWSER_ARGS|url 仅支持 http(s): " + url
		}
		// 建空白页签——内核忽略 /json/new 的 url 参数（实测确认），故导航改走 CDP（与 open 同源）
		endpoint := "http://127.0.0.1:" + strconv.Itoa(browser.Port) + "/json/new"
		created, httpErr := browserHTTPCall(endpoint, true)
		if httpErr != nil {
			s.log(2, "DevTools HTTP 端点调用失败: "+httpErr.Error())
		}
		newID := extractJSONString(created, "id")
		if newID == "" {
			return "ERR|BROWSER_CDP|新建页签失败（返回体无 id）: " + browserPreview(created, 200)
		}
		if err := s.attachTarget(browser, newID); err != nil {
			return "ERR|BROWSER_CDP|" + err.Error()
		}
		navNote := " | url=（空白）"
		if url != "" {
			navStarted := time.Now()
			if _, err := browser.cdp.Call("Page.navigate", `{"url":`+jsonString(url)+`}`, browserCallTimeoutMs); err != nil {
				return "ERR|BROWSER_CDP|" + err.Error()
			}
			navState, waitErr := waitReady(browser.cdp, browserNavTimeoutMs)
			if waitErr != nil {
				return "ERR|BROWSER_CDP|" + waitErr.Error()
			}
			if navState != "interactive" && navState != "complete" {
				return "ERR|BROWSER_TIMEOUT|新页签未在 " + strconv.Itoa(browserNavTimeoutMs) + "ms 内进入可读状态（readyState=" + navState + "）: " + url
			}
			time.Sleep(browserSettleMs * time.Millisecond)
			navNote = " | navigated=" + url + " | ms=" + strconv.FormatInt(time.Since(navStarted).Milliseconds(), 10)
		}
		s.log(1, "browser.tabs new "+catID+" "+newID)
		return "ok | 新页签 " + newID + navNote + "\n" + s.renderTabList(browser)
	}
	if act == "select" {
		endpoint := "http://127.0.0.1:" + strconv.Itoa(browser.Port) + "/json/activate/" + target
		resp, httpErr := browserHTTPCall(endpoint, false)
		if httpErr != nil {
			s.log(2, "DevTools HTTP 端点调用失败: "+httpErr.Error())
		}
		if err := s.attachTarget(browser, target); err != nil {
			return "ERR|BROWSER_CDP|" + err.Error()
		}
		s.log(1, "browser.tabs select "+catID+" "+target)
		return "ok | 已切换到 " + target + " | resp=" + browserPreview(resp, 80) + "\n" + s.renderTabList(browser)
	}
	closingCurrent := target == browser.TargetID
	closeEndpoint := "http://127.0.0.1:" + strconv.Itoa(browser.Port) + "/json/close/" + target
	closeResp, httpErr := browserHTTPCall(closeEndpoint, false)
	if httpErr != nil {
		s.log(2, "DevTools HTTP 端点调用失败: "+httpErr.Error())
	}
	if closingCurrent {
		remaining := s.listTargets(browser.Port)
		if len(remaining) == 0 {
			browser.TargetID = ""
			return "ok | 已关闭 " + target + "（无剩余页签——下次 open 会自动新建）"
		}
		if err := s.attachTarget(browser, remaining[0].ID); err != nil {
			return "ERR|BROWSER_CDP|" + err.Error()
		}
	}
	s.log(1, "browser.tabs close "+catID+" "+target)
	return "ok | 已关闭 " + target + " | resp=" + browserPreview(closeResp, 80) + "\n" + s.renderTabList(browser)
}

// Headful 有头登录实例——browser-headful 执行面（域外专属：主干人工登录用）。
//
// open：关现有实例（含有头——换新窗口）→ 用同 profile 起有头浏览器 → 可选导航 url。
// close：优雅关闭（CDP Browser.close——profile 落盘，登录态保留）。
func (s *BrowserService) Headful(catID, action, url string) string {
	if catID == "" {
		return "ERR|BROWSER_ARGS|缺少 catId"
	}
	if action != "" && action != "open" && action != "close" {
		return "ERR|BROWSER_ARGS|action 非法（open / close）: " + action
	}
	if action == "close" {
		s.closeInstances(catID, true)
		s.log(1, "browser.headful 关闭 "+catID)
		return "有头浏览器已关闭（profile 已落盘——登录态保留）"
	}
	if url != "" && !isHTTPURL(url) {
		return "ERR|BROWSER_ARGS|url 仅支持 http(s): " + url
	}
	s.closeInstances(catID, true)
	browser, errText := s.launch(catID, true)
	if browser == nil {
		return errText
	}
	s.mu.Lock()
	s.cats[catID] = browser
	s.mu.Unlock()
	nav := ""
	if url != "" {
		browser.Sync.Lock()
		if _, err := browser.cdp.Call("Page.navigate", `{"url":`+jsonString(url)+`}`, browserCallTimeoutMs); err != nil {
			nav = " | 导航失败: " + err.Error()
		} else if state, waitErr := waitReady(browser.cdp, browserNavTimeoutMs); waitErr != nil {
			nav = " | 导航失败: " + waitErr.Error()
		} else {
			nav = " | url=" + url + " | ready=" + state
		}
		browser.Sync.Unlock()
	}
	s.log(1, "browser.headful 就绪 "+catID+" port="+strconv.Itoa(browser.Port))
	return "有头浏览器已就绪（窗口已弹出——请登录；完成后调本工具 action=close 关闭）| port=" + strconv.Itoa(browser.Port) + nav
}

// Prepare 预热指定猫的实例——timeback 开锚钩子（失败只记日志，不阻断开锚）。
func (s *BrowserService) Prepare(catID string) {
	if catID == "" {
		return
	}
	if _, errText := s.Ensure(catID); errText != "" {
		s.log(2, "开锚预热浏览器未成功（锚内工具调用会重试）: "+errText)
		return
	}
	s.log(1, "开锚预热浏览器就绪: "+catID)
}

// Close 关闭指定猫的实例——timeback 回收钩子（有头实例保活，等待人的动作）。
func (s *BrowserService) Close(catID string) {
	s.closeInstances(catID, false)
}

// Shutdown 关闭并清理全部实例——宿主退出钩子调用；有头实例保活（不关闭——等待人的动作）。
func (s *BrowserService) Shutdown() {
	all := make([]*catBrowser, 0, len(s.cats))
	kept := 0
	s.mu.Lock()
	for _, browser := range s.cats {
		if browser.Headful && browser.alive() {
			kept++
			continue
		}
		all = append(all, browser)
	}
	s.cats = make(map[string]*catBrowser)
	s.mu.Unlock()
	for _, browser := range all {
		cleanupBrowser(browser, s.log)
	}
	if kept > 0 {
		s.log(1, "有头实例保活（不随宿主退出关闭）: "+strconv.Itoa(kept)+" 个")
	}
	s.log(1, "浏览器实例清理完成，共 "+strconv.Itoa(len(all))+" 个")
}

// Ensure 取本猫实例——三级取用：内存实例存活复用（含有头人工窗口）→ 接管 profile 内活实例 → 懒启动。
func (s *BrowserService) Ensure(catID string) (*catBrowser, string) {
	if catID == "" {
		return nil, "ERR|BROWSER_ARGS|缺少 catId"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.cats[catID]; ok {
		if existing.alive() {
			// 存活实例一律复用——含人工登录的有头窗口（域内工具就地驱动它，不再关掉换无头）
			return existing, ""
		}
		cleanupBrowser(existing, s.log)
		delete(s.cats, catID)
	}
	// 接管优先——宿主重启后实例表为空，但 profile 里可能有活实例（Chromium detach）
	if adopted := s.tryAdopt(catID); adopted != nil {
		s.cats[catID] = adopted
		return adopted, ""
	}
	launched, errText := s.launch(catID, false)
	if launched == nil {
		return nil, errText
	}
	s.cats[catID] = launched
	return launched, ""
}

// tryAdopt 接管已存在的浏览器实例——读 profile 里的端口锚，端口可连则复用（不启新进程）。
func (s *BrowserService) tryAdopt(catID string) *catBrowser {
	dir := filepath.Join(s.DataRoot, "browser", catID)
	profileDir := filepath.Join(dir, "profile")
	// 锚双源——自维护 port.txt 优先；Chromium 的 DevToolsActivePort 兜底
	port := readAnchorPort(filepath.Join(dir, "port.txt"))
	if port == 0 {
		port = readAnchorPort(filepath.Join(profileDir, "DevToolsActivePort"))
	}
	if port == 0 {
		return nil
	}
	probe, err := browserHTTPCall("http://127.0.0.1:"+strconv.Itoa(port)+"/json/version", false)
	if err != nil || probe == "" {
		return nil
	}
	browser := &catBrowser{CatID: catID, Port: port, ProfileDir: profileDir, OutDir: filepath.Join(dir, "out")}
	// 有头判别——/json/version 的 User-Agent 带 HeadlessChrome 即无头；UA 缺失按无头处理
	ua := extractJSONString(probe, "User-Agent")
	browser.Headful = ua != "" && !strings.Contains(strings.ToLower(ua), "headlesschrome")
	if err := s.attachTarget(browser, ""); err != nil {
		s.log(1, "接管探测失败（按无实例处理）: "+err.Error())
		return nil
	}
	s.log(1, "接管已有浏览器实例: "+catID+" port="+strconv.Itoa(port)+" headful="+strconv.FormatBool(browser.Headful))
	return browser
}

// launch 启动本猫浏览器实例——起进程 → 等端口 → 连 CDP → 建会话。
func (s *BrowserService) launch(catID string, headful bool) (*catBrowser, string) {
	kernelPath, errText := s.resolveKernelPath()
	if kernelPath == "" {
		return nil, errText
	}
	dir := filepath.Join(s.DataRoot, "browser", catID)
	profileDir := filepath.Join(dir, "profile")
	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return nil, "ERR|BROWSER_LAUNCH|目录创建失败: " + err.Error()
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, "ERR|BROWSER_LAUNCH|目录创建失败: " + err.Error()
	}
	portFile := filepath.Join(profileDir, "DevToolsActivePort")
	pidFile := filepath.Join(profileDir, "kernel.pid")
	if err := os.Remove(portFile); err != nil && !os.IsNotExist(err) {
		s.log(2, "旧端口文件删除失败（继续）: "+err.Error())
	}
	// 启动前自愈——清掉上次宿主遗留、可能仍占用 profile 的实例（Chromium detach：宿主退出不带走子进程）
	selfHealBrowser(pidFile, s.log)
	mode := "headless"
	if headful {
		mode = "headful"
	}
	args := make([]string, 0, 12)
	if !headful {
		args = append(args, "--headless=new")
	}
	args = append(args,
		"--remote-debugging-port=0",
		"--user-data-dir="+profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		// 同步切断——Edge 同步会把 session.restore_on_startup 覆盖回 1，导致启动恢复上次会话、页签累积
		"--disable-sync",
		"--window-size="+strconv.Itoa(browserViewportWidth)+","+strconv.Itoa(browserViewportHeight),
	)
	if !headful {
		// 无头才藏滚动条——有头是给人看的窗口，滚动条要在
		args = append(args, "--hide-scrollbars")
	} else {
		// 有头登录窗口——关掉自动化特征开关，降低站点反自动化判据命中率
		args = append(args, "--disable-blink-features=AutomationControlled")
	}
	args = append(args, "about:blank")
	cmd := exec.Command(kernelPath, args...)
	prepareBrowserCommand(cmd)
	if err := cmd.Start(); err != nil {
		return nil, "ERR|BROWSER_LAUNCH|浏览器内核进程启动失败: " + err.Error()
	}
	pid := cmd.Process.Pid
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		s.log(2, "PID 落盘失败（自愈能力降级）: "+err.Error())
	}
	port := waitPortFile(portFile, browserBootTimeoutMs)
	if port == 0 {
		killProcessTree(pid, cmd.Process)
		return nil, "ERR|BROWSER_LAUNCH|浏览器内核未在 " + strconv.Itoa(browserBootTimeoutMs) + "ms 内就绪（DevToolsActivePort 未出现）"
	}
	browser := &catBrowser{
		CatID:      catID,
		Port:       port,
		ProfileDir: profileDir,
		OutDir:     outDir,
		Headful:    headful,
		proc:       cmd.Process,
	}
	if err := s.attachTarget(browser, ""); err != nil {
		killProcessTree(pid, cmd.Process)
		return nil, "ERR|BROWSER_CONNECT|连接首个页签失败: " + err.Error()
	}
	// 持久端口锚——只在启动成功时覆写、从不删除：宿主重启 / 启动失败后据此接管存活实例
	if err := os.WriteFile(filepath.Join(dir, "port.txt"), []byte(strconv.Itoa(port)), 0o644); err != nil {
		s.log(2, "端口锚落盘失败（接管能力降级）: "+err.Error())
	}
	s.log(1, "浏览器实例就绪: "+catID+" port="+strconv.Itoa(port)+" pid="+strconv.Itoa(pid)+" target="+browser.TargetID+" mode="+mode)
	return browser, ""
}

// closeInstances 关闭实例核心——includeHeadful=false（域回收）：有头实例保活；
// includeHeadful=true（browser-headful open 换新 / close 收尾）：有头实例一并关闭。
func (s *BrowserService) closeInstances(catID string, includeHeadful bool) {
	targets := make([]*catBrowser, 0, 2)
	keptHeadful := false
	s.mu.Lock()
	removeKeys := make([]string, 0, 2)
	for key, browser := range s.cats {
		if catID != "" && key != catID {
			continue
		}
		if !includeHeadful && browser.Headful && browser.alive() {
			keptHeadful = true
			continue
		}
		targets = append(targets, browser)
		removeKeys = append(removeKeys, key)
	}
	for _, key := range removeKeys {
		delete(s.cats, key)
	}
	s.mu.Unlock()
	for _, browser := range targets {
		cleanupBrowser(browser, s.log)
	}
	// 兜底——本次宿主未曾接管的残留实例（宿主上次被强杀遗留）不在内存表里，
	// 但锚文件仍指向它：域结束 = 实例不该存在，按锚补一刀（探测不通即静默返回）。
	// 有头保活时跳过——锚指向的正是保活实例，补刀会把它误杀。
	if catID != "" && !keptHeadful {
		anchor := filepath.Join(s.DataRoot, "browser", catID, "port.txt")
		if stale := readAnchorPort(anchor); stale > 0 {
			closeBrowserProcess(stale, s.log)
		}
		clearPortAnchor(anchor, s.log)
	}
	if len(targets) > 0 {
		scope := catID
		if scope == "" {
			scope = "全部"
		}
		s.log(1, "浏览器实例关闭: "+scope+" · 共 "+strconv.Itoa(len(targets))+" 个")
	}
	if keptHeadful {
		s.log(1, "有头实例保活（不随域回收关闭）: "+catID)
	}
}

// resolveKernelPath 解析浏览器内核可执行路径——读配置 browser.kernel_path；未配置 / 不存在一律明示。
func (s *BrowserService) resolveKernelPath() (string, string) {
	path := ""
	if s.KernelPath != nil {
		path = s.KernelPath()
	}
	if path == "" {
		return "", "ERR|BROWSER_KERNEL_MISSING|浏览器内核未配置——请在配置面填写 " + browserKernelPathKey + "（Chromium 系可执行文件全路径：msedge.exe / chrome.exe 等）"
	}
	if _, err := os.Stat(path); err != nil {
		return "", "ERR|BROWSER_KERNEL_NOT_FOUND|配置的浏览器内核路径不存在：" + path + "——请更新 " + browserKernelPathKey
	}
	return path, ""
}

// renderTabList 渲染页签清单——`* ` 标记当前页签。
func (s *BrowserService) renderTabList(browser *catBrowser) string {
	pages := s.listTargets(browser.Port)
	var b strings.Builder
	b.WriteString("count=" + strconv.Itoa(len(pages)) + "（* = 当前页签）\n")
	for _, page := range pages {
		mark := " "
		if page.ID == browser.TargetID {
			mark = "*"
		}
		b.WriteString(mark + " " + page.ID + " | " + page.Title + " | " + page.URL + "\n")
	}
	return b.String()
}

// listTargets 列全部 page 类 target——DevTools HTTP 端点 /json/list（解析失败为空表）。
func (s *BrowserService) listTargets(port int) []pageTarget {
	endpoint := "http://127.0.0.1:" + strconv.Itoa(port) + "/json/list"
	jsonText, err := browserHTTPCall(endpoint, false)
	if err != nil {
		s.log(2, "DevTools HTTP 端点调用失败: "+err.Error())
		return nil
	}
	if jsonText == "" {
		return nil
	}
	var rows []struct {
		Type  string `json:"type"`
		ID    string `json:"id"`
		Title string `json:"title"`
		URL   string `json:"url"`
		WsURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.Unmarshal([]byte(jsonText), &rows); err != nil {
		s.log(2, "页签清单解析失败: "+err.Error())
		return nil
	}
	out := make([]pageTarget, 0, len(rows))
	for _, row := range rows {
		if row.Type != "page" {
			continue
		}
		out = append(out, pageTarget{ID: row.ID, Title: row.Title, URL: row.URL, WsURL: row.WsURL})
	}
	return out
}

// resolveTarget 解析目标页签——targetID 为空则取第一个。
func (s *BrowserService) resolveTarget(port int, targetID string) *pageTarget {
	pages := s.listTargets(port)
	if len(pages) == 0 {
		return nil
	}
	if targetID == "" {
		return &pages[0]
	}
	for i := range pages {
		if pages[i].ID == targetID {
			return &pages[i]
		}
	}
	return nil
}

// attachTarget 把当前 CDP 连接挂到指定页签——关旧 WS、连新 target、重做 enable 与视口覆盖。
func (s *BrowserService) attachTarget(browser *catBrowser, targetID string) error {
	target := s.resolveTarget(browser.Port, targetID)
	if target == nil {
		return errors.New("未找到页签: " + targetID)
	}
	if browser.ws != nil {
		_ = browser.ws.Close()
		browser.ws = nil
		browser.cdp = nil
	}
	ws, err := wsDial(target.WsURL, 15*time.Second)
	if err != nil {
		return err
	}
	cdp := &cdpClient{conn: ws}
	if _, err := cdp.Call("Page.enable", "{}", browserCallTimeoutMs); err != nil {
		_ = ws.Close()
		return err
	}
	if _, err := cdp.Call("Runtime.enable", "{}", browserCallTimeoutMs); err != nil {
		_ = ws.Close()
		return err
	}
	// 视口覆盖——有头实例跳过：窗口尺寸由启动参数决定，覆盖会成为页面侧自动化判据
	if !browser.Headful {
		params := `{"width":` + strconv.Itoa(browserViewportWidth) + `,"height":` + strconv.Itoa(browserViewportHeight) + `,"deviceScaleFactor":1,"mobile":false}`
		if _, err := cdp.Call("Emulation.setDeviceMetricsOverride", params, browserCallTimeoutMs); err != nil {
			_ = ws.Close()
			return err
		}
	}
	browser.ws = ws
	browser.cdp = cdp
	browser.TargetID = target.ID
	return nil
}
