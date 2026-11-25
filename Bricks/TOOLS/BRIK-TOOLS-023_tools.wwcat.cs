// ═══════════════════════════════════════════════════
// 积木: tools.wwcat
// ID:   BRIK-TOOLS-023
// 类别: TOOLS
// 作用: 返回狼人杀工具组全部工具的 OpenAI 兼容定义 JSON（宿主工具池拼装原料）
// 依赖: 无
// 引用: 无（纯文本常量）
// 原理: GetToolsJson() 返回本组工具定义整包 JSON——改描述只动本文件，mau proj 重建即生效
//       命名铁律：工具定义积木约定名 = tools.<flowName 小写>（ww_cat.mau → WwCat → tools.wwcat）
//       JSON group 取 .mauproj 声明的组名（本组 = WwCat）
// 常用: FL_WwCat.GetToolsJson() 自曝调用面（IFlow 接口）
// 门禁: 参数面禁出现 key / seat / role / server——身份由宿主注入（catId），本体验证
// ═══════════════════════════════════════════════════
namespace Mau.Bricks
{
    /// <summary>
    /// 工具定义积木——狼人杀组工具定义 JSON（A409）。
    /// 四件：牌面读取 / 身份自省 / 事件增量 / 动作提交。
    /// 门禁：猫永远不知道自己是哪个 Key——参数面与回执面均无凭证字段。
    /// </summary>
    public static class ToolsWwcatBrick
    {
        /// <summary>
        /// 获取本组工具定义 JSON——{"group":"WwCat","tools":[{name,description,parameters}]}
        /// </summary>
        /// <returns>工具定义 JSON 字符串</returns>
        public static string GetToolsJson()
        {
            return "{\"group\":\"WwCat\",\"tools\":[" +
                "{\"name\":\"ww-state\",\"description\":\"查看当前狼人杀牌面（按你的座位裁剪）：阶段（phase）/ 轮次（turn · speechRound/speechRounds）/ 我是谁（座位·角色·存活）/ 存活名单 / legalActions（此刻你能做的动作枚举与参数域）/ 私有知识（狼队友 · 验人结果 · 药水）。每步动手前先看它——合法动作以 legalActions 为准，不要凭记忆猜阶段。身份由宿主注入，你不需要也不应该知道自己的凭证。\",\"parameters\":{\"type\":\"object\",\"properties\":{},\"required\":[]}}," +
                "{\"name\":\"ww-whoami\",\"description\":\"查看「我是谁」：局号 / 座位 / 角色 / 是否存活。排错用——怀疑自己角色或座位时先跑这个。\",\"parameters\":{\"type\":\"object\",\"properties\":{},\"required\":[]}}," +
                "{\"name\":\"ww-events\",\"description\":\"拉取增量事件流（发言 / 投票 / 死讯 / 阶段切换 / 轮次汇总），已按你的视角裁剪——狼队友的狼队讨论只有狼看得到，预言家的验人结果只有预言家看得到。after 传上次读到的最大 seq（不含该条），缺省 0 从头读；增量不重不漏。\",\"parameters\":{\"type\":\"object\",\"properties\":{\"after\":{\"type\":\"integer\",\"description\":\"起始序号（不含；缺省 0=从头）\"}},\"required\":[]}}," +
                "{\"name\":\"ww-act\",\"description\":\"提交一个动作。action 取值与 legalActions 里的枚举同名同形：speak（发言·需 text）/ vote（投票·需 target）/ abstain（弃票）/ lastWords（遗言·需 text）/ wolfChat（狼队讨论·需 text）/ wolfKill（刀口·需 target，不带 target=空刀）/ seerCheck（查验·需 target）/ witchSave（女巫救人）/ witchPoison（女巫毒人·需 target）/ witchNone（女巫本夜不用药）。合法性由本体裁决——被拒会带回本体错误码（PHASE_DENIED / ALREADY_ACTED / TARGET_INVALID 等），照回执改即可，不要重试同一动作。\",\"parameters\":{\"type\":\"object\",\"properties\":{\"action\":{\"type\":\"string\",\"description\":\"动作名（speak / vote / abstain / lastWords / wolfChat / wolfKill / seerCheck / witchSave / witchPoison / witchNone）\"},\"target\":{\"type\":\"integer\",\"description\":\"目标座位号（vote / wolfKill / seerCheck / witchPoison 使用）\"},\"text\":{\"type\":\"string\",\"description\":\"正文（speak / lastWords / wolfChat 使用）\"}},\"required\":[\"action\"]}}" +
                "]}";
        }
    }
}
// #MAU_CHECKSUM:SHA256:ACC3D96ABCD914B5087CC8A9EE533780A329EC43A871C7001F7930833955189F
