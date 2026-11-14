using System;
using System.Collections.Generic;
using System.Text.Json;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// 工具定义条目——统一工具池的原子形态（design-ch4-tools-pool）。
    /// 数据源：Flow.GetToolsJson() 自曝（tools.&lt;组&gt; 积木）或内置定义源——同一 JSON 格式，同一解析器。
    /// </summary>
    public sealed class ToolDef
    {
        /// <summary>工具名（text-read 等——唯一键）</summary>
        public string Name = "";

        /// <summary>语义化描述（LLM 面——OpenAI function description）</summary>
        public string Description = "";

        /// <summary>参数 JSON Schema（OpenAI 原样透传；空 = 无参工具）</summary>
        public string ParametersJson = "";

        /// <summary>归属组（TextCat 等；内置 = ""）</summary>
        public string Group = "";
        /// <summary>组级特权声明——true=本组工具仅主干会话（catKey=majordomo）可见可调（工具定义 JSON 根级 privileged 字段；缺省 false）</summary>
        public bool Privileged = false;
    }

    /// <summary>
    /// 工具组定义缺陷条目——工具定义自曝链路的失败记录（A107；design-ch4-tools §三·十三）。
    /// 随池快照原子换代：reload 成功即清零，不留陈旧残留。
    /// </summary>
    public sealed class ToolDefect
    {
        /// <summary>组名（组 Flow 名 / 内置源）</summary>
        public string Group = "";

        /// <summary>缺陷阶段——throw（自曝抛异常）/ parse（JSON 非法）/ shape（结构不符）/ empty（已加载但零工具入池）</summary>
        public string Stage = "";

        /// <summary>原因——异常消息 / 结构说明（日志与 info 直读，不再二次解释）</summary>
        public string Reason = "";
    }

    /// <summary>
    /// 工具组定义来源——组名 + Flow 实例（A107：原 RebuildAll 只收 IFlow，失败时无从报出是哪一组）。
    /// </summary>
    public sealed class ToolGroupSource
    {
        /// <summary>组名（组 Flow 名——dll 名派生）</summary>
        public string Name = "";

        /// <summary>已加载 Flow 实例（GetToolsJson 自曝源）</summary>
        public IFlow Flow;
    }

    /// <summary>
    /// 统一工具池——工具定义唯一真相源（消费面统一从此派生，不各自实现）。
    /// 聚合路径：Flow 自曝 GetToolsJson（工具组）+ 内置定义源（宿主内建——本质也是 BRIK，只是内置）。
    /// 消费面：ToolRegistry.Init（specs + ownerFlowMap）/ 配置界面名称/组别 / 快照 tools 段。
    /// </summary>
    public static class ToolPool
    {
        /// <summary>
        /// 池快照——三索引一次成型（写侧局部构建 → 整体换引用；读侧取局部引用即稳定视图，无半更新可见）。
        /// </summary>
        private sealed class PoolState
        {
            /// <summary>池条目——按名索引（重名覆盖：后注册优先）</summary>
            public Dictionary<string, ToolDef> ByName = new Dictionary<string, ToolDef>(StringComparer.Ordinal);

            /// <summary>池条目——插入序（All/BuildSpecs 确定性：注册序 = 扫描添加序）</summary>
            public List<ToolDef> Order = new List<ToolDef>();

            /// <summary>池条目——按组索引（配置界面组别/名称自动生成）</summary>
            public Dictionary<string, List<ToolDef>> ByGroup = new Dictionary<string, List<ToolDef>>(StringComparer.Ordinal);

            /// <summary>缺陷清单——工具定义自曝失败记录（A107；与池同代，换代即清零）</summary>
            public List<ToolDefect> Defects = new List<ToolDefect>();

            /// <summary>已产出定义的组名——成功取到 tools 数组即计入（显式空组声明亦计；empty 缺陷判定基准）</summary>
            public List<string> ProducedGroups = new List<string>();
        }

        /// <summary>当前池快照——唯一可变引用，写侧整体替换（RebuildAll）；读侧先取局部引用再遍历</summary>
        private static PoolState _state = new PoolState();

        /// <summary>
        /// 解析工具定义 JSON——{"group":"X","tools":[{name,description,parameters}]}（与 BRIK GetToolsJson 返回值同构）。
        /// </summary>
        /// <param name="json">工具定义 JSON 文本</param>
        /// <param name="groupHint">组名提示（组 Flow 名 / 内置源——失败归因用；A107）</param>
        private static void AddFromJson(PoolState target, string json, string groupHint)
        {
            // A107——四条静默路径全部出声（原为空串 return / 缺 tools return / 异常空 catch 吞掉——整组工具无声消失）
            string groupName = groupHint == null ? "" : groupHint;
            if (json == null || json.Length == 0)
            {
                target.Defects.Add(BuildDefect(groupName, "shape", "工具定义 JSON 为空"));
                return;
            }
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(json))
                {
                    JsonElement root = doc.RootElement;
                    string group = "";
                    JsonElement groupEl;
                    if (root.TryGetProperty("group", out groupEl) && groupEl.ValueKind == JsonValueKind.String)
                    {
                        group = groupEl.GetString() ?? "";
                    }
                    // 组级特权声明——根级 privileged=true → 本组全部工具标记特权（缺省 false）
                    bool privileged = false;
                    JsonElement privEl;
                    if (root.TryGetProperty("privileged", out privEl) && privEl.ValueKind == JsonValueKind.True)
                    {
                        privileged = true;
                    }
                    JsonElement tools;
                    if (!root.TryGetProperty("tools", out tools) || tools.ValueKind != JsonValueKind.Array)
                    {
                        target.Defects.Add(BuildDefect(groupName, "shape", "工具定义缺 tools 数组"));
                        return;
                    }
                    // 已产出登记——取到 tools 数组即算定义到位（显式空组声明 tools:[] 合法，不判缺陷）
                    if (!target.ProducedGroups.Contains(groupName))
                    {
                        target.ProducedGroups.Add(groupName);
                    }
                    int accepted = 0;
                    int platformSkipped = 0;
                    for (int i = 0; i < tools.GetArrayLength(); i = i + 1)
                    {
                        JsonElement item = tools[i];
                        if (item.ValueKind != JsonValueKind.Object)
                        {
                            continue;
                        }
                        // A359 平台过滤——工具声明 platforms 字段（缺省 = 全平台）；不匹配则不入池（design-ch4-platform §五 暴露面单点）
                        if (IsPlatformAllowed(item) == false)
                        {
                            LogStore.Add("ToolPool", 1, "工具按平台过滤（不入池）：" + group + " | " + CurrentPlatformName(), "SYS");
                            platformSkipped = platformSkipped + 1;
                            continue;
                        }
                        ToolDef def = new ToolDef();
                        def.Group = group;
                        def.Privileged = privileged;
                        JsonElement nameEl;
                        if (item.TryGetProperty("name", out nameEl) && nameEl.ValueKind == JsonValueKind.String)
                        {
                            def.Name = nameEl.GetString() ?? "";
                        }
                        JsonElement descEl;
                        if (item.TryGetProperty("description", out descEl) && descEl.ValueKind == JsonValueKind.String)
                        {
                            def.Description = descEl.GetString() ?? "";
                        }
                        JsonElement paramsEl;
                        if (item.TryGetProperty("parameters", out paramsEl))
                        {
                            def.ParametersJson = paramsEl.GetRawText();
                        }
                        if (def.Name.Length == 0)
                        {
                            continue;
                        }
                        AddTo(target, def);
                        accepted = accepted + 1;
                    }
                    // 有声明却零入池——与合法空组区分（空数组走上面的已产出登记，不在此列；平台过滤跳过的不算缺陷）
                    if (tools.GetArrayLength() > 0 && accepted == 0 && platformSkipped == 0)
                    {
                        target.Defects.Add(BuildDefect(groupName, "shape", "工具条目全部无效——" + tools.GetArrayLength().ToString() + " 条均缺 name 或非对象"));
                    }
                }
            }
            catch (Exception ex)
            {
                // 解析失败——整组工具定义缺失（A107：原为静默跳过，池里查无此组）
                target.Defects.Add(BuildDefect(groupName, "parse", "工具定义 JSON 非法: " + ex.Message));
            }
        }

        /// <summary>
        /// 添加单条目——重名覆盖 + 组索引同步。
        /// </summary>
        /// <param name="def">工具定义</param>
        private static void AddTo(PoolState target, ToolDef def)
        {
            if (def == null || def.Name.Length == 0)
            {
                return;
            }
            // 重名覆盖——保持原插入序（确定性：注册序不因覆盖漂移）；内容跟随最新注册
            bool isNew = !target.ByName.ContainsKey(def.Name);
            target.ByName[def.Name] = def;
            if (isNew)
            {
                target.Order.Add(def);
            }
            else
            {
                for (int i = 0; i < target.Order.Count; i = i + 1)
                {
                    if (target.Order[i].Name == def.Name)
                    {
                        target.Order[i] = def;
                        break;
                    }
                }
            }
            string group = "";
            if (def.Group.Length > 0)
            {
                group = def.Group;
            }
            List<ToolDef> list;
            if (!target.ByGroup.TryGetValue(group, out list))
            {
                list = new List<ToolDef>();
                target.ByGroup[group] = list;
            }
            int exists = -1;
            for (int i = 0; i < list.Count; i = i + 1)
            {
                if (list[i].Name == def.Name)
                {
                    exists = i;
                    break;
                }
            }
            if (exists >= 0)
            {
                list[exists] = def;
            }
            else
            {
                list.Add(def);
            }
        }

        /// <summary>
        /// 全量工具定义——声明面/白名单比对基准（注册序 = 添加序）。
        /// </summary>
        /// <returns>工具定义数组</returns>
        public static ToolDef[] All()
        {
            // 读侧局部快照——写侧换引用不影响本次读取；注册序兑现（按插入序返回，不依赖 Dictionary 枚举序）
            PoolState snapshot = _state;
            ToolDef[] result = new ToolDef[snapshot.Order.Count];
            for (int i = 0; i < snapshot.Order.Count; i = i + 1)
            {
                result[i] = snapshot.Order[i];
            }
            return result;
        }

        /// <summary>
        /// 全量工具名——配置界面名称清单（M2c 比对基准）。
        /// </summary>
        /// <returns>工具名数组</returns>
        public static string[] AllNames()
        {
            ToolDef[] all = All();
            string[] names = new string[all.Length];
            for (int i = 0; i < all.Length; i = i + 1)
            {
                names[i] = all[i].Name;
            }
            return names;
        }

        /// <summary>
        /// 按组取工具——配置界面组别/名称自动生成（组空 = 内置）。
        /// </summary>
        /// <param name="group">组名（空 = 内置组）</param>
        /// <returns>组内工具定义数组（不存在空组 = 空数组）</returns>
        public static ToolDef[] ByGroup(string group)
        {
            // 读侧局部快照——写侧换引用不影响本次读取
            PoolState snapshot = _state;
            string key = group ?? "";
            List<ToolDef> list;
            if (snapshot.ByGroup.TryGetValue(key, out list))
            {
                return list.ToArray();
            }
            return new ToolDef[0];
        }

        /// <summary>
        /// 全部组名——配置界面分组展示（含内置空组；按首次出现序）。
        /// </summary>
        /// <returns>组名数组</returns>
        public static string[] AllGroups()
        {
            // 读侧局部快照——写侧换引用不影响本次读取；首次出现序兑现（按注册序摄取组名）
            PoolState snapshot = _state;
            List<string> groups = new List<string>();
            for (int i = 0; i < snapshot.Order.Count; i = i + 1)
            {
                string key = "";
                if (snapshot.Order[i].Group.Length > 0)
                {
                    key = snapshot.Order[i].Group;
                }
                if (!groups.Contains(key))
                {
                    groups.Add(key);
                }
            }
            return groups.ToArray();
        }
        /// <summary>
        /// 构建缺陷条目——写侧共用出口（A107）。
        /// </summary>
        /// <param name="group">组名（组 Flow 名 / 内置源）</param>
        /// <param name="stage">缺陷阶段（throw / parse / shape / empty）</param>
        /// <param name="reason">原因文本</param>
        /// <returns>缺陷条目</returns>
        private static ToolDefect BuildDefect(string group, string stage, string reason)
        {
            ToolDefect defect = new ToolDefect();
            defect.Group = group;
            defect.Stage = stage;
            defect.Reason = reason;
            return defect;
        }
        /// <summary>
        /// 工具定义缺陷清单——定义自曝失败可见面（A107：日志 / info.tools_defect / 配置面 / reload 报告四层同源）。
        /// 读侧局部快照——与池同代（reload 换代即清零）。
        /// </summary>
        /// <returns>缺陷条目数组（无缺陷 = 空数组）</returns>
        public static ToolDefect[] Defects()
        {
            PoolState snapshot = _state;
            return snapshot.Defects.ToArray();
        }
        /// <summary>
        /// 空产出兜底登记——组 Flow 已加载却零工具入池，且此前无更具体缺陷记录时记一条 empty（A107）。
        /// 合法空组声明（tools:[]）已在 AddFromJson 登记产出，不在此列。
        /// </summary>
        /// <param name="target">目标快照（构建期局部对象）</param>
        /// <param name="groupName">组名</param>
        private static void NoteEmptyIfSilent(PoolState target, string groupName)
        {
            string name = groupName == null ? "" : groupName;
            if (name.Length == 0)
            {
                return;
            }
            for (int i = 0; i < target.Defects.Count; i = i + 1)
            {
                if (target.Defects[i].Group == name)
                {
                    return;
                }
            }
            if (target.ProducedGroups.Contains(name))
            {
                return;
            }
            target.Defects.Add(BuildDefect(name, "empty", "组 Flow 已加载但工具定义零产出——池内无本组工具"));
        }

        /// <summary>
        /// 构建 ToolRegistry 数据——specs 数组（ToolSpec 投影）。
        /// </summary>
        /// <returns>工具规格数组</returns>
        public static ToolSpec[] BuildSpecs()
        {
            ToolDef[] all = All();
            ToolSpec[] specs = new ToolSpec[all.Length];
            for (int i = 0; i < all.Length; i = i + 1)
            {
                specs[i] = new ToolSpec(all[i].Name, all[i].Description, all[i].ParametersJson);
            }
            return specs;
        }

        /// <summary>
        /// 构建 OwnerFlow 映射——工具名 → 组名（ToolRegistry 路由表数据源）。
        /// </summary>
        /// <returns>工具名 → 组名映射</returns>
        public static Dictionary<string, string> BuildOwnerFlowMap()
        {
            // 读侧局部快照——写侧换引用不影响本次读取
            PoolState snapshot = _state;
            Dictionary<string, string> map = new Dictionary<string, string>(StringComparer.Ordinal);
            foreach (KeyValuePair<string, ToolDef> kv in snapshot.ByName)
            {
                map[kv.Key] = kv.Value.Group;
            }
            return map;
        }
        /// <summary>
        /// 构建特权标记映射——工具名 → 是否特权（ToolRegistry 可见性判定数据源；组级声明派生）。
        /// </summary>
        /// <returns>工具名 → 特权标记映射</returns>
        public static Dictionary<string, bool> BuildPrivilegedMap()
        {
            // 读侧局部快照——写侧换引用不影响本次读取
            PoolState snapshot = _state;
            Dictionary<string, bool> map = new Dictionary<string, bool>(StringComparer.Ordinal);
            foreach (KeyValuePair<string, ToolDef> kv in snapshot.ByName)
            {
                map[kv.Key] = kv.Value.Privileged;
            }
            return map;
        }
        /// <summary>
        /// 从 Flow 自曝工具定义加入目标快照——IFlow.GetToolsJson()（工具组 Flow 调用面；自曝失败跳过该组）。
        /// </summary>
        /// <param name="target">目标快照（构建期局部对象）</param>
        /// <param name="flow">已加载 Flow</param>
        /// <param name="flowName">组名（组 Flow 名——失败归因用；A107）</param>
        private static void AddFromFlowTo(PoolState target, IFlow flow, string flowName)
        {
            // A107——自曝调用面异常同样出声（组名由调用点带入——Flow 实例自身报不出是哪一组）
            string groupName = flowName == null ? "" : flowName;
            try
            {
                AddFromJson(target, flow.GetToolsJson(), groupName);
            }
            catch (Exception ex)
            {
                target.Defects.Add(BuildDefect(groupName, "throw", "工具定义自曝失败: " + ex.Message));
            }
        }
        /// <summary>
        /// 全量重建工具池——唯一写入口（Bootstrap / 热重载同源）：局部构建新快照 → 原子换引用。
        /// 读者要么看到全旧、要么看到全新（无半更新窗口）；不在新表的工具随旧快照淘汰（无陈旧残留）。
        /// </summary>
        /// <param name="toolGroups">工具组来源列表（组名 + Flow——A107：失败时能报出是哪一组）</param>
        /// <param name="builtinJson">内置工具定义 JSON（Note/time/random/info/host-* 等）</param>
        public static void RebuildAll(List<ToolGroupSource> toolGroups, string builtinJson)
        {
            // [段1] 局部构建新快照——旧快照只读，读者不受构建期影响
            PoolState next = new PoolState();
            if (toolGroups != null)
            {
                for (int i = 0; i < toolGroups.Count; i = i + 1)
                {
                    if (toolGroups[i] == null || toolGroups[i].Flow == null)
                    {
                        continue;
                    }
                    AddFromFlowTo(next, toolGroups[i].Flow, toolGroups[i].Name);
                }
            }
            AddFromJson(next, builtinJson, "内置");
            // [段2] 空产出兜底——组 Flow 已加载却零工具入池，且此前无更具体的缺陷记录（覆盖无异常路径：不报错也不产出）
            if (toolGroups != null)
            {
                for (int i = 0; i < toolGroups.Count; i = i + 1)
                {
                    if (toolGroups[i] == null)
                    {
                        continue;
                    }
                    NoteEmptyIfSilent(next, toolGroups[i].Name);
                }
            }
            // [段2b] order 注记注入——工具执行序表（A127）作为工具定义标准注释追加到描述尾行
            // （单点 = 此处即全池生效：12 组自曝 + 内置定义同池；改表随池重建）
            for (int i = 0; i < next.Order.Count; i = i + 1)
            {
                ToolDef def = next.Order[i];
                string note = ToolOrderTable.NoteLine(def.Name);
                if (def.Description.Length == 0)
                {
                    def.Description = note;
                }
                else
                {
                    def.Description = def.Description + "\n" + note;
                }
            }
            // [段3] 原子换引用——读者要么全旧要么全新；被删工具随旧快照淘汰
            _state = next;
        }
        /// <summary>
        /// 平台归属判定——工具定义项的 <c>platforms</c> 字段（字符串或数组）是否覆盖当前平台。
        /// <para>
        /// 缺省（无字段 / 非法形态）= 全平台放行；<c>any</c> = 全平台；其余按 <c>windows</c> / <c>linux</c> 精确匹配。
        /// 判据只此一处（design-ch4-platform §五「暴露面单点」）——不靠「少拷一个组 dll」压暴露面。
        /// </para>
        /// </summary>
        /// <param name="item">工具定义项</param>
        /// <returns>true=本平台可用</returns>
        private static bool IsPlatformAllowed(JsonElement item)
        {
            JsonElement platEl;
            if (item.TryGetProperty("platforms", out platEl) == false)
            {
                return true;
            }
            string current = CurrentPlatformName();
            if (platEl.ValueKind == JsonValueKind.String)
            {
                return MatchesPlatform(platEl.GetString(), current);
            }
            if (platEl.ValueKind != JsonValueKind.Array)
            {
                return true;
            }
            for (int i = 0; i < platEl.GetArrayLength(); i = i + 1)
            {
                JsonElement entry = platEl[i];
                if (entry.ValueKind != JsonValueKind.String)
                {
                    continue;
                }
                if (MatchesPlatform(entry.GetString(), current))
                {
                    return true;
                }
            }
            return false;
        }
        /// <summary>
        /// 单条平台声明匹配——空串与 <c>any</c> 视为全平台；其余与当前平台名精确比较（忽略大小写）。
        /// </summary>
        /// <param name="declared">声明值</param>
        /// <param name="current">当前平台名</param>
        /// <returns>true=匹配</returns>
        private static bool MatchesPlatform(string declared, string current)
        {
            if (declared == null || declared.Length == 0)
            {
                return true;
            }
            if (string.Equals(declared, "any", StringComparison.OrdinalIgnoreCase))
            {
                return true;
            }
            return string.Equals(declared, current, StringComparison.OrdinalIgnoreCase);
        }
        /// <summary>
        /// 当前平台名——<c>windows</c> / <c>linux</c>（与工具定义 <c>platforms</c> 取值同域）。
        /// </summary>
        /// <returns>平台名</returns>
        private static string CurrentPlatformName()
        {
            if (OperatingSystem.IsWindows())
            {
                return "windows";
            }
            return "linux";
        }
    }
}
