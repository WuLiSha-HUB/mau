using System;
using System.Collections.Generic;
using System.Text.Json;
using System.Threading;
using System.Threading.Channels;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.Logging;
using Mau.Runtime;

namespace CatHome4.Http
{
    /// <summary>
    /// HttpHost HTTP 端点面分部——SSE 连接/指令入口/日志段/静态页。
    /// P7b partial 拆分——自 HttpHost.cs 原样搬移，逻辑零改动。
    /// </summary>
    public sealed partial class HttpHost
    {
        /// <summary>
        /// SSE 连接处理——注册客户端 + 事件队列消费（协议 §四：text/event-stream 帧格式）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>完成任务</returns>
        private async Task StreamEvents(HttpContext ctx)
        {
            ctx.Response.Headers["Content-Type"] = "text/event-stream";
            ctx.Response.Headers["Cache-Control"] = "no-cache";
            ctx.Response.Headers["Connection"] = "keep-alive";
            SseClient client = new SseClient(ctx.Response);
            // A141 按订阅推送——?topics=a,b,c；无参数 = 全量订阅（向后兼容）
            client.Topics = ParseTopics(ctx);
            lock (_clientLock)
            {
                _clients.Add(client);
                // 连接建立即推全量快照——单写本连接队列（重连兜底语义：仅补给新客户端，不广播已有连接——Codex P1）；
                // 首帧同走 NextFrame——帧序号与广播帧共用同一条 per-连接序列（取号与入队同在锁内，顺序不乱）；
                // A141——首帧同样按订阅过滤：不订阅 snapshot 的连接不推全量快照（帧序号也不消耗）
                if (client.Wants("snapshot"))
                {
                    client.Queue.Writer.TryWrite(client.NextFrame("snapshot", _snapshotCache));
                }
                // A225c——连接建立补猫卡 state 首帧（重连即自愈；仅订阅者；连接私有帧不广播）
                if (client.Wants("state") && _catsStateCache.Length > 0)
                {
                    client.Queue.Writer.TryWrite(client.NextFrame("state", _catsStateCache));
                }
                // v2 契约——视图全量首帧（含状态段）：新连接取一次当前快照（替代前端连接建立后自行拉取）；
                // 连接私有帧（不广播、不进变更集）——此后帧轮增量自然接续；断线重连即自愈（自动再取一次全量）
                if (_viewFullBuilder != null && client.Wants("view"))
                {
                    string full = _viewFullBuilder();
                    if (full != null && full.Length > 0)
                    {
                        client.Queue.Writer.TryWrite(client.NextFrame("view", full));
                    }
                }
            }
            try
            {
                await foreach (string frame in client.Queue.Reader.ReadAllAsync(ctx.RequestAborted))
                {
                    await client.Response.WriteAsync(frame);
                    await client.Response.Body.FlushAsync();
                }
            }
            catch (Exception ex)
            {
                // 客户端断开——静默移除
                LogStore.Add("HttpHost", 1, "SSE 客户端断开: " + ex.Message, "SYS");
            }
            finally
            {
                lock (_clientLock)
                {
                    _clients.Remove(client);
                }
            }
        }
        /// <summary>
        /// 解析 ?topics= 订阅清单——按消费方裁剪推送负载（A141：不订阅的事件根本不入队）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>订阅集合；无参数返回 null（= 全量订阅，向后兼容）</returns>
        private static HashSet<string> ParseTopics(HttpContext ctx)
        {
            string raw = ctx.Request.Query["topics"].ToString();
            if (raw == null || raw.Length == 0)
            {
                return null;
            }
            HashSet<string> set = new HashSet<string>(StringComparer.Ordinal);
            string[] parts = raw.Split(',');
            for (int i = 0; i < parts.Length; i++)
            {
                string t = parts[i].Trim();
                if (t.Length > 0)
                {
                    set.Add(t);
                }
            }
            return (set.Count > 0) ? set : null;
        }

