using System;
using System.IO;
using Xunit;

namespace Mau.Runtime.Tests
{
    /// <summary>
    /// 根寻址公共解析器（A257）测试——形态判定 / id 映射 / 归属判定 / 错误码 / 基准语义。
    /// </summary>
    public sealed class RootPathResolverTests
    {
        /// <summary>
        /// 构造测试根表——两枚根（rw 可写 / ro 只读）
        /// </summary>
        /// <param name="rw">可写根路径</param>
        /// <param name="ro">只读根路径</param>
        /// <returns>根表</returns>
        private static WorkspaceConfig.RootEntry[] MakeRoots(string rw, string ro)
        {
            return new WorkspaceConfig.RootEntry[]
            {
                new WorkspaceConfig.RootEntry() { Id = "rw", Path = rw, Writable = true },
                new WorkspaceConfig.RootEntry() { Id = "ro", Path = ro, Writable = false }
            };
        }

        /// <summary>
        /// 尽力删除临时目录——不掩盖断言结果
        /// </summary>
        /// <param name="dir">临时目录</param>
        private static void TryDelete(string dir)
        {
            try
            {
                Directory.Delete(dir, true);
            }
            catch (Exception ex)
            {
                Console.WriteLine("[测试清理] 临时目录删除失败: " + ex.Message);
            }
        }

        /// <summary>
        /// 根寻址展开——id:rel 命中 / 大小写不敏感 / 归属根标记
        /// </summary>
        [Fact]
        public void Expand_RootAddress_ResolvesUnderRoot()
        {
            string baseDir = Path.Combine(Path.GetTempPath(), "mau_root_resolver_" + Guid.NewGuid().ToString("N"));
            string rw = Path.Combine(baseDir, "rw");
            string ro = Path.Combine(baseDir, "ro");
            Directory.CreateDirectory(rw);
            Directory.CreateDirectory(ro);
            try
            {
                WorkspaceConfig.RootEntry[] roots = MakeRoots(rw, ro);
                RootResolveResult rr = RootPathResolver.Expand("rw:sub/a.txt", roots);
                Assert.True(rr.Ok);
                Assert.Equal(Path.GetFullPath(Path.Combine(rw, "sub", "a.txt")), rr.FullPath);
                Assert.Equal("rw", rr.RootId);
                RootResolveResult upper = RootPathResolver.Expand("RW:sub/a.txt", roots);
                Assert.True(upper.Ok);
                Assert.Equal(rr.FullPath, upper.FullPath);
            }
            finally
            {
                TryDelete(baseDir);
            }
        }

        /// <summary>
        /// 裸根名——整串即根 id（无冒号）→ 该根根目录
        /// </summary>
        [Fact]
        public void Expand_BareRootId_ResolvesToRootDirectory()
        {
            string baseDir = Path.Combine(Path.GetTempPath(), "mau_root_bare_" + Guid.NewGuid().ToString("N"));
            string rw = Path.Combine(baseDir, "rw");
            string ro = Path.Combine(baseDir, "ro");
            Directory.CreateDirectory(rw);
            Directory.CreateDirectory(ro);
            try
            {
                WorkspaceConfig.RootEntry[] roots = MakeRoots(rw, ro);
                RootResolveResult rr = RootPathResolver.Expand("rw", roots);
                Assert.True(rr.Ok);
                Assert.Equal(Path.GetFullPath(rw), rr.FullPath);
                Assert.Equal("rw", rr.RootId);
            }
            finally
            {
                TryDelete(baseDir);
            }
        }

        /// <summary>
        /// 未知根 id——根寻址形态但 id 未命中 → ROOT_UNKNOWN_ID（失败可见）
        /// </summary>
        [Fact]
        public void Expand_UnknownId_ReturnsErrorCode()
        {
            string baseDir = Path.Combine(Path.GetTempPath(), "mau_root_unknown_" + Guid.NewGuid().ToString("N"));
            string rw = Path.Combine(baseDir, "rw");
            string ro = Path.Combine(baseDir, "ro");
            Directory.CreateDirectory(rw);
            Directory.CreateDirectory(ro);
            try
            {
                WorkspaceConfig.RootEntry[] roots = MakeRoots(rw, ro);
                RootResolveResult rr = RootPathResolver.Expand("nope:sub/a.txt", roots);
                Assert.False(rr.Ok);
                Assert.Equal(RootPathResolver.ErrUnknownId, rr.ErrorCode);
            }
            finally
            {
                TryDelete(baseDir);
            }
        }

        /// <summary>
        /// 盘符不作根寻址——C:/ 形态按普通路径绝对化（不误判未知根）
        /// </summary>
        [Fact]
        public void Expand_DrivePath_TreatedAsPlainPath()
        {
            if (!OperatingSystem.IsWindows())
            {
                Assert.Skip("盘符是 Windows 概念——Linux 无驱动器语义，本断言不适用");
            }
            string baseDir = Path.Combine(Path.GetTempPath(), "mau_root_drive_" + Guid.NewGuid().ToString("N"));
            string rw = Path.Combine(baseDir, "rw");
            string ro = Path.Combine(baseDir, "ro");
            Directory.CreateDirectory(rw);
            Directory.CreateDirectory(ro);
            try
            {
                WorkspaceConfig.RootEntry[] roots = MakeRoots(rw, ro);
                RootResolveResult rr = RootPathResolver.Expand("C:/x/a.png", roots);
                Assert.True(rr.Ok);
                Assert.Equal(Path.GetFullPath("C:/x/a.png"), rr.FullPath);
            }
            finally
            {
                TryDelete(baseDir);
            }
        }

