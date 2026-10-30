using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Text;
using System.Text.RegularExpressions;

namespace Mau.Runtime
{
    /// <summary>
    /// 受控文件系统服务——file.* 积木的底层安全边界。
    /// 将文件能力限制在显式根目录集合内，所有操作拒绝穿过现有重解析点，
    /// 避免符号链接绕过字符串前缀检查。
    /// </summary>
    public sealed class FileSystemService
    {
        /// <summary>
        /// 规范化允许根目录
        /// </summary>
        private readonly string[] _roots;

        /// <summary>
        /// 软删除回收目录
        /// </summary>
        private readonly string _recycleRoot;

        /// <summary>
        /// 串行保护变更文件的复合操作
        /// </summary>
        private readonly object _writeGate;

        /// <summary>
        /// 只读根标志——与 _roots 对齐；true 的根拒绝写操作（writable=false 语义）
        /// </summary>
        private readonly bool[] _readOnly;

        /// <summary>
        /// 根标识数组——与 _roots 对齐（命名空间寻址 id:relative——P8.5b）
        /// </summary>
        private readonly string[] _rootIds;

        /// <summary>
        /// 根条目表——A257 根寻址公共服务入参（与 _roots / _rootIds / _readOnly 同源）
        /// </summary>
        private readonly WorkspaceConfig.RootEntry[] _entries;

        /// <summary>
        /// 建立明确根目录和位于其中的回收站
        /// </summary>
        /// <param name="roots">允许根目录</param>
        /// <param name="recycleRoot">回收目录</param>
        public FileSystemService(string[] roots, string recycleRoot)
        {
            if (roots == null || roots.Length == 0)
            {
                throw new ArgumentException("At least one filesystem root is required.", "roots");
            }
            _roots = new string[roots.Length];
            _readOnly = new bool[roots.Length];
            _rootIds = new string[roots.Length];
            _entries = new WorkspaceConfig.RootEntry[roots.Length];
            for (int i = 0; i < roots.Length; i = i + 1)
            {
                if (string.IsNullOrWhiteSpace(roots[i]))
                {
                    throw new ArgumentException("Filesystem root is empty.", "roots");
                }
                _roots[i] = PathBoundary.NormalizeRoot(roots[i]);
                _rootIds[i] = "root" + i.ToString();
                _entries[i] = new WorkspaceConfig.RootEntry() { Id = _rootIds[i], Path = _roots[i], Writable = true };
            }
            _recycleRoot = Resolve(recycleRoot, true);
            Directory.CreateDirectory(_recycleRoot);
            _writeGate = new object();
        }

        /// <summary>
        /// 共享读打开——FileShare.ReadWrite（容许读取正在被其他进程写入的文件：活跃日志 / 热文件）。
        /// File.ReadAllBytes 默认 FileShare.Read——与已存在的写句柄不兼容，读活跃日志必然失败（判例 2026-09-15）。
        /// </summary>
        /// <param name="path">规范绝对路径</param>
        /// <returns>文件全部字节</returns>
        private static byte[] ReadAllBytesShared(string path)
        {
            using (System.IO.FileStream fs = new System.IO.FileStream(path, System.IO.FileMode.Open, System.IO.FileAccess.Read, System.IO.FileShare.ReadWrite))
            {
                using (System.IO.MemoryStream ms = new System.IO.MemoryStream())
                {
                    fs.CopyTo(ms);
                    return ms.ToArray();
                }
            }
        }

        /// <summary>
        /// 共享读文本——StreamReader 保真（BOM 检测 + 编码契约与 File.ReadAllText 一致）
        /// </summary>
        /// <param name="path">规范绝对路径</param>
        /// <param name="encoding">编码</param>
        /// <returns>完整文本</returns>
        private static string ReadTextShared(string path, System.Text.Encoding encoding)
        {
            using (System.IO.FileStream fs = new System.IO.FileStream(path, System.IO.FileMode.Open, System.IO.FileAccess.Read, System.IO.FileShare.ReadWrite))
            {
                using (System.IO.StreamReader sr = new System.IO.StreamReader(fs, encoding, true))
                {
                    return sr.ReadToEnd();
                }
            }
        }

        /// <summary>
        /// 共享读文本行——StreamReader.ReadLine 语义与 File.ReadAllLines 对齐（无尾空行）
        /// </summary>
        /// <param name="path">规范绝对路径</param>
        /// <param name="encoding">编码</param>
        /// <returns>行数组</returns>
        private static string[] ReadAllLinesShared(string path, System.Text.Encoding encoding)
        {
            using (System.IO.FileStream fs = new System.IO.FileStream(path, System.IO.FileMode.Open, System.IO.FileAccess.Read, System.IO.FileShare.ReadWrite))
            {
                using (System.IO.StreamReader sr = new System.IO.StreamReader(fs, encoding, true))
                {
                    List<string> collected = new List<string>();
                    string? line = sr.ReadLine();
                    while (line != null)
                    {
                        collected.Add(line);
                        line = sr.ReadLine();
                    }
                    return collected.ToArray();
                }
            }
        }

        /// <summary>
        /// 读取 UTF-8 文本（共享读——正在写入的活跃文件同样可读）
        /// </summary>
        /// <param name="path">受控路径</param>
        /// <returns>完整文本</returns>
        public string ReadText(string path)
        {
            string resolved = Resolve(path, false);
            return ReadTextShared(resolved, Encoding.UTF8);
        }

