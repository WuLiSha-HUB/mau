using System;
using System.Collections.Generic;
using System.IO;
using CatHome4.QQ;
using Xunit;

namespace CatHome4.QQ.Tests
{
    /// <summary>
    /// 转发窗状态机测试（A207）——来源与轮绑定（轮起点认领）/ 隐式轮边界 / 越界游标回写 /
    /// 锚点与队列落盘续接 / 工具主动 done 保留。断言面 = DescribeState（游标 / 锚点 / 窗 / 队列 / 轮内计数）。
    /// </summary>
    public sealed class QqForwardWindowTests
    {
        /// <summary>造视图项——渲染类型 + 内容 + done + origin。</summary>
        /// <param name="renderType">渲染类型</param>
        /// <param name="content">文本内容</param>
        /// <param name="done">本轮结束语义</param>
        /// <param name="origin">原注入来源标记</param>
        /// <returns>视图项</returns>
        private static QqViewItem Item(string renderType, string content, string done, string origin)
        {
            QqViewItem it = new QqViewItem();
            it.RenderType = renderType;
            it.Content = content;
            it.Done = done;
            it.Origin = origin;
            return it;
        }

        /// <summary>文本块——转发候选。</summary>
        /// <param name="content">文本</param>
        /// <returns>视图项</returns>
        private static QqViewItem Text(string content)
        {
            return Item("text", content, "", "");
        }

        /// <summary>轮起点块——QQ 注入的 user 块（origin 非空）。</summary>
        /// <returns>视图项</returns>
        private static QqViewItem Marker()
        {
            return Item("user", "", "", "qq");
        }

        /// <summary>轮结束哨兵。</summary>
        /// <param name="done">tool / stream</param>
        /// <returns>视图项</returns>
        private static QqViewItem RoundSum(string done)
        {
            return Item("roundsum", "", done, "");
        }

        /// <summary>造绑定目标——按列表快照返回视图（脚本化）。</summary>
        /// <param name="key">猫标识</param>
        /// <param name="view">视图脚本</param>
        /// <returns>目标</returns>
        private static QqTarget Target(string key, List<QqViewItem> view)
        {
            QqTarget tg = new QqTarget();
            tg.Key = key;
            tg.DisplayName = "测试猫";
            tg.QqBotId = Guid.Empty;
            tg.Enable = true;
            tg.Inject = delegate (string s, string origin) { return true; };
            tg.GetViewItems = delegate () { return view.ToArray(); };
            tg.IsIdle = delegate () { return true; };
            tg.IsTimebackActive = delegate () { return false; };
            tg.GetBlockFingerprint = delegate (int index)
            {
                if (index < 0 || index >= view.Count)
                {
                    return "";
                }
                return view[index].RenderType + "#" + index.ToString();
            };
            return tg;
        }

        /// <summary>造来源——私聊单条。</summary>
        /// <returns>来源</returns>
        private static QqSource Source()
        {
            return new QqSource("private", "u1", "m1", "莎", "");
        }

        /// <summary>推进一次——按目标集合跑一轮转发内核。</summary>
        /// <param name="tg">目标</param>
        private static void TickOnce(QqTarget tg)
        {
            List<QqTarget> targets = new List<QqTarget>();
            targets.Add(tg);
            QQBotService.TickTargets(targets);
        }

        /// <summary>前端轮（无轮起点）——来源不得被认领 / 消费（A207 主症：错位一轮的根）。</summary>
        [Fact]
        public void FrontendRound_DoesNotClaimSource()
        {
            QQBotService.ResetStateForTest();
            List<QqViewItem> view = new List<QqViewItem>();
            view.Add(Text("前端轮回复"));
            view.Add(RoundSum("stream"));
            QqTarget tg = Target("cat1", view);
            QQBotService.EnqueueSource("cat1", Source(), "qq");
            TickOnce(tg);
            Assert.Equal("cursor=2 anchor=1 window=0 queue=1 claimed=0 imm=0 calls=0", QQBotService.DescribeState("cat1"));
        }

        /// <summary>轮起点到达 → 认领开窗 → 轮末消费：队列清空、游标推进。</summary>
        [Fact]
        public void MarkerClaims_RoundSumConsumes()
        {
            QQBotService.ResetStateForTest();
            List<QqViewItem> view = new List<QqViewItem>();
            view.Add(Marker());
            view.Add(Text("本轮回复"));
            view.Add(RoundSum("stream"));
            QqTarget tg = Target("cat1", view);
            QQBotService.EnqueueSource("cat1", Source(), "qq");
            TickOnce(tg);
            Assert.Equal("cursor=3 anchor=1 window=0 queue=0 claimed=0 imm=0 calls=0", QQBotService.DescribeState("cat1"));
        }

