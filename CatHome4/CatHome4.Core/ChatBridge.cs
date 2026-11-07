using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Text.Json;
using Mau.Runtime;
using CatHome4.Contracts;

namespace CH4
{
    /// <summary>
    /// 会话协调桥——会话注册表 + 默认猫配置 + 轮转泵 + 会话指令（S1 程序集拆分：Program.Chat 静态面 → Core 实例类）。
    /// 归属：agent 循环基建（Core 层）——LLM 会话调度去中心化；注入提示词构建经委托注入（入口壳 CatCfg 域持有）。
    /// 访问性：internal——仅宿主程序集（CatHome4）经 InternalsVisibleTo 消费；ChatSession 同 internal 契约。
    /// </summary>
    internal sealed class ChatBridge
    {
        // [段1] 默认猫配置面——Bootstrap 从 majordomo cat.cfg 读（M2/M2b/M2c/M2d/R2.3）
        /// <summary>默认会话——P9.1 全部会话指令（Chat/session.new/session clear/count）路由目标；会话寻址扩展在 P9.3</summary>
        private ChatSession _defaultSession;

        /// <summary>默认猫角色段——空=无角色段（M2b）</summary>
        private string _defaultPersona = "";

        /// <summary>默认猫注入清单——空=不注入（M2d）</summary>
        private string[] _defaultInjectList = new string[0];

        /// <summary>默认猫项目挂载——当前挂载的项目名（空=未挂载）</summary>
        private string _defaultProjectMount = "";

        /// <summary>默认猫工具声明面——Bootstrap 按 toolNames 裁剪（M2c；session.new 重注入复用）</summary>
        private ToolSpec[] _defaultToolSpecs;

        /// <summary>默认猫工具名单原始串——授权面实时解析入口（design-ch4-tools §三·十一；逗号 / 空白分隔，空 = 全量保底语义）</summary>
        private string _defaultToolNames = "";

        // [段2] 会话注册表与泵
        /// <summary>会话注册表——PumpSessions 轮转推进（P9.1 含默认会话一席）</summary>
        private readonly List<ChatSession> _sessions = new List<ChatSession>();

        /// <summary>会话新开请求标志——HTTP 线程置位/主线程泵消费（P8.5 session.new——ThreadGuard 契约同 Chat）</summary>
        private volatile bool _sessionNewRequested;

        // [段3] 注入依赖
        /// <summary>注入提示词构建委托——入口壳提供（LoadCatDefaultCfg/FallbackBaseRole——CatCfg 域持有）</summary>
        private readonly Func<WorkspaceConfig, ToolSpec[], string, string[], InjectPromptResult> _buildInjectPrompt;

        /// <summary>
        /// 建立会话协调桥——注入提示词构建委托（入口壳 CatCfg 域提供——角色段/注入知识/工具声明拼装）。
        /// </summary>
        /// <param name="buildInjectPrompt">注入提示词构建：workspace/specs/persona/injectList → 提示词 + 逐文件结果</param>
        public ChatBridge(Func<WorkspaceConfig, ToolSpec[], string, string[], InjectPromptResult> buildInjectPrompt)
        {
            _buildInjectPrompt = buildInjectPrompt;
        }

        /// <summary>默认会话——P9.1 全部会话指令路由目标（Bootstrap 构造后赋值）</summary>
        public ChatSession DefaultSession
        {
            get { return _defaultSession; }
            set { _defaultSession = value; }
        }

        /// <summary>默认猫角色段——空=无角色段（M2b）</summary>
        public string DefaultPersona
        {
            get { return _defaultPersona; }
            set { _defaultPersona = value; }
        }

        /// <summary>默认猫注入清单——空=不注入（M2d）</summary>
        public string[] DefaultInjectList
        {
            get { return _defaultInjectList; }
            set { _defaultInjectList = value; }
        }

        /// <summary>默认猫项目挂载——当前挂载的项目名（空=未挂载；跟猫走，注入报告与解挂消费）</summary>
        public string DefaultProjectMount
        {
            get { return _defaultProjectMount; }
            set { _defaultProjectMount = value; }
        }

