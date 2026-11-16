// NRT 抑制（CSharp exp §1.16）——服务壳面：连接表与委托在分支内判空；
// 逐处加 ? 会把启动/停止链淹掉，语义判空在消费点（未注入 = 该面不可用，出声）。
#nullable disable warnings

using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using Mau.Contracts;
using Mau.Runtime;

namespace Mau.Providers
{
    /// <summary>
    /// QQ 机器人渠道服务（A379）——按配置池条目管理多条常驻 WS 连接，承担「2 入 1 出」的通道骨架：
    /// ① 入 · @ 通道：群内 @ 我的消息 → 留档 + 命令直执（A381 起再推送给订阅的猫）；
    /// ② 入 · 全量通道：群内全部消息 → 仅留档（不回复、不推送）；
    /// ③ 出 · 主动发言：<see cref="SendProactive"/> 是唯一外发出口（无 msg_id，受主动消息频控）；
    /// 命令回复走被动通道（带 msg_id + msg_seq），与主动出口互不混用。
    /// 事件分拣与命令路由 → 同名分部文件 <c>QqBotService.Events.cs</c>。
    /// </summary>
    public partial class QqBotService : IQQBotService
    {
        /// <summary>连接表——配置池身份 → 连接（lock 保护，WS 线程只读自身状态）</summary>
        private readonly Dictionary<string, QqBotConnection> _connections = new Dictionary<string, QqBotConnection>();

        /// <summary>连接表访问锁</summary>
        private readonly object _sync = new object();

        /// <summary>回复序号表——msg_id → 已用 msg_seq（被动回复多次须递增，进程内有效）</summary>
        private readonly Dictionary<string, int> _replySeq = new Dictionary<string, int>();

        /// <summary>消息留档面（群全量 / @ 收件 / @ 发出）</summary>
        private QqBotStore _store = null;

        /// <summary>全局状态取数委托——宿主注入（/homeinfo 与工具面共用）</summary>
        private Func<string> _hostInfoProvider = null;

        /// <summary>猫级绑定解析委托——宿主注入（A381；未注入 = 猫级过滤不生效，按池全量处理并出声）</summary>
        private QqBotBindingResolver _catBindingResolver = null;

        /// <summary>@ 推送委托——宿主注入（A381；未注入 = @ 消息只留档不推送，日志出声）</summary>
        private QqBotCatPushHandler _catPush = null;

        /// <summary>留档根路径（空串=未注入，落档停用）</summary>
        public string InboxPath
        {
            get
            {
                if (_store == null)
                {
                    return "";
                }
                return _store.RootPath;
            }
        }

        /// <summary>
        /// 配置落点——按数据根拼 Data/qq（宿主启动链调用一次）。
        /// </summary>
        /// <param name="dataRoot">数据根绝对路径</param>
        public void Configure(string dataRoot)
        {
            if (dataRoot == null || dataRoot.Length == 0)
            {
                _store = null;
                _directory = null;
                return;
            }
            _store = new QqBotStore(Path.Combine(dataRoot, "Data", "qq"));
            // A380 群目录——Data/qq/bots/<appId>/groups.json（别名 / 群名 / 群内权限）
            _directory = new QqBotGroupDirectory(Path.Combine(dataRoot, "Data", "qq", "bots"));
        }

        /// <summary>
        /// 注入全局状态取数委托——宿主在启动链调用；未注入时 /homeinfo 明示不可用。
        /// </summary>
        /// <param name="provider">返回多行状态文本的委托</param>
        public void SetHostInfoProvider(Func<string> provider)
        {
            _hostInfoProvider = provider;
        }

        /// <summary>
        /// 注入猫级绑定解析委托（A381）——宿主启动链调用；未注入时工具面按池全量处理并出声。
        /// </summary>
        /// <param name="resolver">猫 key → 绑定（未绑定返回 null）</param>
        public void SetCatBindingResolver(QqBotBindingResolver resolver)
        {
            _catBindingResolver = resolver;
        }

        /// <summary>
        /// 注入 @ 推送委托（A381）——宿主启动链调用；未注入时 @ 消息只留档不推送。
        /// </summary>
        /// <param name="handler">推送处理（服务侧只负责「收到了一条 @」）</param>
        public void SetCatPushHandler(QqBotCatPushHandler handler)
        {
            _catPush = handler;
        }

