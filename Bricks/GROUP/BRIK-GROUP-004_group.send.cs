// ═══════════════════════════════════════════════════
// 积木: group.send
// ID:   BRIK-GROUP-004
// 类别: GROUP
// 作用: 派单（delegation）——写组消息 + 以 origin 投递目标猫（宿主实现内顺序完成）
// 依赖: 无
// 引用: Mau.Runtime（DataBox / IGroupService）
// 原理: DataBox.TryResolve<IGroupService> → Send(argsJson) → 回执原文（JSON 或 ERR|）
// 常用: group_cat.mau 认领线——'group.send'[@args] > @res
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 派单积木——group.send 工具（GroupCat 工具组 Flow 认领线消费）。
    /// 权限（管理员名单）与参数校验归宿主实现（IGroupService）；本积木只做服务寻址与空回执保护。
    /// </summary>
    public static class GroupSendBrick
    {
        /// <summary>
        /// 写 delegation 并投递目标猫——服务缺失 / 空回执一律 ERR| 前缀（不静默）。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（group / to / text 必填；refs 可选）</param>
        /// <param name="result">JSON 文本或 ERR| 文本</param>
        /// <returns>true=成功</returns>
        public static bool Send(string argsJson, out string result)
        {
            result = "";
            IGroupService service = null!;
            if (!DataBox.TryResolve<IGroupService>(out service) || service == null)
            {
                result = "ERR|GROUP_NO_SERVICE|群组服务未注入（宿主未接线）";
                return false;
            }
            string reply = service.Send(argsJson);
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
// #MAU_CHECKSUM:SHA256:DF206FCFBB80153E4EB06C8E5356770582EA11249690C95D9C8334F26B0FDE3A