        /// <summary>默认猫工具声明面——按 toolNames 裁剪（M2c）</summary>
        public ToolSpec[] DefaultToolSpecs
        {
            get { return _defaultToolSpecs; }
            set { _defaultToolSpecs = value; }
        }

        /// <summary>默认猫工具名单原始串——授权面实时解析（design-ch4-tools §三·十一；空 = 全量保底语义）</summary>
        public string DefaultToolNames
        {
            get { return _defaultToolNames; }
            set { _defaultToolNames = value; }
        }

        /// <summary>会话注册表——编排轮转面（快照/空闲判定遍历共享）</summary>
        public List<ChatSession> Sessions
        {
            get { return _sessions; }
        }

        /// <summary>会话新开请求标志——HTTP 线程置位/主线程泵消费（P8.5 session.new）</summary>
        public bool SessionNewRequested
        {
            get { return _sessionNewRequested; }
            set { _sessionNewRequested = value; }
        }

        /// <summary>
        /// 注册会话进编排轮转表（P9.1 Bootstrap 建立默认会话时调用；注册序 = 推进序）。
        /// </summary>
        /// <param name="session">会话实体</param>
        public void RegisterSession(ChatSession session)
        {
            _sessions.Add(session);
        }

        /// <summary>
        /// 从编排轮转表移除会话（P9.3b cat.delete——会话销毁面）。
        /// </summary>
        /// <param name="session">会话实体</param>
        public void RemoveSession(ChatSession session)
        {
            _sessions.Remove(session);
        }
        /// <summary>
        /// 系统注入入口——等价于「系统在前端输入一行」：按 catKey 定位会话 → 会话侧 sys 入口（类型校验 + 前缀加工 + 入队）。
        /// 失败可见：目标为空 / 会话不存在 / 未知类型 / 空内容 / 停机态未受理一律返回 ERR（不静默丢弃）。
        /// </summary>
        /// <param name="catKey">目标会话（猫 key）</param>
        /// <param name="kind">类型参数（现行仅 systemauto）</param>
        /// <param name="content">注入内容（渠道自带前缀由调用方拼在内容里）</param>
        /// <returns>OK 受理 / ERR|CODE|msg</returns>
        public string PostSystemMessage(string catKey, string kind, string content)
        {
            if (catKey == null || catKey.Length == 0)
            {
                return "ERR|BAD_ARGS|目标 Cat 为空";
            }
            for (int i = 0; i < _sessions.Count; i = i + 1)
            {
                if (_sessions[i].Id == catKey)
                {
                    return _sessions[i].PostSystemMessage(kind, content);
                }
            }
            return "ERR|NO_CAT|目标会话不存在: " + catKey;
        }

        /// <summary>
        /// 会话轮转泵——主循环每帧调用：活跃会话各推进一步。LLM 后台流式与工具批等待期间主线程自由泵其他会话——状态机化核心。
        /// </summary>
        public void PumpSessions()
        {
            for (int i = 0; i < _sessions.Count; i++)
            {
                _sessions[i].Pump();
            }
            double delaysTicks = Stopwatch.GetTimestamp();
            PumpDelays();
            // A254 延迟队列泵分段埋点——cs.delays 段（会话内各段由 ChatSession.Pump 自行埋）
            LoopTiming.Accumulate("cs.delays", (Stopwatch.GetTimestamp() - delaysTicks) * 1000.0 / Stopwatch.Frequency);
        }

        /// <summary>
        /// 延迟队列泵——主循环每帧调用（design-ch4-delay §四）：到点条目按序转入归属会话即时队。
        /// 停机态（宿主重启中）不投递——条目保留，由重启后的新宿主加载后投递。
        /// </summary>
        private void PumpDelays()
        {
            DelayQueue.Pump(DeliverDelay);
        }

