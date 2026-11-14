namespace CatHome4.Contracts
{
    /// <summary>
    /// 开机自启服务——把「开机自启」配置物化为平台机制。
    /// <para>
    /// 配置为真相、物化为派生（design-ch4-host-shell）：Windows 物化到 HKCU Run 值，
    /// Linux 物化到 systemd user unit（P5）。调用方只报开关态，不关心落点。
    /// </para>
    /// </summary>
    public interface IAutoStartService
    {
        /// <summary>
        /// 当前环境是否有自启物化能力。false = 调用方降级为「仅记配置」（并出声）。
        /// </summary>
        bool Available { get; }

        /// <summary>
        /// 同步物化形态——幂等（值一致时重复调用无副作用）。
        /// </summary>
        /// <param name="on">目标开关态</param>
        /// <returns>空串 = 成功；否则为失败原因</returns>
        string Sync(bool on);
    }
}
