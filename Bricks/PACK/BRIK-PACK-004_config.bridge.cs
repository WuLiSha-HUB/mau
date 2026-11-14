// ═══════════════════════════════════════════════════
// 积木: config.bridge
// ID:   BRIK-PACK-004
// 类别: PACK
// 作用: 配置工具桥接口积木——配置自改能力声明（PACK 类，config.* 积木统一经本桥访问）
// 依赖: 无
// 包: 无
// 引用: Mau.Runtime（ConfigStore/DataBox）
// 原理: DataBox.TryResolve<ConfigStore>（全局 store——与配置面 HTTP 同源，§4.4）→ method 分派
//       纯 KV（A364 起）：读面输出已落盘键值，写面 SetChecked 写死防御 + 原子写回滚——无声明面 / 无默认值来源
//       reset 语义 = 删除落盘值（回到未设置，消费侧硬编码默认生效）
// 方法: list → （无参）
//        get → key
//        set → key,value
//        reset → key（空=全群）
// 常用: config.* 四工具的调度底座（P8.5d——配置自改最小闭环；llm.* 私密环境变量只读）
// ═══════════════════════════════════════════════════
using System;
using System.IO;
using System.Text;
using System.Text.Json;
using Mau.Runtime;

namespace Mau.Bricks
{
    /// <summary>
    /// PACK 接口积木——config.bridge（调度 ConfigStore）
    /// </summary>
    public static class ConfigBridgeBrick
    {
        /// <summary>
        /// 配置工具桥单方法调度——method 白名单 + argsJson 展平参数（list/get/set/reset）
        /// </summary>
        /// <param name="method">操作名（list/get/set/reset）</param>
        /// <param name="argsJson">展平参数 JSON</param>
        /// <param name="result">结果文本</param>
        /// <returns>true=调用成功</returns>
        public static bool Invoke(string method, string argsJson, out string result)
        {
            result = "";
            // [参数面] 声明面口径零容忍——未知 / 缺值一律 ERR|BAD_ARGS（catId 保留键放行）
            string allowedKeys;
            string requiredKeys;
            if (method == "list")
            {
                allowedKeys = "";
                requiredKeys = "";
            }
            else if (method == "get")
            {
                allowedKeys = "key";
                requiredKeys = "key";
            }
            else if (method == "set")
            {
                allowedKeys = "key value";
                requiredKeys = "key value";
            }
            else if (method == "reset")
            {
                allowedKeys = "key";
                requiredKeys = "";
            }
            else
            {
                result = "ERR|UNKNOWN_METHOD|config." + method;
                return false;
            }
            string badArgs = JsonArgs.Validate(argsJson, allowedKeys, requiredKeys, "", "");
            if (badArgs.Length > 0)
            {
                result = badArgs;
                return false;
            }
            // 全局 store 单一真相——config-* 与配置面（HTTP /api/v1/config）同源（§4.4：per-cat 路由已退役）
            ConfigStore cfg = null!;
            bool cfgBound = DataBox.TryResolve<ConfigStore>(out cfg);
            if (!cfgBound || cfg == null)
            {
                result = "ERR|CONFIG_NO_STORE|宿主未注入 ConfigStore";
                return false;
            }
            try
            {
                if (method == "list")
                {
                    result = ListAll(cfg);
                    return true;
                }
                if (method == "get")
                {
                    result = GetOne(cfg, JsonArgs.Get(argsJson, "key"));
                    return true;
                }
                if (method == "set")
                {
                    result = SetOne(cfg, JsonArgs.Get(argsJson, "key"), JsonArgs.Get(argsJson, "value"));
                    return true;
                }
                if (method == "reset")
                {
                    result = ResetOne(cfg, JsonArgs.Get(argsJson, "key"));
                    return true;
                }
                result = "ERR|UNKNOWN_METHOD|config." + method;
                return false;
            }
            catch (Exception ex)
            {
                result = "ERR|" + ex.GetType().Name + "|" + ex.Message;
                return false;
            }
        }