        /// <summary>
        /// 延迟条目投递——按猫 key 找会话 → sys 投递入口（入 sys 队列；忙时排队，不打断在途轮次）。
        /// 停机态：未受理（条目保留待重投）；归属会话不存在：条目丢弃并出声（不静默）。
        /// </summary>
        /// <param name="catKey">归属会话（猫 key）</param>
        /// <param name="content">注入内容</param>
        /// <param name="source">来源标记</param>
        /// <returns>true=已受理 / false=未受理（保留待重投）</returns>
        private bool DeliverDelay(string catKey, string content, string source)
        {
            string restartState;
            if (DataBox.TryGet<string>("global", "host_restart_state", out restartState) && restartState == "requested")
            {
                return false;
            }
            for (int i = 0; i < _sessions.Count; i = i + 1)
            {
                if (_sessions[i].Id == catKey)
                {
                    _sessions[i].PostSystemMessage(source, content);
                    return true;
                }
            }
            LogStore.Add("CatHome4", 2, "延迟条目归属会话不存在——已丢弃: cat=" + catKey, "DELAY");
            return true;
        }

        /// <summary>
        /// 任意会话工具批执行中——reload 忙时拒绝面（原 _toolBatchActive 全局标志语义保全）。
        /// </summary>
        /// <returns>true=有会话在工具批执行期</returns>
        public bool IsAnyToolBatchActive()
        {
            for (int i = 0; i < _sessions.Count; i++)
            {
                if (_sessions[i].ToolBatchActive)
                {
                    return true;
                }
            }
            return false;
        }

        /// <summary>
        /// 显式新会话处理——session.new 指令执行体（M2 参数化：按会话 persona/injectList/声明面重注入 + 清前文 + 落盘 + 审计）。
        /// </summary>
        /// <param name="session">目标会话</param>
        /// <param name="persona">角色段（空=仅基础角色）</param>
        /// <param name="injectList">注入清单（空=不注入）</param>
        /// <param name="specs">该会话工具声明面（裁剪后）</param>
        /// <param name="projectMount">当前挂载的项目名（空=未挂载；注入报告 mount 字段单点来源）</param>
        public void HandleSessionNew(ChatSession session, string persona, string[] injectList, ToolSpec[] specs, string projectMount = "")
        {
            WorkspaceConfig ws = null;
            DataBox.TryResolve<WorkspaceConfig>(out ws);
            // M3 新会话生效——拦截面同步最新声明面（改 toolNames 后 session.new 才拉取生效）
            session.SetToolSpecs(specs);
            // A201 显示名按 cat.cfg 现值刷新（新会话取最新；Provider 未接线时保持原值）
            if (ChatSession.DisplayNameProvider != null)
            {
                session.SetDisplayName(ChatSession.DisplayNameProvider(session.Id));
            }
            // 会话标识 ≡ 猫 key（唯一标识）——session.new 不再另起会话身份（LLM 侧身份随猫稳定）
            InjectPromptResult injectResult = _buildInjectPrompt(ws, specs, persona, injectList);
            session.Context.SetSystemPrompt(injectResult.Prompt);
            session.Context.Clear();
            // E3 真实 usage 统计——新会话零统计起算
            session.ResetStats();
            session.Store.Rewrite(session.Context.GetMessages());
            // A87 旧会话留档——清空前导出（user / 正式回复 / 加载报告 / 每轮结算 → sessions_old 落盘）
            session.ArchiveLegacyView();
            // 完整前文定稿——当前份转历史 + 份数轮转（清空前文之前；与留档各自独立）
            session.FinalizeFullContext();
            // F4 视图——session.new 清前文 → 视图随生命周期清空
            session.ClearView();
            // 注入报告——逐文件结果持久化进视图（视图层普通块：ts=0 居首，前端首块渲染）
            session.SetInjectReport(BuildInjectReportJson(injectResult.Files, injectList, specs, projectMount));
            // 问题一修复——会话重置显式事件（前端收到后清空气泡再拉 history——消除清空竞态）
            session.PushSessionReset();
            // 运行态盒——按会话键 + 旧全局键兼容（design-ch4-llm §2.1 会话态独立持有）
            DataBox.Set<string>("global", "chat_state", "idle");
            DataBox.Set<string>("global", "chat_state:" + session.Id, "idle");
            int injectCount = 0;
            if (injectList != null)
            {
                injectCount = injectList.Length;
            }
            string summary = "会话已重建：注入 " + injectCount.ToString() + " 个知识文件，前文已清空";
            LogStore.Add("CatHome4", 1, summary, "CHAT");
        }

