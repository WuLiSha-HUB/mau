using System;
using System.Collections.Generic;
using System.IO;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

namespace Mau.Runtime
{
    /// <summary>
    /// HTTP 注入通道配置池与秘密 Key 的同步明文存储（A405）。
    /// 普通配置（channels.json）不含 Key；Key 单独存秘密文件（channels.cfg）——与 QQ 机器人池同构。
    /// 消费面：console 池管理端点（CRUD）+ 通道注入端点（按 Key 反查绑定猫）。
    /// </summary>
    public sealed class CH_ChannelConfigStore
    {
        /// <summary>通道 Key 长度上限（含 ch_ 前缀）。</summary>
        public const int KeyMaxLength = 80;

        /// <summary>通道名长度上限。</summary>
        public const int DisplayNameMaxLength = 32;

        /// <summary>普通配置文件路径。</summary>
        private readonly string CH_ChannelConfigStore_ConfigPath;

        /// <summary>秘密 cfg 文件路径。</summary>
        private readonly string CH_ChannelConfigStore_SecretPath;

        /// <summary>统一 JSON 选项。</summary>
        private readonly JsonSerializerOptions CH_ChannelConfigStore_JsonOptions;

        /// <summary>配置目录是否可写——false=内存态运行（写入会失败；调用方据此登记启动降级）</summary>
        public bool StorageWritable = true;

        /// <summary>配置目录不可写原因（空=可写）</summary>
        public string StorageError = "";

        /// <summary>最近一次读取失败原因——空=正常（损坏 / 无副本 / 反序列化失败）</summary>
        public string LastLoadError = "";

        /// <summary>秘密目录，供产品快捷打开。</summary>
        public string SecretsRoot
        {
            get
            {
                string? root = Path.GetDirectoryName(CH_ChannelConfigStore_SecretPath);
                if (root == null)
                {
                    return "";
                }
                return root;
            }
        }

        /// <summary>绑定普通配置目录与独立秘密目录。</summary>
        /// <param name="settingsRoot">Data Root 内普通设置目录</param>
        /// <param name="secretsRoot">独立秘密目录</param>
        public CH_ChannelConfigStore(string settingsRoot, string secretsRoot)
        {
            if (string.IsNullOrWhiteSpace(settingsRoot)
                || string.IsNullOrWhiteSpace(secretsRoot))
            {
                throw new ArgumentException("Channel store root is empty.");
            }
            string safeSettingsRoot = Path.GetFullPath(settingsRoot);
            string safeSecretsRoot = Path.GetFullPath(secretsRoot);
            // 目录创建失败不阻断启动——标记只读运行，由宿主登记降级
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
            CH_ChannelConfigStore_ConfigPath = Path.Combine(safeSettingsRoot, "channels.json");
            CH_ChannelConfigStore_SecretPath = Path.Combine(safeSecretsRoot, "channels.cfg");
            if (StorageWritable)
            {
                CH_SecretTextFile.DeleteHistory(CH_ChannelConfigStore_SecretPath);
            }
            CH_ChannelConfigStore_JsonOptions = new JsonSerializerOptions();
            CH_ChannelConfigStore_JsonOptions.IncludeFields = true;
            CH_ChannelConfigStore_JsonOptions.WriteIndented = true;
            CH_ChannelConfigStore_JsonOptions.Encoder = System.Text.Encodings.Web.JavaScriptEncoder.UnsafeRelaxedJsonEscaping;
            CH_ChannelConfigStore_JsonOptions.PropertyNamingPolicy = JsonNamingPolicy.CamelCase;
        }

        /// <summary>读取全部通道配置（按通道名排序）。</summary>
        /// <returns>独立副本数组</returns>
        public CH_ChannelConfig[] GetAll()
        {
            if (!File.Exists(CH_ChannelConfigStore_ConfigPath))
            {
                return Array.Empty<CH_ChannelConfig>();
            }
            bool ignoredRecovery;
            string readError;
            string json = CH_AtomicTextFile.ReadWithRecovery(
                CH_ChannelConfigStore_ConfigPath, IsValidConfigDocument,
                out ignoredRecovery, out readError);
            if (json.Length == 0)
            {
                LastLoadError = readError;
                return Array.Empty<CH_ChannelConfig>();
            }
            CH_ChannelConfig[]? loaded;
            try
            {
                loaded = JsonSerializer.Deserialize<CH_ChannelConfig[]>(json, CH_ChannelConfigStore_JsonOptions);
            }
            catch (Exception ex)
            {
                LastLoadError = "反序列化失败：" + ex.Message;
                return Array.Empty<CH_ChannelConfig>();
            }
            if (loaded == null)
            {
                LastLoadError = "配置反序列化为空";
                return Array.Empty<CH_ChannelConfig>();
            }
            for (int i = 0; i < loaded.Length; i = i + 1)
            {
                NormalizeAndValidate(loaded[i]);
            }
            Array.Sort(loaded, delegate (CH_ChannelConfig left, CH_ChannelConfig right)
            {
                return string.CompareOrdinal(left.DisplayName, right.DisplayName);
            });
            return CopyConfigs(loaded);
        }

