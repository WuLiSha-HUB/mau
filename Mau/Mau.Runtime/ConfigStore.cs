using System;
using System.Collections.Generic;
using System.IO;

namespace Mau.Runtime
{
    /// <summary>
    /// 配置存储——通用键值配置（文本资产 + 防御式读写）。
    /// 文件格式：每行 key=value；# 开头为注释；空行跳过。
    /// 缺文件 → 空配置（Get 返回默认值）；坏行 → 跳过。
    /// 写入 → 原子写（临时文件 + 改名），避免半写文件被读到。
    /// 使用方通过 DataBox.Bind&lt;ConfigStore&gt; 挂载为程序级服务。
    /// </summary>
    public sealed class ConfigStore
    {
        // [段1] 字段
        /// <summary>
        /// 配置表——键 → 值（Ordinal 比较）
        /// </summary>
        private readonly Dictionary<string, string> _values = new Dictionary<string, string>(StringComparer.Ordinal);

        /// <summary>
        /// 配置表锁——读写互斥
        /// </summary>
        private readonly object _gate = new object();

        /// <summary>
        /// 当前文件路径（空 = 未关联文件）
        /// </summary>
        private string _path = "";
        /// <summary>
        /// 多文件槽——键前缀段 → 文件路径（ui. → ui.cfg；无前缀段命中 → 主文件 _path）。P8.5d 配置群多文件化
        /// </summary>
        private readonly Dictionary<string, string> _fileByPrefix = new Dictionary<string, string>(StringComparer.Ordinal);
        // [段2] 构造与加载
        /// <summary>
        /// 构造空配置存储
        /// </summary>
        public ConfigStore()
        {
        }

        /// <summary>
        /// 从文件加载配置——文件不存在返回空配置；坏行跳过
        /// </summary>
        /// <param name="path">配置文件路径</param>
        /// <returns>配置存储（已关联文件路径）</returns>
        public static ConfigStore Load(string path)
        {
            ConfigStore store = new ConfigStore();
            if (path == null || path.Length == 0)
            {
                return store;
            }
            store._path = path;
            if (!File.Exists(path))
            {
                return store;
            }
            string[] lines = File.ReadAllLines(path);
            for (int i = 0; i < lines.Length; i = i + 1)
            {
                string line = lines[i].Trim();
                if (line.Length == 0 || line.StartsWith("#"))
                {
                    continue;
                }
                int eq = line.IndexOf('=');
                if (eq <= 0)
                {
                    continue;
                }
                string key = line.Substring(0, eq).Trim();
                string value = line.Substring(eq + 1).Trim();
                if (key.Length == 0)
                {
                    continue;
                }
                lock (store._gate)
                {
                    store._values[key] = value;
                }
            }
            return store;
        }
        /// <summary>
        /// 追加配置文件槽——键前缀段路由（如 "ui" → ui.cfg）；装载该文件全部键值并注册前缀映射。
        /// P8.5d 配置群多文件化：单一 ConfigStore 承载多配置文件，Set/Save 按键前缀段分组落盘。
        /// </summary>
        /// <param name="prefix">键前缀段（键 "ui.chat_font_size" 的前缀 "ui"）</param>
        /// <param name="path">配置文件路径</param>
        public void AddFile(string prefix, string path)
        {
            if (prefix == null || prefix.Length == 0)
            {
                throw new ArgumentException("ConfigStore file prefix is empty.", "prefix");
            }

            if (path == null || path.Length == 0)
            {
                throw new ArgumentException("ConfigStore file path is empty.", "path");
            }

            lock (_gate)
            {
                if (File.Exists(path))
                {
                    string[] lines = File.ReadAllLines(path);
                    for (int i = 0; i < lines.Length; i = i + 1)
                    {
                        string line = lines[i].Trim();
                        if (line.Length == 0 || line.StartsWith("#"))
                        {
                            continue;
                        }

                        int eq = line.IndexOf('=');
                        if (eq <= 0)
                        {
                            continue;
                        }

                        string key = line.Substring(0, eq).Trim();
                        string value = line.Substring(eq + 1).Trim();
                        if (key.Length == 0)
                        {
                            continue;
                        }

                        _values[key] = value;
                    }
                }

                _fileByPrefix[prefix] = path;
            }
        }
        // [段3] 读取
        /// <summary>
        /// 读取配置值——不存在返回默认值
        /// </summary>
        /// <param name="key">键</param>
        /// <param name="defaultValue">默认值</param>
        /// <returns>配置值或默认值</returns>
        public string Get(string key, string defaultValue)
        {
            lock (_gate)
            {
                string? found;
                if (_values.TryGetValue(key, out found) && found != null)
                {
                    return found;
                }
                return defaultValue;
            }
        }

