using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using System.Text.Json;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.Formatting;
using Microsoft.CodeAnalysis.Options;
using Microsoft.CodeAnalysis.Text;

namespace Mau.Development
{
#pragma warning disable CS8600 // 环境 SDK 对 ConcurrentDictionary/Dictionary TryXXX(out TValue) 的 MaybeNullWhen 分析异常误报——out 实参非空变量也报警（最小复现验证）
    /// <summary>
    /// MauRoslynBridge 格式面——cs.format 空白 / 缩进规整（Roslyn Formatter，partial 分部）。
    /// 边界：只做空白与缩进规整（不做语法糖展开 / 单行块拆分 / 文档风格改写）——语义零变更由 token 流 + directive 双向校验保证。
    /// mode=check 干跑（差异清单，零写入——探针面）/ mode=apply 写盘（保真 BOM + 行尾按文件现状归一 + 写后回读自检，原子替换）。
    /// </summary>
    public sealed partial class MauRoslynBridge
    {
        /// <summary>
        /// cs.format——空白 / 缩进规整。参数：path（.cs 文件 / 目录 / csproj，受控根内）+ mode（check|apply，默认 check）。
        /// </summary>
        /// <param name="args">参数</param>
        /// <param name="result">结果</param>
        /// <returns>调用完成</returns>
        private bool ToolFormat(JsonElement args, out string result)
        {
            // [段1] 参数取出——校验归 Invoke 统一入口（ValidateToolArgs：未知 / 缺值 / 非法值一律 ERR|BAD_ARGS）
            string pathParam = Arg(args, "path");
            string mode = Arg(args, "mode");
            if (mode.Length == 0)
            {
                mode = "check";
            }

            // [段2] 入口路径解析——文件 / 目录 / csproj（受控根内）；存在性与类型分列出声（不存在 / 类型不支持不再落成静默空产物）
            string entry = ResolveInRoots(pathParam);
            if (entry.Length == 0)
            {
                result = "ERR|BAD_PATH|路径越界或为空（受控根内，支持 .cs 文件 / 目录 / csproj）: " + pathParam;
                return false;
            }
            if (!Directory.Exists(entry) && !File.Exists(entry))
            {
                result = "ERR|PATH_NOT_FOUND|路径不存在: " + entry;
                return false;
            }
            if (!Directory.Exists(entry) &&
                !entry.EndsWith(".cs", StringComparison.OrdinalIgnoreCase) &&
                !entry.EndsWith(".csproj", StringComparison.OrdinalIgnoreCase))
            {
                result = "ERR|ENTRY_UNSUPPORTED|入口类型不支持（支持 .cs 文件 / 目录 / csproj）: " + pathParam;
                return false;
            }

            // [段3] 目标文件收集——排除面内建（bin/obj/node_modules/Flows/Data/CatTemp/Mau-public + 生成物 FL_*/BRIKGROUP*）
            List<string> files = new List<string>();
            CollectCsFiles(entry, files);
            files.Sort(StringComparer.OrdinalIgnoreCase);
            if (files.Count == 0)
            {
                // 统一口径（A210）：无目标文件同样走结构化头（原 OK|FORMAT_ 纯文本特例退役）
                Dictionary<string, object> emptyMeta = new Dictionary<string, object>();
                emptyMeta["mode"] = mode;
                result = MetaHead("cs-format", true, RelativeToRoots(entry), 0, emptyMeta) + Environment.NewLine + Echo(RelativeToRoots(entry), "0 文件 · 无目标 .cs");
                return true;
            }

            // [段4] 行尾策略 + 逐文件规整——项目属性 eol（.editorconfig / .gitattributes 自入口向上查找）优先，
            //        未命中回落文件现状（多数优先）；结果文本整体归一后落盘，不允许 Roslyn 输出直接落盘
            string eolSource;
            string projectEol = DetectProjectNewline(entry, out eolSource);
            StringBuilder sb = new StringBuilder();
            int changedFiles = 0;
            int changedLines = 0;
            int writtenFiles = 0;
            int failedFiles = 0;
            int writeFailures = 0;
            for (int i = 0; i < files.Count; i = i + 1)
            {
                string file = files[i];
                string relative = RelativeToRoots(file);
                string source = File.ReadAllText(file);
                int crlfCount = 0;
                int lfCount = 0;
                string newline = DetectTargetNewline(source, out crlfCount, out lfCount);
                if (projectEol.Length > 0)
                {
                    if (projectEol == "crlf")
                    {
                        newline = "\r\n";
                    }
                    else
                    {
                        newline = "\n";
                    }
                }
                string formatted = NormalizeNewLineText(FormatText(source, file, newline), newline);
                if (string.Equals(formatted, source, StringComparison.Ordinal))
                {
                    continue;
                }
                string failure = VerifyFormat(source, formatted, file, newline);
                if (failure.Length > 0)
                {
                    failedFiles = failedFiles + 1;
                    sb.AppendLine("VERIFY_FAIL|" + relative + "|" + failure);
                    continue;
                }
                int lines = CountChangedLines(source, formatted);
                changedFiles = changedFiles + 1;
                changedLines = changedLines + lines;
                if (mode == "apply")
                {
                    string writeFailure = WriteFilePreserving(file, formatted, HasUtf8Bom(file), newline);
                    writtenFiles = writtenFiles + 1;
                    if (writeFailure.Length > 0)
                    {
                        writeFailures = writeFailures + 1;
                        sb.AppendLine("WRITE_FAIL|" + relative + "|" + writeFailure);
                    }
                }
                sb.AppendLine(relative + " | lines=" + lines.ToString());
                if (crlfCount > 0 && lfCount > 0)
                {
                    if (projectEol.Length > 0)
                    {
                        sb.AppendLine("MIXED|" + relative + "|原文件行尾混合（CRLF " + crlfCount + " / LF " + lfCount + "）→ 按项目属性 eol=" + projectEol + " 归一为 " + NewlineLabel(newline) + "（来源 " + RelativeToRoots(eolSource) + "）");
                    }
                    else
                    {
                        sb.AppendLine("MIXED|" + relative + "|原文件行尾混合（CRLF " + crlfCount + " / LF " + lfCount + "）→ 按多数归一为 " + NewlineLabel(newline));
                    }
                }
            }

            // 统一口径（A210）：头（ok/tool/target/items + mode/变更族/eol）+ 摘要行；成功零差异不再带明细与行尾策略行
            Dictionary<string, object> fmtMeta = new Dictionary<string, object>();
            fmtMeta["mode"] = mode;
            fmtMeta["changedFiles"] = changedFiles;
            fmtMeta["changedLines"] = changedLines;
            fmtMeta["failedFiles"] = failedFiles;
            if (projectEol.Length > 0)
            {
                fmtMeta["eol"] = projectEol;
                fmtMeta["eolSource"] = RelativeToRoots(eolSource);
            }
            else
            {
                fmtMeta["eol"] = "file";
            }
            if (mode == "apply")
            {
                fmtMeta["writtenFiles"] = writtenFiles;
                fmtMeta["writeFailures"] = writeFailures;
            }
            string fmtTarget = RelativeToRoots(entry);
            StringBuilder output = new StringBuilder();
            output.Append(MetaHead("cs-format", failedFiles == 0 && writeFailures == 0, fmtTarget, files.Count, fmtMeta));
            output.AppendLine();
            output.AppendLine(Echo(fmtTarget, files.Count + " 文件 · " + (changedFiles == 0 ? "零差异" : changedFiles + " 文件变更 " + changedLines + " 行")));
            if (changedFiles > 0 || failedFiles > 0 || writeFailures > 0)
            {
                if (projectEol.Length > 0)
                {
                    output.AppendLine("── 行尾策略：项目属性 eol=" + projectEol + "（来源 " + RelativeToRoots(eolSource) + "）──");
                }
                else
                {
                    output.AppendLine("── 行尾策略：按各文件现状多数归一（未发现 .editorconfig / .gitattributes 的 eol 声明）──");
                }
                output.Append(sb.ToString());
            }
            result = TrimResult(output.ToString(), MaxResultChars);
            return true;
        }

