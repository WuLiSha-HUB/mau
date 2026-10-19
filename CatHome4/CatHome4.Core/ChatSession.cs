using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Text;
using System.Text.Json;
using System.Threading.Tasks;
using Mau.Runtime;
using Mau.Contracts;
using CatHome4.Contracts;

namespace CH4
{
    /// <summary>
    /// 会话相位——P9.1 四相环（动作段 StartLlm/LlmDone/StartToolBatch/ToolBatchDone/Done 封装进相位 Enter/Exit——design-llm-streaming §6.2）。
    /// </summary>
    internal enum ChatPhase
    {
        /// <summary>空闲——无活跃轮次（可接收用户消息）</summary>
        Idle,
        /// <summary>LLM 后台流式等待——主线程轮询完成槽</summary>
        LlmRunning,
        /// <summary>工具批执行——Dog 逐帧轮询</summary>
        ToolBatchRunning,
        /// <summary>终态动作段——前文落盘 + 状态复位（一帧完成回 Idle）</summary>
        Done
    }

    /// <summary>
    /// 宿主会话实体——上下文/前文/LLM 槽/工具批/相位环（P9.1 会话对象化——design-llm-streaming §六规格）。
    /// 线程模型：相位与上下文仅主线程推进；LLM 槽后台 Task.Run 写、主线程轮询（volatile Busy 置位/复位）。
    /// SSE 推送/日志/工具直执经宿主服务引用回调——ChatSession 不持有宿主静态面。
    /// </summary>
    internal sealed partial class ChatSession
    {
        /// <summary>LLM 超时帧上限——同原 WaitForLlm MaxFramesPerRun 语义（30000 帧 ≈ 25 分钟空转上限）</summary>
        private const long LlmTimeoutFrames = 30000;

        /// <summary>工具批等待帧上限——最长工具 2400 帧（120 秒，A48 统一口径）+ 600 余量</summary>
        private const long ToolBatchWaitFrames = 2400 + 600;

        /// <summary>工具单 Dog owner ID——宿主 Dog 域（同 ToolOwnerId——OA 未开存活校验，多 Dog 未来可扩展独立 ID）</summary>
        private const long ToolOwnerId = 1;

        /// <summary>RoundSummary 工单 owner ID——独立于会话工具工单（避免 RemoveByOwner 互扰）</summary>
        private const long RoundSummaryOwnerId = 2;

        /// <summary>RoundSummary 工单超时帧上限——一次性 LLM 调用（60s 现实耗时 + 余量）</summary>
        private const long RoundSummaryTimeoutFrames = 2400;

        /// <summary>RoundSummary 播报串行键——播报名与正文、全部猫的总结共用（跨猫全串行，不并发）。</summary>
        private const string RoundSummaryAudioKey = "roundsummary";

        /// <summary>RoundSummary 后段提示词（拼在待总结内容之后——固定分隔符，界定待总结范围；第一人称口径）。</summary>
        private const string RoundSummaryPromptTail = "\n——以上是本轮的全部往来（用户输入与你自己的回复按序合并）。请用第一人称向用户交代这一轮你做了什么、结果如何。";
        /// <summary>系统注入类型——现行唯一值 systemauto（后续类型扩在此处；未知值一律拒绝，不静默回落）。</summary>
        internal const string SysKindSystemAuto = "systemauto";

        /// <summary>系统注入族名——延迟族（与 systemauto 并列，均属 sys 路径）。</summary>
        internal const string SysKindSleep = "sleep";
        /// <summary>延迟族——timer 排程注入来源。</summary>
        internal const string SysKindTimer = "timer";
        /// <summary>延迟族——delay 人工延迟来源。</summary>
        internal const string SysKindDelay = "delay";
        /// <summary>延迟族——restart 宿主重启回执来源。</summary>
        internal const string SysKindRestart = "restart";

        /// <summary>系统注入族名集合——sys 路径来源值域（systemauto + 延迟族）。</summary>
        internal static readonly string[] SysKindFamily = new string[] { SysKindSystemAuto, SysKindSleep, SysKindTimer, SysKindDelay, SysKindRestart };
        /// <summary>系统注入行头前缀——固定标记（判据规范见 `L1/Discipline.md` §三：user 消息内容以该前缀开头 = 系统注入）。</summary>
        internal const string SysInjectPrefix = "[systemauto]";
        /// <summary>来源标记——人工输入（视图层不写 src 字段；缺省即人工）。</summary>
        internal const string SourceUser = "user";
        /// <summary>系统注入来源值域校验——systemauto + 延迟族（参数面零容忍：未知来源拒绝并出声）。</summary>
        /// <param name="kind">来源族名（systemauto / sleep / timer / delay / restart）</param>
        /// <returns>true=已知来源</returns>
        internal static bool IsKnownSysKind(string kind)
        {
            if (kind == null)
            {
                return false;
            }
            for (int i = 0; i < SysKindFamily.Length; i = i + 1)
            {
                if (string.Equals(kind, SysKindFamily[i], StringComparison.Ordinal))
                {
                    return true;
                }
            }
            return false;
        }
        /// <summary>路径判定——来源标记是否属系统注入路径（user / 空 = 人工路径；其余族名 = sys 路径）。</summary>
        /// <param name="source">队列来源标记</param>
        /// <returns>true=系统注入路径</returns>
        internal static bool IsSysSource(string source)
        {
            if (source == null || source.Length == 0 || string.Equals(source, SourceUser, StringComparison.Ordinal))
            {
                return false;
            }
            return true;
        }
        /// <summary>系统注入文本加工——唯一实现：套 [systemauto] 行头前缀（渠道自带前缀由内容携带，本处只加外层标记）。</summary>
        /// <param name="content">注入内容（含渠道自定义前缀）</param>
        /// <returns>入前文文本</returns>
        internal static string BuildSysInjectText(string content)
        {
            if (content == null)
            {
                content = "";
            }
            // 幂等——已带行头前缀不重复套（入口侧与落前文侧两处调用共用本实现）
            if (content.StartsWith(SysInjectPrefix, StringComparison.Ordinal))
            {
                return content;
            }
            return SysInjectPrefix + content;
        }
        /// <summary>
        /// 系统注入入口——sys 独立路径：类型校验 + 停机态校验 + 入 _sysPending 队列（固定前缀在落前文单点套用）。
        /// 失败可见：未知类型 / 空内容 / 停机态未受理一律返回 ERR（不静默丢弃）。
        /// </summary>
        /// <param name="kind">类型参数（现行仅 systemauto）</param>
        /// <param name="content">注入内容（原值存入——固定前缀在落前文单点套用）</param>
        /// <param name="imagesJson">附件引用 JSON 数组（图片注入用；空串=无附件）</param>
        /// <returns>OK 受理 / ERR|CODE|msg</returns>
        public string PostSystemMessage(string kind, string content, string imagesJson = "")
        {
            if (!IsKnownSysKind(kind))
            {
                string shown = kind;
                if (shown == null)
                {
                    shown = "";
                }
                return "ERR|BAD_ARGS|未知系统注入类型: " + shown;
            }
            if (content == null || content.Trim().Length == 0)
            {
                return "ERR|BAD_ARGS|系统注入内容为空";
            }
            // 停机态拒收——与 user 路径同口径（design-ch4-host-restart §三 T2：停机后等的是在途轮次）
            string restartState;
            if (DataBox.TryGet<string>("global", "host_restart_state", out restartState) && restartState == "requested")
            {
                LogStore.Add("CatHome4", 2, "宿主重启中——sys 注入未受理（类型 " + kind + "）", "RESTART");
                return "ERR|HOST_RESTARTING|系统注入未受理（宿主重启停机态 · 类型 " + kind + "）";
            }
            PendingMessage msg = new PendingMessage();
            msg.Content = content;
            msg.Source = kind;
            if (imagesJson == null)
            {
                msg.ImagesJson = "";
            }
            else
            {
                msg.ImagesJson = imagesJson;
            }
            msg.Origin = "";
            msg.Seq = _pendingSeq;
            _pendingSeq = _pendingSeq + 1;
            _sysPending.Enqueue(msg);
            LogStore.Add("CatHome4", 1, "系统注入入队: cat=" + _id + " | 类型 " + kind + " | 长度 " + content.Length.ToString(), "CHAT");
            return "OK";
        }

        // [段1] 标识与持久化
        /// <summary>会话唯一 ID——构造注入 = 猫 key（唯一标识；会话重建不换键）</summary>
        private string _id;

        /// <summary>显示名——P9.1 为 "majordomo"；P9.3 用户输入（A201：session.new 时按 cat.cfg 刷新）</summary>
        private string _displayName;

        /// <summary>消息历史——会话上下文容器（第一条 system；Mau.Runtime 实体复用）</summary>
        private readonly ChatContext _context;

        /// <summary>前文落盘——P9.1 保持 Data/sessions/majordomo.json（sessions/&lt;id&gt;.json 化是 P9.4）</summary>
        private readonly SessionStore _store;
        // [段2] LLM 槽 6 字段——后台 Task.Run 写、主线程轮询读
        /// <summary>LLM 后台运行中——volatile 置位（后台 finally 复位）</summary>
        private volatile bool _llmBusy;

        /// <summary>LLM 后台结果——完整回复文本</summary>
        private string _llmResultText;

        /// <summary>LLM 后台结果——完整思考文本（工具轮次回传铁律）</summary>
        private string _llmReasoning;
        /// <summary>
        /// 本块思考用时累计毫秒——think 态内部实现（进入 think 态开表、离开即结算，不跨态；重试退避等非 think 段不计入）。
        /// 单块生命周期：推入视图层时由 TakeReasonDuration 取走并归零（不跨轮 / 不跨块残留）。
        /// </summary>
        private long _reasonDurMs;

        /// <summary>LLM 后台结果——tool_calls JSON 数组（translate 聚合后整体）</summary>
        private string _llmToolCallsJson;

        /// <summary>LLM 后台错误标志</summary>
        private bool _llmError;

        /// <summary>LLM 后台错误文本（ERR| 前缀——失败可见性）</summary>
        private string _llmErrorText;

        /// <summary>本轮是否发生过重试——首个 Text/Reasoning 到达时回填 retry 视图 resolved（S2 §8.4）</summary>
        private bool _sawRetry;

        /// <summary>本轮是否推过 retry 视图——错误中止措辞分档判据（A158：取代视图出口在途序号；轮终复位）</summary>
        private bool _retryPushed;

        /// <summary>retry 气泡原文快照——最近一次重试的尝试序号（A86：resolved 保留报错信息）</summary>
        private string _retryAttempt = "";

        /// <summary>retry 气泡原文快照——最近一次重试的次数上限（A86）</summary>
        private string _retryMax = "";

        /// <summary>retry 气泡原文快照——最近一次重试的原因摘要（含中文注释；A86）</summary>
        private string _retryReason = "";

        /// <summary>空回复续传计数——无回复/纯空格时同上下文重发（无上限——LLM 兜底；整轮清零）</summary>
        private int _emptyReplyRetry;

        /// <summary>STREAM_CLOSED 续传标志——SSE 流未以 [DONE] 结束时置位（PumpLlm 检查；续传后复位）</summary>
        private bool _streamClosedRetry;

        // [段2b] P6 中止——暂停标志与取消令牌（LLM 流物理取消面）
        /// <summary>中止请求标志——HTTP 线程置位（volatile 跨线程可见），主线程 Pump 消费执行收尾</summary>
        private volatile bool _pauseRequested;
        /// <summary>
        /// 继续请求标志——HTTP 线程置位（volatile 跨线程可见），主线程 Pump 消费（Idle 时启动继续轮）。
        /// </summary>
        volatile bool _continueRequested;

        /// <summary>本轮 LLM 流取消令牌——LaunchLlm 创建；Pause 时 Cancel（下一轮重建——取消不跨轮）</summary>
        private System.Threading.CancellationTokenSource _pauseCts;

        /// <summary>Token 用量整轮累计——prompt（含 cache hit）</summary>
        private long _usagePrompt;

        /// <summary>Token 用量整轮累计——completion</summary>
        private long _usageCompletion;

        /// <summary>Token 用量整轮累计——cache hit（CH2 双格式：prompt_cache_hit_tokens / cached_tokens）</summary>
        private long _usageCacheHit;

        /// <summary>Token 用量会话累计——prompt（本次请求增量随轮同期堆入；仅新会话复位，跨轮保留）</summary>
        private long _sessionPrompt;

        /// <summary>Token 用量会话累计——completion（跨轮持续累加；仅新会话复位）</summary>
        private long _sessionCompletion;

        /// <summary>Token 用量会话累计——cache hit（跨轮持续累加；仅新会话复位）</summary>
        private long _sessionCacheHit;

        /// <summary>单次前文长度——最近一次请求的 prompt（覆盖式；非累计——前文长度数据源）</summary>
        private long _contextTokens;

        /// <summary>Token 预警阈值——单次请求 miss（未命中缓存的 prompt）超过此值即投 error 气泡（固定 100k——长前文缓存断裂定位点）</summary>
        private const long MissAlertThreshold = 100000;

        /// <summary>已预警请求号——同一请求多 usage 帧只报一次（比对 _requestCount；0 为起点哨兵，请求号自 1 起）</summary>
        private int _lastMissAlertReq;

        /// <summary>本轮开始时间戳——Stopwatch.GetTimestamp（roundsum 总耗时）</summary>
        private long _roundStartTick;

        /// <summary>本轮工具调用次数——工具 Dog 登记处累加（roundsum toolCount）</summary>
        private int _toolCallCount;

        /// <summary>本轮工具主动 done 标记——sleep 等待登记 / restart 收尾登记置位（roundsum done=tool；QQ 转发面据此续约来源）</summary>
        private bool _toolDone;

        /// <summary>当前运行态——PhaseIdle/Wait/Link/Think/Tool/Run/Reply（-1=无活跃）</summary>
        private int _phaseKind = -1;

        /// <summary>当前态开始时间戳——Stopwatch.GetTimestamp</summary>
        private long _phaseStartTick;

        /// <summary>七态累计毫秒——idle/wait/link/think/tool/run/reply（态切换结算；idle 恒 0——空闲不计时）</summary>
        private long[] _phaseAccumMs = new long[PhaseCount];

        /// <summary>态字段锁——后台消费线程切态、主线程结算与读面（design-ch4-llm §2.1 线程安全）</summary>
        private readonly object _phaseLock = new object();

        /// <summary>本轮 API 请求次数——每次 LaunchLlm 累加（含重试重发与空回复续传；= link 段数）</summary>
        private int _requestCount;

        /// <summary>运行态常量——空闲（本地·空转：轮未开始或收尾中；不计时——只作态名）</summary>
        private const int PhaseIdle = 0;

        /// <summary>运行态常量——等待（本地·退避：重试退避期间；长度由本地决定）</summary>
        private const int PhaseWait = 1;

        /// <summary>运行态常量——链路（远端·等待：请求发出到首个语义增量帧；长度由远端决定）</summary>
        private const int PhaseLink = 2;

        /// <summary>运行态常量——思考（远端·流：reasoning_content 增量）</summary>
        private const int PhaseThink = 3;

        /// <summary>运行态常量——工具（远端·流：LLM 输出 tool_calls 决策的流式过程）</summary>
        private const int PhaseTool = 4;

        /// <summary>运行态常量——执行（本地·程序过程：工具批发单到下一请求发出）</summary>
        private const int PhaseRun = 5;

        /// <summary>运行态常量——回复（远端·流：content 增量）</summary>
        private const int PhaseReply = 6;

        /// <summary>运行态态数——七态（索引与常量一一对应）</summary>
        private const int PhaseCount = 7;

        /// <summary>运行态名表——索引对应（JSON 产出与观测面文本化）</summary>
        private static readonly string[] PhaseNames = { "idle", "wait", "link", "think", "tool", "run", "reply" };

        // [段3] 工具批
        /// <summary>工具批执行中——reload 拒绝检查面（任一会话 TRUE 即拒绝）</summary>
        private bool _toolBatchActive;
        /// <summary>工具单列表——全量工具单按 LLM 声明序入列（A127：入列序即回填序与视图编号基准；执行按批次推进）</summary>
        private readonly List<ToolOrderDog> _dogs;
        /// <summary>host-* 延迟直执清单——当前批末尾宿主直执（顺序保证：同批构建类工具先完成产物落地）</summary>
        private readonly List<ToolOrderDog> _hostDogs;
        /// <summary>分批计划——按 order 值升序的批次列表（A127：同值一批 · 批内 = LLM 声明序）</summary>
        private readonly List<List<ToolOrderDog>> _batches;
        /// <summary>当前批序号——_batches 索引（-1 = 无待执行批次，直接收口）</summary>
        private int _batchIndex;

        // [段4] 相位环
        /// <summary>当前相位</summary>
        private ChatPhase _phase;

        /// <summary>当前工具轮次（0 起——无限续轮，直到 LLM 给出最终回复；日志轮数显示）</summary>
        private long _round;

        /// <summary>当前相位已运行帧数——超时计时面</summary>
        private long _phaseFrames;

        /// <summary>待处理用户消息队列——忙时排队（原同步阻塞天然排队语义保持）</summary>
        /// <summary>
        /// 待处理消息实体——内容 + 来源（user=人发送/system=系统自动）。
        /// </summary>
        private sealed class PendingMessage
        {
            /// <summary>消息内容</summary>
            public string Content;

            /// <summary>来源——user（人工）/ systemauto · sleep · timer · delay · restart（系统注入——视图层 src 一律投影 systemauto）</summary>
            public string Source;

            /// <summary>附件引用 JSON 数组（图片绝对路径；空串=无附件——图片注入经队列携带，见 ImagesJson 语义）</summary>
            public string ImagesJson;

            /// <summary>原注入来源标记——QQ 注入轮起点锚（空=非 QQ 注入 / 系统注入）；输出转发面据此把「来源」绑定到该消息开启的轮</summary>
            public string Origin;

            /// <summary>入队序号（到达序——两队列合并排序键）</summary>
            public long Seq;
        }

        /// <summary>待处理用户消息队列——忙时排队（原同步阻塞天然排队语义保持）</summary>
        private readonly Queue<PendingMessage> _pending;
        /// <summary>待处理 sys 消息队列——系统注入独立路径（来源族名随条目携带；与 user 队列并行消费）</summary>
        private readonly Queue<PendingMessage> _sysPending;
        /// <summary>入队序号计数器——两队列共享（到达序排序键来源）</summary>
        private long _pendingSeq;
        /// <summary>人工输入空闲注入开关——true=人工输入仅在 Idle 时推送（轮内不插）；sys 不受影响（cat.cfg userInjectIdleOnly）</summary>
        private bool _userInjectIdleOnly;
        /// <summary>轮末总结开关——值源=外观配置池条目字段（A244 迁入；无生效外观=关）；关则轮末零成本跳过</summary>
        private bool _roundSummaryEnabled;
        /// <summary>桌宠种类——值源=外观配置池条目字段（A245/A251；空 = 无桌宠，前端按空值渲染不显示）</summary>
        private string _petKind = "";
        /// <summary>本轮 user 文本缓冲——轮末总结输入（全部 user 按序合并）。</summary>
        private readonly StringBuilder _roundSummaryUserBuf = new StringBuilder();
        /// <summary>本轮 text 缓冲——轮末总结输入（全部 text 按序合并）。</summary>
        private readonly StringBuilder _roundSummaryTextBuf = new StringBuilder();
        /// <summary>在途总结工单 officeId——0=无在途；&gt;0=待回收（谁发谁收，会话内自持）</summary>
        private long _roundSummaryOfficeId;
        /// <summary>在途总结块时间戳——固定插在 roundsum 之后（= roundsum ts + 1）。</summary>
        private long _roundSummaryPendingTs;

