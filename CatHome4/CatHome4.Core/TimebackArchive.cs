using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// timeback 作用域归档记录——一次回收的元数据（落归档文件首行；A104 新机制）。
    /// </summary>
    public sealed class TimebackScopeRecord
    {
        /// <summary>全局作用域编号——跨猫 / 跨会话 / 跨重启永久递增</summary>
        public long Id;

        /// <summary>归属猫 key</summary>
        public string CatKey = "";

        /// <summary>域类型（`TimebackProfile` 枚举——start 时登记；观测按类型分布统计）</summary>
        public string Type = "";

        /// <summary>用途标签（start 时登记）</summary>
        public string Purpose = "";

        /// <summary>锚点消息索引（start 的 tool_calls 声明）</summary>
        public int Anchor;

        /// <summary>开锚时刻——Unix 毫秒</summary>
        public long StartAt;

        /// <summary>回收时刻——Unix 毫秒</summary>
        public long BackAt;

        /// <summary>作用域存活时长（秒）</summary>
        public long Seconds;

        /// <summary>实际删除的前文条数</summary>
        public int N;

        /// <summary>域内人工输入保留条数（2026-11-07——用户在这个域里发的消息不随域销毁；未删故不入被删正文）</summary>
        public int KeptUsers;

        /// <summary>回收时已知前文长度（请求级真实 usage 值，零估算）</summary>
        public long Tokens;

        /// <summary>作用域净增（Tokens − 开锚快照）</summary>
        public long Grew;

        /// <summary>释放条数预算（back 回执给出的值——与实际 N 对账）</summary>
        public int Released;

        /// <summary>带回载荷全文（findings）</summary>
        public string Findings = "";
        /// <summary>
        /// 本域工具台账——宿主记录（域内用过的每个工具逐条：工具名 · 目标标识 · 成败；含只读与被拒）；空=域内零调用。
        /// </summary>
        public List<string> Writes = new List<string>();
    }

    /// <summary>
    /// timeback 归档——全局计数 + 每次回收一个作用域文件（A104 · design-ch4-timeback §五）。
    /// 落点：Data/runtime/timeback/
    ///   count.json            全局计数（跨猫 / 跨重启；{"count":N}）
    ///   &lt;id&gt;-&lt;时间戳&gt;.jsonl  一次回收一个文件——首行 meta（记录字段）+ 次行该猫 info 快照 + 其后逐行被删前文消息
    /// 取号：读 count.json → +1 → 原子写回（进程内锁串行；不扫归档文件——A104 前「全文件扫读取号」已退役）。
    /// 写面：失败记 L3 + LastWriteFailed（实例）/ LastWriteFailedAny（进程内，info 观测面数据源）——失败不阻断回收，但必须可见。
    /// 防覆盖：同名文件已存在时改用后缀（编号写失败后重启重号时不销毁旧档）。
    /// </summary>
    public sealed class TimebackArchive
    {
        /// <summary>取号锁——进程内串行（多猫共享全局计数）</summary>
        private static readonly object CountLock = new object();

        /// <summary>进程内最近一次写面失败标志——info timeback.archive.ok 数据源（跨实例可见）</summary>
        private static bool _lastWriteFailedAny;

        /// <summary>归档目录——Data/runtime/timeback</summary>
        private readonly string _dir;

        /// <summary>计数文件路径——&lt;目录&gt;/count.json</summary>
        private readonly string _countPath;

        /// <summary>最近一次写面结果——true=最近一次写入失败（本实例）</summary>
        private bool _lastWriteFailed;

        /// <summary>UTF-8 无 BOM 编码——JSONL / JSON 写入（与 SessionStore 同族）</summary>
        private static readonly UTF8Encoding Utf8NoBom = new UTF8Encoding(false);

        /// <summary>序列化选项——中文直出（默认 \uXXXX 转义人读不便）</summary>
        private static readonly JsonSerializerOptions SerializerOptions = BuildOptions();

        /// <summary>
        /// 建立归档实例——目录与计数文件按需创建（首次写入时建目录）。
        /// </summary>
        /// <param name="dir">归档目录（Data/runtime/timeback）</param>
        public TimebackArchive(string dir)
        {
            _dir = dir == null ? "" : dir;
            _countPath = Path.Combine(_dir, "count.json");
        }

        /// <summary>
        /// 写面可用性（本实例）——最近一次写入是否失败。
        /// </summary>
        public bool LastWriteFailed
        {
            get
            {
                return _lastWriteFailed;
            }
        }

        /// <summary>
        /// 写面可用性（进程内）——最近一次写入是否失败（跨实例；info 观测面数据源）。
        /// </summary>
        public static bool LastWriteFailedAny
        {
            get
            {
                return _lastWriteFailedAny;
            }
        }

        /// <summary>
        /// 取下一个编号——全局永久递增（跨猫 / 跨会话 / 跨重启）。
        /// 读 count.json → +1 → 原子写回；读失败按 0 起算并出声（L3）。
        /// </summary>
        /// <returns>新编号（从 1 起）</returns>
        public long NextId()
        {
            lock (CountLock)
            {
                long current = ReadCount();
                long next = current + 1;
                WriteCount(next);
                return next;
            }
        }

        /// <summary>
        /// 写一次回收的作用域文件——首行 meta + 次行 info 快照 + 其后被删前文消息（逐条一行）。
        /// 原子写（临时文件 + 替换）；目标同名时改用后缀（防覆盖）。
        /// </summary>
        /// <param name="record">作用域记录（首行字段）</param>
        /// <param name="infoJson">该猫 info 快照 JSON（单行；空=不写该行）</param>
        /// <param name="removed">被删前文消息（原序；与真实前文同构）</param>
        /// <param name="path">落盘路径（失败=空串）</param>
        /// <returns>true=已落盘</returns>
        public bool WriteScopeFile(TimebackScopeRecord record, string infoJson, LlmMessage[] removed, out string path)
        {
            path = "";
            if (record == null)
            {
                MarkWriteFailed("timeback 归档写入失败（记录为空）");
                return false;
            }
            try
            {
                EnsureDirectory();
                StringBuilder sb = new StringBuilder();
                sb.Append(BuildMetaLine(record));
                sb.Append('\n');
                if (infoJson != null && infoJson.Trim().Length > 0)
                {
                    sb.Append(NormalizeJsonLine(infoJson));
                    sb.Append('\n');
                }
                if (removed != null)
                {
                    for (int i = 0; i < removed.Length; i = i + 1)
                    {
                        sb.Append(SessionStore.SerializeMessageLine(removed[i]));
                        sb.Append('\n');
                    }
                }
                string target = ResolveTargetPath(record);
                string tmp = target + ".tmp";
                File.WriteAllText(tmp, sb.ToString(), Utf8NoBom);
                File.Move(tmp, target, true);
                path = target;
                _lastWriteFailed = false;
                _lastWriteFailedAny = false;
                return true;
            }
            catch (Exception ex)
            {
                MarkWriteFailed("timeback 归档写入失败（本次未落档）: " + ex.Message);
                return false;
            }
        }
        /// <summary>
        /// info 快照归一为单行——JSONL 要求每行独立（info 序列化选项含缩进，直接写入会把内部换行铺进文件、破坏行结构）。
        /// 已是单行则原样返回；解析失败按原样返回（不静默改写内容）并记 L2。
        /// </summary>
        /// <param name="json">原始 JSON 文本</param>
        /// <returns>单行 JSON</returns>
        private static string NormalizeJsonLine(string json)
        {
            string text = json == null ? "" : json.Trim();
            if (text.Length == 0 || text.IndexOf('\n') < 0)
            {
                return text;
            }
            try
            {
                JsonNode node = JsonNode.Parse(text);
                if (node == null)
                {
                    return text;
                }
                return node.ToJsonString();
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "timeback info 快照归一失败（按原样入档）: " + ex.Message, "TIMEBACK");
                return text;
            }
        }

        /// <summary>
        /// 读面——最近若干次回收的首行 meta（info timeback.recent 数据源）。
        /// 按文件名编号排序取末尾 max 个（不依赖目录项时间戳），逐文件只读首行。
        /// </summary>
        /// <param name="max">最大条数（≤0 = 空列表）</param>
        /// <returns>meta 行 JSON 列表（最旧在前；坏行跳过）</returns>
        public List<string> ReadRecentMeta(int max)
        {
            List<string> result = new List<string>();
            if (max <= 0 || _dir.Length == 0 || !Directory.Exists(_dir))
            {
                return result;
            }
            string[] files;
            try
            {
                files = Directory.GetFiles(_dir, "*.jsonl");
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 3, "timeback 归档目录读取失败（读面不可用）: " + ex.Message, "TIMEBACK");
                return result;
            }
            List<KeyValuePair<string, long>> ordered = new List<KeyValuePair<string, long>>();
            for (int i = 0; i < files.Length; i = i + 1)
            {
                string name = Path.GetFileName(files[i]);
                long id = ParseIdPrefix(name);
                if (id <= 0)
                {
                    continue;
                }
                ordered.Add(new KeyValuePair<string, long>(name, id));
            }
            ordered.Sort(CompareByName);
            int start = ordered.Count - max;
            if (start < 0)
            {
                start = 0;
            }
            for (int i = start; i < ordered.Count; i = i + 1)
            {
                string line = ReadFirstLine(Path.Combine(_dir, ordered[i].Key));
                if (line.Length > 0)
                {
                    result.Add(line);
                }
            }
            return result;
        }

        /// <summary>
        /// 归档文件排序——先比编号（升序），同编号比文件名（防覆盖后缀的确定性序）。
        /// </summary>
        /// <param name="a">左侧（文件名 + 编号）</param>
        /// <param name="b">右侧（文件名 + 编号）</param>
        /// <returns>比较结果</returns>
        private static int CompareByName(KeyValuePair<string, long> a, KeyValuePair<string, long> b)
        {
            int byId = a.Value.CompareTo(b.Value);
            if (byId != 0)
            {
                return byId;
            }
            return string.CompareOrdinal(a.Key, b.Key);
        }

        /// <summary>
        /// 解析文件名编号前缀——&lt;id&gt;-&lt;时间戳&gt;.jsonl（含防覆盖后缀）取首个分隔符之前部分。
        /// </summary>
        /// <param name="fileName">文件名</param>
        /// <returns>编号（0=不可用）</returns>
        private static long ParseIdPrefix(string fileName)
        {
            if (fileName == null || fileName.Length == 0)
            {
                return 0;
            }
            int dash = fileName.IndexOf('-');
            string head = dash > 0 ? fileName.Substring(0, dash) : fileName;
            long id;
            if (long.TryParse(head, out id))
            {
                return id;
            }
            return 0;
        }

        /// <summary>
        /// 读文件首行——只读一行即止（meta 行；避免整文件载入）。
        /// </summary>
        /// <param name="path">文件路径</param>
        /// <returns>首行文本（读取失败 / 空文件 = 空串）</returns>
        private static string ReadFirstLine(string path)
        {
            try
            {
                using (StreamReader reader = new StreamReader(path, Encoding.UTF8))
                {
                    string line = reader.ReadLine();
                    if (line == null)
                    {
                        return "";
                    }
                    return line.Trim();
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 3, "timeback 归档首行读取失败: " + ex.Message, "TIMEBACK");
                return "";
            }
        }

        /// <summary>
        /// 读全局计数——文件缺失 / 结构不符 / 读取失败按 0 起算（出声，不静默）。
        /// </summary>
        /// <returns>当前计数（0=无计数文件）</returns>
        private long ReadCount()
        {
            if (_dir.Length == 0 || !File.Exists(_countPath))
            {
                return 0;
            }
            try
            {
                string text = File.ReadAllText(_countPath, Encoding.UTF8);
                JsonNode node = JsonNode.Parse(text);
                JsonObject obj = node as JsonObject;
                if (obj == null)
                {
                    LogStore.Add("CatHome4", 2, "timeback 计数文件结构不符（按 0 起算）: " + _countPath, "TIMEBACK");
                    return 0;
                }
                JsonNode countNode = obj["count"];
                if (countNode == null)
                {
                    LogStore.Add("CatHome4", 2, "timeback 计数文件缺 count 字段（按 0 起算）: " + _countPath, "TIMEBACK");
                    return 0;
                }
                return countNode.GetValue<long>();
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 3, "timeback 计数读取失败（按 0 起算）: " + ex.Message, "TIMEBACK");
                return 0;
            }
        }

        /// <summary>
        /// 写全局计数——原子写（临时文件 + 替换）；失败记 L3 并立写面标志（失败必须可见）。
        /// </summary>
        /// <param name="value">新计数</param>
        /// <returns>true=已落盘</returns>
        private bool WriteCount(long value)
        {
            try
            {
                EnsureDirectory();
                JsonObject obj = new JsonObject();
                obj["count"] = value;
                string tmp = _countPath + ".tmp";
                File.WriteAllText(tmp, obj.ToJsonString(SerializerOptions), Utf8NoBom);
                File.Move(tmp, _countPath, true);
                _lastWriteFailedAny = false;
                return true;
            }
            catch (Exception ex)
            {
                MarkWriteFailed("timeback 计数写入失败（编号可能重号）: " + ex.Message);
                return false;
            }
        }

        /// <summary>
        /// 目标文件路径——&lt;id&gt;-&lt;时间戳&gt;.jsonl；同名已存在时改后缀（防覆盖，出声）。
        /// </summary>
        /// <param name="record">作用域记录</param>
        /// <returns>可用路径</returns>
        private string ResolveTargetPath(TimebackScopeRecord record)
        {
            string name = record.Id.ToString() + "-" + record.BackAt.ToString();
            string target = Path.Combine(_dir, name + ".jsonl");
            int suffix = 2;
            while (File.Exists(target))
            {
                LogStore.Add("CatHome4", 2, "timeback 归档文件名冲突——改用后缀避免覆盖: " + Path.GetFileName(target), "TIMEBACK");
                target = Path.Combine(_dir, name + "-" + suffix.ToString() + ".jsonl");
                suffix = suffix + 1;
            }
            return target;
        }

        /// <summary>
        /// 确保归档目录存在。
        /// </summary>
        private void EnsureDirectory()
        {
            if (_dir.Length > 0 && !Directory.Exists(_dir))
            {
                Directory.CreateDirectory(_dir);
            }
        }

        /// <summary>
        /// 标记写面失败——记 L3 + 实例标志 + 进程内标志（失败必须可见，不阻断回收）。
        /// </summary>
        /// <param name="message">日志消息</param>
        private void MarkWriteFailed(string message)
        {
            _lastWriteFailed = true;
            _lastWriteFailedAny = true;
            LogStore.Add("CatHome4", 3, message, "TIMEBACK");
        }

        /// <summary>
        /// 构建首行 meta——记录字段（t=timeback 便于识别）。
        /// </summary>
        /// <param name="record">作用域记录</param>
        /// <returns>单行 JSON</returns>
        private static string BuildMetaLine(TimebackScopeRecord record)
        {
            JsonObject obj = new JsonObject();
            obj["t"] = "timeback";
            obj["id"] = record.Id;
            obj["catKey"] = record.CatKey == null ? "" : record.CatKey;
            obj["type"] = record.Type == null ? "" : record.Type;
            obj["purpose"] = record.Purpose == null ? "" : record.Purpose;
            obj["anchor"] = record.Anchor;
            obj["startAt"] = record.StartAt;
            obj["backAt"] = record.BackAt;
            obj["seconds"] = record.Seconds;
            obj["n"] = record.N;
            obj["kept"] = record.KeptUsers;
            obj["tokens"] = record.Tokens;
            obj["grew"] = record.Grew;
            obj["released"] = record.Released;
            obj["tools"] = JsonSerializer.SerializeToNode(record.Writes, SerializerOptions);
            obj["findings"] = record.Findings == null ? "" : record.Findings;
            return obj.ToJsonString(SerializerOptions);
        }

        /// <summary>
        /// 序列化选项——中文直出。
        /// </summary>
        /// <returns>选项实例</returns>
        private static JsonSerializerOptions BuildOptions()
        {
            JsonSerializerOptions options = new JsonSerializerOptions();
            options.Encoder = System.Text.Encodings.Web.JavaScriptEncoder.UnsafeRelaxedJsonEscaping;
            return options;
        }
    }
}
