using System;
using System.Collections.Generic;
using System.Text;

namespace CatHome4.QQ
{
    /// <summary>
    /// QQ 文本切分器——Markdown 结构感知切分（A58：2+2 转发与 /last 多段发送的公共切分面）。
    /// 算法（复用历史规格 design-ch4-qqbot-md §三）：标题行起块 / 空行分段 / 行 / 硬切四级退化；
    /// 代码围栏跨切点补全（段尾闭合 + 次段重开）；表格为不可分割项（打包时作原子块；自身超限原样独占一段）。
    /// 纯函数——零状态零 IO；除代码围栏补全与超长硬切外，分段按序拼接可复现原文。
    /// </summary>
    internal static class QqTextSplitter
    {
        /// <summary>块类型——段落（超限可退化拆分）</summary>
        private const int KindPara = 0;

        /// <summary>块类型——代码围栏（超限按行拆 + 围栏补全）</summary>
        private const int KindCode = 1;

        /// <summary>块类型——表格（不可分割项——超限亦原样独占一段）</summary>
        private const int KindTable = 2;

        /// <summary>代码段围栏开销——"```" + 换行 + 内容 + 换行 + "```" 的固定字符数（不含语言标记）</summary>
        private const int FenceOverhead = 5;

        /// <summary>代码段内容预算下限——语言标记过长时的保底（避免死循环式空段）</summary>
        private const int MinCodeBudget = 16;

        /// <summary>
        /// 按结构切分正文——每段长度不超过 limit（表格参与打包、自身超限时原样独占；代码围栏补全；超长单行硬切）。
        /// </summary>
        /// <param name="text">正文（可含 Markdown 结构）</param>
        /// <param name="limit">单段字符上限</param>
        /// <returns>按序段列表（空文本或非法上限 → 空列表）</returns>
        internal static List<string> Split(string text, int limit)
        {
            List<string> parts = new List<string>();
            if (text == null || text.Length == 0 || limit <= 0)
            {
                return parts;
            }
            List<Block> blocks = BuildBlocks(text);
            StringBuilder buffer = new StringBuilder();
            bool hasBlock = false;
            for (int i = 0; i < blocks.Count; i = i + 1)
            {
                Block b = blocks[i];
                if (b.Kind == KindCode && b.Text.Length > limit)
                {
                    // [段1] 代码块自身超限——按行拆 + 围栏补全（片段不回填缓冲）
                    if (buffer.Length > 0)
                    {
                        Flush(buffer, parts, ref hasBlock);
                    }
                    SplitCode(b, limit, parts);
                    continue;
                }
                // [段2] 装得下即续接（表格等原子块同样参与打包）
                int need = b.Text.Length;
                if (hasBlock)
                {
                    need = buffer.Length + 1 + b.Text.Length;
                }
                if (need <= limit)
                {
                    Append(buffer, b.Text, ref hasBlock);
                    continue;
                }
                // [段3] 装不下——先冲刷；单块仍超限：表格不可拆（原样独占一段），其余走退化拆分（段落按行 + 硬切）
                Flush(buffer, parts, ref hasBlock);
                if (b.Text.Length <= limit)
                {
                    Append(buffer, b.Text, ref hasBlock);
                    continue;
                }
                if (b.Kind == KindTable)
                {
                    parts.Add(b.Text);
                    continue;
                }
                SplitPara(b, limit, parts);
            }
            Flush(buffer, parts, ref hasBlock);
            return parts;
        }

