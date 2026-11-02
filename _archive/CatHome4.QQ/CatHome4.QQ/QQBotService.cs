using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using System.Text.Json;
using System.Text.RegularExpressions;
using Mau.Runtime;
using CatHome4.Contracts;

namespace CatHome4.QQ
{
    /// <summary>
    /// QQ 管理器——附属功能组件 / 全局插件（R2.3.3-R2.3.4）。
    /// 扫描 Bot 池（CH_QqBotConfigStore）→ 每个注册 Bot 建立 WS 连接（注册即建，Cat 配置不影响连接存在）。
    /// 输入路由（R2.3.4；A58 收紧为 1:1）：消息到达 → 强匹配指令直执 / 查绑定 Cat（一个 Bot 唯一对应一只猫）→ 注入该猫（[来自QQ] 前缀 + 来源入队）。
    /// 未绑定 → 返回「无猫」；绑定但未启用 → 返回「目标 Cat 未启用 qqbot 转发功能」。
    /// 注入来源队列（FIFO）——R2.3.5 输出转发消费（回复目标 = 触发来源）。
    /// 输出转发（A58——2+2 预算）：即时 ≤2 条短块 + 轮末最终回复按 MD 结构切分 ≤2 段（官方被动回复 4 次上限）；
    /// 文本段与文件发送共用轮内调用预算，超限拒绝 + L2 留痕。
    /// </summary>
    internal static class QQBotService
    {
        /// <summary>Bot 连接集合——QqBotId → 连接实例</summary>
        private static readonly Dictionary<Guid, QQBotConnection> _connections = new Dictionary<Guid, QQBotConnection>();

        /// <summary>注入来源队列——猫 Key → 待认领来源 FIFO（注入时入队；轮起点块到达即认领，轮末 roundsum 出队消费）</summary>
        private static readonly Dictionary<string, Queue<QqPendingSource>> _sourceQueues = new Dictionary<string, Queue<QqPendingSource>>();

        /// <summary>续接锚点——猫 Key → 末次处理块的内容指纹（A111；块序变更后按指纹重定位游标，不靠裸块数）</summary>
        private static readonly Dictionary<string, string> _anchors = new Dictionary<string, string>();

        /// <summary>本轮即时转发计数——猫 Key → 已即时转发数（2+2 预算：≤2 即时，其余入轮末最终回复池）</summary>
        private static readonly Dictionary<string, int> _immediateCounts = new Dictionary<string, int>();

        /// <summary>最终回复池——猫 Key → 即时通道未发出的 text 块（超长块 / 超额块；轮末按 MD 结构切分 ≤2 段发送）</summary>
        private static readonly Dictionary<string, System.Text.StringBuilder> _accumulated = new Dictionary<string, System.Text.StringBuilder>();

        /// <summary>轮内被动调用计数——猫 Key → 本轮已用被动回复次数（文本段 + 文件发送共用；官方上限 4 次）</summary>
        private static readonly Dictionary<string, int> _roundCalls = new Dictionary<string, int>();

        /// <summary>轮内图片发送计数——猫 Key → 本轮已发图片张数（A216 定死上限 3；随交互轮重置）</summary>
        private static readonly Dictionary<string, int> _imageCounts = new Dictionary<string, int>();
        /// <summary>服务事件队列——WS 线程入队 / 主线程 Tick 事件泵消费（R6-P1-01 单线程化：三字典访问面收敛主线程）</summary>
        private static readonly System.Collections.Concurrent.ConcurrentQueue<QqServiceEvent> _eventQueue = new System.Collections.Concurrent.ConcurrentQueue<QqServiceEvent>();
        /// <summary>启动标志——防重复 Start</summary>
        private static bool _started = false;

        /// <summary>启动时刻——/info 运行时长统计</summary>
        private static DateTime _startTime = DateTime.Now;

        /// <summary>绑定目标收集面——入口壳注入（S3 解耦：不直接引用 Program 静态面）</summary>
        private static IQqTargetCollector _collector;

        /// <summary>文件缓存根——入口壳注入（P9 接收落盘；空=附件功能禁用）</summary>
        private static string _fileCacheRoot = "";

        /// <summary>
        /// 注入绑定目标收集面——入口壳 Bootstrap 调用（S3：默认猫 + 多猫注册表实现；null=禁用 QQ 转发——未注入静默）。
        /// </summary>
        /// <param name="collector">目标收集实现</param>
        public static void SetCollector(IQqTargetCollector collector)
        {
            _collector = collector;
        }

