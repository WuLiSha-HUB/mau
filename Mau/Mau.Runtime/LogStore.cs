using System;
using System.Collections.Generic;

namespace Mau.Runtime
{
    /// <summary>
    /// 日志总账（程序级）——统一观测与时序真源（design-ch4-observe §二）。
    /// 状态经 DataBox scope 存储（BRIK 唯一数据协议）；磁盘投影到 Data/runs/&lt;ts&gt;/ 三文件（log_all.txt/oa_all.txt/err_all.txt）。
    /// 审计事件经 Type 字段并入（audit.* 前缀——O2）；危险等级 Level 1=INFO/2=WARN/3=ERR。
    /// </summary>
    public static class LogStore
    {
        /// <summary>
        /// 日志条目类型——统一观测载体（V2：Type/Payload 字段 + Level 语义 1/2/3）
        /// </summary>
        public struct LogEntry
        {
            /// <summary>
            /// 时间戳
            /// </summary>
            public string Time;

            /// <summary>
            /// 模块名
            /// </summary>
            public string Module;

            /// <summary>
            /// 危险等级——1=INFO / 2=WARN / 3=ERR（旧 0 归一 1）
            /// </summary>
            public int Level;

            /// <summary>
            /// 消息
            /// </summary>
            public string Message;

            /// <summary>
            /// 类别——CHAT/TOOL/CMD/OA/LLM/AUDIT/SYS/"" 等（重复信息面：命令总线/工单系统/工具执行/会话过程）
            /// </summary>
            public string Category;

            /// <summary>
            /// 帧号——写入时全局帧号（FlowRunner 驱动；-1=无帧号来源）
            /// </summary>
            public long Frame;

            /// <summary>
            /// 审计结构化事件名——audit.{source}.{event}（cmd.set / oa.post / flow.register / app.start...）；非审计为 ""
            /// </summary>
            public string Type;

            /// <summary>
            /// 结构化载荷——审计 props 序列化 JSON；非审计为 ""
            /// </summary>
            public string Payload;
        }

        /// <summary>
        /// 总账列表——DataBox scope "log"（内部写入面；读取一律走 Snapshot / SnapshotFrom——读取面收口，不对外暴露可变引用）
        /// </summary>
        private static List<LogEntry> AllLog
        {
            get { return DataBox.GetOrCreate<List<LogEntry>>("log", "entries"); }
        }

        /// <summary>
        /// 总账锁——DataBox scope "log"
        /// </summary>
        public static object Sync
        {
            get { return DataBox.GetOrCreate<object>("log", "sync"); }
        }

        /// <summary>
        /// 读取面快照——锁内复制总账副本，消费面遍历副本、无需再持 Sync（治本读入口）。
        /// </summary>
        /// <param name="tail">大于 0 时只取尾部 N 条；0 取全量</param>
        /// <returns>总账只读副本（元素为值语义 LogEntry，独立于后续写入）</returns>
        public static List<LogEntry> Snapshot(int tail = 0)
        {
            lock (Sync)
            {
                List<LogEntry> all = AllLog;
                int start = 0;
                if (tail > 0 && all.Count > tail)
                {
                    start = all.Count - tail;
                }
                return CopyLogRange(all, start);
            }
        }

        /// <summary>
        /// 增量快照——锁内复制 [fromIndex, endIndex) 区间并回报末端游标，供增量消费方一次取尽。
        /// </summary>
        /// <param name="fromIndex">起始索引（小于 0 收敛到 0；超过总数收敛到末尾）</param>
        /// <param name="endIndex">出参——本次快照覆盖到的末端索引（复制时刻的总数）</param>
        /// <returns>区间只读副本</returns>
        public static List<LogEntry> SnapshotFrom(int fromIndex, out int endIndex)
        {
            lock (Sync)
            {
                List<LogEntry> all = AllLog;
                int start = fromIndex;
                if (start < 0)
                {
                    start = 0;
                }
                if (start > all.Count)
                {
                    start = all.Count;
                }
                endIndex = all.Count;
                return CopyLogRange(all, start);
            }
        }