        /// <summary>
        /// list——配置全览（纯 KV）：全部已落盘键值 + 敏感键掩码 + 段清单
        /// </summary>
        /// <param name="cfg">配置存储</param>
        /// <returns>清单文本</returns>
        private static string ListAll(ConfigStore cfg)
        {
            System.Collections.Generic.KeyValuePair<string, string>[] items = cfg.All();
            StringBuilder sb = new StringBuilder();
            int sensitive = 0;
            for (int i = 0; i < items.Length; i = i + 1)
            {
                string key = items[i].Key;
                string value = items[i].Value;
                bool secret = IsSecretKey(key);
                if (secret)
                {
                    sensitive = sensitive + 1;
                    if (value.Length > 0)
                    {
                        value = "****";
                    }
                }
                sb.Append(key);
                sb.Append('=');
                sb.Append(value);
                sb.AppendLine();
            }
            System.Collections.Generic.Dictionary<string, object> fields = new System.Collections.Generic.Dictionary<string, object>();
            fields["sensitive"] = sensitive;
            fields["sections"] = string.Join(" / ", ListSegments(cfg).ToArray());
            return MetaHead("config-list", true, "", items.Length, fields) + "\n" + items.Length.ToString() + " 项 · 敏感 " + sensitive.ToString() + "\n" + TrimResult(sb.ToString(), 20000);
        }

        /// <summary>
        /// 敏感键判定——api_key/secret/token 系内容掩码（无声明面，按键名兜底）
        /// </summary>
        /// <param name="key">配置键</param>
        /// <returns>是否敏感</returns>
        private static bool IsSecretKey(string key)
        {
            return key.IndexOf("api_key", StringComparison.OrdinalIgnoreCase) >= 0
                || key.IndexOf("secret", StringComparison.OrdinalIgnoreCase) >= 0
                || key.IndexOf("token", StringComparison.OrdinalIgnoreCase) >= 0;
        }

        /// <summary>
        /// get——单项查询（纯 KV）：只输出已落盘值；无落盘 → ERR|NOT_FOUND（附段清单 / 段内键清单）
        /// </summary>
        /// <param name="cfg">配置存储</param>
        /// <param name="key">配置键</param>
        /// <returns>单项文本</returns>
        private static string GetOne(ConfigStore cfg, string key)
        {
            if (key == null || key.Length == 0)
            {
                return "ERR|BAD_ARGS|缺少参数 key";
            }
            string value = "";
            if (!cfg.TryGet(key, out value))
            {
                return "ERR|NOT_FOUND|" + DescribeMissing(cfg, key);
            }
            bool secret = IsSecretKey(key);
            if (secret && value.Length > 0)
            {
                value = "****";
            }
            System.Collections.Generic.Dictionary<string, object> fields = new System.Collections.Generic.Dictionary<string, object>();
            fields["sensitive"] = secret;
            return MetaHead("config-get", true, key, -1, fields) + "\n" + key + " | 已落盘" + "\n" + key + "=" + value;
        }

        /// <summary>
        /// set——写入（纯 KV）：SetChecked 写死防御（键名 / 长度 / 掩码）+ 内存写入 + 原子写回滚；value 空 = 清除该项落盘值（回到默认）
        /// </summary>
        /// <param name="cfg">配置存储</param>
        /// <param name="key">配置键</param>
        /// <param name="value">新值</param>
        /// <returns>确认文本</returns>
        private static string SetOne(ConfigStore cfg, string key, string value)
        {
            if (key == null || key.Length == 0)
            {
                return "ERR|BAD_ARGS|缺少参数 key";
            }
            // 空值 = 清除该项落盘值（回到默认；与 HTTP 写面同语义）
            if (value == null || value.Length == 0)
            {
                string clearError;
                if (!cfg.ResetToDefault(key, out clearError))
                {
                    return "ERR|CONFIG_REJECT|" + clearError;
                }
                System.Collections.Generic.Dictionary<string, object> clearFields = new System.Collections.Generic.Dictionary<string, object>();
                clearFields["sensitive"] = IsSecretKey(key);
                clearFields["cleared"] = true;
                return MetaHead("config-set", true, key, -1, clearFields) + "\n" + key + " | 已清除落盘值（回到默认）";
            }
            string error;
            if (!cfg.SetChecked(key, value, out error))
            {
                return "ERR|CONFIG_REJECT|" + error;
            }
            bool sensitive = IsSecretKey(key);
            // 结构化返回体（design-ch4-tools 附录 · A214）——首行 JSON 头（target/items）+ 正文摘要行
            System.Collections.Generic.Dictionary<string, object> fields = new System.Collections.Generic.Dictionary<string, object>();
            fields["sensitive"] = sensitive;
            return MetaHead("config-set", true, key, -1, fields) + "\n" + key + " | 已更新 = " + (sensitive ? "****" : value);
        }

