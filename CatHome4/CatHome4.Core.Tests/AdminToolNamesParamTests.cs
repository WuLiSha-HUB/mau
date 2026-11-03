using System;
using System.Text.Json;
using CatHome4.Admin;
using Xunit;

namespace CatHome4.Core.Tests
{
    /// <summary>
    /// 工具名单形参解析测试（A310 / 缺陷 T25）——数组与字符串两形态皆收；非法形态出声不静默。
    /// </summary>
    public class AdminToolNamesParamTests
    {
        /// <summary>
        /// 字符串数组形态——按序逗号拼接；空白项与空串跳过（数组形态不再被吞成空串）。
        /// </summary>
        [Fact]
        public void TryReadToolNamesParam_Array_Joined()
        {
            using (JsonDocument doc = JsonDocument.Parse("{\"toolNames\":[\"text-read\",\" cs-read \",\"\"]}"))
            {
                string joined;
                string error;
                bool ok = AdminService.TryReadToolNamesParam(doc.RootElement, out joined, out error);
                Assert.True(ok);
                Assert.Equal("", error);
                Assert.Equal("text-read,cs-read", joined);
            }
        }

        /// <summary>
        /// 逗号分隔字符串形态——原样透传（既有调用方行为不变）。
        /// </summary>
        [Fact]
        public void TryReadToolNamesParam_String_PassedThrough()
        {
            using (JsonDocument doc = JsonDocument.Parse("{\"toolNames\":\"text-read,file-tree\"}"))
            {
                string joined;
                string error;
                bool ok = AdminService.TryReadToolNamesParam(doc.RootElement, out joined, out error);
                Assert.True(ok);
                Assert.Equal("", error);
                Assert.Equal("text-read,file-tree", joined);
            }
        }

        /// <summary>
        /// 空数组——合法，结果为空串（空 = 全量语义由落盘侧解析，不在形参层猜测）。
        /// </summary>
        [Fact]
        public void TryReadToolNamesParam_EmptyArray_EmptyJoined()
        {
            using (JsonDocument doc = JsonDocument.Parse("{\"toolNames\":[]}"))
            {
                string joined;
                string error;
                bool ok = AdminService.TryReadToolNamesParam(doc.RootElement, out joined, out error);
                Assert.True(ok);
                Assert.Equal("", error);
                Assert.Equal("", joined);
            }
        }

        /// <summary>
        /// 数组含非字符串项——拒绝并给出带序号的原因（不静默跳过该项）。
        /// </summary>
        [Fact]
        public void TryReadToolNamesParam_ArrayNonString_Rejected()
        {
            using (JsonDocument doc = JsonDocument.Parse("{\"toolNames\":[\"text-read\",1]}"))
            {
                string joined;
                string error;
                bool ok = AdminService.TryReadToolNamesParam(doc.RootElement, out joined, out error);
                Assert.False(ok);
                Assert.Contains("第 2 项", error, StringComparison.Ordinal);
            }
        }

        /// <summary>
        /// 非法形态（数字 / 对象 / null）——一律拒绝并出声，不再回落空串。
        /// </summary>
        [Fact]
        public void TryReadToolNamesParam_InvalidKind_Rejected()
        {
            string[] payloads = new string[] { "{\"toolNames\":123}", "{\"toolNames\":{\"a\":1}}", "{\"toolNames\":null}" };
            for (int i = 0; i < payloads.Length; i = i + 1)
            {
                using (JsonDocument doc = JsonDocument.Parse(payloads[i]))
                {
                    string joined;
                    string error;
                    bool ok = AdminService.TryReadToolNamesParam(doc.RootElement, out joined, out error);
                    Assert.False(ok);
                    Assert.NotEqual("", error);
                }
            }
        }

        /// <summary>
        /// 缺字段——合法且结果为空串（字段级合并写：缺省即保留旧值，由 present 判定）。
        /// </summary>
        [Fact]
        public void TryReadToolNamesParam_Missing_Empty()
        {
            using (JsonDocument doc = JsonDocument.Parse("{\"cat\":\"chat\"}"))
            {
                string joined;
                string error;
                bool ok = AdminService.TryReadToolNamesParam(doc.RootElement, out joined, out error);
                Assert.True(ok);
                Assert.Equal("", error);
                Assert.Equal("", joined);
            }
        }
    }
}