        // [段5] 宿主服务引用（构造注入——宿主级共享面）
        /// <summary>LLM 运行时——ChatStream 调度（宿主 Bootstrap 注入；M3 apiConfigId 切换 SwapLlmRuntime 替换）</summary>
        private ILlmRuntime _llmRuntime;

        /// <summary>OA 工单平台——Dog Post/轮询</summary>
        private readonly OA _oa;

        /// <summary>工具定义表——后台流式携带（宿主 BuildToolSpecs 产物；M3 改 toolNames 新会话 SetToolSpecs 更新）</summary>
        private ToolSpec[] _tools;

        /// <summary>
        /// 当前授权工具名提供者——catKey → 工具名数组（Admin 域实时解析当前 cat.cfg 名单；宿主启动期组合根注入）。
        /// 授权面与注入面解耦（design-ch4-tools §三·十一）：注入面 _tools 是会话级提示快照，授权面实时查询。
        /// 未接线（测试 / 裸构造）→ 回落 _tools 快照（旧语义）+ 一次告警——不静默放宽、不静默拒绝。
        /// </summary>
        internal static Func<string, string[]> AuthorizedToolNamesProvider;

        /// <summary>授权面提供者缺失告警标志——只报一次（防每次判定刷屏）</summary>
        private bool _authorizedProviderWarned;

        /// <summary>宿主工具直执回调——host-* 延迟直执（批次末尾执行；OA 工具一律走 OA 认领，无直执）</summary>
        private readonly Func<string, string, string> _executeTool;

        /// <summary>猫 key——工具执行按猫裁剪（M4e 白名单；默认猫=majordomo；多猫=会话 ID）</summary>
        private string _catKey = "";

        /// <summary>缓存隔离键取值非法告警标志——只报一次（防每请求刷屏）</summary>
        private bool _cacheIsolationWarned;

        /// <summary>
        /// 设置猫 key——构造后由创建方赋值（默认猫 majordomo / 多猫会话 ID）。
        /// </summary>
        /// <param name="catKey">猫 key</param>
        public void SetCatKey(string catKey)
        {
            _catKey = catKey;
            // A232——待处理消息恢复：catKey 就位后加载（构造期 catKey 未定，故不放在构造）
            LoadPendingFromDisk();
        }
        /// <summary>设置人工输入空闲注入开关——catcfg.apply 即时应用（cat.cfg userInjectIdleOnly）。</summary>
        /// <param name="value">true=人工输入仅在 Idle 时推送（轮内不插）；false=现状</param>
        public void SetUserInjectIdleOnly(bool value)
        {
            _userInjectIdleOnly = value;
        }

        /// <summary>设置轮末总结开关——catcfg.apply 即时应用（A244 起值源=外观配置池条目）。</summary>
        /// <param name="value">true=每轮结束后调度 LLMSimpleRequest 产出总结块</param>
        public void SetRoundSummary(bool value)
        {
            _roundSummaryEnabled = value;
        }
        /// <summary>设置桌宠种类——catcfg.apply 即时应用（A245 起值源=外观配置池条目字段）。</summary>
        /// <param name="kind">素材名（空 = 无桌宠不显示，由前端按空值处理）</param>
        public void SetPetKind(string kind)
        {
            _petKind = kind == null ? "" : kind;
        }

        /// <summary>
        /// 轮末总结 LLM 配置提供者——catKey → [endpoint, apikey, model]（Admin 域按 cat.cfg apiConfigId 解析；宿主启动期组合根注入）。
        /// 未接线 / 返回空数组 → 本轮不出总结（不静默放行、不误报成功）。
        /// </summary>
        internal static Func<string, string[]> RoundSummaryLlmProvider;

        /// <summary>
        /// 轮末总结播报提供者——catKey → [启用标志（"1"/"0"）, 音色槽位值]（Admin 域按外观池条目解析；宿主启动期组合根注入）。
        /// 未接线 / 启用标志非 "1" → 不出声（不静默放行、不误报成功）。
        /// </summary>
        internal static Func<string, string[]> SummaryAudioProvider;

        /// <summary>
        /// 缓存隔离键——LLM 请求身份（请求体 user_id + x-opencode-session 头）。
        /// 取值按全局配置 llm.cache_isolation：cat（默认——键 = 猫 key，会话重建不换键）/ off（不携带 user_id）。
        /// 非法值回落 cat 并告警一次（读面兜底 + 失败可见）。
        /// </summary>
        /// <returns>隔离键（空=不隔离——Provider 侧回落默认会话头）</returns>
        private string ResolveCacheIsolationKey()
        {
            string mode = "cat";
            ConfigStore cfg = null;
            if (DataBox.TryResolve<ConfigStore>(out cfg) && cfg != null)
            {
                string value = cfg.Get("llm.cache_isolation", "");
                if (value.Length > 0)
                {
                    mode = value.Trim().ToLowerInvariant();
                }
            }
            if (mode == "off")
            {
                return "";
            }
            if (mode != "cat" && !_cacheIsolationWarned)
            {
                _cacheIsolationWarned = true;
                LogStore.Add("LLM", 2, "llm.cache_isolation 取值非法（" + mode + "）——回落 cat；合法值 cat/off", "CONFIG");
            }
            return _catKey;
        }

        /// <summary>
        /// 工具执行包装——设置猫上下文后调宿主直执（M4e：按猫解析；执行后恢复）。仅 host-* 延迟直执使用。
        /// </summary>
        /// <param name="name">工具名</param>
        /// <param name="args">参数 JSON</param>
        /// <returns>执行结果</returns>
        private string RunTool(string name, string args)
        {
            string prev = ToolCatContext.CurrentCatKey;
            ToolCatContext.SetCat(_catKey);
            try
            {
                return _executeTool(name, args);
            }
            finally
            {
                ToolCatContext.SetCat(prev);
            }
        }

        /// <summary>会话视图出口——实时区序号与全部视图 SSE 直推（Bootstrap 段6 宿主 HTTP 启动后 Attach 注入）</summary>
        private readonly ViewBus _viewBus = new ViewBus();

        /// <summary>环境信息提供器——info 内置工具数据源（入口壳注入；空=工具返回不可用）</summary>
        private Func<string> _envInfoProvider;

        /// <summary>本轮结束通知回调——入口壳注入（Q：托盘 BalloonTip——displayName + reply 摘要/Token 统计）</summary>
        private Action<string, string> _roundNotify;

        /// <summary>
        /// 建立会话实体——宿主级服务经构造注入；会话生命周期数据自持。
        /// </summary>
        /// <param name="id">会话 ID（时间戳注入）</param>
        /// <param name="displayName">显示名</param>
        /// <param name="context">消息历史容器</param>
        /// <param name="store">前文落盘</param>
        /// <param name="llmRuntime">LLM 运行时</param>
        /// <param name="oa">OA 工单平台</param>
        /// <param name="tools">工具定义表</param>
        /// <param name="executeTool">工具直执回调</param>
        /// <param name="viewStore">视图存储——F4 视图持久化</param>
        public ChatSession(string id, string displayName, ChatContext context, SessionStore store, ILlmRuntime llmRuntime, OA oa, ToolSpec[] tools, Func<string, string, string> executeTool, SessionViewStore viewStore)
        {
            if (id == null)
            {
                id = "";
            }
            if (displayName == null)
            {
                displayName = "";
            }
            _id = id;
            _displayName = displayName;
            _context = context;
            _store = store;
            _viewStore = viewStore;
            _llmRuntime = llmRuntime;
            _oa = oa;
            _tools = FilterPrivilegedSpecs(tools);
            _executeTool = executeTool;
            _dogs = new List<ToolOrderDog>();
            _hostDogs = new List<ToolOrderDog>();
            _batches = new List<List<ToolOrderDog>>();
            _batchIndex = -1;
            _pending = new Queue<PendingMessage>();
            _sysPending = new Queue<PendingMessage>();
            _phase = ChatPhase.Idle;
            _round = 0;
            _phaseFrames = 0;
            _llmResultText = "";
            _llmReasoning = "";
            _llmToolCallsJson = "";
            _llmErrorText = "";
        }

        /// <summary>真实前文末条消息——append 后取最近一条生成视图块（F4 视图钩子）</summary>
        private LlmMessage LastMessage()
        {
            LlmMessage[] all = _context.GetMessages();
            return all[all.Length - 1];
        }

        /// <summary>当前视图时间戳——Unix 毫秒（视图排序键——真实时序权威，跨重启稳定；替代宿主帧号）</summary>
        private static long ViewTimestamp()
        {
            return DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
        }

        /// <summary>
        /// 载入视图层——读回持久块与独立块（A156：载入即权威，不再从前文重建）。
        /// 载入后跑对账抽样哨兵（条目来源索引 / 角色一致性核验）。
        /// </summary>
        public void LoadView()
        {
            _viewStore.Load();
            _viewStore.AuditEntries(_context.GetMessages(), 5);
        }
        /// <summary>
        /// 视图层播种——fork（session.fork）：从源会话视图复制 ≤ 切点的条目（新猫视图面 = 源视图保留面；写盘）。
        /// 与播种前文（seedMessages）同批调用——消息索引坐标系一致，条目原样沿用。
        /// </summary>
        /// <param name="source">源会话</param>
        /// <param name="msgIndex">切点前文索引</param>
        public void SeedViewFrom(ChatSession source, int msgIndex)
        {
            if (source == null)
            {
                return;
            }
            _viewStore.CopyFrom(source._viewStore, msgIndex);
            _viewStore.AuditEntries(_context.GetMessages(), 5);
        }
        /// <summary>
        /// 会话元数据播种——fork（session.fork）：新实例 ID / 新创建时刻 + 会话级 token 累计继承 +
        /// 工具面快照继承（A221：会话工具头与 system prompt 同命）+ 轮级面归零（新会话起点）。
        /// Note 不继承——防双猫抢跑同一计划；落盘。
        /// </summary>
        /// <param name="source">源会话</param>
        public void SeedMetaFrom(ChatSession source)
        {
            if (source == null)
            {
                return;
            }
            long now = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
            _sessionInstanceId = SessionStore.NewSessionId();
            _sessionCreatedAt = now;
            _persistedLastActiveAt = now;
            // 会话级 token 累计继承——分支延续同一历史成本（与回滚「会话级累计不动」同口径）
            _sessionPrompt = source._sessionPrompt;
            _sessionCacheHit = source._sessionCacheHit;
            _sessionCompletion = source._sessionCompletion;
            // 轮级面归零——新会话起点无当前轮
            _lastRoundTokens = new SessionTokens();
            _persistedContextTokens = 0;
            _contextTokens = 0;
            _requestCount = 0;
            _toolCallCount = 0;
            lock (_phaseLock)
            {
                _phaseAccumMs[PhaseWait] = 0;
                _phaseAccumMs[PhaseLink] = 0;
                _phaseAccumMs[PhaseThink] = 0;
                _phaseAccumMs[PhaseTool] = 0;
                _phaseAccumMs[PhaseRun] = 0;
                _phaseAccumMs[PhaseReply] = 0;
            }
            // 工具面快照继承——源会话注入面原样沿用（池面回落由构造期 ResolveSessionTools 完成）
            if (source._tools != null)
            {
                ToolSpec[] tools = new ToolSpec[source._tools.Length];
                for (int i = 0; i < tools.Length; i = i + 1)
                {
                    tools[i] = source._tools[i];
                }
                _tools = tools;
            }
            SaveMeta();
        }
        /// <summary>
        /// 旧会话留档——session.new 清空前导出（A87：user 消息 / 正式回复 / 注入报告 / 每轮结算 → sessions_old 下 MD 文件）。
        /// </summary>
        /// <returns>落盘文件绝对路径（空串=未生成——失败已由视图层记 ERR 日志）</returns>
        public string ArchiveLegacyView()
        {
            string path = _viewStore.ArchiveLegacy(_id, _displayName);
            if (path.Length > 0)
            {
                LogStore.Add("CatHome4", 1, "旧会话留档: " + path, "CHAT");
            }
            return path;
        }

        /// <summary>清空视图层——session.new 清前文时同步（视图随生命周期清理）</summary>
        public void ClearView()
        {
            _viewStore.Clear();
        }

        /// <summary>
        /// 消息落盘——上下文追加后立即 append（A47 增量落盘：每消息完成即落盘，崩溃只影响最后一行）。
        /// </summary>
        /// <param name="msg">追加的消息（可空=未入上下文）</param>
        private void AppendMessage(LlmMessage? msg)
        {
            if (msg == null || _store == null)
            {
                return;
            }
            _store.Append(msg.Value);
        }

        /// <summary>
        /// 会话重置显式事件——session.new 清前文后调用（视图层两区全清 + 置全量待发：帧轮广播全量帧，前端整体重绘空态）。
        /// </summary>
        public void PushSessionReset()
        {
            _viewBus.ResetAll();
        }

        /// <summary>
        /// 重置统计——session.new 清前文后调用（新会话零统计起算；会话级 token 累计 + 前文长度同归零）。
        /// A202：会话元数据面与其余两面同批即时落盘（写新会话初始态）。
        /// </summary>
        public void ResetStats()
        {
            // 会话级 token 累计——新会话唯一归零点（轮级由 ResetRoundCounters 逐轮清零；回滚不清——同会话延续）
            _sessionPrompt = 0;
            _sessionCompletion = 0;
            _sessionCacheHit = 0;
            // A201 会话元数据——新实例 ID + 新创建时刻 + 两级快照清零（会话生命周期重新起算）
            _sessionInstanceId = SessionStore.NewSessionId();
            _sessionCreatedAt = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
            _lastRoundTokens = new SessionTokens();
            // 前文长度——请求级实时值 + 落盘快照同归零（读面不回落旧会话值；新会话尚无请求）
            _contextTokens = 0;
            _persistedContextTokens = 0;
            _usagePrompt = 0;
            _usageCompletion = 0;
            _usageCacheHit = 0;
            // 元数据落盘——新会话初始态（contextCount / chars 取当前注入后的前文）
            SaveMeta();
        }

        /// <summary>
        /// 前文条数——送入 LLM 的消息数（含注入块）。
        /// 消费面：图片包裹编号前缀（组装时刻取真值）+ 前端「前文 n 条」文案口径（HTTP 线程读取，只读计数）。
        /// </summary>
        internal int ContextCount
        {
            get
            {
                return _context.GetMessageCount();
            }
        }

        /// <summary>
        /// 最后一次前文变动时刻——Unix 毫秒（0=从未变动）；展示层自行格式化「距今」。
        /// 数据源 = ChatContext.LastChangeAt（含工具结果写入；恢复导入不刷新）。消费面：info 自查 / 快照 sessions 段。
        /// </summary>
        public long LastContextChangeAt
        {
            get
            {
                return _context.LastChangeAt;
            }
        }

        /// <summary>
        /// 已知最后活跃时刻——内存前文变动时刻优先；为 0（恢复导入不刷新）回落落盘快照，再为 0 回落创建时刻。
        /// 控制台「最后活跃」统一消费本属性（A225b——通道不再空置）。
        /// </summary>
        public long LastActiveAtKnown
        {
            get
            {
                if (_context.LastChangeAt > 0) { return _context.LastChangeAt; }
                if (_persistedLastActiveAt > 0) { return _persistedLastActiveAt; }
                return _sessionCreatedAt;
            }
        }

        /// <summary>
        /// 实时前文长度——最近一次请求的 prompt（请求级；每次 usage 帧覆盖）。
        /// 对照 _persistedContextTokens（落盘快照——最后一次请求边界值）；info / cat.info 消费 ContextTokensKnown（实时优先 + 落盘回落）。
        /// </summary>
        public long ContextTokens
        {
            get
            {
                return _contextTokens;
            }
        }
        /// <summary>
        /// 已知最新前文长度——请求级实时值优先，未发起过请求（或宿主重启后未请求）回落落盘快照（A202：最后一次请求边界态）。
        /// 消费面：info tokens 段 / cat.info 每猫 context 字段——「看别猫」场景需要 idle 猫也有可读值（两者皆真实 usage 值，零估算）。
        /// </summary>
        public long ContextTokensKnown
        {
            get
            {
                if (_contextTokens > 0)
                {
                    return _contextTokens;
                }
                return _persistedContextTokens;
            }
        }

        /// <summary>
        /// 设置注入报告——session.new 后调用（视图层普通块：建条即 append 落盘；覆写语义——同型旧块替换）。
        /// </summary>
        /// <param name="json">注入报告 JSON（BuildInjectReportJson 产物）</param>
        public void SetInjectReport(string json)
        {
            _viewStore.SetInjectReport(json);
        }

        /// <summary>内存视图块——按生成序（history 数据源；F4 视图持久化）</summary>
        public ViewBlock[] GetViewBlocks()
        {
            return _viewStore.GetBlocks();
        }

        /// <summary>会话唯一 ID——构造注入 = 猫 key（唯一标识；无独立"会话身份"层）</summary>
        public string Id
        {
            get
            {
                return _id;
            }
        }

        /// <summary>会话显示名（P9.3 前端可见）</summary>
        public string DisplayName
        {
            get
            {
                return _displayName;
            }
        }

        /// <summary>消息历史——会话操纵面复用（视图/清空/计数）</summary>
        public ChatContext Context
        {
            get
            {
                return _context;
            }
        }

        /// <summary>视图存储——视图块读取面（Admin 装配 QQBot 转发/历史数据源；internal——IVT 域消费）</summary>
        internal SessionViewStore ViewStore
        {
            get
            {
                return _viewStore;
            }
        }

        /// <summary>前文落盘——会话操纵面复用（session.new 重注入落盘）</summary>
        public SessionStore Store
        {
            get
            {
                return _store;
            }
        }

        /// <summary>工具批执行中——reload 拒绝检查（任一会话 TRUE 即拒绝）</summary>
        public bool ToolBatchActive
        {
            get
            {
                return _toolBatchActive;
            }
        }

