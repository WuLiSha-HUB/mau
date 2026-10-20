using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Text;
using System.Text.Json;
using System.Text.RegularExpressions;

namespace Mau.Runtime
{
    /// <summary>
    /// 条目库服务（Smriti / DbCat 底层）——一库一目录、追加式 JSONL、零向量化。
    /// 库文件即真源：无索引件、无缓存件；写入只追加（更新追加新版本行、删除追加墓碑行）。
    /// </summary>
    public sealed class DbService
    {
        /// <summary>
        /// 支持的存储格式版本（entries.jsonl 头行 fmt 判据；不符整库拒绝读取）
        /// </summary>
        private const int FormatSupport = 1;

        /// <summary>
        /// 标题硬上限（Unicode 字符数；超限拒写）
        /// </summary>
        private const int TitleMax = 50;

        /// <summary>
        /// 正文硬上限（Unicode 字符数；超限拒写）
        /// </summary>
        private const int BodyMax = 5000;

        /// <summary>
        /// 查询默认条数
        /// </summary>
        private const int DefaultLimit = 10;

        /// <summary>
        /// 查询条数上限
        /// </summary>
        private const int LimitMax = 200;

        /// <summary>
        /// 库名合法形态——小写字母 / 数字 / 短横线，2–32 位（单层目录名）
        /// </summary>
        private static readonly Regex LibIdPattern = new Regex("^[a-z0-9-]{2,32}$", RegexOptions.Compiled);

        /// <summary>
        /// 条目日志文件名（每库一份，追加式）
        /// </summary>
        private const string EntriesFile = "entries.jsonl";

        /// <summary>
        /// 库元信息文件名
        /// </summary>
        private const string MetaFile = "meta.json";

        /// <summary>
        /// 库注册表文件名（库根下全局一份；登记即库存在性判据）
        /// </summary>
        private const string IndexFile = "_index.json";

        /// <summary>
        /// 库根目录绝对路径（一库一子目录）
        /// </summary>
        private readonly string _dbRoot;

        /// <summary>
        /// 串行保护复合写操作（追加行与注册表更新）
        /// </summary>
        private readonly object _writeGate;

        /// <summary>
        /// 建立库服务——库根不存在则创建。
        /// </summary>
        /// <param name="dbRoot">库根目录绝对路径</param>
        public DbService(string dbRoot)
        {
            if (string.IsNullOrWhiteSpace(dbRoot))
            {
                throw new ArgumentException("Db root is required.", "dbRoot");
            }
            _dbRoot = PathBoundary.NormalizeRoot(dbRoot);
            Directory.CreateDirectory(_dbRoot);
            _writeGate = new object();
        }

        /// <summary>
        /// 库根目录实况（只读）。
        /// </summary>
        public string DbRoot
        {
            get
            {
                return _dbRoot;
            }
        }

        // [段1] 建库
        /// <summary>
        /// 新建库——建目录 + meta.json + entries.jsonl 头行 + 注册表登记。
        /// </summary>
        /// <param name="libId">库名（小写字母 / 数字 / 短横线，2–32 位）</param>
        /// <returns>结果（载荷含库名与创建时刻）</returns>
        public DbResult Create(string libId)
        {
            string lib = NormalizeLibId(libId);
            if (!LibIdPattern.IsMatch(lib))
            {
                return DbResult.Fail("BAD_ARGS", "库名不合法（小写字母 / 数字 / 短横线，2–32 位）: " + libId);
            }
            lock (_writeGate)
            {
                List<DbLib> libs = new List<DbLib>();
                string indexErr = ReadIndex(libs);
                if (indexErr.Length > 0)
                {
                    return DbResult.Fail("DB_INDEX_UNREADABLE", indexErr);
                }
                if (FindLib(libs, lib) != null)
                {
                    return DbResult.Fail("DB_LIB_EXISTS", "库已存在: " + lib);
                }
                string stamp = NowStamp();
                string libDir = Path.Combine(_dbRoot, lib);
                // 目录 + 元信息 + 头行——头行此后不改（格式判据）
                try
                {
                    Directory.CreateDirectory(libDir);
                    ConfigStore.AtomicWrite(Path.Combine(libDir, MetaFile), JsonUtil.Object(("lib", lib), ("fmt", FormatSupport), ("createdAt", stamp)));
                    ConfigStore.AtomicWrite(Path.Combine(libDir, EntriesFile), JsonUtil.Object(("_", "db.entries"), ("fmt", FormatSupport), ("lib", lib)) + "\n");
                }
                catch (Exception ex)
                {
                    return DbResult.Fail("DB_IO_FAIL", "建库落盘失败: " + ex.Message);
                }
                libs.Add(new DbLib() { Id = lib, CreatedAt = stamp });
                string writeErr = WriteIndex(libs);
                if (writeErr.Length > 0)
                {
                    return DbResult.Fail("DB_IO_FAIL", writeErr);
                }
                return DbResult.Success("库已建立: " + lib, JsonUtil.Object(("id", lib), ("createdAt", stamp)));
            }
        }

