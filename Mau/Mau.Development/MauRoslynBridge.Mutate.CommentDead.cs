using System;
using System.Collections.Generic;
using System.Text;
using System.Text.Json;
using System.Xml.Linq;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;

namespace Mau.Development
{
#pragma warning disable CS8600 // 环境 SDK 对 ConcurrentDictionary/Dictionary TryXXX(out TValue) 的 MaybeNullWhen 分析异常误报——out 实参非空变量也报警（最小复现验证）
    /// <summary>
    /// MauRoslynBridge 元数据面分部——cs.comment（XML 注释增改）+ cs.dead（零引用扫描，partial 分部）。
    /// 写操作统一：磁盘为准——恒落盘，编译诊断随回执返回（不阻断）；盘面校验 + 树差异落盘。
    /// </summary>
    public sealed partial class MauRoslynBridge
    {
        /// <summary>
        /// cs.comment——XML 注释增改（summary/param/returns；member 空=类）
        /// </summary>
        /// <param name="args">参数</param>
        /// <param name="result">结果</param>
        /// <returns>调用完成</returns>
        private bool ToolComment(JsonElement args, out string result)
        {
            string path = Arg(args, "path");
            string className = Arg(args, "class");
            string member = Arg(args, "member");
            string type = Arg(args, "type");
            string text = Arg(args, "text");
            string paramName = Arg(args, "param");
            if (className.Length == 0 || type.Length == 0 || text.Length == 0)
            {
                result = "ERR|BAD_ARGS|缺参数 class/type/text（必填：path class type text）";
                return false;
            }
            if (type != "summary" && type != "param" && type != "returns")
            {
                result = "ERR|BAD_ARGS|type 非法值: " + type + "（summary|param|returns）";
                return false;
            }
            if (type == "param" && paramName.Length == 0)
            {
                result = "ERR|BAD_ARGS|缺参数 param（type=param 时必须）";
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
                SyntaxTree foundTree = parts[0].Tree;
                ClassDeclarationSyntax classNode = parts[0].Node;
                SyntaxNode target = classNode;
                if (member.Length > 0)
                {
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
                    target = memberNode;
                    foundTree = memberNode.SyntaxTree;
                }
                XElement rootXml;
                try
                {
                    rootXml = ParseDocComments(target);
                }
                catch (Exception ex)
                {
                    result = "ERR|DOC_PARSE|doc 注释解析失败: " + ex.Message;
                    return true;
                }
                if (type == "summary")
                {
                    XElement? summary = rootXml.Element("summary");
                    if (summary == null)
                    {
                        summary = new XElement("summary");
                        rootXml.AddFirst(summary);
                    }
                    summary.Value = text;
                }
                else if (type == "param")
                {
                    XElement? param = null;
                    foreach (XElement element in rootXml.Elements("param"))
                    {
                        string? nameAttr = element.Attribute("name")?.Value;
                        if (nameAttr == paramName)
                        {
                            param = element;
                            break;
                        }
                    }
                    if (param == null)
                    {
                        param = new XElement("param", new XAttribute("name", paramName));
                        rootXml.Add(param);
                    }
                    param.Value = text;
                }
                else
                {
                    XElement? returns = rootXml.Element("returns");
                    if (returns == null)
                    {
                        returns = new XElement("returns");
                        rootXml.Add(returns);
                    }
                    returns.Value = text;
                }
                StringBuilder docText = new StringBuilder();
                // 换行风格——跟随目标文件（\r\n 或 \n——防止注释与声明同行语法崩坏 CS1022）
                string newline = DetectNewLine(foundTree);
                SyntaxTriviaList oldTrivia = target.GetLeadingTrivia();
                // 缩进——doc 首行缩进由前置空白 trivia 承担（重建时原样保留）；续行须自带同宽缩进，否则续行顶格
                string indent = "";
                for (int i = 0; i < oldTrivia.Count; i = i + 1)
                {
                    if (oldTrivia[i].IsKind(SyntaxKind.SingleLineDocumentationCommentTrivia) ||
                        oldTrivia[i].IsKind(SyntaxKind.MultiLineDocumentationCommentTrivia))
                    {
                        break;
                    }
                    if (oldTrivia[i].IsKind(SyntaxKind.WhitespaceTrivia))
                    {
                        indent = oldTrivia[i].ToString();
                    }
                }
                int docLineIndex = 0;
                foreach (XElement element in rootXml.Elements())
                {
                    if (docLineIndex > 0)
                    {
                        docText.Append(indent);
                    }
                    AppendDocLine(docText, SerializeDocElement(element), indent, newline);
                    docLineIndex = docLineIndex + 1;
                }
                SyntaxTriviaList newDocTrivia = SyntaxFactory.ParseLeadingTrivia(docText.ToString());
                // 重建 leading——原 doc 块（doc trivia + 紧邻 EOL）整段替换为新 doc（其余 trivia 原样保留）
                List<SyntaxTrivia> rebuilt = new List<SyntaxTrivia>();
                bool replaced = false;
                for (int i = 0; i < oldTrivia.Count; i = i + 1)
                {
                    SyntaxTrivia trivia = oldTrivia[i];
                    bool isDoc = trivia.IsKind(SyntaxKind.SingleLineDocumentationCommentTrivia) ||
                                 trivia.IsKind(SyntaxKind.MultiLineDocumentationCommentTrivia);
                    if (isDoc && !replaced)
                    {
                        replaced = true;
                        for (int j = 0; j < newDocTrivia.Count; j = j + 1)
                        {
                            rebuilt.Add(newDocTrivia[j]);
                        }
                        // 吞掉旧 doc 后紧邻的 EOL/doc trivia——新 doc 自带行尾换行（防双换行空行）
                        while (i + 1 < oldTrivia.Count &&
                               (oldTrivia[i + 1].IsKind(SyntaxKind.EndOfLineTrivia) ||
                                oldTrivia[i + 1].IsKind(SyntaxKind.SingleLineDocumentationCommentTrivia) ||
                                oldTrivia[i + 1].IsKind(SyntaxKind.MultiLineDocumentationCommentTrivia)))
                        {
                            i = i + 1;
                        }
                    }
                    else
                    {
                        rebuilt.Add(trivia);
                    }
                }
                if (!replaced)
                {
                    // 原无 doc 注释——doc 追加到尾部 whitespace 之前，成员缩进保留在 doc 后（声明不顶格）
                    SyntaxTrivia indentTrivia = default(SyntaxTrivia);
                    bool hasIndent = false;
                    for (int j = rebuilt.Count - 1; j >= 0; j = j - 1)
                    {
                        if (rebuilt[j].IsKind(SyntaxKind.WhitespaceTrivia))
                        {
                            indentTrivia = rebuilt[j];
                            hasIndent = true;
                            break;
                        }
                    }
                    for (int j = 0; j < newDocTrivia.Count; j = j + 1)
                    {
                        rebuilt.Add(newDocTrivia[j]);
                    }
                    if (hasIndent)
                    {
                        rebuilt.Add(indentTrivia);
                    }
                }
                SyntaxNode newNode = target.WithLeadingTrivia(rebuilt);
                SyntaxNode root = foundTree.GetRoot();
                SyntaxNode newRoot = root.ReplaceNode(target, newNode);
                SyntaxTree newTree = CreateTreeFromRoot(foundTree, newRoot);
                // 写盘裁决——磁盘为准：编译诊断随回执可见，不再阻断（与 patch / member 同规）
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
                // 统一口径（A210）：头（ok/tool/target/items + type）+ 摘要行
                string commentTarget = member.Length > 0 ? className + "." + member : className;
                Dictionary<string, object> cmMeta = new Dictionary<string, object>();
                cmMeta["type"] = type;
                if (writeNote.Length > 0)
                {
                    cmMeta["writeNote"] = writeNote;
                }
                NoteCompileErrors(cmMeta, newErrors);
                result = MetaHead("cs-comment", true, commentTarget, -1, cmMeta) + Environment.NewLine + Echo(commentTarget, "改 " + type) + CompileDiagBlock(newErrors);
                return true;
            }
        }

