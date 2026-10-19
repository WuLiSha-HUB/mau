using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.IO;
using System.Text;
using System.Text.Json;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Microsoft.CodeAnalysis.CSharp.Syntax;
using Mau.Runtime;

namespace Mau.Development
{
#pragma warning disable CS8600 // 环境 SDK 对 ConcurrentDictionary/Dictionary TryXXX(out TValue) 的 MaybeNullWhen 分析异常误报——out 实参非空变量也报警（最小复现验证）
    /// <summary>
    /// C# 工具桥实现——Roslyn 编码工具域缓存桥（design-ch4-cs.md D1-D3）。
    /// 磁盘权威 + 快照快链（mtime+size）与内容比较兜底 + 项目键隔离常驻池（LRU 4）+ 树/编译/语义三态无感。
    /// 引用集 = 目标项目已 build 的 bin 产物 + TPA + 共享框架探测（AspNetCore/WindowsDesktop——莎拍板 A 方案）——
    /// bin 缺失 → 引导先 cs.build（check 快、build 权威的闭环）。
    /// 工具面 13 件：check / build / test / list / read / find_ref / find / patch / member / comment / dead / comment_check / format。
    /// </summary>
    public sealed partial class MauRoslynBridge : ICSharpBridge
    {
        /// <summary>
        /// 池上限——LRU 淘汰只丢内存缓存（磁盘权威无损失）
        /// </summary>
        private const int PoolLimit = 4;

        /// <summary>
        /// 单结果截断上限——回传 LLM 上下文防爆
        /// </summary>
        private const int MaxResultChars = 20000;

        /// <summary>
        /// 受控根列表——csproj 绝对路径必须落在某根内（同 mau.* 工具的仓库根校验语义）
        /// </summary>
        private readonly string[] _roots;

        /// <summary>
        /// 受控根 id 列表——与 _roots 对齐（id: 命名空间寻址——runtime:/mau:/ccbp:；对齐 FileSystemService）
        /// </summary>
        private readonly string[] _rootIds;

        /// <summary>
        /// 项目缓存池——Key = csproj 绝对路径（GetFullPath 归一化；OrdinalIgnoreCase）
        /// </summary>
        private readonly ConcurrentDictionary<string, ProjectCache> _pool;

        /// <summary>
        /// 建条目串行锁——首次加载（Parse 全树）不并发
        /// </summary>
        private readonly object _poolGate;

        /// <summary>
        /// LRU 淘汰锁
        /// </summary>
        private readonly object _lruGate;
        // A66（2026-09-20）：行文本缓存改调用内局部——原静态单槽跨会话并发串味（字段已退役）

        /// <summary>
        /// 创建工具桥——受控根用于项目路径越界校验（id: 命名空间寻址对齐 FileSystemService）
        /// </summary>
        /// <param name="rootEntries">受控根条目（id + 绝对路径；csproj 必须在其内）</param>
        public MauRoslynBridge(WorkspaceConfig.RootEntry[] rootEntries)
        {
            if (rootEntries == null || rootEntries.Length == 0)
            {
                throw new ArgumentException("MauRoslynBridge 需要至少一个受控根。", "rootEntries");
            }
            List<string> normalized = new List<string>();
            List<string> ids = new List<string>();
            for (int i = 0; i < rootEntries.Length; i = i + 1)
            {
                WorkspaceConfig.RootEntry entry = rootEntries[i];
                if (entry == null || string.IsNullOrWhiteSpace(entry.Path))
                {
                    throw new ArgumentException("受控根路径为空。", "rootEntries");
                }
                normalized.Add(Path.GetFullPath(entry.Path));
                string entryId = entry.Id;
                if (entryId == null || entryId.Length == 0)
                {
                    entryId = "root" + i.ToString();
                }
                ids.Add(entryId);
            }
            _roots = normalized.ToArray();
            _rootIds = ids.ToArray();
            _pool = new ConcurrentDictionary<string, ProjectCache>(StringComparer.OrdinalIgnoreCase);
            _poolGate = new object();
            _lruGate = new object();
        }