        /// <summary>
        /// reset——清掉落盘值（纯 KV）：key 空 = 全部落盘项；key 非空 = 单项（回到未设置）
        /// </summary>
        /// <param name="cfg">配置存储</param>
        /// <param name="key">配置键（空=全群）</param>
        /// <returns>确认文本</returns>
        private static string ResetOne(ConfigStore cfg, string key)
        {
            int resetCount = 0;
            if (key == null || key.Length == 0)
            {
                resetCount = cfg.All().Length;
            }
            else
            {
                resetCount = 1;
            }
            string clearKey = "";
            if (key != null)
            {
                clearKey = key;
            }
            string error;
            if (!cfg.ResetToDefault(clearKey, out error))
            {
                return "ERR|CONFIG_REJECT|" + error;
            }
            string keyText = "";
            if (key != null)
            {
                keyText = key;
            }
            bool all = (keyText.Length == 0);
            System.Collections.Generic.Dictionary<string, object> fields = new System.Collections.Generic.Dictionary<string, object>();
            fields["scope"] = all ? "all" : "single";
            if (all)
            {
                return MetaHead("config-reset", true, "", resetCount, fields) + "\n" + "全部落盘配置项 | 已清除 " + resetCount.ToString() + " 项（回到未设置）";
            }
            return MetaHead("config-reset", true, keyText, resetCount, fields) + "\n" + keyText + " | 已清除（回到未设置）";
        }

        /// <summary>
        /// 展平参数提取——argsJson 中取字符串值（不存在返回空串）
        /// </summary>
        /// <param name="argumentsJson">参数 JSON</param>
        /// <param name="key">参数名</param>
        /// <summary>
        /// 段名提取（A152）——键首点号前缀（`ui.font_scale` → `ui`）；无点号或点号在首位返回空串
        /// </summary>
        /// <param name="key">配置键</param>
        /// <returns>段名（空=无段）</returns>
        private static string SegmentOf(string key)
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
        /// 段清单——落盘键前缀（去重保序）
        /// </summary>
        /// <param name="cfg">配置存储</param>
        /// <returns>段名列表</returns>
        private static System.Collections.Generic.List<string> ListSegments(ConfigStore cfg)
        {
            System.Collections.Generic.List<string> segments = new System.Collections.Generic.List<string>();
            if (cfg != null)
            {
                System.Collections.Generic.KeyValuePair<string, string>[] all = cfg.All();
                for (int i = 0; i < all.Length; i = i + 1)
                {
                    AddSegment(segments, SegmentOf(all[i].Key));
                }
            }
            return segments;
        }

        /// <summary>
        /// 段名去重追加——已存在（忽略大小写）不重复加入
        /// </summary>
        /// <param name="segments">段清单</param>
        /// <param name="segment">候选段名（空跳过）</param>
        private static void AddSegment(System.Collections.Generic.List<string> segments, string segment)
        {
            if (segment.Length == 0)
            {
                return;
            }
            for (int i = 0; i < segments.Count; i = i + 1)
            {
                if (string.Equals(segments[i], segment, StringComparison.OrdinalIgnoreCase))
                {
                    return;
                }
            }
            segments.Add(segment);
        }

