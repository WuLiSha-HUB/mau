#nullable disable warnings
using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using Mau.Runtime;

namespace Mau.Providers
{
    /// <summary>
    /// 群目录条目——一个机器人在一个群里的登记（A380）。群名 / 别名 / 权限三段各自可空，
    /// 空值表示「未知」，不表示「无」。
    /// </summary>
    public sealed class QqBotGroupEntry
    {
        /// <summary>群 OpenID（稳定主键）。</summary>
        public string OpenId = "";

        /// <summary>别名——工具调用用它寻址（bot 内唯一，人工起名）。</summary>
        public string Alias = "";

        /// <summary>群名称——来自官方 /info 或人工填写。</summary>
        public string Title = "";

        /// <summary>群名来源——none / auto（官方接口）/ manual（人工）。</summary>
        public string TitleSource = "none";

        /// <summary>机器人在群内角色——member / admin / owner（来自消息事件，未知为空）。</summary>
        public string Role = "";

        /// <summary>群主是否允许机器人主动发言（官方 bot_state 真值；未知为 false）。</summary>
        public bool AllowProactive = false;

        /// <summary>收信范围——all / only_mention / mention_and_context（未知为空）。</summary>
        public string RecvSetting = "";

        /// <summary>首次见到时间（yyyy-MM-dd HH:mm:ss）。</summary>
        public string FirstSeen = "";

        /// <summary>最近见到时间（yyyy-MM-dd HH:mm:ss）。</summary>
        public string LastSeen = "";
        /// <summary>最近一次官方状态刷新失败的时刻（空 = 无失败记录；非空 = 该群状态未确认，可能已退群或接口无权限）。</summary>
        public string LastRefreshFail = "";
    }

    /// <summary>
    /// 群目录文档——一个 bot 一份 <c>Data/qq/bots/&lt;appId&gt;/groups.json</c>。
    /// 用 JSON 而不是 JSONL：这份文件要按条目改写（别名 / 权限），不是纯追加流。
    /// </summary>
    public sealed class QqBotGroupDocument
    {
        /// <summary>文档结构版本。</summary>
        public int SchemaVersion = 1;

        /// <summary>群条目集合（按首次见到顺序）。</summary>
        public List<QqBotGroupEntry> Groups = new List<QqBotGroupEntry>();
    }

    /// <summary>
    /// 群目录与别名解析（A380）——把「群」升级成一级对象：工具调用用别名寻址，不必记 OpenID。
    /// 数据面：<c>Data/qq/bots/&lt;appId&gt;/groups.json</c>（原子写）。
    /// 解析顺序：精确别名 → OpenID 精确 → 唯一前缀/包含命中 → 失败附候选。
    /// </summary>
    public sealed class QqBotGroupDirectory
    {
        /// <summary>机器人目录根（Data/qq/bots）。</summary>
        private readonly string _botsRoot;

        /// <summary>统一 JSON 选项——字段序列化 + 驼峰（与配置池同口径）。</summary>
        private readonly JsonSerializerOptions _jsonOptions;

        /// <summary>
        /// 构造群目录。
        /// </summary>
        /// <param name="botsRoot">机器人目录根（Data/qq/bots）</param>
        public QqBotGroupDirectory(string botsRoot)
        {
            _botsRoot = botsRoot == null ? "" : botsRoot;
            _jsonOptions = new JsonSerializerOptions();
            _jsonOptions.IncludeFields = true;
            _jsonOptions.PropertyNamingPolicy = JsonNamingPolicy.CamelCase;
            _jsonOptions.WriteIndented = true;
        }

        /// <summary>
        /// 读取一个 bot 的群目录（文件不存在 / 损坏返回空目录，不抛）。
        /// </summary>
        /// <param name="appId">AppID</param>
        /// <returns>群目录文档（非 null）</returns>
        public QqBotGroupDocument Load(string appId)
        {
            QqBotGroupDocument doc = new QqBotGroupDocument();
            string path = DocumentPath(appId);
            if (path.Length == 0 || !File.Exists(path))
            {
                return doc;
            }
            try
            {
                string json = File.ReadAllText(path);
                QqBotGroupDocument loaded = JsonSerializer.Deserialize<QqBotGroupDocument>(json, _jsonOptions);
                if (loaded != null && loaded.Groups != null)
                {
                    return loaded;
                }
                LogStore.Add("QQBot", 2, "QQBot | 群目录结构异常（按空目录处理）: " + path, "QQBOT");
            }
            catch (Exception ex)
            {
                LogStore.Add("QQBot", 2, "QQBot | 群目录读取失败（按空目录处理）: " + ex.Message + " | " + path, "QQBOT");
            }
            return doc;
        }

