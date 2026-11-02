using System;
using System.Collections.Generic;
using System.IO;
using System.Reflection;
using System.Security.Cryptography;
using System.Text;

namespace SetUp
{
    /// <summary>
    /// Program 分部——自举代际自检：exe 内嵌源码快照与磁盘 SetUp/ 源码内容比对（A264 立、A270 换判据）。
    /// 判据 = 内容（逐文件 SHA256），不依赖写盘时刻——git 拉取同批落盘让 mtime 先后无保证（同秒误报），
    /// 复制保时刻时又会漏报，时间判据两个方向都失真（A270 实测：exe 21:31:02.888 / 源码 21:31:02.911，
    /// 内容一致却报落后）。
    /// 规范：design-ch4-release.md §九（SetUp/ 源码与 SetUp.exe 同 commit）。
    /// </summary>
    public static partial class Program
    {
        /// <summary>内嵌源码快照的逻辑名标记（csproj EmbeddedResource 的 LogicalName 约定）。</summary>
        private const string SourceSnapshotMarker = "src/";

        /// <summary>差异清单每类最多列出的文件数（超出以「…」收尾）。</summary>
        private const int DeltaListLimit = 5;

        /// <summary>
        /// 自举代际自检——exe 内嵌快照与磁盘源码逐文件比对，不一致即返回告警文本（逐文件点名）。
        /// 不阻断（旧 exe 仍能自举），但必须出声——判例：段5b 资产复制随 1.8.154 进源码，
        /// 根目录 SetUp.exe 仍是旧二进制，提示音整段缺失而报告全绿。
        /// </summary>
        /// <param name="repoRoot">仓库根</param>
        /// <returns>告警文本（空串 = exe 内嵌源码与磁盘一致）</returns>
        private static string CheckSelfGeneration(string repoRoot)
        {
            try
            {
                // [段1] 源码目录——仓库内无 SetUp/（纯运行实例）即不适用，静默跳过
                string srcDir = Path.Combine(repoRoot, "SetUp");
                if (!Directory.Exists(srcDir))
                {
                    return "";
                }
                // [段2] exe 侧——内嵌源码快照（相对路径 → 内容哈希）；快照缺失即出声，不静默放行
                Dictionary<string, string> built = ReadEmbeddedSourceSnapshot();
                if (built.Count == 0)
                {
                    return "SetUp.exe 未内嵌源码快照（构建期 EmbeddedResource 缺失）——代际自检不可用，请重发 exe（design-ch4-release §九）";
                }
                // [段3] 磁盘侧——SetUp/ 下的 .cs，排除 bin / obj 构建产物
                Dictionary<string, string> disk = ReadDiskSourceSnapshot(srcDir);
                // [段4] 逐文件比对——按新增 / 变更 / 删除三类收集，输出可点名
                List<string> added = new List<string>();
                List<string> changed = new List<string>();
                List<string> removed = new List<string>();
                foreach (KeyValuePair<string, string> pair in disk)
                {
                    string builtHash = null;
                    if (!built.TryGetValue(pair.Key, out builtHash))
                    {
                        added.Add(pair.Key);
                        continue;
                    }
                    if (!string.Equals(builtHash, pair.Value, StringComparison.Ordinal))
                    {
                        changed.Add(pair.Key);
                    }
                }
                foreach (string key in built.Keys)
                {
                    if (!disk.ContainsKey(key))
                    {
                        removed.Add(key);
                    }
                }
                added.Sort(StringComparer.Ordinal);
                changed.Sort(StringComparer.Ordinal);
                removed.Sort(StringComparer.Ordinal);
                return BuildDeltaText(added, changed, removed);
            }
            catch (Exception ex)
            {
                Console.WriteLine("[SetUp] 警告：自举代际自检异常——" + ex.Message);
                return "";
            }
        }

        /// <summary>
        /// 读取 exe 内嵌的源码快照——资源名按 LogicalName 约定取标记之后的相对路径，内容算 SHA256。
        /// </summary>
        /// <returns>相对路径 → 内容哈希（无快照时为空表）</returns>
        private static Dictionary<string, string> ReadEmbeddedSourceSnapshot()
        {
            Dictionary<string, string> map = new Dictionary<string, string>(StringComparer.Ordinal);
            Assembly assembly = typeof(Program).Assembly;
            string[] names = assembly.GetManifestResourceNames();
            for (int i = 0; i < names.Length; i = i + 1)
            {
                string relative = RelativeFromSnapshotName(names[i]);
                if (relative.Length == 0)
                {
                    continue;
                }
                using (Stream stream = assembly.GetManifestResourceStream(names[i]))
                {
                    if (stream == null)
                    {
                        continue;
                    }
                    map[relative] = HashStream(stream);
                }
            }
            return map;
        }