        // [段2] 写入
        /// <summary>
        /// 新增条目——分配库内自增 ID（当前最大 ID + 1，含墓碑行），标题库内唯一。
        /// </summary>
        /// <param name="libId">库名</param>
        /// <param name="title">标题（≤50 字符，库内唯一）</param>
        /// <param name="body">正文（≤5000 字符）</param>
        /// <returns>结果（载荷含 ID 与版本号）</returns>
        public DbResult Put(string libId, string title, string body)
        {
            string titleText = TrimText(title);
            string bodyText = body == null ? "" : body;
            lock (_writeGate)
            {
                List<DbEntry> all = new List<DbEntry>();
                int badLines = 0;
                DbResult? openFail = OpenLib(libId, all, ref badLines);
                if (openFail != null)
                {
                    return openFail;
                }
                DbResult? textFail = ValidateTitleBody(titleText, bodyText, true);
                if (textFail != null)
                {
                    return textFail;
                }
                if (FindActiveByTitle(all, titleText) != null)
                {
                    return DbResult.Fail("DB_TITLE_EXISTS", "标题已存在: " + titleText);
                }
                int nextId = 1;
                for (int i = 0; i < all.Count; i = i + 1)
                {
                    if (all[i].Id >= nextId)
                    {
                        nextId = all[i].Id + 1;
                    }
                }
                string stamp = NowStamp();
                DbEntry entry = new DbEntry();
                entry.Id = nextId;
                entry.Title = titleText;
                entry.Body = bodyText;
                entry.V = 1;
                entry.Del = 0;
                entry.CreatedAt = stamp;
                entry.UpdatedAt = stamp;
                string appendErr = AppendEntry(libId, entry);
                if (appendErr.Length > 0)
                {
                    return DbResult.Fail("DB_IO_FAIL", appendErr);
                }
                return DbResult.Success("条目已写入: #" + nextId.ToString(), JsonUtil.Object(("id", nextId), ("v", 1)), badLines);
            }
        }

        /// <summary>
        /// 更新条目正文——按标题定位，追加 v+1 新版本行（标题不可改）。
        /// </summary>
        /// <param name="libId">库名</param>
        /// <param name="title">标题（定位键，完全匹配）</param>
        /// <param name="body">新正文（≤5000 字符）</param>
        /// <returns>结果（载荷含 ID 与新版本号）</returns>
        public DbResult Update(string libId, string title, string body)
        {
            string titleText = TrimText(title);
            string bodyText = body == null ? "" : body;
            lock (_writeGate)
            {
                List<DbEntry> all = new List<DbEntry>();
                int badLines = 0;
                DbResult? openFail = OpenLib(libId, all, ref badLines);
                if (openFail != null)
                {
                    return openFail;
                }
                DbResult? textFail = ValidateTitleBody(titleText, bodyText, false);
                if (textFail != null)
                {
                    return textFail;
                }
                DbEntry? current = FindActiveByTitle(all, titleText);
                if (current == null)
                {
                    return DbResult.Fail("DB_TITLE_NOT_FOUND", "标题不存在: " + titleText);
                }
                DbEntry entry = new DbEntry();
                entry.Id = current.Id;
                entry.Title = current.Title;
                entry.Body = bodyText;
                entry.V = current.V + 1;
                entry.Del = 0;
                entry.CreatedAt = current.CreatedAt;
                entry.UpdatedAt = NowStamp();
                string appendErr = AppendEntry(libId, entry);
                if (appendErr.Length > 0)
                {
                    return DbResult.Fail("DB_IO_FAIL", appendErr);
                }
                return DbResult.Success("条目已更新: #" + entry.Id.ToString() + " v" + entry.V.ToString(), JsonUtil.Object(("id", entry.Id), ("v", entry.V)), badLines);
            }
        }

