using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;

namespace Mau.Runtime
{
    /// <summary>
    /// 外观配置池的同步明文存储（Data/config/appearance.json）。
    /// 无密钥面——单文件落盘，不设 secrets 层。
    /// </summary>
    public sealed class CH_AppearanceConfigStore
    {
        /// <summary>池文件路径。</summary>
        private readonly string CH_AppearanceConfigStore_ConfigPath;

        /// <summary>统一 JSON 选项。</summary>
        private readonly JsonSerializerOptions CH_AppearanceConfigStore_JsonOptions;
        /// <summary>配置目录是否可写——false=内存态运行（调用方据此登记启动降级）</summary>
        public bool StorageWritable = true;
        /// <summary>配置目录不可写原因（空=可写）</summary>
        public string StorageError = "";
        /// <summary>最近一次读取失败原因——空=正常（损坏 / 无副本 / 反序列化失败）</summary>
        public string LastLoadError = "";

        /// <summary>绑定池文件所在设置目录。</summary>
        /// <param name="settingsRoot">Data Root 内设置目录</param>
        public CH_AppearanceConfigStore(string settingsRoot)
        {
            if (string.IsNullOrWhiteSpace(settingsRoot))
            {
                throw new ArgumentException("Appearance store root is empty.");
            }
            string safeSettingsRoot = Path.GetFullPath(settingsRoot);
            // A260 目录创建失败不阻断启动——标记只读运行，由宿主登记降级
            try
            {
                Directory.CreateDirectory(safeSettingsRoot);
                StorageWritable = true;
            }
            catch (Exception ex)
            {
                StorageWritable = false;
                StorageError = ex.Message;
            }
            CH_AppearanceConfigStore_ConfigPath = Path.Combine(safeSettingsRoot,
                "appearance.json");
            CH_AppearanceConfigStore_JsonOptions = new JsonSerializerOptions();
            CH_AppearanceConfigStore_JsonOptions.IncludeFields = true;
            CH_AppearanceConfigStore_JsonOptions.WriteIndented = true;
            CH_AppearanceConfigStore_JsonOptions.Encoder = System.Text.Encodings.Web.JavaScriptEncoder.UnsafeRelaxedJsonEscaping;   // 中文直出
            CH_AppearanceConfigStore_JsonOptions.PropertyNamingPolicy =
                JsonNamingPolicy.CamelCase;
        }

        /// <summary>读取全部池条目。</summary>
        /// <returns>按 AppearanceId 排序的独立数组</returns>
        public CH_AppearanceConfig[] GetAll()
        {
            if (!File.Exists(CH_AppearanceConfigStore_ConfigPath))
            {
                return Array.Empty<CH_AppearanceConfig>();
            }
            bool ignoredRecovery;
            string readError;
            string json = CH_AtomicTextFile.ReadWithRecovery(
                CH_AppearanceConfigStore_ConfigPath, IsValidConfigDocument,
                out ignoredRecovery, out readError);
            if (json.Length == 0)
            {
                LastLoadError = readError;
                return Array.Empty<CH_AppearanceConfig>();
            }
            CH_AppearanceConfig[]? loaded;
            try
            {
                loaded = JsonSerializer.Deserialize<
                    CH_AppearanceConfig[]>(json, CH_AppearanceConfigStore_JsonOptions);
            }
            catch (Exception ex)
            {
                LastLoadError = "反序列化失败：" + ex.Message;
                return Array.Empty<CH_AppearanceConfig>();
            }
            if (loaded == null)
            {
                LastLoadError = "配置反序列化为空";
                return Array.Empty<CH_AppearanceConfig>();
            }
            for (int i = 0; i < loaded.Length; i = i + 1)
            {
                NormalizeAndValidate(loaded[i]);
            }
            Array.Sort(loaded, delegate (CH_AppearanceConfig left,
                CH_AppearanceConfig right)
            {
                return left.AppearanceId.CompareTo(right.AppearanceId);
            });
            return CopyConfigs(loaded);
        }

        /// <summary>按稳定身份读取一份外观配置。</summary>
        /// <param name="appearanceId">外观配置身份</param>
        /// <param name="config">独立配置副本</param>
        /// <returns>是否存在</returns>
        public bool TryGet(Guid appearanceId, out CH_AppearanceConfig config)
        {
            CH_AppearanceConfig[] configs = GetAll();
            for (int i = 0; i < configs.Length; i = i + 1)
            {
                if (configs[i].AppearanceId == appearanceId)
                {
                    config = CopyConfig(configs[i]);
                    return true;
                }
            }
            config = new CH_AppearanceConfig();
            return false;
        }

        /// <summary>
        /// 解析默认外观——IsDefault=true 的条目；无默认返回 null。
        /// 池空亦返回 null（调用方取各字段保守值，不做静默回退造值）。
        /// </summary>
        /// <returns>默认配置；无默认 null</returns>
        public CH_AppearanceConfig? ResolveDefault()
        {
            CH_AppearanceConfig[] configs = GetAll();
            for (int i = 0; i < configs.Length; i = i + 1)
            {
                if (configs[i].IsDefault)
                {
                    return configs[i];
                }
            }
            return null;
        }