        /// <summary>
        /// 启动一条机器人连接——参数校验 + 幂等替换 + 拉起 WS 线程。
        /// </summary>
        /// <param name="botKey">配置池身份</param>
        /// <param name="displayName">显示名</param>
        /// <param name="appId">AppID</param>
        /// <param name="appSecret">AppSecret</param>
        /// <param name="sandbox">沙箱标志</param>
        /// <returns>空串成功；"ERR|CODE|说明"失败</returns>
        public string Start(string botKey, string displayName, string appId, string appSecret, bool sandbox)
        {
            if (botKey == null || botKey.Length == 0)
            {
                return "ERR|BAD_ARGS|缺少参数 botKey";
            }
            if (appId == null || appId.Length == 0)
            {
                return "ERR|BAD_ARGS|缺少参数 appId";
            }
            if (appSecret == null || appSecret.Length == 0)
            {
                return "ERR|BAD_SECRET|AppSecret 未配置——请在该机器人的配置池条目里填写";
            }
            string capturedKey = botKey;
            string capturedName = displayName ?? "";
            Action<string, string> callback = delegate (string eventType, string raw)
            {
                OnEvent(capturedKey, capturedName, eventType, raw);
            };
            lock (_sync)
            {
                QqBotConnection previous;
                if (_connections.TryGetValue(botKey, out previous) && previous != null)
                {
                    previous.Stop();
                    _connections.Remove(botKey);
                }
                QqBotConnection conn = new QqBotConnection(botKey, capturedName, appId, appSecret, sandbox, callback);
                _connections[botKey] = conn;
                conn.Start();
            }
            LogStore.Add("QQBot", 1, "QQBot | " + capturedName + " | 收信服务已启动 " + (sandbox ? "[沙箱]" : "[正式]") + " 留档根=" + InboxPath, "QQBOT");
            return "";
        }

        /// <summary>
        /// 停止指定连接——未运行视为已停止（不报错）。
        /// </summary>
        /// <param name="botKey">配置池身份</param>
        /// <returns>空串成功；"ERR|BAD_ARGS|说明"参数缺失</returns>
        public string Stop(string botKey)
        {
            if (botKey == null || botKey.Length == 0)
            {
                return "ERR|BAD_ARGS|缺少参数 botKey";
            }
            QqBotConnection conn = null;
            lock (_sync)
            {
                if (_connections.TryGetValue(botKey, out conn))
                {
                    _connections.Remove(botKey);
                }
            }
            if (conn != null)
            {
                conn.Stop();
            }
            return "";
        }

        /// <summary>
        /// 运行态摘要——一行文本（每条连接：显示名 / 环境 / 重连计数 / 最近事件）；空表返回空串。
        /// </summary>
        /// <returns>运行态摘要</returns>
        public string Status()
        {
            List<QqBotConnection> snapshot = new List<QqBotConnection>();
            lock (_sync)
            {
                foreach (KeyValuePair<string, QqBotConnection> pair in _connections)
                {
                    snapshot.Add(pair.Value);
                }
            }
            if (snapshot.Count == 0)
            {
                return "";
            }
            StringBuilder sb = new StringBuilder();
            for (int i = 0; i < snapshot.Count; i = i + 1)
            {
                QqBotConnection c = snapshot[i];
                if (i > 0)
                {
                    sb.Append("；");
                }
                sb.Append(c.DisplayName);
                sb.Append(c.Sandbox ? "[沙箱]" : "[正式]");
                sb.Append(" 重连#").Append(c.ReconnectCount.ToString());
                sb.Append(" 最近=").Append(c.LastEventSummary);
            }
            return sb.ToString();
        }

