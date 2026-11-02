using System;
using System.Drawing;
using System.Threading;
using System.Windows.Forms;
using Microsoft.Win32;
using CatHome4.Admin;
using CatHome4.Contracts;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// Program 托盘与自启分部——P1：HKCU Run 自启（配置为真相，注册表为物化）+ 系统托盘独立 UI 线程。
    /// 边界：仅交互模式启用；托盘线程不触碰业务状态（ThreadGuard 域外——只经 ConfigStore 锁 / LogStore 并发写者 / 静态只读）。
    /// 退出走主循环 break → Main finally 优雅收尾（禁 Environment.Exit——跳 finally 丢观测缓冲）。
    /// </summary>
    public static partial class Program
    {
        // [段1] 常量与状态
        /// <summary>自启配置键——schema 声明（app.json 群）</summary>
        private const string AutoStartKey = "app.autostart";

        /// <summary>注册表 Run 键路径——HKCU（无管理员权限，信任面增量）</summary>
        private const string AutoStartRunPath = @"Software\Microsoft\Windows\CurrentVersion\Run";

        /// <summary>注册表值名——按 exe 名（部署实例自引用自适应）</summary>
        private const string AutoStartRunName = "CatHome4";

        /// <summary>主循环轮询间隔帧——10 秒（FrameTargetMs=100ms × 100；2026-10-16 帧预算 50→100 后随之放大）</summary>
        private const int AutoStartPollFrames = 100;

        /// <summary>退出请求标志——托盘"退出"置位，主循环轮询 break（走 Main finally 优雅收尾）</summary>
        private static volatile bool _trayExitRequested;

        /// <summary>通知弹窗队列——任意线程入队/托盘线程 Timer 消费（BalloonTip 仅 STA 线程安全）</summary>
        private static readonly System.Collections.Concurrent.ConcurrentQueue<string[]> _balloonQueue = new System.Collections.Concurrent.ConcurrentQueue<string[]>();

        /// <summary>自启配置快照——主循环轮询对比（变化才同步注册表）</summary>
        private static string _lastAutoStartValue = "";

        // [段2] 自启初始化与注册表同步——配置为真相，注册表为物化
        /// <summary>
        /// 自启初始化——启动时同步一次注册表（防手动删注册表后漂移）+ 记录配置快照。
        /// 调用点：Bootstrap 完成后（配置群已挂载）。
        /// </summary>
        private static void InitAutoStart()
        {
            ConfigStore cfg = null!;
            if (DataBox.TryResolve<ConfigStore>(out cfg) && cfg != null)
            {
                _lastAutoStartValue = cfg.Get(AutoStartKey, "false");
            }
            else
            {
                _lastAutoStartValue = "false";
            }
            SyncAutoStart();
        }

        /// <summary>
        /// 注册表同步——读配置 app.autostart → 写/删 HKCU Run 值（带引号 exe 路径——路径含空格惯例）。
        /// 幂等：值一致时重复调用无副作用；失败仅 LogStore 警告不崩溃（注册表非关键路径）。
        /// </summary>
        private static void SyncAutoStart()
        {
            string value = "false";
            ConfigStore cfg = null!;
            if (DataBox.TryResolve<ConfigStore>(out cfg) && cfg != null)
            {
                value = cfg.Get(AutoStartKey, "false");
            }
            try
            {
                using (RegistryKey runKey = Registry.CurrentUser.OpenSubKey(AutoStartRunPath, true))
                {
                    if (runKey == null)
                    {
                        LogStore.Add("CatHome4", 2, "自启同步失败: Run 键不存在", "AUTOSTART");
                        return;
                    }
                    if (value == "true" || value == "1")
                    {
                        string exePath = Environment.ProcessPath;
                        if (string.IsNullOrEmpty(exePath))
                        {
                            LogStore.Add("CatHome4", 2, "自启同步失败: 无法解析当前进程路径", "AUTOSTART");
                            return;
                        }
                        runKey.SetValue(AutoStartRunName, "\"" + exePath + "\"");
                    }
                    else
                    {
                        runKey.DeleteValue(AutoStartRunName, false);
                    }
                }
                LogStore.Add("CatHome4", 1, "自启同步: " + value, "AUTOSTART");
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "自启同步失败: " + ex.Message, "AUTOSTART");
            }
        }

        /// <summary>
        /// 主循环轮询——每 5 秒对比自启配置值变化，变化则同步注册表（覆盖前端/config-* 写路径——零基座改动）。
        /// </summary>
        private static void CheckAutoStartPoll()
        {
            string value = "false";
            ConfigStore cfg = null!;
            if (DataBox.TryResolve<ConfigStore>(out cfg) && cfg != null)
            {
                value = cfg.Get(AutoStartKey, "false");
            }
            if (value != _lastAutoStartValue)
            {
                _lastAutoStartValue = value;
                SyncAutoStart();
            }
        }

        /// <summary>
        /// 自启是否开启——读配置（托盘勾选状态唯一源）
        /// </summary>
        /// <returns>true=开启</returns>
        private static bool IsAutoStartOn()
        {
            ConfigStore cfg = null!;
            if (DataBox.TryResolve<ConfigStore>(out cfg) && cfg != null)
            {
                string value = cfg.Get(AutoStartKey, "false");
                return value == "true" || value == "1";
            }
            return false;
        }

        // [段3] 托盘——独立 STA 线程 + Application.Run 消息泵（主循环零改动）
        /// <summary>应用图标加载——从 exe 嵌入图标提取（多尺寸猫爪印 ico 随 ApplicationIcon 编译期嵌入——exe 文件 / 任务栏 / 窗体标题栏 / 托盘四处同源）。失败回退系统图标（托盘不因图标崩溃）。</summary>
        /// <returns>托盘 Icon</returns>
        private static Icon LoadAppIcon()
        {
            try
            {
                string exePath = Environment.ProcessPath;
                if (exePath != null && exePath.Length > 0)
                {
                    Icon extracted = Icon.ExtractAssociatedIcon(exePath);
                    if (extracted != null)
                    {
                        return extracted;
                    }
                }
                LogStore.Add("CatHome4", 2, "应用图标提取失败（exe 路径缺失）——回退系统图标", "TRAY");
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "应用图标提取失败（回退系统图标）: " + ex.Message, "TRAY");
            }
            return SystemIcons.Application;
        }

        /// <summary>
        /// 通知弹窗入队——本轮结束系统通知（Q：开关 app.round_notify 由 ChatSession 判定；跨线程安全——托盘 Timer 消费）。
        /// </summary>
        /// <param name="title">标题（猫 displayName）</param>
        /// <param name="text">正文（末轮回复摘要/Token 统计）</param>
        public static void NotifyBalloon(string title, string text)
        {
            _balloonQueue.Enqueue(new string[] { title, text });
        }

        /// <summary>
        /// 启动托盘线程——仅交互模式调用；后台线程（主线程退出进程即止）。
        /// </summary>
        private static void StartTray()
        {
            Thread trayThread = new Thread(delegate ()
            {
                RunTrayMessageLoop();
            });
            trayThread.SetApartmentState(ApartmentState.STA);
            trayThread.IsBackground = true;
            trayThread.Start();
        }

        /// <summary>
        /// 托盘消息循环——NotifyIcon + ContextMenuStrip（开机自启勾选 / 打开数据目录 / 退出）。
        /// 退出：置标志 + Application.Exit() → 主循环 break → Main finally 优雅收尾。
        /// </summary>
        private static void RunTrayMessageLoop()
        {
            NotifyIcon icon = new NotifyIcon();
            try
            {
                // 区标识与版本并入提示——开发区 / 部署区两实例并存时可区分（A118）
                string trayArea = "";
                if (_portBand != null)
                {
                    trayArea = _portBand.Area;
                }
                string trayVersion = VersionInfo.GetEntryVersion();
                if (trayVersion.Length == 0)
                {
                    trayVersion = "?";
                }
                icon.Icon = LoadAppIcon();
                string trayText = "CatHome4 " + trayVersion + "（" + trayArea + "）";
                // A260 降级角标——常驻可见面（ToolTip 上限 63 字符，超出截断）
                if (StartupDegradation.HasAny)
                {
                    trayText = trayText + " ⚠ 降级 " + StartupDegradation.Count.ToString() + " 项";
                }
                if (trayText.Length > 63)
                {
                    trayText = trayText.Substring(0, 63);
                }
                icon.Text = trayText;
                icon.Visible = true;
                // Q 通知消费——托盘线程 Timer 轮询队列（BalloonTip 仅 STA 线程安全；任意线程入队不越界）
                System.Windows.Forms.Timer balloonTimer = new System.Windows.Forms.Timer();
                balloonTimer.Interval = 1000;
                balloonTimer.Tick += delegate (object s2, EventArgs e2)
                {
                    string[] item;
                    while (_balloonQueue.TryDequeue(out item))
                    {
                        if (item != null && item.Length >= 2 && item[0] != null && item[1] != null)
                        {
                            icon.ShowBalloonTip(3000, item[0], item[1], ToolTipIcon.Info);
                        }
                    }
                };
                balloonTimer.Start();

                ContextMenuStrip menu = new ContextMenuStrip();

                // 开机自启——CheckOnClick 勾选；状态从配置读；点击写配置 + 立即同步注册表
                ToolStripMenuItem autoStartItem = new ToolStripMenuItem("开机自启");
                autoStartItem.CheckOnClick = true;
                autoStartItem.Checked = IsAutoStartOn();
                autoStartItem.Click += delegate (object sender, EventArgs e)
                {
                    string newValue = autoStartItem.Checked ? "true" : "false";
                    ConfigStore cfg = null!;
                    ConfigSchema schema = null!;
                    DataBox.TryResolve<ConfigStore>(out cfg);
                    DataBox.TryResolve<ConfigSchema>(out schema);
                    string error = "";
                    if (cfg == null || !cfg.SetChecked(AutoStartKey, newValue, schema, out error))
                    {
                        autoStartItem.Checked = !autoStartItem.Checked;
                        icon.ShowBalloonTip(3000, "CatHome4", "自启设置失败: " + error, ToolTipIcon.Warning);
                        return;
                    }
                    SyncAutoStart();
                    icon.ShowBalloonTip(1500, "CatHome4", "开机自启已" + (autoStartItem.Checked ? "开启" : "关闭"), ToolTipIcon.Info);
                };
                menu.Items.Add(autoStartItem);

                menu.Items.Add(new ToolStripSeparator());

                // 显示控制台——总控窗（A118；窗口线程按需起停，左键单击同入口）
                ToolStripMenuItem showShellItem = new ToolStripMenuItem("显示控制台");
                showShellItem.Click += delegate (object sender, EventArgs e)
                {
                    ShowShellWindow();
                };
                menu.Items.Add(showShellItem);

                // 打开数据目录——复用既有端点逻辑（explorer.exe + LogStore；托盘线程安全）
                ToolStripMenuItem openDataItem = new ToolStripMenuItem("打开数据目录");
                openDataItem.Click += delegate (object sender, EventArgs e)
                {
                    AdminService.HandleOpenDataDir();
                };
                menu.Items.Add(openDataItem);

                menu.Items.Add(new ToolStripSeparator());

                // 退出——优雅收尾（禁 Environment.Exit：跳 finally 丢观测缓冲）
                ToolStripMenuItem exitItem = new ToolStripMenuItem("退出");
                exitItem.Click += delegate (object sender, EventArgs e)
                {
                    icon.Visible = false;
                    RequestHostExit();
                };
                menu.Items.Add(exitItem);

                icon.ContextMenuStrip = menu;

                // 左键单击——显示/前置总控窗（与菜单项同入口；窗口线程按需起停）
                icon.MouseClick += delegate (object sender, MouseEventArgs e)
                {
                    if (e.Button == MouseButtons.Left)
                    {
                        ShowShellWindow();
                    }
                };

                Application.Run();
            }
            finally
            {
                icon.Visible = false;
                icon.Dispose();
            }
        }
    }
}
