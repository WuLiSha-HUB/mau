using System;
using System.Collections.Concurrent;
using System.Diagnostics;
using System.Drawing;
using System.IO;
using System.Text;
using System.Threading;
using System.Windows.Forms;
using CatHome4.Admin;
using CatHome4.Contracts;
using CatHome4.Platform;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// 桌面外壳桥——IHostShell 的 Windows WinForms 实现（design-ch4-platform §一 出口二）。
    /// 承载：高 DPI 与 AUMID 早段设置 / HKCU Run 自启物化 / 系统托盘 / 总控窗 / 启动降级提示窗 / 失败与通知面。
    /// 边界：只读写进程级只读面与配置读面（ThreadGuard 域外）；退出只置标志，收尾集中在内核 Main finally。
    /// </summary>
    public sealed class WinHostShell : IHostShell
    {

        /// <summary>应用用户模型 ID——通知与任务栏归组标识（换键 = 换通知图标缓存）</summary>
        private const string AppHostAumid = "CatHome4.Host";

        /// <summary>主循环轮询间隔帧——10 秒（帧预算 100 ms × 100）</summary>
        private const int AutoStartPollFrames = 100;

        /// <summary>活动实例——窗体与托盘线程经它取外壳状态（同进程单实例）</summary>
        internal static WinHostShell Current;

        /// <summary>宿主启动时刻（TickCount64）——总控窗「运行时长」起算点</summary>
        private long _startTicks;

        /// <summary>退出请求标志——托盘「退出」/ 总控窗「停止运行」置位，主循环轮询 break</summary>
        private volatile bool _exitRequested;

        /// <summary>通知弹窗队列——任意线程入队 / 托盘线程 Timer 消费（BalloonTip 仅 STA 线程安全）</summary>
        private readonly ConcurrentQueue<string[]> _balloonQueue = new ConcurrentQueue<string[]>();

        /// <summary>自启配置快照——主循环轮询对比（变化才同步注册表）</summary>
        private bool _lastAutoStartOn;

        /// <summary>自启轮询计帧</summary>
        private int _autoStartPoll;

        /// <summary>窗口线程引用——null=当前无窗口线程（开窗幂等判据）</summary>
        private Thread _shellFormThread;

        /// <summary>活动窗体引用——null=当前无活动窗体（托盘前置激活判据）</summary>
        private Form _shellForm;

        /// <summary>外壳状态锁——窗口线程创建/销毁与托盘线程之间的跨线程同步</summary>
        private readonly object _shellLock = new object();

        /// <summary>
        /// 启动时刻——总控窗按它算已运行时间（TickCount64 毫秒基准）。
        /// </summary>
        internal long StartTicks
        {
            get { return _startTicks; }
        }

        // [段2] IHostShell
        /// <summary>
        /// 早段初始化——高 DPI（必须早于任何窗口创建）+ 启动时刻 + 应用用户模型 ID。
        /// </summary>
        public void InitEarly()
        {
            Current = this;
            _startTicks = Environment.TickCount64;
            bool dpiOk = false;
            try
            {
                dpiOk = Application.SetHighDpiMode(HighDpiMode.PerMonitorV2);
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "高 DPI 设置异常（按进程默认模式继续）：" + ex.Message, "SHELL");
            }
            if (!dpiOk)
            {
                LogStore.Add("CatHome4", 1, "高 DPI 设置未生效（进程默认模式——可能已有窗口创建）", "SHELL");
            }
            InitAppUserModelId();
        }

        /// <summary>
        /// 外壳就绪——自启对齐 + 托盘线程 + 总控窗（仅交互模式由内核调用）。
        /// </summary>
        public void OnHostReady()
        {
            InitAutoStart();
            StartTray();
            ShowShellWindow();
        }

        /// <summary>
        /// 主循环每帧泵——自启配置变化轮询（配置为真相，注册表为物化；覆盖前端 / config-* 写路径）。
        /// </summary>
        public void PumpFrame()
        {
            _autoStartPoll = _autoStartPoll + 1;
            if (_autoStartPoll < AutoStartPollFrames)
            {
                return;
            }
            _autoStartPoll = 0;
            bool on = IsAutoStartOn();
            if (on != _lastAutoStartOn)
            {
                _lastAutoStartOn = on;
                SyncAutoStart();
            }
        }

        /// <summary>退出请求标志——内核主循环据此 break。</summary>
        public bool ExitRequested
        {
            get { return _exitRequested; }
        }

        /// <summary>
        /// 启动健康面——交互：有降级则起提示窗；非交互：stderr 出声（headless 无 UI 出口的补位）。
        /// </summary>
        /// <param name="interactive">是否交互模式</param>
        public void ShowStartupHealth(bool interactive)
        {
            if (!interactive)
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
                if (HostAccess.View.StartupReportPath.Length > 0)
                {
                    Console.Error.WriteLine("[CH4]   报告：" + HostAccess.View.StartupReportPath);
                }
                return;
            }
            ShowStartupDegradeWindow();
        }

        /// <summary>
        /// 启动失败提示——WinExe 无控制台时代的失败可见面（非交互由内核只返退出码，不弹窗）。
        /// </summary>
        /// <param name="message">失败摘要</param>
        public void ShowStartupFailure(string message)
        {
            string text = "CatHome4 启动失败：\n\n" + message + "\n\n日志目录：Data/runs/<时间戳>/err_all.txt";
            try
            {
                MessageBox.Show(text, "CatHome4 启动失败", MessageBoxButtons.OK, MessageBoxIcon.Error);
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 3, "启动失败弹窗显示异常：" + ex.Message, "SHELL");
            }
        }

        /// <summary>
        /// 通知入队——托盘线程 Timer 消费（跨线程安全）。
        /// </summary>
        /// <param name="title">标题（猫 displayName）</param>
        /// <param name="text">正文（末轮回复摘要 / Token 统计）</param>
        public void NotifyBalloon(string title, string text)
        {
            _balloonQueue.Enqueue(new string[] { title, text });
        }

        /// <summary>
        /// 打开目录或定位文件——交资源管理器（失败记 L2，不影响主流程）。
        /// </summary>
        /// <param name="path">目录或文件路径</param>
        public void OpenFolder(string path)
        {
            PlatformServices.ShellLauncher.OpenFolder(path);
        }

        /// <summary>
        /// 打开外部链接——交默认浏览器（进程外动作，不触碰业务对象）。
        /// </summary>
        /// <param name="url">目标地址（空串直接忽略）</param>
        internal void OpenExternalUrl(string url)
        {
            PlatformServices.ShellLauncher.OpenUrl(url);
        }

        /// <summary>
        /// 请求宿主退出——两个 UI 退出入口（窗体「停止运行」/ 托盘「退出」）的唯一出口。
        /// 只置标志 + 结束消息泵；收尾动作集中在内核 Main finally（观测落盘 / 浏览器清理）。
        /// </summary>
        internal void RequestExit()
        {
            _exitRequested = true;
            Application.Exit();
        }

        // [段3] 自启——配置为真相，注册表为物化
        /// <summary>
        /// 自启是否开启（读配置）。
        /// </summary>
        /// <returns>true=开启</returns>
        internal bool IsAutoStartOn()
        {
            if (HostAccess.View == null)
            {
                return false;
            }
            return HostAccess.View.IsAutoStartOn();
        }

        /// <summary>
        /// 写自启开关——配置写入 + 注册表同步；失败回滚由调用方处理（返回原因非空即失败）。
        /// </summary>
        /// <param name="on">目标状态</param>
        /// <returns>空串 = 成功；否则为失败原因</returns>
        internal string ApplyAutoStart(bool on)
        {
            if (HostAccess.View == null)
            {
                return "宿主读面未就绪";
            }
            string error = HostAccess.View.SetAutoStart(on);
            if (error.Length > 0)
            {
                return error;
            }
            _lastAutoStartOn = on;
            SyncAutoStart();
            return "";
        }

        /// <summary>
        /// 自启初始化——启动时同步一次注册表（防手动删注册表后漂移）+ 记录配置快照。
        /// </summary>
        private void InitAutoStart()
        {
            _lastAutoStartOn = IsAutoStartOn();
            SyncAutoStart();
        }

        /// <summary>自启物化同步——读配置 app.autostart → 交平台自启服务物化（Windows 写/删 HKCU Run 值，实现在 CatHome4.Platform）。幂等：值一致时重复调用无副作用；失败只记 L2 不崩溃（非关键路径）。</summary>
        private void SyncAutoStart()
        {
            bool on = IsAutoStartOn();
            PlatformServices.AutoStart.Sync(on);
        }

        // [段4] 应用图标与 AUMID
        /// <summary>应用图标加载——从 exe 嵌入图标提取（exe 文件 / 任务栏 / 窗体标题栏 / 托盘四处同源）。失败回退系统图标。</summary>
        /// <returns>托盘与窗体 Icon</returns>
        internal Icon LoadAppIcon()
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
        /// shell32 显式应用用户模型 ID——进程级设置（Windows 通知按 AUMID 关联应用图标并缓存）。
        /// </summary>
        /// <param name="appId">应用 ID</param>
        /// <returns>HRESULT（0=成功）</returns>
        [System.Runtime.InteropServices.DllImport("shell32.dll", CharSet = System.Runtime.InteropServices.CharSet.Unicode)]
        private static extern int SetCurrentProcessExplicitAppUserModelID(string appId);

        /// <summary>
        /// 注册应用用户模型 ID——早段调用。Win10+ 把 NotifyIcon 气泡转成系统通知，图标取自 Shell 按 AUMID 关联的应用图标缓存。
        /// 做法：显式 AUMID + 注册表 IconUri 指向当前 exe；任一步失败仅告警，不影响宿主运行。
        /// </summary>
        private void InitAppUserModelId()
        {
            try
            {
                int hr = SetCurrentProcessExplicitAppUserModelID(AppHostAumid);
                if (hr != 0)
                {
                    LogStore.Add("CatHome4", 2, "AUMID 设置返回非零 HRESULT: 0x" + hr.ToString("X8") + "（通知图标可能沿用旧缓存）", "SHELL");
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "AUMID 设置失败（通知图标可能沿用旧缓存）：" + ex.Message, "SHELL");
            }
            string exePath = Environment.ProcessPath;
            if (exePath == null || exePath.Length == 0)
            {
                LogStore.Add("CatHome4", 2, "AUMID 注册表写入跳过（exe 路径不可解析）", "SHELL");
                return;
            }
            try
            {
                using (Microsoft.Win32.RegistryKey key = Microsoft.Win32.Registry.CurrentUser.CreateSubKey("Software\\Classes\\AppUserModelId\\" + AppHostAumid))
                {
                    if (key != null)
                    {
                        key.SetValue("DisplayName", "CatHome4");
                        key.SetValue("IconUri", exePath);
                    }
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "AUMID 注册表写入失败（通知图标可能沿用旧缓存）：" + ex.Message, "SHELL");
            }
        }

        // [段5] 启动降级提示窗
        /// <summary>
        /// 启动提示窗——独立非模态窗口（独立 STA 线程 + 自有消息循环，不占用主循环）；无降级条目时不弹。
        /// </summary>
        private void ShowStartupDegradeWindow()
        {
            if (!StartupDegradation.HasAny)
            {
                return;
            }
            Thread thread = new Thread(StartupDegradeThreadProc);
            thread.SetApartmentState(ApartmentState.STA);
            thread.IsBackground = true;
            try
            {
                thread.Start();
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 3, "启动提示窗线程启动失败：" + ex.Message, "SHELL");
            }
        }

        /// <summary>
        /// 启动提示窗线程体——构建窗体并运行消息循环（线程退出即窗关闭）。
        /// </summary>
        private void StartupDegradeThreadProc()
        {
            try
            {
                StartupDegradeEntry[] entries = StartupDegradation.Snapshot();
                Form form = new Form();
                form.Text = "CatHome4 已启动（" + entries.Length.ToString() + " 项降级）";
                form.Width = 760;
                form.Height = 480;
                form.StartPosition = FormStartPosition.CenterScreen;
                form.MinimizeBox = false;

                TextBox box = new TextBox();
                box.Multiline = true;
                box.ReadOnly = true;
                box.ScrollBars = ScrollBars.Vertical;
                box.Dock = DockStyle.Fill;
                box.Font = new Font("Microsoft YaHei UI", 9F);
                StringBuilder sb = new StringBuilder();
                sb.AppendLine("宿主已启动。下列能力因配置或环境问题处于降级形态，可修复后重启恢复：");
                sb.AppendLine("");
                for (int i = 0; i < entries.Length; i = i + 1)
                {
                    StartupDegradeEntry entry = entries[i];
                    sb.AppendLine("【" + entry.Ability + "】" + entry.Code + "（" + entry.State + (entry.Count > 1 ? " ×" + entry.Count.ToString() : "") + "）");
                    sb.AppendLine("    原因：" + entry.Detail);
                    sb.AppendLine("    处理：" + entry.Fix);
                    sb.AppendLine("");
                }
                string reportPath = HostAccess.View.StartupReportPath;
                if (reportPath.Length > 0)
                {
                    sb.AppendLine("完整报告：" + reportPath);
                }
                box.Text = sb.ToString();

                FlowLayoutPanel bar = new FlowLayoutPanel();
                bar.Dock = DockStyle.Bottom;
                bar.Height = 56;
                bar.FlowDirection = FlowDirection.RightToLeft;
                bar.WrapContents = false;
                bar.AutoScroll = true;

                // 按钮宽高一律 AutoSize——高 DPI 下固定像素宽会把中文按钮文字裁断（A260 目视验收判例）
                Button btnOk = new Button();
                btnOk.Text = "知道了";
                btnOk.AutoSize = true;
                btnOk.AutoSizeMode = AutoSizeMode.GrowAndShrink;
                btnOk.Height = 30;
                btnOk.Margin = new Padding(6);
                btnOk.Click += delegate (object sender, EventArgs e) { form.Close(); };

                Button btnReport = new Button();
                btnReport.Text = "查看启动报告";
                btnReport.AutoSize = true;
                btnReport.AutoSizeMode = AutoSizeMode.GrowAndShrink;
                btnReport.Height = 30;
                btnReport.Margin = new Padding(6);
                btnReport.Click += delegate (object sender, EventArgs e) { OpenFolder(reportPath); };

                Button btnConfig = new Button();
                btnConfig.Text = "打开配置目录";
                btnConfig.AutoSize = true;
                btnConfig.AutoSizeMode = AutoSizeMode.GrowAndShrink;
                btnConfig.Height = 30;
                btnConfig.Margin = new Padding(6);
                btnConfig.Click += delegate (object sender, EventArgs e)
                {
                    OpenFolder(Path.Combine(HostAccess.View.DataRoot, "Data", "config"));
                };

                bar.Controls.Add(btnOk);
                bar.Controls.Add(btnReport);
                bar.Controls.Add(btnConfig);
                form.Controls.Add(box);
                form.Controls.Add(bar);
                Application.Run(form);
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 3, "启动提示窗显示异常：" + ex.Message, "SHELL");
            }
        }

        // [段6] 托盘
        /// <summary>
        /// 启动托盘线程——仅交互模式调用；后台线程（主线程退出进程即止）。
        /// </summary>
        private void StartTray()
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
        /// 托盘消息循环——NotifyIcon + ContextMenuStrip（开机自启勾选 / 显示控制台 / 打开数据目录 / 退出）。
        /// 退出：置标志 + Application.Exit() → 主循环 break → 内核 finally 优雅收尾。
        /// </summary>
        private void RunTrayMessageLoop()
        {
            NotifyIcon icon = new NotifyIcon();
            try
            {
                // 区标识与版本并入提示——开发区 / 部署区两实例并存时可区分（A118）
                string trayArea = "";
                if (HostAccess.View != null)
                {
                    trayArea = HostAccess.View.AreaLabel;
                }
                string trayVersion = "?";
                if (HostAccess.View != null)
                {
                    trayVersion = HostAccess.View.VersionInfo;
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
                    string error = ApplyAutoStart(autoStartItem.Checked);
                    if (error.Length > 0)
                    {
                        autoStartItem.Checked = !autoStartItem.Checked;
                        icon.ShowBalloonTip(3000, "CatHome4", "自启设置失败: " + error, ToolTipIcon.Warning);
                        return;
                    }
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
                    RequestExit();
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

        // [段7] 总控窗
        /// <summary>
        /// 显示总控窗——托盘点击入口（幂等）。
        /// 已有活动窗体 → 跨线程前置激活；无 → 新建 STA 线程拉起（关窗即线程结束，再次点击重新拉起）。
        /// </summary>
        internal void ShowShellWindow()
        {
            Form existing = null;
            bool starting = false;
            lock (_shellLock)
            {
                existing = _shellForm;
                starting = _shellFormThread != null;
            }
            if (existing != null && !existing.IsDisposed)
            {
                try
                {
                    existing.BeginInvoke(new Action(delegate () { ActivateShellWindow(existing); }));
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 2, "总控窗前置于失败：" + ex.Message, "SHELL");
                }
                return;
            }
            if (starting)
            {
                // 窗口线程已拉起但窗体尚未建立——本次点击忽略，避免开出第二个窗
                return;
            }
            Thread thread = new Thread(ShellFormThreadProc);
            thread.SetApartmentState(ApartmentState.STA);
            thread.IsBackground = true;
            lock (_shellLock)
            {
                _shellFormThread = thread;
            }
            try
            {
                thread.Start();
            }
            catch (Exception ex)
            {
                lock (_shellLock)
                {
                    _shellFormThread = null;
                }
                LogStore.Add("CatHome4", 3, "总控窗线程启动失败：" + ex.Message, "SHELL");
            }
        }

        /// <summary>
        /// 前置激活总控窗——窗口线程内执行（跨线程经 BeginInvoke 投递）。
        /// </summary>
        /// <param name="form">目标窗体</param>
        private static void ActivateShellWindow(Form form)
        {
            if (form.WindowState == FormWindowState.Minimized)
            {
                form.WindowState = FormWindowState.Normal;
            }
            form.Show();
            form.Activate();
            form.BringToFront();
        }

        /// <summary>
        /// 窗口线程主体——建窗 + 消息泵（Application.Run 于窗体关闭时返回 → 线程函数结束 → 线程自然终止）。
        /// </summary>
        private void ShellFormThreadProc()
        {
            ShellForm form = null;
            try
            {
                form = new ShellForm(this);
                lock (_shellLock)
                {
                    _shellForm = form;
                }
                Application.Run(form);
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 3, "总控窗线程异常退出：" + ex.Message, "SHELL");
            }
            finally
            {
                lock (_shellLock)
                {
                    _shellForm = null;
                    _shellFormThread = null;
                }
                if (form != null)
                {
                    form.Dispose();
                }
            }
        }
    }
}