        /// <summary>按通道身份读取一份配置。</summary>
        /// <param name="channelId">通道身份</param>
        /// <param name="config">独立配置副本</param>
        /// <returns>是否存在</returns>
        public bool TryGet(Guid channelId, out CH_ChannelConfig config)
        {
            CH_ChannelConfig[] configs = GetAll();
            for (int i = 0; i < configs.Length; i = i + 1)
            {
                if (configs[i].ChannelId == channelId)
                {
                    config = CopyConfig(configs[i]);
                    return true;
                }
            }
            config = new CH_ChannelConfig();
            return false;
        }

        /// <summary>
        /// 按 Key 反查通道——常量时间比较（防时序侧信道）；未命中返回 false。
        /// 这是外部程序与猫身份之间唯一的映射点（Key 只传凭证，不传身份）。
        /// </summary>
        /// <param name="key">请求携带的通道 Key</param>
        /// <param name="config">命中的通道配置</param>
        /// <returns>true=Key 有效</returns>
        public bool TryResolveByKey(string key, out CH_ChannelConfig config)
        {
            config = new CH_ChannelConfig();
            if (string.IsNullOrWhiteSpace(key))
            {
                return false;
            }
            byte[] presented = Encoding.UTF8.GetBytes(key.Trim());
            CH_ChannelConfig[] configs = GetAll();
            Dictionary<Guid, string> secrets = ReadSecrets();
            for (int i = 0; i < configs.Length; i = i + 1)
            {
                string? stored;
                if (!secrets.TryGetValue(configs[i].ChannelId, out stored)
                    || string.IsNullOrWhiteSpace(stored))
                {
                    continue;
                }
                if (CryptographicOperations.FixedTimeEquals(presented, Encoding.UTF8.GetBytes(stored)))
                {
                    config = configs[i];
                    return true;
                }
            }
            return false;
        }

        /// <summary>读取一份通道 Key（管理面重发用）。</summary>
        /// <param name="channelId">通道身份</param>
        /// <returns>Key 明文或空串</returns>
        public string GetKey(Guid channelId)
        {
            Dictionary<Guid, string> secrets = ReadSecrets();
            string? key;
            if (secrets.TryGetValue(channelId, out key) && key != null)
            {
                return key;
            }
            return "";
        }

