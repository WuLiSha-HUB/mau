using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Mau.Runtime;
using CatHome4.Contracts;
using Xunit;

namespace CatHome4.Core.Tests
{
    /// <summary>
    /// timeback 上下文作用域测试（design-ch4-timeback · 2026-09-28 语义重设 · 2026-10-09 拆两名）——
    /// 区间删除（锚点 = timeback-start 调用 / 结论 = timeback-back 返回值）/ 同批空区间 / 三工具锁定 /
    /// 视图不触 / 参数面 / 执行序两态。
    /// 拆分后：原单工具 timeback（action=start|back）→ 两个工具名（功能不变）。
    /// </summary>
    [Collection("GlobalToolState")]
    public class TimebackTests
    {
        /// <summary>
        /// Mock LLM——按队列返回 tool_calls 或纯文本（每次 ChatStream 消费一项）。
        /// </summary>
        private sealed class MockLlm : ILlmRuntime
        {
            /// <summary>tool_calls JSON 队列——空=返回纯文本</summary>
            public Queue<string> ToolCallsQueue = new Queue<string>();

            /// <summary>纯文本回复内容</summary>
            public string ReplyText = "ok";

            /// <summary>ChatStream 调用次数</summary>
            public int CallCount = 0;

            /// <summary>思考增量队列——每次调用取一项作为该轮 reasoning_content（空=不产思考事件）</summary>
            public Queue<string> ReasoningQueue = new Queue<string>();

            /// <summary>
            /// 流式对话——队列有内容则产 tool_calls（含决策流开始帧），否则产纯文本。
            /// </summary>
            /// <param name="messages">消息序列</param>
            /// <param name="tools">工具定义</param>
            /// <param name="userId">用户标识</param>
            /// <param name="ct">取消令牌</param>
            /// <returns>事件流</returns>
            public async IAsyncEnumerable<LlmStreamEvent> ChatStream(LlmMessage[] messages, ToolSpec[] tools, string userId = "", [System.Runtime.CompilerServices.EnumeratorCancellation] CancellationToken ct = default)
            {
                CallCount = CallCount + 1;
                if (ReasoningQueue.Count > 0)
                {
                    string reasoning = ReasoningQueue.Dequeue();
                    if (reasoning.Length > 0)
                    {
                        yield return new LlmStreamEvent(LlmStreamKind.Reasoning, reasoning);
                    }
                }
                if (ToolCallsQueue.Count > 0)
                {
                    string tc = ToolCallsQueue.Dequeue();
                    yield return new LlmStreamEvent(LlmStreamKind.ToolCallsStart, "");
                    yield return new LlmStreamEvent(LlmStreamKind.ToolCalls, tc);
                    await Task.Yield();
                    yield return new LlmStreamEvent(LlmStreamKind.Done, "");
                    yield break;
                }
                yield return new LlmStreamEvent(LlmStreamKind.Text, ReplyText);
                await Task.Yield();
                yield return new LlmStreamEvent(LlmStreamKind.Done, "");
            }
        }

        /// <summary>
        /// 构造测试会话——临时前文文件 + 唯一临时归档路径 + Mock LLM（Note / timeback-* / info 工具面）。
        /// </summary>
        /// <param name="llm">Mock LLM</param>
        /// <returns>会话实体</returns>
        private static CH4.ChatSession CreateSession(MockLlm llm)
        {
            return CreateSession(llm, new string[] { "Note", "timeback-start", "timeback-back", "info", "sleep", "timer" });
        }
        /// <summary>
        /// 构造测试会话（指定工具面）——临时前文文件 + 唯一临时归档路径 + Mock LLM。
        /// </summary>
        /// <param name="llm">Mock LLM</param>
        /// <param name="toolNames">工具名数组（注册表工具面）</param>
        /// <returns>会话实体</returns>
        private static CH4.ChatSession CreateSession(MockLlm llm, string[] toolNames)
        {
            ToolSpec[] tools = new ToolSpec[toolNames.Length];
            for (int i = 0; i < toolNames.Length; i = i + 1)
            {
                tools[i] = new ToolSpec(toolNames[i], toolNames[i], "{}");
            }
            CH4.ToolRegistry.Init(tools, null, null);
            CH4.ChatSession.AuthorizedToolNamesProvider = null;
            string archiveDir = Path.Combine(Path.GetTempPath(), "cat4tb_" + Guid.NewGuid().ToString("N"));
            CH4.ChatSession.TimebackArchiveDirProvider = delegate (string catKey)
            {
                return archiveDir;
            };
            CH4.ChatSession.TimebackInfoProvider = delegate ()
            {
                // 探针——采集点猫上下文（修复前读到空猫 → 归档第二行空心）
                string cat = CH4.ToolCatContext.CurrentCatKey;
                return "快照:" + (cat == null ? "" : cat);
            };
            ChatContext ctx = new ChatContext();
            string tmp = Path.Combine(Path.GetTempPath(), "cat4tb_" + Guid.NewGuid().ToString("N") + ".jsonl");
            SessionStore store = new SessionStore(tmp);
            OA oa = new OA(new ThreadGuard());
            CH4.SessionViewStore viewStore = new CH4.SessionViewStore(Path.Combine(Path.GetTempPath(), "cat4tb_" + Guid.NewGuid().ToString("N") + ".view.jsonl"));
            CH4.ChatSession session = new CH4.ChatSession("tb-session", "tb", ctx, store, llm, oa, tools, delegate (string name, string args) { return "ERR|NO_TOOL|" + name; }, viewStore);
            session.SetCatKey("tb-session");
            return session;
        }

        /// <summary>
        /// 泵会话直到 Idle——上限 500 帧（防死循环）。
        /// </summary>
        /// <param name="session">会话</param>
        private static void PumpUntilIdle(CH4.ChatSession session)
        {
            for (int i = 0; i < 500; i = i + 1)
            {
                session.Pump();
                Thread.Sleep(5);
                if (session.IsIdle && i >= 2)
                {
                    break;
                }
            }
        }

        /// <summary>
        /// 构造单条 tool_calls JSON（OpenAI wire 形态）。
        /// </summary>
        /// <param name="toolName">工具名</param>
        /// <param name="id">调用 id</param>
        /// <param name="argsJson">参数 JSON 字符串</param>
        /// <returns>tool_calls 数组 JSON</returns>
        private static string BuildToolCalls(string toolName, string id, string argsJson)
        {
            return "[{\"id\":\"" + id + "\",\"function\":{\"name\":\"" + toolName + "\",\"arguments\":" + JsonSerializer.Serialize(argsJson) + "}}]";
        }

        /// <summary>
        /// 拼接同批多条 tool_calls JSON（顺序即执行顺序）。
        /// </summary>
        /// <param name="toolNames">工具名数组</param>
        /// <param name="ids">调用 id 数组</param>
        /// <param name="argsJsons">参数 JSON 字符串数组</param>
        /// <returns>tool_calls 数组 JSON</returns>
        private static string BuildToolCallsBatch(string[] toolNames, string[] ids, string[] argsJsons)
        {
            StringBuilder sb = new StringBuilder();
            sb.Append("[");
            for (int i = 0; i < ids.Length; i = i + 1)
            {
                if (i > 0)
                {
                    sb.Append(",");
                }
                sb.Append("{\"id\":\"" + ids[i] + "\",\"function\":{\"name\":\"" + toolNames[i] + "\",\"arguments\":" + JsonSerializer.Serialize(argsJsons[i]) + "}}");
            }
            sb.Append("]");
            return sb.ToString();
        }

        /// <summary>
        /// 取前文第 k 条（0 起）tool 消息内容——工具结果断言用。
        /// </summary>
        /// <param name="session">会话</param>
        /// <param name="k">序号（0 起）</param>
        /// <returns>结果文本（不足=空串）</returns>
        private static string ToolResultText(CH4.ChatSession session, int k)
        {
            LlmMessage[] all = session.Context.GetMessages();
            int seen = 0;
            for (int i = 0; i < all.Length; i = i + 1)
            {
                if (all[i].Role == LlmRole.Tool)
                {
                    if (seen == k)
                    {
                        return all[i].Content ?? "";
                    }
                    seen = seen + 1;
                }
            }
            return "";
        }

        /// <summary>
        /// 前文是否含指定角色与子串的消息。
        /// </summary>
        /// <param name="session">会话</param>
        /// <param name="role">角色</param>
        /// <param name="fragment">子串</param>
        /// <returns>true=命中</returns>
        private static bool HasMessage(CH4.ChatSession session, LlmRole role, string fragment)
        {
            LlmMessage[] all = session.Context.GetMessages();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                if (all[i].Role == role && all[i].Content != null && all[i].Content.Contains(fragment, StringComparison.Ordinal))
                {
                    return true;
                }
            }
            return false;
        }

        /// <summary>
        /// 视图块是否含指定子串（任意块型的 payload）。
        /// </summary>
        /// <param name="session">会话</param>
        /// <param name="fragment">子串</param>
        /// <returns>true=命中</returns>
        private static bool ViewHas(CH4.ChatSession session, string fragment)
        {
            CH4.ViewBlock[] blocks = session.GetViewBlocks();
            for (int i = 0; i < blocks.Length; i = i + 1)
            {
                string payload = blocks[i].Payload == null ? "" : blocks[i].Payload;
                if (payload.Contains(fragment, StringComparison.Ordinal))
                {
                    return true;
                }
            }
            return false;
        }

