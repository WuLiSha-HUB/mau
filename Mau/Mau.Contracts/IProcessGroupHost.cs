using System;
using System.Diagnostics;

namespace Mau.Contracts
{
    /// <summary>
    /// 进程组宿主契约——子进程组的生命周期归属（宿主进程终止即连带终止组内全部进程）。
    /// <para>
    /// 平台差异按环境分实现（Windows = Job 对象 / Linux = 进程组 + 组杀，见 <c>Mau.Runtime/Platform/</c>）。
    /// 组句柄是 <b>不透明对象</b>：Windows 实现里是 Job 句柄，Linux 实现里是进程组标识包装；
    /// 调用方只透传，不解释也不做算术（design-ch4-platform §二）。
    /// </para>
    /// </summary>
    public interface IProcessGroupHost
    {
        /// <summary>
        /// 当前环境是否支持进程组强杀。false = 调用方走自身强杀兜底（行为与旧版「非 Windows 返回 0 句柄」同义）。
        /// </summary>
        bool Available { get; }

        /// <summary>
        /// 建立进程组宿主——惰性创建，一个服务执行器一个组，互不覆盖。
        /// </summary>
        /// <param name="logTag">日志标签（创建失败出声用）</param>
        /// <returns>组句柄（不透明）；不可用返回 null</returns>
        object? CreateGroup(string logTag);

        /// <summary>
        /// 把目标进程加入组——加入后该进程随组宿主生命周期终止。
        /// </summary>
        /// <param name="group">组句柄（<see cref="CreateGroup"/> 的返回值；null = 无组，直接 false）</param>
        /// <param name="target">目标进程</param>
        /// <returns>true = 加入成功</returns>
        bool Attach(object? group, Process target);
    }
}
