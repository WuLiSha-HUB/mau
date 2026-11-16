using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using Mau.Runtime;
using Xunit;

namespace Mau.Runtime.Tests
{
    /// <summary>
    /// M1a LLM API 配置池测试——Store 生命周期 + 明文零 key + 原子写恢复 + 故障测序。
    /// 隔离：临时目录 Guid 唯一命名 + finally 清理。
    /// </summary>
    public sealed class LlmApiConfigStoreTests
    {
        /// <summary>建立独立临时 Store。</summary>
        /// <param name="settingsRoot">普通设置目录</param>
        /// <param name="secretsRoot">秘密目录</param>
        /// <returns>Store</returns>
        private static CH_LlmApiConfigStore NewStore(out string settingsRoot,
            out string secretsRoot)
        {
            string stamp = Guid.NewGuid().ToString("N");
            settingsRoot = Path.Combine(Path.GetTempPath(), "llmapi_s_" + stamp);
            secretsRoot = Path.Combine(Path.GetTempPath(), "llmapi_k_" + stamp);
            return new CH_LlmApiConfigStore(settingsRoot, secretsRoot);
        }

        /// <summary>递归清理临时目录。</summary>
        /// <param name="root">目录</param>
        private static void CleanDir(string root)
        {
            try
            {
                if (Directory.Exists(root))
                {
                    Directory.Delete(root, true);
                }
            }
            catch (Exception ex)
            {
                // 清理失败不阻断测试
                Console.WriteLine("[测试清理] 配置目录删除失败: " + ex.Message);
            }
        }

        /// <summary>池空首个配置标默认——Save 新建首个配置自动 IsDefault（唯一端点必然被默认消费面使用）。</summary>
        [Fact]
        public void Store_Save_FirstConfigBecomesDefault()
        {
            string settingsRoot;
            string secretsRoot;
            CH_LlmApiConfigStore store = NewStore(out settingsRoot, out secretsRoot);
            try
            {
                Guid id = Guid.NewGuid();
                CH_LlmApiConfig config = MakeConfig(id, "First", "https://first.example/v1");
                store.Save(config, "");
                CH_LlmApiConfig[] all = store.GetAll();
                Assert.Single(all);
                Assert.True(all[0].IsDefault);
                CH_LlmApiConfig? resolved = store.ResolveDefault();
                Assert.NotNull(resolved);
                Assert.Equal(id, resolved.ApiConfigId);
            }
            finally
            {
                CleanDir(settingsRoot);
                CleanDir(secretsRoot);
            }
        }

        /// <summary>空配置 ResolveDefault——返回 null（不做静默回退）。</summary>
        [Fact]
        public void Store_ResolveDefault_EmptyReturnsNull()
        {
            string settingsRoot;
            string secretsRoot;
            CH_LlmApiConfigStore store = NewStore(out settingsRoot, out secretsRoot);
            try
            {
                Assert.Null(store.ResolveDefault());
            }
            finally
            {
                CleanDir(settingsRoot);
                CleanDir(secretsRoot);
            }
        }

        /// <summary>SetDefault——唯一默认语义（旧默认清除 + 目标置默认；目标不存在 false）。</summary>
        [Fact]
        public void Store_SetDefault_UniqueDefault()
        {
            string settingsRoot;
            string secretsRoot;
            CH_LlmApiConfigStore store = NewStore(out settingsRoot, out secretsRoot);
            try
            {
                Guid idA = new Guid("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa");
                Guid idB = new Guid("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb");
                store.Save(MakeConfig(idA, "A", "https://a.example/v1"), "");
                store.Save(MakeConfig(idB, "B", "https://b.example/v1"), "");
                // 首个自动默认——切换默认到 B
                Assert.True(store.SetDefault(idB));
                CH_LlmApiConfig? resolved = store.ResolveDefault();
                Assert.NotNull(resolved);
                Assert.Equal(idB, resolved.ApiConfigId);
                CH_LlmApiConfig[] all = store.GetAll();
                int defaultCount = 0;
                for (int i = 0; i < all.Length; i = i + 1)
                {
                    if (all[i].IsDefault)
                    {
                        defaultCount = defaultCount + 1;
                    }
                }
                Assert.Equal(1, defaultCount);
                // 目标不存在——false 且原默认不变
                Assert.False(store.SetDefault(Guid.NewGuid()));
                CH_LlmApiConfig? after = store.ResolveDefault();
                Assert.NotNull(after);
                Assert.Equal(idB, after.ApiConfigId);
            }
            finally
            {
                CleanDir(settingsRoot);
                CleanDir(secretsRoot);
            }
        }

