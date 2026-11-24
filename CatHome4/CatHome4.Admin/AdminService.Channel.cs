using System;
using System.Collections.Generic;
using System.Text.Json;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Http;
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
        /// <summary>单次注入正文长度上限（字符数）。</summary>
        private const int ChannelInjectMaxChars = 32768;

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

        /// <summary>
        /// 通道池新建——POST /api/v1/channels（body: displayName / catKey）。
        /// Key 自动生成，明文仅在本次回执返回一次（与「Key 只在 serve 启动时打印给人」同源）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON——ok / channelId / key</returns>
        internal static async Task<IResult> HandleChannelsPost(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string displayName = "";
            string catKey = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    displayName = GetJsonString(root, "displayName");
                    catKey = GetJsonString(root, "catKey");
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
            if (catKey.Length == 0)
            {
                return Results.Json(new { ok = false, error = "catKey 为空" });
            }
            CH_ChannelConfigStore store = null;
            DataBox.TryResolve<CH_ChannelConfigStore>(out store);
            if (store == null)
            {
                return Results.Json(new { ok = false, error = "通道池未绑定" });
            }
            string catName = ResolveChannelCatName(catKey);
            if (catName.Length == 0)
            {
                return Results.Json(new { ok = false, error = "绑定猫不存在: " + catKey });
            }
            string key = CH_ChannelConfigStore.GenerateKey();
            CH_ChannelConfig config = new CH_ChannelConfig();
            config.ChannelId = Guid.NewGuid();
            config.DisplayName = displayName;
            config.CatKey = catKey;
            try
            {
                store.Save(config, key);
            }
            catch (Exception ex)
            {
                return Results.Json(new { ok = false, error = ex.Message });
            }
            LogStore.Add("CatHome4", 1, "HTTP 通道池新建「" + displayName + "」→ " + catName + "（" + config.ChannelId.ToString("D") + "）", "CONFIG");
            return Results.Json(new { ok = true, channelId = config.ChannelId.ToString("D"), key = key });
        }

        /// <summary>
        /// 通道池编辑——POST /api/v1/channels/edit（body: channelId + displayName / catKey；空串=保留现值）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleChannelsEdit(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string channelId = "";
            string displayName = "";
            string catKey = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    channelId = GetJsonString(root, "channelId");
                    displayName = GetJsonString(root, "displayName");
                    catKey = GetJsonString(root, "catKey");
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
            if (catKey.Length > 0)
            {
                string catName = ResolveChannelCatName(catKey);
                if (catName.Length == 0)
                {
                    return Results.Json(new { ok = false, error = "绑定猫不存在: " + catKey });
                }
                config.CatKey = catKey;
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
            if (text.Length > ChannelInjectMaxChars)
            {
                return Results.Json(new { ok = false, error = "text 超长（上限 " + ChannelInjectMaxChars.ToString() + " 字符）" }, statusCode: 400);
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
    }
}
