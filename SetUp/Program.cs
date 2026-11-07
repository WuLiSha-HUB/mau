using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Text;
using System.Text.Json;
using System.Windows.Forms;

namespace SetUp
{
    /// <summary>
    /// SetUp 启动器——CH4/Mau 一键部署工具。
    /// 定位：单文件 exe 跟随 Git 放仓库根，clone 即用；双击即部署即运行。
    /// 规范：Project/CH4/design-ch4-release.md（D2 定稿）
    /// 模式：无参=WinForms UI 外观层（主线程） / prepare（重建全发布链） / deploy &lt;目标目录&gt;（产出正式运行实例）
    ///      / sync-html（外观层静态资源镜像同步——源区 → 产物区 + 运行区） / relaunch（宿主自更新接力）
    /// 前置检测：.NET 10 Runtime + SDK + WindowsDesktop + 同目录存在 Mau.sln 才运行。
    /// </summary>
    public static partial class Program
    {
        /// <summary>
        /// 主入口——模式分发。
        /// </summary>
        /// <param name="args">命令行参数：无参=UI 界面 / prepare / deploy &lt;目标目录&gt;</param>
        /// <returns>退出码（0=成功，非0=失败）</returns>
        [STAThread]
        public static int Main(string[] args)
        {
            // [段1] UI 模式——无参双击进入 WinForms 外观层（主线程 = Application.Run）
            if (args.Length == 0)
            {
                Application.EnableVisualStyles();
                Application.SetCompatibleTextRenderingDefault(false);
                Application.Run(new SetUpForm());
                return 0;
            }

            // [段1b] CLI 模式——尽力附加父控制台（WinExe 无自有控制台；交互终端可见输出）
            // 附加成功后 .NET Console 流不会自动绑定控制台句柄——手动 GetStdHandle 绑定（否则 WriteLine 仍丢失）
            bool attached = AttachConsole(ATTACH_PARENT_PROCESS);
            if (attached)
            {
                IntPtr outHandle = GetStdHandle(STD_OUTPUT_HANDLE);
                if (outHandle != IntPtr.Zero && outHandle != new IntPtr(-1))
                {
                    Console.SetOut(new StreamWriter(new FileStream(new Microsoft.Win32.SafeHandles.SafeFileHandle(outHandle, false), FileAccess.Write), Console.OutputEncoding) { AutoFlush = true });
                }
                IntPtr errHandle = GetStdHandle(STD_ERROR_HANDLE);
                if (errHandle != IntPtr.Zero && errHandle != new IntPtr(-1))
                {
                    Console.SetError(new StreamWriter(new FileStream(new Microsoft.Win32.SafeHandles.SafeFileHandle(errHandle, false), FileAccess.Write), Console.OutputEncoding) { AutoFlush = true });
                }
            }

            // [段1c] 报告参数解析——--report <path>（Agent 确定性部署验证面：同路径 .log 全量日志 + 结束写 JSON 报告）
            string reportPath = "";
            for (int i = 1; i < args.Length; i = i + 1)
            {
                if (args[i] == "--report" && i + 1 < args.Length)
                {
                    reportPath = args[i + 1];
                }
            }
            if (reportPath.Length > 0)
            {
                _reportRequested = true;
                try
                {
                    string logPath = reportPath + ".log";
                    string logDir = Path.GetDirectoryName(logPath);
                    if (logDir != null && logDir.Length > 0)
                    {
                        Directory.CreateDirectory(logDir);
                    }
                    _logFile = new StreamWriter(logPath, false, new UTF8Encoding(false)) { AutoFlush = true };
                    Console.SetOut(new TeeWriter(Console.Out, _logFile));
                    Console.SetError(new TeeWriter(Console.Error, _logFile));
                }
                catch (Exception ex)
                {
                    Console.WriteLine("[SetUp] 警告：日志文件打开失败——" + ex.Message);
                }
            }

            // [段1d] 帮助提示
            if (args[0] == "-h" || args[0] == "--help")
            {
                PrintHelp();
                return 0;
            }

            // [段2] 仓库根检测——当前目录存在 Mau.sln 才继续（SetUp.exe 合法位置 = Mau 仓库根）
            string repoRoot = ResolveRepoRoot(Environment.CurrentDirectory);
            if (repoRoot.Length == 0)
            {
                Console.WriteLine("[SetUp] 错误：当前目录未找到 Mau.sln——SetUp.exe 必须位于 Mau 仓库根目录运行。");
                Pause();
                return 1;
            }
            Console.WriteLine("[SetUp] 仓库根: " + repoRoot);

            // [段3] 环境检测——.NET 10 Runtime + SDK
            if (!CheckEnvironment())
            {
                Console.WriteLine("[SetUp] 环境检测未通过，部署中止。");
                Pause();
                return 1;
            }

            // [段4] 模式分发——统一收尾：flush 日志 + 写报告（确定性部署验证面）
            string mode = args[0];
            int exitCode = 1;
            if (mode == "prepare")
            {
                exitCode = Prepare(repoRoot);
            }
            else if (mode == "deploy")
            {
                if (args.Length < 2)
                {
                    Console.WriteLine("[SetUp] deploy 需要目标目录参数：SetUp.exe deploy <目标目录>");
                    exitCode = 1;
                }
                else
                {
                    exitCode = Deploy(repoRoot, args[1]);
                }
            }
            else if (mode == "sync-html")
            {
                // sync-html —— 外观层静态资源镜像同步（源区 → 产物区 + 运行区；G 方案 2026-09-23）
                exitCode = SyncHtml(repoRoot, args);
            }
            else if (mode == "diag-holders")
            {
                // diag-holders —— 目录持有者诊断（文件句柄 + 进程工作目录两条证据面；运维与验证入口）
                if (args.Length < 2)
                {
                    Console.WriteLine("[SetUp] diag-holders 需要目标目录参数：SetUp.exe diag-holders <目录>");
                    exitCode = 1;
                }
                else
                {
                    Console.WriteLine("[SetUp] 目录持有者诊断: " + args[1]);
                    Console.Write(DiagnoseDirectoryHolders(args[1]));
                    exitCode = 0;
                }
            }
            else if (mode == "relaunch")
            {
                // relaunch —— 宿主自更新接力（等旧宿主退出 → prepare → 原子切换 → 自启；参数零容忍校验在 Relaunch 内）
                exitCode = Relaunch(repoRoot, args);
            }
            else
            {
                Console.WriteLine("[SetUp] 未知模式: " + mode);
                PrintHelp();
                exitCode = 1;
            }
            if (_logFile != null)
            {
                try
                {
                    _logFile.Flush();
                    _logFile.Close();
                    _logFile = null;
                }
                catch (Exception ex)
                {
                    Console.WriteLine("[SetUp] 警告：日志文件关闭失败——" + ex.Message);
                }
            }
            if (reportPath.Length > 0)
            {
                WriteReport(reportPath, mode, exitCode, repoRoot);
            }
            return exitCode;
        }

