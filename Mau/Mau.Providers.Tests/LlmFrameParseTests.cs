using System;
using Xunit;

namespace Mau.Providers.Tests
{
    /// <summary>
    /// SSE 帧解析测试——TryParseUsage / TryParseDelta / ReplayUsageFrames（运行时同源解析链）。
    /// 覆盖：双格式 usage 字段（DeepSeek prompt_cache_hit_tokens / OpenAI prompt_tokens_details）/ 全零拒绝 / 无 usage 早退 /
    /// 畸形帧容忍 / delta 文本与思考增量 / choices 空数组 / 回放取最后一份（同一请求只统计一次）。
    /// </summary>
    public class LlmFrameParseTests
    {
        /// <summary>
        /// usage 解析——DeepSeek 格式 prompt_cache_hit_tokens 读为 cacheHit。
        /// </summary>
        [Fact]
        public void TryParseUsage_DeepSeekCacheField()
        {
            string usageJson = "";
            bool ok = DeepSeekLlmRuntime.TryParseUsage("{\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7,\"prompt_cache_hit_tokens\":3}}", out usageJson);
            Assert.True(ok);
            Assert.Equal("{\"prompt\":11,\"completion\":7,\"cacheHit\":3}", usageJson);
        }

        /// <summary>
        /// usage 解析——OpenAI 兼容格式 prompt_tokens_details.cached_tokens 兜底读为 cacheHit。
        /// </summary>
        [Fact]
        public void TryParseUsage_OpenAiDetailsForm()
        {
            string usageJson = "";
            bool ok = DeepSeekLlmRuntime.TryParseUsage("{\"choices\":[],\"usage\":{\"prompt_tokens\":50,\"completion_tokens\":4,\"prompt_tokens_details\":{\"cached_tokens\":12}}}", out usageJson);
            Assert.True(ok);
            Assert.Equal("{\"prompt\":50,\"completion\":4,\"cacheHit\":12}", usageJson);
        }

        /// <summary>
        /// usage 全零——无有效统计不产事件（返回 false + 空串）。
        /// </summary>
        [Fact]
        public void TryParseUsage_AllZeroValues_ReturnsFalse()
        {
            string usageJson = "keep";
            bool ok = DeepSeekLlmRuntime.TryParseUsage("{\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":0}}", out usageJson);
            Assert.False(ok);
            Assert.Equal("", usageJson);
        }

        /// <summary>
        /// 帧内无 usage 字段——早退路径返回 false（不解析 JSON）。
        /// </summary>
        [Fact]
        public void TryParseUsage_NoUsageField_EarlyExit()
        {
            string usageJson = "";
            bool ok = DeepSeekLlmRuntime.TryParseUsage("{\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}", out usageJson);
            Assert.False(ok);
            Assert.Equal("", usageJson);
        }

        /// <summary>
        /// 畸形 JSON——容忍上游抖动返回 false，不抛异常。
        /// </summary>
        [Fact]
        public void TryParseUsage_MalformedJson_ReturnsFalse()
        {
            string usageJson = "";
            bool ok = DeepSeekLlmRuntime.TryParseUsage("{usage\"", out usageJson);
            Assert.False(ok);
        }

        /// <summary>
        /// delta 文本增量——content 正常读出。
        /// </summary>
        [Fact]
        public void TryParseDelta_TextIncrement()
        {
            string text = "";
            string reasoning = "";
            bool ok = DeepSeekLlmRuntime.TryParseDelta("{\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}", out text, out reasoning);
            Assert.True(ok);
            Assert.Equal("hi", text);
            Assert.Equal("", reasoning);
        }

        /// <summary>
        /// delta 思考增量——reasoning_content 正常读出（与文本同帧可并存）。
        /// </summary>
        [Fact]
        public void TryParseDelta_ReasoningIncrement()
        {
            string text = "";
            string reasoning = "";
            bool ok = DeepSeekLlmRuntime.TryParseDelta("{\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"想\"}}]}", out text, out reasoning);
            Assert.True(ok);
            Assert.Equal("想", reasoning);
        }

