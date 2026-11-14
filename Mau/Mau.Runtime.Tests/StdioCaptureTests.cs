using System;
using System.Diagnostics;
using System.Runtime.InteropServices;
using Xunit;

namespace Mau.Runtime.Tests
{
    /// <summary>
    /// StdioCapture 采集单点测试（A365）——三条 stdio 链共用的读流实现：
    /// 不丢行 / 空输出与丢输出可区分 / 上限截断 / 超时杀树（判据须能变红：旧实现下 InvariantStable 会周期性读空）。
    /// </summary>
    public class StdioCaptureTests
    {
        /// <summary>宽上限（正例用例不受截断干扰）</summary>
        private const int WideCap = 65536;

        /// <summary>日志标签</summary>
        private const string Tag = "StdioCaptureTest";

        /// <summary>
        /// 启动一个 shell 子进程——Windows = cmd /c，其它平台 = /bin/bash -lc。
        /// </summary>
        /// <param name="script">命令文本</param>
        /// <returns>已启动（尚未采集）的进程</returns>
        private static Process StartShell(string script)
        {
            ProcessStartInfo psi = new ProcessStartInfo();
            if (RuntimeInformation.IsOSPlatform(OSPlatform.Windows))
            {
                psi.FileName = "cmd.exe";
                psi.ArgumentList.Add("/c");
            }
            else
            {
                psi.FileName = "/bin/bash";
                psi.ArgumentList.Add("-lc");
            }
            psi.ArgumentList.Add(script);
            psi.UseShellExecute = false;
            psi.CreateNoWindow = true;
            psi.RedirectStandardOutput = true;
            psi.RedirectStandardError = true;
            Process proc = new Process();
            proc.StartInfo = psi;
            proc.Start();
            return proc;
        }

        /// <summary>
        /// 生成「输出 count 行、每行 prefix + 序号」的命令文本。
        /// </summary>
        /// <param name="prefix">行前缀</param>
        /// <param name="count">行数</param>
        /// <returns>命令文本</returns>
        private static string BuildEchoScript(string prefix, int count)
        {
            if (RuntimeInformation.IsOSPlatform(OSPlatform.Windows))
            {
                return "for /l %i in (1,1," + count.ToString() + ") do @echo " + prefix + "%i";
            }
            return "for i in $(seq 1 " + count.ToString() + "); do echo " + prefix + "$i; done";
        }

        /// <summary>
        /// 生成「无任何输出、退出码 0」的命令文本。
        /// </summary>
        /// <returns>命令文本</returns>
        private static string BuildSilentScript()
        {
            if (RuntimeInformation.IsOSPlatform(OSPlatform.Windows))
            {
                return "exit /b 0";
            }
            return "true";
        }

        /// <summary>
        /// 生成「长时间无输出」的命令文本（超时用例专用）。
        /// </summary>
        /// <param name="seconds">时长（秒）</param>
        /// <returns>命令文本</returns>
        private static string BuildSleepScript(int seconds)
        {
            if (RuntimeInformation.IsOSPlatform(OSPlatform.Windows))
            {
                return "ping -n " + seconds.ToString() + " 127.0.0.1 > nul";
            }
            return "sleep " + seconds.ToString();
        }

        /// <summary>
        /// 整树终止回调——与各面注入的实现同语义（超时分支调用）。
        /// </summary>
        /// <param name="target">目标进程</param>
        private static void KillAll(Process target)
        {
            target.Kill(true);
        }

        /// <summary>
        /// 全量采集——200 行逐行不丢，首末行内容与行数一致。
        /// </summary>
        [Fact]
        public void DrainCapturesEveryLine()
        {
            Process proc = StartShell(BuildEchoScript("line", 200));
            StdioCaptureResult capture = StdioCapture.Drain(proc, 30000, WideCap, WideCap, KillAll, Tag);
            Assert.True(capture.Drained, "采集未排空（收尾窗口用尽）");
            Assert.False(capture.Timeout, "不应超时");
            Assert.Equal(0, capture.ExitCode);
            string[] lines = capture.Stdout.Split('\n');
            Assert.True(lines.Length == 200, "行数不符：期望 200，实际 " + lines.Length.ToString());
            Assert.Equal("line1", lines[0]);
            Assert.Equal("line200", lines[199]);
        }

        /// <summary>
        /// 空输出与丢输出必须可区分——无输出的命令读到空串且已排空，退出码正常。
        /// </summary>
        [Fact]
        public void DrainEmptyOutputIsDistinguishable()
        {
            Process proc = StartShell(BuildSilentScript());
            StdioCaptureResult capture = StdioCapture.Drain(proc, 30000, WideCap, WideCap, KillAll, Tag);
            Assert.True(capture.Drained, "无输出命令也应排空");
            Assert.False(capture.Timeout, "不应超时");
            Assert.Equal(0, capture.ExitCode);
            Assert.Equal("", capture.Stdout);
            Assert.Equal("", capture.Stderr);
        }

        /// <summary>
        /// 稳定性——同一命令连跑 20 次，每次都必须拿到输出（用户判据：旧实现 15 次丢 7 次）。
        /// </summary>
        [Fact]
        public void DrainIsStableAcrossTwentyRuns()
        {
            // [段1] 逐次采集 + 逐次断言——空输出、行数、首行任一不符即失败并指出第几次
            for (int round = 1; round <= 20; round = round + 1)
            {
                Process proc = StartShell(BuildEchoScript("stable", 3));
                StdioCaptureResult capture = StdioCapture.Drain(proc, 30000, WideCap, WideCap, KillAll, Tag);
                string where = "第 " + round.ToString() + " 次采集——";
                Assert.True(capture.Drained, where + "未排空");
                Assert.True(capture.Stdout.Length > 0, where + "输出为空（疑似丢流）");
                string[] lines = capture.Stdout.Split('\n');
                Assert.True(lines.Length == 3, where + "行数 " + lines.Length.ToString() + " 而非 3");
                Assert.Equal("stable1", lines[0]);
            }
        }

        /// <summary>
        /// 上限截断——达上限后置截断标记，内容不再增长（整行粒度，最多超出一行）。
        /// </summary>
        [Fact]
        public void DrainTruncatesAtCap()
        {
            Process proc = StartShell(BuildEchoScript("abcdefghij", 50));
            StdioCaptureResult capture = StdioCapture.Drain(proc, 30000, 25, 25, KillAll, Tag);
            Assert.True(capture.OutTruncated, "达上限未标记截断");
            Assert.True(capture.Stdout.Length <= 40, "截断后内容超量：" + capture.Stdout.Length.ToString());
        }

        /// <summary>
        /// 超时——超时分支调用终止回调、退出码置 -1、标记 Timeout（不静默当成正常退出）。
        /// </summary>
        [Fact]
        public void DrainTimesOutAndKillsTree()
        {
            int killed = 0;
            Process proc = StartShell(BuildSleepScript(10));
            StdioCaptureResult capture = StdioCapture.Drain(proc, 300, WideCap, WideCap, delegate (Process target) { killed = killed + 1; KillAll(target); }, Tag);
            Assert.True(capture.Timeout, "应为超时");
            Assert.True(killed == 1, "终止回调调用次数：" + killed.ToString());
            Assert.Equal(-1, capture.ExitCode);
        }
    }
}
