using System;
using System.Diagnostics;
using CatHome4.Contracts;
using Mau.Runtime;

namespace CatHome4.Platform.Windows
{
    /// <summary>
    /// 进程树终止·Windows 实现——<c>taskkill /T /F</c> 先杀子树，再对自身句柄补一刀。
    /// <para>
    /// 由 <c>PsService.KillTree</c> 与 <c>CdpBrowserService.KillTree</c> 两份同形实现收口而来（P3 迁移）。
    /// </para>
    /// </summary>
    public sealed class WindowsProcessService : IProcessService
    {
        /// <summary>
        /// 终止进程树——子进程一并终止；失败出声不抛（目标进程可能已退出）。
        /// </summary>
        /// <param name="process">目标进程</param>
        /// <param name="logTag">日志标签（调用方分类）</param>
        public void KillTree(Process process, string logTag)
        {
            if (process == null)
            {
                return;
            }
            if (OperatingSystem.IsWindows())
            {
                try
                {
                    ProcessStartInfo psi = new ProcessStartInfo();
                    psi.FileName = "taskkill.exe";
                    psi.Arguments = "/T /F /PID " + process.Id.ToString();
                    psi.UseShellExecute = false;
                    psi.CreateNoWindow = true;
                    Process? killer = Process.Start(psi);
                    if (killer != null)
                    {
                        killer.WaitForExit(5000);
                    }
                }
                catch (Exception ex)
                {
                    // 杀树失败出声——进程可能已退出
                    LogStore.Add(logTag, 2, "进程树强杀失败: " + ex.Message, "SYS");
                }
            }
            try
            {
                process.Kill();
            }
            catch (Exception ex)
            {
                // 已退出
                LogStore.Add(logTag, 1, "进程已退出，Kill 跳过: " + ex.Message, "SYS");
            }
        }
    }
}
