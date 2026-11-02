using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using System.Text.Json;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;

namespace Mau.Development
{
#pragma warning disable CS8600 // 环境 SDK 对 ConcurrentDictionary/Dictionary TryXXX(out TValue) 的 MaybeNullWhen 分析异常误报——out 实参非空变量也报警（最小复现验证）
    /// <summary>
    /// MauRoslynBridge 成员变更面分部——cs.member 三改法（insert/delete/rename，partial 分部）。
    /// 写操作统一：磁盘为准——恒落盘，编译诊断随回执返回（不阻断）；逐文件盘面校验 + 树差异落盘。
    /// </summary>
    public sealed partial class MauRoslynBridge
    {
        /// <summary>
        /// cs.member——增/删（树级）/重命名（语义级——全项目引用同步）
        /// </summary>
        /// <param name="args">参数</param>
        /// <param name="result">结果</param>
        /// <returns>调用完成</returns>
        private bool ToolMember(JsonElement args, out string result)
        {
            string op = Arg(args, "op");
            if (op == "insert")
            {
                return MemberInsert(args, out result);
            }
            if (op == "delete")
            {
                return MemberDelete(args, out result);
            }
            if (op == "rename")
            {
                return MemberRename(args, out result);
            }
            result = "ERR|BAD_ARGS|op 非法值: " + op + "（insert|delete|rename）";
            return false;
        }

