using System;
using System.Collections.Generic;
using System.Text.Json;
using CatHome4.Contracts;
using Mau.Runtime;
using CH4;
using Xunit;

namespace CatHome4.Core.Tests
{
    /// <summary>
    /// 会话视图出口测试（A165 阶段 1 · chat 快照协议 v2 · A196 临时区状态投影）。
    /// 三段结构：state（后端权威业务态整段比对，变化才推）· persist（持久对话区只增：全量 / 追加两模式）·
    /// live（临时区 · **状态投影**：type + context 两个字符串，整段覆盖；type ∈ toolrun / thinksse / replysse / empty）。
    /// 铁律：无事发生零字节 · 推送序即渲染序 · 无表 / 无槽位 / 无配对 / 无增删（覆盖即销毁）。
    /// </summary>
    public sealed class ViewBusTests
    {
        /// <summary>捕获型推送面——记录每次 PushView 的 op（v2 下视图面不再走此出口；保留以满足接口契约）</summary>
        private sealed class RecordingHost : IHostPush
        {
            /// <summary>事件 op 序列（按发出序）</summary>
            public readonly List<string> Ops = new List<string>();

            /// <summary>视图事件捕获——op</summary>
            /// <param name="op">事件操作</param>
            /// <param name="payload">载荷 JSON</param>
            /// <param name="meta">块元数据 JSON</param>
            public void PushView(string op, string payload, string meta)
            {
                Ops.Add(op);
            }
        }

        /// <summary>构造持久块——v2 五字段（测试面直赋）</summary>
        /// <param name="type">渲染类型</param>
        /// <param name="payload">载荷 JSON</param>
        /// <param name="ts">事件时刻</param>
        /// <param name="msgIndex">前文来源索引（独立块 -1）</param>
        /// <param name="round">所属轮次</param>
        /// <returns>视图块</returns>
        private static ViewBlock Block(string type, string payload, long ts, int msgIndex, int round)
        {
            ViewBlock block = new ViewBlock();
            block.RenderType = type;
            block.Payload = payload;
            block.Timestamp = ts;
            block.MsgIndex = msgIndex;
            block.Round = round;
            return block;
        }

        /// <summary>取帧——无变化返回 null</summary>
        /// <param name="bus">视图出口</param>
        /// <returns>帧 JSON 或 null</returns>
        private static string TakeFrame(CH4.ViewBus bus)
        {
            string json;
            if (bus.TryTakeFrame(out json))
            {
                return json;
            }
            return null;
        }

