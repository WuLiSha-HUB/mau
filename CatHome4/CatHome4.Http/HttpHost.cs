using System;
using System.Collections.Generic;
using System.Text.Json;
using System.Threading;
using System.Threading.Channels;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.Logging;
using Mau.Runtime;
using CatHome4.Contracts;

namespace CatHome4.Http
{
    /// <summary>
    /// HTTP 外观层宿主——Kestrel + Minimal API（P6 最小闭环）。
    /// 通道：GET /api/v1/snapshot（全量快照）· GET /api/v1/stream（SSE 事件流）· POST /api/v1/command（指令入口）· GET /（静态页）。
    /// 协议唯一权威：design-ch4-protocol.md（CCBP Project/CH4/）。归 CH4 业务区块（边界判据 7：外观层可选/可切换）。
    /// 服务面（Start/BuildApp/Stop/Port/SseClient）在本文件；推送面分部在 HttpHost.Pump.cs；
    /// HTTP 端点面分部在 HttpHost.Api.cs；配置区面分部在 HttpHost.Config.cs（P7b partial 拆分）。
    /// </summary>
    /// <summary>
    /// HTTP 外观层启动参数——Start 具名选项（R6-P3-06：13 位置参数 → 属性赋值，调用点自文档、顺序无关）。
    /// 回调属性为空 = 该面对应端点/推送不注册（与旧签名 null 参数同义）。
    /// </summary>
    public sealed class HttpHostOptions
    {
        /// <summary>绑定端口</summary>
        public int Port { get; set; }

        /// <summary>前端测试服务端口（/api/v1/frontend-test 转发目标——区段值：开发区 8069 / 部署区 8099）</summary>
        public int FrontendTestPort { get; set; }

        /// <summary>会话归属 ID——SSE 帧的 sessionId 归属</summary>
        public string SessionId { get; set; }

        /// <summary>快照 JSON 构建回调</summary>
        public Func<bool, string> SnapshotBuilder { get; set; }

        /// <summary>指令投递回调</summary>
        public Func<string, bool> Dispatcher { get; set; }

        /// <summary>图片包裹组装回调（可空 = 不组装）——(指令行, 图片路径列表) → 套用后的指令行；宿主侧注入（按会话前文条数真值拼编号）</summary>
        public Func<string, IList<string>, string> EnvelopeBuilder { get; set; }

        /// <summary>紧凑帧构建回调（可空=不落帧）</summary>
        public Func<string> FrameBuilder { get; set; }

        /// <summary>多猫列表构建回调（可空=不注册该端点）</summary>
        public Func<string> CatsBuilder { get; set; }

        /// <summary>增量 patch 构建回调（可空=全量推送）</summary>
        public Func<string> PatchBuilder { get; set; }

        /// <summary>视图全量帧构建回调（可空=连接建立不推视图）——v2 契约：连接建立取一次（连接私有首帧）</summary>
        public Func<string> ViewFullBuilder { get; set; }

        /// <summary>视图增量帧构建回调（可空=不推）——v2 契约：帧轮取，无变化返回 null/空（零推送）</summary>
        public Func<string> ViewFrameBuilder { get; set; }

        /// <summary>静态页模式（true=chat.html / false=index.html；StaticPage 非空时以 StaticPage 为准）</summary>
        public bool ServeChatPage { get; set; }

        /// <summary>静态页文件名（可空 = 按 ServeChatPage 旧口径；A301 群聊页 = "group.html"）</summary>
        public string StaticPage { get; set; }

        /// <summary>管理路由注册回调（可空）</summary>
        public Action<IHttpRouteSink> RouteRegistrar { get; set; }

        /// <summary>html 根解析（源码区优先 + 部署区回退）</summary>
        public IHtmlRootProvider HtmlRootProvider { get; set; }

        /// <summary>页面标题展示名（null=不注入）</summary>
        public string DisplayName { get; set; }

        /// <summary>依赖探测手动刷新回调（可空=不注册 /api/v1/deps/refresh）——宿主侧注入 DependencyProbe.Refresh（A253 5b）</summary>
        public Action DepsRefresh { get; set; }

        /// <summary>LLM 端点池探活回调（可空=不注册 /api/v1/llm/probe）——宿主侧注入 LlmEndpointProbe.ProbeAsync（A253 扩展）</summary>
        public Func<Task<string>> LlmProbe { get; set; }

