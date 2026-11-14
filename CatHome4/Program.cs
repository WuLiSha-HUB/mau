using System;
using CatHome4.Contracts;

namespace CH4
{
    /// <summary>
    /// headless 出口入口（design-ch4-platform §一 出口一）——Linux 生产常驻 / Windows 服务形态。
    /// 边界：本类不含内核逻辑；无 GUI、无托盘，观测走 runs/ 日志 + HTTP 面（SSH 隧道访问）。
    /// </summary>
    public static class HeadlessEntry
    {
        /// <summary>
        /// 进程入口——把参数与无头外壳桥交给内核运行入口。
        /// </summary>
        /// <param name="args">命令行参数</param>
        /// <returns>进程退出码</returns>
        public static int Main(string[] args)
        {
            return HostEntry.Run(args, new HeadlessHostShell());
        }
    }
}
