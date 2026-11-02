using System;
using System.Diagnostics;
using System.Drawing;
using System.Threading;
using System.Windows.Forms;
using Mau.Runtime;
using CatHome4.Admin;
using CatHome4.Contracts;
using CatHome4.Http;

namespace CH4
{
    /// <summary>
    /// Program 外壳分部——A118：WinExe 桌面外壳（总控窗 + 托盘常驻）。
    /// 边界（规格 Project/CH4/design-ch4-host-shell.md）：窗口线程独立 STA、按需起停，不触碰业务状态
    /// （只读进程级只读面 + 既有配置写面）；两个退出入口（窗体「停止运行」/ 托盘「退出」）只通知
    /// runtime 自我销毁——收尾动作集中在 Main finally。
    /// </summary>
    public static partial class Program
    {
        // [段1] 外壳状态
        /// <summary>宿主启动时刻（TickCount64 毫秒基准）——已运行时间的起算点</summary>
        private static long _shellStartTicks;

        /// <summary>窗口线程引用——null=当前无窗口线程（开窗幂等判据）</summary>
        private static Thread _shellFormThread;

        /// <summary>活动窗体引用——null=当前无活动窗体（托盘前置激活判据）</summary>
        private static Form _shellForm;

        /// <summary>外壳状态锁——窗口线程创建/销毁与托盘线程之间的跨线程同步</summary>
        private static readonly object _shellLock = new object();

        // [段2] 外壳初始化
        /// <summary>
        /// 外壳初始化——记录启动时刻 + 设置高 DPI 模式（PerMonitorV2）。由 Main 最早段调用。
        /// 🔴 必须在任何窗口创建之前调用：进程级 DPI 感知设置晚于窗口创建即失败（返回 false）。
        /// </summary>
        internal static void ShellInit()
        {
            _shellStartTicks = Environment.TickCount64;
            bool dpiOk = false;
            try
            {
                dpiOk = Application.SetHighDpiMode(HighDpiMode.PerMonitorV2);
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "高 DPI 设置异常（按进程默认模式继续）：" + ex.Message, "SHELL");
                return;
            }
            if (!dpiOk)
            {
                LogStore.Add("CatHome4", 1, "高 DPI 设置未生效（进程默认模式——可能已有窗口创建）", "SHELL");
            }
        }