        /// <summary>
        /// 目标文件收集——.cs 文件 / 目录递归 / csproj。
        /// csproj 走**项目源文件集**（与扫描面同一实现 CollectProjectSources——DefaultItemExcludes 子项目目录排除生效，
        /// 不含 obj 生成物）；.cs 与目录入口保持文件树语义（目录 = 显式全树意图，只排内建忽略面）。
        /// </summary>
        /// <param name="entry">入口绝对路径</param>
        /// <param name="files">收集结果</param>
        private void CollectCsFiles(string entry, List<string> files)
        {
            if (File.Exists(entry))
            {
                if (entry.EndsWith(".csproj", StringComparison.OrdinalIgnoreCase))
                {
                    string[] sources = CollectProjectSources(entry, false);
                    for (int i = 0; i < sources.Length; i = i + 1)
                    {
                        files.Add(sources[i]);
                    }
                    return;
                }
                if (entry.EndsWith(".cs", StringComparison.OrdinalIgnoreCase))
                {
                    files.Add(entry);
                }
                return;
            }
            if (Directory.Exists(entry))
            {
                CollectDirectory(entry, files);
            }
        }

        /// <summary>
        /// 目录递归收集——排除 bin/obj/node_modules/Flows/Data/CatTemp/Mau-public 与生成物（FL_* / BRIKGROUP*）。
        /// </summary>
        /// <param name="dir">目录</param>
        /// <param name="files">收集结果</param>
        private static void CollectDirectory(string dir, List<string> files)
        {
            string[] found = Directory.GetFiles(dir, "*.cs");
            for (int i = 0; i < found.Length; i = i + 1)
            {
                string name = Path.GetFileName(found[i]);
                if (name.StartsWith("FL_", StringComparison.Ordinal) || name.StartsWith("BRIKGROUP", StringComparison.Ordinal))
                {
                    continue;
                }
                files.Add(found[i]);
            }
            string[] subdirs = Directory.GetDirectories(dir);
            for (int i = 0; i < subdirs.Length; i = i + 1)
            {
                string name = Path.GetFileName(subdirs[i]);
                if (name.Length == 0 || name[0] == '.')
                {
                    continue;
                }
                string lower = name.ToLowerInvariant();
                if (lower == "bin" || lower == "obj" || lower == "node_modules" || lower == "flows" || lower == "data" || lower == "cattemp" || lower == "mau-public")
                {
                    continue;
                }
                CollectDirectory(subdirs[i], files);
            }
        }

