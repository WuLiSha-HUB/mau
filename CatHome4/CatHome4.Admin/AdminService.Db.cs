using System;
using System.Collections.Generic;
using System.Text.Json;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Http;
using Mau.Runtime;

namespace CatHome4.Admin
{
    /// <summary>
    /// Admin 域数据库（Smriti）浏览面（A269）——库清单 / 建库 / 条目查询 / 条目全文四端点。
    /// 写入一律走 db-* 工具面（本面只读 + 建库），杜绝两套写路径。
    /// 服务实例经 DataBox 取用（S2 段绑定；未绑定 = 库根不可用，走启动降级面）。
    /// </summary>
    internal static partial class AdminService
    {
        // [段1] 端点

        /// <summary>
        /// 库清单——GET /api/v1/db/libs（注册表登记的库 + 活跃条目计数）。
        /// </summary>
        /// <returns>回执 JSON——ok + data.libs</returns>
        internal static IResult HandleDbLibsGet()
        {
            DbService service = ResolveDbService();
            if (service == null)
            {
                return DbServiceUnavailable();
            }
            // 管理面：无猫上下文（空 catId = 全放）
            DbResult result = service.ListLibs("");
            if (!result.Ok)
            {
                return DbServiceFail(result);
            }
            JsonElement payload;
            if (!TryReadDbPayload(result.Data, out payload))
            {
                return DbPayloadBroken();
            }
            return Results.Json(new
            {
                ok = true,
                message = result.Message,
                data = payload,
                badLines = result.BadLines
            });
        }