        /// <summary>
        /// 注入文件缓存根——入口壳 Bootstrap 调用（P9：Data/qqbot-files；未注入=附件缓存禁用）。
        /// </summary>
        /// <param name="root">缓存根目录</param>
        public static void SetFileCacheRoot(string root)
        {
            if (root == null)
            {
                _fileCacheRoot = "";
                return;
            }
            _fileCacheRoot = root;
            // A33——转发态文件随之定位（同 Data 根：缓存根父目录 + qq-forward.json）；显式覆盖优先（SetStatePath）——
            // 本派生仅为不改动入口壳 Bootstrap 注入点。
            if (_statePath.Length == 0)
            {
                try
                {
                    string dir = Path.GetDirectoryName(root);
                    if (dir != null && dir.Length > 0)
                    {
                        _statePath = Path.Combine(dir, "qq-forward.json");
                    }
                }
                catch (Exception e)
                {
                    LogStore.Add("QQBot", 2, "转发态路径派生失败（转发态禁用）: " + e.Message, "QQBOT");
                }
            }
        }
        /// <summary>
        /// 注入转发态文件路径——入口壳 Bootstrap 调用（A33：Data/qq-forward.json；空=转发态禁用——行为退回纯内存）。
        /// </summary>
        /// <param name="path">转发态文件绝对路径</param>
        public static void SetStatePath(string path)
        {
            if (path == null)
            {
                _statePath = "";
            }
            else
            {
                _statePath = path;
            }
        }
        /// <summary>
        /// 转发态落盘——独立文件（A33：挂载功能自持，不介入核心基座与真实会话）。
        /// 载荷：每猫 {游标、续接锚点指纹、待认领来源队列（来源 + 轮起点锚 + 认领态）、轮状态（即时计数 / 最终回复池 / 轮内预算）}
        /// + 每连接 msg_seq 水位（不续号则重启后首条被官方去重拒）。
        /// 时机：变更即落盘（来源入队 / 游标推进 / 轮结束 / 残留清理）——T4 收尾不调 QQ Stop，退出前 flush 不可依赖。
        /// </summary>
        public static void SaveForwardState()
        {
            if (_statePath.Length == 0 || !_stateLoaded)
            {
                return;
            }
            try
            {
                StringBuilder sb = new StringBuilder();
                sb.Append("{\"at\":" + JsonUtil.Scalar(DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss")) + ",\"seqs\":{");
                bool first = true;
                foreach (KeyValuePair<Guid, QQBotConnection> kv in _connections)
                {
                    if (!first)
                    {
                        sb.Append(",");
                    }
                    first = false;
                    sb.Append(JsonUtil.Scalar(kv.Key.ToString()) + ":" + kv.Value.MsgSeq.ToString());
                }
                sb.Append("},\"cats\":{");
                first = true;
                foreach (KeyValuePair<string, int> kv in _cursors)
                {
                    string key = kv.Key;
                    int imm;
                    if (!_immediateCounts.TryGetValue(key, out imm))
                    {
                        imm = 0;
                    }
                    int calls;
                    if (!_roundCalls.TryGetValue(key, out calls))
                    {
                        calls = 0;
                    }
                    string anchor = "";
                    if (_anchors.TryGetValue(key, out anchor))
                    {
                        // 命中即用（缺失保持空串——续接回落位置夹取）
                    }
                    System.Text.StringBuilder acc;
                    string accText = "";
                    if (_accumulated.TryGetValue(key, out acc) && acc.Length > 0)
                    {
                        accText = acc.ToString();
                    }
                    if (!first)
                    {
                        sb.Append(",");
                    }
                    first = false;
                    sb.Append(JsonUtil.Scalar(key) + ":{\"cursor\":" + kv.Value.ToString()
                        + ",\"anchor\":" + JsonUtil.Scalar(anchor)
                        + ",\"imm\":" + imm.ToString()
                        + ",\"calls\":" + calls.ToString() + ",\"acc\":" + JsonUtil.Scalar(accText)
                        + ",\"queue\":" + BuildQueueJson(key) + "}");
                }
                sb.Append("}}");
                File.WriteAllText(_statePath, sb.ToString(), Encoding.UTF8);
            }
            catch (Exception e)
            {
                LogStore.Add("QQBot", 2, "转发态落盘失败: " + e.Message, "QQBOT");
            }
        }
        /// <summary>待认领来源队列 → JSON 数组（落盘用；逐项 origin / claimed / src）。</summary>
        /// <param name="catKey">猫标识</param>
        /// <returns>JSON 数组文本（空队列 []）</returns>
        private static string BuildQueueJson(string catKey)
        {
            Queue<QqPendingSource> q;
            StringBuilder sb = new StringBuilder();
            sb.Append("[");
            if (_sourceQueues.TryGetValue(catKey, out q))
            {
                QqPendingSource[] all = q.ToArray();
                for (int i = 0; i < all.Length; i = i + 1)
                {
                    if (i > 0)
                    {
                        sb.Append(",");
                    }
                    QqSource s = all[i].Source;
                    sb.Append("{\"origin\":" + JsonUtil.Scalar(all[i].Origin)
                        + ",\"claimed\":" + (all[i].Claimed ? "true" : "false")
                        + ",\"src\":{\"type\":" + JsonUtil.Scalar(s.Type) + ",\"targetId\":" + JsonUtil.Scalar(s.TargetId)
                        + ",\"msgId\":" + JsonUtil.Scalar(s.MsgId) + ",\"displayName\":" + JsonUtil.Scalar(s.DisplayName)
                        + ",\"role\":" + JsonUtil.Scalar(s.Role) + "}}");
                }
            }
            sb.Append("]");
            return sb.ToString();
        }
        /// <summary>
        /// 读 JSON 字符串属性——缺失/类型不符返回空串（转发态解析用）。
        /// </summary>
        /// <param name="el">JSON 对象</param>
        /// <param name="name">属性名</param>
        /// <returns>属性值或空串</returns>
        private static string ReadJsonStr(JsonElement el, string name)
        {
            JsonElement v;
            if (el.ValueKind == JsonValueKind.Object && el.TryGetProperty(name, out v) && v.ValueKind == JsonValueKind.String)
            {
                return v.GetString() ?? "";
            }
            return "";
        }
        /// <summary>
        /// 待认领来源队列恢复——新格式 queue 数组优先（含 origin / claimed）；旧快照（src / renew 单来源）回落兼容。
        /// 接力载入的来源：轮中途重启时认领态随盘恢复（回执轮 / 唤醒轮消费）；旧快照无锚 → 载入即认领（保守：窗口直接开）。
        /// </summary>
        /// <param name="catKey">猫标识</param>
        /// <param name="c">该猫的状态对象</param>
        private static void RestoreQueue(string catKey, JsonElement c)
        {
            if (c.ValueKind != JsonValueKind.Object)
            {
                return;
            }
            JsonElement queue;
            if (c.TryGetProperty("queue", out queue) && queue.ValueKind == JsonValueKind.Array)
            {
                for (int i = 0; i < queue.GetArrayLength(); i = i + 1)
                {
                    JsonElement e = queue[i];
                    JsonElement src;
                    if (e.ValueKind != JsonValueKind.Object || !e.TryGetProperty("src", out src) || src.ValueKind != JsonValueKind.Object)
                    {
                        continue;
                    }
                    QqSource s = new QqSource(ReadJsonStr(src, "type"), ReadJsonStr(src, "targetId"), ReadJsonStr(src, "msgId"), ReadJsonStr(src, "displayName"), ReadJsonStr(src, "role"));
                    if (s.Type.Length == 0 || s.TargetId.Length == 0)
                    {
                        continue;
                    }
                    EnqueueSource(catKey, s, ReadJsonStr(e, "origin"));
                    JsonElement claimed;
                    if (e.TryGetProperty("claimed", out claimed) && claimed.ValueKind == JsonValueKind.True)
                    {
                        ClaimSource(catKey);
                    }
                }
                return;
            }
            // 旧快照回落——单来源（src）+ 续约态（renew）：载入即认领（窗口直接开）
            JsonElement legacy;
            if (c.TryGetProperty("src", out legacy) && legacy.ValueKind == JsonValueKind.Object)
            {
                QqSource s = new QqSource(ReadJsonStr(legacy, "type"), ReadJsonStr(legacy, "targetId"), ReadJsonStr(legacy, "msgId"), ReadJsonStr(legacy, "displayName"), ReadJsonStr(legacy, "role"));
                if (s.Type.Length > 0 && s.TargetId.Length > 0)
                {
                    EnqueueSource(catKey, s, "");
                    ClaimSource(catKey);
                }
            }
        }
        /// <summary>
        /// 转发态加载与续接——启动时调用（此时前文/视图已恢复，避免读到半成品块数）。
        /// 续接判据（A111）：优先按锚点内容指纹定位（命中 = 精确续接——块数增长但前缀一致同样命中）；
        /// 锚点未命中 / 缺失 → 位置夹取（越界 = 旧快照 / 视图收缩 → 保守前看，不追发历史）。
        /// 残留来源恢复入队（含轮起点锚与认领态）——重启后回执轮的回复据此转发回 QQ。
        /// </summary>
        /// <param name="targets">当前绑定目标集合</param>
        internal static void LoadForwardState(List<QqTarget> targets)
        {
            if (_statePath.Length == 0 || !File.Exists(_statePath))
            {
                return;
            }
            string raw = "";
            try
            {
                raw = File.ReadAllText(_statePath, Encoding.UTF8);
            }
            catch (Exception e)
            {
                LogStore.Add("QQBot", 2, "转发态加载失败（空态启动）: " + e.Message, "QQBOT");
                return;
            }
            if (raw.Length == 0)
            {
                return;
            }
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(raw))
                {
                    JsonElement root = doc.RootElement;
                    // ① msg_seq 水位续接——同 msg_id 重复 seq 会被官方去重拒（40054005）
                    JsonElement seqs;
                    if (root.TryGetProperty("seqs", out seqs) && seqs.ValueKind == JsonValueKind.Object)
                    {
                        foreach (JsonProperty p in seqs.EnumerateObject())
                        {
                            Guid botId;
                            QQBotConnection conn;
                            if (Guid.TryParse(p.Name, out botId) && _connections.TryGetValue(botId, out conn) && p.Value.ValueKind == JsonValueKind.Number)
                            {
                                conn.MsgSeq = p.Value.GetInt64();
                            }
                        }
                    }
                    // ② 每猫——游标续接（A111 锚点判据）+ 残留来源入队 + 轮状态恢复（即时计数 / 最终回复池 / 轮内预算）
                    JsonElement cats;
                    if (!root.TryGetProperty("cats", out cats) || cats.ValueKind != JsonValueKind.Object)
                    {
                        return;
                    }
                    foreach (JsonProperty p in cats.EnumerateObject())
                    {
                        QqTarget tg = null;
                        for (int i = 0; i < targets.Count; i = i + 1)
                        {
                            if (targets[i].Key == p.Name)
                            {
                                tg = targets[i];
                                break;
                            }
                        }
                        if (tg == null)
                        {
                            continue;
                        }
                        JsonElement c = p.Value;
                        JsonElement numEl;
                        int cursor = 0;
                        if (c.ValueKind == JsonValueKind.Object && c.TryGetProperty("cursor", out numEl) && numEl.ValueKind == JsonValueKind.Number)
                        {
                            cursor = numEl.GetInt32();
                        }
                        QqViewItem[] items = tg.GetViewItems != null ? tg.GetViewItems() : new QqViewItem[0];
                        // 续接判据（A111）：锚点内容指纹命中 → 精确续接（命中位置 + 1，覆盖「块数增长但前缀一致」）；
                        // 未命中 / 无锚 → 位置夹取（越界 = 旧快照 / 视图收缩 → 保守前看，不追发历史）
                        string anchor = ReadJsonStr(c, "anchor");
                        int resolved = -1;
                        string how = "";
                        if (anchor.Length > 0 && tg.GetBlockFingerprint != null)
                        {
                            for (int k = items.Length - 1; k >= 0; k = k - 1)
                            {
                                if (tg.GetBlockFingerprint(k) == anchor)
                                {
                                    resolved = k + 1;
                                    how = "锚点续接（指纹命中）";
                                    break;
                                }
                            }
                        }
                        if (resolved < 0)
                        {
                            resolved = ClampCursor(cursor, items.Length);
                            how = (resolved == cursor) ? "位置续接" : "保守前看（越界夹取）";
                        }
                        _cursors[p.Name] = resolved;
                        // 残留来源恢复——队列（含轮起点锚 / 认领态）；旧快照回落单来源
                        _anchors[p.Name] = anchor;
                        RestoreQueue(p.Name, c);
                        int imm = 0;
                        if (c.ValueKind == JsonValueKind.Object && c.TryGetProperty("imm", out numEl) && numEl.ValueKind == JsonValueKind.Number)
                        {
                            imm = numEl.GetInt32();
                        }
                        if (imm > 0)
                        {
                            _immediateCounts[p.Name] = imm;
                        }
                        int calls = 0;
                        if (c.ValueKind == JsonValueKind.Object && c.TryGetProperty("calls", out numEl) && numEl.ValueKind == JsonValueKind.Number)
                        {
                            calls = numEl.GetInt32();
                        }
                        if (calls > 0)
                        {
                            _roundCalls[p.Name] = calls;
                        }
                        string accText = ReadJsonStr(c, "acc");
                        if (accText.Length > 0)
                        {
                            _accumulated[p.Name] = new System.Text.StringBuilder(accText);
                        }
                        LogStore.Add("QQBot", 1, "转发态续接 | " + p.Name + " 游标 " + cursor.ToString() + " → " + resolved.ToString() + "（块数 " + items.Length.ToString() + " · " + how + "）", "QQBOT");
                    }
                }
            }
            catch (Exception e)
            {
                LogStore.Add("QQBot", 2, "转发态解析失败（空态启动）: " + e.Message, "QQBOT");
            }
        }

        /// <summary>
        /// 启动管理器——扫描 Bot 池建立全部连接。
        /// </summary>
        public static void Start()
        {
            if (_started)
            {
                return;
            }
            _started = true;
            _startTime = DateTime.Now;
            try
            {
                Refresh();
                // A33——转发态续接：连接就位后加载（游标 / 残留来源 / 轮状态 / msg_seq 水位）——重启后接续转发回 QQ
                if (_collector != null)
                {
                    LoadForwardState(_collector.CollectAll());
                }
                // 装载流程结束——置「转发态就绪」（SaveForwardState 门：未就绪不落盘，防空态覆写已有快照）
                _stateLoaded = true;
            }
            catch (Exception ex)
            {
                // A260：QQ 通道是附属能力——启动失败不得阻断宿主（其余能力照常）
                LogStore.Add("QQBot", 2, "QQ 管理器启动失败（通道不可用，宿主继续运行）: " + ex.Message, "QQBOT");
                StartupDegradation.Report("QQ_START_FAILED", StartupDegradation.KindExternal, "QQ 通道",
                    StartupDegradation.StateUnavailable, "QQ 管理器启动失败：" + ex.Message,
                    "管理面板 → QQ Bot → 检查配置后重启", "S7");
            }
        }

        /// <summary>
        /// 停止管理器——停止全部连接并清空。
        /// </summary>
        public static void Stop()
        {
            _started = false;
            List<Guid> keys = new List<Guid>(_connections.Keys);
            for (int i = 0; i < keys.Count; i = i + 1)
            {
                QQBotConnection conn;
                if (_connections.TryGetValue(keys[i], out conn))
                {
                    conn.Stop();
                }
            }
            _connections.Clear();
        }

