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
                "{\"name\":\"config-list\",\"description\":\"配置全览——全部已落盘配置项（键/值/段清单）；敏感键掩码；未落盘的键不列出（默认值活在消费侧，配置面不登记）\",\"parameters\":{\"type\":\"object\",\"properties\":{}}}," +
                "{\"name\":\"config-get\",\"description\":\"配置单项查询——按键返回已落盘值；键未落盘报 NOT_FOUND 并给段清单；敏感键掩码\",\"parameters\":{\"type\":\"object\",\"properties\":{\"key\":{\"type\":\"string\",\"description\":\"配置键（如 ui.font_scale）\"}},\"required\":[\"key\"]}}," +
                "{\"name\":\"config-set\",\"description\":\"配置写入——任意键直接写（纯 KV：内存 + 原子落盘，失败回滚）；键名只允许字母/数字/下划线/点；掩码值拒绝；**value 传空串 = 清除该项落盘值（回到默认）**；llm.* 私密环境变量只读\",\"parameters\":{\"type\":\"object\",\"properties\":{\"key\":{\"type\":\"string\",\"description\":\"配置键（带段前缀，如 x.enabled）\"},\"value\":{\"type\":\"string\",\"description\":\"新值（掩码值拒绝；空串 = 清除该项，回到默认）\"}},\"required\":[\"key\",\"value\"]}}," +
                "{\"name\":\"config-reset\",\"description\":\"配置还原默认——删除落盘值（回到未设置，消费侧内置默认生效）；key 空=全部落盘项，key 非空=单项\",\"parameters\":{\"type\":\"object\",\"properties\":{\"key\":{\"type\":\"string\",\"description\":\"配置键（空=全群）\"}},\"required\":[]}}," +
                "{\"name\":\"config-cat-get\",\"description\":\"每猫配置读取——返回指定猫 cat.cfg 全部字段；特权面——仅主干会话可调\",\"parameters\":{\"type\":\"object\",\"properties\":{\"cat\":{\"type\":\"string\",\"description\":\"目标猫寻址键（majordomo=主干会话；多猫=会话 id / 显示名 / id 前缀唯一）\"}},\"required\":[\"cat\"]}}," +
                "{\"name\":\"config-cat-set\",\"description\":\"每猫配置写入——字段级合并写（读现值→改单字段→全量写回，未提交字段逐字保留）；apiConfigId 立即生效，persona/toolNames/injectList/packs/enabledRoots 新会话生效；返回落盘实况；toolNames 中 group-catcfg-set / group-newsession 属硬编码受限名单——填入会被剔除并在回执出声（只拦新增，现值已持有的保留）\",\"parameters\":{\"type\":\"object\",\"properties\":{\"cat\":{\"type\":\"string\",\"description\":\"目标猫寻址键\"},\"field\":{\"type\":\"string\",\"description\":\"字段名（displayName/type/apiConfigId/persona/toolNames/injectList/packs/enabledRoots/dbLibs/userInjectIdleOnly/appearanceId/reasoningEffort/themeColor/qqbotId/qqbotEnable/qqbotGroups/qqbotCanSend——共 17 字段，可用字段以 cat.cfg.set 清单为准）\"},\"value\":{\"type\":\"string\",\"description\":\"新值；数组类字段用逗号分隔（如 enabledRoots=\\\"CCBP,Git,mau\\\"）\"}},\"required\":[\"cat\",\"field\",\"value\"]}}" +
                "]}";
        }
    }
}
// #MAU_CHECKSUM:SHA256:486809975E5FCC59D8D28C64310773A600D2415E39C085F6959AFF46E7B8E6F2
