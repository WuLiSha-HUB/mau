using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using System.Threading;
using CatHome4.Admin;
using CatHome4.Contracts;
using CatHome4.Core;
using CatHome4.Http;
using CatHome4.QQ;
using CatHome4.Observe;
using Mau.Runtime;
using Mau.Providers;
using Mau.Development;

namespace CH4
{
    /// <summary>
    /// CatHome4 —— Mau 自举循环宿主（第一轮）
    /// 职责：Runtime 服务组装 + 语料加载 + Command 投递器 + LLM 桥 + 观测出口 + 指令路由。
    /// 业务代码 0 行——全部功能在 .mau 语料（热重载面）；无 UI（无头优先）。
    /// 入口/组装面（Main/Bootstrap/FindDllDir）在本文件；
    /// 运行模式分部在 Program.Run.cs；工具执行面在 Program.Tools.cs/Program.Tools.Exec.cs；
    /// 热重载面在 Program.Reload.cs；路径解析面在 Program.Resolve.cs；工具协调在 Program.Chat.cs（P7b partial 拆分续）。
    /// </summary>
    public static partial class Program
    {
        // [段1] LLM 桥常量——P4 配置项机制接管：llm.* 从 Data/config/llm.cfg 拉起（改配置不重编译）；key 优先级：配置 → 环境变量 DEEPSEEK_API_KEY
        /// <summary>
        /// 宿主帧预算——主循环目标帧周期（毫秒）。主循环按「补足式」推进：本帧工作实际耗时不足预算则睡差额，
        /// 超出则不睡（该帧即「超帧」——LoopTiming 计数）；其余模式（自检 / --run 驱动）仍按固定睡眠解释本值。
        /// <para>
        /// 🔴 语义变更（2026-10-16 · 50 → 100）：帧数口径的时限随之**等比放大**——语料 <c>[t=2400]</c>（≈240 s）
        /// 与宿主对齐常量（`ToolBatchWaitFrames` / `RoundSummaryTimeoutFrames` / `MapTimeoutFrames` / `AutoStartPollFrames`）
        /// **保持原值不动**（语料侧为翻译期字面量、不可运行时换算）；一致性不破——等待上限仍大于语料侧超时。
        /// 调本值 = 帧率变化 + 所有帧数时限的实际时间等比变化，改前先核连带面。
        /// </para>
        /// </summary>
        private const int FrameTargetMs = 100;

        /// <summary>单轮驱动帧上限——LLM 最坏 60s + 超时结算余量（30000 帧 ≈ 25 分钟空转上限，实际由 Idle 判定提前退出）</summary>
        private const int MaxFramesPerRun = 30000;

        /// <summary>预热帧——CmdPump 懒注册 CommandBus 发生在生成物首 Tick（注册完成前宿主投递会被 SetText REJECT——未注册）；10 帧含安全余量（启动/重载/回滚/自检共用——P7b 缺口2 纪律文档化）</summary>
        private const int WarmupFrames = 10;

        /// <summary>宿主组件——Flow 注册/驱动入口</summary>
        private static FlowRunner _runner;

        /// <summary>工具组 Flow 句柄表——按 Flow 名索引（R0.2：TextCat/MauCat/CsCat/ConfigCat 独立 Flow——独立热重载/独立退役）。🔴 线程契约：主线程独占（写 = Bootstrap/reload；读 = 观测打印 + 主循环 Idle 判定）——无跨线程访问，故不加锁；新增写点必须遵守本契约</summary>
        private static readonly Dictionary<string, FlowHandle> _toolFlowHandles = new Dictionary<string, FlowHandle>();

        /// <summary>注册 ID——观测与回收用</summary>
        /// <summary>工具组 Flow 注册 ID 表——按 Flow 名索引（观测与回收用）。🔴 线程契约：主线程独占（与 _toolFlowHandles 同源同寿命）——无跨线程访问</summary>
        private static readonly Dictionary<string, long> _toolFlowIds = new Dictionary<string, long>();

        /// <summary>命令总线——Command 投递器直接引用（SetText 面）</summary>
        private static CommandBus _bus;

        /// <summary>OA 工单平台——观测快照（诊断期）</summary>
        private static OA _oa;
        /// <summary>HTTP 外观层——Kestrel + Minimal API（P6：快照/SSE/指令/静态页）</summary>
        private static HttpHost _httpHost;
        /// <summary>会话协调桥——S1 程序集拆分：会话注册表/默认猫配置/轮转泵/会话指令（Core 实例类）</summary>
        private static ChatBridge _chatBridge;
        /// <summary>
        /// qqbot 绑定目标收集适配——IQqTargetCollector 实现（S3 解耦：QQ 域经接口消费，入口壳提供——默认猫 + 多猫注册表）。
        /// </summary>
        private sealed class QqTargetCollector : IQqTargetCollector
        {
            /// <summary>收集绑定指定 qqbot 的猫——委托到 Program.CollectQqTargets</summary>
            /// <param name="qqBotId">qqbot 配置身份</param>
            /// <returns>绑定目标列表（空=未绑定）</returns>
            public List<QqTarget> CollectByBot(Guid qqBotId)
            {
                return CatHostService.CollectQqTargets(qqBotId);
            }

