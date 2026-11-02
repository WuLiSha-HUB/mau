using System.Collections.Generic;
using System.Text;
using CatHome4.QQ;
using Xunit;

namespace CatHome4.QQ.Tests
{
    /// <summary>
    /// QqTextSplitter 测试（A58）——MD 结构感知切分边界。
    /// 覆盖：短文本单段 / 空输入 / 段落切分与保真 / 标题起块边界 / 代码块原子与超限补围栏 /
    /// 表格不可分割 / 超长单行硬切 / 纯空白不发送。
    /// </summary>
    public class QqTextSplitterTests
    {
        /// <summary>
        /// 短文本——单段原样返回（含【猫名：】前缀行）。
        /// </summary>
        [Fact]
        public void ShortText_SinglePart()
        {
            string text = "【majordomo：】\nhello world";
            List<string> parts = QqTextSplitter.Split(text, 1800);
            Assert.Single(parts);
            Assert.Equal(text, parts[0]);
        }

        /// <summary>
        /// 空输入 / 非法上限——空段列表（不产生空消息）。
        /// </summary>
        [Fact]
        public void EmptyInput_NoParts()
        {
            Assert.Empty(QqTextSplitter.Split("", 1800));
            Assert.Empty(QqTextSplitter.Split(null, 1800));
            Assert.Empty(QqTextSplitter.Split("abc", 0));
        }

        /// <summary>
        /// 纯空白文本——不切出空白段（Trim 后为空即丢弃）。
        /// </summary>
        [Fact]
        public void BlankText_NoParts()
        {
            Assert.Empty(QqTextSplitter.Split("\n\n\n", 1800));
        }

        /// <summary>
        /// 无空行长文——按行贪心打包，各段不超上限且按序拼接复现原文。
        /// </summary>
        [Fact]
        public void LongParagraph_PacksByLine_Lossless()
        {
            string text = "行一\n行二\n行三\n行四\n行五";
            List<string> parts = QqTextSplitter.Split(text, 8);
            Assert.Equal(2, parts.Count);
            Assert.Equal("行一\n行二\n行三", parts[0]);
            Assert.Equal("行四\n行五", parts[1]);
            Assert.Equal(text, string.Join("\n", parts));
        }

        /// <summary>
        /// 标题起块——标题行不与前一段正文黏连（装不下时标题单独成段）。
        /// </summary>
        [Fact]
        public void Heading_StartsOwnBlock()
        {
            string text = "前言一行\n# 标题\n标题下的正文";
            List<string> parts = QqTextSplitter.Split(text, 6);
            Assert.Equal(3, parts.Count);
            Assert.Equal("前言一行", parts[0]);
            Assert.Equal("# 标题", parts[1]);
            Assert.Equal("标题下的正文", parts[2]);
        }

        /// <summary>
        /// 代码块未超限——整块原子（不拆、不补围栏、原文保真）。
        /// </summary>
        [Fact]
        public void CodeBlock_WithinLimit_Atomic()
        {
            string text = "```csharp\nint a = 1;\nint b = 2;\n```";
            List<string> parts = QqTextSplitter.Split(text, 100);
            Assert.Single(parts);
            Assert.Equal(text, parts[0]);
        }

        /// <summary>
        /// 代码块超限——按行拆分，每段首重开围栏、尾补闭合围栏（各段独立可解析）。
        /// </summary>
        [Fact]
        public void CodeBlock_OverLimit_FenceRepaired()
        {
            StringBuilder sb = new StringBuilder();
            sb.Append("```csharp\n");
            sb.Append(new string('x', 60));
            sb.Append("\n");
            sb.Append(new string('y', 60));
            sb.Append("\n```");
            string text = sb.ToString();
            List<string> parts = QqTextSplitter.Split(text, 50);
            Assert.True(parts.Count >= 2);
            for (int i = 0; i < parts.Count; i = i + 1)
            {
                Assert.StartsWith("```csharp", parts[i]);
                Assert.EndsWith("```", parts[i]);
                Assert.True(parts[i].Length <= 50);
            }
        }

