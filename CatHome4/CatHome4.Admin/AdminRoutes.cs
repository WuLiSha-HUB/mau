using System;
using CatHome4.Contracts;

namespace CatHome4.Admin
{
    /// <summary>
    /// 管理端 HTTP 路由装配表（A284）——装配与执行分离：本文件只声明端点（路径 + HTTP 方法 + 处理器）。
    /// 执行体归 AdminService 各分部；登记序即声明序，同路径不同 HTTP 方法互不冲突。
    /// 归属：Admin 域装配面（入口壳 S6 段经 IHttpRouteSink 注入，主端口仅注册一次）。
    /// </summary>
    internal static class AdminRoutes
    {
        /// <summary>
        /// 管理路由注册——31 端点全量（llm-apis / qqbot-apis / cat-config / cat-default / workspace / appearance-apis / packs / open-data-dir / chat-images / audio / db）。
        /// </summary>
        /// <param name="sink">HTTP 路由注册面（HttpHost 实现）</param>
        public static void Register(IHttpRouteSink sink)
        {
            // [段1] LLM API 池 CRUD——M3 管理面
            sink.MapGet("/api/v1/llm-apis", (Delegate)AdminService.HandleLlmApisGet);
            sink.MapPost("/api/v1/llm-apis", (Delegate)AdminService.HandleLlmApisPost);
            sink.MapPost("/api/v1/llm-apis/edit", (Delegate)AdminService.HandleLlmApisEdit);
            sink.MapPost("/api/v1/llm-apis/delete", (Delegate)AdminService.HandleLlmApisDelete);
            sink.MapPost("/api/v1/llm-apis/default", (Delegate)AdminService.HandleLlmApisDefault);

            // [段2] QQ Bot 池 CRUD——R2.3 管理面
            sink.MapGet("/api/v1/qqbot-apis", (Delegate)AdminService.HandleQqBotApisGet);
            sink.MapPost("/api/v1/qqbot-apis", (Delegate)AdminService.HandleQqBotApisPost);
            sink.MapPost("/api/v1/qqbot-apis/edit", (Delegate)AdminService.HandleQqBotApisEdit);
            sink.MapPost("/api/v1/qqbot-apis/delete", (Delegate)AdminService.HandleQqBotApisDelete);

            // [段3] 每猫配置读写——M3
            sink.MapGet("/api/v1/cat-config", (Delegate)AdminService.HandleCatConfigGet);
            sink.MapPost("/api/v1/cat-config", (Delegate)AdminService.HandleCatConfigPost);

            // [段4] 新猫默认模板——全局配置管理面（M3d）
            sink.MapGet("/api/v1/cat-default", (Delegate)AdminService.HandleCatDefaultGet);
            sink.MapPost("/api/v1/cat-default", (Delegate)AdminService.HandleCatDefaultPost);

            // [段5] 受控根编辑面——管理员面（M4d）
            sink.MapGet("/api/v1/workspace", (Delegate)AdminService.HandleWorkspaceGet);
            sink.MapPost("/api/v1/workspace", (Delegate)AdminService.HandleWorkspacePost);

            // [段6] 外观配置池——全局池管理面（A244）
            sink.MapGet("/api/v1/appearance-apis", (Delegate)AdminService.HandleAppearanceApisGet);
            sink.MapPost("/api/v1/appearance-apis", (Delegate)AdminService.HandleAppearanceApisPost);
            sink.MapPost("/api/v1/appearance-apis/edit", (Delegate)AdminService.HandleAppearanceApisEdit);
            sink.MapPost("/api/v1/appearance-apis/delete", (Delegate)AdminService.HandleAppearanceApisDelete);
            sink.MapPost("/api/v1/appearance-apis/default", (Delegate)AdminService.HandleAppearanceApisDefault);

            // [段7] 加载包池——全局池管理面（R4 加载包）
            sink.MapGet("/api/v1/packs", (Delegate)AdminService.HandlePacksGet);
            sink.MapPost("/api/v1/packs", (Delegate)AdminService.HandlePacksPost);
            sink.MapPost("/api/v1/packs/delete", (Delegate)AdminService.HandlePacksDelete);

            // [段8] 打开数据目录——前端按钮（explorer.exe 打开持久化 Data 目录）
            sink.MapPost("/api/v1/open-data-dir", (Delegate)AdminService.HandleOpenDataDir);

            // [段9] 对话图片附件——上传 + 取图（主端口注册；每猫 / majordomo 对话页走对话页注册面）
            sink.MapPost("/api/v1/chat-images", (Delegate)AdminService.HandleChatImagesUpload);
            sink.MapGet("/api/v1/cache-image/{**file}", (Delegate)AdminService.HandleCacheImageGet);

            // [段10] 音频资产——轮末总结拼接音频取回（A265；与图片面分离）
            sink.MapGet("/api/v1/audio/{**file}", (Delegate)AdminService.HandleAudioGet);

            // [段11] 数据库（Smriti）——库清单 / 新建库 / 条目查询 / 条目全文（写入走 db-* 工具面）
            sink.MapGet("/api/v1/db/libs", (Delegate)AdminService.HandleDbLibsGet);
            sink.MapPost("/api/v1/db/libs", (Delegate)AdminService.HandleDbLibsPost);
            sink.MapGet("/api/v1/db/libs/{lib}/entries", (Delegate)AdminService.HandleDbEntriesGet);
            sink.MapGet("/api/v1/db/libs/{lib}/entries/{entryId}", (Delegate)AdminService.HandleDbEntryGet);
        }
    }
}
