using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;

namespace Mau.Runtime
{
    /// <summary>
    /// QQ Bot 配置池与秘密 cfg 的同步明文存储。
    /// 普通配置（qqbot.json）不含 Secret 明文；Secret 单独存秘密文件（qqbot.cfg）。
    /// 对齐 CH_LlmApiConfigStore（M1）模式——无默认端点语义（QQ Bot 无"默认"概念，每猫显式选）。
    /// </summary>
    public sealed class CH_QqBotConfigStore
    {
        /// <summary>普通配置文件路径。</summary>
        private readonly string CH_QqBotConfigStore_ConfigPath;

        /// <summary>秘密 cfg 文件路径。</summary>
        private readonly string CH_QqBotConfigStore_SecretPath;

        /// <summary>统一 JSON 选项。</summary>
        private readonly JsonSerializerOptions CH_QqBotConfigStore_JsonOptions;
        /// <summary>配置目录是否可写——false=内存态运行（调用方据此登记启动降级）</summary>
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
                    CH_QqBotConfigStore_SecretPath);
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
        public CH_QqBotConfigStore(string settingsRoot, string secretsRoot)
        {
            if (string.IsNullOrWhiteSpace(settingsRoot)
                || string.IsNullOrWhiteSpace(secretsRoot))
            {
                throw new ArgumentException("QQ Bot store root is empty.");
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
            CH_QqBotConfigStore_ConfigPath = Path.Combine(safeSettingsRoot,
                "qqbot.json");
            CH_QqBotConfigStore_SecretPath = Path.Combine(safeSecretsRoot,
                "qqbot.cfg");
            if (StorageWritable)
            {
                CH_SecretTextFile.DeleteHistory(CH_QqBotConfigStore_SecretPath);
            }
            CH_QqBotConfigStore_JsonOptions = new JsonSerializerOptions();
            CH_QqBotConfigStore_JsonOptions.IncludeFields = true;
            CH_QqBotConfigStore_JsonOptions.WriteIndented = true;
            CH_QqBotConfigStore_JsonOptions.Encoder = System.Text.Encodings.Web.JavaScriptEncoder.UnsafeRelaxedJsonEscaping;   // 中文直出（2026-09-08 全局统一）
            CH_QqBotConfigStore_JsonOptions.PropertyNamingPolicy =
                JsonNamingPolicy.CamelCase;
        }

        /// <summary>读取全部过滤后的普通配置。</summary>
        /// <returns>按 QqBotId 排序的独立数组</returns>
        public CH_QqBotConfig[] GetAll()
        {
            if (!File.Exists(CH_QqBotConfigStore_ConfigPath))
            {
                return Array.Empty<CH_QqBotConfig>();
            }
            bool ignoredRecovery;
            string readError;
            string json = CH_AtomicTextFile.ReadWithRecovery(
                CH_QqBotConfigStore_ConfigPath, IsValidConfigDocument,
                out ignoredRecovery, out readError);
            if (json.Length == 0)
            {
                LastLoadError = readError;
                return Array.Empty<CH_QqBotConfig>();
            }
            CH_QqBotConfig[]? loaded;
            try
            {
                loaded = JsonSerializer.Deserialize<
                    CH_QqBotConfig[]>(json, CH_QqBotConfigStore_JsonOptions);
            }
            catch (Exception ex)
            {
                LastLoadError = "反序列化失败：" + ex.Message;
                return Array.Empty<CH_QqBotConfig>();
            }
            if (loaded == null)
            {
                LastLoadError = "配置反序列化为空";
                return Array.Empty<CH_QqBotConfig>();
            }
            for (int i = 0; i < loaded.Length; i = i + 1)
            {
                NormalizeAndValidate(loaded[i]);
            }
            Array.Sort(loaded, delegate (CH_QqBotConfig left,
                CH_QqBotConfig right)
            {
                return left.QqBotId.CompareTo(right.QqBotId);
            });
            return CopyConfigs(loaded);
        }

        /// <summary>按稳定身份读取一份 QQ Bot 配置。</summary>
        /// <param name="qqBotId">QQ Bot 配置身份</param>
        /// <param name="config">独立配置副本</param>
        /// <returns>是否存在</returns>
        public bool TryGet(Guid qqBotId, out CH_QqBotConfig config)
        {
            CH_QqBotConfig[] configs = GetAll();
            for (int i = 0; i < configs.Length; i = i + 1)
            {
                if (configs[i].QqBotId == qqBotId)
                {
                    config = CopyConfig(configs[i]);
                    return true;
                }
            }
            config = new CH_QqBotConfig();
            return false;
        }

        /// <summary>新增或替换一份普通配置与可选 Secret。</summary>
        /// <param name="config">普通配置</param>
        /// <param name="secret">Secret；空值表示保留原 Secret</param>
        public void Save(CH_QqBotConfig config, string secret)
        {
            NormalizeAndValidate(config);
            List<CH_QqBotConfig> configs = new List<CH_QqBotConfig>(GetAll());
            bool replaced = false;
            for (int i = 0; i < configs.Count; i = i + 1)
            {
                if (configs[i].QqBotId == config.QqBotId)
                {
                    configs[i] = CopyConfig(config);
                    replaced = true;
                    break;
                }
            }
            if (!replaced)
            {
                configs.Add(CopyConfig(config));
            }
            SaveConfigs(configs.ToArray());
            if (!string.IsNullOrWhiteSpace(secret))
            {
                Dictionary<Guid, string> secrets = ReadSecrets();
                secrets[config.QqBotId] = secret.Trim();
                SaveSecrets(secrets);
            }
        }

        /// <summary>读取一份 Secret，不把它包装进普通配置。</summary>
        /// <param name="qqBotId">QQ Bot 配置身份</param>
        /// <returns>Secret 或空字符串</returns>
        public string GetSecret(Guid qqBotId)
        {
            Dictionary<Guid, string> secrets = ReadSecrets();
            string? secret;
            if (secrets.TryGetValue(qqBotId, out secret)
                && secret != null)
            {
                return secret;
            }
            return "";
        }

        /// <summary>清除一份 Secret 但保留普通 QQ Bot 配置。</summary>
        /// <param name="qqBotId">QQ Bot 配置身份</param>
        public void ClearSecret(Guid qqBotId)
        {
            Dictionary<Guid, string> secrets = ReadSecrets();
            if (secrets.Remove(qqBotId))
            {
                SaveSecrets(secrets);
            }
        }

        /// <summary>删除一份 QQ Bot 配置与对应 Secret。</summary>
        /// <param name="qqBotId">QQ Bot 配置身份</param>
        /// <returns>true=存在并删除</returns>
        public bool Delete(Guid qqBotId)
        {
            List<CH_QqBotConfig> configs = new List<CH_QqBotConfig>(GetAll());
            bool removed = false;
            for (int i = configs.Count - 1; i >= 0; i = i - 1)
            {
                if (configs[i].QqBotId == qqBotId)
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
            ClearSecret(qqBotId);
            return true;
        }

        /// <summary>过滤普通配置并拒绝身份或 AppId 错误。</summary>
        private void NormalizeAndValidate(CH_QqBotConfig config)
        {
            if (config == null)
            {
                throw new ArgumentNullException("config");
            }
            config.DisplayName = SafeText(config.DisplayName).Trim();
            config.AppId = SafeText(config.AppId).Trim();
            if (config.SchemaVersion != 1 || config.QqBotId == Guid.Empty
                || config.DisplayName.Length == 0 || config.AppId.Length == 0)
            {
                throw new InvalidDataException("QQ Bot config is invalid.");
            }
        }

        /// <summary>读取秘密 cfg 并过滤全部键值。</summary>
        private Dictionary<Guid, string> ReadSecrets()
        {
            Dictionary<Guid, string> result = new Dictionary<Guid, string>();
            if (!File.Exists(CH_QqBotConfigStore_SecretPath))
            {
                return result;
            }
            string json = CH_SecretTextFile.Read(
                CH_QqBotConfigStore_SecretPath);
            if (!IsValidSecretDocument(json))
            {
                throw new InvalidDataException(
                    "QQ Bot secret cfg is invalid.");
            }
            Dictionary<string, string>? source = JsonSerializer.Deserialize<
                Dictionary<string, string>>(json,
                    CH_QqBotConfigStore_JsonOptions);
            if (source == null)
            {
                throw new InvalidDataException("QQ Bot secret cfg is null.");
            }
            foreach (KeyValuePair<string, string> item in source)
            {
                Guid id;
                if (!Guid.TryParseExact(item.Key, "N", out id)
                    || string.IsNullOrWhiteSpace(item.Value))
                {
                    throw new InvalidDataException(
                        "QQ Bot secret cfg contains invalid entry.");
                }
                result.Add(id, item.Value.Trim());
            }
            return result;
        }

        /// <summary>原子保存普通配置数组。</summary>
        private void SaveConfigs(CH_QqBotConfig[] configs)
        {
            for (int i = 0; i < configs.Length; i = i + 1)
            {
                NormalizeAndValidate(configs[i]);
            }
            string json = JsonSerializer.Serialize(configs,
                CH_QqBotConfigStore_JsonOptions);
            CH_AtomicTextFile.Write(CH_QqBotConfigStore_ConfigPath, json);
        }

        /// <summary>原子保存只含 GUID 到 Secret 的秘密 cfg。</summary>
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
                CH_QqBotConfigStore_JsonOptions);
            CH_SecretTextFile.Write(CH_QqBotConfigStore_SecretPath, json);
        }

