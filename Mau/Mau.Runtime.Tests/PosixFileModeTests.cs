using System;
using System.IO;
using Xunit;

namespace Mau.Runtime.Tests
{
    /// <summary>
    /// POSIX 权限位保真测试（A397）——原子替换不丢可执行位 / 受限位，新建文件仍受 umask 约束。
    /// <para>
    /// 仅 POSIX 平台有语义：Windows 上 <see cref="File.GetUnixFileMode"/> / <see cref="File.SetUnixFileMode"/>
    /// 抛 PlatformNotSupportedException，用例直接返回（Windows 侧行为由「空操作」契约保证）。
    /// </para>
    /// </summary>
    public sealed class PosixFileModeTests
    {
        /// <summary>普通权限位掩码——user / group / other 三段 rwx。</summary>
        private const UnixFileMode OrdinaryMask =
            UnixFileMode.UserRead | UnixFileMode.UserWrite | UnixFileMode.UserExecute
            | UnixFileMode.GroupRead | UnixFileMode.GroupWrite | UnixFileMode.GroupExecute
            | UnixFileMode.OtherRead | UnixFileMode.OtherWrite | UnixFileMode.OtherExecute;

        /// <summary>0755——可执行脚本的典型权限。</summary>
        private const UnixFileMode Mode0755 =
            UnixFileMode.UserRead | UnixFileMode.UserWrite | UnixFileMode.UserExecute
            | UnixFileMode.GroupRead | UnixFileMode.GroupExecute
            | UnixFileMode.OtherRead | UnixFileMode.OtherExecute;

        /// <summary>0600——私密文件典型权限（组 / 其他全无）。</summary>
        private const UnixFileMode Mode0600 = UnixFileMode.UserRead | UnixFileMode.UserWrite;
        /// <summary>0700——私有目录 / 私密文件的典型权限（组 / 其他全无）。</summary>
        private const UnixFileMode Mode0700 =
            UnixFileMode.UserRead | UnixFileMode.UserWrite | UnixFileMode.UserExecute;

        /// <summary>0664——组可写文件典型权限（umask 022 会被削成 0644 的对照面）。</summary>
        private const UnixFileMode Mode0664 =
            UnixFileMode.UserRead | UnixFileMode.UserWrite
            | UnixFileMode.GroupRead | UnixFileMode.GroupWrite
            | UnixFileMode.OtherRead;

        /// <summary>助手保真——0755 目标文件的权限位复制到待替换文件。</summary>
        [Fact]
        public void PreserveForReplace_KeepsExecutableMode()
        {
            RunPreserveCase(Mode0755);
        }

        /// <summary>助手保真——0600 目标文件的权限位复制到待替换文件。</summary>
        [Fact]
        public void PreserveForReplace_KeepsRestrictiveMode()
        {
            RunPreserveCase(Mode0600);
        }

        /// <summary>助手保真——0664 目标文件的权限位复制到待替换文件（组写位不丢）。</summary>
        [Fact]
        public void PreserveForReplace_KeepsGroupWritableMode()
        {
            RunPreserveCase(Mode0664);
        }

        /// <summary>新建语义——目标不存在时不做任何设置（临时文件 mode 原样保留，不抛异常）。</summary>
        [Fact]
        public void PreserveForReplace_TargetMissing_IsNoOp()
        {
            if (OperatingSystem.IsWindows())
            {
                return;
            }
            string baseDir = TempDir("mau_posix_missing_");
            try
            {
                string target = Path.Combine(baseDir, "absent.txt");
                string temp = Path.Combine(baseDir, "absent.txt.tmp");
                File.WriteAllText(temp, "x");
                UnixFileMode before = File.GetUnixFileMode(temp) & OrdinaryMask;
                PosixFileMode.PreserveForReplace(target, temp);
                Assert.False(File.Exists(target));
                Assert.Equal(before, File.GetUnixFileMode(temp) & OrdinaryMask);
            }
            finally
            {
                TryDelete(baseDir);
            }
        }