        /// <summary>
        /// 主动发言——向指定群发一条主动消息（QQBot 唯一外发出口；命令回复不走这里）。
        /// </summary>
        /// <param name="botKey">配置池身份（空=取唯一已连接机器人）</param>
        /// <param name="groupOpenId">群 OpenID</param>
        /// <param name="text">正文</param>
        /// <returns>空串成功；"ERR|CODE|说明"失败</returns>
        public string SendProactive(string botKey, string groupOpenId, string text)
        {
            QqBotConnection conn = ResolveConnection(botKey);
            if (conn == null)
            {
                return "ERR|BOT_NOT_CONNECTED|没有可用的已连接机器人";
            }
            if (string.IsNullOrEmpty(groupOpenId))
            {
                return "ERR|BAD_ARGS|群 OpenID 为空";
            }
            string err = conn.SendMessage("group", groupOpenId, text, "", 0);
            string appId = conn.AppId;
            if (err.Length == 0)
            {
                AppendMentionOut(appId, conn.BotKey, conn.DisplayName, "proactive", groupOpenId, "主动发言", text);
            }
            else
            {
                LogStore.Add("QQBot", 3, "QQBot | " + conn.DisplayName + " | 主动发言失败 → " + Truncate(groupOpenId, 12) + " | " + err, "QQBOT");
            }
            return err;
        }

        /// <summary>
        /// 本机器人信息——所在群与每群权限（官方 bot_state 优先，不可用时降级本地观测并明示）。
        /// 硬编码 /botinfo 与工具面 qqbot-botinfo 共用同一实现。
        /// </summary>
        /// <param name="botKey">配置池身份（空=全部已连接机器人的合并视图）</param>
        /// <returns>多行文本</returns>
        public string BotInfoText(string botKey)
        {
            List<QqBotConnection> targets = new List<QqBotConnection>();
            lock (_sync)
            {
                foreach (KeyValuePair<string, QqBotConnection> pair in _connections)
                {
                    if (botKey == null || botKey.Length == 0 || pair.Key == botKey)
                    {
                        targets.Add(pair.Value);
                    }
                }
            }
            if (targets.Count == 0)
            {
                if (_store == null)
                {
                    return "没有已连接的机器人（留档面亦未注入）";
                }
                return "没有已连接的机器人";
            }
            StringBuilder sb = new StringBuilder();
            for (int i = 0; i < targets.Count; i = i + 1)
            {
                QqBotConnection c = targets[i];
                if (i > 0)
                {
                    sb.Append("\n\n");
                }
                QqBotCommandContext ctx = BuildContext(c, 0);
                sb.Append(QqBotCommands.Handle("/botinfo", ctx));
            }
            return sb.ToString();
        }

        /// <summary>
        /// 宿主收尾——停止全部连接（不抛异常；Main finally 调用一次）。
        /// </summary>
        public void Shutdown()
        {
            List<QqBotConnection> snapshot = new List<QqBotConnection>();
            lock (_sync)
            {
                foreach (KeyValuePair<string, QqBotConnection> pair in _connections)
                {
                    snapshot.Add(pair.Value);
                }
                _connections.Clear();
            }
            for (int i = 0; i < snapshot.Count; i = i + 1)
            {
                try
                {
                    snapshot[i].Stop();
                }
                catch (Exception ex)
                {
                    LogStore.Add("QQBot", 2, "QQBot | 收尾停止连接异常: " + ex.Message, "QQBOT");
                }
            }
        }

        /// <summary>
        /// 解析目标连接——指定 botKey 取该条；空 botKey 时若仅有一条连接则取它。
        /// </summary>
        /// <param name="botKey">配置池身份（可空）</param>
        /// <returns>连接或 null</returns>
        private QqBotConnection ResolveConnection(string botKey)
        {
            lock (_sync)
            {
                if (botKey != null && botKey.Length > 0)
                {
                    QqBotConnection one;
                    if (_connections.TryGetValue(botKey, out one))
                    {
                        return one;
                    }
                    return null;
                }
                if (_connections.Count == 1)
                {
                    foreach (KeyValuePair<string, QqBotConnection> pair in _connections)
                    {
                        return pair.Value;
                    }
                }
                return null;
            }
        }