        /// <summary>Save + GetAll——按稳定 ID 排序 + TryGet 命中/未命中。</summary>
        [Fact]
        public void Store_SaveGetAll_SortedRoundtrip()
        {
            string settingsRoot;
            string secretsRoot;
            CH_LlmApiConfigStore store = NewStore(out settingsRoot, out secretsRoot);
            try
            {
                Guid idA = new Guid("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa");
                Guid idB = new Guid("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb");
                CH_LlmApiConfig configB = MakeConfig(idB, "B", "https://b.example/v1");
                CH_LlmApiConfig configA = MakeConfig(idA, "A", "https://a.example/v1");
                store.Save(configB, "");
                store.Save(configA, "");
                CH_LlmApiConfig[] all = store.GetAll();
                Assert.Equal(2, all.Length);
                Assert.Equal(idA, all[0].ApiConfigId);
                Assert.Equal(idB, all[1].ApiConfigId);
                CH_LlmApiConfig found;
                Assert.True(store.TryGet(idA, out found));
                Assert.Equal("A", found.DisplayName);
                Assert.False(store.TryGet(Guid.NewGuid(), out found));
            }
            finally
            {
                CleanDir(settingsRoot);
                CleanDir(secretsRoot);
            }
        }

        /// <summary>明文零 key——llm-api.json 不含 Key，Key 只在秘密 cfg。</summary>
        [Fact]
        public void Store_Save_KeepsKeyOutOfPlainConfig()
        {
            string settingsRoot;
            string secretsRoot;
            CH_LlmApiConfigStore store = NewStore(out settingsRoot, out secretsRoot);
            try
            {
                Guid id = Guid.NewGuid();
                store.Save(MakeConfig(id, "X", "https://x.example/v1"), "sk-secret-123");
                string plain = File.ReadAllText(
                    Path.Combine(settingsRoot, "llm-api.json"));
                Assert.DoesNotContain("sk-secret-123", plain);
                string secretFile = File.ReadAllText(
                    Path.Combine(secretsRoot, "llm-api.cfg"));
                Assert.Contains("sk-secret-123", secretFile);
                Assert.Equal("sk-secret-123", store.GetSecret(id));
            }
            finally
            {
                CleanDir(settingsRoot);
                CleanDir(secretsRoot);
            }
        }

        /// <summary>Secret 生命周期——ClearSecret 清 Key 但保留普通配置。</summary>
        [Fact]
        public void Store_SecretLifecycle_ClearKeepsConfig()
        {
            string settingsRoot;
            string secretsRoot;
            CH_LlmApiConfigStore store = NewStore(out settingsRoot, out secretsRoot);
            try
            {
                Guid id = Guid.NewGuid();
                store.Save(MakeConfig(id, "X", "https://x.example/v1"), "sk-life");
                Assert.Equal("sk-life", store.GetSecret(id));
                store.ClearSecret(id);
                Assert.Equal("", store.GetSecret(id));
                CH_LlmApiConfig found;
                Assert.True(store.TryGet(id, out found));
            }
            finally
            {
                CleanDir(settingsRoot);
                CleanDir(secretsRoot);
            }
        }

        /// <summary>校验拒绝——空字段与坏端点抛 InvalidDataException。</summary>
        [Fact]
        public void Store_Save_RejectsInvalid()
        {
            string settingsRoot;
            string secretsRoot;
            CH_LlmApiConfigStore store = NewStore(out settingsRoot, out secretsRoot);
            try
            {
                CH_LlmApiConfig emptyName = MakeConfig(Guid.NewGuid(), "",
                    "https://x.example/v1");
                Assert.Throws<InvalidDataException>(delegate
                {
                    store.Save(emptyName, "");
                });
                CH_LlmApiConfig badUri = MakeConfig(Guid.NewGuid(), "X",
                    "not-a-uri");
                Assert.Throws<InvalidDataException>(delegate
                {
                    store.Save(badUri, "");
                });
            }
            finally
            {
                CleanDir(settingsRoot);
                CleanDir(secretsRoot);
            }
        }

