// ═══════════════════════════════════════════════════
// 积木: tools.textcat
// ID:   BRIK-TOOLS-001
// 类别: TOOLS
// 作用: 返回 TextCat 工具组全部工具（A67 后 7 件·内容面）的 OpenAI 兼容定义 JSON（宿主工具池拼装原料——design-ch4-tools-pool）
// 依赖: 无
// 引用: 无（纯文本常量）
// 原理: GetToolsJson() 返回本组工具定义整包 JSON——改描述只动本文件，mau proj 重建即生效
// 常用: FL_TextCat.GetToolsJson() 自曝调用面（IFlow 接口）
// ═══════════════════════════════════════════════════
namespace Mau.Bricks
{
    /// <summary>
    /// 工具定义积木——TextCat 组工具定义 JSON（OpenAI 兼容拼装原料）
    /// </summary>
    public static class ToolsTextcatBrick
    {
        /// <summary>
        /// 获取本组工具定义 JSON——{"group":"TextCat","tools":[{name,description,parameters}]}
        /// </summary>
        /// <returns>工具定义 JSON 字符串</returns>
        public static string GetToolsJson()
        {
            return "{\"group\":\"TextCat\",\"tools\":[" +
                "{\"name\":\"text-read\",\"description\":\"读取 UTF-8 文本文件（编码自动探测），返回完整内容\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\"文件路径\"}},\"required\":[\"path\"]}}," +
                "{\"name\":\"text-write\",\"description\":\"覆写文件（含新建）——整文件替换为 content（不可逆——改某处优先 text-replace，覆写前先读）\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\"目标文件路径\"},\"content\":{\"type\":\"string\",\"description\":\"完整新内容\"}},\"required\":[\"path\",\"content\"]}}," +
                "{\"name\":\"text-append\",\"description\":\"追加文本到文件末尾（文件不存在则新建）\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\"目标文件路径\"},\"content\":{\"type\":\"string\",\"description\":\"要追加的文本\"}},\"required\":[\"path\",\"content\"]}}," +
                "{\"name\":\"text-replace\",\"description\":\"锚点替换——mode=exact/ignore_case 要求 old 全文唯一（出现多次报 ANCHOR_AMBIGUOUS 附候选行）；mode=all 全部出现替换返回数量；mode=regex 正则全部匹配支持 $1 捕获组；未找到报 ANCHOR_NOT_FOUND 附差异事实面（偏移 / 行号 / 已匹配前缀 / 差异字符码点 / 空白连续段个数）+ 定向提示——按「差异在锚点第 N 字符」校正，不盲换锚点；锚点按标准 JSON 语义：字面反斜杠写成两个反斜杠、字面引号写成反斜杠加引号，取 text-read 原文行，勿把源码字符串结束引号当作转义引号；new 不可为空——删除匹配文本请传删除标记「黑暗剑+22」（按空文本落盘，四模式通用）\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\"目标文件路径\"},\"old\":{\"type\":\"string\",\"description\":\"要查找的旧文本（锚点；exact/ignore_case 须全文唯一；含转义符按标准 JSON 语义）\"},\"new\":{\"type\":\"string\",\"description\":\"替换后的新文本（不可为空）\"},\"mode\":{\"type\":\"string\",\"description\":\"exact（默认，唯一锚点）| ignore_case（唯一锚点不区分大小写）| all（字面量全部替换）| regex（正则全部匹配）\"}},\"required\":[\"path\",\"old\",\"new\"]}}," +
                "{\"name\":\"text-read_lines\",\"description\":\"按行号区间读取文本（1 起；end 省略读至文件尾）——大文件省 token\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\"文件路径\"},\"start\":{\"type\":\"integer\",\"description\":\"起始行（1 起，默认 1）\"},\"end\":{\"type\":\"integer\",\"description\":\"结束行（默认文件尾）\"}},\"required\":[\"path\"]}}," +
                "{\"name\":\"text-read_between\",\"description\":\"锚点区间读取——str1 与 str2 之间内容（str1 空=文件头 / str2 空=文件尾；锚点须全文唯一；含转义符按标准 JSON 语义——同 text-replace）——大文件精确取段\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\"文件路径\"},\"str1\":{\"type\":\"string\",\"description\":\"起始锚点（空=文件头）\"},\"str2\":{\"type\":\"string\",\"description\":\"结束锚点（空=文件尾）\"}},\"required\":[\"path\"]}}," +
                "{\"name\":\"text-grep\",\"description\":\"内容关键词搜索——目录内递归扫文本文件，返回 相对路径:行号:上下文（前后 ≤10 字符）；超限时末行附 [截断]（含总条数）——定位代码/文档关键词\",\"parameters\":{\"type\":\"object\",\"properties\":{\"dir\":{\"type\":\"string\",\"description\":\"搜索根目录\"},\"keyword\":{\"type\":\"string\",\"description\":\"搜索关键词（大小写敏感）\"},\"pattern\":{\"type\":\"string\",\"description\":\"文件名过滤（默认 *）\"},\"limit\":{\"type\":\"integer\",\"description\":\"最大结果（默认 200；触顶时末行给出总条数）\"}},\"required\":[\"dir\",\"keyword\"]}}" +
                "]}";
        }
    }
}
// #MAU_CHECKSUM:SHA256:A68224961B44ACF675052A961804AE3383AC0279E4130D5401ABCA03FE3F9BDE
