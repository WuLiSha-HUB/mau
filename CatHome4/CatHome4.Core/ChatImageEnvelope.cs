using System;
using System.Collections.Generic;
using System.Text;
using System.Text.RegularExpressions;

namespace CH4
{
    /// <summary>
    /// 对话图片包裹条目——编号（前文条数-批内序号）与图片绝对路径。
    /// 编号供渲染角标与模型指代（「图片92-1」）；路径供 Agent 侧 image-analyze 直读。
    /// </summary>
    internal sealed class ChatImageItem
    {
        /// <summary>编号文本（如 92-1）——前文条数-批内序号</summary>
        internal string Ref;

        /// <summary>图片绝对路径（视觉服务直读——不解析受控根寻址）</summary>
        internal string Path;
    }

    /// <summary>
    /// 对话图片包裹——组装（后端在消息落前文时刻拼 [image-open]…[image-end]）与解析（前端按严格门判定渲染）。
    /// 规格：Project/CH4/design-ch4-chat-images.md §四（格式唯一权威）——本类为组装与解析的单一真相源（同一格式定义两处共用）。
    /// 纯函数——零状态零 IO；严格门：段头 + ≥1 条目 + 段尾齐备，内部无杂行，缺任一条整段按普通文本。
    /// </summary>
    internal static class ChatImageEnvelope
    {
        /// <summary>段头——独占一行，大小写固定</summary>
        internal const string OpenTag = "[image-open]";

        /// <summary>段尾——独占一行，大小写固定</summary>
        internal const string EndTag = "[image-end]";

        /// <summary>指令前缀——Chat 指令行的固定头部（组装时插在正文之前、前缀之后）</summary>
        private const string ChatPrefix = "Chat ";

        /// <summary>条目行正则——图片&lt;前文条数&gt;-&lt;批内序号&gt;：&lt;绝对路径&gt;（全角冒号；路径取到行尾，可含空格）</summary>
        private static readonly Regex _itemRegex = new Regex("^图片(\\d+)-(\\d+)：(.+)$", RegexOptions.Compiled);

        /// <summary>
        /// 组装包裹 + 正文——无有效路径时原样返回正文（零回归）。
        /// </summary>
        /// <param name="images">图片绝对路径列表（可空）</param>
        /// <param name="contextCount">组装时刻的前文条数（编号前缀）</param>
        /// <param name="body">用户正文（可空）</param>
        /// <returns>完整消息文本（包裹 + 空行 + 正文；无图片 = 原正文）</returns>
        internal static string Build(IList<string> images, int contextCount, string body)
        {
            List<string> paths = Normalize(images);
            if (paths.Count == 0)
            {
                if (body == null)
                {
                    return "";
                }
                return body;
            }
            // [段1] 包裹本体——段头 + 条目 + 段尾（每行独占）
            StringBuilder sb = new StringBuilder();
            sb.Append(OpenTag);
            for (int i = 0; i < paths.Count; i = i + 1)
            {
                sb.Append("\n");
                sb.Append(BuildItemLine(contextCount, i + 1, paths[i]));
            }
            sb.Append("\n");
            sb.Append(EndTag);
            // [段2] 正文——空行分隔；无正文不留多余空行
            if (body != null && body.Length > 0)
            {
                sb.Append("\n\n");
                sb.Append(body);
            }
            return sb.ToString();
        }

        /// <summary>
        /// 指令行套用——`Chat &lt;正文&gt;` → `Chat &lt;包裹+正文&gt;`；非 Chat 行 / 无图片原样返回（零回归）。
        /// 单一出口：Chat 前缀约定只在本函数处理，调用面（宿主）不感知。
        /// </summary>
        /// <param name="line">指令行（如 "Chat 看这个"）</param>
        /// <param name="images">图片绝对路径列表（可空）</param>
        /// <param name="contextCount">组装时刻的前文条数</param>
        /// <returns>套用后的指令行（不成立时原样）</returns>
        internal static string ApplyToLine(string line, IList<string> images, int contextCount)
        {
            if (line == null || line.Length == 0)
            {
                return line;
            }
            if (images == null || images.Count == 0)
            {
                return line;
            }
            if (!line.StartsWith(ChatPrefix, StringComparison.Ordinal))
            {
                return line;
            }
            string body = line.Substring(ChatPrefix.Length);
            return ChatPrefix + Build(images, contextCount, body);
        }

