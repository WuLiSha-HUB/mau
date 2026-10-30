// ═══════════════════════════════════════════════════
// 积木: ts.check
// ID:   BRIK-TS-001
// 类别: TS
// 作用: TypeScript 工程口径类型诊断——回执逐条 文件:行:码:消息 + 错误/警告计数（唯一权威）
// 依赖: 无
// 引用: Mau.Runtime（INodeService / DataBox / RootPathResolver / JsonUtil / JsonArgs）
// 原理: 参数校验 → 受控根寻址展开绝对路径 → 组 payload → INodeService.Invoke(name="ts")
// 常用: ts_cat.mau 认领线——'ts.check'[@chkArgs] > @chkRes
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// TS 诊断积木——ts-check 工具（TsCat 工具组 Flow 认领线消费）。
    /// </summary>
    public static class TsCheckBrick
    {
        /// <summary>
        /// 服务实例名——对应配置段 node.service.ts.*
        /// </summary>
        private const string ServiceName = "ts";

        /// <summary>
        /// 类型诊断——按 tsconfig 整工程编译；单文件路径降级为单文件口径（回执标注）。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（path 必填；full 可选）</param>
        /// <param name="result">服务回执 JSON 或 ERR| 错误文本</param>
        /// <returns>true=执行完成</returns>
        public static bool Check(string argsJson, out string result)
        {
            result = "";
            string badArgs = JsonArgs.Validate(argsJson, "path full", "path", "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            string path = JsonArgs.Get(argsJson, "path");
            string fullText = JsonArgs.Get(argsJson, "full");
            if (path == "§PARSE_FAIL§" || fullText == "§PARSE_FAIL§")
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
            bool full = fullText == "true";
            string payload = "{\"op\":\"ts.check\",\"path\":" + JsonUtil.Str(absPath) + ",\"full\":" + (full ? "true" : "false") + "}";
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
// #MAU_CHECKSUM:SHA256:12956C20A1723C7157F95DF1E35208B5EB70FA7F9A71A32AAFB545E3452A1CB1
