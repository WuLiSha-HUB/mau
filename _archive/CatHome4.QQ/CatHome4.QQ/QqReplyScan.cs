using System;
using System.Collections.Generic;

namespace CatHome4.QQ
{
    /// <summary>
    /// QQ 回复图片包裹识别结果（A216）——正文（已剥离包裹）+ 图片路径列表。
    /// 由装配侧桥接 Core 域协议（ChatImageEnvelope）产出——QQ 域不依赖 Core（窄 DTO 口径）。
    /// </summary>
    public sealed class QqImageScan
    {
        /// <summary>剥离图片包裹后的正文</summary>
        public string Body = "";

        /// <summary>图片路径列表（包裹条目顺序）</summary>
        public List<string> Images = new List<string>();
    }

    /// <summary>
    /// 回复附件统一识别结构（A216）——转发路（SendRouted）与 /last（SendLastReply）共用。
    /// 文件标记（QqFileMarker——QQ 域私有格式）就地解析；图片包裹（Core 域协议 ChatImageEnvelope）
    /// 经注入委托 parseImages 解析（装配侧桥接——QQ 域零 Core 依赖）。
    /// 纯函数——零状态零 IO。
    /// </summary>
    internal static class QqReplyScan
    {
        /// <summary>
        /// 一次扫描——产出正文 + 文件路径 + 图片路径（先剥离文件标记行，再在剩余正文上识别图片包裹）。
        /// </summary>
        /// <param name="text">回复正文（可空）</param>
        /// <param name="parseImages">图片包裹解析委托（null=不识别图片）</param>
        /// <param name="body">出参——剥离标记与包裹后的正文</param>
        /// <param name="files">出参——文件标记路径列表</param>
        /// <param name="images">出参——图片包裹路径列表</param>
        internal static void Scan(string text, Func<string, QqImageScan> parseImages, out string body, out List<string> files, out List<string> images)
        {
            files = QqFileMarker.Extract(text, out string afterFiles);
            images = new List<string>();
            body = afterFiles;
            if (parseImages == null || afterFiles.Length == 0)
            {
                return;
            }
            QqImageScan scan = parseImages(afterFiles);
            if (scan == null)
            {
                return;
            }
            body = scan.Body;
            images = scan.Images;
        }
    }
}
