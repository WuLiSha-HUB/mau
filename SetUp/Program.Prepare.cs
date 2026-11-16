using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using CatHome4.Platform;

namespace SetUp
{
    /// <summary>
    /// SetUp prepare 模式——重建全发布链（= README 6 步序自动化 + 收尾构建服务器清理）。
    /// 目标：配置好 public/ + Mau-public/（就地自举）+ 零隐式后台进程残留。
    /// 规范：design-ch4-release.md §二 / design-ch4-deploy.md §三 / README §3.2
    /// </summary>
    /// <summary>
    /// Program 分部——prepare 模式：就地自举链（build/test/publish/组翻译/publish/宿主自检）。
    /// </summary>
    public static partial class Program
    {
        /// <summary>
        /// prepare 模式入口——按序执行 6 步（依赖序不可跳），末尾无论成败清理构建服务器。
        /// 清理铁律：dotnet build/test/publish 会驻留隐式 MSBuild/VBCSCompiler 后台进程（无窗口、删目录被锁）——
        /// 不允许前台不可见的后台驻留，prepare 结束前统一 shutdown（try/finally 保证失败路径也清理）。
        /// </summary>
        /// <param name="repoRoot">仓库根</param>
        /// <returns>退出码（0=成功，非0=失败）</returns>
        private static int Prepare(string repoRoot)
        {
            Console.WriteLine("[SetUp] prepare —— 重建全发布链");
            Console.WriteLine("  " + repoRoot);

            try
            {
                // [段0a] 自举代际自检（A264）——运行中的 SetUp 落后于 SetUp/ 源码即出声
                // 不阻断：旧 exe 仍能自举；但必须可见（design-ch4-release §九：SetUp/ 源码与 SetUp 同 commit）
                long selfCheckMs = Environment.TickCount64;
                string selfDetail = CheckSelfGeneration(repoRoot);
                bool selfGenOk = selfDetail.Length == 0;
                _steps.Add(new StepReport() { Step = 0, Name = "段0a SetUp 自举代际自检", Ok = selfGenOk, Ms = Environment.TickCount64 - selfCheckMs, Detail = selfDetail });
                if (!selfGenOk)
                {
                    Console.WriteLine("[SetUp] ⚠ 自举代际告警：" + selfDetail);
                }

                // [段0] 版本自增——全量部署唯一自增点（design-ch4-release.md §6.1）：读单点 → 递增序号 +1 → 写回后编译
                // 版本 ≡ full 代际——自增失败即中止（不变量优先），不留「同号双代际」；incr / host 重启链不经过本段
                long verMs = Environment.TickCount64;
                string verFrom = "";
                string verTo = "";
                bool verOk = BumpVersion(repoRoot, out verFrom, out verTo);
                _steps.Add(new StepReport() { Step = 0, Name = "版本自增 " + verFrom + " -> " + verTo, Ok = verOk, Ms = Environment.TickCount64 - verMs });
                if (!verOk)
                {
                    return Fail("步0 版本自增失败——Directory.Build.props 的 Version 缺失或非 1.yy.xxx 三段式，中止。");
                }
                Console.WriteLine("[SetUp] 版本: " + verFrom + " → " + verTo);

                // [段1] 步1：dotnet build Mau.sln（构建 + NuGet 还原——0 错误 0 警告底线）
                long stepMs = Environment.TickCount64;
                bool stepOk = RunProcess("dotnet", "build Mau.sln", repoRoot);
                _steps.Add(new StepReport() { Step = 1, Name = "dotnet build Mau.sln", Ok = stepOk, Ms = Environment.TickCount64 - stepMs });
                if (!stepOk)
                {
                    return Fail("步1 dotnet build Mau.sln 失败——构建未通过，中止。");
                }

                // [段2] 步2：全量测试——Mau 三测试项目 + CatHome4 宿主两测试项目（A58：宿主测试并入链，步号不变）
                stepMs = Environment.TickCount64;
                stepOk = RunProcess("dotnet", "test Mau.sln", repoRoot);
                _steps.Add(new StepReport() { Step = 2, Name = "dotnet test Mau.sln", Ok = stepOk, Ms = Environment.TickCount64 - stepMs });
                if (!stepOk)
                {
                    return Fail("步2 dotnet test Mau.sln 失败——测试未通过，中止。");
                }
                stepMs = Environment.TickCount64;
                stepOk = RunProcess("dotnet", "test CatHome4.sln", repoRoot);
                _steps.Add(new StepReport() { Step = 2, Name = "dotnet test CatHome4.sln", Ok = stepOk, Ms = Environment.TickCount64 - stepMs });
                if (!stepOk)
                {
                    return Fail("步2 dotnet test CatHome4.sln 失败——宿主测试未通过，中止。");
                }

                // [段3] 步3：dotnet publish Mau\Mau.Cli -c Debug -o Mau-public（基座部署区——FL 组引用源，缺失则步4报 M3245）
                stepMs = Environment.TickCount64;
                stepOk = RunProcess("dotnet", "publish " + Path.Combine("Mau", "Mau.Cli", "Mau.Cli.csproj") + " -c Debug -o Mau-public", repoRoot);
                _steps.Add(new StepReport() { Step = 3, Name = "publish Mau.Cli -> Mau-public", Ok = stepOk, Ms = Environment.TickCount64 - stepMs });
                if (!stepOk)
                {
                    return Fail("步3 publish Mau.Cli 失败——基座部署区未生成，中止。");
                }

                // [段4] 步4：mau proj 全组扫描（工作目录=仓库根——黄金对比/积木索引依赖相对路径；经 Mau-public\Mau.exe 调用）
                // 扫描化：遍历 corpus/ch4/ 目录，有 .mauproj 即构建（design-ch4-flow-scan §3.1——新增 Flow 零 SetUp 改动）
                string mauExe = Path.Combine(repoRoot, "Mau-public", PlatformServices.Paths.MauExeName);
                if (!File.Exists(mauExe))
                {
                    return Fail("步4 前置缺失：" + mauExe + " 不存在——基座 CLI 未生成。");
                }
                string corpusDir = Path.Combine(repoRoot, "corpus", "ch4");
                if (!Directory.Exists(corpusDir))
                {
                    return Fail("步4 前置缺失：corpus/ch4 目录不存在——语料堆未就位。");
                }
                // 产物目录先清空——组翻译与 publish 都只覆盖不清理，跨版本残留（退役组 dll / 旧出口程序集）会混装进运行区
                string[] purgeTargets = new string[]
                {
                    Path.Combine(repoRoot, "public", "app"),
                    Path.Combine(repoRoot, "public", "src")
                };
                for (int p = 0; p < purgeTargets.Length; p = p + 1)
                {
                    if (!Directory.Exists(purgeTargets[p]))
                    {
                        continue;
                    }
                    try
                    {
                        Directory.Delete(purgeTargets[p], true);
                        Console.WriteLine("[SetUp] 步4 前置清理：" + purgeTargets[p]);
                    }
                    catch (Exception ex)
                    {
                        return Fail("步4 前置清理失败：" + purgeTargets[p] + " —— " + ex.Message);
                    }
                }
                string[] groupDirs = Directory.GetDirectories(corpusDir);
                Array.Sort(groupDirs, StringComparer.OrdinalIgnoreCase);
                List<string[]> groupBuilds = new List<string[]>();
                for (int d = 0; d < groupDirs.Length; d = d + 1)
                {
                    string[] mauprojs = Directory.GetFiles(groupDirs[d], "*.mauproj");
                    if (mauprojs.Length == 0)
                    {
                        Console.WriteLine("[SetUp] 步4 跳过（目录无 .mauproj）：" + Path.GetFileName(groupDirs[d]));
                        continue;
                    }
                    string groupName = Path.GetFileName(groupDirs[d]);
                    string projRel = Path.Combine("corpus", "ch4", groupName, Path.GetFileName(mauprojs[0]));
                    groupBuilds.Add(new string[] { projRel, groupName });
                }
                if (groupBuilds.Count == 0)
                {
                    return Fail("步4 无任何可构建组——corpus\\ch4 下未发现 .mauproj。");
                }
                for (int i = 0; i < groupBuilds.Count; i = i + 1)
                {
                    string proj = groupBuilds[i][0];
                    string groupName = groupBuilds[i][1];
                    string outDir = Path.Combine("public", "src", groupName);
                    string args = "proj " + proj + " -o " + outDir + " --build";
                    Console.WriteLine("[SetUp] 步4/" + (i + 1) + "/" + groupBuilds.Count + "：" + groupName);
                    long groupMs = Environment.TickCount64;
                    bool groupOk = RunProcess(mauExe, args, repoRoot);
                    _steps.Add(new StepReport() { Step = 4, Name = "mau proj " + groupName + " --build", Ok = groupOk, Ms = Environment.TickCount64 - groupMs });
                    if (!groupOk)
                    {
                        return Fail("步4 组翻译失败：" + groupName + "——中止。");
                    }
                }

                // [段5] 步5：dotnet publish CatHome4 -c Debug -o public\app（宿主部署区）
                long hostMs = Environment.TickCount64;
                // 出口按平台选：Windows = 桌面壳（WinExe）；非 Windows = headless（net10.0——Linux 无 WinForms 运行时，
                // 发布桌面壳会得到要求 Microsoft.WindowsDesktop.App 的 runtimeconfig，宿主直接起不来）
                string hostProj = OperatingSystem.IsWindows()
                    ? Path.Combine("CatHome4", "CatHome4.Desktop.Win", "CatHome4.Desktop.Win.csproj")
                    : Path.Combine("CatHome4", "CatHome4.csproj");
                bool hostOk = RunProcess("dotnet", "publish " + hostProj + " -c Debug -o " + Path.Combine("public", "app"), repoRoot);
                _steps.Add(new StepReport() { Step = 5, Name = "publish CatHome4 -> public/app", Ok = hostOk, Ms = Environment.TickCount64 - hostMs });
                if (!hostOk)
                {
                    return Fail("步5 publish CatHome4 失败——宿主部署区未生成，中止。");
                }

                // [段5b] 音频资产复制——仓库 assets/ → public/app/assets/（随 incr 搬运进入运行区）
                // 可见性（A264）：成功也落步明细（文件数 / 字节数）；源缺失记 Detail 出声——静默跳过正是提示音消失的根因形态
                long assetsMs = Environment.TickCount64;
                string assetsPublishSrc = Path.Combine(repoRoot, "assets");
                string assetsPublishDst = Path.Combine(repoRoot, "public", "app", "assets");
                bool assetsOk = true;
                string assetsDetail = "";
                if (Directory.Exists(assetsPublishSrc))
                {
                    assetsOk = CopyDirectory(assetsPublishSrc, assetsPublishDst);
                    assetsDetail = assetsOk ? "复制完成：" + DescribeTree(assetsPublishDst) : "复制失败：" + DescribeTree(assetsPublishDst);
                }
                else
                {
                    assetsDetail = "源缺失：仓库根 assets/ 不存在——提示音等资产不会进入产物区";
                }
                _steps.Add(new StepReport() { Step = 5, Name = "段5b 音频资产复制 -> public/app/assets", Ok = assetsOk, Ms = Environment.TickCount64 - assetsMs, Detail = assetsDetail });
                if (!assetsOk)
                {
                    Console.WriteLine("[SetUp] ⚠ 音频资产复制失败：" + assetsDetail);
                }

                // [段5c] 构建指纹落盘——产物自带 commit，接收端比提交身份不比时间戳
                // 动机（2026-11-16）：双机两腿下产物 mtime = 推送落盘时刻、源码 mtime = 检出时刻，
                // 恒有「源码比产物新」→ mtime 哨兵结构性误拒；commit 全等 ⇒ 源码树内容相同，
                // mtime 判据要防的静默失效被全覆盖且无假阳性。
                // 严判（莎 2026-11-16）：工作区有未提交改动 / 取不到 HEAD 即中止——产物无核验依据，不做半成品。
                long buildInfoMs = Environment.TickCount64;
                string buildInfoCommit;
                string buildInfoDetail;
                bool buildInfoOk = WriteBuildInfo(repoRoot, Path.Combine(repoRoot, "public", "app"), out buildInfoCommit, out buildInfoDetail);
                _steps.Add(new StepReport() { Step = 5, Name = "段5c 构建指纹落盘 -> public/app/buildinfo.txt", Ok = buildInfoOk, Ms = Environment.TickCount64 - buildInfoMs, Detail = buildInfoDetail });
                if (!buildInfoOk)
                {
                    return Fail("段5c 构建指纹落盘失败：" + buildInfoDetail + "——中止（无指纹的产物接收端必拒）。");
                }
                // [段6] 步6：宿主自检——CLI 全链（主线程直执 + 进程自退；8080 单例——验证前确认无宿主占用）
                string hostExe = Path.Combine(repoRoot, "public", "app", PlatformServices.Paths.HostExeName);
                if (!File.Exists(hostExe))
                {
                    return Fail("步6 前置缺失：" + hostExe + " 不存在——宿主未生成。");
                }
                long selfMs = Environment.TickCount64;
                bool selfOk = RunProcess(hostExe, "--run \"session count\"", repoRoot);
                _steps.Add(new StepReport() { Step = 6, Name = "宿主自检 --run session count", Ok = selfOk, Ms = Environment.TickCount64 - selfMs });
                if (!selfOk)
                {
                    return Fail("步6 宿主自检失败——CLI 全链未通过，中止。");
                }

                Console.WriteLine("[SetUp] prepare 完成——public/ + Mau-public/ 已就绪。");
                return 0;
            }
            finally
            {
                // [段7] 收尾清理（无论成败必执行）：shutdown 构建服务器——不留前台不可见的隐式后台驻留
                // dotnet build/test/publish 每次调用会驻留 MSBuild/VBCSCompiler 进程（无窗口、锁目录）
                // 不允许这种负优化——结束前统一 shutdown；下一轮 prepare 冷启动，代价是首轮稍慢但零残留
                Console.WriteLine("[SetUp] 清理构建服务器（dotnet build-server shutdown）…");
                if (!RunProcess("dotnet", "build-server shutdown", repoRoot))
                {
                    Console.WriteLine("[SetUp] 警告：build-server shutdown 未正常退出——可能有隐式构建进程残留，建议手动 dotnet build-server shutdown。");
                }
            }
        }

