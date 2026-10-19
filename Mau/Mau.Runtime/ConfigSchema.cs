using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using System.Text.Json;

namespace Mau.Runtime
{
    /// <summary>
    /// 配置群 schema——schema.json 元声明装载（P8.5 配置群规范 design-ch4-workspace §四）。
    /// 每项声明：键 / 文件归属 / 默认值 / 敏感 / 可写 / 描述——配置面唯一契约。
    /// 当前消费：/api/v1/config GET 输出面（default/writable/desc 补全——LLM 可读）；P8.5d config.* 写通道按此声明放权。
    /// </summary>
    public sealed class ConfigSchema
    {
        /// <summary>
        /// schema 条目——配置项元声明
        /// </summary>
        public sealed class Item
        {
            /// <summary>
            /// 配置键（带文件前缀寻址：llm.api_key / app.frame_ms）
            /// </summary>
            public string Key = "";

            /// <summary>
            /// 文件归属——llm.cfg / app.json / workspace.json
            /// </summary>
            public string File = "";

            /// <summary>
            /// 默认值——缺省空串
            /// </summary>
            public string Default = "";

            /// <summary>
            /// 敏感标志——true 时所有输出面掩码（api_key/secret/token 系）
            /// </summary>
            public bool Sensitive;

            /// <summary>
            /// 可写标志——false 时写通道拒绝（roots 等结构项只读）
            /// </summary>
            public bool Writable;

            /// <summary>
            /// 描述——一行 ≤40 字
            /// </summary>
            public string Desc = "";
            /// <summary>
            /// 空值语义文案——「空 = …」的含义（如「空=未配置，工具明示不可用」；缺省空串 = 无特殊语义）
            /// </summary>
            public string EmptyDesc = "";
            /// <summary>
            /// 值类型——"string"（缺省）/ "int" / "bool"；P8.5d 值域校验
            /// </summary>
            public string Type = "";
            /// <summary>
            /// 数值下限（type=int 时生效）
            /// </summary>
            public string Min = "";
            /// <summary>
            /// 数值上限（type=int 时生效）
            /// </summary>
            public string Max = "";

            /// <summary>
            /// 枚举值域——type=string/空 且非空时生效（写通道只接受其中之一；空=不限制）
            /// </summary>
            public string[] Values = new string[0];
        }

        /// <summary>
        /// schema 条目表——按键查询（Ordinal）
        /// </summary>
        private readonly Dictionary<string, Item> _items;
        /// <summary>
        /// 运行副本路径——Load 时记录（如 Data/config/schema.json；声明通道据此写回）
        /// </summary>
        public string SourcePath = "";
        /// <summary>
        /// schema 模板路径——宿主 Bootstrap 注入（如 config/schema.json.example）；声明通道同时改写模板与运行副本，避免启动期模板同步覆盖
        /// </summary>
        public string TemplatePath = "";
        /// <summary>
        /// 装载降级事实——空=正常；非空=格式 code|state|detail（读取失败 / JSON 损坏时已回退为空 schema）。
        /// 宿主据此登记启动降级项（A260：配置损坏不得阻断启动）。
        /// </summary>
        public string LoadDegrade = "";

        /// <summary>
        /// 建立空 schema
        /// </summary>
        public ConfigSchema()
        {
            _items = new Dictionary<string, Item>(StringComparer.Ordinal);
        }

