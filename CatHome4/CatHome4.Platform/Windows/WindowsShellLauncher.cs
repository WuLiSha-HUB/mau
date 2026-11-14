using System;
using System.Diagnostics;
using System.IO;
using CatHome4.Contracts;
using Mau.Runtime;

namespace CatHome4.Platform.Windows
{
    /// <summary>
    /// 外壳启动器·Windows 实现——目录用资源管理器打开，文件用 <c>/select</c> 定位。
    /// <para>
    /// 由 <c>WinHostShell.OpenFolder</c> / <c>OpenExternalUrl</c> 与
    /// <c>AdminService.HandleOpenDataDir</c> 收口而来（P3 迁移）。
    /// </para>
    /// </summary>
    public sealed class WindowsShellLauncher : IShellLauncher
    {
        /// <summary>
        /// 打开目录，或定位文件（在资源管理器中选中）——失败记 L2，不影响主流程。
        /// </summary>
        /// <param name="path">目录或文件路径（空串直接忽略）</param>
        public void OpenFolder(string path)
        {
            if (path == null || path.Length == 0)
            {
                return;
            }
            try
            {
                if (Directory.Exists(path))
                {
                    Process.Start("explorer.exe", path);
                    return;
                }
                if (File.Exists(path))
                {
                    Process.Start("explorer.exe", "/select,\"" + path + "\"");
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "打开路径失败：" + path + " | " + ex.Message, "SHELL");
            }
        }

        /// <summary>
        /// 用默认程序打开外部链接——进程外动作，不触碰业务对象。
        /// </summary>
        /// <param name="url">目标地址（空串直接忽略）</param>
        public void OpenUrl(string url)
        {
            if (url == null || url.Length == 0)
            {
                return;
            }
            try
            {
                ProcessStartInfo psi = new ProcessStartInfo(url);
                psi.UseShellExecute = true;
                Process.Start(psi);
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "打开链接失败：" + url + "——" + ex.Message, "SHELL");
            }
        }
    }
}
