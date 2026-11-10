// ═══════════════════════════════════════════════════
// 积木: text.read_between
// ID:   BRIK-TEXT-006
// 类别: TEXT
// 作用: 锚点区间读取——str1 与 str2 之间内容（str1 空=文件头 / str2 空=文件尾；锚点须全文唯一；编码自动探测）——LLM 工具 text-read_between 语料执行面
// 依赖: 无
// 引用: Mau.Runtime（FileSystemService/DataBox/TextAlgorithms）
// 原理: ReadTextAuto 读 → TextAlgorithms 定位锚点 → 区间子串；未命中差异事实面复用 TextReplaceBrick.AnchorMissNote（单点，A333）；唯一契约在本积木（A227 归位——算法面只给事实）
// 常用: TextCat 认领线——'text.read_between'[@args] > @result
// ═══════════════════════════════════════════════════
using System;
using System.Text.Json;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 文本积木——text-read_between 锚点区间读取（LLM 工具执行面：参数整包 argsJson）
    /// </summary>
    public static class TextReadBetweenBrick
    {
        /// <summary>
        /// 锚点区间读取
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（path/str1/str2）</param>
        /// <param name="result">区间内容或 ERR| 错误文本</param>
        /// <returns>true=执行成功</returns>
        public static bool ReadBetween(string argsJson, out string result)
        {
            result = "";
            // [参数面] 声明面口径零容忍——未知 / 缺值一律 ERR|BAD_ARGS（catId 保留键放行）
            string badArgs = JsonArgs.Validate(argsJson, "path str1 str2", "path", "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            string path = JsonArgs.Get(argsJson, "path");
            if (path == "§PARSE_FAIL§")
            {
                result = "ERR|BAD_ARGS|工具参数 JSON 解析失败（LLM 生成参数可能被截断——超长内容请分段写入）";
                return false;
            }
            if (path.Length == 0)
            {
                result = "ERR|BAD_ARGS|缺少参数 path";
                return false;
            }
            string str1 = JsonArgs.Get(argsJson, "str1");
            string str2 = JsonArgs.Get(argsJson, "str2");
            if (str1 == "§PARSE_FAIL§" || str2 == "§PARSE_FAIL§")
            {
                result = "ERR|BAD_ARGS|工具参数 JSON 解析失败（LLM 生成参数可能被截断——超长内容请分段写入）";
                return false;
            }
            try
            {
                FileSystemService? fs = FileSystemRegistry.ResolveScoped(JsonArgs.Get(argsJson, "catId"));
                if (fs == null)
                {
                    DataBox.TryResolve<FileSystemService>(out fs);
                }
                if (fs == null)
                {
                    result = "ERR|FS_NO_SERVICE|宿主未注入 FileSystemService";
                    return false;
                }
                // [IO] 读取（编码内建——去 BOM）→ 换行归一供匹配
                string content = fs.ReadTextAuto(path);
                string unified = content.Replace("\r\n", "\n");
                string a1 = str1.Replace("\r\n", "\n");
                string a2 = str2.Replace("\r\n", "\n");
                int start = 0;
                if (a1.Length > 0)
                {
                    int[] idx1 = TextAlgorithms.FindOccurrences(unified, a1, TextMatchMode.Exact);
                    if (idx1.Length == 0)
                    {
                        result = TextReplaceBrick.AnchorMissNote(unified, a1, "起始锚点");
                        return false;
                    }
                    if (idx1.Length > 1)
                    {
                        result = "ERR|ANCHOR_AMBIGUOUS|起始锚点多次出现: " + a1;
                        return false;
                    }
                    start = idx1[0] + a1.Length;
                }
                int end = unified.Length;
                if (a2.Length > 0)
                {
                    int[] idx2 = TextAlgorithms.FindOccurrences(unified, a2, TextMatchMode.Exact);
                    if (idx2.Length == 0)
                    {
                        result = TextReplaceBrick.AnchorMissNote(unified, a2, "结束锚点");
                        return false;
                    }
                    if (idx2.Length > 1)
                    {
                        result = "ERR|ANCHOR_AMBIGUOUS|结束锚点多次出现: " + a2;
                        return false;
                    }
                    end = idx2[0];
                }
                if (end < start)
                {
                    result = "ERR|ANCHOR_ORDER|结束锚点位于起始锚点之前";
                    return false;
                }
                string body = unified.Substring(start, end - start);
                int lines = 1;
                for (int i = 0; i < body.Length; i = i + 1)
                {
                    if (body[i] == '\n')
                    {
                        lines = lines + 1;
                    }
                }
                // 结构化返回体（design-ch4-tools 附录 · A214）——首行 JSON 头（target/items）+ 正文摘要行
                result = MetaHead("text-read_between", true, path, lines) + "\n" + path + " | " + lines.ToString() + " 行" + "\n" + body;
                return true;
            }
            catch (Exception ex)
            {
                result = "ERR|" + ex.GetType().Name + "|" + ex.Message;
                return false;
            }
        }

        /// <summary>
        /// 结构化元数据头（统一口径·A214）——恒定 ok / tool + 主来源 target + 主计数 items（空串 / 负值 = 省略）。
        /// </summary>
        /// <param name="tool">工具名</param>
        /// <param name="ok">成败</param>
        /// <param name="target">主来源（空串 = 省略）</param>
        /// <param name="items">主计数（负值 = 省略）</param>
        /// <returns>单行 JSON</returns>
        private static string MetaHead(string tool, bool ok, string target, int items)
        {
            System.Collections.Generic.Dictionary<string, object> head = new System.Collections.Generic.Dictionary<string, object>();
            head["ok"] = ok;
            head["tool"] = tool;
            if (target.Length > 0)
            {
                head["target"] = target;
            }
            if (items >= 0)
            {
                head["items"] = items;
            }
            return JsonSerializer.Serialize(head);
        }
    }
}
// #MAU_CHECKSUM:SHA256:5B0E923170F2FAA96BD74B82D64449AEDB8E127FB0D79B8039760157343C58B5
