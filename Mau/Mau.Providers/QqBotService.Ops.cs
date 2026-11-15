#nullable disable warnings
using System;
using System.Collections.Generic;
using Mau.Contracts;
using Mau.Runtime;

namespace Mau.Providers
{
    /// <summary>
    /// QQ 渠道服务·业务面分部（A380）——工具组执行面：发言寻址（别名解析）、通道自述、群目录维护。
    /// 与生命周期面（Start / Stop / Shutdown）同宿主同连接表；一次实现 <see cref="IQQBotOps"/> 挂到 DataBox。
    /// </summary>
    public sealed partial class QqBotService : IQQBotOps
    {
        /// <summary>群目录（Data/qq/bots/&lt;appId&gt;/groups.json）——Configure 时创建。</summary>
        private QqBotGroupDirectory _directory;

        /// <summary>
        /// 群目录实例（宿主未 Configure 时为 null）。
        /// </summary>
        public QqBotGroupDirectory Directory
        {
            get
            {
                return _directory;
            }
        }

        /// <summary>
        /// 主动发言——群寻址（别名 / 群名 / OpenID / 唯一前缀）→ 池白名单校验 → 发送 → 回执带实际目标。
        /// </summary>
        /// <param name="botKey">配置池身份（空=默认机器人）</param>
        /// <param name="groupRef">群寻址（空=目录唯一群）</param>
        /// <param name="text">正文</param>
        /// <returns>回执文本（失败以 ERR| 开头）</returns>
        public string SendProactiveTo(string botKey, string groupRef, string text)
        {
            if (text == null || text.Trim().Length == 0)
            {
                return "ERR|BAD_ARGS|正文为空";
            }
            string key = botKey == null ? "" : botKey.Trim();
            if (key.Length == 0)
            {
                key = ResolveDefaultBotKey();
            }
            if (key.Length == 0)
            {
                return "ERR|NO_DEFAULT_BOT|未指定机器人且配置池无默认项";
            }
            QqBotConnection conn = ResolveConnection(key);
            if (conn == null)
            {
                return "ERR|BOT_NOT_CONNECTED|机器人未连接（" + key + "）——检查池配置、AppSecret 与宿主启动日志";
            }
            CH_QqBotConfigStore pool;
            DataBox.TryResolve<CH_QqBotConfigStore>(out pool);
            CH_QqBotConfig cfg = null;
            if (pool != null)
            {
                Guid id;
                if (Guid.TryParse(key, out id))
                {
                    pool.TryGet(id, out cfg);
                }
            }
            if (_directory == null)
            {
                return "ERR|DIRECTORY_NOT_READY|群目录未初始化（宿主未完成 Configure）";
            }
            QqBotGroupEntry entry;
            string candidates;
            string reference = groupRef == null ? "" : groupRef.Trim();
            if (reference.Length == 0)
            {
                QqBotGroupDocument doc = _directory.Load(conn.AppId);
                if (doc.Groups.Count != 1)
                {
                    return "ERR|GROUP_REQUIRED|未指定群，且目录内群数为 " + doc.Groups.Count.ToString()
                        + (doc.Groups.Count > 1 ? "（候选: " + QqBotGroupDirectory.Describe(doc.Groups) + "）" : "");
                }
                entry = doc.Groups[0];
            }
            else if (!_directory.TryResolve(conn.AppId, reference, out entry, out candidates))
            {
                return "ERR|GROUP_NOT_FOUND|" + reference + (candidates.Length > 0 ? "（候选: " + candidates + "）" : "（目录为空，先让机器人在群里收到一条消息，或用 AddGroup 登记）");
            }
            string gate = CheckAllowed(cfg, conn.AppId, entry);
            if (gate.Length > 0)
            {
                return gate;
            }
            string err = conn.SendMessage("group", entry.OpenId, text, "", 1, true);
            string label = QqBotGroupDirectory.Label(entry);
            string tail = Tail8(entry.OpenId);
            string target = string.Equals(label, tail, StringComparison.OrdinalIgnoreCase) ? label : (label + " · " + tail);
            if (err.Length > 0)
            {
                LogStore.Add("QQBot", 3, "QQBot | " + conn.DisplayName + " | 主动发言失败 → " + target + " | " + err, "QQBOT");
                return err + "\n目标: " + target;
            }
            // 主动发言同样进 @ 发出留档——留档缺口会让「发过什么」只剩运行日志可疑
            AppendMentionOut(conn.AppId, key, conn.DisplayName, "proactive-group", entry.OpenId, target, text);
            string proactive = entry.AllowProactive ? "允许" : "未知（官方接口未确认）";
            return target + " | 主动消息已投递"
                + "\n机器人: " + conn.DisplayName + " · 群内主动发言权限: " + proactive;
        }

