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

        /// <summary>层级——预留字段（P6 条件唤醒）</summary>
        public string Layer = "";

        /// <summary>启用标记——false = 组停用（不参与派单与巡检）</summary>
        public bool Enabled = true;

        /// <summary>组内最大消息序号——0 = 无消息</summary>
        public long LastSeq;

        /// <summary>消息条数</summary>
        public int MsgCount;

        /// <summary>成员已读游标——catKey → lastReadSeq（members.json；未读 = LastSeq − 游标）</summary>
        public Dictionary<string, long> MemberReads = new Dictionary<string, long>();

        /// <summary>最新消息时刻——Unix 毫秒（0 = 无消息）</summary>
        public long LastAt;
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
    /// 组数据面（A300）——Data/group/&lt;组名&gt;/{group.json, messages.jsonl, members.json}。
    /// 语义：组配置与消息流是 Harness 的核心数据面；落盘范式照 delays.json（变更即原子写 + 重启续接）。
    /// 线程模型：全部操作锁内完成（HTTP 线程 / 主线程 / 后续轻宿主）。
    /// </summary>
    public static class GroupStore
    {
        /// <summary>组名长度上限</summary>
        public const int MaxNameLength = 32;

        /// <summary>单次读消息条数上限</summary>
        public const int MaxReadLimit = 500;

        /// <summary>组根目录——Data/group（空 = 仅内存，不落盘）</summary>
        private static string _root = "";

        /// <summary>组缓存——组名 → 配置 + 统计（大小写不敏感，避免同组两目录）</summary>
        private static Dictionary<string, GroupSummary> _groups = new Dictionary<string, GroupSummary>(StringComparer.OrdinalIgnoreCase);

        /// <summary>存储锁——全部读写锁内完成</summary>
        private static readonly object StoreLock = new object();
        /// <summary>
        /// 消息追加钩子——(组名, 消息)；由组合根接线做「订阅自动转发」，Core 不依赖会话面。
        /// </summary>
        public static Action<string, GroupMessage> MessageAppended;

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
                    ScanMessages(dirs[i], info);
                    ReadMembersFile(dirs[i], info);
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
            copy.LastSeq = src.LastSeq;
            copy.MsgCount = src.MsgCount;
            copy.LastAt = src.LastAt;
            copy.MemberReads = new Dictionary<string, long>();
            foreach (KeyValuePair<string, long> kv in src.MemberReads)
            {
                copy.MemberReads[kv.Key] = kv.Value;
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
        /// <returns>结果文本</returns>
        public static string CreateOrUpdate(string name, string[] memberKeys, string project, string[] adminKeys = null, string[] subscriberKeys = null)
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
                    _groups[useName] = info;
                }
                info.MemberKeys = members;
                info.Project = project == null ? "" : project.Trim();
                // 管理员 / 订阅者恒为成员子集——未显式给出时按原名单随成员变更收敛（不静默保留非成员）
                string[] wantAdmins = adminKeys == null ? info.AdminKeys : adminKeys;
                string[] wantSubs = subscriberKeys == null ? info.SubscriberKeys : subscriberKeys;
                info.AdminKeys = KeepMembers(wantAdmins, members);
                info.SubscriberKeys = KeepMembers(wantSubs, members);
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
        /// 组启用 / 停用——落 group.json；停用组不参与派单与巡检（P5 消费）。
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
            GroupMessage appended = null;
            lock (StoreLock)
            {
                GroupSummary info;
                if (!_groups.TryGetValue(useName, out info))
                {
                    return "ERR|GROUP_NOT_FOUND|组不存在: " + useName;
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
                appended = new GroupMessage();
                appended.Seq = nextSeq;
                appended.From = from == null ? "" : from.Trim();
                appended.To = useTo;
                appended.Type = useType;
                appended.Text = useText;
                appended.Ts = now;
                appended.Refs = refs == null ? "" : refs.Trim();
            }
            // [段2] 订阅自动转发钩子——锁外触发（处理器碰会话面，不持存储锁回调；异常不吞写入结果）
            Action<string, GroupMessage> hook = MessageAppended;
            if (hook != null && appended != null)
            {
                try
                {
                    hook(nameOut, appended);
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 3, "组消息转发钩子异常: " + nameOut + " | " + ex.Message, "GROUP");
                }
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
        /// 已读游标读取——members.json（缺失 = 空表；坏 JSON 出声后按空表继续，不阻断组加载）。
        /// </summary>
        /// <param name="dir">组目录</param>
        /// <param name="info">组投影（就地填 MemberReads）</param>
        private static void ReadMembersFile(string dir, GroupSummary info)
        {
            string path = Path.Combine(dir, "members.json");
            if (!File.Exists(path))
            {
                return;
            }
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(File.ReadAllText(path)))
                {
                    if (doc.RootElement.ValueKind != JsonValueKind.Object)
                    {
                        return;
                    }
                    foreach (JsonProperty prop in doc.RootElement.EnumerateObject())
                    {
                        if (prop.Value.ValueKind != JsonValueKind.Number)
                        {
                            continue;
                        }
                        long seq;
                        if (prop.Value.TryGetInt64(out seq))
                        {
                            info.MemberReads[prop.Name] = seq;
                        }
                    }
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "组已读游标解析失败（按空表继续）: " + path + " | " + ex.Message, "GROUP");
            }
        }

        /// <summary>
        /// 已读游标写入——members.json 原子写（唯一出口）。
        /// </summary>
        /// <param name="info">组投影</param>
        private static void WriteMembersFile(GroupSummary info)
        {
            string dir = GetGroupDir(info.Name);
            Directory.CreateDirectory(dir);
            Dictionary<string, object> map = new Dictionary<string, object>();
            foreach (KeyValuePair<string, long> kv in info.MemberReads)
            {
                map[kv.Key] = kv.Value;
            }
            ConfigStore.AtomicWrite(Path.Combine(dir, "members.json"), JsonUtil.Serialize(map));
        }

        /// <summary>
        /// 已读推进——只允许前进（seq ≤ 旧游标时幂等返回）；落盘 members.json。
        /// 唯一诚实入口 = 猫真实拉取消息后主动上报（禁止打开即推进 / 投递即读）。
        /// </summary>
        /// <param name="name">组名</param>
        /// <param name="catKey">猫 key</param>
        /// <param name="seq">已读到的序号</param>
        /// <returns>结果文本（ok|组名|read=N）</returns>
        public static string MarkRead(string name, string catKey, long seq)
        {
            string useName = name == null ? "" : name.Trim();
            string useCat = catKey == null ? "" : catKey.Trim();
            if (useCat.Length == 0)
            {
                return "ERR|GROUP_CAT_EMPTY|猫 key 为空";
            }
            if (seq < 0)
            {
                return "ERR|GROUP_SEQ_INVALID|已读序号非法: " + seq.ToString();
            }
            lock (StoreLock)
            {
                GroupSummary info;
                if (!_groups.TryGetValue(useName, out info))
                {
                    return "ERR|GROUP_NOT_FOUND|组不存在: " + useName;
                }
                long current = 0;
                if (info.MemberReads.TryGetValue(useCat, out current))
                {
                    if (seq <= current)
                    {
                        return "ok|" + info.Name + "|read=" + current.ToString();
                    }
                }
                if (seq > info.LastSeq)
                {
                    seq = info.LastSeq;
                }
                info.MemberReads[useCat] = seq;
                WriteMembersFile(info);
                return "ok|" + info.Name + "|read=" + seq.ToString();
            }
        }

        /// <summary>
        /// 未读数——组内最大序号 − 该猫游标（无游标 = 全部未读）。
        /// </summary>
        /// <param name="name">组名</param>
        /// <param name="catKey">猫 key</param>
        /// <returns>未读条数（组不存在 = 0）</returns>
        public static long GetUnread(string name, string catKey)
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
                long read = 0;
                if (useCat.Length > 0)
                {
                    info.MemberReads.TryGetValue(useCat, out read);
                }
                long unread = info.LastSeq - read;
                if (unread < 0)
                {
                    unread = 0;
                }
                return unread;
            }
        }

        /// <summary>
        /// 当前时刻——Unix 毫秒（UTC）。
        /// </summary>
        /// <returns>Unix 毫秒</returns>
        private static long Now()
        {
            return DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
        }
    }
}