        /// <summary>
        /// 更新工具注入面——session.new 时从最新 cat.cfg 重裁剪后调用（会话级生效；授权面实时查询见 IsToolAllowed——design-ch4-tools §三·十一）。
        /// </summary>
        /// <param name="specs">新注入面数组（FilterToolSpecs 产物）</param>
        public void SetToolSpecs(ToolSpec[] specs)
        {
            _tools = FilterPrivilegedSpecs(specs);
            // A221 新会话换面——快照随新会话同步落盘（与 system prompt 同批；design-ch4-tools §三·十一）
            SaveMeta();
        }
        /// <summary>
        /// 会话注入面工具名——_tools 快照派生（info tools_drift 左值：与本猫当前授权集比对；design-ch4-tools §三·十一）。
        /// </summary>
        public string[] DeclaredToolNames
        {
            get
            {
                if (_tools == null)
                {
                    return new string[0];
                }
                string[] names = new string[_tools.Length];
                for (int i = 0; i < _tools.Length; i = i + 1)
                {
                    names[i] = _tools[i].Name;
                }
                return names;
            }
        }
        /// <summary>
        /// 默认会话判定——特权面（majordomo-*）可见与可调的授权基准。
        /// 判据：猫 key = majordomo（创建方 SetCatKey 后成立）；构造期 catKey 未赋值，以显示名兜底（默认猫 displayName = majordomo）。
        /// 设计依据：design-ch4-host-restart §二——特权面是单点授权，判据不得取运行时会话 ID（时间戳）。
        /// </summary>
        private bool IsDefaultSession
        {
            get
            {
                if (_catKey == "majordomo")
                {
                    return true;
                }
                return _catKey.Length == 0 && _displayName == "majordomo";
            }
        }
        /// <summary>特权面可见性过滤——非默认会话剔除全部特权工具（判据 = 注册面组级标记，见 IsPrivilegedTool；默认会话判定见 IsDefaultSession）。设计依据：声明面（cat.cfg toolNames）的全量保底语义是「防外部损坏」，不构成授权通道（design-ch4-host-restart §二）——故特权工具在会话内单点剔除，与 IsToolAllowed 拦截构成双面。</summary>
        /// <param name="specs">原始工具面</param>
        /// <returns>过滤后的工具面（默认会话原样返回）</returns>
        private ToolSpec[] FilterPrivilegedSpecs(ToolSpec[] specs)
        {
            if (specs == null || IsDefaultSession)
            {
                return specs;
            }
            List<ToolSpec> kept = new List<ToolSpec>();
            for (int i = 0; i < specs.Length; i = i + 1)
            {
                if (!IsPrivilegedTool(specs[i].Name))
                {
                    kept.Add(specs[i]);
                }
            }
            return kept.ToArray();
        }
        /// <summary>特权工具判定——读注册面组级标记（ToolRegistry 单一真相源：工具定义 JSON 根级 privileged；未注册 = 非特权）。</summary>
        /// <param name="name">工具名</param>
        /// <returns>true=特权（仅主干会话可见可调）</returns>
        private static bool IsPrivilegedTool(string name)
        {
            ToolRegistryEntry entry = ToolRegistry.Find(name);
            if (entry == null)
            {
                return false;
            }
            return entry.Privileged;
        }

        /// <summary>
        /// 换 LLM 运行时——apiConfigId 切换立即生效（M3：API 配置切换即时；主线程 Idle 时调用）。
        /// </summary>
        /// <param name="runtime">新运行时实例（按新 API 配置构造）</param>
        public void SwapLlmRuntime(ILlmRuntime runtime)
        {
            _llmRuntime = runtime;
        }

        /// <summary>会话空闲——AllIdle 判定面</summary>
        public bool IsIdle
        {
            get
            {
                return _phase == ChatPhase.Idle;
            }
        }

        /// <summary>当前相位（快照观测面）</summary>
        public ChatPhase Phase
        {
            get
            {
                return _phase;
            }
        }

        /// <summary>当前工具轮次（快照观测面——状态卡预览用）</summary>
        public long Round
        {
            get
            {
                return _round;
            }
        }

        /// <summary>消息历史条数（快照观测面——状态卡预览用）</summary>
        public int MsgCount
        {
            get
            {
                return _context.GetMessages().Length;
            }
        }

        /// <summary>待处理消息数（快照观测面——忙时排队可见）</summary>
        public int PendingCount
        {
            get
            {
                return _pending.Count + _sysPending.Count;
            }
        }

        /// <summary>Note 计划激活中——任务未全完成（快照观测面——状态卡 Note 徽标）</summary>
        public bool NoteActive
        {
            get
            {
                return _noteTasks != null && _noteCurrent < _noteTasks.Length;
            }
        }

        /// <summary>
        /// 附加宿主推送面——Bootstrap 段6 宿主 HTTP 启动后调用（转交视图出口；SSE 转发面就位）。
        /// </summary>
        /// <param name="host">HTTP 外观层实例</param>
        public void AttachHost(IHostPush host)
        {
            _viewBus.Attach(host);
            _viewStore.OnBlockAppended = _viewBus.PushPersist;
            // 全量数据源接线（2026-10-03）——视图存储是唯一权威：全量帧每次现取（GetBlocks），
            // 输出侧不持副本（副本必与权威分叉——载入 / 清空 / 回滚都是分叉点；判例：刷新丢历史）。
            _viewBus.AttachSource(_viewStore.GetBlocks);
        }

        /// <summary>连接数提供器——state 段 conn.clients 数据源（入口壳注入 host.ClientCount；未注入 = 0）</summary>
        private Func<int> _clientCountProvider;

        /// <summary>
        /// 注入连接数提供器——连接健康字段数据源（state 段 conn.clients；入口壳注入 host.ClientCount）。
        /// 连接健康是「服务端知道而前端不知道」的信息（重启停机中 / 多页面连接）；
        /// 断线可见性不由此字段承担（断线后收不到帧）——那属外观层本地信号（契约 §12.2 / §12.8）。
        /// </summary>
        /// <param name="provider">连接数提供委托</param>
        public void AttachConnInfo(Func<int> provider)
        {
            _clientCountProvider = provider;
        }

        /// <summary>
        /// 注入环境信息提供器——info 内置工具数据源（拆分后由入口壳注入；原直接调用 Program.BuildEnvInfo）
        /// </summary>
        /// <param name="provider">环境信息构建委托</param>
        public void AttachEnvInfo(Func<string> provider)
        {
            _envInfoProvider = provider;
        }

        /// <summary>
        /// 注入本轮结束通知回调——CloseRound 正常收尾时调用（Q：系统通知 Tip——displayName + 末轮回复摘要；入口壳接托盘 BalloonTip）。
        /// </summary>
        /// <param name="notify">通知回调（标题 + 正文）</param>
        public void AttachRoundNotify(Action<string, string> notify)
        {
            _roundNotify = notify;
        }

        /// <summary>
        /// 中止当前轮次——取消 LLM 流（后台 Task 取消）/ 放弃未完成工具批；已完成内容保留 + 前文格式修复（不裁剪——裁剪唯一通道是回滚）。
        /// 线程安全：仅置位 + Cancel（volatile 标志跨线程可见）；相位推进在主线程 Pump 消费。
        /// </summary>
        public void Pause()
        {
            if (_phase == ChatPhase.Idle)
            {
                // 无进行中对话——前端按钮 idle 态禁用；防御性返回
                return;
            }
            _pauseRequested = true;
            if (_pauseCts != null)
            {
                _pauseCts.Cancel();
            }
            LogStore.Add("CatHome4", 1, "收到中止指令——正在停止本轮（已完成内容保留）", "CHAT");
        }
        /// <summary>
        /// 继续——不追加任何用户消息，直接用当前前文发起一次 LLM 请求（模型续写；空回复续传同上下文语义的显式入口）。
        /// 线程安全：前文非空校验 + 入队（相位推进归主线程 Pump——Idle 才消费，忙时按插话队列语义排队）。
        /// </summary>
        public void Continue()
        {
            if (_context.GetMessageCount() == 0)
            {
                // 空前文无可续内容——前端按钮同条件禁用；此处防御性出声（不静默吞掉）
                LogStore.Add("CatHome4", 2, "继续指令未受理——前文为空", "CHAT");
                return;
            }
            // 跨线程只置位（与 Pause / SessionNewRequested 同模式）——HTTP 线程不得直接触碰会话内部队列；
            // 轮次启动归主线程 Pump Idle 分支消费（Idle 才启动，忙时等同排队）。
            _continueRequested = true;
            LogStore.Add("CatHome4", 1, "收到继续指令——本轮结束后启动继续轮（不追加消息，用当前前文再发一次请求）", "CHAT");
        }

        /// <summary>中止收尾——主线程 Pump 消费（相位串行）。已完成工具结果保留（不丢信息）/未完成放弃+格式修复/前文落盘/复位 Idle + paused 事件；实现委托 FinalizeInterrupted（中断收尾公共实现）。</summary>
        private void PauseFinalize()
        {
            _pauseRequested = false;
            FinalizeInterrupted("本轮已中止——前文保留 + 格式修复");
        }
        /// <summary>
        /// 中断收尾公共实现——已完成工具结果保留 + 上下文格式修复 + 截断落盘 + 序号复位 + 状态复位 Idle + 状态段推送。
        /// 调用面：PauseFinalize（用户中止）——宿主重启不再走强制中断（A72：本轮常规结束，见 RestartRequest）。
        /// </summary>
        /// <param name="logPrefix">日志前缀（实现追加落盘条数）</param>
        private void FinalizeInterrupted(string logPrefix)
        {
            // [段0] 已完成工具结果保留——未完成放弃（ReplaceMessages 对未配对声明补占位）+ 先行卡补终态（完成/已中止）
            for (int i = 0; i < _dogs.Count; i = i + 1)
            {
                ToolOrderDog dog = _dogs[i];
                bool done = dog.IsClosed && dog.Result != null && dog.Result.Length > 0;
                if (done)
                {
                    AppendMessage(_context.AddToolResult(dog.ToolCallId, dog.Name, dog.Result));
                    NoteTimebackEvent();
                    NoteTimebackWrite(dog.Name, dog.ArgsJson, dog.Result);
                }
            }
            _dogs.Clear();
            _hostDogs.Clear();
            _batches.Clear();
            _batchIndex = -1;
            _toolBatchActive = false;
            // [段1] 上下文格式修复——S3 ReplaceMessages 原地（幂等；孤儿 tool_calls 补占位/孤立结果丢弃）
            _context.ReplaceMessages(_context.GetMessages());
            // [段2] 前文落盘——落盘保真（消息行随追加即落盘；A202 起元数据行不再承载统计）
            // [段2b] 运行态——中断结算（失败/中止轮同出统计：L2 摘要留档——design-ch4-llm §2.1 终止语义）
            PhaseSettle();
            LogStore.Add("LLM", 2, "本轮运行态统计（中断）: " + BuildRunStateSummary(), "LLM");
            // [段3] 临时区写空态（A196：live 是状态投影——未落持久块的内容随覆盖而消失，不进前文 / 存档）
            SealReasonStream();
            ResetRetryView();
            // [段4] 状态复位——Idle（不推 roundsum/Note 拉起——中断非正常完成语义）
            _round = 0;
            _phase = ChatPhase.Idle;
            _phaseFrames = 0;
            _llmBusy = false;
            _llmError = false;
            _llmErrorText = "";
            _llmResultText = "";
            _llmReasoning = "";
            _llmToolCallsJson = "";
            _emptyReplyRetry = 0;
            _streamClosedRetry = false;
            SetChatState("idle");
            // [段5] 状态段推送——按钮复位与运行态就位（v2：状态段承载业务态，无独立事件）
            PushState();
        }

        /// <summary>
        /// 宿主重启请求登记（A72——design-ch4-delay §7.2）：登记重启请求 + 置停机态，**不中断本轮**。
        /// 本轮照常续 LLM 轮并正常 CloseRound 完整结算（roundsum / 前文落盘齐全）；
        /// 停机态拒收新输入（在途排队消息丢弃并出声）；全局 Idle 后由主循环闸门执行接力（design-ch4-host-restart §三 T2/T3）。
        /// </summary>
        /// <param name="requestJson">重启请求 JSON（target/push，由 majordomo 工具组积木落盒）</param>
        private void RestartRequest(string requestJson)
        {
            DataBox.Set<string>("global", "host_restart_request", requestJson);
            DataBox.Set<string>("global", "host_restart_state", "requested");
            LogStore.Add("CatHome4", 1, "宿主重启请求已登记——本轮照常收尾（停机态拒收新输入），等待全局空闲", "RESTART");
        }
        /// <summary>重启族工具名单——请求判定的单点真相源（批次结果检测、timeback 暴毙黑名单共用同一清单；改名 / 增件只动此处）</summary>
        internal static readonly string[] RestartToolNames = new string[] { "restart-full", "restart-incr", "restart-host" };
        /// <summary>
        /// 是否重启族工具——判定单点（不散落字符串比较）。
        /// </summary>
        /// <param name="name">工具名</param>
        /// <returns>true=重启族</returns>
        internal static bool IsRestartTool(string name)
        {
            if (name == null || name.Length == 0)
            {
                return false;
            }
            for (int i = 0; i < RestartToolNames.Length; i = i + 1)
            {
                if (name == RestartToolNames[i])
                {
                    return true;
                }
            }
            return false;
        }

        /// <summary>
        /// 宿主重启停机态判定——DataBox 全局盒（键与 Program.Restart 同源）。
        /// </summary>
        /// <returns>true=停机态（拒收新输入）</returns>
        private static bool IsHostRestarting()
        {
            string state;
            if (DataBox.TryGet<string>("global", "host_restart_state", out state))
            {
                return state == "requested";
            }
            return false;
        }

        /// <summary>
        /// 停机态丢弃在途**系统注入**（A72 · A232 收窄）——宿主即将重启，不再开新轮；丢弃并出声（不静默）。
        /// 人工队列（_pending）自 A232 起跨重启保留（入队即物化），不在本方法清理面。
        /// </summary>
        private void DropPendingIfRestarting()
        {
            // A232——人工队列（_pending）跨重启保留：入队即物化，重启后由 LoadPendingFromDisk 恢复；
            // 本方法只清 sys 队列（系统注入生命周期短，不跨重启保留）。停的是「新输入受理」（见 PostUserMessage），
            // 不是「已受理消息」。
            int sysCount = _sysPending.Count;
            if (sysCount == 0)
            {
                return;
            }
            if (!IsHostRestarting())
            {
                return;
            }
            _sysPending.Clear();
            LogStore.Add("CatHome4", 2, "宿主重启停机态——丢弃在途系统注入 " + sysCount.ToString() + " 条", "RESTART");
        }
        /// <summary>待处理消息合并排序键——入队序号升序（到达序）。</summary>
        /// <param name="a">条目 A</param>
        /// <param name="b">条目 B</param>
        /// <returns>比较结果</returns>
        private static int ComparePendingBySeq(PendingMessage a, PendingMessage b)
        {
            return a.Seq.CompareTo(b.Seq);
        }
        /// <summary>两队列按到达时间序合并取尽（user / sys 平行路径）。</summary>
        /// <param name="includeUser">true=取人工队列（Idle 启动 / 未开启空闲注入）；false=仅取 sys 队列</param>
        /// <returns>按入队序升序的消息清单</returns>
        private List<PendingMessage> TakeAllPendingByTime(bool includeUser)
        {
            List<PendingMessage> all = new List<PendingMessage>();
            // 人工队列——空闲注入开启时轮内不取（仅 Idle 启动取）
            bool tookUser = false;
            if (includeUser)
            {
                while (_pending.Count > 0)
                {
                    all.Add(_pending.Dequeue());
                    tookUser = true;
                }
            }
            while (_sysPending.Count > 0)
            {
                all.Add(_sysPending.Dequeue());
            }
            all.Sort(ComparePendingBySeq);
            // A232——消费即移除（人工队列被取走即重写落盘；仅本趟真取走人工条目时写——空队列零写）
            if (tookUser)
            {
                SavePendingToDisk();
            }
            return all;
        }
        /// <summary>本轮边界——两队列按到达序取尽并插入前文（user / sys 双路径；同注入点多条一并）。</summary>
        /// <returns>true=有插入（调用方据此续轮）</returns>
        private bool FlushPendingIntoRound()
        {
            List<PendingMessage> pending = TakeAllPendingByTime(!_userInjectIdleOnly);
            if (pending.Count == 0)
            {
                return false;
            }
            for (int i = 0; i < pending.Count; i = i + 1)
            {
                AppendInjection(pending[i].Content, pending[i].Source, pending[i].ImagesJson, pending[i].Origin);
            }
            return true;
        }

        /// <summary>
        /// sleep 作废——主干被「非 sleep 输入」启动时，本猫未到点 sleep 全部销毁（等待语义：人回来了就不再需要叫醒）。
        /// 返回告知文本（调用方以系统注入口径入队落前文——FIFO 保证其位于触发消息之前）。
        /// 触发点：轮首取尽之前（唯一入口——含 QQ 消息 / Note 拉起 / timer / delay / restart 回执）。
        /// </summary>
        /// <param name="triggerSource">触发本轮的输入来源（sleep=自身到点，不销毁）</param>
        /// <returns>告知文本（空串=无作废 · 无需注入）</returns>
        private string ConsumeSleepOnWake(string triggerSource)
        {
            if (triggerSource == "sleep")
            {
                return "";
            }
            DelayEntry[] killed = DelayQueue.CancelBySource(_catKey, "sleep");
            if (killed.Length == 0)
            {
                return "";
            }
            StringBuilder sb = new StringBuilder();
            sb.Append("（系统自动 · sleep 作废）等待被提前启动打断——以下定时唤醒已销毁（共 ");
            sb.Append(killed.Length.ToString());
            sb.Append(" 条）：");
            for (int i = 0; i < killed.Length; i = i + 1)
            {
                sb.Append("\n  #");
                sb.Append(killed[i].Id.ToString());
                sb.Append(" 原定 ");
                sb.Append(DelayQueue.FormatTime(killed[i].DueAt));
                sb.Append(" 到期");
            }
            LogStore.Add("CatHome4", 1, "sleep 作废（等待被提前启动打断）: cat=" + _catKey + " | 销毁 " + killed.Length.ToString() + " 条 | 触发来源 " + triggerSource, "DELAY");
            return sb.ToString();
        }

        /// <summary>
        /// 用户消息入队——Idle 时 Pump 立即启动轮次；忙时排队等待（原同步阻塞天然排队语义保持）。
        /// 主线程泵消费调用（ThreadGuard：不推进相位——启动在 Pump）。
        /// </summary>
        /// <param name="content">用户消息内容</param>
        /// <param name="source">来源——user（人发送，默认）/ systemauto · sleep · timer · delay · restart（系统注入——视图层 src 一律投影为 systemauto）</param>
        /// <param name="imagesJson">附件引用 JSON 数组（图片绝对路径；空串=无附件——图片注入经队列携带）</param>
        /// <returns>true=已受理入队 / false=未受理（空内容 · 宿主停机态）</returns>
        /// <param name="origin">原注入来源标记——QQ 注入的轮起点锚（空=非 QQ 注入 / 系统注入）；输出转发面据此把来源绑定到该消息开启的轮</param>
        public bool PostUserMessage(string content, string source = "user", string imagesJson = "", string origin = "")
        {
            if (content == null)
            {
                content = "";
            }
            if (imagesJson == null)
            {
                imagesJson = "";
            }
            if (origin == null)
            {
                origin = "";
            }
            // 空输入零受理——文本与附件皆空才拒（图片注入可无正文，故两者合并判定）
            if (content.Length == 0 && imagesJson.Length == 0)
            {
                return false;
            }
            if (source == null || source.Length == 0)
            {
                source = "user";
            }
            // [段1] 宿主重启停机态——拒收一切新需求（design-ch4-host-restart §三 T2：停机后等的是在途轮次，不是等人）
            string restartState;
            if (DataBox.TryGet<string>("global", "host_restart_state", out restartState) && restartState == "requested")
            {
                LogStore.Add("CatHome4", 2, "宿主重启中——本条输入未受理（来源 " + source + "）", "RESTART");
                return false;
            }
            PendingMessage msg = new PendingMessage();
            msg.Content = content;
            msg.Source = source;
            msg.ImagesJson = imagesJson;
            msg.Origin = origin;
            msg.Seq = _pendingSeq;
            _pendingSeq = _pendingSeq + 1;
            _pending.Enqueue(msg);
            // A232——入队即物化（跨宿主重启保留）
            SavePendingToDisk();
            return true;
        }

