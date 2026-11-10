using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Mau.Runtime;
using CatHome4.Contracts;
using Xunit;

namespace CatHome4.Core.Tests
{
    /// <summary>
    /// 工具执行序表与分批调度测试（A127 · design-ch4-tools §三·十四）——
    /// 档位裁决 / timeback 钉死值 / 未登记回落默认 / 对账门禁行为 / 批间串行（会话级）。
    /// 全局静态面（ToolPool / ToolRegistry）参与——归串行集合，防与其余用例互踩。
    /// </summary>
    [Collection("GlobalToolState")]
    public class ToolOrderTests
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

            /// <summary>
            /// 流式对话——队列有内容则产 tool_calls，否则产纯文本。
            /// </summary>
            /// <param name="messages">消息序列</param>
            /// <param name="tools">工具定义</param>
            /// <param name="userId">用户标识</param>
            /// <param name="ct">取消令牌</param>
            /// <returns>事件流</returns>
            public async IAsyncEnumerable<LlmStreamEvent> ChatStream(LlmMessage[] messages, ToolSpec[] tools, string userId = "", [System.Runtime.CompilerServices.EnumeratorCancellation] CancellationToken ct = default)
            {
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
        /// 构造测试会话——宿主直执委托记录调用序（host-* 经 RunTool 出口，可观测批间执行顺序）。
        /// </summary>
        /// <param name="llm">Mock LLM</param>
        /// <param name="toolNames">工具名数组（注册表工具面）</param>
        /// <param name="execLog">宿主直执调用序记录（null=不记录）</param>
        /// <returns>会话实体</returns>
        private static CH4.ChatSession CreateSession(MockLlm llm, string[] toolNames, List<string> execLog)
        {
            ToolSpec[] tools = new ToolSpec[toolNames.Length];
            for (int i = 0; i < toolNames.Length; i = i + 1)
            {
                tools[i] = new ToolSpec(toolNames[i], toolNames[i], "{}");
            }
            CH4.ToolRegistry.Init(tools, null, null);
            CH4.ChatSession.AuthorizedToolNamesProvider = null;
            ChatContext ctx = new ChatContext();
            string tmp = Path.Combine(Path.GetTempPath(), "cat4ord_" + Guid.NewGuid().ToString("N") + ".jsonl");
            SessionStore store = new SessionStore(tmp);
            OA oa = new OA(new ThreadGuard());
            CH4.SessionViewStore viewStore = new CH4.SessionViewStore(Path.Combine(Path.GetTempPath(), "cat4ord_" + Guid.NewGuid().ToString("N") + ".view.jsonl"));
            Func<string, string, string> runner = delegate (string name, string args)
            {
                if (execLog != null)
                {
                    execLog.Add(name);
                }
                return "OK|stub|" + name;
            };
            CH4.ChatSession session = new CH4.ChatSession("ord-session", "ord", ctx, store, llm, oa, tools, runner, viewStore);
            session.SetCatKey("ord-session");
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
        /// 拼接同批多条 tool_calls JSON（数组序 = LLM 声明序）。
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
                sb.Append("{\"id\":\"" + ids[i] + "\",\"function\":{\"name\":\"" + toolNames[i] + "\",\"arguments\":" + System.Text.Json.JsonSerializer.Serialize(argsJsons[i]) + "}}");
            }
            sb.Append("]");
            return sb.ToString();
        }

        /// <summary>
        /// 只读档——观测类工具落 -1（先于默认档，新工具不会插到只读面之前）。
        /// </summary>
        [Fact]
        public void ReadOnlyToolsRankMinusOne()
        {
            Assert.Equal(-1, CH4.ToolOrderTable.Resolve("text-read"));
            Assert.Equal(-1, CH4.ToolOrderTable.Resolve("file-tree"));
            Assert.Equal(-1, CH4.ToolOrderTable.Resolve("cs-read"));
            Assert.Equal(-1, CH4.ToolOrderTable.Resolve("info"));
            Assert.Equal(-1, CH4.ToolOrderTable.Resolve("host-flows"));
        }