        /// <summary>
        /// 删除条目——按标题定位，追加墓碑行（物理行保留可审计；标题随即释放）。
        /// </summary>
        /// <param name="libId">库名</param>
        /// <param name="title">标题（定位键，完全匹配）</param>
        /// <returns>结果（载荷含 ID）</returns>
        public DbResult Delete(string libId, string title)
        {
            string titleText = TrimText(title);
            lock (_writeGate)
            {
                List<DbEntry> all = new List<DbEntry>();
                int badLines = 0;
                DbResult? openFail = OpenLib(libId, all, ref badLines);
                if (openFail != null)
                {
                    return openFail;
                }
                if (titleText.Length == 0)
                {
                    return DbResult.Fail("BAD_ARGS", "标题不能为空");
                }
                DbEntry? current = FindActiveByTitle(all, titleText);
                if (current == null)
                {
                    return DbResult.Fail("DB_TITLE_NOT_FOUND", "标题不存在: " + titleText);
                }
                DbEntry entry = new DbEntry();
                entry.Id = current.Id;
                entry.Title = current.Title;
                entry.Body = current.Body;
                entry.V = current.V + 1;
                entry.Del = 1;
                entry.CreatedAt = current.CreatedAt;
                entry.UpdatedAt = NowStamp();
                string appendErr = AppendEntry(libId, entry);
                if (appendErr.Length > 0)
                {
                    return DbResult.Fail("DB_IO_FAIL", appendErr);
                }
                return DbResult.Success("条目已删除（墓碑保留）: #" + entry.Id.ToString(), JsonUtil.Object(("id", entry.Id)), badLines);
            }
        }

        // [段3] 读取
        /// <summary>
        /// 查询条目——标题子串匹配（忽略大小写），返回 ID / 标题 / 更新时间，不含正文；按更新时间倒序。
        /// </summary>
        /// <param name="libId">库名</param>
        /// <param name="keyword">关键词（空 = 全库列表）</param>
        /// <param name="limit">返回条数（1–200；≤0 取默认 10）</param>
        /// <returns>结果（载荷含 total / matched / shown / items 三数自证）</returns>
        public DbResult Query(string libId, string keyword, int limit)
        {
            int take = limit;
            if (take <= 0)
            {
                take = DefaultLimit;
            }
            if (take > LimitMax)
            {
                return DbResult.Fail("BAD_ARGS", "limit 超上限（1–" + LimitMax.ToString() + "）: " + limit.ToString());
            }
            List<DbEntry> all = new List<DbEntry>();
            int badLines = 0;
            DbResult? openFail = OpenLib(libId, all, ref badLines);
            if (openFail != null)
            {
                return openFail;
            }
            string needle = TrimText(keyword);
            List<DbEntry> active = new List<DbEntry>();
            for (int i = 0; i < all.Count; i = i + 1)
            {
                if (all[i].Del == 0)
                {
                    active.Add(all[i]);
                }
            }
            List<DbEntry> matched = new List<DbEntry>();
            for (int i = 0; i < active.Count; i = i + 1)
            {
                if (needle.Length == 0 || active[i].Title.IndexOf(needle, StringComparison.OrdinalIgnoreCase) >= 0)
                {
                    matched.Add(active[i]);
                }
            }
            matched.Sort(CompareEntryDesc);
            int shown = matched.Count;
            if (shown > take)
            {
                shown = take;
            }
            List<string> items = new List<string>();
            for (int i = 0; i < shown; i = i + 1)
            {
                items.Add(JsonUtil.Object(("id", matched[i].Id), ("title", matched[i].Title), ("updatedAt", matched[i].UpdatedAt)));
            }
            string payload = JsonUtil.Object(("total", active.Count), ("matched", matched.Count), ("shown", shown), ("items", JsonUtil.RawArray(items.ToArray())));
            return DbResult.Success("命中 " + matched.Count.ToString() + " / 活跃 " + active.Count.ToString(), payload, badLines);
        }

