// ═══════════════════════════════════════════════════
// 积木: sh.exec
// ID:   BRIK-SH-001
// 类别: SH
// 作用: Linux 单行指令执行——bash -lc 直达；UTF-8 输出内建；超时进程树终止；结构化回执（exit / 行数 / 截断 / 超时）
// 依赖: 无
// 引用: Mau.Runtime（IShellService / JsonArgs / DataBox）
// 原理: JsonArgs.Get 取 command / timeout_ms → DataBox.TryResolve<IShellService> → Execute(command, timeoutMs)
//       拦截词表按 design-ch4-platform D5 后置——先跑通真实命令面，再按实际风险成表（本轮不加词表）
//       Windows 侧服务 Available=false → 具名 ERR 回执（不静默回落 ps 面）
// 常用: sh_cat.mau 认领线——'sh.exec'[@args] > @result
// ═══════════════════════════════════════════════════
using System;
using Mau.Contracts;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// Linux 命令行执行积木——sh-exec 工具（ShCat 工具组 Flow 认领线消费）
    /// </summary>
    public static class ShExecBrick
    {
        /// <summary>
        /// 执行单行命令——先做参数校验与服务解析（业务规则归积木层），通过后交宿主实现执行
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（command / timeout_ms）</param>
        /// <param name="result">结果 JSON 或 ERR| 错误文本</param>
        /// <returns>true=执行成功（回执已产出）</returns>
        public static bool Exec(string argsJson, out string result)
        {
            result = "";
            if (argsJson == null || argsJson.Length == 0)
            {
                result = "ERR|SH_BAD_ARGS|缺少参数 command";
                return false;
            }
            try
            {
                // [段1] 参数校验——command 必填（单行纪律由工具描述承载；拦截词表按 D5 后置）
                string command = JsonArgs.Get(argsJson, "command");
                if (command.Length == 0 || command == "§PARSE_FAIL§")
                {
                    result = "ERR|SH_BAD_ARGS|缺少参数 command";
                    return false;
                }
                // [段2] 服务解析——宿主注入的 IShellService（Linux 实现；Windows 侧 Available=false）
                IShellService service;
                DataBox.TryResolve<IShellService>(out service);
                if (service == null)
                {
                    result = "ERR|SH_NO_SERVICE|宿主未注入 IShellService";
                    return false;
                }
                if (service.Available == false)
                {
                    result = "ERR|SH_NOT_AVAILABLE|当前平台不提供 sh-exec（Linux 专用）——请改用 powershell / powershell7";
                    return false;
                }
                // [段3] 超时约束——参数缺省 30000，越界收敛到 [1, 120000]
                int timeoutMs = 30000;
                string timeoutRaw = JsonArgs.Get(argsJson, "timeout_ms");
                if (timeoutRaw.Length > 0 && timeoutRaw != "§PARSE_FAIL§")
                {
                    int parsed = 0;
                    if (int.TryParse(timeoutRaw, out parsed))
                    {
                        timeoutMs = parsed;
                    }
                }
                if (timeoutMs <= 0)
                {
                    timeoutMs = 30000;
                }
                if (timeoutMs > 120000)
                {
                    timeoutMs = 120000;
                }
                // [段4] 执行——回执由实现侧组装（JSON 头 + 正文；失败为 ERR| 前缀文本）
                result = service.Execute(command, timeoutMs);
                return true;
            }
            catch (Exception ex)
            {
                result = "ERR|" + ex.GetType().Name + "|" + ex.Message;
                return false;
            }
        }
    }
}
// #MAU_CHECKSUM:SHA256:B76BF4BA992B8DB358108500899E7766CDEDF0E50AB81AC8FAA3FAA901F12259
