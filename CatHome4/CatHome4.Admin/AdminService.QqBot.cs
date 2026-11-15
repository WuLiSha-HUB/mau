using System;
using System.Collections.Generic;
using System.Text.Json;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Http;
using Mau.Runtime;

namespace CatHome4.Admin
{
    /// <summary>
    /// Admin 域 QQ 机器人配置池面（A379）——池 CRUD 端点。
    /// 与 LLM API 池同构：普通配置（qqbot.json）不含 AppSecret，AppSecret 落独立 secrets（qqbot.cfg）。
    /// 消费面：收信服务（A379 起）与 qqbot 工具组（A380）按 ApiEnv 解析端点，按默认标记解析默认机器人。
    /// </summary>
    internal static partial class AdminService
    {
        /// <summary>
        /// QQ 机器人池列表——GET /api/v1/qqbot-apis（AppSecret 掩码展示；hasSecret 供前端判断是否已配置）。
        /// </summary>
        /// <returns>列表 JSON——version + items</returns>
        internal static IResult HandleQqBotApisGet()
        {
            CH_QqBotConfigStore store = null;
            DataBox.TryResolve<CH_QqBotConfigStore>(out store);
            List<object> items = new List<object>();
            if (store != null)
            {
                CH_QqBotConfig[] configs = store.GetAll();
                for (int i = 0; i < configs.Length; i = i + 1)
                {
                    CH_QqBotConfig c = configs[i];
                    string secret = store.GetSecret(c.QqBotId);
                    string secretShown;
                    if (secret.Length > 0)
                    {
                        secretShown = MaskApiKey(secret);
                    }
                    else
                    {
                        secretShown = "";
                    }
                    items.Add(new
                    {
                        qqBotId = c.QqBotId.ToString("D"),
                        displayName = c.DisplayName,
                        appId = c.AppId,
                        apiEnv = c.ApiEnv,
                        isDefault = c.IsDefault,
                        appSecret = secretShown,
                        hasSecret = secret.Length > 0
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
        /// QQ 机器人池新建——POST /api/v1/qqbot-apis（body: displayName/appId/apiEnv/appSecret）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleQqBotApisPost(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string displayName = "";
            string appId = "";
            string apiEnv = "";
            string appSecret = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    displayName = GetJsonString(root, "displayName");
                    appId = GetJsonString(root, "appId");
                    apiEnv = GetJsonString(root, "apiEnv");
                    appSecret = GetJsonString(root, "appSecret");
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
            if (appId.Length == 0)
            {
                return Results.Json(new { ok = false, error = "appId 为空" });
            }
            CH_QqBotConfigStore store = null;
            DataBox.TryResolve<CH_QqBotConfigStore>(out store);
            if (store == null)
            {
                return Results.Json(new { ok = false, error = "配置池未绑定" });
            }
            if (apiEnv.Length == 0)
            {
                apiEnv = "production";
            }
            CH_QqBotConfig config = new CH_QqBotConfig();
            config.QqBotId = Guid.NewGuid();
            config.DisplayName = displayName;
            config.AppId = appId;
            config.ApiEnv = apiEnv;
            try
            {
                store.Save(config, appSecret);
            }
            catch (Exception ex)
            {
                return Results.Json(new { ok = false, error = ex.Message });
            }
            LogStore.Add("CatHome4", 1, "QQ 机器人池新增配置「" + displayName + "」（" + config.QqBotId.ToString("D") + "）", "CONFIG");
            return Results.Json(new { ok = true, qqBotId = config.QqBotId.ToString("D") });
        }

        /// <summary>
        /// QQ 机器人池编辑——POST /api/v1/qqbot-apis/edit（body: qqBotId + 字段；appSecret 空=保留原秘密）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleQqBotApisEdit(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string qqBotId = "";
            string displayName = "";
            string appId = "";
            string apiEnv = "";
            string appSecret = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    qqBotId = GetJsonString(root, "qqBotId");
                    displayName = GetJsonString(root, "displayName");
                    appId = GetJsonString(root, "appId");
                    apiEnv = GetJsonString(root, "apiEnv");
                    appSecret = GetJsonString(root, "appSecret");
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            Guid parsed;
            if (!Guid.TryParse(qqBotId, out parsed) || parsed == Guid.Empty)
            {
                return Results.Json(new { ok = false, error = "qqBotId 无效" });
            }
            CH_QqBotConfigStore store = null;
            DataBox.TryResolve<CH_QqBotConfigStore>(out store);
            if (store == null)
            {
                return Results.Json(new { ok = false, error = "配置池未绑定" });
            }
            CH_QqBotConfig config;
            if (!store.TryGet(parsed, out config))
            {
                return Results.Json(new { ok = false, error = "配置不存在" });
            }
            // 字段级合并写——空串表示保留现值（name / appId 不允许清空，env 空回落 production）
            if (displayName.Length > 0)
            {
                config.DisplayName = displayName;
            }
            if (appId.Length > 0)
            {
                config.AppId = appId;
            }
            if (apiEnv.Length > 0)
            {
                config.ApiEnv = apiEnv;
            }
            try
            {
                store.Save(config, appSecret);
            }
            catch (Exception ex)
            {
                return Results.Json(new { ok = false, error = ex.Message });
            }
            LogStore.Add("CatHome4", 1, "QQ 机器人池编辑配置：" + qqBotId, "CONFIG");
            return Results.Json(new { ok = true });
        }

        /// <summary>
        /// QQ 机器人池设为默认——POST /api/v1/qqbot-apis/default（body: qqBotId）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleQqBotApisDefault(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string qqBotId = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    qqBotId = GetJsonString(root, "qqBotId");
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            Guid parsed;
            if (!Guid.TryParse(qqBotId, out parsed) || parsed == Guid.Empty)
            {
                return Results.Json(new { ok = false, error = "qqBotId 无效" });
            }
            CH_QqBotConfigStore store = null;
            DataBox.TryResolve<CH_QqBotConfigStore>(out store);
            if (store == null)
            {
                return Results.Json(new { ok = false, error = "配置池未绑定" });
            }
            if (!store.SetDefault(parsed))
            {
                return Results.Json(new { ok = false, error = "配置不存在" });
            }
            LogStore.Add("CatHome4", 1, "QQ 机器人池已设为默认：" + qqBotId, "CONFIG");
            return Results.Json(new { ok = true });
        }

        /// <summary>
        /// QQ 机器人池删除——POST /api/v1/qqbot-apis/delete（body: qqBotId）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleQqBotApisDelete(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string qqBotId = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    qqBotId = GetJsonString(root, "qqBotId");
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            Guid parsed;
            if (!Guid.TryParse(qqBotId, out parsed) || parsed == Guid.Empty)
            {
                return Results.Json(new { ok = false, error = "qqBotId 无效" });
            }
            CH_QqBotConfigStore store = null;
            DataBox.TryResolve<CH_QqBotConfigStore>(out store);
            if (store == null)
            {
                return Results.Json(new { ok = false, error = "配置池未绑定" });
            }
            if (!store.Delete(parsed))
            {
                return Results.Json(new { ok = false, error = "配置不存在" });
            }
            LogStore.Add("CatHome4", 1, "QQ 机器人池删除配置：" + qqBotId, "CONFIG");
            return Results.Json(new { ok = true });
        }
    }
}
