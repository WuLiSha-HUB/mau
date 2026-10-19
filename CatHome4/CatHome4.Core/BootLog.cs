using System;
using System.Collections.Generic;
using System.IO;
using System.Text;

namespace CatHome4.Core
{
    /// <summary>
    /// 启动早期诊断 sink——配置面与观测体系就绪之前的基础可写落点（规格 design-ch4-host-bootstrap-resilience §2.1）。
    /// 三级落点：数据根 Data/runs/&lt;ts&gt; → %TEMP%/CatHome4/runs/&lt;ts&gt; → 仅内存。
    /// 写入形态为开-写-关（不持有长驻句柄）——与 LogStore 的常驻写者互不干扰。
    /// 失败不抛：早期阶段抛异常即是启动中断，本类存在的意义正是避免这一中断。
    /// </summary>
    public static class BootLog
    {
        /// <summary>落点形态——常规（数据根 runs 目录）</summary>
        public const string SinkNormal = "normal";

        /// <summary>落点形态——临时目录回退</summary>
        public const string SinkTemp = "temp";

        /// <summary>落点形态——仅内存（两处均不可写）</summary>
        public const string SinkMemory = "memory";

        /// <summary>sink 锁——启动期主线程为主，附属段可能跨线程</summary>
        private static readonly object _gate = new object();

        /// <summary>内存缓冲（无论落盘成败，均保留至回灌）</summary>
        private static readonly List<string> _lines = new List<string>();

        /// <summary>实际落点目录（空 = 仅内存）</summary>
        private static string _sinkDir = "";

        /// <summary>落点形态（normal / temp / memory）</summary>
        private static string _sinkMode = SinkMemory;

        /// <summary>是否已初始化（幂等判据）</summary>
        private static bool _initialized;

        /// <summary>落盘是否出现失败（内存仍完整——回灌时作为降级项登记）</summary>
        private static bool _writeFailed;

        /// <summary>
        /// 实际落点目录（空串 = 仅内存）。
        /// </summary>
        public static string SinkDir
        {
            get
            {
                lock (_gate)
                {
                    return _sinkDir;
                }
            }
        }

        /// <summary>
        /// 落点形态（normal / temp / memory）。
        /// </summary>
        public static string SinkMode
        {
            get
            {
                lock (_gate)
                {
                    return _sinkMode;
                }
            }
        }

        /// <summary>
        /// 落盘是否出现过失败（早期条目仍可从内存取回）。
        /// </summary>
        public static bool WriteFailed
        {
            get
            {
                lock (_gate)
                {
                    return _writeFailed;
                }
            }
        }

        /// <summary>
        /// 初始化 sink——解析三级落点并写一行头部。幂等：已初始化则直接返回。
        /// </summary>
        /// <param name="dataRoot">数据根父级目录（Data 的上级；空串=不可用）</param>
        public static void Init(string dataRoot)
        {
            lock (_gate)
            {
                if (_initialized)
                {
                    return;
                }
                _initialized = true;
                string stamp = DateTime.Now.ToString("yyyyMMdd_HHmmss");
                // [段1] 候选 1——数据根 runs 目录（与观测四文件同址）
                if (dataRoot != null && dataRoot.Length > 0)
                {
                    string normal = Path.Combine(dataRoot, "Data", "runs", stamp);
                    if (TryOpen(normal))
                    {
                        _sinkDir = normal;
                        _sinkMode = SinkNormal;
                        AppendLine(BuildHeader(stamp, SinkNormal));
                        return;
                    }
                }
                // [段2] 候选 2——临时目录（数据根不可写 / 解析失败）
                string temp = "";
                try
                {
                    temp = Path.Combine(Path.GetTempPath(), "CatHome4", "runs", stamp);
                }
                catch (Exception)
                {
                    temp = "";
                }
                if (temp.Length > 0 && TryOpen(temp))
                {
                    _sinkDir = temp;
                    _sinkMode = SinkTemp;
                    AppendLine(BuildHeader(stamp, SinkTemp));
                    return;
                }
                // [段3] 候选 3——仅内存（磁盘满 / 权限全失）
                _sinkDir = "";
                _sinkMode = SinkMemory;
                _lines.Add(BuildHeader(stamp, SinkMemory));
            }
        }

        /// <summary>
        /// 记一条早期诊断——内存必留，磁盘尽力写（失败置标志，不抛）。
        /// </summary>
        /// <param name="stage">段名（S0..S7 或早期标记）</param>
        /// <param name="message">内容</param>
        public static void Add(string stage, string message)
        {
            string line = DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss.fff") + " | " + stage + " | " + message;
            lock (_gate)
            {
                _lines.Add(line);
                if (_sinkDir.Length > 0)
                {
                    if (!AppendLine(line))
                    {
                        _writeFailed = true;
                    }
                }
            }
        }

        /// <summary>
        /// 取走全部缓冲行并清空内存（回灌 LogStore 用；磁盘文件保留供人工查看）。
        /// </summary>
        /// <returns>条目行数组（按写入序）</returns>
        public static string[] Drain()
        {
            lock (_gate)
            {
                string[] copy = _lines.ToArray();
                _lines.Clear();
                return copy;
            }
        }

        /// <summary>
        /// 目录可用性探测——建目录 + 试写一行探针（探针留在文件内，作为时间线起点）。
        /// </summary>
        /// <param name="dir">候选目录</param>
        /// <returns>可用返回 true</returns>
        private static bool TryOpen(string dir)
        {
            try
            {
                Directory.CreateDirectory(dir);
                string probe = Path.Combine(dir, "startup.log");
                File.AppendAllText(probe, "", new UTF8Encoding(false));
                return true;
            }
            catch (Exception)
            {
                return false;
            }
        }

        /// <summary>
        /// 追加一行——开-写-关，异常转 false（调用方置失败标志）。
        /// </summary>
        /// <param name="line">行文本</param>
        /// <returns>写入成功返回 true</returns>
        private static bool AppendLine(string line)
        {
            if (_sinkDir.Length == 0)
            {
                return false;
            }
            try
            {
                string probe = Path.Combine(_sinkDir, "startup.log");
                File.AppendAllText(probe, line + Environment.NewLine, new UTF8Encoding(false));
                return true;
            }
            catch (Exception)
            {
                return false;
            }
        }

        /// <summary>
        /// 头部行——记录实例与落点形态（后续排查的第一行）。
        /// </summary>
        /// <param name="stamp">时间戳串</param>
        /// <param name="mode">落点形态</param>
        /// <returns>头部文本</returns>
        private static string BuildHeader(string stamp, string mode)
        {
            string version = "";
            try
            {
                version = typeof(BootLog).Assembly.GetName().Version.ToString();
            }
            catch (Exception)
            {
                version = "unknown";
            }
            return "==== CatHome4 启动诊断 | " + stamp + " | sink=" + mode + " | core=" + version + " ====";
        }
    }
}