        /// <summary>
        /// 构建注入报告 JSON——逐文件结果 + 注入工具组（前端 history 首块渲染；ok/missing/error 三态 + 字符数 + toolGroups 工具清单）。
        /// </summary>
        /// <param name="files">逐文件结果（委托产物）</param>
        /// <param name="injectList">注入清单（null=空）</param>
        /// <param name="specs">该会话工具声明面（裁剪后——Q5 工具组气泡数据源）</param>
        /// <param name="projectMount">当前挂载的项目名（空=未挂载；cat.cfg.projectMount 单点来源）</param>
        /// <returns>注入报告 JSON 字符串</returns>
        private static string BuildInjectReportJson(List<InjectFileResult> files, string[] injectList, ToolSpec[] specs, string projectMount)
        {
            List<string> fileItems = new List<string>();
            int total = 0;
            int ok = 0;
            int missing = 0;
            int failed = 0;
            if (files != null)
            {
                for (int i = 0; i < files.Count; i = i + 1)
                {
                    InjectFileResult f = files[i];
                    string status = f.Status;
                    if (status == "ok") { ok = ok + 1; }
                    else if (status == "missing") { missing = missing + 1; }
                    else { failed = failed + 1; }
                    fileItems.Add(JsonUtil.Object(
                        ("file", f.File),
                        ("status", status),
                        ("message", f.Message ?? ""),
                        ("chars", f.Chars),
                        ("bytes", f.Bytes)));
                    total = total + 1;
                }
            }
            // Q5 注入工具组——按组聚合（工具名→组名映射来自 ToolPool；每工具 name+desc；前端独立气泡全文展示）
            Dictionary<string, List<ToolSpec>> groups = new Dictionary<string, List<ToolSpec>>(StringComparer.Ordinal);
            if (specs != null)
            {
                Dictionary<string, string> ownerMap = ToolPool.BuildOwnerFlowMap();
                for (int i = 0; i < specs.Length; i = i + 1)
                {
                    ToolSpec spec = specs[i];
                    string group = "";
                    if (ownerMap.TryGetValue(spec.Name, out string ownerGroup))
                    {
                        group = ownerGroup;
                    }
                    List<ToolSpec> groupTools;
                    if (!groups.TryGetValue(group, out groupTools))
                    {
                        groupTools = new List<ToolSpec>();
                        groups[group] = groupTools;
                    }
                    groupTools.Add(spec);
                }
            }
            List<string> groupItems = new List<string>();
            foreach (KeyValuePair<string, List<ToolSpec>> kv in groups)
            {
                List<string> toolItems = new List<string>();
                for (int i = 0; i < kv.Value.Count; i = i + 1)
                {
                    toolItems.Add(JsonUtil.Object(("name", kv.Value[i].Name), ("desc", kv.Value[i].Description ?? "")));
                }
                groupItems.Add(JsonUtil.Object(("group", kv.Key), ("tools", JsonUtil.RawArray(toolItems.ToArray()))));
            }
            int injectCount = 0;
            if (injectList != null)
            {
                injectCount = injectList.Length;
            }
            // 项目挂载态——cat.cfg.projectMount 单点来源（跟猫走，不从清单反推）
            string mount = projectMount == null ? "" : projectMount;
            return JsonUtil.Object(
                ("files", JsonUtil.RawArray(fileItems.ToArray())),
                ("total", total),
                ("ok", ok),
                ("missing", missing),
                ("failed", failed),
                ("injectCount", injectCount),
                ("mount", mount),
                ("toolGroups", JsonUtil.RawArray(groupItems.ToArray())));
        }

