using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using Mau.Runtime;
using CatHome4.Http;
using Mau.Providers;
using CH4;
using static CatHome4.Admin.AdminService;

namespace CatHome4.Admin
{
    /// <summary>
    /// 猫宿主管理域持久化分部（A285：自 AdminService 抽出）——CatEntry/CatCfgData 实体 + cat.cfg 读写与文件清理（P9.3b 拆分自 Program.Cats.cs）。
    /// 线程模型：处理函数仅主线程调用（HTTP 线程经 P9.3c PumpCatQueues 入队泵消费）。
    /// </summary>
    internal static partial class CatHostService
    {
        /// <summary>
        /// 猫实体——注册表条目（会话 + 外观层 + 持久化态）。
        /// </summary>
        internal sealed class CatEntry
        {
            /// <summary>会话唯一 ID——创建时间戳注入（cat.cfg 持久化）</summary>
            public string Id;

            /// <summary>显示名——用户输入（cat.new 参数）</summary>
            public string DisplayName;

            /// <summary>会话 Type 标签——cat.cfg 持久化（A224；空=默认组；自由文本）</summary>
            public string Type;

            /// <summary>运行态——true=HttpHost 监听中</summary>
            public bool Running;

            /// <summary>监听端口——静默态 0</summary>
            public int Port;

            /// <summary>会话实体——PumpSessions 轮转推进</summary>
            public ChatSession Session;

            /// <summary>HTTP 外观层——启动态非空</summary>
            public HttpHost Host;


            /// <summary>LLM API 配置身份——cat.cfg 持久化；缺省回退内置 DeepSeek（M1b）</summary>
            public Guid ApiConfigId;

            /// <summary>API 配置解析副本——M1c 每猫 Runtime 构造消费</summary>
            public CH_LlmApiConfig ApiConfig;

            /// <summary>角色段——cat.cfg 持久化（M2b；空=无角色段）</summary>
            public string Persona;

            /// <summary>工具名单原始串——cat.cfg 持久化（M2c；空=全量保底）</summary>
            public string ToolNames;

            /// <summary>前文注入清单——cat.cfg 持久化（M2d；空=不注入）</summary>
            public string[] InjectList;

            /// <summary>加载包挂载清单——cat.cfg 持久化（R4；空=未挂载任何包）</summary>
            public string[] Packs;

            /// <summary>qqbot 配置身份——cat.cfg 持久化（R2.3；Guid.Empty=未绑定）</summary>
            public Guid QqBotId;

            /// <summary>qqbot 启用标志——cat.cfg 持久化（R2.3；false=不注入不转发）</summary>
            public bool QqBotEnable;

            /// <summary>人工输入空闲注入开关——cat.cfg 持久化（A219-3；true=人工输入仅在 Idle 时推送）</summary>
            public bool UserInjectIdleOnly;

            /// <summary>思考强度覆盖——cat.cfg 持久化（A234；空=跟随全局 llm.reasoning_effort；low/high/max）</summary>
            public string ReasoningEffort;

            /// <summary>外观配置身份——cat.cfg 持久化（A244；Guid.Empty=跟随默认外观）</summary>
            public Guid AppearanceId;

            /// <summary>轮末总结生效值——经外观配置解析（A244 迁入池；不落 cat.cfg，此处只作解析结果缓存）</summary>
            public bool RoundSummary;

            /// <summary>工具声明面——M2c 裁剪后（session.new 重注入复用）</summary>
            public ToolSpec[] ToolSpecs;

        }

        /// <summary>
        /// cat.cfg 数据形态——sessions/&lt;id&gt;/cat.cfg（防御式读写；原子写落盘）。
        /// </summary>
        internal sealed class CatCfgData
        {
            /// <summary>会话 ID</summary>
            public string Id { get; set; }

            /// <summary>显示名</summary>
            public string DisplayName { get; set; }

            /// <summary>会话 Type 标签——前端按标签分区排布 Cat Card（A224；空=默认组；自由文本）</summary>
            public string Type { get; set; }

            /// <summary>运行态——启动扫描拉起依据</summary>
            public bool Running { get; set; }

            /// <summary>监听端口——静默态 0</summary>
            public int Port { get; set; }

