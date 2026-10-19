using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using System.Threading;
using Mau.Runtime;
using CH4;
using CatHome4.Contracts;

namespace CatHome4.Observe
{
    /// <summary>
    /// Program 快照面分部——协议 §三 cats/boxes/logs 组装（HttpHost 回调查询）。
    /// P7b partial 拆分——自 Program.cs 原样搬移，逻辑零改动。
    /// </summary>
    internal static partial class ObserveService
    {
        /// <summary>
        /// 上次帧体 JSON（除帧号外全部字段）——帧流脏检查比较面；主线程泵独占（ThreadGuard 契约）
        /// </summary>
        private static string _lastFrameBody = "";
        /// <summary>构建帧流落盘行——frame.txt 一行（帧/pid/chat_state/各 Cat 状态行/OA 计数；主线程泵调用）；脏检查：除帧号外与上次全等 → 只落仅含帧号的精简行（帧号仍连续，行体积大幅缩小）</summary>
        /// <returns>紧凑 JSON 文本</returns>
        internal static string BuildCompactFrameJson()
        {
            List<object> cats = new List<object>();
            AppendRegistryCatsCompact(cats);
            OAView oa = _oa.GetSnapshot();
            string chatState = "";
            string cs;
            if (DataBox.TryGet<string>("global", "chat_state", out cs) && cs != null)
            {
                chatState = cs;
            }
            // [段1] 帧体构造——除帧号外全部字段
            var body = new
            {
                pid = Environment.ProcessId,
                chat = chatState,
                oa = new
                {
                    o = oa.OpenCount,
                    w = oa.WorkCount,
                    c = oa.ClosedCount,
                    t = oa.TimeoutCount
                },
                cats = cats
            };
            string bodyJson = JsonUtil.Serialize(body);
            long frame = FlowRunner.GlobalFrame;
            // [段2] 脏检查——帧体全等只落帧号行
            if (string.Equals(bodyJson, _lastFrameBody, StringComparison.Ordinal))
            {
                return "{\"f\":" + frame.ToString() + "}";
            }
            _lastFrameBody = bodyJson;
            // [段3] 完整行——帧号前置 + 帧体拼接
            return "{\"f\":" + frame.ToString() + "," + bodyJson.Substring(1);
        }

        /// <summary>
        /// 单 Flow 紧凑状态——状态行拼接（帧流一行体积控制；名字来自注册表）
        /// </summary>
        /// <param name="cats">目标列表</param>
        /// <param name="entry">注册表条目</param>
        /// <param name="flow">Flow 实例（可 null 防御）</param>
        private static void AppendFlowCompact(List<object> cats, FlowEntry entry, IFlow flow)
        {
            if (flow == null)
            {
                cats.Add(new { n = entry.Name, s = "-" });
                return;
            }
            IObservableFlow obs = flow as IObservableFlow;
            if (obs == null)
            {
                cats.Add(new { n = entry.Name, s = "-" });
                return;
            }
            FlowStatusV3 status = obs.GetStatus();
            string states = "";
            for (int i = 0; i < status.StateLines.Length; i++)
            {
                if (i > 0)
                {
                    states = states + " ";
                }
                states = states + status.StateLines[i];
            }
            cats.Add(new { n = entry.Name, s = states });
        }

        /// <summary>
        /// 追加全部 Runtime 实体紧凑状态——从 FlowRegistry 拉取真实实例（替代硬编码）。按注册 ID 排序确定性输出。
        /// </summary>
        /// <param name="cats">目标列表</param>
        private static void AppendRegistryCatsCompact(List<object> cats)
        {
            FlowEntry[] entries = _runner.Registry.Entries;
            Array.Sort(entries, delegate (FlowEntry a, FlowEntry b)
            {
                return a.Id.CompareTo(b.Id);
            });
            for (int i = 0; i < entries.Length; i++)
            {
                IFlow flow = _runner.Registry.Get(entries[i].Id);
                AppendFlowCompact(cats, entries[i], flow);
            }
        }