        /// <summary>
        /// 构建注入摘要文本——/new 指令同步回复面（注入前文与工具气泡可回显给外部通道）。
        /// 纯构建：读 workspace + 调注入提示词构建委托（不触碰会话状态——WS 线程安全）；真正 session.new 由主线程泵异步执行。
        /// </summary>
        /// <param name="persona">角色段</param>
        /// <param name="injectList">注入清单（null=不注入）</param>
        /// <param name="specs">工具声明面</param>
        /// <returns>摘要文本（清单三态 + 工具组清单）</returns>
        public string BuildInjectSummary(string persona, string[] injectList, ToolSpec[] specs)
        {
            WorkspaceConfig ws = null;
            DataBox.TryResolve<WorkspaceConfig>(out ws);
            InjectPromptResult injectResult = _buildInjectPrompt(ws, specs, persona, injectList);
            System.Text.StringBuilder sb = new System.Text.StringBuilder();
            if (injectList != null && injectList.Length > 0)
            {
                sb.Append("清单 " + injectResult.Files.Count + " 个：");
                for (int i = 0; i < injectResult.Files.Count; i++)
                {
                    InjectFileResult f = injectResult.Files[i];
                    if (i > 0)
                    {
                        sb.Append("；");
                    }
                    sb.Append(f.File);
                    if (f.Status == "ok")
                    {
                        sb.Append(" ✓" + f.Chars + "字");
                    }
                    else if (f.Status == "missing")
                    {
                        sb.Append(" ✗缺失");
                    }
                    else
                    {
                        sb.Append(" ✗" + (f.Message ?? "错误"));
                    }
                }
            }
            else
            {
                sb.Append("无清单");
            }
            // 工具组——按组聚合（ToolPool 归属映射同注入报告）
            sb.Append("\n工具组：");
            Dictionary<string, string> ownerMap = ToolPool.BuildOwnerFlowMap();
            Dictionary<string, List<string>> groups = new Dictionary<string, List<string>>(StringComparer.Ordinal);
            if (specs != null)
            {
                for (int i = 0; i < specs.Length; i = i + 1)
                {
                    ToolSpec spec = specs[i];
                    string group = "";
                    if (ownerMap.TryGetValue(spec.Name, out string ownerGroup))
                    {
                        group = ownerGroup;
                    }
                    List<string> names;
                    if (!groups.TryGetValue(group, out names))
                    {
                        names = new List<string>();
                        groups[group] = names;
                    }
                    names.Add(spec.Name);
                }
            }
            bool firstGroup = true;
            foreach (KeyValuePair<string, List<string>> kv in groups)
            {
                if (!firstGroup)
                {
                    sb.Append(" / ");
                }
                firstGroup = false;
                sb.Append(kv.Key + "(" + string.Join(",", kv.Value.ToArray()) + ")");
            }
            return sb.ToString();
        }

        /// <summary>
        /// 构建注入系统提示词——转发注入委托（入口壳 CatCfg 域实现——角色 + 注入知识 + 工具语义声明）。
        /// </summary>
        /// <param name="workspace">工作区配置（roots + inject 清单）</param>
        /// <param name="specs">工具声明表</param>
        /// <param name="persona">角色段</param>
        /// <param name="injectList">注入清单</param>
        /// <returns>系统提示词</returns>
        public string BuildPrompt(WorkspaceConfig workspace, ToolSpec[] specs, string persona, string[] injectList)
        {
            return _buildInjectPrompt(workspace, specs, persona, injectList).Prompt;
        }

        /// <summary>
        /// 会话调试指令执行——session clear（清空保留 system）/ session count（消息数）/ note.start / note.add；仅主线程调用（泵消费/CLI 直执）。
        /// </summary>
        /// <param name="session">目标会话</param>
        /// <param name="cmd">指令文本</param>
        public void HandleSessionCmd(ChatSession session, string cmd)
        {
            if (cmd == "note.start")
            {
                // M4c Note 启动——主线程执行（泵消费/CLI 直执）
                session.NoteStart();
                return;
            }
            if (cmd.StartsWith("note.add ", StringComparison.Ordinal))
            {
                // M4c Note 手动新增——主线程执行（泵消费/CLI 直执）
                session.NoteAdd(cmd.Substring(9).Trim());
                return;
            }
            if (cmd == "session clear")
            {
                session.Context.Clear();
                session.Store.Rewrite(session.Context.GetMessages());
                // 问题一附带——session clear 同步清视图（视图随生命周期清理；注入报告保留——非会话轮次产物）
                session.ClearView();
                LogStore.Add("CatHome4", 1, "会话已清空（保留系统提示词）", "CMD");
                return;
            }
            if (cmd == "session count")
            {
                return;
            }
        }


