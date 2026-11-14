using System;
using System.Collections.Generic;
using System.IO;
using System.Net;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// 下载任务实体——任务表行（持久化单元；design-ch4-webfetch §4.1）。
    /// 运行态（取消令牌）不入表——崩溃 / 停机后按 State 与 .part 字节续传。
    /// </summary>
    public sealed class WebFetchTask
    {
        /// <summary>任务号——进程内自增（落盘续号，跨重启唯一）</summary>
        public long Id;

        /// <summary>源地址</summary>
        public string Url = "";

        /// <summary>目标文件名（派生后）</summary>
        public string FileName = "";

        /// <summary>产物绝对路径</summary>
        public string TargetPath = "";

        /// <summary>来源猫（回调注入收件人）</summary>
        public string CatKey = "";

        /// <summary>状态：Pending / Downloading / Completed / Failed / Canceled</summary>
        public string State = "Pending";

        /// <summary>总长（-1 = 未知——服务端未给 Content-Length）</summary>
        public long TotalBytes = -1;

        /// <summary>已收字节</summary>
        public long ReceivedBytes;

        /// <summary>HTTP 状态码（0 = 未响应）</summary>
        public int HttpStatus;

        /// <summary>失败原因（终态；原文）</summary>
        public string Error = "";

        /// <summary>登记时刻——Unix 毫秒</summary>
        public long CreatedAt;

        /// <summary>终结时刻——Unix 毫秒（0 = 未终结）</summary>
        public long FinishedAt;

        /// <summary>通知已产生（终态后置位——防重复投递）</summary>
        public bool Notified;

        /// <summary>通知已投递 / 无需投递（取消类直接置位——不注入）</summary>
        public bool Delivered;
    }

    /// <summary>
    /// 网页资产下载服务——web-fetch / web-fetch-jobs 工具实现（全局单一下载器）。
    /// 形态：全局任务表（全猫共用）+ 每任务一个后台 Task + 静态 HttpClient（不设超时）；
    ///   任务态原子落盘、跨宿主重启续传（Range 续 / 服务端不支持则截断重下）；完成 / 失败回调注入（拒收者留存重试）。
    /// 无隐藏进程：全链 HttpClient + Task，宿主退出即止（Shutdown 取消令牌，半截 .part 留待续传）。
    /// 规格：Project/CH4/design-ch4-webfetch.md
    /// </summary>
    public sealed class WebFetchService : IWebFetchService
    {
        /// <summary>通知泵扫描周期（毫秒）</summary>
        private const int PumpMs = 300;

        /// <summary>读取缓冲（字节）</summary>
        private const int ReadBufferSize = 81920;

        /// <summary>HTTP 客户端——静态复用；不设超时（下载任务无上限，取消走令牌）</summary>
        private static readonly HttpClient Client = CreateClient();

        /// <summary>任务表</summary>
        private readonly List<WebFetchTask> _tasks = new List<WebFetchTask>();

        /// <summary>运行态取消令牌——任务号 → 令牌源（不入表）</summary>
        private readonly Dictionary<long, CancellationTokenSource> _tokens = new Dictionary<long, CancellationTokenSource>();

        /// <summary>表锁——全部读写锁内完成</summary>
        private readonly object _lock = new object();

        /// <summary>任务号自增位</summary>
        private long _nextId = 1;

        /// <summary>任务表落盘路径（空 = 仅内存）</summary>
        private string _storePath = "";

        /// <summary>产物目录（WorkSpace:Downloads）</summary>
        private string _downloadsDir = "";

        /// <summary>完成注入回调（catKey, content）→ 是否受理</summary>
        private Func<string, string, bool> _inject;

        /// <summary>通知泵取消源（空 = 未启动）</summary>
        private CancellationTokenSource _pumpCts;

        /// <summary>
        /// 推入下载任务——登记（含冲突检查）→ 起后台下载 → 立即返回任务号（非阻塞；完成 / 失败走注入回调）。
        /// </summary>
        /// <param name="catKey">来源猫</param>
        /// <param name="url">目标地址（http/https）</param>
        /// <param name="fileName">目标文件名（空 = 按 URL 派生）</param>
        /// <returns>回执文本（失败 ERR| 前缀）</returns>
        public string Fetch(string catKey, string url, string fileName)
        {
            if (catKey == null || catKey.Length == 0)
            {
                return "ERR|DOWNLOAD_ARGS|缺少来源猫（catKey）";
            }
            if (url == null || url.Length == 0)
            {
                return "ERR|DOWNLOAD_BAD_URL|缺少参数 url";
            }
            string scheme = "";
            try
            {
                Uri parsed = new Uri(url, UriKind.Absolute);
                scheme = parsed.Scheme;
            }
            catch (Exception)
            {
                return "ERR|DOWNLOAD_BAD_URL|URL 非法: " + url;
            }
            if (!string.Equals(scheme, "http", StringComparison.OrdinalIgnoreCase) &&
                !string.Equals(scheme, "https", StringComparison.OrdinalIgnoreCase))
            {
                return "ERR|DOWNLOAD_BAD_URL|仅支持 http/https: " + url;
            }
            string name = fileName;
            if (name == null)
            {
                name = "";
            }
            if (name.Length == 0)
            {
                name = DeriveFileName(url);
            }
            string dir = _downloadsDir;
            if (dir.Length == 0)
            {
                return "ERR|DOWNLOAD_TARGET|下载落点未配置（宿主未接线 downloadsDir）";
            }
            string target = Path.Combine(dir, name);
            WebFetchTask task = new WebFetchTask();
            lock (_lock)
            {
                if (File.Exists(target) || File.Exists(target + ".part"))
                {
                    return "ERR|DOWNLOAD_NAME_CONFLICT|目标同名文件已存在: " + name + "（改名或清理后重试）";
                }
                for (int i = 0; i < _tasks.Count; i = i + 1)
                {
                    WebFetchTask row = _tasks[i];
                    if (!IsTerminal(row.State) &&
                        string.Equals(row.Url, url, StringComparison.Ordinal) &&
                        string.Equals(row.TargetPath, target, StringComparison.OrdinalIgnoreCase))
                    {
                        return "ERR|DOWNLOAD_EXISTS|同源同目标任务已在进行: #" + row.Id.ToString();
                    }
                }
                if (!Directory.Exists(dir))
                {
                    try
                    {
                        Directory.CreateDirectory(dir);
                    }
                    catch (Exception ex)
                    {
                        return "ERR|DOWNLOAD_TARGET|下载目录创建失败: " + ex.Message;
                    }
                }
                task.Id = _nextId;
                _nextId = _nextId + 1;
                task.Url = url;
                task.FileName = name;
                task.TargetPath = target;
                task.CatKey = catKey;
                task.State = "Pending";
                task.CreatedAt = NowMs();
                _tasks.Add(task);
                SaveLocked();
            }
            LogStore.Add("CatHome4", 1, "下载登记: id=#" + task.Id.ToString() + " | cat=" + catKey + " | " + url, "WEBFETCH");
            StartTask(task.Id);
            return "任务 #" + task.Id.ToString() + " 已登记 | " + task.FileName + " | 下载中（完成或失败时系统提示）→ " + task.TargetPath;
        }

        /// <summary>
        /// 任务管理——list（全部任务）/ status（单任务详情）/ cancel（取消进行中任务）。
        /// </summary>
        /// <param name="action">动作（空 = list）</param>
        /// <param name="id">任务号（status / cancel 必填）</param>
        /// <returns>清单 / 详情 / 动作结果（失败 ERR| 前缀）</returns>
        public string Jobs(string action, string id)
        {
            string act = action;
            if (act == null || act.Length == 0)
            {
                act = "list";
            }
            if (string.Equals(act, "list", StringComparison.Ordinal))
            {
                lock (_lock)
                {
                    if (_tasks.Count == 0)
                    {
                        return "（无下载任务）";
                    }
                    StringBuilder sb = new StringBuilder();
                    for (int i = 0; i < _tasks.Count; i = i + 1)
                    {
                        if (i > 0)
                        {
                            sb.Append("\n");
                        }
                        sb.Append(ListLine(_tasks[i]));
                    }
                    return sb.ToString();
                }
            }
            long taskId;
            if (!long.TryParse(id, out taskId))
            {
                return "ERR|DOWNLOAD_ARGS|缺少或非法参数 id（status / cancel 必填任务号）";
            }
            if (string.Equals(act, "status", StringComparison.Ordinal))
            {
                lock (_lock)
                {
                    WebFetchTask row = FindLocked(taskId);
                    if (row == null)
                    {
                        return "ERR|DOWNLOAD_NO_TASK|任务不存在: #" + taskId.ToString();
                    }
                    return DetailText(row);
                }
            }
            if (string.Equals(act, "cancel", StringComparison.Ordinal))
            {
                return CancelTask(taskId);
            }
            return "ERR|DOWNLOAD_ARGS|未知动作: " + act + "（list|status|cancel）";
        }

        /// <summary>
        /// 配置接线——任务表落盘路径 + 产物目录 + 注入回调（宿主启动期注入）。
        /// </summary>
        /// <param name="storePath">任务表路径（downloads.json；空 = 仅内存）</param>
        /// <param name="downloadsDir">产物目录</param>
        /// <param name="inject">注入回调（catKey, content）→ 是否受理</param>
        public void Configure(string storePath, string downloadsDir, Func<string, string, bool> inject)
        {
            lock (_lock)
            {
                _storePath = storePath == null ? "" : storePath;
                _downloadsDir = downloadsDir == null ? "" : downloadsDir;
            }
            _inject = inject;
        }

        /// <summary>
        /// 启动恢复——载入任务表 → 未完成任务续传 → 起通知泵。
        /// </summary>
        public void Start()
        {
            Load();
            List<long> resume = new List<long>();
            lock (_lock)
            {
                for (int i = 0; i < _tasks.Count; i = i + 1)
                {
                    if (!IsTerminal(_tasks[i].State))
                    {
                        resume.Add(_tasks[i].Id);
                    }
                }
            }
            for (int i = 0; i < resume.Count; i = i + 1)
            {
                LogStore.Add("CatHome4", 1, "下载续传: id=#" + resume[i].ToString(), "WEBFETCH");
                StartTask(resume[i]);
            }
            _pumpCts = new CancellationTokenSource();
            CancellationToken token = _pumpCts.Token;
            Task.Run(delegate { PumpLoop(token); });
        }

        /// <summary>
        /// 收尾——停通知泵 + 取消全部在途任务（不置终态：半截 .part 留待下次续传）。
        /// </summary>
        public void Shutdown()
        {
            if (_pumpCts != null)
            {
                try
                {
                    _pumpCts.Cancel();
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 2, "下载通知泵取消异常: " + ex.Message, "WEBFETCH");
                }
                _pumpCts = null;
            }
            List<CancellationTokenSource> list = new List<CancellationTokenSource>();
            lock (_lock)
            {
                foreach (KeyValuePair<long, CancellationTokenSource> kv in _tokens)
                {
                    list.Add(kv.Value);
                }
                _tokens.Clear();
            }
            for (int i = 0; i < list.Count; i = i + 1)
            {
                try
                {
                    list[i].Cancel();
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 2, "下载任务取消异常: " + ex.Message, "WEBFETCH");
                }
            }
        }

        /// <summary>
        /// 起后台下载——登记取消令牌并投递线程池（同任务不重复起）。
        /// </summary>
        /// <param name="taskId">任务号</param>
        private void StartTask(long taskId)
        {
            CancellationTokenSource cts = new CancellationTokenSource();
            lock (_lock)
            {
                if (_tokens.ContainsKey(taskId))
                {
                    return;
                }
                _tokens[taskId] = cts;
            }
            CancellationToken token = cts.Token;
            Task.Run(async delegate { await RunTaskAsync(taskId, token); });
        }

        /// <summary>
        /// 后台下载主体——请求（带 Range 续传）→ 流式落 .part → 成功后改名 → 置终态。
        /// </summary>
        /// <param name="taskId">任务号</param>
        /// <param name="token">取消令牌</param>
        private async Task RunTaskAsync(long taskId, CancellationToken token)
        {
            WebFetchTask task;
            lock (_lock)
            {
                task = FindLocked(taskId);
                if (task == null)
                {
                    return;
                }
                task.State = "Downloading";
                SaveLocked();
            }
            string part = task.TargetPath + ".part";
            bool ok = false;
            string failReason = "";
            try
            {
                long existing = 0;
                if (File.Exists(part))
                {
                    existing = new FileInfo(part).Length;
                }
                HttpRequestMessage request = new HttpRequestMessage(HttpMethod.Get, task.Url);
                if (existing > 0)
                {
                    request.Headers.Range = new RangeHeaderValue(existing, null);
                }
                HttpResponseMessage response = await Client.SendAsync(request, HttpCompletionOption.ResponseHeadersRead, token).ConfigureAwait(false);
                int status = (int)response.StatusCode;
                lock (_lock)
                {
                    task.HttpStatus = status;
                }
                if (!response.IsSuccessStatusCode)
                {
                    failReason = "HTTP " + status.ToString() + " " + response.ReasonPhrase;
                    response.Dispose();
                    Finish(taskId, false, failReason);
                    return;
                }
                bool append = existing > 0 && response.StatusCode == HttpStatusCode.PartialContent;
                long start = append ? existing : 0;
                long? contentLength = response.Content.Headers.ContentLength;
                long total = -1;
                if (contentLength.HasValue)
                {
                    total = start + contentLength.Value;
                }
                lock (_lock)
                {
                    task.TotalBytes = total;
                    task.ReceivedBytes = start;
                }
                using (Stream source = await response.Content.ReadAsStreamAsync(token).ConfigureAwait(false))
                using (FileStream sink = new FileStream(part, append ? FileMode.Append : FileMode.Create, FileAccess.Write, FileShare.None))
                {
                    byte[] buffer = new byte[ReadBufferSize];
                    while (true)
                    {
                        int read = await source.ReadAsync(buffer, 0, buffer.Length, token).ConfigureAwait(false);
                        if (read <= 0)
                        {
                            break;
                        }
                        await sink.WriteAsync(buffer, 0, read, token).ConfigureAwait(false);
                        lock (_lock)
                        {
                            task.ReceivedBytes = task.ReceivedBytes + read;
                        }
                    }
                }
                response.Dispose();
                token.ThrowIfCancellationRequested();
                if (File.Exists(task.TargetPath))
                {
                    File.Delete(task.TargetPath);
                }
                File.Move(part, task.TargetPath);
                ok = true;
            }
            catch (OperationCanceledException)
            {
                failReason = "已取消";
            }
            catch (Exception ex)
            {
                failReason = ex.GetType().Name + ": " + ex.Message;
            }
            Finish(taskId, ok, failReason);
        }

        /// <summary>
        /// 置终态——成功 / 失败 / 取消三态收口 + 终结时刻 + 落盘 + 日志；取消令牌出表。
        /// </summary>
        /// <param name="taskId">任务号</param>
        /// <param name="ok">是否成功</param>
        /// <param name="failReason">失败原因（成功时空）</param>
        private void Finish(long taskId, bool ok, string failReason)
        {
            string state = "";
            string fileName = "";
            string errorText = "";
            long received = 0;
            lock (_lock)
            {
                WebFetchTask task = FindLocked(taskId);
                if (task == null)
                {
                    return;
                }
                _tokens.Remove(taskId);
                if (string.Equals(task.State, "Canceled", StringComparison.Ordinal))
                {
                    task.FinishedAt = NowMs();
                    SaveLocked();
                    return;
                }
                if (ok)
                {
                    task.State = "Completed";
                }
                else
                {
                    task.State = "Failed";
                    task.Error = failReason == null ? "" : failReason;
                }
                task.FinishedAt = NowMs();
                SaveLocked();
                state = task.State;
                fileName = task.FileName;
                errorText = task.Error;
                received = task.ReceivedBytes;
            }
            if (string.Equals(state, "Completed", StringComparison.Ordinal))
            {
                LogStore.Add("CatHome4", 1, "下载完成: id=#" + taskId.ToString() + " | " + fileName + " | " + FormatBytes(received), "WEBFETCH");
            }
            else
            {
                LogStore.Add("CatHome4", 3, "下载失败: id=#" + taskId.ToString() + " | " + fileName + " | " + errorText, "WEBFETCH");
            }
        }

        /// <summary>
        /// 通知泵——周期调度单次扫描。
        /// </summary>
        /// <param name="token">泵取消令牌</param>
        private void PumpLoop(CancellationToken token)
        {
            while (!token.IsCancellationRequested)
            {
                try
                {
                    PumpOnce();
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 3, "下载通知泵异常: " + ex.GetType().Name + ": " + ex.Message, "WEBFETCH");
                }
                try
                {
                    Task.Delay(PumpMs, token).GetAwaiter().GetResult();
                }
                catch (Exception)
                {
                    break;
                }
            }
        }

        /// <summary>
        /// 单次扫描——终态即产生通知（取消类直接标为无需投递）；未投递通知就地投递，拒收者留存下轮重试。
        /// </summary>
        private void PumpOnce()
        {
            List<WebFetchTask> due = new List<WebFetchTask>();
            lock (_lock)
            {
                bool changed = false;
                for (int i = 0; i < _tasks.Count; i = i + 1)
                {
                    WebFetchTask row = _tasks[i];
                    if (IsTerminal(row.State) && !row.Notified && row.FinishedAt > 0)
                    {
                        row.Notified = true;
                        if (string.Equals(row.State, "Canceled", StringComparison.Ordinal))
                        {
                            row.Delivered = true;
                        }
                        changed = true;
                    }
                    if (row.Notified && !row.Delivered)
                    {
                        due.Add(row);
                    }
                }
                if (changed)
                {
                    SaveLocked();
                }
            }
            if (due.Count == 0)
            {
                return;
            }
            Func<string, string, bool> inject = _inject;
            if (inject == null)
            {
                return;
            }
            for (int i = 0; i < due.Count; i = i + 1)
            {
                WebFetchTask row = due[i];
                bool accepted = false;
                try
                {
                    accepted = inject(row.CatKey, BuildNotifyText(row));
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 3, "下载通知投递异常: id=#" + row.Id.ToString() + " | " + ex.Message, "WEBFETCH");
                }
                if (accepted)
                {
                    lock (_lock)
                    {
                        row.Delivered = true;
                        SaveLocked();
                    }
                }
            }
        }

        /// <summary>
        /// 取消任务——在途置 Canceled 并触发令牌；终态任务不可取消。
        /// </summary>
        /// <param name="taskId">任务号</param>
        /// <returns>结果文本（失败 ERR| 前缀）</returns>
        private string CancelTask(long taskId)
        {
            CancellationTokenSource cts = null;
            string fileName = "";
            lock (_lock)
            {
                WebFetchTask row = FindLocked(taskId);
                if (row == null)
                {
                    return "ERR|DOWNLOAD_NO_TASK|任务不存在: #" + taskId.ToString();
                }
                if (IsTerminal(row.State))
                {
                    return "ERR|DOWNLOAD_NO_TASK|任务已终结，不可取消: #" + taskId.ToString() + "（" + row.State + "）";
                }
                row.State = "Canceled";
                row.FinishedAt = NowMs();
                row.Notified = true;
                row.Delivered = true;
                fileName = row.FileName;
                SaveLocked();
                if (_tokens.TryGetValue(taskId, out cts))
                {
                    _tokens.Remove(taskId);
                }
            }
            if (cts != null)
            {
                try
                {
                    cts.Cancel();
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 2, "下载取消令牌异常: id=#" + taskId.ToString() + " | " + ex.Message, "WEBFETCH");
                }
            }
            LogStore.Add("CatHome4", 1, "下载取消: id=#" + taskId.ToString() + " | " + fileName, "WEBFETCH");
            return "已取消 #" + taskId.ToString() + " | " + fileName;
        }

        /// <summary>
        /// 载入任务表——文件缺失 / 解析失败按空表继续（出声，不阻断）。
        /// </summary>
        private void Load()
        {
            lock (_lock)
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
                        if (root.TryGetProperty("tasks", out arr) && arr.ValueKind == JsonValueKind.Array)
                        {
                            for (int i = 0; i < arr.GetArrayLength(); i = i + 1)
                            {
                                JsonElement item = arr[i];
                                if (item.ValueKind != JsonValueKind.Object)
                                {
                                    continue;
                                }
                                WebFetchTask row = ParseTask(item);
                                if (row != null)
                                {
                                    _tasks.Add(row);
                                    if (row.Id >= _nextId)
                                    {
                                        _nextId = row.Id + 1;
                                    }
                                }
                            }
                        }
                    }
                    LogStore.Add("CatHome4", 1, "下载任务表已加载：" + _tasks.Count.ToString() + " 条（" + _storePath + "）", "WEBFETCH");
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 3, "下载任务表加载失败（按空表继续）: " + ex.Message, "WEBFETCH");
                }
            }
        }

        /// <summary>
        /// 落盘——锁内调用（原子写：临时文件 + 替换）。
        /// </summary>
        private void SaveLocked()
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
                List<string> items = new List<string>();
                for (int i = 0; i < _tasks.Count; i = i + 1)
                {
                    items.Add(JsonUtil.Object(
                        ("id", _tasks[i].Id),
                        ("url", _tasks[i].Url),
                        ("fileName", _tasks[i].FileName),
                        ("targetPath", _tasks[i].TargetPath),
                        ("catKey", _tasks[i].CatKey),
                        ("state", _tasks[i].State),
                        ("totalBytes", _tasks[i].TotalBytes),
                        ("receivedBytes", _tasks[i].ReceivedBytes),
                        ("httpStatus", _tasks[i].HttpStatus),
                        ("error", _tasks[i].Error),
                        ("createdAt", _tasks[i].CreatedAt),
                        ("finishedAt", _tasks[i].FinishedAt),
                        ("notified", _tasks[i].Notified),
                        ("delivered", _tasks[i].Delivered)));
                }
                string payload = JsonUtil.Object(("version", 1), ("nextId", _nextId), ("tasks", JsonUtil.RawArray(items.ToArray())));
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
                LogStore.Add("CatHome4", 3, "下载任务表落盘失败: " + ex.Message, "WEBFETCH");
            }
        }

        /// <summary>
        /// 任务行解析——id 缺失视为无效行（跳过）；其余字段缺省按初值。
        /// </summary>
        /// <param name="item">JSON 对象</param>
        /// <returns>任务实体（无效行 null）</returns>
        private static WebFetchTask ParseTask(JsonElement item)
        {
            WebFetchTask row = new WebFetchTask();
            JsonElement el;
            if (!item.TryGetProperty("id", out el) || el.ValueKind != JsonValueKind.Number)
            {
                return null;
            }
            row.Id = el.GetInt64();
            row.Url = StrOf(item, "url");
            row.FileName = StrOf(item, "fileName");
            row.TargetPath = StrOf(item, "targetPath");
            row.CatKey = StrOf(item, "catKey");
            row.State = StrOf(item, "state");
            if (row.State.Length == 0)
            {
                row.State = "Pending";
            }
            row.TotalBytes = LongOf(item, "totalBytes", -1);
            row.ReceivedBytes = LongOf(item, "receivedBytes", 0);
            row.HttpStatus = (int)LongOf(item, "httpStatus", 0);
            row.Error = StrOf(item, "error");
            row.CreatedAt = LongOf(item, "createdAt", 0);
            row.FinishedAt = LongOf(item, "finishedAt", 0);
            row.Notified = BoolOf(item, "notified", false);
            row.Delivered = BoolOf(item, "delivered", false);
            return row;
        }

        /// <summary>
        /// 通知文案——完成 / 失败两形态（任务号 / 文件名 / 规模 / 路径 / 原因）。
        /// </summary>
        /// <param name="task">任务实体</param>
        /// <returns>注入内容</returns>
        private static string BuildNotifyText(WebFetchTask task)
        {
            StringBuilder sb = new StringBuilder();
            if (string.Equals(task.State, "Completed", StringComparison.Ordinal))
            {
                sb.Append("下载完成：#").Append(task.Id.ToString());
                sb.Append(" | ").Append(task.FileName);
                sb.Append(" | ").Append(FormatBytes(task.ReceivedBytes));
                sb.Append(" | 用时 ").Append(FormatDuration(task.FinishedAt - task.CreatedAt));
                sb.Append(" → ").Append(task.TargetPath);
            }
            else
            {
                sb.Append("下载失败：#").Append(task.Id.ToString());
                sb.Append(" | ").Append(task.FileName);
                sb.Append(" | ").Append(task.Error);
                sb.Append(" | 源 ").Append(task.Url);
            }
            return sb.ToString();
        }

        /// <summary>
        /// 按任务号查表——锁内调用。
        /// </summary>
        /// <param name="taskId">任务号</param>
        /// <returns>任务实体（未命中 null）</returns>
        private WebFetchTask FindLocked(long taskId)
        {
            for (int i = 0; i < _tasks.Count; i = i + 1)
            {
                if (_tasks[i].Id == taskId)
                {
                    return _tasks[i];
                }
            }
            return null;
        }

        /// <summary>
        /// 终态判定——Completed / Failed / Canceled。
        /// </summary>
        /// <param name="state">状态串</param>
        /// <returns>true=终态</returns>
        private static bool IsTerminal(string state)
        {
            return string.Equals(state, "Completed", StringComparison.Ordinal) ||
                string.Equals(state, "Failed", StringComparison.Ordinal) ||
                string.Equals(state, "Canceled", StringComparison.Ordinal);
        }

        /// <summary>
        /// 文件名派生——取 URL 路径尾段并解码；空 / 非法字符清理后为空则回落 download。
        /// </summary>
        /// <param name="url">目标地址</param>
        /// <returns>文件名</returns>
        private static string DeriveFileName(string url)
        {
            string name = url;
            try
            {
                Uri parsed = new Uri(url, UriKind.Absolute);
                name = parsed.AbsolutePath;
            }
            catch (Exception)
            {
                name = url;
            }
            int cut = name.LastIndexOf('/');
            if (cut >= 0)
            {
                name = name.Substring(cut + 1);
            }
            name = Uri.UnescapeDataString(name);
            char[] invalid = Path.GetInvalidFileNameChars();
            StringBuilder sb = new StringBuilder();
            for (int i = 0; i < name.Length; i = i + 1)
            {
                char c = name[i];
                bool bad = false;
                for (int j = 0; j < invalid.Length; j = j + 1)
                {
                    if (invalid[j] == c)
                    {
                        bad = true;
                        break;
                    }
                }
                if (!bad)
                {
                    sb.Append(c);
                }
            }
            string clean = sb.ToString();
            if (clean.Length == 0)
            {
                clean = "download";
            }
            return clean;
        }

        /// <summary>
        /// 清单行——任务号 / 状态 / 文件名 / 进度 / 来源猫。
        /// </summary>
        /// <param name="row">任务实体</param>
        /// <returns>单行文本</returns>
        private static string ListLine(WebFetchTask row)
        {
            return "#" + row.Id.ToString() + " [" + row.State + "] " + row.FileName +
                " | " + FormatProgress(row) + " | cat=" + row.CatKey;
        }

        /// <summary>
        /// 详情文本——单任务全字段（源 / 产物 / 进度 / HTTP / 时刻 / 失败原因）。
        /// </summary>
        /// <param name="row">任务实体</param>
        /// <returns>多行文本</returns>
        private static string DetailText(WebFetchTask row)
        {
            StringBuilder sb = new StringBuilder();
            sb.Append("#").Append(row.Id.ToString()).Append(" [").Append(row.State).Append("]\n");
            sb.Append("源: ").Append(row.Url).Append("\n");
            sb.Append("产物: ").Append(row.TargetPath).Append("\n");
            sb.Append("进度: ").Append(FormatProgress(row)).Append("\n");
            sb.Append("HTTP: ").Append(row.HttpStatus.ToString()).Append("\n");
            sb.Append("来源猫: ").Append(row.CatKey).Append("\n");
            sb.Append("登记: ").Append(FormatTime(row.CreatedAt));
            if (row.FinishedAt > 0)
            {
                sb.Append(" | 终结: ").Append(FormatTime(row.FinishedAt));
                sb.Append(" | 用时 ").Append(FormatDuration(row.FinishedAt - row.CreatedAt));
            }
            if (row.Error.Length > 0)
            {
                sb.Append("\n失败: ").Append(row.Error);
            }
            return sb.ToString();
        }

        /// <summary>
        /// 进度文本——总长未知时只报已收；已知则给「已收 / 总长（百分比）」。
        /// </summary>
        /// <param name="row">任务实体</param>
        /// <returns>进度文本</returns>
        private static string FormatProgress(WebFetchTask row)
        {
            if (row.TotalBytes < 0)
            {
                return "大小未知（已收 " + FormatBytes(row.ReceivedBytes) + "）";
            }
            long percent = 0;
            if (row.TotalBytes > 0)
            {
                percent = row.ReceivedBytes * 100 / row.TotalBytes;
                if (percent > 100)
                {
                    percent = 100;
                }
            }
            return FormatBytes(row.ReceivedBytes) + " / " + FormatBytes(row.TotalBytes) + "（" + percent.ToString() + "%）";
        }

        /// <summary>
        /// 字节数格式化——B / KB / MB / GB。
        /// </summary>
        /// <param name="bytes">字节数</param>
        /// <returns>可读文本</returns>
        private static string FormatBytes(long bytes)
        {
            if (bytes < 1024)
            {
                return bytes.ToString() + " B";
            }
            double kb = bytes / 1024.0;
            if (kb < 1024)
            {
                return kb.ToString("0.0") + " KB";
            }
            double mb = kb / 1024.0;
            if (mb < 1024)
            {
                return mb.ToString("0.0") + " MB";
            }
            double gb = mb / 1024.0;
            return gb.ToString("0.00") + " GB";
        }

        /// <summary>
        /// 时长格式化——ms / s / min。
        /// </summary>
        /// <param name="ms">毫秒</param>
        /// <returns>可读文本</returns>
        private static string FormatDuration(long ms)
        {
            if (ms < 1000)
            {
                return ms.ToString() + "ms";
            }
            double sec = ms / 1000.0;
            if (sec < 60)
            {
                return sec.ToString("0.0") + "s";
            }
            double min = sec / 60.0;
            return min.ToString("0.0") + "min";
        }

        /// <summary>
        /// 时刻格式化——Unix 毫秒 → 本机时区可读文本。
        /// </summary>
        /// <param name="unixMs">Unix 毫秒</param>
        /// <returns>yyyy-MM-dd HH:mm:ss</returns>
        private static string FormatTime(long unixMs)
        {
            return DateTimeOffset.FromUnixTimeMilliseconds(unixMs).ToLocalTime().ToString("yyyy-MM-dd HH:mm:ss");
        }

        /// <summary>
        /// 当前时刻——Unix 毫秒（UTC）。
        /// </summary>
        /// <returns>Unix 毫秒</returns>
        private static long NowMs()
        {
            return DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
        }

        /// <summary>
        /// HTTP 客户端构造——不设超时（下载无上限，取消走令牌）+ 常规 UA。
        /// </summary>
        /// <returns>客户端实例</returns>
        private static HttpClient CreateClient()
        {
            HttpClient client = new HttpClient();
            client.Timeout = Timeout.InfiniteTimeSpan;
            client.DefaultRequestHeaders.UserAgent.ParseAdd("Mozilla/5.0 (Windows NT 10.0; Win64; x64)");
            return client;
        }

        /// <summary>
        /// 取字符串字段——非字符串 / 缺失返回空串。
        /// </summary>
        /// <param name="item">JSON 对象</param>
        /// <param name="key">键名</param>
        /// <returns>字符串值</returns>
        private static string StrOf(JsonElement item, string key)
        {
            JsonElement el;
            if (item.TryGetProperty(key, out el) && el.ValueKind == JsonValueKind.String)
            {
                return el.GetString() ?? "";
            }
            return "";
        }

        /// <summary>
        /// 取长整字段——非数字 / 缺失返回兜底值。
        /// </summary>
        /// <param name="item">JSON 对象</param>
        /// <param name="key">键名</param>
        /// <param name="fallback">兜底值</param>
        /// <returns>数值</returns>
        private static long LongOf(JsonElement item, string key, long fallback)
        {
            JsonElement el;
            if (item.TryGetProperty(key, out el) && el.ValueKind == JsonValueKind.Number)
            {
                return el.GetInt64();
            }
            return fallback;
        }

        /// <summary>
        /// 取布尔字段——非布尔 / 缺失返回兜底值。
        /// </summary>
        /// <param name="item">JSON 对象</param>
        /// <param name="key">键名</param>
        /// <param name="fallback">兜底值</param>
        /// <returns>布尔值</returns>
        private static bool BoolOf(JsonElement item, string key, bool fallback)
        {
            JsonElement el;
            if (item.TryGetProperty(key, out el))
            {
                if (el.ValueKind == JsonValueKind.True)
                {
                    return true;
                }
                if (el.ValueKind == JsonValueKind.False)
                {
                    return false;
                }
            }
            return fallback;
        }
    }
}