        // [段3] 开窗与退出入口
        /// <summary>
        /// 显示总控窗——托盘点击入口（幂等）。
        /// 已有活动窗体 → 跨线程前置激活；无 → 新建 STA 线程拉起（关窗即线程结束，再次点击重新拉起）。
        /// </summary>
        internal static void ShowShellWindow()
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
        private static void ShellFormThreadProc()
        {
            ShellForm form = null;
            try
            {
                form = new ShellForm();
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

        // [段5] 非交互输出落盘（A118——WinExe 无控制台，shell 直调取不到 stdout）
        /// <summary>控制台输出落盘写器——null=未启用</summary>
        private static System.IO.StreamWriter _cmdCaptureWriter;

        /// <summary>
        /// 启用控制台输出落盘——非交互模式（`--run` / `--script` / `--selfcheck` / `--probe-llm`）
        /// 把 Console 输出**双写** `Data/runs/cmd_&lt;时间戳&gt;.txt`。
        /// 🔴 保留原 stdout——父进程显式重定向（SetUp / mau-setup）的捕获链路不受影响，本方法只增落盘副本。
        /// </summary>
        internal static void EnableCmdCapture()
        {
            if (_cmdCaptureWriter != null)
            {
                return;
            }
            try
            {
                string dataRoot = ResolveDataRoot();
                string runDir = System.IO.Path.Combine(dataRoot, "Data", "runs");
                System.IO.Directory.CreateDirectory(runDir);
                string file = System.IO.Path.Combine(runDir, "cmd_" + DateTime.Now.ToString("yyyyMMdd_HHmmss") + ".txt");
                System.IO.StreamWriter writer = new System.IO.StreamWriter(file, false, new System.Text.UTF8Encoding(false));
                writer.AutoFlush = true;
                _cmdCaptureWriter = writer;
                Console.SetOut(new TeeTextWriter(Console.Out, writer));
            }
            catch (Exception ex)
            {
                _cmdCaptureWriter = null;
                LogStore.Add("CatHome4", 2, "控制台输出落盘启用失败（继续走 stdout）：" + ex.Message, "SHELL");
            }
        }

        /// <summary>
        /// 关闭输出落盘写器——Main finally 调用（与观测写器同批收尾，AutoFlush 下不丢尾）。
        /// </summary>
        internal static void CloseCmdCapture()
        {
            if (_cmdCaptureWriter == null)
            {
                return;
            }
            try
            {
                Console.Out.Flush();
                _cmdCaptureWriter.Flush();
                _cmdCaptureWriter.Dispose();
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "控制台输出落盘收尾异常：" + ex.Message, "SHELL");
            }
            _cmdCaptureWriter = null;
        }

        /// <summary>
        /// 输出双写器——原 stdout + 落盘文件（A118）；写操作加锁（观测行来自多线程）。
        /// 只覆写 Write(char) / Write(string) / Flush——基类其余写入路径最终都汇聚到这两个入口。
        /// </summary>
        private sealed class TeeTextWriter : System.IO.TextWriter
        {
            /// <summary>主输出（stdout / 父进程重定向管道）</summary>
            private readonly System.IO.TextWriter _primary;

            /// <summary>副本输出（落盘）</summary>
            private readonly System.IO.TextWriter _secondary;

            /// <summary>写锁——跨线程写安全</summary>
            private readonly object _writeLock = new object();

            /// <summary>
            /// 构造双写器。
            /// </summary>
            /// <param name="primary">主输出</param>
            /// <param name="secondary">副本输出</param>
            public TeeTextWriter(System.IO.TextWriter primary, System.IO.TextWriter secondary)
            {
                _primary = primary;
                _secondary = secondary;
            }

            /// <summary>输出编码——沿用主输出</summary>
            public override System.Text.Encoding Encoding
            {
                get { return _primary.Encoding; }
            }

            /// <summary>
            /// 写单字符——两路同步。
            /// </summary>
            /// <param name="value">字符</param>
            public override void Write(char value)
            {
                lock (_writeLock)
                {
                    _primary.Write(value);
                    _secondary.Write(value);
                }
            }

            /// <summary>
            /// 写字符串——两路同步（null 直接忽略）。
            /// </summary>
            /// <param name="value">字符串</param>
            public override void Write(string value)
            {
                if (value == null)
                {
                    return;
                }
                lock (_writeLock)
                {
                    _primary.Write(value);
                    _secondary.Write(value);
                }
            }

            /// <summary>
            /// 刷新两路输出。
            /// </summary>
            public override void Flush()
            {
                lock (_writeLock)
                {
                    _primary.Flush();
                    _secondary.Flush();
                }
            }
        }

        // [段6] 应用用户模型 ID（AUMID）——Windows 通知的应用身份键
        /// <summary>应用用户模型 ID——通知与任务栏归组标识（换键 = 换通知图标缓存，2026-10-01 A118）</summary>
        private const string AppHostAumid = "CatHome4.Host";

        /// <summary>
        /// shell32 显式应用用户模型 ID——进程级设置（Windows 通知按 AUMID 关联应用图标并缓存）。
        /// </summary>
        /// <param name="appId">应用 ID</param>
        /// <returns>HRESULT（0=成功）</returns>
        [System.Runtime.InteropServices.DllImport("shell32.dll", CharSet = System.Runtime.InteropServices.CharSet.Unicode)]
        private static extern int SetCurrentProcessExplicitAppUserModelID(string appId);

        /// <summary>
        /// 注册应用用户模型 ID——Main 早段调用。
        /// 背景：Win10+ 把 NotifyIcon 气泡转成系统通知，图标取自 Shell 按 AUMID 关联的应用图标缓存（不是 NotifyIcon.Icon）——
        /// 换 exe 图标后托盘与窗角已更新，通知弹窗仍显示旧图（实测）。
        /// 做法：显式 AUMID + 注册表 IconUri 指向当前 exe；任一步失败仅告警，不影响宿主运行。
        /// </summary>
        internal static void InitAppUserModelId()
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

        /// <summary>
        /// 启动失败提示——WinExe（无控制台）时代的失败可见面：弹窗显示错误摘要。
        /// 非交互形态（脚本 / 自检 / 探针）由调用方静默返回退出码，不弹窗。
        /// </summary>
        /// <param name="message">失败摘要</param>
        internal static void ShowStartupFailure(string message)
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
        /// 请求宿主退出——两个 UI 退出入口（窗体「停止运行」/ 托盘「退出」）的唯一出口。
        /// 只通知 runtime 自我销毁（置标志）；收尾动作集中在 Main finally（观测落盘 / 浏览器清理）。
        /// </summary>
        internal static void RequestHostExit()
        {
            _trayExitRequested = true;
            Application.Exit();
        }

        /// <summary>
        /// 打开外部链接——交默认浏览器（进程外动作，不触碰业务对象）。
        /// </summary>
        /// <param name="url">目标地址（空串直接忽略）</param>
        internal static void OpenExternalUrl(string url)
        {
            if (url == null || url.Length == 0)
            {
                return;
            }
            try
            {
                ProcessStartInfo psi = new ProcessStartInfo(url);
                psi.UseShellExecute = true;
                Process.Start(psi);
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "打开链接失败：" + url + "——" + ex.Message, "SHELL");
            }
        }