        /// <summary>
        /// 单 Flow 快照 JSON 追加——协议 §3.2 cats[].status 四柱映射（FlowStatusV3 → 匿名对象）。
        /// 名字/id/type/kind 来自注册表条目——Runtime 真实实例，非业务硬编码。
        /// </summary>
        /// <param name="cats">目标列表</param>
        /// <param name="entry">注册表条目</param>
        /// <param name="flow">Flow 实例（可 null 防御）</param>
        private static void AppendFlowJson(List<object> cats, FlowEntry entry, IFlow flow)
        {
            if (flow == null)
            {
                cats.Add(new { name = entry.Name, id = entry.Id, type = entry.TypeName, kind = entry.Kind, faulted = true, faultReason = "注册表实例缺失", status = (object)null });
                return;
            }
            IObservableFlow obs = flow as IObservableFlow;
            if (obs == null)
            {
                cats.Add(new { name = entry.Name, id = entry.Id, type = entry.TypeName, kind = entry.Kind, faulted = false, faultReason = "无观测面（非 IObservableFlow）", status = (object)null });
                return;
            }

            FlowStatusV3 status = obs.GetStatus();
            List<object> sensors = new List<object>();
            for (int i = 0; i < status.SensorValues.Length; i++)
            {
                SignalValueV3 s = status.SensorValues[i];
                sensors.Add(new { name = s.Name, value = s.Value });
            }

            List<object> slots = new List<object>();
            for (int i = 0; i < status.SlotLevels.Length; i++)
            {
                SlotValueV3 s = status.SlotLevels[i];
                slots.Add(new { name = s.Name, available = s.Available, capacity = s.Capacity });
            }

            List<object> wires = new List<object>();
            for (int i = 0; i < status.WireStatuses.Length; i++)
            {
                WireStatusV3 w = status.WireStatuses[i];
                wires.Add(new { name = w.Name, busy = w.Busy, lastTriggerFrame = w.LastTriggerFrame, timedOut = w.TimedOut });
            }

            cats.Add(new { name = entry.Name, id = entry.Id, type = entry.TypeName, kind = entry.Kind, faulted = false, faultReason = "", status = new { stateLines = status.StateLines, sensors = sensors, slots = slots, wires = wires }, desc = obs.GetSelfDesc() });
        }

