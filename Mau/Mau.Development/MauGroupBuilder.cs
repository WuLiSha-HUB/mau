using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Text;
using Mau.Translator;

namespace Mau.Development
{
    /// <summary>
    /// 组翻译共享服务——统一构筑链（design-ch4-deploy.md §三）的库形态承载。
    /// B3 下沉：自 Mau.Cli/CommandProjV3 提取——Mau.Cli 与宿主 CatHome4（原名 CH4.Entry）共用同一实现（消灭双轨）；
    /// 返回结构化结果，输出由调用方格式化（CLI Console / CH4 工具结果）。
    /// 流程：解析 mauproj → 收集成员 → 组翻译（MauCompilerV3.CompileGroup）→ 落盘 public/src/&lt;组&gt;/ → 可选 dotnet build → Flows/FL_&lt;组&gt;.dll。
    /// </summary>
    public static class MauGroupBuilder
    {
        /// <summary>
        /// 组翻译 + 可选编译——.mauproj → 中间产物（srcDir）→ 可选 dotnet build（dllDir）
        /// </summary>
        /// <param name="mauprojPath">.mauproj 文件绝对路径</param>
        /// <param name="srcDir">中间产物输出目录（public/src/&lt;组&gt;/）</param>
        /// <param name="dllDir">dll 输出目录（唯一来源 public/app/Flows/——与生成 csproj 的 OutputPath 一致；非产物定向，仅作落点校验）</param>
        /// <param name="doBuild">true=翻译后执行 dotnet build</param>
        /// <returns>结构化结果（Success=false 时 Error/FailDiagnostics 有内容）</returns>
        public static MauGroupBuildResult Build(string mauprojPath, string srcDir, string dllDir, bool doBuild)
        {
            MauGroupBuildResult result = new MauGroupBuildResult();
            try
            {
                // [段1] 解析 mauproj
                MauProjParseResult parsed = MauProjFile.Load(mauprojPath);
                if (parsed.Error.Length > 0)
                {
                    result.Error = parsed.Error;
                    return result;
                }
                MauProjFile proj = parsed.File!;
                result.Steps.Add("组 " + proj.Name + " | 版本 " + (proj.Version.Length > 0 ? proj.Version : "（无）"));

                // [段2] 输出目录
                Directory.CreateDirectory(srcDir);
                Directory.CreateDirectory(dllDir);

                // [段3] 收集成员文件 + 组翻译（embedBricks=false——积木进 BRIKGROUP.cs）
                List<string> files = proj.CollectFiles();
                if (files.Count == 0)
                {
                    result.Error = "组 " + proj.Name + " 无成员 .mau 文件";
                    return result;
                }
                result.Steps.Add("成员 " + files.Count + " 个");
                string[] sources = new string[files.Count];
                string[] flowNames = new string[files.Count];
                for (int i = 0; i < files.Count; i++)
                {
                    sources[i] = File.ReadAllText(files[i]);
                    flowNames[i] = FlowNameFromPath(files[i]);
                    result.Steps.Add("  " + Path.GetFileName(files[i]) + " → FL_" + flowNames[i]);
                }
                GroupCompileResultV3 group = MauCompilerV3.CompileGroup(sources, flowNames);
                for (int i = 0; i < group.Results.Count; i++)
                {
                    if (!group.Results[i].Success)
                    {
                        for (int d = 0; d < group.Results[i].Diagnostics.Count; d++)
                        {
                            MauDiagnostic diag = group.Results[i].Diagnostics[d];
                            result.FailDiagnostics.Add(files[i] + ":" + diag.Line + ": " + diag.Code + ": " + diag.Message);
                        }
                        result.Error = "构建失败: " + Path.GetFileName(files[i]) + " 验证未通过——整组拒绝";
                        return result;
                    }
                }

                // [段4] 落盘——csproj + BRIKGROUP.cs + 各 FL_*.cs（确定性全量重写——陈旧窗口 0）
                string csprojPath = Path.Combine(srcDir, proj.Name + ".csproj");
                WriteCsproj(csprojPath, proj, group.Results.Count);
                result.Steps.Add("csproj 生成: " + csprojPath);
                string brikgroupPath = Path.Combine(srcDir, "BRIKGROUP.cs");
                File.WriteAllText(brikgroupPath, group.BrickGroupSource, Encoding.UTF8);
                result.Steps.Add("BRIKGROUP.cs: " + (group.BrickGroupSource.Length > 0 ? "积木共享闭包已生成" : "（组无积木引用）"));
                for (int i = 0; i < group.Results.Count; i++)
                {
                    string outFile = Path.Combine(srcDir, "FL_" + flowNames[i] + ".cs");
                    File.WriteAllText(outFile, group.Results[i].GeneratedCode, Encoding.UTF8);
                }
                result.Steps.Add("中间产物落盘: " + srcDir + "（" + (group.Results.Count + 1) + " 文件）");

                // [段5] 可选 dotnet build——标准链（PDB/可调试）
                if (doBuild)
                {
                    bool ok = BuildWithDotnet(csprojPath, dllDir, proj.Name, result);
                    if (!ok)
                    {
                        return result;
                    }
                    result.DllPath = Path.Combine(dllDir, "FL_" + proj.Name + ".dll");
                    result.Steps.Add("构建成功: " + result.DllPath);
                }
                result.Success = true;
                return result;
            }
            catch (Exception ex)
            {
                result.Error = "组翻译异常——" + ex.GetType().Name + ": " + ex.Message;
                return result;
            }
        }