        /// <summary>
        /// 取单条全文——按标题完全匹配或按 ID 取（墓碑行视为不存在）。
        /// </summary>
        /// <param name="libId">库名</param>
        /// <param name="mode">定位模式（title | id）</param>
        /// <param name="key">标题文本或 ID 文本</param>
        /// <returns>结果（载荷为完整条目记录）</returns>
        public DbResult Get(string libId, string mode, string key)
        {
            string useMode = TrimText(mode).ToLowerInvariant();
            if (useMode.Length == 0)
            {
                useMode = "title";
            }
            if (useMode != "title" && useMode != "id")
            {
                return DbResult.Fail("BAD_ARGS", "mode 非枚举（title|id）: " + mode);
            }
            List<DbEntry> all = new List<DbEntry>();
            int badLines = 0;
            DbResult? openFail = OpenLib(libId, all, ref badLines);
            if (openFail != null)
            {
                return openFail;
            }
            string keyText = TrimText(key);
            DbEntry? found = null;
            if (useMode == "title")
            {
                found = FindActiveByTitle(all, keyText);
            }
            else
            {
                int id = 0;
                if (int.TryParse(keyText, out id))
                {
                    found = FindActiveById(all, id);
                }
            }
            if (found == null)
            {
                return DbResult.Fail("DB_ENTRY_NOT_FOUND", "条目未命中（" + useMode + "=" + keyText + "）");
            }
            string payload = JsonUtil.Object(
                ("id", found.Id),
                ("title", found.Title),
                ("body", found.Body),
                ("v", found.V),
                ("createdAt", found.CreatedAt),
                ("updatedAt", found.UpdatedAt));
            return DbResult.Success("条目 #" + found.Id.ToString(), payload, badLines);
        }

        /// <summary>
        /// 库清单——注册表登记的库 + 活跃条目计数（控制台库下拉与端点用）。
        /// </summary>
        /// <returns>结果（载荷为 libs 数组）</returns>
        public DbResult ListLibs()
        {
            List<DbLib> libs = new List<DbLib>();
            string indexErr = ReadIndex(libs);
            if (indexErr.Length > 0)
            {
                return DbResult.Fail("DB_INDEX_UNREADABLE", indexErr);
            }
            List<string> items = new List<string>();
            for (int i = 0; i < libs.Count; i = i + 1)
            {
                List<DbEntry> all = new List<DbEntry>();
                int badLines = 0;
                DbResult? openFail = OpenLib(libs[i].Id, all, ref badLines);
                int active = 0;
                if (openFail == null)
                {
                    for (int j = 0; j < all.Count; j = j + 1)
                    {
                        if (all[j].Del == 0)
                        {
                            active = active + 1;
                        }
                    }
                }
                items.Add(JsonUtil.Object(("id", libs[i].Id), ("createdAt", libs[i].CreatedAt), ("entries", active), ("broken", openFail != null)));
            }
            return DbResult.Success("库 " + libs.Count.ToString(), JsonUtil.Object(("libs", JsonUtil.RawArray(items.ToArray()))));
        }

        // [段5] 内部——打开与追加
        /// <summary>
        /// 打开库并载入条目聚合——库名形态 → 注册表登记 → 条目文件读取（三关全过才返回 null）。
        /// </summary>
        /// <param name="libId">库名</param>
        /// <param name="all">输出：按 ID 聚合后的最新版本条目（含墓碑）</param>
        /// <param name="badLines">坏行计数（引用累加）</param>
        /// <returns>失败结果；成功返回 null</returns>
        private DbResult? OpenLib(string libId, List<DbEntry> all, ref int badLines)
        {
            string lib = NormalizeLibId(libId);
            if (!LibIdPattern.IsMatch(lib))
            {
                return DbResult.Fail("BAD_ARGS", "库名不合法（小写字母 / 数字 / 短横线，2–32 位）: " + libId);
            }
            List<DbLib> libs = new List<DbLib>();
            string indexErr = ReadIndex(libs);
            if (indexErr.Length > 0)
            {
                return DbResult.Fail("DB_INDEX_UNREADABLE", indexErr);
            }
            if (FindLib(libs, lib) == null)
            {
                return DbResult.Fail("DB_LIB_NOT_FOUND", "库未登记: " + lib);
            }
            string readErr = ReadEntries(lib, all, ref badLines);
            if (readErr.Length > 0)
            {
                return DbResult.Fail("DB_FORMAT_UNSUPPORTED", readErr);
            }
            return null;
        }

