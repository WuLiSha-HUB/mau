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
                return AdminService.CollectQqTargets(qqBotId);
            }

            /// <summary>收集所有绑定 qqbot 的猫——委托到 Program.CollectAllQqTargets</summary>
            /// <returns>全部绑定目标</returns>
            public List<QqTarget> CollectAll()
            {
                return AdminService.CollectAllQqTargets();
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
        /// 主程序入口——参数路由：无参=交互模式 / --selfcheck=启动自检 / --run "指令"=单指令脚本模式 / --script &lt;file&gt;=指令文件批量模式（P3a 热重载实测通道）/ --probe-llm &lt;目标&gt;=LLM 返回体探针（方案 B 诊断通道——不启动宿主）/ --tool-check &lt;工具名&gt; &lt;参数JSON&gt;=工具全链自检（A23：OA 走单 → 认领线 → 桥）
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
                // 注册确认——启动观测一行（CommandBus 直执面已退役：当前应为 0 个模块，OA 认领为唯一执行路径）
                string[] keyDic = _bus.GetKeyDic();
                Console.WriteLine("[CMD] CommandBus " + keyDic[0]);
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
                // [段3a] 工具全链自检通道——真实 OA 走单 → 认领线 → 桥（A23 集成面实跑通道）
                for (int i = 0; i < args.Length; i++)
                {
                    if (args[i] == "--tool-check" && i + 2 < args.Length)
                    {
                        return RunToolCheck(args[i + 1], args[i + 2]);
                    }
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
        /// <param name="headless">非交互模式（--run/--script/--selfcheck/--tool-check）</param>
        private static void Bootstrap(string dllDir, bool headless)
        {
            // [段0] 端口段解析——机器级端口空间按区划分（开发区向下 / 部署区向上——A74：两区实例可同时运行）
            _portBand = ResolvePortBand();
            // [段1] 线程守卫 + 机制服务
            ThreadGuard guard = new ThreadGuard();
            _oa = new OA(guard);
            _bus = new CommandBus(guard);
            IdAllocator ids = new IdAllocator();
            _runner = new FlowRunner(guard, _oa, _bus, ids);
            // [段2] DataBox 服务绑定——积木 TryResolve 面（OA/CommandBus/ILlmRuntime）
            DataBox.Bind<IOA>(_oa);
            DataBox.Bind<ICommandBus>(_bus);
            // 文件系统服务——file.* 积木依赖（受控根 = workspace.json 配置 roots；回收站=可写根 CatTemp/fs_recycle——P8.5 配置群）
            string dataRoot = ResolveDataRoot();
            WorkspaceConfig workspace = WorkspaceConfig.Load(Path.Combine(dataRoot, "Data", "config", "workspace.json"), dataRoot);
            DataBox.Bind<WorkspaceConfig>(workspace);
            // A260 受控根降级登记——加载期已自动兜底的事实进启动健康面（不阻断启动）
            for (int i = 0; i < workspace.LoadDegrades.Count; i = i + 1)
            {
                ReportLoadDegrade(workspace.LoadDegrades[i], "文件工具（受控根）",
                    "管理面板 → 工作区 → 修正 workspace.json 根路径", "S2");
            }
            // A153 受控根启动对账——生效根回显 + 同 path 多根检出（文本在 LogStore 就绪后于段3b 落盘）
            bool rootsConflict = false;
            string rootsNote = RootsAuditNote(workspace, out rootsConflict);
            // mau 仓库根注入——受控根 id=mau → MauGroupBuilder/BrickIndex 静态入口（BRIK-MAU 工具 + 翻译器积木索引解析优先注入值）
            for (int i = 0; i < workspace.Roots.Length; i = i + 1)
            {
                if (string.Equals(workspace.Roots[i].Id, "mau", StringComparison.OrdinalIgnoreCase))
                {
                    Mau.Development.MauGroupBuilder.SetWorkspaceRoot(workspace.Roots[i].Path);
                    Mau.Translator.BrickIndex.SetWorkspaceRoot(workspace.Roots[i].Path);
                    break;
                }
            }
            // 兜底实例 = 常驻根子集（workspace/runtime）——猫级实例解析失败时只剩 workspace 可用（失败默认收紧，不回落全量）
            List<WorkspaceConfig.RootEntry> fallbackRoots = new List<WorkspaceConfig.RootEntry>();
            for (int i = 0; i < workspace.Roots.Length; i = i + 1)
            {
                WorkspaceConfig.RootEntry entry = workspace.Roots[i];
                if (string.Equals(entry.Id, "workspace", StringComparison.OrdinalIgnoreCase) || string.Equals(entry.Id, "runtime", StringComparison.OrdinalIgnoreCase))
                {
                    fallbackRoots.Add(entry);
                }
            }
            DataBox.Bind<FileSystemService>(new FileSystemService(fallbackRoots.ToArray(), Path.Combine(WorkspaceRecycleRoot(workspace, dataRoot), "CatTemp", "fs_recycle")));
            string configDir = Path.Combine(dataRoot, "Data", "config");
            // P8.5 配置群 schema——schema.json 元声明（默认值/敏感/可写/空值语义——/api/v1/config 输出面）
            // §4.1 schema 同步（模板为准：不一致 → .bak 备份 + 整体覆盖）→ §4.2 schema 驱动段注册（宿主零硬编码段清单）
            // 两条结论在 LogStore 就绪后补记审计（启动期只有 Console——落盘补记见段3b）
            string schemaTemplatePath;
            string schemaSyncNote = EnsureSchemaSync(configDir, out schemaTemplatePath);
            ConfigSchema schema = ConfigSchema.Load(Path.Combine(configDir, "schema.json"));
            // A260 schema 降级登记——损坏已回退为空 schema（配置面只读可见），事实进启动健康面
            if (schema.LoadDegrade.Length > 0)
            {
                ReportLoadDegrade(schema.LoadDegrade, "配置面（schema）",
                    "打开配置目录替换 schema.json（坏档已备份 .bad-<ts>）", "S1");
            }
            // A152 派生段声明通道——模板路径注入（声明时模板与运行副本同步写，避免启动期模板同步覆盖）
            schema.TemplatePath = schemaTemplatePath;
            DataBox.Bind<ConfigSchema>(schema);
            ConfigStore llmConfig = ConfigStore.Load(Path.Combine(configDir, "llm.cfg"));
            string segmentNote = RegisterSchemaSegments(llmConfig, schema, configDir);
            DataBox.Bind<ConfigStore>(llmConfig);
            // M1a LLM API 配置池——llm-api.json 明文（零 key）+ Data/secrets 独立秘密文件（CH3 同构迁移；环境变量首次导入兜底）
            CH_LlmApiConfigStore apiConfigStore = new CH_LlmApiConfigStore(
                Path.Combine(dataRoot, "Data", "config"),
                Path.Combine(dataRoot, "Data", "secrets"));
            DataBox.Bind<CH_LlmApiConfigStore>(apiConfigStore);
            ReportStorageDegrade(apiConfigStore.StorageWritable, apiConfigStore.StorageError, "配置面（LLM 端点）");
            // M3 管理面静态引用——catcfg.apply 重建 Runtime 消费（S4 迁 Admin 域——Configure 注入）
            // R2.3 QQ Bot 配置池——qqbot.json 明文（零 secret）+ Data/secrets 独立秘密文件（对齐 LLM API 池 M1 模式）
            CH_QqBotConfigStore qqBotStore = new CH_QqBotConfigStore(
                Path.Combine(dataRoot, "Data", "config"),
                Path.Combine(dataRoot, "Data", "secrets"));
            DataBox.Bind<CH_QqBotConfigStore>(qqBotStore);
            ReportStorageDegrade(qqBotStore.StorageWritable, qqBotStore.StorageError, "QQ 通道（Bot 配置）");
            // A244 外观配置池——appearance.json 明文（无密钥面；每猫 appearanceId 引用 + 唯一默认项）
            CH_AppearanceConfigStore appearanceStore = new CH_AppearanceConfigStore(
                Path.Combine(dataRoot, "Data", "config"));
            DataBox.Bind<CH_AppearanceConfigStore>(appearanceStore);
            ReportStorageDegrade(appearanceStore.StorageWritable, appearanceStore.StorageError, "外观配置池");
            // M1 语料面 Runtime——默认端点语义（QuickCat 工单 llm.stream 消费面；Guid.Empty=每次调用实时解析默认配置）
            // 空配置池无默认端点 → 启动失败（不做静默回退——必须先配置端点后才能运行）
            _llmRuntime = new DeepSeekLlmRuntime(apiConfigStore, Guid.Empty, llmConfig);
            DataBox.Bind<ILlmRuntime>(_llmRuntime);
            // P8 三期——Roslyn cs.* 编码工具域（MauRoslynBridge——受控根=可写根子集，只读知识根不参与项目扫描；磁盘权威快照 + 三态缓存）
            DataBox.Bind<ICSharpBridge>(new MauRoslynBridge(WritableRootPaths(workspace)));
            // R2.2 图像识别服务——DeepSeek vision 模型（vision.api_config_id 引用 LLM 池配置；未配置=不可用）
            DataBox.Bind<IVisionService>(new Mau.Providers.DeepSeekVisionService(apiConfigStore, llmConfig));
            // PsCat PowerShell 执行服务——EncodedCommand 免转义 + UTF-8 内建 + 写文件拦截 + 超时进程树杀（PsService）
            DataBox.Bind<IPsService>(new PsService());
            // py 执行与 py 服务进程管理——解释器读配置 py.python_path；句柄归基座持有（design-ch4-pytools §二）
            // 运行期目录注入——锚文件落点（启动前自愈 + 服务锚；§5.2 清理第③层）
            Mau.Runtime.PyService pySvc = new Mau.Runtime.PyService(WritableRootPaths(workspace));
            pySvc.Configure(System.IO.Path.Combine(dataRoot, "Data", "runtime"));
            DataBox.Bind<IPyService>(pySvc);
            // A110 浏览器服务——系统 Edge headless + 裸 CDP（零第三方依赖 · 每猫独立 profile · timeback 域限定）
            CdpBrowserService browserSvc = new CdpBrowserService(dataRoot);
            DataBox.Bind<IBrowserService>(browserSvc);
            DataBox.Bind<IBrowserLifecycle>(browserSvc);
            // 网页资产下载服务——全局单一下载器（HttpClient + 后台 Task；任务态落盘 + 跨重启续传——design-ch4-webfetch）
            WebFetchService webFetchSvc = new WebFetchService();
            DataBox.Bind<IWebFetchService>(webFetchSvc);
            // 宿主指令服务——工具面触达内核指令族（host.command 积木 → IHostCommandService；与 CLI/HTTP 面板同源单内核）
            DataBox.Bind<IHostCommandService>(new HostCommandService());
            // A239 音频播放服务——per-Key 串行 + 组间并发（NAudio.Wasapi；design-mau-audio §三）
            DataBox.Bind<Mau.Contracts.IAudioService>(new Mau.Audio.AudioService());
            AuditStore audit = new AuditStore();
            AuditStore.Default = audit;
            _runner.Audit = audit;
            // [段3] 统一观测运行目录——O 系列：Data/runs/<ts>/ 四文件（log_all.txt/oa_all.txt/frame.txt/err_all.txt——design-ch4-observe §三）
            string sessionRunId = DateTime.Now.ToString("yyyyMMdd_HHmmss");
            string runDir = Path.Combine(dataRoot, "Data", "runs", sessionRunId);
            LogStore.ConfigureRuns(runDir);
            // A260 观测落点降级——runs 目录不可写时换早期 sink 落点重试（仍失败则仅内存运行 + 登记）
            if (LogStore.LastConfigureError.Length > 0)
            {
                string obsError = LogStore.LastConfigureError;
                string fallbackRunDir = "";
                if (BootLog.SinkDir.Length > 0 && !string.Equals(BootLog.SinkDir, runDir, StringComparison.OrdinalIgnoreCase))
                {
                    fallbackRunDir = BootLog.SinkDir;
                }
                if (fallbackRunDir.Length > 0)
                {
                    LogStore.ConfigureRuns(fallbackRunDir);
                }
                if (LogStore.LastConfigureError.Length > 0)
                {
                    StartupDegradation.Report("OBS_UNWRITABLE", StartupDegradation.KindEnv, "观测日志",
                        StartupDegradation.StateEmpty, "运行目录不可写：" + runDir + "（" + obsError + "）",
                        "检查数据根权限或改用可写位置（CH4_DATA_ROOT）", "S3");
                }
                else
                {
                    StartupDegradation.Report("OBS_FALLBACK", StartupDegradation.KindEnv, "观测日志",
                        StartupDegradation.StateFallback, "运行目录不可写，已回落：" + fallbackRunDir,
                        "检查数据根权限或改用可写位置（CH4_DATA_ROOT）", "S3");
                    runDir = fallbackRunDir;
                }
            }
            // A260 帧流落点容错——写不了帧流不影响宿主（观测降级为仅 log/oa/err）
            try
            {
                FrameStore.Configure(Path.Combine(runDir, "frame.txt"));
            }
            catch (Exception ex)
            {
                StartupDegradation.Report("FRAME_STORE_FAILED", StartupDegradation.KindEnv, "观测帧流",
                    StartupDegradation.StateEmpty, "帧流文件不可写：" + Path.Combine(runDir, "frame.txt") + " | " + ex.Message,
                    "检查运行目录权限后重启", "S3");
            }
            // [段3a] Console 订阅——Log 同构行输出窗口（O4：过程行不再直打——统一观测）
            LogStore.ConsoleSink = delegate (string line)
            {
                Console.WriteLine(line);
            };
            // A260 早期诊断回灌——sink 条目按序并入观测时间线（来源标注 BOOT；sink 内存缓冲随即清空）
            string[] bootLines = BootLog.Drain();
            for (int i = 0; i < bootLines.Length; i = i + 1)
            {
                LogStore.Add("CatHome4", 1, bootLines[i], "BOOT");
            }
            // [段3b] 配置面启动审计补记——schema 同步与段注册结论 + 受控根对账（启动期仅 Console 输出，此处落盘可回溯）
            LogStore.Add("CatHome4", 1, "config.boot | " + schemaSyncNote + " | " + segmentNote, "CONFIG");
            // A153 受控根对账落盘——同 path 多根走 L2（WARN）出声
            if (rootsConflict)
            {
                LogStore.Add("CatHome4", 2, "roots.boot | " + rootsNote, "WORKSPACE");
            }
            else
            {
                LogStore.Add("CatHome4", 1, "roots.boot | " + rootsNote, "WORKSPACE");
            }
            // 根不可达告警——加载期跳过的非常驻根在此出声（L2）：面板可修正路径后重启恢复
            for (int warnIdx = 0; warnIdx < workspace.LoadWarnings.Count; warnIdx = warnIdx + 1)
            {
                LogStore.Add("CatHome4", 2, "roots.boot | " + workspace.LoadWarnings[warnIdx], "WORKSPACE");
            }
            // [段4] 语料加载——扫描 Flows/FL_*.dll 统一装配（design-ch4-flow-scan §3.2：QuickCat + 全部工具组 Flow）
            // 扫描化：文件名去 FL_ 前缀得 Flow 名（dll 名 = 组名——QuickCat 与工具组一视同仁）
            // 容错：加载失败 → 警告 + 跳过注册（该组工具工单无人认领 → 超时诚实 ERR——不再宿主直执）
            // 路由表数据化 + 工具定义数据化：加载后经 IFlow.GetMetaJson/GetToolsJson 读自曝 → 统一工具池（design-ch4-tools-pool §六）
            // 工具组来源（组名 + Flow——A107：定义自曝失败时必须报得出是哪一组）
            List<ToolGroupSource> poolGroups = new List<ToolGroupSource>();
            // A260 语料目录容错——目录缺失 / 扫描失败 / 零 dll 一律降级为「工具面不可用」，宿主照常启动
            string[] flowDlls = new string[0];
            if (!Directory.Exists(dllDir))
            {
                StartupDegradation.Report("FLOWS_DIR_MISSING", StartupDegradation.KindEnv, "工具面（全部 Flow）",
                    StartupDegradation.StateEmpty, "语料 dll 目录不存在：" + dllDir,
                    "运行 mau-setup（prepare）重建产物，或用 -dll 指定正确目录", "S4");
            }
            else
            {
                try
                {
                    flowDlls = Directory.GetFiles(dllDir, "FL_*.dll");
                }
                catch (Exception ex)
                {
                    StartupDegradation.Report("FLOWS_SCAN_FAILED", StartupDegradation.KindEnv, "工具面（全部 Flow）",
                        StartupDegradation.StateEmpty, "扫描语料目录失败：" + dllDir + " | " + ex.Message,
                        "检查目录权限后重启，或重新 prepare", "S4");
                }
                if (flowDlls.Length == 0)
                {
                    StartupDegradation.Report("FLOWS_EMPTY", StartupDegradation.KindEnv, "工具面（全部 Flow）",
                        StartupDegradation.StateEmpty, "语料目录内无 FL_*.dll：" + dllDir,
                        "运行 mau-setup（prepare）重建全部 Flow", "S4");
                }
            }
            Array.Sort(flowDlls, StringComparer.OrdinalIgnoreCase);
            for (int f = 0; f < flowDlls.Length; f = f + 1)
            {
                string flowName = Path.GetFileNameWithoutExtension(flowDlls[f]);
                if (flowName.StartsWith("FL_", StringComparison.Ordinal))
                {
                    flowName = flowName.Substring(3);
                }
                if (flowName.Length == 0)
                {
                    continue;
                }
                {
                    FlowHandle handle = LoadToolGroup(flowName, dllDir);
                    if (handle != null)
                    {
                        ToolGroupSource groupSource = new ToolGroupSource();
                        groupSource.Name = flowName;
                        groupSource.Flow = handle.Flow;
                        poolGroups.Add(groupSource);
                    }
                }
            }
            // 内置工具定义源——宿主内建小表（本质也是 BRIK，只是内置：Note/time/random/info/host-*）
            ToolPool.RebuildAll(poolGroups, BuildBuiltinToolsJson());
            // A107——定义缺陷出声：逐条 L3 落盘（判例：整组静默消失无人知）
            LogToolPoolDefects();
            // A127——执行序对账：池内工具须在 ToolOrderTable 全量登记（未登记者静默落默认档 = 隐性缺口，此处出声）
            LogToolOrderAccounting();
            // A210——域类型白名单对账：白名单里的名字须在池内（拼写漂移 / 工具退役漏改 = 域内静默缺件，此处出声）
            LogTimebackProfileAccounting();
            // [段5] 会话面——上下文 + 前文恢复 + 工具定义 + 默认会话注册（P9.1 会话对象化：ChatSession 承载状态机——design-llm-streaming §六）
            ChatContext chatCtx = new ChatContext();
            // S1 ChatBridge 化——会话协调实例（注入提示词构建委托——CatCfg 域静态面 BuildInjectPrompt）
            _chatBridge = new ChatBridge(BuildInjectPrompt);
            // S4 Admin 域接线——依赖注入（管理 API 处理器 + cat.* 指令族迁入 CatHome4.Admin）
            AdminService.Configure(_chatBridge, _oa, dataRoot, _mainThreadId, ExecuteTool, ObserveService.BuildSnapshotJson, new HtmlRootProvider(), apiConfigStore, qqBotStore, appearanceStore, llmConfig, _portBand);
            AdminService.BuildEnvInfoProvider = BuildEnvInfo;
            // 加载包数据面接线——pack 内置工具（池定义与猫挂载解析归 Admin 域；Core 零配置面依赖）
            ChatSession.PackPayloadProvider = AdminService.BuildPackPayload;
            // 授权面实时查询接线——工具调用判定按当前 cat.cfg 名单实时解析（design-ch4-tools §三·十一：注入面=提示，授权面=真相）
            ChatSession.AuthorizedToolNamesProvider = AdminService.ResolveCatAuthorizedToolNames;
            // 轮末总结 LLM 配置接线——catKey → [endpoint, apikey, model]（LLMSimpleRequest 服务工单参数；roundsummary）
            ChatSession.RoundSummaryLlmProvider = AdminService.ResolveRoundSummaryLlm;
            // 轮末总结播报接线——catKey → [启用标志, 音色槽位值]（A247：总结自动出声）
            ChatSession.SummaryAudioProvider = AdminService.ResolveSummaryAudio;
            // 延迟队列落盘接线——Data/runtime/delays.json（跨宿主重启保留；design-ch4-delay §七）
            DelayQueue.Configure(Path.Combine(dataRoot, "Data", "runtime", "delays.json"));
            DelayQueue.Load();
            // 待处理消息队列落盘接线——Data/runtime/pending_<catKey>.json（A232：入队即物化 · 跨宿主重启保留；须先于会话创建）
            ChatSession.ConfigurePendingStore(Path.Combine(dataRoot, "Data", "runtime"));
            // 网页资产下载接线——任务表落盘 + 产物目录（workspace 根下 Downloads）+ 完成注入回调（design-ch4-webfetch）
            string downloadsDir = "";
            for (int i = 0; i < workspace.Roots.Length; i = i + 1)
            {
                if (string.Equals(workspace.Roots[i].Id, "workspace", StringComparison.OrdinalIgnoreCase))
                {
                    downloadsDir = Path.Combine(workspace.Roots[i].Path, "Downloads");
                    break;
                }
            }
            webFetchSvc.Configure(
                Path.Combine(dataRoot, "Data", "runtime", "downloads.json"),
                downloadsDir,
                delegate (string downloadCatKey, string downloadText)
                {
                    return _chatBridge.PostSystemMessage(downloadCatKey, "systemauto", downloadText) == "OK";
                });
            // A260 下载器启动容错——任务表损坏 / 目录不可用时通道降级，不阻断宿主
            try
            {
                webFetchSvc.Start();
            }
            catch (Exception ex)
            {
                StartupDegradation.Report("WEBFETCH_START_FAILED", StartupDegradation.KindExternal, "网页下载器",
                    StartupDegradation.StateUnavailable, "下载器启动失败：" + ex.Message,
                    "检查 Data/runtime 权限后重启（在途任务需重新登记）", "S7");
            }
            // py 服务自启——follow=true 的服务随宿主启动拉起（design-ch4-pytools §5.2；接线 2026-10-15）
            {
                IPyService pyAutostart;
                if (DataBox.TryResolve<IPyService>(out pyAutostart) && pyAutostart != null)
                {
                    LogStore.Add("CatHome4", 1, "py.boot | " + pyAutostart.Autostart(), "CONFIG");
                }
            }
            // timeback 归档落点接线——Data/runtime/timeback（全局计数 + 每次回收一个作用域文件；A104）
            ChatSession.TimebackArchiveDirProvider = AdminService.ResolveTimebackArchiveDir;
            // A201 会话元数据落点接线——Data/sessions/<id>/<id>.session.json（会话自身参数持久化；design-ch4-protocol §十三）
            ChatSession.SessionMetaPathProvider = AdminService.ResolveSessionMetaPath;
            // A201 显示名解析接线——session.new 时按 cat.cfg 现值刷新（新会话取最新）
            ChatSession.DisplayNameProvider = AdminService.ResolveCatDisplayName;
            // timeback 归档现场记录接线——回收时采集本猫 info 快照（归档文件第二行）
            ChatSession.TimebackInfoProvider = BuildEnvInfo;
            AdminService.NotifyBalloon = Program.NotifyBalloon;
            // S5 Observe 域接线——依赖注入（观测面迁入 CatHome4.Observe）
            ObserveService.Configure(_oa, _chatBridge, _toolFlowHandles, _toolFlowIds, _runner, null);
            // A260 启动健康段提供者——快照 boot 段与日志 / 报告 / 弹窗同源
            ObserveService.BootSinkProvider = delegate () { return BootLog.SinkDir; };
            ObserveService.BootReportPathProvider = delegate () { return StartupReportPath; };
            SessionStore chatStore = new SessionStore(Path.Combine(dataRoot, "Data", "sessions", "majordomo", "majordomo.jsonl"));
            LlmMessage[] restored;
            // M1 默认猫——API 配置身份从 sessions/majordomo/cat.cfg 读取；缺省 Guid.Empty=默认端点语义（每次调用实时解析）
            // M2 默认猫同构——persona/toolNames/injectList 三字段同迁（每猫配置完全独立；注入源从 workspace.inject 切到 cat.cfg）
            Guid defaultApiConfigId = Guid.Empty;
            // R0.2 工具注册表——统一工具池灌入（Flow 自曝工具定义 + 内置定义源；替代 BuildToolSpecs 人工表）
            // 🔴 必须先于 EnsureMajordomoCfg——SaveCatCfgData 内 ValidateToolNames 依赖注册表（空表 → 模板工具全被过滤）
            ToolRegistry.Init(ToolPool.BuildSpecs(), ToolPool.BuildOwnerFlowMap(), ToolPool.BuildPrivilegedMap());
            // 工具池摘要——启动观测（工具总数 + 组别分布；design-ch4-tools-pool §六）
            string poolSummary = "工具池已聚合：" + ToolPool.AllNames().Length.ToString() + " 个工具 / " + ToolPool.AllGroups().Length.ToString() + " 个组";
            if (ToolPool.Defects().Length > 0)
            {
                poolSummary = poolSummary + "｜定义缺陷 " + ToolPool.Defects().Length.ToString() + " 组（详见 err 面）";
            }
            LogStore.Add("CatHome4", 1, poolSummary, "CONFIG");

            // 默认猫 cat.cfg 补建——缺失时按全局默认模板创建（新用户无 cfg 必然态；运行时缺省回退 → 启动落盘）
            AdminService.EnsureMajordomoCfg();
            AdminService.CatCfgData defaultCfg = AdminService.LoadCatCfg(Path.Combine(dataRoot, "Data", "sessions", "majordomo", "cat.cfg"));
            _chatBridge.DefaultPersona = "";
            _chatBridge.DefaultInjectList = new string[0];
            string defaultToolNames = "";
            if (defaultCfg != null)
            {
                if (defaultCfg.ApiConfigId != null && defaultCfg.ApiConfigId.Length > 0)
                {
                    Guid parsed;
                    if (Guid.TryParse(defaultCfg.ApiConfigId, out parsed) && parsed != Guid.Empty)
                    {
                        defaultApiConfigId = parsed;
                    }
                }
                if (defaultCfg.Persona != null)
                {
                    _chatBridge.DefaultPersona = defaultCfg.Persona;
                }
                if (defaultCfg.InjectList != null)
                {
                    _chatBridge.DefaultInjectList = defaultCfg.InjectList;
                }
                if (defaultCfg.ToolNames != null)
                {
                    defaultToolNames = defaultCfg.ToolNames;
                }
            }
            // M2c 声明面裁剪——读时比对（非法名过滤/全空全量保底）；session.new 重注入复用
            _chatBridge.DefaultToolSpecs = AdminService.FilterToolSpecs(AdminService.ResolveToolNames(defaultToolNames, true), true);
            // 授权面原始串——实时解析输入（design-ch4-tools §三·十一）
            _chatBridge.DefaultToolNames = defaultToolNames;
            AdminService._defaultApiConfigId = defaultApiConfigId;
            AdminService._defaultReasoningEffort = defaultCfg != null ? AdminService.NormalizeReasoningEffort(defaultCfg.ReasoningEffort) : "";
            // R2.3 默认猫 qqbot 配置——majordomo cat.cfg 读取（缺省未绑定/禁用）
            Guid defaultQqBotId = Guid.Empty;
            bool defaultQqBotEnable = false;
            if (defaultCfg != null)
            {
                if (defaultCfg.QqBotId != null && defaultCfg.QqBotId.Length > 0)
                {
                    Guid parsed;
                    if (Guid.TryParse(defaultCfg.QqBotId, out parsed) && parsed != Guid.Empty)
                    {
                        defaultQqBotId = parsed;
                    }
                }
                defaultQqBotEnable = defaultCfg.QqBotEnable;
            }
            _chatBridge.DefaultQqBotId = defaultQqBotId;
            _chatBridge.DefaultQqBotEnable = defaultQqBotEnable;
            if (chatStore.TryLoad(out restored))
            {
                chatCtx.ReplaceMessages(restored);
                Console.WriteLine("[CMD] 会话前文恢复: " + restored.Length.ToString() + " 条消息（有前文——不注入）");
            }
            else
            {
                // 无前文 = 隐式新会话——按 cat.cfg injectList 清单注入（M2d：不再走全局 workspace.inject）
                string injectPrompt = _chatBridge.BuildPrompt(workspace, _chatBridge.DefaultToolSpecs, _chatBridge.DefaultPersona, _chatBridge.DefaultInjectList);
                chatCtx.SetSystemPrompt(injectPrompt);
                Console.WriteLine("[CMD] 新会话注入: " + _chatBridge.DefaultInjectList.Length.ToString() + " 个文件");
            }
            // 会话标识 ≡ 猫 key（唯一标识——不再有独立"会话身份"层）；首启 / 外部改写时校正并回写
            string defaultSessionId = chatStore.SessionId;
            if (!string.Equals(defaultSessionId, "majordomo", StringComparison.Ordinal))
            {
                defaultSessionId = "majordomo";
                chatStore.SessionId = defaultSessionId;
                chatStore.Rewrite(chatCtx.GetMessages());
                Console.WriteLine("[CMD] 会话标识对齐猫 key: " + defaultSessionId);
            }
            // P9.1 会话对象化——默认会话注册（工具表就位后构造——ChatSession 状态机承载面；M2c 声明面按猫裁剪）
            SessionViewStore chatViewStore = new SessionViewStore(Path.Combine(dataRoot, "Data", "sessions", "majordomo", "majordomo.view.jsonl"));
            // A221 工具面解析——持久化快照优先（会话工具头与 system prompt 同命；重启不改面）
            ToolSpec[] defaultTools = ChatSession.ResolveSessionTools(defaultSessionId, _chatBridge.DefaultToolSpecs);
            _chatBridge.DefaultSession = new ChatSession(defaultSessionId, "majordomo", chatCtx, chatStore,
                new DeepSeekLlmRuntime(apiConfigStore, defaultApiConfigId, llmConfig, AdminService._defaultReasoningEffort), _oa, defaultTools, ExecuteTool, chatViewStore);
            // M4e 猫级白名单——默认猫启用根（cat.cfg enabledRoots；缺省全量）+ 工具执行猫上下文
            _chatBridge.DefaultSession.SetCatKey("majordomo");
            _chatBridge.DefaultSession.SetUserInjectIdleOnly(defaultCfg != null && defaultCfg.UserInjectIdleOnly);
            AdminService.ApplyCatRoots("majordomo");
            _chatBridge.DefaultSession.AttachEnvInfo(() => BuildEnvInfo());
            _chatBridge.DefaultSession.AttachRoundNotify(Program.NotifyBalloon);
            _chatBridge.RegisterSession(_chatBridge.DefaultSession);
            // F4 视图——从真实前文重建视图层（恢复/注入后——真实前文绝对可用）
            _chatBridge.DefaultSession.LoadView();
            // A201 会话元数据——启动恢复（实例 ID / 创建时刻 / 两级 token 累计 + A202 请求边界态；文件缺失走首建）
            _chatBridge.DefaultSession.LoadMeta();
            // LLM 注入探测——默认端点解析（无默认端点 = 未注入；启动失败语义由语料面消费时暴露）
            CH_LlmApiConfig llmProbeConfig = apiConfigStore.ResolveDefault();
            if (apiConfigStore.LastLoadError.Length > 0)
            {
                StartupDegradation.Report("CFG_CORRUPT", StartupDegradation.KindData, "LLM 端点池",
                    StartupDegradation.StateEmpty, "llm-api.json 读取失败：" + apiConfigStore.LastLoadError,
                    "管理面板 → LLM 端点池 → 重建端点（坏档已备份 .broken-<ts>）", "S5");
            }
            string llmKeyProbe = "";
            if (llmProbeConfig != null)
            {
                llmKeyProbe = apiConfigStore.GetSecret(llmProbeConfig.ApiConfigId);
            }
            string llmState;
            if (llmKeyProbe.Length == 0)
            {
                llmState = "未注入(ERR路径)";
            }
            else
            {
                llmState = "已注入";
            }
            // A41 启动自检——默认端点存在性 + 缓存隔离声明合法性（失败必须可见：缺失/非法一律记 WARN，不静默）
            if (llmProbeConfig == null)
            {
                LogStore.Add("CatHome4", 2, "启动自检：LLM 配置池无默认端点（IsDefault 未设置）——未显式绑定端点的会话无法发起请求", "CONFIG");
            }
            else if (llmKeyProbe.Length == 0)
            {
                LogStore.Add("CatHome4", 2, "启动自检：默认端点「" + llmProbeConfig.DisplayName + "」缺少 API Key（secrets 未注入）", "CONFIG");
            }
            string cacheIsolationProbe = llmConfig.Get("llm.cache_isolation", "");
            if (cacheIsolationProbe.Length > 0)
            {
                string normalizedIsolation = cacheIsolationProbe.Trim().ToLowerInvariant();
                if (normalizedIsolation != "cat" && normalizedIsolation != "session" && normalizedIsolation != "off")
                {
                    LogStore.Add("CatHome4", 2, "启动自检：llm.cache_isolation 取值非法（" + cacheIsolationProbe + "）——运行时回落 cat；合法值 cat/session/off", "CONFIG");
                }
            }
            // P8.5 观测落点——workspace.load 审计 + 全局盒 roots 摘要（design §七：当前受控根永远可从观测层看到）
            System.Text.StringBuilder rootSummary = new System.Text.StringBuilder();
            for (int i = 0; i < workspace.Roots.Length; i++)
            {
                if (i > 0)
                {
                    rootSummary.Append("|");
                }
                rootSummary.Append(workspace.Roots[i].Id);
                if (workspace.Roots[i].Writable)
                {
                    rootSummary.Append("(rw)");
                }
                else
                {
                    rootSummary.Append("(ro)");
                }
            }
            DataBox.Set<string>("global", "workspace.roots", rootSummary.ToString());
            LogStore.Add("CatHome4", 1, "工作区已加载：" + workspace.Roots.Length.ToString() + " 个根、" + workspace.Inject.Length.ToString() + " 条注入（" + rootSummary.ToString() + "）", "CONFIG");
            Console.WriteLine("[CMD] 就绪 | Flows: " + ToolGroupSummary() + " | LLM: " + llmState + " | 帧节流 " + FrameTargetMs + "ms");
            // [段6] HTTP 外观层启动——P6 最小闭环（协议 design-ch4-protocol.md；快照回调 + 指令投递回调注入）
            // P9.3 多实例化签名——sessionId 归属默认会话；catsBuilder 多猫列表（管理页签数据源）；主端口服务 index.html
            // F2.1/F2.2——主端口纯管理面板（index.html）；majordomo 对话走独立端口（chat.html，与多猫同构）
            // 🔴 非交互模式（--run/--script/--selfcheck/--tool-check）整段跳过——探针通道单指令直执 + 进程自退，
            //    不启动外观层与常驻附属（多猫 / 前端测试服务 / QQ 桥）：端口零占用，与常驻实例彻底解耦（A74）
            if (headless)
            {
                Console.WriteLine("[CMD] 非交互模式——外观层与常驻附属未启动（端口零占用）");
                StartupDegradeEntry[] headlessEntries = StartupDegradation.Snapshot();
                for (int i = 0; i < headlessEntries.Length; i = i + 1)
                {
                    LogStore.Add("CatHome4", 2, "boot.degrade | " + headlessEntries[i].Code + " | "
                        + headlessEntries[i].Ability + " | " + headlessEntries[i].Detail, "BOOT");
                }
                Console.WriteLine("[CMD] 启动降级 " + StartupDegradation.Count.ToString() + " 项——详见 startup_report.json");
                WriteStartupReport("non-interactive");
                return;
            }
            HttpHostOptions mainHostOptions = new HttpHostOptions
            {
                Port = ResolveHttpPort(llmConfig),
                FrontendTestPort = _portBand.FrontendTestPort,
                SessionId = _chatBridge.DefaultSession.Id,
                SnapshotBuilder = ObserveService.BuildSnapshotJson,
                Dispatcher = DispatchCommand,
                EnvelopeBuilder = AdminService.MakeChatEnvelopeBuilder(_chatBridge.DefaultSession),
                FrameBuilder = ObserveService.BuildCompactFrameJson,
                CatsBuilder = AdminService.BuildCatsJson,
                PatchBuilder = ObserveService.BuildPatchJson,
                ServeChatPage = false,
                RouteRegistrar = AdminService.RegisterAdminRoutes,
                DepsRefresh = DependencyProbe.Refresh,
                LlmProbe = LlmEndpointProbe.ProbeAsync,
                HtmlRootProvider = new HtmlRootProvider()
            };
            // A260 端口回退——首选端口被占时在端口段内回退；段内全占 = 无外观层（宿主继续运行）
            int mainPort = 0;
            _httpHost = StartMainHostWithFallback(mainHostOptions, mainHostOptions.Port, out mainPort);
            // F2.2 majordomo 特殊会话——独立端口 serve chat.html + 强制自启（DefaultSession 事件推送绑独立端口 host）
            AdminService.StartMajorHost();
            // [段6b] 启动扫描——sessions/*/cat.cfg 中 running 猫拉起（主 HTTP 就位后——每猫 HttpHost 独立实例）
            AdminService.LoadCatsOnBoot();
            if (_httpHost != null)
            {
                Console.WriteLine("[CMD] HTTP 外观层就绪: http://127.0.0.1:" + _httpHost.Port.ToString() + " | Majordomo 对话: http://127.0.0.1:" + AdminService.MajorPort.ToString());
            }
            else
            {
                Console.WriteLine("[CMD] HTTP 外观层未启动（端口段耗尽）——宿主以无外观层形态运行");
            }
            // [段6c] 前端测试服务拉起——开发流程（改前端 → 跑测试 → 刷新生效）；未监听则启动 node server.js
            EnsureFrontendTestService();
            // R2.3 QQ 管理器启动——扫描 Bot 池建立全部连接（附属功能组件；Bot 池空 = 零连接静默）
            // S3 解耦——注入绑定目标收集面（默认猫 + 多猫注册表实现）
            QQBotService.SetCollector(new QqTargetCollector());
            QQBotService.SetFileCacheRoot(Path.Combine(dataRoot, "Data", "qqbot-files"));
            QQBotService.Start();
            // A260 启动健康面收口——报告落盘 + 独立提示窗（有降级才弹；关窗不改运行态）
            // 配置池读取校验：QQ / 外观池在启动期未必被消费，此处主动读一次，让损坏在启动面可见
            if (qqBotStore.LastLoadError.Length > 0)
            {
                StartupDegradation.Report("CFG_CORRUPT", StartupDegradation.KindData, "QQ 通道（Bot 配置）",
                    StartupDegradation.StateEmpty, "qqbot.json 读取失败：" + qqBotStore.LastLoadError,
                    "管理面板 → QQ Bot → 重建配置（坏档已备份 .broken-<ts>）", "S7");
            }
            appearanceStore.GetAll();
            if (appearanceStore.LastLoadError.Length > 0)
            {
                StartupDegradation.Report("CFG_CORRUPT", StartupDegradation.KindData, "外观配置池",
                    StartupDegradation.StateEmpty, "appearance.json 读取失败：" + appearanceStore.LastLoadError,
                    "管理面板 → 外观 → 重建配置（坏档已备份 .broken-<ts>）", "S7");
            }
            WriteStartupReport("interactive");
            if (StartupDegradation.HasAny)
            {
                // 逐条降级出声（err 面第一入口）+ 摘要行——启动健康面的日志出口
                StartupDegradeEntry[] bootEntries = StartupDegradation.Snapshot();
                for (int i = 0; i < bootEntries.Length; i = i + 1)
                {
                    LogStore.Add("CatHome4", 2, "boot.degrade | " + bootEntries[i].Code + " | "
                        + bootEntries[i].Ability + " | " + bootEntries[i].Detail, "BOOT");
                }
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
                CH_LlmApiConfig cfg = AdminService.ResolveEffectiveApiConfig(catKey, out followDefault);
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
            int chatPort = AdminService.ResolveCatPort(catKey);
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
                WorkspaceConfig.RootEntry[] entries = AdminService.ResolveCatRootEntries(catKey);
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
            ChatSession session = AdminService.FindCatSession(catKey);
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
            string qqbotShow = AdminService.BuildCatQqBotInfo(catKey);
            if (qqbotShow.Length > 0)
            {
                Dictionary<string, object> qqbotBlock = new Dictionary<string, object>();
                qqbotBlock["usage"] = qqbotShow;
                info["qqbot"] = qqbotBlock;
            }
            // [段9] tools_drift——工具面漂移（会话注入面 vs 当前授权集；仅不一致时输出——design-ch4-tools §三·十一）
            // added 附用法摘要（name/desc/args——注册表声明派生，冷启动猫不新会话即可上手；removed 只给名字）
            if (session != null)
            {
                string[] declaredNames = session.DeclaredToolNames;
                string[] authorizedNames = AdminService.ResolveCatAuthorizedToolNames(catKey);
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
            string timebackDir = AdminService.ResolveTimebackArchiveDir(catKey);
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
            return JsonSerializer.Serialize(info, AdminService.InfoJsonOptions);
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
                // 测试服务启动失败不影响宿主主功能
                LogStore.Add("CatHome4", 2, "前端测试服务启动失败: " + ex.Message, "CONFIG");
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