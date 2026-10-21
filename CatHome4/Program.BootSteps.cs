using System;
using System.Collections.Generic;
using System.IO;
using CatHome4.Admin;
using CatHome4.Contracts;
using CatHome4.Core;
using CatHome4.Http;
using CatHome4.Observe;
using CatHome4.QQ;
using Mau.Development;
using Mau.Providers;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// 启动段上下文——八段执行器之间传递的启动期状态（段间唯一数据通道）。
    /// 段失败后对应字段保持空值 / 默认值，后续段据 Ready 标志判断可用性并继续（设计 §2.2 纪律）。
    /// </summary>
    internal sealed class BootContext
    {
        /// <summary>语料生成物 dll 目录</summary>
        internal string DllDir = "";

        /// <summary>非交互模式（--run/--script/--selfcheck/--probe-llm）</summary>
        internal bool Headless = false;

        /// <summary>数据根绝对路径（S2 产物）</summary>
        internal string DataRoot = "";

        /// <summary>配置目录 Data/config（S2 产物）</summary>
        internal string ConfigDir = "";

        /// <summary>受控根配置（S2 产物；S2 失败保持 null）</summary>
        internal WorkspaceConfig Workspace = null;

        /// <summary>受控根对账是否存在同 path 多根（S2 产物）</summary>
        internal bool RootsConflict = false;

        /// <summary>受控根对账结论文本（S2 产物）</summary>
        internal string RootsNote = "";

        /// <summary>schema 模板路径（S1 产物）</summary>
        internal string SchemaTemplatePath = "";

        /// <summary>schema 同步结论（S1 产物）</summary>
        internal string SchemaSyncNote = "";

        /// <summary>配置段注册结论（S1 产物）</summary>
        internal string SegmentNote = "";

        /// <summary>配置面元声明（S1 产物）</summary>
        internal ConfigSchema Schema = null;

        /// <summary>全局配置存储（S1 产物）</summary>
        internal ConfigStore LlmConfig = null;

        /// <summary>LLM 端点池（S1 产物）</summary>
        internal CH_LlmApiConfigStore ApiConfigStore = null;

        /// <summary>QQ Bot 配置池（S1 产物）</summary>
        internal CH_QqBotConfigStore QqBotStore = null;

        /// <summary>外观配置池（S1 产物）</summary>
        internal CH_AppearanceConfigStore AppearanceStore = null;

        /// <summary>网页资产下载服务（S1 构造 / S7 接线启动）</summary>
        internal WebFetchService WebFetchSvc = null;

        /// <summary>本次运行观测目录（S3 产物）</summary>
        internal string RunDir = "";

        /// <summary>工具组来源清单（S4 产物）</summary>
        internal List<ToolGroupSource> PoolGroups = new List<ToolGroupSource>();

        /// <summary>会话上下文（S5 产物）</summary>
        internal ChatContext ChatCtx = null;

        /// <summary>默认会话存储（S5 产物）</summary>
        internal SessionStore ChatStore = null;

        /// <summary>默认猫配置（S5 产物；缺失为 null）</summary>
        internal AdminService.CatCfgData DefaultCfg = null;

        /// <summary>默认猫 API 配置引用（S5 产物）</summary>
        internal Guid DefaultApiConfigId = Guid.Empty;

        /// <summary>默认猫工具名原始串（S5 产物）</summary>
        internal string DefaultToolNames = "";

        /// <summary>默认会话标识（S5 产物）</summary>
        internal string DefaultSessionId = "";

        /// <summary>默认会话工具面（S5 产物）</summary>
        internal ToolSpec[] DefaultTools = new ToolSpec[0];

        /// <summary>默认会话视图存储（S5 产物）</summary>
        internal SessionViewStore ChatViewStore = null;

        /// <summary>受控根段是否就绪（S2 收尾置位）</summary>
        internal bool WorkspaceReady = false;

        /// <summary>配置面段是否就绪（S1 收尾置位）</summary>
        internal bool ConfigReady = false;

        /// <summary>会话段是否就绪（S5 收尾置位）</summary>
        internal bool SessionReady = false;
    }

    /// <summary>
    /// 启动段降级策略——每个 Step 必须显式声明，无默认值（设计 §2.2 纪律：未声明视为审查期缺陷）。
    /// </summary>
    internal enum BootDegradePolicy
    {
        /// <summary>失败登记后继续——后续段自行判断可用性</summary>
        Continue = 0,

        /// <summary>失败即置本段产物为空集——依赖本段产物的能力整体不可用</summary>
        EmptySet = 1,

        /// <summary>内部一致性缺陷——ERROR 级出声并计入需上报栏</summary>
        Internal = 2
    }

    /// <summary>
    /// Program 启动段执行器分部（A260）——八段 Extract Method 与段级降级。
    /// 规格：Project/CH4/design-ch4-host-bootstrap-resilience.md §2.2。
    /// 边界：本分部只做「按外部依赖边界切段 + 段失败登记后继续」，不含业务逻辑。
    /// 段序：S0 → S2 → S1 → S3 → S4 → S5 → S6 → S7（受控根先于配置面——WritableRootPaths 是配置面服务的构造入参）。
    /// </summary>
    public static partial class Program
    {
        /// <summary>
        /// 执行一个启动段——异常即登记降级并继续（R1：任何环境异常不得阻断启动）。
        /// </summary>
        /// <param name="ctx">启动段上下文</param>
        /// <param name="stage">段名（S0..S7）</param>
        /// <param name="ability">该段承载的能力名（进降级条目的 Ability 字段）</param>
        /// <param name="policy">降级策略（必填，无默认值）</param>
        /// <param name="action">段体</param>
        internal static void RunStep(BootContext ctx, string stage, string ability, BootDegradePolicy policy, Action<BootContext> action)
        {
            try
            {
                action(ctx);
            }
            catch (Exception ex)
            {
                string code = "STAGE_FAILED_" + stage;
                string kind = StartupDegradation.KindEnv;
                string state = StartupDegradation.StateUnavailable;
                string fix = "查看启动报告与日志定位该段失败点，修复后重启";
                int level = 2;
                if (policy == BootDegradePolicy.EmptySet)
                {
                    state = StartupDegradation.StateEmpty;
                    fix = "该段承载的能力整体不可用——按启动报告列出的原因修复后重启";
                }
                if (policy == BootDegradePolicy.Internal)
                {
                    kind = StartupDegradation.KindInternal;
                    fix = "内部一致性缺陷（需上报）——附启动报告与日志反馈";
                    level = 3;
                }
                StartupDegradation.Report(code, kind, ability, state, "启动段 " + stage + " 失败：" + ex.Message, fix, stage);
                BootLog.Add(stage, "段失败 | " + ex.GetType().Name + " | " + ex.Message);
                LogStore.Add("CatHome4", level, "boot.stage | " + stage + " | 失败：" + ex.Message, "BOOT");
            }
        }

        /// <summary>
        /// 登记「前置段未就绪 → 本段跳过」——避免级联异常刷屏，让缺口在启动健康面上一目了然。
        /// </summary>
        /// <param name="stage">本段名</param>
        /// <param name="ability">本段能力</param>
        /// <param name="missingStage">缺失的前置段名</param>
        internal static void SkipStage(string stage, string ability, string missingStage)
        {
            StartupDegradation.Report("STAGE_SKIPPED_" + stage, StartupDegradation.KindEnv, ability,
                StartupDegradation.StateUnavailable,
                "前置段 " + missingStage + " 未就绪，本段跳过",
                "按启动报告修复前置段原因后重启", stage);
            BootLog.Add(stage, "跳过 | 前置段 " + missingStage + " 未就绪");
            LogStore.Add("CatHome4", 2, "boot.stage | " + stage + " | 跳过：前置段 " + missingStage + " 未就绪", "BOOT");
        }

        /// <summary>
        /// S0 机制服务——端口段解析 + 线程守卫 / OA / CommandBus / FlowRunner + DataBox 基础绑定。
        /// </summary>
        /// <param name="ctx">启动段上下文</param>
        internal static void StageS0(BootContext ctx)
        {
            // 端口段解析——机器级端口空间按区划分（开发区向下 / 部署区向上——A74：两区实例可同时运行）
            _portBand = ResolvePortBand();
            // 线程守卫 + 机制服务
            ThreadGuard guard = new ThreadGuard();
            _oa = new OA(guard);
            _bus = new CommandBus(guard);
            IdAllocator ids = new IdAllocator();
            _runner = new FlowRunner(guard, _oa, _bus, ids);
            // DataBox 服务绑定——积木 TryResolve 面（OA；ILlmRuntime 由 Providers 段绑定）
            DataBox.Bind<IOA>(_oa);
            // A283：CommandBus 不作服务绑定——语料 `⇚` 能力保留（FlowRunner 持有本体），宿主侧零投递、积木零引用
        }

        /// <summary>
        /// S2 受控根——数据根锚定 + workspace.json 加载对账 + FileSystemService 绑定（先于配置面：WritableRootPaths 是其入参）。
        /// </summary>
        /// <param name="ctx">启动段上下文</param>
        internal static void StageS2(BootContext ctx)
        {
            // 文件系统服务——file.* 积木依赖（受控根 = workspace.json 配置 roots；回收站=可写根 CatTemp/fs_recycle——P8.5 配置群）
            string dataRoot = ResolveDataRoot();
            ctx.DataRoot = dataRoot;
            ctx.ConfigDir = Path.Combine(dataRoot, "Data", "config");
            WorkspaceConfig workspace = WorkspaceConfig.Load(Path.Combine(dataRoot, "Data", "config", "workspace.json"), dataRoot);
            // A266/A273 固定根校准——data / db 路径由系统计算（路径锁定），缺则补、偏差则写回；失败出声不阻断启动
            string dbRootNote = AdminService.EnsureFixedRoots(workspace, dataRoot);
            if (dbRootNote.Length > 0)
            {
                BootLog.Add("S2", "固定根 | " + dbRootNote);
            }
            ctx.Workspace = workspace;
            // 受控根配置绑定——file.* 积木 / Admin 工作区页签 / 根寻址服务的唯一数据源
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
            ctx.RootsConflict = rootsConflict;
            ctx.RootsNote = rootsNote;
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
            // A267 条目库服务——库根取受控根 db（A266 启动自愈保证存在）；DbCat 积木经 DataBox 取用
            string dbRoot = "";
            for (int i = 0; i < workspace.Roots.Length; i = i + 1)
            {
                if (string.Equals(workspace.Roots[i].Id, "db", StringComparison.OrdinalIgnoreCase))
                {
                    dbRoot = workspace.Roots[i].Path;
                    break;
                }
            }
            if (dbRoot.Length > 0)
            {
                DataBox.Bind<DbService>(new DbService(dbRoot));
            }
            else
            {
                BootLog.Add("S2", "db 服务 | 未绑定：受控根 db 缺失");
            }
            ctx.WorkspaceReady = true;
        }

        /// <summary>
        /// S1 配置面——schema 同步与加载 + 全局配置 + 三配置池 + 各服务绑定（LLM / Roslyn / 视觉 / Ps / py / 浏览器 / 下载 / 宿主指令 / 音频）。
        /// </summary>
        /// <param name="ctx">启动段上下文</param>
        internal static void StageS1(BootContext ctx)
        {
            if (!ctx.WorkspaceReady)
            {
                SkipStage("S1", "配置面", "S2");
                return;
            }
            string dataRoot = ctx.DataRoot;
            WorkspaceConfig workspace = ctx.Workspace;
            string configDir = ctx.ConfigDir;
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
            // 段产物交回上下文
            ctx.Schema = schema;
            ctx.SchemaTemplatePath = schemaTemplatePath;
            ctx.SchemaSyncNote = schemaSyncNote;
            ctx.LlmConfig = llmConfig;
            ctx.SegmentNote = segmentNote;
            ctx.ApiConfigStore = apiConfigStore;
            ctx.QqBotStore = qqBotStore;
            ctx.AppearanceStore = appearanceStore;
            ctx.WebFetchSvc = webFetchSvc;
            ctx.ConfigReady = true;
        }
    }
}

