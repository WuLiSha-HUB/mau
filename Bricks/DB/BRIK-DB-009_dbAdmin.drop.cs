// ═══════════════════════════════════════════════════
// 积木: dbAdmin.drop
// ID:   BRIK-DB-009
// 类别: DB
// 作用: 删库——目录移入库根 `_trash/<库名>-<时间戳>`（可恢复）+ 注册表摘除
// 依赖: 无
// 引用: Mau.Runtime（DbService/DataBox）
// 原理: DataBox.TryResolve<DbService> → DropLib(lib, confirm, catId)；confirm 须等于库名
// 常用: db_admin.mau 认领线——'dbAdmin.drop'[@args] > @result
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 库管理积木——dbAdmin-drop 删库（LLM 工具执行面：参数整包 argsJson）
    /// </summary>
    public static class DbAdminDropBrick
    {
        /// <summary>
        /// 删库——destructive：confirm 须与库名一致；目录移回收区，不物理删除。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（lib/confirm）</param>
        /// <param name="result">回执文本或 ERR| 错误文本</param>
        /// <returns>true=执行成功</returns>
        public static bool Drop(string argsJson, out string result)
        {
            result = "";
            // [参数面] 声明面口径零容忍——未知 / 缺值一律 ERR|BAD_ARGS
            string badArgs = JsonArgs.Validate(argsJson, "lib confirm", "lib confirm", "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            string lib = JsonArgs.Get(argsJson, "lib");
            string confirm = JsonArgs.Get(argsJson, "confirm");
            try
            {
                DbService? db = null;
                DataBox.TryResolve<DbService>(out db);
                if (db == null)
                {
                    result = "ERR|DB_NO_SERVICE|宿主未注入 DbService";
                    return false;
                }
                DbResult call = db.DropLib(lib, confirm, JsonArgs.Get(argsJson, "catId"));
                result = call.ToToolText("dbAdmin-drop", lib);
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
// #MAU_CHECKSUM:SHA256:6F1B92089026C9C42C1C2190335816C956B142A9FA4CBC517AECA5827FA08F83