        /// <summary>
        /// 指令入口处理——解析 JSON body → 投递 → 回执（协议 §五：投递即回执，结果异步可见）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON</returns>
        private async Task<IResult> HandleCommand(HttpContext ctx)
        {
            string body = "";
            using (System.IO.StreamReader reader = new System.IO.StreamReader(ctx.Request.Body))
            {
                body = await reader.ReadToEndAsync();
            }
            string text = ExtractText(body);
            // A65 图片包裹——前端投递图片路径列表时由后端组装（编号取组装时刻前文条数真值；不成立时原样投递）
            if (_envelopeBuilder != null)
            {
                List<string> images = ExtractImages(body);
                if (images.Count > 0)
                {
                    string applied = _envelopeBuilder(text, images);
                    if (applied != null && applied.Length > 0)
                    {
                        text = applied;
                    }
                }
            }
            bool ok = _dispatcher(text);
            string cmdId = "cmd-" + Interlocked.Increment(ref _seq).ToString();
            long frame = FlowRunner.GlobalFrame;
            if (ok)
            {
                var resp = new
                {
                    ok = true,
                    cmdId = cmdId,
                    frame = frame
                };
                PushEvent("cmd", JsonUtil.Serialize(resp));
                return Results.Json(resp);
            }
            var fail = new
            {
                ok = false,
                cmdId = cmdId,
                frame = frame,
                error = "指令未识别: " + text
            };
            PushEvent("cmd", JsonUtil.Serialize(fail));
            return Results.Json(fail);
        }
        /// <summary>
        /// 前端 Log 面噪音判定——trace 类审计（audit.*.trace.*）不进前端（2026-09-17）。
        /// 导线级执行轨迹（trace.fire/trace.state）由翻译器自动注入、量大，只服务审计查询面（内存真源 + CLI status）；
        /// 磁盘面本就 skipDisk 不落盘——前端 Log 页只留业务日志。
        /// </summary>
        /// <param name="entry">日志条目</param>
        /// <returns>true=前端不出（trace 审计）</returns>
        private static bool IsTraceAudit(LogStore.LogEntry entry)
        {
            string type = entry.Type;
            if (type.Length == 0)
            {
                return false;
            }
            return type.StartsWith("audit.", StringComparison.Ordinal) && type.Contains(".trace.");
        }

        /// <summary>日志查询端点——GET /api/v1/logs（O5：统一从 Log 真源读取——替代快照锚点替换面）；trace 类审计不进前端（IsTraceAudit——只服务内存真源与 CLI 审计面，2026-09-17）。</summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>日志 JSON</returns>
        public static IResult HandleLogs(HttpContext ctx)
        {
            int count = 200;
            string raw = ctx.Request.Query["n"].ToString();
            int parsed;
            if (int.TryParse(raw, out parsed) && parsed > 0)
            {
                count = parsed;
            }
            if (count > 2000)
            {
                count = 2000;
            }
            string cat = ctx.Request.Query["cat"].ToString();
            string typePrefix = ctx.Request.Query["type"].ToString();
            List<object> list = new List<object>();
            List<LogStore.LogEntry> all = LogStore.Snapshot();
            // 尾部倒序扫描——匹配 cat/type 收集 count 条（防全量遍历）
            for (int i = all.Count - 1; i >= 0 && list.Count < count; i--)
            {
                LogStore.LogEntry entry = all[i];
                // trace 类审计不进前端（导线级轨迹——2026-09-17）
                if (IsTraceAudit(entry))
                {
                    continue;
                }
                if (cat.Length > 0 && entry.Category != cat)
                {
                    continue;
                }
                if (typePrefix.Length > 0 && !entry.Type.StartsWith(typePrefix, StringComparison.Ordinal))
                {
                    continue;
                }
                list.Add(new { time = entry.Time, frame = entry.Frame, level = LogStore.LevelText(entry.Level), category = entry.Category, module = entry.Module, type = entry.Type, message = entry.Message });
            }
            // 倒序收集——还原时间序（旧→新）
            list.Reverse();
            var resp = new
            {
                version = 1,
                logs = list
            };
            return Results.Json(resp);
        }

        /// <summary>
        /// 解析 ?logs=N 查询参数——缺省/非法回退 200（v2 刷新拉取最后 N 条）
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>日志条数（1-2000 夹取）</returns>
        private static int ParseLogsQuery(HttpContext ctx)
        {
            int count = 200;
            if (ctx.Request.Query.TryGetValue("logs", out Microsoft.Extensions.Primitives.StringValues values))
            {
                string raw = values.ToString();
                int parsed;
                if (int.TryParse(raw, out parsed) && parsed > 0)
                {
                    count = parsed;
                }
            }
            if (count > 2000)
            {
                count = 2000;
            }
            return count;
        }

