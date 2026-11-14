using System;
using System.Drawing;
using System.Windows.Forms;
using CatHome4.Contracts;
using CatHome4.Http;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// 总控窗——七项内容：猫头标题 / 版本 / 构建时刻 / 运行时长 / 端点 / 开机自启 / 停止运行。
    /// 关闭即结束本窗线程（Application.Run 返回）；数据面只用进程级只读值（版本 / 端口）与配置读面（app.autostart）——
    /// 不读会话注册表与 Flow 句柄表（主线程独占面）。P2 拆分：自 Program 内嵌类提为独立类（design-ch4-platform §一）。
    /// </summary>
    internal sealed class ShellForm : Form
    {
        /// <summary>说明列左边距（96 DPI 基准——AutoScaleMode=Dpi 随系统缩放）</summary>
        private const int LeftMargin = 18;

        /// <summary>值列起点（96 DPI 基准）</summary>
        private const int ValueX = 100;

        /// <summary>所属外壳——取启动时刻 / 自启读写 / 图标 / 退出请求</summary>
        private readonly WinHostShell _shell;

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
        /// <param name="shell">所属外壳（非空）</param>
        public ShellForm(WinHostShell shell)
        {
            _shell = shell;
            // [段1] 窗体属性——固定尺寸、不可缩放、居中
            Text = "CatHome4 控制台";
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
            Controls.Add(BuildLabel("版本", LeftMargin, 62, Font));
            Controls.Add(BuildLabel(HostAccess.View.VersionInfo, ValueX, 62, Font));
            Controls.Add(BuildLabel("构建于", LeftMargin, 88, Font));
            Controls.Add(BuildLabel(HostAccess.View.BuildTime, ValueX, 88, Font));
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
                degradeLabel.Click += delegate (object sender, EventArgs e) { _shell.OpenFolder(HostAccess.View.StartupReportPath); };
                Controls.Add(degradeLabel);
            }
            _autoStartBox = new CheckBox();
            _autoStartBox.Text = "开机自启";
            _autoStartBox.AutoSize = true;
            _autoStartBox.Location = new Point(LeftMargin, 210);
            _autoStartBox.Font = Font;
            _autoStartBox.Checked = _shell.IsAutoStartOn();
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
            long elapsedMs = Environment.TickCount64 - _shell.StartTicks;
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
                link.LinkClicked += delegate (object sender, LinkLabelLinkClickedEventArgs e) { _shell.OpenExternalUrl(url); };
            }
            return link;
        }

        /// <summary>
        /// 解析管理面板地址——未监听返回空串。
        /// </summary>
        /// <returns>地址或空串</returns>
        private static string ResolvePanelUrl()
        {
            int port = HostAccess.View.PanelPort;
            if (port <= 0)
            {
                return "";
            }
            return "http://127.0.0.1:" + port.ToString();
        }

        /// <summary>
        /// 解析 majordomo 对话页地址——未监听返回空串。
        /// </summary>
        /// <returns>地址或空串</returns>
        private static string ResolveMajorUrl()
        {
            int port = HostAccess.View.MajorPort;
            if (port <= 0)
            {
                return "";
            }
            return "http://127.0.0.1:" + port.ToString();
        }

        /// <summary>
        /// 窗体图标——与托盘 / exe 同源（自 exe 嵌入图标提取；失败静默回退默认）。
        /// </summary>
        private void ApplyFormIcon()
        {
            try
            {
                Icon = _shell.LoadAppIcon();
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
            string error = _shell.ApplyAutoStart(box.Checked);
            if (error.Length > 0)
            {
                box.Checked = !box.Checked;
                LogStore.Add("CatHome4", 2, "自启设置失败：" + error + "（总控窗）", "SHELL");
                MessageBox.Show(this, "自启设置失败：" + error, "CatHome4 控制台", MessageBoxButtons.OK, MessageBoxIcon.Warning);
                return;
            }
            LogStore.Add("CatHome4", 1, "自启设置: " + (box.Checked ? "true" : "false") + "（总控窗）", "SHELL");
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
            _shell.RequestExit();
        }

        /// <summary>
        /// 释放窗体资源——停表 + 释放自建字体与自绘位图（窗口线程结束时由线程主体调用）。
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
            bool on = _shell.IsAutoStartOn();
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
                // [段3] 电源符号——竖棒 + 开口向上的圆弧
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