        /// <summary>
        /// 帮助文本输出。
        /// </summary>
        private static void PrintHelp()
        {
            Console.WriteLine("SetUp —— CH4/Mau 一键部署工具");
            Console.WriteLine("用法:");
            Console.WriteLine("  SetUp.exe            打开图形界面（无参双击）");
            Console.WriteLine("  SetUp.exe prepare    重建全发布链（public/ + Mau-public/）");
            Console.WriteLine("  SetUp.exe deploy <目录>  部署正式运行实例到目标目录（原子切换——现行更名 _old 留作回退源）");
            Console.WriteLine("  SetUp.exe sync-html [--target <运行区目录>] [--no-run] [--report <路径>]");
            Console.WriteLine("                           外观层静态资源镜像同步：源区 html → 产物区 + 运行区（--target 缺省从运行中宿主反推）");
            Console.WriteLine("  SetUp.exe diag-holders <目录>");
            Console.WriteLine("                           目录持有者诊断：逐个文件独占打开探测 + 进程工作目录扫描（回答「谁锁着运行区」）");
            Console.WriteLine("  SetUp.exe relaunch [--mode <full|incr|host>] --wait-pid <pid> --target <目录> [--majordomopush <串>] [--report <路径>]");
            Console.WriteLine("                           宿主自更新接力（--mode 缺省 full）：等旧宿主退出 → 全链 prepare / 纯搬运 / 原地 → 原子切换 → 自启新宿主（回执注入）");
            Console.WriteLine("前置：.NET 10 Runtime + SDK + WindowsDesktop；本 exe 须位于 Mau 仓库根（含 Mau.sln）。");
        }

