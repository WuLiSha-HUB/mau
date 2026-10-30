// ═══════════════════════════════════════════════════
// 积木: ts.findRef
// ID:   BRIK-TS-004
// 类别: TS
// 作用: TypeScript 引用查找——语义级全量返回（decl / ref / type-ref / prop / import / destructure），不预裁剪
// 依赖: 无
// 引用: Mau.Runtime（INodeService / DataBox / RootPathResolver / JsonUtil / JsonArgs）
// 原理: 参数校验 → file 与 tsconfig 双寻址展开 → 组 payload（op=ts.findRef）→ INodeService.Invoke
// 常用: ts_cat.mau 认领线——'ts.findRef'[@frArgs] > @frRes
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// TS 引用查找积木——ts-find-ref 工具（TsCat 工具组 Flow 认领线消费）。
    /// </summary>
    public static class TsFindRefBrick
    {
        /// <summary>
        /// 服务实例名——对应配置段 node.service.ts.*
        /// </summary>
        private const string ServiceName = "ts";

        /// <summary>
        /// 引用查找——搜索面为 tsconfig 编译面内全部文件；种类全量返回（噪音留待实际开发裁剪）。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（file / symbol 必填；tsconfig 可选）</param>
        /// <param name="result">服务回执 JSON 或 ERR| 错误文本</param>
        /// <returns>true=执行完成</returns>
        public static bool FindRef(string argsJson, out string result)
        {
            result = "";
            string badArgs = JsonArgs.Validate(argsJson, "file symbol tsconfig", "file symbol", "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            string file = JsonArgs.Get(argsJson, "file");
            string symbol = JsonArgs.Get(argsJson, "symbol");
            string tsconfig = JsonArgs.Get(argsJson, "tsconfig");
            if (file == "§PARSE_FAIL§" || symbol == "§PARSE_FAIL§" || tsconfig == "§PARSE_FAIL§")
            {
                result = "ERR|TS_BAD_ARGS|工具参数 JSON 解析失败";
                return false;
            }
            string fileError;
            string absFile = ExpandPath(file, out fileError);
            if (fileError.Length > 0)
            {
                result = fileError;
                return false;
            }
            string configError = "";
            string absConfig = "";
            if (tsconfig.Length > 0)
            {
                absConfig = ExpandPath(tsconfig, out configError);
                if (configError.Length > 0)
                {
                    result = configError;
                    return false;
                }
            }
            string payload = "{\"op\":\"ts.findRef\",\"file\":" + JsonUtil.Str(absFile) + ",\"symbol\":" + JsonUtil.Str(symbol)
                + ",\"tsconfig\":" + JsonUtil.Str(absConfig) + "}";
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
// #MAU_CHECKSUM:SHA256:4ED9C6892FDEDCA7C04EAB81B4232087A3AF1779D877660CDBEB5B5E0917F7E9
