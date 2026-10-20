using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Http;
using CatHome4.Contracts;
using CatHome4.QQ;
using Mau.Runtime;
using Mau.Providers;
using CH4;

namespace CatHome4.Admin
{
    /// <summary>
    /// Program 管理端点面分部——M3 前后端配置管理（LLM API 池 CRUD + 每猫配置读写）。
    /// 端点处理器经 HttpHost.BuildApp 主端口注册（catsBuilder 非空——管理面收敛主端口）。
    /// 线程模型：读端点 HTTP 线程直读（BuildCatsJson 同先例）；写端点落盘 HTTP 线程 + 运行时生效入队主线程泵（catcfg.apply 指令）。
    /// </summary>
    internal static partial class AdminService
    {
        /// <summary>LLM API 配置池——Bootstrap 绑定（catcfg.apply 重建 Runtime 消费）</summary>
        internal static CH_LlmApiConfigStore _apiStore;

        /// <summary>QQ Bot 配置池——Bootstrap 绑定（qqbot 管理器消费）</summary>
        internal static CH_QqBotConfigStore _qqBotStore;

        /// <summary>外观配置池——Bootstrap 绑定（每猫外观引用解析消费——A244）</summary>
        internal static CH_AppearanceConfigStore _appearanceStore;

        /// <summary>全局配置存储——Bootstrap 绑定（catcfg.apply 重建 Runtime 消费）</summary>
        internal static ConfigStore _globalConfig;

        /// <summary>默认猫当前 API 配置身份——Bootstrap 赋值（catcfg.apply 变更比对）</summary>
        internal static Guid _defaultApiConfigId;

        /// <summary>默认猫当前思考强度覆盖——Bootstrap / catcfg.apply 变更比对（A234；空=跟随全局）</summary>
        internal static string _defaultReasoningEffort = "";

        /// <summary>
        /// 思考强度归一——A234（low/high/max 之外一律空=跟随全局；防上游协议拒绝）。
        /// </summary>
        /// <param name="value">原始值（可 null）</param>
        /// <returns>归一值（low/high/max/空）</returns>
        internal static string NormalizeReasoningEffort(string value)
        {
            string norm = value == null ? "" : value.Trim().ToLowerInvariant();
            if (norm == "low" || norm == "high" || norm == "max")
            {
                return norm;
            }
            return "";
        }

        /// <summary>
        /// 全局思考强度解析——A234（llm.reasoning_effort → env → 默认 high；GET cat-config 回执展示"跟随的是谁"）。
        /// </summary>
        /// <returns>全局生效强度（low/high/max）</returns>
        internal static string ResolveGlobalReasoningEffort()
        {
            string value = "";
            if (_globalConfig != null)
            {
                value = _globalConfig.Get("llm.reasoning_effort", "");
            }
            string norm = NormalizeReasoningEffort(value);
            if (norm.Length == 0)
            {
                string env = Environment.GetEnvironmentVariable("MAU_LLM_REASONING_EFFORT");
                norm = NormalizeReasoningEffort(env);
            }
            if (norm.Length == 0)
            {
                norm = "high";
            }
            return norm;
        }

        /// <summary>
        /// 管理路由注册——S2 解耦：入口壳经 IHttpRouteSink 向 Http 域注册管理端点（主端口仅一次）。
        /// 归属：Admin 域——llm-apis/qqbot-apis/cat-config/cat-default/workspace 管理面（S4 随 Admin 域迁 CatHome4.Admin）。
        /// </summary>
        /// <param name="sink">HTTP 路由注册面（HttpHost 实现）</param>
        internal static void RegisterAdminRoutes(IHttpRouteSink sink)
        {
            // LLM API 池 CRUD——M3 管理面
            sink.MapGet("/api/v1/llm-apis", (Delegate)HandleLlmApisGet);
            sink.MapPost("/api/v1/llm-apis", (Delegate)HandleLlmApisPost);
            sink.MapPost("/api/v1/llm-apis/edit", (Delegate)HandleLlmApisEdit);
            sink.MapPost("/api/v1/llm-apis/delete", (Delegate)HandleLlmApisDelete);
            sink.MapPost("/api/v1/llm-apis/default", (Delegate)HandleLlmApisDefault);
            // QQ Bot 池 CRUD——R2.3 管理面
            sink.MapGet("/api/v1/qqbot-apis", (Delegate)HandleQqBotApisGet);
            sink.MapPost("/api/v1/qqbot-apis", (Delegate)HandleQqBotApisPost);
            sink.MapPost("/api/v1/qqbot-apis/edit", (Delegate)HandleQqBotApisEdit);
            sink.MapPost("/api/v1/qqbot-apis/delete", (Delegate)HandleQqBotApisDelete);
            // 每猫配置读写——M3
            sink.MapGet("/api/v1/cat-config", (Delegate)HandleCatConfigGet);
            sink.MapPost("/api/v1/cat-config", (Delegate)HandleCatConfigPost);
            // 新猫默认模板——全局配置管理面（M3d）
            sink.MapGet("/api/v1/cat-default", (Delegate)HandleCatDefaultGet);
            sink.MapPost("/api/v1/cat-default", (Delegate)HandleCatDefaultPost);
            // 受控根编辑面——管理员面（M4d）
            sink.MapGet("/api/v1/workspace", (Delegate)HandleWorkspaceGet);
            sink.MapPost("/api/v1/workspace", (Delegate)HandleWorkspacePost);
            // A244 外观配置池——全局池管理面（每猫 appearanceId 引用 + 唯一默认项）
            sink.MapGet("/api/v1/appearance-apis", (Delegate)HandleAppearanceApisGet);
            sink.MapPost("/api/v1/appearance-apis", (Delegate)HandleAppearanceApisPost);
            sink.MapPost("/api/v1/appearance-apis/edit", (Delegate)HandleAppearanceApisEdit);
            sink.MapPost("/api/v1/appearance-apis/delete", (Delegate)HandleAppearanceApisDelete);
            sink.MapPost("/api/v1/appearance-apis/default", (Delegate)HandleAppearanceApisDefault);
            // 加载包池——全局池管理面（R4 加载包）
            sink.MapGet("/api/v1/packs", (Delegate)HandlePacksGet);
            sink.MapPost("/api/v1/packs", (Delegate)HandlePacksPost);
            sink.MapPost("/api/v1/packs/delete", (Delegate)HandlePacksDelete);
            // 打开数据目录——前端按钮（explorer.exe 打开持久化 Data 目录——三级锚定解析）
            sink.MapPost("/api/v1/open-data-dir", (Delegate)HandleOpenDataDir);
            // A65 对话图片附件——上传 + 取图（主端口注册；每猫 / majordomo 对话页走 RegisterChatPageRoutes）
            sink.MapPost("/api/v1/chat-images", (Delegate)HandleChatImagesUpload);
            sink.MapGet("/api/v1/cache-image/{**file}", (Delegate)HandleCacheImageGet);
            // A265 音频资产——轮末总结拼接音频取回（单独资产入口；与图片面分离）
            sink.MapGet("/api/v1/audio/{**file}", (Delegate)HandleAudioGet);
            // A269 数据库（Smriti）——库清单 / 新建库 / 条目查询 / 条目全文（写入走 db-* 工具面）
            sink.MapGet("/api/v1/db/libs", (Delegate)HandleDbLibsGet);
            sink.MapPost("/api/v1/db/libs", (Delegate)HandleDbLibsPost);
            sink.MapGet("/api/v1/db/libs/{lib}/entries", (Delegate)HandleDbEntriesGet);
            sink.MapGet("/api/v1/db/libs/{lib}/entries/{entryId}", (Delegate)HandleDbEntryGet);
        }

        /// <summary>
        /// 打开数据目录——POST /api/v1/open-data-dir（explorer.exe 打开持久化 Data 目录）。
        /// Data 根由 ResolveDataRoot 三级锚定（env→仓库根→AppData）——前端零路径知识。
        /// 纯前端 JS 无法直接打开本地资源管理器（浏览器沙箱）——必须后端端点调 explorer.exe。
        /// </summary>
        /// <returns>回执 JSON（含打开的目录路径）</returns>
        internal static IResult HandleOpenDataDir()
        {
            string dataDir = Path.Combine(_dataRoot, "Data");
            if (!Directory.Exists(dataDir))
            {
                Directory.CreateDirectory(dataDir);
            }
            try
            {
                System.Diagnostics.Process.Start("explorer.exe", dataDir);
                LogStore.Add("CatHome4", 1, "打开数据目录: " + dataDir, "CONFIG");
                return Results.Json(new { ok = true, path = dataDir });
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "打开数据目录失败：" + ex.Message, "CONFIG");
                return Results.Json(new { ok = false, error = ex.Message });
            }
        }

