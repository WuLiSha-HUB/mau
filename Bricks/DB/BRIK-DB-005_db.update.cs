// ═══════════════════════════════════════════════════
// 积木: db.update
// ID:   BRIK-DB-005
// 类别: DB
// 作用: 条目库更新——按标题定位覆盖正文（追加 v+1 行；标题不可改）
// 依赖: 无
// 引用: Mau.Runtime（DbService/DataBox）
// 原理: DataBox.TryResolve<DbService> → Update(lib, title, body)
// 常用: db_cat.mau 认领线——'db.update'[@args] > @result
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 条目库积木——db-update 更新正文（LLM 工具执行面：参数整包 argsJson）
    /// </summary>
    public static class DbUpdateBrick
    {
        /// <summary>
        /// 更新正文——标题不存在 / 正文超长由服务层具名拒绝。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（lib/title/body）</param>
        /// <param name="result">回执文本或 ERR| 错误文本</param>
        /// <returns>true=执行成功</returns>
        public static bool Update(string argsJson, out string result)
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
                DbResult call = db.Update(lib, title, body, JsonArgs.Get(argsJson, "catId"));
                result = call.ToToolText("db-update", lib);
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
// #MAU_CHECKSUM:SHA256:4D30ED61CA3F8BAD0D456C52F71F1883E10C129ED49EAEFE23C333136B8B38EB