        /// <summary>新增或替换一份通道配置与可选 Key（Key 空=保留原值）。</summary>
        /// <param name="config">通道配置</param>
        /// <param name="key">新 Key；空=保留原 Key</param>
        public void Save(CH_ChannelConfig config, string key)
        {
            NormalizeAndValidate(config);
            List<CH_ChannelConfig> configs = new List<CH_ChannelConfig>(GetAll());
            // 通道名唯一（忽略大小写；排除自身）——重复名会让注入来源无法追溯
            for (int i = 0; i < configs.Count; i = i + 1)
            {
                if (configs[i].ChannelId != config.ChannelId
                    && string.Equals(configs[i].DisplayName, config.DisplayName, StringComparison.OrdinalIgnoreCase))
                {
                    throw new InvalidDataException("通道名已存在：" + config.DisplayName);
                }
            }
            bool replaced = false;
            for (int i = 0; i < configs.Count; i = i + 1)
            {
                if (configs[i].ChannelId == config.ChannelId)
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
            if (!string.IsNullOrWhiteSpace(key))
            {
                string normalized = NormalizeKey(key);
                Dictionary<Guid, string> secrets = ReadSecrets();
                secrets[config.ChannelId] = normalized;
                SaveSecrets(secrets);
            }
        }

        /// <summary>重置一份通道 Key 并返回新值（旧 Key 立即失效）。</summary>
        /// <param name="channelId">通道身份</param>
        /// <param name="key">新 Key（失败空串）</param>
        /// <returns>true=已重置</returns>
        public bool RotateKey(Guid channelId, out string key)
        {
            key = "";
            CH_ChannelConfig config;
            if (!TryGet(channelId, out config))
            {
                return false;
            }
            string fresh = GenerateKey();
            Save(config, fresh);
            key = fresh;
            return true;
        }

        /// <summary>删除一份通道配置与对应 Key。</summary>
        /// <param name="channelId">通道身份</param>
        /// <returns>true=存在并删除</returns>
        public bool Delete(Guid channelId)
        {
            List<CH_ChannelConfig> configs = new List<CH_ChannelConfig>(GetAll());
            bool removed = false;
            for (int i = configs.Count - 1; i >= 0; i = i - 1)
            {
                if (configs[i].ChannelId == channelId)
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
            Dictionary<Guid, string> secrets = ReadSecrets();
            if (secrets.Remove(channelId))
            {
                SaveSecrets(secrets);
            }
            return true;
        }

        /// <summary>生成通道 Key——随机 32 字节 hex（前缀 ch_）；禁语义化。</summary>
        /// <returns>新 Key</returns>
        public static string GenerateKey()
        {
            byte[] bytes = new byte[32];
            RandomNumberGenerator.Fill(bytes);
            return "ch_" + Convert.ToHexString(bytes).ToLowerInvariant();
        }

        /// <summary>把可空文本规范为空字符串。</summary>
        /// <param name="value">原始文本（可 null）</param>
        /// <returns>非 null 文本</returns>
        private static string SafeText(string? value)
        {
            if (value == null)
            {
                return "";
            }
            return value;
        }

        /// <summary>归一 Key——Trim + 长度与空白校验（非法即抛，不静默回落）。</summary>
        /// <param name="key">原始 Key</param>
        /// <returns>归一后的 Key</returns>
        private static string NormalizeKey(string key)
        {
            string trimmed = SafeText(key).Trim();
            if (trimmed.Length == 0 || trimmed.Length > KeyMaxLength)
            {
                throw new InvalidDataException("通道 Key 长度非法（1.." + KeyMaxLength.ToString() + "）。");
            }
            for (int i = 0; i < trimmed.Length; i = i + 1)
            {
                if (char.IsWhiteSpace(trimmed[i]))
                {
                    throw new InvalidDataException("通道 Key 不得含空白字符。");
                }
            }
            return trimmed;
        }

        /// <summary>归一与校验通道配置——身份 / 通道名必填；CatKey 与 BotKey 至少一项（注入或播报，A406 起 CatKey 可空）；schema 1..2 均接受（读出即升 2）。</summary>
        /// <param name="config">通道配置</param>
        private static void NormalizeAndValidate(CH_ChannelConfig config)
        {
            if (config == null)
            {
                throw new ArgumentNullException("config");
            }
            config.DisplayName = SafeText(config.DisplayName).Trim();
            config.CatKey = SafeText(config.CatKey).Trim();
            config.BotKey = SafeText(config.BotKey).Trim();
            config.Groups = NormalizeGroups(config.Groups);
            if (config.SchemaVersion < 1
                || config.SchemaVersion > 2
                || config.ChannelId == Guid.Empty
                || config.DisplayName.Length == 0
                || config.DisplayName.Length > DisplayNameMaxLength)
            {
                throw new InvalidDataException("通道配置非法（身份 / 通道名必填，通道名 ≤" + DisplayNameMaxLength.ToString() + "）。");
            }
            // A406——CatKey 可空（纯播报通道）；但两项能力至少留一项，否则通道存在却什么都做不了
            if (config.CatKey.Length == 0 && config.BotKey.Length == 0)
            {
                throw new InvalidDataException("通道配置非法（至少保留一项能力：绑定猫=注入，或出口机器人=播报）。");
            }
            // 旧档（schema 1）无出口字段，读作空（= 仅注入）；写出恒为 2
            config.SchemaVersion = 2;
        }

        /// <summary>
        /// 归一群白名单（A406）——逐项 Trim、去空白项、忽略大小写去重（保序）。
        /// </summary>
        /// <param name="groups">原始群白名单（可 null）</param>
        /// <returns>非 null 白名单数组</returns>
        private static string[] NormalizeGroups(string[]? groups)
        {
            if (groups == null || groups.Length == 0)
            {
                return Array.Empty<string>();
            }
            List<string> result = new List<string>();
            for (int i = 0; i < groups.Length; i = i + 1)
            {
                string item = SafeText(groups[i]).Trim();
                if (item.Length == 0)
                {
                    continue;
                }
                bool duplicated = false;
                for (int j = 0; j < result.Count; j = j + 1)
                {
                    if (string.Equals(result[j], item, StringComparison.OrdinalIgnoreCase))
                    {
                        duplicated = true;
                        break;
                    }
                }
                if (!duplicated)
                {
                    result.Add(item);
                }
            }
            return result.ToArray();
        }

        /// <summary>读取秘密 cfg 并过滤全部键值对。</summary>
        /// <returns>通道身份 → Key</returns>
        private Dictionary<Guid, string> ReadSecrets()
        {
            Dictionary<Guid, string> result = new Dictionary<Guid, string>();
            if (!File.Exists(CH_ChannelConfigStore_SecretPath))
            {
                return result;
            }
            string json = CH_SecretTextFile.Read(CH_ChannelConfigStore_SecretPath);
            if (!IsValidSecretDocument(json))
            {
                throw new InvalidDataException("通道 secret cfg 非法。");
            }
            Dictionary<string, string>? source = JsonSerializer.Deserialize<Dictionary<string, string>>(
                json, CH_ChannelConfigStore_JsonOptions);
            if (source == null)
            {
                throw new InvalidDataException("通道 secret cfg 为空。");
            }
            foreach (KeyValuePair<string, string> item in source)
            {
                Guid id;
                if (!Guid.TryParseExact(item.Key, "N", out id) || string.IsNullOrWhiteSpace(item.Value))
                {
                    throw new InvalidDataException("通道 secret cfg 含非法条目。");
                }
                result.Add(id, item.Value.Trim());
            }
            return result;
        }

        /// <summary>原子保存普通配置数组。</summary>
        /// <param name="configs">通道配置数组</param>
        private void SaveConfigs(CH_ChannelConfig[] configs)
        {
            for (int i = 0; i < configs.Length; i = i + 1)
            {
                NormalizeAndValidate(configs[i]);
            }
            string json = JsonSerializer.Serialize(configs, CH_ChannelConfigStore_JsonOptions);
            CH_AtomicTextFile.Write(CH_ChannelConfigStore_ConfigPath, json);
        }

        /// <summary>原子保存只含身份到 Key 的秘密 cfg。</summary>
        /// <param name="secrets">通道身份 → Key</param>
        private void SaveSecrets(Dictionary<Guid, string> secrets)
        {
            Dictionary<string, string> document = new Dictionary<string, string>(StringComparer.Ordinal);
            Guid[] keys = new Guid[secrets.Count];
            secrets.Keys.CopyTo(keys, 0);
            Array.Sort(keys);
            for (int i = 0; i < keys.Length; i = i + 1)
            {
                document.Add(keys[i].ToString("N"), secrets[keys[i]]);
            }
            string json = JsonSerializer.Serialize(document, CH_ChannelConfigStore_JsonOptions);
            CH_SecretTextFile.Write(CH_ChannelConfigStore_SecretPath, json);
        }

        /// <summary>验证普通通道配置文档。</summary>
        /// <param name="content">文件原文</param>
        /// <returns>true=可接受</returns>
        private bool IsValidConfigDocument(string content)
        {
            CH_ChannelConfig[]? configs = JsonSerializer.Deserialize<CH_ChannelConfig[]>(
                content, CH_ChannelConfigStore_JsonOptions);
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

        /// <summary>验证秘密 cfg 只含身份到非空 Key 的映射。</summary>
        /// <param name="content">文件原文</param>
        /// <returns>true=可接受</returns>
        private bool IsValidSecretDocument(string content)
        {
            Dictionary<string, string>? values = JsonSerializer.Deserialize<Dictionary<string, string>>(
                content, CH_ChannelConfigStore_JsonOptions);
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

        /// <summary>复制通道配置数组。</summary>
        /// <param name="source">来源数组</param>
        /// <returns>独立副本</returns>
        private static CH_ChannelConfig[] CopyConfigs(CH_ChannelConfig[] source)
        {
            CH_ChannelConfig[] result = new CH_ChannelConfig[source.Length];
            for (int i = 0; i < source.Length; i = i + 1)
            {
                result[i] = CopyConfig(source[i]);
            }
            return result;
        }

        /// <summary>复制一份通道配置。</summary>
        /// <param name="source">来源配置</param>
        /// <returns>独立副本</returns>
        private static CH_ChannelConfig CopyConfig(CH_ChannelConfig source)
        {
            CH_ChannelConfig result = new CH_ChannelConfig();
            result.SchemaVersion = source.SchemaVersion;
            result.ChannelId = source.ChannelId;
            result.DisplayName = source.DisplayName;
            result.CatKey = source.CatKey;
            result.BotKey = SafeText(source.BotKey);
            result.Groups = CopyGroups(source.Groups);
            return result;
        }

        /// <summary>复制群白名单数组（A406——数组按引用共享会让读面副本被调用方改坏存储态）。</summary>
        /// <param name="source">来源数组（可 null）</param>
        /// <returns>独立副本</returns>
        private static string[] CopyGroups(string[]? source)
        {
            if (source == null || source.Length == 0)
            {
                return Array.Empty<string>();
            }
            string[] result = new string[source.Length];
            for (int i = 0; i < source.Length; i = i + 1)
            {
                result[i] = SafeText(source[i]);
            }
            return result;
        }
    }
}