        /// <summary>
        /// 读取条目文件并按 ID 聚合——同 ID 多行取 v 最大者；坏行具名出声后跳过（不中断全库）。
        /// </summary>
        /// <param name="lib">库名（已归一）</param>
        /// <param name="all">输出：聚合后的条目（含墓碑；按 ID 升序）</param>
        /// <param name="badLines">坏行计数（引用累加）</param>
        /// <returns>错误文案（空 = 成功；头行或文件异常导致整库拒绝）</returns>
        private string ReadEntries(string lib, List<DbEntry> all, ref int badLines)
        {
            string path = Path.Combine(Path.Combine(_dbRoot, lib), EntriesFile);
            if (!File.Exists(path))
            {
                return "库条目文件缺失: " + path;
            }
            string[] lines = new string[0];
            try
            {
                lines = File.ReadAllLines(path);
            }
            catch (Exception ex)
            {
                return "库条目文件读取失败: " + ex.Message;
            }
            if (lines.Length == 0)
            {
                return "库条目文件为空（缺头行）: " + path;
            }
            // 头行校验——格式判据，不符整库拒绝读取
            JsonDocument? head = JsonUtil.Parse(lines[0], "db.entries.head");
            if (head == null)
            {
                return "库头行解析失败（格式不符）: " + path;
            }
            try
            {
                int headFmt = JsonUtil.GetInt(head.RootElement, "fmt", 0);
                string headKind = JsonUtil.GetStr(head.RootElement, "_", "");
                if (headFmt != FormatSupport || headKind != "db.entries")
                {
                    return "库头行格式不符（期望 db.entries fmt=" + FormatSupport.ToString() + "）: " + path;
                }
            }
            finally
            {
                head.Dispose();
            }
            Dictionary<int, DbEntry> latest = new Dictionary<int, DbEntry>();
            for (int i = 1; i < lines.Length; i = i + 1)
            {
                string text = lines[i];
                if (text.Trim().Length == 0)
                {
                    continue;
                }
                string lineErr = "";
                DbEntry? entry = ParseEntry(text, out lineErr);
                if (entry == null)
                {
                    badLines = badLines + 1;
                    LogStore.Add("Db", 2, "条目行解析失败已跳过 | lib=" + lib + " | 行=" + (i + 1).ToString() + " | " + lineErr, "DB");
                    continue;
                }
                DbEntry? known = null;
                if (latest.TryGetValue(entry.Id, out known))
                {
                    if (entry.V > known.V)
                    {
                        latest[entry.Id] = entry;
                    }
                }
                else
                {
                    latest[entry.Id] = entry;
                }
            }
            all.AddRange(latest.Values);
            all.Sort(CompareEntryAsc);
            return "";
        }

        /// <summary>
        /// 解析一行条目记录——坏行返回 null 并给出原因（调用方出声跳过）。
        /// </summary>
        /// <param name="text">行文本</param>
        /// <param name="error">输出：失败原因</param>
        /// <returns>条目；坏行 null</returns>
        private DbEntry? ParseEntry(string text, out string error)
        {
            error = "";
            JsonDocument? doc = JsonUtil.Parse(text, "db.entries.line");
            if (doc == null)
            {
                error = "JSON 解析失败";
                return null;
            }
            try
            {
                JsonElement el = doc.RootElement;
                if (el.ValueKind != JsonValueKind.Object)
                {
                    error = "行不是对象";
                    return null;
                }
                int id = JsonUtil.GetInt(el, "id", 0);
                string title = JsonUtil.GetStr(el, "title", "");
                if (id <= 0 || title.Length == 0)
                {
                    error = "缺 id 或 title";
                    return null;
                }
                DbEntry entry = new DbEntry();
                entry.Id = id;
                entry.Title = title;
                entry.Body = JsonUtil.GetStr(el, "body", "");
                entry.V = JsonUtil.GetInt(el, "v", 1);
                entry.Del = JsonUtil.GetInt(el, "del", 0);
                entry.CreatedAt = JsonUtil.GetStr(el, "createdAt", "");
                entry.UpdatedAt = JsonUtil.GetStr(el, "updatedAt", "");
                return entry;
            }
            catch (Exception ex)
            {
                error = ex.Message;
                return null;
            }
            finally
            {
                doc.Dispose();
            }
        }