        /// <summary>
        /// 原子覆写 UTF-8 无 BOM 文本
        /// </summary>
        /// <param name="path">受控路径</param>
        /// <param name="content">完整正文</param>
        public void WriteText(string path, string content)
        {
            string resolved = Resolve(path, true);
            lock (_writeGate)
            {
                WriteAtomic(resolved, SafeText(content));
            }
        }
        /// <summary>
        /// 自动编码读取——BOM 优先探测，无 BOM 按类型契约（P1 编码内建）
        /// </summary>
        /// <param name="path">受控路径</param>
        /// <returns>完整文本（编码已按契约解析）</returns>
        public string ReadTextAuto(string path)
        {
            string resolved = Resolve(path, false);
            byte[] raw = ReadAllBytesShared(resolved);
            System.Text.Encoding enc = TextFileCodec.DetectReadEncoding(path, raw);
            // 去掉 BOM 头避免首字符 \uFEFF 混入（UTF8Encoding(true) 解码会吞掉 BOM 自身）
            int offset = (enc is UTF8Encoding utf8 && utf8.GetPreamble().Length > 0 && raw.Length >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF) ? 3 : 0;
            return enc.GetString(raw, offset, raw.Length - offset);
        }
        /// <summary>自动编码覆写——既有文件的 BOM/换行保真，新建文件按类型契约（P1/P2；A25）；返回写入风格描述（编码 + BOM / 换行——A148）。</summary>
        /// <param name="path">受控路径</param>
        /// <param name="content">完整正文（换行自动归一为目标风格）</param>
        public string WriteTextAuto(string path, string content) { string resolved = Resolve(path, true); lock (_writeGate) { return WriteAutoCore(resolved, path, SafeText(content)); } }
        /// <summary>自动编码追加——既有文件的 BOM/换行保真，新建文件按类型契约（P1/P2；A25）；返回写入风格描述（编码 + BOM / 换行——A148）。</summary>
        /// <param name="path">受控路径</param>
        /// <param name="content">追加正文（换行自动归一为目标风格）</param>
        public string AppendTextAuto(string path, string content)
        {
            string resolved = Resolve(path, true);
            lock (_writeGate)
            {
                EnsureParentDirectory(resolved);
                bool bom = false;
                string newline = ResolveWriteStyle(resolved, path, out bom);
                string body = TextFileCodec.NormalizeNewlines(SafeText(content), newline);
                File.AppendAllText(resolved, body, TextFileCodec.WriteEncoding(path, bom));
                return TextFileCodec.DescribeStyle(path, bom, newline);
            }
        }
        /// <summary>
        /// 写侧风格解析——既有文件探测实际 BOM + 换行并保真；新建文件按类型契约（P1/P2；A25）。
        /// 单次读取同时产出两者（BOM 保真 + 换行保真共用一份字节）。
        /// </summary>
        /// <param name="resolved">规范绝对路径</param>
        /// <param name="path">用户路径（契约判定用）</param>
        /// <param name="bom">输出——写入是否带 BOM（既有文件=实际值 / 新建=DefaultBom）</param>
        /// <returns>换行串（既有文件=实际风格 / 新建=DefaultNewline）</returns>
        private string ResolveWriteStyle(string resolved, string path, out bool bom)
        {
            if (File.Exists(resolved))
            {
                byte[] raw = ReadAllBytesShared(resolved);
                bom = TextFileCodec.DetectBom(raw);
                // 文件无任何换行 → 按类型默认（契约在无换行时仍生效）
                for (int i = 0; i < raw.Length; i = i + 1)
                {
                    if (raw[i] == (byte)'\n')
                    {
                        return TextFileCodec.DetectNewline(raw);
                    }
                }
                return TextFileCodec.DefaultNewline(path);
            }
            bom = TextFileCodec.DefaultBom(path);
            return TextFileCodec.DefaultNewline(path);
        }

        /// <summary>编码内建写核心——两态风格（既有文件 BOM/换行保真；新建文件按类型契约）+ 原子写；返回写入风格描述（A148）。</summary>
        /// <param name="resolved">规范绝对路径</param>
        /// <param name="path">用户路径（契约判定用）</param>
        /// <param name="content">正文</param>
        private string WriteAutoCore(string resolved, string path, string content)
        {
            bool bom = false;
            string newline = ResolveWriteStyle(resolved, path, out bom);
            System.Text.Encoding enc = TextFileCodec.WriteEncoding(path, bom);
            string body = TextFileCodec.NormalizeNewlines(content, newline);
            // 原子写——编码 + BOM（含 BOM 的 UTF8Encoding 由 WriteAllText 自动前置）
            string? dir = Path.GetDirectoryName(resolved);
            if (dir != null && dir.Length > 0)
            {
                Directory.CreateDirectory(dir);
            }
            string tmp = resolved + ".tmp";
            File.WriteAllText(tmp, body, enc);
            File.Move(tmp, resolved, true);
            return TextFileCodec.DescribeStyle(path, bom, newline);
        }

        /// <summary>
        /// 追加 UTF-8 文本并创建父目录
        /// </summary>
        /// <param name="path">受控路径</param>
        /// <param name="content">追加正文</param>
        public void AppendText(string path, string content)
        {
            string resolved = Resolve(path, true);
            lock (_writeGate)
            {
                EnsureParentDirectory(resolved);
                File.AppendAllText(resolved, SafeText(content), new UTF8Encoding(false));
            }
        }

        /// <summary>
        /// 替换全部精确文本并原子写回
        /// </summary>
        /// <param name="path">受控路径</param>
        /// <param name="oldText">非空目标</param>
        /// <param name="newText">新文本</param>
        /// <returns>替换数量</returns>
        public int ReplaceText(string path, string oldText, string newText)
        {
            if (string.IsNullOrEmpty(oldText))
            {
                throw new ArgumentException("Replacement target is empty.", "oldText");
            }
            string resolved = Resolve(path, true);
            lock (_writeGate)
            {
                string content = ReadTextShared(resolved, Encoding.UTF8);
                int count = CountOccurrences(content, oldText);
                if (count > 0)
                {
                    WriteAtomic(resolved, content.Replace(oldText, SafeText(newText), StringComparison.Ordinal));
                }
                return count;
            }
        }

        /// <summary>
        /// 按一基闭区间读取文本行
        /// </summary>
        /// <param name="path">受控路径</param>
        /// <param name="startLine">起始行</param>
        /// <param name="endLine">结束行；零表示文件尾</param>
        /// <returns>带行号文本</returns>
        public string ReadLines(string path, int startLine, int endLine)
        {
            if (startLine < 1 || endLine < 0)
            {
                throw new ArgumentOutOfRangeException("startLine");
            }
            string[] lines = ReadAllLinesShared(Resolve(path, false), Encoding.UTF8);
            int end = endLine;
            if (end == 0 || end > lines.Length)
            {
                end = lines.Length;
            }
            if (startLine > end && lines.Length > 0)
            {
                throw new ArgumentOutOfRangeException("startLine");
            }
            StringBuilder builder = new StringBuilder();
            for (int line = startLine; line <= end; line = line + 1)
            {
                if (builder.Length > 0)
                {
                    builder.Append('\n');
                }
                builder.Append(line.ToString());
                builder.Append(": ");
                builder.Append(lines[line - 1]);
            }
            return builder.ToString();
        }
        /// <summary>
        /// 自动编码行号区间读取——BOM 优先探测，无 BOM 按类型契约（P5 编码契约补全；规格 design-ch4-text-tools §四）
        /// </summary>
        /// <param name="path">受控路径</param>
        /// <param name="startLine">起始行（1 起）</param>
        /// <param name="endLine">结束行；零表示文件尾</param>
        /// <returns>带行号文本</returns>
        public string ReadLinesAuto(string path, int startLine, int endLine)
        {
            if (startLine < 1 || endLine < 0)
            {
                throw new ArgumentOutOfRangeException("startLine");
            }

            string resolved = Resolve(path, false);
            byte[] raw = ReadAllBytesShared(resolved);
            System.Text.Encoding enc = TextFileCodec.DetectReadEncoding(path, raw);
            int bom = (enc is UTF8Encoding u && u.GetPreamble().Length > 0 && raw.Length >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF) ? 3 : 0;
            string content = enc.GetString(raw, bom, raw.Length - bom);
            string[] lines = content.Replace("\r\n", "\n").Split('\n');
            // [段1] 尾空行修剪——以 \n 结尾时 Split 产生尾空串，与 File.ReadAllLines 语义对齐
            if (lines.Length > 0 && lines[lines.Length - 1].Length == 0)
            {
                string[] trimmed = new string[lines.Length - 1];
                for (int i = 0; i < trimmed.Length; i = i + 1)
                {
                    trimmed[i] = lines[i];
                }

                lines = trimmed;
            }

            // [段2] 区间裁剪——end 零=文件尾，超尾截断
            int end = endLine;
            if (end == 0 || end > lines.Length)
            {
                end = lines.Length;
            }

            if (startLine > end && lines.Length > 0)
            {
                throw new ArgumentOutOfRangeException("startLine");
            }

            // [段3] 行号格式化输出——与 ReadLines 同格式（行号: 内容）
            StringBuilder builder = new StringBuilder();
            for (int line = startLine; line <= end; line = line + 1)
            {
                if (builder.Length > 0)
                {
                    builder.Append('\n');
                }

                builder.Append(line.ToString());
                builder.Append(": ");
                builder.Append(lines[line - 1]);
            }

            return builder.ToString();
        }
        /// <summary>
        /// <summary>忽略目录名单——递归遍历时跳过（.git/bin/obj 等版本控制/编译产物；精确匹配目录名忽略大小写；显式以忽略名为根的查询不受影响——Q4 2026-09-08）</summary>
        private static readonly string[] IgnoredDirNames = new string[]
        {
            ".git", "bin", "obj", "node_modules", ".vs", "dist", "build", "out", ".idea", ".godot"
        };

