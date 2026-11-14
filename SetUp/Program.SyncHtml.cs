using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Security.Cryptography;
using System.Text;
using CatHome4.Platform;

namespace SetUp
{
    /// <summary>
    /// Program 分部——sync-html 模式：外观层静态资源镜像同步（源区 → 产物区 + 运行区）。
    /// 规范：design-ch4-release.md §二（第三模式）。
    /// 定位：html 是纯静态资源（无编译、无程序集锁）——前端改动不走全链部署，一条命令同步到消费点，刷新即生效。
    /// 🔴 判据三条：无条件覆盖（源为准，绕开时间戳判据）· 镜像语义（目标多余文件删除）· SHA256 逐文件核验。
    /// </summary>
    public static partial class Program
    {
        /// <summary>外观层目录名——源区 / 产物区 / 运行区共用</summary>
        private const string HtmlDirName = "html";

        /// <summary>镜像排除路径段——测试依赖目录（源区无、运行区有；两侧一律跳过：不复制、不删除、不核验）</summary>
        private const string HtmlExcludeSegment = "node_modules";

        /// <summary>
        /// sync-html 模式入口。
        /// 参数：--target &lt;运行区目录&gt;（可选，缺省从运行中 CatHome4 进程反推）· --no-run（只同步产物区）· --report（Main 解析）。
        /// </summary>
        /// <param name="repoRoot">仓库根（Main 已探测）</param>
        /// <param name="args">命令行参数</param>
        /// <returns>退出码（0=同步与核验全部通过；1=失败）</returns>
        private static int SyncHtml(string repoRoot, string[] args)
        {
            Console.WriteLine("[SetUp] sync-html —— 外观层静态资源同步（源区 → 产物区 + 运行区）");
            string badArgs = ValidateSyncHtmlArgs(args);
            if (badArgs.Length > 0)
            {
                Console.WriteLine("[SetUp] " + badArgs);
                PrintHelp();
                return 1;
            }

            // [段1] 源区校验——权威面必须存在
            string sourceDir = Path.Combine(repoRoot, "CatHome4", HtmlDirName);
            if (!Directory.Exists(sourceDir))
            {
                return Fail("源区目录不存在：" + sourceDir);
            }
            Console.WriteLine("  源区: " + sourceDir);

            // [段2] 产物区同步（仓库内，必做——deploy 的来源）
            string artifactDir = Path.Combine(repoRoot, "public", "app", HtmlDirName);
            if (!SyncOne(sourceDir, artifactDir, "产物区", 1))
            {
                return Fail("产物区同步失败：" + artifactDir);
            }

            // [段3] 运行区同步（消费点）——--no-run 时跳过
            if (HasFlag(args, "--no-run"))
            {
                Console.WriteLine("[SetUp] --no-run：跳过运行区同步。");
                _steps.Add(new StepReport() { Step = 2, Name = "运行区同步（--no-run 跳过）", Ok = true, Ms = 0, Detail = "跳过" });
                return 0;
            }
            string runHow = "";
            string runDir = ResolveRunDir(ExtractOption(args, "--target"), out runHow);
            if (runDir.Length == 0)
            {
                return Fail("未能定位运行区——请用 --target <运行区目录> 显式指定，或用 --no-run 只同步产物区。");
            }
            Console.WriteLine("  运行区: " + runDir + "（" + runHow + "）");
            string runHtml = Path.Combine(runDir, HtmlDirName);
            if (!SyncOne(sourceDir, runHtml, "运行区", 2))
            {
                return Fail("运行区同步失败：" + runHtml);
            }
            Console.WriteLine("[SetUp] sync-html 完成——产物区与运行区均已对齐源区。");
            return 0;
        }

        /// <summary>
        /// 运行区定位——显式 --target 优先；缺省从运行中 CatHome4 进程反推（主模块所在目录 = 运行区）。
        /// </summary>
        /// <param name="targetOpt">--target 参数值（空=未给）</param>
        /// <param name="how">定位方式说明（out，报告与日志用）</param>
        /// <returns>运行区绝对路径；空串=未能定位</returns>
        private static string ResolveRunDir(string targetOpt, out string how)
        {
            how = "";
            if (targetOpt.Length > 0)
            {
                how = "--target 显式指定";
                return Path.GetFullPath(targetOpt);
            }
            Process[] procs;
            try
            {
                procs = Process.GetProcessesByName("CatHome4");
            }
            catch (Exception ex)
            {
                Console.WriteLine("[SetUp] 警告：进程枚举失败——" + ex.Message);
                return "";
            }
            for (int i = 0; i < procs.Length; i = i + 1)
            {
                try
                {
                    string module = procs[i].MainModule.FileName;
                    string dir = Path.GetDirectoryName(module);
                    if (dir != null && dir.Length > 0 && File.Exists(Path.Combine(dir, PlatformServices.Paths.HostExeName)))
                    {
                        how = "运行中实例反推（pid " + procs[i].Id.ToString() + "）";
                        return dir;
                    }
                }
                catch (Exception ex)
                {
                    Console.WriteLine("[SetUp] 警告：进程 " + procs[i].Id.ToString() + " 模块路径不可读——" + ex.Message);
                }
            }
            return "";
        }