        /// <summary>
        /// 本通道自述——池内每个机器人：连接状态 / 环境 / 群目录摘要。
        /// </summary>
        /// <param name="catId">调用者猫 key（A381 前仅记录，不参与过滤）</param>
        /// <returns>多行文本</returns>
        public string MyChannelText(string catId)
        {
            CH_QqBotConfigStore pool;
            DataBox.TryResolve<CH_QqBotConfigStore>(out pool);
            if (pool == null)
            {
                return "ERR|POOL_NOT_READY|配置池未注入";
            }
            CH_QqBotConfig[] bots = pool.GetAll();
            if (bots.Length == 0)
            {
                return "QQBot | 池内无机器人配置（控制台 config 页 →「QQ 机器人池」新建）";
            }
            List<string> lines = new List<string>();
            lines.Add("QQBot 通道 · 猫: " + (catId == null || catId.Length == 0 ? "(未注入)" : catId) + " · 机器人 " + bots.Length.ToString() + " 个");
            for (int i = 0; i < bots.Length; i = i + 1)
            {
                CH_QqBotConfig cfg = bots[i];
                string key = cfg.QqBotId.ToString("D");
                QqBotConnection conn = ResolveConnection(key);
                string state = conn == null ? "未连接" : "已连接";
                string groups = "";
                if (_directory != null)
                {
                    groups = QqBotGroupDirectory.Describe(_directory.Load(cfg.AppId).Groups);
                }
                lines.Add("· " + cfg.DisplayName + (cfg.IsDefault ? "（默认）" : "") + " · " + cfg.ApiEnv
                    + " · " + state + " · 群: " + (groups.Length > 0 ? groups : "（无）"));
            }
            return string.Join("\n", lines.ToArray());
        }

        /// <summary>
        /// 单机器人详情——群目录逐条（别名 / 群名 / 角色 / 收信范围 / 主动发言）。
        /// </summary>
        /// <param name="botKey">配置池身份（空=默认机器人）</param>
        /// <param name="refreshOfficial">true=调官方接口刷新权限与群名（失败降级并明示）</param>
        /// <returns>多行文本</returns>
        public string BotDetailText(string botKey, bool refreshOfficial)
        {
            string key = botKey == null ? "" : botKey.Trim();
            if (key.Length == 0)
            {
                key = ResolveDefaultBotKey();
            }
            if (key.Length == 0)
            {
                return "ERR|NO_DEFAULT_BOT|未指定机器人且配置池无默认项";
            }
            QqBotConnection conn = ResolveConnection(key);
            if (conn == null)
            {
                return "ERR|BOT_NOT_CONNECTED|机器人未连接（" + key + "）";
            }
            if (_directory == null)
            {
                return "ERR|DIRECTORY_NOT_READY|群目录未初始化";
            }
            QqBotGroupDocument doc = _directory.Load(conn.AppId);
            string degrade = "";
            int refreshed = 0;
            if (refreshOfficial && doc.Groups.Count > 0)
            {
                for (int i = 0; i < doc.Groups.Count; i = i + 1)
                {
                    string openId = doc.Groups[i].OpenId;
                    bool ok = RefreshOne(conn, openId);
                    if (ok)
                    {
                        refreshed = refreshed + 1;
                    }
                }
                if (refreshed < doc.Groups.Count)
                {
                    degrade = " · 官方接口不可用（11253 仅白名单机器人），部分或全部权限取自本地观测";
                }
                doc = _directory.Load(conn.AppId);
            }
            List<string> lines = new List<string>();
            lines.Add("QQBot | " + conn.DisplayName + " · AppID " + conn.AppId + " · 群 " + doc.Groups.Count.ToString() + " 个"
                + (refreshed > 0 ? " · 已刷新 " + refreshed.ToString() + " 群" : "") + degrade);
            for (int i = 0; i < doc.Groups.Count; i = i + 1)
            {
                QqBotGroupEntry e = doc.Groups[i];
                lines.Add("· " + QqBotGroupDirectory.Label(e) + " · " + Tail8(e.OpenId)
                    + " · 角色: " + (e.Role.Length > 0 ? e.Role : "未知")
                    + " · 收信: " + (e.RecvSetting.Length > 0 ? e.RecvSetting : "未知")
                    + " · 主动发言: " + (e.AllowProactive ? "允许" : (e.RecvSetting.Length > 0 ? "不允许" : "未知"))
                    + " · 最近: " + (e.LastSeen.Length > 0 ? e.LastSeen : "—"));
            }
            CH_QqBotConfigStore pool;
            DataBox.TryResolve<CH_QqBotConfigStore>(out pool);
            if (pool != null)
            {
                Guid id;
                CH_QqBotConfig cfg = null;
                if (Guid.TryParse(key, out id))
                {
                    pool.TryGet(id, out cfg);
                }
                if (cfg != null)
                {
                    string allowed = cfg.AllowedGroups == null || cfg.AllowedGroups.Length == 0
                        ? "未限制（目录内全部可发）"
                        : string.Join(" / ", cfg.AllowedGroups);
                    lines.Add("发言白名单: " + allowed);
                }
            }
            return string.Join("\n", lines.ToArray());
        }

