using System;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Http;
using Mau.Runtime;

namespace CatHome4.Http
{
    /// <summary>
    /// HttpHost 端口面分部——A328 端口面收缩：console（主端口）全量，chat（对话端口）与 group（组端口）只放行各自最小端点集。
    /// 阻断语义是「君子协定级」：端点仍注册，但非本面请求一律 404 —— 让越界者立刻看到「这里没有这个工具」，
    /// 而不是靠凭据隐藏。设计权威：design-ch4-console-auth.md §三。
    /// </summary>
    public sealed partial class HttpHost
    {
        /// <summary>
        /// 端口面裁切中间件——非 console 面按最小集放行，其余 /api/v1 请求返回 404 并记日志（失败必须可见）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <param name="next">下一段管道</param>
        /// <returns>异步任务</returns>
        private async Task HttpSurfaceGate(HttpContext ctx, Func<Task> next)
        {
            if (!IsSurfaceBlocked(ctx.Request))
            {
                await next();
                return;
            }
            LogStore.Add("HttpSurface", 1, "端口面拒绝 | " + ctx.Request.Method + " " + (ctx.Request.Path.Value ?? "") + " | surface=" + CurrentSurfaceName(), "A328");
            ctx.Response.StatusCode = 404;
            ctx.Response.ContentType = "application/json; charset=utf-8";
            await ctx.Response.WriteAsync("{\"ok\":false,\"error\":\"SURFACE_ENDPOINT_ABSENT\"}");
        }

        /// <summary>
        /// 端点是否被端口面拦下——console 全放行；chat / group 走各自白名单（静态资源与非 /api/v1 请求一律放行）。
        /// </summary>
        /// <param name="req">HTTP 请求</param>
        /// <returns>true=拦截（404）</returns>
        private bool IsSurfaceBlocked(HttpRequest req)
        {
            if (_surfaceConsole)
            {
                return false;
            }
            string path = req.Path.Value ?? "";
            if (!path.StartsWith("/api/v1", StringComparison.Ordinal))
            {
                return false;
            }
            bool isGet = HttpMethods.IsGet(req.Method);
            if (_surfaceGroup)
            {
                if (path.StartsWith("/api/v1/group/", StringComparison.Ordinal))
                {
                    return false;
                }
                if (isGet && (string.Equals(path, "/api/v1/config", StringComparison.Ordinal) || string.Equals(path, "/api/v1/cats", StringComparison.Ordinal)))
                {
                    return false;
                }
                return true;
            }
            // chat 面——对话页实际消费的端点（发消息 / 实时流 / 图片 / 音频 / 字号配置只读）
            if (string.Equals(path, "/api/v1/snapshot", StringComparison.Ordinal))
            {
                return false;
            }
            if (string.Equals(path, "/api/v1/stream", StringComparison.Ordinal))
            {
                return false;
            }
            if (string.Equals(path, "/api/v1/command", StringComparison.Ordinal))
            {
                return false;
            }
            if (isGet && string.Equals(path, "/api/v1/config", StringComparison.Ordinal))
            {
                return false;
            }
            if (string.Equals(path, "/api/v1/chat-images", StringComparison.Ordinal))
            {
                return false;
            }
            if (path.StartsWith("/api/v1/cache-image/", StringComparison.Ordinal))
            {
                return false;
            }
            if (path.StartsWith("/api/v1/audio/", StringComparison.Ordinal))
            {
                return false;
            }
            return true;
        }

        /// <summary>
        /// 当前端口面名——日志与拒绝文案用。
        /// </summary>
        /// <returns>console / chat / group</returns>
        private string CurrentSurfaceName()
        {
            if (_surfaceGroup)
            {
                return "group";
            }
            if (_surfaceChat)
            {
                return "chat";
            }
            return "console";
        }

        /// <summary>
        /// 对话端口输入判定——对话页自用指令走白名单放行（停止 / 继续 / 新会话 / 回滚），
        /// 其余管理前缀一律拒（判据面 = chat/input.js 与 chat/blocks/text.js 的实际消费面，A328）。
        /// </summary>
        /// <param name="text">输入行</param>
        /// <returns>true=允许投递</returns>
        private static bool IsChatPortInputAllowed(string text)
        {
            if (text == null || text.Length == 0)
            {
                return true;
            }
            string head = text.TrimStart();
            string[] chatOwned = new string[]
            {
                "cat.pause", "cat.continue", "session.new", "session.rollback"
            };
            for (int i = 0; i < chatOwned.Length; i = i + 1)
            {
                if (head.StartsWith(chatOwned[i], StringComparison.OrdinalIgnoreCase))
                {
                    return true;
                }
            }
            return !IsManagedInput(head);
        }

        /// <summary>
        /// 管理指令判定——非 console 面拒绝的输入前缀（对话端口只服务对话输入与页内自用指令）。
        /// </summary>
        /// <param name="text">输入行</param>
        /// <returns>true=属管理指令</returns>
        private static bool IsManagedInput(string text)
        {
            if (text == null || text.Length == 0)
            {
                return false;
            }
            string[] prefixes = new string[]
            {
                "session ", "cat.", "reload", "status", "freeze", "restart", "kill",
                "config ", "delay ", "llm ", "mau ", "note ", "delegate "
            };
            string head = text.TrimStart();
            for (int i = 0; i < prefixes.Length; i = i + 1)
            {
                if (head.StartsWith(prefixes[i], StringComparison.OrdinalIgnoreCase))
                {
                    return true;
                }
            }
            return false;
        }
    }
}