        /// <summary>
        /// 单目标镜像同步——覆盖复制（源为准，不判时间戳）+ 删除目标多余文件 + SHA256 逐文件核验。
        /// </summary>
        /// <param name="sourceDir">源目录（权威）</param>
        /// <param name="targetDir">目标目录（镜像）</param>
        /// <param name="label">报告标签（产物区 / 运行区）</param>
        /// <param name="stepNo">报告步号（sync-html：产物区=1 / 运行区=2；deploy 前置对齐=21）</param>
        /// <returns>true=同步且核验通过</returns>
        private static bool SyncOne(string sourceDir, string targetDir, string label, int stepNo)
        {
            long ms = Environment.TickCount64;
            List<string> sourceFiles = ListHtmlFiles(sourceDir);
            int copied = 0;
            int deleted = 0;
            try
            {
                // [段1] 目标目录就绪 + 无条件覆盖复制（File.Copy 保留源时间戳——顺带修复 mtime 倒挂）
                if (!Directory.Exists(targetDir))
                {
                    Directory.CreateDirectory(targetDir);
                }
                for (int i = 0; i < sourceFiles.Count; i = i + 1)
                {
                    string rel = sourceFiles[i];
                    string dst = Path.Combine(targetDir, rel);
                    string dstParent = Path.GetDirectoryName(dst);
                    if (dstParent != null && dstParent.Length > 0 && !Directory.Exists(dstParent))
                    {
                        Directory.CreateDirectory(dstParent);
                    }
                    File.Copy(Path.Combine(sourceDir, rel), dst, true);
                    copied = copied + 1;
                }

                // [段2] 镜像语义——目标多余文件删除（源已删的不在目标残留）
                HashSet<string> sourceSet = new HashSet<string>(sourceFiles, StringComparer.OrdinalIgnoreCase);
                List<string> targetFiles = ListHtmlFiles(targetDir);
                for (int i = 0; i < targetFiles.Count; i = i + 1)
                {
                    string rel = targetFiles[i];
                    if (!sourceSet.Contains(rel))
                    {
                        File.Delete(Path.Combine(targetDir, rel));
                        deleted = deleted + 1;
                    }
                }

                // [段3] 核验——SHA256 逐文件比对（源 vs 目标）；不一致即出声（不静默通过）
                string mismatch = VerifyMirror(sourceDir, targetDir, sourceFiles);
                if (mismatch.Length > 0)
                {
                    _steps.Add(new StepReport() { Step = stepNo, Name = "sync-html " + label + " 核验", Ok = false, Ms = Environment.TickCount64 - ms, Detail = mismatch });
                    Console.WriteLine("[SetUp] ❌ " + label + " 核验不一致——" + mismatch);
                    return false;
                }
                long bytes = CountBytes(sourceDir, sourceFiles);
                _steps.Add(new StepReport()
                {
                    Step = stepNo,
                    Name = "sync-html " + label + "（复制 " + copied.ToString() + " / 删除 " + deleted.ToString() + " / 核验 " + sourceFiles.Count.ToString() + " 件）",
                    Ok = true,
                    Ms = Environment.TickCount64 - ms,
                    Detail = targetDir + " · " + bytes.ToString() + " 字节"
                });
                Console.WriteLine("[SetUp] ✅ " + label + " 同步完成——复制 " + copied.ToString() + " 件，删除多余 " + deleted.ToString() + " 件，核验 " + sourceFiles.Count.ToString() + " 件一致（" + bytes.ToString() + " 字节）");
                return true;
            }
            catch (Exception ex)
            {
                _steps.Add(new StepReport() { Step = stepNo, Name = "sync-html " + label, Ok = false, Ms = Environment.TickCount64 - ms, Detail = ex.Message });
                Console.WriteLine("[SetUp] ❌ " + label + " 同步异常——" + ex.Message);
                return false;
            }
        }

        /// <summary>
        /// 枚举目录内全部文件（相对路径，排序）——排除 node_modules 路径段。
        /// </summary>
        /// <param name="root">根目录</param>
        /// <returns>相对路径列表（空=目录不存在）</returns>
        private static List<string> ListHtmlFiles(string root)
        {
            List<string> list = new List<string>();
            if (!Directory.Exists(root))
            {
                return list;
            }
            string[] files = Directory.GetFiles(root, "*", SearchOption.AllDirectories);
            for (int i = 0; i < files.Length; i = i + 1)
            {
                string rel = Path.GetRelativePath(root, files[i]);
                if (IsExcludedRel(rel))
                {
                    continue;
                }
                list.Add(rel);
            }
            list.Sort(StringComparer.OrdinalIgnoreCase);
            return list;
        }

