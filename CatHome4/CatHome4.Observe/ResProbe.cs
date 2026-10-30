using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Text.Json;
using System.Threading.Tasks;
using Mau.Runtime;
using CatHome4.Contracts;

namespace CatHome4.Observe
{
    /// <summary>
    /// 资源占用采集——本体进程 / py 服务进程 / GPU 显存（design-ch4-pytools.md §七 · A254）。
    /// <para>
    /// OS 口径：进程工作集（<see cref="Process.WorkingSet64"/>）+ 托管堆（<see cref="GC"/> 内存信息）
    /// + GPU 全局占用（<c>nvidia-smi</c> 子进程）。
    /// </para>
    /// <para>
    /// 线程契约：主线程调用（DataBox / 进程计数器读——廉价）；GPU 子进程交后台单飞拉取 + TTL 缓存
    /// （不阻塞主循环、不每秒起子进程）。WDDM 下按进程显存不可得（实测 <c>used_memory</c> 恒 N/A）——
    /// 只报 GPU 全局占用。
    /// </para>
    /// </summary>
    internal static class ResProbe
    {
        /// <summary>GPU 采集 TTL 毫秒——一次子进程覆盖多个采样节拍</summary>
        private const int GpuTtlMs = 3000;

        /// <summary>GPU 命令超时毫秒</summary>
        private const int GpuTimeoutMs = 3000;

        /// <summary>py 服务枚举 TTL 毫秒——进程树遍历要扫全系统进程，不每帧做</summary>
        private const int PyTtlMs = 2000;

        /// <summary>上次 py 枚举时刻（Unix 毫秒；0 = 未枚举）</summary>
        private static long _pyAtMs;

        /// <summary>py 服务行缓存（TTL 内复用——构建新行集合时整体替换）</summary>
        private static readonly List<object> _pyCache = new List<object>();

        /// <summary>上次 node 枚举时刻（Unix 毫秒；0 = 未枚举）</summary>
        private static long _nodeAtMs;

        /// <summary>node 服务行缓存（TTL 内复用——构建新行集合时整体替换）</summary>
        private static readonly List<object> _nodeCache = new List<object>();

        // [段1] GPU 缓存（锁保护——后台写、主线程读）
        /// <summary>GPU 缓存锁</summary>
        private static readonly object _gpuGate = new object();

        /// <summary>上次 GPU 采集时刻（Unix 毫秒；0 = 未采集）</summary>
        private static long _gpuAtMs;

        /// <summary>GPU 采集在跑标记（单飞）</summary>
        private static bool _gpuRunning;

        /// <summary>显存已用 MiB（-1 = 不可用）</summary>
        private static int _gpuUsedMiB = -1;

        /// <summary>显存总量 MiB</summary>
        private static int _gpuTotalMiB;

        // [段2] 采集入口（主线程）
        /// <summary>
        /// 采集资源占用截面（主线程调用）。
        /// </summary>
        /// <returns>可序列化对象（t / proc / py / gpu）</returns>
        internal static object Capture()
        {
            long now = NowMs();
            StartGpuRefreshIfStale(now);

            // [段1] 本体进程——工作集 + 私有字节 + 托管堆分代
            GCMemoryInfo gcInfo = GC.GetGCMemoryInfo();
            Dictionary<string, object> proc = new Dictionary<string, object>();
            proc["ws"] = ReadCurrentProcessCounter(true);
            proc["private"] = ReadCurrentProcessCounter(false);
            proc["heap"] = GC.GetTotalMemory(false);
            proc["committed"] = gcInfo.TotalCommittedBytes;
            long gen0 = 0;
            long gen1 = 0;
            long gen2 = 0;
            long loh = 0;
            ReadOnlySpan<GCGenerationInfo> gens = gcInfo.GenerationInfo;
            if (gens.Length > 0)
            {
                gen0 = gens[0].SizeAfterBytes;
            }
            if (gens.Length > 1)
            {
                gen1 = gens[1].SizeAfterBytes;
            }
            if (gens.Length > 2)
            {
                gen2 = gens[2].SizeAfterBytes;
            }
            if (gens.Length > 3)
            {
                loh = gens[3].SizeAfterBytes;
            }
            proc["g0"] = gen0;
            proc["g1"] = gen1;
            proc["g2"] = gen2;
            proc["loh"] = loh;