        /// <summary>
        /// 复制总账区间——锁内调用（start 已收敛）；集中一处避免消费面重复实现。
        /// </summary>
        /// <param name="all">总账引用</param>
        /// <param name="start">起始索引（已收敛到合法区间）</param>
        /// <returns>区间只读副本</returns>
        private static List<LogEntry> CopyLogRange(List<LogEntry> all, int start)
        {
            List<LogEntry> copy = new List<LogEntry>(all.Count - start);
            for (int i = start; i < all.Count; i = i + 1)
            {
                copy.Add(all[i]);
            }
            return copy;
        }

        /// <summary>
        /// 磁盘持久化路径——空=仅内存（V2：log_all.txt 主文件路径）
        /// </summary>
        public static string LogFilePath
        {
            get
            {
                string path;
                if (DataBox.TryGet<string>("log", "filePath", out path))
                {
                    return path;
                }
                return "";
            }
            set { DataBox.Set<string>("log", "filePath", value); }
        }

        /// <summary>
        /// 运行目录——Data/runs/&lt;ts&gt;（三文件写者根；空=未配置）
        /// </summary>
        private static string _runDir = "";

        /// <summary>
        /// 主写者——log_all.txt（全量时序）
        /// </summary>
        private static System.IO.StreamWriter? _logWriter;

        /// <summary>
        /// OA 投影写者——oa_all.txt（category=OA 专属：Dog 生命周期帧序链）
        /// </summary>
        private static System.IO.StreamWriter? _oaWriter;

        /// <summary>
        /// ERR 投影写者——err_all.txt（Level=3 错误专属——排查第一入口）
        /// </summary>
        private static System.IO.StreamWriter? _errWriter;

        /// <summary>
        /// 写盘锁——并发 Add 串行化
        /// </summary>
        private static readonly object _writeGate = new object();

        /// <summary>
        /// 上次 flush 时间——1s 定时刷
        /// </summary>
        private static DateTime _lastFlush = DateTime.MinValue;

        /// <summary>
        /// Console 订阅——非空时每条 Add 打印一行（窗口与 log_all.txt 同构——O4 全量接入）
        /// </summary>
        public static Action<string>? ConsoleSink;

        /// <summary>
        /// 配置磁盘日志文件——单文件兼容模式（V1 行为；O4 后宿主切 ConfigureRuns——三文件）
        /// </summary>
        /// <param name="path">日志文件路径，空=仅内存</param>
        public static void ConfigureLogFile(string path)
        {
            LogFilePath = path;
            if (LogFilePath == null)
            {
                LogFilePath = "";
            }
        }

        /// <summary>
        /// 配置运行目录——三文件写者（design-ch4-observe §三）：log_all.txt（全量）+ oa_all.txt（OA 投影）+ err_all.txt（L3 投影）。
        /// 后续调用幂等——重复配置重开写者（路径变化）。
        /// </summary>
        /// <param name="runDir">Data/runs/&lt;yyyyMMdd_HHmmss&gt;</param>
        public static void ConfigureRuns(string runDir)
        {
            if (runDir == null)
            {
                runDir = "";
            }
            if (runDir.Length == 0)
            {
                return;
            }
            lock (_writeGate)
            {
                CloseWriters();
                LastConfigureError = "";
                // 目录创建失败不抛——返回错误串由调用方换落点重试（启动韧性：观测不可写不得阻断宿主启动）
                try
                {
                    System.IO.Directory.CreateDirectory(runDir);
                }
                catch (System.Exception ex)
                {
                    LastConfigureError = ex.Message;
                    _runDir = "";
                    LogFilePath = "";
                    return;
                }
                _runDir = runDir;
                LogFilePath = System.IO.Path.Combine(runDir, "log_all.txt");
                // 重试打开——瞬时占用（安全软件扫描 / 残留句柄）由退避重试化解；仍失败写诊断文件（失败可见）
                _logWriter = OpenWriterWithRetry(System.IO.Path.Combine(runDir, "log_all.txt"));
                _oaWriter = OpenWriterWithRetry(System.IO.Path.Combine(runDir, "oa_all.txt"));
                _errWriter = OpenWriterWithRetry(System.IO.Path.Combine(runDir, "err_all.txt"));
            }
        }

