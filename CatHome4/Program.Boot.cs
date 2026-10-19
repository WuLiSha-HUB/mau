using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Text;
using System.Threading;
using System.Windows.Forms;
using CatHome4.Contracts;
using CatHome4.Core;
using CatHome4.Http;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// Program 启动韧性分部（A260）——降级登记解析 / 启动报告落盘 / 启动健康快照 / 独立提示窗。
    /// 规格：Project/CH4/design-ch4-host-bootstrap-resilience.md。
    /// 边界：本分部不承担任何业务逻辑，只把「启动期降级事实」翻译成四种可见面（日志 / 快照 / 报告 / 弹窗）。
    /// </summary>
    public static partial class Program
    {
        // [段1] 启动报告
        /// <summary>
        /// 最近一次启动报告的实际路径——空=尚未落盘或全部落点失败（快照 / 弹窗据此显示）。
        /// </summary>
        internal static string StartupReportPath { get; private set; } = "";

        // [段2] 降级事实登记
        /// <summary>
        /// 登记配置目录不可写（内存态运行）——三个配置池构造后统一调用。
        /// </summary>
        /// <param name="writable">目录是否可写</param>
        /// <param name="error">不可写原因</param>
        /// <param name="ability">受影响能力</param>
        internal static void ReportStorageDegrade(bool writable, string error, string ability)
        {
            if (writable)
            {
                return;
            }
            StartupDegradation.Report("CFG_READONLY", StartupDegradation.KindEnv, ability,
                StartupDegradation.StateReadonly, "配置目录不可写：" + error,
                "检查数据根权限或改用可写位置（CH4_DATA_ROOT）", "S1");
        }

        /// <summary>
        /// 解析并登记一条库侧降级事实——格式 `code|state|detail`（WorkspaceConfig.LoadDegrades / ConfigSchema.LoadDegrade 同构）。
        /// </summary>
        /// <param name="line">库侧事实串</param>
        /// <param name="ability">受影响能力</param>
        /// <param name="fix">修复指引</param>
        /// <param name="stage">启动段名</param>
        internal static void ReportLoadDegrade(string line, string ability, string fix, string stage)
        {
            if (line == null || line.Length == 0)
            {
                return;
            }
            int p1 = line.IndexOf('|');
            int p2 = -1;
            if (p1 >= 0)
            {
                p2 = line.IndexOf('|', p1 + 1);
            }
            string code = "BOOT_DEGRADE";
            string state = StartupDegradation.StateFallback;
            string detail = line;
            if (p1 >= 1)
            {
                code = line.Substring(0, p1);
            }
            if (p2 > p1)
            {
                state = line.Substring(p1 + 1, p2 - p1 - 1);
                detail = line.Substring(p2 + 1);
            }
            else if (p1 >= 1)
            {
                detail = line.Substring(p1 + 1);
            }
            string kind = StartupDegradation.KindConfig;
            if (code.StartsWith("ROOT_", StringComparison.Ordinal)
                || string.Equals(code, "CFG_UNREADABLE", StringComparison.Ordinal))
            {
                kind = StartupDegradation.KindEnv;
            }
            StartupDegradation.Report(code, kind, ability, state, detail, fix, stage);
            BootLog.Add(stage, code + " | " + detail);
        }

        // [段3] 启动健康快照
        /// <summary>
        /// 构建快照 boot 段——启动健康面的统一数据源（HTTP 快照 / 面板页签 / 提示窗 / 报告共用）。
        /// </summary>
        /// <returns>boot 段 JSON 对象文本</returns>
        internal static string BuildBootJson()
        {
            StartupDegradeEntry[] entries = StartupDegradation.Snapshot();
            List<string> items = new List<string>();
            for (int i = 0; i < entries.Length; i = i + 1)
            {
                StartupDegradeEntry entry = entries[i];
                items.Add(JsonUtil.Object(
                    ("code", entry.Code),
                    ("kind", entry.Kind),
                    ("ability", entry.Ability),
                    ("state", entry.State),
                    ("detail", entry.Detail),
                    ("fix", entry.Fix),
                    ("stage", entry.Stage),
                    ("at", entry.At),
                    ("count", entry.Count)));
            }
            return JsonUtil.Object(
                ("ok", entries.Length == 0),
                ("count", entries.Length),
                ("summary", StartupDegradation.BuildSummaryLine()),
                ("sink", BootLog.SinkDir),
                ("sinkMode", BootLog.SinkMode),
                ("reportPath", StartupReportPath),
                ("degraded", JsonUtil.RawArray(items.ToArray())));
        }

        // [段4] 启动报告落盘
        /// <summary>
        /// 落盘启动报告——最近一次启动的完整降级清单（覆盖写）。
        /// 落点：数据根 Data/runtime/startup_report.json；不可写时回落早期 sink 目录。
        /// </summary>
        /// <param name="mode">启动形态（interactive / run / script / selfcheck / tool-check / probe）</param>
        internal static void WriteStartupReport(string mode)
        {
            try
            {
                string dataRoot = ResolveDataRoot();
                string path = Path.Combine(dataRoot, "Data", "runtime", "startup_report.json");
                // [段1] 确定落点——数据根 runtime 优先；不可写时回落早期 sink 目录
                bool pathOk = false;
                try
                {
                    Directory.CreateDirectory(Path.GetDirectoryName(path));
                    pathOk = true;
                }
                catch (Exception)
                {
                    pathOk = false;
                }
                if (!pathOk)
                {
                    if (BootLog.SinkDir.Length > 0)
                    {
                        path = Path.Combine(BootLog.SinkDir, "startup_report.json");
                        pathOk = true;
                    }
                }
                if (!pathOk)
                {
                    StartupReportPath = "";
                    BootLog.Add("S7", "启动报告无法落盘（数据根与 sink 均不可写）");
                    return;
                }
                // [段2] 先置路径再构造内容——boot 段内嵌的 reportPath 与实际落点一致
                StartupReportPath = path;
                string json = JsonUtil.Object(
                    ("ts", DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss")),
                    ("version", typeof(Program).Assembly.GetName().Version.ToString()),
                    ("mode", mode),
                    ("boot", JsonUtil.Raw(BuildBootJson())));
                File.WriteAllText(path, json, new UTF8Encoding(false));
            }
            catch (Exception ex)
            {
                StartupReportPath = "";
                BootLog.Add("S7", "启动报告写入失败：" + ex.Message);
            }
        }

        // [段5] 启动提示窗
        /// <summary>
        /// 启动提示窗——独立非模态窗口（独立 STA 线程 + 自有消息循环，不占用主循环）。
        /// 仅在存在降级条目时弹出；关窗不改变任何运行态。
        /// </summary>
        internal static void ShowStartupDegradeWindow()
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
        private static void StartupDegradeThreadProc()
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
                box.Font = new System.Drawing.Font("Microsoft YaHei UI", 9F);
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
                if (StartupReportPath.Length > 0)
                {
                    sb.AppendLine("完整报告：" + StartupReportPath);
                }
                box.Text = sb.ToString();

                FlowLayoutPanel bar = new FlowLayoutPanel();
                bar.Dock = DockStyle.Bottom;
                bar.Height = 46;
                bar.FlowDirection = FlowDirection.RightToLeft;

                Button btnOk = new Button();
                btnOk.Text = "知道了";
                btnOk.Width = 96;
                btnOk.Height = 30;
                btnOk.Margin = new Padding(6);
                btnOk.Click += delegate (object sender, EventArgs e) { form.Close(); };

                Button btnReport = new Button();
                btnReport.Text = "查看启动报告";
                btnReport.Width = 120;
                btnReport.Height = 30;
                btnReport.Margin = new Padding(6);
                btnReport.Click += delegate (object sender, EventArgs e) { OpenFolder(StartupReportPath); };

                Button btnConfig = new Button();
                btnConfig.Text = "打开配置目录";
                btnConfig.Width = 120;
                btnConfig.Height = 30;
                btnConfig.Margin = new Padding(6);
                btnConfig.Click += delegate (object sender, EventArgs e)
                {
                    OpenFolder(Path.Combine(ResolveDataRoot(), "Data", "config"));
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

        // [段6] 主端口回退
        /// <summary>
        /// 启动主 HTTP 宿主——首选端口被占时按端口段顺序回退；段内全占则降级为「无外观层」（返回 null，宿主继续运行）。
        /// </summary>
        /// <param name="options">宿主参数（Port 由本方法覆写）</param>
        /// <param name="preferredPort">首选端口（配置 / 端口段主端口）</param>
        /// <param name="actualPort">实际绑定端口（失败为 0）</param>
        /// <returns>宿主实例（null=端口段耗尽）</returns>
        internal static HttpHost StartMainHostWithFallback(HttpHostOptions options, int preferredPort, out int actualPort)
        {
            actualPort = 0;
            List<int> candidates = new List<int>();
            candidates.Add(preferredPort);
            int step = 1;
            int from = preferredPort;
            int to = preferredPort;
            if (_portBand != null)
            {
                step = _portBand.FamilyStep();
                from = _portBand.FamilyFrom;
                to = _portBand.FamilyTo;
            }
            int port = from;
            while (true)
            {
                if (port != preferredPort)
                {
                    candidates.Add(port);
                }
                if (port == to)
                {
                    break;
                }
                port = port + step;
                if (candidates.Count > 300)
                {
                    break;
                }
            }
            string lastError = "";
            for (int i = 0; i < candidates.Count; i = i + 1)
            {
                options.Port = candidates[i];
                try
                {
                    HttpHost host = HttpHost.Start(options);
                    actualPort = candidates[i];
                    if (candidates[i] != preferredPort)
                    {
                        StartupDegradation.Report("PORT_FALLBACK", StartupDegradation.KindEnv, "外观层（主端口）",
                            StartupDegradation.StateFallback,
                            "端口 " + preferredPort + " 不可用，已改用 " + candidates[i] + "（" + lastError + "）",
                            "关闭占用端口的进程后重启即可回到首选端口", "S6");
                    }
                    return host;
                }
                catch (Exception ex)
                {
                    lastError = ex.Message;
                }
            }
            StartupDegradation.Report("PORT_EXHAUSTED", StartupDegradation.KindEnv, "外观层（主端口）",
                StartupDegradation.StateUnavailable,
                "端口段 " + from.ToString() + ".." + to.ToString() + " 全部不可用：" + lastError,
                "关闭占用端口的进程后重启", "S6");
            return null;
        }

        /// <summary>
        /// 打开目录或定位文件——交资源管理器（失败记 L2，不影响主流程）。
        /// </summary>
        /// <param name="path">目录或文件路径</param>
        private static void OpenFolder(string path)
        {
            if (path == null || path.Length == 0)
            {
                return;
            }
            try
            {
                if (Directory.Exists(path))
                {
                    Process.Start("explorer.exe", path);
                    return;
                }
                if (File.Exists(path))
                {
                    Process.Start("explorer.exe", "/select,\"" + path + "\"");
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "打开路径失败：" + path + " | " + ex.Message, "SHELL");
            }
        }
    }
}
