// ═══════════════════════════════════════════════════
// 积木: db.query
// ID:   BRIK-DB-003
// 类别: DB
// 作用: 条目库查询——标题子串匹配，返回 ID / 标题 / 更新时间列表（不含正文）
// 依赖: 无
// 引用: Mau.Runtime（DbService/DataBox）
// 原理: DataBox.TryResolve<DbService> → Query(lib, keyword, limit)；limit 缺省取服务默认
// 常用: db_cat.mau 认领线——'db.query'[@args] > @result
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 条目库积木——db-query 查询条目（LLM 工具执行面：参数整包 argsJson）
    /// </summary>
    public static class DbQueryBrick
    {
        /// <summary>
        /// 查询条目——空关键词 = 全库列表；三数自证（total / matched / shown）。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（lib/keyword/limit）</param>
        /// <param name="result">回执文本或 ERR| 错误文本</param>
        /// <returns>true=执行成功</returns>
        public static bool Query(string argsJson, out string result)
        {
            result = "";
            // [参数面] 声明面口径零容忍——未知 / 缺值一律 ERR|BAD_ARGS（catId 保留键放行）
            string badArgs = JsonArgs.Validate(argsJson, "lib keyword limit", "lib", "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            string lib = JsonArgs.Get(argsJson, "lib");
            string keyword = JsonArgs.Get(argsJson, "keyword");
            string limitText = JsonArgs.Get(argsJson, "limit");
            if (lib == "§PARSE_FAIL§" || keyword == "§PARSE_FAIL§" || limitText == "§PARSE_FAIL§")
            {
                result = "ERR|BAD_ARGS|工具参数 JSON 解析失败";
                return false;
            }
            int limit = 0;
            if (limitText.Length > 0)
            {
                if (!int.TryParse(limitText, out limit))
                {
                    result = "ERR|BAD_ARGS|limit 非整数: " + limitText;
                    return false;
                }
            }
            try
            {
                DbService? db = null;
                DataBox.TryResolve<DbService>(out db);
                if (db == null)
                {
                    result = "ERR|DB_NO_SERVICE|宿主未注入 DbService";
                    return false;
                }
                DbResult call = db.Query(lib, keyword, limit, JsonArgs.Get(argsJson, "catId"));
                result = call.ToToolText("db-query", lib);
                return call.Ok;
            }
            catch (Exception ex)
            {
                result = "ERR|" + ex.GetType().Name + "|" + ex.Message;
                return false;
            }
        }
    }
}
// #MAU_CHECKSUM:SHA256:5BEEFA9E30A8989C2F23220F82356CDA841431A7F69E502891E0DE2299DD15E2
