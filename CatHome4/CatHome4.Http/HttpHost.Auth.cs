using System;
using System.Text.Json;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Http;
using Mau.Runtime;

namespace CatHome4.Http
{
    /// <summary>
    /// HttpHost 认证面分部——主端口 console 口令校验中间件 + 四个认证端点（A328）。
    /// 启用条件：Start 时注入 ConsoleAuth 实例（仅主端口注入；每猫对话端口与组端口不注入 = 不校验）。
    /// 设计权威：design-ch4-console-auth.md。
    /// </summary>
    public sealed partial class HttpHost
    {
        /// <summary>
        /// 认证闸门中间件——非 /api/v1 请求与认证入口放行；其余须持有效会话令牌。
        /// 未设置口令（auth.console_hash 为空）时全放行——与既有实例行为一致，升级不锁死。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <param name="next">下一段管道</param>
        /// <returns>异步任务</returns>
        private async Task ConsoleAuthGate(HttpContext ctx, Func<Task> next)
        {
            string path = ctx.Request.Path.Value ?? "";
            if (!path.StartsWith("/api/v1", StringComparison.Ordinal))
            {
                await next();
                return;
            }
            bool open = string.Equals(path, "/api/v1/auth/status", StringComparison.Ordinal)
                || string.Equals(path, "/api/v1/auth/login", StringComparison.Ordinal);
            bool hasPassword = _consoleAuth.HasPassword();
            string token = ExtractConsoleToken(ctx);
            bool pass = open || !hasPassword || _consoleAuth.IsAuthenticated(token);
            // 认证面请求留痕——响应与结果不一致时（如写入成功却回 401）靠这条日志定界
            if (path.StartsWith("/api/v1/auth", StringComparison.Ordinal))
            {
                LogStore.Add("HttpAuth", 1, "gate | " + ctx.Request.Method + " " + path + " | hasPwd=" + (hasPassword ? "1" : "0") + " | tok=" + (token.Length > 0 ? "1" : "0") + " | " + (pass ? "pass" : "deny"), "A328");
            }
            if (pass)
            {
                await next();
                return;
            }
            ctx.Response.StatusCode = 401;
            ctx.Response.ContentType = "application/json; charset=utf-8";
            await ctx.Response.WriteAsync("{\"ok\":false,\"error\":\"CONSOLE_AUTH_REQUIRED\"}");
        }

        /// <summary>
        /// 认证状态——GET /api/v1/auth/status（放行）。前端据此决定显示「设置口令」还是「输入口令」。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON——hasPassword / authenticated</returns>
        private IResult HandleAuthStatus(HttpContext ctx)
        {
            bool hasPassword = _consoleAuth.HasPassword();
            bool authenticated = hasPassword && _consoleAuth.IsAuthenticated(ExtractConsoleToken(ctx));
            return Results.Json(new
            {
                ok = true,
                hasPassword = hasPassword,
                authenticated = authenticated
            });
        }

        /// <summary>
        /// 登录——POST /api/v1/auth/login（body: {"password":"..."}）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON——ok/token 或 401</returns>
        private async Task<IResult> HandleAuthLogin(HttpContext ctx)
        {
            string password = await ReadAuthPassword(ctx).ConfigureAwait(false);
            string token = _consoleAuth.Login(password);
            if (token.Length == 0)
            {
                return Results.Json(new { ok = false, error = "口令错误" }, statusCode: 401);
            }
            return Results.Json(new { ok = true, token = token });
        }

        /// <summary>
        /// 设置 / 修改口令——POST /api/v1/auth/setup（body: {"password":"..."}）。
        /// 未设置口令时放行（首次）；已设置时须持有效令牌（改口令），由中间件拦住。
        /// 设置成功即签发令牌，免二次登录。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON——ok/token 或错误</returns>
        private async Task<IResult> HandleAuthSetup(HttpContext ctx)
        {
            string password = await ReadAuthPassword(ctx).ConfigureAwait(false);
            string error;
            if (!_consoleAuth.SetPassword(password, out error))
            {
                return Results.Json(new { ok = false, error = error });
            }
            // A328——改即生效：旧会话令牌全部作废，随后为本次调用签发新令牌（当前页面不中断）
            _consoleAuth.ClearTokens();
            string token = _consoleAuth.Login(password);
            if (token.Length == 0)
            {
                return Results.Json(new { ok = false, error = "口令已写入但登录失败——请重新登录" });
            }
            return Results.Json(new { ok = true, token = token });
        }

        /// <summary>
        /// 登出——POST /api/v1/auth/logout（须已认证，由中间件保证）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON——ok</returns>
        private IResult HandleAuthLogout(HttpContext ctx)
        {
            _consoleAuth.Logout(ExtractConsoleToken(ctx));
            return Results.Json(new { ok = true });
        }

        /// <summary>
        /// 提取会话令牌——Authorization: Bearer → X-Console-Token → query token（SSE 用，EventSource 不能设头）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>令牌（空=无）</returns>
        private static string ExtractConsoleToken(HttpContext ctx)
        {
            string auth = ctx.Request.Headers["Authorization"].ToString();
            if (auth.Length > 7 && auth.StartsWith("Bearer ", StringComparison.OrdinalIgnoreCase))
            {
                return auth.Substring(7).Trim();
            }
            string custom = ctx.Request.Headers["X-Console-Token"].ToString();
            if (custom.Length > 0)
            {
                return custom.Trim();
            }
            string query = ctx.Request.Query["token"].ToString();
            if (query.Length > 0)
            {
                return query.Trim();
            }
            return "";
        }

        /// <summary>
        /// 读取认证请求体里的 password 字段——非 JSON / 缺字段返回空串（走失败分支，不抛异常）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>口令明文（空=未提供）</returns>
        private static async Task<string> ReadAuthPassword(HttpContext ctx)
        {
            string body;
            using (System.IO.StreamReader reader = new System.IO.StreamReader(ctx.Request.Body))
            {
                body = await reader.ReadToEndAsync().ConfigureAwait(false);
            }
            if (body.Length == 0)
            {
                return "";
            }
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    if (root.TryGetProperty("password", out JsonElement p))
                    {
                        return p.GetString() ?? "";
                    }
                }
            }
            catch (Exception)
            {
                return "";
            }
            return "";
        }
    }
}