        /// <summary>
        /// 相对受控根路径——诊断输出可读性（不在任何根内时回落绝对路径）。
        /// </summary>
        /// <param name="filePath">文件绝对路径</param>
        /// <returns>相对路径</returns>
        private string RelativeToRoots(string filePath)
        {
            for (int i = 0; i < _roots.Length; i = i + 1)
            {
                if (filePath.StartsWith(_roots[i] + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase))
                {
                    return filePath.Substring(_roots[i].Length + 1);
                }
            }
            return filePath;
        }

        /// <summary>
        /// 文本规整——AdhocWorkspace + Formatter（4 空格缩进 / 显式换行；只调空白与缩进，不动 token）。
        /// </summary>
        /// <param name="text">源码文本</param>
        /// <param name="path">文件路径（诊断定位用）</param>
        /// <param name="newline">目标换行</param>
        /// <returns>规整后文本</returns>
        private static string FormatText(string text, string path, string newline)
        {
            using (AdhocWorkspace workspace = new AdhocWorkspace())
            {
                OptionSet options = FormatOptions(workspace, newline);
                SourceText sourceText = SourceText.From(text, Encoding.UTF8);
                Project project = workspace.AddProject("cs-format", LanguageNames.CSharp);
                Document document = project.AddDocument(path, sourceText);
                Document formatted = Formatter.FormatAsync(document, options).GetAwaiter().GetResult();
                return formatted.GetTextAsync().GetAwaiter().GetResult().ToString();
            }
        }

