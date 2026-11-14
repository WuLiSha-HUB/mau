using System;
using System.Diagnostics;
using System.IO;
using System.Text;
using System.Threading.Tasks;

namespace Mau.Runtime
{
    /// <summary>
    /// 子进程 stdio 采集结果——stdout / stderr 全文 + 超时 / 退出码 / 截断标记。
    /// </summary>
    public sealed class StdioCaptureResult
    {
        /// <summary>stdout 全文（按行以 \n 连接；达上限后截断）</summary>
        public string Stdout = "";

        /// <summary>stderr 全文（按行以 \n 连接；达上限后截断）</summary>
        public string Stderr = "";

        /// <summary>是否超时（为 true 时进程树已被终止）</summary>
        public bool Timeout = false;

        /// <summary>退出码（超时固定 -1）</summary>
        public int ExitCode = -1;

        /// <summary>stdout 是否达上限被截断</summary>
        public bool OutTruncated = false;

        /// <summary>stderr 是否达上限被截断</summary>
        public bool ErrTruncated = false;

        /// <summary>从启动到采集完成的耗时（毫秒）</summary>
        public long ElapsedMs = 0;

        /// <summary>输出是否完整排空（false = 收尾窗口用尽，本次输出可能不全——已出声）</summary>
        public bool Drained = true;
    }

    /// <summary>
    /// 子进程 stdio 采集——三条 stdio 链（sh-exec / powershell / py-run）共用的单点实现（A365）。
    /// <para>
    /// 读流模型：<see cref="StreamReader.ReadLineAsync"/> 逐行泵送，流真正关闭才返回 null。不依赖
    /// <c>OutputDataReceived</c> 事件模型——事件在进程快速退出时存在历史性丢数据的竞态
    /// （旧实现 <c>BeginOutputReadLine</c> + <c>WaitForExit(timeout)</c> 后立即取缓冲，会周期性读到空输出）。
    /// </para>
    /// <para>
    /// 超时：<c>WaitForExit(timeout)</c> 返回 false → 调 <paramref name="killTree"/> 终止进程树 →
    /// 再给收尾窗口等流 EOF；仍不关闭则记 WARN 并置 <see cref="StdioCaptureResult.Drained"/> = false（不静默丢输出）。
    /// </para>
    /// </summary>
    public static class StdioCapture
    {
        /// <summary>kill 之后的排空收尾窗口（毫秒）——等进程真正退出、流 EOF</summary>
        public const int DrainGraceMs = 5000;

        /// <summary>
        /// 采集已启动子进程的 stdout / stderr——阻塞至进程退出或超时。
        /// </summary>
        /// <param name="proc">已 Start 的进程（须 RedirectStandardOutput / RedirectStandardError 为 true）</param>
        /// <param name="timeoutMs">超时毫秒（≤0 按立即超时处理）</param>
        /// <param name="maxOutChars">stdout 字符上限</param>
        /// <param name="maxErrChars">stderr 字符上限</param>
        /// <param name="killTree">终止进程树回调（各平台/各面注入自己的实现；null = 不终止）</param>
        /// <param name="logTag">日志标签</param>
        /// <returns>采集结果</returns>
        public static StdioCaptureResult Drain(Process proc, int timeoutMs, int maxOutChars, int maxErrChars, Action<Process> killTree, string logTag)
        {
            StdioCaptureResult result = new StdioCaptureResult();
            long startMs = Environment.TickCount64;
            Pump outPump = new Pump(maxOutChars);
            Pump errPump = new Pump(maxErrChars);
            Task outTask = PumpAsync(proc.StandardOutput, outPump, logTag);
            Task errTask = PumpAsync(proc.StandardError, errPump, logTag);
            // [段1] 超时等待——超时终止进程树（先杀后收尾，避免管道悬挂）
            bool exited;
            try
            {
                exited = proc.WaitForExit(timeoutMs);
            }
            catch (Exception ex)
            {
                LogStore.Add(logTag, 2, "等待子进程异常，按已退出处理: " + ex.Message, "SYS");
                exited = true;
            }
            if (exited == false)
            {
                result.Timeout = true;
                if (killTree != null)
                {
                    try
                    {
                        killTree(proc);
                    }
                    catch (Exception ex)
                    {
                        LogStore.Add(logTag, 3, "超时终止进程树失败: " + ex.Message, "SYS");
                    }
                }
                try
                {
                    proc.WaitForExit(DrainGraceMs);
                }
                catch (Exception ex)
                {
                    LogStore.Add(logTag, 2, "收尾等待异常: " + ex.Message, "SYS");
                }
            }
            // [段2] 排空收尾——流关闭后泵自然结束；未结束即出声（不静默丢输出）
            bool outDone = WaitQuiet(outTask, DrainGraceMs);
            bool errDone = WaitQuiet(errTask, DrainGraceMs);
            if (outDone == false || errDone == false)
            {
                result.Drained = false;
                LogStore.Add(logTag, 2, "子进程输出未排空（收尾窗口 " + DrainGraceMs.ToString() + " ms 用尽）——本次输出可能不全", "SYS");
            }
            // [段3] 结果装配——截断判据与旧实现同口径（长度达上限即标记）
            result.Stdout = outPump.Text;
            result.Stderr = errPump.Text;
            result.OutTruncated = outPump.Truncated;
            result.ErrTruncated = errPump.Truncated;
            result.ExitCode = -1;
            if (result.Timeout == false)
            {
                try
                {
                    result.ExitCode = proc.ExitCode;
                }
                catch (Exception ex)
                {
                    LogStore.Add(logTag, 2, "退出码读取失败: " + ex.Message, "SYS");
                }
            }
            result.ElapsedMs = Environment.TickCount64 - startMs;
            return result;
        }