        /// <summary>
        /// 仓库根探测——从起始目录向上找含 Mau.sln 的目录。
        /// </summary>
        /// <param name="startDir">起始目录</param>
        /// <returns>仓库根或空字符串</returns>
        private static string ResolveRepoRoot(string startDir)
        {
            string dir = new DirectoryInfo(startDir).FullName;
            while (true)
            {
                if (File.Exists(Path.Combine(dir, "Mau.sln")))
                {
                    return dir;
                }
                string parent = Directory.GetParent(dir)?.FullName;
                if (parent == null)
                {
                    return "";
                }
                dir = parent;
            }
        }

        /// <summary>
        /// 环境检测——.NET 10 Runtime（自启前提）+ SDK（部署前置）。
        /// 检测命令：dotnet --list-runtimes / --list-sdks。
        /// </summary>
        /// <returns>true=环境就绪，false=缺 runtime 或 SDK</returns>
        private static bool CheckEnvironment()
        {
            // [段1] dotnet 本体检测——找不到 dotnet = runtime 缺失
            string dotnetOut = RunProcessCapture("dotnet", "--list-runtimes", Environment.CurrentDirectory);
            if (dotnetOut == null)
            {
                Console.WriteLine("[SetUp] 错误：未检测到 dotnet 命令——需要 .NET 10 Runtime。");
                Console.WriteLine("  安装指引: https://dotnet.microsoft.com/download/dotnet/10.0");
                return false;
            }

            // [段2] Runtime 检测——找 Microsoft.NETCore.App 主版本 ≥ 10（部署链目标框架 net10.0——≥10 即通过）
            bool runtime10 = false;
            string[] runtimeLines = dotnetOut.Split('\n');
            for (int i = 0; i < runtimeLines.Length; i = i + 1)
            {
                string line = runtimeLines[i].Trim();
                if (line.StartsWith("Microsoft.NETCore.App", StringComparison.Ordinal))
                {
                    if (VersionAtLeast10(line))
                    {
                        runtime10 = true;
                    }
                }
            }
            if (!runtime10)
            {
                Console.WriteLine("[SetUp] 错误：需要 .NET 10 及以上 Runtime（当前未装）。");
                Console.WriteLine("  安装指引: https://dotnet.microsoft.com/download/dotnet/10.0");
                return false;
            }

            // [段2b] WindowsDesktop 检测——WinForms 外观层前置（UI 模式必需；SDK 安装默认带）
            bool desktop10 = false;
            for (int i = 0; i < runtimeLines.Length; i = i + 1)
            {
                string line = runtimeLines[i].Trim();
                if (line.StartsWith("Microsoft.WindowsDesktop.App", StringComparison.Ordinal))
                {
                    if (VersionAtLeast10(line))
                    {
                        desktop10 = true;
                    }
                }
            }
            if (!desktop10)
            {
                Console.WriteLine("[SetUp] 错误：需要 .NET 10 及以上 WindowsDesktop Runtime（WinForms 外观层）。");
                Console.WriteLine("  安装指引: https://dotnet.microsoft.com/download/dotnet/10.0");
                return false;
            }

            // [段3] SDK 检测——dotnet --list-sdks 找主版本 ≥ 10
            string sdkOut = RunProcessCapture("dotnet", "--list-sdks", Environment.CurrentDirectory);
            bool sdk10 = false;
            if (sdkOut != null)
            {
                string[] sdkLines = sdkOut.Split('\n');
                for (int i = 0; i < sdkLines.Length; i = i + 1)
                {
                    if (VersionAtLeast10(sdkLines[i]))
                    {
                        sdk10 = true;
                    }
                }
            }
            if (!sdk10)
            {
                Console.WriteLine("[SetUp] 错误：需要 .NET 10 及以上 SDK（部署全程 build/test/publish 前置）。");
                Console.WriteLine("  安装指引: https://dotnet.microsoft.com/download/dotnet/10.0");
                return false;
            }

            Console.WriteLine("[SetUp] 环境就绪：.NET 10 Runtime + SDK + WindowsDesktop 已检测到。");
            return true;
        }

        /// <summary>
        /// 部署步骤记录——报告数据源（Prepare/Deploy 填充）
        /// </summary>
        private static readonly List<StepReport> _steps = new List<StepReport>();

        /// <summary>
        /// 日志文件流——--report 时 CLI 输出双写（无控制台环境确定性留痕）
        /// </summary>
        private static StreamWriter _logFile;

