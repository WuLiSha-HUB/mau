using System;
using System.Collections.Generic;
using System.IO;

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
                // [段0a] 自举代际自检（A264）——运行中的 SetUp.exe 落后于 SetUp/ 源码即出声
                // 不阻断：旧 exe 仍能自举；但必须可见（design-ch4-release §九：SetUp/ 源码与 SetUp.exe 同 commit）
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
                stepOk = RunProcess("dotnet", "publish Mau\\Mau.Cli\\Mau.Cli.csproj -c Debug -o Mau-public", repoRoot);
                _steps.Add(new StepReport() { Step = 3, Name = "publish Mau.Cli -> Mau-public", Ok = stepOk, Ms = Environment.TickCount64 - stepMs });
                if (!stepOk)
                {
                    return Fail("步3 publish Mau.Cli 失败——基座部署区未生成，中止。");
                }

                // [段4] 步4：mau proj 全组扫描（工作目录=仓库根——黄金对比/积木索引依赖相对路径；经 Mau-public\Mau.exe 调用）
                // 扫描化：遍历 corpus/ch4/ 目录，有 .mauproj 即构建（design-ch4-flow-scan §3.1——新增 Flow 零 SetUp 改动）
                string mauExe = Path.Combine(repoRoot, "Mau-public", "Mau.exe");
                if (!File.Exists(mauExe))
                {
                    return Fail("步4 前置缺失：" + mauExe + " 不存在——基座 CLI 未生成。");
                }
                string corpusDir = Path.Combine(repoRoot, "corpus", "ch4");
                if (!Directory.Exists(corpusDir))
                {
                    return Fail("步4 前置缺失：corpus\\ch4 目录不存在——语料堆未就位。");
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
                    string projRel = "corpus\\ch4\\" + groupName + "\\" + Path.GetFileName(mauprojs[0]);
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
                    string outDir = "public\\src\\" + groupName;
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
                bool hostOk = RunProcess("dotnet", "publish CatHome4\\CatHome4.csproj -c Debug -o public\\app", repoRoot);
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

                // [段6] 步6：宿主自检——CLI 全链（主线程直执 + 进程自退；8080 单例——验证前确认无宿主占用）
                string hostExe = Path.Combine(repoRoot, "public", "app", "CatHome4.exe");
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
