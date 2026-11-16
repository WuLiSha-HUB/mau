using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using Mau.Contracts;
using Mau.Runtime;
using static CatHome4.Admin.AdminService;

namespace CatHome4.Admin
{
    /// <summary>
    /// 猫级 QQ 渠道分部（A381）——cat.cfg 绑定解析 + 群 @ 推送投递。
    /// 通道服务（Mau.Providers）不认识猫：绑定解析与投递形态经本分部两个静态入口桥接（宿主启动链注入委托）。
    /// 投递走 user 入口（PostUserMessage source=user、origin 留空）——对目标猫而言这就是「有人在跟我说话」，
    /// 不是系统注入：它开启一轮正常对话，也会出现在用户消息气泡里。
    /// </summary>
    internal static partial class CatHostService
    {
        /// <summary>
        /// 猫级绑定解析（A381）——工具面（自述 / 发言授权）用；无 cat.cfg 返回 null。
        /// </summary>
        /// <param name="catId">猫 key（特殊会话 majordomo 走其 cat.cfg）</param>
        /// <returns>绑定；无配置面 null</returns>
        internal static QqBotCatBinding ResolveQqBinding(string catId)
        {
            if (catId == null || catId.Length == 0)
            {
                return null;
            }
            CatCfgData cfg = LoadCatCfg(Path.Combine(_dataRoot, "Data", "sessions", catId, "cat.cfg"));
            if (cfg == null)
            {
                return null;
            }
            QqBotCatBinding binding = new QqBotCatBinding();
            binding.BotKey = cfg.QqBotId == null ? "" : cfg.QqBotId;
            binding.Enable = cfg.QqBotEnable;
            binding.Groups = cfg.QqBotGroups == null ? new string[0] : cfg.QqBotGroups;
            binding.CanSend = cfg.QqBotCanSend;
            return binding;
        }

        /// <summary>
        /// 群 @ 推送投递（A381）——按 cat.cfg 绑定 + 群白名单分发给运行中的猫（user 入口）。
        /// 无人订阅不算失败（返回空串）；有订阅但投递未受理才出声。
        /// </summary>
        /// <param name="botKey">配置池机器人身份</param>
        /// <param name="groupOpenId">群 OpenID</param>
        /// <param name="groupAlias">群别名（未知空串）</param>
        /// <param name="groupTitle">群名称（未知空串）</param>
        /// <param name="senderName">发送人显示名</param>
        /// <param name="text">消息正文</param>
        /// <returns>空串=有猫受理 · "NO_TARGET"=无人订阅 · 其余=失败说明</returns>
        internal static string PushQqMention(string botKey, string groupOpenId, string groupAlias, string groupTitle, string senderName, string text)
        {
            List<string> targets = new List<string>();
            for (int i = 0; i < _cats.Count; i = i + 1)
            {
                CatEntry cat = _cats[i];
                if (MatchQqTarget(cat.QqBotId, cat.QqBotEnable, cat.QqBotGroups, botKey, groupOpenId, groupAlias, groupTitle))
                {
                    targets.Add(cat.Id);
                }
            }
            // 默认会话不在 _cats 注册表（主端口专属）——单独读 cat.cfg 判一次
            CatCfgData majorCfg = LoadCatCfg(Path.Combine(_dataRoot, "Data", "sessions", "majordomo", "cat.cfg"));
            if (majorCfg != null
                && MatchQqTarget(majorCfg.QqBotId, majorCfg.QqBotEnable, majorCfg.QqBotGroups, botKey, groupOpenId, groupAlias, groupTitle))
            {
                targets.Add("majordomo");
            }
            if (targets.Count == 0)
            {
                // 无人订阅不是失败——服务侧据此回「仅支持 / 指令」提示（A381：有猫接就静默）
                return "NO_TARGET";
            }
            string label = GroupLabel(groupAlias, groupTitle, groupOpenId);
            string content = "[来自QQ:" + label + "]\n" + (senderName == null || senderName.Length == 0 ? "（未知发送人）" : senderName) + "：" + text;
            StringBuilder failed = new StringBuilder();
            for (int i = 0; i < targets.Count; i = i + 1)
            {
                string key = targets[i];
                CatEntry cat = FindDeliveryCat(key);
                if (cat == null || cat.Session == null)
                {
                    failed.Append(failed.Length > 0 ? " / " : "");
                    failed.Append(key + "（未运行）");
                    continue;
                }
                if (!cat.Session.PostUserMessage(content, "user", "", ""))
                {
                    failed.Append(failed.Length > 0 ? " / " : "");
                    failed.Append(key + "（未受理）");
                    continue;
                }
                LogStore.Add("QQBot", 1, "QQBot | 群 @" + Truncate(label, 16) + " → 猫 " + key, "QQBOT");
            }
            if (failed.Length == 0)
            {
                return "";
            }
            return "推送未全部受理: " + failed.ToString();
        }

        /// <summary>
        /// 推送命中判据（A381）——绑定机器人一致 / 收信开 / 群在白名单内（空名单=全目录）。
        /// </summary>
        /// <param name="botKey">该猫绑定的机器人</param>
        /// <param name="enable">收信开关</param>
        /// <param name="groups">群白名单</param>
        /// <param name="targetBotKey">事件所属机器人</param>
        /// <param name="groupOpenId">群 OpenID</param>
        /// <param name="groupAlias">群别名</param>
        /// <param name="groupTitle">群名称</param>
        /// <returns>true=应推送</returns>
        private static bool MatchQqTarget(string botKey, bool enable, string[] groups, string targetBotKey, string groupOpenId, string groupAlias, string groupTitle)
        {
            if (!enable)
            {
                return false;
            }
            if (botKey == null || botKey.Length == 0)
            {
                return false;
            }
            if (!string.Equals(botKey, targetBotKey, StringComparison.OrdinalIgnoreCase))
            {
                return false;
            }
            if (groups == null || groups.Length == 0)
            {
                return true;
            }
            for (int i = 0; i < groups.Length; i = i + 1)
            {
                string item = groups[i];
                if (item == null || item.Length == 0)
                {
                    continue;
                }
                if (string.Equals(item, groupOpenId, StringComparison.OrdinalIgnoreCase)
                    || string.Equals(item, groupAlias, StringComparison.OrdinalIgnoreCase)
                    || string.Equals(item, groupTitle, StringComparison.OrdinalIgnoreCase))
                {
                    return true;
                }
            }
            return false;
        }

        /// <summary>
        /// 群显示标签（A381）——别名优先，其次群名，末 8 位兜底（推送正文前缀用）。
        /// </summary>
        /// <param name="alias">群别名</param>
        /// <param name="title">群名称</param>
        /// <param name="groupOpenId">群 OpenID</param>
        /// <returns>标签</returns>
        private static string GroupLabel(string alias, string title, string groupOpenId)
        {
            if (alias != null && alias.Length > 0)
            {
                return alias;
            }
            if (title != null && title.Length > 0)
            {
                return title;
            }
            if (groupOpenId == null)
            {
                return "";
            }
            if (groupOpenId.Length <= 8)
            {
                return groupOpenId;
            }
            return groupOpenId.Substring(groupOpenId.Length - 8);
        }

        /// <summary>
        /// 日志文本截断（A381）——超长裁剪，仅用于日志行可读性。
        /// </summary>
        /// <param name="text">原文本</param>
        /// <param name="max">最大长度</param>
        /// <returns>裁剪结果</returns>
        private static string Truncate(string text, int max)
        {
            if (text == null)
            {
                return "";
            }
            if (max <= 0 || text.Length <= max)
            {
                return text;
            }
            return text.Substring(0, max);
        }

    }
}
