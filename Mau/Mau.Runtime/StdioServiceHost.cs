using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Runtime.InteropServices;
using System.Text;
using System.Text.Json;
using System.Threading;

namespace Mau.Runtime
{
    /// <summary>
    /// 通用 stdio 常驻服务宿主——进程持有 + 一行 JSON 往返协议 + 生命周期 + 失败面。
    /// <para>
    /// 机制归本类；语义归各薄壳（<see cref="PyService"/> / <see cref="NodeService"/>）：
    /// 解释器来源 · 启动参数模板 · 配置段名 · 锚前缀 · 错误码前缀 · 日志标签均由调用方注入。
    /// </para>
    /// <para>
    /// stdout 只收协议行；stderr 归日志（落 LogStore，不进协议面）。
    /// 超时报错<b>不杀进程</b>（服务端仍在跑，残留回执清空后下次调用重来）。
    /// </para>
    /// </summary>
    internal sealed class StdioServiceHost
    {
        /// <summary>
        /// 服务名
        /// </summary>
        private readonly string _name;

        /// <summary>
        /// 解释器全路径
        /// </summary>
        private readonly string _exe;

        /// <summary>
        /// 入口脚本全路径
        /// </summary>
        private readonly string _script;

        /// <summary>
        /// 工作目录
        /// </summary>
        private readonly string _workDir;

        /// <summary>
        /// 单次调用超时毫秒
        /// </summary>
        private readonly int _timeoutMs;

        /// <summary>
        /// 启动参数（命令行附加串——如 --home &lt;dir&gt;；空=无）
        /// </summary>
        private readonly string _args;

        /// <summary>
        /// 环境目录（起服务时以 --home 传入；空=不传）
        /// </summary>
        private readonly string _home;

        /// <summary>
        /// 脚本前的参数前缀——解释器语义（Python = "-u "；Node = 空串）
        /// </summary>
        private readonly string _argPrefix;

        /// <summary>
        /// 错误码前缀——PY / NODE（失败面按面归因）
        /// </summary>
        private readonly string _errPrefix;

        /// <summary>
        /// 日志标签——LogStore 来源名（PyService / NodeService）
        /// </summary>
        private readonly string _logTag;

        /// <summary>
        /// 连续拉起失败上限——达上限置不可用态（防崩溃风暴；需重启恢复）
        /// </summary>
        private readonly int _maxLaunchFailures;

        /// <summary>
        /// 往返串行锁
        /// </summary>
        private readonly object _gate = new object();

        /// <summary>
        /// 协议行队列（读线程喂入）
        /// </summary>
        private readonly BlockingCollection<string> _lines = new BlockingCollection<string>();

        /// <summary>
        /// 进程句柄
        /// </summary>
        private Process? _process;

        /// <summary>
        /// 进程组句柄——强杀兜底（外层注入；null = 不加入组）
        /// </summary>
        public object? JobHandle;

        /// <summary>
        /// 连续拉起失败计数
        /// </summary>
        private int _failCount;

        /// <summary>
        /// 建立服务持有对象。
        /// </summary>
        /// <param name="name">服务名</param>
        /// <param name="exe">解释器全路径</param>
        /// <param name="script">入口脚本全路径</param>
        /// <param name="workDir">工作目录</param>
        /// <param name="timeoutMs">单次调用超时毫秒</param>
        /// <param name="args">启动参数（命令行附加串；空=无）</param>
        /// <param name="home">环境目录（--home 传参；空=不传）</param>
        /// <param name="argPrefix">脚本前的参数前缀（解释器语义；空=无）</param>
        /// <param name="errPrefix">错误码前缀（PY / NODE）</param>
        /// <param name="logTag">日志标签</param>
        /// <param name="maxLaunchFailures">连续拉起失败上限</param>
        public StdioServiceHost(string name, string exe, string script, string workDir, int timeoutMs, string args, string home,
            string argPrefix, string errPrefix, string logTag, int maxLaunchFailures)
        {
            _name = name;
            _exe = exe;
            _script = script;
            _workDir = workDir;
            _timeoutMs = timeoutMs;
            _args = args ?? "";
            _home = home ?? "";
            _argPrefix = argPrefix ?? "";
            _errPrefix = errPrefix;
            _logTag = logTag;
            _maxLaunchFailures = maxLaunchFailures;
        }

