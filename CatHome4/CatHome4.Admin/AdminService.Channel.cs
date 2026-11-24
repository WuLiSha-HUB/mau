using System;
using System.Collections.Generic;
using System.Text.Json;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Http;
using Mau.Contracts;
using Mau.Runtime;

namespace CatHome4.Admin
{
    /// <summary>
    /// Admin 域 HTTP 注入通道面（A405）——通道池 CRUD + 注入端点。
    /// 授权分层：CRUD 在 console 面（ConsoleAuth 口令闸内）；注入端点由外部程序持通道 Key 调用，
    /// 认证在处理器内完成（HttpHost.ConsoleAuthGate 对该路径豁免，设计权威 design-ch4-channel.md）。
    /// 身份原则：外部程序只持 Key，永远不知道猫 key——服务端按 Key 反查绑定猫。
    /// </summary>
    internal static partial class AdminService
    {
        /// <summary>单次正文长度上限（字符数）——注入与播报共用。</summary>
        private const int ChannelTextMaxChars = 32768;

        /// <summary>
        /// 通道池列表——GET /api/v1/channels（Key 掩码展示；hasKey 供前端判断是否为通道配置过凭证）。
        /// </summary>
        /// <returns>列表 JSON——version + items</returns>
        internal static IResult HandleChannelsGet()
        {
            CH_ChannelConfigStore store = null;
            DataBox.TryResolve<CH_ChannelConfigStore>(out store);
            List<object> items = new List<object>();
            if (store != null)
            {
                CH_ChannelConfig[] configs = store.GetAll();
                for (int i = 0; i < configs.Length; i = i + 1)
                {
                    CH_ChannelConfig c = configs[i];
                    string key = store.GetKey(c.ChannelId);
                    string keyShown = "";
                    if (key.Length > 0)
                    {
                        keyShown = MaskApiKey(key);
                    }
                    items.Add(new
                    {
                        channelId = c.ChannelId.ToString("D"),
                        displayName = c.DisplayName,
                        catKey = c.CatKey,
                        catName = CatHostService.ResolveCatDisplayName(c.CatKey),
                        botKey = c.BotKey,
                        botName = ResolveChannelBotName(c.BotKey),
                        groups = c.Groups,
                        hasExit = c.BotKey.Length > 0,
                        key = keyShown,
                        hasKey = key.Length > 0
                    });
                }
            }
            var resp = new
            {
                version = 1,
                items = items
            };
            return Results.Json(resp);
        }

        /// <summary>通道池新建——POST /api/v1/channels（body: displayName / catKey / botKey? / groups?；A406 出口两字段可省=仅注入）。Key 自动生成，明文仅在本次回执返回一次（与「Key 只在 serve 启动时打印给人」同源）。</summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON——ok / channelId / key</returns>
        internal static async Task<IResult> HandleChannelsPost(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string displayName = "";
            string catKey = "";
            string botKey = "";
            string[] groups = Array.Empty<string>();
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    displayName = GetJsonString(root, "displayName");
                    catKey = GetJsonString(root, "catKey");
                    botKey = GetJsonString(root, "botKey");
                    groups = GetJsonStringArray(root, "groups");
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            if (displayName.Length == 0)
            {
                return Results.Json(new { ok = false, error = "displayName 为空" });
            }
            if (catKey.Length == 0 && botKey.Length == 0)
            {
                return Results.Json(new { ok = false, error = "至少配置一项能力：绑定猫（注入）或出口机器人（播报）" });
            }
            CH_ChannelConfigStore store = null;
            DataBox.TryResolve<CH_ChannelConfigStore>(out store);
            if (store == null)
            {
                return Results.Json(new { ok = false, error = "通道池未绑定" });
            }
            string catName = "";
            if (catKey.Length > 0)
            {
                catName = ResolveChannelCatName(catKey);
                if (catName.Length == 0)
                {
                    return Results.Json(new { ok = false, error = "绑定猫不存在: " + catKey });
                }
            }
            if (botKey.Length > 0 && ResolveChannelBotName(botKey).Length == 0)
            {
                return Results.Json(new { ok = false, error = "出口机器人不存在: " + botKey });
            }
            string key = CH_ChannelConfigStore.GenerateKey();
            CH_ChannelConfig config = new CH_ChannelConfig();
            config.ChannelId = Guid.NewGuid();
            config.DisplayName = displayName;
            config.CatKey = catKey;
            config.BotKey = botKey;
            config.Groups = groups;
            try
            {
                store.Save(config, key);
            }
            catch (Exception ex)
            {
                return Results.Json(new { ok = false, error = ex.Message });
            }
            LogStore.Add("CatHome4", 1, "HTTP 通道池新建「" + displayName + "」→ " + (catName.Length > 0 ? catName : "（纯播报）") + "（" + config.ChannelId.ToString("D") + "）", "CONFIG");
            return Results.Json(new { ok = true, channelId = config.ChannelId.ToString("D"), key = key });
        }

