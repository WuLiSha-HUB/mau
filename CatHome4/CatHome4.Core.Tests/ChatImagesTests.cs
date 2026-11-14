using System;
using System.IO;
using CatHome4.Admin;
using Mau.Runtime;
using Xunit;

namespace CatHome4.Core.Tests
{
    /// <summary>
    /// 对话图片附件测试（A65 Admin 面纯逻辑）——类型映射 / 文件名安全判定 / 内容寻址命名 / 目录解析。
    /// 端点 IO 面（上传落盘 / 取图）走部署实测（design-ch4-chat-images §九）。
    /// </summary>
    [Collection("GlobalToolState")]
    public sealed class ChatImagesTests
    {
        /// <summary>
        /// 类型白名单——命中返回扩展名；未知类型返回空串（入口面零容忍：调用方据此拒绝）。
        /// </summary>
        [Fact]
        public void ImageExtOf_Whitelist_MapsAndRejects()
        {
            Assert.Equal(".png", AdminService.ImageExtOf("image/png"));
            Assert.Equal(".jpg", AdminService.ImageExtOf("image/jpeg"));
            Assert.Equal(".jpg", AdminService.ImageExtOf("image/jpg"));
            Assert.Equal(".webp", AdminService.ImageExtOf("image/webp"));
            Assert.Equal(".gif", AdminService.ImageExtOf("image/gif"));
            Assert.Equal(".bmp", AdminService.ImageExtOf("image/bmp"));
            Assert.Equal("", AdminService.ImageExtOf("image/svg+xml"));
            Assert.Equal("", AdminService.ImageExtOf("text/plain"));
            Assert.Equal("", AdminService.ImageExtOf(""));
        }

        /// <summary>
        /// 扩展名 → MIME——大小写不敏感；未知回落 application/octet-stream。
        /// </summary>
        [Fact]
        public void ImageMimeOfExt_KnownAndUnknown()
        {
            Assert.Equal("image/png", AdminService.ImageMimeOfExt(".png"));
            Assert.Equal("image/jpeg", AdminService.ImageMimeOfExt(".JPG"));
            Assert.Equal("image/webp", AdminService.ImageMimeOfExt(".webp"));
            Assert.Equal("application/octet-stream", AdminService.ImageMimeOfExt(".exe"));
            Assert.Equal("application/octet-stream", AdminService.ImageMimeOfExt(""));
        }

        /// <summary>
        /// 文件名安全判定——取图端点只接受纯文件名（分隔符 / 上级引用 / 空串一律拒绝）。
        /// </summary>
        [Fact]
        public void IsSafeImageName_TraversalAndSeparators_Rejected()
        {
            Assert.True(AdminService.IsSafeImageName("a1b2c3d4e5f60718.png"));
            Assert.False(AdminService.IsSafeImageName(""));
            Assert.False(AdminService.IsSafeImageName("../secret.txt"));
            Assert.False(AdminService.IsSafeImageName("..\\secret.txt"));
            Assert.False(AdminService.IsSafeImageName("sub/a.png"));
            Assert.False(AdminService.IsSafeImageName("sub\\a.png"));
            Assert.False(AdminService.IsSafeImageName(".."));
            if (OperatingSystem.IsWindows())
            {
                // Windows 下 "C:" 是驱动器引用（Path.GetFileName 返空串）需拒；
                // Linux 下 ":" 不是分隔符，它是合法纯文件名，判据不覆盖
                Assert.False(AdminService.IsSafeImageName("C:"));
            }
        }

        /// <summary>
        /// 内容寻址命名——同内容同名（去重前提）、异内容异名；16 位十六进制 + 扩展名。
        /// </summary>
        [Fact]
        public void ImageNameOf_ContentAddressed()
        {
            byte[] a = new byte[] { 1, 2, 3, 4 };
            byte[] b = new byte[] { 1, 2, 3, 5 };
            string na = AdminService.ImageNameOf(a, ".png");
            string nb = AdminService.ImageNameOf(a, ".png");
            string nc = AdminService.ImageNameOf(b, ".png");
            Assert.Equal(na, nb);
            Assert.NotEqual(na, nc);
            Assert.Equal(20, na.Length);
            Assert.EndsWith(".png", na);
            Assert.Matches("^[0-9a-f]{16}\\.png$", na);
        }

        /// <summary>
        /// 目录解析——Data 下相对名（换机不改配置）；测试后恢复静态现场。
        /// </summary>
        [Fact]
        public void ImageDirPath_BelowDataRoot()
        {
            string saved = AdminService._dataRoot;
            try
            {
                AdminService._dataRoot = "X";
                Assert.Equal(Path.Combine("X", "Data", "chat-images"), AdminService.ImageDirPath("chat-images"));
            }
            finally
            {
                AdminService._dataRoot = saved;
            }
        }

