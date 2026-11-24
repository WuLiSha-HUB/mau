using System;
using System.IO;
using Mau.Runtime;
using Xunit;

namespace CatHome4.Core.Tests
{
    /// <summary>
    /// HTTP 注入通道池测试（design-ch4-channel.md）——CRUD / Key 反查 / Key 重置 / 校验拒绝 / 落盘往返。
    /// 落盘产物根参数化到临时目录（Dispose 清理）。
    /// </summary>
    public class ChannelConfigStoreTests : IDisposable
    {
        /// <summary>测试临时目录（配置面）。</summary>
        private readonly string _dir;

        /// <summary>测试临时目录（秘密面）。</summary>
        private readonly string _secretDir;

        /// <summary>建立测试环境——独立临时目录（配置 / 秘密分目录，跑更新构）。</summary>
        public ChannelConfigStoreTests()
        {
            string root = Path.Combine(Path.GetTempPath(),
                "ch4-channel-test-" + Guid.NewGuid().ToString("N"));
            _dir = Path.Combine(root, "config");
            _secretDir = Path.Combine(root, "secrets");
            Directory.CreateDirectory(_dir);
            Directory.CreateDirectory(_secretDir);
        }

        /// <summary>清理——删临时目录（失败不掩盖测试结论）。</summary>
        public void Dispose()
        {
            try
            {
                string root = Path.GetDirectoryName(_dir);
                if (root != null && Directory.Exists(root))
                {
                    Directory.Delete(root, true);
                }
            }
            catch (IOException ex)
            {
                // 清理失败不改变测试结论（临时目录可延时回收）
                Console.WriteLine("[测试清理] 通道临时目录删除失败: " + ex.Message);
            }
        }

        /// <summary>构造一条通道配置（随机身份）。</summary>
        private static CH_ChannelConfig MakeConfig(string name, string catKey)
        {
            CH_ChannelConfig config = new CH_ChannelConfig();
            config.ChannelId = Guid.NewGuid();
            config.DisplayName = name;
            config.CatKey = catKey;
            return config;
        }

        /// <summary>新建一个绑定测试目录的池。</summary>
        private CH_ChannelConfigStore NewStore()
        {
            return new CH_ChannelConfigStore(_dir, _secretDir);
        }

        /// <summary>空池——列表为空、Key 反查不命中、按身份读取不命中（不造值）。</summary>
        [Fact]
        public void EmptyPool_ReturnsEmptyAndMisses()
        {
            CH_ChannelConfigStore store = NewStore();
            Assert.Empty(store.GetAll());
            CH_ChannelConfig resolved;
            Assert.False(store.TryResolveByKey("ch_anything", out resolved));
            Assert.False(store.TryResolveByKey("", out resolved));
            Assert.False(store.TryGet(Guid.NewGuid(), out resolved));
            Assert.False(store.Delete(Guid.NewGuid()));
            string key = "";
            Assert.False(store.RotateKey(Guid.NewGuid(), out key));
            Assert.Equal("", key);
        }

        /// <summary>新建即带 Key——按 Key 反查命中绑定猫（外部程序只持 Key，不需知道猫 key）。</summary>
        [Fact]
        public void Save_WithKey_ResolvesBoundCat()
        {
            CH_ChannelConfigStore store = NewStore();
            CH_ChannelConfig config = MakeConfig("ww-guest", "639249925591864296");
            store.Save(config, "ch_abc123");
            CH_ChannelConfig resolved;
            Assert.True(store.TryResolveByKey("ch_abc123", out resolved));
            Assert.Equal("ww-guest", resolved.DisplayName);
            Assert.Equal("639249925591864296", resolved.CatKey);
            Assert.Equal(config.ChannelId, resolved.ChannelId);
        }

        /// <summary>落盘往返——新实例读同一目录：配置与 Key 都能反查（Key 在独立秘密文件）。</summary>
        [Fact]
        public void Save_ThenReload_KeepsKeyResolvable()
        {
            CH_ChannelConfigStore store = NewStore();
            store.Save(MakeConfig("persisted", "cat-a"), "ch_persist");
            CH_ChannelConfigStore reloaded = NewStore();
            CH_ChannelConfig[] all = reloaded.GetAll();
            Assert.Single(all);
            Assert.Equal("persisted", all[0].DisplayName);
            Assert.Equal("ch_persist", reloaded.GetKey(all[0].ChannelId));
            CH_ChannelConfig resolved;
            Assert.True(reloaded.TryResolveByKey("ch_persist", out resolved));
            Assert.Equal("cat-a", resolved.CatKey);
        }

