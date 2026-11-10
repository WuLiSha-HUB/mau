using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Mau.Runtime;
using CatHome4.Contracts;
using Xunit;

namespace CatHome4.Core.Tests
{
    /// <summary>
    /// ChatSession 核心测试——相位环/工具批/Note 全链（S7 程序集拆分验收）。
    /// 覆盖：纯 LLM 回复相位环闭环 / 内置工具（Note）工具批路径 / Note 手动添加与启动 / 工具批无限续轮（2026-09-09 删收敛上限）。
    /// </summary>
    [Collection("GlobalToolState")]
    public class ChatSessionTests
    {
        /// <summary>
        /// Mock LLM 运行时——按队列返回 tool_calls 或纯文本（每次 ChatStream 调用消费一个）。
        /// </summary>
        private sealed class MockLlm : ILlmRuntime
        {
            /// <summary>每轮 tool_calls JSON 队列——空=返回纯文本</summary>
            public Queue<string> ToolCallsQueue = new Queue<string>();

            /// <summary>纯文本回复内容</summary>
            public string ReplyText = "ok";

            /// <summary>E3——usage JSON（非空则在文本前产 Usage 事件；null=不产）</summary>
            public string UsageJson = "";

            /// <summary>ChatStream 调用次数——工具批收敛验证</summary>
            public int CallCount = 0;

            /// <summary>前 N 次调用返回错误——S2 重试/错误隔离验证（0=不注入错误）</summary>
            public int FailTimes = 0;

            /// <summary>注入的错误文本——FailTimes > 0 时使用</summary>
            public string FailText = "ERR|TEST|模拟失败";

            /// <summary>是否模拟重试——产 Retrying 事件后正常回复（S2 §8.4 前端可见性验证）</summary>
            public bool EmitRetry = false;

            /// <summary>是否模拟空回复——只产 Done 不产 Text（空回复续传验证）</summary>
            public bool EmptyReply = false;

            /// <summary>空回复次数——前 N 次调用产空回复（0=每次）</summary>
            public int EmptyReplyTimes = 0;

            /// <summary>是否模拟 STREAM_CLOSED——产 Error 不产 Done（续传验证）</summary>
            public bool EmitStreamClosed = false;

            /// <summary>STREAM_CLOSED 次数——前 N 次调用产 Error（0=每次）</summary>
            public int StreamClosedTimes = 0;

            /// <summary>A94——重试耗尽模拟：产 Retrying 事件后直接产 Error（Runtime 重试后仍失败路径）</summary>
            public bool RetryThenFail = false;
            /// <summary>是否模拟纯空格回复——只产空格 Text（Trim 判空续传验证）</summary>
            public bool WhitespaceReply = false;
            /// <summary>纯空格次数——前 N 次调用产空格（0=每次）</summary>
            public int WhitespaceReplyTimes = 0;

            /// <summary>运行态连续计时验证——思考增量块数（≤1 不生效；>1 产多块 Reasoning 同态事件）</summary>
            public int ChunkCount = 0;

            /// <summary>运行态连续计时验证——增量块间隔毫秒（ChunkCount &gt; 1 时生效）</summary>
            public int ChunkSleepMs = 0;

            /// <summary>P6 中止模拟——产 Text 后挂起等待此事件（Pause 时 cts.Cancel → WaitOne 抛 OCE；null=不挂起）</summary>
            public AutoResetEvent HoldStream;

            /// <summary>P6 中止模拟——产 Retrying 前挂起等待取消（Runtime 取消识别后路径：取消 → OCE 冒泡，不产 Retrying）</summary>
            public bool EmitRetryThenCancel;

            /// <summary>
            /// 流式对话——按队列返回工具调用或纯文本。
            /// </summary>
            /// <param name="messages">消息序列</param>
            /// <param name="tools">工具定义</param>
            /// <param name="userId">用户标识</param>
            /// <param name="ct">取消令牌</param>
            /// <returns>事件流</returns>
            public async IAsyncEnumerable<LlmStreamEvent> ChatStream(LlmMessage[] messages, ToolSpec[] tools, string userId = "", [System.Runtime.CompilerServices.EnumeratorCancellation] CancellationToken ct = default)
            {
                CallCount = CallCount + 1;
                // S2 错误注入——前 N 次调用产出 Error 事件（对应 Runtime 重试耗尽前/后两态）
                if (CallCount <= FailTimes)
                {
                    yield return new LlmStreamEvent(LlmStreamKind.Error, FailText);
                    yield break;
                }
                // A94——重试耗尽模拟：产 Retrying → 产 Error（Runtime 有限重试后仍失败路径）
                if (RetryThenFail)
                {
                    yield return new LlmStreamEvent(LlmStreamKind.Retrying, "RETRY|1/3|ERR|TRANSPORT|模拟连接失败");
                    yield return new LlmStreamEvent(LlmStreamKind.Error, "ERR|TRANSPORT|模拟连接失败（重试耗尽）");
                    yield break;
                }
                // 空回复续传——STREAM_CLOSED 模拟：前 N 次产 Error（未以 [DONE] 结束）
                if (EmitStreamClosed && (StreamClosedTimes == 0 || CallCount <= StreamClosedTimes))
                {
                    yield return new LlmStreamEvent(LlmStreamKind.Error, "ERR|STREAM_CLOSED|SSE 流未以 [DONE] 结束");
                    yield break;
                }
                // 空回复续传——空回复模拟：只产 Done 不产 Text（有思考无回复语义；EmptyReplyTimes 控制前 N 次）
                if (EmptyReply && (EmptyReplyTimes == 0 || CallCount <= EmptyReplyTimes))
                {
                    yield return new LlmStreamEvent(LlmStreamKind.Reasoning, "思考中");
                    yield return new LlmStreamEvent(LlmStreamKind.Done, "");
                    yield break;
                }
                // 空回复续传——纯空格模拟：只产空格 Text（Trim 判空续传验证；WhitespaceReplyTimes 控制前 N 次）
                if (WhitespaceReply && (WhitespaceReplyTimes == 0 || CallCount <= WhitespaceReplyTimes))
                {
                    yield return new LlmStreamEvent(LlmStreamKind.Text, "   ");
                    yield return new LlmStreamEvent(LlmStreamKind.Done, "");
                    yield break;
                }
                // S2 §8.4——模拟 Runtime 重试：重试通知后正常产出（前端可见性验证）
                if (EmitRetry)
                {
                    yield return new LlmStreamEvent(LlmStreamKind.Retrying, "RETRY|1/3|ERR|TRANSPORT|模拟网络抖动");
                    // 运行态七态——退避结束、重发开始（真实 Runtime：退避后重发前产 RetryResume；2026-09-16）
                    yield return new LlmStreamEvent(LlmStreamKind.RetryResume, "");
                }
                // P6 中止——取消识别路径：产 Retrying 前挂起（Pause → cts.Cancel → OCE 冒泡——不产 retry 气泡；Runtime catch OCE throw 语义模拟）
                if (EmitRetryThenCancel)
                {
                    while (!HoldStream.WaitOne(50))
                    {
                        ct.ThrowIfCancellationRequested();
                    }
                }
                if (ToolCallsQueue.Count > 0)
                {
                    // A78——思考先于工具决策流（真实链路帧序：reasoning 增量帧 → tool_calls 增量帧）
                    if (ChunkCount > 1)
                    {
                        for (int ri = 0; ri < ChunkCount; ri = ri + 1)
                        {
                            yield return new LlmStreamEvent(LlmStreamKind.Reasoning, "思");
                            await Task.Delay(ChunkSleepMs);
                        }
                    }
                    string tc = ToolCallsQueue.Dequeue();
                    // 运行态七态——工具决策流开始（真实 Runtime：首个 tool_calls 增量帧产一次；2026-09-16）
                    yield return new LlmStreamEvent(LlmStreamKind.ToolCallsStart, "");
                    yield return new LlmStreamEvent(LlmStreamKind.ToolCalls, tc);
                    await Task.Yield();
                    yield return new LlmStreamEvent(LlmStreamKind.Done, "");
                    yield break;
                }
                // E3——usage 事件（文本前产——模拟 usage-only 尾帧到达）
                if (UsageJson != null && UsageJson.Length > 0)
                {
                    yield return new LlmStreamEvent(LlmStreamKind.Usage, UsageJson);
                }
                // 运行态连续计时验证——多块思考增量（同态重复事件：真实链路每个 reasoning 增量帧切一次态）
                if (ChunkCount > 1)
                {
                    for (int ci = 0; ci < ChunkCount; ci = ci + 1)
                    {
                        yield return new LlmStreamEvent(LlmStreamKind.Reasoning, "思");
                        await Task.Delay(ChunkSleepMs);
                    }
                }
                yield return new LlmStreamEvent(LlmStreamKind.Text, ReplyText);
                await Task.Yield();
                // P6 中止模拟——挂起流等待用户暂停（Pause → cts.Cancel → ThrowIfCancellationRequested 抛 OCE——Runtime 取消路径；AutoResetEvent 无 CancellationToken 重载——轮询）
                if (HoldStream != null)
                {
                    while (!HoldStream.WaitOne(50))
                    {
                        ct.ThrowIfCancellationRequested();
                    }
                }
                yield return new LlmStreamEvent(LlmStreamKind.Done, "");
            }
        }

        /// <summary>
        /// Mock 宿主推送面——捕获 SSE 事件调用（E3 usage 转发断言）。
        /// </summary>
        private sealed class MockHost : IHostPush
        {
            /// <summary>被观察会话——A162 状态推送：视图变更经会话增量取用回放（表读取时自动回放未取走的增量）</summary>
            public CH4.ChatSession Session;

            /// <summary>捕获的视图块——renderType → payload 列表（F4 视图事件）</summary>
            private readonly Dictionary<string, List<string>> _viewEvents = new Dictionary<string, List<string>>();

            /// <summary>捕获的事件 op——renderType → op 列表（A162：块面统一 delta；旧 live.* / persist.append 已退役）</summary>
            private readonly Dictionary<string, List<string>> _viewOps = new Dictionary<string, List<string>>();

            /// <summary>捕获的事件块键——renderType → key 列表（同键原位更新断言用）</summary>
            private readonly Dictionary<string, List<string>> _viewKeys = new Dictionary<string, List<string>>();

            /// <summary>事件到达序——renderType 列表（跨类型时序断言用）</summary>
            private readonly List<string> _eventOrder = new List<string>();

            /// <summary>最近一帧 live 段的 type 存储</summary>
            private string _lastLiveType = "";

            /// <summary>最近一帧 live 段的 context 存储</summary>
            private string _lastLiveContext = "";

            /// <summary>最近一帧 live 段的 type（A196 状态投影——中止回空态 / 完成移出断言用；读取即先回放未取走的帧）</summary>
            public string LastLiveType
            {
                get
                {
                    FlushView();
                    return _lastLiveType;
                }
            }

            /// <summary>最近一帧 live 段的 context（A196——未完成工具清单断言用；读取即先回放未取走的帧）</summary>
            public string LastLiveContext
            {
                get
                {
                    FlushView();
                    return _lastLiveContext;
                }
            }

            /// <summary>捕获视图块——renderType → payload 列表（读取即先回放未取走的增量）</summary>
            public Dictionary<string, List<string>> ViewEvents
            {
                get
                {
                    FlushView();
                    return _viewEvents;
                }
            }

            /// <summary>捕获事件 op——renderType → op 列表（读取即先回放未取走的增量）</summary>
            public Dictionary<string, List<string>> ViewOps
            {
                get
                {
                    FlushView();
                    return _viewOps;
                }
            }

            /// <summary>捕获事件块键——renderType → key 列表（读取即先回放未取走的增量）</summary>
            public Dictionary<string, List<string>> ViewKeys
            {
                get
                {
                    FlushView();
                    return _viewKeys;
                }
            }

            /// <summary>事件到达序——renderType 列表（读取即先回放未取走的增量）</summary>
            public List<string> EventOrder
            {
                get
                {
                    FlushView();
                    return _eventOrder;
                }
            }