        /// <summary>
        /// 追加一行条目记录——UTF-8 无 BOM + LF，一次写整行。
        /// </summary>
        /// <param name="lib">库名（已归一）</param>
        /// <param name="entry">条目</param>
        /// <returns>错误文案（空 = 成功）</returns>
        private string AppendEntry(string lib, DbEntry entry)
        {
            string path = Path.Combine(Path.Combine(_dbRoot, lib), EntriesFile);
            string line = JsonUtil.Object(
                ("id", entry.Id),
                ("title", entry.Title),
                ("body", entry.Body),
                ("v", entry.V),
                ("del", entry.Del),
                ("createdAt", entry.CreatedAt),
                ("updatedAt", entry.UpdatedAt));
            try
            {
                byte[] bytes = new UTF8Encoding(false).GetBytes(line + "\n");
                using (FileStream stream = new FileStream(path, FileMode.Append, FileAccess.Write, FileShare.Read))
                {
                    stream.Write(bytes, 0, bytes.Length);
                    stream.Flush();
                }
            }
            catch (Exception ex)
            {
                return "条目追加失败: " + ex.Message;
            }
            return "";
        }

        // [段6] 内部——注册表与工具
        /// <summary>
        /// 读库注册表——文件缺失视为空库表（首次建库形态）；解析失败或格式不符整表拒绝。
        /// </summary>
        /// <param name="libs">输出：库清单</param>
        /// <returns>错误文案（空 = 成功）</returns>
        private string ReadIndex(List<DbLib> libs)
        {
            string path = Path.Combine(_dbRoot, IndexFile);
            if (!File.Exists(path))
            {
                return "";
            }
            string text = "";
            try
            {
                text = File.ReadAllText(path);
            }
            catch (Exception ex)
            {
                return "库注册表读取失败: " + ex.Message;
            }
            JsonDocument? doc = JsonUtil.Parse(text, "db.index");
            if (doc == null)
            {
                return "库注册表解析失败: " + path;
            }
            try
            {
                JsonElement el = doc.RootElement;
                int fmt = JsonUtil.GetInt(el, "fmt", 0);
                if (fmt != FormatSupport)
                {
                    return "库注册表格式不符（fmt=" + fmt.ToString() + "，期望 " + FormatSupport.ToString() + "）";
                }
                JsonElement libsEl;
                if (el.TryGetProperty("libs", out libsEl) && libsEl.ValueKind == JsonValueKind.Array)
                {
                    for (int i = 0; i < libsEl.GetArrayLength(); i = i + 1)
                    {
                        JsonElement item = libsEl[i];
                        if (item.ValueKind != JsonValueKind.Object)
                        {
                            continue;
                        }
                        DbLib lib = new DbLib();
                        lib.Id = JsonUtil.GetStr(item, "id", "");
                        lib.CreatedAt = JsonUtil.GetStr(item, "createdAt", "");
                        if (lib.Id.Length > 0)
                        {
                            libs.Add(lib);
                        }
                    }
                }
            }
            catch (Exception ex)
            {
                return "库注册表解析失败: " + ex.Message;
            }
            finally
            {
                doc.Dispose();
            }
            return "";
        }

        /// <summary>
        /// 写库注册表——原子写（先落临时文件再替换）。
        /// </summary>
        /// <param name="libs">库清单</param>
        /// <returns>错误文案（空 = 成功）</returns>
        private string WriteIndex(List<DbLib> libs)
        {
            List<string> items = new List<string>();
            for (int i = 0; i < libs.Count; i = i + 1)
            {
                items.Add(JsonUtil.Object(("id", libs[i].Id), ("createdAt", libs[i].CreatedAt)));
            }
            string json = JsonUtil.Object(("fmt", FormatSupport), ("libs", JsonUtil.RawArray(items.ToArray())));
            try
            {
                ConfigStore.AtomicWrite(Path.Combine(_dbRoot, IndexFile), json);
            }
            catch (Exception ex)
            {
                return "库注册表写入失败: " + ex.Message;
            }
            return "";
        }

        /// <summary>
        /// 按 ID 查库登记——大小写不敏感；未登记返回 null。
        /// </summary>
        /// <param name="libs">库清单</param>
        /// <param name="lib">库名</param>
        /// <returns>库登记；未命中 null</returns>
        private static DbLib? FindLib(List<DbLib> libs, string lib)
        {
            for (int i = 0; i < libs.Count; i = i + 1)
            {
                if (string.Equals(libs[i].Id, lib, StringComparison.OrdinalIgnoreCase))
                {
                    return libs[i];
                }
            }
            return null;
        }

        /// <summary>
        /// 按标题查活跃条目——完全匹配（区分大小写，与写入键一致）；未命中返回 null。
        /// </summary>
        /// <param name="all">聚合条目</param>
        /// <param name="title">标题</param>
        /// <returns>条目；未命中 null</returns>
        private static DbEntry? FindActiveByTitle(List<DbEntry> all, string title)
        {
            for (int i = 0; i < all.Count; i = i + 1)
            {
                if (all[i].Del == 0 && string.Equals(all[i].Title, title, StringComparison.Ordinal))
                {
                    return all[i];
                }
            }
            return null;
        }

