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
    /// DeepSeekLlmRuntime SSE 解析面分部——帧翻译/增量解析/错误解析/tool_calls 聚合。
    /// P7b partial 拆分——自 DeepSeekLlmRuntime.cs 原样搬移，逻辑零改动。
    /// </summary>
    public sealed partial class DeepSeekLlmRuntime : ILlmRuntime
    {
        /// <summary>
        /// SSE 帧翻译——增量事件流（零 catch：畸形帧/缺 [DONE] 全部事件化；取消异常冒泡给调用方）。
        /// [DONE] 到达即终止（忽略余帧——实测 [DONE] 后可能有余帧）。
        /// usage 事件每请求一次——双形态兼容（独立统计尾帧 / 与 finish 帧同构），双发供应商以最后一份为准；流末统一产出，缺 [DONE] 不产。
        /// 帧内序：Reasoning → Text（同帧）；首个 tool_calls 增量帧产一次 ToolCallsStart（后续分片静默）——会话层运行态判定依据（2026-09-16）。
        /// </summary>
        /// <param name="parser">SseParser 实例</param>
        /// <param name="ct">取消令牌</param>
        /// <returns>流式事件序列</returns>
        private static async IAsyncEnumerable<LlmStreamEvent> TranslateSse(SseParser<string> parser, [EnumeratorCancellation] CancellationToken ct)
        {
            bool done = false;
            // [段0-暂存] finish_reason——流末统一产一次 Finish 事件（length = 输出被截断，消费方据此出声）
            string lastFinishReason = "";
            // [段0-暂存] usage——双发形态去重：本处只缓存，流末统一产一次事件（同一请求不得累加两次）
            string lastUsage = "";
            // [段0] 工具调用聚合——按 index 累积（design A.5：id/name 仅首帧；arguments 是累积增量需拼接后整体解析）
            Dictionary<int, string> toolIds = new Dictionary<int, string>();
            Dictionary<int, string> toolNames = new Dictionary<int, string>();
            Dictionary<int, System.Text.StringBuilder> toolArgs = new Dictionary<int, System.Text.StringBuilder>();
            List<int> toolOrder = new List<int>();
            await foreach (SseItem<string> item in parser.EnumerateAsync(ct))
            {
                // [段1] [DONE] 哨兵——唯一可信终止；忽略余帧
                if (item.Data == "[DONE]")
                {
                    done = true;
                    break;
                }
                // [段2] usage 无条件解析——双形态兼容（2026-09-11 修复：同帧形态曾被互斥分支丢弃）
                // 形态①独立 usage-only 尾帧（DeepSeek 官方——choices 空数组 + usage 对象）
                // 形态②与 finish 帧同构（opencode zen v4.1 后端——delta 非空同帧携带 usage）
                // 形态③双发（Foldin——finish 帧与独立尾帧各带一份）→ 本处只缓存，流末统一产一次事件
                // 原实现置于 TryParseDelta 的 else 分支 → 形态②永远走不到（token 统计全零根因）；CH2 原实现为帧入口无条件提取，移植时结构漂移
                string usageJson = "";
                if (TryParseUsage(item.Data, out usageJson) && usageJson.Length > 0)
                {
                    lastUsage = usageJson;
                }
                // [段2a] finish_reason——每帧尝试（与 delta 同帧或独立帧），流末统一产出
                string finishReason;
                if (TryParseFinishReason(item.Data, out finishReason) && finishReason.Length > 0)
                {
                    lastFinishReason = finishReason;
                }
                // [段2b] 帧解析——delta.content/reasoning_content 判空再产事件（空首帧不开块；空 delta 帧跳过）
                string text;
                string reasoning;
                bool parsed = TryParseDelta(item.Data, out text, out reasoning);
                if (parsed)
                {
                    // [段2b] 帧内产出序 = Reasoning → Text（2026-09-16 定：思考先于回复，产出序即语义序——会话层按到达序切态）
                    if (reasoning.Length > 0)
                    {
                        yield return new LlmStreamEvent(LlmStreamKind.Reasoning, reasoning);
                    }
                    if (text.Length > 0)
                    {
                        yield return new LlmStreamEvent(LlmStreamKind.Text, text);
                    }
                    // [段2c] tool_calls 增量——按 index 聚合（与文本/思考同帧可并存）
                    // 首分片产出 ToolCallsStart（后续分片静默——防事件噪音；会话层 Tool 态判定依据，2026-09-16）
                    int toolOrderBefore = toolOrder.Count;
                    AccumulateToolCalls(item.Data, toolIds, toolNames, toolArgs, toolOrder);
                    if (toolOrder.Count > toolOrderBefore)
                    {
                        yield return new LlmStreamEvent(LlmStreamKind.ToolCallsStart, "");
                    }
                }
            }
            // [段3] 完整性检查——无 [DONE] 提前结束 = STREAM_CLOSED（模型调用不可信，按失败处理）
            if (!done)
            {
                yield return new LlmStreamEvent(LlmStreamKind.Error, "ERR|STREAM_CLOSED|SSE 流未以 [DONE] 结束");
                yield break;
            }
            // [段3b] finish_reason 事件——流末一次（length = 输出被截断；text 为上游原文）
            if (lastFinishReason.Length > 0)
            {
                yield return new LlmStreamEvent(LlmStreamKind.Finish, lastFinishReason);
            }
            // [段4] usage 事件——每请求一次（双发形态以最后一个为准；消费方按请求累加，同一请求不重复计数）
            if (lastUsage.Length > 0)
            {
                yield return new LlmStreamEvent(LlmStreamKind.Usage, lastUsage);
            }
            // [段5] 工具调用完整列表——finish 后一次性发出（聚合后的 arguments 为完整 JSON——消费方整体解析）
            if (toolOrder.Count > 0)
            {
                yield return new LlmStreamEvent(LlmStreamKind.ToolCalls, BuildToolCallsJson(toolIds, toolNames, toolArgs, toolOrder));
            }
            yield return new LlmStreamEvent(LlmStreamKind.Done, "");
        }
        /// <summary>
        /// 解析 SSE 帧 JSON——提取 choices[0].delta.content / reasoning_content。
        /// 畸形帧返回 false（跳过不产事件）；choices 空数组返回 false（usage-only 尾帧）。
        /// </summary>
        /// <param name="data">帧 data 载荷</param>
        /// <param name="text">回复增量（无则空串）</param>
        /// <param name="reasoning">思考增量（无则空串）</param>
        /// <returns>true=解析成功（增量可能为空——调用方判空再产事件）</returns>
        internal static bool TryParseDelta(string data, out string text, out string reasoning)
        {
            text = "";
            reasoning = "";
            if (data == null || data.Length == 0)
            {
                return false;
            }
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(data))
                {
                    JsonElement root = doc.RootElement;
                    JsonElement choices;
                    if (!root.TryGetProperty("choices", out choices) || choices.GetArrayLength() == 0)
                    {
                        return false;
                    }
                    JsonElement first = choices[0];
                    JsonElement delta;
                    if (!first.TryGetProperty("delta", out delta))
                    {
                        return false;
                    }
                    JsonElement value;
                    if (delta.TryGetProperty("content", out value) && value.ValueKind == JsonValueKind.String)
                    {
                        string? gotText = value.GetString();
                        if (gotText != null)
                        {
                            text = gotText;
                        }
                    }
                    if (delta.TryGetProperty("reasoning_content", out value) && value.ValueKind == JsonValueKind.String)
                    {
                        string? gotReasoning = value.GetString();
                        if (gotReasoning != null)
                        {
                            reasoning = gotReasoning;
                        }
                    }
                    return true;
                }
            }
            catch
            {
                // 畸形 JSON 帧——调用方跳过（MALFORMED 帧不做硬失败，容忍上游抖动）
                return false;
            }
        }

        /// <summary>
        /// 解析 finish_reason——choices[0].finish_reason 字符串（无则空串）。
        /// 用途：length 表示输出达 max_tokens 被截断——上层据此出声（失败必须可见）。
        /// </summary>
        /// <param name="data">帧 data 载荷</param>
        /// <param name="finishReason">finish_reason 原文（无为空串）</param>
        /// <returns>true=解析到非空 finish_reason</returns>
        internal static bool TryParseFinishReason(string data, out string finishReason)
        {
            finishReason = "";
            if (data == null || data.Length == 0)
            {
                return false;
            }
            // 早退——帧内无该字段直接跳过（占比 <1%，避免每帧 JSON 解析开销）
            if (data.IndexOf("\"finish_reason\"", StringComparison.Ordinal) < 0)
            {
                return false;
            }
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(data))
                {
                    JsonElement root = doc.RootElement;
                    JsonElement choices;
                    if (!root.TryGetProperty("choices", out choices) || choices.GetArrayLength() == 0)
                    {
                        return false;
                    }
                    JsonElement value;
                    if (choices[0].TryGetProperty("finish_reason", out value) && value.ValueKind == JsonValueKind.String)
                    {
                        string? got = value.GetString();
                        if (got != null && got.Length > 0)
                        {
                            finishReason = got;
                            return true;
                        }
                    }
                    return false;
                }
            }
            catch
            {
                // 畸形帧跳过——容忍上游抖动
                return false;
            }
        }

        /// <summary>
        /// 解析错误响应——错误 JSON 双形态兜底（官方 error{type,code,message} / 代理 type+error.type）。
        /// 格式：ERR|HTTP_{码}|{type}|{message}（截断 200 字防刷屏）。
        /// </summary>
        /// <param name="statusCode">HTTP 状态码</param>
        /// <param name="raw">响应体</param>
        /// <returns>错误文本</returns>
        private static string ParseErrorText(int statusCode, string raw)
        {
            // [段1] 解析错误 JSON——error.type/error.message 或外层 type
            string type = "";
            string message = "";
            if (raw != null && raw.Length > 0)
            {
                try
                {
                    using (JsonDocument doc = JsonUtil.ParseStrict(raw))
                    {
                        JsonElement root = doc.RootElement;
                        JsonElement err;
                        if (root.TryGetProperty("error", out err) && err.ValueKind == JsonValueKind.Object)
                        {
                            JsonElement value;
                            if (err.TryGetProperty("type", out value) && value.ValueKind == JsonValueKind.String)
                            {
                                type = value.GetString() ?? "";
                            }
                            if (err.TryGetProperty("message", out value) && value.ValueKind == JsonValueKind.String)
                            {
                                message = value.GetString() ?? "";
                            }
                        }
                        else
                        {
                            JsonElement value;
                            if (root.TryGetProperty("type", out value) && value.ValueKind == JsonValueKind.String)
                            {
                                type = value.GetString() ?? "";
                            }
                        }
                    }
                }
                catch (Exception ex)
                {
                    LogStore.Add("LLM", 2, "错误响应体解析失败（回落原文）: " + ex.Message, "LLM");
                    message = TrimText(raw, 200);
                }
            }
            // [段2] 兜底——无 type 用 HTTP 码；无 message 用原文
            if (type.Length == 0)
            {
                type = "HTTP_" + statusCode.ToString();
            }
            string detail;
            if (message.Length > 0)
            {
                detail = message;
            }
            else if (raw == null)
            {
                detail = "";
            }
            else
            {
                detail = raw;
            }
            return "ERR|" + type + "|" + TrimText(detail, 200);
        }

        /// <summary>
        /// 截断错误详情——防止超长错误文本刷屏
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
            return text.Substring(0, max);
        }
        /// <summary>
        /// 累积 SSE 帧的 tool_calls 增量——按 index 聚合（design A.5：id/name 仅首帧；arguments 累积增量拼接）。
        /// </summary>
        /// <param name="data">帧 data 载荷</param>
        /// <param name="ids">index → 调用 ID</param>
        /// <param name="names">index → 工具名</param>
        /// <param name="args">index → 参数拼接缓冲</param>
        /// <param name="order">index 出现顺序</param>
        private static void AccumulateToolCalls(string data, Dictionary<int, string> ids, Dictionary<int, string> names, Dictionary<int, System.Text.StringBuilder> args, List<int> order)
        {
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(data))
                {
                    JsonElement root = doc.RootElement;
                    JsonElement choices;
                    if (!root.TryGetProperty("choices", out choices) || choices.GetArrayLength() == 0)
                    {
                        return;
                    }

                    JsonElement delta;
                    if (!choices[0].TryGetProperty("delta", out delta))
                    {
                        return;
                    }

                    JsonElement calls;
                    if (!delta.TryGetProperty("tool_calls", out calls) || calls.ValueKind != JsonValueKind.Array)
                    {
                        return;
                    }

                    for (int i = 0; i < calls.GetArrayLength(); i++)
                    {
                        JsonElement call = calls[i];
                        JsonElement indexEl;
                        int index;
                        if (!call.TryGetProperty("index", out indexEl) || !indexEl.TryGetInt32(out index))
                        {
                            continue;
                        }

                        System.Text.StringBuilder? builder;
                        if (!args.TryGetValue(index, out builder) || builder == null)
                        {
                            builder = new System.Text.StringBuilder();
                            args[index] = builder;
                            ids[index] = "";
                            names[index] = "";
                            order.Add(index);
                        }

                        JsonElement idEl;
                        if (ids[index].Length == 0 && call.TryGetProperty("id", out idEl) && idEl.ValueKind == JsonValueKind.String)
                        {
                            string? gotId = idEl.GetString();
                            if (gotId != null)
                            {
                                ids[index] = gotId!;
                            }
                        }

                        JsonElement funcEl;
                        if (call.TryGetProperty("function", out funcEl))
                        {
                            if (names[index].Length == 0)
                            {
                                JsonElement nameEl;
                                if (funcEl.TryGetProperty("name", out nameEl) && nameEl.ValueKind == JsonValueKind.String)
                                {
                                    string? gotName = nameEl.GetString();
                                    if (gotName != null)
                                    {
                                        names[index] = gotName!;
                                    }
                                }
                            }

                            JsonElement argsEl;
                            if (funcEl.TryGetProperty("arguments", out argsEl) && argsEl.ValueKind == JsonValueKind.String)
                            {
                                string? gotArgs = argsEl.GetString();
                                if (gotArgs != null)
                                {
                                    builder.Append(gotArgs);
                                }
                            }
                        }
                    }
                }
            }
            catch (Exception ex)
            {
                // 畸形帧跳过——容忍上游抖动
                LogStore.Add("LLM", 2, "SSE 畸形帧跳过: " + ex.Message, "LLM");
            }
        }
        /// <summary>
        /// 聚合结果 → 完整 tool_calls JSON 数组（[{"id","name","arguments"}]——arguments 为完整 JSON 文本，消费方整体解析）。
        /// </summary>
        /// <param name="ids">index → 调用 ID</param>
        /// <param name="names">index → 工具名</param>
        /// <param name="args">index → 参数拼接缓冲</param>
        /// <param name="order">index 出现顺序</param>
        /// <returns>JSON 数组字符串</returns>
        private static string BuildToolCallsJson(Dictionary<int, string> ids, Dictionary<int, string> names, Dictionary<int, System.Text.StringBuilder> args, List<int> order)
        {
            System.Text.StringBuilder builder = new System.Text.StringBuilder();
            builder.Append("[");
            for (int i = 0; i < order.Count; i++)
            {
                int index = order[i];
                if (i > 0)
                {
                    builder.Append(",");
                }
                // 空参数边界——arguments 为完整 JSON 文本（消费方整体解析；空串补 "{}"，R4-P3-04）
                string argsText = args[index].ToString();
                if (argsText.Length == 0)
                {
                    argsText = "{}";
                }
                // OpenAI wire 标准：{"id","type":"function","function":{"name","arguments"}}——function 为嵌套对象（判例：missing field function）
                string functionJson = JsonUtil.Object(("name", names[index]), ("arguments", argsText));
                builder.Append(JsonUtil.Object(("id", ids[index]), ("type", "function"), ("function", JsonUtil.Raw(functionJson))));
            }
            builder.Append("]");
            return builder.ToString();
        }
        /// <summary>
        /// 解析 usage 帧——提取 prompt/completion/cacheHit（双格式：DeepSeek prompt_cache_hit_tokens / OpenAI 兼容 prompt_tokens_details.cached_tokens）。
        /// 双形态：独立 usage-only 尾帧 / 与 finish 帧同构（opencode zen v4.1 后端——delta 非空同帧携带 usage）——调用方无条件调用，不做 choices 判空前置。
        /// 全零返回 false（无有效统计不产事件）；格式：{"prompt":N,"completion":N,"cacheHit":N}。
        /// </summary>
        /// <param name="data">帧 data 载荷</param>
        /// <param name="usageJson">usage JSON 字符串（无 usage 或全零返回空串）</param>
        /// <returns>true=解析到有效 usage</returns>
        internal static bool TryParseUsage(string data, out string usageJson)
        {
            usageJson = "";
            if (data == null || data.Length == 0)
            {
                return false;
            }
            // 早退——帧内无 usage 字段直接跳过（usage 帧占比 <1%，避免每帧 JSON 解析开销）
            if (data.IndexOf("\"usage\"", StringComparison.Ordinal) < 0)
            {
                return false;
            }

            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(data))
                {
                    JsonElement root = doc.RootElement;
                    JsonElement usage;
                    if (!root.TryGetProperty("usage", out usage) || usage.ValueKind != JsonValueKind.Object)
                    {
                        return false;
                    }

                    long prompt = 0;
                    long completion = 0;
                    long cacheHit = 0;
                    if (usage.TryGetProperty("prompt_tokens", out JsonElement pt) && pt.ValueKind == JsonValueKind.Number)
                    {
                        prompt = pt.GetInt64();
                    }

                    if (usage.TryGetProperty("completion_tokens", out JsonElement ct) && ct.ValueKind == JsonValueKind.Number)
                    {
                        completion = ct.GetInt64();
                    }

                    if (usage.TryGetProperty("prompt_cache_hit_tokens", out JsonElement cht) && cht.ValueKind == JsonValueKind.Number)
                    {
                        cacheHit = cht.GetInt64();
                    }
                    else if (usage.TryGetProperty("prompt_tokens_details", out JsonElement details) && details.ValueKind == JsonValueKind.Object && details.TryGetProperty("cached_tokens", out JsonElement cached) && cached.ValueKind == JsonValueKind.Number)
                    {
                        cacheHit = cached.GetInt64();
                    }

                    if (prompt == 0 && completion == 0 && cacheHit == 0)
                    {
                        return false;
                    }

                    usageJson = "{\"prompt\":" + prompt.ToString() + ",\"completion\":" + completion.ToString() + ",\"cacheHit\":" + cacheHit.ToString() + "}";
                    return true;
                }
            }
            catch
            {
                // 解析失败=无有效 usage——调用方按零值处理（每帧调用，不落日志避免刷屏）
                return false;
            }
        }

        /// <summary>
        /// 诊断回放——用运行时同源解析函数逐帧重放 usage（宿主 CLI --probe-llm 消费；2026-09-11）。
        /// 口径与 TranslateSse 一致：逐帧 TryParseUsage，取最后一个有效 usage（同一请求只统计一次）。
        /// 用途：探针抓帧后直接验证"运行时能否解析到 token"——无需启动宿主即可回归。
        /// </summary>
        /// <param name="frames">SSE data 载荷序列</param>
        /// <returns>回放结果 JSON（usageFrames/prompt/completion/cacheHit）</returns>
        public static string ReplayUsageFrames(string[] frames)
        {
            int usageFrames = 0;
            string lastUsage = "";
            if (frames != null)
            {
                for (int i = 0; i < frames.Length; i = i + 1)
                {
                    string usageJson = "";
                    if (TryParseUsage(frames[i], out usageJson) && usageJson.Length > 0)
                    {
                        usageFrames = usageFrames + 1;
                        lastUsage = usageJson;
                    }
                }
            }
            long prompt = 0;
            long completion = 0;
            long cacheHit = 0;
            if (lastUsage.Length > 0)
            {
                try
                {
                    using (JsonDocument doc = JsonUtil.ParseStrict(lastUsage))
                    {
                        JsonElement root = doc.RootElement;
                        prompt = ReadReplayNumber(root, "prompt");
                        completion = ReadReplayNumber(root, "completion");
                        cacheHit = ReadReplayNumber(root, "cacheHit");
                    }
                }
                catch (Exception ex)
                {
                    // 回放解析失败保持零值——诊断面不抛异常
                    LogStore.Add("LLM", 2, "回放 usage 解析失败，保持零值: " + ex.Message, "LLM");
                }
            }
            StringBuilder builder = new StringBuilder();
            builder.Append("{\"replayed\":true,\"usageFrames\":");
            builder.Append(usageFrames.ToString());
            builder.Append(",\"prompt\":");
            builder.Append(prompt.ToString());
            builder.Append(",\"completion\":");
            builder.Append(completion.ToString());
            builder.Append(",\"cacheHit\":");
            builder.Append(cacheHit.ToString());
            builder.Append("}");
            return builder.ToString();
        }

        /// <summary>
        /// 回放数值读取——缺失/非数值返回 0。
        /// </summary>
        /// <param name="obj">父对象</param>
        /// <param name="name">字段名</param>
        /// <returns>数值或 0</returns>
        private static long ReadReplayNumber(JsonElement obj, string name)
        {
            JsonElement value;
            if (!obj.TryGetProperty(name, out value) || value.ValueKind != JsonValueKind.Number)
            {
                return 0;
            }
            long number;
            if (!value.TryGetInt64(out number))
            {
                return 0;
            }
            return number;
        }
    }
}