        /// <summary>
        /// 单方法调度入口——method 白名单 12 件分派；异常不外泄（ERR|EX）
        /// </summary>
        /// <param name="method">操作名</param>
        /// <param name="argsJson">展平参数 JSON</param>
        /// <param name="result">结果文本</param>
        /// <returns>true=调用完成（语义成功与否看 result 前缀）</returns>
        public bool Invoke(string method, string argsJson, out string result)
        {
            try
            {
                JsonDocument args;
                try
                {
                    args = JsonUtil.ParseStrict(argsJson);
                }
                catch (Exception ex)
                {
                    result = "ERR|BAD_ARGS|参数 JSON 解析失败: " + ex.Message;
                    return false;
                }
                using (args)
                {
                    JsonElement root = args.RootElement;
                    // 参数面零容忍——未知 / 缺值 / 非法值一律 ERR|BAD_ARGS（校验归工具内部；宿主注入保留键放行）
                    string badArgs = ValidateToolArgs(method, root);
                    if (badArgs.Length > 0)
                    {
                        result = badArgs;
                        return false;
                    }
                    if (method == "check")
                    {
                        return ToolCheck(root, out result);
                    }
                    if (method == "build")
                    {
                        return ToolBuild(root, out result);
                    }
                    if (method == "test")
                    {
                        return ToolTest(root, out result);
                    }
                    if (method == "list")
                    {
                        return ToolList(root, out result);
                    }
                    if (method == "read")
                    {
                        return ToolRead(root, out result);
                    }
                    if (method == "find_ref")
                    {
                        return ToolFindRef(root, out result);
                    }
                    if (method == "find")
                    {
                        return ToolFind(root, out result);
                    }
                    if (method == "patch")
                    {
                        return ToolPatch(root, out result);
                    }
                    if (method == "member")
                    {
                        return ToolMember(root, out result);
                    }
                    if (method == "comment")
                    {
                        return ToolComment(root, out result);
                    }
                    if (method == "dead")
                    {
                        return ToolDead(root, out result);
                    }
                    if (method == "comment_check")
                    {
                        return ToolCommentCheck(root, out result);
                    }
                    if (method == "format")
                    {
                        return ToolFormat(root, out result);
                    }
                    result = "ERR|UNKNOWN_METHOD|未知方法: " + method;
                    return false;
                }
            }
            catch (Exception ex)
            {
                result = "ERR|EX|" + ex.GetType().Name + ": " + ex.Message;
                return false;
            }
        }

        /// <summary>
        /// 单项目入口解析——三态入口（csproj / .sln / 目录）统一解析后定位目标项目。
        /// 入口恰含一个项目时直接采用；多项目入口按类声明跨项目定位（唯一命中即采用，多处命中出声）。
        /// </summary>
        /// <param name="pathParam">路径参数（受控根内，支持 csproj / .sln / 目录）</param>
        /// <param name="className">类名（多项目入口的定位依据）</param>
        /// <param name="csproj">出参：目标 csproj 绝对路径（失败为空串）</param>
        /// <param name="error">出参：失败原因（含错误码；成功为空串）</param>
        /// <returns>是否解析成功</returns>
        private bool ResolveSingleProject(string pathParam, string className, out string csproj, out string error)
        {
            csproj = "";
            List<string> projects = ResolveProjects(pathParam, out error);
            if (projects.Count == 0)
            {
                return false;
            }
            if (projects.Count == 1)
            {
                csproj = projects[0];
                error = "";
                return true;
            }
            // 多项目入口——按类声明定位（轻量扫描；唯一命中即采用）
            List<string> hitProjects = new List<string>();
            List<string> hitPlaces = new List<string>();
            for (int i = 0; i < projects.Count; i = i + 1)
            {
                List<string> places = LocateClassInProject(projects[i], className);
                if (places.Count == 0)
                {
                    continue;
                }
                hitProjects.Add(projects[i]);
                for (int j = 0; j < places.Count; j = j + 1)
                {
                    hitPlaces.Add(RelativeToRoots(projects[i]).Replace(Path.DirectorySeparatorChar, '/') + " · " + places[j]);
                }
            }
            if (hitProjects.Count == 0)
            {
                error = "ERR|CLASS_NOT_FOUND|类不存在: " + className + "（入口含 " + projects.Count + " 个项目，已全扫）";
                return false;
            }
            if (hitProjects.Count > 1)
            {
                StringBuilder sb = new StringBuilder();
                sb.Append("ERR|CLASS_AMBIGUOUS|类名在 " + hitProjects.Count + " 个项目内命中: " + className);
                for (int i = 0; i < hitPlaces.Count; i = i + 1)
                {
                    sb.Append(Environment.NewLine + "  " + hitPlaces[i]);
                }
                sb.Append(Environment.NewLine + "——请改传命中项目的 csproj 路径");
                error = sb.ToString();
                return false;
            }
            csproj = hitProjects[0];
            error = "";
            return true;
        }

