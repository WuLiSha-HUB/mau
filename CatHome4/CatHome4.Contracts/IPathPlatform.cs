using System;

namespace CatHome4.Contracts
{
    /// <summary>
    /// 路径平台面——各平台不同的路径默认值与比较口径（design-ch4-platform §六）。
    /// <para>
    /// 只承载「平台差异」：数据根机器级默认、html 根回退、路径比较口径、可执行名。
    /// 仓库根探测（<c>FindRepoRoot</c>）不在此列——它同时服务数据根与 html 根解析，本体禁动。
    /// </para>
    /// </summary>
    public interface IPathPlatform
    {
        /// <summary>
        /// 数据根机器级默认（三级锚定的最后一级）——Windows = <c>%LOCALAPPDATA%/CatHome4</c>；Linux = <c>~/.local/share/CatHome4</c>。
        /// </summary>
        string DefaultDataRoot { get; }

        /// <summary>
        /// 路径比较口径——Windows 大小写不敏感（OrdinalIgnoreCase）；Linux 大小写敏感（Ordinal）。
        /// </summary>
        StringComparison PathComparison { get; }

        /// <summary>
        /// 宿主可执行文件名——Windows = <c>CatHome4.exe</c>；Linux = <c>CatHome4</c>。
        /// </summary>
        string HostExeName { get; }

        /// <summary>
        /// 基座 CLI 可执行文件名——Windows = <c>Mau.exe</c>；Linux = <c>Mau</c>。
        /// </summary>
        string MauExeName { get; }

        /// <summary>
        /// 一键部署链可执行文件名（仓库根下）——Windows = <c>SetUp.exe</c>；Linux = <c>SetUp-linux</c>。
        /// <para>
        /// 仓库根的判据面按本名派生（A366）——Linux 上仓库根只有 PE 形态的 <c>SetUp.exe</c> 时不可执行，
        /// 故存在性判定与启动一律走本名，缺失即明示「本平台 SetUp 未入库」，不回落跨平台 PE。
        /// </para>
        /// <para>
        /// Linux 不叫 <c>SetUp</c>：仓库根同时存在 <c>SetUp/</c> 源码目录，同名文件与目录在 NTFS / ext4 均不可共存
        /// （Windows 侧 clone 会直接 checkout 失败）。
        /// </para>
        /// </summary>
        string SetUpExeName { get; }

        /// <summary>
        /// 源码区 html 根——仓库根下的前端源码目录（存在性由调用方判定）。
        /// </summary>
        /// <param name="repoRoot">仓库根（空串 = 未命中，调用方走回退）</param>
        /// <returns>源码区 html 目录路径</returns>
        string SourceHtmlRoot(string repoRoot);

        /// <summary>
        /// 部署区 html 根——发布包内的前端目录（源码区不可用时的回退面）。
        /// </summary>
        /// <param name="appBaseDir">进程基目录</param>
        /// <returns>部署区 html 目录路径</returns>
        string DeployHtmlRoot(string appBaseDir);
    }
}
