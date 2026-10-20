using System;
using System.Collections.Generic;
using System.IO;
using CatHome4.Admin;
using Mau.Runtime;
using Xunit;

namespace CatHome4.Core.Tests
{
    /// <summary>
    /// db 受控根与 cat.cfg dbLibs 字段测试（A266）——自愈幂等 + 只读固定列 + 字段读写往返。
    /// </summary>
    public class AdminDbRootTests
    {
        /// <summary>
        /// 首次自愈——补建目录 + 补入只读根 + workspace.json 落盘（含 db 条目）。
        /// </summary>
        [Fact]
        public void EnsureDbRoot_FirstCall_AddsReadOnlyRootAndWritesFile()
        {
            string tempRoot = NewTempRoot();
            try
            {
                WorkspaceConfig ws = new WorkspaceConfig();
                ws.Roots = new WorkspaceConfig.RootEntry[] { MakeRoot("workspace", tempRoot) };
                string note = AdminService.EnsureDbRoot(ws, tempRoot);
                Assert.NotEqual("", note);
                WorkspaceConfig.RootEntry db = FindRoot(ws, "db");
                Assert.NotNull(db);
                Assert.False(db.Writable);
                Assert.Equal(WorkspaceConfig.NormalizeRoot(Path.Combine(tempRoot, "Data", "db")), db.Path);
                Assert.True(Directory.Exists(db.Path));
                string wsPath = Path.Combine(tempRoot, "Data", "config", "workspace.json");
                Assert.True(File.Exists(wsPath));
                string text = File.ReadAllText(wsPath);
                Assert.Contains("\"db\"", text);
                // 幂等——二次调用不再动作
                Assert.Equal("", AdminService.EnsureDbRoot(ws, tempRoot));
            }
            finally
            {
                SafeDelete(tempRoot);
            }
        }

        /// <summary>
        /// 目录已存在但根条目缺失——补条目并落盘（升级形态：库目录先于配置存在）。
        /// </summary>
        [Fact]
        public void EnsureDbRoot_EntryMissingButDirExists_AddsEntry()
        {
            string tempRoot = NewTempRoot();
            try
            {
                Directory.CreateDirectory(Path.Combine(tempRoot, "Data", "db"));
                WorkspaceConfig ws = new WorkspaceConfig();
                ws.Roots = new WorkspaceConfig.RootEntry[] { MakeRoot("workspace", tempRoot) };
                Assert.NotEqual("", AdminService.EnsureDbRoot(ws, tempRoot));
                Assert.NotNull(FindRoot(ws, "db"));
                Assert.Equal("", AdminService.EnsureDbRoot(ws, tempRoot));
            }
            finally
            {
                SafeDelete(tempRoot);
            }
        }

        /// <summary>
        /// cat.cfg 读取——dbLibs 数组解析（缺字段返回空数组，不抛）。
        /// </summary>
        [Fact]
        public void LoadCatCfg_DbLibs_Parsed()
        {
            string tempRoot = NewTempRoot();
            try
            {
                string cfgPath = Path.Combine(tempRoot, "cat.cfg");
                File.WriteAllText(cfgPath, "{\"id\":\"t1\",\"displayName\":\"测试\",\"dbLibs\":[\"ccbp\",\"notes\"]}");
                AdminService.CatCfgData cfg = AdminService.LoadCatCfg(cfgPath);
                Assert.NotNull(cfg);
                Assert.Equal(new string[] { "ccbp", "notes" }, cfg.DbLibs);
            }
            finally
            {
                SafeDelete(tempRoot);
            }
        }

