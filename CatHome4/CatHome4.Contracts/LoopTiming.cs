using System;
using System.Collections.Generic;

namespace CatHome4.Contracts
{
    /// <summary>
    /// 主循环耗时采样——记录最近窗口内「单帧工作实际耗时」（Tick + 各泵，不含帧尾 Sleep），
    /// 并给出逐段分解（design-ch4-pytools.md §七 · A254）。
    /// <para>
    /// 🔴 适用边界：只反映<b>主循环健康度</b>（帧耗时分布），<b>不等于进程总 CPU</b>——
    /// 负载可能在后台线程（LLM 流式 / Task）与独立进程（Kokoro / Chromium），那一面它看不见。
    /// </para>
    /// <para>
    /// 线程契约：写入在宿主帧循环（主线程），读取在快照构建（同为宿主主线程）——仍加锁，防消费面调整。
    /// </para>
    /// </summary>
    public static class LoopTiming
    {
        /// <summary>采样窗口帧数（120 帧 ≈ 12 秒 @ 100 ms 帧间隔）</summary>
        private const int WindowFrames = 120;

        /// <summary>分段上限（超出忽略——防段名无界增长）</summary>
        private const int MaxSegments = 24;

        /// <summary>整帧耗时环形缓冲（毫秒）</summary>
        private static readonly double[] _frame = new double[WindowFrames];

        /// <summary>本帧分段累计（段名 → 毫秒）</summary>
        private static readonly Dictionary<string, double> _current = new Dictionary<string, double>(StringComparer.Ordinal);

        /// <summary>段名顺序表（首次出现顺序——展示序）</summary>
        private static readonly List<string> _segNames = new List<string>();

        /// <summary>分段环形缓冲（段名 → 窗口样本）</summary>
        private static readonly Dictionary<string, double[]> _segSamples = new Dictionary<string, double[]>(StringComparer.Ordinal);

        /// <summary>写指针（环形）</summary>
        private static int _index;

        /// <summary>已填充帧数（≤ WindowFrames）</summary>
        private static int _count;

        /// <summary>最近一次帧预算（毫秒——EndFrame 传入）</summary>
        private static int _lastBudgetMs;

        /// <summary>累计帧数</summary>
        private static long _totalFrames;

        /// <summary>累计超帧数（工作耗时超过预算的帧）</summary>
        private static long _totalOverruns;

        /// <summary>当前秒窗口起点（Environment.TickCount64；0 = 未起）</summary>
        private static long _secStartTicks;

        /// <summary>当前秒窗口帧数</summary>
        private static int _secFrames;

        /// <summary>当前秒窗口超帧数</summary>
        private static int _secOverruns;

        /// <summary>上一完整秒帧数</summary>
        private static int _secLastFrames;

        /// <summary>上一完整秒超帧数</summary>
        private static int _secLastOverruns;

        /// <summary>采样锁——写帧循环、读快照面</summary>
        private static readonly object _gate = new object();

        /// <summary>
        /// 帧开始——清空本帧分段累计（帧循环首行调用）。
        /// </summary>
        public static void BeginFrame()
        {
            lock (_gate)
            {
                _current.Clear();
            }
        }

        /// <summary>
        /// 累加一段耗时（毫秒）——段名首次出现即登记（超出 MaxSegments 的段被忽略）。
        /// </summary>
        /// <param name="name">段名（如 tick / http / sessions）</param>
        /// <param name="ms">该段耗时（毫秒）</param>
        public static void Accumulate(string name, double ms)
        {
            if (name == null || name.Length == 0)
            {
                return;
            }
            lock (_gate)
            {
                if (!_current.ContainsKey(name) && !_segSamples.ContainsKey(name) && _segNames.Count >= MaxSegments)
                {
                    return;
                }
                double exist;
                if (_current.TryGetValue(name, out exist))
                {
                    _current[name] = exist + ms;
                }
                else
                {
                    _current[name] = ms;
                }
            }
        }