        /// <summary>
        /// 刷新连接集合——扫描 Bot 池：新增缺失连接 / 停止已删除 Bot 连接。
        /// Bot 池 CRUD 变更后调用（注册即建连接语义）。
        /// </summary>
        public static void Refresh()
        {
            CH_QqBotConfigStore store = null;
            DataBox.TryResolve<CH_QqBotConfigStore>(out store);
            if (store == null)
            {
                return;
            }
            CH_QqBotConfig[] configs = store.GetAll();
            HashSet<Guid> active = new HashSet<Guid>();
            for (int i = 0; i < configs.Length; i = i + 1)
            {
                CH_QqBotConfig c = configs[i];
                active.Add(c.QqBotId);
                QQBotConnection existing;
                if (_connections.TryGetValue(c.QqBotId, out existing))
                {
                    // 配置变更检测——sandbox/appId/displayName 任一变化 → 重建连接（edit 后立即生效；沙箱↔正式切换关键）
                    if (existing.Sandbox != c.Sandbox || existing.AppId != c.AppId || existing.DisplayName != c.DisplayName)
                    {
                        existing.Stop();
                        _connections.Remove(c.QqBotId);
                        string secret = store.GetSecret(c.QqBotId);
                        QQBotConnection conn = new QQBotConnection(
                            c.QqBotId, c.DisplayName, c.AppId, secret, c.Sandbox,
                            delegate (string raw) { OnMessage(c.QqBotId, raw); });
                        _connections[c.QqBotId] = conn;
                        conn.Start();
                        string sandboxText = "false";
                        if (c.Sandbox)
                        {
                            sandboxText = "true";
                        }
                        LogStore.Add("QQBot", 1, "配置变更重建连接 | " + c.DisplayName + "（sandbox=" + sandboxText + "）", "QQBOT");
                    }
                    continue;
                }
                string newSecret = store.GetSecret(c.QqBotId);
                QQBotConnection newConn = new QQBotConnection(
                    c.QqBotId, c.DisplayName, c.AppId, newSecret, c.Sandbox,
                    delegate (string raw) { OnMessage(c.QqBotId, raw); });
                _connections[c.QqBotId] = newConn;
                newConn.Start();
            }
            // 停止已删除的 Bot 连接
            List<Guid> toRemove = new List<Guid>();
            foreach (KeyValuePair<Guid, QQBotConnection> kv in _connections)
            {
                if (!active.Contains(kv.Key))
                {
                    toRemove.Add(kv.Key);
                }
            }
            for (int i = 0; i < toRemove.Count; i = i + 1)
            {
                QQBotConnection conn;
                if (_connections.TryGetValue(toRemove[i], out conn))
                {
                    conn.Stop();
                    _connections.Remove(toRemove[i]);
                }
            }
        }
        /// <summary>
        /// 消息到达回调——WS 层投递（raw JSON）。
        /// 路由链：解析消息 → 强匹配指令直执（不走 LLM）→ 查绑定 Cat → 广播注入所有绑定且启用的 Cat。
        /// </summary>
        /// <param name="qqBotId">Bot 配置身份</param>
        /// <param name="raw">原始消息 JSON</param>
        private static void OnMessage(Guid qqBotId, string raw)
        {
            string t = "";
            QqSource source = null;
            string text = "";
            List<QqAttachment> attachments = null;
            if (!TryParseMessage(raw, out t, out source, out text, out attachments))
            {
                return;
            }
            // P9 附件——私聊+群@都下载缓存（URL 时效短——立即下载；注入文本带缓存路径；v0.96.1 放开群聊）
            string attachText = "";
            if (attachments != null && attachments.Count > 0)
            {
                bool hasFace = _faceTagRegex.IsMatch(text);
                attachText = DownloadAttachments(qqBotId, attachments);
                if (hasFace)
                {
                    // v0.96.1 简化——faceType 原位嵌入真实本地路径，agent 心智负担更低；成功时尾部不重复
                    text = NormalizeFaceTags(text, attachments);
                    if (attachText.Length > 0 && !attachText.Contains("|超限拒绝") && !attachText.Contains("|下载失败") && !attachText.Contains("|缓存未配置"))
                    {
                        attachText = "";
                    }
                }
            }
            if (text.Length == 0 && attachText.Length == 0)
            {
                return;
            }
            // 强匹配指令——不走 LLM 路由，代码直执（R2.3.8；Q5 扩充 /new /info）
            string cmdReply = HandleSlashCommand(text, qqBotId);
            if (cmdReply != null)
            {
                // A208——指令受理统一留痕（兜底 / 状态 / 重开会话族同出口；此前全族静默）
                LogStore.Add("QQBot", 1, "指令受理 | " + ResolveCatKey(qqBotId) + " | " + text, "QQBOT");
                if (text == "/last")
                {
                    // /last 独立通道——切分 ≤4 段逐条发送（不占转发预算）
                    SendLastReply(qqBotId, source, cmdReply);
                }
                else
                {
                    SendToSource(qqBotId, source, cmdReply);
                }
                return;
            }
            // 查绑定该 Bot 的猫（A58 1:1——一个 Bot 唯一对应一只猫）
            List<QqTarget> targets = new List<QqTarget>();
            if (_collector != null)
            {
                targets = _collector.CollectByBot(qqBotId);
            }
            if (targets.Count == 0)
            {
                SendToSource(qqBotId, source, "无猫");
                return;
            }
            QqTarget tg = targets[0];
            if (!tg.Enable)
            {
                SendToSource(qqBotId, source, "目标 Cat 未启用 qqbot 转发功能");
                return;
            }
            // 注入唯一绑定猫——消息头区分渠道+来源（v0.96.1：私聊硬编码雾理莎/群@昵称+角色）
            // 🔴 三字典单线程化（R6-P1-01）——WS 线程只入队事件，EnqueueSource/ResetRound 由主线程 Tick 事件泵统一消费
            if (tg.Inject == null)
            {
                LogStore.Add("QQBot", 2, "注入面未接线——消息未受理 | " + tg.Key, "QQBOT");
                SendToSource(qqBotId, source, "（消息未受理——猫注入面未就绪）");
                return;
            }
            string payload = "[来自QQ]" + BuildHeader(source) + " " + text + attachText;
            bool accepted = tg.Inject(payload, QqInjectionOrigin);
            if (!accepted)
            {
                // 注入未受理（停机态 / 空内容）——出声 + 回执；不入队来源（无注入即无回复可路由）
                LogStore.Add("QQBot", 2, "注入未受理（停机态 / 空内容）——消息未进会话 | " + tg.Key, "QQBOT");
                SendToSource(qqBotId, source, "（消息未受理——宿主正在收尾 / 停机，请稍后重发）");
                return;
            }
            // A208——受理面正向留痕（与拒绝面 L2 对称：此前成功路径静默，无从判断消息是否进了会话）
            LogStore.Add("QQBot", 1, "注入受理 | " + tg.Key + " | " + payload.Length.ToString() + " 字符 | origin=" + QqInjectionOrigin, "QQBOT");
            _eventQueue.Enqueue(new QqServiceEvent { Kind = "source", CatKey = tg.Key, Source = source, Origin = QqInjectionOrigin });
            _eventQueue.Enqueue(new QqServiceEvent { Kind = "reset", CatKey = tg.Key });
        }
        /// <summary>
        /// 取 Bot 绑定猫标识——日志标识用（1:1 唯一绑定；收集面未就绪 / 未绑定给可读占位）。
        /// </summary>
        /// <param name="qqBotId">Bot 配置身份</param>
        /// <returns>猫标识（或占位文本）</returns>
        private static string ResolveCatKey(Guid qqBotId)
        {
            if (_collector == null)
            {
                return "（收集器未就绪）";
            }
            List<QqTarget> targets = _collector.CollectByBot(qqBotId);
            if (targets.Count == 0)
            {
                return "（无绑定猫）";
            }
            return targets[0].Key;
        }
        /// <summary>
        /// 解析消息——C2C 私聊 / GROUP_AT 群@。
        /// </summary>
        /// <param name="raw">原始消息 JSON</param>
        /// <param name="t">事件类型</param>
        /// <param name="source">来源——private:uid / group:gid:mid</param>
        /// <param name="text">消息文本（剥离 @Bot 前缀）</param>
        /// <param name="attachments">附件列表（P9——attachments 非空即含附件）；无附件 null</param>
        /// <returns>解析成功 true</returns>
        private static bool TryParseMessage(string raw, out string t, out QqSource source, out string text, out List<QqAttachment> attachments)
        {
            t = "";
            source = null;
            text = "";
            attachments = null;
            try
            {
                using (JsonDocument d = JsonUtil.ParseStrict(raw))
                {
                    if (!d.RootElement.TryGetProperty("t", out JsonElement tp))
                    {
                        return false;
                    }
                    t = tp.GetString() ?? "";
                    if (t == "C2C_MESSAGE_CREATE")
                    {
                        JsonElement dd = d.RootElement.GetProperty("d");
                        JsonElement authorE = dd.GetProperty("author");
                        string uid = authorE.GetProperty("user_openid").GetString() ?? "";
                        string authorName = "";
                        if (authorE.TryGetProperty("username", out JsonElement un))
                        {
                            authorName = un.GetString() ?? "";
                        }
                        string content = "";
                        if (dd.TryGetProperty("content", out JsonElement c))
                        {
                            content = c.GetString() ?? "";
                        }
                        string msgId = "";
                        if (dd.TryGetProperty("id", out JsonElement idE))
                        {
                            msgId = idE.GetString() ?? "";
                        }
                        attachments = ParseAttachments(dd);
                        source = new QqSource("private", uid, msgId, authorName, "");
                        text = StripAtMention(content);
                        return true;
                    }
                    if (t == "GROUP_AT_MESSAGE_CREATE")
                    {
                        JsonElement dd = d.RootElement.GetProperty("d");
                        JsonElement authorE = dd.GetProperty("author");
                        string gid = dd.GetProperty("group_openid").GetString() ?? "";
                        string mid = authorE.GetProperty("member_openid").GetString() ?? "";
                        string authorName = "";
                        if (authorE.TryGetProperty("username", out JsonElement un))
                        {
                            authorName = un.GetString() ?? "";
                        }
                        string memberRole = "";
                        if (authorE.TryGetProperty("member_role", out JsonElement mr))
                        {
                            memberRole = mr.GetString() ?? "";
                        }
                        string content = "";
                        if (dd.TryGetProperty("content", out JsonElement c))
                        {
                            content = c.GetString() ?? "";
                        }
                        string msgId = "";
                        if (dd.TryGetProperty("id", out JsonElement idE))
                        {
                            msgId = idE.GetString() ?? "";
                        }
                        attachments = ParseAttachments(dd);
                        source = new QqSource("group", gid + ":" + mid, msgId, authorName, memberRole);
                        text = StripAtMention(content);
                        return true;
                    }
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("QQBot", 2, "群消息解析失败: " + ex.Message, "QQBOT");
            }
            return false;
        }        /// <summary>附件大小硬限制——200MB（官方硬限制）</summary>
        private const long MaxAttachmentBytes = 200L * 1024 * 1024;

        /// <summary>
        /// 附件下载落盘——逐项：超限拒绝 / 下载 / 失败标记；返回注入文本段（P9 接收）。
        /// </summary>
        /// <param name="qqBotId">Bot 配置身份</param>
        /// <param name="atts">附件列表</param>
        /// <returns>注入文本段（[附件: ...] 拼接；无附件空串）</returns>
        private static string DownloadAttachments(Guid qqBotId, List<QqAttachment> atts)
        {
            QQBotConnection conn;
            if (!_connections.TryGetValue(qqBotId, out conn))
            {
                return "";
            }
            string dir = GetFileCacheDir();
            StringBuilder sb = new StringBuilder();
            for (int i = 0; i < atts.Count; i = i + 1)
            {
                QqAttachment a = atts[i];
                if (a.Size > MaxAttachmentBytes)
                {
                    sb.Append(" [附件: " + a.FileName + "|" + a.Size + "|超限拒绝]");
                    LogStore.Add("QQBot", 2, "附件超限拒绝 | " + a.FileName + " | " + a.Size, "QQBOT");
                    continue;
                }
                if (dir.Length == 0)
                {
                    sb.Append(" [附件: " + a.FileName + "|" + a.Size + "|缓存未配置]");
                    continue;
                }
                string dest = Path.Combine(dir, DateTime.Now.ToString("yyyyMMdd_HHmmss") + "_" + SanitizeFileName(a.FileName));
                if (conn.DownloadAttachment(a.Url, dest))
                {
                    a.LocalPath = dest;
                    // v0.96.1 简化——成功路径含文件名，不重复 size；faceType 原位嵌入路径时尾部 attachText 清空
                    sb.Append(" [附件: " + dest + "]");
                }
                else
                {
                    sb.Append(" [附件: " + a.FileName + "|" + a.Size + "|下载失败]");
                }
            }
            return sb.ToString();
        }
        /// <summary>
        /// 解析附件数组——attachments 非空即含附件（消息事件与文本同构）。
        /// </summary>
        /// <param name="dd">事件 d 对象</param>
        /// <returns>附件列表；无附件 null</returns>
        private static List<QqAttachment> ParseAttachments(JsonElement dd)
        {
            if (!dd.TryGetProperty("attachments", out JsonElement attE) || attE.ValueKind != JsonValueKind.Array)
            {
                return null;
            }
            List<QqAttachment> result = new List<QqAttachment>();
            for (int i = 0; i < attE.GetArrayLength(); i = i + 1)
            {
                JsonElement a = attE[i];
                QqAttachment att = new QqAttachment();
                if (a.TryGetProperty("url", out JsonElement u))
                {
                    att.Url = u.GetString() ?? "";
                }
                if (a.TryGetProperty("filename", out JsonElement f))
                {
                    att.FileName = f.GetString() ?? "";
                }
                if (a.TryGetProperty("size", out JsonElement s) && s.ValueKind == JsonValueKind.Number)
                {
                    att.Size = s.GetInt64();
                }
                if (a.TryGetProperty("content_type", out JsonElement ct))
                {
                    att.ContentType = ct.GetString() ?? "";
                }
                if (att.Url.Length > 0)
                {
                    result.Add(att);
                }
            }
            if (result.Count > 0)
            {
                return result;
            }
            return null;
        }

        /// <summary>文件名净化——去非法字符（落盘安全）</summary>
        private static string SanitizeFileName(string name)
        {
            string s = "file";
            if (name != null)
            {
                s = name;
            }
            char[] invalid = Path.GetInvalidFileNameChars();
            StringBuilder sb = new StringBuilder();
            for (int i = 0; i < s.Length; i = i + 1)
            {
                bool bad = false;
                for (int j = 0; j < invalid.Length; j = j + 1)
                {
                    if (s[i] == invalid[j])
                    {
                        bad = true;
                        break;
                    }
                }
                if (bad)
                {
                    sb.Append('_');
                }
                else
                {
                    sb.Append(s[i]);
                }
            }
            string result = sb.ToString().Trim();
            if (result.Length == 0)
            {
                return "file";
            }
            return result;
        }

        /// <summary>缓存目录——Ensure 创建（未配置返回空串）</summary>
        private static string GetFileCacheDir()
        {
            if (_fileCacheRoot.Length == 0)
            {
                return "";
            }
            try
            {
                Directory.CreateDirectory(_fileCacheRoot);
            }
            catch (Exception)
            {
                return "";
            }
            return _fileCacheRoot;
        }

        /// <summary>
        /// 剥离群@的 &lt;@!bot_openid&gt; 前缀——强匹配指令需要纯净文本。
        /// </summary>
        /// <param name="content">原始内容</param>
        /// <returns>剥离后的文本</returns>
        private static string StripAtMention(string content)
        {
            string s = "";
            if (content != null)
            {
                s = content.Trim();
            }
            if (s.StartsWith("<@"))
            {
                int end = s.IndexOf('>');
                if (end >= 0)
                {
                    s = s.Substring(end + 1).Trim();
                }
            }
            return s;
        }
        /// <summary>
        /// 归一化 QQ 富文本 faceType 标记——&lt;faceType=X,...&gt; → [本地缓存路径] 原位嵌入。
        /// 多图嵌长文本可区分图片与文字的对应关系；agent 直接可调 image-analyze（v0.96.1）。
        /// </summary>
        /// <param name="content">原始内容（已剥离 @ 前缀）</param>
        /// <param name="attachments">附件列表——按序对应 faceType 标记；降级链：本地路径→文件名→[图片]</param>
        /// <returns>归一化后的文本</returns>
        private static string NormalizeFaceTags(string content, List<QqAttachment> attachments)
        {
            // [段1] 空内容——原样回退（?? 语法糖展开）
            if (string.IsNullOrEmpty(content))
            {
                if (content == null)
                {
                    return "";
                }
                return content;
            }
            // [段2] 匹配收集——正则整体匹配（替代 MatchEvaluator 委托闭包；无匹配直接返回原文本）
            MatchCollection matches = _faceTagRegex.Matches(content);
            if (matches.Count == 0)
            {
                return content;
            }
            // [段3] 逐匹配原位替换——附件按序对应（降级链：本地路径→文件名→[图片]）
            StringBuilder sb = new StringBuilder();
            int cursor = 0;
            int idx = 0;
            for (int i = 0; i < matches.Count; i = i + 1)
            {
                Match m = matches[i];
                sb.Append(content.Substring(cursor, m.Index - cursor));
                string mark = "";
                if (attachments != null && idx < attachments.Count)
                {
                    QqAttachment a = attachments[idx];
                    if (!string.IsNullOrEmpty(a.LocalPath))
                    {
                        mark = a.LocalPath;
                    }
                    else if (!string.IsNullOrEmpty(a.FileName))
                    {
                        mark = a.FileName;
                    }
                }
                idx = idx + 1;
                if (string.IsNullOrEmpty(mark))
                {
                    sb.Append("[图片]");
                }
                else
                {
                    sb.Append("[" + mark + "]");
                }
                cursor = m.Index + m.Length;
            }
            sb.Append(content.Substring(cursor));
            return sb.ToString();
        }        /// <summary>
                 /// 构建消息头——区分渠道+来源（v0.96.1）。
                 /// 私聊：硬编码雾理莎（莎本人私聊 ID，User.md 补身份）；群@：昵称+角色（事件白拿字段）。
                 /// </summary>
                 /// <param name="source">消息来源</param>
                 /// <returns>消息头——[私聊|雾理莎] / [群@|昵称(角色)]</returns>
        private static string BuildHeader(QqSource source)
        {
            if (source.Type == "private")
            {
                return "[私聊|雾理莎]";
            }
            string name = source.DisplayName ?? "";
            string role = source.Role ?? "";
            if (name.Length == 0)
            {
                return "[群@]";
            }
            if (role.Length == 0)
            {
                return "[群@|" + name + "]";
            }
            return "[群@|" + name + "(" + RoleText(role) + ")]";
        }

        /// <summary>群角色中文映射——owner=群主 / admin=管理员 / member=成员；未知原值</summary>
        /// <param name="role">事件 member_role 原值</param>
        /// <returns>中文角色文本</returns>
        private static string RoleText(string role)
        {
            switch (role)
            {
                case "owner": return "群主";
                case "admin": return "管理员";
                case "member": return "成员";
                default: return role;
            }
        }
        /// <summary>
        /// 强匹配指令处理——/ 开头指令完全一致才命中；命中返回回复文本，未命中返回 null（走 LLM 路由）。
        /// Q5：/new 重开新会话（前端 session.new 同款）+ /info 查看状态（qqbot 管理器 + 合并猫 info）。
        /// </summary>
        /// <param name="text">消息文本</param>
        /// <param name="qqBotId">Bot 配置身份——按 bot 收集绑定猫</param>
        /// <returns>指令回复；非指令 null</returns>
        private static string HandleSlashCommand(string text, Guid qqBotId)
        {
            if (text == "/ping")
            {
                return "pong";
            }
            if (text == "/info")
            {
                return BuildInfoText(qqBotId);
            }
            if (text == "/new")
            {
                return HandleNewSession(qqBotId);
            }
            // A58——兜底拉取：重启后补感知（仅最后一条；超长由发送面切分 ≤4 段；design-qqbot-forward §五）
            if (text == "/last")
            {
                return BuildLastText(qqBotId);
            }
            if (text.StartsWith("/"))
            {
                return "未知指令: " + text + "（可用: /ping /info /new /last）";
            }
            return null;
        }
        /// <summary>构建 /last 回复（A58）——取绑定猫（1:1 唯一）视图层最后一条 text 块（正式回复；A188 起 gap_text 间隙文本不取），正文不截断。
        /// 超长由发送面（SendLastReply）按 MD 结构切分，最多 4 段独立发送（末尾段优先）；文件标记（A112）同样由发送面扫描剥离并经文件通道发送。
        /// A208——取块结果 L1 留痕（命中位置 / 字符数 / 无回复三态）；internal 供单测直调。</summary>
        /// <param name="qqBotId">Bot 配置身份——1:1 唯一绑定猫</param>
        /// <returns>回复正文（含【猫名：】前缀）</returns>
        internal static string BuildLastText(Guid qqBotId)
        {
            if (_collector == null)
            {
                LogStore.Add("QQBot", 1, "/last 取块 | （收集器未就绪）", "QQBOT");
                return "无视图数据（绑定目标收集面未注入）";
            }
            List<QqTarget> targets = _collector.CollectByBot(qqBotId);
            if (targets.Count == 0)
            {
                LogStore.Add("QQBot", 1, "/last 取块 | （无绑定猫）", "QQBOT");
                return "无猫";
            }
            QqTarget tg = targets[0];
            if (tg.GetViewItems == null)
            {
                LogStore.Add("QQBot", 1, "/last 取块 | " + tg.Key + " | 无已生成回复（视图面未接线）", "QQBOT");
                return "【" + tg.DisplayName + "】无已生成回复";
            }
            QqViewItem[] items = tg.GetViewItems();
            for (int i = items.Length - 1; i >= 0; i = i - 1)
            {
                QqViewItem it = items[i];
                if (it.RenderType == "text" && it.Content.Length > 0)
                {
                    // A208——取块结果留痕（命中位置与字符数；兜底通道取到什么可回溯）
                    LogStore.Add("QQBot", 1, "/last 取块 | " + tg.Key + " | 命中第 " + (i + 1).ToString() + " / " + items.Length.ToString() + " 块（" + it.Content.Length.ToString() + " 字符）", "QQBOT");
                    return tg.DisplayName + "：\n" + it.Content;
                }
            }
            LogStore.Add("QQBot", 1, "/last 取块 | " + tg.Key + " | 无已生成回复（共 " + items.Length.ToString() + " 块）", "QQBOT");
            return "【" + tg.DisplayName + "】无已生成回复";
        }

        /// <summary>
        /// /new 指令执行——等同前端 session.new（置位标志投递总线，主线程泵异步执行）+ 注入摘要同步返回（Q5：注入前文与工具气泡同步到 qqbot）。
        /// </summary>
        /// <param name="qqBotId">Bot 配置身份</param>
        /// <returns>回复文本</returns>
        private static string HandleNewSession(Guid qqBotId)
        {
            if (_collector == null)
            {
                return "无法执行：qqbot 收集器未就绪";
            }
            List<QqTarget> targets = _collector.CollectByBot(qqBotId);
            if (targets.Count == 0)
            {
                return "无绑定猫";
            }
            StringBuilder sb = new StringBuilder();
            // A58 1:1——该 Bot 唯一绑定猫
            QqTarget tg = targets[0];
            sb.Append("【" + tg.DisplayName + "】");
            if (tg.NewSession != null)
            {
                sb.Append("\n" + tg.NewSession());
            }
            else
            {
                sb.Append("\n不支持新会话");
            }
            return sb.ToString();
        }

        /// <summary>
        /// 构建 /info 状态文本——运行总时长 + 当前 qqbot 数 + 可用指令 + 合并绑定猫 info（Q5）。
        /// </summary>
        /// <param name="qqBotId">Bot 配置身份</param>
        /// <returns>状态文本</returns>
        private static string BuildInfoText(Guid qqBotId)
        {
            TimeSpan up = DateTime.Now - _startTime;
            string upText = "";
            if (up.TotalHours >= 1)
            {
                upText = (int)up.TotalHours + "h" + up.Minutes + "m";
            }
            else
            {
                upText = up.Minutes + "m" + up.Seconds + "s";
            }
            StringBuilder sb = new StringBuilder();
            sb.Append("【QQ 管理器状态】\n运行时长: " + upText + "\n当前 qqbot 数: " + _connections.Count + "\n可用指令: /ping /info /new /last");
            // Q5——附绑定猫 info（直接调用工具函数，不经 LLM/OA）；A31——附会话状态（兜底判据：IDLE = 安全拉取窗口）
            // A58 1:1——该 Bot 唯一绑定猫
            if (_collector != null)
            {
                List<QqTarget> targets = _collector.CollectByBot(qqBotId);
                if (targets.Count > 0)
                {
                    QqTarget tg = targets[0];
                    string state = "未知";
                    if (tg.IsIdle != null)
                    {
                        state = tg.IsIdle() ? "IDLE" : "运行中";
                    }
                    sb.Append("\n\n【猫: " + tg.DisplayName + "】\n会话状态: " + state);
                    if (tg.GetInfo != null)
                    {
                        // info 返回体 = 分类 JSON 块（LLM 可读优先）——QQ 侧经解码器投影为 Markdown（可见根表格）
                        sb.Append("\n" + QqInfoFormatter.ToMarkdown(tg.GetInfo()));
                    }
                }
            }
            return sb.ToString();
        }

        /// <summary>
        /// 发送回复到来源——通过 Bot 连接（QqSource 载荷；指令回复/错误提示走纯文本通道）。
        /// </summary>
        /// <param name="qqBotId">Bot 配置身份</param>
        /// <param name="source">来源</param>
        /// <param name="text">回复文本</param>
        private static void SendToSource(Guid qqBotId, QqSource source, string text)
        {
            QQBotConnection conn;
            if (!_connections.TryGetValue(qqBotId, out conn))
            {
                return;
            }
            // 指令回复/错误提示——MD 通道（Q4 全 MD），携带被动 msg_id；失败降级主动消息一次（A34）
            SendWithFallback(conn, source.Type, source.TargetId, text, source.MsgId, true);
        }

        /// <summary>
        /// 注入来源入队——猫 Key → 待认领来源 FIFO（R2.3.5 输出转发消费）。
        /// </summary>
        /// <param name="catKey">猫标识（majordomo / cat id）</param>
        /// <param name="source">来源</param>
        /// <param name="origin">轮起点锚（注入时给；空=接力载入无锚——载入即认领）</param>
        internal static void EnqueueSource(string catKey, QqSource source, string origin)
        {
            Queue<QqPendingSource> q;
            if (!_sourceQueues.TryGetValue(catKey, out q))
            {
                q = new Queue<QqPendingSource>();
                _sourceQueues[catKey] = q;
            }
            QqPendingSource p = new QqPendingSource();
            p.Source = source;
            p.Origin = origin == null ? "" : origin;
            p.Claimed = false;
            q.Enqueue(p);
        }
        /// <summary>
        /// 认领来源——本猫 QQ 注入的 user 块（轮起点）到达时调用：认领队列中第一个未认领项。
        /// 认领面恒为「已认领前缀之后第一个」——与注入 FIFO 序一致（同轮多消息逐个认领）。
        /// </summary>
        /// <param name="catKey">猫标识</param>
        private static void ClaimSource(string catKey)
        {
            Queue<QqPendingSource> q;
            if (!_sourceQueues.TryGetValue(catKey, out q) || q.Count == 0)
            {
                return;
            }
            QqPendingSource[] all = q.ToArray();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                if (!all[i].Claimed)
                {
                    all[i].Claimed = true;
                    return;
                }
            }
        }
        /// <summary>转发窗开启判定——队首已认领（本轮由 QQ 注入消息开启，块可转发）。</summary>
        /// <param name="catKey">猫标识</param>
        /// <returns>窗口开启 true</returns>
        private static bool WindowOpen(string catKey)
        {
            QqPendingSource head = PeekClaimedSource(catKey);
            return head != null;
        }
        /// <summary>队首已认领来源读取——不消费（整轮转发同一来源；roundsum / 隐式轮边界才出队）。</summary>
        /// <param name="catKey">猫标识</param>
        /// <returns>已认领队首来源（未开启转发窗返回 null）</returns>
        private static QqPendingSource PeekClaimedSource(string catKey)
        {
            Queue<QqPendingSource> q;
            if (_sourceQueues.TryGetValue(catKey, out q) && q.Count > 0 && q.Peek().Claimed)
            {
                return q.Peek();
            }
            return null;
        }
        /// <summary>出队一个已认领来源——轮末消费（done=stream / 隐式轮边界）。</summary>
        /// <param name="catKey">猫标识</param>
        /// <returns>消费到 true</returns>
        private static bool TryDequeueClaimed(string catKey)
        {
            Queue<QqPendingSource> q;
            if (_sourceQueues.TryGetValue(catKey, out q) && q.Count > 0 && q.Peek().Claimed)
            {
                q.Dequeue();
                return true;
            }
            return false;
        }
        /// <summary>
        /// 来源队列非空判定——异常中止轮残留清理触发条件之一。
        /// </summary>
        /// <param name="catKey">猫标识</param>
        /// <returns>队列非空 true</returns>
        private static bool HasPendingSource(string catKey)
        {
            Queue<QqPendingSource> q;
            return _sourceQueues.TryGetValue(catKey, out q) && q.Count > 0;
        }                /// <summary>重置交互轮状态——新用户消息注入时调用（即时计数归零 + 最终回复池清空 + 轮内预算归零）。</summary>
/// <param name="catKey">猫标识</param>
        private static void ResetRound(string catKey)
        {
            _immediateCounts[catKey] = 0;
            _accumulated.Remove(catKey);
            _roundCalls.Remove(catKey);
            _imageCounts.Remove(catKey);
        }
        /// <summary>
        /// 清空交互轮状态——残留来源兜底（会话 Idle 且队列残留 = 该消息未开启任何轮）：逐条出声 + 回执告知 + 丢弃 + 计数/累计清零。
        /// 静默丢弃不可接受（A207）——用户侧必须知道「这条消息没产生回复」。
        /// </summary>
        /// <param name="tg">绑定目标（回执经 QqBotId 定位连接）</param>
        private static void ClearRoundState(QqTarget tg)
        {
            Queue<QqPendingSource> q;
            if (_sourceQueues.TryGetValue(tg.Key, out q))
            {
                while (q.Count > 0)
                {
                    QqPendingSource p = q.Dequeue();
                    LogStore.Add("QQBot", 2, "残留来源清理 | " + tg.Key + "（" + (p.Claimed ? "轮已开启未收口" : "轮未开启") + "——来源丢弃） | " + p.Source.ToString(), "QQBOT");
                    SendToSource(tg.QqBotId, p.Source, "（上一条消息未产生回复——该轮未正常收尾，请重发）");
                }
            }
            ResetRound(tg.Key);
            // A33——残留清理即落盘（防重启后恢复已清理来源）
            SaveForwardState();
        }
        /// <summary>
        /// 即时转发——2+2 预算（A58）：短块（含前缀 ≤ 单段上限）且计数 &lt; 2 → 立即转发；
        /// 超长块与超额块入最终回复池（超长块必走轮末切分——即时通道的整条截断会丢内容）。
        /// 转发窗未开（前端对话 / 来源未认领）→ 不转发（被动机制——只有用户主动输入开启的轮才转发）。
        /// </summary>
        /// <param name="tg">绑定目标</param>
        /// <param name="content">text 块内容</param>
        private static void ForwardText(QqTarget tg, string content)
        {
            QqPendingSource head = PeekClaimedSource(tg.Key);
            if (head == null)
            {
                // A34——不转发但必留痕（原静默 return：重启轮/前端轮的漏转发无从诊断）
                LogStore.Add("QQBot", 1, "转发跳过（窗口未开） | " + tg.Key + " ← " + Truncate(content, 30), "QQBOT");
                return;
            }
            int n;
            if (!_immediateCounts.TryGetValue(tg.Key, out n))
            {
                n = 0;
            }
            // 前缀"猫名：\n"计入首段预算（与发送面拼前缀再切分的口径一致）
            int cost = content.Length + tg.DisplayName.Length + 2;
            if (n < MaxImmediateReplies && cost <= MaxChunkChars)
            {
                // 短块——即时转发（单段不切分；占轮内预算）
                _immediateCounts[tg.Key] = n + 1;
                SendRouted(tg, head.Source, content, 1, tg.Key);
                return;
            }
            System.Text.StringBuilder sb;
            if (!_accumulated.TryGetValue(tg.Key, out sb))
            {
                sb = new System.Text.StringBuilder();
                _accumulated[tg.Key] = sb;
            }
            if (sb.Length > 0)
            {
                sb.Append("\n\n");
            }
            sb.Append(content);
        }
        /// <summary>
        /// 轮结束——roundsum 块哨兵 / 隐式轮边界共用：最终回复池非空 → 按 MD 结构切分（最多 2 段——2+2 预算的第二个 2）逐段发送；
        /// 段数超限丢弃尾部 + L2 留痕；来源按 done 语义处置（tool=保留待下一轮 / stream=出队消费）；重置轮状态（即时计数 / 池 / 预算）。
        /// </summary>
        /// <param name="tg">绑定目标</param>
        /// <param name="done">本轮结束语义——tool=工具主动 done（来源保留）/ stream=流式自然收尾（来源出队）</param>
        private static void FinishRound(QqTarget tg, string done)
        {
            QqPendingSource head = PeekClaimedSource(tg.Key);
            if (head != null)
            {
                System.Text.StringBuilder sb;
                if (_accumulated.TryGetValue(tg.Key, out sb) && sb.Length > 0)
                {
                    // 段数上限 = 轮内被动预算（A58 甲——用尽剩余预算）：无即时转发时可发满 4 段，
                    // 即时转发占用的次数自然把最终回复收敛为 2 段（即 2+2）；绝不超过官方 4 次上限。
                    SendRouted(tg, head.Source, sb.ToString(), MaxRoundCalls, tg.Key);
                }
            }
            // 来源处置——done=tool（工具主动 done：本轮登记了等待/收尾，真正的回复在下一轮注入）→ 保留（认领态不退）；
            // done=stream（流式自然收尾 / 隐式轮边界）→ 出队消费（本轮即回复轮）
            if (done == "tool")
            {
                if (head != null)
                {
                    LogStore.Add("QQBot", 1, "来源保留（工具主动 done——等下一轮消费） | " + tg.Key, "QQBOT");
                }
            }
            else
            {
                while (TryDequeueClaimed(tg.Key))
                {
                    // 同轮多消息：已认领来源全部消费（本轮即回复轮）
                }
            }
            ResetRound(tg.Key);
            // A33——来源处置即落盘（防重启后恢复已消费来源 → 错配转发）
            SaveForwardState();
        }
        /// <summary>游标——猫 Key → 已处理消息数（只转发启用后新块）</summary>
        private static readonly Dictionary<string, int> _cursors = new Dictionary<string, int>();
        /// <summary>
        /// 事件泵——WS 线程入队事件统一在主线程消费（R6-P1-01 单线程化）。
        /// source 事件 → 注入来源入队；reset 事件 → 轮状态重置（即时计数归零 + 最终回复池清空 + 轮内预算归零）。
        /// </summary>
        private static void DrainEvents()
        {
            QqServiceEvent ev;
            bool changed = false;
            while (_eventQueue.TryDequeue(out ev))
            {
                if (ev.Kind == "source")
                {
                    EnqueueSource(ev.CatKey, ev.Source, ev.Origin);
                    changed = true;
                }
                else if (ev.Kind == "reset")
                {
                    ResetRound(ev.CatKey);
                    changed = true;
                }
            }
            // A33——来源入队 / 轮状态重置即落盘（残留来源是重启接力的关键载荷）
            if (changed)
            {
                SaveForwardState();
            }
        }
        /// <summary>
        /// 输出转发轮询——主线程每帧调用（宿主主循环接入）。
        /// 转发窗模型：本猫 QQ 注入的 user 块（Origin 非空）= 轮起点——到达即认领来源、开窗；
        /// 窗内 text 块 → 即时转发 ≤2（超长块入池）；roundsum → 轮结束哨兵（发池 + 来源按 done 处置 + 重置）；
        /// 窗未开（前端对话 / 来源未认领）→ 不转发（被动机制）。
        /// 隐式轮边界：轮起点先于上一轮收口到达（忙时插话跳过 CloseRound ⇒ 上一轮无 roundsum）→ 先按自然收尾收口。
        /// 续接：锚点指纹优先；越界夹取回写 + 出声（游标不随视图收缩校正会永久失配——A207 根因）。
        /// </summary>
        public static void Tick()
        {
            if (!_started)
            {
                return;
            }
            // 事件泵——WS 线程入队事件统一在主线程消费（R6-P1-01：三字典访问面收敛单线程）
            DrainEvents();
            List<QqTarget> targets = new List<QqTarget>();
            if (_collector != null)
            {
                targets = _collector.CollectAll();
            }
            TickTargets(targets);
        }