            /// <summary>
            /// A165——取会话未取走的视图帧并回放为捕获事件（chat 快照协议 v2）：
            /// persist 段条目按 type / payload 入表（op 记 full / append）· live 段条目记 op=live ·
            /// state 段的 note 并入事件序（消费面保留）。帧取走即清，故表读取可反复安全调用。
            /// </summary>
            public void FlushView()
            {
                if (Session == null)
                {
                    return;
                }
                string json = Session.TakeViewFrameJson();
                if (json == null || json.Length == 0)
                {
                    return;
                }
                using (JsonDocument doc = JsonDocument.Parse(json))
                {
                    JsonElement persist;
                    if (doc.RootElement.TryGetProperty("persist", out persist))
                    {
                        string mode = persist.GetProperty("mode").GetString();
                        foreach (JsonElement it in persist.GetProperty("items").EnumerateArray())
                        {
                            string renderType = it.GetProperty("type").GetString();
                            string payload = "";
                            JsonElement p;
                            if (it.TryGetProperty("payload", out p))
                            {
                                payload = p.GetRawText();
                            }
                            Accumulate(_viewEvents, renderType, payload);
                            Accumulate(_viewOps, renderType, mode);
                            _eventOrder.Add(renderType);
                        }
                    }
                    JsonElement live;
                    if (doc.RootElement.TryGetProperty("live", out live))
                    {
                        // A196——live 段为 {type, context} 两字符串：toolrun 的 context 是未完成工具卡数组，
                        // 逐卡登记（保持旧 items 语义——按工具卡元素文本核）；thinksse / replysse 登记全文；empty 不登记
                        string liveType = live.GetProperty("type").GetString();
                        string liveContext = live.GetProperty("context").GetString();
                        _lastLiveType = liveType;
                        _lastLiveContext = liveContext;
                        if (liveType == "toolrun")
                        {
                            using (JsonDocument cards = JsonDocument.Parse(liveContext.Length > 0 ? liveContext : "[]"))
                            {
                                foreach (JsonElement card in cards.RootElement.EnumerateArray())
                                {
                                    Accumulate(_viewEvents, "toolrun", card.GetRawText());
                                    Accumulate(_viewOps, "toolrun", "live");
                                    _eventOrder.Add("toolrun");
                                }
                            }
                        }
                        else if (liveType != "empty")
                        {
                            Accumulate(_viewEvents, liveType, liveContext);
                            Accumulate(_viewOps, liveType, "live");
                            _eventOrder.Add(liveType);
                        }
                    }
                    JsonElement state;
                    if (doc.RootElement.TryGetProperty("state", out state))
                    {
                        JsonElement note;
                        if (state.TryGetProperty("note", out note) && note.ValueKind != JsonValueKind.Null)
                        {
                            Accumulate(_viewEvents, "note", note.GetRawText());
                            Accumulate(_viewOps, "note", "state");
                            _eventOrder.Add("note");
                        }
                        // A165——业务态面（原 usage / chatdone / paused 三条 control 事件并入 state 段）
                        JsonElement tokens;
                        if (state.TryGetProperty("tokens", out tokens) && tokens.ValueKind != JsonValueKind.Null)
                        {
                            Accumulate(_viewEvents, "tokens", tokens.GetRawText());
                            Accumulate(_viewOps, "tokens", "state");
                        }
                        JsonElement runState;
                        if (state.TryGetProperty("runState", out runState) && runState.ValueKind == JsonValueKind.String)
                        {
                            Accumulate(_viewEvents, "runState", runState.GetString());
                            Accumulate(_viewOps, "runState", "state");
                        }
                    }
                }
            }

            /// <summary>即时事件捕获——control 等非块事件仍走推送面（A162：块面不再即时推送）</summary>
            /// <param name="op">事件语义（control）</param>
            /// <param name="payload">载荷 JSON</param>
            /// <param name="meta">块元数据 JSON</param>
            public void PushView(string op, string payload, string meta)
            {
                string renderType = MetaValue(meta, "renderType");
                string key = MetaValue(meta, "key");
                Accumulate(_viewEvents, renderType, payload);
                Accumulate(_viewOps, renderType, op);
                Accumulate(_viewKeys, renderType, key);
                _eventOrder.Add(renderType);
            }

            /// <summary>累积辅助——按 renderType 列表追加</summary>
            /// <param name="map">目标表</param>
            /// <param name="renderType">渲染类型键</param>
            /// <param name="value">追加值</param>
            private static void Accumulate(Dictionary<string, List<string>> map, string renderType, string value)
            {
                List<string> list;
                if (!map.TryGetValue(renderType, out list))
                {
                    list = new List<string>();
                    map[renderType] = list;
                }
                list.Add(value);
            }

            /// <summary>块元数据取值——meta JSON 内单键字符串（缺失回空串）</summary>
            /// <param name="meta">块元数据 JSON</param>
            /// <param name="name">键名</param>
            /// <returns>字符串值（缺失 = 空串）</returns>
            private static string MetaValue(string meta, string name)
            {
                using (JsonDocument doc = JsonDocument.Parse(meta))
                {
                    JsonElement el;
                    if (!doc.RootElement.TryGetProperty(name, out el))
                    {
                        return "";
                    }
                    return el.ToString();
                }
            }
        }

        /// <summary>
        /// 构造测试会话——临时前文文件 + Mock LLM + 声明面工具（缺省仅 Note）。
        /// </summary>
        /// <param name="llm">LLM 运行时</param>
        /// <param name="declared">会话声明面工具（缺省 = 仅 Note；OA 工具测试须显式声明——无消费者时靠 Pause 收尾）</param>
        /// <returns>会话实体</returns>
        private static CH4.ChatSession CreateSession(ILlmRuntime llm, ToolSpec[] declared = null)
        {
            // 工具注册表初始化——内置判定（IsBuiltinTool）+ 池校验（IsToolAllowed：工具须在注册面内）读 ToolRegistry 单一真相源；
            // 测试环境无宿主 Init，须显式灌入（内置表 + 声明面工具——生产环境声明面必为池子集，测试同构；判例 2026-09-16）
            List<ToolSpec> regSpecs = new List<ToolSpec>();
            regSpecs.Add(new ToolSpec("Note", "Note 任务追踪", "{}"));
            regSpecs.Add(new ToolSpec("time", "当前时间", "{}"));
            regSpecs.Add(new ToolSpec("random", "随机整数", "{}"));
            regSpecs.Add(new ToolSpec("info", "运行状态", "{}"));
            regSpecs.Add(new ToolSpec("sleep", "定时唤醒", "{}"));
            regSpecs.Add(new ToolSpec("timer", "定时注入", "{}"));
            if (declared != null)
            {
                for (int d = 0; d < declared.Length; d = d + 1)
                {
                    bool duplicate = false;
                    for (int r = 0; r < regSpecs.Count; r = r + 1)
                    {
                        if (regSpecs[r].Name == declared[d].Name)
                        {
                            duplicate = true;
                            break;
                        }
                    }
                    if (!duplicate)
                    {
                        regSpecs.Add(declared[d]);
                    }
                }
            }
            CH4.ToolRegistry.Init(regSpecs.ToArray(), null, null);
            ChatContext ctx = new ChatContext();
            string tmp = Path.Combine(Path.GetTempPath(), "cat4test_" + Guid.NewGuid().ToString("N") + ".jsonl");
            SessionStore store = new SessionStore(tmp);
            OA oa = new OA(new ThreadGuard());
            // 测试工具声明面——含内置 Note（IsToolAllowed 声明面拦截需要；OA 工具测试走内置避免无消费者卡死）
            ToolSpec[] tools;
            if (declared == null)
            {
                tools = new ToolSpec[]
                {
                    new ToolSpec("Note", "Note 任务追踪", "{}")
                };
            }
            else
            {
                tools = declared;
            }
            CH4.SessionViewStore viewStore = new CH4.SessionViewStore(Path.Combine(Path.GetTempPath(), "cat4test_" + Guid.NewGuid().ToString("N") + ".view.jsonl"));
            CH4.ChatSession session = new CH4.ChatSession("test-session", "test", ctx, store, llm, oa, tools, delegate (string name, string args) { return "ERR|NO_TOOL|" + name; }, viewStore);
            return session;
        }

        /// <summary>
        /// 泵会话直到 Idle——上限 200 帧（防死循环）。
        /// </summary>
        /// <param name="session">会话</param>
        private static void PumpUntilIdle(CH4.ChatSession session)
        {
            // 至少泵 3 帧消费 pending（初始 Idle 但 pending 有内容）；后台 LLM 消费需真实时间片（sleep 5ms）
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
        /// 提取最近一条 assistant 文本——从会话上下文。
        /// </summary>
        /// <param name="session">会话</param>
        /// <returns>assistant 内容（无则空串）</returns>
        private static string GetLastAssistantText(CH4.ChatSession session)
        {
            LlmMessage[] all = session.Context.GetMessages();
            for (int i = all.Length - 1; i >= 0; i = i - 1)
            {
                if (all[i].Role == LlmRole.Assistant && all[i].Content != null && all[i].Content.Length > 0)
                {
                    return all[i].Content;
                }
            }
            return "";
        }

        /// <summary>
        /// 相位环闭环——纯 LLM 文本回复：PostUserMessage → Pump → 回复入上下文 → 回 Idle。
        /// </summary>
        [Fact]
        public void PhaseCycle_TextReply_ReturnsToIdle()
        {
            MockLlm llm = new MockLlm();
            llm.ReplyText = "你好，测试回复";
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("测试一下");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            Assert.Contains("你好，测试回复", GetLastAssistantText(session));
            Assert.Equal(1, llm.CallCount);
        }

        /// <summary>
        /// Note 工具批路径——LLM 返回 Note set 工具调用 → 工具批执行 → 计划写入 → 第二轮文本回复。
        /// </summary>
        [Fact]
        public void Note_ExecuteTool_SetPlan_ThroughToolBatch()
        {
            MockLlm llm = new MockLlm();
            // 第一轮：Note 工具调用（set 计划）
            string tc = "[{\"id\":\"n1\",\"function\":{\"name\":\"Note\",\"arguments\":\"{\\\"action\\\":\\\"set\\\",\\\"content\\\":[\\\"任务A\\\",\\\"任务B\\\"]}\"}}]";
            llm.ToolCallsQueue.Enqueue(tc);
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("创建计划");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            // 计划已写入 Note 状态（JSON 中文为 \uXXXX 转义——解析后断言）
            string noteJson = session.BuildNoteJson();
            using (JsonDocument nd = JsonDocument.Parse(noteJson))
            {
                JsonElement ntasks;
                Assert.True(nd.RootElement.TryGetProperty("tasks", out ntasks));
                Assert.Equal(2, ntasks.GetArrayLength());
                Assert.Equal("任务A", ntasks[0].GetString());
                Assert.Equal("任务B", ntasks[1].GetString());
            }
        }

        /// <summary>
        /// sleep 内置工具——同轮帧等待（2026-11-04 语义）：不登记延迟队列，工具单保持未闭合，到点闭合后本轮继续。
        /// </summary>
        [Fact]
        public void Sleep_ExecuteTool_WaitsInSameRound()
        {
            CH4.DelayQueue.ResetForTest();
            MockLlm llm = new MockLlm();
            string tc = "[{\"id\":\"s1\",\"function\":{\"name\":\"sleep\",\"arguments\":\"{\\\"seconds\\\":3}\"}}]";
            llm.ToolCallsQueue.Enqueue(tc);
            CH4.ChatSession session = CreateSession(llm, new ToolSpec[]
            {
                new ToolSpec("Note", "Note 任务追踪", "{}"),
                new ToolSpec("sleep", "定时唤醒", "{}")
            });
            session.SetCatKey("test-session");
            session.PostUserMessage("3 秒后再动手");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            // 帧等待不产生延迟队列条目（DelayQueue 的 sleep 通道已退役）
            Assert.Empty(CH4.DelayQueue.List("test-session"));
            bool sawWait = false;
            LlmMessage[] all = session.Context.GetMessages();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                if (all[i].Role == LlmRole.Tool && all[i].Content != null && all[i].Content.Contains("等待 | 3 秒——到点即回本批"))
                {
                    sawWait = true;
                }
            }
            Assert.True(sawWait);
            CH4.DelayQueue.ResetForTest();
        }

