using System;
using System.Collections.Generic;
using System.Text;

namespace CatHome4.Contracts
{
    /// <summary>
    /// 启动降级条目——一次启动中某能力因配置或环境异常而降低形态的记账单元。
    /// 五要素：机器码 / 类别 / 受影响能力 / 形态 / 修复指引（规格 design-ch4-host-bootstrap-resilience §2.3）。
    /// </summary>
    public sealed class StartupDegradeEntry
    {
        /// <summary>机器码（大写下划线，如 ROOT_UNREACHABLE / PORT_FALLBACK / CFG_CORRUPT）</summary>
        public string Code { get; set; } = "";

        /// <summary>类别：config / env / data / external / internal（internal = 代码缺陷，ERROR 级）</summary>
        public string Kind { get; set; } = "";

        /// <summary>受影响能力（文件工具 / 外观层 / QQ 通道 / LLM 请求……）</summary>
        public string Ability { get; set; } = "";

        /// <summary>形态：unavailable / readonly / empty / fallback / rebuilt</summary>
        public string State { get; set; } = "";

        /// <summary>人读原因——必须含具体对象（路径 / 端口 / 文件名）</summary>
        public string Detail { get; set; } = "";

        /// <summary>修复指引（去哪改什么，一句话）</summary>
        public string Fix { get; set; } = "";

        /// <summary>启动段名（S0..S7）</summary>
        public string Stage { get; set; } = "";

        /// <summary>登记时刻（yyyy-MM-dd HH:mm:ss）</summary>
        public string At { get; set; } = "";

        /// <summary>同一 Code + Ability 的累计次数（去重口径）</summary>
        public int Count { get; set; }
    }

    /// <summary>
    /// 启动降级登记表——启动链唯一的降级记账点。
    /// 写者：各启动段与降级点（经 Report）；读者：日志出口 / 快照 boot 段 / 面板启动健康页签 / 启动提示窗 / startup_report.json。
    /// 线程安全：启动期以主线程为主，附属段可能来自线程池，读写一律走锁。
    /// 去重口径：Code + Ability 相同者累加 Count，不重复列条（详情保留首条）。
    /// </summary>
    public static class StartupDegradation
    {
        /// <summary>类别常量——配置错误</summary>
        public const string KindConfig = "config";

        /// <summary>类别常量——外部环境（路径 / 权限 / 端口 / 磁盘）</summary>
        public const string KindEnv = "env";

        /// <summary>类别常量——数据文件损坏</summary>
        public const string KindData = "data";

        /// <summary>类别常量——外部服务（QQ / LLM / 浏览器内核）</summary>
        public const string KindExternal = "external";

        /// <summary>类别常量——内部一致性缺陷（不计入「可忽略」，ERROR 级出声）</summary>
        public const string KindInternal = "internal";

        /// <summary>形态常量——能力不提供</summary>
        public const string StateUnavailable = "unavailable";

        /// <summary>形态常量——读面可用写面拒绝</summary>
        public const string StateReadonly = "readonly";

        /// <summary>形态常量——按空数据继续</summary>
        public const string StateEmpty = "empty";

        /// <summary>形态常量——换端口 / 换落点</summary>
        public const string StateFallback = "fallback";

        /// <summary>形态常量——缺目录 / 缺文件补建默认</summary>
        public const string StateRebuilt = "rebuilt";

        /// <summary>登记表锁——启动期跨线程写入的唯一同步点</summary>
        private static readonly object _gate = new object();

        /// <summary>条目表（按登记顺序；去重时就地累加）</summary>
        private static readonly List<StartupDegradeEntry> _entries = new List<StartupDegradeEntry>();