        /// <summary>
        /// LLM API 池列表——GET /api/v1/llm-apis（key 掩码展示；hasKey 供前端判断是否已配置）。
        /// </summary>
        /// <returns>列表 JSON——version + items</returns>
        internal static IResult HandleLlmApisGet()
        {
            CH_LlmApiConfigStore store = null;
            DataBox.TryResolve<CH_LlmApiConfigStore>(out store);
            List<object> items = new List<object>();
            if (store != null)
            {
                CH_LlmApiConfig[] configs = store.GetAll();
                for (int i = 0; i < configs.Length; i++)
                {
                    CH_LlmApiConfig c = configs[i];
                    string key = store.GetSecret(c.ApiConfigId);
                    string keyShown;
                    if (key.Length > 0)
                    {
                        keyShown = MaskApiKey(key);
                    }
                    else
                    {
                        keyShown = "";
                    }
                    items.Add(new
                    {
                        apiConfigId = c.ApiConfigId.ToString("D"),
                        displayName = c.DisplayName,
                        apiType = c.ApiType,
                        endpoint = c.Endpoint,
                        defaultModel = c.DefaultModel,
                        isDefault = c.IsDefault,
                        apiKey = keyShown,
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
        /// LLM API 池新建——POST /api/v1/llm-apis（body: displayName/apiType/endpoint/defaultModel/apiKey）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleLlmApisPost(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string displayName = "";
            string apiType = "";
            string endpoint = "";
            string defaultModel = "";
            string apiKey = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    displayName = GetJsonString(root, "displayName");
                    apiType = GetJsonString(root, "apiType");
                    endpoint = GetJsonString(root, "endpoint");
                    defaultModel = GetJsonString(root, "defaultModel");
                    apiKey = GetJsonString(root, "apiKey");
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
            if (endpoint.Length == 0)
            {
                return Results.Json(new { ok = false, error = "endpoint 为空" });
            }
            CH_LlmApiConfigStore store = null;
            DataBox.TryResolve<CH_LlmApiConfigStore>(out store);
            if (store == null)
            {
                return Results.Json(new { ok = false, error = "配置池未绑定" });
            }
            if (apiType.Length == 0)
            {
                apiType = "deepseek";
            }
            CH_LlmApiConfig config = new CH_LlmApiConfig();
            config.ApiConfigId = Guid.NewGuid();
            config.DisplayName = displayName;
            config.ApiType = apiType;
            config.Endpoint = endpoint;
            config.DefaultModel = defaultModel;
            store.Save(config, apiKey);
            LogStore.Add("CatHome4", 1, "LLM API 池新增配置「" + displayName + "」（" + config.ApiConfigId.ToString("D") + "）", "CONFIG");
            return Results.Json(new { ok = true, apiConfigId = config.ApiConfigId.ToString("D") });
        }

        /// <summary>
        /// LLM API 池设为默认——POST /api/v1/llm-apis/default（body: apiConfigId）。
        /// 默认端点语义：QuickCat 语料面与未显式配置的猫固定走默认（每次调用实时解析——切换立即生效）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleLlmApisDefault(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string apiConfigId = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    apiConfigId = GetJsonString(root, "apiConfigId");
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            Guid parsed;
            if (!Guid.TryParse(apiConfigId, out parsed) || parsed == Guid.Empty)
            {
                return Results.Json(new { ok = false, error = "apiConfigId 无效" });
            }
            CH_LlmApiConfigStore store = null;
            DataBox.TryResolve<CH_LlmApiConfigStore>(out store);
            if (store == null)
            {
                return Results.Json(new { ok = false, error = "配置池未绑定" });
            }
            if (!store.SetDefault(parsed))
            {
                return Results.Json(new { ok = false, error = "配置不存在" });
            }
            LogStore.Add("CatHome4", 1, "LLM API 池已设为默认：" + apiConfigId, "CONFIG");
            return Results.Json(new { ok = true });
        }

        /// <summary>
        /// LLM API 池编辑——POST /api/v1/llm-apis/edit（body: apiConfigId + 字段；apiKey 空=保留原 key）。
        /// endpoint/model/key 编辑立即生效——Runtime 每次 ChatStream 实时读 Store（M2e 零机制）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleLlmApisEdit(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string apiConfigId = "";
            string displayName = "";
            string apiType = "";
            string endpoint = "";
            string defaultModel = "";
            string apiKey = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    apiConfigId = GetJsonString(root, "apiConfigId");
                    displayName = GetJsonString(root, "displayName");
                    apiType = GetJsonString(root, "apiType");
                    endpoint = GetJsonString(root, "endpoint");
                    defaultModel = GetJsonString(root, "defaultModel");
                    apiKey = GetJsonString(root, "apiKey");
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            Guid id;
            if (!Guid.TryParse(apiConfigId, out id) || id == Guid.Empty)
            {
                return Results.Json(new { ok = false, error = "apiConfigId 非法" });
            }
            if (displayName.Length == 0)
            {
                return Results.Json(new { ok = false, error = "displayName 为空" });
            }
            if (endpoint.Length == 0)
            {
                return Results.Json(new { ok = false, error = "endpoint 为空" });
            }
            CH_LlmApiConfigStore store = null;
            DataBox.TryResolve<CH_LlmApiConfigStore>(out store);
            if (store == null)
            {
                return Results.Json(new { ok = false, error = "配置池未绑定" });
            }
            CH_LlmApiConfig config = new CH_LlmApiConfig();
            config.ApiConfigId = id;
            config.DisplayName = displayName;
            config.ApiType = apiType;
            config.Endpoint = endpoint;
            config.DefaultModel = defaultModel;
            store.Save(config, apiKey);
            LogStore.Add("CatHome4", 1, "LLM API 池「" + displayName + "」已更新（" + id.ToString("D") + "），密钥" + (apiKey.Length > 0 ? "已更换" : "保持原值"), "CONFIG");
            return Results.Json(new { ok = true, apiConfigId = id.ToString("D") });
        }

        /// <summary>
        /// LLM API 池删除——POST /api/v1/llm-apis/delete（body: apiConfigId；普通配置 + key 同删）。
        /// 默认配置可删（M3 拍板：多配置组场景；EnsureDefaults 首次初始化语义——删除不复活）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleLlmApisDelete(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string apiConfigId = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    apiConfigId = GetJsonString(doc.RootElement, "apiConfigId");
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            Guid id;
            if (!Guid.TryParse(apiConfigId, out id) || id == Guid.Empty)
            {
                return Results.Json(new { ok = false, error = "apiConfigId 非法" });
            }
            CH_LlmApiConfigStore store = null;
            DataBox.TryResolve<CH_LlmApiConfigStore>(out store);
            if (store == null)
            {
                return Results.Json(new { ok = false, error = "配置池未绑定" });
            }
            if (!store.Delete(id))
            {
                return Results.Json(new { ok = false, error = "配置不存在" });
            }
            LogStore.Add("CatHome4", 1, "LLM API 池配置已删除：" + id.ToString("D"), "CONFIG");
            return Results.Json(new { ok = true, apiConfigId = id.ToString("D") });
        }

        /// <summary>
        /// QQ Bot 池列表——GET /api/v1/qqbot-apis（secret 掩码展示；hasSecret 供前端判断是否已配置）。
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
                for (int i = 0; i < configs.Length; i++)
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
                        sandbox = c.Sandbox,
                        secret = secretShown,
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
        /// QQ Bot 池新建——POST /api/v1/qqbot-apis（body: displayName/appId/secret）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleQqBotApisPost(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string displayName = "";
            string appId = "";
            string secret = "";
            bool sandbox = true;
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    displayName = GetJsonString(root, "displayName");
                    appId = GetJsonString(root, "appId");
                    secret = GetJsonString(root, "secret");
                    sandbox = GetBoolProp(root, "sandbox");
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
            CH_QqBotConfig config = new CH_QqBotConfig();
            config.QqBotId = Guid.NewGuid();
            config.DisplayName = displayName;
            config.AppId = appId;
            config.Sandbox = sandbox;
            store.Save(config, secret);
            LogStore.Add("CatHome4", 1, "QQ Bot 池新增配置「" + displayName + "」（" + config.QqBotId.ToString("D") + "）", "CONFIG");
            QQBotService.Refresh();
            return Results.Json(new { ok = true, qqBotId = config.QqBotId.ToString("D") });
        }