        /// <summary>
        /// 一帧推进——主线程每帧调用（PumpSessions 轮转）。相位分发：Idle 检查排队 / Llm 轮询完成槽 / 工具批轮询 Dog / 终态落盘复位。
        /// </summary>
        public void Pump()
        {
            // A254 会话内部分段埋点——cs.* 段进 LoopTiming（与主循环段同表展示在资源页签）
            long segTicks = Stopwatch.GetTimestamp();
            // RoundSummary——在途总结工单回收（每帧；无在途零成本返回）
            PumpRoundSummary();
            LoopTiming.Accumulate("cs.summary", ElapsedMsSince(segTicks));
            // P6 中止——暂停请求消费（任何相位统一收尾：LLM 流取消/工具批放弃/已完成保留）
            if (_pauseRequested)
            {
                segTicks = Stopwatch.GetTimestamp();
                PauseFinalize();
                LoopTiming.Accumulate("cs.pause", ElapsedMsSince(segTicks));
                return;
            }
            if (_phase == ChatPhase.Idle)
            {
                segTicks = Stopwatch.GetTimestamp();
                // A72——停机态丢弃在途排队消息（宿主即将重启，不再开新轮）
                DropPendingIfRestarting();
                // 继续——HTTP 线程置位、主线程消费（与 Pause / SessionNewRequested 同模式）：Idle 才启动继续轮
                if (_continueRequested)
                {
                    _continueRequested = false;
                    if (IsHostRestarting())
                    {
                        LogStore.Add("CatHome4", 2, "宿主重启中——继续请求未受理", "RESTART");
                        LoopTiming.Accumulate("cs.idle", ElapsedMsSince(segTicks));
                        return;
                    }
                    // sleep 作废——继续属「非 sleep 输入」（与常规轮同口径）
                    ConsumeSleepOnWake("continue");
                    StartContinueRound();
                    LoopTiming.Accumulate("cs.idle", ElapsedMsSince(segTicks));
                    return;
                }
                if (_pending.Count > 0 || _sysPending.Count > 0)
                {
                    // 轮首取尽——本帧排队条目全部落前文（FIFO）后启一轮；sleep 作废告知置最前
                    StartRoundFromPending();
                }
                LoopTiming.Accumulate("cs.idle", ElapsedMsSince(segTicks));
                return;
            }
            if (_phase == ChatPhase.LlmRunning)
            {
                segTicks = Stopwatch.GetTimestamp();
                PumpLlm();
                LoopTiming.Accumulate("cs.llm", ElapsedMsSince(segTicks));
                return;
            }
            if (_phase == ChatPhase.ToolBatchRunning)
            {
                segTicks = Stopwatch.GetTimestamp();
                PumpToolBatch();
                LoopTiming.Accumulate("cs.tool", ElapsedMsSince(segTicks));
                return;
            }
            if (_phase == ChatPhase.Done)
            {
                segTicks = Stopwatch.GetTimestamp();
                CloseRound();
                LoopTiming.Accumulate("cs.close", ElapsedMsSince(segTicks));
            }
        }
        /// <summary>
        /// 读自指定时刻起的毫秒差——会话内部分段埋点用（A254）。
        /// </summary>
        /// <param name="startTicks">起始 Stopwatch 时间戳</param>
        /// <returns>毫秒数</returns>
        private static double ElapsedMsSince(long startTicks)
        {
            return (Stopwatch.GetTimestamp() - startTicks) * 1000.0 / Stopwatch.Frequency;
        }
        /// <summary>
        /// 轮首计数与相位复位——StartRoundFromPending 与 StartContinueRound 共用（统计清零 + 进入 link 相位；消息追加由各自承担）。
        /// </summary>
        private void ResetRoundCounters()
        {
            // E3 Token 统计——整轮清零（工具续轮 LaunchLlm 不清——跨轮累加语义）
            _usagePrompt = 0;
            _usageCompletion = 0;
            _usageCacheHit = 0;
            // roundsum 统计——单次前文/工具计数/七态计时清零 + 请求计数清零 + 轮次起表 + 进入 link 相位
            _contextTokens = 0;
            _toolCallCount = 0;
            _requestCount = 0;
            // 空回复续传计数——整轮清零（同 CH2 [段2.3] 每轮独立语义）
            _emptyReplyRetry = 0;
            _streamClosedRetry = false;
            _toolDone = false;
            // RoundSummary——本轮输入缓冲清零（全部 user / 全部 text 按序累积）
            _roundSummaryUserBuf.Clear();
            _roundSummaryTextBuf.Clear();
            _roundStartTick = System.Diagnostics.Stopwatch.GetTimestamp();
            lock (_phaseLock)
            {
                _phaseAccumMs = new long[PhaseCount];
                _phaseKind = -1;
                _phaseStartTick = 0;
            }
            PhaseEnter(PhaseLink);
        }
        /// <summary>
        /// 启动继续轮——不追加任何消息，直接用当前前文发起请求（继续入口 pump 消费点）。
        /// 与 StartRoundFromPending 的差别仅在「不追加用户消息 / 不推 user 事件」（无新消息即无新视图块）。
        /// </summary>
        private void StartContinueRound()
        {
            ResetRoundCounters();
            SetChatState("working");
            LaunchLlm();
        }
        /// <summary>
        /// 轮首取尽——本帧排队条目全部落前文（FIFO 序）后启动一轮；sleep 作废告知置于最前（先于触发消息）。
        /// </summary>
        private void StartRoundFromPending()
        {
            List<PendingMessage> batch = TakeAllPendingByTime(true);
            if (batch.Count == 0)
            {
                return;
            }
            // 轮首计数与相位复位（与继续轮共用）
            ResetRoundCounters();
            // sleep 作废——主干被「非 sleep 输入」启动即销毁未到点 sleep（等待语义）
            string notice = ConsumeSleepOnWake(batch[0].Source);
            if (notice.Length > 0)
            {
                AppendInjection(notice, SysKindSystemAuto, "");
            }
            for (int i = 0; i < batch.Count; i = i + 1)
            {
                AppendInjection(batch[i].Content, batch[i].Source, batch[i].ImagesJson, batch[i].Origin);
            }
            SetChatState("working");
            LaunchLlm();
        }
        /// <summary>
        /// 消息注入落前文 + 视图——路径分派（sys → systemautomessage 块 + 固定前缀；人工 → user 块）。
        /// </summary>
        /// <param name="content">注入内容（sys 路径在方法内套固定前缀——入口无须预处理）</param>
        /// <param name="source">队列来源标记</param>
        /// <param name="imagesJson">附件引用 JSON 数组（空串=无附件）</param>
        /// <param name="origin">原注入来源标记（空串=非 QQ 注入；非空=QQ 注入的轮起点锚——视图层落载荷 origin 供转发面轮绑定）</param>
        private void AppendInjection(string content, string source, string imagesJson, string origin = "")
        {
            string text = content;
            if (text == null)
            {
                text = "";
            }
            if (origin == null)
            {
                origin = "";
            }
            // 路径分派——sys 来源套固定行头前缀并落 systemautomessage 块；人工路径落 user 块
            bool isSys = IsSysSource(source);
            if (isSys)
            {
                text = BuildSysInjectText(text);
            }
            LlmMessage? msg;
            if (imagesJson != null && imagesJson.Length > 0)
            {
                msg = _context.AddUserMessage(text, imagesJson);
            }
            else
            {
                msg = _context.AddUserMessage(text);
            }
            if (msg == null)
            {
                return;
            }
            AppendMessage(msg.Value);
            int msgIndex = _context.GetMessageCount() - 1;
            if (isSys)
            {
                _viewStore.OnSystemMessage(LastMessage(), msgIndex, source, origin);
            }
            else
            {
                _viewStore.OnUserMessage(LastMessage(), msgIndex, origin);
                // RoundSummary——本轮 user 文本入缓冲（全部 user 按序；sys 注入不计入）
                _roundSummaryUserBuf.AppendLine(text);
            }
        }

        /// <summary>
        /// StartLlm 动作段——消息序列固化 + 槽清零 + 后台 Task.Run 消费（主线程零阻塞；LlmRunning 相位帧计数归零）。
        /// </summary>
        private void LaunchLlm()
        {
            // P6 中止——每轮新取消令牌（上一轮取消不跨轮；Pause 时 Cancel 后台流）
            _pauseCts = new System.Threading.CancellationTokenSource();
            LlmMessage[] messages = _context.GetMessages();
            _llmBusy = true;
            _llmResultText = "";
            _llmReasoning = "";
            _reasonAccum.Clear();
            // 思考计时同弃——思考内容被清（新一轮 / 空回复续传重发），本块用时同步归零
            _reasonDurMs = 0;
            _llmToolCallsJson = "";
            _llmError = false;
            _llmErrorText = "";
            System.Threading.Tasks.Task.Run(delegate
            {
                _ = ConsumeLlmStream(messages, _pauseCts.Token);
            });
            _phase = ChatPhase.LlmRunning;
            _phaseFrames = 0;
            // 运行态——请求发出即链路等待（首轮/续轮/重试重发同路径；长度由远端决定——design-ch4-llm §2.1）
            _requestCount = _requestCount + 1;
            PhaseEnter(PhaseLink);
        }

        /// <summary>
        /// 空回复续传——流成功结束但无回复文本（STREAM_CLOSED 或 content 空）时同上下文重发（CH2 [段2.3] 移植；无上限——LLM 兜底）。
        /// 复用 S2 retry 视图机制：置位 _sawRetry → 续传流首个 Text/Reasoning 到达时前端自动回填 resolved。
        /// </summary>
        /// <param name="reason">续传原因——日志 + retry 视图展示</param>
        private void RetryEmptyReply(string reason)
        {
            _emptyReplyRetry = _emptyReplyRetry + 1;
            _streamClosedRetry = false;
            LogStore.Add("LLM", 2, "空回复续传 第" + _emptyReplyRetry.ToString() + "/∞ 次（" + reason + "）——同上下文重发", "LLM");
            // A94——原文快照齐备：续传路径同样写入三元组（否则 resolved / failed 终态丢失报错原文；原因加中文注释与 Retrying 分支同源）
            _retryAttempt = _emptyReplyRetry.ToString();
            _retryMax = "∞";
            _retryReason = ErrorNote.Apply(reason);
            _sawRetry = true;
            PushRetryView("retrying");
            // 同上下文重发——LaunchLlm 内部清槽 + 帧计数归零（上下文未污染：空文本未入 Ctx/无错误文本入 Ctx）
            LaunchLlm();
        }
        /// <summary>
        /// 构造 retry 视图载荷（A94 统一出口）——报错原文三元组（尝试序号 / 次数上限 / 原因摘要）在所有状态下齐备，
        /// 终态（resolved / failed）只追加状态标注，不覆盖原文。
        /// </summary>
        /// <param name="state">状态（retrying / resolved / failed）</param>
        /// <returns>retry 视图载荷 JSON</returns>
        private string BuildRetryViewJson(string state)
        {
            return JsonUtil.Object(("state", state), ("attempt", _retryAttempt), ("max", _retryMax), ("text", _retryReason));
        }
        /// <summary>
        /// retry 视图推送统一出口（A94；A158 期三改「只增不改」）——构造载荷 + 落盘追加块（推送由建块事件驱动）：
        /// retrying / resolved / failed 各推一块——不做原位更新（持久区只增不改，前端只追加渲染）。
        /// </summary>
        /// <param name="state">状态（retrying / resolved / failed）</param>
        private void PushRetryView(string state)
        {
            string json = BuildRetryViewJson(state);
            long ts = ViewTimestamp();
            _retryPushed = true;
            _viewStore.AppendRetry(json, ts);
        }
        /// <summary>
        /// 重试视图态清零（A94 单一出口）——在途标志 / 原文三元组 / 待回填标志；
        /// 轮终（正常 / 中断 / 错误中止）统一调用，防跨轮污染。
        /// </summary>
        private void ResetRetryView()
        {
            _retryPushed = false;
            _retryAttempt = "";
            _retryMax = "";
            _retryReason = "";
            _sawRetry = false;
        }

        /// <summary>
        /// 后台消费 LLM 流——写会话槽 + SSE 转发 + 结算行。Task.Run 执行——异常全兜底（错误可见性）。
        /// </summary>
        /// <param name="messages">消息序列</param>
        private async System.Threading.Tasks.Task ConsumeLlmStream(LlmMessage[] messages, System.Threading.CancellationToken ct)
        {
            try
            {
                // [段1] 槽预置 + 后台流式消费
                StringBuilder text = new StringBuilder();
                string toolCalls = "";
                await foreach (LlmStreamEvent ev in _llmRuntime.ChatStream(messages, _tools, ResolveCacheIsolationKey(), ct))
                {
                    if (ev.Kind == LlmStreamKind.Text)
                    {
                        // S2 §8.4——重试成功回填：本轮发生过重试且首个 Text 到达 → retry 气泡更新为 resolved（只回填一次）
                        if (_sawRetry)
                        {
                            _sawRetry = false;
                            PushRetryView("resolved");
                        }
                        text.Append(ev.Text);
                        // 运行态——Text 增量到达即回复态（远端·流；长度由远端决定）
                        PhaseEnter(PhaseReply);
                        // A196 临时区——状态投影：回复流当前全文整段覆盖（前端按 type 投给渲染结构）
                        _viewBus.SetLive("replysse", text.ToString());
                    }
                    else if (ev.Kind == LlmStreamKind.Reasoning)
                    {
                        // S2 §8.4——重试成功回填（Reasoning 也是恢复信号）
                        if (_sawRetry)
                        {
                            _sawRetry = false;
                            PushRetryView("resolved");
                        }
                        _reasonAccum.Append(ev.Text);
                        // 运行态——Reasoning 增量到达即思考态（远端·流；长度由远端决定）
                        PhaseEnter(PhaseThink);
                        // A196 临时区——状态投影：思考流当前全文整段覆盖
                        _viewBus.SetLive("thinksse", _reasonAccum.ToString());
                    }
                    else if (ev.Kind == LlmStreamKind.ToolCalls)
                    {
                        // S2 §8.4——重试成功回填（ToolCalls 也是恢复信号——工具轮场景无文本）
                        if (_sawRetry)
                        {
                            _sawRetry = false;
                            PushRetryView("resolved");
                        }
                        toolCalls = ev.Text;
                        // 运行态——ToolCalls（流末聚合）不切态：tool 态已由 ToolCallsStart（首个增量帧）进入；
                        // run 态在发单时进入（PumpLlm 工具分支）——design-ch4-llm §2.1
                        // 视图——"进行中"工具卡在 StartToolBatch 段统一推送（LLM 输出工具即出卡；完成/中断原位替换）
                    }
                    else if (ev.Kind == LlmStreamKind.ToolCallsStart)
                    {
                        // 运行态——工具决策流开始（远端·流：LLM 输出 tool_calls 的流式过程；长度由远端决定）
                        PhaseEnter(PhaseTool);
                    }
                    else if (ev.Kind == LlmStreamKind.RetryResume)
                    {
                        // 运行态——退避结束、重发开始：wait → link（长度重回远端决定）
                        PhaseEnter(PhaseLink);
                    }
                    else if (ev.Kind == LlmStreamKind.Retrying)
                    {
                        // P6 中止——暂停中忽略重试事件（取消被误判为传输错误的防御：不推 retry 气泡——根治在 Runtime catch OCE 冒泡）
                        if (_pauseRequested)
                        {
                            continue;
                        }
                        // 运行态——重试退避开始：link → wait（长度由本地决定——退避时长）
                        PhaseEnter(PhaseWait);
                        // S2 §8.4——重试可见性：独立视图条目（retry renderType）——不入会话槽/上下文/日志（Runtime 已记 L2）
                        _sawRetry = true;
                        // RETRY|N/3|原因摘要 —— 提取尝试序号与原因（原因摘要自身含 | —— 取第二个分隔符之后全段）
                        string retryText = ev.Text;
                        string attempt = "";
                        string max = "";
                        string reason = retryText;
                        int sep1 = retryText.IndexOf('|');
                        int sep2 = sep1 < 0 ? -1 : retryText.IndexOf('|', sep1 + 1);
                        if (sep2 > 0)
                        {
                            string seg = retryText.Substring(sep1 + 1, sep2 - sep1 - 1);
                            string[] am = seg.Split('/');
                            if (am.Length >= 2)
                            {
                                attempt = am[0];
                                max = am[1];
                            }
                            else
                            {
                                attempt = seg;
                            }
                            reason = retryText.Substring(sep2 + 1);
                        }
                        // A69 视图层报错中文注释——重试原因摘要追加中文注释
                        reason = ErrorNote.Apply(reason);
                        // A86——原文快照：resolved 时保留报错信息（前端原文 + 追加「已恢复」，不覆盖）
                        _retryAttempt = attempt;
                        _retryMax = max;
                        _retryReason = reason;
                        PushRetryView("retrying");
                    }
                    else if (ev.Kind == LlmStreamKind.Usage)
                    {
                        // E3 Token 统计——解析 usage JSON 累计整轮（工具多轮累加）+ 单次前文覆盖 + 转发 SSE（前端覆盖式显示整轮累计值）；A115 载荷补 count（前文条数——前端「前文 N 条」与 tokens 同节奏刷新）
                        // 会话级累计——本次请求增量随轮同期堆入（跨轮保留；仅新会话复位）
                        long reqPrompt = _usagePrompt;
                        long reqCompletion = _usageCompletion;
                        long reqCacheHit = _usageCacheHit;
                        ParseUsage(ev.Text, ref _usagePrompt, ref _usageCompletion, ref _usageCacheHit, ref _contextTokens);
                        _sessionPrompt = _sessionPrompt + (_usagePrompt - reqPrompt);
                        _sessionCompletion = _sessionCompletion + (_usageCompletion - reqCompletion);
                        _sessionCacheHit = _sessionCacheHit + (_usageCacheHit - reqCacheHit);
                        // 🔴 A202 落盘点（唯一常规写点）——请求边界即物化点：全量落盘会话元数据（本轮六态 / 计数 / 耗时 / Note）
                        _lastRoundTokens = CaptureRoundTokens();
                        _persistedContextTokens = _contextTokens;
                        SaveMeta();
                        // F4 视图——状态段推送（前文长度与条数实时化；v2：状态段承载，无独立事件）
                        PushState();
                        // 请求 miss 超阈预警——长前文缓存断裂定位点：单次请求 miss（未命中缓存 prompt）超过阈值即投 error 气泡（同请求多 usage 帧只报一次）
                        long reqMiss = (_usagePrompt - reqPrompt) - (_usageCacheHit - reqCacheHit);
                        if (reqMiss > MissAlertThreshold && _lastMissAlertReq != _requestCount)
                        {
                            _lastMissAlertReq = _requestCount;
                            long oncePrompt = _usagePrompt - reqPrompt;
                            long onceCacheHit = _usageCacheHit - reqCacheHit;
                            long onceCompletion = _usageCompletion - reqCompletion;
                            string missAlert = "⚠ Token 预警：本次请求 miss " + reqMiss.ToString() + "（> " + MissAlertThreshold.ToString() + "）——前文 " + oncePrompt.ToString() + " / 命中 " + onceCacheHit.ToString() + " / 补全 " + onceCompletion.ToString() + "；本轮第 " + _requestCount.ToString() + " 次请求";
                            _viewStore.AppendError(missAlert, ViewTimestamp());
                        }
                    }
                    else if (ev.Kind == LlmStreamKind.Done)
                    {
                        // F4 视图——done 由整块 replace 表达（流式结束不单独推事件；无需推送）
                    }
                    else if (ev.Kind == LlmStreamKind.Error)
                    {
                        // P6 中止——暂停中忽略错误事件（取消引发的 TaskCanceled 转 Error 不推前端/不置错误）
                        if (_pauseRequested)
                        {
                            continue;
                        }
                        _llmError = true;
                        _llmErrorText = ev.Text;
                        // 空回复续传——STREAM_CLOSED（SSE 流未以 [DONE] 结束）：置位续传标志（PumpLlm 走续传），不推前端 error（无上限——LLM 兜底）
                        if (ev.Text.StartsWith("ERR|STREAM_CLOSED", StringComparison.Ordinal))
                        {
                            _streamClosedRetry = true;
                        }
                        // 非续传错误——不在此推视图/落盘：错误可见性统一出口 = AbortRoundError
                        // （主线程下一帧收尾落盘 + 推送——单块单事件；在此双推会造成重复错误气泡）
                    }
                }
                // [段2] 结果槽落位
                _llmResultText = text.ToString();
                // RoundSummary——本轮 text 入缓冲（全部 text 按序：工具轮间隙文本与最终回复同收）
                if (_llmResultText.Length > 0)
                {
                    _roundSummaryTextBuf.AppendLine(_llmResultText);
                }
                _llmReasoning = _reasonAccum.ToString();
                _llmToolCallsJson = toolCalls;
                // L1-META 结算行（D5 分级——观测全链：LLM 流完成一行为准，SSE log 事件实时可见）
                string llmSummary = "模型已回复（" + text.Length.ToString() + " 字符）";
                if (toolCalls.Length > 0)
                {
                    llmSummary = llmSummary + "，本轮调用了工具";
                }
                else
                {
                    llmSummary = llmSummary + "，未调用工具";
                }
                // E3 Token 统计——结算行带本轮 usage（轮级：本轮全部请求累加；SSE 推送仍走 usage 事件——前端覆盖式显示整轮累计值）
                llmSummary = llmSummary + "。本轮 Token：输入 " + _usagePrompt.ToString() + "（含缓存 " + _usageCacheHit.ToString() + "）· 输出 " + _usageCompletion.ToString();
                LogStore.Add("LLM", 0, llmSummary, "LLM");
            }
            catch (System.OperationCanceledException ex)
            {
                // P6 中止——用户暂停取消流：不置错误（PumpLlm 检查 _pauseRequested 走收尾）
                // 超时兜底——HttpClient.Timeout（60s）抛 TaskCanceledException（OCE 子类）也走此分支：非暂停请求的取消 = 超时/物理中断 → 置位续传（不静默吞并）
                if (!_pauseRequested)
                {
                    _llmError = true;
                    _streamClosedRetry = true;
                    _llmErrorText = "ERR|STREAM_CLOSED|SSE 流请求取消（超时/物理中断）: " + ex.GetType().Name;
                }
            }
            catch (Exception ex)
            {
                // P6 中止——暂停中异常忽略（取消竞态窗口——取消标志未及读取时异常已抛）
                if (!_pauseRequested)
                {
                    _llmError = true;
                    // 空回复续传——网络物理中断（连接重置/代理断连/IO 抖动）与优雅断流（EOF 无 [DONE]）同语义：置位续传（TranslateSse 迭代器内异常无法事件化——消费层兜底）
                    if (ex is System.Net.Http.HttpRequestException || ex is System.IO.IOException || ex is System.Net.Sockets.SocketException)
                    {
                        _streamClosedRetry = true;
                        _llmErrorText = "ERR|STREAM_CLOSED|SSE 流物理中断: " + ex.GetType().Name + "|" + ex.Message;
                    }
                    else
                    {
                        _llmErrorText = "ERR|" + ex.GetType().Name + "|" + ex.Message;
                    }
                }
            }
            finally
            {
                _llmBusy = false;
            }
        }
        /// <summary>思考段终结——离开 think 态的唯一收口（莎 2026-09-22 定）：临时区写空态（A196 状态投影——写 empty 即清面板）。
        /// 思考内容由持久区 `reason` 整块承载（同帧收口）。调用面：PhaseEnter 离开 think 态 + 中止 / 暂停收尾。</summary>
        private void SealReasonStream()
        {
            _viewBus.SetLive("empty", "");
        }

