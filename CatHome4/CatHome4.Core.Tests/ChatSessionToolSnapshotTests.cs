using System;
using System.IO;
using Mau.Runtime;
using Xunit;

namespace CatHome4.Core.Tests
{
    /// <summary>
    /// 会话工具面快照测试（A221 · design-ch4-tools §三·十一）——会话工具头与 system prompt 同命：
    /// 持久化、仅在 session.new 时更换；重启恢复**不**从池重取。
    /// </summary>
    [Collection("GlobalToolState")]
    public class ChatSessionToolSnapshotTests
    {
        /// <summary>临时文件路径——唯一化（测试隔离）。</summary>
        /// <param name="tag">标签</param>
        /// <param name="ext">扩展名</param>
        /// <returns>临时绝对路径</returns>
        private static string TempPath(string tag, string ext)
        {
            return Path.Combine(Path.GetTempPath(), "cat4tools_" + tag + "_" + Guid.NewGuid().ToString("N") + ext);
        }

        /// <summary>构造工具规格。</summary>
        /// <param name="name">工具名</param>
        /// <param name="desc">描述</param>
        /// <returns>规格</returns>
        private static ToolSpec Spec(string name, string desc)
        {
            return new ToolSpec(name, desc, "{}");
        }

        /// <summary>构造快照条目。</summary>
        /// <param name="name">工具名</param>
        /// <param name="desc">描述</param>
        /// <returns>快照条目</returns>
        private static CH4.SessionToolSpec Item(string name, string desc)
        {
            CH4.SessionToolSpec item = new CH4.SessionToolSpec();
            item.Name = name;
            item.Description = desc;
            item.ParametersJson = "{}";
            return item;
        }

        /// <summary>写快照元数据文件。</summary>
        /// <param name="path">元数据路径</param>
        /// <param name="tools">工具面快照</param>
        private static void WriteSnapshot(string path, CH4.SessionToolSpec[] tools)
        {
            CH4.SessionMeta meta = new CH4.SessionMeta();
            meta.SessionId = "s";
            meta.CatId = "tool-snap";
            meta.Tools = tools;
            new CH4.SessionMetaStore(path).Save(meta);
        }

        /// <summary>无快照——回落池面（旧档 / 首建）。</summary>
        [Fact]
        public void ResolveSessionTools_NoSnapshot_FallsBackToPool()
        {
            Func<string, string> prev = CH4.ChatSession.SessionMetaPathProvider;
            try
            {
                CH4.ChatSession.SessionMetaPathProvider = delegate (string catId) { return TempPath("miss", ".session.json"); };
                ToolSpec[] pool = new ToolSpec[] { Spec("text-read", "池面") };
                ToolSpec[] got = CH4.ChatSession.ResolveSessionTools("tool-snap", pool);
                Assert.Same(pool, got);
            }
            finally
            {
                CH4.ChatSession.SessionMetaPathProvider = prev;
            }
        }

        /// <summary>快照优先——池面描述已变，仍用快照原文（重启不改面）。</summary>
        [Fact]
        public void ResolveSessionTools_SnapshotWins_OverChangedPool()
        {
            Func<string, string> prev = CH4.ChatSession.SessionMetaPathProvider;
            string path = TempPath("hit", ".session.json");
            try
            {
                WriteSnapshot(path, new CH4.SessionToolSpec[] { Item("text-read", "旧面"), Item("cs-read", "旧面") });
                CH4.ChatSession.SessionMetaPathProvider = delegate (string catId) { return path; };
                ToolSpec[] pool = new ToolSpec[] { Spec("text-read", "新面"), Spec("cs-read", "新面"), Spec("cs-build", "新增") };
                ToolSpec[] got = CH4.ChatSession.ResolveSessionTools("tool-snap", pool);
                Assert.Equal(2, got.Length);
                Assert.Equal("text-read", got[0].Name);
                Assert.Equal("旧面", got[0].Description);
                Assert.Equal("cs-read", got[1].Name);
            }
            finally
            {
                CH4.ChatSession.SessionMetaPathProvider = prev;
            }
        }