        /// <summary>
        /// 追加全部 Runtime 实体快照——从 FlowRegistry 拉取真实实例（替代硬编码 QuickCat/DevCat；R0.2 工具组 Flow 化后自动覆盖 TextCat/MauCat/CsCat/ConfigCat）。
        /// 任何 RegisterFlow 注册的实例自动出现——状态面板 = Runtime 真实截面。按注册 ID 排序确定性输出。
        /// </summary>
        /// <param name="cats">目标列表</param>
        private static void AppendRegistryCats(List<object> cats)
        {
            FlowEntry[] entries = _runner.Registry.Entries;
            Array.Sort(entries, delegate (FlowEntry a, FlowEntry b)
            {
                return a.Id.CompareTo(b.Id);
            });
            for (int i = 0; i < entries.Length; i++)
            {
                IFlow flow = _runner.Registry.Get(entries[i].Id);
                AppendFlowJson(cats, entries[i], flow);
            }
        }
        /// <summary>
        /// 构建全量快照 JSON——协议 design-ch4-protocol.md §三（version/pid/frame/cats/oa/logs；logs 按 includeLogs 裁剪）
        /// </summary>
        /// <param name="includeLogs">是否携带日志（GET 轮询 true / SSE 事件 false——协议 §4.2 snapshot 事件裁剪）</param>
        /// <returns>快照 JSON 文本</returns>
        internal static string BuildSnapshotJson(bool includeLogs)
        {
            // [段0] 帧级缓存——同帧同参数直接复用（主端口 + majordomo + 各猫 host 各调一次 PumpMainThread，
            // 数据源是同一份全局快照 → 不缓存即 N 倍冗余构建；2026-10-16 A254 主循环耗时定位）
            long cacheFrameNow = FlowRunner.GlobalFrame;
            lock (_snapshotCacheGate)
            {
                if (_snapshotCacheFrame == cacheFrameNow && _snapshotCacheLogs == includeLogs && _snapshotCacheJson.Length > 0)
                {
                    return _snapshotCacheJson;
                }
            }
            List<object> cats = new List<object>();
            AppendRegistryCats(cats);
            OAView oa = _oa.GetSnapshot();
            // [段1] boxes 字段——DataBox 全量截面（协议 v1.1：新增字段旧端忽略；复杂对象摘要化——内部实现盒子不刷爆快照）
            DataBoxSnapshot boxSnap = DataBox.Capture();
            List<object> boxes = new List<object>();
            for (int i = 0; i < boxSnap.Data.Length; i = i + 1)
            {
                DataBoxDataEntry d = boxSnap.Data[i];
                if (IsInternalBox(d.Scope, d.Key))
                {
                    continue;
                }
                boxes.Add(BuildBoxEntry(d));
            }
            // [段2] logs 段——includeLogs 裁剪（SSE 事件空数组；GET ?logs=N 由 HttpHost 动态合成替换）
            object logs;
            if (includeLogs)
            {
                List<object> logList = new List<object>();
                List<LogStore.LogEntry> all = LogStore.Snapshot(50);
                for (int i = 0; i < all.Count; i++)
                {
                    LogStore.LogEntry entry = all[i];
                    logList.Add(new { time = entry.Time, frame = entry.Frame, level = LogStore.LevelText(entry.Level), category = entry.Category, module = entry.Module, message = entry.Message });
                }
                logs = logList;
            }
            else
            {
                logs = new object[0];
            }
            // [段3] 快照组装——version/pid/frame/cats/sessions/oa/tools/boxes/logs（协议 v1.2：前端状态区会话卡 + 工具注册表）
            var snapshot = new
            {
                version = 3,
                pid = Environment.ProcessId,
                frame = FlowRunner.GlobalFrame,
                cats = cats,
                sessions = BuildSessionsJson(),
                oa = new
                {
                    open = oa.OpenCount,
                    work = oa.WorkCount,
                    closed = oa.ClosedCount,
                    timeout = oa.TimeoutCount
                },
                tools = BuildToolsJson(),
                boot = BuildBootSegment(),
                boxes = boxes,
                deps = BuildDepsJson(out _),
                res = ResProbe.Capture(),
                logs = logs
            };
            string snapshotJson = JsonUtil.Serialize(snapshot);
            // 写回帧级缓存（构建过程不持锁——并发最坏重复构建一次，无害）
            lock (_snapshotCacheGate)
            {
                _snapshotCacheFrame = cacheFrameNow;
                _snapshotCacheLogs = includeLogs;
                _snapshotCacheJson = snapshotJson;
            }
            return snapshotJson;
        }

        /// <summary>
        /// 启动健康段（A260）——降级清单 + 早期 sink 落点 + 报告路径（与日志 / 弹窗同源）。
        /// </summary>
        /// <returns>匿名对象（System.Text.Json 正常序列化）</returns>
        private static object BuildBootSegment()
        {
            StartupDegradeEntry[] entries = StartupDegradation.Snapshot();
            List<object> items = new List<object>();
            for (int i = 0; i < entries.Length; i = i + 1)
            {
                StartupDegradeEntry entry = entries[i];
                items.Add(new
                {
                    code = entry.Code,
                    kind = entry.Kind,
                    ability = entry.Ability,
                    state = entry.State,
                    detail = entry.Detail,
                    fix = entry.Fix,
                    stage = entry.Stage,
                    at = entry.At,
                    count = entry.Count
                });
            }
            string sink = "";
            if (BootSinkProvider != null)
            {
                sink = BootSinkProvider();
            }
            string reportPath = "";
            if (BootReportPathProvider != null)
            {
                reportPath = BootReportPathProvider();
            }
            return new
            {
                ok = entries.Length == 0,
                count = entries.Length,
                summary = StartupDegradation.BuildSummaryLine(),
                sink = sink,
                reportPath = reportPath,
                degraded = items
            };
        }