        /// <summary>
        /// 全量帧取自注入的数据源——单一真相源（视图存储），每次现取、按时间戳升序（前端零排序）。
        /// 判例 2026-10-03：输出侧持副本 → 载入的历史发不出去（刷新即丢视图面历史）。
        /// </summary>
        [Fact]
        public void FullFrame_TakesFromInjectedSource()
        {
            CH4.ViewBus bus = new CH4.ViewBus();
            RecordingHost host = new RecordingHost();
            bus.Attach(host);
            bus.AttachSource(delegate ()
            {
                return new ViewBlock[]
                {
                    Block("user", "{\"text\":\"旧消息\"}", 2000, 1, 0),
                    Block("reason", "{\"text\":\"旧思考\"}", 1000, 2, 0)
                };
            });
            JsonElement root = Root(bus.BuildFull());
            JsonElement items = root.GetProperty("persist").GetProperty("items");
            Assert.Equal(2, items.GetArrayLength());
            Assert.Equal("reason", items[0].GetProperty("type").GetString());
            Assert.Equal("user", items[1].GetProperty("type").GetString());
        }
        /// <summary>
        /// 待推内容探测（A206）——重启闸门第二判据：内容面（持久块 / 临时区）置位，
        /// 状态段**不计入**（含运行时长等每帧变化的值，纳入即永久置位——闸门永不通过）；
        /// 未接线（推送面未附加）恒 false。
        /// </summary>
        [Fact]
        public void HasPendingContent_CoversContentOnly()
        {
            CH4.ViewBus bus = new CH4.ViewBus();
            Assert.False(bus.HasPendingContent);
            RecordingHost host = new RecordingHost();
            bus.Attach(host);
            Assert.False(bus.HasPendingContent);
            // 状态段变更不计入
            bus.SetState("{\"runState\":\"idle\"}");
            Assert.False(bus.HasPendingContent);
            // 临时区变更计入——探测本身不消耗（连续两次探测同值）
            bus.SetLive("replysse", "甲");
            Assert.True(bus.HasPendingContent);
            Assert.True(bus.HasPendingContent);
            Assert.NotNull(TakeFrame(bus));
            Assert.False(bus.HasPendingContent);
            // 持久块追加计入
            bus.PushPersist(Block("text", "{\"text\":\"乙\"}", 1000, 0, 1));
            Assert.True(bus.HasPendingContent);
            Assert.NotNull(TakeFrame(bus));
            Assert.False(bus.HasPendingContent);
        }
        /// <summary>
        /// 待推标记复位（A297）——猫停机后其 HttpHost 已 Stop、帧泵消失，待推标记再无人取走，
        /// 全局重启闸门 `AnyViewPending` 因此永久置位（判例 2026-11-02：restart-incr 连续三次超时）。
        /// 复位后内容面标记全清；复位只清标记不动内容，再次写入仍能正常置位。
        /// </summary>
        [Fact]
        public void ResetPending_ClearsContentFlags()
        {
            CH4.ViewBus bus = new CH4.ViewBus();
            RecordingHost host = new RecordingHost();
            bus.Attach(host);
            bus.SetLive("replysse", "甲");
            bus.PushPersist(Block("text", "{\"text\":\"乙\"}", 1000, 0, 1));
            Assert.True(bus.HasPendingContent);
            bus.ResetPending();
            Assert.False(bus.HasPendingContent);
            // 复位不产生状态副作用——再次写入照常置位
            bus.SetLive("empty", "");
            Assert.True(bus.HasPendingContent);
        }

        /// <summary>解析 JSON 根节点（独立副本——原 document 立刻释放）</summary>
        /// <param name="json">JSON 文本</param>
        /// <returns>根元素副本</returns>
        private static JsonElement Root(string json)
        {
            using (JsonDocument doc = JsonDocument.Parse(json))
            {
                return doc.RootElement.Clone();
            }
        }

        /// <summary>取临时区段——{type, context}（A196 两个字符串）</summary>
        /// <param name="frame">帧 JSON</param>
        /// <returns>live 段对象</returns>
        private static JsonElement LiveSeg(string frame)
        {
            return Root(frame).GetProperty("live");
        }

        /// <summary>全量帧——三段齐备；条目只带业务定位字段（无键 / 无 ID / 无生命周期态）</summary>
        [Fact]
        public void FullFrameCarriesThreeSegments()
        {
            RecordingHost host = new RecordingHost();
            CH4.ViewBus bus = new CH4.ViewBus();
            bus.Attach(host);
            bus.SetState("{\"runState\":\"idle\"}");
            bus.PushPersist(Block("text", "{\"text\":\"甲\"}", 1000, 0, 1));
            bus.SetLive("thinksse", "想");

            string full = bus.BuildFull();
            JsonElement root = Root(full);
            Assert.Equal(2, root.GetProperty("v").GetInt32());
            Assert.Equal("idle", root.GetProperty("state").GetProperty("runState").GetString());
            Assert.Equal("full", root.GetProperty("persist").GetProperty("mode").GetString());
            JsonElement items = root.GetProperty("persist").GetProperty("items");
            Assert.Equal(1, items.GetArrayLength());
            JsonElement item = items[0];
            Assert.Equal("text", item.GetProperty("type").GetString());
            Assert.Equal(1000L, item.GetProperty("ts").GetInt64());
            Assert.Equal(1, item.GetProperty("round").GetInt32());
            Assert.Equal(0, item.GetProperty("msgIndex").GetInt32());
            Assert.Equal("甲", item.GetProperty("payload").GetProperty("text").GetString());
            JsonElement unused;
            Assert.False(item.TryGetProperty("key", out unused));
            Assert.False(item.TryGetProperty("id", out unused));
            Assert.False(item.TryGetProperty("state", out unused));
            Assert.Equal("thinksse", root.GetProperty("live").GetProperty("type").GetString());
            Assert.Equal("想", root.GetProperty("live").GetProperty("context").GetString());
        }