        /// <summary>
        /// 人工登记群——没说过话的群也能进目录。
        /// </summary>
        /// <param name="botKey">配置池身份（空=默认机器人）</param>
        /// <param name="groupOpenId">群 OpenID</param>
        /// <returns>空串成功；否则 ERR|</returns>
        public string AddGroupToDirectory(string botKey, string groupOpenId)
        {
            QqBotConnection conn = ResolveByKey(botKey);
            if (conn == null)
            {
                return "ERR|BOT_NOT_CONNECTED|机器人未连接";
            }
            if (_directory == null)
            {
                return "ERR|DIRECTORY_NOT_READY|群目录未初始化";
            }
            string err = _directory.AddGroup(conn.AppId, groupOpenId);
            if (err.Length > 0)
            {
                return "ERR|DIR_ADD_FAIL|" + err;
            }
            return "";
        }

        /// <summary>
        /// 群别名维护——同一 bot 内唯一。
        /// </summary>
        /// <param name="botKey">配置池身份（空=默认机器人）</param>
        /// <param name="groupRef">群寻址</param>
        /// <param name="alias">新别名（空=清除）</param>
        /// <returns>空串成功；否则 ERR|</returns>
        public string SetGroupAlias(string botKey, string groupRef, string alias)
        {
            QqBotConnection conn = ResolveByKey(botKey);
            if (conn == null)
            {
                return "ERR|BOT_NOT_CONNECTED|机器人未连接";
            }
            if (_directory == null)
            {
                return "ERR|DIRECTORY_NOT_READY|群目录未初始化";
            }
            string err = _directory.SetAlias(conn.AppId, groupRef, alias);
            if (err.Length > 0)
            {
                return "ERR|ALIAS_FAIL|" + err;
            }
            return "";
        }

        /// <summary>
        /// 刷新单个群的官方状态与群名（bot_state + info）——任一接口不可用即返回 false。
        /// </summary>
        /// <param name="conn">连接</param>
        /// <param name="groupOpenId">群 OpenID</param>
        /// <returns>true=官方数据已写入目录</returns>
        private bool RefreshOne(QqBotConnection conn, string groupOpenId)
        {
            string stateRaw = conn.GetBotStateRaw(groupOpenId);
            bool allowProactive = false;
            string recvSetting = "";
            string role = "";
            bool gotState = false;
            if (stateRaw != null && stateRaw.Length > 0)
            {
                try
                {
                    using (System.Text.Json.JsonDocument d = JsonUtil.ParseStrict(stateRaw))
                    {
                        System.Text.Json.JsonElement e;
                        if (d.RootElement.TryGetProperty("allow_proactive_msg", out e)
                            && (e.ValueKind == System.Text.Json.JsonValueKind.True || e.ValueKind == System.Text.Json.JsonValueKind.False))
                        {
                            allowProactive = e.GetBoolean();
                            gotState = true;
                        }
                        if (d.RootElement.TryGetProperty("recv_msg_setting", out e) && e.ValueKind == System.Text.Json.JsonValueKind.String)
                        {
                            recvSetting = e.GetString();
                        }
                        if (d.RootElement.TryGetProperty("member_role", out e) && e.ValueKind == System.Text.Json.JsonValueKind.String)
                        {
                            role = e.GetString();
                        }
                    }
                }
                catch (Exception ex)
                {
                    LogStore.Add("QQBot", 2, "QQBot | bot_state 解析失败: " + ex.Message, "QQBOT");
                }
            }
            if (!gotState)
            {
                return false;
            }
            string title = "";
            string infoRaw = conn.GetGroupInfoRaw(groupOpenId);
            if (infoRaw != null && infoRaw.Length > 0)
            {
                try
                {
                    using (System.Text.Json.JsonDocument d = JsonUtil.ParseStrict(infoRaw))
                    {
                        System.Text.Json.JsonElement e;
                        if (d.RootElement.TryGetProperty("group_name", out e) && e.ValueKind == System.Text.Json.JsonValueKind.String)
                        {
                            title = e.GetString();
                        }
                    }
                }
                catch (Exception ex)
                {
                    LogStore.Add("QQBot", 2, "QQBot | 群信息解析失败: " + ex.Message, "QQBOT");
                }
            }
            _directory.ApplyOfficialState(conn.AppId, groupOpenId, title, allowProactive, recvSetting, role);
            return true;
        }

