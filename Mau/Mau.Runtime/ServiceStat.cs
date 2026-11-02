using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Runtime.InteropServices;

namespace Mau.Runtime
{
    /// <summary>
    /// 常驻服务进程统计——工作集读数 / 进程树合计 / 系统父子关系快照。
    /// <para>
    /// py 与 node 两侧共用的机制面（单点实现，避免双份漂移）。
    /// OS 口径读数（<see cref="Process.WorkingSet64"/>）；进程不存在 / 已退出 / 不可读一律按 0 计，不抛。
    /// </para>
    /// </summary>
    internal static class ServiceStat
    {
        /// <summary>
        /// 读进程工作集字节数（OS 口径）——进程不存在 / 已退出不可读返回 0。
        /// </summary>
        /// <param name="pid">进程号</param>
        /// <returns>工作集字节数；不可读 0</returns>
        internal static long ReadWorkingSet(int pid)
        {
            if (pid <= 0)
            {
                return 0;
            }
            try
            {
                using (Process proc = Process.GetProcessById(pid))
                {
                    return proc.WorkingSet64;
                }
            }
            catch (Exception)
            {
                return 0;
            }
        }

        /// <summary>
        /// 读服务进程树的合计工作集——venv 转发解释器 / node 子进程等场景下真实负载落在后代进程，
        /// 只读直接子进程会得到远小于实际的数字（2026-10-16 实机判例：venv shim 4 MB / 真实解释器 765 MB）。
        /// </summary>
        /// <param name="rootPid">服务进程号（进程树根）</param>
        /// <returns>根进程及其全部后代的工作集之和（不可读项按 0 计）；根无效 0</returns>
        internal static long ReadProcessTreeWorkingSet(int rootPid)
        {
            if (rootPid <= 0)
            {
                return 0;
            }
            Dictionary<int, List<int>> children = SnapshotParentLinks();
            List<int> pending = new List<int>();
            List<int> tree = new List<int>();
            pending.Add(rootPid);
            while (pending.Count > 0)
            {
                int current = pending[0];
                pending.RemoveAt(0);
                tree.Add(current);
                List<int>? kids;
                if (children.TryGetValue(current, out kids) && kids != null)
                {
                    pending.AddRange(kids);
                }
            }
            long total = 0;
            for (int i = 0; i < tree.Count; i = i + 1)
            {
                total = total + ReadWorkingSet(tree[i]);
            }
            return total;
        }

        /// <summary>
        /// 快照系统进程父子关系——toolhelp32 全量遍历（父进程号 → 子进程号列表）。
        /// </summary>
        /// <returns>父子关系表；枚举失败返回已收集部分（空表）</returns>
        internal static Dictionary<int, List<int>> SnapshotParentLinks()
        {
            Dictionary<int, List<int>> links = new Dictionary<int, List<int>>();
            IntPtr snapshot = CreateToolhelp32Snapshot(Th32csSnapProcess, 0);
            if (snapshot == InvalidHandleValue)
            {
                return links;
            }
            try
            {
                ProcessEntry32W entry = new ProcessEntry32W();
                entry.dwSize = (uint)Marshal.SizeOf(typeof(ProcessEntry32W));
                bool ok = Process32FirstW(snapshot, ref entry);
                while (ok)
                {
                    int parent = unchecked((int)entry.th32ParentProcessID);
                    int child = unchecked((int)entry.th32ProcessID);
                    List<int>? kids;
                    if (!links.TryGetValue(parent, out kids) || kids == null)
                    {
                        kids = new List<int>();
                        links[parent] = kids;
                    }
                    kids.Add(child);
                    ok = Process32NextW(snapshot, ref entry);
                }
            }
            catch (Exception ex)
            {
                // 枚举异常——出声后返回已收集部分（资源面宁可少算不炸）
                LogStore.Add("ServiceStat", 2, "进程关系枚举异常（进程树内存将少算）: " + ex.Message, "SYS");
            }
            finally
            {
                CloseHandle(snapshot);
            }
            return links;
        }

        /// <summary>toolhelp32 进程快照标志——TH32CS_SNAPPROCESS</summary>
        private const uint Th32csSnapProcess = 0x00000002;

        /// <summary>无效句柄值（CreateToolhelp32Snapshot 失败返回）</summary>
        private static readonly IntPtr InvalidHandleValue = new IntPtr(-1);

        /// <summary>进程条目（toolhelp32 PROCESSENTRY32W——仅取父子进程号两个字段）</summary>
        [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
        private struct ProcessEntry32W
        {
            /// <summary>结构大小（调用前须赋值）</summary>
            public uint dwSize;

            /// <summary>引用计数（未用）</summary>
            public uint cntUsage;

            /// <summary>进程号</summary>
            public uint th32ProcessID;

            /// <summary>默认堆标识（未用）</summary>
            public IntPtr th32DefaultHeapID;

            /// <summary>模块标识（未用）</summary>
            public uint th32ModuleID;

            /// <summary>线程数（未用）</summary>
            public uint cntThreads;

            /// <summary>父进程号</summary>
            public uint th32ParentProcessID;

            /// <summary>优先级基值（未用）</summary>
            public int pcPriClassBase;

            /// <summary>标志（未用）</summary>
            public uint dwFlags;

            /// <summary>可执行文件名（未用）</summary>
            [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 260)]
            public string szExeFile;
        }

        /// <summary>创建进程快照（Win32 kernel32.CreateToolhelp32Snapshot）</summary>
        [DllImport("kernel32.dll", SetLastError = true)]
        private static extern IntPtr CreateToolhelp32Snapshot(uint dwFlags, uint th32ProcessID);

        /// <summary>取首个进程条目（Win32 kernel32.Process32FirstW）</summary>
        [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        private static extern bool Process32FirstW(IntPtr hSnapshot, ref ProcessEntry32W lppe);

        /// <summary>取下一个进程条目（Win32 kernel32.Process32NextW）</summary>
        [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        private static extern bool Process32NextW(IntPtr hSnapshot, ref ProcessEntry32W lppe);

        /// <summary>关闭句柄（Win32 kernel32.CloseHandle）</summary>
        [DllImport("kernel32.dll", SetLastError = true)]
        private static extern bool CloseHandle(IntPtr hObject);
    }
}