        /// <summary>
        /// 早期诊断 sink 落点提供者——宿主注入（快照 boot 段显示实际落点）。
        /// </summary>
        public static Func<string> BootSinkProvider { get; set; }

        /// <summary>
        /// 启动报告路径提供者——宿主注入（报告落盘后取值）。
        /// </summary>
        public static Func<string> BootReportPathProvider { get; set; }

        /// <summary>
        /// deps 段——外部依赖探测结果（A253；DependencyProbe 后端统一快照 · D1）。
        /// 主线程调用：EnsureFresh（采集配置类 + 触发后台 IO）→ 取当前快照。
        /// stableJson = 「去时刻」稳定视图（patch 对比用——probedAt 每轮变化，混入 diff 面即每帧误推）。
        /// </summary>
        /// <param name="stableJson">稳定视图 JSON（不含 probedAt；对比面）</param>
        /// <returns>条目数组（含 probedAt——全量快照面）</returns>
        private static List<object> BuildDepsJson(out string stableJson)
        {
            DependencyProbe.EnsureFresh();
            DependencyProbe.ProbeResult[] results = DependencyProbe.Snapshot();
            List<object> deps = new List<object>();
            List<object> stable = new List<object>();
            for (int i = 0; i < results.Length; i++)
            {
                DependencyProbe.ProbeResult r = results[i];
                deps.Add(new { level = r.Level, name = r.Name, state = r.State, detail = r.Detail, probedAt = r.ProbedAtMs });
                stable.Add(new { level = r.Level, name = r.Name, state = r.State, detail = r.Detail });
            }
            stableJson = JsonUtil.Serialize(stable);
            return deps;
        }

        /// <summary>
        /// 会话状态段——会话注册表（Majordomo + 多猫）四相环预览（外观层状态区）。
        /// 数据源：Program._sessions（P9.1 会话对象化注册表）；ChatSession 公开观测属性。
        /// </summary>
        /// <returns>会话条目数组（id/name/phase/round/msgCount/pending/noteActive）</returns>
        private static List<object> BuildSessionsJson()
        {
            List<object> sessions = new List<object>();
            List<ChatSession> all = _chatBridge.Sessions;
            for (int i = 0; i < all.Count; i++)
            {
                ChatSession s = all[i];
                // A225 会话态统一投影——与猫列表 / cat.info 同源（ChatSession.BuildStateEntry）
                Dictionary<string, object> entry = s.BuildStateEntry();
                entry["id"] = s.Id;
                entry["name"] = s.DisplayName;
                entry["lastContextChangeAt"] = s.LastContextChangeAt;
                sessions.Add(entry);
            }
            return sessions;
        }

        /// <summary>
        /// 工具注册表段——按组分类摘要（外观层状态区；R0.2 ToolRegistry 单一真相源）。
        /// 条目：name + group（归属 Flow）/builtin（内置标记）。
        /// </summary>
        /// <returns>工具条目数组</returns>
        private static List<object> BuildToolsJson()
        {
            List<object> tools = new List<object>();
            ToolSpec[] specs = ToolRegistry.BuildSpecs();
            for (int i = 0; i < specs.Length; i++)
            {
                ToolRegistryEntry entry = ToolRegistry.Find(specs[i].Name);
                string group = "";
                bool builtin = false;
                if (entry != null)
                {
                    group = entry.OwnerFlow;
                    builtin = entry.IsBuiltin;
                }
                tools.Add(new { name = specs[i].Name, group = group, builtin = builtin });
            }
            return tools;
        }

