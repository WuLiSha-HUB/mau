// ═══════════════════════════════════════════════════
// 积木: werewolf.events
// ID:   BRIK-WEREWOLF-003
// 类别: WEREWOLF
// 作用: 增量事件流——发言 / 投票 / 死讯 / 阶段切换 / 轮次汇总，已按调用者视角裁剪
// 依赖: 无
// 引用: Mau.Contracts（IWerewolfOps）/ Mau.Runtime（DataBox/JsonArgs）
// 原理: DataBox.TryResolve<IWerewolfOps> → Events(catId, after)——增量不重不漏（after 不含边界）
// 常用: ww_cat.mau 认领线——'werewolf.events'[@args] > @result
// 门禁: after 非法即拒（不静默归零）；负值归零、缺失取 0
// ═══════════════════════════════════════════════════
using System;
using Mau.Contracts;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 狼人杀事件增量积木——ww-events 工具执行面（A409）。
    /// </summary>
    public static class WerewolfEventsBrick
    {
        /// <summary>
        /// 取增量事件。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（after；catId 为宿主注入保留键）</param>
        /// <param name="result">事件数组 JSON 或 ERR| 错误文本</param>
        /// <returns>true=执行成功</returns>
        public static bool Events(string argsJson, out string result)
        {
            result = "";
            string badArgs = JsonArgs.Validate(argsJson, "catId after", "", "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            string afterText = JsonArgs.Get(argsJson, "after");
            int after = 0;
            if (afterText.Length > 0)
            {
                if (!int.TryParse(afterText, out after))
                {
                    result = "ERR|BAD_ARGS|after 须为非负整数: " + afterText;
                    return false;
                }
            }
            string catId = JsonArgs.Get(argsJson, "catId");
            try
            {
                IWerewolfOps? service;
                DataBox.TryResolve<IWerewolfOps>(out service);
                if (service == null)
                {
                    result = "ERR|WW_NO_SERVICE|宿主未注入 IWerewolfOps";
                    return false;
                }
                string body = service.Events(catId, after);
                result = body;
                return !body.StartsWith("ERR|", StringComparison.Ordinal);
            }
            catch (Exception ex)
            {
                result = "ERR|WW_BRICK|" + ex.GetType().Name + ": " + ex.Message;
                return false;
            }
        }
    }
}
// #MAU_CHECKSUM:SHA256:3C3BEEBF47AA5AF01BDA9BFAF23EE5C13FEF9A10AC8A843A8837288658D1284E
