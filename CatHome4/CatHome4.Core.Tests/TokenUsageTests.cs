using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Mau.Runtime;
using CatHome4.Contracts;
using Xunit;

namespace CatHome4.Core.Tests
{
    /// <summary>
    /// Token 用量流水测试（A388）——每次请求一行落盘 / 无用量不落行 / 按日分文件 / 字段与差分口径。
    /// </summary>
    [Collection("GlobalToolState")]
    public class TokenUsageTests
    {
        /// <summary>
        /// 脚本化假 LLM 运行时——按配置产 usage 帧，再产文本与结束帧。
        /// </summary>
        private sealed class ScriptedLlm : ILlmRuntime
        {
            /// <summary>本流要产出的 usage 事件文本序列（按序产出）。</summary>
            public string[] UsageFrames = new string[0];

            /// <summary>
            /// 流式对话——usage 帧 + 文本 + 结束帧。
            /// </summary>
            /// <param name="messages">消息序列</param>
            /// <param name="tools">工具定义</param>
            /// <param name="userId">用户标识</param>
            /// <param name="ct">取消令牌</param>
            /// <returns>事件流</returns>
            public async IAsyncEnumerable<LlmStreamEvent> ChatStream(LlmMessage[] messages, ToolSpec[] tools, string userId = "", [System.Runtime.CompilerServices.EnumeratorCancellation] CancellationToken ct = default)
            {
                for (int i = 0; i < UsageFrames.Length; i = i + 1)
                {
                    yield return new LlmStreamEvent(LlmStreamKind.Usage, UsageFrames[i]);
                }
                yield return new LlmStreamEvent(LlmStreamKind.Text, "回复");
                yield return new LlmStreamEvent(LlmStreamKind.Done, "");
                await Task.Yield();
            }
        }

