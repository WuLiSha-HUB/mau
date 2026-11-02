using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using System.Text.Json;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;
using Microsoft.CodeAnalysis.Text;

namespace Mau.Development
{
#pragma warning disable CS8600 // 环境 SDK 对 ConcurrentDictionary/Dictionary TryXXX(out TValue) 的 MaybeNullWhen 分析异常误报——out 实参非空变量也报警（最小复现验证）
    /// <summary>
    /// MauRoslynBridge 查询与补丁面分部——cs.find_ref（全引用）+ cs.patch（方法体级替换，partial 分部）。
    /// 写操作统一：磁盘为准——恒落盘，编译诊断随回执返回（不阻断）；盘面校验 + 树差异落盘，写后内存 = 磁盘。
    /// </summary>
    public sealed partial class MauRoslynBridge
    {
        /// <summary>
        /// cs.find_ref——全引用（含重载全匹配——CH2 find_ref 仅首重载痛点修复）
        /// </summary>
        /// <param name="args">参数</param>
        /// <param name="result">结果</param>
        /// <returns>调用完成</returns>
        private bool ToolFindRef(JsonElement args, out string result)
        {
            string path = Arg(args, "path");
            string className = Arg(args, "class");
            string member = Arg(args, "member");
            if (member.Length == 0)
            {
                result = "ERR|BAD_ARGS|缺参数 member（必填：path class member）";
                return false;
            }
            string resolveError;
            List<string> projects = ResolveProjects(path, out resolveError);
            if (projects.Count == 0)
            {
                result = resolveError;
                return false;
            }
            // 定义所在项目定位——多项目入口（.sln / 目录）下逐项目查找类声明，命中即为 ownerCache
            List<ProjectCache> caches = new List<ProjectCache>();
            ProjectCache ownerCache = null;
            SyntaxTree foundTree = null;
            ClassDeclarationSyntax classNode = null;
            for (int i = 0; i < projects.Count; i = i + 1)
            {
                ProjectCache cache = EnsureProject(projects[i]);
                FullScan(cache);
                caches.Add(cache);
                SyntaxTree tree;
                ClassDeclarationSyntax node;
                int total;
                if (ownerCache == null && FindClassNode(cache, className, out tree, out node, out total))
                {
                    ownerCache = cache;
                    foundTree = tree;
                    classNode = node;
                }
            }
            if (ownerCache == null)
            {
                result = "ERR|CLASS_NOT_FOUND|类不存在: " + className;
                return false;
            }
            SemanticModel classModel = EnsureSemantics(ownerCache, foundTree!.FilePath);
            INamedTypeSymbol? classSymbol = classModel.GetDeclaredSymbol(classNode!) as INamedTypeSymbol;
            if (classSymbol == null)
            {
                result = "ERR|SYMBOL_BIND|类符号解析失败: " + className;
                return false;
            }
            System.Collections.Immutable.ImmutableArray<ISymbol> targets = classSymbol.GetMembers(member);
            if (targets.Length == 0)
            {
                result = "ERR|SYMBOL_NOT_FOUND|成员不存在: " + className + "." + member;
                return false;
            }
            // 跨程序集匹配面——兄弟项目里该成员解析为元数据符号（对方 dll），与源码符号不可用符号相等比较，
            // 按引用键比对（判例 2026-09-16：入口壳对域成员的调用被漏报）
            HashSet<string> targetKeys = new HashSet<string>(StringComparer.Ordinal);
            for (int i = 0; i < targets.Length; i = i + 1)
            {
                targetKeys.Add(RefKey(targets[i]));
            }
            List<string> hits = new List<string>();
            Dictionary<string, string[]> lineCache = new Dictionary<string, string[]>(StringComparer.OrdinalIgnoreCase);
            for (int c = 0; c < caches.Count; c = c + 1)
            {
                ProjectCache cache = caches[c];
                bool sameProject = cache == ownerCache;
                foreach (KeyValuePair<string, SyntaxTree> pair in cache.Trees)
                {
                    SemanticModel model = EnsureSemantics(cache, pair.Key);
                    SyntaxNode root = pair.Value.GetRoot();
                    foreach (SyntaxNode node in root.DescendantNodes())
                    {
                        string name = "";
                        SyntaxNode? namedNode = null;
                        IdentifierNameSyntax? identifier = node as IdentifierNameSyntax;
                        if (identifier != null)
                        {
                            name = identifier.Identifier.Text;
                            namedNode = identifier;
                        }
                        else
                        {
                            GenericNameSyntax? generic = node as GenericNameSyntax;
                            if (generic != null)
                            {
                                name = generic.Identifier.Text;
                                namedNode = generic;
                            }
                        }
                        if (namedNode == null || name != member)
                        {
                            continue;
                        }
                        SymbolInfo info = model.GetSymbolInfo(namedNode);
                        bool matched = MatchesAny(info.Symbol, targets);
                        if (!matched)
                        {
                            for (int i = 0; i < info.CandidateSymbols.Length; i = i + 1)
                            {
                                if (MatchesAny(info.CandidateSymbols[i], targets))
                                {
                                    matched = true;
                                    break;
                                }
                            }
                        }
                        if (!matched && info.Symbol != null)
                        {
                            matched = targetKeys.Contains(RefKey(info.Symbol));
                        }
                        if (!matched)
                        {
                            for (int i = 0; i < info.CandidateSymbols.Length; i = i + 1)
                            {
                                if (targetKeys.Contains(RefKey(info.CandidateSymbols[i])))
                                {
                                    matched = true;
                                    break;
                                }
                            }
                        }
                        if (matched)
                        {
                            FileLinePositionSpan span = namedNode.GetLocation().GetLineSpan();
                            string lineText = ExtractLineText(pair.Key, span.StartLinePosition.Line, lineCache);
                            string mark = sameProject ? "" : "[跨程序集] ";
                            hits.Add(mark + RelativeToProject(cache, pair.Key) + ":" + (span.StartLinePosition.Line + 1) + ":" + (span.StartLinePosition.Character + 1) + ": " + lineText);
                        }
                    }
                }
            }
            // 结构化返回（2026-09-18）：首行 JSON 元数据头 + 正文命中行（`[跨程序集] 文件:行:列: 代码`）
            Dictionary<string, object> refMeta = new Dictionary<string, object>();
            refMeta["projects"] = projects.Count;
            StringBuilder sb = new StringBuilder();
            string refTarget = className + "." + member;
            sb.Append(MetaHead("cs-find_ref", true, refTarget, hits.Count, refMeta));
            sb.Append(Environment.NewLine + Echo(refTarget, hits.Count + " 引用"));
            for (int i = 0; i < hits.Count; i = i + 1)
            {
                sb.Append(Environment.NewLine + hits[i]);
            }
            result = TrimResult(sb.ToString(), MaxResultChars);
            return true;
        }