        // [段1] 装载
        /// <summary>
        /// 从 schema.json 装载——文件缺失返回空 schema（兼容无配置启动）；损坏抛异常（配置了不静默）
        /// </summary>
        /// <param name="path">schema.json 路径</param>
        /// <returns>配置 schema</returns>
        public static ConfigSchema Load(string path)
        {
            ConfigSchema schema = new ConfigSchema();
            if (path != null)
            {
                schema.SourcePath = path;
            }
            if (path == null || path.Length == 0 || !File.Exists(path))
            {
                return schema;
            }
            // [段1] 读取——权限 / 占用失败 → 空 schema + 降级事实（不阻断启动）
            string text;
            try
            {
                text = File.ReadAllText(path);
            }
            catch (Exception ex)
            {
                schema.LoadDegrade = "CFG_UNREADABLE|fallback|" + path + " | " + ex.Message;
                return schema;
            }
            // [段2] 解析——JSON 损坏 → 备份坏档 + 空 schema + 降级事实
            JsonDocument doc;
            try
            {
                doc = JsonUtil.ParseStrict(text);
            }
            catch (Exception ex)
            {
                string backup = ConfigFileGuard.BackupCorrupt(path);
                string note = "";
                if (backup.Length > 0)
                {
                    note = "（坏档备份 " + backup + "）";
                }
                schema.LoadDegrade = "CFG_CORRUPT|fallback|" + path + note + " | " + ex.Message;
                return schema;
            }
            using (doc)
            {
                JsonElement root = doc.RootElement;
                JsonElement itemsEl;
                if (!root.TryGetProperty("items", out itemsEl) || itemsEl.ValueKind != JsonValueKind.Array)
                {
                    return schema;
                }
                for (int i = 0; i < itemsEl.GetArrayLength(); i++)
                {
                    JsonElement el = itemsEl[i];
                    string key = GetProp(el, "key");
                    if (key.Length == 0)
                    {
                        continue;
                    }
                    Item item = new Item();
                    item.Key = key;
                    item.File = GetProp(el, "file");
                    item.Default = GetProp(el, "default");
                    item.Desc = GetProp(el, "desc");
                    item.EmptyDesc = GetProp(el, "empty_desc");
                    item.Sensitive = GetBoolProp(el, "sensitive", false);
                    item.Writable = GetBoolProp(el, "writable", true);
                    item.Type = GetProp(el, "type");
                    item.Min = GetProp(el, "min");
                    item.Max = GetProp(el, "max");
                    JsonElement valuesEl;
                    if (el.TryGetProperty("values", out valuesEl) && valuesEl.ValueKind == JsonValueKind.Array)
                    {
                        List<string> values = new List<string>();
                        for (int v = 0; v < valuesEl.GetArrayLength(); v = v + 1)
                        {
                            JsonElement valueEl = valuesEl[v];
                            if (valueEl.ValueKind == JsonValueKind.String)
                            {
                                string? got = valueEl.GetString();
                                if (got != null && got.Length > 0)
                                {
                                    values.Add(got);
                                }
                            }
                        }
                        item.Values = values.ToArray();
                    }
                    schema._items[key] = item;
                }
            }
            return schema;
        }

        // [段2] 查询
        /// <summary>
        /// 全部条目快照——按装载顺序（键组序）
        /// </summary>
        /// <returns>条目数组</returns>
        public Item[] All()
        {
            Item[] copy = new Item[_items.Count];
            int i = 0;
            foreach (KeyValuePair<string, Item> pair in _items)
            {
                copy[i] = pair.Value;
                i = i + 1;
            }
            return copy;
        }

        /// <summary>
        /// 按键查条目——不存在返回 null
        /// </summary>
        /// <param name="key">配置键</param>
        /// <returns>条目或 null</returns>
        public Item? Find(string key)
        {
            Item? found;
            if (_items.TryGetValue(key, out found))
            {
                return found;
            }
            return null;
        }

        /// <summary>
        /// 敏感判定——schema 声明优先；未声明时键名子串兜底（api_key/secret/token 系）
        /// </summary>
        /// <param name="key">配置键</param>
        /// <returns>是否敏感</returns>
        public bool IsSensitive(string key)
        {
            Item? item = Find(key);
            if (item != null)
            {
                return item.Sensitive;
            }
            return key.IndexOf("api_key", StringComparison.OrdinalIgnoreCase) >= 0
                || key.IndexOf("secret", StringComparison.OrdinalIgnoreCase) >= 0
                || key.IndexOf("token", StringComparison.OrdinalIgnoreCase) >= 0;
        }