        /// <summary>坏正文恢复——从 .last 副本恢复并隔离坏原文。</summary>
        [Fact]
        public void Store_GetAll_RecoversFromLastCopy()
        {
            string settingsRoot;
            string secretsRoot;
            CH_LlmApiConfigStore store = NewStore(out settingsRoot, out secretsRoot);
            try
            {
                Guid idA = new Guid("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa");
                Guid idB = new Guid("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb");
                store.Save(MakeConfig(idA, "A", "https://a.example/v1"), "");
                store.Save(MakeConfig(idB, "B", "https://b.example/v1"), "");
                string configPath = Path.Combine(settingsRoot, "llm-api.json");
                Assert.True(File.Exists(configPath + CH_AtomicTextFile.LastSuffix));
                File.WriteAllText(configPath, "{ broken json");
                CH_LlmApiConfig[] all = store.GetAll();
                // .last 是上一版副本（第二次 Save 时 .last = 仅 A 的版本）——恢复后 1 条
                Assert.Single(all);
                Assert.Equal(idA, all[0].ApiConfigId);
                string[] broken = Directory.GetFiles(settingsRoot,
                    "llm-api.json.broken-*");
                Assert.Single(broken);
            }
            finally
            {
                CleanDir(settingsRoot);
                CleanDir(secretsRoot);
            }
        }

        /// <summary>Secrets 无历史泄露——读取时清理 .last 等历史明文。</summary>
        [Fact]
        public void Store_SecretFile_NoHistoryLeak()
        {
            string settingsRoot;
            string secretsRoot;
            CH_LlmApiConfigStore store = NewStore(out settingsRoot, out secretsRoot);
            try
            {
                Guid id = Guid.NewGuid();
                store.Save(MakeConfig(id, "X", "https://x.example/v1"), "sk-leak");
                string secretPath = Path.Combine(secretsRoot, "llm-api.cfg");
                string lastPath = secretPath + CH_AtomicTextFile.LastSuffix;
                File.WriteAllText(lastPath, "{\"old\":\"sk-old\"}");
                Assert.Equal("sk-leak", store.GetSecret(id));
                Assert.False(File.Exists(lastPath));
            }
            finally
            {
                CleanDir(settingsRoot);
                CleanDir(secretsRoot);
            }
        }

        /// <summary>磁盘不足发生在临时正文落盘前，正式文件保持不变。</summary>
        [Fact]
        public void Fault_DiskFullDoesNotReplaceConsistentFile()
        {
            CH_FaultAtomicFilePort port = new CH_FaultAtomicFilePort();
            string path = Path.GetFullPath("atomic-disk-full.json");
            port.Seed(path, "stable");
            port.FailOperation = "write";

            Assert.Throws<IOException>(delegate
            {
                CH_AtomicTextFile.Write(path, "changed", port);
            });

            Assert.Equal("stable", port.Read(path));
            Assert.Equal(1, port.FailureCount);
            Assert.Equal(1, port.FileCount);
        }

        /// <summary>权限变化发生在备份阶段，不重试且不替换正式文件。</summary>
        [Fact]
        public void Fault_PermissionChangeDoesNotRetryOrReplace()
        {
            CH_FaultAtomicFilePort port = new CH_FaultAtomicFilePort();
            string path = Path.GetFullPath("atomic-permission.json");
            port.Seed(path, "stable");
            port.FailOperation = "copy";

            Assert.Throws<UnauthorizedAccessException>(delegate
            {
                CH_AtomicTextFile.Write(path, "changed", port);
            });

            Assert.Equal("stable", port.Read(path));
            Assert.Equal(1, port.FailureCount);
            Assert.Equal(1, port.FileCount);
        }

        /// <summary>替换瞬间失败会清理临时文件并保留正文与最后副本。</summary>
        [Fact]
        public void Fault_PartialReplaceKeepsPrimaryAndLastCopy()
        {
            CH_FaultAtomicFilePort port = new CH_FaultAtomicFilePort();
            string path = Path.GetFullPath("atomic-partial.json");
            port.Seed(path, "stable");
            port.FailOperation = "move";

            Assert.Throws<IOException>(delegate
            {
                CH_AtomicTextFile.Write(path, "changed", port);
            });

            Assert.Equal("stable", port.Read(path));
            Assert.Equal("stable", port.Read(path
                + CH_AtomicTextFile.LastSuffix));
            Assert.Equal(1, port.FailureCount);
            Assert.Equal(2, port.FileCount);
        }
        /// <summary>端点前文上限 maxToken 落盘往返——Save 后新 Store 读回同值。</summary>
        [Fact]
        public void Store_MaxToken_RoundTrips()
        {
            string settingsRoot;
            string secretsRoot;
            CH_LlmApiConfigStore store = NewStore(out settingsRoot, out secretsRoot);
            try
            {
                CH_LlmApiConfig config = MakeConfig(Guid.NewGuid(), "Budget",
                    "https://budget.example/v1");
                config.MaxToken = 200000;
                store.Save(config, "");
                CH_LlmApiConfigStore reopened = new CH_LlmApiConfigStore(
                    settingsRoot, secretsRoot);
                CH_LlmApiConfig[] all = reopened.GetAll();
                Assert.Single(all);
                Assert.Equal(200000, all[0].MaxToken);
            }
            finally
            {
                CleanDir(settingsRoot);
                CleanDir(secretsRoot);
            }
        }
        /// <summary>端点前文上限为负——Save 拒绝（不静默归零）。</summary>
        [Fact]
        public void Store_MaxToken_NegativeRejected()
        {
            string settingsRoot;
            string secretsRoot;
            CH_LlmApiConfigStore store = NewStore(out settingsRoot, out secretsRoot);
            try
            {
                CH_LlmApiConfig config = MakeConfig(Guid.NewGuid(), "BadBudget",
                    "https://bad.example/v1");
                config.MaxToken = -1;
                Assert.Throws<InvalidDataException>(delegate
                {
                    store.Save(config, "");
                });
                Assert.Empty(store.GetAll());
            }
            finally
            {
                CleanDir(settingsRoot);
                CleanDir(secretsRoot);
            }
        }

