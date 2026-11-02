using CatHome4.QQ;
using Xunit;

namespace CatHome4.QQ.Tests
{
    /// <summary>
    /// QqInfoFormatter 测试——info 分类 JSON 块 → QQ Markdown 投影（可见根表格）。
    /// 覆盖：完整块 / 空说明占位与竖线转义 / 非 JSON 原样返回 / 空输入与缺分类不崩。
    /// </summary>
    public class QqInfoFormatterTests
    {
        /// <summary>
        /// 完整分类块——键值列表（版本含编译 / 端点双项 / 前文 / 加载包 / QQBot）+ 可见根表格。
        /// </summary>
        [Fact]
        public void FullBlock_ListsAndRootsTable()
        {
            string json = "{\n  \"ok\": true,\n  \"tool\": \"info\",\n  \"cat\": \"c1\",\n  \"version\": { \"version\": \"1.03.016\", \"build\": \"2026-09-18 20:11:22\" },\n  \"time\": { \"now\": \"2026-09-18 20:13:16\" },\n  \"llm\": { \"protocol\": \"opencode\", \"host\": \"opencode.ai\", \"model\": \"deepseek-v4.1-flash\", \"source\": \"猫绑定\" },\n  \"endpoint\": { \"chat\": \"http://127.0.0.1:8082\", \"panel\": \"http://127.0.0.1:8080\" },\n  \"roots\": [ { \"id\": \"WorkSpace\", \"writable\": true, \"note\": \"默认工作区域\" }, { \"id\": \"Data\", \"writable\": false, \"note\": \"\" } ],\n  \"tokens\": { \"context\": 223086 },\n  \"packs\": [ { \"key\": \"overwork\", \"desc\": \"收工加载包\" } ],\n  \"qqbot\": { \"usage\": \"Coder：发文件\" }\n}";
            string expected = "- **版本** 1.03.016 · 编译 2026-09-18 20:11:22\n"
                + "- **当前时间** 2026-09-18 20:13:16\n"
                + "- **LLM** opencode · opencode.ai · model=deepseek-v4.1-flash · 猫绑定\n"
                + "- **本地端点** 对话 http://127.0.0.1:8082 · 管理面板 http://127.0.0.1:8080\n"
                + "- **前文** 223086 tokens\n"
                + "- **加载包** overwork(收工加载包)\n"
                + "- **QQBot** Coder：发文件\n"
                + "\n"
                + "**可见根**\n"
                + "\n"
                + "| 根 | 读写 | 说明 |\n"
                + "| --- | --- | --- |\n"
                + "| WorkSpace | rw | 默认工作区域 |\n"
                + "| Data | ro | — |";
            Assert.Equal(expected, QqInfoFormatter.ToMarkdown(json));
        }

        /// <summary>
        /// 根标识与说明含竖线——表格单元格转义（不破格）。
        /// </summary>
        [Fact]
        public void RootNoteWithPipe_Escaped()
        {
            string json = "{\"roots\":[{\"id\":\"a|b\",\"writable\":true,\"note\":\"x|y\"}]}";
            string md = QqInfoFormatter.ToMarkdown(json);
            Assert.Contains("| a\\|b | rw | x\\|y |", md);
        }

        /// <summary>
        /// 非 JSON（旧格式 / 异常文本）——原样返回，失败可见（不静默丢内容）。
        /// </summary>
        [Fact]
        public void NonJson_ReturnedAsIs()
        {
            Assert.Equal("CH4 v1.0 | LLM: x", QqInfoFormatter.ToMarkdown("CH4 v1.0 | LLM: x"));
        }

        /// <summary>
        /// 空输入与缺分类块——空串 / 无内容行，不崩不产空行。
        /// </summary>
        [Fact]
        public void EmptyAndPartialBlock()
        {
            Assert.Equal("", QqInfoFormatter.ToMarkdown(""));
            Assert.Equal("", QqInfoFormatter.ToMarkdown(null));
            Assert.Equal("", QqInfoFormatter.ToMarkdown("{\"ok\":true,\"tool\":\"info\",\"cat\":\"c1\"}"));
        }

        /// <summary>
        /// 前文变动——tokens.lastContextChangeAt（绝对毫秒戳）投影为「距今」；缺字段 / 0 不显示（见 FullBlock 用例）。
        /// </summary>
        [Fact]
        public void TokensWithChangeAt_AppendsAgo()
        {
            long fiveMinAgo = System.DateTimeOffset.UtcNow.ToUnixTimeMilliseconds() - (5 * 60 + 10) * 1000;
            string json = "{\"tokens\":{\"context\":223086,\"lastContextChangeAt\":" + fiveMinAgo.ToString() + "}}";
            Assert.Equal("- **前文** 223086 tokens · 前文变动 5 分钟前", QqInfoFormatter.ToMarkdown(json));
        }
    }
}