        /// <summary>
        /// QQ Bot 池编辑——POST /api/v1/qqbot-apis/edit（body: qqBotId + 字段；secret 空=保留原 secret）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleQqBotApisEdit(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string qqBotId = "";
            string displayName = "";
            string appId = "";
            string secret = "";
            bool sandbox = true;
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    qqBotId = GetJsonString(root, "qqBotId");
                    displayName = GetJsonString(root, "displayName");
                    appId = GetJsonString(root, "appId");
                    secret = GetJsonString(root, "secret");
                    sandbox = GetBoolProp(root, "sandbox");
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            Guid id;
            if (!Guid.TryParse(qqBotId, out id) || id == Guid.Empty)
            {
                return Results.Json(new { ok = false, error = "qqBotId 非法" });
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
            CH_QqBotConfig config = new CH_QqBotConfig();
            config.QqBotId = id;
            config.DisplayName = displayName;
            config.AppId = appId;
            config.Sandbox = sandbox;
            store.Save(config, secret);
            LogStore.Add("CatHome4", 1, "QQ Bot 池「" + displayName + "」已更新（" + id.ToString("D") + "），secret" + (secret.Length > 0 ? "已更换" : "保持原值"), "CONFIG");
            QQBotService.Refresh();
            return Results.Json(new { ok = true, qqBotId = id.ToString("D") });
        }

