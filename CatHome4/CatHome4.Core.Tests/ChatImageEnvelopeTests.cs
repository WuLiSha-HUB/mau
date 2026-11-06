using System;
using System.Collections.Generic;
using CH4;
using Xunit;

namespace CatHome4.Core.Tests
{
    /// <summary>
    /// 对话图片包裹测试（A65；2026-10-06 放宽位置判据）——组装格式 / 严格门解析 / 指令行套用 / 往返一致。
    /// 规格：Project/CH4/design-ch4-chat-images.md §四（格式唯一权威）。
    /// 🔴 放宽（2026-10-06 · 莎裁）：包裹可在消息**任意位置**——解析输出前段（before）与后段（after）两段文本。
    /// </summary>
    public sealed class ChatImageEnvelopeTests
    {
        /// <summary>
        /// 组装——段头 + 条目（前文条数-批内序号，全角冒号）+ 段尾 + 空行 + 正文。
        /// </summary>
        [Fact]
        public void Build_TwoImages_PrefixesEnvelope()
        {
            List<string> images = new List<string>();
            images.Add(@"C:\Data\chat-images\a1b2.png");
            images.Add(@"C:\Data\chat-images\c3d4.jpg");
            string text = ChatImageEnvelope.Build(images, 92, "看这个");
            Assert.Equal("[image-open]\n图片92-1：C:\\Data\\chat-images\\a1b2.png\n图片92-2：C:\\Data\\chat-images\\c3d4.jpg\n[image-end]\n\n看这个", text);
        }

        /// <summary>
        /// 无图片——原样返回正文（零回归）。
        /// </summary>
        [Fact]
        public void Build_NoImages_ReturnsBodyUnchanged()
        {
            Assert.Equal("看这个", ChatImageEnvelope.Build(new List<string>(), 5, "看这个"));
            Assert.Equal("看这个", ChatImageEnvelope.Build(null, 5, "看这个"));
        }

        /// <summary>
        /// 无正文——包裹后不留多余空行（仅图片消息）。
        /// </summary>
        [Fact]
        public void Build_EmptyBody_NoTrailingBlankLine()
        {
            List<string> images = new List<string>();
            images.Add(@"C:\img\a1b2.png");
            string text = ChatImageEnvelope.Build(images, 3, "");
            Assert.Equal("[image-open]\n图片3-1：C:\\img\\a1b2.png\n[image-end]", text);
        }

        /// <summary>
        /// 空白路径——归一丢弃（剔除后序号从 1 重排；仅空项时不生成包裹）。
        /// </summary>
        [Fact]
        public void Build_BlankPaths_DroppedAndRenumbered()
        {
            List<string> images = new List<string>();
            images.Add("   ");
            images.Add(@"C:\img\a1b2.png");
            string text = ChatImageEnvelope.Build(images, 7, "x");
            Assert.Equal("[image-open]\n图片7-1：C:\\img\\a1b2.png\n[image-end]\n\nx", text);
            List<string> onlyBlank = new List<string>();
            onlyBlank.Add("  ");
            Assert.Equal("y", ChatImageEnvelope.Build(onlyBlank, 7, "y"));
        }

        /// <summary>
        /// 解析——合法包裹（消息开头）：条目按序取出，正文进后段，前段为空。
        /// </summary>
        [Fact]
        public void TryParse_ValidEnvelope_SplitsItemsAndBody()
        {
            string text = "[image-open]\n图片92-1：C:\\a b\\a1b2.png\n图片92-2：C:\\c\\c3d4.jpg\n[image-end]\n\n第一行\n第二行";
            List<ChatImageItem> items;
            string before;
            string after;
            bool ok = ChatImageEnvelope.TryParse(text, out items, out before, out after);
            Assert.True(ok);
            Assert.Equal(2, items.Count);
            Assert.Equal("92-1", items[0].Ref);
            Assert.Equal("C:\\a b\\a1b2.png", items[0].Path);
            Assert.Equal("92-2", items[1].Ref);
            Assert.Equal("", before);
            Assert.Equal("第一行\n第二行", after);
        }

