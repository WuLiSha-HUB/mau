// NRT 抑制（CSharp exp §1.16）——命令解析面：JSON 取值链在分支内判空，逐处加 ? 会把解析淹掉。
#nullable disable warnings

using System;
using System.Collections.Generic;
using System.Text;
using System.Text.Json;
using Mau.Runtime;

namespace Mau.Providers
{
    /// <summary>
    /// 一条硬编码命令的执行上下文（A379）——服务层在事件路径上组装，命令层只读不用宿主内部状态。
    /// </summary>
    public sealed class QqBotCommandContext
    {
        /// <summary>配置池身份</summary>
        public string BotKey = "";

        /// <summary>QQ 开放平台 AppID</summary>
        public string AppId = "";

        /// <summary>显示名</summary>
        public string DisplayName = "";

        /// <summary>沙箱标志</summary>
        public bool Sandbox = false;

        /// <summary>本 bot 见过的群 OpenID 列表（留档去重后的结果）</summary>
        public List<string> KnownGroups = new List<string>();

        /// <summary>bot_state 取数委托（群 OpenID → 原始响应 JSON；失败返回空串）</summary>
        public Func<string, string> BotStateFetcher = null;

        /// <summary>收到事件到执行命令的耗时（毫秒）——/ping 的报告值</summary>
        public long ElapsedMs = 0;
        /// <summary>已知群的目录条目（群 OpenID → 条目）——群真实名与权限快照由此取；缺项 = 该群尚未刷新过官方状态</summary>
        public Dictionary<string, QqBotGroupEntry> GroupEntries = new Dictionary<string, QqBotGroupEntry>();

        /// <summary>当前在线的机器人清单（名称 + AppID）——/homeinfo 的全局面，与哪只机器人收到指令无关</summary>
        public List<QqBotOnlineBot> OnlineBots = new List<QqBotOnlineBot>();
    }

    /// <summary>
    /// 在线机器人的一行（/homeinfo「在线机器人」表格用）——只放对外可展示的两项。
    /// </summary>
    public sealed class QqBotOnlineBot
    {
        /// <summary>显示名</summary>
        public string DisplayName = "";

        /// <summary>QQ 开放平台 AppID</summary>
        public string AppId = "";
    }

    /// <summary>
    /// 硬编码命令层（A379）——群内 @ 且以 / 开头的消息、私聊以 / 开头的消息走此通道，不经 LLM、不走 OA。
    /// 现行四条：/ping（处理耗时）· /help（指令集）· /botinfo（本 bot 所在群与权限）· /homeinfo（宿主环境 + 在线机器人）。
    /// 未知指令回提示，不静默——失败必须可见。
    /// </summary>
    public static class QqBotCommands
    {
        /// <summary>
        /// 判断消息是否为命令——以 / 开头（去前导空白后）。
        /// </summary>
        /// <param name="content">消息正文</param>
        /// <returns>true=命令</returns>
        public static bool IsCommand(string content)
        {
            if (content == null)
            {
                return false;
            }
            string t = content.Trim();
            return t.Length > 0 && t[0] == '/';
        }

        /// <summary>
        /// 执行命令——返回值即要回复的正文（命令层不负责发送，也不做发送失败处理）。
        /// </summary>
        /// <param name="content">消息正文（含 / 前缀）</param>
        /// <param name="ctx">执行上下文</param>
        /// <returns>回复正文（未知指令返回提示文本）</returns>
        public static string Handle(string content, QqBotCommandContext ctx)
        {
            string t = content == null ? "" : content.Trim();
            if (t.Length == 0 || t[0] != '/')
            {
                return "**仅支持 / 指令**，使用 `/help` 查看指令集";
            }
            int space = t.IndexOf(' ');
            string name = space > 0 ? t.Substring(0, space) : t;
            string arg = space > 0 ? t.Substring(space + 1).Trim() : "";
            string lower = name.ToLowerInvariant();
            if (lower == "/ping")
            {
                return BuildPing(ctx);
            }
            if (lower == "/help")
            {
                return BuildHelp();
            }
            if (lower == "/botinfo")
            {
                return BuildBotInfo(ctx, arg);
            }
            if (lower == "/homeinfo")
            {
                return BuildHomeInfo(ctx);
            }
            return "未知指令 **" + name + "**，使用 `/help` 查看指令集";
        }

