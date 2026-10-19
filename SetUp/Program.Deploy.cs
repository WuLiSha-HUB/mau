using System;
using System.IO;

namespace SetUp
{
    /// <summary>
    /// SetUp deploy 模式——产出正式运行实例到外部目标目录。
    /// 目标：把 public/app 可运行版本复制到目标目录（无 Mau.sln → 运行走 AppData Data 三级锚定）。
    /// 规范：design-ch4-release.md §二/§三/§五——目标目录禁止在仓库内；运行中先停进程（模块锁）。
    /// </summary>
    /// <summary>
    /// Program 分部——deploy 模式：产物发布到目标运行目录（原子切换 + 报告）。
    /// </summary>
    public static partial class Program
    {
        /// <summary>deploy 模式入口——public/app 落新目录后原子切换上架（现行更名 _old 留作回退源）+ 版本落盘。</summary>
        /// <param name="repoRoot">仓库根</param>
        /// <param name="targetDir">目标目录（外部运行实例）</param>
        /// <returns>退出码（0=成功，非0=失败）</returns>
        private static int Deploy(string repoRoot, string targetDir)
        {
            string sourceDir = Path.Combine(repoRoot, "public", "app");
            Console.WriteLine("[SetUp] deploy —— 部署正式运行实例（原子切换）");
            Console.WriteLine("  源: " + sourceDir);
            Console.WriteLine("  目标: " + targetDir);

            // [段1] 目标目录校验——禁止部署到仓库内（FindRepoRoot 误判 → Data 挂仓库根，污染纯净源）
            // 边界判定：比较时仓库根追加路径分隔符——防 "mau" 前缀误伤 "mau-test-deploy" 这类兄弟目录
            string targetFull = Path.GetFullPath(targetDir);
            string repoFull = Path.GetFullPath(repoRoot);
            string repoPrefix = repoFull.TrimEnd('\\', '/') + Path.DirectorySeparatorChar;
            bool insideRepo = string.Equals(targetFull, repoFull, StringComparison.OrdinalIgnoreCase) ||
                              targetFull.StartsWith(repoPrefix, StringComparison.OrdinalIgnoreCase);
            if (insideRepo)
            {
                return Fail("目标目录位于仓库内——禁止部署（Data 会误挂仓库根）。目标: " + targetFull);
            }

            // [段2] 前置校验——public\app 已就绪（prepare 过）
            string hostExe = Path.Combine(sourceDir, "CatHome4.exe");
            if (!File.Exists(hostExe))
            {
                return Fail("public\\app\\CatHome4.exe 不存在——请先运行 SetUp.exe prepare 重建发布链。");
            }

            // [段2b-4] 准备段——外观层对齐 + 复制 _new + config 模板 + version.txt（与 incr 重启链共用同一实现）
            string prepErr = PrepareTargetDir(repoRoot, sourceDir, targetFull, true);
            if (prepErr.Length > 0)
            {
                return Fail(prepErr);
            }

            // [段4b] 原子切换——现行更名 _old（留作回退源）→ 新目录更名上架（同卷改名 = 原子）
            string targetOld = targetFull + "_old";
            long switchMs = Environment.TickCount64;
            string switchErr = SwitchDirectory(targetFull, targetFull + "_new", targetOld);
            _steps.Add(new StepReport() { Step = 3, Name = "原子切换运行区", Ok = switchErr.Length == 0, Ms = Environment.TickCount64 - switchMs });
            if (switchErr.Length > 0)
            {
                return Fail("原子切换失败——" + switchErr);
            }

            // [段5] 完成提示——Data 走 AppData（本机 %LOCALAPPDATA%/CatHome4/Data 三级锚定回退）
            Console.WriteLine("[SetUp] deploy 完成。");
            Console.WriteLine("  正式实例 Data 落位: " + Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "CatHome4", "Data"));
            Console.WriteLine("  启动: " + Path.Combine(targetFull, "CatHome4.exe"));
            Console.WriteLine("  回退源: " + targetOld);
            return 0;
        }
        /// <summary>
        /// 准备段——public/app 落 &lt;target&gt;_new（外观层对齐（可选）+ 复制 + config 模板 + version.txt）。
        /// 与 incr 重启链共用同一实现：实现只有一份，入口有三个（design-ch4-host-restart §三·五）。
        /// </summary>
        /// <param name="repoRoot">仓库根</param>
        /// <param name="sourceDir">产物区目录（public/app 绝对路径）</param>
        /// <param name="targetFull">运行区绝对路径（本方法写 &lt;target&gt;_new）</param>
        /// <param name="syncHtml">是否执行「源区 html → 产物区」对齐——deploy / full 为 true；incr 为 false（纯搬运产物区，前端改动走 sync-html）</param>
        /// <returns>空串=成功；非空=错误描述</returns>
        private static string PrepareTargetDir(string repoRoot, string sourceDir, string targetFull, bool syncHtml)
        {
            // [段0] 干净起点——暂存目录若存在（上次失败残留）先清（A135，带退避重试；清理失败即中止，不留混合态）
            string stageErr;
            if (!DeleteDirectoryWithRetry(targetFull + "_new", out stageErr))
            {
                return "暂存目录清理失败（" + targetFull + "_new）：" + stageErr + "——请检查是否有程序占用该目录。";
            }

            // [段2b] 外观层静态资源对齐（G 方案 2026-09-23）——deploy 的来源是 public/app，html 面须先与源区对齐，
            //        否则把落后版本推给运行区（产物区曾被 PreserveNewest 时间戳倒挂冻结）。失败即中止，不带落后版本部署。
            //        incr 关闭本段——它是「搬运产物区」，不是「顺手又部署一次前端」
            if (syncHtml)
            {
                string htmlSource = Path.Combine(repoRoot, "CatHome4", "html");
                string htmlArtifact = Path.Combine(sourceDir, "html");
                if (Directory.Exists(htmlSource))
                {
                    if (!SyncOne(htmlSource, htmlArtifact, "产物区", 21))
                    {
                        return "外观层静态资源对齐失败——中止（避免把落后版本推给运行区）。";
                    }
                }
            }

            // [段3] 复制 public\app 平铺内容到 <target>_new（含 Flows/ html/）——先落新目录，全程不触碰现行实例
            string targetNew = targetFull + "_new";
            Console.WriteLine("[SetUp] 复制中——落新目录: " + targetNew);
            long copyMs = Environment.TickCount64;
            bool copyOk = CopyDirectory(sourceDir, targetNew);
            _steps.Add(new StepReport() { Step = 1, Name = "复制 public/app -> <target>_new", Ok = copyOk, Ms = Environment.TickCount64 - copyMs });
            if (!copyOk)
            {
                return "目录复制失败——新目录可能被占用或不可写。";
            }

            // [段3b] 配置模板复制——仓库 config/ 模板 → 新目录 config/（规范 §二：配置模板进部署包；Data 自举在首次运行完成）
            string configTpl = Path.Combine(repoRoot, "config");
            if (Directory.Exists(configTpl))
            {
                CopyDirectory(configTpl, Path.Combine(targetNew, "config"));
            }
            // [段3c] 音频资产复制——仓库 assets/ → 新目录 assets/（轮末总结提示音等随包分发；缺失不阻断）
            string assetsSrc = Path.Combine(repoRoot, "assets");
            if (Directory.Exists(assetsSrc))
            {
                CopyDirectory(assetsSrc, Path.Combine(targetNew, "assets"));
            }

            // [段4] 版本落盘——version.txt（读取 Directory.Build.props 单点 Version；写新目录）
            string version = ReadVersion(repoRoot);
            long verMs = Environment.TickCount64;
            bool verOk = true;
            try
            {
                File.WriteAllText(Path.Combine(targetNew, "version.txt"), version);
            }
            catch (Exception ex)
            {
                verOk = false;
                Console.WriteLine("[SetUp] 警告：version.txt 写入失败——" + ex.Message);
            }
            _steps.Add(new StepReport() { Step = 2, Name = "版本落盘 version.txt", Ok = verOk, Ms = Environment.TickCount64 - verMs });
            Console.WriteLine("[SetUp] 版本: " + version);
            return "";
        }
        /// <summary>
        /// 原子切换运行区——现行目录更名 _old（先清旧回退源）→ 新目录更名上架；任一步失败即回滚。
        /// 前提：运行区（MauOut）无持久信息——持久数据在 Data 三级锚定区，整目录替换安全。
        /// </summary>
        /// <param name="target">现行运行区绝对路径</param>
        /// <param name="targetNew">新目录绝对路径（内容已就绪）</param>
        /// <param name="targetOld">回退源绝对路径</param>
        /// <returns>空串=切换成功；非空=错误描述（已尽力回滚）</returns>
        private static string SwitchDirectory(string target, string targetNew, string targetOld)
        {
            // [段1] 清旧回退源——上一次切换的残留（带退避重试：句柄未释放时删除同样失败）
            if (Directory.Exists(targetOld))
            {
                string delErr;
                if (!DeleteDirectoryWithRetry(targetOld, out delErr))
                {
                    string diag = DiagnoseDirectoryHolders(targetOld);
                    Console.WriteLine("[SetUp] 持有者诊断（" + targetOld + "）：");
                    Console.WriteLine(diag);
                    return "回退源清理失败（" + targetOld + "）：" + delErr + Environment.NewLine + diag;
                }
            }
            // [段2] 现行 → 回退源（A135——退避重试：宿主退出到文件映射 / 句柄完全回收存在时序窗口）
            bool movedOld = false;
            if (Directory.Exists(target))
            {
                string moveErr;
                if (!MoveDirectoryWithRetry(target, targetOld, out moveErr))
                {
                    CleanupStaging(targetNew);
                    string diag = DiagnoseDirectoryHolders(target);
                    Console.WriteLine("[SetUp] 持有者诊断（" + target + "）：");
                    Console.WriteLine(diag);
                    return "现行目录改名失败：" + moveErr + Environment.NewLine + diag;
                }
                movedOld = true;
            }
            // [段3] 新目录上架——失败回滚（回退源改回现行；回滚同样带退避重试）
            string upErr;
            if (!MoveDirectoryWithRetry(targetNew, target, out upErr))
            {
                if (movedOld)
                {
                    string rollbackErr;
                    if (!MoveDirectoryWithRetry(targetOld, target, out rollbackErr))
                    {
                        Console.WriteLine("[SetUp] 警告：回滚失败——" + rollbackErr);
                    }
                }
                CleanupStaging(targetNew);
                return "新目录上架失败（已回滚）：" + upErr;
            }
            return "";
        }
        /// <summary>
        /// 清理暂存目录（A135）——切换失败后不留 _new 残留（下次准备段会重建）；清理失败出声但不改判定。
        /// </summary>
        /// <param name="targetNew">暂存目录（&lt;target&gt;_new）</param>
        private static void CleanupStaging(string targetNew)
        {
            string err;
            if (!DeleteDirectoryWithRetry(targetNew, out err))
            {
                Console.WriteLine("[SetUp] 警告：暂存目录清理失败——" + targetNew + "：" + err);
            }
            else
            {
                Console.WriteLine("[SetUp] 暂存目录已清理：" + targetNew);
            }
        }
        /// <summary>切换类目录操作的退避重试次数（A135 引入；A139 由 10 提升至 20——实测 3 s 窗口不够，持有者可能是稳定持有而非正在退出）</summary>
        private const int SwitchRetryAttempts = 20;
        /// <summary>切换类目录操作的退避间隔毫秒（20 × 300 ms = 6 s 上限）</summary>
        private const int SwitchRetryDelayMs = 300;
        /// <summary>
        /// 目录改名（带退避重试）——一次失败即判死会写出不健壮的底层实现：宿主刚退出时其文件映射 / 句柄可能尚未回收。
        /// </summary>
        /// <param name="src">源目录</param>
        /// <param name="dest">目标目录</param>
        /// <param name="err">错误描述（空=成功）</param>
        /// <returns>true=改名成功</returns>
        private static bool MoveDirectoryWithRetry(string src, string dest, out string err)
        {
            err = "";
            for (int attempt = 1; attempt <= SwitchRetryAttempts; attempt = attempt + 1)
            {
                try
                {
                    Directory.Move(src, dest);
                    if (attempt > 1)
                    {
                        Console.WriteLine("[SetUp] 目录改名重试成功（第 " + attempt.ToString() + " 次）: " + src);
                    }
                    return true;
                }
                catch (Exception ex)
                {
                    err = ex.Message;
                    if (attempt < SwitchRetryAttempts)
                    {
                        Console.WriteLine("[SetUp] 目录改名失败（第 " + attempt.ToString() + "/" + SwitchRetryAttempts.ToString() + " 次）——" + ex.Message + "；退避 " + SwitchRetryDelayMs.ToString() + " ms 后重试。");
                        System.Threading.Thread.Sleep(SwitchRetryDelayMs);
                    }
                }
            }
            err = err + "（重试 " + SwitchRetryAttempts.ToString() + " 次仍失败——疑似外部程序仍持有该目录句柄）";
            return false;
        }
        /// <summary>
        /// 目录删除（带退避重试）——目录不存在视为成功；句柄未释放时删除同样会失败。
        /// </summary>
        /// <param name="dir">目标目录</param>
        /// <param name="err">错误描述（空=成功）</param>
        /// <returns>true=已删除或本就不存在</returns>
        private static bool DeleteDirectoryWithRetry(string dir, out string err)
        {
            err = "";
            if (!Directory.Exists(dir))
            {
                return true;
            }
            for (int attempt = 1; attempt <= SwitchRetryAttempts; attempt = attempt + 1)
            {
                try
                {
                    Directory.Delete(dir, true);
                    if (attempt > 1)
                    {
                        Console.WriteLine("[SetUp] 目录删除重试成功（第 " + attempt.ToString() + " 次）: " + dir);
                    }
                    return true;
                }
                catch (Exception ex)
                {
                    err = ex.Message;
                    if (attempt < SwitchRetryAttempts)
                    {
                        Console.WriteLine("[SetUp] 目录删除失败（第 " + attempt.ToString() + "/" + SwitchRetryAttempts.ToString() + " 次）——" + ex.Message + "；退避 " + SwitchRetryDelayMs.ToString() + " ms 后重试。");
                        System.Threading.Thread.Sleep(SwitchRetryDelayMs);
                    }
                }
            }
            err = err + "（重试 " + SwitchRetryAttempts.ToString() + " 次仍失败——疑似外部程序仍持有该目录句柄）";
            return false;
        }

        /// <summary>
        /// 递归目录复制——文件覆盖复制（已存在直接覆盖），失败即中止。
        /// </summary>
        /// <param name="sourceDir">源目录</param>
        /// <param name="targetDir">目标目录</param>
        /// <returns>true=复制成功</returns>
        private static bool CopyDirectory(string sourceDir, string targetDir)
        {
            try
            {
                if (!Directory.Exists(targetDir))
                {
                    Directory.CreateDirectory(targetDir);
                }
                string[] dirs = Directory.GetDirectories(sourceDir, "*", SearchOption.AllDirectories);
                for (int i = 0; i < dirs.Length; i = i + 1)
                {
                    string rel = dirs[i].Substring(sourceDir.Length).TrimStart('\\', '/');
                    Directory.CreateDirectory(Path.Combine(targetDir, rel));
                }
                string[] files = Directory.GetFiles(sourceDir, "*", SearchOption.AllDirectories);
                for (int i = 0; i < files.Length; i = i + 1)
                {
                    string rel = files[i].Substring(sourceDir.Length).TrimStart('\\', '/');
                    File.Copy(files[i], Path.Combine(targetDir, rel), true);
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