        /// <summary>
        /// dotnet build 标准链——编译中间产物 → Flows/FL_&lt;组&gt;.dll
        /// </summary>
        /// <param name="csprojPath">组 csproj 路径</param>
        /// <param name="dllDir">dll 输出目录</param>
        /// <param name="groupName">组名</param>
        /// <param name="result">结果（BuildOutput 写入尾部输出）</param>
        /// <returns>构建是否成功</returns>
        private static bool BuildWithDotnet(string csprojPath, string dllDir, string groupName, MauGroupBuildResult result)
        {
            // 180s watchdog——dotnet build 大组可能长；管道死锁根治走共享 ProcessRunner（双流并行读）
            ProcessRunResult pr = ProcessRunner.RunAndCapture("dotnet", "build \"" + csprojPath + "\"", Path.GetDirectoryName(csprojPath) ?? ".", 180000);
            if (!pr.Started)
            {
                result.Error = "FAIL: dotnet 进程启动失败";
                return false;
            }
            string tail = TailLines(pr.Stdout + "\n" + pr.Stderr, 15);
            if (!pr.Exited)
            {
                result.BuildOutput = tail;
                result.Error = "失败: dotnet build 超时（180s）已强杀";
                return false;
            }
            result.BuildOutput = tail;
            if (pr.ExitCode != 0)
            {
                result.Error = "构建失败: dotnet build exit " + pr.ExitCode;
                return false;
            }
            result.Steps.Add(tail);
            string dllPath = Path.Combine(dllDir, "FL_" + groupName + ".dll");
            if (!File.Exists(dllPath))
            {
                result.Error = "构建完成但未找到输出: " + dllPath + "（检查 csproj OutputPath）";
                return false;
            }
            return true;
        }
        /// <summary>
        /// 从文件路径推导流程名——talk.mau → Talk（自 Mau.Cli Program.FlowNameFromPath 移入共享库）
        /// </summary>
        /// <param name="path">文件路径</param>
        /// <returns>PascalCase 流程名</returns>
        public static string FlowNameFromPath(string path)
        {
            string baseName = Path.GetFileNameWithoutExtension(path);
            string[] parts = baseName.Split('_');
            StringBuilder sb = new StringBuilder();
            for (int i = 0; i < parts.Length; i++)
            {
                if (parts[i].Length == 0)
                {
                    continue;
                }
                string head = parts[i].Substring(0, 1).ToUpperInvariant();
                string tail = parts[i].Length > 1 ? parts[i].Substring(1) : "";
                sb.Append(head);
                sb.Append(tail);
            }
            return sb.ToString();
        }

