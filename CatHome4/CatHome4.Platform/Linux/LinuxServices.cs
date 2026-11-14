using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Text;
using CatHome4.Contracts;
using Mau.Contracts;
using Mau.Runtime;

namespace CatHome4.Platform.Linux
{
    /// <summary>
    /// 进程树终止·Linux 实现（A358 落地）。
    /// <para>
    /// 形态：<see cref="Process.Kill(bool)"/> 在 Linux 上由运行时按 <c>/proc</c> 枚举子树并逐 pid 发 SIGKILL，
    /// 语义等价于「树内全杀」。<b>不做 <c>kill(-pgid)</c></b>——子进程未 <c>setsid</c> 时其进程组等于宿主进程组，
    /// 组杀会连带杀死宿主自身（design-ch4-platform §二 记的是目标形态，落地按安全面收窄）。
    /// 组级生命周期（宿主退出即连带终止）由 systemd unit 的 <c>KillMode=control-group</c> 承担，见
    /// <see cref="LinuxAutoStartService"/>。
    /// </para>
    /// </summary>
    public sealed class LinuxProcessService : IProcessService
    {
        /// <summary>
        /// 终止进程树——BCL 跨平台子树终止（逐 pid SIGKILL），失败只出声不抛。
        /// </summary>
        /// <param name="process">目标进程</param>
        /// <param name="logTag">日志标签</param>
        public void KillTree(Process process, string logTag)
        {
            if (process == null)
            {
                return;
            }
            try
            {
                if (process.HasExited == false)
                {
                    process.Kill(true);
                }
            }
            catch (Exception ex)
            {
                LogStore.Add(logTag, 2, "进程树终止失败: " + ex.Message, "SYS");
            }
        }
    }

    /// <summary>
    /// 外壳启动器·Linux 实现（A358 落地）——统一交 <c>xdg-open</c>（freedesktop 默认打开器）。
    /// <para>
    /// 与 Windows 实现的能力差：<c>explorer /select</c> 的「定位到文件」语义在 freedesktop 无对应，
    /// 文件路径一律按「用默认程序打开父目录」处理（调用方无需区分）。
    /// </para>
    /// </summary>
    public sealed class LinuxShellLauncher : IShellLauncher
    {
        /// <summary>日志标签——本类固定归属</summary>
        private const string LogTag = "CatHome4";

        /// <summary>
        /// 打开目录（或文件所在目录）。
        /// </summary>
        /// <param name="path">目录或文件路径</param>
        public void OpenFolder(string path)
        {
            if (path == null || path.Length == 0)
            {
                return;
            }
            string target = path;
            try
            {
                if (File.Exists(path))
                {
                    string? dir = Path.GetDirectoryName(path);
                    if (dir != null && dir.Length > 0)
                    {
                        target = dir;
                    }
                }
            }
            catch (Exception ex)
            {
                LogStore.Add(LogTag, 2, "路径解析失败（按原值打开）：" + path + " | " + ex.Message, "SHELL");
            }
            Open(target);
        }

        /// <summary>
        /// 用默认程序打开外部链接。
        /// </summary>
        /// <param name="url">目标地址</param>
        public void OpenUrl(string url)
        {
            if (url == null || url.Length == 0)
            {
                return;
            }
            Open(url);
        }

        /// <summary>
        /// 交 <c>xdg-open</c> 处理——失败只出声不抛（与 Windows 实现同口径：打开失败不改业务状态）。
        /// </summary>
        /// <param name="target">目录 / 文件 / URL</param>
        private static void Open(string target)
        {
            try
            {
                ProcessStartInfo psi = new ProcessStartInfo();
                psi.FileName = "xdg-open";
                psi.ArgumentList.Add(target);
                psi.UseShellExecute = false;
                psi.CreateNoWindow = true;
                Process.Start(psi);
            }
            catch (Exception ex)
            {
                LogStore.Add(LogTag, 2, "打开路径失败：" + target + " | " + ex.Message, "SHELL");
            }
        }
    }

