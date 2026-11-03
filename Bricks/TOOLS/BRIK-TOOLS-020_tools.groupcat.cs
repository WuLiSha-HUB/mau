// ═══════════════════════════════════════════════════
// 积木: tools.groupcat
// ID:   BRIK-TOOLS-020
// 类别: TOOLS
// 作用: 返回 GroupCat 工具组全部工具的 OpenAI 兼容定义 JSON（宿主工具池拼装原料）
// 依赖: 无
// 引用: 无（纯文本常量）
// 原理: GetToolsJson() 返回本组工具定义整包 JSON——改描述只动本文件，mau proj 重建即生效
//       命名铁律：工具定义积木约定名 = tools.<flowName 小写>（group_cat.mau → GroupCat → tools.groupcat）
// 常用: FL_GroupCat.GetToolsJson() 自曝调用面（IFlow 接口）
// ═══════════════════════════════════════════════════
namespace Mau.Bricks
{
    /// <summary>
    /// 工具定义积木——GroupCat 组工具定义 JSON（OpenAI 兼容拼装原料）。
    /// 四件：group.info（组信息）/ group.inbox（拉消息）/ group.say（发讨论）/ group.send（@ 派单）。
    /// 权限：调用者须为组成员；group.send 另需组管理员（group.json adminKeys）——校验在宿主实现。
    /// </summary>
    public static class ToolsGroupcatBrick
    {
        /// <summary>
        /// 获取本组工具定义 JSON——{"group":"GroupCat","tools":[{name,description,parameters}]}
        /// </summary>
        /// <returns>工具定义 JSON 字符串</returns>
        public static string GetToolsJson()
        {
            return "{\"group\":\"GroupCat\",\"tools\":[" +
                "{\"name\":\"group.info\",\"description\":\"查看本组信息：组名 / 项目 / 启用态 / 消息统计 / 成员名单（含管理员标记）。调用者须为本组成员。返回整块 JSON。\",\"parameters\":{\"type\":\"object\",\"properties\":{\"group\":{\"type\":\"string\",\"description\":\"组名\"}},\"required\":[\"group\"]}}," +
                "{\"name\":\"group.inbox\",\"description\":\"拉取本组消息流：按 seq 升序返回 after 之后的消息（含发送方 / @ 目标 / 类型 / 正文 / 时刻）。用于批阅回执或回看派单。调用者须为本组成员。\",\"parameters\":{\"type\":\"object\",\"properties\":{\"group\":{\"type\":\"string\",\"description\":\"组名\"},\"after\":{\"type\":\"integer\",\"description\":\"起始序号（不含；缺省=从头）\"},\"limit\":{\"type\":\"integer\",\"description\":\"最多条数（缺省 100，上限 500）\"}},\"required\":[\"group\"]}}," +
                "{\"name\":\"group.say\",\"description\":\"在组内发一条讨论消息（note）——只写消息流，不投递、不唤醒任何猫。可带 to 标记 @ 对象（仅记录，不投递；要投递请用 group.send）。调用者须为本组成员。\",\"parameters\":{\"type\":\"object\",\"properties\":{\"group\":{\"type\":\"string\",\"description\":\"组名\"},\"text\":{\"type\":\"string\",\"description\":\"消息正文\"},\"to\":{\"type\":\"string\",\"description\":\"@ 目标猫 key（可选；须为组内成员）\"}},\"required\":[\"group\",\"text\"]}}," +
                "{\"name\":\"group.send\",\"description\":\"派单：向指定组员 @ 一条任务消息——写组消息（type=delegation）并以来源锚 group:<组名>:<seq>:delegation 立即投递到目标猫的会话（Idle 起轮 / 忙时排队）。目标猫轮末自动把最终回复作为回执写回本组。调用者须为组管理员（adminKeys）。目标猫须在组内，否则报错。\",\"parameters\":{\"type\":\"object\",\"properties\":{\"group\":{\"type\":\"string\",\"description\":\"组名\"},\"to\":{\"type\":\"string\",\"description\":\"@ 目标猫 key（组内成员）\"},\"text\":{\"type\":\"string\",\"description\":\"任务正文（派单包：任务 / 交付判据 / 相关文件位置 / 上一节点产物）\"},\"refs\":{\"type\":\"string\",\"description\":\"关联锚（单号 / 轮次，可空）\"}},\"required\":[\"group\",\"to\",\"text\"]}}" +
                "]}";
        }
    }
}
// #MAU_CHECKSUM:SHA256:A4E7BDD456B0E3ABEF97D7B00DBB8BFAFB85398F7278E43202F184FC40C9A9BE
