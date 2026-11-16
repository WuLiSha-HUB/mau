// NRT 抑制（CSharp exp §1.16）——事件分拣面：JSON 取值链在分支内判空，逐处加 ? 会把分拣淹掉；
// 语义判空在消费点（字段缺失按空串处理，缺关键字段则该事件只留痕不路由）。
#nullable disable warnings

using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Text;
using System.Text.Json;
using Mau.Runtime;

namespace Mau.Providers
{
    /// <summary>
    /// QQ 渠道服务·事件分拣分部（A379）——一条帧进来后的全部去向：
    /// 群全量留档 → @ 判定（mentions.is_you）→ @ 收件留档 → 命令直执（/ 前缀）或提示回复 → 被动发送 → @ 发出留档。
    /// 🔴 判据（2026-11-15 真机实测）：开「接收全部消息」后 @ 消息也走 GROUP_MESSAGE_CREATE，
    /// 其 content 不剥 @ 前缀而是内嵌 &lt;@openid&gt; 占位符——是否 @ 我看 mentions 里的 is_you，正文清洗靠剥离占位符。
    /// </summary>
    public partial class QqBotService
    {
        /// <summary>单条消息事件的可读投影——解析一次，全链路复用</summary>
        private sealed class QqMessageInfo
        {
            /// <summary>事件名</summary>
            public string EventType = "";

            /// <summary>消息 ID（被动回复锚 msg_id）</summary>
            public string MsgId = "";

            /// <summary>事件序号（帧 s）</summary>
            public long Seq = 0;

            /// <summary>原始正文（可能含 &lt;@openid&gt; 占位符）</summary>
            public string Content = "";

            /// <summary>事件时间戳（RFC3339）</summary>
            public string Timestamp = "";

            /// <summary>群 OpenID（私聊为空）</summary>
            public string GroupOpenId = "";

            /// <summary>发送者 OpenID（群=member_openid / 私聊=user_openid）</summary>
            public string SenderOpenId = "";

            /// <summary>发送者显示名</summary>
            public string SenderName = "";

            /// <summary>群内角色（member / admin / owner）</summary>
            public string MemberRole = "";

            /// <summary>mentions 中是否含 is_you=true（仅群消息有意义）</summary>
            public bool MentionedMe = false;
        }

