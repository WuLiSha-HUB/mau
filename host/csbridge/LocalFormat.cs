using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Reflection;
using System.Text;
using System.Text.Json;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.Formatting;
using Microsoft.CodeAnalysis.Host.Mef;
using Microsoft.CodeAnalysis.Options;
using Microsoft.CodeAnalysis.Text;
using Mau.Development;
using Mau.Runtime;

namespace MauCsBridge;

/// <summary>
/// 桥侧本地 cs-format——MefHostServices 显式装配（mauout 无 Features / CSharp.Features 程序集，
/// AdhocWorkspace 默认 host 不含 C# 格式化服务 → NotSupportedException「语言"C#"不受支持」）。
/// 只读引用 Roslyn dll（不复制、不改 mau / mauout 仓库）；契约对齐 MauRoslynBridge.Format.cs：
/// path（.cs 文件 / 目录 / csproj，受控根内）+ mode（check|apply，默认 check），A214 头 + 摘要行 + 明细。
/// 边界：只调空白与缩进（token 流 + directive 双向校验保证语义零变更）；csproj 入口按同目录递归收集（已知近似）。
/// </summary>
public static class LocalFormat
{
    private const int MaxResultChars = 20000;

    private static readonly object HostLock = new object();
    private static MefHostServices? _host;

    /// <summary>
    /// MEF 宿主——DefaultAssemblies + CSharp.Workspaces（显式加入格式化服务所在程序集）。
    /// </summary>
    private static MefHostServices Host
    {
        get
        {
            lock (HostLock)
            {
                if (_host == null)
                {
                    List<Assembly> assemblies = new List<Assembly>(MefHostServices.DefaultAssemblies)
                    {
                        typeof(Formatter).Assembly,
                        typeof(CSharpSyntaxTree).Assembly
                    };
                    // CSharp.Workspaces（格式化服务所在程序集）——按文件显式加载，缺失时保留其余（失败可见：格式化会报语言不支持）
                    string[] extra =
                    {
                        "Microsoft.CodeAnalysis.Workspaces.dll",
                        "Microsoft.CodeAnalysis.CSharp.Workspaces.dll",
                        "System.Composition.Runtime.dll",
                        "System.Composition.AttributedModel.dll",
                        "System.Composition.TypedParts.dll",
                        "System.Composition.Hosting.dll",
                        "System.Composition.Convention.dll"
                    };
                    for (int i = 0; i < extra.Length; i++)
                    {
                        string path = Path.Combine(AppContext.BaseDirectory, extra[i]);
                        if (!File.Exists(path))
                        {
                            continue;
                        }
                        try
                        {
                            assemblies.Add(Assembly.LoadFrom(path));
                        }
                        catch (Exception)
                        {
                            // 单个依赖加载失败不阻断宿主构建（错误在 FormatAsync 处显式暴露）
                        }
                    }
                    _host = MefHostServices.Create(assemblies.Distinct());
                }
                return _host;
            }
        }
    }

