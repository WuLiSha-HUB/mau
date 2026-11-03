using System;
using System.Collections.Generic;
using System.Text.Json;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Http;
using Mau.Runtime;
using CH4;

namespace CatHome4.Admin
{
    /// <summary>
    /// Admin 配置面分部——配队（Group）数据面端点（A300）。
    /// 语义：组配置 / 成员 / 消息流归 GroupStore（Data/group/&lt;组名&gt;/）；本分部只管 HTTP 装配与成员显示名解析。
    /// </summary>
    internal static partial class AdminService
    {
        /// <summary>
        /// 组列表——GET /api/v1/groups（配队页数据源；成员带显示名）。
        /// </summary>
        /// <returns>组列表 JSON</returns>
        internal static IResult HandleGroupsGet()
        {
            return Results.Json(new { ok = true, groups = BuildGroupRows() });
        }

        /// <summary>
        /// 组列表行装配——成员解析为 {key, displayName}（catKey 是稳定主键，显示名只是附属内容）。
        /// </summary>
        /// <returns>行列表</returns>
        internal static List<object> BuildGroupRows()
        {
            List<GroupSummary> groups = GroupStore.ListGroups();
            List<object> list = new List<object>();
            for (int i = 0; i < groups.Count; i = i + 1)
            {
                list.Add(BuildGroupRow(groups[i]));
            }
            return list;
        }

        /// <summary>
        /// 单行组投影——配置 + 统计 + 成员显示名。
        /// </summary>
        /// <param name="g">组投影</param>
        /// <returns>行对象</returns>
        private static object BuildGroupRow(GroupSummary g)
        {
            // [段1] 成员解析（catKey → 显示名）
            List<object> members = new List<object>();
            for (int i = 0; i < g.MemberKeys.Length; i = i + 1)
            {
                string key = g.MemberKeys[i];
                long read = 0;
                g.MemberReads.TryGetValue(key, out read);
                long unread = g.LastSeq - read;
                if (unread < 0)
                {
                    unread = 0;
                }
                members.Add(new { key = key, displayName = CatHostService.ResolveCatDisplayName(key), lastReadSeq = read, unread = unread });
            }
            // [段2] 行装配
            return new
            {
                name = g.Name,
                members = members,
                adminKeys = g.AdminKeys,
                project = g.Project,
                createdAt = g.CreatedAt,
                layer = g.Layer,
                enabled = g.Enabled,
                lastSeq = g.LastSeq,
                msgCount = g.MsgCount,
                lastAt = g.LastAt,
                port = CatHostService.GroupHostPort(g.Name),
                running = CatHostService.GroupHostRunning(g.Name)
            };
        }

        /// <summary>
        /// 新建 / 改组——POST /api/v1/groups（body: {name, memberKeys[], project}；同名覆盖成员与项目）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleGroupsPost(HttpContext ctx)
        {
            // [段1] body 解析
            string body = await ReadBodyText(ctx);
            string name = "";
            string project = "";
            string[] memberKeys = new string[0];
            string[] adminKeys = null;
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    name = GetJsonString(root, "name").Trim();
                    project = GetJsonString(root, "project").Trim();
                    memberKeys = ParseStringArray(root, "memberKeys");
                    JsonElement adminEl;
                    if (root.TryGetProperty("adminKeys", out adminEl) && adminEl.ValueKind == JsonValueKind.Array)
                    {
                        adminKeys = ParseStringArray(root, "adminKeys");
                    }
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            // [段2] 落盘 + 回执（新建的组 enabled 默认 true → 同步拉起宿主，与「启动」语义一致）
            string result = GroupStore.CreateOrUpdate(name, memberKeys, project, adminKeys);
            if (result.StartsWith("ok|", StringComparison.Ordinal))
            {
                GroupSummary created = GroupStore.GetGroup(name.Trim());
                if (created != null && created.Enabled)
                {
                    CatHostService.StartGroupHost(created.Name);
                }
            }
            return BuildMutationResult(result);
        }

        /// <summary>
        /// 组启停——POST /api/v1/groups/enabled（body: {name, enabled}）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleGroupsEnabled(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string name = "";
            bool enabled = true;
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    name = GetJsonString(root, "name").Trim();
                    JsonElement enabledEl;
                    if (root.TryGetProperty("enabled", out enabledEl) && enabledEl.ValueKind == JsonValueKind.False)
                    {
                        enabled = false;
                    }
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            string result = GroupStore.SetEnabled(name, enabled);
            if (result.StartsWith("ok|", StringComparison.Ordinal))
            {
                if (enabled)
                {
                    CatHostService.StartGroupHost(name.Trim());
                }
                else
                {
                    CatHostService.StopGroupHost(name.Trim());
                }
            }
            return BuildMutationResult(result);
        }

