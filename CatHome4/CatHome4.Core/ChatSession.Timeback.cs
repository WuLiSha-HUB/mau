using System;
using System.Collections.Generic;
using System.Text.Json;
using System.Threading.Tasks;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// 宿主会话实体——timeback 上下文作用域分部（design-ch4-timeback §2 / §三 · 2026-09-28 语义重设 · 2026-10-09 拆两名）。
    /// 工具面：timeback-start（开锚）· timeback-back（回卷回收）——原单工具 timeback（action=start|back）拆分，功能不变。
    /// 语义（莎定）：**锚点 = start 的 tool_calls 声明本身；回收 = back 的返回值**。
    ///   前文形态：[start 声明][start 结果] [查证过程…] [域内人工输入…] [back 声明][back 结果 = findings]
    ///   回收动作：删除「start 结果之后、back 声明之前」的全部消息——两次调用对与结论原样保留；
    ///   **域内人工输入保留**（2026-11-07 定）：用户在这个域里发的消息不随域销毁，按原序落回锚定结果之后、back 声明之前；
    ///   系统注入（`SysInjectPrefix` 行头——域规范卡 / 状态自述 / 图片注入）不在保留面，仍随域删除。
    /// 由此收益：历史里不新增注入消息（无连续 user）、无绕点搜索、无轮末特判、无「劈开工具对」风险；
    /// 回卷后序列天然合法，本轮照常续跑（LLM 直接从 back 的工具返回继续）。
    /// 用途（2026-10-03 莎裁）：仅两个域限定工具解锁——主干识图（image-inject）· 浏览网页（browser-*）；域内不做工作、不写计划。
    /// findings 口径（A205 · 2026-10-06 莎定）：**只写「成果在哪」，不写「结论是什么」**——域内不下判断，主干按位置回读、以回读到的真实内容为准（治域内复述致幻觉）。
    /// 思考净化（2026-10-09 莎定）：回收时把 **back 声明**的 `ReasoningContent` 换成占位句——它与 findings 同源（回域时的断言），
    ///   且不在 findings 治理面（模型自然输出）；start 声明的思考（工具需求生成过程）保留。原文留视图层与归档。
    /// 锁定：作用域存活期间 Note / sleep / timer 不可用（ERR|TIMEBACK_LOCKED——与区间删除语义冲突）。
    /// 视图层：删除区间对应的前文派生块合并为一个废弃块（Rebuild 不清——跨宿主重启仍可回看）。
    /// 归档（A104）：全局计数（Data/runtime/timeback/count.json）+ 每次回收一个作用域文件（&lt;编号&gt;-&lt;时间戳&gt;.jsonl）。
    /// </summary>
    internal sealed partial class ChatSession
    {
        /// <summary>归档目录提供者——catKey → Data/runtime/timeback 目录（宿主启动期由组合根注入；空=归档不可用）。</summary>
        internal static Func<string, string> TimebackArchiveDirProvider;

        /// <summary>info 快照提供者——归档文件第二行的现场记录（宿主启动期由组合根注入；空=该行不写）。</summary>
        internal static Func<string> TimebackInfoProvider;

        /// <summary>timeback 作用域——内存单对象（v1 不嵌套；宿主重启即失效，兜底走 session.rollback）。</summary>
        private sealed class TimebackScope
        {
            /// <summary>作用域编号——本猫内永久递增（归档 open/close 以 id 关联）。</summary>
            public long Id;

            /// <summary>start 的 tool_calls 声明消息索引——锚点（回收时保留，含其后续的 start 结果）。</summary>
            public int StartDeclIndex;

            /// <summary>back 的 tool_calls 声明消息索引——回收区间上界（回收时保留，含其后续的 back 结果）。</summary>
            public int BackDeclIndex;

            /// <summary>开锚时刻——Unix 毫秒。</summary>
            public long StartAtMs;

            /// <summary>域类型（`TimebackProfile` 枚举）——决定域内工具白名单 / 回执骨架 / 资源预热；开锚时登记。</summary>
            public string Type = "";

            /// <summary>用途标签——归档与事后判读原料。</summary>
            public string Purpose = "";

            /// <summary>待执行回收的带回载荷——back 已作为工具返回值送出，批后段据此执行区间删除。</summary>
            public string PendingFindings;

            /// <summary>作用域内事件累计——think / 工具完成 / assistant 产出各计 1（状态自述阈值数据源）。</summary>
            public long EventCount;

            /// <summary>上次状态自述时的计数水位——距其再满 25 个事件注入下一条（25 / 50 / 75…）。</summary>
            public long LastNotifyCount;

            /// <summary>开锚时的已知前文长度快照——回收时对比算净增（请求级真实 usage 值，零估算）。</summary>
            public long TokensAtOpen;

            /// <summary>本次回收的释放条数——back 时刻预算（区间上下界已定，精确可算）；随返回值送出，并与批后实际删除数对账。</summary>
            public int PendingReleased;
            /// <summary>
            /// 本域工具台账——宿主记录（域内**用过的每个工具**逐条登记：工具名 · 目标标识 · 成败；含只读与被拒）。
            /// 客观事实面：从实际执行流水提取（不由 LLM 自述）；back 回执附于 findings 之前，主干据此抽样核对。
            /// </summary>
            public List<string> WriteLog = new List<string>();

            /// <summary>域规范卡是否已注入——批后段一次性（§十一）；防同域重复注入。</summary>
            public bool CardPosted;
        }

        /// <summary>当前未闭合作用域——null=无作用域。</summary>
        private TimebackScope _timebackScope;

        /// <summary>timeback 未闭合自动拉起计数——同域累计（A229；达上限后放行本轮结束——防模型不回收导致的无限空转）。</summary>
        private int _timebackPullCount;

        /// <summary>归档实例——懒建（首次使用时按本猫路径构造）。</summary>
        private TimebackArchive _timebackArchive;

        /// <summary>
        /// timeback 活跃态——start 之后至 back 回收完成。
        /// 消费方：QQ 转发豁免（不消费来源 / 不推进游标）· 工具锁定（Note / sleep / timer）· 观测面。
        /// </summary>
        public bool TimebackActive
        {
            get
            {
                return _timebackScope != null;
            }
        }

        /// <summary>
        /// timeback-start 执行体——参数面（purpose 必填；未知参数拒绝）。
        /// 锚点 = 本刻前文末条（即本次调用的 tool_calls 声明）；回收由 timeback-back 承担。
        /// </summary>
        /// <param name="argsJson">参数 JSON（purpose）</param>
        /// <returns>结构化结果（元数据头 + 正文；失败 ERR| 前缀）</returns>
        private string ExecuteTimebackStart(string argsJson)
        {
            string type = "";
            string purpose = "";
            // [段0] 参数面——type / purpose 必填 + 未知参数拒绝（零容忍；catId 宿主保留键放行）
            if (argsJson != null && argsJson.Length > 0 && argsJson.StartsWith("{"))
            {
                try
                {
                    using (JsonDocument doc = JsonUtil.ParseStrict(argsJson))
                    {
                        JsonElement root = doc.RootElement;
                        foreach (JsonProperty property in root.EnumerateObject())
                        {
                            if (property.Name == "catId")
                            {
                                continue;
                            }
                            if (property.Name == "type")
                            {
                                if (property.Value.ValueKind != JsonValueKind.String)
                                {
                                    return "ERR|TIMEBACK_ARGS|type 需为字符串";
                                }
                                type = property.Value.GetString() ?? "";
                                continue;
                            }
                            if (property.Name == "purpose")
                            {
                                if (property.Value.ValueKind != JsonValueKind.String)
                                {
                                    return "ERR|TIMEBACK_ARGS|purpose 需为字符串";
                                }
                                purpose = property.Value.GetString() ?? "";
                                continue;
                            }
                            return "ERR|TIMEBACK_ARGS|未知参数: " + property.Name + "（支持 type / purpose）";
                        }
                    }
                }
                catch (Exception ex)
                {
                    return "ERR|TIMEBACK_ARGS|timeback-start 参数解析失败: " + ex.Message;
                }
            }
            return TimebackStart(type, purpose);
        }
        /// <summary>
        /// timeback-back 执行体——参数面（findings 必填；未知参数拒绝）。
        /// 载荷即本次调用的返回值；区间删除在工具批后段执行（ApplyTimebackBack）。
        /// </summary>
        /// <param name="argsJson">参数 JSON（findings）</param>
        /// <returns>结构化结果（元数据头 + 正文；失败 ERR| 前缀）</returns>
        private string ExecuteTimebackBack(string argsJson)
        {
            string findings = "";
            // [段0] 参数面——findings 必填 + 未知参数拒绝（零容忍；catId 宿主保留键放行）
            if (argsJson != null && argsJson.Length > 0 && argsJson.StartsWith("{"))
            {
                try
                {
                    using (JsonDocument doc = JsonUtil.ParseStrict(argsJson))
                    {
                        JsonElement root = doc.RootElement;
                        foreach (JsonProperty property in root.EnumerateObject())
                        {
                            if (property.Name == "catId")
                            {
                                continue;
                            }
                            if (property.Name == "findings")
                            {
                                if (property.Value.ValueKind != JsonValueKind.String)
                                {
                                    return "ERR|TIMEBACK_ARGS|findings 需为字符串";
                                }
                                findings = property.Value.GetString() ?? "";
                                continue;
                            }
                            return "ERR|TIMEBACK_ARGS|未知参数: " + property.Name + "（支持 findings）";
                        }
                    }
                }
                catch (Exception ex)
                {
                    return "ERR|TIMEBACK_ARGS|timeback-back 参数解析失败: " + ex.Message;
                }
            }
            return TimebackBack(findings);
        }

        /// <summary>
        /// start——登记作用域：锚点 = 本刻前文末条（本次 start 的 tool_calls 声明，工具批执行点已在盘上）。
        /// v1 未闭合前禁止再次 start。
        /// </summary>
        /// <param name="type">域类型（`TimebackProfile` 枚举——必填；决定域内白名单 / 回执骨架 / 资源预热）</param>
        /// <param name="purpose">用途标签</param>
        /// <returns>回执文本</returns>
        private string TimebackStart(string type, string purpose)
        {
            if (_timebackScope != null)
            {
                return "ERR|TIMEBACK_NESTED|已有未闭合作用域 #" + _timebackScope.Id.ToString() + "——先 back 再 start";
            }
            if (!TimebackProfile.IsValid(type))
            {
                return "ERR|TIMEBACK_ARGS|start 需要合法 type（域类型）: " + TimebackProfile.TypeListText()
                    + (type.Length == 0 ? "" : "——收到「" + type + "」");
            }
            if (purpose.Length == 0)
            {
                return "ERR|TIMEBACK_ARGS|start 需要 purpose（用途标签）";
            }
            int declIndex = _context.GetMessageCount() - 1;
            if (declIndex < 0)
            {
                return "ERR|TIMEBACK_ANCHOR|前文为空——无锚点可记";
            }
            TimebackScope scope = new TimebackScope();
            scope.StartDeclIndex = declIndex;
            scope.StartAtMs = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
            scope.Type = type;
            scope.Purpose = purpose.Length > 48 ? purpose.Substring(0, 48) : purpose;
            // 开锚快照——回收时据此算作用域净增 token（真实 usage 值，不估算）
            scope.TokensAtOpen = ContextTokensKnown;
            // A104 新机制——取号 = 全局计数落盘（count.json；跨猫 / 跨重启递增）；不再写 open 行
            // （回收时一次成档：首行 meta + info 快照 + 被删前文消息）；开了不回 = 号已耗而无文件
            TimebackArchive archive = ResolveTimebackArchive();
            if (archive != null)
            {
                scope.Id = archive.NextId();
            }
            _timebackScope = scope;
            // [段6] 浏览器实例预热——**按域类型**（design-ch4-timeback-type §五）：仅名单含浏览器族的 type 预热；
            // 其余 type 不起进程（免「每次开域起一个浏览器后台」）。域 = 浏览器进程容器：开锚即起，锚关即关（CloseCat）；
            // 锚内 browser-* 工具只操作该进程（多页签 = 同一进程内的多个 target），不涉及孤儿与交接。
            // 失败不阻断开锚（浏览器起不来不该让取证任务开不了局）——锚内工具调用会重试并如实返回 ERR
            if (TimebackProfile.NeedsBrowser(scope.Type))
            {
                Mau.Runtime.IBrowserLifecycle browserLifecycle;
                Mau.Runtime.DataBox.TryResolve<Mau.Runtime.IBrowserLifecycle>(out browserLifecycle);
                if (browserLifecycle != null)
                {
                    // A254 主循环耗时治理（2026-10-16）——预热转后台：开锚不阻塞 tick；
                    // 锚内首次 browser-* 调用经 Ensure 的 _gate 锁自然等待预热完成（等待计入该工具自身执行耗时）
                    Mau.Runtime.IBrowserLifecycle warmLifecycle = browserLifecycle;
                    string warmCatKey = _catKey;
                    Task.Run(delegate () { warmLifecycle.PrepareCat(warmCatKey); });
                }
            }
            Dictionary<string, object> fields = new Dictionary<string, object>();
            fields["anchor"] = declIndex;
            fields["type"] = scope.Type;
            fields["purpose"] = scope.Purpose;
            // §十一——start 结果**只留结构化头**：规范与骨架迁入「域规范卡」（TimebackCard · 批后段注入 systemauto user）。
            // 动机：规范住在 start 结果里 + 每域重复一份 → 同形模板随会话堆积，模型失去「哪份是活跃的」锚点（判例见 §十一 · `_log` 批 4）。
            LogStore.Add("CatHome4", 1, "timeback #" + scope.Id.ToString() + " 开锚（锚点 " + declIndex.ToString() + " / 用途 " + scope.Purpose + "）", "TIMEBACK");
            return ToolMetaHead.With("timeback-start", true, scope.Id.ToString(), -1, fields, "");
        }

        /// <summary>
        /// back——校验后返回载荷（findings 即本次工具调用的返回值）；区间删除在工具批后段执行。
        /// </summary>
        /// <param name="findings">带回载荷（成果定位：位置 + 简短描述，不下结论）</param>
        /// <returns>回执 + findings 全文（工具返回值）</returns>
        private string TimebackBack(string findings)
        {
            if (_timebackScope == null)
            {
                return "ERR|TIMEBACK_NO_SCOPE|无未闭合作用域——back 无对象";
            }
            if (findings.Length == 0)
            {
                return "ERR|TIMEBACK_ARGS|back 需要 findings（带回载荷）";
            }
            _timebackScope.PendingFindings = findings;
            _timebackScope.BackDeclIndex = _context.GetMessageCount() - 1;
            // 释放条数预算——区间上下界此刻已定（[start 结果之后 .. back 声明之前]），精确可算；
            // 该值随返回值送出，并与批后实际删除数对账（归档记实际）
            _timebackScope.PendingReleased = CountTimebackReleased(_timebackScope);
            long tokensNow = ContextTokensKnown;
            Dictionary<string, object> fields = new Dictionary<string, object>();
            fields["anchor"] = _timebackScope.StartDeclIndex;
            fields["type"] = _timebackScope.Type;
            fields["purpose"] = _timebackScope.Purpose;
            fields["tokens"] = tokensNow;
            fields["grew"] = tokensNow - _timebackScope.TokensAtOpen;
            fields["tools"] = _timebackScope.WriteLog.Count;
            // 情报行——裁剪读数以自然语言进正文（条数 / token / 写入三项皆宿主实测），令**返回体自足**：
            // 不必回解结构头即可读到准确情报；表体（工具台账）紧随其后。
            string infoLine = "📉 释放 " + _timebackScope.PendingReleased.ToString() + " 条前文 · 域内节约 "
                + (tokensNow - _timebackScope.TokensAtOpen).ToString() + "token · 工具使用 "
                + _timebackScope.WriteLog.Count.ToString() + " 次";
            if (_timebackScope.WriteLog.Count > 0)
            {
                infoLine = infoLine + "（详见下表）";
            }
            // 台账附于 findings 之前——头 = 宿主事实（从执行流水提取，不可编），体 = LLM 自述；主干据此抽样核对
            return ToolMetaHead.With("timeback-back", true, _timebackScope.Id.ToString(), _timebackScope.PendingReleased, fields, infoLine + "\n" + BuildTimebackWrites() + findings + Environment.NewLine + Environment.NewLine + TimebackCard.Readback(_timebackScope.Type));
        }

        /// <summary>
        /// 释放条数预算——按当前前文算「[start 结果之后 .. back 声明之前]」的消息条数。
        /// back 时刻区间上下界已定，故返回值给出的条数与批后实际删除数同源可对账（归档记实际，不一致记 L2）。
        /// </summary>
        /// <param name="scope">作用域</param>
        /// <returns>释放条数（无可删区间 = 0）</returns>
        private int CountTimebackReleased(TimebackScope scope)
        {
            LlmMessage[] all = _context.GetMessages();
            int keepEnd = scope.StartDeclIndex;
            while (keepEnd + 1 < all.Length && all[keepEnd + 1].Role == LlmRole.Tool)
            {
                keepEnd = keepEnd + 1;
            }
            int from = keepEnd + 1;
            int to = scope.BackDeclIndex - 1;
            if (from > to || from >= all.Length)
            {
                return 0;
            }
            if (to >= all.Length)
            {
                to = all.Length - 1;
            }
            // 域内人工输入不随域销毁（与 ApplyTimebackBack 同源判定）——预算与批后实际删除数因此仍同源可对账
            int keptUsers = 0;
            for (int i = from; i <= to && i < all.Length; i = i + 1)
            {
                if (IsHumanUserMessage(all[i]))
                {
                    keptUsers = keptUsers + 1;
                }
            }
            return to - from + 1 - keptUsers;
        }
        /// <summary>
        /// 域内人工输入判定——回卷时唯一保留的域内消息类型（其余一律销毁）。
        /// 判据：role=user 且正文不带系统注入行头（`SysInjectPrefix`）——系统注入与图片注入一律经
        /// <see cref="BuildSysInjectText"/> 单点套前缀，故该前缀即「非人工」的前文判据（LlmMessage 无 source 字段）。
        /// </summary>
        /// <param name="message">前文消息</param>
        /// <returns>true=人工输入（回卷保留）</returns>
        private bool IsHumanUserMessage(LlmMessage message)
        {
            if (message.Role != LlmRole.User)
            {
                return false;
            }
            string content = message.Content == null ? "" : message.Content;
            return !content.StartsWith(SysInjectPrefix, StringComparison.Ordinal);
        }

        /// <summary>
        /// 批后回收执行——工具批结果全部回填后调用（design §12.2）：
        /// ① 前文删除「start 结果之后、back 声明之前」的全部消息（两次调用对与结论保留）；back 声明的思考换成占位句（净化 · 2026-10-09）
        /// ② 视图层移出「**锚定声明之后**、back 声明（含）」的块 → 合并为一个废弃块（`void`；timeback 自己的卡保留在对话流）
        /// ③ 归档一次成档（A104：首行 meta + 该猫 info 快照 + 被删前文消息）→ 关闭作用域。本轮照常续跑（不置工具主动 done、不注入消息）。
        /// 前文无可删区间（同批 start+back / 索引异常）时跳过 ①，② 仍执行（锚定批的 sibling 结果卡照归废弃段）。
        /// </summary>
        private void ApplyTimebackBack()
        {
            TimebackScope scope = _timebackScope;
            if (scope == null || scope.PendingFindings == null)
            {
                return;
            }
            LlmMessage[] all = _context.GetMessages();
            // 保留终点 = start 声明 + 其完整结果块（同批多调用时结果不止一条——逐条数到非 tool 为止）
            int keepEnd = scope.StartDeclIndex;
            while (keepEnd + 1 < all.Length && all[keepEnd + 1].Role == LlmRole.Tool)
            {
                keepEnd = keepEnd + 1;
            }
            int from = keepEnd + 1;
            int to = scope.BackDeclIndex - 1;
            int removed = 0;
            int keptUsers = 0;
            List<LlmMessage> removedMessages = new List<LlmMessage>();
            // [段0] 现场采集——info 快照取删除之前（归档第二行记录回收动作发生时的状态）
            string infoJson = CollectTimebackInfo();
            long nowMs = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
            long seconds = (nowMs - scope.StartAtMs) / 1000;
            if (seconds < 0)
            {
                seconds = 0;
            }
            // [段1] 前文删除——保留 [0..保留终点] + [back 声明..尾]（同批 start+back 时区间为空）
            if (from <= to && from < all.Length)
            {
                if (to >= all.Length)
                {
                    to = all.Length - 1;
                }
                int secondStart = scope.BackDeclIndex;
                if (secondStart < keepEnd + 1)
                {
                    secondStart = keepEnd + 1;
                }
                List<LlmMessage> keep = new List<LlmMessage>();
                for (int i = 0; i <= keepEnd && i < all.Length; i = i + 1)
                {
                    keep.Add(all[i]);
                }
                // 域内人工输入保留（2026-11-07 定）——区间只销毁过程，用户在这个域里说的话必须回到前文：
                // 按原序插在锚定结果之后、back 声明之前（时序与它在域内的位置一致）；系统注入（[systemauto] 行头）不在此列，随域删除。
                for (int i = from; i <= to && i < all.Length; i = i + 1)
                {
                    if (!IsHumanUserMessage(all[i]))
                    {
                        continue;
                    }
                    keep.Add(all[i]);
                    keptUsers = keptUsers + 1;
                }
                for (int i = secondStart; i < all.Length; i = i + 1)
                {
                    // back 声明——思考净化（莎 2026-10-09 定）：该思考是对域内工作的断言，不是工具事实；
                    // 换成占位句，原文留视图层与归档（start 声明的思考 = 工具需求生成过程，保留）
                    if (i == scope.BackDeclIndex)
                    {
                        keep.Add(WithTimebackBackReasoningCleared(all[i]));
                        continue;
                    }
                    keep.Add(all[i]);
                }
                // 被删前文快照——归档文件的正文（删除前按原序取值；保留下来的用户消息仍在会话里，不入被删档）
                for (int i = from; i <= to && i < all.Length; i = i + 1)
                {
                    if (IsHumanUserMessage(all[i]))
                    {
                        continue;
                    }
                    removedMessages.Add(all[i]);
                }
                removed = all.Length - keep.Count;
                // [段2] 前文落盘——区间删除重写（append-only 的合法例外）
                _context.ReplaceMessages(keep.ToArray());
                LlmMessage[] toSave = _context.GetMessages();
                _store.Rewrite(toSave);
            }
            else
            {
                if (from <= to)
                {
                    // 索引超界（前文被外部改动）——不删，仅出声
                    LogStore.Add("CatHome4", 2, "timeback 回收区间越界（from " + from.ToString() + " / to " + to.ToString() + " / len " + all.Length.ToString() + "）——本次未删", "TIMEBACK");
                }
                // [段1b] back 声明思考净化——未走删除路径（同批开收 / 区间为空 / 索引越界）时索引未变，就地处理
                LlmMessage[] current = _context.GetMessages();
                if (scope.BackDeclIndex >= 0 && scope.BackDeclIndex < current.Length)
                {
                    LlmMessage clearedDecl = WithTimebackBackReasoningCleared(current[scope.BackDeclIndex]);
                    if (!string.Equals(clearedDecl.ReasoningContent, current[scope.BackDeclIndex].ReasoningContent, StringComparison.Ordinal))
                    {
                        current[scope.BackDeclIndex] = clearedDecl;
                        _context.ReplaceMessages(current);
                        _store.Rewrite(_context.GetMessages());
                    }
                }
            }
            // [段3] 视图层——不动（A165 v2 契约：持久即持久，移出 / 废弃面退役；回收只作用于送入 LLM 的前文）
            // [段4] 归档——一次回收一个文件（A104）：首行 meta + 该猫 info 快照 + 被删前文消息
            bool archived = WriteTimebackArchiveFile(scope, removed, keptUsers, removedMessages, infoJson, nowMs, seconds);
            // [段5] 释放条数对账——back 返回值给出的预算 vs 批后实际删除数（归档记实际；不一致必须出声）
            if (removed != scope.PendingReleased)
            {
                LogStore.Add("CatHome4", 2, "timeback #" + scope.Id.ToString() + " 释放条数对账不一致（预算 " + scope.PendingReleased.ToString() + " / 实际 " + removed.ToString() + "）", "TIMEBACK");
            }
            // [段6] 作用域关闭——活跃期结束（TimebackActive 回 false；Note / sleep / timer 解锁）
            _timebackScope = null;
            string keptNote = keptUsers > 0 ? " · 保留人工输入 " + keptUsers.ToString() + " 条" : "";
            LogStore.Add("CatHome4", 1, "timeback #" + scope.Id.ToString() + " 已回收 " + removed.ToString() + " 条" + keptNote + "（" + seconds.ToString() + " 秒 / 锚点 " + scope.StartDeclIndex.ToString() + "）", "TIMEBACK");
            if (!archived)
            {
                LogStore.Add("CatHome4", 2, "timeback #" + scope.Id.ToString() + " 归档落档失败（运行不受影响）", "TIMEBACK");
            }
            // [段7] 浏览器实例关闭——域级生命周期（A110）：浏览器是非主干信息的载体，域收即关（不跨域残留）
            Mau.Runtime.IBrowserLifecycle browserLifecycle;
            Mau.Runtime.DataBox.TryResolve<Mau.Runtime.IBrowserLifecycle>(out browserLifecycle);
            if (browserLifecycle != null)
            {
                browserLifecycle.CloseCat(_catKey);
            }
        }

        /// <summary>
        /// 归档实例解析——懒建（首次使用时按归档目录构造）；provider 未接线或目录为空 → null（归档不可用，功能不阻断）。
        /// </summary>
        /// <returns>归档实例（null=不可用）</returns>
        private TimebackArchive ResolveTimebackArchive()
        {
            if (_timebackArchive != null)
            {
                return _timebackArchive;
            }
            if (TimebackArchiveDirProvider == null)
            {
                LogStore.Add("CatHome4", 2, "timeback 归档目录提供者未接线——本次不落档", "TIMEBACK");
                return null;
            }
            string dir = TimebackArchiveDirProvider(_catKey);
            if (dir == null || dir.Length == 0)
            {
                LogStore.Add("CatHome4", 2, "timeback 归档目录解析为空——本次不落档", "TIMEBACK");
                return null;
            }
            _timebackArchive = new TimebackArchive(dir);
            return _timebackArchive;
        }
        /// <summary>
        /// 归档写出一一一次回收一个文件（A104）：首行 meta + 该猫 info 快照 + 被删前文消息。
        /// 文件名 &lt;编号&gt;-&lt;回收时刻&gt;.jsonl；失败不阻断回收（记 L3 + 写面标志）。
        /// </summary>
        /// <param name="scope">作用域（记录字段数据源）</param>
        /// <param name="removed">实际删除条数</param>
        /// <param name="removedMessages">被删前文消息（原序）</param>
        /// <param name="infoJson">info 快照 JSON（空=不写该行）</param>
        /// <param name="nowMs">回收时刻——Unix 毫秒</param>
        /// <param name="seconds">存活时长（秒）</param>
        /// <returns>true=已落盘</returns>
        private bool WriteTimebackArchiveFile(TimebackScope scope, int removed, int keptUsers, List<LlmMessage> removedMessages, string infoJson, long nowMs, long seconds)
        {
            TimebackArchive archive = ResolveTimebackArchive();
            if (archive == null)
            {
                return false;
            }
            TimebackScopeRecord record = new TimebackScopeRecord();
            record.Id = scope.Id;
            record.CatKey = _catKey;
            record.Type = scope.Type;
            record.Purpose = scope.Purpose;
            record.Anchor = scope.StartDeclIndex;
            record.StartAt = scope.StartAtMs;
            record.BackAt = nowMs;
            record.Seconds = seconds;
            record.N = removed;
            record.KeptUsers = keptUsers;
            record.Tokens = ContextTokensKnown;
            record.Grew = ContextTokensKnown - scope.TokensAtOpen;
            record.Released = scope.PendingReleased;
            record.Findings = scope.PendingFindings;
            record.Writes = scope.WriteLog;
            string path = "";
            bool ok = archive.WriteScopeFile(record, infoJson, removedMessages.ToArray(), out path);
            if (ok)
            {
                LogStore.Add("CatHome4", 1, "timeback #" + scope.Id.ToString() + " 归档落档: " + path, "TIMEBACK");
            }
            return ok;
        }
        /// <summary>采集本猫 info 快照——归档文件第二行的现场记录。采集前临时设本会话猫上下文（provider 经 ToolCatContext 解析猫级 roots / 端点 / llm）；批后段不在工具执行上下文内，缺此步 info 即空心。provider 未接线 / 采集失败 = 空串，不阻断回收。</summary>
        /// <returns>info JSON（单行；不可用=空串）</returns>
        private string CollectTimebackInfo()
        {
            if (TimebackInfoProvider == null)
            {
                return "";
            }
            // [段1] 猫上下文对齐——归档采集发生在批后段（工具执行上下文之外），provider 读 ToolCatContext 会读到空猫；与 ExecuteInfo 同模式，临时设本会话猫 key、采集后恢复（修复归档第二行空心）
            string prev = ToolCatContext.CurrentCatKey;
            ToolCatContext.SetCat(_catKey);
            try
            {
                string info = TimebackInfoProvider();
                if (info == null)
                {
                    return "";
                }
                return info;
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "timeback info 快照采集失败（归档缺该行）: " + ex.Message, "TIMEBACK");
                return "";
            }
            finally
            {
                ToolCatContext.SetCat(prev);
            }
        }
        /// <summary>状态自述步长——作用域内每累计 25 个事件注入一条 user 系统提示（莎 2026-09-30 定：10 → 25）。</summary>
        private const long TimebackNoticeStep = 25;
        /// <summary>
        /// 作用域内事件计数——think / 工具完成 / assistant 产出各计 1（莎 2026-09-28 定）。
        /// 只累加，不触碰前文——注入时机归批后段 FlushTimebackNotice（批中注入会打乱 tool_call 配对）。
        /// </summary>
        private void NoteTimebackEvent()
        {
            TimebackScope scope = _timebackScope;
            if (scope == null)
            {
                return;
            }
            scope.EventCount = scope.EventCount + 1;
        }
        /// <summary>
        /// 台账回执内联上限——超出后回执只列前 N 条 + 归档指针，全文进 jsonl（不静默截断）。
        /// </summary>
        private const int TimebackWriteLogInlineLimit = 20;
        /// <summary>
        /// 作用域内工具台账——工具结果回填时登记（**全量**：域内用过的每个工具都记）。
        /// 口径（莎 2026-10-09 定）：谨慎优先——记全所有使用过的工具（含只读 / 被拒），不做语义收窄；
        /// 唯二例外是域机制自用的围栏本身（`timeback-start` / `timeback-back`——记它等于每条都带噪声）。
        /// 只记事实，不做语义判断。
        /// </summary>
        /// <param name="name">工具名</param>
        /// <param name="argsJson">工具参数 JSON</param>
        /// <param name="result">工具结果文本</param>
        private void NoteTimebackWrite(string name, string argsJson, string result)
        {
            TimebackScope scope = _timebackScope;
            if (scope == null)
            {
                return;
            }
            // 域机制自用两件不入账——台账记「域内工作用过的工具」，围栏本身（开锚 / 回收）不算
            if (name == "timeback-start" || name == "timeback-back")
            {
                return;
            }
            scope.WriteLog.Add(name + " · " + TimebackWriteTarget(argsJson) + " · " + TimebackToolStatus(result));
        }
        /// <summary>台账状态列——OK（成功）· DENY（被权限 / 域门禁拒）· ERR（工具自身执行失败）· FAIL（工具跑通但业务未过——编译 / 跑测未通过）；拒 / 败 / 业务未过分列，读面可区分。</summary>
        /// <param name="result">工具结果文本</param>
        /// <returns>状态列取值（OK / DENY / ERR / FAIL / FAIL(明细) / UNPARSED）</returns>
        internal static string TimebackToolStatus(string result)
        {
            if (result == null || result.Length == 0)
            {
                return "OK";
            }
            if (result.StartsWith("ERR|TIMEBACK_PROFILE", StringComparison.Ordinal)
                || result.StartsWith("ERR|TIMEBACK_LOCKED", StringComparison.Ordinal)
                || result.StartsWith("ERR|TOOL_FORBIDDEN", StringComparison.Ordinal))
            {
                return "DENY";
            }
            if (result.StartsWith("ERR|", StringComparison.Ordinal) || result.StartsWith("ROLLED_BACK", StringComparison.Ordinal))
            {
                return "ERR";
            }
            if (result.StartsWith("{", StringComparison.Ordinal))
            {
                return JsonHeadStatus(result);
            }
            return "OK";
        }
        /// <summary>台账状态列·JSON 头分支——cs-build / cs-test / cs-check 等桥工具以首行 `{"ok":…}` 回报业务成败，不以 `ERR|` 开头；
        /// 只认前缀会把编译 / 跑测失败记成 OK（事实列说谎）。ok 缺失 / 解析失败出声 `UNPARSED`——不回落 OK（静默族）。</summary>
        /// <param name="result">工具结果文本（首行为结构化头）</param>
        /// <returns>状态列取值（OK / FAIL / FAIL(明细) / UNPARSED）</returns>
        private static string JsonHeadStatus(string result)
        {
            string head = result;
            int lineEnd = result.IndexOf('\n');
            if (lineEnd >= 0)
            {
                head = result.Substring(0, lineEnd);
            }
            JsonDocument doc = JsonUtil.Parse(head.Trim(), "timeback.toolstatus");
            if (doc == null)
            {
                return "UNPARSED";
            }
            JsonElement root = doc.RootElement;
            if (root.ValueKind != JsonValueKind.Object)
            {
                return "UNPARSED";
            }
            JsonElement ok;
            if (root.TryGetProperty("ok", out ok) == false)
            {
                return "UNPARSED";
            }
            if (ok.ValueKind == JsonValueKind.True)
            {
                return "OK";
            }
            if (ok.ValueKind != JsonValueKind.False)
            {
                return "UNPARSED";
            }
            string detail = JsonFailDetail(root);
            if (detail.Length == 0)
            {
                return "FAIL";
            }
            return "FAIL(" + detail + ")";
        }
        /// <summary>失败明细——从结构化头取可读计数（exit / errors / failedTests / failed），全缺返回空串。</summary>
        /// <param name="root">结构化头根元素</param>
        /// <returns>以逗号分隔的计数串</returns>
        private static string JsonFailDetail(JsonElement root)
        {
            string detail = "";
            int exit = JsonHeadInt(root, "exit");
            int errors = JsonHeadInt(root, "errors");
            int failed = JsonHeadInt(root, "failedTests");
            if (failed <= 0)
            {
                failed = JsonHeadInt(root, "failed");
            }
            if (exit != 0)
            {
                detail = detail + "exit=" + exit.ToString();
            }
            if (errors > 0)
            {
                detail = AppendDetail(detail, "errors=" + errors.ToString());
            }
            if (failed > 0)
            {
                detail = AppendDetail(detail, "failed=" + failed.ToString());
            }
            return detail;
        }
        /// <summary>计数取值——缺字段 / 非数字一律 0（计数只作展示，缺失不构成失败判定）。</summary>
        /// <param name="root">结构化头根元素</param>
        /// <param name="key">键名</param>
        /// <returns>整数值（取不到 0）</returns>
        private static int JsonHeadInt(JsonElement root, string key)
        {
            JsonElement value;
            if (root.TryGetProperty(key, out value) == false)
            {
                return 0;
            }
            int number;
            if (value.ValueKind != JsonValueKind.Number || value.TryGetInt32(out number) == false)
            {
                return 0;
            }
            return number;
        }
        /// <summary>明细追加——已有时以逗号连接。</summary>
        /// <param name="detail">已有明细</param>
        /// <param name="item">新增项</param>
        /// <returns>拼接后的明细</returns>
        private static string AppendDetail(string detail, string item)
        {
            if (detail.Length == 0)
            {
                return item;
            }
            return detail + "," + item;
        }
        /// <summary>台账目标标识——从参数约定键取首个非空值（路径 / 目录类取文件名，标识类原样）；powershell 取命令行原文（外部通道 · 单行指令一次一条，不截断）。</summary>
        /// <param name="argsJson">工具参数 JSON</param>
        /// <returns>目标标识（取不到写「—」）</returns>
        internal static string TimebackWriteTarget(string argsJson)
        {
            if (argsJson == null || argsJson.Length == 0)
            {
                return "—";
            }
            JsonDocument doc = JsonUtil.Parse(argsJson, "timeback.write");
            if (doc == null)
            {
                return "—";
            }
            try
            {
                string[] keys = new string[] { "path", "dest", "src", "file", "proj", "dir", "class", "member" };
                for (int i = 0; i < keys.Length; i = i + 1)
                {
                    string value = JsonUtil.GetStr(doc.RootElement, keys[i], "");
                    if (value.Length > 0)
                    {
                        return ShortenTimebackTarget(value);
                    }
                }
                // powershell——命令行本身即标识（外部通道 · 单行指令一次一条），取原文不截断（莎 2026-10-02 定）
                string cmd = JsonUtil.GetStr(doc.RootElement, "command", "");
                if (cmd.Length > 0)
                {
                    return cmd;
                }
            }
            finally
            {
                doc.Dispose();
            }
            return "—";
        }
        /// <summary>
        /// 目标标识截短——取路径最后一段；超 48 字符截断。
        /// </summary>
        /// <param name="value">原始值</param>
        /// <returns>短标识</returns>
        private static string ShortenTimebackTarget(string value)
        {
            string text = value;
            int cut = text.LastIndexOf('/');
            int cut2 = text.LastIndexOf('\\');
            if (cut2 > cut)
            {
                cut = cut2;
            }
            if (cut >= 0 && cut + 1 < text.Length)
            {
                text = text.Substring(cut + 1);
            }
            if (text.Length > 48)
            {
                text = text.Substring(0, 48);
            }
            return text;
        }
        /// <summary>
        /// 本域工具台账文本——附于 findings 之前（头 = 宿主事实，体 = LLM 自述）。
        /// 超上限给「计数 + 首 N 条 + 归档指针」，全文进归档 jsonl——不静默截断。
        /// </summary>
        /// <returns>台账段（无写操作返回空串）</returns>
        private string BuildTimebackWrites()
        {
            TimebackScope scope = _timebackScope;
            if (scope == null || scope.WriteLog.Count == 0)
            {
                return "";
            }
            string text = "[本域工具台账 · 宿主记录 · " + scope.WriteLog.Count.ToString() + " 条 · 含只读与被拒]\n";
            int shown = scope.WriteLog.Count;
            if (shown > TimebackWriteLogInlineLimit)
            {
                shown = TimebackWriteLogInlineLimit;
            }
            for (int i = 0; i < shown; i = i + 1)
            {
                text = text + (i + 1).ToString() + ". " + scope.WriteLog[i] + "\n";
            }
            if (shown < scope.WriteLog.Count)
            {
                text = text + "…（余 " + (scope.WriteLog.Count - shown).ToString() + " 条见归档 · timeback #" + scope.Id.ToString() + " · data:runtime/timeback）\n";
            }
            return text + "\n";
        }
        /// <summary>状态提示注入——批后段调用：距上次提示累计满 25 个事件则追加一条角色 user 的系统提示（source=systemauto）。
        /// C1 纪律：只追加新块、绝不回改历史块（前缀一字未动 → 缓存仍命中）。
        /// 🔴 角色是 user 不是 assistant（判例 2026-09-28 · 1.03.049 运行态）：思考模式下注入的 assistant 无 reasoning_content 回传 → 端点 400（`The reasoning_content in the thinking mode must be passed back to the API`）→ 本轮中止；user 注入走既有系统通道，零协议风险。
        /// 提示块落在作用域区间内——回收时与查证过程一并删除（零残留）；本轮照常续跑，不置工具主动 done。</summary>
        private void FlushTimebackNotice()
        {
            TimebackScope scope = _timebackScope;
            if (scope == null)
            {
                return;
            }
            if (scope.EventCount - scope.LastNotifyCount < TimebackNoticeStep)
            {
                return;
            }
            long nowMs = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
            long seconds = (nowMs - scope.StartAtMs) / 1000;
            if (seconds < 0)
            {
                seconds = 0;
            }
            scope.LastNotifyCount = scope.EventCount;
            string text = "（系统自动 · timeback #" + scope.Id.ToString() + "）你处在 timeback 中，已经历【" + scope.EventCount.ToString()
                + "】条前文条目（已用 " + seconds.ToString() + " 秒）";
            string posted = PostSystemMessage(SysKindSystemAuto, text);
            if (posted.StartsWith("ERR|", StringComparison.Ordinal))
            {
                LogStore.Add("CatHome4", 2, "timeback 状态提示未受理: " + posted, "TIMEBACK");
                return;
            }
            LogStore.Add("CatHome4", 1, "timeback #" + scope.Id.ToString() + " 状态提示注入（累计 " + scope.EventCount.ToString()
                + " 事件 / 已用 " + seconds.ToString() + " 秒）", "TIMEBACK");
        }
        /// <summary>
        /// 活跃作用域快照——info timeback.active 数据源（无作用域 = null）。
        /// </summary>
        /// <returns>字段字典（id / purpose / anchor / startAt / events；无作用域 = null）</returns>
        public Dictionary<string, object> TimebackActiveSnapshot()
        {
            TimebackScope scope = _timebackScope;
            if (scope == null)
            {
                return null;
            }
            Dictionary<string, object> map = new Dictionary<string, object>();
            map["id"] = scope.Id;
            map["type"] = scope.Type;
            map["purpose"] = scope.Purpose;
            map["anchor"] = scope.StartDeclIndex;
            map["startAt"] = scope.StartAtMs;
            map["events"] = scope.EventCount;
            return map;
        }

        /// <summary>timeback 暴毙风险黑名单（莎 2026-09-28 定 · 2026-10-01 宽松化 · 2026-10-02 放行 mau-setup）——只隔离「会让 Mau 框架 / 宿主进程自身当场失效」的行为，判据 = 该动作是否可能立刻中断进程或让作用域上下文失效（重启 / 程序集句柄替换）。拦两类：restart-*（重启族——本轮中断，作用域随内存丢失）· host-reload（换程序集句柄 + 重建工具池，无后悔药）。其余一律放行——只读（mau-verify / host-flows / config-get / cat.list 类指令）、写仓库产物区（mau-gen / mau-proj）、一键链（prepare / sync-html——只写仓库与静态资源，不换程序集）、配置写（config-*）、管理指令（majordomo-cmd）。判据 = 工具名精确匹配（黑名单式，不用前缀通配）。与 C3 的 Note / sleep / timer 锁定并列：前者防「区间删除语义冲突」，本项防「作用域内把本体搞崩」。🔴 本判据是工具边界要求（机制层），不是使用授权——放行不等于该在域内用，开域时机归规范层（环节判据，见工具描述与设计 §三）。</summary>
        /// <param name="name">工具名</param>
        /// <returns>true=作用域内禁用</returns>
        private static bool IsTimebackBodyLocked(string name)
        {
            return IsRestartTool(name)
                || name == "host-reload";
        }

        /// <summary>
        /// timeback 域限定工具判定（A110）——仅作用域内可用（域外 ERR|TIMEBACK_REQUIRED）：
        /// image-inject（图片不必常驻主干）· browser-*（网页内容属非主干信息——用完回收）。
        /// 与本体修正黑名单方向相反：黑名单拦「域内调用」，本项拦「域外调用」。
        /// </summary>
        /// <param name="name">工具名</param>
        /// <returns>true=需要活跃作用域</returns>
        private static bool IsTimebackScopedTool(string name)
        {
            if (name == "image-inject")
            {
                return true;
            }
            // 例外（A1）——browser-headful 是主干登录工具（域外专属），与其余 browser-* 方向相反
            if (name == "browser-headful")
            {
                return false;
            }
            return name.StartsWith("browser-", StringComparison.Ordinal);
        }

        /// <summary>
        /// 域类型白名单豁免——不归白名单管辖的工具（交各自机制报错）：
        /// timeback 两件（域机制自用）· Note / sleep / timer（C3 锁定——报 `TIMEBACK_LOCKED` 语义比 `TIMEBACK_PROFILE` 准）。
        /// </summary>
        /// <param name="name">工具名</param>
        /// <returns>true=不参与域白名单判定</returns>
        private static bool IsTimebackProfileExempt(string name)
        {
            if (name == "timeback-start" || name == "timeback-back")
            {
                return true;
            }
            return name == "Note" || name == "sleep" || name == "timer";
        }

        /// <summary>
        /// 域规范卡注入——批后段调用（`ApplyTimebackBack` 之后 · design-ch4-timeback-type §十一）。
        /// 作用域活跃且未注入过 → 经系统注入口入队一条 systemauto user 卡。
        /// 卡的落点天然在区间 `[start 结果之后 .. back 声明之前]` 内 → 闭合时随区间一并删除（零新增回收逻辑）。
        /// 零动作：无作用域（同批开收——`ApplyTimebackBack` 已关域）· 已注入 · back 已 pending（区间为空，注入即残留在区间外）。
        /// 不计入作用域事件数——机制消息，非域内工作产出（25 事件状态自述的口径不受其影响）。
        /// </summary>
        internal void PostTimebackCard()
        {
            TimebackScope scope = _timebackScope;
            if (scope == null || scope.CardPosted || scope.PendingFindings != null)
            {
                return;
            }
            scope.CardPosted = true;
            string text = TimebackCard.Card(scope.Type, scope.Purpose, scope.Id, scope.StartDeclIndex);
            string posted = PostSystemMessage(SysKindSystemAuto, text);
            if (posted.StartsWith("ERR|", StringComparison.Ordinal))
            {
                LogStore.Add("CatHome4", 2, "timeback #" + scope.Id.ToString() + " 域规范卡未受理: " + posted, "TIMEBACK");
                return;
            }
            LogStore.Add("CatHome4", 1, "timeback #" + scope.Id.ToString() + " 域规范卡注入（" + scope.Type + "）", "TIMEBACK");
        }

        /// <summary>back 声明思考占位句——回收时替换原思考内容（莎 2026-10-09 定；原文留视图层与归档）。</summary>
        private const string TimebackBackReasoningPlaceholder = "原思考内容已因锚回收被清除";

        /// <summary>
        /// back 声明思考净化——把该条 assistant 消息的 ReasoningContent 换成占位句（其余字段原样）。
        /// 判据（莎 2026-10-09 定）：back 声明是回收动作本身，其思考是对域内工作的「断言」而非工具事实——
        /// 回收即净化，防止后续轮次把它当事实引用（判例：timeback 域内空转致 findings 幻觉 · ReasoningContent 为真污染载体）；
        /// start 声明的思考是「工具需求生成过程」，属正常推理，保留。原文留视图层与归档，不做全局改写。
        /// </summary>
        /// <param name="m">前文消息（非 assistant 或思考为空时原样返回）</param>
        /// <returns>净化后的消息副本（struct 值语义——调用方须换入数组）</returns>
        private static LlmMessage WithTimebackBackReasoningCleared(LlmMessage m)
        {
            if (m.Role != LlmRole.Assistant || m.ReasoningContent == null || m.ReasoningContent.Length == 0)
            {
                return m;
            }
            m.ReasoningContent = TimebackBackReasoningPlaceholder;
            return m;
        }
    }
}
