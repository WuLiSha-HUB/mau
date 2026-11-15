// ═══════════════════════════════════════════════════
// 积木: qqbot.info
// ID:   BRIK-QQ-002
// 类别: QQ
// 作用: QQ 通道自述——池内每个机器人的连接状态、环境与群目录摘要（观察者视角）
// 依赖: 无
// 引用: Mau.Contracts（IQQBotOps）/ Mau.Runtime（DataBox/JsonArgs）
// 原理: DataBox.TryResolve<IQQBotOps> → MyChannelText(catId)
//       与硬编码 /botinfo 同源（同一服务实现），区别在粒度：/botinfo 面向群内人，本工具面向调用方猫
// 常用: qqbot_cat.mau 认领线——'qqbot.info'[@args] > @result
// 门禁: A381 前不带猫级过滤——catId 只记入回执头
// ═══════════════════════════════════════════════════
using System;
using Mau.Contracts;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// QQ 通道自述积木——qqbot-info 工具执行面（A380）。
    /// </summary>
    public static class QqBotInfoBrick
    {
        /// <summary>
        /// 取本通道自述。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（无业务参数；catId 为宿主注入保留键）</param>
        /// <param name="result">自述文本或 ERR| 错误文本</param>
        /// <returns>true=执行成功</returns>
        public static bool Info(string argsJson, out string result)
        {
            result = "";
            string badArgs = JsonArgs.Validate(argsJson, "catId", "", "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            string catId = JsonArgs.Get(argsJson, "catId");
            try
            {
                IQQBotOps? service;
                DataBox.TryResolve<IQQBotOps>(out service);
                if (service == null)
                {
                    result = "ERR|QQBOT_NO_SERVICE|宿主未注入 IQQBotOps";
                    return false;
                }
                string body = service.MyChannelText(catId);
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
// #MAU_CHECKSUM:SHA256:F724ED23AE4787CF4D7E339CE8D29582353D6A1699EB25316343A7F633743882
