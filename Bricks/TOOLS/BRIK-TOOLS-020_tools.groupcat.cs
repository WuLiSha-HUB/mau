// ═══════════════════════════════════════════════════
// 积木: tools.groupcat
// ID:   BRIK-TOOLS-020
// 类别: TOOLS
// 作用: 返回 GroupCat 工具组全部工具的 OpenAI 兼容定义 JSON（宿主工具池拼装原料）
// 依赖: 无
// 引用: 无（纯文本常量）
// 原理: GetToolsJson() 返回本组工具定义整包 JSON——改描述只动本文件，mau proj 重建即生效
//       命名铁律：工具定义积木约定名 = tools.<flowName 小写>（group_cat.mau → GroupCat → tools.groupcat）
//       工具名铁律：分隔符用连字符（group-info），点号只用于积木名（group.info）
// 常用: FL_GroupCat.GetToolsJson() 自曝调用面（IFlow 接口）
// ═══════════════════════════════════════════════════
namespace Mau.Bricks
{
    /// <summary>
    /// 工具定义积木——GroupCat 组工具定义 JSON（OpenAI 兼容拼装原料）。
    /// 四件：group-info（组信息）/ group-inbox（拉消息）/ group-say（发讨论）/ group-send（@ 派单）。
    /// 权限：调用者须为组成员；group-send 另需组管理员（group.json adminKeys）——校验在宿主实现。
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
                "{\"name\":\"group-info\",\"description\":\"查看本组信息：组名 / 项目 / 启用态 / 消息统计 / 成员名单（含管理员标记）。调用者须为本组成员。返回整块 JSON。\",\"parameters\":{\"type\":\"object\",\"properties\":{\"group\":{\"type\":\"string\",\"description\":\"组名\"}},\"required\":[\"group\"]}}," +
                "{\"name\":\"group-inbox\",\"description\":\"拉取本组消息流：按 seq 升序返回 after 之后的消息（含发送方 / @ 目标 / 类型 / 正文 / 时刻）。用于批阅回执或回看派单。调用者须为本组成员。\",\"parameters\":{\"type\":\"object\",\"properties\":{\"group\":{\"type\":\"string\",\"description\":\"组名\"},\"after\":{\"type\":\"integer\",\"description\":\"起始序号（不含；缺省=从头）\"},\"limit\":{\"type\":\"integer\",\"description\":\"最多条数（缺省 100，上限 500）\"}},\"required\":[\"group\"]}}," +
                "{\"name\":\"group-say\",\"description\":\"在组内发一条讨论消息（note）——只写消息流，不投递、不唤醒任何猫。可带 to 标记 @ 对象（仅记录，不投递；要投递请用 group-send）。调用者须为本组成员。\",\"parameters\":{\"type\":\"object\",\"properties\":{\"group\":{\"type\":\"string\",\"description\":\"组名\"},\"text\":{\"type\":\"string\",\"description\":\"消息正文\"},\"to\":{\"type\":\"string\",\"description\":\"@ 目标猫 key（可选；须为组内成员）\"}},\"required\":[\"group\",\"text\"]}}," +
                "{\"name\":\"group-send\",\"description\":\"派单：向指定组员 @ 一条任务消息——写组消息（type=delegation）并以来源锚 group:<组名>:<seq>:delegation 立即投递到目标猫的会话（Idle 起轮 / 忙时排队）。目标猫轮末自动把最终回复作为回执写回本组。调用者须为组管理员（adminKeys）。目标猫须在组内，否则报错。\",\"parameters\":{\"type\":\"object\",\"properties\":{\"group\":{\"type\":\"string\",\"description\":\"组名\"},\"to\":{\"type\":\"string\",\"description\":\"@ 目标猫 key（组内成员）\"},\"text\":{\"type\":\"string\",\"description\":\"任务正文（派单包：任务 / 交付判据 / 相关文件位置 / 上一节点产物）\"},\"refs\":{\"type\":\"string\",\"description\":\"关联锚（单号 / 轮次，可空）\"}},\"required\":[\"group\",\"to\",\"text\"]}}," +
                "{\"name\":\"group-newsession\",\"description\":\"重建成员的会话（清空前文 + 按该猫 persona/注入清单/工具面重注入，旧会话自动留档）。对他人：目标须空闲，忙则拒绝（不排队）；对自己：挂起到本轮收尾后执行。可选 firstMessage：重建后立即注入为该会话首条用户消息（带派单锚，执行完自动回执进组）；不填则只重建、回到空闲。会在组内留一条 note 留痕。调用者须为组管理员；目标须为组内成员且运行中。reason 必填——新会话唯一的上下文说明。\",\"parameters\":{\"type\":\"object\",\"properties\":{\"group\":{\"type\":\"string\",\"description\":\"组名\"},\"to\":{\"type\":\"string\",\"description\":\"目标猫 key（组内成员；可为自己）\"},\"reason\":{\"type\":\"string\",\"description\":\"重建原因（必填；写入新会话注入报告与组留痕）\"},\"firstMessage\":{\"type\":\"string\",\"description\":\"重建后注入的首条用户消息（可选；空 = 只重建）\"}},\"required\":[\"group\",\"to\",\"reason\"]}}" +
                "]}";
        }
    }
}
// #MAU_CHECKSUM:SHA256:DC1BD5011C84C5610BFAF10F3EC153F30B69597F460631DC5BC36CD583E95A0C