        /// <summary>
        /// 目录白名单——首项为写入目录且落在白名单内（取图查找面含预留目录）。
        /// </summary>
        [Fact]
        public void ImageDirs_WriteDirIsFirstEntry()
        {
            Assert.True(AdminService.ImageDirs.Length >= 1);
            Assert.Equal(AdminService.ImageWriteDir, AdminService.ImageDirs[0]);
        }

        /// <summary>
        /// 受控根寻址判定——根寻址真、盘符绝对路径假（与前端 chatImgIsRootAddress 同判据）。
        /// </summary>
        [Fact]
        public void IsRootAddress_RootFormVsDrivePath()
        {
            Assert.True(RootPathResolver.IsRootAddress("mau:CatTemp/a.png"));
            Assert.True(RootPathResolver.IsRootAddress("mau:a.png"));
            Assert.True(RootPathResolver.IsRootAddress("WorkSpace:out/shot 图.png"));
            Assert.False(RootPathResolver.IsRootAddress(@"C:\x\a.png"));
            Assert.False(RootPathResolver.IsRootAddress("C:/x/a.png"));
            Assert.False(RootPathResolver.IsRootAddress("a1b2.png"));
            Assert.False(RootPathResolver.IsRootAddress(""));
            Assert.False(RootPathResolver.IsRootAddress(null));
        }

        /// <summary>
        /// 受控根图片解析——命中 / 根 id 大小写不敏感 / 根不存在 / 非图片扩展名 / 越界 / 空参 各态一律显式拒绝。
        /// </summary>
        [Fact]
        public void TryResolveRootImage_HitAndGuards()
        {
            WorkspaceConfig ws = new WorkspaceConfig();
            WorkspaceConfig.RootEntry root = new WorkspaceConfig.RootEntry();
            root.Id = "mau";
            root.Path = Path.Combine("X", "repo");
            ws.Roots = new WorkspaceConfig.RootEntry[] { root };
            string full;
            string mime;
            Assert.True(AdminService.TryResolveRootImage(ws, "mau:sub/a.png", out full, out mime));
            Assert.Equal(Path.GetFullPath(Path.Combine("X", "repo", "sub", "a.png")), full);
            Assert.Equal("image/png", mime);
            Assert.True(AdminService.TryResolveRootImage(ws, "MAU:sub/shot 图.webp", out full, out mime));
            Assert.Equal("image/webp", mime);
            Assert.False(AdminService.TryResolveRootImage(ws, "ccbp:L1/Tree.md", out full, out mime));
            Assert.False(AdminService.TryResolveRootImage(ws, "mau:src/Program.cs", out full, out mime));
            Assert.False(AdminService.TryResolveRootImage(ws, "mau:../secret.png", out full, out mime));
            Assert.False(AdminService.TryResolveRootImage(ws, "mau:", out full, out mime));
            Assert.False(AdminService.TryResolveRootImage(null, "mau:a.png", out full, out mime));
        }

        /// <summary>
        /// 受控根内绝对路径解析——根内命中 / 根外拒绝 / 非图片拒绝 / 空参拒绝（模型天然写绝对路径的兜底面）。
        /// </summary>
        [Fact]
        public void TryResolveInsideRoots_HitAndGuards()
        {
            WorkspaceConfig ws = new WorkspaceConfig();
            WorkspaceConfig.RootEntry root = new WorkspaceConfig.RootEntry();
            root.Id = "WorkSpace";
            root.Path = Path.Combine("X", "WorkSpace");
            ws.Roots = new WorkspaceConfig.RootEntry[] { root };
            string full;
            string mime;
            Assert.True(AdminService.TryResolveInsideRoots(ws, Path.Combine("X", "WorkSpace", "pet", "a.gif"), out full, out mime));
            Assert.Equal("image/gif", mime);
            Assert.True(AdminService.TryResolveInsideRoots(ws, Path.Combine("X", "WorkSpace", "shot 图.png"), out full, out mime));
            Assert.False(AdminService.TryResolveInsideRoots(ws, Path.Combine("X", "Other", "a.png"), out full, out mime));
            Assert.False(AdminService.TryResolveInsideRoots(ws, Path.Combine("X", "WorkSpace", "Program.cs"), out full, out mime));
            Assert.False(AdminService.TryResolveInsideRoots(ws, "", out full, out mime));
            Assert.False(AdminService.TryResolveInsideRoots(null, Path.Combine("X", "WorkSpace", "a.png"), out full, out mime));
        }
    }
}