        /// <summary>
        /// 符号是否命中目标集（SymbolEqualityComparer）
        /// </summary>
        /// <param name="symbol">候选符号（可空）</param>
        /// <param name="targets">目标集</param>
        /// <returns>是否命中</returns>
        private static bool MatchesAny(ISymbol? symbol, System.Collections.Immutable.ImmutableArray<ISymbol> targets)
        {
            if (symbol == null)
            {
                return false;
            }
            for (int i = 0; i < targets.Length; i = i + 1)
            {
                if (SymbolEqualityComparer.Default.Equals(symbol, targets[i]))
                {
                    return true;
                }
            }
            return false;
        }

        /// <summary>
        /// 读取文件指定行文本（引用点上下文展示）
        /// </summary>
        /// <param name="filePath">文件</param>
        /// <param name="lineZero">0-based 行号</param>
        /// <param name="cache">本次调用内行缓存（零共享状态——并发安全）</param>
        /// <returns>行文本（trim 80）</returns>
        private static string ExtractLineText(string filePath, int lineZero, Dictionary<string, string[]> cache)
        {
            try
            {
                // 行缓存——同文件多次取行复用（R2-P3 原意）；改调用内局部字典——原静态单槽跨会话并发串味（A66 修复 2026-09-20）
                string[] lines = null!;
                if (!cache.TryGetValue(filePath, out lines))
                {
                    lines = File.ReadAllLines(filePath);
                    cache[filePath] = lines;
                }
                if (lineZero < 0 || lineZero >= lines.Length)
                {
                    return "";
                }
                string text = lines[lineZero].Trim();
                if (text.Length > 80)
                {
                    text = text.Substring(0, 80) + "…";
                }
                return text;
            }
            catch (Exception)
            {
                return "";
            }
        }

