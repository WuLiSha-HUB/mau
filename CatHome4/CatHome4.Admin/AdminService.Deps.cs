using System;
using System.Collections.Generic;
using System.Text.Json;
using CatHome4.Contracts;
using CatHome4.Http;
using Mau.Runtime;
using CH4;

namespace CatHome4.Admin
{
    /// <summary>
    /// Admin 域依赖注入面 + 工具名单辅助——入口壳 Bootstrap 一次性注入（S4 程序集拆分：管理 API 处理器 + cat.* 指令族迁入）。
    /// 依赖：ChatBridge（Core）/OA（宿主）/工具执行器/快照构建/线程守卫/html 根；数据根沿用 CatEntry 域 _dataRoot 字段。
    /// </summary>
    internal static partial class AdminService
    {
        /// <summary>数据根——Bootstrap 注入（Admin 全域共用；猫域经 using static 引用同一份，不另存副本）。</summary>
        internal static string _dataRoot;

        // [段1] 依赖注入字段——入口壳 Configure 一次性设置（命名与入口壳原静态成员一致——引用零改动）
        /// <summary>会话协调桥——默认猫/多猫会话注册（Core 域；入口壳注入）</summary>
        public static ChatBridge _chatBridge;

        /// <summary>OA 工单平台——ChatSession 构造（宿主注入）</summary>
        public static OA _oa;

        /// <summary>宿主主线程 ID——HTTP 线程分流判断（入口壳注入）</summary>
        public static int _mainThreadId;

        /// <summary>宿主工具直执回调——ChatSession 构造（入口壳注入；仅 host-* 延迟直执使用）</summary>
        public static Func<string, string, string> ExecuteTool;

        /// <summary>环境信息构建回调——ChatSession info 工具注入（入口壳注入；M4e 猫级白名单 roots 视图）</summary>
        public static Func<string> BuildEnvInfoProvider;

        /// <summary>本轮结束通知回调——ChatSession AttachRoundNotify 注入（入口壳接托盘 BalloonTip；Q 系统通知）</summary>
        public static Action<string, string> NotifyBalloon;

        /// <summary>快照 JSON 构建——StartCatHost 注入（入口壳注入）</summary>
        public static Func<bool, string> BuildSnapshotJson;

        /// <summary>html 根解析——StartCatHost 注入（入口壳适配）</summary>
        public static IHtmlRootProvider HtmlRoot;

        /// <summary>端口段——入口壳注入（每猫与 majordomo 独立端口按区段分配——A74）</summary>
        public static PortBand PortBand;

        /// <summary>
        /// 注入 Admin 域依赖——入口壳 Bootstrap 调用（S4：程序集拆分接线）。
        /// </summary>
        /// <param name="bridge">会话协调桥（Core）</param>
        /// <param name="oa">OA 工单平台（宿主）</param>
        /// <param name="dataRoot">数据根（CatEntry 域 _dataRoot）</param>
        /// <param name="mainThreadId">宿主主线程 ID</param>
        /// <param name="executeTool">宿主工具直执回调（入口壳工具域；仅 host-* 延迟直执使用）</param>
        /// <param name="snapshotBuilder">快照 JSON 构建</param>
        /// <param name="htmlRoot">html 根解析（入口壳适配）</param>
        /// <param name="apiStore">LLM API 配置池（catcfg.apply 重建 Runtime 消费）</param>
        /// <param name="qqBotStore">QQ Bot 配置池</param>
        /// <param name="appearanceStore">外观配置池（每猫外观引用解析消费——A244）</param>
        /// <param name="globalConfig">全局配置存储（catcfg.apply 消费）</param>
        /// <param name="portBand">端口段（每猫与 majordomo 独立端口按区段分配——A74）</param>
        public static void Configure(ChatBridge bridge, OA oa, string dataRoot, int mainThreadId, Func<string, string, string> executeTool, Func<bool, string> snapshotBuilder, IHtmlRootProvider htmlRoot, CH_LlmApiConfigStore apiStore, CH_QqBotConfigStore qqBotStore, CH_AppearanceConfigStore appearanceStore, ConfigStore globalConfig, PortBand portBand)
        {
            _chatBridge = bridge;
            _oa = oa;
            _dataRoot = dataRoot;
            _mainThreadId = mainThreadId;
            ExecuteTool = executeTool;
            BuildSnapshotJson = snapshotBuilder;
            HtmlRoot = htmlRoot;
            _apiStore = apiStore;
            _qqBotStore = qqBotStore;
            _appearanceStore = appearanceStore;
            _globalConfig = globalConfig;
            PortBand = portBand;
        }