        /// <summary>
        /// choices 空数组——usage-only 尾帧不产文本事件（返回 false）。
        /// </summary>
        [Fact]
        public void TryParseDelta_EmptyChoices_ReturnsFalse()
        {
            string text = "";
            string reasoning = "";
            bool ok = DeepSeekLlmRuntime.TryParseDelta("{\"choices\":[],\"usage\":{\"prompt_tokens\":1}}", out text, out reasoning);
            Assert.False(ok);
        }

        /// <summary>
        /// 畸形帧——返回 false 不中断（MALFORMED 容忍口径）。
        /// </summary>
        [Fact]
        public void TryParseDelta_Malformed_ReturnsFalse()
        {
            string text = "";
            string reasoning = "";
            bool ok = DeepSeekLlmRuntime.TryParseDelta("not-json", out text, out reasoning);
            Assert.False(ok);
        }

        /// <summary>
        /// 回放同帧形态——usage 非零（opencode zen v4.1 修复回归）。
        /// </summary>
        [Fact]
        public void ReplayUsageFrames_FinishFrame_ReturnsNonZero()
        {
            string[] frames = new string[] {
                "{\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7}}",
                "[DONE]"
            };
            string json = DeepSeekLlmRuntime.ReplayUsageFrames(frames);
            Assert.Contains("\"usageFrames\":1", json, StringComparison.Ordinal);
            Assert.Contains("\"prompt\":11", json, StringComparison.Ordinal);
            Assert.Contains("\"completion\":7", json, StringComparison.Ordinal);
        }

        /// <summary>
        /// 回放双发形态——两帧各计一次，取值以最后一份为准（同一请求只统计一次语义）。
        /// </summary>
        [Fact]
        public void ReplayUsageFrames_BothForms_TakesLast()
        {
            string[] frames = new string[] {
                "{\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":30,\"completion_tokens\":9}}",
                "{\"choices\":[],\"usage\":{\"prompt_tokens\":31,\"completion_tokens\":10}}",
                "[DONE]"
            };
            string json = DeepSeekLlmRuntime.ReplayUsageFrames(frames);
            Assert.Contains("\"usageFrames\":2", json, StringComparison.Ordinal);
            Assert.Contains("\"prompt\":31", json, StringComparison.Ordinal);
            Assert.Contains("\"completion\":10", json, StringComparison.Ordinal);
        }

        /// <summary>
        /// 回放无 usage——计数零，数值全零且不抛异常。
        /// </summary>
        [Fact]
        public void ReplayUsageFrames_NoUsage_ZeroCount()
        {
            string[] frames = new string[] {
                "{\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}",
                "[DONE]"
            };
            string json = DeepSeekLlmRuntime.ReplayUsageFrames(frames);
            Assert.Contains("\"usageFrames\":0", json, StringComparison.Ordinal);
            Assert.Contains("\"prompt\":0", json, StringComparison.Ordinal);
        }

        /// <summary>
        /// finish_reason 解析——length 被原样取出（截断可见性判据）。
        /// </summary>
        [Fact]
        public void TryParseFinishReason_Length_ReturnsValue()
        {
            string reason = "";
            bool ok = DeepSeekLlmRuntime.TryParseFinishReason("{\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"length\"}]}", out reason);
            Assert.True(ok);
            Assert.Equal("length", reason);
        }

        /// <summary>
        /// finish_reason 解析——帧内无该字段（含 null）早退返回 false 且输出空串。
        /// </summary>
        [Fact]
        public void TryParseFinishReason_MissingOrNull_ReturnsFalse()
        {
            string reason = "keep";
            bool ok = DeepSeekLlmRuntime.TryParseFinishReason("{\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}", out reason);
            Assert.False(ok);
            Assert.Equal("", reason);
            bool nullOk = DeepSeekLlmRuntime.TryParseFinishReason("{\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":null}]}", out reason);
            Assert.False(nullOk);
            Assert.Equal("", reason);
        }
    }
}