        /// <summary>特殊位不复制——04755 目标只带普通权限位下来（setuid 不随替换传播）。</summary>
        [Fact]
        public void PreserveForReplace_DropsSpecialBits()
        {
            if (OperatingSystem.IsWindows())
            {
                return;
            }
            string baseDir = TempDir("mau_posix_special_");
            try
            {
                string target = Path.Combine(baseDir, "s.bin");
                string temp = Path.Combine(baseDir, "s.bin.tmp");
                File.WriteAllText(target, "old");
                File.SetUnixFileMode(target, Mode0755 | UnixFileMode.SetUser);
                File.WriteAllText(temp, "new");
                PosixFileMode.PreserveForReplace(target, temp);
                UnixFileMode mode = File.GetUnixFileMode(temp);
                Assert.Equal(0, (int)(mode & UnixFileMode.SetUser));
                Assert.Equal(Mode0755, mode & OrdinaryMask);
            }
            finally
            {
                TryDelete(baseDir);
            }
        }

        /// <summary>消费点实锤——text-write（WriteText）覆写可执行脚本后权限保持不变；新建文件不带执行位。</summary>
        [Fact]
        public void FileSystemService_WriteText_KeepsExecutableMode()
        {
            if (OperatingSystem.IsWindows())
            {
                return;
            }
            string baseDir = TempDir("mau_posix_write_");
            string writable = Path.Combine(baseDir, "rw");
            Directory.CreateDirectory(writable);
            try
            {
                WorkspaceConfig.RootEntry[] entries = new WorkspaceConfig.RootEntry[]
                {
                    new WorkspaceConfig.RootEntry() { Id = "rw", Path = writable, Writable = true }
                };
                FileSystemService fs = new FileSystemService(entries, Path.Combine(writable, "recycle"));
                string script = Path.Combine(writable, "run.sh");
                fs.WriteText(script, "#!/bin/sh\necho old\n");
                File.SetUnixFileMode(script, Mode0755);
                fs.WriteText(script, "#!/bin/sh\necho new\n");
                Assert.Equal(Mode0755, File.GetUnixFileMode(script) & OrdinaryMask);
                Assert.Equal("#!/bin/sh\necho new\n", fs.ReadText(script));
                // 新建文件走 umask 默认——不携带执行位（保真不越界到新建面）
                string fresh = Path.Combine(writable, "new.txt");
                fs.WriteText(fresh, "x");
                UnixFileMode freshMode = File.GetUnixFileMode(fresh);
                Assert.Equal(0, (int)(freshMode & UnixFileMode.UserExecute));
                Assert.Equal(0, (int)(freshMode & UnixFileMode.GroupExecute));
                Assert.Equal(0, (int)(freshMode & UnixFileMode.OtherExecute));
                // 无临时文件残留
                Assert.False(File.Exists(script + ".tmp"));
            }
            finally
            {
                TryDelete(baseDir);
            }
        }

        /// <summary>公共用例体——按给定权限建立目标后调助手，断言待替换文件继承同一普通权限位。</summary>
        /// <param name="mode">目标文件的普通权限位</param>
        private static void RunPreserveCase(UnixFileMode mode)
        {
            if (OperatingSystem.IsWindows())
            {
                return;
            }
            string baseDir = TempDir("mau_posix_case_");
            try
            {
                string target = Path.Combine(baseDir, "target.txt");
                string temp = Path.Combine(baseDir, "target.txt.tmp");
                File.WriteAllText(target, "old");
                File.SetUnixFileMode(target, mode);
                File.WriteAllText(temp, "new");
                PosixFileMode.PreserveForReplace(target, temp);
                Assert.Equal(mode, File.GetUnixFileMode(temp) & OrdinaryMask);
            }
            finally
            {
                TryDelete(baseDir);
            }
        }

