using System;
using System.IO;
using Xunit;

namespace Mau.Providers.Tests
{
    /// <summary>
    /// 群目录与寻址测试（A384）——别名落盘 / 别名唯一 / 清除别名 / 歧义候选 / 群名包含寻址。
    /// 判据来源：`qqbot-groups alias` 曾报成功而磁盘 alias 仍为空——SetAlias 的条目取自 TryResolve 内部另一次 Load，
    /// 改动落在文档实例之外，Save 把别名静默丢弃。本组用例把该缺陷变红（落盘后再解析必须命中）。
    /// </summary>
    public class QqBotGroupDirectoryTests
    {
        /// <summary>测试用 AppID。</summary>
        private const string AppId = "1905759910";

        /// <summary>第一个测试群 OpenID。</summary>
        private const string GroupA = "AAAACCCC11110000FFFF8888F8A00F0A";

        /// <summary>第二个测试群 OpenID（末 8 位与真实群同形，覆盖末 8 位寻址面）。</summary>
        private const string GroupB = "BBBBCCCC22220000FFFF99999B2A0D34";

        /// <summary>
        /// 建临时目录并登记两个群（含官方群名与权限）。
        /// </summary>
        /// <param name="root">临时根路径（供清理）</param>
        /// <returns>群目录实例</returns>
        private static QqBotGroupDirectory CreateDirectory(out string root)
        {
            root = Path.Combine(Path.GetTempPath(), "qqbot-dir-tests-" + Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(root);
            QqBotGroupDirectory directory = new QqBotGroupDirectory(root);
            directory.AddGroup(AppId, GroupA);
            directory.AddGroup(AppId, GroupB);
            directory.ApplyOfficialState(AppId, GroupA, "猫窝大队", true, "all", "member");
            directory.ApplyOfficialState(AppId, GroupB, "养生水疗中心", true, "only_mention", "member");
            return directory;
        }

        /// <summary>
        /// 清理临时目录——清理失败只出声，不掩盖断言结论。
        /// </summary>
        /// <param name="root">临时根路径</param>
        private static void Cleanup(string root)
        {
            try
            {
                if (root.Length > 0 && Directory.Exists(root))
                {
                    Directory.Delete(root, true);
                }
            }
            catch (IOException ex)
            {
                Console.Error.WriteLine("清理临时目录失败: " + ex.Message);
            }
        }

        /// <summary>
        /// 按 OpenID 在文档里取条目（测试侧自取，不复用被测私有方法）。
        /// </summary>
        /// <param name="doc">群目录文档</param>
        /// <param name="openId">群 OpenID</param>
        /// <returns>条目——条目缺失即用例前提不成立，直接抛而不返回 null</returns>
        private static QqBotGroupEntry Find(QqBotGroupDocument doc, string openId)
        {
            for (int i = 0; i < doc.Groups.Count; i = i + 1)
            {
                if (string.Equals(doc.Groups[i].OpenId, openId, StringComparison.OrdinalIgnoreCase))
                {
                    return doc.Groups[i];
                }
            }
            throw new InvalidOperationException("群条目不存在: " + openId);
        }

        /// <summary>
        /// 别名落盘——设置成功后重新读盘，alias 字段必须在，且别名寻址能命中该群。
        /// </summary>
        [Fact]
        public void SetAlias_PersistsToDisk()
        {
            string root;
            QqBotGroupDirectory directory = CreateDirectory(out root);
            try
            {
                string err = directory.SetAlias(AppId, "9B2A0D34", "摸鱼群");
                Assert.Equal("", err);
                QqBotGroupEntry persisted = Find(directory.Load(AppId), GroupB);
                Assert.Equal("摸鱼群", persisted.Alias, StringComparer.Ordinal);
                QqBotGroupEntry resolved;
                string candidates;
                Assert.True(directory.TryResolve(AppId, "摸鱼群", out resolved, out candidates));
                Assert.Equal(GroupB, resolved.OpenId, StringComparer.OrdinalIgnoreCase);
            }
            finally
            {
                Cleanup(root);
            }
        }

        /// <summary>
        /// 别名唯一——同一 bot 内重名必须报错，且两个群的原别名都不被改动。
        /// </summary>
        [Fact]
        public void SetAlias_DuplicateAlias_Rejected()
        {
            string root;
            QqBotGroupDirectory directory = CreateDirectory(out root);
            try
            {
                Assert.Equal("", directory.SetAlias(AppId, GroupA, "常用群"));
                string err = directory.SetAlias(AppId, GroupB, "常用群");
                Assert.Contains("别名已被占用", err, StringComparison.Ordinal);
                QqBotGroupDocument doc = directory.Load(AppId);
                Assert.Equal("常用群", Find(doc, GroupA).Alias, StringComparer.Ordinal);
                Assert.Equal("", Find(doc, GroupB).Alias);
            }
            finally
            {
                Cleanup(root);
            }
        }

        /// <summary>
        /// 清除别名——传空串后落盘为空，原别名不再能寻址。
        /// </summary>
        [Fact]
        public void SetAlias_Empty_ClearsAlias()
        {
            string root;
            QqBotGroupDirectory directory = CreateDirectory(out root);
            try
            {
                Assert.Equal("", directory.SetAlias(AppId, GroupB, "摸鱼群"));
                Assert.Equal("", directory.SetAlias(AppId, GroupB, ""));
                Assert.Equal("", Find(directory.Load(AppId), GroupB).Alias);
                QqBotGroupEntry resolved;
                string candidates;
                Assert.False(directory.TryResolve(AppId, "摸鱼群", out resolved, out candidates));
            }
            finally
            {
                Cleanup(root);
            }
        }

        /// <summary>
        /// 歧义寻址——两个群共同命中时必须返回候选并提示改用别名 / 完整 OpenID，不猜。
        /// </summary>
        [Fact]
        public void TryResolve_Ambiguous_ReturnsCandidates()
        {
            string root;
            QqBotGroupDirectory directory = CreateDirectory(out root);
            try
            {
                QqBotGroupEntry resolved;
                string candidates;
                Assert.False(directory.TryResolve(AppId, "C", out resolved, out candidates));
                Assert.Contains("多个群命中", candidates, StringComparison.Ordinal);
            }
            finally
            {
                Cleanup(root);
            }
        }

        /// <summary>
        /// 群名包含寻址——别名未设时按官方群名命中唯一群（A384 群名寻址判据）。
        /// </summary>
        [Fact]
        public void TryResolve_ByTitle_Contains()
        {
            string root;
            QqBotGroupDirectory directory = CreateDirectory(out root);
            try
            {
                QqBotGroupEntry resolved;
                string candidates;
                Assert.True(directory.TryResolve(AppId, "水疗", out resolved, out candidates));
                Assert.Equal(GroupB, resolved.OpenId, StringComparer.OrdinalIgnoreCase);
            }
            finally
            {
                Cleanup(root);
            }
        }
    }
}