        /// <summary>
        /// 服务名。
        /// </summary>
        public string Name
        {
            get
            {
                return _name;
            }
        }

        /// <summary>
        /// 进程号——锚文件记录用（未启动 / 已退出返回 0）。
        /// </summary>
        /// <returns>进程号；无进程 0</returns>
        public int ProcessId()
        {
            Process? proc = _process;
            if (proc == null)
            {
                return 0;
            }
            try
            {
                if (proc.HasExited)
                {
                    return 0;
                }
                return proc.Id;
            }
            catch (Exception)
            {
                return 0;
            }
        }

        /// <summary>
        /// 进程是否存活。
        /// </summary>
        /// <returns>存活为 true</returns>
        public bool IsAlive()
        {
            Process? proc = _process;
            if (proc == null)
            {
                return false;
            }
            try
            {
                return !proc.HasExited;
            }
            catch (Exception)
            {
                return false;
            }
        }

        /// <summary>
        /// 连续拉起失败计数。
        /// </summary>
        /// <returns>计数</returns>
        public int FailCount()
        {
            return _failCount;
        }

        /// <summary>
        /// 拉起进程并等 ready 握手。
        /// </summary>
        /// <returns>错误文本（空=成功）</returns>
        public string Start()
        {
            lock (_gate)
            {
                return StartLocked();
            }
        }

        /// <summary>
        /// 优雅停止——发 quit，超时强杀兜底。
        /// </summary>
        /// <returns>错误文本（空=成功）</returns>
        public string Stop()
        {
            lock (_gate)
            {
                Process? proc = _process;
                if (proc == null)
                {
                    return "";
                }
                try
                {
                    if (!proc.HasExited && proc.StandardInput != null)
                    {
                        proc.StandardInput.WriteLine("{\"id\":\"_quit\",\"op\":\"quit\"}");
                        proc.StandardInput.Flush();
                    }
                    if (!proc.WaitForExit(5000))
                    {
                        KillTree(proc, _logTag);
                    }
                }
                catch (Exception ex)
                {
                    LogStore.Add(_logTag, 2, "服务停止异常（" + _name + "）: " + ex.Message, "SYS");
                    KillTree(proc, _logTag);
                }
                _process = null;
                return "";
            }
        }

        /// <summary>
        /// 协议往返——发一行 JSON、读一行 JSON；进程不在则重拉。
        /// </summary>
        /// <param name="payload">请求体 JSON 文本</param>
        /// <returns>回执 JSON 或 ERR| 前缀错误文本</returns>
        public string Invoke(string payload)
        {
            lock (_gate)
            {
                bool restarted = false;
                if (!IsAlive())
                {
                    string startError = StartLocked();
                    if (startError.Length > 0)
                    {
                        return startError;
                    }
                    restarted = true;
                }

                // [段1] 清残留——超时中断的上一次回执（串行语义下只可能属于上一次）
                string? residue;
                while (_lines.TryTake(out residue, 0))
                {
                }

                Process? proc = _process;
                if (proc == null || proc.StandardInput == null)
                {
                    return "ERR|" + _errPrefix + "_NO_PROCESS|服务进程不可用：" + _name;
                }

                try
                {
                    proc.StandardInput.WriteLine(payload);
                    proc.StandardInput.Flush();
                }
                catch (Exception ex)
                {
                    _failCount = _failCount + 1;
                    return "ERR|" + _errPrefix + "_WRITE|写入服务失败（" + _name + "）: " + ex.Message;
                }

                // [段2] 收回执——id 不符即上一次调用的迟到回执（串行语义下只可能属于上一次），丢弃续读
                string requestId = ExtractId(payload);
                long deadlineMs = Environment.TickCount64 + _timeoutMs;
                string? line = null;
                while (true)
                {
                    int remainMs = (int)(deadlineMs - Environment.TickCount64);
                    if (remainMs < 0)
                    {
                        remainMs = 0;
                    }
                    if (!_lines.TryTake(out line, remainMs))
                    {
                        // 超时不杀——清残留（本次回执迟到即弃），下次调用重来
                        string? late;
                        while (_lines.TryTake(out late, 0))
                        {
                        }
                        _failCount = _failCount + 1;
                        return "ERR|" + _errPrefix + "_TIMEOUT|服务调用超时（" + _name + "，" + _timeoutMs.ToString() + " ms；进程未杀）";
                    }
                    if (requestId.Length == 0)
                    {
                        break;
                    }
                    string replyId = ExtractId(line);
                    if (replyId.Length == 0 || replyId == requestId)
                    {
                        break;
                    }
                    // 迟到回执——出声后丢弃（错配比超时更危险：调用方会把它当本次结果）
                    LogStore.Add(_logTag, 2, "丢弃迟到回执（" + _name + "）：请求 id=" + requestId + "，回执 id=" + replyId, "SYS");
                    line = null;
                }
                _failCount = 0;
                string replyLine = line ?? string.Empty;
                if (restarted)
                {
                    return MergeRestartedReply(replyLine);
                }
                return replyLine;
            }
        }

