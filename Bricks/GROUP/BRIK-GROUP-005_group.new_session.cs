// ═══════════════════════════════════════════════════
// 积木: group.new_session
// ID:   BRIK-GROUP-005
// 类别: GROUP
// 作用: 重建会话（group-newsession）——清前文 + 重注入 + 可选注入首条消息（宿主实现内完成）
// 依赖: 无
// 引用: Mau.Runtime（DataBox / IGroupService）
// 原理: DataBox.TryResolve<IGroupService> → NewSession(argsJson) → 回执原文（A214 头 + 摘要行，或 ERR|）
// 常用: group_cat.mau 认领线——'group.new_session'[@args] > @res
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 重建会话积木——group.new_session 工具（GroupCat 工具组 Flow 认领线消费）。
    /// 权限（管理员名单）、目标空闲判定与注入语义归宿主实现（IGroupService）；本积木只做服务寻址与空回执保护。
    /// </summary>
    public static class GroupNewSessionBrick
    {
        /// <summary>
        /// 重建目标会话——服务缺失 / 空回执一律 ERR| 前缀（不静默）。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（group / to / reason 必填；firstMessage 可选）</param>
        /// <param name="result">JSON 文本或 ERR| 文本</param>
        /// <returns>true=成功</returns>
        public static bool NewSession(string argsJson, out string result)
        {
            result = "";
            IGroupService service = null!;
            if (!DataBox.TryResolve<IGroupService>(out service) || service == null)
            {
                result = "ERR|GROUP_NO_SERVICE|群组服务未注入（宿主未接线）";
                return false;
            }
            string reply = service.NewSession(argsJson);
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
// #MAU_CHECKSUM:SHA256:C24293136527FD99290202328A09F892F240C56CAABCE17F2C8BEA12D71C7F92
