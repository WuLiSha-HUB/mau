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
            // A328——对话端口只服务对话输入与页内自用指令（停止/继续/新会话/回滚）：其余管理指令一律拒绝
            if (!_surfaceConsole && !IsChatPortInputAllowed(text))
            {
                LogStore.Add("HttpSurface", 1, "对话端口拒绝管理指令 | " + text, "A328");
                string denyId = "cmd-" + Interlocked.Increment(ref _seq).ToString();
                var deny = new
                {
                    ok = false,
                    cmdId = denyId,
                    frame = FlowRunner.GlobalFrame,
                    error = "对话端口不接受管理指令（请在 console 主端口执行）: " + text
                };
                return Results.Json(deny);
            }
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
        private IResult ServeIndex(HttpContext ctx)
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
                // [段2] 资产基址注入（A362）——非 console 端口的页面把 js / css 一律指向 console：
                //   等价于构建期 publicPath（业界资产宿主 / CDN 模式），源文件保持相对路径不变；
                //   浏览器 HTTP 缓存分区键取 scheme://eTLD+1（忽略端口），故同 site 全端口命中同一份缓存。
                //   CSS 内部相对 url(../fonts/…) 随 CSS 的 URL 解析 → 自动落到 console，无需改样式表。
                string assetBase = ResolveAssetBase(ctx);
                if (assetBase.Length > 0)
                {
                    html = html.Replace("src=\"js/", "src=\"" + assetBase + "js/");
                    html = html.Replace("href=\"css/", "href=\"" + assetBase + "css/");
                    // 脚本内相对取数（桌宠 pet/… / 版本 json）走同一基址——前端读 window.__ASSET_BASE__
                    html = html.Replace("<head>", "<head>\n    <script>window.__ASSET_BASE__=\"" + assetBase + "\";</script>");
                }
                // [段3] 条件缓存（A362）——ETag 取**替换后的正文**：刷新即校验，正文未变回 304（版本自增 / 页面改动即新正文 → 200）
                string etag = ResolveContentETag(html);
                ctx.Response.Headers["Cache-Control"] = "no-cache";
                ctx.Response.Headers["ETag"] = etag;
                if (MatchIfNoneMatch(ctx, etag))
                {
                    return Results.StatusCode(304);
                }
                return Results.Text(html, "text/html");
            }
            return Results.Text("CH4 外观层——" + fileName + " 未找到: " + htmlPath + "（源码区 CatHome4/html/ 或部署区 html/ 需放置静态页）", "text/plain");
        }

        /// <summary>
        /// 资产基址（A362）——console 端口与当前实例端口不同时返回 `http://&lt;host&gt;:&lt;console端口&gt;/`，同源时返回空串。
        /// host 取请求 Host（本机 127.0.0.1 / 远程 tailscale 地址各自成立），故同一份页面配置在本地与远程都正确。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>基地址（以斜杠结尾）或空串（同源）</returns>
        private static string ResolveAssetBase(HttpContext ctx)
        {
            int assetPort = ConsoleAssetPort;
            if (assetPort <= 0)
            {
                return "";
            }
            string host = ctx.Request.Host.Host;
            if (host == null || host.Length == 0)
            {
                return "";
            }
            int currentPort = 80;
            if (ctx.Request.Host.Port != null)
            {
                currentPort = ctx.Request.Host.Port.Value;
            }
            if (currentPort == assetPort)
            {
                return "";
            }
            return "http://" + host + ":" + assetPort.ToString() + "/";
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

        /// <summary>静态资源服务——html/css|js|pet|fonts/{file}（路径穿越校验 GetFullPath+StartsWith；缓存口径按面分层 + ETag 条件请求 → 304；二进制安全——Bytes 响应）。</summary>
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
            // [段3] 缓存口径分面 + 内容校验器（A362）——校验器取内容哈希：部署复制 / 版本自增均不改变它
            System.IO.FileInfo info = new System.IO.FileInfo(filePath);
            string etag = ResolveETag(filePath, info);
            ApplyStaticCache(ctx, subDir, etag);
            // [段4] 条件请求——ETag 命中即 304（零字节；浏览器刷新校验走此路，不回传资源体）
            if (MatchIfNoneMatch(ctx, etag))
            {
                return Results.StatusCode(304);
            }
            // 二进制安全——文本（css/js）与二进制（pet webp）共用；ReadAllText 会破坏二进制资源
            byte[] bytes = System.IO.File.ReadAllBytes(filePath);
            return Results.Bytes(bytes, mime);
        }
        /// <summary>
        /// 静态资源缓存口径（A362）——按面分层 + ETag 内容校验器：字体长期可用（不随宿主版本变更而失效）、
        /// css / js 每次校验（改文件即生效，未变走 304 零字节）、pet 七日缓存（前端 Cache API 为第一层）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <param name="subDir">子目录名（css/js/pet/fonts）</param>
        /// <param name="etag">内容校验器（内容哈希前 8 字节；哈希失败回落长度 + 写入时刻）</param>
        private static void ApplyStaticCache(HttpContext ctx, string subDir, string etag)
        {
            ctx.Response.Headers["ETag"] = etag;
            // 跨端口资产共享（A362）——页面可从 console 端口取字体 / 桌宠 / 脚本：字体与 fetch 跨源必须 CORS，
            // 缺头时浏览器静默丢弃（字体回退系统字体、桌宠 Cache API 拒存 opaque 响应）；CORP 放行跨源读取。
            ctx.Response.Headers["Access-Control-Allow-Origin"] = "*";
            ctx.Response.Headers["Cross-Origin-Resource-Policy"] = "cross-origin";
            // 性能观测可见面（A362）——跨源资源默认屏蔽 transferSize / decodedBodySize / deliveryType，
            // 无此头则「是否来自缓存」在本机不可判定（浏览器侧只能看到 0）；允许跨源读取计时数据。
            ctx.Response.Headers["Timing-Allow-Origin"] = "*";
            // 字体与宿主版本解耦——一次拉取后长期复用；文件自身变更靠 ETag 到期校验兜底
            if (subDir == "fonts")
            {
                ctx.Response.Headers["Cache-Control"] = "public, max-age=2592000";
                return;
            }
            // 桌宠七日内不重复请求（前端 Cache API 命中时根本不发请求，此为第二层）
            if (subDir == "pet")
            {
                ctx.Response.Headers["Cache-Control"] = "public, max-age=604800";
                return;
            }
            // css / js——必须重新校验：开发期改文件不升版本同样即时生效，未变不回传资源体
            ctx.Response.Headers["Cache-Control"] = "no-cache";
        }
        /// <summary>
        /// 静态资源内容校验器（A362）——取内容哈希而非文件时刻：部署复制 / 版本自增都不改变它，
        /// 浏览器缓存的资产因此跨版本持续可用（核心诉求：拉过一次就不再从远端拉）。
        /// </summary>
        /// <param name="filePath">资源物理路径</param>
        /// <param name="info">文件信息（哈希失败时的回落源）</param>
        /// <returns>弱校验器串（W/"&lt;内容哈希前 8 字节&gt;"）</returns>
        private static string ResolveETag(string filePath, System.IO.FileInfo info)
        {
            // [段1] 内容哈希——8 字节（16 hex）足够区分静态资源，且不随复制 / 时间变化
            try
            {
                using (System.IO.FileStream stream = System.IO.File.OpenRead(filePath))
                {
                    using (System.Security.Cryptography.SHA256 sha = System.Security.Cryptography.SHA256.Create())
                    {
                        byte[] digest = sha.ComputeHash(stream);
                        return "W/\"" + System.Convert.ToHexString(digest, 0, 8).ToLowerInvariant() + "\"";
                    }
                }
            }
            catch (System.Exception ex)
            {
                // [段2] 失败必须可见——回落长度 + 时刻（保守取向：内容变即换值，宁可多下一次）
                LogStore.Add("CatHome4", 2, "静态资源 ETag 哈希失败（回落长度+时刻）: " + ex.Message, "HTTP");
                return "W/\"" + info.Length.ToString() + "-" + info.LastWriteTimeUtc.Ticks.ToString() + "\"";
            }
        }
        /// <summary>
        /// 条件请求判定（A362）——请求头 If-None-Match 与当前内容校验器相等即命中（回 304，零字节资源体）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <param name="etag">当前内容校验器</param>
        /// <returns>true = 命中，应回 304</returns>
        private static bool MatchIfNoneMatch(HttpContext ctx, string etag)
        {
            Microsoft.Extensions.Primitives.StringValues inm = ctx.Request.Headers["If-None-Match"];
            for (int i = 0; i < inm.Count; i = i + 1)
            {
                if (string.Equals(inm[i], etag, StringComparison.Ordinal))
                {
                    return true;
                }
            }
            return false;
        }
        /// <summary>
        /// 文本内容校验器（A362）——对已组装正文取内容哈希（页面 ETag 取替换版本号后的正文，版本变化即换值）。
        /// </summary>
        /// <param name="text">正文</param>
        /// <returns>弱校验器串（W/"&lt;内容哈希前 8 字节&gt;"）</returns>
        private static string ResolveContentETag(string text)
        {
            byte[] bytes = System.Text.Encoding.UTF8.GetBytes(text);
            using (System.Security.Cryptography.SHA256 sha = System.Security.Cryptography.SHA256.Create())
            {
                byte[] digest = sha.ComputeHash(bytes);
                return "W/\"" + System.Convert.ToHexString(digest, 0, 8).ToLowerInvariant() + "\"";
            }
        }

        /// <summary>
        /// 静态资源服务——html 根**顶层文件**（favicon.ico 等；无路由参数，故与 ServeStatic 分开）。
        /// 同口径：路径穿越校验（GetFullPath + StartsWith）+ 二进制安全（Bytes 响应）。
        /// </summary>
        /// <param name="fileName">文件名（不含路径分隔符）</param>
        /// <param name="mime">响应 MIME</param>
        /// <returns>文件响应；未找到/越界 404</returns>
        private IResult ServeRootFile(HttpContext ctx, string fileName, string mime)
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
            // [段2] 条件缓存（A362）——顶层文件同口径：no-cache 即每次校验，内容未变回 304（图标换代不再全量回传）
            System.IO.FileInfo info = new System.IO.FileInfo(filePath);
            string etag = ResolveETag(filePath, info);
            ctx.Response.Headers["Cache-Control"] = "no-cache";
            ctx.Response.Headers["ETag"] = etag;
            if (MatchIfNoneMatch(ctx, etag))
            {
                return Results.StatusCode(304);
            }
            byte[] bytes = System.IO.File.ReadAllBytes(filePath);
            return Results.Bytes(bytes, mime);
        }
    }
}