        /// <summary>
        /// doc 元素序列化——& 原样（防 &amp; 污染文件）、&lt;/&gt; 转义保 XML 结构、param name 属性转义
        /// </summary>
        /// <param name="element">doc 元素</param>
        /// <returns>doc 行文本（不含 /// 前缀）</returns>
        private static string SerializeDocElement(XElement element)
        {
            string name = element.Name.LocalName;
            string body = element.Value.Replace("<", "&lt;").Replace(">", "&gt;");
            if (name == "param")
            {
                string attr = (element.Attribute("name")?.Value ?? "").Replace("&", "&amp;").Replace("\"", "&quot;").Replace("<", "&lt;").Replace(">", "&gt;");
                return "<param name=\"" + attr + "\">" + body + "</param>";
            }
            return "<" + name + ">" + body + "</" + name + ">";
        }

        /// <summary>
        /// doc 行追加——元素文本含多行时逐行补 /// 前缀（续行不补则沦为代码行 CS1022）
        /// </summary>
        /// <param name="target">doc 文本缓冲</param>
        /// <param name="body">元素文本（不含 /// 前缀，可含换行）</param>
        /// <param name="indent">续行缩进</param>
        /// <param name="newline">目标换行</param>
        private static void AppendDocLine(StringBuilder target, string body, string indent, string newline)
        {
            string[] lines = NormalizeNewLineText(body, newline).Split('\n');
            for (int i = 0; i < lines.Length; i = i + 1)
            {
                string line = lines[i];
                if (line.Length > 0 && line[line.Length - 1] == '\r')
                {
                    line = line.Substring(0, line.Length - 1);
                }
                if (i > 0)
                {
                    target.Append(newline);
                    target.Append(indent);
                }
                target.Append("/// ");
                target.Append(line);
            }
            target.Append(newline);
        }