        /// <summary>
        /// 清空内存总账——测试隔离专用（AuditSerial 集合测试开头调用；运行时不使用）
        /// </summary>
        public static void ClearForTest()
        {
            lock (Sync)
            {
                AllLog.Clear();
            }
        }

        /// <summary>
        /// 写入一条结构化日志（V1 兼容签名——Type/Payload 空；level 0/1 归一 INFO）
        /// </summary>
        /// <param name="module">模块名</param>
        /// <param name="level">危险等级——0/1=INFO 2=WARN 3=ERR</param>
        /// <param name="message">消息</param>
        /// <param name="category">类别——""/CMD/OA/CHAT/TOOL/LLM/AUDIT</param>
        public static void Add(string module, int level, string message, string category)
        {
            Add(module, level, message, category, "", "", 0, -1, false);
        }

        /// <summary>
        /// 写入一条结构化日志（V2 七参——Type/Payload/maxLen；默认帧与落盘）
        /// </summary>
        public static void Add(string module, int level, string message, string category, string type, string payload, int maxLen)
        {
            Add(module, level, message, category, type, payload, maxLen, -1, false);
        }

        /// <summary>
        /// 写入一条结构化日志（V2 全参数——统一观测唯一入口）。
        /// 内存总账 + 磁盘三投影（log_all.txt 全量 / oa_all.txt 投影 / err_all.txt 投影）+ Console 订阅打印。
        /// </summary>
        /// <param name="module">模块名</param>
        /// <param name="level">危险等级——0/1=INFO 2=WARN 3=ERR（0 归一 1）</param>
        /// <param name="message">消息（maxLen&gt;0 时超长截断）</param>
        /// <param name="category">类别——CHAT/TOOL/CMD/OA/LLM/AUDIT/SYS/""</param>
        /// <param name="type">审计事件名 audit.*（非审计空串）</param>
        /// <param name="payload">结构化载荷 JSON（非审计空串）</param>
        /// <param name="maxLen">消息截断上限字符——0=不截断；工具结果入 Log 建议 100（设计拍板）</param>
        /// <param name="frame">显式帧号（-1=FlowRunner.GlobalFrame——审计 Record 带调用侧 frame）</param>
        /// <param name="skipDisk">true=仅内存/SSE/Console（不落盘投影——L0-TRACE 类）</param>
        public static void Add(string module, int level, string message, string category, string type, string payload, int maxLen, long frame, bool skipDisk)
        {
            if (level <= 0)
            {
                level = 1;
            }
            if (maxLen > 0 && message != null && message.Length > maxLen)
            {
                message = message.Substring(0, maxLen) + "…[截断:" + message.Length.ToString() + "]";
            }
            if (message == null)
            {
                message = "";
            }
            if (category == null)
            {
                category = "";
            }
            if (type == null)
            {
                type = "";
            }
            if (payload == null)
            {
                payload = "";
            }
            LogEntry entry;
            entry.Time = DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss");
            entry.Module = SafeText(module);
            entry.Level = level;
            entry.Message = message;
            entry.Category = category;
            if (frame >= 0)
            {
                entry.Frame = frame;
            }
            else
            {
                entry.Frame = FlowRunner.GlobalFrame;
            }
            entry.Type = type;
            entry.Payload = payload;
            lock (Sync)
            {
                AllLog.Add(entry);
            }
            // [段1] 磁盘三投影——同锁同批（log_all.txt 全量 + oa_all.txt/err_all.txt 投影）；skipDisk 类不落盘（L0-TRACE）
            if (!skipDisk && (LogFilePath.Length > 0 || _runDir.Length > 0))
            {
                try
                {
                    lock (_writeGate)
                    {
                        EnsureWriters();
                        string line = FormatLine(entry);
                        if (_logWriter != null)
                        {
                            _logWriter.WriteLine(line);
                        }
                        if (_oaWriter != null && entry.Category == "OA")
                        {
                            _oaWriter.WriteLine(line);
                        }
                        if (_errWriter != null && entry.Level >= 3)
                        {
                            _errWriter.WriteLine(line);
                        }
                        FlushIfDue();
                        RotateIfDue();
                    }
                }
                catch (Exception ex)
                {
                    // 磁盘写失败——内存总账已保留
                    Console.Error.WriteLine("[LogStore] 磁盘投影写入失败: " + ex.Message);
                }
            }
            // [段2] Console 订阅——窗口同构（O4 全量接入后宿主过程行不再直打）；skipDisk 类（L0-TRACE）跳过感官通道
            if (!skipDisk && ConsoleSink != null)
            {
                try
                {
                    ConsoleSink(FormatLine(entry));
                }
                catch (Exception ex)
                {
                    // 订阅异常不影响日志主链
                    Console.Error.WriteLine("[LogStore] Console 订阅回调异常: " + ex.Message);
                }
            }
        }

