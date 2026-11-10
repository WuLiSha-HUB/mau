// ═══════════════════════════════════════════════════
// 积木: ts.lint
// ID:   BRIK-TS-007
// 类别: TS
// 作用: TypeScript 松散写法扫描——7 条可机检规则（显式 any / as unknown as / 非空断言 / 压制指令 / 空 catch / 裸 console.log / undefined 裸比较）
// 依赖: 无
// 引用: Mau.Runtime（INodeService / DataBox / RootPathResolver / JsonUtil / JsonArgs）
// 原理: 参数校验 → 受控根寻址展开绝对路径 → 组 payload（rules 逗号串转数组）→ INodeService.Invoke(name="ts")
// 常用: ts_cat.mau 认领线——'ts.lint'[@lnArgs] > @lnRes
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// TS 松散写法扫描积木——ts-lint 工具（TsCat 工具组 Flow 认领线消费）。
    /// </summary>
    public static class TsLintBrick
    {
        /// <summary>
        /// 服务实例名——对应配置段 node.service.ts.*
        /// </summary>
        private const string ServiceName = "ts";

        /// <summary>
        /// 松散写法扫描——返回逐条命中（相对路径:行:规则:摘录）+ 每规则计数。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（path 必填；rules 逗号串可选）</param>
        /// <param name="result">服务回执 JSON 或 ERR| 错误文本</param>
        /// <returns>true=执行完成</returns>
        public static bool Lint(string argsJson, out string result)
        {
            result = "";
            string badArgs = JsonArgs.Validate(argsJson, "path rules", "path", "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            string path = JsonArgs.Get(argsJson, "path");
            string rulesText = JsonArgs.Get(argsJson, "rules");
            if (path == "§PARSE_FAIL§" || rulesText == "§PARSE_FAIL§")
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
            string rulesJson = BuildRules(rulesText);
            string payload = "{\"op\":\"ts.lint\",\"path\":" + JsonUtil.Str(absPath) + rulesJson + "}";
            return Invoke(payload, out result);
        }

        /// <summary>
        /// 规则名逗号串 → JSON 数组片段（空串返回空，不设隐式默认集）。
        /// </summary>
        /// <param name="rulesText">规则名逗号串</param>
        /// <returns>JSON 片段（形如 ,"rules":["any"]）；无规则返回空串</returns>
        private static string BuildRules(string rulesText)
        {
            if (rulesText.Length == 0)
            {
                return "";
            }
            string[] parts = rulesText.Split(',');
            string built = "";
            for (int i = 0; i < parts.Length; i = i + 1)
            {
                string one = parts[i].Trim();
                if (one.Length == 0)
                {
                    continue;
                }
                if (built.Length > 0)
                {
                    built = built + ",";
                }
                built = built + JsonUtil.Str(one);
            }
            if (built.Length == 0)
            {
                return "";
            }
            return ",\"rules\":[" + built + "]";
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
// #MAU_CHECKSUM:SHA256:F4A80B10DDFEBAE71B1BDF1207E4CA60ABF40EB5767F9E805C0CAB40B3D7A132
