// ═══════════════════════════════════════════════════
// 积木: dbAdmin.libs
// ID:   BRIK-DB-007
// 类别: DB
// 作用: 库级明细——登记的库 + 活跃 / 墓碑 / 坏行 / 字节数（管理面只读）
// 依赖: 无
// 引用: Mau.Runtime（DbService/DataBox）
// 原理: DataBox.TryResolve<DbService> → ListLibsDetail(catId)；授权过滤在服务层
// 常用: db_admin.mau 认领线——'dbAdmin.libs'[@args] > @result
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 库管理积木——dbAdmin-libs 列库明细（LLM 工具执行面：参数整包 argsJson）
    /// </summary>
    public static class DbAdminLibsBrick
    {
        /// <summary>
        /// 列库——活跃 / 墓碑 / 坏行 / 字节数；按猫授权过滤，摘要自证全集。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（无参数）</param>
        /// <param name="result">回执文本或 ERR| 错误文本</param>
        /// <returns>true=执行成功</returns>
        public static bool Lists(string argsJson, out string result)
        {
            result = "";
            // [参数面] 声明面口径零容忍——未知参数一律 ERR|BAD_ARGS
            string badArgs = JsonArgs.Validate(argsJson, "", "", "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
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
                DbResult call = db.ListLibsDetail(JsonArgs.Get(argsJson, "catId"));
                result = call.ToToolText("dbAdmin-libs", "");
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
// #MAU_CHECKSUM:SHA256:9C749EEE9B49725CAD4F20FC8326F3EF7BAB9804647CE67419B05883C7B13265