    /// <summary>
    /// 执行 cs-format——解析参数 → 路径解析 → 逐文件规整 / 校验 / 写盘 → A214 回执。
    /// </summary>
    /// <param name="argsJson">工具参数 JSON（path / mode）</param>
    /// <param name="roots">受控根表</param>
    /// <param name="result">回执文本</param>
    /// <returns>true=执行完成（含校验失败的文件级失败——文件级失败经头部 ok=false 表达）</returns>
    public static bool Invoke(string argsJson, WorkspaceConfig.RootEntry[] roots, out string result)
    {
        result = "";
        // [段1] 参数面零容忍——未知键 / 缺 path / mode 非法一律 ERR|BAD_ARGS
        string pathParam = "";
        string mode = "";
        try
        {
            using JsonDocument doc = JsonDocument.Parse(argsJson);
            if (doc.RootElement.ValueKind != JsonValueKind.Object)
            {
                result = "ERR|BAD_ARGS|参数必须是 JSON 对象";
                return false;
            }
            foreach (JsonProperty property in doc.RootElement.EnumerateObject())
            {
                if (property.Name != "path" && property.Name != "mode")
                {
                    result = "ERR|BAD_ARGS|未知参数: " + property.Name + "（支持 path mode）";
                    return false;
                }
            }
            if (doc.RootElement.TryGetProperty("path", out JsonElement pathElement) && pathElement.ValueKind == JsonValueKind.String)
            {
                pathParam = pathElement.GetString() ?? "";
            }
            if (doc.RootElement.TryGetProperty("mode", out JsonElement modeElement) && modeElement.ValueKind == JsonValueKind.String)
            {
                mode = modeElement.GetString() ?? "";
            }
        }
        catch (Exception ex)
        {
            result = "ERR|BAD_ARGS|参数 JSON 解析失败: " + ex.Message;
            return false;
        }
        pathParam = pathParam.Trim();
        if (pathParam.Length == 0)
        {
            result = "ERR|BAD_ARGS|缺少参数 path（必填：path mode）";
            return false;
        }
        if (mode.Length == 0)
        {
            mode = "check";
        }
        if (mode != "check" && mode != "apply")
        {
            result = "ERR|BAD_ARGS|mode 非法值: " + mode + "（check|apply）";
            return false;
        }

        // [段2] 入口路径解析——文件 / 目录 / csproj（受控根内）
        string entry = ResolveInRoots(pathParam, roots);
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

        // [段3] 目标文件收集——目录递归排除内建忽略面与生成物；csproj 按同目录递归（已知近似）
        List<string> files = new List<string>();
        CollectCsFiles(entry, files);
        files.Sort(StringComparer.OrdinalIgnoreCase);
        string target = RelativeToRoots(entry, roots);
        if (files.Count == 0)
        {
            result = MetaHead("cs-format", true, target, 0, new (string, object)[] { ("mode", mode) }) +
                     Environment.NewLine + Echo(target, "0 文件 · 无目标 .cs");
            return true;
        }

        // [段4] 行尾策略 + 逐文件规整——项目 eol（.editorconfig / .gitattributes）优先，未命中按文件多数
        string eolSource;
        string projectEol = DetectProjectNewline(entry, out eolSource);
        StringBuilder sb = new StringBuilder();
        int changedFiles = 0;
        int changedLines = 0;
        int writtenFiles = 0;
        int failedFiles = 0;
        int writeFailures = 0;
        for (int i = 0; i < files.Count; i++)
        {
            string file = files[i];
            string relative = RelativeToRoots(file, roots);
            string source = File.ReadAllText(file);
            int crlfCount;
            int lfCount;
            string newline = DetectTargetNewline(source, out crlfCount, out lfCount);
            if (projectEol.Length > 0)
            {
                newline = projectEol == "crlf" ? "\r\n" : "\n";
            }
            string formatted = NormalizeNewLineText(FormatText(source, file, newline), newline);
            if (string.Equals(formatted, source, StringComparison.Ordinal))
            {
                continue;
            }
            string failure = VerifyFormat(source, formatted, file, newline);
            if (failure.Length > 0)
            {
                failedFiles++;
                sb.AppendLine("VERIFY_FAIL|" + relative + "|" + failure);
                continue;
            }
            int lines = CountChangedLines(source, formatted);
            changedFiles++;
            changedLines += lines;
            if (mode == "apply")
            {
                string writeFailure = WriteFilePreserving(file, formatted, HasUtf8Bom(file), newline);
                writtenFiles++;
                if (writeFailure.Length > 0)
                {
                    writeFailures++;
                    sb.AppendLine("WRITE_FAIL|" + relative + "|" + writeFailure);
                }
            }
            sb.AppendLine(relative + " | lines=" + lines);
            if (crlfCount > 0 && lfCount > 0)
            {
                if (projectEol.Length > 0)
                {
                    sb.AppendLine("MIXED|" + relative + "|原文件行尾混合（CRLF " + crlfCount + " / LF " + lfCount + "）→ 按项目属性 eol=" + projectEol + " 归一为 " + NewlineLabel(newline) + "（来源 " + RelativeToRoots(eolSource, roots) + "）");
                }
                else
                {
                    sb.AppendLine("MIXED|" + relative + "|原文件行尾混合（CRLF " + crlfCount + " / LF " + lfCount + "）→ 按多数归一为 " + NewlineLabel(newline));
                }
            }
        }

        // [段5] A214 回执——头（ok/tool/target/items + mode/变更族/eol）+ 摘要行 + 明细
        List<(string, object)> meta = new List<(string, object)>
        {
            ("mode", mode),
            ("changedFiles", changedFiles),
            ("changedLines", changedLines),
            ("failedFiles", failedFiles)
        };
        if (projectEol.Length > 0)
        {
            meta.Add(("eol", projectEol));
            meta.Add(("eolSource", RelativeToRoots(eolSource, roots)));
        }
        else
        {
            meta.Add(("eol", "file"));
        }
        if (mode == "apply")
        {
            meta.Add(("writtenFiles", writtenFiles));
            meta.Add(("writeFailures", writeFailures));
        }
        StringBuilder output = new StringBuilder();
        output.Append(MetaHead("cs-format", failedFiles == 0 && writeFailures == 0, target, files.Count, meta.ToArray()));
        output.AppendLine();
        output.AppendLine(Echo(target, files.Count + " 文件 · " + (changedFiles == 0 ? "零差异" : changedFiles + " 文件变更 " + changedLines + " 行")));
        if (changedFiles > 0 || failedFiles > 0 || writeFailures > 0)
        {
            if (projectEol.Length > 0)
            {
                output.AppendLine("── 行尾策略：项目属性 eol=" + projectEol + "（来源 " + RelativeToRoots(eolSource, roots) + "）──");
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
    /// 路径解析——受控根 id:rel 或绝对路径；结果必须落在某个根内（越界返回空串）。
    /// </summary>
    private static string ResolveInRoots(string raw, WorkspaceConfig.RootEntry[] roots)
    {
        string value = raw.Trim();
        if (value.Length == 0)
        {
            return "";
        }
        int colon = value.IndexOf(':');
        if (!Path.IsPathRooted(value) && colon > 0)
        {
            string id = value.Substring(0, colon);
            string rest = value.Substring(colon + 1);
            for (int i = 0; i < roots.Length; i++)
            {
                if (!string.Equals(roots[i].Id, id, StringComparison.OrdinalIgnoreCase))
                {
                    continue;
                }
                string combined = Path.GetFullPath(Path.Combine(roots[i].Path, rest.Replace('/', Path.DirectorySeparatorChar)));
                return IsUnder(combined, roots[i].Path) ? combined : "";
            }
            return "";
        }
        string absolute = Path.GetFullPath(value);
        for (int i = 0; i < roots.Length; i++)
        {
            if (IsUnder(absolute, roots[i].Path))
            {
                return absolute;
            }
        }
        return "";
    }

    /// <summary>路径是否在根内（含根本身）。</summary>
    private static bool IsUnder(string path, string root)
    {
        string normalizedRoot = Path.GetFullPath(root).TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar);
        string normalizedPath = Path.GetFullPath(path);
        return normalizedPath.Equals(normalizedRoot, StringComparison.OrdinalIgnoreCase) ||
               normalizedPath.StartsWith(normalizedRoot + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase);
    }

    /// <summary>相对受控根路径——不在任何根内时回落绝对路径。</summary>
    private static string RelativeToRoots(string filePath, WorkspaceConfig.RootEntry[] roots)
    {
        for (int i = 0; i < roots.Length; i++)
        {
            if (IsUnder(filePath, roots[i].Path))
            {
                string root = Path.GetFullPath(roots[i].Path).TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar);
                string full = Path.GetFullPath(filePath);
                if (full.Equals(root, StringComparison.OrdinalIgnoreCase))
                {
                    return "";
                }
                return full.Substring(root.Length + 1);
            }
        }
        return filePath;
    }

    /// <summary>目标文件收集——.cs 文件 / 目录递归 / csproj 同目录递归。</summary>
    private static void CollectCsFiles(string entry, List<string> files)
    {
        if (File.Exists(entry))
        {
            if (entry.EndsWith(".csproj", StringComparison.OrdinalIgnoreCase))
            {
                CollectDirectory(Path.GetDirectoryName(entry) ?? entry, files);
                return;
            }
            if (entry.EndsWith(".cs", StringComparison.OrdinalIgnoreCase))
            {
                files.Add(entry);
            }
            return;
        }
        CollectDirectory(entry, files);
    }

    /// <summary>目录递归收集——排除 bin/obj/node_modules/Flows/Data/CatTemp/Mau-public 与生成物。</summary>
    private static void CollectDirectory(string dir, List<string> files)
    {
        string[] found = Directory.GetFiles(dir, "*.cs");
        for (int i = 0; i < found.Length; i++)
        {
            string name = Path.GetFileName(found[i]);
            if (name.StartsWith("FL_", StringComparison.Ordinal) || name.StartsWith("BRIKGROUP", StringComparison.Ordinal))
            {
                continue;
            }
            files.Add(found[i]);
        }
        string[] subdirs = Directory.GetDirectories(dir);
        for (int i = 0; i < subdirs.Length; i++)
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
    /// 文本规整——MefHostServices 显式装配 + AdhocWorkspace + Formatter（4 空格缩进 / 显式换行；只动空白）。
    /// </summary>
    private static string FormatText(string text, string path, string newline)
    {
        using AdhocWorkspace workspace = new AdhocWorkspace(Host);
        OptionSet options = FormatOptions(workspace, newline);
        SourceText sourceText = SourceText.From(text, Encoding.UTF8);
        Project project = workspace.AddProject("cs-format", LanguageNames.CSharp);
        Document document = project.AddDocument(path, sourceText);
        Document formatted = Formatter.FormatAsync(document, options).GetAwaiter().GetResult();
        return formatted.GetTextAsync().GetAwaiter().GetResult().ToString();
    }

    /// <summary>格式选项——4 空格缩进 / 显式换行（与 C# cs.format 同源配置）。</summary>
    private static OptionSet FormatOptions(Workspace workspace, string newline)
    {
        return workspace.Options
            .WithChangedOption(FormattingOptions.UseTabs, LanguageNames.CSharp, false)
            .WithChangedOption(FormattingOptions.TabSize, LanguageNames.CSharp, 4)
            .WithChangedOption(FormattingOptions.IndentationSize, LanguageNames.CSharp, 4)
            .WithChangedOption(FormattingOptions.NewLine, LanguageNames.CSharp, newline);
    }

    /// <summary>写前校验——token 流等价 + directive 等价 + 幂等（任一不满足即拒写）。</summary>
    private static string VerifyFormat(string source, string formatted, string path, string newline)
    {
        if (!SequenceEqual(TokenList(source), TokenList(formatted)))
        {
            return "token 流不等价（规整动了代码而非空白）";
        }
        if (!SequenceEqual(DirectiveList(source), DirectiveList(formatted)))
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

    /// <summary>token 序列——Kind + ValueText（trivia 无关）。</summary>
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

    /// <summary>directive 序列——Kind + 去空白文本。</summary>
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

    /// <summary>序列逐项比较。</summary>
    private static bool SequenceEqual(List<string> left, List<string> right)
    {
        if (left.Count != right.Count)
        {
            return false;
        }
        for (int i = 0; i < left.Count; i++)
        {
            if (!string.Equals(left[i], right[i], StringComparison.Ordinal))
            {
                return false;
            }
        }
        return true;
    }

    /// <summary>目标行尾探测——多数行尾优先（平局取首个）；无换行回落平台默认。</summary>
    private static string DetectTargetNewline(string text, out int crlfCount, out int lfCount)
    {
        crlfCount = 0;
        lfCount = 0;
        string first = "";
        for (int i = 0; i < text.Length; i++)
        {
            if (text[i] != '\n')
            {
                continue;
            }
            bool crlf = i > 0 && text[i - 1] == '\r';
            if (crlf)
            {
                crlfCount++;
            }
            else
            {
                lfCount++;
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

    /// <summary>项目行尾探测——自入口向上查找 .editorconfig / .gitattributes 的 eol 声明（最多 12 层）。</summary>
    private static string DetectProjectNewline(string entry, out string source)
    {
        source = "";
        string dir = File.Exists(entry) ? (Path.GetDirectoryName(entry) ?? "") : entry;
        for (int depth = 0; depth < 12; depth++)
        {
            if (dir.Length == 0)
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

    /// <summary>.editorconfig 的 end_of_line 读取——取最后一条适用于 .cs 的声明。</summary>
    private static string ReadEditorConfigEol(string path)
    {
        string[] lines = File.ReadAllLines(path);
        bool applies = false;
        string result = "";
        for (int i = 0; i < lines.Length; i++)
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

    /// <summary>段是否适用于 .cs——`*` 或 `.cs` 结尾的模式。</summary>
    private static bool SectionAppliesToCs(string section)
    {
        string[] tokens = section.Split(',');
        for (int i = 0; i < tokens.Length; i++)
        {
            string token = tokens[i].Trim().Trim('{', '}', '[', ']').Trim();
            if (token == "*" || token.EndsWith(".cs", StringComparison.Ordinal))
            {
                return true;
            }
        }
        return false;
    }

    /// <summary>.gitattributes 的 eol 读取——取最后一条适用于 .cs 的 eol 属性。</summary>
    private static string ReadGitAttributesEol(string path)
    {
        string[] lines = File.ReadAllLines(path);
        string result = "";
        for (int i = 0; i < lines.Length; i++)
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
            for (int j = 1; j < parts.Length; j++)
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

    /// <summary>行尾归一——CRLF / CR / LF 统一为目标换行。</summary>
    private static string NormalizeNewLineText(string text, string newline)
    {
        string working = text.Replace("\r\n", "\n").Replace('\r', '\n');
        if (newline != "\n")
        {
            working = working.Replace("\n", newline);
        }
        return working;
    }

    /// <summary>行尾文案（CRLF / LF）。</summary>
    private static string NewlineLabel(string newline)
    {
        return newline == "\r\n" ? "CRLF" : "LF";
    }

    /// <summary>UTF-8 BOM 探测——保真写回。</summary>
    private static bool HasUtf8Bom(string path)
    {
        using FileStream stream = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite);
        int b0 = stream.ReadByte();
        int b1 = stream.ReadByte();
        int b2 = stream.ReadByte();
        return b0 == 0xEF && b1 == 0xBB && b2 == 0xBF;
    }

    /// <summary>保真原子落盘——临时文件 + Move 覆盖；编码 / BOM 按原文；写后回读自检行尾。</summary>
    private static string WriteFilePreserving(string path, string text, bool bom, string newlineOverride)
    {
        string newline = newlineOverride;
        if (newline.Length == 0)
        {
            newline = Environment.NewLine;
            if (File.Exists(path))
            {
                int crlfCount;
                int lfCount;
                newline = DetectTargetNewline(File.ReadAllText(path), out crlfCount, out lfCount);
            }
        }
        string payload = NormalizeNewLineText(text, newline);
        string temporary = path + "." + Guid.NewGuid().ToString("N") + ".tmp";
        try
        {
            File.WriteAllText(temporary, payload, new UTF8Encoding(bom));
            File.Move(temporary, path, true);
        }
        finally
        {
            if (File.Exists(temporary))
            {
                File.Delete(temporary);
            }
        }
        string mismatch = CheckNewlineUniform(File.ReadAllText(path), newline);
        if (mismatch.Length > 0)
        {
            return "写盘后行尾自检不一致（期望 " + NewlineLabel(newline) + "）: " + mismatch;
        }
        return "";
    }

    /// <summary>行尾一致性检查——目标 CRLF 时不得残留 LF，反之亦然。</summary>
    private static string CheckNewlineUniform(string text, string newline)
    {
        int crlfCount = 0;
        int lfCount = 0;
        for (int i = 0; i < text.Length; i++)
        {
            if (text[i] != '\n')
            {
                continue;
            }
            if (i > 0 && text[i - 1] == '\r')
            {
                crlfCount++;
            }
            else
            {
                lfCount++;
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

    /// <summary>差异行数——按行对齐比较（行内空白差异计入）。</summary>
    private static int CountChangedLines(string a, string b)
    {
        string[] left = NormalizeNewLineText(a, "\n").Split('\n');
        string[] right = NormalizeNewLineText(b, "\n").Split('\n');
        int count = 0;
        int total = Math.Max(left.Length, right.Length);
        for (int i = 0; i < total; i++)
        {
            string x = i < left.Length ? left[i] : "";
            string y = i < right.Length ? right[i] : "";
            if (!string.Equals(x, y, StringComparison.Ordinal))
            {
                count++;
            }
        }
        return count;
    }

    /// <summary>结构化返回头（A214）——ok / tool / target / items（负值省略）+ 专有字段（插入序）。</summary>
    private static string MetaHead(string tool, bool ok, string target, int items, (string Key, object Value)[] extra)
    {
        Dictionary<string, object> head = new Dictionary<string, object>
        {
            ["ok"] = ok,
            ["tool"] = tool,
            ["target"] = target
        };
        if (items >= 0)
        {
            head["items"] = items;
        }
        for (int i = 0; i < extra.Length; i++)
        {
            head[extra[i].Key] = extra[i].Value;
        }
        return JsonSerializer.Serialize(head);
    }

    /// <summary>摘要行（A210）——来源 | 工作量。</summary>
    private static string Echo(string target, string workload)
    {
        return target + " | " + workload;
    }

    /// <summary>结果截断——超长回执限幅（cs-format 输出通常远小于上限）。</summary>
    private static string TrimResult(string text, int max)
    {
        if (text.Length <= max)
        {
            return text;
        }
        return text.Substring(0, max) + Environment.NewLine + "（回执已截断，共 " + text.Length + " 字符）";
    }
}