        /// <summary>
        /// 尝试读取配置值
        /// </summary>
        /// <param name="key">键</param>
        /// <param name="value">配置值（不存在为默认）</param>
        /// <returns>是否存在</returns>
        public bool TryGet(string key, out string value)
        {
            lock (_gate)
            {
                string? found;
                if (_values.TryGetValue(key, out found) && found != null)
                {
                    value = found;
                    return true;
                }
                value = "";
                return false;
            }
        }

        // [段4] 写入
        /// <summary>
        /// 设置配置值——覆盖已有值（内存生效，Save 落盘）
        /// </summary>
        /// <param name="key">键</param>
        /// <param name="value">值</param>
        public void Set(string key, string value)
        {
            if (key == null || key.Length == 0)
            {
                throw new ArgumentException("ConfigStore key is empty.", "key");
            }
            if (value == null)
            {
                throw new ArgumentException("ConfigStore value is null.", "value");
            }
            lock (_gate)
            {
                _values[key] = value;
                if (Audit != null)
                {
                    Audit.Record("ConfigStore", "cfg.change", -1, new AuditProp[] {
                        new AuditProp("key", key),
                        new AuditProp("value", AuditStore.Summarize(value))
                    });
                }
            }
        }
        /// <summary>
        /// 提取键前缀段——"ui.chat_font_size" → "ui"；无句点或空前缀返回空串（归主文件）
        /// </summary>
        /// <param name="key">配置键</param>
        /// <returns>前缀段</returns>
        private static string KeyPrefix(string key)
        {
            int dot = key.IndexOf('.');
            if (dot <= 0)
            {
                return "";
            }

            return key.Substring(0, dot);
        }
        // [段5] 持久化
        /// <summary>
        /// 保存到当前文件路径——未关联路径时不动作
        /// </summary>
        public void Save()
        {
            lock (_gate)
            {
                // [段1] 每个注册文件各建一个缓冲（主文件 + 各前缀段文件；同路径去重）
                Dictionary<string, System.Text.StringBuilder> builders = new Dictionary<string, System.Text.StringBuilder>(StringComparer.Ordinal);
                if (_path.Length > 0)
                {
                    builders[_path] = new System.Text.StringBuilder();
                }
                foreach (KeyValuePair<string, string> pair in _fileByPrefix)
                {
                    if (!builders.ContainsKey(pair.Value))
                    {
                        builders[pair.Value] = new System.Text.StringBuilder();
                    }
                }
                // [段2] 逐键归位——前缀段命中 → 该文件；否则主文件
                KeyValuePair<string, string>[] pairs = All();
                for (int i = 0; i < pairs.Length; i = i + 1)
                {
                    string key = pairs[i].Key;
                    string prefix = KeyPrefix(key);
                    string target = "";
                    string? mapped = "";
                    if (prefix.Length > 0 && _fileByPrefix.TryGetValue(prefix, out mapped) && mapped != null)
                    {
                        target = mapped;
                    }
                    else if (_path.Length > 0)
                    {
                        target = _path;
                    }
                    if (target.Length == 0 || !builders.ContainsKey(target))
                    {
                        continue;
                    }
                    System.Text.StringBuilder sb = builders[target];
                    sb.Append(key);
                    sb.Append('=');
                    sb.Append(pairs[i].Value);
                    sb.AppendLine();
                }
                // [段3] 逐文件原子写（空内容 = 清空该文件——移除键后不留残留）
                foreach (KeyValuePair<string, System.Text.StringBuilder> pair2 in builders)
                {
                    if (pair2.Value.Length == 0 && !File.Exists(pair2.Key))
                    {
                        continue;
                    }
                    AtomicWrite(pair2.Key, pair2.Value.ToString());
                }
            }
        }
        /// <summary>
        /// 保存到指定路径——原子写（临时文件 + 改名）
        /// </summary>
        /// <param name="path">目标路径</param>
        public void SaveTo(string path)
        {
            if (path == null || path.Length == 0)
            {
                throw new ArgumentException("ConfigStore path is empty.", "path");
            }
            lock (_gate)
            {
                string? dir = Path.GetDirectoryName(path);
                if (dir != null && dir.Length > 0)
                {
                    Directory.CreateDirectory(dir);
                }
                System.Text.StringBuilder sb = new System.Text.StringBuilder();
                KeyValuePair<string, string>[] pairs = All();
                for (int i = 0; i < pairs.Length; i = i + 1)
                {
                    sb.Append(pairs[i].Key);
                    sb.Append('=');
                    sb.Append(pairs[i].Value);
                    sb.AppendLine();
                }
                string tmp = path + ".tmp";
                File.WriteAllText(tmp, sb.ToString());
                File.Move(tmp, path, true);
            }
        }

