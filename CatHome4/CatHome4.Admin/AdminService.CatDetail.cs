using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using Microsoft.AspNetCore.Http;
using Mau.Runtime;
using CH4;

namespace CatHome4.Admin
{
    /// <summary>
    /// Program 猫详情面分部——会话状态卡点击后的详情数据源（GET /api/v1/cat-detail）。
    /// 读面三源：状态字段（与 cat.info 同源——BuildCatInfoEntry 单一实现）+ 末条 user/text 视图块（内存真源）+ sessions_old 留档（上一会话回落）。
    /// 线程模型：HTTP 线程直读（视图块数组与留档文件均只读——同 /api/v1/history 先例；不触注册表与会话操纵面）。
    /// </summary>
    internal static partial class AdminService
    {
        /// <summary>
        /// 猫详情——GET /api/v1/cat-detail?cat=&lt;key&gt;（会话状态卡点击后的详情弹层数据源）。
        /// 返回：状态字段 + 本会话末条 user / 回复 + 该猫最近一份会话留档全文。
        /// 本会话无对应消息时回落留档末条（source=archive——前端标注「上一会话」）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文（cat=猫寻址键：id / 显示名 / id 前缀；majordomo=主干）</param>
        /// <returns>详情 JSON（整块——缩进 + 中文直显同 cat.info 口径）</returns>
        internal static IResult HandleCatDetail(HttpContext ctx)
        {
            string catKey = ctx.Request.Query["cat"].ToString();
            if (catKey.Length == 0)
            {
                return Results.Json(new { ok = false, error = "cat 参数为空" });
            }
            // 边界校验——catKey 不承载路径语义（与 cat-config 端点同口径）
            if (!IsSafeCatKey(catKey))
            {
                return Results.Json(new { ok = false, error = "cat 参数非法: " + catKey });
            }
            bool special = string.Equals(catKey, "majordomo", StringComparison.Ordinal);
            string id = catKey;
            string name = catKey;
            bool running = false;
            int port = 0;
            ChatSession session = null;
            if (special)
            {
                running = CatHostService.MajorHostRunning;
                port = CatHostService.MajorPort;
                if (_chatBridge != null)
                {
                    session = _chatBridge.DefaultSession;
                    if (session != null)
                    {
                        name = session.DisplayName;
                    }
                }
            }
            else
            {
                CatHostService.CatEntry cat = CatHostService.FindCat(catKey);
                if (cat == null)
                {
                    return Results.Json(new { ok = false, error = "未找到猫: " + catKey });
                }
                id = cat.Id;
                name = cat.DisplayName;
                running = cat.Running;
                port = cat.Port;
                session = cat.Session;
            }
            // [段1] 状态字段——与 cat.info 同源（BuildCatInfoEntry 单一实现，不另起数据面）
            Dictionary<string, object> resp = CatHostService.BuildCatInfoEntry(id, name, running, port, special, session, "");
            resp["ok"] = true;
            resp["cat"] = id;
            resp["isIdle"] = session == null || session.IsIdle;
            // [段2] 本会话末条 user / 回复——视图块内存真源（零文件 IO）
            ViewBlock[] blocks = new ViewBlock[0];
            if (session != null)
            {
                blocks = session.GetViewBlocks();
            }
            Dictionary<string, object> lastUser = FindLastViewBlock(blocks, "user");
            Dictionary<string, object> lastReply = FindLastViewBlock(blocks, "text");
            // [段3] 上一会话回落——sessions_old 该猫最近一份留档（本会话无对应消息时取档内末条）
            string archiveFile = SessionViewStore.FindLatestArchive(Path.Combine(_dataRoot, "Data", "sessions_old"), id);
            string archiveText = "";
            if (archiveFile.Length > 0)
            {
                try
                {
                    archiveText = File.ReadAllText(archiveFile);
                }
                catch (Exception ex)
                {
                    // 读取失败不阻断详情（失败可见——WARN 日志 + 视为无留档）
                    LogStore.Add("CatHome4", 2, "会话留档读取失败: " + ex.Message, "CHAT");
                    archiveFile = "";
                }
            }
            if (lastUser == null && archiveText.Length > 0)
            {
                lastUser = FindLegacyEntry(archiveText, "用户");
            }
            if (lastReply == null && archiveText.Length > 0)
            {
                lastReply = FindLegacyEntry(archiveText, "回复");
            }
            resp["lastUser"] = lastUser;
            resp["lastReply"] = lastReply;
            if (archiveFile.Length > 0)
            {
                Dictionary<string, object> archive = new Dictionary<string, object>();
                archive["file"] = Path.GetFileName(archiveFile);
                archive["path"] = archiveFile;
                archive["text"] = archiveText;
                resp["archive"] = archive;
            }
            return Results.Text(JsonSerializer.Serialize(resp, CatHostService.InfoJsonOptions), "application/json");
        }

