namespace CatHome4.Contracts
{
    /// <summary>
    /// 外壳启动器——打开目录 / 定位文件 / 打开外部链接（交环境默认程序）。
    /// <para>
    /// Windows 实现走资源管理器；Linux 实现走 <c>xdg-open</c>。失败只出声不抛（非关键路径）。
    /// </para>
    /// </summary>
    public interface IShellLauncher
    {
        /// <summary>
        /// 打开目录，或定位文件（在文件管理器中选中）。
        /// </summary>
        /// <param name="path">目录或文件路径（空串直接忽略）</param>
        void OpenFolder(string path);

        /// <summary>
        /// 用默认程序打开外部链接。
        /// </summary>
        /// <param name="url">目标地址（空串直接忽略）</param>
        void OpenUrl(string url);
    }
}
