// ═══════════════════════════════════════════════════
// 积木: werewolf.state
// ID:   BRIK-WEREWOLF-001
// 类别: WEREWOLF
// 作用: 读取当前牌面——按调用者座位裁剪的视图（阶段 / 轮次 / 我是谁 / 存活名单 / 合法动作 / 私有知识）
// 依赖: 无
// 引用: Mau.Contracts（IWerewolfOps）/ Mau.Runtime（DataBox/JsonArgs）
// 原理: DataBox.TryResolve<IWerewolfOps> → State(catId)——身份由宿主注入，本体地址与凭证落宿主侧
//       零业务判断：合法动作枚举由本体给，猫照 legalActions 做即可
// 常用: ww_cat.mau 认领线——'werewolf.state'[@args] > @result
// 门禁: 参数面禁 key/seat/role/server——身份只经 catId 注入；回执不出凭证
// ═══════════════════════════════════════════════════
using System;
using Mau.Contracts;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 狼人杀牌面读取积木——ww-state 工具执行面（A409）。
    /// </summary>
    public static class WerewolfStateBrick
    {
        /// <summary>
        /// 取当前牌面（按调用者座位裁剪）。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（无业务参数；catId 为宿主注入保留键）</param>
        /// <param name="result">视图 JSON 或 ERR| 错误文本</param>
        /// <returns>true=执行成功</returns>
        public static bool State(string argsJson, out string result)
        {
            result = "";
            string badArgs = JsonArgs.Validate(argsJson, "catId", "", "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
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
                string body = service.State(catId);
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
// #MAU_CHECKSUM:SHA256:D0A973F19DD804FB2038C7024272236E9D14FE89814054D716102E05514100FE
