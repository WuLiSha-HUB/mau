using System;
using System.Collections.Generic;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using Mau.Providers;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// Program 探针分部——LLM 返回体探针（方案 B：宿主 CLI 诊断能力，2026-09-11）。
    /// 独立通道：不启动宿主/不占 8080/不载语料——配置池直读 + 单次最小流式请求 + SseFrameProbe 结构摘要。
    /// 用途：API 供应商返回体差异排查（usage 位置 / 字段名 / 尾部余帧 / 工具调用形态）——新端点接入前先跑一次。
    /// 用法：CatHome4.exe --probe-llm &lt;default|显示名|apiConfigId&gt; [--probe-tools] [--probe-model &lt;模型名&gt;]
    /// </summary>
    public static partial class Program
    {
        /// <summary>探针请求超时（秒）——单次最小请求，超时即失败不重试</summary>
        private const int ProbeTimeoutSeconds = 90;

        /// <summary>
        /// 是否存在探针参数——Main 路由判定（探针通道先于 Bootstrap，避免启动宿主）
        /// </summary>
        /// <param name="args">命令行参数</param>
        /// <returns>true=命中 --probe-llm</returns>
        internal static bool HasProbeLlmArg(string[] args)
        {
            for (int i = 0; i < args.Length; i = i + 1)
            {
                if (args[i] == "--probe-llm")
                {
                    return true;
                }
            }
            return false;
        }

        /// <summary>
        /// 执行 LLM 返回体探针——读配置池 → 发一次最小流式请求 → 输出结构摘要 JSON。
        /// </summary>
        /// <param name="args">命令行参数（--probe-llm 目标 / --probe-tools / --probe-model）</param>
        /// <returns>0=探针成功 / 2=探针失败（配置缺失 / 传输异常 / HTTP 非 200）</returns>
        internal static int RunProbeLlm(string[] args)
        {
            // [段1] 参数解析——目标（default=默认端点）/ 工具探针开关 / 模型覆盖
            string target = ReadProbeArg(args, "--probe-llm");
            bool withTools = false;
            string modelOverride = ReadProbeArg(args, "--probe-model");
            for (int i = 0; i < args.Length; i = i + 1)
            {
                if (args[i] == "--probe-tools")
                {
                    withTools = true;
                }
            }
            if (target.Length == 0)
            {
                target = "default";
            }
            // [段2] 配置池直读——llm-api.json（零 key）+ Data/secrets（key 独立面）
            string dataRoot = ResolveDataRoot();
            CH_LlmApiConfigStore store;
            try
            {
                store = new CH_LlmApiConfigStore(
                    System.IO.Path.Combine(dataRoot, "Data", "config"),
                    System.IO.Path.Combine(dataRoot, "Data", "secrets"));
            }
            catch (Exception ex)
            {
                Console.WriteLine("[CMD] 探针失败：配置池不可读——" + ex.Message);
                return 2;
            }
            CH_LlmApiConfig config = ResolveProbeConfig(store, target);
            if (config == null)
            {
                Console.WriteLine("[CMD] 探针失败：未找到 API 配置（目标 " + target + "）——可用配置：");
                CH_LlmApiConfig[] all = store.GetAll();
                for (int i = 0; i < all.Length; i = i + 1)
                {
                    Console.WriteLine("  " + all[i].ApiConfigId.ToString("N") + "  " + all[i].DisplayName + "  " + all[i].Endpoint + "  " + all[i].DefaultModel);
                }
                return 2;
            }
            string apiKey = store.GetSecret(config.ApiConfigId);
            if (apiKey.Length == 0)
            {
                string envKey = Environment.GetEnvironmentVariable("MAU_LLM_API_KEY");
                if (envKey != null && envKey.Length > 0)
                {
                    apiKey = envKey;
                }
            }
            if (apiKey.Length == 0)
            {
                string legacyKey = Environment.GetEnvironmentVariable("DEEPSEEK_API_KEY");
                if (legacyKey != null && legacyKey.Length > 0)
                {
                    apiKey = legacyKey;
                }
            }
            if (apiKey.Length == 0)
            {
                Console.WriteLine("[CMD] 探针失败：配置无 Key（" + config.DisplayName + "）且环境变量 MAU_LLM_API_KEY / DEEPSEEK_API_KEY 未设置");
                return 2;
            }
            string model = modelOverride;
            if (model.Length == 0)
            {
                model = config.DefaultModel;
            }
            // [段3] 最小探针请求体——流式 + include_usage（可选探针工具，检查 tool_calls 形态）
            Dictionary<string, object> payload = new Dictionary<string, object>();
            payload["model"] = model;
            List<object> messages = new List<object>();
            messages.Add(new { role = "user", content = "ping" });
            payload["messages"] = messages;
            payload["stream"] = true;
            payload["stream_options"] = new { include_usage = true };
            if (withTools)
            {
                List<object> tools = new List<object>();
                tools.Add(new
                {
                    type = "function",
                    function = new
                    {
                        name = "probe_echo",
                        description = "探针回显工具——返回入参原样，仅用于返回体形态检查",
                        parameters = new
                        {
                            type = "object",
                            properties = new { text = new { type = "string" } },
                            required = new string[] { "text" }
                        }
                    }
                });
                payload["tools"] = tools;
            }
            string requestBody = JsonUtil.Serialize(payload);
            // [段4] 发送与帧收集——SSE 按行取 data 载荷（探针面简化为单行 data；SseParser 的多行合并仅影响正文分片，不影响结构判定）
            int httpStatus = 0;
            string[] frames = new string[0];
            string rawBody = "";
            HttpClient client = new HttpClient();
            client.Timeout = TimeSpan.FromSeconds(ProbeTimeoutSeconds);
            try
            {
                using (HttpRequestMessage request = new HttpRequestMessage(HttpMethod.Post, config.Endpoint))
                {
                    request.Headers.TryAddWithoutValidation("Authorization", "Bearer " + apiKey);
                    request.Headers.TryAddWithoutValidation("Accept", "text/event-stream");
                    // 网关会话路由头——与运行时同构（opencode zen 缺失即拒绝请求）
                    request.Headers.TryAddWithoutValidation("x-opencode-session", "cat-home4-probe");
                    request.Content = new StringContent(requestBody, Encoding.UTF8, "application/json");
                    using (HttpResponseMessage response = client.SendAsync(request, HttpCompletionOption.ResponseHeadersRead).GetAwaiter().GetResult())
                    {
                        httpStatus = (int)response.StatusCode;
                        rawBody = response.Content.ReadAsStringAsync().GetAwaiter().GetResult();
                    }
                }
            }
            catch (Exception ex)
            {
                Console.WriteLine("[CMD] 探针失败：传输异常 " + ex.GetType().Name + "|" + ex.Message);
                return 2;
            }
            frames = SplitSseFrames(rawBody);
            // [段5] 输出——结构摘要 JSON + 人读提示
            Console.WriteLine("[CMD] LLM 返回体探针 → " + config.DisplayName + "  model=" + model + "  tools=" + (withTools ? "yes" : "no"));
            Console.WriteLine(SseFrameProbe.Describe(frames, httpStatus, config.Endpoint, model));
            // 运行时同源回放——探针帧直接喂运行时解析函数（修复回归证据；无需启动宿主）
            Console.WriteLine("[CMD] 运行时解析回放: " + DeepSeekLlmRuntime.ReplayUsageFrames(frames));
            if (httpStatus != 200)
            {
                Console.WriteLine("[CMD] HTTP " + httpStatus.ToString() + " 响应体: " + TrimProbeText(rawBody, 300));
                return 2;
            }
            Console.WriteLine("[CMD] 判读：usageLocation=finish-frame（usage 与 finish 帧同构——运行时须无条件解析）/ usage-only-frame（独立统计尾帧）/ both / absent（无统计）；trailingAfterDone=[DONE] 后余帧数。");
            return 0;
        }

        /// <summary>
        /// 解析探测目标——default（默认端点）/ 显示名（不区分大小写）/ apiConfigId（32 位十六进制或带连字符 Guid）。
        /// </summary>
        /// <param name="store">API 配置池</param>
        /// <param name="target">目标标识</param>
        /// <returns>配置；未命中 null</returns>
        private static CH_LlmApiConfig ResolveProbeConfig(CH_LlmApiConfigStore store, string target)
        {
            if (target == "default")
            {
                return store.ResolveDefault();
            }
            Guid parsedId;
            if (Guid.TryParse(target, out parsedId))
            {
                CH_LlmApiConfig config;
                if (store.TryGet(parsedId, out config))
                {
                    return config;
                }
                return null;
            }
            CH_LlmApiConfig[] all = store.GetAll();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                if (string.Equals(all[i].DisplayName, target, StringComparison.OrdinalIgnoreCase))
                {
                    return all[i];
                }
            }
            return null;
        }

        /// <summary>
        /// 读取命令参数值——"--name value" 形态；缺失返回空串。
        /// </summary>
        /// <param name="args">命令行参数</param>
        /// <param name="name">参数名</param>
        /// <returns>参数值或空串</returns>
        private static string ReadProbeArg(string[] args, string name)
        {
            for (int i = 0; i < args.Length; i = i + 1)
            {
                if (args[i] == name && i + 1 < args.Length)
                {
                    string value = args[i + 1];
                    if (value.StartsWith("--", StringComparison.Ordinal))
                    {
                        return "";
                    }
                    return value;
                }
            }
            return "";
        }

        /// <summary>
        /// 拆分 SSE 帧——逐行取 "data: " 载荷（含 [DONE] 与原样保留空行外的一切载荷）。
        /// </summary>
        /// <param name="raw">SSE 响应原文</param>
        /// <returns>帧载荷序列</returns>
        private static string[] SplitSseFrames(string raw)
        {
            List<string> frames = new List<string>();
            if (raw == null || raw.Length == 0)
            {
                return frames.ToArray();
            }
            string[] lines = raw.Split('\n');
            for (int i = 0; i < lines.Length; i = i + 1)
            {
                string line = lines[i].TrimEnd('\r');
                if (line.Length == 0)
                {
                    continue;
                }
                if (!line.StartsWith("data: ", StringComparison.Ordinal))
                {
                    continue;
                }
                frames.Add(line.Substring(6));
            }
            return frames.ToArray();
        }

        /// <summary>
        /// 截断诊断文本——超长截尾并加省略标记（防刷屏）。
        /// </summary>
        /// <param name="text">原文</param>
        /// <param name="limit">上限字符数</param>
        /// <returns>截断文本</returns>
        private static string TrimProbeText(string text, int limit)
        {
            if (text == null)
            {
                return "";
            }
            if (text.Length <= limit)
            {
                return text;
            }
            return text.Substring(0, limit) + "…(+ " + (text.Length - limit).ToString() + " 字符)";
        }
    }
}