        // [段3] 辅助
        /// <summary>
        /// 读取字符串属性——缺字段返回空串（防御式）
        /// </summary>
        /// <param name="obj">JSON 元素</param>
        /// <param name="prop">属性名</param>
        /// <returns>属性值</returns>
        private static string GetProp(JsonElement obj, string prop)
        {
            JsonElement value;
            if (obj.TryGetProperty(prop, out value) && value.ValueKind == JsonValueKind.String)
            {
                string? got = value.GetString();
                if (got != null)
                {
                    return got;
                }
            }
            return "";
        }

        /// <summary>
        /// 读取布尔属性——缺字段返回默认值（防御式）
        /// </summary>
        /// <param name="obj">JSON 元素</param>
        /// <param name="prop">属性名</param>
        /// <param name="defaultValue">缺省值</param>
        /// <returns>布尔值</returns>
        private static bool GetBoolProp(JsonElement obj, string prop, bool defaultValue)
        {
            JsonElement value;
            if (obj.TryGetProperty(prop, out value) && (value.ValueKind == JsonValueKind.True || value.ValueKind == JsonValueKind.False))
            {
                return value.GetBoolean();
            }
            return defaultValue;
        }
        /// <summary>
        /// 枚举值域命中判定——忽略大小写（声明 values 与候选值逐项比较）
        /// </summary>
        /// <param name="values">声明值域</param>
        /// <param name="value">候选值</param>
        /// <returns>是否命中</returns>
        private static bool ContainsValue(string[] values, string value)
        {
            for (int i = 0; i < values.Length; i = i + 1)
            {
                if (string.Equals(values[i], value, StringComparison.OrdinalIgnoreCase))
                {
                    return true;
                }
            }
            return false;
        }

        /// <summary>
        /// 值域校验——P8.5d 写入通道（SetChecked）前置校验。
        /// 规则：值长度上限 1024；type=int → 整数解析 + min/max 范围；type=bool → true/false/1/0；type=string/空 → 放行。
        /// </summary>
        /// <param name="key">配置键</param>
        /// <param name="value">候选值</param>
        /// <param name="error">失败原因（成功为空串）</param>
        /// <returns>是否通过</returns>
        public bool Validate(string key, string value, out string error)
        {
            error = "";
            Item? item = Find(key);
            if (item == null)
            {
                error = "配置键未在 schema 声明: " + key;
                return false;
            }

            if (value.Length > 1024)
            {
                error = "配置值过长（>1024）";
                return false;
            }

            string type = item.Type;
            if (type.Length == 0 || type == "string")
            {
                // 枚举值域——声明非空时只接受其中之一（忽略大小写；值原样落盘，消费端归一）
                if (item.Values.Length > 0 && !ContainsValue(item.Values, value))
                {
                    error = "配置项 " + key + " 只接受以下值之一: " + string.Join(" / ", item.Values);
                    return false;
                }

                return true;
            }

            if (type == "int")
            {
                long parsed;
                if (!long.TryParse(value, out parsed))
                {
                    error = "配置项 " + key + " 需要整数: " + value;
                    return false;
                }

                if (item.Min.Length > 0)
                {
                    long min;
                    if (long.TryParse(item.Min, out min) && parsed < min)
                    {
                        error = "配置项 " + key + " 低于下限 " + item.Min;
                        return false;
                    }
                }

                if (item.Max.Length > 0)
                {
                    long max;
                    if (long.TryParse(item.Max, out max) && parsed > max)
                    {
                        error = "配置项 " + key + " 超出上限 " + item.Max;
                        return false;
                    }
                }

                return true;
            }

            if (type == "bool")
            {
                if (value != "true" && value != "false" && value != "1" && value != "0")
                {
                    error = "配置项 " + key + " 需要布尔值: " + value;
                    return false;
                }

                return true;
            }

            error = "未知值类型: " + type;
            return false;
        }