namespace CH4
{
    /// <summary>
    /// Program 启动段执行器分部（续）——S3 观测 / S4 语料 / S5 会话 / S6 外观层 / S7 常驻附属。
    /// 规格：Project/CH4/design-ch4-host-bootstrap-resilience.md §2.2。
    /// </summary>
    public static partial class Program
    {
        /// <summary>
        /// S3 观测——统一运行目录四文件 + Console 订阅 + 早期 sink 回灌 + 配置面启动审计补记。
        /// </summary>
        /// <param name="ctx">启动段上下文</param>
        internal static void StageS3(BootContext ctx)
        {
            if (!ctx.WorkspaceReady)
            {
                SkipStage("S3", "观测日志", "S2");
                return;
            }
            // [段3] 统一观测运行目录——O 系列：Data/runs/<ts>/ 四文件（log_all.txt/oa_all.txt/frame.txt/err_all.txt——design-ch4-observe §三）
            string dataRoot = ctx.DataRoot;
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
            ctx.RunDir = runDir;
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
            LogStore.Add("CatHome4", 1, "config.boot | " + ctx.SchemaSyncNote + " | " + ctx.SegmentNote, "CONFIG");
            // A153 受控根对账落盘——同 path 多根走 L2（WARN）出声
            if (ctx.RootsConflict)
            {
                LogStore.Add("CatHome4", 2, "roots.boot | " + ctx.RootsNote, "WORKSPACE");
            }
            else
            {
                LogStore.Add("CatHome4", 1, "roots.boot | " + ctx.RootsNote, "WORKSPACE");
            }
            // 根不可达告警——加载期跳过的非常驻根在此出声（L2）：面板可修正路径后重启恢复
            WorkspaceConfig workspace = ctx.Workspace;
            if (workspace != null)
            {
                for (int warnIdx = 0; warnIdx < workspace.LoadWarnings.Count; warnIdx = warnIdx + 1)
                {
                    LogStore.Add("CatHome4", 2, "roots.boot | " + workspace.LoadWarnings[warnIdx], "WORKSPACE");
                }
            }
        }

