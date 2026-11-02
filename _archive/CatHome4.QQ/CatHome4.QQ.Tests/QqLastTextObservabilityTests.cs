using System;
using System.Collections.Generic;
using CatHome4.QQ;
using Mau.Runtime;
using Xunit;

namespace CatHome4.QQ.Tests
{
    /// <summary>
    /// /last 兜底通道观测留痕测试（A208）——取块结果 L1 落总账。
    /// 断言面 = BuildLastText 返回值 + LogStore 总账条目（命中位置 / 无回复两态）。
    /// </summary>
    public sealed class QqLastTextObservabilityTests
    {
        /// <summary>造视图项——渲染类型 + 内容。</summary>
        /// <param name="renderType">渲染类型</param>
        /// <param name="content">内容</param>
        /// <returns>视图项</returns>
        private static QqViewItem Item(string renderType, string content)
        {
            QqViewItem it = new QqViewItem();
            it.RenderType = renderType;
            it.Content = content;
            it.Done = "";
            it.Origin = "";
            return it;
        }

        /// <summary>造绑定目标——视图脚本化；注入面不参与本组断言。</summary>
        /// <param name="key">猫标识</param>
        /// <param name="view">视图脚本</param>
        /// <returns>目标</returns>
        private static QqTarget Target(string key, List<QqViewItem> view)
        {
            QqTarget tg = new QqTarget();
            tg.Key = key;
            tg.DisplayName = "观测猫";
            tg.QqBotId = Guid.Empty;
            tg.Enable = true;
            tg.Inject = delegate (string s, string origin) { return true; };
            tg.GetViewItems = delegate () { return view.ToArray(); };
            tg.IsIdle = delegate () { return true; };
            tg.IsTimebackActive = delegate () { return false; };
            return tg;
        }

        /// <summary>在总账里找含关键词的首条消息——找不到返回空串。</summary>
        /// <param name="keyword">关键词</param>
        /// <returns>消息文本</returns>
        /// <remarks>
        /// 走 LogStore.Snapshot() 锁内快照——总账是共享可变 List（写入侧 `lock (LogStore.Sync)` 内 Add），
        /// xUnit 类间并行下裸 foreach 会撞「集合已修改」（2026-10-14 实测：prepare 步 2b 偶发红）。
        /// </remarks>
        private static string FindLog(string keyword)
        {
            List<LogStore.LogEntry> snapshot = LogStore.Snapshot();
            foreach (LogStore.LogEntry e in snapshot)
            {
                if (e.Message.Contains(keyword))
                {
                    return e.Message;
                }
            }
            return "";
        }

        /// <summary>命中块——返回最后一条 text 内容，且总账记下命中位置（第 i / 总数 块）。</summary>
        [Fact]
        public void BuildLastText_Hit_LogsPosition()
        {
            LogStore.ClearForTest();
            List<QqViewItem> view = new List<QqViewItem>();
            view.Add(Item("text", "旧回复"));
            view.Add(Item("roundsum", ""));
            view.Add(Item("text", "最新回复"));
            QQBotService.SetCollector(new StubCollector(Target("a208-cat", view)));

            string body = QQBotService.BuildLastText(Guid.Empty);

            Assert.Equal("观测猫：\n最新回复", body);
            string log = FindLog("/last 取块");
            Assert.Contains("a208-cat", log);
            Assert.Contains("命中第 3 / 3 块", log);
        }

        /// <summary>无正式回复——总账记「无已生成回复（共 N 块）」，不回空。</summary>
        [Fact]
        public void BuildLastText_NoReply_LogsEmpty()
        {
            LogStore.ClearForTest();
            List<QqViewItem> view = new List<QqViewItem>();
            view.Add(Item("user", ""));
            view.Add(Item("roundsum", "stream"));
            QQBotService.SetCollector(new StubCollector(Target("a208-empty", view)));

            string body = QQBotService.BuildLastText(Guid.Empty);

            Assert.Equal("【观测猫】无已生成回复", body);
            string log = FindLog("/last 取块");
            Assert.Contains("a208-empty", log);
            Assert.Contains("无已生成回复（共 2 块）", log);
        }

        /// <summary>收集器桩——按注入目标返回 1:1 绑定。</summary>
        private sealed class StubCollector : IQqTargetCollector
        {
            /// <summary>绑定目标</summary>
            private readonly QqTarget _target;

            /// <summary>建桩。</summary>
            /// <param name="target">绑定目标</param>
            public StubCollector(QqTarget target)
            {
                _target = target;
            }

            /// <summary>取绑定指定 Bot 的猫——恒返回注入目标。</summary>
            /// <param name="qqBotId">Bot 配置身份</param>
            /// <returns>目标列表</returns>
            public List<QqTarget> CollectByBot(Guid qqBotId)
            {
                List<QqTarget> list = new List<QqTarget>();
                list.Add(_target);
                return list;
            }

            /// <summary>收集全部目标——同 CollectByBot。</summary>
            /// <returns>目标列表</returns>
            public List<QqTarget> CollectAll()
            {
                List<QqTarget> list = new List<QqTarget>();
                list.Add(_target);
                return list;
            }
        }
    }
}