        /// <summary>
        /// 项目内核验类声明——轻量语法扫描（逐源文件 Parse 找类声明，不建引用集 / 编译态），供多项目入口定位使用。
        /// </summary>
        /// <param name="csproj">csproj 绝对路径</param>
        /// <param name="className">类名</param>
        /// <returns>命中位置清单（「相对路径:L行」；无命中为空列表）</returns>
        private List<string> LocateClassInProject(string csproj, string className)
        {
            List<string> places = new List<string>();
            ProjectCache probe = new ProjectCache();
            probe.ProjectDir = Path.GetDirectoryName(csproj) ?? "";
            string[] sources = CollectProjectSources(csproj, true);
            for (int i = 0; i < sources.Length; i = i + 1)
            {
                SyntaxTree tree;
                try
                {
                    tree = ParseFile(sources[i]);
                }
                catch (IOException)
                {
                    continue;
                }
                catch (UnauthorizedAccessException)
                {
                    continue;
                }
                foreach (SyntaxNode node in tree.GetRoot().DescendantNodes())
                {
                    ClassDeclarationSyntax? decl = node as ClassDeclarationSyntax;
                    if (decl != null && decl.Identifier.Text == className)
                    {
                        int line = decl.GetLocation().GetLineSpan().StartLinePosition.Line + 1;
                        places.Add(RelativeToProject(probe, sources[i]).Replace(Path.DirectorySeparatorChar, '/') + ":L" + line);
                    }
                }
            }
            return places;
        }
        /// <summary>
        /// 受控根内路径归一——id: 命名空间寻址（项目面与文件面共用的**唯一**入口路径解析；不要求路径存在）。
        /// </summary>
        /// <param name="pathParam">路径参数</param>
        /// <returns>绝对路径（越界 / 空返回空串）</returns>
        private string ResolveInRoots(string pathParam)
        {
            if (string.IsNullOrWhiteSpace(pathParam))
            {
                return "";
            }
            string path = pathParam;
            int nsSep = path.IndexOf(':');
            if (nsSep > 0)
            {
                string nsId = path.Substring(0, nsSep);
                string nsRel = path.Substring(nsSep + 1);
                for (int i = 0; i < _roots.Length; i = i + 1)
                {
                    // A211 根 id 读面大小写不敏感——对齐 FileSystemService.Resolve（全局规范：根 id 一律小写，读取比较点统一 OrdinalIgnoreCase）
                    if (string.Equals(_rootIds[i], nsId, StringComparison.OrdinalIgnoreCase))
                    {
                        path = Path.Combine(_roots[i], nsRel);
                        break;
                    }
                }
            }
            string full;
            if (Path.IsPathFullyQualified(path))
            {
                full = Path.GetFullPath(path);
            }
            else
            {
                full = Path.GetFullPath(Path.Combine(Environment.CurrentDirectory, path));
            }
            for (int i = 0; i < _roots.Length; i = i + 1)
            {
                if (full.StartsWith(_roots[i] + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase) ||
                    string.Equals(full, _roots[i], StringComparison.OrdinalIgnoreCase))
                {
                    return full;
                }
            }
            return "";
        }
        /// <summary>
        /// 解析 .sln 内的 csproj 列表——Project(...) 行取工程相对路径（相对 sln 目录；非 .csproj / 不存在者跳过）。
        /// </summary>
        /// <param name="slnPath">解决方案绝对路径</param>
        /// <param name="projects">出参：csproj 绝对路径列表（按 sln 声明序去重）</param>
        private static void ReadSolutionProjects(string slnPath, List<string> projects)
        {
            string slnDir = Path.GetDirectoryName(slnPath) ?? "";
            string[] lines = File.ReadAllLines(slnPath);
            for (int i = 0; i < lines.Length; i = i + 1)
            {
                string line = lines[i].Trim();
                if (!line.StartsWith("Project(", StringComparison.Ordinal))
                {
                    continue;
                }
                List<int> quotes = new List<int>();
                int cursor = line.IndexOf('"');
                while (cursor >= 0)
                {
                    quotes.Add(cursor);
                    cursor = line.IndexOf('"', cursor + 1);
                }
                if (quotes.Count < 6)
                {
                    continue;
                }
                // 引号对序：① 类型 GUID ② 项目名 ③ 相对路径 ④ 项目 GUID——取第 3 对（索引 4/5）
                string relative = line.Substring(quotes[4] + 1, quotes[5] - quotes[4] - 1);
                if (!relative.EndsWith(".csproj", StringComparison.OrdinalIgnoreCase))
                {
                    continue;
                }
                string full = Path.GetFullPath(Path.Combine(slnDir, relative.Replace('\\', Path.DirectorySeparatorChar)));
                if (!File.Exists(full))
                {
                    continue;
                }
                bool exists = false;
                for (int j = 0; j < projects.Count; j = j + 1)
                {
                    if (string.Equals(projects[j], full, StringComparison.OrdinalIgnoreCase))
                    {
                        exists = true;
                        break;
                    }
                }
                if (!exists)
                {
                    projects.Add(full);
                }
            }
        }
        /// <summary>
        /// 三态入口解析——csproj / .sln / 目录 → csproj 绝对路径列表（唯一入口解析实现；受控根校验 + id: 命名空间寻址）。
        /// 目录入口候选序：顶层 csproj（≥1 时全收）→ 顶层 .sln（唯一则展开，多个出声）→ 无可识别入口。
        /// 失败原因含错误码（BAD_PATH / PATH_NOT_FOUND / ENTRY_UNSUPPORTED / ENTRY_EMPTY / ENTRY_AMBIGUOUS）——调用点直出。
        /// </summary>
        /// <param name="pathParam">路径参数（csproj / .sln / 目录）</param>
        /// <param name="error">出参：失败原因（含错误码；成功为空串）</param>
        /// <returns>csproj 绝对路径列表（目录按名称序；失败返回空列表）</returns>
        private List<string> ResolveProjects(string pathParam, out string error)
        {
            List<string> projects = new List<string>();
            error = "";
            string full = ResolveInRoots(pathParam);
            if (full.Length == 0)
            {
                error = "ERR|BAD_PATH|路径越界或为空（受控根内，支持 csproj / .sln / 目录）: " + pathParam;
                return projects;
            }
            if (Directory.Exists(full))
            {
                string[] found = Directory.GetFiles(full, "*.csproj", SearchOption.TopDirectoryOnly);
                Array.Sort(found, StringComparer.OrdinalIgnoreCase);
                for (int i = 0; i < found.Length; i = i + 1)
                {
                    projects.Add(Path.GetFullPath(found[i]));
                }
                if (projects.Count > 0)
                {
                    return projects;
                }
                // 顶层无 csproj → 顶层 .sln（多项目聚合的规范入口；与 csproj 同为只扫顶层）
                string[] solutions = Directory.GetFiles(full, "*.sln", SearchOption.TopDirectoryOnly);
                Array.Sort(solutions, StringComparer.OrdinalIgnoreCase);
                if (solutions.Length == 1)
                {
                    string solutionPath = Path.GetFullPath(solutions[0]);
                    ReadSolutionProjects(solutionPath, projects);
                    if (projects.Count == 0)
                    {
                        error = "ERR|ENTRY_EMPTY|解决方案内无 csproj: " + solutionPath;
                    }
                    return projects;
                }
                if (solutions.Length > 1)
                {
                    StringBuilder multi = new StringBuilder();
                    multi.Append("ERR|ENTRY_AMBIGUOUS|目录内有 " + solutions.Length + " 个 .sln（仅扫顶层）——请指定其一:");
                    for (int i = 0; i < solutions.Length; i = i + 1)
                    {
                        multi.Append(Environment.NewLine + "  " + RelativeToRoots(solutions[i]));
                    }
                    error = multi.ToString();
                    return projects;
                }
                error = "ERR|ENTRY_EMPTY|目录内无 csproj / .sln（仅扫顶层）: " + full + SubdirCsprojHint(full);
                return projects;
            }
            if (File.Exists(full) && full.EndsWith(".sln", StringComparison.OrdinalIgnoreCase))
            {
                ReadSolutionProjects(full, projects);
                if (projects.Count == 0)
                {
                    error = "ERR|ENTRY_EMPTY|解决方案内无 csproj: " + full;
                }
                return projects;
            }
            if (File.Exists(full) && full.EndsWith(".csproj", StringComparison.OrdinalIgnoreCase))
            {
                projects.Add(Path.GetFullPath(full));
                return projects;
            }
            // 诊断面分列——「路径不是 csproj / .sln / 目录」对不存在路径具误导性（判例 2026-09-14）
            if (File.Exists(full))
            {
                error = "ERR|ENTRY_UNSUPPORTED|路径存在但类型不符（支持 csproj / .sln / 目录）: " + full;
                return projects;
            }
            error = "ERR|PATH_NOT_FOUND|路径不存在: " + full + NeighborCandidates(full);
            return projects;
        }
        /// <summary>
        /// 子目录 csproj 提示——目录入口只扫顶层，此时浅扫一级子目录给出可执行指引
        /// （多项目聚合的规范入口是 .sln，或逐个传 csproj 路径）。
        /// </summary>
        /// <param name="dir">目录绝对路径</param>
        /// <returns>提示串（子目录无 csproj 时空串）</returns>
        private static string SubdirCsprojHint(string dir)
        {
            List<string> names = new List<string>();
            try
            {
                string[] subs = Directory.GetDirectories(dir);
                Array.Sort(subs, StringComparer.OrdinalIgnoreCase);
                for (int i = 0; i < subs.Length; i = i + 1)
                {
                    string[] found = Directory.GetFiles(subs[i], "*.csproj", SearchOption.TopDirectoryOnly);
                    if (found.Length > 0)
                    {
                        names.Add(Path.GetFileName(subs[i]));
                    }
                }
            }
            catch (Exception)
            {
                return "";
            }
            if (names.Count == 0)
            {
                return "";
            }
            return "；子目录发现 " + names.Count + " 个项目（如 " + names[0] + "）——目录入口仅扫顶层，请传 .sln 或具体 csproj";
        }
        /// <summary>
        /// 邻近候选提示——路径不存在时列出同级（父目录）与上一级（祖父目录）内的 .sln / .csproj（各扫 1 级，不递归）。
        /// 结果只进报错文本，不改变寻路结果——不静默切换、不自动适应（失败必须可见）。
        /// </summary>
        /// <param name="full">请求的绝对路径（不存在）</param>
        /// <returns>提示串（无候选时空串）</returns>
        private string NeighborCandidates(string full)
        {
            string name = Path.GetFileName(full);
            string dir = Path.GetDirectoryName(full) ?? "";
            List<string> lines = new List<string>();
            CollectNeighbor(dir, name, "同级", lines);
            string upper = dir.Length > 0 ? (Path.GetDirectoryName(dir) ?? "") : "";
            if (upper.Length > 0 && !string.Equals(upper, dir, StringComparison.OrdinalIgnoreCase))
            {
                CollectNeighbor(upper, name, "上一级", lines);
            }
            if (lines.Count == 0)
            {
                return "";
            }
            StringBuilder sb = new StringBuilder();
            sb.Append("；邻近候选（仅提示，不自动切换）:");
            for (int i = 0; i < lines.Count; i = i + 1)
            {
                sb.Append(Environment.NewLine + "  " + lines[i]);
            }
            return sb.ToString();
        }
        /// <summary>
        /// 单层候选收集——目录内 .sln / .csproj（同名优先，每层上限 3 条）
        /// </summary>
        /// <param name="dir">待扫目录</param>
        /// <param name="wantedName">请求的文件名（同名优先）</param>
        /// <param name="level">层级标签（同级 / 上一级）</param>
        /// <param name="lines">输出行集合</param>
        private void CollectNeighbor(string dir, string wantedName, string level, List<string> lines)
        {
            if (dir.Length == 0 || !Directory.Exists(dir))
            {
                return;
            }
            List<string> hits = new List<string>();
            try
            {
                string[] solutions = Directory.GetFiles(dir, "*.sln", SearchOption.TopDirectoryOnly);
                string[] projects = Directory.GetFiles(dir, "*.csproj", SearchOption.TopDirectoryOnly);
                for (int i = 0; i < solutions.Length; i = i + 1)
                {
                    hits.Add(solutions[i]);
                }
                for (int i = 0; i < projects.Length; i = i + 1)
                {
                    hits.Add(projects[i]);
                }
            }
            catch (Exception)
            {
                return;
            }
            hits.Sort(StringComparer.OrdinalIgnoreCase);
            int added = 0;
            for (int i = 0; i < hits.Count && added < 3; i = i + 1)
            {
                if (string.Equals(Path.GetFileName(hits[i]), wantedName, StringComparison.OrdinalIgnoreCase))
                {
                    lines.Add(level + "同名: " + DescribePath(hits[i]));
                    added = added + 1;
                }
            }
            for (int i = 0; i < hits.Count && added < 3; i = i + 1)
            {
                if (!string.Equals(Path.GetFileName(hits[i]), wantedName, StringComparison.OrdinalIgnoreCase))
                {
                    lines.Add(level + ": " + DescribePath(hits[i]));
                    added = added + 1;
                }
            }
        }
        /// <summary>
        /// 路径描述——落在受控根内时用「根id:相对路径」形式（与工具寻址同口径），否则原样绝对路径。
        /// 根重叠时取**最具体的根**（路径最长者）——嵌套根（如 gitee ⊃ mau）下给出对使用者更有意义的短形式。
        /// </summary>
        /// <param name="full">绝对路径</param>
        /// <returns>描述串</returns>
        private string DescribePath(string full)
        {
            int best = -1;
            for (int i = 0; i < _roots.Length; i = i + 1)
            {
                if (full.StartsWith(_roots[i] + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase))
                {
                    if (best < 0 || _roots[i].Length > _roots[best].Length)
                    {
                        best = i;
                    }
                }
            }
            if (best >= 0)
            {
                return _rootIds[best] + ":" + full.Substring(_roots[best].Length + 1).Replace(Path.DirectorySeparatorChar, '/');
            }
            return full;
        }
        /// <summary>
        /// LRU 淘汰——池超限时淘汰 LastAccess 最旧条目（跳过当前正在用的 key）
        /// </summary>
        /// <param name="currentKey">当前操作项目键（不淘汰）</param>
        private void EvictIfNeeded(string currentKey)
        {
            lock (_lruGate)
            {
                if (_pool.Count <= PoolLimit)
                {
                    return;
                }
                string? oldestKey = null;
                long oldest = long.MaxValue;
                foreach (KeyValuePair<string, ProjectCache> pair in _pool)
                {
                    if (string.Equals(pair.Key, currentKey, StringComparison.OrdinalIgnoreCase))
                    {
                        continue;
                    }
                    if (pair.Value.LastAccess < oldest)
                    {
                        oldest = pair.Value.LastAccess;
                        oldestKey = pair.Key;
                    }
                }
                if (oldestKey != null)
                {
                    ProjectCache removed;
                    _pool.TryRemove(oldestKey, out removed);
                }
            }
        }