        /// <summary>member insert——类中插入成员（position: end/before/after/after_fields；code 单成员 / codes 批量数组二选一——批量整批一次构树 + 一次落盘，A92；单成员含多个声明即 BAD_ARGS 拒绝，不静默截断）</summary>
        /// <param name="args">参数</param>
        /// <param name="result">结果</param>
        /// <returns>调用完成</returns>
        private bool MemberInsert(JsonElement args, out string result)
        {
            string path = Arg(args, "path");
            string className = Arg(args, "class");
            string code = Arg(args, "code");
            string position = Arg(args, "position");
            string anchor = Arg(args, "anchor");
            if (className.Length == 0)
            {
                result = "ERR|BAD_ARGS|缺参数 class（必填：path class op）";
                return false;
            }
            // [段1] 入参形态归一——code 单成员 / codes 批量数组二选一（A92；互斥在参数面拦，此处兜底）
            List<string> codes = new List<string>();
            bool useCodes = false;
            JsonElement codesElement;
            if (args.TryGetProperty("codes", out codesElement))
            {
                if (codesElement.ValueKind != JsonValueKind.Array)
                {
                    result = "ERR|BAD_ARGS|codes 必须是字符串数组";
                    return false;
                }
                foreach (JsonElement item in codesElement.EnumerateArray())
                {
                    if (item.ValueKind != JsonValueKind.String)
                    {
                        result = "ERR|BAD_ARGS|codes 元素必须是字符串";
                        return false;
                    }
                    string itemText = item.GetString();
                    if (itemText == null)
                    {
                        itemText = "";
                    }
                    codes.Add(itemText);
                }
                if (codes.Count > 0)
                {
                    useCodes = true;
                }
            }
            if (codes.Count == 0)
            {
                if (code.Length == 0)
                {
                    result = "ERR|BAD_ARGS|缺参数 code 或 codes（code 与 codes 二选一）";
                    return false;
                }
                codes.Add(code);
            }
            bool batch = codes.Count > 1;
            // [段2] 声明数防线——code 路径一次一成员；codes 路径每元素恰一成员（防 ParseMemberDeclaration 静默截断）
            for (int i = 0; i < codes.Count; i = i + 1)
            {
                int declared = CountMemberDeclarations(codes[i]);
                if (useCodes)
                {
                    if (declared != 1)
                    {
                        result = "ERR|BAD_ARGS|codes 第 " + (i + 1) + " 个元素含 " + declared + " 个成员声明——codes 批量路径每个元素须恰一个成员声明";
                        return true;
                    }
                }
                else if (declared > 1)
                {
                    result = "ERR|BAD_ARGS|code 含 " + declared + " 个成员声明——单成员用 code；多成员请改用 codes 批量（整批一次落盘）";
                    return true;
                }
            }
            if (position.Length == 0)
            {
                position = "end";
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
                // 落点分部——end/after_fields 落首个分部；before/after 落锚点所在分部（跨分部聚合）
                ClassPart targetPart = parts[0];
                int insertIndex = -1;
                if (position == "end")
                {
                    insertIndex = targetPart.Node.Members.Count;
                }
                else if (position == "after_fields")
                {
                    insertIndex = 0;
                    for (int i = 0; i < targetPart.Node.Members.Count; i = i + 1)
                    {
                        if (targetPart.Node.Members[i] is FieldDeclarationSyntax)
                        {
                            insertIndex = i + 1;
                        }
                    }
                }
                else if (position == "after" || position == "before")
                {
                    if (anchor.Length == 0)
                    {
                        result = "ERR|BAD_ARGS|position=after/before 需要 anchor";
                        return true;
                    }
                    ClassPart anchorPart = null!;
                    int foundIndex = -1;
                    for (int p = 0; p < parts.Count; p = p + 1)
                    {
                        for (int i = 0; i < parts[p].Node.Members.Count; i = i + 1)
                        {
                            if (MemberNameOf(parts[p].Node.Members[i]) == anchor)
                            {
                                anchorPart = parts[p];
                                foundIndex = i;
                                break;
                            }
                        }
                        if (foundIndex >= 0)
                        {
                            break;
                        }
                    }
                    if (foundIndex < 0)
                    {
                        result = "ERR|ANCHOR_NOT_FOUND|锚点成员不存在: " + anchor + PartialHint(parts.Count);
                        return true;
                    }
                    targetPart = anchorPart;
                    if (position == "after")
                    {
                        insertIndex = foundIndex + 1;
                    }
                    else
                    {
                        insertIndex = foundIndex;
                    }
                }
                else
                {
                    result = "ERR|BAD_ARGS|position 非法值: " + position + "（end|before|after|after_fields）";
                    return true;
                }
                SyntaxTree foundTree = targetPart.Tree;
                ClassDeclarationSyntax classNode = targetPart.Node;
                // [段3] 成员逐个解析 + 写侧规整（行尾归一 + 规范化布局 + 基准缩进平移）——规范格式是写侧责任
                string memberNewline = DetectNewLine(foundTree);
                string memberIndent = classNode.Members.Count > 0 ? IndentAt(foundTree, classNode.Members[0]) : IndentAt(foundTree, classNode) + "    ";
                List<MemberDeclarationSyntax> newMembers = new List<MemberDeclarationSyntax>();
                List<SyntaxAnnotation> insertMarks = new List<SyntaxAnnotation>();
                for (int i = 0; i < codes.Count; i = i + 1)
                {
                    string label;
                    if (useCodes)
                    {
                        label = "codes 第 " + (i + 1) + " 个元素";
                    }
                    else
                    {
                        label = "code";
                    }
                    MemberDeclarationSyntax? parsedMember = SyntaxFactory.ParseMemberDeclaration(NormalizeNewLineText(codes[i], memberNewline));
                    if (parsedMember == null)
                    {
                        result = "ERR|BAD_CODE|" + label + " 不是有效的成员声明";
                        return true;
                    }
                    MemberDeclarationSyntax? formattedMember = SyntaxFactory.ParseMemberDeclaration(FormatNodeText(parsedMember, memberIndent, memberNewline));
                    if (formattedMember == null)
                    {
                        result = "ERR|BAD_CODE|" + label + " 不是有效的成员声明";
                        return true;
                    }
                    SyntaxAnnotation insertMark = new SyntaxAnnotation();
                    newMembers.Add(EnsureMemberTrailingNewLine(formattedMember, memberNewline).WithAdditionalAnnotations(insertMark));
                    insertMarks.Add(insertMark);
                }
                // [段4] 整批一次构树 + 一次落盘——互相引用的成组成员同批可见（A92：单成员落盘会报缺名诊断，随回执返回）
                ClassDeclarationSyntax newClassNode = classNode.WithMembers(classNode.Members.InsertRange(insertIndex, newMembers));
                SyntaxNode root = foundTree.GetRoot();
                SyntaxNode newRoot = root.ReplaceNode(classNode, newClassNode);
                SyntaxTree newTree = CreateTreeFromRoot(foundTree, newRoot);
                CSharpCompilation trial = (CSharpCompilation)cache.Compilation.ReplaceSyntaxTree(foundTree, newTree);
                string commitError;
                string writeNote;
                List<string> newErrors;
                if (!CommitTreeChange(cache, foundTree, newTree, trial, out commitError, out writeNote, out newErrors))
                {
                    result = commitError;
                    return true;
                }
                string filePath = foundTree.FilePath;
                // [段5] 统一口径（A210）：头（ok/tool/target/items + op/file/…）+ 摘要行——行号基准 = 完整文件树上的 attached 节点
                string relFile = RelativeToProject(cache, filePath);
                if (!batch)
                {
                    SyntaxNode insertedMember;
                    if (!TryResolveAnnotated(newTree, insertMarks[0], out insertedMember))
                    {
                        result = "ERR|LINE_BASE|插入后节点定位失败——修改已落盘但返回行号不可信: " + className;
                        return true;
                    }
                    Dictionary<string, object> miMeta = new Dictionary<string, object>();
                    miMeta["op"] = "insert";
                    miMeta["file"] = relFile;
                    miMeta["start"] = newTree.GetText().Lines.GetLineFromPosition(insertedMember.FullSpan.Start).LineNumber + 1;
                    miMeta["end"] = insertedMember.GetLocation().GetLineSpan().EndLinePosition.Line + 1;
                    miMeta["kind"] = newMembers[0].GetType().Name;
                    if (writeNote.Length > 0)
                    {
                        miMeta["writeNote"] = writeNote;
                    }
                    NoteCompileErrors(miMeta, newErrors);
                    result = MetaHead("cs-member", true, className, 1, miMeta) + Environment.NewLine + Echo(className, "插入 1 成员") + CompileDiagBlock(newErrors);
                    return true;
                }
                List<Dictionary<string, object>> items = new List<Dictionary<string, object>>();
                for (int i = 0; i < insertMarks.Count; i = i + 1)
                {
                    SyntaxNode insertedItem;
                    if (!TryResolveAnnotated(newTree, insertMarks[i], out insertedItem))
                    {
                        result = "ERR|LINE_BASE|插入后节点定位失败——修改已落盘但返回行号不可信: " + className;
                        return true;
                    }
                    Dictionary<string, object> itemMeta = new Dictionary<string, object>();
                    itemMeta["start"] = newTree.GetText().Lines.GetLineFromPosition(insertedItem.FullSpan.Start).LineNumber + 1;
                    itemMeta["end"] = insertedItem.GetLocation().GetLineSpan().EndLinePosition.Line + 1;
                    itemMeta["kind"] = newMembers[i].GetType().Name;
                    items.Add(itemMeta);
                }
                Dictionary<string, object> mbMeta = new Dictionary<string, object>();
                mbMeta["op"] = "insert";
                mbMeta["file"] = relFile;
                mbMeta["entries"] = items;
                if (writeNote.Length > 0)
                {
                    mbMeta["writeNote"] = writeNote;
                }
                NoteCompileErrors(mbMeta, newErrors);
                result = MetaHead("cs-member", true, className, items.Count, mbMeta) + Environment.NewLine + Echo(className, "插入 " + items.Count + " 成员") + CompileDiagBlock(newErrors);
                return true;
            }
        }
        /// <summary>
        /// 顶层成员声明计数——member insert 多成员检测（ParseMemberDeclaration 只解析首个成员，静默截断防护）。
        /// </summary>
        /// <param name="code">成员源码文本</param>
        /// <returns>顶层成员声明数</returns>
        private static int CountMemberDeclarations(string code)
        {
            CompilationUnitSyntax unit = SyntaxFactory.ParseCompilationUnit(code);
            return unit.Members.Count;
        }