        /// <summary>
        /// 拉起实现——调用方持锁。
        /// </summary>
        /// <returns>错误文本（空=成功）</returns>
        private string StartLocked()
        {
            if (_failCount >= _maxLaunchFailures)
            {
                return "ERR|" + _errPrefix + "_UNAVAILABLE|服务连续拉起失败达上限（" + _name + "）——需重启恢复";
            }

            // [段0] 清残留回执——上次进程留下的 §EOF§ / 迟到行不得被本次 ready 握手读成「启动即退出」
            string? staleLine;
            while (_lines.TryTake(out staleLine, 0))
            {
            }

            ProcessStartInfo psi = new ProcessStartInfo();
            psi.FileName = _exe;
            string argsSuffix = _args.Length > 0 ? " " + _args : "";
            if (_home.Length > 0)
            {
                argsSuffix = argsSuffix + " --home \"" + _home + "\"";
            }
            psi.Arguments = _argPrefix + "\"" + _script + "\"" + argsSuffix;
            psi.UseShellExecute = false;
            psi.CreateNoWindow = true;
            psi.RedirectStandardInput = true;
            psi.RedirectStandardOutput = true;
            psi.RedirectStandardError = true;
            psi.StandardOutputEncoding = Encoding.UTF8;
            psi.StandardErrorEncoding = Encoding.UTF8;
            psi.StandardInputEncoding = new UTF8Encoding(false);
            if (_workDir.Length > 0 && Directory.Exists(_workDir))
            {
                psi.WorkingDirectory = _workDir;
            }

            Process proc;
            try
            {
                proc = new Process();
                proc.StartInfo = psi;
                proc.Start();
            }
            catch (Exception ex)
            {
                _failCount = _failCount + 1;
                return "ERR|" + _errPrefix + "_LAUNCH|" + ex.GetType().Name + "|" + ex.Message;
            }

            Thread reader = new Thread(delegate ()
            {
                try
                {
                    string? line;
                    while ((line = proc.StandardOutput.ReadLine()) != null)
                    {
                        _lines.Add(line);
                    }
                }
                catch (Exception ex)
                {
                    // 进程退出或流关闭——读线程自然结束（不打断调用方）
                    LogStore.Add(_logTag, 2, "协议读线程结束（" + _name + "）: " + ex.Message, "SYS");
                }
                _lines.Add("§EOF§");
            });
            reader.IsBackground = true;
            reader.Start();

            Thread logReader = new Thread(delegate ()
            {
                try
                {
                    string? line;
                    while ((line = proc.StandardError.ReadLine()) != null)
                    {
                        LogStore.Add(_logTag, 1, "[" + _name + "] " + line, "SYS");
                    }
                }
                catch (Exception ex)
                {
                    // 日志面异常——记一笔，不阻断协议面
                    LogStore.Add(_logTag, 1, "日志读线程结束（" + _name + "）: " + ex.Message, "SYS");
                }
            });
            logReader.IsBackground = true;
            logReader.Start();

            _process = proc;
            if (JobHandle != null && !ProcessGroups.Current.Attach(JobHandle, proc))
            {
                LogStore.Add(_logTag, 2, "进程加入 Job 失败（" + _name + "）——该进程无强杀兜底", "SYS");
            }

            // [段1] ready 握手——首行必须为 ready 事件（严格协议，见 design-ch4-pytools §5.1）
            string? first;
            if (!_lines.TryTake(out first, _timeoutMs) || first == null)
            {
                _failCount = _failCount + 1;
                KillTree(proc, _logTag);
                _process = null;
                return "ERR|" + _errPrefix + "_READY_TIMEOUT|服务启动未返回 ready（" + _name + "，" + _timeoutMs.ToString() + " ms）";
            }
            if (first == "§EOF§")
            {
                _failCount = _failCount + 1;
                _process = null;
                return "ERR|" + _errPrefix + "_START_FAILED|服务进程启动即退出（" + _name + "）——请检查脚本与依赖";
            }
            _failCount = 0;
            return "";
        }
        /// <summary>
        /// 取协议行里的 id 文本——非 JSON / 缺 id 返回空串（空串表示无法比对，按本次结果收下）。
        /// </summary>
        /// <param name="text">协议行文本</param>
        /// <returns>id 文本</returns>
        private static string ExtractId(string text)
        {
            using (JsonDocument? doc = JsonUtil.Parse(text, "StdioServiceHost.id"))
            {
                if (doc == null)
                {
                    return "";
                }
                return JsonUtil.GetStr(doc.RootElement, "id", "");
            }
        }

