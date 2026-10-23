using System;
using System.Collections.Generic;
using System.IO;
using System.Net.Http;
using System.Net.ServerSentEvents;
using System.Runtime.CompilerServices;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Mau.Runtime;

namespace Mau.Providers
{
    /// <summary>
    /// DeepSeek 非流式/流式适配器——内建最小实现（OpenAI 兼容 /chat/completions）。
    /// 非流式：宿主构造时注入 baseUrl/apiKey/model；流式：ConfigStore 拉起（P4 配置项机制）。
    /// 接入主入口（构造/ChatStream/配置实时读取）在本文件；
    /// SSE 解析面分部在 DeepSeekLlmRuntime.Sse.cs；wire 序列化面分部在 DeepSeekLlmRuntime.Serialize.cs（P7b partial 拆分）。
    /// </summary>
    public sealed partial class DeepSeekLlmRuntime : ILlmRuntime
    {
        /// <summary>
        /// HTTP 客户端——每实例独立（超时互不干扰）
        /// </summary>
        private readonly HttpClient _client;

        /// <summary>
        /// LLM API 配置池——endpoint/model/key 实时来源（M1c 每猫独立；M2e 立即生效语义）
        /// </summary>
        private readonly CH_LlmApiConfigStore? _apiStore;

        /// <summary>
        /// API 配置身份——按猫路由；Guid.Empty=默认端点语义（每次调用实时解析默认配置）
        /// </summary>
        private readonly Guid _apiConfigId;

        /// <summary>
        /// 全局配置——thinking/reasoning_effort 键段实时读取（可 null）
        /// </summary>
        private readonly ConfigStore? _globalConfig;

        /// <summary>
        /// 每猫思考强度覆盖——A234（空=跟随全局 llm.reasoning_effort；low/high/max）
        /// </summary>
        private readonly string _reasoningEffort;

        /// <summary>
        /// 默认兜底模型名——官方现役 v4-pro / v4-flash（deepseek-chat 已废弃）
        /// </summary>
        private const string FallbackModel = "deepseek-v4-flash";

        /// <summary>
        /// 默认输出上限——实测 Foldin 端点接受 131072（DeepSeek 官方文档口径 8192）；显式传入以脱离上游默认（实测默认 1024）
        /// </summary>
        private const int DefaultMaxTokens = 131072;

        /// <summary>
        /// 输出上限合法上界——取本端点实测可接受上限 131072；超界回落默认
        /// </summary>
        private const int MaxMaxTokens = 131072;

        /// <summary>
        /// 非法 max_tokens 已告警标记——避免每请求刷屏（静态：配置非法时进程内只告警一次）
        /// </summary>
        private static bool _maxTokensWarned;

        /// <summary>
        /// 请求重试上限——S2 规格：同端点重试 3 次（共 4 次尝试），失败后中止（无配置池降级）
        /// </summary>
        private const int MaxRetries = 3;

        /// <summary>
        /// 重试退避延迟——递增（500ms × (attempt+1)，上限 2s）——限流场景留喘息
        /// </summary>
        /// <param name="attempt">已尝试次数（0 起）</param>
        /// <returns>延迟毫秒数</returns>
        private static int RetryDelayMs(int attempt)
        {
            int ms = 500 * (attempt + 1);
            if (ms > 2000)
            {
                ms = 2000;
            }
            return ms;
        }