        /// <summary>
        /// /ping——本机处理耗时（网络往返无法从事件侧测得，此处只报本地耗时）。
        /// </summary>
        /// <param name="ctx">执行上下文</param>
        /// <returns>回复正文</returns>
        private static string BuildPing(QqBotCommandContext ctx)
        {
            return "**pong** · " + ctx.ElapsedMs.ToString() + " ms（本机处理耗时）";
        }

        /// <summary>
        /// /help——指令集与渠道约定。
        /// </summary>
        /// <returns>回复正文</returns>
        private static string BuildHelp()
        {
            StringBuilder sb = new StringBuilder();
            sb.Append("# 可用指令\n");
            sb.Append("- **/ping** — 本机处理耗时\n");
            sb.Append("- **/help** — 本指令集\n");
            sb.Append("- **/botinfo** — 本机器人所在群与权限\n");
            sb.Append("- **/homeinfo** — 宿主版本 · 时间 · 系统\n");
            sb.Append("\n> 群内仅响应 @ 我的 / 指令，不参与闲聊。");
            return sb.ToString();
        }

        /// <summary>
        /// /botinfo——本 bot 所在群 + 每群的收信范围与主动发言权（官方 bot_state 优先，不可用时降级本地观测并明示）。
        /// </summary>
        /// <param name="ctx">执行上下文</param>
        /// <param name="arg">指令参数（可指定单个群 OpenID）</param>
        /// <returns>回复正文</returns>
        private static string BuildBotInfo(QqBotCommandContext ctx, string arg)
        {
            StringBuilder sb = new StringBuilder();
            sb.Append("# Bot 信息\n");
            sb.Append("- **名称**：").Append(ctx.DisplayName).Append("\n");
            sb.Append("- **AppID**：").Append(ctx.AppId).Append("\n");
            sb.Append("- **环境**：").Append(ctx.Sandbox ? "沙箱" : "正式");
            List<string> groups = ctx.KnownGroups;
            if (groups == null || groups.Count == 0)
            {
                sb.Append("\n- **已知群**：0（尚未在群内收到消息）");
                return sb.ToString();
            }
            sb.Append("\n- **已知群**：").Append(groups.Count.ToString());
            for (int i = 0; i < groups.Count; i = i + 1)
            {
                string gid = groups[i];
                if (arg.Length > 0 && !string.Equals(arg, gid, StringComparison.Ordinal))
                {
                    continue;
                }
                // A386——群标题用群真实名（无群名时退 OpenID 末 8 位），不对外暴露内部别名
                sb.Append("\n\n## 群 ").Append(GroupDisplayName(ctx, gid));
                string raw = ctx.BotStateFetcher == null ? "" : ctx.BotStateFetcher(gid);
                string parsed = ParseBotState(raw, out bool official);
                if (official)
                {
                    sb.Append("\n").Append(parsed);
                    continue;
                }
                // 官方接口不可用——取目录缓存（A384 刷新时点）的本地观测值，不把未知报成「关闭」
                QqBotGroupEntry entry = LookupEntry(ctx, gid);
                if (HasPermissionSnapshot(entry))
                {
                    sb.Append("\n").Append(BuildPermissionFromEntry(entry));
                    sb.Append("\n> 官方接口不可用（").Append(parsed.Length > 0 ? parsed : "无响应").Append("），以上取自本地观测");
                    sb.Append("（").Append(entry.LastSeen.Length > 0 ? entry.LastSeen : "刷新时点未知").Append("）");
                    continue;
                }
                sb.Append("\n> 权限未知（官方接口不可用 · ").Append(parsed.Length > 0 ? parsed : "无响应").Append("）");
            }
            return sb.ToString();
        }