        /// <summary>
        /// 行格式——F{frame} | L{level} | INFO/WARN/ERR | [{category}] | {message} [payload]（log_all.txt 与窗口同构）
        /// </summary>
        /// <param name="entry">条目</param>
        /// <returns>格式行</returns>
        private static string FormatLine(LogEntry entry)
        {
            string line = entry.Time + " | F" + entry.Frame.ToString() + " | " + LevelText(entry.Level);
            if (entry.Category.Length > 0)
            {
                line = line + " | [" + entry.Category + "]";
            }
            line = line + " | " + entry.Message;
            if (entry.Type.Length > 0)
            {
                line = line + " | #" + entry.Type;
            }
            if (entry.Payload.Length > 0)
            {
                // 审计载荷去裸 JSON——渲染为 k=v 逗号拼接（人读自然化；完整 JSON 仍在内存 LogEntry.Payload 供 AuditQuery 机器查询）
                string summary = SummarizePayload(entry.Payload);
                if (summary.Length > 0)
                {
                    line = line + " | " + summary;
                }
            }
            return line;
        }

        /// <summary>
        /// 级别文本——0/1=INFO 2=WARN >=3=ERR
        /// </summary>
        /// <param name="level">级别数字</param>
        /// <returns>文本</returns>
        public static string LevelText(int level)
        {
            if (level >= 3)
            {
                return "ERR";
            }
            if (level == 2)
            {
                return "WARN";
            }
            return "INFO";
        }

        /// <summary>
        /// 载荷摘要——审计 props JSON（[{"k":..,"v":..}]）渲染为 k=v 逗号拼接（人读自然化；解析失败回落原文截断 120）。
        /// </summary>
        /// <param name="payload">载荷 JSON</param>
        /// <returns>摘要文本</returns>
        private static string SummarizePayload(string payload)
        {
            try
            {
                using (System.Text.Json.JsonDocument doc = JsonUtil.ParseStrict(payload))
                {
                    System.Text.Json.JsonElement root = doc.RootElement;
                    if (root.ValueKind != System.Text.Json.JsonValueKind.Array)
                    {
                        return payload;
                    }
                    System.Text.StringBuilder sb = new System.Text.StringBuilder();
                    for (int i = 0; i < root.GetArrayLength(); i++)
                    {
                        System.Text.Json.JsonElement item = root[i];
                        string k = "";
                        string v = "";
                        System.Text.Json.JsonElement kk;
                        if (item.TryGetProperty("k", out kk) && kk.ValueKind == System.Text.Json.JsonValueKind.String)
                        {
                            string? g = kk.GetString();
                            if (g != null)
                            {
                                k = g;
                            }
                        }
                        System.Text.Json.JsonElement vv;
                        if (item.TryGetProperty("v", out vv) && vv.ValueKind == System.Text.Json.JsonValueKind.String)
                        {
                            string? g = vv.GetString();
                            if (g != null)
                            {
                                v = g;
                            }
                        }
                        if (sb.Length > 0)
                        {
                            sb.Append(", ");
                        }
                        sb.Append(k);
                        sb.Append("=");
                        sb.Append(v);
                    }
                    string result = sb.ToString();
                    if (result.Length > 0)
                    {
                        return result;
                    }
                }
            }
            catch (Exception ex)
            {
                // 解析失败——回落原文
                Console.Error.WriteLine("[LogStore] 载荷解析失败，回落原文: " + ex.Message);
            }
            if (payload.Length > 120)
            {
                return payload.Substring(0, 120) + "...";
            }
            return payload;
        }

