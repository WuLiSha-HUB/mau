using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using CatHome4.Platform;

namespace SetUp
{
    /// <summary>
    /// Program 分部——publish-linux 模式：本地交叉构建 Linux 产物区（public/app-linux）。
    /// 定位：Linux 部署链的产物端（push-app 只负责打包 + 传输），一次调用产出可直接投放的产物区。
    /// 与 prepare 的差别三处：① **不自增版本**——版本自增点唯一定义在 prepare，Linux 链只是同一份源码的
    /// Linux 形态，重编一次不该产生第二个代际（否则同一源码两个代际号）；② **输出目录独立**——
    /// public/app 保 Windows 桌面壳形态（本地 incr 依赖），Linux 产物落 public/app-linux，两平台产物不再混装
    /// （原混装形态会把 WinExe 与桌面壳 dll 一起打进 Linux 包）；③ **不做宿主自检**——Linux apphost 在
    /// Windows 上跑不起来，改做静态结构闸（可执行 / runtimeconfig / Flows / 指纹回读）。
    /// 规范：design-ch4-deploy.md · design-ch4-release.md §6.1 · tools/deploy/README.md
    /// </summary>
    public static partial class Program
    {
        /// <summary>
        /// Linux 产物区目录名——与 Windows 产物区 public/app 并列（两平台产物不混装）。
        /// </summary>
        private const string LinuxAppDirName = "app-linux";