        /// <summary>
        /// 文本截断——超长保留头部 + 截断提示
        /// </summary>
        /// <param name="text">原文</param>
        /// <param name="max">上限</param>
        /// <returns>截断文本</returns>
        private static string TrimResult(string text, int max)
        {
            if (text == null || text.Length == 0)
            {
                return "";
            }
            if (text.Length <= max)
            {
                return text;
            }
            return text.Substring(0, max) + System.Environment.NewLine + "…[截断: 共 " + text.Length.ToString() + " 字符，仅保留前 " + max.ToString() + "]";
        }

        /// <summary>
        /// 相对 csproj 目录路径——诊断输出可读性
        /// </summary>
        private static string RelativeToProject(ProjectCache cache, string filePath)
        {
            if (filePath.StartsWith(cache.ProjectDir + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase))
            {
                return filePath.Substring(cache.ProjectDir.Length + 1);
            }
            return filePath;
        }

        /// <summary>
        /// 从 argsJson 提取字段（缺省空串）
        /// </summary>
        private static string Arg(JsonElement root, string name)
        {
            if (root.ValueKind != JsonValueKind.Object)
            {
                return "";
            }
            JsonElement value;
            if (!root.TryGetProperty(name, out value))
            {
                return "";
            }
            if (value.ValueKind == JsonValueKind.String)
            {
                return value.GetString() ?? "";
            }
            if (value.ValueKind == JsonValueKind.True)
            {
                return "true";
            }
            if (value.ValueKind == JsonValueKind.False)
            {
                return "false";
            }
            return value.ToString();
        }