        /// <summary>
        /// 写前校验——token 流等价 + directive 等价 + 幂等（任一不满足即拒写）。
        /// 动机：Formatter 只应改空白；token 流等价 ⊆ 语义等价，且比编译验证便宜（零编译成本）。
        /// 幂等判定同样按归一后文本比较——否则 Formatter 输出的行尾差异会伪装成「非幂等」。
        /// </summary>
        /// <param name="source">原文</param>
        /// <param name="formatted">规整后</param>
        /// <param name="path">文件路径</param>
        /// <param name="newline">目标换行</param>
        /// <returns>失败原因（空=通过）</returns>
        private static string VerifyFormat(string source, string formatted, string path, string newline)
        {
            if (!TokensEqual(source, formatted))
            {
                return "token 流不等价（规整动了代码而非空白）";
            }
            if (!DirectivesEqual(source, formatted))
            {
                return "预处理指令不等价（#if/#pragma 面变动）";
            }
            string again = NormalizeNewLineText(FormatText(formatted, path, newline), newline);
            if (!string.Equals(again, formatted, StringComparison.Ordinal))
            {
                return "非幂等（二次规整仍有变化）";
            }
            return "";
        }

        /// <summary>
        /// token 流等价——Kind + ValueText 序列（trivia 无关）。
        /// </summary>
        /// <param name="a">原文</param>
        /// <param name="b">对照文本</param>
        /// <returns>等价</returns>
        private static bool TokensEqual(string a, string b)
        {
            List<string> left = TokenList(a);
            List<string> right = TokenList(b);
            if (left.Count != right.Count)
            {
                return false;
            }
            for (int i = 0; i < left.Count; i = i + 1)
            {
                if (!string.Equals(left[i], right[i], StringComparison.Ordinal))
                {
                    return false;
                }
            }
            return true;
        }

        /// <summary>
        /// token 序列提取。
        /// </summary>
        /// <param name="text">文本</param>
        /// <returns>序列</returns>
        private static List<string> TokenList(string text)
        {
            List<string> list = new List<string>();
            SyntaxNode root = CSharpSyntaxTree.ParseText(text).GetRoot();
            foreach (SyntaxToken token in root.DescendantTokens())
            {
                list.Add(token.Kind().ToString() + "|" + token.ValueText);
            }
            return list;
        }

        /// <summary>
        /// directive 等价——预处理指令序列（trivia 面，token 流不含）。
        /// </summary>
        /// <param name="a">原文</param>
        /// <param name="b">对照文本</param>
        /// <returns>等价</returns>
        private static bool DirectivesEqual(string a, string b)
        {
            List<string> left = DirectiveList(a);
            List<string> right = DirectiveList(b);
            if (left.Count != right.Count)
            {
                return false;
            }
            for (int i = 0; i < left.Count; i = i + 1)
            {
                if (!string.Equals(left[i], right[i], StringComparison.Ordinal))
                {
                    return false;
                }
            }
            return true;
        }

        /// <summary>
        /// directive 序列提取——Kind + 去空白文本。
        /// </summary>
        /// <param name="text">文本</param>
        /// <returns>序列</returns>
        private static List<string> DirectiveList(string text)
        {
            List<string> list = new List<string>();
            SyntaxNode root = CSharpSyntaxTree.ParseText(text).GetRoot();
            foreach (SyntaxTrivia trivia in root.DescendantTrivia())
            {
                if (trivia.IsDirective)
                {
                    list.Add(trivia.Kind().ToString() + "|" + trivia.ToString().Trim());
                }
            }
            return list;
        }