            // [段2] py 服务——逐个服务进程工作集（IPyService 枚举面；TTL 缓存——进程树遍历要扫全系统进程，不每帧做）
            List<object> pyServices = new List<object>();
            if (now - _pyAtMs >= PyTtlMs)
            {
                List<object> fresh = new List<object>();
                IPyService py;
                DataBox.TryResolve<IPyService>(out py);
                if (py != null)
                {
                    ParseServices(py.Enumerate(), fresh, "py");
                }
                _pyAtMs = now;
                _pyCache.Clear();
                _pyCache.AddRange(fresh);
            }
            pyServices.AddRange(_pyCache);

            // [段2b] node 服务——独立键与独立缓存（与 py 互不覆盖；两侧任一侧未注入即空段）
            List<object> nodeServices = new List<object>();
            if (now - _nodeAtMs >= PyTtlMs)
            {
                List<object> freshNode = new List<object>();
                INodeService nodeSvc;
                DataBox.TryResolve<INodeService>(out nodeSvc);
                if (nodeSvc != null)
                {
                    ParseServices(nodeSvc.Enumerate(), freshNode, "node");
                }
                _nodeAtMs = now;
                _nodeCache.Clear();
                _nodeCache.AddRange(freshNode);
            }
            nodeServices.AddRange(_nodeCache);

            // [段3] GPU——只读缓存（后台刷新）
            Dictionary<string, object> gpu = new Dictionary<string, object>();
            lock (_gpuGate)
            {
                bool available = _gpuUsedMiB >= 0 && _gpuTotalMiB > 0;
                gpu["available"] = available;
                gpu["usedMib"] = available ? _gpuUsedMiB : 0;
                gpu["totalMib"] = available ? _gpuTotalMiB : 0;
                gpu["pct"] = available ? Math.Round(_gpuUsedMiB * 100.0 / _gpuTotalMiB, 1) : 0.0;
            }

            // [段4] 主循环——窗口平均 / 峰值帧耗时 + 逐段分解 + 超帧统计（A254；只反映主循环健康度，非进程总 CPU）
            Dictionary<string, object> loop = new Dictionary<string, object>();
            double loopAvgMs;
            double loopPeakMs;
            int loopSamples;
            List<LoopTiming.SegmentStat> loopSegments;
            LoopTiming.OverrunStat loopOverrun;
            LoopTiming.Read(out loopAvgMs, out loopPeakMs, out loopSamples, out loopSegments, out loopOverrun);
            loop["avgMs"] = Math.Round(loopAvgMs, 2);
            loop["peakMs"] = Math.Round(loopPeakMs, 2);
            loop["samples"] = loopSamples;
            // 超帧口径——帧预算 / 累计超帧 / 上一秒帧数与超帧数（「每秒超帧数」即瓶颈直读指标）
            double loopSecPct = 0.0;
            if (loopOverrun.SecFrames > 0)
            {
                loopSecPct = Math.Round(loopOverrun.SecOverruns * 100.0 / loopOverrun.SecFrames, 1);
            }
            loop["budgetMs"] = loopOverrun.BudgetMs;
            loop["overrunTotal"] = loopOverrun.TotalOverruns;
            loop["secFrames"] = loopOverrun.SecFrames;
            loop["secOverruns"] = loopOverrun.SecOverruns;
            loop["secOverrunPct"] = loopSecPct;
            List<object> loopRows = new List<object>();
            for (int i = 0; i < loopSegments.Count; i = i + 1)
            {
                LoopTiming.SegmentStat seg = loopSegments[i];
                loopRows.Add(new { name = seg.Name, avgMs = Math.Round(seg.AvgMs, 2), peakMs = Math.Round(seg.PeakMs, 2) });
            }
            loop["segments"] = loopRows;

