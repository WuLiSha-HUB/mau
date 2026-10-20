using System;
using System.Collections.Generic;
using System.IO;
using System.Security.Cryptography;
using System.Text;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Http;
using CatHome4.Contracts;
using CatHome4.QQ;
using Mau.Runtime;
using CH4;

namespace CatHome4.Admin
{
    /// <summary>
    /// 对话图片附件分部——粘贴图片上传 / 取图 / 包裹组装入口（规格：Project/CH4/design-ch4-chat-images.md）。
    /// 存储：Data 下相对目录（<see cref="ImageDirs"/> 顺序即查找序），内容寻址命名（sha256 前 16 位 + 扩展名）——同图去重。
    /// 生命周期：不自动清理（无 TTL / 无引用计数）——人工处置。
    /// 端点：POST /api/v1/chat-images（原始字节入库）· GET /api/v1/cache-image/{file}（按文件名取图；白名单目录内查找，不认任意路径）。
    /// </summary>
    internal static partial class AdminService
    {
        /// <summary>图片缓存目录白名单——Data 下相对名（换机不改配置）；首项为写入目录，其余仅参与取图查找</summary>
        internal static readonly string[] ImageDirs = new string[] { "chat-images", "qqbot-files" };

        /// <summary>写入目录——上传落点（ImageDirs 首项）</summary>
        internal const string ImageWriteDir = "chat-images";

        /// <summary>单图体积上限——10 MiB（与 QQ 文件通道口径对齐）</summary>
        internal const int MaxImageBytes = 10 * 1024 * 1024;

        /// <summary>临时文件后缀——先写临时名再改名（避免半截文件被当成完整图片）</summary>
        private const string TmpSuffix = ".tmp";

        /// <summary>
        /// 图片缓存目录绝对路径——按当前 Data 根解析（配置面只存相对名）。
        /// </summary>
        /// <param name="rel">Data 下相对目录名（ImageDirs 条目）</param>
        /// <returns>绝对路径</returns>
        internal static string ImageDirPath(string rel)
        {
            return Path.Combine(_dataRoot, "Data", rel);
        }

        /// <summary>
        /// Content-Type → 扩展名映射——白名单（未命中返回空串 = 拒绝上传）。
        /// </summary>
        /// <param name="contentType">小写去参数后的 Content-Type</param>
        /// <returns>扩展名（含点）或空串</returns>
        internal static string ImageExtOf(string contentType)
        {
            if (contentType == "image/png")
            {
                return ".png";
            }
            if (contentType == "image/jpeg" || contentType == "image/jpg")
            {
                return ".jpg";
            }
            if (contentType == "image/webp")
            {
                return ".webp";
            }
            if (contentType == "image/gif")
            {
                return ".gif";
            }
            if (contentType == "image/bmp" || contentType == "image/x-ms-bmp")
            {
                return ".bmp";
            }
            return "";
        }

        /// <summary>
        /// 扩展名 → Content-Type 映射——取图响应 MIME（未命中回落 application/octet-stream）。
        /// </summary>
        /// <param name="ext">扩展名（含点，大小写不敏感）</param>
        /// <returns>MIME 文本</returns>
        internal static string ImageMimeOfExt(string ext)
        {
            string e = ext;
            if (e == null)
            {
                e = "";
            }
            e = e.ToLowerInvariant();
            if (e == ".png")
            {
                return "image/png";
            }
            if (e == ".jpg" || e == ".jpeg")
            {
                return "image/jpeg";
            }
            if (e == ".webp")
            {
                return "image/webp";
            }
            if (e == ".gif")
            {
                return "image/gif";
            }
            if (e == ".bmp")
            {
                return "image/bmp";
            }
            return "application/octet-stream";
        }