        /// <summary>
        /// 成员落盘格式规整——尾部空白 trivia 剥净后补一个换行。
        /// 动机：插入点后续 token（类闭合括号 / 下一成员声明）的缩进前导 trivia 依赖前一 token 以换行结尾；
        ///       缺失时落盘为 `}    }` 或 `}        /// &lt;summary&gt;` 同行——编译通过故门禁不报，只能写侧规整。
        /// </summary>
        /// <param name="member">解析出的成员节点</param>
        /// <param name="newline">换行文本（跟随目标文件）</param>
        /// <returns>规整后的成员节点</returns>
        private static MemberDeclarationSyntax EnsureMemberTrailingNewLine(MemberDeclarationSyntax member, string newline)
        {
            SyntaxTriviaList trailing = member.GetTrailingTrivia();
            int cut = trailing.Count;
            while (cut > 0)
            {
                SyntaxTrivia last = trailing[cut - 1];
                if (!last.IsKind(SyntaxKind.WhitespaceTrivia) && !last.IsKind(SyntaxKind.EndOfLineTrivia))
                {
                    break;
                }
                cut = cut - 1;
            }
            List<SyntaxTrivia> kept = new List<SyntaxTrivia>();
            for (int i = 0; i < cut; i = i + 1)
            {
                kept.Add(trailing[i]);
            }
            kept.Add(SyntaxFactory.EndOfLine(newline));
            return member.WithTrailingTrivia(SyntaxFactory.TriviaList(kept));
        }