        /// <summary>运行态切换——结算旧态累计毫秒 + 进入新态（七态：idle/wait/link/think/tool/run/reply；锁内）；同态连续计时（重复事件不重置起表）；idle 不计时——只作态名。</summary>
        /// <param name="kind">目标态（PhaseIdle/PhaseWait/PhaseLink/PhaseThink/PhaseTool/PhaseRun/PhaseReply）</param>
        private void PhaseEnter(int kind)
        {
            bool leaveThink = false;
            long now = System.Diagnostics.Stopwatch.GetTimestamp();
            lock (_phaseLock)
            {
                // 同态——连续计时：不结算、不重置起表（重复事件不吞时长；2026-09-16 实测修正）
                if (_phaseKind == kind)
                {
                    return;
                }
                // 思考段终结判定——离开 think 态即收口（唯一出口：全部态转移都经本方法；莎 2026-09-22 定）
                if (_phaseKind == PhaseThink && kind != PhaseThink)
                {
                    leaveThink = true;
                }
                if (_phaseKind >= 0 && _phaseStartTick > 0)
                {
                    long ms = (now - _phaseStartTick) * 1000 / System.Diagnostics.Stopwatch.Frequency;
                    if (ms < 0) { ms = 0; }
                    _phaseAccumMs[_phaseKind] = _phaseAccumMs[_phaseKind] + ms;
                    // 思考用时——think 态内部实现：离开 think 即把本段净时长累进本块（不跨态；推入视图时取走并归零）
                    if (_phaseKind == PhaseThink)
                    {
                        _reasonDurMs = _reasonDurMs + ms;
                    }
                }
                _phaseKind = kind;
                // 空闲态不计时——idle 只作态名（轮未开始 / 收尾中）；计时效用为零，且实时增量会让快照 sessions 段每帧脏变化
                if (kind == PhaseIdle)
                {
                    _phaseStartTick = 0;
                }
                else
                {
                    _phaseStartTick = now;
                }
            }
            // 锁外推事件——思考段整块（幂等：无在途思考流式时静默返回）
            if (leaveThink)
            {
                SealReasonStream();
            }
        }

        /// <summary>
        /// 结算当前运行态——roundsum 生成前 / 终止收尾（错误中止 / 暂停 / 宿主重启中断 / Note 拉起）调用；累计落七态 + 态标记归零 + 快照留档。
        /// </summary>
        private void PhaseSettle()
        {
            long now = System.Diagnostics.Stopwatch.GetTimestamp();
            lock (_phaseLock)
            {
                if (_phaseKind >= 0 && _phaseStartTick > 0)
                {
                    long ms = (now - _phaseStartTick) * 1000 / System.Diagnostics.Stopwatch.Frequency;
                    if (ms < 0) { ms = 0; }
                    _phaseAccumMs[_phaseKind] = _phaseAccumMs[_phaseKind] + ms;
                    // 思考用时——收尾结算同口径（think 态内部实现：任何离开 think 的路径都入账，不跨态）
                    if (_phaseKind == PhaseThink)
                    {
                        _reasonDurMs = _reasonDurMs + ms;
                    }
                    _phaseKind = -1;
                    _phaseStartTick = 0;
                }
            }
        }
        /// <summary>
        /// 取走本块思考用时——推入视图层的时刻调用（取走即归零：计时生命周期止于落到视图层，不残留跨块）。
        /// </summary>
        /// <returns>本块思考用时毫秒（0 = 无思考段或已取走）</returns>
        private long TakeReasonDuration()
        {
            lock (_phaseLock)
            {
                long ms = _reasonDurMs;
                _reasonDurMs = 0;
                return ms;
            }
        }
        /// <summary>
        /// 七态累计快照——锁内复制（含当前活跃态实时增量）。约定：调用方必须已持有 _phaseLock。
        /// </summary>
        /// <returns>七态毫秒数组（索引同 PhaseNames）</returns>
        private long[] SnapshotPhaseAccumLocked()
        {
            long[] copy = new long[PhaseCount];
            long now = System.Diagnostics.Stopwatch.GetTimestamp();
            for (int i = 0; i < PhaseCount; i = i + 1)
            {
                copy[i] = _phaseAccumMs[i];
            }
            if (_phaseKind >= 0 && _phaseStartTick > 0)
            {
                long ms = (now - _phaseStartTick) * 1000 / System.Diagnostics.Stopwatch.Frequency;
                if (ms < 0) { ms = 0; }
                copy[_phaseKind] = copy[_phaseKind] + ms;
            }
            return copy;
        }
        /// <summary>运行态读取面——当前态名 + 七态累计毫秒表（idle 恒 0——空闲不计时）+ 本轮请求次数（锁内快照；观测面与前端拉取口子数据源）。</summary>
        /// <param name="name">输出：当前态名（idle/wait/link/think/tool/run/reply）</param>
        /// <param name="requests">输出：本轮 API 请求次数（= link 段数）</param>
        /// <returns>七态累计毫秒表（态名 → 毫秒）</returns>
        internal Dictionary<string, long> GetRunState(out string name, out int requests)
        {
            lock (_phaseLock)
            {
                long[] ms = SnapshotPhaseAccumLocked();
                Dictionary<string, long> table = new Dictionary<string, long>();
                for (int i = 0; i < PhaseCount; i = i + 1)
                {
                    table[PhaseNames[i]] = ms[i];
                }
                if (_phaseKind >= 0 && _phaseKind < PhaseCount)
                {
                    name = PhaseNames[_phaseKind];
                }
                else
                {
                    name = "idle";
                }
                requests = _requestCount;
                return table;
            }
        }
        /// <summary>
        /// 设置会话运行态盒——按会话键（chat_state:&lt;catKey&gt;）+ 旧全局键兼容（观测面与前端拉取口子）。
        /// </summary>
        /// <param name="state">粗粒度态（idle/working/tools——细粒度七态走 PhaseEnter）</param>
        private void SetChatState(string state)
        {
            DataBox.Set<string>("global", "chat_state", state);
            if (_catKey != null && _catKey.Length > 0)
            {
                DataBox.Set<string>("global", "chat_state:" + _catKey, state);
            }
        }
        /// <summary>运行态摘要行——当前态 + 六态毫秒（idle 不计时故不入行）+ 本轮请求次数（失败轮统计留档——日志观测面；正常轮由 roundsum 承载）。</summary>
        /// <returns>摘要文本（state=… link=…ms … requests=N）</returns>
        private string BuildRunStateSummary()
        {
            string name;
            int requests;
            Dictionary<string, long> ms = GetRunState(out name, out requests);
            return "state=" + name
                + " wait=" + ms["wait"].ToString() + "ms"
                + " link=" + ms["link"].ToString() + "ms"
                + " think=" + ms["think"].ToString() + "ms"
                + " tool=" + ms["tool"].ToString() + "ms"
                + " run=" + ms["run"].ToString() + "ms"
                + " reply=" + ms["reply"].ToString() + "ms"
                + " requests=" + requests.ToString();
        }
        /// <summary>
        /// 本猫运行态块 JSON——对话端口增量推送数据源（状态条；变化才推、不变不推——前端零轮询）。
        /// </summary>
        /// <returns>运行态 JSON（sessionId / runState / runMs 七键 / requests）</returns>
        public string BuildRunStateJson()
        {
            string name;
            int requests;
            Dictionary<string, long> ms = GetRunState(out name, out requests);
            return JsonUtil.Object(
                ("sessionId", Id),
                ("runState", name),
                ("runMs", JsonUtil.Raw(JsonUtil.Object(
                    ("idle", ms["idle"]),
                    ("wait", ms["wait"]),
                    ("link", ms["link"]),
                    ("think", ms["think"]),
                    ("tool", ms["tool"]),
                    ("run", ms["run"]),
                    ("reply", ms["reply"])))),
                ("requests", requests));
        }
        /// <summary>
        /// 会话态统一投影——控制台各通道（猫列表 / 快照会话段 / cat.info）单一数据源（A225）。
        /// 字段 = 运行态 + 计数 + 前文 + 会话级 token；猫元数据（id / name / 端口 / 类型）由调用方补齐。
        /// </summary>
        /// <returns>会话态字段字典</returns>
        internal Dictionary<string, object> BuildStateEntry()
        {
            Dictionary<string, object> entry = new Dictionary<string, object>();
            string runStateName;
            int requests;
            Dictionary<string, long> runMs = GetRunState(out runStateName, out requests);
            entry["phase"] = Phase.ToString();
            entry["runState"] = runStateName;
            entry["runMs"] = runMs;
            entry["requests"] = requests;
            entry["round"] = Round;
            entry["msgCount"] = MsgCount;
            entry["pending"] = PendingCount;
            entry["noteActive"] = NoteActive;
            entry["contextCount"] = ContextCount;
            entry["context"] = ContextTokensKnown;
            entry["contextChars"] = ComputeContextChars();
            entry["lastActiveAt"] = LastActiveAtKnown;
            entry["sessionPrompt"] = _sessionPrompt;
            entry["sessionCompletion"] = _sessionCompletion;
            entry["sessionCacheHit"] = _sessionCacheHit;
            SessionTokens st = new SessionTokens();
            st.Prompt = _sessionPrompt;
            st.CacheHit = _sessionCacheHit;
            st.Completion = _sessionCompletion;
            entry["sessionMiss"] = st.Miss;
            entry["sessionRate"] = st.Rate;
            return entry;
        }
        /// <summary>快照全量帧——v2 契约（连接建立首帧取一次；宿主 HTTP 侧注入）</summary>
        /// <returns>全量帧 JSON（state + persist:full + live）</returns>
        public string BuildViewFullJson()
        {
            _viewBus.SetState(BuildStateJson());
            return _viewBus.BuildFull();
        }
        /// <summary>取快照帧——v2 契约（帧轮取；无变化返回 null——零字节）</summary>
        /// <returns>帧 JSON（追加 / 状态 / 临时区可同帧承载）或 null</returns>
        public string TakeViewFrameJson()
        {
            _viewBus.SetState(BuildStateJson());
            string json;
            if (_viewBus.TryTakeFrame(out json))
            {
                return json;
            }
            return null;
        }
        /// <summary>
        /// 视图推送面是否仍有待推内容——重启闸门第二判据（A206：业务 Idle ≠ 推送面清空）。
        /// 只探测内容面（持久块 / 临时区），不含状态段——状态段每帧变化，纳入即永久置位。
        /// </summary>
        public bool ViewHasPendingContent
        {
            get
            {
                return _viewBus.HasPendingContent;
            }
        }
        /// <summary>
        /// 状态段整段 JSON——后端权威业务态（v2 契约：轮阶段 / 六态用时 / Note / 延迟队列 / Token 三级 / 前文长度与条数）。
        /// 前端零推断：状态位与数字就位即渲染；整段比对去重由视图出口承担（无变化零字节）。
        /// </summary>
        /// <returns>状态段 JSON</returns>
        public string BuildStateJson()
        {
            string name;
            int requests;
            Dictionary<string, long> ms = GetRunState(out name, out requests);
            int clients = _clientCountProvider != null ? _clientCountProvider() : 0;
            string runMsJson = JsonUtil.Object(
                ("idle", ms["idle"]),
                ("wait", ms["wait"]),
                ("link", ms["link"]),
                ("think", ms["think"]),
                ("tool", ms["tool"]),
                ("run", ms["run"]),
                ("reply", ms["reply"]));
            // A201 token 双类计量——轮级（空闲回落最近落盘快照，与 ContextTokensKnown 同模式）+ 会话级 + 派生值与命中率
            bool roundIdle = _phase == ChatPhase.Idle;
            SessionTokens roundTokens = new SessionTokens();
            roundTokens.Prompt = roundIdle ? _lastRoundTokens.Prompt : _usagePrompt;
            roundTokens.CacheHit = roundIdle ? _lastRoundTokens.CacheHit : _usageCacheHit;
            roundTokens.Completion = roundIdle ? _lastRoundTokens.Completion : _usageCompletion;
            SessionTokens sessionTokens = new SessionTokens();
            sessionTokens.Prompt = _sessionPrompt;
            sessionTokens.CacheHit = _sessionCacheHit;
            sessionTokens.Completion = _sessionCompletion;
            string tokensJson = JsonUtil.Object(
                ("prompt", roundTokens.Prompt),
                ("completion", roundTokens.Completion),
                ("cacheHit", roundTokens.CacheHit),
                ("miss", roundTokens.Miss),
                ("rate", roundTokens.Rate),
                // 前文长度（请求级）——轮首清零后回落落盘快照（契约 §12.10 轮间读面口径；原直取 _contextTokens 致轮内闪 0）
                ("context", ContextTokensKnown),
                ("count", _context.GetMessageCount()),
                ("sessionPrompt", sessionTokens.Prompt),
                ("sessionCompletion", sessionTokens.Completion),
                ("sessionCacheHit", sessionTokens.CacheHit),
                ("sessionMiss", sessionTokens.Miss),
                ("sessionRate", sessionTokens.Rate));
            string delayJson = JsonUtil.Object(("entries", DelayQueue.BuildEntriesFragment(_catKey)));
            // A233 pending 段——人工队列待处理消息投影（含非前端来源：QQ / 指令 / 定时转投）
            string pendingJson = BuildPendingFragment();
            // A201 会话元数据面——displayName / 会话实例 ID / 时间戳 / 前文条数与字符数（持久化面见 design-ch4-protocol §十三）
            // 易用性修复（A201 前端轮）：① lastActiveAt 在恢复导入后为 0（ReplaceMessages 不刷新）→ 回落创建时刻（与落盘面 SaveMeta 同口径）
            //                       ② contextChars 原缓存只在轮末刷新（轮内滞后）→ 推送时实时刷新
            long lastActive = LastActiveAtKnown;
            _contextChars = ComputeContextChars();
            string metaJson = JsonUtil.Object(
                ("catId", _id),
                ("displayName", _displayName),
                ("sessionId", _sessionInstanceId),
                ("createdAt", _sessionCreatedAt),
                ("lastActiveAt", lastActive),
                ("contextCount", _context.GetMessageCount()),
                ("contextChars", _contextChars));
            return JsonUtil.Object(
                ("sessionId", Id),
                ("runState", name),
                ("runMs", JsonUtil.Raw(runMsJson)),
                ("requests", requests),
                ("note", JsonUtil.Raw(BuildNoteJson())),
                ("delay", JsonUtil.Raw(delayJson)),
                ("pending", JsonUtil.Raw(pendingJson)),
                ("conn", JsonUtil.Raw(JsonUtil.Object(("server", IsHostRestarting() ? "stopping" : "ok"), ("clients", clients)))),
                ("tokens", JsonUtil.Raw(tokensJson)),
                ("meta", JsonUtil.Raw(metaJson)),
                ("pet", JsonUtil.Raw(JsonUtil.Object(("kind", _petKind)))));
        }
        /// <summary>状态段推送——状态变化即推（视图出口整段比对去重；未 Attach 时静默）</summary>
        public void PushState()
        {
            _viewBus.SetState(BuildStateJson());
        }
        /// <summary>
        /// pending 段 JSON——人工队列（_pending）待处理消息投影（A233；按入队序升序）。
        /// 含全部来源：前端输入（user）· QQ 注入（user + origin）· 指令 / 定时 / --run 注入。
        /// 条目字段：content / source / origin（附件面 ImagesJson 不投影——展示面用不到）。
        /// </summary>
        /// <returns>{"entries":[{"content","source","origin"}, …]}</returns>
        public string BuildPendingFragment()
        {
            List<PendingMessage> list = new List<PendingMessage>(_pending);
            list.Sort(ComparePendingBySeq);
            List<string> items = new List<string>();
            for (int i = 0; i < list.Count; i = i + 1)
            {
                items.Add(JsonUtil.Object(
                    ("content", list[i].Content),
                    ("source", list[i].Source),
                    ("origin", list[i].Origin)));
            }
            return JsonUtil.Object(("entries", JsonUtil.RawArray(items.ToArray())));
        }