        /// <summary>
        /// 解析——包裹在消息**中间**（放宽后新支持）：前段与后段各自取出，顺序保持原文。
        /// </summary>
        [Fact]
        public void TryParse_EnvelopeInMiddle_SplitsBeforeAndAfter()
        {
            string text = "A200 完成。\n\n[image-open]\n图片276-1：C:\\a.png\n[image-end]\n\n以下是说明。";
            List<ChatImageItem> items;
            string before;
            string after;
            bool ok = ChatImageEnvelope.TryParse(text, out items, out before, out after);
            Assert.True(ok);
            Assert.Single(items);
            Assert.Equal("276-1", items[0].Ref);
            Assert.Equal("A200 完成。", before);
            Assert.Equal("以下是说明。", after);
        }

        /// <summary>
        /// 解析——前一处段头残缺（有段头无段尾）：继续向后找下一处合法包裹。
        /// </summary>
        [Fact]
        public void TryParse_BrokenCandidateThenValid_ResolvesToLater()
        {
            string text = "[image-open]\n图片1-1：C:\\a.png\n中间没有段尾\n\n[image-open]\n图片2-1：C:\\b.png\n[image-end]\n尾部正文";
            List<ChatImageItem> items;
            string before;
            string after;
            bool ok = ChatImageEnvelope.TryParse(text, out items, out before, out after);
            Assert.True(ok);
            Assert.Single(items);
            Assert.Equal("2-1", items[0].Ref);
            Assert.Contains("中间没有段尾", before);
            Assert.Equal("尾部正文", after);
        }

        /// <summary>
        /// 严格门——缺段尾：整段按普通文本（items 空，before = 原文）。
        /// </summary>
        [Fact]
        public void TryParse_MissingEndTag_Rejected()
        {
            string text = "[image-open]\n图片1-1：C:\\a.png\n正文";
            List<ChatImageItem> items;
            string before;
            string after;
            Assert.False(ChatImageEnvelope.TryParse(text, out items, out before, out after));
            Assert.Empty(items);
            Assert.Equal(text, before);
            Assert.Equal("", after);
        }

        /// <summary>
        /// 严格门——缺段头：不命中。
        /// </summary>
        [Fact]
        public void TryParse_MissingOpenTag_Rejected()
        {
            List<ChatImageItem> items;
            string before;
            string after;
            Assert.False(ChatImageEnvelope.TryParse("图片1-1：C:\\a.png\n[image-end]", out items, out before, out after));
            Assert.Empty(items);
        }

        /// <summary>
        /// 严格门——包裹内空行：不命中（内部无杂行）。
        /// </summary>
        [Fact]
        public void TryParse_BlankLineInside_Rejected()
        {
            List<ChatImageItem> items;
            string before;
            string after;
            Assert.False(ChatImageEnvelope.TryParse("[image-open]\n图片1-1：C:\\a.png\n\n[image-end]", out items, out before, out after));
            Assert.Empty(items);
        }

        /// <summary>
        /// 严格门——包裹内非条目行：不命中。
        /// </summary>
        [Fact]
        public void TryParse_UnknownLineInside_Rejected()
        {
            List<ChatImageItem> items;
            string before;
            string after;
            Assert.False(ChatImageEnvelope.TryParse("[image-open]\n这里说明一下\n图片1-1：C:\\a.png\n[image-end]", out items, out before, out after));
            Assert.Empty(items);
        }