        /// <summary>建立独立临时目录。</summary>
        /// <param name="prefix">目录名前缀</param>
        /// <returns>目录绝对路径</returns>
        private static string TempDir(string prefix)
        {
            string dir = Path.Combine(Path.GetTempPath(), prefix + Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(dir);
            return dir;
        }

        /// <summary>清理临时目录（失败静默——测试残留不影响判据）。</summary>
        /// <param name="dir">目录路径</param>
        private static void TryDelete(string dir)
        {
            try
            {
                if (Directory.Exists(dir))
                {
                    Directory.Delete(dir, true);
                }
            }
            catch (IOException exception)
            {
                System.Diagnostics.Debug.WriteLine("临时目录清理失败: " + exception.Message);
            }
            catch (UnauthorizedAccessException exception)
            {
                System.Diagnostics.Debug.WriteLine("临时目录清理被拒绝: " + exception.Message);
            }
        }
        /// <summary>消费点实锤——FileSystemService.Copy 整棵复制私有目录（0700）后，根与子目录权限位保真（A398）。</summary>
        [Fact]
        public void FileSystemService_Copy_DirectoryKeepsMode()
        {
            if (OperatingSystem.IsWindows())
            {
                return;
            }
            string baseDir = TempDir("mau_posix_copydir_");
            try
            {
                string writable = Path.Combine(baseDir, "rw");
                Directory.CreateDirectory(writable);
                WorkspaceConfig.RootEntry[] entries = new WorkspaceConfig.RootEntry[]
                {
                            new WorkspaceConfig.RootEntry() { Id = "rw", Path = writable, Writable = true }
                };
                FileSystemService fs = new FileSystemService(entries, Path.Combine(writable, "recycle"));
                string source = Path.Combine(writable, "src");
                string nested = Path.Combine(source, "secrets");
                Directory.CreateDirectory(nested);
                File.WriteAllText(Path.Combine(nested, "key.txt"), "k");
                string script = Path.Combine(source, "run.sh");
                File.WriteAllText(script, "#!/bin/sh\n");
                File.SetUnixFileMode(script, Mode0755);
                File.SetUnixFileMode(nested, Mode0700);
                File.SetUnixFileMode(source, Mode0700);

                string copy = Path.Combine(writable, "copy");
                fs.Copy(source, copy);

                // 目录自身权限位按源保真——不再退化成 0777 & ~umask
                Assert.Equal(Mode0700, File.GetUnixFileMode(copy) & OrdinaryMask);
                Assert.Equal(Mode0700, File.GetUnixFileMode(Path.Combine(copy, "secrets")) & OrdinaryMask);
                // 文件面（File.Copy 自带保真）与内容照旧
                Assert.Equal(Mode0755, File.GetUnixFileMode(Path.Combine(copy, "run.sh")) & OrdinaryMask);
                Assert.Equal("k", fs.ReadText(Path.Combine(copy, "secrets", "key.txt")));
            }
            finally
            {
                TryDelete(baseDir);
            }
        }
        /// <summary>特殊位不复制——源目录带 setgid 时副本只继承普通权限位。</summary>
        [Fact]
        public void PreserveForCopy_DropsSpecialBits()
        {
            if (OperatingSystem.IsWindows())
            {
                return;
            }
            string baseDir = TempDir("mau_posix_copy_special_");
            try
            {
                string source = Path.Combine(baseDir, "src");
                string destination = Path.Combine(baseDir, "dst");
                Directory.CreateDirectory(source);
                File.SetUnixFileMode(source, Mode0700 | UnixFileMode.SetGroup);
                Directory.CreateDirectory(destination);
                PosixFileMode.PreserveForCopy(source, destination);
                UnixFileMode mode = File.GetUnixFileMode(destination);
                Assert.Equal(0, (int)(mode & UnixFileMode.SetGroup));
                Assert.Equal(0, (int)(mode & UnixFileMode.SetUser));
                Assert.Equal(Mode0700, mode & OrdinaryMask);
            }
            finally
            {
                TryDelete(baseDir);
            }
        }
    }
}