        /// <summary>
        /// 结构块切分——行扫描：代码围栏 / 表格 / 标题起新块，空行段落边界（行连续划分，保真换行）。
        /// </summary>
        /// <param name="text">正文</param>
        /// <returns>按序结构块列表</returns>
        private static List<Block> BuildBlocks(string text)
        {
            string[] lines = text.Split('\n');
            List<Block> blocks = new List<Block>();
            int i = 0;
            while (i < lines.Length)
            {
                string line = lines[i];
                if (IsFence(line))
                {
                    // [段1] 代码围栏——收集到闭合围栏（未闭合则收到文末）
                    List<string> fence = new List<string>();
                    string lang = FenceLang(line);
                    fence.Add(line);
                    i = i + 1;
                    while (i < lines.Length)
                    {
                        fence.Add(lines[i]);
                        if (IsFence(lines[i]))
                        {
                            i = i + 1;
                            break;
                        }
                        i = i + 1;
                    }
                    blocks.Add(NewBlock(KindCode, fence, lang));
                    continue;
                }
                if (IsTableLine(line))
                {
                    // [段2] 表格——连续表格行整体成块（不可分割项）
                    List<string> table = new List<string>();
                    while (i < lines.Length && IsTableLine(lines[i]))
                    {
                        table.Add(lines[i]);
                        i = i + 1;
                    }
                    blocks.Add(NewBlock(KindTable, table, ""));
                    continue;
                }
                if (IsHeading(line))
                {
                    // [段3] 标题起块——标题行 + 其后紧邻的普通行
                    List<string> head = new List<string>();
                    head.Add(line);
                    i = i + 1;
                    while (i < lines.Length && IsPlainLine(lines[i]))
                    {
                        head.Add(lines[i]);
                        i = i + 1;
                    }
                    blocks.Add(NewBlock(KindPara, head, ""));
                    continue;
                }
                if (line.Trim().Length == 0)
                {
                    // [段4] 空行——段落边界（连续空行整体成块，保真换行）
                    List<string> blank = new List<string>();
                    while (i < lines.Length && lines[i].Trim().Length == 0)
                    {
                        blank.Add(lines[i]);
                        i = i + 1;
                    }
                    blocks.Add(NewBlock(KindPara, blank, ""));
                    continue;
                }
                // [段5] 普通段落——收集到空行 / 标题 / 围栏 / 表格
                List<string> para = new List<string>();
                while (i < lines.Length && IsPlainLine(lines[i]))
                {
                    para.Add(lines[i]);
                    i = i + 1;
                }
                blocks.Add(NewBlock(KindPara, para, ""));
            }
            return blocks;
        }

        /// <summary>
        /// 段落块超限——按行贪心打包；单行超限按 limit 硬切（硬切片独立成段，不回填缓冲）。
        /// </summary>
        /// <param name="b">段落块</param>
        /// <param name="limit">单段字符上限</param>
        /// <param name="parts">段列表（出参）</param>
        private static void SplitPara(Block b, int limit, List<string> parts)
        {
            string[] lines = b.Text.Split('\n');
            StringBuilder buffer = new StringBuilder();
            bool hasBlock = false;
            for (int i = 0; i < lines.Length; i = i + 1)
            {
                string line = lines[i];
                if (line.Length > limit)
                {
                    // 单行超限——硬切（保结构优先级的最后一级退化）
                    if (buffer.Length > 0)
                    {
                        Flush(buffer, parts, ref hasBlock);
                    }
                    int pos = 0;
                    while (pos < line.Length)
                    {
                        int take = limit;
                        if (line.Length - pos < limit)
                        {
                            take = line.Length - pos;
                        }
                        parts.Add(line.Substring(pos, take));
                        pos = pos + take;
                    }
                    continue;
                }
                if (hasBlock && buffer.Length + 1 + line.Length > limit)
                {
                    Flush(buffer, parts, ref hasBlock);
                }
                Append(buffer, line, ref hasBlock);
            }
            Flush(buffer, parts, ref hasBlock);
        }

        /// <summary>
        /// 代码块超限——按行拆分；段尾补闭合围栏、次段首重开围栏（语言标记沿用），保证各段独立可解析。
        /// </summary>
        /// <param name="b">代码块</param>
        /// <param name="limit">单段字符上限</param>
        /// <param name="parts">段列表（出参）</param>
        private static void SplitCode(Block b, int limit, List<string> parts)
        {
            List<string> body = new List<string>();
            string[] lines = b.Text.Split('\n');
            for (int i = 0; i < lines.Length; i = i + 1)
            {
                body.Add(lines[i]);
            }
            if (body.Count > 0 && IsFence(body[0]))
            {
                body.RemoveAt(0);
            }
            if (body.Count > 0 && IsFence(body[body.Count - 1]))
            {
                body.RemoveAt(body.Count - 1);
            }
            string open = "```" + b.Lang;
            int budget = limit - open.Length - FenceOverhead;
            if (budget < MinCodeBudget)
            {
                budget = MinCodeBudget;
            }
            bool produced = false;
            bool curBlock = false;
            StringBuilder cur = new StringBuilder();
            for (int i = 0; i < body.Count; i = i + 1)
            {
                string line = body[i];
                if (line.Length > budget)
                {
                    // 超长行——按预算硬切成多个独立代码段
                    if (cur.Length > 0)
                    {
                        parts.Add(open + "\n" + cur.ToString() + "\n```");
                        produced = true;
                        cur.Length = 0;
                        curBlock = false;
                    }
                    int pos = 0;
                    while (pos < line.Length)
                    {
                        int take = budget;
                        if (line.Length - pos < budget)
                        {
                            take = line.Length - pos;
                        }
                        parts.Add(open + "\n" + line.Substring(pos, take) + "\n```");
                        produced = true;
                        pos = pos + take;
                    }
                    continue;
                }
                int need = line.Length;
                if (cur.Length > 0)
                {
                    need = cur.Length + 1 + line.Length;
                }
                if (need > budget)
                {
                    parts.Add(open + "\n" + cur.ToString() + "\n```");
                    produced = true;
                    cur.Length = 0;
                    curBlock = false;
                }
                Append(cur, line, ref curBlock);
            }
            if (cur.Length > 0)
            {
                parts.Add(open + "\n" + cur.ToString() + "\n```");
                produced = true;
            }
            if (!produced)
            {
                parts.Add(b.Text);
            }
        }