        /// <summary>
        /// QQ Bot 池删除——POST /api/v1/qqbot-apis/delete（body: qqBotId；普通配置 + secret 同删）。
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
                    qqBotId = GetJsonString(doc.RootElement, "qqBotId");
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            Guid id;
            if (!Guid.TryParse(qqBotId, out id) || id == Guid.Empty)
            {
                return Results.Json(new { ok = false, error = "qqBotId 非法" });
            }
            CH_QqBotConfigStore store = null;
            DataBox.TryResolve<CH_QqBotConfigStore>(out store);
            if (store == null)
            {
                return Results.Json(new { ok = false, error = "配置池未绑定" });
            }
            if (!store.Delete(id))
            {
                return Results.Json(new { ok = false, error = "配置不存在" });
            }
            LogStore.Add("CatHome4", 1, "QQ Bot 池配置已删除：" + id.ToString("D"), "CONFIG");
            QQBotService.Refresh();
            return Results.Json(new { ok = true, qqBotId = id.ToString("D") });
        }

        /// <summary>
        /// 每猫配置读取——GET /api/v1/cat-config?cat=&lt;id|majordomo&gt;。
        /// 返回：displayName/apiConfigId/persona/toolNames/injectList + allToolNames（工具勾选清单）+ apiOptions（API 下拉清单）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>配置 JSON</returns>
        internal static IResult HandleCatConfigGet(HttpContext ctx)
        {
            string catKey = ctx.Request.Query["cat"].ToString();
            if (catKey.Length == 0)
            {
                return Results.Json(new { ok = false, error = "cat 参数为空" });
            }
            // 边界校验——catKey 不承载路径语义（拒绝分隔符/穿越/通配——GET 与 POST 对称收口）
            if (!IsSafeCatKey(catKey))
            {
                return Results.Json(new { ok = false, error = "cat 参数非法: " + catKey });
            }
            CatCfgData cfg = LoadCatCfg(Path.Combine(_dataRoot, "Data", "sessions", catKey, "cat.cfg"));
            if (cfg == null)
            {
                return Results.Json(new { ok = false, error = "cat.cfg 不存在: " + catKey });
            }
            // API 下拉清单——配置池全量（id + 显示名）
            CH_LlmApiConfigStore store = null;
            DataBox.TryResolve<CH_LlmApiConfigStore>(out store);
            List<object> apiOptions = new List<object>();
            if (store != null)
            {
                CH_LlmApiConfig[] configs = store.GetAll();
                for (int i = 0; i < configs.Length; i++)
                {
                    apiOptions.Add(new
                    {
                        apiConfigId = configs[i].ApiConfigId.ToString("D"),
                        displayName = configs[i].DisplayName,
                        isDefault = configs[i].IsDefault
                    });
                }
            }
            // R2.3 QQ Bot 下拉清单——Bot 池全量（id + 显示名 + 占用者；A58 1:1）
            CH_QqBotConfigStore qqStore = null;
            DataBox.TryResolve<CH_QqBotConfigStore>(out qqStore);
            List<object> qqbotOptions = new List<object>();
            if (qqStore != null)
            {
                CH_QqBotConfig[] qqConfigs = qqStore.GetAll();
                for (int i = 0; i < qqConfigs.Length; i++)
                {
                    // A58——boundCat = 除本猫外的占用者显示名（前端据此禁用已绑他猫的 Bot；后端保存时仍显式校验）
                    qqbotOptions.Add(new
                    {
                        qqBotId = qqConfigs[i].QqBotId.ToString("D"),
                        displayName = qqConfigs[i].DisplayName,
                        boundCat = FindQqBotBindingOwner(catKey, qqConfigs[i].QqBotId.ToString("D"))
                    });
                }
            }
            var resp = new
            {
                ok = true,
                cat = catKey,
                displayName = cfg.DisplayName,
                type = cfg.Type,
                apiConfigId = cfg.ApiConfigId,
                persona = cfg.Persona,
                toolNames = cfg.ToolNames,
                injectList = cfg.InjectList,
                packs = cfg.Packs,
                dbLibs = cfg.DbLibs,
                allPacks = BuildAllPacks(),
                qqbotId = cfg.QqBotId,
                qqbotEnable = cfg.QqBotEnable,
                userInjectIdleOnly = cfg.UserInjectIdleOnly,
                reasoningEffort = cfg.ReasoningEffort,
                appearanceId = cfg.AppearanceId,
                roundSummary = ResolveEffectiveRoundSummary(catKey),
                defaultReasoningEffort = ResolveGlobalReasoningEffort(),
                enabledRoots = cfg.EnabledRoots,
                allRoots = BuildAllRootsJson(),
                allToolNames = GetConfigurableToolNames(),
                allTools = GetToolsWithGroupForConfig(),
                defectGroups = BuildToolDefectItems(),
                staleToolNames = ResolveStaleToolNames(cfg.ToolNames),
                apiOptions = apiOptions,
                qqbotOptions = qqbotOptions,
                appearanceOptions = BuildAppearanceOptions()
            };
            return Results.Json(resp);
        }
        /// <summary>
        /// catKey 边界校验——cat.cfg 路径直拼前收口（拒绝路径分隔符/穿越/通配/空）。
        /// 语义：catKey 只能是单层目录名（id / 显示名 / majordomo）——不承载任何路径语义。
        /// </summary>
        /// <param name="key">寻址键</param>
        /// <returns>true=可安全拼入 sessions 目录</returns>
        private static bool IsSafeCatKey(string key)
        {
            if (key == null || key.Length == 0 || key.Length > 128)
            {
                return false;
            }
            if (key == "." || key == "..")
            {
                return false;
            }
            for (int i = 0; i < key.Length; i = i + 1)
            {
                char c = key[i];
                if (c < ' ')
                {
                    return false;
                }
                if (c == '/' || c == '\\' || c == ':' || c == '*' || c == '?' || c == '"' || c == '<' || c == '>' || c == '|')
                {
                    return false;
                }
            }
            return true;
        }

        /// <summary>
        /// 每猫配置写入——POST /api/v1/cat-config（body: cat/displayName/apiConfigId/persona/type/toolNames/injectList/packs/enabledRoots/qqbotId/qqbotEnable/userInjectIdleOnly）。
        /// 🔴 字段级合并写（PUT 语义 · 2026-10-02 判例蒸馏轮）——字段出现才落盘，缺省保留磁盘现值；显式空值（空数组 / 空串）= 清空。
        /// displayName 非空校验（空一律拒绝——与 cat.cfg.set 同源）。
        /// toolNames 写时校验（非法名过滤）；落盘 HTTP 线程原子写；运行时生效（字段更新 + Swap）入队主线程泵 catcfg.apply。
        /// 生效语义：apiConfigId 立即生效（SwapLlmRuntime）；persona/toolNames/injectList/displayName 新会话生效（session.new 重注入）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON（ok=已受理落盘）</returns>
        internal static async Task<IResult> HandleCatConfigPost(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string catKey = "";
            string apiConfigId = "";
            // A41-3 显式空值语义——字段出现且为空/全零 = 清空回默认端点（字段缺省 = 保留旧值）
            bool apiConfigIdPresent = false;
            bool displayNamePresent = false;
            bool personaPresent = false;
            bool typePresent = false;
            bool toolNamesPresent = false;
            bool qqbotEnablePresent = false;
            bool injectListPresent = false;
            bool packsPresent = false;
            bool dbLibsPresent = false;
            bool enabledRootsPresent = false;
            bool userInjectIdleOnlyPresent = false;
            bool reasoningEffortPresent = false;
            bool appearanceIdPresent = false;
            string displayName = "";
            string persona = "";
            string typeValue = "";
            string toolNames = "";
            string qqbotId = "";
            string reasoningEffort = "";
            bool qqbotEnable = false;
            bool userInjectIdleOnly = false;
            string appearanceIdValue = "";
            List<string> injectList = new List<string>();
            List<string> enabledRoots = new List<string>();
            List<string> packs = new List<string>();
            List<string> dbLibs = new List<string>();
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    catKey = GetJsonString(root, "cat");
                    // 字段级合并写（2026-10-02 判例蒸馏轮）——先登记本请求出现的字段（缺省 = 保留旧值）
                    JsonElement probeEl;
                    displayNamePresent = root.TryGetProperty("displayName", out probeEl);
                    personaPresent = root.TryGetProperty("persona", out probeEl);
                    typePresent = root.TryGetProperty("type", out probeEl);
                    toolNamesPresent = root.TryGetProperty("toolNames", out probeEl);
                    qqbotEnablePresent = root.TryGetProperty("qqbotEnable", out probeEl);
                    enabledRootsPresent = root.TryGetProperty("enabledRoots", out probeEl);
                    userInjectIdleOnlyPresent = root.TryGetProperty("userInjectIdleOnly", out probeEl);
                    reasoningEffortPresent = root.TryGetProperty("reasoningEffort", out probeEl);
                    appearanceIdPresent = root.TryGetProperty("appearanceId", out probeEl);
                    packsPresent = root.TryGetProperty("packs", out probeEl);
                    dbLibsPresent = root.TryGetProperty("dbLibs", out probeEl);
                    injectListPresent = root.TryGetProperty("injectList", out probeEl);
                    JsonElement apiConfigIdEl;
                    if (root.TryGetProperty("apiConfigId", out apiConfigIdEl))
                    {
                        apiConfigIdPresent = true;
                    }
                    apiConfigId = GetJsonString(root, "apiConfigId");
                    displayName = GetJsonString(root, "displayName");
                    persona = GetJsonString(root, "persona");
                    typeValue = GetJsonString(root, "type");
                    toolNames = GetJsonString(root, "toolNames");
                    qqbotId = GetJsonString(root, "qqbotId");
                    qqbotEnable = GetBoolProp(root, "qqbotEnable");
                    userInjectIdleOnly = GetBoolProp(root, "userInjectIdleOnly");
                    reasoningEffort = GetJsonString(root, "reasoningEffort");
                    appearanceIdValue = GetJsonString(root, "appearanceId");
                    JsonElement rootsEl;
                    if (root.TryGetProperty("enabledRoots", out rootsEl) && rootsEl.ValueKind == JsonValueKind.Array)
                    {
                        for (int i = 0; i < rootsEl.GetArrayLength(); i++)
                        {
                            JsonElement item = rootsEl[i];
                            if (item.ValueKind == JsonValueKind.String)
                            {
                                string got = item.GetString();
                                if (got != null && got.Length > 0)
                                {
                                    enabledRoots.Add(got);
                                }
                            }
                        }
                    }
                    JsonElement packsEl;
                    if (root.TryGetProperty("packs", out packsEl) && packsEl.ValueKind == JsonValueKind.Array)
                    {
                        for (int i = 0; i < packsEl.GetArrayLength(); i = i + 1)
                        {
                            JsonElement item = packsEl[i];
                            if (item.ValueKind == JsonValueKind.String)
                            {
                                string got = item.GetString();
                                if (got != null && got.Length > 0)
                                {
                                    // 挂载清单——池内 key；未挂载/池外项在 pack 调用期校验（此处只做去空白归一）
                                    packs.Add(got.Trim());
                                }
                            }
                        }
                    }
                    JsonElement dbLibsEl;
                    if (root.TryGetProperty("dbLibs", out dbLibsEl) && dbLibsEl.ValueKind == JsonValueKind.Array)
                    {
                        for (int i = 0; i < dbLibsEl.GetArrayLength(); i = i + 1)
                        {
                            JsonElement item = dbLibsEl[i];
                            if (item.ValueKind == JsonValueKind.String)
                            {
                                string got = item.GetString();
                                if (got != null && got.Length > 0)
                                {
                                    // 库白名单——库 ID 清单；库存在性与授权校验后置（A266 首版只落字段）
                                    dbLibs.Add(got.Trim());
                                }
                            }
                        }
                    }
                    JsonElement injectEl;
                    if (root.TryGetProperty("injectList", out injectEl) && injectEl.ValueKind == JsonValueKind.Array)
                    {
                        for (int i = 0; i < injectEl.GetArrayLength(); i++)
                        {
                            JsonElement item = injectEl[i];
                            if (item.ValueKind == JsonValueKind.String)
                            {
                                string got = item.GetString();
                                if (got != null && got.Length > 0)
                                {
                                    // 路径校验——只接受完整路径（绝对路径/id: 命名空间）；相对路径/非法格式不录入（莎拍板 2026-08-25）
                                    string trimmed = got.Trim();
                                    if (IsValidInjectPath(trimmed))
                                    {
                                        injectList.Add(trimmed);
                                    }
                                    else
                                    {
                                        LogStore.Add("CatHome4", 2, "猫配置：注入路径被拒绝（非法格式）" + got, "CONFIG");
                                    }
                                }
                            }
                        }
                    }
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            if (catKey.Length == 0)
            {
                return Results.Json(new { ok = false, error = "cat 为空" });
            }
            // 边界校验——catKey 不承载路径语义（GET 与 POST 对称收口）
            if (!IsSafeCatKey(catKey))
            {
                return Results.Json(new { ok = false, error = "cat 参数非法: " + catKey });
            }
            if (catKey != "majordomo" && FindCat(catKey) == null)
            {
                return Results.Json(new { ok = false, error = "猫不存在: " + catKey });
            }
            // M2c 写时校验——池外工具名过滤（持久化面只落合法名）；A132 被剔名字随响应带回（调用方可见，不静默）
            string[] removedToolNames;
            string validToolNames = ValidateToolNames(toolNames, out removedToolNames);
            // A261 启用根剔除出声——池外根 id 随响应带回（与工具名单同源，不静默）
            string[] removedEnabledRoots = new string[0];
            bool rootsPoolUnavailable = false;
            // 落盘——读旧配置保留 id/displayName/running/port；apiConfigId 空=保留旧值
            CatCfgData cfg = LoadCatCfg(Path.Combine(_dataRoot, "Data", "sessions", catKey, "cat.cfg"));
            if (cfg == null)
            {
                return Results.Json(new { ok = false, error = "cat.cfg 不存在: " + catKey });
            }
            // A41-3——显式清空（空串/全零）回默认端点；显式非法值拒绝；字段缺省保留旧值
            if (apiConfigIdPresent)
            {
                string trimmedApiConfigId = apiConfigId.Trim();
                if (trimmedApiConfigId.Length == 0 || trimmedApiConfigId == Guid.Empty.ToString("D"))
                {
                    // 存全零串——与 SaveCatCfg 出口一致（读侧以 Guid.Empty 判默认端点，实时解析全局默认）
                    cfg.ApiConfigId = Guid.Empty.ToString("D");
                }
                else
                {
                    Guid parsed;
                    if (!Guid.TryParse(trimmedApiConfigId, out parsed) || parsed == Guid.Empty)
                    {
                        return Results.Json(new { ok = false, error = "apiConfigId 非法" });
                    }
                    cfg.ApiConfigId = trimmedApiConfigId;
                }
            }
            if (qqbotId.Length > 0)
            {
                Guid parsed;
                if (!Guid.TryParse(qqbotId, out parsed) || parsed == Guid.Empty)
                {
                    return Results.Json(new { ok = false, error = "qqbotId 非法" });
                }
                // A58 1:1 查重——同一 Bot 不得被两只猫绑定（入口面显式拒绝；运行时不做去重）
                string boundBy = FindQqBotBindingOwner(catKey, qqbotId);
                if (boundBy.Length > 0)
                {
                    return Results.Json(new { ok = false, error = "该 QQ Bot 已绑定猫「" + boundBy + "」——一只 Bot 只能绑一只猫" });
                }
                cfg.QqBotId = qqbotId;
            }
            // 字段级合并写（2026-10-02 判例蒸馏轮）——字段出现才落盘，缺省保留磁盘现值（空数组 / 空串 = 显式清空）
            if (displayNamePresent)
            {
                string trimmedName = displayName.Trim();
                if (trimmedName.Length == 0)
                {
                    return Results.Json(new { ok = false, error = "displayName 不能为空" });
                }
                cfg.DisplayName = trimmedName;
            }
            if (qqbotEnablePresent)
            {
                cfg.QqBotEnable = qqbotEnable;
            }
            if (userInjectIdleOnlyPresent)
            {
                cfg.UserInjectIdleOnly = userInjectIdleOnly;
            }
            if (reasoningEffortPresent)
            {
                // A234——空=跟随全局；非空白名单校验（非法拒绝）
                string effNorm = reasoningEffort == null ? "" : reasoningEffort.Trim().ToLowerInvariant();
                if (effNorm.Length == 0)
                {
                    cfg.ReasoningEffort = "";
                }
                else if (effNorm == "low" || effNorm == "high" || effNorm == "max")
                {
                    cfg.ReasoningEffort = effNorm;
                }
                else
                {
                    return Results.Json(new { ok = false, error = "reasoningEffort 非法（low/high/max/空）" });
                }
            }
            if (appearanceIdPresent)
            {
                // A244——空/全零=跟随默认外观；非零须 GUID（外观条目存在性在解析期判定）
                string trimmedAppearance = appearanceIdValue == null ? "" : appearanceIdValue.Trim();
                if (trimmedAppearance.Length == 0 || trimmedAppearance == Guid.Empty.ToString("D"))
                {
                    cfg.AppearanceId = Guid.Empty.ToString("D");
                }
                else
                {
                    Guid parsedAppearance;
                    if (!Guid.TryParse(trimmedAppearance, out parsedAppearance) || parsedAppearance == Guid.Empty)
                    {
                        return Results.Json(new { ok = false, error = "appearanceId 非法" });
                    }
                    cfg.AppearanceId = parsedAppearance.ToString("D");
                }
            }
            if (personaPresent)
            {
                cfg.Persona = persona;
            }
            if (typePresent)
            {
                cfg.Type = typeValue == null ? "" : typeValue.Trim();
            }
            if (toolNamesPresent)
            {
                cfg.ToolNames = validToolNames;
            }
            if (injectListPresent)
            {
                cfg.InjectList = injectList.ToArray();
            }
            if (packsPresent)
            {
                cfg.Packs = packs.ToArray();
            }
            if (dbLibsPresent)
            {
                cfg.DbLibs = dbLibs.ToArray();
            }
            if (enabledRootsPresent)
            {
                // 启用根校验——workspace 强制 + 全局池子集；池外 id 剔除并出声（A261）
                cfg.EnabledRoots = ValidateEnabledRoots(enabledRoots.ToArray(), out removedEnabledRoots, out rootsPoolUnavailable);
            }
            SaveCatCfgData(catKey, cfg);
            // 运行时生效——入队主线程泵（注册表/会话面仅主线程触碰）
            _catQueue.Enqueue("catcfg.apply " + catKey);
            LogStore.Add("CatHome4", 1, "猫配置已受理：" + catKey + "（工具面 " + cfg.ToolNames + "）", "CONFIG");
            // A132 剔除出声——被剔名字随响应带回（调用方可见；明细已记日志，不静默）
            // 字段级合并写——回执取落盘实况（cfg.ToolNames），不取本次提交值（字段缺省时二者不同）
            return Results.Json(new { ok = true, cat = catKey, toolNames = cfg.ToolNames, removedToolNames = removedToolNames, dbLibs = cfg.DbLibs, enabledRoots = cfg.EnabledRoots, removedEnabledRoots = removedEnabledRoots, rootsPoolUnavailable = rootsPoolUnavailable });
        }

        /// <summary>
        /// 全局默认模板读取——GET /api/v1/cat-default（新猫默认配置管理面）。
        /// 返回：baseRole/defaultPersona/defaultToolNames/defaultInjectList + allToolNames（工具勾选清单）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>模板 JSON</returns>
        internal static IResult HandleCatDefaultGet(HttpContext ctx)
        {
            CatDefaultCfgData tpl = LoadCatDefaultCfg();
            string baseRole = FallbackBaseRole;
            string defaultPersona = "";
            string defaultToolNames = "";
            string[] defaultInjectList = new string[0];
            string[] defaultPacks = new string[0];
            bool defaultUserInjectIdleOnly = false;
            if (tpl != null)
            {
                if (tpl.BaseRole != null)
                {
                    baseRole = tpl.BaseRole;
                }
                if (tpl.DefaultPersona != null)
                {
                    defaultPersona = tpl.DefaultPersona;
                }
                if (tpl.DefaultToolNames != null)
                {
                    defaultToolNames = tpl.DefaultToolNames;
                }
                if (tpl.DefaultInjectList != null)
                {
                    defaultInjectList = tpl.DefaultInjectList;
                }
                if (tpl.DefaultPacks != null)
                {
                    defaultPacks = tpl.DefaultPacks;
                }
                defaultUserInjectIdleOnly = tpl.DefaultUserInjectIdleOnly;
            }
            var resp = new
            {
                ok = true,
                baseRole = baseRole,
                defaultPersona = defaultPersona,
                defaultToolNames = defaultToolNames,
                defaultInjectList = defaultInjectList,
                defaultPacks = defaultPacks,
                defaultUserInjectIdleOnly = defaultUserInjectIdleOnly,
                allToolNames = GetConfigurableToolNames(),
                allTools = GetToolsWithGroupForConfig(),
                defectGroups = BuildToolDefectItems(),
                staleToolNames = ResolveStaleToolNames(defaultToolNames),
                allPacks = BuildAllPacks()
            };
            return Results.Json(resp);
        }

        /// <summary>
        /// 全局默认模板写入——POST /api/v1/cat-default（body: baseRole/defaultPersona/defaultToolNames/defaultInjectList）。
        /// injectList 路径校验（只接受完整路径——非法不录入）；toolNames 写时校验；原子写落盘。
        /// 生效语义：新猫创建时继承（已创建猫不受影响）；baseRole 新会话生效（session.new 重注入）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleCatDefaultPost(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            string baseRole = "";
            string defaultPersona = "";
            string defaultToolNames = "";
            List<string> defaultInjectList = new List<string>();
            List<string> defaultPacks = new List<string>();
            bool defaultUserInjectIdleOnly = false;
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    baseRole = GetJsonString(root, "baseRole");
                    defaultPersona = GetJsonString(root, "defaultPersona");
                    defaultToolNames = GetJsonString(root, "defaultToolNames");
                    defaultUserInjectIdleOnly = GetBoolProp(root, "defaultUserInjectIdleOnly");
                    JsonElement dPacksEl;
                    if (root.TryGetProperty("defaultPacks", out dPacksEl) && dPacksEl.ValueKind == JsonValueKind.Array)
                    {
                        for (int i = 0; i < dPacksEl.GetArrayLength(); i = i + 1)
                        {
                            JsonElement item = dPacksEl[i];
                            if (item.ValueKind == JsonValueKind.String)
                            {
                                string gotPack = item.GetString();
                                if (gotPack != null && gotPack.Length > 0)
                                {
                                    defaultPacks.Add(gotPack.Trim());
                                }
                            }
                        }
                    }
                    JsonElement injectEl;
                    if (root.TryGetProperty("defaultInjectList", out injectEl) && injectEl.ValueKind == JsonValueKind.Array)
                    {
                        for (int i = 0; i < injectEl.GetArrayLength(); i++)
                        {
                            JsonElement item = injectEl[i];
                            if (item.ValueKind == JsonValueKind.String)
                            {
                                string got = item.GetString();
                                if (got != null && got.Length > 0)
                                {
                                    // 路径校验——与 cat-config 同规（只接受完整路径；非法不录入）
                                    string trimmed = got.Trim();
                                    if (IsValidInjectPath(trimmed))
                                    {
                                        defaultInjectList.Add(trimmed);
                                    }
                                    else
                                    {
                                        LogStore.Add("CatHome4", 2, "全局默认模板：注入路径被拒绝（非法格式）" + got, "CONFIG");
                                    }
                                }
                            }
                        }
                    }
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            CatDefaultCfgData data = new CatDefaultCfgData();
            data.BaseRole = baseRole;
            data.DefaultPersona = defaultPersona;
            string[] removedDefaultToolNames;
            data.DefaultToolNames = ValidateToolNames(defaultToolNames, out removedDefaultToolNames);
            data.DefaultInjectList = defaultInjectList.ToArray();
            data.DefaultPacks = defaultPacks.ToArray();
            data.DefaultUserInjectIdleOnly = defaultUserInjectIdleOnly;
            SaveCatDefaultCfg(data);
            LogStore.Add("CatHome4", 1, "全局默认模板已保存（注入 " + defaultInjectList.Count.ToString() + " 条）", "CONFIG");
            // A132 剔除出声——被剔名字随响应带回（调用方可见；明细已记日志，不静默）
            return Results.Json(new { ok = true, removedToolNames = removedDefaultToolNames });
        }

        /// <summary>受控根写入输入条目——POST /api/v1/workspace body 解析形态</summary>
        internal sealed class WorkspaceRootInput
        {
            /// <summary>根标识</summary>
            public string Id;

            /// <summary>根路径</summary>
            public string Path;

            /// <summary>是否可写</summary>
            public bool Writable;

            /// <summary>认路注释（≤20 字——给 LLM 的语义提示；超长由宿主截断）</summary>
            public string Note = "";
        }

        /// <summary>
        /// 受控根读取——GET /api/v1/workspace（管理面：roots 编辑面数据源）。
        /// roots 是安全边界——LLM 工具面（config.*）保持只读；本端点仅管理员面（主端口）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>roots JSON</returns>
        internal static IResult HandleWorkspaceGet(HttpContext ctx)
        {
            WorkspaceConfig ws = null;
            DataBox.TryResolve<WorkspaceConfig>(out ws);
            if (ws == null)
            {
                return Results.Json(new { ok = false, error = "工作区配置未绑定" });
            }
            List<object> roots = new List<object>();
            for (int i = 0; i < ws.Roots.Length; i++)
            {
                FixedRootDef rdef = FindFixedRoot(ws.Roots[i].Id);
                roots.Add(new
                {
                    id = ws.Roots[i].Id,
                    path = ws.Roots[i].Path,
                    writable = rdef != null ? rdef.Writable : ws.Roots[i].Writable,
                    note = rdef != null ? rdef.Note : ws.Roots[i].Note,
                    fixedRoot = rdef != null
                });
            }
            return Results.Json(new { ok = true, roots = roots });
        }

        /// <summary>
        /// 全局根池 JSON——供猫配置界面勾选（cat-config GET allRoots 字段；workspace 强制必选）。
        /// </summary>
        /// <returns>根池数组 [{id,path,writable,note,fixedRoot}]——fixedRoot=系统固定命名根（前端渲染锁定用）</returns>
        internal static object[] BuildAllRootsJson()
        {
            WorkspaceConfig ws = null;
            DataBox.TryResolve<WorkspaceConfig>(out ws);
            List<object> roots = new List<object>();
            if (ws != null)
            {
                for (int i = 0; i < ws.Roots.Length; i++)
                {
                    FixedRootDef rdef = FindFixedRoot(ws.Roots[i].Id);
                    roots.Add(new
                    {
                        id = ws.Roots[i].Id,
                        path = ws.Roots[i].Path,
                        writable = rdef != null ? rdef.Writable : ws.Roots[i].Writable,
                        note = rdef != null ? rdef.Note : ws.Roots[i].Note,
                        fixedRoot = rdef != null
                    });
                }
            }
            return roots.ToArray();
        }

        /// <summary>
        /// 猫启用根校验——workspace 强制必选 + 全局池子集；非法 id 剔除、去重、保序。
        /// 语义（收紧·X）：空数组 = 仅常驻系统根（物化为显式清单——与 ResolveCatRootEntries 同源）；非空 = workspace 强制 + 用户子集。
        /// </summary>
        /// <param name="ids">前端提交的启用根 id 数组（空=仅常驻根）</param>
        /// <returns>合法启用根 id 数组（至少含常驻根；池不可用时为空）</returns>
        /// <param name="removed">被剔除的池外根 id 清单（根池中不存在；空 = 无剔除）</param>
        /// <param name="poolUnavailable">全局根池不可用（此时整份白名单按空处理——调用方须出声）</param>
        internal static string[] ValidateEnabledRoots(string[] ids, out string[] removed, out bool poolUnavailable)
        {
            removed = new string[0];
            poolUnavailable = false;
            // 空白名单（未配置/全不勾）= 仅常驻根——物化落盘，消除"未配置"二义（前端已如实渲染）
            if (ids == null || ids.Length == 0)
            {
                string residentOnly = ResolveResidentRootId();
                if (residentOnly.Length == 0)
                {
                    return new string[0];
                }
                return new string[] { residentOnly };
            }
            WorkspaceConfig ws = null;
            DataBox.TryResolve<WorkspaceConfig>(out ws);
            if (ws == null || ws.Roots == null || ws.Roots.Length == 0)
            {
                // A261 降级出声——池不可用时整份白名单会被置空，绝不静默回落（失败必须可见）
                poolUnavailable = true;
                LogStore.Add("CatHome4", 3, "猫配置：全局根池不可用，启用根白名单按空处理（该猫仅常驻根可用）", "CONFIG");
                return new string[0];
            }
            List<string> result = new List<string>();
            List<string> dropped = new List<string>();
            // 全局池合法 id 集合
            List<string> pool = new List<string>();
            for (int i = 0; i < ws.Roots.Length; i++)
            {
                string rootId = ws.Roots[i].Id;
                if (rootId == null || rootId.Length == 0)
                {
                    continue;
                }
                pool.Add(rootId);
            }
            // workspace 强制——用户说必选不可取消；兼容旧配置 id=runtime
            string wsId = "";
            for (int p = 0; p < pool.Count; p++)
            {
                if (string.Equals(pool[p], "workspace", StringComparison.OrdinalIgnoreCase))
                {
                    wsId = pool[p];
                    break;
                }
            }
            if (wsId.Length == 0)
            {
                for (int p = 0; p < pool.Count; p++)
                {
                    if (string.Equals(pool[p], "runtime", StringComparison.OrdinalIgnoreCase))
                    {
                        wsId = pool[p];
                        break;
                    }
                }
            }
            if (wsId.Length > 0)
            {
                result.Add(wsId);
            }
            // 用户选择子集——只保留全局池内 id，去重；池外 id 剔除并收集（A261 出声不静默）
            for (int i = 0; i < ids.Length; i++)
            {
                string id = ids[i];
                if (id == null || id.Length == 0)
                {
                    continue;
                }
                if (string.Equals(id, wsId, StringComparison.OrdinalIgnoreCase))
                {
                    continue;
                }
                bool inPool = false;
                for (int p = 0; p < pool.Count; p++)
                {
                    if (string.Equals(pool[p], id, StringComparison.OrdinalIgnoreCase))
                    {
                        inPool = true;
                        break;
                    }
                }
                bool already = false;
                for (int r = 0; r < result.Count; r++)
                {
                    if (string.Equals(result[r], id, StringComparison.OrdinalIgnoreCase))
                    {
                        already = true;
                        break;
                    }
                }
                if (!inPool)
                {
                    dropped.Add(id);
                }
                else if (!already)
                {
                    result.Add(id);
                }
            }
            if (dropped.Count > 0)
            {
                removed = dropped.ToArray();
                LogStore.Add("CatHome4", 2, "启用根写时剔除 " + dropped.Count.ToString() + " 个池外根 id：" + string.Join(",", dropped.ToArray()) + "（根池中不存在——名单以根池为准）", "CONFIG");
            }
            return result.ToArray();
        }

        /// <summary>
        /// 常驻系统根 id——workspace（旧配置 runtime）；池不可用/无匹配返回空串。
        /// </summary>
        /// <returns>常驻根 id（按池内原大小写）</returns>
        private static string ResolveResidentRootId()
        {
            WorkspaceConfig ws = null;
            DataBox.TryResolve<WorkspaceConfig>(out ws);
            if (ws == null || ws.Roots == null)
            {
                return "";
            }
            string legacy = "";
            for (int i = 0; i < ws.Roots.Length; i++)
            {
                string rootId = ws.Roots[i].Id;
                if (rootId == null || rootId.Length == 0)
                {
                    continue;
                }
                if (string.Equals(rootId, "workspace", StringComparison.OrdinalIgnoreCase))
                {
                    return rootId;
                }
                if (legacy.Length == 0 && string.Equals(rootId, "runtime", StringComparison.OrdinalIgnoreCase))
                {
                    legacy = rootId;
                }
            }
            return legacy;
        }
        /// <summary>
        /// 现存根 path 映射读取——写面存量容留判定用（不可达根原样提交时不拒）；文件缺失/损坏返回空映射（按无容留处理，出声）。
        /// </summary>
        /// <returns>id（归一为小写）→ path 原值</returns>
        private static Dictionary<string, string> LoadExistingRootPaths()
        {
            Dictionary<string, string> map = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
            string wsPath = Path.Combine(_dataRoot, "Data", "config", "workspace.json");
            if (!File.Exists(wsPath))
            {
                return map;
            }
            try
            {
                using (JsonDocument oldDoc = JsonUtil.ParseStrict(File.ReadAllText(wsPath)))
                {
                    JsonElement rootsEl;
                    if (oldDoc.RootElement.TryGetProperty("roots", out rootsEl) && rootsEl.ValueKind == JsonValueKind.Array)
                    {
                        for (int i = 0; i < rootsEl.GetArrayLength(); i = i + 1)
                        {
                            string id = NormalizeRootId(GetJsonString(rootsEl[i], "id"));
                            string path = GetJsonString(rootsEl[i], "path");
                            if (id.Length > 0 && path.Length > 0)
                            {
                                map[id] = path;
                            }
                        }
                    }
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "现存根表读取失败（按无存量容留处理）: " + ex.Message, "CONFIG");
            }
            return map;
        }

        /// <summary>
        /// 受控根写入——POST /api/v1/workspace（body: {roots:[{id,path,writable,note}]}）。
        /// 校验：id 归一（小写字母数字）+ 重名忽略大小写 + path 非空绝对路径 + 目录存在 + 固定命名根齐备且三列一致（缺则拒绝——不静默造根）。
        /// 生效语义：落盘 + 重启生效（roots 是 Bootstrap 一次性读取——低频事件，莎拍板 2026-08-25）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleWorkspacePost(HttpContext ctx)
        {
            string body = await ReadBodyText(ctx);
            List<WorkspaceRootInput> rootsIn = new List<WorkspaceRootInput>();
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    JsonElement rootsEl;
                    if (root.TryGetProperty("roots", out rootsEl) && rootsEl.ValueKind == JsonValueKind.Array)
                    {
                        for (int i = 0; i < rootsEl.GetArrayLength(); i++)
                        {
                            JsonElement item = rootsEl[i];
                            WorkspaceRootInput input = new WorkspaceRootInput();
                            input.Id = GetJsonString(item, "id").Trim();
                            input.Path = GetJsonString(item, "path").Trim();
                            input.Writable = true;
                            JsonElement wEl;
                            if (item.TryGetProperty("writable", out wEl) && wEl.ValueKind == JsonValueKind.False)
                            {
                                input.Writable = false;
                            }
                            input.Note = WorkspaceConfig.TruncateNote(GetJsonString(item, "note"));
                            rootsIn.Add(input);
                        }
                    }
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            // [段1] 归一——id 去空白 + 大写转小写（写面统一小写；读面按小写识别）
            for (int i = 0; i < rootsIn.Count; i = i + 1)
            {
                rootsIn[i].Id = NormalizeRootId(rootsIn[i].Id);
            }
            // [段2a] 现存根 path 映射——存量容留判定（不可达根可原样提交以移除或改路径，不构成改根死锁）
            Dictionary<string, string> existingPaths = LoadExistingRootPaths();
            // [段2] 校验——字符集（小写字母数字）+ 重名（忽略大小写）+ path 绝对且存在（存量容留）+ 固定命名根齐备
            string rootErr = ValidateRootsInput(rootsIn, existingPaths, _dataRoot);
            if (rootErr.Length > 0)
            {
                return Results.Json(new { ok = false, error = rootErr });
            }
            // [段3] 落盘——统一出口 WriteWorkspaceRoots（保留 inject 段；与 db 根自愈同源）
            string wsPath = Path.Combine(_dataRoot, "Data", "config", "workspace.json");
            string writeErr = WriteWorkspaceRoots(wsPath, rootsIn);
            if (writeErr.Length > 0)
            {
                return Results.Json(new { ok = false, error = writeErr });
            }
            LogStore.Add("CatHome4", 1, "工作区已保存：" + rootsIn.Count.ToString() + " 个根（重启生效）", "CONFIG");
            return Results.Json(new { ok = true, roots = rootsIn.Count });
        }

        /// <summary>
        /// catcfg.apply 执行体——主线程泵消费：重读 cat.cfg → 更新注册表字段 + 重裁剪声明面 + apiConfigId 变更 SwapLlmRuntime。
        /// </summary>
        /// <param name="key">猫寻址键（majordomo=默认猫）</param>
        /// <returns>结果文本</returns>
        internal static string HandleCatCfgApply(string key)
        {
            CatCfgData cfg = LoadCatCfg(Path.Combine(_dataRoot, "Data", "sessions", key, "cat.cfg"));
            if (cfg == null)
            {
                return "catcfg.apply | cat.cfg 不存在: " + key;
            }
            string persona = "";
            if (cfg.Persona != null)
            {
                persona = cfg.Persona;
            }
            string[] injectList = cfg.InjectList;
            if (injectList == null)
            {
                injectList = new string[0];
            }
            string toolNames = "";
            if (cfg.ToolNames != null)
            {
                toolNames = cfg.ToolNames;
            }
            bool includePrivileged = IsDefaultCatKey(key);
            ToolSpec[] specs = FilterToolSpecs(ResolveToolNames(toolNames, includePrivileged), includePrivileged);
            // M4e 猫级白名单——配置变更后重建猫文件系统（启用根子集）
            ApplyCatRoots(key);
            if (key == "majordomo")
            {
                // 默认猫——静态面更新（session.new 重注入消费）
                _chatBridge.DefaultPersona = persona;
                _chatBridge.DefaultInjectList = injectList;
                _chatBridge.DefaultToolSpecs = specs;
                // 授权面原始串——实时解析输入（改配置即改内存；design-ch4-tools §三·十一）
                _chatBridge.DefaultToolNames = toolNames;
                _chatBridge.DefaultQqBotId = ResolveQqBotId(cfg);
                _chatBridge.DefaultQqBotEnable = cfg.QqBotEnable;
                _chatBridge.DefaultSession.SetUserInjectIdleOnly(cfg.UserInjectIdleOnly);
                _chatBridge.DefaultSession.SetRoundSummary(ResolveEffectiveRoundSummary("majordomo"));
                _chatBridge.DefaultSession.SetPetKind(ResolveEffectivePetKind("majordomo"));
                // A230 显示名立即刷新（改配置即改内存；空值保持原值）
                if (cfg.DisplayName != null && cfg.DisplayName.Length > 0)
                {
                    _chatBridge.DefaultSession.SetDisplayName(cfg.DisplayName);
                }
                Guid newApi = ResolveApiConfigId(cfg);
                string newEffort = NormalizeReasoningEffort(cfg.ReasoningEffort);
                if (_defaultApiConfigId != newApi || _defaultReasoningEffort != newEffort)
                {
                    _defaultApiConfigId = newApi;
                    _defaultReasoningEffort = newEffort;
                    _chatBridge.DefaultSession.SwapLlmRuntime(new DeepSeekLlmRuntime(_apiStore, newApi, _globalConfig, newEffort));
                    LogStore.Add("CatHome4", 1, "majordomo 配置生效：LLM 端点 " + newApi.ToString("D") + "，思考强度 " + (newEffort.Length > 0 ? newEffort : "跟随全局"), "CONFIG");
                }
                return "catcfg.apply | majordomo | 已生效（前文项新会话生效）";
            }
            CatEntry cat = FindCat(key);
            if (cat == null)
            {
                return "catcfg.apply | 猫不存在: " + key;
            }
            cat.Persona = persona;
            // A230 显示名立即刷新（改配置即改内存；空值保持原值）
            if (cfg.DisplayName != null && cfg.DisplayName.Length > 0)
            {
                cat.DisplayName = cfg.DisplayName;
                cat.Session.SetDisplayName(cfg.DisplayName);
            }
            cat.Type = cfg.Type == null ? "" : cfg.Type;
            cat.InjectList = injectList;
            cat.ToolNames = toolNames;
            cat.ToolSpecs = specs;
            cat.QqBotId = ResolveQqBotId(cfg);
            cat.QqBotEnable = cfg.QqBotEnable;
            cat.UserInjectIdleOnly = cfg.UserInjectIdleOnly;
            cat.Session.SetUserInjectIdleOnly(cfg.UserInjectIdleOnly);
            cat.AppearanceId = ResolveAppearanceId(cfg);
            cat.RoundSummary = ResolveEffectiveRoundSummary(key);
            cat.Session.SetRoundSummary(cat.RoundSummary);
            cat.Session.SetPetKind(ResolveEffectivePetKind(key));
            // M4e 猫级白名单——运行时猫文件系统已在上方 ApplyCatRoots 重建（含本分支）
            Guid newApiId = ResolveApiConfigId(cfg);
            string newCatEffort = NormalizeReasoningEffort(cfg.ReasoningEffort);
            if (cat.ApiConfigId != newApiId || cat.ReasoningEffort != newCatEffort)
            {
                cat.ApiConfigId = newApiId;
                cat.ReasoningEffort = newCatEffort;
                CH_LlmApiConfig apiConfig = new CH_LlmApiConfig();
                if (_apiStore != null)
                {
                    _apiStore.TryGet(newApiId, out apiConfig);
                }
                cat.ApiConfig = apiConfig;
                cat.Session.SwapLlmRuntime(new DeepSeekLlmRuntime(_apiStore, newApiId, _globalConfig, newCatEffort));
                LogStore.Add("CatHome4", 1, "猫「" + cat.DisplayName + "」配置生效：LLM 端点 " + newApiId.ToString("D") + "，思考强度 " + (newCatEffort.Length > 0 ? newCatEffort : "跟随全局"), "CONFIG");
            }
            return "catcfg.apply | " + cat.DisplayName + " | 已生效（前文项新会话生效）";
        }

        /// <summary>
        /// 解析 cat.cfg 的 qqbot 配置身份——缺省 Guid.Empty=未绑定。
        /// </summary>
        /// <param name="cfg">配置数据</param>
        /// <returns>qqbot 配置身份</returns>
        private static Guid ResolveQqBotId(CatCfgData cfg)
        {
            Guid id = Guid.Empty;
            if (cfg != null && cfg.QqBotId != null && cfg.QqBotId.Length > 0)
            {
                Guid parsed;
                if (Guid.TryParse(cfg.QqBotId, out parsed) && parsed != Guid.Empty)
                {
                    id = parsed;
                }
            }
            return id;
        }

        /// <summary>
        /// 解析 cat.cfg 的 API 配置身份——缺省 Guid.Empty=默认端点语义（Cat 可选配置；未配置走默认端点）。
        /// </summary>
        /// <param name="cfg">配置数据</param>
        /// <returns>API 配置身份</returns>
        private static Guid ResolveApiConfigId(CatCfgData cfg)
        {
            Guid id = Guid.Empty;
            if (cfg != null && cfg.ApiConfigId != null && cfg.ApiConfigId.Length > 0)
            {
                Guid parsed;
                if (Guid.TryParse(cfg.ApiConfigId, out parsed) && parsed != Guid.Empty)
                {
                    id = parsed;
                }
            }
            return id;
        }

        /// <summary>
        /// cat.cfg 原子写——按配置数据落盘（M3 端点写面；与 SaveCatCfg(CatEntry) 同源）。
        /// </summary>
        /// <param name="id">会话 ID（majordomo=默认猫）</param>
        /// <param name="data">配置数据（用户配置面——运行态 running/port 不在此落盘）</param>
        private static void SaveCatCfgData(string id, CatCfgData data)
        {
            string dir = Path.Combine(_dataRoot, "Data", "sessions", id);
            string path = Path.Combine(dir, "cat.cfg");
            var payload = new
            {
                id = data.Id,
                displayName = data.DisplayName,
                type = data.Type,
                apiConfigId = data.ApiConfigId,
                persona = data.Persona,
                // M2c 写时校验——序列化前比对内置清单过滤非法名（外部损坏防御：持久化面只落合法名）
                toolNames = ValidateToolNames(data.ToolNames),
                injectList = data.InjectList,
                qqbotId = data.QqBotId,
                qqbotEnable = data.QqBotEnable,
                enabledRoots = data.EnabledRoots,
                packs = data.Packs,
                dbLibs = data.DbLibs,
                userInjectIdleOnly = data.UserInjectIdleOnly,
                appearanceId = data.AppearanceId,
                reasoningEffort = data.ReasoningEffort
            };
            try
            {
                ConfigStore.AtomicWrite(path, JsonUtil.Serialize(payload));
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "cat.cfg 写入失败：" + ex.Message, "CHAT");
            }
        }

        /// <summary>
        /// 读取请求 body 全文。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>body 文本</returns>
        private static async Task<string> ReadBodyText(HttpContext ctx)
        {
            using (StreamReader reader = new StreamReader(ctx.Request.Body))
            {
                return await reader.ReadToEndAsync();
            }
        }

        /// <summary>
        /// 读取 JSON 对象字符串属性——防御式（缺字段返回空串）。
        /// </summary>
        /// <param name="obj">JSON 元素</param>
        /// <param name="prop">属性名</param>
        /// <returns>属性值</returns>
        private static string GetJsonString(JsonElement obj, string prop)
        {
            JsonElement value;
            if (obj.TryGetProperty(prop, out value) && value.ValueKind == JsonValueKind.String)
            {
                string got = value.GetString();
                if (got != null)
                {
                    return got;
                }
            }
            return "";
        }

        /// <summary>
        /// API key 掩码——保留头尾 4 字符（列表展示；长度 ≤8 全掩）。
        /// </summary>
        /// <param name="key">原始 key</param>
        /// <returns>掩码文本</returns>
        private static string MaskApiKey(string key)
        {
            if (key.Length <= 8)
            {
                return "****";
            }
            return key.Substring(0, 4) + "****" + key.Substring(key.Length - 4);
        }
        /// <summary>
        /// majordomo 默认猫 cat.cfg 补建——缺失时按全局默认模板创建（新用户无 cfg 是必然态；模板值 = 初次部署状态权威）。
        /// 已存在不覆盖——尊重用户已保存配置；模板缺失回退空默认（与运行时缺省回退语义一致）。
        /// </summary>
        internal static void EnsureMajordomoCfg()
        {
            string path = Path.Combine(_dataRoot, "Data", "sessions", "majordomo", "cat.cfg");
            if (File.Exists(path))
            {
                return;
            }

            CatDefaultCfgData tpl = LoadCatDefaultCfg();
            CatCfgData data = new CatCfgData();
            data.Id = "majordomo";
            data.DisplayName = "majordomo";
            data.Running = false;
            data.Port = 0;
            data.ApiConfigId = "";
            data.QqBotId = "";
            data.QqBotEnable = false;
            data.EnabledRoots = null;
            if (tpl != null)
            {
                data.Persona = tpl.DefaultPersona != null ? tpl.DefaultPersona : "";
                data.ToolNames = tpl.DefaultToolNames != null ? tpl.DefaultToolNames : "";
                data.InjectList = tpl.DefaultInjectList != null ? tpl.DefaultInjectList : new string[0];
            }
            else
            {
                data.Persona = "";
                data.ToolNames = "";
                data.InjectList = new string[0];
            }

            if (tpl != null)
            {
                data.UserInjectIdleOnly = tpl.DefaultUserInjectIdleOnly;
            }
            SaveCatCfgData("majordomo", data);
            LogStore.Add("CatHome4", 1, "majordomo cat.cfg 缺失——已按默认模板补建", "CONFIG");
        }
    }
}