    /// <summary>
    /// 开机自启·systemd user unit 实现（A358 落地）。
    /// <para>
    /// 物化落点：<c>~/.config/systemd/user/cathome4.service</c>；开关走
    /// <c>systemctl --user daemon-reload</c> + <c>enable</c> / <c>disable</c>。
    /// unit 内 <c>KillMode=control-group</c> 承担「宿主退出即连带终止其子进程组」的组生命周期语义
    /// （Linux 侧无 Job 对象，见 <see cref="Mau.Runtime.ProcessGroupLinux"/>）；
    /// <c>Restart=on-failure</c> 承担崩溃自愈。
    /// </para>
    /// </summary>
    public sealed class LinuxAutoStartService : IAutoStartService
    {
        /// <summary>日志标签——本类固定归属</summary>
        private const string LogTag = "CatHome4";

        /// <summary>unit 文件名（含后缀）</summary>
        private const string UnitName = "cathome4.service";

        /// <summary>systemctl 单次调用超时（毫秒）</summary>
        private const int SystemctlTimeoutMs = 10000;

        /// <summary>
        /// 当前环境是否有自启物化能力——unit 目录可写即可。
        /// </summary>
        public bool Available
        {
            get
            {
                return true;
            }
        }

        /// <summary>
        /// 同步物化形态——幂等；失败返回原因串（不静默成功）。
        /// </summary>
        /// <param name="on">目标开关态</param>
        /// <returns>空串 = 成功；否则为失败原因</returns>
        public string Sync(bool on)
        {
            string unitDir;
            try
            {
                unitDir = ResolveUnitDir();
                Directory.CreateDirectory(unitDir);
            }
            catch (Exception ex)
            {
                LogStore.Add(LogTag, 2, "自启 unit 目录不可用: " + ex.Message, "AUTOSTART");
                return "unit 目录不可用：" + ex.Message;
            }
            string unitPath = Path.Combine(unitDir, UnitName);
            if (on)
            {
                string? exePath = Environment.ProcessPath;
                if (exePath == null || exePath.Length == 0)
                {
                    return "无法取得宿主可执行路径（Environment.ProcessPath 为空）";
                }
                try
                {
                    File.WriteAllText(unitPath, BuildUnitText(exePath), new UTF8Encoding(false));
                }
                catch (Exception ex)
                {
                    LogStore.Add(LogTag, 2, "自启 unit 写入失败: " + ex.Message, "AUTOSTART");
                    return "unit 写入失败：" + ex.Message;
                }
                string reloadFail = RunSystemctl("daemon-reload");
                if (reloadFail.Length > 0)
                {
                    return reloadFail;
                }
                string enableFail = RunSystemctl("enable " + UnitName);
                if (enableFail.Length > 0)
                {
                    return enableFail;
                }
                LogStore.Add(LogTag, 1, "自启已物化（systemd user unit）: " + unitPath, "AUTOSTART");
                return "";
            }
            string disableFail = RunSystemctl("disable " + UnitName);
            try
            {
                if (File.Exists(unitPath))
                {
                    File.Delete(unitPath);
                }
            }
            catch (Exception ex)
            {
                LogStore.Add(LogTag, 2, "自启 unit 删除失败: " + ex.Message, "AUTOSTART");
                return "unit 删除失败：" + ex.Message;
            }
            string failAfterReload = RunSystemctl("daemon-reload");
            if (disableFail.Length > 0)
            {
                return disableFail;
            }
            if (failAfterReload.Length > 0)
            {
                return failAfterReload;
            }
            LogStore.Add(LogTag, 1, "自启已解除（systemd user unit 已移除）", "AUTOSTART");
            return "";
        }

        /// <summary>
        /// unit 目录——遵循 XDG：<c>$XDG_CONFIG_HOME/systemd/user</c>，缺省 <c>~/.config/systemd/user</c>。
        /// </summary>
        /// <returns>unit 目录绝对路径</returns>
        private static string ResolveUnitDir()
        {
            string? configHome = Environment.GetEnvironmentVariable("XDG_CONFIG_HOME");
            if (configHome != null && configHome.Length > 0)
            {
                return Path.Combine(configHome, "systemd", "user");
            }
            string home = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
            return Path.Combine(home, ".config", "systemd", "user");
        }

