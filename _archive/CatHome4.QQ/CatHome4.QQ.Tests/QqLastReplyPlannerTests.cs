using System.Collections.Generic;
using CatHome4.QQ;
using Xunit;

namespace CatHome4.QQ.Tests
{
    /// <summary>
    /// QqLastReplyPlanner 测试（A112 / A216）——/last 发送计划：末尾优先取段 + 文本段 / 文件 / 图片共用被动调用预算。
    /// 覆盖：短正文 + 文件 / 纯标记（正文剥离后为空）/ 无附件 / 段数超限取末尾 /
    /// 段与文件共用预算 / 段用满预算 / 图片张数上限（A216）/ 段与图片共用预算 / 文件先占额度 / 空输入。
    /// </summary>
    public sealed class QqLastReplyPlannerTests
    {
        /// <summary>构造附件路径列表</summary>
        /// <param name="paths">路径数组</param>
        /// <returns>路径列表</returns>
        private static List<string> Paths(params string[] paths)
        {
            List<string> list = new List<string>();
            for (int i = 0; i < paths.Length; i = i + 1)
            {
                list.Add(paths[i]);
            }
            return list;
        }

        /// <summary>五行正文（每行恰好等于 limit——切分后每行独立成段）</summary>
        /// <returns>五行文本</returns>
        private static string FiveLines()
        {
            return "AAAAA\nBBBBB\nCCCCC\nDDDDD\nEEEEE";
        }

        /// <summary>短正文 + 单文件——一段 + 一文件，各占一次预算，无丢弃。</summary>
        [Fact]
        public void ShortBodyAndFile_BothPlanned()
        {
            QqLastReplyPlan plan = QqLastReplyPlanner.Plan("正文", Paths("C:\\a.png"), null, 1800, 4, 3);
            Assert.Single(plan.Segments);
            Assert.Equal("正文", plan.Segments[0]);
            Assert.Single(plan.Files);
            Assert.Equal("C:\\a.png", plan.Files[0]);
            Assert.Equal(0, plan.DroppedHeadSegments);
            Assert.Equal(0, plan.DroppedFiles);
        }

        /// <summary>纯标记（正文剥离后为空）——无文本段（不发空消息），文件照发。</summary>
        [Fact]
        public void EmptyBody_FileOnly_NoEmptySegment()
        {
            QqLastReplyPlan plan = QqLastReplyPlanner.Plan("", Paths("C:\\a.png"), null, 1800, 4, 3);
            Assert.Empty(plan.Segments);
            Assert.Single(plan.Files);
            Assert.Equal(0, plan.DroppedFiles);
        }

        /// <summary>无附件——只出文本段（null 与空列表同效）。</summary>
        [Fact]
        public void NoFiles_SegmentsOnly()
        {
            QqLastReplyPlan plan = QqLastReplyPlanner.Plan("正文", null, null, 1800, 4, 3);
            Assert.Single(plan.Segments);
            Assert.Empty(plan.Files);
            Assert.Empty(plan.Images);
            QqLastReplyPlan plan2 = QqLastReplyPlanner.Plan("正文", Paths(), Paths(), 1800, 4, 3);
            Assert.Single(plan2.Segments);
            Assert.Empty(plan2.Files);
            Assert.Empty(plan2.Images);
        }

        /// <summary>段数超上限——取末尾段（补感知语义：尾部最新），开头段计入丢弃。</summary>
        [Fact]
        public void TooManySegments_TakesTail()
        {
            QqLastReplyPlan plan = QqLastReplyPlanner.Plan(FiveLines(), null, null, 5, 4, 3);
            Assert.Equal(4, plan.Segments.Count);
            Assert.Equal("BBBBB", plan.Segments[0]);
            Assert.Equal("EEEEE", plan.Segments[3]);
            Assert.Equal(1, plan.DroppedHeadSegments);
        }

        /// <summary>段与文件共用预算——三段正文占 3 次，文件只余 1 次额度，超出丢弃并计数。</summary>
        [Fact]
        public void SegmentsAndFiles_ShareBudget()
        {
            string body = "AAAAA\nBBBBB\nCCCCC";
            QqLastReplyPlan plan = QqLastReplyPlanner.Plan(body, Paths("C:\\1.png", "C:\\2.png", "C:\\3.png"), null, 5, 4, 3);
            Assert.Equal(3, plan.Segments.Count);
            Assert.Single(plan.Files);
            Assert.Equal("C:\\1.png", plan.Files[0]);
            Assert.Equal(2, plan.DroppedFiles);
        }

        /// <summary>段用满预算——文件全丢并计数（失败可见：调用方据此 L2 留痕）。</summary>
        [Fact]
        public void SegmentsFillBudget_AllFilesDropped()
        {
            QqLastReplyPlan plan = QqLastReplyPlanner.Plan(FiveLines(), Paths("C:\\1.png"), null, 5, 4, 3);
            Assert.Equal(4, plan.Segments.Count);
            Assert.Empty(plan.Files);
            Assert.Equal(1, plan.DroppedFiles);
        }

        /// <summary>图片张数上限——预算足够（空正文）时仍最多 3 张，超出丢弃并计数（A216）。</summary>
        [Fact]
        public void Images_CappedByMaxImages()
        {
            QqLastReplyPlan plan = QqLastReplyPlanner.Plan("", null, Paths("C:\\1.png", "C:\\2.png", "C:\\3.png", "C:\\4.png"), 1800, 4, 3);
            Assert.Empty(plan.Segments);
            Assert.Equal(3, plan.Images.Count);
            Assert.Equal(1, plan.DroppedImages);
        }

        /// <summary>段与图片共用预算——1 段正文占 1 次，图片余 3 次额度（常见形态 1 文本 + 3 图片）。</summary>
        [Fact]
        public void SegmentAndImages_ShareBudget()
        {
            QqLastReplyPlan plan = QqLastReplyPlanner.Plan("正文", null, Paths("C:\\1.png", "C:\\2.png", "C:\\3.png"), 1800, 4, 3);
            Assert.Single(plan.Segments);
            Assert.Equal(3, plan.Images.Count);
            Assert.Equal(0, plan.DroppedImages);
        }

        /// <summary>文件先占额度——1 段 + 2 文件占 3 次，图片仅余 1 次，超出丢弃并计数。</summary>
        [Fact]
        public void FilesThenImages_ShareBudget()
        {
            QqLastReplyPlan plan = QqLastReplyPlanner.Plan("正文", Paths("C:\\a.bin", "C:\\b.bin"), Paths("C:\\1.png", "C:\\2.png"), 1800, 4, 3);
            Assert.Single(plan.Segments);
            Assert.Equal(2, plan.Files.Count);
            Assert.Single(plan.Images);
            Assert.Equal(1, plan.DroppedImages);
        }

        /// <summary>空输入——无段无附件（null 正文与空文件 / 空图片列表）。</summary>
        [Fact]
        public void EmptyInput_EmptyPlan()
        {
            QqLastReplyPlan plan = QqLastReplyPlanner.Plan(null, null, null, 1800, 4, 3);
            Assert.Empty(plan.Segments);
            Assert.Empty(plan.Files);
            Assert.Empty(plan.Images);
            Assert.Equal(0, plan.DroppedHeadSegments);
            Assert.Equal(0, plan.DroppedFiles);
            Assert.Equal(0, plan.DroppedImages);
            QqLastReplyPlan plan2 = QqLastReplyPlanner.Plan("   ", null, null, 1800, 4, 3);
            Assert.Empty(plan2.Segments);
        }
    }
}
