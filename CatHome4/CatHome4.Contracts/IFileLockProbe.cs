namespace CatHome4.Contracts
{
    /// <summary>
    /// 文件占用探测——部署切换前置判据（目标文件是否被其它进程持有）。
    /// <para>
    /// Windows 实现 = 独占打开探测 + Restart Manager 反查持有进程；
    /// Linux 实现（P5）= 无锁判定 + rename 重试。查询面不可用时返回空串（不抛、不阻断切换）。
    /// </para>
    /// </summary>
    public interface IFileLockProbe
    {
        /// <summary>
        /// 独占打开探测——能独占打开 = 无持有者。
        /// </summary>
        /// <param name="path">文件路径</param>
        /// <returns>true = 可独占打开</returns>
        bool CanOpenExclusive(string path);

        /// <summary>
        /// 反查持有文件的进程（应用名 + PID）。
        /// </summary>
        /// <param name="path">文件路径</param>
        /// <returns>持有者描述（多个以 / 分隔）；无命中或查询面不可用返回空串</returns>
        string QueryHolders(string path);
    }
}