        /// <summary>
        /// 逐行剥离行首空白——doc 续行缩进是版面（非内容）：留着会让 value 带上缩进并在重复覆写时逐轮累积
        /// </summary>
        /// <param name="text">多行文本</param>
        /// <returns>剥净行首空白的文本</returns>
        private static string StripLineIndent(string text)
        {
            string[] lines = NormalizeNewLineText(text, "\n").Split('\n');
            StringBuilder sb = new StringBuilder();
            for (int i = 0; i < lines.Length; i = i + 1)
            {
                if (i > 0)
                {
                    sb.Append('\n');
                }
                sb.Append(lines[i].TrimStart(' ', '\t'));
            }
            return sb.ToString();
        }

        /// <summary>
        /// 解析节点 doc 注释为 XML 根元素（<root> 包裹）
        /// </summary>
        /// <param name="node">声明节点</param>
        /// <returns>根元素（含 summary/param/returns 子元素）</returns>
        private static XElement ParseDocComments(SyntaxNode node)
        {
            StringBuilder raw = new StringBuilder();
            foreach (SyntaxTrivia trivia in node.GetLeadingTrivia())
            {
                if (trivia.IsKind(SyntaxKind.SingleLineDocumentationCommentTrivia) ||
                    trivia.IsKind(SyntaxKind.MultiLineDocumentationCommentTrivia))
                {
                    raw.Append(trivia.ToString());
                }
            }
            XElement rootElement;
            if (raw.Length == 0)
            {
                rootElement = new XElement("root");
                return rootElement;
            }
            string xmlText = raw.ToString();
            int firstLt = xmlText.IndexOf('<');
            if (firstLt < 0)
            {
                rootElement = new XElement("root");
                return rootElement;
            }
            xmlText = xmlText.Substring(firstLt);
            xmlText = xmlText.Replace("///", "").Replace("/*", "").Replace("*/", "");
            // 续行缩进剥离——doc 版面缩进不是内容（防 value 带缩进 + 重复覆写逐轮累积）
            xmlText = StripLineIndent(xmlText);
            XDocument doc = XDocument.Parse("<root>" + xmlText + "</root>", LoadOptions.None);
            rootElement = doc.Root!;
            return rootElement;
        }

