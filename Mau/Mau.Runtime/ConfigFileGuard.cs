using System;
using System.IO;

namespace Mau.Runtime
{
    /// <summary>
    /// 配置文件守卫——损坏档的统一备份通道（workspace / schema / 各配置池共用，避免多处各写一份复制逻辑）。
    /// 失败返回空串，不抛：调用点本身处于降级路径，备份失败不得再引发中断。
    /// </summary>
    public static class ConfigFileGuard
    {
        /// <summary>
        /// 备份损坏配置——复制为 path.bad-yyyyMMdd_HHmmss。
        /// </summary>
        /// <param name="path">原文件路径</param>
        /// <returns>备份路径（空=备份失败）</returns>
        public static string BackupCorrupt(string path)
        {
            if (path == null || path.Length == 0)
            {
                return "";
            }
            try
            {
                string target = path + ".bad-" + DateTime.Now.ToString("yyyyMMdd_HHmmss");
                File.Copy(path, target, false);
                return target;
            }
            catch (Exception)
            {
                return "";
            }
        }
    }
}