        /// <summary>
        /// 记录「见到过这个群」——新增时置首次见到，已存在时刷新角色与最近见到时间。
        /// </summary>
        /// <param name="appId">AppID</param>
        /// <param name="openId">群 OpenID</param>
        /// <param name="role">机器人在群内角色（空=未知）</param>
        /// <param name="stamp">时间戳（yyyy-MM-dd HH:mm:ss）</param>
        /// <returns>空串成功；否则为失败原因</returns>
        public string UpsertSeen(string appId, string openId, string role, string stamp)
        {
            if (openId == null || openId.Length == 0)
            {
                return "群 OpenID 为空";
            }
            QqBotGroupDocument doc = Load(appId);
            QqBotGroupEntry entry = FindByOpenId(doc, openId);
            if (entry == null)
            {
                entry = new QqBotGroupEntry();
                entry.OpenId = openId;
                entry.FirstSeen = stamp;
                doc.Groups.Add(entry);
            }
            entry.LastSeen = stamp;
            if (role != null && role.Length > 0)
            {
                entry.Role = role;
            }
            return Save(appId, doc);
        }

        /// <summary>
        /// 写入权限与群名（来源 = 官方接口）——群名为空或已被人工命名时不覆盖群名。
        /// </summary>
        /// <param name="appId">AppID</param>
        /// <param name="openId">群 OpenID</param>
        /// <param name="title">官方群名（可空）</param>
        /// <param name="allowProactive">是否允许主动发言</param>
        /// <param name="recvSetting">收信范围</param>
        /// <param name="role">机器人在群内的角色（bot_state.member_role，可空）</param>
        /// <returns>空串成功；否则为失败原因</returns>
        public string ApplyOfficialState(string appId, string openId, string title, bool allowProactive, string recvSetting, string role)
        {
            QqBotGroupDocument doc = Load(appId);
            QqBotGroupEntry entry = FindByOpenId(doc, openId);
            if (entry == null)
            {
                return "群未登记: " + openId;
            }
            entry.AllowProactive = allowProactive;
            entry.LastRefreshFail = "";
            if (recvSetting != null && recvSetting.Length > 0)
            {
                entry.RecvSetting = recvSetting;
            }
            if (role != null && role.Length > 0)
            {
                entry.Role = role;
            }
            if (title != null && title.Length > 0 && entry.TitleSource != "manual")
            {
                entry.Title = title;
                entry.TitleSource = "auto";
            }
            return Save(appId, doc);
        }

        /// <summary>
        /// 人工命名——同一 bot 内别名唯一（重名报错，不静默覆盖）。
        /// </summary>
        /// <param name="appId">AppID</param>
        /// <param name="groupRef">群寻址（别名 / 唯一前缀 / OpenID）</param>
        /// <param name="alias">新别名（空=清除别名）</param>
        /// <returns>空串成功；否则为失败原因</returns>
        public string SetAlias(string appId, string groupRef, string alias)
        {
            QqBotGroupEntry resolved;
            string candidates;
            if (!TryResolve(appId, groupRef, out resolved, out candidates))
            {
                return "群未命中: " + groupRef + (candidates.Length > 0 ? "（候选: " + candidates + "）" : "");
            }
            // TryResolve 内部自行 Load——返回的条目属于另一份文档实例；改动须回绑到本次 doc 上，否则 Save 把别名静默丢弃（只报成功、重名保护同时失效）
            QqBotGroupDocument doc = Load(appId);
            QqBotGroupEntry entry = FindByOpenId(doc, resolved.OpenId);
            if (entry == null)
            {
                return "群未登记: " + resolved.OpenId;
            }
            string target = alias == null ? "" : alias.Trim();
            if (target.Length > 0)
            {
                for (int i = 0; i < doc.Groups.Count; i = i + 1)
                {
                    if (doc.Groups[i] != entry
                        && string.Equals(doc.Groups[i].Alias, target, StringComparison.OrdinalIgnoreCase))
                    {
                        return "别名已被占用: " + target + "（群 " + doc.Groups[i].OpenId + "）";
                    }
                }
            }
            entry.Alias = target;
            return Save(appId, doc);
        }