        /// <summary>构建日志段 JSON——LogStore 尾部 N 条（与 PushLogIncrements 同格式；锁内快照）；trace 类审计不进前端（IsTraceAudit，2026-09-17）。</summary>
        /// <param name="count">条数</param>
        /// <returns>日志数组 JSON；无日志返回空串（不拼接）</returns>
        private static string BuildLogsJson(int count)
        {
            List<LogStore.LogEntry> logs = LogStore.Snapshot(count);
            List<object> list = new List<object>();
            for (int i = 0; i < logs.Count; i++)
            {
                LogStore.LogEntry entry = logs[i];
                // trace 类审计不进前端（导线级轨迹——2026-09-17）
                if (IsTraceAudit(entry))
                {
                    continue;
                }
                list.Add(new { time = entry.Time, frame = entry.Frame, level = LogStore.LevelText(entry.Level), category = entry.Category, module = entry.Module, message = entry.Message });
            }
            return JsonUtil.Serialize(list);
        }

        /// <summary>
        /// 替换快照 JSON 的日志段——缓存快照 logs=[] 的精确字符串替换（System.Text.Json 固定输出无空格；不可靠则原样返回）
        /// </summary>
        /// <param name="json">快照 JSON（含 "logs":[]）</param>
        /// <param name="logsJson">日志数组 JSON</param>
        /// <returns>拼接后 JSON；未命中返回原 JSON</returns>
        private static string ReplaceLogsSection(string json, string logsJson)
        {
            string marker = "\"logs\":[]";
            int idx = json.IndexOf(marker, StringComparison.Ordinal);
            if (idx < 0)
            {
                return json;
            }
            return json.Substring(0, idx) + "\"logs\":" + logsJson + json.Substring(idx + marker.Length);
        }