            Dictionary<string, object> root = new Dictionary<string, object>();
            root["t"] = now;
            root["proc"] = proc;
            root["py"] = pyServices;
            root["node"] = nodeServices;
            root["gpu"] = gpu;
            root["loop"] = loop;
            return root;
        }

        /// <summary>
        /// 读本体进程计数器——工作集或私有字节（读失败返回 0）。
        /// </summary>
        /// <param name="workingSet">true=工作集；false=私有字节</param>
        /// <returns>字节数；不可读 0</returns>
        private static long ReadCurrentProcessCounter(bool workingSet)
        {
            try
            {
                using (Process proc = Process.GetCurrentProcess())
                {
                    if (workingSet)
                    {
                        return proc.WorkingSet64;
                    }
                    return proc.PrivateMemorySize64;
                }
            }
            catch (Exception)
            {
                return 0;
            }
        }

        /// <summary>
        /// 解析服务枚举回执——services 数组逐项转资源行（解析失败出声并跳过该段）。
        /// </summary>
        /// <param name="json">Enumerate 回执 JSON</param>
        /// <param name="sink">结果行输出</param>
        /// <param name="tag">服务面标签（py / node——日志归因用）</param>
        private static void ParseServices(string json, List<object> sink, string tag)
        {
            if (json == null || json.Length == 0)
            {
                return;
            }
            if (json.StartsWith("ERR|", StringComparison.Ordinal))
            {
                LogStore.Add("ResProbe", 2, tag + " 服务枚举失败（资源面缺该段）: " + json, "SYS");
                return;
            }
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(json))
                {
                    JsonElement root = doc.RootElement;
                    JsonElement services;
                    if (!root.TryGetProperty("services", out services) || services.ValueKind != JsonValueKind.Array)
                    {
                        return;
                    }
                    foreach (JsonElement item in services.EnumerateArray())
                    {
                        Dictionary<string, object> row = new Dictionary<string, object>();
                        row["name"] = ReadStr(item, "name");
                        row["state"] = ReadStr(item, "state");
                        row["alive"] = ReadBool(item, "alive");
                        row["pid"] = ReadLong(item, "pid");
                        row["ws"] = ReadLong(item, "ws");
                        sink.Add(row);
                    }
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("ResProbe", 2, tag + " 服务枚举回执解析失败（资源面缺该段）: " + ex.Message, "SYS");
            }
        }

        /// <summary>
        /// 读 JSON 对象字符串字段。
        /// </summary>
        /// <param name="item">目标对象</param>
        /// <param name="name">字段名</param>
        /// <returns>字符串值；缺失 / 类型不符空串</returns>
        private static string ReadStr(JsonElement item, string name)
        {
            JsonElement value;
            if (item.TryGetProperty(name, out value) && value.ValueKind == JsonValueKind.String)
            {
                return value.GetString() ?? "";
            }
            return "";
        }

        /// <summary>
        /// 读 JSON 对象布尔字段。
        /// </summary>
        /// <param name="item">目标对象</param>
        /// <param name="name">字段名</param>
        /// <returns>布尔值；缺失 / 类型不符 false</returns>
        private static bool ReadBool(JsonElement item, string name)
        {
            JsonElement value;
            if (item.TryGetProperty(name, out value) && (value.ValueKind == JsonValueKind.True || value.ValueKind == JsonValueKind.False))
            {
                return value.GetBoolean();
            }
            return false;
        }

        /// <summary>
        /// 读 JSON 对象数值字段。
        /// </summary>
        /// <param name="item">目标对象</param>
        /// <param name="name">字段名</param>
        /// <returns>数值；缺失 / 类型不符 0</returns>
        private static long ReadLong(JsonElement item, string name)
        {
            JsonElement value;
            if (item.TryGetProperty(name, out value) && value.ValueKind == JsonValueKind.Number)
            {
                return value.GetInt64();
            }
            return 0;
        }