        /// <summary>前文条目单条正文上限（字符）——弹层展示截断阈值（truncated 标记 + chars 给真实长度）</summary>
        private const int ContextItemLimit = 8000;

        /// <summary>前文条目摘要上限（字符）——弹层折叠行显示</summary>
        private const int ContextPreviewLimit = 120;

        /// <summary>
        /// 构建前文条目视图 JSON——对话页状态栏「前文 n 条 / n tokens」点击弹层数据源（GET /api/v1/context）。
        /// 条目源 = 会话消息序列（送入 LLM 的真实前文——含 system 注入与 tool 结果）；每条正文截断 ≤ContextItemLimit 字符（truncated 标记 + chars 真实长度）。
        /// ctxTokens = ContextTokensKnown（真实 usage 值，零估算）；线程模型同其余视图出口（HTTP 线程直读内存真源）。
        /// </summary>
        /// <param name="session">目标会话（P9.3 按猫参数化——每猫闭包传各自会话）</param>
        /// <param name="max">返回条目上限（1-500 夹取，缺省 200；超出取尾部 + 恒含首条）</param>
        /// <returns>前文视图 JSON</returns>
        public string BuildContextView(ChatSession session, int max)
        {
            LlmMessage[] msgs;
            try
            {
                msgs = session.Context.GetMessages();
            }
            catch (Exception ex)
            {
                // 失败可见——并发写入致快照失败时显式报错（不静默返回空列表）
                LogStore.Add("CatHome4", 2, "前文条目快照失败: " + ex.Message, "HTTP");
                Dictionary<string, object> err = new Dictionary<string, object>();
                err["ok"] = false;
                err["error"] = "前文快照失败: " + ex.Message;
                return JsonUtil.Serialize(err);
            }
            int total = msgs.Length;
            int start = 0;
            if (max > 0 && total > max)
            {
                start = total - max;
            }
            long totalChars = 0;
            for (int i = 0; i < total; i++)
            {
                totalChars = totalChars + MessageBodyChars(msgs[i]);
            }
            List<object> items = new List<object>();
            if (start > 0)
            {
                // 首条恒在窗口内——前文头部（system 注入块）不因尾部窗口而不可见（2026-10-02 判例）
                items.Add(BuildContextItem(msgs, 0));
            }
            for (int i = start; i < total; i++)
            {
                items.Add(BuildContextItem(msgs, i));
            }
            Dictionary<string, object> resp = new Dictionary<string, object>();
            resp["ok"] = true;
            resp["sessionId"] = session.Id;
            resp["count"] = total;
            resp["start"] = start + 1;
            resp["shown"] = items.Count;
            resp["chars"] = totalChars;
            resp["ctxTokens"] = session.ContextTokensKnown;
            resp["items"] = items;
            return JsonUtil.Serialize(resp);
        }
        /// <summary>
        /// 单条前文条目——i / role / tool / chars / time / truncated / preview / content（视图与完整前文同构）。
        /// </summary>
        /// <param name="msgs">消息序列</param>
        /// <param name="index">消息下标（0 基）</param>
        /// <returns>条目对象</returns>
        private Dictionary<string, object> BuildContextItem(LlmMessage[] msgs, int index)
        {
            LlmMessage m = msgs[index];
            string full = MessageBody(m);
            bool truncated = full.Length > ContextItemLimit;
            string body = truncated ? full.Substring(0, ContextItemLimit) : full;
            Dictionary<string, object> item = new Dictionary<string, object>();
            item["i"] = index + 1;
            item["role"] = RoleName(m.Role);
            item["tool"] = m.ToolName == null ? "" : m.ToolName;
            item["chars"] = full.Length;
            item["time"] = m.CreatedAt;
            item["truncated"] = truncated;
            item["preview"] = MessagePreview(full);
            item["content"] = body;
            return item;
        }
        /// <summary>
        /// 构建会话关键信息视图 JSON——对话页状态栏「前文关键信息」点击弹层数据源（GET /api/v1/keyinfo）。
        /// 内容 = 旧会话留档同源四部分（加载报告 / user 消息 / 正式回复 / 每轮结算）；条目形态与 BuildContextView 同构。
        /// </summary>
        /// <param name="session">目标会话（P9.3 按猫参数化——每猫闭包传各自会话）</param>
        /// <param name="max">返回条目上限（1-500 夹取，缺省 200；超出取尾部）</param>
        /// <returns>关键信息视图 JSON</returns>
        public string BuildKeyInfoView(ChatSession session, int max)
        {
            return session.BuildKeyInfoView(max);
        }

