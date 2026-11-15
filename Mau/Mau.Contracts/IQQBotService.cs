namespace Mau.Contracts
{
    /// <summary>
    /// QQ 机器人渠道服务——入口渠道（收信）+ 出口渠道（回复）的运行时引用服务。
    /// 契约面只用 BCL 类型；WebSocket / HTTP 实现细节不泄漏到此（同 ITtsService 口径）。
    /// A379：首版只交付收信面（连接 + 事件落盘），发送面随工具组（A380）补。
    /// </summary>
    public interface IQQBotService
    {
        /// <summary>
        /// 事件落盘路径——收到的原始事件按行写此 JSONL（数据根 Data/qqbot/inbox.jsonl）。
        /// 空串表示宿主未完成注入（调用方应按「收信不可用」处理并出声）。
        /// </summary>
        string InboxPath { get; }

        /// <summary>
        /// 启动一条机器人连接（同一 botKey 重复启动为幂等替换）。
        /// </summary>
        /// <param name="botKey">配置池身份（qqBotId 的 D 格式串，日志与落盘标识）</param>
        /// <param name="displayName">显示名（日志可读性）</param>
        /// <param name="appId">QQ 开放平台 AppID</param>
        /// <param name="appSecret">AppSecret（仅内存持有，不落盘）</param>
        /// <param name="sandbox">true=沙箱环境（沙箱与正式数据不互通）</param>
        /// <returns>空串=已受理启动；"ERR|CODE|说明"=不可启动（可见，不静默）</returns>
        string Start(string botKey, string displayName, string appId, string appSecret, bool sandbox);

        /// <summary>
        /// 停止指定机器人连接。
        /// </summary>
        /// <param name="botKey">配置池身份</param>
        /// <returns>空串=已停止或本就未运行；"ERR|CODE|说明"=停止失败</returns>
        string Stop(string botKey);

        /// <summary>
        /// 运行态摘要——一行文本（连接数 / 每条连接的环境与重连计数）或空串（未启动任何连接）。
        /// </summary>
        /// <returns>运行态摘要</returns>
        string Status();

        /// <summary>
        /// 主动发言——向指定群发一条主动消息（无 msg_id；受 20 qpm 与 1000 条/群/天 频控，需群主开启「允许主动发言」）。
        /// 这是 QQBot 唯一的外发出口（A379；命令回复走被动通道，与本方法互不混用）。
        /// </summary>
        /// <param name="botKey">配置池身份</param>
        /// <param name="groupOpenId">群 OpenID</param>
        /// <param name="text">正文（纯文本）</param>
        /// <returns>空串成功；"ERR|CODE|说明"失败</returns>
        string SendProactive(string botKey, string groupOpenId, string text);

        /// <summary>
        /// 本机器人信息——所在群与每群权限（官方 bot_state 优先，不可用时降级本地观测并明示）。
        /// 硬编码 <c>/botinfo</c> 与工具面 <c>qqbot-botinfo</c> 共用同一实现。
        /// </summary>
        /// <param name="botKey">配置池身份（空=全部已连接机器人的合并视图）</param>
        /// <returns>多行文本</returns>
        string BotInfoText(string botKey);

        /// <summary>
        /// 宿主收尾——停止全部连接（Main finally 调用一次；不抛异常）。
        /// </summary>
        void Shutdown();
    }
}
