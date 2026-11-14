using System;
using System.IO;
using CatHome4.Contracts;
using Mau.Runtime;

namespace CatHome4.Platform.Windows
{
    /// <summary>
    /// 文件占用探测·Windows 实现——独占打开探测（已迁）；Restart Manager 反查归 P4（随 SetUp 迁移一并搬）。
    /// <para>
    /// P3 现状：<see cref="CanOpenExclusive"/> 已收口；<see cref="QueryHolders"/> 先返回空串并出声——
    /// 现役调用方（<c>SetUp/Program.Relaunch.cs</c>）仍持自己的 rstrtmgr 实现，行为不变；
    /// 待 P4 SetUp 去 GUI 并接入本服务时补齐，缺口登记在 <c>design-ch4-platform_log.md</c>。
    /// </para>
    /// </summary>
    public sealed class WindowsFileLockProbe : IFileLockProbe
    {
        /// <summary>反查未迁移告警标志——只出声一次</summary>
        private static bool _holdersReported;

        /// <summary>
        /// 独占打开探测——能独占打开 = 无持有者。
        /// <para>
        /// 用只读打开：ReadWrite 会对只读文件 / 权限受限文件直接抛 UnauthorizedAccessException，被误判成「被占」。
        /// </para>
        /// </summary>
        /// <param name="path">文件路径</param>
        /// <returns>true = 可独占打开</returns>
        public bool CanOpenExclusive(string path)
        {
            try
            {
                using (FileStream fs = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.None))
                {
                    return true;
                }
            }
            catch (Exception)
            {
                return false;
            }
        }

        /// <summary>
        /// 反查持有文件的进程——P4 前返回空串（Restart Manager 未迁入）。
        /// </summary>
        /// <param name="path">文件路径</param>
        /// <returns>空串（未实现）</returns>
        public string QueryHolders(string path)
        {
            if (_holdersReported == false)
            {
                _holdersReported = true;
                LogStore.Add("CatHome4", 2, "文件持有者反查未迁入 Platform（Restart Manager 归 P4）——本次按无命中处理", "SYS");
            }
            return "";
        }
    }
}
