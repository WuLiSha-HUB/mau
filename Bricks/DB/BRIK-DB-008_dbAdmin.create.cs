// ═══════════════════════════════════════════════════
// 积木: dbAdmin.create
// ID:   BRIK-DB-008
// 类别: DB
// 作用: 新建库——建目录 + 头行 + 注册表登记（管理面）
// 依赖: 无
// 引用: Mau.Runtime（DbService/DataBox）
// 原理: DataBox.TryResolve<DbService> → Create(lib, catId)；库名形态与授权由服务层判
// 常用: db_admin.mau 认领线——'dbAdmin.create'[@args] > @result
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 库管理积木——dbAdmin-create 建库（LLM 工具执行面：参数整包 argsJson）
    /// </summary>
    public static class DbAdminCreateBrick
    {
        /// <summary>
        /// 建库——库名小写字母 / 数字 / 短横线，2–32 位；已存在报 DB_LIB_EXISTS。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（lib）</param>
        /// <param name="result">回执文本或 ERR| 错误文本</param>
        /// <returns>true=执行成功</returns>
        public static bool Create(string argsJson, out string result)
        {
            result = "";
            // [参数面] 声明面口径零容忍——未知 / 缺值一律 ERR|BAD_ARGS
            string badArgs = JsonArgs.Validate(argsJson, "lib", "lib", "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            string lib = JsonArgs.Get(argsJson, "lib");
            try
            {
                DbService? db = null;
                DataBox.TryResolve<DbService>(out db);
                if (db == null)
                {
                    result = "ERR|DB_NO_SERVICE|宿主未注入 DbService";
                    return false;
                }
                DbResult call = db.Create(lib, JsonArgs.Get(argsJson, "catId"));
                result = call.ToToolText("dbAdmin-create", lib);
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
// #MAU_CHECKSUM:SHA256:CABD18A535A41BE112DBED70F3B24FFF92785734242D41B286B6A1483814C95B