        /// <summary>
        /// 从 LLM API 配置池建立适配器——endpoint/model/key 实时从 Store 取（M1c 每猫独立 Runtime）。
        /// 拉取优先级：API 配置池（llm-api.json + secrets）→ Mau 全局环境变量 MAU_LLM_* → 兼容/默认。
        /// key 优先级：secrets → MAU_LLM_API_KEY → DEEPSEEK_API_KEY（兼容）→ 空串（ERR 路径）。
        /// thinking/reasoning_effort 为全局参数——globalConfig 实时读取（llm.thinking/llm.reasoning_effort）。
        /// </summary>
        /// <param name="apiStore">LLM API 配置池</param>
        /// <param name="apiConfigId">API 配置身份</param>
        /// <param name="globalConfig">全局配置存储（可 null——默认值）</param>
        /// <param name="reasoningEffort">每猫思考强度覆盖（A234；空=跟随全局 llm.reasoning_effort；low/high/max）</param>
        public DeepSeekLlmRuntime(CH_LlmApiConfigStore apiStore,
            Guid apiConfigId, ConfigStore? globalConfig, string reasoningEffort = "")
        {
            // [段1] 配置源持有——运行期实时读取（P7 热载拍板：配置以本地持久化为准，构造期快照退役）
            _apiStore = apiStore;
            _apiConfigId = apiConfigId;
            _globalConfig = globalConfig;
            _reasoningEffort = reasoningEffort == null ? "" : reasoningEffort;
            _client = new HttpClient();
            _client.Timeout = TimeSpan.FromSeconds(60);
        }
        /// <summary>
        /// 流式对话完成——消息序列 + 工具定义 → 事件流（P5：OpenAI 兼容 tool_calls）。
        /// 帧内序：Reasoning → Text（同帧两事件）；ToolCallsStart（首个 tool_calls 增量帧）→ ToolCalls（流末聚合）→ Usage → Done；Retrying 退避前 / RetryResume 重发前；错误 → Error。
        /// 注意：C# 迭代器禁止 try-catch 内 yield——网络层错误用 catch 赋值 + catch 后 yield 模式。
        /// </summary>
        /// <param name="messages">完整消息序列（system/user/assistant/tool 多 role）</param>
        /// <param name="tools">工具定义数组（可为空——纯对话）</param>
        /// <param name="userId">缓存隔离键——请求体 user_id + x-opencode-session 头（来源 llm.cache_isolation：cat=猫键 / session=会话键 / off=空；空=不携带 user_id，头回落默认）</param>
        /// <param name="ct">取消令牌</param>
        /// <returns>流式事件序列</returns>
        public async IAsyncEnumerable<LlmStreamEvent> ChatStream(LlmMessage[] messages, ToolSpec[] tools, string userId = "", [EnumeratorCancellation] CancellationToken ct = default)
        {
            // [段1] 构造流式请求体并发送（ResponseHeadersRead——流式读取）
            // S2 重试——TRANSPORT/429/5xx 重试最多 MaxRetries 次（共 MaxRetries+1 次尝试）；4xx 不重试；流中断不重试
            string body = BuildChatRequestBody(messages, tools, userId);
            int retryCount = 0;
            while (true)
            {
                using (HttpRequestMessage request = new HttpRequestMessage(HttpMethod.Post, GetBaseUrl()))
                {
                    request.Headers.TryAddWithoutValidation("Authorization", "Bearer " + GetApiKey());
                    request.Headers.TryAddWithoutValidation("Accept", "text/event-stream");
                    // opencode.ai zen/go 网关会话路由头——缺失拒绝请求（实测 2026-09-07；userId 即缓存隔离键——llm.cache_isolation 语义对齐）
                    request.Headers.TryAddWithoutValidation("x-opencode-session", "cat-home4-" + (userId.Length > 0 ? userId : "default"));
                    request.Content = new StringContent(body, Encoding.UTF8, "application/json");
                    HttpResponseMessage? response = null;
                    string netError = "";
                    try
                    {
                        response = await _client.SendAsync(request, HttpCompletionOption.ResponseHeadersRead, ct);
                    }
                    catch (Exception ex)
                    {
                        // P6 中止——取消不是传输错误：冒泡（取消不重试——重试分支 Task.Delay(ct) 也会立即取消）
                        if (ex is System.OperationCanceledException)
                        {
                            throw;
                        }
                        netError = "ERR|TRANSPORT|" + ex.GetType().Name + "|" + ex.Message;
                    }

                    if (netError.Length > 0)
                    {
                        if (retryCount < MaxRetries)
                        {
                            retryCount = retryCount + 1;
                            LogStore.Add("LLM", 2, "LLM 请求传输失败，自动重试（" + retryCount.ToString() + "/" + MaxRetries.ToString() + "）：" + TrimText(netError, 200), "LLM");
                            yield return new LlmStreamEvent(LlmStreamKind.Retrying, "RETRY|" + retryCount.ToString() + "/" + MaxRetries.ToString() + "|" + TrimText(netError, 200));
                            await Task.Delay(RetryDelayMs(retryCount - 1), ct);
                            yield return new LlmStreamEvent(LlmStreamKind.RetryResume, "");
                            continue;
                        }
                        yield return new LlmStreamEvent(LlmStreamKind.Error, netError);
                        yield break;
                    }

                    using (response)
                    {
                        // [段2] HTTP 层失败——错误 JSON 双形态解析（error.type/message 兜底，HTTP 码不可作唯一判据）
                        if (response == null)
                        {
                            yield return new LlmStreamEvent(LlmStreamKind.Error, "ERR|TRANSPORT|空响应");
                            yield break;
                        }

                        if (!response.IsSuccessStatusCode)
                        {
                            int statusCode = (int)response.StatusCode;
                            // S2 重试——429/5xx 可重试（未产出业务事件，安全重发）；4xx 参数/鉴权错误不重试
                            bool retryable = statusCode == 429 || statusCode >= 500;
                            if (retryable && retryCount < MaxRetries)
                            {
                                retryCount = retryCount + 1;
                                LogStore.Add("LLM", 2, "LLM 请求返回 HTTP " + statusCode.ToString() + "，自动重试（" + retryCount.ToString() + "/" + MaxRetries.ToString() + "）", "LLM");
                                yield return new LlmStreamEvent(LlmStreamKind.Retrying, "RETRY|" + retryCount.ToString() + "/" + MaxRetries.ToString() + "|HTTP " + statusCode.ToString());
                                await Task.Delay(RetryDelayMs(retryCount - 1), ct);
                                yield return new LlmStreamEvent(LlmStreamKind.RetryResume, "");
                                continue;
                            }
                            string? raw = "";
                            string readError = "";
                            try
                            {
                                raw = await response.Content.ReadAsStringAsync(ct);
                            }
                            catch (Exception ex)
                            {
                                // P6 中止——取消不是传输错误：冒泡（取消不重试）
                                if (ex is System.OperationCanceledException)
                                {
                                    throw;
                                }
                                readError = "ERR|TRANSPORT|" + ex.GetType().Name + "|" + ex.Message;
                            }

                            if (readError.Length > 0)
                            {
                                yield return new LlmStreamEvent(LlmStreamKind.Error, readError);
                                yield break;
                            }

                            string rawText;
                            if (raw == null)
                            {
                                rawText = "";
                            }
                            else
                            {
                                rawText = raw;
                            }

                            yield return new LlmStreamEvent(LlmStreamKind.Error, ParseErrorText(statusCode, rawText));
                            yield break;
                        }

                        // [段3] SSE 解析——官方 SseParser（注释行/多行 data/空行分隔自动处理）
                        Stream? stream = null;
                        string streamError = "";
                        try
                        {
                            stream = await response.Content.ReadAsStreamAsync(ct);
                        }
                        catch (Exception ex)
                        {
                            // P6 中止——取消不是传输错误：冒泡（取消不重试——读流失败重试分支同样会被 Task.Delay(ct) 立即取消）
                            if (ex is System.OperationCanceledException)
                            {
                                throw;
                            }
                            streamError = "ERR|TRANSPORT|" + ex.GetType().Name + "|" + ex.Message;
                        }

                        if (streamError.Length > 0)
                        {
                            // S2 重试——读流失败（业务事件未产出，安全重发）
                            if (retryCount < MaxRetries)
                            {
                                retryCount = retryCount + 1;
                                LogStore.Add("LLM", 2, "LLM 读取响应流失败，自动重试（" + retryCount.ToString() + "/" + MaxRetries.ToString() + "）：" + TrimText(streamError, 200), "LLM");
                                yield return new LlmStreamEvent(LlmStreamKind.Retrying, "RETRY|" + retryCount.ToString() + "/" + MaxRetries.ToString() + "|" + TrimText(streamError, 200));
                                await Task.Delay(RetryDelayMs(retryCount - 1), ct);
                                yield return new LlmStreamEvent(LlmStreamKind.RetryResume, "");
                                continue;
                            }
                            yield return new LlmStreamEvent(LlmStreamKind.Error, streamError);
                            yield break;
                        }

                        SseParser<string> parser = SseParser.Create(stream!);
                        // [段4] translate——独立迭代器（零 catch：所有错误事件化；tool_calls 聚合在 translate 内）
                        // 流中断（STREAM_CLOSED）不重试——已产出业务事件无法回收（yield 不可撤销）；事件化给上层续传
                        await foreach (LlmStreamEvent ev in TranslateSse(parser, ct))
                        {
                            yield return ev;
                        }
                        yield break;
                    }
                }
            }
        }