        /// <summary>
        /// 首次拉起回执装配——内层为 JSON 对象时浅合并 restarted 字段（调用方读 ok/out 等零感知）；
        /// 非对象形态退回包装（回执原文受保护）。
        /// </summary>
        /// <param name="reply">服务原样回执行</param>
        /// <returns>带 restarted 标注的回执 JSON</returns>
        private static string MergeRestartedReply(string reply)
        {
            System.Text.Json.JsonDocument? doc = JsonUtil.Parse(reply, "StdioServiceHost");
            if (doc == null || doc.RootElement.ValueKind != System.Text.Json.JsonValueKind.Object)
            {
                if (doc != null)
                {
                    doc.Dispose();
                }
                return "{\"restarted\":true,\"reply\":" + JsonUtil.Str(reply) + "}";
            }
            StringBuilder sb = new StringBuilder();
            sb.Append("{");
            bool first = true;
            foreach (System.Text.Json.JsonProperty prop in doc.RootElement.EnumerateObject())
            {
                if (!first)
                {
                    sb.Append(",");
                }
                sb.Append(JsonUtil.Str(prop.Name));
                sb.Append(":");
                sb.Append(prop.Value.GetRawText());
                first = false;
            }
            if (!first)
            {
                sb.Append(",");
            }
            sb.Append("\"restarted\":true}");
            doc.Dispose();
            return sb.ToString();
        }

