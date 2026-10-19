using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;

namespace Mau.Runtime
{
    /// <summary>
    /// LLM API 配置池与秘密 cfg 的同步明文存储。
    /// 普通配置（llm-api.json）不含 Key 明文；Key 单独存秘密文件（llm-api.cfg）。
    /// </summary>
    public sealed class CH_LlmApiConfigStore
    {

        /// <summary>普通配置文件路径。</summary>
        private readonly string CH_LlmApiConfigStore_ConfigPath;

        /// <summary>秘密 cfg 文件路径。</summary>
        private readonly string CH_LlmApiConfigStore_SecretPath;

        /// <summary>统一 JSON 选项。</summary>
        private readonly JsonSerializerOptions CH_LlmApiConfigStore_JsonOptions;
        /// <summary>配置目录是否可写——false=内存态运行（写入会失败；调用方据此登记启动降级）</summary>
        public bool StorageWritable = true;
        /// <summary>配置目录不可写原因（空=可写）</summary>
        public string StorageError = "";
        /// <summary>最近一次读取失败原因——空=正常（损坏 / 无副本 / 反序列化失败）</summary>
        public string LastLoadError = "";

        /// <summary>读取秘密目录，供产品快捷打开。</summary>
        public string SecretsRoot
        {
            get
            {
                string? root = Path.GetDirectoryName(
                    CH_LlmApiConfigStore_SecretPath);
                if (root == null)
                {
                    return "";
                }
                return root;
            }
        }

        /// <summary>绑定普通 Settings 和独立 Secrets 目录。</summary>
        /// <param name="settingsRoot">Data Root 内普通设置目录</param>
        /// <param name="secretsRoot">独立秘密目录</param>
        public CH_LlmApiConfigStore(string settingsRoot, string secretsRoot)
        {
            if (string.IsNullOrWhiteSpace(settingsRoot)
                || string.IsNullOrWhiteSpace(secretsRoot))
            {
                throw new ArgumentException("LLM API store root is empty.");
            }
            string safeSettingsRoot = Path.GetFullPath(settingsRoot);
            string safeSecretsRoot = Path.GetFullPath(secretsRoot);
            // A260 目录创建失败不阻断启动——标记只读运行，由宿主登记降级
            try
            {
                Directory.CreateDirectory(safeSettingsRoot);
                Directory.CreateDirectory(safeSecretsRoot);
                StorageWritable = true;
            }
            catch (Exception ex)
            {
                StorageWritable = false;
                StorageError = ex.Message;
            }
            CH_LlmApiConfigStore_ConfigPath = Path.Combine(safeSettingsRoot,
                "llm-api.json");
            CH_LlmApiConfigStore_SecretPath = Path.Combine(safeSecretsRoot,
                "llm-api.cfg");
            if (StorageWritable)
            {
                CH_SecretTextFile.DeleteHistory(CH_LlmApiConfigStore_SecretPath);
            }
            CH_LlmApiConfigStore_JsonOptions = new JsonSerializerOptions();
            CH_LlmApiConfigStore_JsonOptions.IncludeFields = true;
            CH_LlmApiConfigStore_JsonOptions.WriteIndented = true;
            CH_LlmApiConfigStore_JsonOptions.Encoder = System.Text.Encodings.Web.JavaScriptEncoder.UnsafeRelaxedJsonEscaping;   // 中文直出（2026-09-08 全局统一）
            CH_LlmApiConfigStore_JsonOptions.PropertyNamingPolicy =
                JsonNamingPolicy.CamelCase;
        }

        /// <summary>读取全部过滤后的普通配置。</summary>
        /// <returns>按 ApiConfigId 排序的独立数组</returns>
        public CH_LlmApiConfig[] GetAll()
        {
            if (!File.Exists(CH_LlmApiConfigStore_ConfigPath))
            {
                return Array.Empty<CH_LlmApiConfig>();
            }
            bool ignoredRecovery;
            string readError;
            string json = CH_AtomicTextFile.ReadWithRecovery(
                CH_LlmApiConfigStore_ConfigPath, IsValidConfigDocument,
                out ignoredRecovery, out readError);
            if (json.Length == 0)
            {
                LastLoadError = readError;
                return Array.Empty<CH_LlmApiConfig>();
            }
            CH_LlmApiConfig[]? loaded;
            try
            {
                loaded = JsonSerializer.Deserialize<
                    CH_LlmApiConfig[]>(json, CH_LlmApiConfigStore_JsonOptions);
            }
            catch (Exception ex)
            {
                LastLoadError = "反序列化失败：" + ex.Message;
                return Array.Empty<CH_LlmApiConfig>();
            }
            if (loaded == null)
            {
                LastLoadError = "配置反序列化为空";
                return Array.Empty<CH_LlmApiConfig>();
            }
            for (int i = 0; i < loaded.Length; i = i + 1)
            {
                NormalizeAndValidate(loaded[i]);
            }
            Array.Sort(loaded, delegate (CH_LlmApiConfig left,
                CH_LlmApiConfig right)
            {
                return left.ApiConfigId.CompareTo(right.ApiConfigId);
            });
            return CopyConfigs(loaded);
        }

