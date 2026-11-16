// ═══════════════════════════════════════════════════
// 积木: tools.majordomocat
// ID:   BRIK-TOOLS-009
// 类别: TOOLS
// 作用: 返回 Majordomo 工具组全部工具的 OpenAI 兼容定义 JSON（宿主工具池拼装原料——design-ch4-tools-pool）
// 依赖: 无
// 引用: 无（纯文本常量）
// 原理: GetToolsJson() 返回本组工具定义整包 JSON——改描述只动本文件，mau proj 重建即生效
//       命名铁律：工具定义积木约定名 = tools.<flowName 小写>——flowName 由语料文件名派生
//       （majordomo_cat.mau → MajordomoCat → tools.majordomocat）；名不符则静默跳过（池内无定义）
//       JSON group 取 .mauproj 声明的组名（本组 = Majordomo）——组名与积木约定名是两个面
// 常用: FL_MajordomoCat.GetToolsJson() 自曝调用面（IFlow 接口）
// ═══════════════════════════════════════════════════
namespace Mau.Bricks
{
    /// <summary>
    /// 工具定义积木——Majordomo 组工具定义 JSON（OpenAI 兼容拼装原料）。
    /// 特权组：仅默认会话（catId=majordomo）可见可调（design-ch4-host-restart §二）。
    /// 五件 = 生命周期族三件（restart-full / incr / host——代价递增分层）+ 管理族两件（cmd / catinfo）。
    /// </summary>
    public static class ToolsMajordomocatBrick
    {
        /// <summary>
        /// 获取本组工具定义 JSON——{"group":"Majordomo","tools":[{name,description,parameters}]}（JSON group 取 .mauproj 声明的组名，与积木约定名 tools.majordomocat 是两个面）
        /// </summary>
        /// <returns>工具定义 JSON 字符串</returns>
        public static string GetToolsJson()
        {
            return "{\"group\":\"Majordomo\",\"privileged\":true,\"tools\":[" +
                "{\"name\":\"restart-full\",\"description\":\"宿主全链重启（保底路径）：重建发布链（编译/测试/组翻译/自检）+ 原子切换运行区 + 重启宿主，完成后结果回执自动注入本会话。唯一带版本自增的重启方式——改语料/积木/宿主源码后的正式回填用它。架构级变更（Runtime 机制/协议/签名）不要用本工具。\",\"parameters\":{\"type\":\"object\",\"properties\":{\"target\":{\"type\":\"string\",\"description\":\"目标运行区绝对路径（缺省=受控根 mauout）\"},\"push\":{\"type\":\"string\",\"description\":\"重启后附加说明串（随部署报告一起注入本会话，可空）\"}},\"required\":[]}}," +
                "{\"name\":\"restart-incr\",\"description\":\"宿主增量重启（纯搬运）：把已在一键部署中验证过的产物区搬运到运行区（含前置离线探活）后重启——不编译、不测试、不组翻译。适用于刚跑过一键部署（prepare）后的日常回填，停摆窗口更短。改 Bricks / Mau 基座 / 语料（未重翻）会被哨兵拒绝并提示走 restart-full。\",\"parameters\":{\"type\":\"object\",\"properties\":{\"target\":{\"type\":\"string\",\"description\":\"目标运行区绝对路径（缺省=受控根 mauout）\"},\"push\":{\"type\":\"string\",\"description\":\"重启后附加说明串（随部署报告一起注入本会话，可空）\"}},\"required\":[]}}," +
                "{\"name\":\"restart-host\",\"description\":\"宿主原地重启：不编译、不覆盖运行区，仅重启宿主进程。适用于配置 / Data 面改动后重读、宿主卡死恢复。版本不变（不自增）。\",\"parameters\":{\"type\":\"object\",\"properties\":{\"target\":{\"type\":\"string\",\"description\":\"目标运行区绝对路径（缺省=受控根 mauout）\"},\"push\":{\"type\":\"string\",\"description\":\"重启后附加说明串（随部署报告一起注入本会话，可空）\"}},\"required\":[]}}," +
                "{\"name\":\"majordomo-cmd\",\"description\":\"宿主管理指令执行——把一行管理指令交给内核执行（与前端管理面板同源单内核）。允许前缀：cat.*（cat.list 全猫清单 / cat.new 新建 / cat.start 启动 / cat.stop 停止 / cat.delete 销毁 / cat.pause 中止回合 / cat.chat 投递对话 / cat.cfg.get 读每猫配置 / cat.cfg.set 写每猫配置）与 catcfg.*（catcfg.apply 配置生效）。错 key 回执附可用指令清单。特权面——仅主干会话（majordomo）可调。\",\"parameters\":{\"type\":\"object\",\"properties\":{\"cmd\":{\"type\":\"string\",\"description\":\"一行管理指令（如 cat.list / cat.cfg.get CH4Coder / cat.cfg.set CH4Coder persona 新角色段）\"}},\"required\":[\"cmd\"]}}" +
                ",{\"name\":\"majordomo-catinfo\",\"description\":\"全猫状态统计——一次调用取全部猫：运行态（是否运行 / 监听端口 / 主干标记）、会话流式态（四相环 + 七态细分）、前文长度（请求级实时 tokens 与前文条数）、最近活跃时间（最近一次前文变动时刻）、轮次 / 消息数 / 待处理 / Note 标记。返回体 = 整块分类 JSON（缩进 + 中文直显，同 info 规格；无「头 + 正文」两段）。特权面——仅主干会话（majordomo）可调。\",\"parameters\":{\"type\":\"object\",\"properties\":{},\"required\":[]}}" +
                "]}";
        }
    }
}
// #MAU_CHECKSUM:SHA256:6197DB63C98C28942D6F6B8D1E1087C493C8A14A18B3141B2398714CBF1BD9A8
