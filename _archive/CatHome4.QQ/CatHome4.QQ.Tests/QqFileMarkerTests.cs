using System.Collections.Generic;
using CatHome4.QQ;
using Xunit;

namespace CatHome4.QQ.Tests
{
    /// <summary>
    /// QqFileMarker 测试（A58）——强匹配文件标记解析边界。
    /// 覆盖：独占行正例 / 行首尾空白 / 多文件 / 空行合并 / 无标记原样 /
    /// 空输入 / 负例（旧格式、工具输出形态、行内引用、缺引号、路径含引号）。
    /// </summary>
    public class QqFileMarkerTests
    {
        /// <summary>
        /// 独占行标记——提取路径，正文移除标记行（相邻空行合并）。
        /// </summary>
        [Fact]
        public void MarkerOnOwnLine_Extracted_BodyCleaned()
        {
            string text = "正文一\n\n[QQBot发送文件:\"C:\\a\\b.png\"]\n\n正文二";
            string body;
            List<string> files = QqFileMarker.Extract(text, out body);
            Assert.Single(files);
            Assert.Equal("C:\\a\\b.png", files[0]);
            Assert.Equal("正文一\n\n正文二", body);
        }

        /// <summary>
        /// 行首尾空白——仍视为独占行（宽松空白，严格形态）。
        /// </summary>
        [Fact]
        public void MarkerWithSurroundingSpaces_Extracted()
        {
            string text = "  [QQBot发送文件:\"C:\\a.txt\"]  ";
            string body;
            List<string> files = QqFileMarker.Extract(text, out body);
            Assert.Single(files);
            Assert.Equal("C:\\a.txt", files[0]);
            Assert.Equal("", body);
        }

        /// <summary>
        /// 多文件——多行各一标记，按出现顺序收集。
        /// </summary>
        [Fact]
        public void MultipleMarkers_CollectedInOrder()
        {
            string text = "甲\n[QQBot发送文件:\"C:\\1.txt\"]\n[QQBot发送文件:\"D:\\目录\\2.png\"]\n乙";
            string body;
            List<string> files = QqFileMarker.Extract(text, out body);
            Assert.Equal(2, files.Count);
            Assert.Equal("C:\\1.txt", files[0]);
            Assert.Equal("D:\\目录\\2.png", files[1]);
            Assert.Equal("甲\n乙", body);
        }

        /// <summary>
        /// 无标记——正文 Trim 后原样返回，不改写内容。
        /// </summary>
        [Fact]
        public void NoMarker_BodyTrimmedUnchanged()
        {
            string text = "  正文内容\n第二行  ";
            string body;
            List<string> files = QqFileMarker.Extract(text, out body);
            Assert.Empty(files);
            Assert.Equal(text.Trim(), body);
        }

        /// <summary>
        /// 空输入——空列表 + 空正文。
        /// </summary>
        [Fact]
        public void EmptyInput_NoFiles()
        {
            string body;
            Assert.Empty(QqFileMarker.Extract("", out body));
            Assert.Equal("", body);
            Assert.Empty(QqFileMarker.Extract(null, out body));
            Assert.Equal("", body);
        }

        /// <summary>
        /// 负例——旧宽匹配格式（`[文件:path]`）不再触发。
        /// </summary>
        [Fact]
        public void LegacyMarker_NotExtracted()
        {
            string text = "[文件:path]";
            string body;
            List<string> files = QqFileMarker.Extract(text, out body);
            Assert.Empty(files);
            Assert.Equal("[文件:path]", body);
        }

        /// <summary>
        /// 负例——cs-read 工具输出形态（含 `[文件: <路径> L10-20]`）不再触发。
        /// </summary>
        [Fact]
        public void ToolOutputForm_NotExtracted()
        {
            string text = "正文\n[文件: C:\\repo\\x.cs L1271-1287]\nint a = 1;";
            string body;
            List<string> files = QqFileMarker.Extract(text, out body);
            Assert.Empty(files);
            Assert.Equal(text.Trim(), body);
        }

        /// <summary>
        /// 负例——行内引用（非独占行）不触发。
        /// </summary>
        [Fact]
        public void InlineReference_NotExtracted()
        {
            string text = "要发文件请写 [QQBot发送文件:\"C:\\a.txt\"] 这一行";
            string body;
            List<string> files = QqFileMarker.Extract(text, out body);
            Assert.Empty(files);
            Assert.Equal(text.Trim(), body);
        }

        /// <summary>
        /// 负例——缺引号（形态不符）不触发。
        /// </summary>
        [Fact]
        public void MissingQuotes_NotExtracted()
        {
            string text = "[QQBot发送文件:C:\\a.txt]";
            string body;
            List<string> files = QqFileMarker.Extract(text, out body);
            Assert.Empty(files);
            Assert.Equal(text.Trim(), body);
        }

        /// <summary>
        /// 负例——路径含引号（形态不符）不触发。
        /// </summary>
        [Fact]
        public void QuotedPath_NotExtracted()
        {
            string text = "[QQBot发送文件:\"C:\\a\"b.txt\"]";
            string body;
            List<string> files = QqFileMarker.Extract(text, out body);
            Assert.Empty(files);
            Assert.Equal(text.Trim(), body);
        }
    }
}
