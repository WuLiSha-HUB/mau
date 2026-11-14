using System;
using Mau.Contracts;

namespace Mau.Runtime
{
    /// <summary>
    /// 进程统计实现选型——全进程唯一出口（design-ch4-platform §二 约束）。
    /// <para>
    /// 调用方只认 <see cref="Current"/>，不做逐调用点环境判断；平台差异全部落在实现类内。
    /// </para>
    /// </summary>
    public static class ProcessStats
    {
        /// <summary>
        /// 当前环境实现——Windows = toolhelp32（<see cref="ProcessStatWindows"/>）· 其它 = procfs 位（<see cref="ProcessStatLinux"/>，P5 补齐）。
        /// </summary>
        public static IProcessStat Current { get; } = Resolve();

        /// <summary>
        /// 实现解析——只在类型初始化时执行一次。
        /// </summary>
        /// <returns>进程统计实现</returns>
        private static IProcessStat Resolve()
        {
            if (OperatingSystem.IsWindows())
            {
                return new ProcessStatWindows();
            }
            return new ProcessStatLinux();
        }
    }
}