        /// <summary>
        /// member delete——删除成员（含注释）；编译诊断随回执（不阻断落盘）
        /// </summary>
        /// <param name="args">参数</param>
        /// <param name="result">结果</param>
        /// <returns>调用完成</returns>
        private bool MemberDelete(JsonElement args, out string result)
        {
            string path = Arg(args, "path");
            string className = Arg(args, "class");
            string member = Arg(args, "member");
            if (className.Length == 0 || member.Length == 0)
            {
                result = "ERR|BAD_ARGS|缺参数 class/member（必填：path class op member）";
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
                ClassPart memberPart;
                SyntaxNode memberNode;
                string memberKind;
                int memberCount;
                List<string> memberCandidates;
                if (!FindMemberInParts(parts, member, out memberPart, out memberNode, out memberKind, out memberCount, out memberCandidates))
                {
                    if (memberCount > 1)
                    {
                        result = "ERR|MEMBER_AMBIGUOUS|成员歧义——同名 " + memberCount + " 处，候选签名: " + string.Join(" / ", memberCandidates) + "——member 传签名后缀区分（如 " + member + "(int)）";
                        return true;
                    }
                    result = "ERR|MEMBER_NOT_FOUND|成员不存在: " + className + "." + member + PartialHint(parts.Count);
                    return true;
                }
                // 写落点 = 成员所在分部（跨分部聚合：树必须与命中节点同树）
                SyntaxTree foundTree = memberNode.SyntaxTree;
                SyntaxNode root = foundTree.GetRoot();
                SyntaxNode newRoot = root.RemoveNode(memberNode, SyntaxRemoveOptions.KeepNoTrivia);
                if (newRoot == null)
                {
                    result = "ERR|REMOVE_FAIL|节点删除失败（结构异常）";
                    return true;
                }
                SyntaxTree newTree = CreateTreeFromRoot(foundTree, newRoot);
                CSharpCompilation trial = (CSharpCompilation)cache.Compilation.ReplaceSyntaxTree(foundTree, newTree);
                string commitError;
                string writeNote;
                List<string> newErrors;
                if (!CommitTreeChange(cache, foundTree, newTree, trial, out commitError, out writeNote, out newErrors))
                {
                    result = commitError;
                    return true;
                }
                string filePath = foundTree.FilePath;
                // 统一口径（A210）：头（ok/tool/target/items + op/member/file）+ 摘要行
                Dictionary<string, object> mdMeta = new Dictionary<string, object>();
                mdMeta["op"] = "delete";
                mdMeta["member"] = member;
                mdMeta["file"] = RelativeToProject(cache, filePath);
                if (writeNote.Length > 0)
                {
                    mdMeta["writeNote"] = writeNote;
                }
                NoteCompileErrors(mdMeta, newErrors);
                result = MetaHead("cs-member", true, className, 1, mdMeta) + Environment.NewLine + Echo(className, "删除 " + member) + CompileDiagBlock(newErrors);
                return true;
            }
        }