        /// <summary>Console 口令校验实例（可空=不启用认证）——仅主端口注入；每猫对话端口与组端口不注入（A328）</summary>
        public ConsoleAuth ConsoleAuth { get; set; }

        /// <summary>端口面——console（主端口全量）/ chat（对话端口最小集）/ group（组端口最小集）；缺省 console（A328 端口面收缩）</summary>
        public string Surface { get; set; }
    }

    /// <summary>
    /// HTTP 宿主——每猫独立实例（Kestrel 最小 API：快照/SSE/指令/配置/静态资源路由）。
    /// </summary>
    public sealed partial class HttpHost : IHostPush, IHttpRouteSink
    {
        // [段1] 服务字段——应用实例/端口/回调/事件状态
        /// <summary>Kestrel 应用实例</summary>
        private WebApplication _app;

        /// <summary>绑定端口（区段值：开发区 8079 / 部署区 8080——协议 §二）</summary>
        private int _port;

        /// <summary>前端测试服务端口（/api/v1/frontend-test 转发目标——区段值：开发区 8069 / 部署区 8099）</summary>
        private int _frontendTestPort;

        /// <summary>快照 JSON 构建回调——宿主侧注入（Program.BuildSnapshotJson，includeLogs 参数）</summary>
        private Func<bool, string> _snapshotBuilder;

        /// <summary>增量 patch 构建回调——宿主侧注入（Program.BuildPatchJson；可空=不推 patch 保持全量推送）</summary>
        private Func<string> _patchBuilder;

        /// <summary>视图全量帧构建回调——对话端口注入（Session.BuildViewFullJson；可空=连接建立不推视图）</summary>
        private Func<string> _viewFullBuilder;

        /// <summary>视图增量帧构建回调——对话端口注入（Session.TakeViewFrameJson；可空=不推）</summary>
        private Func<string> _viewFrameBuilder;

        /// <summary>指令投递回调——宿主侧注入（Program.DispatchCommand）</summary>
        private Func<string, bool> _dispatcher;

        /// <summary>依赖探测手动刷新回调——宿主侧注入（DependencyProbe.Refresh；可空=不注册刷新端点）</summary>
        private Action _depsRefresh;

        /// <summary>LLM 端点池探活回调——宿主侧注入（LlmEndpointProbe.ProbeAsync；可空=不注册探活端点）</summary>
        private Func<Task<string>> _llmProbe;

        /// <summary>Console 口令校验实例（可空=不启用认证——仅主端口注入，A328）</summary>
        private ConsoleAuth _consoleAuth;

        /// <summary>主端口面——全量端点（含配置写 / 管理指令 / 前端测试）（A328）</summary>
        private bool _surfaceConsole = true;

        /// <summary>对话端口面——只读观测 + 对话输入 + 图片音频（不含配置写与管理指令）（A328）</summary>
        private bool _surfaceChat;

        /// <summary>组端口面——组消息流 + 只读展示（不含配置写 / 对话输入 / SSE）（A328）</summary>
        private bool _surfaceGroup;

        /// <summary>图片包裹组装回调——宿主侧注入（Admin 按会话前文条数拼编号；可空 = 不组装）</summary>
        private Func<string, IList<string>, string> _envelopeBuilder;

        /// <summary>SSE 事件序号——单调递增（协议 §4.3 seq 锚点；多线程并发写走 Interlocked——HTTP/LLM/泵三线程）</summary>
        private int _seq;

        /// <summary>当前 SSE 连接数——连接健康字段数据源（state 段 conn.clients；锁内读取）</summary>
        public int ClientCount
        {
            get
            {
                lock (_clientLock)
                {
                    return _clients.Count;
                }
            }
        }

        /// <summary>SSE 客户端集合——锁保护（多连接独立广播）</summary>
        private readonly List<SseClient> _clients = new List<SseClient>();

        /// <summary>客户端集合锁</summary>
        private readonly object _clientLock = new object();

        /// <summary>LogStore 增量游标——已推送条目数（协议 §4.2 log 事件）</summary>
        private int _logCursor;

        /// <summary>后台推送任务取消源——宿主退出时停</summary>
        private CancellationTokenSource _pumpCts;

        /// <summary>后台推送任务——快照定时 + log 增量（协议 §4.1）</summary>
        private Task _pumpTask;

