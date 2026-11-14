// ═══════════════════════════════════════════════════
// 积木: tools.shcat
// ID:   BRIK-TOOLS-021
// 类别: TOOLS
// 作用: 返回 ShCat 工具组全部工具的 OpenAI 兼容定义 JSON（宿主工具池拼装原料——design-ch4-tools-pool）
// 依赖: 无
// 引用: 无（纯文本常量）
// 原理: GetToolsJson() 返回本组工具定义整包 JSON——改描述只动本文件，mau proj 重建即生效
//       platforms 字段 = 暴露面平台声明（design-ch4-platform §五）：宿主 ToolPool 单点过滤，Windows 侧 sh-exec 不入池
// 常用: FL_ShCat.GetToolsJson() 自曝调用面（IFlow 接口）
// ═══════════════════════════════════════════════════
namespace Mau.Bricks
{
    /// <summary>
    /// 工具定义积木——ShCat 组工具定义 JSON（OpenAI 兼容拼装原料）
    /// </summary>
    public static class ToolsShcatBrick
    {
        /// <summary>
        /// 获取本组工具定义 JSON——{"group":"ShCat","tools":[{name,platforms,description,parameters}]}
        /// </summary>
        /// <returns>工具定义 JSON 字符串</returns>
        public static string GetToolsJson()
        {
            return "{\"group\":\"ShCat\",\"tools\":[" +
                "{\"name\":\"sh-exec\",\"platforms\":[\"linux\"],\"description\":\"【单行指令·bash（Linux）】仅支持「命令 + 字面量参数」——不接受任何形式的可运行代码注入：禁脚本块、过程语句、多段（分号/&&/换行）、管道、重定向——多步拆成多次工具调用（同轮可并发）。禁绕过工具组另起功能：文件读写走 text-*、列目录走 file-tree、起进程走宿主通道、判断归你自己。返回 JSON（exit/stdoutLines/stderrLines/truncated/timeout）。Windows 侧不提供本工具（走 powershell / powershell7）\",\"parameters\":{\"type\":\"object\",\"properties\":{\"command\":{\"type\":\"string\",\"description\":\"bash 命令文本（单行指令：命令 + 字面量参数）\"},\"timeout_ms\":{\"type\":\"integer\",\"description\":\"超时毫秒（默认 30000，上限 120000）\"}},\"required\":[\"command\"]}}" +
                "]}";
        }
    }
}
// #MAU_CHECKSUM:SHA256:472FEF2D3C857A68C802FAFD305612F2F7B4CB0A3F2F22425C55E8355AB5E147
