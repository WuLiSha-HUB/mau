using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using System.Text.Json;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// 组投影——组配置 + 消息流统计（端点与前端消费；不落盘，派生自 group.json 与 messages.jsonl）。
    /// </summary>
    public sealed class GroupSummary
    {
        /// <summary>组名——唯一键（目录名）</summary>
        public string Name = "";

        /// <summary>成员猫 key 列表（= Data/sessions/&lt;key&gt; 目录名；显示名由消费方解析）</summary>
        public string[] MemberKeys = new string[0];

        /// <summary>管理员猫 key 列表（显式配置；@ 派单权限来源）</summary>
        public string[] AdminKeys = new string[0];
        /// <summary>订阅自动转发的猫 key 列表（成员子集；组内新消息自动投递给这些猫）</summary>
        public string[] SubscriberKeys = new string[0];

        /// <summary>项目绑定——自由文本（可空）</summary>
        public string Project = "";

        /// <summary>创建时刻——Unix 毫秒（UTC）</summary>
        public long CreatedAt;

        /// <summary>层级——预留字段（分组维度，当前未消费）</summary>
        public string Layer = "";

        /// <summary>启用标记——false = 组停用（落盘意图；运行态见 Running）</summary>
        public bool Enabled = true;

        /// <summary>运行态——true = 已加载并在运行（工具面可用）；false = 未启用（不扫消息流、工具面拒绝）</summary>
        public bool Running;

        /// <summary>组内最大消息序号——0 = 无消息</summary>
        public long LastSeq;

        /// <summary>消息条数</summary>
        public int MsgCount;

        /// <summary>最新消息时刻——Unix 毫秒（0 = 无消息）</summary>
        public long LastAt;
        /// <summary>订阅推送静默阈值——秒；0 = 未设置（按默认 300 秒计）</summary>
        public int SubscribeIdleSec;
        /// <summary>订阅者未读游标——猫 key → 已推送到的 seq（缺省 0 = 从头）</summary>
        public Dictionary<string, long> SubscribeCursors = new Dictionary<string, long>(StringComparer.OrdinalIgnoreCase);
    }

    /// <summary>
    /// 组消息——messages.jsonl 一行一条（design-ch4-cat-delegation §3.2）。
    /// </summary>
    public sealed class GroupMessage
    {
        /// <summary>组内单调递增序号——分配器归宿主</summary>
        public long Seq;

        /// <summary>发送方猫 key（空 = 系统/管理员）</summary>
        public string From = "";

        /// <summary>@ 目标猫 key（空 = 不指向具体猫；非空 = 定向投递/提醒）</summary>
        public string To = "";

        /// <summary>消息类型——delegation 派单 / receipt 回执 / note 其它</summary>
        public string Type = "note";

        /// <summary>正文</summary>
        public string Text = "";

        /// <summary>发送时刻——Unix 毫秒（UTC）</summary>
        public long Ts;

        /// <summary>关联锚——单号 / 轮次（可空）</summary>
        public string Refs = "";
    }

    /// <summary>
    /// 组数据面（A300）——Data/group/&lt;组名&gt;/{group.json, messages.jsonl}。
    /// 语义：组配置与消息流是 Harness 的核心数据面；落盘范式照 delays.json（变更即原子写 + 重启续接）。
    /// 线程模型：全部操作锁内完成（HTTP 线程 / 主线程 / 后续轻宿主）。
    /// </summary>
    public static class GroupStore
    {
        /// <summary>组名长度上限</summary>
        public const int MaxNameLength = 32;

        /// <summary>单次读消息条数上限</summary>
        public const int MaxReadLimit = 500;

        /// <summary>单次读消息条数缺省值</summary>
        public const int DefaultReadLimit = 100;

        /// <summary>订阅推送静默阈值缺省值（秒）</summary>
        public const int DefaultSubscribeIdleSec = 300;

        /// <summary>组根目录——Data/group（空 = 仅内存，不落盘）</summary>
        private static string _root = "";

        /// <summary>组缓存——组名 → 配置 + 统计（大小写不敏感，避免同组两目录）</summary>
        private static Dictionary<string, GroupSummary> _groups = new Dictionary<string, GroupSummary>(StringComparer.OrdinalIgnoreCase);

        /// <summary>存储锁——全部读写锁内完成</summary>
        private static readonly object StoreLock = new object();

        /// <summary>
        /// 配置组根目录——宿主启动期注入（Core 不依赖配置面，路径由入口壳给）。
        /// </summary>
        /// <param name="root">组根目录绝对路径（空 = 仅内存）</param>
        public static void Configure(string root)
        {
            lock (StoreLock)
            {
                if (root == null)
                {
                    _root = "";
                }
                else
                {
                    _root = root;
                }
                _groups = new Dictionary<string, GroupSummary>(StringComparer.OrdinalIgnoreCase);
            }
        }

        /// <summary>
        /// 启动加载——扫描组根目录下全部组，读配置并统计消息流（重启续接）。
        /// </summary>
        public static void Load()
        {
            lock (StoreLock)
            {
                _groups = new Dictionary<string, GroupSummary>(StringComparer.OrdinalIgnoreCase);
                if (_root.Length == 0)
                {
                    return;
                }
                if (!Directory.Exists(_root))
                {
                    return;
                }
                string[] dirs;
                try
                {
                    dirs = Directory.GetDirectories(_root);
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 2, "组目录枚举失败: " + ex.Message, "GROUP");
                    return;
                }
                int loaded = 0;
                for (int i = 0; i < dirs.Length; i = i + 1)
                {
                    string dirName = Path.GetFileName(dirs[i]);
                    if (string.Equals(dirName, "_trash", StringComparison.OrdinalIgnoreCase))
                    {
                        continue;
                    }
                    GroupSummary info = ReadGroupFile(dirs[i], dirName);
                    if (info == null)
                    {
                        continue;
                    }
                    // 启用组才扫消息流并进入运行态；停用组保持轻量条目（配置在内存、统计归零、零消息流 IO）
                    if (info.Enabled)
                    {
                        ScanMessages(dirs[i], info);
                        info.Running = true;
                    }
                    _groups[info.Name] = info;
                    loaded = loaded + 1;
                }
                if (loaded > 0)
                {
                    LogStore.Add("CatHome4", 1, "组数据面已加载：" + loaded.ToString() + " 组", "GROUP");
                }
            }
        }

        /// <summary>
        /// 组目录路径——Data/group/&lt;组名&gt;。
        /// </summary>
        /// <param name="name">组名</param>
        /// <returns>目录绝对路径</returns>
        private static string GetGroupDir(string name)
        {
            return Path.Combine(_root, name);
        }

        /// <summary>
        /// 组配置读取——防御式（文件缺失按目录名构造并出声；坏 JSON 出声后按默认构造，不静默吞组）。
        /// </summary>
        /// <param name="dir">组目录</param>
        /// <param name="fallbackName">目录名（组名兜底）</param>
        /// <returns>组投影；无法构造 null</returns>
        private static GroupSummary ReadGroupFile(string dir, string fallbackName)
        {
            GroupSummary info = new GroupSummary();
            info.Name = fallbackName;
            info.Enabled = true;
            string path = Path.Combine(dir, "group.json");
            if (!File.Exists(path))
            {
                LogStore.Add("CatHome4", 2, "组配置缺失（按目录名建最小配置）: " + path, "GROUP");
                WriteGroupFile(info);
                return info;
            }
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(File.ReadAllText(path)))
                {
                    JsonElement root = doc.RootElement;
                    string name = GetStringProp(root, "name");
                    if (name.Length > 0)
                    {
                        info.Name = name;
                    }
                    info.MemberKeys = GetStringArrayProp(root, "memberKeys");
                    info.AdminKeys = GetStringArrayProp(root, "adminKeys");
                    info.SubscriberKeys = GetStringArrayProp(root, "subscriberKeys");
                    info.SubscribeCursors = GetLongMapProp(root, "subscribeCursors");
                    JsonElement idleEl;
                    if (root.TryGetProperty("subscribeIdleSec", out idleEl) && idleEl.ValueKind == JsonValueKind.Number)
                    {
                        info.SubscribeIdleSec = idleEl.GetInt32();
                    }
                    info.Project = GetStringProp(root, "project");
                    info.Layer = GetStringProp(root, "layer");
                    JsonElement createdEl;
                    if (root.TryGetProperty("createdAt", out createdEl) && createdEl.ValueKind == JsonValueKind.Number)
                    {
                        info.CreatedAt = createdEl.GetInt64();
                    }
                    JsonElement enabledEl;
                    if (root.TryGetProperty("enabled", out enabledEl))
                    {
                        if (enabledEl.ValueKind == JsonValueKind.False)
                        {
                            info.Enabled = false;
                        }
                    }
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 3, "组配置解析失败（按目录名建最小配置）: " + path + " | " + ex.Message, "GROUP");
            }
            return info;
        }

        /// <summary>
        /// 组配置写入——group.json 原子写（唯一出口）。
        /// </summary>
        /// <param name="info">组投影</param>
        private static void WriteGroupFile(GroupSummary info)
        {
            // [段1] 目录与内容装配
            string dir = GetGroupDir(info.Name);
            Directory.CreateDirectory(dir);
            Dictionary<string, object> map = new Dictionary<string, object>();
            map["name"] = info.Name;
            map["memberKeys"] = info.MemberKeys;
            map["adminKeys"] = info.AdminKeys;
            map["subscriberKeys"] = info.SubscriberKeys;
            map["subscribeIdleSec"] = info.SubscribeIdleSec;
            map["subscribeCursors"] = info.SubscribeCursors;
            map["project"] = info.Project;
            map["createdAt"] = info.CreatedAt;
            map["layer"] = info.Layer;
            map["enabled"] = info.Enabled;
            // [段2] 原子落盘
            ConfigStore.AtomicWrite(Path.Combine(dir, "group.json"), JsonUtil.Serialize(map));
        }

        /// <summary>
        /// 消息流统计扫描——读 messages.jsonl 取末序号 / 条数 / 最新时刻；坏行出声跳过（不静默吞）。
        /// </summary>
        /// <param name="dir">组目录</param>
        /// <param name="info">组投影（就地写统计字段）</param>
        private static void ScanMessages(string dir, GroupSummary info)
        {
            // [段1] 文件存在性
            string path = Path.Combine(dir, "messages.jsonl");
            if (!File.Exists(path))
            {
                return;
            }
            // [段2] 逐行扫描
            int badCount = 0;
            long badFirstLine = 0;
            try
            {
                int lineNo = 0;
                foreach (string line in File.ReadLines(path))
                {
                    lineNo = lineNo + 1;
                    if (line.Trim().Length == 0)
                    {
                        continue;
                    }
                    GroupMessage msg = ParseMessageLine(line);
                    if (msg == null)
                    {
                        badCount = badCount + 1;
                        if (badFirstLine == 0)
                        {
                            badFirstLine = lineNo;
                        }
                        continue;
                    }
                    info.MsgCount = info.MsgCount + 1;
                    if (msg.Seq > info.LastSeq)
                    {
                        info.LastSeq = msg.Seq;
                    }
                    if (msg.Ts > info.LastAt)
                    {
                        info.LastAt = msg.Ts;
                    }
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 3, "组消息流读取失败: " + path + " | " + ex.Message, "GROUP");
                return;
            }
            // [段3] 坏行出声
            if (badCount > 0)
            {
                LogStore.Add("CatHome4", 2, "组消息坏行跳过：" + path + " | 共 " + badCount.ToString() + " 行（首行 " + badFirstLine.ToString() + "）", "GROUP");
            }
        }

        /// <summary>
        /// 消息行解析——JSONL 单行解析；非法（非对象 / 缺 seq）返回 null（调用方出声计数）。
        /// </summary>
        /// <param name="line">单行 JSON 文本</param>
        /// <returns>消息；解析失败 null</returns>
        private static GroupMessage ParseMessageLine(string line)
        {
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(line))
                {
                    JsonElement root = doc.RootElement;
                    if (root.ValueKind != JsonValueKind.Object)
                    {
                        return null;
                    }
                    JsonElement seqEl;
                    if (!root.TryGetProperty("seq", out seqEl) || seqEl.ValueKind != JsonValueKind.Number)
                    {
                        return null;
                    }
                    GroupMessage msg = new GroupMessage();
                    msg.Seq = seqEl.GetInt64();
                    msg.From = GetStringProp(root, "from");
                    msg.To = GetStringProp(root, "to");
                    string type = GetStringProp(root, "type");
                    if (type.Length > 0)
                    {
                        msg.Type = type;
                    }
                    msg.Text = GetStringProp(root, "text");
                    JsonElement tsEl;
                    if (root.TryGetProperty("ts", out tsEl) && tsEl.ValueKind == JsonValueKind.Number)
                    {
                        msg.Ts = tsEl.GetInt64();
                    }
                    msg.Refs = GetStringProp(root, "refs");
                    return msg;
                }
            }
            catch (Exception)
            {
                return null;
            }
        }

        /// <summary>
        /// JSON 字符串属性读取——缺失 / 非字符串返回空串。
        /// </summary>
        /// <param name="root">对象元素</param>
        /// <param name="name">属性名</param>
        /// <returns>属性值（缺失空串）</returns>
        private static string GetStringProp(JsonElement root, string name)
        {
            JsonElement el;
            if (!root.TryGetProperty(name, out el) || el.ValueKind != JsonValueKind.String)
            {
                return "";
            }
            string got = el.GetString();
            if (got == null)
            {
                return "";
            }
            return got;
        }

        /// <summary>
        /// JSON 字符串数组属性读取——缺失 / 非数组返回空数组（项级非字符串与空串跳过）。
        /// </summary>
        /// <param name="root">对象元素</param>
        /// <param name="name">属性名</param>
        /// <returns>字符串数组</returns>
        private static string[] GetStringArrayProp(JsonElement root, string name)
        {
            JsonElement el;
            if (!root.TryGetProperty(name, out el) || el.ValueKind != JsonValueKind.Array)
            {
                return new string[0];
            }
            List<string> list = new List<string>();
            for (int i = 0; i < el.GetArrayLength(); i = i + 1)
            {
                JsonElement item = el[i];
                if (item.ValueKind != JsonValueKind.String)
                {
                    continue;
                }
                string got = item.GetString();
                if (got != null && got.Trim().Length > 0)
                {
                    list.Add(got.Trim());
                }
            }
            return list.ToArray();
        }
        /// <summary>
        /// JSON 键值映射读取——对象形态 {键: 数值}；缺失 / 非对象返回空表（项级非数值跳过）。
        /// </summary>
        /// <param name="root">对象元素</param>
        /// <param name="name">属性名</param>
        /// <returns>键值映射（键大小写不敏感）</returns>
        private static Dictionary<string, long> GetLongMapProp(JsonElement root, string name)
        {
            Dictionary<string, long> map = new Dictionary<string, long>(StringComparer.OrdinalIgnoreCase);
            JsonElement el;
            if (!root.TryGetProperty(name, out el) || el.ValueKind != JsonValueKind.Object)
            {
                return map;
            }
            foreach (JsonProperty prop in el.EnumerateObject())
            {
                if (prop.Value.ValueKind != JsonValueKind.Number)
                {
                    continue;
                }
                long value = 0;
                if (!prop.Value.TryGetInt64(out value))
                {
                    continue;
                }
                map[prop.Name] = value;
            }
            return map;
        }
        /// <summary>
        /// 消息流全量读取——逐行解析（坏行出声计数跳过），seq 升序。
        /// </summary>
        /// <param name="name">组名</param>
        /// <returns>消息列表（组不存在 / 无文件 = 空列表）</returns>
        private static List<GroupMessage> ReadAllMessages(string name)
        {
            List<GroupMessage> list = new List<GroupMessage>();
            string useName = name == null ? "" : name.Trim();
            lock (StoreLock)
            {
                GroupSummary info;
                if (!_groups.TryGetValue(useName, out info))
                {
                    return list;
                }
                string path = Path.Combine(GetGroupDir(info.Name), "messages.jsonl");
                if (!File.Exists(path))
                {
                    return list;
                }
                int badCount = 0;
                try
                {
                    foreach (string line in File.ReadLines(path))
                    {
                        if (line.Trim().Length == 0)
                        {
                            continue;
                        }
                        GroupMessage msg = ParseMessageLine(line);
                        if (msg == null)
                        {
                            badCount = badCount + 1;
                            continue;
                        }
                        list.Add(msg);
                    }
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 3, "组消息流读取失败: " + path + " | " + ex.Message, "GROUP");
                }
                if (badCount > 0)
                {
                    LogStore.Add("CatHome4", 2, "组消息坏行跳过：" + path + " | 共 " + badCount.ToString() + " 行", "GROUP");
                }
            }
            return list;
        }
        /// <summary>
        /// 读消息流尾部——取最后 limit 条（seq 升序）；hasMore = 更早还有未返回的条目。
        /// </summary>
        /// <param name="name">组名</param>
        /// <param name="limit">条数上限（≤0 取 100；上限 MaxReadLimit）</param>
        /// <param name="hasMore">更早还有未返回条目</param>
        /// <returns>消息列表</returns>
        public static List<GroupMessage> ReadTailMessages(string name, int limit, out bool hasMore)
        {
            hasMore = false;
            int useLimit = limit;
            if (useLimit <= 0)
            {
                useLimit = DefaultReadLimit;
            }
            if (useLimit > MaxReadLimit)
            {
                useLimit = MaxReadLimit;
            }
            List<GroupMessage> all = ReadAllMessages(name);
            List<GroupMessage> list = new List<GroupMessage>();
            if (all.Count == 0)
            {
                return list;
            }
            int start = all.Count - useLimit;
            if (start < 0)
            {
                start = 0;
            }
            if (start > 0)
            {
                hasMore = true;
            }
            for (int i = start; i < all.Count; i = i + 1)
            {
                list.Add(all[i]);
            }
            return list;
        }
        /// <summary>
        /// 订阅静默阈值（秒）——组配置字段；未设置（≤0）按默认 300 秒。
        /// </summary>
        /// <param name="info">组投影</param>
        /// <returns>阈值秒数</returns>
        public static int GetSubscribeIdleSec(GroupSummary info)
        {
            if (info == null || info.SubscribeIdleSec <= 0)
            {
                return DefaultSubscribeIdleSec;
            }
            return info.SubscribeIdleSec;
        }
        /// <summary>
        /// 订阅游标读取——未设置返回 0（从头）。
        /// </summary>
        /// <param name="name">组名</param>
        /// <param name="catKey">订阅猫 key</param>
        /// <returns>已推送到的 seq</returns>
        public static long GetSubscriberCursor(string name, string catKey)
        {
            string useName = name == null ? "" : name.Trim();
            string useCat = catKey == null ? "" : catKey.Trim();
            lock (StoreLock)
            {
                GroupSummary info;
                if (!_groups.TryGetValue(useName, out info))
                {
                    return 0;
                }
                long seq = 0;
                if (info.SubscribeCursors.TryGetValue(useCat, out seq))
                {
                    return seq;
                }
                return 0;
            }
        }
        /// <summary>
        /// 订阅游标推进——写入并落盘（低频操作，整文件原子写）。
        /// </summary>
        /// <param name="name">组名</param>
        /// <param name="catKey">订阅猫 key</param>
        /// <param name="seq">已推送到的 seq</param>
        /// <returns>ok| 前缀；失败 ERR| 前缀</returns>
        public static string SetSubscriberCursor(string name, string catKey, long seq)
        {
            string useName = name == null ? "" : name.Trim();
            string useCat = catKey == null ? "" : catKey.Trim();
            if (useCat.Length == 0)
            {
                return "ERR|GROUP_CURSOR_KEY_EMPTY|订阅游标缺少猫 key";
            }
            lock (StoreLock)
            {
                GroupSummary info;
                if (!_groups.TryGetValue(useName, out info))
                {
                    return "ERR|GROUP_NOT_FOUND|组不存在: " + useName;
                }
                info.SubscribeCursors[useCat] = seq;
                WriteGroupFile(info);
                return "ok|" + info.Name + "|" + useCat + "|" + seq.ToString();
            }
        }

        /// <summary>
        /// 组投影副本——缓存不直出（调用方改动不污染缓存）。
        /// </summary>
        /// <param name="src">源投影</param>
        /// <returns>副本</returns>
        private static GroupSummary CopySummary(GroupSummary src)
        {
            GroupSummary copy = new GroupSummary();
            copy.Name = src.Name;
            copy.MemberKeys = (string[])src.MemberKeys.Clone();
            copy.AdminKeys = (string[])src.AdminKeys.Clone();
            copy.SubscriberKeys = (string[])src.SubscriberKeys.Clone();
            copy.Project = src.Project;
            copy.CreatedAt = src.CreatedAt;
            copy.Layer = src.Layer;
            copy.Enabled = src.Enabled;
            copy.Running = src.Running;
            copy.LastSeq = src.LastSeq;
            copy.MsgCount = src.MsgCount;
            copy.LastAt = src.LastAt;
            copy.SubscribeIdleSec = src.SubscribeIdleSec;
            copy.SubscribeCursors = new Dictionary<string, long>(StringComparer.OrdinalIgnoreCase);
            foreach (KeyValuePair<string, long> kv in src.SubscribeCursors)
            {
                copy.SubscribeCursors[kv.Key] = kv.Value;
            }
            return copy;
        }

        /// <summary>
        /// 组排序比较——创建时刻升序（同刻按组名序）。
        /// </summary>
        /// <param name="a">组 A</param>
        /// <param name="b">组 B</param>
        /// <returns>比较结果</returns>
        private static int CompareByCreatedAt(GroupSummary a, GroupSummary b)
        {
            if (a.CreatedAt != b.CreatedAt)
            {
                if (a.CreatedAt < b.CreatedAt)
                {
                    return -1;
                }
                return 1;
            }
            return string.CompareOrdinal(a.Name, b.Name);
        }

        /// <summary>
        /// 组全量列表——创建时刻升序（配队页数据源）。
        /// </summary>
        /// <returns>组投影列表（副本）</returns>
        public static List<GroupSummary> ListGroups()
        {
            lock (StoreLock)
            {
                List<GroupSummary> list = new List<GroupSummary>();
                foreach (KeyValuePair<string, GroupSummary> kv in _groups)
                {
                    list.Add(CopySummary(kv.Value));
                }
                list.Sort(CompareByCreatedAt);
                return list;
            }
        }

        /// <summary>
        /// 单组查询。
        /// </summary>
        /// <param name="name">组名</param>
        /// <returns>组投影副本；不存在 null</returns>
        public static GroupSummary GetGroup(string name)
        {
            string useName = name == null ? "" : name.Trim();
            lock (StoreLock)
            {
                GroupSummary info;
                if (!_groups.TryGetValue(useName, out info))
                {
                    return null;
                }
                return CopySummary(info);
            }
        }

        /// <summary>
        /// 组名校验——字母 / 数字 / 中文 / 连字符 / 下划线，1..32 字符；禁路径分隔与空白（容器 Key 空间约束）。
        /// </summary>
        /// <param name="name">组名候选</param>
        /// <returns>合法 true</returns>
        public static bool IsValidName(string name)
        {
            if (name == null || name.Length == 0 || name.Length > MaxNameLength)
            {
                return false;
            }
            for (int i = 0; i < name.Length; i = i + 1)
            {
                char c = name[i];
                if (char.IsLetterOrDigit(c))
                {
                    continue;
                }
                if (c == '-' || c == '_')
                {
                    continue;
                }
                return false;
            }
            return true;
        }
        /// <summary>
        /// 名单收敛——只保留同时出现在成员名单里的项（管理员 / 订阅者恒为成员子集）。
        /// </summary>
        /// <param name="wanted">候选名单（可 null）</param>
        /// <param name="members">组成员名单</param>
        /// <returns>收敛后数组（保持入参序）</returns>
        private static string[] KeepMembers(string[] wanted, string[] members)
        {
            List<string> kept = new List<string>();
            string[] want = NormalizeMembers(wanted);
            for (int i = 0; i < want.Length; i = i + 1)
            {
                for (int k = 0; k < members.Length; k = k + 1)
                {
                    if (string.Equals(want[i], members[k], StringComparison.Ordinal))
                    {
                        kept.Add(want[i]);
                        break;
                    }
                }
            }
            return kept.ToArray();
        }
        /// <summary>
        /// 订阅游标收敛——只保留当前订阅者的键；新订阅者从 seedSeq 起（只收之后的消息，不补历史）。
        /// </summary>
        /// <param name="current">原游标表（可 null）</param>
        /// <param name="subscribers">收敛后的订阅名单</param>
        /// <param name="seedSeq">新订阅者游标起点（当前末序号）</param>
        /// <returns>新游标表（键大小写不敏感）</returns>
        private static Dictionary<string, long> ReseedCursors(Dictionary<string, long> current, string[] subscribers, long seedSeq)
        {
            Dictionary<string, long> next = new Dictionary<string, long>(StringComparer.OrdinalIgnoreCase);
            if (subscribers == null)
            {
                return next;
            }
            for (int i = 0; i < subscribers.Length; i = i + 1)
            {
                string key = subscribers[i];
                if (key == null || key.Trim().Length == 0)
                {
                    continue;
                }
                long cursor = seedSeq;
                if (current != null)
                {
                    long existing = 0;
                    if (current.TryGetValue(key, out existing))
                    {
                        cursor = existing;
                    }
                }
                next[key] = cursor;
            }
            return next;
        }

        /// <summary>
        /// 成员清洗——去空白 / 去空项 / 去重（保持入参序）。
        /// </summary>
        /// <param name="members">成员 key 数组（可 null）</param>
        /// <returns>清洗后数组</returns>
        private static string[] NormalizeMembers(string[] members)
        {
            List<string> list = new List<string>();
            if (members == null)
            {
                return list.ToArray();
            }
            for (int i = 0; i < members.Length; i = i + 1)
            {
                if (members[i] == null)
                {
                    continue;
                }
                string key = members[i].Trim();
                if (key.Length == 0)
                {
                    continue;
                }
                bool exists = false;
                for (int k = 0; k < list.Count; k = k + 1)
                {
                    if (string.Equals(list[k], key, StringComparison.Ordinal))
                    {
                        exists = true;
                        break;
                    }
                }
                if (!exists)
                {
                    list.Add(key);
                }
            }
            return list.ToArray();
        }

        /// <summary>
        /// 新建 / 改组——同名覆盖成员与项目（创建时刻与启用态保留）；返回 ok 或 ERR| 前缀文本。
        /// </summary>
        /// <param name="name">组名</param>
        /// <param name="memberKeys">成员猫 key 数组</param>
        /// <param name="project">项目绑定（可空）</param>
        /// <param name="adminKeys">管理员猫 key 数组（null = 保留原值；非成员项自动剔除）</param>
        /// <param name="subscriberKeys">订阅自动转发的猫 key 数组（null = 保留原值；非成员项自动剔除）</param>
        /// <param name="subscribeIdleSec">订阅静默阈值秒（-1 = 保留原值；0 = 按默认 300 秒）</param>
        /// <returns>结果文本</returns>
        public static string CreateOrUpdate(string name, string[] memberKeys, string project, string[] adminKeys = null, string[] subscriberKeys = null, int subscribeIdleSec = -1)
        {
            string useName = name == null ? "" : name.Trim();
            if (!IsValidName(useName))
            {
                return "ERR|GROUP_NAME_INVALID|组名非法（字母/数字/中文/-/_，1.." + MaxNameLength.ToString() + " 字符）: " + useName;
            }
            string[] members = NormalizeMembers(memberKeys);
            if (members.Length == 0)
            {
                return "ERR|GROUP_MEMBERS_EMPTY|组至少需要一名成员";
            }
            lock (StoreLock)
            {
                GroupSummary info;
                bool found = _groups.TryGetValue(useName, out info);
                if (!found)
                {
                    info = new GroupSummary();
                    info.Name = useName;
                    info.CreatedAt = Now();
                    info.Enabled = true;
                    info.Running = true;
                    _groups[useName] = info;
                }
                info.MemberKeys = members;
                info.Project = project == null ? "" : project.Trim();
                // 管理员 / 订阅者恒为成员子集——未显式给出时按原名单随成员变更收敛（不静默保留非成员）
                string[] wantAdmins = adminKeys == null ? info.AdminKeys : adminKeys;
                string[] wantSubs = subscriberKeys == null ? info.SubscriberKeys : subscriberKeys;
                info.AdminKeys = KeepMembers(wantAdmins, members);
                info.SubscriberKeys = KeepMembers(wantSubs, members);
                if (subscribeIdleSec >= 0)
                {
                    info.SubscribeIdleSec = subscribeIdleSec;
                }
                // 游标收敛——取消订阅即删游标；新订阅者从当前末序号起（只收之后的消息，不补历史）
                info.SubscribeCursors = ReseedCursors(info.SubscribeCursors, info.SubscriberKeys, info.LastSeq);
                WriteGroupFile(info);
                string verb = "组新建: ";
                if (found)
                {
                    verb = "组更新: ";
                }
                LogStore.Add("CatHome4", 1, verb + useName + " | 成员 " + members.Length.ToString() + " 名 | 管理员 " + info.AdminKeys.Length.ToString() + " 名 | 订阅 " + info.SubscriberKeys.Length.ToString() + " 名", "GROUP");
                return "ok|" + useName;
            }
        }

        /// <summary>
        /// 组启用 / 停用——落 group.json 的启用意图；运行态由 StartGroup / StopGroup 管理。
        /// </summary>
        /// <param name="name">组名</param>
        /// <param name="enabled">true 启用 / false 停用</param>
        /// <returns>结果文本</returns>
        public static string SetEnabled(string name, bool enabled)
        {
            string useName = name == null ? "" : name.Trim();
            lock (StoreLock)
            {
                GroupSummary info;
                if (!_groups.TryGetValue(useName, out info))
                {
                    return "ERR|GROUP_NOT_FOUND|组不存在: " + useName;
                }
                info.Enabled = enabled;
                WriteGroupFile(info);
                string verb = "组停用: ";
                if (enabled)
                {
                    verb = "组启用: ";
                }
                LogStore.Add("CatHome4", 1, verb + info.Name, "GROUP");
                return "ok|" + info.Name;
            }
        }

        /// <summary>
        /// 组启用——进入运行态：扫描消息流统计并置 Running；重复启用幂等。
        /// </summary>
        /// <param name="name">组名</param>
        /// <returns>结果文本</returns>
        public static string StartGroup(string name)
        {
            string useName = name == null ? "" : name.Trim();
            lock (StoreLock)
            {
                GroupSummary info;
                if (!_groups.TryGetValue(useName, out info))
                {
                    return "ERR|GROUP_NOT_FOUND|组不存在: " + useName;
                }
                if (info.Running)
                {
                    return "ok|" + info.Name;
                }
                info.LastSeq = 0;
                info.MsgCount = 0;
                info.LastAt = 0;
                ScanMessages(GetGroupDir(info.Name), info);
                info.Running = true;
                LogStore.Add("CatHome4", 1, "组启动: " + info.Name + " | 消息 " + info.MsgCount.ToString() + " 条", "GROUP");
                return "ok|" + info.Name;
            }
        }

        /// <summary>
        /// 组停用——退出运行态：统计归零（不删配置、不动磁盘）；重复停用幂等。
        /// </summary>
        /// <param name="name">组名</param>
        /// <returns>结果文本</returns>
        public static string StopGroup(string name)
        {
            string useName = name == null ? "" : name.Trim();
            lock (StoreLock)
            {
                GroupSummary info;
                if (!_groups.TryGetValue(useName, out info))
                {
                    return "ERR|GROUP_NOT_FOUND|组不存在: " + useName;
                }
                if (!info.Running)
                {
                    return "ok|" + info.Name;
                }
                info.Running = false;
                info.LastSeq = 0;
                info.MsgCount = 0;
                info.LastAt = 0;
                LogStore.Add("CatHome4", 1, "组停止: " + info.Name, "GROUP");
                return "ok|" + info.Name;
            }
        }

        /// <summary>
        /// 运行态查询——工具面门禁用（停用组一律拒绝）。
        /// </summary>
        /// <param name="name">组名</param>
        /// <returns>true = 运行中</returns>
        public static bool IsRunning(string name)
        {
            string useName = name == null ? "" : name.Trim();
            lock (StoreLock)
            {
                GroupSummary info;
                if (!_groups.TryGetValue(useName, out info))
                {
                    return false;
                }
                return info.Running;
            }
        }

        /// <summary>
        /// 删组——整目录移入 Data/group/_trash/&lt;组名&gt;_&lt;时刻&gt;/（可恢复），缓存移除。
        /// </summary>
        /// <param name="name">组名</param>
        /// <returns>结果文本</returns>
        public static string Delete(string name)
        {
            string useName = name == null ? "" : name.Trim();
            lock (StoreLock)
            {
                GroupSummary info;
                if (!_groups.TryGetValue(useName, out info))
                {
                    return "ERR|GROUP_NOT_FOUND|组不存在: " + useName;
                }
                string dir = GetGroupDir(info.Name);
                string trashDir = Path.Combine(_root, "_trash");
                string target = Path.Combine(trashDir, info.Name + "_" + Now().ToString());
                try
                {
                    Directory.CreateDirectory(trashDir);
                    if (Directory.Exists(dir))
                    {
                        Directory.Move(dir, target);
                    }
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 3, "组删除失败: " + info.Name + " | " + ex.Message, "GROUP");
                    return "ERR|GROUP_DELETE_FAIL|删组失败: " + ex.Message;
                }
                _groups.Remove(useName);
                LogStore.Add("CatHome4", 1, "组删除: " + info.Name + " → " + target, "GROUP");
                return "ok|" + info.Name;
            }
        }
        /// <summary>
        /// 清空消息流——原地归档 messages.jsonl（改名 .bak）后重建空流；统计与订阅游标归零（新流从 #1 起）。
        /// </summary>
        /// <param name="name">组名</param>
        /// <returns>ok|组名|归档文件名（无消息文件时末段为空）；失败 ERR| 前缀</returns>
        public static string ClearMessages(string name)
        {
            string useName = name == null ? "" : name.Trim();
            lock (StoreLock)
            {
                GroupSummary info;
                if (!_groups.TryGetValue(useName, out info))
                {
                    return "ERR|GROUP_NOT_FOUND|组不存在: " + useName;
                }
                string dir = GetGroupDir(info.Name);
                string path = Path.Combine(dir, "messages.jsonl");
                string archived = "";
                if (File.Exists(path))
                {
                    // [段1] 归档——同秒重复清空按序号避让（不覆盖旧归档）
                    string stamp = DateTimeOffset.UtcNow.ToLocalTime().ToString("yyyyMMdd_HHmmss");
                    string target = Path.Combine(dir, "messages." + stamp + ".jsonl.bak");
                    int suffix = 1;
                    while (File.Exists(target))
                    {
                        target = Path.Combine(dir, "messages." + stamp + "_" + suffix.ToString() + ".jsonl.bak");
                        suffix = suffix + 1;
                    }
                    try
                    {
                        File.Move(path, target);
                        archived = Path.GetFileName(target);
                    }
                    catch (Exception ex)
                    {
                        LogStore.Add("CatHome4", 3, "组消息归档失败: " + path + " | " + ex.Message, "GROUP");
                        return "ERR|GROUP_WRITE_FAIL|消息归档失败: " + ex.Message;
                    }
                }
                // [段2] 统计与游标归零——新流 seq 从 1 起；游标留在高位会让订阅永远推不出去
                info.LastSeq = 0;
                info.MsgCount = 0;
                info.LastAt = 0;
                info.SubscribeCursors = new Dictionary<string, long>(StringComparer.OrdinalIgnoreCase);
                WriteGroupFile(info);
                LogStore.Add("CatHome4", 1, "组消息清空: " + info.Name + " | 归档 " + (archived.Length == 0 ? "（无消息文件）" : archived), "GROUP");
                return "ok|" + info.Name + "|" + archived;
            }
        }

        /// <summary>
        /// 追加消息——组内 seq 单调递增（末序号 + 1），一行一条追加写 messages.jsonl。
        /// </summary>
        /// <param name="name">组名</param>
        /// <param name="from">发送方猫 key（空 = 系统 / 管理员）</param>
        /// <param name="type">消息类型（delegation / receipt / note；空 = note）</param>
        /// <param name="text">正文（必填）</param>
        /// <param name="refs">关联锚（可空）</param>
        /// <param name="to">@ 目标猫 key（空 = 不指向；非空须为组内成员且非自己）</param>
        /// <returns>结果文本（ok|组名|seq=N）</returns>
        public static string AppendMessage(string name, string from, string type, string text, string refs, string to = "")
        {
            string useName = name == null ? "" : name.Trim();
            string useType = type == null ? "" : type.Trim();
            string useTo = to == null ? "" : to.Trim();
            if (useType.Length == 0)
            {
                useType = "note";
            }
            if (!string.Equals(useType, "delegation", StringComparison.Ordinal)
                && !string.Equals(useType, "receipt", StringComparison.Ordinal)
                && !string.Equals(useType, "note", StringComparison.Ordinal))
            {
                return "ERR|GROUP_TYPE_INVALID|消息类型非法（delegation|receipt|note）: " + useType;
            }
            string useText = text == null ? "" : text.Trim();
            if (useText.Length == 0)
            {
                return "ERR|GROUP_TEXT_EMPTY|消息正文为空";
            }
            string nameOut = "";
            long seqOut = 0;
            lock (StoreLock)
            {
                GroupSummary info;
                if (!_groups.TryGetValue(useName, out info))
                {
                    return "ERR|GROUP_NOT_FOUND|组不存在: " + useName;
                }
                if (!info.Running)
                {
                    return "ERR|GROUP_NOT_RUNNING|组未启用: " + useName;
                }
                // [段1.4] 发送方校验——非空须为本组成员（网页恒空 = 人；防冒充非成员署名 / 订阅误触）
                string fromMemberKey = from == null ? "" : from.Trim();
                if (fromMemberKey.Length > 0)
                {
                    bool fromIsMember = false;
                    for (int i = 0; i < info.MemberKeys.Length; i = i + 1)
                    {
                        if (string.Equals(info.MemberKeys[i], fromMemberKey, StringComparison.Ordinal))
                        {
                            fromIsMember = true;
                            break;
                        }
                    }
                    if (!fromIsMember)
                    {
                        return "ERR|GROUP_FROM_NOT_MEMBER|发送方不在组内: " + fromMemberKey;
                    }
                }
                string dir = GetGroupDir(info.Name);
                // [段1.5] @ 目标校验——组内成员才可指向；自 @ 拒绝（自激一轮且无业务意义）
                if (useTo.Length > 0)
                {
                    bool isMember = false;
                    for (int i = 0; i < info.MemberKeys.Length; i = i + 1)
                    {
                        if (string.Equals(info.MemberKeys[i], useTo, StringComparison.Ordinal))
                        {
                            isMember = true;
                            break;
                        }
                    }
                    if (!isMember)
                    {
                        return "ERR|GROUP_TARGET_NOT_MEMBER|@ 目标不在组内: " + useTo;
                    }
                    string fromKey = from == null ? "" : from.Trim();
                    if (fromKey.Length > 0 && string.Equals(useTo, fromKey, StringComparison.Ordinal))
                    {
                        return "ERR|GROUP_TARGET_SELF|不能 @ 自己: " + useTo;
                    }
                }
                long nextSeq = info.LastSeq + 1;
                long now = Now();
                string line = JsonUtil.Object(
                    ("seq", nextSeq),
                    ("from", from == null ? "" : from.Trim()),
                    ("to", useTo),
                    ("type", useType),
                    ("text", useText),
                    ("ts", now),
                    ("refs", refs == null ? "" : refs.Trim()));
                try
                {
                    Directory.CreateDirectory(dir);
                    File.AppendAllText(Path.Combine(dir, "messages.jsonl"), line + "\n", new UTF8Encoding(false));
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 3, "组消息追加失败: " + info.Name + " | " + ex.Message, "GROUP");
                    return "ERR|GROUP_WRITE_FAIL|消息写入失败: " + ex.Message;
                }
                info.LastSeq = nextSeq;
                info.MsgCount = info.MsgCount + 1;
                info.LastAt = now;
                nameOut = info.Name;
                seqOut = nextSeq;
            }
            return "ok|" + nameOut + "|seq=" + seqOut.ToString();
        }

        /// <summary>
        /// 读消息流——按 seq 升序返回 afterSeq 之后的消息（afterSeq ≤ 0 = 从头）。
        /// </summary>
        /// <param name="name">组名</param>
        /// <param name="afterSeq">起始序号（不含；≤0 从头）</param>
        /// <param name="limit">条数上限（≤0 取 100；上限 MaxReadLimit）</param>
        /// <returns>消息列表（组不存在 / 无消息 = 空列表）</returns>
        public static List<GroupMessage> ReadMessages(string name, long afterSeq, int limit)
        {
            string useName = name == null ? "" : name.Trim();
            int useLimit = limit;
            if (useLimit <= 0)
            {
                useLimit = 100;
            }
            if (useLimit > MaxReadLimit)
            {
                useLimit = MaxReadLimit;
            }
            List<GroupMessage> list = new List<GroupMessage>();
            lock (StoreLock)
            {
                GroupSummary info;
                if (!_groups.TryGetValue(useName, out info))
                {
                    return list;
                }
                string path = Path.Combine(GetGroupDir(info.Name), "messages.jsonl");
                if (!File.Exists(path))
                {
                    return list;
                }
                try
                {
                    foreach (string line in File.ReadLines(path))
                    {
                        if (line.Trim().Length == 0)
                        {
                            continue;
                        }
                        GroupMessage msg = ParseMessageLine(line);
                        if (msg == null)
                        {
                            continue;
                        }
                        if (msg.Seq <= afterSeq)
                        {
                            continue;
                        }
                        list.Add(msg);
                        if (list.Count >= useLimit)
                        {
                            break;
                        }
                    }
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 3, "组消息读取失败: " + path + " | " + ex.Message, "GROUP");
                }
            }
            return list;
        }

        /// <summary>
        /// 当前时刻——Unix 毫秒（UTC）。
        /// </summary>
        /// <returns>Unix 毫秒</returns>
        public static long Now()
        {
            return DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
        }
    }
}