        /// <summary>构建 roundsum 载荷——本轮 Token 消耗 + 工具次数 + 请求次数 + 总耗时 + 六态用时（idle 不计时故不入载荷；CloseRound 推送/落盘数据源）+ done（本轮结束语义：stream=流式自然收尾 / tool=工具主动 done）。</summary>
        /// <returns>roundsum 视图载荷 JSON（{"type":"roundsum","data":{...}}）</returns>
        private string BuildRoundSumJson()
        {
            long miss = _usagePrompt - _usageCacheHit;
            if (miss < 0) { miss = 0; }
            long elapsedMs = 0;
            if (_roundStartTick > 0)
            {
                elapsedMs = (System.Diagnostics.Stopwatch.GetTimestamp() - _roundStartTick) * 1000 / System.Diagnostics.Stopwatch.Frequency;
                if (elapsedMs < 0) { elapsedMs = 0; }
            }
            string stateName;
            int requests;
            Dictionary<string, long> ms = GetRunState(out stateName, out requests);
            // roundsum 只承载时长与计数——当前态名由此处不留档（观测面走 GetRunState）
            _ = stateName;
            // done——工具主动 done（sleep 等待 / restart 收尾登记）与流式自然 done 的分野（QQ 转发面据此决定来源是否续约）
            string doneKind = "stream";
            if (_toolDone)
            {
                doneKind = "tool";
            }
            string dataJson = JsonUtil.Object(
                ("prompt", _usagePrompt),
                ("completion", _usageCompletion),
                ("cacheHit", _usageCacheHit),
                ("miss", miss),
                ("done", doneKind),
                ("toolCount", _toolCallCount),
                ("requests", requests),
                ("elapsedMs", elapsedMs),
                ("phases", JsonUtil.Raw(JsonUtil.Object(
                    ("wait", ms["wait"]),
                    ("link", ms["link"]),
                    ("think", ms["think"]),
                    ("tool", ms["tool"]),
                    ("run", ms["run"]),
                    ("reply", ms["reply"])))));
            return JsonUtil.Object(("type", "roundsum"), ("data", JsonUtil.Raw(dataJson)));
        }

