// ═══════════════════════════════════════════════════
// 积木: tools.pycat
// ID:   BRIK-TOOLS-016
// 类别: TOOLS
// 作用: 返回 PyCat 工具组全部工具（py-run / py-list / py-service 3 件）的 OpenAI 兼容定义 JSON（宿主工具池拼装原料）
// 依赖: 无
// 引用: 无（纯文本常量）
// 原理: GetToolsJson() 返回本组工具定义整包 JSON——改描述只动本文件，重建即生效
// 常用: FL_PyCat.GetToolsJson() 自曝调用面（IFlow 接口）
// ═══════════════════════════════════════════════════
namespace Mau.Bricks
{
    /// <summary>
    /// 工具定义积木——PyCat 组工具定义 JSON（OpenAI 兼容拼装原料）
    /// </summary>
    public static class ToolsPycatBrick
    {
        /// <summary>
        /// 获取本组工具定义 JSON——{"group":"PyCat","tools":[{name,description,parameters}]}
        /// </summary>
        /// <returns>工具定义 JSON 字符串</returns>
        public static string GetToolsJson()
        {
            return "{\"group\":\"PyCat\",\"tools\":[" +
                "{\"name\":\"py-run\",\"description\":\"执行一个 py 脚本（短命进程，起-跑-退）。脚本路径必须在可写受控根内。解释器路径读配置 py.python_path（未配置时明示不可用，不回落探测路径）。返回 JSON（exit/stdout/stderr/truncated/timeout/elapsedMs）。适合一次性工具脚本（素材烘焙 / 数据统计 / 探针）；常驻服务用 py-service 而非每次起进程\",\"parameters\":{\"type\":\"object\",\"properties\":{\"script\":{\"type\":\"string\",\"description\":\"脚本路径（可写受控根内，如 mau:tools/pet/convert_pet.py）\"},\"args\":{\"type\":\"string\",\"description\":\"命令行参数（可选，原样传给脚本）\"},\"timeout_ms\":{\"type\":\"integer\",\"description\":\"超时毫秒（默认 30000，上限 120000）\"}},\"required\":[\"script\"]}}" +
                ",{\"name\":\"py-list\",\"description\":\"列出可运行的 py 脚本（可写受控根内 *.py 扫描；依赖目录与虚拟环境目录自动跳过）——用于发现有哪些工具脚本可用\",\"parameters\":{\"type\":\"object\",\"properties\":{\"dir\":{\"type\":\"string\",\"description\":\"可选的受控根内子目录（空 = 仓库 tools 面）\"}}}}" +
                ",{\"name\":\"py-service\",\"description\":\"py 常驻服务的启停与状态查询。start=拉起并等就绪握手（冷启动可能数秒）/ stop=优雅退出（超时强杀兜底）/ status=查进程态。服务清单来自配置声明（py.service.<name>.script）——未声明即不可用。常驻服务与短命脚本的区别：服务加载一次模型反复用（毫秒级响应），脚本每次冷启动\",\"parameters\":{\"type\":\"object\",\"properties\":{\"name\":{\"type\":\"string\",\"description\":\"服务名（对应配置 py.service.<name>.script 的 name 段）\"},\"action\":{\"type\":\"string\",\"description\":\"start | stop | status\"}},\"required\":[\"name\",\"action\"]}}" +
                "]}";
        }
    }
}
// #MAU_CHECKSUM:SHA256:C6A7009E50D0E18721F5FEE8028F5A70FBE46D4735D42063D4EEA8027D42DC7E
