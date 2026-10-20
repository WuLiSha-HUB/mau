// ═══════════════════════════════════════════════════
// 积木: db.delete
// ID:   BRIK-DB-006
// 类别: DB
// 作用: 条目库删除——按标题追加墓碑行（物理行保留可审计；标题随即释放）
// 依赖: 无
// 引用: Mau.Runtime（DbService/DataBox）
// 原理: DataBox.TryResolve<DbService> → Delete(lib, title)
// 常用: db_cat.mau 认领线——'db.delete'[@args] > @result
// ═══════════════════════════════════════════════════
using System;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// 条目库积木——db-delete 删除条目（LLM 工具执行面：参数整包 argsJson）
    /// </summary>
    public static class DbDeleteBrick
    {
        /// <summary>
        /// 删除条目——墓碑追加写入；标题不存在由服务层具名拒绝。
        /// </summary>
        /// <param name="argsJson">工具参数 JSON（lib/title）</param>
        /// <param name="result">回执文本或 ERR| 错误文本</param>
        /// <returns>true=执行成功</returns>
        public static bool Delete(string argsJson, out string result)
        {
            result = "";
            // [参数面] 声明面口径零容忍——未知 / 缺值一律 ERR|BAD_ARGS（catId 保留键放行）
            string badArgs = JsonArgs.Validate(argsJson, "lib title", "lib title", "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            string lib = JsonArgs.Get(argsJson, "lib");
            string title = JsonArgs.Get(argsJson, "title");
            if (lib == "§PARSE_FAIL§" || title == "§PARSE_FAIL§")
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
                DbResult call = db.Delete(lib, title);
                result = call.ToToolText("db-delete", lib);
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
// #MAU_CHECKSUM:SHA256:0AEC64632A00D984DD98CED4530AF26E59CBCA6102ED4E3D28A1222814920079
