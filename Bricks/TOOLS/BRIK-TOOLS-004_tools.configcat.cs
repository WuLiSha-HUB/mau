// ═══════════════════════════════════════════════════
// 积木: tools.configcat
// ID:   BRIK-TOOLS-004
// 类别: TOOLS
// 作用: 返回 ConfigCat 工具组全部工具的 OpenAI 兼容定义 JSON（宿主工具池拼装原料——design-ch4-tools-pool）
// 依赖: 无
// 引用: 无（纯文本常量）
// 原理: GetToolsJson() 返回本组工具定义整包 JSON——改描述只动本文件，mau proj 重建即生效
// 常用: FL_ConfigCat.GetToolsJson() 自曝调用面（IFlow 接口）
// ═══════════════════════════════════════════════════
namespace Mau.Bricks
{
    /// <summary>
    /// 工具定义积木——ConfigCat 组工具定义 JSON（OpenAI 兼容拼装原料）
    /// </summary>
    public static class ToolsConfigcatBrick
    {
        /// <summary>
        /// 获取本组工具定义 JSON——{"group":"ConfigCat","tools":[{name,description,parameters}]}
        /// </summary>
        /// <returns>工具定义 JSON 字符串</returns>
        public static string GetToolsJson()
        {
            return "{\"group\":\"ConfigCat\",\"privileged\":true,\"tools\":[" +
                "{\"name\":\"config-list\",\"description\":\"配置全览——schema 全部条目（键/当前值/来源/schema 默认/敏感/可写/值域/描述）；敏感键掩码\",\"parameters\":{\"type\":\"object\",\"properties\":{}}}," +
                "{\"name\":\"config-get\",\"description\":\"配置单项查询——按 schema 键返回（含默认/敏感/可写/值域/描述）；敏感键掩码\",\"parameters\":{\"type\":\"object\",\"properties\":{\"key\":{\"type\":\"string\",\"description\":\"配置键（如 ui.chat_font_size）\"}},\"required\":[\"key\"]}}," +
                "{\"name\":\"config-set\",\"description\":\"配置写入——schema 声明且 writable=true 的项（白名单+值域校验+原子写+失败回滚）；declare=true 时未声明键先声明入 schema（模板与运行副本同步写，段文件即时注册）再写入；llm.* 私密环境变量只读\",\"parameters\":{\"type\":\"object\",\"properties\":{\"key\":{\"type\":\"string\",\"description\":\"配置键（未声明键须带段前缀，如 x.enabled）\"},\"value\":{\"type\":\"string\",\"description\":\"新值（掩码值拒绝）\"},\"declare\":{\"type\":\"string\",\"description\":\"是否声明新键——\\\"true\\\" 时未声明键先入 schema 再写入（默认不声明，未声明键一律拒绝）\"},\"desc\":{\"type\":\"string\",\"description\":\"声明时的描述（declare=true 时生效）\"}},\"required\":[\"key\",\"value\"]}}," +
                "{\"name\":\"config-reset\",\"description\":\"配置还原默认——key 空=全群 writable 项还原 schema default；key 非空=单项\",\"parameters\":{\"type\":\"object\",\"properties\":{\"key\":{\"type\":\"string\",\"description\":\"配置键（空=全群）\"}},\"required\":[]}}," +
                "{\"name\":\"config-cat-get\",\"description\":\"每猫配置读取——返回指定猫 cat.cfg 全部字段；特权面——仅主干会话可调\",\"parameters\":{\"type\":\"object\",\"properties\":{\"cat\":{\"type\":\"string\",\"description\":\"目标猫寻址键（majordomo=主干会话；多猫=会话 id / 显示名 / id 前缀唯一）\"}},\"required\":[\"cat\"]}}," +
                "{\"name\":\"config-cat-set\",\"description\":\"每猫配置写入——字段级合并写（读现值→改单字段→全量写回，未提交字段逐字保留）；apiConfigId 立即生效，persona/toolNames/injectList/packs/enabledRoots 新会话生效；返回落盘实况；toolNames 中 group-catcfg-set / group-newsession 属硬编码受限名单——填入会被剔除并在回执出声（只拦新增，现值已持有的保留）\",\"parameters\":{\"type\":\"object\",\"properties\":{\"cat\":{\"type\":\"string\",\"description\":\"目标猫寻址键\"},\"field\":{\"type\":\"string\",\"description\":\"字段名（displayName/type/apiConfigId/persona/toolNames/injectList/packs/enabledRoots/dbLibs/userInjectIdleOnly/appearanceId/reasoningEffort/themeColor）\"},\"value\":{\"type\":\"string\",\"description\":\"新值；数组类字段用逗号分隔（如 enabledRoots=\\\"CCBP,Git,mau\\\"）\"}},\"required\":[\"cat\",\"field\",\"value\"]}}" +
                "]}";
        }
    }
}
// #MAU_CHECKSUM:SHA256:A5BB75C37E793E0A746311B91ADEC36536FF3B270006EDCA27429D5A79121763
