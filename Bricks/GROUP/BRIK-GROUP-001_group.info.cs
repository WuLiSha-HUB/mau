// ═══════════════════════════════════════════════════
// 积木: group.info
// ID:   BRIK-GROUP-001
// 类别: GROUP
// 作用: 组信息读取（成员 / 管理员 / 统计）——调用者须为本组成员
// 依赖: 无
// 引用: Mau.Runtime（DataBox / IGroupService）
// 原理: DataBox.TryResolve<IGroupService> → Info(argsJson) → 回执原文（A214 头 + 摘要行 + 正文行，或 ERR|）
// 常用: group_cat.mau 认领线——'group.info'[@args] > @res
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 组信息积木——group.info 工具（GroupCat 工具组 Flow 认领线消费）。
    /// 权限与参数校验归宿主实现（IGroupService）；本积木只做服务寻址与空回执保护。
    /// </summary>
    public static class GroupInfoBrick
    {
        /// <summary>
        /// 读取组信息——服务缺失 / 空回执一律 ERR| 前缀（不静默）。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（group 必填；catId 为宿主注入保留键）</param>
        /// <param name="result">JSON 文本或 ERR| 文本</param>
        /// <returns>true=成功</returns>
        public static bool Info(string argsJson, out string result)
        {
            result = "";
            IGroupService service = null!;
            if (!DataBox.TryResolve<IGroupService>(out service) || service == null)
            {
                result = "ERR|GROUP_NO_SERVICE|群组服务未注入（宿主未接线）";
                return false;
            }
            string reply = service.Info(argsJson);
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
// #MAU_CHECKSUM:SHA256:04EE799CE961B4B7A5CA8D5A2C17CC03F04E99ABCD53A6A4D9CD1ADF178E05BF
