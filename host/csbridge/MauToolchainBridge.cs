using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using Mau.Development;
using Mau.Runtime;
using Mau.Translator;

namespace MauCsBridge;

/// <summary>
/// Mau 工具链桥——MauCat 三件（verify_raw / gen / proj）的进程内调用面。
///
/// 分工：Go 侧 MauService 负责参数面 / A214 头 / 正文格式化；本桥只回**结构化数据**
/// （target / flow / reports / diagnostics / steps / error），不拼工具回执文本。
/// 仓库根由启动参数注入（受控根 id=mau）——桥 cwd 在 bin 目录，向上探测不到 Mau.sln。
/// verify_raw 只作差分的 oracle（Go front vs C# 编译链诊断逐字对拍）；gen / proj 是
/// gen / proj 工具的执行面（进程内直调 MauGroupBuilder，结果由调用方格式化）。
/// </summary>
public static class MauToolchainBridge
{
    /// <summary>
    /// 受控根表（启动注入——受控根前缀解析面）
    /// </summary>
    private static List<WorkspaceConfig.RootEntry> _roots = new List<WorkspaceConfig.RootEntry>();

    /// <summary>
    /// 注入仓库根与受控根表——宿主启动时调用（受控根 id=mau → MauGroupBuilder / BrickIndex 静态根）
    /// </summary>
    /// <param name="roots">受控根表（--root id=path 解析结果）</param>
    public static void Configure(IEnumerable<WorkspaceConfig.RootEntry> roots)
    {
        _roots = new List<WorkspaceConfig.RootEntry>(roots);
        foreach (WorkspaceConfig.RootEntry root in _roots)
        {
            if (string.Equals(root.Id, "mau", StringComparison.OrdinalIgnoreCase))
            {
                MauGroupBuilder.SetWorkspaceRoot(root.Path);
                BrickIndex.SetWorkspaceRoot(root.Path);
                break;
            }
        }
    }

    /// <summary>
    /// 方法分派——verify_raw / gen / proj
    /// </summary>
    /// <param name="method">方法名</param>
    /// <param name="argsJson">参数 JSON（file / proj / build）</param>
    /// <param name="result">结构化结果 JSON 或 ERR| 错误文本</param>
    /// <returns>true=成功</returns>
    public static bool Invoke(string method, string argsJson, out string result)
    {
        switch (method)
        {
            case "verify_raw":
                return VerifyRaw(argsJson, out result);
            case "gen":
                return RunGroupBuild(argsJson, false, out result);
            case "proj":
                return RunGroupBuild(argsJson, true, out result);
        }
        result = "ERR|UNKNOWN_METHOD|" + method;
        return false;
    }

    /// <summary>
    /// verify_raw——全链检查结构化返回（Go front 差分的 oracle）
    /// </summary>
    /// <param name="argsJson">参数 JSON（file）</param>
    /// <param name="result">结构化结果 JSON</param>
    /// <returns>true=验证通过</returns>
    private static bool VerifyRaw(string argsJson, out string result)
    {
        result = "";
        string file = JsonArgs.Get(argsJson, "file");
        if (file.Length == 0)
        {
            result = FailJson("BAD_ARGS", "缺少参数 file");
            return false;
        }
        try
        {
            string root = MauGroupBuilder.FindWorkspaceRoot() ?? "";
            if (root.Length == 0)
            {
                result = FailJson("NO_REPO", "未找到仓库根（Mau.sln 向上探测）");
                return false;
            }
            string abs = ResolveRepoPath(root, file);
            if (abs.Length == 0)
            {
                result = FailJson("PATH_ESCAPE", "路径越界（仅允许仓库根内）: " + file);
                return false;
            }
            if (!File.Exists(abs))
            {
                result = FailJson("NOT_FOUND", "文件不存在: " + abs);
                return false;
            }
            string source = File.ReadAllText(abs);
            string flowName = MauGroupBuilder.FlowNameFromPath(abs);
            CompileResultV3 r = MauCompilerV3.Compile(source, flowName);
            Dictionary<string, object> payload = new Dictionary<string, object>();
            payload["ok"] = r.Success;
            payload["target"] = file;
            payload["flow"] = "FL_" + flowName;
            List<string> reports = new List<string>(r.Reports);
            payload["reports"] = reports;
            List<Dictionary<string, object>> diagnostics = new List<Dictionary<string, object>>();
            for (int i = 0; i < r.Diagnostics.Count; i++)
            {
                MauDiagnostic d = r.Diagnostics[i];
                Dictionary<string, object> item = new Dictionary<string, object>();
                item["line"] = d.Line;
                item["code"] = d.Code;
                item["message"] = d.Message;
                diagnostics.Add(item);
            }
            payload["diagnostics"] = diagnostics;
            result = JsonSerializer.Serialize(payload);
            return r.Success;
        }
        catch (Exception ex)
        {
            result = FailJson(ex.GetType().Name, ex.Message);
            return false;
        }
    }

