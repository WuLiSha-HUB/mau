using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Text;
using System.Text.Json;
using System.Threading;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// PowerShell 执行服务——powershell / powershell7 工具实现（PsCat 工具组）。
    /// 解释器线（shell 参数）：powershell=默认解释器（Windows PowerShell 5.1）；powershell7=PowerShell 7
    ///   （pwsh.exe 路径读配置 ps.pwsh_path——未配置/路径不存在即明示 ERR，不静默回落默认线）。
    /// 设计动机（CH2 shell_exec 痛点反推）：
    ///   1) 编码/转义不可控 → 整段命令 UTF-16LE → Base64 走 -EncodedCommand（PS 官方免转义通道）
    ///   2) 输出乱码 → 命令前缀注入 [Console]::OutputEncoding=UTF8 + chcp 65001，宿主按 UTF-8 解码
    ///   3) 写文件损坏数据 → 词法拦截（A228 档 2 起判定归 ps.exec 积木层——词表与文案随积木热改；宿主侧只执行）
    ///   4) 进程挂死 → 禁 Start-Process / ReadKey / ReadLine；超时杀进程树（taskkill /T /F）
    ///   5) 输出截断 → stdout 16KB / stderr 8KB + truncated 标记
    /// 线程模型：同步执行（工具循环等待语义）；进程异步读流防死锁。
    /// </summary>
    public sealed class PsService : Mau.Runtime.IPsService
    {
        /// <summary>stdout 截断上限——回传 LLM 上下文防爆</summary>
        private const int MaxStdoutChars = 16384;

        /// <summary>stderr 截断上限——排错入口保留较短</summary>
        private const int MaxStderrChars = 8192;

        /// <summary>默认超时毫秒——无 timeout_ms 参数时</summary>
        private const long DefaultTimeoutMs = 30000;

        /// <summary>超时上限毫秒——与工单/后台执行层统一 120 秒口径（A48 同步）</summary>
        private const long MaxTimeoutMs = 120000;

        /// <summary>
        /// 执行 PowerShell 命令——EncodedCommand 启动 → 超时/输出处理 → JSON 回执（词法拦截已在积木层前置，A228 档 2）
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（command/cwd/timeout_ms）</param>
        /// <param name="shell">解释器线标识（powershell=默认解释器 / powershell7=PowerShell 7）</param>
        /// <returns>结果 JSON 或 ERR| 错误文本</returns>
        public string Exec(string argsJson, string shell)
        {
            string command = ExtractArg(argsJson, "command");
            if (command.Length == 0)
            {
                return "ERR|PS_BAD_ARGS|缺少参数 command";
            }
            // [段0] 解释器解析——默认线 powershell.exe；powershell7 线读配置 ps.pwsh_path（未配置/路径不存在 → 明示 ERR，不静默回落默认线）
            string exePath = "powershell.exe";
            if (string.Equals(shell, "powershell7", StringComparison.Ordinal))
            {
                string shellError = "";
                exePath = ResolvePwshPath(out shellError);
                if (exePath.Length == 0)
                {
                    return shellError;
                }
            }
            string cwd = ExtractArg(argsJson, "cwd");
            long timeoutMs = ParseTimeout(ExtractArg(argsJson, "timeout_ms"));
            // [段1] 拦截检查已下沉积木层（A228 档 2——ps.exec 积木先判；词表与文案归积木，宿主只执行）
            // [段2] 命令前缀——UTF-8 输出内建（PS 5.1 默认 ANSI/GBK——乱码根因；chcp 65001 + OutputEncoding 双保险）+ 进度流静默（首载模块 CLIXML 噪音）
            //        powershell7 线追加 $PSStyle.OutputRendering='PlainText'（pwsh 7 专有——默认 Host 渲染给格式化输出与错误流加 ANSI 色码，LLM 消费面需纯文本）
            string prefix = "[Console]::OutputEncoding=[Text.Encoding]::UTF8; $ProgressPreference='SilentlyContinue'; chcp 65001 > $null; ";
            if (string.Equals(shell, "powershell7", StringComparison.Ordinal))
            {
                prefix = prefix + "$PSStyle.OutputRendering='PlainText'; ";
            }
            // [段2b] 根寻址前缀——猫可见受控根注册为本会话 PS 驱动器（命令内可直接写 ccbp:L1/Tree.md）
            string rootPrefix = BuildRootDrivePrefix(argsJson);
            string prefixed = prefix + rootPrefix + command;
            // [段3] EncodedCommand——UTF-16LE → Base64（免转义：引号/反斜杠/JSON 原样直达 PS 解析器）
            string base64 = Convert.ToBase64String(Encoding.Unicode.GetBytes(prefixed));
            ProcessStartInfo psi = new ProcessStartInfo();
            psi.FileName = exePath;
            psi.Arguments = "-NoProfile -NonInteractive -ExecutionPolicy Bypass -EncodedCommand " + base64;
            psi.UseShellExecute = false;
            psi.CreateNoWindow = true;
            psi.RedirectStandardOutput = true;
            psi.RedirectStandardError = true;
            psi.StandardOutputEncoding = Encoding.UTF8;
            psi.StandardErrorEncoding = Encoding.UTF8;
            if (cwd.Length > 0)
            {
                try
                {
                    psi.WorkingDirectory = cwd;
                }
                catch (Exception ex)
                {
                    // 工作目录无效——回退默认（不阻断执行）
                    LogStore.Add("CatHome4", 2, "PS 工作目录无效，回退默认: " + ex.Message, "SYS");
                }
            }
            // [段4] 启动 + 异步读流（异步读防管道缓冲区死锁——同步 ReadToEnd 大输出会卡）
            Process proc;
            try
            {
                proc = new Process();
                proc.StartInfo = psi;
                proc.Start();
            }
            catch (Exception ex)
            {
                return "ERR|PS_LAUNCH|" + ex.GetType().Name + "|" + ex.Message;
            }
            StringBuilder stdout = new StringBuilder();
            StringBuilder stderr = new StringBuilder();
            proc.OutputDataReceived += delegate (object sender, DataReceivedEventArgs e)
            {
                if (e.Data != null)
                {
                    AppendCapped(stdout, e.Data, MaxStdoutChars);
                }
            };
            proc.ErrorDataReceived += delegate (object sender, DataReceivedEventArgs e)
            {
                if (e.Data != null)
                {
                    AppendCapped(stderr, e.Data, MaxStderrChars);
                }
            };
            proc.BeginOutputReadLine();
            proc.BeginErrorReadLine();
            // [段5] 超时等待——超时杀进程树（taskkill /T /F——不留僵尸）
            bool timeout = false;
            try
            {
                if (!proc.WaitForExit((int)timeoutMs))
                {
                    timeout = true;
                    KillTree(proc);
                }
            }
            catch (Exception ex)
            {
                // 等待异常（进程已退出等）——按已退出处理
                LogStore.Add("CatHome4", 2, "PS 等待进程异常，按已退出处理: " + ex.Message, "SYS");
            }
            string outText = stdout.ToString();
            string errText = CleanErrorText(stderr.ToString());
            bool outTrunc = outText.Length >= MaxStdoutChars;
            bool errTrunc = errText.Length >= MaxStderrChars;
            // [段6] 结构化回执（design-ch4-tools 附录）——首行 JSON 元数据头 + 正文定界
            //   正文 = stdout（+ 换行 + stderr）；行数在头里给出，前端按行数切分（不依赖内容分隔符——零撞车）
            int stdoutLines = CountLinesOf(outText);
            int stderrLines = CountLinesOf(errText);
            string shellName = string.Equals(shell, "powershell7", StringComparison.Ordinal) ? "powershell7" : "powershell";
            Dictionary<string, object> fields = new Dictionary<string, object>();
            int exitCode = timeout ? -1 : proc.ExitCode;
            fields["exit"] = exitCode;
            fields["truncated"] = outTrunc || errTrunc;
            fields["timeout"] = timeout;
            fields["stdoutLines"] = stdoutLines;
            fields["stderrLines"] = stderrLines;
            string bodyText = errText.Length > 0 ? (outText + "\n" + errText) : outText;
            return ToolMetaHead.With(shellName, true, shellName, -1, fields, shellName + " | 退出 " + exitCode.ToString() + " · " + stdoutLines.ToString() + " 行" + "\n" + bodyText);
        }

        /// <summary>
        /// 行数统计——空串 0；否则 \n 计数 + 1（与前端 body.split('\n').length 同口径）
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
        /// <summary>
        /// 根寻址前缀——猫可见受控根注册为本次 PS 会话的驱动器（New-PSDrive）。
        /// 注入后命令内可直接写 ccbp:L1/Tree.md / mau:README.md（驱动器相对路径——正反斜杠均可）。
        /// 失败出声不中断：注册异常写入错误流（重名等由管理员面黑名单规避，此处不静默、不改名）。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（catId 取猫级根集）</param>
        /// <returns>PS 前缀片段（无可注入根时空串）</returns>
        private static string BuildRootDrivePrefix(string argsJson)
        {
            string catId = ExtractArg(argsJson, "catId");
            if (catId.Length == 0)
            {
                return "";
            }
            FileSystemService fs = ToolCatContext.ResolveCatFileSystem(catId);
            if (fs == null)
            {
                return "";
            }
            // [段1] 逐根生成注册片段——try/catch 出声（注册失败不阻断命令本身）
            Dictionary<string, string> roots = fs.DescribeRoots();
            StringBuilder sb = new StringBuilder();
            foreach (KeyValuePair<string, string> pair in roots)
            {
                string safeRoot = pair.Value.Replace("'", "''");
                sb.Append("try { New-PSDrive -Name ");
                sb.Append(pair.Key);
                sb.Append(" -PSProvider FileSystem -Root '");
                sb.Append(safeRoot);
                sb.Append("' -ErrorAction Stop | Out-Null } catch { Write-Error ('根寻址注册失败: ");
                sb.Append(pair.Key);
                sb.Append(" — ' + $_.Exception.Message) }; ");
            }
            return sb.ToString();
        }

        /// <summary>
        /// 解析 PowerShell 7 可执行路径——读配置 ps.pwsh_path（未配置 / 路径不存在 → error 明示，不静默回落默认解释器）
        /// </summary>
        /// <param name="error">错误文本（空=解析成功）</param>
        /// <returns>pwsh.exe 全路径（解析失败返回空串）</returns>
        private static string ResolvePwshPath(out string error)
        {
            error = "";
            Mau.Runtime.ConfigStore cfg;
            Mau.Runtime.DataBox.TryResolve<Mau.Runtime.ConfigStore>(out cfg);
            string path = "";
            if (cfg != null)
            {
                path = cfg.Get("ps.pwsh_path", "");
            }
            if (path.Length == 0)
            {
                error = "ERR|PS_PWSH_MISSING|PowerShell 7 未配置——请在配置面填写 ps.pwsh_path（pwsh.exe 全路径；获取方式 powershell -Command where.exe pwsh）";
                return "";
            }
            if (!System.IO.File.Exists(path))
            {
                error = "ERR|PS_PWSH_NOT_FOUND|配置的 PowerShell 7 路径不存在：" + path + "——请更新 ps.pwsh_path";
                return "";
            }
            return path;
        }

        /// <summary>
        /// 清洗错误文本——CLIXML 序列化解包（PowerShell stderr 重定向时原生格式）+ ANSI 色码剥离（LLM 消费面需纯文本）
        /// </summary>
        /// <param name="raw">原始 stderr 文本</param>
        /// <returns>清洗后的错误文本（无 CLIXML 壳时仅剥色）</returns>
        private static string CleanErrorText(string raw)
        {
            if (raw.Length == 0)
            {
                return raw;
            }
            string text = raw;
            // [段1] CLIXML 解包——拼接全部 <S S="Error">…</S> 片段（无 Objs 壳时原样保留）
            int objStart = text.IndexOf("<Objs", StringComparison.Ordinal);
            if (objStart >= 0)
            {
                StringBuilder sb = new StringBuilder();
                int cursor = objStart;
                while (true)
                {
                    int openTag = text.IndexOf("<S S=\"", cursor, StringComparison.Ordinal);
                    if (openTag < 0)
                    {
                        break;
                    }
                    int contentStart = text.IndexOf('>', openTag);
                    if (contentStart < 0)
                    {
                        break;
                    }
                    int contentEnd = text.IndexOf("</S>", contentStart + 1, StringComparison.Ordinal);
                    if (contentEnd < 0)
                    {
                        break;
                    }
                    sb.Append(UnescapeCliXml(text.Substring(contentStart + 1, contentEnd - contentStart - 1)));
                    cursor = contentEnd + 4;
                }
                if (sb.Length > 0)
                {
                    text = sb.ToString();
                }
            }
            // [段2] ANSI 剥离
            return StripAnsi(text);
        }

        /// <summary>
        /// CLIXML 转义还原——_xNNNN_ 控制字符 + XML 实体（&amp;lt; &amp;gt; &amp;amp; &amp;quot; &amp;apos;）
        /// </summary>
        /// <param name="text">转义文本</param>
        /// <returns>还原文本</returns>
        private static string UnescapeCliXml(string text)
        {
            StringBuilder sb = new StringBuilder();
            int i = 0;
            while (i < text.Length)
            {
                if (text[i] == '_' && i + 6 < text.Length && text[i + 1] == 'x' && text[i + 6] == '_')
                {
                    int code = Hex4(text, i + 2);
                    if (code >= 0)
                    {
                        sb.Append((char)code);
                        i = i + 7;
                        continue;
                    }
                }
                if (text[i] == '&')
                {
                    if (i + 4 <= text.Length && string.CompareOrdinal(text, i, "&lt;", 0, 4) == 0)
                    {
                        sb.Append('<');
                        i = i + 4;
                        continue;
                    }
                    if (i + 4 <= text.Length && string.CompareOrdinal(text, i, "&gt;", 0, 4) == 0)
                    {
                        sb.Append('>');
                        i = i + 4;
                        continue;
                    }
                    if (i + 5 <= text.Length && string.CompareOrdinal(text, i, "&amp;", 0, 5) == 0)
                    {
                        sb.Append('&');
                        i = i + 5;
                        continue;
                    }
                    if (i + 6 <= text.Length && string.CompareOrdinal(text, i, "&quot;", 0, 6) == 0)
                    {
                        sb.Append('"');
                        i = i + 6;
                        continue;
                    }
                    if (i + 6 <= text.Length && string.CompareOrdinal(text, i, "&apos;", 0, 6) == 0)
                    {
                        sb.Append('\'');
                        i = i + 6;
                        continue;
                    }
                }
                sb.Append(text[i]);
                i = i + 1;
            }
            return sb.ToString();
        }

        /// <summary>
        /// 四位十六进制解析——"000D" → 13（非法返回 -1）
        /// </summary>
        /// <param name="text">源文本</param>
        /// <param name="start">起始下标</param>
        /// <returns>数值（-1=非法）</returns>
        private static int Hex4(string text, int start)
        {
            if (start + 4 > text.Length)
            {
                return -1;
            }
            int value = 0;
            for (int i = 0; i < 4; i = i + 1)
            {
                char c = text[start + i];
                int digit;
                if (c >= '0' && c <= '9')
                {
                    digit = c - '0';
                }
                else if (c >= 'a' && c <= 'f')
                {
                    digit = c - 'a' + 10;
                }
                else if (c >= 'A' && c <= 'F')
                {
                    digit = c - 'A' + 10;
                }
                else
                {
                    return -1;
                }
                value = value * 16 + digit;
            }
            return value;
        }

        /// <summary>
        /// ANSI 转义剥离——ESC[ 参数 终止字母 序列整体删除
        /// </summary>
        /// <param name="text">含转义文本</param>
        /// <returns>纯文本</returns>
        private static string StripAnsi(string text)
        {
            StringBuilder sb = new StringBuilder();
            int i = 0;
            while (i < text.Length)
            {
                if (text[i] == '\u001B' && i + 1 < text.Length && text[i + 1] == '[')
                {
                    int j = i + 2;
                    while (j < text.Length)
                    {
                        char c = text[j];
                        if ((c >= '0' && c <= '9') || c == ';' || c == '?' || c == ' ')
                        {
                            j = j + 1;
                            continue;
                        }
                        break;
                    }
                    if (j < text.Length && ((text[j] >= 'A' && text[j] <= 'Z') || (text[j] >= 'a' && text[j] <= 'z')))
                    {
                        i = j + 1;
                        continue;
                    }
                }
                sb.Append(text[i]);
                i = i + 1;
            }
            return sb.ToString();
        }

        /// <summary>
        /// 追加文本到 StringBuilder——上限截断（超限后丢弃后续——截断标记由调用方比对长度）
        /// </summary>
        /// <param name="sb">目标缓冲区</param>
        /// <param name="line">追加行</param>
        /// <param name="max">上限字符数</param>
        private static void AppendCapped(StringBuilder sb, string line, int max)
        {
            if (sb.Length >= max)
            {
                return;
            }
            if (sb.Length > 0)
            {
                sb.Append('\n');
            }
            int remain = max - sb.Length;
            if (remain <= 0)
            {
                return;
            }
            if (line.Length <= remain)
            {
                sb.Append(line);
            }
            else
            {
                sb.Append(line, 0, remain);
            }
        }

        /// <summary>
        /// 超时解析——非法/空回退默认；超上限钳制
        /// </summary>
        /// <param name="raw">原始字符串</param>
        /// <returns>超时毫秒</returns>
        private static long ParseTimeout(string raw)
        {
            long ms;
            if (long.TryParse(raw, out ms) && ms > 0)
            {
                if (ms > MaxTimeoutMs)
                {
                    return MaxTimeoutMs;
                }
                return ms;
            }
            return DefaultTimeoutMs;
        }

        /// <summary>
        /// 杀进程树——taskkill /T /F（子进程一并终止；杀不掉静默——进程已退出）
        /// </summary>
        /// <param name="proc">目标进程</param>
        private static void KillTree(Process proc)
        {
            try
            {
                ProcessStartInfo psi = new ProcessStartInfo();
                psi.FileName = "taskkill.exe";
                psi.Arguments = "/T /F /PID " + proc.Id.ToString();
                psi.UseShellExecute = false;
                psi.CreateNoWindow = true;
                Process killer = Process.Start(psi);
                if (killer != null)
                {
                    killer.WaitForExit(5000);
                }
            }
            catch (Exception ex)
            {
                // 杀树失败静默——进程可能已退出
                LogStore.Add("CatHome4", 2, "进程树强杀失败: " + ex.Message, "SYS");
            }
            try
            {
                proc.Kill();
            }
            catch (Exception ex)
            {
                // 已退出
                LogStore.Add("CatHome4", 1, "进程已退出，Kill 跳过: " + ex.Message, "SYS");
            }
        }

        /// <summary>
        /// 展平参数提取——argsJson 中取字符串值（不存在返回空串）
        /// </summary>
        /// <param name="argumentsJson">参数 JSON</param>
        /// <param name="key">参数名</param>
        /// <returns>参数值</returns>
        private static string ExtractArg(string argumentsJson, string key)
        {
            try
            {
                JsonDocument doc = JsonDocument.Parse(argumentsJson);
                try
                {
                    if (doc.RootElement.TryGetProperty(key, out JsonElement el))
                    {
                        if (el.ValueKind == JsonValueKind.String)
                        {
                            string s = el.GetString();
                            if (s == null)
                            {
                                return "";
                            }
                            return s;
                        }
                        return el.GetRawText();
                    }
                }
                finally
                {
                    doc.Dispose();
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "powershell 参数提取失败: " + ex.Message, "PS");
            }
            return "";
        }
    }
}