        /// <summary>
        /// 从资源名提取快照相对路径——统一正斜杠，取标记（src/）之后部分；非快照资源返回空串。
        /// </summary>
        /// <param name="resourceName">程序集资源名</param>
        /// <returns>相对路径（如 Program.cs）或空串</returns>
        private static string RelativeFromSnapshotName(string resourceName)
        {
            if (resourceName == null || resourceName.Length == 0)
            {
                return "";
            }
            string normalized = resourceName.Replace('\\', '/');
            int index = normalized.IndexOf(SourceSnapshotMarker, StringComparison.Ordinal);
            if (index < 0)
            {
                return "";
            }
            return normalized.Substring(index + SourceSnapshotMarker.Length);
        }

        /// <summary>
        /// 扫描磁盘源码——SetUp/ 下全部 .cs（递归），排除 bin / obj 构建产物，内容算 SHA256。
        /// </summary>
        /// <param name="srcDir">SetUp 源码目录</param>
        /// <returns>相对路径 → 内容哈希</returns>
        private static Dictionary<string, string> ReadDiskSourceSnapshot(string srcDir)
        {
            Dictionary<string, string> map = new Dictionary<string, string>(StringComparer.Ordinal);
            string[] files = Directory.GetFiles(srcDir, "*.cs", SearchOption.AllDirectories);
            for (int i = 0; i < files.Length; i = i + 1)
            {
                string relative = Path.GetRelativePath(srcDir, files[i]).Replace('\\', '/');
                if (IsBuildArtifactPath(relative))
                {
                    continue;
                }
                using (FileStream stream = File.OpenRead(files[i]))
                {
                    map[relative] = HashStream(stream);
                }
            }
            return map;
        }

        /// <summary>
        /// 判定相对路径是否落在构建产物目录（bin / obj）内——与 csproj 的 Exclude 面同构。
        /// </summary>
        /// <param name="relative">相对路径（正斜杠）</param>
        /// <returns>true = 构建产物</returns>
        private static bool IsBuildArtifactPath(string relative)
        {
            if (relative.StartsWith("bin/", StringComparison.OrdinalIgnoreCase))
            {
                return true;
            }
            if (relative.StartsWith("obj/", StringComparison.OrdinalIgnoreCase))
            {
                return true;
            }
            if (relative.IndexOf("/bin/", StringComparison.OrdinalIgnoreCase) >= 0)
            {
                return true;
            }
            if (relative.IndexOf("/obj/", StringComparison.OrdinalIgnoreCase) >= 0)
            {
                return true;
            }
            return false;
        }

        /// <summary>
        /// 计算流内容的 SHA256（小写十六进制）。
        /// </summary>
        /// <param name="stream">可读流</param>
        /// <returns>哈希文本</returns>
        private static string HashStream(Stream stream)
        {
            using (SHA256 sha = SHA256.Create())
            {
                byte[] hash = sha.ComputeHash(stream);
                return Convert.ToHexString(hash).ToLowerInvariant();
            }
        }

        /// <summary>
        /// 组装差异告警文本——按新增 / 变更 / 删除三类分节，逐文件点名（每类上限 DeltaListLimit）。
        /// </summary>
        /// <param name="added">磁盘有、exe 无的文件</param>
        /// <param name="changed">两侧都有但内容不同的文件</param>
        /// <param name="removed">exe 有、磁盘无的文件</param>
        /// <returns>告警文本（无差异时为空串）</returns>
        private static string BuildDeltaText(List<string> added, List<string> changed, List<string> removed)
        {
            StringBuilder builder = new StringBuilder();
            AppendDeltaSection(builder, "新增", added);
            AppendDeltaSection(builder, "变更", changed);
            AppendDeltaSection(builder, "删除", removed);
            if (builder.Length == 0)
            {
                return "";
            }
            return "SetUp.exe 与 SetUp/ 源码内容不一致（exe 内嵌快照 ≠ 磁盘）——" + builder.ToString() + "——请重发 exe（design-ch4-release §九）";
        }

        /// <summary>
        /// 追加单个差异分节——格式「标签 N（文件 / 文件 / …）」。
        /// </summary>
        /// <param name="builder">输出缓冲</param>
        /// <param name="label">分节标签</param>
        /// <param name="names">该类的文件清单（已排序）</param>
        private static void AppendDeltaSection(StringBuilder builder, string label, List<string> names)
        {
            if (names.Count == 0)
            {
                return;
            }
            if (builder.Length > 0)
            {
                builder.Append(" · ");
            }
            builder.Append(label);
            builder.Append(" ");
            builder.Append(names.Count);
            builder.Append("（");
            int limit = names.Count;
            if (limit > DeltaListLimit)
            {
                limit = DeltaListLimit;
            }
            for (int i = 0; i < limit; i = i + 1)
            {
                if (i > 0)
                {
                    builder.Append(" / ");
                }
                builder.Append(names[i]);
            }
            if (names.Count > limit)
            {
                builder.Append(" / …");
            }
            builder.Append("）");
        }
    }
}
