using System;
using System.IO;
using System.Text;
using Mau.Runtime;
using Xunit;

namespace Mau.Runtime.Tests
{
    /// <summary>
    /// 启动韧性——配置加载降级测试（A260）：workspace.json / schema.json 损坏与根不可达一律不抛，事实落 LoadDegrades / LoadDegrade。
    /// 隔离：临时目录 Guid 唯一命名 + finally 清理。
    /// </summary>
    public sealed class BootstrapResilienceTests
    {
        /// <summary>建一个唯一临时目录（不创建子项）。</summary>
        /// <param name="prefix">目录名前缀</param>
        /// <returns>绝对路径</returns>
        private static string NewTempDir(string prefix)
        {
            string dir = Path.Combine(Path.GetTempPath(), prefix + "_" + Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(dir);
            return dir;
        }

        /// <summary>递归清理临时目录（尽力）。</summary>
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
            catch (Exception)
            {
                // 清理失败不影响断言
            }
        }

        /// <summary>
        /// JSON 损坏——不抛，回落默认单根，坏档备份，降级事实记 CFG_CORRUPT。
        /// </summary>
        [Fact]
        public void WorkspaceLoad_CorruptJson_FallsBackToSingleRoot()
        {
            string dir = NewTempDir("ch4ws");
            try
            {
                string path = Path.Combine(dir, "workspace.json");
                File.WriteAllText(path, "{ this is not json", new UTF8Encoding(false));
                WorkspaceConfig cfg = WorkspaceConfig.Load(path, dir);
                Assert.Single(cfg.Roots);
                Assert.Equal("workspace", cfg.Roots[0].Id);
                Assert.True(cfg.LoadDegrades.Count >= 1);
                Assert.Contains("CFG_CORRUPT", cfg.LoadDegrades[0]);
                bool hasBackup = false;
                string[] files = Directory.GetFiles(dir, "workspace.json.bad-*");
                if (files.Length > 0)
                {
                    hasBackup = true;
                }
                Assert.True(hasBackup);
            }
            finally
            {
                CleanDir(dir);
            }
        }

        /// <summary>
        /// roots 项缺 id/path——跳过该项并记 ROOT_ITEM_INVALID；其余有效项保留。
        /// </summary>
        [Fact]
        public void WorkspaceLoad_InvalidRootItem_SkipsEntry()
        {
            string dir = NewTempDir("ch4ws");
            try
            {
                string valid = Path.Combine(dir, "data");
                Directory.CreateDirectory(valid);
                string path = Path.Combine(dir, "workspace.json");
                string json = "{\"roots\":[{\"id\":\"workspace\",\"path\":\"" + valid.Replace("\\", "/") + "\"},{\"id\":\"\",\"path\":\"\"}]}";
                File.WriteAllText(path, json, new UTF8Encoding(false));
                WorkspaceConfig cfg = WorkspaceConfig.Load(path, dir);
                Assert.Single(cfg.Roots);
                Assert.Equal("workspace", cfg.Roots[0].Id);
                Assert.Contains("ROOT_ITEM_INVALID", string.Join(";", cfg.LoadDegrades));
            }
            finally
            {
                CleanDir(dir);
            }
        }

        /// <summary>
        /// 常驻根不存在但可创建——自动重建并记 ROOT_REBUILT（不抛）。
        /// </summary>
        [Fact]
        public void WorkspaceLoad_ResidentRootMissing_Rebuilds()
        {
            string dir = NewTempDir("ch4ws");
            try
            {
                string missing = Path.Combine(dir, "workspace_root");
                string path = Path.Combine(dir, "workspace.json");
                string json = "{\"roots\":[{\"id\":\"workspace\",\"path\":\"" + missing.Replace("\\", "/") + "\"}]}";
                File.WriteAllText(path, json, new UTF8Encoding(false));
                WorkspaceConfig cfg = WorkspaceConfig.Load(path, dir);
                Assert.True(Directory.Exists(missing));
                Assert.Single(cfg.Roots);
                Assert.Contains("ROOT_REBUILT", string.Join(";", cfg.LoadDegrades));
            }
            finally
            {
                CleanDir(dir);
            }
        }

        /// <summary>
        /// 常驻根不可达且不可创建——回落数据根并记 ROOT_UNREACHABLE（不抛）。
        /// </summary>
        [Fact]
        public void WorkspaceLoad_ResidentRootUnreachable_FallsBackToDefaultRoot()
        {
            string dir = NewTempDir("ch4ws");
            try
            {
                string path = Path.Combine(dir, "workspace.json");
                // 不存在的盘符 + 非法目录名组合：CreateDirectory 必失败
                string json = "{\"roots\":[{\"id\":\"workspace\",\"path\":\"Q:/ch4_no_such_dir_zz\"}]}";
                File.WriteAllText(path, json, new UTF8Encoding(false));
                WorkspaceConfig cfg = WorkspaceConfig.Load(path, dir);
                Assert.Single(cfg.Roots);
                Assert.Equal(WorkspaceConfig.NormalizeRoot(dir), cfg.Roots[0].Path);
                Assert.Contains("ROOT_UNREACHABLE", string.Join(";", cfg.LoadDegrades));
            }
            finally
            {
                CleanDir(dir);
            }
        }

        /// <summary>
        /// schema.json 损坏——不抛，返回空 schema，事实记 LoadDegrade（含备份标记）。
        /// </summary>
        [Fact]
        public void SchemaLoad_CorruptJson_ReturnsEmptySchemaWithDegrade()
        {
            string dir = NewTempDir("ch4schema");
            try
            {
                string path = Path.Combine(dir, "schema.json");
                File.WriteAllText(path, "[[[", new UTF8Encoding(false));
                ConfigSchema schema = ConfigSchema.Load(path);
                Assert.NotNull(schema);
                Assert.Empty(schema.All());
                Assert.Contains("CFG_CORRUPT", schema.LoadDegrade);
                string[] backups = Directory.GetFiles(dir, "schema.json.bad-*");
                Assert.True(backups.Length > 0);
            }
            finally
            {
                CleanDir(dir);
            }
        }

        /// <summary>
        /// 配置文件缺失——既有语义不变（空 schema / 默认单根），且不产生降级事实。
        /// </summary>
        [Fact]
        public void Load_MissingFiles_NoDegrade()
        {
            string dir = NewTempDir("ch4missing");
            try
            {
                WorkspaceConfig cfg = WorkspaceConfig.Load(Path.Combine(dir, "none.json"), dir);
                Assert.Single(cfg.Roots);
                Assert.Empty(cfg.LoadDegrades);
                ConfigSchema schema = ConfigSchema.Load(Path.Combine(dir, "none-schema.json"));
                Assert.Equal("", schema.LoadDegrade);
            }
            finally
            {
                CleanDir(dir);
            }
        }
    }
}