        /// <summary>
        /// 事件到达——分拣入口（连接层回调）。非消息类事件只留状态，不进分拣。
        /// </summary>
        /// <param name="botKey">配置池身份</param>
        /// <param name="displayName">显示名</param>
        /// <param name="eventType">事件名（空串=帧无 t）</param>
        /// <param name="raw">帧原文</param>
        private void OnEvent(string botKey, string displayName, string eventType, string raw)
        {
            long beginTick = Stopwatch.GetTimestamp();
            if (eventType == "READY" || eventType == "RESUMED")
            {
                LogStore.Add("QQBot", 1, "QQBot | " + displayName + " | " + eventType + " 会话就绪", "QQBOT");
                return;
            }
            if (eventType != "C2C_MESSAGE_CREATE" && eventType != "GROUP_AT_MESSAGE_CREATE" && eventType != "GROUP_MESSAGE_CREATE")
            {
                return;
            }
            QqMessageInfo m;
            try
            {
                m = ParseMessage(eventType, raw);
            }
            catch (Exception ex)
            {
                LogStore.Add("QQBot", 2, "QQBot | " + displayName + " | 事件解析失败（跳过）: " + ex.Message, "QQBOT");
                return;
            }
            QqBotConnection conn = ResolveConnection(botKey);
            string appId = conn == null ? "" : conn.AppId;
            bool isGroup = m.GroupOpenId.Length > 0;
            bool isAt = isGroup ? (m.EventType == "GROUP_AT_MESSAGE_CREATE" || m.MentionedMe) : true;
            string clean = CleanContent(m.Content);

            // [段1] 群全量留档——群内每一条消息都留（含不 @ 的），供后续检索与 per-Cat 授权审计
            if (isGroup && _store != null)
            {
                string werr = _store.AppendGroupMessage(m.GroupOpenId, DayOf(m.Timestamp), BuildRecordLine(m, botKey, appId, isAt));
                if (werr.Length > 0)
                {
                    LogStore.Add("QQBot", 2, "QQBot | 群留档失败: " + werr, "QQBOT");
                }
                // A380 群目录登记——别名 / 群名 / 权限的主存储（groups.json；自动发现 = 收到过消息）
                if (_directory != null)
                {
                    string derr = _directory.UpsertSeen(appId, m.GroupOpenId, "", DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss"));
                    if (derr.Length > 0)
                    {
                        LogStore.Add("QQBot", 2, "QQBot | 群目录登记失败: " + derr, "QQBOT");
                    }
                }
            }

            if (!isAt)
            {
                // [段2] 群内非 @ 消息——按权限只留档，不回复、不推送
                LogStore.Add("QQBot", 1, "QQBot | " + displayName + " | 群 " + Truncate(m.GroupOpenId, 12) + " ← " + Truncate(m.SenderName, 16) + "（非 @，仅留档）: " + Truncate(OneLine(clean), 40), "QQBOT");
                return;
            }

            // [段3] @ 通道——收件留档
            if (_store != null)
            {
                string merr = _store.AppendMentionIn(appId, BuildRecordLine(m, botKey, appId, true));
                if (merr.Length > 0)
                {
                    LogStore.Add("QQBot", 2, "QQBot | @ 收件留档失败: " + merr, "QQBOT");
                }
            }

            if (conn == null)
            {
                LogStore.Add("QQBot", 2, "QQBot | " + displayName + " | @ 消息无连接可回复（连接已停止）", "QQBOT");
                return;
            }

            long elapsedMs = (Stopwatch.GetTimestamp() - beginTick) * 1000 / Stopwatch.Frequency;

            // [段4] 命令直执（/ 前缀）——不经 LLM、不走 OA
            if (QqBotCommands.IsCommand(clean))
            {
                QqBotCommandContext ctx = BuildContext(conn, elapsedMs);
                string reply = QqBotCommands.Handle(clean, ctx);
                ReplyPassive(conn, isGroup, isGroup ? m.GroupOpenId : m.SenderOpenId, m.MsgId, reply);
                return;
            }

            // [段5] @ 推送订阅猫（A381）——走 user 入口：对目标猫而言这就是「有人在跟我说话」
            bool handled = false;
            if (_catPush == null)
            {
                LogStore.Add("QQBot", 2, "QQBot | @ 推送未接线（宿主未注入推送委托）: " + Truncate(m.GroupOpenId, 12), "QQBOT");
            }
            else
            {
                handled = PushMentionToCats(botKey, conn, m.GroupOpenId, m.SenderName, clean);
            }
            // [段6] 回调路径收尾（A381）——两条路径互不掺杂：
            //   ① `/` 开头 = 指令路径（段4 已直执并 return，永不进这里）
            //   ② 非 `/` 开头 = 回调路径：推送成功即静默（由猫用 qqbot-send 回答），推送失败必须发声——
            //      「没人接」这件事对群里的人是信息，不能藏（失败必须可见）
            if (handled)
            {
                return;
            }
            if (isGroup)
            {
                ReplyPassive(conn, true, m.GroupOpenId, m.MsgId, "**该推送无 Cat 接收**，请检查 CatHome");
            }
            else
            {
                LogStore.Add("QQBot", 1, "QQBot | " + displayName + " | 私聊非命令（静默）: " + Truncate(OneLine(clean), 40), "QQBOT");
            }
        }

        /// <summary>
        /// @ 消息推送（A381）——服务侧只把「收到了一条群内 @」交出去；谁该收、投递到哪个入口归宿主。
        /// </summary>
        /// <param name="botKey">配置池机器人身份</param>
        /// <param name="conn">连接（取 AppId 查群标签）</param>
        /// <param name="groupOpenId">群 OpenID</param>
        /// <param name="senderName">发送人显示名</param>
        /// <param name="text">正文（已清洗）</param>
        /// <returns>true=有猫受理（机制静默）；false=无人订阅或投递失败（由调用方回提示）</returns>
        private bool PushMentionToCats(string botKey, QqBotConnection conn, string groupOpenId, string senderName, string text)
        {
            string appId = conn == null ? "" : conn.AppId;
            string alias;
            string title;
            ResolveGroupInfo(appId, groupOpenId, out alias, out title);
            string err = _catPush(botKey, groupOpenId, alias, title, senderName, text);
            if (err.Length == 0)
            {
                return true;
            }
            if (err == "NO_TARGET")
            {
                LogStore.Add("QQBot", 1, "QQBot | @ 无人订阅（无猫绑定该机器人或群不在其白名单）: " + Truncate(groupOpenId, 12) + "——仅留档", "QQBOT");
                return false;
            }
            LogStore.Add("QQBot", 2, "QQBot | @ 推送失败（" + Truncate(groupOpenId, 12) + "）: " + err, "QQBOT");
            return false;
        }

        /// <summary>
        /// 群别名与群名（A381）——目录命中即取，未命中两值皆空（推送侧按 OpenID 仍可命中白名单）。
        /// </summary>
        /// <param name="appId">AppID</param>
        /// <param name="groupOpenId">群 OpenID</param>
        /// <param name="alias">输出：群别名（未知空串）</param>
        /// <param name="title">输出：群名称（未知空串）</param>
        private void ResolveGroupInfo(string appId, string groupOpenId, out string alias, out string title)
        {
            alias = "";
            title = "";
            if (_directory == null || appId.Length == 0)
            {
                return;
            }
            QqBotGroupDocument doc = _directory.Load(appId);
            for (int i = 0; i < doc.Groups.Count; i = i + 1)
            {
                QqBotGroupEntry g = doc.Groups[i];
                if (g != null && string.Equals(g.OpenId, groupOpenId, StringComparison.OrdinalIgnoreCase))
                {
                    alias = g.Alias == null ? "" : g.Alias;
                    title = g.Title == null ? "" : g.Title;
                    return;
                }
            }
        }

        /// <summary>
        /// 被动回复——带 msg_id + 递增 msg_seq（同一消息多次回复须不同序号，否则平台拒收）。
        /// </summary>
        /// <param name="conn">连接</param>
        /// <param name="isGroup">群/私聊</param>
        /// <param name="targetOpenId">group_openid 或 user_openid</param>
        /// <param name="msgId">被动锚</param>
        /// <param name="text">回复正文</param>
        private void ReplyPassive(QqBotConnection conn, bool isGroup, string targetOpenId, string msgId, string text)
        {
            int seq = NextReplySeq(msgId);
            string err = conn.SendMessage(isGroup ? "group" : "user", targetOpenId, text, msgId, seq);
            if (err.Length > 0)
            {
                LogStore.Add("QQBot", 3, "QQBot | " + conn.DisplayName + " | 被动回复失败 seq=" + seq.ToString() + " | " + err, "QQBOT");
                return;
            }
            if (_store != null)
            {
                string kind = isGroup ? "passive-group" : "passive-user";
                string line = "{\"at\":" + JsonUtil.Str(DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss.fff"))
                    + ",\"botKey\":" + JsonUtil.Str(conn.BotKey)
                    + ",\"appId\":" + JsonUtil.Str(conn.AppId)
                    + ",\"kind\":" + JsonUtil.Str(kind)
                    + ",\"targetOpenId\":" + JsonUtil.Str(targetOpenId)
                    + ",\"msgId\":" + JsonUtil.Str(msgId)
                    + ",\"msgSeq\":" + seq.ToString()
                    + ",\"content\":" + JsonUtil.Str(text ?? "") + "}";
                _store.AppendMentionOut(conn.AppId, line);
            }
        }

        /// <summary>
        /// 追加一条 @ 发出记录（主动发言走这条）。
        /// </summary>
        /// <param name="appId">AppID</param>
        /// <param name="botKey">配置池身份</param>
        /// <param name="displayName">显示名</param>
        /// <param name="kind">类型标记（proactive）</param>
        /// <param name="targetOpenId">目标群</param>
        /// <param name="summary">摘要标记</param>
        /// <param name="text">正文</param>
        private void AppendMentionOut(string appId, string botKey, string displayName, string kind, string targetOpenId, string summary, string text)
        {
            if (_store == null)
            {
                return;
            }
            string line = "{\"at\":" + JsonUtil.Str(DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss.fff"))
                + ",\"botKey\":" + JsonUtil.Str(botKey)
                + ",\"bot\":" + JsonUtil.Str(displayName ?? "")
                + ",\"appId\":" + JsonUtil.Str(appId)
                + ",\"kind\":" + JsonUtil.Str(kind)
                + ",\"targetOpenId\":" + JsonUtil.Str(targetOpenId)
                + ",\"summary\":" + JsonUtil.Str(summary ?? "")
                + ",\"content\":" + JsonUtil.Str(text ?? "") + "}";
            string err = _store.AppendMentionOut(appId, line);
            if (err.Length > 0)
            {
                LogStore.Add("QQBot", 2, "QQBot | @ 发出留档失败: " + err, "QQBOT");
            }
        }

        /// <summary>
        /// 取本条消息的下一个回复序号——同一 msg_id 递增（进程内计数，重启回到 1）。
        /// </summary>
        /// <param name="msgId">消息 ID</param>
        /// <returns>msg_seq（≥1）</returns>
        private int NextReplySeq(string msgId)
        {
            if (string.IsNullOrEmpty(msgId))
            {
                return 1;
            }
            lock (_sync)
            {
                int cur;
                if (_replySeq.TryGetValue(msgId, out cur))
                {
                    cur = cur + 1;
                }
                else
                {
                    cur = 1;
                }
                _replySeq[msgId] = cur;
                if (_replySeq.Count > 2000)
                {
                    _replySeq.Clear();
                    _replySeq[msgId] = cur;
                }
                return cur;
            }
        }

        /// <summary>
        /// 解析消息事件体——一次解析供全链路消费。
        /// </summary>
        /// <param name="eventType">事件名</param>
        /// <param name="raw">帧原文</param>
        /// <returns>消息投影</returns>
        private static QqMessageInfo ParseMessage(string eventType, string raw)
        {
            QqMessageInfo m = new QqMessageInfo();
            m.EventType = eventType;
            using (JsonDocument d = JsonUtil.ParseStrict(raw))
            {
                JsonElement root = d.RootElement;
                JsonElement se;
                if (root.TryGetProperty("s", out se) && se.ValueKind == JsonValueKind.Number)
                {
                    m.Seq = se.GetInt64();
                }
                JsonElement dd;
                if (!root.TryGetProperty("d", out dd))
                {
                    return m;
                }
                JsonElement e;
                if (dd.TryGetProperty("id", out e))
                {
                    m.MsgId = SafeText(e);
                }
                if (dd.TryGetProperty("content", out e))
                {
                    m.Content = SafeText(e);
                }
                if (dd.TryGetProperty("timestamp", out e))
                {
                    m.Timestamp = SafeText(e);
                }
                if (dd.TryGetProperty("group_openid", out e))
                {
                    m.GroupOpenId = SafeText(e);
                }
                JsonElement author;
                if (dd.TryGetProperty("author", out author) && author.ValueKind == JsonValueKind.Object)
                {
                    JsonElement ae;
                    if (author.TryGetProperty("member_openid", out ae))
                    {
                        m.SenderOpenId = SafeText(ae);
                    }
                    if (m.SenderOpenId.Length == 0 && author.TryGetProperty("user_openid", out ae))
                    {
                        m.SenderOpenId = SafeText(ae);
                    }
                    if (m.SenderOpenId.Length == 0 && author.TryGetProperty("id", out ae))
                    {
                        m.SenderOpenId = SafeText(ae);
                    }
                    if (author.TryGetProperty("member_nickname", out ae))
                    {
                        m.SenderName = SafeText(ae);
                    }
                    if (m.SenderName.Length == 0 && author.TryGetProperty("username", out ae))
                    {
                        m.SenderName = SafeText(ae);
                    }
                    if (author.TryGetProperty("member_role", out ae))
                    {
                        m.MemberRole = SafeText(ae);
                    }
                }
                JsonElement mentions;
                if (dd.TryGetProperty("mentions", out mentions) && mentions.ValueKind == JsonValueKind.Array)
                {
                    for (int i = 0; i < mentions.GetArrayLength(); i = i + 1)
                    {
                        JsonElement one = mentions[i];
                        if (one.ValueKind != JsonValueKind.Object)
                        {
                            continue;
                        }
                        JsonElement flag;
                        if (one.TryGetProperty("is_you", out flag) && flag.ValueKind == JsonValueKind.True)
                        {
                            m.MentionedMe = true;
                            break;
                        }
                    }
                }
            }
            return m;
        }

        /// <summary>
        /// 正文清洗——剥离 &lt;@openid&gt; / &lt;@all&gt; 占位符并 Trim（开全量后 @ 前缀内嵌在正文里）。
        /// </summary>
        /// <param name="content">原始正文</param>
        /// <returns>清洗后正文</returns>
        private static string CleanContent(string content)
        {
            if (string.IsNullOrEmpty(content))
            {
                return "";
            }
            StringBuilder sb = new StringBuilder(content.Length);
            int i = 0;
            while (i < content.Length)
            {
                char c = content[i];
                bool isMention = c == '<' && i + 1 < content.Length && content[i + 1] == '@';
                if (isMention)
                {
                    int end = content.IndexOf('>', i + 2);
                    if (end > 0)
                    {
                        i = end + 1;
                        continue;
                    }
                }
                sb.Append(c);
                i = i + 1;
            }
            return sb.ToString().Trim();
        }

        /// <summary>
        /// 归日戳——事件时间戳（RFC3339）转本机日期 yyyy-MM-dd；解析失败回落当天。
        /// </summary>
        /// <param name="timestamp">事件时间戳</param>
        /// <returns>yyyy-MM-dd</returns>
        private static string DayOf(string timestamp)
        {
            if (!string.IsNullOrEmpty(timestamp))
            {
                DateTimeOffset dto;
                if (DateTimeOffset.TryParse(timestamp, out dto))
                {
                    return dto.ToLocalTime().ToString("yyyy-MM-dd");
                }
            }
            return DateTime.Now.ToString("yyyy-MM-dd");
        }

        /// <summary>
        /// 构造一条留档行——字段面固定，缺字段按空串写入（不做字段裁剪，便于后续回溯）。
        /// </summary>
        /// <param name="m">消息投影</param>
        /// <param name="botKey">配置池身份</param>
        /// <param name="appId">AppID</param>
        /// <param name="atMe">是否 @ 了我</param>
        /// <returns>一行 JSON</returns>
        private static string BuildRecordLine(QqMessageInfo m, string botKey, string appId, bool atMe)
        {
            StringBuilder sb = new StringBuilder();
            sb.Append("{\"at\":").Append(JsonUtil.Str(DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss.fff")));
            sb.Append(",\"botKey\":").Append(JsonUtil.Str(botKey ?? ""));
            sb.Append(",\"appId\":").Append(JsonUtil.Str(appId ?? ""));
            sb.Append(",\"event\":").Append(JsonUtil.Str(m.EventType));
            sb.Append(",\"seq\":").Append(m.Seq.ToString());
            sb.Append(",\"groupOpenId\":").Append(JsonUtil.Str(m.GroupOpenId));
            sb.Append(",\"senderOpenId\":").Append(JsonUtil.Str(m.SenderOpenId));
            sb.Append(",\"senderName\":").Append(JsonUtil.Str(m.SenderName));
            sb.Append(",\"memberRole\":").Append(JsonUtil.Str(m.MemberRole));
            sb.Append(",\"atMe\":").Append(atMe ? "true" : "false");
            sb.Append(",\"msgId\":").Append(JsonUtil.Str(m.MsgId));
            sb.Append(",\"content\":").Append(JsonUtil.Str(m.Content));
            sb.Append(",\"cleanContent\":").Append(JsonUtil.Str(CleanContent(m.Content)));
            sb.Append(",\"timestamp\":").Append(JsonUtil.Str(m.Timestamp));
            sb.Append("}");
            return sb.ToString();
        }

        /// <summary>
        /// 取 JSON 元素字符串值——数字/字符串两态统一转文本（其他类型返回空串）。
        /// </summary>
        /// <param name="e">JSON 元素</param>
        /// <returns>字符串值</returns>
        private static string SafeText(JsonElement e)
        {
            if (e.ValueKind == JsonValueKind.String)
            {
                return e.GetString() ?? "";
            }
            if (e.ValueKind == JsonValueKind.Number)
            {
                return e.GetRawText();
            }
            return "";
        }
    }
}
