using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Net.Http;
using System.Net.WebSockets;
using System.Text;
using System.Text.Json;
using System.Threading;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// 浏览器服务——browser-* 工具实现（BrowserCat 工具组 · A110）。
    /// 形态：浏览器内核 headless（Chromium 系：Edge / Chrome / Brave 等）+ 裸 CDP（零第三方依赖——HttpClient + ClientWebSocket）。
    /// 🔴 换内核 = 换配置 browser.kernel_path——Chromium 系协议同为 CDP，无协议分支（Firefox / WebKit 不支持 CDP，需另写实现）。
    /// 每猫独立实例：Data/browser/&lt;catId&gt;/profile（user-data-dir，持久）+ out（产物）。
    /// 生命周期域级——首次调用懒启动，timeback back 回收即关（IBrowserLifecycle.CloseCat）。
    /// 非主干信息：域门禁由宿主派发面判定（见 ChatSession），本服务不重复判定；同猫调用串行，跨猫互不干扰。
    /// </summary>
    public sealed class CdpBrowserService : IBrowserService, IBrowserLifecycle
    {
        /// <summary>浏览器内核路径配置键——配置面声明项（schema：browser.kernel_path · 文件 browser.cfg）</summary>
        private const string KernelPathKey = "browser.kernel_path";

        /// <summary>启动超时——等 DevToolsActivePort 出现</summary>
        private const long BootTimeoutMs = 20000;

        /// <summary>导航预算——等 document.readyState 进入可读状态；20 秒判「无法访问」（莎 2026-11-02 定：打不开页面是常态，不该拖到工具层 120 秒）</summary>
        private const long NavTimeoutMs = 20000;

        /// <summary>单次 CDP 调用上限（毫秒）——收发无上限时 WebSocket 可永久挂住（A295 根因：就绪判据轮不到，工具层超时与后台线程脱钩）</summary>
        private const int CallTimeoutMs = 10000;

        /// <summary>就绪轮询单次 eval 切片上限（毫秒）——保证 20 秒预算能被按时判出（单次调用不得超过剩余预算）</summary>
        private const int EvalSliceMs = 2000;

        /// <summary>加载静默期毫秒——可读状态后再等一拍，兜住 SPA 挂载与首屏渲染尾巴</summary>
        private const int SettleMs = 800;

        /// <summary>视口宽——Emulation 覆盖用</summary>
        private const int ViewportWidth = 1440;

        /// <summary>视口高——Emulation 覆盖用</summary>
        private const int ViewportHeight = 900;

        /// <summary>eval 结果直返上限（超出落盘）</summary>
        private const int InlineEvalChars = 4096;

        /// <summary>摘要预览字符数——回执里带头部内容，减少二次读盘往返</summary>
        private const int PreviewChars = 1200;

        /// <summary>链接面预览条数</summary>
        private const int LinkPreviewCount = 20;

        /// <summary>实例表访问锁（每猫实例各自串行——见 CatBrowser.Sync）</summary>
        private readonly object _gate = new object();

        /// <summary>每猫实例表</summary>
        private readonly Dictionary<string, CatBrowser> _cats = new Dictionary<string, CatBrowser>();

        /// <summary>数据根（Program.ResolveDataRoot 解析——数据目录 = dataRoot/Data）</summary>
        private readonly string _dataRoot;

        /// <summary>
        /// 构造——数据根由宿主注入（与 PsService / 观测面同源单点解析）
        /// </summary>
        /// <param name="dataRoot">数据根（其下 Data/ 为持久化数据目录）</param>
        public CdpBrowserService(string dataRoot)
        {
            _dataRoot = dataRoot;
        }

        /// <summary>
        /// 打开页面并等待加载完成——返回状态摘要（标题 / 最终 URL / 状态 / 耗时）
        /// </summary>
        /// <param name="catId">猫 key（定位本猫 profile 与实例）</param>
        /// <param name="url">目标地址（仅 http/https）</param>
        /// <returns>状态摘要或 ERR| 错误文本</returns>
        public string Open(string catId, string url)
        {
            if (url.Length == 0)
            {
                return "ERR|BROWSER_ARGS|缺少参数 url";
            }
            if (!url.StartsWith("http://", StringComparison.OrdinalIgnoreCase) && !url.StartsWith("https://", StringComparison.OrdinalIgnoreCase))
            {
                return "ERR|BROWSER_ARGS|url 仅支持 http(s): " + url;
            }
            string error;
            CatBrowser browser = Ensure(catId, out error);
            if (browser == null)
            {
                return error;
            }
            lock (browser.Sync)
            {
                try
                {
                    // [预算] 一次 open 全程受 NavTimeoutMs 约束——打不开页面是常态，20 秒必出结果（A295 · 莎 2026-11-02）
                    Stopwatch sw = Stopwatch.StartNew();
                    browser.Cdp.Call("Page.navigate", JsonUtil.Serialize(new { url = url }), BudgetSlice(sw, NavTimeoutMs, CallTimeoutMs));
                    string state = WaitReady(browser.Cdp, BudgetRemaining(sw, NavTimeoutMs));
                    if (state != "interactive" && state != "complete")
                    {
                        return "ERR|BROWSER_TIMEOUT|页面无法访问（" + NavTimeoutMs.ToString() + "ms 内未进入可读状态，readyState=" + state + "）: " + url;
                    }
                    Thread.Sleep(SettleMs);
                    sw.Stop();
                    string title = SafeEval(browser.Cdp, "document.title");
                    string finalUrl = SafeEval(browser.Cdp, "location.href");
                    LogStore.Add("CatHome4", 1, "browser.open " + catId + " " + finalUrl + " " + sw.ElapsedMilliseconds + "ms", "BROWSER");
                    return "ready=" + state + " | title=" + title + " | url=" + finalUrl + " | ms=" + sw.ElapsedMilliseconds;
                }
                catch (TimeoutException ex)
                {
                    // A295——CDP 无响应即判不可达：不把一次 open 拖成无限等待（工具层超时与后台线程脱钩的根因）
                    LogStore.Add("CatHome4", 3, "browser.open 超时: " + ex.Message, "BROWSER");
                    return "ERR|BROWSER_TIMEOUT|页面无法访问（CDP 无响应）: " + url + " —— " + ex.Message;
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 3, "browser.open 失败: " + ex.Message, "BROWSER");
                    return "ERR|BROWSER_NAV|" + ex.GetType().Name + ": " + ex.Message;
                }
            }
        }
        /// <summary>预算剩余（毫秒）——下限 200，保证轮询能拿到一次有效切片（A295）</summary>
        /// <param name="sw">起点计时器</param>
        /// <param name="budgetMs">总预算毫秒</param>
        /// <returns>剩余毫秒（≥200）</returns>
        private static int BudgetRemaining(Stopwatch sw, long budgetMs)
        {
            long remaining = budgetMs - sw.ElapsedMilliseconds;
            if (remaining < 200)
            {
                return 200;
            }
            return (int)remaining;
        }
        /// <summary>预算切片（毫秒）——取剩余与单次调用上限的较小者（A295）</summary>
        /// <param name="sw">起点计时器</param>
        /// <param name="budgetMs">总预算毫秒</param>
        /// <param name="capMs">单次调用上限毫秒</param>
        /// <returns>本次调用可用的超时毫秒</returns>
        private static int BudgetSlice(Stopwatch sw, long budgetMs, int capMs)
        {
            int remaining = BudgetRemaining(sw, budgetMs);
            if (remaining < capMs)
            {
                return remaining;
            }
            return capMs;
        }
        /// <summary>取值兜底——超时不抛（已判完可读状态后的标题 / 地址取值不该让整单失败；A295）</summary>
        /// <param name="cdp">CDP 会话</param>
        /// <param name="expression">取值表达式</param>
        /// <returns>取值结果（失败为空串）</returns>
        private static string SafeEval(CdpClient cdp, string expression)
        {
            try
            {
                return EvalRaw(cdp, expression);
            }
            catch (Exception)
            {
                return "";
            }
        }

        /// <summary>
        /// 读取当前页面——mode: text（正文）/ ax（无障碍树精简）/ links（链接面）；产物落盘，返回摘要
        /// </summary>
        /// <param name="catId">猫 key</param>
        /// <param name="mode">观察面模式（text / ax / links）</param>
        /// <returns>摘要（路径 + 规模 + 预览）或 ERR| 错误文本</returns>
        public string Read(string catId, string mode)
        {
            string m = mode.Length == 0 ? "text" : mode;
            if (m != "text" && m != "ax" && m != "links")
            {
                return "ERR|BROWSER_ARGS|mode 非法（text / ax / links）: " + m;
            }
            string error;
            CatBrowser browser = Ensure(catId, out error);
            if (browser == null)
            {
                return error;
            }
            lock (browser.Sync)
            {
                try
                {
                    if (m == "text")
                    {
                        string body = EvalRaw(browser.Cdp, "document.body ? document.body.innerText : ''");
                        string path = NextOutPath(browser, "read-text", "txt");
                        File.WriteAllText(path, body, new UTF8Encoding(false));
                        return "mode=text | file=" + path + " | chars=" + body.Length + "\n" + Preview(body, PreviewChars);
                    }
                    if (m == "links")
                    {
                        string linksJson = EvalRaw(browser.Cdp,
                            "JSON.stringify([...document.querySelectorAll('a[href]')].map(a => ({ t: (a.innerText || '').replace(/\\s+/g, ' ').trim().slice(0, 80), h: a.href })).slice(0, 400))");
                        string path = NextOutPath(browser, "read-links", "json");
                        File.WriteAllText(path, linksJson, new UTF8Encoding(false));
                        int count = 0;
                        using (JsonDocument doc = JsonUtil.ParseStrict(linksJson))
                        {
                            count = doc.RootElement.GetArrayLength();
                        }
                        return "mode=links | file=" + path + " | count=" + count + "\n" + Preview(BuildLinkPreview(linksJson), PreviewChars);
                    }
                    JsonElement ax = browser.Cdp.Call("Accessibility.getFullAXTree", "{}");
                    string slim = BuildAxSlim(ax);
                    string axPath = NextOutPath(browser, "read-ax", "txt");
                    File.WriteAllText(axPath, slim, new UTF8Encoding(false));
                    return "mode=ax | file=" + axPath + " | chars=" + slim.Length + "\n" + Preview(slim, PreviewChars);
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 3, "browser.read 失败: " + ex.Message, "BROWSER");
                    return "ERR|BROWSER_CDP|" + ex.GetType().Name + ": " + ex.Message;
                }
            }
        }

        /// <summary>
        /// 在当前页面执行 JS 表达式——小结果直返，大结果落盘
        /// </summary>
        /// <param name="catId">猫 key</param>
        /// <param name="expression">JS 表达式</param>
        /// <returns>结果文本（或 ERR| 错误文本）</returns>
        public string Eval(string catId, string expression)
        {
            if (expression.Length == 0)
            {
                return "ERR|BROWSER_ARGS|缺少参数 expression";
            }
            string error;
            CatBrowser browser = Ensure(catId, out error);
            if (browser == null)
            {
                return error;
            }
            lock (browser.Sync)
            {
                try
                {
                    string value = EvalRaw(browser.Cdp, expression);
                    if (value.Length <= InlineEvalChars)
                    {
                        return "chars=" + value.Length + "\n" + value;
                    }
                    string path = NextOutPath(browser, "eval", "txt");
                    File.WriteAllText(path, value, new UTF8Encoding(false));
                    return "chars=" + value.Length + " | file=" + path + "\n" + Preview(value, PreviewChars);
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 3, "browser.eval 失败: " + ex.Message, "BROWSER");
                    return "ERR|BROWSER_CDP|" + ex.GetType().Name + ": " + ex.Message;
                }
            }
        }

        /// <summary>
        /// 截图当前页面——PNG 落盘，返回绝对路径（供 image-inject 直读）
        /// </summary>
        /// <param name="catId">猫 key</param>
        /// <param name="fullPage">true=全页（captureBeyondViewport）；false=当前视口</param>
        /// <returns>PNG 绝对路径或 ERR| 错误文本</returns>
        public string Shot(string catId, bool fullPage)
        {
            string error;
            CatBrowser browser = Ensure(catId, out error);
            if (browser == null)
            {
                return error;
            }
            lock (browser.Sync)
            {
                try
                {
                    JsonElement shot = browser.Cdp.Call("Page.captureScreenshot", JsonUtil.Serialize(new { format = "png", captureBeyondViewport = fullPage }));
                    string b64 = "";
                    if (shot.TryGetProperty("result", out JsonElement res) && res.TryGetProperty("data", out JsonElement dataEl))
                    {
                        b64 = dataEl.GetString() ?? "";
                    }
                    if (b64.Length == 0)
                    {
                        return "ERR|BROWSER_CDP|截图返回空数据";
                    }
                    string path = NextOutPath(browser, "shot", "png");
                    File.WriteAllBytes(path, Convert.FromBase64String(b64));
                    FileInfo fi = new FileInfo(path);
                    string size = PngSize(path);
                    LogStore.Add("CatHome4", 1, "browser.shot " + catId + " " + size + " " + fi.Length + "B", "BROWSER");
                    return "file=" + path + " | full=" + fullPage.ToString() + " | px=" + size + " | bytes=" + fi.Length;
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 3, "browser.shot 失败: " + ex.Message, "BROWSER");
                    return "ERR|BROWSER_CDP|" + ex.GetType().Name + ": " + ex.Message;
                }
            }
        }

        /// <summary>
        /// 关闭并清理全部实例——宿主退出钩子调用（Main finally 与 CloseWriters 同处）。
        /// 🔴 Chromium 启动后 detach：宿主被杀不带走子进程；不清理则孤儿实例持续占用 profile。
        /// </summary>
        public void Shutdown()
        {
            List<CatBrowser> all = new List<CatBrowser>();
            lock (_gate)
            {
                foreach (KeyValuePair<string, CatBrowser> pair in _cats)
                {
                    all.Add(pair.Value);
                }
                _cats.Clear();
            }
            int i = 0;
            while (i < all.Count)
            {
                Cleanup(all[i]);
                i = i + 1;
            }
            LogStore.Add("CatHome4", 1, "浏览器实例清理完成，共 " + i.ToString() + " 个", "BROWSER");
        }
        /// <summary>
        /// 预热指定猫的浏览器实例——timeback 开锚时调用（A123 域级生命周期主路径）。
        /// 起失败不抛（开锚不该被浏览器问题阻断）：错误已记日志，锚内工具调用会再试一次并如实返回 ERR。
        /// </summary>
        /// <param name="catId">猫 key</param>
        public void PrepareCat(string catId)
        {
            if (catId.Length == 0)
            {
                return;
            }
            string error = "";
            CatBrowser browser = Ensure(catId, out error);
            if (browser == null)
            {
                LogStore.Add("CatHome4", 2, "开锚预热浏览器未成功（锚内工具调用会重试）: " + error, "BROWSER");
                return;
            }
            LogStore.Add("CatHome4", 1, "开锚预热浏览器就绪: " + catId + " port=" + browser.Port.ToString(), "BROWSER");
        }

        /// <summary>
        /// 关闭并清理指定猫的实例——timeback 作用域回收时调用（域级生命周期主路径）。
        /// </summary>
        /// <param name="catId">猫 key（空=全部）</param>
        public void CloseCat(string catId)
        {
            List<CatBrowser> targets = new List<CatBrowser>();
            lock (_gate)
            {
                if (catId.Length == 0)
                {
                    foreach (KeyValuePair<string, CatBrowser> pair in _cats)
                    {
                        targets.Add(pair.Value);
                    }
                    _cats.Clear();
                }
                else
                {
                    CatBrowser one;
                    if (_cats.TryGetValue(catId, out one))
                    {
                        targets.Add(one);
                        _cats.Remove(catId);
                    }
                }
            }
            int i = 0;
            while (i < targets.Count)
            {
                Cleanup(targets[i]);
                i = i + 1;
            }
            // 兜底（A123）——本次宿主未曾接管的残留实例（宿主上次被强杀遗留）不在内存表里，
            // 但锚文件仍指向它：域结束 = 实例不该存在，按锚补一刀（探测不通即静默返回）
            if (catId.Length > 0)
            {
                string anchor = Path.Combine(_dataRoot, "Data", "browser", catId, "port.txt");
                int stale = ReadAnchorPort(anchor);
                if (stale > 0)
                {
                    CloseBrowserProcess(stale);
                }
                ClearPortAnchor(anchor);
            }
            if (i > 0)
            {
                string scope = catId;
                if (scope.Length == 0)
                {
                    scope = "全部";
                }
                LogStore.Add("CatHome4", 1, "浏览器实例关闭（域回收）: " + scope + " · 共 " + i.ToString() + " 个", "BROWSER");
            }
        }

        /// <summary>
        /// 页签管理——list / new / select / close（DevTools HTTP 端点 + 当前页 WS 重连）。
        /// 多页签的价值：URL 寻路时"开着不关"——保留来过哪一页，可回退与对比。
        /// </summary>
        /// <param name="catId">猫 key</param>
        /// <param name="action">动作（缺省 list）</param>
        /// <param name="target">页签 id（new 可空；select / close 必填）</param>
        /// <param name="url">新建页签地址（仅 new 用）</param>
        /// <returns>结果摘要或 ERR| 错误文本</returns>
        public string Tabs(string catId, string action, string target, string url)
        {
            string act = action;
            if (act.Length == 0)
            {
                act = "list";
            }
            if (act != "list" && act != "new" && act != "select" && act != "close")
            {
                return "ERR|BROWSER_ARGS|action 非法（list / new / select / close）: " + act;
            }
            if ((act == "select" || act == "close") && target.Length == 0)
            {
                return "ERR|BROWSER_ARGS|" + act + " 需要参数 target（页签 id——先用 action=list 取）";
            }
            string error;
            CatBrowser browser = Ensure(catId, out error);
            if (browser == null)
            {
                return error;
            }
            lock (browser.Sync)
            {
                try
                {
                    if (act == "list")
                    {
                        return RenderTabList(browser);
                    }
                    if (act == "new")
                    {
                        if (url.Length > 0 && !url.StartsWith("http://", StringComparison.OrdinalIgnoreCase) && !url.StartsWith("https://", StringComparison.OrdinalIgnoreCase))
                        {
                            return "ERR|BROWSER_ARGS|url 仅支持 http(s): " + url;
                        }
                        // 建空白页签——内核忽略 /json/new 的 url 参数（实测确认），故导航改走 CDP（与 open 同源）
                        string endpoint = "http://127.0.0.1:" + browser.Port.ToString() + "/json/new";
                        string created = HttpCall(endpoint, true);
                        string newId = ExtractJsonString(created, "id");
                        if (newId.Length == 0)
                        {
                            return "ERR|BROWSER_CDP|新建页签失败（返回体无 id）: " + Preview(created, 200);
                        }
                        AttachTarget(browser, newId);
                        string navNote = " | url=（空白）";
                        if (url.Length > 0)
                        {
                            Stopwatch navSw = Stopwatch.StartNew();
                            browser.Cdp.Call("Page.navigate", JsonUtil.Serialize(new { url = url }));
                            string navState = WaitReady(browser.Cdp, NavTimeoutMs);
                            if (navState != "interactive" && navState != "complete")
                            {
                                return "ERR|BROWSER_TIMEOUT|新页签未在 " + NavTimeoutMs + "ms 内进入可读状态（readyState=" + navState + "）: " + url;
                            }
                            Thread.Sleep(SettleMs);
                            navSw.Stop();
                            navNote = " | navigated=" + url + " | ms=" + navSw.ElapsedMilliseconds;
                        }
                        LogStore.Add("CatHome4", 1, "browser.tabs new " + catId + " " + newId, "BROWSER");
                        return "ok | 新页签 " + newId + navNote + "\n" + RenderTabList(browser);
                    }
                    if (act == "select")
                    {
                        string endpoint = "http://127.0.0.1:" + browser.Port.ToString() + "/json/activate/" + target;
                        string resp = HttpCall(endpoint, false);
                        AttachTarget(browser, target);
                        LogStore.Add("CatHome4", 1, "browser.tabs select " + catId + " " + target, "BROWSER");
                        return "ok | 已切换到 " + target + " | resp=" + Preview(resp, 80) + "\n" + RenderTabList(browser);
                    }
                    bool closingCurrent = target == browser.TargetId;
                    string closeEndpoint = "http://127.0.0.1:" + browser.Port.ToString() + "/json/close/" + target;
                    string closeResp = HttpCall(closeEndpoint, false);
                    if (closingCurrent)
                    {
                        List<PageTarget> remaining = ListTargets(browser.Port);
                        if (remaining.Count == 0)
                        {
                            browser.TargetId = "";
                            return "ok | 已关闭 " + target + "（无剩余页签——下次 open 会自动新建）";
                        }
                        AttachTarget(browser, remaining[0].Id);
                    }
                    LogStore.Add("CatHome4", 1, "browser.tabs close " + catId + " " + target, "BROWSER");
                    return "ok | 已关闭 " + target + " | resp=" + Preview(closeResp, 80) + "\n" + RenderTabList(browser);
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 3, "browser.tabs 失败: " + ex.Message, "BROWSER");
                    return "ERR|BROWSER_CDP|" + ex.GetType().Name + ": " + ex.Message;
                }
            }
        }

        /// <summary>取本猫实例——三级取用：内存实例存活复用 → 接管 profile 内活实例（A122）→ 懒启动（失败返回 null 并给错误文本）</summary>
        /// <param name="catId">猫 key</param>
        /// <param name="error">失败时的 ERR| 文本</param>
        /// <returns>可用实例或 null</returns>
        private CatBrowser Ensure(string catId, out string error)
        {
            error = "";
            if (catId.Length == 0)
            {
                error = "ERR|BROWSER_ARGS|缺少 catId";
                return null;
            }
            lock (_gate)
            {
                CatBrowser existing;
                if (_cats.TryGetValue(catId, out existing))
                {
                    if (existing.IsAlive() && !existing.IsHeadful)
                    {
                        return existing;
                    }
                    // 残留实例 / 有头实例（域开锚语义 = 默认无头）——清理后重建
                    Cleanup(existing);
                    _cats.Remove(catId);
                }
                // 接管优先（A122）——宿主重启后 _cats 为空，但 profile 里可能有活实例
                // （Chromium detach：启动器进程退出、真正的 browser 进程存活并持有调试端口）。
                // 端口文件 + CDP 探测是唯一稳定锚：接管成功即零启动开销，同时避开
                // 「起新进程 → 被转交旧实例后退出 → DevToolsActivePort 永不出现」的启动失败
                CatBrowser adopted = TryAdopt(catId);
                if (adopted != null)
                {
                    _cats[catId] = adopted;
                    return adopted;
                }
                CatBrowser launched = Launch(catId, false, out error);
                if (launched == null)
                {
                    return null;
                }
                return launched;
            }
        }
        /// <summary>
        /// 有头登录工具（A1）——browser-headful 执行面：域外专属（主干人工登录用）。
        /// open：关现有实例 → 用同 profile 起有头浏览器（窗口弹出供人工登录）→ 可选导航 url。
        /// close：优雅关闭（CDP Browser.close——profile 落盘，登录态保留）。
        /// 同 profile 一次只能一个浏览器进程，故 open 必先关旧实例（含无头——域开锚预热的那个）。
        /// </summary>
        /// <param name="catId">猫 key</param>
        /// <param name="action">动作：open（缺省，起有头）/ close（关有头）</param>
        /// <param name="url">open 时可选导航地址（http/https；空=停在空白页）</param>
        /// <returns>状态摘要或 ERR| 错误文本</returns>
        public string Headful(string catId, string action, string url)
        {
            if (catId.Length == 0)
            {
                return "ERR|BROWSER_ARGS|缺少 catId";
            }
            if (action.Length > 0 && action != "open" && action != "close")
            {
                return "ERR|BROWSER_ARGS|action 非法（open / close）: " + action;
            }
            if (action == "close")
            {
                CloseCat(catId);
                LogStore.Add("CatHome4", 1, "browser.headful 关闭 " + catId, "BROWSER");
                return "有头浏览器已关闭（profile 已落盘——登录态保留）";
            }
            if (url.Length > 0)
            {
                if (!url.StartsWith("http://", StringComparison.OrdinalIgnoreCase) && !url.StartsWith("https://", StringComparison.OrdinalIgnoreCase))
                {
                    return "ERR|BROWSER_ARGS|url 仅支持 http(s): " + url;
                }
            }
            CloseCat(catId);
            string error = "";
            CatBrowser browser = Launch(catId, true, out error);
            if (browser == null)
            {
                return error;
            }
            string nav = "";
            if (url.Length > 0)
            {
                lock (browser.Sync)
                {
                    try
                    {
                        browser.Cdp.Call("Page.navigate", JsonUtil.Serialize(new { url = url }));
                        string state = WaitReady(browser.Cdp, NavTimeoutMs);
                        nav = " | url=" + url + " | ready=" + state;
                    }
                    catch (Exception ex)
                    {
                        nav = " | 导航失败: " + ex.GetType().Name + ": " + ex.Message;
                    }
                }
            }
            LogStore.Add("CatHome4", 1, "browser.headful 就绪 " + catId + " port=" + browser.Port.ToString(), "BROWSER");
            return "有头浏览器已就绪（窗口已弹出——请登录；完成后调本工具 action=close 关闭）| port=" + browser.Port.ToString() + nav;
        }
        /// <summary>
        /// 读端口锚文件（首行端口号）——文件缺失 / 首行非正整数 / 读取异常一律返回 0（调用方按无锚处理）
        /// </summary>
        /// <param name="file">锚文件路径</param>
        /// <returns>端口号；0=无有效锚</returns>
        private static int ReadAnchorPort(string file)
        {
            try
            {
                if (!File.Exists(file))
                {
                    return 0;
                }
                string[] lines = File.ReadAllLines(file);
                if (lines.Length == 0)
                {
                    return 0;
                }
                int port = 0;
                if (!int.TryParse(lines[0].Trim(), out port) || port <= 0)
                {
                    return 0;
                }
                return port;
            }
            catch (IOException)
            {
                return 0;
            }
        }
        /// <summary>
        /// 接管已存在的浏览器实例（A122）——读 profile 里的 DevToolsActivePort，端口可连则复用（不启新进程）。
        /// 动机：Chromium detach——启动器进程写完端口文件后即退出，真正的 browser 进程存活并持有 profile 与调试端口；
        /// 宿主重启 / 进程句柄失效后，起新进程会被「转交旧实例」并立即退出，DevToolsActivePort 永不出现（A110 判例根治）。
        /// </summary>
        /// <param name="catId">猫 key</param>
        /// <returns>接管成功的实例（Edge 为空——无进程句柄）；无活实例返回 null</returns>
        private CatBrowser TryAdopt(string catId)
        {
            string dir = Path.Combine(_dataRoot, "Data", "browser", catId);
            string profileDir = Path.Combine(dir, "profile");
            // 锚双源（A122）——自维护 port.txt（启动成功时落盘、从不删除）优先；
            // Chromium 的 DevToolsActivePort 兜底（它会被下次启动删除，失败路径后可能已缺失）
            int port = ReadAnchorPort(Path.Combine(dir, "port.txt"));
            if (port == 0)
            {
                port = ReadAnchorPort(Path.Combine(profileDir, "DevToolsActivePort"));
            }
            if (port == 0)
            {
                return null;
            }
            try
            {
                // 端口探测——只认 CDP 有响应者（陈旧锚在此被拒，随后由 Launch 重建）
                string probe = HttpCall("http://127.0.0.1:" + port.ToString() + "/json/version", false);
                if (probe.Length == 0)
                {
                    return null;
                }
                CatBrowser adopted = new CatBrowser();
                adopted.CatId = catId;
                adopted.Port = port;
                adopted.ProfileDir = profileDir;
                adopted.OutDir = Path.Combine(dir, "out");
                AttachTarget(adopted, "");
                LogStore.Add("CatHome4", 1, "接管已有浏览器实例: " + catId + " port=" + port.ToString(), "BROWSER");
                return adopted;
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 1, "接管探测失败（按无实例处理）: " + ex.Message, "BROWSER");
                return null;
            }
        }

        /// <summary>
        /// 启动本猫浏览器实例——起进程 → 等端口 → 连 CDP → 建会话
        /// </summary>
        /// <param name="catId">猫 key</param>
        /// <param name="error">失败时的 ERR| 文本</param>
        /// <returns>新实例或 null</returns>
        private CatBrowser Launch(string catId, bool headful, out string error)
        {
            error = "";
            string kernelPath = ResolveKernelPath(out error);
            if (kernelPath.Length == 0)
            {
                return null;
            }
            string dir = Path.Combine(_dataRoot, "Data", "browser", catId);
            string profileDir = Path.Combine(dir, "profile");
            string outDir = Path.Combine(dir, "out");
            try
            {
                Directory.CreateDirectory(profileDir);
                Directory.CreateDirectory(outDir);
            }
            catch (Exception ex)
            {
                error = "ERR|BROWSER_LAUNCH|目录创建失败: " + ex.Message;
                return null;
            }
            string portFile = Path.Combine(profileDir, "DevToolsActivePort");
            string pidFile = Path.Combine(profileDir, "kernel.pid");
            try
            {
                if (File.Exists(portFile))
                {
                    File.Delete(portFile);
                }
            }
            catch (IOException ex)
            {
                LogStore.Add("CatHome4", 2, "旧端口文件删除失败（继续）: " + ex.Message, "BROWSER");
            }
            // 启动前自愈——清掉上次宿主遗留、可能仍占用 profile 的实例（Chromium detach：宿主退出不带走子进程）
            SelfHeal(pidFile);

            // 有头模式（A1）——由调用方指定（browser-headful 传 true；域开锚一律 false）。
            // 不读配置：域开锚默认无头，有头仅在主干显式调 browser-headful 时出现
            string mode = "headless";
            if (headful)
            {
                mode = "headful";
            }

            ProcessStartInfo psi = new ProcessStartInfo(kernelPath);
            psi.UseShellExecute = false;
            psi.CreateNoWindow = true;
            psi.RedirectStandardOutput = true;
            psi.RedirectStandardError = true;
            if (!headful)
            {
                psi.ArgumentList.Add("--headless=new");
            }
            psi.ArgumentList.Add("--remote-debugging-port=0");
            psi.ArgumentList.Add("--user-data-dir=" + profileDir);
            psi.ArgumentList.Add("--no-first-run");
            psi.ArgumentList.Add("--no-default-browser-check");
            psi.ArgumentList.Add("--disable-extensions");
            // 同步切断（A1 附）——Edge 同步会把 session.restore_on_startup 从 5 覆盖回 1（实测 settings_diagnostic 记
            // source=sync），导致启动恢复上次会话、页签累积。本 profile 是每猫独立自动化 profile，同步本就不该有
            psi.ArgumentList.Add("--disable-sync");
            psi.ArgumentList.Add("--window-size=" + ViewportWidth.ToString() + "," + ViewportHeight.ToString());
            if (!headful)
            {
                // 无头才藏滚动条——有头是给人看的窗口，滚动条要在
                psi.ArgumentList.Add("--hide-scrollbars");
            }
            psi.ArgumentList.Add("about:blank");

            Process proc = Process.Start(psi);
            if (proc == null)
            {
                error = "ERR|BROWSER_LAUNCH|浏览器内核进程启动失败";
                return null;
            }
            // 落盘 PID——供下次启动前自愈（宿主被重启时子进程 detach 存活，需要下次能认出来）
            try
            {
                File.WriteAllText(pidFile, proc.Id.ToString());
            }
            catch (IOException ex)
            {
                LogStore.Add("CatHome4", 2, "PID 落盘失败（自愈能力降级）: " + ex.Message, "BROWSER");
            }
            int port = WaitPortFile(portFile, BootTimeoutMs);
            if (port == 0)
            {
                KillTree(proc);
                error = "ERR|BROWSER_LAUNCH|浏览器内核未在 " + BootTimeoutMs + "ms 内就绪（DevToolsActivePort 未出现）";
                return null;
            }
            CatBrowser browser = new CatBrowser();
            browser.CatId = catId;
            browser.Edge = proc;
            browser.Port = port;
            browser.ProfileDir = profileDir;
            browser.OutDir = outDir;
            browser.IsHeadful = headful;
            try
            {
                // 连接首个 page target——多页签下"当前页签"由 TargetId 标识（page 操作面都作用于它）
                AttachTarget(browser, "");
            }
            catch (Exception ex)
            {
                KillTree(proc);
                error = "ERR|BROWSER_CONNECT|连接首个页签失败: " + ex.GetType().Name + ": " + ex.Message;
                return null;
            }
            _cats[catId] = browser;
            // 持久端口锚（A122）——DevToolsActivePort 会被下次启动删除，一旦启动失败就永久丢锚；
            // 本文件只在启动成功时覆写、从不删除：宿主重启 / 启动失败后据此接管存活实例
            try
            {
                File.WriteAllText(Path.Combine(dir, "port.txt"), port.ToString());
            }
            catch (IOException ex)
            {
                LogStore.Add("CatHome4", 2, "端口锚落盘失败（接管能力降级）: " + ex.Message, "BROWSER");
            }
            LogStore.Add("CatHome4", 1, "浏览器实例就绪: " + catId + " port=" + port.ToString() + " pid=" + proc.Id.ToString() + " target=" + browser.TargetId + " mode=" + mode, "BROWSER");
            return browser;
        }
        /// <summary>关闭浏览器进程（A123）——发 Browser.close 令浏览器自行退出（连带全部子进程），并等端口失效（进程真正退出）再返回（A1 起新实例前置——不等会撞上未退出的旧实例占用 profile）。为什么不靠进程句柄：`Process.Start` 拿到的是 Chromium 启动器进程，它写完 DevToolsActivePort 即退出；真正持有 profile 的 browser 进程既不在这条句柄链上、也不在任何 pid 文件里——只有 CDP 端点认得它。</summary>
        /// <param name="port">CDP 端口（≤0 视为无实例，直接返回）</param>
        private static void CloseBrowserProcess(int port)
        {
            if (port <= 0)
            {
                return;
            }
            SendBrowserClose(port);
            WaitPortClosed(port, 5000);
        }
        /// <summary>
        /// 发 Browser.close（A123）——连 browser 级 CDP 端点令浏览器自行退出（连带全部子进程）。
        /// 为什么不靠进程句柄：`Process.Start` 拿到的是 Chromium 启动器进程，它写完 DevToolsActivePort 即退出；
        /// 真正持有 profile 的 browser 进程既不在这条句柄链上、也不在任何 pid 文件里——只有 CDP 端点认得它。
        /// </summary>
        /// <param name="port">CDP 端口（≤0 视为无实例，直接返回）</param>
        private static void SendBrowserClose(int port)
        {
            try
            {
                string version = HttpCall("http://127.0.0.1:" + port.ToString() + "/json/version", false);
                string wsUrl = ExtractJsonString(version, "webSocketDebuggerUrl");
                if (wsUrl.Length == 0)
                {
                    return;
                }
                ClientWebSocket ws = new ClientWebSocket();
                ws.ConnectAsync(new Uri(wsUrl), CancellationToken.None).GetAwaiter().GetResult();
                CdpClient cdp = new CdpClient(ws);
                try
                {
                    cdp.Call("Browser.close", "{}");
                }
                catch (Exception ex)
                {
                    // Browser.close 一生效，浏览器立即退出、连接随之断开——握手未完成属预期，不是失败
                    LogStore.Add("CatHome4", 1, "Browser.close 已发出（连接随实例退出关闭，预期）: " + ex.Message, "BROWSER");
                    ws.Dispose();
                    return;
                }
                ws.Dispose();
                LogStore.Add("CatHome4", 1, "浏览器进程已关闭（CDP Browser.close）: port=" + port.ToString(), "BROWSER");
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 1, "Browser.close 未能发出（实例可能已退出）: " + ex.Message, "BROWSER");
            }
        }
        /// <summary>
        /// CDP 端口存活探测（静默）——不写日志（轮询等待期大量失败属预期，记日志会刷屏）。
        /// </summary>
        /// <param name="port">CDP 端口</param>
        /// <returns>true=端点有响应</returns>
        private static bool PortAlive(int port)
        {
            try
            {
                using (HttpClient http = new HttpClient())
                {
                    http.Timeout = TimeSpan.FromSeconds(2);
                    string probe = http.GetStringAsync("http://127.0.0.1:" + port.ToString() + "/json/version").GetAwaiter().GetResult();
                    return probe.Length > 0;
                }
            }
            catch (Exception)
            {
                return false;
            }
        }
        /// <summary>
        /// 等旧实例端口失效（A1）——Browser.close 异步生效：不等待就起新进程会撞上未退出的旧实例
        /// （同 profile 转交 → 新进程被转交后退出 → DevToolsActivePort 永不出现 → 启动超时，与 A122 同源）。
        /// </summary>
        /// <param name="port">CDP 端口（≤0 直接返回）</param>
        /// <param name="timeoutMs">等待上限毫秒</param>
        private static void WaitPortClosed(int port, long timeoutMs)
        {
            if (port <= 0)
            {
                return;
            }
            Stopwatch sw = Stopwatch.StartNew();
            while (sw.ElapsedMilliseconds < timeoutMs)
            {
                if (!PortAlive(port))
                {
                    return;
                }
                Thread.Sleep(100);
            }
            LogStore.Add("CatHome4", 2, "等待旧实例端口关闭超时: port=" + port.ToString(), "BROWSER");
        }
        /// <summary>
        /// 清理端口锚文件——域回收 / 实例关闭后调用：锚只在实例存活期间有意义，
        /// 留着会让下次启动读到陈旧端口（探测虽会拒绝，但白付一次探测）。
        /// </summary>
        /// <param name="file">锚文件路径</param>
        private static void ClearPortAnchor(string file)
        {
            try
            {
                if (File.Exists(file))
                {
                    File.Delete(file);
                }
            }
            catch (IOException ex)
            {
                LogStore.Add("CatHome4", 2, "端口锚清理失败: " + ex.Message, "BROWSER");
            }
        }

        /// <summary>
        /// 清理实例——关连接 + 杀进程树（失败静默，进程可能已退出）
        /// </summary>
        /// <param name="browser">目标实例</param>
        private static void Cleanup(CatBrowser browser)
        {
            // [段1] 关浏览器进程（A123）——CDP Browser.close 是唯一可靠手段：
            // 启动器进程句柄早已退出，KillTree 打空；只有 CDP 端点认得真正持有 profile 的那个进程
            CloseBrowserProcess(browser.Port);
            // [段2] 释放 CDP 连接
            if (browser.Ws != null)
            {
                try
                {
                    browser.Ws.Dispose();
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 1, "WS 释放异常（忽略）: " + ex.Message, "BROWSER");
                }
            }
            // [段3] 进程句柄兜底（句柄仍有效且进程活着时才需要）——正常路径已在段1 关掉
            if (browser.Edge != null)
            {
                try
                {
                    if (!browser.Edge.HasExited)
                    {
                        KillTree(browser.Edge);
                    }
                }
                catch (InvalidOperationException ex)
                {
                    LogStore.Add("CatHome4", 1, "实例进程已退出（清理跳过）: " + ex.Message, "BROWSER");
                }
            }
        }

        /// <summary>
        /// 解析浏览器内核可执行路径——读配置 browser.kernel_path（Chromium 系：msedge.exe / chrome.exe 等）
        /// 与 PsService.ResolvePwshPath 同构：外部可执行文件依赖位置可配置，未配置 / 路径不存在一律明示（不静默回落探测）。
        /// 换内核 = 换本配置值——Chromium 系协议同为 CDP，无分支。
        /// </summary>
        /// <param name="error">错误文本（空=解析成功）</param>
        /// <returns>内核可执行文件全路径（解析失败返回空串）</returns>
        private static string ResolveKernelPath(out string error)
        {
            error = "";
            ConfigStore cfg;
            DataBox.TryResolve<ConfigStore>(out cfg);
            string path = "";
            if (cfg != null)
            {
                path = cfg.Get(KernelPathKey, "");
            }
            if (path.Length == 0)
            {
                error = "ERR|BROWSER_KERNEL_MISSING|浏览器内核未配置——请在配置面填写 " + KernelPathKey + "（Chromium 系可执行文件全路径：msedge.exe / chrome.exe 等）";
                return "";
            }
            if (!File.Exists(path))
            {
                error = "ERR|BROWSER_KERNEL_NOT_FOUND|配置的浏览器内核路径不存在：" + path + "——请更新 " + KernelPathKey;
                return "";
            }
            return path;
        }

        /// <summary>
        /// 渲染页签清单——`* ` 标记当前页签
        /// </summary>
        /// <param name="browser">实例</param>
        /// <returns>清单文本（count + 每行 id | title | url）</returns>
        private static string RenderTabList(CatBrowser browser)
        {
            List<PageTarget> pages = ListTargets(browser.Port);
            StringBuilder sb = new StringBuilder();
            sb.Append("count=").Append(pages.Count.ToString()).Append("（* = 当前页签）\n");
            int i = 0;
            while (i < pages.Count)
            {
                PageTarget p = pages[i];
                string mark = " ";
                if (p.Id == browser.TargetId)
                {
                    mark = "*";
                }
                sb.Append(mark).Append(' ').Append(p.Id).Append(" | ").Append(p.Title).Append(" | ").Append(p.Url).Append('\n');
                i = i + 1;
            }
            return sb.ToString();
        }

        /// <summary>
        /// 列全部 page 类 target——DevTools HTTP 端点 /json/list
        /// </summary>
        /// <param name="port">CDP 端口</param>
        /// <returns>页签清单（解析失败为空表）</returns>
        private static List<PageTarget> ListTargets(int port)
        {
            List<PageTarget> list = new List<PageTarget>();
            string endpoint = "http://127.0.0.1:" + port.ToString() + "/json/list";
            string json = HttpCall(endpoint, false);
            if (json.Length == 0)
            {
                return list;
            }
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(json))
                {
                    foreach (JsonElement item in doc.RootElement.EnumerateArray())
                    {
                        JsonElement typeEl;
                        if (!item.TryGetProperty("type", out typeEl) || typeEl.GetString() != "page")
                        {
                            continue;
                        }
                        PageTarget p = new PageTarget();
                        p.Id = JsonString(item, "id");
                        p.Title = JsonString(item, "title");
                        p.Url = JsonString(item, "url");
                        p.WsUrl = JsonString(item, "webSocketDebuggerUrl");
                        list.Add(p);
                    }
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "页签清单解析失败: " + ex.Message, "BROWSER");
            }
            return list;
        }

        /// <summary>
        /// 解析目标页签——targetId 为空则取第一个
        /// </summary>
        /// <param name="port">CDP 端口</param>
        /// <param name="targetId">页签 id（空=第一个）</param>
        /// <returns>目标页签（未找到返回 null）</returns>
        private static PageTarget ResolveTarget(int port, string targetId)
        {
            List<PageTarget> pages = ListTargets(port);
            if (targetId.Length == 0)
            {
                if (pages.Count == 0)
                {
                    return null;
                }
                return pages[0];
            }
            int i = 0;
            while (i < pages.Count)
            {
                if (pages[i].Id == targetId)
                {
                    return pages[i];
                }
                i = i + 1;
            }
            return null;
        }

        /// <summary>
        /// 把当前 CDP 连接挂到指定页签——关旧 WS、连新 target、重做 enable 与视口覆盖。
        /// 多页签的核心动作：操作面（Read / Eval / Shot）始终作用于"当前页签"。
        /// </summary>
        /// <param name="browser">实例</param>
        /// <param name="targetId">页签 id（空=第一个）</param>
        private static void AttachTarget(CatBrowser browser, string targetId)
        {
            PageTarget target = ResolveTarget(browser.Port, targetId);
            if (target == null)
            {
                throw new InvalidOperationException("未找到页签: " + targetId);
            }
            if (browser.Ws != null)
            {
                try
                {
                    browser.Ws.Dispose();
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 1, "旧 WS 释放异常（忽略）: " + ex.Message, "BROWSER");
                }
                browser.Ws = null;
                browser.Cdp = null;
            }
            ClientWebSocket ws = new ClientWebSocket();
            ws.ConnectAsync(new Uri(target.WsUrl), CancellationToken.None).GetAwaiter().GetResult();
            CdpClient cdp = new CdpClient(ws);
            cdp.Call("Page.enable", "{}");
            cdp.Call("Runtime.enable", "{}");
            cdp.Call("Emulation.setDeviceMetricsOverride", JsonUtil.Serialize(new { width = ViewportWidth, height = ViewportHeight, deviceScaleFactor = 1, mobile = false }));
            browser.Ws = ws;
            browser.Cdp = cdp;
            browser.TargetId = target.Id;
        }

        /// <summary>
        /// DevTools HTTP 端点调用——GET / PUT（页签新建需 PUT）
        /// </summary>
        /// <param name="url">端点地址</param>
        /// <param name="put">true=PUT；false=GET</param>
        /// <returns>响应文本（失败为空串——调用方按返回体判空）</returns>
        private static string HttpCall(string url, bool put)
        {
            try
            {
                using (HttpClient http = new HttpClient())
                {
                    http.Timeout = TimeSpan.FromSeconds(15);
                    if (put)
                    {
                        HttpRequestMessage req = new HttpRequestMessage(HttpMethod.Put, url);
                        HttpResponseMessage resp = http.SendAsync(req).GetAwaiter().GetResult();
                        return resp.Content.ReadAsStringAsync().GetAwaiter().GetResult();
                    }
                    return http.GetStringAsync(url).GetAwaiter().GetResult();
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "DevTools HTTP 端点调用失败: " + ex.Message, "BROWSER");
                return "";
            }
        }

        /// <summary>
        /// 从 JSON 文本取顶层字符串字段（解析失败 / 缺字段返回空串）
        /// </summary>
        /// <param name="json">JSON 文本</param>
        /// <param name="key">字段名</param>
        /// <returns>字段值</returns>
        private static string ExtractJsonString(string json, string key)
        {
            if (json.Length == 0)
            {
                return "";
            }
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(json))
                {
                    return JsonString(doc.RootElement, key);
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "JSON 解析失败: " + ex.Message, "BROWSER");
            }
            return "";
        }

        /// <summary>
        /// 取 JSON 对象字段字符串值
        /// </summary>
        /// <param name="obj">对象</param>
        /// <param name="key">字段名</param>
        /// <returns>字段值（非字符串 / 缺失为空串）</returns>
        private static string JsonString(JsonElement obj, string key)
        {
            JsonElement el;
            if (obj.TryGetProperty(key, out el) && el.ValueKind == JsonValueKind.String)
            {
                return el.GetString() ?? "";
            }
            return "";
        }

        /// <summary>
        /// 启动前自愈——读 edge.pid，若对应进程仍在且是 msedge，则杀其进程树。
        /// 动机：Chromium detach——宿主退出不带走子进程；孤儿实例持续持有 profile，
        /// 会让新实例把命令行转交旧实例后退出、DevToolsActivePort 永不出现（A110 实测缺陷）。
        /// </summary>
        /// <param name="pidFile">PID 文件路径</param>
        private static void SelfHeal(string pidFile)
        {
            try
            {
                if (!File.Exists(pidFile))
                {
                    return;
                }
                string raw = File.ReadAllText(pidFile).Trim();
                int pid = 0;
                if (!int.TryParse(raw, out pid) || pid <= 0)
                {
                    return;
                }
                Process stale = Process.GetProcessById(pid);
                if (stale.ProcessName == "msedge" || stale.ProcessName == "chrome")
                {
                    LogStore.Add("CatHome4", 2, "自愈：清理残留浏览器内核实例 pid=" + pid.ToString(), "BROWSER");
                    KillTree(stale);
                }
            }
            catch (ArgumentException ex)
            {
                LogStore.Add("CatHome4", 1, "自愈：PID 无对应进程（正常——上次已正常退出）: " + ex.Message, "BROWSER");
            }
            catch (InvalidOperationException ex)
            {
                LogStore.Add("CatHome4", 1, "自愈：进程已退出: " + ex.Message, "BROWSER");
            }
            catch (IOException ex)
            {
                LogStore.Add("CatHome4", 2, "自愈：PID 文件读取失败: " + ex.Message, "BROWSER");
            }
        }

        /// <summary>
        /// 等 DevToolsActivePort 出现并解析端口——首行为端口号
        /// </summary>
        /// <param name="portFile">端口文件路径</param>
        /// <param name="timeoutMs">等待上限毫秒</param>
        /// <returns>端口号（超时为 0）</returns>
        private static int WaitPortFile(string portFile, long timeoutMs)
        {
            Stopwatch sw = Stopwatch.StartNew();
            while (sw.ElapsedMilliseconds < timeoutMs)
            {
                try
                {
                    if (File.Exists(portFile))
                    {
                        string[] lines = File.ReadAllLines(portFile);
                        if (lines.Length > 0)
                        {
                            int port = 0;
                            if (int.TryParse(lines[0].Trim(), out port) && port > 0)
                            {
                                return port;
                            }
                        }
                    }
                }
                catch (IOException ex)
                {
                    LogStore.Add("CatHome4", 1, "端口文件读取竞争（重试）: " + ex.Message, "BROWSER");
                }
                Thread.Sleep(50);
            }
            return 0;
        }

        /// <summary>
        /// 等页面进入可读状态——interactive（DOM 就绪）或 complete（含全部子资源）
        /// 🔴 判据取 interactive：complete 要等全部子资源（图片 / 字体 / 分析脚本）落地，外网站首访可达数十秒，
        /// 而 DOM 在此前早已就绪——读正文 / 结构 / 截图都不需要等子资源（A110 运行态实测：首访 34.7s → 同域二次 1.9s）。
        /// </summary>
        /// <param name="cdp">CDP 会话</param>
        /// <param name="timeoutMs">等待上限毫秒</param>
        /// <returns>最后一次读到的 readyState</returns>
        private static string WaitReady(CdpClient cdp, long timeoutMs)
        {
            Stopwatch sw = Stopwatch.StartNew();
            string state = "";
            while (sw.ElapsedMilliseconds < timeoutMs)
            {
                long remaining = timeoutMs - sw.ElapsedMilliseconds;
                int slice = EvalSliceMs;
                if (remaining < slice)
                {
                    slice = (int)remaining;
                }
                if (slice < 200)
                {
                    slice = 200;
                }
                try
                {
                    state = EvalRaw(cdp, "document.readyState", slice);
                }
                catch (TimeoutException)
                {
                    // 单次 eval 无响应——按「打不开」处置（A295）：预算到点即判，不把 20 秒拖成无限等待
                    return "timeout";
                }
                if (state == "interactive" || state == "complete")
                {
                    return state;
                }
                Thread.Sleep(50);
            }
            return state;
        }

        /// <summary>
        /// 执行 JS 表达式并取回值——非字符串按 JSON 文本返回
        /// </summary>
        /// <param name="cdp">CDP 会话</param>
        /// <param name="expression">JS 表达式</param>
        /// <param name="timeoutMs">单次调用上限毫秒（A295——无上限时 WebSocket 可永久挂住）</param>
        /// <returns>取值结果（失败为空串）</returns>
        private static string EvalRaw(CdpClient cdp, string expression, int timeoutMs)
        {
            JsonElement r = cdp.Call("Runtime.evaluate", JsonUtil.Serialize(new { expression = expression, returnByValue = true }), timeoutMs);
            JsonElement res;
            if (!r.TryGetProperty("result", out res))
            {
                return "";
            }
            JsonElement inner;
            if (!res.TryGetProperty("result", out inner))
            {
                return "";
            }
            JsonElement v;
            if (!inner.TryGetProperty("value", out v))
            {
                return "";
            }
            if (v.ValueKind == JsonValueKind.String)
            {
                return v.GetString() ?? "";
            }
            return v.GetRawText();
        }
        /// <summary>
        /// 执行 JS 表达式并取回值——默认单次调用上限（CallTimeoutMs）
        /// </summary>
        /// <param name="cdp">CDP 会话</param>
        /// <param name="expression">JS 表达式</param>
        /// <returns>取值结果（失败为空串）</returns>
        private static string EvalRaw(CdpClient cdp, string expression)
        {
            return EvalRaw(cdp, expression, CallTimeoutMs);
        }

        /// <summary>
        /// 无障碍树精简——只留 role: name（剔除 ignored 与空名节点）
        /// </summary>
        /// <param name="axResponse">getFullAXTree 原始响应</param>
        /// <returns>精简文本（每行一个节点）</returns>
        private static string BuildAxSlim(JsonElement axResponse)
        {
            StringBuilder sb = new StringBuilder();
            JsonElement result;
            if (!axResponse.TryGetProperty("result", out result))
            {
                return "";
            }
            JsonElement nodes;
            if (!result.TryGetProperty("nodes", out nodes) || nodes.ValueKind != JsonValueKind.Array)
            {
                return "";
            }
            foreach (JsonElement node in nodes.EnumerateArray())
            {
                JsonElement ignoredEl;
                if (node.TryGetProperty("ignored", out ignoredEl) && ignoredEl.ValueKind == JsonValueKind.True)
                {
                    continue;
                }
                string role = AxText(node, "role");
                string name = AxText(node, "name");
                if (name.Length == 0)
                {
                    continue;
                }
                sb.Append(role).Append(": ").Append(name).Append('\n');
            }
            return sb.ToString();
        }

        /// <summary>
        /// 取 AX 节点字段文本——{type, value} 结构取 value
        /// </summary>
        /// <param name="node">AX 节点</param>
        /// <param name="prop">字段名</param>
        /// <returns>字段文本（缺失为空串）</returns>
        private static string AxText(JsonElement node, string prop)
        {
            JsonElement field;
            if (!node.TryGetProperty(prop, out field))
            {
                return "";
            }
            if (field.ValueKind == JsonValueKind.Object)
            {
                JsonElement value;
                if (field.TryGetProperty("value", out value))
                {
                    if (value.ValueKind == JsonValueKind.String)
                    {
                        return value.GetString() ?? "";
                    }
                    return value.GetRawText();
                }
            }
            return "";
        }

        /// <summary>
        /// 生成产物落盘路径——out/&lt;时间戳&gt;-&lt;用途&gt;.&lt;扩展名&gt;
        /// </summary>
        /// <param name="browser">实例（取 out 目录）</param>
        /// <param name="purpose">用途标识（read-text / read-ax / read-links / eval / shot）</param>
        /// <param name="ext">扩展名</param>
        /// <returns>绝对路径</returns>
        private static string NextOutPath(CatBrowser browser, string purpose, string ext)
        {
            string stamp = DateTime.Now.ToString("yyyyMMdd_HHmmss_fff");
            return Path.Combine(browser.OutDir, stamp + "-" + purpose + "." + ext);
        }

        /// <summary>
        /// 摘要预览——头部截断（截断时标注总量）
        /// </summary>
        /// <param name="text">原文</param>
        /// <param name="max">上限字符数</param>
        /// <returns>预览文本</returns>
        private static string Preview(string text, int max)
        {
            if (text.Length <= max)
            {
                return text;
            }
            return text.Substring(0, max) + "\n…[截断 共 " + text.Length.ToString() + " 字符]";
        }

        /// <summary>
        /// 链接面预览——解析 JSON 取前 N 条（锚文本 → 地址）
        /// </summary>
        /// <param name="linksJson">链接数组 JSON</param>
        /// <returns>预览文本</returns>
        private static string BuildLinkPreview(string linksJson)
        {
            StringBuilder sb = new StringBuilder();
            using (JsonDocument doc = JsonUtil.ParseStrict(linksJson))
            {
                int total = doc.RootElement.GetArrayLength();
                int i = 0;
                foreach (JsonElement item in doc.RootElement.EnumerateArray())
                {
                    if (i >= LinkPreviewCount)
                    {
                        break;
                    }
                    JsonElement t;
                    JsonElement h;
                    string label = item.TryGetProperty("t", out t) ? (t.GetString() ?? "") : "";
                    string href = item.TryGetProperty("h", out h) ? (h.GetString() ?? "") : "";
                    if (label.Length == 0)
                    {
                        label = "(无锚文本)";
                    }
                    sb.Append("- ").Append(label).Append(" → ").Append(href).Append('\n');
                    i = i + 1;
                }
                if (total > LinkPreviewCount)
                {
                    sb.Append("…[共 ").Append(total.ToString()).Append(" 条，完整清单见落盘文件]");
                }
            }
            return sb.ToString();
        }

        /// <summary>
        /// 读 PNG 尺寸——IHDR 宽高（非 PNG 或读取失败返回 "?"）
        /// </summary>
        /// <param name="path">PNG 路径</param>
        /// <returns>宽x高</returns>
        private static string PngSize(string path)
        {
            try
            {
                byte[] head = new byte[24];
                using (FileStream fs = File.OpenRead(path))
                {
                    int n = fs.Read(head, 0, 24);
                    if (n < 24)
                    {
                        return "?";
                    }
                }
                int w = (head[16] << 24) | (head[17] << 16) | (head[18] << 8) | head[19];
                int h = (head[20] << 24) | (head[21] << 16) | (head[22] << 8) | head[23];
                return w.ToString() + "x" + h.ToString();
            }
            catch (IOException ex)
            {
                LogStore.Add("CatHome4", 2, "PNG 尺寸读取失败: " + ex.Message, "BROWSER");
                return "?";
            }
        }

        /// <summary>
        /// 杀进程树——taskkill /T /F（子进程一并终止；失败静默，进程可能已退出）
        /// </summary>
        /// <param name="proc">目标进程</param>
        private static void KillTree(Process proc)
        {
            try
            {
                ProcessStartInfo psi = new ProcessStartInfo();
                psi.FileName = "taskkill.exe";
                psi.Arguments = "/T /F /PID " + proc.Id.ToString();
                psi.UseShellExecute = false;
                psi.CreateNoWindow = true;
                Process killer = Process.Start(psi);
                if (killer != null)
                {
                    killer.WaitForExit(5000);
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "进程树强杀失败: " + ex.Message, "BROWSER");
            }
            try
            {
                proc.Kill();
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 1, "进程已退出，Kill 跳过: " + ex.Message, "BROWSER");
            }
        }

        /// <summary>
        /// 页签（CDP page target）——DevTools HTTP 端点 /json/list 的条目
        /// </summary>
        private sealed class PageTarget
        {
            /// <summary>target id（CDP 标识——select / close 用它定位）</summary>
            public string Id = "";

            /// <summary>页面标题</summary>
            public string Title = "";

            /// <summary>页面地址</summary>
            public string Url = "";

            /// <summary>CDP WebSocket 地址（连接该页签用）</summary>
            public string WsUrl = "";
        }

        /// <summary>
        /// 单猫浏览器实例——进程 + CDP 连接 + 路径（Sync 保证同猫调用串行）
        /// </summary>
        private sealed class CatBrowser
        {
            /// <summary>同猫调用串行锁——单实例单连接，不开并发</summary>
            public readonly object Sync = new object();

            /// <summary>猫 key</summary>
            public string CatId = "";

            /// <summary>Edge 进程</summary>
            public Process Edge;

            /// <summary>CDP 会话</summary>
            public CdpClient Cdp;

            /// <summary>WebSocket 连接</summary>
            public ClientWebSocket Ws;

            /// <summary>profile 目录（Edge user-data-dir）</summary>
            public string ProfileDir = "";

            /// <summary>产物目录</summary>
            public string OutDir = "";

            /// <summary>CDP 端口（DevToolsActivePort 发现）</summary>
            public int Port;

            /// <summary>有头标记（browser-headful 起的实例）——域开锚 Ensure 见有头实例即关掉重起无头（保「每次开域默认无头」）</summary>
            public bool IsHeadful;

            /// <summary>当前页签 id——操作面（Read / Eval / Shot）作用的 target</summary>
            public string TargetId = "";

            /// <summary>存活判定（A122 修正）——CDP 连接仍开即可复用；不再看进程句柄：Chromium 启动器进程写完端口文件后即退出，按句柄判活会把可用实例误判为失效（进而重启 → 被转交旧实例 → 启动失败）</summary>
            /// <returns>true=可复用</returns>
            public bool IsAlive()
            {
                if (Ws == null)
                {
                    return false;
                }
                return Ws.State == WebSocketState.Open;
            }
        }

        /// <summary>
        /// 极简 CDP 客户端——JSON-RPC over WebSocket（id 匹配；事件消息丢弃）
        /// </summary>
        private sealed class CdpClient
        {
            /// <summary>命令序号</summary>
            private int _id;

            /// <summary>连接</summary>
            private readonly ClientWebSocket _ws;

            /// <summary>
            /// 构造
            /// </summary>
            /// <param name="ws">已连接的 CDP WebSocket</param>
            public CdpClient(ClientWebSocket ws)
            {
                _ws = ws;
            }

            /// <summary>
            /// 发送命令并等待同 id 响应——命令返回 error 时抛异常
            /// </summary>
            /// <param name="method">CDP 方法名</param>
            /// <param name="paramsJson">参数 JSON（无参传 "{}"）</param>
            /// <returns>响应根节点（已 Clone）</returns>
            public JsonElement Call(string method, string paramsJson)
            {
                return Call(method, paramsJson, CallTimeoutMs);
            }

            /// <summary>
            /// 发送命令并等待同 id 响应——超时抛 TimeoutException（A295：收发无上限时 WebSocket 可永久挂住，就绪判据永远轮不到）
            /// </summary>
            /// <param name="method">CDP 方法名</param>
            /// <param name="paramsJson">参数 JSON（无参传 "{}"）</param>
            /// <param name="timeoutMs">单次调用上限毫秒</param>
            /// <returns>响应根节点（已 Clone）</returns>
            public JsonElement Call(string method, string paramsJson, int timeoutMs)
            {
                int id = _id + 1;
                _id = id;
                string msg = JsonUtil.Object(("id", id), ("method", method), ("params", JsonUtil.Raw(paramsJson)));
                byte[] bytes = Encoding.UTF8.GetBytes(msg);
                SendFrame(bytes, timeoutMs);
                Stopwatch sw = Stopwatch.StartNew();
                while (true)
                {
                    long remaining = timeoutMs - sw.ElapsedMilliseconds;
                    if (remaining <= 0)
                    {
                        throw new TimeoutException("CDP " + method + " 超时（" + timeoutMs.ToString() + "ms 无响应）");
                    }
                    string text = ReceiveText((int)remaining);
                    using (JsonDocument doc = JsonUtil.ParseStrict(text))
                    {
                        JsonElement root = doc.RootElement;
                        JsonElement idEl;
                        if (root.TryGetProperty("id", out idEl) && idEl.ValueKind == JsonValueKind.Number && idEl.GetInt32() == id)
                        {
                            JsonElement errEl;
                            if (root.TryGetProperty("error", out errEl))
                            {
                                throw new InvalidOperationException("CDP " + method + " 失败: " + errEl.GetRawText());
                            }
                            return root.Clone();
                        }
                    }
                }
            }

            /// <summary>
            /// 发送一帧并受超时约束（A295——发送侧同样可能挂住）
            /// </summary>
            /// <param name="bytes">负载</param>
            /// <param name="timeoutMs">超时毫秒</param>
            private void SendFrame(byte[] bytes, int timeoutMs)
            {
                using (CancellationTokenSource cts = new CancellationTokenSource(timeoutMs))
                {
                    try
                    {
                        _ws.SendAsync(new ArraySegment<byte>(bytes), WebSocketMessageType.Text, true, cts.Token).GetAwaiter().GetResult();
                    }
                    catch (OperationCanceledException)
                    {
                        throw new TimeoutException("CDP 发送超时（" + timeoutMs.ToString() + "ms）");
                    }
                }
            }

            /// <summary>
            /// 接收一条完整文本消息（分片累积到 EndOfMessage；整体受 timeoutMs 约束）
            /// </summary>
            /// <param name="timeoutMs">超时毫秒</param>
            /// <returns>消息文本</returns>
            private string ReceiveText(int timeoutMs)
            {
                byte[] buf = new byte[65536];
                using (MemoryStream ms = new MemoryStream())
                {
                    using (CancellationTokenSource cts = new CancellationTokenSource(timeoutMs))
                    {
                        try
                        {
                            while (true)
                            {
                                WebSocketReceiveResult r = _ws.ReceiveAsync(new ArraySegment<byte>(buf), cts.Token).GetAwaiter().GetResult();
                                ms.Write(buf, 0, r.Count);
                                if (r.EndOfMessage)
                                {
                                    break;
                                }
                            }
                        }
                        catch (OperationCanceledException)
                        {
                            throw new TimeoutException("CDP 接收超时（" + timeoutMs.ToString() + "ms 内无完整消息）");
                        }
                    }
                    return Encoding.UTF8.GetString(ms.ToArray());
                }
            }
        }
    }
}