        /// <summary>
        /// 人工登记一个群（没说过话的群也能进目录）。
        /// </summary>
        /// <param name="appId">AppID</param>
        /// <param name="openId">群 OpenID</param>
        /// <returns>空串成功；否则为失败原因</returns>
        public string AddGroup(string appId, string openId)
        {
            if (openId == null || openId.Trim().Length == 0)
            {
                return "群 OpenID 为空";
            }
            string stamp = DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss");
            return UpsertSeen(appId, openId.Trim(), "", stamp);
        }
        /// <summary>
        /// 记录一次官方状态刷新失败（A384）——条目标为「未确认」并清掉旧权限值，避免被踢出后仍显示「允许主动发言」。
        /// </summary>
        /// <param name="appId">AppID</param>
        /// <param name="openId">群 OpenID</param>
        /// <param name="stamp">失败时刻（yyyy-MM-dd HH:mm:ss）</param>
        /// <returns>空串成功；否则为失败原因</returns>
        public string ApplyRefreshFailure(string appId, string openId, string stamp)
        {
            QqBotGroupDocument doc = Load(appId);
            QqBotGroupEntry entry = FindByOpenId(doc, openId);
            if (entry == null)
            {
                return "群未登记: " + openId;
            }
            entry.LastRefreshFail = stamp == null ? "" : stamp;
            entry.Role = "";
            entry.RecvSetting = "";
            entry.AllowProactive = false;
            return Save(appId, doc);
        }
        /// <summary>
        /// 移除群条目（A384）——被踢出或永不再用的群从目录清掉，补上「只进不出」的出口。
        /// 只动本地目录，不改平台侧归属（机器人在不在群里由平台决定）。
        /// </summary>
        /// <param name="appId">AppID</param>
        /// <param name="groupRef">群寻址（别名 / 群名 / OpenID / 前缀）</param>
        /// <returns>空串成功；否则为失败原因</returns>
        public string RemoveGroup(string appId, string groupRef)
        {
            QqBotGroupEntry resolved;
            string candidates;
            if (!TryResolve(appId, groupRef, out resolved, out candidates))
            {
                return "群未命中: " + groupRef + (candidates.Length > 0 ? "（候选: " + candidates + "）" : "");
            }
            QqBotGroupDocument doc = Load(appId);
            int index = -1;
            for (int i = 0; i < doc.Groups.Count; i = i + 1)
            {
                if (string.Equals(doc.Groups[i].OpenId, resolved.OpenId, StringComparison.OrdinalIgnoreCase))
                {
                    index = i;
                    break;
                }
            }
            if (index < 0)
            {
                return "群未登记: " + resolved.OpenId;
            }
            doc.Groups.RemoveAt(index);
            return Save(appId, doc);
        }

        /// <summary>
        /// 群寻址解析——精确别名 → OpenID 精确 → 唯一前缀/包含命中。
        /// </summary>
        /// <param name="appId">AppID</param>
        /// <param name="groupRef">寻址串（别名 / 群名 / OpenID / 前缀）</param>
        /// <param name="entry">命中条目</param>
        /// <param name="candidates">未命中时的候选清单（单行，空=目录为空）</param>
        /// <returns>true=唯一命中</returns>
        public bool TryResolve(string appId, string groupRef, out QqBotGroupEntry entry, out string candidates)
        {
            entry = null;
            candidates = "";
            QqBotGroupDocument doc = Load(appId);
            string reference = groupRef == null ? "" : groupRef.Trim();
            if (reference.Length == 0 || doc.Groups.Count == 0)
            {
                return false;
            }
            // [段1] 精确别名 / 精确 OpenID
            for (int i = 0; i < doc.Groups.Count; i = i + 1)
            {
                QqBotGroupEntry item = doc.Groups[i];
                if (string.Equals(item.Alias, reference, StringComparison.OrdinalIgnoreCase)
                    || string.Equals(item.OpenId, reference, StringComparison.OrdinalIgnoreCase))
                {
                    entry = item;
                    return true;
                }
            }
            // [段2] 唯一前缀 / 包含命中（别名 > 群名 > OpenID）——多处命中即歧义，不猜
            List<QqBotGroupEntry> hits = new List<QqBotGroupEntry>();
            for (int i = 0; i < doc.Groups.Count; i = i + 1)
            {
                QqBotGroupEntry item = doc.Groups[i];
                if (Contains(item.Alias, reference) || Contains(item.Title, reference)
                    || Contains(item.OpenId, reference))
                {
                    hits.Add(item);
                }
            }
            if (hits.Count == 1)
            {
                entry = hits[0];
                return true;
            }
            if (hits.Count > 1)
            {
                candidates = Describe(hits) + "（多个群命中，请用别名或完整 OpenID）";
                return false;
            }
            candidates = Describe(doc.Groups);
            return false;
        }