        // [段3] GPU 采集（后台单飞 + TTL）
        /// <summary>
        /// GPU 缓存过期则启动后台刷新（单飞——同刻只跑一个子进程）。
        /// </summary>
        /// <param name="now">当前时刻（Unix 毫秒）</param>
        private static void StartGpuRefreshIfStale(long now)
        {
            bool start = false;
            lock (_gpuGate)
            {
                if (!_gpuRunning && now - _gpuAtMs > GpuTtlMs)
                {
                    _gpuRunning = true;
                    start = true;
                }
            }
            if (start)
            {
                Task.Run(delegate () { RefreshGpu(); });
            }
        }

        /// <summary>
        /// 后台刷新 GPU 占用——起 <c>nvidia-smi</c> 取显存已用 / 总量（失败置不可用）。
        /// </summary>
        private static void RefreshGpu()
        {
            int used = -1;
            int total = 0;
            try
            {
                ProcessStartInfo psi = new ProcessStartInfo();
                psi.FileName = "nvidia-smi";
                psi.Arguments = "--query-gpu=memory.used,memory.total --format=csv,noheader,nounits";
                psi.UseShellExecute = false;
                psi.RedirectStandardOutput = true;
                psi.RedirectStandardError = true;
                psi.CreateNoWindow = true;
                Process sub = Process.Start(psi);
                if (sub != null)
                {
                    Task<string> outTask = sub.StandardOutput.ReadToEndAsync();
                    Task<string> errTask = sub.StandardError.ReadToEndAsync();
                    if (!sub.WaitForExit(GpuTimeoutMs))
                    {
                        try
                        {
                            sub.Kill(true);
                        }
                        catch (Exception ex)
                        {
                            // 超时强杀失败——进程可能已自行退出，继续取已读到的输出
                            LogStore.Add("ResProbe", 2, "nvidia-smi 超时强杀失败: " + ex.Message, "SYS");
                        }
                    }
                    string text = "";
                    try
                    {
                        text = outTask.GetAwaiter().GetResult();
                    }
                    catch (Exception)
                    {
                        text = "";
                    }
                    try
                    {
                        errTask.GetAwaiter().GetResult();
                    }
                    catch (Exception ex)
                    {
                        // stderr 读取失败不影响 stdout 结果
                        LogStore.Add("ResProbe", 2, "nvidia-smi stderr 读取失败: " + ex.Message, "SYS");
                    }
                    sub.Dispose();
                    ParseGpu(text, out used, out total);
                }
            }
            catch (Exception)
            {
                used = -1;
                total = 0;
            }
            lock (_gpuGate)
            {
                _gpuUsedMiB = used;
                _gpuTotalMiB = total;
                _gpuAtMs = NowMs();
                _gpuRunning = false;
            }
        }

        /// <summary>
        /// 解析 nvidia-smi 输出——首行 <c>used, total</c>（MiB；格式不符置不可用）。
        /// </summary>
        /// <param name="text">子进程输出</param>
        /// <param name="used">显存已用 MiB（输出，-1 不可用）</param>
        /// <param name="total">显存总量 MiB（输出）</param>
        private static void ParseGpu(string text, out int used, out int total)
        {
            used = -1;
            total = 0;
            if (text == null || text.Trim().Length == 0)
            {
                return;
            }
            string line = text.Trim();
            int newline = line.IndexOf('\n');
            if (newline >= 0)
            {
                line = line.Substring(0, newline).Trim();
            }
            string[] parts = line.Split(',');
            if (parts.Length < 2)
            {
                return;
            }
            int parsedUsed;
            int parsedTotal;
            if (int.TryParse(parts[0].Trim(), out parsedUsed) && int.TryParse(parts[1].Trim(), out parsedTotal))
            {
                used = parsedUsed;
                total = parsedTotal;
            }
        }

        // [段4] 时刻
        /// <summary>
        /// 当前时刻（Unix 毫秒）。
        /// </summary>
        /// <returns>Unix 毫秒时间戳</returns>
        private static long NowMs()
        {
            return DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
        }
    }
}