        /// <summary>
        /// 登记一条降级——同 Code + Ability 已存在时只累加次数（详情与修复指引保留首条）。
        /// </summary>
        /// <param name="code">机器码</param>
        /// <param name="kind">类别（KindConfig / KindEnv / KindData / KindExternal / KindInternal）</param>
        /// <param name="ability">受影响能力</param>
        /// <param name="state">形态（StateUnavailable / StateReadonly / StateEmpty / StateFallback / StateRebuilt）</param>
        /// <param name="detail">人读原因（含具体对象）</param>
        /// <param name="fix">修复指引</param>
        /// <param name="stage">启动段名</param>
        /// <returns>登记后的条目（去重时为既有条目）</returns>
        public static StartupDegradeEntry Report(string code, string kind, string ability, string state,
            string detail, string fix, string stage)
        {
            lock (_gate)
            {
                for (int i = 0; i < _entries.Count; i = i + 1)
                {
                    StartupDegradeEntry existing = _entries[i];
                    if (string.Equals(existing.Code, code, StringComparison.Ordinal)
                        && string.Equals(existing.Ability, ability, StringComparison.Ordinal))
                    {
                        existing.Count = existing.Count + 1;
                        existing.At = DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss");
                        return existing;
                    }
                }
                StartupDegradeEntry entry = new StartupDegradeEntry();
                entry.Code = code;
                entry.Kind = kind;
                entry.Ability = ability;
                entry.State = state;
                entry.Detail = detail;
                entry.Fix = fix;
                entry.Stage = stage;
                entry.At = DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss");
                entry.Count = 1;
                _entries.Add(entry);
                return entry;
            }
        }

        /// <summary>
        /// 取当前登记快照——一次调用一份深拷贝（读面不持锁遍历可变集合）。
        /// </summary>
        /// <returns>条目数组（按登记顺序）</returns>
        public static StartupDegradeEntry[] Snapshot()
        {
            lock (_gate)
            {
                StartupDegradeEntry[] copy = new StartupDegradeEntry[_entries.Count];
                for (int i = 0; i < _entries.Count; i = i + 1)
                {
                    StartupDegradeEntry src = _entries[i];
                    StartupDegradeEntry dst = new StartupDegradeEntry();
                    dst.Code = src.Code;
                    dst.Kind = src.Kind;
                    dst.Ability = src.Ability;
                    dst.State = src.State;
                    dst.Detail = src.Detail;
                    dst.Fix = src.Fix;
                    dst.Stage = src.Stage;
                    dst.At = src.At;
                    dst.Count = src.Count;
                    copy[i] = dst;
                }
                return copy;
            }
        }

        /// <summary>
        /// 当前降级条目数。
        /// </summary>
        public static int Count
        {
            get
            {
                lock (_gate)
                {
                    return _entries.Count;
                }
            }
        }

        /// <summary>
        /// 是否存在降级条目（弹窗 / 角标判据）。
        /// </summary>
        public static bool HasAny
        {
            get
            {
                lock (_gate)
                {
                    return _entries.Count > 0;
                }
            }
        }

        /// <summary>
        /// 是否存在内部缺陷类降级（计入「需上报」栏——不得当作环境问题忽略）。
        /// </summary>
        public static bool HasInternal
        {
            get
            {
                lock (_gate)
                {
                    for (int i = 0; i < _entries.Count; i = i + 1)
                    {
                        if (string.Equals(_entries[i].Kind, KindInternal, StringComparison.Ordinal))
                        {
                            return true;
                        }
                    }
                    return false;
                }
            }
        }

        /// <summary>
        /// 清空登记表——测试隔离专用（启动链不使用）。
        /// </summary>
        public static void Clear()
        {
            lock (_gate)
            {
                _entries.Clear();
            }
        }

        /// <summary>
        /// 摘要行——单行文本（日志 / 托盘 ToolTip / 弹窗标题共用）：`启动降级 N 项：CODE1, CODE2`。
        /// </summary>
        /// <returns>摘要文本（无降级为空串）</returns>
        public static string BuildSummaryLine()
        {
            lock (_gate)
            {
                if (_entries.Count == 0)
                {
                    return "";
                }
                StringBuilder sb = new StringBuilder();
                sb.Append("启动降级 ");
                sb.Append(_entries.Count.ToString());
                sb.Append(" 项：");
                for (int i = 0; i < _entries.Count; i = i + 1)
                {
                    if (i > 0)
                    {
                        sb.Append(", ");
                    }
                    sb.Append(_entries[i].Code);
                    if (_entries[i].Count > 1)
                    {
                        sb.Append("×");
                        sb.Append(_entries[i].Count.ToString());
                    }
                }
                return sb.ToString();
            }
        }
    }
}