        /// <summary>
        /// cs.dead——零引用成员（private/internal；public/override/构造跳过）
        /// </summary>
        /// <param name="args">参数</param>
        /// <param name="result">结果</param>
        /// <returns>调用完成</returns>
        private bool ToolDead(JsonElement args, out string result)
        {
            string path = Arg(args, "path");
            string resolveError;
            List<string> projects = ResolveProjects(path, out resolveError);
            if (projects.Count == 0)
            {
                result = resolveError;
                return false;
            }
            if (projects.Count == 1)
            {
                return DeadSingle(projects[0], out result);
            }
            // 多项目聚合——跨程序集引用复核：单项目语义只见本项目源码符号，兄弟项目对它的引用解析为
            // 元数据符号（对方 dll），须按引用键跨项目比对（判例 2026-09-16：入口壳调用的域成员被误报零引用）
            List<ProjectCache> caches = new List<ProjectCache>();
            for (int i = 0; i < projects.Count; i = i + 1)
            {
                ProjectCache cache = EnsureProject(projects[i]);
                FullScan(cache);
                caches.Add(cache);
            }
            HashSet<string> crossKeys = CollectReferenceKeys(caches);
            // 统一口径（A210）：聚合头（ok/tool/target/items/dead）+ 摘要行 + 逐项目分节（节内无头）
            string entry = RelativeToRoots(ResolveInRoots(path));
            int totalDead = 0;
            List<string> sections = new List<string>();
            for (int i = 0; i < caches.Count; i = i + 1)
            {
                int oneDead;
                string one = DeadInCache(caches[i], crossKeys, out oneDead);
                totalDead = totalDead + oneDead;
                sections.Add("── " + RelativeToRoots(projects[i]) + " ──" + Environment.NewLine + StripHead(one));
            }
            Dictionary<string, object> aggMeta = new Dictionary<string, object>();
            aggMeta["dead"] = totalDead;
            StringBuilder sb = new StringBuilder();
            sb.Append(MetaHead("cs-dead", totalDead == 0, entry, projects.Count, aggMeta));
            sb.Append(Environment.NewLine + Echo(entry, projects.Count + " 项目 · 死码 " + totalDead));
            for (int i = 0; i < sections.Count; i = i + 1)
            {
                sb.Append(Environment.NewLine);
                sb.Append(sections[i]);
            }
            result = TrimResult(sb.ToString(), MaxResultChars);
            return true;
        }
        /// <summary>
        /// 单项目 dead——零引用成员扫描（private/internal；public/override/构造跳过）。
        /// </summary>
        /// <param name="csproj">csproj 绝对路径</param>
        /// <param name="result">结果文本</param>
        /// <returns>调用完成</returns>
        private bool DeadSingle(string csproj, out string result)
        {
            ProjectCache cache = EnsureProject(csproj);
            FullScan(cache);
            int deadCount;
            result = DeadInCache(cache, null, out deadCount);
            return true;
        }