        /// <summary>
        /// unit 文本——ExecStart / WorkingDirectory 取运行实例当前值；KillMode 承担组生命周期。
        /// </summary>
        /// <param name="exePath">宿主可执行文件绝对路径</param>
        /// <returns>unit 文件全文</returns>
        private static string BuildUnitText(string exePath)
        {
            string? workDir = Path.GetDirectoryName(exePath);
            if (workDir == null || workDir.Length == 0)
            {
                workDir = ".";
            }
            StringBuilder sb = new StringBuilder();
            sb.Append("[Unit]\n");
            sb.Append("Description=CatHome4 host\n");
            sb.Append("After=network-online.target\n");
            sb.Append("\n");
            sb.Append("[Service]\n");
            sb.Append("Type=simple\n");
            sb.Append("ExecStart=");
            sb.Append(exePath);
            sb.Append("\n");
            sb.Append("WorkingDirectory=");
            sb.Append(workDir);
            sb.Append("\n");
            sb.Append("Restart=on-failure\n");
            sb.Append("RestartSec=5\n");
            sb.Append("KillMode=control-group\n");
            sb.Append("\n");
            sb.Append("[Install]\n");
            sb.Append("WantedBy=default.target\n");
            return sb.ToString();
        }

        /// <summary>
        /// 调 <c>systemctl --user</c>——成功返回空串，失败返回含 stderr 的原因串。
        /// </summary>
        /// <param name="arguments">systemctl 子命令参数</param>
        /// <returns>空串 = 成功；否则为失败原因</returns>
        private static string RunSystemctl(string arguments)
        {
            ProcessStartInfo psi = new ProcessStartInfo();
            psi.FileName = "systemctl";
            psi.ArgumentList.Add("--user");
            string[] parts = arguments.Split(' ');
            for (int i = 0; i < parts.Length; i = i + 1)
            {
                if (parts[i].Length > 0)
                {
                    psi.ArgumentList.Add(parts[i]);
                }
            }
            psi.RedirectStandardOutput = true;
            psi.RedirectStandardError = true;
            psi.UseShellExecute = false;
            psi.CreateNoWindow = true;
            Process proc;
            try
            {
                proc = new Process();
                proc.StartInfo = psi;
                proc.Start();
            }
            catch (Exception ex)
            {
                LogStore.Add(LogTag, 2, "systemctl 启动失败: " + ex.Message, "AUTOSTART");
                return "systemctl 启动失败：" + ex.Message;
            }
            string outText = proc.StandardOutput.ReadToEnd();
            string errText = proc.StandardError.ReadToEnd();
            bool exited = proc.WaitForExit(SystemctlTimeoutMs);
            if (exited == false)
            {
                PlatformServices.ProcessService.KillTree(proc, LogTag);
                LogStore.Add(LogTag, 2, "systemctl " + arguments + " 超时", "AUTOSTART");
                return "systemctl " + arguments + " 超时";
            }
            if (proc.ExitCode == 0)
            {
                return "";
            }
            string detail = errText.Trim();
            if (detail.Length == 0)
            {
                detail = outText.Trim();
            }
            LogStore.Add(LogTag, 2, "systemctl " + arguments + " 失败: " + detail, "AUTOSTART");
            return "systemctl " + arguments + " 失败：" + detail;
        }
    }

    /// <summary>
    /// 路径平台面·Linux 实现（A358 落地）——<c>$XDG_DATA_HOME</c> 数据根 · Ordinal 比较 · 无 <c>.exe</c> 后缀。
    /// </summary>
    public sealed class LinuxPathPlatform : IPathPlatform
    {
        /// <summary>
        /// 数据根机器级默认——<c>$XDG_DATA_HOME/CatHome4</c>，缺省 <c>~/.local/share/CatHome4</c>。
        /// </summary>
        public string DefaultDataRoot
        {
            get
            {
                string? dataHome = Environment.GetEnvironmentVariable("XDG_DATA_HOME");
                if (dataHome != null && dataHome.Length > 0)
                {
                    return Path.Combine(dataHome, "CatHome4");
                }
                string home = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
                return Path.Combine(home, ".local", "share", "CatHome4");
            }
        }