        /// <summary>
        /// 转发轮询内核——按目标集合逐猫推进（Tick 的宿主门面之外的全部逻辑；单测可直接喂目标集合）。
        /// </summary>
        /// <param name="targets">绑定目标集合</param>
        internal static void TickTargets(List<QqTarget> targets)
        {
            for (int i = 0; i < targets.Count; i = i + 1)
            {
                QqTarget tg = targets[i];
                if (tg == null || tg.GetViewItems == null)
                {
                    continue;
                }
                QqViewItem[] items = tg.GetViewItems();
                int count = items.Length;
                // timeback 活跃期——不计入转发（design-ch4-timeback §11.1）：游标跟随但不消费（取证过程不外发、
                // 来源与轮状态保留至结论轮）；回卷不改变块数语义（视图层零重建，游标天然连续）
                if (tg.IsTimebackActive != null && tg.IsTimebackActive())
                {
                    if (GetCursor(tg.Key) != count)
                    {
                        _cursors[tg.Key] = count;
                        SaveForwardState();
                    }
                    continue;
                }
                int cursor = GetCursor(tg.Key);
                // 位置夹取（持久区只增 ⇒ 位置语义天然稳定；越界 = 旧快照 / 视图收缩 → 保守前看，不追发历史）
                // 🔴 越界必须回写 + 出声——只改局部变量会让游标永久失配、扫描面整体停摆（A207 根因）
                if (count < cursor)
                {
                    LogStore.Add("QQBot", 2, "游标越界夹取 | " + tg.Key + " | " + cursor.ToString() + " → " + count.ToString() + "（视图收缩 / 旧快照——保守前看，已回写）", "QQBOT");
                    cursor = count;
                    _cursors[tg.Key] = cursor;
                    SaveForwardState();
                }
                if (count <= cursor)
                {
                    // 无新块——残留来源兜底清理（会话 Idle 且队列残留 = 该消息未开启任何轮）
                    // 已认领来源（工具主动 done 保留 / 接力载入）免疫——留给真正的回复轮消费
                    if (HasPendingSource(tg.Key) && tg.IsIdle())
                    {
                        if (PeekClaimedSource(tg.Key) != null)
                        {
                            // 轮已开启未收口——保留：由回执轮 / 唤醒轮经 FinishRound 消费
                        }
                        else
                        {
                            ClearRoundState(tg);
                        }
                    }
                    continue;
                }
                for (int j = cursor; j < count; j = j + 1)
                {
                    QqViewItem it = items[j];
                    if (it.RenderType == "user" && it.Origin != null && it.Origin.Length > 0)
                    {
                        // 轮起点——本猫 QQ 注入消息开启本轮：窗已开（上一轮无 roundsum 未收口）→ 先按自然收尾收口
                        if (WindowOpen(tg.Key))
                        {
                            FinishRound(tg, "stream");
                        }
                        ClaimSource(tg.Key);
                        continue;
                    }
                    if (it.RenderType == "roundsum")
                    {
                        // 轮结束哨兵——最终回复池切分发送（≤2 段）+ 来源按 done 处置 + 重置轮状态（窗未开则不理）
                        if (WindowOpen(tg.Key))
                        {
                            FinishRound(tg, it.Done);
                        }
                    }
                    else if ((it.RenderType == "text" || it.RenderType == "gap_text") && it.Content.Length > 0)
                    {
                        // 即时转发 ≤2（短块）——正式回复与工具轮间隙文本（A188 分型）同走此路；超长块与超额块累计到轮末最终回复池
                        ForwardText(tg, it.Content);
                    }
                }
                _cursors[tg.Key] = count;
                // A111——末次处理块的内容指纹（块序变更后续接锚点）
                RecordAnchor(tg, count - 1);
                // A33——游标推进即落盘（T4 收尾不调 QQ Stop，退出前 flush 不可依赖）
                SaveForwardState();
            }
        }