        /// <summary>
        /// member rename——全项目引用同步（语义验证每处替换；逐文件盘面校验 + 独立落盘——中途失败不回滚已写文件，回执给失败文件与原因）
        /// </summary>
        /// <param name="args">参数</param>
        /// <param name="result">结果</param>
        /// <returns>调用完成</returns>
        private bool MemberRename(JsonElement args, out string result)
        {
            string path = Arg(args, "path");
            string className = Arg(args, "class");
            string oldName = Arg(args, "oldName");
            string newName = Arg(args, "newName");
            if (className.Length == 0 || oldName.Length == 0 || newName.Length == 0)
            {
                result = "ERR|BAD_ARGS|缺参数 class/oldName/newName（必填：path class op oldName newName）";
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
                SyntaxTree foundTree;
                ClassDeclarationSyntax classNode;
                int classTotal;
                if (!FindClassNode(cache, className, out foundTree, out classNode, out classTotal))
                {
                    result = "ERR|CLASS_NOT_FOUND|类不存在: " + className;
                    return true;
                }
                SemanticModel classModel = EnsureSemantics(cache, foundTree.FilePath);
                INamedTypeSymbol? classSymbol = classModel.GetDeclaredSymbol(classNode) as INamedTypeSymbol;
                if (classSymbol == null)
                {
                    result = "ERR|SYMBOL_BIND|类符号解析失败: " + className;
                    return true;
                }
                System.Collections.Immutable.ImmutableArray<ISymbol> targets = classSymbol.GetMembers(oldName);
                if (targets.Length == 0)
                {
                    result = "ERR|SYMBOL_NOT_FOUND|成员不存在: " + className + "." + oldName;
                    return true;
                }
                HashSet<ISymbol> targetSet = new HashSet<ISymbol>(targets, SymbolEqualityComparer.Default);
                List<string> changedFiles = new List<string>();
                List<SyntaxTree> changedTrees = new List<SyntaxTree>();
                List<SyntaxTree> changedOldTrees = new List<SyntaxTree>();
                foreach (KeyValuePair<string, SyntaxTree> pair in cache.Trees)
                {
                    SemanticModel model = EnsureSemantics(cache, pair.Key);
                    SymbolRenamingRewriter rewriter = new SymbolRenamingRewriter(model, targetSet, oldName, newName);
                    SyntaxNode newRoot = rewriter.Visit(pair.Value.GetRoot());
                    if (rewriter.Changed && newRoot != null)
                    {
                        changedFiles.Add(pair.Key);
                        changedOldTrees.Add(pair.Value);
                        changedTrees.Add(CreateTreeFromRoot(pair.Value, newRoot));
                    }
                }
                if (changedTrees.Count == 0)
                {
                    // 统一口径（A210）：头（ok/tool/target/items + op/oldName/newName）+ 摘要行
                    Dictionary<string, object> mrNoneMeta = new Dictionary<string, object>();
                    mrNoneMeta["op"] = "rename";
                    mrNoneMeta["oldName"] = oldName;
                    mrNoneMeta["newName"] = newName;
                    result = MetaHead("cs-member", true, className, 0, mrNoneMeta) + Environment.NewLine + Echo(className, "重命名 " + oldName + "→" + newName + " · 0 文件");
                    return true;
                }
                CSharpCompilation trial = cache.Compilation;
                for (int i = 0; i < changedTrees.Count; i = i + 1)
                {
                    trial = (CSharpCompilation)trial.ReplaceSyntaxTree(changedOldTrees[i], changedTrees[i]);
                }
                List<string> newErrors = CollectNewErrors(cache.Compilation, trial);
                List<string> writeNotes = new List<string>();
                for (int i = 0; i < changedFiles.Count; i = i + 1)
                {
                    string filePath = changedFiles[i];
                    SyntaxTree newTree = changedTrees[i];
                    string commitError;
                    string writeNote;
                    if (!CommitTreeWrite(cache, changedOldTrees[i], newTree, out commitError, out writeNote))
                    {
                        result = commitError;
                        return true;
                    }
                    if (writeNote.Length > 0)
                    {
                        writeNotes.Add(Path.GetFileName(filePath) + ": " + writeNote);
                    }
                }
                cache.Compilation = trial;
                // 统一口径（A210）：头（ok/tool/target/items + op/oldName/newName）+ 摘要行
                Dictionary<string, object> mrMeta = new Dictionary<string, object>();
                mrMeta["op"] = "rename";
                mrMeta["oldName"] = oldName;
                mrMeta["newName"] = newName;
                if (writeNotes.Count > 0)
                {
                    mrMeta["writeNote"] = string.Join(" | ", writeNotes);
                }
                NoteCompileErrors(mrMeta, newErrors);
                result = MetaHead("cs-member", true, className, changedFiles.Count, mrMeta) + Environment.NewLine + Echo(className, "重命名 " + oldName + "→" + newName + " · " + changedFiles.Count + " 文件") + CompileDiagBlock(newErrors);
                return true;
            }
        }