        // [段6] 快照
        /// <summary>
        /// 全部键值对——快照（只读拷贝）
        /// </summary>
        /// <returns>键值对数组</returns>
        public KeyValuePair<string, string>[] All()
        {
            lock (_gate)
            {
                KeyValuePair<string, string>[] copy = new KeyValuePair<string, string>[_values.Count];
                int i = 0;
                foreach (KeyValuePair<string, string> pair in _values)
                {
                    copy[i] = pair;
                    i = i + 1;
                }
                return copy;
            }
        }
        /// <summary>
        /// 审计存储——宿主注入后配置变更事件写入（null = 不审计）。零业务侵入：仅记录，不改流程。
        /// </summary>
        /// <summary>
        /// 审计存储——宿主注入后配置变更事件写入（null = 不审计）。零业务侵入：仅记录，不改流程。
        /// </summary>
        public AuditStore? Audit
        {
            get;
            set;
        }

        /// <summary>
        /// 原子写文件——临时文件 + 改名（防半写文件被读到；统一实现——AppRegistry/DogBase/FileSystemService 原四处独立，审查修复轮 2026-08-11 决策3 收拢）
        /// </summary>
        /// <param name="path">目标路径</param>
        /// <param name="content">内容</param>
        /// <param name="encoding">编码（默认 UTF-8 无 BOM）</param>
        public static void AtomicWrite(string path, string content, System.Text.Encoding? encoding = null)
        {
            string? dir = Path.GetDirectoryName(path);
            if (dir != null && dir.Length > 0)
            {
                Directory.CreateDirectory(dir);
            }
            string tmp = path + ".tmp";
            System.Text.Encoding effectiveEncoding;
            if (encoding == null)
            {
                effectiveEncoding = new System.Text.UTF8Encoding(false);
            }
            else
            {
                effectiveEncoding = encoding;
            }
            System.IO.File.WriteAllText(tmp, content, effectiveEncoding);
            System.IO.File.Move(tmp, path, true);
        }
        /// <summary>
        /// 受控配置写入——P8.5d 自改通道唯一实现（serve POST / config 积木 / 宿主直执共用）。
        /// 校验链：写死防御（键名 / 长度 / 掩码）→ 旧值快照 → 写入落盘 → 失败回滚（A364 起无声明面 / 白名单 / 值域校验）。
        /// </summary>
        /// <param name="key">配置键（带文件前缀：ui.chat_font_size）</param>
        /// <param name="value">新值</param>
        /// <param name="error">失败原因（成功为空串）</param>
        /// <returns>是否成功</returns>
        public bool SetChecked(string key, string value, out string error)
        {
            error = "";
            // [段1] 写死防御——键名 / 长度 / 掩码（纯 KV，无声明面，一次性校验）
            if (key == null || key.Length == 0)
            {
                error = "key 为空";
                return false;
            }

            if (key.Length > 128)
            {
                error = "key 过长（>128）: " + key;
                return false;
            }

            if (!IsSimpleKey(key))
            {
                error = "key 只允许字母 / 数字 / 下划线 / 点: " + key;
                return false;
            }

            if (value == null)
            {
                error = "value 为空";
                return false;
            }

            if (value.Length > 1024)
            {
                error = "配置值过长（>1024）";
                return false;
            }

            if (value.IndexOf("****", StringComparison.Ordinal) >= 0)
            {
                error = "value 含掩码标记——请输入真实值";
                return false;
            }

            // [段2] 旧值快照 + 写入 + 失败回滚（写入异常时内存与磁盘一致恢复）
            bool hadOld;
            string? oldValue = "";
            lock (_gate)
            {
                hadOld = _values.TryGetValue(key, out oldValue);
            }

            try
            {
                Set(key, value);
                Save();
                return true;
            }
            catch (Exception ex)
            {
                try
                {
                    lock (_gate)
                    {
                        if (hadOld && oldValue != null)
                        {
                            _values[key] = oldValue;
                        }
                        else
                        {
                            _values.Remove(key);
                        }
                    }

                    Save();
                }
                catch (Exception rollbackEx)
                {
                    // 回滚再失败——内存保持一致，磁盘差异交由审计提示
                    LogStore.Add("ConfigStore", 2, "回滚再失败（内存一致/磁盘差异）: " + rollbackEx.Message, "SYS");
                }

                error = "写入失败: " + ex.Message;
                return false;
            }
        }
        /// <summary>
        /// 配置还原默认——纯 KV 语义：key 空 = 清空全部落盘值（全部回到未设置，消费侧硬编码默认生效）；
        /// key 非空 = 清除该键落盘值。无 schema 默认值来源，不写值，只做「删」。
        /// </summary>
        /// <param name="key">配置键（空 = 全群）</param>
        /// <param name="error">失败原因（成功为空串）</param>
        /// <returns>是否成功</returns>
        public bool ResetToDefault(string key, out string error)
        {
            error = "";
            // [段1] 目标集合——空 key = 全部落盘键；非空 = 单键（必须已落盘）
            List<string> targets = new List<string>();
            lock (_gate)
            {
                if (key == null || key.Length == 0)
                {
                    foreach (KeyValuePair<string, string> pair in _values)
                    {
                        targets.Add(pair.Key);
                    }
                }
                else
                {
                    // 无落盘值 = 目标状态（未设置）已达成——幂等成功（前端「空值保存」不报错）
                    if (!_values.ContainsKey(key))
                    {
                        return true;
                    }
                    targets.Add(key);
                }
            }

            // [段2] 快照 + 移除 + 落盘（失败回滚）
            string[] oldValues = new string[targets.Count];
            lock (_gate)
            {
                for (int i = 0; i < targets.Count; i = i + 1)
                {
                    string? found;
                    if (_values.TryGetValue(targets[i], out found) && found != null)
                    {
                        oldValues[i] = found;
                    }
                    else
                    {
                        oldValues[i] = "";
                    }
                }
            }

            try
            {
                lock (_gate)
                {
                    for (int i = 0; i < targets.Count; i = i + 1)
                    {
                        _values.Remove(targets[i]);
                    }
                }

                Save();
                return true;
            }
            catch (Exception ex)
            {
                try
                {
                    lock (_gate)
                    {
                        for (int i = 0; i < targets.Count; i = i + 1)
                        {
                            _values[targets[i]] = oldValues[i];
                        }
                    }

                    Save();
                }
                catch (Exception rollbackEx)
                {
                    // 回滚再失败——内存保持一致，磁盘差异交由审计提示
                    LogStore.Add("ConfigStore", 2, "回滚再失败（内存一致/磁盘差异）: " + rollbackEx.Message, "SYS");
                }

                error = "还原失败: " + ex.Message;
                return false;
            }
        }
        /// <summary>
        /// 从目录加载配置——扫描目录下全部 *.cfg（文件名去后缀 = 键前缀段）装入同一张表。
        /// 无前缀键落主文件 llm.cfg；目录缺失返回空配置。
        /// </summary>
        /// <param name="configDir">配置目录（Data/config 绝对路径）</param>
        /// <returns>配置存储（已关联主文件 llm.cfg）</returns>
        public static ConfigStore LoadDir(string configDir)
        {
            ConfigStore store = new ConfigStore();
            if (configDir == null || configDir.Length == 0)
            {
                return store;
            }
            store._path = Path.Combine(configDir, "llm.cfg");
            if (!Directory.Exists(configDir))
            {
                return store;
            }
            string[] files = Directory.GetFiles(configDir, "*.cfg");
            for (int i = 0; i < files.Length; i = i + 1)
            {
                string segment = Path.GetFileNameWithoutExtension(files[i]);
                store.AddFile(segment, files[i]);
            }
            return store;
        }
        /// <summary>
        /// 键名合法性——只允许字母 / 数字 / 下划线 / 点，且不以点开头（写入前一次性校验，无 schema）
        /// </summary>
        /// <param name="key">配置键</param>
        /// <returns>是否合法</returns>
        private static bool IsSimpleKey(string key)
        {
            if (key.Length == 0 || key[0] == '.')
            {
                return false;
            }
            for (int i = 0; i < key.Length; i = i + 1)
            {
                char c = key[i];
                bool ok = (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '.';
                if (!ok)
                {
                    return false;
                }
            }
            return true;
        }
    }
}
