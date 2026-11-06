using System.Collections.Generic;
using Xunit;

namespace CatHome4.Core.Tests
{
    /// <summary>
    /// ViewCardPayload 测试——工具卡载荷构造内核（四处拼装点收口）的字段契约。
    /// 覆盖：七字段齐备 · order 与档位表同源（关系断言，不硬编码档位值）· null result = 先行卡（不写字段）· 空串 result 仍写字段。
    /// 动机：内核就是「字段漏一半」这类事故的止血点——契约由测试钉住，散落拼装不可能再复发。
    /// A165：运行时长由块字段改为载荷字段（`durMs`），字段数 6 → 7。
    /// 2026-11-06：两段计时拆出 `queueMs` / `runMs`（排队 / 执行），字段数 7 → 9——`durMs` 保留为总时长（向后兼容）。
    /// </summary>
    public sealed class ViewCardPayloadTests
    {
        /// <summary>七字段齐备 + order 与档位表同源——实时终态卡形态</summary>
        [Fact]
        public void BuildToolCard_CarriesAllFields_AndOrderFromTable()
        {
            Dictionary<string, object> payload = CH4.ViewCardPayload.BuildToolCard("cs-build", "{}", "OK", 2, 3, 250);
            Assert.Equal("cs-build", payload["name"]);
            Assert.Equal("{}", payload["arguments"]);
            Assert.Equal("OK", payload["result"]);
            Assert.Equal(2, (int)payload["toolIndex"]);
            Assert.Equal(3, (int)payload["toolTotal"]);
            Assert.Equal(CH4.ToolOrderTable.OrderText("cs-build"), payload["order"]);
            Assert.Equal(250L, (long)payload["durMs"]);
            // 两段计时（2026-11-06）——缺省未观测：两值 -1，字段仍在（前端按 -1 回落总时长）
            Assert.Equal(-1L, (long)payload["queueMs"]);
            Assert.Equal(-1L, (long)payload["runMs"]);
            Assert.Equal(9, payload.Count);
        }

        /// <summary>null result = 先行卡——不写 result 字段（前端按处理中渲染）；durMs 缺省 -1</summary>
        [Fact]
        public void BuildToolCard_NullResult_OmitsResultKey()
        {
            Dictionary<string, object> payload = CH4.ViewCardPayload.BuildToolCard("cs-build", "{}", null, 1, 1);
            Assert.False(payload.ContainsKey("result"));
            Assert.Equal(-1L, (long)payload["durMs"]);
            Assert.Equal(8, payload.Count);
        }

        /// <summary>空串 result 是合法结果——写字段（与先行卡区分：落成空结果文本而非处理中）</summary>
        [Fact]
        public void BuildToolCard_EmptyResult_KeepsResultKey()
        {
            Dictionary<string, object> payload = CH4.ViewCardPayload.BuildToolCard("cs-build", "{}", "", 1, 1);
            Assert.True(payload.ContainsKey("result"));
            Assert.Equal("", payload["result"]);
            Assert.Equal(9, payload.Count);
        }
        /// <summary>思考块载荷——text / durMs / chars / cps 四字段齐备（cps = 字符数 ÷ 用时，保留一位小数）</summary>
        [Fact]
        public void BuildReason_CarriesCounts_WithCpsFromDuration()
        {
            Dictionary<string, object> payload = CH4.ViewCardPayload.BuildReason("一二三四五", 500);
            Assert.Equal("一二三四五", payload["text"]);
            Assert.Equal(500L, (long)payload["durMs"]);
            Assert.Equal(5, (int)payload["chars"]);
            Assert.Equal(10.0, (double)payload["cps"]);
            Assert.Equal(4, payload.Count);
        }
        /// <summary>用时未记录（载入顶尾补差路径）——durMs / cps 取 -1；chars 由正文算出，恒有值</summary>
        [Fact]
        public void BuildReason_UnknownDuration_CpsUnavailable()
        {
            Dictionary<string, object> payload = CH4.ViewCardPayload.BuildReason("想想", -1);
            Assert.Equal(-1L, (long)payload["durMs"]);
            Assert.Equal(2, (int)payload["chars"]);
            Assert.Equal(-1.0, (double)payload["cps"]);
        }
        /// <summary>用时为 0——比值不成立取 -1（不出 Infinity / NaN）；非整除法取一位小数</summary>
        [Fact]
        public void BuildReason_ZeroDuration_CpsUnavailable()
        {
            Dictionary<string, object> zero = CH4.ViewCardPayload.BuildReason("想想", 0);
            Assert.Equal(0L, (long)zero["durMs"]);
            Assert.Equal(-1.0, (double)zero["cps"]);
            Dictionary<string, object> rounded = CH4.ViewCardPayload.BuildReason("abc", 700);
            Assert.Equal(4.3, (double)rounded["cps"]);
        }
    }
}
