using System;

namespace CatHome4.Contracts
{
    /// <summary>
    /// 宿主外壳桥——内核（CatHome4.Host）与出口（桌面 / headless）之间的唯一接口。
    /// 内核只认本接口：桌面出口提供托盘 / 总控窗 / 自启注册表 / 弹窗实现，headless 出口提供控制台与信号实现。
    /// 边界（design-ch4-platform §一 R3）：平台与 UI 差异只落在出口内，内核不出现 WinForms / Registry 引用。
    /// </summary>
    public interface IHostShell
    {
        /// <summary>
        /// 早段初始化——高 DPI / 应用用户模型 ID / 启动时刻基准。由内核在入口最早期调用（早于任何窗口创建）。
        /// </summary>
        void InitEarly();

        /// <summary>
        /// 启动完成通知——交互模式下由内核在预热帧后调用（桌面：起托盘 + 显示总控窗；headless：空实现）。
        /// </summary>
        void OnHostReady();

        /// <summary>
        /// 主循环每帧泵——桌面：自启配置变化轮询节流；headless：空实现。
        /// </summary>
        void PumpFrame();

        /// <summary>
        /// 退出请求标志——桌面：托盘「退出」/ 总控窗「停止运行」置位；headless：SIGTERM / Ctrl+C 置位。
        /// 主循环据此 break 走统一收尾。
        /// </summary>
        bool ExitRequested { get; }

        /// <summary>
        /// 启动健康面出口——存在降级条目时出声（桌面：独立提示窗；headless：stderr + 启动报告路径）。
        /// </summary>
        /// <param name="interactive">是否交互模式（false = 非交互，提示面须静默降级为 stderr）</param>
        void ShowStartupHealth(bool interactive);

        /// <summary>
        /// 启动失败提示——内核启动期致命异常的唯一可见面（桌面：消息框；headless：stderr）。
        /// </summary>
        /// <param name="message">失败摘要</param>
        void ShowStartupFailure(string message);

        /// <summary>
        /// 系统通知——轮末结算等一次性提醒（桌面：托盘气泡；headless：stderr）。
        /// </summary>
        /// <param name="title">标题</param>
        /// <param name="text">正文</param>
        void NotifyBalloon(string title, string text);

        /// <summary>
        /// 打开外部目录或定位文件。
        /// </summary>
        /// <param name="path">目录或文件路径</param>
        void OpenFolder(string path);
    }

    /// <summary>
    /// 宿主状态读面——外壳需要但由内核持有的量（端口 / 版本 / 降级清单 / 自启开关）。
    /// 内核实现后经 <see cref="IHostShell"/> 的实现方设置（桌面壳构造后调用 HostBridge.Attach）。
    /// </summary>
    public interface IHostView
    {
        /// <summary>版本信息串——运行版本（总控窗标题面）。</summary>
        string VersionInfo { get; }

        /// <summary>构建时刻——exe 元数据装配时间（总控窗「构建于」行）。</summary>
        string BuildTime { get; }

        /// <summary>端口段区标识（dev / deploy）——托盘提示区分两实例。</summary>
        string AreaLabel { get; }

        /// <summary>管理面板端口（默认猫 HTTP 宿主端口）。</summary>
        int PanelPort { get; }

        /// <summary>Majordomo 独立端口（未拉起时为 0）。</summary>
        int MajorPort { get; }

        /// <summary>最近一次启动报告落盘路径（空 = 未落盘）。</summary>
        string StartupReportPath { get; }

        /// <summary>启动降级条目快照——提示面与面板读取同一份数据源。</summary>
        /// <returns>条目数组（按登记顺序）</returns>
        StartupDegradeEntry[] StartupDegrades();

        /// <summary>数据根绝对路径——「打开数据目录」菜单动作使用。</summary>
        string DataRoot { get; }

        /// <summary>自启开关现值——读配置（配置为真相，注册表为物化）。</summary>
        /// <returns>true = 开机自启开启</returns>
        bool IsAutoStartOn();

        /// <summary>写自启开关——配置写入 + 由外壳同步其物化形态（注册表 / systemd unit）。</summary>
        /// <param name="on">目标状态</param>
        /// <returns>空串 = 成功；否则为失败原因</returns>
        string SetAutoStart(bool on);
    }
}