            /// <summary>LLM API 配置身份——缺省空串（回退内置 DeepSeek）</summary>
            public string ApiConfigId { get; set; }

            /// <summary>角色段——system prompt 注入后追加（M2b；空=无角色段）</summary>
            public string Persona { get; set; }

            /// <summary>工具名单——逗号清单（*=全部/空=全量保底；读时比对内置清单过滤非法名）</summary>
            public string ToolNames { get; set; }

            /// <summary>前文注入清单——文件寻址数组（id:相对路径；空=不注入）</summary>
            public string[] InjectList { get; set; }

            /// <summary>qqbot 配置身份——缺省空串（未绑定）</summary>
            public string QqBotId { get; set; }

            /// <summary>qqbot 启用标志——缺省 false</summary>
            public bool QqBotEnable { get; set; }

            /// <summary>人工输入空闲注入开关——缺省 false（=现状：人工输入可轮内插入）</summary>
            public bool UserInjectIdleOnly { get; set; }

            /// <summary>思考强度覆盖——空=跟随全局（llm.reasoning_effort）；low/high/max</summary>
            public string ReasoningEffort { get; set; }

            /// <summary>外观配置身份——缺省空串（跟随默认外观）</summary>
            public string AppearanceId { get; set; }

            /// <summary>启用根 id 清单——全局根池子集（null/空=全量；workspace 强制必选不可取消）</summary>
            public string[] EnabledRoots { get; set; }

            /// <summary>加载包挂载清单——packs.json 池内 key 子集（空=未挂载任何包；pack 工具按此授权）</summary>
            public string[] Packs { get; set; }

            /// <summary>库白名单——库 ID 清单（A266：首版只落字段与勾选面，工具面校验后置）</summary>
            public string[] DbLibs { get; set; }
        }

        /// <summary>
        /// 新猫默认模板数据形态——Data/config/cat-default.cfg（全局配置：基础角色段 + 新猫三字段默认值）。
        /// 独立于基座配置群——LLM/猫域配置不混入基座（莎拍板 2026-08-25）。
        /// </summary>
        internal sealed class CatDefaultCfgData
        {
            /// <summary>基础角色段——所有猫系统提示词首行（空=无基础角色行；模板缺失回退内置文案）</summary>
            public string BaseRole { get; set; }

            /// <summary>新猫默认 persona（空=无角色段）</summary>
            public string DefaultPersona { get; set; }

            /// <summary>新猫默认工具名单（空=全量保底）</summary>
            public string DefaultToolNames { get; set; }

            /// <summary>新猫默认加载包挂载清单——packs.json 池内 key 子集（空=不挂载）</summary>
            public string[] DefaultPacks { get; set; }

            /// <summary>新猫默认前文注入清单（完整路径数组；空=不注入）</summary>
            public string[] DefaultInjectList { get; set; }

            /// <summary>新猫默认人工输入空闲注入开关（缺省 false）</summary>
            public bool DefaultUserInjectIdleOnly { get; set; }
        }

        /// <summary>内置基础角色段——模板缺失时回退（行为不倒退）</summary>
        internal const string FallbackBaseRole = "你是 MajorDomoCat——CH4 自举宿主的管理员对话中枢（P8.5 会话配置化）。";

        /// <summary>
        /// 全局默认模板路径——Data/config/cat-default.cfg。
        /// </summary>
        /// <returns>模板文件绝对路径</returns>
        private static string GetCatDefaultPath()
        {
            return Path.Combine(_dataRoot, "Data", "config", "cat-default.cfg");
        }

