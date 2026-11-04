using System;
using System.Collections.Generic;
using System.Text;
using CH4;
using Mau.Runtime;

namespace CatHome4.Admin
{
    /// <summary>
    /// 订阅推送（独立渠道）——组静默窗口到期后，把每个订阅者的未读区间汇总成一条注入。
    /// 语义：与派单 / 回执无关（所有条目都计入，含订阅者自己发的）；origin 第 4 段 auto（轮末不回执 → 零环）；
    /// 投递成功才推进游标（未运行 / 未受理下次静默窗口重试，不丢消息）。
    /// </summary>
    public static class GroupAutoForward
    {
        /// <summary>单批最多汇总条数——超出留待下一窗口继续（不一次冲爆订阅者前文）</summary>
        private const int MaxBatchMessages = 100;

        /// <summary>巡检最小间隔（毫秒）——主循环每帧调用，内部按 1 秒节流</summary>
        private const int PumpIntervalMs = 1000;

        /// <summary>上次巡检时刻——Environment.TickCount64（毫秒）</summary>
        private static long _lastPumpAt;

        /// <summary>
        /// 订阅巡检——主循环每帧调用（内部 1 秒节流）；静默窗口到期且有未读的组按订阅者推送。
        /// </summary>
        public static void PumpIdle()
        {
            long tick = Environment.TickCount64;
            if (tick - _lastPumpAt < PumpIntervalMs)
            {
                return;
            }
            _lastPumpAt = tick;
            List<GroupSummary> groups = GroupStore.ListGroups();
            long nowMs = GroupStore.Now();
            for (int i = 0; i < groups.Count; i = i + 1)
            {
                GroupSummary g = groups[i];
                if (g == null || !g.Running)
                {
                    continue;
                }
                if (g.SubscriberKeys == null || g.SubscriberKeys.Length == 0)
                {
                    continue;
                }
                if (g.LastSeq <= 0 || g.LastAt <= 0)
                {
                    continue;
                }
                long idleSec = GroupStore.GetSubscribeIdleSec(g);
                if (nowMs - g.LastAt < idleSec * 1000L)
                {
                    continue;
                }
                PushGroup(g);
            }
        }

        /// <summary>
        /// 单组推送——逐订阅者取未读区间（游标之后），汇总注入并推进游标。
        /// </summary>
        /// <param name="g">组投影（ListGroups 副本）</param>
        private static void PushGroup(GroupSummary g)
        {
            for (int i = 0; i < g.SubscriberKeys.Length; i = i + 1)
            {
                string sub = g.SubscriberKeys[i];
                if (sub == null || sub.Length == 0)
                {
                    continue;
                }
                long cursor = GroupStore.GetSubscriberCursor(g.Name, sub);
                if (cursor >= g.LastSeq)
                {
                    continue;
                }
                // [段1] 未读区间——单批上限；投递成功才推进游标（剩余留待下一窗口）
                List<GroupMessage> batch = GroupStore.ReadMessages(g.Name, cursor, MaxBatchMessages);
                if (batch.Count == 0)
                {
                    continue;
                }
                CatHostService.CatEntry cat = CatHostService.FindDeliveryCat(sub);
                if (cat == null || cat.Session == null)
                {
                    LogStore.Add("CatHome4", 2, "订阅推送跳过（订阅猫未运行）: " + g.Name + " | " + sub, "GROUP");
                    continue;
                }
                long lastSeq = batch[batch.Count - 1].Seq;
                string content = BuildSummary(g.Name, batch);
                string origin = "group:" + g.Name + ":" + lastSeq.ToString() + ":auto";
                if (!cat.Session.PostUserMessage(content, "user", "", origin))
                {
                    LogStore.Add("CatHome4", 2, "订阅推送未受理: " + g.Name + " | " + sub, "GROUP");
                    continue;
                }
                // [段2] 游标推进——落盘失败出声（下次窗口会重推该区间，不静默丢）
                string setResult = GroupStore.SetSubscriberCursor(g.Name, sub, lastSeq);
                if (!setResult.StartsWith("ok|", StringComparison.Ordinal))
                {
                    LogStore.Add("CatHome4", 2, "订阅游标未写入: " + g.Name + " | " + sub + " | " + setResult, "GROUP");
                }
                LogStore.Add("CatHome4", 1, "订阅推送: " + g.Name + " | " + sub + " | 未读 " + batch.Count.ToString() + " 条 → #" + lastSeq.ToString(), "GROUP");
            }
        }

        /// <summary>
        /// 汇总正文——组名 + 区间头 + 逐条（#seq [类型] 发方 → 目标 · 正文 · 关联）。
        /// </summary>
        /// <param name="groupName">组名</param>
        /// <param name="batch">未读消息（seq 升序）</param>
        /// <returns>注入正文</returns>
        private static string BuildSummary(string groupName, List<GroupMessage> batch)
        {
            StringBuilder sb = new StringBuilder();
            sb.Append("[来自组:").Append(groupName).Append("]\n");
            sb.Append("（订阅汇总 · 未读 ").Append(batch.Count.ToString()).Append(" 条 · #");
            sb.Append(batch[0].Seq.ToString()).Append("–#").Append(batch[batch.Count - 1].Seq.ToString()).Append("）");
            for (int i = 0; i < batch.Count; i = i + 1)
            {
                GroupMessage m = batch[i];
                sb.Append("\n#").Append(m.Seq.ToString());
                sb.Append(" [").Append(TypeLabel(m.Type)).Append("] ");
                sb.Append(DisplayNameOf(m.From));
                if (m.To != null && m.To.Length > 0)
                {
                    sb.Append(" → @").Append(DisplayNameOf(m.To));
                }
                sb.Append(" · ").Append(Flatten(m.Text));
                if (m.Refs != null && m.Refs.Length > 0)
                {
                    sb.Append("（关联 ").Append(m.Refs).Append("）");
                }
            }
            return sb.ToString();
        }

        /// <summary>
        /// 正文单行化——换行折成空格（汇总里一行一条）。
        /// </summary>
        /// <param name="text">原文</param>
        /// <returns>单行文本</returns>
        private static string Flatten(string text)
        {
            if (text == null)
            {
                return "";
            }
            return text.Replace("\r", " ").Replace("\n", " ");
        }

        /// <summary>
        /// 消息类型中文标签。
        /// </summary>
        /// <param name="type">消息类型</param>
        /// <returns>标签文本</returns>
        private static string TypeLabel(string type)
        {
            if (string.Equals(type, "delegation", StringComparison.Ordinal))
            {
                return "派单";
            }
            if (string.Equals(type, "receipt", StringComparison.Ordinal))
            {
                return "回执";
            }
            return "发言";
        }

        /// <summary>
        /// 显示名——空 key（人 / 外部）回「人」。
        /// </summary>
        /// <param name="catKey">猫 key</param>
        /// <returns>显示名</returns>
        private static string DisplayNameOf(string catKey)
        {
            if (catKey == null || catKey.Length == 0)
            {
                return "人";
            }
            string name = CatHostService.ResolveCatDisplayName(catKey);
            if (name == null || name.Length == 0)
            {
                return catKey;
            }
            return name;
        }
    }
}