        /// <summary>记录续接锚点——末次处理块的内容指纹（A111：块序变更后按指纹重定位游标，不靠裸块数）。</summary>
        /// <param name="tg">绑定目标</param>
        /// <param name="index">末次处理块下标（&lt; 0 不记）</param>
        private static void RecordAnchor(QqTarget tg, int index)
        {
            if (index < 0 || tg.GetBlockFingerprint == null)
            {
                return;
            }
            string fp = tg.GetBlockFingerprint(index);
            if (fp != null && fp.Length > 0)
            {
                _anchors[tg.Key] = fp;
            }
        }
        /// <summary>读取游标——缺省 0</summary>
        private static int GetCursor(string catKey)
        {
            int c;
            if (_cursors.TryGetValue(catKey, out c))
            {
                return c;
            }
            return 0;
        }

        /// <summary>
        /// 启用转发态落盘/读回——单测用（生产由入口壳 SetStatePath + Start 设置；空路径=禁用）。
        /// </summary>
        /// <param name="path">状态文件路径（空=禁用）</param>
        internal static void EnableStateForTest(string path)
        {
            _statePath = path == null ? "" : path;
            _stateLoaded = _statePath.Length > 0;
        }

        /// <summary>
        /// 清空转发态——单测隔离用（生产不调用：状态只由 Tick / LoadForwardState 驱动）。
        /// </summary>
        internal static void ResetStateForTest()
        {
            _sourceQueues.Clear();
            _cursors.Clear();
            _anchors.Clear();
            _immediateCounts.Clear();
            _accumulated.Clear();
            _roundCalls.Clear();
            _imageCounts.Clear();
            while (_eventQueue.TryDequeue(out _))
            {
                // 排空事件队列（异步来源事件不进测试）
            }
        }