        /// <summary>
        /// 独占档——cs-* 写操作与工程构建落 2，每次调用各自成批（杜绝「预检读全项目」与「同批写文件」撞车，
        /// 以及并发编译抢同工程产物目录）。
        /// </summary>
        [Fact]
        public void ExclusiveToolsRankTwo()
        {
            Assert.Equal(2, CH4.ToolOrderTable.Resolve("cs-patch"));
            Assert.Equal(2, CH4.ToolOrderTable.Resolve("cs-member"));
            Assert.Equal(2, CH4.ToolOrderTable.Resolve("cs-comment"));
            Assert.Equal(2, CH4.ToolOrderTable.Resolve("cs-format"));
            Assert.Equal(2, CH4.ToolOrderTable.Resolve("cs-build"));
            Assert.True(CH4.ToolOrderTable.IsExclusive("cs-patch"));
            Assert.True(CH4.ToolOrderTable.IsExclusive("cs-build"));
            Assert.False(CH4.ToolOrderTable.IsExclusive("text-write"));
            Assert.False(CH4.ToolOrderTable.IsExclusive("cs-read"));
        }

        /// <summary>
        /// 写入档——变更类工具落 1（后于默认档与只读档）。
        /// </summary>
        [Fact]
        public void WriteToolsRankOne()
        {
            Assert.Equal(1, CH4.ToolOrderTable.Resolve("text-write"));
            Assert.Equal(1, CH4.ToolOrderTable.Resolve("file-delete"));
            Assert.Equal(1, CH4.ToolOrderTable.Resolve("text-replace"));
            Assert.Equal(1, CH4.ToolOrderTable.Resolve("config-set"));
        }

        /// <summary>
        /// 构建档——执行 / 部署类工具落 3（最后；cs-build 归独占档 2，不在此列）。
        /// </summary>
        [Fact]
        public void BuildToolsRankTwo()
        {
            Assert.Equal(3, CH4.ToolOrderTable.Resolve("mau-proj"));
            Assert.Equal(3, CH4.ToolOrderTable.Resolve("powershell"));
            Assert.Equal(3, CH4.ToolOrderTable.Resolve("host-reload"));
            Assert.Equal(3, CH4.ToolOrderTable.Resolve("mau-setup"));
        }

        /// <summary>
        /// 默认档——显式登记项与未登记项同落 0（默认数插在只读之后、写入之前）。
        /// </summary>
        [Fact]
        public void DefaultRankIsZero()
        {
            Assert.Equal(0, CH4.ToolOrderTable.Resolve("sleep"));
            Assert.Equal(0, CH4.ToolOrderTable.Resolve("timer"));
            Assert.Equal(0, CH4.ToolOrderTable.Resolve("未登记的工具名"));
        }

        /// <summary>
        /// timeback 两态各具一名——timeback-start = -100 / timeback-back = 100（静态表按名裁死，参数面无关）。
        /// </summary>
        [Fact]
        public void TimebackPinnedByName()
        {
            Assert.Equal(-100, CH4.ToolOrderTable.Resolve("timeback-start"));
            Assert.Equal(100, CH4.ToolOrderTable.Resolve("timeback-back"));
        }

        /// <summary>
        /// 显示文本——timeback 两态各自单值（-100 / 100，前端零裁决）；其余为单值数字串。
        /// </summary>
        [Fact]
        public void OrderTextIsSingleValuePerName()
        {
            Assert.Equal("-100", CH4.ToolOrderTable.OrderText("timeback-start"));
            Assert.Equal("100", CH4.ToolOrderTable.OrderText("timeback-back"));
            Assert.Equal("-1", CH4.ToolOrderTable.OrderText("text-read"));
            Assert.Equal("2", CH4.ToolOrderTable.OrderText("cs-build"));
            Assert.Equal("0", CH4.ToolOrderTable.OrderText("未登记的工具名"));
        }

        /// <summary>
        /// 定义注记形态——`order: &lt;n&gt;`（注入工具描述尾行的契约）。
        /// </summary>
        [Fact]
        public void NoteLineCarriesOrder()
        {
            Assert.Equal("order: -1", CH4.ToolOrderTable.NoteLine("text-read"));
            Assert.Equal("order: -100", CH4.ToolOrderTable.NoteLine("timeback-start"));
            Assert.Equal("order: 100", CH4.ToolOrderTable.NoteLine("timeback-back"));
        }