        /// <summary>
        /// sleep 参数面——超上限拒绝（1..99 秒；错误可见性：ERR| 文本进工具结果）。
        /// </summary>
        [Fact]
        public void Sleep_ExecuteTool_RejectsBadArgs()
        {
            CH4.DelayQueue.ResetForTest();
            CH4.DelayQueue.NowProvider = delegate () { return 6000000; };
            MockLlm llm = new MockLlm();
            string tc = "[{\"id\":\"s2\",\"function\":{\"name\":\"sleep\",\"arguments\":\"{\\\"seconds\\\":7200}\"}}]";
            llm.ToolCallsQueue.Enqueue(tc);
            CH4.ChatSession session = CreateSession(llm, new ToolSpec[]
            {
                new ToolSpec("Note", "Note 任务追踪", "{}"),
                new ToolSpec("sleep", "定时唤醒", "{}")
            });
            session.SetCatKey("test-session");
            session.PostUserMessage("睡两小时");
            PumpUntilIdle(session);
            Assert.Empty(CH4.DelayQueue.List("test-session"));
            bool sawError = false;
            LlmMessage[] all = session.Context.GetMessages();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                if (all[i].Role == LlmRole.Tool && all[i].Content != null && all[i].Content.Contains("ERR|BAD_ARGS"))
                {
                    sawError = true;
                }
            }
            Assert.True(sawError);
            CH4.DelayQueue.ResetForTest();
        }

        /// <summary>
        /// timer 内置工具——登记延迟指令注入（排程语义）：source=timer + loop 标记 + 循环时长落表。
        /// </summary>
        [Fact]
        public void Timer_ExecuteTool_RegistersLoopEntry()
        {
            CH4.DelayQueue.ResetForTest();
            CH4.DelayQueue.NowProvider = delegate () { return 9000000; };
            MockLlm llm = new MockLlm();
            string tc = "[{\"id\":\"m1\",\"function\":{\"name\":\"timer\",\"arguments\":\"{\\\"content\\\":\\\"检查构建进度\\\",\\\"minutes\\\":5,\\\"loop\\\":true}\"}}]";
            llm.ToolCallsQueue.Enqueue(tc);
            CH4.ChatSession session = CreateSession(llm, new ToolSpec[]
            {
                new ToolSpec("Note", "Note 任务追踪", "{}"),
                new ToolSpec("timer", "定时注入", "{}")
            });
            session.SetCatKey("test-session");
            session.PostUserMessage("五分钟后自查");
            PumpUntilIdle(session);
            CH4.DelayEntry[] entries = CH4.DelayQueue.List("test-session");
            Assert.Single(entries);
            Assert.Equal("timer", entries[0].Source);
            Assert.Equal("检查构建进度", entries[0].Content);
            Assert.Equal(9000000 + 300000, entries[0].DueAt);
            Assert.True(entries[0].Loop);
            Assert.Equal(300000, entries[0].IntervalMs);
            CH4.DelayQueue.ResetForTest();
        }