        /// <summary>
        /// 解析 bot_state 响应——返回可读摘要；official=false 表示官方接口未给成功体（含 11253 无权限）。
        /// </summary>
        /// <param name="raw">原始响应 JSON</param>
        /// <param name="official">出参——是否取到官方数据</param>
        /// <returns>可读摘要（失败时为失败原因）</returns>
        private static string ParseBotState(string raw, out bool official)
        {
            official = false;
            if (raw == null || raw.Trim().Length == 0)
            {
                return "无响应";
            }
            try
            {
                using (JsonDocument d = JsonUtil.ParseStrict(raw))
                {
                    JsonElement root = d.RootElement;
                    JsonElement e;
                    if (root.TryGetProperty("code", out e) && e.ValueKind == JsonValueKind.Number)
                    {
                        string code = e.GetRawText();
                        string msg = "";
                        if (root.TryGetProperty("message", out e))
                        {
                            msg = e.GetString() ?? "";
                        }
                        return "code=" + code + " " + msg;
                    }
                    string role = "";
                    if (root.TryGetProperty("member_role", out e))
                    {
                        role = e.GetString() ?? "";
                    }
                    string recv = "";
                    if (root.TryGetProperty("recv_msg_setting", out e))
                    {
                        recv = e.GetString() ?? "";
                    }
                    // A386——三态：字段缺失即未知，不把「没取到」报成「关闭」
                    bool hasProactive = false;
                    bool proactive = false;
                    if (root.TryGetProperty("allow_proactive_msg", out e)
                        && (e.ValueKind == JsonValueKind.True || e.ValueKind == JsonValueKind.False))
                    {
                        proactive = e.GetBoolean();
                        hasProactive = true;
                    }
                    string proactiveText = "未知";
                    if (hasProactive)
                    {
                        proactiveText = proactive ? "允许" : "不允许";
                    }
                    official = true;
                    StringBuilder sb = new StringBuilder();
                    sb.Append("- **角色**：").Append(role.Length > 0 ? role : "未知");
                    sb.Append("\n- **收信范围**：").Append(recv.Length > 0 ? recv : "未知");
                    sb.Append("\n- **主动发言**：").Append(proactiveText);
                    return sb.ToString();
                }
            }
            catch (Exception ex)
            {
                return "解析失败: " + ex.Message;
            }
        }