        /// <summary>通道池编辑——POST /api/v1/channels/edit（body: channelId + displayName / catKey；空串=保留现值）。A406 出口两字段按「出现即改」——botKey 空串 = 解除出口绑定，groups 空数组 = 清空白名单（清空即禁发）。</summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleChannelsEdit(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string channelId = "";
            string displayName = "";
            string catKey = "";
            bool catKeyPresent = false;
            bool botKeyPresent = false;
            string botKey = "";
            bool groupsPresent = false;
            string[] groups = Array.Empty<string>();
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    channelId = GetJsonString(root, "channelId");
                    displayName = GetJsonString(root, "displayName");
                    catKey = GetJsonString(root, "catKey");
                    // A406——出口字段按「出现即改、缺省保留」判（空串 = 解除出口绑定；空数组 = 清空白名单）
                    JsonElement element;
                    // A406——catKey 与 botKey 同口径：出现即改（空串 = 解除注入绑定，改纯播报通道）
                    if (root.TryGetProperty("catKey", out element) && element.ValueKind == JsonValueKind.String)
                    {
                        catKeyPresent = true;
                    }
                    if (root.TryGetProperty("botKey", out element) && element.ValueKind == JsonValueKind.String)
                    {
                        botKeyPresent = true;
                        botKey = element.GetString() ?? "";
                    }
                    if (root.TryGetProperty("groups", out element) && element.ValueKind == JsonValueKind.Array)
                    {
                        groupsPresent = true;
                        groups = ToStringArray(element);
                    }
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            Guid parsed;
            if (!Guid.TryParse(channelId, out parsed) || parsed == Guid.Empty)
            {
                return Results.Json(new { ok = false, error = "channelId 无效" });
            }
            CH_ChannelConfigStore store = null;
            DataBox.TryResolve<CH_ChannelConfigStore>(out store);
            if (store == null)
            {
                return Results.Json(new { ok = false, error = "通道池未绑定" });
            }
            CH_ChannelConfig config;
            if (!store.TryGet(parsed, out config))
            {
                return Results.Json(new { ok = false, error = "通道不存在" });
            }
            if (displayName.Length > 0)
            {
                config.DisplayName = displayName;
            }
            if (catKeyPresent)
            {
                string catName = "";
                if (catKey.Length > 0)
                {
                    catName = ResolveChannelCatName(catKey);
                    if (catName.Length == 0)
                    {
                        return Results.Json(new { ok = false, error = "绑定猫不存在: " + catKey });
                    }
                }
                config.CatKey = catKey;
            }
            if (botKeyPresent)
            {
                if (botKey.Length > 0 && ResolveChannelBotName(botKey).Length == 0)
                {
                    return Results.Json(new { ok = false, error = "出口机器人不存在: " + botKey });
                }
                config.BotKey = botKey;
            }
            if (groupsPresent)
            {
                config.Groups = groups;
            }
            try
            {
                store.Save(config, "");
            }
            catch (Exception ex)
            {
                return Results.Json(new { ok = false, error = ex.Message });
            }
            LogStore.Add("CatHome4", 1, "HTTP 通道池编辑：" + channelId, "CONFIG");
            return Results.Json(new { ok = true });
        }

        /// <summary>
        /// 通道 Key 重置——POST /api/v1/channels/rotate（body: channelId）；旧 Key 立即失效，新 Key 仅在本次回执返回。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON——ok / key</returns>
        internal static async Task<IResult> HandleChannelsRotate(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string channelId = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    channelId = GetJsonString(doc.RootElement, "channelId");
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            Guid parsed;
            if (!Guid.TryParse(channelId, out parsed) || parsed == Guid.Empty)
            {
                return Results.Json(new { ok = false, error = "channelId 无效" });
            }
            CH_ChannelConfigStore store = null;
            DataBox.TryResolve<CH_ChannelConfigStore>(out store);
            if (store == null)
            {
                return Results.Json(new { ok = false, error = "通道池未绑定" });
            }
            string key = "";
            bool ok;
            try
            {
                ok = store.RotateKey(parsed, out key);
            }
            catch (Exception ex)
            {
                return Results.Json(new { ok = false, error = ex.Message });
            }
            if (!ok)
            {
                return Results.Json(new { ok = false, error = "通道不存在" });
            }
            LogStore.Add("CatHome4", 1, "HTTP 通道 Key 重置：" + channelId, "CONFIG");
            return Results.Json(new { ok = true, key = key });
        }