        /// <summary>
        /// 文件名安全判定——纯文件名（非空 / 无路径分隔符 / 无上级引用 / GetFileName 不变形）。
        /// </summary>
        /// <param name="file">路由取值</param>
        /// <returns>true=可作为白名单目录内的查找键</returns>
        internal static bool IsSafeImageName(string file)
        {
            if (file == null || file.Length == 0)
            {
                return false;
            }
            if (file.IndexOf('/') >= 0 || file.IndexOf('\\') >= 0)
            {
                return false;
            }
            if (file.IndexOf("..", StringComparison.Ordinal) >= 0)
            {
                return false;
            }
            if (Path.GetFileName(file) != file)
            {
                return false;
            }
            return true;
        }

        /// <summary>
        /// 内容寻址文件名——sha256 前 8 字节（16 位十六进制） + 扩展名。
        /// </summary>
        /// <param name="bytes">图片字节</param>
        /// <param name="ext">扩展名（含点）</param>
        /// <returns>文件名（如 a1b2c3d4e5f60718.png）</returns>
        internal static string ImageNameOf(byte[] bytes, string ext)
        {
            StringBuilder sb = new StringBuilder();
            using (SHA256 sha = SHA256.Create())
            {
                byte[] digest = sha.ComputeHash(bytes);
                for (int i = 0; i < 8; i = i + 1)
                {
                    sb.Append(digest[i].ToString("x2"));
                }
            }
            return sb.ToString() + ext;
        }

        /// <summary>
        /// 上传入口——POST /api/v1/chat-images（body = 图片原始字节；类型由 Content-Type 判定）。
        /// 入口面零容忍：类型白名单 / 体积上限 / 空体拒绝——任一不成立即拒绝并给出原因（不静默）。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>回执 JSON（ok / name / path / bytes / dedup；失败 ok=false + error）</returns>
        internal static async Task<IResult> HandleChatImagesUpload(HttpContext ctx)
        {
            // [段1] 类型判定——Content-Type 去参数 + 小写 + 白名单
            string contentType = ctx.Request.ContentType;
            if (contentType == null)
            {
                contentType = "";
            }
            int sep = contentType.IndexOf(';');
            if (sep >= 0)
            {
                contentType = contentType.Substring(0, sep);
            }
            contentType = contentType.Trim().ToLowerInvariant();
            string ext = ImageExtOf(contentType);
            if (ext.Length == 0)
            {
                LogStore.Add("CatHome4", 2, "图片上传拒绝（类型不在白名单）：" + contentType, "CHAT");
                return Results.Json(new { ok = false, error = "不支持的图片类型：" + contentType });
            }
            // [段2] 体积预检——Content-Length 超限不读 body
            if (ctx.Request.ContentLength.HasValue && ctx.Request.ContentLength.Value > MaxImageBytes)
            {
                LogStore.Add("CatHome4", 2, "图片上传拒绝（超上限）：" + ctx.Request.ContentLength.Value.ToString() + " 字节", "CHAT");
                return Results.Json(new { ok = false, error = "图片超过上限 " + MaxImageBytes.ToString() + " 字节" });
            }
            // [段3] body 读入——上限内读满；越界即停（不写盘）
            byte[] bytes;
            using (MemoryStream ms = new MemoryStream())
            {
                byte[] buf = new byte[65536];
                while (true)
                {
                    int n = await ctx.Request.Body.ReadAsync(buf, 0, buf.Length);
                    if (n <= 0)
                    {
                        break;
                    }
                    if (ms.Length + n > MaxImageBytes)
                    {
                        LogStore.Add("CatHome4", 2, "图片上传拒绝（读取越界上限）", "CHAT");
                        return Results.Json(new { ok = false, error = "图片超过上限 " + MaxImageBytes.ToString() + " 字节" });
                    }
                    ms.Write(buf, 0, n);
                }
                bytes = ms.ToArray();
            }
            if (bytes.Length == 0)
            {
                return Results.Json(new { ok = false, error = "空图片内容" });
            }
            // [段4] 落盘——内容寻址命名 + 已存在即去重（临时文件后改名，避免半截文件被当成完整图片）
            string name = ImageNameOf(bytes, ext);
            string dir = ImageDirPath(ImageWriteDir);
            string full = Path.Combine(dir, name);
            bool dedup = false;
            try
            {
                Directory.CreateDirectory(dir);
                if (File.Exists(full))
                {
                    dedup = true;
                }
                else
                {
                    string tmp = full + TmpSuffix;
                    File.WriteAllBytes(tmp, bytes);
                    try
                    {
                        File.Move(tmp, full);
                    }
                    catch (IOException)
                    {
                        // 并发同图——他人已落地即视为去重命中
                        if (File.Exists(tmp))
                        {
                            File.Delete(tmp);
                        }
                        dedup = File.Exists(full);
                    }
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 3, "图片上传失败：" + ex.Message, "CHAT");
                return Results.Json(new { ok = false, error = "图片落盘失败：" + ex.Message });
            }
            LogStore.Add("CatHome4", 1, "图片上传：" + name + "（" + bytes.Length.ToString() + " 字节" + (dedup ? "，去重命中" : "") + "）", "CHAT");
            return Results.Json(new { ok = true, name = name, path = full, bytes = bytes.Length, dedup = dedup });
        }