        /// <summary>
        /// timer 参数面——内容缺失拒绝（content 必填；错误可见性：ERR| 文本进工具结果）。
        /// </summary>
        [Fact]
        public void Timer_ExecuteTool_RejectsMissingContent()
        {
            CH4.DelayQueue.ResetForTest();
            CH4.DelayQueue.NowProvider = delegate () { return 9100000; };
            MockLlm llm = new MockLlm();
            string tc = "[{\"id\":\"m2\",\"function\":{\"name\":\"timer\",\"arguments\":\"{\\\"minutes\\\":5}\"}}]";
            llm.ToolCallsQueue.Enqueue(tc);
            CH4.ChatSession session = CreateSession(llm, new ToolSpec[]
            {
                new ToolSpec("Note", "Note 任务追踪", "{}"),
                new ToolSpec("timer", "定时注入", "{}")
            });
            session.SetCatKey("test-session");
            session.PostUserMessage("五分钟后自查");
            PumpUntilIdle(session);
            Assert.Empty(CH4.DelayQueue.List("test-session"));
            bool sawError = false;
            LlmMessage[] all = session.Context.GetMessages();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                if (all[i].Role == LlmRole.Tool && all[i].Content != null && all[i].Content.Contains("ERR|BAD_ARGS"))
                {
                    sawError = true;
                }
            }
            Assert.True(sawError);
            CH4.DelayQueue.ResetForTest();
        }

        /// <summary>
        /// sleep 同轮等待——不再产生「sleep 作废」系统注入（延迟队列通道已退役）；等待期间主输入排队，不打断本轮。
        /// </summary>
        [Fact]
        public void Sleep_WaitsInSameRound_NoCancelInjection()
        {
            CH4.DelayQueue.ResetForTest();
            MockLlm llm = new MockLlm();
            string tc = "[{\"id\":\"s3\",\"function\":{\"name\":\"sleep\",\"arguments\":\"{\\\"seconds\\\":2}\"}}]";
            llm.ToolCallsQueue.Enqueue(tc);
            CH4.ChatSession session = CreateSession(llm, new ToolSpec[]
            {
                new ToolSpec("Note", "Note 任务追踪", "{}"),
                new ToolSpec("sleep", "定时唤醒", "{}")
            });
            session.SetCatKey("test-session");
            session.PostUserMessage("2 秒后叫我");
            PumpUntilIdle(session);
            Assert.Empty(CH4.DelayQueue.List("test-session"));
            session.PostUserMessage("回来了");
            PumpUntilIdle(session);
            LlmMessage[] all = session.Context.GetMessages();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                if (all[i].Role == LlmRole.User && all[i].Content != null)
                {
                    Assert.DoesNotContain("sleep 作废", all[i].Content);
                }
            }
            Assert.Empty(CH4.DelayQueue.List("test-session"));
            CH4.DelayQueue.ResetForTest();
        }

        /// <summary>
        /// sleep 打断（A327）——允许对话注入（userInjectIdleOnly=false）时，等待期间到达的人工消息即刻闭合等待，
        /// 消息随本批收口进入上下文并同轮续跑（Group 回执走 user 通道，即走此路）。
        /// </summary>
        [Fact]
        public void Sleep_InterruptedByUserInject_WhenIdleOnlyOff()
        {
            CH4.DelayQueue.ResetForTest();
            MockLlm llm = new MockLlm();
            string tc = "[{\"id\":\"s5\",\"function\":{\"name\":\"sleep\",\"arguments\":\"{\\\"seconds\\\":5}\"}}]";
            llm.ToolCallsQueue.Enqueue(tc);
            CH4.ChatSession session = CreateSession(llm, new ToolSpec[]
            {
                new ToolSpec("Note", "Note 任务追踪", "{}"),
                new ToolSpec("sleep", "定时唤醒", "{}")
            });
            session.SetCatKey("test-session");
            session.PostUserMessage("等我 5 秒");
            for (int i = 0; i < 3; i = i + 1)
            {
                session.Pump();
                Thread.Sleep(5);
            }
            Assert.False(session.IsIdle);
            session.PostUserMessage("回来了");
            for (int i = 0; i < 400; i = i + 1)
            {
                session.Pump();
                Thread.Sleep(5);
                if (session.IsIdle && llm.CallCount >= 2 && i >= 2)
                {
                    break;
                }
            }
            bool sawInterrupt = false;
            bool sawUser = false;
            LlmMessage[] all = session.Context.GetMessages();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                if (all[i].Role == LlmRole.Tool && all[i].Content != null && all[i].Content.Contains("对话注入打断"))
                {
                    sawInterrupt = true;
                }
                if (all[i].Role == LlmRole.User && all[i].Content != null && all[i].Content.Contains("回来了"))
                {
                    sawUser = true;
                }
            }
            Assert.True(sawInterrupt);
            Assert.True(sawUser);
            Assert.Equal(2, llm.CallCount);
            CH4.DelayQueue.ResetForTest();
        }

        /// <summary>
        /// sleep 不打断（A327 反例）——空闲注入开启（userInjectIdleOnly=true）时，等待期间的人工消息排队到轮收尾后另起一轮，
        /// 等待照常到点闭合（开关即唯一判据）。
        /// </summary>
        [Fact]
        public void Sleep_NotInterrupted_WhenIdleOnlyOn()
        {
            CH4.DelayQueue.ResetForTest();
            MockLlm llm = new MockLlm();
            string tc = "[{\"id\":\"s6\",\"function\":{\"name\":\"sleep\",\"arguments\":\"{\\\"seconds\\\":2}\"}}]";
            llm.ToolCallsQueue.Enqueue(tc);
            CH4.ChatSession session = CreateSession(llm, new ToolSpec[]
            {
                new ToolSpec("Note", "Note 任务追踪", "{}"),
                new ToolSpec("sleep", "定时唤醒", "{}")
            });
            session.SetCatKey("test-session");
            session.SetUserInjectIdleOnly(true);
            session.PostUserMessage("等我 2 秒");
            for (int i = 0; i < 3; i = i + 1)
            {
                session.Pump();
                Thread.Sleep(5);
            }
            Assert.False(session.IsIdle);
            session.PostUserMessage("回来了");
            for (int i = 0; i < 400; i = i + 1)
            {
                session.Pump();
                Thread.Sleep(5);
                if (session.IsIdle && llm.CallCount >= 3 && i >= 2)
                {
                    break;
                }
            }
            bool sawInterrupt = false;
            bool sawUser = false;
            LlmMessage[] all = session.Context.GetMessages();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                if (all[i].Role == LlmRole.Tool && all[i].Content != null && all[i].Content.Contains("对话注入打断"))
                {
                    sawInterrupt = true;
                }
                if (all[i].Role == LlmRole.User && all[i].Content != null && all[i].Content.Contains("回来了"))
                {
                    sawUser = true;
                }
            }
            Assert.False(sawInterrupt);
            Assert.True(sawUser);
            Assert.Equal(3, llm.CallCount);
            CH4.DelayQueue.ResetForTest();
        }

        /// <summary>
        /// roundsum done 语义——sleep 已改同轮帧等待（不再工具主动 done），两轮均按 stream 自然收尾。
        /// </summary>
        [Fact]
        public void RoundSum_CarriesDoneKind()
        {
            CH4.DelayQueue.ResetForTest();
            MockLlm llm = new MockLlm();
            string tc = "[{\"id\":\"s4\",\"function\":{\"name\":\"sleep\",\"arguments\":\"{\\\"seconds\\\":2}\"}}]";
            llm.ToolCallsQueue.Enqueue(tc);
            CH4.ChatSession session = CreateSession(llm, new ToolSpec[]
            {
                new ToolSpec("Note", "Note 任务追踪", "{}"),
                new ToolSpec("sleep", "定时唤醒", "{}")
            });
            session.SetCatKey("test-session");
            session.PostUserMessage("半分钟后叫我");
            PumpUntilIdle(session);
            Assert.Contains("\"done\":\"stream\"", LastRoundSumPayload(session));
            // 第二轮（纯文本）——同样自然收尾
            session.PostUserMessage("普通一轮");
            PumpUntilIdle(session);
            Assert.Contains("\"done\":\"stream\"", LastRoundSumPayload(session));
            CH4.DelayQueue.ResetForTest();
        }

        /// <summary>取最近一个 roundsum 块载荷——done 语义断言用</summary>
        /// <param name="session">会话</param>
        /// <returns>载荷 JSON（无块=空串）</returns>
        private static string LastRoundSumPayload(CH4.ChatSession session)
        {
            CH4.ViewBlock[] blocks = session.GetViewBlocks();
            for (int i = blocks.Length - 1; i >= 0; i = i - 1)
            {
                if (blocks[i].RenderType == "roundsum")
                {
                    return blocks[i].Payload;
                }
            }
            return "";
        }

        /// <summary>
        /// 提取工具结果文本——按 ToolCallId 从会话上下文取 tool 消息内容（工具批执行落点）。
        /// </summary>
        /// <param name="session">会话</param>
        /// <param name="callId">工具调用 ID</param>
        /// <returns>结果文本（无则空串）</returns>
        private static string GetToolResultText(CH4.ChatSession session, string callId)
        {
            LlmMessage[] all = session.Context.GetMessages();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                if (all[i].Role == LlmRole.Tool && all[i].ToolCallId == callId)
                {
                    return all[i].Content ?? "";
                }
            }
            return "";
        }

        /// <summary>
        /// Note 进度计数——set 三条：待完成 = 总数 - 已完成（已完成 + 待完成 == 总数）。
        /// </summary>
        [Fact]
        public void Note_ProgressCounts_DonePlusTodoEqualsTotal()
        {
            MockLlm llm = new MockLlm();
            llm.ToolCallsQueue.Enqueue("[{\"id\":\"n1\",\"function\":{\"name\":\"Note\",\"arguments\":\"{\\\"action\\\":\\\"set\\\",\\\"content\\\":[\\\"任务A\\\",\\\"任务B\\\",\\\"任务C\\\"]}\"}}]");
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("创建三条计划");
            PumpUntilIdle(session);
            string text = GetToolResultText(session, "n1");
            Assert.Contains("第1/3条  已完成0  待完成3", text);
        }

        /// <summary>
        /// Note 进度计数——推进一条后仍自洽（已完成 1 + 待完成 2 == 总数 3）。
        /// </summary>
        [Fact]
        public void Note_ProgressCounts_AfterAdvance()
        {
            MockLlm llm = new MockLlm();
            llm.ToolCallsQueue.Enqueue("[{\"id\":\"n1\",\"function\":{\"name\":\"Note\",\"arguments\":\"{\\\"action\\\":\\\"set\\\",\\\"content\\\":[\\\"任务A\\\",\\\"任务B\\\",\\\"任务C\\\"]}\"}}]");
            llm.ToolCallsQueue.Enqueue("[{\"id\":\"n2\",\"function\":{\"name\":\"Note\",\"arguments\":\"{}\"}}]");
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("创建并推进");
            PumpUntilIdle(session);
            Assert.Contains("第2/3条  已完成1  待完成2", GetToolResultText(session, "n2"));
        }

        /// <summary>
        /// Note 末条提示——单条计划：首条即最后一条（提示按当前索引判定，不随待完成口径漂移）。
        /// </summary>
        [Fact]
        public void Note_LastItemHint_OnSingleTaskPlan()
        {
            MockLlm llm = new MockLlm();
            llm.ToolCallsQueue.Enqueue("[{\"id\":\"n1\",\"function\":{\"name\":\"Note\",\"arguments\":\"{\\\"action\\\":\\\"set\\\",\\\"content\\\":[\\\"唯一任务\\\"]}\"}}]");
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("创建单条计划");
            PumpUntilIdle(session);
            string text = GetToolResultText(session, "n1");
            Assert.Contains("第1/1条  已完成0  待完成1", text);
            Assert.Contains("已是最后一条需求", text);
        }
        /// <summary>
        /// A129 参数形态——Note.content 非原生字符串数组一律 ERR|BAD_ARGS（旧字符串形态 / 非字符串元素 / 空串元素 / 空数组四态出声，
        /// 退役静默回落与静默截断——参数面零容忍）。
        /// </summary>
        [Fact]
        public void Note_ContentShape_RejectsNonArray()
        {
            MockLlm llm = new MockLlm();
            llm.ToolCallsQueue.Enqueue("[{\"id\":\"s1\",\"function\":{\"name\":\"Note\",\"arguments\":\"{\\\"action\\\":\\\"set\\\",\\\"content\\\":\\\"任务A\\\\n任务B\\\"}\"}}]");
            llm.ToolCallsQueue.Enqueue("[{\"id\":\"s2\",\"function\":{\"name\":\"Note\",\"arguments\":\"{\\\"action\\\":\\\"set\\\",\\\"content\\\":[\\\"任务A\\\",1]}\"}}]");
            llm.ToolCallsQueue.Enqueue("[{\"id\":\"s3\",\"function\":{\"name\":\"Note\",\"arguments\":\"{\\\"action\\\":\\\"set\\\",\\\"content\\\":[\\\"任务A\\\",\\\"\\\"]}\"}}]");
            llm.ToolCallsQueue.Enqueue("[{\"id\":\"s4\",\"function\":{\"name\":\"Note\",\"arguments\":\"{\\\"action\\\":\\\"set\\\",\\\"content\\\":[]}\"}}]");
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("形态校验");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            Assert.Contains("ERR|BAD_ARGS", GetToolResultText(session, "s1"));
            Assert.Contains("ERR|BAD_ARGS", GetToolResultText(session, "s2"));
            Assert.Contains("ERR|BAD_ARGS", GetToolResultText(session, "s3"));
            Assert.Contains("ERR|BAD_ARGS", GetToolResultText(session, "s4"));
            // 四态全拒——计划未写入（零静默回落）
            using (JsonDocument nd = JsonDocument.Parse(session.BuildNoteJson()))
            {
                JsonElement tasks;
                Assert.True(nd.RootElement.TryGetProperty("tasks", out tasks));
                Assert.Equal(0, tasks.GetArrayLength());
            }
        }

        /// <summary>
        /// Note 手动添加 + 启动——NoteAdd 入列 → NoteStart 触发 LLM 轮次。
        /// </summary>
        [Fact]
        public void Note_ManualAddAndStart()
        {
            MockLlm llm = new MockLlm();
            llm.ReplyText = "开始执行";
            CH4.ChatSession session = CreateSession(llm);
            session.NoteAdd("第一步");
            session.NoteAdd("第二步");
            string before = session.BuildNoteJson();
            using (JsonDocument bd = JsonDocument.Parse(before))
            {
                JsonElement btasks;
                Assert.True(bd.RootElement.TryGetProperty("tasks", out btasks));
                Assert.Equal(2, btasks.GetArrayLength());
                Assert.Equal("第一步", btasks[0].GetString());
                Assert.Equal("第二步", btasks[1].GetString());
            }
            // NoteStart → 以 user 名义推计划 → LLM 回复
            session.NoteStart();
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            Assert.Contains("开始执行", GetLastAssistantText(session));
        }

        /// <summary>
        /// 工具批收敛——连续多轮工具调用后纯文本结束：轮次 ≤ 收敛上限且回 Idle。
        /// </summary>
        [Fact]
        public void ToolBatch_ConvergesAfterToolRounds()
        {
            MockLlm llm = new MockLlm();
            // 三轮内置 time 工具调用（不进 OA——会话内直执）+ 最后文本收尾
            llm.ToolCallsQueue.Enqueue("[{\"id\":\"t1\",\"function\":{\"name\":\"time\",\"arguments\":\"{}\"}}]");
            llm.ToolCallsQueue.Enqueue("[{\"id\":\"t2\",\"function\":{\"name\":\"time\",\"arguments\":\"{}\"}}]");
            llm.ToolCallsQueue.Enqueue("[{\"id\":\"t3\",\"function\":{\"name\":\"time\",\"arguments\":\"{}\"}}]");
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("连续调用工具");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            // 4 次 LLM 调用（3 轮工具 + 1 轮收尾文本）——收敛
            Assert.Equal(4, llm.CallCount);
        }

        /// <summary>
        /// 工具批并发编号——同批多工具 toolcard payload 携带 toolIndex/toolTotal（前端 [n/m] 前缀数据源）。
        /// </summary>
        [Fact]
        public void ToolBatch_ConcurrentIndexAndTotal()
        {
            MockLlm llm = new MockLlm();
            // 同批两个内置 time 调用（并发——不依赖 OA 消费者）
            string tc = "[{\"id\":\"t1\",\"function\":{\"name\":\"time\",\"arguments\":\"{}\"}},{\"id\":\"t2\",\"function\":{\"name\":\"time\",\"arguments\":\"{}\"}}]";
            llm.ToolCallsQueue.Enqueue(tc);
            MockHost host = new MockHost();
            CH4.ChatSession session = CreateSession(llm);
            session.AttachHost(host);
            host.Session = session;
            session.PostUserMessage("并发调用工具");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            List<string> cards = host.ViewEvents["toolcard"];
            Assert.True(cards.Count >= 2);
            // 期三——每次推送都是一条新事件（无原位替换）；批内编号按工具逐条核
            bool sawFirst = false;
            bool sawSecond = false;
            for (int i = 0; i < cards.Count; i = i + 1)
            {
                using (JsonDocument d = JsonDocument.Parse(cards[i]))
                {
                    Assert.Equal(2, d.RootElement.GetProperty("toolTotal").GetInt32());
                    int idx = d.RootElement.GetProperty("toolIndex").GetInt32();
                    if (idx == 1) { sawFirst = true; }
                    if (idx == 2) { sawSecond = true; }
                }
            }
            Assert.True(sawFirst);
            Assert.True(sawSecond);
        }

        /// <summary>
        /// 工具卡两段式——LLM 输出工具即推"进行中"卡（无 result 字段），完成时以同序号 replaceSeq 原位替换为完整卡。
        /// </summary>
        [Fact]
        public void ToolCard_PendingThenReplacedOnComplete()
        {
            MockLlm llm = new MockLlm();
            string tc = "[{\"id\":\"p1\",\"function\":{\"name\":\"time\",\"arguments\":\"{}\"}}]";
            llm.ToolCallsQueue.Enqueue(tc);
            MockHost host = new MockHost();
            ToolSpec[] declared = new ToolSpec[]
            {
                new ToolSpec("time", "当前时间", "{}")
            };
            CH4.ChatSession session = CreateSession(llm, declared);
            session.AttachHost(host);
            host.Session = session;
            session.PostUserMessage("调用工具");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            List<string> cards = host.ViewEvents["toolcard"];
            Assert.True(cards.Count >= 1);
            // A162 状态同步——同键在未取走期间合并（先行态被终态覆盖）：末条即最终卡（含 result）
            using (JsonDocument d = JsonDocument.Parse(cards[cards.Count - 1]))
            {
                JsonElement res;
                Assert.True(d.RootElement.TryGetProperty("result", out res));
                Assert.Equal("time", d.RootElement.GetProperty("name").GetString());
                Assert.Equal(1, d.RootElement.GetProperty("toolIndex").GetInt32());
                Assert.Equal(1, d.RootElement.GetProperty("toolTotal").GetInt32());
            }
            List<string> cardOps = host.ViewOps["toolcard"];
            Assert.Equal("append", cardOps[0]);
        }

        /// <summary>
        /// 工具区中止（A196 更新）——工具批进行中（OA 工具无消费者）Pause 中止：临时区写空态（未完成工具不在面板残留）；
        /// 中止前在途卡如实可见（toolrun 段带该工具）。
        /// </summary>
        [Fact]
        public void ToolCard_PendingAbortedOnPause()
        {
            MockLlm llm = new MockLlm();
            // OA 工具（声明面内、非内置）——无消费者 → 工具批停留进行中
            string tc = "[{\"id\":\"o1\",\"function\":{\"name\":\"text-read\",\"arguments\":\"{\\\"path\\\":\\\"a.txt\\\"}\"}}]";
            llm.ToolCallsQueue.Enqueue(tc);
            MockHost host = new MockHost();
            ToolSpec[] declared = new ToolSpec[]
            {
                new ToolSpec("text-read", "读取文本", "{}")
            };
            CH4.ChatSession session = CreateSession(llm, declared);
            session.AttachHost(host);
            host.Session = session;
            session.PostUserMessage("调用 OA 工具");
            // 泵帧直到先行卡出现（工具批已进入）
            for (int i = 0; i < 200; i = i + 1)
            {
                session.Pump();
                Thread.Sleep(5);
                List<string> cardsNow;
                if (host.ViewEvents.TryGetValue("toolrun", out cardsNow) && cardsNow.Count > 0)
                {
                    break;
                }
            }
            Assert.False(session.IsIdle);
            // 中止——临时区覆盖为空态（面板清空；未落前文的内容随覆盖消失）
            session.Pause();
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            Assert.Equal("empty", host.LastLiveType);
            Assert.Equal("", host.LastLiveContext);
            Assert.Contains("text-read", string.Join("|", host.ViewEvents["toolrun"]));
        }        /// <summary>
                 /// 分批调度的观测面（A165 改写 · A196 更新）——声明序 [text-write(order 1), host-flows(order -1)]：
                 /// 执行按 order 分桶（host-flows 批先、text-write 批后，OA 无消费者 → 停留）。
                 /// 持久卡按声明序待整批收口落位；临时区只列**未完成**工具——已完成者当帧移出面板。
                 /// </summary>
        [Fact]
        public void ToolCard_PendingBatchStaysInLivePanel()
        {
            MockLlm llm = new MockLlm();
            string tc = "[{\"id\":\"e1\",\"function\":{\"name\":\"text-write\",\"arguments\":\"{}\"}},{\"id\":\"e2\",\"function\":{\"name\":\"host-flows\",\"arguments\":\"{}\"}}]";
            llm.ToolCallsQueue.Enqueue(tc);
            MockHost host = new MockHost();
            ToolSpec[] declared = new ToolSpec[]
            {
                        new ToolSpec("text-write", "写入文本", "{}"),
                        new ToolSpec("host-flows", "Flow 清单", "{}")
            };
            CH4.ChatSession session = CreateSession(llm, declared);
            session.AttachHost(host);
            host.Session = session;
            session.PostUserMessage("逐条回填验证");
            // 泵到 text-write 批派发（前批 host-flows 已完成）
            for (int i = 0; i < 200; i = i + 1)
            {
                session.Pump();
                Thread.Sleep(5);
                List<string> now;
                if (host.ViewEvents.TryGetValue("toolrun", out now) && now.Count >= 1
                    && now[now.Count - 1].Contains("text-write", StringComparison.Ordinal))
                {
                    break;
                }
            }
            // 批未收口——后批 OA 工具仍在进行中
            Assert.False(session.IsIdle);
            // 批未收口——持久区尚无工具卡（持久卡按声明序待整批收口落位）
            Assert.False(host.ViewEvents.ContainsKey("toolcard"));
            List<string> pendingCards = host.ViewEvents["toolrun"];
            Assert.True(pendingCards.Count >= 1);
            // A196——live 是状态投影（整段覆盖，无历史帧）：此刻只列**未完成**工具
            // （host-flows 已完成即移出面板；其持久卡按声明序待整批收口落位）
            Assert.Equal("toolrun", host.LastLiveType);
            Assert.Contains("text-write", host.LastLiveContext, StringComparison.Ordinal);
            Assert.DoesNotContain("host-flows", host.LastLiveContext, StringComparison.Ordinal);
        }
        /// <summary>
        /// 授权面实时查询——AuthorizedToolNamesProvider 压过会话注入面：声明面含 text-read 而提供者未放行 → 调用被 TOOL_FORBIDDEN 拒（design-ch4-tools §三·十一）。
        /// </summary>
        [Fact]
        public void ToolAuth_ProviderOverridesDeclaredFace()
        {
            MockLlm llm = new MockLlm();
            string tc = "[{\"id\":\"a1\",\"function\":{\"name\":\"text-read\",\"arguments\":\"{\\\"path\\\":\\\"a.txt\\\"}\"}}]";
            llm.ToolCallsQueue.Enqueue(tc);
            MockHost host = new MockHost();
            ToolSpec[] declared = new ToolSpec[]
            {
                        new ToolSpec("text-read", "读取文本", "{}")
            };
            CH4.ChatSession session = CreateSession(llm, declared);
            session.AttachHost(host);
            host.Session = session;
            CH4.ChatSession.AuthorizedToolNamesProvider = delegate (string catKey) { return new string[] { "Note" }; };
            try
            {
                session.PostUserMessage("授权面外工具调用");
                PumpUntilIdle(session);
            }
            finally
            {
                CH4.ChatSession.AuthorizedToolNamesProvider = null;
            }
            List<string> cards;
            Assert.True(host.ViewEvents.TryGetValue("toolcard", out cards));
            Assert.Contains("TOOL_FORBIDDEN", string.Join("|", cards));
        }

        /// <summary>
        /// E3 usage 转发——LLM 流带 Usage 事件 → 宿主收到 state 段 tokens（A165：原 control 段 usage 块整体并入 state 段）且累计整轮（覆盖式）。
        /// </summary>
        [Fact]
        public void Usage_ForwardedToHost()
        {
            MockLlm llm = new MockLlm();
            llm.ReplyText = "token 统计";
            llm.UsageJson = "{\"prompt\":100,\"completion\":20,\"cacheHit\":30}";
            MockHost host = new MockHost();
            CH4.ChatSession session = CreateSession(llm);
            session.AttachHost(host);
            host.Session = session;
            session.PostUserMessage("统计一下");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            List<string> tokenStates;
            Assert.True(host.ViewEvents.TryGetValue("tokens", out tokenStates));
            Assert.True(tokenStates.Count > 0);
            // 末条即最新累计（整轮累计值；覆盖式）
            using (JsonDocument ud = JsonDocument.Parse(tokenStates[tokenStates.Count - 1]))
            {
                JsonElement data = ud.RootElement;
                Assert.Equal(100, data.GetProperty("prompt").GetInt64());
                Assert.Equal(20, data.GetProperty("completion").GetInt64());
                Assert.Equal(30, data.GetProperty("cacheHit").GetInt64());
                // A115——前文条数随 state 下发（前端「前文 N 条」实时化数据源）
                Assert.True(data.GetProperty("count").GetInt64() > 0);
            }
        }

        /// <summary>
        /// A202 落盘时机——会话元数据面在每次 API 请求结算后落盘（轮末不写）。
        /// 本例单请求轮：盘上应留请求边界态（两级 token + 前文长度 + 请求次数），
        /// 而内存轮级值已被 CloseRound 归零——验证「结算后清零、盘上不丢」。
        /// </summary>
        [Fact]
        public void Meta_PersistedOnEveryRequest()
        {
            string metaPath = Path.Combine(Path.GetTempPath(), "cat4a202_" + Guid.NewGuid().ToString("N") + ".session.json");
            Func<string, string> prev = CH4.ChatSession.SessionMetaPathProvider;
            try
            {
                CH4.ChatSession.SessionMetaPathProvider = delegate (string key) { return metaPath; };
                MockLlm llm = new MockLlm();
                llm.ReplyText = "落盘验证";
                llm.UsageJson = "{\"prompt\":900,\"completion\":120,\"cacheHit\":700}";
                CH4.ChatSession session = CreateSession(llm);
                session.LoadMeta();
                session.PostUserMessage("落个盘");
                PumpUntilIdle(session);
                Assert.True(session.IsIdle);
                CH4.SessionMeta saved = new CH4.SessionMetaStore(metaPath).Load();
                Assert.NotNull(saved);
                // 请求边界态——两级 token + 请求级前文长度 + 本轮计数
                Assert.Equal(900L, saved.RoundTokens.Prompt);
                Assert.Equal(120L, saved.RoundTokens.Completion);
                Assert.Equal(700L, saved.RoundTokens.CacheHit);
                Assert.Equal(900L, saved.ContextTokens);
                Assert.Equal(900L, saved.SessionTokens.Prompt);
                Assert.Equal(1L, saved.RoundRequests);
                Assert.True(saved.RoundElapsedMs >= 0);
                // 前文条数——请求边界态：usage 帧先于回复文本到达，此刻上下文仅含本轮 user 一条
                // （盘值 = 边界快照，不等于轮末终值——轮末终值只进视图层 roundsum 块）
                Assert.Equal(1L, saved.ContextCount);
            }
            finally
            {
                CH4.ChatSession.SessionMetaPathProvider = prev;
            }
        }

        /// <summary>
        /// roundsum 轮末统计——CloseRound 推送（Token 消耗 + 工具次数 + 请求次数 + 总耗时 + 六态用时；chatdone stats 带 context 前文长度）。
        /// </summary>
        [Fact]
        public void RoundSum_PushedOnCloseRound()
        {
            MockLlm llm = new MockLlm();
            llm.ReplyText = "统计完成";
            llm.UsageJson = "{\"prompt\":1000,\"completion\":200,\"cacheHit\":800}";
            MockHost host = new MockHost();
            CH4.ChatSession session = CreateSession(llm);
            session.AttachHost(host);
            host.Session = session;
            session.PostUserMessage("算一下");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            // roundsum 视图事件存在
            List<string> rsEvents;
            Assert.True(host.ViewEvents.TryGetValue("roundsum", out rsEvents));
            Assert.True(rsEvents.Count >= 1);
            // 载荷结构——type=roundsum + data 字段 + phases 六态（idle 不计时不入载荷——A59）
            using (JsonDocument rd = JsonDocument.Parse(rsEvents[0]))
            {
                JsonElement root = rd.RootElement;
                Assert.Equal("roundsum", root.GetProperty("type").GetString());
                JsonElement data = root.GetProperty("data");
                Assert.Equal(1000, data.GetProperty("prompt").GetInt64());
                Assert.Equal(200, data.GetProperty("completion").GetInt64());
                Assert.Equal(800, data.GetProperty("cacheHit").GetInt64());
                Assert.Equal(200, data.GetProperty("miss").GetInt64());
                Assert.True(data.GetProperty("elapsedMs").GetInt64() >= 0);
                // 请求次数——纯文本轮 = 1 次 API 请求（link 段数）
                Assert.Equal(1, data.GetProperty("requests").GetInt64());
                JsonElement phases = data.GetProperty("phases");
                Assert.True(phases.GetProperty("wait").GetInt64() >= 0);
                Assert.True(phases.GetProperty("link").GetInt64() >= 0);
                Assert.True(phases.GetProperty("think").GetInt64() >= 0);
                Assert.True(phases.GetProperty("tool").GetInt64() >= 0);
                Assert.True(phases.GetProperty("run").GetInt64() >= 0);
                Assert.True(phases.GetProperty("reply").GetInt64() >= 0);
                // idle 不计时——不入 roundsum 载荷（A59 六态拼接）
                JsonElement idlePhase;
                Assert.False(phases.TryGetProperty("idle", out idlePhase));
            }
            // state 段 tokens 带 context（单次前文长度——非累计；A165：原 chatdone stats 并入 state 段）
            List<string> tokenStates;
            Assert.True(host.ViewEvents.TryGetValue("tokens", out tokenStates));
            Assert.True(tokenStates.Count > 0);
            using (JsonDocument dd = JsonDocument.Parse(tokenStates[tokenStates.Count - 1]))
            {
                Assert.Equal(1000, dd.RootElement.GetProperty("context").GetInt64());
            }
        }

        /// <summary>
        /// S2 错误隔离——单次瞬态错误后会话就地修复：错误文本不入上下文，上下文保持断点（用户消息保留），相位回 Idle。
        /// 语义：错误可见（Error 事件已推前端），但不污染 ChatContext——断点续传的干净前提。
        /// 说明：Runtime 重试（TRANSPORT/5xx/429 最多 3 次）在 DeepSeekLlmRuntime 内部完成，对会话层透明；
        /// 本测试验证的是 Runtime 重试耗尽后最终 Error 的会话层处理——错误文本绝不入上下文。
        /// </summary>
        [Fact]
        public void S2_Error_NotPollutingContext_KeepsBreakpoint()
        {
            MockLlm llm = new MockLlm();
            llm.FailTimes = 1;
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("断点测试");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            // 错误文本绝不入上下文（GetLastAssistantText 不含 ERR|）
            Assert.Equal("", GetLastAssistantText(session));
            // 上下文保持断点——用户消息保留（不回退到上一节点——CH2 语义淘汰）
            LlmMessage[] all = session.Context.GetMessages();
            bool hasUser = false;
            for (int i = 0; i < all.Length; i = i + 1)
            {
                if (all[i].Role == LlmRole.User && all[i].Content == "断点测试")
                {
                    hasUser = true;
                }
            }
            Assert.True(hasUser);
        }

        /// <summary>
        /// S2 断点续传——错误隔离后用户重发：上下文无错误残留，新一轮正常回复（断点续传的用户触发面）。
        /// </summary>
        [Fact]
        public void S2_Error_ThenResend_RecoversClean()
        {
            MockLlm llm = new MockLlm();
            llm.FailTimes = 1;
            llm.ReplyText = "重试成功回复";
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("重试测试");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            // 第一次失败——错误隔离中止（无回复文本）
            Assert.Equal("", GetLastAssistantText(session));
            // 用户重发（断点续传）——第二次调用成功
            session.PostUserMessage("再试一次");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            Assert.Contains("重试成功回复", GetLastAssistantText(session));
            // 全程上下文无错误文本
            LlmMessage[] all = session.Context.GetMessages();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                Assert.DoesNotContain("ERR|", all[i].Content == null ? "" : all[i].Content);
            }
        }

        /// <summary>
        /// A55 错误可见性——错误中止：视图层落盘 error 块（单块——统一出口 AbortRoundError；实时面不双推）。
        /// </summary>
        [Fact]
        public void A55_Error_PersistsSingleErrorBlock()
        {
            MockLlm llm = new MockLlm();
            llm.FailTimes = 1;
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("错误落盘测试");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            CH4.ViewBlock[] blocks = session.ViewStore.GetBlocks();
            int errorCount = 0;
            string payload = "";
            for (int i = 0; i < blocks.Length; i = i + 1)
            {
                if (blocks[i].RenderType == "error")
                {
                    errorCount = errorCount + 1;
                    payload = blocks[i].Payload;
                }
            }
            Assert.Equal(1, errorCount);
            Assert.Contains("ERR|", payload);
        }

        /// <summary>
        /// A55 重试落盘——同一重试序列原位更新（一块不堆叠）；恢复后终态 resolved。
        /// </summary>
        [Fact]
        public void A55_Retry_PersistsSingleRetryBlock()
        {
            MockLlm llm = new MockLlm();
            llm.EmitRetry = true;
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("重试落盘测试");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            CH4.ViewBlock[] blocks = session.ViewStore.GetBlocks();
            int retryCount = 0;
            string payload = "";
            for (int i = 0; i < blocks.Length; i = i + 1)
            {
                if (blocks[i].RenderType == "retry")
                {
                    retryCount = retryCount + 1;
                    payload = blocks[i].Payload;
                }
            }
            // 期三——只增不改：retrying 与 resolved 各落一块（莎 2026-10-02 红线）
            Assert.Equal(2, retryCount);
            Assert.Contains("resolved", payload);
        }

        /// <summary>
        /// S2 §8.4 重试前端可见性——MockLlm 产 Retrying 事件 → 会话层推 retry view 事件（retrying 态）
        /// → 首个 Text 到达 → 推 resolved 回填。断言 host 收到 retry 视图事件（retrying + resolved）。
        /// </summary>
        [Fact]
        public void S2_Retry_VisibleToFrontend()
        {
            MockLlm llm = new MockLlm();
            llm.EmitRetry = true;
            llm.ReplyText = "重试后正常回复";
            MockHost host = new MockHost();
            CH4.ChatSession session = CreateSession(llm);
            session.AttachHost(host);
            host.Session = session;
            session.PostUserMessage("重试可见性");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            // 收到 retry view 事件（≥2：retrying + resolved）
            List<string> retryEvents;
            Assert.True(host.ViewEvents.TryGetValue("retry", out retryEvents));
            Assert.True(retryEvents.Count >= 2);
            // 首个 retrying 态——attempt/max 解析正确
            using (JsonDocument d1 = JsonDocument.Parse(retryEvents[0]))
            {
                Assert.Equal("retrying", d1.RootElement.GetProperty("state").GetString());
                Assert.Equal("1", d1.RootElement.GetProperty("attempt").GetString());
                Assert.Equal("3", d1.RootElement.GetProperty("max").GetString());
            }
            // 末个 resolved 态——重试成功回填；A86——保留报错原文（attempt/max/原因摘要齐，不覆盖）
            using (JsonDocument dLast = JsonDocument.Parse(retryEvents[retryEvents.Count - 1]))
            {
                Assert.Equal("resolved", dLast.RootElement.GetProperty("state").GetString());
                Assert.Equal("1", dLast.RootElement.GetProperty("attempt").GetString());
                Assert.Equal("3", dLast.RootElement.GetProperty("max").GetString());
                Assert.False(string.IsNullOrEmpty(dLast.RootElement.GetProperty("text").GetString()));
            }
            // 重试后正常回复
            Assert.Contains("重试后正常回复", GetLastAssistantText(session));
        }

        /// <summary>
        /// <summary>
        /// A94——续传路径终态载荷齐备：空回复 / STREAM_CLOSED 续传同样写入原文三元组，
        /// resolved 回填不丢报错原文（attempt/max/reason 齐备；修复前载荷为空 → 前端只剩「✓ 已恢复」）。
        /// </summary>
        [Fact]
        public void A94_EmptyReplyResolvedKeepsReason()
        {
            MockLlm llm = new MockLlm();
            llm.EmitStreamClosed = true;
            llm.StreamClosedTimes = 1;
            llm.ReplyText = "续传后正常回复";
            MockHost host = new MockHost();
            CH4.ChatSession session = CreateSession(llm);
            session.AttachHost(host);
            host.Session = session;
            session.PostUserMessage("续传终态载荷");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            List<string> retryEvents;
            Assert.True(host.ViewEvents.TryGetValue("retry", out retryEvents));
            Assert.True(retryEvents.Count >= 2);
            using (JsonDocument dLast = JsonDocument.Parse(retryEvents[retryEvents.Count - 1]))
            {
                Assert.Equal("resolved", dLast.RootElement.GetProperty("state").GetString());
                Assert.Equal("1", dLast.RootElement.GetProperty("attempt").GetString());
                Assert.Equal("∞", dLast.RootElement.GetProperty("max").GetString());
                Assert.False(string.IsNullOrEmpty(dLast.RootElement.GetProperty("text").GetString()));
            }
        }

        /// <summary>
        /// A94——重试耗尽终态：本轮推过 retry 气泡后错误耗尽 → 补 failed 终态（保留原文）；
        /// error 气泡仍给最终错误详情（过程 + 结论双气泡语义）。
        /// </summary>
        [Fact]
        public void A94_RetryExhaustedPushesFailedTerminal()
        {
            MockLlm llm = new MockLlm();
            llm.RetryThenFail = true;
            MockHost host = new MockHost();
            CH4.ChatSession session = CreateSession(llm);
            session.AttachHost(host);
            host.Session = session;
            session.PostUserMessage("重试耗尽终态");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            List<string> retryEvents;
            Assert.True(host.ViewEvents.TryGetValue("retry", out retryEvents));
            Assert.True(retryEvents.Count >= 2);
            using (JsonDocument d1 = JsonDocument.Parse(retryEvents[0]))
            {
                Assert.Equal("retrying", d1.RootElement.GetProperty("state").GetString());
                Assert.Equal("1", d1.RootElement.GetProperty("attempt").GetString());
                Assert.Equal("3", d1.RootElement.GetProperty("max").GetString());
                Assert.False(string.IsNullOrEmpty(d1.RootElement.GetProperty("text").GetString()));
            }
            using (JsonDocument dLast = JsonDocument.Parse(retryEvents[retryEvents.Count - 1]))
            {
                Assert.Equal("failed", dLast.RootElement.GetProperty("state").GetString());
                Assert.Equal("1", dLast.RootElement.GetProperty("attempt").GetString());
                Assert.Equal("3", dLast.RootElement.GetProperty("max").GetString());
                Assert.False(string.IsNullOrEmpty(dLast.RootElement.GetProperty("text").GetString()));
            }
            Assert.True(host.ViewEvents.ContainsKey("error"));
        }

        /// <summary>
        /// 空回复续传——MockLlm 模拟流正常结束但只产思考不产文本（CH2 [段2.3] 语义）：
        /// 第一次调用 EmptyReply=true（空回复）→ 续传（同上下文重发）→ 第二次调用正常回复。
        /// 断言：续传后正常完成 + 无空 assistant 消息入上下文 + 无前端 error（续传可恢复不推 error）。
        /// </summary>
        [Fact]
        public void EmptyReply_AutoContinueAndResolve()
        {
            MockLlm llm = new MockLlm();
            // 第 1 次调用空回复（只思考无文本）——后续正常
            llm.EmptyReply = true;
            llm.EmptyReplyTimes = 1;
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("空回复续传测试");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            // 续传后正常完成——assistant 文本存在（第二次调用产出 "ok"）
            Assert.Contains("ok", GetLastAssistantText(session));
            // 无空 assistant 消息——上下文中不出现 content=="" 的 assistant 消息
            LlmMessage[] all = session.Context.GetMessages();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                if (all[i].Role == LlmRole.Assistant)
                {
                    Assert.True(all[i].Content == null || all[i].Content.Length > 0);
                }
            }
            // LLM 调用次数 = 2（1 次空回复 + 1 次续传）
            Assert.Equal(2, llm.CallCount);
        }
        /// <summary>
        /// 纯空格回复续传——MockLlm 模拟流正常结束但只产空格文本（Trim 判空语义）：
        /// 第一次调用产空格 → 自动续传 → 第二次调用正常回复。
        /// 断言：续传后正常完成 + 无空格 assistant 消息入上下文 + 调用次数 = 2。
        /// </summary>
        [Fact]
        public void WhitespaceReply_AutoContinueAndResolve()
        {
            MockLlm llm = new MockLlm();
            llm.WhitespaceReply = true;
            llm.WhitespaceReplyTimes = 1;
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("纯空格续传测试");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            // 续传后正常完成——assistant 文本存在（第二次调用产出 "ok"）
            Assert.Contains("ok", GetLastAssistantText(session));
            // 无空格 assistant 消息——上下文中不出现纯空格 assistant 消息
            LlmMessage[] all = session.Context.GetMessages();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                if (all[i].Role == LlmRole.Assistant)
                {
                    Assert.True(all[i].Content == null || all[i].Content.Trim().Length > 0);
                }
            }

            // LLM 调用次数 = 2（1 次空格回复 + 1 次续传）
            Assert.Equal(2, llm.CallCount);
        }
        /// <summary>
        /// STREAM_CLOSED 续传——MockLlm 模拟 SSE 流未以 [DONE] 结束（ERR|STREAM_CLOSED）：
        /// 第一次调用产 STREAM_CLOSED 错误 → 续传（同上下文重发）→ 第二次调用正常回复。
        /// 断言：续传后正常完成 + 空回复续传计数工作（前 N 次 STREAM_CLOSED 由 StreamClosedTimes 控制）。
        /// </summary>
        [Fact]
        public void StreamClosed_AutoContinueAndResolve()
        {
            MockLlm llm = new MockLlm();
            llm.EmitStreamClosed = true;
            llm.StreamClosedTimes = 1; // 只有第 1 次产 STREAM_CLOSED——后续正常
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("流中断续传测试");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            // 续传后正常完成
            Assert.Contains("ok", GetLastAssistantText(session));
            // LLM 调用次数 = 2（1 次 STREAM_CLOSED + 1 次续传）
            Assert.Equal(2, llm.CallCount);
        }
        /// <summary>
        /// P6 中止——LLM 流中暂停：后台流挂起 → Pause() 取消 → 上下文保留用户消息（半截文本不入上下文——不裁剪、不写半截）→ 复位 Idle + paused 事件（无 chatdone）。
        /// </summary>
        [Fact]
        public void Pause_WhileLlmStreaming_KeepsContextAndPushesPaused()
        {
            MockLlm llm = new MockLlm();
            llm.ReplyText = "半截回复";
            llm.HoldStream = new AutoResetEvent(false);
            MockHost host = new MockHost();
            CH4.ChatSession session = CreateSession(llm);
            session.AttachHost(host);
            host.Session = session;
            session.PostUserMessage("测试暂停");
            // 泵帧直到进入活跃相位（后台流挂起在 HoldStream——保证 Pause 时 LlmRunning）
            for (int i = 0; i < 100 && session.IsIdle; i++)
            {
                session.Pump();
                Thread.Sleep(5);
            }
            Assert.False(session.IsIdle);
            // 中止——取消令牌 → 后台 WaitOne 抛 OCE → 主线程 Pump 消费收尾
            session.Pause();
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            // 上下文保留用户消息——半截文本不入上下文（不裁剪、不写半截；测试会话无 system 提示词——仅 user）
            LlmMessage[] msgs = session.Context.GetMessages();
            Assert.Single(msgs); // user（半截丢弃）
            Assert.Equal(LlmRole.User, msgs[0].Role);
            // 运行态回归 idle（A165：原 paused 控制事件整体并入 state 段 runState）——中止非正常完成语义
            List<string> runStates;
            Assert.True(host.ViewEvents.TryGetValue("runState", out runStates));
            Assert.True(runStates.Count > 0);
            Assert.Equal("idle", runStates[runStates.Count - 1]);
        }
        /// <summary>
        /// P6 中止——取消识别：Runtime 取消路径不产 Retrying（挂起在 Retrying 前）→ Pause 后无 retry 气泡 + paused 气泡 + user 保留。
        /// </summary>
        [Fact]
        public void Pause_CancelBeforeRetry_NoRetryBubble()
        {
            MockLlm llm = new MockLlm();
            llm.EmitRetryThenCancel = true;
            llm.HoldStream = new AutoResetEvent(false);
            MockHost host = new MockHost();
            CH4.ChatSession session = CreateSession(llm);
            session.AttachHost(host);
            host.Session = session;
            session.PostUserMessage("暂停重试测试");
            // 泵帧直到活跃（后台流挂起在 Retrying 前——等待取消）
            for (int i = 0; i < 100 && session.IsIdle; i++)
            {
                session.Pump();
                Thread.Sleep(5);
            }
            Assert.False(session.IsIdle);
            session.Pause();
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            // 无 retry 视图气泡（取消不产重试）
            Assert.False(host.ViewEvents.ContainsKey("retry"));
            // 运行态回归 idle（A165：原 paused 控制事件并入 state 段 runState）
            List<string> runStates;
            Assert.True(host.ViewEvents.TryGetValue("runState", out runStates));
            Assert.True(runStates.Count > 0);
            Assert.Equal("idle", runStates[runStates.Count - 1]);
            // 用户消息保留（不裁剪）
            Assert.Single(session.Context.GetMessages());
        }
        /// <summary>
        /// P10 roundsum 重建归并——多轮会话重建后 GetBlocks 顺序：roundsum 位于所属轮末块之后、下一轮块之前
        /// （时间戳排序——真实时序权威，跨重启稳定；修复"刷新后所有统计排末尾"）。
        /// </summary>
        [Fact]
        public void View_Rebuild_RoundSumMergedByTimestamp()
        {
            // 构造多轮真实前文——Sleep 保证 CreatedAt 毫秒递增（视图排序键）
            ChatContext ctx = new ChatContext();
            ctx.SetSystemPrompt("系统");
            ctx.AddUserMessage("第一轮问题");
            ctx.AddAssistantMessage("第一轮回复");
            Thread.Sleep(5);
            ctx.AddUserMessage("第二轮问题");
            ctx.AddAssistantMessage("第二轮回复");
            Thread.Sleep(5);
            ctx.AddUserMessage("第三轮问题");
            ctx.AddAssistantMessage("第三轮回复");
            LlmMessage[] msgs = ctx.GetMessages();
            // 增量写入视图（与真实运行一致：每轮末 CloseRound 追加 roundsum——时间戳 = 轮末块之后）
            string tmp = Path.Combine(Path.GetTempPath(), "cat4test_" + Guid.NewGuid().ToString("N") + ".view.jsonl");
            CH4.SessionViewStore viewStore = new CH4.SessionViewStore(tmp);
            for (int i = 0; i < msgs.Length; i++)
            {
                LlmMessage m = msgs[i];
                if (m.Role == LlmRole.System)
                {
                    continue;
                }
                if (m.Role == LlmRole.User)
                {
                    viewStore.OnUserMessage(m, i);
                }
                else if (m.Role == LlmRole.Assistant)
                {
                    viewStore.OnAssistantText(m, i);
                    // 该轮末 CloseRound——rs 时间戳略大于本轮末块（真实语义：CloseRound 在该轮结束后）
                    viewStore.AppendRoundSummary("{\"type\":\"roundsum\",\"data\":{}}", m.CreatedAt + 1);
                }
            }
            // A248：每块 append 即落盘——重启场景无需显式写盘
            // A156：重启场景 = 载入持久块（不再从前文重建）
            viewStore.Load();
            // 归并顺序断言——每轮：user, text, roundsum（rs 紧跟轮末块，不在末尾堆叠）
            CH4.ViewBlock[] merged = viewStore.GetBlocks();
            Assert.Equal(9, merged.Length); // 6 blocks + 3 rs（无注入报告）
            string[] expected = { "user", "text", "roundsum", "user", "text", "roundsum", "user", "text", "roundsum" };
            for (int i = 0; i < expected.Length; i++)
            {
                Assert.Equal(expected[i], merged[i].RenderType);
            }
        }

        /// <summary>
        /// P6b 回滚——从正式回复节点截断：上下文/落盘/视图/统计全部复位（保留 [0..msgIndex]）。
        /// 测试环境无 system 消息——两轮后结构：user1+reply1+user2+reply2 = 4 条。
        /// </summary>
        [Fact]
        public void Rollback_ToReply_TruncatesAndPersists()
        {
            MockLlm llm = new MockLlm();
            llm.ReplyText = "第一轮回复";
            llm.UsageJson = "{\"prompt\":900,\"completion\":30,\"cacheHit\":700}";
            CH4.ChatSession session = CreateSession(llm);
            // 两轮对话——user1+reply1+user2+reply2 = 4 条（测试环境无 system）
            session.PostUserMessage("第一轮问题");
            PumpUntilIdle(session);
            llm.ReplyText = "第二轮回复";
            session.PostUserMessage("第二轮问题");
            PumpUntilIdle(session);
            LlmMessage[] before = session.Context.GetMessages();
            Assert.Equal(4, before.Length);
            // 回滚前——请求级前文长度有值（末次请求 prompt；回滚后必须归零）
            Assert.Equal(900L, session.ContextTokensKnown);
            // 回滚到第一个正式回复（索引 1）
            string result = session.Rollback(1);
            Assert.StartsWith("rollback", result);
            LlmMessage[] after = session.Context.GetMessages();
            Assert.Equal(2, after.Length);
            Assert.Equal("第一轮问题", after[0].Content);
            Assert.Equal("第一轮回复", after[1].Content);
            // 落盘验证——从文件重载仍是截断后前文
            LlmMessage[] restored;
            Assert.True(session.Store.TryLoad(out restored));
            Assert.Equal(2, restored.Length);
            // 视图验证——视图层截断随真实前文同步（回滚：切点之后的前文派生条目一并移除——此前「视图不动」
            //           使帧轮重置后的全量帧取数源仍带已回滚消息，前端观感无效）
            CH4.ViewBlock[] blocks = session.GetViewBlocks();
            Assert.Equal("user", blocks[0].RenderType);
            Assert.Equal(0, blocks[0].MsgIndex);
            Assert.Equal("text", blocks[1].RenderType);
            Assert.Equal(1, blocks[1].MsgIndex);
            // 前文派生块——切点之后零残留（非前文派生块 roundsum 按时间界限保留）
            int kept = 0;
            for (int i = 0; i < blocks.Length; i = i + 1)
            {
                if (blocks[i].MsgIndex >= 0)
                {
                    Assert.True(blocks[i].MsgIndex <= 1);
                    kept = kept + 1;
                }
            }
            Assert.Equal(2, kept);
            Assert.Equal("user", blocks[0].RenderType);
            Assert.Equal(0, blocks[0].MsgIndex);
            Assert.Equal("text", blocks[1].RenderType);
            Assert.Equal(1, blocks[1].MsgIndex);
            // 统计复位——新起点零统计（A202：请求级前文长度实时值与落盘回落源一并归零）
            Assert.Equal(0L, session.ContextTokensKnown);
        }
        /// <summary>
        /// session.new 统计重置——前文长度（请求级实时值 + 落盘回落源）一并归零：新会话后读面不显示旧会话 context。
        /// </summary>
        [Fact]
        public void ResetStats_ClearsContextTokensKnown()
        {
            MockLlm llm = new MockLlm();
            llm.ReplyText = "回复";
            llm.UsageJson = "{\"prompt\":1234,\"completion\":10,\"cacheHit\":0}";
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("问题");
            PumpUntilIdle(session);
            // 请求结束——前文长度 = 该次请求 prompt（请求级实时值）
            Assert.Equal(1234L, session.ContextTokensKnown);
            // session.new 重置——读面归零（不回落旧会话值）
            session.ResetStats();
            Assert.Equal(0L, session.ContextTokensKnown);
        }

        /// <summary>
        /// P6b 回滚——非正式回复节点拒绝（user 消息/工具声明不可作切点——协议完整性）。
        /// </summary>
        [Fact]
        public void Rollback_NonReplyNode_Rejected()
        {
            MockLlm llm = new MockLlm();
            llm.ReplyText = "回复";
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("问题");
            PumpUntilIdle(session);
            string result = session.Rollback(0); // user 消息
            Assert.StartsWith("ERR|ROLLBACK_NODE", result);
            Assert.Equal(2, session.Context.GetMessages().Length);
        }

        /// <summary>
        /// P6b 回滚——越界索引拒绝。
        /// </summary>
        [Fact]
        public void Rollback_OutOfRange_Rejected()
        {
            MockLlm llm = new MockLlm();
            llm.ReplyText = "回复";
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("问题");
            PumpUntilIdle(session);
            string result = session.Rollback(99);
            Assert.StartsWith("ERR|ROLLBACK_INDEX", result);
            result = session.Rollback(-1);
            Assert.StartsWith("ERR|ROLLBACK_INDEX", result);
            Assert.Equal(2, session.Context.GetMessages().Length);
        }

        /// <summary>
        /// P6b 视图 MsgIndex——增量写入时携带真实前文索引（text 块指向正式回复；roundsum=-1）。
        /// </summary>
        [Fact]
        public void View_MsgIndex_CarriedOnAppend()
        {
            ChatContext ctx = new ChatContext();
            ctx.SetSystemPrompt("系统");
            ctx.AddUserMessage("问题");
            ctx.AddAssistantMessage("回复");
            LlmMessage[] msgs = ctx.GetMessages();
            string tmp = Path.Combine(Path.GetTempPath(), "cat4test_" + Guid.NewGuid().ToString("N") + ".view.jsonl");
            CH4.SessionViewStore viewStore = new CH4.SessionViewStore(tmp);
            for (int i = 0; i < msgs.Length; i = i + 1)
            {
                LlmMessage m = msgs[i];
                if (m.Role == LlmRole.System)
                {
                    continue;
                }
                if (m.Role == LlmRole.User)
                {
                    viewStore.OnUserMessage(m, i);
                }
                else if (m.Role == LlmRole.Assistant)
                {
                    viewStore.OnAssistantText(m, i);
                }
            }
            viewStore.AppendRoundSummary("{\"type\":\"roundsum\",\"data\":{}}", msgs[2].CreatedAt + 1);
            CH4.ViewBlock[] blocks = viewStore.GetBlocks();
            Assert.Equal(3, blocks.Length);
            Assert.Equal(1, blocks[0].MsgIndex);
            Assert.Equal(2, blocks[1].MsgIndex);
            Assert.Equal(-1, blocks[2].MsgIndex);
        }

        /// <summary>
        /// Q2 对照——工具轮后空回复无限续传恢复为正常 reply：有 reply 且 Note 未完成 → Note 拉起（正常语义保持）。
        /// </summary>
        [Fact]
        public void ToolRound_ThenEmptyReply_ThenReply_PullsNote()
        {
            MockLlm llm = new MockLlm();
            // 第 1 轮：工具调用（time 内置——不进 OA）
            llm.ToolCallsQueue.Enqueue("[{\"id\":\"t1\",\"function\":{\"name\":\"time\",\"arguments\":\"{}\"}}]");
            // 第 2 轮：空回复（只思考无文本）→ 无限续传
            llm.EmptyReply = true;
            llm.EmptyReplyTimes = 1;
            // 第 3 轮：正常回复（MockLlm 默认 ReplyText="ok"）→ Note 剩余 2 条 → 自动拉起
            CH4.ChatSession session = CreateSession(llm);
            session.NoteAdd("任务A");
            session.NoteAdd("任务B");
            session.PostUserMessage("开始");
            // 泵至 [Note 未完成] 入上下文（上限 300 帧——拉起后 MockLlm 持续回复会重复拉起，非本测试范围）
            bool pulled = false;
            for (int i = 0; i < 300 && !pulled; i = i + 1)
            {
                session.Pump();
                Thread.Sleep(5);
                LlmMessage[] check = session.Context.GetMessages();
                for (int j = 0; j < check.Length; j = j + 1)
                {
                    if (check[j].Role == LlmRole.User && check[j].Content != null && check[j].Content.IndexOf("[Note 未完成]", StringComparison.Ordinal) >= 0)
                    {
                        pulled = true;
                    }
                }
            }
            // 有正常 reply + Note 未完成 → Note 拉起（user 消息入上下文）
            Assert.True(pulled);
        }

        /// <summary>
        /// Q 对照——剩余 1 条（最后一条）不自动拉起：LLM 完成后自然结束（工具提示"最后一条完成后可结束本轮"——不再强制拉起）。
        /// </summary>
        [Fact]
        public void ToolRound_LastTaskRemain_NoPull()
        {
            MockLlm llm = new MockLlm();
            llm.ToolCallsQueue.Enqueue("[{\"id\":\"t1\",\"function\":{\"name\":\"time\",\"arguments\":\"{}\"}}]");
            llm.EmptyReply = true;
            llm.EmptyReplyTimes = 1;
            CH4.ChatSession session = CreateSession(llm);
            session.NoteAdd("任务A");
            session.PostUserMessage("开始");
            bool pulled = false;
            for (int i = 0; i < 120 && !pulled; i = i + 1)
            {
                session.Pump();
                Thread.Sleep(5);
                LlmMessage[] check = session.Context.GetMessages();
                for (int j = 0; j < check.Length; j = j + 1)
                {
                    if (check[j].Role == LlmRole.User && check[j].Content != null && check[j].Content.IndexOf("[Note 未完成]", StringComparison.Ordinal) >= 0)
                    {
                        pulled = true;
                    }
                }
            }
            // 剩余 1 条不拉起——上下文无 [Note 未完成]（最后一条由 LLM 完成后自然结束）
            Assert.False(pulled);
        }
        /// <summary>
        /// 运行态七态——工具轮：requests=2（工具前后各一次 API 请求）；七态表可读（idle 不计时恒 0）；轮末当前态归 idle（2026-09-16 基建事实态 + A59 去 idle 计时）。
        /// </summary>
        [Fact]
        public void RunState_SevenPhases_ToolRound()
        {
            MockLlm llm = new MockLlm();
            llm.ReplyText = "完成";
            // 单任务 Note（不触发 Note 自动拉起——剩余 0 条）
            string tc = "[{\"id\":\"n1\",\"function\":{\"name\":\"Note\",\"arguments\":\"{\\\"action\\\":\\\"set\\\",\\\"content\\\":\\\"任务A\\\"}\"}}]";
            llm.ToolCallsQueue.Enqueue(tc);
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("做任务");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            // 请求次数——工具轮 = 2 次 API 请求（工具前 + 工具后）
            Assert.Equal(2, llm.CallCount);
            // 运行态读取面——七态键齐 + 请求次数 + 轮末归 idle
            string stateName;
            int requests;
            Dictionary<string, long> ms = session.GetRunState(out stateName, out requests);
            Assert.Equal(2, requests);
            Assert.Equal("idle", stateName);
            Assert.True(ms.ContainsKey("idle"));
            Assert.True(ms.ContainsKey("wait"));
            Assert.True(ms.ContainsKey("link"));
            Assert.True(ms.ContainsKey("think"));
            Assert.True(ms.ContainsKey("tool"));
            Assert.True(ms.ContainsKey("run"));
            Assert.True(ms.ContainsKey("reply"));
            // 本地态与远端态均可累计（数值非负——真实时长归后端单源）
            Assert.True(ms["link"] >= 0);
            Assert.True(ms["reply"] >= 0);
            Assert.True(ms["run"] >= 0);
            // idle 不计时——只作态名（恒 0：空闲期 sessions 段不脏变化，A59）
            Assert.Equal(0, ms["idle"]);
        }

        /// <summary>
        /// 运行态连续计时——同态重复事件不重置起表：多块思考增量累计覆盖全程（2026-09-16 实测缺陷修正——旧实现同态重置只留最后一段）。
        /// </summary>
        [Fact]
        public void RunState_SameStateRepeat_KeepsAccumulating()
        {
            MockLlm llm = new MockLlm();
            llm.ReplyText = "完成";
            llm.ChunkCount = 4;
            llm.ChunkSleepMs = 30;
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("连续计时");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            string stateName;
            int requests;
            Dictionary<string, long> ms = session.GetRunState(out stateName, out requests);
            // 四块思考增量各间隔 30ms——think 累计覆盖三段间隔（≥60ms 留抖动余量；旧实现同态重置只留最后一段 ≈0ms）
            Assert.True(ms["think"] >= 60, "think 累计应覆盖全部增量段（实际 " + ms["think"].ToString() + "ms）");
            Assert.Equal("idle", stateName);
        }

        /// <summary>
        /// A78 思考段终结（纯文本轮）——离开 think 态（首个回复帧）即推思考整块：replaceSeq 命中流式容器序号；
        /// 唯一出口 = PhaseEnter（莎 2026-09-22 定）。
        /// </summary>
        [Fact]
        public void ReasonStream_SealedOnLeavingThink_TextReply()
        {
            MockLlm llm = new MockLlm();
            llm.ChunkCount = 2;
            llm.ReplyText = "回复";
            MockHost host = new MockHost();
            CH4.ChatSession session = CreateSession(llm);
            session.AttachHost(host);
            host.Session = session;
            session.PostUserMessage("想一想");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            // A165——流式容器在轮末全部撤离（临时区全量镜像随之清空）：ViewEvents 无流式两类型键
            Assert.False(host.ViewEvents.ContainsKey("replysse"));
            Assert.False(host.ViewEvents.ContainsKey("thinksse"));
            // 纯文本轮的思考段只属流式区：前文消息不带 reasoning → 不产 reason 持久块
            Assert.False(host.ViewEvents.ContainsKey("reason"));
        }

        /// <summary>
        /// A78 思考段终结（工具轮）——收口提前到工具决策流首帧（不等流末）：思考整块先于工具卡推送。
        /// </summary>
        [Fact]
        public void ReasonStream_SealedOnToolCallsStart()
        {
            MockLlm llm = new MockLlm();
            llm.ChunkCount = 2;
            llm.ToolCallsQueue.Enqueue("[{\"id\":\"t1\",\"function\":{\"name\":\"time\",\"arguments\":\"{}\"}}]");
            MockHost host = new MockHost();
            CH4.ChatSession session = CreateSession(llm, new ToolSpec[]
            {
                new ToolSpec("time", "当前时间", "{}")
            });
            session.AttachHost(host);
            host.Session = session;
            session.PostUserMessage("用工具");
            PumpUntilIdle(session);
            Assert.True(session.IsIdle);
            List<string> reasons = host.ViewEvents["reason"];
            Assert.True(reasons.Count >= 1);
            using (JsonDocument d = JsonDocument.Parse(reasons[0]))
            {
                Assert.Equal("思思", d.RootElement.GetProperty("text").GetString());
            }
            // 时序——思考整块先于工具卡（决策流首帧收口）
            Assert.True(host.EventOrder.IndexOf("reason") < host.EventOrder.IndexOf("toolcard"));
        }
        /// <summary>
        /// 继续轮——不追加任何用户消息，直接用当前前文发一次 LLM 请求（cat.continue 后端语义）。
        /// 判据：请求次数 +1（真发请求）· 前文只多回复一条（不产生新 user 消息——常规轮为 +2）。
        /// </summary>
        [Fact]
        public void Continue_NoNewMessage()
        {
            MockLlm llm = new MockLlm();
            llm.ReplyText = "续写内容";
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("第一轮问题");
            PumpUntilIdle(session);
            int beforeCount = session.Context.GetMessages().Length;
            int callsBefore = llm.CallCount;
            session.Continue();
            PumpUntilIdle(session);
            // 真发了一次请求
            Assert.Equal(callsBefore + 1, llm.CallCount);
            // 前文只多一条 assistant 回复（无新 user 消息）
            LlmMessage[] after = session.Context.GetMessages();
            Assert.Equal(beforeCount + 1, after.Length);
            Assert.Equal(LlmRole.Assistant, after[after.Length - 1].Role);
            Assert.Equal("续写内容", after[after.Length - 1].Content);
        }
        /// <summary>继续——忙时不打断当前轮（仅置位排队：跨线程不碰会话内部队列），轮末 Idle 后消费。</summary>
        [Fact]
        public void Continue_WhileBusy_QueuedUntilIdle()
        {
            MockLlm llm = new MockLlm();
            llm.ReplyText = "回复";
            llm.HoldStream = new AutoResetEvent(false);
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("第一轮");
            for (int i = 0; i < 100 && session.IsIdle; i = i + 1)
            {
                session.Pump();
                Thread.Sleep(5);
            }
            Assert.False(session.IsIdle);
            Assert.Equal(1, llm.CallCount);
            // 忙时继续——仅置位（不打断当前轮：请求次数不变）
            session.Continue();
            Assert.Equal(1, llm.CallCount);
            // 放行当前轮 → 轮末 Idle 消费继续请求（第二次请求）；继续轮同样挂起——逐帧放行
            for (int i = 0; i < 300; i = i + 1)
            {
                llm.HoldStream.Set();
                session.Pump();
                Thread.Sleep(5);
                if (llm.CallCount >= 2 && session.IsIdle)
                {
                    break;
                }
            }
            Assert.Equal(2, llm.CallCount);
            Assert.True(session.IsIdle);
        }
        /// <summary>
        /// 继续——空前文拒绝（无可续内容）：不入队、不发请求（防御性出声，不静默）。
        /// </summary>
        [Fact]
        public void Continue_EmptyContext_Rejected()
        {
            MockLlm llm = new MockLlm();
            CH4.ChatSession session = CreateSession(llm);
            session.Continue();
            PumpUntilIdle(session);
            Assert.Equal(0, llm.CallCount);
            Assert.True(session.IsIdle);
        }
        /// <summary>
        /// 继续——跨线程调用路径（HTTP 线程置位 → 主线程 Pump 消费）。
        /// 回归：2026-09-30 缺陷——Continue 曾在 HTTP 线程直接改会话内部队列（无内存屏障），主线程读不到 → 指令被受理却无轮次；
        /// 现行 = volatile 置位（与 Pause / SessionNewRequested 同模式）——跨线程置位后主线程必须能启动继续轮。
        /// </summary>
        [Fact]
        public void Continue_FromOtherThread_ConsumedByPump()
        {
            MockLlm llm = new MockLlm();
            llm.ReplyText = "回复";
            CH4.ChatSession session = CreateSession(llm);
            session.PostUserMessage("第一轮");
            PumpUntilIdle(session);
            int callsBefore = llm.CallCount;
            // 模拟 HTTP 线程调用（非主线程）——只置位，不碰会话内部队列
            Thread worker = new Thread(delegate () { session.Continue(); });
            worker.Start();
            worker.Join();
            PumpUntilIdle(session);
            Assert.Equal(callsBefore + 1, llm.CallCount);
            // 前文只多回复一条（继续轮不追加用户消息）
            LlmMessage[] msgs = session.Context.GetMessages();
            Assert.Equal(LlmRole.Assistant, msgs[msgs.Length - 1].Role);
        }
    }
}