        /// <summary>
        /// 严格门——零条目（段头紧跟段尾）：不命中。
        /// </summary>
        [Fact]
        public void TryParse_NoItems_Rejected()
        {
            List<ChatImageItem> items;
            string before;
            string after;
            Assert.False(ChatImageEnvelope.TryParse("[image-open]\n[image-end]\n正文", out items, out before, out after));
            Assert.Empty(items);
        }
        /// <summary>
        /// 图片优先——代码围栏内的包裹照样命中，紧邻围栏行不留在前后段（与前端 imgSplit 同判据）。
        /// </summary>
        [Fact]
        public void TryParse_EnvelopeInsideCodeFence_Accepted()
        {
            string text = "前文\n\n```\n[image-open]\n图片1-1：C:\\a.png\n[image-end]\n```\n\n后文";
            List<ChatImageItem> items;
            string before;
            string after;
            Assert.True(ChatImageEnvelope.TryParse(text, out items, out before, out after));
            Assert.Single(items);
            Assert.Equal("1-1", items[0].Ref);
            Assert.Equal("前文", before);
            Assert.Equal("后文", after);
        }
        /// <summary>
        /// 围栏外的包裹——围栏闭合完整、与包裹不相邻，前后段各自内容原样保留。
        /// </summary>
        [Fact]
        public void TryParse_EnvelopeAfterClosedFence_Accepted()
        {
            string text = "```\ncode\n```\n\n[image-open]\n图片3-1：C:\\c.png\n[image-end]\n尾部";
            List<ChatImageItem> items;
            string before;
            string after;
            Assert.True(ChatImageEnvelope.TryParse(text, out items, out before, out after));
            Assert.Single(items);
            Assert.Equal("3-1", items[0].Ref);
            Assert.Equal("```\ncode\n```", before);
            Assert.Equal("尾部", after);
        }
        /// <summary>
        /// 包裹在围栏外、其后紧跟代码块——真代码块的围栏行不被吞。
        /// </summary>
        [Fact]
        public void TryParse_CodeFenceAfterEnvelope_Kept()
        {
            string text = "[image-open]\n图片2-1：C:\\b.png\n[image-end]\n\n```js\nvar a = 1;\n```";
            List<ChatImageItem> items;
            string before;
            string after;
            Assert.True(ChatImageEnvelope.TryParse(text, out items, out before, out after));
            Assert.Single(items);
            Assert.Equal("", before);
            Assert.Equal("```js\nvar a = 1;\n```", after);
        }

        /// <summary>
        /// 往返一致——组装产物必被自身解析命中（条目与正文逐一还原）。
        /// </summary>
        [Fact]
        public void TryParse_BuildRoundTrip_Matches()
        {
            List<string> images = new List<string>();
            images.Add(@"C:\Data\chat-images\a1b2.png");
            images.Add(@"C:\Data\chat-images\c3d4.webp");
            string text = ChatImageEnvelope.Build(images, 41, "两行\n正文");
            List<ChatImageItem> items;
            string before;
            string after;
            Assert.True(ChatImageEnvelope.TryParse(text, out items, out before, out after));
            Assert.Equal(2, items.Count);
            Assert.Equal("41-1", items[0].Ref);
            Assert.Equal("41-2", items[1].Ref);
            Assert.Equal(@"C:\Data\chat-images\a1b2.png", items[0].Path);
            Assert.Equal(@"C:\Data\chat-images\c3d4.webp", items[1].Path);
            Assert.Equal("", before);
            Assert.Equal("两行\n正文", after);
        }

        /// <summary>
        /// 指令行套用——Chat 行：包裹插在前缀之后、正文之前。
        /// </summary>
        [Fact]
        public void ApplyToLine_ChatLine_InsertsEnvelopeAfterPrefix()
        {
            List<string> images = new List<string>();
            images.Add(@"C:\img\a1b2.png");
            string line = ChatImageEnvelope.ApplyToLine("Chat 看这个", images, 9);
            Assert.Equal("Chat [image-open]\n图片9-1：C:\\img\\a1b2.png\n[image-end]\n\n看这个", line);
        }

        /// <summary>
        /// 指令行套用——非 Chat 行：原样返回（其他指令不带图片）。
        /// </summary>
        [Fact]
        public void ApplyToLine_NonChatLine_Unchanged()
        {
            List<string> images = new List<string>();
            images.Add(@"C:\img\a1b2.png");
            Assert.Equal("session.new", ChatImageEnvelope.ApplyToLine("session.new", images, 9));
        }

        /// <summary>
        /// 指令行套用——无图片：原样返回（零回归）。
        /// </summary>
        [Fact]
        public void ApplyToLine_NoImages_Unchanged()
        {
            Assert.Equal("Chat 看这个", ChatImageEnvelope.ApplyToLine("Chat 看这个", new List<string>(), 9));
            Assert.Equal("Chat 看这个", ChatImageEnvelope.ApplyToLine("Chat 看这个", null, 9));
        }

        /// <summary>
        /// 条目行格式——组装与解析共用产出点（编号-序号 + 全角冒号）。
        /// </summary>
        [Fact]
        public void BuildItemLine_Format()
        {
            Assert.Equal("图片92-2：C:\\img\\a.png", ChatImageEnvelope.BuildItemLine(92, 2, @"C:\img\a.png"));
        }
    }
}
