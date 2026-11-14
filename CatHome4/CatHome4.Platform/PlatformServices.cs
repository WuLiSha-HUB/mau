using System;
using CatHome4.Contracts;
using Mau.Runtime;
using Mau.Contracts;

namespace CatHome4.Platform
{
    /// <summary>
    /// 平台服务选型——全进程唯一出口（design-ch4-platform §二 约束：不做逐调用点判断）。
    /// <para>
    /// 按 <see cref="OperatingSystem.IsWindows"/> 一次性选型：Windows 走 <c>Windows/</c>，其余走 <c>Linux/</c>（P5 补齐）。
    /// 启动期只做两件事：实现选型 + 依赖缺失登记（design-ch4-platform §三）。
    /// </para>
    /// </summary>
    public static class PlatformServices
    {
        /// <summary>进程树终止服务。</summary>
        public static IProcessService ProcessService { get; } = CreateProcessService();

        /// <summary>外壳启动器（打开目录 / 链接）。</summary>
        public static IShellLauncher ShellLauncher { get; } = CreateShellLauncher();

        /// <summary>开机自启物化服务。</summary>
        public static IAutoStartService AutoStart { get; } = CreateAutoStart();

        /// <summary>路径平台面（默认值 / 比较口径 / 可执行名）。</summary>
        public static IPathPlatform Paths { get; } = CreatePaths();

        /// <summary>文件占用探测（部署切换前置）。</summary>
        public static IFileLockProbe FileLockProbe { get; } = CreateFileLockProbe();

        /// <summary>命令行服务（Linux 单行指令面；Windows 不可用）。</summary>
        public static IShellService Shell { get; } = CreateShell("");

        /// <summary>
        /// 进程服务选型。
        /// </summary>
        /// <returns>进程服务实现</returns>
        private static IProcessService CreateProcessService()
        {
            if (OperatingSystem.IsWindows())
            {
                return new Windows.WindowsProcessService();
            }
            return new Linux.LinuxProcessService();
        }

        /// <summary>
        /// 外壳启动器选型。
        /// </summary>
        /// <returns>外壳启动器实现</returns>
        private static IShellLauncher CreateShellLauncher()
        {
            if (OperatingSystem.IsWindows())
            {
                return new Windows.WindowsShellLauncher();
            }
            return new Linux.LinuxShellLauncher();
        }

        /// <summary>
        /// 自启服务选型。
        /// </summary>
        /// <returns>自启服务实现</returns>
        private static IAutoStartService CreateAutoStart()
        {
            if (OperatingSystem.IsWindows())
            {
                return new Windows.WindowsAutoStartService();
            }
            return new Linux.LinuxAutoStartService();
        }

        /// <summary>
        /// 路径平台面选型。
        /// </summary>
        /// <returns>路径平台实现</returns>
        private static IPathPlatform CreatePaths()
        {
            if (OperatingSystem.IsWindows())
            {
                return new Windows.WindowsPathPlatform();
            }
            return new Linux.LinuxPathPlatform();
        }

        /// <summary>
        /// 文件占用探测选型。
        /// </summary>
        /// <returns>占用探测实现</returns>
        private static IFileLockProbe CreateFileLockProbe()
        {
            if (OperatingSystem.IsWindows())
            {
                return new Windows.WindowsFileLockProbe();
            }
            return new Linux.LinuxFileLockProbe();
        }

        /// <summary>
        /// 命令行服务选型——解释器路径由调用方给（配置 <c>sh.shell_path</c>；空串取平台缺省）。
        /// </summary>
        /// <param name="shellPath">bash 可执行路径（Windows 侧忽略）</param>
        /// <returns>命令行服务实现</returns>
        public static IShellService CreateShell(string shellPath)
        {
            if (OperatingSystem.IsWindows())
            {
                return new Windows.WindowsShellService();
            }
            return new Linux.LinuxShellService(shellPath);
        }
    }
}