        /// <summary>
        /// 构建指纹落盘——写 public/app/buildinfo.txt（四行键=值：commit / dirty / built_at / version；UTF-8 无 BOM + LF）。
        /// 来源：commit = git HEAD 完整 40 位；dirty = git status --porcelain 是否非空。
        /// 严判（莎 2026-11-16）：取不到 HEAD / git 不可用 / 工作区有未提交改动 → 一律失败，不落半成品指纹
        /// （产物无核验依据，接收端必拒——把门禁推到最左侧）。
        /// </summary>
        /// <param name="repoRoot">仓库根</param>
        /// <param name="appDir">产物区（public/app）</param>
        /// <param name="commit">输出：写入的 commit（失败为空串）</param>
        /// <param name="detail">输出：步明细（成功 = commit 前 8 位 + dirty；失败 = 原因）</param>
        /// <returns>true=已落盘</returns>
        private static bool WriteBuildInfo(string repoRoot, string appDir, out string commit, out string detail)
        {
            commit = "";
            detail = "";
            string head = RunProcessCapture("git", "rev-parse HEAD", repoRoot);
            if (head == null)
            {
                detail = "取 git HEAD 失败（git 不可用或仓库不可读）";
                return false;
            }
            head = head.Trim();
            if (head.Length != 40)
            {
                detail = "git HEAD 非 40 位 commit（实际 " + head.Length.ToString() + " 位）：" + head;
                return false;
            }
            string status = RunProcessCapture("git", "status --porcelain", repoRoot);
            if (status == null)
            {
                detail = "取 git status 失败——未知即脏，拒绝落指纹";
                return false;
            }
            string dirtyItem = FindDirtyItem(status);
            if (dirtyItem.Length > 0)
            {
                detail = "工作区有未提交改动——产物无法被接收端核验，请先提交：" + dirtyItem;
                return false;
            }
            string text = "commit=" + head + "\n"
                + "dirty=false\n"
                + "built_at=" + DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss") + "\n"
                + "version=" + ReadVersion(repoRoot) + "\n";
            string path = Path.Combine(appDir, "buildinfo.txt");
            try
            {
                File.WriteAllText(path, text, new UTF8Encoding(false));
            }
            catch (Exception ex)
            {
                detail = "写 buildinfo.txt 失败：" + ex.Message;
                return false;
            }
            commit = head;
            detail = "commit=" + ShortCommit(head) + " dirty=false";
            return true;
        }