        /// <summary>
        /// 实时读取完整端点——API 配置池 endpoint → MAU_LLM_BASE_URL（base 格式兼容拼接）→ 空串（每次调用取当前值）
        /// </summary>
        /// <returns>完整 API 端点（含 /chat/completions）</returns>
        private string GetBaseUrl()
        {
            if (_apiStore != null)
            {
                CH_LlmApiConfig? config = ResolveTarget();
                if (config != null && config.Endpoint.Length > 0)
                {
                    return config.Endpoint;
                }
            }
            string? env = Environment.GetEnvironmentVariable("MAU_LLM_BASE_URL");
            if (!string.IsNullOrEmpty(env))
            {
                // 兼容两种格式：完整端点原样 / 旧 base 格式拼接
                if (env.EndsWith("/chat/completions", StringComparison.Ordinal))
                {
                    return env;
                }
                return env.TrimEnd('/') + "/chat/completions";
            }
            return "";
        }

        /// <summary>
        /// 实时读取 API 密钥——secrets → MAU_LLM_API_KEY → DEEPSEEK_API_KEY（兼容）→ 空串
        /// </summary>
        /// <returns>API 密钥</returns>
        private string GetApiKey()
        {
            if (_apiStore != null)
            {
                CH_LlmApiConfig? config = ResolveTarget();
                if (config != null)
                {
                    string secret = _apiStore.GetSecret(config.ApiConfigId);
                    if (secret.Length > 0)
                    {
                        return secret;
                    }
                }
            }
            string? env = Environment.GetEnvironmentVariable("MAU_LLM_API_KEY");
            if (!string.IsNullOrEmpty(env))
            {
                return env;
            }
            env = Environment.GetEnvironmentVariable("DEEPSEEK_API_KEY");
            if (!string.IsNullOrEmpty(env))
            {
                return env;
            }
            return "";
        }