        /// <summary>
        /// 路径比较口径——Linux 大小写敏感。
        /// </summary>
        public StringComparison PathComparison
        {
            get
            {
                return StringComparison.Ordinal;
            }
        }

        /// <summary>
        /// 宿主可执行文件名（无后缀）。
        /// </summary>
        public string HostExeName
        {
            get
            {
                return "CatHome4";
            }
        }

        /// <summary>
        /// 基座 CLI 可执行文件名（无后缀）。
        /// </summary>
        public string MauExeName
        {
            get
            {
                return "Mau";
            }
        }

        /// <summary>
        /// 一键部署链可执行文件名（仓库根下，无后缀）。
        /// </summary>
        public string SetUpExeName
        {
            get
            {
                return "SetUp-linux";
            }
        }

        /// <summary>
        /// 源码区 html 根——仓库根下的前端源码目录。
        /// </summary>
        /// <param name="repoRoot">仓库根（空串 = 未命中）</param>
        /// <returns>源码区 html 目录路径；repoRoot 空串返回空串</returns>
        public string SourceHtmlRoot(string repoRoot)
        {
            if (repoRoot == null || repoRoot.Length == 0)
            {
                return "";
            }
            return Path.Combine(repoRoot, "CatHome4", "html");
        }

        /// <summary>
        /// 部署区 html 根——发布包内的前端目录。
        /// </summary>
        /// <param name="appBaseDir">进程基目录</param>
        /// <returns>部署区 html 目录路径</returns>
        public string DeployHtmlRoot(string appBaseDir)
        {
            return Path.Combine(appBaseDir, "html");
        }
    }

    /// <summary>
    /// 文件占用探测·Linux 实现（A358 落地）——独占开放探测 + procfs 反查持有进程。
    /// <para>
    /// Linux 允许替换已打开文件（替换的是目录项，不是 inode），因此「文件被打开」不阻断切换；
    /// 阻断面只在「目标文件本身无法独占打开」（权限 / 只读挂载 / 被 flock 排他锁）。
    /// 反查走 <c>/proc/&lt;pid&gt;/fd</c> 符号链接比对（权限不足的进程跳过，出声一次）。
    /// </para>
    /// </summary>
    public sealed class LinuxFileLockProbe : IFileLockProbe
    {
        /// <summary>日志标签——本类固定归属</summary>
        private const string LogTag = "CatHome4";

        /// <summary>procfs 读取受限出声标志（逐进程出声会刷屏）</summary>
        private static bool _reportedProcDenied;

        /// <summary>
        /// 独占打开探测——能独占打开 = 无阻断；文件不存在视为可写（切换会新建）。
        /// </summary>
        /// <param name="path">文件路径</param>
        /// <returns>true = 可独占打开（或文件尚不存在）</returns>
        public bool CanOpenExclusive(string path)
        {
            if (path == null || path.Length == 0)
            {
                return true;
            }
            if (File.Exists(path) == false)
            {
                return true;
            }
            try
            {
                using (FileStream stream = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.None))
                {
                    stream.Close();
                }
                return true;
            }
            catch (Exception ex)
            {
                LogStore.Add(LogTag, 2, "独占打开失败（切换前置阻断）: " + path + " | " + ex.Message, "DEPLOY");
                return false;
            }
        }