        /// <summary>
        /// 全局默认模板读取——防御式解析（损坏/不存在返回 null）。
        /// </summary>
        /// <returns>模板数据；不存在/损坏 null</returns>
        internal static CatDefaultCfgData LoadCatDefaultCfg()
        {
            try
            {
                string path = GetCatDefaultPath();
                if (!File.Exists(path))
                {
                    return null;
                }
                string json = File.ReadAllText(path);
                using (JsonDocument doc = JsonUtil.ParseStrict(json))
                {
                    JsonElement root = doc.RootElement;
                    CatDefaultCfgData data = new CatDefaultCfgData();
                    data.BaseRole = GetStringProp(root, "baseRole");
                    data.DefaultPersona = GetStringProp(root, "defaultPersona");
                    data.DefaultToolNames = GetStringProp(root, "defaultToolNames");
                    data.DefaultInjectList = GetStringArrayProp(root, "defaultInjectList");
                    data.DefaultPacks = GetStringArrayProp(root, "defaultPacks");
                    data.DefaultUserInjectIdleOnly = GetBoolProp(root, "defaultUserInjectIdleOnly");
                    return data;
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "全局默认模板读取失败（按不存在处理）: " + ex.Message, "CONFIG");
                return null;
            }
        }

        /// <summary>
        /// 全局默认模板原子写——ConfigStore.AtomicWrite（与 cat.cfg 同源）。
        /// </summary>
        /// <param name="data">模板数据</param>
        internal static void SaveCatDefaultCfg(CatDefaultCfgData data)
        {
            var payload = new
            {
                baseRole = data.BaseRole,
                defaultPersona = data.DefaultPersona,
                defaultToolNames = ValidateToolNames(data.DefaultToolNames),
                defaultInjectList = data.DefaultInjectList,
                defaultPacks = data.DefaultPacks,
                defaultUserInjectIdleOnly = data.DefaultUserInjectIdleOnly
            };
            try
            {
                ConfigStore.AtomicWrite(GetCatDefaultPath(), JsonUtil.Serialize(payload));
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "全局默认模板写入失败：" + ex.Message, "CONFIG");
            }
        }