        // [段4] 总控窗
        /// <summary>
        /// 总控窗——七项内容：猫头标题 / 版本 / 构建时刻 / 运行时长 / 端点 / 开机自启 / 停止运行。
        /// 关闭即结束本窗线程（Application.Run 返回）；数据面只用进程级只读值（版本 / 端口）
        /// 与既有配置读面（app.autostart）——不读会话注册表与 Flow 句柄表（主线程独占面）。
        /// </summary>
        private sealed class ShellForm : Form
        {
            /// <summary>说明列左边距（96 DPI 基准——AutoScaleMode=Dpi 随系统缩放）</summary>
            private const int LeftMargin = 18;

            /// <summary>值列起点（96 DPI 基准）</summary>
            private const int ValueX = 100;

            /// <summary>运行时长标签——每秒刷新</summary>
            private Label _uptimeLabel;

            /// <summary>运行时长刷新计时器（System.Windows.Forms.Timer——窗体线程）</summary>
            private System.Windows.Forms.Timer _uptimeTimer;

            /// <summary>标题字体——自建，随窗体释放（避免 GDI 句柄滞留）</summary>
            private Font _titleFont;
            /// <summary>开机自启复选框——字段化：定时器据此与配置对齐（托盘侧改动同步显示）</summary>
            private CheckBox _autoStartBox;

            /// <summary>电源按钮——自绘 PictureBox（无系统边框 / 无 hover 灰框）</summary>
            private PictureBox _powerButton;

            /// <summary>电源图标——常态（红底渐变 + 白符号 + 渐变描边）</summary>
            private Image _powerIconNormal;

            /// <summary>电源图标——悬停态（描边加亮加粗）</summary>
            private Image _powerIconHover;