        /// <summary>
        /// 解析包裹——严格门：段头 + ≥1 条目 + 段尾齐备，包裹内部无杂行。
        /// 🔴 2026-10-06 放宽：包裹可位于消息**任意位置**（行粒度）——原「必须首个非空行」退役；
        ///    前一候选不成立（缺段尾 / 内部杂行 / 零条目）继续向后找下一处段头。
        /// 🔴 图片包裹优先级最高——代码围栏（```）内的段头照样判包裹，紧邻的围栏行不留在前后段（与前端 lib/images.js imgSplit 同判据，2026-11-06 莎裁）。
        /// 不成立时：items 空列表、before = 原文 Trim、after = 空串（调用方按普通文本处理）。
        /// </summary>
        /// <param name="text">消息文本（可空）</param>
        /// <param name="items">出参——条目列表（按出现顺序）</param>
        /// <param name="before">出参——包裹**之前**的文本（无包裹 = 原文 Trim）</param>
        /// <param name="after">出参——包裹**之后**的文本（无包裹 = 空串）</param>
        /// <returns>true=命中合法包裹</returns>
        internal static bool TryParse(string text, out List<ChatImageItem> items, out string before, out string after)
        {
            items = new List<ChatImageItem>();
            before = "";
            after = "";
            if (text == null || text.Length == 0)
            {
                return false;
            }
            before = text.Trim();
            string[] lines = text.Replace("\r\n", "\n").Replace('\r', '\n').Split('\n');
            // [段0] 围栏状态预扫——inCodeAt[f] = 第 f 行之前是否处于代码围栏内
            // 只用于「包裹是否被围栏包住」的吞并判定，不参与命中判定（图片渲染优先，围栏内照样命中）
            bool[] inCodeAt = new bool[lines.Length];
            bool inCode = false;
            for (int f = 0; f < lines.Length; f = f + 1)
            {
                inCodeAt[f] = inCode;
                if (lines[f].Trim().StartsWith("```", StringComparison.Ordinal))
                {
                    inCode = !inCode;
                }
            }
            // [段1] 段头定位——任意位置扫描（放宽后不再要求首个非空行）；图片包裹优先级最高，
            // 不受代码围栏限制——围栏内的段头照样判包裹（与前端 lib/images.js imgSplit 同判据）
            for (int s = 0; s < lines.Length; s = s + 1)
            {
                if (lines[s].Trim() != OpenTag)
                {
                    continue;
                }
                // [段2] 条目行——≥1；段尾之前不得出现空行或非条目行（严格门）
                // 局部收集——本候选任一判定失败即放弃本候选（不留半截条目），继续向后找
                List<ChatImageItem> found = new List<ChatImageItem>();
                int i = s + 1;
                bool broken = false;
                while (i < lines.Length)
                {
                    string t = lines[i].Trim();
                    if (t == EndTag)
                    {
                        break;
                    }
                    if (t.Length == 0)
                    {
                        broken = true;
                        break;
                    }
                    Match m = _itemRegex.Match(t);
                    if (!m.Success)
                    {
                        broken = true;
                        break;
                    }
                    ChatImageItem item = new ChatImageItem();
                    item.Ref = m.Groups[1].Value + "-" + m.Groups[2].Value;
                    item.Path = m.Groups[3].Value.Trim();
                    found.Add(item);
                    i = i + 1;
                }
                if (broken || found.Count == 0 || i >= lines.Length)
                {
                    continue;
                }
                items = found;
                // 围栏行吞并——仅当包裹被围栏包住时才吞紧邻的围栏行；包裹在围栏外时，
                // 相邻的 ``` 属于真正的代码块，必须原样保留（与前端 imgSplit 同判据）
                bool fenced = inCodeAt[s];
                int beforeEnd = s;
                if (fenced && beforeEnd > 0 && lines[beforeEnd - 1].Trim().StartsWith("```", StringComparison.Ordinal))
                {
                    beforeEnd = beforeEnd - 1;
                }
                int afterStart = i + 1;
                if (fenced && afterStart < lines.Length && lines[afterStart].Trim().StartsWith("```", StringComparison.Ordinal))
                {
                    afterStart = afterStart + 1;
                }
                before = JoinLines(lines, 0, beforeEnd).Trim();
                after = JoinLines(lines, afterStart, lines.Length).Trim();
                return true;
            }
            return false;
        }

        /// <summary>
        /// 行区间拼接——[from, to) 逐行以 \n 连接（空区间返回空串）。
        /// </summary>
        /// <param name="lines">行数组</param>
        /// <param name="from">起始下标（含）</param>
        /// <param name="to">结束下标（不含）</param>
        /// <returns>拼接文本</returns>
        private static string JoinLines(string[] lines, int from, int to)
        {
            StringBuilder sb = new StringBuilder();
            for (int i = from; i < to; i = i + 1)
            {
                if (sb.Length > 0)
                {
                    sb.Append("\n");
                }
                sb.Append(lines[i]);
            }
            return sb.ToString();
        }

        /// <summary>
        /// 条目行构造——图片&lt;前文条数&gt;-&lt;序号&gt;：&lt;路径&gt;（组装与解析共用的格式唯一产出点）。
        /// </summary>
        /// <param name="contextCount">前文条数</param>
        /// <param name="index">批内序号（1 起）</param>
        /// <param name="path">图片绝对路径</param>
        /// <returns>条目行文本</returns>
        internal static string BuildItemLine(int contextCount, int index, string path)
        {
            return "图片" + contextCount.ToString() + "-" + index.ToString() + "：" + path;
        }

        /// <summary>
        /// 路径列表归一——Trim + 丢空项（顺序保持）。
        /// </summary>
        /// <param name="images">原始列表（可空）</param>
        /// <returns>归一后的路径列表（可直接入包裹）</returns>
        private static List<string> Normalize(IList<string> images)
        {
            List<string> paths = new List<string>();
            if (images == null)
            {
                return paths;
            }
            for (int i = 0; i < images.Count; i = i + 1)
            {
                string p = images[i];
                if (p == null)
                {
                    continue;
                }
                p = p.Trim();
                if (p.Length == 0)
                {
                    continue;
                }
                paths.Add(p);
            }
            return paths;
        }
    }
}