        /// <summary>
        /// 构建单盒子条目对象——类型归一（bool/number/string/object 摘要）。
        /// </summary>
        /// <param name="d">DataBox 条目</param>
        /// <returns>条目匿名对象</returns>
        private static object BuildBoxEntry(DataBoxDataEntry d)
        {
            string t = "o";
            object val;
            if (d.Value is bool)
            {
                t = "b";
                val = d.Value;
            }
            else if (d.Value is long || d.Value is int || d.Value is double)
            {
                t = "n";
                val = d.Value;
            }
            else if (d.Value is string)
            {
                t = "s";
                val = d.Value;
            }
            else
            {
                t = "o";
                val = SummarizeBoxValue(d.Value);
            }
            return new { scope = d.Scope, key = d.Key, t = t, value = val };
        }

        // 增量 diff 缓存——主线程独占（PumpMainThread 调用；ThreadGuard 契约）
        /// <summary>上次 cats 段 JSON——变化检测</summary>
        private static string _lastCatsJson = "";

        /// <summary>上次 oa 段 JSON——变化检测</summary>
        private static string _lastOaJson = "";

        /// <summary>上次 sessions 段 JSON——变化检测（会话四相环预览）</summary>
        private static string _lastSessionsJson = "";

        /// <summary>上次 deps 段稳定视图 JSON——变化检测（依赖探测；probedAt 不入对比面）</summary>
        private static string _lastDepsJson = "";

        /// <summary>快照 JSON 帧级缓存锁——多 host 主线程泵 + HTTP 线程 GET 快照并发访问</summary>
        private static readonly object _snapshotCacheGate = new object();

        /// <summary>快照缓存——命中帧号（-1 = 无缓存）</summary>
        private static long _snapshotCacheFrame = -1;

        /// <summary>快照缓存——是否含日志维（缓存键第二维）</summary>
        private static bool _snapshotCacheLogs;

        /// <summary>快照缓存——JSON 文本</summary>
        private static string _snapshotCacheJson = "";

        /// <summary>res 段推送节拍毫秒——固定刷新参（A254 资源占用：每秒一带，不参与内容比对）</summary>
        private const int ResIntervalMs = 1000;

        /// <summary>上次 res 段推送时刻（Environment.TickCount64；0 = 未推——首帧即带）</summary>
        private static long _lastResPushMs;

        /// <summary>上次 boxes 字典——scope+key → 条目 JSON（变化检测）</summary>
        private static Dictionary<string, string> _lastBoxes = new Dictionary<string, string>();

