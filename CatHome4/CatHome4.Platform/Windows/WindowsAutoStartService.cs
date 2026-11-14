using System;
using CatHome4.Contracts;
using Mau.Runtime;

namespace CatHome4.Platform.Windows
{
    /// <summary>
    /// 开机自启·Windows 实现——写 / 删 HKCU <c>Run</c> 值（无管理员权限，信任面增量）。
    /// <para>
    /// 由 <c>WinHostShell.SyncAutoStart</c> 收口而来（P3 迁移）；配置为真相，注册表为物化。
    /// </para>
    /// </summary>
    public sealed class WindowsAutoStartService : IAutoStartService
    {
        /// <summary>注册表 Run 键路径——HKCU</summary>
        private const string AutoStartRunPath = @"Software\Microsoft\Windows\CurrentVersion\Run";

        /// <summary>注册表值名——按 exe 名（部署实例自引用自适应）</summary>
        private const string AutoStartRunName = "CatHome4";

        /// <summary>
        /// 当前环境是否有自启物化能力——Windows 恒 true。
        /// </summary>
        public bool Available
        {
            get
            {
                return true;
            }
        }

        /// <summary>
        /// 注册表同步——带引号 exe 路径（路径含空格惯例）；幂等，值一致时重复调用无副作用。
        /// </summary>
        /// <param name="on">目标开关态</param>
        /// <returns>空串 = 成功；否则为失败原因</returns>
        public string Sync(bool on)
        {
            if (!OperatingSystem.IsWindows())
            {
                LogStore.Add("CatHome4", 2, "自启同步失败: 非 Windows 环境", "AUTOSTART");
                return "非 Windows 环境";
            }
            try
            {
                using (Microsoft.Win32.RegistryKey? runKey = Microsoft.Win32.Registry.CurrentUser.OpenSubKey(AutoStartRunPath, true))
                {
                    if (runKey == null)
                    {
                        LogStore.Add("CatHome4", 2, "自启同步失败: Run 键不存在", "AUTOSTART");
                        return "Run 键不存在";
                    }
                    if (on)
                    {
                        string? exePath = Environment.ProcessPath;
                        if (string.IsNullOrEmpty(exePath))
                        {
                            LogStore.Add("CatHome4", 2, "自启同步失败: 无法解析当前进程路径", "AUTOSTART");
                            return "无法解析当前进程路径";
                        }
                        runKey.SetValue(AutoStartRunName, "\"" + exePath + "\"");
                    }
                    else
                    {
                        runKey.DeleteValue(AutoStartRunName, false);
                    }
                }
                LogStore.Add("CatHome4", 1, "自启同步: " + (on ? "true" : "false"), "AUTOSTART");
                return "";
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "自启同步失败: " + ex.Message, "AUTOSTART");
                return ex.Message;
            }
        }
    }
}
