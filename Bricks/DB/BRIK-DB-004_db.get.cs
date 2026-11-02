// ═══════════════════════════════════════════════════
// 积木: db.get
// ID:   BRIK-DB-004
// 类别: DB
// 作用: 条目库读取——按标题完全匹配或按 ID 取单条全文（含正文 / 版本 / 时间）
// 依赖: 无
// 引用: Mau.Runtime（DbService/DataBox）
// 原理: DataBox.TryResolve<DbService> → Get(lib, mode, key)；mode 缺省 title
// 常用: db_cat.mau 认领线——'db.get'[@args] > @result
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 条目库积木——db-get 取单条全文（LLM 工具执行面：参数整包 argsJson）
    /// </summary>
    public static class DbGetBrick
    {
        /// <summary>
        /// 取单条全文——墓碑行视为不存在（DB_ENTRY_NOT_FOUND）。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（lib/mode/key）</param>
        /// <param name="result">回执文本或 ERR| 错误文本</param>
        /// <returns>true=执行成功</returns>
        public static bool Get(string argsJson, out string result)
        {
            result = "";
            // [参数面] 声明面口径零容忍——未知 / 缺值一律 ERR|BAD_ARGS（catId 保留键放行）
            string badArgs = JsonArgs.Validate(argsJson, "lib mode key", "lib key", "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            string lib = JsonArgs.Get(argsJson, "lib");
            string mode = JsonArgs.Get(argsJson, "mode");
            string key = JsonArgs.Get(argsJson, "key");
            if (lib == "§PARSE_FAIL§" || mode == "§PARSE_FAIL§" || key == "§PARSE_FAIL§")
            {
                result = "ERR|BAD_ARGS|工具参数 JSON 解析失败";
                return false;
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
                DbResult call = db.Get(lib, mode, key, JsonArgs.Get(argsJson, "catId"));
                result = call.ToToolText("db-get", lib);
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
// #MAU_CHECKSUM:SHA256:E3E6E891FFF60647C1F2B23AFA61CFDD1F47C007DBD642069939E8F0B9F1CBCF
