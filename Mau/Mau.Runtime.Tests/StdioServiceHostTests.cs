using System;
using System.IO;
using Xunit;

namespace Mau.Runtime.Tests
{
    /// <summary>
    /// StdioServiceHost 协议往返测试——超时与迟到回执处置（A292 T8）。
    /// </summary>
    public class StdioServiceHostTests
    {
        /// <summary>
        /// 桩脚本路径——fixtures/stub_slow_service.js（可控慢回执）
        /// </summary>
        /// <returns>绝对路径</returns>
        private static string StubScript()
        {
            string baseDir = AppDomain.CurrentDomain.BaseDirectory;
            return Path.GetFullPath(Path.Combine(baseDir, "..", "..", "..", "fixtures", "stub_slow_service.js"));
        }

        /// <summary>
        /// 新建测试用服务宿主
        /// </summary>
        /// <param name="timeoutMs">单次调用超时</param>
        /// <returns>宿主</returns>
        private static StdioServiceHost NewHost(int timeoutMs)
        {
            string scriptPath = StubScript();
            Assert.True(File.Exists(scriptPath), "桩脚本缺失：" + scriptPath);
            string workDir = Path.GetDirectoryName(scriptPath) ?? ".";
            return new StdioServiceHost("stub", "node", scriptPath, workDir, timeoutMs, "", "", "", "NODE", "StdioStubTest", 5);
        }

        /// <summary>
        /// 正常往返——回执是本次请求的
        /// </summary>
        [Fact]
        public void InvokeNormalRoundTripReturnsOwnReply()
        {
            StdioServiceHost host = NewHost(5000);
            try
            {
                string reply = host.Invoke("{\"id\":\"a1\",\"op\":\"echo\"}");
                Assert.Contains("\"id\":\"a1\"", reply);
            }
            finally
            {
                host.Stop();
            }
        }

        /// <summary>
        /// 超时后的迟到回执不得被下一次调用当成自己的结果（串行语义下它必属上一次请求）
        /// </summary>
        [Fact]
        public void InvokeLateReplyIsNotTakenAsNextReply()
        {
            // 超时预算按「2 核负载下 node 冷启动」放宽——原先 400 ms 同时兼任 ready 握手超时，
            // 并行跑测时 node 启动超过它即误判 START_FAILED（Windows 单机少见，Linux 2 核实测必现偶发）
            StdioServiceHost host = NewHost(2000);
            try
            {
                string first = host.Invoke("{\"id\":\"1\",\"op\":\"slow\",\"delayMs\":3000}");
                Assert.Contains("TIMEOUT", first);
                string second = host.Invoke("{\"id\":\"2\",\"op\":\"echo\"}");
                Assert.Contains("\"id\":\"2\"", second);
            }
            finally
            {
                host.Stop();
            }
        }
    }
}