        /// <summary>按稳定身份读取一份 API 配置。</summary>
        /// <param name="apiConfigId">API 配置身份</param>
        /// <param name="config">独立配置副本</param>
        /// <returns>是否存在</returns>
        public bool TryGet(Guid apiConfigId, out CH_LlmApiConfig config)
        {
            CH_LlmApiConfig[] configs = GetAll();
            for (int i = 0; i < configs.Length; i = i + 1)
            {
                if (configs[i].ApiConfigId == apiConfigId)
                {
                    config = CopyConfig(configs[i]);
                    return true;
                }
            }
            config = new CH_LlmApiConfig();
            return false;
        }
        /// <summary>
        /// 解析默认端点——IsDefault=true 的配置；无默认返回 null（不做静默回退——空配置必须显式配置端点后才能运行）。
        /// </summary>
        /// <returns>默认配置；无默认 null</returns>
        public CH_LlmApiConfig? ResolveDefault()
        {
            CH_LlmApiConfig[] configs = GetAll();
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
        /// 设置默认端点——清除其他默认标记 + 置目标默认（唯一默认语义；目标不存在返回 false）。
        /// </summary>
        /// <param name="apiConfigId">目标配置身份</param>
        /// <returns>true=设置成功</returns>
        public bool SetDefault(Guid apiConfigId) { CH_LlmApiConfig[] configs = GetAll(); bool found = false; for (int i = 0; i < configs.Length; i = i + 1) { if (configs[i].ApiConfigId == apiConfigId) { configs[i].IsDefault = true; found = true; } else { configs[i].IsDefault = false; } } if (!found) { return false; } SaveConfigs(configs); return true; }


        /// <summary>新增或替换一份普通配置与可选 Key。</summary>
        /// <param name="config">普通配置</param>
        /// <param name="apiKey">Key；空值表示保留原 Key</param>
        public void Save(CH_LlmApiConfig config, string apiKey)
        {
            NormalizeAndValidate(config);
            List<CH_LlmApiConfig> configs = new List<CH_LlmApiConfig>(GetAll());
            bool replaced = false;
            for (int i = 0; i < configs.Count; i = i + 1)
            {
                if (configs[i].ApiConfigId == config.ApiConfigId)
                {
                    configs[i] = CopyConfig(config);
                    replaced = true;
                    break;
                }
            }
            if (!replaced)
            {
                // 池空时首个配置标默认——唯一端点必然被默认消费面使用（之后切换默认走 SetDefault 显式操作）
                if (configs.Count == 0)
                {
                    config.IsDefault = true;
                }
                configs.Add(CopyConfig(config));
            }
            SaveConfigs(configs.ToArray());
            if (!string.IsNullOrWhiteSpace(apiKey))
            {
                Dictionary<Guid, string> secrets = ReadSecrets();
                secrets[config.ApiConfigId] = apiKey.Trim();
                SaveSecrets(secrets);
            }
        }
        /// <summary>读取一份 Key，不把它包装进普通配置。</summary>
        /// <param name="apiConfigId">API 配置身份</param>
        /// <returns>Key 或空字符串</returns>
        public string GetSecret(Guid apiConfigId)
        {
            Dictionary<Guid, string> secrets = ReadSecrets();
            string? secret;
            if (secrets.TryGetValue(apiConfigId, out secret)
                && secret != null)
            {
                return secret;
            }
            return "";
        }

        /// <summary>清除一份 Key 但保留普通 API 配置。</summary>
        /// <param name="apiConfigId">API 配置身份</param>
        public void ClearSecret(Guid apiConfigId)
        {
            Dictionary<Guid, string> secrets = ReadSecrets();
            if (secrets.Remove(apiConfigId))
            {
                SaveSecrets(secrets);
            }
        }

        /// <summary>删除一份 API 配置与对应 Key（M3：默认配置可删——多配置组场景；引用该 ID 的猫回退空配置）。</summary>
        /// <param name="apiConfigId">API 配置身份</param>
        /// <returns>true=存在并删除</returns>
        public bool Delete(Guid apiConfigId)
        {
            List<CH_LlmApiConfig> configs = new List<CH_LlmApiConfig>(GetAll());
            bool removed = false;
            for (int i = configs.Count - 1; i >= 0; i = i - 1)
            {
                if (configs[i].ApiConfigId == apiConfigId)
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
            ClearSecret(apiConfigId);
            return true;
        }

        /// <summary>过滤普通配置并拒绝身份或端点错误。</summary>
        private void NormalizeAndValidate(CH_LlmApiConfig config)
        {
            if (config == null)
            {
                throw new ArgumentNullException("config");
            }
            config.DisplayName = SafeText(config.DisplayName).Trim();
            config.ApiType = SafeText(config.ApiType).Trim().ToLowerInvariant();
            config.Endpoint = SafeText(config.Endpoint).Trim();
            config.DefaultModel = SafeText(config.DefaultModel).Trim();
            if (config.SchemaVersion != 1 || config.ApiConfigId == Guid.Empty
                || config.DisplayName.Length == 0 || config.ApiType.Length == 0
                || config.Endpoint.Length == 0
                || config.DefaultModel.Length == 0)
            {
                throw new InvalidDataException("LLM API config is invalid.");
            }
            Uri? endpoint;
            if (!Uri.TryCreate(config.Endpoint, UriKind.Absolute, out endpoint)
                || endpoint == null)
            {
                throw new InvalidDataException("LLM API endpoint is invalid.");
            }
        }

        /// <summary>读取秘密 cfg 并过滤全部键值。</summary>
        private Dictionary<Guid, string> ReadSecrets()
        {
            Dictionary<Guid, string> result = new Dictionary<Guid, string>();
            if (!File.Exists(CH_LlmApiConfigStore_SecretPath))
            {
                return result;
            }
            string json = CH_SecretTextFile.Read(
                CH_LlmApiConfigStore_SecretPath);
            if (!IsValidSecretDocument(json))
            {
                throw new InvalidDataException(
                    "LLM API secret cfg is invalid.");
            }
            Dictionary<string, string>? source = JsonSerializer.Deserialize<
                Dictionary<string, string>>(json,
                    CH_LlmApiConfigStore_JsonOptions);
            if (source == null)
            {
                throw new InvalidDataException("LLM API secret cfg is null.");
            }
            foreach (KeyValuePair<string, string> item in source)
            {
                Guid id;
                if (!Guid.TryParseExact(item.Key, "N", out id)
                    || string.IsNullOrWhiteSpace(item.Value))
                {
                    throw new InvalidDataException(
                        "LLM API secret cfg contains invalid entry.");
                }
                result.Add(id, item.Value.Trim());
            }
            return result;
        }

        /// <summary>原子保存普通配置数组。</summary>
        private void SaveConfigs(CH_LlmApiConfig[] configs)
        {
            for (int i = 0; i < configs.Length; i = i + 1)
            {
                NormalizeAndValidate(configs[i]);
            }
            string json = JsonSerializer.Serialize(configs,
                CH_LlmApiConfigStore_JsonOptions);
            CH_AtomicTextFile.Write(CH_LlmApiConfigStore_ConfigPath, json);
        }

        /// <summary>原子保存只含 GUID 到 Key 的秘密 cfg。</summary>
        private void SaveSecrets(Dictionary<Guid, string> secrets)
        {
            Dictionary<string, string> document =
                new Dictionary<string, string>(StringComparer.Ordinal);
            Guid[] keys = new Guid[secrets.Count];
            secrets.Keys.CopyTo(keys, 0);
            Array.Sort(keys);
            for (int i = 0; i < keys.Length; i = i + 1)
            {
                document.Add(keys[i].ToString("N"), secrets[keys[i]]);
            }
            string json = JsonSerializer.Serialize(document,
                CH_LlmApiConfigStore_JsonOptions);
            CH_SecretTextFile.Write(CH_LlmApiConfigStore_SecretPath, json);
        }

        /// <summary>验证普通 API 配置文档。</summary>
        private bool IsValidConfigDocument(string content)
        {
            CH_LlmApiConfig[]? configs = JsonSerializer.Deserialize<
                CH_LlmApiConfig[]>(content, CH_LlmApiConfigStore_JsonOptions);
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

        /// <summary>验证秘密 cfg 只含 GUID 到非空 Key 的映射。</summary>
        private bool IsValidSecretDocument(string content)
        {
            Dictionary<string, string>? values = JsonSerializer.Deserialize<
                Dictionary<string, string>>(content,
                    CH_LlmApiConfigStore_JsonOptions);
            if (values == null)
            {
                return false;
            }
            foreach (KeyValuePair<string, string> pair in values)
            {
                Guid id;
                if (!Guid.TryParseExact(pair.Key, "N", out id)
                    || id == Guid.Empty
                    || string.IsNullOrWhiteSpace(pair.Value))
                {
                    return false;
                }
            }
            return true;
        }

        /// <summary>复制普通配置数组。</summary>
        private CH_LlmApiConfig[] CopyConfigs(CH_LlmApiConfig[] source)
        {
            CH_LlmApiConfig[] result = new CH_LlmApiConfig[source.Length];
            for (int i = 0; i < source.Length; i = i + 1)
            {
                result[i] = CopyConfig(source[i]);
            }
            return result;
        }

        /// <summary>复制一份普通配置。</summary>
        private CH_LlmApiConfig CopyConfig(CH_LlmApiConfig source)
        {
            CH_LlmApiConfig result = new CH_LlmApiConfig();
            result.SchemaVersion = source.SchemaVersion;
            result.ApiConfigId = source.ApiConfigId;
            result.DisplayName = source.DisplayName;
            result.ApiType = source.ApiType;
            result.Endpoint = source.Endpoint;
            result.DefaultModel = source.DefaultModel;
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