        /// <summary>
        /// 追加块到缓冲区——非首块补一个换行（块为行连续划分，块间换行为其固有分隔）。
        /// </summary>
        /// <param name="buffer">缓冲区</param>
        /// <param name="blockText">块文本</param>
        /// <param name="hasBlock">本段是否已有块（出参——入列后置 true）</param>
        private static void Append(StringBuilder buffer, string blockText, ref bool hasBlock)
        {
            if (hasBlock)
            {
                buffer.Append("\n");
            }
            buffer.Append(blockText);
            hasBlock = true;
        }

        /// <summary>
        /// 冲刷缓冲区——非空段入列；纯空白段丢弃（不产生空消息）；无论是否入列都重置缓冲状态。
        /// </summary>
        /// <param name="buffer">缓冲区</param>
        /// <param name="parts">段列表（出参）</param>
        /// <param name="hasBlock">本段是否已有块（出参——重置为 false）</param>
        private static void Flush(StringBuilder buffer, List<string> parts, ref bool hasBlock)
        {
            string s = buffer.ToString();
            buffer.Length = 0;
            hasBlock = false;
            if (s.Trim().Length == 0)
            {
                return;
            }
            parts.Add(s);
        }

        /// <summary>
        /// 普通行判定——非空行且非标题 / 非围栏 / 非表格行。
        /// </summary>
        /// <param name="line">行文本</param>
        /// <returns>普通行 true</returns>
        private static bool IsPlainLine(string line)
        {
            if (line.Trim().Length == 0)
            {
                return false;
            }
            if (IsFence(line) || IsTableLine(line) || IsHeading(line))
            {
                return false;
            }
            return true;
        }

        /// <summary>
        /// 代码围栏行判定——行首（允许前导空白）连续三个反引号。
        /// </summary>
        /// <param name="line">行文本</param>
        /// <returns>围栏行 true</returns>
        private static bool IsFence(string line)
        {
            string t = line.TrimStart();
            return t.StartsWith("```", StringComparison.Ordinal);
        }

        /// <summary>
        /// 围栏语言标记——围栏后的首个词（空=无标记）。
        /// </summary>
        /// <param name="line">围栏行文本</param>
        /// <returns>语言标记</returns>
        private static string FenceLang(string line)
        {
            string t = line.TrimStart();
            if (!t.StartsWith("```", StringComparison.Ordinal))
            {
                return "";
            }
            return t.Substring(3).Trim();
        }

        /// <summary>
        /// 表格行判定——行首（允许前导空白）为竖线。
        /// </summary>
        /// <param name="line">行文本</param>
        /// <returns>表格行 true</returns>
        private static bool IsTableLine(string line)
        {
            string t = line.TrimStart();
            return t.StartsWith("|", StringComparison.Ordinal);
        }

        /// <summary>
        /// 标题行判定——1~6 个 # 后接空格或行尾（Markdown ATX 标题）。
        /// </summary>
        /// <param name="line">行文本</param>
        /// <returns>标题行 true</returns>
        private static bool IsHeading(string line)
        {
            string t = line.TrimStart();
            int n = 0;
            while (n < t.Length && t[n] == '#')
            {
                n = n + 1;
            }
            if (n == 0 || n > 6)
            {
                return false;
            }
            if (n == t.Length)
            {
                return true;
            }
            return t[n] == ' ';
        }

        /// <summary>
        /// 构造结构块——行列表以 \n 拼接为块文本。
        /// </summary>
        /// <param name="kind">块类型</param>
        /// <param name="lines">块内行列表</param>
        /// <param name="lang">代码语言标记（非代码块传空串）</param>
        /// <returns>结构块</returns>
        private static Block NewBlock(int kind, List<string> lines, string lang)
        {
            Block b = new Block();
            b.Kind = kind;
            b.Lang = lang;
            b.Text = string.Join("\n", lines);
            return b;
        }

        /// <summary>
        /// 结构块——行为连续划分（Text 为原文行以 \n 拼接，保真换行）。
        /// </summary>
        private sealed class Block
        {
            /// <summary>块类型——KindPara / KindCode / KindTable</summary>
            public int Kind;

            /// <summary>块文本（原文行以 \n 拼接）</summary>
            public string Text;

            /// <summary>代码块语言标记（围栏后首个词；非代码块空串）</summary>
            public string Lang = "";
        }
    }
}
