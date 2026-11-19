using System;
using System.Collections.Generic;
using System.Text.Json;
using CH4;
using Mau.Runtime;

namespace CatHome4.Admin
{
    /// <summary>
    /// 群组服务实现（IGroupService）——Group 工具面五件：group-info / group-inbox / group-post / group-delegate / group-newsession。
    /// 权限：调用者须为组成员（Info / Inbox / Post）；Delegate 与 NewSession 另须组管理员（group.json adminKeys）。
    /// 命名（2026-11-05 莎裁 β）：旧名 group-say / group-send 表达的是实现动作而非意图，且 say 的 to 参数是纯展示标记、
    /// 与 send 的投递目标同名不同义——改为 post（只写组流，无 @ 目标参数）/ delegate（派活并唤醒目标猫）。
    /// 内部方法名 Say / Send 保留（接口面不变），工具名映射见各方法注释。
    /// 投递：Send 在同一方法内顺序完成「写组消息 → 以 origin 投递目标猫」——不拆两步（避免单有记录或单有投递）。
    /// 返回体：统一口径 A214——首行元数据头（ok / tool / target / items + 专有字段）+ 摘要行 + 正文行；失败走 ERR| 前缀。
    /// 线程模型：主线程同步（工具组 Flow 认领线调用；投递触碰会话面）。
    /// </summary>
    internal sealed class GroupService : IGroupService
    {
        /// <summary>单次拉取默认条数</summary>
        private const int DefaultLimit = 100;

        /// <summary>组信息——组行 JSON（成员 / 管理员带显示名 + 统计）。</summary>
        /// <param name="argsJson">工具参数 JSON（group 必填；catId 为宿主注入保留键）</param>
        /// <returns>A214 回执（头行 + 摘要行 + 正文行）；失败为 ERR| 前缀</returns>
        public string Info(string argsJson)
        {
            string group = "";
            string catId = "";
            string err = ReadCommon(argsJson, out group, out catId);
            if (err.Length > 0)
            {
                return err;
            }
            GroupSummary g = GroupStore.GetGroup(group);
            if (g == null)
            {
                return "ERR|GROUP_NOT_FOUND|组不存在: " + group;
            }
            if (!IsMember(g, catId))
            {
                return "ERR|GROUP_NOT_MEMBER|调用者不是本组成员: " + catId;
            }
            if (!g.Running)
            {
                return "ERR|GROUP_NOT_RUNNING|组未启用: " + group;
            }
            // [段1] 成员行——显示名 + key + 角色标记 + 工作状态（A325）
            List<string> lines = new List<string>();
            for (int i = 0; i < g.MemberKeys.Length; i = i + 1)
            {
                string key = g.MemberKeys[i];
                string line = "· " + CatHostService.ResolveCatDisplayName(key) + "（" + key + "）";
                List<string> roles = new List<string>();
                if (IsAdmin(g, key))
                {
                    roles.Add("管理员");
                }
                if (IsSubscriber(g, key))
                {
                    roles.Add("订阅");
                }
                if (roles.Count > 0)
                {
                    line = line + " — " + string.Join(" / ", roles.ToArray());
                }
                line = line + " · " + MemberWorkState(key);
                lines.Add(line);
            }
            if (lines.Count == 0)
            {
                lines.Add("（无成员）");
            }
            // [段2] 专有字段 + 摘要行
            Dictionary<string, object> extras = new Dictionary<string, object>();
            extras["project"] = g.Project;
            extras["enabled"] = g.Enabled;
            extras["msgCount"] = g.MsgCount;
            extras["lastSeq"] = g.LastSeq;
            extras["lastAt"] = g.LastAt;
            string summary = g.Name + " | " + g.MemberKeys.Length.ToString() + " 名成员 · 消息 " + g.MsgCount.ToString() + " 条 · 最新 #" + g.LastSeq.ToString();
            return BuildReply("group-info", g.Name, g.MemberKeys.Length, extras, summary, lines);
        }

