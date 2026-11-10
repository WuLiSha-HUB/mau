// ═══════════════════════════════════════════════════
// 积木: ts.test
// ID:   BRIK-TS-008
// 类别: TS
// 作用: TypeScript 跑测窗口——逐项用例计数（通过 / 失败 / 跳过）+ 失败用例 + 剥 ANSI；同步阻塞（与服务串行模型一致，A332）
// 依赖: 无
// 引用: Mau.Runtime（INodeService / DataBox / RootPathResolver / JsonUtil / JsonArgs）
// 原理: 参数校验 → 受控根寻址展开绝对路径 → 组 payload → INodeService.Invoke(name="ts")
// 常用: ts_cat.mau 认领线——'ts.test'[@teArgs] > @teRes
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// TS 跑测窗口积木——ts-test 工具（TsCat 工具组 Flow 认领线消费）。
    /// </summary>
    public static class TsTestBrick
    {
        /// <summary>
        /// 服务实例名——对应配置段 node.service.ts.*
        /// </summary>
        private const string ServiceName = "ts";

        /// <summary>
        /// 跑测——只认宿主已配置的运行器（vitest / node:test），回执必给用例计数。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（path 必填；filter 可选）</param>
        /// <param name="result">服务回执 JSON 或 ERR| 错误文本</param>
        /// <returns>true=执行完成</returns>
        public static bool Run(string argsJson, out string result)
        {
            result = "";
            string badArgs = JsonArgs.Validate(argsJson, "path filter", "path", "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            string path = JsonArgs.Get(argsJson, "path");
            string filter = JsonArgs.Get(argsJson, "filter");
            if (path == "§PARSE_FAIL§" || filter == "§PARSE_FAIL§")
            {
                result = "ERR|TS_BAD_ARGS|工具参数 JSON 解析失败";
                return false;
            }
            string pathError;
            string absPath = ExpandPath(path, out pathError);
            if (pathError.Length > 0)
            {
                result = pathError;
                return false;
            }
            string payload = "{\"op\":\"ts.test\",\"path\":" + JsonUtil.Str(absPath) + ",\"filter\":" + JsonUtil.Str(filter) + "}";
            return Invoke(payload, out result);
        }

        /// <summary>
        /// 受控根寻址展开——&lt;根id&gt;:&lt;相对&gt; 或绝对路径 → 绝对路径。
        /// </summary>
        /// <param name="raw">原始路径</param>
        /// <param name="error">错误文本（空=成功）</param>
        /// <returns>绝对路径</returns>
        private static string ExpandPath(string raw, out string error)
        {
            error = "";
            WorkspaceConfig ws = null!;
            DataBox.TryResolve<WorkspaceConfig>(out ws);
            if (ws == null)
            {
                return raw;
            }
            RootResolveResult rr = RootPathResolver.Expand(raw, ws.Roots, Environment.CurrentDirectory);
            if (!rr.Ok)
            {
                error = "ERR|TS_BAD_PATH|路径解析失败（" + raw + "）: " + rr.ErrorCode;
                return "";
            }
            return rr.FullPath;
        }

        /// <summary>
        /// 投递服务调用——payload 交 INodeService（进程与协议归服务层）。
        /// </summary>
        /// <param name="payload">请求体 JSON 文本</param>
        /// <param name="result">服务回执或 ERR| 文本</param>
        /// <returns>true=执行完成</returns>
        private static bool Invoke(string payload, out string result)
        {
            try
            {
                INodeService service;
                DataBox.TryResolve<INodeService>(out service);
                if (service == null)
                {
                    result = "ERR|TS_NO_SERVICE|宿主未注入 INodeService";
                    return false;
                }
                result = service.Invoke("{\"name\":" + JsonUtil.Str(ServiceName) + ",\"payload\":" + JsonUtil.Str(payload) + "}");
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
// #MAU_CHECKSUM:SHA256:848D06F92F4650AF0850CEB14866F47924485737C4CB969191DE35EEEEB318AB