        /// <summary>
        /// 成员声明名提取（anchor 匹配用）
        /// </summary>
        /// <param name="member">成员声明</param>
        /// <returns>成员名（无则空串）</returns>
        private static string MemberNameOf(MemberDeclarationSyntax member)
        {
            MethodDeclarationSyntax? method = member as MethodDeclarationSyntax;
            if (method != null)
            {
                return method.Identifier.Text;
            }
            PropertyDeclarationSyntax? property = member as PropertyDeclarationSyntax;
            if (property != null)
            {
                return property.Identifier.Text;
            }
            FieldDeclarationSyntax? field = member as FieldDeclarationSyntax;
            if (field != null && field.Declaration != null && field.Declaration.Variables.Count > 0)
            {
                return field.Declaration.Variables[0].Identifier.Text;
            }
            ConstructorDeclarationSyntax? ctor = member as ConstructorDeclarationSyntax;
            if (ctor != null)
            {
                return ctor.Identifier.Text;
            }
            return "";
        }
    }

    /// <summary>
    /// 重命名重写器——语义验证的引用/声明替换（仅绑定符号 == 目标集才改；局部变量/同名异符零误伤）
    /// </summary>
    internal sealed class SymbolRenamingRewriter : CSharpSyntaxRewriter
    {
        /// <summary>
        /// 语义模型（当前文件）
        /// </summary>
        private readonly SemanticModel _model;

        /// <summary>
        /// 目标符号集（重载全匹配）
        /// </summary>
        private readonly HashSet<ISymbol> _targets;

        /// <summary>
        /// 旧名（快速过滤）
        /// </summary>
        private readonly string _oldName;

        /// <summary>
        /// 新名
        /// </summary>
        private readonly string _newName;

        /// <summary>
        /// 是否有变更
        /// </summary>
        public bool Changed;

        /// <summary>
        /// 创建重写器
        /// </summary>
        /// <param name="model">语义模型</param>
        /// <param name="targets">目标符号集</param>
        /// <param name="oldName">旧名</param>
        /// <param name="newName">新名</param>
        public SymbolRenamingRewriter(SemanticModel model, HashSet<ISymbol> targets, string oldName, string newName)
        {
            _model = model;
            _targets = targets;
            _oldName = oldName;
            _newName = newName;
            Changed = false;
        }

        /// <summary>
        /// 引用处——IdentifierName / GenericName（语义绑定验证——泛型调用 Bind&lt;T&gt; 也覆盖）
        /// </summary>
        public override SyntaxNode? VisitIdentifierName(IdentifierNameSyntax node)
        {
            return RewriteIfTarget(node, node.Identifier);
        }