        /// <summary>
        /// 注入路径合法性校验——只接受完整路径：绝对路径（盘符/UNC）或 id: 命名空间（id 为安全标识符）。
        /// 相对路径/空 id/非法字符 → false（不录入——莎拍板 2026-08-25）。
        /// </summary>
        /// <param name="path">候选路径</param>
        /// <returns>合法 true</returns>
        internal static bool IsValidInjectPath(string path)
        {
            if (path == null || path.Length == 0)
            {
                return false;
            }
            if (Path.IsPathRooted(path))
            {
                return true;
            }
            int colon = path.IndexOf(':');
            if (colon <= 0 || colon >= path.Length - 1)
            {
                return false;
            }
            string id = path.Substring(0, colon);
            for (int i = 0; i < id.Length; i++)
            {
                char c = id[i];
                bool ok = (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_';
                if (!ok)
                {
                    return false;
                }
            }
            return true;
        }

        /// <summary>
        /// cat.cfg 读取——防御式解析（损坏/缺字段回退默认；不存在返回 null）。
        /// </summary>
        /// <param name="path">cfg 路径</param>
        /// <returns>配置数据；损坏 null</returns>
        internal static CatCfgData LoadCatCfg(string path)
        {
            try
            {
                string json = File.ReadAllText(path);
                using (JsonDocument doc = JsonUtil.ParseStrict(json))
                {
                    JsonElement root = doc.RootElement;
                    CatCfgData data = new CatCfgData();
                    data.Id = GetStringProp(root, "id");
                    data.DisplayName = GetStringProp(root, "displayName");
                    data.Type = GetStringProp(root, "type");
                    data.Running = GetBoolProp(root, "running");
                    data.Port = GetIntProp(root, "port");
                    data.ApiConfigId = GetStringProp(root, "apiConfigId");
                    data.Persona = GetStringProp(root, "persona");
                    data.ToolNames = GetStringProp(root, "toolNames");
                    data.InjectList = GetStringArrayProp(root, "injectList");
                    data.QqBotId = GetStringProp(root, "qqbotId");
                    data.QqBotEnable = GetBoolProp(root, "qqbotEnable");
                    data.EnabledRoots = GetStringArrayProp(root, "enabledRoots");
                    data.Packs = GetStringArrayProp(root, "packs");
                    data.DbLibs = GetStringArrayProp(root, "dbLibs");
                    data.UserInjectIdleOnly = GetBoolProp(root, "userInjectIdleOnly");
                    data.ReasoningEffort = GetStringProp(root, "reasoningEffort");
                    data.AppearanceId = GetStringProp(root, "appearanceId");
                    return data;
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "猫配置读取失败（按损坏处理）: " + ex.Message, "CONFIG");
                return null;
            }
        }

        /// <summary>
        /// cat.cfg 原子写——用户配置落盘（运行态 running/port 不落此文件；见 AdminService.CatRuntime.cs）。
        /// </summary>
        /// <param name="cat">猫实体</param>
        private static void SaveCatCfg(CatEntry cat)
        {
            CatCfgData data = new CatCfgData();
            data.Id = cat.Id;
            data.DisplayName = cat.DisplayName;
            data.Type = cat.Type;
            data.ApiConfigId = cat.ApiConfigId.ToString("D");
            data.Persona = cat.Persona;
            data.ToolNames = cat.ToolNames;
            data.InjectList = cat.InjectList;
            data.Packs = cat.Packs;
            data.QqBotId = cat.QqBotId.ToString("D");
            data.QqBotEnable = cat.QqBotEnable;
            data.UserInjectIdleOnly = cat.UserInjectIdleOnly;
            data.ReasoningEffort = cat.ReasoningEffort;
            data.AppearanceId = cat.AppearanceId.ToString("D");
            SaveCatCfgData(cat.Id, data);
        }

        /// <summary>timeback 归档目录——Data/runtime/timeback（全局：count.json 全局计数 + 每次回收一个作用域文件；design-ch4-timeback §5.1）。接线：ChatSession.TimebackArchiveDirProvider（Core 零配置面依赖）。</summary>
        /// <param name="catKey">猫 key</param>
        /// <returns>归档文件路径（猫 key 空 = 空串，调用方不落档）</returns>
        public static string ResolveTimebackArchiveDir(string catKey)
        {
            string id = catKey == null ? "" : catKey;
            if (id.Length == 0)
            {
                return "";
            }
            return Path.Combine(_dataRoot, "Data", "runtime", "timeback");
        }

        /// <summary>会话元数据文件路径——Data/sessions/&lt;id&gt;/&lt;id&gt;.session.json（A201 · design-ch4-protocol §十三）。接线：ChatSession.SessionMetaPathProvider（Core 零配置面依赖）。</summary>
        /// <param name="catKey">猫 key</param>
        /// <returns>元数据文件绝对路径（猫 key 空 = 空串，调用方不落盘）</returns>
        public static string ResolveSessionMetaPath(string catKey)
        {
            string id = catKey == null ? "" : catKey;
            if (id.Length == 0)
            {
                return "";
            }
            return Path.Combine(_dataRoot, "Data", "sessions", id, id + ".session.json");
        }

        /// <summary>猫显示名现值——cat.cfg 的 displayName（A201：session.new 时刷新会话显示名）。接线：ChatSession.DisplayNameProvider。</summary>
        /// <param name="catKey">猫 key</param>
        /// <returns>显示名（猫 key 空 / 配置缺失 = 空串，调用方保持原值）</returns>
        public static string ResolveCatDisplayName(string catKey)
        {
            string id = catKey == null ? "" : catKey;
            if (id.Length == 0)
            {
                return "";
            }
            CatCfgData cfg = LoadCatCfg(Path.Combine(_dataRoot, "Data", "sessions", id, "cat.cfg"));
            if (cfg == null || cfg.DisplayName == null)
            {
                return "";
            }
            return cfg.DisplayName;
        }

        /// <summary>
        /// 删除猫文件——sessions/&lt;id&gt;/ 目录（含前文 jsonl；异常不阻断删除流程）。
        /// </summary>
        /// <param name="id">会话 ID</param>
        private static void DeleteCatFiles(string id)
        {
            try
            {
                string dir = Path.Combine(_dataRoot, "Data", "sessions", id);
                if (Directory.Exists(dir))
                {
                    Directory.Delete(dir, true);
                }
                string storePath = Path.Combine(_dataRoot, "Data", "sessions", id, id + ".jsonl");
                if (File.Exists(storePath))
                {
                    File.Delete(storePath);
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "销毁猫文件清理异常：" + ex.Message, "CHAT");
            }
        }

        /// <summary>
        /// 读取 JSON 对象布尔属性——防御式（缺字段返回 false）。
        /// </summary>
        /// <param name="obj">JSON 对象</param>
        /// <param name="prop">属性名</param>
        /// <returns>属性值</returns>
        internal static bool GetBoolProp(JsonElement obj, string prop)
        {
            JsonElement value;
            if (obj.TryGetProperty(prop, out value) && value.ValueKind == JsonValueKind.True)
            {
                return true;
            }
            return false;
        }

        /// <summary>
        /// 读取 JSON 对象整数属性——防御式（缺字段/非整数返回 0）。
        /// </summary>
        /// <param name="obj">JSON 对象</param>
        /// <param name="prop">属性名</param>
        /// <returns>属性值</returns>
        private static int GetIntProp(JsonElement obj, string prop)
        {
            JsonElement value;
            if (obj.TryGetProperty(prop, out value) && value.ValueKind == JsonValueKind.Number)
            {
                int n;
                if (value.TryGetInt32(out n))
                {
                    return n;
                }
            }
            return 0;
        }

        /// <summary>
        /// 读取 JSON 对象字符串数组属性——防御式（缺字段/非数组返回空数组）。
        /// </summary>
        /// <param name="obj">JSON 对象</param>
        /// <param name="prop">属性名</param>
        /// <returns>字符串数组；缺字段空数组</returns>
        internal static string[] GetStringArrayProp(JsonElement obj, string prop)
        {
            JsonElement value;
            if (obj.TryGetProperty(prop, out value) && value.ValueKind == JsonValueKind.Array)
            {
                List<string> list = new List<string>();
                for (int i = 0; i < value.GetArrayLength(); i++)
                {
                    JsonElement item = value[i];
                    if (item.ValueKind == JsonValueKind.String)
                    {
                        string got = item.GetString();
                        if (got != null && got.Length > 0)
                        {
                            list.Add(got);
                        }
                    }
                }
                return list.ToArray();
            }
            return new string[0];
        }

        /// <summary>
        /// cat.cfg 原子写——按配置数据落盘（M3 端点写面；与 SaveCatCfg(CatEntry) 同源；A285 自 Handlers 迁入）。
        /// </summary>
        /// <param name="id">会话 ID（majordomo=默认猫）</param>
        /// <param name="data">配置数据（用户配置面——运行态 running/port 不在此落盘）</param>
        internal static void SaveCatCfgData(string id, CatCfgData data)
        {
            string dir = Path.Combine(_dataRoot, "Data", "sessions", id);
            string path = Path.Combine(dir, "cat.cfg");
            var payload = new
            {
                id = data.Id,
                displayName = data.DisplayName,
                type = data.Type,
                apiConfigId = data.ApiConfigId,
                persona = data.Persona,
                // M2c 写时校验——序列化前比对内置清单过滤非法名（外部损坏防御：持久化面只落合法名）
                toolNames = ValidateToolNames(data.ToolNames),
                injectList = data.InjectList,
                qqbotId = data.QqBotId,
                qqbotEnable = data.QqBotEnable,
                enabledRoots = data.EnabledRoots,
                packs = data.Packs,
                dbLibs = data.DbLibs,
                userInjectIdleOnly = data.UserInjectIdleOnly,
                appearanceId = data.AppearanceId,
                reasoningEffort = data.ReasoningEffort
            };
            try
            {
                ConfigStore.AtomicWrite(path, JsonUtil.Serialize(payload));
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "cat.cfg 写入失败：" + ex.Message, "CHAT");
            }
        }