        /// <summary>
        /// 排除判据——相对路径含排除段（node_modules）即跳过。
        /// </summary>
        /// <param name="rel">相对路径</param>
        /// <returns>true=排除</returns>
        private static bool IsExcludedRel(string rel)
        {
            string[] parts = rel.Split(new char[] { Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar });
            for (int i = 0; i < parts.Length; i = i + 1)
            {
                if (string.Equals(parts[i], HtmlExcludeSegment, StringComparison.OrdinalIgnoreCase))
                {
                    return true;
                }
            }
            return false;
        }

        /// <summary>
        /// 镜像核验——逐文件比对存在性 / 大小 / SHA256；返回首个不一致描述（空=全一致）。
        /// </summary>
        /// <param name="sourceDir">源目录</param>
        /// <param name="targetDir">目标目录</param>
        /// <param name="sourceFiles">源文件相对路径清单</param>
        /// <returns>不一致描述（空=通过）</returns>
        private static string VerifyMirror(string sourceDir, string targetDir, List<string> sourceFiles)
        {
            for (int i = 0; i < sourceFiles.Count; i = i + 1)
            {
                string rel = sourceFiles[i];
                string src = Path.Combine(sourceDir, rel);
                string dst = Path.Combine(targetDir, rel);
                if (!File.Exists(dst))
                {
                    return "目标缺文件 " + rel;
                }
                FileInfo srcInfo = new FileInfo(src);
                FileInfo dstInfo = new FileInfo(dst);
                if (srcInfo.Length != dstInfo.Length)
                {
                    return "大小不符 " + rel + "（源 " + srcInfo.Length.ToString() + " / 目标 " + dstInfo.Length.ToString() + "）";
                }
                string srcHash = Sha256Hex(src);
                string dstHash = Sha256Hex(dst);
                if (!string.Equals(srcHash, dstHash, StringComparison.OrdinalIgnoreCase))
                {
                    return "哈希不符 " + rel + "（源 " + srcHash.Substring(0, 8) + "… / 目标 " + dstHash.Substring(0, 8) + "…）";
                }
            }
            return "";
        }

        /// <summary>
        /// 统计文件总字节数——报告用（同步规模自证）。
        /// </summary>
        /// <param name="root">根目录</param>
        /// <param name="files">相对路径清单</param>
        /// <returns>总字节数</returns>
        private static long CountBytes(string root, List<string> files)
        {
            long total = 0;
            for (int i = 0; i < files.Count; i = i + 1)
            {
                total = total + new FileInfo(Path.Combine(root, files[i])).Length;
            }
            return total;
        }

        /// <summary>
        /// 文件 SHA256 十六进制小写——核验用。
        /// </summary>
        /// <param name="path">文件路径</param>
        /// <returns>64 位十六进制串</returns>
        private static string Sha256Hex(string path)
        {
            byte[] hash;
            using (FileStream stream = File.OpenRead(path))
            {
                using (SHA256 sha = SHA256.Create())
                {
                    hash = sha.ComputeHash(stream);
                }
            }
            StringBuilder sb = new StringBuilder();
            for (int i = 0; i < hash.Length; i = i + 1)
            {
                sb.Append(hash[i].ToString("x2"));
            }
            return sb.ToString();
        }

        /// <summary>
        /// 标志参数判据——出现即为真。
        /// </summary>
        /// <param name="args">命令行参数</param>
        /// <param name="name">标志名</param>
        /// <returns>true=存在</returns>
        private static bool HasFlag(string[] args, string name)
        {
            for (int i = 0; i < args.Length; i = i + 1)
            {
                if (args[i] == name)
                {
                    return true;
                }
            }
            return false;
        }

        /// <summary>
        /// sync-html 参数校验——声明面口径零容忍：未知参数 / 选项缺值一律报错（不静默忽略）。
        /// </summary>
        /// <param name="args">命令行参数</param>
        /// <returns>错误文本（空=通过）</returns>
        private static string ValidateSyncHtmlArgs(string[] args)
        {
            string[] allowed = new string[] { "--target", "--no-run", "--report" };
            for (int i = 1; i < args.Length; i = i + 1)
            {
                string arg = args[i];
                if (arg.Length == 0 || arg[0] != '-')
                {
                    continue;
                }
                if (arg == "--no-run")
                {
                    continue;
                }
                bool known = false;
                for (int k = 0; k < allowed.Length; k = k + 1)
                {
                    if (arg == allowed[k])
                    {
                        known = true;
                    }
                }
                if (!known)
                {
                    return "参数错误：未知参数 " + arg + "（支持 --target / --no-run / --report）。";
                }
                if (i + 1 >= args.Length)
                {
                    return "参数错误：" + arg + " 缺值。";
                }
                i = i + 1;
            }
            return "";
        }
    }
}
