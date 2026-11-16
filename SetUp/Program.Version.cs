using System;
using System.IO;
using System.Text;

namespace SetUp
{
    /// <summary>
    /// SetUp 版本号域——单点读写 + 全量部署自增。
    /// 唯一事实源 = 仓库根 Directory.Build.props 的 Version 标签（A53——CH4 + Mau 全项目跟随；SetUp 自身独立）。
    /// 规范：design-ch4-release.md §六 / §6.1（A130 版本规范 1.yy.xxx——唯一自增点 = 全量部署 prepare）。
    /// </summary>
    /// <summary>
    /// Program 分部——版本域：读 / 写 / 自增三段（消费面 = prepare 自增 · deploy 落盘 version.txt · relaunch 回执）。
    /// </summary>
    public static partial class Program
    {
        /// <summary>
        /// 读取仓库单点版本号——Directory.Build.props 的 Version 标签（A53 唯一事实源：CH4 + Mau 全跟随，
        /// SetUp 独立）；消费路径 = deploy 落盘 version.txt / relaunch 回执 / prepare 自增读数。
        /// </summary>
        /// <param name="repoRoot">仓库根</param>
        /// <returns>版本号（未找到返回 unknown）</returns>
        private static string ReadVersion(string repoRoot)
        {
            string props = Path.Combine(repoRoot, "Directory.Build.props");
            try
            {
                if (!File.Exists(props))
                {
                    return "unknown";
                }
                string text = File.ReadAllText(props);
                string marker = "<Version>";
                int start = text.IndexOf(marker, StringComparison.Ordinal);
                if (start < 0)
                {
                    return "unknown";
                }
                start = start + marker.Length;
                int end = text.IndexOf("</Version>", start, StringComparison.Ordinal);
                if (end < 0)
                {
                    return "unknown";
                }
                return text.Substring(start, end - start).Trim();
            }
            catch (Exception ex)
            {
                Console.WriteLine("[SetUp] 警告：版本读取失败——" + ex.Message);
                return "unknown";
            }
        }

        /// <summary>
        /// 写回仓库单点版本号——只替换 Version 标签内容，其余字节原样落盘（保真：文件无 BOM + CRLF，只改版本串）。
        /// </summary>
        /// <param name="repoRoot">仓库根</param>
        /// <param name="version">新版本号</param>
        /// <returns>成功 true / 失败 false（标签缺失或写入异常——调用方按失败即中止处置）</returns>
        private static bool WriteVersion(string repoRoot, string version)
        {
            string props = Path.Combine(repoRoot, "Directory.Build.props");
            try
            {
                if (!File.Exists(props))
                {
                    Console.WriteLine("[SetUp] 版本写回失败：文件不存在——" + props);
                    return false;
                }
                string text = File.ReadAllText(props);
                string marker = "<Version>";
                int start = text.IndexOf(marker, StringComparison.Ordinal);
                if (start < 0)
                {
                    Console.WriteLine("[SetUp] 版本写回失败：未找到 Version 起始标签。");
                    return false;
                }
                int contentStart = start + marker.Length;
                int end = text.IndexOf("</Version>", contentStart, StringComparison.Ordinal);
                if (end < 0)
                {
                    Console.WriteLine("[SetUp] 版本写回失败：未找到 Version 结束标签。");
                    return false;
                }
                string updated = text.Substring(0, contentStart) + version + text.Substring(end);
                File.WriteAllText(props, updated, new UTF8Encoding(false));
                return true;
            }
            catch (Exception ex)
            {
                Console.WriteLine("[SetUp] 版本写回异常——" + ex.Message);
                return false;
            }
        }

        /// <summary>
        /// 版本自增——全量部署唯一自增点（design-ch4-release.md §6.1）：读单点 → 第三段（xxx）纯递增 +1
        /// （不限位数、不归零；前两段不动）→ 写回，供其后编译产出带新号的产物。
        /// 自增失败（标签缺失 / 非三段式 / 序号非数字 / 写回失败）返回 false——调用方按失败即中止处置：
        /// 版本 ≡ full 代际，不变量优先于部署可用性，不留「同号双代际」。
        /// </summary>
        /// <param name="repoRoot">仓库根</param>
        /// <param name="from">输出：自增前版本号</param>
        /// <param name="to">输出：自增后版本号（失败时为空串）</param>
        /// <returns>成功 true / 失败 false</returns>
        private static bool BumpVersion(string repoRoot, out string from, out string to)
        {
            from = ReadVersion(repoRoot);
            to = "";
            string[] parts = from.Split('.');
            if (parts.Length != 3)
            {
                Console.WriteLine("[SetUp] 版本自增失败：版本号非 x.y.z 三段式——" + from);
                return false;
            }
            if (parts[0].Length == 0 || parts[1].Length == 0)
            {
                Console.WriteLine("[SetUp] 版本自增失败：版本段为空——" + from);
                return false;
            }
            int serial = 0;
            if (!int.TryParse(parts[2], out serial))
            {
                Console.WriteLine("[SetUp] 版本自增失败：递增序号非数字——" + from);
                return false;
            }
            serial = serial + 1;
            // 宽度对齐——原序号带前导零时保持同宽（013 → 014）；自然超出宽度则不截断（999 → 1000）
            string serialText = serial.ToString();
            if (serialText.Length < parts[2].Length)
            {
                serialText = serialText.PadLeft(parts[2].Length, '0');
            }
            to = parts[0] + "." + parts[1] + "." + serialText;
            if (!WriteVersion(repoRoot, to))
            {
                return false;
            }
            return true;
        }

        /// <summary>
        /// 解析待部署版本的落盘口径——**version.txt 的身份 = 产物自述版本**（莎 2026-11-16 定）：
        /// 优先产物区自带的 version.txt；仅当产物区无该文件（刚 publish 完尚未落盘的部署期）才回落仓库单点 Version
        /// ——此时那个版本号就是即将编进产物的版本，与自述语义同源。
        /// <para>
        /// 版本号不承载源码状态：产物是否基于当前源码由构建指纹 buildinfo.txt 的 commit 判定
        /// （SetUp publish-linux 段5f 落盘 · 接收端哨兵 L6 比对）。本函数只回答「这份产物自称哪个版本」。
        /// </para>
        /// </summary>
        /// <param name="repoRoot">仓库根（回落源）</param>
        /// <param name="sourceDir">产物区目录（public/app；空 = 直接回落）</param>
        /// <returns>版本号（产物区优先；都拿不到时回落 ReadVersion 的返回值，可能是 unknown）</returns>
        private static string ResolveDeployVersion(string repoRoot, string sourceDir)
        {
            // [段1] 产物区 version.txt 优先——它就是即将被搬进运行区的那份产物自述的版本
            if (sourceDir.Length > 0)
            {
                string artifactPath = Path.Combine(sourceDir, "version.txt");
                try
                {
                    if (File.Exists(artifactPath))
                    {
                        string text = File.ReadAllText(artifactPath).Trim();
                        if (text.Length > 0)
                        {
                            Console.WriteLine("[SetUp] 版本来源: 产物区 version.txt");
                            return text;
                        }
                    }
                }
                catch (Exception ex)
                {
                    Console.WriteLine("[SetUp] 警告：产物区 version.txt 读取失败——回落仓库版本：" + ex.Message);
                }
            }
            // [段2] 回落——仓库单点 Version（产物区没有版本自述时的唯一可用口径）
            Console.WriteLine("[SetUp] 版本来源: 仓库 Directory.Build.props（产物区无 version.txt）");
            return ReadVersion(repoRoot);
        }
    }
}