        /// <summary>退役剔除——快照含池外工具名时剔除，其余保留。</summary>
        [Fact]
        public void ResolveSessionTools_DropsRetiredTools()
        {
            Func<string, string> prev = CH4.ChatSession.SessionMetaPathProvider;
            string path = TempPath("drop", ".session.json");
            try
            {
                WriteSnapshot(path, new CH4.SessionToolSpec[] { Item("text-read", "旧面"), Item("retired-tool", "已退役") });
                CH4.ChatSession.SessionMetaPathProvider = delegate (string catId) { return path; };
                ToolSpec[] pool = new ToolSpec[] { Spec("text-read", "新面") };
                ToolSpec[] got = CH4.ChatSession.ResolveSessionTools("tool-snap", pool);
                Assert.Single(got);
                Assert.Equal("text-read", got[0].Name);
            }
            finally
            {
                CH4.ChatSession.SessionMetaPathProvider = prev;
            }
        }

        /// <summary>快照全退役——回落池面（不空手）。</summary>
        [Fact]
        public void ResolveSessionTools_AllRetired_FallsBackToPool()
        {
            Func<string, string> prev = CH4.ChatSession.SessionMetaPathProvider;
            string path = TempPath("allret", ".session.json");
            try
            {
                WriteSnapshot(path, new CH4.SessionToolSpec[] { Item("gone-a", ""), Item("gone-b", "") });
                CH4.ChatSession.SessionMetaPathProvider = delegate (string catId) { return path; };
                ToolSpec[] pool = new ToolSpec[] { Spec("text-read", "新面") };
                ToolSpec[] got = CH4.ChatSession.ResolveSessionTools("tool-snap", pool);
                Assert.Same(pool, got);
            }
            finally
            {
                CH4.ChatSession.SessionMetaPathProvider = prev;
            }
        }

        /// <summary>落盘往返——快照三字段（含 order 注记原文）可原样读回。</summary>
        [Fact]
        public void SessionToolSpec_RoundTrip()
        {
            string path = TempPath("round", ".session.json");
            WriteSnapshot(path, new CH4.SessionToolSpec[] { Item("text-read", "描述含 order: -1") });
            CH4.SessionMeta meta = new CH4.SessionMetaStore(path).Load();
            Assert.NotNull(meta);
            Assert.NotNull(meta.Tools);
            Assert.Single(meta.Tools);
            Assert.Equal("text-read", meta.Tools[0].Name);
            Assert.Equal("描述含 order: -1", meta.Tools[0].Description);
            Assert.Equal("{}", meta.Tools[0].ParametersJson);
        }

        /// <summary>快照名集——info 漂移左值（A323：前文工具头事实，非内存面）。</summary>
        [Fact]
        public void LoadPersistedToolNames_SnapshotHit_ReturnsNames()
        {
            Func<string, string> prev = CH4.ChatSession.SessionMetaPathProvider;
            string path = TempPath("names", ".session.json");
            try
            {
                WriteSnapshot(path, new CH4.SessionToolSpec[] { Item("text-read", ""), Item("retired-tool", "") });
                CH4.ChatSession.SessionMetaPathProvider = delegate (string catId) { return path; };
                string[] names = CH4.ChatSession.LoadPersistedToolNames("tool-snap");
                Assert.NotNull(names);
                Assert.Equal(2, names.Length);
                Assert.Equal("text-read", names[0]);
                // 退役名照原样返回——漂移判定与「池剔除」是两个面（A323）
                Assert.Equal("retired-tool", names[1]);
            }
            finally
            {
                CH4.ChatSession.SessionMetaPathProvider = prev;
            }
        }

        /// <summary>无快照——返回 null（调用方回落内存面，不静默空集）。</summary>
        [Fact]
        public void LoadPersistedToolNames_NoSnapshot_ReturnsNull()
        {
            Func<string, string> prev = CH4.ChatSession.SessionMetaPathProvider;
            try
            {
                CH4.ChatSession.SessionMetaPathProvider = delegate (string catId) { return TempPath("namesmiss", ".session.json"); };
                string[] names = CH4.ChatSession.LoadPersistedToolNames("tool-snap");
                Assert.Null(names);
            }
            finally
            {
                CH4.ChatSession.SessionMetaPathProvider = prev;
            }
        }
    }
}