        /// <summary>
        /// publish-linux 模式入口——门禁（build/test）→ 基座 → 组翻译 → linux 交叉发布 → 组装 → 结构闸。
        /// 🔴 版本不自增：version.txt 与 buildinfo.txt 一律读 Directory.Build.props 现值。
        /// </summary>
        /// <param name="repoRoot">仓库根</param>
        /// <returns>退出码（0=成功，非0=失败）</returns>
        private static int PublishLinux(string repoRoot)
        {
            Console.WriteLine("[SetUp] publish-linux —— 本地交叉构建 Linux 产物区（不自增版本）");
            Console.WriteLine("  " + repoRoot);
            try
            {
                // [段0] 版本——只读不自增（Linux 链继承本地 prepare 已产生的代际）
                string version = ReadVersion(repoRoot);
                _steps.Add(new StepReport() { Step = 0, Name = "版本读取（不自增）", Ok = true, Detail = "version=" + version });
                Console.WriteLine("[SetUp] 版本: " + version + "（不自增——代际由本地 prepare 产生）");

                // [段1] 步1：dotnet build Mau.sln（门禁与 prepare 同源——产物必须来自能通过门禁的源码）
                long stepMs = Environment.TickCount64;
                bool stepOk = RunProcess("dotnet", "build Mau.sln", repoRoot);
                _steps.Add(new StepReport() { Step = 1, Name = "dotnet build Mau.sln", Ok = stepOk, Ms = Environment.TickCount64 - stepMs });
                if (!stepOk)
                {
                    return Fail("步1 dotnet build Mau.sln 失败——构建未通过，中止。");
                }

                // [段2] 步2：全量测试（Mau 三项目 + 宿主两项目）
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

                // [段3] 步3：dotnet publish Mau.Cli -> Mau-public（基座部署区——组翻译的引用源）
                stepMs = Environment.TickCount64;
                stepOk = RunProcess("dotnet", "publish " + Path.Combine("Mau", "Mau.Cli", "Mau.Cli.csproj") + " -c Debug -o Mau-public", repoRoot);
                _steps.Add(new StepReport() { Step = 3, Name = "publish Mau.Cli -> Mau-public", Ok = stepOk, Ms = Environment.TickCount64 - stepMs });
                if (!stepOk)
                {
                    return Fail("步3 publish Mau.Cli 失败——基座部署区未生成，中止。");
                }

                // [段4] 步4：组翻译全组——dll 固定落 public/app/Flows（CLI 出口约定），
                // 清理面只到 Flows 与 public/src（**不清 public/app 整目录**——Windows 桌面壳产物要保留给本地 incr）
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
                string[] purgeTargets = new string[]
                {
                    Path.Combine(repoRoot, "public", "app", "Flows"),
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
                // 组扫描规则与 prepare 段4 同源（扫描式——新增 Flow 零 SetUp 改动）
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
                    Console.WriteLine("[SetUp] 步4/" + (i + 1).ToString() + "/" + groupBuilds.Count.ToString() + "：" + groupName);
                    long groupMs = Environment.TickCount64;
                    bool groupOk = RunProcess(mauExe, args, repoRoot);
                    _steps.Add(new StepReport() { Step = 4, Name = "mau proj " + groupName + " --build", Ok = groupOk, Ms = Environment.TickCount64 - groupMs });
                    if (!groupOk)
                    {
                        return Fail("步4 组翻译失败：" + groupName + "——中止。");
                    }
                }

                // [段5] 步5：linux-x64 交叉发布 → public/app-linux（框架依赖，Windows 上可产 Linux apphost）
                string appLinux = Path.Combine(repoRoot, "public", LinuxAppDirName);
                if (Directory.Exists(appLinux))
                {
                    try
                    {
                        Directory.Delete(appLinux, true);
                    }
                    catch (Exception ex)
                    {
                        return Fail("步5 前置清理失败：" + appLinux + " —— " + ex.Message);
                    }
                }
                long hostMs = Environment.TickCount64;
                string hostProj = Path.Combine("CatHome4", "CatHome4.csproj");
                bool hostOk = RunProcess("dotnet", "publish " + hostProj + " -c Debug -r linux-x64 --self-contained false -o " + Path.Combine("public", LinuxAppDirName), repoRoot);
                _steps.Add(new StepReport() { Step = 5, Name = "publish CatHome4 -> public/" + LinuxAppDirName + " (linux-x64)", Ok = hostOk, Ms = Environment.TickCount64 - hostMs });
                if (!hostOk)
                {
                    return Fail("步5 linux-x64 publish 失败——Linux 产物区未生成，中止。");
                }

                // [段5b] Flows 随包——组翻译产物（dll 平台无关）从产物区拷入 Linux 产物区
                string flowsSrc = Path.Combine(repoRoot, "public", "app", "Flows");
                string flowsDst = Path.Combine(appLinux, "Flows");
                if (!Directory.Exists(flowsSrc))
                {
                    return Fail("步5b 前置缺失：" + flowsSrc + " 不存在——组翻译未产出 dll。");
                }
                long flowsMs = Environment.TickCount64;
                bool flowsOk = CopyDirectory(flowsSrc, flowsDst);
                _steps.Add(new StepReport() { Step = 5, Name = "段5b Flows 拷入 -> public/" + LinuxAppDirName + "/Flows", Ok = flowsOk, Ms = Environment.TickCount64 - flowsMs, Detail = DescribeTree(flowsDst) });
                if (!flowsOk)
                {
                    return Fail("步5b Flows 拷贝失败——Linux 包缺 Flows，中止。");
                }

                // [段5c] 资产复制——仓库根 assets/ → Linux 产物区
                long assetsMs = Environment.TickCount64;
                string assetsSrc = Path.Combine(repoRoot, "assets");
                string assetsDst = Path.Combine(appLinux, "assets");
                bool assetsOk = true;
                string assetsDetail = "";
                if (Directory.Exists(assetsSrc))
                {
                    assetsOk = CopyDirectory(assetsSrc, assetsDst);
                    assetsDetail = assetsOk ? "复制完成：" + DescribeTree(assetsDst) : "复制失败：" + DescribeTree(assetsDst);
                }
                else
                {
                    assetsDetail = "源缺失：仓库根 assets/ 不存在——提示音等资产不会进入 Linux 包";
                }
                _steps.Add(new StepReport() { Step = 5, Name = "段5c 音频资产复制 -> public/" + LinuxAppDirName + "/assets", Ok = assetsOk, Ms = Environment.TickCount64 - assetsMs, Detail = assetsDetail });
                if (!assetsOk)
                {
                    return Fail("步5c 音频资产复制失败：" + assetsDetail);
                }

                // [段5d] 外观层镜像——源区 CatHome4/html → Linux 产物区（排除 tests/node_modules：
                // Windows 依赖含平台相关二进制，推给 Linux 只增体积并埋假绿）
                long htmlMs = Environment.TickCount64;
                string htmlSrc = Path.Combine(repoRoot, "CatHome4", "html");
                string htmlDst = Path.Combine(appLinux, "html");
                bool htmlOk = true;
                string htmlDetail = "";
                if (Directory.Exists(htmlSrc))
                {
                    htmlOk = CopyDirectoryExcluding(htmlSrc, htmlDst, "node_modules");
                    htmlDetail = htmlOk ? "镜像完成：" + DescribeTree(htmlDst) : "镜像失败：" + DescribeTree(htmlDst);
                }
                else
                {
                    htmlDetail = "源缺失：CatHome4/html 不存在——外观层不会进入 Linux 包";
                }
                _steps.Add(new StepReport() { Step = 5, Name = "段5d 外观层镜像 -> public/" + LinuxAppDirName + "/html", Ok = htmlOk, Ms = Environment.TickCount64 - htmlMs, Detail = htmlDetail });
                if (!htmlOk)
                {
                    return Fail("步5d 外观层镜像失败：" + htmlDetail);
                }

                // [段5e] version.txt 落盘——读单点现值（不自增），供远端升级比对面
                string versionPath = Path.Combine(appLinux, "version.txt");
                try
                {
                    File.WriteAllText(versionPath, version, new UTF8Encoding(false));
                }
                catch (Exception ex)
                {
                    return Fail("步5e version.txt 落盘失败：" + ex.Message);
                }
                _steps.Add(new StepReport() { Step = 5, Name = "段5e version.txt -> public/" + LinuxAppDirName, Ok = true, Detail = "version=" + version });

                // [段5f] 构建指纹——投放链唯一的指纹写入点（2026-11-16 莎裁：Windows prepare 不产指纹，只此一处）
                long buildInfoMs = Environment.TickCount64;
                string buildInfoCommit = "";
                string buildInfoDetail = "";
                bool buildInfoOk = WriteBuildInfo(repoRoot, appLinux, out buildInfoCommit, out buildInfoDetail);
                _steps.Add(new StepReport() { Step = 5, Name = "段5f 构建指纹落盘 -> public/" + LinuxAppDirName + "/buildinfo.txt", Ok = buildInfoOk, Ms = Environment.TickCount64 - buildInfoMs, Detail = buildInfoDetail });
                if (!buildInfoOk)
                {
                    return Fail("段5f 构建指纹落盘失败：" + buildInfoDetail + "——中止（无指纹的产物接收端必拒）。");
                }

                // [段6] 结构闸——Linux 产物静态核验（宿主自检在 Windows 上跑不了 Linux apphost，改做结构面）
                // 🔴 Linux apphost 无扩展名——不能用 PlatformServices.Paths.HostExeName
                // （它取的是「宿主所在平台」的名字，在 Windows 上跑本模式会得 CatHome4.exe，结构闸必假红）
                string appExe = Path.Combine(appLinux, "CatHome4");
                string appRtc = Path.Combine(appLinux, "CatHome4.runtimeconfig.json");
                string appDll = Path.Combine(appLinux, "CatHome4.dll");
                string miss = "";
                if (!File.Exists(appExe))
                {
                    miss = "缺可执行 " + appExe;
                }
                else if (!File.Exists(appDll))
                {
                    miss = "缺 " + appDll;
                }
                else if (!File.Exists(appRtc))
                {
                    miss = "缺 " + appRtc;
                }
                else if (!Directory.Exists(flowsDst))
                {
                    miss = "缺 Flows 目录";
                }
                else if (!File.Exists(Path.Combine(appLinux, "buildinfo.txt")))
                {
                    miss = "缺 buildinfo.txt";
                }
                _steps.Add(new StepReport() { Step = 6, Name = "结构闸（Linux 产物静态核验）", Ok = (miss.Length == 0), Detail = (miss.Length == 0) ? ("Flows " + DescribeTree(flowsDst)) : miss });
                if (miss.Length > 0)
                {
                    return Fail("步6 结构闸未过：" + miss + "——Linux 产物不完整，中止。");
                }

                Console.WriteLine("[SetUp] publish-linux 完成——public/" + LinuxAppDirName + " 已就绪（version " + version + "，commit " + buildInfoCommit.Substring(0, 8) + "）");
                Console.WriteLine("[SetUp] 下一步：tools\\deploy\\push-app.ps1（打包 + scp 到 /srv/inbox）");
                return 0;
            }
            finally
            {
                // [段7] 收尾清理（无论成败必执行）：不留前台不可见的隐式后台驻留
                Console.WriteLine("[SetUp] 清理构建服务器（dotnet build-server shutdown）…");
                if (!RunProcess("dotnet", "build-server shutdown", repoRoot))
                {
                    Console.WriteLine("[SetUp] 警告：build-server shutdown 未正常退出——可能有隐式构建进程残留，建议手动 dotnet build-server shutdown。");
                }
            }
        }

