using System.Reflection;
using System.Text.Json;
using Mau.Development;
using Mau.Runtime;

namespace MauCsBridge;

/// <summary>
/// P0 cs 桥外置服务——独立 .NET 进程承载 Roslyn/MSBuild 工作区。
/// 契约：POST /invoke {method,argsJson} → {ok,result}；GET /health → 状态。
/// 边界：只读引用 mauout 的 Mau.* 程序集，不回写 mau / CH4 仓库。
/// </summary>
public static class Program
{
    private static readonly string[] Methods =
    {
        "check", "build", "test", "list", "read", "find_ref",
        "find", "patch", "member", "comment", "dead", "comment_check", "format",
        "verify_raw", "gen", "proj"
    };

    /// <summary>
    /// 入口——解析 --port / --root id=path，装配 MauRoslynBridge 并起 HTTP 面。
    /// </summary>
    public static int Main(string[] args)
    {
        Console.OutputEncoding = System.Text.Encoding.UTF8;
        // 依赖解析兜底——输出目录同名 dll 显式解析（Roslyn 4.11 依赖链在非标准加载上下文下的加载失败防护）
        AppDomain.CurrentDomain.AssemblyResolve += (sender, e) =>
        {
            string name = new AssemblyName(e.Name).Name;
            if (string.IsNullOrEmpty(name))
            {
                return null;
            }
            string path = Path.Combine(AppContext.BaseDirectory, name + ".dll");
            return File.Exists(path) ? Assembly.LoadFrom(path) : null;
        };

        int port = 8130;
        List<WorkspaceConfig.RootEntry> roots = new List<WorkspaceConfig.RootEntry>();
        for (int i = 0; i < args.Length; i++)
        {
            if (args[i] == "--port" && i + 1 < args.Length)
            {
                int.TryParse(args[i + 1], out port);
                i++;
            }
            else if (args[i] == "--root" && i + 1 < args.Length)
            {
                string spec = args[i + 1];
                i++;
                int eq = spec.IndexOf('=');
                if (eq > 0)
                {
                    roots.Add(new WorkspaceConfig.RootEntry
                    {
                        Id = spec.Substring(0, eq),
                        Path = spec.Substring(eq + 1),
                        Writable = true
                    });
                }
            }
        }
        if (roots.Count == 0)
        {
            roots.Add(new WorkspaceConfig.RootEntry
            {
                Id = "workspace",
                Path = "D:\\Mau\\WorkSpace",
                Writable = true
            });
        }

        MauRoslynBridge bridge = new MauRoslynBridge(roots.ToArray());
        // Mau 工具链桥——受控根 id=mau 注入 MauGroupBuilder / BrickIndex 静态根（MauCat gen/proj 面）
        MauToolchainBridge.Configure(roots);

        WebApplicationBuilder builder = WebApplication.CreateBuilder(new string[0]);
        builder.Logging.ClearProviders();
        builder.WebHost.UseUrls("http://127.0.0.1:" + port.ToString());
        WebApplication app = builder.Build();

        app.MapGet("/health", () => Results.Json(new
        {
            ok = true,
            service = "mau-csbridge",
            version = "0.1.0",
            pid = Environment.ProcessId,
            roots = roots.ConvertAll(r => new { id = r.Id, path = r.Path }),
            methods = Methods
        }));

        app.MapPost("/invoke", async (HttpRequest request) =>
        {
            JsonDocument doc;
            try
            {
                doc = await JsonDocument.ParseAsync(request.Body);
            }
            catch (Exception ex)
            {
                return Results.Json(new { ok = false, result = "ERR|BAD_JSON|" + ex.Message });
            }
            using (doc)
            {
                JsonElement root = doc.RootElement;
                string method = root.TryGetProperty("method", out JsonElement m) ? (m.GetString() ?? "") : "";
                string argsJson = root.TryGetProperty("argsJson", out JsonElement a) ? (a.GetString() ?? "{}") : "{}";
                if (method.Length == 0)
                {
                    return Results.Json(new { ok = false, result = "ERR|BAD_ARGS|缺少 method" });
                }
                bool ok;
                string result;
                if (method == "format")
                {
                    // 桥侧本地 format——MefHostServices 显式装配 C# 格式化服务（mauout 无 Features 程序集，
                    // MauRoslynBridge.Format 的 AdhocWorkspace 默认 host 报「语言"C#"不受支持」）
                    try
                    {
                        ok = LocalFormat.Invoke(argsJson, roots.ToArray(), out result);
                    }
                    catch (Exception ex)
                    {
                        ok = false;
                        result = "ERR|EX|" + ex.GetType().Name + "|" + (ex.Message + " " + ex.StackTrace);
                    }
                }
                else if (method == "verify_raw" || method == "gen" || method == "proj")
                {
                    ok = MauToolchainBridge.Invoke(method, argsJson, out result);
                }
                else
                {
                    ok = bridge.Invoke(method, argsJson, out result);
                }
                return Results.Json(new { ok, result });
            }
        });

        app.Run();
        return 0;
    }
}
