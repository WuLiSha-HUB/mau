using System;
using System.Collections.Generic;
using System.Text.Json;
using CH4;
using Mau.Runtime;

namespace CatHome4.Admin
{
    /// <summary>
    /// 群组服务实现（IGroupService）——Group 工具面 group-info / group-inbox / group-say / group-send。
    /// 权限：调用者须为组成员（Info / Inbox / Say）；Send（@ 派单）须为组管理员（group.json adminKeys）。
    /// 投递：Send 在同一方法内顺序完成「写组消息 → 以 origin 投递目标猫」——不拆两步（避免单有记录或单有投递）。
    /// 线程模型：主线程同步（工具组 Flow 认领线调用；投递触碰会话面）。
    /// </summary>
    internal sealed class GroupService : IGroupService
    {
        /// <summary>单次拉取默认条数</summary>
        private const int DefaultLimit = 100;

        /// <summary>组信息——组行 JSON（成员 / 管理员带显示名 + 统计）。</summary>
        /// <param name="argsJson">工具参数 JSON（group 必填；catId 为宿主注入保留键）</param>
        /// <returns>JSON 文本；失败为 ERR| 前缀</returns>
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
            return BuildGroupJson(g);
        }

        /// <summary>拉取消息——按 after / limit 取组内消息流（seq 升序）。</summary>
        /// <param name="argsJson">工具参数 JSON（group 必填；after / limit 可选）</param>
        /// <returns>JSON 文本；失败为 ERR| 前缀</returns>
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
            List<GroupMessage> messages = GroupStore.ReadMessages(g.Name, after, limit);
            return BuildMessagesJson(g, messages);
        }

        /// <summary>发讨论消息（note）——只写组消息流，不投递。</summary>
        /// <param name="argsJson">工具参数 JSON（group / text 必填；to 可选）</param>
        /// <returns>JSON 文本；失败为 ERR| 前缀</returns>
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
            string result = GroupStore.AppendMessage(g.Name, catId, "note", text, "", to);
            if (!result.StartsWith("ok|", StringComparison.Ordinal))
            {
                return result;
            }
            long seq = ParseSeq(result);
            Dictionary<string, object> reply = new Dictionary<string, object>();
            reply["ok"] = true;
            reply["tool"] = "group-say";
            reply["group"] = g.Name;
            reply["seq"] = seq;
            return JsonUtil.Serialize(reply);
        }

        /// <summary>派单（delegation）——写组消息 + 投递目标猫（同一方法内顺序完成）。</summary>
        /// <param name="argsJson">工具参数 JSON（group / to / text 必填；refs 可选）</param>
        /// <returns>JSON 文本；失败为 ERR| 前缀（消息已入组时在文本中带 seq）</returns>
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
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(argsJson))
                {
                    JsonElement root = doc.RootElement;
                    text = JsonString(root, "text");
                    to = JsonString(root, "to");
                    refs = JsonString(root, "refs");
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
            CatHostService.CatEntry cat = CatHostService.FindCat(to);
            if (cat == null || cat.Session == null)
            {
                return "ERR|GROUP_TARGET_OFFLINE|目标猫未运行: " + to + "（消息已入组 #" + seq.ToString() + "）";
            }
            string content = "[来自组:" + g.Name + "]\n" + text;
            string origin = "group:" + g.Name + ":" + seq.ToString() + ":delegation";
            if (!cat.Session.PostUserMessage(content, "user", "", origin))
            {
                return "ERR|GROUP_DELIVER_FAIL|投递未受理: " + to + "（消息已入组 #" + seq.ToString() + "）";
            }
            Dictionary<string, object> reply = new Dictionary<string, object>();
            reply["ok"] = true;
            reply["tool"] = "group-send";
            reply["group"] = g.Name;
            reply["seq"] = seq;
            reply["to"] = to;
            return JsonUtil.Serialize(reply);
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

        /// <summary>组行 JSON——成员与管理员带显示名（key 为主键）。</summary>
        /// <param name="g">组投影</param>
        /// <returns>JSON 文本</returns>
        private static string BuildGroupJson(GroupSummary g)
        {
            List<object> members = new List<object>();
            for (int i = 0; i < g.MemberKeys.Length; i = i + 1)
            {
                Dictionary<string, object> row = new Dictionary<string, object>();
                row["key"] = g.MemberKeys[i];
                row["displayName"] = CatHostService.ResolveCatDisplayName(g.MemberKeys[i]);
                row["admin"] = IsAdmin(g, g.MemberKeys[i]);
                members.Add(row);
            }
            Dictionary<string, object> body = new Dictionary<string, object>();
            body["ok"] = true;
            body["tool"] = "group-info";
            body["name"] = g.Name;
            body["project"] = g.Project;
            body["enabled"] = g.Enabled;
            body["msgCount"] = g.MsgCount;
            body["lastSeq"] = g.LastSeq;
            body["lastAt"] = g.LastAt;
            body["members"] = members;
            return JsonUtil.Serialize(body);
        }

        /// <summary>消息流 JSON——消息带 from/to 显示名。</summary>
        /// <param name="g">组投影</param>
        /// <param name="messages">消息列表</param>
        /// <returns>JSON 文本</returns>
        private static string BuildMessagesJson(GroupSummary g, List<GroupMessage> messages)
        {
            List<object> list = new List<object>();
            for (int i = 0; i < messages.Count; i = i + 1)
            {
                GroupMessage m = messages[i];
                Dictionary<string, object> row = new Dictionary<string, object>();
                row["seq"] = m.Seq;
                row["from"] = m.From;
                row["fromName"] = m.From.Length > 0 ? CatHostService.ResolveCatDisplayName(m.From) : "";
                row["to"] = m.To;
                row["toName"] = m.To.Length > 0 ? CatHostService.ResolveCatDisplayName(m.To) : "";
                row["type"] = m.Type;
                row["text"] = m.Text;
                row["ts"] = m.Ts;
                row["refs"] = m.Refs;
                list.Add(row);
            }
            Dictionary<string, object> body = new Dictionary<string, object>();
            body["ok"] = true;
            body["tool"] = "group-inbox";
            body["group"] = g.Name;
            body["lastSeq"] = g.LastSeq;
            body["messages"] = list;
            return JsonUtil.Serialize(body);
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