        /// <summary>
        /// --report 请求标志——自动化场景（Agent 调用）：Pause 不等待按键、报告必写
        /// </summary>
        private static bool _reportRequested;

        /// <summary>
        /// 部署步骤报告——单步名称 + 成败 + 耗时
        /// </summary>
        public sealed class StepReport
        {
            /// <summary>步骤号</summary>
            public int Step;
            /// <summary>步骤名</summary>
            public string Name = "";
            /// <summary>是否成功</summary>
            public bool Ok;
            /// <summary>耗时毫秒</summary>
            public long Ms;
            /// <summary>步骤细节（可选——文件数 / 字节数 / 差异描述）</summary>
            public string Detail = "";
        }

        /// <summary>
        /// 产物快照——报告 artifacts 条目（时间戳确定性验证）
        /// </summary>
        public sealed class ArtifactReport
        {
            /// <summary>绝对路径</summary>
            public string Path = "";
            /// <summary>字节数</summary>
            public long Size;
            /// <summary>修改时间</summary>
            public string Modified = "";
        }

        /// <summary>
        /// 部署报告文档——ts/mode/ok/steps/artifacts
        /// </summary>
        public sealed class ReportDoc
        {
            /// <summary>时间戳</summary>
            public string Ts = "";
            /// <summary>模式</summary>
            public string Mode = "";
            /// <summary>是否成功</summary>
            public bool Ok;
            /// <summary>退出码</summary>
            public int ExitCode;
            /// <summary>步骤明细</summary>
            public StepReport[] Steps = new StepReport[0];
            /// <summary>关键产物时间戳</summary>
            public ArtifactReport[] Artifacts = new ArtifactReport[0];
            /// <summary>是否完整（A136）——false=自启前排空的前置版（步 11-13），true=收尾终版（含自启 / 回退结论）</summary>
            public bool Complete;
        }

        /// <summary>
        /// 双写输出流——原 stdout（可能 Null）+ 日志文件（--report 场景）
        /// </summary>
        private sealed class TeeWriter : TextWriter
        {
            /// <summary>原 stdout 写入目标（可能为 Null 流）</summary>
            private readonly TextWriter _primary;
            /// <summary>日志文件写入目标（--report 场景同写）</summary>
            private readonly TextWriter _file;

            /// <summary>构造</summary>
            public TeeWriter(TextWriter primary, TextWriter file)
            {
                _primary = primary;
                _file = file;
            }

            /// <summary>编码</summary>
            public override Encoding Encoding
            {
                get { return Encoding.UTF8; }
            }

            /// <summary>写字符——双写</summary>
            public override void Write(char value)
            {
                _primary.Write(value);
                _file.Write(value);
            }

            /// <summary>写字符串——双写</summary>
            public override void Write(string value)
            {
                _primary.Write(value);
                _file.Write(value);
            }

            /// <summary>写行——双写</summary>
            public override void WriteLine(string value)
            {
                _primary.WriteLine(value);
                _file.WriteLine(value);
            }

            /// <summary>冲刷</summary>
            public override void Flush()
            {
                _primary.Flush();
                _file.Flush();
            }
        }

        /// <summary>
        /// 写部署报告——JSON 落盘（steps + artifacts + 成败）；--report 参数启用
        /// </summary>
        /// <param name="reportPath">报告文件路径</param>
        /// <param name="mode">模式（prepare/deploy）</param>
        /// <param name="exitCode">退出码</param>
        /// <param name="repoRoot">仓库根（产物收集基准）</param>
        /// <param name="complete">是否完整（false=自启前排空的前置版——读者任何时刻都能读到文件；true=收尾终版）</param>
        private static void WriteReport(string reportPath, string mode, int exitCode, string repoRoot, bool complete = true)
        {
            try
            {
                ReportDoc doc = new ReportDoc();
                doc.Ts = DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss");
                doc.Mode = mode;
                doc.Ok = exitCode == 0;
                doc.ExitCode = exitCode;
                doc.Complete = complete;
                doc.Steps = _steps.ToArray();
                List<ArtifactReport> artifacts = CollectArtifacts(repoRoot);
                artifacts.AddRange(_extraArtifacts);
                doc.Artifacts = artifacts.ToArray();
                JsonSerializerOptions options = new JsonSerializerOptions();
                options.WriteIndented = true;
                options.IncludeFields = true;   // 报告 DTO 为 public 字段——System.Text.Json 默认只序列化属性，必须显式 IncludeFields
                options.Encoder = System.Text.Encodings.Web.JavaScriptEncoder.UnsafeRelaxedJsonEscaping;   // 中文直出（默认 \uXXXX 转义人读不便）
                                                                                                           // A136——原子替换：先写临时文件再改名（读者不会读到半截 JSON；同卷改名 = 原子）
                string tmpPath = reportPath + ".tmp";
                File.WriteAllText(tmpPath, JsonSerializer.Serialize(doc, options), new UTF8Encoding(false));
                File.Move(tmpPath, reportPath, true);
            }
            catch (Exception ex)
            {
                Console.WriteLine("[SetUp] 警告：报告写入失败——" + ex.Message);
            }
        }