        /// <summary>
        /// 表格超限——不可分割项：整表独占一段（原样，不拆行）。
        /// </summary>
        [Fact]
        public void Table_OverLimit_KeptAsOnePart()
        {
            string table = "| 列一 | 列二 |\n| --- | --- |\n| 甲 | 乙 |";
            List<string> parts = QqTextSplitter.Split(table, 10);
            Assert.Single(parts);
            Assert.Equal(table, parts[0]);
        }

        /// <summary>
        /// 表格装得下——与前文同段（原子块参与打包，不强制独占）。
        /// </summary>
        [Fact]
        public void Table_WithinLimit_PackedWithLeadingText()
        {
            string text = "前言一行\n| a | b |\n| 1 | 2 |";
            List<string> parts = QqTextSplitter.Split(text, 100);
            Assert.Single(parts);
            Assert.Equal(text, parts[0]);
        }

        /// <summary>
        /// 表格与前文（表格自身超限）——不可拆：整表原样独占一段，前文单独一段。
        /// </summary>
        [Fact]
        public void TableWithLeadingText_OverLimit_TableOwnPart()
        {
            string text = "前言\n| a | b |\n| 1 | 2 |";
            List<string> parts = QqTextSplitter.Split(text, 12);
            Assert.Equal(2, parts.Count);
            Assert.Equal("前言", parts[0]);
            Assert.Equal("| a | b |\n| 1 | 2 |", parts[1]);
        }

        /// <summary>
        /// 段数不膨胀——标题 / 表格 / 多段正文在总量未超限时合成单段（防"每块各成一段"导致切分段数虚高、预算被浪费）。
        /// </summary>
        [Fact]
        public void StructuralBlocks_PackedTogether_NoPartInflation()
        {
            StringBuilder sb = new StringBuilder();
            sb.Append("## 样本");
            sb.Append("\n");
            sb.Append("前言一行。");
            sb.Append("\n\n");
            sb.Append("| 观察项 | 期望 |");
            sb.Append("\n|:--|:--|");
            sb.Append("\n| 收到条数 | 恰好 2 条 |");
            sb.Append("\n\n");
            sb.Append("素材一第一段。");
            sb.Append("\n\n");
            sb.Append("素材一第二段。");
            string text = sb.ToString();
            List<string> parts = QqTextSplitter.Split(text, 1800);
            Assert.Single(parts);
            Assert.Equal(text, parts[0]);
        }

        /// <summary>
        /// 超长单行——按上限硬切（最后一段为余数）。
        /// </summary>
        [Fact]
        public void LongSingleLine_HardSplit()
        {
            string text = new string('a', 25);
            List<string> parts = QqTextSplitter.Split(text, 10);
            Assert.Equal(3, parts.Count);
            Assert.Equal(10, parts[0].Length);
            Assert.Equal(10, parts[1].Length);
            Assert.Equal(5, parts[2].Length);
        }

        /// <summary>
        /// 混合结构未超限——单段且原文保真（空行与列表标记原样保留）。
        /// </summary>
        [Fact]
        public void MixedStructure_FitsInOnePart_Lossless()
        {
            string text = "# 标题\n正文一\n\n正文二\n\n- 列表一\n- 列表二";
            List<string> parts = QqTextSplitter.Split(text, 1800);
            Assert.Single(parts);
            Assert.Equal(text, parts[0]);
        }

        /// <summary>
        /// 混合结构分段——各段不超上限且均非空白（保真只对无空行切点成立）。
        /// </summary>
        [Fact]
        public void MixedStructure_MultiPart_WithinLimit()
        {
            string text = "# 标题\n正文一\n\n正文二\n\n- 列表一\n- 列表二";
            List<string> parts = QqTextSplitter.Split(text, 14);
            Assert.True(parts.Count > 1);
            for (int i = 0; i < parts.Count; i = i + 1)
            {
                Assert.True(parts[i].Length <= 14);
                Assert.True(parts[i].Trim().Length > 0);
            }
            // 结构单元不被拆散——标题行落在段首（未黏连前文）
            bool headingAtPartStart = false;
            for (int i = 0; i < parts.Count; i = i + 1)
            {
                if (parts[i].StartsWith("# 标题", System.StringComparison.Ordinal))
                {
                    headingAtPartStart = true;
                }
            }
            Assert.True(headingAtPartStart);
        }
    }
}