        /// <summary>
        /// 写组 csproj——SDK 项目头 + Reference Mau-public/ dll（引用源铁律 design-ch4-deploy.md §五）
        /// </summary>
        /// <param name="path">csproj 输出路径</param>
        /// <param name="proj">mauproj 组声明</param>
        /// <param name="fileCount">FL 生成物数量（不含 BRIKGROUP）</param>
        private static void WriteCsproj(string path, MauProjFile proj, int fileCount)
        {
            // [段1] 引用解析——mauproj 引用: 声明 → Mau-public/ dll（默认基座自动带；缺失 WARN 不入 csproj）
            string root = FindWorkspaceRoot() ?? Directory.GetCurrentDirectory();
            string mauPublic = Path.Combine(root, "Mau-public");
            List<string> refErrors;
            List<string> refs = proj.ResolveReferences(mauPublic, out refErrors);
            refs.Add(Path.Combine(mauPublic, "Mau.Runtime.dll"));
            refs.Add(Path.Combine(mauPublic, "Mau.Contracts.dll"));
            // 引用缺失留痕（R2-P3：原静默——编译失败时才暴露，缺失面不可观测）
            for (int i = 0; i < refErrors.Count; i = i + 1)
            {
                Console.WriteLine("[mau proj] WARN 引用缺失: " + refErrors[i]);
            }

            // [段2] csproj 文本——相对 src/<组>/ 的 Mau-public/ 引用（../../.. 到仓库根）
            StringBuilder sb = new StringBuilder();
            sb.AppendLine("<Project Sdk=\"Microsoft.NET.Sdk\">");
            sb.AppendLine("  <PropertyGroup>");
            sb.AppendLine("    <TargetFramework>net10.0</TargetFramework>");
            sb.AppendLine("    <Nullable>disable</Nullable>");
            sb.AppendLine("    <ImplicitUsings>disable</ImplicitUsings>");
            sb.AppendLine("    <AssemblyName>FL_" + proj.Name + "</AssemblyName>");
            sb.AppendLine("    <RootNamespace>Mau.Generated</RootNamespace>");
            sb.AppendLine("    <OutputPath>" + "../../app/Flows/" + "</OutputPath>");
            sb.AppendLine("    <AppendTargetFrameworkToOutputPath>false</AppendTargetFrameworkToOutputPath>");
            sb.AppendLine("    <EnableDefaultCompileItems>false</EnableDefaultCompileItems>");
            sb.AppendLine("  </PropertyGroup>");
            sb.AppendLine("  <ItemGroup>");
            sb.AppendLine("    <Compile Include=\"BRIKGROUP.cs\" />");
            sb.AppendLine("    <Compile Include=\"FL_*.cs\" />");
            sb.AppendLine("  </ItemGroup>");
            sb.AppendLine("  <ItemGroup>");
            for (int i = 0; i < refs.Count; i++)
            {
                string name = Path.GetFileNameWithoutExtension(refs[i]);
                // HintPath 相对 csproj 所在目录（public/src/<组>/）——仓库根为 ../../..
                string hint = Path.Combine("../../..", "Mau-public", name + ".dll");
                sb.AppendLine("    <Reference Include=\"" + name + "\">");
                sb.AppendLine("      <HintPath>" + hint + "</HintPath>");
                sb.AppendLine("      <Private>false</Private>");
                sb.AppendLine("    </Reference>");
            }
            sb.AppendLine("  </ItemGroup>");
            sb.AppendLine("</Project>");
            File.WriteAllText(path, sb.ToString(), new UTF8Encoding(true));
        }

        /// <summary>
        /// 静态仓库根注入——宿主 Bootstrap 从受控根 id=mau 设置；优先于 cwd 探测（BRIK-MAU 工具链）
        /// </summary>
        private static string? _injectedRoot;

