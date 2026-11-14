using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Text;

namespace Mau.Runtime
{
    /// <summary>
    /// Python 执行服务——短命脚本执行分部（<see cref="PyService"/> partial）。
    /// <para>
    /// <see cref="Run"/>（起-跑-退：异步读流防死锁 + 超时杀树）· <see cref="List"/>（可写受控根内 *.py 扫描）
    /// · 路径校验族（受控根内 + 存在性判定）。
    /// </para>
    /// <para>
    /// 常驻服务池与 stdio 严格协议归另一分部（<c>PyService.cs</c>）。
    /// </para>
    /// </summary>
    public sealed partial class PyService
    {
        // [段1] 短命脚本执行
        /// <summary>
        /// 执行一个 py 脚本（短命进程）——起-跑-退。
        /// </summary>
        /// <param name="argsJson">参数 JSON（script 必填；args 可选；cwd 可选；timeout_ms 可选）</param>
        /// <returns>结果 JSON 或 ERR| 前缀错误文本</returns>
        public string Run(string argsJson)
        {
            string scriptArg = JsonArgs.Get(argsJson, "script");
            if (scriptArg.Length == 0 || scriptArg == "§PARSE_FAIL§")
            {
                return "ERR|PY_BAD_ARGS|缺少参数 script";
            }
            string scriptError;
            string script = ResolveScript(scriptArg, out scriptError);
            if (script.Length == 0)
            {
                return scriptError;
            }
            string pythonError;
            string python = ResolvePython("", out pythonError);
            if (python.Length == 0)
            {
                return pythonError;
            }

            string extra = JsonArgs.Get(argsJson, "args");
            string workDir = JsonArgs.Get(argsJson, "cwd");
            if (workDir.Length == 0)
            {
                workDir = Path.GetDirectoryName(script) ?? "";
            }
            int timeoutMs = ParseTimeout(JsonArgs.Get(argsJson, "timeout_ms"));

            // [段1] 命令行组装——脚本路径加引号（含空格路径）；args 原样追加（调用方负责自洽）
            string arguments = "-u \"" + script + "\"";
            if (extra.Length > 0 && extra != "§PARSE_FAIL§")
            {
                arguments = arguments + " " + extra;
            }

            ProcessStartInfo psi = new ProcessStartInfo();
            psi.FileName = python;
            psi.Arguments = arguments;
            psi.UseShellExecute = false;
            psi.CreateNoWindow = true;
            psi.RedirectStandardOutput = true;
            psi.RedirectStandardError = true;
            psi.StandardOutputEncoding = Encoding.UTF8;
            psi.StandardErrorEncoding = Encoding.UTF8;
            if (workDir.Length > 0 && Directory.Exists(workDir))
            {
                psi.WorkingDirectory = workDir;
            }

            // [段2] 启动 + stdio 采集——单点实现（A365）：逐行泵送（流关闭才结束，不依赖事件回调）+ 超时杀树 + 排空收尾
            Stopwatch watch = Stopwatch.StartNew();
            Process proc;
            try
            {
                proc = new Process();
                proc.StartInfo = psi;
                proc.Start();
            }
            catch (Exception ex)
            {
                return "ERR|PY_LAUNCH|" + ex.GetType().Name + "|" + ex.Message;
            }
            StdioCaptureResult capture = StdioCapture.Drain(proc, timeoutMs, MaxOutputChars, MaxOutputChars, delegate (Process target) { KillTree(target); }, "PyService");
            watch.Stop();

            // [段3] 回执装配——stdout / stderr 全文 + 行数 + 耗时（与历史字段面一致）
            string outText = capture.Stdout;
            string errText = capture.Stderr;
            int exitCode = capture.ExitCode;
            Dictionary<string, object> fields = new Dictionary<string, object>();
            fields["exit"] = exitCode;
            fields["timeout"] = capture.Timeout;
            fields["stdout"] = outText;
            fields["stderr"] = errText;
            fields["stdoutLines"] = CountLines(outText);
            fields["stderrLines"] = CountLines(errText);
            fields["elapsedMs"] = watch.ElapsedMilliseconds;
            fields["script"] = script;
            return JsonUtil.Serialize(fields);
        }