        /// <summary>
        /// 目录名是否在忽略名单——递归遍历跳过判断（根目录本身不校验）。
        /// </summary>
        /// <param name="name">目录名</param>
        /// <returns>true=应跳过</returns>
        private static bool IsIgnoredDir(string name)
        {
            for (int i = 0; i < IgnoredDirNames.Length; i = i + 1)
            {
                if (string.Equals(name, IgnoredDirNames[i], StringComparison.OrdinalIgnoreCase))
                {
                    return true;
                }
            }
            return false;
        }

        /// <summary>
        /// 收集 .git 特征事实——HEAD 指向 + 本地分支 commit（零外部依赖：直读 HEAD + refs/ 文件；detached HEAD 回退 HEAD 原文）。
        /// 展示文案归积木层（A228 档 2 · B3）。
        /// </summary>
        /// <param name="rel">相对根路径（含 .git）</param>
        /// <param name="gitDir">.git 绝对路径</param>
        /// <returns>git 特征事实（相对路径 + HEAD 摘要）</returns>
        private static FsGitFact CollectGitFact(string rel, string gitDir)
        {
            FsGitFact fact = new FsGitFact();
            fact.RelativePath = rel;
            string head = "";
            try
            {
                head = ReadTextShared(Path.Combine(gitDir, "HEAD"), Encoding.UTF8).Trim();
            }
            catch (Exception ex)
            {
                // HEAD 不可读——按空处理（无特征可报）
                LogStore.Add("FileSystemService", 2, "git HEAD 读取失败，按空处理: " + ex.Message, "SYS");
            }
            if (head.StartsWith("ref:", StringComparison.Ordinal))
            {
                string branch = head.Substring(4).Trim();
                string commit = "";
                string refPath = Path.Combine(gitDir, branch.Replace('/', Path.DirectorySeparatorChar));
                try
                {
                    commit = ReadTextShared(refPath, Encoding.UTF8).Trim();
                }
                catch (Exception ex)
                {
                    // ref 文件缺失（packed-refs 场景）——仅报 HEAD 指向
                    LogStore.Add("FileSystemService", 2, "git ref 读取失败，仅报 HEAD 指向: " + ex.Message, "SYS");
                }
                if (commit.Length > 7)
                {
                    commit = commit.Substring(0, 7);
                }
                fact.Head = branch + (commit.Length > 0 ? " → " + commit : "");
                return fact;
            }
            // detached HEAD——HEAD 即 commit
            if (head.Length > 7)
            {
                head = head.Substring(0, 7);
            }
            fact.Head = head;
            return fact;
        }
        /// <summary>
        /// 移动文件且拒绝覆盖目标
        /// </summary>
        /// <param name="source">源路径</param>
        /// <param name="destination">目标路径</param>
        public void Move(string source, string destination)
        {
            string resolvedSource = Resolve(source, true);
            string resolvedDestination = Resolve(destination, true);
            lock (_writeGate)
            {
                if (File.Exists(resolvedDestination) || Directory.Exists(resolvedDestination))
                {
                    throw new IOException("Move destination already exists.");
                }
                EnsureParentDirectory(resolvedDestination);
                // P1 文件与目录双支持——目录整棵移动（含非空子目录）；自动建父目录（EnsureParentDirectory）
                if (Directory.Exists(resolvedSource))
                {
                    Directory.Move(resolvedSource, resolvedDestination);
                    return;
                }
                if (File.Exists(resolvedSource))
                {
                    File.Move(resolvedSource, resolvedDestination, false);
                    return;
                }
                throw new FileNotFoundException("Move source was not found.", resolvedSource);
            }
        }

        /// <summary>
        /// 复制文件或目录（含非空目录——整棵子树）到目标；自动创建目标父目录；目标已存在拒绝
        /// </summary>
        /// <param name="source">源路径</param>
        /// <param name="destination">目标路径</param>
        public void Copy(string source, string destination)
        {
            string resolvedSource = Resolve(source, false);
            string resolvedDestination = Resolve(destination, true);
            lock (_writeGate)
            {
                if (File.Exists(resolvedDestination) || Directory.Exists(resolvedDestination))
                {
                    throw new IOException("Copy destination already exists.");
                }
                if (Directory.Exists(resolvedSource))
                {
                    CopyDirectory(resolvedSource, resolvedDestination);
                    return;
                }
                if (File.Exists(resolvedSource))
                {
                    EnsureParentDirectory(resolvedDestination);
                    File.Copy(resolvedSource, resolvedDestination, false);
                    return;
                }
                throw new FileNotFoundException("Copy source was not found.", resolvedSource);
            }
        }

