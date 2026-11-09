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
            // [段1] 成员解析（catKey → 显示名 + 角色标记——@ 选择器与成员名册共用）
            // [段1b] 运行态与前文（A319——群聊成员球列数据源，与 console 猫卡同源 BuildStateEntry）
            List<object> members = new List<object>();
            for (int i = 0; i < g.MemberKeys.Length; i = i + 1)
            {
                string key = g.MemberKeys[i];
                bool memberRunning = false;
                string memberRunState = "idle";
                long memberContext = 0;
                CatHostService.CatEntry memberCat = CatHostService.FindDeliveryCat(key);
                if (memberCat != null)
                {
                    memberRunning = memberCat.Running;
                    if (memberCat.Session != null)
                    {
                        Dictionary<string, object> memberState = memberCat.Session.BuildStateEntry();
                        object runStateValue;
                        if (memberState.TryGetValue("runState", out runStateValue))
                        {
                            if (runStateValue is string)
                            {
                                memberRunState = (string)runStateValue;
                            }
                        }
                        object contextValue;
                        if (memberState.TryGetValue("context", out contextValue))
                        {
                            if (contextValue is long)
                            {
                                memberContext = (long)contextValue;
                            }
                        }
                    }
                }
                members.Add(new
                {
                    key = key,
                    displayName = CatHostService.ResolveCatDisplayName(key),
                    admin = ContainsKey(g.AdminKeys, key),
                    subscriber = ContainsKey(g.SubscriberKeys, key),
                    themeColor = CatHostService.ResolveCatThemeColor(key),
                    running = memberRunning,
                    runState = memberRunState,
                    context = memberContext
                });
            }
            // [段2] 行装配
            return new
            {
                name = g.Name,
                members = members,
                adminKeys = g.AdminKeys,
                subscriberKeys = g.SubscriberKeys,
                project = g.Project,
                createdAt = g.CreatedAt,
                layer = g.Layer,
                enabled = g.Enabled,
                lastSeq = g.LastSeq,
                msgCount = g.MsgCount,
                lastAt = g.LastAt,
                subscribeIdleSec = GroupStore.GetSubscribeIdleSec(g),
                port = CatHostService.GroupHostPort(g.Name),
                running = CatHostService.GroupHostRunning(g.Name)
            };
        }
        /// <summary>
        /// 名单包含判定——按 key 精确匹配（Ordinal）。
        /// </summary>
        /// <param name="keys">名单数组</param>
        /// <param name="key">待查 key</param>
        /// <returns>包含 true</returns>
        private static bool ContainsKey(string[] keys, string key)
        {
            if (keys == null || key == null)
            {
                return false;
            }
            for (int i = 0; i < keys.Length; i = i + 1)
            {
                if (string.Equals(keys[i], key, StringComparison.Ordinal))
                {
                    return true;
                }
            }
            return false;
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
            string[] subscriberKeys = null;
            int subscribeIdleSec = -1;
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
                    JsonElement subEl;
                    if (root.TryGetProperty("subscriberKeys", out subEl) && subEl.ValueKind == JsonValueKind.Array)
                    {
                        subscriberKeys = ParseStringArray(root, "subscriberKeys");
                    }
                    JsonElement idleEl;
                    if (root.TryGetProperty("subscribeIdleSec", out idleEl) && idleEl.ValueKind == JsonValueKind.Number)
                    {
                        subscribeIdleSec = idleEl.GetInt32();
                    }
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            // [段2] 落盘 + 回执（新建的组 enabled 默认 true → 同步拉起宿主，与「启动」语义一致）
            string result = GroupStore.CreateOrUpdate(name, memberKeys, project, adminKeys, subscriberKeys, subscribeIdleSec);
            if (result.StartsWith("ok|", StringComparison.Ordinal))
            {
                GroupSummary created = GroupStore.GetGroup(name.Trim());
                if (created != null && created.Enabled)
                {
                    GroupStore.StartGroup(created.Name);
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
                // 真实启停——运行态与宿主同步联动（停用组：不扫消息流、不占端口、工具面拒绝）
                if (enabled)
                {
                    GroupStore.StartGroup(name.Trim());
                    CatHostService.StartGroupHost(name.Trim());
                }
                else
                {
                    CatHostService.StopGroupHost(name.Trim());
                    GroupStore.StopGroup(name.Trim());
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
            // [段3] 消息装配（发送方带显示名；from 空不作解析）——after 缺省走尾部模式（最近 limit 条 + hasMore）
            List<GroupMessage> messages;
            bool hasMore = false;
            if (afterText.Length > 0)
            {
                messages = GroupStore.ReadMessages(name, after, limit);
            }
            else
            {
                messages = GroupStore.ReadTailMessages(name, limit, out hasMore);
            }
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
            return Results.Json(new { ok = true, name = group.Name, lastSeq = group.LastSeq, hasMore = hasMore, messages = list });
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
            // [段1] @ 投递口径——to 非空按派单处理（类型强制 delegation，回执链自动生效）
            string useTo = to == null ? "" : to.Trim();
            string useType = type;
            if (useTo.Length > 0)
            {
                useType = "delegation";
            }
            string result = GroupStore.AppendMessage(group, from, useType, text, refs, useTo);
            if (result == null || !result.StartsWith("ok|", StringComparison.Ordinal))
            {
                return BuildMutationResult(result);
            }
            // [段2] 非派单——只写流水
            if (useTo.Length == 0)
            {
                return Results.Json(new { ok = true, detail = result });
            }
            // [段3] 派单——投递目标（未运行 / 未受理：消息已入组，带提示不吞）
            long seq = ParseGroupSeq(result);
            GroupSummary current = GroupStore.GetGroup(group);
            string groupName = current == null ? group : current.Name;
            string content = "[来自组:" + groupName + "]\n" + text;
            string origin = "group:" + groupName + ":" + seq.ToString() + ":delegation";
            CatHostService.CatEntry cat = CatHostService.FindDeliveryCat(useTo);
            if (cat == null || cat.Session == null)
            {
                return Results.Json(new { ok = true, detail = result, delivered = false, seq = seq, warning = "ERR|GROUP_TARGET_OFFLINE|目标猫未运行: " + useTo + "（消息已入组 #" + seq.ToString() + "）" });
            }
            if (!cat.Session.PostUserMessage(content, "user", "", origin))
            {
                return Results.Json(new { ok = true, detail = result, delivered = false, seq = seq, warning = "ERR|GROUP_DELIVER_FAIL|投递未受理: " + useTo + "（消息已入组 #" + seq.ToString() + "）" });
            }
            return Results.Json(new { ok = true, detail = result, delivered = true, seq = seq, to = useTo });
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
        /// 清空消息流——组端口版（POST /api/v1/group/clear；组名由端口绑定）。
        /// </summary>
        /// <param name="name">组名</param>
        /// <returns>回执 JSON（detail = ok|组名|归档文件名）</returns>
        internal static IResult ClearGroupMessages(string name)
        {
            string result = GroupStore.ClearMessages(name);
            return BuildMutationResult(result);
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
            // [段1] 使用者显示名——全局设置投影（组端口与主端口不同源，页面无法跨端口读配置）
            string userName = "";
            if (_globalConfig != null)
            {
                userName = _globalConfig.Get("ui.user_name", "").Trim();
            }
            if (userName.Length == 0)
            {
                userName = "默认用户";
            }
            return Results.Json(new { ok = true, group = BuildGroupRow(g), userName = userName });
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
        /// 组消息回执解析——ok|组名|seq=N → N；解析失败 0。
        /// </summary>
        /// <param name="result">GroupStore 返回文本</param>
        /// <returns>消息序号（失败 0）</returns>
        private static long ParseGroupSeq(string result)
        {
            long seq = 0;
            if (result == null)
            {
                return 0;
            }
            int idx = result.LastIndexOf("seq=", StringComparison.Ordinal);
            if (idx < 0)
            {
                return 0;
            }
            string text = result.Substring(idx + 4);
            long.TryParse(text, out seq);
            return seq;
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