        /// <summary>Key 重置——旧 Key 立即失效，新 Key 命中同一通道。</summary>
        [Fact]
        public void RotateKey_InvalidatesOldKey()
        {
            CH_ChannelConfigStore store = NewStore();
            CH_ChannelConfig config = MakeConfig("rotate", "cat-b");
            store.Save(config, "ch_old");
            string fresh = "";
            Assert.True(store.RotateKey(config.ChannelId, out fresh));
            Assert.NotEqual("ch_old", fresh);
            CH_ChannelConfig resolved;
            Assert.False(store.TryResolveByKey("ch_old", out resolved));
            Assert.True(store.TryResolveByKey(fresh, out resolved));
            Assert.Equal("cat-b", resolved.CatKey);
        }

        /// <summary>Key 空值保存——保留原 Key（字段级合并写语义）。</summary>
        [Fact]
        public void Save_EmptyKey_KeepsExistingKey()
        {
            CH_ChannelConfigStore store = NewStore();
            CH_ChannelConfig config = MakeConfig("keep", "cat-c");
            store.Save(config, "ch_keep");
            config.CatKey = "cat-d";
            store.Save(config, "");
            Assert.Equal("ch_keep", store.GetKey(config.ChannelId));
            CH_ChannelConfig resolved;
            Assert.True(store.TryResolveByKey("ch_keep", out resolved));
            Assert.Equal("cat-d", resolved.CatKey);
        }

        /// <summary>删除——配置与 Key 一并移除，反查不再命中。</summary>
        [Fact]
        public void Delete_RemovesConfigAndKey()
        {
            CH_ChannelConfigStore store = NewStore();
            CH_ChannelConfig config = MakeConfig("delete", "cat-e");
            store.Save(config, "ch_delete");
            Assert.True(store.Delete(config.ChannelId));
            Assert.Empty(store.GetAll());
            CH_ChannelConfig resolved;
            Assert.False(store.TryResolveByKey("ch_delete", out resolved));
            Assert.Equal("", store.GetKey(config.ChannelId));
            Assert.False(store.Delete(config.ChannelId));
        }

        /// <summary>校验拒绝——通道名 / 绑定猫必填，身份不得为零值。</summary>
        [Fact]
        public void Save_RejectsInvalidConfig()
        {
            CH_ChannelConfigStore store = NewStore();
            CH_ChannelConfig blankName = MakeConfig("", "cat-f");
            Assert.Throws<InvalidDataException>(delegate { store.Save(blankName, "ch_x"); });
            CH_ChannelConfig blankCat = MakeConfig("named", "");
            Assert.Throws<InvalidDataException>(delegate { store.Save(blankCat, "ch_x"); });
            CH_ChannelConfig zeroId = MakeConfig("named", "cat-g");
            zeroId.ChannelId = Guid.Empty;
            Assert.Throws<InvalidDataException>(delegate { store.Save(zeroId, "ch_x"); });
            CH_ChannelConfig longName = MakeConfig(new string('a', CH_ChannelConfigStore.DisplayNameMaxLength + 1), "cat-h");
            Assert.Throws<InvalidDataException>(delegate { store.Save(longName, "ch_x"); });
            Assert.Empty(store.GetAll());
        }

        /// <summary>通道名唯一——重名（忽略大小写）拒绝；同身份改名放行。</summary>
        [Fact]
        public void Save_DuplicateDisplayName_IsRejected()
        {
            CH_ChannelConfigStore store = NewStore();
            CH_ChannelConfig first = MakeConfig("Dup", "cat-i");
            store.Save(first, "ch_one");
            CH_ChannelConfig second = MakeConfig("dup", "cat-j");
            Assert.Throws<InvalidDataException>(delegate { store.Save(second, "ch_two"); });
            CH_ChannelConfig renamed = MakeConfig("unique", "cat-k");
            renamed.ChannelId = first.ChannelId;
            store.Save(renamed, "");
            Assert.Single(store.GetAll());
        }

        /// <summary>Key 归一——两端空白被修剪后可用；含内部空白拒绝。</summary>
        [Fact]
        public void Save_NormalizesAndRejectsBadKeys()
        {
            CH_ChannelConfigStore store = NewStore();
            CH_ChannelConfig config = MakeConfig("norm", "cat-l");
            store.Save(config, "  ch_trim  ");
            CH_ChannelConfig resolved;
            Assert.True(store.TryResolveByKey("ch_trim", out resolved));
            CH_ChannelConfig bad = MakeConfig("bad", "cat-m");
            Assert.Throws<InvalidDataException>(delegate { store.Save(bad, "ch has space"); });
        }

        /// <summary>生成 Key——非语义化随机串，两次生成不同且长度在限内。</summary>
        [Fact]
        public void GenerateKey_IsRandomAndFitsLimit()
        {
            string first = CH_ChannelConfigStore.GenerateKey();
            string second = CH_ChannelConfigStore.GenerateKey();
            Assert.NotEqual(first, second);
            Assert.StartsWith("ch_", first);
            Assert.True(first.Length <= CH_ChannelConfigStore.KeyMaxLength);
        }
    }
}