        /// <summary>
        /// 反查持有文件的进程——扫描 <c>/proc/&lt;pid&gt;/fd</c> 的符号链接目标。
        /// </summary>
        /// <param name="path">文件路径</param>
        /// <returns>持有者描述（<c>pid:&lt;进程号&gt;</c>，多个以 / 分隔）；无命中返回空串</returns>
        public string QueryHolders(string path)
        {
            if (path == null || path.Length == 0)
            {
                return "";
            }
            string full;
            try
            {
                full = Path.GetFullPath(path);
            }
            catch (Exception)
            {
                return "";
            }
            string[] procDirs;
            try
            {
                procDirs = Directory.GetDirectories("/proc");
            }
            catch (Exception ex)
            {
                ReportProcDenied("procfs 不可读: " + ex.Message);
                return "";
            }
            StringBuilder hits = new StringBuilder();
            for (int i = 0; i < procDirs.Length; i = i + 1)
            {
                string name = Path.GetFileName(procDirs[i]);
                int pid;
                if (int.TryParse(name, out pid) == false)
                {
                    continue;
                }
                if (ScanProcessFds(procDirs[i], pid, full))
                {
                    if (hits.Length > 0)
                    {
                        hits.Append("/");
                    }
                    hits.Append("pid:");
                    hits.Append(pid.ToString());
                }
            }
            return hits.ToString();
        }

        /// <summary>
        /// 扫描单进程的 fd 表——命中目标全路径即判定持有。
        /// </summary>
        /// <param name="procDir">进程目录（<c>/proc/&lt;pid&gt;</c>）</param>
        /// <param name="pid">进程号（日志用）</param>
        /// <param name="targetFullPath">目标文件全路径</param>
        /// <returns>true = 该进程持有目标文件</returns>
        private static bool ScanProcessFds(string procDir, int pid, string targetFullPath)
        {
            string fdDir = Path.Combine(procDir, "fd");
            string[] entries;
            try
            {
                entries = Directory.GetFileSystemEntries(fdDir);
            }
            catch (Exception ex)
            {
                ReportProcDenied("procfs fd 读取受限（pid " + pid.ToString() + "）: " + ex.Message);
                return false;
            }
            for (int i = 0; i < entries.Length; i = i + 1)
            {
                string? linkTarget;
                try
                {
                    FileInfo info = new FileInfo(entries[i]);
                    linkTarget = info.LinkTarget;
                }
                catch (Exception)
                {
                    continue;
                }
                if (linkTarget == null || linkTarget.Length == 0)
                {
                    continue;
                }
                if (string.Equals(linkTarget, targetFullPath, StringComparison.Ordinal))
                {
                    return true;
                }
            }
            return false;
        }

