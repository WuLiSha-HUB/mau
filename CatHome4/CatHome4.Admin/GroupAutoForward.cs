using System;
using CH4;
using Mau.Runtime;

namespace CatHome4.Admin
{
    /// <summary>
    /// 组消息自动转发（订阅面）——组内新消息按 group.json 的 subscriberKeys 投递给订阅猫。
    /// 语义：发方自己跳过 · 未运行订阅猫出声跳过 · origin 第 4 段为 auto（轮末不回执 → 零环）。
    /// </summary>
    public static class GroupAutoForward
    {
        /// <summary>
        /// 追加钩子处理——GroupStore.MessageAppended 的接线实现（锁外调用）。
        /// </summary>
        /// <param name="groupName">组名</param>
        /// <param name="msg">已落盘消息</param>
        public static void Handle(string groupName, GroupMessage msg)
        {
            if (msg == null || groupName == null || groupName.Length == 0)
            {
                return;
            }
            GroupSummary g = GroupStore.GetGroup(groupName);
            if (g == null || g.SubscriberKeys.Length == 0)
            {
                return;
            }
            string fromKey = msg.From == null ? "" : msg.From;
            for (int i = 0; i < g.SubscriberKeys.Length; i = i + 1)
            {
                string sub = g.SubscriberKeys[i];
                if (sub.Length == 0 || string.Equals(sub, fromKey, StringComparison.Ordinal))
                {
                    continue;
                }
                CatHostService.CatEntry cat = CatHostService.FindCat(sub);
                if (cat == null || cat.Session == null)
                {
                    LogStore.Add("CatHome4", 2, "组自动转发跳过（订阅猫未运行）: " + groupName + " | " + sub, "GROUP");
                    continue;
                }
                string content = "[来自组:" + groupName + "]\n（自动转发 · " + TypeLabel(msg.Type) + " · " + DisplayNameOf(fromKey) + "）\n" + msg.Text;
                string origin = "group:" + groupName + ":" + msg.Seq.ToString() + ":auto";
                if (!cat.Session.PostUserMessage(content, "user", "", origin))
                {
                    LogStore.Add("CatHome4", 2, "组自动转发未受理: " + groupName + " | " + sub, "GROUP");
                }
            }
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
        /// 发送方显示名——空 key（系统 / 管理员）回「人」。
        /// </summary>
        /// <param name="catKey">发送方猫 key</param>
        /// <returns>显示名</returns>
        private static string DisplayNameOf(string catKey)
        {
            if (catKey == null || catKey.Length == 0)
            {
                return "人";
            }
            string name = CatHostService.ResolveCatDisplayName(catKey);
            return (name == null || name.Length == 0) ? catKey : name;
        }
    }
}
