using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.IO;
using System.Reflection;
using System.Text;
using System.Xml.Linq;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;
using Microsoft.CodeAnalysis.Formatting;
using Microsoft.CodeAnalysis.Options;
using Microsoft.CodeAnalysis.Text;
using Mau.Runtime;

namespace Mau.Development
{
#pragma warning disable CS8600 // 环境 SDK 对 ConcurrentDictionary/Dictionary TryXXX(out TValue) 的 MaybeNullWhen 分析异常误报——out 实参非空变量也报警（最小复现验证）
    /// <summary>
    /// MauRoslynBridge 核心——项目加载 / 快照监管 / 全扫对账 / 引用集 / 编译构建（partial 分部）。
    /// </summary>
    public sealed partial class MauRoslynBridge
    {
        /// <summary>
        /// 获取项目缓存——命中 touch LRU；未命中串行加载（Parse 全树 10 秒级一次）
        /// </summary>
        /// <param name="csproj">csproj 绝对路径</param>
        /// <returns>项目缓存</returns>
        private ProjectCache EnsureProject(string csproj)
        {
            ProjectCache cache = null!;
            if (_pool.TryGetValue(csproj, out cache))
            {
                cache.LastAccess = Environment.TickCount64;
                return cache;
            }
            lock (_poolGate)
            {
                if (_pool.TryGetValue(csproj, out cache))
                {
                    cache.LastAccess = Environment.TickCount64;
                    return cache;
                }
                ProjectCache created = LoadProject(csproj);
                _pool[csproj] = created;
                EvictIfNeeded(csproj);
                return created;
            }
        }

        /// <summary>
        /// 加载项目——解析 csproj + 收集源文件 + Parse 全树 + 构建引用集 + 编译
        /// </summary>
        /// <param name="csproj">csproj 绝对路径</param>
        /// <returns>项目缓存（已就绪）</returns>
        private ProjectCache LoadProject(string csproj)
        {
            ProjectCache cache = new ProjectCache();
            cache.CsprojPath = csproj;
            cache.ProjectDir = Path.GetDirectoryName(csproj) ?? "";
            ParseCsproj(cache);
            cache.SourceFiles = CollectSources(cache);
            for (int i = 0; i < cache.SourceFiles.Length; i = i + 1)
            {
                string filePath = cache.SourceFiles[i];
                SyntaxTree tree = ParseFile(filePath);
                cache.Trees[filePath] = tree;
                cache.Stamps[filePath] = SnapshotOf(filePath);
            }
            cache.References = BuildReferences(cache);
            cache.Compilation = BuildCompilation(cache);
            return cache;
        }

        /// <summary>
        /// 解析 csproj——AssemblyName / Tfm / Nullable / OutputType（XML 直接读取，缺省默认）
        /// </summary>
        /// <param name="cache">缓存条目（写属性）</param>
        private void ParseCsproj(ProjectCache cache)
        {
            cache.AssemblyName = Path.GetFileNameWithoutExtension(cache.CsprojPath);
            cache.Tfm = "net10.0";
            cache.NullableEnable = false;
            cache.IsExe = false;
            cache.DefaultExcludes = new List<string>();
            try
            {
                XDocument doc = XDocument.Load(cache.CsprojPath);
                foreach (XElement property in doc.Descendants("PropertyGroup"))
                {
                    XElement? assemblyName = property.Element("AssemblyName");
                    if (assemblyName != null && !string.IsNullOrWhiteSpace(assemblyName.Value))
                    {
                        cache.AssemblyName = assemblyName.Value.Trim();
                    }
                    XElement? tfm = property.Element("TargetFramework");
                    if (tfm != null && !string.IsNullOrWhiteSpace(tfm.Value))
                    {
                        cache.Tfm = tfm.Value.Trim();
                    }
                    XElement? nullable = property.Element("Nullable");
                    if (nullable != null)
                    {
                        string value = nullable.Value.Trim();
                        if (value == "enable" || value == "annotations")
                        {
                            cache.NullableEnable = true;
                        }
                    }
                    XElement? outputType = property.Element("OutputType");
                    if (outputType != null)
                    {
                        string value = outputType.Value.Trim();
                        if (value == "Exe" || value == "WinExe")
                        {
                            cache.IsExe = true;
                        }
                    }
                    // DefaultItemExcludes——SDK 默认编译排除（子项目目录）；分号分割条目
                    XElement? excludes = property.Element("DefaultItemExcludes");
                    if (excludes != null && !string.IsNullOrWhiteSpace(excludes.Value))
                    {
                        string[] parts = excludes.Value.Split(';');
                        for (int e = 0; e < parts.Length; e = e + 1)
                        {
                            string item = parts[e].Trim();
                            if (item.Length > 0 && !cache.DefaultExcludes.Contains(item))
                            {
                                cache.DefaultExcludes.Add(item);
                            }
                        }
                    }
                }
            }
            catch (Exception ex)
            {
                // 解析失败保留默认——语义诊断可能噪音，cs.build 权威兜底
                LogStore.Add("Mau", 2, "csproj 解析失败，保留默认: " + ex.Message, "SYS");
            }
        }