        /// <summary>
        /// 转发态摘要——诊断与单测观测面（游标 / 锚点 / 转发窗 / 队列与认领数 / 轮内计数）。
        /// </summary>
        /// <param name="catKey">猫标识</param>
        /// <returns>紧凑状态串</returns>
        internal static string DescribeState(string catKey)
        {
            int cursor = GetCursor(catKey);
            int anchored = _anchors.ContainsKey(catKey) ? 1 : 0;
            int open = WindowOpen(catKey) ? 1 : 0;
            int pending = 0;
            int claimed = 0;
            Queue<QqPendingSource> q;
            if (_sourceQueues.TryGetValue(catKey, out q))
            {
                pending = q.Count;
                QqPendingSource[] all = q.ToArray();
                for (int i = 0; i < all.Length; i = i + 1)
                {
                    if (all[i].Claimed)
                    {
                        claimed = claimed + 1;
                    }
                }
            }
            int imm;
            if (!_immediateCounts.TryGetValue(catKey, out imm))
            {
                imm = 0;
            }
            int calls;
            if (!_roundCalls.TryGetValue(catKey, out calls))
            {
                calls = 0;
            }
            return "cursor=" + cursor.ToString() + " anchor=" + anchored.ToString() + " window=" + open.ToString()
                + " queue=" + pending.ToString() + " claimed=" + claimed.ToString()
                + " imm=" + imm.ToString() + " calls=" + calls.ToString();
        }

        /// <summary>游标夹取——限定在 [0, 块数]（越界安全）</summary>
        /// <param name="value">待夹取值</param>
        /// <param name="count">块数上界</param>
        /// <returns>夹取后的游标</returns>
        private static int ClampCursor(int value, int count)
        {
            if (value < 0)
            {
                return 0;
            }
            if (value > count)
            {
                return count;
            }
            return value;
        }

        /// <summary>
        /// 发送回复（被动优先 + 超窗降级）——A217 定则：**仅私聊**且失败码为 **40034005（msg_id 窗口过期）** 时，
        /// 清 msg_id 走主动消息重试一次；群聊无主动权限（40034105 实测），不做降级尝试。成功直返；未降级面 L2 留痕（响应体由 SendReply 留）。
        /// </summary>
        /// <param name="conn">Bot 连接</param>
        /// <param name="msgType">消息类型——private / group</param>
        /// <param name="targetId">目标 ID</param>
        /// <param name="text">回复正文</param>
        /// <param name="msgId">被动回复引用（空=主动消息，不再降级）</param>
        /// <param name="isMarkdown">true=MD 通道</param>
        /// <returns>发送成功 true</returns>
        private static bool SendWithFallback(QQBotConnection conn, string msgType, string targetId, string text, string msgId, bool isMarkdown)
        {
            if (msgId == null || msgId.Length == 0)
            {
                // 本就主动通道——无被动引用可失效，降级无意义
                return conn.SendReply(msgType, targetId, text, "", isMarkdown);
            }
            string errCode = "";
            if (conn.SendReplyWithCode(msgType, targetId, text, msgId, isMarkdown, out errCode))
            {
                return true;
            }
            // A217——仅私聊 + 仅窗口超时降级；群聊主动消息无权限（40034105），不做尝试
            if (msgType != "private" || errCode != "40034005")
            {
                LogStore.Add("QQBot", 2, "被动回复失败（不降级主动） | " + msgType + " | code=" + errCode, "QQBOT");
                return false;
            }
            bool ok = conn.SendReply(msgType, targetId, text, "", isMarkdown);
            LogStore.Add("QQBot", 2, (ok ? "被动回复超窗 → 主动消息降级成功 | " : "被动回复超窗 → 主动消息降级亦失败 | ") + msgType + ":" + targetId, "QQBOT");
            return ok;
        }

        /// <summary>轮起点锚——QQ 注入标记（落 user 块载荷 origin；转发面据此把来源绑定到该消息开启的轮）</summary>
        private const string QqInjectionOrigin = "qq";

        /// <summary>单段字符上限——切分粒度（社区经验 ~2000，留余量）；超长块不入即时通道，必走轮末切分</summary>
        private const int MaxChunkChars = 1800;

        /// <summary>即时转发上限——2+2 预算：≤2 条即时转发（短块），其余入轮末最终回复池</summary>
        private const int MaxImmediateReplies = 2;