        /// <summary>
        /// 关键产物快照——宿主 exe + 基座 CLI + Flows dll（时间戳/大小确定性验证）
        /// </summary>
        /// <param name="repoRoot">仓库根</param>
        /// <returns>产物列表</returns>
        private static List<ArtifactReport> CollectArtifacts(string repoRoot)
        {
            List<ArtifactReport> list = new List<ArtifactReport>();
            AddArtifact(list, Path.Combine(repoRoot, "public", "app", "CatHome4.exe"));
            AddArtifact(list, Path.Combine(repoRoot, "Mau-public", "Mau.exe"));
            string flowsDir = Path.Combine(repoRoot, "public", "app", "Flows");
            if (Directory.Exists(flowsDir))
            {
                string[] flows = Directory.GetFiles(flowsDir, "*.dll");
                Array.Sort(flows, StringComparer.Ordinal);
                for (int i = 0; i < flows.Length; i = i + 1)
                {
                    AddArtifact(list, flows[i]);
                }
            }
            return list;
        }

        /// <summary>
        /// 单产物快照条目——存在才加入
        /// </summary>
        /// <param name="list">目标列表</param>
        /// <param name="path">产物路径</param>
        private static void AddArtifact(List<ArtifactReport> list, string path)
        {
            if (File.Exists(path))
            {
                FileInfo info = new FileInfo(path);
                list.Add(new ArtifactReport() { Path = path, Size = info.Length, Modified = info.LastWriteTime.ToString("yyyy-MM-dd HH:mm:ss") });
            }
        }

        /// <summary>
        /// 子进程执行——统一 Process.Start 封装（工作目录 + 退出码检查；stdout/stderr 捕获转发 Console——UI 模式显示到日志框）。
        /// </summary>
        /// <param name="fileName">可执行文件</param>
        /// <param name="arguments">参数串</param>
        /// <param name="workingDir">工作目录</param>
        /// <returns>true=退出码 0</returns>
        /// <param name="timeoutMs">超时毫秒（0=无限等待——原行为；&gt;0 超时即按失败处置并终止进程树，不留孤儿）</param>
        private static bool RunProcess(string fileName, string arguments, string workingDir, int timeoutMs = 0)
        {
            Console.WriteLine("[SetUp] >>> " + fileName + " " + arguments);
            ProcessStartInfo psi = new ProcessStartInfo(fileName, arguments);
            psi.WorkingDirectory = workingDir;
            psi.UseShellExecute = false;
            psi.CreateNoWindow = true;
            psi.RedirectStandardOutput = true;
            psi.RedirectStandardError = true;
            psi.StandardOutputEncoding = Encoding.UTF8;
            psi.StandardErrorEncoding = Encoding.UTF8;
            Process proc;
            try
            {
                proc = Process.Start(psi);
            }
            catch (Exception ex)
            {
                Console.WriteLine("[SetUp] 错误：进程启动失败——" + fileName + "：" + ex.Message);
                return false;
            }
            if (proc == null)
            {
                Console.WriteLine("[SetUp] 错误：进程启动失败——" + fileName);
                return false;
            }
            proc.OutputDataReceived += OnProcessOutput;
            proc.ErrorDataReceived += OnProcessOutput;
            proc.BeginOutputReadLine();
            proc.BeginErrorReadLine();
            // A135——可选超时：超时按失败处置并终止进程树（不留孤儿）；未传超时=原行为（无限等待）
            if (timeoutMs > 0)
            {
                if (!proc.WaitForExit(timeoutMs))
                {
                    Console.WriteLine("[SetUp] 错误：子进程超时（" + timeoutMs.ToString() + " ms）——按失败处置并终止进程树：" + fileName);
                    KillProcessTree(proc);
                    return false;
                }
            }
            proc.WaitForExit();
            // 第二次无参 WaitForExit()——等待异步输出流（OutputDataReceived/ErrorDataReceived）结清，非冗余（R7-P3-7 判定）
            proc.WaitForExit();
            if (proc.ExitCode != 0)
            {
                Console.WriteLine("[SetUp] 错误：退出码 " + proc.ExitCode + "——" + fileName + " " + arguments);
                return false;
            }
            return true;
        }
        /// <summary>
        /// 终止进程树（A135）——超时子进程按失败处置时不留孤儿（宿主 --run 自检 / 探活进程可能挂起）。
        /// </summary>
        /// <param name="proc">目标进程</param>
        private static void KillProcessTree(Process proc)
        {
            try
            {
                if (!proc.HasExited)
                {
                    proc.Kill(true);
                }
            }
            catch (Exception ex)
            {
                Console.WriteLine("[SetUp] 警告：超时子进程终止失败——" + ex.Message);
            }
            try
            {
                proc.WaitForExit(5000);
            }
            catch (Exception ex)
            {
                Console.WriteLine("[SetUp] 警告：超时进程收尾等待异常——" + ex.Message);
            }
        }

