// ═══════════════════════════════════════════════════
// 积木: group.clear
// ID:   BRIK-GROUP-009
// 类别: GROUP
// 作用: 清空组消息流（group-clear）——旧流原地归档为 .bak 后重建空流，仅组管理员
// 依赖: 无
// 引用: Mau.Runtime（DataBox / IGroupService）
// 原理: DataBox.TryResolve<IGroupService> → Clear(argsJson) → 回执原文（A214 头 + 摘要行 + 正文，或 ERR|）
// 常用: group_cat.mau 认领线——'group.clear'[@args] > @res
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 清空组消息流积木——group.clear 工具（GroupCat 工具组 Flow 认领线消费）。
    /// 权限（组管理员名单）、旧流归档与统计 / 订阅游标归零语义归宿主实现
    /// （IGroupService.Clear → GroupStore.ClearMessages，与面板清空按钮同一单点）；
    /// 本积木只做服务寻址与空回执保护。
    /// </summary>
    public static class GroupClearBrick
    {
        /// <summary>
        /// 清空目标组消息流——服务缺失 / 空回执一律 ERR| 前缀（不静默）。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（group 必填；catId 为宿主注入保留键）</param>
        /// <param name="result">结构化回执或 ERR| 文本</param>
        /// <returns>true=成功</returns>
        public static bool Clear(string argsJson, out string result)
        {
            result = "";
            IGroupService service = null!;
            if (!DataBox.TryResolve<IGroupService>(out service) || service == null)
            {
                result = "ERR|GROUP_NO_SERVICE|群组服务未注入（宿主未接线）";
                return false;
            }
            string reply = service.Clear(argsJson);
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
// #MAU_CHECKSUM:SHA256:FF878EA413B0B79257DAE8E209E246ED5EBD256898DFBAA7A8E80AB2A4971046