        /// <summary>
        /// 打开写者——append + 读共享（不锁文件）
        /// </summary>
        /// <param name="path">文件路径</param>
        /// <returns>写者</returns>
        private static System.IO.StreamWriter OpenWriter(string path)
        {
            System.IO.StreamWriter w = new System.IO.StreamWriter(new System.IO.FileStream(path, System.IO.FileMode.Append, System.IO.FileAccess.Write, System.IO.FileShare.ReadWrite), new System.Text.UTF8Encoding(false));
            // AutoFlush——窗口/长进程尾部滞留修复（1s 批量依赖后续写入触发；无写入则尾部滞留——强杀丢尾判例 2026-08-19）
            w.AutoFlush = true;
            return w;
        }

        /// <summary>
        /// 确保写者存在——单文件兼容模式（ConfigureLogFile 调用过但未 ConfigureRuns）
        /// </summary>
        private static void EnsureWriters()
        {
            if (_logWriter == null && LogFilePath.Length > 0)
            {
                string? dir = System.IO.Path.GetDirectoryName(LogFilePath);
                if (dir != null && dir.Length > 0)
                {
                    System.IO.Directory.CreateDirectory(dir);
                }
                _logWriter = OpenWriter(LogFilePath);
            }
        }

        /// <summary>
        /// 打开写者（带重试与失败上报）——瞬时占用（安全软件扫描 / 残留句柄）由退避重试化解；
        /// 三次仍失败则把原因写入 runs 同级诊断文件（不依赖 runs 目录，保证失败可见——判例 2026-09-14）。
        /// </summary>
        /// <param name="path">目标文件路径</param>
        /// <returns>写者；失败返回 null（调用方按空写者容忍）</returns>
        private static System.IO.StreamWriter? OpenWriterWithRetry(string path)
        {
            int[] delays = new int[] { 100, 300, 700 };
            Exception? last = null;
            for (int i = 0; i <= delays.Length; i = i + 1)
            {
                try
                {
                    return OpenWriter(path);
                }
                catch (Exception ex)
                {
                    last = ex;
                    if (i < delays.Length)
                    {
                        System.Threading.Thread.Sleep(delays[i]);
                    }
                }
            }
            ReportOpenFailure(path, last);
            return null;
        }

        /// <summary>
        /// 上报写者打开失败——append 到 runs 同级目录的 log_open_error.txt（自身失败静默，避免递归）。
        /// </summary>
        /// <param name="path">打开失败的日志文件路径</param>
        /// <param name="error">最后一次异常（可空）</param>
        private static void ReportOpenFailure(string path, Exception? error)
        {
            try
            {
                string? parent = System.IO.Path.GetDirectoryName(_runDir);
                if (parent == null || parent.Length == 0)
                {
                    return;
                }
                string diagnostic = System.IO.Path.Combine(parent, "log_open_error.txt");
                string line = DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss")
                    + " | pid " + System.Diagnostics.Process.GetCurrentProcess().Id.ToString()
                    + " | 打开失败: " + path
                    + " | " + (error == null ? "(无异常)" : error.GetType().Name + ": " + error.Message)
                    + Environment.NewLine;
                using (System.IO.FileStream fs = new System.IO.FileStream(diagnostic,
                    System.IO.FileMode.Append, System.IO.FileAccess.Write, System.IO.FileShare.ReadWrite))
                using (System.IO.StreamWriter sw = new System.IO.StreamWriter(fs, new System.Text.UTF8Encoding(false)))
                {
                    sw.Write(line);
                }
            }
            catch (Exception ex)
            {
                // 诊断通道自身失败——无处可报（不递归）
                Console.Error.WriteLine("[LogStore] 诊断通道写入失败: " + ex.Message);
            }
        }

        /// <summary>单日志文件轮转上限——5MB（R1-P1-02：磁盘三文件防无限膨胀）</summary>
        private const long MaxLogFileBytes = 5L * 1024 * 1024;
        /// <summary>
        /// 上次配置运行目录的结果——空=成功；非空=失败原因。
        /// 调用方据此降级（换落点重试 / 仅内存运行），并登记启动降级项。
        /// </summary>
        public static string LastConfigureError { get; private set; } = "";