        /// <summary>
        /// 目标行尾探测——多数行尾优先（CRLF / LF 计数取多），平局回退首个出现的行尾；无任何换行回退平台默认。
        /// 动机：Roslyn Formatter 对既有行保留原行尾、新插入行用 options 换行 ⇒ 单文件内混行；目标行尾必须取自文件现状而非平台常量。
        /// </summary>
        /// <param name="text">原文件文本</param>
        /// <param name="crlfCount">输出——CRLF 行尾数</param>
        /// <param name="lfCount">输出——LF 行尾数</param>
        /// <returns>换行串（\r\n 或 \n）</returns>
        private static string DetectTargetNewline(string text, out int crlfCount, out int lfCount)
        {
            crlfCount = 0;
            lfCount = 0;
            string first = "";
            for (int i = 0; i < text.Length; i = i + 1)
            {
                if (text[i] != '\n')
                {
                    continue;
                }
                bool crlf = i > 0 && text[i - 1] == '\r';
                if (crlf)
                {
                    crlfCount = crlfCount + 1;
                }
                else
                {
                    lfCount = lfCount + 1;
                }
                if (first.Length == 0)
                {
                    first = crlf ? "\r\n" : "\n";
                }
            }
            if (first.Length == 0)
            {
                return Environment.NewLine;
            }
            if (crlfCount > lfCount)
            {
                return "\r\n";
            }
            if (lfCount > crlfCount)
            {
                return "\n";
            }
            return first;
        }
        /// <summary>
        /// 项目行尾探测——自入口目录向上查找 .editorconfig / .gitattributes 的 eol 声明（跨仓场景）。
        /// 语义：命中即以此为规整目标行尾（覆盖文件现状多数）；未命中返回空串（回落文件现状多数）。
        /// </summary>
        /// <param name="entry">入口路径（文件或目录）</param>
        /// <param name="source">输出——命中声明所在文件路径（未命中空串）</param>
        /// <returns>crlf / lf / 空串</returns>
        internal static string DetectProjectNewline(string entry, out string source)
        {
            source = "";
            string dir = entry;
            if (File.Exists(entry))
            {
                dir = Path.GetDirectoryName(entry);
            }
            for (int depth = 0; depth < 12; depth = depth + 1)
            {
                if (dir == null || dir.Length == 0)
                {
                    break;
                }
                string editorPath = Path.Combine(dir, ".editorconfig");
                if (File.Exists(editorPath))
                {
                    string editorEol = ReadEditorConfigEol(editorPath);
                    if (editorEol.Length > 0)
                    {
                        source = editorPath;
                        return editorEol;
                    }
                }
                string attrPath = Path.Combine(dir, ".gitattributes");
                if (File.Exists(attrPath))
                {
                    string attrEol = ReadGitAttributesEol(attrPath);
                    if (attrEol.Length > 0)
                    {
                        source = attrPath;
                        return attrEol;
                    }
                }
                DirectoryInfo parent = Directory.GetParent(dir);
                if (parent == null)
                {
                    break;
                }
                dir = parent.FullName;
            }
            return "";
        }
        /// <summary>
        /// .editorconfig 的 end_of_line 读取——取最后一条适用于 .cs（`*` / `*.cs` 族）段的声明。
        /// </summary>
        /// <param name="path">.editorconfig 路径</param>
        /// <returns>crlf / lf / 空串</returns>
        internal static string ReadEditorConfigEol(string path)
        {
            string[] lines = File.ReadAllLines(path);
            bool applies = false;
            string result = "";
            for (int i = 0; i < lines.Length; i = i + 1)
            {
                string line = lines[i].Trim();
                if (line.Length == 0 || line.StartsWith("#", StringComparison.Ordinal) || line.StartsWith(";", StringComparison.Ordinal))
                {
                    continue;
                }
                if (line.StartsWith("[", StringComparison.Ordinal) && line.EndsWith("]", StringComparison.Ordinal))
                {
                    applies = SectionAppliesToCs(line.Substring(1, line.Length - 2));
                    continue;
                }
                if (!applies)
                {
                    continue;
                }
                int eq = line.IndexOf('=');
                if (eq <= 0)
                {
                    continue;
                }
                string key = line.Substring(0, eq).Trim().ToLowerInvariant();
                if (key != "end_of_line")
                {
                    continue;
                }
                string value = line.Substring(eq + 1).Trim().ToLowerInvariant();
                if (value == "crlf" || value == "lf")
                {
                    result = value;
                }
            }
            return result;
        }
        /// <summary>
        /// 段是否适用于 .cs——`*` 或 `.cs` 结尾的模式（含 `{*.cs,*.csx}` 逗号列表形态）。
        /// </summary>
        /// <param name="section">段名（去方括号）</param>
        /// <returns>true=适用</returns>
        internal static bool SectionAppliesToCs(string section)
        {
            string[] tokens = section.Split(',');
            for (int i = 0; i < tokens.Length; i = i + 1)
            {
                string token = tokens[i].Trim().Trim('{', '}', '[', ']').Trim();
                if (token == "*" || token.EndsWith(".cs", StringComparison.Ordinal))
                {
                    return true;
                }
            }
            return false;
        }
        /// <summary>
        /// .gitattributes 的 eol 读取——取最后一条适用于 .cs（`*` / `*.cs`）的 `eol=` 属性。
        /// </summary>
        /// <param name="path">.gitattributes 路径</param>
        /// <returns>crlf / lf / 空串</returns>
        internal static string ReadGitAttributesEol(string path)
        {
            string[] lines = File.ReadAllLines(path);
            string result = "";
            for (int i = 0; i < lines.Length; i = i + 1)
            {
                string line = lines[i].Trim();
                if (line.Length == 0 || line.StartsWith("#", StringComparison.Ordinal))
                {
                    continue;
                }
                string[] parts = line.Split(new char[] { ' ', '\t' }, StringSplitOptions.RemoveEmptyEntries);
                if (parts.Length < 2)
                {
                    continue;
                }
                if (parts[0] != "*" && !parts[0].EndsWith(".cs", StringComparison.Ordinal))
                {
                    continue;
                }
                for (int j = 1; j < parts.Length; j = j + 1)
                {
                    string token = parts[j].ToLowerInvariant();
                    if (token == "eol=crlf")
                    {
                        result = "crlf";
                    }
                    else if (token == "eol=lf")
                    {
                        result = "lf";
                    }
                }
            }
            return result;
        }

