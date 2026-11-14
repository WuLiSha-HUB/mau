using System;
using System.IO;
using Mau.Runtime;
using Xunit;

namespace Mau.Runtime.Tests
{
    /// <summary>
    /// 配置纯 KV 语义测试（A364）——目录扫描装载 / 写入落盘 / 键名写死防御 / 还原 = 删除落盘值。
    /// 隔离：临时目录 Guid 唯一命名 + finally 清理。
    /// </summary>
    public sealed class ConfigStoreKvTests
    {
        /// <summary>建临时配置目录。</summary>
        /// <returns>目录绝对路径</returns>
        private static string NewTempDir()
        {
            string dir = Path.Combine(Path.GetTempPath(), "kvcfg_" + Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(dir);
            return dir;
        }

        /// <summary>临时目录清理（失败不阻断测试结论）。</summary>
        /// <param name="dir">目录路径</param>
        private static void CleanDir(string dir)
        {
            try
            {
                if (Directory.Exists(dir))
                {
                    Directory.Delete(dir, true);
                }
            }
            catch (Exception ex)
            {
                // 测试夹具清理——尽力删除（文件被占用等不影响用例结论）
                Console.WriteLine("[测试清理] 目录删除失败: " + ex.Message);
            }
        }

        /// <summary>目录扫描装载——*.cfg 按文件名去后缀归段。</summary>
        [Fact]
        public void LoadDir_ReadsAllCfgFiles()
        {
            string dir = NewTempDir();
            try
            {
                File.WriteAllText(Path.Combine(dir, "ui.cfg"), "ui.font_scale=120\n");
                File.WriteAllText(Path.Combine(dir, "llm.cfg"), "llm.thinking=enabled\n");
                ConfigStore store = ConfigStore.LoadDir(dir);
                Assert.Equal("120", store.Get("ui.font_scale", ""));
                Assert.Equal("enabled", store.Get("llm.thinking", ""));
                Assert.Equal("缺省值", store.Get("nosuch.key", "缺省值"));
            }
            finally
            {
                CleanDir(dir);
            }
        }

        /// <summary>写入——内存生效且按段落盘，读回一致。</summary>
        [Fact]
        public void SetChecked_WritesAndPersists()
        {
            string dir = NewTempDir();
            try
            {
                ConfigStore store = ConfigStore.LoadDir(dir);
                string error;
                Assert.True(store.SetChecked("ui.user_name", "阿莎", out error));
                Assert.Equal("", error);
                Assert.Equal("阿莎", store.Get("ui.user_name", ""));
                ConfigStore reload = ConfigStore.LoadDir(dir);
                Assert.Equal("阿莎", reload.Get("ui.user_name", ""));
            }
            finally
            {
                CleanDir(dir);
            }
        }

        /// <summary>写入防御——键名非法（等号 / 空格 / 点开头）与超长值一律拒绝。</summary>
        [Fact]
        public void SetChecked_RejectsBadKeyAndLongValue()
        {
            string dir = NewTempDir();
            try
            {
                ConfigStore store = ConfigStore.LoadDir(dir);
                string error;
                Assert.False(store.SetChecked("bad key", "1", out error));
                Assert.Contains("只允许", error);
                Assert.False(store.SetChecked("bad=key", "1", out error));
                Assert.False(store.SetChecked(".leading", "1", out error));
                string longValue = new string('x', 2000);
                Assert.False(store.SetChecked("ui.user_name", longValue, out error));
                Assert.Contains("过长", error);
            }
            finally
            {
                CleanDir(dir);
            }
        }

        /// <summary>还原——删除落盘值（回到未设置），文件内不留残留行。</summary>
        [Fact]
        public void ResetToDefault_RemovesPersistedValue()
        {
            string dir = NewTempDir();
            try
            {
                File.WriteAllText(Path.Combine(dir, "ui.cfg"), "ui.font_scale=120\nui.user_name=阿莎\n");
                ConfigStore store = ConfigStore.LoadDir(dir);
                string error;
                Assert.True(store.ResetToDefault("ui.font_scale", out error));
                Assert.Equal("", error);
                Assert.Equal("100", store.Get("ui.font_scale", "100"));
                string text = File.ReadAllText(Path.Combine(dir, "ui.cfg"));
                Assert.DoesNotContain("ui.font_scale", text);
                Assert.Contains("ui.user_name", text);
            }
            finally
            {
                CleanDir(dir);
            }
        }
    }
}
