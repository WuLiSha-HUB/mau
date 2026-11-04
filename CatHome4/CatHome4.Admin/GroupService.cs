using System;
using System.Collections.Generic;
using System.Text.Json;
using CH4;
using Mau.Runtime;

namespace CatHome4.Admin
{
    /// <summary>
    /// 群组服务实现（IGroupService）——Group 工具面五件：group-info / group-inbox / group-say / group-send / group-newsession。
    /// 权限：调用者须为组成员（Info / Inbox / Say）；Send 与 NewSession 另须组管理员（group.json adminKeys）。
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
            // [段1] 成员行——显示名 + key + 角色标记
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

        /// <summary>发讨论消息（note）——只写组消息流，不投递。</summary>
        /// <param name="argsJson">工具参数 JSON（group / text 必填；to 可选）</param>
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
            string to = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(argsJson))
                {
                    JsonElement root = doc.RootElement;
                    text = JsonString(root, "text");
                    to = JsonString(root, "to");
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
            string result = GroupStore.AppendMessage(g.Name, catId, "note", text, "", to);
            if (!result.StartsWith("ok|", StringComparison.Ordinal))
            {
                return result;
            }
            long seq = ParseSeq(result);
            Dictionary<string, object> extras = new Dictionary<string, object>();
            extras["seq"] = seq;
            string summary = g.Name + " | 已记 #" + seq.ToString();
            return BuildReply("group-say", g.Name, -1, extras, summary, new List<string>());
        }

        /// <summary>派单（delegation）——写组消息 + 投递目标猫（同一方法内顺序完成）。</summary>
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
            if (!IsAdmin(g, catId))
            {
                return "ERR|GROUP_NOT_ADMIN|调用者不是本组管理员: " + catId;
            }
            if (to.Length == 0)
            {
                return "ERR|BAD_ARGS|派单必须指定 to（@ 目标猫）";
            }
            if (!IsMember(g, to))
            {
                return "ERR|GROUP_TARGET_NOT_MEMBER|@ 目标不在组内: " + to;
            }
            string result = GroupStore.AppendMessage(g.Name, catId, "delegation", text, refs, to);
            if (!result.StartsWith("ok|", StringComparison.Ordinal))
            {
                return result;
            }
            long seq = ParseSeq(result);
            CatHostService.CatEntry cat = CatHostService.FindDeliveryCat(to);
            if (cat == null || cat.Session == null)
            {
                return "ERR|GROUP_TARGET_OFFLINE|目标猫未运行: " + to + "（消息已入组 #" + seq.ToString() + "）";
            }
            string content = "[来自组:" + g.Name + "]\n" + text;
            string origin = "group:" + g.Name + ":" + seq.ToString() + ":delegation";
            if (notify)
            {
                // 显式通知（方案丙）——origin 第五段 = 派单方猫 key；目标轮末回执产生后由宿主投递回它
                origin = origin + ":" + catId;
            }
            if (!cat.Session.PostUserMessage(content, "user", "", origin))
            {
                return "ERR|GROUP_DELIVER_FAIL|投递未受理: " + to + "（消息已入组 #" + seq.ToString() + "）";
            }
            string toName = CatHostService.ResolveCatDisplayName(to);
            Dictionary<string, object> extras = new Dictionary<string, object>();
            extras["seq"] = seq;
            extras["to"] = to;
            string summary = g.Name + " | 已派单 → " + toName + " · #" + seq.ToString();
            return BuildReply("group-send", g.Name, -1, extras, summary, new List<string>());
        }
        /// <summary>重建会话（group-newsession）——管理员发起；目标须成员且运行中；目标是调用者自己时挂起到轮末。</summary>
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
            if (!IsMember(g, to))
            {
                return "ERR|GROUP_TARGET_NOT_MEMBER|@ 目标不在组内: " + to;
            }
            if (reason.Length == 0)
            {
                return "ERR|BAD_ARGS|重建会话必须给出 reason（新会话唯一的上下文说明）";
            }
            CatHostService.CatEntry target = CatHostService.FindDeliveryCat(to);
            if (target == null || target.Session == null)
            {
                return "ERR|GROUP_TARGET_OFFLINE|目标猫未运行: " + to;
            }
            string noteText = "[系统] 请求重建会话 → " + to + " | 原因 " + reason;
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
            bool self = string.Equals(to, catId, StringComparison.Ordinal);
            if (self)
            {
                if (!target.Session.RequestSessionRebuild(first, origin))
                {
                    return "ERR|GROUP_REBUILD_PENDING|本会话已有挂起的重建请求";
                }
            }
            else
            {
                string result = AdminService.RebuildCatSession(to, first, origin);
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
            string toName = CatHostService.ResolveCatDisplayName(to);
            Dictionary<string, object> extras = new Dictionary<string, object>();
            extras["seq"] = seq;
            extras["to"] = to;
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
            if (key == null || key.Length == 0)
            {
                return false;
            }
            for (int i = 0; i < g.MemberKeys.Length; i = i + 1)
            {
                if (string.Equals(g.MemberKeys[i], key, StringComparison.Ordinal))
                {
                    return true;
                }
            }
            return false;
        }
        /// <summary>订阅者判定——显式 subscriberKeys 名单（空 = 不自动转发）。</summary>
        /// <param name="g">组投影</param>
        /// <param name="key">猫 key</param>
        /// <returns>true=订阅者</returns>
        private static bool IsSubscriber(GroupSummary g, string key)
        {
            if (key == null || key.Length == 0)
            {
                return false;
            }
            for (int i = 0; i < g.SubscriberKeys.Length; i = i + 1)
            {
                if (string.Equals(g.SubscriberKeys[i], key, StringComparison.Ordinal))
                {
                    return true;
                }
            }
            return false;
        }

        /// <summary>管理员判定——显式 adminKeys 名单（空 = 无人可派单，失败可见）。</summary>
        /// <param name="g">组投影</param>
        /// <param name="key">猫 key</param>
        /// <returns>true=管理员</returns>
        private static bool IsAdmin(GroupSummary g, string key)
        {
            if (key == null || key.Length == 0)
            {
                return false;
            }
            for (int i = 0; i < g.AdminKeys.Length; i = i + 1)
            {
                if (string.Equals(g.AdminKeys[i], key, StringComparison.Ordinal))
                {
                    return true;
                }
            }
            return false;
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
    }
}
