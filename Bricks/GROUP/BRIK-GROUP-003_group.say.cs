// ═══════════════════════════════════════════════════
// 积木: group.say
// ID:   BRIK-GROUP-003
// 类别: GROUP
// 作用: 发讨论消息（note）——只写组消息流，不投递
// 依赖: 无
// 引用: Mau.Runtime（DataBox / IGroupService）
// 原理: DataBox.TryResolve<IGroupService> → Say(argsJson) → 回执原文（A214 头 + 摘要行，或 ERR|）
// 常用: group_cat.mau 认领线——'group.say'[@args] > @res
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 组讨论消息积木——group.say 工具（GroupCat 工具组 Flow 认领线消费）。
    /// 权限与参数校验归宿主实现（IGroupService）；本积木只做服务寻址与空回执保护。
    /// </summary>
    public static class GroupSayBrick
    {
        /// <summary>
        /// 发一条 note——服务缺失 / 空回执一律 ERR| 前缀（不静默）。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（group / text 必填；to 可选）</param>
        /// <param name="result">JSON 文本或 ERR| 文本</param>
        /// <returns>true=成功</returns>
        public static bool Say(string argsJson, out string result)
        {
            result = "";
            IGroupService service = null!;
            if (!DataBox.TryResolve<IGroupService>(out service) || service == null)
            {
                result = "ERR|GROUP_NO_SERVICE|群组服务未注入（宿主未接线）";
                return false;
            }
            string reply = service.Say(argsJson);
            if (reply == null || reply.Length == 0)
            {
                result = "ERR|EMPTY_RESULT|群组服务无回执";
                return false;
            }
            result = reply;
            return !reply.StartsWith("ERR|", StringComparison.Ordinal);
        }
    }
}
// #MAU_CHECKSUM:SHA256:A9142B9054C52DD260E21E3861A8094E17C0537ED962DD24124FE2BC670EEE3F