        /// <summary>
        /// 行尾文案——诊断输出用（CRLF / LF）
        /// </summary>
        /// <param name="newline">换行串</param>
        /// <returns>文案</returns>
        private static string NewlineLabel(string newline)
        {
            if (newline == "\r\n")
            {
                return "CRLF";
            }
            return "LF";
        }

        /// <summary>
        /// 共享读文本——FileShare.ReadWrite 打开（活跃文件可读，避免占用冲突）；BOM 自动剥离。
        /// </summary>
        /// <param name="path">文件路径</param>
        /// <returns>文本</returns>
        private static string ReadTextShared(string path)
        {
            using (FileStream stream = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite))
            {
                using (StreamReader reader = new StreamReader(stream, Encoding.UTF8, true))
                {
                    return reader.ReadToEnd();
                }
            }
        }

        /// <summary>
        /// 行尾一致性检查——目标 CRLF 时不得残留独立 LF，目标 LF 时不得残留 CRLF；返回不一致描述（空=一致）。
        /// </summary>
        /// <param name="text">读回文本</param>
        /// <param name="newline">目标换行</param>
        /// <returns>不一致描述（空=一致）</returns>
        private static string CheckNewlineUniform(string text, string newline)
        {
            int crlfCount = 0;
            int lfCount = 0;
            for (int i = 0; i < text.Length; i = i + 1)
            {
                if (text[i] != '\n')
                {
                    continue;
                }
                if (i > 0 && text[i - 1] == '\r')
                {
                    crlfCount = crlfCount + 1;
                }
                else
                {
                    lfCount = lfCount + 1;
                }
            }
            if (newline == "\r\n" && lfCount > 0)
            {
                return "残留 LF " + lfCount + " 处";
            }
            if (newline == "\n" && crlfCount > 0)
            {
                return "残留 CRLF " + crlfCount + " 处";
            }
            return "";
        }