        /// <summary>
        /// 删组——POST /api/v1/groups/delete（body: {name}）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleGroupsDelete(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string name = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    name = GetJsonString(doc.RootElement, "name").Trim();
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            CatHostService.StopGroupHost(name.Trim());
            string result = GroupStore.Delete(name);
            return BuildMutationResult(result);
        }

        /// <summary>
        /// 组消息流——GET /api/v1/groups/{name}/messages?after=N&amp;limit=M（组内 seq 升序）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>消息列表 JSON</returns>
        internal static IResult HandleGroupsMessagesGet(HttpContext ctx)
        {
            string name = "";
            if (ctx.Request.RouteValues["name"] != null)
            {
                name = ctx.Request.RouteValues["name"].ToString();
            }
            if (name.Length == 0)
            {
                return Results.Json(new { ok = false, error = "ERR|BAD_ARGS|缺少组名" });
            }
            return BuildGroupMessagesResult(name, ctx);
        }

        /// <summary>
        /// 组消息流（共用实现）——主端口带组名、组端口由端口绑定组名；after/limit 非法一律报错。
        /// </summary>
        /// <param name="name">组名</param>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>消息列表 JSON</returns>
        internal static IResult BuildGroupMessagesResult(string name, HttpContext ctx)
        {
            // [段1] 查询参数解析——非法值报错，不静默回落
            long after = 0;
            string afterText = ctx.Request.Query["after"].ToString();
            if (afterText.Length > 0)
            {
                if (!long.TryParse(afterText, out after))
                {
                    return Results.Json(new { ok = false, error = "ERR|BAD_ARGS|after 非整数: " + afterText });
                }
            }
            int limit = 0;
            string limitText = ctx.Request.Query["limit"].ToString();
            if (limitText.Length > 0)
            {
                if (!int.TryParse(limitText, out limit))
                {
                    return Results.Json(new { ok = false, error = "ERR|BAD_ARGS|limit 非整数: " + limitText });
                }
            }
            // [段2] 组存在性
            GroupSummary group = GroupStore.GetGroup(name);
            if (group == null)
            {
                return Results.Json(new { ok = false, error = "ERR|GROUP_NOT_FOUND|组不存在: " + name });
            }
            // [段3] 消息装配（发送方带显示名；from 空不作解析）
            List<GroupMessage> messages = GroupStore.ReadMessages(name, after, limit);
            List<object> list = new List<object>();
            for (int i = 0; i < messages.Count; i = i + 1)
            {
                GroupMessage m = messages[i];
                string display = "";
                if (m.From.Length > 0)
                {
                    display = CatHostService.ResolveCatDisplayName(m.From);
                }
                string toDisplay = "";
                if (m.To.Length > 0)
                {
                    toDisplay = CatHostService.ResolveCatDisplayName(m.To);
                }
                list.Add(new
                {
                    seq = m.Seq,
                    from = m.From,
                    displayName = display,
                    to = m.To,
                    toDisplayName = toDisplay,
                    type = m.Type,
                    text = m.Text,
                    ts = m.Ts,
                    refs = m.Refs
                });
            }
            return Results.Json(new { ok = true, name = group.Name, lastSeq = group.LastSeq, messages = list });
        }

        /// <summary>
        /// 组投递——POST /api/v1/groups/messages（body: {group, from, type, text, refs}）。
        /// 只写组消息流；派单到猫的投递归 P5（本端点供外部通道与自测共用）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleGroupsMessagesPost(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string group = "";
            string from = "";
            string type = "";
            string text = "";
            string refs = "";
            string to = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    group = GetJsonString(root, "group").Trim();
                    from = GetJsonString(root, "from").Trim();
                    type = GetJsonString(root, "type").Trim();
                    text = GetJsonString(root, "text");
                    refs = GetJsonString(root, "refs").Trim();
                    to = GetJsonString(root, "to").Trim();
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            return AppendGroupMessageResult(group, from, type, text, refs, to);
        }

        /// <summary>
        /// 投递（共用实现）——主端口带组名、组端口由端口绑定组名。
        /// </summary>
        /// <param name="group">组名</param>
        /// <param name="from">发送方猫 key（空 = 人 / 外部）</param>
        /// <param name="type">类型（delegation / receipt / note）</param>
        /// <param name="text">正文</param>
        /// <param name="refs">关联锚</param>
        /// <param name="to">@ 目标猫 key（空 = 不指向）</param>
        /// <returns>回执 JSON</returns>
        internal static IResult AppendGroupMessageResult(string group, string from, string type, string text, string refs, string to)
        {
            string result = GroupStore.AppendMessage(group, from, type, text, refs, to);
            return BuildMutationResult(result);
        }

        /// <summary>
        /// 投递——组端口版（POST /api/v1/group/messages；组名由端口绑定，body 不含 group）。
        /// </summary>
        /// <param name="name">组名</param>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> BuildGroupMessageAppend(string name, HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string from = "";
            string type = "";
            string text = "";
            string refs = "";
            string to = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    from = GetJsonString(root, "from").Trim();
                    type = GetJsonString(root, "type").Trim();
                    text = GetJsonString(root, "text");
                    refs = GetJsonString(root, "refs").Trim();
                    to = GetJsonString(root, "to").Trim();
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            return AppendGroupMessageResult(name, from, type, text, refs, to);
        }

        /// <summary>
        /// 已读推进——POST /api/v1/groups/read（body: {group, catKey, seq}）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleGroupsReadPost(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string group = "";
            string catKey = "";
            long seq = 0;
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    group = GetJsonString(root, "group").Trim();
                    catKey = GetJsonString(root, "catKey").Trim();
                    JsonElement seqEl;
                    if (root.TryGetProperty("seq", out seqEl) && seqEl.ValueKind == JsonValueKind.Number)
                    {
                        seq = seqEl.GetInt64();
                    }
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            return MarkGroupReadResult(group, catKey, seq);
        }

        /// <summary>
        /// 已读推进（共用实现）——只前进语义由 GroupStore 保证。
        /// </summary>
        /// <param name="group">组名</param>
        /// <param name="catKey">猫 key</param>
        /// <param name="seq">已读到序号</param>
        /// <returns>回执 JSON</returns>
        internal static IResult MarkGroupReadResult(string group, string catKey, long seq)
        {
            string result = GroupStore.MarkRead(group, catKey, seq);
            return BuildMutationResult(result);
        }

        /// <summary>
        /// 已读推进——组端口版（POST /api/v1/group/read；组名由端口绑定）。
        /// </summary>
        /// <param name="name">组名</param>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> BuildGroupRead(string name, HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string catKey = "";
            long seq = 0;
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    catKey = GetJsonString(root, "catKey").Trim();
                    JsonElement seqEl;
                    if (root.TryGetProperty("seq", out seqEl) && seqEl.ValueKind == JsonValueKind.Number)
                    {
                        seq = seqEl.GetInt64();
                    }
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            return MarkGroupReadResult(name, catKey, seq);
        }

        /// <summary>
        /// 单组信息——组端口 GET /api/v1/group/info（端口即组身份，组名由闭包绑定）。
        /// </summary>
        /// <param name="name">组名</param>
        /// <returns>组行 JSON</returns>
        internal static IResult BuildGroupInfoResult(string name)
        {
            GroupSummary g = GroupStore.GetGroup(name);
            if (g == null)
            {
                return Results.Json(new { ok = false, error = "ERR|GROUP_NOT_FOUND|组不存在: " + name });
            }
            return Results.Json(new { ok = true, group = BuildGroupRow(g) });
        }

        /// <summary>
        /// 组轻宿主端口——GET /api/v1/groups/host（前端「打开」寻址；未启动返回 running=false）。
        /// </summary>
        /// <returns>端口 JSON</returns>
        internal static IResult HandleGroupsHostGet()
        {
            List<GroupSummary> groups = GroupStore.ListGroups();
            List<object> list = new List<object>();
            for (int i = 0; i < groups.Count; i = i + 1)
            {
                list.Add(new
                {
                    name = groups[i].Name,
                    port = CatHostService.GroupHostPort(groups[i].Name),
                    running = CatHostService.GroupHostRunning(groups[i].Name)
                });
            }
            return Results.Json(new { ok = true, hosts = list });
        }

        /// <summary>
        /// GroupStore 结果文本 → 端点回执（ok| 前缀 = 成功；其余原样作 error）。
        /// </summary>
        /// <param name="result">GroupStore 返回文本</param>
        /// <returns>回执 JSON</returns>
        private static IResult BuildMutationResult(string result)
        {
            if (result == null || result.Length == 0)
            {
                return Results.Json(new { ok = false, error = "ERR|GROUP_FAIL|未返回结果" });
            }
            if (result.StartsWith("ok|", StringComparison.Ordinal))
            {
                return Results.Json(new { ok = true, detail = result });
            }
            return Results.Json(new { ok = false, error = result });
        }

        /// <summary>
        /// JSON 字符串数组解析——缺失 / 非数组返回空数组（项级非字符串与空串跳过）。
        /// </summary>
        /// <param name="root">对象元素</param>
        /// <param name="prop">属性名</param>
        /// <returns>字符串数组</returns>
        private static string[] ParseStringArray(JsonElement root, string prop)
        {
            JsonElement el;
            if (!root.TryGetProperty(prop, out el) || el.ValueKind != JsonValueKind.Array)
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
    }
}