        /// <summary>
        /// 末条指定类型视图块——倒序扫描（内容为空 / 载荷解析失败则继续前一条）。
        /// </summary>
        /// <param name="blocks">视图块数组（可空）</param>
        /// <param name="renderType">块渲染类型（user / text）</param>
        /// <returns>条目字典（无命中=null）</returns>
        private static Dictionary<string, object> FindLastViewBlock(ViewBlock[] blocks, string renderType)
        {
            if (blocks == null)
            {
                return null;
            }
            for (int i = blocks.Length - 1; i >= 0; i = i - 1)
            {
                ViewBlock b = blocks[i];
                if (b == null || !string.Equals(b.RenderType, renderType, StringComparison.Ordinal))
                {
                    continue;
                }
                string content = ReadBlockContent(b.Payload);
                if (content.Length == 0)
                {
                    continue;
                }
                Dictionary<string, object> entry = new Dictionary<string, object>();
                entry["text"] = content;
                entry["source"] = "current";
                entry["time"] = b.Timestamp;
                entry["timeText"] = "";
                return entry;
            }
            return null;
        }

        /// <summary>
        /// 视图块载荷 → 正文（content 字段；解析失败 / 缺字段 = 空串）。
        /// </summary>
        /// <param name="payload">载荷 JSON 字符串</param>
        /// <returns>正文（已 Trim）</returns>
        private static string ReadBlockContent(string payload)
        {
            if (payload == null || payload.Length == 0)
            {
                return "";
            }
            try
            {
                using (JsonDocument doc = JsonDocument.Parse(payload))
                {
                    JsonElement el;
                    if (doc.RootElement.TryGetProperty("content", out el) && el.ValueKind == JsonValueKind.String)
                    {
                        string s = el.GetString();
                        return s == null ? "" : s.Trim();
                    }
                }
            }
            catch (Exception ex)
            {
                // 单块解析失败不拖垮详情（跳过该块——失败可见）
                LogStore.Add("CatHome4", 2, "详情块载荷解析失败: " + ex.Message, "CHAT");
            }
            return "";
        }

        /// <summary>
        /// 留档末条条目 → 详情条目（source=archive——前端标注「上一会话」；时刻为档内文本，非 Unix 毫秒）。
        /// </summary>
        /// <param name="markdown">留档全文</param>
        /// <param name="title">条目类别（用户 / 回复）</param>
        /// <returns>条目字典（无命中=null）</returns>
        private static Dictionary<string, object> FindLegacyEntry(string markdown, string title)
        {
            string timeText;
            string content;
            if (!SessionViewStore.TryReadLastLegacyEntry(markdown, title, out timeText, out content))
            {
                return null;
            }
            Dictionary<string, object> entry = new Dictionary<string, object>();
            entry["text"] = content;
            entry["source"] = "archive";
            entry["time"] = 0;
            entry["timeText"] = timeText;
            return entry;
        }
    }
}
