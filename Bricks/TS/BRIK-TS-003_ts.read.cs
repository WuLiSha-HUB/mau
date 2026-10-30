// ═══════════════════════════════════════════════════
// 积木: ts.read
// ID:   BRIK-TS-003
// 类别: TS
// 作用: TypeScript 符号源码读取——带行号返回，注释归属随符号；symbol 空=文件概览
// 依赖: 无
// 引用: Mau.Runtime（INodeService / DataBox / RootPathResolver / JsonUtil / JsonArgs）
// 原理: 参数校验 → 受控根寻址展开 → 组 payload（op=ts.read）→ INodeService.Invoke
// 常用: ts_cat.mau 认领线——'ts.read'[@rdArgs] > @rdRes
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// TS 源码读取积木——ts-read 工具（TsCat 工具组 Flow 认领线消费）。
    /// </summary>
    public static class TsReadBrick
    {
        /// <summary>
        /// 服务实例名——对应配置段 node.service.ts.*
        /// </summary>
        private const string ServiceName = "ts";

        /// <summary>
        /// 读取符号源码——symbol 空 = 文件概览（导入段 + 全部声明头 + 行号区间）。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（file 必填；symbol 可选）</param>
        /// <param name="result">服务回执 JSON 或 ERR| 错误文本</param>
        /// <returns>true=执行完成</returns>
        public static bool Read(string argsJson, out string result)
        {
            result = "";
            string badArgs = JsonArgs.Validate(argsJson, "file symbol", "file", "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            string file = JsonArgs.Get(argsJson, "file");
            string symbol = JsonArgs.Get(argsJson, "symbol");
            if (file == "§PARSE_FAIL§" || symbol == "§PARSE_FAIL§")
            {
                result = "ERR|TS_BAD_ARGS|工具参数 JSON 解析失败";
                return false;
            }
            string pathError;
            string absFile = ExpandPath(file, out pathError);
            if (pathError.Length > 0)
            {
                result = pathError;
                return false;
            }
            string payload = "{\"op\":\"ts.read\",\"file\":" + JsonUtil.Str(absFile) + ",\"symbol\":" + JsonUtil.Str(symbol) + "}";
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
// #MAU_CHECKSUM:SHA256:763A4DC597AE54F3679549BFDDCC8A51C7E3B5876C04F9EECF5CCEDF45A224B5