        // [段4] 声明（A152 派生段创建通道——config-set declare=true 的落地实现）
        /// <summary>
        /// 声明新配置项——内存 schema 立即生效 + 模板与运行副本同步插入（两文件内容保持一致，避免下次启动被模板同步覆盖）。
        /// 约束：键须带段前缀（形如 x.key）；段已有声明项则沿用其文件归属，否则按「段名.cfg」推断。
        /// </summary>
        /// <param name="key">配置键（须带段前缀）</param>
        /// <param name="desc">描述（空则回落默认文案）</param>
        /// <param name="fileName">归属文件名（出参）</param>
        /// <param name="newSegment">是否新建段（出参——true 时段文件尚未注册，调用方须 AddFile）</param>
        /// <param name="error">失败原因（成功为空串）</param>
        /// <returns>是否声明成功</returns>
        public bool Declare(string key, string desc, out string fileName, out bool newSegment, out string error)
        {
            fileName = "";
            newSegment = false;
            error = "";
            if (key == null || key.Length == 0)
            {
                error = "key 为空";
                return false;
            }
            int dot = key.IndexOf('.');
            if (dot <= 0 || dot == key.Length - 1)
            {
                error = "键缺少段前缀（形如 ui.font_scale）";
                return false;
            }
            string segment = key.Substring(0, dot);
            if (!IsSimpleName(segment) || !IsSimpleName(key.Substring(dot + 1)))
            {
                error = "键名只允许字母 / 数字 / 下划线（段与键名各一段）: " + key;
                return false;
            }
            if (_items.ContainsKey(key))
            {
                error = "配置键已声明: " + key;
                return false;
            }
            // [段1] 文件归属——段已声明则沿用其文件，否则按「段名.cfg」推断
            string file = "";
            bool segmentKnown = false;
            foreach (KeyValuePair<string, Item> pair in _items)
            {
                if (!string.Equals(PrefixOf(pair.Value.Key), segment, StringComparison.OrdinalIgnoreCase))
                {
                    continue;
                }
                segmentKnown = true;
                if (file.Length == 0)
                {
                    file = pair.Value.File;
                }
            }
            if (file.Length == 0)
            {
                file = segment + ".cfg";
            }
            if (!segmentKnown)
            {
                newSegment = true;
            }
            // [段2] 模板与运行副本文本插入（同一份内容写两处）
            string templatePath = TemplatePath;
            if (templatePath == null || templatePath.Length == 0 || !File.Exists(templatePath))
            {
                error = "schema 模板未定位（TemplatePath 空或文件不存在）——声明通道需要模板";
                return false;
            }
            string sourcePath = SourcePath;
            if (sourcePath == null || sourcePath.Length == 0)
            {
                error = "schema 运行副本路径未知（Load 未记录 SourcePath）";
                return false;
            }
            string text = File.ReadAllText(templatePath);
            string updated = InsertItem(text, BuildItemLine(key, file, desc));
            if (updated.Length == 0)
            {
                error = "schema 模板结构不识别（未找到 items 数组闭合）: " + templatePath;
                return false;
            }
            UTF8Encoding encoding = new UTF8Encoding(HasUtf8Bom(templatePath));
            File.WriteAllText(templatePath, updated, encoding);
            File.WriteAllText(sourcePath, updated, encoding);
            // [段3] 内存项——声明后立即可读可写（无需重启）
            Item item = new Item();
            item.Key = key;
            item.File = file;
            item.Default = "";
            item.Sensitive = false;
            item.Writable = true;
            item.Desc = desc;
            if (item.Desc == null || item.Desc.Length == 0)
            {
                item.Desc = "由 config-set declare 声明（schema 派生段）";
            }
            item.Type = "string";
            _items[key] = item;
            fileName = file;
            LogStore.Add("ConfigSchema", 1, "配置键声明: " + key + " → " + file + "（模板与运行副本同步写）", "SYS");
            return true;
        }

