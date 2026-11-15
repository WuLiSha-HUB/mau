using System;
using System.Text.Json;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Http;
using Mau.Contracts;
using Mau.Runtime;

namespace CatHome4.Admin
{
    /// <summary>
    /// Admin 域 QQ 群目录面（A380）——前端配置页的群清单与目录维护端点。
    /// 数据面 = <c>Data/qq/bots/&lt;appId&gt;/groups.json</c>（收信自动登记 + 官方接口刷权限）；
    /// 本面只做读写转发，业务规则（别名唯一 / 白名单闸）在 <see cref="IQQBotOps"/> 实现侧。
    /// </summary>
    internal static partial class AdminService
    {
        /// <summary>
        /// 群目录列表——GET /api/v1/qqbot-groups?botKey=&lt;qqBotId&gt;（空=池内默认机器人）。
        /// 成功返回目录 JSON（ok / appId / bot / groups[]）；失败返回 { ok:false, error }。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>目录 JSON</returns>
        internal static IResult HandleQqBotGroupsGet(HttpContext ctx)
        {
            string botKey = ctx.Request.Query["botKey"].ToString();
            IQQBotOps ops = null;
            DataBox.TryResolve<IQQBotOps>(out ops);
            if (ops == null)
            {
                return Results.Json(new { ok = false, error = "QQBot 业务面未注入（宿主未完成装配）" });
            }
            string json = ops.DirectoryJson(botKey);
            if (json.StartsWith("ERR|", StringComparison.Ordinal))
            {
                return Results.Json(new { ok = false, error = json });
            }
            return Results.Content(json, "application/json; charset=utf-8");
        }

        /// <summary>
        /// 设别名——POST /api/v1/qqbot-groups/alias（body: botKey / groupRef / alias）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleQqBotGroupsAlias(HttpContext ctx)
        {
            return await HandleQqBotGroupsOp(ctx, "alias");
        }

        /// <summary>
        /// 登记群——POST /api/v1/qqbot-groups/add（body: botKey / groupRef=群 OpenID）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        internal static async Task<IResult> HandleQqBotGroupsAdd(HttpContext ctx)
        {
            return await HandleQqBotGroupsOp(ctx, "add");
        }

        /// <summary>
        /// 刷新官方状态——POST /api/v1/qqbot-groups/refresh（body: botKey；逐群刷 bot_state + info）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON（text 段为逐群结果）</returns>
        internal static async Task<IResult> HandleQqBotGroupsRefresh(HttpContext ctx)
        {
            return await HandleQqBotGroupsOp(ctx, "refresh");
        }

        /// <summary>
        /// 目录操作公共段——三个 POST 端点共用（解析 body → 调 IQQBotOps.GroupsOp → 统一回执）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <param name="action">动作（alias / add / refresh）</param>
        /// <returns>回执 JSON</returns>
        private static async Task<IResult> HandleQqBotGroupsOp(HttpContext ctx, string action)
        {
            string body = await ReadBodyText(ctx);
            string botKey = "";
            string groupRef = "";
            string alias = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    botKey = GetJsonString(root, "botKey");
                    groupRef = GetJsonString(root, "groupRef");
                    if (groupRef.Length == 0)
                    {
                        groupRef = GetJsonString(root, "groupOpenId");
                    }
                    alias = GetJsonString(root, "alias");
                }
            }
            catch (Exception)
            {
                return Results.Json(new { ok = false, error = "body 非 JSON" });
            }
            IQQBotOps ops = null;
            DataBox.TryResolve<IQQBotOps>(out ops);
            if (ops == null)
            {
                return Results.Json(new { ok = false, error = "QQBot 业务面未注入（宿主未完成装配）" });
            }
            string text = ops.GroupsOp(botKey, action, groupRef, alias);
            bool failed = text.StartsWith("ERR|", StringComparison.Ordinal);
            LogStore.Add("CatHome4", failed ? 2 : 1, "qqbot-groups " + action + " → " + OneLineShort(text), "CONFIG");
            return Results.Json(new { ok = !failed, text = text, error = failed ? text : "" });
        }

        /// <summary>
        /// 回执单行化——日志用（换行替换空格，超长截断）。
        /// </summary>
        /// <param name="text">原文</param>
        /// <returns>单行摘要</returns>
        private static string OneLineShort(string text)
        {
            if (text == null)
            {
                return "";
            }
            string one = text.Replace("\r", " ").Replace("\n", " ");
            if (one.Length > 160)
            {
                one = one.Substring(0, 160) + "…";
            }
            return one;
        }
    }
}