    /// <summary>
    /// 组翻译共享执行——gen / proj（MauProjFile.Load → MauGroupBuilder.Build）
    /// </summary>
    /// <param name="argsJson">参数 JSON（proj / build）</param>
    /// <param name="doBuild">true=翻译后 dotnet build</param>
    /// <param name="result">结构化结果 JSON</param>
    /// <returns>true=成功</returns>
    private static bool RunGroupBuild(string argsJson, bool doBuild, out string result)
    {
        result = "";
        string projParam = JsonArgs.Get(argsJson, "proj");
        if (projParam.Length == 0)
        {
            result = FailJson("BAD_ARGS", "缺少参数 proj");
            return false;
        }
        try
        {
            string root = MauGroupBuilder.FindWorkspaceRoot() ?? "";
            if (root.Length == 0)
            {
                result = FailJson("NO_REPO", "未找到仓库根（Mau.sln 向上探测）");
                return false;
            }
            string abs = ResolveRepoPath(root, projParam);
            if (abs.Length == 0)
            {
                result = FailJson("PATH_ESCAPE", "路径越界（仅允许仓库根内）: " + projParam);
                return false;
            }
            if (!File.Exists(abs))
            {
                result = FailJson("NOT_FOUND", "文件不存在: " + abs);
                return false;
            }
            MauProjParseResult parsed = MauProjFile.Load(abs);
            if (parsed.Error.Length > 0)
            {
                result = FailJson("MAUPROJ", parsed.Error);
                return false;
            }
            MauProjFile proj = parsed.File!;
            string srcDir = Path.Combine(root, "public", "src", proj.Name);
            string dllDir = Path.Combine(root, "public", "app", "Flows");
            MauGroupBuildResult r = MauGroupBuilder.Build(abs, srcDir, dllDir, doBuild);
            Dictionary<string, object> payload = new Dictionary<string, object>();
            payload["ok"] = r.Success;
            payload["target"] = proj.Name;
            payload["steps"] = new List<string>(r.Steps);
            payload["diagnostics"] = new List<string>(r.FailDiagnostics);
            payload["error"] = r.Error;
            payload["dll"] = r.DllPath;
            result = JsonSerializer.Serialize(payload);
            return r.Success;
        }
        catch (Exception ex)
        {
            result = FailJson(ex.GetType().Name, ex.Message);
            return false;
        }
    }

    /// <summary>
    /// 路径解析——受控根前缀（id:relative）按受控根表展开，无前缀按仓库根拼接；统一验证在仓库根内
    /// </summary>
    /// <param name="root">仓库根（受控根 id=mau 的绝对路径）</param>
    /// <param name="relPath">路径参数（支持 mau: 前缀）</param>
    /// <returns>绝对路径（越界 / 非法返回空串）</returns>
    private static string ResolveRepoPath(string root, string relPath)
    {
        string full;
        int sep = relPath.IndexOf(':');
        if (sep > 0)
        {
            string id = relPath.Substring(0, sep);
            string rest = relPath.Substring(sep + 1);
            string rootPath = "";
            for (int i = 0; i < _roots.Count; i++)
            {
                if (string.Equals(_roots[i].Id, id, StringComparison.OrdinalIgnoreCase))
                {
                    rootPath = _roots[i].Path;
                    break;
                }
            }
            if (rootPath.Length == 0)
            {
                return "";
            }
            try
            {
                full = Path.GetFullPath(Path.Combine(rootPath, rest.Replace('/', Path.DirectorySeparatorChar)));
            }
            catch (Exception)
            {
                return "";
            }
            if (!InRoot(rootPath, full))
            {
                return "";
            }
        }
        else
        {
            try
            {
                full = Path.GetFullPath(Path.Combine(root, relPath));
            }
            catch (Exception)
            {
                return "";
            }
        }
        if (!InRoot(root, full))
        {
            return "";
        }
        return full;
    }

    /// <summary>
    /// 归属判定——full 在 root 内（含 root 自身；Windows 大小写不敏感）
    /// </summary>
    /// <param name="root">根目录</param>
    /// <param name="full">绝对路径</param>
    /// <returns>true=在根内</returns>
    private static bool InRoot(string root, string full)
    {
        string normalized = Path.GetFullPath(root);
        if (!normalized.EndsWith(Path.DirectorySeparatorChar.ToString(), StringComparison.Ordinal))
        {
            normalized = normalized + Path.DirectorySeparatorChar;
        }
        if (string.Equals(full, Path.GetFullPath(root), StringComparison.OrdinalIgnoreCase))
        {
            return true;
        }
        return full.StartsWith(normalized, StringComparison.OrdinalIgnoreCase);
    }

    /// <summary>
    /// 失败结构化返回——kind 标记失败面（BAD_ARGS / NO_REPO / PATH_ESCAPE / NOT_FOUND / MAUPROJ / EXCEPTION）
    /// </summary>
    /// <param name="kind">失败面</param>
    /// <param name="message">错误文本</param>
    /// <returns>单行 JSON</returns>
    private static string FailJson(string kind, string message)
    {
        Dictionary<string, object> payload = new Dictionary<string, object>();
        payload["ok"] = false;
        payload["kind"] = kind;
        payload["error"] = message;
        return JsonSerializer.Serialize(payload);
    }
}
