// ═══════════════════════════════════════════════════
// 积木: group.catcfg_set
// ID:   BRIK-GROUP-008
// 类别: GROUP
// 作用: 每猫配置写入（group-catcfg-set）——字段级合并写，仅组管理员且目标不含调用者自己
// 依赖: 无
// 引用: Mau.Runtime（DataBox / IGroupService）
// 原理: DataBox.TryResolve<IGroupService> → CatCfgSet(argsJson) → 回执原文（A214 头 + 摘要行 + 落盘实况，或 ERR|）
// 常用: group_cat.mau 认领线——'group.catcfg_set'[@args] > @res
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 每猫配置写入积木——group.catcfg_set 工具（GroupCat 工具组 Flow 认领线消费）。
    /// 权限（组管理员）、拒改自己、字段白名单与受限工具名单均归宿主实现（IGroupService → CatHostService）；
    /// 本积木只做服务寻址与空回执保护。
    /// </summary>
    public static class GroupCatCfgSetBrick
    {
        /// <summary>
        /// 写入目标猫配置——服务缺失 / 空回执一律 ERR| 前缀（不静默）。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（group / cat / field / value 必填；catId 为宿主注入保留键）</param>
        /// <param name="result">结构化回执或 ERR| 文本</param>
        /// <returns>true=成功</returns>
        public static bool CatCfgSet(string argsJson, out string result)
        {
            result = "";
            IGroupService service = null!;
            if (!DataBox.TryResolve<IGroupService>(out service) || service == null)
            {
                result = "ERR|GROUP_NO_SERVICE|群组服务未注入（宿主未接线）";
                return false;
            }
            string reply = service.CatCfgSet(argsJson);
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
// #MAU_CHECKSUM:SHA256:F5FEE0E4D9A4F412AEEBA999F1C4CD0307710E8C6595AD75FFDE1CBC5A64FBD4
