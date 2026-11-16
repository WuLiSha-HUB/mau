namespace Mau.Contracts
{
    /// <summary>
    /// 猫级 QQ 渠道绑定（A381）——宿主从 cat.cfg 读出后交给通道服务，服务侧按它做推送分发与发言授权。
    /// 契约面只用 BCL 类型（同 ITtsService 口径）；语义权威 → Project/CH4/design-ch4-qqbot.md §十。
    /// </summary>
    public sealed class QqBotCatBinding
    {
        /// <summary>绑定的机器人配置身份（配置池 QqBotId 的 Guid 串）；空=未绑定</summary>
        public string BotKey;

        /// <summary>收信开关——true=群内 @ 本猫时按白名单推送过来</summary>
        public bool Enable;

        /// <summary>渠道白名单——群别名 / 群名 / OpenID；空=机器人目录内全部群</summary>
        public string[] Groups;

        /// <summary>主动发言授权——true=允许向本猫白名单内的群发言</summary>
        public bool CanSend;

        /// <summary>
        /// 创建空绑定——未绑定 / 不收信 / 全目录 / 不可发言（缺省即最保守态）。
        /// </summary>
        public QqBotCatBinding()
        {
            BotKey = "";
            Groups = new string[0];
        }
    }

    /// <summary>
    /// 猫级绑定解析委托（A381）——宿主注入：给一个猫 key，返回该猫的 QQ 绑定。
    /// 通道服务（Mau.Providers）不依赖宿主程序集，解析面由此委托桥接；未注入 = 该面不可用（出声）。
    /// </summary>
    /// <param name="catId">调用者猫 key（可空——空返回 null）</param>
    /// <returns>绑定；null=该猫无配置面 / 未注入</returns>
    public delegate QqBotCatBinding QqBotBindingResolver(string catId);

    /// <summary>
    /// @ 推送委托（A381）——宿主注入：把一条群内 @ 消息按猫级绑定分发给订阅的猫。
    /// 服务侧只负责「收到了一条 @」，谁该收、怎么投递（user 入口 / 正文前缀）归宿主。
    /// </summary>
    /// <param name="botKey">配置池机器人身份</param>
    /// <param name="groupOpenId">群 OpenID</param>
    /// <param name="groupAlias">群别名（目录内人工起名；未登记为空串）</param>
    /// <param name="groupTitle">群名称（官方 / 人工；未知为空串）</param>
    /// <param name="senderName">发送人显示名</param>
    /// <param name="text">消息正文（已清洗）</param>
    /// <returns>空串=已处理（含无人订阅）；否则为失败说明（服务侧记日志出声）</returns>
    public delegate string QqBotCatPushHandler(string botKey, string groupOpenId, string groupAlias, string groupTitle, string senderName, string text);
}