        /// <summary>停止标志——Stop 后 Push 静默丢弃</summary>
        private bool _stopped;

        /// <summary>快照缓存——主线程 PumpMainThread 构建，HTTP 线程只读（最近一帧主线程快照）</summary>
        private string _snapshotCache = "{}";

        /// <summary>快照待构建标志——PumpLoop 置位，主线程 PumpMainThread 消费</summary>
        private volatile bool _snapshotPending;

        /// <summary>紧凑帧构建回调——frame.txt 帧流（可空=不落帧）</summary>
        private Func<string> _frameBuilder;

        /// <summary>会话归属 ID——SSE 帧 sessionId 字段（P9.3 多实例化：每猫实例绑定自身会话）</summary>
        private string _sessionId;

        /// <summary>多猫列表构建回调——GET /api/v1/cats（可空=不注册该端点——P9.3b 注册表就位后传入）</summary>
        private Func<string> _catsBuilder;

        /// <summary>猫卡 state 缓存——最近一次推送的猫列表 JSON（A225c：SSE state 首帧 + 变化对比；主线程独占）</summary>
        private string _catsStateCache = "";

        /// <summary>静态页模式——true=chat.html 独立对话页 / false=index.html 主面板（P9.3 双页模式）</summary>
        private bool _serveChatPage;

        /// <summary>静态页文件名——非空时优先于 _serveChatPage（A301 群聊页 group.html）</summary>
        private string _staticPage;

        /// <summary>管理路由注册回调——入口壳注入（Admin 域经 IHttpRouteSink 注册 llm-apis/workspace 等；仅主端口非空）</summary>
        private Action<IHttpRouteSink> _routeRegistrar;

        /// <summary>html 根解析——入口壳注入（源码区优先 + 部署区回退——Program.ResolveHtmlRoot 适配）</summary>
        private IHtmlRootProvider _htmlRootProvider;

        /// <summary>页面标题——快照注入 title 字段（Q6 2026-09-08：displayName · Chat；前端初始化 fetch 快照直接用；null=不注入）</summary>
        private string _pageTitle;

        /// <summary>快照推送间隔毫秒——250ms（协议 §4.2 snapshot 事件）</summary>
        private const int SnapshotIntervalMs = 250;
        /// <summary>前端测试代理客户端——静态复用（R6-P3-01；Timeout 仅初始化可设，故用工厂构造）</summary>
        private static readonly System.Net.Http.HttpClient FrontendTestHttp = CreateFrontendTestHttp();

        /// <summary>
        /// 启动 HTTP 外观层——Kestrel 绑定端口 + 注册路由 + 后台推送任务启动。
        /// </summary>
        /// <param name="options">启动参数（端口/会话/回调集/页面模式——见 HttpHostOptions 各属性注释）</param>
        /// <returns>HttpHost 实例</returns>
        public static HttpHost Start(HttpHostOptions options)
        {
            HttpHost host = new HttpHost();
            host._port = options.Port;
            host._frontendTestPort = options.FrontendTestPort;
            host._sessionId = options.SessionId;
            host._snapshotBuilder = options.SnapshotBuilder;
            host._dispatcher = options.Dispatcher;
            host._depsRefresh = options.DepsRefresh;
            host._llmProbe = options.LlmProbe;
            host._consoleAuth = options.ConsoleAuth;
            // A328 端口面——未声明 = console（主端口全量）；chat / group 各自最小集
            host._surfaceConsole = true;
            host._surfaceChat = false;
            host._surfaceGroup = false;
            if (string.Equals(options.Surface, "chat", StringComparison.OrdinalIgnoreCase))
            {
                host._surfaceConsole = false;
                host._surfaceChat = true;
            }
            else if (string.Equals(options.Surface, "group", StringComparison.OrdinalIgnoreCase))
            {
                host._surfaceConsole = false;
                host._surfaceGroup = true;
            }
            host._envelopeBuilder = options.EnvelopeBuilder;
            host._frameBuilder = options.FrameBuilder;
            host._catsBuilder = options.CatsBuilder;
            host._patchBuilder = options.PatchBuilder;
            host._viewFullBuilder = options.ViewFullBuilder;
            host._viewFrameBuilder = options.ViewFrameBuilder;
            host._serveChatPage = options.ServeChatPage;
            host._staticPage = options.StaticPage;
            host._routeRegistrar = options.RouteRegistrar;
            host._htmlRootProvider = options.HtmlRootProvider;
            if (options.DisplayName != null && options.DisplayName.Length > 0)
            {
                host._pageTitle = options.DisplayName + " · Chat";
            }
            host.BuildApp();
            host._pumpCts = new CancellationTokenSource();
            host._pumpTask = Task.Run(delegate
            {
                host.PumpLoop(host._pumpCts.Token);
            });
            return host;
        }