        /// <summary>
        /// 实时读取模型名——API 配置池 defaultModel → 兜底模型
        /// </summary>
        /// <returns>模型名</returns>
        private string GetModel()
        {
            if (_apiStore != null)
            {
                CH_LlmApiConfig? config = ResolveTarget();
                if (config != null && config.DefaultModel.Length > 0)
                {
                    return config.DefaultModel;
                }
            }
            return FallbackModel;
        }

        /// <summary>
        /// 解析目标配置——Guid.Empty=默认端点语义（每次调用实时 ResolveDefault——默认切换立即生效）；非空=显式身份 TryGet。
        /// </summary>
        /// <returns>目标配置；未命中 null</returns>
        private CH_LlmApiConfig? ResolveTarget()
        {
            if (_apiConfigId == Guid.Empty)
            {
                return _apiStore!.ResolveDefault();
            }
            CH_LlmApiConfig config;
            if (_apiStore!.TryGet(_apiConfigId, out config))
            {
                return config;
            }
            return null;
        }

        /// <summary>
        /// 实时读取思考模式开关——全局配置 llm.thinking → MAU_LLM_THINKING → 默认 enabled
        /// </summary>
        /// <returns>true=思考模式</returns>
        private bool GetThinkingEnabled()
        {
            string value = "";
            if (_globalConfig != null)
            {
                value = _globalConfig.Get("llm.thinking", "");
            }
            if (value.Length == 0)
            {
                string? env = Environment.GetEnvironmentVariable("MAU_LLM_THINKING");
                if (!string.IsNullOrEmpty(env))
                {
                    value = env;
                }
            }
            if (value.Length == 0)
            {
                value = "enabled";
            }
            // 大小写/空白归一——"ENABLED" 亦视为开启（R4-P3-05）
            return value.Trim().ToLowerInvariant() == "enabled";
        }

