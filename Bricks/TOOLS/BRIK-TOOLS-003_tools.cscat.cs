// ═══════════════════════════════════════════════════
// 积木: tools.cscat
// ID:   BRIK-TOOLS-003
// 类别: TOOLS
// 作用: 返回 CsCat 工具组全部工具的 OpenAI 兼容定义 JSON（宿主工具池拼装原料——design-ch4-tools-pool）
// 依赖: 无
// 引用: 无（纯文本常量）
// 原理: GetToolsJson() 返回本组工具定义整包 JSON——改描述只动本文件，mau proj 重建即生效
// 常用: FL_CsCat.GetToolsJson() 自曝调用面（IFlow 接口）
// ═══════════════════════════════════════════════════
namespace Mau.Bricks
{
    /// <summary>
    /// 工具定义积木——CsCat 组工具定义 JSON（OpenAI 兼容拼装原料）
    /// </summary>
    public static class ToolsCscatBrick
    {
        /// <summary>
        /// 获取本组工具定义 JSON——{"group":"CsCat","tools":[{name,description,parameters}]}
        /// </summary>
        /// <returns>工具定义 JSON 字符串</returns>
        public static string GetToolsJson()
        {
            return "{\"group\":\"CsCat\",\"tools\":[" +
                "{\"name\":\"cs-check\",\"description\":\"C# 语法层验证——写完代码后的第一轮全量语法检查（逐文件语法诊断，不解析类型/引用；多项目入口聚合分组输出）；full=true 含语法警告；默认附空 catch 块检测（CS_EMPTY_CATCH——块内无语句即报（注释不算），计入 warnings）；程序集引用与编译裁决以 cs-build 为唯一权威\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\"csproj / .sln / 目录\"},\"full\":{\"type\":\"boolean\",\"description\":\"true=输出全部语法警告\"}},\"required\":[\"path\"]}}," +
                "{\"name\":\"cs-build\",\"description\":\"C# 实机编译——dotnet build 子进程（唯一权威裁决；成功后引用集自动刷新；多项目入口逐个执行）\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\"csproj / .sln / 目录\"}},\"required\":[\"path\"]}}," +
                "{\"name\":\"cs-test\",\"description\":\"C# 跑测——dotnet test 子进程（csproj / .sln / 目录三态；回执降噪——只留每项目摘要行『通过 N / 失败 M』，剥还原 / 编译噪声）；filter=测试过滤器（--filter）；noBuild=true 跳过编译直跑（依赖先 cs-build 出产物）；超时 120s\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\"csproj / .sln / 目录\"},\"filter\":{\"type\":\"string\",\"description\":\"测试过滤器（--filter 值，如 FullyQualifiedName~Foo）\"},\"noBuild\":{\"type\":\"boolean\",\"description\":\"true=跳过编译直跑测试\"}},\"required\":[\"path\"]}}," +
                "{\"name\":\"cs-list\",\"description\":\"类/成员签名清单（语法层；class 空=全项目类清单；多项目入口聚合分组输出）\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\"csproj / .sln / 目录\"},\"class\":{\"type\":\"string\",\"description\":\"类名（空=全项目）\"}},\"required\":[\"path\"]}}," +
                "{\"name\":\"cs-read\",\"description\":\"成员源码 + 文件行号标注（统一文件坐标系；member 空=类概览；多项目入口按 class 定位所属项目）——🔴 修改前必读，不凭记忆改\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\"csproj / .sln / 目录\"},\"class\":{\"type\":\"string\",\"description\":\"类名\"},\"member\":{\"type\":\"string\",\"description\":\"成员名（空=类概览；支持签名后缀如 SubmitChoice(int) 区分重载；.ctor/类名=构造函数）\"}},\"required\":[\"path\",\"class\"]}}," +
                "{\"name\":\"cs-find_ref\",\"description\":\"成员全引用（含重载全匹配；语义级）——多项目入口扫描全部项目，跨程序集命中标 [跨程序集]\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\"csproj / .sln / 目录\"},\"class\":{\"type\":\"string\",\"description\":\"类名\"},\"member\":{\"type\":\"string\",\"description\":\"成员名\"}},\"required\":[\"path\",\"class\",\"member\"]}}," +
                "{\"name\":\"cs-find\",\"description\":\"符号查找——按名字（包含匹配，不区分大小写）在多项目入口内定位声明：类 / 方法 / 构造函数 / 属性 / 字段 / 事件 → [项目] 文件:行: 种类 全名（已知名字反查定义位置；引用方向用 cs-find_ref）；精确匹配优先，输出上限 60 条\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\"csproj / .sln / 目录\"},\"name\":{\"type\":\"string\",\"description\":\"查找值（类名 / 成员名，包含匹配）\"}},\"required\":[\"path\",\"name\"]}}," +
                "{\"name\":\"cs-patch\",\"description\":\"方法体级替换（锚点=类+方法名；body 完整含大括号；多项目入口按 class 定位所属项目）——支持方法 + 构造函数（锚点 .ctor / 类名），不支持属性访问器 / 事件 / 析构 / 运算符（改其体走 text-replace）——两态：OK 已落盘（编译诊断随 compileErrors 返回）/ ERR 未落盘——改完必 cs-check（语法）+ cs-build（唯一权威）\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\"csproj / .sln / 目录\"},\"class\":{\"type\":\"string\",\"description\":\"类名\"},\"method\":{\"type\":\"string\",\"description\":\"方法名（.ctor/类名=构造函数）\"},\"body\":{\"type\":\"string\",\"description\":\"新方法体（含大括号）\"}},\"required\":[\"path\",\"class\",\"method\",\"body\"]}}," +
                "{\"name\":\"cs-member\",\"description\":\"成员增删改——op=insert(增 单成员 code / 批量 codes 返回落盘行号区间)/delete(删 含注释)/rename(改名 全项目引用同步)；多项目入口按 class 定位所属项目\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\"csproj / .sln / 目录\"},\"class\":{\"type\":\"string\",\"description\":\"类名\"},\"op\":{\"type\":\"string\",\"description\":\"insert|delete|rename\"},\"member\":{\"type\":\"string\",\"description\":\"delete 用：待删除成员名（支持签名后缀如 SubmitChoice(int) 区分重载；.ctor/类名=构造函数）\"},\"position\":{\"type\":\"string\",\"description\":\"insert 用：end|before|after|after_fields\"},\"anchor\":{\"type\":\"string\",\"description\":\"before/after 用：锚点成员名\"},\"code\":{\"type\":\"string\",\"description\":\"insert 用：单成员声明源码（与 codes 互斥）\"},\"codes\":{\"type\":\"array\",\"items\":{\"type\":\"string\"},\"description\":\"insert 用：批量成员声明源码数组（与 code 互斥——整批一次落盘，可含互相引用的成组成员）\"},\"oldName\":{\"type\":\"string\",\"description\":\"rename 用：旧成员名\"},\"newName\":{\"type\":\"string\",\"description\":\"rename 用：新成员名\"}},\"required\":[\"path\",\"class\",\"op\"]}}," +
                "{\"name\":\"cs-comment\",\"description\":\"XML 注释增改——type=summary/param/returns（param 需 param=参数名）；多项目入口按 class 定位所属项目\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\"csproj / .sln / 目录\"},\"class\":{\"type\":\"string\",\"description\":\"类名\"},\"member\":{\"type\":\"string\",\"description\":\"成员名（空=类；支持签名后缀如 SubmitChoice(int) 区分重载；.ctor/类名=构造函数）\"},\"type\":{\"type\":\"string\",\"description\":\"summary|param|returns\"},\"text\":{\"type\":\"string\",\"description\":\"注释文本\"},\"param\":{\"type\":\"string\",\"description\":\"type=param 时的参数名\"}},\"required\":[\"path\",\"class\",\"type\",\"text\"]}}," +
                "{\"name\":\"cs-dead\",\"description\":\"零引用成员扫描（private/internal；public/override 跳过）——多项目入口含跨程序集引用复核（被兄弟项目调用的成员不误报）\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\"csproj / .sln / 目录\"}},\"required\":[\"path\"]}}," +
                "{\"name\":\"cs-comment_check\",\"description\":\"缺 summary 注释扫描（类 + 成员；交付自检链三件之一：check 编译 / comment_check 注释 / dead 零引用；多项目入口聚合分组输出）\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\"csproj / .sln / 目录\"}},\"required\":[\"path\"]}}," +
                "{\"name\":\"cs-format\",\"description\":\"空白/缩进规整（Roslyn Formatter；零语义变更——token 流 + directive 双校验）；入口 .cs 文件 / 目录 / csproj（.sln 不吃——报 ENTRY_UNSUPPORTED）——mode=check 干跑差异清单（零写入）/ apply 写盘（保真 BOM 与换行）\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\".cs 文件 / 目录 / csproj\"},\"mode\":{\"type\":\"string\",\"description\":\"check（默认，零写入）| apply（写盘）\"}},\"required\":[\"path\"]}}" +
                "]}";
        }
    }
}
// #MAU_CHECKSUM:SHA256:33AAB67E084A8DF8961087A426B797C7249801ACF582F4F4C734CD8187B94F1D
