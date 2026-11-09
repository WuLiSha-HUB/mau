// ═══════════════════════════════════════════════════
// 积木: group.catcfg_get
// ID:   BRIK-GROUP-007
// 类别: GROUP
// 作用: 每猫配置读取（group-catcfg-get）——cat.cfg 全量字段，范围限本组成员
// 依赖: 无
// 引用: Mau.Runtime（DataBox / IGroupService）
// 原理: DataBox.TryResolve<IGroupService> → CatCfgGet(argsJson) → 回执原文（A214 头 + 摘要行 + 字段行，或 ERR|）
// 常用: group_cat.mau 认领线——'group.catcfg_get'[@args] > @res
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 每猫配置读取积木——group.catcfg_get 工具（GroupCat 工具组 Flow 认领线消费）。
    /// 权限（组成员）与字段面归宿主实现（IGroupService → CatHostService.HandleCatCfgGet）；本积木只做服务寻址与空回执保护。
    /// </summary>
    public static class GroupCatCfgGetBrick
    {
        /// <summary>
        /// 读取目标猫配置——服务缺失 / 空回执一律 ERR| 前缀（不静默）。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（group / cat 必填；catId 为宿主注入保留键）</param>
        /// <param name="result">结构化回执或 ERR| 文本</param>
        /// <returns>true=成功</returns>
        public static bool CatCfgGet(string argsJson, out string result)
        {
            result = "";
            IGroupService service = null!;
            if (!DataBox.TryResolve<IGroupService>(out service) || service == null)
            {
                result = "ERR|GROUP_NO_SERVICE|群组服务未注入（宿主未接线）";
                return false;
            }
            string reply = service.CatCfgGet(argsJson);
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
// #MAU_CHECKSUM:SHA256:B9F5E7F9A5C6E9DD9A3EB658696C1C61FC66D1F91006D0D1D41C1175EF6AA47A