            /// <summary>
            /// 构造总控窗——固定尺寸 + 手写布局（无设计器）。
            /// </summary>
            public ShellForm()
            {
                // [段1] 窗体属性——固定尺寸、不可缩放、居中
                Text = "CatHome4";
                FormBorderStyle = FormBorderStyle.FixedDialog;
                MaximizeBox = false;
                MinimizeBox = true;
                StartPosition = FormStartPosition.CenterScreen;
                ClientSize = new Size(430, 414);
                AutoScaleMode = AutoScaleMode.Dpi;
                ApplyFormIcon();
                // [段2] 标题（猫头颜文字）/ 版本 / 构建时刻 / 运行时长
                _titleFont = new Font(Font.FontFamily, Font.Size + 5, FontStyle.Bold);
                Controls.Add(BuildLabel("(=^･ω･^=)", LeftMargin, 16, _titleFont));
                string version = VersionInfo.GetEntryVersion();
                if (version.Length == 0)
                {
                    version = "?";
                }
                Controls.Add(BuildLabel("版本", LeftMargin, 62, Font));
                Controls.Add(BuildLabel(version, ValueX, 62, Font));
                Controls.Add(BuildLabel("构建于", LeftMargin, 88, Font));
                Controls.Add(BuildLabel(VersionInfo.GetEntryBuildTime(), ValueX, 88, Font));
                Controls.Add(BuildLabel("运行时长", LeftMargin, 114, Font));
                _uptimeLabel = BuildLabel("", ValueX, 114, Font);
                Controls.Add(_uptimeLabel);
                // [段3] 端点——本机访问地址（未监听=空串，显示「未监听」且不可点）
                Controls.Add(BuildLabel("管理面板", LeftMargin, 148, Font));
                Controls.Add(BuildLink(ResolvePanelUrl(), ValueX, 148));
                Controls.Add(BuildLabel("Majordomo", LeftMargin, 174, Font));
                Controls.Add(BuildLink(ResolveMajorUrl(), ValueX, 174));
                // [段4] 开机自启——与托盘菜单同源（app.autostart）；勾选框状态即反馈（不做文字提示行）
                // A260 启动降级——常驻可见面（与托盘 ToolTip / 提示窗同源；无降级时不显示，点击打开启动报告）
                if (StartupDegradation.HasAny)
                {
                    Label degradeLabel = BuildLabel("⚠ 启动降级 " + StartupDegradation.Count.ToString() + " 项（点此查看报告）", LeftMargin, 242, Font);
                    degradeLabel.ForeColor = Color.FromArgb(255, 200, 90, 40);
                    degradeLabel.Cursor = Cursors.Hand;
                    degradeLabel.Click += delegate (object sender, EventArgs e) { OpenFolder(StartupReportPath); };
                    Controls.Add(degradeLabel);
                }
                _autoStartBox = new CheckBox();
                _autoStartBox.Text = "开机自启";
                _autoStartBox.AutoSize = true;
                _autoStartBox.Location = new Point(LeftMargin, 210);
                _autoStartBox.Font = Font;
                _autoStartBox.Checked = IsAutoStartOn();
                _autoStartBox.Click += delegate (object sender, EventArgs e) { OnAutoStartToggled(_autoStartBox); };
                Controls.Add(_autoStartBox);
                // [段5] 停止运行——电源图标按钮（自绘 PictureBox：无系统边框、无 hover 灰框；破坏性动作：二次确认）
                // 图标 = 红底渐变圆 + 白色电源符号 + 渐变描边；悬停态描边加亮（莎 2026-10-01 指定）
                _powerIconNormal = CreatePowerIcon(100, false);
                _powerIconHover = CreatePowerIcon(100, true);
                _powerButton = new PictureBox();
                _powerButton.Size = new Size(100, 100);
                _powerButton.Location = new Point((ClientSize.Width - _powerButton.Size.Width) / 2, 276);
                _powerButton.Image = _powerIconNormal;
                _powerButton.SizeMode = PictureBoxSizeMode.StretchImage;
                _powerButton.BackColor = Color.Transparent;
                _powerButton.Cursor = Cursors.Hand;
                _powerButton.MouseEnter += delegate (object sender, EventArgs e) { _powerButton.Image = _powerIconHover; };
                _powerButton.MouseLeave += delegate (object sender, EventArgs e) { _powerButton.Image = _powerIconNormal; };
                _powerButton.Click += delegate (object sender, EventArgs e) { OnStopClicked(); };
                Controls.Add(_powerButton);
                Label stopCaption = BuildLabel("停止运行", 0, 382, Font);
                stopCaption.ForeColor = Color.FromArgb(255, 100, 100, 100);
                stopCaption.Location = new Point((ClientSize.Width - stopCaption.PreferredWidth) / 2, 382);
                Controls.Add(stopCaption);
                // [段6] 运行时长刷新——每秒
                _uptimeTimer = new System.Windows.Forms.Timer();
                _uptimeTimer.Interval = 1000;
                _uptimeTimer.Tick += delegate (object sender, EventArgs e) { RefreshUptime(); RefreshAutoStartState(); };
                _uptimeTimer.Start();
                RefreshUptime();
                RefreshAutoStartState();
            }

            /// <summary>
            /// 刷新已运行时间——每秒调用（TickCount64 差值换算 h/m/s）。
            /// </summary>
            private void RefreshUptime()
            {
                long elapsedMs = Environment.TickCount64 - _shellStartTicks;
                if (elapsedMs < 0)
                {
                    elapsedMs = 0;
                }
                long totalSeconds = elapsedMs / 1000;
                long hours = totalSeconds / 3600;
                long minutes = (totalSeconds % 3600) / 60;
                long seconds = totalSeconds % 60;
                if (_uptimeLabel != null)
                {
                    _uptimeLabel.Text = hours.ToString() + " h " + minutes.ToString() + " m " + seconds.ToString() + " s";
                }
            }

            /// <summary>
            /// 建标签——统一样式（AutoSize + 指定字体）。
            /// </summary>
            /// <param name="text">文本</param>
            /// <param name="x">X 坐标（96 DPI 基准）</param>
            /// <param name="y">Y 坐标（96 DPI 基准）</param>
            /// <param name="font">字体</param>
            /// <returns>标签控件</returns>
            private Label BuildLabel(string text, int x, int y, Font font)
            {
                Label label = new Label();
                label.Text = text;
                label.AutoSize = true;
                label.Location = new Point(x, y);
                label.Font = font;
                return label;
            }