        /// <summary>
        /// S4 语料与工具池——扫描 Flows/FL_*.dll 统一装配 + 工具池重建 + 三面对账出声。
        /// </summary>
        /// <param name="ctx">启动段上下文</param>
        internal static void StageS4(BootContext ctx)
        {
            // [段4] 语料加载——扫描 Flows/FL_*.dll 统一装配（design-ch4-flow-scan §3.2：QuickCat + 全部工具组 Flow）
            // 扫描化：文件名去 FL_ 前缀得 Flow 名（dll 名 = 组名——QuickCat 与工具组一视同仁）
            // 容错：加载失败 → 警告 + 跳过注册（该组工具工单无人认领 → 超时诚实 ERR——不再宿主直执）
            // 路由表数据化 + 工具定义数据化：加载后经 IFlow.GetMetaJson/GetToolsJson 读自曝 → 统一工具池（design-ch4-tools-pool §六）
            string dllDir = ctx.DllDir;
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
            ctx.PoolGroups = poolGroups;
        }

        /// <summary>
        /// S5 会话面——会话中枢 + 默认猫配置 + 会话对象化 + 前文恢复 + LLM 端点自检。
        /// </summary>
        /// <param name="ctx">启动段上下文</param>
        internal static void StageS5(BootContext ctx)
        {
            string dataRoot = ctx.DataRoot;
            WorkspaceConfig workspace = ctx.Workspace;
            ConfigStore llmConfig = ctx.LlmConfig;
            CH_LlmApiConfigStore apiConfigStore = ctx.ApiConfigStore;
            // [段5] 会话面——上下文 + 前文恢复 + 工具定义 + 默认会话注册（P9.1 会话对象化：ChatSession 承载状态机——design-llm-streaming §六）
            ChatContext chatCtx = new ChatContext();
            // S1 ChatBridge 化——会话协调实例（注入提示词构建委托——CatCfg 域静态面 BuildInjectPrompt）
            _chatBridge = new ChatBridge(BuildInjectPrompt);
            // S4 Admin 域接线——依赖注入（管理 API 处理器 + cat.* 指令族迁入 CatHome4.Admin）
            AdminService.Configure(_chatBridge, _oa, dataRoot, _mainThreadId, ExecuteTool, ObserveService.BuildSnapshotJson, new HtmlRootProvider(), apiConfigStore, ctx.QqBotStore, ctx.AppearanceStore, llmConfig, _portBand);
            AdminService.BuildEnvInfoProvider = BuildEnvInfo;
            // A281 分发表接线——停机态探针 + 热重载执行体（宿主级能力，Admin 域不持有实现）
            AdminService.IsRestartingProvider = IsRestarting;
            AdminService.ReloadHandler = ExecuteReload;
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
            WebFetchService webFetchSvc = ctx.WebFetchSvc;
            if (webFetchSvc != null)
            {
                webFetchSvc.Configure(
                    Path.Combine(dataRoot, "Data", "runtime", "downloads.json"),
                    downloadsDir,
                    delegate (string downloadCatKey, string downloadText)
                    {
                        return _chatBridge.PostSystemMessage(downloadCatKey, "systemauto", downloadText) == "OK";
                    });
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
            // 外观池生效值——桌宠 / 轮末总结（与 catcfg.apply §1520-1521 同源）
            // 🔴 缺这两句 = 重启后 majordomo 外观不生效、只有点保存（catcfg.apply）才应用（判例 2026-10-20）
            _chatBridge.DefaultSession.SetRoundSummary(AdminService.ResolveEffectiveRoundSummary("majordomo"));
            _chatBridge.DefaultSession.SetPetKind(AdminService.ResolveEffectivePetKind("majordomo"));
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
            // 段产物交回上下文
            ctx.ChatCtx = chatCtx;
            ctx.ChatStore = chatStore;
            ctx.DefaultCfg = defaultCfg;
            ctx.DefaultApiConfigId = defaultApiConfigId;
            ctx.DefaultToolNames = defaultToolNames;
            ctx.DefaultSessionId = defaultSessionId;
            ctx.DefaultTools = defaultTools;
            ctx.ChatViewStore = chatViewStore;
            ctx.SessionReady = true;
        }
    }
}

namespace CH4
{
    /// <summary>
    /// Program 启动段执行器分部（续二）——S6 外观层 / S7 常驻附属。
    /// 规格：Project/CH4/design-ch4-host-bootstrap-resilience.md §2.2。
    /// </summary>
    public static partial class Program
    {
        /// <summary>
        /// S6 外观层——主 HTTP 宿主（端口段回退）+ 默认猫独立端口。非交互模式整段跳过（端口零占用）。
        /// </summary>
        /// <param name="ctx">启动段上下文</param>
        internal static void StageS6(BootContext ctx)
        {
            if (ctx.Headless)
            {
                Console.WriteLine("[CMD] 非交互模式——外观层与常驻附属未启动（端口零占用）");
                return;
            }
            if (!ctx.SessionReady)
            {
                SkipStage("S6", "外观层（主端口）", "S5");
                return;
            }
            HttpHostOptions mainHostOptions = new HttpHostOptions
            {
                Port = ResolveHttpPort(ctx.LlmConfig),
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
            if (_httpHost != null)
            {
                Console.WriteLine("[CMD] HTTP 外观层就绪: http://127.0.0.1:" + _httpHost.Port.ToString() + " | Majordomo 对话: http://127.0.0.1:" + AdminService.MajorPort.ToString());
            }
            else
            {
                Console.WriteLine("[CMD] HTTP 外观层未启动（端口段耗尽）——宿主以无外观层形态运行");
            }
        }

        /// <summary>
        /// S7 常驻附属——多猫拉起 / 前端测试服务 / QQ 桥接 / py 自启 / 下载器启动 / 配置池读取校验。
        /// 非交互模式整段跳过（与 S6 同判据：端口零占用、附属零启动）。
        /// </summary>
        /// <param name="ctx">启动段上下文</param>
        internal static void StageS7(BootContext ctx)
        {
            if (!ctx.SessionReady)
            {
                SkipStage("S7", "常驻附属（多猫 / QQ / py / 前端测试服务）", "S5");
                return;
            }
            string dataRoot = ctx.DataRoot;
            // A260 下载器启动——任务表损坏 / 目录不可用时通道降级，不阻断宿主（两种模式均启动——保持 A74 前既有行为）
            WebFetchService webFetchSvc = ctx.WebFetchSvc;
            if (webFetchSvc != null)
            {
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
            }
            // py 服务自启——follow=true 的服务随宿主启动拉起（design-ch4-pytools §5.2；接线 2026-10-15）
            // A260 二期：失败服务逐条接降级登记面（原仅一行 L1 汇总日志，降级事实在启动健康面不可见）
            {
                IPyService pyAutostart;
                if (DataBox.TryResolve<IPyService>(out pyAutostart) && pyAutostart != null)
                {
                    string pyResult = pyAutostart.Autostart();
                    LogStore.Add("CatHome4", 1, "py.boot | " + pyResult, "CONFIG");
                    ReportPyAutostartDegrade(pyResult);
                }
            }
            // 非交互模式到此为止——外观层与常驻附属（多猫 / 前端测试服务 / QQ）不启动（A74：端口零占用）
            if (ctx.Headless)
            {
                return;
            }
            // [段6b] 启动扫描——sessions/*/cat.cfg 中 running 猫拉起（主 HTTP 就位后——每猫 HttpHost 独立实例）
            AdminService.LoadCatsOnBoot();
            // [段6c] 前端测试服务拉起——开发流程（改前端 → 跑测试 → 刷新生效）；未监听则启动 node server.js
            EnsureFrontendTestService();
            // R2.3 QQ 管理器启动——扫描 Bot 池建立全部连接（附属功能组件；Bot 池空 = 零连接静默）
            // S3 解耦——注入绑定目标收集面（默认猫 + 多猫注册表实现）
            QQBotService.SetCollector(new QqTargetCollector());
            QQBotService.SetFileCacheRoot(Path.Combine(dataRoot, "Data", "qqbot-files"));
            QQBotService.Start();
            // A260 启动健康面收口——报告落盘 + 独立提示窗（有降级才弹；关窗不改运行态）
            // 配置池读取校验：QQ / 外观池在启动期未必被消费，此处主动读一次，让损坏在启动面可见
            CH_QqBotConfigStore qqBotStore = ctx.QqBotStore;
            if (qqBotStore != null && qqBotStore.LastLoadError.Length > 0)
            {
                StartupDegradation.Report("CFG_CORRUPT", StartupDegradation.KindData, "QQ 通道（Bot 配置）",
                    StartupDegradation.StateEmpty, "qqbot.json 读取失败：" + qqBotStore.LastLoadError,
                    "管理面板 → QQ Bot → 重建配置（坏档已备份 .broken-<ts>）", "S7");
            }
            CH_AppearanceConfigStore appearanceStore = ctx.AppearanceStore;
            if (appearanceStore != null)
            {
                appearanceStore.GetAll();
                if (appearanceStore.LastLoadError.Length > 0)
                {
                    StartupDegradation.Report("CFG_CORRUPT", StartupDegradation.KindData, "外观配置池",
                        StartupDegradation.StateEmpty, "appearance.json 读取失败：" + appearanceStore.LastLoadError,
                        "管理面板 → 外观 → 重建配置（坏档已备份 .broken-<ts>）", "S7");
                }
            }
        }

        /// <summary>
        /// py 自启结果解析——`errors` 数组非空时逐服务登记降级（二期：让 py 服务拉起失败在启动健康面可见）。
        /// </summary>
        /// <param name="result">IPyService.Autostart 返回体（JSON 或 ERR| 前缀）</param>
        internal static void ReportPyAutostartDegrade(string result)
        {
            if (result == null || result.Length == 0)
            {
                return;
            }
            if (result.StartsWith("ERR|", StringComparison.Ordinal))
            {
                StartupDegradation.Report("PY_AUTOSTART_FAILED", StartupDegradation.KindExternal, "py 服务",
                    StartupDegradation.StateUnavailable, "py 自启未执行：" + result,
                    "配置 py.python_path 后在面板「资源」页签手动启动服务", "S7");
                return;
            }
            System.Text.Json.JsonDocument doc = JsonUtil.Parse(result, "py.autostart");
            if (doc == null)
            {
                StartupDegradation.Report("PY_AUTOSTART_UNPARSED", StartupDegradation.KindInternal, "py 服务",
                    StartupDegradation.StateUnavailable, "py 自启返回体无法解析：" + TruncateForLog(result, 120),
                    "内部一致性缺陷（需上报）——附启动报告与日志反馈", "S7");
                return;
            }
            using (doc)
            {
                System.Text.Json.JsonElement root = doc.RootElement;
                System.Text.Json.JsonElement errors;
                if (!root.TryGetProperty("errors", out errors) || errors.ValueKind != System.Text.Json.JsonValueKind.Array)
                {
                    return;
                }
                for (int i = 0; i < errors.GetArrayLength(); i = i + 1)
                {
                    string name = errors[i].GetString();
                    if (name == null || name.Length == 0)
                    {
                        continue;
                    }
                    StartupDegradation.Report("PY_AUTOSTART_FAILED", StartupDegradation.KindExternal, "py 服务",
                        StartupDegradation.StateUnavailable, "服务自启失败：" + name,
                        "面板「资源」页签查看该服务错误并手动启动", "S7");
                }
            }
        }

        /// <summary>
        /// 文本截断——日志行长度保护（不改变语义，只防单行过长）。
        /// </summary>
        /// <param name="text">原文本</param>
        /// <param name="max">最大长度</param>
        /// <returns>截断文本（超限补省略号）</returns>
        private static string TruncateForLog(string text, int max)
        {
            if (text == null)
            {
                return "";
            }
            if (text.Length <= max)
            {
                return text;
            }
            return text.Substring(0, max) + "…";
        }
    }
}
