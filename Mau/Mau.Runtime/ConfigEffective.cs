using System;
using System.Collections.Generic;

namespace Mau.Runtime
{
    /// <summary>
    /// 配置有效值解析——读面单一出口（design-ch4-workspace §4.3 / §4.5）。
    /// 三层兜底：file（落盘显式值）→ env（MAU_LLM_* 环境变量）→ default（schema 声明默认值）；
    /// 未声明键（落盘存在但 schema 无声明）一并输出并显式标注 declared=false + 只读（不静默隐藏）。
    /// 消费面：HTTP GET /api/v1/config 与 config.bridge（工具面）共用——掩码策略由调用方决定（管理面与 LLM 面不同）。
    /// </summary>
    public static class ConfigEffective
    {
        /// <summary>
        /// 有效配置项——读面统一结构（未声明键 declared=false + writable=false）
        /// </summary>
        public sealed class Entry
        {
            /// <summary>
            /// 配置键
            /// </summary>
            public string Key = "";

            /// <summary>
            /// 有效值（未掩码——调用方按消费面决定是否掩码）
            /// </summary>
            public string Value = "";

            /// <summary>
            /// 来源——file（落盘显式值）/ env（环境变量兜底）/ default（schema 默认值）
            /// </summary>
            public string Source = "";

            /// <summary>
            /// 是否在 schema 声明
            /// </summary>
            public bool Declared;

            /// <summary>
            /// 文件归属（未声明为空串）
            /// </summary>
            public string File = "";

            /// <summary>
            /// schema 默认值（未声明为空串）
            /// </summary>
            public string Default = "";

            /// <summary>
            /// 可写标志（未声明恒 false）
            /// </summary>
            public bool Writable;

            /// <summary>
            /// 敏感标志（未声明按键名兜底判定）
            /// </summary>
            public bool Sensitive;

            /// <summary>
            /// 描述（未声明为固定文案）
            /// </summary>
            public string Desc = "";

            /// <summary>
            /// 空值语义文案（未声明为空串）
            /// </summary>
            public string EmptyDesc = "";

            /// <summary>
            /// 值类型（未声明为空串）
            /// </summary>
            public string Type = "";

            /// <summary>
            /// 数值下限（未声明为空串）
            /// </summary>
            public string Min = "";

            /// <summary>
            /// 数值上限（未声明为空串）
            /// </summary>
            public string Max = "";
        }

        /// <summary>
        /// 未声明键描述文案——读面必须标明该键身份，不静默隐藏、不默认放行（§4.5）
        /// </summary>
        public const string UndeclaredDesc = "未在 schema 声明（只读；声明后可见可改）";

        /// <summary>
        /// 环境变量名解析——无兜底返回空串（M1c 后仅全局思考参数；API 连接三参归 LLM API 配置池）
        /// </summary>
        /// <param name="key">配置键</param>
        /// <returns>环境变量名（无兜底为空串）</returns>
        public static string EnvName(string key)
        {
            if (key == "llm.thinking")
            {
                return "MAU_LLM_THINKING";
            }
            if (key == "llm.reasoning_effort")
            {
                return "MAU_LLM_REASONING_EFFORT";
            }
            if (key == "llm.max_tokens")
            {
                return "MAU_LLM_MAX_TOKENS";
            }
            return "";
        }

        /// <summary>
        /// 三层解析单一出口——file → env → default；未声明且未落盘返回空值 + 空来源
        /// </summary>
        /// <param name="cfg">配置存储</param>
        /// <param name="schema">配置 schema（可 null）</param>
        /// <param name="key">配置键</param>
        /// <returns>解析结果</returns>
        public static Entry Resolve(ConfigStore cfg, ConfigSchema? schema, string key)
        {
            Entry entry = new Entry();
            entry.Key = key;
            string value;
            if (cfg != null && cfg.TryGet(key, out value))
            {
                entry.Value = value;
                entry.Source = "file";
            }
            else
            {
                string envName = EnvName(key);
                if (envName.Length > 0)
                {
                    string? raw = Environment.GetEnvironmentVariable(envName);
                    if (raw != null && raw.Length > 0)
                    {
                        entry.Value = raw;
                        entry.Source = "env";
                    }
                }
            }
            ConfigSchema.Item? item = null;
            if (schema != null)
            {
                item = schema.Find(key);
            }
            if (item != null)
            {
                entry.Declared = true;
                entry.File = item.File;
                entry.Default = item.Default;
                entry.Writable = item.Writable;
                entry.Sensitive = item.Sensitive;
                entry.Desc = item.Desc;
                entry.EmptyDesc = item.EmptyDesc;
                entry.Type = item.Type;
                entry.Min = item.Min;
                entry.Max = item.Max;
                if (entry.Source.Length == 0)
                {
                    entry.Value = item.Default;
                    entry.Source = "default";
                }
            }
            else
            {
                entry.Declared = false;
                entry.Writable = false;
                if (schema != null)
                {
                    entry.Sensitive = schema.IsSensitive(key);
                }
                else
                {
                    entry.Sensitive = IsSensitiveName(key);
                }
                entry.Desc = UndeclaredDesc;
            }
            return entry;
        }

        /// <summary>
        /// 全项解析——schema 声明全项（按声明序）+ 落盘存在但未声明的键（追加尾部，declared=false）
        /// </summary>
        /// <param name="cfg">配置存储</param>
        /// <param name="schema">配置 schema（可 null）</param>
        /// <returns>有效配置项数组</returns>
        public static Entry[] BuildAll(ConfigStore cfg, ConfigSchema? schema)
        {
            List<Entry> list = new List<Entry>();
            if (schema != null)
            {
                ConfigSchema.Item[] items = schema.All();
                for (int i = 0; i < items.Length; i = i + 1)
                {
                    list.Add(Resolve(cfg, schema, items[i].Key));
                }
            }
            if (cfg != null)
            {
                KeyValuePair<string, string>[] all = cfg.All();
                for (int i = 0; i < all.Length; i = i + 1)
                {
                    if (schema != null && schema.Find(all[i].Key) != null)
                    {
                        continue;
                    }
                    list.Add(Resolve(cfg, schema, all[i].Key));
                }
            }
            return list.ToArray();
        }

        /// <summary>
        /// 键名敏感兜底判定——api_key/secret/token 系（schema 未声明时使用）
        /// </summary>
        /// <param name="key">配置键</param>
        /// <returns>是否敏感</returns>
        private static bool IsSensitiveName(string key)
        {
            return key.IndexOf("api_key", StringComparison.OrdinalIgnoreCase) >= 0
                || key.IndexOf("secret", StringComparison.OrdinalIgnoreCase) >= 0
                || key.IndexOf("token", StringComparison.OrdinalIgnoreCase) >= 0;
        }
    }
}
