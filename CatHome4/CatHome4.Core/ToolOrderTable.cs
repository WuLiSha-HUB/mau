using System;
using System.Collections.Generic;

namespace CH4
{
    /// <summary>
    /// 工具执行序表——工具分批调度的内核裁决面（A127 · design-ch4-tools §三·十四）。
    /// order 是宿主内部静态表，**不是 LLM 可写参数**：无论 LLM 以何序返回工具调用，一律按 order 分桶执行
    /// （同值一批 · 值升序 · 批间串行 / 批内并发）。LLM 知道机制存在（工具定义注释 + L1 元规则），
    /// 但调用序本身不承载语义。
    /// 档位：-100 timeback-start 钉死 / -1 只读 / 0 默认 / 1 写入变更 / 2 独占（每次调用各自成批）
    /// / 3 构建执行部署 / 100 timeback-back 钉死。
    /// 默认 0 落在只读之后、写入之前——未登记工具不会插到只读工具前面。
    /// 维护：全量登记——新增工具必须入表，对账门禁（ToolOrderTableTests）会红。
    /// </summary>
    public static class ToolOrderTable
    {
        /// <summary>钉死档——timeback-start：会话结构约束优先，忽略 LLM 调用序</summary>
        public const int OrderTimebackStart = -100;

        /// <summary>只读 / 无副作用档</summary>
        public const int OrderReadOnly = -1;

        /// <summary>默认档——未登记回落值；也承载不产生外部副作用、又不属只读观测的工具（sleep / timer）</summary>
        public const int OrderDefault = 0;

        /// <summary>写入 / 变更档</summary>
        public const int OrderWrite = 1;

        /// <summary>
        /// 独占档——本档工具**每次调用各自成一批**：不与任何工具同批，彼此之间也不同批（A144）。
        /// 适用面 = 「预检读全项目再落盘」与「独占工程构建面」——cs-* 语法树写操作在预检期读全项目源码，
        /// 与同批的文件写入撞车即 IOException（目标文件不同也冲突，判例 2026-10-02）；
        /// cs-build / cs-test 并发编译同一工程会争抢产物目录。本档把并发控制交给批次机制，工具层零锁。
        /// </summary>
        public const int OrderExclusive = 2;

        /// <summary>构建 / 执行 / 部署档</summary>
        public const int OrderBuild = 3;

        /// <summary>钉死档——timeback-back：会话结构约束优先，忽略 LLM 调用序</summary>
        public const int OrderTimebackBack = 100;

        /// <summary>只读档清单（-1）——观测 / 查询类，无外部副作用</summary>
        private static readonly string[] ReadOnlyNames =
        {
            "text-read", "text-read_lines", "text-read_between", "text-grep",
            "file-tree", "file-find", "file-version",
            "cs-check", "cs-list", "cs-read", "cs-find_ref", "cs-find", "cs-dead", "cs-comment_check",
            "mau-verify",
            "config-list", "config-get", "config-cat-get",
            "web-search", "web-ds-search", "image-analyze",
            "temp-info",
            "browser-open", "browser-read", "browser-shot",
            "majordomo-catinfo",
            "py-list",
            "db-query", "db-get",
            "host-flows", "info", "time", "random", "pack", "Note"
        };

        /// <summary>写入档清单（1）——改文件 / 改配置 / 改仓库产物</summary>
        private static readonly string[] WriteNames =
        {
            "text-write", "text-append", "text-replace",
            "file-move", "file-delete", "file-copy",
            "config-set", "config-reset", "config-cat-set",
            "image-inject",
            "browser-eval", "browser-tabs",
            "web-fetch", "web-fetch-jobs",
            "audio-play",
            "db-put", "db-update", "db-delete"
        };

        /// <summary>
        /// 独占档清单（2）——cs-* 写操作 / 工程构建 / 浏览器实例动作：预检读全项目、争抢构建产物目录、
        /// 同 profile 进程互斥（open 与 close 必须保序），每次调用各自成批（PlanBatches 单点裁决）。
        /// </summary>
        private static readonly string[] ExclusiveNames =
        {
            "cs-patch", "cs-member", "cs-comment", "cs-format", "cs-build", "cs-test",
            "browser-headful"
        };

