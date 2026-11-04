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
            // 超时窗口放宽到 3 s——ready 握手 + 慢回执都要在窗口内可控（400 ms 在并行跑测下
            // 连 node 冷启动都撑不住，会把「启动超时」误判成「迟到回执」用例）。
            StdioServiceHost host = NewHost(3000);
            try
            {
                // 慢回执延迟 3400 ms > 超时窗口 3000 ms → 首次调用必走 TIMEOUT 分支
                string first = host.Invoke("{\"id\":\"1\",\"op\":\"slow\",\"delayMs\":3400}");
                Assert.Contains("TIMEOUT", first);
                // 第二次调用：桩以 setTimeout 延迟回执（不阻塞事件循环）→ id=2 立即可回；
                // id=1 的迟到回执若在读取窗口内到达，其 id 不符 → 必须被丢弃（不得当成本次结果）
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