        /// <summary>
        /// 构建 Kestrel 应用——路由注册（快照/流/指令/静态页）。
        /// </summary>
        private void BuildApp()
        {
            WebApplicationBuilder builder = WebApplication.CreateSlimBuilder();
            // D5：Kestrel 请求级日志降噪（Hosting.Diagnostics 每请求 4-6 行 → 仅 Warning；Lifetime 启动一行保留）
            builder.Logging.AddFilter("Microsoft.Hosting.Lifetime", LogLevel.Warning);
            builder.Logging.AddFilter("Microsoft.AspNetCore", LogLevel.Warning);
            builder.WebHost.ConfigureKestrel((options) =>
            {
                options.ListenLocalhost(_port);
            });
            _app = builder.Build();
            // A328——端口面收缩：非 console 面只放行最小端点集（其余 /api/v1 请求 404）
            if (!_surfaceConsole)
            {
                _app.Use(HttpSurfaceGate);
            }
            // A328——console 口令校验：仅主端口注入 ConsoleAuth 时启用（其余端口 null = 不校验）
            if (_consoleAuth != null)
            {
                _app.Use(ConsoleAuthGate);
                _app.MapGet("/api/v1/auth/status", (Delegate)HandleAuthStatus);
                _app.MapPost("/api/v1/auth/login", (Delegate)HandleAuthLogin);
                _app.MapPost("/api/v1/auth/setup", (Delegate)HandleAuthSetup);
                _app.MapPost("/api/v1/auth/logout", (Delegate)HandleAuthLogout);
            }
            _app.MapGet("/api/v1/snapshot", (HttpContext ctx) =>
            {
                // 缓存快照——主线程 PumpMainThread 构建（ThreadGuard：OA.GetSnapshot 仅宿主主线程）
                // ?logs=N——动态附加日志段（LogStore 锁内快照，HTTP 线程安全；v2 界面刷新拉最后 N 条）
                string json = _snapshotCache;
                int logCount = ParseLogsQuery(ctx);
                string logsJson = BuildLogsJson(logCount);
                if (logsJson.Length > 0)
                {
                    json = ReplaceLogsSection(json, logsJson);
                }
                return Results.Text(json, "application/json");
            });
            _app.MapGet("/api/v1/stream", (RequestDelegate)StreamEvents);
            _app.MapPost("/api/v1/command", (Delegate)HandleCommand);
            // A253 5b——依赖探测手动刷新：重置全部条目探测时刻（下一轮快照全量重探；一次性档亦随之刷新）
            if (_depsRefresh != null)
            {
                _app.MapPost("/api/v1/deps/refresh", (HttpContext ctx) =>
                {
                    _depsRefresh();
                    return Results.Text("{\"ok\":true}", "application/json");
                });
            }
            // A253 扩展——LLM 端点池探活：手动触发，逐个端点发一次最小请求（真实链路）
            if (_llmProbe != null)
            {
                _app.MapPost("/api/v1/llm/probe", async (HttpContext ctx) =>
                {
                    string probeJson = await _llmProbe().ConfigureAwait(false);
                    return Results.Text(probeJson, "application/json");
                });
            }
            _app.MapGet("/api/v1/logs", (Delegate)HandleLogs);
            _app.MapGet("/api/v1/config", (Delegate)HandleConfigGet);
            _app.MapPost("/api/v1/config", (Delegate)HandleConfigPost);
            if (_catsBuilder != null)
            {
                _app.MapGet("/api/v1/cats", (HttpContext ctx) =>
                {
                    // P9.3 多猫列表——catsBuilder 非空才注册（主端口管理页签数据源；每猫实例不注册）
                    return Results.Text(_catsBuilder(), "application/json");
                });
            }
            // S2 管理端点族——经 IHttpRouteSink 由 Admin 域注册（Http 域零依赖 Admin 域）
            // 2026-09-18：移出 _catsBuilder 块——catsBuilder 只决定 /api/v1/cats 注册面，
            // 每猫 host（CatsBuilder=null）同样要走自己的 registrar（采集面下发）
            if (_routeRegistrar != null)
            {
                _routeRegistrar(this);
            }
            _app.MapGet("/", (HttpContext ctx) =>
            {
                // B4 修复：静态页禁缓存——前端频繁迭代，浏览器启发式缓存导致拿旧版 html（工具回填等新 JS 不生效）
                ctx.Response.Headers["Cache-Control"] = "no-cache, no-store, must-revalidate";
                return ServeIndex();
            });
            // 2026-08-25 模块化拆分——静态资源多文件路由（css/js 子目录；禁缓存同 index 策略；路径穿越校验）
            // A167——js 面改 catch-all（{**file}）：前端重构后脚本分 lib/ · blocks/ 子目录，单段 {file} 匹配不到含斜杠路径；
            //        css 面仍平铺（无子目录），保持单段
            _app.MapGet("/css/{file}", (HttpContext ctx) => ServeStatic(ctx, "css", "text/css"));
            _app.MapGet("/js/{**file}", (HttpContext ctx) => ServeStatic(ctx, "js", "application/javascript"));
            // 桌宠资源——html/pet/<skin>/*.webp（动画 WebP 二进制；禁缓存同 index 策略；路径穿越校验）
            // A245——按皮肤分子目录，单段 {file} 匹配不到含斜杠路径 → catch-all（同 js 面 A167）
            _app.MapGet("/pet/{**file}", (HttpContext ctx) => ServeStatic(ctx, "pet", "image/webp"));
            // A120 字体资源——html/fonts/*.ttf（web 字体二进制；缓存口径与其余静态资源不同——见 ServeStatic）
            _app.MapGet("/fonts/{file}", (HttpContext ctx) => ServeStatic(ctx, "fonts", "font/ttf"));
            // A118 应用图标——浏览器标签页 favicon（html/favicon.ico；与 exe 内嵌图标同源，由 tools/icongen 一并产出）
            _app.MapGet("/favicon.ico", (HttpContext ctx) =>
            {
                ctx.Response.Headers["Cache-Control"] = "no-cache, no-store, must-revalidate";
                return ServeRootFile("favicon.ico", "image/x-icon");
            });
            // 前端测试服务代理——CH4 通过宿主端点触发前端测试（开发流程：改前端 → 跑测试 → 刷新生效；转发区段端口）
            _app.MapGet("/api/v1/frontend-test", async (HttpContext ctx) =>
            {
                string path = "unit";   // E2E 已移除（2026-08-28）——只转发 Vitest
                try
                {
                    System.Net.Http.HttpResponseMessage resp = await FrontendTestHttp.GetAsync("http://127.0.0.1:" + _frontendTestPort.ToString() + "/api/test/" + path);
                    string body = await resp.Content.ReadAsStringAsync();
                    return Results.Text(body, "application/json");
                }
                catch (Exception ex)
                {
                    return Results.Text("{\"ok\":false,\"error\":\"" + ex.Message + "\"}", "application/json");
                }
            });
            try
            {
                _app.StartAsync().GetAwaiter().GetResult();
            }
            catch (Exception ex)
            {
                // 端口绑定失败分类——优先提示"实例已在运行"（P7 裂缝1：双击 exe 闪退判例）
                string detail = ex.Message;
                string hint;
                if (detail.IndexOf("in use", StringComparison.OrdinalIgnoreCase) >= 0 || detail.IndexOf("占用", StringComparison.Ordinal) >= 0)
                {
                    hint = "端口 " + _port + " 已被占用——可能已有 CH4 实例在运行";
                }
                else
                {
                    hint = "HTTP 端口 " + _port + " 绑定失败";
                }
                throw new InvalidOperationException(hint + "（" + detail + "）", ex);
            }
        }