        /// <summary>隐式轮边界——上一轮无 roundsum 时，下一个轮起点先收口上一轮（各消费一条来源）。</summary>
        [Fact]
        public void ImplicitBoundary_ClosesPreviousRound()
        {
            QQBotService.ResetStateForTest();
            List<QqViewItem> view = new List<QqViewItem>();
            view.Add(Marker());
            view.Add(Text("第一条回复"));
            view.Add(Marker());
            view.Add(Text("第二条回复"));
            view.Add(RoundSum("stream"));
            QqTarget tg = Target("cat1", view);
            QQBotService.EnqueueSource("cat1", Source(), "qq");
            QQBotService.EnqueueSource("cat1", Source(), "qq");
            TickOnce(tg);
            Assert.Equal("cursor=5 anchor=1 window=0 queue=0 claimed=0 imm=0 calls=0", QQBotService.DescribeState("cat1"));
        }

        /// <summary>工具主动 done——来源保留（认领态不退，等唤醒轮 / 回执轮消费），窗保持开启。</summary>
        [Fact]
        public void ToolDone_KeepsSourceForNextRound()
        {
            QQBotService.ResetStateForTest();
            List<QqViewItem> view = new List<QqViewItem>();
            view.Add(Marker());
            view.Add(Text("工具轮间隙"));
            view.Add(RoundSum("tool"));
            QqTarget tg = Target("cat1", view);
            QQBotService.EnqueueSource("cat1", Source(), "qq");
            TickOnce(tg);
            Assert.Equal("cursor=3 anchor=1 window=1 queue=1 claimed=1 imm=0 calls=0", QQBotService.DescribeState("cat1"));
        }

        /// <summary>视图收缩——越界游标必须回写（旧实现只改局部变量 ⇒ 扫描面永久停摆 ⇒ 盲窗）。</summary>
        [Fact]
        public void CursorClamp_WritesBack_NoBlindWindow()
        {
            QQBotService.ResetStateForTest();
            List<QqViewItem> view = new List<QqViewItem>();
            view.Add(Marker());
            view.Add(Text("第一轮"));
            QqTarget tg = Target("cat1", view);
            QQBotService.EnqueueSource("cat1", Source(), "qq");
            TickOnce(tg);
            Assert.Equal("cursor=2 anchor=1 window=1 queue=1 claimed=1 imm=1 calls=1", QQBotService.DescribeState("cat1"));
            // 会话重建：视图清空 → 游标越界 ⇒ 夹取回写
            view.Clear();
            TickOnce(tg);
            Assert.Equal("cursor=0 anchor=1 window=1 queue=1 claimed=1 imm=1 calls=1", QQBotService.DescribeState("cat1"));
            // 新会话的块必须被扫到（旧实现游标停在 2 ⇒ 越界不回写 ⇒ 永不扫描 ⇒ 来源永不消费）
            view.Add(Text("新会话回复"));
            view.Add(RoundSum("stream"));
            TickOnce(tg);
            Assert.Equal("cursor=2 anchor=1 window=0 queue=0 claimed=0 imm=0 calls=0", QQBotService.DescribeState("cat1"));
        }

        /// <summary>落盘续接——游标 + 锚点 + 队列（含轮起点锚与认领态）读回后与内存态一致。</summary>
        [Fact]
        public void StateRoundTrip_RestoresQueueAndAnchor()
        {
            QQBotService.ResetStateForTest();
            string path = Path.Combine(Path.GetTempPath(), "qq-forward-test-" + Guid.NewGuid().ToString("N") + ".json");
            QQBotService.EnableStateForTest(path);
            try
            {
                List<QqViewItem> view = new List<QqViewItem>();
                view.Add(Marker());
                view.Add(Text("回复未收口"));
                List<QqTarget> targets = new List<QqTarget>();
                QqTarget tg = Target("cat1", view);
                targets.Add(tg);
                QQBotService.EnqueueSource("cat1", Source(), "qq");
                QQBotService.TickTargets(targets);
                string before = QQBotService.DescribeState("cat1");
                Assert.Equal("cursor=2 anchor=1 window=1 queue=1 claimed=1 imm=1 calls=1", before);
                // 重启：内存态清空后从盘读回（LoadForwardState 即 Start 的续接入口）
                QQBotService.ResetStateForTest();
                QQBotService.EnableStateForTest(path);
                QQBotService.LoadForwardState(targets);
                Assert.Equal(before, QQBotService.DescribeState("cat1"));
            }
            finally
            {
                QQBotService.EnableStateForTest("");
                if (File.Exists(path))
                {
                    File.Delete(path);
                }
            }
        }
    }
}
