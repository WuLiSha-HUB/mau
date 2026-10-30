// ═══════════════════════════════════════════════════
// 积木: tools.tscat
// ID:   BRIK-TOOLS-019
// 类别: TOOLS
// 作用: 返回 TsCat 工具组全部工具（ts-* 五件——TypeScript 编码工具面）的 OpenAI 兼容定义 JSON
// 依赖: 无
// 引用: 无（纯文本常量）
// 原理: GetToolsJson() 返回本组工具定义整包 JSON——改描述只动本文件，mau proj 重建即生效
// 常用: FL_TsCat.GetToolsJson() 自曝调用面（IFlow 接口）
// ═══════════════════════════════════════════════════
namespace Mau.Bricks
{
    /// <summary>
    /// 工具定义积木——TsCat 组工具定义 JSON（OpenAI 兼容拼装原料）
    /// </summary>
    public static class ToolsTscatBrick
    {
        /// <summary>
        /// 获取本组工具定义 JSON——{"group":"TsCat","tools":[{name,description,parameters}]}
        /// </summary>
        /// <returns>工具定义 JSON 字符串</returns>
        public static string GetToolsJson()
        {
            return "{\"group\":\"TsCat\",\"tools\":[" +
                "{\"name\":\"ts-check\",\"description\":\"TypeScript 类型诊断——按 tsconfig 整工程编译，逐条返回 文件:行:码:消息 + 错误/警告计数；这是「能不能编译」的唯一权威面。path 支持 tsconfig（推荐）/ 目录（找 tsconfig）/ 单文件（降级为单文件口径——跨文件类型错误不可见，回执 mode 字段标注）；找不到 tsconfig 报 TS_TSCONFIG\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\"tsconfig 路径 / 目录 / 单文件（受控根寻址 <根id>:<相对> 或绝对路径）\"},\"full\":{\"type\":\"boolean\",\"description\":\"true=连警告一并返回（默认 false）\"}},\"required\":[\"path\"]}}," +
                "{\"name\":\"ts-list\",\"description\":\"TypeScript 单文件结构清单——返回 模块/全局脚本 判定 + 类/接口/函数/枚举/成员 的种类、名称、签名与行号（不展开实现）。改码前先用它定位符号名，再用 ts-read 读实现\",\"parameters\":{\"type\":\"object\",\"properties\":{\"file\":{\"type\":\"string\",\"description\":\"目标文件（受控根寻址或绝对路径）\"},\"kind\":{\"type\":\"string\",\"description\":\"过滤：class / interface / function / enum / export / all（默认 all）\"}},\"required\":[\"file\"]}}," +
                "{\"name\":\"ts-read\",\"description\":\"TypeScript 符号源码读取——返回带行号的源码段，文档注释随符号一并返回；symbol 为空=文件概览（导入段 + 全部声明头 + 行号区间）。修改前必须先读（不凭记忆改）\",\"parameters\":{\"type\":\"object\",\"properties\":{\"file\":{\"type\":\"string\",\"description\":\"目标文件\"},\"symbol\":{\"type\":\"string\",\"description\":\"符号名（空=文件概览）\"}},\"required\":[\"file\"]}}," +
                "{\"name\":\"ts-find-ref\",\"description\":\"TypeScript 引用查找（语义级，非文本搜索）——返回 tsconfig 编译面内全部引用：文件:行:种类（decl 声明 / ref 普通引用 / type-ref 类型标注 / prop 属性 / import 导入 / destructure 解构），合计给 total；种类全量返回，由调用方自行分辨噪音。重构前用它确认影响面\",\"parameters\":{\"type\":\"object\",\"properties\":{\"file\":{\"type\":\"string\",\"description\":\"符号所在文件\"},\"symbol\":{\"type\":\"string\",\"description\":\"符号名\"},\"tsconfig\":{\"type\":\"string\",\"description\":\"工程 tsconfig（空=自 file 向上查找）\"}},\"required\":[\"file\",\"symbol\"]}}," +
                "{\"name\":\"ts-patch\",\"description\":\"TypeScript 改动（双模式）——符号模式：symbol + body 整段替换该符号声明（含其文档注释）；替换模式：old + new，old 须全文唯一（删除时 new 传空串）。两模式二选一。写侧恒落盘，回执带 diff 与变更后的诊断（diagnostics / errorCount）——编译层结论仍以 ts-check 为准\",\"parameters\":{\"type\":\"object\",\"properties\":{\"file\":{\"type\":\"string\",\"description\":\"目标文件\"},\"symbol\":{\"type\":\"string\",\"description\":\"符号模式：要替换的符号名\"},\"body\":{\"type\":\"string\",\"description\":\"符号模式：新声明全文（含注释与结尾换行）\"},\"old\":{\"type\":\"string\",\"description\":\"替换模式：旧文本（须全文唯一）\"},\"new\":{\"type\":\"string\",\"description\":\"替换模式：新文本（删除传空串）\"},\"tsconfig\":{\"type\":\"string\",\"description\":\"工程 tsconfig（空=自 file 向上查找）\"}},\"required\":[\"file\"]}}" +
                "]}";
        }
    }
}
// #MAU_CHECKSUM:SHA256:2C8CA6222806A26F4F649EF70AD26F5E69B2D311077FD29FC4FE08AD253F602D
