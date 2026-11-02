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
    /// DeepSeekLlmRuntime wire 序列化面分部——assistant 消息/tools 数组/请求体构造。
    /// P7b partial 拆分——自 DeepSeekLlmRuntime.cs 原样搬移，逻辑零改动。
    /// </summary>
    public sealed partial class DeepSeekLlmRuntime : ILlmRuntime
    {
        /// <summary>
        /// assistant 消息序列化——tool_calls JSON 原样透传 + reasoning_content 回传铁律。
        /// 规则（A.6 ①⑦）：有 tool_calls 必带 reasoning_content（含空串）；无 tool_calls 但保留思考也带（多轮保留）。
        /// </summary>
        /// <param name="m">assistant 消息</param>
        /// <returns>wire 消息对象</returns>
        private static object BuildAssistantMessage(LlmMessage m)
        {
            Dictionary<string, object> wire = new Dictionary<string, object>();
            wire["role"] = "assistant";
            wire["content"] = m.Content;
            bool hasTools = m.ToolCallsJson.Length > 0;
            if (hasTools)
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(m.ToolCallsJson))
                {
                    wire["tool_calls"] = doc.RootElement.Clone();
                }
            }

            if (hasTools)
            {
                wire["reasoning_content"] = m.ReasoningContent;
            }
            else if (m.ReasoningContent.Length > 0)
            {
                wire["reasoning_content"] = m.ReasoningContent;
            }

            return wire;
        }
        /// <summary>
        /// user 消息序列化（带图片附件）——content 输出为**内容块数组**：text 块（正文 + 失效引用提示）+ 每张图一个 image_url 块。
        /// 引用形态 = 图片绝对路径（design-ch4-chat-images §8.2：持久层只存引用，此处展开）；单图读取失败不阻断请求，
        /// 失败信息并入 text 块（失败必须可见）。无可用图片且正文为空 → text 兜底 "(图片)"，避免空内容块数组。
        /// </summary>
        /// <param name="m">user 消息（ImagesJson = 图片绝对路径 JSON 数组字符串）</param>
        /// <returns>wire 消息对象（含内容块数组）</returns>
        internal static object BuildUserMessageWithImages(LlmMessage m)
        {
            StringBuilder textBuilder = new StringBuilder();
            textBuilder.Append(m.Content);
            List<string> failures = new List<string>();
            List<string> imageUrls = new List<string>();
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(m.ImagesJson))
                {
                    JsonElement root = doc.RootElement;
                    if (root.ValueKind == JsonValueKind.Array)
                    {
                        for (int i = 0; i < root.GetArrayLength(); i = i + 1)
                        {
                            JsonElement item = root[i];
                            if (item.ValueKind != JsonValueKind.String)
                            {
                                continue;
                            }
                            string? path = item.GetString();
                            if (path == null || path.Length == 0)
                            {
                                continue;
                            }
                            string error = "";
                            string url = ImagePayload.Resolve(path, out error);
                            if (error.Length > 0)
                            {
                                failures.Add(error);
                            }
                            else
                            {
                                imageUrls.Add(url);
                            }
                        }
                    }
                }
            }
            catch (Exception ex)
            {
                failures.Add("ERR|IMAGE_REF_PARSE|附件引用解析失败: " + ex.Message);
            }
            if (failures.Count > 0)
            {
                textBuilder.Append("\n\n[图片引用失效]");
                for (int i = 0; i < failures.Count; i = i + 1)
                {
                    textBuilder.Append("\n");
                    textBuilder.Append(failures[i]);
                }
            }
            string text = textBuilder.ToString();
            if (text.Length == 0)
            {
                text = "(图片)";
            }
            List<object> parts = new List<object>();
            parts.Add(new { type = "text", text = text });
            for (int i = 0; i < imageUrls.Count; i = i + 1)
            {
                parts.Add(new
                {
                    type = "image_url",
                    image_url = new { url = imageUrls[i] }
                });
            }
            Dictionary<string, object> wire = new Dictionary<string, object>();
            wire["role"] = "user";
            wire["content"] = parts;
            return wire;
        }

        /// <summary>
        /// tools 数组序列化——OpenAI function 定义；parameters JSON Schema 原样透传（空参数 = 空对象 schema）。
        /// </summary>
        /// <param name="tools">工具规格数组</param>
        /// <returns>wire tools 数组</returns>
        private static object[] BuildWireTools(ToolSpec[] tools)
        {
            if (tools == null || tools.Length == 0)
            {
                return new object[0];
            }

            object[] result = new object[tools.Length];
            for (int i = 0; i < tools.Length; i++)
            {
                ToolSpec spec = tools[i];
                Dictionary<string, object> function = new Dictionary<string, object>();
                function["name"] = spec.Name;
                function["description"] = spec.Description;
                if (spec.ParametersJson.Length > 0)
                {
                    using (JsonDocument doc = JsonUtil.ParseStrict(spec.ParametersJson))
                    {
                        function["parameters"] = doc.RootElement.Clone();
                    }
                }
                else
                {
                    function["parameters"] = new
                    {
                        type = "object",
                        properties = new object()
                    };
                }

                Dictionary<string, object> tool = new Dictionary<string, object>();
                tool["type"] = "function";
                tool["function"] = function;
                result[i] = tool;
            }

            return result;
        }
        /// <summary>
        /// 构造流式对话请求体——OpenAI 兼容消息序列 + tools（P5：接口端零创新，wire 标准）。
        /// system/user 文本直写；assistant 带 tool_calls（JSON 透传）+ reasoning_content（A.6 ①⑦ 回传铁律）；
        /// tool 独立消息（tool_call_id 配对）；思考模式 + effort 按配置；空 tools 省略字段；user_id 非空携带（P9.4 KVCache 隔离）。
        /// </summary>
        /// <param name="messages">消息序列</param>
        /// <param name="tools">工具定义数组</param>
        /// <param name="userId">缓存隔离键（空=不携带）</param>
        /// <returns>请求体 JSON</returns>
        private string BuildChatRequestBody(LlmMessage[] messages, ToolSpec[] tools, string userId)
        {
            // [段1] 消息数组——多 role 序列化（null 字段防御归一——外部消息来源可能带 null）
            List<object> wireMessages = new List<object>();
            for (int i = 0; i < messages.Length; i++)
            {
                LlmMessage m = messages[i];
                if (m.Content == null)
                {
                    m.Content = "";
                }
                if (m.ToolCallId == null)
                {
                    m.ToolCallId = "";
                }
                if (m.ToolCallsJson == null)
                {
                    m.ToolCallsJson = "";
                }
                if (m.ReasoningContent == null)
                {
                    m.ReasoningContent = "";
                }
                if (m.Role == LlmRole.System)
                {
                    wireMessages.Add(new { role = "system", content = m.Content });
                }
                else if (m.Role == LlmRole.User)
                {
                    if (m.ImagesJson != null && m.ImagesJson.Length > 0)
                    {
                        wireMessages.Add(BuildUserMessageWithImages(m));
                    }
                    else
                    {
                        wireMessages.Add(new { role = "user", content = m.Content });
                    }
                }
                else if (m.Role == LlmRole.Assistant)
                {
                    wireMessages.Add(BuildAssistantMessage(m));
                }
                else if (m.Role == LlmRole.Tool)
                {
                    string toolContent = m.Content;
                    if (toolContent.Length == 0)
                    {
                        toolContent = "(no output)";
                    }
                    wireMessages.Add(new { role = "tool", tool_call_id = m.ToolCallId, content = toolContent });
                }
            }
            // [段2] tools 数组——OpenAI function 定义（空数组省略字段——省略原则）
            object[] wireTools = BuildWireTools(tools);
            // [段3] 请求体——思考模式 enabled 带 effort；disabled 关闭思考；tools 非空才带
            Dictionary<string, object> payload = new Dictionary<string, object>();
            payload["model"] = GetModel();
            payload["messages"] = wireMessages;
            payload["stream"] = true;
            // 输出上限——显式传入（不传则吃上游默认值——实测 Foldin 默认 1024，回复与工具参数被静默切断）
            payload["max_tokens"] = GetMaxTokens();
            // E3 Token 统计（CH2 移植）——流式 usage 兼容：要求服务端在 [DONE] 前发送完整统计块（usage-only 尾帧）
            payload["stream_options"] = new { include_usage = true };
            if (GetThinkingEnabled())
            {
                payload["thinking"] = new { type = "enabled" };
                payload["reasoning_effort"] = GetReasoningEffort();
            }
            else
            {
                payload["thinking"] = new { type = "disabled" };
            }
            if (wireTools.Length > 0)
            {
                payload["tools"] = wireTools;
            }
            if (userId != null && userId.Length > 0)
            {
                payload["user_id"] = userId;
            }
            return JsonUtil.Serialize(payload);
        }
    }
}