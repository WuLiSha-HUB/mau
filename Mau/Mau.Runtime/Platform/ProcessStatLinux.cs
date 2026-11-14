using System;
using System.Collections.Generic;
using System.IO;
using Mau.Contracts;

namespace Mau.Runtime
{
    /// <summary>
    /// 进程统计·Linux 实现（A358 落地）——procfs 读数。
    /// <para>
    /// 父子关系与内存读数来源：<c>/proc/&lt;pid&gt;/stat</c> 的 <c>ppid</c> 字段（第 4 项）与
    /// <c>/proc/&lt;pid&gt;/statm</c> 的驻留页数（第 2 项）乘系统页大小。读取失败按 0 / 已收集部分返回，
    /// 且<b>出声一次</b>——读数缺失必须可见，不静默当成真实值（与服务内存显示面口径一致）。
    /// </para>
    /// </summary>
    public sealed class ProcessStatLinux : IProcessStat
    {
        /// <summary>出声标志——进程统计每帧被调用，读取异常告警只出声一次</summary>
        private static bool _reported;

        /// <summary>
        /// 读进程工作集字节数——<c>/proc/&lt;pid&gt;/statm</c> 驻留页数 × 页大小。
        /// </summary>
        /// <param name="pid">进程号</param>
        /// <returns>工作集字节数；不可读 0</returns>
        public long ReadWorkingSet(int pid)
        {
            if (pid <= 0)
            {
                return 0;
            }
            string path = "/proc/" + pid.ToString() + "/statm";
            string text;
            try
            {
                text = File.ReadAllText(path);
            }
            catch (Exception ex)
            {
                ReportOnce("procfs statm 读取失败（服务内存按 0 计）: " + ex.Message);
                return 0;
            }
            string[] parts = text.Split(' ');
            if (parts.Length < 2)
            {
                ReportOnce("procfs statm 字段不足（服务内存按 0 计）: " + path);
                return 0;
            }
            long residentPages;
            if (long.TryParse(parts[1], out residentPages) == false)
            {
                ReportOnce("procfs statm 驻留页数非法（服务内存按 0 计）: " + path);
                return 0;
            }
            return residentPages * Environment.SystemPageSize;
        }

        /// <summary>
        /// 读进程树合计工作集——按父子关系表展开后代后逐 pid 累加。
        /// </summary>
        /// <param name="rootPid">进程树根进程号</param>
        /// <returns>根进程及全部后代工作集之和（不可读项按 0 计）；根无效 0</returns>
        public long ReadProcessTreeWorkingSet(int rootPid)
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
                if (children.TryGetValue(current, out kids))
                {
                    if (kids != null)
                    {
                        pending.AddRange(kids);
                    }
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
        /// 快照系统进程父子关系——遍历 <c>/proc</c> 数字目录读 ppid。
        /// </summary>
        /// <returns>父子关系表；枚举失败返回已收集部分（可能为空表）</returns>
        public Dictionary<int, List<int>> SnapshotParentLinks()
        {
            Dictionary<int, List<int>> links = new Dictionary<int, List<int>>();
            string[] procDirs;
            try
            {
                procDirs = Directory.GetDirectories("/proc");
            }
            catch (Exception ex)
            {
                ReportOnce("procfs 遍历失败（进程关系表为空）: " + ex.Message);
                return links;
            }
            for (int i = 0; i < procDirs.Length; i = i + 1)
            {
                string name = Path.GetFileName(procDirs[i]);
                int pid;
                if (int.TryParse(name, out pid) == false)
                {
                    continue;
                }
                int parent = ReadParentPid(procDirs[i]);
                if (parent < 0)
                {
                    continue;
                }
                List<int>? kids;
                if (links.TryGetValue(parent, out kids) == false)
                {
                    kids = new List<int>();
                    links[parent] = kids;
                }
                kids.Add(pid);
            }
            return links;
        }

        /// <summary>
        /// 读单进程的父进程号——<c>/proc/&lt;pid&gt;/stat</c> 解析。
        /// <para>
        /// 第二个字段 comm 可含空格与括号，故先定位<b>最后一个右括号</b>，其后按空格切分：
        /// 第 1 项 state、第 2 项 ppid。
        /// </para>
        /// </summary>
        /// <param name="procDir">进程目录（<c>/proc/&lt;pid&gt;</c>）</param>
        /// <returns>父进程号；解析失败 -1</returns>
        private static int ReadParentPid(string procDir)
        {
            string path = Path.Combine(procDir, "stat");
            string text;
            try
            {
                text = File.ReadAllText(path);
            }
            catch (Exception)
            {
                return -1;
            }
            int close = text.LastIndexOf(')');
            if (close < 0)
            {
                return -1;
            }
            string rest = text.Substring(close + 1).Trim();
            string[] parts = rest.Split(' ');
            if (parts.Length < 2)
            {
                return -1;
            }
            int parent;
            if (int.TryParse(parts[1], out parent) == false)
            {
                return -1;
            }
            return parent;
        }

        /// <summary>
        /// 读取异常出声——只出声一次（避免每帧刷屏），语义明确指向 procfs 读数缺失。
        /// </summary>
        /// <param name="message">原因</param>
        private static void ReportOnce(string message)
        {
            if (_reported)
            {
                return;
            }
            _reported = true;
            LogStore.Add("ProcessStat", 2, message, "SYS");
        }
    }
}
