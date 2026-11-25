// ═══════════════════════════════════════════════════
// 积木: werewolf.whoami
// ID:   BRIK-WEREWOLF-002
// 类别: WEREWOLF
// 作用: 我是谁——局号 / 座位 / 角色 / 存活（排错用：怀疑自己角色时先跑这个）
// 依赖: 无
// 引用: Mau.Contracts（IWerewolfOps）/ Mau.Runtime（DataBox/JsonArgs）
// 原理: DataBox.TryResolve<IWerewolfOps> → WhoAmI(catId)
// 常用: ww_cat.mau 认领线——'werewolf.whoami'[@args] > @result
// 门禁: 回执不含 X-Werewolf-Key——猫只知座位号
// ═══════════════════════════════════════════════════
using System;
using Mau.Contracts;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 狼人杀身份自省积木——ww-whoami 工具执行面（A409）。
    /// </summary>
    public static class WerewolfWhoAmIBrick
    {
        /// <summary>
        /// 取「我是谁」。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（无业务参数；catId 为宿主注入保留键）</param>
        /// <param name="result">视图 JSON 或 ERR| 错误文本</param>
        /// <returns>true=执行成功</returns>
        public static bool WhoAmI(string argsJson, out string result)
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
                string body = service.WhoAmI(catId);
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
// #MAU_CHECKSUM:SHA256:665FE30FD1E4E5E620BDE201F9297949BBA5E5D4CCA7D02B97575D503A2EA8C3