        /// <summary>commit 短号——前 8 位（回执 / 拒绝语的可读面；比对一律用完整 40 位）。</summary>
        /// <param name="commit">完整 commit</param>
        /// <returns>前 8 位（不足则原样）</returns>
        private static string ShortCommit(string commit)
        {
            if (commit == null || commit.Length <= 8)
            {
                return commit ?? "";
            }
            return commit.Substring(0, 8);
        }

        /// <summary>
        /// 取首个「非产物元数据」的脏条目——排除 Directory.Build.props（版本单点：prepare 段0 的自增会写它，
        /// 属产物自述字段而非源码改动；版本语义已固定为「产物自述」，不承载源码状态，源码状态由 commit 承载）。
        /// </summary>
        /// <param name="status">git status --porcelain 全文（可为 null）</param>
        /// <returns>首个脏条目原文（无脏条目返回空串）</returns>
        private static string FindDirtyItem(string status)
        {
            if (status == null)
            {
                return "";
            }
            string[] lines = status.Split('\n');
            for (int i = 0; i < lines.Length; i = i + 1)
            {
                string line = lines[i].TrimEnd();
                if (line.Trim().Length == 0)
                {
                    continue;
                }
                // 格式：XY <path>——两列状态码后即路径（含空格时 git 会加引号）
                string path = line.Length > 3 ? line.Substring(3).Trim().Trim('"') : "";
                if (string.Equals(path, "Directory.Build.props", StringComparison.Ordinal))
                {
                    continue;
                }
                return line.Trim();
            }
            return "";
        }

        /// <summary>
        /// 失败收尾——红字提示 + 返回非0。
        /// </summary>
        /// <param name="message">失败信息</param>
        /// <returns>退出码 1</returns>
        private static int Fail(string message)
        {
            Console.WriteLine("[SetUp] ❌ " + message);
            return 1;
        }
    }
}