        /// <summary>
        /// 取图端点——GET /api/v1/cache-image/{**file}（catch-all：受控根寻址含斜杠）。两分支：
        /// ① 受控根寻址 `<rootId>:<rel>`——根内只读 + 图片扩展名白名单 + 越界拒绝（跑测产物等任意根内图片）；
        /// ② 缓存目录白名单（纯文件名）——只认文件名，白名单目录内查找（对话图 / QQ 图）。
        /// 设计取舍：不认绝对路径 / 不认白名单与受控根之外的路径——避免成为任意文件读取面。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>图片字节响应；未命中 / 名字非法 / 越界 404</returns>
        internal static IResult HandleCacheImageGet(HttpContext ctx)
        {
            string file = "";
            object fileValue = ctx.Request.RouteValues["file"];
            if (fileValue != null)
            {
                file = fileValue.ToString();
            }
            WorkspaceConfig ws = null;
            DataBox.TryResolve<WorkspaceConfig>(out ws);
            // [段1] 受控根寻址分支——`<rootId>:<rel>`（冒号在首个分隔符之前才视为根寻址）
            if (RootPathResolver.IsRootAddress(file))
            {
                string rootFull = "";
                string rootMime = "";
                if (!TryResolveRootImage(ws, file, out rootFull, out rootMime))
                {
                    return Results.NotFound();
                }
                return ServeImageFile(ctx, rootFull, rootMime, true);
            }
            // [段2] 本地绝对路径分支——必须落在某个受控根内（视觉工具吃绝对路径，模型天然按绝对路径引用）；
            //       不在任何根内 → 回落文件名分支（缓存目录图可能落在根外——三级 Data 锚定差异）
            if (Path.IsPathRooted(file))
            {
                string absFull = "";
                string absMime = "";
                if (TryResolveInsideRoots(ws, file, out absFull, out absMime))
                {
                    return ServeImageFile(ctx, absFull, absMime, true);
                }
                file = Path.GetFileName(file);
            }
            // [段3] 缓存目录白名单分支——纯文件名
            if (!IsSafeImageName(file))
            {
                return Results.NotFound();
            }
            for (int i = 0; i < ImageDirs.Length; i = i + 1)
            {
                string full = Path.Combine(ImageDirPath(ImageDirs[i]), file);
                if (!File.Exists(full))
                {
                    continue;
                }
                string mime = ImageMimeOfExt(Path.GetExtension(file));
                // 内容寻址不可变——同文件名同内容，可长缓存
                return ServeImageFile(ctx, full, mime, false);
            }
            return Results.NotFound();
        }

