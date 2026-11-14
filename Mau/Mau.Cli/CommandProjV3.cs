using System;
using System.IO;
using Mau.Development;

namespace Mau.Cli
{
    /// <summary>
    /// mauproj 组工程命令——统一构筑链（design-ch4-deploy.md §三）。
    /// 组翻译：.mau → BRIKGROUP.cs + 各 FL_*.cs + csproj → 落盘 public/src/&lt;组&gt;/；--build 走 dotnet build → public/app/Flows/FL_&lt;组&gt;.dll。
    /// 单 mau 也建 mauproj（dll 粒度 = mauproj 粒度 = 热重载粒度）——无双轨。
    /// B3 改造：执行段下沉 Mau.Development.MauGroupBuilder（与宿主 CatHome4 的 mau.* 工具共用——消灭双轨；宿主原名 CH4.Entry）；参数解析与输出格式保持 v0.26 等价。
    /// </summary>
    public static class CommandProjV3
    {
        /// <summary>proj 命令入口——mau proj &lt;组.mauproj&gt; [-o srcDir] [--build]（产物固定 public/app/Flows/——--out 分支已删，2026-09-11）</summary>
        /// <param name="args">命令参数（不含 proj）</param>
        /// <returns>退出码——0 成功</returns>
        public static int Run(string[] args)
        {
            string usage = "mau proj <组.mauproj> [-o <srcDir>] [--build]";
            string? mauprojPath = null;
            string srcDir = "";
            bool doBuild = false;
            for (int i = 0; i < args.Length; i++)
            {
                if (args[i] == "-o")
                {
                    if (i + 1 >= args.Length)
                    {
                        return CliSupport.ArgError("参数缺值: -o", usage);
                    }

                    srcDir = args[i + 1];
                    i = i + 1;
                }
                else if (args[i] == "--build")
                {
                    doBuild = true;
                }
                else if (mauprojPath == null && args[i].Length > 0 && args[i].Substring(0, 1) != "-")
                {
                    mauprojPath = args[i];
                }
                else
                {
                    return CliSupport.ArgError("未知参数: " + args[i], usage);
                }
            }

            if (mauprojPath == null)
            {
                Console.WriteLine("用法: " + usage);
                return 1;
            }

            if (!File.Exists(mauprojPath))
            {
                Console.WriteLine("文件不存在: " + mauprojPath);
                return 1;
            }

            // 相对 → 绝对——CLI 工作目录=仓库根约定；BuildWithDotnet 依赖绝对路径（相对 csproj 路径 MSB1009——2026-08-19 修正）
            mauprojPath = Path.GetFullPath(mauprojPath);
            // [段1] 解析 mauproj + 输出目录（public/src/<组名>/ + public/app/Flows/——产物去向由生成 csproj 的 OutputPath 决定）
            MauProjParseResult parsed = MauProjFile.Load(mauprojPath);
            if (parsed.Error.Length > 0)
            {
                Console.WriteLine("FAIL: " + parsed.Error);
                return 1;
            }

            MauProjFile proj = parsed.File!;
            string root = CliSupport.FindWorkspaceRoot() ?? Directory.GetCurrentDirectory();
            if (srcDir.Length == 0)
            {
                srcDir = Path.Combine(root, "public", "src", proj.Name);
            }

            string dllDir = Path.Combine(root, "public", "app", "Flows");
            srcDir = Path.GetFullPath(srcDir);
            dllDir = Path.GetFullPath(dllDir);
            // [段2] 组翻译（共享服务）——Load→Collect→CompileGroup→落盘→可选 build
            MauGroupBuildResult result = MauGroupBuilder.Build(mauprojPath, srcDir, dllDir, doBuild);
            for (int i = 0; i < result.Steps.Count; i++)
            {
                Console.WriteLine(result.Steps[i]);
            }

            if (!result.Success)
            {
                for (int i = 0; i < result.FailDiagnostics.Count; i++)
                {
                    Console.WriteLine(result.FailDiagnostics[i]);
                }

                if (result.BuildOutput.Length > 0)
                {
                    Console.WriteLine("── dotnet build 输出 ──");
                    Console.WriteLine(result.BuildOutput);
                }

                if (result.Error.Length > 0)
                {
                    Console.WriteLine("FAIL: " + result.Error);
                }

                return 1;
            }

            return 0;
        }
    }
}