        /// <summary>
        /// 相对路径基准——缺省取根表首根；显式 basePath 覆盖
        /// </summary>
        [Fact]
        public void Expand_RelativePath_UsesBasePath()
        {
            string baseDir = Path.Combine(Path.GetTempPath(), "mau_root_base_" + Guid.NewGuid().ToString("N"));
            string rw = Path.Combine(baseDir, "rw");
            string ro = Path.Combine(baseDir, "ro");
            Directory.CreateDirectory(rw);
            Directory.CreateDirectory(ro);
            try
            {
                WorkspaceConfig.RootEntry[] roots = MakeRoots(rw, ro);
                RootResolveResult def = RootPathResolver.Expand("sub/a.txt", roots);
                Assert.True(def.Ok);
                Assert.Equal(Path.GetFullPath(Path.Combine(rw, "sub", "a.txt")), def.FullPath);
                RootResolveResult over = RootPathResolver.Expand("sub/a.txt", roots, ro);
                Assert.True(over.Ok);
                Assert.Equal(Path.GetFullPath(Path.Combine(ro, "sub", "a.txt")), over.FullPath);
            }
            finally
            {
                TryDelete(baseDir);
            }
        }

        /// <summary>
        /// 归属判定——根寻址命中归属根；越界返回 ROOT_OUT_OF_SCOPE
        /// </summary>
        [Fact]
        public void ResolveOwned_OwnershipAndOutOfScope()
        {
            string baseDir = Path.Combine(Path.GetTempPath(), "mau_root_owned_" + Guid.NewGuid().ToString("N"));
            string rw = Path.Combine(baseDir, "rw");
            string ro = Path.Combine(baseDir, "ro");
            Directory.CreateDirectory(rw);
            Directory.CreateDirectory(ro);
            try
            {
                WorkspaceConfig.RootEntry[] roots = MakeRoots(rw, ro);
                RootResolveResult hit = RootPathResolver.ResolveOwned("rw:sub/a.txt", roots);
                Assert.True(hit.Ok);
                Assert.Equal("rw", hit.RootId);
                Assert.Equal(Path.GetFullPath(rw), hit.RootPath);
                RootResolveResult miss = RootPathResolver.ResolveOwned(Path.Combine(baseDir, "outside.txt"), roots);
                Assert.False(miss.Ok);
                Assert.Equal(RootPathResolver.ErrOutOfScope, miss.ErrorCode);
            }
            finally
            {
                TryDelete(baseDir);
            }
        }

        /// <summary>
        /// 逃逸拒绝——根寻址含 .. 逃出根 → ROOT_OUT_OF_SCOPE
        /// </summary>
        [Fact]
        public void ResolveOwned_Escape_Rejected()
        {
            string baseDir = Path.Combine(Path.GetTempPath(), "mau_root_escape_" + Guid.NewGuid().ToString("N"));
            string rw = Path.Combine(baseDir, "rw");
            string ro = Path.Combine(baseDir, "ro");
            Directory.CreateDirectory(rw);
            Directory.CreateDirectory(ro);
            try
            {
                WorkspaceConfig.RootEntry[] roots = MakeRoots(rw, ro);
                RootResolveResult rr = RootPathResolver.ResolveOwned("rw:../secret.png", roots);
                Assert.False(rr.Ok);
                Assert.Equal(RootPathResolver.ErrOutOfScope, rr.ErrorCode);
            }
            finally
            {
                TryDelete(baseDir);
            }
        }

        /// <summary>
        /// 空路径——Ok + 空串（调用方自决；解析器不抛）
        /// </summary>
        [Fact]
        public void Expand_EmptyPath_ReturnsEmpty()
        {
            WorkspaceConfig.RootEntry[] roots = new WorkspaceConfig.RootEntry[]
            {
                new WorkspaceConfig.RootEntry() { Id = "rw", Path = Path.GetTempPath(), Writable = true }
            };
            RootResolveResult rr = RootPathResolver.Expand("", roots);
            Assert.True(rr.Ok);
            Assert.Equal("", rr.FullPath);
        }

        /// <summary>
        /// 形态判定——根寻址真 / 盘符假 / 纯文件名假 / 空假（与前端镜像同判据）
        /// </summary>
        [Fact]
        public void IsRootAddress_Cases()
        {
            Assert.True(RootPathResolver.IsRootAddress("mau:CatTemp/a.png"));
            Assert.True(RootPathResolver.IsRootAddress("mau:a.png"));
            Assert.True(RootPathResolver.IsRootAddress("WorkSpace:out/shot 图.png"));
            Assert.False(RootPathResolver.IsRootAddress("C:\\x\\a.png"));
            Assert.False(RootPathResolver.IsRootAddress("C:/x/a.png"));
            Assert.False(RootPathResolver.IsRootAddress("a1b2.png"));
            Assert.False(RootPathResolver.IsRootAddress(""));
            Assert.False(RootPathResolver.IsRootAddress(null));
        }
    }
}