        /// <summary>建立字段完整的测试配置。</summary>
        /// <param name="id">稳定身份</param>
        /// <param name="name">显示名</param>
        /// <param name="endpoint">端点</param>
        /// <returns>配置</returns>
        private static CH_LlmApiConfig MakeConfig(Guid id, string name,
            string endpoint)
        {
            CH_LlmApiConfig config = new CH_LlmApiConfig();
            config.ApiConfigId = id;
            config.DisplayName = name;
            config.ApiType = "deepseek";
            config.Endpoint = endpoint;
            config.DefaultModel = "deepseek-v4-flash";
            return config;
        }
    }

    /// <summary>完全内存化且可在单一步骤失败的文件端口。</summary>
    internal sealed class CH_FaultAtomicFilePort : CH_I_AtomicFilePort
    {
        /// <summary>文件正文。</summary>
        private readonly Dictionary<string, byte[]> CH_FaultAtomicFilePort_Files;

        /// <summary>要失败的操作名。</summary>
        internal string FailOperation;

        /// <summary>实际注入失败次数。</summary>
        internal int FailureCount;

        /// <summary>当前文件数量。</summary>
        internal int FileCount
        {
            get { return CH_FaultAtomicFilePort_Files.Count; }
        }

        /// <summary>建立空端口。</summary>
        internal CH_FaultAtomicFilePort()
        {
            CH_FaultAtomicFilePort_Files =
                new Dictionary<string, byte[]>(StringComparer.Ordinal);
            FailOperation = "";
            FailureCount = 0;
        }

        /// <summary>预置稳定正文。</summary>
        internal void Seed(string path, string content)
        {
            CH_FaultAtomicFilePort_Files[path] =
                Encoding.UTF8.GetBytes(content);
        }

        /// <summary>读取正文。</summary>
        internal string Read(string path)
        {
            return Encoding.UTF8.GetString(
                CH_FaultAtomicFilePort_Files[path]);
        }

        /// <summary>内存端口无需建立目录。</summary>
        public void CreateDirectory(string path)
        {
        }

        /// <summary>判断文件存在。</summary>
        public bool Exists(string path)
        {
            return CH_FaultAtomicFilePort_Files.ContainsKey(path);
        }

        /// <summary>写临时正文。</summary>
        public void WriteDurable(string path, byte[] bytes)
        {
            Fail("write", false);
            byte[] copy = new byte[bytes.Length];
            Array.Copy(bytes, copy, bytes.Length);
            CH_FaultAtomicFilePort_Files.Add(path, copy);
        }

        /// <summary>复制稳定正文。</summary>
        public void Copy(string source, string destination)
        {
            Fail("copy", true);
            byte[] sourceBytes = CH_FaultAtomicFilePort_Files[source];
            byte[] copy = new byte[sourceBytes.Length];
            Array.Copy(sourceBytes, copy, sourceBytes.Length);
            CH_FaultAtomicFilePort_Files[destination] = copy;
        }

        /// <summary>覆盖移动临时正文。</summary>
        public void MoveReplace(string source, string destination)
        {
            Fail("move", false);
            CH_FaultAtomicFilePort_Files[destination] =
                CH_FaultAtomicFilePort_Files[source];
            CH_FaultAtomicFilePort_Files.Remove(source);
        }

        /// <summary>清理临时正文。</summary>
        public void Delete(string path)
        {
            CH_FaultAtomicFilePort_Files.Remove(path);
        }

        /// <summary>在指定步骤注入单次失败。</summary>
        private void Fail(string operation, bool permission)
        {
            if (FailOperation != operation)
            {
                return;
            }
            FailureCount = FailureCount + 1;
            if (permission)
            {
                throw new UnauthorizedAccessException("Injected permission.");
            }
            throw new IOException("Injected IO failure.");
        }
    }
}