        /// <summary>
        /// 登记面自洽——无重复名，且各档代表均在册（对账基准可用）。
        /// </summary>
        [Fact]
        public void RegisteredNamesAreUnique()
        {
            string[] names = CH4.ToolOrderTable.RegisteredNames();
            Assert.True(names.Length > 0);
            for (int i = 0; i < names.Length; i = i + 1)
            {
                for (int j = i + 1; j < names.Length; j = j + 1)
                {
                    Assert.NotEqual(names[i], names[j]);
                }
            }
            Assert.Contains("text-read", names);
            Assert.Contains("text-write", names);
            Assert.Contains("cs-build", names);
            Assert.Contains("sleep", names);
            Assert.Contains("timeback-start", names);
            Assert.Contains("timeback-back", names);
        }
        /// <summary>
        /// 组件与 TS 件档位登记（A324）——读面 -1 / 写面 1 / 独占面 2。
        /// 未登记时一律回落默认档 0；对账门禁只报「未登记名」，档位错配报不出，故由本用例兜底。
        /// </summary>
        [Fact]
        public void GroupAndTsToolsAreRegistered()
        {
            Assert.Equal(-1, CH4.ToolOrderTable.Resolve("group-info"));
            Assert.Equal(-1, CH4.ToolOrderTable.Resolve("group-inbox"));
            Assert.Equal(-1, CH4.ToolOrderTable.Resolve("group-catinfo"));
            Assert.Equal(-1, CH4.ToolOrderTable.Resolve("group-catcfg-get"));
            Assert.Equal(1, CH4.ToolOrderTable.Resolve("group-post"));
            Assert.Equal(1, CH4.ToolOrderTable.Resolve("group-delegate"));
            Assert.Equal(1, CH4.ToolOrderTable.Resolve("group-catcfg-set"));
            Assert.Equal(2, CH4.ToolOrderTable.Resolve("group-newsession"));
            Assert.Equal(-1, CH4.ToolOrderTable.Resolve("ts-check"));
            Assert.Equal(-1, CH4.ToolOrderTable.Resolve("ts-list"));
            Assert.Equal(-1, CH4.ToolOrderTable.Resolve("ts-read"));
            Assert.Equal(-1, CH4.ToolOrderTable.Resolve("ts-find-ref"));
            Assert.Equal(-1, CH4.ToolOrderTable.Resolve("ts-info"));
            Assert.Equal(-1, CH4.ToolOrderTable.Resolve("ts-lint"));
            Assert.Equal(2, CH4.ToolOrderTable.Resolve("ts-patch"));
            Assert.Equal(2, CH4.ToolOrderTable.Resolve("ts-test"));
            Assert.Equal(2, CH4.ToolOrderTable.Resolve("ts-format"));
            Assert.True(CH4.ToolOrderTable.IsExclusive("group-newsession"));
            Assert.True(CH4.ToolOrderTable.IsExclusive("ts-patch"));
            Assert.Empty(CH4.ToolOrderTable.FindUnregistered(CH4.ToolOrderTable.RegisteredNames()));
        }

        /// <summary>
        /// 对账门禁——未登记名被报出，已登记名不误报（名单含 text-read）。
        /// </summary>
        [Fact]
        public void FindUnregisteredReportsMissing()
        {
            string[] probe = new string[] { "text-read", "不存在的工具", "sleep" };
            string[] missing = CH4.ToolOrderTable.FindUnregistered(probe);
            Assert.Single(missing);
            Assert.Equal("不存在的工具", missing[0]);
            Assert.Empty(CH4.ToolOrderTable.FindUnregistered(new string[] { "text-read", "cs-build" }));
            Assert.Empty(CH4.ToolOrderTable.FindUnregistered(null));
        }

        /// <summary>
        /// 池对账链路——灌入含未知工具的池定义，FindUnregistered 能报出（新增工具忘登记不静默）。
        /// </summary>
        [Fact]
        public void PoolAccountingFindsUnknownTool()
        {
            string poolJson = "{\"group\":\"\",\"tools\":[{\"name\":\"text-read\",\"description\":\"d\",\"parameters\":{}},{\"name\":\"zz-unknown\",\"description\":\"d\",\"parameters\":{}}]}";
            CH4.ToolPool.RebuildAll(null, poolJson);
            string[] missing = CH4.ToolOrderTable.FindUnregistered(CH4.ToolPool.AllNames());
            Assert.Single(missing);
            Assert.Equal("zz-unknown", missing[0]);
            // 清池——不给后续用例留残余
            CH4.ToolPool.RebuildAll(null, "{\"group\":\"\",\"tools\":[]}");
        }