        /// <summary>
        /// 递归复制目录子树——重解析点（符号链接）跳过（拒绝穿过重解析点，与类边界语义一致）
        /// </summary>
        /// <param name="source">源目录（已校验）</param>
        /// <param name="destination">目标目录（已校验）</param>
        private static void CopyDirectory(string source, string destination)
        {
            Directory.CreateDirectory(destination);
            foreach (string file in Directory.EnumerateFiles(source))
            {
                if (PathBoundary.IsReparsePoint(file))
                {
                    continue;
                }
                File.Copy(file, Path.Combine(destination, Path.GetFileName(file)), false);
            }
            foreach (string sub in Directory.EnumerateDirectories(source))
            {
                if (PathBoundary.IsReparsePoint(sub))
                {
                    continue;
                }
                CopyDirectory(sub, Path.Combine(destination, Path.GetFileName(sub)));
            }
        }
        /// <summary>
        /// 将文件或目录（含非空目录——整棵子树）移动到受控回收站，并统计被移动内容
        /// </summary>
        /// <param name="path">目标路径</param>
        /// <returns>回收结果（落点 + 文件数 / 子目录数 / 字节数）</returns>
        public RecycleOutcome Recycle(string path)
        {
            string resolved = Resolve(path, true);
            string trimmed = resolved.TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar);
            for (int i = 0; i < _roots.Length; i = i + 1)
            {
                string root = _roots[i].TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar);
                if (string.Equals(trimmed, root, StringComparison.OrdinalIgnoreCase))
                {
                    // 受控根本身不可回收——软删可恢复，但根被移走等于运行面整体失踪
                    throw new InvalidOperationException("Refusing to recycle a controlled root: " + resolved);
                }
            }
            lock (_writeGate)
            {
                string leaf = Path.GetFileName(trimmed);
                if (leaf.Length == 0)
                {
                    leaf = "root";
                }
                RecycleOutcome outcome = new RecycleOutcome();
                outcome.Target = Path.Combine(_recycleRoot,
                    DateTime.UtcNow.ToString("yyyyMMddTHHmmssfffffffZ") + "-"
                    + Guid.NewGuid().ToString("N") + "-" + leaf);
                if (File.Exists(resolved))
                {
                    outcome.FileCount = 1;
                    outcome.TotalBytes = new FileInfo(resolved).Length;
                    File.Move(resolved, outcome.Target, false);
                    return outcome;
                }
                if (Directory.Exists(resolved))
                {
                    outcome.IsDirectory = true;
                    CollectRecycleStats(resolved, outcome);
                    Directory.Move(resolved, outcome.Target);
                    return outcome;
                }
                throw new FileNotFoundException("Recycle target was not found.", resolved);
            }
        }

        /// <summary>
        /// 回收统计采集——迁移前递归统计文件数 / 子目录数 / 字节数
        /// </summary>
        /// <param name="directory">目录</param>
        /// <param name="outcome">统计载体</param>
        private static void CollectRecycleStats(string directory, RecycleOutcome outcome)
        {
            foreach (string file in Directory.EnumerateFiles(directory))
            {
                outcome.FileCount = outcome.FileCount + 1;
                outcome.TotalBytes = outcome.TotalBytes + new FileInfo(file).Length;
            }
            foreach (string sub in Directory.EnumerateDirectories(directory))
            {
                outcome.DirectoryCount = outcome.DirectoryCount + 1;
                CollectRecycleStats(sub, outcome);
            }
        }

        /// <summary>
        /// 解析路径并验证归属和重解析点边界
        /// </summary>
        /// <param name="path">用户路径</param>
        /// <param name="forWrite">是否为写入</param>
        /// <returns>规范绝对路径</returns>
        public string Resolve(string path, bool forWrite)
        {
            if (string.IsNullOrWhiteSpace(path))
            {
                // v3 机制纯净——空路径是调用方错误（参数校验，不做产品级回落；LLM 适配归自举层语料）
                throw new ArgumentException("Path is empty.", "path");
            }
            // A257 根寻址统一解析（公共服务·点二：展开 + 归属判定）——越界 / 未知 id / 非法路径在此转异常，保本类既有契约
            RootResolveResult rr = RootPathResolver.ResolveOwned(path, _entries);
            if (!rr.Ok)
            {
                throw new UnauthorizedAccessException("Path resolve failed: " + rr.ErrorCode + " | " + rr.Message);
            }
            // P8.5 只读根校验——forWrite 且归属根只读 → 拒绝（安全层归本类；公共服务不插手）
            if (forWrite && IsReadOnlyRoot(rr.RootPath))
            {
                throw new UnauthorizedAccessException("Path is in a read-only root: " + rr.RootPath);
            }
            // 重解析点边界证明（安全层归本类；公共服务不插手）
            return PathBoundary.ResolveOwnedPath(rr.RootPath, rr.FullPath);
        }

        /// <summary>
        /// 显式遍历搜索目录，并拒绝进入或返回重解析点。
        /// </summary>
        /// <param name="root">搜索相对根</param>
        /// <param name="current">当前目录</param>
        /// <param name="pattern">文件名模式</param>
        /// <param name="recursive">是否递归</param>
        /// <param name="limit">收集上限（output 至多 limit 条）</param>
        /// <param name="output">绝对文件路径（前 limit 条）</param>
        /// <param name="ignoredDirs">忽略目录计数（累加）</param>
        /// <param name="matched">匹配总数（累加——遍历不因 limit 提前退出，供总量报告）</param>
        private void AppendFind(string root, string current, string pattern,
            bool recursive, int limit, List<string> output, ref int ignoredDirs, ref int matched)
        {
            // [段0] **/ 目录通配前缀——剥前缀 + 强制递归（text-find 的 **/*.txt 语义；连续段剥净；剥空回退 *）
            string dirWild = "**/";
            while (pattern.StartsWith(dirWild, StringComparison.Ordinal))
            {
                pattern = pattern.Substring(dirWild.Length);
                recursive = true;
            }
            if (pattern.Length == 0)
            {
                pattern = "*";
            }
            // [段1] 当前目录文件按稳定顺序加入——匹配即计数；前 limit 条入 output（不提前退出：总量可见）
            string[] files = Directory.GetFiles(current, pattern, SearchOption.TopDirectoryOnly);
            Array.Sort(files, StringComparer.OrdinalIgnoreCase);
            for (int i = 0; i < files.Length; i = i + 1)
            {
                if (PathBoundary.IsReparsePoint(files[i]))
                {
                    continue;
                }
                matched = matched + 1;
                if (output.Count < limit)
                {
                    output.Add(files[i]);
                }
            }
            if (!recursive)
            {
                return;
            }
            // [段2] 逐目录验证后递归，避免 AllDirectories 隐式穿过链接
            string[] directories = Directory.GetDirectories(current, "*", SearchOption.TopDirectoryOnly);
            Array.Sort(directories, StringComparer.OrdinalIgnoreCase);
            for (int i = 0; i < directories.Length; i = i + 1)
            {
                if (PathBoundary.IsReparsePoint(directories[i]))
                {
                    continue;
                }
                // Q4 忽略目录——跳过（.git/bin/obj 等；计数提示——不静默）
                if (IsIgnoredDir(Path.GetFileName(directories[i])))
                {
                    ignoredDirs = ignoredDirs + 1;
                    continue;
                }
                PathBoundary.ResolveOwnedPath(root, directories[i]);
                AppendFind(root, directories[i], pattern, true, limit, output, ref ignoredDirs, ref matched);
            }
        }

        /// <summary>
        /// 统计非重叠精确子串数量
        /// </summary>
        /// <param name="content">正文</param>
        /// <param name="target">目标</param>
        /// <returns>数量</returns>
        private int CountOccurrences(string content, string target)
        {
            int count = 0;
            int index = 0;
            while (index <= content.Length - target.Length)
            {
                int found = content.IndexOf(target, index, StringComparison.Ordinal);
                if (found < 0)
                {
                    return count;
                }
                count = count + 1;
                index = found + target.Length;
            }
            return count;
        }

        /// <summary>
        /// 同目录临时文件原子替换
        /// </summary>
        /// <param name="path">目标路径</param>
        /// <param name="content">正文</param>
        private void WriteAtomic(string path, string content)
        {
            EnsureParentDirectory(path);
            // 原子写——统一实现 ConfigStore.AtomicWrite（审查修复轮 2026-08-11 决策3）
            ConfigStore.AtomicWrite(path, content);
        }
        /// <summary>
        /// 创建目标父目录
        /// </summary>
        /// <param name="path">文件路径</param>
        private void EnsureParentDirectory(string path)
        {
            string? directory = Path.GetDirectoryName(path);
            if (directory == null)
            {
                throw new InvalidOperationException("Parent directory is unavailable.");
            }
            Directory.CreateDirectory(directory);
        }

        /// <summary>
        /// 把可空文本规范为空字符串
        /// </summary>
        /// <param name="value">输入</param>
        /// <returns>非空文本</returns>
        private static string SafeText(string? value)
        {
            if (value == null)
            {
                return "";
            }
            return value;
        }

        /// <summary>
        /// 建立受控根条目版构造——含只读标志（writable=false 根拒绝写操作；design-ch4-workspace §三）
        /// </summary>
        /// <param name="rootEntries">受控根条目（id + 路径 + 可写标志）</param>
        /// <param name="recycleRoot">回收目录</param>
        public FileSystemService(WorkspaceConfig.RootEntry[] rootEntries, string recycleRoot)
        {
            if (rootEntries == null || rootEntries.Length == 0)
            {
                throw new ArgumentException("At least one filesystem root is required.", "rootEntries");
            }
            _roots = new string[rootEntries.Length];
            _readOnly = new bool[rootEntries.Length];
            _rootIds = new string[rootEntries.Length];
            _entries = new WorkspaceConfig.RootEntry[rootEntries.Length];
            for (int i = 0; i < rootEntries.Length; i = i + 1)
            {
                WorkspaceConfig.RootEntry entry = rootEntries[i];
                if (entry == null || string.IsNullOrWhiteSpace(entry.Path))
                {
                    throw new ArgumentException("Filesystem root is empty.", "rootEntries");
                }
                _roots[i] = PathBoundary.NormalizeRoot(entry.Path);
                _readOnly[i] = !entry.Writable;
                string entryId = entry.Id;
                if (entryId == null || entryId.Length == 0)
                {
                    entryId = "root" + i.ToString();
                }
                _rootIds[i] = entryId;
                _entries[i] = new WorkspaceConfig.RootEntry() { Id = _rootIds[i], Path = _roots[i], Writable = entry.Writable };
            }
            _recycleRoot = Resolve(recycleRoot, true);
            Directory.CreateDirectory(_recycleRoot);
            _writeGate = new object();
        }

        /// <summary>
        /// 根清单——id → 规范绝对路径（只读快照；PsCat 根寻址注入等消费面用）。
        /// </summary>
        /// <returns>根 id 到规范路径的映射（键大小写不敏感）</returns>
        public Dictionary<string, string> DescribeRoots()
        {
            Dictionary<string, string> map = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
            for (int i = 0; i < _roots.Length; i = i + 1)
            {
                map[_rootIds[i]] = _roots[i];
            }
            return map;
        }

        /// <summary>
        /// 查询根是否只读——按归属根路径匹配（Resolve forWrite 校验用）
        /// </summary>
        /// <param name="rootPath">归属根路径</param>
        /// <returns>true=只读（拒绝写操作）</returns>
        private bool IsReadOnlyRoot(string rootPath)
        {
            for (int i = 0; i < _roots.Length; i = i + 1)
            {
                if (string.Equals(_roots[i], rootPath, StringComparison.OrdinalIgnoreCase))
                {
                    return _readOnly[i];
                }
            }
            return false;
        }
        /// <summary>
        /// 目录树事实（结构化——A228 档 2 · B3：条目 / git 特征 / 计数；展示文案归积木层）。
        /// </summary>
        /// <param name="path">受控目录</param>
        /// <param name="depth">输出层数（1–10；1 = 只列直属子项）</param>
        /// <param name="limit">条目上限（1..10000）——只影响 Entries 条数；Total 仍按全量计数</param>
        /// <returns>目录树事实</returns>
        public FsTreeFacts TreeFacts(string path, int depth, int limit)
        {
            if (depth < 1 || depth > 10 || limit < 1 || limit > 10000)
            {
                throw new ArgumentOutOfRangeException("depth");
            }
            string root = Resolve(path, false);
            if (!Directory.Exists(root))
            {
                throw new DirectoryNotFoundException("Tree root was not found.");
            }
            List<FsEntry> entries = new List<FsEntry>();
            List<FsGitFact> gitFacts = new List<FsGitFact>();
            int ignoredDirs = 0;
            int total = 0;
            // [段1] 广度优先逐层推进——同层条目全部出完再进下一层；遍历不因 limit 提前退出（超出部分只计数）
            List<string> currentLayer = new List<string>();
            currentLayer.Add(root);
            int remainLayer = depth;
            while (currentLayer.Count > 0)
            {
                List<string> nextLayer = new List<string>();
                for (int d = 0; d < currentLayer.Count; d = d + 1)
                {
                    string[] layerItems = Directory.GetFileSystemEntries(currentLayer[d]);
                    Array.Sort(layerItems, StringComparer.OrdinalIgnoreCase);
                    for (int i = 0; i < layerItems.Length; i = i + 1)
                    {
                        string rel = Path.GetRelativePath(root, layerItems[i]);
                        bool isDir = Directory.Exists(layerItems[i]);
                        // Q4 忽略目录——.git 记特征事实（不列出），其余忽略名单目录跳过（子项不展开）；计数不静默
                        if (isDir && IsIgnoredDir(Path.GetFileName(layerItems[i])))
                        {
                            if (string.Equals(Path.GetFileName(layerItems[i]), ".git", StringComparison.OrdinalIgnoreCase))
                            {
                                gitFacts.Add(CollectGitFact(rel, layerItems[i]));
                            }
                            ignoredDirs = ignoredDirs + 1;
                            continue;
                        }
                        total = total + 1;
                        if (total <= limit)
                        {
                            FsEntry entry = new FsEntry();
                            entry.RelativePath = rel;
                            entry.IsDir = isDir;
                            entries.Add(entry);
                        }
                        if (remainLayer > 1 && isDir && !PathBoundary.IsReparsePoint(layerItems[i]))
                        {
                            nextLayer.Add(layerItems[i]);
                        }
                    }
                }
                if (remainLayer <= 1)
                {
                    break;
                }
                remainLayer = remainLayer - 1;
                currentLayer = nextLayer;
            }
            FsTreeFacts facts = new FsTreeFacts();
            facts.Entries = entries.ToArray();
            facts.GitFacts = gitFacts.ToArray();
            facts.IgnoredDirs = ignoredDirs;
            facts.Total = total;
            return facts;
        }
        /// <summary>
        /// PE 版本事实（结构化——A228 档 2 · B3：版本三件 + 元数据；展示文案与回落注记归积木层）。
        /// </summary>
        /// <param name="path">文件路径（受控根寻址——只读语义）</param>
        /// <returns>版本事实</returns>
        public FsVersionFacts VersionFacts(string path)
        {
            string resolved = Resolve(path, false);
            if (!File.Exists(resolved))
            {
                throw new FileNotFoundException("File was not found.", resolved);
            }
            // [段1] PE 判定——首两字节 "MZ"（非 PE 直接报错，不让空版本伪装成正常）
            bool isPe = false;
            FileStream probe = new FileStream(resolved, FileMode.Open, FileAccess.Read, FileShare.ReadWrite);
            try
            {
                int b0 = probe.ReadByte();
                int b1 = probe.ReadByte();
                isPe = b0 == 'M' && b1 == 'Z';
            }
            finally
            {
                probe.Dispose();
            }
            if (!isPe)
            {
                throw new InvalidDataException("Not a PE file (no version resource).");
            }
            // [段2] 版本三件——ProductVersion 承载 InformationalVersion（.NET SDK 注入 $(Version)+编译时刻——A53）
            FileVersionInfo info = FileVersionInfo.GetVersionInfo(resolved);
            string product = SafeText(info.ProductVersion);
            string file = SafeText(info.FileVersion);
            // [段3] 空值 / 全零占位归一——0.0.0.0 是 csc 直编产物的占位形态，不算版本事实（CR-1 第二向：不把零值当正常版本）
            if (IsZeroVersionText(product))
            {
                product = "";
            }
            if (IsZeroVersionText(file))
            {
                file = "";
            }
            // [段4] Win32 回退——.NET 对 UE4 monolithic Shipping exe 整体取空（CR-1 第一向：假阴性），改按 RT_VERSION 内 Translation 表逐语言/代码页取
            string fixedVersion = "";
            if (product.Length == 0 && file.Length == 0)
            {
                bool hasVersionResource = ReadVersionFactsWin32(resolved, out product, out file, out fixedVersion);
                if (file.Length == 0 && fixedVersion.Length > 0)
                {
                    file = fixedVersion;
                }
                if (product.Length == 0 && file.Length == 0)
                {
                    if (hasVersionResource)
                    {
                        throw new InvalidDataException("PE file has an RT_VERSION resource but no readable version facts (ProductVersion/FileVersion empty or zero placeholder, VS_FIXEDFILEINFO all-zero).");
                    }
                    throw new InvalidDataException("PE file has no version resource (no RT_VERSION entry).");
                }
            }
            FsVersionFacts facts = new FsVersionFacts();
            facts.ResolvedPath = resolved;
            facts.ProductVersion = product;
            facts.FileVersion = file;
            if (product.Length > 0)
            {
                facts.Version = product;
            }
            else
            {
                facts.Version = file;
                facts.FallbackUsed = true;
            }
            // [段5] 元数据——修改时间与大小（同一次读取的自然产物）
            FileInfo fi = new FileInfo(resolved);
            facts.LastWrite = fi.LastWriteTime;
            facts.SizeBytes = fi.Length;
            return facts;
        }
        /// <summary>
        /// 文件名搜索事实（结构化——A228 档 2 · B3：相对路径 + 计数；展示文案归积木层）。
        /// </summary>
        /// <param name="directory">受控目录</param>
        /// <param name="pattern">文件名模式</param>
        /// <param name="recursive">是否递归</param>
        /// <param name="limit">结果上限（收集上限）</param>
        /// <returns>搜索结果事实</returns>
        public FsFindFacts FindFacts(string directory, string pattern, bool recursive, int limit)
        {
            if (string.IsNullOrWhiteSpace(pattern) || limit < 1 || limit > 10000)
            {
                throw new ArgumentException("Find parameters are invalid.", "pattern");
            }
            string root = Resolve(directory, false);
            List<string> files = new List<string>();
            int ignoredDirs = 0;
            int matched = 0;
            AppendFind(root, root, pattern, recursive, limit, files, ref ignoredDirs, ref matched);
            files.Sort(StringComparer.OrdinalIgnoreCase);
            string[] rels = new string[files.Count];
            for (int i = 0; i < files.Count; i = i + 1)
            {
                rels[i] = Path.GetRelativePath(root, files[i]);
            }
            FsFindFacts facts = new FsFindFacts();
            facts.RelativePaths = rels;
            facts.IgnoredDirs = ignoredDirs;
            facts.Matched = matched;
            return facts;
        }
        /// <summary>
        /// 内容搜索事实（结构化——A228 档 2 · B3：命中 / 跳过 / 计数；展示文案归积木层）。
        /// </summary>
        /// <param name="directory">受控目录</param>
        /// <param name="keyword">关键词（大小写敏感）</param>
        /// <param name="pattern">文件名过滤（默认 *）</param>
        /// <param name="limit">命中上限（1..10000）——只影响 Hits 条数；HitTotal 仍按全量计数</param>
        /// <returns>内容搜索事实</returns>
        public FsGrepFacts GrepFacts(string directory, string keyword, string pattern, int limit)
        {
            if (string.IsNullOrWhiteSpace(keyword) || limit < 1 || limit > 10000)
            {
                throw new ArgumentException("Grep parameters are invalid.", "keyword");
            }
            string root = Resolve(directory, false);
            string filter = (string.IsNullOrWhiteSpace(pattern) || pattern == "*") ? "*" : pattern;
            List<string> files = new List<string>();
            int ignoredDirs = 0;
            // 文件收集上限 10000——收集超出部分只计数（与 Tree / Find 同口径）
            int collectMatched = 0;
            AppendFind(root, root, filter, true, 10000, files, ref ignoredDirs, ref collectMatched);
            List<FsGrepHit> hits = new List<FsGrepHit>();
            List<FsGrepSkip> skipped = new List<FsGrepSkip>();
            int hitTotal = 0;
            // 命中上限 limit——超出部分只计数（不提前退出：截断提示需报总条数）
            for (int i = 0; i < files.Count; i = i + 1)
            {
                string file = files[i];
                if (PathBoundary.IsReparsePoint(file))
                {
                    continue;
                }
                string relative = Path.GetRelativePath(root, file);
                try
                {
                    byte[] raw = ReadAllBytesShared(file);
                    System.Text.Encoding enc = TextFileCodec.DetectReadEncoding(file, raw);
                    int bom = (enc is UTF8Encoding u && u.GetPreamble().Length > 0 && raw.Length >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF) ? 3 : 0;
                    string[] lines = enc.GetString(raw, bom, raw.Length - bom).Replace("\r\n", "\n").Split('\n');
                    for (int n = 0; n < lines.Length; n = n + 1)
                    {
                        string line = lines[n];
                        int idx = line.IndexOf(keyword, StringComparison.Ordinal);
                        if (idx >= 0)
                        {
                            hitTotal = hitTotal + 1;
                            if (hits.Count < limit)
                            {
                                int start = Math.Max(0, idx - 10);
                                int len = Math.Min(10 + keyword.Length + 10, line.Length - start);
                                FsGrepHit hit = new FsGrepHit();
                                hit.RelativePath = relative;
                                hit.Line = n + 1;
                                hit.Context = line.Substring(start, len);
                                hits.Add(hit);
                            }
                        }
                    }
                }
                catch (Exception ex)
                {
                    // 不可读文件（二进制/权限/被独占）跳过——不静默：具名告警随结果返回（失败可见）
                    FsGrepSkip skip = new FsGrepSkip();
                    skip.RelativePath = relative;
                    skip.Reason = ex.GetType().Name;
                    skipped.Add(skip);
                }
            }
            FsGrepFacts facts = new FsGrepFacts();
            facts.Hits = hits.ToArray();
            facts.Skipped = skipped.ToArray();
            facts.IgnoredDirs = ignoredDirs;
            facts.HitTotal = hitTotal;
            return facts;
        }
        /// <summary>
        /// 取版本资源块字节数（只认 RT_VERSION 类型；0 = 无版本资源块）
        /// </summary>
        [System.Runtime.InteropServices.DllImport("version.dll", CharSet = System.Runtime.InteropServices.CharSet.Unicode, SetLastError = true)]
        private static extern int GetFileVersionInfoSizeW(string fileName, out int handle);
        /// <summary>
        /// 取版本资源块内容
        /// </summary>
        [System.Runtime.InteropServices.DllImport("version.dll", CharSet = System.Runtime.InteropServices.CharSet.Unicode, SetLastError = true)]
        private static extern bool GetFileVersionInfoW(string fileName, int handle, int size, byte[] block);
        /// <summary>
        /// 版本块内取值（字符串键 / Translation 表 / VS_FIXEDFILEINFO）
        /// </summary>
        [System.Runtime.InteropServices.DllImport("version.dll", CharSet = System.Runtime.InteropServices.CharSet.Unicode, SetLastError = true)]
        private static extern bool VerQueryValueW(byte[] block, string subBlock, out IntPtr buffer, out int length);
        /// <summary>
        /// 只读映射模块（LOAD_LIBRARY_AS_DATAFILE）——资源类型遍历用，不执行映像
        /// </summary>
        [System.Runtime.InteropServices.DllImport("kernel32.dll", CharSet = System.Runtime.InteropServices.CharSet.Unicode, SetLastError = true)]
        private static extern IntPtr LoadLibraryExW(string fileName, IntPtr file, uint flags);
        /// <summary>
        /// 卸载模块映射
        /// </summary>
        [System.Runtime.InteropServices.DllImport("kernel32.dll", SetLastError = true)]
        private static extern bool FreeLibrary(IntPtr module);
        /// <summary>
        /// 按资源类型枚举资源名（本类只用 RT_VERSION = 16）
        /// </summary>
        [System.Runtime.InteropServices.DllImport("kernel32.dll", CharSet = System.Runtime.InteropServices.CharSet.Unicode, SetLastError = true)]
        private static extern bool EnumResourceNamesW(IntPtr module, IntPtr type, EnumResNameProcW proc, IntPtr param);
        /// <summary>
        /// 资源枚举回调签名（EnumResourceNamesW）
        /// </summary>
        private delegate bool EnumResNameProcW(IntPtr module, IntPtr type, IntPtr name, IntPtr param);
        /// <summary>
        /// RT_VERSION 枚举回调——计数器 +1 后继续枚举
        /// </summary>
        private static bool CountVersionEntry(IntPtr module, IntPtr type, IntPtr name, IntPtr counter)
        {
            System.Runtime.InteropServices.Marshal.WriteInt32(counter, System.Runtime.InteropServices.Marshal.ReadInt32(counter) + 1);
            return true;
        }
        /// <summary>
        /// 按 RT_VERSION（资源类型 16）遍历模块资源条目并计数——仅用于错误文案区分「无版本资源」与「有资源但读不出」。
        /// </summary>
        /// <param name="path">已解析绝对路径</param>
        /// <returns>RT_VERSION 资源条目数（0 = 无版本资源）</returns>
        private static int CountVersionResourceEntries(string path)
        {
            IntPtr module = LoadLibraryExW(path, IntPtr.Zero, 0x00000002);
            if (module == IntPtr.Zero)
            {
                return 0;
            }
            IntPtr counter = System.Runtime.InteropServices.Marshal.AllocHGlobal(4);
            try
            {
                System.Runtime.InteropServices.Marshal.WriteInt32(counter, 0);
                EnumResourceNamesW(module, new IntPtr(16), CountVersionEntry, counter);
                return System.Runtime.InteropServices.Marshal.ReadInt32(counter);
            }
            finally
            {
                System.Runtime.InteropServices.Marshal.FreeHGlobal(counter);
                FreeLibrary(module);
            }
        }
        /// <summary>
        /// Win32 版本事实读取（CR-1 回退通道）——按 RT_VERSION 资源内 VarFileInfo\\Translation 表枚举全部语言/代码页组合
        /// （不依赖固定 0409/04b0，这是假阴性的根因），字符串键缺失时回落 VS_FIXEDFILEINFO 数值版本；全零占位一律归空。
        /// </summary>
        /// <param name="path">已解析绝对路径</param>
        /// <param name="product">ProductVersion（取不到为空串）</param>
        /// <param name="file">FileVersion（取不到为空串）</param>
        /// <param name="fixedVersion">VS_FIXEDFILEINFO 数值版本 a.b.c.d（全零 / 缺失为空串）</param>
        /// <returns>文件是否含 RT_VERSION 资源条目（错误文案据此区分「无资源」与「有资源但读不出」）</returns>
        private static bool ReadVersionFactsWin32(string path, out string product, out string file, out string fixedVersion)
        {
            product = "";
            file = "";
            fixedVersion = "";
            int handle;
            int size = GetFileVersionInfoSizeW(path, out handle);
            if (size <= 0)
            {
                return CountVersionResourceEntries(path) > 0;
            }
            byte[] block = new byte[size];
            if (!GetFileVersionInfoW(path, 0, size, block))
            {
                return true;
            }
            IntPtr table;
            int tableBytes;
            if (VerQueryValueW(block, "\\VarFileInfo\\Translation", out table, out tableBytes) && table != IntPtr.Zero && tableBytes >= 4)
            {
                int groups = tableBytes / 4;
                for (int i = 0; i < groups; i++)
                {
                    byte[] pair = new byte[4];
                    System.Runtime.InteropServices.Marshal.Copy(new IntPtr(table.ToInt64() + (i * 4)), pair, 0, 4);
                    int lang = pair[0] | (pair[1] << 8);
                    int codePage = pair[2] | (pair[3] << 8);
                    string prefix = "\\StringFileInfo\\" + lang.ToString("x4") + codePage.ToString("x4") + "\\";
                    if (product.Length == 0)
                    {
                        product = ReadBlockString(block, prefix + "ProductVersion");
                    }
                    if (file.Length == 0)
                    {
                        file = ReadBlockString(block, prefix + "FileVersion");
                    }
                    if (product.Length > 0 && file.Length > 0)
                    {
                        break;
                    }
                }
            }
            fixedVersion = ReadBlockFixedVersion(block);
            if (IsZeroVersionText(product))
            {
                product = "";
            }
            if (IsZeroVersionText(file))
            {
                file = "";
            }
            return true;
        }
        /// <summary>
        /// 读版本资源块内的单个字符串值（UTF-16——去掉尾零；键不存在为空串）
        /// </summary>
        /// <param name="block">版本资源块</param>
        /// <param name="subBlock">子块路径（\\StringFileInfo\\langcp\\键名）</param>
        /// <returns>字符串值（取不到为空串）</returns>
        private static string ReadBlockString(byte[] block, string subBlock)
        {
            IntPtr buffer;
            int length;
            if (!VerQueryValueW(block, subBlock, out buffer, out length) || buffer == IntPtr.Zero || length <= 0)
            {
                return "";
            }
            string value = System.Runtime.InteropServices.Marshal.PtrToStringUni(buffer) ?? "";
            return value.TrimEnd('\0');
        }
        /// <summary>
        /// 读 VS_FIXEDFILEINFO 的数值版本 a.b.c.d——全零 / 结构缺失返回空串
        /// </summary>
        /// <param name="block">版本资源块</param>
        /// <returns>数值版本（全零为空串）</returns>
        private static string ReadBlockFixedVersion(byte[] block)
        {
            IntPtr buffer;
            int length;
            if (!VerQueryValueW(block, "\\", out buffer, out length) || buffer == IntPtr.Zero || length < 52)
            {
                return "";
            }
            uint fileVersionMS = (uint)System.Runtime.InteropServices.Marshal.ReadInt32(buffer, 8);
            uint fileVersionLS = (uint)System.Runtime.InteropServices.Marshal.ReadInt32(buffer, 12);
            if (fileVersionMS == 0 && fileVersionLS == 0)
            {
                return "";
            }
            return ((fileVersionMS >> 16) & 0xFFFF).ToString() + "." + (fileVersionMS & 0xFFFF).ToString() + "." + ((fileVersionLS >> 16) & 0xFFFF).ToString() + "." + (fileVersionLS & 0xFFFF).ToString();
        }
        /// <summary>
        /// 判定「全零占位版本」（空串 / 纯空白 / 0 / 0.0 / 0.0.0.0）——csc 直编产物的占位形态，不算版本事实
        /// （CR-1 第二向：不把 0.0.0.0 当正常回显）。
        /// </summary>
        /// <param name="text">版本文本</param>
        /// <returns>true = 空或全零占位</returns>
        private static bool IsZeroVersionText(string text)
        {
            if (text.Length == 0)
            {
                return true;
            }
            for (int i = 0; i < text.Length; i++)
            {
                char c = text[i];
                if (c == '.' || c == ' ' || c == '\t')
                {
                    continue;
                }
                if (c != '0')
                {
                    return false;
                }
            }
            return true;
        }
    }

    /// <summary>
    /// 路径边界证明——根归属 + 重解析点防护
    /// </summary>
    internal static class PathBoundary
    {
        /// <summary>
        /// 规范并创建受控根，同时拒绝根本身为重解析点。
        /// </summary>
        /// <param name="root">受控根路径</param>
        /// <returns>不带多余末尾分隔符的绝对路径</returns>
        internal static string NormalizeRoot(string root)
        {
            if (string.IsNullOrWhiteSpace(root))
            {
                throw new ArgumentException("Controlled root is empty.", "root");
            }
            string normalized = TrimEndingSeparator(Path.GetFullPath(root));
            Directory.CreateDirectory(normalized);
            if (IsReparsePoint(normalized))
            {
                throw new UnauthorizedAccessException("Controlled root cannot be a reparse point.");
            }
            return normalized;
        }

        /// <summary>
        /// 证明目标仍在受控根内且现存路径没有穿过重解析点。
        /// </summary>
        /// <param name="root">已规范的受控根</param>
        /// <param name="path">目标绝对或相对路径</param>
        /// <returns>规范目标绝对路径</returns>
        internal static string ResolveOwnedPath(string root, string path)
        {
            string normalized = Path.GetFullPath(path);
            string prefix = root + Path.DirectorySeparatorChar.ToString();
            if (!string.Equals(normalized, root, StringComparison.OrdinalIgnoreCase)
                && !normalized.StartsWith(prefix, StringComparison.OrdinalIgnoreCase))
            {
                throw new UnauthorizedAccessException("Controlled path escaped its owned root.");
            }
            RejectExistingReparsePoints(root, normalized);
            return normalized;
        }

        /// <summary>
        /// 判断现存文件或目录是否为重解析点。
        /// </summary>
        /// <param name="path">现存路径</param>
        /// <returns>是否为重解析点</returns>
        internal static bool IsReparsePoint(string path)
        {
            return (File.GetAttributes(path) & FileAttributes.ReparsePoint) == FileAttributes.ReparsePoint;
        }

        /// <summary>
        /// 拒绝从受控根到目标的任一现存重解析点。
        /// </summary>
        /// <param name="root">受控根</param>
        /// <param name="path">根内目标</param>
        private static void RejectExistingReparsePoints(string root, string path)
        {
            if (IsReparsePoint(root))
            {
                throw new UnauthorizedAccessException("Controlled root cannot be a reparse point.");
            }
            string relative = Path.GetRelativePath(root, path);
            string[] segments = relative.Split(new char[]
            {
                Path.DirectorySeparatorChar,
                Path.AltDirectorySeparatorChar
            }, StringSplitOptions.RemoveEmptyEntries);
            string current = root;
            for (int i = 0; i < segments.Length; i = i + 1)
            {
                current = Path.Combine(current, segments[i]);
                if ((File.Exists(current) || Directory.Exists(current)) && IsReparsePoint(current))
                {
                    throw new UnauthorizedAccessException("Controlled path crosses a reparse point.");
                }
            }
        }

        /// <summary>
        /// 移除普通目录末尾分隔符但保留卷根。
        /// </summary>
        /// <param name="path">绝对目录</param>
        /// <returns>规范目录</returns>
        private static string TrimEndingSeparator(string path)
        {
            string? volumeRoot = Path.GetPathRoot(path);
            if (volumeRoot != null && string.Equals(path, volumeRoot, StringComparison.OrdinalIgnoreCase))
            {
                return path;
            }
            return path.TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar);
        }
    }
}

