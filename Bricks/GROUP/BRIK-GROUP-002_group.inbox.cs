// ═══════════════════════════════════════════════════
// 积木: group.inbox
// ID:   BRIK-GROUP-002
// 类别: GROUP
// 作用: 拉取组消息流（after / limit）——调用者须为本组成员
// 依赖: 无
// 引用: Mau.Runtime（DataBox / IGroupService）
// 原理: DataBox.TryResolve<IGroupService> → Inbox(argsJson) → 回执原文（JSON 或 ERR|）
// 常用: group_cat.mau 认领线——'group.inbox'[@args] > @res
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 组消息拉取积木——group.inbox 工具（GroupCat 工具组 Flow 认领线消费）。
    /// 权限与参数校验归宿主实现（IGroupService）；本积木只做服务寻址与空回执保护。
    /// </summary>
    public static class GroupInboxBrick
    {
        /// <summary>
        /// 拉取组消息——服务缺失 / 空回执一律 ERR| 前缀（不静默）。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（group 必填；after / limit 可选）</param>
        /// <param name="result">JSON 文本或 ERR| 文本</param>
        /// <returns>true=成功</returns>
        public static bool Inbox(string argsJson, out string result)
        {
            result = "";
            IGroupService service = null!;
            if (!DataBox.TryResolve<IGroupService>(out service) || service == null)
            {
                result = "ERR|GROUP_NO_SERVICE|群组服务未注入（宿主未接线）";
                return false;
            }
            string reply = service.Inbox(argsJson);
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
// #MAU_CHECKSUM:SHA256:70D4F386B3BC5B30500BE5A603DF3823794EE76E9A70B218F3EB36C521B705A7