        /// <summary>
        /// 逐行泵送一个流到上限缓冲——读满上限后继续读并丢弃（管道不得积压）。
        /// </summary>
        /// <param name="reader">标准输出/错误读取器</param>
        /// <param name="pump">目标缓冲</param>
        /// <param name="logTag">日志标签</param>
        /// <returns>泵任务</returns>
        private static async Task PumpAsync(StreamReader reader, Pump pump, string logTag)
        {
            while (true)
            {
                string? line;
                try
                {
                    line = await reader.ReadLineAsync().ConfigureAwait(false);
                }
                catch (Exception ex)
                {
                    LogStore.Add(logTag, 2, "读流异常: " + ex.Message, "SYS");
                    return;
                }
                if (line == null)
                {
                    return;
                }
                pump.Append(line);
            }
        }

        /// <summary>
        /// 等任务结束（带窗口）——异常一律视为已结束。
        /// </summary>
        /// <param name="task">目标任务</param>
        /// <param name="waitMs">等待窗口毫秒</param>
        /// <returns>true = 已结束</returns>
        private static bool WaitQuiet(Task task, int waitMs)
        {
            try
            {
                return task.Wait(waitMs);
            }
            catch (Exception)
            {
                return true;
            }
        }

        /// <summary>
        /// 上限缓冲——与逐行追加同口径（行间 \n，达上限后停止增长并标记截断）。
        /// </summary>
        private sealed class Pump
        {
            /// <summary>字符上限</summary>
            private readonly int _cap;

            /// <summary>内容缓冲（与泵线程并发访问，读写均加锁）</summary>
            private readonly StringBuilder _buffer = new StringBuilder();

            /// <summary>是否已达上限被截断</summary>
            private bool _truncated = false;

            /// <summary>
            /// 构造——指定字符上限。
            /// </summary>
            /// <param name="cap">字符上限</param>
            public Pump(int cap)
            {
                _cap = cap;
            }

            /// <summary>是否已达上限被截断</summary>
            public bool Truncated
            {
                get
                {
                    return _truncated;
                }
            }

            /// <summary>当前内容快照</summary>
            public string Text
            {
                get
                {
                    lock (_buffer)
                    {
                        return _buffer.ToString();
                    }
                }
            }

            /// <summary>
            /// 追加一行——达上限后只标记不再增长。
            /// </summary>
            /// <param name="line">新增行</param>
            public void Append(string line)
            {
                lock (_buffer)
                {
                    if (_buffer.Length >= _cap)
                    {
                        _truncated = true;
                        return;
                    }
                    if (_buffer.Length > 0)
                    {
                        _buffer.Append('\n');
                    }
                    _buffer.Append(line);
                }
            }
        }
    }
}