        /// <summary>
        /// 注入仓库根——宿主启动时调用（受控根 id=mau）；空/不含 Mau.sln 忽略
        /// </summary>
        /// <param name="root">仓库根绝对路径</param>
        public static void SetWorkspaceRoot(string root)
        {
            if (root == null || root.Length == 0)
            {
                return;
            }
            _injectedRoot = root;
        }

        /// <summary>
        /// 查找 workspace 根——注入根 → env MAU_ROOT → 当前目录向上 → 程序集位置兜底
        /// </summary>
        /// <returns>workspace 根或空</returns>
        public static string? FindWorkspaceRoot()
        {
            if (_injectedRoot != null && _injectedRoot.Length > 0 && File.Exists(Path.Combine(_injectedRoot, "Mau.sln")))
            {
                return _injectedRoot;
            }
            string? envRoot = Environment.GetEnvironmentVariable("MAU_ROOT");
            if (envRoot != null && envRoot.Length > 0 && File.Exists(Path.Combine(envRoot, "Mau.sln")))
            {
                return envRoot;
            }
            string? from = Directory.GetCurrentDirectory();
            string? found = Probe(from, new string[] { "Mau.sln" });
            if (found != null)
            {
                return found;
            }
            return Probe(AppContext.BaseDirectory, new string[] { "Mau.sln" });
        }

        /// <summary>
        /// 向上探测标记
        /// </summary>
        /// <param name="from">起始目录</param>
        /// <param name="markers">标记集合</param>
        /// <returns>命中目录或空</returns>
        private static string? Probe(string? from, string[] markers)
        {
            string? dir = from == null ? null : new DirectoryInfo(from).FullName;
            while (dir != null)
            {
                for (int i = 0; i < markers.Length; i = i + 1)
                {
                    string candidate = Path.Combine(dir, markers[i]);
                    if (File.Exists(candidate) || Directory.Exists(candidate))
                    {
                        return dir;
                    }
                }
                dir = Directory.GetParent(dir)?.FullName;
            }
            return null;
        }

        /// <summary>
        /// 尾部保留截断——从尾部保留最后 N 行（错误/结论在尾部）
        /// </summary>
        /// <param name="text">原始文本</param>
        /// <param name="maxLines">保留行数</param>
        /// <returns>截断后文本</returns>
        private static string TailLines(string text, int maxLines)
        {
            if (text == null || text.Trim().Length == 0)
            {
                return "（无输出）";
            }
            string[] lines = text.Replace("\r\n", "\n").Split('\n');
            int start = lines.Length > maxLines ? lines.Length - maxLines : 0;
            StringBuilder sb = new StringBuilder();
            for (int i = start; i < lines.Length; i = i + 1)
            {
                sb.AppendLine(lines[i]);
            }
            return sb.ToString();
        }
    }

    /// <summary>
    /// 组翻译结构化结果——共享服务输出（CLI 与 CH4 各自格式化）
    /// </summary>
    public sealed class MauGroupBuildResult
    {
        /// <summary>
        /// 流程是否成功（false 时 Error 或 FailDiagnostics 有内容）
        /// </summary>
        public bool Success;

        /// <summary>
        /// 致命错误（解析失败/无成员/编译拒绝/构建失败/异常）
        /// </summary>
        public string Error = "";

        /// <summary>
        /// 步骤日志行——组/版本/成员/落盘/构建输出（CLI 与工具结果共用）
        /// </summary>
        public List<string> Steps = new List<string>();

        /// <summary>
        /// 编译失败诊断——文件:行: 错误码: 消息（整组拒绝）
        /// </summary>
        public List<string> FailDiagnostics = new List<string>();

        /// <summary>
        /// dotnet build 成功的产物——Flows/FL_&lt;组&gt;.dll 绝对路径（未构建为空）
        /// </summary>
        public string DllPath = "";

        /// <summary>
        /// dotnet build 尾部输出（≤15 行；未构建为空）
        /// </summary>
        public string BuildOutput = "";
    }
}