        /// <summary>拉取消息——按 after / limit 取组内消息流（seq 升序）。</summary>
        /// <param name="argsJson">工具参数 JSON（group 必填；after / limit 可选）</param>
        /// <returns>A214 回执（头行 + 摘要行 + 正文行）；失败为 ERR| 前缀</returns>
        public string Inbox(string argsJson)
        {
            string group = "";
            string catId = "";
            string err = ReadCommon(argsJson, out group, out catId);
            if (err.Length > 0)
            {
                return err;
            }
            long after = 0;
            int limit = DefaultLimit;
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(argsJson))
                {
                    JsonElement root = doc.RootElement;
                    JsonElement el;
                    if (root.TryGetProperty("after", out el) && el.ValueKind == JsonValueKind.Number)
                    {
                        after = el.GetInt64();
                    }
                    if (root.TryGetProperty("limit", out el) && el.ValueKind == JsonValueKind.Number)
                    {
                        limit = el.GetInt32();
                    }
                }
            }
            catch (Exception ex)
            {
                return "ERR|BAD_ARGS|参数 JSON 解析失败: " + ex.Message;
            }
            GroupSummary g = GroupStore.GetGroup(group);
            if (g == null)
            {
                return "ERR|GROUP_NOT_FOUND|组不存在: " + group;
            }
            if (!IsMember(g, catId))
            {
                return "ERR|GROUP_NOT_MEMBER|调用者不是本组成员: " + catId;
            }
            if (!g.Running)
            {
                return "ERR|GROUP_NOT_RUNNING|组未启用: " + group;
            }
            List<GroupMessage> messages = GroupStore.ReadMessages(g.Name, after, limit);
            // [段1] 消息行——#seq [类型] 发方 → 收方 · 正文（单行化，不截断）
            List<string> lines = new List<string>();
            for (int i = 0; i < messages.Count; i = i + 1)
            {
                GroupMessage m = messages[i];
                string from = "";
                if (m.From.Length > 0)
                {
                    from = CatHostService.ResolveCatDisplayName(m.From);
                }
                string to = "";
                if (m.To.Length > 0)
                {
                    to = CatHostService.ResolveCatDisplayName(m.To);
                }
                string route = from;
                if (to.Length > 0)
                {
                    route = from + " → " + to;
                }
                string line = "#" + m.Seq.ToString() + " [" + LabelOf(m.Type) + "] " + route + " · " + Flatten(m.Text);
                if (m.Refs.Length > 0)
                {
                    line = line + "（关联 " + m.Refs + "）";
                }
                lines.Add(line);
            }
            if (lines.Count == 0)
            {
                lines.Add("（无消息）");
            }
            // [段2] 专有字段 + 摘要行
            Dictionary<string, object> extras = new Dictionary<string, object>();
            extras["lastSeq"] = g.LastSeq;
            string summary = g.Name + " | " + messages.Count.ToString() + " 条 · 最新 #" + g.LastSeq.ToString();
            return BuildReply("group-inbox", g.Name, messages.Count, extras, summary, lines);
        }

        /// <summary>发讨论消息（工具名 group-post）——只写组消息流，不投递、不唤醒；无 @ 目标参数（2026-11-05 删 to）。</summary>
        /// <param name="argsJson">工具参数 JSON（group / text 必填）</param>
        /// <returns>A214 回执（头行 + 摘要行 + 正文行）；失败为 ERR| 前缀</returns>
        public string Say(string argsJson)
        {
            string group = "";
            string catId = "";
            string err = ReadCommon(argsJson, out group, out catId);
            if (err.Length > 0)
            {
                return err;
            }
            string text = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(argsJson))
                {
                    JsonElement root = doc.RootElement;
                    text = JsonString(root, "text");
                }
            }
            catch (Exception ex)
            {
                return "ERR|BAD_ARGS|参数 JSON 解析失败: " + ex.Message;
            }
            GroupSummary g = GroupStore.GetGroup(group);
            if (g == null)
            {
                return "ERR|GROUP_NOT_FOUND|组不存在: " + group;
            }
            if (!IsMember(g, catId))
            {
                return "ERR|GROUP_NOT_MEMBER|调用者不是本组成员: " + catId;
            }
            if (!g.Running)
            {
                return "ERR|GROUP_NOT_RUNNING|组未启用: " + group;
            }
            string result = GroupStore.AppendMessage(g.Name, catId, "note", text, "", "");
            if (!result.StartsWith("ok|", StringComparison.Ordinal))
            {
                return result;
            }
            long seq = ParseSeq(result);
            Dictionary<string, object> extras = new Dictionary<string, object>();
            extras["seq"] = seq;
            string summary = g.Name + " | 已记 #" + seq.ToString();
            return BuildReply("group-post", g.Name, -1, extras, summary, new List<string>());
        }

        /// <summary>派单（工具名 group-delegate）——写组消息 + 唤醒目标猫（同一方法内顺序完成）；A337 起组员可用（对齐 group-info）。</summary>
        /// <param name="argsJson">工具参数 JSON（group / to / text 必填；refs 可选）</param>
        /// <returns>A214 回执（头行 + 摘要行）；失败为 ERR| 前缀（消息已入组时在文本中带 seq）</returns>
        public string Send(string argsJson)
        {
            string group = "";
            string catId = "";
            string err = ReadCommon(argsJson, out group, out catId);
            if (err.Length > 0)
            {
                return err;
            }
            string text = "";
            string to = "";
            string refs = "";
            bool notify = false;
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(argsJson))
                {
                    JsonElement root = doc.RootElement;
                    text = JsonString(root, "text");
                    to = JsonString(root, "to");
                    refs = JsonString(root, "refs");
                    notify = JsonBool(root, "notify");
                }
            }
            catch (Exception ex)
            {
                return "ERR|BAD_ARGS|参数 JSON 解析失败: " + ex.Message;
            }
            GroupSummary g = GroupStore.GetGroup(group);
            if (g == null)
            {
                return "ERR|GROUP_NOT_FOUND|组不存在: " + group;
            }
            if (!g.Running)
            {
                return "ERR|GROUP_NOT_RUNNING|组未启用: " + group;
            }
            if (!IsMember(g, catId))
            {
                return "ERR|GROUP_NOT_MEMBER|调用者不是本组成员: " + catId;
            }
            if (to.Length == 0)
            {
                return "ERR|BAD_ARGS|派单必须指定 to（@ 目标猫）";
            }
            // A402——to 认成员 key 或显示名（组内解析；歧义 / 未命中随错误附候选）
            string toKey = "";
            string toErr = "";
            if (!ResolveMemberKey(g, to, out toKey, out toErr))
            {
                return toErr;
            }
            string result = GroupStore.AppendMessage(g.Name, catId, "delegation", text, refs, toKey);
            if (!result.StartsWith("ok|", StringComparison.Ordinal))
            {
                return result;
            }
            long seq = ParseSeq(result);
            CatHostService.CatEntry cat = CatHostService.FindDeliveryCat(toKey);
            if (cat == null || cat.Session == null)
            {
                return "ERR|GROUP_TARGET_OFFLINE|目标猫未运行: " + toKey + "（消息已入组 #" + seq.ToString() + "）";
            }
            string content = "[来自组:" + g.Name + "]\\n" + text;
            string origin = "group:" + g.Name + ":" + seq.ToString() + ":delegation";
            if (notify)
            {
                // 显式通知（方案丙）——origin 第五段 = 派单方猫 key；目标轮末回执产生后由宿主投递回它
                origin = origin + ":" + catId;
            }
            if (!cat.Session.PostUserMessage(content, "user", "", origin))
            {
                return "ERR|GROUP_DELIVER_FAIL|投递未受理: " + toKey + "（消息已入组 #" + seq.ToString() + "）";
            }
            string toName = CatHostService.ResolveCatDisplayName(toKey);
            Dictionary<string, object> extras = new Dictionary<string, object>();
            extras["seq"] = seq;
            extras["to"] = toKey;
            string summary = g.Name + " | 已派单 → " + toName + " · #" + seq.ToString();
            return BuildReply("group-delegate", g.Name, -1, extras, summary, new List<string>());
        }
        /// <summary>重建会话（group-newsession）——管理员发起；目标须成员且运行中；管理员不得重建另一名管理员（单向数据流）；目标是调用者自己时挂起到轮末。</summary>
        /// <param name="argsJson">工具参数 JSON（group / to / reason 必填；firstMessage 可选）</param>
        /// <returns>A214 回执（头行 + 摘要行 + 正文行）；失败为 ERR| 前缀</returns>
        public string NewSession(string argsJson)
        {
            string group = "";
            string catId = "";
            string err = ReadCommon(argsJson, out group, out catId);
            if (err.Length > 0)
            {
                return err;
            }
            string to = "";
            string reason = "";
            string first = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(argsJson))
                {
                    JsonElement root = doc.RootElement;
                    to = JsonString(root, "to").Trim();
                    reason = JsonString(root, "reason").Trim();
                    first = JsonString(root, "firstMessage");
                }
            }
            catch (Exception ex)
            {
                return "ERR|BAD_ARGS|参数 JSON 解析失败: " + ex.Message;
            }
            GroupSummary g = GroupStore.GetGroup(group);
            if (g == null)
            {
                return "ERR|GROUP_NOT_FOUND|组不存在: " + group;
            }
            if (!g.Running)
            {
                return "ERR|GROUP_NOT_RUNNING|组未启用: " + group;
            }
            if (!IsAdmin(g, catId))
            {
                return "ERR|GROUP_NOT_ADMIN|调用者不是本组管理员: " + catId;
            }
            if (to.Length == 0)
            {
                return "ERR|BAD_ARGS|重建会话必须指定 to（@ 目标猫）";
            }
            // A402——to 认成员 key 或显示名（组内解析；歧义 / 未命中随错误附候选）
            string toKey = "";
            string toErr = "";
            if (!ResolveMemberKey(g, to, out toKey, out toErr))
            {
                return toErr;
            }
            // 单向数据流：管理员不得重建另一名管理员——只能重建自己或非管理成员
            if (!string.Equals(toKey, catId, StringComparison.Ordinal) && IsAdmin(g, toKey))
            {
                return "ERR|GROUP_TARGET_IS_ADMIN|管理员不得重建另一名管理员（仅可重建自己或非管理成员）: " + toKey;
            }
            if (reason.Length == 0)
            {
                return "ERR|BAD_ARGS|重建会话必须给出 reason（新会话唯一的上下文说明）";
            }
            CatHostService.CatEntry target = CatHostService.FindDeliveryCat(toKey);
            if (target == null || target.Session == null)
            {
                return "ERR|GROUP_TARGET_OFFLINE|目标猫未运行: " + toKey;
            }
            string noteText = "[系统] 请求重建会话 → " + toKey + " | 原因 " + reason;
            if (first.Length > 0)
            {
                noteText = noteText + " | 初始指令 " + first.Length.ToString() + " 字";
            }
            string appended = GroupStore.AppendMessage(g.Name, catId, "note", noteText, "", "");
            if (!appended.StartsWith("ok|", StringComparison.Ordinal))
            {
                return appended;
            }
            long seq = ParseSeq(appended);
            string origin = "group:" + g.Name + ":" + seq.ToString() + ":delegation";
            if (first.Length == 0)
            {
                origin = "";
            }
            bool self = string.Equals(toKey, catId, StringComparison.Ordinal);
            if (self)
            {
                if (!target.Session.RequestSessionRebuild(first, origin))
                {
                    return "ERR|GROUP_REBUILD_PENDING|本会话已有挂起的重建请求";
                }
            }
            else
            {
                string result = AdminService.RebuildCatSession(toKey, first, origin);
                if (!result.StartsWith("ok|", StringComparison.Ordinal))
                {
                    return result;
                }
            }
            string mode = "rebuilt";
            if (self)
            {
                mode = "queued";
            }
            string toName = CatHostService.ResolveCatDisplayName(toKey);
            Dictionary<string, object> extras = new Dictionary<string, object>();
            extras["seq"] = seq;
            extras["to"] = toKey;
            extras["mode"] = mode;
            string summary = g.Name + " | 已重建 → " + toName + " · #" + seq.ToString();
            if (self)
            {
                summary = g.Name + " | 已排队 → " + toName + " · #" + seq.ToString();
            }
            return BuildReply("group-newsession", g.Name, -1, extras, summary, new List<string>());
        }
        /// <summary>
        /// A214 回执构造——首行元数据头（ok / tool / target / items + 专有字段）+ 摘要行 + 正文行。
        /// </summary>
        /// <param name="tool">工具名</param>
        /// <param name="target">主来源（组名；空串 = 省略）</param>
        /// <param name="items">主计数（负值 = 省略）</param>
        /// <param name="extras">专有字段（null = 无）</param>
        /// <param name="summary">摘要行（正文首行）</param>
        /// <param name="bodyLines">正文行（可为空列表）</param>
        /// <returns>完整回执文本</returns>
        private static string BuildReply(string tool, string target, int items, Dictionary<string, object> extras, string summary, List<string> bodyLines)
        {
            Dictionary<string, object> head = new Dictionary<string, object>();
            head["ok"] = true;
            head["tool"] = tool;
            if (target.Length > 0)
            {
                head["target"] = target;
            }
            if (items >= 0)
            {
                head["items"] = items;
            }
            if (extras != null)
            {
                foreach (KeyValuePair<string, object> pair in extras)
                {
                    head[pair.Key] = pair.Value;
                }
            }
            System.Text.StringBuilder sb = new System.Text.StringBuilder();
            sb.Append(JsonUtil.Serialize(head));
            sb.Append("\n");
            sb.Append(summary);
            for (int i = 0; i < bodyLines.Count; i = i + 1)
            {
                sb.Append("\n");
                sb.Append(bodyLines[i]);
            }
            return sb.ToString();
        }
        /// <summary>
        /// 成员工作状态——会话细粒度态名（idle / wait / link / think / tool / run / reply）；
        /// 未运行或未建会话为 offline（A325）。与群聊球列 / cat.info 同源（ChatSession.BuildStateEntry 的 runState）。
        /// </summary>
        /// <param name="catKey">成员猫 key</param>
        /// <returns>态名（简短，直接可读）</returns>
        private static string MemberWorkState(string catKey)
        {
            CatHostService.CatEntry entry = CatHostService.FindDeliveryCat(catKey);
            if (entry == null || entry.Session == null)
            {
                return "offline";
            }
            Dictionary<string, object> state = entry.Session.BuildStateEntry();
            object value;
            if (!state.TryGetValue("runState", out value))
            {
                return "offline";
            }
            if (!(value is string))
            {
                return "offline";
            }
            string name = (string)value;
            if (name.Length == 0)
            {
                return "offline";
            }
            return name;
        }
        /// <summary>正文单行化——换行压平为空格（正文行式定界不被破坏）。</summary>
        /// <param name="text">原文</param>
        /// <returns>单行文本</returns>
        private static string Flatten(string text)
        {
            if (text == null || text.Length == 0)
            {
                return "";
            }
            return text.Replace("\r", " ").Replace("\n", " ");
        }
        /// <summary>消息类型中文标签——派单 / 回执 / 发言。</summary>
        /// <param name="type">消息类型</param>
        /// <returns>标签文本</returns>
        private static string LabelOf(string type)
        {
            if (type == "delegation")
            {
                return "派单";
            }
            if (type == "receipt")
            {
                return "回执";
            }
            return "发言";
        }

        /// <summary>
        /// 公共参数读取——group（必填）+ catId（宿主注入保留键，缺则无法判权限）。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON</param>
        /// <param name="group">输出组名</param>
        /// <param name="catId">输出调用者猫 key</param>
        /// <returns>错误文本（空 = 通过）</returns>
        private static string ReadCommon(string argsJson, out string group, out string catId)
        {
            group = "";
            catId = "";
            if (argsJson == null || argsJson.Length == 0)
            {
                return "ERR|BAD_ARGS|工具参数 JSON 为空";
            }
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(argsJson))
                {
                    JsonElement root = doc.RootElement;
                    if (root.ValueKind != JsonValueKind.Object)
                    {
                        return "ERR|BAD_ARGS|参数必须是 JSON 对象";
                    }
                    group = JsonString(root, "group").Trim();
                    catId = JsonString(root, "catId").Trim();
                }
            }
            catch (Exception ex)
            {
                return "ERR|BAD_ARGS|参数 JSON 解析失败: " + ex.Message;
            }
            if (group.Length == 0)
            {
                return "ERR|BAD_ARGS|缺少参数 group";
            }
            if (catId.Length == 0)
            {
                return "ERR|GROUP_NO_CALLER|缺少调用者标识（catId）——无法判定权限";
            }
            return "";
        }

        /// <summary>JSON 字符串属性读取——缺失 / 非字符串返回空串。</summary>
        /// <param name="root">对象元素</param>
        /// <param name="name">属性名</param>
        /// <returns>属性值</returns>
        private static string JsonString(JsonElement root, string name)
        {
            JsonElement el;
            if (!root.TryGetProperty(name, out el) || el.ValueKind != JsonValueKind.String)
            {
                return "";
            }
            string value = el.GetString();
            return value == null ? "" : value;
        }
        /// <summary>JSON 布尔属性读取——缺失 / 非 true 一律返回 false（notify 缺省不通知）。</summary>
        /// <param name="root">对象元素</param>
        /// <param name="name">属性名</param>
        /// <returns>true=显式为 true</returns>
        private static bool JsonBool(JsonElement root, string name)
        {
            JsonElement el;
            if (!root.TryGetProperty(name, out el))
            {
                return false;
            }
            return el.ValueKind == JsonValueKind.True;
        }

        /// <summary>成员判定。</summary>
        /// <param name="g">组投影</param>
        /// <param name="key">猫 key</param>
        /// <returns>true=组成员</returns>
        private static bool IsMember(GroupSummary g, string key)
        {
            if (g == null)
            {
                return false;
            }
            return g.HasMember(key);
        }
        /// <summary>订阅者判定——显式 subscriberKeys 名单（空 = 不自动转发）。</summary>
        /// <param name="g">组投影</param>
        /// <param name="key">猫 key</param>
        /// <returns>true=订阅者</returns>
        private static bool IsSubscriber(GroupSummary g, string key)
        {
            if (g == null)
            {
                return false;
            }
            return g.HasSubscriber(key);
        }

        /// <summary>管理员判定——显式 adminKeys 名单（空 = 无人可派单，失败可见）。</summary>
        /// <param name="g">组投影</param>
        /// <param name="key">猫 key</param>
        /// <returns>true=管理员</returns>
        private static bool IsAdmin(GroupSummary g, string key)
        {
            if (g == null)
            {
                return false;
            }
            return g.HasAdmin(key);
        }

        /// <summary>解析 AppendMessage 回执中的 seq（ok|组名|seq=N）。</summary>
        /// <param name="result">回执文本</param>
        /// <returns>序号（解析失败 0）</returns>
        private static long ParseSeq(string result)
        {
            int pos = result.LastIndexOf("seq=", StringComparison.Ordinal);
            if (pos < 0)
            {
                return 0;
            }
            string text = result.Substring(pos + 4).Trim();
            long seq;
            if (long.TryParse(text, out seq))
            {
                return seq;
            }
            return 0;
        }
        /// <summary>
        /// group-catinfo——单猫运行态统计（组成员可读任一成员；与 cat.info 单条同源，不另造投影）。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（group / cat 必填；catId 为宿主注入保留键）</param>
        /// <returns>A214 回执；失败为 ERR| 前缀</returns>
        public string CatInfo(string argsJson)
        {
            string group = "";
            string catId = "";
            string err = ReadCommon(argsJson, out group, out catId);
            if (err.Length > 0)
            {
                return err;
            }
            string target = "";
            string targetErr = ReadTarget(argsJson, group, catId, false, false, out target);
            if (targetErr.Length > 0)
            {
                return targetErr;
            }
            string text = CatHostService.HandleCatInfoOne(target);
            if (text.StartsWith("ERR|", StringComparison.Ordinal))
            {
                return text;
            }
            Dictionary<string, object> extras = new Dictionary<string, object>();
            extras["cat"] = target;
            List<string> body = new List<string>();
            body.Add(Flatten(text));
            return BuildReply("group-catinfo", group, -1, extras, "运行态：" + CatHostService.ResolveCatDisplayName(target) + "（" + target + "）", body);
        }
        /// <summary>
        /// group-catcfg-get——每猫配置读取（组成员可读任一成员；与 cat.cfg.get 同源，不设掩码——cat.cfg 无密钥字段）。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（group / cat 必填；catId 为宿主注入保留键）</param>
        /// <returns>A214 回执；失败为 ERR| 前缀</returns>
        public string CatCfgGet(string argsJson)
        {
            string group = "";
            string catId = "";
            string err = ReadCommon(argsJson, out group, out catId);
            if (err.Length > 0)
            {
                return err;
            }
            string target = "";
            string targetErr = ReadTarget(argsJson, group, catId, false, false, out target);
            if (targetErr.Length > 0)
            {
                return targetErr;
            }
            string text = CatHostService.HandleCatCfgGet(target);
            if (!text.StartsWith("cat.cfg | ", StringComparison.Ordinal))
            {
                return "ERR|GROUP_CATCFG_GET_FAIL|" + Flatten(text);
            }
            string[] lines = text.Split(new char[] { '\n' }, StringSplitOptions.RemoveEmptyEntries);
            List<string> body = new List<string>();
            for (int i = 0; i < lines.Length; i = i + 1)
            {
                string line = lines[i].TrimEnd('\r');
                if (line.Length > 0)
                {
                    body.Add(line);
                }
            }
            return BuildReply("group-catcfg-get", group, -1, null, "配置全量：" + target, body);
        }
        /// <summary>
        /// group-catcfg-set——每猫配置写入（仅组管理员；目标不含调用者自己）。
        /// 字段白名单与校验由 cat.cfg.set 单点承载（13 字段全开，等价默认会话权利）；受限工具名单在公共层拦。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（group / cat / field / value 必填；catId 为宿主注入保留键）</param>
        /// <returns>A214 回执；失败为 ERR| 前缀</returns>
        public string CatCfgSet(string argsJson)
        {
            string group = "";
            string catId = "";
            string err = ReadCommon(argsJson, out group, out catId);
            if (err.Length > 0)
            {
                return err;
            }
            string target = "";
            string targetErr = ReadTarget(argsJson, group, catId, true, true, out target);
            if (targetErr.Length > 0)
            {
                return targetErr;
            }
            string field = "";
            string value = "";
            string parseErr = ReadFieldValue(argsJson, out field, out value);
            if (parseErr.Length > 0)
            {
                return parseErr;
            }
            // [段1] 写入——与 cat.cfg.set 单点同源（字段校验 / 全量写回 / catcfg.apply 入队 / 写后读回）
            string text = CatHostService.HandleCatCfgSet(target + " " + field + " " + value);
            // [段2] 成败判据——成功路径以「已更新」收尾（HandleCatCfgSet 段3），失败路径不带该词
            if (text.IndexOf(" 已更新", StringComparison.Ordinal) < 0)
            {
                return "ERR|GROUP_CATCFG_SET_FAIL|" + Flatten(text);
            }
            Dictionary<string, object> extras = new Dictionary<string, object>();
            extras["cat"] = target;
            extras["field"] = field;
            List<string> body = new List<string>();
            body.Add(Flatten(text));
            return BuildReply("group-catcfg-set", group, -1, extras, "配置已写入：" + target + " / " + field, body);
        }
        /// <summary>
        /// group-clear——清空组消息流（仅组管理员）：旧流原地归档为 .bak 后重建空流，统计与订阅游标归零。
        /// 与面板清空按钮共用同一单点（GroupStore.ClearMessages），工具面只加权限层与 A214 回执。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（group 必填；catId 为宿主注入保留键）</param>
        /// <returns>A214 回执；失败为 ERR| 前缀</returns>
        public string Clear(string argsJson)
        {
            string group = "";
            string catId = "";
            string err = ReadCommon(argsJson, out group, out catId);
            if (err.Length > 0)
            {
                return err;
            }
            GroupSummary g = GroupStore.GetGroup(group);
            if (g == null)
            {
                return "ERR|GROUP_NOT_FOUND|组不存在: " + group;
            }
            if (!IsMember(g, catId))
            {
                return "ERR|GROUP_NOT_MEMBER|调用者不是本组成员: " + catId;
            }
            if (!g.Running)
            {
                return "ERR|GROUP_NOT_RUNNING|组未启用: " + group;
            }
            // [段1] 权限——清空是破坏性写面，只认 adminKeys 名单（组成员不足）
            if (!IsAdmin(g, catId))
            {
                return "ERR|GROUP_NOT_ADMIN|调用者不是本组管理员: " + catId;
            }
            long beforeSeq = g.LastSeq;
            int beforeCount = g.MsgCount;
            // [段2] 执行——同一单点：归档 .bak + 统计与订阅游标归零（新流从 #1 起）
            string result = GroupStore.ClearMessages(g.Name);
            if (!result.StartsWith("ok|", StringComparison.Ordinal))
            {
                return result;
            }
            string[] parts = result.Split(new char[] { '|' });
            string archived = "";
            if (parts.Length >= 3)
            {
                archived = parts[2];
            }
            string archiveText = archived.Length == 0 ? "（无消息文件）" : archived;
            Dictionary<string, object> extras = new Dictionary<string, object>();
            extras["archived"] = archived;
            extras["clearedCount"] = beforeCount;
            extras["clearedSeq"] = beforeSeq;
            List<string> body = new List<string>();
            body.Add("旧流 " + beforeCount.ToString() + " 条（最新 #" + beforeSeq.ToString() + "）已归档：" + archiveText);
            body.Add("新流从 #1 起；订阅游标已归零（归档文件保留在同组目录）");
            return BuildReply("group-clear", g.Name, beforeCount, extras, g.Name + " | 已清空 " + beforeCount.ToString() + " 条 · 归档 " + archiveText, body);
        }
        /// <summary>
        /// 组内目标解析（A402）——成员 key 精确优先，其次成员显示名（Trim + 忽略大小写）；
        /// 显示名重名或未命中一律拒收，并列出可选目标（失败可见、可自行纠正）。
        /// </summary>
        /// <param name="g">组投影</param>
        /// <param name="wanted">入参（成员 cat key 或显示名）</param>
        /// <param name="key">输出成员 cat key</param>
        /// <param name="err">输出错误文本（空 = 通过）</param>
        /// <returns>true=解析成功</returns>
        private static bool ResolveMemberKey(GroupSummary g, string wanted, out string key, out string err)
        {
            key = "";
            err = "";
            string want = wanted == null ? "" : wanted.Trim();
            if (want.Length == 0)
            {
                err = "ERR|BAD_ARGS|目标猫不能为空";
                return false;
            }
            // [段1] key 精确——成员名单即授权边界，真 key 直通
            if (g.HasMember(want))
            {
                key = want;
                return true;
            }
            // [段2] 成员显示名——只在本组成员内匹配（不放开到全局猫表）；重名视为歧义拒收
            string hit = "";
            int hitCount = 0;
            for (int i = 0; i < g.MemberKeys.Length; i = i + 1)
            {
                string memberKey = g.MemberKeys[i];
                string name = CatHostService.ResolveCatDisplayName(memberKey);
                if (name.Length == 0)
                {
                    continue;
                }
                if (!string.Equals(name, want, StringComparison.OrdinalIgnoreCase))
                {
                    continue;
                }
                hitCount = hitCount + 1;
                hit = memberKey;
            }
            if (hitCount == 1)
            {
                key = hit;
                return true;
            }
            if (hitCount > 1)
            {
                err = "ERR|GROUP_TARGET_AMBIGUOUS|显示名在组内命中 " + hitCount.ToString() + " 只猫——请改用 key（可用：" + DescribeMembers(g) + "）";
                return false;
            }
            err = "ERR|GROUP_TARGET_NOT_MEMBER|目标不在组内: " + want + "（可用：" + DescribeMembers(g) + "）";
            return false;
        }
        /// <summary>
        /// 成员候选文本——「显示名（key）」顿号连接（目标解析失败时随错误带回，便于直接复制 key）。
        /// </summary>
        /// <param name="g">组投影</param>
        /// <returns>候选文本（无成员 = 「（无成员）」）</returns>
        private static string DescribeMembers(GroupSummary g)
        {
            List<string> parts = new List<string>();
            for (int i = 0; i < g.MemberKeys.Length; i = i + 1)
            {
                string memberKey = g.MemberKeys[i];
                string name = CatHostService.ResolveCatDisplayName(memberKey);
                if (name.Length > 0)
                {
                    parts.Add(name + "（" + memberKey + "）");
                }
                else
                {
                    parts.Add(memberKey);
                }
            }
            if (parts.Count == 0)
            {
                return "（无成员）";
            }
            return string.Join("、", parts.ToArray());
        }
        /// <summary>
        /// 目标猫解析——组内成员解析（key 精确优先、其次显示名；A402）；不把组 key 直接喂给 cat.cfg 寻址，防 id 前缀误配。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（cat 必填）</param>
        /// <param name="group">组名</param>
        /// <param name="catId">调用者猫 key</param>
        /// <param name="requireAdmin">true=须组管理员（写面）</param>
        /// <param name="denySelf">true=拒绝目标是调用者自己（改自己请由人操作）</param>
        /// <param name="target">输出目标猫 key</param>
        /// <returns>错误文本（空 = 通过）</returns>
        private static string ReadTarget(string argsJson, string group, string catId, bool requireAdmin, bool denySelf, out string target)
        {
            target = "";
            GroupSummary g = GroupStore.GetGroup(group);
            if (g == null)
            {
                return "ERR|GROUP_NOT_FOUND|组不存在: " + group;
            }
            if (!IsMember(g, catId))
            {
                return "ERR|GROUP_NOT_MEMBER|调用者不是本组成员: " + catId;
            }
            if (!g.Running)
            {
                return "ERR|GROUP_NOT_RUNNING|组未启用: " + group;
            }
            if (requireAdmin && !IsAdmin(g, catId))
            {
                return "ERR|GROUP_NOT_ADMIN|调用者不是组管理员: " + catId;
            }
            string wanted = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(argsJson))
                {
                    wanted = JsonString(doc.RootElement, "cat").Trim();
                }
            }
            catch (Exception ex)
            {
                return "ERR|BAD_ARGS|参数 JSON 解析失败: " + ex.Message;
            }
            if (wanted.Length == 0)
            {
                return "ERR|BAD_ARGS|缺少参数 cat";
            }
            // [段1] 成员名单即授权边界——key 精确优先、其次成员显示名（A402 组内解析；不放开前缀匹配）
            string memberKey = "";
            string memberErr = "";
            if (!ResolveMemberKey(g, wanted, out memberKey, out memberErr))
            {
                return memberErr;
            }
            if (denySelf && string.Equals(memberKey, catId, StringComparison.Ordinal))
            {
                return "ERR|GROUP_TARGET_SELF|不能修改自己的配置——改自己请由人操作";
            }
            target = memberKey;
            return "";
        }
        /// <summary>
        /// 字段与值读取——field / value 必填（value 可为空串＝清空该字段，透传给 cat.cfg.set）。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON</param>
        /// <param name="field">输出字段名</param>
        /// <param name="value">输出新值</param>
        /// <returns>错误文本（空 = 通过）</returns>
        private static string ReadFieldValue(string argsJson, out string field, out string value)
        {
            field = "";
            value = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(argsJson))
                {
                    field = JsonString(doc.RootElement, "field").Trim();
                    value = JsonString(doc.RootElement, "value");
                }
            }
            catch (Exception ex)
            {
                return "ERR|BAD_ARGS|参数 JSON 解析失败: " + ex.Message;
            }
            if (field.Length == 0)
            {
                return "ERR|BAD_ARGS|缺少参数 field";
            }
            return "";
        }
    }
}
