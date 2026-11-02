using System;
using System.Collections.Generic;
using System.Text.Json;
using System.Threading;
using System.Threading.Channels;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.Logging;
using Mau.Runtime;

namespace CatHome4.Http
{
    /// <summary>
    /// HttpHost 推送面分部——后台快照循环/主线程泵/Log 增量/SSE 事件广播。
    /// P7b partial 拆分——自 HttpHost.cs 原样搬移，逻辑零改动。
    /// </summary>
    public sealed partial class HttpHost
    {
        /// <summary>
        /// 后台推送循环——每 250ms 推全量快照 + LogStore 增量（协议 §4.2）。
        /// </summary>
        /// <param name="token">取消令牌</param>
        private void PumpLoop(CancellationToken token)
        {
            while (!token.IsCancellationRequested)
            {
                try
                {
                    Thread.Sleep(SnapshotIntervalMs);
                    // 快照构建归主线程（ThreadGuard——OA.GetSnapshot 仅宿主主线程）——后台只置标志，主线程 PumpMainThread 消费
                    _snapshotPending = true;
                    PushLogIncrements();
                }
                catch (Exception ex)
                {
                    // 推送异常不炸宿主——静默跳过下一轮
                    LogStore.Add("HttpHost", 2, "日志增量推送异常，跳过本轮: " + ex.Message, "SYS");
                }
            }
        }