        /// <summary>
        /// 组装命令执行上下文——已知群清单取自留档（bot 目录 groups.jsonl 去重）。
        /// </summary>
        /// <param name="conn">连接</param>
        /// <param name="elapsedMs">本机处理耗时</param>
        /// <returns>命令上下文</returns>
        private QqBotCommandContext BuildContext(QqBotConnection conn, long elapsedMs)
        {
            QqBotCommandContext ctx = new QqBotCommandContext();
            ctx.BotKey = conn.BotKey;
            ctx.AppId = conn.AppId;
            ctx.DisplayName = conn.DisplayName;
            ctx.Sandbox = conn.Sandbox;
            ctx.ElapsedMs = elapsedMs;
            // A380 已知群清单取自群目录（groups.json）——旧的按行去重清单退为兼容兜底
            if (_directory != null)
            {
                QqBotGroupDocument doc = _directory.Load(conn.AppId);
                System.Collections.Generic.List<string> openIds = new System.Collections.Generic.List<string>();
                for (int i = 0; i < doc.Groups.Count; i = i + 1)
                {
                    openIds.Add(doc.Groups[i].OpenId);
                }
                ctx.KnownGroups = openIds;
            }
            else if (_store != null)
            {
                ctx.KnownGroups = _store.ReadKnownGroups(conn.AppId);
            }
            ctx.BotStateFetcher = conn.GetBotStateRaw;
            ctx.HostInfoProvider = _hostInfoProvider != null ? _hostInfoProvider : BuildDefaultHomeInfo;
            return ctx;
        }

        /// <summary>
        /// 内置全局状态文本——/homeinfo 的默认实现（宿主未注入 provider 时使用）：
        /// 版本 / 构建时刻 / 运行时长 / 平台（OS + 架构 + .NET 运行时）。
        /// </summary>
        /// <returns>多行文本</returns>
        private static string BuildDefaultHomeInfo()
        {
            StringBuilder sb = new StringBuilder();
            string version = global::Mau.Runtime.VersionInfo.GetEntryVersion();
            sb.Append("# CH4 宿主 v").Append(version.Length > 0 ? version : "?");
            string buildAt = global::Mau.Runtime.VersionInfo.GetEntryBuildTime();
            if (buildAt.Length > 0)
            {
                sb.Append("\n- **构建**：").Append(buildAt);
            }
            try
            {
                using (System.Diagnostics.Process p = System.Diagnostics.Process.GetCurrentProcess())
                {
                    TimeSpan up = DateTime.Now - p.StartTime;
                    sb.Append("\n- **运行时长**：").Append(BuildUptime(up));
                }
            }
            catch (Exception ex)
            {
                sb.Append("\n- **运行时长**：不可得（").Append(ex.Message).Append("）");
            }
            sb.Append("\n- **平台**：").Append(System.Runtime.InteropServices.RuntimeInformation.OSDescription);
            sb.Append(" · ").Append(System.Runtime.InteropServices.RuntimeInformation.OSArchitecture.ToString());
            sb.Append(" · ").Append(System.Runtime.InteropServices.RuntimeInformation.FrameworkDescription);
            return sb.ToString();
        }

        /// <summary>
        /// 运行时长格式化——天/时/分/秒，取非零最大两级。
        /// </summary>
        /// <param name="up">时长</param>
        /// <returns>可读文本</returns>
        private static string BuildUptime(TimeSpan up)
        {
            if (up.TotalDays >= 1)
            {
                return up.Days.ToString() + " 天 " + up.Hours.ToString() + " 小时";
            }
            if (up.TotalHours >= 1)
            {
                return up.Hours.ToString() + " 小时 " + up.Minutes.ToString() + " 分";
            }
            if (up.TotalMinutes >= 1)
            {
                return up.Minutes.ToString() + " 分 " + up.Seconds.ToString() + " 秒";
            }
            return up.Seconds.ToString() + " 秒";
        }

        /// <summary>截断文本——日志与回复展示用。</summary>
        /// <param name="s">原文</param>
        /// <param name="max">上限</param>
        /// <returns>截断结果</returns>
        private static string Truncate(string s, int max)
        {
            if (s == null)
            {
                return "";
            }
            return s.Length <= max ? s : s.Substring(0, max);
        }

        /// <summary>单行化——\r\n 替换空格（LogStore 单行存储）。</summary>
        /// <param name="s">原文</param>
        /// <returns>单行文本</returns>
        private static string OneLine(string s)
        {
            if (s == null)
            {
                return "";
            }
            return s.Replace("\r", " ").Replace("\n", " ");
        }
    }
}