            /// <summary>收集所有绑定 qqbot 的猫——委托到 Program.CollectAllQqTargets</summary>
            /// <returns>全部绑定目标</returns>
            public List<QqTarget> CollectAll()
            {
                return CatHostService.CollectAllQqTargets();
            }
        }

        /// <summary>
        /// html 根解析适配——IHtmlRootProvider 实现（S2 解耦：Http 域经接口消费 html 根，入口壳提供实现——源码区优先 + 部署区回退）。
        /// </summary>
        private sealed class HtmlRootProvider : IHtmlRootProvider
        {
            /// <summary>解析 html 根——委托到 Program.ResolveHtmlRoot（源码区优先 + 部署区回退——唯一事实源）</summary>
            /// <returns>html 根绝对路径</returns>
            public string ResolveHtmlRoot()
            {
                return Program.ResolveHtmlRoot();
            }
        }

        /// <summary>
        /// 主程序入口——参数路由：无参=交互模式 / --selfcheck=启动自检 / --run "指令"=单指令脚本模式 / --script &lt;file&gt;=指令文件批量模式（P3a 热重载实测通道）/ --probe-llm &lt;目标&gt;=LLM 返回体探针（方案 B 诊断通道——不启动宿主）
        /// </summary>
        public static int Main(string[] args)
        {
            _mainThreadId = Environment.CurrentManagedThreadId;
            // A118 外壳初始化——高 DPI（必须早于任何窗口创建）+ 启动时刻（已运行时间基准）+ AUMID（通知图标身份键）
            ShellInit();
            InitAppUserModelId();
            // A260 启动韧性——早期诊断 sink 先于一切配置解析（数据根不可写时回落 %TEMP%，再回落仅内存）
            BootLog.Init(ResolveDataRoot());
            try
            {
                Console.OutputEncoding = System.Text.Encoding.UTF8;
            }
            catch (Exception ex)
            {
                // 输出编码设置失败不影响功能
                LogStore.Add("CatHome4", 2, "控制台编码设置失败: " + ex.Message, "CONFIG");
            }
            try
            {
                // [段1a] 参数面校验——非交互零容忍（调用方是程序，必须拿到失败退出码）；
                // 交互模式降级为提示后以无参交互继续（A260 R2：环境/配置类问题不得阻断启动）
                string argErr = ValidateHostArgs(args, out bool nonInteractive);
                if (argErr.Length > 0)
                {
                    Console.WriteLine("[CMD] " + argErr);
                    BootLog.Add("S0", "参数面：" + argErr);
                    if (nonInteractive)
                    {
                        return 1;
                    }
                    StartupDegradation.Report("ARG_DEGRADED", StartupDegradation.KindConfig, "启动参数",
                        StartupDegradation.StateFallback, argErr,
                        "按正确用法重新启动（启动提示窗列有用法）", "S0");
                }
                // A118：非交互模式输出双写落盘（WinExe 无控制台——shell 直调取不到 stdout；父进程重定向链路不受影响）
                if (nonInteractive)
                {
                    EnableCmdCapture();
                }
                _majordomoPush = ExtractArgValue(args, "--majordomopush");
                // [段1b] 探针通道——LLM 返回体结构诊断（方案 B：宿主能力；不启动宿主/不占端口/不载语料，先于 Bootstrap 返回）
                if (HasProbeLlmArg(args))
                {
                    return RunProbeLlm(args);
                }
                // [段2] 服务组装 + 语料加载
                string dllDir = FindDllDir(args);
                try
                {
                    Bootstrap(dllDir, nonInteractive);
                }
                catch (Exception ex)
                {
                    Console.WriteLine("[CMD] 启动失败: " + ex.Message);
                    BootLog.Add("S7", "启动链异常：" + ex.ToString());
                    WriteStartupReport("failed");
                    // A118：WinExe 无控制台——启动失败必须可见（原 Console.ReadKey 在无控制台时抛异常且失败静默）；
                    // 脚本/自检/探针模式无人值守不弹窗，仅以退出码 1 表达
                    bool showFailBox = !nonInteractive;
                    if (showFailBox)
                    {
                        ShowStartupFailure(ex.Message);
                    }
                    return 1;
                }
                // [段2b] 预热帧——CmdPump 懒注册 CommandBus 发生在生成物首 Tick（投递前必须注册完成）
                for (int i = 0; i < WarmupFrames; i++)
                {
                    _runner.Tick();
                }
                // 注册确认——仅在语料真的使用 `⇚`（模块数 > 0）时出声；harness 不投递，常态静默（A278 裁决 / A283）
                string[] keyDic = _bus.GetKeyDic();
                if (keyDic.Length > 0 && keyDic[0].IndexOf("(0个模块)", StringComparison.Ordinal) < 0)
                {
                    Console.WriteLine("[CMD] CommandBus " + keyDic[0]);
                }
                // [段2c] P1 自启同步——启动时注册表与配置对齐（防手动删注册表漂移）
                // 🔴 仅交互模式——非交互探针不得改写 HKCU Run 项（开发区 Data 无 app.cfg → 默认 false → 会静默删掉部署区自启项）
                if (!nonInteractive)
                {
                    InitAutoStart();
                }
                // [段3] 模式路由
                string mode = "";
                for (int i = 0; i < args.Length; i++)
                {
                    if (args[i] == "--selfcheck")
                    {
                        mode = "selfcheck";
                    }
                    else if (args[i] == "--run" && i + 1 < args.Length)
                    {
                        mode = "run:" + args[i + 1];
                    }
                    else if (args[i] == "--script" && i + 1 < args.Length)
                    {
                        mode = "script:" + args[i + 1];
                    }
                }
                if (mode == "selfcheck")
                {
                    return RunSelfCheck();
                }
                if (mode.StartsWith("run:", StringComparison.Ordinal))
                {
                    return RunSingle(mode.Substring(4));
                }
                if (mode.StartsWith("script:", StringComparison.Ordinal))
                {
                    return RunScript(mode.Substring(7));
                }
                return RunInteractive();
            }
            finally
            {
                // O4 优雅收尾——统一观测四文件 flush 落盘（0 字节判例根因修复：--run 模式直接 return 丢缓冲）
                LogStore.CloseWriters();
                FrameStore.Close();
                // A118：非交互输出落盘收尾
                CloseCmdCapture();
                // A110 浏览器实例清理——Chromium detach（宿主退出不带走子进程）；不清会留孤儿实例占用 profile
                IBrowserLifecycle browserLifecycle;
                DataBox.TryResolve<IBrowserLifecycle>(out browserLifecycle);
                if (browserLifecycle != null)
                {
                    browserLifecycle.Shutdown();
                }
                // 网页资产下载收尾——停通知泵 + 取消在途任务（不置终态：半截 .part 留待下次续传）
                IWebFetchService webFetchLifecycle;
                DataBox.TryResolve<IWebFetchService>(out webFetchLifecycle);
                if (webFetchLifecycle != null)
                {
                    webFetchLifecycle.Shutdown();
                }
            }
        }
        /// <summary>
        /// 服务组装——OA/CommandBus/FlowRunner + DataBox 绑定 + 审计配置。
        /// headless=true（非交互模式）跳过外观层与常驻附属（段6）——探针通道与常驻实例端口零耦合（A74）。
        /// </summary>
        /// <param name="dllDir">语料生成物 dll 目录</param>
        /// <param name="headless">非交互模式（--run/--script/--selfcheck）</param>
        private static void Bootstrap(string dllDir, bool headless)
        {
            // A260 二期——八段执行器编排：按外部依赖边界切段，每段失败登记后继续（R1：环境异常不得阻断启动）
            // 段序 S0 → S2 → S1 → S3 → S4 → S5 → S6 → S7（受控根先于配置面——WritableRootPaths 是配置面服务的构造入参）
            BootContext ctx = new BootContext();
            ctx.DllDir = dllDir;
            ctx.Headless = headless;
            RunStep(ctx, "S0", "机制服务（OA / CommandBus / FlowRunner）", BootDegradePolicy.Internal, StageS0);
            RunStep(ctx, "S2", "文件工具（受控根）", BootDegradePolicy.EmptySet, StageS2);
            RunStep(ctx, "S1", "配置面（schema / 配置池 / 服务绑定）", BootDegradePolicy.EmptySet, StageS1);
            RunStep(ctx, "S3", "观测日志", BootDegradePolicy.Continue, StageS3);
            RunStep(ctx, "S4", "工具面（全部 Flow）", BootDegradePolicy.EmptySet, StageS4);
            RunStep(ctx, "S5", "会话面（默认猫）", BootDegradePolicy.EmptySet, StageS5);
            RunStep(ctx, "S6", "外观层（主端口）", BootDegradePolicy.Continue, StageS6);
            RunStep(ctx, "S7", "常驻附属（多猫 / QQ / py / 前端测试服务）", BootDegradePolicy.Continue, StageS7);
            // 启动健康面收口——日志出口（逐条 + 摘要）· 报告落盘 · 独立提示窗
            StartupDegradeEntry[] bootEntries = StartupDegradation.Snapshot();
            for (int i = 0; i < bootEntries.Length; i = i + 1)
            {
                LogStore.Add("CatHome4", 2, "boot.degrade | " + bootEntries[i].Code + " | "
                    + bootEntries[i].Ability + " | " + bootEntries[i].Detail, "BOOT");
            }
            if (headless)
            {
                Console.WriteLine("[CMD] 启动降级 " + StartupDegradation.Count.ToString() + " 项——详见 startup_report.json");
                WriteStartupReport("non-interactive");
                return;
            }
            WriteStartupReport("interactive");
            if (StartupDegradation.HasAny)
            {
                LogStore.Add("CatHome4", 2, "boot.summary | " + StartupDegradation.BuildSummaryLine(), "BOOT");
                ShowStartupDegradeWindow();
            }
        }
        /// <summary>
        /// 环境信息——运行版本 + LLM 端点类型 + 当前时间（info 内置工具数据源；R0.2 拍板：不显示工具组清单——工具注册是前文初始化一次性）
        /// </summary>
        /// <returns>环境信息文本</returns>
        internal static string BuildEnvInfo()
        {
            string version = VersionInfo.GetEntryVersion();
            if (version.Length == 0)
            {
                version = "?";
            }
            // info 返回体 = 分类 JSON 块（design-ch4-tools 附录 §info——LLM 可读优先：分类 + 缩进 + 中文直显）
            // 分类：version(版本+编译时刻) / time(当前时间) / llm / endpoint(本地端点) / roots / tokens / packs / qqbot
            string catKey = ToolCatContext.CurrentCatKey;
            if (catKey == null)
            {
                catKey = "";
            }
            Dictionary<string, object> info = new Dictionary<string, object>();
            info["ok"] = true;
            info["tool"] = "info";
            info["cat"] = catKey;
            // [段1] version——版本与编译时刻（与 time 大类严格分开）
            Dictionary<string, object> versionBlock = new Dictionary<string, object>();
            versionBlock["version"] = version;
            versionBlock["build"] = VersionInfo.GetEntryBuildTime();
            info["version"] = versionBlock;
            // [段2] time——当前时间
            Dictionary<string, object> timeBlock = new Dictionary<string, object>();
            timeBlock["now"] = DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss");
            info["time"] = timeBlock;
            // [段3] llm——本猫实际生效端点（A50 端点解析同源化；零值/缺失 = 跟随全局默认）
            Dictionary<string, object> llmBlock = new Dictionary<string, object>();
            llmBlock["protocol"] = "未配置";
            llmBlock["host"] = "";
            llmBlock["model"] = "";
            llmBlock["source"] = "";
            try
            {
                bool followDefault = true;
                CH_LlmApiConfig cfg = CatHostService.ResolveEffectiveApiConfig(catKey, out followDefault);
                if (cfg != null)
                {
                    // 端点摘要——主机（去协议与路径；端点非私密——key 在 secrets 面）
                    string host = cfg.Endpoint;
                    if (host.Length > 0)
                    {
                        host = host.Replace("https://", "").Replace("http://", "");
                        int slash = host.IndexOf('/');
                        if (slash > 0)
                        {
                            host = host.Substring(0, slash);
                        }
                    }
                    llmBlock["protocol"] = cfg.ApiType;
                    llmBlock["host"] = host;
                    llmBlock["model"] = cfg.DefaultModel;
                    llmBlock["source"] = followDefault ? "跟随默认" : "猫绑定";
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "LLM 协议读取失败: " + ex.Message, "CONFIG");
                llmBlock["protocol"] = "读取失败";
            }
            info["llm"] = llmBlock;
            // [段4] endpoint——本地端点位置（本猫对话页 + 主管理面板；端口未监听 = 空串）
            Dictionary<string, object> endpointBlock = new Dictionary<string, object>();
            int chatPort = CatHostService.ResolveCatPort(catKey);
            endpointBlock["chat"] = chatPort > 0 ? "http://127.0.0.1:" + chatPort.ToString() : "";
            int panelPort = 0;
            if (_httpHost != null)
            {
                panelPort = _httpHost.Port;
            }
            endpointBlock["panel"] = panelPort > 0 ? "http://127.0.0.1:" + panelPort.ToString() : "";
            info["endpoint"] = endpointBlock;
            // [段5] roots——猫可见受控根（M4e 猫级白名单；info 是猫自省目录范围的通道）
            List<Dictionary<string, object>> rootList = new List<Dictionary<string, object>>();
            if (catKey.Length > 0)
            {
                WorkspaceConfig.RootEntry[] entries = CatHostService.ResolveCatRootEntries(catKey);
                for (int i = 0; i < entries.Length; i = i + 1)
                {
                    Dictionary<string, object> item = new Dictionary<string, object>();
                    item["id"] = entries[i].Id;
                    item["writable"] = entries[i].Writable;
                    item["note"] = entries[i].Note == null ? "" : entries[i].Note;
                    // [段5b] path——规范绝对路径（物理位置认知；与文件工具根解析同规）
                    string rootPath = entries[i].Path;
                    if (string.IsNullOrWhiteSpace(rootPath))
                    {
                        item["path"] = "";
                    }
                    else
                    {
                        item["path"] = WorkspaceConfig.NormalizeRoot(rootPath);
                    }
                    rootList.Add(item);
                }
            }
            info["roots"] = rootList;
            // [段6] tokens——前文长度（请求级实时值优先，未发起请求则回落最近一轮轮末落盘值——「看别猫」场景 idle 猫也有可读值）+ 最近前文变动时刻（Unix 毫秒；展示层格式化）
            ChatSession session = CatHostService.FindCatSession(catKey);
            long ctxLen = 0;
            long lastChangeAt = 0;
            if (session != null)
            {
                ctxLen = session.ContextTokensKnown;
                lastChangeAt = session.LastContextChangeAt;
            }
            Dictionary<string, object> tokensBlock = new Dictionary<string, object>();
            tokensBlock["context"] = ctxLen;
            tokensBlock["lastContextChangeAt"] = lastChangeAt;
            info["tokens"] = tokensBlock;
            // [段7] packs——本猫挂载包（空 = 不输出该键）
            List<Dictionary<string, object>> packList = AdminService.BuildMountedPackItems(catKey);
            if (packList.Count > 0)
            {
                info["packs"] = packList;
            }
            // [段8] qqbot——渠道说明（未接入 = 不输出该键）
            string qqbotShow = CatHostService.BuildCatQqBotInfo(catKey);
            if (qqbotShow.Length > 0)
            {
                Dictionary<string, object> qqbotBlock = new Dictionary<string, object>();
                qqbotBlock["usage"] = qqbotShow;
                info["qqbot"] = qqbotBlock;
            }
            // [段8b] db——授权库清单（A279）：库权限（cat.cfg dbLibs）已授权时 info 必须自证可见面；
            // 判据取 DbService.ListLibs（按 catId 授权过滤 + 摘要自证全集）；服务缺失 / 读失败如实出声，不静默省略该段
            Dictionary<string, object> dbBlock = new Dictionary<string, object>();
            DbService dbSvc = null;
            bool dbSvcReady = DataBox.TryResolve<DbService>(out dbSvc);
            if (!dbSvcReady || dbSvc == null)
            {
                dbBlock["ok"] = false;
                dbBlock["reason"] = "数据库服务未绑定";
            }
            else
            {
                DbResult dbList = dbSvc.ListLibs(catKey);
                dbBlock["ok"] = dbList.Ok;
                if (dbList.Ok)
                {
                    dbBlock["summary"] = dbList.Message;
                    JsonDocument dbDoc = JsonUtil.Parse(dbList.Data, "info.db");
                    if (dbDoc != null)
                    {
                        JsonElement dbLibsNode;
                        if (dbDoc.RootElement.TryGetProperty("libs", out dbLibsNode) && dbLibsNode.ValueKind == JsonValueKind.Array)
                        {
                            dbBlock["libs"] = dbLibsNode.Clone();
                        }
                        dbDoc.Dispose();
                    }
                }
                else
                {
                    dbBlock["reason"] = dbList.Code + " " + dbList.Message;
                }
            }
            bool dbRootVisible = false;
            for (int i = 0; i < rootList.Count; i = i + 1)
            {
                object rootIdObj;
                if (rootList[i].TryGetValue("id", out rootIdObj))
                {
                    string rootIdText = rootIdObj as string;
                    if (rootIdText != null && string.Equals(rootIdText, "db", StringComparison.OrdinalIgnoreCase))
                    {
                        dbRootVisible = true;
                        break;
                    }
                }
            }
            dbBlock["rootVisible"] = dbRootVisible;
            info["db"] = dbBlock;
            // [段9] tools_drift——工具面漂移（会话注入面 vs 当前授权集；仅不一致时输出——design-ch4-tools §三·十一）
            // added 附用法摘要（name/desc/args——注册表声明派生，冷启动猫不新会话即可上手；removed 只给名字）
            if (session != null)
            {
                string[] declaredNames = session.DeclaredToolNames;
                string[] authorizedNames = CatHostService.ResolveCatAuthorizedToolNames(catKey);
                List<string> driftAdded = new List<string>();
                List<string> driftRemoved = new List<string>();
                for (int i = 0; i < authorizedNames.Length; i = i + 1)
                {
                    if (Array.IndexOf(declaredNames, authorizedNames[i]) >= 0)
                    {
                        continue;
                    }
                    // 三面判定——特权面 / 池校验 / 授权集全过才算「新增可用」（不能用 = 不报；莎 2026-09-28）
                    if (!session.IsToolAllowed(authorizedNames[i]))
                    {
                        continue;
                    }
                    driftAdded.Add(authorizedNames[i]);
                }
                for (int i = 0; i < declaredNames.Length; i = i + 1)
                {
                    if (Array.IndexOf(authorizedNames, declaredNames[i]) < 0)
                    {
                        driftRemoved.Add(declaredNames[i]);
                    }
                }
                if (driftAdded.Count > 0 || driftRemoved.Count > 0)
                {
                    Dictionary<string, object> driftBlock = new Dictionary<string, object>();
                    List<object> addedBriefs = new List<object>();
                    for (int i = 0; i < driftAdded.Count; i = i + 1)
                    {
                        addedBriefs.Add(BuildToolBrief(driftAdded[i]));
                    }
                    driftBlock["added"] = addedBriefs;
                    driftBlock["removed"] = driftRemoved;
                    info["tools_drift"] = driftBlock;
                }
            }
            // [段9b] tools_defect——工具组定义缺陷（A107：解析失败 / 结构不符 / 零产出；仅非空时输出——design-ch4-tools §三·十三）
            ToolDefect[] poolDefects = ToolPool.Defects();
            if (poolDefects.Length > 0)
            {
                List<object> defectItems = new List<object>();
                for (int i = 0; i < poolDefects.Length; i = i + 1)
                {
                    defectItems.Add(new { group = poolDefects[i].Group, stage = poolDefects[i].Stage, reason = poolDefects[i].Reason });
                }
                info["tools_defect"] = defectItems;
            }
            // [段10] timeback——上下文作用域现状 + 归档到达面（design-ch4-timeback §八 T2）
            // active：未闭合作用域（id / purpose / anchor / startAt / seconds / events）——无作用域不输出
            // recent：最近 5 次回收的首行 meta（t / id / n / seconds / purpose）——findings 全文不进 info（归档面不得大于会话面）
            // archive：写面可用性（ok=false = 最近一次落档失败；失败不阻断回卷，但必须可见）
            Dictionary<string, object> timebackBlock = new Dictionary<string, object>();
            bool timebackAny = false;
            if (session != null)
            {
                Dictionary<string, object> timebackActive = session.TimebackActiveSnapshot();
                if (timebackActive != null)
                {
                    long activeStartAt = 0;
                    object startAtObj;
                    if (timebackActive.TryGetValue("startAt", out startAtObj) && startAtObj is long)
                    {
                        activeStartAt = (long)startAtObj;
                    }
                    long activeSeconds = (DateTimeOffset.UtcNow.ToUnixTimeMilliseconds() - activeStartAt) / 1000;
                    if (activeSeconds < 0)
                    {
                        activeSeconds = 0;
                    }
                    timebackActive["seconds"] = activeSeconds;
                    timebackBlock["active"] = timebackActive;
                    timebackAny = true;
                }
            }
            string timebackDir = CatHostService.ResolveTimebackArchiveDir(catKey);
            if (timebackDir.Length > 0)
            {
                TimebackArchive timebackArchive = new TimebackArchive(timebackDir);
                List<string> timebackTail = timebackArchive.ReadRecentMeta(5);
                List<Dictionary<string, object>> timebackRecent = new List<Dictionary<string, object>>();
                for (int i = 0; i < timebackTail.Count; i = i + 1)
                {
                    Dictionary<string, object> item = BuildTimebackSummary(timebackTail[i]);
                    if (item != null)
                    {
                        timebackRecent.Add(item);
                    }
                }
                if (timebackRecent.Count > 0)
                {
                    timebackBlock["recent"] = timebackRecent;
                }
                Dictionary<string, object> archiveBlock = new Dictionary<string, object>();
                // 写面可用性取进程内标志——info 每次新建归档实例，实例标志（LastWriteFailed）在新实例上恒 false
                archiveBlock["ok"] = !TimebackArchive.LastWriteFailedAny;
                timebackBlock["archive"] = archiveBlock;
                timebackAny = true;
            }
            if (timebackAny)
            {
                info["timeback"] = timebackBlock;
            }
            return JsonSerializer.Serialize(info, CatHostService.InfoJsonOptions);
        }
        /// <summary>
        /// timeback 归档行摘要——info timeback.recent 条目（只取观测字段；findings 全文不进 info）。
        /// </summary>
        /// <param name="line">归档单行 JSON</param>
        /// <returns>摘要字典（不可解析 = null）</returns>
        private static Dictionary<string, object> BuildTimebackSummary(string line)
        {
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(line))
                {
                    JsonElement root = doc.RootElement;
                    Dictionary<string, object> item = new Dictionary<string, object>();
                    JsonElement tNode;
                    if (root.TryGetProperty("t", out tNode) && tNode.ValueKind == JsonValueKind.String)
                    {
                        item["t"] = tNode.GetString();
                    }
                    JsonElement idNode;
                    if (root.TryGetProperty("id", out idNode) && idNode.ValueKind == JsonValueKind.Number)
                    {
                        item["id"] = idNode.GetInt64();
                    }
                    JsonElement nNode;
                    if (root.TryGetProperty("n", out nNode) && nNode.ValueKind == JsonValueKind.Number)
                    {
                        item["n"] = nNode.GetInt32();
                    }
                    JsonElement secNode;
                    if (root.TryGetProperty("seconds", out secNode) && secNode.ValueKind == JsonValueKind.Number)
                    {
                        item["seconds"] = secNode.GetInt64();
                    }
                    JsonElement purposeNode;
                    if (root.TryGetProperty("purpose", out purposeNode) && purposeNode.ValueKind == JsonValueKind.String)
                    {
                        item["purpose"] = purposeNode.GetString();
                    }
                    JsonElement typeNode;
                    if (root.TryGetProperty("type", out typeNode) && typeNode.ValueKind == JsonValueKind.String)
                    {
                        item["type"] = typeNode.GetString();
                    }
                    return item;
                }
            }
            catch (Exception)
            {
                return null;
            }
        }
        /// <summary>
        /// 前端测试服务拉起——宿主启动时自动启动 html/tests/server.js（未监听本区段前端测试端口时）；失败不影响主功能。
        /// 端口走端口段（开发区 8069 / 部署区 8099）——不跨区复用，避免开发区代理到部署区 node（假验证）。
        /// </summary>
        private static void EnsureFrontendTestService()
        {
            try
            {
                // 已监听则跳过（多实例/重复启动保护）
                using (System.Net.Sockets.TcpClient probe = new System.Net.Sockets.TcpClient())
                {
                    IAsyncResult ar = probe.BeginConnect("127.0.0.1", _portBand.FrontendTestPort, null, null);
                    if (ar.AsyncWaitHandle.WaitOne(300))
                    {
                        probe.EndConnect(ar);
                        return;
                    }
                }
                // 定位测试目录——优先源码区（仓库根/CatHome4/html/tests——开发流程；纯净仓库 node_modules 缺失时自动部署）
                string testDir = "";
                string repoRoot = FindRepoRoot(AppContext.BaseDirectory);
                if (repoRoot.Length > 0)
                {
                    string src = System.IO.Path.Combine(repoRoot, "CatHome4", "html", "tests");
                    if (System.IO.File.Exists(System.IO.Path.Combine(src, "server.js")))
                    {
                        testDir = src;
                    }
                }
                // 回退部署区（html/tests——server.js 随 publish 复制）
                if (testDir.Length == 0)
                {
                    string deploy = System.IO.Path.Combine(AppContext.BaseDirectory, "html", "tests");
                    if (System.IO.File.Exists(System.IO.Path.Combine(deploy, "server.js")))
                    {
                        testDir = deploy;
                    }
                }
                if (testDir.Length == 0)
                {
                    return;
                }
                // node_modules 缺失 → 自动部署（npm install + playwright chromium + 启动 server.js——后台链；纯净仓库跟随 Mau 自动部署）
                if (!System.IO.Directory.Exists(System.IO.Path.Combine(testDir, "node_modules")))
                {
                    // 部署中保护——.fe-deploying 标记存在 = 上次自动部署进行中（跳过，避免重复部署冲突）
                    string deployMark = System.IO.Path.Combine(testDir, ".fe-deploying");
                    if (System.IO.File.Exists(deployMark))
                    {
                        return;
                    }
                    System.IO.File.WriteAllText(deployMark, "auto-deploy");
                    System.Diagnostics.ProcessStartInfo deployPsi = new System.Diagnostics.ProcessStartInfo
                    {
                        FileName = "cmd.exe",
                        Arguments = "/c npm install && node server.js",
                        WorkingDirectory = testDir,
                        CreateNoWindow = true,
                        UseShellExecute = false
                    };
                    // D9 修复——注入宿主 PID，server.js 守望宿主（宿主退出 → node 自退 → 目录解锁）
                    deployPsi.EnvironmentVariables["FE_TEST_PORT"] = _portBand.FrontendTestPort.ToString();
                    deployPsi.EnvironmentVariables["FE_HOST_PID"] = System.Diagnostics.Process.GetCurrentProcess().Id.ToString();
                    System.Diagnostics.Process.Start(deployPsi);
                    LogStore.Add("CatHome4", 1, "前端测试服务依赖缺失，自动部署中（npm install）：" + testDir, "CONFIG");
                    return;
                }
                System.Diagnostics.ProcessStartInfo nodePsi = new System.Diagnostics.ProcessStartInfo
                {
                    FileName = "node",
                    Arguments = "server.js",
                    WorkingDirectory = testDir,
                    CreateNoWindow = true,
                    UseShellExecute = false
                };
                // D9 修复——注入宿主 PID，server.js 守望宿主（宿主退出 → node 自退 → 目录解锁）
                nodePsi.EnvironmentVariables["FE_TEST_PORT"] = _portBand.FrontendTestPort.ToString();
                nodePsi.EnvironmentVariables["FE_HOST_PID"] = System.Diagnostics.Process.GetCurrentProcess().Id.ToString();
                System.Diagnostics.Process.Start(nodePsi);
                LogStore.Add("CatHome4", 1, "前端测试服务已启动：" + testDir, "CONFIG");
            }
            catch (Exception ex)
            {
                // 测试服务启动失败不影响宿主主功能——A260 二期：接启动降级登记面（原仅一行 L2 日志，启动健康面不可见）
                LogStore.Add("CatHome4", 2, "前端测试服务启动失败: " + ex.Message, "CONFIG");
                StartupDegradation.Report("FE_TEST_SERVICE_FAILED", StartupDegradation.KindEnv, "前端测试服务",
                    StartupDegradation.StateUnavailable, "前端测试服务拉起失败：" + ex.Message,
                    "开发期手动执行 node server.js（html/tests 目录）后重启", "S7");
            }
        }
        /// <summary>
        /// 工具组 Flow 摘要——就绪打印用（"TextCat#2 MauCat#3 ..."；未加载组省略）
        /// </summary>
        /// <returns>摘要文本</returns>
        private static string ToolGroupSummary()
        {
            string text = "";
            foreach (KeyValuePair<string, long> kv in _toolFlowIds)
            {
                if (text.Length > 0)
                {
                    text = text + " ";
                }
                text = text + kv.Key + "#" + kv.Value;
            }
            return text;
        }
        /// <summary>
        /// 加载工具组 Flow——Load dll + RegisterFlow + 存句柄表（R0.2 工具组独立注册/独立热重载/独立退役；失败警告 + 跳过——该组工具不可用）
        /// </summary>
        /// <param name="flowName">工具组 Flow 名（TextCat/MauCat/CsCat/ConfigCat——dll = FL_&lt;名&gt;.dll）</param>
        /// <param name="dllDir">语料 dll 目录</param>
        private static FlowHandle LoadToolGroup(string flowName, string dllDir)
        {
            string dllPath = Path.Combine(dllDir, "FL_" + flowName + ".dll");
            int size = PoolSizeOf(flowName);
            List<FlowHandle> loaded = new List<FlowHandle>();
            List<string> loadedNames = new List<string>();
            FlowHandle first = null;
            try
            {
                for (int k = 0; k < size; k = k + 1)
                {
                    // 实例池——首实例用基础名，其余加 ~N 后缀（同 dll、各自 ALC、各自 FlowId——OA 原子接单天然分流）
                    string instName = k == 0 ? flowName : flowName + "~" + (k + 1).ToString();
                    FlowHandle handle = FlowHandle.Load(dllPath);
                    long id = _runner.RegisterFlow(handle.Flow, instName);
                    _toolFlowHandles[instName] = handle;
                    _toolFlowIds[instName] = id;
                    loaded.Add(handle);
                    loadedNames.Add(instName);
                    if (k == 0)
                    {
                        first = handle;
                    }
                }
                if (size > 1)
                {
                    Console.WriteLine("[CMD] 实例池: " + flowName + " × " + size.ToString());
                }
                return first;
            }
            catch (Exception ex)
            {
                // 池化加载失败——回滚已加载实例（不留半池）
                for (int i = 0; i < loaded.Count; i = i + 1)
                {
                    long rid;
                    if (_toolFlowIds.TryGetValue(loadedNames[i], out rid))
                    {
                        _runner.UnregisterFlow(rid);
                    }
                    _toolFlowHandles.Remove(loadedNames[i]);
                    _toolFlowIds.Remove(loadedNames[i]);
                    try
                    {
                        loaded[i].TryUnload(1);
                    }
                    catch (Exception unloadEx)
                    {
                        LogStore.Add("CatHome4", 2, "池化回滚卸载异常: " + unloadEx.Message, "RELOAD");
                    }
                }
                Console.WriteLine("[CMD] 警告: FL_" + flowName + ".dll 加载失败——该组工具不可用（工单将超时 ERR）: " + ex.Message);
                return null;
            }
        }

        /// <summary>
        /// 工具组实例池尺寸——组名 → 实例数（缺省 1 = 单实例）。
        /// 池化组一次加载 K 个实例（独立 FlowId），由 OA 原子接单天然分流——服务型组并发吞吐。
        /// </summary>
        /// <param name="flowName">Flow 名</param>
        /// <returns>实例数（≥1）</returns>
        private static int PoolSizeOf(string flowName)
        {
            if (flowName == "LLMSimpleRequest")
            {
                return 4;
            }
            return 1;
        }

        /// <summary>
        /// 池化组兄弟实例名——基础名 base 对应 base~2..base~K；非池化组 / 已是兄弟名返回空数组。
        /// </summary>
        /// <param name="flowName">Flow 名</param>
        /// <returns>兄弟实例名数组</returns>
        private static string[] PoolSiblingNames(string flowName)
        {
            int size = PoolSizeOf(flowName);
            if (size <= 1 || flowName.IndexOf('~') >= 0)
            {
                return new string[0];
            }
            string[] names = new string[size - 1];
            for (int k = 1; k < size; k = k + 1)
            {
                names[k - 1] = flowName + "~" + (k + 1).ToString();
            }
            return names;
        }

        /// <summary>
        /// 定位语料 dll 目录——参数 -dll 指定，否则默认仓库根 public/app/Flows（统一构筑链部署区）；无仓库根回退部署区 AppContext.BaseDirectory/Flows（design-ch4-deploy §二——deploy 复制面）
        /// </summary>
        /// <param name="args">命令行参数</param>
        /// <returns>dll 目录</returns>
        private static string FindDllDir(string[] args)
        {
            for (int i = 0; i < args.Length - 1; i++)
            {
                if (args[i] == "-dll")
                {
                    return args[i + 1];
                }
            }
            // 统一构筑链默认——public/app/Flows/（design-ch4-deploy §2.1）；仓库根探测（向上找 Mau.sln）
            string root = FindRepoRoot(AppContext.BaseDirectory);
            if (root.Length > 0)
            {
                return Path.Combine(root, "public", "app", "Flows");
            }
            return Path.Combine(AppContext.BaseDirectory, "Flows");
        }
    }
}