        /// <summary>
        /// 建临时流水根——返回绝对路径（调用方负责删除）。
        /// </summary>
        /// <returns>临时目录</returns>
        private static string NewRoot()
        {
            string root = Path.Combine(Path.GetTempPath(), "cat4token_" + Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(root);
            return root;
        }

        /// <summary>
        /// 构造测试会话——临时前文文件 + 视图存储。
        /// </summary>
        /// <param name="llm">LLM 运行时</param>
        /// <param name="catKey">猫 key</param>
        /// <returns>会话实体</returns>
        private static CH4.ChatSession CreateSession(ILlmRuntime llm, string catKey)
        {
            CH4.ToolRegistry.Init(new ToolSpec[0], null, new Dictionary<string, bool>());
            ChatContext ctx = new ChatContext();
            string tmp = Path.Combine(Path.GetTempPath(), "cat4token_" + Guid.NewGuid().ToString("N") + ".jsonl");
            SessionStore store = new SessionStore(tmp);
            OA oa = new OA(new ThreadGuard());
            CH4.SessionViewStore viewStore = new CH4.SessionViewStore(Path.Combine(Path.GetTempPath(), "cat4token_" + Guid.NewGuid().ToString("N") + ".view.jsonl"));
            CH4.ChatSession session = new CH4.ChatSession(catKey, "token", ctx, store, llm, oa, new ToolSpec[0], delegate (string name, string args) { return "ERR|NO_TOOL|" + name; }, viewStore);
            session.SetCatKey(catKey);
            return session;
        }

        /// <summary>
        /// 泵会话直到 Idle——上限 500 帧（防死循环）。
        /// </summary>
        /// <param name="session">会话</param>
        private static void PumpUntilIdle(CH4.ChatSession session)
        {
            for (int i = 0; i < 500; i = i + 1)
            {
                session.Pump();
                Thread.Sleep(5);
                if (session.IsIdle && i >= 2)
                {
                    break;
                }
            }
        }

        /// <summary>
        /// 单请求带 usage——落一行，七字段与实际消耗一致（miss 由 prompt - cacheHit 派生）。
        /// </summary>
        [Fact]
        public void Request_WithUsage_AppendsOneLine()
        {
            string root = NewRoot();
            try
            {
                CH4.TokenUsageStore.DirProvider = delegate (string catKey) { return Path.Combine(root, catKey); };
                ScriptedLlm llm = new ScriptedLlm();
                llm.UsageFrames = new string[] { "{\"prompt\":100,\"completion\":20,\"cacheHit\":40}" };
                CH4.ChatSession session = CreateSession(llm, "test-token");
                session.PostUserMessage("测试一下");
                PumpUntilIdle(session);
                string dir = Path.Combine(root, "test-token");
                string[] files = Directory.GetFiles(dir, "*.jsonl");
                Assert.Single(files);
                string[] lines = File.ReadAllLines(files[0]);
                Assert.Single(lines);
                using (JsonDocument doc = JsonDocument.Parse(lines[0]))
                {
                    JsonElement row = doc.RootElement;
                    Assert.Equal(100, row.GetProperty("prompt").GetInt64());
                    Assert.Equal(20, row.GetProperty("completion").GetInt64());
                    Assert.Equal(40, row.GetProperty("cacheHit").GetInt64());
                    Assert.Equal(60, row.GetProperty("miss").GetInt64());
                    Assert.Equal(1, row.GetProperty("req").GetInt32());
                    Assert.Equal(0, row.GetProperty("round").GetInt32());
                    Assert.True(row.GetProperty("ts").GetInt64() > 0);
                }
            }
            finally
            {
                CH4.TokenUsageStore.DirProvider = null;
                if (Directory.Exists(root))
                {
                    Directory.Delete(root, true);
                }
            }
        }

        /// <summary>
        /// 无 usage 帧（模型未报用量）——不落行、不建目录。
        /// </summary>
        [Fact]
        public void Request_NoUsage_NoFile()
        {
            string root = NewRoot();
            try
            {
                CH4.TokenUsageStore.DirProvider = delegate (string catKey) { return Path.Combine(root, catKey); };
                ScriptedLlm llm = new ScriptedLlm();
                CH4.ChatSession session = CreateSession(llm, "test-token");
                session.PostUserMessage("测试一下");
                PumpUntilIdle(session);
                Assert.False(Directory.Exists(Path.Combine(root, "test-token")));
            }
            finally
            {
                CH4.TokenUsageStore.DirProvider = null;
                if (Directory.Exists(root))
                {
                    Directory.Delete(root, true);
                }
            }
        }

        /// <summary>
        /// 三项全零——Append 直接返回，不落行（空用量不产生假记录）。
        /// </summary>
        [Fact]
        public void Append_ZeroUsage_NoLine()
        {
            string root = NewRoot();
            try
            {
                CH4.TokenUsageStore.DirProvider = delegate (string catKey) { return Path.Combine(root, catKey); };
                CH4.TokenUsageStore.Append("test-token", 0, 1, 0, 0, 0, 1794910164470L);
                Assert.False(File.Exists(Path.Combine(root, "test-token", "20261117.jsonl")));
            }
            finally
            {
                CH4.TokenUsageStore.DirProvider = null;
                if (Directory.Exists(root))
                {
                    Directory.Delete(root, true);
                }
            }
        }

        /// <summary>
        /// 文件名按本地日期派生——同一天多行追加在同一文件。
        /// </summary>
        [Fact]
        public void Append_SameDay_SameFileAppend()
        {
            string root = NewRoot();
            try
            {
                CH4.TokenUsageStore.DirProvider = delegate (string catKey) { return Path.Combine(root, catKey); };
                long morning = new DateTimeOffset(new DateTime(2026, 11, 17, 9, 0, 0, DateTimeKind.Local)).ToUnixTimeMilliseconds();
                long evening = new DateTimeOffset(new DateTime(2026, 11, 17, 21, 0, 0, DateTimeKind.Local)).ToUnixTimeMilliseconds();
                CH4.TokenUsageStore.Append("test-token", 0, 1, 10, 2, 0, morning);
                CH4.TokenUsageStore.Append("test-token", 0, 2, 20, 3, 5, evening);
                string dir = Path.Combine(root, "test-token");
                string path = Path.Combine(dir, "20261117.jsonl");
                Assert.True(File.Exists(path));
                Assert.Equal(2, File.ReadAllLines(path).Length);
            }
            finally
            {
                CH4.TokenUsageStore.DirProvider = null;
                if (Directory.Exists(root))
                {
                    Directory.Delete(root, true);
                }
            }
        }
    }
}