        /// <summary>
        /// 群目录 JSON——前端配置页渲染用（每群一条：openId / alias / title / titleSource / role / allowProactive / recvSetting / firstSeen / lastSeen）。
        /// </summary>
        /// <param name="botKey">配置池身份（空=默认机器人）</param>
        /// <returns>JSON 文本；失败以 ERR| 开头</returns>
        public string DirectoryJson(string botKey)
        {
            QqBotConnection conn = ResolveByKey(botKey);
            if (conn == null)
            {
                return "ERR|BOT_NOT_CONNECTED|机器人未连接（" + (botKey == null ? "" : botKey) + "）";
            }
            if (_directory == null)
            {
                return "ERR|DIRECTORY_NOT_READY|群目录未初始化";
            }
            QqBotGroupDocument doc = _directory.Load(conn.AppId);
            System.Text.StringBuilder sb = new System.Text.StringBuilder();
            sb.Append("{\"ok\":true,\"appId\":").Append(JsonUtil.Str(conn.AppId))
                .Append(",\"bot\":").Append(JsonUtil.Str(conn.DisplayName))
                .Append(",\"groups\":[");
            for (int i = 0; i < doc.Groups.Count; i = i + 1)
            {
                QqBotGroupEntry g = doc.Groups[i];
                if (i > 0)
                {
                    sb.Append(",");
                }
                sb.Append("{\"openId\":").Append(JsonUtil.Str(g.OpenId))
                    .Append(",\"alias\":").Append(JsonUtil.Str(g.Alias))
                    .Append(",\"title\":").Append(JsonUtil.Str(g.Title))
                    .Append(",\"titleSource\":").Append(JsonUtil.Str(g.TitleSource))
                    .Append(",\"role\":").Append(JsonUtil.Str(g.Role))
                    .Append(",\"allowProactive\":").Append(g.AllowProactive ? "true" : "false")
                    .Append(",\"recvSetting\":").Append(JsonUtil.Str(g.RecvSetting))
                    .Append(",\"firstSeen\":").Append(JsonUtil.Str(g.FirstSeen))
                    .Append(",\"lastSeen\":").Append(JsonUtil.Str(g.LastSeen))
                    .Append("}");
            }
            sb.Append("]}");
            return sb.ToString();
        }

        /// <summary>
        /// 群目录操作（工具面统一入口）——list / alias / add / refresh。
        /// </summary>
        /// <param name="botKey">配置池身份（空=默认机器人）</param>
        /// <param name="action">动作</param>
        /// <param name="groupRef">群寻址</param>
        /// <param name="alias">新别名（action=alias）</param>
        /// <returns>多行文本；失败以 ERR| 开头</returns>
        public string GroupsOp(string botKey, string action, string groupRef, string alias)
        {
            string act = action == null ? "" : action.Trim().ToLowerInvariant();
            if (act.Length == 0 || act == "list")
            {
                return BotDetailText(botKey, false);
            }
            if (act == "alias")
            {
                string aliasErr = SetGroupAlias(botKey, groupRef, alias);
                if (aliasErr.Length > 0)
                {
                    return aliasErr;
                }
                string cleared = alias == null || alias.Trim().Length == 0 ? "（已清除）" : alias.Trim();
                return "别名已设置: " + (groupRef == null ? "" : groupRef) + " → " + cleared;
            }
            if (act == "add")
            {
                string addErr = AddGroupToDirectory(botKey, groupRef);
                if (addErr.Length > 0)
                {
                    return addErr;
                }
                return "已登记群: " + (groupRef == null ? "" : groupRef) + "（刷新官方状态用 action=refresh）";
            }
            if (act == "refresh")
            {
                return RefreshOfficial(botKey);
            }
            return "ERR|BAD_ARGS|未知 action: " + act + "（list / alias / add / refresh）";
        }