        /// <summary>
        /// cs.patch——方法体级替换（锚点=类+方法名；弃行号）。两态：OK 已落盘（编译诊断随回执）/ ERR 未落盘
        /// </summary>
        /// <param name="args">参数</param>
        /// <param name="result">结果</param>
        /// <returns>调用完成</returns>
        private bool ToolPatch(JsonElement args, out string result)
        {
            string path = Arg(args, "path");
            string className = Arg(args, "class");
            string methodName = Arg(args, "method");
            string body = Arg(args, "body");
            if (className.Length == 0 || methodName.Length == 0 || body.Length == 0)
            {
                result = "ERR|BAD_ARGS|缺参数 class/method/body（必填：path class method body）";
                return false;
            }
            string csproj;
            string entryError;
            if (!ResolveSingleProject(path, className, out csproj, out entryError))
            {
                result = entryError;
                return false;
            }
            ProjectCache cache = EnsureProject(csproj);
            FullScan(cache);
            lock (cache.Gate)
            {
                List<ClassPart> parts = FindClassParts(cache, className);
                if (parts.Count == 0)
                {
                    result = "ERR|CLASS_NOT_FOUND|类不存在: " + className;
                    return true;
                }
                ClassPart methodPart;
                BaseMethodDeclarationSyntax methodNode;
                List<string> signatures;
                int methodFind = FindPatchTargetInParts(parts, methodName, out methodPart, out methodNode, out signatures);
                if (methodFind == 1)
                {
                    result = "ERR|METHOD_NOT_FOUND|方法不存在: " + className + "." + methodName + PartialHint(parts.Count) + UnsupportedMemberHint(parts, methodName);
                    return true;
                }
                if (methodFind == 2)
                {
                    StringBuilder sb = new StringBuilder();
                    sb.Append("ERR|METHOD_AMBIGUOUS|方法重载歧义——请用 cs-find_ref 区分签名（cs-patch 锚点 = 类 + 方法名，重载需先唯一化）:");
                    for (int i = 0; i < signatures.Count; i = i + 1)
                    {
                        sb.Append(Environment.NewLine + "  " + methodName + signatures[i]);
                    }
                    result = sb.ToString();
                    return true;
                }
                // 写落点 = 方法所在分部（跨分部聚合：foundTree 必须与命中节点同树）
                SyntaxTree foundTree = methodNode.SyntaxTree;
                if (methodNode.Body == null)
                {
                    result = "ERR|ARROW_BODY|表达式体成员（方法 / 构造函数）请先展开为块体（语法糖展开铁律）: " + className + "." + methodName;
                    return true;
                }
                // 写侧规整——行尾归一 + 规范化布局 + 基准缩进平移（规范格式是写侧责任，不依赖事后格式重整器）
                string patchNewline = DetectNewLine(foundTree);
                string patchIndent = IndentAt(foundTree, methodNode);
                StatementSyntax parsed;
                try
                {
                    parsed = SyntaxFactory.ParseStatement(NormalizeNewLineText(body, patchNewline));
                }
                catch (Exception)
                {
                    // 解析失败——走下方 fallback（整方法声明解析），最终失败以 ERR 回执可见
                    parsed = null!;
                }
                BlockSyntax? block = parsed as BlockSyntax;
                if (block == null)
                {
                    // 容错——允许直接给完整方法声明（自动取方法体），省一轮「BAD_BODY 后重发」的往返
                    MemberDeclarationSyntax? fallback = SyntaxFactory.ParseMemberDeclaration(NormalizeNewLineText(body, patchNewline));
                    BaseMethodDeclarationSyntax? fallbackMethod = fallback as BaseMethodDeclarationSyntax;
                    if (fallbackMethod != null)
                    {
                        block = fallbackMethod.Body;
                    }
                }
                if (block == null)
                {
                    result = "ERR|BAD_BODY|body 必须是块（{ ... }）或完整方法声明（含方法体）";
                    return true;
                }
                BlockSyntax? finalBlock = SyntaxFactory.ParseStatement(FormatNodeText(block, patchIndent, patchNewline)) as BlockSyntax;
                if (finalBlock == null)
                {
                    result = "ERR|BAD_BODY|body 规整后无法重解析（结构异常）";
                    return true;
                }
                // 尾部补换行——后续 token 的缩进前导 trivia 依赖前一 token 以换行结尾（与 member insert 同规）
                // 行号锚——新节点由 WithBody 生成、未挂载语法树，其 Span 从自身起点起算（行号恒为 1）
                SyntaxAnnotation patchMark = new SyntaxAnnotation();
                BaseMethodDeclarationSyntax patchedDecl;
                ConstructorDeclarationSyntax? ctorDecl = methodNode as ConstructorDeclarationSyntax;
                if (ctorDecl != null)
                {
                    patchedDecl = ctorDecl.WithBody(finalBlock);
                }
                else
                {
                    patchedDecl = ((MethodDeclarationSyntax)methodNode).WithBody(finalBlock);
                }
                BaseMethodDeclarationSyntax newMethod = (BaseMethodDeclarationSyntax)EnsureMemberTrailingNewLine(patchedDecl, patchNewline).WithAdditionalAnnotations(patchMark);
                SyntaxNode root = foundTree.GetRoot();
                SyntaxNode newRoot = root.ReplaceNode(methodNode, newMethod);
                SyntaxTree newTree = CreateTreeFromRoot(foundTree, newRoot);
                CSharpCompilation newCompilation = (CSharpCompilation)cache.Compilation.ReplaceSyntaxTree(foundTree, newTree);
                string commitError;
                string writeNote;
                List<string> newErrors;
                if (!CommitTreeChange(cache, foundTree, newTree, newCompilation, out commitError, out writeNote, out newErrors))
                {
                    result = commitError;
                    return true;
                }
                string filePath = foundTree.FilePath;
                // 结构化返回（2026-09-18）：JSON 元数据头 + 正文方法段源码（H28 校验链；行尾行号给 LLM）
                // 行号基准 = 完整文件树上的 attached 节点——替换生成的新节点未挂载，位置从自身起点起算
                SyntaxNode patchedNode;
                if (!TryResolveAnnotated(newTree, patchMark, out patchedNode))
                {
                    result = "ERR|LINE_BASE|替换后节点定位失败——修改已落盘但返回行号不可信: " + className + "." + methodName;
                    return true;
                }
                int methodStartLine = newTree.GetText().Lines.GetLineFromPosition(patchedNode.FullSpan.Start).LineNumber + 1;
                int methodEndLine = patchedNode.GetLocation().GetLineSpan().EndLinePosition.Line + 1;
                // 统一口径（A210）：头（ok/tool/target/items + file/start/end）+ 摘要行 + 改后方法段源码
                string patchTarget = className + "." + methodName;
                int patchLines = methodEndLine - methodStartLine + 1;
                Dictionary<string, object> ptMeta = new Dictionary<string, object>();
                ptMeta["file"] = RelativeToProject(cache, filePath);
                ptMeta["start"] = methodStartLine;
                ptMeta["end"] = methodEndLine;
                if (writeNote.Length > 0)
                {
                    ptMeta["writeNote"] = writeNote;
                }
                NoteCompileErrors(ptMeta, newErrors);
                result = TrimResult(MetaHead("cs-patch", true, patchTarget, patchLines, ptMeta) + Environment.NewLine + Echo(patchTarget, patchLines + " 行") + CompileDiagBlock(newErrors) + Environment.NewLine + NumberedSource(patchedNode.ToFullString(), methodStartLine), MaxResultChars);
                return true;
            }
        }
        /// <summary>
        /// 取回替换后节点的 attached 实例——WithXxx / 片段节点派生的新节点未挂载语法树，
        /// 其 Span 从自身起点起算（行号归零或落到片段相对行）；替换前打 SyntaxAnnotation，
        /// 替换后在完整树上取回同节点，位置才是文件坐标。
        /// </summary>
        /// <param name="tree">替换后的完整语法树</param>
        /// <param name="mark">替换前打上的注解</param>
        /// <param name="node">取回的节点（失败为 null!）</param>
        /// <returns>取回成功</returns>
        private static bool TryResolveAnnotated(SyntaxTree tree, SyntaxAnnotation mark, out SyntaxNode node)
        {
            foreach (SyntaxNode candidate in tree.GetRoot().GetAnnotatedNodes(mark))
            {
                node = candidate;
                return true;
            }
            node = null!;
            return false;
        }