            /// <summary>
            /// 建端点链接——空串按「未监听」显示且不可点（不猜端口，同 info 的 endpoint 语义）。
            /// </summary>
            /// <param name="url">地址（空串=未监听）</param>
            /// <param name="x">X 坐标（96 DPI 基准）</param>
            /// <param name="y">Y 坐标（96 DPI 基准）</param>
            /// <returns>链接控件</returns>
            private LinkLabel BuildLink(string url, int x, int y)
            {
                LinkLabel link = new LinkLabel();
                link.AutoSize = true;
                link.Location = new Point(x, y);
                link.Font = Font;
                if (url.Length == 0)
                {
                    link.Text = "（未监听）";
                    link.Enabled = false;
                }
                else
                {
                    link.Text = url;
                    link.LinkClicked += delegate (object sender, LinkLabelLinkClickedEventArgs e) { OpenExternalUrl(url); };
                }
                return link;
            }

            /// <summary>
            /// 解析管理面板地址——未监听返回空串。
            /// </summary>
            /// <returns>地址或空串</returns>
            private static string ResolvePanelUrl()
            {
                HttpHost host = _httpHost;
                if (host == null || host.Port <= 0)
                {
                    return "";
                }
                return "http://127.0.0.1:" + host.Port.ToString();
            }

            /// <summary>
            /// 解析 majordomo 对话页地址——未监听返回空串。
            /// </summary>
            /// <returns>地址或空串</returns>
            private static string ResolveMajorUrl()
            {
                int port = CatHostService.MajorPort;
                if (port <= 0)
                {
                    return "";
                }
                return "http://127.0.0.1:" + port.ToString();
            }

            /// <summary>
            /// 窗体图标——与托盘 / exe 同源（LoadAppIcon 自 exe 嵌入图标提取；失败静默回退默认）。
            /// </summary>
            private void ApplyFormIcon()
            {
                try
                {
                    Icon = LoadAppIcon();
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 2, "窗体图标设置失败（回退默认图标）：" + ex.Message, "SHELL");
                }
            }

            /// <summary>
            /// 开机自启勾选——写配置（唯一真相）+ 同步注册表；失败回滚勾选并提示。
            /// </summary>
            /// <param name="box">自启复选框</param>
            private void OnAutoStartToggled(CheckBox box)
            {
                string newValue = "false";
                if (box.Checked)
                {
                    newValue = "true";
                }
                ConfigStore cfg = null;
                ConfigSchema schema = null;
                DataBox.TryResolve<ConfigStore>(out cfg);
                DataBox.TryResolve<ConfigSchema>(out schema);
                string error = "";
                if (cfg == null || !cfg.SetChecked(AutoStartKey, newValue, schema, out error))
                {
                    box.Checked = !box.Checked;
                    LogStore.Add("CatHome4", 2, "自启设置失败：" + error + "（总控窗）", "SHELL");
                    MessageBox.Show(this, "自启设置失败：" + error, "CatHome4 控制台", MessageBoxButtons.OK, MessageBoxIcon.Warning);
                    return;
                }
                SyncAutoStart();
                LogStore.Add("CatHome4", 1, "自启设置: " + newValue + "（总控窗）", "SHELL");
            }

            /// <summary>
            /// 停止运行——二次确认后请求宿主退出（破坏性动作：终止全部会话与定时任务，托盘随之退出）。
            /// </summary>
            private void OnStopClicked()
            {
                DialogResult result = MessageBox.Show(
                    this,
                    "确认停止 CatHome4 宿主？\n\n停止后：\n· 全部会话与定时任务终止\n· 托盘图标消失，宿主进程退出\n· 下次启动前前端页面不可访问",
                    "停止运行",
                    MessageBoxButtons.OKCancel,
                    MessageBoxIcon.Warning,
                    MessageBoxDefaultButton.Button2);
                if (result != DialogResult.OK)
                {
                    return;
                }
                if (_powerButton != null)
                {
                    _powerButton.Enabled = false;
                }
                LogStore.Add("CatHome4", 1, "停止运行请求（总控窗）", "SHELL");
                RequestHostExit();
            }