        /// <summary>
        /// 通道池删除——POST /api/v1/channels/delete（body: channelId）；配置与 Key 一并移除。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleChannelsDelete(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string channelId = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    channelId = GetJsonString(doc.RootElement, "channelId");
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            Guid parsed;
            if (!Guid.TryParse(channelId, out parsed) || parsed == Guid.Empty)
            {
                return Results.Json(new { ok = false, error = "channelId 无效" });
            }
            CH_ChannelConfigStore store = null;
            DataBox.TryResolve<CH_ChannelConfigStore>(out store);
            if (store == null)
            {
                return Results.Json(new { ok = false, error = "通道池未绑定" });
            }
            bool removed;
            try
            {
                removed = store.Delete(parsed);
            }
            catch (Exception ex)
            {
                return Results.Json(new { ok = false, error = ex.Message });
            }
            if (!removed)
            {
                return Results.Json(new { ok = false, error = "通道不存在" });
            }
            LogStore.Add("CatHome4", 1, "HTTP 通道池删除：" + channelId, "CONFIG");
            return Results.Json(new { ok = true });
        }

        /// <summary>
        /// 通道注入——POST /api/v1/channels/inject（外部程序入口；body: text）。
        /// 认证 = 通道 Key（Authorization: Bearer / X-Channel-Key）；目标 = 通道绑定猫（调用方无法指定）。
        /// 注入走 sys 路径（type=systemautomessage · source=channel），OK 表示已受理入队（忙时排队，不打断在途轮次）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON——ok / channel / cat / result</returns>
        internal static async Task<IResult> HandleChannelsInject(HttpContext ctx)
        {
            string key = ExtractChannelKey(ctx);
            if (key.Length == 0)
            {
                LogStore.Add("Channel", 2, "通道注入拒绝 | 无 Key | " + (ctx.Request.Path.Value ?? ""), "A405");
                return Results.Json(new { ok = false, error = "缺少通道 Key（Authorization: Bearer <key>）" }, statusCode: 401);
            }
            CH_ChannelConfigStore store = null;
            DataBox.TryResolve<CH_ChannelConfigStore>(out store);
            if (store == null)
            {
                return Results.Json(new { ok = false, error = "通道池未绑定" }, statusCode: 500);
            }
            CH_ChannelConfig config;
            bool resolved;
            try
            {
                resolved = store.TryResolveByKey(key, out config);
            }
            catch (Exception ex)
            {
                LogStore.Add("Channel", 3, "通道池读取失败 | " + ex.Message, "A405");
                return Results.Json(new { ok = false, error = "通道池读取失败: " + ex.Message }, statusCode: 500);
            }
            if (!resolved)
            {
                LogStore.Add("Channel", 2, "通道注入拒绝 | Key 无效 | 前缀 " + KeyPrefixLabel(key), "A405");
                return Results.Json(new { ok = false, error = "通道 Key 无效" }, statusCode: 401);
            }
            string body = await ReadBodyText(ctx);
            string text = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    text = GetJsonString(doc.RootElement, "text");
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON（应为 {\"text\":\"...\"}）" }, statusCode: 400);
            }
            if (text.Trim().Length == 0)
            {
                return Results.Json(new { ok = false, error = "text 为空" }, statusCode: 400);
            }
            if (text.Length > ChannelTextMaxChars)
            {
                return Results.Json(new { ok = false, error = "text 超长（上限 " + ChannelTextMaxChars.ToString() + " 字符）" }, statusCode: 400);
            }
            if (config.CatKey.Length == 0)
            {
                // A406——纯播报通道没有注入目标：这是「能力未配置」，不是「猫离线」，两者状态码与处置都不同
                LogStore.Add("Channel", 2, "通道注入拒绝 | 该通道不接注入 | 通道 " + config.DisplayName, "A405");
                return Results.Json(new { ok = false, error = "该通道未配置注入目标（纯播报通道）" }, statusCode: 403);
            }
            CatHostService.CatEntry cat = CatHostService.FindDeliveryCat(config.CatKey);
            if (cat == null || cat.Session == null)
            {
                // 目标猫离线不自动拉起——外部程序不该有隐含的拉起权力（重启 / 手工 cat.start 由人决定）
                LogStore.Add("Channel", 2, "通道注入未受理 | 目标猫未运行 | 通道 " + config.DisplayName + " → " + config.CatKey, "A405");
                // A405 §三-1——回执不暴露 catKey（外部程序只持 Key，不持有绑定猫身份）；排错看 Channel 日志
                return Results.Json(new { ok = false, error = "绑定猫未运行" }, statusCode: 409);
            }
            string content = "[来自通道:" + config.DisplayName + "]\n" + text;
            string result = cat.Session.PostSystemMessage("channel", content);
            if (!string.Equals(result, "OK", StringComparison.Ordinal))
            {
                int status = 500;
                if (result.StartsWith("ERR|HOST_RESTARTING", StringComparison.Ordinal))
                {
                    status = 503;
                }
                LogStore.Add("Channel", 2, "通道注入未受理 | 通道 " + config.DisplayName + " → " + config.CatKey + " | " + result, "A405");
                return Results.Json(new { ok = false, error = result }, statusCode: status);
            }
            LogStore.Add("Channel", 1, "通道注入 | " + config.DisplayName + " → " + config.CatKey + " | " + text.Length.ToString() + " 字", "A405");
            return Results.Json(new
            {
                ok = true,
                channel = config.DisplayName,
                cat = config.CatKey,
                result = result
            });
        }

        /// <summary>下游出口播报——POST /api/v1/channels/publish（A406；外部程序入口；body: text）。认证 = 通道 Key；出口 = 通道配置的机器人 + 通道级群白名单（空 = 禁发）。广播语义——白名单即目标集，一次调用向每个群各投一份，逐群独立判定；聚合回执 200 全成 / 207 部分成 / 其余按首条失败映射。与注入的分工：注入是私密唤醒（本地受理），播报是对外投递（平台受理）——两者失败面不同构，不共用状态码语义。</summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON——ok / channel / bot / delivered / failed / groups[]（逐群明细）</returns>
        internal static async Task<IResult> HandleChannelsPublish(HttpContext ctx)
        {
            string key = ExtractChannelKey(ctx);
            if (key.Length == 0)
            {
                LogStore.Add("Channel", 2, "通道播报拒绝 | 无 Key | " + (ctx.Request.Path.Value ?? ""), "A406");
                return Results.Json(new { ok = false, error = "缺少通道 Key（Authorization: Bearer <key>）" }, statusCode: 401);
            }
            CH_ChannelConfigStore store = null;
            DataBox.TryResolve<CH_ChannelConfigStore>(out store);
            if (store == null)
            {
                return Results.Json(new { ok = false, error = "通道池未绑定" }, statusCode: 500);
            }
            CH_ChannelConfig config;
            bool resolved;
            try
            {
                resolved = store.TryResolveByKey(key, out config);
            }
            catch (Exception ex)
            {
                LogStore.Add("Channel", 3, "通道池读取失败 | " + ex.Message, "A406");
                return Results.Json(new { ok = false, error = "通道池读取失败: " + ex.Message }, statusCode: 500);
            }
            if (!resolved)
            {
                LogStore.Add("Channel", 2, "通道播报拒绝 | Key 无效 | 前缀 " + KeyPrefixLabel(key), "A406");
                return Results.Json(new { ok = false, error = "通道 Key 无效" }, statusCode: 401);
            }
            string body = await ReadBodyText(ctx);
            string text = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    if (root.ValueKind != JsonValueKind.Object)
                    {
                        return Results.Json(new { ok = false, error = "body 应为 JSON 对象（{\"text\":\"...\"}）" }, statusCode: 400);
                    }
                    // 参数面零容忍——播报目标是通道白名单，请求不接受任何群参数（含已退役的 group）
                    foreach (JsonProperty property in root.EnumerateObject())
                    {
                        if (!string.Equals(property.Name, "text", StringComparison.Ordinal))
                        {
                            return Results.Json(new { ok = false, error = "未知参数: " + property.Name + "（publish 只接受 text；播报目标由通道白名单决定）" }, statusCode: 400);
                        }
                    }
                    text = GetJsonString(root, "text");
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON（应为 {\"text\":\"...\"}）" }, statusCode: 400);
            }
            if (text.Trim().Length == 0)
            {
                return Results.Json(new { ok = false, error = "text 为空" }, statusCode: 400);
            }
            if (text.Length > ChannelTextMaxChars)
            {
                return Results.Json(new { ok = false, error = "text 超长（上限 " + ChannelTextMaxChars.ToString() + " 字符）" }, statusCode: 400);
            }
            if (config.BotKey.Length == 0)
            {
                LogStore.Add("Channel", 2, "通道播报拒绝 | 未接出口 | 通道 " + config.DisplayName, "A406");
                return Results.Json(new { ok = false, error = "该通道未配置出口机器人" }, statusCode: 403);
            }
            if (config.Groups.Length == 0)
            {
                LogStore.Add("Channel", 2, "通道播报拒绝 | 白名单为空 | 通道 " + config.DisplayName, "A406");
                return Results.Json(new { ok = false, error = "该通道未配置群白名单（空 = 禁发）" }, statusCode: 403);
            }
            IQQBotOps ops;
            DataBox.TryResolve<IQQBotOps>(out ops);
            if (ops == null)
            {
                return Results.Json(new { ok = false, error = "QQ 通道服务未注入" }, statusCode: 503);
            }
            // [段1] 逐群投递——白名单即目标集，一个群失败不中断其余群
            string botName = ResolveChannelBotName(config.BotKey);
            List<object> groupResults = new List<object>();
            int delivered = 0;
            int failed = 0;
            string firstError = "";
            for (int i = 0; i < config.Groups.Length; i = i + 1)
            {
                string target = config.Groups[i];
                string result;
                try
                {
                    // 通道不是猫：catId 传空串——服务面语义是「只按池白名单放行」，猫级四字段不参与
                    result = ops.SendProactiveTo("", config.BotKey, target, text);
                }
                catch (Exception ex)
                {
                    result = "ERR|EX|" + ex.GetType().Name + ": " + ex.Message;
                }
                if (result == null)
                {
                    result = "";
                }
                bool ok = !result.StartsWith("ERR|", StringComparison.Ordinal);
                if (ok)
                {
                    delivered = delivered + 1;
                    groupResults.Add(new { group = FirstLineOf(result), ok = true, result = result });
                }
                else
                {
                    failed = failed + 1;
                    if (firstError.Length == 0)
                    {
                        firstError = result;
                    }
                    groupResults.Add(new { group = target, ok = false, result = result });
                }
            }
            // [段2] 聚合回执——全成 200 / 部分成 207 / 全败按首条失败映射
            if (failed == 0)
            {
                LogStore.Add("Channel", 1, "通道播报 | " + config.DisplayName + " → " + delivered.ToString() + " 群 | " + text.Length.ToString() + " 字", "A406");
                return Results.Json(new { ok = true, channel = config.DisplayName, bot = botName, delivered = delivered, failed = 0, groups = groupResults });
            }
            if (delivered == 0)
            {
                LogStore.Add("Channel", 2, "通道播报未投递 | 通道 " + config.DisplayName + " | " + firstError, "A406");
                return Results.Json(new { ok = false, channel = config.DisplayName, bot = botName, delivered = 0, failed = failed, groups = groupResults }, statusCode: MapPublishStatus(firstError));
            }
            LogStore.Add("Channel", 2, "通道播报部分成功 | " + config.DisplayName + " | 成 " + delivered.ToString() + " 败 " + failed.ToString() + " | " + firstError, "A406");
            return Results.Json(new { ok = false, channel = config.DisplayName, bot = botName, delivered = delivered, failed = failed, groups = groupResults }, statusCode: 207);
        }

        /// <summary>
        /// 提取通道 Key——Authorization: Bearer 优先，其次 X-Channel-Key（外部程序两种都可用）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>Key（空=未提供）</returns>
        private static string ExtractChannelKey(HttpContext ctx)
        {
            string auth = ctx.Request.Headers["Authorization"].ToString();
            if (auth.Length > 7 && auth.StartsWith("Bearer ", StringComparison.OrdinalIgnoreCase))
            {
                return auth.Substring(7).Trim();
            }
            string custom = ctx.Request.Headers["X-Channel-Key"].ToString();
            if (custom.Length > 0)
            {
                return custom.Trim();
            }
            return "";
        }

        /// <summary>
        /// Key 前缀标签——日志脱敏用（只留前 6 字符，便于人区分是哪条 Key 被误用）。
        /// </summary>
        /// <param name="key">原始 Key</param>
        /// <returns>脱敏标签</returns>
        private static string KeyPrefixLabel(string key)
        {
            if (key.Length <= 6)
            {
                return "***";
            }
            return key.Substring(0, 6) + "***";
        }

        /// <summary>
        /// 绑定猫解析——返回显示名（空=不存在的猫）；走投递寻址链（普通猫 + 特殊会话 majordomo 均可绑定）。
        /// </summary>
        /// <param name="catKey">猫 key</param>
        /// <returns>显示名（未命中空串）</returns>
        private static string ResolveChannelCatName(string catKey)
        {
            CatHostService.CatEntry cat = CatHostService.FindDeliveryCat(catKey);
            if (cat == null)
            {
                return "";
            }
            if (cat.DisplayName != null && cat.DisplayName.Length > 0)
            {
                return cat.DisplayName;
            }
            return catKey;
        }

        /// <summary>
        /// 出口机器人显示名（A406）——按池配置 ID 取显示名；空 key / 池未注入 / 池内未命中一律空串。
        /// </summary>
        /// <param name="botKey">机器人池配置 ID</param>
        /// <returns>显示名（未命中空串）</returns>
        private static string ResolveChannelBotName(string botKey)
        {
            if (botKey == null || botKey.Length == 0)
            {
                return "";
            }
            Guid id;
            if (!Guid.TryParse(botKey, out id) || id == Guid.Empty)
            {
                return "";
            }
            CH_QqBotConfigStore pool = null;
            DataBox.TryResolve<CH_QqBotConfigStore>(out pool);
            if (pool == null)
            {
                return "";
            }
            CH_QqBotConfig poolItem;
            if (!pool.TryGet(id, out poolItem) || poolItem == null)
            {
                return "";
            }
            if (poolItem.DisplayName == null)
            {
                return "";
            }
            return poolItem.DisplayName;
        }

        /// <summary>
        /// 取 JSON 数组字段为字符串数组（缺失 / 类型不符 → 空数组）。
        /// </summary>
        /// <param name="root">JSON 根</param>
        /// <param name="name">字段名</param>
        /// <returns>字符串数组</returns>
        private static string[] GetJsonStringArray(JsonElement root, string name)
        {
            JsonElement element;
            if (!root.TryGetProperty(name, out element) || element.ValueKind != JsonValueKind.Array)
            {
                return Array.Empty<string>();
            }
            return ToStringArray(element);
        }

        /// <summary>
        /// JSON 数组 → 字符串数组（非字符串项与空项跳过）。
        /// </summary>
        /// <param name="array">JSON 数组</param>
        /// <returns>字符串数组</returns>
        private static string[] ToStringArray(JsonElement array)
        {
            List<string> result = new List<string>();
            for (int i = 0; i < array.GetArrayLength(); i = i + 1)
            {
                JsonElement item = array[i];
                if (item.ValueKind != JsonValueKind.String)
                {
                    continue;
                }
                string value = item.GetString() ?? "";
                if (value.Length > 0)
                {
                    result.Add(value);
                }
            }
            return result.ToArray();
        }

        /// <summary>
        /// 播报失败码 → HTTP 状态码（A406）——403 授权 / 出口未通 · 404 群寻址失败 · 其余 502 平台侧失败。
        /// </summary>
        /// <param name="err">服务面错误文本</param>
        /// <returns>状态码</returns>
        private static int MapPublishStatus(string err)
        {
            if (err.StartsWith("ERR|GROUP_NOT_FOUND", StringComparison.Ordinal)
                || err.StartsWith("ERR|GROUP_REQUIRED", StringComparison.Ordinal))
            {
                return 404;
            }
            if (err.StartsWith("ERR|BOT_NOT_CONNECTED", StringComparison.Ordinal)
                || err.StartsWith("ERR|NO_DEFAULT_BOT", StringComparison.Ordinal)
                || err.StartsWith("ERR|DIRECTORY_NOT_READY", StringComparison.Ordinal)
                || err.StartsWith("ERR|GROUP_NOT_ALLOWED", StringComparison.Ordinal))
            {
                return 403;
            }
            return 502;
        }

        /// <summary>
        /// 取文本首行（播报回执的投递目标摘要在首行）。
        /// </summary>
        /// <param name="text">原文</param>
        /// <returns>首行（Trim）</returns>
        private static string FirstLineOf(string text)
        {
            if (string.IsNullOrEmpty(text))
            {
                return "";
            }
            int index = text.IndexOf('\n');
            if (index < 0)
            {
                return text.Trim();
            }
            return text.Substring(0, index).Trim();
        }
    }
}
