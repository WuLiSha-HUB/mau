using System;
using System.IO;

namespace Mau.Runtime
{
    /// <summary>
    /// POSIX 权限位保真——两个面：原子替换（临时文件 + rename）之前把目标文件的普通权限位复制到临时文件；
    /// 目录整棵复制时把源目录的普通权限位补到新建的目标目录（A398）。
    /// <para>
    /// 背景：rename 挂的是新 inode，其 mode 由临时文件创建时的 <c>0666 &amp; ~umask</c> 决定，
    /// 原文件的可执行位等被静默丢弃（可执行脚本被覆写后退化为 0644）。<c>chmod</c> 不受 umask 再削，
    /// 故在替换之前显式设置即可保真（A397）。
    /// </para>
    /// <para>
    /// 复制面（A398）：<c>File.Copy</c> 自带权限位复制，<c>Directory.CreateDirectory</c> 不带——
    /// 私有目录（0700）整棵复制后副本退化为 0755，对 other 变成可读列。
    /// </para>
    /// <para>
    /// 边界：只复制普通权限位（三段 rwx），不复制 setuid / setgid / sticky（普通进程复制这些位等于自造提权文件），
    /// 也不复制所有权 / ACL / xattr（rename 恒挂新 inode，属主为当前进程 UID）。Windows 上为空操作。
    /// </para>
    /// </summary>
    public static class PosixFileMode
    {
        /// <summary>
        /// 普通权限位掩码——user / group / other 三段 rwx，不含特殊位。
        /// </summary>
        private const UnixFileMode OrdinaryMask =
            UnixFileMode.UserRead | UnixFileMode.UserWrite | UnixFileMode.UserExecute
            | UnixFileMode.GroupRead | UnixFileMode.GroupWrite | UnixFileMode.GroupExecute
            | UnixFileMode.OtherRead | UnixFileMode.OtherWrite | UnixFileMode.OtherExecute;

        /// <summary>
        /// 替换前保权限——目标存在时把其普通权限位设到待替换的临时文件上；失败记账且出声，不阻断写入。
        /// </summary>
        /// <param name="targetPath">将被替换的目标文件（不存在则为新建语义，不做任何设置）</param>
        /// <param name="tempPath">即将 rename 到目标的临时文件</param>
        public static void PreserveForReplace(string targetPath, string tempPath)
        {
            if (OperatingSystem.IsWindows())
            {
                return;
            }
            try
            {
                if (!File.Exists(targetPath))
                {
                    return;
                }
                UnixFileMode mode = File.GetUnixFileMode(targetPath) & OrdinaryMask;
                File.SetUnixFileMode(tempPath, mode);
            }
            catch (Exception exception) when (exception is IOException
                || exception is UnauthorizedAccessException
                || exception is ArgumentException
                || exception is NotSupportedException)
            {
                // 失败必须可见——权限位保真失败时按默认权限落盘，但不静默
                LogStore.Add("Mau", 2, "权限位保真失败（按默认权限落盘）: " + tempPath + " | " + exception.Message, "FILE");
            }
        }
        /// <summary>
        /// 复制后保权限——把源路径（文件或目录）的普通权限位设到复制出的目标上；失败记账且出声，不阻断复制。
        /// <para>
        /// 背景：<c>File.Copy</c> 在 Unix 上自带权限位复制，但 <c>Directory.CreateDirectory</c> 的 mode 由
        /// <c>0777 &amp; ~umask</c> 决定——整棵复制私有目录（0700）时副本退化成 0755，对 other 变成可读列（A398）。
        /// </para>
        /// </summary>
        /// <param name="sourcePath">源文件或源目录（已存在）</param>
        /// <param name="destinationPath">复制出的目标文件或目标目录</param>
        public static void PreserveForCopy(string sourcePath, string destinationPath)
        {
            if (OperatingSystem.IsWindows())
            {
                return;
            }
            try
            {
                UnixFileMode mode = File.GetUnixFileMode(sourcePath) & OrdinaryMask;
                File.SetUnixFileMode(destinationPath, mode);
            }
            catch (Exception exception) when (exception is IOException
                || exception is UnauthorizedAccessException
                || exception is ArgumentException
                || exception is NotSupportedException)
            {
                // 失败必须可见——权限位保真失败时按默认权限落盘，但不静默
                LogStore.Add("Mau", 2, "复制权限位保真失败（按默认权限落盘）: " + destinationPath + " | " + exception.Message, "FILE");
            }
        }
    }
}
