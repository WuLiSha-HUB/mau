using System;
using System.IO;
using Microsoft.AspNetCore.Http;
using CatHome4.Contracts;

namespace CatHome4.Admin
{
    /// <summary>
    /// 音频资产分部（A265）——轮末总结拼接音频的取回面。
    /// 端点：GET /api/v1/audio/{**file}（裸文件名 + .wav；目录 = 配置 py.service.kokoro.out_dir）。
    /// 设计取舍：单独资产入口，不与图片端点混面（图片面认受控根寻址 / 绝对路径，音频只认产物目录裸名——最小可读面）。
    /// </summary>
    internal static partial class AdminService
    {
        /// <summary>
        /// 音频资产路由注册——对话页（主端口 + 每猫 host）双端注册。
        /// </summary>
        /// <param name="sink">HTTP 路由注册面</param>
        internal static void RegisterChatAudioRoutes(IHttpRouteSink sink)
        {
            sink.MapGet("/api/v1/audio/{**file}", (Delegate)HandleAudioGet);
        }

        /// <summary>
        /// 取音频端点——GET /api/v1/audio/{**file}：只认 tts 产物目录下的裸文件名（.wav）。
        /// 不认路径分隔符 / 不认受控根寻址 / 不认绝对路径——避免成为任意文件读取面。
        /// </summary>
        /// <param name="ctx">HTTP 上下文</param>
        /// <returns>音频字节响应；名字非法 / 目录未配置 / 不存在 404</returns>
        internal static IResult HandleAudioGet(HttpContext ctx)
        {
            string file = "";
            object fileValue = ctx.Request.RouteValues["file"];
            if (fileValue != null)
            {
                file = fileValue.ToString();
            }
            if (!IsSafeAudioName(file))
            {
                return Results.NotFound();
            }
            string dir = "";
            if (_globalConfig != null)
            {
                dir = _globalConfig.Get("py.service.kokoro.out_dir", "");
            }
            if (dir == null || dir.Length == 0)
            {
                return Results.NotFound();
            }
            string full = Path.Combine(dir, file);
            if (!File.Exists(full))
            {
                return Results.NotFound();
            }
            // 文件名按轮次唯一（summary_<轮次戳>_<猫>.wav）——内容不可变，可长缓存
            ctx.Response.Headers["Cache-Control"] = "public, max-age=31536000, immutable";
            return Results.Bytes(File.ReadAllBytes(full), "audio/wav");
        }

        /// <summary>
        /// 音频文件名安全检查——裸名（无路径分隔符与上级引用）+ .wav 扩展 + 长度上限。
        /// </summary>
        /// <param name="name">文件名（不含目录）</param>
        /// <returns>通过 true</returns>
        private static bool IsSafeAudioName(string name)
        {
            if (name == null || name.Length == 0 || name.Length > 128)
            {
                return false;
            }
            if (name.IndexOf('/') >= 0 || name.IndexOf('\\') >= 0 || name.IndexOf("..", StringComparison.Ordinal) >= 0)
            {
                return false;
            }
            if (!name.EndsWith(".wav", StringComparison.OrdinalIgnoreCase))
            {
                return false;
            }
            return true;
        }
    }
}