        /// <summary>
        /// 按 ID 查活跃条目——墓碑行视为不存在。
        /// </summary>
        /// <param name="all">聚合条目</param>
        /// <param name="id">条目 ID</param>
        /// <returns>条目；未命中 null</returns>
        private static DbEntry? FindActiveById(List<DbEntry> all, int id)
        {
            for (int i = 0; i < all.Count; i = i + 1)
            {
                if (all[i].Del == 0 && all[i].Id == id)
                {
                    return all[i];
                }
            }
            return null;
        }

        /// <summary>
        /// 标题与正文硬限校验——按 Unicode 字符数计（非字节）。
        /// </summary>
        /// <param name="title">标题</param>
        /// <param name="body">正文</param>
        /// <param name="checkTitle">是否校验标题（更新 / 删除时标题只作定位键）</param>
        /// <returns>失败结果；通过返回 null</returns>
        private static DbResult? ValidateTitleBody(string title, string body, bool checkTitle)
        {
            if (checkTitle)
            {
                int titleCount = CharCount(title);
                if (titleCount == 0)
                {
                    return DbResult.Fail("BAD_ARGS", "标题不能为空");
                }
                if (titleCount > TitleMax)
                {
                    return DbResult.Fail("DB_TITLE_TOO_LONG", "标题超长（上限 " + TitleMax.ToString() + " 字符，实际 " + titleCount.ToString() + "）");
                }
            }
            int bodyCount = CharCount(body);
            if (bodyCount > BodyMax)
            {
                return DbResult.Fail("DB_BODY_TOO_LONG", "正文超长（上限 " + BodyMax.ToString() + " 字符，实际 " + bodyCount.ToString() + "）");
            }
            return null;
        }

        /// <summary>
        /// Unicode 字符数——按文本元素计数（组合字符 / 代理对算一个字符）。
        /// </summary>
        /// <param name="text">文本</param>
        /// <returns>字符数</returns>
        private static int CharCount(string text)
        {
            if (string.IsNullOrEmpty(text))
            {
                return 0;
            }
            return StringInfo.ParseCombiningCharacters(text).Length;
        }

        /// <summary>
        /// 库名归一——去空白 + 转小写。
        /// </summary>
        /// <param name="libId">原始库名</param>
        /// <returns>归一后库名（空输入返回空串）</returns>
        private static string NormalizeLibId(string libId)
        {
            if (libId == null)
            {
                return "";
            }
            return libId.Trim().ToLowerInvariant();
        }

        /// <summary>
        /// 文本归一——null 转空串并去两端空白。
        /// </summary>
        /// <param name="text">原始文本</param>
        /// <returns>归一后文本</returns>
        private static string TrimText(string text)
        {
            if (text == null)
            {
                return "";
            }
            return text.Trim();
        }

        /// <summary>
        /// 当前时刻——`yyyy-MM-dd HH:mm:ss`（字典序即时间序）。
        /// </summary>
        /// <returns>时刻文本</returns>
        private static string NowStamp()
        {
            return DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss");
        }

        /// <summary>
        /// 查询排序——更新时间倒序，同刻按 ID 倒序（新者在前）。
        /// </summary>
        /// <param name="a">条目 A</param>
        /// <param name="b">条目 B</param>
        /// <returns>比较结果</returns>
        private static int CompareEntryDesc(DbEntry a, DbEntry b)
        {
            int byTime = string.CompareOrdinal(b.UpdatedAt, a.UpdatedAt);
            if (byTime != 0)
            {
                return byTime;
            }
            return b.Id.CompareTo(a.Id);
        }

        /// <summary>
        /// 聚合排序——ID 升序（读面稳定序）。
        /// </summary>
        /// <param name="a">条目 A</param>
        /// <param name="b">条目 B</param>
        /// <returns>比较结果</returns>
        private static int CompareEntryAsc(DbEntry a, DbEntry b)
        {
            return a.Id.CompareTo(b.Id);
        }
    }

    /// <summary>
    /// 库服务结果——统一两态：Ok=true 带载荷 / Ok=false 带错误码与说明。
    /// </summary>
    public sealed class DbResult
    {
        /// <summary>
        /// 成功标志（false = 失败，Code / Message 有效）
        /// </summary>
        public bool Ok;

