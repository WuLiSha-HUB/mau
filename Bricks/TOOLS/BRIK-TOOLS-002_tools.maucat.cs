// ═══════════════════════════════════════════════════
// 积木: tools.maucat
// ID:   BRIK-TOOLS-002
// 类别: TOOLS
// 作用: 返回 MauCat 工具组全部工具的 OpenAI 兼容定义 JSON（宿主工具池拼装原料——design-ch4-tools-pool）
// 依赖: 无
// 引用: 无（纯文本常量）
// 原理: GetToolsJson() 返回本组工具定义整包 JSON——改描述只动本文件，mau proj 重建即生效
// 常用: FL_MauCat.GetToolsJson() 自曝调用面（IFlow 接口）
// ═══════════════════════════════════════════════════
namespace Mau.Bricks
{
    /// <summary>
    /// 工具定义积木——MauCat 组工具定义 JSON（OpenAI 兼容拼装原料）
    /// </summary>
    public static class ToolsMaucatBrick
    {
        /// <summary>
        /// 获取本组工具定义 JSON——{"group":"MauCat","tools":[{name,description,parameters}]}
        /// </summary>
        /// <returns>工具定义 JSON 字符串</returns>
        public static string GetToolsJson()
        {
            return "{\"group\":\"MauCat\",\"tools\":[" +
                "{\"name\":\"mau-verify\",\"description\":\"Mau 语料全链检查（词法→解析→验证→分析），返回诊断（文件:行:错误码:消息）；零产出\",\"parameters\":{\"type\":\"object\",\"properties\":{\"file\":{\"type\":\"string\",\"description\":\".mau 文件路径\"}},\"required\":[\"file\"]}}," +
                "{\"name\":\"mau-gen\",\"description\":\"组翻译——.mauproj 组声明 → 中间产物（验证全组 + BRIKGROUP.cs + FL_*.cs）；不编译\",\"parameters\":{\"type\":\"object\",\"properties\":{\"proj\":{\"type\":\"string\",\"description\":\".mauproj 文件路径\"}},\"required\":[\"proj\"]}}," +
                "{\"name\":\"mau-proj\",\"description\":\"组翻译 + 编译——.mauproj → Flows/FL_<组>.dll（长耗时；产物可在宿主热重载）\",\"parameters\":{\"type\":\"object\",\"properties\":{\"proj\":{\"type\":\"string\",\"description\":\".mauproj 文件路径\"},\"build\":{\"type\":\"boolean\",\"description\":\"true=翻译后执行 dotnet build\"}},\"required\":[\"proj\"]}}," +
                "{\"name\":\"mau-setup\",\"description\":\"一键部署链执行（SetUp.exe）——prepare（就地自举：build/test/publish/组翻译/publish/宿主自检）/ sync-html（外观层静态资源镜像同步：源区 html → 产物区 + 运行区——前端改动刷新即生效，不走全链、不重启）/ publish-linux（Linux 部署链产物端：本地交叉构建 public/app-linux，门禁+组翻译+linux-x64 发布+指纹，不自增版本——代际由本地 prepare 产生）；阻塞至进程结束并返回 JSON 报告摘要（步明细/成败/产物）。对外部署（deploy）不属工具面——归 SetUp.exe 人工通道 + Majordomo 特权重启链；改宿主侧程序集后的默认姿势 = 先本工具 prepare（本身份动作面，直接做、不问不等），再通报 restart-incr\",\"parameters\":{\"type\":\"object\",\"properties\":{\"mode\":{\"type\":\"string\",\"description\":\"prepare（默认）| sync-html | publish-linux\"},\"target\":{\"type\":\"string\",\"description\":\"sync-html 运行区目录（可选——缺省从运行中宿主反推）\"},\"report\":{\"type\":\"string\",\"description\":\"报告 JSON 路径（默认仓库根 CatTemp/setup_report.json）\"}},\"required\":[]}}" +
                "]}";
        }
    }
}
// #MAU_CHECKSUM:SHA256:012D6D5351106DE2BA543DD21160E9E31E83831913D3C0D09A8529C651846474