        /// <summary>验证普通 QQ Bot 配置文档。</summary>
        private bool IsValidConfigDocument(string content)
        {
            CH_QqBotConfig[]? configs = JsonSerializer.Deserialize<
                CH_QqBotConfig[]>(content, CH_QqBotConfigStore_JsonOptions);
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

        /// <summary>验证秘密 cfg 只含 GUID 到非空 Secret 的映射。</summary>
        private bool IsValidSecretDocument(string content)
        {
            Dictionary<string, string>? values = JsonSerializer.Deserialize<
                Dictionary<string, string>>(content,
                    CH_QqBotConfigStore_JsonOptions);
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
        private CH_QqBotConfig[] CopyConfigs(CH_QqBotConfig[] source)
        {
            CH_QqBotConfig[] result = new CH_QqBotConfig[source.Length];
            for (int i = 0; i < source.Length; i = i + 1)
            {
                result[i] = CopyConfig(source[i]);
            }
            return result;
        }

        /// <summary>复制一份普通配置。</summary>
        private CH_QqBotConfig CopyConfig(CH_QqBotConfig source)
        {
            CH_QqBotConfig result = new CH_QqBotConfig();
            result.SchemaVersion = source.SchemaVersion;
            result.QqBotId = source.QqBotId;
            result.DisplayName = source.DisplayName;
            result.AppId = source.AppId;
            result.Sandbox = source.Sandbox;
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
