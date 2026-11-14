using System;
using System.IO;
using CatHome4.Contracts;

namespace CatHome4.Platform.Windows
{
    /// <summary>
    /// 路径平台面·Windows 实现——<c>%LOCALAPPDATA%</c> 数据根 · OrdinalIgnoreCase 比较 · <c>.exe</c> 后缀。
    /// <para>
    /// 由 <c>Program.Resolve.cs</c> 的平台默认值三处抽自（P3 迁移）；仓库根探测本体不动。
    /// </para>
    /// </summary>
    public sealed class WindowsPathPlatform : IPathPlatform
    {
        /// <summary>
        /// 数据根机器级默认——<c>%LOCALAPPDATA%/CatHome4</c>（部署实例持久数据，secrets 不进漫游域）。
        /// </summary>
        public string DefaultDataRoot
        {
            get
            {
                return Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "CatHome4");
            }
        }

        /// <summary>
        /// 路径比较口径——Windows 大小写不敏感。
        /// </summary>
        public StringComparison PathComparison
        {
            get
            {
                return StringComparison.OrdinalIgnoreCase;
            }
        }

        /// <summary>
        /// 宿主可执行文件名。
        /// </summary>
        public string HostExeName
        {
            get
            {
                return "CatHome4.exe";
            }
        }

        /// <summary>
        /// 基座 CLI 可执行文件名。
        /// </summary>
        public string MauExeName
        {
            get
            {
                return "Mau.exe";
            }
        }

        /// <summary>
        /// 源码区 html 根——仓库根下的前端源码目录（唯一事实源，改即生效）。
        /// </summary>
        /// <param name="repoRoot">仓库根（空串 = 未命中）</param>
        /// <returns>源码区 html 目录路径；repoRoot 空串返回空串</returns>
        public string SourceHtmlRoot(string repoRoot)
        {
            if (repoRoot == null || repoRoot.Length == 0)
            {
                return "";
            }
            return Path.Combine(repoRoot, "CatHome4", "html");
        }

        /// <summary>
        /// 部署区 html 根——发布包内的前端目录。
        /// </summary>
        /// <param name="appBaseDir">进程基目录</param>
        /// <returns>部署区 html 目录路径</returns>
        public string DeployHtmlRoot(string appBaseDir)
        {
            return Path.Combine(appBaseDir, "html");
        }
    }
}
