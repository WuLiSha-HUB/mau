// ═══════════════════════════════════════════════════
// 积木: werewolf.act
// ID:   BRIK-WEREWOLF-004
// 类别: WEREWOLF
// 作用: 提交一个动作——speak / vote / abstain / lastWords / wolfChat / wolfKill / seerCheck / witchSave / witchPoison / witchNone
// 依赖: 无
// 引用: Mau.Contracts（IWerewolfOps）/ Mau.Runtime（DataBox/JsonArgs）
// 原理: DataBox.TryResolve<IWerewolfOps> → Act(catId, action, target, text)
//       🔴 薄壳——合法性由本体裁决（PHASE_DENIED / ALREADY_ACTED / TARGET_INVALID…直通不吞），本积木零业务判断
// 常用: ww_cat.mau 认领线——'werewolf.act'[@args] > @result
// 门禁: action 枚举与本体 ActionType 同名同形；target 空 = 不带该字段（空刀 / 弃票由枚举表达）
// ═══════════════════════════════════════════════════
using System;
using Mau.Contracts;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 狼人杀动作提交积木——ww-act 工具执行面（A409）。
    /// </summary>
    public static class WerewolfActBrick
    {
        /// <summary>
        /// 提交动作。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（action / target / text；catId 为宿主注入保留键）</param>
        /// <param name="result">受理回执 JSON 或 ERR| 错误文本</param>
        /// <returns>true=执行成功</returns>
        public static bool Act(string argsJson, out string result)
        {
            result = "";
            string allowed = "catId action target text";
            string enums = "speak|vote|abstain|lastWords|wolfChat|wolfKill|seerCheck|witchSave|witchPoison|witchNone";
            string badArgs = JsonArgs.Validate(argsJson, allowed, "action", "action", enums);
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            string action = JsonArgs.Get(argsJson, "action");
            string target = JsonArgs.Get(argsJson, "target");
            string text = JsonArgs.Get(argsJson, "text");
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
                string body = service.Act(catId, action, target, text);
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
// #MAU_CHECKSUM:SHA256:CED42035778D31B6D8BD51CCE969C4615FC2575C0E676D979D446FFFDCE8C6FF