        /// <summary>
        /// UTF-8 BOM 探测——保真写回（有则保留，无则不添加）。
        /// </summary>
        /// <param name="path">文件路径</param>
        /// <returns>有 BOM</returns>
        private static bool HasUtf8Bom(string path)
        {
            using (FileStream stream = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite))
            {
                int b0 = stream.ReadByte();
                int b1 = stream.ReadByte();
                int b2 = stream.ReadByte();
                return b0 == 0xEF && b1 == 0xBB && b2 == 0xBF;
            }
        }

        /// <summary>
        /// 保真原子落盘——临时文件 + Move 覆盖（防半截写入）；编码 / BOM 按原文状态；行尾由调用方显式指定（空串=按原文件多数归一，Roslyn 输出不直接落盘）+ 写后回读自检。
        /// </summary>
        /// <param name="path">目标文件</param>
        /// <param name="text">完整新内容</param>
        /// <param name="bom">是否写 UTF-8 BOM</param>
        /// <param name="newlineOverride">显式目标行尾（空串=按原文件多数归一）</param>
        /// <returns>写盘诊断（空=正常；非空=行尾自检不一致描述）</returns>
        private static string WriteFilePreserving(string path, string text, bool bom, string newlineOverride)
        {
            // [段1] 目标行尾——调用方显式指定优先（cs-format 项目属性 eol）；否则原文件多数行尾（文件缺失 / 无换行回退平台默认）
            string newline = newlineOverride;
            if (newline.Length == 0)
            {
                newline = Environment.NewLine;
                if (File.Exists(path))
                {
                    int crlfCount = 0;
                    int lfCount = 0;
                    newline = DetectTargetNewline(ReadTextShared(path), out crlfCount, out lfCount);
                }
            }
            string payload = NormalizeNewLineText(text, newline);
            // [段2] 原子写——临时文件 + Move 覆盖（防半截写入）
            string temporary = path + "." + Guid.NewGuid().ToString("N") + ".tmp";
            try
            {
                File.WriteAllText(temporary, payload, new UTF8Encoding(bom));
                Mau.Runtime.PosixFileMode.PreserveForReplace(path, temporary);
                File.Move(temporary, path, true);
            }
            finally
            {
                if (File.Exists(temporary))
                {
                    File.Delete(temporary);
                }
            }
            // [段3] 写后回读自检——行尾形态与目标不一致即报告（失败必须可见，不静默）
            string mismatch = CheckNewlineUniform(ReadTextShared(path), newline);
            if (mismatch.Length > 0)
            {
                return "写盘后行尾自检不一致（期望 " + NewlineLabel(newline) + "）: " + mismatch;
            }
            return "";
        }

        /// <summary>
        /// 差异行数——按行对齐比较（行内空白差异计入）。
        /// </summary>
        /// <param name="a">原文</param>
        /// <param name="b">对照文本</param>
        /// <returns>不同行数</returns>
        private static int CountChangedLines(string a, string b)
        {
            string[] left = NormalizeNewLineText(a, "\n").Split('\n');
            string[] right = NormalizeNewLineText(b, "\n").Split('\n');
            int count = 0;
            int total = left.Length > right.Length ? left.Length : right.Length;
            for (int i = 0; i < total; i = i + 1)
            {
                string x = i < left.Length ? left[i] : "";
                string y = i < right.Length ? right[i] : "";
                if (!string.Equals(x, y, StringComparison.Ordinal))
                {
                    count = count + 1;
                }
            }
            return count;
        }
    }
}