        /// <summary>
        /// 引用键——类型全名.成员名(参数类型序列)。跨编译匹配用：跨项目引用在本项目语义模型里解析为
        /// 元数据符号（对方 dll），与源码符号不共享 SymbolEqualityComparer，只能按字符串键比对。
        /// </summary>
        /// <param name="symbol">符号（源码或元数据）</param>
        /// <returns>引用键（跨编译稳定）</returns>
        private static string RefKey(ISymbol symbol)
        {
            ISymbol def = symbol.OriginalDefinition;
            INamedTypeSymbol? type = def.ContainingType;
            string typeName = type != null ? type.ToDisplayString(SymbolDisplayFormat.FullyQualifiedFormat) : "?";
            IMethodSymbol? method = def as IMethodSymbol;
            if (method != null)
            {
                StringBuilder sb = new StringBuilder();
                sb.Append(typeName);
                sb.Append(".");
                sb.Append(def.Name);
                sb.Append("(");
                for (int i = 0; i < method.Parameters.Length; i = i + 1)
                {
                    if (i > 0)
                    {
                        sb.Append(",");
                    }
                    sb.Append(method.Parameters[i].Type.ToDisplayString(SymbolDisplayFormat.FullyQualifiedFormat));
                }
                sb.Append(")");
                return sb.ToString();
            }
            return typeName + "." + def.Name;
        }
        /// <summary>
        /// 收集引用键集——遍历给定项目集的全部语法树，取每个标识符解析出的符号键（含候选符号）。
        /// 用于跨程序集复核：单项目语义只见本项目源码符号，兄弟项目对它的引用解析为元数据符号，
        /// 只能按引用键匹配（判例 2026-09-16：跨项目被引用成员被误报为死代码）。
        /// </summary>
        /// <param name="caches">项目缓存集（跨程序集复核范围）</param>
        /// <returns>引用键集合（RefKey 口径）</returns>
        private HashSet<string> CollectReferenceKeys(List<ProjectCache> caches)
        {
            HashSet<string> keys = new HashSet<string>(StringComparer.Ordinal);
            for (int c = 0; c < caches.Count; c = c + 1)
            {
                ProjectCache cache = caches[c];
                foreach (KeyValuePair<string, SyntaxTree> pair in cache.Trees)
                {
                    SemanticModel model = EnsureSemantics(cache, pair.Key);
                    SyntaxNode root = pair.Value.GetRoot();
                    foreach (SyntaxNode node in root.DescendantNodes())
                    {
                        SyntaxNode? namedNode = null;
                        IdentifierNameSyntax? identifier = node as IdentifierNameSyntax;
                        if (identifier != null)
                        {
                            namedNode = identifier;
                        }
                        else
                        {
                            GenericNameSyntax? generic = node as GenericNameSyntax;
                            if (generic != null)
                            {
                                namedNode = generic;
                            }
                        }
                        if (namedNode == null)
                        {
                            continue;
                        }
                        SymbolInfo info = model.GetSymbolInfo(namedNode);
                        if (info.Symbol != null)
                        {
                            keys.Add(RefKey(info.Symbol));
                        }
                        for (int i = 0; i < info.CandidateSymbols.Length; i = i + 1)
                        {
                            keys.Add(RefKey(info.CandidateSymbols[i]));
                        }
                    }
                }
            }
            return keys;
        }
        /// <summary>
        /// 单缓存零引用扫描——DeadSingle 主体（多项目聚合时携跨程序集引用键集，见 CollectReferenceKeys）。
        /// </summary>
        /// <param name="cache">项目缓存</param>
        /// <param name="crossKeys">跨程序集引用键集（null=仅项目内判定）</param>
        /// <param name="deadCount">输出——零引用成员数</param>
        /// <returns>报告文本</returns>
        private string DeadInCache(ProjectCache cache, HashSet<string>? crossKeys, out int deadCount)
        {
            HashSet<ISymbol> targets = new HashSet<ISymbol>(SymbolEqualityComparer.Default);
            foreach (KeyValuePair<string, SyntaxTree> pair in cache.Trees)
            {
                SemanticModel model = EnsureSemantics(cache, pair.Key);
                SyntaxNode root = pair.Value.GetRoot();
                foreach (SyntaxNode node in root.DescendantNodes())
                {
                    TypeDeclarationSyntax? typeDecl = node as TypeDeclarationSyntax;
                    if (typeDecl == null)
                    {
                        continue;
                    }
                    INamedTypeSymbol? typeSymbol = model.GetDeclaredSymbol(typeDecl) as INamedTypeSymbol;
                    if (typeSymbol == null)
                    {
                        continue;
                    }
                    System.Collections.Immutable.ImmutableArray<ISymbol> members = typeSymbol.GetMembers();
                    for (int i = 0; i < members.Length; i = i + 1)
                    {
                        ISymbol member = members[i];
                        if (member.IsImplicitlyDeclared)
                        {
                            continue;
                        }
                        Accessibility access = member.DeclaredAccessibility;
                        if (access != Accessibility.Private && access != Accessibility.Internal)
                        {
                            continue;
                        }
                        IMethodSymbol? method = member as IMethodSymbol;
                        if (method != null)
                        {
                            if (method.MethodKind != MethodKind.Ordinary)
                            {
                                continue;
                            }
                            if (method.IsOverride)
                            {
                                continue;
                            }
                        }
                        IPropertySymbol? property = member as IPropertySymbol;
                        if (property != null && property.IsOverride)
                        {
                            continue;
                        }
                        targets.Add(member);
                    }
                }
            }
            HashSet<ISymbol> referenced = new HashSet<ISymbol>(SymbolEqualityComparer.Default);
            foreach (KeyValuePair<string, SyntaxTree> pair in cache.Trees)
            {
                SemanticModel model = EnsureSemantics(cache, pair.Key);
                SyntaxNode root = pair.Value.GetRoot();
                foreach (SyntaxNode node in root.DescendantNodes())
                {
                    SyntaxNode? namedNode = null;
                    IdentifierNameSyntax? identifier = node as IdentifierNameSyntax;
                    if (identifier != null)
                    {
                        namedNode = identifier;
                    }
                    else
                    {
                        GenericNameSyntax? generic = node as GenericNameSyntax;
                        if (generic != null)
                        {
                            namedNode = generic;
                        }
                    }
                    if (namedNode == null)
                    {
                        continue;
                    }
                    SymbolInfo info = model.GetSymbolInfo(namedNode);
                    if (info.Symbol != null)
                    {
                        // OriginalDefinition 归一——泛型方法调用 GetSymbolInfo 返回构造符号，与声明原定义比较须归一到原定义（R2-P2-01 泛型漏匹配）
                        referenced.Add(info.Symbol.OriginalDefinition);
                    }
                    for (int i = 0; i < info.CandidateSymbols.Length; i = i + 1)
                    {
                        referenced.Add(info.CandidateSymbols[i].OriginalDefinition);
                    }
                }
            }
            List<string> deadLines = new List<string>();
            foreach (ISymbol target in targets)
            {
                if (referenced.Contains(target))
                {
                    continue;
                }
                // 跨程序集复核——兄弟项目引用在本项目语义里是元数据符号，靠引用键判定（否则误报死代码）
                if (crossKeys != null && crossKeys.Contains(RefKey(target)))
                {
                    continue;
                }
                string typeName = target.ContainingType != null ? target.ContainingType.Name : "?";
                string lineText = "";
                if (target.DeclaringSyntaxReferences.Length > 0)
                {
                    FileLinePositionSpan span = target.DeclaringSyntaxReferences[0].GetSyntax().GetLocation().GetLineSpan();
                    lineText = span.Path + ":" + (span.StartLinePosition.Line + 1);
                }
                deadLines.Add(typeName + "." + target.Name + " // " + lineText);
            }
            deadLines.Sort(StringComparer.Ordinal);
            deadCount = deadLines.Count;
            // 统一口径（A210）：头（ok/tool/target/items + dead）+ 摘要行 + 零引用清单（`类型.成员 // 文件:行`）
            Dictionary<string, object> deadMeta = new Dictionary<string, object>();
            deadMeta["dead"] = deadLines.Count;
            StringBuilder sb = new StringBuilder();
            sb.Append(MetaHead("cs-dead", deadLines.Count == 0, cache.AssemblyName, targets.Count, deadMeta));
            sb.Append(Environment.NewLine + Echo(cache.AssemblyName, targets.Count + " 成员 · 死码 " + deadLines.Count));
            for (int i = 0; i < deadLines.Count; i = i + 1)
            {
                sb.Append(Environment.NewLine + deadLines[i]);
            }
            return sb.ToString();
        }
        /// <summary>
        /// 编译诊断收集——变更前后诊断键比对，取新增 error（项目既有错误不误伤）。
        /// 语义：结果只作回执负载，不阻断落盘（写侧以磁盘为准，错误可见即可）。
        /// </summary>
        /// <param name="before">改动前编译</param>
        /// <param name="after">改动后编译</param>
        /// <returns>新增错误诊断文本行（无错为空表）</returns>
        private static List<string> CollectNewErrors(CSharpCompilation before, CSharpCompilation after)
        {
            List<string> newErrors = new List<string>();
            HashSet<string> beforeErrors = new HashSet<string>(StringComparer.Ordinal);
            ImmutableArrayHelper.CollectErrorKeys(before, beforeErrors);
            foreach (Diagnostic diagnostic in after.GetDiagnostics())
            {
                if (diagnostic.Severity != DiagnosticSeverity.Error)
                {
                    continue;
                }
                string key = diagnostic.Id + "|" + diagnostic.Location.GetLineSpan().Path + "|" + diagnostic.GetMessage();
                if (!beforeErrors.Contains(key))
                {
                    newErrors.Add(diagnostic.ToString());
                }
            }
            return newErrors;
        }
        /// <summary>
        /// 单文件盘面刷新——外部改动（text-* / 编辑器）后重解析该文件并替换编译中的旧树。
        /// 磁盘是唯一真相：调用后内存树与该文件盘面一致。
        /// </summary>
        /// <param name="cache">项目缓存</param>
        /// <param name="filePath">文件绝对路径</param>
        /// <returns>刷新成功（文件不存在为 false）</returns>
        private bool RefreshFile(ProjectCache cache, string filePath)
        {
            if (!File.Exists(filePath))
            {
                return false;
            }
            SyntaxTree fresh = ParseFile(filePath);
            SyntaxTree existing;
            if (cache.Trees.TryGetValue(filePath, out existing))
            {
                cache.Compilation = (CSharpCompilation)cache.Compilation.ReplaceSyntaxTree(existing, fresh);
            }
            else
            {
                cache.Compilation = (CSharpCompilation)cache.Compilation.AddSyntaxTrees(fresh);
            }
            cache.Trees[filePath] = fresh;
            cache.Stamps[filePath] = SnapshotOf(filePath);
            SemanticModel removed = null!;
            cache.Semantics.TryRemove(filePath, out removed);
            return true;
        }
        /// <summary>
        /// 变更区间应用——把树差异按 span 落到盘面原文上，未变更区原样保留（不再整文件由内存树重生成）。
        /// </summary>
        /// <param name="text">盘面原文（与变更前树文本一致）</param>
        /// <param name="changes">树差异（坐标基于变更前树）</param>
        /// <returns>应用后文本；区间重叠或越界（不可安全应用）为 null</returns>
        private static string? ApplyChanges(string text, IEnumerable<TextChange> changes)
        {
            List<TextChange> ordered = new List<TextChange>(changes);
            ordered.Sort(CompareTextChange);
            StringBuilder sb = new StringBuilder(text.Length + 256);
            int cursor = 0;
            for (int i = 0; i < ordered.Count; i = i + 1)
            {
                TextChange change = ordered[i];
                if (change.Span.Start < cursor || change.Span.End > text.Length)
                {
                    return null;
                }
                sb.Append(text, cursor, change.Span.Start - cursor);
                sb.Append(change.NewText);
                cursor = change.Span.End;
            }
            sb.Append(text, cursor, text.Length - cursor);
            return sb.ToString();
        }
        /// <summary>
        /// 树差异排序——按起点升序（应用顺序确定）
        /// </summary>
        /// <param name="a">差异甲</param>
        /// <param name="b">差异乙</param>
        /// <returns>比较结果</returns>
        private static int CompareTextChange(TextChange a, TextChange b)
        {
            return a.Span.Start.CompareTo(b.Span.Start);
        }
        /// <summary>
        /// 统一落盘出口——盘面一致性校验 + 区间替换写盘 + 树/快照同步（不动编译态，多文件写入由调用方统一提交）。
        /// 盘面已变（内存树陈旧）→ 自动刷新该文件树并拒绝本次写入（失败可见，不静默覆写别处改动）。
        /// </summary>
        /// <param name="cache">项目缓存</param>
        /// <param name="oldTree">变更前树</param>
        /// <param name="newTree">变更后树</param>
        /// <param name="error">失败码（空=成功）</param>
        /// <param name="writeNote">写盘诊断（空=正常）</param>
        /// <returns>落盘成功</returns>
        private bool CommitTreeWrite(ProjectCache cache, SyntaxTree oldTree, SyntaxTree newTree, out string error, out string writeNote)
        {
            error = "";
            writeNote = "";
            string filePath = oldTree.FilePath;
            string diskText;
            try
            {
                diskText = ReadTextShared(filePath);
            }
            catch (Exception ex)
            {
                error = "ERR|WRITE_FAIL|盘面读取失败: " + ex.Message;
                return false;
            }
            string memoryText = oldTree.GetText().ToString();
            if (!string.Equals(diskText, memoryText, StringComparison.Ordinal))
            {
                RefreshFile(cache, filePath);
                error = "ERR|STALE_TREE|盘面已被外部修改（内存树已自动刷新）——请重新调用";
                return false;
            }
            string? updated = ApplyChanges(diskText, newTree.GetChanges(oldTree));
            if (updated == null)
            {
                RefreshFile(cache, filePath);
                error = "ERR|STALE_TREE|变更区间与盘面错位（内存树已自动刷新）——请重新调用";
                return false;
            }
            writeNote = WriteAtomicText(filePath, updated);
            cache.Trees[filePath] = newTree;
            cache.Stamps[filePath] = SnapshotOf(filePath);
            SemanticModel removed = null!;
            cache.Semantics.TryRemove(filePath, out removed);
            return true;
        }
        /// <summary>
        /// 单文件变更提交——诊断收集 + 落盘 + 编译态更新（写侧统一出口）。
        /// 编译错误不阻断落盘：错误随回执返回，由调用方 cs-check / cs-build 收口。
        /// </summary>
        /// <param name="cache">项目缓存</param>
        /// <param name="oldTree">变更前树</param>
        /// <param name="newTree">变更后树</param>
        /// <param name="trial">变更后编译</param>
        /// <param name="error">失败码（空=成功）</param>
        /// <param name="writeNote">写盘诊断（空=正常）</param>
        /// <param name="newErrors">新增编译错误（已落盘）</param>
        /// <returns>落盘成功</returns>
        private bool CommitTreeChange(ProjectCache cache, SyntaxTree oldTree, SyntaxTree newTree, CSharpCompilation trial, out string error, out string writeNote, out List<string> newErrors)
        {
            newErrors = CollectNewErrors(cache.Compilation, trial);
            if (!CommitTreeWrite(cache, oldTree, newTree, out error, out writeNote))
            {
                return false;
            }
            cache.Compilation = trial;
            return true;
        }
        /// <summary>
        /// 回执元数据——编译诊断计数入头（仅新增错误非空时）
        /// </summary>
        /// <param name="meta">元数据字典</param>
        /// <param name="newErrors">新增编译错误</param>
        private static void NoteCompileErrors(Dictionary<string, object> meta, List<string> newErrors)
        {
            if (newErrors.Count > 0)
            {
                meta["compileErrors"] = newErrors.Count;
            }
        }
        /// <summary>
        /// 回执正文——编译诊断段（落盘后可见，不阻断）
        /// </summary>
        /// <param name="newErrors">新增编译错误</param>
        /// <returns>诊断段（无错为空串）</returns>
        private static string CompileDiagBlock(List<string> newErrors)
        {
            if (newErrors.Count == 0)
            {
                return "";
            }
            StringBuilder sb = new StringBuilder();
            sb.Append(Environment.NewLine);
            sb.Append("编译诊断（新增 " + newErrors.Count + " 条 · 已落盘）:");
            for (int i = 0; i < newErrors.Count; i = i + 1)
            {
                sb.Append(Environment.NewLine + "  " + newErrors[i]);
            }
            return sb.ToString();
        }
    }
}