        /// <summary>/last 段数上限——最多 4 段独立发送（末尾段优先，丢弃开头段 + L2 留痕）</summary>
        private const int MaxLastParts = 4;

        /// <summary>轮内被动调用预算——被动回复次数上限（文本段 + 文件发送共用）；统一取 4——私聊实测值；群聊实测为 5（2026-10-10 A215 探针），保守统一不分档。</summary>
        private const int MaxRoundCalls = 4;

        /// <summary>轮内图片发送上限——一轮最多转发 3 张（A216 定死；与文本段 / 文件共用轮内 4 次被动预算，常见形态 1 文本 + 3 图片）</summary>
        private const int MaxImageSends = 3;
        /// <summary>QQ 富文本 faceType 标记正则——表情/大表情图片标记（v0.96.1 简化）</summary>
        private static readonly Regex _faceTagRegex = new Regex("<faceType=[^>]*>", RegexOptions.Compiled);
        /// <summary>转发态文件路径——入口壳注入（A33：Data/qq-forward.json；空=转发态不落盘）</summary>
        private static string _statePath = "";
        /// <summary>转发态加载标志——首帧懒加载（此时前文/视图已恢复，避免读到半成品块数）</summary>
        private static bool _stateLoaded = false;
        /// <summary>截断文本——日志展示</summary>
        private static string Truncate(string s, int max)
        {
            if (s.Length <= max)
            {
                return s;
            }
            return s.Substring(0, max);
        }
        /// <summary>
        /// QQ 服务事件——WS 线程入队 / 主线程 Tick 事件泵消费（R6-P1-01 单线程化）。
        /// Kind：source=注入来源入队（Source 有效） / reset=轮状态重置。
        /// </summary>
        private sealed class QqServiceEvent
        {
            /// <summary>事件类型——source / reset</summary>
            public string Kind;

            /// <summary>猫标识——majordomo / cat id（来源队列键）</summary>
            public string CatKey;

            /// <summary>来源——Kind=source 时有效</summary>
            public QqSource Source;

            /// <summary>轮起点锚——Kind=source 时有效（QQ 注入的 origin；空=无锚）</summary>
            public string Origin;
        }
        /// <summary>
        /// 轮内被动调用预算——文本段与文件发送共用官方 4 次上限（A58）；超限拒绝 + L2 留痕（可诊断）。
        /// </summary>
        /// <param name="catKey">猫标识</param>
        /// <param name="what">调用种类（text / file——日志用）</param>
        /// <returns>可发送 true</returns>
        private static bool TakeRoundBudget(string catKey, string what)
        {
            int n;
            if (!_roundCalls.TryGetValue(catKey, out n))
            {
                n = 0;
            }
            if (n >= MaxRoundCalls)
            {
                LogStore.Add("QQBot", 2, "被动回复预算耗尽（跳过） | " + catKey + " | " + what, "QQBOT");
                return false;
            }
            _roundCalls[catKey] = n + 1;
            return true;
        }
        /// <summary>
        /// 发送单个文本段——私聊 / 群聊统一走 MD 通道 + 被动 msg_id（失败降级主动消息一次）。
        /// </summary>
        /// <param name="tg">绑定目标</param>
        /// <param name="source">来源</param>
        /// <param name="text">段文本</param>
        private static void SendSegment(QqTarget tg, QqSource source, string text)
        {
            QQBotConnection conn;
            if (!_connections.TryGetValue(tg.QqBotId, out conn))
            {
                return;
            }
            SendWithFallback(conn, source.Type, source.TargetId, text, source.MsgId, true);
        }
        /// <summary>
        /// 发送文件到来源——私聊 / 群聊双通道（A215：群聊放开，端点由来源类型分派）；失败错误文本嵌回消息渠道。
        /// </summary>
        /// <param name="tg">绑定目标</param>
        /// <param name="source">来源</param>
        /// <param name="path">本地文件路径</param>
        private static void SendFileToTarget(QqTarget tg, QqSource source, string path)
        {
            QQBotConnection conn;
            if (!_connections.TryGetValue(tg.QqBotId, out conn))
            {
                return;
            }
            string err = conn.SendFile(source.Type, source.TargetId, path, source.MsgId);
            if (err.Length > 0)
            {
                conn.SendReply(source.Type, source.TargetId, err, source.MsgId, false);
            }
        }
        /// <summary>
        /// 文件发送段（A112 统一出口）——逐条经文件通道发送（私聊 / 群聊；失败错误文本由 SendFileToTarget 嵌回消息渠道）。
        /// 转发路与 /last 共用本实现——文件标记扫描（QqFileMarker）与文件发送各只有一处实现。
        /// </summary>
        /// <param name="tg">绑定目标（空=不发送）</param>
        /// <param name="source">来源</param>
        /// <param name="files">文件路径列表</param>
        /// <param name="budgetKey">预算键（猫标识；空=不占预算——调用方已按额度规划）</param>
        private static void SendFilesToTarget(QqTarget tg, QqSource source, List<string> files, string budgetKey)
        {
            if (tg == null || files == null)
            {
                return;
            }
            for (int i = 0; i < files.Count; i = i + 1)
            {
                if (budgetKey.Length > 0 && !TakeRoundBudget(budgetKey, "file"))
                {
                    return;
                }
                SendFileToTarget(tg, source, files[i]);
            }
        }
        /// <summary>
        /// 轮内图片发送额度判定（A216）——定死上限 MaxImageSends 张（跨同一轮多次调用累计）；budgetKey 空 = 不占额度（调用方已按额度规划）。
        /// </summary>
        /// <param name="budgetKey">预算键（猫标识；空=不占额度）</param>
        /// <returns>可发送 true</returns>
        private static bool TakeImageBudget(string budgetKey)
        {
            if (budgetKey.Length == 0)
            {
                return true;
            }
            int n;
            if (!_imageCounts.TryGetValue(budgetKey, out n))
            {
                n = 0;
            }
            if (n >= MaxImageSends)
            {
                return false;
            }
            _imageCounts[budgetKey] = n + 1;
            return true;
        }
        /// <summary>
        /// 图片路径解析（A216）——受控根寻址（&lt;rootId&gt;:&lt;rel&gt;）经 FileSystemService.Resolve 展开为绝对路径；
        /// 绝对路径 / 盘符路径原样返回；http(s) URL 返回空串（QQ 媒体通道只接受本地文件字节，远端不入通道）。
        /// 解析失败原样透传——失败在下游 File.Exists 处可见（不静默改写成别的路径）。
        /// </summary>
        /// <param name="path">包裹条目路径</param>
        /// <param name="catKey">猫标识——猫级文件系统解析键（空=回退全局）</param>
        /// <returns>可用于文件通道的路径（空=不可发送）</returns>
        private static string ResolveImagePath(string path, string catKey)
        {
            if (path == null)
            {
                return "";
            }
            string p = path.Trim();
            if (p.Length == 0)
            {
                return "";
            }
            if (p.StartsWith("http://", StringComparison.OrdinalIgnoreCase) || p.StartsWith("https://", StringComparison.OrdinalIgnoreCase))
            {
                return "";
            }
            // 已绝对路径（盘符 `C:\…`）直接用——不再进 Resolve（全局面按「无匹配根」对根外绝对路径抛越界；判例 2026-10-10 自测）
            if (p.Length >= 2 && p[1] == ':')
            {
                return p;
            }
            // 无命名空间寻址（相对路径 / 裸文件名 / UNC）原样——不猜测根
            if (p.IndexOf(':') <= 0)
            {
                return p;
            }
            // 命名空间寻址（<rootId>:<rel>）——猫级文件系统解析（与 text-* / cs-* 同一实现）；未注册回退全局
            FileSystemService fs = FileSystemRegistry.Resolve(catKey);
            if (fs == null)
            {
                DataBox.TryResolve<FileSystemService>(out fs);
            }
            if (fs == null)
            {
                return p;
            }
            try
            {
                string full = fs.Resolve(p, false);
                if (full != null && full.Length > 0)
                {
                    return full;
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("QQBot", 2, "图片路径解析失败，原样透传 | " + p + " | " + ex.Message, "QQBOT");
            }
            return p;
        }
        /// <summary>
        /// 图片发送段（A216）——逐条经文件通道发送（msg_type=7 · file_type 按扩展名判定，图片=1），每图独立一次调用。
        /// 上限 MaxImageSends（定死 3 张 · 轮内累计）；占轮内被动预算（与文本段 / 文件共用官方 4 次）。
        /// 超限 / 路径不可用一律 L2 留痕（不静默）。/last 路由 budgetKey 空——额度由 planner 按剩余分配，执行期不再判定。
        /// </summary>
        /// <param name="tg">绑定目标（空=不发送）</param>
        /// <param name="source">来源</param>
        /// <param name="images">图片路径列表</param>
        /// <param name="budgetKey">预算键（猫标识；空=不占预算——调用方已按额度规划）</param>
        private static void SendImagesToTarget(QqTarget tg, QqSource source, List<string> images, string budgetKey)
        {
            if (tg == null || images == null)
            {
                return;
            }
            for (int i = 0; i < images.Count; i = i + 1)
            {
                if (!TakeImageBudget(budgetKey))
                {
                    LogStore.Add("QQBot", 2, "图片发送超限（跳过） | " + tg.Key + " | 上限 " + MaxImageSends.ToString() + " 张", "QQBOT");
                    return;
                }
                if (budgetKey.Length > 0 && !TakeRoundBudget(budgetKey, "image"))
                {
                    return;
                }
                string path = ResolveImagePath(images[i], tg.Key);
                if (path.Length == 0)
                {
                    LogStore.Add("QQBot", 2, "图片发送跳过（非本地文件路径） | " + images[i], "QQBOT");
                    continue;
                }
                SendFileToTarget(tg, source, path);
            }
        }
        /// <summary>转发发送统一出口（A58 / A112 / A216）——统一扫描（文件标记 + 图片包裹）→ 正文加【猫名：】前缀 → MD 结构切分 → 逐段发送 → 文件逐条发送 → 图片逐条发送。
        /// 段数超上限丢弃尾部 + L2 留痕；每次被动调用占轮内预算（budgetKey 空 = 不占，指令通道独立配额）。</summary>
        /// <param name="tg">绑定目标</param>
        /// <param name="source">来源</param>
        /// <param name="text">回复正文</param>
        /// <param name="maxParts">段数上限（即时 1 / 轮末最终回复 2）</param>
        /// <param name="budgetKey">预算键（猫标识；空=不占预算）</param>
        private static void SendRouted(QqTarget tg, QqSource source, string text, int maxParts, string budgetKey)
        {
            // [段1] 附件识别——文件标记 + 图片包裹一次扫描（A216 统一识别结构）；避免标记 / 包裹被切分截断（正文与附件分别发送）
            QqReplyScan.Scan(text, tg.ParseImages, out string body, out List<string> files, out List<string> images);
            if (body.Length > 0)
            {
                // [段2] 前缀拼进正文首部再切分——前缀自然计入首段预算（历史规格口径）
                string withPrefix = tg.DisplayName + "：\n" + body;
                List<string> parts = QqTextSplitter.Split(withPrefix, MaxChunkChars);
                if (parts.Count > maxParts)
                {
                    LogStore.Add("QQBot", 2, "切分段数超限（丢弃尾部） | " + tg.Key + " | " + parts.Count.ToString() + " → " + maxParts.ToString() + " 段", "QQBOT");
                }
                int take = maxParts;
                if (parts.Count < maxParts)
                {
                    take = parts.Count;
                }
                for (int i = 0; i < take; i = i + 1)
                {
                    // A58 丙——真发生丢弃（段数超上限）时在末段附截断提示：失败可见，不静默丢
                    string seg = parts[i];
                    if (parts.Count > take && i == take - 1)
                    {
                        seg = seg + "\n\n（内容超长已截断——发 /last 取完整尾部）";
                    }
                    if (budgetKey.Length > 0 && !TakeRoundBudget(budgetKey, "text"))
                    {
                        break;
                    }
                    SendSegment(tg, source, seg);
                }
            }
            // [段3] 文件逐条发送（A112——统一出口；预算与文本段共用官方 4 次上限）
            SendFilesToTarget(tg, source, files, budgetKey);
            // [段4] 图片逐条发送（A216——每图独立一次；上限 3 张；预算与文本段 / 文件共用）
            SendImagesToTarget(tg, source, images, budgetKey);
        }
        /// <summary>/last 回复发送（A58 / A112 / A216）——统一识别结构先扫描剥离文件标记与图片包裹（标记 / 包裹不进正文，附件经对应通道发送，与转发路共用同一实现）；
        /// 正文按 MD 结构切分，最多 4 段独立发送；超出取末尾段（丢弃开头段 + L2 留痕）。
        /// 文本段与附件发送共用同一被动调用上限（planner 按剩余额度分配，执行期不再判定）；不占转发轮预算（指令通道独立 msg_id 配额）；任一段失败停止后续 + L2 留痕。
        /// A208——服务留痕（实际段 / 文件 / 图片数）+ 连接缺失 L2。</summary>
        /// <param name="qqBotId">Bot 配置身份</param>
        /// <param name="source">来源</param>
        /// <param name="body">回复正文（含【猫名：】前缀）</param>
        private static void SendLastReply(Guid qqBotId, QqSource source, string body)
        {
            QQBotConnection conn;
            if (!_connections.TryGetValue(qqBotId, out conn))
            {
                // A208——兜底通道不可达出声（此前静默返回：发送面缺失无从诊断）
                LogStore.Add("QQBot", 2, "/last 发送跳过（连接不存在） | " + qqBotId.ToString(), "QQBOT");
                return;
            }
            // 绑定目标——图片包裹识别委托与附件发送共用（A216：与转发路同一识别结构）
            QqTarget tg = null;
            if (_collector != null)
            {
                List<QqTarget> targets = _collector.CollectByBot(qqBotId);
                if (targets.Count > 0)
                {
                    tg = targets[0];
                }
            }
            // A112 / A216——附件识别统一：文件标记 + 图片包裹一次扫描（标记 / 包裹不进正文，经对应通道发送——与转发路同一实现）
            Func<string, QqImageScan> parseImages = null;
            if (tg != null)
            {
                parseImages = tg.ParseImages;
            }
            QqReplyScan.Scan(body, parseImages, out string textBody, out List<string> files, out List<string> images);
            QqLastReplyPlan plan = QqLastReplyPlanner.Plan(textBody, files, images, MaxChunkChars, MaxLastParts, MaxImageSends);
            // A208——服务留痕（实际发出段数与附件数；此前仅异常出声，兜底发送量不可观测）
            LogStore.Add("QQBot", 1, "/last 服务 | " + source.ToString() + " | " + plan.Segments.Count.ToString() + " 段 + " + plan.Files.Count.ToString() + " 文件 + " + plan.Images.Count.ToString() + " 图片", "QQBOT");
            if (plan.DroppedHeadSegments > 0)
            {
                LogStore.Add("QQBot", 2, "/last 段数超限（取末尾段） | 丢弃前 " + plan.DroppedHeadSegments.ToString() + " 段 | " + source.ToString(), "QQBOT");
            }
            if (plan.DroppedFiles > 0)
            {
                LogStore.Add("QQBot", 2, "/last 文件预算耗尽（跳过） | " + plan.DroppedFiles.ToString() + " 个 | " + source.ToString(), "QQBOT");
            }
            if (plan.DroppedImages > 0)
            {
                LogStore.Add("QQBot", 2, "/last 图片预算耗尽（跳过） | " + plan.DroppedImages.ToString() + " 张 | " + source.ToString(), "QQBOT");
            }
            for (int i = 0; i < plan.Segments.Count; i = i + 1)
            {
                if (!SendWithFallback(conn, source.Type, source.TargetId, plan.Segments[i], source.MsgId, true))
                {
                    LogStore.Add("QQBot", 2, "/last 发送中断（第 " + (i + 1).ToString() + " 段失败） | " + source.ToString(), "QQBOT");
                    return;
                }
            }
            // 附件发送——文本段与附件共用被动调用预算（planner 已按剩余额度截断，执行期不再判定）
            if (plan.Files.Count > 0)
            {
                if (tg == null)
                {
                    LogStore.Add("QQBot", 2, "/last 文件发送跳过（无绑定目标） | " + plan.Files.Count.ToString() + " 个 | " + source.ToString(), "QQBOT");
                }
                else
                {
                    SendFilesToTarget(tg, source, plan.Files, "");
                }
            }
            if (plan.Images.Count > 0)
            {
                if (tg == null)
                {
                    LogStore.Add("QQBot", 2, "/last 图片发送跳过（无绑定目标） | " + plan.Images.Count.ToString() + " 张 | " + source.ToString(), "QQBOT");
                }
                else
                {
                    SendImagesToTarget(tg, source, plan.Images, "");
                }
            }
        }

        /// <summary>
        /// 待认领来源——注入来源 + 轮绑定态（A207 批二：来源与轮绑定）。
        /// 认领 = 该来源触发的消息在视图层出现轮起点块（QQ 注入的 user 块）——转发窗据此开启。
        /// </summary>
        private sealed class QqPendingSource
        {
            /// <summary>来源——被动回复目标（type / targetId / msgId / 昵称 / 角色）</summary>
            public QqSource Source;

            /// <summary>轮起点锚——注入时给的 origin（空=接力载入无锚——载入即认领）</summary>
            public string Origin;

            /// <summary>认领态——轮起点块已到达（转发窗开启；轮末消费 / done=tool 保留）</summary>
            public bool Claimed;
        }
    }