        /// <summary>
        /// 段前缀提取——点号前段（无点号或点号在首位返回空串）
        /// </summary>
        /// <param name="key">配置键</param>
        /// <returns>段名（空=无段）</returns>
        private static string PrefixOf(string key)
        {
            if (key == null)
            {
                return "";
            }
            int dot = key.IndexOf('.');
            if (dot <= 0)
            {
                return "";
            }
            return key.Substring(0, dot);
        }

        /// <summary>
        /// 简易名判定——字母 / 数字 / 下划线，非空
        /// </summary>
        /// <param name="text">候选名</param>
        /// <returns>是否合法</returns>
        private static bool IsSimpleName(string text)
        {
            if (text == null || text.Length == 0)
            {
                return false;
            }
            for (int i = 0; i < text.Length; i = i + 1)
            {
                char c = text[i];
                bool ok = (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_';
                if (!ok)
                {
                    return false;
                }
            }
            return true;
        }

        /// <summary>
        /// 新项 JSON 行构造——缩进 4 空格，与模板既有排版一致
        /// </summary>
        /// <param name="key">配置键</param>
        /// <param name="file">归属文件名</param>
        /// <param name="desc">描述（空则回落默认文案）</param>
        /// <returns>单行 JSON 项</returns>
        private static string BuildItemLine(string key, string file, string desc)
        {
            string text = desc;
            if (text == null || text.Length == 0)
            {
                text = "由 config-set declare 声明（schema 派生段）";
            }
            return "    { \"key\": " + JsonUtil.Str(key) + ", \"file\": " + JsonUtil.Str(file)
                + ", \"default\": \"\", \"sensitive\": false, \"writable\": true, \"type\": \"string\", \"desc\": " + JsonUtil.Str(text) + " }";
        }

        /// <summary>
        /// 文本插入——在 items 数组闭合行前追加一项（补前项逗号；换行风格跟随原文件）
        /// </summary>
        /// <param name="text">模板全文</param>
        /// <param name="itemLine">新项行（不含换行）</param>
        /// <returns>插入后的全文（结构不识别返回空串）</returns>
        private static string InsertItem(string text, string itemLine)
        {
            if (text == null || text.Length == 0)
            {
                return "";
            }
            int close = text.LastIndexOf("\n  ]", StringComparison.Ordinal);
            if (close < 0)
            {
                return "";
            }
            string newline = "\n";
            if (text.IndexOf("\r\n", StringComparison.Ordinal) >= 0)
            {
                newline = "\r\n";
            }
            string head = text.Substring(0, close);
            string tail = text.Substring(close);
            int end = head.Length;
            while (end > 0 && (head[end - 1] == ' ' || head[end - 1] == '\t' || head[end - 1] == '\r' || head[end - 1] == '\n'))
            {
                end = end - 1;
            }
            string trimmed = head.Substring(0, end);
            string whitespace = head.Substring(end);
            if (!trimmed.EndsWith(",", StringComparison.Ordinal))
            {
                trimmed = trimmed + ",";
            }
            return trimmed + newline + itemLine + whitespace + tail;
        }

        /// <summary>
        /// UTF-8 BOM 探测——读前三字节判定（写回时保持同一编码形态）
        /// </summary>
        /// <param name="path">文件路径</param>
        /// <returns>是否有 BOM（探测失败按无 BOM 处理 + WARN）</returns>
        private static bool HasUtf8Bom(string path)
        {
            try
            {
                byte[] head = new byte[3];
                int read = 0;
                using (FileStream fs = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite))
                {
                    read = fs.Read(head, 0, 3);
                }
                if (read < 3)
                {
                    return false;
                }
                return head[0] == 0xEF && head[1] == 0xBB && head[2] == 0xBF;
            }
            catch (Exception ex)
            {
                LogStore.Add("ConfigSchema", 2, "BOM 探测失败（按无 BOM 处理）: " + ex.Message, "SYS");
                return false;
            }
        }
    }
}