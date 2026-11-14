using System.Collections.Generic;

namespace Mau.Contracts
{
    /// <summary>
    /// 进程统计契约——常驻服务（py / node）的内存与父子关系读面。
    /// <para>
    /// 平台差异按环境分实现（Windows = toolhelp32 / Linux = procfs，见 <c>Mau.Runtime/Platform/</c>），
    /// 调用方只经 <c>Mau.Runtime.ProcessStats.Current</c> 取用，不做逐调用点环境判断（design-ch4-platform §二 约束）。
    /// 零依赖：参数与返回值只用 BCL 类型。
    /// </para>
    /// </summary>
    public interface IProcessStat
    {
        /// <summary>
        /// 读进程工作集字节数（OS 口径）——进程不存在 / 已退出 / 不可读返回 0，不抛。
        /// </summary>
        /// <param name="pid">进程号</param>
        /// <returns>工作集字节数；不可读 0</returns>
        long ReadWorkingSet(int pid);

        /// <summary>
        /// 读进程树的合计工作集——真实负载常落在后代进程（venv 转发解释器 / node 子进程）。
        /// </summary>
        /// <param name="rootPid">进程树根进程号</param>
        /// <returns>根进程及全部后代工作集之和（不可读项按 0 计）；根无效 0</returns>
        long ReadProcessTreeWorkingSet(int rootPid);

        /// <summary>
        /// 快照系统进程父子关系（父进程号 → 子进程号列表）。
        /// </summary>
        /// <returns>父子关系表；枚举失败返回已收集部分（可能为空表）</returns>
        Dictionary<int, List<int>> SnapshotParentLinks();
    }
}