        /// <summary>
        /// 杀进程树——不留僵尸（薄壳的短命脚本执行面同样复用本实现）。
        /// </summary>
        /// <param name="proc">目标进程</param>
        /// <param name="logTag">日志标签</param>
        public static void KillTree(Process proc, string logTag)
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
                LogStore.Add(logTag, 2, "进程树终止失败: " + ex.Message, "SYS");
            }
        }
    }

    /// <summary>
    /// 服务锚——pid 文件的读写与启动前自愈。
    /// <para>
    /// 锚前缀与日志标签由调用方注入（py / node 各一套），<b>互不扫描对方锚文件</b>——
    /// 一侧的遗留清理不会误杀另一侧进程。
    /// </para>
    /// </summary>
    internal static class ServiceAnchor
    {
        /// <summary>
        /// 写服务锚——拉起成功后落盘（自愈唯一依据；不主动删，正常停止时 <see cref="Remove"/> 清）。
        /// </summary>
        /// <param name="runtimeDir">运行期落盘目录（空 = 不写锚）</param>
        /// <param name="prefix">锚前缀（py / node）</param>
        /// <param name="serviceName">服务名</param>
        /// <param name="pid">进程号</param>
        /// <param name="logTag">日志标签</param>
        public static void Write(string runtimeDir, string prefix, string serviceName, int pid, string logTag)
        {
            if (runtimeDir.Length == 0 || pid <= 0)
            {
                return;
            }
            try
            {
                if (!Directory.Exists(runtimeDir))
                {
                    Directory.CreateDirectory(runtimeDir);
                }
                File.WriteAllText(Path.Combine(runtimeDir, prefix + "_" + serviceName + ".pid"), pid.ToString());
            }
            catch (Exception ex)
            {
                LogStore.Add(logTag, 2, "锚文件写入失败（" + serviceName + "）: " + ex.Message, "SYS");
            }
        }

        /// <summary>
        /// 删服务锚——服务正常停止后清理（异常残留由 <see cref="SelfHeal"/> 兜底）。
        /// </summary>
        /// <param name="runtimeDir">运行期落盘目录（空 = 无锚面）</param>
        /// <param name="prefix">锚前缀（py / node）</param>
        /// <param name="serviceName">服务名</param>
        /// <param name="logTag">日志标签</param>
        public static void Remove(string runtimeDir, string prefix, string serviceName, string logTag)
        {
            if (runtimeDir.Length == 0)
            {
                return;
            }
            try
            {
                string anchor = Path.Combine(runtimeDir, prefix + "_" + serviceName + ".pid");
                if (File.Exists(anchor))
                {
                    File.Delete(anchor);
                }
            }
            catch (Exception ex)
            {
                LogStore.Add(logTag, 2, "清除服务锚失败（" + serviceName + "）: " + ex.Message, "SYS");
            }
        }

        /// <summary>
        /// 启动前自愈——清理上次宿主遗留的服务进程（锚文件记录；调用时机 = 宿主启动链、本实例尚未拉起服务）。
        /// </summary>
        /// <param name="runtimeDir">运行期落盘目录（空 = 自愈面关闭）</param>
        /// <param name="prefix">锚前缀（py / node——只扫本前缀）</param>
        /// <param name="logTag">日志标签</param>
        /// <returns>清理条数</returns>
        public static int SelfHeal(string runtimeDir, string prefix, string logTag)
        {
            if (runtimeDir.Length == 0 || !Directory.Exists(runtimeDir))
            {
                return 0;
            }
            int cleaned = 0;
            string[] anchors = Directory.GetFiles(runtimeDir, prefix + "_*.pid");
            for (int i = 0; i < anchors.Length; i = i + 1)
            {
                string anchor = anchors[i];
                int pid = 0;
                try
                {
                    string raw = File.ReadAllText(anchor).Trim();
                    int.TryParse(raw, out pid);
                }
                catch (Exception)
                {
                    pid = 0;
                }
                if (pid > 0 && pid != Environment.ProcessId)
                {
                    try
                    {
                        Process orphan = Process.GetProcessById(pid);
                        if (!orphan.HasExited)
                        {
                            orphan.Kill(true);
                            cleaned = cleaned + 1;
                            LogStore.Add(logTag, 1, "自愈清理遗留 " + prefix + " 服务进程 pid=" + pid.ToString(), "SYS");
                        }
                    }
                    catch (Exception ex)
                    {
                        // 进程不存在 / 无权限——锚直接清（出声：失败可见）
                        LogStore.Add(logTag, 2, "自愈清理遗留进程失败 pid=" + pid.ToString() + ": " + ex.Message, "SYS");
                    }
                }
                try
                {
                    File.Delete(anchor);
                }
                catch (Exception ex)
                {
                    LogStore.Add(logTag, 2, "自愈清理锚文件失败: " + anchor + " | " + ex.Message, "SYS");
                }
            }
            return cleaned;
        }
    }
}