        /// <summary>
        /// 批间串行——LLM 声明序与 order 序相反时按 order 分桶执行（宿主直执出口可观测）。
        /// 声明序 [host-reload(3), host-flows(-1)] → 执行序应为 [host-flows, host-reload]；
        /// 未分批实现会按声明序执行（host-reload 先），故本用例对「分批」有鉴别力。
        /// </summary>
        [Fact]
        public void BatchesRunSeriallyByOrder()
        {
            List<string> execLog = new List<string>();
            MockLlm llm = new MockLlm();
            string[] toolNames = new string[] { "host-reload", "host-flows" };
            llm.ToolCallsQueue.Enqueue(BuildToolCallsBatch(
                new string[] { "host-reload", "host-flows" },
                new string[] { "c1", "c2" },
                new string[] { "{\"cat\":\"TextCat\"}", "{}" }));
            CH4.ChatSession session = CreateSession(llm, toolNames, execLog);
            session.PostUserMessage("批序验证");
            PumpUntilIdle(session);
            Assert.Equal(2, execLog.Count);
            Assert.Equal("host-flows", execLog[0]);
            Assert.Equal("host-reload", execLog[1]);
        }

        /// <summary>
        /// 单批情形——同 order 的工具按批一次派发（顺序仍由 order 决定，不因声明序改写）。
        /// 声明序 [host-reload, host-reload] 同批（order 3）→ 执行序保持声明序（批内按声明序、批末直执）。
        /// </summary>
        [Fact]
        public void SameOrderFormsSingleBatch()
        {
            List<string> execLog = new List<string>();
            MockLlm llm = new MockLlm();
            string[] toolNames = new string[] { "host-reload", "host-flows" };
            llm.ToolCallsQueue.Enqueue(BuildToolCallsBatch(
                new string[] { "host-flows", "host-reload" },
                new string[] { "d1", "d2" },
                new string[] { "{}", "{\"cat\":\"TextCat\"}" }));
            CH4.ChatSession session = CreateSession(llm, toolNames, execLog);
            session.PostUserMessage("同序批验证");
            PumpUntilIdle(session);
            // order 序与声明序一致时执行序不变（对照组——防「批次实现改写同序批」）
            Assert.Equal(2, execLog.Count);
            Assert.Equal("host-flows", execLog[0]);
            Assert.Equal("host-reload", execLog[1]);
        }

        /// <summary>
        /// 分批计划——独占档每次调用各自成批（A144 核心判据）。
        /// ① [cs-patch, text-write, cs-patch]：写入档 1 先于独占档 2；两个 cs-patch 各自成批 → 3 批（声明序被序值改写）。
        /// ② [cs-build, cs-build]：同名两次调用也各自成批 → 2 批（静态档位值解不了这一格）。
        /// ③ 对照组 [text-write, text-replace]：非独占同序工具仍同批 → 1 批。
        /// </summary>
        [Fact]
        public void PlanBatchesIsolatesExclusiveTools()
        {
            List<string> names = new List<string>();
            names.Add("cs-patch");
            names.Add("text-write");
            names.Add("cs-patch");
            List<int> orders = new List<int>();
            for (int i = 0; i < names.Count; i = i + 1)
            {
                orders.Add(CH4.ToolOrderTable.Resolve(names[i]));
            }
            List<List<int>> plan = CH4.ToolOrderTable.PlanBatches(names, orders);
            Assert.Equal(3, plan.Count);
            Assert.Single(plan[0]);
            Assert.Equal(1, plan[0][0]);
            Assert.Single(plan[1]);
            Assert.Equal(0, plan[1][0]);
            Assert.Single(plan[2]);
            Assert.Equal(2, plan[2][0]);

            List<string> twins = new List<string>();
            twins.Add("cs-build");
            twins.Add("cs-build");
            List<int> twinsOrders = new List<int>();
            twinsOrders.Add(CH4.ToolOrderTable.Resolve("cs-build"));
            twinsOrders.Add(CH4.ToolOrderTable.Resolve("cs-build"));
            List<List<int>> twinsPlan = CH4.ToolOrderTable.PlanBatches(twins, twinsOrders);
            Assert.Equal(2, twinsPlan.Count);
            Assert.Single(twinsPlan[0]);
            Assert.Single(twinsPlan[1]);

            List<string> plain = new List<string>();
            plain.Add("text-write");
            plain.Add("text-replace");
            List<int> plainOrders = new List<int>();
            plainOrders.Add(CH4.ToolOrderTable.Resolve("text-write"));
            plainOrders.Add(CH4.ToolOrderTable.Resolve("text-replace"));
            List<List<int>> plainPlan = CH4.ToolOrderTable.PlanBatches(plain, plainOrders);
            Assert.Single(plainPlan);
            Assert.Equal(2, plainPlan[0].Count);
        }
    }
}