        /// <summary>
        /// 轮转检查——任一文件超上限 → 改名归档 + 新建同名继续写（R1-P1-02）。
        /// 内存总账无上限保留（预期设计）；磁盘面收敛。
        /// </summary>
        private static void RotateIfDue()
        {
            try
            {
                if (_logWriter != null && _logWriter.BaseStream.Length >= MaxLogFileBytes)
                {
                    RotateWriter(ref _logWriter, LogFilePath);
                }
                if (_oaWriter != null && _oaWriter.BaseStream.Length >= MaxLogFileBytes)
                {
                    RotateWriter(ref _oaWriter, System.IO.Path.Combine(_runDir, "oa_all.txt"));
                }
                if (_errWriter != null && _errWriter.BaseStream.Length >= MaxLogFileBytes)
                {
                    RotateWriter(ref _errWriter, System.IO.Path.Combine(_runDir, "err_all.txt"));
                }
            }
            catch (Exception ex)
            {
                // 轮转异常——保留现状继续写（磁盘异常不阻断日志主链）
                Console.Error.WriteLine("[LogStore] 日志轮转异常，保留现状继续写: " + ex.Message);
            }
        }

        /// <summary>
        /// 轮转单个写者——关闭 → 改名归档（时间戳后缀）→ 新建同名写者继续写。
        /// </summary>
        /// <param name="writer">写者引用</param>
        /// <param name="path">当前文件路径</param>
        private static void RotateWriter(ref System.IO.StreamWriter writer, string path)
        {
            try
            {
                writer.Flush();
                writer.Dispose();
                string archive = path + "." + DateTime.Now.ToString("yyyyMMdd_HHmmss_fff");
                if (System.IO.File.Exists(archive))
                {
                    archive = archive + "." + Guid.NewGuid().ToString("N").Substring(0, 4);
                }
                System.IO.File.Move(path, archive);
                writer = OpenWriter(path);
            }
            catch (Exception ex)
            {
                // 轮转失败——保留当前写者（下次写入再试；日志主链不受影响）
                Console.Error.WriteLine("[LogStore] 轮转失败，保留当前写者: " + ex.Message);
            }
        }

        /// <summary>
        /// 写者就绪且超过 1s 未 flush——批量落盘
        /// </summary>
        private static void FlushIfDue()
        {
            DateTime now = DateTime.Now;
            if ((now - _lastFlush).TotalSeconds >= 1.0)
            {
                if (_logWriter != null)
                {
                    _logWriter.Flush();
                }
                if (_oaWriter != null)
                {
                    _oaWriter.Flush();
                }
                if (_errWriter != null)
                {
                    _errWriter.Flush();
                }
                _lastFlush = now;
            }
        }

        /// <summary>
        /// 关闭全部写者——宿主退出前调用（幂等）
        /// </summary>
        public static void CloseWriters()
        {
            if (_logWriter != null)
            {
                try
                {
                    _logWriter.Flush();
                }
                catch (Exception ex)
                {
                    // Flush 失败不影响释放（日志内存总账已保留）
                    Console.Error.WriteLine("[LogStore] log_all 写者 Flush 失败: " + ex.Message);
                }
                _logWriter.Dispose();
                _logWriter = null;
            }
            if (_oaWriter != null)
            {
                try
                {
                    _oaWriter.Flush();
                }
                catch (Exception ex)
                {
                    // Flush 失败不影响释放（日志内存总账已保留）
                    Console.Error.WriteLine("[LogStore] oa_all 写者 Flush 失败: " + ex.Message);
                }
                _oaWriter.Dispose();
                _oaWriter = null;
            }
            if (_errWriter != null)
            {
                try
                {
                    _errWriter.Flush();
                }
                catch (Exception ex)
                {
                    // Flush 失败不影响释放（日志内存总账已保留）
                    Console.Error.WriteLine("[LogStore] err_all 写者 Flush 失败: " + ex.Message);
                }
                _errWriter.Dispose();
                _errWriter = null;
            }
        }        /// <summary>
                 /// 把可空文本规范为空字符串
                 /// </summary>
                 /// <param name="value">输入</param>
                 /// <returns>非空文本</returns>
        public static string SafeText(string? value)
        {
            if (value == null)
            {
                return "";
            }
            return value;
        }
    }
}