        /// <summary>
        /// 新建库——POST /api/v1/db/libs（body: {"id":"&lt;libId&gt;"}）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON——ok + data.id / data.createdAt</returns>
        internal static async Task<IResult> HandleDbLibsPost(HttpContext ctx)
        {
            DbService service = ResolveDbService();
            if (service == null)
            {
                return DbServiceUnavailable();
            }
            string body = await ReadBodyText(ctx);
            string libId = "";
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    libId = GetJsonString(doc.RootElement, "id");
                }
            }
            catch (Exception ex)
            {
                return Results.Json(new
                {
                    ok = false,
                    code = "BAD_ARGS",
                    message = "请求体解析失败: " + ex.Message
                });
            }
            DbResult result = service.Create(libId, "");
            if (!result.Ok)
            {
                return DbServiceFail(result);
            }
            JsonElement payload;
            if (!TryReadDbPayload(result.Data, out payload))
            {
                return DbPayloadBroken();
            }
            return Results.Json(new
            {
                ok = true,
                message = result.Message,
                data = payload,
                badLines = result.BadLines
            });
        }

        /// <summary>
        /// 条目列表——GET /api/v1/db/libs/{lib}/entries?keyword=&amp;limit=（与 db-query 同语义）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON——ok + data（total / matched / shown / items）</returns>
        internal static IResult HandleDbEntriesGet(HttpContext ctx)
        {
            DbService service = ResolveDbService();
            if (service == null)
            {
                return DbServiceUnavailable();
            }
            string lib = ReadRouteValue(ctx, "lib");
            string keyword = ReadQueryText(ctx, "keyword");
            string limitText = ReadQueryText(ctx, "limit");
            int limit = 0;
            if (limitText.Length > 0)
            {
                if (!int.TryParse(limitText, out limit))
                {
                    return Results.Json(new
                    {
                        ok = false,
                        code = "BAD_ARGS",
                        message = "limit 非法（数字）: " + limitText
                    });
                }
            }
            DbResult result = service.Query(lib, keyword, limit, "");
            if (!result.Ok)
            {
                return DbServiceFail(result);
            }
            JsonElement payload;
            if (!TryReadDbPayload(result.Data, out payload))
            {
                return DbPayloadBroken();
            }
            return Results.Json(new
            {
                ok = true,
                message = result.Message,
                data = payload,
                badLines = result.BadLines
            });
        }

        /// <summary>
        /// 条目全文——GET /api/v1/db/libs/{lib}/entries/{entryId}（与 db-get 按 ID 同语义）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON——ok + data（完整条目）</returns>
        internal static IResult HandleDbEntryGet(HttpContext ctx)
        {
            DbService service = ResolveDbService();
            if (service == null)
            {
                return DbServiceUnavailable();
            }
            string lib = ReadRouteValue(ctx, "lib");
            string entryId = ReadRouteValue(ctx, "entryId");
            DbResult result = service.Get(lib, "id", entryId, "");
            if (!result.Ok)
            {
                return DbServiceFail(result);
            }
            JsonElement payload;
            if (!TryReadDbPayload(result.Data, out payload))
            {
                return DbPayloadBroken();
            }
            return Results.Json(new
            {
                ok = true,
                message = result.Message,
                data = payload,
                badLines = result.BadLines
            });
        }

        // [段3] 猫配置面

        /// <summary>
        /// 库池清单——猫配置弹层「库权限」勾选面消费（A275：库 id + 活跃条目数 + 坏库标记）。
        /// </summary>
        /// <param name="available">输出：清单是否可取（服务未绑定 / 注册表不可读 → false——调用方须出声，不静默空列表）</param>
        /// <returns>库项列表（不可取 → 空列表）</returns>
        internal static List<object> BuildAllDbLibs(out bool available)
        {
            available = false;
            List<object> list = new List<object>();
            DbService service = ResolveDbService();
            if (service == null)
            {
                return list;
            }
            // 管理面：无猫上下文（空 catId = 全放）
            DbResult result = service.ListLibs("");
            if (!result.Ok)
            {
                return list;
            }
            JsonElement payload;
            if (!TryReadDbPayload(result.Data, out payload))
            {
                return list;
            }
            JsonElement libsEl;
            if (!payload.TryGetProperty("libs", out libsEl) || libsEl.ValueKind != JsonValueKind.Array)
            {
                return list;
            }
            available = true;
            for (int i = 0; i < libsEl.GetArrayLength(); i = i + 1)
            {
                JsonElement item = libsEl[i];
                string id = "";
                int entries = 0;
                bool broken = false;
                JsonElement idEl;
                if (item.TryGetProperty("id", out idEl) && idEl.ValueKind == JsonValueKind.String)
                {
                    id = idEl.GetString();
                }
                if (id == null || id.Length == 0)
                {
                    continue;
                }
                JsonElement entriesEl;
                if (item.TryGetProperty("entries", out entriesEl) && entriesEl.ValueKind == JsonValueKind.Number)
                {
                    entries = entriesEl.GetInt32();
                }
                JsonElement brokenEl;
                if (item.TryGetProperty("broken", out brokenEl))
                {
                    broken = brokenEl.ValueKind == JsonValueKind.True;
                }
                list.Add(new
                {
                    id = id,
                    entries = entries,
                    broken = broken
                });
            }
            return list;
        }

        // [段2] 辅助

        /// <summary>
        /// 取数据库服务实例——未绑定返回 null（库根不可用时的降级形态）。
        /// </summary>
        /// <returns>服务实例 / null</returns>
        private static DbService ResolveDbService()
        {
            DbService service = null;
            DataBox.TryResolve<DbService>(out service);
            return service;
        }

        /// <summary>
        /// 服务未绑定回执——库根不可用（启动健康页有降级登记）。
        /// </summary>
        /// <returns>回执 JSON</returns>
        private static IResult DbServiceUnavailable()
        {
            return Results.Json(new
            {
                ok = false,
                code = "DB_SERVICE_UNAVAILABLE",
                message = "数据库服务未绑定——库根不可用（见启动健康页）"
            });
        }

        /// <summary>
        /// 服务失败回执——机器码 + 中文原因。
        /// </summary>
        /// <param name="result">服务结果</param>
        /// <returns>回执 JSON</returns>
        private static IResult DbServiceFail(DbResult result)
        {
            return Results.Json(new
            {
                ok = false,
                code = result.Code,
                message = result.Message
            });
        }

        /// <summary>
        /// 载荷解析失败回执——服务声称成功但载荷不可读（不静默空列表）。
        /// </summary>
        /// <returns>回执 JSON</returns>
        private static IResult DbPayloadBroken()
        {
            return Results.Json(new
            {
                ok = false,
                code = "DB_IO_FAIL",
                message = "服务载荷解析失败"
            });
        }

        /// <summary>
        /// 解析服务层载荷——统一入口（JsonUtil），失败返回 false 且不抛。
        /// </summary>
        /// <param name="data">载荷 JSON 文本</param>
        /// <param name="value">输出：载荷对象（已 Clone 脱离文档生命周期）</param>
        /// <returns>是否成功</returns>
        private static bool TryReadDbPayload(string data, out JsonElement value)
        {
            value = default(JsonElement);
            if (data == null || data.Length == 0)
            {
                return false;
            }
            JsonDocument doc = JsonUtil.Parse(data, "db.payload");
            if (doc == null)
            {
                return false;
            }
            using (doc)
            {
                JsonElement root = doc.RootElement;
                if (root.ValueKind != JsonValueKind.Object)
                {
                    return false;
                }
                value = root.Clone();
                return true;
            }
        }

        /// <summary>
        /// 读路由段值——缺失返回空串。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <param name="key">段名</param>
        /// <returns>段值</returns>
        private static string ReadRouteValue(HttpContext ctx, string key)
        {
            object raw;
            if (ctx.Request.RouteValues.TryGetValue(key, out raw))
            {
                if (raw != null)
                {
                    return raw.ToString();
                }
            }
            return "";
        }

        /// <summary>
        /// 读查询串参数——缺失返回空串。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <param name="key">参数名</param>
        /// <returns>参数值</returns>
        private static string ReadQueryText(HttpContext ctx, string key)
        {
            return ctx.Request.Query[key].ToString();
        }
    }
}