        /// <summary>构建 / 执行 / 部署档清单（3）——外部通道执行、宿主动作</summary>
        private static readonly string[] BuildNames =
        {
            "powershell", "powershell7",
            "mau-gen", "mau-proj", "host-reload", "mau-setup",
            "restart-full", "restart-incr", "restart-host", "majordomo-cmd",
            "py-run", "py-service", "audio-speak",
            "temp-exec"
        };

        /// <summary>默认档清单（0）显式登记——登记在案，对账门禁以「全量登记」为判据</summary>
        private static readonly string[] DefaultNames =
        {
            "sleep", "timer"
        };

        /// <summary>钉死档清单——静态两名（timeback-start / timeback-back），值由工具名直接裁决（参数面无关）</summary>
        private static readonly string[] PinnedNames =
        {
            "timeback-start", "timeback-back"
        };

        /// <summary>
        /// 解析工具执行序——静态表裁决入口（timeback 两态 = 两个独立工具名，各取一端钉死值）。
        /// </summary>
        /// <param name="name">工具名</param>
        /// <returns>执行序值（未登记 = 默认档）</returns>
        public static int Resolve(string name)
        {
            if (name == null || name.Length == 0)
            {
                return OrderDefault;
            }
            if (name == "timeback-start")
            {
                return OrderTimebackStart;
            }
            if (name == "timeback-back")
            {
                return OrderTimebackBack;
            }
            if (Contains(ReadOnlyNames, name))
            {
                return OrderReadOnly;
            }
            if (Contains(WriteNames, name))
            {
                return OrderWrite;
            }
            if (Contains(ExclusiveNames, name))
            {
                return OrderExclusive;
            }
            if (Contains(BuildNames, name))
            {
                return OrderBuild;
            }
            return OrderDefault;
        }

        /// <summary>
        /// 独占判据——本档工具每次调用各自成一批（不与任何工具同批）。
        /// 单点裁决：批次计划（PlanBatches）与对账门禁共用本出口。
        /// </summary>
        /// <param name="name">工具名</param>
        /// <returns>true = 独占档</returns>
        public static bool IsExclusive(string name)
        {
            if (name == null || name.Length == 0)
            {
                return false;
            }
            return Contains(ExclusiveNames, name);
        }

        /// <summary>
        /// 分批计划——按 order 值升序分桶（同值一批 · 批内保持入参声明序）；独占档工具每次调用各自成批。
        /// 单点实装：ChatSession.BuildBatches 与测试共用本出口（批次规则只有一处）。
        /// </summary>
        /// <param name="names">工具名（入参序 = LLM 声明序；已闭合单须由调用方预筛）</param>
        /// <param name="orders">对应执行序值（与 names 同长）</param>
        /// <returns>批计划——每批为下标列表（下标指向入参序）</returns>
        public static List<List<int>> PlanBatches(IList<string> names, IList<int> orders)
        {
            List<List<int>> plan = new List<List<int>>();
            if (names == null || orders == null || names.Count != orders.Count)
            {
                return plan;
            }
            List<int> values = new List<int>();
            for (int i = 0; i < orders.Count; i = i + 1)
            {
                if (!values.Contains(orders[i]))
                {
                    values.Add(orders[i]);
                }
            }
            values.Sort();
            for (int v = 0; v < values.Count; v = v + 1)
            {
                List<int> current = new List<int>();
                for (int i = 0; i < orders.Count; i = i + 1)
                {
                    if (orders[i] != values[v])
                    {
                        continue;
                    }
                    if (IsExclusive(names[i]))
                    {
                        // 独占——当前批先闭合，本单自成一第二批（保证「每次调用各自成批」）
                        if (current.Count > 0)
                        {
                            plan.Add(current);
                            current = new List<int>();
                        }
                        List<int> single = new List<int>();
                        single.Add(i);
                        plan.Add(single);
                    }
                    else
                    {
                        current.Add(i);
                    }
                }
                if (current.Count > 0)
                {
                    plan.Add(current);
                }
            }
            return plan;
        }