        /// <summary>
        /// 实时读取推理强度——全局配置 llm.reasoning_effort → MAU_LLM_REASONING_EFFORT → 默认 high
        /// </summary>
        /// <returns>推理强度 low/high/max</returns>
        private string GetReasoningEffort()
        {
            // A234——每猫覆盖优先（非空且合法即用；否则回落全局三层）
            if (_reasoningEffort.Length > 0)
            {
                string catNorm = _reasoningEffort.Trim().ToLowerInvariant();
                if (catNorm == "low" || catNorm == "high" || catNorm == "max")
                {
                    return catNorm;
                }
            }
            string value = "";
            if (_globalConfig != null)
            {
                value = _globalConfig.Get("llm.reasoning_effort", "");
            }
            if (value.Length == 0)
            {
                string? env = Environment.GetEnvironmentVariable("MAU_LLM_REASONING_EFFORT");
                if (!string.IsNullOrEmpty(env))
                {
                    value = env;
                }
            }
            // 白名单校验——low/high/max 之外一律回退 high（R4-P3-06：防上游协议拒绝）
            string norm = value.Trim().ToLowerInvariant();
            if (norm == "low" || norm == "high" || norm == "max")
            {
                return norm;
            }
            return "high";
        }

        /// <summary>
        /// 实时读取单次输出上限——全局配置 llm.max_tokens → MAU_LLM_MAX_TOKENS → 默认 131072。
        /// 显式传入动机：不带 max_tokens 时上游按自家默认截断（实测 Foldin 默认 1024，回复正文与工具参数被切断）。
        /// </summary>
        /// <returns>max_tokens（有效区间 1..131072；空/非法回落默认，非法值进程内告警一次）</returns>
        private int GetMaxTokens()
        {
            string value = "";
            if (_globalConfig != null)
            {
                value = _globalConfig.Get("llm.max_tokens", "");
            }
            if (value.Length == 0)
            {
                string? env = Environment.GetEnvironmentVariable("MAU_LLM_MAX_TOKENS");
                if (!string.IsNullOrEmpty(env))
                {
                    value = env;
                }
            }
            string norm = value.Trim();
            if (norm.Length == 0)
            {
                return DefaultMaxTokens;
            }
            int parsed;
            if (int.TryParse(norm, out parsed) && parsed > 0 && parsed <= MaxMaxTokens)
            {
                return parsed;
            }
            if (!_maxTokensWarned)
            {
                _maxTokensWarned = true;
                LogStore.Add("LLM", 2, "llm.max_tokens 非法（" + norm + "）——回落 " + DefaultMaxTokens.ToString(), "LLM");
            }
            return DefaultMaxTokens;
        }
    }
}