        /// <summary>
        /// catcfg.apply 执行体——主线程泵消费：重读 cat.cfg → 更新注册表字段 + 重裁剪声明面 + apiConfigId 变更 SwapLlmRuntime（A285 自 Handlers 迁入）。
        /// </summary>
        /// <param name="key">猫寻址键（majordomo=默认猫）</param>
        /// <returns>结果文本</returns>
        internal static string HandleCatCfgApply(string key)
        {
            CatCfgData cfg = LoadCatCfg(Path.Combine(_dataRoot, "Data", "sessions", key, "cat.cfg"));
            if (cfg == null)
            {
                return "catcfg.apply | cat.cfg 不存在: " + key;
            }
            string persona = "";
            if (cfg.Persona != null)
            {
                persona = cfg.Persona;
            }
            string[] injectList = cfg.InjectList;
            if (injectList == null)
            {
                injectList = new string[0];
            }
            string toolNames = "";
            if (cfg.ToolNames != null)
            {
                toolNames = cfg.ToolNames;
            }
            bool includePrivileged = IsDefaultCatKey(key);
            ToolSpec[] specs = FilterToolSpecs(ResolveToolNames(toolNames, includePrivileged), includePrivileged);
            // M4e 猫级白名单——配置变更后重建猫文件系统（启用根子集）
            ApplyCatRoots(key);
            if (key == "majordomo")
            {
                // 默认猫——静态面更新（session.new 重注入消费）
                _chatBridge.DefaultPersona = persona;
                _chatBridge.DefaultInjectList = injectList;
                _chatBridge.DefaultToolSpecs = specs;
                // 授权面原始串——实时解析输入（改配置即改内存；design-ch4-tools §三·十一）
                _chatBridge.DefaultToolNames = toolNames;
                _chatBridge.DefaultQqBotId = ResolveQqBotId(cfg);
                _chatBridge.DefaultQqBotEnable = cfg.QqBotEnable;
                _chatBridge.DefaultSession.SetUserInjectIdleOnly(cfg.UserInjectIdleOnly);
                _chatBridge.DefaultSession.SetRoundSummary(ResolveEffectiveRoundSummary("majordomo"));
                _chatBridge.DefaultSession.SetPetKind(ResolveEffectivePetKind("majordomo"));
                // A230 显示名立即刷新（改配置即改内存；空值保持原值）
                if (cfg.DisplayName != null && cfg.DisplayName.Length > 0)
                {
                    _chatBridge.DefaultSession.SetDisplayName(cfg.DisplayName);
                }
                Guid newApi = ResolveApiConfigId(cfg);
                string newEffort = NormalizeReasoningEffort(cfg.ReasoningEffort);
                if (_defaultApiConfigId != newApi || _defaultReasoningEffort != newEffort)
                {
                    _defaultApiConfigId = newApi;
                    _defaultReasoningEffort = newEffort;
                    _chatBridge.DefaultSession.SwapLlmRuntime(new DeepSeekLlmRuntime(_apiStore, newApi, _globalConfig, newEffort));
                    LogStore.Add("CatHome4", 1, "majordomo 配置生效：LLM 端点 " + newApi.ToString("D") + "，思考强度 " + (newEffort.Length > 0 ? newEffort : "跟随全局"), "CONFIG");
                }
                return "catcfg.apply | majordomo | 已生效（前文项新会话生效）";
            }
            CatEntry cat = FindCat(key);
            if (cat == null)
            {
                return "catcfg.apply | 猫不存在: " + key;
            }
            cat.Persona = persona;
            // A230 显示名立即刷新（改配置即改内存；空值保持原值）
            if (cfg.DisplayName != null && cfg.DisplayName.Length > 0)
            {
                cat.DisplayName = cfg.DisplayName;
                cat.Session.SetDisplayName(cfg.DisplayName);
            }
            cat.Type = cfg.Type == null ? "" : cfg.Type;
            cat.InjectList = injectList;
            cat.ToolNames = toolNames;
            cat.ToolSpecs = specs;
            cat.QqBotId = ResolveQqBotId(cfg);
            cat.QqBotEnable = cfg.QqBotEnable;
            cat.UserInjectIdleOnly = cfg.UserInjectIdleOnly;
            cat.Session.SetUserInjectIdleOnly(cfg.UserInjectIdleOnly);
            cat.AppearanceId = ResolveAppearanceId(cfg);
            cat.RoundSummary = ResolveEffectiveRoundSummary(key);
            cat.Session.SetRoundSummary(cat.RoundSummary);
            cat.Session.SetPetKind(ResolveEffectivePetKind(key));
            // M4e 猫级白名单——运行时猫文件系统已在上方 ApplyCatRoots 重建（含本分支）
            Guid newApiId = ResolveApiConfigId(cfg);
            string newCatEffort = NormalizeReasoningEffort(cfg.ReasoningEffort);
            if (cat.ApiConfigId != newApiId || cat.ReasoningEffort != newCatEffort)
            {
                cat.ApiConfigId = newApiId;
                cat.ReasoningEffort = newCatEffort;
                CH_LlmApiConfig apiConfig = new CH_LlmApiConfig();
                if (_apiStore != null)
                {
                    _apiStore.TryGet(newApiId, out apiConfig);
                }
                cat.ApiConfig = apiConfig;
                cat.Session.SwapLlmRuntime(new DeepSeekLlmRuntime(_apiStore, newApiId, _globalConfig, newCatEffort));
                LogStore.Add("CatHome4", 1, "猫「" + cat.DisplayName + "」配置生效：LLM 端点 " + newApiId.ToString("D") + "，思考强度 " + (newCatEffort.Length > 0 ? newCatEffort : "跟随全局"), "CONFIG");
            }
            return "catcfg.apply | " + cat.DisplayName + " | 已生效（前文项新会话生效）";
        }

