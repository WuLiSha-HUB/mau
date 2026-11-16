// ═══════════════════════════════════════════════════
// 积木: tools.qqbotcat
// ID:   BRIK-TOOLS-022
// 类别: TOOLS
// 作用: 返回 QQBot 工具组全部工具的 OpenAI 兼容定义 JSON（宿主工具池拼装原料）
// 依赖: 无
// 引用: 无（纯文本常量）
// 原理: GetToolsJson() 返回本组工具定义整包 JSON——改描述只动本文件，mau proj 重建即生效
//       命名铁律：工具定义积木约定名 = tools.<flowName 小写>（qqbot_cat.mau → QqBotCat → tools.qqbotcat）
//       JSON group 取 .mauproj 声明的组名（本组 = QqBotCat）
// 常用: FL_QqBotCat.GetToolsJson() 自曝调用面（IFlow 接口）
// ═══════════════════════════════════════════════════
namespace Mau.Bricks
{
    /// <summary>
    /// 工具定义积木——QQBot 组工具定义 JSON（A380）。
    /// 四件：发言（主动出口）/ 通道自述 / 机器人详情 / 群目录维护。
    /// 出口铁律：工具发言一律主动消息（无 msg_id）；群内命令回复走被动通道，两条路径不混用。
    /// </summary>
    public static class ToolsQqbotcatBrick
    {
        /// <summary>
        /// 获取本组工具定义 JSON——{"group":"QqBotCat","tools":[{name,description,parameters}]}
        /// </summary>
        /// <returns>工具定义 JSON 字符串</returns>
        public static string GetToolsJson()
        {
            return "{\"group\":\"QqBotCat\",\"tools\":[" +
                "{\"name\":\"qqbot-send\",\"description\":\"向 QQ 群发送一条主动消息（Markdown）。群用别名寻址即可（别名 / 群名 / OpenID / 唯一前缀都认，省略则为该机器人目录中的唯一群），不必记 OpenID。回执会回报实际投递目标（别名 + 群名 + OpenID 末 8 位）与成败——失败带错误码，不是静默。猫级授权：仅当本猫已绑定该机器人、开启发言且群在其渠道白名单内时可发（否则回 ERR|QQBOT_* 并说明原因）。主动消息受平台频控（20 条/分钟/群、1000 条/群/天），且要求群主已开启「机器人主动在群聊内发言」。\",\"parameters\":{\"type\":\"object\",\"properties\":{\"text\":{\"type\":\"string\",\"description\":\"正文（支持 Markdown）\"},\"group\":{\"type\":\"string\",\"description\":\"目标群：别名 / 群名 / OpenID / 唯一前缀；省略=目录内唯一群\"},\"bot\":{\"type\":\"string\",\"description\":\"机器人配置 ID（省略=池内默认机器人）\"}},\"required\":[\"text\"]}}," +
                "{\"name\":\"qqbot-info\",\"description\":\"查看本通道自述：本猫绑定的机器人（未绑定时如实说明，不返回空表）的连接状态、接入环境（sandbox/production）与可收信的群清单摘要。用于确认「我能用哪个机器人、连没连上、在哪些群里」。\",\"parameters\":{\"type\":\"object\",\"properties\":{},\"required\":[]}}," +
                "{\"name\":\"qqbot-botinfo\",\"description\":\"查看单个机器人的详情：逐个群列出别名 / 群名 / 机器人在群内角色 / 群收信范围 / 是否允许主动发言 / 最近活跃时间，并给出该机器人的发言白名单。refresh=true 时顺带调官方接口刷新权限与群名（该接口仅白名单机器人可用，不可用时降级为本地观测并在回执里明示）。\",\"parameters\":{\"type\":\"object\",\"properties\":{\"bot\":{\"type\":\"string\",\"description\":\"机器人配置 ID（省略=默认机器人）\"},\"refresh\":{\"type\":\"boolean\",\"description\":\"是否调官方接口刷新权限与群名（默认 false）\"}},\"required\":[]}}," +
                "{\"name\":\"qqbot-groups\",\"description\":\"维护并查看某机器人的群目录（qqbot-send 的寻址来源）。action=list 列群（群名 / 别名 / OpenID / 角色 / 收信范围 / 主动发言 / 最近活跃）；action=alias 给群设别名（group 定位目标群，alias 新别名，留空=清除；同一机器人内别名唯一）；action=add 手动登记一个尚未说过话的群（group=群 OpenID）；action=refresh 逐群调官方接口刷新群名与群内权限（失败的群标为「未确认」，不再沿用旧权限值）；action=remove 从目录移除一个群（被踢出 / 不再使用的群；只清本地目录，平台侧归属不变）。群在机器人收到群消息时自动入目录。\",\"parameters\":{\"type\":\"object\",\"properties\":{\"action\":{\"type\":\"string\",\"description\":\"list（列群，默认）/ alias（设别名）/ add（登记群）/ refresh（刷官方权限）/ remove（移除条目）\"},\"group\":{\"type\":\"string\",\"description\":\"目标群：别名 / 群名 / OpenID / 唯一前缀（action=add 时填群 OpenID）\"},\"alias\":{\"type\":\"string\",\"description\":\"新别名（action=alias 时使用；空=清除别名）\"},\"bot\":{\"type\":\"string\",\"description\":\"机器人配置 ID（省略=池内默认机器人）\"}},\"required\":[]}}" +
                "]}";
        }
    }
}
// #MAU_CHECKSUM:SHA256:56A422D332DF68327CFD289F17B10075500D3AF5F5791822E6C720C346413DCA
