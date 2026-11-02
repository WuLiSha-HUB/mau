// ═══════════════════════════════════════════════════
// 积木: llm.simple_request
// ID:   BRIK-LLM-007
// 类别: LLM
// 作用: 一次性 LLM 请求启动器——后台做一次 OpenAI 兼容非流式调用（自带 endpoint/apikey/model），
//       结果写段盒 llm_simple_reply:<requestId>（文本或 ERR|）+ llm_simple_done:<requestId>（"1"=完成）；主线程立即返回
// 依赖: 无
// 引用: Mau.Runtime（DataBox/LogStore/JsonUtil）
// 原理: 主线程入口捕获 requestId（ThreadStatic 不跨后台线程）→ Task.Run 后台 HTTP POST → 解析 choices[0].message.content
// 盒子: llm_simple_reply:<requestId>(完整文本或 ERR|) / llm_simple_done:<requestId>("1"=完成)——requestId 取 OA officeId（全局唯一；B1 豁免）
// 常用: LLMSimpleRequest 服务语料 T_Call——一次性请求执行体
// 注意: 后台线程写 DataBox（scope 级并发安全）；HTTP 非流式（stream=false）
// 注意: 参数缺失（endpoint/apikey/model）→ 直接落失败盒 + 返回 false（失败可见；语料侧 ready 探测走失败回执）
// ═══════════════════════════════════════════════════
using System;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using System.Threading;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// LLM 积木——llm.simple_request 一次性 LLM 请求启动器（后台 HTTP + 段盒桥）。
    /// </summary>
    public static class LlmSimpleRequestBrick
    {
        /// <summary>
        /// HTTP 客户端——静态复用（超时按每次请求 CTS 控制）
        /// </summary>
        private static readonly HttpClient _client = new HttpClient();

        /// <summary>
        /// 请求超时毫秒——一次性请求上限
        /// </summary>
        private const int TimeoutMs = 120000;

        /// <summary>
        /// 启动一次性 LLM 请求——后台 HTTP 调用；立即返回（开始信号）
        /// </summary>
        /// <param name="endpoint">LLM 端点（OpenAI 兼容 base_url 或完整 /chat/completions）</param>
        /// <param name="apikey">密钥</param>
        /// <param name="model">模型名</param>
        /// <param name="system">系统提示词</param>
        /// <param name="content">内容</param>
        /// <param name="requestId">请求标识（取 OA officeId——全局唯一）</param>
        /// <returns>true=已受理并启动；false=参数非法（失败盒已落）</returns>
        public static bool SimpleRequest(string endpoint, string apikey, string model, string system, string content, long requestId)
        {
            if (requestId <= 0)
            {
                return false;
            }
            string seg = requestId.ToString();
            if (endpoint == null || endpoint.Length == 0 || apikey == null || apikey.Length == 0 || model == null || model.Length == 0)
            {
                string badArgs = "ERR|BAD_ARGS|一次性 LLM 请求缺 endpoint/apikey/model";
                DataBox.Set<string>("global", "llm_simple_reply:" + seg, badArgs);
                DataBox.Set<string>("global", "llm_simple_done:" + seg, "1");
                LogStore.Add("LLM", 2, badArgs + "（requestId=" + seg + "）", "LLM");
                return false;
            }
            // 启动清理——本请求段盒基线干净（reply 清空 + done 清空）
            DataBox.Set<string>("global", "llm_simple_reply:" + seg, "");
            DataBox.Set<string>("global", "llm_simple_done:" + seg, "");
            // 后台发起（fire-and-forget——内部全 try-catch 兜底；lambda 内返回值显式丢弃）
            System.Threading.Tasks.Task.Run(delegate
            {
                _ = DoRequest(endpoint, apikey, model, system, content, requestId);
            });
            return true;
        }

        /// <summary>
        /// 后台执行——HTTP POST → 解析 → 段盒落盘（完成标志置位）
        /// </summary>
        /// <param name="endpoint">端点</param>
        /// <param name="apikey">密钥</param>
        /// <param name="model">模型名</param>
        /// <param name="system">系统提示词</param>
        /// <param name="content">内容</param>
        /// <param name="requestId">请求标识</param>
        private static async System.Threading.Tasks.Task DoRequest(string endpoint, string apikey, string model, string system, string content, long requestId)
        {
            string seg = requestId.ToString();
            try
            {
                string url = BuildUrl(endpoint);
                string body = BuildBody(model, system, content);
                string text;
                CancellationTokenSource cts = new CancellationTokenSource(TimeoutMs);
                try
                {
                    HttpRequestMessage request = new HttpRequestMessage(HttpMethod.Post, url);
                    try
                    {
                        request.Headers.TryAddWithoutValidation("Authorization", "Bearer " + apikey);
                        // opencode 系网关会话路由头——缺失即 400（MissingSessionID；实测 2026-10-13）。服务为一次性请求，用固定命名空间即可
                        request.Headers.TryAddWithoutValidation("x-opencode-session", "cat-home4-simple");
                        request.Content = new StringContent(body, Encoding.UTF8, "application/json");
                        HttpResponseMessage response = await _client.SendAsync(request, cts.Token);
                        try
                        {
                            string json = await response.Content.ReadAsStringAsync(cts.Token);
                            if (!response.IsSuccessStatusCode)
                            {
                                text = "ERR|HTTP_" + ((int)response.StatusCode).ToString() + "|" + TrimText(json, 300);
                            }
                            else
                            {
                                text = ParseText(json);
                            }
                        }
                        finally
                        {
                            response.Dispose();
                        }
                    }
                    finally
                    {
                        request.Dispose();
                    }
                }
                finally
                {
                    cts.Dispose();
                }
                DataBox.Set<string>("global", "llm_simple_reply:" + seg, text);
                DataBox.Set<string>("global", "llm_simple_done:" + seg, "1");
                LogStore.Add("LLM", 0, "一次性请求完成：requestId=" + seg + " · " + text.Length.ToString() + " 字符", "LLM");
            }
            catch (Exception ex)
            {
                DataBox.Set<string>("global", "llm_simple_reply:" + seg, "ERR|" + ex.GetType().Name + "|" + ex.Message);
                DataBox.Set<string>("global", "llm_simple_done:" + seg, "1");
                LogStore.Add("LLM", 3, "一次性请求异常（requestId=" + seg + " · " + ex.GetType().Name + "）：" + TrimText(ex.Message, 200), "LLM");
            }
        }

        /// <summary>
        /// 构造请求 URL——端点含 /chat/completions 原样；否则补 /chat/completions（base_url 形态）
        /// </summary>
        /// <param name="endpoint">端点</param>
        /// <returns>完整请求 URL</returns>
        private static string BuildUrl(string endpoint)
        {
            string url = endpoint.Trim();
            if (url.IndexOf("/chat/completions", StringComparison.OrdinalIgnoreCase) >= 0)
            {
                return url;
            }
            if (url.EndsWith("/", StringComparison.Ordinal))
            {
                return url + "chat/completions";
            }
            return url + "/chat/completions";
        }

        /// <summary>
        /// 构造请求体——OpenAI 兼容 chat/completions（system + user 两条消息；stream=false）
        /// </summary>
        /// <param name="model">模型名</param>
        /// <param name="system">系统提示词</param>
        /// <param name="content">内容</param>
        /// <returns>请求体 JSON</returns>
        private static string BuildBody(string model, string system, string content)
        {
            StringBuilder sb = new StringBuilder();
            sb.Append("{\"model\":");
            sb.Append(JsonUtil.Str(model));
            sb.Append(",\"stream\":false,\"messages\":[{\"role\":\"system\",\"content\":");
            sb.Append(JsonUtil.Str(system == null ? "" : system));
            sb.Append("},{\"role\":\"user\",\"content\":");
            sb.Append(JsonUtil.Str(content == null ? "" : content));
            sb.Append("}]}");
            return sb.ToString();
        }

        /// <summary>
        /// 解析响应——choices[0].message.content；失败返回 ERR| 文本（空内容亦为失败：ERR|EMPTY_CONTENT）
        /// </summary>
        /// <param name="json">响应体</param>
        /// <returns>生成文本或 ERR| 错误</returns>
        private static string ParseText(string json)
        {
            JsonDocument? doc = JsonUtil.Parse(json, "llm.simple_request");
            if (doc == null)
            {
                return "ERR|PARSE|响应 JSON 解析失败";
            }
            try
            {
                JsonElement root = doc.RootElement;
                if (root.ValueKind != JsonValueKind.Object)
                {
                    return "ERR|EMPTY_RESULT|响应非对象";
                }
                JsonElement choices;
                if (!root.TryGetProperty("choices", out choices) || choices.ValueKind != JsonValueKind.Array || choices.GetArrayLength() == 0)
                {
                    return "ERR|EMPTY_RESULT|响应无 choices";
                }
                JsonElement message;
                if (!choices[0].TryGetProperty("message", out message) || message.ValueKind != JsonValueKind.Object)
                {
                    return "ERR|EMPTY_RESULT|响应无 message";
                }
                string content = JsonUtil.GetStr(message, "content", "");
                if (content.Length == 0)
                {
                    // A287（2026-10-22）——空内容不是有效回执：原样落空串会让调用方按「未就绪」无限轮询
                    return "ERR|EMPTY_CONTENT|模型返回空内容";
                }
                return content;
            }
            finally
            {
                doc.Dispose();
            }
        }

        /// <summary>
        /// 截断日志文本——防刷屏
        /// </summary>
        /// <param name="text">原文</param>
        /// <param name="max">上限</param>
        /// <returns>截断文本</returns>
        private static string TrimText(string text, int max)
        {
            if (text == null)
            {
                return "";
            }
            if (text.Length <= max)
            {
                return text;
            }
            return text.Substring(0, max) + "...";
        }
    }
}
// #MAU_CHECKSUM:SHA256:34B0D1AF31A38FAD8FA2E06D24C75C6F541130EFA75554634995A4BD1F4ABA62