        /// <summary>
        /// 解析 usage JSON——prompt/completion/cacheHit 累加到整轮计数（CH2 语义：多工具轮累加）；singlePrompt 覆盖式为单次请求 prompt（前文长度）。
        /// </summary>
        /// <param name="usageJson">usage JSON 字符串（{"prompt":N,"completion":N,"cacheHit":N}）</param>
        /// <param name="prompt">prompt 累计引用</param>
        /// <param name="completion">completion 累计引用</param>
        /// <param name="cacheHit">cacheHit 累计引用</param>
        /// <param name="singlePrompt">单次 prompt 覆盖引用——最近一次请求的前文长度（非累计）</param>
        private static void ParseUsage(string usageJson, ref long prompt, ref long completion, ref long cacheHit, ref long singlePrompt)
        {
            if (usageJson == null || usageJson.Length == 0)
            {
                return;
            }
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(usageJson))
                {
                    JsonElement root = doc.RootElement;
                    if (root.TryGetProperty("prompt", out JsonElement p) && p.ValueKind == JsonValueKind.Number)
                    {
                        prompt = prompt + p.GetInt64();
                        singlePrompt = p.GetInt64();
                    }
                    if (root.TryGetProperty("completion", out JsonElement c) && c.ValueKind == JsonValueKind.Number)
                    {
                        completion = completion + c.GetInt64();
                    }
                    if (root.TryGetProperty("cacheHit", out JsonElement h) && h.ValueKind == JsonValueKind.Number)
                    {
                        cacheHit = cacheHit + h.GetInt64();
                    }
                }
            }
            catch (Exception ex)
            {
                // 解析失败静默——观测面不受单帧畸形影响
                LogStore.Add("ChatSession", 2, "usage 帧解析失败，累计跳过: " + ex.Message, "SYS");
            }
        }

        /// <summary>
        /// LlmRunning 相位推进——轮询完成槽；完成 → LlmDone 动作段三分支（错误/纯文本/工具）；帧超限 → 超时错误路径（同 WaitForLlm 语义）。
        /// </summary>
        private void PumpLlm()
        {
            _phaseFrames = _phaseFrames + 1;
            if (_llmBusy)
            {
                if (_phaseFrames < LlmTimeoutFrames)
                {
                    return;
                }
                // 超时兜底——同 WaitForLlm 超时语义（后台 Task 完成后槽再写为等价现状竞态——终态已出）
                _llmBusy = false;
                _llmError = true;
                _llmErrorText = "ERR|LLM_TIMEOUT|LLM 调用超时（帧上限 " + LlmTimeoutFrames.ToString() + "）";
            }
            // LlmDone 动作段——S2 修复：错误文本不入上下文（就地修复——断点干净，不污染 ChatContext）
            // 错误可见性：前端 error 控制事件（ConsumeLlmStream 已推）+ LogStore L3；上下文保持断点（用户消息 + 已完成 turn 保留）
            if (_llmError)
            {
                // 空回复续传——STREAM_CLOSED → 同上下文重发（无上限——LLM 兜底；StreamClosedRetry 标志由 ConsumeLlmStream 置位）
                if (_streamClosedRetry)
                {
                    RetryEmptyReply("SSE 流中断（未以 [DONE] 结束）");
                    return;
                }
                // API 错误中止——重试策略归 Runtime（429/5xx/传输类有限重试；4xx 参数/额度类单次即返），此处统一收尾：落盘断点 + 错误气泡（不走 CloseRound——不 roundsum/Note 拉起）
                AbortRoundError();
                return;
            }
            // timeback 事件计数——think（本轮产出思考内容）计 1；assistant 产出在下文两分支各计 1
            if (_llmReasoning.Length > 0)
            {
                NoteTimebackEvent();
            }
            if (_llmToolCallsJson.Length == 0)
            {
                // 空回复续传——流正常结束但无回复文本（有思考无回复或完全空）→ 同上下文重发（CH2 [段2.3] 移植；无上限——LLM 兜底；Trim 判空）
                if (string.IsNullOrWhiteSpace(_llmResultText))
                {
                    RetryEmptyReply("模型未生成回复文本");
                    return;
                }
                // 纯文本回复——本轮完成
                long textTicks = Stopwatch.GetTimestamp();
                AppendMessage(_context.AddAssistantMessage(_llmResultText));
                LoopTiming.Accumulate("cs.done.append", ElapsedMsSince(textTicks));
                NoteTimebackEvent();
                textTicks = Stopwatch.GetTimestamp();
                _viewStore.OnAssistantText(LastMessage(), _context.GetMessageCount() - 1, TakeReasonDuration());
                LoopTiming.Accumulate("cs.done.view", ElapsedMsSince(textTicks));
                // A196 临时区——本轮收尾写空态（内容已由持久区承载）
                _viewBus.SetLive("empty", "");
                // 单向数据流改造——忙时插话：本轮结束有排队消息 → 插入 Ctx + 直接开新轮（跳过 Done/CloseRound）
                if (FlushPendingIntoRound())
                {
                    _round = 0;
                    LaunchLlm();
                    return;
                }
                // 运行态——进入收尾（idle 只作态名不计时；轮末落盘与统计已由 PhaseSettle 结算——design-ch4-llm §2.1）
                PhaseEnter(PhaseIdle);
                _phase = ChatPhase.Done;
                return;
            }
            // StartToolBatch 动作段——assistant tool_calls 入上下文 + chat_state=tools + 发单
            long doneTicks = Stopwatch.GetTimestamp();
            AppendMessage(_context.AddAssistantToolCalls(_llmToolCallsJson, _llmReasoning));
            LoopTiming.Accumulate("cs.done.append", ElapsedMsSince(doneTicks));
            NoteTimebackEvent();
            doneTicks = Stopwatch.GetTimestamp();
            _viewStore.OnAssistantToolCalls(LastMessage(), _context.GetMessageCount() - 1, TakeReasonDuration());
            LoopTiming.Accumulate("cs.done.view", ElapsedMsSince(doneTicks));
            // 思考段整块——由 SealReasonStream 在离开 think 态时统一推送（工具决策流首帧即收口；唯一出口，莎 2026-09-22 定）
            // 工具轮 seal——视图层补 gap text 块（全量外观真源：前端历史/QQBot 转发消费）+ SSE 推送（实时）；空文本不推
            if (_llmResultText.Length > 0)
            {
                long gapTs = ViewTimestamp();
                _viewStore.AppendGapText(_llmResultText, gapTs);
            }
            // 思考段整块——先于工具先行卡推送（实时序对齐视图块生成序：assistant 思考块在工具卡之前）；
            // 判例 2026-09-29：此前直接清序（不推整块），实时面仅剩前端 live 块，F5 重建后 think 块与工具卡换位
            SealReasonStream();
            _viewBus.SetLive("empty", "");
            // 运行态——发单即执行态（本地·程序过程：工具批到下一请求发出；长度由本地决定）
            PhaseEnter(PhaseRun);
            SetChatState("tools");
            long toolsTicks = Stopwatch.GetTimestamp();
            EnterToolBatch(_llmToolCallsJson);
            // A196 临时区——工具运行态：未完成工具卡数组入 context（派发即写；单个完成即减；全清后由收尾点写 empty）
            RefreshToolLive();
            LoopTiming.Accumulate("cs.done.tools", ElapsedMsSince(toolsTicks));
        }
        /// <summary>
        /// 工具调用条目——tool_calls JSON 解析产物（先行推卡消费；字段与 OpenAI wire 对齐）。
        /// </summary>
        private sealed class ToolCallInfo
        {
            /// <summary>tool_call_id——结果配对键</summary>
            public string Id;

            /// <summary>工具名</summary>
            public string Name;

            /// <summary>参数 JSON（未注入 catId——发单前注入）</summary>
            public string Arguments;

            /// <summary>并发序号（1-based——tool_calls 数组顺序）</summary>
            public int Index;

            /// <summary>并发总数（同批 tool_calls 数组长度）</summary>
            public int Total;
        }

        /// <summary>
        /// 解析 tool_calls JSON（OpenAI wire：{id, function:{name, arguments}}）——逐条条目（携带 Index/Total）。
        /// 解析失败 / 非数组 → 空列表（容错——与发单侧同语义：空批立即收敛）。
        /// </summary>
        /// <param name="toolCallsJson">tool_calls JSON 数组</param>
        /// <returns>条目列表</returns>
        private List<ToolCallInfo> ParseToolCalls(string toolCallsJson)
        {
            List<ToolCallInfo> list = new List<ToolCallInfo>();
            if (toolCallsJson == null || toolCallsJson.Length == 0)
            {
                return list;
            }
            JsonDocument doc = null;
            try
            {
                doc = JsonUtil.ParseStrict(toolCallsJson);
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 3, "tool_calls 解析失败: " + ex.Message, "TOOL");
                return list;
            }
            using (doc)
            {
                JsonElement root = doc.RootElement;
                if (root.ValueKind != JsonValueKind.Array)
                {
                    return list;
                }
                int total = root.GetArrayLength();
                for (int i = 0; i < total; i = i + 1)
                {
                    JsonElement call = root[i];
                    ToolCallInfo info = new ToolCallInfo();
                    info.Id = GetStringProp(call, "id");
                    // OpenAI wire：name/arguments 在 function 嵌套对象内
                    JsonElement funcEl;
                    if (call.TryGetProperty("function", out funcEl))
                    {
                        info.Name = GetStringProp(funcEl, "name");
                        info.Arguments = TextUtil.DecodeArgEntities(info.Name, GetStringProp(funcEl, "arguments"));
                    }
                    else
                    {
                        info.Name = "";
                        info.Arguments = "";
                    }
                    info.Index = i + 1;
                    info.Total = total;
                    list.Add(info);
                }
            }
            return list;
        }

        /// <summary>工具运行态重写——未完成工具卡数组 JSON 入 live 段（A196：主干按「还没完成的 tool」决定 context）。
        /// 完成即从 context 移除（该卡由持久区承载，整批收口时落位）；全部收口后由收尾点写 empty。声明面外工具不入列。</summary>
        private void RefreshToolLive()
        {
            _viewBus.SetLive("toolrun", BuildPendingToolsJson());
        }

        /// <summary>未完成工具卡数组 JSON——live 段 toolrun 的 context（元素形态与持久工具卡载荷同构，前端复用同一渲染件）。</summary>
        /// <returns>JSON 数组字符串（无未完成工具 = "[]"）</returns>
        private string BuildPendingToolsJson()
        {
            List<string> cards = new List<string>();
            for (int i = 0; i < _dogs.Count; i = i + 1)
            {
                ToolOrderDog dog = _dogs[i];
                if (dog.IsClosed)
                {
                    continue;
                }
                Dictionary<string, object> payload = ViewCardPayload.BuildToolCard(dog.Name, InjectCatId(dog.ArgsJson), null, i + 1, _dogs.Count);
                cards.Add(JsonUtil.Serialize(payload));
            }
            if (cards.Count == 0)
            {
                return "[]";
            }
            return JsonUtil.RawArray(cards.ToArray()).Json;
        }

        /// <summary>工具卡逐条回填（A128）——单工具完成即定稿结果（超时 / 空结果兜底）并重写 live 段未完成清单（完成即从面板移除）。
        /// A196：临时区为状态投影——不发终态卡、不做两区配对；该工具的卡由持久区承载（段3 按声明序统一落位）。</summary>
        /// <param name="dog">工具单</param>
        private void FlushToolCard(ToolOrderDog dog)
        {
            if (dog.Result == null || dog.Result.Length == 0)
            {
                if (dog.IsTimedOut)
                {
                    LogStore.Add("CatHome4", 2, "工具 " + dog.Name + " 工单 #" + dog.OfficeId + " 超时（时限内无回执）——诚实 ERR；超时 ≠ 终止：底层执行可能仍在跑", "TOOL");
                    dog.Result = "ERR|OA_TIMEOUT|工单超时（时限内无回执）: " + dog.Name + "——超时 ≠ 终止：只失去回执，宿主不中断已认领的执行，底层可能仍在跑；勿用同参数重试（可能重复执行），长任务请分片（拆成多次小批）或走断点续传";
                }
                else
                {
                    dog.Result = "ERR|EMPTY_RESULT|工具执行无结果";
                }
            }
            RefreshToolLive();
        }

        /// <summary>
        /// StartToolBatch 动作段——解析 tool_calls → OA 发单（host-* 延迟直执登记 / 普通工单 Post / Post 失败诚实 ERR）→ ToolBatchRunning。解析失败 = 空批（allDone 立即成立——等价原 try-catch 跳过语义：续轮保持）。
        /// </summary>
        /// <param name="toolCallsJson">tool_calls JSON 数组</param>
        private void EnterToolBatch(string toolCallsJson)
        {
            _toolBatchActive = true;
            _dogs.Clear();
            _hostDogs.Clear();
            _batches.Clear();
            _batchIndex = -1;
            List<ToolCallInfo> calls = ParseToolCalls(toolCallsJson);
            // [P0] 剥离——批内 timeback-start / timeback-back 各至多 1 条（design-ch4-timeback §2.4）：
            // 各取首条剥离，重复调用（同类第 2 条起）判参数面拒绝（不静默丢弃）
            int startIndex = -1;
            int backIndex = -1;
            bool[] duplicated = new bool[calls.Count];
            for (int i = 0; i < calls.Count; i = i + 1)
            {
                if (calls[i].Name == "timeback-start")
                {
                    if (startIndex < 0)
                    {
                        startIndex = i;
                    }
                    else
                    {
                        duplicated[i] = true;
                    }
                    continue;
                }
                if (calls[i].Name == "timeback-back")
                {
                    if (backIndex < 0)
                    {
                        backIndex = i;
                    }
                    else
                    {
                        duplicated[i] = true;
                    }
                }
            }
            // [P1] 前置——start 同步直执（不并入批次）：其余工具的授权 / 门禁判定读的是 start 之后的
            // 作用域终局态，认定时点必须晚于 start 执行（会话结构约束，非 order 可表达）；失败不阻断
            ToolOrderDog startDog = null;
            if (startIndex >= 0)
            {
                ToolCallInfo startCall = calls[startIndex];
                ToolOrderDog preDog = new ToolOrderDog(startCall.Id, startCall.Name, InjectCatId(startCall.Arguments));
                if (!IsToolAllowed(startCall.Name))
                {
                    preDog.Result = "ERR|TOOL_FORBIDDEN|工具不在当前授权面: " + startCall.Name;
                    LogStore.Add("CatHome4", 2, "工具 " + startCall.Name + " 被拒绝：不在本会话声明面", "TOOL");
                }
                else
                {
                    preDog.Result = ExecuteBuiltin(startCall.Name, preDog.ArgsJson);
                    _toolCallCount = _toolCallCount + 1;
                }
                preDog.IsClosed = true;
                startDog = preDog;
            }
            // [P2] 建单——按原数组序构建全部工具单（_dogs 入列序 = 声明序：结果回填序与视图编号基准）；
            // 判定读 P1 之后的作用域终局态（本批含 start 则整批按作用域活跃判定——防数组前位工具逃逸）；
            // A127：本段只建单与判定，实际派发（host-* 登记 / 内置直执 / OA Post）推迟到批启动
            for (int i = 0; i < calls.Count; i = i + 1)
            {
                ToolCallInfo call = calls[i];
                // 批内重复 timeback-start / timeback-back——参数面拒绝（各至多 × 1）
                if (duplicated[i])
                {
                    ToolOrderDog dupDog = new ToolOrderDog(call.Id, call.Name, call.Arguments);
                    dupDog.Result = "ERR|TIMEBACK_ARGS|批内不允许多条 timeback-start / timeback-back 调用（各至多 × 1）——第 " + (i + 1).ToString() + " 条被拒";
                    dupDog.IsClosed = true;
                    _dogs.Add(dupDog);
                    LogStore.Add("CatHome4", 2, "timeback 批内重复调用被拒（第 " + (i + 1).ToString() + " 条）", "TIMEBACK");
                    continue;
                }
                // 剥离条目——start 已在前置段执行（授权面判定已在 P1 完成）
                if (i == startIndex)
                {
                    _dogs.Add(startDog);
                    continue;
                }
                // M2c 拦截——声明面外工具直接拒绝（ERR 回执不进 OA；host-* 同拦；拦截即时生效）
                if (!IsToolAllowed(call.Name))
                {
                    ToolOrderDog forbiddenDog = new ToolOrderDog(call.Id, call.Name, call.Arguments);
                    forbiddenDog.Result = "ERR|TOOL_FORBIDDEN|工具不在当前授权面: " + call.Name;
                    forbiddenDog.IsClosed = true;
                    _dogs.Add(forbiddenDog);
                    LogStore.Add("CatHome4", 2, "工具 " + call.Name + " 被拒绝：不在本会话声明面", "TOOL");
                    continue;
                }
                // timeback 暴毙风险黑名单——作用域存活期只拦「会让进程 / 作用域当场失效」的操作
                // （莎 2026-09-28 定 · 2026-10-01 判据收窄：从「改动本体」改为「暴毙风险」；统一在此点拦 host-* / 内置 / OA 三分支）
                if (_timebackScope != null && IsTimebackBodyLocked(call.Name))
                {
                    ToolOrderDog lockedDog = new ToolOrderDog(call.Id, call.Name, call.Arguments);
                    lockedDog.Result = "ERR|TIMEBACK_LOCKED|timeback 作用域内禁止会让进程或作用域当场失效的操作（重启 / 热重载 / 部署）: " + call.Name + "——先 back 回收";
                    lockedDog.IsClosed = true;
                    _dogs.Add(lockedDog);
                    LogStore.Add("CatHome4", 2, "工具 " + call.Name + " 被拒绝：timeback 作用域内暴毙风险操作", "TIMEBACK");
                    continue;
                }
                // 域限定工具门禁——仅 timeback 活跃时可用（image-inject / browser-* 四件：域外拒绝，不静默降级；
                // 方向与上一条黑名单相反——这里拦的是「域外调用」；名单判定见 ChatSession.Timeback.IsTimebackScopedTool）
                if (IsTimebackScopedTool(call.Name) && _timebackScope == null)
                {
                    ToolOrderDog scopeDog = new ToolOrderDog(call.Id, call.Name, call.Arguments);
                    scopeDog.Result = "ERR|TIMEBACK_REQUIRED|" + call.Name + " 仅在 timeback 作用域内可用（非主干信息——用完回收）——先 start 开锚";
                    scopeDog.IsClosed = true;
                    _dogs.Add(scopeDog);
                    LogStore.Add("CatHome4", 2, "工具 " + call.Name + " 被拒绝：timeback 作用域外不可用", "TIMEBACK");
                    continue;
                }
                // 域类型白名单（design-ch4-timeback-type §三 第 2 道）——域内只给本 type 的工具面（fail-closed）；
                // 由 start 登记的类型决定，与「域外专属集」正交（前者判"域内给不给"，后者判"域外给不给"）。
                // 三类不放本表管辖、交各自机制报错：timeback 两件（域机制自用）· Note/sleep/timer（C3 锁定——
                // 报 TIMEBACK_LOCKED 语义比 PROFILE 准）；C5 暴毙名单在上方已拒。
                // 会话授权面之外的工具亦不做先验——声明面在上方已拒，本表只判「是否属于本 type」
                if (_timebackScope != null && !IsTimebackProfileExempt(call.Name)
                    && !TimebackProfile.Allows(_timebackScope.Type, call.Name))
                {
                    ToolOrderDog profileDog = new ToolOrderDog(call.Id, call.Name, call.Arguments);
                    profileDog.Result = "ERR|TIMEBACK_PROFILE|" + call.Name + " 不在本域类型「" + _timebackScope.Type
                        + "」的白名单内——域类型决定域内可用工具面；需用则先 back 回收、回主干调用";
                    profileDog.IsClosed = true;
                    _dogs.Add(profileDog);
                    LogStore.Add("CatHome4", 2, "工具 " + call.Name + " 被拒绝：不在域类型 " + _timebackScope.Type + " 白名单内", "TIMEBACK");
                    continue;
                }
                // per-cat 路由——载荷注入猫 key（会话标识 ≡ 猫 key；积木按 catId 解析猫级文件系统与配置面）
                string arguments = InjectCatId(call.Arguments);
                // roundsum 工具计数——合法工具调用 +1（被拒工具不计）
                _toolCallCount = _toolCallCount + 1;
                ToolOrderDog dog = new ToolOrderDog(call.Id, call.Name, arguments);
                // A127——执行序裁决（静态表：timeback-start / timeback-back 各取一端钉死值）
                dog.Order = ToolOrderTable.Resolve(call.Name);
                _dogs.Add(dog);
            }
            // [P3] 分批——按 order 值升序分桶（同值一批 · 批内声明序；独占档每个调用各自成批）+ 启动首批
            // （A127：批间串行 / 批内并发；timeback start 已在 P1 前置，back 作为末批自然后置）
            BuildBatches();
            // A128——已闭合单（声明面拦截 / start 前置 / 批内重复调用）即刻出终态卡，不随批次推进延后
            for (int i = 0; i < _dogs.Count; i = i + 1)
            {
                if (_dogs[i].IsClosed)
                {
                    FlushToolCard(_dogs[i]);
                }
            }
            if (_batches.Count > 0)
            {
                StartBatch(0);
            }
            _phase = ChatPhase.ToolBatchRunning;
            _phaseFrames = 0;
        }
        /// <summary>分批计划构建——规则单点实装 `ToolOrderTable.PlanBatches`：按 order 值升序分桶（A127：同值一批 · 批内保持 LLM 声明序），**独占档工具每次调用各自成批**（A144：cs-* 预检写与 cs-build 不与任何工具同批）。
        /// 已闭合单（拦截 / start 前置执行）不入批——它们无待执行动作（由 _dogs 序承担回填与视图编号基准）。</summary>
        private void BuildBatches()
        {
            _batches.Clear();
            _batchIndex = -1;
            // 批次规则单点实装 → ToolOrderTable.PlanBatches（同值一批 · 值升序 · 批内保持声明序；独占档每次调用各自成批）
            List<ToolOrderDog> open = new List<ToolOrderDog>();
            List<string> names = new List<string>();
            List<int> orders = new List<int>();
            for (int i = 0; i < _dogs.Count; i = i + 1)
            {
                ToolOrderDog dog = _dogs[i];
                if (dog.IsClosed)
                {
                    continue;
                }
                open.Add(dog);
                names.Add(dog.Name);
                orders.Add(dog.Order);
            }
            List<List<int>> plan = ToolOrderTable.PlanBatches(names, orders);
            for (int p = 0; p < plan.Count; p = p + 1)
            {
                List<ToolOrderDog> batch = new List<ToolOrderDog>();
                List<int> indexes = plan[p];
                for (int k = 0; k < indexes.Count; k = k + 1)
                {
                    batch.Add(open[indexes[k]]);
                }
                _batches.Add(batch);
            }
        }
        /// <summary>
        /// 批启动——派发本批工具单（host-* 延迟直执登记 / 内置直执 / 其余 OA Post）+ 帧预算复位。
        /// 批间串行由此保证：上一批全部闭合后才启动下一批（见 PumpToolBatch 段2b-2）。
        /// </summary>
        /// <param name="index">批次序号（_batches 索引）</param>
        private void StartBatch(int index)
        {
            _batchIndex = index;
            _hostDogs.Clear();
            _phaseFrames = 0;
            List<ToolOrderDog> batch = _batches[index];
            for (int i = 0; i < batch.Count; i = i + 1)
            {
                ToolOrderDog dog = batch[i];
                if (dog.IsClosed)
                {
                    continue;
                }
                // A157——运行时长起表（工具卡时长基准：批派发时刻 → 完成时刻）
                dog.StartedAtMs = ViewTimestamp();
                if (dog.Name.StartsWith("host-", StringComparison.Ordinal))
                {
                    // host-* 延迟直执登记——本批末尾执行（顺序保证：同批构建类工具先完成产物落地）
                    _hostDogs.Add(dog);
                    dog.IsClosed = true;
                    continue;
                }
                if (IsBuiltinTool(dog.Name))
                {
                    // 内置工具会话内直执——不需 OA（R0.2 分层：Note 状态在会话实例；time/random/info 宿主直执）
                    dog.Result = ExecuteBuiltin(dog.Name, dog.ArgsJson);
                    dog.IsClosed = true;
                    dog.ClosedAtMs = ViewTimestamp();
                    continue;
                }
                dog.Post(_oa, ToolOwnerId);
                if (dog.OfficeId == 0)
                {
                    // Post 失败——诚实失败（直执面已移除 2026-09-04——OA 不可用即 ERR，不静默降级）
                    dog.Result = "ERR|OA_POST_FAIL|工单提交失败（OA 不可用）: " + dog.Name;
                    dog.IsClosed = true;
                    dog.ClosedAtMs = ViewTimestamp();
                    LogStore.Add("CatHome4", 2, "工具 " + dog.Name + " 工单提交失败（OA 不可用）——诚实 ERR", "TOOL");
                }
            }
        }

        /// <summary>
        /// 工具可用性判定——特权面 + 池校验 + 当前授权集实时查询（design-ch4-tools §三·十一：注入面 = 提示，授权面 = 真相）。
        /// 授权集由组合根注入的 AuthorizedToolNamesProvider 提供（Admin 域实时解析 cat.cfg 名单）；未接线 → 回落 _tools 快照（旧语义）+ 一次告警。
        /// 公开面——漂移观测等诊断消费（info tools_drift.added 只报三面全过者；莎 2026-09-28）。
        /// </summary>
        /// <param name="name">工具名</param>
        /// <returns>true=可调用</returns>
        public bool IsToolAllowed(string name)
        {
            // 特权面——仅默认会话可调（判据 = 注册面组级标记，单一真相源；design-ch4-host-restart §二）
            if (IsPrivilegedTool(name) && !IsDefaultSession)
            {
                return false;
            }
            // 池校验——工具须在运行时注册表内（未加载 / 已退役 = 拒）
            if (ToolRegistry.Find(name) == null)
            {
                return false;
            }
            // 当前授权集——实时查询（改配置 / 热重载即时生效，无需新会话）
            string[] authorized = ResolveAuthorizedToolNames();
            for (int i = 0; i < authorized.Length; i = i + 1)
            {
                if (string.Equals(authorized[i], name, StringComparison.Ordinal))
                {
                    return true;
                }
            }
            return false;
        }
        /// <summary>
        /// 解析当前授权工具名——委托链（Admin 域实时解析当前 cat.cfg 名单）；未接线回落会话注入面快照。
        /// </summary>
        /// <returns>授权工具名数组（非 null）</returns>
        private string[] ResolveAuthorizedToolNames()
        {
            if (AuthorizedToolNamesProvider == null)
            {
                if (!_authorizedProviderWarned)
                {
                    _authorizedProviderWarned = true;
                    LogStore.Add("CatHome4", 2, "授权面提供者未接线——工具判定回落会话声明面快照", "TOOL");
                }
                return DeclaredToolNames;
            }
            string[] names = AuthorizedToolNamesProvider(_catKey);
            if (names == null)
            {
                return new string[0];
            }
            return names;
        }

        /// <summary>
        /// 载荷注入猫 key——arguments JSON 合并 catId 字段（会话标识 ≡ 猫 key，唯一标识；非对象/解析失败原样透传）。
        /// </summary>
        /// <param name="arguments">LLM 原始参数 JSON</param>
        /// <returns>注入后 JSON</returns>
        private string InjectCatId(string arguments)
        {
            if (arguments == null || arguments.Length == 0)
            {
                return arguments;
            }
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(arguments))
                {
                    if (doc.RootElement.ValueKind != JsonValueKind.Object)
                    {
                        return arguments;
                    }
                    Dictionary<string, object> map = new Dictionary<string, object>();
                    foreach (JsonProperty p in doc.RootElement.EnumerateObject())
                    {
                        map[p.Name] = p.Value.Clone();
                    }
                    map["catId"] = _catKey;
                    return JsonUtil.Serialize(map);
                }
            }
            catch (Exception)
            {
                // 参数 JSON 损坏——原样透传（下游 BAD_ARGS 校验可见拒绝）
                return arguments;
            }
        }
        /// <summary>
        /// ToolBatchRunning 相位推进——逐 Dog 帧轮询；allDone 或帧超限 → ToolBatchDone 动作段（host-* 延迟直执 → 收集回执 → 续轮/收敛）。
        /// </summary>
        private void PumpToolBatch()
        {
            _phaseFrames = _phaseFrames + 1;
            bool allDone = true;
            // A127——只轮询当前批：未启动批次的工具单尚未 Post，Tick 会即时判超时（结构性错误）
            if (_batchIndex >= 0 && _batchIndex < _batches.Count)
            {
                List<ToolOrderDog> batch = _batches[_batchIndex];
                for (int i = 0; i < batch.Count; i = i + 1)
                {
                    batch[i].Tick(_oa);
                    if (!batch[i].IsClosed && !batch[i].IsTimedOut)
                    {
                        allDone = false;
                    }
                    else if (!_hostDogs.Contains(batch[i]))
                    {
                        // A128 逐条回填——单工具完成（回执 / 超时 / 内置直执）即出终态卡（幂等）；
                        // host-* 在 StartBatch 已置 IsClosed（认领态）而结果待段2b 直执——不在此推
                        FlushToolCard(batch[i]);
                    }
                }
            }
            if (!allDone && _phaseFrames < ToolBatchWaitFrames)
            {
                return;
            }
            // [段2b] host-* 延迟直执——本批其他工具完成后宿主直执（忙时豁免：主线程串行——宿主 ExecuteReload 事务三段式兜底）
            for (int h = 0; h < _hostDogs.Count; h = h + 1)
            {
                ToolOrderDog dog = _hostDogs[h];
                bool savedBusy = _toolBatchActive;
                _toolBatchActive = false;
                string hr = RunTool(dog.Name, dog.ArgsJson);
                _toolBatchActive = savedBusy;
                if (hr == null || hr.Length == 0)
                {
                    hr = "ERR|EMPTY_RESULT|工具执行无结果";
                }
                dog.Result = hr;
                dog.ClosedAtMs = ViewTimestamp();
                // A128 逐条回填——host-* 直执结果就位即出终态卡（不等段3 收口）
                FlushToolCard(dog);
            }
            // [段2b-2] 批间推进——本批收口即启动下一批（A127：批间串行 / 批内并发；批间失败不阻断——
            // 失败由工具卡红标可见，不构成链断）。timeback back 作为 order 100 末批在此自然后置：
            // 前文末条仍为本批 assistant 声明 → 回收区间上界与释放条数预算同源可对账
            if (_batchIndex + 1 < _batches.Count)
            {
                StartBatch(_batchIndex + 1);
                return;
            }
            // [段3] 收集——Closed 取回执；TimeOut/帧超限 → 诚实 ERR（OA 链路失败即报错，不直执）
            for (int i = 0; i < _dogs.Count; i = i + 1)
            {
                ToolOrderDog dog = _dogs[i];
                if (!dog.IsClosed && !dog.IsTimedOut)
                {
                    dog.IsTimedOut = true;
                }
                if (dog.IsTimedOut)
                {
                    dog.IsClosed = true;
                }
                // A128——终态卡已随各工具完成即时回填（FlushToolCard 幂等，此处兜底：结果定稿 + 未推过的补齐）
                FlushToolCard(dog);
                AppendMessage(_context.AddToolResult(dog.ToolCallId, dog.Name, dog.Result));
                NoteTimebackEvent();
                NoteTimebackWrite(dog.Name, dog.ArgsJson, dog.Result);
                _viewStore.OnToolResult(LastMessage(), _context.GetMessageCount() - 1, dog.ElapsedMs());
            }
            _toolBatchActive = false;
            // [段2d-0] 图片注入——本批 image-inject 登记合并为一条 user 注入消息（落在作用域区间内：back 回收时一并删除）
            FlushImageInjections();
            // [段2d] timeback 回卷——本批请求了 back 则在此执行（工具结果已全部回填：截断 + 结论注入 + 工具主动 done）
            ApplyTimebackBack();
            // [段2d-1] timeback 域规范卡注入（§十一）——作用域活跃且未注入 → 入队一条 systemauto user（落在区间内 · 闭合随删）
            // 位于回收之后：同批 start+back 时作用域已关 → 零动作（区间为空，注入会落在删除区间之外）
            PostTimebackCard();
            // [段2e] timeback 状态自述——回收后作用域已关（自然跳过）；未关且累计满 10 事件则追加一条 assistant 自述
            FlushTimebackNotice();
            // [段2c] 宿主重启检测——majordomo-restart 成功回执 → 登记重启请求 + 停机态（A72：本轮走常规结束流程，不强制中断）
            for (int r = 0; r < _dogs.Count; r = r + 1)
            {
                ToolOrderDog rd = _dogs[r];
                if (IsRestartTool(rd.Name) && rd.Result != null && !rd.Result.StartsWith("ERR|", StringComparison.Ordinal))
                {
                    string restartReq;
                    if (!DataBox.TryGet<string>("global", "host_restart_request", out restartReq) || restartReq == null || restartReq.Length == 0)
                    {
                        restartReq = "{}";
                    }
                    // 工具主动 done——本轮结束语义为「工具收尾」（QQ 转发面据此续约来源：回执轮才是回复轮）
                    _toolDone = true;
                    RestartRequest(restartReq);
                }
            }
            // A72——停机态丢弃在途排队消息（宿主即将重启，不再开新轮）
            DropPendingIfRestarting();
            // 单向数据流改造——忙时插话：工具批完成有排队消息 → 插入 Ctx + 直接续轮（工具结果 + 插话同轮可见）
            if (FlushPendingIntoRound())
            {
                _round = 0;
                LaunchLlm();
                return;
            }
            _round = _round + 1;
            // 续轮——StartLlm 动作段（无收敛上限——LLM 未给出最终回复前持续工具循环；终止条件 = 正常回复 / 空回复续传 / API 错误中止）
            LaunchLlm();
        }
        /// <summary>错误中止收尾——LLM API 错误后调用（重试型：Runtime 对 429/5xx/传输类有限重试耗尽；单次型：4xx 参数/额度类 Runtime 不重试——两者同路径，日志措辞按是否真重试分档）。保留断点上下文 + 错误气泡；不走 CloseRound——中止非正常完成语义。</summary>
        private void AbortRoundError()
        {
            SealReasonStream();
            // 运行态——中止前结算当前态（失败轮同出统计：L2 摘要留档；不推 roundsum 气泡——中止非正常完成语义）
            PhaseSettle();
            LogStore.Add("LLM", 2, "本轮运行态统计（中止）: " + BuildRunStateSummary(), "LLM");
            // 失败措辞如实分档（2026-09-30）——重试型（Runtime 对 429/5xx/传输类有限重试后）与本轮从未重试的单次失败
            // （4xx 参数/额度类——Runtime 不重试）不可混称「重试耗尽」；判据 = 本轮是否推过 retry 视图（视图出口在途标志）
            string abortKind;
            if (_retryPushed)
            {
                abortKind = "重试耗尽";
            }
            else
            {
                abortKind = "请求失败（未重试）";
            }
            LogStore.Add("LLM", 3, "LLM 错误（" + abortKind + "——本轮中止，上下文保持断点）: " + TrimDisplay(_llmErrorText, 300), "LLM");
            // [段1] 前文落盘——落盘保真（消息行随追加即落盘；A202 起元数据行不再承载统计）
            // [段2] 错误可见——视图块落盘（持久化）+ 前端 error 事件（文本取清空前原值）
            // A69 视图层报错中文注释——错误原文仍进日志与前文面，仅视图块追加中文注释
            string viewError = ErrorNote.Apply(_llmErrorText);
            long errTs = ViewTimestamp();
            _viewStore.AppendError(viewError, errTs);
            // [段3] 重试耗尽终态（A94——本轮推过 retry 气泡则补 failed 终态，保留报错原文；最终错误详情仍归 error 气泡）
            if (_retryPushed)
            {
                PushRetryView("failed");
            }
            // [段4] 状态复位——Idle（不推 roundsum/Note 拉起——错误中止非正常完成语义）
            _round = 0;
            _phase = ChatPhase.Idle;
            _phaseFrames = 0;
            ResetRetryView();
            _llmBusy = false;
            _llmError = false;
            _llmErrorText = "";
            _llmResultText = "";
            _llmReasoning = "";
            _llmToolCallsJson = "";
            _emptyReplyRetry = 0;
            _streamClosedRetry = false;
            SetChatState("idle");
        }

        /// <summary>
        /// Done 相位——前文落盘（落盘保真）+ chat_state=idle + 复位（原 HandleChat 段4）。
        /// </summary>
        private void CloseRound()
        {
            // A202 落盘时机——元数据面只在每次 API 请求结算后落盘（usage 处置点）；
            // 轮末与 Note 拉起轮均为纯内存行为：轮末只写视图层 roundsum 块（A248 起每块即时落盘——轮末无额外落盘动作），元数据面不写
            // A229 timeback 未闭合自动拉起——优先于 Note（莎 2026-10-12 定）：域未回收 = 本轮未结束，同样不 roundsum；
            // 上限 3 次防无限空转（模型不回收时出声后放行本轮结束）
            if (TimebackActive)
            {
                if (_timebackPullCount < 3)
                {
                    _timebackPullCount = _timebackPullCount + 1;
                    PhaseSettle();
                    PostSystemMessage(SysKindSystemAuto, "先回收timeback域再结束工作");
                    _round = 0;
                    _phase = ChatPhase.Idle;
                    return;
                }
                if (_timebackPullCount == 3)
                {
                    // 上限已出声一次——此后不再拉起（防每轮重复告警刷屏）；域回收后计数归零，下一个域重新计数
                    _timebackPullCount = 4;
                    LogStore.Add("CatHome4", 2, "timeback 未闭合拉起达上限（3 次）——不再拉起，放行本轮结束: cat=" + _catKey, "TIMEBACK");
                }
            }
            else
            {
                _timebackPullCount = 0;
            }
            // M4a Note 自动拉起提前——剩余≥2 条时以 user 名义推下一轮（最后 1 条不拉起——LLM 完成后自然结束；Q2 顺序：Note 未完成 = 本轮未结束——不 roundsum；全部完成天然跳过——防无限循环闸门）
            if (_noteTasks != null && _noteTasks.Length > 0 && _noteCurrent + 1 < _noteTasks.Length)
            {
                int remain = _noteTasks.Length - _noteCurrent;
                // 运行态——Note 拉起轮同样结算（本轮统计留档——design-ch4-llm §2.1 终止语义）
                PhaseSettle();
                PostSystemMessage(SysKindSystemAuto, "[Note 未完成] 剩余 " + remain + " 条\n当前任务：" + _noteTasks[_noteCurrent]);
                _round = 0;
                _phase = ChatPhase.Idle;
                return;
            }
            // roundsum 轮末统计——相位结算 + 载荷构建（Appender 内落盘）+ SSE 推送（本轮 Token 消耗 + 工具次数 + 总耗时 + 四态用时）
            PhaseSettle();
            string roundsumJson = BuildRoundSumJson();
            long sumTs = ViewTimestamp();
            _viewStore.AppendRoundSummary(roundsumJson, sumTs);
            // RoundSummary——轮末调度总结服务（开关开 + 配置可用；块固定插在 roundsum 之后 = ts + 1）
            PostRoundSummary(sumTs + 1);
            SetChatState("idle");
            // B4 对话区：轮末状态段推送——v2 契约下会话终态由 state 段承载（runState 回 idle + Token 三级就位；前端零推断）
            PushState();
            // Q 本轮结束系统通知——配置 app.round_notify 可开关（默认开启）；正文优先末轮回复前 40 字符，空则回退 Token 统计
            if (_roundNotify != null)
            {
                string notifyOn = "true";
                ConfigStore cfg = null;
                if (DataBox.TryResolve<ConfigStore>(out cfg) && cfg != null)
                {
                    notifyOn = cfg.Get("app.round_notify", "true");
                }
                if (notifyOn == "true" || notifyOn == "1")
                {
                    string body = TrimDisplay(_llmResultText, 40);
                    if (body.Length == 0)
                    {
                        body = "Token：输入 " + _usagePrompt.ToString() + " · 输出 " + _usageCompletion.ToString() + " · 工具 " + _toolCallCount.ToString() + " 次";
                    }
                    _roundNotify(_displayName, body);
                }
            }
            // M4c Note 清空——无剩余任务（正常结束路径；拉起已提前到 roundsum 之前——Q2 顺序调整）
            _noteTasks = null;
            _noteCurrent = 0;
            _noteDone = 0;
            PushNoteState();
            _round = 0;
            _phase = ChatPhase.Idle;
            ResetRetryView();
            // A201 轮级归零——本轮结算视图推入之后（design-ch4-protocol §十三：结算后清零；轮间读面回落落盘快照）
            _usagePrompt = 0;
            _usageCompletion = 0;
            _usageCacheHit = 0;
        }
        /// <summary>
        /// RoundSummary 前段提示词——按猫身份生成的第一人称视角（向用户交代，不做第三方总结）。
        /// </summary>
        /// <returns>system 提示词</returns>
        private string BuildRoundSummaryPromptHead()
        {
            string name = DisplayName;
            if (name == null || name.Length == 0)
            {
                name = _catKey;
            }
            return "你是「" + name + "」。这一轮你刚做完事，现在向用户亲口交代你做了什么、结果如何。"
                + "第一人称（我 / 我这边），口语，像当面说话；只说自己的动作与结果，不复述用户的要求，"
                + "不用「用户」「助手」「该轮」这类旁观措辞；一句话 20-50 字；不要客套、不要分点、不要引号。"
                + "这句话会被直接念出来，数字与缩写按读音写：版本号、编号、端口号逐位写成汉字（1.8.152 写成 一点八点一五二，8080 写成 八零八零），"
                + "只有表示数量时才用阿拉伯数字（3 个文件）；技术缩写拆成大写单字母加空格（CUDA 写成 C U D A）。";
        }
        /// <summary>
        /// RoundSummary——轮末调度：组 prompt 并投 LLMSimpleRequest 服务工单（开关开 + 配置可用 + 有内容时才发）。
        /// 谁发谁收——officeId 记入 _roundSummaryOfficeId，由 Pump 每帧轮询回收。
        /// </summary>
        /// <param name="insertTs">总结块时间戳（= roundsum ts + 1——固定插在轮末统计之后）</param>
        private void PostRoundSummary(long insertTs)
        {
            _roundSummaryOfficeId = 0;
            _roundSummaryPendingTs = insertTs;
            if (!_roundSummaryEnabled)
            {
                return;
            }
            string[] llm = RoundSummaryLlmProvider != null ? RoundSummaryLlmProvider(_catKey) : null;
            if (llm == null || llm.Length < 3 || llm[0].Length == 0 || llm[1].Length == 0 || llm[2].Length == 0)
            {
                return;
            }
            string content = BuildRoundSummaryContent();
            if (content.Length == 0)
            {
                return;
            }
            long officeId = _oa.Post(RoundSummaryOwnerId, "SVC", "LLMSimpleRequest", RoundSummaryTimeoutFrames);
            if (officeId <= 0)
            {
                LogStore.Add("CatHome4", 2, "轮末总结发单失败（OA.Post 未受理）: cat=" + _catKey, "CHAT");
                return;
            }
            _oa.SetStr(officeId, RoundSummaryOwnerId, "endpoint", llm[0]);
            _oa.SetStr(officeId, RoundSummaryOwnerId, "apikey", llm[1]);
            _oa.SetStr(officeId, RoundSummaryOwnerId, "model", llm[2]);
            _oa.SetStr(officeId, RoundSummaryOwnerId, "system", BuildRoundSummaryPromptHead());
            _oa.SetStr(officeId, RoundSummaryOwnerId, "content", content);
            _roundSummaryOfficeId = officeId;
        }
        /// <summary>
        /// RoundSummary——本轮待总结内容：全部 user + 全部 text 按序合并 + 后段提示词（固定分隔符）。
        /// </summary>
        /// <returns>普通段文本（轮内无内容返回空串）</returns>
        private string BuildRoundSummaryContent()
        {
            StringBuilder sb = new StringBuilder();
            sb.Append(_roundSummaryUserBuf.ToString());
            sb.Append(_roundSummaryTextBuf.ToString());
            if (sb.Length == 0)
            {
                return "";
            }
            sb.Append(RoundSummaryPromptTail);
            return sb.ToString();
        }
        /// <summary>
        /// RoundSummary——在途工单回收（每帧调用）：闭合即取回执注入总结块；超时亦出声（失败可见）。
        /// </summary>
        private void PumpRoundSummary()
        {
            if (_roundSummaryOfficeId <= 0)
            {
                return;
            }
            long officeId = _roundSummaryOfficeId;
            OfficeState st = _oa.GetStatus(officeId);
            if (st != OfficeState.Closed && st != OfficeState.TimeOut)
            {
                return;
            }
            _roundSummaryOfficeId = 0;
            string result = "";
            Office office = _oa.GetOffice(officeId);
            if (office.Result.Strs != null && office.Result.Strs.ContainsKey("result"))
            {
                result = office.Result.Strs["result"];
            }
            _oa.RemoveByOwner(RoundSummaryOwnerId);
            if (st == OfficeState.TimeOut)
            {
                result = "ERR|TIMEOUT|轮末总结超时";
            }
            if (result.Length == 0)
            {
                result = "ERR|EMPTY|轮末总结无可读回执";
            }
            long ts = _roundSummaryPendingTs > 0 ? _roundSummaryPendingTs : ViewTimestamp();
            _viewStore.AppendRoundSummaryNote(result, ts);
            // A247——总结自动出声（外观配置启用时；失败记 Log、不影响轮次）
            if (!result.StartsWith("ERR|", StringComparison.Ordinal))
            {
                SpeakRoundSummary(result);
            }
        }

        /// <summary>
        /// 前文截断到指定条数——截断唯一共用实现（人工回滚 / timeback 回卷同源；design-ch4-timeback §12.5）。
        /// 执行：截断上下文 → 前文落盘（截断重写：append-only 的合法例外）→ 轮级计数与统计条数复位。
        /// 不含：会话级统计归零（调用方决定）/ Note 处置 / 视图处置 / 前端通知——各路径语义不同，分头保留。
        /// </summary>
        /// <param name="keepCount">保留条数（保留 [0..keepCount-1]）</param>
        private void TruncateMessages(int keepCount)
        {
            // [段1] 截断上下文——保留前 keepCount 条（ReplaceMessages 安全面——格式修复幂等）
            LlmMessage[] all = _context.GetMessages();
            int count = keepCount;
            if (count < 0)
            {
                count = 0;
            }
            if (count > all.Length)
            {
                count = all.Length;
            }
            LlmMessage[] keep = new LlmMessage[count];
            for (int i = 0; i < count; i = i + 1)
            {
                keep[i] = all[i];
            }
            _context.ReplaceMessages(keep);
            // [段2] 前文落盘——落盘保真（截断重写：append-only 的合法例外；A202 起元数据行不再承载统计）
            LlmMessage[] toSave = _context.GetMessages();
            _store.Rewrite(toSave);
            // [段3] 轮级计数复位——新起点零统计起算（会话级累计不动——同会话延续）
            _usagePrompt = 0;
            _usageCompletion = 0;
            _usageCacheHit = 0;
            _contextTokens = 0;
            _persistedContextTokens = 0;
            _toolCallCount = 0;
        }

        /// <summary>
        /// 回滚——从指定正式回复节点重新开始（P6b：裁剪唯一通道；该节点后消息全部丢弃）。
        /// 校验：仅 Idle；msgIndex 指向 assistant 正式回复（Content>0——工具声明轮天然排除）。执行：截断上下文 → 落盘 → 统计/视图截断/Note 复位 → session_reset 推送。
        /// </summary>
        /// <param name="msgIndex">真实前文消息索引（指向 assistant 正式回复——前端 text 块 MsgIndex）</param>
        /// <returns>ERR| 前缀失败 / 成功摘要</returns>
        public string Rollback(int msgIndex)
        {
            if (_phase != ChatPhase.Idle)
            {
                return "ERR|ROLLBACK_BUSY|会话忙——回滚仅空闲时执行";
            }
            LlmMessage[] all = _context.GetMessages();
            if (msgIndex < 0 || msgIndex >= all.Length)
            {
                return "ERR|ROLLBACK_INDEX|节点索引越界: " + msgIndex.ToString();
            }
            LlmMessage target = all[msgIndex];
            if (target.Role != LlmRole.Assistant || target.Content == null || target.Content.Length == 0)
            {
                return "ERR|ROLLBACK_NODE|节点不是正式回复（仅 assistant 回复可作切点）";
            }
            // [段1] 截断前文到切点——共用实现（timeback 回卷同源；含落盘与轮级计数复位）
            TruncateMessages(msgIndex + 1);
            // [段2] 最近轮统计重置——新起点零统计起算（会话级累计不动：回滚属同会话延续；
            //        原实现调 ResetStats() 会清会话级 token 累计——与 glossary「回滚不归零」口径冲突，2026-09-28 修正；
            //        A202：轮级读面回落源与请求级前文长度一并归零）
            _lastRoundTokens = new SessionTokens();
            // 前文长度——请求级实时值 + 落盘回落源同归零（读面不显示回滚点之前的旧值）
            _contextTokens = 0;
            _persistedContextTokens = 0;
            // [段4] 视图层截断——与真实前文同批（视图层截断通道回归：此前「视图不动」使回滚在观感上无效——
            //        帧轮重置后的全量帧取数源正是视图层，未截断即把已回滚的消息原样重发）
            _viewStore.TruncateFrom(msgIndex);
            // [段5] Note 任务清空——防旧任务自动拉起新轮
            _noteTasks = null;
            _noteCurrent = 0;
            _noteDone = 0;
            _noteJustCompleted = false;
            PushNoteState();
            // [段6] 前端通知——session_reset（帧轮广播全量帧：前端持久区清空后按截断后的视图层整体重绘）
            PushSessionReset();
            SetChatState("idle");
            LogStore.Add("CatHome4", 1, "已回滚到节点 " + msgIndex.ToString() + "（保留 " + (msgIndex + 1).ToString() + " 条消息）", "CHAT");
            return "rollback | 已截断到节点 " + msgIndex.ToString() + "（保留 " + (msgIndex + 1).ToString() + " 条消息）";
        }

        /// <summary>
        /// 读取 JSON 对象字符串属性——防御式（缺字段返回空串）。
        /// </summary>
        /// <param name="obj">JSON 对象</param>
        /// <param name="prop">属性名</param>
        /// <returns>属性值</returns>
        private static string GetStringProp(JsonElement obj, string prop)
        {
            JsonElement value;
            if (obj.TryGetProperty(prop, out value) && value.ValueKind == JsonValueKind.String)
            {
                string got = value.GetString();
                if (got != null)
                {
                    return got;
                }
            }
            return "";
        }

        /// <summary>
        /// 截断显示文本——日志防刷屏（保留头部 + 截断提示）。
        /// </summary>
        /// <param name="text">原文</param>
        /// <param name="max">上限</param>
        /// <returns>截断文本</returns>
        private static string TrimDisplay(string text, int max)
        {
            if (text == null || text.Length <= max)
            {
                if (text == null)
                {
                    return "";
                }
                return text;
            }
            return text.Substring(0, max) + "...";
        }
        /// <summary>
        /// RoundSummary——总结文本自动出声：外观配置启用时后台合成并播放（不阻塞主线程；失败记 Log、不影响轮次）。
        /// </summary>
        /// <param name="text">总结文本（非 ERR| 的正常总结）</param>
        private void SpeakRoundSummary(string text)
        {
            Func<string, string[]> provider = SummaryAudioProvider;
            if (provider == null || text.Length == 0)
            {
                return;
            }
            string catKey = _catKey;
            string[] cfg = provider(catKey);
            if (cfg == null || cfg.Length < 2 || !string.Equals(cfg[0], "1", StringComparison.Ordinal))
            {
                return;
            }
            string voice = cfg[1] == null ? "" : cfg[1];
            string displayName = ResolveSpeakName(catKey);
            Task.Run(delegate ()
            {
                try
                {
                    RunSummarySpeak(catKey, displayName, voice, text);
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 3, "轮末总结播报异常: " + ex.Message, "CHAT");
                }
            });
        }
        /// <summary>
        /// 总结播报产物路径——配置 py.service.kokoro.out_dir 下按时间戳命名（未配置返回空串，不猜目录）。
        /// </summary>
        /// <returns>WAV 全路径；未配置空串</returns>
        private static string BuildSummaryAudioPath()
        {
            ConfigStore cfg = null;
            string dir = "";
            if (DataBox.TryResolve<ConfigStore>(out cfg) && cfg != null)
            {
                dir = cfg.Get("py.service.kokoro.out_dir", "");
            }
            if (dir.Length == 0)
            {
                return "";
            }
            try
            {
                if (!System.IO.Directory.Exists(dir))
                {
                    System.IO.Directory.CreateDirectory(dir);
                }
            }
            catch (Exception)
            {
                return "";
            }
            return System.IO.Path.Combine(dir, "summary_" + DateTime.Now.ToString("yyyyMMdd_HHmmss_fff") + ".wav");
        }
    }
}