        /// <summary>
        /// 图片响应单一出口——存在性检查 + 缓存头 + 字节响应。
        /// 缓存语义：受控根内产物同名覆盖是常态（跑测出图）→ 禁缓存；缓存目录内容寻址不可变 → 长缓存。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <param name="full">图片绝对路径</param>
        /// <param name="mime">响应 MIME</param>
        /// <param name="noCache">true=禁缓存（根内可变产物）/ false=长缓存（内容寻址）</param>
        /// <returns>字节响应；文件不存在 404</returns>
        private static IResult ServeImageFile(HttpContext ctx, string full, string mime, bool noCache)
        {
            if (!File.Exists(full))
            {
                return Results.NotFound();
            }
            if (noCache)
            {
                ctx.Response.Headers["Cache-Control"] = "no-cache, no-store, must-revalidate";
            }
            else
            {
                ctx.Response.Headers["Cache-Control"] = "public, max-age=31536000, immutable";
            }
            return Results.Bytes(File.ReadAllBytes(full), mime);
        }

        /// <summary>
        /// 受控根内绝对路径解析——绝对路径必须落在某个受控根内 + 图片扩展名白名单（纯路径逻辑，不做存在性检查）。
        /// 动机：视觉工具吃绝对路径，模型天然按绝对路径引用图片——端点兜底识别；**不新增可读面**（仍限根内 + 图片）。
        /// </summary>
        /// <param name="ws">工作区配置（可空 = 无根 = 不成立）</param>
        /// <param name="path">绝对路径</param>
        /// <param name="full">出参——规范化绝对路径</param>
        /// <param name="mime">出参——响应 MIME</param>
        /// <returns>true=可作为根内图片服务</returns>
        internal static bool TryResolveInsideRoots(WorkspaceConfig ws, string path, out string full, out string mime)
        {
            full = "";
            mime = "";
            if (ws == null || path == null || path.Length == 0)
            {
                return false;
            }
            // A257——归属判定统一走公共服务（点二；相对路径按 CWD 基准，与原 Path.GetFullPath 语义一致）
            RootResolveResult rr = RootPathResolver.ResolveOwned(path, ws.Roots, Environment.CurrentDirectory);
            if (!rr.Ok)
            {
                return false;
            }
            // 扩展名白名单——端点只服务图片（非图片类型不借道取图面）
            string resolvedMime = ImageMimeOfExt(Path.GetExtension(rr.FullPath));
            if (resolvedMime == "application/octet-stream")
            {
                return false;
            }
            full = rr.FullPath;
            mime = resolvedMime;
            return true;
        }

        /// <summary>
        /// 受控根图片解析——`&lt;rootId&gt;:&lt;rel&gt;` → 绝对路径 + MIME（纯路径逻辑，不做存在性检查）。
        /// 不成立（根不存在 / 非图片扩展名 / 越界）返回 false——调用方一律 404，不静默回落。
        /// 安全约束：只读、只服务图片扩展名、解析后必须仍在根内（穿越拒绝）。
        /// </summary>
        /// <param name="ws">工作区配置（可空 = 无根 = 不成立）</param>
        /// <param name="addr">`&lt;rootId&gt;:&lt;rel&gt;` 寻址</param>
        /// <param name="full">出参——绝对路径</param>
        /// <param name="mime">出参——响应 MIME</param>
        /// <returns>true=可作为根内图片服务</returns>
        internal static bool TryResolveRootImage(WorkspaceConfig ws, string addr, out string full, out string mime)
        {
            full = "";
            mime = "";
            if (ws == null || addr == null || addr.Length == 0)
            {
                return false;
            }
            // A257——根寻址统一走公共服务（点二：展开 + 归属判定）
            RootResolveResult rr = RootPathResolver.ResolveOwned(addr, ws.Roots);
            if (!rr.Ok)
            {
                return false;
            }
            // 扩展名白名单——端点只服务图片（非图片类型不借道取图面）
            string resolvedMime = ImageMimeOfExt(Path.GetExtension(rr.FullPath));
            if (resolvedMime == "application/octet-stream")
            {
                return false;
            }
            full = rr.FullPath;
            mime = resolvedMime;
            return true;
        }