        /// <summary>
        /// 缺键说明——分「段不存在」/「键不存在」：段不存在时列可用段；段存在时列段内键（上限 8 条）
        /// </summary>
        /// <param name="cfg">配置存储</param>
        /// <param name="key">未命中的配置键</param>
        /// <returns>说明文本</returns>
        private static string DescribeMissing(ConfigStore cfg, string key)
        {
            System.Collections.Generic.List<string> segments = ListSegments(cfg);
            string segment = SegmentOf(key);
            if (segment.Length == 0)
            {
                return "键缺少段前缀（形如 ui.font_scale）——可用段: " + string.Join(" / ", segments.ToArray());
            }
            bool known = false;
            for (int i = 0; i < segments.Count; i = i + 1)
            {
                if (string.Equals(segments[i], segment, StringComparison.OrdinalIgnoreCase))
                {
                    known = true;
                    break;
                }
            }
            if (!known)
            {
                return "段不存在: " + segment + "——可用段: " + string.Join(" / ", segments.ToArray());
            }
            System.Collections.Generic.List<string> keys = new System.Collections.Generic.List<string>();
            if (cfg != null)
            {
                System.Collections.Generic.KeyValuePair<string, string>[] all = cfg.All();
                for (int i = 0; i < all.Length; i = i + 1)
                {
                    if (!string.Equals(SegmentOf(all[i].Key), segment, StringComparison.OrdinalIgnoreCase))
                    {
                        continue;
                    }
                    keys.Add(all[i].Key);
                }
            }
            System.Text.StringBuilder sb = new System.Text.StringBuilder();
            sb.Append("键不存在: ");
            sb.Append(key);
            sb.Append("——段 ");
            sb.Append(segment);
            sb.Append(" 现有键 ");
            sb.Append(keys.Count.ToString());
            sb.Append(" 个");
            if (keys.Count > 0)
            {
                int show = keys.Count;
                if (show > 8)
                {
                    show = 8;
                }
                sb.Append(": ");
                for (int i = 0; i < show; i = i + 1)
                {
                    if (i > 0)
                    {
                        sb.Append(", ");
                    }
                    sb.Append(keys[i]);
                }
                if (keys.Count > show)
                {
                    sb.Append(" …");
                }
            }
            return sb.ToString();
        }

        /// <summary>
        /// 结构化元数据头（统一口径·A214）——恒定 ok / tool + 主来源 target + 主计数 items + 专有字段（插入序）。
        /// 约定（design-ch4-tools 附录）：返回体 = 首行 JSON 头 + 正文摘要行（正文不塞进 JSON——避免转义膨胀）。
        /// </summary>
        /// <param name="tool">工具名（config-list / config-get / config-set / config-reset）</param>
        /// <param name="ok">成败（带头即成功——失败走 ERR| 不带头）</param>
        /// <param name="target">主来源（配置键；空串 = 省略）</param>
        /// <param name="items">主计数（负值 = 省略）</param>
        /// <param name="fields">附加字段（按插入序输出）</param>
        /// <returns>单行 JSON</returns>
        private static string MetaHead(string tool, bool ok, string target, int items, System.Collections.Generic.Dictionary<string, object> fields)
        {
            System.Collections.Generic.Dictionary<string, object> head = new System.Collections.Generic.Dictionary<string, object>();
            head["ok"] = ok;
            head["tool"] = tool;
            if (target.Length > 0)
            {
                head["target"] = target;
            }
            if (items >= 0)
            {
                head["items"] = items;
            }
            foreach (System.Collections.Generic.KeyValuePair<string, object> kv in fields)
            {
                head[kv.Key] = kv.Value;
            }
            return JsonSerializer.Serialize(head);
        }

        /// <summary>
        /// 结果截断——超长文本保留头部 + 截断提示（上下文防爆）
        /// </summary>
        /// <param name="text">原文</param>
        /// <param name="max">上限字符数</param>
        /// <returns>截断文本</returns>
        private static string TrimResult(string text, int max)
        {
            if (text == null || text.Length <= max)
            {
                return text ?? "";
            }
            return text.Substring(0, max) + "\n…[截断: 共 " + text.Length.ToString() + " 字符，仅保留前 " + max.ToString() + "]";
        }
    }
}
// #MAU_CHECKSUM:SHA256:0BDAEE7F86FE31CFF8D482C0AD47D183521A04FCA3E3B7E423011A7A4B82CF52