        /// <summary>
        /// cat.cfg 读取——缺 dbLibs 字段返回空数组（旧档兼容；不回落 null）。
        /// </summary>
        [Fact]
        public void LoadCatCfg_DbLibsMissing_EmptyArray()
        {
            string tempRoot = NewTempRoot();
            try
            {
                string cfgPath = Path.Combine(tempRoot, "cat.cfg");
                File.WriteAllText(cfgPath, "{\"id\":\"t1\",\"displayName\":\"测试\"}");
                AdminService.CatCfgData cfg = AdminService.LoadCatCfg(cfgPath);
                Assert.NotNull(cfg);
                Assert.Empty(cfg.DbLibs);
            }
            finally
            {
                SafeDelete(tempRoot);
            }
        }
        /// <summary>
        /// 自愈落盘不丢根——磁盘原文含不可达根（加载期被跳过）时，写回仍保留该根。
        /// </summary>
        [Fact]
        public void EnsureDbRoot_KeepsUnreachableRootOnDisk()
        {
            string tempRoot = NewTempRoot();
            try
            {
                string wsPath = Path.Combine(tempRoot, "Data", "config", "workspace.json");
                string ghostPath = Path.Combine(tempRoot, "ghost_dir_absent").Replace('\\', '/');
                string originalJson = "{\"roots\":[{\"id\":\"workspace\",\"path\":\"" + tempRoot.Replace('\\', '/') + "\",\"writable\":true},{\"id\":\"ghost\",\"path\":\"" + ghostPath + "\",\"writable\":true}],\"inject\":[{\"file\":\"L1/Tree.md\"}]}";
                File.WriteAllText(wsPath, originalJson);
                WorkspaceConfig ws = WorkspaceConfig.Load(wsPath, tempRoot);
                AdminService.EnsureDbRoot(ws, tempRoot);
                string text = File.ReadAllText(wsPath);
                Assert.Contains("ghost", text);
                Assert.Contains("\"db\"", text);
                Assert.Contains("L1/Tree.md", text);
            }
            finally
            {
                SafeDelete(tempRoot);
            }
        }

        /// <summary>
        /// 固定根校准（A273）——配置里 data 指向别处时校准回数据根，db 一并就位；二次调用幂等。
        /// </summary>
        [Fact]
        public void EnsureFixedRoots_DataRootDrift_CalibratesBack()
        {
            string tempRoot = NewTempRoot();
            try
            {
                string otherData = Path.Combine(tempRoot, "other_data");
                Directory.CreateDirectory(otherData);
                WorkspaceConfig ws = new WorkspaceConfig();
                ws.Roots = new WorkspaceConfig.RootEntry[] { MakeRoot("workspace", tempRoot), MakeRoot("data", otherData) };
                string note = AdminService.EnsureFixedRoots(ws, tempRoot);
                Assert.NotEqual("", note);
                WorkspaceConfig.RootEntry data = FindRoot(ws, "data");
                Assert.NotNull(data);
                Assert.Equal(WorkspaceConfig.NormalizeRoot(Path.Combine(tempRoot, "Data")), data.Path);
                Assert.NotNull(FindRoot(ws, "db"));
                // 幂等——二次调用不再动作
                Assert.Equal("", AdminService.EnsureFixedRoots(ws, tempRoot));
            }
            finally
            {
                SafeDelete(tempRoot);
            }
        }

        /// <summary>
        /// 写面校验（A273）——data / db 的 path 提交系统值以外一律拒绝（不静默覆盖）。
        /// </summary>
        [Fact]
        public void ValidateRootsInput_LockedRootPathDiffers_Rejected()
        {
            string tempRoot = NewTempRoot();
            try
            {
                List<AdminService.WorkspaceRootInput> roots = MakeFixedRoots(tempRoot, Path.Combine(tempRoot, "other_db"));
                Directory.CreateDirectory(Path.Combine(tempRoot, "other_db"));
                string err = AdminService.ValidateRootsInput(roots, null, tempRoot);
                Assert.Contains("不可修改", err);
            }
            finally
            {
                SafeDelete(tempRoot);
            }
        }