        /// <summary>
        /// 解析 cat.cfg 的 qqbot 配置身份——缺省 Guid.Empty=未绑定（A285 自 Handlers 迁入）。
        /// </summary>
        /// <param name="cfg">配置数据</param>
        /// <returns>qqbot 配置身份</returns>
        private static Guid ResolveQqBotId(CatCfgData cfg)
        {
            Guid id = Guid.Empty;
            if (cfg != null && cfg.QqBotId != null && cfg.QqBotId.Length > 0)
            {
                Guid parsed;
                if (Guid.TryParse(cfg.QqBotId, out parsed) && parsed != Guid.Empty)
                {
                    id = parsed;
                }
            }
            return id;
        }

        /// <summary>
        /// 解析 cat.cfg 的 API 配置身份——缺省 Guid.Empty=默认端点语义（Cat 可选配置；未配置走默认端点；A285 自 Handlers 迁入）。
        /// </summary>
        /// <param name="cfg">配置数据</param>
        /// <returns>API 配置身份</returns>
        internal static Guid ResolveApiConfigId(CatCfgData cfg)
        {
            Guid id = Guid.Empty;
            if (cfg != null && cfg.ApiConfigId != null && cfg.ApiConfigId.Length > 0)
            {
                Guid parsed;
                if (Guid.TryParse(cfg.ApiConfigId, out parsed) && parsed != Guid.Empty)
                {
                    id = parsed;
                }
            }
            return id;
        }