        /// <summary>
        /// 机器码（成功为空；失败为大写下划线码，如 DB_LIB_NOT_FOUND）
        /// </summary>
        public string Code = "";

        /// <summary>
        /// 说明文本（成功给动作摘要；失败给中文原因）
        /// </summary>
        public string Message = "";

        /// <summary>
        /// 载荷（成功时的 JSON 文本；无载荷为空串）
        /// </summary>
        public string Data = "";

        /// <summary>
        /// 本次读取跳过的坏行数（0 = 无坏行；>0 时已逐行出声）
        /// </summary>
        public int BadLines;

        /// <summary>
        /// 成功结果——动作摘要 + 载荷。
        /// </summary>
        /// <param name="message">动作摘要</param>
        /// <param name="data">载荷 JSON（可空）</param>
        /// <returns>成功结果</returns>
        public static DbResult Success(string message, string data)
        {
            return Success(message, data, 0);
        }

        /// <summary>
        /// 成功结果——附坏行计数。
        /// </summary>
        /// <param name="message">动作摘要</param>
        /// <param name="data">载荷 JSON（可空）</param>
        /// <param name="badLines">跳过的坏行数</param>
        /// <returns>成功结果</returns>
        public static DbResult Success(string message, string data, int badLines)
        {
            DbResult result = new DbResult();
            result.Ok = true;
            result.Message = message;
            if (data == null)
            {
                result.Data = "";
            }
            else
            {
                result.Data = data;
            }
            result.BadLines = badLines;
            return result;
        }

        /// <summary>
        /// 失败结果——机器码 + 中文原因。
        /// </summary>
        /// <param name="code">大写下划线码</param>
        /// <param name="message">中文原因</param>
        /// <returns>失败结果</returns>
        public static DbResult Fail(string code, string message)
        {
            DbResult result = new DbResult();
            result.Ok = false;
            result.Code = code;
            result.Message = message;
            result.Data = "";
            result.BadLines = 0;
            return result;
        }
        /// <summary>
        /// 工具回执文本——成功走「结构化头 + 摘要行 + 载荷」三段；失败走 ERR|CODE|msg。
        /// 形态单点：五个 db-* 执行积木共用（组翻译积木闭包不跨积木引用，故落服务层）。
        /// </summary>
        /// <param name="tool">工具名（db-put 等）</param>
        /// <param name="target">主来源（库名或 库名#ID；空串省略）</param>
        /// <returns>回执文本</returns>
        public string ToToolText(string tool, string target)
        {
            if (!Ok)
            {
                return "ERR|" + Code + "|" + Message;
            }
            string head = "{\"ok\":true,\"tool\":" + JsonUtil.Str(tool);
            if (target != null && target.Length > 0)
            {
                head = head + ",\"target\":" + JsonUtil.Str(target);
            }
            if (BadLines > 0)
            {
                head = head + ",\"badLines\":" + BadLines.ToString();
            }
            head = head + "}";
            string text = head + "\n" + Message;
            if (Data.Length > 0)
            {
                text = text + "\n" + Data;
            }
            return text;
        }
    }

    /// <summary>
    /// 条目聚合态——同一 ID 的多行记录取 v 最大者后的内存形态。
    /// </summary>
    public sealed class DbEntry
    {
        /// <summary>
        /// 库内自增 ID（从 1 起；删除不复用）
        /// </summary>
        public int Id;

        /// <summary>
        /// 标题（库内唯一键，不可改名）
        /// </summary>
        public string Title = "";

        /// <summary>
        /// 正文
        /// </summary>
        public string Body = "";

        /// <summary>
        /// 版本号（从 1 起，每次改 +1）
        /// </summary>
        public int V;

        /// <summary>
        /// 墓碑标记（1 = 已删除，物理行保留）
        /// </summary>
        public int Del;

        /// <summary>
        /// 首次写入时刻
        /// </summary>
        public string CreatedAt = "";

        /// <summary>
        /// 最近写入时刻
        /// </summary>
        public string UpdatedAt = "";
    }

    /// <summary>
    /// 库登记条目（`_index.json` 一行）。
    /// </summary>
    public sealed class DbLib
    {
        /// <summary>
        /// 库名（库根下一层目录名）
        /// </summary>
        public string Id = "";

        /// <summary>
        /// 建库时刻
        /// </summary>
        public string CreatedAt = "";
    }
}