        /// <summary>
        /// 递归目录复制（排除指定名字的目录段）——外观层镜像用（node_modules 属 Windows 本机依赖）。
        /// </summary>
        /// <param name="sourceDir">源目录</param>
        /// <param name="targetDir">目标目录</param>
        /// <param name="excludedDirName">要跳过的目录名（任一路径段命中即跳过该文件）</param>
        /// <returns>true=复制成功</returns>
        private static bool CopyDirectoryExcluding(string sourceDir, string targetDir, string excludedDirName)
        {
            try
            {
                if (!Directory.Exists(targetDir))
                {
                    Directory.CreateDirectory(targetDir);
                }
                string[] files = Directory.GetFiles(sourceDir, "*", SearchOption.AllDirectories);
                for (int i = 0; i < files.Length; i = i + 1)
                {
                    string rel = files[i].Substring(sourceDir.Length).TrimStart('\\', '/');
                    string[] segs = rel.Split(new char[] { '\\', '/' });
                    bool skip = false;
                    for (int s = 0; s < segs.Length; s = s + 1)
                    {
                        if (string.Equals(segs[s], excludedDirName, StringComparison.OrdinalIgnoreCase))
                        {
                            skip = true;
                            break;
                        }
                    }
                    if (skip)
                    {
                        continue;
                    }
                    string dst = Path.Combine(targetDir, rel);
                    string dir = Path.GetDirectoryName(dst);
                    if (dir != null && dir.Length > 0 && !Directory.Exists(dir))
                    {
                        Directory.CreateDirectory(dir);
                    }
                    File.Copy(files[i], dst, true);
                }
                return true;
            }
            catch (Exception ex)
            {
                Console.WriteLine("[SetUp] 复制异常: " + ex.Message);
                return false;
            }
        }
    }
}