        /// <summary>
        /// 收集源文件——全部 .cs（排除 obj/bin 目录 + csproj DefaultItemExcludes 子项目目录）
        /// </summary>
        /// <param name="cache">缓存条目（ProjectDir + DefaultExcludes）</param>
        /// <returns>绝对路径数组</returns>
        internal string[] CollectSources(ProjectCache cache)
        {
            List<string> result = new List<string>();
            string[] all = Directory.GetFiles(cache.ProjectDir, "*.cs", SearchOption.AllDirectories);
            // DefaultExcludes 预编译——目录前缀排除（X\** → X\ 前缀；$( 展开引用跳过）
            List<string> dirPrefixes = new List<string>();
            for (int i = 0; i < cache.DefaultExcludes.Count; i = i + 1)
            {
                string item = cache.DefaultExcludes[i];
                if (item.StartsWith("$(", StringComparison.Ordinal))
                {
                    continue;
                }
                string norm = item.Replace('/', '\\');
                if (norm.EndsWith("\\**", StringComparison.Ordinal))
                {
                    dirPrefixes.Add(norm.Substring(0, norm.Length - 2));
                }
            }
            // 本 cache 自己的 obj 产出目录——仅此处放行程序集属性文件（子项目 obj 一律排除：目录聚合模式重复特性 = CS0579）
            string ownObjPrefix = Path.Combine(cache.ProjectDir, "obj") + Path.DirectorySeparatorChar;
            List<string> assemblyInfo = new List<string>();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                string path = all[i];
                if (path.StartsWith(ownObjPrefix, StringComparison.OrdinalIgnoreCase))
                {
                    // obj 生成产物——仅放行程序集属性文件（InternalsVisibleTo/AssemblyAttributes：真实编译含；
                    // 缺失则 internal 类型的 public 字段被判定"从未赋值" → CS0649 误报）
                    if (Path.GetFileName(path).EndsWith(".AssemblyInfo.cs", StringComparison.OrdinalIgnoreCase))
                    {
                        assemblyInfo.Add(path);
                    }
                    continue;
                }
                if (path.IndexOf("\\obj\\", StringComparison.OrdinalIgnoreCase) >= 0 ||
                    path.IndexOf("/obj/", StringComparison.OrdinalIgnoreCase) >= 0)
                {
                    continue;
                }
                if (path.IndexOf("\\bin\\", StringComparison.OrdinalIgnoreCase) >= 0 ||
                    path.IndexOf("/bin/", StringComparison.OrdinalIgnoreCase) >= 0)
                {
                    continue;
                }
                string rel = path.Substring(cache.ProjectDir.Length).TrimStart('\\', '/');
                bool excluded = false;
                for (int d = 0; d < dirPrefixes.Count; d = d + 1)
                {
                    if (rel.StartsWith(dirPrefixes[d], StringComparison.OrdinalIgnoreCase))
                    {
                        excluded = true;
                        break;
                    }
                }
                if (excluded)
                {
                    continue;
                }
                result.Add(path);
            }
            // 程序集属性文件去重——同名只收路径序首套（多 Configuration / publish 产物并存时两套同入编译单元 = CS0579 特性重复）
            assemblyInfo.Sort(StringComparer.OrdinalIgnoreCase);
            HashSet<string> seenAssemblyInfo = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
            for (int i = 0; i < assemblyInfo.Count; i = i + 1)
            {
                if (seenAssemblyInfo.Add(Path.GetFileName(assemblyInfo[i])))
                {
                    result.Add(assemblyInfo[i]);
                }
            }
            result.Sort(StringComparer.OrdinalIgnoreCase);
            return result.ToArray();
        }
        /// <summary>
        /// 项目源文件集——csproj 声明的编译单元（DefaultItemExcludes 子项目目录排除 + obj/bin 排除）。
        /// 🔴 单一实现——cs-format 的 csproj 入口（按项目边界规整）与跨项目类定位（LocateClassInProject）同源调用；
        /// includeGenerated=true 时含 obj 下程序集属性文件（扫描面编译语义），false 时只留手写源（format 面）。
        /// </summary>
        /// <param name="csprojPath">csproj 绝对路径</param>
        /// <param name="includeGenerated">true=含 obj 生成物（程序集属性文件）</param>
        /// <returns>源文件绝对路径数组（路径序）</returns>
        internal string[] CollectProjectSources(string csprojPath, bool includeGenerated)
        {
            ProjectCache probe = new ProjectCache();
            probe.CsprojPath = csprojPath;
            probe.ProjectDir = Path.GetDirectoryName(csprojPath) ?? "";
            ParseCsproj(probe);
            string[] all = CollectSources(probe);
            if (includeGenerated)
            {
                return all;
            }
            List<string> handwritten = new List<string>();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                if (all[i].IndexOf("\\obj\\", StringComparison.OrdinalIgnoreCase) >= 0 ||
                    all[i].IndexOf("/obj/", StringComparison.OrdinalIgnoreCase) >= 0)
                {
                    continue;
                }
                handwritten.Add(all[i]);
            }
            return handwritten.ToArray();
        }

        /// <summary>
        /// 从旧树创建带新根的树——CSharpSyntaxTree.Create（WithRoot 在工具编译器 Roslyn 集不可用的替代）
        /// </summary>
        /// <param name="source">源树（路径/选项继承）</param>
        /// <param name="newRoot">新根（CompilationUnit）</param>
        /// <returns>新树</returns>
        private static SyntaxTree CreateTreeFromRoot(SyntaxTree source, SyntaxNode newRoot)
        {
            CompilationUnitSyntax? unit = newRoot as CompilationUnitSyntax;
            if (unit == null)
            {
                throw new InvalidOperationException("newRoot 不是 CompilationUnitSyntax——结构异常");
            }
            return CSharpSyntaxTree.Create(unit, ParseOptions(), source.FilePath, null);
        }

        /// <summary>
        /// 换行风格探测——跟随目标文件（\r\n 或 \n）。写侧统一使用：混行会污染后续 diff，注释与声明同行还会语法崩坏（CS1022）
        /// </summary>
        /// <param name="tree">目标语法树</param>
        /// <returns>换行文本</returns>
        private static string DetectNewLine(SyntaxTree tree)
        {
            string text = tree.GetText().ToString();
            int index = text.IndexOf('\n');
            if (index > 0 && text[index - 1] != '\r')
            {
                return "\n";
            }
            return "\r\n";
        }

        /// <summary>
        /// 行尾归一——CRLF / CR / LF 统一为目标换行（写侧规整第一步）
        /// </summary>
        /// <param name="text">原文</param>
        /// <param name="newline">目标换行</param>
        /// <returns>归一后文本</returns>
        private static string NormalizeNewLineText(string text, string newline)
        {
            string working = text.Replace("\r\n", "\n").Replace('\r', '\n');
            if (newline != "\n")
            {
                working = working.Replace("\n", newline);
            }
            return working;
        }

        /// <summary>
        /// 行首缩进提取——节点起始位置所在行的前导空白（空串=顶格）
        /// </summary>
        /// <param name="tree">所在树</param>
        /// <param name="node">节点</param>
        /// <returns>行首空白文本</returns>
        private static string IndentAt(SyntaxTree tree, SyntaxNode node)
        {
            string text = tree.GetText().ToString();
            int position = node.SpanStart;
            int lineStart = text.LastIndexOf('\n', position > 0 ? position - 1 : 0) + 1;
            if (position == 0)
            {
                lineStart = 0;
            }
            int index = lineStart;
            while (index < text.Length && (text[index] == ' ' || text[index] == '\t'))
            {
                index = index + 1;
            }
            if (index <= lineStart || index > text.Length)
            {
                return "";
            }
            return text.Substring(lineStart, index - lineStart);
        }

        /// <summary>
        /// 注入代码文本规整——行尾归一（跟随目标文件）+ 行首缩进对齐（相对缩进不变，空行不带缩进）。
        /// 动机：工具参数的行尾与首行缩进由调用方决定 ⇒ 不规整即落盘混行 / 顶格；规范格式是写侧责任，不能依赖事后格式重整器。
        /// </summary>
        /// <param name="code">调用方代码文本</param>
        /// <param name="baseIndent">目标落点基准缩进（空=只做行尾归一）</param>
        /// <param name="newline">目标文件换行</param>
        /// <returns>规整后代码文本</returns>
        private static string NormalizeCodeText(string code, string baseIndent, string newline)
        {
            string normalized = NormalizeNewLineText(code, newline);
            if (baseIndent.Length == 0)
            {
                return normalized;
            }
            string[] rawLines = normalized.Split('\n');
            string[] lines = new string[rawLines.Length];
            int minIndent = -1;
            for (int i = 0; i < rawLines.Length; i = i + 1)
            {
                string line = rawLines[i];
                if (line.Length > 0 && line[line.Length - 1] == '\r')
                {
                    line = line.Substring(0, line.Length - 1);
                }
                lines[i] = line;
                if (line.Trim().Length == 0)
                {
                    continue;
                }
                int indent = CountLeadingSpace(line);
                if (minIndent < 0 || indent < minIndent)
                {
                    minIndent = indent;
                }
            }
            if (minIndent < 0)
            {
                return normalized;
            }
            StringBuilder sb = new StringBuilder();
            for (int i = 0; i < lines.Length; i = i + 1)
            {
                if (i > 0)
                {
                    sb.Append(newline);
                }
                string line = lines[i];
                if (line.Trim().Length == 0)
                {
                    continue;
                }
                int indent = CountLeadingSpace(line);
                sb.Append(baseIndent);
                for (int k = minIndent; k < indent; k = k + 1)
                {
                    sb.Append(' ');
                }
                sb.Append(line.Substring(indent));
            }
            return sb.ToString();
        }

        /// <summary>
        /// 节点文本规整——Roslyn Formatter（只调空白与缩进，token 形态原样）+ 基准缩进平移。
        /// 动机：调用方给的缩进不可信（顶格 / 错位即格式违规）；相对布局归 Formatter（与 cs.format 同源），基准缩进归落点上下文。
        /// 边界：不使用 NormalizeWhitespace——它重建全部 trivia，会把 token 形态一并重写（后置 ! 抑制符被拆开、XML doc 属性被写成带空格形态）。
        /// </summary>
        /// <param name="node">已解析节点（成员 / 语句块）</param>
        /// <param name="baseIndent">落点基准缩进</param>
        /// <param name="newline">目标换行</param>
        /// <returns>规整后文本</returns>
        private static string FormatNodeText(SyntaxNode node, string baseIndent, string newline)
        {
            SyntaxNode canonical = FormatNodeCanonical(node, newline);
            return NormalizeCodeText(canonical.ToFullString(), baseIndent, newline);
        }

        /// <summary>
        /// 节点级 Formatter——只调空白与缩进，token 形态原样（与 cs.format 同源配置）。
        /// </summary>
        /// <param name="node">目标节点</param>
        /// <param name="newline">目标换行</param>
        /// <returns>规整后节点</returns>
        private static SyntaxNode FormatNodeCanonical(SyntaxNode node, string newline)
        {
            using (AdhocWorkspace workspace = new AdhocWorkspace())
            {
                OptionSet options = FormatOptions(workspace, newline);
                return Formatter.Format(node, workspace, options);
            }
        }

        /// <summary>
        /// 格式选项——4 空格缩进 / 显式换行（写侧节点规整与 cs.format 文档规整共用同一份配置）。
        /// </summary>
        /// <param name="workspace">工作区</param>
        /// <param name="newline">目标换行</param>
        /// <returns>选项集</returns>
        private static OptionSet FormatOptions(Workspace workspace, string newline)
        {
            return workspace.Options
                .WithChangedOption(FormattingOptions.UseTabs, LanguageNames.CSharp, false)
                .WithChangedOption(FormattingOptions.TabSize, LanguageNames.CSharp, 4)
                .WithChangedOption(FormattingOptions.IndentationSize, LanguageNames.CSharp, 4)
                .WithChangedOption(FormattingOptions.NewLine, LanguageNames.CSharp, newline);
        }

        /// <summary>
        /// 行首空白计数（空格 / 制表符同计一位——相对缩进只作差）
        /// </summary>
        /// <param name="line">单行文本</param>
        /// <returns>前导空白字符数</returns>
        private static int CountLeadingSpace(string line)
        {
            int index = 0;
            while (index < line.Length && (line[index] == ' ' || line[index] == '\t'))
            {
                index = index + 1;
            }
            return index;
        }

        /// <summary>
        /// 单文件 ParseText——UTF-8 读取 + 路径伴随
        /// </summary>
        /// <param name="filePath">文件绝对路径</param>
        /// <returns>语法树</returns>
        private static SyntaxTree ParseFile(string filePath)
        {
            string text = File.ReadAllText(filePath);
            return CSharpSyntaxTree.ParseText(text, ParseOptions(), filePath);
        }

        /// <summary>
        /// 解析选项——固定 C# 12（语法分析档；与编译期 LangVersion 解耦）
        /// </summary>
        /// <returns>解析选项</returns>
        private static CSharpParseOptions ParseOptions()
        {
            return new CSharpParseOptions(LanguageVersion.CSharp12);
        }

        /// <summary>
        /// 文件快照采集
        /// </summary>
        /// <param name="filePath">文件路径</param>
        /// <returns>签名</returns>
        private static ProjectSnapshot SnapshotOf(string filePath)
        {
            ProjectSnapshot snapshot;
            snapshot.Utc = File.GetLastWriteTimeUtc(filePath);
            snapshot.Length = new FileInfo(filePath).Length;
            return snapshot;
        }

        /// <summary>
        /// 项目级全扫——重扫源文件集合（增删检测）+ 全文件对账（元数据快链 + 内容比较，批量刷新）
        /// 语义操作（check/find_ref/dead/rename）前调用；磁盘是唯一真相——内容比较兜底，外部改动本轮自动重解析。
        /// </summary>
        /// <param name="cache">缓存</param>
        private void FullScan(ProjectCache cache)
        {
            lock (cache.Gate)
            {
                if (cache.Compilation == null || cache.References.Count == 0)
                {
                    // 轻量缓存（cs.build 直建未 Parse/未建引用集）——补真实引用集 + 编译（bin 产物已存在）
                    cache.References = BuildReferences(cache);
                    cache.Compilation = BuildCompilation(cache);
                }
                string[] currentSources = CollectSources(cache);
                HashSet<string> currentSet = new HashSet<string>(currentSources, StringComparer.OrdinalIgnoreCase);
                List<string> removedFiles = new List<string>();
                for (int i = 0; i < cache.SourceFiles.Length; i = i + 1)
                {
                    if (!currentSet.Contains(cache.SourceFiles[i]))
                    {
                        removedFiles.Add(cache.SourceFiles[i]);
                    }
                }
                for (int i = 0; i < removedFiles.Count; i = i + 1)
                {
                    SyntaxTree? removedTree;
                    cache.Trees.TryRemove(removedFiles[i], out removedTree);
                    ProjectSnapshot removedStamp;
                    cache.Stamps.TryRemove(removedFiles[i], out removedStamp);
                    SemanticModel removedSem = null!;
                    cache.Semantics.TryRemove(removedFiles[i], out removedSem);
                }
                List<SyntaxTree> addTrees = new List<SyntaxTree>();
                List<SyntaxTree> replaceOld = new List<SyntaxTree>();
                List<SyntaxTree> replaceNew = new List<SyntaxTree>();
                for (int i = 0; i < currentSources.Length; i = i + 1)
                {
                    string filePath = currentSources[i];
                    SyntaxTree existing;
                    if (!cache.Trees.TryGetValue(filePath, out existing))
                    {
                        // 新增文件
                        SyntaxTree fresh = ParseFile(filePath);
                        cache.Trees[filePath] = fresh;
                        cache.Stamps[filePath] = SnapshotOf(filePath);
                        addTrees.Add(fresh);
                    }
                    else
                    {
                        ProjectSnapshot currentSnapshot = SnapshotOf(filePath);
                        bool changed = true;
                        ProjectSnapshot known;
                        if (cache.Stamps.TryGetValue(filePath, out known))
                        {
                            // 快照对账——mtime+size 只是快线索（同秒同大小的外部改写会漏判）；元数据全等时再做内容比较，
                            // 磁盘是唯一真相：外部 text-* / 编辑器的改动必须在本次扫描内被看见并自动重解析。
                            bool stampsValid = known.Utc > DateTime.MinValue && currentSnapshot.Utc > DateTime.MinValue;
                            if (stampsValid && known.Utc == currentSnapshot.Utc && known.Length == currentSnapshot.Length)
                            {
                                changed = !string.Equals(ReadTextShared(filePath), existing.GetText().ToString(), StringComparison.Ordinal);
                            }
                        }
                        if (changed)
                        {
                            SyntaxTree fresh = ParseFile(filePath);
                            cache.Trees[filePath] = fresh;
                            cache.Stamps[filePath] = currentSnapshot;
                            SemanticModel removedSem = null!;
                            cache.Semantics.TryRemove(filePath, out removedSem);
                            replaceOld.Add(existing);
                            replaceNew.Add(fresh);
                        }
                    }
                }
                cache.SourceFiles = currentSources;
                bool anyChange = addTrees.Count > 0 || replaceOld.Count > 0 || removedFiles.Count > 0;
                if (anyChange)
                {
                    CSharpCompilation updated = cache.Compilation;
                    if (addTrees.Count > 0)
                    {
                        updated = (CSharpCompilation)updated.AddSyntaxTrees(addTrees);
                    }
                    for (int i = 0; i < replaceOld.Count; i = i + 1)
                    {
                        updated = (CSharpCompilation)updated.ReplaceSyntaxTree(replaceOld[i], replaceNew[i]);
                    }
                    cache.Compilation = updated;
                    cache.Semantics.Clear();
                }
            }
        }

        /// <summary>
        /// 构建引用集——bin 产物（莎拍板 A）+ TPA + 共享框架探测（AspNetCore 全项目 / WindowsDesktop 仅 -windows 目标——A46）；按名去重（bin 优先）
        /// </summary>
        /// <param name="cache">缓存</param>
        /// <returns>引用列表（bin 缺失时返回空列表——check 引导 build）</returns>
        internal List<MetadataReference> BuildReferences(ProjectCache cache)
        {
            List<MetadataReference> references = new List<MetadataReference>();
            Dictionary<string, string> pathsByName = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
            // TPA——运行时平台程序集（宿主进程运行时；目标项目非 -windows 时剔除 WindowsDesktop 框架程序集——宿主是 -windows 应用，其 TPA 内 Accessibility.dll 会与 Roslyn 的 Accessibility 枚举抢名 → CS0118/CS0234 假阳性，A46）
            string? tpaRaw = AppContext.GetData("TRUSTED_PLATFORM_ASSEMBLIES") as string;
            bool targetWindows = cache.Tfm.IndexOf("-windows", StringComparison.OrdinalIgnoreCase) >= 0;
            if (tpaRaw != null)
            {
                string[] parts = tpaRaw.Split(';');
                for (int i = 0; i < parts.Length; i = i + 1)
                {
                    if (parts[i].Length == 0)
                    {
                        continue;
                    }
                    if (!targetWindows && parts[i].IndexOf("Microsoft.WindowsDesktop.App", StringComparison.OrdinalIgnoreCase) >= 0)
                    {
                        continue;
                    }
                    string name = Path.GetFileNameWithoutExtension(parts[i]);
                    if (!pathsByName.ContainsKey(name))
                    {
                        pathsByName[name] = parts[i];
                    }
                }
            }
            // 共享框架探测——AspNetCore（全项目）+ WindowsDesktop（仅 -windows 目标——WinForms/WPF 程序集所在；按项目 TFM 主版本匹配 + 跳过 native dll）
            string? runtimeDir = Path.GetDirectoryName(typeof(object).Assembly.Location);
            if (runtimeDir != null)
            {
                string? netCoreAppDir = Path.GetDirectoryName(runtimeDir);
                if (netCoreAppDir != null)
                {
                    string sharedDir = Path.GetDirectoryName(netCoreAppDir) ?? "";
                    ProbeSharedFramework(pathsByName, sharedDir, "Microsoft.AspNetCore.App", cache.Tfm);
                    // A46 TFM 判定——WindowsDesktop 框架仅对 -windows 目标注入（非 -windows 项目注入会引入 Accessibility.dll，与 Roslyn 的 Accessibility 枚举抢名 → CS0118/CS0234 假阳性）
                    if (targetWindows)
                    {
                        ProbeSharedFramework(pathsByName, sharedDir, "Microsoft.WindowsDesktop.App", cache.Tfm);
                    }
                }
            }
            // bin 产物——目标项目已 build 输出（优先于 TPA/框架）
            string? outputDir = FindOutputDir(cache);
            if (outputDir != null)
            {
                string[] dlls = Directory.GetFiles(outputDir, "*.dll", SearchOption.TopDirectoryOnly);
                Array.Sort(dlls, StringComparer.OrdinalIgnoreCase);
                for (int i = 0; i < dlls.Length; i = i + 1)
                {
                    string name = Path.GetFileNameWithoutExtension(dlls[i]);
                    if (string.Equals(name, cache.AssemblyName, StringComparison.OrdinalIgnoreCase))
                    {
                        continue; // 自身 bin 产物不引用（统一剔除见下）
                    }
                    pathsByName[name] = dlls[i]; // bin 优先覆盖
                }
            }
            // 编译集内项目程序集剔除——TPA/共享框架段可能已注入宿主进程加载的同名程序集（部署区副本）；
            // 与源码树并存即报 CS0436（源类型 vs 导入类型冲突）。目录聚合模式下编译集含多个子项目，一律剔除。
            HashSet<string> localAssemblies = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
            if (cache.AssemblyName.Length > 0)
            {
                localAssemblies.Add(cache.AssemblyName);
            }
            string ownPrefix = cache.ProjectDir.TrimEnd('\\', '/') + Path.DirectorySeparatorChar;
            for (int i = 0; i < cache.SourceFiles.Length; i = i + 1)
            {
                string source = cache.SourceFiles[i];
                if (!source.StartsWith(ownPrefix, StringComparison.OrdinalIgnoreCase))
                {
                    continue;
                }
                string rel = source.Substring(ownPrefix.Length);
                int sep = rel.IndexOf(Path.DirectorySeparatorChar);
                if (sep <= 0)
                {
                    continue;
                }
                string sub = rel.Substring(0, sep);
                string csproj = Path.Combine(cache.ProjectDir, sub, sub + ".csproj");
                if (File.Exists(csproj))
                {
                    localAssemblies.Add(sub);
                }
            }
            foreach (string localName in localAssemblies)
            {
                pathsByName.Remove(localName);
            }
            foreach (KeyValuePair<string, string> pair in pathsByName)
            {
                try
                {
                    references.Add(MetadataReference.CreateFromFile(pair.Value));
                }
                catch (Exception ex)
                {
                    // 单个引用失败跳过——编译器会报缺失引用而非崩溃
                    LogStore.Add("Mau", 2, "引用程序集加载失败，跳过: " + ex.Message, "SYS");
                }
            }
            return references;
        }

        /// <summary>
        /// 共享框架探测——dotnet/shared/&lt;框架名&gt;/ 按项目 TFM 主版本匹配（net10.0-windows → 8.0.x）的托管 dll 并入引用集（按名去重）
        /// </summary>
        /// <param name="pathsByName">按名路径表（写）</param>
        /// <param name="sharedDir">shared 根目录</param>
        /// <param name="frameworkName">框架目录名（Microsoft.AspNetCore.App / Microsoft.WindowsDesktop.App）</param>
        /// <param name="tfm">项目 TFM（如 net10.0-windows）——主版本不匹配则跳过该框架</param>
        private static void ProbeSharedFramework(Dictionary<string, string> pathsByName, string sharedDir, string frameworkName, string tfm)
        {
            string frameworkDir = Path.Combine(sharedDir, frameworkName);
            if (!Directory.Exists(frameworkDir))
            {
                return;
            }
            // TFM 主版本提取——net10.0-windows → "8.0"；解析失败跳过（宁缺勿错——引入错误版本 = CS1705 洪水）
            int dash = tfm.IndexOf('-');
            string baseTfm = dash >= 0 ? tfm.Substring(0, dash) : tfm;
            string netPart = baseTfm.StartsWith("net", StringComparison.OrdinalIgnoreCase) ? baseTfm.Substring(3) : "";
            if (netPart.Length == 0)
            {
                return;
            }
            // 匹配框架目录前缀（8.0. 匹配 8.0.x；9.0. 匹配 9.0.x）——取匹配中最高补丁
            string[] versions = Directory.GetDirectories(frameworkDir);
            string? best = null;
            for (int i = 0; i < versions.Length; i = i + 1)
            {
                string v = Path.GetFileName(versions[i]);
                if (v.StartsWith(netPart + ".", StringComparison.OrdinalIgnoreCase))
                {
                    if (best == null || string.CompareOrdinal(v, Path.GetFileName(best)) > 0)
                    {
                        best = versions[i];
                    }
                }
            }
            if (best == null)
            {
                return;
            }
            string[] dlls = Directory.GetFiles(best, "*.dll", SearchOption.TopDirectoryOnly);
            for (int i = 0; i < dlls.Length; i = i + 1)
            {
                string name = Path.GetFileNameWithoutExtension(dlls[i]);
                if (!pathsByName.ContainsKey(name))
                {
                    // 跳过 native dll（wpfgfx_cor3 等无托管元数据——CS0009）
                    if (!IsManagedAssembly(dlls[i]))
                    {
                        continue;
                    }
                    pathsByName[name] = dlls[i];
                }
            }
        }

        /// <summary>
        /// 托管程序集判定——PE 文件含 CLI 元数据（跳过 native dll）
        /// </summary>
        /// <param name="path">dll 路径</param>
        /// <returns>true=托管程序集</returns>
        private static bool IsManagedAssembly(string path)
        {
            try
            {
                using (FileStream fs = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite))
                using (System.Reflection.PortableExecutable.PEReader pe = new System.Reflection.PortableExecutable.PEReader(fs))
                {
                    return pe.HasMetadata;
                }
            }
            catch (Exception)
            {
                return false;
            }
        }

        /// <summary>
        /// 查找目标项目 bin 输出目录——扫描 bin/ 下含 &lt;AssemblyName&gt;.dll/.exe 的目录，取最新写入
        /// </summary>
        /// <param name="cache">缓存</param>
        /// <returns>输出目录绝对路径（无产物返回 null）</returns>
        private string? FindOutputDir(ProjectCache cache)
        {
            string binRoot = Path.Combine(cache.ProjectDir, "bin");
            if (!Directory.Exists(binRoot))
            {
                return null;
            }
            string[] candidateDirs = Directory.GetDirectories(binRoot, "*", SearchOption.AllDirectories);
            string? bestDir = null;
            DateTime bestTime = DateTime.MinValue;
            for (int i = 0; i < candidateDirs.Length; i = i + 1)
            {
                string dir = candidateDirs[i];
                string dllPath = Path.Combine(dir, cache.AssemblyName + ".dll");
                if (File.Exists(dllPath))
                {
                    DateTime t = File.GetLastWriteTimeUtc(dllPath);
                    if (t > bestTime)
                    {
                        bestTime = t;
                        bestDir = dir;
                    }
                    continue;
                }
                string exePath = Path.Combine(dir, cache.AssemblyName + ".exe");
                if (File.Exists(exePath))
                {
                    DateTime t = File.GetLastWriteTimeUtc(exePath);
                    if (t > bestTime)
                    {
                        bestTime = t;
                        bestDir = dir;
                    }
                }
            }
            return bestDir;
        }

        /// <summary>
        /// 构建编译——树集合 + 引用集 + 项目选项（OutputKind / Nullable）
        /// </summary>
        /// <param name="cache">缓存</param>
        /// <returns>编译对象</returns>
        private static CSharpCompilation BuildCompilation(ProjectCache cache)
        {
            OutputKind kind = cache.IsExe ? OutputKind.ConsoleApplication : OutputKind.DynamicallyLinkedLibrary;
            CSharpCompilationOptions options = new CSharpCompilationOptions(kind);
            if (cache.NullableEnable)
            {
                options = options.WithNullableContextOptions(NullableContextOptions.Enable);
            }
            else
            {
                options = options.WithNullableContextOptions(NullableContextOptions.Disable);
            }
            return CSharpCompilation.Create(cache.AssemblyName, cache.Trees.Values, cache.References, options);
        }

        /// <summary>
        /// 语义准备——引用集脏时重建编译；确保语义模型可用（惰性生成 + 缓存）
        /// </summary>
        /// <param name="cache">缓存</param>
        /// <param name="filePath">文件路径</param>
        /// <returns>语义模型</returns>
        private SemanticModel EnsureSemantics(ProjectCache cache, string filePath)
        {
            SemanticModel? model;
            if (cache.Semantics.TryGetValue(filePath, out model))
            {
                return model;
            }
            lock (cache.Gate)
            {
                if (cache.ReferencesDirty)
                {
                    cache.References = BuildReferences(cache);
                    cache.Compilation = BuildCompilation(cache);
                    cache.Semantics.Clear();
                    cache.ReferencesDirty = false;
                }
                if (cache.Semantics.TryGetValue(filePath, out model))
                {
                    return model;
                }
                SyntaxTree tree;
                cache.Trees.TryGetValue(filePath, out tree);
                if (tree == null)
                {
                    tree = ParseFile(filePath);
                    cache.Trees[filePath] = tree;
                    cache.Stamps[filePath] = SnapshotOf(filePath);
                }
                SemanticModel created = cache.Compilation.GetSemanticModel(tree);
                cache.Semantics[filePath] = created;
                return created;
            }
        }

        /// <summary>
        /// 类分部条目——一次类声明（partial 跨分部聚合的基本单位：所在树 + 声明节点）
        /// </summary>
        private sealed class ClassPart
        {
            /// <summary>
            /// 声明所在树
            /// </summary>
            public SyntaxTree Tree = null!;

            /// <summary>
            /// 类声明节点
            /// </summary>
            public ClassDeclarationSyntax Node = null!;
        }

        /// <summary>
        /// 同名类声明的全部分部——跨全部树聚合（按 cache.Trees 顺序；供 partial 类读写落点选择）
        /// </summary>
        /// <param name="cache">缓存</param>
        /// <param name="className">类名</param>
        /// <returns>分部列表（空 = 类不存在）</returns>
        private static List<ClassPart> FindClassParts(ProjectCache cache, string className)
        {
            List<ClassPart> parts = new List<ClassPart>();
            foreach (KeyValuePair<string, SyntaxTree> pair in cache.Trees)
            {
                foreach (SyntaxNode node in pair.Value.GetRoot().DescendantNodes())
                {
                    ClassDeclarationSyntax? decl = node as ClassDeclarationSyntax;
                    if (decl != null && decl.Identifier.Text == className)
                    {
                        ClassPart part = new ClassPart();
                        part.Tree = pair.Value;
                        part.Node = decl;
                        parts.Add(part);
                    }
                }
            }
            return parts;
        }

        /// <summary>
        /// 按类名定位类声明——全部树搜索（partial 合并：返回第一个匹配 + 同名类列表）
        /// </summary>
        /// <param name="cache">缓存</param>
        /// <param name="className">类名</param>
        /// <param name="foundTree">命中树</param>
        /// <param name="foundNode">命中声明节点</param>
        /// <param name="total">全部同名声明数（partial 提示）</param>
        /// <returns>是否找到</returns>
        private static bool FindClassNode(ProjectCache cache, string className, out SyntaxTree foundTree, out ClassDeclarationSyntax foundNode, out int total)
        {
            foundTree = null!;
            foundNode = null!;
            List<ClassPart> parts = FindClassParts(cache, className);
            total = parts.Count;
            if (parts.Count > 0)
            {
                foundTree = parts[0].Tree;
                foundNode = parts[0].Node;
            }
            return foundNode != null;
        }

        /// <summary>
        /// 跨分部成员定位——在全部同名类声明中聚合查找；唯一命中返回其分部与节点，多处命中返回候选（分部&gt;1 时附所在文件）
        /// </summary>
        /// <param name="parts">类分部列表</param>
        /// <param name="memberName">成员名（支持签名后缀；.ctor/类名=构造函数）</param>
        /// <param name="part">命中分部（唯一命中时）</param>
        /// <param name="found">命中节点（唯一命中时）</param>
        /// <param name="foundKind">命中类别</param>
        /// <param name="count">全部分部同名数量合计</param>
        /// <param name="candidates">候选签名列表（歧义时填充）</param>
        /// <returns>是否唯一命中</returns>
        private static bool FindMemberInParts(List<ClassPart> parts, string memberName, out ClassPart part, out SyntaxNode found, out string foundKind, out int count, out List<string> candidates)
        {
            part = null!;
            found = null!;
            foundKind = "";
            count = 0;
            candidates = new List<string>();
            bool multiPart = parts.Count > 1;
            for (int i = 0; i < parts.Count; i = i + 1)
            {
                SyntaxNode node;
                string kind;
                int localCount;
                List<string> localCandidates;
                bool unique = FindMemberInClass(parts[i].Node, memberName, out node, out kind, out localCount, out localCandidates);
                if (localCount == 0)
                {
                    continue;
                }
                count = count + localCount;
                string suffix = multiPart ? " @" + Path.GetFileName(parts[i].Tree.FilePath) : "";
                if (unique)
                {
                    part = parts[i];
                    found = node;
                    foundKind = kind;
                    candidates.Add(MemberSignatureLabel(node) + suffix);
                    continue;
                }
                for (int k = 0; k < localCandidates.Count; k = k + 1)
                {
                    candidates.Add(localCandidates[k] + suffix);
                }
            }
            if (count != 1 || found == null)
            {
                part = null!;
                found = null!;
                foundKind = "";
                return false;
            }
            return true;
        }

        /// <summary>
        /// 成员签名标签——歧义候选列表用（与 FindMemberInClass 候选口径一致）
        /// </summary>
        /// <param name="node">成员节点</param>
        /// <returns>标签文本</returns>
        private static string MemberSignatureLabel(SyntaxNode node)
        {
            MethodDeclarationSyntax? method = node as MethodDeclarationSyntax;
            if (method != null)
            {
                return method.Identifier.Text + method.ParameterList.ToString();
            }
            ConstructorDeclarationSyntax? ctor = node as ConstructorDeclarationSyntax;
            if (ctor != null)
            {
                return ".ctor" + ctor.ParameterList.ToString();
            }
            return FindMemberLabel(node);
        }
        /// <summary>
        /// 定位 patch 目标声明——类内按名，覆盖方法（MethodDeclarationSyntax）与构造函数（ConstructorDeclarationSyntax）。
        /// 锚点口径与成员级（cs-read / cs-member / cs-comment）对齐：".ctor" / 类名 = 构造函数；重载歧义时返回全部签名供区分。
        /// </summary>
        /// <param name="classNode">类声明</param>
        /// <param name="methodName">方法名（".ctor" / 类名 = 构造函数）</param>
        /// <param name="found">命中声明</param>
        /// <param name="signatures">全部同名签名（歧义时输出）</param>
        /// <returns>0=唯一命中 1=无 2=歧义</returns>
        private static int FindPatchTargetInClass(ClassDeclarationSyntax classNode, string methodName, out BaseMethodDeclarationSyntax found, out List<string> signatures)
        {
            found = null!;
            signatures = new List<string>();
            bool ctorRequest = methodName == ".ctor" || methodName == classNode.Identifier.Text;
            List<BaseMethodDeclarationSyntax> matches = new List<BaseMethodDeclarationSyntax>();
            foreach (SyntaxNode node in classNode.DescendantNodes())
            {
                MethodDeclarationSyntax? method = node as MethodDeclarationSyntax;
                if (method != null && method.Identifier.Text == methodName)
                {
                    matches.Add(method);
                    continue;
                }
                ConstructorDeclarationSyntax? ctor = node as ConstructorDeclarationSyntax;
                if (ctor != null && ctorRequest)
                {
                    matches.Add(ctor);
                }
            }
            for (int i = 0; i < matches.Count; i = i + 1)
            {
                signatures.Add(PatchSignature(matches[i]));
            }
            if (matches.Count == 0)
            {
                return 1;
            }
            if (matches.Count > 1)
            {
                return 2;
            }
            found = matches[0];
            return 0;
        }
        /// <summary>
        /// patch 目标参数表文本——方法 / 构造函数通用（歧义候选签名回显用）
        /// </summary>
        /// <param name="node">声明节点</param>
        /// <returns>参数表文本</returns>
        private static string PatchSignature(BaseMethodDeclarationSyntax node)
        {
            MethodDeclarationSyntax? method = node as MethodDeclarationSyntax;
            if (method != null)
            {
                return method.ParameterList.ToString();
            }
            ConstructorDeclarationSyntax? ctor = node as ConstructorDeclarationSyntax;
            if (ctor != null)
            {
                return ctor.ParameterList.ToString();
            }
            return "";
        }
        /// <summary>
        /// 跨分部 patch 目标定位——在全部同名类声明中聚合查找方法与构造函数（候选签名列表附带分部文件）
        /// </summary>
        /// <param name="parts">类分部列表</param>
        /// <param name="methodName">方法名（".ctor" / 类名 = 构造函数）</param>
        /// <param name="part">命中分部（唯一命中时）</param>
        /// <param name="found">命中声明</param>
        /// <param name="signatures">全部同名签名（歧义时输出）</param>
        /// <returns>0=唯一命中 1=无 2=歧义</returns>
        private static int FindPatchTargetInParts(List<ClassPart> parts, string methodName, out ClassPart part, out BaseMethodDeclarationSyntax found, out List<string> signatures)
        {
            part = null!;
            found = null!;
            signatures = new List<string>();
            bool multiPart = parts.Count > 1;
            int hits = 0;
            for (int i = 0; i < parts.Count; i = i + 1)
            {
                BaseMethodDeclarationSyntax localFound;
                List<string> localSignatures;
                int local = FindPatchTargetInClass(parts[i].Node, methodName, out localFound, out localSignatures);
                if (local == 1)
                {
                    continue;
                }
                if (local == 0)
                {
                    hits = hits + 1;
                    part = parts[i];
                    found = localFound;
                    signatures.Add(PatchSignature(localFound) + (multiPart ? " @" + Path.GetFileName(parts[i].Tree.FilePath) : ""));
                    continue;
                }
                for (int k = 0; k < localSignatures.Count; k = k + 1)
                {
                    hits = hits + 1;
                    signatures.Add(localSignatures[k] + (multiPart ? " @" + Path.GetFileName(parts[i].Tree.FilePath) : ""));
                }
            }
            if (hits == 0)
            {
                part = null!;
                found = null!;
                return 1;
            }
            if (hits > 1)
            {
                part = null!;
                found = null!;
                return 2;
            }
            return 0;
        }
        /// <summary>
        /// 不支持成员类型的定向提示——锚点按名找不到方法 / 构造函数时，若类内确有同名其它成员，
        /// 提示「该成员类型不在 cs-patch 支持面」（区分「名字写错」与「类型不支持」——两者最小修复动作不同）。
        /// </summary>
        /// <param name="parts">类分部列表</param>
        /// <param name="methodName">锚点名</param>
        /// <returns>提示串（无同名成员时空串）</returns>
        private static string UnsupportedMemberHint(List<ClassPart> parts, string methodName)
        {
            string pureName = methodName;
            int paren = pureName.IndexOf('(');
            if (paren >= 0)
            {
                pureName = pureName.Substring(0, paren).Trim();
            }
            string accessorOwner = "";
            if (pureName.StartsWith("get_", StringComparison.Ordinal) || pureName.StartsWith("set_", StringComparison.Ordinal))
            {
                accessorOwner = pureName.Substring(4);
            }
            for (int i = 0; i < parts.Count; i = i + 1)
            {
                foreach (SyntaxNode node in parts[i].Node.DescendantNodes())
                {
                    PropertyDeclarationSyntax? property = node as PropertyDeclarationSyntax;
                    if (property != null && (property.Identifier.Text == pureName || (accessorOwner.Length > 0 && property.Identifier.Text == accessorOwner)))
                    {
                        return "；类内存在属性 " + property.Identifier.Text + "——属性访问器不在 cs-patch 支持面（改访问器体走 text-replace，增删属性走 cs-member）";
                    }
                    EventDeclarationSyntax? evt = node as EventDeclarationSyntax;
                    if (evt != null && evt.Identifier.Text == pureName)
                    {
                        return "；类内存在事件 " + evt.Identifier.Text + "——事件不在 cs-patch 支持面（改事件声明走 text-replace）";
                    }
                    IndexerDeclarationSyntax? indexer = node as IndexerDeclarationSyntax;
                    if (indexer != null && (pureName == "this" || pureName == "Item"))
                    {
                        return "；类内存在索引器——索引器不在 cs-patch 支持面（改访问器体走 text-replace）";
                    }
                    DestructorDeclarationSyntax? destructor = node as DestructorDeclarationSyntax;
                    if (destructor != null && (pureName == "~" + parts[i].Node.Identifier.Text || pureName.StartsWith("~", StringComparison.Ordinal)))
                    {
                        return "；类内存在析构函数——析构函数不在 cs-patch 支持面（改其体走 text-replace）";
                    }
                    OperatorDeclarationSyntax? op = node as OperatorDeclarationSyntax;
                    if (op != null && pureName.IndexOf("operator", StringComparison.Ordinal) >= 0)
                    {
                        return "；类内存在运算符声明——运算符不在 cs-patch 支持面（改其体走 text-replace）";
                    }
                }
            }
            return "";
        }

        /// <summary>
        /// 定位成员节点——字段/属性/方法/构造函数（按名）；memberName 支持签名后缀（如 SubmitChoice(int)）区分重载；
        /// 构造函数 = ".ctor" 或类名；歧义返回候选签名列表
        /// </summary>
        /// <param name="classNode">类声明</param>
        /// <param name="memberName">成员名（签名后缀可选；.ctor/类名=构造函数）</param>
        /// <param name="found">命中节点</param>
        /// <param name="foundKind">命中类别（方法/字段/属性/构造函数）</param>
        /// <param name="count">同名数量</param>
        /// <param name="candidates">候选签名列表（歧义时填充——AMBIGUOUS 提示用）</param>
        /// <returns>是否唯一命中</returns>
        private static bool FindMemberInClass(ClassDeclarationSyntax classNode, string memberName, out SyntaxNode found, out string foundKind, out int count, out List<string> candidates)
        {
            found = null!;
            foundKind = "";
            count = 0;
            candidates = new List<string>();
            string pureName = memberName;
            string signature = "";
            int paren = memberName.IndexOf('(');
            if (paren >= 0)
            {
                pureName = memberName.Substring(0, paren).Trim();
                signature = memberName.Substring(paren);
            }
            bool ctorRequest = pureName == ".ctor" || pureName == classNode.Identifier.Text;
            List<SyntaxNode> matches = new List<SyntaxNode>();
            foreach (SyntaxNode node in classNode.DescendantNodes())
            {
                MethodDeclarationSyntax? method = node as MethodDeclarationSyntax;
                if (method != null && method.Identifier.Text == pureName)
                {
                    if (signature.Length == 0 || SignatureMatches(method.ParameterList.ToString(), signature))
                    {
                        matches.Add(method);
                    }
                    continue;
                }
                ConstructorDeclarationSyntax? ctor = node as ConstructorDeclarationSyntax;
                if (ctor != null && ctorRequest)
                {
                    if (signature.Length == 0 || SignatureMatches(ctor.ParameterList.ToString(), signature))
                    {
                        matches.Add(ctor);
                    }
                    continue;
                }
                FieldDeclarationSyntax? field = node as FieldDeclarationSyntax;
                if (field != null)
                {
                    foreach (VariableDeclaratorSyntax variable in field.Declaration.Variables)
                    {
                        if (variable.Identifier.Text == memberName)
                        {
                            matches.Add(field);
                        }
                    }
                    continue;
                }
                PropertyDeclarationSyntax? property = node as PropertyDeclarationSyntax;
                if (property != null && property.Identifier.Text == memberName)
                {
                    matches.Add(property);
                }
            }
            count = matches.Count;
            if (matches.Count == 0)
            {
                return false;
            }
            if (matches.Count > 1)
            {
                for (int i = 0; i < matches.Count; i = i + 1)
                {
                    MethodDeclarationSyntax? overloadMethod = matches[i] as MethodDeclarationSyntax;
                    if (overloadMethod != null)
                    {
                        candidates.Add(overloadMethod.Identifier.Text + overloadMethod.ParameterList.ToString());
                        continue;
                    }
                    ConstructorDeclarationSyntax? overloadCtor = matches[i] as ConstructorDeclarationSyntax;
                    if (overloadCtor != null)
                    {
                        candidates.Add(".ctor" + overloadCtor.ParameterList.ToString());
                        continue;
                    }
                    candidates.Add(FindMemberLabel(matches[i]));
                }
                return false;
            }
            found = matches[0];
            if (found is MethodDeclarationSyntax)
            {
                foundKind = "method";
            }
            else if (found is ConstructorDeclarationSyntax)
            {
                foundKind = "constructor";
            }
            else if (found is FieldDeclarationSyntax)
            {
                foundKind = "field";
            }
            else
            {
                foundKind = "property";
            }
            return true;
        }

        /// <summary>
        /// 签名匹配——按参数类型序列比较（"(int, string)" 与 "(int choice, string name)" 等价；泛型/修饰符/数组保形）
        /// </summary>
        /// <param name="parameterListText">实际参数列表文本（ParameterList.ToString()）</param>
        /// <param name="requestedSignature">请求签名（member 后缀，如 "(int, string)"）</param>
        /// <returns>类型序列是否一致</returns>
        private static bool SignatureMatches(string parameterListText, string requestedSignature)
        {
            return ExtractParameterTypes(parameterListText) == ExtractParameterTypes(requestedSignature);
        }

        /// <summary>
        /// 提取参数类型序列——剥参数名/默认值/空白，保留类型与修饰符（ref/out/params）
        /// </summary>
        /// <param name="paramListText">参数列表文本（含括号）</param>
        /// <returns>逗号连接的类型序列（空参返回空串）</returns>
        private static string ExtractParameterTypes(string paramListText)
        {
            string inner = paramListText.Trim();
            if (inner.StartsWith("(", StringComparison.Ordinal))
            {
                inner = inner.Substring(1);
            }
            if (inner.EndsWith(")", StringComparison.Ordinal))
            {
                inner = inner.Substring(0, inner.Length - 1);
            }
            if (inner.Trim().Length == 0)
            {
                return "";
            }
            List<string> parts = SplitTopLevelArgs(inner);
            List<string> types = new List<string>();
            for (int i = 0; i < parts.Count; i = i + 1)
            {
                string p = parts[i].Trim();
                int eq = p.IndexOf('=');
                if (eq >= 0)
                {
                    p = p.Substring(0, eq).Trim();
                }
                int lastSpace = p.LastIndexOf(' ');
                if (lastSpace > 0)
                {
                    string candidate = p.Substring(lastSpace + 1).Trim();
                    if (candidate.Length > 0 && (char.IsLetter(candidate[0]) || candidate[0] == '_'))
                    {
                        p = p.Substring(0, lastSpace).Trim();
                    }
                }
                types.Add(p.Replace(" ", "").Replace("\t", ""));
            }
            return string.Join(",", types);
        }

        /// <summary>
        /// 顶层逗号分割——感知泛型尖括号/元组圆括号嵌套（Func&lt;int, string&gt; 与 (int, string) 内逗号不分隔）
        /// </summary>
        /// <param name="text">参数列表内文本（已剥括号）</param>
        /// <returns>顶层片段列表</returns>
        private static List<string> SplitTopLevelArgs(string text)
        {
            List<string> parts = new List<string>();
            int angle = 0;
            int paren = 0;
            int start = 0;
            for (int i = 0; i < text.Length; i = i + 1)
            {
                char c = text[i];
                if (c == '<')
                {
                    angle = angle + 1;
                }
                else if (c == '>')
                {
                    angle = angle - 1;
                }
                else if (c == '(')
                {
                    paren = paren + 1;
                }
                else if (c == ')')
                {
                    paren = paren - 1;
                }
                else if (c == ',' && angle == 0 && paren == 0)
                {
                    parts.Add(text.Substring(start, i - start));
                    start = i + 1;
                }
            }
            parts.Add(text.Substring(start));
            return parts;
        }

        /// <summary>
        /// 成员标签——字段/属性歧义时的候选描述（方法/构造函数走签名路径）
        /// </summary>
        /// <param name="node">成员节点</param>
        /// <returns>标签文本</returns>
        private static string FindMemberLabel(SyntaxNode node)
        {
            PropertyDeclarationSyntax? property = node as PropertyDeclarationSyntax;
            if (property != null)
            {
                return "属性 " + property.Identifier.Text;
            }
            FieldDeclarationSyntax? field = node as FieldDeclarationSyntax;
            if (field != null && field.Declaration.Variables.Count > 0)
            {
                return "字段 " + field.Declaration.Variables[0].Identifier.Text;
            }
            return node.GetType().Name;
        }

        /// <summary>
        /// 分部类提示——类有 N 处声明时说明检索已跨全部分部（成员确实不存在，不是漏查）；非分部类返回空串
        /// </summary>
        /// <param name="total">类声明处数（FindClassParts 数量）</param>
        /// <returns>提示文本</returns>
        private static string PartialHint(int total)
        {
            if (total <= 1)
            {
                return "";
            }
            return "——注意：该类是分部类（" + total + " 处声明），成员已在全部分部跨文件检索";
        }

        /// <summary>
        /// 原子落盘——临时文件 + Move 覆盖（防半截写入）；编码保真（原文件 BOM 有则保留、无则不添加）+ 行尾按原文件多数归一。
        /// 写侧共用出口（patch / member / comment 与 format 同源）——保真语义一处收口。
        /// </summary>
        /// <param name="path">目标文件</param>
        /// <param name="text">完整新内容</param>
        /// <returns>写盘诊断（空=正常；非空=行尾自检不一致描述）</returns>
        private static string WriteAtomicText(string path, string text)
        {
            bool bom = File.Exists(path) && HasUtf8Bom(path);
            return WriteFilePreserving(path, text, bom, "");
        }
    }

    /// <summary>
    /// ImmutableArray 助手——编译诊断键收集（避免 lambda 的直接使用点集中管理）
    /// </summary>
    internal static class ImmutableArrayHelper
    {
        /// <summary>
        /// 收集编译错误键集合（Id|路径|行|列|消息）
        /// </summary>
        /// <param name="compilation">编译</param>
        /// <param name="target">目标集合</param>
        public static void CollectErrorKeys(CSharpCompilation compilation, HashSet<string> target)
        {
            System.Collections.Immutable.ImmutableArray<Diagnostic> diagnostics = compilation.GetDiagnostics();
            for (int i = 0; i < diagnostics.Length; i = i + 1)
            {
                Diagnostic diagnostic = diagnostics[i];
                if (diagnostic.Severity != DiagnosticSeverity.Error)
                {
                    continue;
                }
                FileLinePositionSpan span = diagnostic.Location.GetLineSpan();
                target.Add(diagnostic.Id + "|" + span.Path + "|" + diagnostic.GetMessage());
            }
        }
    }
}