        /// <summary>
        /// 图片包裹组装——指令行套用包裹（编号取组装时刻前文条数真值）。
        /// 单一出口：Chat 前缀处理归 <see cref="ChatImageEnvelope.ApplyToLine"/>；本函数只补会话真值来源。
        /// </summary>
        /// <param name="session">目标会话（取前文条数；可空 = 0）</param>
        /// <param name="line">指令行（如 "Chat 看这个"）</param>
        /// <param name="images">图片绝对路径列表（可空）</param>
        /// <returns>套用后的指令行（不成立时原样）</returns>
        internal static string BuildChatEnvelope(ChatSession session, string line, IList<string> images)
        {
            int count = 0;
            if (session != null)
            {
                count = session.ContextCount;
            }
            return ChatImageEnvelope.ApplyToLine(line, images, count);
        }

        /// <summary>
        /// 包裹组装回调构造——把会话绑进委托（宿主各端口注入 HttpHostOptions.EnvelopeBuilder）。
        /// </summary>
        /// <param name="session">目标会话（取前文条数）</param>
        /// <returns>(指令行, 图片路径列表) → 套用后的指令行</returns>
        internal static Func<string, IList<string>, string> MakeChatEnvelopeBuilder(ChatSession session)
        {
            return delegate (string line, IList<string> images)
            {
                return BuildChatEnvelope(session, line, images);
            };
        }

        /// <summary>
        /// QQ 图片包裹解析桥（A216）——转发面经 QqTarget.ParseImages 委托调用：Core 域协议（ChatImageEnvelope.TryParse）
        /// 解析结果投影为 QQ 域窄 DTO（QqImageScan）——QQ 域不依赖 Core（依赖注入的委托桥接）。
        /// 包裹前后文本按原文顺序拼回正文；未命中时正文原样返回（零回归）。
        /// </summary>
        /// <param name="text">回复正文</param>
        /// <returns>正文（剥离包裹）+ 图片路径列表</returns>
        internal static QqImageScan ParseQqImages(string text)
        {
            QqImageScan scan = new QqImageScan();
            if (text == null || text.Length == 0)
            {
                return scan;
            }
            if (ChatImageEnvelope.TryParse(text, out List<ChatImageItem> items, out string before, out string after))
            {
                string body = before;
                if (after.Length > 0)
                {
                    if (body.Length > 0)
                    {
                        body = body + "\n" + after;
                    }
                    else
                    {
                        body = after;
                    }
                }
                scan.Body = body.Trim();
                for (int i = 0; i < items.Count; i = i + 1)
                {
                    scan.Images.Add(items[i].Path);
                }
            }
            else
            {
                scan.Body = text;
            }
            return scan;
        }

        /// <summary>
        /// 对话页路由注册——chat.html 专用（不含管理 CRUD），可安全下发到每猫 host（RouteRegistrar 槽）。
        /// 内容：图片上传与取图。
        /// </summary>
        /// <param name="sink">HTTP 路由注册面</param>
        internal static void RegisterChatPageRoutes(IHttpRouteSink sink)
        {
            RegisterChatImageRoutes(sink);
            RegisterChatAudioRoutes(sink);
        }

        /// <summary>
        /// 图片附件路由注册——上传 + 取图（主端口与每猫 host 双端注册）。
        /// </summary>
        /// <param name="sink">HTTP 路由注册面</param>
        internal static void RegisterChatImageRoutes(IHttpRouteSink sink)
        {
            sink.MapPost("/api/v1/chat-images", (Delegate)HandleChatImagesUpload);
            sink.MapGet("/api/v1/cache-image/{**file}", (Delegate)HandleCacheImageGet);
        }
    }
}
