using System;
using System.Collections.Generic;
using System.Net;
using System.Net.Http;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Http;
using Mau.Runtime;
using CH4;

namespace CatHome4.Admin
{
    /// <summary>
    /// Admin 域对话页模型面分部——对话页顶栏 [API:端点名[模型]] 标签的模型下拉数据源 + 会话级临时模型写入。
    /// 语义（2026-11-04 · Rushiera 定）：模型选择**仅会话页有效**——不回写端点池的 defaultModel；
    /// 端点池只在「配置页 → LLM API 池」改默认模型，两条面互不干扰。
    /// 失败可见：探针失败即回执带 error（前端出声，不静默给空列表）。
    /// </summary>
    internal static partial class AdminService
    {
        /// <summary>模型清单探针超时（秒）——与配置页三探同量级。</summary>
        private const int ApiModelListTimeoutSeconds = 15;

        /// <summary>
        /// 对话页模型清单 JSON——GET /api/v1/api-models 数据源。
        /// 目标站 = 当前生效端点（角色决定：备用态走备用站）；清单由上游 GET /models 实时探得。
        /// </summary>
        /// <param name="session">目标会话</param>
        /// <returns>JSON（{ok, endpointName, current, models:[名称]} / {ok=false, error}）</returns>
        internal static async Task<string> BuildApiModelsJsonAsync(ChatSession session)
        {
            if (session == null)
            {
                return JsonUtil.Serialize(new { ok = false, error = "会话未接线" });
            }
            Guid id = session.GetEffectiveApiConfigId();
            if (id == Guid.Empty)
            {
                return JsonUtil.Serialize(new { ok = false, error = "当前生效端点未解析（池未绑定或无端点）" });
            }
            CH_LlmApiConfigStore store = null;
            DataBox.TryResolve<CH_LlmApiConfigStore>(out store);
            if (store == null)
            {
                return JsonUtil.Serialize(new { ok = false, error = "配置池未绑定" });
            }
            CH_LlmApiConfig config;
            if (!store.TryGet(id, out config))
            {
                return JsonUtil.Serialize(new { ok = false, error = "端点配置不存在" });
            }
            string modelsUrl = "";
            string siteOrigin = "";
            DeriveProbeUrls(config.Endpoint, out modelsUrl, out siteOrigin);
            List<string> models = await ProbeModelIdsAsync(modelsUrl, store.GetSecret(id)).ConfigureAwait(false);
            if (models == null)
            {
                return JsonUtil.Serialize(new { ok = false, error = "模型清单探取失败（上游无响应或格式不符）" });
            }
            // 排序口径（2026-11-04 · Rushiera 定）：**不排序**——沿用上游 GET /models 返回顺序（端点自己的排列即用户预期）
            return JsonUtil.Serialize(new
            {
                ok = true,
                apiConfigId = id.ToString("D"),
                endpointName = config.DisplayName,
                current = config.DefaultModel,
                models = models
            });
        }

        /// <summary>
        /// 会话级临时模型写入——POST /api/v1/api-model 数据源。
        /// 空串 = 清空覆盖（跟随端点默认模型）；不回写端点池配置。
        /// </summary>
        /// <param name="session">目标会话</param>
        /// <param name="model">模型名（空串=清空）</param>
        /// <returns>回执 JSON（{ok, model}）</returns>
        internal static string SetApiModelJson(ChatSession session, string model)
        {
            if (session == null)
            {
                return JsonUtil.Serialize(new { ok = false, error = "会话未接线" });
            }
            string value = session.SetModelOverride(model);
            LogStore.Add("CatHome4", 1, "对话页临时模型：" + session.Id + " → " + (value.Length > 0 ? value : "跟随端点默认"), "LLM");
            return JsonUtil.Serialize(new { ok = true, model = value });
        }

        /// <summary>
        /// 模型清单探针——GET {base}/models（带 Bearer Key）。返回模型名清单；失败返回 null（不抛）。
        /// </summary>
        /// <param name="url">模型清单 URL</param>
        /// <param name="apiKey">API Key（空则不带头）</param>
        /// <returns>模型名清单；失败 null</returns>
        private static async Task<List<string>> ProbeModelIdsAsync(string url, string apiKey)
        {
            if (url == null || url.Length == 0)
            {
                return null;
            }
            string body = "";
            try
            {
                using (HttpRequestMessage request = new HttpRequestMessage(HttpMethod.Get, url))
                {
                    if (apiKey != null && apiKey.Length > 0)
                    {
                        request.Headers.TryAddWithoutValidation("Authorization", "Bearer " + apiKey);
                    }
                    using (CancellationTokenSource cts = new CancellationTokenSource(TimeSpan.FromSeconds(ApiModelListTimeoutSeconds)))
                    {
                        using (HttpResponseMessage response = await LlmProbeClient.SendAsync(request, cts.Token).ConfigureAwait(false))
                        {
                            if (response.StatusCode != HttpStatusCode.OK)
                            {
                                return null;
                            }
                            body = await response.Content.ReadAsStringAsync().ConfigureAwait(false);
                        }
                    }
                }
            }
            catch (Exception)
            {
                return null;
            }
            List<string> names = new List<string>();
            try
            {
                using (JsonDocument doc = JsonDocument.Parse(body))
                {
                    JsonElement root = doc.RootElement;
                    JsonElement data;
                    if (!root.TryGetProperty("data", out data) || data.ValueKind != JsonValueKind.Array)
                    {
                        return null;
                    }
                    foreach (JsonElement item in data.EnumerateArray())
                    {
                        string id = "";
                        if (item.ValueKind == JsonValueKind.Object)
                        {
                            JsonElement idEl;
                            if (item.TryGetProperty("id", out idEl) && idEl.ValueKind == JsonValueKind.String)
                            {
                                id = idEl.GetString() ?? "";
                            }
                        }
                        if (id.Length > 0)
                        {
                            names.Add(id);
                        }
                    }
                }
            }
            catch (Exception)
            {
                return null;
            }
            if (names.Count == 0)
            {
                return null;
            }
            return names;
        }
    }
}