        /// <summary>
        /// 构建会话完整前文视图 JSON——对话页弹层「完整前文」数据源（GET /api/v1/fullctx）。
        /// 内容 = 送入 LLM 的全量消息（留档文本剥离修饰后还原）；条目形态与 BuildContextView 同构。
        /// </summary>
        /// <param name="session">目标会话（按猫参数化——每猫闭包传各自会话）</param>
        /// <param name="max">返回条目上限（1-500 夹取，缺省 200；超出取尾部）</param>
        /// <returns>完整前文视图 JSON</returns>
        public string BuildFullContextView(ChatSession session, int max)
        {
            return session.BuildFullContextView(max);
        }

        /// <summary>前文条目正文——四字段合并（content / tool_calls / reasoning / images；单点实现见 FullContextStore.MergeBody）。</summary>
        /// <param name="m">消息</param>
        /// <returns>正文（无正文=空串）</returns>
        private static string MessageBody(LlmMessage m)
        {
            return FullContextStore.MergeBody(m.Content, m.ToolCallsJson, m.ReasoningContent, m.ImagesJson);
        }

        /// <summary>前文条目正文长度——统计用（截断前真实长度）</summary>
        /// <param name="m">消息</param>
        /// <returns>字符数</returns>
        private static long MessageBodyChars(LlmMessage m)
        {
            return MessageBody(m).Length;
        }

        /// <summary>前文条目摘要——单行化后取首 ContextPreviewLimit 字符（弹层折叠行显示）</summary>
        /// <param name="body">条目正文</param>
        /// <returns>摘要文本</returns>
        private static string MessagePreview(string body)
        {
            if (body == null || body.Length == 0)
            {
                return "";
            }
            string flat = body.Replace("\r", " ").Replace("\n", " ");
            if (flat.Length > ContextPreviewLimit)
            {
                flat = flat.Substring(0, ContextPreviewLimit) + "…";
            }
            return flat;
        }

        /// <summary>前文条目角色名——OpenAI 兼容小写四 role（前端样式类锚）</summary>
        /// <param name="role">角色枚举</param>
        /// <returns>角色名</returns>
        private static string RoleName(LlmRole role)
        {
            if (role == LlmRole.System) { return "system"; }
            if (role == LlmRole.User) { return "user"; }
            if (role == LlmRole.Assistant) { return "assistant"; }
            return "tool";
        }


    }
}

/// <summary>
/// 注入提示词构建结果——提示词 + 逐文件结果（问题二：前文加载明细可见性）。
/// 只承载结果——不再裸拼字符串（入口壳 BuildInjectPrompt 产物）。
/// </summary>
internal sealed class InjectPromptResult
{
    /// <summary>提示词——角色 + 注入知识 + 工具语义声明（原文拼装产物）</summary>
    public string Prompt;

    /// <summary>逐文件结果——按注入清单序（ok/missing/error）</summary>
    public List<InjectFileResult> Files;
}

/// <summary>
/// 加载结果——单文件加载明细（前端报告视图块渲染单元）。
/// </summary>
internal sealed class InjectFileResult
{
    /// <summary>文件路径——注入清单原文（受控根 id: 或绝对路径）</summary>
    public string File;

    /// <summary>状态——ok=成功 / missing=缺失跳过 / error=读取异常</summary>
    public string Status;

    /// <summary>补充信息——error 时错误摘要；ok 时可为空</summary>
    public string Message;

    /// <summary>成功时字符数——注入内容长度（0=失败）</summary>
    public int Chars;
    /// <summary>文件字节数——token 估算口径（与 PromptPlanBuilder.TokensPerByte 同基准；0=未取到）</summary>
    public long Bytes;
}
