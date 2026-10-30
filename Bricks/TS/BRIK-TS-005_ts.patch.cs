// ═══════════════════════════════════════════════════
// 积木: ts.patch
// ID:   BRIK-TS-005
// 类别: TS
// 作用: TypeScript 改动——符号模式（整段替换）/ 替换模式（old→new 唯一锚点），回执带 diff + 诊断
// 依赖: 无
// 引用: Mau.Runtime（INodeService / DataBox / RootPathResolver / JsonUtil / JsonArgs）
// 原理: 参数校验（两模式二选一）→ 寻址展开 → 组 payload（op=ts.patch）→ INodeService.Invoke
// 常用: ts_cat.mau 认领线——'ts.patch'[@ptArgs] > @ptRes
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// TS 改动积木——ts-patch 工具（TsCat 工具组 Flow 认领线消费）。
    /// </summary>
    public static class TsPatchBrick
    {
        /// <summary>
        /// 服务实例名——对应配置段 node.service.ts.*
        /// </summary>
        private const string ServiceName = "ts";

        /// <summary>
        /// 改动文件——符号模式（symbol + body）或替换模式（old + new）二选一；写侧恒落盘，诊断随回执。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（file 必填；symbol/body 或 old/new）</param>
        /// <param name="result">服务回执 JSON 或 ERR| 错误文本</param>
        /// <returns>true=执行完成</returns>
        public static bool Patch(string argsJson, out string result)
        {
            result = "";
            string badArgs = JsonArgs.Validate(argsJson, "file symbol body old new tsconfig", "file", "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            string file = JsonArgs.Get(argsJson, "file");
            string symbol = JsonArgs.Get(argsJson, "symbol");
            string body = JsonArgs.Get(argsJson, "body");
            string oldText = JsonArgs.Get(argsJson, "old");
            string newText = JsonArgs.Get(argsJson, "new");
            string tsconfig = JsonArgs.Get(argsJson, "tsconfig");
            if (file == "§PARSE_FAIL§" || symbol == "§PARSE_FAIL§" || body == "§PARSE_FAIL§"
                || oldText == "§PARSE_FAIL§" || newText == "§PARSE_FAIL§" || tsconfig == "§PARSE_FAIL§")
            {
                result = "ERR|TS_BAD_ARGS|工具参数 JSON 解析失败";
                return false;
            }
            // [模式判定] 符号模式与替换模式二选一——两者同时给或都不给一律拒（语义必须唯一）
            bool symbolMode = symbol.Length > 0;
            bool replaceMode = oldText.Length > 0;
            if (symbolMode == replaceMode)
            {
                result = "ERR|TS_BAD_ARGS|须二选一——符号模式（symbol + body）或替换模式（old + new）";
                return false;
            }
            if (symbolMode && body.Length == 0)
            {
                result = "ERR|TS_BAD_ARGS|符号模式下 body 不可为空";
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
            string payload = "{\"op\":\"ts.patch\",\"file\":" + JsonUtil.Str(absFile)
                + ",\"symbol\":" + JsonUtil.Str(symbol)
                + ",\"body\":" + JsonUtil.Str(body)
                + ",\"old\":" + JsonUtil.Str(oldText)
                + ",\"new\":" + JsonUtil.Str(newText)
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
// #MAU_CHECKSUM:SHA256:5C3E815A53F31EF93D878BABF134E0A1BC34B0D4C57B712C1A5F8E397A2EE613
