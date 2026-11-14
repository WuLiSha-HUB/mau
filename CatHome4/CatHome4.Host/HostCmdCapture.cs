using System;
using System.IO;
using System.Text;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// Program 控制台输出落盘分部（原 Program.Shell.cs 段5，P2 拆分时随内核留在 Host）。
    /// 边界：非交互模式下把 Console 输出双写到该实例自己的数据根：WinExe 无控制台，shell 直调取不到 stdout；
    /// 父进程显式重定向（SetUp / mau-setup）的捕获链路不受影响——本分部只增落盘副本。
    /// </summary>
    public static partial class Program
    {
        // [段5] 非交互输出落盘
        /// <summary>控制台输出落盘写器——null=未启用</summary>
        private static StreamWriter _cmdCaptureWriter;

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
                string runDir = Path.Combine(dataRoot, "Data", "runs");
                Directory.CreateDirectory(runDir);
                string file = Path.Combine(runDir, "cmd_" + DateTime.Now.ToString("yyyyMMdd_HHmmss") + ".txt");
                StreamWriter writer = new StreamWriter(file, false, new UTF8Encoding(false));
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
        private sealed class TeeTextWriter : TextWriter
        {
            /// <summary>主输出（stdout / 父进程重定向管道）</summary>
            private readonly TextWriter _primary;

            /// <summary>副本输出（落盘）</summary>
            private readonly TextWriter _secondary;

            /// <summary>写锁——跨线程写安全</summary>
            private readonly object _writeLock = new object();

            /// <summary>
            /// 构造双写器。
            /// </summary>
            /// <param name="primary">主输出</param>
            /// <param name="secondary">副本输出</param>
            public TeeTextWriter(TextWriter primary, TextWriter secondary)
            {
                _primary = primary;
                _secondary = secondary;
            }

            /// <summary>输出编码——沿用主输出</summary>
            public override Encoding Encoding
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
    }
}