        /// <summary>
        /// 注册 GET 路由——IHttpRouteSink 实现（Admin 域管理路由注册面；转发到 Minimal API 路由表——S2 解耦：Http 域零依赖 Admin 域）。
        /// </summary>
        /// <param name="pattern">路由模板（如 /api/v1/llm-apis）</param>
        /// <param name="handler">处理委托（Minimal API Delegate）</param>
        public void MapGet(string pattern, Delegate handler)
        {
            _app.MapGet(pattern, handler);
        }

        /// <summary>
        /// 注册 POST 路由——IHttpRouteSink 实现。
        /// </summary>
        /// <param name="pattern">路由模板</param>
        /// <param name="handler">处理委托（Minimal API Delegate）</param>
        public void MapPost(string pattern, Delegate handler)
        {
            _app.MapPost(pattern, handler);
        }

        /// <summary>
        /// 停止外观层——Kestrel 停止 + 后台推送取消。
        /// </summary>
        public void Stop()
        {
            _stopped = true;
            if (_pumpCts != null)
            {
                _pumpCts.Cancel();
                _pumpCts.Dispose();
                _pumpCts = null;
            }
            if (_app != null)
            {
                _app.StopAsync().GetAwaiter().GetResult();
                _app = null;
            }
        }

        /// <summary>
        /// 已绑定端口——对外查询（观测用）。
        /// </summary>
        public int Port
        {
            get { return _port; }
        }