        /// <summary>
        /// 快照注入页面标题——根对象加 title 字段（不可变快照复制 + 字段前置；解析失败原样返回）。
        /// </summary>
        /// <param name="json">原快照 JSON</param>
        /// <param name="title">标题文本（displayName · Chat）</param>
        /// <returns>注入后的快照 JSON</returns>
        private static string InjectSnapshotTitle(string json, string title)
        {
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(json))
                {
                    JsonElement root = doc.RootElement;
                    using (System.IO.MemoryStream ms = new System.IO.MemoryStream())
                    {
                        using (Utf8JsonWriter writer = new Utf8JsonWriter(ms))
                        {
                            writer.WriteStartObject();
                            writer.WriteString("title", title);
                            foreach (JsonProperty prop in root.EnumerateObject())
                            {
                                writer.WritePropertyName(prop.Name);
                                prop.Value.WriteTo(writer);
                            }
                            writer.WriteEndObject();
                        }
                        return System.Text.Encoding.UTF8.GetString(ms.ToArray());
                    }
                }
            }
            catch (Exception)
            {
                // 快照畸形——原样返回（观测面不受单帧异常影响）
                return json;
            }
        }

        /// <summary>
        /// 主线程泵——宿主帧循环调用：快照待构建标志置位时在主线程构建 + 缓存 + 推送（ThreadGuard 契约）。
        /// </summary>
        public void PumpMainThread()
        {
            if (!_snapshotPending)
            {
                return;
            }
            _snapshotPending = false;
            string json = "";
            if (_snapshotBuilder != null)
            {
                json = _snapshotBuilder(false);
            }
            // Q6 页面标题——每猫快照注入 title 字段（displayName · Chat；前端初始化 fetch 快照直接用——前端零业务逻辑）
            // A301——无快照构建器（群聊轻宿主）时不注入、不推送
            if (_pageTitle != null && _pageTitle.Length > 0 && json.Length > 0)
            {
                json = InjectSnapshotTitle(json, _pageTitle);
            }
            _snapshotCache = json;
            // A225c——猫卡 state 推送：多猫列表变化即推（管理页猫卡实时跟随；无变化零字节）
            if (_catsBuilder != null)
            {
                string catsJson = _catsBuilder();
                if (catsJson != null && catsJson.Length > 0 && !string.Equals(catsJson, _catsStateCache, StringComparison.Ordinal))
                {
                    _catsStateCache = catsJson;
                    PushEvent("state", catsJson);
                }
            }
            // v2 契约——视图增量帧（状态段 + 临时区）：有变化推一帧、无变化零字节
            if (_viewFrameBuilder != null)
            {
                string frame = _viewFrameBuilder();
                if (frame != null && frame.Length > 0)
                {
                    PushEvent("view", frame);
                }
            }
            // 主端口——增量 patch / 全量快照回落（chat 面内容与状态由帧承载，不再单推运行态）
            else if (_patchBuilder != null)
            {
                string patch = _patchBuilder();
                if (patch != null && patch.Length > 0)
                {
                    PushEvent("patch", patch);
                }
            }
            else if (json.Length > 0)
            {
                PushEvent("snapshot", json);
            }
            // O3 帧流——每帧紧凑 JSON 落盘（回放/复盘；FrameStore 未配置时静默 no-op）
            if (_frameBuilder != null)
            {
                try
                {
                    string frameJson = _frameBuilder();
                    if (frameJson != null && frameJson.Length > 0)
                    {
                        FrameStore.Append(frameJson);
                    }
                }
                catch (Exception ex)
                {
                    // 帧流异常不阻塞主线程泵
                    LogStore.Add("HttpHost", 2, "帧流构建异常，不阻塞主线程泵: " + ex.Message, "SYS");
                }
            }
        }
        /// <summary>
        /// 退出前视图收尾（A206）——强制取一帧推送 + 等各连接队列排空（有限等待）。
        /// 动机：视图帧「入队 ≠ 写出」——Environment.Exit 直接终止进程会丢弃在途帧，
        /// 前端停在半句（判例 2026-10-06：full 重启时最后一帧未送达）。
        /// 顺序：先推最后一帧（把待推内容送出）→ 再等各连接队列排空 → 超时即走（重启不因慢客户端拖延）。
        /// </summary>
        /// <param name="timeoutMs">排空等待上限毫秒</param>
        public void FlushViewBeforeExit(int timeoutMs)
        {
            if (_stopped)
            {
                return;
            }
            if (_viewFrameBuilder != null)
            {
                string frame = _viewFrameBuilder();
                if (frame != null && frame.Length > 0)
                {
                    PushEvent("view", frame);
                }
            }
            long start = Environment.TickCount64;
            while (true)
            {
                int pending = 0;
                lock (_clientLock)
                {
                    for (int i = 0; i < _clients.Count; i = i + 1)
                    {
                        SseClient client = _clients[i];
                        if (client.Queue.Reader.CanCount)
                        {
                            pending = pending + client.Queue.Reader.Count;
                        }
                    }
                }
                if (pending == 0)
                {
                    // 队列已空——消费端出队即已 WriteAsync + FlushAsync，留一小段让最后一条帧落到 socket
                    Thread.Sleep(60);
                    return;
                }
                if (Environment.TickCount64 - start >= timeoutMs)
                {
                    LogStore.Add("HttpHost", 2, "退出前 SSE 排空超时——仍有 " + pending.ToString() + " 帧未写出（上限 " + timeoutMs.ToString() + " ms）", "SYS");
                    return;
                }
                Thread.Sleep(15);
            }
        }
        /// <summary>LogStore 增量推送——锁内只取区间快照 + 推进游标，序列化与广播在锁外；一轮泵的多条日志批量合帧为单个 log 事件（载荷数组，A141）；trace 类审计不进前端（IsTraceAudit，2026-09-17）。</summary>
        private void PushLogIncrements()
        {
            List<LogStore.LogEntry> pending = new List<LogStore.LogEntry>();
            // A141/A252 锁内只做「取增量快照 + 推进游标」——序列化与广播移到锁外（宿主主循环写日志不再与推送面竞争同一把锁）
            int endIndex;
            List<LogStore.LogEntry> logs = LogStore.SnapshotFrom(_logCursor, out endIndex);
            _logCursor = endIndex;
            for (int i = 0; i < logs.Count; i = i + 1)
            {
                LogStore.LogEntry entry = logs[i];
                // trace 类审计不进前端（导线级轨迹——2026-09-17）
                if (IsTraceAudit(entry))
                {
                    continue;
                }
                pending.Add(entry);
            }
            if (pending.Count == 0)
            {
                return;
            }
            // A141 批量合帧——一轮泵的多条日志合成一个 log 帧（载荷为数组，前端按数组逐条消费；
            // 日志属"可弃"级：整批丢弃代价远低于每帧一槽的队列占用）
            List<object> list = new List<object>();
            for (int i = 0; i < pending.Count; i++)
            {
                LogStore.LogEntry entry = pending[i];
                list.Add(new
                {
                    time = entry.Time,
                    frame = entry.Frame,
                    level = LogStore.LevelText(entry.Level),
                    category = entry.Category,
                    module = entry.Module,
                    message = entry.Message
                });
            }
            PushEvent("log", JsonUtil.Serialize(list));
        }

        /// <summary>广播 SSE 事件帧——id 行（每连接帧序号，入队即分配）+ event 行 + data 行（协议 §4.1 标准 text/event-stream）。队列满时丢弃最旧帧不再静默：溢出计数 + 首次出声；丢弃条数体现为客户端所见 id 落差（design-ch4-push-perf §3.2）。</summary>
        /// <param name="eventName">事件名（snapshot/llm/log/cmd）</param>
        /// <param name="data">JSON 载荷</param>
        private void PushEvent(string eventName, string data)
        {
            if (_stopped)
            {
                return;
            }
            bool overflow = false;
            int overflowSeq = 0;
            lock (_clientLock)
            {
                for (int i = 0; i < _clients.Count; i++)
                {
                    SseClient client = _clients[i];
                    // A141 按订阅推送——不订阅本事件名的连接根本不入队（也不消耗其帧序号）
                    if (!client.Wants(eventName))
                    {
                        continue;
                    }
                    // 队列已满——本次 TryWrite 将丢弃最旧帧（DropOldest 内部完成、无回调）→ 显式计数
                    if (client.Queue.Reader.CanCount && client.Queue.Reader.Count >= SseClient.Capacity)
                    {
                        client.OverflowCount = client.OverflowCount + 1;
                        if (!client.OverflowReported)
                        {
                            client.OverflowReported = true;
                            overflow = true;
                            overflowSeq = client.SentSeq;
                        }
                    }
                    string frame = client.NextFrame(eventName, data);
                    if (!client.Queue.Writer.TryWrite(frame))
                    {
                        // 队列写失败（客户端断开）——完成该队列（消费端退出清理）
                        client.Queue.Writer.TryComplete();
                    }
                }
            }
            if (overflow)
            {
                // 锁外出声——失败必须可见（丢弃条数体现为客户端所见 id 落差）
                LogStore.Add("HttpHost", 2, "SSE 队列溢出——丢弃最旧帧（客户端已发 seq=" + overflowSeq + "；丢弃条数以 lastEventId 落差体现）", "SYS");
            }
        }
        /// <summary>
        /// 视图事件推送——接口保底出口（A165 起块面不再走此通道：块经 chat 快照帧下发；本方法保留以满足 IHostPush 契约）。
        /// </summary>
        /// <param name="op">出口事件类型（历史 op 面：persist.append / live.* / control——现行均不再发出）</param>
        /// <param name="payload">载荷 JSON 字符串（内嵌对象）</param>
        /// <param name="meta">块元数据 JSON（空串 = 不带）</param>
        public void PushView(string op, string payload, string meta)
        {
            object payloadObj = "";
            if (payload != null && payload.Length > 0)
            {
                try
                {
                    payloadObj = JsonSerializer.Deserialize<object>(payload);
                    if (payloadObj == null)
                    {
                        payloadObj = "";
                    }
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 2, "视图载荷解析失败（按原文）: " + ex.Message, "TOOL");
                    payloadObj = payload;
                }
            }
            Dictionary<string, object> ev = new Dictionary<string, object>();
            ev["op"] = op;
            ev["payload"] = payloadObj;
            MergeViewMeta(ev, meta);
            PushEvent("view", JsonUtil.Serialize(ev));
        }

        /// <summary>
        /// 块元数据并入视图事件（A157）——meta 是 JSON 对象串时逐键并入事件顶层（同名键以 meta 为准）。
        /// 解析失败或非对象：出声并忽略——元数据缺失可见，不阻断事件推送。
        /// </summary>
        /// <param name="ev">视图事件字典</param>
        /// <param name="meta">块元数据 JSON（空串 = 无）</param>
        private static void MergeViewMeta(Dictionary<string, object> ev, string meta)
        {
            if (meta == null || meta.Length == 0)
            {
                return;
            }
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(meta))
                {
                    JsonElement root = doc.RootElement;
                    if (root.ValueKind != JsonValueKind.Object)
                    {
                        LogStore.Add("CatHome4", 2, "视图块元数据非对象——已忽略: " + meta, "TOOL");
                        return;
                    }
                    foreach (JsonProperty prop in root.EnumerateObject())
                    {
                        // Clone——值节点独立于 JsonDocument：using 结束释放 doc 后仍可序列化。
                        // 未 Clone 的 JsonElement 逃出作用域 → 序列化时 ObjectDisposedException
                        // （2026-10-02 判例：视图事件推送即崩，exit 0xE0434352）
                        ev[prop.Name] = prop.Value.Clone();
                    }
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "视图块元数据解析失败（已忽略）: " + ex.Message, "TOOL");
            }
        }
    }
}