        /// <summary>
        /// 执行序显示文本——toolcard 载荷与配置面共用出口（前端零裁决）。
        /// timeback 两态各自一名 → 单值数字串（-100 / 100）。
        /// </summary>
        /// <param name="name">工具名</param>
        /// <returns>显示文本（未知工具 = 默认档文本）</returns>
        public static string OrderText(string name)
        {
            return Resolve(name).ToString();
        }

        /// <summary>
        /// 工具定义标准注记——ToolPool.RebuildAll 单点注入（工具描述尾行）。
        /// 形态与 LLM 面约定一致：`order: &lt;n&gt;`。
        /// </summary>
        /// <param name="name">工具名</param>
        /// <returns>注记行（永不为空）</returns>
        public static string NoteLine(string name)
        {
            return "order: " + OrderText(name);
        }

        /// <summary>
        /// 已登记工具名全集——对账门禁基准（ToolPool 全量工具名须逐一登记）。
        /// </summary>
        /// <returns>已登记工具名数组（含默认档显式登记项）</returns>
        public static string[] RegisteredNames()
        {
            string[] all = new string[ReadOnlyNames.Length + WriteNames.Length + ExclusiveNames.Length + BuildNames.Length + DefaultNames.Length + PinnedNames.Length];
            int cursor = 0;
            for (int i = 0; i < ReadOnlyNames.Length; i = i + 1)
            {
                all[cursor] = ReadOnlyNames[i];
                cursor = cursor + 1;
            }
            for (int i = 0; i < WriteNames.Length; i = i + 1)
            {
                all[cursor] = WriteNames[i];
                cursor = cursor + 1;
            }
            for (int i = 0; i < ExclusiveNames.Length; i = i + 1)
            {
                all[cursor] = ExclusiveNames[i];
                cursor = cursor + 1;
            }
            for (int i = 0; i < BuildNames.Length; i = i + 1)
            {
                all[cursor] = BuildNames[i];
                cursor = cursor + 1;
            }
            for (int i = 0; i < DefaultNames.Length; i = i + 1)
            {
                all[cursor] = DefaultNames[i];
                cursor = cursor + 1;
            }
            for (int i = 0; i < PinnedNames.Length; i = i + 1)
            {
                all[cursor] = PinnedNames[i];
                cursor = cursor + 1;
            }
            return all;
        }

        /// <summary>
        /// 未登记工具名——对账门禁（A127）：池内工具必须逐一登记，未登记者静默落默认档。
        /// 调用面：宿主启动装配（LogToolOrderAccounting）；测试面：ToolOrderTests。
        /// </summary>
        /// <param name="names">工具名清单（池全量）</param>
        /// <returns>未登记名数组（顺序同入参；空 = 全量已登记）</returns>
        public static string[] FindUnregistered(string[] names)
        {
            if (names == null)
            {
                return new string[0];
            }
            string[] registered = RegisteredNames();
            List<string> missing = new List<string>();
            for (int i = 0; i < names.Length; i = i + 1)
            {
                if (names[i] == null || names[i].Length == 0)
                {
                    continue;
                }
                if (!Contains(registered, names[i]))
                {
                    missing.Add(names[i]);
                }
            }
            return missing.ToArray();
        }

        /// <summary>
        /// 名单线性查找——名单规模小（＜100），线性查找免建索引（勿增实体）。
        /// </summary>
        /// <param name="names">名单</param>
        /// <param name="name">工具名</param>
        /// <returns>true=命中</returns>
        private static bool Contains(string[] names, string name)
        {
            for (int i = 0; i < names.Length; i = i + 1)
            {
                if (string.Equals(names[i], name, StringComparison.Ordinal))
                {
                    return true;
                }
            }
            return false;
        }
    }
}
