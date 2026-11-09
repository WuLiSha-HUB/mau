// ═══════════════════════════════════════════════════
// 积木: group.catinfo
// ID:   BRIK-GROUP-006
// 类别: GROUP
// 作用: 单猫运行态统计（group-catinfo）——组成员可读任一成员（与 cat.info 单条同源）
// 依赖: 无
// 引用: Mau.Runtime（DataBox / IGroupService）
// 原理: DataBox.TryResolve<IGroupService> → CatInfo(argsJson) → 回执原文（A214 头 + 摘要行 + 正文行，或 ERR|）
// 常用: group_cat.mau 认领线——'group.catinfo'[@args] > @res
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 单猫运行态积木——group.catinfo 工具（GroupCat 工具组 Flow 认领线消费）。
    /// 权限（组成员）与字段面归宿主实现（IGroupService → CatHostService）；本积木只做服务寻址与空回执保护。
    /// </summary>
    public static class GroupCatInfoBrick
    {
        /// <summary>
        /// 读取单猫运行态——服务缺失 / 空回执一律 ERR| 前缀（不静默）。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（group / cat 必填；catId 为宿主注入保留键）</param>
        /// <param name="result">结构化回执或 ERR| 文本</param>
        /// <returns>true=成功</returns>
        public static bool CatInfo(string argsJson, out string result)
        {
            result = "";
            IGroupService service = null!;
            if (!DataBox.TryResolve<IGroupService>(out service) || service == null)
            {
                result = "ERR|GROUP_NO_SERVICE|群组服务未注入（宿主未接线）";
                return false;
            }
            string reply = service.CatInfo(argsJson);
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
// #MAU_CHECKSUM:SHA256:74F626B70BC45DB9A5BBD11721D651984EDF915BCB543C983C4F215E3A58CB83