        // [段2] 工具名单处理——自持（原 Program.Tools.cs 静态面迁入；ToolRegistry 为 Core 静态注册表）
        /// <summary>
        /// 全量工具名清单——每次从 ToolRegistry.BuildSpecs 派生（声明表是唯一真相源）。
        /// 🔴 不缓存——热重载/组重翻后清单即时同步（旧懒加载缓存在重载路径不失效 → 新工具被白名单过滤）。
        /// </summary>
        /// <returns>全量工具名数组</returns>
        internal static string[] GetAllToolNames()
        {
            ToolSpec[] specs = ToolRegistry.BuildSpecs();
            string[] names = new string[specs.Length];
            for (int i = 0; i < specs.Length; i = i + 1)
            {
                names[i] = specs[i].Name;
            }
            return names;
        }
        /// <summary>
        /// 可配置工具名清单——全量剔除特权工具（可配白名单：cat.cfg 名单写时校验 / 新猫模板校验；design-ch4-tools §三·十 2026-09-29 修订）。
        /// </summary>
        /// <returns>可配置工具名数组</returns>
        internal static string[] GetConfigurableToolNames()
        {
            string[] all = GetAllToolNames();
            List<string> list = new List<string>();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                if (!IsPrivilegedName(all[i]))
                {
                    list.Add(all[i]);
                }
            }
            return list.ToArray();
        }
        /// <summary>
        /// 配置面勾选清单（全量 + 特权标记）——配置面渲染数据源：特权工具可见不可选（design-ch4-tools §三·十 渲染三态，2026-09-29）。
        /// 可配白名单仍走 GetConfigurableToolNames——校验面不放开。
        /// </summary>
        /// <returns>工具名 + 组别 + 特权标记数组（name/group/privileged）</returns>
        internal static object[] GetToolsWithGroupForConfig()
        {
            ToolDef[] defs = ToolPool.All();
            List<object> result = new List<object>();
            for (int i = 0; i < defs.Length; i = i + 1)
            {
                result.Add(new { name = defs[i].Name, group = defs[i].Group, privileged = IsPrivilegedName(defs[i].Name), order = ToolOrderTable.OrderText(defs[i].Name) });
            }
            return result.ToArray();
        }
        /// <summary>
        /// 工具组定义缺陷清单——配置面暴露（A107：不可用组在前端可见，不静默消失）。
        /// </summary>
        /// <returns>缺陷对象数组（group / stage / reason）</returns>
        internal static object[] BuildToolDefectItems()
        {
            ToolDefect[] defects = ToolPool.Defects();
            List<object> list = new List<object>();
            for (int i = 0; i < defects.Length; i = i + 1)
            {
                list.Add(new { group = defects[i].Group, stage = defects[i].Stage, reason = defects[i].Reason });
            }
            return list.ToArray();
        }
        /// <summary>
        /// 清单里已失效的工具名——cat.cfg 名单含而注册表（池）无（A107：保存时会被静默剔除的那些名）。
        /// </summary>
        /// <param name="raw">cat.cfg toolNames 原始串</param>
        /// <returns>失效名数组（空 = 全部合法）</returns>
        internal static string[] ResolveStaleToolNames(string raw)
        {
            if (raw == null || raw.Trim().Length == 0)
            {
                return new string[0];
            }
            string[] all = GetAllToolNames();
            List<string> stale = new List<string>();
            string[] parts = raw.Split(new char[] { ',', ' ', '\t' }, StringSplitOptions.RemoveEmptyEntries);
            for (int i = 0; i < parts.Length; i = i + 1)
            {
                string name = parts[i].Trim();
                if (name.Length == 0 || name == "*")
                {
                    continue;
                }
                if (!ContainsToolName(all, name))
                {
                    stale.Add(name);
                }
            }
            return stale.ToArray();
        }
        /// <summary>
        /// 特权工具判定——读注册面组级标记（ToolRegistry 单一真相源；未注册 = 非特权）。
        /// </summary>
        /// <param name="name">工具名</param>
        /// <returns>true=特权（仅默认会话永久激活，不入配置面）</returns>
        internal static bool IsPrivilegedName(string name)
        {
            ToolRegistryEntry entry = ToolRegistry.Find(name);
            if (entry == null)
            {
                return false;
            }
            return entry.Privileged;
        }
        /// <summary>
        /// 特权工具全量名——默认会话永久激活集来源（不入 cat.cfg 名单；莎 2026-09-28 定）。
        /// </summary>
        /// <returns>特权工具名数组</returns>
        internal static string[] GetAllPrivilegedToolNames()
        {
            string[] all = GetAllToolNames();
            List<string> list = new List<string>();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                if (IsPrivilegedName(all[i]))
                {
                    list.Add(all[i]);
                }
            }
            return list.ToArray();
        }
        /// <summary>
        /// 特权工具并入——默认会话永久激活（去重并入；其他会话原样返回）。
        /// </summary>
        /// <param name="names">名单解析结果（可配置面）</param>
        /// <param name="includePrivileged">是否并入特权工具</param>
        /// <returns>最终工具名数组</returns>
        internal static string[] AppendPrivilegedNames(string[] names, bool includePrivileged)
        {
            if (!includePrivileged)
            {
                return names;
            }
            string[] privileged = GetAllPrivilegedToolNames();
            List<string> list = new List<string>();
            for (int i = 0; i < names.Length; i = i + 1)
            {
                list.Add(names[i]);
            }
            for (int i = 0; i < privileged.Length; i = i + 1)
            {
                if (!ContainsToolName(names, privileged[i]))
                {
                    list.Add(privileged[i]);
                }
            }
            return list.ToArray();
        }
        /// <summary>
        /// 名单解析（可配置口径）——白名单 = 可配置清单；空 / "*" / 过滤后全空 → 全量保底（去特权）。
        /// </summary>
        /// <param name="raw">cat.cfg toolNames 原始串（逗号/空白分隔）</param>
        /// <returns>可配置工具名数组（保底去特权）</returns>
        internal static string[] ResolveConfigurableNames(string raw)
        {
            string[] all = GetConfigurableToolNames();
            if (raw == null || raw.Trim().Length == 0)
            {
                return all;
            }
            string[] parts = raw.Split(new char[] { ',', ' ', '\t' }, StringSplitOptions.RemoveEmptyEntries);
            List<string> names = new List<string>();
            for (int i = 0; i < parts.Length; i = i + 1)
            {
                string name = parts[i].Trim();
                if (name == "*")
                {
                    return all;
                }
                if (ContainsToolName(all, name))
                {
                    names.Add(name);
                }
            }
            if (names.Count == 0)
            {
                return all;
            }
            return names.ToArray();
        }
        /// <summary>
        /// 工具名单解析（含特权并入）——默认会话永久激活口径（design-ch4-tools §三·十）。
        /// 白名单 = 可配置清单（特权名不入名单）；includePrivileged=true 时结果并入全部特权工具。
        /// </summary>
        /// <param name="raw">cat.cfg toolNames 原始串</param>
        /// <param name="includePrivileged">是否并入特权工具（默认会话 true）</param>
        /// <returns>工具名数组（空 / "*" / 全非法 → 全量保底，按同口径含或去特权）</returns>
        internal static string[] ResolveToolNames(string raw, bool includePrivileged)
        {
            return AppendPrivilegedNames(ResolveConfigurableNames(raw), includePrivileged);
        }

        /// <summary>
        /// 工具名比对——线性扫描内置清单。
        /// </summary>
        /// <param name="names">清单数组</param>
        /// <param name="name">目标名</param>
        /// <returns>true=在清单内</returns>
        internal static bool ContainsToolName(string[] names, string name)
        {
            for (int i = 0; i < names.Length; i++)
            {
                if (string.Equals(names[i], name, StringComparison.Ordinal))
                {
                    return true;
                }
            }
            return false;
        }

        /// <summary>
        /// 工具名单写时校验——序列化落盘前比对工具池（M2c：池外名剔除，合法名逗号重拼）。
        /// A132 剔除出声——被剔名字经 removed 带回（调用方在响应面报告），同时记日志（失败必须可见，不静默）。
        /// </summary>
        /// <param name="raw">待写入工具名单原始串</param>
        /// <param name="removed">被剔除名字清单（工具池中不存在；空 = 无剔除）</param>
        /// <returns>过滤后逗号清单（空串=全量语义）</returns>
        internal static string ValidateToolNames(string raw, out string[] removed)
        {
            removed = new string[0];
            if (raw == null || raw.Trim().Length == 0)
            {
                return "";
            }
            string[] all = GetConfigurableToolNames();
            string[] parts = raw.Split(new char[] { ',', ' ', '\t' }, StringSplitOptions.RemoveEmptyEntries);
            List<string> names = new List<string>();
            List<string> dropped = new List<string>();
            for (int i = 0; i < parts.Length; i++)
            {
                string name = parts[i].Trim();
                if (name == "*" || ContainsToolName(all, name))
                {
                    names.Add(name);
                }
                else
                {
                    dropped.Add(name);
                }
            }
            if (dropped.Count > 0)
            {
                removed = dropped.ToArray();
                LogStore.Add("CatHome4", 2, "工具名单写时剔除 " + dropped.Count.ToString() + " 个池外工具名：" + string.Join(",", dropped.ToArray()) + "（工具池中不存在——名单以池为准）", "CONFIG");
            }
            return string.Join(",", names.ToArray());
        }

        /// <summary>
        /// 工具名单写时校验（无报告面）——内部落盘兜底路径调用；剔除仍记日志（出声不静默）。
        /// </summary>
        /// <param name="raw">待写入工具名单原始串</param>
        /// <returns>过滤后逗号清单（空串=全量语义）</returns>
        internal static string ValidateToolNames(string raw)
        {
            string[] removed;
            return ValidateToolNames(raw, out removed);
        }

        /// <summary>
        /// 工具声明面裁剪——按名单从全量表取子集（M2c 每会话独立声明面）；特权组按 includePrivileged 处置（默认会话永久激活 / 其他会话恒剔除；莎 2026-09-28）。
        /// </summary>
        /// <param name="names">合法工具名数组（ResolveToolNames 产物）</param>
        /// <param name="includePrivileged">是否并入特权工具（默认会话 true）</param>
        /// <returns>裁剪后工具数组；名单全非法 → 全量保底（按同口径含或去特权）</returns>
        internal static ToolSpec[] FilterToolSpecs(string[] names, bool includePrivileged)
        {
            ToolSpec[] all = ToolRegistry.BuildSpecs();
            List<ToolSpec> list = new List<ToolSpec>();
            for (int i = 0; i < all.Length; i = i + 1)
            {
                ToolSpec spec = all[i];
                if (IsPrivilegedName(spec.Name))
                {
                    // 特权面——默认会话永久激活（不受名单约束）；其他会话恒剔除（莎 2026-09-28）
                    if (includePrivileged)
                    {
                        list.Add(spec);
                    }
                    continue;
                }
                if (ContainsToolName(names, spec.Name))
                {
                    list.Add(spec);
                }
            }
            if (list.Count == 0)
            {
                // 全空保底——全量（默认会话含特权，其他会话去特权）
                List<ToolSpec> fallback = new List<ToolSpec>();
                for (int i = 0; i < all.Length; i = i + 1)
                {
                    if (includePrivileged || !IsPrivilegedName(all[i].Name))
                    {
                        fallback.Add(all[i]);
                    }
                }
                return fallback.ToArray();
            }
            return list.ToArray();
        }

        // [段3] JSON 辅助——自持（原 Program.Chat.cs 静态面迁入）
        /// <summary>
        /// 读取 JSON 对象字符串属性——防御式（缺字段返回空串）
        /// </summary>
        /// <param name="obj">JSON 对象</param>
        /// <param name="prop">属性名</param>
        /// <returns>属性值</returns>
        internal static string GetStringProp(JsonElement obj, string prop)
        {
            JsonElement value;
            if (obj.TryGetProperty(prop, out value) && value.ValueKind == JsonValueKind.String)
            {
                string got = value.GetString();
                if (got != null)
                {
                    return got;
                }
            }
            return "";
        }
    }
}