        /// <summary>
        /// 目录摘要（单行，给 /botinfo 与工具回执用）。
        /// </summary>
        /// <param name="doc">群目录</param>
        /// <returns>形如「别名(群名/末8位) × N」的单行文本</returns>
        public static string Describe(List<QqBotGroupEntry> groups)
        {
            if (groups == null || groups.Count == 0)
            {
                return "";
            }
            List<string> parts = new List<string>();
            for (int i = 0; i < groups.Count; i = i + 1)
            {
                parts.Add(Label(groups[i]));
            }
            return string.Join(" / ", parts.ToArray());
        }

        /// <summary>
        /// 单群标签——别名优先，其次群名，再次 OpenID 末 8 位。
        /// </summary>
        /// <param name="entry">群条目</param>
        /// <returns>可读标签</returns>
        public static string Label(QqBotGroupEntry entry)
        {
            if (entry == null)
            {
                return "";
            }
            if (entry.Alias.Length > 0)
            {
                if (entry.Title.Length > 0)
                {
                    return entry.Alias + "（" + entry.Title + "）";
                }
                return entry.Alias;
            }
            // 无别名时用官方群名——回执与工具输出都要人能认出来是哪个群
            if (entry.Title.Length > 0)
            {
                return entry.Title;
            }
            return entry.OpenId.Length > 8 ? entry.OpenId.Substring(entry.OpenId.Length - 8) : entry.OpenId;
        }

        /// <summary>
        /// 写盘——原子替换（临时文件 + 更名）。
        /// </summary>
        /// <param name="appId">AppID</param>
        /// <param name="doc">群目录文档</param>
        /// <returns>空串成功；否则为失败原因</returns>
        private string Save(string appId, QqBotGroupDocument doc)
        {
            string path = DocumentPath(appId);
            if (path.Length == 0)
            {
                return "AppID 为空，无法定位群目录";
            }
            string json = JsonSerializer.Serialize(doc, _jsonOptions);
            try
            {
                CH_AtomicTextFile.Write(path, json);
                return "";
            }
            catch (Exception ex)
            {
                LogStore.Add("QQBot", 3, "QQBot | 群目录写入失败: " + ex.Message + " | " + path, "QQBOT");
                return "群目录写入失败: " + ex.Message;
            }
        }

        /// <summary>
        /// 文档路径——Data/qq/bots/&lt;appId&gt;/groups.json。
        /// </summary>
        /// <param name="appId">AppID</param>
        /// <returns>绝对路径（AppID 为空返回空串）</returns>
        private string DocumentPath(string appId)
        {
            string segment = SafeSegment(appId);
            if (_botsRoot.Length == 0 || segment.Length == 0)
            {
                return "";
            }
            return Path.Combine(_botsRoot, segment, "groups.json");
        }

        /// <summary>
        /// 按 OpenID 找条目。
        /// </summary>
        /// <param name="doc">群目录</param>
        /// <param name="openId">群 OpenID</param>
        /// <returns>条目或 null</returns>
        private static QqBotGroupEntry FindByOpenId(QqBotGroupDocument doc, string openId)
        {
            for (int i = 0; i < doc.Groups.Count; i = i + 1)
            {
                if (string.Equals(doc.Groups[i].OpenId, openId, StringComparison.OrdinalIgnoreCase))
                {
                    return doc.Groups[i];
                }
            }
            return null;
        }

        /// <summary>
        /// 包含匹配（忽略大小写）——空串一律不命中，避免空别名把所有群匹配上。
        /// </summary>
        /// <param name="text">被搜索文本</param>
        /// <param name="reference">搜索串</param>
        /// <returns>true=命中</returns>
        private static bool Contains(string text, string reference)
        {
            if (text == null || text.Length == 0 || reference.Length == 0)
            {
                return false;
            }
            return text.IndexOf(reference, StringComparison.OrdinalIgnoreCase) >= 0;
        }

        /// <summary>
        /// 路径段消毒——AppID 作为目录名，去掉路径分隔符与保留字符。
        /// </summary>
        /// <param name="segment">原始段</param>
        /// <returns>安全段（非法字符替换为空）</returns>
        private static string SafeSegment(string segment)
        {
            if (segment == null)
            {
                return "";
            }
            string trimmed = segment.Trim();
            char[] bad = Path.GetInvalidFileNameChars();
            System.Text.StringBuilder builder = new System.Text.StringBuilder();
            for (int i = 0; i < trimmed.Length; i = i + 1)
            {
                char c = trimmed[i];
                bool isBad = false;
                for (int j = 0; j < bad.Length; j = j + 1)
                {
                    if (bad[j] == c)
                    {
                        isBad = true;
                        break;
                    }
                }
                if (!isBad && c != '.' && c != ':')
                {
                    builder.Append(c);
                }
            }
            return builder.ToString();
        }
    }
}