        /// <summary>追加帧——新持久条目按 append 模式推送（其余段省略）</summary>
        [Fact]
        public void AppendFrameCarriesPersistAppendOnly()
        {
            RecordingHost host = new RecordingHost();
            CH4.ViewBus bus = new CH4.ViewBus();
            bus.Attach(host);

            bus.PushPersist(Block("user", "{\"text\":\"问\"}", 1000, 0, 1));
            string frame = TakeFrame(bus);
            Assert.NotNull(frame);
            JsonElement root = Root(frame);
            Assert.Equal("append", root.GetProperty("persist").GetProperty("mode").GetString());
            Assert.Equal(1, root.GetProperty("persist").GetProperty("items").GetArrayLength());
            JsonElement unused;
            Assert.False(root.TryGetProperty("state", out unused));
            Assert.False(root.TryGetProperty("live", out unused));
        }

        /// <summary>无事发生零字节——取走后再取返回空</summary>
        [Fact]
        public void NoChangeYieldsNoFrame()
        {
            RecordingHost host = new RecordingHost();
            CH4.ViewBus bus = new CH4.ViewBus();
            bus.Attach(host);

            bus.PushPersist(Block("text", "{}", 1000, 0, 1));
            Assert.NotNull(TakeFrame(bus));
            Assert.Null(TakeFrame(bus));
        }

        /// <summary>状态段——整段比对：同值零帧，变化才入帧</summary>
        [Fact]
        public void StateSegmentPushesOnChangeOnly()
        {
            RecordingHost host = new RecordingHost();
            CH4.ViewBus bus = new CH4.ViewBus();
            bus.Attach(host);

            bus.SetState("{\"runState\":\"idle\"}");
            Assert.NotNull(TakeFrame(bus));

            bus.SetState("{\"runState\":\"idle\"}");
            Assert.Null(TakeFrame(bus));

            bus.SetState("{\"runState\":\"wait\"}");
            string frame = TakeFrame(bus);
            Assert.Equal("wait", Root(frame).GetProperty("state").GetProperty("runState").GetString());
        }

        /// <summary>临时区四类——SetLive 整段覆盖：type / context 如实入帧（A196 唯一入口）</summary>
        [Fact]
        public void SetLiveCoversFourTypes()
        {
            RecordingHost host = new RecordingHost();
            CH4.ViewBus bus = new CH4.ViewBus();
            bus.Attach(host);

            bus.SetLive("thinksse", "想");
            JsonElement live = LiveSeg(TakeFrame(bus));
            Assert.Equal("thinksse", live.GetProperty("type").GetString());
            Assert.Equal("想", live.GetProperty("context").GetString());

            bus.SetLive("replysse", "答");
            live = LiveSeg(TakeFrame(bus));
            Assert.Equal("replysse", live.GetProperty("type").GetString());
            Assert.Equal("答", live.GetProperty("context").GetString());

            bus.SetLive("toolrun", "[{\"name\":\"time\"}]");
            live = LiveSeg(TakeFrame(bus));
            Assert.Equal("toolrun", live.GetProperty("type").GetString());
            Assert.Equal("[{\"name\":\"time\"}]", live.GetProperty("context").GetString());

            bus.SetLive("empty", "");
            live = LiveSeg(TakeFrame(bus));
            Assert.Equal("empty", live.GetProperty("type").GetString());
            Assert.Equal("", live.GetProperty("context").GetString());
            Assert.Null(TakeFrame(bus));
        }

        /// <summary>整段覆盖——同 type 连续写入只发最新内容（无累积、无条目）；同值零帧</summary>
        [Fact]
        public void SetLiveOverwritesPreviousContent()
        {
            RecordingHost host = new RecordingHost();
            CH4.ViewBus bus = new CH4.ViewBus();
            bus.Attach(host);

            bus.SetLive("replysse", "甲");
            Assert.Equal("甲", LiveSeg(TakeFrame(bus)).GetProperty("context").GetString());

            bus.SetLive("replysse", "甲乙");
            Assert.Equal("甲乙", LiveSeg(TakeFrame(bus)).GetProperty("context").GetString());

            bus.SetLive("replysse", "甲乙");
            Assert.Null(TakeFrame(bus));
        }

