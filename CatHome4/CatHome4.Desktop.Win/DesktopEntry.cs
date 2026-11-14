using System;

namespace CH4
{
    /// <summary>
    /// 桌面出口入口（design-ch4-platform §一 出口二）——只做一件事：构造 WinForms 外壳桥并交给内核。
    /// 边界：本类不含任何内核逻辑；误在非 Windows 上启动时由工程 TFM（net10.0-windows）与运行时检查双重拒绝。
    /// </summary>
    public static class DesktopEntry
    {
        /// <summary>
        /// 进程入口——把参数与桌面外壳桥交给内核运行入口。
        /// </summary>
        /// <param name="args">命令行参数</param>
        /// <returns>进程退出码</returns>
        public static int Main(string[] args)
        {
            if (!OperatingSystem.IsWindows())
            {
                Console.Error.WriteLine("[CH4] 桌面出口仅支持 Windows——headless 出口请用 CatHome4（检测到 " + Environment.OSVersion.Platform.ToString() + "）");
                return 2;
            }
            return HostEntry.Run(args, new WinHostShell());
        }
    }
}
