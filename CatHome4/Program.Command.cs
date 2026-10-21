using System;
using Mau.Runtime;
using CatHome4.Admin;

namespace CH4
{
    /// <summary>
    /// Program 宿主指令服务分部——工具面触达内核指令族的桥（IHostCommandService 实现 + 统一执行出口）。
    /// 数据流单向：工具组 Flow → host.command 积木 → IHostCommandService → 内核指令族 → 回执（无回流）。
    /// 单一内核：CLI（--run）/ HTTP 面板（/api/v1/command）/ 工具桥三面落同一执行体 CatHostService.HandleCatCommand。
    /// 权限分层：本桥不判权限——可见性由会话工具面承担（工具定义积木组级 privileged 声明 → ToolRegistry 标记）。
    /// 线程契约：指令族触碰注册表与会话面（ThreadGuard 守卫）——主线程直执；非主线程入 _catQueue 泵。
    /// </summary>
    public static partial class Program
    {
        /// <summary>工具面指令前缀白名单——管理指令族（cat.* / catcfg.*；入口面 = 用法声明面）</summary>
        private static readonly string[] AdminCmdPrefixes = new string[] { "cat.", "catcfg." };

        /// <summary>
        /// 管理指令统一执行——CLI / HTTP 面板 / 工具桥三面同源（前缀校验 + 停机态 + 线程域 + 内核调用）。
        /// </summary>
        /// <param name="line">指令行（cat.* / catcfg.*）</param>
        /// <returns>内核回执原文；越界 / 未受理返回 ERR| 前缀文本</returns>
        internal static string ExecuteAdminCommand(string line)
        {
            if (line == null || line.Trim().Length == 0)
            {
                return "ERR|BAD_ARGS|指令为空";
            }
            string trimmed = line.Trim();
            // [段1] 前缀白名单——工具面只开管理指令族；其余前缀直接拒绝（不投递）
            bool allowed = false;
            for (int i = 0; i < AdminCmdPrefixes.Length; i = i + 1)
            {
                if (trimmed.StartsWith(AdminCmdPrefixes[i], StringComparison.Ordinal))
                {
                    allowed = true;
                    break;
                }
            }
            if (!allowed)
            {
                return "ERR|BAD_PREFIX|仅接受 cat.* / catcfg.* 指令（收到: " + trimmed + "）";
            }
            // [段2] 线程域——非主线程入队主线程泵（与 HTTP 面同源；异步受理，回执走泵日志）
            if (Environment.CurrentManagedThreadId != _mainThreadId)
            {
                AdminService.PostInput(AdminService.MainInputTarget(), trimmed);
                return "已受理（非主线程入队，主线程泵执行）: " + trimmed;
            }
            // [段3] 重启停机态——拒收（与 CLI / HTTP 面同规；design-ch4-host-restart §三 T2）
            if (IsRestarting())
            {
                return "ERR|HOST_RESTARTING|宿主重启中——指令未受理: " + trimmed;
            }
            // [段4] 内核执行——唯一执行体（与 CLI / HTTP 主线程分支同一落点）
            return CatHostService.HandleCatCommand(trimmed);
        }

        /// <summary>
        /// 宿主指令服务实现——工具桥（DataBox.Bind&lt;IHostCommandService&gt; 注入面）。
        /// </summary>
        private sealed class HostCommandService : IHostCommandService
        {
            /// <summary>
            /// 执行一行宿主指令——交给统一执行出口（前缀白名单 / 线程域 / 停机态 / 内核调用集中一处）。
            /// </summary>
            /// <param name="line">指令行（cat.* / catcfg.*）</param>
            /// <returns>内核回执原文；越界 / 非法调用返回 ERR| 前缀文本</returns>
            public string Execute(string line)
            {
                return ExecuteAdminCommand(line);
            }
        }
    }
}