        /// <summary>
        /// 设置默认外观——清除其他默认标记 + 置目标默认（唯一默认语义；目标不存在返回 false）。
        /// </summary>
        /// <param name="appearanceId">目标配置身份</param>
        /// <returns>true=设置成功</returns>
        public bool SetDefault(Guid appearanceId)
        {
            CH_AppearanceConfig[] configs = GetAll();
            bool found = false;
            for (int i = 0; i < configs.Length; i = i + 1)
            {
                if (configs[i].AppearanceId == appearanceId)
                {
                    configs[i].IsDefault = true;
                    found = true;
                }
                else
                {
                    configs[i].IsDefault = false;
                }
            }
            if (!found)
            {
                return false;
            }
            SaveConfigs(configs);
            return true;
        }

        /// <summary>新增或替换一份外观配置。</summary>
        /// <param name="config">外观配置</param>
        public void Save(CH_AppearanceConfig config)
        {
            NormalizeAndValidate(config);
            List<CH_AppearanceConfig> configs = new List<CH_AppearanceConfig>(GetAll());
            bool replaced = false;
            for (int i = 0; i < configs.Count; i = i + 1)
            {
                if (configs[i].AppearanceId == config.AppearanceId)
                {
                    // 默认标记是池级属性——同身份重写默认不改动它（调用方显式置 true 时以显式为准）
                    if (!config.IsDefault)
                    {
                        config.IsDefault = configs[i].IsDefault;
                    }
                    configs[i] = CopyConfig(config);
                    replaced = true;
                    break;
                }
            }
            if (!replaced)
            {
                // 池空时首个条目标默认——唯一外观必然被跟随默认的猫消费（之后切换默认走 SetDefault 显式操作）
                if (configs.Count == 0)
                {
                    config.IsDefault = true;
                }
                configs.Add(CopyConfig(config));
            }
            SaveConfigs(configs.ToArray());
        }

        /// <summary>删除一份外观配置（引用它的猫解析为 null → 回退默认外观 / 保守值）。</summary>
        /// <param name="appearanceId">外观配置身份</param>
        /// <returns>true=存在并删除</returns>
        public bool Delete(Guid appearanceId)
        {
            List<CH_AppearanceConfig> configs = new List<CH_AppearanceConfig>(GetAll());
            bool removed = false;
            for (int i = configs.Count - 1; i >= 0; i = i - 1)
            {
                if (configs[i].AppearanceId == appearanceId)
                {
                    configs.RemoveAt(i);
                    removed = true;
                }
            }
            if (!removed)
            {
                return false;
            }
            SaveConfigs(configs.ToArray());
            return true;
        }

        /// <summary>规范化并拒绝身份或名称错误。</summary>
        private void NormalizeAndValidate(CH_AppearanceConfig config)
        {
            if (config == null)
            {
                throw new ArgumentNullException("config");
            }
            config.DisplayName = SafeText(config.DisplayName).Trim();
            config.PetKind = SafeText(config.PetKind).Trim();
            config.TtsStyle = SafeText(config.TtsStyle).Trim();
            if (config.SchemaVersion != 1 || config.AppearanceId == Guid.Empty
                || config.DisplayName.Length == 0)
            {
                throw new InvalidDataException("Appearance config is invalid.");
            }
        }

        /// <summary>原子保存池条目数组。</summary>
        private void SaveConfigs(CH_AppearanceConfig[] configs)
        {
            for (int i = 0; i < configs.Length; i = i + 1)
            {
                NormalizeAndValidate(configs[i]);
            }
            string json = JsonSerializer.Serialize(configs,
                CH_AppearanceConfigStore_JsonOptions);
            CH_AtomicTextFile.Write(CH_AppearanceConfigStore_ConfigPath, json);
        }

        /// <summary>验证池文档（损坏恢复用）。</summary>
        private bool IsValidConfigDocument(string content)
        {
            CH_AppearanceConfig[]? configs = JsonSerializer.Deserialize<
                CH_AppearanceConfig[]>(content, CH_AppearanceConfigStore_JsonOptions);
            if (configs == null)
            {
                return false;
            }
            for (int i = 0; i < configs.Length; i = i + 1)
            {
                NormalizeAndValidate(configs[i]);
            }
            return true;
        }

        /// <summary>复制池条目数组。</summary>
        private CH_AppearanceConfig[] CopyConfigs(CH_AppearanceConfig[] source)
        {
            CH_AppearanceConfig[] result = new CH_AppearanceConfig[source.Length];
            for (int i = 0; i < source.Length; i = i + 1)
            {
                result[i] = CopyConfig(source[i]);
            }
            return result;
        }

        /// <summary>复制一份外观配置。</summary>
        private CH_AppearanceConfig CopyConfig(CH_AppearanceConfig source)
        {
            CH_AppearanceConfig result = new CH_AppearanceConfig();
            result.SchemaVersion = source.SchemaVersion;
            result.AppearanceId = source.AppearanceId;
            result.DisplayName = source.DisplayName;
            result.PetKind = source.PetKind;
            result.TtsStyle = source.TtsStyle;
            result.RoundSummary = source.RoundSummary;
            result.SummaryAudio = source.SummaryAudio;
            result.IsDefault = source.IsDefault;
            return result;
        }

        /// <summary>把可空文本规范为空字符串。</summary>
        private string SafeText(string? value)
        {
            if (value == null)
            {
                return "";
            }
            return value;
        }
    }
}