        /// <summary>
        /// majordomo 默认猫 cat.cfg 补建——缺失时按全局默认模板创建（A285 自 Handlers 迁入）。
        /// 已存在不覆盖——尊重用户已保存配置；模板缺失回退空默认（与运行时缺省回退语义一致）。
        /// </summary>
        internal static void EnsureMajordomoCfg()
        {
            string path = Path.Combine(_dataRoot, "Data", "sessions", "majordomo", "cat.cfg");
            if (File.Exists(path))
            {
                return;
            }

            CatDefaultCfgData tpl = LoadCatDefaultCfg();
            CatCfgData data = new CatCfgData();
            data.Id = "majordomo";
            data.DisplayName = "majordomo";
            data.Running = false;
            data.Port = 0;
            data.ApiConfigId = "";
            data.QqBotId = "";
            data.QqBotEnable = false;
            data.EnabledRoots = null;
            if (tpl != null)
            {
                data.Persona = tpl.DefaultPersona != null ? tpl.DefaultPersona : "";
                data.ToolNames = tpl.DefaultToolNames != null ? tpl.DefaultToolNames : "";
                data.InjectList = tpl.DefaultInjectList != null ? tpl.DefaultInjectList : new string[0];
            }
            else
            {
                data.Persona = "";
                data.ToolNames = "";
                data.InjectList = new string[0];
            }

            if (tpl != null)
            {
                data.UserInjectIdleOnly = tpl.DefaultUserInjectIdleOnly;
            }
            SaveCatCfgData("majordomo", data);
            LogStore.Add("CatHome4", 1, "majordomo cat.cfg 缺失——已按默认模板补建", "CONFIG");
        }
    }
}