        /// <summary>
        /// 子进程输出行转发——stdout/stderr 逐行写回 Console（UI 模式经 Console.SetOut 重定向进日志框）。
        /// </summary>
        /// <param name="sender">事件源</param>
        /// <param name="e">输出行</param>
        private static void OnProcessOutput(object sender, DataReceivedEventArgs e)
        {
            if (e.Data != null)
            {
                Console.WriteLine(e.Data);
            }
        }

        /// <summary>
        /// 子进程执行并捕获标准输出——检测命令用（不继承控制台）。
        /// </summary>
        /// <param name="fileName">可执行文件</param>
        /// <param name="arguments">参数串</param>
        /// <param name="workingDir">工作目录</param>
        /// <returns>stdout 全文；启动失败返回 null</returns>
        private static string RunProcessCapture(string fileName, string arguments, string workingDir)
        {
            ProcessStartInfo psi = new ProcessStartInfo(fileName, arguments);
            psi.WorkingDirectory = workingDir;
            psi.UseShellExecute = false;
            psi.CreateNoWindow = true;
            psi.RedirectStandardOutput = true;
            psi.StandardOutputEncoding = Encoding.UTF8;
            Process proc;
            try
            {
                proc = Process.Start(psi);
            }
            catch (Exception ex)
            {
                Console.WriteLine("[SetUp] 警告：进程启动失败——" + ex.Message);
                return null;
            }
            if (proc == null)
            {
                return null;
            }
            string output = proc.StandardOutput.ReadToEnd();
            proc.WaitForExit();
            return output;
        }

        /// <summary>
        /// 版本行解析——提取首段版本号并判断主版本 ≥ 10（部署链目标框架 net10.0）。
        /// 输入示例：Microsoft.NETCore.App 8.0.15 [...] 或 9.0.314 [...]
        /// </summary>
        /// <param name="line">dotnet 输出行</param>
        /// <returns>true=主版本 ≥ 10</returns>
        private static bool VersionAtLeast10(string line)
        {
            // [段1] 找第一个数字开头段（版本号）
            string[] tokens = line.Split(' ');
            for (int i = 0; i < tokens.Length; i = i + 1)
            {
                string token = tokens[i].Trim();
                if (token.Length == 0)
                {
                    continue;
                }
                int major;
                int dot = token.IndexOf('.');
                string majorStr = dot > 0 ? token.Substring(0, dot) : token;
                if (int.TryParse(majorStr, out major))
                {
                    return major >= 10;
                }
            }
            return false;
        }

        /// <summary>
        /// 失败暂停——保留窗口（双击运行场景看得到错误）。
        /// 自动化/无控制台场景不等待——防 shell 无按键输入无限阻塞：
        ///   1) stdout 被重定向（管道捕获） 2) --report 显式自动化模式（Agent 调用） 3) 无控制台窗口（GetConsoleWindow 为空——WinExe 未附加成功）
        /// </summary>
        private static void Pause()
        {
            if (Console.IsOutputRedirected || _reportRequested || GetConsoleWindow() == IntPtr.Zero)
            {
                return;
            }
            Console.WriteLine("按任意键退出…");
            Console.ReadKey(true);
        }
    }
}
