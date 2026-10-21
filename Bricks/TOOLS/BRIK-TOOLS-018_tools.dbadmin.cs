// ═══════════════════════════════════════════════════
// 积木: tools.dbadmin
// ID:   BRIK-TOOLS-018
// 类别: TOOLS
// 作用: 返回 DbAdmin 工具组全部工具（dbAdmin-* 三件——库级管理面）的定义 JSON（宿主工具池拼装原料）
// 依赖: 无
// 引用: 无（纯文本常量）
// 原理: GetToolsJson() 返回本组工具定义整包 JSON——改描述只动本文件，mau proj 重建即生效
// 常用: FL_DbAdmin.GetToolsJson() 自曝调用面（IFlow 接口）
// ═══════════════════════════════════════════════════
namespace Mau.Bricks
{
    /// <summary>
    /// 工具定义积木——DbAdmin 组工具定义 JSON（OpenAI 兼容拼装原料）
    /// </summary>
    public static class ToolsDbadminBrick
    {
        /// <summary>
        /// 获取本组工具定义 JSON——{"group":"DbAdmin","tools":[{name,description,parameters}]}
        /// </summary>
        /// <returns>工具定义 JSON 字符串</returns>
        public static string GetToolsJson()
        {
            return "{\"group\":\"DbAdmin\",\"tools\":[" +
                "{\"name\":\"dbAdmin-libs\",\"description\":\"库级明细——列出注册表登记的库（活跃条目 / 墓碑 / 坏行 / 字节数），按猫 dbLibs 授权过滤（未授权库不出现，摘要行自证全集）；只读\",\"parameters\":{\"type\":\"object\",\"properties\":{}}}," +
                "{\"name\":\"dbAdmin-create\",\"description\":\"新建库——库名须小写字母 / 数字 / 短横线，2–32 位；库名即物理目录名，库根内唯一。已存在报 DB_LIB_EXISTS；未授权的库名报 DB_LIB_DENIED\",\"parameters\":{\"type\":\"object\",\"properties\":{\"lib\":{\"type\":\"string\",\"description\":\"库名（小写字母 / 数字 / 短横线，2–32 位）\"}},\"required\":[\"lib\"]}}," +
                "{\"name\":\"dbAdmin-drop\",\"description\":\"删库（破坏性）——目录移入库根 _trash/<库名>-<时间戳>（可恢复），注册表摘除；confirm 必须与库名完全一致才执行。库未登记报 DB_LIB_NOT_FOUND；未授权报 DB_LIB_DENIED\",\"parameters\":{\"type\":\"object\",\"properties\":{\"lib\":{\"type\":\"string\",\"description\":\"库名\"},\"confirm\":{\"type\":\"string\",\"description\":\"确认串（须与库名一致）\"}},\"required\":[\"lib\",\"confirm\"]}}" +
                "]}";
        }
    }
}
// #MAU_CHECKSUM:SHA256:2ECB71BFA2BB3DDC82BB5E2C06FECFDC2E1C614E2E18FD5DBD7C85C74614E02F