        /// <summary>取 timeback 收拢摘要正文——前文里唯一含「timeback #」的 user 消息（无则空串）。</summary>
        /// <param name="session">会话</param>
        /// <returns>摘要全文</returns>
        private static string SummaryText(CH4.ChatSession session)
        {
            LlmMessage[] all = session.Context.GetMessages();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                if (all[i].Role != LlmRole.User)
                {
                    continue;
                }
                string content = all[i].Content == null ? "" : all[i].Content;
                if (content.Contains("timeback #", StringComparison.Ordinal))
                {
                    return content;
                }
            }
            return "";
        }

        /// <summary>
        /// 跨轮取证（A331 改口径）——start → 查证（info）→ back：整段收拢后前文只剩 user / sys 摘要 / 结论轮回复。
        /// </summary>
        [Fact]
        public void Back_CollapsesScopeRange_KeepsSummaryOnly()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "t1", "{\"type\":\"text_search\",\"purpose\":\"跨轮取证\"}"));
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("info", "f1", "{}"));
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-back", "t2", "{\"findings\":\"结论：目录 85 个\"}"));
            session.PostUserMessage("开始取证");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle, "msgCount=" + session.Context.GetMessageCount().ToString() + " phase=" + session.Phase.ToString() + " round=" + session.Round.ToString());
            // 前文：[user][sys 摘要][结论轮回复] = 3 条（声明 / 结果 / 域内过程整段移出）
            Assert.Equal(3, session.Context.GetMessageCount());
            LlmMessage[] all = session.Context.GetMessages();
            // 两次调用对亦不保留——域闭合后前文零 timeback 痕迹
            for (int i = 0; i < all.Length; i = i + 1)
            {
                string tcJson = all[i].ToolCallsJson == null ? "" : all[i].ToolCallsJson;
                Assert.DoesNotContain("timeback", tcJson);
            }
            // 结论 = sys 摘要（user 角色）里的 findings
            Assert.True(HasMessage(session, LlmRole.User, "结论：目录 85 个"));
            Assert.True(HasMessage(session, LlmRole.User, "整段回收 7 条前文"));
            // 查证过程（info 调用声明）已删——只看调用声明（台账正文提及工具名不算残留）
            for (int i = 0; i < all.Length; i = i + 1)
            {
                Assert.DoesNotContain("info", all[i].ToolCallsJson == null ? "" : all[i].ToolCallsJson);
            }
            // 序列合法：末尾为结论轮的正式回复
            Assert.Equal(LlmRole.Assistant, all[all.Length - 1].Role);
            Assert.False(session.TimebackActive);
        }

        /// <summary>
        /// back 声明思考净化（莎 2026-10-09 定）——回收后 back 声明的 ReasoningContent 换占位句；
        /// start 声明的思考保留（工具需求生成过程）；视图层保留原文（不动）。
        /// </summary>
        /// <summary>
        /// A331 整段回收——域闭合后前文不留 start / back 声明，也不留域内过程：
        /// `[start 声明][start 结果][域内过程][back 声明][back 结果]` 全部移出，原位只留一条 sys 摘要。
        /// （原「start 思考保留 / back 思考换占位句」的净化口径随整段删除退役——声明本身已不在前文。）
        /// </summary>
        [Fact]
        public void Back_CollapsesWholeScope_NoDeclarationsInContext()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            llm.ReasoningQueue.Enqueue("开域计划：查 A 与 B");
            llm.ReasoningQueue.Enqueue("域内巡检");
            llm.ReasoningQueue.Enqueue("断言：A、B 均已查完并落盘");
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "t1", "{\"type\":\"text_search\",\"purpose\":\"净化验证\"}"));
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("info", "f1", "{}"));
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-back", "t2", "{\"findings\":\"成果：（无）\"}"));
            session.PostUserMessage("净化验证");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle, "phase=" + session.Phase.ToString());
            LlmMessage[] all = session.Context.GetMessages();
            // 前文零域痕迹——start / back 声明与域内过程（含两轮思考）全部移出
            for (int i = 0; i < all.Length; i = i + 1)
            {
                string tcJson = all[i].ToolCallsJson == null ? "" : all[i].ToolCallsJson;
                Assert.DoesNotContain("timeback", tcJson);
                Assert.DoesNotContain("域内巡检", all[i].ReasoningContent == null ? "" : all[i].ReasoningContent);
                Assert.DoesNotContain("开域计划", all[i].ReasoningContent == null ? "" : all[i].ReasoningContent);
            }
            // 原位留一条 sys 摘要——域编号 + 用途 + findings 一处交代
            Assert.True(HasMessage(session, LlmRole.User, "timeback #1 域已闭合"));
            Assert.True(HasMessage(session, LlmRole.User, "成果：（无）"));
        }

        /// <summary>
        /// 骨架按域类型类别分派（批 2 / §十一 起载体迁移）——实现类（code_write）给「变更 + 跑测」（可回调）；
        /// 审查类（text_search）给「成果位置」（可回读）。两类的可核验面不同，骨架不可互换。
        /// 🔴 §十一：骨架**不在 start 回执里**（回执只留结构化头），而在「域规范卡」（systemauto user）——断言面随之迁移。
        /// </summary>
        [Fact]
        public void Start_SkeletonDispatchesByType()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "t1", "{\"type\":\"code_write\",\"purpose\":\"实现类骨架\"}"));
            session.PostUserMessage("实现类骨架");
            PumpUntilIdle(session);
            string writeReceipt = ToolResultText(session, 0);
            // start 回执只留结构化头——骨架 / 枚举一律不在回执里
            Assert.Contains("\"tool\":\"timeback-start\"", writeReceipt);
            Assert.DoesNotContain("变更：", writeReceipt);
            Assert.DoesNotContain("域内可用", writeReceipt);
            // 骨架在域规范卡里（systemauto user）
            Assert.True(HasMessage(session, LlmRole.User, "变更："));
            Assert.True(HasMessage(session, LlmRole.User, "跑测："));
            Assert.True(HasMessage(session, LlmRole.User, "台账"));
            Assert.False(HasMessage(session, LlmRole.User, "成果：<逐条"));

            MockLlm llm2 = new MockLlm();
            CH4.ChatSession session2 = CreateSession(llm2);
            llm2.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "t1", "{\"type\":\"text_search\",\"purpose\":\"审查类骨架\"}"));
            session2.PostUserMessage("审查类骨架");
            PumpUntilIdle(session2);
            Assert.True(HasMessage(session2, LlmRole.User, "成果："));
            Assert.True(HasMessage(session2, LlmRole.User, "按位置回读"));
            Assert.False(HasMessage(session2, LlmRole.User, "跑测："));
        }

        /// <summary>
        /// 域类型参数面——type 缺失 / 非法一律拒开锚（ERR|TIMEBACK_ARGS，附合法枚举；域不建立）。
        /// </summary>
        [Fact]
        public void Start_RejectsUnknownType()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "t1", "{\"purpose\":\"缺类型\"}"));
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "t2", "{\"type\":\"impl\",\"purpose\":\"非法类型\"}"));
            session.PostUserMessage("类型参数面");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle, "phase=" + session.Phase.ToString());
            Assert.Contains("TIMEBACK_ARGS", ToolResultText(session, 0));
            Assert.Contains("TIMEBACK_ARGS", ToolResultText(session, 1));
            Assert.False(session.TimebackActive);
        }

        /// <summary>
        /// 域类型白名单——名单外工具一律拒（ERR|TIMEBACK_PROFILE）；域保持活跃（拒绝不关域）。
        /// 说明：本用例不收域——被拒结果留在前文供断言（收域会随区间删除）。
        /// </summary>
        [Fact]
        public void Scope_ProfileRejectsToolOutsideWhitelist()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm, new string[] { "timeback-start", "timeback-back", "text-grep", "cs-patch", "file-delete" });
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "t1", "{\"type\":\"text_search\",\"purpose\":\"白名单验证\"}"));
            llm.ToolCallsQueue.Enqueue(BuildToolCallsBatch(
                new string[] { "cs-patch", "file-delete" },
                new string[] { "p1", "d1" },
                new string[] { "{}", "{}" }));
            session.PostUserMessage("白名单验证");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle, "phase=" + session.Phase.ToString());
            Assert.Contains("TIMEBACK_PROFILE", ToolResultText(session, 1));
            Assert.Contains("TIMEBACK_PROFILE", ToolResultText(session, 2));
            Assert.True(session.TimebackActive);
        }

        /// <summary>
        /// 域类型白名单——表级形态：五类型均含基础读面（文本读面 + 文件结构面）；系统信息类（info / host-flows）
        /// 不进任何域；审查类不含写面 / 跑测面（cs 与 ts 两栈同判——`ts-patch` / `ts-test` / `ts-format` 均不进）；
        /// 实现类含 cs 全套十三件 + ts 全套九件（含跑测件 `cs-test` / `ts-test`）+ 文本写面 + mau-verify。
        /// 说明：域内名单工具均为 OA 工具——单测无工单消费者（Post 后无回执 → 批次不闭合），
        /// 故放行面在表级验证；会话级「名单外一律拒」由 `Scope_ProfileRejectsToolOutsideWhitelist` 反向覆盖。
        /// </summary>
        [Fact]
        public void Scope_ProfileWhitelistTableShape()
        {
            string[] readBase = new string[] { "text-read", "text-read_lines", "text-read_between", "text-grep", "file-tree", "file-find", "file-version" };
            string[] types = CH4.TimebackProfile.Types();
            Assert.Equal(5, types.Length);
            for (int t = 0; t < types.Length; t = t + 1)
            {
                for (int i = 0; i < readBase.Length; i = i + 1)
                {
                    Assert.True(CH4.TimebackProfile.Allows(types[t], readBase[i]), types[t] + " 缺基础读面: " + readBase[i]);
                }
                Assert.False(CH4.TimebackProfile.Allows(types[t], "info"), types[t] + " 不应含系统信息类 info");
                Assert.False(CH4.TimebackProfile.Allows(types[t], "host-flows"), types[t] + " 不应含系统信息类 host-flows");
            }
            Assert.False(CH4.TimebackProfile.Allows(CH4.TimebackProfile.CodeReview, "cs-patch"));
            Assert.False(CH4.TimebackProfile.Allows(CH4.TimebackProfile.CodeReview, "mau-verify"));
            Assert.False(CH4.TimebackProfile.Allows(CH4.TimebackProfile.CodeReview, "cs-test"));
            Assert.True(CH4.TimebackProfile.Allows(CH4.TimebackProfile.CodeWrite, "cs-patch"));
            Assert.True(CH4.TimebackProfile.Allows(CH4.TimebackProfile.CodeWrite, "cs-build"));
            Assert.True(CH4.TimebackProfile.Allows(CH4.TimebackProfile.CodeWrite, "cs-test"));
            Assert.True(CH4.TimebackProfile.Allows(CH4.TimebackProfile.CodeWrite, "text-replace"));
            Assert.True(CH4.TimebackProfile.Allows(CH4.TimebackProfile.CodeWrite, "mau-verify"));
            Assert.True(CH4.TimebackProfile.Allows(CH4.TimebackProfile.CodeReview, "ts-read"));
            Assert.True(CH4.TimebackProfile.Allows(CH4.TimebackProfile.CodeReview, "ts-check"));
            Assert.True(CH4.TimebackProfile.Allows(CH4.TimebackProfile.CodeReview, "ts-lint"));
            Assert.True(CH4.TimebackProfile.Allows(CH4.TimebackProfile.CodeReview, "ts-info"));
            Assert.False(CH4.TimebackProfile.Allows(CH4.TimebackProfile.CodeReview, "ts-patch"));
            Assert.False(CH4.TimebackProfile.Allows(CH4.TimebackProfile.CodeReview, "ts-test"));
            Assert.False(CH4.TimebackProfile.Allows(CH4.TimebackProfile.CodeReview, "ts-format"));
            Assert.True(CH4.TimebackProfile.Allows(CH4.TimebackProfile.CodeWrite, "ts-patch"));
            Assert.True(CH4.TimebackProfile.Allows(CH4.TimebackProfile.CodeWrite, "ts-test"));
            Assert.True(CH4.TimebackProfile.Allows(CH4.TimebackProfile.CodeWrite, "ts-format"));
            Assert.True(CH4.TimebackProfile.Allows(CH4.TimebackProfile.CodeWrite, "ts-find-ref"));
            Assert.False(CH4.TimebackProfile.Allows(CH4.TimebackProfile.CodeWrite, "file-delete"));
            Assert.Equal(CH4.TimebackProfile.KindReview, CH4.TimebackProfile.Kind(CH4.TimebackProfile.TextSearch));
            Assert.Equal(CH4.TimebackProfile.KindReview, CH4.TimebackProfile.Kind(CH4.TimebackProfile.CodeReview));
            Assert.Equal(CH4.TimebackProfile.KindWrite, CH4.TimebackProfile.Kind(CH4.TimebackProfile.CodeWrite));
        }

        /// <summary>
        /// 台账状态列——JSON 头形态（cs-build / cs-test / cs-check 桥工具）：业务失败记 `FAIL` 而非 `OK`；
        /// 解析不出出声 `UNPARSED`（不回落 OK）；`ERR|` 与域门禁拒仍走 ERR / DENY。
        /// </summary>
        [Fact]
        public void ToolStatus_JsonHeadBusinessFailure()
        {
            Assert.Equal("OK", CH4.ChatSession.TimebackToolStatus("{\"ok\":true,\"tool\":\"cs-build\",\"errors\":0}"));
            Assert.Equal("FAIL(exit=1,errors=3)", CH4.ChatSession.TimebackToolStatus("{\"ok\":false,\"tool\":\"cs-build\",\"exit\":1,\"errors\":3}\n编译失败 · 3 错误"));
            Assert.Equal("FAIL(failed=2)", CH4.ChatSession.TimebackToolStatus("{\"ok\":false,\"failed\":2}"));
            Assert.Equal("FAIL", CH4.ChatSession.TimebackToolStatus("{\"ok\":false}"));
            Assert.Equal("UNPARSED", CH4.ChatSession.TimebackToolStatus("{ 不是 JSON"));
            Assert.Equal("ERR", CH4.ChatSession.TimebackToolStatus("ERR|BUILD_TIMEOUT|构建超时"));
            Assert.Equal("DENY", CH4.ChatSession.TimebackToolStatus("ERR|TIMEBACK_PROFILE|不在名单"));
            Assert.Equal("OK", CH4.ChatSession.TimebackToolStatus("OK|编译通过"));
        }

        /// <summary>
        /// 域类型白名单——活跃域快照带 type（info.timeback.active 与 recent 对齐；轮内实测发现：原缺该字段）。
        /// 说明：本用例不收域，只走内置 start（无 OA 工具 → 单测可闭合）。
        /// </summary>
        [Fact]
        public void Scope_ProfileActiveSnapshotCarriesType()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "t1", "{\"type\":\"text_search\",\"purpose\":\"快照验证\"}"));
            session.PostUserMessage("快照验证");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle, "phase=" + session.Phase.ToString());
            Assert.True(session.TimebackActive);
            Dictionary<string, object> active = session.TimebackActiveSnapshot();
            Assert.NotNull(active);
            Assert.Equal("text_search", active["type"]);
        }

        /// <summary>
        /// 域类型白名单——豁免面不进本表：Note / sleep / timer 交 C3 报 LOCKED，
        /// 语义比 PROFILE 准（`IsTimebackProfileExempt`）。
        /// </summary>
        [Fact]
        public void Scope_ProfileExemptToolsReportC3Lock()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            llm.ToolCallsQueue.Enqueue(BuildToolCallsBatch(
                new string[] { "timeback-start", "Note" },
                new string[] { "t1", "n1" },
                new string[] { "{\"type\":\"code_write\",\"purpose\":\"豁免面验证\"}", "{\"action\":\"set\",\"content\":\"任务A\"}" }));
            session.PostUserMessage("豁免面验证");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle, "phase=" + session.Phase.ToString());
            string noteResult = ToolResultText(session, 1);
            Assert.Contains("TIMEBACK_LOCKED", noteResult);
            Assert.DoesNotContain("TIMEBACK_PROFILE", noteResult);
        }

        /// <summary>
        /// 域类型档案——静态判据：浏览器预热仅 browser_vision；类型合法性判定；写面归属分域。
        /// </summary>
        [Fact]
        public void Profile_TypeJudgements()
        {
            Assert.True(CH4.TimebackProfile.NeedsBrowser("browser_vision"));
            Assert.False(CH4.TimebackProfile.NeedsBrowser("text_search"));
            Assert.False(CH4.TimebackProfile.NeedsBrowser("code_review"));
            Assert.False(CH4.TimebackProfile.NeedsBrowser("code_write"));
            Assert.False(CH4.TimebackProfile.NeedsBrowser("image_vision"));
            Assert.True(CH4.TimebackProfile.IsValid("image_vision"));
            Assert.True(CH4.TimebackProfile.IsValid("code_write"));
            Assert.False(CH4.TimebackProfile.IsValid("impl"));
            Assert.False(CH4.TimebackProfile.IsValid(""));
            Assert.True(CH4.TimebackProfile.Allows("code_write", "cs-patch"));
            Assert.True(CH4.TimebackProfile.Allows("code_write", "text-write"));
            Assert.False(CH4.TimebackProfile.Allows("code_write", "file-delete"));
            Assert.False(CH4.TimebackProfile.Allows("code_write", "powershell"));
            Assert.False(CH4.TimebackProfile.Allows("code_review", "cs-patch"));
            Assert.True(CH4.TimebackProfile.Allows("code_review", "cs-read"));
            Assert.True(CH4.TimebackProfile.Allows("image_vision", "image-inject"));
            Assert.True(CH4.TimebackProfile.Allows("image_vision", "image-analyze"));
            Assert.False(CH4.TimebackProfile.Allows("image_vision", "browser-open"));
            Assert.False(CH4.TimebackProfile.Allows("browser_vision", "image-inject"));
            Assert.False(CH4.TimebackProfile.Allows("text_search", "image-inject"));
            Assert.Equal(CH4.TimebackProfile.KindWrite, CH4.TimebackProfile.Kind("code_write"));
            Assert.Equal(CH4.TimebackProfile.KindImage, CH4.TimebackProfile.Kind("image_vision"));
            Assert.Equal(CH4.TimebackProfile.KindReview, CH4.TimebackProfile.Kind("code_review"));
            Assert.Equal(CH4.TimebackProfile.KindReview, CH4.TimebackProfile.Kind("browser_vision"));
            Assert.Equal(CH4.TimebackProfile.KindReview, CH4.TimebackProfile.Kind("text_search"));
        }

        /// <summary>
        /// 域类型档案——白名单对账：池中缺失的名字必须显形（拼写漂移 / 工具退役漏改的哨兵）。
        /// </summary>
        [Fact]
        public void Profile_UnknownReportsWhitelistDrift()
        {
            string[] pool = new string[] { "text-read", "cs-read", "info", "browser-open", "image-inject", "browser-read", "browser-eval", "browser-shot", "browser-tabs", "image-analyze", "file-version", "file-tree", "file-find", "text-grep", "text-read_lines", "text-read_between" };
            string[] missing = CH4.TimebackProfile.Unknown(pool);
            Assert.Contains("cs-patch", missing);
            Assert.Contains("text-write", missing);
            Assert.DoesNotContain("text-read", missing);
            Assert.DoesNotContain("browser-open", missing);
        }

        /// <summary>
        /// 用途自解释（2026-10-01 · B 档；A331 改载体）——start / back 两张卡整段离开前文后，
        /// 用途由 sys 摘要承载（闭合交代自解释：类型 + 用途 + 用时一行给全）。
        /// </summary>
        [Fact]
        public void Back_MetaCarriesPurpose()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "t1", "{\"type\":\"text_search\",\"purpose\":\"回执用途验证\"}"));
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("info", "f1", "{}"));
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-back", "t2", "{\"findings\":\"结论：验证\\n事实：（无）\\n指针：（无）\"}"));
            session.PostUserMessage("开始取证");
            PumpUntilIdle(session);
            Assert.True(HasMessage(session, LlmRole.User, "用途「回执用途验证」"));
            Assert.True(HasMessage(session, LlmRole.User, "text_search · 用途「回执用途验证」"));
        }

        /// <summary>
        /// 同批 start + back（A331）——整段（声明 + 两个结果）收拢为一条 sys 摘要，作用域照常关闭。
        /// </summary>
        [Fact]
        public void StartAndBackInSameBatch_CollapsesToSummary()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            string batch = BuildToolCallsBatch(
                new string[] { "timeback-start", "timeback-back" },
                new string[] { "t1", "t2" },
                new string[] { "{\"type\":\"text_search\",\"purpose\":\"同批\"}", "{\"findings\":\"结论：同批\"}" });
            llm.ToolCallsQueue.Enqueue(batch);
            session.PostUserMessage("同批开收");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            // user + sys 摘要 + 结论轮回复 = 3 条（声明与两个结果整段移出）
            Assert.Equal(3, session.Context.GetMessageCount());
            Assert.True(HasMessage(session, LlmRole.User, "结论：同批"));
            Assert.False(session.TimebackActive);
        }

        /// <summary>
        /// 同批多调用（A331 改口径）——sibling 工具（info）与 start 同批时同属域内，随整段一并收拢：
        /// 前文不留声明与结果（原「start 结果块必须原样保留」的回归口径随整段回收退役），
        /// 域内做了什么由 sys 摘要的工具次数交代。
        /// </summary>
        [Fact]
        public void StartBatchWithSiblingTool_CollapsesSiblings()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            string batch = BuildToolCallsBatch(
                new string[] { "timeback-start", "info" },
                new string[] { "t1", "r1" },
                new string[] { "{\"type\":\"text_search\",\"purpose\":\"同批多调用\"}", "{}" });
            llm.ToolCallsQueue.Enqueue(batch);
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("info", "r2", "{}"));
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-back", "t2", "{\"findings\":\"结论：多调用\"}"));
            session.PostUserMessage("同批多调用取证");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle, "phase=" + session.Phase.ToString());
            // user + sys 摘要 + 结论轮回复 = 3 条（声明与三份结果整段移出）
            Assert.Equal(3, session.Context.GetMessageCount());
            Assert.True(HasMessage(session, LlmRole.User, "结论：多调用"));
            // 域内两个 info 调用计入台账——摘要情报行给次数（明细在归档）
            Assert.True(HasMessage(session, LlmRole.User, "工具使用 2 次"));
        }

        /// <summary>
        /// 作用域锁定——Note / sleep / timer 在活跃期一律拒绝（ERR|TIMEBACK_LOCKED）。
        /// </summary>
        [Fact]
        public void Scope_Locks_Note_Sleep_Timer()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            string batch = BuildToolCallsBatch(
                new string[] { "timeback-start", "Note", "sleep", "timer" },
                new string[] { "t1", "n1", "s1", "m1" },
                new string[] { "{\"type\":\"text_search\",\"purpose\":\"锁定验证\"}", "{\"action\":\"set\",\"content\":\"任务A\"}", "{\"seconds\":30}", "{\"content\":\"回来看看\",\"seconds\":60}" });
            llm.ToolCallsQueue.Enqueue(batch);
            session.PostUserMessage("锁定验证");
            PumpUntilIdle(session);
            Assert.Contains("TIMEBACK_LOCKED", ToolResultText(session, 1));
            Assert.Contains("TIMEBACK_LOCKED", ToolResultText(session, 2));
            Assert.Contains("TIMEBACK_LOCKED", ToolResultText(session, 3));
            // 作用域仍开着（锁定不影响 start 本身）
            Assert.True(session.TimebackActive);
        }
        /// <summary>暴毙风险黑名单（T2 扩展 · 2026-10-01 宽松化 · 2026-10-02 放行 mau-setup）——作用域存活期只拦「会让进程 / 作用域当场失效」的两件：restart-full / restart-incr / restart-host / host-reload 一律 ERR|TIMEBACK_LOCKED；其余（只读 / 仓库产物 / 一键链 prepare·sync-html / 配置写 / 管理指令）放行——作用域外同工具不受该判定拦截。</summary>
        [Fact]
        public void Scope_Locks_BodyRepairTools()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm, new string[]
            {
                                "timeback-start", "timeback-back", "info", "info", "host-flows", "host-reload", "restart-full"
            });
            // 作用域外——host-reload 不被该判定拦（测试环境直执回落 ERR|NO_TOOL）
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("host-reload", "h0", "{\"cat\":\"TextCat\"}"));
            // 作用域内——start 与「两件拦 + 三件放行」同批。
            // mau-setup 已于 2026-10-02 放行（deploy 从工具面退役后无暴毙风险），但它是 OA 工具——
            // 测试环境无工单消费者会挂起，故不在此批验证；黑名单清单本身由 §C5 规格 + 代码审查锁定。
            string batch = BuildToolCallsBatch(
                new string[] { "timeback-start", "host-reload", "restart-full", "host-flows", "info", "info" },
                new string[] { "t1", "h1", "r1", "f1", "i1", "x1" },
                new string[]
                {
                                    "{\"type\":\"text_search\",\"purpose\":\"黑名单验证\"}",
                                    "{\"cat\":\"TextCat\"}",
                                    "{}",
                                    "{}",
                                    "{}",
                                    "{}"
                });
            llm.ToolCallsQueue.Enqueue(batch);
            session.PostUserMessage("黑名单验证");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle, "phase=" + session.Phase.ToString());
            // 作用域外那一批未被锁定
            Assert.False(ToolResultText(session, 0).Contains("TIMEBACK_LOCKED", StringComparison.Ordinal));
            // 作用域内：start 回执保留（§十一 起只留结构化头）+ 其后两件全拒（暴毙风险面）
            Assert.Contains("\"tool\":\"timeback-start\"", ToolResultText(session, 1));
            Assert.Contains("TIMEBACK_LOCKED", ToolResultText(session, 2));
            Assert.Contains("TIMEBACK_LOCKED", ToolResultText(session, 3));
            // 放行面——只读 / 常驻内置工具不再被锁
            for (int i = 4; i <= 6; i = i + 1)
            {
                Assert.False(ToolResultText(session, i).Contains("TIMEBACK_LOCKED", StringComparison.Ordinal), "第 " + i.ToString() + " 件被误锁");
            }
            Assert.True(session.TimebackActive);
        }
        /// <summary>
        /// 域规范卡带骨架与边界（§十一 · 2026-10-09 起由 start 回执迁入）——审查类四段齐备（成果 / 未竟 / 卡点 / 失败）
        /// + 位置硬约束句；卡另含边界 / 可用 / 不可用 / 建议长度四段。start 回执只留结构化头。
        /// </summary>
        [Fact]
        public void Start_CardCarriesSkeletonAndBoundary()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "s1", "{\"type\":\"text_search\",\"purpose\":\"骨架验证\"}"));
            session.PostUserMessage("骨架验证");
            PumpUntilIdle(session);
            // start 回执——只留结构化头
            string receipt = ToolResultText(session, 0);
            Assert.Contains("\"tool\":\"timeback-start\"", receipt);
            Assert.Contains("\"type\":\"text_search\"", receipt);
            Assert.DoesNotContain("成果：", receipt);
            // 域规范卡——五段齐备
            Assert.True(HasMessage(session, LlmRole.User, "timeback #1 域规范"));
            Assert.True(HasMessage(session, LlmRole.User, "① 本域边界：文本 / 文档检索"));
            Assert.True(HasMessage(session, LlmRole.User, "② 域内可用：text-read"));
            Assert.True(HasMessage(session, LlmRole.User, "③ 域内不可用："));
            Assert.True(HasMessage(session, LlmRole.User, "④ 回执骨架"));
            Assert.True(HasMessage(session, LlmRole.User, "⑤ 建议长度：本域建议"));
            Assert.True(HasMessage(session, LlmRole.User, "成果："));
            Assert.True(HasMessage(session, LlmRole.User, "未竟："));
            Assert.True(HasMessage(session, LlmRole.User, "卡点："));
            Assert.True(HasMessage(session, LlmRole.User, "失败："));
            Assert.True(HasMessage(session, LlmRole.User, "只写「成果在哪」"));
            Assert.True(HasMessage(session, LlmRole.User, "位置必须是这趟真实读到"));
            Assert.False(HasMessage(session, LlmRole.User, "⑥ 浏览器操作"), "text_search 卡不应含浏览器段");
        }

        /// <summary>
        /// 域规范卡——browser_vision 追加第⑥段浏览器操作指引（寻路 / 取值 / 截图落点 / 看图分流 / 扒图 / 边界）；其他 type 不含。
        /// </summary>
        [Fact]
        public void Card_BrowserVisionCarriesBrowserGuide()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "s1", "{\"type\":\"browser_vision\",\"purpose\":\"操作指引验证\"}"));
            session.PostUserMessage("操作指引验证");
            PumpUntilIdle(session);
            Assert.True(HasMessage(session, LlmRole.User, "⑥ 浏览器操作（本域专属）"));
            Assert.True(HasMessage(session, LlmRole.User, "data:browser/<cat>/out/"));
            Assert.True(HasMessage(session, LlmRole.User, "不含识图"));
            Assert.True(HasMessage(session, LlmRole.User, "页签：tabs"));
            Assert.True(HasMessage(session, LlmRole.User, "原图直链常 403"));
            Assert.False(HasMessage(session, LlmRole.User, "⑦ 看图操作"), "browser_vision 卡不应含识图段");
        }

        /// <summary>
        /// 域规范卡——image_vision 追加第⑦段看图操作指引（直读 / OCR 分流 · ASCII 图证规格 · 多图与证据位置）；
        /// 回执骨架为识图类（图证 + 结论），且不带浏览器段（A293）。
        /// </summary>
        [Fact]
        public void Card_ImageVisionCarriesImageGuide()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "i1", "{\"type\":\"image_vision\",\"purpose\":\"识图指引验证\"}"));
            session.PostUserMessage("识图指引验证");
            PumpUntilIdle(session);
            Assert.True(HasMessage(session, LlmRole.User, "① 本域边界：看图取证"));
            Assert.True(HasMessage(session, LlmRole.User, "② 域内可用：image-inject · image-analyze"));
            Assert.True(HasMessage(session, LlmRole.User, "④ 回执骨架"));
            Assert.True(HasMessage(session, LlmRole.User, "图证："));
            Assert.True(HasMessage(session, LlmRole.User, "证据位置："));
            Assert.True(HasMessage(session, LlmRole.User, "⑦ 看图操作（本域专属）"));
            Assert.True(HasMessage(session, LlmRole.User, "ASCII 画判定所需区域"));
            Assert.False(HasMessage(session, LlmRole.User, "⑥ 浏览器操作"), "image_vision 卡不应含浏览器段");
            Assert.False(HasMessage(session, LlmRole.User, "成果："), "识图类骨架不应含审查类成果段");
        }

        /// <summary>
        /// 域规范卡生命周期（§十一）——① 同域只注入一条（幂等）；② 闭合时随区间删除（零残留）；
        /// ③ 同批 start+back 零注入（区间为空，注入会落在删除区间之外 · 永久残留）。
        /// </summary>
        [Fact]
        public void Card_InjectedOnceAndSkippedOnSameBatchClose()
        {
            // ① 单域——开域后卡在，域内多批仍只一条
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "t1", "{\"type\":\"text_search\",\"purpose\":\"卡幂等\"}"));
            session.PostUserMessage("卡幂等");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle, "phase=" + session.Phase.ToString());
            Assert.True(session.TimebackActive);
            Assert.True(HasMessage(session, LlmRole.User, "域规范"));
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("info", "r1", "{}"));
            session.PostUserMessage("域内读一");
            PumpUntilIdle(session);
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("info", "r2", "{}"));
            session.PostUserMessage("域内读二");
            PumpUntilIdle(session);
            Assert.Equal(1, CountCardMessages(session));

            // ② 闭合——卡随区间删除，前文零残留
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-back", "t2", "{\"findings\":\"成果：（无）\"}"));
            session.PostUserMessage("回收");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle, "phase=" + session.Phase.ToString());
            Assert.False(session.TimebackActive);
            Assert.False(HasMessage(session, LlmRole.User, "域规范"));

            // ③ 同批开收——零注入
            MockLlm llm2 = new MockLlm();
            CH4.ChatSession session2 = CreateSession(llm2);
            string batch = BuildToolCallsBatch(
                new string[] { "timeback-start", "timeback-back" },
                new string[] { "t1", "t2" },
                new string[] { "{\"type\":\"text_search\",\"purpose\":\"同批开收\"}", "{\"findings\":\"成果：（无）\"}" });
            llm2.ToolCallsQueue.Enqueue(batch);
            session2.PostUserMessage("同批开收");
            PumpUntilIdle(session2);
            Assert.True(session2.IsIdle, "phase=" + session2.Phase.ToString());
            Assert.False(session2.TimebackActive);
            Assert.False(HasMessage(session2, LlmRole.User, "域规范"));
        }

        /// <summary>
        /// 域规范卡条数——user 角色且正文含卡标识的消息数（§十一 生命周期断言用）。
        /// </summary>
        /// <param name="session">会话</param>
        /// <returns>卡条数</returns>
        private static int CountCardMessages(CH4.ChatSession session)
        {
            int count = 0;
            LlmMessage[] all = session.Context.GetMessages();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                string content = all[i].Content == null ? "" : all[i].Content;
                if (all[i].Role == LlmRole.User && content.Contains("timeback #", StringComparison.Ordinal) && content.Contains("域规范", StringComparison.Ordinal))
                {
                    count = count + 1;
                }
            }
            return count;
        }
        /// <summary>
        /// 工具台账——域内**用过的每个工具**逐条登记（宿主记录；含只读与被拒——莎 2026-10-09 定：谨慎口径，记全），
        /// 唯域机制自用两件（start / back）不入账；back 回执附于 findings 之前。
        /// </summary>
        /// <summary>
        /// 摘要情报行 + 台账归属（A331）——摘要给整段回收口径（条数 / 节约 token / 工具使用数）；
        /// 台账明细（含只读与被拒、DENY 单列）归归档首行 meta：摘要只留次数，明细不挤前文。
        /// </summary>
        [Fact]
        public void Back_SummaryCarriesCounts_ArchiveCarriesTools()
        {
            MockLlm llm = new MockLlm();
            string archiveDir = Path.Combine(Path.GetTempPath(), "cat4tb_writelog_" + Guid.NewGuid().ToString("N"));
            CH4.ChatSession session = CreateSession(llm);
            CH4.ChatSession.TimebackArchiveDirProvider = delegate (string catKey)
            {
                return archiveDir;
            };
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "s1", "{\"type\":\"text_search\",\"purpose\":\"台账验证\"}"));
            session.PostUserMessage("开锚");
            PumpUntilIdle(session);
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("time", "s2", "{}"));
            session.PostUserMessage("读时间");
            PumpUntilIdle(session);
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("host-reload", "s3", "{\"cat\":\"TextCat\"}"));
            session.PostUserMessage("域内热重载");
            PumpUntilIdle(session);
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-back", "s4", "{\"findings\":\"成果：台账验证\\n未竟：（无）\"}"));
            session.PostUserMessage("收域");
            PumpUntilIdle(session);
            // 摘要情报行——整段回收口径（条数 / 节约 token / 工具使用数）
            string summary = SummaryText(session);
            Assert.Contains("📉 整段回收 ", summary);
            Assert.Contains("域内节约 ", summary);
            Assert.Contains("工具使用 2 次", summary);
            // 台账明细归归档——被拒工具记 DENY（拒与败分列）· 只读工具同样入账 · 机制自用两件不入账
            string[] files = Directory.GetFiles(archiveDir, "*.jsonl");
            Assert.Single(files);
            string meta = File.ReadAllLines(files[0])[0];
            Assert.Contains("host-reload", meta);
            Assert.Contains("DENY", meta);
            Assert.Contains("time · ", meta);
            Assert.DoesNotContain("timeback-back · ", meta);
            CH4.ChatSession.TimebackArchiveDirProvider = null;
            Directory.Delete(archiveDir, true);
        }

        /// <summary>
        /// 台账目标标识提取（2026-10-02）——路径类取末段 · 标识类原样 · powershell 取命令行原文（不截断）· 取不到写「—」。
        /// </summary>
        [Fact]
        public void TimebackWriteTarget_ShapesAndPowerShellCommand()
        {
            Assert.Equal("Tree.md", CH4.ChatSession.TimebackWriteTarget(@"{""path"":""ccbp:L1/Tree.md""}"));
            Assert.Equal("Foo", CH4.ChatSession.TimebackWriteTarget(@"{""class"":""Foo""}"));
            string psArg = @"{""command"":""dotnet test CatHome4.sln --nologo""}";
            Assert.Equal("dotnet test CatHome4.sln --nologo", CH4.ChatSession.TimebackWriteTarget(psArg));
            Assert.Equal("—", CH4.ChatSession.TimebackWriteTarget("{}"));
        }

        /// <summary>
        /// 被回收区间的视图块（A331）——视图层**原样保留**：两张 timeback 卡与域内工具卡照旧在对话区，
        /// 摘要以一条 systemautomessage 条目额外插入（单块追加帧，不做全量重绘）。
        /// </summary>
        [Fact]
        public void DeletedRange_ViewKeepsCardsAppendsSummary()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "t1", "{\"type\":\"text_search\",\"purpose\":\"视图转 gap\"}"));
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("info", "f1", "{}"));
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-back", "t2", "{\"findings\":\"结论：视图验证\"}"));
            session.PostUserMessage("开始取证");
            PumpUntilIdle(session);
            // timeback 两张卡仍留在视图层
            Assert.True(ViewHas(session, "timeback-start"));
            Assert.True(ViewHas(session, "timeback-back"));
            // 摘要额外插入——域编号 + findings 在人可见面
            Assert.True(ViewHas(session, "timeback #1 域已闭合"));
            Assert.True(ViewHas(session, "结论：视图验证"));
        }

        /// <summary>
        /// 归档落盘（A104）——一次回收一个文件：首行 meta + 次行 info 快照 + 其后被删前文消息；全局计数落 count.json。
        /// </summary>
        [Fact]
        public void Back_WritesScopeArchiveFile()
        {
            MockLlm llm = new MockLlm();
            string archiveDir = Path.Combine(Path.GetTempPath(), "cat4tb_scope_" + Guid.NewGuid().ToString("N"));
            CH4.ChatSession session = CreateSession(llm);
            CH4.ChatSession.TimebackArchiveDirProvider = delegate (string catKey)
            {
                return archiveDir;
            };
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "t1", "{\"type\":\"text_search\",\"purpose\":\"归档验证\"}"));
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("info", "f1", "{}"));
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-back", "t2", "{\"findings\":\"结论：A\"}"));
            session.PostUserMessage("开始取证");
            PumpUntilIdle(session);
            // [断言1] 全局计数——count.json 落盘且为 1
            string countPath = Path.Combine(archiveDir, "count.json");
            Assert.True(File.Exists(countPath));
            Assert.Contains("\"count\":1", File.ReadAllText(countPath));
            // [断言2] 作用域文件——<编号>-<回收时刻>.jsonl（一次回收一个文件）
            string[] files = Directory.GetFiles(archiveDir, "*.jsonl");
            Assert.Single(files);
            Assert.StartsWith("1-", Path.GetFileName(files[0]));
            string[] lines = File.ReadAllLines(files[0]);
            // [断言3] 首行 meta（记录字段）+ 次行 info 快照 + 其后被删前文消息
            Assert.Contains("\"t\":\"timeback\"", lines[0]);
            Assert.Contains("归档验证", lines[0]);
            Assert.Contains("\"n\":7", lines[0]);
            Assert.Contains("\"findings\":\"结论：A\"", lines[0]);
            Assert.Contains("快照:tb-session", lines[1]);
            // 被删前文 7 条 = start 声明 + start 结果 + 域规范卡（§十一）+ info 声明 + 其结果 + back 声明 + back 结果
            Assert.True(lines.Length >= 9);
            Assert.Contains("\"t\":\"m\"", lines[2]);
            Assert.Contains("\"t\":\"m\"", lines[3]);
            Assert.Contains("\"t\":\"m\"", lines[4]);
            Assert.Contains("\"t\":\"m\"", lines[8]);
            Directory.Delete(archiveDir, true);
        }

        /// <summary>
        /// 参数面——未闭合时再次 start 被拒（ERR|TIMEBACK_NESTED）；无作用域 / 缺载荷的 back 被拒。
        /// </summary>
        [Fact]
        public void Args_RejectedAsNestedOrWithoutScope()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            // 无作用域 back
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-back", "t0", "{\"findings\":\"结论：C\"}"));
            session.PostUserMessage("空 back");
            PumpUntilIdle(session);
            Assert.Contains("TIMEBACK_NO_SCOPE", ToolResultText(session, 0));
            // 批内重复 timeback-start——参数面拒绝（A106：批内各至多 × 1）
            string batch = BuildToolCallsBatch(
                new string[] { "timeback-start", "timeback-start" },
                new string[] { "t1", "t2" },
                new string[] { "{\"type\":\"text_search\",\"purpose\":\"甲\"}", "{\"type\":\"text_search\",\"purpose\":\"乙\"}" });
            llm.ToolCallsQueue.Enqueue(batch);
            session.PostUserMessage("同批连开两次");
            PumpUntilIdle(session);
            Assert.Contains("批内不允许多条", ToolResultText(session, 2));
            Assert.True(session.TimebackActive);
            // 跨批再次 start——嵌套拒绝（ERR|TIMEBACK_NESTED）
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "t4", "{\"type\":\"text_search\",\"purpose\":\"丙\"}"));
            session.PostUserMessage("再开一次");
            PumpUntilIdle(session);
            Assert.Contains("TIMEBACK_NESTED", ToolResultText(session, 3));
            // 缺 findings 的 back
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-back", "t5", "{}"));
            session.PostUserMessage("缺载荷");
            PumpUntilIdle(session);
            Assert.Contains("TIMEBACK_ARGS", ToolResultText(session, 4));
        }

        /// <summary>
        /// 归档编号（A104）——count.json 全局递增：跨实例（等价跨重启）续接；不依赖归档文件内容。
        /// </summary>
        [Fact]
        public void Archive_NumberIncrementsAcrossInstances()
        {
            string dir = Path.Combine(Path.GetTempPath(), "cat4tb_count_" + Guid.NewGuid().ToString("N"));
            CH4.TimebackArchive first = new CH4.TimebackArchive(dir);
            Assert.Equal(1L, first.NextId());
            // 跨实例（等价跨重启）续接——读 count.json，不扫归档文件
            CH4.TimebackArchive second = new CH4.TimebackArchive(dir);
            Assert.Equal(2L, second.NextId());
            Assert.Equal(3L, second.NextId());
            Assert.Contains("\"count\":3", File.ReadAllText(Path.Combine(dir, "count.json")));
            CH4.TimebackArchive third = new CH4.TimebackArchive(dir);
            Assert.Equal(4L, third.NextId());
            Directory.Delete(dir, true);
        }
        /// <summary>
        /// 状态提示（T2）——作用域内累计满 25 个事件（think / 工具完成 / assistant 各计 1）时注入一条 user 系统提示。
        /// 角色 user（source=systemauto）——思考模式下 assistant 注入缺 reasoning 回传会致端点 400（判例 2026-09-28）。
        /// </summary>
        [Fact]
        public void Notice_InjectedAfterTwentyFiveEvents()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "t1", "{\"type\":\"text_search\",\"purpose\":\"计数取证\"}"));
            // 计数从作用域开启后起算：start 批只计其结果 1（该批 assistant 产出于开锚之前）；
            // 其后每批 +2（assistant + 工具完成）——第 12 个 info 批后累计 25，越过 25 阈值触发一次自述
            for (int i = 0; i < 12; i = i + 1)
            {
                llm.ToolCallsQueue.Enqueue(BuildToolCalls("info", "r" + i.ToString(), "{}"));
            }
            session.PostUserMessage("计数取证");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle, "phase=" + session.Phase.ToString());
            Assert.True(session.TimebackActive);
            Assert.True(HasMessage(session, LlmRole.User, "你处在 timeback 中"));
            Assert.True(HasMessage(session, LlmRole.User, "已经历【25】条"));
        }
        /// <summary>
        /// 状态提示归作用域区间——回收时与查证过程一并删除（零残留）。
        /// </summary>
        [Fact]
        public void Notice_RemovedWithScopeOnBack()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "t1", "{\"type\":\"text_search\",\"purpose\":\"计数取证\"}"));
            // 先凑满 25 阈值触发一次状态提示（1 + 2×12），再由 back 批验证提示随区间回收删除
            for (int i = 0; i < 12; i = i + 1)
            {
                llm.ToolCallsQueue.Enqueue(BuildToolCalls("info", "r" + i.ToString(), "{}"));
            }
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-back", "t2", "{\"findings\":\"结论：计数取证\"}"));
            session.PostUserMessage("计数取证");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle, "phase=" + session.Phase.ToString());
            Assert.False(session.TimebackActive);
            // 状态提示 + 域规范卡随前文整段收拢移出；视图层保留两张 timeback 卡（只增不改）
            Assert.False(HasMessage(session, LlmRole.User, "你处在 timeback 中"));
            Assert.False(HasMessage(session, LlmRole.User, "域规范"));
            Assert.True(ViewHas(session, "timeback-start"));
            Assert.True(HasMessage(session, LlmRole.User, "结论：计数取证"));
            LlmMessage[] all = session.Context.GetMessages();
            // 区间内容已删——判据取调用声明（back 回执的工具台账正文提及工具名不算残留）
            for (int i = 0; i < all.Length; i = i + 1)
            {
                string toolCalls = all[i].ToolCallsJson == null ? "" : all[i].ToolCallsJson;
                Assert.DoesNotContain("info", toolCalls);
            }
        }
        /// <summary>
        /// 归档读面（A104）——只读各文件首行 meta、按编号取末尾 N 个；异名文件跳过。
        /// </summary>
        [Fact]
        public void Archive_ReadRecentMetaSkipsBadFiles()
        {
            string dir = Path.Combine(Path.GetTempPath(), "cat4tb_recent_" + Guid.NewGuid().ToString("N"));
            CH4.TimebackArchive archive = new CH4.TimebackArchive(dir);
            CH4.TimebackScopeRecord first = BuildScopeRecord(archive.NextId(), "用途甲", "结论甲", 1000);
            string path1 = "";
            // info 快照含缩进换行——入档须归一为单行（JSONL 每行独立）
            string multilineInfo = "{\n  \"cat\": \"cat\",\n  \"version\": \"1.0\"\n}";
            Assert.True(archive.WriteScopeFile(first, multilineInfo, new LlmMessage[0], out path1));
            Assert.Equal(2, File.ReadAllLines(path1).Length);
            CH4.TimebackScopeRecord second = BuildScopeRecord(archive.NextId(), "用途乙", "结论乙", 2000);
            string path2 = "";
            Assert.True(archive.WriteScopeFile(second, "", new LlmMessage[0], out path2));
            // 异名文件（编号前缀不可解析）——不参与读面
            File.WriteAllText(Path.Combine(dir, "readme.jsonl"), "{\"t\":\"timeback\",\"id\":99}");
            List<string> one = archive.ReadRecentMeta(1);
            Assert.Single(one);
            Assert.Contains("结论乙", one[0]);
            List<string> all = archive.ReadRecentMeta(5);
            Assert.Equal(2, all.Count);
            // 最旧在前（按编号升序）
            Assert.Contains("结论甲", all[0]);
            Assert.Contains("结论乙", all[1]);
            Assert.False(archive.LastWriteFailed);
            Directory.Delete(dir, true);
        }

        /// <summary>
        /// 构造归档记录——读面用例用（编号 / 用途 / findings / 回收时刻可变）。
        /// </summary>
        /// <param name="id">编号</param>
        /// <param name="purpose">用途标签</param>
        /// <param name="findings">带回载荷</param>
        /// <param name="backAt">回收时刻——Unix 毫秒</param>
        /// <returns>记录实例</returns>
        private static CH4.TimebackScopeRecord BuildScopeRecord(long id, string purpose, string findings, long backAt)
        {
            CH4.TimebackScopeRecord record = new CH4.TimebackScopeRecord();
            record.Id = id;
            record.CatKey = "cat";
            record.Purpose = purpose;
            record.Anchor = 3;
            record.StartAt = backAt - 1000;
            record.BackAt = backAt;
            record.Seconds = 1;
            record.N = 2;
            record.Tokens = 900;
            record.Grew = 100;
            record.Released = 2;
            record.Findings = findings;
            return record;
        }
        /// <summary>
        /// 归档写失败（A104 判据）——写面失败可见（返回 false + LastWriteFailed / LastWriteFailedAny 立起；info archive.ok 数据源）。
        /// </summary>
        [Fact]
        public void Archive_WriteFailureIsVisible()
        {
            // 目录路径指向一个已存在的文件——建目录必然失败（写面失败必须可见）
            string blockPath = Path.Combine(Path.GetTempPath(), "cat4tb_block_" + Guid.NewGuid().ToString("N") + ".txt");
            File.WriteAllText(blockPath, "x");
            CH4.TimebackArchive archive = new CH4.TimebackArchive(blockPath);
            string path = "";
            Assert.False(archive.WriteScopeFile(BuildScopeRecord(1, "用途", "结论", 1000), "", new LlmMessage[0], out path));
            Assert.True(archive.LastWriteFailed);
            Assert.True(CH4.TimebackArchive.LastWriteFailedAny);
            File.Delete(blockPath);
        }
        /// <summary>
        /// A106 批内次序——start 在数组后位也必须先执行：数组前位的 C5 黑名单工具（host-reload）按作用域活跃判定被拒。
        /// （回归：按数组序判定时，前位工具在作用域开启前逃逸——作用域内改本体的缺口。）
        /// </summary>
        [Fact]
        public void BatchOrder_StartStripsBeforeSiblings_LocksBodyRepairTools()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm, new string[] { "timeback-start", "timeback-back", "host-reload" });
            string batch = BuildToolCallsBatch(
                new string[] { "host-reload", "timeback-start" },
                new string[] { "h1", "t1" },
                new string[] { "{\"cat\":\"TextCat\"}", "{\"type\":\"text_search\",\"purpose\":\"批内次序\"}" });
            llm.ToolCallsQueue.Enqueue(batch);
            session.PostUserMessage("批内次序");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle, "phase=" + session.Phase.ToString());
            // 数组前位的 host-reload 被 C5 拦下；后位的 start 已开锚（§十一 起回执只留结构化头）
            Assert.Contains("TIMEBACK_LOCKED", ToolResultText(session, 0));
            Assert.Contains("\"tool\":\"timeback-start\"", ToolResultText(session, 1));
            Assert.True(session.TimebackActive);
        }
        /// <summary>
        /// A106 批内次序——start 失败（参数面拒绝）不阻断、不按假想作用域拦截：作用域未开时同批 host-reload 照常执行。
        /// </summary>
        [Fact]
        public void BatchOrder_StartFailed_NoScopeNoLocks()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm, new string[] { "timeback-start", "timeback-back", "host-reload" });
            string batch = BuildToolCallsBatch(
                new string[] { "timeback-start", "host-reload" },
                new string[] { "t1", "h1" },
                new string[] { "{}", "{\"cat\":\"TextCat\"}" });
            llm.ToolCallsQueue.Enqueue(batch);
            session.PostUserMessage("start 失败");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle, "phase=" + session.Phase.ToString());
            // start 缺 purpose → 参数面拒绝；作用域未开 → host-reload 不被本体修正黑名单拦
            Assert.Contains("TIMEBACK_ARGS", ToolResultText(session, 0));
            Assert.Contains("NO_TOOL", ToolResultText(session, 1));
            Assert.False(session.TimebackActive);
        }
        /// <summary>
        /// A106 批内次序——同一批内 back 在数组前位、start 在数组后位时规范化为「先开后收」：
        /// back 不再误报 NO_SCOPE；A331 起同批整段（声明 + 三结果）收拢为一条 sys 摘要。
        /// </summary>
        [Fact]
        public void BatchOrder_BackStripsAfterSiblings_SameBatchOrderNormalized()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            string batch = BuildToolCallsBatch(
                new string[] { "timeback-back", "info", "timeback-start" },
                new string[] { "t2", "r1", "t1" },
                new string[] { "{\"findings\":\"结论：乱序开收\"}", "{}", "{\"type\":\"text_search\",\"purpose\":\"乱序开收\"}" });
            llm.ToolCallsQueue.Enqueue(batch);
            session.PostUserMessage("乱序开收");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle, "phase=" + session.Phase.ToString());
            // back 作为结论返回（非 NO_SCOPE）→ 随整段收拢落进 sys 摘要；作用域关闭
            string summary = SummaryText(session);
            Assert.Contains("结论：乱序开收", summary);
            Assert.DoesNotContain("NO_SCOPE", summary);
            Assert.False(session.TimebackActive);
            // user + 摘要 + 结论回复 = 3 条（声明与三份结果整段移出）
            Assert.Equal(3, session.Context.GetMessageCount());
        }
        /// <summary>
        /// A106 批内次序——back 与 sibling 工具同批（back 在数组前位）：回收区间仍以本批声明为上界，
        /// 同批 sibling 结果保留，释放条数与跨批查证区间一致。
        /// sibling 取域白名单外工具（`cs-patch` 在 text_search 域被拒——`ERR|TIMEBACK_PROFILE`）：
        /// 域内名单工具均为 OA 工具，单测无工单消费者（Post 后无回执 → 批次不闭合），被拒结果同样是可断言的工具结果消息。
        /// </summary>
        [Fact]
        public void BatchOrder_BackWithSiblingTool_ScopeRangeStillDeleted()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm, new string[] { "timeback-start", "timeback-back", "cs-patch" });
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "t1", "{\"type\":\"text_search\",\"purpose\":\"批内 sibling\"}"));
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("cs-patch", "f1", "{}"));
            llm.ToolCallsQueue.Enqueue(BuildToolCallsBatch(
                new string[] { "timeback-back", "cs-patch" },
                new string[] { "t2", "r2" },
                new string[] { "{\"findings\":\"结论：批内 sibling\"}", "{}" }));
            session.PostUserMessage("批内 sibling 取证");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle, "phase=" + session.Phase.ToString());
            // A331——整段收拢：同批 sibling 的声明与结果同样在区间内，一并移出前文，只留 sys 摘要
            Assert.True(HasMessage(session, LlmRole.User, "结论：批内 sibling"));
            Assert.True(HasMessage(session, LlmRole.User, "整段回收"));
            LlmMessage[] collapsed = session.Context.GetMessages();
            for (int i = 0; i < collapsed.Length; i = i + 1)
            {
                Assert.DoesNotContain("timeback", collapsed[i].ToolCallsJson == null ? "" : collapsed[i].ToolCallsJson);
            }
            Assert.False(session.TimebackActive);
        }
        /// <summary>
        /// A106 批内次序——批内不允许多条同名 timeback 工具：同类第 2 条起判参数面拒绝（不静默丢弃）。
        /// </summary>
        [Fact]
        public void BatchOrder_DuplicateTimebackRejected()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            string batch = BuildToolCallsBatch(
                new string[] { "timeback-start", "timeback-start" },
                new string[] { "t1", "t2" },
                new string[] { "{\"type\":\"text_search\",\"purpose\":\"首条\"}", "{\"type\":\"text_search\",\"purpose\":\"重复\"}" });
            llm.ToolCallsQueue.Enqueue(batch);
            session.PostUserMessage("重复 timeback");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle, "phase=" + session.Phase.ToString());
            Assert.Contains("\"tool\":\"timeback-start\"", ToolResultText(session, 0));
            Assert.Contains("批内不允许多条", ToolResultText(session, 1));
            Assert.True(session.TimebackActive);
        }
        /// <summary>
        /// A331 视图层**原样保留**——timeback 卡与域内工具卡照旧留在对话区（不重绘、不移出），
        /// 摘要以一条 systemautomessage 条目额外插入（单块追加帧）。
        /// `void`（A165 v1 废弃段）仍不复用——摘要块是正经块型，不是废弃标记。
        /// </summary>
        [Fact]
        public void Back_KeepsViewBlocksAppendsSummary()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "t1", "{\"type\":\"text_search\",\"purpose\":\"视图保留验证\"}"));
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("info", "f1", "{}"));
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-back", "t2", "{\"findings\":\"结论：视图保留\"}"));
            session.PostUserMessage("视图保留验证");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle, "phase=" + session.Phase.ToString());
            CH4.ViewBlock[] blocks = session.GetViewBlocks();
            int voidCount = 0;
            int summaryCount = 0;
            for (int i = 0; i < blocks.Length; i = i + 1)
            {
                string payload = blocks[i].Payload == null ? "" : blocks[i].Payload;
                if (blocks[i].RenderType == "void")
                {
                    voidCount = voidCount + 1;
                }
                if (blocks[i].RenderType == "systemautomessage" && payload.Contains("timeback #1 域已闭合", StringComparison.Ordinal))
                {
                    summaryCount = summaryCount + 1;
                }
            }
            // 废弃段不复用；摘要额外插入恰一条
            Assert.Equal(0, voidCount);
            Assert.Equal(1, summaryCount);
            // 视图层原样保留——timeback 两张卡仍在对话区
            Assert.True(ViewHas(session, "timeback-start"));
            Assert.True(ViewHas(session, "timeback-back"));
            // 前文同口径——结论落在摘要里
            Assert.True(HasMessage(session, LlmRole.User, "结论：视图保留"));
        }
        /// <summary>
        /// A331 sibling 卡在视图层保留——视图层只增不改：sibling 卡照旧在对话区，摘要另插一条；
        /// 前文面才是整段回收（sibling 的结果随区间离开前文）。
        /// </summary>
        [Fact]
        public void Back_KeepsSiblingCardAppendsSummary()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            string batch = BuildToolCallsBatch(
                new string[] { "timeback-start", "info" },
                new string[] { "t1", "r1" },
                new string[] { "{\"type\":\"text_search\",\"purpose\":\"sibling 收拢\"}", "{}" });
            llm.ToolCallsQueue.Enqueue(batch);
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-back", "t2", "{\"findings\":\"结论：sibling\"}"));
            session.PostUserMessage("sibling 收拢");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle, "phase=" + session.Phase.ToString());
            CH4.ViewBlock[] blocks = session.GetViewBlocks();
            int voidCount = 0;
            int summaryCount = 0;
            bool anchorCardKept = false;
            bool siblingCardKept = false;
            for (int i = 0; i < blocks.Length; i = i + 1)
            {
                string candidatePayload = blocks[i].Payload == null ? "" : blocks[i].Payload;
                if (blocks[i].RenderType == "void")
                {
                    voidCount = voidCount + 1;
                }
                if (blocks[i].RenderType == "systemautomessage" && candidatePayload.Contains("timeback #1 域已闭合", StringComparison.Ordinal))
                {
                    summaryCount = summaryCount + 1;
                }
                if (blocks[i].RenderType == "toolcard" && candidatePayload.Contains("\"timeback-start\"", StringComparison.Ordinal))
                {
                    anchorCardKept = true;
                }
                if (blocks[i].RenderType == "toolcard" && candidatePayload.Contains("\"info\"", StringComparison.Ordinal))
                {
                    siblingCardKept = true;
                }
            }
            Assert.Equal(0, voidCount);
            // 视图层原样保留——锚卡与 sibling 卡都还在对话区，摘要另插一条
            Assert.True(anchorCardKept);
            Assert.True(siblingCardKept);
            Assert.Equal(1, summaryCount);
        }
        /// <summary>
        /// A331 同批 tail 吞并——back 与其它工具同批时，sibling 的结果与 back 结果连续排列；
        /// 只删到 back 结果会留下「无对应 tool_calls 的孤立 tool 结果」（上游 API 直接 400）。
        /// 判据：整段回收后前文零 tool 消息 · 摘要条数含 tail · 视图层保留（back 卡不移出）。
        /// </summary>
        [Fact]
        public void Back_SwallowsSameBatchTailResults()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "t1", "{\"type\":\"text_search\",\"purpose\":\"同批 tail\"}"));
            string batch = BuildToolCallsBatch(
                new string[] { "timeback-back", "info", "time" },
                new string[] { "b1", "r1", "r2" },
                new string[] { "{\"findings\":\"结论：同批 tail\"}", "{}", "{}" });
            llm.ToolCallsQueue.Enqueue(batch);
            session.PostUserMessage("同批 tail 收拢");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle, "phase=" + session.Phase.ToString());
            LlmMessage[] all = session.Context.GetMessages();
            int toolCount = 0;
            for (int i = 0; i < all.Length; i = i + 1)
            {
                if (all[i].Role == LlmRole.Tool)
                {
                    toolCount = toolCount + 1;
                }
            }
            // 判据一：同批 tail 结果随整段移出——前文零 tool 消息（留下即孤立结果）
            Assert.Equal(0, toolCount);
            // 判据二：摘要在场，条数含 tail（start 声明 + start 结果 + back 声明 + 三条结果）
            string summary = SummaryText(session);
            // 判据二：摘要在场，条数含 tail——start 声明 + start 结果 + 域规范卡（start 批后注入）+ back 声明 + 三条结果
            Assert.True(summary.Contains("整段回收 7 条", StringComparison.Ordinal), summary);
            Assert.Contains("结论：同批 tail", summary);
            // 判据三：视图层同口径——三张卡全部移出，摘要块恰一条
            int summaryCards = 0;
            bool tailCardKept = false;
            CH4.ViewBlock[] blocks = session.GetViewBlocks();
            for (int i = 0; i < blocks.Length; i = i + 1)
            {
                string payload = blocks[i].Payload == null ? "" : blocks[i].Payload;
                if (blocks[i].RenderType == "systemautomessage" && payload.Contains("timeback #1 域已闭合", StringComparison.Ordinal))
                {
                    summaryCards = summaryCards + 1;
                }
                if (blocks[i].RenderType == "toolcard" && payload.Contains("\"timeback-back\"", StringComparison.Ordinal))
                {
                    tailCardKept = true;
                }
            }
            Assert.Equal(1, summaryCards);
            // 视图层原样保留——back 卡仍在对话区，摘要另插一条
            Assert.True(tailCardKept);
        }
        /// <summary>
        /// A229 timeback 未闭合自动拉起——域未回收 = 本轮未结束：轮末注入固定提示拉起（优先于 Note）；
        /// 连续拉起达上限（3 次）后放行本轮结束（防模型不回收导致的无限空转）。
        /// </summary>
        [Fact]
        public void A229_TimebackOpen_PullsUntilLimit()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            llm.ToolCallsQueue.Enqueue(BuildToolCalls("timeback-start", "t1", "{\"type\":\"text_search\",\"purpose\":\"未回收验证\"}"));
            session.PostUserMessage("开域后不回收");
            // 拉起瞬间即回 Idle（与 Note 拉起同构）——PumpUntilIdle 在首次 Idle 即退出、拉起消息尚未入前文，
            // 故泵到「提示出现 3 次且回到 Idle」或上限 500 帧
            for (int i = 0; i < 500; i = i + 1)
            {
                session.Pump();
                Thread.Sleep(5);
                if (CountPullText(session) >= 3 && session.IsIdle)
                {
                    break;
                }
            }
            // 闸门生效——最终回到 Idle（不无限空转）
            Assert.True(session.IsIdle, "phase=" + session.Phase.ToString());
            // 域仍未回收
            Assert.True(session.TimebackActive);
            // 拉起提示入前文——固定文案，恰好 3 次（上限）
            Assert.Equal(3, CountPullText(session));
        }
        /// <summary>统计前文里 A229 拉起提示出现次数。</summary>
        /// <param name="session">会话</param>
        /// <returns>出现次数</returns>
        private static int CountPullText(CH4.ChatSession session)
        {
            int n = 0;
            LlmMessage[] all = session.Context.GetMessages();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                string c = all[i].Content == null ? "" : all[i].Content;
                if (c.Contains("先回收timeback域再结束工作", StringComparison.Ordinal))
                {
                    n = n + 1;
                }
            }
            return n;
        }
    }
}