        /// <summary>
        /// 刷新该机器人全部群的官方状态与群名（逐群 bot_state + info）。
        /// </summary>
        /// <param name="botKey">配置池身份（空=默认机器人）</param>
        /// <returns>多行文本（含每群刷新结果）</returns>
        public string RefreshOfficial(string botKey)
        {
            QqBotConnection conn = ResolveByKey(botKey);
            if (conn == null)
            {
                return "ERR|BOT_NOT_CONNECTED|机器人未连接";
            }
            if (_directory == null)
            {
                return "ERR|DIRECTORY_NOT_READY|群目录未初始化";
            }
            QqBotGroupDocument doc = _directory.Load(conn.AppId);
            if (doc.Groups.Count == 0)
            {
                return "机器人「" + conn.DisplayName + "」目录内暂无群——先让它在群里收到一条消息，或用 add 登记";
            }
            System.Text.StringBuilder sb = new System.Text.StringBuilder();
            sb.Append("刷新官方状态 · ").Append(conn.DisplayName).Append(" · 群 ").Append(doc.Groups.Count.ToString()).Append(" 个");
            int okCount = 0;
            for (int i = 0; i < doc.Groups.Count; i = i + 1)
            {
                QqBotGroupEntry g = doc.Groups[i];
                bool got = RefreshOne(conn, g.OpenId);
                if (got)
                {
                    okCount = okCount + 1;
                }
                sb.Append("\n· ").Append(QqBotGroupDirectory.Label(g)).Append(" · ").Append(Tail8(g.OpenId))
                    .Append(got ? " → 已刷新" : " → 官方接口不可用（保留本地观测）");
            }
            sb.Append("\n成功 ").Append(okCount.ToString()).Append(" / ").Append(doc.Groups.Count.ToString());
            return sb.ToString();
        }

        /// <summary>
        /// 白名单闸——池条目 allowedGroups 为空表示不限制；非空时目标群必须命中其中一项。
        /// </summary>
        /// <param name="cfg">池配置（可 null）</param>
        /// <param name="appId">AppID</param>
        /// <param name="entry">目标群</param>
        /// <returns>空串放行；否则 ERR|</returns>
        private string CheckAllowed(CH_QqBotConfig cfg, string appId, QqBotGroupEntry entry)
        {
            if (cfg == null || cfg.AllowedGroups == null || cfg.AllowedGroups.Length == 0)
            {
                return "";
            }
            for (int i = 0; i < cfg.AllowedGroups.Length; i = i + 1)
            {
                string item = cfg.AllowedGroups[i];
                if (item == null || item.Length == 0)
                {
                    continue;
                }
                if (string.Equals(item, entry.OpenId, StringComparison.OrdinalIgnoreCase)
                    || string.Equals(item, entry.Alias, StringComparison.OrdinalIgnoreCase))
                {
                    return "";
                }
                QqBotGroupEntry allowed;
                string ignored;
                if (_directory.TryResolve(appId, item, out allowed, out ignored) && allowed != null
                    && string.Equals(allowed.OpenId, entry.OpenId, StringComparison.OrdinalIgnoreCase))
                {
                    return "";
                }
            }
            return "ERR|GROUP_NOT_ALLOWED|该群不在发言白名单内（" + QqBotGroupDirectory.Label(entry)
                + "）· 白名单: " + string.Join(" / ", cfg.AllowedGroups);
        }

        /// <summary>
        /// 解析默认 botKey（池默认项）。
        /// </summary>
        /// <returns>botKey 或空串</returns>
        private string ResolveDefaultBotKey()
        {
            CH_QqBotConfigStore pool;
            DataBox.TryResolve<CH_QqBotConfigStore>(out pool);
            if (pool == null)
            {
                return "";
            }
            CH_QqBotConfig cfg = pool.ResolveDefault();
            if (cfg == null)
            {
                return "";
            }
            return cfg.QqBotId.ToString("D");
        }

        /// <summary>
        /// 按 botKey 取连接（空 key 用默认机器人）。
        /// </summary>
        /// <param name="botKey">配置池身份</param>
        /// <returns>连接或 null</returns>
        private QqBotConnection ResolveByKey(string botKey)
        {
            string key = botKey == null ? "" : botKey.Trim();
            if (key.Length == 0)
            {
                key = ResolveDefaultBotKey();
            }
            if (key.Length == 0)
            {
                return null;
            }
            return ResolveConnection(key);
        }

        /// <summary>
        /// OpenID 末 8 位——日志与回执里的短标识。
        /// </summary>
        /// <param name="openId">OpenID</param>
        /// <returns>短标识</returns>
        private static string Tail8(string openId)
        {
            if (openId == null || openId.Length == 0)
            {
                return "—";
            }
            return openId.Length > 8 ? openId.Substring(openId.Length - 8) : openId;
        }
    }
}
