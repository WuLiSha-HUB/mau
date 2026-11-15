// ═══════════════════════════════════════════════════
// 积木: qqbot.botinfo
// ID:   BRIK-QQ-003
// 类别: QQ
// 作用: 单机器人详情——群目录逐条（别名 / 群名 / 角色 / 收信范围 / 主动发言可用性）+ 发言白名单
// 依赖: 无
// 引用: Mau.Contracts（IQQBotOps）/ Mau.Runtime（DataBox/JsonArgs）
// 原理: DataBox.TryResolve<IQQBotOps> → BotDetailText(botKey, refresh)
//       refresh=true 时顺带调官方 bot_state / group_info（白名单接口，11253 时降级本地观测并明示）
// 常用: qqbot_cat.mau 认领线——'qqbot.botinfo'[@args] > @result
// 门禁: 与硬编码 /botinfo 同源实现（同一服务方法）
// ═══════════════════════════════════════════════════
using System;
using Mau.Contracts;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 机器人详情积木——qqbot-botinfo 工具执行面（A380）。
    /// </summary>
    public static class QqBotBotInfoBrick
    {
        /// <summary>
        /// 取单机器人详情。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（bot / refresh；catId 为宿主注入保留键）</param>
        /// <param name="result">详情文本或 ERR| 错误文本</param>
        /// <returns>true=执行成功</returns>
        public static bool BotInfo(string argsJson, out string result)
        {
            result = "";
            string badArgs = JsonArgs.Validate(argsJson, "bot catId refresh", "", "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            string bot = JsonArgs.Get(argsJson, "bot");
            bool refresh = false;
            string refreshText = JsonArgs.Get(argsJson, "refresh");
            if (string.Equals(refreshText, "true", StringComparison.OrdinalIgnoreCase))
            {
                refresh = true;
            }
            try
            {
                IQQBotOps? service;
                DataBox.TryResolve<IQQBotOps>(out service);
                if (service == null)
                {
                    result = "ERR|QQBOT_NO_SERVICE|宿主未注入 IQQBotOps";
                    return false;
                }
                string body = service.BotDetailText(bot, refresh);
                result = body;
                return !body.StartsWith("ERR|", StringComparison.Ordinal);
            }
            catch (Exception ex)
            {
                result = "ERR|QQBOT_BRICK|" + ex.GetType().Name + ": " + ex.Message;
                return false;
            }
        }
    }
}
// #MAU_CHECKSUM:SHA256:F3F0F9A8DB7308DFF32DEA8A4C9817BCC7C710DB2F2FDC8E5E4EB6162ACF7392