            /// <summary>
            /// 释放窗体资源——停表 + 释放自建字体（窗口线程结束时由线程主体调用）。
            /// </summary>
            /// <param name="disposing">是否托管释放</param>
            protected override void Dispose(bool disposing)
            {
                if (disposing)
                {
                    if (_uptimeTimer != null)
                    {
                        _uptimeTimer.Stop();
                        _uptimeTimer.Dispose();
                        _uptimeTimer = null;
                    }
                    if (_titleFont != null)
                    {
                        _titleFont.Dispose();
                        _titleFont = null;
                    }
                    if (_powerIconNormal != null)
                    {
                        _powerIconNormal.Dispose();
                        _powerIconNormal = null;
                    }
                    if (_powerIconHover != null)
                    {
                        _powerIconHover.Dispose();
                        _powerIconHover = null;
                    }
                }
                base.Dispose(disposing);
            }

            /// <summary>
            /// 勾选状态与配置对齐——每秒随运行时长一起刷新（托盘或其他入口改动后本窗显示跟随；配置是唯一真相）。
            /// </summary>
            private void RefreshAutoStartState()
            {
                if (_autoStartBox == null)
                {
                    return;
                }
                bool on = IsAutoStartOn();
                if (_autoStartBox.Checked != on)
                {
                    _autoStartBox.Checked = on;
                }
            }
            /// <summary>
            /// 电源图标——红底渐变圆 + 白色电源符号（竖棒 + 开口圆弧）+ 外圈渐变描边；悬停态描边加亮加粗。
            /// 纯 GDI+ 绘制、零外部资源；由 PictureBox 承载（避开 Button 的 hover 灰框）。
            /// </summary>
            /// <param name="size">边长（像素）</param>
            /// <param name="hover">是否悬停态（描边加亮加粗）</param>
            /// <returns>位图（调用方负责释放）</returns>
            private static Image CreatePowerIcon(int size, bool hover)
            {
                Bitmap bmp = new Bitmap(size, size);
                using (Graphics g = Graphics.FromImage(bmp))
                {
                    g.SmoothingMode = System.Drawing.Drawing2D.SmoothingMode.AntiAlias;
                    g.Clear(Color.Transparent);
                    float inset = size * 0.05f;
                    RectangleF outer = new RectangleF(inset, inset, size - inset * 2f, size - inset * 2f);
                    // [段1] 外圈渐变描边——悬停态加亮加粗
                    float ringWidth = size * 0.05f;
                    if (hover)
                    {
                        ringWidth = size * 0.07f;
                    }
                    Color ringFrom = Color.FromArgb(255, 240, 110, 100);
                    Color ringTo = Color.FromArgb(255, 140, 20, 20);
                    if (hover)
                    {
                        ringFrom = Color.FromArgb(255, 255, 150, 140);
                        ringTo = Color.FromArgb(255, 190, 40, 35);
                    }
                    using (System.Drawing.Drawing2D.LinearGradientBrush ringBrush = new System.Drawing.Drawing2D.LinearGradientBrush(outer, ringFrom, ringTo, 45f))
                    {
                        using (Pen ringPen = new Pen(ringBrush, ringWidth))
                        {
                            g.DrawEllipse(ringPen, outer);
                        }
                    }
                    // [段2] 底盘——线性渐变红（左上亮 → 右下深）
                    RectangleF faceRect = new RectangleF(outer.X + ringWidth, outer.Y + ringWidth, outer.Width - ringWidth * 2f, outer.Height - ringWidth * 2f);
                    using (System.Drawing.Drawing2D.LinearGradientBrush faceBrush = new System.Drawing.Drawing2D.LinearGradientBrush(faceRect, Color.FromArgb(255, 235, 85, 75), Color.FromArgb(255, 150, 22, 22), 60f))
                    {
                        g.FillEllipse(faceBrush, faceRect);
                    }
                    // [段2] 电源符号——竖棒 + 开口向上的圆弧
                    float center = size / 2f;
                    float radius = size * 0.25f;
                    float stroke = size * 0.105f;
                    using (Pen pen = new Pen(Color.White, stroke))
                    {
                        pen.StartCap = System.Drawing.Drawing2D.LineCap.Round;
                        pen.EndCap = System.Drawing.Drawing2D.LineCap.Round;
                        g.DrawArc(pen, center - radius, center - radius + stroke * 0.4f, radius * 2f, radius * 2f, -60f, 300f);
                        g.DrawLine(pen, center, center - radius * 1.55f, center, center - radius * 0.25f);
                    }
                }
                return bmp;
            }
        }
    }
}
