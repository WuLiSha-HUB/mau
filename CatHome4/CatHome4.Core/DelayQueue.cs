using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using System.Text.Json;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// 延迟条目——输入队列的时间维度形态（design-ch4-delay §二）：内容 + 绝对墙钟时间戳 + 来源。
    /// 排序键 = DueAt 升序（同刻按 CreatedAt / Id 序）——批量到点时保序注入。
    /// </summary>
    public sealed class DelayEntry
    {
        /// <summary>条目序号——进程内自增（跨会话唯一；前端编辑/取消引用）</summary>
        public long Id;

        /// <summary>归属会话——猫 key（会话标识 ≡ 猫 key）</summary>
        public string CatKey = "";

        /// <summary>注入内容（原样文本，不做解析）</summary>
        public string Content = "";

        /// <summary>触发时刻——Unix 毫秒（UTC；绝对墙钟，不是内部倒计时计数）</summary>
        public long DueAt;

        /// <summary>登记时刻——Unix 毫秒（观测与同刻排序用）</summary>
        public long CreatedAt;

        /// <summary>来源标记——delay（人工定时）/ sleep（LLM 等待）/ timer（LLM 排程）/ restart（宿主重启回执）</summary>
        public string Source = "delay";

        /// <summary>循环标记——true=触发后按「原预定时刻 + 周期」推进相位重排回表（不出表）；false=一次性（触发即移除）</summary>
        public bool Loop;

        /// <summary>循环时长——Unix 毫秒（登记时的相对长度；重排基准 = 原预定时刻 + 本值，已错过的点跳过）</summary>
        public long IntervalMs;

        /// <summary>已触发次数——循环条目累计（观测面「已响 N 次」；一次性条目恒 0）</summary>
        public int Fired;
    }

    /// <summary>
    /// 延迟指令队列——定时表 + 到点转移（design-ch4-delay §三/§四）。
    /// 两段式：本表（未到点）→ 到点按序逐条转入会话即时队（ChatSession.PostUserMessage）——用户插话不被未到点条目阻塞。
    /// 时间基准 = 绝对墙钟时间戳（Unix 毫秒）：每帧比较 now &gt;= DueAt，不累加帧计数（无漂移；改时刻即改触发点；前端倒计时为纯投影）。
    /// 线程安全：全部操作锁内完成（HTTP 线程 / 主线程 / 后台线程低频访问）。
    /// 落盘：Data/runtime/delays.json——变更即原子落盘；条目跨宿主重启保留（停机期间到期的条目重启后按序立即注入）。
    /// </summary>
    public static class DelayQueue
    {
        /// <summary>单会话条目上限——超出拒绝（防 LLM 反复登记堆积）</summary>
        public const int MaxPerSession = 50;

        /// <summary>sleep 时长下限（秒）——0 无意义（等于立即注入）</summary>
        public const long MinSleepSeconds = 1;

        /// <summary>sleep 时长上限（秒）——LLM 自主挂起防呆；人工登记不受此限</summary>
        public const long MaxSleepSeconds = 3600;

        /// <summary>定时表——按 DueAt 升序维护（登记即有序插入）</summary>
        private static List<DelayEntry> _entries = new List<DelayEntry>();

        /// <summary>表锁——全部读写锁内完成</summary>
        private static readonly object QueueLock = new object();

        /// <summary>条目序号——进程内单调自增</summary>
        private static long _nextId = 1;

        /// <summary>落盘路径——Data/runtime/delays.json（空 = 不落盘，仅内存）</summary>
        private static string _storePath = "";

        /// <summary>时钟——可替换（测试注入假时钟）；默认本机 UTC 墙钟毫秒</summary>
        internal static Func<long> NowProvider = DefaultNow;

        /// <summary>默认时钟——本机 UTC 墙钟毫秒</summary>
        /// <returns>Unix 毫秒（UTC）</returns>
        private static long DefaultNow()
        {
            return DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
        }

        /// <summary>当前时刻——Unix 毫秒（UTC）。</summary>
        /// <returns>Unix 毫秒</returns>
        public static long Now()
        {
            return NowProvider();
        }

        /// <summary>时刻文本——本机时区可读形态（日志与列表用）。</summary>
        /// <param name="unixMs">Unix 毫秒</param>
        /// <returns>yyyy-MM-dd HH:mm:ss</returns>
        public static string FormatTime(long unixMs)
        {
            return DateTimeOffset.FromUnixTimeMilliseconds(unixMs).ToLocalTime().ToString("yyyy-MM-dd HH:mm:ss");
        }

        /// <summary>
        /// 配置落盘路径——宿主启动期注入（Admin/Core 不依赖配置面，路径由入口壳给）。
        /// </summary>
        /// <param name="storePath">delays.json 绝对路径（空 = 仅内存）</param>
        public static void Configure(string storePath)
        {
            lock (QueueLock)
            {
                _storePath = storePath == null ? "" : storePath;
            }
        }

        /// <summary>
        /// 登记条目——调用方负责把相对时长/绝对时刻换算为 dueAt。
        /// </summary>
        /// <param name="catKey">归属会话（猫 key）</param>
        /// <param name="content">注入内容</param>
        /// <param name="source">来源标记（delay/sleep/timer/restart）</param>
        /// <param name="dueAt">触发时刻——Unix 毫秒（UTC）</param>
        /// <param name="intervalMs">循环时长——Unix 毫秒（loop=true 时重排基准；0=不可循环）</param>
        /// <param name="loop">循环标记——true=触发后按「原预定时刻 + 周期」推进相位重排回表（不出表）</param>
        /// <returns>结果 JSON（ok/id/dueAt/loop/due；失败 ERR| 前缀）</returns>
        public static string Add(string catKey, string content, string source, long dueAt, long intervalMs = 0, bool loop = false)
        {
            if (catKey == null || catKey.Length == 0)
            {
                return "ERR|DELAY_NO_CAT|延迟条目缺少归属会话";
            }
            if (content == null || content.Length == 0)
            {
                return "ERR|BAD_ARGS|延迟条目内容为空";
            }
            if (dueAt <= 0)
            {
                return "ERR|BAD_ARGS|延迟条目触发时刻非法";
            }
            string useSource = source;
            if (useSource == null || useSource.Length == 0)
            {
                useSource = "delay";
            }
            lock (QueueLock)
            {
                int owned = 0;
                for (int i = 0; i < _entries.Count; i = i + 1)
                {
                    if (_entries[i].CatKey == catKey)
                    {
                        owned = owned + 1;
                    }
                }
                if (owned >= MaxPerSession)
                {
                    LogStore.Add("CatHome4", 2, "延迟条目登记被拒（上限）: cat=" + catKey + " | 已达 " + MaxPerSession.ToString() + " 条", "DELAY");
                    return "ERR|DELAY_LIMIT|本会话待触发条目已达上限 " + MaxPerSession.ToString() + "（可取消后再登记）";
                }
                DelayEntry entry = new DelayEntry();
                entry.Id = _nextId;
                _nextId = _nextId + 1;
                entry.CatKey = catKey;
                entry.Content = content;
                entry.Source = useSource;
                entry.DueAt = dueAt;
                entry.CreatedAt = NowProvider();
                entry.IntervalMs = intervalMs;
                entry.Loop = loop && intervalMs > 0;
                InsertSorted(entry);
                SaveLocked();
                string loopNote = "";
                if (entry.Loop)
                {
                    loopNote = " | loop=" + intervalMs.ToString() + "ms";
                }
                LogStore.Add("CatHome4", 1, "延迟条目登记: id=#" + entry.Id.ToString() + " | cat=" + catKey + " | due=" + FormatTime(dueAt) + " | source=" + useSource + loopNote, "DELAY");
                return JsonUtil.Object(
                    ("ok", true),
                    ("id", entry.Id),
                    ("dueAt", dueAt),
                    ("loop", entry.Loop),
                    ("due", FormatTime(dueAt)));
            }
        }

        /// <summary>
        /// 改触发时刻——重设 DueAt（前端编辑 / 手动调整时间戳）。
        /// </summary>
        /// <param name="catKey">归属会话（猫 key——越权校验：非本会话条目拒绝）</param>
        /// <param name="id">条目序号</param>
        /// <param name="dueAt">新触发时刻——Unix 毫秒（UTC）</param>
        /// <returns>结果文本（ok / ERR| 前缀）</returns>
        public static string SetDueAt(string catKey, long id, long dueAt)
        {
            if (dueAt <= 0)
            {
                return "ERR|BAD_ARGS|触发时刻非法";
            }
            lock (QueueLock)
            {
                for (int i = 0; i < _entries.Count; i = i + 1)
                {
                    if (_entries[i].Id != id)
                    {
                        continue;
                    }
                    if (_entries[i].CatKey != catKey)
                    {
                        return "ERR|DELAY_NOT_FOUND|条目不属于本会话: #" + id.ToString();
                    }
                    DelayEntry entry = BuildCopy(_entries[i], id, catKey, dueAt);
                    _entries.RemoveAt(i);
                    InsertSorted(entry);
                    SaveLocked();
                    LogStore.Add("CatHome4", 1, "延迟条目改时刻: id=#" + id.ToString() + " | due=" + FormatTime(dueAt), "DELAY");
                    return "ok|#" + id.ToString() + " due=" + FormatTime(dueAt);
                }
                return "ERR|DELAY_NOT_FOUND|条目不存在: #" + id.ToString();
            }
        }

        /// <summary>
        /// 取消条目。
        /// </summary>
        /// <param name="catKey">归属会话（猫 key——越权校验）</param>
        /// <param name="id">条目序号</param>
        /// <returns>结果文本（ok / ERR| 前缀）</returns>
        public static string Cancel(string catKey, long id)
        {
            lock (QueueLock)
            {
                for (int i = 0; i < _entries.Count; i = i + 1)
                {
                    if (_entries[i].Id != id)
                    {
                        continue;
                    }
                    if (_entries[i].CatKey != catKey)
                    {
                        return "ERR|DELAY_NOT_FOUND|条目不属于本会话: #" + id.ToString();
                    }
                    _entries.RemoveAt(i);
                    SaveLocked();
                    LogStore.Add("CatHome4", 1, "延迟条目取消: id=#" + id.ToString() + " | cat=" + catKey, "DELAY");
                    return "ok|#" + id.ToString() + " 已取消";
                }
                return "ERR|DELAY_NOT_FOUND|条目不存在: #" + id.ToString();
            }
        }

        /// <summary>
        /// 按来源批量取消——返回被取消条目（sleep 被打断即销毁的落点；运维批量清理）。
        /// </summary>
        /// <param name="catKey">归属会话（猫 key）</param>
        /// <param name="source">来源标记（如 sleep）</param>
        /// <returns>被取消条目数组（按表序升序；空=无匹配）</returns>
        public static DelayEntry[] CancelBySource(string catKey, string source)
        {
            List<DelayEntry> removed = new List<DelayEntry>();
            lock (QueueLock)
            {
                for (int i = _entries.Count - 1; i >= 0; i = i - 1)
                {
                    if (_entries[i].CatKey == catKey && _entries[i].Source == source)
                    {
                        removed.Insert(0, _entries[i]);
                        _entries.RemoveAt(i);
                    }
                }
                if (removed.Count > 0)
                {
                    SaveLocked();
                    LogStore.Add("CatHome4", 1, "延迟条目批量取消: cat=" + catKey + " | source=" + source + " | 共 " + removed.Count.ToString() + " 条", "DELAY");
                }
            }
            return removed.ToArray();
        }

        /// <summary>
        /// 切换循环标记——loop=true 需条目已有循环时长（登记时给；无时长拒绝并出声）。
        /// </summary>
        /// <param name="catKey">归属会话（猫 key——越权校验）</param>
        /// <param name="id">条目序号</param>
        /// <param name="loop">目标标记</param>
        /// <returns>结果文本（ok / ERR| 前缀）</returns>
        public static string SetLoop(string catKey, long id, bool loop)
        {
            lock (QueueLock)
            {
                for (int i = 0; i < _entries.Count; i = i + 1)
                {
                    if (_entries[i].Id != id)
                    {
                        continue;
                    }
                    if (_entries[i].CatKey != catKey)
                    {
                        return "ERR|DELAY_NOT_FOUND|条目不属于本会话: #" + id.ToString();
                    }
                    if (loop && _entries[i].IntervalMs <= 0)
                    {
                        return "ERR|BAD_ARGS|条目缺少循环时长（重新登记时给定时长）: #" + id.ToString();
                    }
                    _entries[i].Loop = loop;
                    SaveLocked();
                    string mark = "关";
                    if (loop)
                    {
                        mark = "开";
                    }
                    LogStore.Add("CatHome4", 1, "延迟条目循环标记: id=#" + id.ToString() + " | loop=" + mark, "DELAY");
                    return "ok|#" + id.ToString() + " 循环" + mark;
                }
                return "ERR|DELAY_NOT_FOUND|条目不存在: #" + id.ToString();
            }
        }

        /// <summary>
        /// 待触发条目快照——按 DueAt 升序（本会话）。
        /// </summary>
        /// <param name="catKey">归属会话（猫 key）</param>
        /// <returns>条目数组（拷贝——调用方不持锁）</returns>
        public static DelayEntry[] List(string catKey)
        {
            lock (QueueLock)
            {
                List<DelayEntry> list = new List<DelayEntry>();
                for (int i = 0; i < _entries.Count; i = i + 1)
                {
                    if (_entries[i].CatKey == catKey)
                    {
                        list.Add(_entries[i]);
                    }
                }
                return list.ToArray();
            }
        }

        /// <summary>
        /// 待触发条目数组 JSON——视图出口 state 段 delay 字段数据源（A185：原 GET /api/v1/delay 旁路端点退役）。
        /// </summary>
        /// <param name="catKey">归属会话（猫 key）</param>
        /// <returns>[{"id","content","dueAt","createdAt","source","loop","intervalMs","fired"}, …]</returns>
        public static JsonFragment BuildEntriesFragment(string catKey)
        {
            DelayEntry[] list = List(catKey);
            List<string> items = new List<string>();
            for (int i = 0; i < list.Length; i = i + 1)
            {
                items.Add(JsonUtil.Object(
                    ("id", list[i].Id),
                    ("content", list[i].Content),
                    ("dueAt", list[i].DueAt),
                    ("createdAt", list[i].CreatedAt),
                    ("source", list[i].Source),
                    ("loop", list[i].Loop),
                    ("intervalMs", list[i].IntervalMs),
                    ("fired", list[i].Fired)));
            }
            return JsonUtil.RawArray(items.ToArray());
        }

        /// <summary>
        /// 待触发条目 JSON——CLI / 工具面（delay.list）。
        /// </summary>
        /// <param name="catKey">归属会话（猫 key）</param>
        /// <returns>{"ok":true,"now":&lt;ms&gt;,"entries":[{"id","content","dueAt","createdAt","source"}]}</returns>
        public static string BuildListJson(string catKey)
        {
            return JsonUtil.Object(("ok", true), ("now", NowProvider()), ("entries", BuildEntriesFragment(catKey)));
        }

        /// <summary>
        /// 待触发条目文本——CLI / LLM 面（delay.list）。
        /// </summary>
        /// <param name="catKey">归属会话（猫 key）</param>
        /// <returns>逐行文本（空表 = 明确空态）</returns>
        public static string BuildListText(string catKey)
        {
            DelayEntry[] list = List(catKey);
            if (list.Length == 0)
            {
                return "延迟队列：空（本会话无待触发条目）";
            }
            StringBuilder sb = new StringBuilder();
            sb.Append("延迟队列：");
            sb.Append(list.Length.ToString());
            sb.Append(" 条（本会话）");
            for (int i = 0; i < list.Length; i = i + 1)
            {
                sb.Append("\n  #");
                sb.Append(list[i].Id.ToString());
                sb.Append(" | ");
                sb.Append(FormatTime(list[i].DueAt));
                sb.Append(" | ");
                sb.Append(list[i].Source);
                if (list[i].Loop)
                {
                    sb.Append(" 🔁");
                }
                if (list[i].Fired > 0)
                {
                    sb.Append(" 已响 ");
                    sb.Append(list[i].Fired.ToString());
                }
                sb.Append(" | ");
                sb.Append(list[i].Content);
            }
            return sb.ToString();
        }

        /// <summary>
        /// 帧泵——摘出到点条目 → 按序投递（锁外）→ 未受理的重新入表。
        /// 积压语义：多个到点条目按序一次性立即注入（不丢、不合并、不覆盖）。
        /// loop 语义：投递成功且 Loop=true → 按「原预定时刻 + IntervalMs」推进相位重排回表（不出表；Fired 累计；错过的周期跳过不补）。
        /// </summary>
        /// <param name="deliver">投递委托（catKey, content, source）→ true=已受理 / false=未受理（保留待重投）</param>
        /// <returns>本次投递条数</returns>
        public static int Pump(Func<string, string, string, bool> deliver)
        {
            if (deliver == null)
            {
                return 0;
            }
            List<DelayEntry> due = null;
            lock (QueueLock)
            {
                if (_entries.Count == 0)
                {
                    return 0;
                }
                long now = NowProvider();
                for (int i = 0; i < _entries.Count;)
                {
                    if (_entries[i].DueAt <= now)
                    {
                        if (due == null)
                        {
                            due = new List<DelayEntry>();
                        }
                        due.Add(_entries[i]);
                        _entries.RemoveAt(i);
                    }
                    else
                    {
                        i = i + 1;
                    }
                }
            }
            if (due == null)
            {
                return 0;
            }
            int delivered = 0;
            List<DelayEntry> rejected = new List<DelayEntry>();
            List<DelayEntry> repeated = new List<DelayEntry>();
            for (int i = 0; i < due.Count; i = i + 1)
            {
                bool accepted = deliver(due[i].CatKey, due[i].Content, due[i].Source);
                if (accepted)
                {
                    delivered = delivered + 1;
                    LogStore.Add("CatHome4", 1, "延迟条目到点投递: id=#" + due[i].Id.ToString() + " | cat=" + due[i].CatKey + " | source=" + due[i].Source, "DELAY");
                    // loop——触发后按「原预定时刻 + 周期」推进到首个未来相位（不出表；错过的周期跳过不补）
                    if (due[i].Loop)
                    {
                        if (due[i].IntervalMs > 0)
                        {
                            due[i].Fired = due[i].Fired + 1;
                            due[i].DueAt = NextLoopDueAt(due[i].DueAt, due[i].IntervalMs, NowProvider());
                            repeated.Add(due[i]);
                            LogStore.Add("CatHome4", 1, "延迟条目循环重排: id=#" + due[i].Id.ToString() + " | next=" + FormatTime(due[i].DueAt) + " | 已响 " + due[i].Fired.ToString() + " 次", "DELAY");
                        }
                        else
                        {
                            LogStore.Add("CatHome4", 2, "延迟条目循环重排跳过（缺循环时长）: id=#" + due[i].Id.ToString(), "DELAY");
                        }
                    }
                }
                else
                {
                    rejected.Add(due[i]);
                }
            }
            lock (QueueLock)
            {
                for (int i = 0; i < rejected.Count; i = i + 1)
                {
                    InsertSorted(rejected[i]);
                }
                for (int i = 0; i < repeated.Count; i = i + 1)
                {
                    InsertSorted(repeated[i]);
                }
                if (delivered > 0 || rejected.Count > 0 || repeated.Count > 0)
                {
                    SaveLocked();
                }
            }
            return delivered;
        }

        /// <summary>循环重排单次最多跳过的周期数——防超大跨度（长期停机）在重排时死循环</summary>
        private const int MaxLoopSkipSteps = 100000;

        /// <summary>
        /// 循环条目的下一次触发时刻——按时间轴相位推进（原预定点 + 周期），已错过的点一次性跳过。
        /// 不按「投递时刻 + 周期」重算：那样每轮都从此刻起算，周期条目会随帧泵粒度与投递延迟逐轮漂移（闹钟变成越走越晚）。
        /// </summary>
        /// <param name="dueAt">本轮预定的触发时刻——Unix 毫秒</param>
        /// <param name="intervalMs">循环周期——Unix 毫秒</param>
        /// <param name="now">当前时刻——Unix 毫秒</param>
        /// <returns>首个晚于 now 的相位点；周期非法时返回首轮推算值</returns>
        private static long NextLoopDueAt(long dueAt, long intervalMs, long now)
        {
            long next = dueAt + intervalMs;
            if (intervalMs <= 0 || next > now)
            {
                return next;
            }
            long missed = (now - next) / intervalMs;
            if (missed > MaxLoopSkipSteps)
            {
                missed = MaxLoopSkipSteps;
            }
            next = next + missed * intervalMs;
            while (next <= now)
            {
                next = next + intervalMs;
            }
            return next;
        }

        /// <summary>
        /// 加载落盘条目——宿主启动期调用（跨重启保留）。
        /// </summary>
        public static void Load()
        {
            lock (QueueLock)
            {
                if (_storePath.Length == 0 || !File.Exists(_storePath))
                {
                    return;
                }
                try
                {
                    string text = File.ReadAllText(_storePath);
                    using (JsonDocument doc = JsonUtil.ParseStrict(text))
                    {
                        JsonElement root = doc.RootElement;
                        JsonElement nextEl;
                        if (root.TryGetProperty("nextId", out nextEl) && nextEl.ValueKind == JsonValueKind.Number)
                        {
                            long next = nextEl.GetInt64();
                            if (next > _nextId)
                            {
                                _nextId = next;
                            }
                        }
                        JsonElement arr;
                        if (root.TryGetProperty("entries", out arr) && arr.ValueKind == JsonValueKind.Array)
                        {
                            for (int i = 0; i < arr.GetArrayLength(); i = i + 1)
                            {
                                JsonElement item = arr[i];
                                if (item.ValueKind != JsonValueKind.Object)
                                {
                                    continue;
                                }
                                DelayEntry entry = ParseEntry(item);
                                if (entry != null)
                                {
                                    InsertSorted(entry);
                                    if (entry.Id >= _nextId)
                                    {
                                        _nextId = entry.Id + 1;
                                    }
                                }
                            }
                        }
                    }
                    LogStore.Add("CatHome4", 1, "延迟队列已加载：" + _entries.Count.ToString() + " 条（" + _storePath + "）", "DELAY");
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 3, "延迟队列加载失败（按空表继续）: " + ex.Message, "DELAY");
                }
            }
        }

        /// <summary>
        /// 测试专用——清空表与时钟替换（单测隔离）。
        /// </summary>
        internal static void ResetForTest()
        {
            lock (QueueLock)
            {
                _entries = new List<DelayEntry>();
                _nextId = 1;
                _storePath = "";
                NowProvider = DefaultNow;
            }
        }

        /// <summary>
        /// 解析单条目——防御式（缺字段/类型不符即丢弃）。
        /// </summary>
        /// <param name="item">JSON 对象</param>
        /// <returns>条目（非法 = null）</returns>
        private static DelayEntry ParseEntry(JsonElement item)
        {
            DelayEntry entry = new DelayEntry();
            JsonElement idEl;
            if (!item.TryGetProperty("id", out idEl) || idEl.ValueKind != JsonValueKind.Number)
            {
                return null;
            }
            entry.Id = idEl.GetInt64();
            JsonElement catEl;
            if (item.TryGetProperty("cat", out catEl) && catEl.ValueKind == JsonValueKind.String)
            {
                entry.CatKey = catEl.GetString() ?? "";
            }
            JsonElement contentEl;
            if (item.TryGetProperty("content", out contentEl) && contentEl.ValueKind == JsonValueKind.String)
            {
                entry.Content = contentEl.GetString() ?? "";
            }
            JsonElement dueEl;
            if (item.TryGetProperty("dueAt", out dueEl) && dueEl.ValueKind == JsonValueKind.Number)
            {
                entry.DueAt = dueEl.GetInt64();
            }
            JsonElement createdEl;
            if (item.TryGetProperty("createdAt", out createdEl) && createdEl.ValueKind == JsonValueKind.Number)
            {
                entry.CreatedAt = createdEl.GetInt64();
            }
            JsonElement sourceEl;
            if (item.TryGetProperty("source", out sourceEl) && sourceEl.ValueKind == JsonValueKind.String)
            {
                string got = sourceEl.GetString();
                if (got != null && got.Length > 0)
                {
                    entry.Source = got;
                }
            }
            JsonElement loopEl;
            if (item.TryGetProperty("loop", out loopEl) && loopEl.ValueKind == JsonValueKind.True)
            {
                entry.Loop = true;
            }
            JsonElement intervalEl;
            if (item.TryGetProperty("intervalMs", out intervalEl) && intervalEl.ValueKind == JsonValueKind.Number)
            {
                entry.IntervalMs = intervalEl.GetInt64();
            }
            JsonElement firedEl;
            if (item.TryGetProperty("fired", out firedEl) && firedEl.ValueKind == JsonValueKind.Number)
            {
                entry.Fired = firedEl.GetInt32();
            }
            if (entry.Loop && entry.IntervalMs <= 0)
            {
                entry.Loop = false;
            }
            if (entry.CatKey.Length == 0 || entry.Content.Length == 0 || entry.DueAt <= 0)
            {
                return null;
            }
            return entry;
        }

        /// <summary>
        /// 有序插入——按 DueAt 升序（同刻按 CreatedAt / Id）——批量到点保序。
        /// </summary>
        /// <param name="entry">待插入条目</param>
        private static void InsertSorted(DelayEntry entry)
        {
            int pos = _entries.Count;
            for (int i = 0; i < _entries.Count; i = i + 1)
            {
                if (CompareEntry(_entries[i], entry) > 0)
                {
                    pos = i;
                    break;
                }
            }
            _entries.Insert(pos, entry);
        }

        /// <summary>
        /// 条目比较——DueAt 升序；同刻按 CreatedAt；再同按 Id。
        /// </summary>
        /// <param name="left">左条目</param>
        /// <param name="right">右条目</param>
        /// <returns>负数=左在前</returns>
        private static int CompareEntry(DelayEntry left, DelayEntry right)
        {
            if (left.DueAt != right.DueAt)
            {
                return left.DueAt < right.DueAt ? -1 : 1;
            }
            if (left.CreatedAt != right.CreatedAt)
            {
                return left.CreatedAt < right.CreatedAt ? -1 : 1;
            }
            if (left.Id == right.Id)
            {
                return 0;
            }
            return left.Id < right.Id ? -1 : 1;
        }

        /// <summary>
        /// 拷贝条目并改触发时刻——SetDueAt 用（保持内容与来源不变）。
        /// </summary>
        /// <param name="entry">原条目（保留字段源）</param>
        /// <param name="id">条目序号</param>
        /// <param name="catKey">归属会话</param>
        /// <param name="dueAt">新触发时刻</param>
        /// <returns>新条目</returns>
        private static DelayEntry BuildCopy(DelayEntry entry, long id, string catKey, long dueAt)
        {
            DelayEntry copy = new DelayEntry();
            copy.Id = id;
            copy.CatKey = catKey;
            copy.Content = entry.Content;
            copy.Source = entry.Source;
            copy.CreatedAt = entry.CreatedAt;
            copy.DueAt = dueAt;
            copy.IntervalMs = entry.IntervalMs;
            copy.Loop = entry.Loop;
            copy.Fired = entry.Fired;
            return copy;
        }

        /// <summary>
        /// 落盘——锁内调用（原子写：临时文件 + 替换）。
        /// </summary>
        private static void SaveLocked()
        {
            if (_storePath.Length == 0)
            {
                return;
            }
            try
            {
                string dir = Path.GetDirectoryName(_storePath);
                if (dir != null && dir.Length > 0 && !Directory.Exists(dir))
                {
                    Directory.CreateDirectory(dir);
                }
                List<string> entryItems = new List<string>();
                for (int i = 0; i < _entries.Count; i = i + 1)
                {
                    entryItems.Add(JsonUtil.Object(
                        ("id", _entries[i].Id),
                        ("cat", _entries[i].CatKey),
                        ("content", _entries[i].Content),
                        ("dueAt", _entries[i].DueAt),
                        ("createdAt", _entries[i].CreatedAt),
                        ("source", _entries[i].Source),
                        ("loop", _entries[i].Loop),
                        ("intervalMs", _entries[i].IntervalMs),
                        ("fired", _entries[i].Fired)));
                }
                string payload = JsonUtil.Object(("version", 1), ("nextId", _nextId), ("entries", JsonUtil.RawArray(entryItems.ToArray())));
                string temp = _storePath + ".tmp";
                File.WriteAllText(temp, payload, new UTF8Encoding(false));
                if (File.Exists(_storePath))
                {
                    File.Delete(_storePath);
                }
                File.Move(temp, _storePath);
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 3, "延迟队列落盘失败: " + ex.Message, "DELAY");
            }
        }
    }
}