        /// <summary>
        /// /homeinfo——QQBot 所在宿主的全局摘要：版本 / 时间 / 系统 / 在线机器人四块。
        /// 面向群里的外部人——查的是「这台 CatHome 上跑着什么」，不含任何猫级 / 会话级数据
        /// （受控根 / 计量 / 加载包 / 域记录都绑定会话，Cat 不指定会话，故不收集——2026-11-16 莎裁）。
        /// </summary>
        /// <param name="ctx">执行上下文（在线机器人清单由此取）</param>
        /// <returns>回复正文</returns>
        private static string BuildHomeInfo(QqBotCommandContext ctx)
        {
            StringBuilder sb = new StringBuilder();
            sb.Append("# CH4 宿主状态");
            sb.Append("\n\n## 版本");
            string version = global::Mau.Runtime.VersionInfo.GetEntryVersion();
            sb.Append("\n- **版本**：").Append(version.Length > 0 ? version : "?");
            string buildAt = global::Mau.Runtime.VersionInfo.GetEntryBuildTime();
            if (buildAt.Length > 0)
            {
                sb.Append("\n- **编译时刻**：").Append(buildAt);
            }
            sb.Append("\n\n## 时间");
            sb.Append("\n- **当前时间**：").Append(DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss"));
            sb.Append("\n\n## 系统");
            sb.Append("\n- **描述**：").Append(System.Runtime.InteropServices.RuntimeInformation.OSDescription);
            sb.Append("\n- **架构**：").Append(System.Runtime.InteropServices.RuntimeInformation.OSArchitecture.ToString());
            sb.Append("\n- **运行时**：").Append(System.Runtime.InteropServices.RuntimeInformation.FrameworkDescription);
            sb.Append("\n\n## 在线机器人");
            List<QqBotOnlineBot> bots = ctx.OnlineBots;
            if (bots == null || bots.Count == 0)
            {
                sb.Append("\n- （无在线机器人）");
            }
            else
            {
                sb.Append("\n| 名称 | AppID |");
                sb.Append("\n|:--|:--|");
                for (int i = 0; i < bots.Count; i = i + 1)
                {
                    sb.Append("\n| ").Append(bots[i].DisplayName).Append(" | ").Append(bots[i].AppId).Append(" |");
                }
            }
            return sb.ToString();
        }
        /// <summary>
        /// 群显示名——群目录里的真实群名优先，无名时退 OpenID 末 8 位（内部别名不对外）。
        /// </summary>
        /// <param name="ctx">执行上下文</param>
        /// <param name="groupOpenId">群 OpenID</param>
        /// <returns>展示用群名</returns>
        private static string GroupDisplayName(QqBotCommandContext ctx, string groupOpenId)
        {
            QqBotGroupEntry entry = LookupEntry(ctx, groupOpenId);
            if (entry != null && entry.Title.Length > 0)
            {
                return entry.Title;
            }
            string id = groupOpenId == null ? "" : groupOpenId;
            if (id.Length > 8)
            {
                return id.Substring(id.Length - 8);
            }
            return id;
        }
        /// <summary>
        /// 取群目录条目——上下文未带目录或该群无条目时返回 null。
        /// </summary>
        /// <param name="ctx">执行上下文</param>
        /// <param name="groupOpenId">群 OpenID</param>
        /// <returns>条目或 null</returns>
        private static QqBotGroupEntry LookupEntry(QqBotCommandContext ctx, string groupOpenId)
        {
            if (ctx.GroupEntries == null || groupOpenId == null)
            {
                return null;
            }
            QqBotGroupEntry entry = null;
            ctx.GroupEntries.TryGetValue(groupOpenId, out entry);
            return entry;
        }
        /// <summary>
        /// 条目是否带权限快照——收信范围 / 角色 / 主动发言任一有值即视为本地观测过一次。
        /// </summary>
        /// <param name="entry">群条目（可空）</param>
        /// <returns>true=可展示本地观测值</returns>
        private static bool HasPermissionSnapshot(QqBotGroupEntry entry)
        {
            if (entry == null)
            {
                return false;
            }
            return entry.RecvSetting.Length > 0 || entry.Role.Length > 0 || entry.AllowProactive;
        }
        /// <summary>
        /// 本地观测权限摘要——与工具面同口径三态（允许 / 不允许 / 未知），未知不报成否定。
        /// </summary>
        /// <param name="entry">群条目</param>
        /// <returns>三行 Markdown 列表</returns>
        private static string BuildPermissionFromEntry(QqBotGroupEntry entry)
        {
            StringBuilder sb = new StringBuilder();
            sb.Append("- **角色**：").Append(entry.Role.Length > 0 ? entry.Role : "未知");
            sb.Append("\n- **收信范围**：").Append(entry.RecvSetting.Length > 0 ? entry.RecvSetting : "未知");
            string proactive = "未知";
            if (entry.AllowProactive)
            {
                proactive = "允许";
            }
            else
            {
                if (entry.RecvSetting.Length > 0)
                {
                    proactive = "不允许";
                }
            }
            sb.Append("\n- **主动发言**：").Append(proactive);
            return sb.ToString();
        }
    }
}