        /// <summary>
        /// SSE 客户端——每连接一个无界事件队列（协议 §4.3：多浏览器连接独立广播）。
        /// </summary>
        private sealed class SseClient
        {
            /// <summary>队列容量——满时丢最旧（PushEvent 溢出判定同源单点）</summary>
            public const int Capacity = 512;

            /// <summary>事件队列——宿主 Push 入队 / 连接消费写响应</summary>
            public Channel<string> Queue;

            /// <summary>本连接帧序号——入队即分配（被丢弃的帧带走自身序号，客户端因此见 id 落差；仅 _clientLock 内推进）</summary>
            public int SentSeq;

            /// <summary>溢出出声哨兵——首次队列满时出声一次（避免刷屏）</summary>
            public bool OverflowReported;

            /// <summary>溢出计数——本连接累计丢弃次数（诊断面）</summary>
            public long OverflowCount;

            /// <summary>订阅主题集——null = 全量（缺省向后兼容）；非 null 时只接收集合内的事件名</summary>
            public HashSet<string> Topics;

            /// <summary>
            /// 是否订阅该事件——按消费方裁剪负载（不订阅的类型根本不入队）。
            /// </summary>
            /// <param name="eventName">事件名</param>
            /// <returns>true = 订阅（Topics 为 null 时全量订阅）</returns>
            public bool Wants(string eventName)
            {
                if (Topics == null)
                {
                    return true;
                }
                return Topics.Contains(eventName);
            }

            /// <summary>HTTP 响应——写入 SSE 帧</summary>
            public HttpResponse Response;

            /// <summary>
            /// 构造 SSE 客户端
            /// </summary>
            /// <param name="response">HTTP 响应</param>
            public SseClient(HttpResponse response)
            {
                Response = response;
                // 有界队列 + 满时丢最旧——保留最新事件；慢客户端内存有界（协议 §四"写失败静默丢弃"；无界队列慢客户端堆积判例）
                BoundedChannelOptions options = new BoundedChannelOptions(Capacity);
                options.FullMode = BoundedChannelFullMode.DropOldest;
                options.SingleReader = true;
                Queue = Channel.CreateBounded<string>(options);
            }

            /// <summary>
            /// 组装一帧 SSE 文本——标准 id 行 + event 行 + data 行；id 入队即分配（被丢弃的帧带走自身序号，客户端因此见落差）。
            /// </summary>
            /// <param name="eventName">事件名</param>
            /// <param name="data">JSON 载荷</param>
            /// <returns>完整帧文本</returns>
            public string NextFrame(string eventName, string data)
            {
                SentSeq = SentSeq + 1;
                return "id: " + SentSeq + "\nevent: " + eventName + "\ndata: " + data + "\n\n";
            }
        }
        /// <summary>
        /// 创建前端测试代理客户端——静态初始化设超时（HttpClient.Timeout 首次请求后不可改）。
        /// </summary>
        /// <returns>配置好超时的客户端实例</returns>
        private static System.Net.Http.HttpClient CreateFrontendTestHttp()
        {
            System.Net.Http.HttpClient h = new System.Net.Http.HttpClient();
            h.Timeout = TimeSpan.FromSeconds(120);
            return h;
        }
    }
}