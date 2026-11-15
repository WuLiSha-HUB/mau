// ═══════════════════════════════════════════════════
// 积木: qqbot.groups
// ID:   BRIK-QQ-004
// 类别: QQ
// 作用: 群目录维护——action=list 列群 / alias 设别名 / add 登记群 / refresh 刷官方权限（群名 + 群内权限）
// 依赖: 无
// 引用: Mau.Contracts（IQQBotOps）/ Mau.Runtime（DataBox/JsonArgs）
// 原理: DataBox.TryResolve<IQQBotOps> → GroupsOp(bot, action, group, alias)
//       别名唯一性、白名单、官方接口降级全在服务面（宿主侧单点）；本积木只做参数校验与回执整形
// 常用: qqbot_cat.mau 认领线——'qqbot.groups'[@args] > @grRes
// 门禁: 群名与权限来自官方接口（受白名单限制）；取不到时降级本地观测并在回执标「未知」（A380）
// ═══════════════════════════════════════════════════
using System;
using Mau.Contracts;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// QQ 群目录积木——qqbot-groups 工具执行面（A380）。
    /// </summary>
    public static class QqBotGroupsBrick
    {
        /// <summary>
        /// 群目录操作。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（bot / action / group / alias）</param>
        /// <param name="result">回执文本或 ERR| 错误文本</param>
        /// <returns>true=执行成功</returns>
        public static bool Run(string argsJson, out string result)
        {
            result = "";
            string badArgs = JsonArgs.Validate(argsJson, "bot action group alias", "", "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            string bot = JsonArgs.Get(argsJson, "bot");
            string action = JsonArgs.Get(argsJson, "action");
            string group = JsonArgs.Get(argsJson, "group");
            string alias = JsonArgs.Get(argsJson, "alias");
            try
            {
                IQQBotOps? service;
                DataBox.TryResolve<IQQBotOps>(out service);
                if (service == null)
                {
                    result = "ERR|QQBOT_NO_SERVICE|宿主未注入 IQQBotOps";
                    return false;
                }
                string body = service.GroupsOp(bot, action, group, alias);
                result = body;
                return !body.StartsWith("ERR|", StringComparison.Ordinal);
            }
            catch (Exception ex)
            {
                result = "ERR|QQBOT_BRICK|" + ex.GetType().Name + ": " + ex.Message;
                return false;
            }
        }
    }
}
// #MAU_CHECKSUM:SHA256:00ED2AA1B6EF5C56B80D2EB4D77A9F01BC1728E99A11973A877AB2DCE0E8115E
