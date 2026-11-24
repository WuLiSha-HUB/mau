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
        /// 管理路由注册——37 端点全量（llm-apis / cat-config / cat-default / workspace / appearance-apis / packs / open-data-dir / chat-images / audio / db / groups / token-stats）。
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

            // [段1b] QQ 机器人池 CRUD——A379（收信服务与 qqbot 工具组的配置面；AppSecret 落 secrets）
            sink.MapGet("/api/v1/qqbot-apis", (Delegate)AdminService.HandleQqBotApisGet);
            sink.MapPost("/api/v1/qqbot-apis", (Delegate)AdminService.HandleQqBotApisPost);
            sink.MapPost("/api/v1/qqbot-apis/edit", (Delegate)AdminService.HandleQqBotApisEdit);
            sink.MapPost("/api/v1/qqbot-apis/delete", (Delegate)AdminService.HandleQqBotApisDelete);
            sink.MapPost("/api/v1/qqbot-apis/default", (Delegate)AdminService.HandleQqBotApisDefault);

            // [段1c] QQ 群目录面——A380（收信自动登记的群 + 别名 + 官方权限刷新）
            sink.MapGet("/api/v1/qqbot-groups", (Delegate)AdminService.HandleQqBotGroupsGet);
            sink.MapPost("/api/v1/qqbot-groups/alias", (Delegate)AdminService.HandleQqBotGroupsAlias);
            sink.MapPost("/api/v1/qqbot-groups/add", (Delegate)AdminService.HandleQqBotGroupsAdd);
            sink.MapPost("/api/v1/qqbot-groups/refresh", (Delegate)AdminService.HandleQqBotGroupsRefresh);
            sink.MapPost("/api/v1/qqbot-groups/remove", (Delegate)AdminService.HandleQqBotGroupsRemove);

            // [段2] 每猫配置读写——M3
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

            // [段12] 配队（Group）数据面——组列表 / 建改 / 启停 / 删除 / 消息流（A300）
            sink.MapGet("/api/v1/groups", (Delegate)AdminService.HandleGroupsGet);
            sink.MapPost("/api/v1/groups", (Delegate)AdminService.HandleGroupsPost);
            sink.MapPost("/api/v1/groups/enabled", (Delegate)AdminService.HandleGroupsEnabled);
            sink.MapPost("/api/v1/groups/delete", (Delegate)AdminService.HandleGroupsDelete);
            sink.MapGet("/api/v1/groups/{name}/messages", (Delegate)AdminService.HandleGroupsMessagesGet);
            sink.MapPost("/api/v1/groups/messages", (Delegate)AdminService.HandleGroupsMessagesPost);
            sink.MapGet("/api/v1/groups/host", (Delegate)AdminService.HandleGroupsHostGet);

            // [段13] Token 用量统计面——A389（快照读取 + 手动刷新；数据源 Data/tokennumber/，后台每分钟自动重算）
            sink.MapGet("/api/v1/token-stats", (Delegate)AdminService.HandleTokenStatsGet);
            sink.MapPost("/api/v1/token-stats/refresh", (Delegate)AdminService.HandleTokenStatsRefresh);

            // [段14] HTTP 注入通道池——A405（CRUD 在 console 面；注入端点由外部程序持通道 Key 调用，
            // 认证在处理器内完成——ConsoleAuthGate 对该路径豁免，设计权威 design-ch4-channel.md）
            sink.MapGet("/api/v1/channels", (Delegate)AdminService.HandleChannelsGet);
            sink.MapPost("/api/v1/channels", (Delegate)AdminService.HandleChannelsPost);
            sink.MapPost("/api/v1/channels/edit", (Delegate)AdminService.HandleChannelsEdit);
            sink.MapPost("/api/v1/channels/rotate", (Delegate)AdminService.HandleChannelsRotate);
            sink.MapPost("/api/v1/channels/delete", (Delegate)AdminService.HandleChannelsDelete);
            sink.MapPost("/api/v1/channels/inject", (Delegate)AdminService.HandleChannelsInject);
            // A406——下游出口播报（同一通道 Key 的第二语义：投递到 QQ 群；认证同上，在此精确豁免）
            sink.MapPost("/api/v1/channels/publish", (Delegate)AdminService.HandleChannelsPublish);
        }
    }
}