        /// <summary>
        /// 从请求 body 提取指令文本——防御式解析（协议 §5.1：{"text":"..."}）。
        /// </summary>
        /// <param name="body">原始 body</param>
        /// <returns>指令文本（解析失败空串）</returns>
        private static string ExtractText(string body)
        {
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    JsonElement textEl;
                    if (root.TryGetProperty("text", out textEl) && textEl.ValueKind == JsonValueKind.String)
                    {
                        string text = textEl.GetString();
                        if (text != null)
                        {
                            return text;
                        }
                    }
                }
            }
            catch (Exception ex)
            {
                // 解析失败——按空指令处理（错误可见性：回执 ok=false）
                LogStore.Add("HttpHost", 2, "指令解析失败，按空指令处理: " + ex.Message, "SYS");
            }
            return "";
        }

        /// <summary>
        /// 从请求 body 提取图片路径列表——防御式解析（协议 §5.1：{"text":"…","images":["…"]}；字段可缺）。
        /// </summary>
        /// <param name="body">原始 body</param>
        /// <returns>图片绝对路径列表（缺字段 / 非数组 / 解析失败 = 空列表）</returns>
        private static List<string> ExtractImages(string body)
        {
            List<string> images = new List<string>();
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(body))
                {
                    JsonElement root = doc.RootElement;
                    JsonElement arr;
                    if (root.TryGetProperty("images", out arr) && arr.ValueKind == JsonValueKind.Array)
                    {
                        foreach (JsonElement el in arr.EnumerateArray())
                        {
                            if (el.ValueKind != JsonValueKind.String)
                            {
                                continue;
                            }
                            string p = el.GetString();
                            if (p != null && p.Trim().Length > 0)
                            {
                                images.Add(p.Trim());
                            }
                        }
                    }
                }
            }
            catch (Exception ex)
            {
                // 解析失败——按无图片处理（文本走原路径投递）
                LogStore.Add("HttpHost", 2, "图片列表解析失败，按无图片处理: " + ex.Message, "SYS");
            }
            return images;
        }

        /// <summary>
        /// 静态页服务——双页模式（协议 §六：GET / 本地面板）：_serveChatPage=true 返回 chat.html 独立对话页 / false 返回 index.html 主面板（P9.3 每猫实例按端口路由）。
        /// </summary>
        /// <returns>HTML 响应</returns>
        private IResult ServeIndex()
        {
            string htmlRoot = _htmlRootProvider.ResolveHtmlRoot();
            string fileName = "index.html";
            if (_staticPage != null && _staticPage.Length > 0)
            {
                fileName = _staticPage;
            }
            else if (_serveChatPage)
            {
                fileName = "chat.html";
            }
            string htmlPath = System.IO.Path.Combine(htmlRoot, fileName);
            if (System.IO.File.Exists(htmlPath))
            {
                string html = System.IO.File.ReadAllText(htmlPath);
                // 前端版本号注入——__V__ 占位符替换为程序集版本（cache-busting：版本变化 URL 变 → 浏览器强制拉新；治缓存纠结）
                html = html.Replace("__V__", GetFrontendVersion());
                return Results.Text(html, "text/html");
            }
            return Results.Text("CH4 外观层——" + fileName + " 未找到: " + htmlPath + "（源码区 CatHome4/html/ 或部署区 html/ 需放置静态页）", "text/plain");
        }

        /// <summary>
        /// 前端版本号——程序集版本 3 段（index/chat 静态资源 cache-busting；失败回退 "0"）
        /// </summary>
        /// <returns>版本串（如 0.51.0）</returns>
        private static string GetFrontendVersion()
        {
            try
            {
                System.Reflection.Assembly asm = System.Reflection.Assembly.GetEntryAssembly();
                if (asm != null)
                {
                    System.Reflection.AssemblyName an = asm.GetName();
                    if (an.Version != null)
                    {
                        return an.Version.ToString(3);
                    }
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "前端版本读取失败（回落 0）: " + ex.Message, "HTTP");
            }
            return "0";
        }

        /// <summary>静态资源服务——html/css|js|pet|fonts/{file}（路径穿越校验 GetFullPath+StartsWith；缓存口径按面——fonts 长缓存 / 其余禁缓存；二进制安全——Bytes 响应）。</summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <param name="subDir">子目录名（css/js/pet/fonts）</param>
        /// <param name="mime">响应 MIME</param>
        /// <returns>文件响应；未找到/越界 404</returns>
        private IResult ServeStatic(HttpContext ctx, string subDir, string mime)
        {
            string htmlRoot = System.IO.Path.GetFullPath(_htmlRootProvider.ResolveHtmlRoot());
            // [段2] 文件名取值——RouteValues 键可能缺失/null（替代 ?. + ?? 语法糖）
            string fileName = "";
            object fileValue = ctx.Request.RouteValues["file"];
            if (fileValue != null)
            {
                fileName = fileValue.ToString()!;
            }
            string filePath = System.IO.Path.GetFullPath(System.IO.Path.Combine(htmlRoot, subDir, fileName));
            // 路径穿越校验——解析后必须仍在 html 根内（C# 包 exp §六 TCP/HTTP 规则 9）
            if (!filePath.StartsWith(htmlRoot + System.IO.Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase))
            {
                return Results.NotFound();
            }
            if (!System.IO.File.Exists(filePath))
            {
                return Results.NotFound();
            }
            // 缓存口径分面（A120）——fonts：几十 MB 二进制走长缓存（URL 固定，换代随文件名/版本变化）；
            // 其余静态资源保持禁缓存（前端频繁迭代，同 index 策略）
            if (subDir == "fonts")
            {
                ctx.Response.Headers["Cache-Control"] = "public, max-age=604800";
            }
            else
            {
                ctx.Response.Headers["Cache-Control"] = "no-cache, no-store, must-revalidate";
            }
            // 二进制安全——文本（css/js）与二进制（pet webp）共用；ReadAllText 会破坏二进制资源
            byte[] bytes = System.IO.File.ReadAllBytes(filePath);
            return Results.Bytes(bytes, mime);
        }

        /// <summary>
        /// 静态资源服务——html 根**顶层文件**（favicon.ico 等；无路由参数，故与 ServeStatic 分开）。
        /// 同口径：路径穿越校验（GetFullPath + StartsWith）+ 二进制安全（Bytes 响应）。
        /// </summary>
        /// <param name="fileName">文件名（不含路径分隔符）</param>
        /// <param name="mime">响应 MIME</param>
        /// <returns>文件响应；未找到/越界 404</returns>
        private IResult ServeRootFile(string fileName, string mime)
        {
            string htmlRoot = System.IO.Path.GetFullPath(_htmlRootProvider.ResolveHtmlRoot());
            string filePath = System.IO.Path.GetFullPath(System.IO.Path.Combine(htmlRoot, fileName));
            if (!filePath.StartsWith(htmlRoot + System.IO.Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase))
            {
                return Results.NotFound();
            }
            if (!System.IO.File.Exists(filePath))
            {
                return Results.NotFound();
            }
            byte[] bytes = System.IO.File.ReadAllBytes(filePath);
            return Results.Bytes(bytes, mime);
        }
    }
}