        /// <summary>
        /// 泛型调用名——如 DataBox.Bind&lt;IOA&gt; 的 Bind
        /// </summary>
        public override SyntaxNode? VisitGenericName(GenericNameSyntax node)
        {
            if (node.Identifier.Text == _oldName)
            {
                SymbolInfo info = _model.GetSymbolInfo(node);
                if (info.Symbol != null && _targets.Contains(info.Symbol))
                {
                    Changed = true;
                    return node.WithIdentifier(SyntaxFactory.Identifier(_newName).WithTriviaFrom(node.Identifier));
                }
                for (int i = 0; i < info.CandidateSymbols.Length; i = i + 1)
                {
                    if (_targets.Contains(info.CandidateSymbols[i]))
                    {
                        Changed = true;
                        return node.WithIdentifier(SyntaxFactory.Identifier(_newName).WithTriviaFrom(node.Identifier));
                    }
                }
            }
            return base.VisitGenericName(node);
        }

        /// <summary>
        /// 目标符号替换判断——IdentifierName 共用逻辑
        /// </summary>
        /// <param name="node">节点</param>
        /// <param name="token">标识符</param>
        /// <returns>替换后节点</returns>
        private SyntaxNode? RewriteIfTarget(IdentifierNameSyntax node, SyntaxToken token)
        {
            if (token.Text == _oldName)
            {
                SymbolInfo info = _model.GetSymbolInfo(node);
                if (info.Symbol != null && _targets.Contains(info.Symbol))
                {
                    Changed = true;
                    return node.WithIdentifier(SyntaxFactory.Identifier(_newName).WithTriviaFrom(node.Identifier));
                }
                for (int i = 0; i < info.CandidateSymbols.Length; i = i + 1)
                {
                    if (_targets.Contains(info.CandidateSymbols[i]))
                    {
                        Changed = true;
                        return node.WithIdentifier(SyntaxFactory.Identifier(_newName).WithTriviaFrom(node.Identifier));
                    }
                }
            }
            return base.VisitIdentifierName(node);
        }

        /// <summary>
        /// 方法声明处
        /// </summary>
        public override SyntaxNode? VisitMethodDeclaration(MethodDeclarationSyntax node)
        {
            if (node.Identifier.Text == _oldName)
            {
                ISymbol? symbol = _model.GetDeclaredSymbol(node);
                if (symbol != null && _targets.Contains(symbol))
                {
                    Changed = true;
                    node = node.WithIdentifier(SyntaxFactory.Identifier(_newName).WithTriviaFrom(node.Identifier));
                }
            }
            return base.VisitMethodDeclaration(node);
        }

        /// <summary>
        /// 属性声明处
        /// </summary>
        public override SyntaxNode? VisitPropertyDeclaration(PropertyDeclarationSyntax node)
        {
            if (node.Identifier.Text == _oldName)
            {
                ISymbol? symbol = _model.GetDeclaredSymbol(node);
                if (symbol != null && _targets.Contains(symbol))
                {
                    Changed = true;
                    node = node.WithIdentifier(SyntaxFactory.Identifier(_newName).WithTriviaFrom(node.Identifier));
                }
            }
            return base.VisitPropertyDeclaration(node);
        }

        /// <summary>
        /// 变量声明处——字段（局部变量符号 ≠ 目标——自动安全）
        /// </summary>
        public override SyntaxNode? VisitVariableDeclarator(VariableDeclaratorSyntax node)
        {
            if (node.Identifier.Text == _oldName)
            {
                ISymbol? symbol = _model.GetDeclaredSymbol(node);
                if (symbol != null && _targets.Contains(symbol))
                {
                    Changed = true;
                    node = node.WithIdentifier(SyntaxFactory.Identifier(_newName).WithTriviaFrom(node.Identifier));
                }
            }
            return base.VisitVariableDeclarator(node);
        }
    }
}