        /// <summary>
        /// 构建增量 patch——与上次快照对比只含变化段（cats 整段/oa 计数/boxes 字典 diff）；无变化返回 null（零推送）。
        /// 主线程泵调用（PumpMainThread——ThreadGuard 契约）；缓存字段主线程独占。
        /// </summary>
        /// <returns>patch JSON 文本；无变化 null</returns>
        public static string BuildPatchJson()
        {
            // [段1] cats 段——整段序列化对比（状态转移才变，频率低）
            List<object> cats = new List<object>();
            AppendRegistryCats(cats);
            string catsJson = JsonUtil.Serialize(cats);
            bool catsChanged = !string.Equals(catsJson, _lastCatsJson, StringComparison.Ordinal);
            // [段1b] sessions 段——整段序列化对比（会话四相环/轮次/消息数变化推送）
            List<object> sessions = BuildSessionsJson();
            string sessionsJson = JsonUtil.Serialize(sessions);
            bool sessionsChanged = !string.Equals(sessionsJson, _lastSessionsJson, StringComparison.Ordinal);
            // [段1c] deps 段——稳定视图对比（A253 依赖探测；probedAt 不进对比面——观测元数据混入 diff 即每帧误推）
            string depsStableJson = "";
            List<object> deps = BuildDepsJson(out depsStableJson);
            bool depsChanged = !string.Equals(depsStableJson, _lastDepsJson, StringComparison.Ordinal);
            // [段1d] res 段——资源占用（A254）：固定刷新参（1 秒节拍强制带——内存数字每秒都在变，
            // 不参与内容比对；到点即带，从而打破 Idle 稳态的零推送静默）
            long resNow = Environment.TickCount64;
            bool resDue = resNow - _lastResPushMs >= ResIntervalMs;
            object resObj = null;
            if (resDue)
            {
                resObj = ResProbe.Capture();
            }
            // [段2] oa 段——四计数对比
            OAView oa = _oa.GetSnapshot();
            object oaObj = new
            {
                open = oa.OpenCount,
                work = oa.WorkCount,
                closed = oa.ClosedCount,
                timeout = oa.TimeoutCount
            };
            string oaJson = JsonUtil.Serialize(oaObj);
            bool oaChanged = !string.Equals(oaJson, _lastOaJson, StringComparison.Ordinal);
            // [段3] boxes 段——字典 diff（scope+key → 条目 JSON；新增/变化进 set，消失进 del）
            DataBoxSnapshot boxSnap = DataBox.Capture();
            Dictionary<string, string> boxesNow = new Dictionary<string, string>();
            for (int i = 0; i < boxSnap.Data.Length; i = i + 1)
            {
                DataBoxDataEntry d = boxSnap.Data[i];
                if (IsInternalBox(d.Scope, d.Key))
                {
                    continue;
                }
                string entryKey = d.Scope + "\u0001" + d.Key;
                boxesNow[entryKey] = JsonUtil.Serialize(BuildBoxEntry(d));
            }
            List<object> boxSet = new List<object>();
            List<object> boxDel = new List<object>();
            foreach (KeyValuePair<string, string> kv in boxesNow)
            {
                string old;
                if (!_lastBoxes.TryGetValue(kv.Key, out old) || !string.Equals(old, kv.Value, StringComparison.Ordinal))
                {
                    boxSet.Add(JsonSerializer.Deserialize<object>(kv.Value));
                }
            }
            foreach (KeyValuePair<string, string> kv in _lastBoxes)
            {
                if (!boxesNow.ContainsKey(kv.Key))
                {
                    int sep = kv.Key.IndexOf('\u0001');
                    boxDel.Add(new { scope = kv.Key.Substring(0, sep), key = kv.Key.Substring(sep + 1) });
                }
            }
            bool boxesChanged = boxSet.Count > 0 || boxDel.Count > 0;
            // [段4] 无变化零推送——Idle 稳态静默
            if (!catsChanged && !sessionsChanged && !oaChanged && !boxesChanged && !depsChanged && !resDue)
            {
                return null;
            }
            // [段5] patch 组装 + 缓存更新
            Dictionary<string, object> patch = new Dictionary<string, object>();
            patch["frame"] = FlowRunner.GlobalFrame;
            if (catsChanged)
            {
                patch["cats"] = cats;
                _lastCatsJson = catsJson;
            }
            if (sessionsChanged)
            {
                patch["sessions"] = sessions;
                _lastSessionsJson = sessionsJson;
            }
            if (depsChanged)
            {
                patch["deps"] = deps;
                _lastDepsJson = depsStableJson;
            }
            if (resDue)
            {
                patch["res"] = resObj;
                _lastResPushMs = resNow;
            }
            if (oaChanged)
            {
                patch["oa"] = oaObj;
                _lastOaJson = oaJson;
            }
            if (boxesChanged)
            {
                patch["boxes"] = new
                {
                    set = boxSet,
                    del = boxDel
                };
                _lastBoxes = boxesNow;
            }
            return JsonUtil.Serialize(patch);
        }
    }
}