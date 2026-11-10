// ═══════════════════════════════════════════════════
// 积木: text.replace
// ID:   BRIK-TEXT-004
// 类别: TEXT
// 作用: 锚点替换——exact/ignore_case 唯一锚点替换，all/regex 全部匹配替换并原子写回（exact/ignore_case/all/regex；NotFound 带差异事实面（偏移 / 行号 / 已匹配前缀 / 差异字符码点 / 空白段长度），Ambiguous 带候选行）；new 传删除标记「黑暗剑+22」按空文本落盘（等价删除，四模式通用），空 new 一律拒绝；成功回执附自证四件——原文摘要（行数 + 首行，被吃掉的原文必须在回执里看得见）/ 行数差（净删行 ⚠；多处替换按「每次替换」表述）/ 结构行计数（表行·分隔行·标题·分节线减少即 ⚠，告警不拒）/ 窗口范围（大文件显式说明只显示替换点前后 ≤3 行）——LLM 工具 text-replace 语料执行面
// 依赖: 无
// 引用: Mau.Runtime（FileSystemService/DataBox/TextAlgorithms）
// 原理: ReadTextAuto 读 → TextAlgorithms 匹配/替换 → WriteTextAuto 写；锚点三态判定与全部文案在本积木（A227 归位——算法面只给事实）
// 常用: TextCat 认领线——'text.replace'[@args] > @result
// ═══════════════════════════════════════════════════
using System;
using System.Text.Json;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 文本积木——text-replace 替换文本（LLM 工具执行面：参数整包 argsJson）
    /// </summary>
    public static class TextReplaceBrick
    {
        /// <summary>删除标记——new 精确等于本串时按空文本落盘（等价删除；四模式通用）</summary>
        private const string DeleteKey = "黑暗剑+22";

        /// <summary>regex 单次匹配跨度出声阈值（行）</summary>
        private const int SpanWarnLines = 10;

        /// <summary>regex 单次匹配跨度出声阈值（字符）</summary>
        private const int SpanWarnChars = 1000;

        /// <summary>差异窗口单侧字符数（期望 / 实际 各取差异点前后 ≤ N 字符；差异字符单独用 ⟨⟩ 标出）</summary>
        private const int DiffContextChars = 5;

        /// <summary>原文摘要首行截断长度（回执「原文首行」——过长只留前 N 字符）</summary>
        private const int OldScopeChars = 60;

        /// <summary>实体形态标记（锚点未命中出声判据——tokens 交由中性算法 ContainsAny 判定）</summary>
        private static readonly string[] EntityTokens = new string[] { "&lt;", "&gt;", "&amp;", "&quot;" };

        /// <summary>
        /// 锚点替换——exact/ignore_case 唯一命中替换，all/regex 全部匹配；编码内建 + 换行保真；
        /// new 空值一律拒绝（错误文本提示删除标记），new 等于 DeleteKey 时按空文本落盘（等价删除），成功返回文本注明本次按空 New 删除
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（path/old/new/mode）</param>
        /// <param name="result">三态确认文本或 ERR| 错误文本</param>
        /// <returns>true=执行成功</returns>
        public static bool Replace(string argsJson, out string result)
        {
            result = "";
            // [参数面] 声明面口径零容忍——未知 / 缺值 / 非法 mode 一律 ERR|BAD_ARGS（catId 保留键放行）
            string badArgs = JsonArgs.Validate(argsJson, "path old new mode", "path old new", "mode", "exact|ignore_case|all|regex");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            string path = JsonArgs.Get(argsJson, "path");
            string oldText = JsonArgs.Get(argsJson, "old");
            string newText = JsonArgs.Get(argsJson, "new");
            if (path == "§PARSE_FAIL§" || oldText == "§PARSE_FAIL§" || newText == "§PARSE_FAIL§")
            {
                result = "ERR|BAD_ARGS|工具参数 JSON 解析失败（LLM 生成参数可能被截断——超长内容请分段写入）";
                return false;
            }
            if (path.Length == 0 || oldText.Length == 0)
            {
                result = "ERR|BAD_ARGS|缺少参数 path 或 old";
                return false;
            }
            // [删除语义] new 面——空值 / 缺值一律拒绝（区分显式删除与漏传）；删除标记按空文本落盘（A88）
            if (newText.Length == 0)
            {
                result = "ERR|BAD_ARGS|缺参数 new——替换文本不可为空；删除匹配文本请传删除标记「" + DeleteKey + "」";
                return false;
            }
            bool deleteMode = newText == DeleteKey;
            if (deleteMode)
            {
                newText = "";
            }
            string mode = JsonArgs.Get(argsJson, "mode");
            if (mode.Length == 0)
            {
                mode = "exact";
            }
            TextMatchMode matchMode = TextMatchMode.Exact;
            if (mode == "ignore_case")
            {
                matchMode = TextMatchMode.IgnoreCase;
            }
            else if (mode == "regex")
            {
                matchMode = TextMatchMode.Regex;
            }
            try
            {
                FileSystemService? fs = FileSystemRegistry.ResolveScoped(JsonArgs.Get(argsJson, "catId"));
                if (fs == null)
                {
                    DataBox.TryResolve<FileSystemService>(out fs);
                }
                if (fs == null)
                {
                    result = "ERR|FS_NO_SERVICE|宿主未注入 FileSystemService";
                    return false;
                }
                // [IO] 读取（编码内建——去 BOM）→ 换行归一供匹配
                string content = fs.ReadTextAuto(path);
                string unified = content.Replace("\r\n", "\n");
                string target = oldText.Replace("\r\n", "\n");
                // [匹配] 算法面——命中跨度（索引 + 长度）
                TextMatchSpan[] spans = TextAlgorithms.FindMatches(unified, target, matchMode);
                if (spans.Length == 0)
                {
                    // [未命中] 差异事实面（A333）——偏移 / 行号 / 已匹配前缀 / 差异字符码点 / 空白段长度
                    result = AnchorMissNote(unified, target, "");
                    return false;
                }
                if (mode != "regex" && mode != "all" && spans.Length > 1)
                {
                    result = "ERR|ANCHOR_AMBIGUOUS|锚点出现 " + spans.Length.ToString() + " 次，候选行: " + string.Join(", ", LineNumbersOf(unified, spans));
                    return false;
                }
                // [替换] 算法面——全部匹配替换（regex 支持捕获组）
                string replaced = TextAlgorithms.ReplaceAll(unified, target, newText, matchMode);
                // [IO] 写回（既有文件保真——BOM / 换行）
                fs.WriteTextAuto(path, replaced);
                string deleteNote = deleteMode ? "——本次 New 为空（删除标记「" + DeleteKey + "」）：删除匹配的 Old 串" : "";
                string spanNote = "";
                if (mode == "regex")
                {
                    int maxChars = 0;
                    int maxIndex = 0;
                    int totalChars = 0;
                    for (int i = 0; i < spans.Length; i = i + 1)
                    {
                        totalChars = totalChars + spans[i].Length;
                        if (spans[i].Length > maxChars)
                        {
                            maxChars = spans[i].Length;
                            maxIndex = spans[i].Index;
                        }
                    }
                    int maxLines = CountLinesOf(Slice(unified, maxIndex, maxChars));
                    spanNote = " · 匹配跨度 最长 " + maxLines.ToString() + " 行 / " + maxChars.ToString() + " 字符 · 命中合计 " + totalChars.ToString() + " 字符";
                    if (maxLines >= SpanWarnLines || maxChars >= SpanWarnChars)
                    {
                        spanNote = spanNote + " ⚠ 单次匹配跨度过大——请核对是否吞入相邻内容";
                    }
                }
                string lineNote = "";
                if (mode == "all" && spans.Length > 0)
                {
                    string[] lines = LineNumbersOf(unified, spans);
                    lineNote = " · 命中行 " + string.Join(", ", lines);
                    if (lines.Length < spans.Length)
                    {
                        lineNote = lineNote + "…（前 " + lines.Length.ToString() + " / 共 " + spans.Length.ToString() + "）";
                    }
                }
                // [自证面] 行数差 + 结构行计数 + 窗口范围——三件都在回执里出声（丢行 / 吃结构行必须当场可见）
                string shiftNote = BuildLineShiftNote(target, newText, deleteMode, spans.Length);
                string oldNote = BuildOldScopeNote(target);
                string structNote = BuildStructMarkNote(target, newText);
                // 结构化返回体（design-ch4-tools 附录 · A214）——首行 JSON 头（target/items）+ 正文摘要行 + 目标段
                string snippet = SnippetAround(replaced, spans[0].Index);
                result = MetaHead("text-replace", true, path, spans.Length) + "\n" + path + " | 替换 " + spans.Length.ToString() + " 处" + spanNote + lineNote + deleteNote + shiftNote + structNote + oldNote + "\n" + snippet + BuildSnippetScopeNote(replaced, snippet);
                return true;
            }
            catch (Exception ex)
            {
                result = "ERR|" + ex.GetType().Name + "|" + ex.Message;
                return false;
            }
        }

        /// <summary>
        /// 锚点未命中回执——差异事实面：偏移 / 行号 / 已匹配前缀长度 / 差异字符码点 / 空白段长度 + 定向提示。
        /// 动机（A333）：原回执只给差异点后 10 字符，且无行号与前缀长度——空白差异肉眼不可辨，模型只能靠猜重试
        /// （2026-11-10 #966：三次换锚点全败在「多一个空格」，最后放弃锚点改整文件覆写）。
        /// </summary>
        /// <param name="unified">归一化正文</param>
        /// <param name="target">归一化锚点</param>
        /// <param name="label">锚点角色标注（空串 = 「锚点」；读取面传「起始锚点」/「结束锚点」）</param>
        /// <returns>多行错误文本（首行保持 ERR|ANCHOR_NOT_FOUND| 契约）</returns>
        public static string AnchorMissNote(string unified, string target, string label)
        {
            TextDiffPoint diff = TextAlgorithms.LocateBestMatch(unified, target);
            int common = diff.CommonLength;
            int index = diff.ByteIndex;
            if (index < 0)
            {
                index = 0;
            }
            if (index > unified.Length)
            {
                index = unified.Length;
            }
            System.Collections.Generic.List<string> rows = new System.Collections.Generic.List<string>();
            string head = label.Length > 0 ? label + " · " : "";
            rows.Add("ERR|ANCHOR_NOT_FOUND|" + head + "差异点 第 " + index.ToString() + " 字符（文件第 " + LineNumberOf(unified, index).ToString() + " 行）· 已匹配前缀 " + common.ToString() + " 字符 · 差异在锚点第 " + (common + 1).ToString() + " 字符");
            rows.Add("期望：" + BuildDiffWindow(target, common));
            rows.Add("实际：" + BuildDiffWindow(unified, index));
            bool blankDiff = false;
            string verdict = BuildDiffVerdict(unified, target, common, index, out blankDiff);
            if (verdict.Length > 0)
            {
                rows.Add("｜" + verdict);
            }
            // 空白差异已有机械结论——不再追加转义提示（两条提示对冲会淹没真因）
            string hint = "";
            if (blankDiff == false)
            {
                hint = BuildMissHint(unified, target, common, index);
            }
            if (hint.Length > 0)
            {
                rows.Add("｜" + hint);
            }
            return string.Join("\n", rows);
        }

        /// <summary>差异窗口——差异点前后各 DiffContextChars 字符，差异字符用 ⟨⟩ 标出（不可见字符转标记）</summary>
        /// <param name="text">待取文本</param>
        /// <param name="index">差异点索引</param>
        /// <returns>窗口文本</returns>
        private static string BuildDiffWindow(string text, int index)
        {
            if (index < 0)
            {
                index = 0;
            }
            if (index > text.Length)
            {
                index = text.Length;
            }
            int from = index - DiffContextChars;
            string lead = "…";
            if (from <= 0)
            {
                from = 0;
                lead = "";
            }
            string head = text.Substring(from, index - from);
            string mark = "⟨∅⟩";
            if (index < text.Length)
            {
                mark = "⟨" + VisibleChar(text.Substring(index, 1)) + "⟩";
            }
            string tail = "";
            int afterStart = index + 1;
            if (afterStart < text.Length)
            {
                int take = Math.Min(DiffContextChars, text.Length - afterStart);
                tail = text.Substring(afterStart, take);
                if (afterStart + take < text.Length)
                {
                    tail = tail + "…";
                }
            }
            return lead + head + mark + tail;
        }

        /// <summary>不可见字符转可见标记（空格 / TAB / 换行——回执里必须一眼可辨）</summary>
        /// <param name="ch">单字符</param>
        /// <returns>可见形态</returns>
        private static string VisibleChar(string ch)
        {
            if (ch == " ")
            {
                return "[空格]";
            }
            if (ch == "\t")
            {
                return "[TAB]";
            }
            if (ch == "\n")
            {
                return "[换行]";
            }
            return ch;
        }

        /// <summary>差异定性——空白段长度对比 / 一侧结束 / 具体字符码点（机械判定，不让模型自己数）</summary>
        /// <param name="unified">归一化正文</param>
        /// <param name="target">归一化锚点</param>
        /// <param name="common">已匹配前缀长度</param>
        /// <param name="index">文件侧差异点索引</param>
        /// <param name="blankDiff">出参——差异是否落在空白段（消费方据此抑制转义提示）</param>
        /// <returns>结论句（空串=无结论）</returns>
        private static string BuildDiffVerdict(string unified, string target, int common, int index, out bool blankDiff)
        {
            blankDiff = false;
            bool targetBlank = common < target.Length && IsBlankChar(target, common);
            bool fileBlank = index < unified.Length && IsBlankChar(unified, index);
            if (targetBlank || fileBlank)
            {
                blankDiff = true;
                return "差异是空白——锚点此处连续空白 " + BlankRunAt(target, common).ToString() + " 个，文件 " + BlankRunAt(unified, index).ToString() + " 个（空白肉眼不可辨，按此数校正）";
            }
            if (common >= target.Length)
            {
                return "锚点在差异点处已结束——锚点全文 " + target.Length.ToString() + " 字符全部命中文件前缀，差异来自文件该处还有后续内容";
            }
            if (index >= unified.Length)
            {
                return "文件在差异点处已结束——锚点比文件多出 " + (target.Length - common).ToString() + " 字符";
            }
            return "差异字符：期望 " + CodePointNote(target.Substring(common, 1)) + "，实际 " + CodePointNote(unified.Substring(index, 1));
        }

        /// <summary>字符码点标注——U+XXXX（可见形态）</summary>
        /// <param name="ch">单字符</param>
        /// <returns>码点文本</returns>
        private static string CodePointNote(string ch)
        {
            int code = (int)ch[0];
            return "U+" + code.ToString("X4") + "（" + VisibleChar(ch) + "）";
        }

        /// <summary>差异点所在连续空白段长度（空格 / TAB——向前后各扩到非空白处）</summary>
        /// <param name="text">待检文本</param>
        /// <param name="index">差异点索引</param>
        /// <returns>空白段总长度</returns>
        private static int BlankRunAt(string text, int index)
        {
            int start = index;
            if (start > text.Length)
            {
                start = text.Length;
            }
            while (start > 0 && IsBlankChar(text, start - 1))
            {
                start = start - 1;
            }
            int end = index;
            if (end < 0)
            {
                end = 0;
            }
            while (end < text.Length && IsBlankChar(text, end))
            {
                end = end + 1;
            }
            return end - start;
        }

        /// <summary>是否空格 / TAB（换行不算——换行走码点分支，避免空白段跨行误读）</summary>
        /// <param name="text">待检文本</param>
        /// <param name="index">索引</param>
        /// <returns>true=空格或 TAB</returns>
        private static bool IsBlankChar(string text, int index)
        {
            if (index < 0 || index >= text.Length)
            {
                return false;
            }
            char ch = text[index];
            return ch == ' ' || ch == '\t';
        }

        /// <summary>偏移 → 行号（1 起）</summary>
        /// <param name="content">归一化正文</param>
        /// <param name="index">字符偏移</param>
        /// <returns>行号</returns>
        private static int LineNumberOf(string content, int index)
        {
            if (index > content.Length)
            {
                index = content.Length;
            }
            int line = 1;
            for (int i = 0; i < index; i = i + 1)
            {
                if (content[i] == '\n')
                {
                    line = line + 1;
                }
            }
            return line;
        }

        /// <summary>定向提示——只在差异真要转义 / 实体写法时出声（原判据「含反斜杠或引号即提示」在 C# 源码里必中，噪音淹没真因）</summary>
        /// <param name="unified">归一化正文</param>
        /// <param name="target">归一化锚点</param>
        /// <param name="common">已匹配前缀长度</param>
        /// <param name="index">文件侧差异点索引</param>
        /// <returns>提示句（空串=无提示）</returns>
        private static string BuildMissHint(string unified, string target, int common, int index)
        {
            string want = common < target.Length ? target.Substring(common, 1) : "";
            string got = index < unified.Length ? unified.Substring(index, 1) : "";
            if (want == "\\" || want == "\"" || got == "\\" || got == "\"")
            {
                return "提示：差异落在反斜杠 / 引号上——锚点按标准 JSON 语义书写（字面反斜杠写两个反斜杠、字面引号写反斜杠加引号），并直接取 text-read 的原文行";
            }
            int fromA = common > DiffContextChars ? common - DiffContextChars : 0;
            int fromB = index > DiffContextChars ? index - DiffContextChars : 0;
            string window = Slice(target, fromA, DiffContextChars * 2) + Slice(unified, fromB, DiffContextChars * 2);
            if (TextAlgorithms.ContainsAny(window, EntityTokens))
            {
                return "提示：差异窗口含实体写法——锚点直接写裸符号 < > &；若文件本身是实体文本，锚点写双写形态 &amp;lt;";
            }
            return "";
        }

        /// <summary>截取片段（越界截断）</summary>
        /// <param name="text">原文</param>
        /// <param name="start">起始索引</param>
        /// <param name="count">最大长度</param>
        /// <returns>片段</returns>
        private static string Slice(string text, int start, int count)
        {
            if (start < 0)
            {
                start = 0;
            }
            if (start >= text.Length)
            {
                return "";
            }
            int take = Math.Min(count, text.Length - start);
            return text.Substring(start, take);
        }

        /// <summary>命中跨度 → 行号（1 起，前 5 个——Ambiguous / all 候选）</summary>
        /// <param name="content">归一化正文</param>
        /// <param name="spans">命中跨度</param>
        /// <returns>行号字符串数组</returns>
        private static string[] LineNumbersOf(string content, TextMatchSpan[] spans)
        {
            System.Collections.Generic.List<string> lines = new System.Collections.Generic.List<string>();
            for (int i = 0; i < spans.Length && lines.Count < 5; i = i + 1)
            {
                int line = 1;
                int limit = Math.Min(spans[i].Index, content.Length);
                for (int j = 0; j < limit; j = j + 1)
                {
                    if (content[j] == '\n')
                    {
                        line = line + 1;
                    }
                }
                lines.Add(line.ToString());
            }
            return lines.ToArray();
        }

        /// <summary>替换点附近 ±3 行摘要（写后验证——返回目标段读回）</summary>
        /// <param name="content">归一化正文</param>
        /// <param name="index">命中起始索引</param>
        /// <returns>行摘要文本</returns>
        private static string SnippetAround(string content, int index)
        {
            if (index < 0)
            {
                index = 0;
            }
            if (index > content.Length)
            {
                index = content.Length;
            }
            int lineStart = index;
            int lineEnd = index;
            while (lineStart > 0 && content[lineStart - 1] != '\n')
            {
                lineStart = lineStart - 1;
            }
            while (lineEnd < content.Length && content[lineEnd] != '\n')
            {
                lineEnd = lineEnd + 1;
            }
            int start = lineStart;
            for (int i = 0; i < 3 && start > 0; i = i + 1)
            {
                int prev = content.LastIndexOf('\n', start - 1);
                if (prev < 0)
                {
                    start = 0;
                    break;
                }
                start = prev + 1;
            }
            int end = lineEnd;
            for (int i = 0; i < 3 && end < content.Length; i = i + 1)
            {
                int next = content.IndexOf('\n', end);
                if (next < 0)
                {
                    end = content.Length;
                    break;
                }
                end = next + 1;
            }
            return content.Substring(start, end - start);
        }

        /// <summary>
        /// 行数差自证——净删行必须出声（回执原只报「替换 N 处」，锚点吃掉的行使静默丢失）。
        /// 判据：old 行数 N → new 行数 M；M &lt; N 给 ⚠（删除标记的显式删除除外，只标注）；M &gt; N 只报差额（插入是常态）。
        /// 动机：2026-10-22 / 10-26 / 10-30 三起「锚点吃行」——Markdown 少行仍合法，编译 / 语法 / 渲染三验证面全绿，只有读回能抓。
        /// </summary>
        /// <param name="oldText">归一化锚点文本</param>
        /// <param name="newText">替换文本（删除标记已折为空串）</param>
        /// <param name="deleteMode">是否为显式删除标记</param>
        /// <param name="matchCount">本次替换处数（&gt;1 时行数差按「每次替换」表述，不冒充全文变化）</param>
        /// <returns>摘要片段（无需出声时为空串）</returns>
        private static string BuildLineShiftNote(string oldText, string newText, bool deleteMode, int matchCount)
        {
            int before = CountLinesOf(oldText);
            int after = CountLinesOf(newText);
            if (before == after)
            {
                return "";
            }
            string scope = matchCount > 1 ? " · 每次替换 行 " : " · 行 ";
            string note = scope + before.ToString() + "→" + after.ToString();
            if (deleteMode)
            {
                return note + "（删除标记·显式删除）";
            }
            if (after < before)
            {
                return note + " ⚠ 净删 " + (before - after).ToString() + " 行——锚点吃掉的每一行都要在 new 里交代";
            }
            return note;
        }

        /// <summary>
        /// 结构标记计数——表行（行首 |）· 表格分隔行（行首 |:-- 等）· 标题（行首 ##）· 分节线（整行 --- 或 ***）。
        /// 四类覆盖 Markdown 结构与章节结构的最小判据族；分隔行优先于表行判定（分隔行亦以 | 开头）。
        /// </summary>
        /// <param name="text">待检文本</param>
        /// <returns>长度 4 的计数数组（表行 / 分隔行 / 标题 / 分节线）</returns>
        private static int[] CountStructMarks(string text)
        {
            int[] marks = new int[4];
            if (text.Length == 0)
            {
                return marks;
            }
            string[] lines = text.Split('\n');
            for (int i = 0; i < lines.Length; i = i + 1)
            {
                string line = lines[i].Trim();
                if (line.Length == 0)
                {
                    continue;
                }
                if (line.StartsWith("|:--", StringComparison.Ordinal) || line.StartsWith("|-", StringComparison.Ordinal))
                {
                    marks[1] = marks[1] + 1;
                }
                else if (line[0] == '|')
                {
                    marks[0] = marks[0] + 1;
                }
                if (line.StartsWith("##", StringComparison.Ordinal))
                {
                    marks[2] = marks[2] + 1;
                }
                if (line == "---" || line == "***")
                {
                    marks[3] = marks[3] + 1;
                }
            }
            return marks;
        }

        /// <summary>
        /// 结构行守护——锚点内结构标记在 new 中减少即出声（告警不拒绝：改表为列表 / 删表头都是合法意图，只提示核对）。
        /// </summary>
        /// <param name="oldText">归一化锚点文本</param>
        /// <param name="newText">替换文本</param>
        /// <returns>摘要片段（无减少时为空串）</returns>
        private static string BuildStructMarkNote(string oldText, string newText)
        {
            int[] before = CountStructMarks(oldText);
            int[] after = CountStructMarks(newText);
            string[] names = new string[] { "表行", "表格分隔行", "标题", "分节线" };
            string parts = "";
            for (int i = 0; i < before.Length; i = i + 1)
            {
                if (after[i] < before[i])
                {
                    if (parts.Length > 0)
                    {
                        parts = parts + " / ";
                    }
                    parts = parts + names[i] + " " + before[i].ToString() + "→" + after[i].ToString();
                }
            }
            if (parts.Length == 0)
            {
                return "";
            }
            return " · 结构行 " + parts + " ⚠ 锚点吃掉结构行而未重建——请核对 new 是否漏抄（表格需分隔行、章节需标题）";
        }

        /// <summary>原文摘要——回执只显示替换后文本，被吃掉的原文完全不可见（单行整行替换时行数差为零，机械判据抓不到；2026-10-26 判例正是此形态）。</summary>
        /// <param name="oldText">归一化锚点文本</param>
        /// <returns>摘要片段（行数 + 首行，首行超 60 字符截断）</returns>
        private static string BuildOldScopeNote(string oldText)
        {
            string first = FirstLineOf(oldText);
            if (first.Length > OldScopeChars)
            {
                first = first.Substring(0, OldScopeChars) + "…";
            }
            return " · 原文 " + CountLinesOf(oldText).ToString() + " 行 ｜ 首行：" + first;
        }

        /// <summary>取首行——无换行则整串；空串返回空串</summary>
        /// <param name="text">待取文本</param>
        /// <returns>首行文本</returns>
        private static string FirstLineOf(string text)
        {
            int cut = text.IndexOf('\n');
            if (cut < 0)
            {
                return text;
            }
            return text.Substring(0, cut);
        }

        /// <summary>回执窗口自证——窗口只显示替换点前后 ≤3 行；文件更长时显式说明（集合缩水必须自证全集）</summary>
        /// <param name="replaced">替换后全文</param>
        /// <param name="snippet">已截取的窗口文本</param>
        /// <returns>自证行（无需说明时为空串）</returns>
        private static string BuildSnippetScopeNote(string replaced, string snippet)
        {
            int total = CountLinesOf(replaced);
            int shown = CountLinesOf(snippet.TrimEnd('\n'));
            if (total <= shown)
            {
                return "";
            }
            return "…（文件共 " + total.ToString() + " 行，此处只显示替换点前后 ≤3 行）\n";
        }

        /// <summary>匹配跨度行数——空串 0；否则换行数 + 1</summary>
        /// <param name="text">匹配文本</param>
        /// <returns>行数（空串 0）</returns>
        private static int CountLinesOf(string text)
        {
            if (text.Length == 0)
            {
                return 0;
            }
            int lines = 1;
            for (int i = 0; i < text.Length; i = i + 1)
            {
                if (text[i] == '\n')
                {
                    lines = lines + 1;
                }
            }
            return lines;
        }

        /// <summary>
        /// 结构化元数据头（统一口径·A214）——恒定 ok / tool + 主来源 target + 主计数 items（空串 / 负值 = 省略）。
        /// </summary>
        /// <param name="tool">工具名</param>
        /// <param name="ok">成败</param>
        /// <param name="target">主来源（空串 = 省略）</param>
        /// <param name="items">主计数（负值 = 省略）</param>
        /// <returns>单行 JSON</returns>
        private static string MetaHead(string tool, bool ok, string target, int items)
        {
            System.Collections.Generic.Dictionary<string, object> head = new System.Collections.Generic.Dictionary<string, object>();
            head["ok"] = ok;
            head["tool"] = tool;
            if (target.Length > 0)
            {
                head["target"] = target;
            }
            if (items >= 0)
            {
                head["items"] = items;
            }
            return JsonSerializer.Serialize(head);
        }
    }
}
// #MAU_CHECKSUM:SHA256:246466CFA3DA6BD524DDE4CC70C2371282F645EBFFAD930A82E5C4368E61FFF4