        /// <summary>非法 type——出声并按 empty 落（失败可见；不静默写入未知类型）</summary>
        [Fact]
        public void SetLiveUnknownTypeFallsToEmpty()
        {
            RecordingHost host = new RecordingHost();
            CH4.ViewBus bus = new CH4.ViewBus();
            bus.Attach(host);

            bus.SetLive("replysse", "甲");
            Assert.Equal("replysse", LiveSeg(TakeFrame(bus)).GetProperty("type").GetString());

            // 非法 type → 按空态落（当前有内容 → 变化可见，出一帧）
            bus.SetLive("bogus", "x");
            JsonElement live = LiveSeg(TakeFrame(bus));
            Assert.Equal("empty", live.GetProperty("type").GetString());
            Assert.Equal("", live.GetProperty("context").GetString());

            // 已是空态 → 同值零动作（零字节铁律）
            bus.SetLive("bogus", "x");
            Assert.Null(TakeFrame(bus));
        }

        /// <summary>重置——两区全清并置全量待发（帧轮广播空态全量帧，前端整体重绘）</summary>
        [Fact]
        public void ResetAllEmitsFullFrame()
        {
            RecordingHost host = new RecordingHost();
            CH4.ViewBus bus = new CH4.ViewBus();
            bus.Attach(host);

            bus.PushPersist(Block("text", "{}", 1000, 0, 1));
            bus.SetLive("replysse", "甲");
            Assert.NotNull(TakeFrame(bus));

            bus.ResetAll();
            string frame = TakeFrame(bus);
            JsonElement root = Root(frame);
            Assert.Equal("full", root.GetProperty("persist").GetProperty("mode").GetString());
            Assert.Equal(0, root.GetProperty("persist").GetProperty("items").GetArrayLength());
            Assert.Equal("empty", root.GetProperty("live").GetProperty("type").GetString());
            Assert.Equal("", root.GetProperty("live").GetProperty("context").GetString());
            Assert.Null(TakeFrame(bus));
        }

        /// <summary>推送面未就绪——入口静默（持久块不落、临时区不入账、取帧恒空）</summary>
        [Fact]
        public void DetachedBusIsSilent()
        {
            CH4.ViewBus bus = new CH4.ViewBus();

            Assert.False(bus.Ready);
            bus.SetLive("thinksse", "甲");
            bus.PushPersist(Block("text", "{}", 1000, -1, 1));

            Assert.Null(TakeFrame(bus));

            RecordingHost host = new RecordingHost();
            bus.Attach(host);
            Assert.True(bus.Ready);
            Assert.Empty(host.Ops);
            // 未附加期间的写入不入账——附加后两区仍为空态
            Assert.Null(TakeFrame(bus));
        }

        /// <summary>
        /// 元数据并入的生命周期契约（A157）——JsonElement 是 JsonDocument 的引用视图，
        /// document 释放后再序列化**必须失败**：这是宿主侧 MergeViewMeta 必须 Clone 的根据。
        /// </summary>
        [Fact]
        public void MetaElementEscapingDocumentThrows()
        {
            Dictionary<string, object> ev = new Dictionary<string, object>();
            using (JsonDocument doc = JsonDocument.Parse("{\"ts\":1000,\"round\":3}"))
            {
                foreach (JsonProperty prop in doc.RootElement.EnumerateObject())
                {
                    ev[prop.Name] = prop.Value;
                }
            }

            Assert.Throws<ObjectDisposedException>(() => JsonUtil.Serialize(ev));
        }

        /// <summary>
        /// 元数据并入的正确写法（A157）——Clone 后的值节点独立于 document，释放后仍可序列化。
        /// </summary>
        [Fact]
        public void MetaElementClonedSurvivesDispose()
        {
            Dictionary<string, object> ev = new Dictionary<string, object>();
            ev["seq"] = 1L;
            using (JsonDocument doc = JsonDocument.Parse("{\"ts\":1000,\"round\":3}"))
            {
                foreach (JsonProperty prop in doc.RootElement.EnumerateObject())
                {
                    ev[prop.Name] = prop.Value.Clone();
                }
            }

            string json = JsonUtil.Serialize(ev);
            Assert.Contains("\"ts\":1000", json);
            Assert.Contains("\"round\":3", json);
        }
    }
}