    /// <summary>
    /// QQ 消息来源——结构化载荷（P8：msg_id 被动回复扩展）。
    /// </summary>
    internal sealed class QqSource
    {
        /// <summary>消息类型——private / group</summary>
        public string Type;

        /// <summary>目标 ID——私聊 user_openid / 群聊 gid:mid</summary>
        public string TargetId;

        /// <summary>被动回复引用——事件 d.id；空=非被动</summary>
        public string MsgId;

        /// <summary>发送者昵称——群@ author.username；私聊事件为空串（私聊 header 硬编码雾理莎）</summary>
        public string DisplayName;

        /// <summary>群内角色——owner/admin/member（事件白拿字段）；私聊空</summary>
        public string Role;

        /// <summary>构造来源。</summary>
        /// <param name="type">消息类型</param>
        /// <param name="targetId">目标 ID</param>
        /// <param name="msgId">被动回复 msg_id</param>
        public QqSource(string type, string targetId, string msgId, string displayName = "", string role = "")
        {
            Type = type;
            TargetId = targetId;
            MsgId = msgId;
            DisplayName = displayName;
            Role = role;
        }

        /// <summary>显示——private:uid / group:gid:mid（日志兼容）</summary>
        /// <returns>来源文本</returns>
        public override string ToString()
        {
            return Type + ":" + TargetId;
        }
    }

    /// <summary>
    /// QQ 附件——事件 attachments[] 载荷（P9 接收面）。
    /// </summary>
    internal sealed class QqAttachment
    {
        /// <summary>下载 URL——带时效签名（rkey），收到后尽快下载</summary>
        public string Url;

        /// <summary>文件名</summary>
        public string FileName;

        /// <summary>本地缓存路径——下载成功后写回；原位嵌入用（v0.96.1）</summary>
        public string LocalPath;

        /// <summary>大小（字节）</summary>
        public long Size;

        /// <summary>内容类型（image/jpeg / video/mp4 / voice / file 等）</summary>
        public string ContentType;
    }

    /// <summary>
    /// qqbot 绑定目标——默认猫 / 多猫统一抽象（路由注入面 + 输出转发面）。
    /// </summary>
    public sealed class QqTarget
    {
        /// <summary>猫标识——majordomo / cat id（来源队列键）</summary>
        public string Key;

        /// <summary>显示名——输出转发【Cat名：】前缀</summary>
        public string DisplayName;

        /// <summary>绑定的 qqbot 配置身份——Guid.Empty=未绑定</summary>
        public Guid QqBotId;

        /// <summary>启用标志——false=不注入不转发</summary>
        public bool Enable;

        /// <summary>注入回调——PostUserMessage(content, origin)；返回 false=未受理（停机态 / 空内容——调用方必须出声）</summary>
        public Func<string, string, bool> Inject;

        /// <summary>视图块流读取——游标增量轮询（QQBot 转发数据源：text=转发候选 / roundsum=轮结束哨兵 / user+origin=轮起点）</summary>
        public Func<QqViewItem[]> GetViewItems;

        /// <summary>块内容指纹读取——按块下标取内容指纹（A111 续接锚点；越界返回空串；null=不做锚点续接）</summary>
        public Func<int, string> GetBlockFingerprint;

        /// <summary>图片包裹识别——回复文本 → 正文（剥离包裹）+ 图片路径（A216：Core 域协议经装配侧桥接注入；null=不识别图片）</summary>
        public Func<string, QqImageScan> ParseImages;

        /// <summary>会话空闲判定——异常中止轮残留来源兜底清理（null=永不空闲）</summary>
        public Func<bool> IsIdle;

        /// <summary>timeback 活跃判定——活跃期不计入转发（游标跟随但不消费、来源保留；null=视为不活跃）</summary>
        public Func<bool> IsTimebackActive;

        /// <summary>新会话桥——触发 session.new（异步置位）+ 返回注入摘要文本（Q5 /new 指令；null=不支持）</summary>
        public Func<string> NewSession;

        /// <summary>会话信息桥——猫 info 工具文本（Q5 /info 指令合并；null=不支持）</summary>
        public Func<string> GetInfo;
    }

    /// <summary>
    /// QQ 转发视图项——视图块窄 DTO（转发面消费 RenderType + Content + Done + Origin；装配侧从 SessionViewStore 转换——解耦 QQ 域与视图层内部结构）。
    /// </summary>
    public sealed class QqViewItem
    {
        /// <summary>渲染类型——user=轮起点候选（Origin 非空即 QQ 注入的轮起点） / text=转发候选 / roundsum=轮结束哨兵 / 其他跳过</summary>
        public string RenderType;

        /// <summary>文本内容——text 块内容（已解析 payload.text）；其他类型空串</summary>
        public string Content;

        /// <summary>本轮结束语义——roundsum 块携带（tool=工具主动 done / stream=流式自然收尾）；其他类型空串</summary>
        public string Done;

        /// <summary>原注入来源标记——user 块携带（非空=QQ 注入的轮起点；其他类型空串）</summary>
        public string Origin;
    }

}