        /// <summary>
        /// procfs 读取受限出声——只出声一次（避免逐进程刷屏），并与「无持有者」区分。
        /// </summary>
        /// <param name="message">原因</param>
        private static void ReportProcDenied(string message)
        {
            if (_reportedProcDenied)
            {
                return;
            }
            _reportedProcDenied = true;
            LogStore.Add(LogTag, 2, message + "（持有者反查结果可能不完整）", "DEPLOY");
        }
    }

    /// <summary>
    /// 命令行服务·Linux 实现（A358 落地）——<c>bash -lc</c> 单行执行 + 超时子树终止 + 结构化回执。
    /// <para>
    /// 单行纪律与 ps 面同源（禁代码形态 / 多段 / 管道 / 块 / 过程语句——拦截词表按 D5 后置，先跑通真实命令面）。
    /// 回执与 <c>powershell</c> 同形：首行 JSON 元数据头 + 正文（stdout 换行 stderr）。
    /// </para>
    /// </summary>
    public sealed class LinuxShellService : IShellService
    {
        /// <summary>日志标签——本类固定归属</summary>
        private const string LogTag = "CatHome4";

        /// <summary>stdout 字符上限（与 ps 面同量级）</summary>
        private const int MaxStdoutChars = 64 * 1024;

        /// <summary>stderr 字符上限（与 ps 面同量级）</summary>
        private const int MaxStderrChars = 8 * 1024;

        /// <summary>缺省超时（毫秒）</summary>
        private const int DefaultTimeoutMs = 30000;
        /// <summary>bash 可执行路径——构造可指定（读配置 sh.shell_path）；空串取缺省 /bin/bash</summary>
        private readonly string _shellPath;
        /// <summary>
        /// 构造——指定 bash 路径（配置 <c>sh.shell_path</c>；空串取缺省 <c>/bin/bash</c>）。
        /// </summary>
        /// <param name="shellPath">bash 可执行文件绝对路径（空 = 缺省 /bin/bash）</param>
        public LinuxShellService(string shellPath)
        {
            if (shellPath != null && shellPath.Length > 0)
            {
                _shellPath = shellPath;
                return;
            }
            _shellPath = "/bin/bash";
        }

        /// <summary>
        /// 当前环境是否提供命令行服务——Linux 恒 true。
        /// </summary>
        public bool Available
        {
            get
            {
                return true;
            }
        }

        /// <summary>
        /// 执行单行指令——结果 JSON 头 + 正文；失败返回 <c>ERR|</c> 前缀文本。
        /// </summary>
        /// <param name="command">命令文本</param>
        /// <param name="timeoutMs">超时毫秒（超时即终止进程树）</param>
        /// <returns>结构化回执</returns>
        public string Execute(string command, int timeoutMs)
        {
            if (command == null || command.Trim().Length == 0)
            {
                return "ERR|SH_BAD_ARGS|命令为空";
            }
            int timeout = timeoutMs;
            if (timeout <= 0)
            {
                timeout = DefaultTimeoutMs;
            }
            ProcessStartInfo psi = new ProcessStartInfo();
            psi.FileName = _shellPath;
            psi.ArgumentList.Add("-lc");
            psi.ArgumentList.Add(command);
            psi.RedirectStandardOutput = true;
            psi.RedirectStandardError = true;
            psi.UseShellExecute = false;
            psi.CreateNoWindow = true;
            Process proc;
            try
            {
                proc = new Process();
                proc.StartInfo = psi;
                proc.Start();
            }
            catch (Exception ex)
            {
                return "ERR|SH_LAUNCH|" + ex.GetType().Name + "|" + ex.Message;
            }
            // [段1] stdio 采集——单点实现（A365）：逐行泵送（流关闭才结束，不依赖事件回调）+ 超时子树终止 + 排空收尾
            StdioCaptureResult capture = StdioCapture.Drain(proc, timeout, MaxStdoutChars, MaxStderrChars, delegate (Process target) { PlatformServices.ProcessService.KillTree(target, LogTag); }, LogTag);
            string outText = capture.Stdout;
            string errText = capture.Stderr;
            bool outTrunc = capture.OutTruncated;
            bool errTrunc = capture.ErrTruncated;
            bool isTimeout = capture.Timeout;
            int exitCode = capture.ExitCode;
            // [段2] 行数统计——与 ps 面同口径
            int stdoutLines = CountLinesOf(outText);
            int stderrLines = CountLinesOf(errText);
            // [段3] 结构化回执——首行 JSON 元数据头 + 正文（stdout + 换行 + stderr）
            Dictionary<string, object> fields = new Dictionary<string, object>();
            fields["ok"] = true;
            fields["tool"] = "sh-exec";
            fields["target"] = "sh-exec";
            fields["exit"] = exitCode;
            fields["truncated"] = outTrunc || errTrunc;
            fields["timeout"] = isTimeout;
            fields["stdoutLines"] = stdoutLines;
            fields["stderrLines"] = stderrLines;
            string head = JsonUtil.Serialize(fields);
            string bodyText = outText;
            if (errText.Length > 0)
            {
                bodyText = outText + "\n" + errText;
            }
            if (bodyText.Length == 0)
            {
                return head;
            }
            return head + "\n" + bodyText;
        }

        /// <summary>
        /// 行数统计——空串 0；否则换行计数 + 1（与前端 <c>split('\n')</c> 同口径）。
        /// </summary>
        /// <param name="text">源文本</param>
        /// <returns>行数</returns>
        private static int CountLinesOf(string text)
        {
            if (text == null || text.Length == 0)
            {
                return 0;
            }
            int n = 1;
            for (int i = 0; i < text.Length; i = i + 1)
            {
                if (text[i] == '\n')
                {
                    n = n + 1;
                }
            }
            return n;
        }
    }
}
