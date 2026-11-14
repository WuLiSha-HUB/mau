using System;
using System.Runtime.InteropServices;
using CatHome4.Contracts;

namespace CH4
{
    /// <summary>
    /// headless 外壳桥——IHostShell 的无界面实现（Linux 生产常驻 / Windows 服务形态）。
    /// 边界（design-ch4-host-bootstrap-resilience §五 出口补位）：桌面提示窗抽走后，本出口以
    /// stderr + 启动报告路径承担失败与降级可见面——失败必须可见，不静默降级。
    /// 退出语义：SIGTERM / SIGINT / Ctrl+C 置退出请求，主循环 break 走内核统一收尾（systemd stop 走 SIGTERM）。
    /// </summary>
    public sealed class HeadlessHostShell : IHostShell
    {
        /// <summary>退出请求标志——信号处理器置位，主循环轮询</summary>
        private volatile bool _exitRequested;

        /// <summary>SIGTERM 注册句柄——释放即恢复默认行为</summary>
        private PosixSignalRegistration _sigTerm;

        /// <summary>SIGINT 注册句柄——释放即恢复默认行为</summary>
        private PosixSignalRegistration _sigInt;

        /// <summary>
        /// 早段初始化——注册退出信号（常驻进程须能被 systemd / 控制台优雅停止）。
        /// </summary>
        public void InitEarly()
        {
            try
            {
                _sigTerm = PosixSignalRegistration.Create(PosixSignal.SIGTERM, OnSignal);
                _sigInt = PosixSignalRegistration.Create(PosixSignal.SIGINT, OnSignal);
            }
            catch (Exception ex)
            {
                Console.Error.WriteLine("[CH4] 退出信号注册失败（只能强杀）：" + ex.Message);
            }
        }

        /// <summary>
        /// 外壳就绪——无 UI：只在 stderr 声明就绪与可访问端点（运维判活的第一行）。
        /// </summary>
        public void OnHostReady()
        {
            string panel = "";
            if (HostAccess.View != null && HostAccess.View.PanelPort > 0)
            {
                panel = "http://127.0.0.1:" + HostAccess.View.PanelPort.ToString();
            }
            Console.WriteLine("[CH4] headless 宿主就绪 版本 " + HostAccess.View.VersionInfo + " 面板 " + panel);
        }

        /// <summary>
        /// 主循环每帧泵——headless 无界面状态需要轮询（空实现）。
        /// </summary>
        public void PumpFrame()
        {
        }

        /// <summary>退出请求标志——信号触发后为 true。</summary>
        public bool ExitRequested
        {
            get { return _exitRequested; }
        }

        /// <summary>
        /// 启动健康面——stderr 逐条出声（无窗口可弹；交互参数在 headless 下同样走 stderr）。
        /// </summary>
        /// <param name="interactive">是否交互模式（headless 恒按非交互处理）</param>
        public void ShowStartupHealth(bool interactive)
        {
            StartupDegradeEntry[] entries = StartupDegradation.Snapshot();
            if (entries.Length == 0)
            {
                return;
            }
            Console.Error.WriteLine("[CH4] 启动降级 " + entries.Length.ToString() + " 项：" + StartupDegradation.BuildSummaryLine());
            for (int i = 0; i < entries.Length; i = i + 1)
            {
                Console.Error.WriteLine("[CH4]   " + entries[i].Code + " | " + entries[i].Ability + " | " + entries[i].Detail);
            }
            string reportPath = "";
            if (HostAccess.View != null)
            {
                reportPath = HostAccess.View.StartupReportPath;
            }
            if (reportPath.Length > 0)
            {
                Console.Error.WriteLine("[CH4]   报告：" + reportPath);
            }
        }

        /// <summary>
        /// 启动失败提示——stderr 输出摘要（无对话框可弹）。
        /// </summary>
        /// <param name="message">失败摘要</param>
        public void ShowStartupFailure(string message)
        {
            Console.Error.WriteLine("[CH4] 启动失败：" + message);
        }

        /// <summary>
        /// 系统通知——stderr 一行（headless 无气泡）。
        /// </summary>
        /// <param name="title">标题</param>
        /// <param name="text">正文</param>
        public void NotifyBalloon(string title, string text)
        {
            Console.WriteLine("[CH4] 通知 | " + title + " | " + text);
        }

        /// <summary>
        /// 打开目录——headless 无文件管理器：出声提示路径，不静默丢弃。
        /// </summary>
        /// <param name="path">目录或文件路径</param>
        public void OpenFolder(string path)
        {
            Console.WriteLine("[CH4] 打开目录不可用（headless）：" + path);
        }

        /// <summary>
        /// 信号处理——置退出请求并取消默认终止（让主循环走统一收尾）。
        /// </summary>
        /// <param name="context">信号上下文</param>
        private void OnSignal(PosixSignalContext context)
        {
            context.Cancel = true;
            _exitRequested = true;
            Console.WriteLine("[CH4] 收到 " + context.Signal.ToString() + "——本轮收尾后退出");
        }
    }
}
