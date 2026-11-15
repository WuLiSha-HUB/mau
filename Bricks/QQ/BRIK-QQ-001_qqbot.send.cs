// ═══════════════════════════════════════════════════
// 积木: qqbot.send
// ID:   BRIK-QQ-001
// 类别: QQ
// 作用: 主动发言到指定 QQ 群——群寻址支持别名 / 群名 / OpenID / 唯一前缀（空=目录唯一群）；回执带实际投递目标
// 依赖: 无
// 引用: Mau.Contracts（IQQBotOps）/ Mau.Runtime（DataBox/JsonArgs）
// 原理: DataBox.TryResolve<IQQBotOps> → SendProactiveTo(bot, group, text)
//       白名单闸、别名解析、频控与错误码全在服务面（宿主侧单点）；本积木只做参数校验与回执整形
// 常用: qqbot_cat.mau 认领线——'qqbot.send'[@args] > @result
// 门禁: 出口仅主动消息（无 msg_id）——命令回复走被动通道，两条路径不混用（A379）
// ═══════════════════════════════════════════════════
using System;
using Mau.Contracts;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// QQ 主动发言积木——qqbot-send 工具执行面（A380）。
    /// </summary>
    public static class QqBotSendBrick
    {
        /// <summary>
        /// 发送主动消息到群。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（bot / group / text；catId 为宿主注入保留键）</param>
        /// <param name="result">回执文本或 ERR| 错误文本</param>
        /// <returns>true=执行成功</returns>
        public static bool Send(string argsJson, out string result)
        {
            result = "";
            string badArgs = JsonArgs.Validate(argsJson, "bot group text", "text", "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            string bot = JsonArgs.Get(argsJson, "bot");
            string group = JsonArgs.Get(argsJson, "group");
            string text = JsonArgs.Get(argsJson, "text");
            if (text.Trim().Length == 0)
            {
                result = "ERR|BAD_ARGS|缺少参数 text";
                return false;
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
                string body = service.SendProactiveTo(bot, group, text);
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
// #MAU_CHECKSUM:SHA256:1874256C8013B6BF7443C2ED8F143EFC9464B3B860997972D55CB53A875EEB20
