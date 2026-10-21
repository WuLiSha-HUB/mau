// ═══════════════════════════════════════════════════
// 积木: tools.dbcat
// ID:   BRIK-TOOLS-017
// 类别: TOOLS
// 作用: 返回 DbCat 工具组全部工具（db-* 五件——Smriti 条目库）的 OpenAI 兼容定义 JSON（宿主工具池拼装原料）
// 依赖: 无
// 引用: 无（纯文本常量）
// 原理: GetToolsJson() 返回本组工具定义整包 JSON——改描述只动本文件，mau proj 重建即生效
// 常用: FL_DbCat.GetToolsJson() 自曝调用面（IFlow 接口）
// ═══════════════════════════════════════════════════
namespace Mau.Bricks
{
    /// <summary>
    /// 工具定义积木——DbCat 组工具定义 JSON（OpenAI 兼容拼装原料）
    /// </summary>
    public static class ToolsDbcatBrick
    {
        /// <summary>
        /// 获取本组工具定义 JSON——{"group":"DbCat","tools":[{name,description,parameters}]}
        /// </summary>
        /// <returns>工具定义 JSON 字符串</returns>
        public static string GetToolsJson()
        {
            return "{\"group\":\"DbCat\",\"tools\":[" +
                "{\"name\":\"db-put\",\"description\":\"条目库新增——追加一条条目并分配库内自增 ID。标题库内唯一（≤50 字符，常态引导 20–30 字）；正文 ≤5000 字符（常态引导 <2000）。同标题已存在报 DB_TITLE_EXISTS；库须先由控制台新建（工具面不建库）；库须在猫的 dbLibs 白名单内（未授权报 DB_LIB_DENIED）\",\"parameters\":{\"type\":\"object\",\"properties\":{\"lib\":{\"type\":\"string\",\"description\":\"库名（小写字母 / 数字 / 短横线，2–32 位）\"},\"title\":{\"type\":\"string\",\"description\":\"标题（库内唯一键，不可改名）\"},\"body\":{\"type\":\"string\",\"description\":\"正文\"}},\"required\":[\"lib\",\"title\",\"body\"]}}," +
                "{\"name\":\"db-query\",\"description\":\"条目库查询——按标题子串匹配（忽略大小写），返回 ID / 标题 / 更新时间列表（不含正文，取正文用 db-get）；空关键词 = 全库列表；响应给 total / matched / shown 三数；库须在猫的 dbLibs 白名单内（未授权报 DB_LIB_DENIED）\",\"parameters\":{\"type\":\"object\",\"properties\":{\"lib\":{\"type\":\"string\",\"description\":\"库名\"},\"keyword\":{\"type\":\"string\",\"description\":\"关键词（可空——空 = 全库列表）\"},\"limit\":{\"type\":\"integer\",\"description\":\"返回条数（默认 10，上限 200）\"}},\"required\":[\"lib\"]}}," +
                "{\"name\":\"db-get\",\"description\":\"条目库读取——按标题完全匹配或按 ID 取单条全文（含正文 / 版本 / 时间）；未命中报 DB_ENTRY_NOT_FOUND（已删除条目同样视为不存在）\",\"parameters\":{\"type\":\"object\",\"properties\":{\"lib\":{\"type\":\"string\",\"description\":\"库名\"},\"mode\":{\"type\":\"string\",\"description\":\"title（默认，标题完全匹配）| id（按条目 ID）\"},\"key\":{\"type\":\"string\",\"description\":\"标题文本或 ID 文本\"}},\"required\":[\"lib\",\"key\"]}}," +
                "{\"name\":\"db-update\",\"description\":\"条目库更新——按标题定位覆盖正文（版本号 +1，追加写入）；标题本身不可改名（要改标题 = 删除后重建）。标题不存在报 DB_TITLE_NOT_FOUND\",\"parameters\":{\"type\":\"object\",\"properties\":{\"lib\":{\"type\":\"string\",\"description\":\"库名\"},\"title\":{\"type\":\"string\",\"description\":\"标题（定位键，完全匹配）\"},\"body\":{\"type\":\"string\",\"description\":\"新正文（≤5000 字符）\"}},\"required\":[\"lib\",\"title\",\"body\"]}}," +
                "{\"name\":\"db-delete\",\"description\":\"条目库删除——按标题追加墓碑（物理行保留可审计，可追溯）；删除后同标题可再新建（得到新 ID）。标题不存在报 DB_TITLE_NOT_FOUND\",\"parameters\":{\"type\":\"object\",\"properties\":{\"lib\":{\"type\":\"string\",\"description\":\"库名\"},\"title\":{\"type\":\"string\",\"description\":\"标题（定位键，完全匹配）\"}},\"required\":[\"lib\",\"title\"]}}" +
                "]}";
        }
    }
}
// #MAU_CHECKSUM:SHA256:4673C8CD5E4AB59BC2459090BD44DB58FBDA514F44A1DC7C108FFAB1E82F6534