        /// <summary>
        /// 宿主注入保留参数探测——不进工具声明面（LLM 零感知），参数校验面一律放行。
        /// 来源：宿主 ChatSession.InjectCatId 每次调用向载荷合并 catId（P9.4 猫级路由：文件系统 / 配置群按猫作用域）。
        /// </summary>
        /// <param name="name">参数名</param>
        /// <returns>true=宿主注入字段</returns>
        private static bool IsHostInjectedArg(string name)
        {
            return string.Equals(name, "catId", StringComparison.Ordinal);
        }
        /// <summary>cs-* 参数面校验——声明面口径零容忍：未知参数 / 必填缺值 / 枚举非法值 / code-codes 互斥一律 ERR|BAD_ARGS（校验归工具内部；宿主注入保留键 IsHostInjectedArg 放行）。</summary>
        /// <param name="method">方法名（已过白名单分派）</param>
        /// <param name="args">参数对象</param>
        /// <returns>错误文本（空=通过）</returns>
        private string ValidateToolArgs(string method, JsonElement args)
        {
            string allowed;
            string required;
            if (method == "check")
            {
                allowed = "path full";
                required = "path";
            }
            else if (method == "build")
            {
                allowed = "path";
                required = "path";
            }
            else if (method == "test")
            {
                allowed = "path filter noBuild";
                required = "path";
            }
            else if (method == "list")
            {
                allowed = "path class";
                required = "path";
            }
            else if (method == "read")
            {
                allowed = "path class member";
                required = "path class";
            }
            else if (method == "find_ref")
            {
                allowed = "path class member";
                required = "path class member";
            }
            else if (method == "find")
            {
                allowed = "path name";
                required = "path name";
            }
            else if (method == "patch")
            {
                allowed = "path class method body";
                required = "path class method body";
            }
            else if (method == "member")
            {
                allowed = "path class op member position anchor code codes oldName newName";
                required = "path class op";
            }
            else if (method == "comment")
            {
                allowed = "path class member type text param";
                required = "path class type text";
            }
            else if (method == "dead" || method == "comment_check")
            {
                allowed = "path";
                required = "path";
            }
            else if (method == "format")
            {
                allowed = "path mode";
                required = "path";
            }
            else
            {
                return "ERR|UNKNOWN_METHOD|未知方法: " + method;
            }

            if (args.ValueKind != JsonValueKind.Object)
            {
                return "ERR|BAD_ARGS|参数必须是 JSON 对象";
            }
            foreach (JsonProperty property in args.EnumerateObject())
            {
                if (!IsHostInjectedArg(property.Name) && !ContainsKey(allowed, property.Name))
                {
                    return "ERR|BAD_ARGS|未知参数: " + property.Name + "（支持 " + allowed + "）";
                }
            }
            string[] requiredKeys = required.Split(' ');
            for (int i = 0; i < requiredKeys.Length; i = i + 1)
            {
                if (Arg(args, requiredKeys[i]).Length == 0)
                {
                    return "ERR|BAD_ARGS|缺参数 " + requiredKeys[i] + "（必填：" + required + "）";
                }
            }
            if (method == "check")
            {
                string fullValue = Arg(args, "full");
                if (fullValue.Length > 0 && fullValue != "true" && fullValue != "false")
                {
                    return "ERR|BAD_ARGS|full 非法值: " + fullValue + "（true|false）";
                }
            }
            if (method == "member")
            {
                string op = Arg(args, "op");
                if (op != "insert" && op != "delete" && op != "rename")
                {
                    return "ERR|BAD_ARGS|op 非法值: " + op + "（insert|delete|rename）";
                }
                string position = Arg(args, "position");
                if (position.Length > 0 && position != "end" && position != "before" && position != "after" && position != "after_fields")
                {
                    return "ERR|BAD_ARGS|position 非法值: " + position + "（end|before|after|after_fields）";
                }
                // code 与 codes 互斥——单成员用 code，批量用 codes（A92）
                bool hasCode = false;
                JsonElement codeValue;
                if (args.TryGetProperty("code", out codeValue))
                {
                    hasCode = Arg(args, "code").Length > 0;
                }
                bool hasCodes = false;
                JsonElement codesValue;
                if (args.TryGetProperty("codes", out codesValue))
                {
                    hasCodes = codesValue.ValueKind == JsonValueKind.Array && codesValue.GetArrayLength() > 0;
                }
                if (hasCode && hasCodes)
                {
                    return "ERR|BAD_ARGS|code 与 codes 互斥——单成员用 code，批量用 codes";
                }
            }
            if (method == "comment")
            {
                string typeValue = Arg(args, "type");
                if (typeValue != "summary" && typeValue != "param" && typeValue != "returns")
                {
                    return "ERR|BAD_ARGS|type 非法值: " + typeValue + "（summary|param|returns）";
                }
            }
            if (method == "format")
            {
                string modeValue = Arg(args, "mode");
                if (modeValue.Length > 0 && modeValue != "check" && modeValue != "apply")
                {
                    return "ERR|BAD_ARGS|mode 非法值: " + modeValue + "（check|apply）";
                }
            }
            return "";
        }
        /// <summary>
        /// 键集包含判断——空格分隔键表（声明面口径）。
        /// </summary>
        /// <param name="keyList">空格分隔键表</param>
        /// <param name="name">待查键名</param>
        /// <returns>true=包含</returns>
        private static bool ContainsKey(string keyList, string name)
        {
            string[] keys = keyList.Split(' ');
            for (int i = 0; i < keys.Length; i = i + 1)
            {
                if (keys[i] == name)
                {
                    return true;
                }
            }
            return false;
        }
    }
}