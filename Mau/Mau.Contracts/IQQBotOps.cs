namespace Mau.Contracts
{
    /// <summary>
    /// QQ 机器人业务面（A380）——工具组的执行面契约：发言寻址（别名解析）、通道自述、群目录维护。
    /// 与生命周期面 <see cref="Mau.Contracts.IQQBotService"/> 分开：那份管连接启停，这份管「说什么、发到哪」。
    /// 契约面只用 BCL 类型（同 ITtsService 口径）。
    /// </summary>
    public interface IQQBotOps
    {
        /// <summary>
        /// 主动发言——向指定群发一条主动消息（无 msg_id）。
        /// 群寻址支持别名 / 群名 / OpenID / 唯一前缀；空 = 该 bot 目录中的唯一群。
        /// 回执正文含实际投递目标（别名 + 群名 + OpenID 末 8 位）；失败必可见。
        /// </summary>
        /// <param name="botKey">配置池身份（空=默认机器人）</param>
        /// <param name="groupRef">群寻址（别名 / 群名 / OpenID / 前缀；空=目录唯一群）</param>
        /// <param name="text">正文（Markdown）</param>
        /// <returns>空串外的任何内容 = 回执行文本（首行 JSON 头 + 摘要行 + 正文）；失败以 ERR| 开头</returns>
        string SendProactiveTo(string botKey, string groupRef, string text);

        /// <summary>
        /// 本通道自述——池内每个机器人的连接状态、收/发能力与群目录摘要。
        /// A381 落地前不带猫级过滤（catId 仅记录在回执头，不参与过滤）。
        /// </summary>
        /// <param name="catId">调用者猫 key（宿主注入保留键，可空）</param>
        /// <returns>多行文本</returns>
        string MyChannelText(string catId);

        /// <summary>
        /// 单个机器人的详情——群目录逐条（别名 / 群名 / 角色 / 收信范围 / 主动发言可用性）。
        /// </summary>
        /// <param name="botKey">配置池身份（空=默认机器人）</param>
        /// <param name="refreshOfficial">true=顺带调官方接口刷新群名与群内权限（受白名单限制，失败降级并明示）</param>
        /// <returns>多行文本</returns>
        string BotDetailText(string botKey, bool refreshOfficial);

        /// <summary>
        /// 群目录维护——人工登记一个群（没说过话的群也能进目录）。
        /// </summary>
        /// <param name="botKey">配置池身份（空=默认机器人）</param>
        /// <param name="groupOpenId">群 OpenID</param>
        /// <returns>空串成功；否则为 "ERR|CODE|说明"</returns>
        string AddGroupToDirectory(string botKey, string groupOpenId);

        /// <summary>
        /// 群目录维护——给群起别名（空别名 = 清除）；同一 bot 内别名唯一。
        /// </summary>
        /// <param name="botKey">配置池身份（空=默认机器人）</param>
        /// <param name="groupRef">群寻址（别名 / 群名 / OpenID / 前缀）</param>
        /// <param name="alias">新别名（空=清除）</param>
        /// <returns>空串成功；否则为 "ERR|CODE|说明"</returns>
        string SetGroupAlias(string botKey, string groupRef, string alias);

        /// <summary>
        /// 群目录 JSON——前端配置页渲染用（每个 bot 一项：openId / alias / title / titleSource / role / allowProactive / recvSetting / firstSeen / lastSeen）。
        /// </summary>
        /// <param name="botKey">配置池身份（空=默认机器人）</param>
        /// <returns>成功为 JSON 文本；失败为 "ERR|CODE|说明"</returns>
        string DirectoryJson(string botKey);

        /// <summary>
        /// 群目录操作（工具面统一入口）——action：list（列群）· alias（设别名）· add（登记群）· refresh（刷官方状态）。
        /// 回执为多行文本；失败以 ERR| 开头（失败可见）。
        /// </summary>
        /// <param name="botKey">配置池身份（空=默认机器人）</param>
        /// <param name="action">动作（list / alias / add / refresh）</param>
        /// <param name="groupRef">群寻址（别名 / 群名 / OpenID / 前缀）</param>
        /// <param name="alias">新别名（action=alias 时使用；空=清除）</param>
        /// <returns>多行文本</returns>
        string GroupsOp(string botKey, string action, string groupRef, string alias);

        /// <summary>
        /// 刷新该机器人全部群的官方状态与群名（逐群 bot_state + info；任一失败在回执里逐个标注）。
        /// </summary>
        /// <param name="botKey">配置池身份（空=默认机器人）</param>
        /// <returns>多行文本（含每群刷新结果）</returns>
        string RefreshOfficial(string botKey);
    }
}
