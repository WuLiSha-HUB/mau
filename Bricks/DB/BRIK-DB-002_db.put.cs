// ═══════════════════════════════════════════════════
// 积木: db.put
// ID:   BRIK-DB-002
// 类别: DB
// 作用: 条目库新增——追加一条条目并分配库内自增 ID（LLM 工具 db-put 语料执行面）
// 依赖: 无
// 引用: Mau.Runtime（DbService/DataBox）
// 原理: DataBox.TryResolve<DbService> → Put(lib, title, body)；argsJson 内解析三参
// 常用: db_cat.mau 认领线——'db.put'[@args] > @result
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 条目库积木——db-put 新增条目（LLM 工具执行面：参数整包 argsJson）
    /// </summary>
    public static class DbPutBrick
    {
        /// <summary>
        /// 新增条目——同标题已存在 / 超长 / 库未登记均由服务层具名拒绝。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（lib/title/body）</param>
        /// <param name="result">回执文本或 ERR| 错误文本</param>
        /// <returns>true=执行成功</returns>
        public static bool Put(string argsJson, out string result)
        {
            result = "";
            // [参数面] 声明面口径零容忍——未知 / 缺值一律 ERR|BAD_ARGS（catId 保留键放行）
            string badArgs = JsonArgs.Validate(argsJson, "lib title body", "lib title body", "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            string lib = JsonArgs.Get(argsJson, "lib");
            string title = JsonArgs.Get(argsJson, "title");
            string body = JsonArgs.Get(argsJson, "body");
            if (lib == "§PARSE_FAIL§" || title == "§PARSE_FAIL§" || body == "§PARSE_FAIL§")
            {
                result = "ERR|BAD_ARGS|工具参数 JSON 解析失败（超长正文请精简后重试）";
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
                DbResult call = db.Put(lib, title, body, JsonArgs.Get(argsJson, "catId"));
                result = call.ToToolText("db-put", lib);
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
// #MAU_CHECKSUM:SHA256:7D3788C6305C7DBFA7105D958EB32021CE73978969DAEC319CE38372B5770401