        /// <summary>
        /// 帧结束——写入整帧耗时与各段样本 + 超帧判定（帧循环在节拍睡眠之前调用）。
        /// <para>
        /// 超帧 = 本帧工作实际耗时**超过帧预算**（该帧掉出预算）；按墙钟每秒滚动统计——
        /// 「每秒超帧数」是性能瓶颈的直读指标（超帧率高的秒即主循环被拖慢的秒）。
        /// </para>
        /// </summary>
        /// <param name="totalMs">整帧工作实际耗时（毫秒）</param>
        /// <param name="budgetMs">帧预算（毫秒——调用方传宿主帧目标周期）</param>
        public static void EndFrame(double totalMs, int budgetMs)
        {
            lock (_gate)
            {
                _frame[_index] = totalMs;
                // [段0] 本帧未出现的已登记段补 0——否则环形缓冲残留旧值，Read 的均值被摊薄失真
                //（低频段如 cs.tool 只在批次收口帧出现，不补零会按帧数分母被稀释）
                for (int s = 0; s < _segNames.Count; s = s + 1)
                {
                    string segName = _segNames[s];
                    if (!_current.ContainsKey(segName))
                    {
                        _segSamples[segName][_index] = 0;
                    }
                }
                foreach (KeyValuePair<string, double> kv in _current)
                {
                    double[]? buffer;
                    if (!_segSamples.TryGetValue(kv.Key, out buffer) || buffer == null)
                    {
                        buffer = new double[WindowFrames];
                        _segSamples[kv.Key] = buffer;
                        _segNames.Add(kv.Key);
                    }
                    buffer[_index] = kv.Value;
                }
                _index = (_index + 1) % WindowFrames;
                if (_count < WindowFrames)
                {
                    _count = _count + 1;
                }
                // [段1] 超帧判定——严格口径：工作耗时 > 预算即计（不留容差）
                _lastBudgetMs = budgetMs;
                _totalFrames = _totalFrames + 1;
                bool overrun = budgetMs > 0 && totalMs > budgetMs;
                if (overrun)
                {
                    _totalOverruns = _totalOverruns + 1;
                }
                // [段2] 秒窗口滚动——满 1 秒先结算上一秒（帧数 / 超帧数），再起新窗口
                long nowTicks = Environment.TickCount64;
                if (_secStartTicks == 0)
                {
                    _secStartTicks = nowTicks;
                }
                if (nowTicks - _secStartTicks >= 1000)
                {
                    _secLastFrames = _secFrames;
                    _secLastOverruns = _secOverruns;
                    _secFrames = 0;
                    _secOverruns = 0;
                    _secStartTicks = nowTicks;
                }
                _secFrames = _secFrames + 1;
                if (overrun)
                {
                    _secOverruns = _secOverruns + 1;
                }
            }
        }

        /// <summary>
        /// 读窗口统计——整帧平均 / 峰值 + 各段明细（毫秒）；无样本时数值皆 0。
        /// </summary>
        /// <param name="avgMs">整帧平均耗时（输出）</param>
        /// <param name="peakMs">整帧峰值耗时（输出）</param>
        /// <param name="samples">样本帧数（输出）</param>
        /// <param name="segments">分段统计（输出——按首次出现顺序）</param>
        /// <param name="overrun">超帧统计（输出——帧预算 / 累计帧与超帧 / 上一秒帧与超帧）</param>
        public static void Read(out double avgMs, out double peakMs, out int samples, out List<SegmentStat> segments, out OverrunStat overrun)
        {
            lock (_gate)
            {
                samples = _count;
                segments = new List<SegmentStat>();
                overrun = new OverrunStat();
                overrun.BudgetMs = _lastBudgetMs;
                overrun.TotalFrames = _totalFrames;
                overrun.TotalOverruns = _totalOverruns;
                overrun.SecFrames = _secLastFrames;
                overrun.SecOverruns = _secLastOverruns;
                if (_count == 0)
                {
                    avgMs = 0;
                    peakMs = 0;
                    return;
                }
                double sum = 0;
                double peak = 0;
                for (int i = 0; i < _count; i = i + 1)
                {
                    double value = _frame[i];
                    sum = sum + value;
                    if (value > peak)
                    {
                        peak = value;
                    }
                }
                avgMs = sum / _count;
                peakMs = peak;
                for (int s = 0; s < _segNames.Count; s = s + 1)
                {
                    string name = _segNames[s];
                    double[] buffer = _segSamples[name];
                    double segSum = 0;
                    double segPeak = 0;
                    for (int i = 0; i < _count; i = i + 1)
                    {
                        double value = buffer[i];
                        segSum = segSum + value;
                        if (value > segPeak)
                        {
                            segPeak = value;
                        }
                    }
                    SegmentStat stat = new SegmentStat();
                    stat.Name = name;
                    stat.AvgMs = segSum / _count;
                    stat.PeakMs = segPeak;
                    segments.Add(stat);
                }
            }
        }

        /// <summary>
        /// 分段统计——段名 + 窗口均值 / 峰值（毫秒）。
        /// </summary>
        public sealed class SegmentStat
        {
            /// <summary>段名</summary>
            public string Name = "";

            /// <summary>窗口平均（毫秒）</summary>
            public double AvgMs;

            /// <summary>窗口峰值（毫秒）</summary>
            public double PeakMs;
        }

        /// <summary>
        /// 超帧统计——帧预算 + 累计计数 + 上一完整秒的帧数 / 超帧数。
        /// </summary>
        public sealed class OverrunStat
        {
            /// <summary>帧预算（毫秒——最近一次 EndFrame 传入）</summary>
            public int BudgetMs;

            /// <summary>累计帧数</summary>
            public long TotalFrames;

            /// <summary>累计超帧数</summary>
            public long TotalOverruns;

            /// <summary>上一完整秒帧数</summary>
            public int SecFrames;

            /// <summary>上一完整秒超帧数</summary>
            public int SecOverruns;
        }
    }
}
