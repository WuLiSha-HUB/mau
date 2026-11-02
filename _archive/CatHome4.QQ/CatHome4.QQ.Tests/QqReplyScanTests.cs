using System.Collections.Generic;
using CatHome4.QQ;
using Xunit;

namespace CatHome4.QQ.Tests
{
    /// <summary>
    /// QqReplyScan 测试（A216）——回复附件统一识别结构：文件标记 + 图片包裹一次扫描。
    /// 覆盖：仅文件标记 / 仅图片（注入解析委托）/ 两者同现 / 无解析委托（零回归：包裹原样留在正文）/ 空输入。
    /// </summary>
    public sealed class QqReplyScanTests
    {
        /// <summary>模拟图片包裹解析——命中 [image-open]…[image-end] 时剥离包裹并收集条目路径。</summary>
        /// <param name="text">回复正文</param>
        /// <returns>正文（剥离包裹）+ 图片路径</returns>
        private static QqImageScan StubParse(string text)
        {
            QqImageScan scan = new QqImageScan();
            string openTag = "[image-open]";
            string endTag = "[image-end]";
            int open = text.IndexOf(openTag);
            int end = text.IndexOf(endTag);
            if (open < 0 || end < 0 || end < open)
            {
                scan.Body = text;
                return scan;
            }
            scan.Body = (text.Substring(0, open) + text.Substring(end + endTag.Length)).Trim();
            string inner = text.Substring(open + openTag.Length, end - open - openTag.Length);
            string[] lines = inner.Split('\n');
            for (int i = 0; i < lines.Length; i = i + 1)
            {
                string t = lines[i].Trim();
                if (t.Length > 0)
                {
                    int colon = t.IndexOf('：');
                    if (colon >= 0)
                    {
                        scan.Images.Add(t.Substring(colon + 1).Trim());
                    }
                }
            }
            return scan;
        }

        /// <summary>仅文件标记——标记行剥离，路径收集；正文保真。</summary>
        [Fact]
        public void FilesOnly_StrippedAndCollected()
        {
            string text = "看这个\n[QQBot发送文件:\"C:\\a.bin\"]\n后续";
            QqReplyScan.Scan(text, null, out string body, out List<string> files, out List<string> images);
            Assert.Single(files);
            Assert.Equal("C:\\a.bin", files[0]);
            Assert.Empty(images);
            Assert.Contains("看这个", body);
            Assert.DoesNotContain("QQBot发送文件", body);
        }

        /// <summary>仅图片包裹——注入解析委托后剥离包裹并收集路径。</summary>
        [Fact]
        public void ImagesOnly_StrippedAndCollected()
        {
            string text = "[image-open]\n图片9-1：C:\\a.png\n图片9-2：C:\\b.png\n[image-end]\n\n这是截图";
            QqReplyScan.Scan(text, StubParse, out string body, out List<string> files, out List<string> images);
            Assert.Empty(files);
            Assert.Equal(2, images.Count);
            Assert.Equal("C:\\a.png", images[0]);
            Assert.DoesNotContain("image-open", body);
            Assert.Contains("这是截图", body);
        }

        /// <summary>文件标记 + 图片包裹同现——两者各自剥离与收集，正文前后段拼回。</summary>
        [Fact]
        public void FilesAndImages_BothScanned()
        {
            string text = "看这个\n[QQBot发送文件:\"C:\\a.bin\"]\n[image-open]\n图片3-1：C:\\a.png\n[image-end]\n后续";
            QqReplyScan.Scan(text, StubParse, out string body, out List<string> files, out List<string> images);
            Assert.Single(files);
            Assert.Single(images);
            Assert.Equal("C:\\a.png", images[0]);
            Assert.Contains("看这个", body);
            Assert.Contains("后续", body);
        }

        /// <summary>无解析委托——图片包裹原样留在正文（零回归：未接线时不识别图片）。</summary>
        [Fact]
        public void NullParser_EnvelopeLeftInBody()
        {
            string text = "[image-open]\n图片9-1：C:\\a.png\n[image-end]";
            QqReplyScan.Scan(text, null, out string body, out List<string> files, out List<string> images);
            Assert.Empty(files);
            Assert.Empty(images);
            Assert.Contains("image-open", body);
        }

        /// <summary>空输入——三项全空。</summary>
        [Fact]
        public void EmptyInput_EmptyResult()
        {
            QqReplyScan.Scan("", StubParse, out string body, out List<string> files, out List<string> images);
            Assert.Equal("", body);
            Assert.Empty(files);
            Assert.Empty(images);
        }
    }
}
