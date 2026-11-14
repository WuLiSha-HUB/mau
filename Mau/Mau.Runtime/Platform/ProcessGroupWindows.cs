using System;
using System.Diagnostics;
using System.Runtime.InteropServices;
using Mau.Contracts;

namespace Mau.Runtime
{
    /// <summary>
    /// 进程组宿主·Windows 实现——Job 对象（KILL_ON_JOB_CLOSE：宿主进程终止即连带终止组内全部服务进程）。
    /// <para>
    /// 每个服务执行器（py / node）各持一个 Job，互不覆盖；创建失败返回 null，
    /// 此时强杀兜底关闭（启动前自愈仍生效，其余功能不受影响）。
    /// </para>
    /// </summary>
    public sealed class ProcessGroupWindows : IProcessGroupHost
    {
        /// <summary>
        /// 当前环境是否支持进程组强杀——Windows 恒 true（创建失败按 null 句柄出声）。
        /// </summary>
        public bool Available
        {
            get
            {
                return true;
            }
        }

        /// <summary>
        /// 建立 Job 对象——惰性创建（KILL_ON_JOB_CLOSE）。
        /// </summary>
        /// <param name="logTag">日志标签</param>
        /// <returns>Job 句柄（装箱）；创建失败 / 非 Windows 返回 null</returns>
        public object? CreateGroup(string logTag)
        {
            if (OperatingSystem.IsWindows() == false)
            {
                LogStore.Add(logTag, 2, "Windows 进程组实现被非 Windows 环境调用——不建组（强杀兜底关闭）", "SYS");
                return null;
            }
            IntPtr job = CreateJobObject(IntPtr.Zero, null);
            if (job == IntPtr.Zero)
            {
                LogStore.Add(logTag, 2, "Job 对象创建失败——强杀兜底关闭（启动前自愈仍生效）", "SYS");
                return null;
            }
            JobBasicLimitInformation basic = new JobBasicLimitInformation();
            basic.LimitFlags = JobObjectLimitKillOnJobClose;
            JobExtendedLimitInformation extended = new JobExtendedLimitInformation();
            extended.BasicLimitInformation = basic;
            int size = Marshal.SizeOf(extended);
            IntPtr buffer = Marshal.AllocHGlobal(size);
            try
            {
                Marshal.StructureToPtr(extended, buffer, false);
                bool applied = SetInformationJobObject(job, JobObjectExtendedLimitInformation, buffer, (uint)size);
                if (!applied)
                {
                    LogStore.Add(logTag, 2, "Job 限制设置失败——强杀兜底关闭（启动前自愈仍生效）", "SYS");
                    return null;
                }
            }
            finally
            {
                Marshal.FreeHGlobal(buffer);
            }
            return job;
        }

        /// <summary>
        /// 把进程加入 Job（Win32 kernel32.AssignProcessToJobObject）。
        /// </summary>
        /// <param name="group">Job 句柄（<see cref="CreateGroup"/> 返回值）</param>
        /// <param name="target">目标进程</param>
        /// <returns>true = 加入成功</returns>
        public bool Attach(object? group, Process target)
        {
            if (group == null || target == null)
            {
                return false;
            }
            if (OperatingSystem.IsWindows() == false)
            {
                return false;
            }
            IntPtr job = (IntPtr)group;
            if (job == IntPtr.Zero)
            {
                return false;
            }
            return AssignProcessToJobObject(job, target.Handle);
        }

        /// <summary>Job 扩展限制信息类——JOBOBJECTINFOCLASS.JobObjectExtendedLimitInformation</summary>
        private const int JobObjectExtendedLimitInformation = 9;

        /// <summary>KILL_ON_JOB_CLOSE 标志——Job 最后一个句柄关闭时终止其内全部进程</summary>
        private const uint JobObjectLimitKillOnJobClose = 0x2000;

        /// <summary>创建 Job 对象（Win32 kernel32.CreateJobObject）</summary>
        [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        private static extern IntPtr CreateJobObject(IntPtr lpJobAttributes, string? lpName);

        /// <summary>设置 Job 限制信息（Win32 kernel32.SetInformationJobObject）</summary>
        [DllImport("kernel32.dll", SetLastError = true)]
        private static extern bool SetInformationJobObject(IntPtr hJob, int infoClass, IntPtr info, uint infoLength);

        /// <summary>把进程加入 Job（Win32 kernel32.AssignProcessToJobObject）</summary>
        [DllImport("kernel32.dll", SetLastError = true)]
        private static extern bool AssignProcessToJobObject(IntPtr hJob, IntPtr hProcess);

        /// <summary>Job 基本限制信息（Win32 JOBOBJECT_BASIC_LIMIT_INFORMATION）</summary>
        [StructLayout(LayoutKind.Sequential)]
        private struct JobBasicLimitInformation
        {
            /// <summary>单进程用户态时间上限</summary>
            public long PerProcessUserTimeLimit;
            /// <summary>单 Job 用户态时间上限</summary>
            public long PerJobUserTimeLimit;
            /// <summary>限制标志（承载 KILL_ON_JOB_CLOSE）</summary>
            public uint LimitFlags;
            /// <summary>最小工作集</summary>
            public UIntPtr MinimumWorkingSetSize;
            /// <summary>最大工作集</summary>
            public UIntPtr MaximumWorkingSetSize;
            /// <summary>活动进程数上限</summary>
            public uint ActiveProcessLimit;
            /// <summary>CPU 亲和性掩码</summary>
            public UIntPtr Affinity;
            /// <summary>优先级类</summary>
            public uint PriorityClass;
            /// <summary>调度类</summary>
            public uint SchedulingClass;
        }

        /// <summary>Job IO 计数器（Win32 IO_COUNTERS）</summary>
        [StructLayout(LayoutKind.Sequential)]
        private struct JobIoCounters
        {
            /// <summary>读操作次数</summary>
            public ulong ReadOperationCount;
            /// <summary>写操作次数</summary>
            public ulong WriteOperationCount;
            /// <summary>其它操作次数</summary>
            public ulong OtherOperationCount;
            /// <summary>读取字节数</summary>
            public ulong ReadTransferCount;
            /// <summary>写入字节数</summary>
            public ulong WriteTransferCount;
            /// <summary>其它传输字节数</summary>
            public ulong OtherTransferCount;
        }

        /// <summary>Job 扩展限制信息（Win32 JOBOBJECT_EXTENDED_LIMIT_INFORMATION）</summary>
        [StructLayout(LayoutKind.Sequential)]
        private struct JobExtendedLimitInformation
        {
            /// <summary>基本限制信息</summary>
            public JobBasicLimitInformation BasicLimitInformation;
            /// <summary>IO 计数器</summary>
            public JobIoCounters IoInfo;
            /// <summary>单进程内存上限</summary>
            public UIntPtr ProcessMemoryLimit;
            /// <summary>Job 内存上限</summary>
            public UIntPtr JobMemoryLimit;
            /// <summary>进程峰值内存</summary>
            public UIntPtr PeakProcessMemoryUsed;
            /// <summary>Job 峰值内存</summary>
            public UIntPtr PeakJobMemoryUsed;
        }
    }
}
