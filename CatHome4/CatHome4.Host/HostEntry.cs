using System;
using CatHome4.Contracts;

namespace CH4
{
    /// <summary>
    /// 内核运行入口——两个出口（桌面 / headless）各自的 Main 只做一件事：构造外壳桥并调用本方法。
    /// 边界（design-ch4-platform §一 R2）：双出口同进程托管内核，零 IPC；本方法不感知出口形态。
    /// </summary>
    public static class HostEntry
    {
        /// <summary>
        /// 运行内核——设置接入面后交给内核主流程（参数路由 / 启动链 / 主循环 / 收尾）。
        /// </summary>
        /// <param name="args">命令行参数（原样转交内核参数面校验）</param>
        /// <param name="shell">外壳桥实现（桌面 WinForms / headless 控制台）</param>
        /// <returns>进程退出码</returns>
        public static int Run(string[] args, IHostShell shell)
        {
            if (shell == null)
            {
                throw new ArgumentNullException(nameof(shell));
            }
            HostAccess.Shell = shell;
            HostAccess.View = new HostView();
            return Program.RunHost(args);
        }
    }
}
