using System;
using System.Collections.Generic;
using System.Text;
using System.Text.RegularExpressions;

namespace CatHome4.QQ
{
    /// <summary>
    /// QQBot 文件标记解析（A58）——强匹配格式：标记必须**独占一行**且形态固定
    /// <c>[QQBot发送文件:"&lt;绝对路径&gt;"]</c>（前后允许空白）。
    /// 动机：旧的宽匹配 `[文件:…]` 会被自然语言说明与工具输出（cs-read 结果行）误命中——
    /// 强格式 + 独占行使"非主动声明"不可能触发；字符级撞车不再具备语义。
    /// 纯函数——零状态零 IO。
    /// </summary>
    internal static class QqFileMarker
    {
        /// <summary>标记行正则——行级全匹配：允许前后空白，路径以双引号包裹且不含引号</summary>
        private const string MarkerPattern = "^\\s*\\[QQBot发送文件:\"([^\"]+)\"\\]\\s*$";

        /// <summary>标记正则实例——编译期一次</summary>
        private static readonly Regex _markerRegex = new Regex(MarkerPattern, RegexOptions.Compiled);

        /// <summary>
        /// 提取文件标记——收集独占行的标记路径；正文移除标记行（其余内容保真，连续空行合并为一行）。
        /// 无标记 → 原样返回（Trim 后）。
        /// </summary>
        /// <param name="text">回复正文</param>
        /// <param name="body">移除标记行后的正文（出参）</param>
        /// <returns>文件路径列表（按出现顺序；无标记 → 空列表）</returns>
        internal static List<string> Extract(string text, out string body)
        {
            List<string> files = new List<string>();
            if (text == null || text.Length == 0)
            {
                body = "";
                return files;
            }
            string[] lines = text.Split('\n');
            List<string> kept = new List<string>();
            for (int i = 0; i < lines.Length; i = i + 1)
            {
                Match m = _markerRegex.Match(lines[i]);
                if (m.Success)
                {
                    string path = m.Groups[1].Value.Trim();
                    if (path.Length > 0)
                    {
                        files.Add(path);
                    }
                    continue;
                }
                kept.Add(lines[i]);
            }
            if (files.Count == 0)
            {
                body = text.Trim();
                return files;
            }
            // [段1] 摘除标记行后重排——合并因摘除而相邻的连续空行（避免残留空段）
            StringBuilder sb = new StringBuilder();
            bool prevBlank = false;
            for (int i = 0; i < kept.Count; i = i + 1)
            {
                string line = kept[i];
                bool blank = line.Trim().Length == 0;
                if (blank && prevBlank)
                {
                    continue;
                }
                if (sb.Length > 0)
                {
                    sb.Append("\n");
                }
                sb.Append(line);
                prevBlank = blank;
            }
            body = sb.ToString().Trim();
            return files;
        }
    }
}