        // [段2] 脚本清单
        /// <summary>
        /// 列出可用脚本——默认扫 mau 根下 tools/（py 工具线约定面；跳过依赖与构建目录）。
        /// </summary>
        /// <param name="argsJson">参数 JSON（dir 可选——受控根内子目录；空 = mau 根的 tools 目录）</param>
        /// <returns>结果 JSON（scripts 数组）或 ERR| 前缀错误文本</returns>
        public string List(string argsJson)
        {
            string startDir = JsonArgs.Get(argsJson, "dir");
            if (startDir == "§PARSE_FAIL§")
            {
                return "ERR|PY_BAD_ARGS|参数 JSON 解析失败";
            }
            List<string> roots = new List<string>();
            if (startDir.Length > 0)
            {
                string dirError;
                string resolved = ResolveDirectory(startDir, out dirError);
                if (resolved.Length == 0)
                {
                    return dirError;
                }
                roots.Add(resolved);
            }
            else
            {
                if (_defaultScanDir.Length == 0)
                {
                    return "ERR|PY_NOT_CONFIGURED|默认脚本面缺失（无 mau 根）——请显式给 dir";
                }
                roots.Add(_defaultScanDir);
            }

            List<string> found = new List<string>();
            HashSet<string> seen = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
            for (int i = 0; i < roots.Count; i = i + 1)
            {
                if (!Directory.Exists(roots[i]))
                {
                    continue;
                }
                try
                {
                    string[] files = Directory.GetFiles(roots[i], "*.py", SearchOption.AllDirectories);
                    for (int k = 0; k < files.Length; k = k + 1)
                    {
                        string file = files[k];
                        if (IsSkippedPath(file) || seen.Contains(file))
                        {
                            continue;
                        }
                        if (found.Count >= 200)
                        {
                            break;
                        }
                        seen.Add(file);
                        found.Add(file);
                    }
                }
                catch (Exception ex)
                {
                    LogStore.Add("PyService", 2, "脚本扫描失败: " + ex.Message, "SYS");
                }
            }

            Dictionary<string, object> payload = new Dictionary<string, object>();
            payload["count"] = found.Count;
            payload["scripts"] = found;
            return JsonUtil.Serialize(payload);
        }

        /// <summary>
        /// 脚本路径校验——必须在可写受控根内且存在。
        /// </summary>
        /// <param name="script">脚本路径（相对或绝对）</param>
        /// <param name="error">错误文本（空=成功）</param>
        /// <returns>规范全路径（失败返回空串）</returns>
        private string ResolveScript(string script, out string error)
        {
            error = "";
            RootResolveResult rr = RootPathResolver.Expand(script, _entries, Environment.CurrentDirectory);
            if (!rr.Ok)
            {
                error = "ERR|PY_BAD_PATH|脚本路径非法: " + rr.Message;
                return "";
            }
            string full = rr.FullPath;
            if (!InRoots(full))
            {
                error = "ERR|PY_OUT_OF_ROOT|脚本超出受控根范围：" + full;
                return "";
            }
            if (!File.Exists(full))
            {
                error = "ERR|PY_SCRIPT_NOT_FOUND|脚本不存在：" + full;
                return "";
            }
            return full;
        }

        /// <summary>
        /// 目录路径校验——必须在可写受控根内且存在。
        /// </summary>
        /// <param name="dir">目录路径</param>
        /// <param name="error">错误文本（空=成功）</param>
        /// <returns>规范全路径（失败返回空串）</returns>
        private string ResolveDirectory(string dir, out string error)
        {
            error = "";
            RootResolveResult rr = RootPathResolver.Expand(dir, _entries, Environment.CurrentDirectory);
            if (!rr.Ok)
            {
                error = "ERR|PY_BAD_PATH|目录路径非法: " + rr.Message;
                return "";
            }
            string full = rr.FullPath;
            if (!InRoots(full))
            {
                error = "ERR|PY_OUT_OF_ROOT|目录超出受控根范围：" + full;
                return "";
            }
            if (!Directory.Exists(full))
            {
                error = "ERR|PY_DIR_NOT_FOUND|目录不存在：" + full;
                return "";
            }
            return full;
        }

        /// <summary>
        /// 判定路径是否落在可写受控根内（带分隔符边界——防兄弟目录误判）。
        /// </summary>
        /// <param name="full">规范全路径</param>
        /// <returns>是否在根内</returns>
        private bool InRoots(string full)
        {
            for (int i = 0; i < _roots.Length; i = i + 1)
            {
                string root = _roots[i];
                if (string.Equals(full, root, StringComparison.OrdinalIgnoreCase))
                {
                    return true;
                }
                if (full.StartsWith(root + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase))
                {
                    return true;
                }
            }
            return false;
        }

        /// <summary>
        /// 扫描跳过判定——依赖与构建目录不进清单。
        /// </summary>
        /// <param name="path">文件路径</param>
        /// <returns>是否跳过</returns>
        private static bool IsSkippedPath(string path)
        {
            string lower = path.ToLowerInvariant();
            return lower.Contains("\\.venv\\") || lower.Contains("\\venv\\") || lower.Contains("\\node_modules\\")
                || lower.Contains("\\bin\\") || lower.Contains("\\obj\\") || lower.Contains("\\.git\\")
                || lower.Contains("\\__pycache__\\") || lower.Contains("\\site-packages\\");
        }

        // [段4] 退出码
        /// <summary>
        /// 退出码读取——进程未退或读取异常返回 -1。
        /// </summary>
        /// <param name="proc">目标进程</param>
        /// <returns>退出码</returns>
        private static int SafeExitCode(Process proc)
        {
            try
            {
                return proc.ExitCode;
            }
            catch (Exception)
            {
                return -1;
            }
        }
    }
}
