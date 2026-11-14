using System;
using Mau.Contracts;

namespace Mau.Runtime
{
    /// <summary>
    /// 进程组宿主实现选型——全进程唯一出口（design-ch4-platform §二 约束）。
    /// <para>
    /// 调用方只认 <see cref="Current"/>；非 Windows 环境返回不支持实现（<see cref="Available"/> = false）。
    /// </para>
    /// </summary>
    public static class ProcessGroups
    {
        /// <summary>
        /// 当前环境实现——Windows = Job 对象（<see cref="ProcessGroupWindows"/>）· 其它 = 进程组位（<see cref="ProcessGroupLinux"/>，P5 补齐）。
        /// </summary>
        public static IProcessGroupHost Current { get; } = Resolve();

        /// <summary>
        /// 实现解析——只在类型初始化时执行一次。
        /// </summary>
        /// <returns>进程组宿主实现</returns>
        private static IProcessGroupHost Resolve()
        {
            if (OperatingSystem.IsWindows())
            {
                return new ProcessGroupWindows();
            }
            return new ProcessGroupLinux();
        }
    }
}