        /// <summary>
        /// cs-comment_check——缺 summary 注释扫描（类 + 成员；语法层——复用 FirstSummary）
        /// </summary>
        /// <param name="args">参数</param>
        /// <param name="result">结果</param>
        /// <returns>调用完成</returns>
        private bool ToolCommentCheck(JsonElement args, out string result)
        {
            string path = Arg(args, "path");
            string resolveError;
            List<string> projects = ResolveProjects(path, out resolveError);
            if (projects.Count == 0)
            {
                result = resolveError;
                return false;
            }
            if (projects.Count == 1)
            {
                int singleMissing;
                return CommentCheckSingle(projects[0], out singleMissing, out result);
            }
            // 统一口径（A210）：聚合头（ok/tool/target/items/missing）+ 摘要行 + 逐项目分节（节内无头）
            string ccEntry = RelativeToRoots(ResolveInRoots(path));
            int totalMissing = 0;
            StringBuilder sb = new StringBuilder();
            List<string> ccSections = new List<string>();
            for (int i = 0; i < projects.Count; i = i + 1)
            {
                string one;
                int oneMissing;
                CommentCheckSingle(projects[i], out oneMissing, out one);
                totalMissing = totalMissing + oneMissing;
                ccSections.Add("── " + RelativeToRoots(projects[i]) + " ──" + Environment.NewLine + StripHead(one));
            }
            Dictionary<string, object> ccAgg = new Dictionary<string, object>();
            ccAgg["missing"] = totalMissing;
            sb.Append(MetaHead("cs-comment_check", totalMissing == 0, ccEntry, projects.Count, ccAgg));
            sb.Append(Environment.NewLine + Echo(ccEntry, projects.Count + " 项目 · 缺 " + totalMissing));
            for (int i = 0; i < ccSections.Count; i = i + 1)
            {
                sb.Append(Environment.NewLine);
                sb.Append(ccSections[i]);
            }
            result = TrimResult(sb.ToString(), MaxResultChars);
            return true;
        }
        /// <summary>
        /// 单项目 comment_check——缺 summary 注释扫描（类 + 成员；语法层——复用 FirstSummary）。
        /// </summary>
        /// <param name="csproj">csproj 绝对路径</param>
        /// <param name="missingCount">缺失 summary 条数</param>
        /// <param name="result">结果文本</param>
        /// <returns>调用完成</returns>
        private bool CommentCheckSingle(string csproj, out int missingCount, out string result)
        {
            ProjectCache cache = EnsureProject(csproj);
            FullScan(cache);
            List<string> missingLines = new List<string>();
            int checkedCount = 0;
            foreach (KeyValuePair<string, SyntaxTree> pair in cache.Trees)
            {
                SyntaxNode root = pair.Value.GetRoot();
                foreach (SyntaxNode node in root.DescendantNodes())
                {
                    TypeDeclarationSyntax? typeDecl = node as TypeDeclarationSyntax;
                    if (typeDecl == null)
                    {
                        continue;
                    }
                    string typeName = typeDecl.Identifier.Text;
                    int typeLine = typeDecl.GetLocation().GetLineSpan().StartLinePosition.Line + 1;
                    checkedCount = checkedCount + 1;
                    if (FirstSummary(typeDecl).Length == 0)
                    {
                        missingLines.Add(typeName + " // 缺 summary（L" + typeLine.ToString() + "）");
                    }
                    for (int i = 0; i < typeDecl.Members.Count; i = i + 1)
                    {
                        MemberDeclarationSyntax member = typeDecl.Members[i];
                        string memberName = DescribeMember(member);
                        if (memberName.Length == 0)
                        {
                            continue;
                        }
                        int memberLine = member.GetLocation().GetLineSpan().StartLinePosition.Line + 1;
                        checkedCount = checkedCount + 1;
                        if (FirstSummary(member).Length == 0)
                        {
                            missingLines.Add("  " + typeName + "." + memberName + " // 缺 summary（L" + memberLine.ToString() + "）");
                        }
                    }
                }
            }
            missingLines.Sort(StringComparer.Ordinal);
            // 统一口径（A210）：头（ok/tool/target/items + missing）+ 摘要行 + 缺失清单
            missingCount = missingLines.Count;
            Dictionary<string, object> ccMeta = new Dictionary<string, object>();
            ccMeta["missing"] = missingCount;
            StringBuilder sb = new StringBuilder();
            sb.Append(MetaHead("cs-comment_check", missingLines.Count == 0, cache.AssemblyName, checkedCount, ccMeta));
            sb.Append(Environment.NewLine + Echo(cache.AssemblyName, checkedCount + " 成员 · 缺 " + missingLines.Count));
            for (int i = 0; i < missingLines.Count; i = i + 1)
            {
                sb.Append(Environment.NewLine + missingLines[i].TrimStart());
            }
            result = TrimResult(sb.ToString(), MaxResultChars);
            return true;
        }

        /// <summary>
        /// 成员描述名——comment_check 缺注释清单行（方法/属性/字段/构造函数；其余空串跳过）
        /// </summary>
        /// <param name="member">成员声明</param>
        /// <returns>描述名</returns>
        private static string DescribeMember(MemberDeclarationSyntax member)
        {
            MethodDeclarationSyntax? method = member as MethodDeclarationSyntax;
            if (method != null)
            {
                return method.Identifier.Text + method.ParameterList.ToString();
            }
            PropertyDeclarationSyntax? property = member as PropertyDeclarationSyntax;
            if (property != null)
            {
                return property.Identifier.Text;
            }
            FieldDeclarationSyntax? field = member as FieldDeclarationSyntax;
            if (field != null)
            {
                return field.Declaration.ToString().Replace("\r\n", " ").Replace("\n", " ");
            }
            ConstructorDeclarationSyntax? ctor = member as ConstructorDeclarationSyntax;
            if (ctor != null)
            {
                return ctor.Identifier.Text + ctor.ParameterList.ToString();
            }
            return "";
        }
    }
}
