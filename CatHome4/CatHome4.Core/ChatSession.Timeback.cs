using System;
using System.Collections.Generic;
using System.Text.Json;
using System.Threading.Tasks;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// 宿主会话实体——timeback 上下文作用域分部（design-ch4-timeback §2 / §三 · 2026-09-28 语义重设 · 2026-10-09 拆两名 · 2026-11-12 A331 整段回收）。
    /// 工具面：timeback-start（开锚）· timeback-back（回卷回收）——原单工具 timeback（action=start|back）拆分，功能不变。
    /// 语义（莎 2026-11-12 定）：**闭合 = 整段收拢**——`[start 声明][start 结果][域内过程…][back 声明][back 结果]` 全部离开前文，
    ///   原位只留**一条** sys 摘要（`SysKindTimeback`：实测条数 / token / 工具次数 + findings + 回读规范）。
    ///   于是前文里「同时只存在一个 start 卡 + 一个未闭合的域」：域开则整段在，域闭则整段缩成一行交代。
    /// 由此收益：会话内不留同形重复（域越用越干净）；整段移出后序列天然合法（互配的声明与结果一起走），本轮照常续跑。
    /// 用途（2026-10-03 莎裁）：仅两个域限定工具解锁——主干识图（image-inject）· 浏览网页（browser-*）；域内不做工作、不写计划。
    /// findings 口径（A205 · 2026-10-06 莎定）：**只写「成果在哪」，不写「结论是什么」**——域内不下判断，主干按位置回读、以回读到的真实内容为准（治域内复述致幻觉）。
    /// 思考净化（2026-10-09 立 / A331 退役）：原机制把 back 声明的思考换占位句；整段回收后该声明本身已离开前文，净化无对象——原文留视图层历史与归档。
    /// 锁定：作用域存活期间 Note / sleep / timer 不可用（ERR|TIMEBACK_LOCKED——与区间删除语义冲突）。
    /// 视图层（A331）：**原样保留**（A165 v2「持久即持久」不松绑）——摘要以 `systemautomessage` 条目额外插入，
    ///   走单块追加帧即时上屏，**不做全量重绘**；区间内的 timeback 卡与过程卡照旧在对话区，前端 `tbScopeMark` 按 start / back 行着色。
    /// 归档（A104）：全局计数（Data/runtime/timeback/count.json）+ 每次回收一个作用域文件（&amp;lt;编号&amp;gt;-&amp;lt;时间戳&amp;gt;.jsonl）——整段全文在内。
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

            /// <summary>本次回收的整段条数预算——back 时刻可算（`[start 声明 .. back 结果]` = BackDeclIndex − StartDeclIndex + 2）；随返回值送出，并与批后实际删除数对账。</summary>
            public int PendingCollapsed;

            /// <summary>back 结果消息索引——批后段结果回填时刻记录（A331：回收区间上界 = 本条，含 back 结果本身）。</summary>
            public int BackResultIndex = -1;
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
            // 整段回收条数预算——区间上下界此刻已定（[start 声明 .. back 结果]，back 结果紧随声明 = +1 条）；
            // 该值随返回值送出，并与批后实际删除数对账（归档记实际）
            _timebackScope.PendingCollapsed = CountTimebackCollapsed(_timebackScope);
            long tokensNow = ContextTokensKnown;
            Dictionary<string, object> fields = new Dictionary<string, object>();
            fields["anchor"] = _timebackScope.StartDeclIndex;
            fields["type"] = _timebackScope.Type;
            fields["purpose"] = _timebackScope.Purpose;
            fields["tokens"] = tokensNow;
            fields["grew"] = tokensNow - _timebackScope.TokensAtOpen;
            fields["tools"] = _timebackScope.WriteLog.Count;
            // 情报行——整段回收口径（A331）：闭合后前文不留任何域痕迹，只留一条 sys 摘要
            string infoLine = "📉 整段回收 " + _timebackScope.PendingCollapsed.ToString() + " 条前文（含 start / back 两张卡）· 域内节约 "
                + (tokensNow - _timebackScope.TokensAtOpen).ToString() + "token · 工具使用 "
                + _timebackScope.WriteLog.Count.ToString() + " 次";
            if (_timebackScope.WriteLog.Count > 0)
            {
                infoLine = infoLine + "（详见下表）";
            }
            // 台账附于 findings 之前——头 = 宿主事实（从执行流水提取，不可编），体 = LLM 自述；主干据此抽样核对
            return ToolMetaHead.With("timeback-back", true, _timebackScope.Id.ToString(), _timebackScope.PendingCollapsed, fields, infoLine + "\n" + BuildTimebackWrites() + findings + Environment.NewLine + Environment.NewLine + TimebackCard.Readback(_timebackScope.Type));
        }

        /// <summary>
        /// 整段回收条数预算——`[start 声明 .. back 结果]` 的条数（A331 整段口径）。
        /// back 时刻 back 结果尚未回填，按结构推定：`BackDeclIndex − StartDeclIndex + 2`
        /// （start 声明 + 中间过程 + back 声明 + back 结果各占 1 条，故 +2 起算）。
        /// 批后按实际删除数与它对账（不一致记 L2——结构推定失败的可见信号）。
        /// </summary>
        /// <param name="scope">作用域</param>
        /// <returns>整段条数（索引异常 = 0）</returns>
        private int CountTimebackCollapsed(TimebackScope scope)
        {
            if (scope.BackDeclIndex < scope.StartDeclIndex)
            {
                return 0;
            }
            return scope.BackDeclIndex - scope.StartDeclIndex + 2;
        }

        /// <summary>
        /// 批后回收执行——工具批结果全部回填后调用（design §12.2；A331 整段回收 · 2026-11-12 莎裁）：
        /// ① 前文整段移出 `[start 声明 .. back 结果]`（两张工具卡 + 域内全部过程），原位插入**一条** sys 摘要
        ///    （`SysKindTimeback` · 宿主实测数字 + findings + 回读规范）——它是留在前文的唯一域痕迹
        /// ② 视图层原样保留——摘要以 systemautomessage 条目额外插入（单块追加帧，不做全量重绘）
        /// ③ 归档一次成档（A104：首行 meta + 该猫 info 快照 + 被删前文消息）→ 关闭作用域。本轮照常续跑（不置工具主动 done）。
        /// 区间索引异常（越界 / 空）时不删、不注入，仅出声；作用域照常关闭（不留在活跃态）。
        /// </summary>
        private void ApplyTimebackBack()
        {
            TimebackScope scope = _timebackScope;
            if (scope == null || scope.PendingFindings == null)
            {
                return;
            }
            LlmMessage[] all = _context.GetMessages();
            // [段0] 区间定界——下界 = start 声明；上界 = back 结果（未记录时退到 back 声明，仍含其本身）
            int from = scope.StartDeclIndex;
            int to = scope.BackDeclIndex;
            if (scope.BackResultIndex >= scope.BackDeclIndex)
            {
                to = scope.BackResultIndex;
            }
            if (from < 0)
            {
                from = 0;
            }
            if (to >= all.Length)
            {
                to = all.Length - 1;
            }
            // [段0b] 同批 tail 吞并——back 与其它工具同批时，那些声明与 back 同属一条 assistant 消息；
            // 只删到 back 结果会把它们的结果留在前文 → 孤立 tool 结果（无对应 tool_calls，API 400）。
            // 批与批之间必有 assistant 消息，故向后连续的 tool 消息必属同批，一并纳入区间
            int tailExtra = 0;
            while (to + 1 < all.Length && all[to + 1].Role == LlmRole.Tool)
            {
                to = to + 1;
                tailExtra = tailExtra + 1;
            }
            // 现场采集——info 快照取删除之前（归档第二行记录回收动作发生时的状态）
            string infoJson = CollectTimebackInfo();
            long nowMs = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
            long seconds = (nowMs - scope.StartAtMs) / 1000;
            if (seconds < 0)
            {
                seconds = 0;
            }
            int removed = 0;
            List<LlmMessage> removedMessages = new List<LlmMessage>();
            string summaryText = "";
            bool collapsed = false;
            // [段1] 前文整段回收——[0..from-1] + [sys 摘要] + [to+1..尾]（区间含 start / back 两张工具卡）
            if (from <= to && from < all.Length)
            {
                for (int i = from; i <= to; i = i + 1)
                {
                    removedMessages.Add(all[i]);
                }
                removed = removedMessages.Count;
                summaryText = BuildSysInjectText(TimebackCard.Summary(
                    scope.Type, scope.Purpose, scope.Id, removed, ContextTokensKnown - scope.TokensAtOpen,
                    scope.WriteLog.Count, seconds, scope.PendingFindings));
                List<LlmMessage> next = new List<LlmMessage>();
                for (int i = 0; i < from; i = i + 1)
                {
                    next.Add(all[i]);
                }
                // 摘要就地占位——角色 user（sys 路径同形）。与相邻 user 并列合法：同注入点多条一并入前文是既有常态
                // （FlushPendingIntoRound 逐条 AppendInjection），不额外合并——合并会让视图层净减量与前文对不上
                next.Add(CreateTimebackSummaryMessage(summaryText));
                for (int i = to + 1; i < all.Length; i = i + 1)
                {
                    next.Add(all[i]);
                }
                // [段2] 前文落盘——区间回收重写（append-only 的合法例外）
                _context.ReplaceMessages(next.ToArray());
                _store.Rewrite(_context.GetMessages());
                collapsed = true;
            }
            else
            {
                // 索引越界（前文被外部改动 / 空区间）——不删，仅出声
                LogStore.Add("CatHome4", 2, "timeback 回收区间越界（from " + from.ToString() + " / to " + to.ToString() + " / len " + all.Length.ToString() + "）——本次未删", "TIMEBACK");
            }
            // [段3] 视图层——**原样保留**（A165 v2「持久即持久」契约不松绑）；摘要以一条 systemautomessage 条目
            // **额外插入**（非前文派生 · `msgIndex = -1` · 走单块追加帧即时上屏，**不做全量重绘**）。
            // 区间内的 timeback 卡与过程卡照旧留在对话区，前端 tbScopeMark 仍按 start / back 行着色
            if (collapsed)
            {
                _viewStore.AppendSystemBlock(summaryText, SysKindTimeback, nowMs);
            }
            // [段4] 归档——一次回收一个文件（A104）：首行 meta + 该猫 info 快照 + 被删前文消息
            bool archived = WriteTimebackArchiveFile(scope, removed, removedMessages, infoJson, nowMs, seconds);
            // [段5] 条数对账——预算（back 时刻的结构推定）+ 同批 tail 吞并数 vs 批后实际删除数
            // （剔除 tail 后仍不一致 = 区间被外部改动 / 结构推定失效——必须出声）
            if (removed != scope.PendingCollapsed + tailExtra)
            {
                LogStore.Add("CatHome4", 2, "timeback #" + scope.Id.ToString() + " 整段条数对账不一致（预算 " + scope.PendingCollapsed.ToString() + " + 同批 " + tailExtra.ToString() + " / 实际 " + removed.ToString() + "）", "TIMEBACK");
            }
            // [段6] 作用域关闭——活跃期结束（TimebackActive 回 false；Note / sleep / timer 解锁）
            _timebackScope = null;
            LogStore.Add("CatHome4", 1, "timeback #" + scope.Id.ToString() + " 已整段回收 " + removed.ToString() + " 条（" + seconds.ToString() + " 秒 / 锚点 " + scope.StartDeclIndex.ToString() + "）", "TIMEBACK");
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
        /// 构造 timeback 收拢摘要消息——sys 路径的**就地插入**形态（非入队）：全字段初始化
        /// （struct 默认字段为 null，序列化判空会 NRE）+ 当前时刻。
        /// 与 `PostSystemMessage` 的分工：那条走队列在轮边界取尽；本件取代被回收区间的**原位**，时序必须落在域的位置上。
        /// </summary>
        /// <param name="text">摘要正文（已含 [systemauto] 行头前缀）</param>
        /// <returns>user 角色消息</returns>
        private static LlmMessage CreateTimebackSummaryMessage(string text)
        {
            LlmMessage msg = new LlmMessage();
            msg.Role = LlmRole.User;
            msg.Content = text;
            msg.ToolCallId = "";
            msg.ToolName = "";
            msg.ToolCallsJson = "";
            msg.ReasoningContent = "";
            msg.ImagesJson = "";
            msg.CreatedAt = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
            return msg;
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
        private bool WriteTimebackArchiveFile(TimebackScope scope, int removed, List<LlmMessage> removedMessages, string infoJson, long nowMs, long seconds)
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
            record.Tokens = ContextTokensKnown;
            record.Grew = ContextTokensKnown - scope.TokensAtOpen;
            record.Released = scope.PendingCollapsed;
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
    }
}