        /// <summary>
        /// 写面校验（A273）——data / db 提交系统值时通过（正常保存不被锁死）。
        /// </summary>
        [Fact]
        public void ValidateRootsInput_LockedRootPathSame_Passes()
        {
            string tempRoot = NewTempRoot();
            try
            {
                string dbPath = Path.Combine(tempRoot, "Data", "db");
                Directory.CreateDirectory(dbPath);
                List<AdminService.WorkspaceRootInput> roots = MakeFixedRoots(tempRoot, dbPath);
                string err = AdminService.ValidateRootsInput(roots, null, tempRoot);
                Assert.Equal("", err);
            }
            finally
            {
                SafeDelete(tempRoot);
            }
        }

        /// <summary>
        /// 临时数据根——每次测试独立目录（避免共享状态）。
        /// </summary>
        /// <returns>临时目录绝对路径（含 Data/config 子目录）</returns>
        private static string NewTempRoot()
        {
            string tempRoot = Path.Combine(Path.GetTempPath(), "ch4_dbroot_" + Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(Path.Combine(tempRoot, "Data", "config"));
            return tempRoot;
        }

        /// <summary>
        /// 构造根条目——可写测试根。
        /// </summary>
        /// <param name="id">根 id</param>
        /// <param name="path">根路径</param>
        /// <returns>根条目</returns>
        private static WorkspaceConfig.RootEntry MakeRoot(string id, string path)
        {
            WorkspaceConfig.RootEntry entry = new WorkspaceConfig.RootEntry();
            entry.Id = id;
            entry.Path = path;
            entry.Writable = true;
            entry.Note = "";
            return entry;
        }

        /// <summary>
        /// 构造 5 个固定根的提交输入（A273 校验用例）——db 路径由调用方给定，其余目录在临时根内真实创建。
        /// </summary>
        /// <param name="tempRoot">临时数据根</param>
        /// <param name="dbPath">db 根提交路径</param>
        /// <returns>固定根输入列表</returns>
        private static List<AdminService.WorkspaceRootInput> MakeFixedRoots(string tempRoot, string dbPath)
        {
            string mauDir = Path.Combine(tempRoot, "mau");
            string outDir = Path.Combine(tempRoot, "mauout");
            Directory.CreateDirectory(mauDir);
            Directory.CreateDirectory(outDir);
            List<AdminService.WorkspaceRootInput> roots = new List<AdminService.WorkspaceRootInput>();
            roots.Add(MakeInput("workspace", tempRoot));
            roots.Add(MakeInput("mau", mauDir));
            roots.Add(MakeInput("mauout", outDir));
            roots.Add(MakeInput("data", Path.Combine(tempRoot, "Data")));
            roots.Add(MakeInput("db", dbPath));
            return roots;
        }

        /// <summary>
        /// 构造受控根提交输入。
        /// </summary>
        /// <param name="id">根标识</param>
        /// <param name="path">根路径</param>
        /// <returns>输入条目</returns>
        private static AdminService.WorkspaceRootInput MakeInput(string id, string path)
        {
            AdminService.WorkspaceRootInput input = new AdminService.WorkspaceRootInput();
            input.Id = id;
            input.Path = path;
            input.Writable = true;
            input.Note = "";
            return input;
        }

        /// <summary>
        /// 按 id 查根——未命中返回 null。
        /// </summary>
        /// <param name="ws">受控根配置</param>
        /// <param name="id">根 id</param>
        /// <returns>根条目；未命中 null</returns>
        private static WorkspaceConfig.RootEntry FindRoot(WorkspaceConfig ws, string id)
        {
            for (int i = 0; i < ws.Roots.Length; i = i + 1)
            {
                if (string.Equals(ws.Roots[i].Id, id, StringComparison.OrdinalIgnoreCase))
                {
                    return ws.Roots[i];
                }
            }
            return null;
        }

        /// <summary>
        /// 清理临时目录——异常不阻断（临时目录残留可接受）。
        /// </summary>
        /// <param name="dir">目录路径</param>
        private static void SafeDelete(string dir)
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
                // 清理失败不构成测试失败（临时目录残留可接受）——异常仅记录到调试输出
                System.Diagnostics.Debug.WriteLine(ex.Message);
            }
        }
    }
}
