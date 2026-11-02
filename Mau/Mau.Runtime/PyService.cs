using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Runtime.InteropServices;
using System.Text;
using System.Threading;

namespace Mau.Runtime
{
    /// <summary>
    /// Python 执行服务——py 工具组的执行面与 py 服务进程管理（design-ch4-pytools §二 / §五）。
    /// <para>
    /// 两类入口：<see cref="Run"/>（短命脚本，起-跑-退）· <see cref="Service"/> / <see cref="Invoke"/>
    /// （常驻服务，stdio 严格协议——一行 JSON 进 / 一行 JSON 出，stdout 只出协议行）。
    /// </para>
    /// <para>
    /// 解释器路径读配置 <c>py.python_path</c>（每服务可用 <c>py.service.&lt;name&gt;.python</c> 覆盖——
    /// 系统 Python 与 venv 的依赖面不同，全局单解释器跑不起来）；未配置即明示不可用，不回落探测路径。
    /// </para>
    /// <para>
    /// 进程句柄归本服务持有（不在 Flow 层——Flow 可热重载，句柄存 Flow 里一次 reload 即悬空成孤儿）。
    /// 宿主退出时统一清理（<see cref="Dispose"/> + ProcessExit 钩子），不留孤儿。
    /// </para>
    /// </summary>
    public sealed partial class PyService : IPyService, IDisposable
    {
        // [段1] 字段
        /// <summary>
        /// 可写受控根——脚本路径必须落在其一之内
        /// </summary>
        private readonly string[] _roots;

        /// <summary>
        /// 可写根条目表——A257 根寻址公共服务入参（只含 writable 根）
        /// </summary>
        private readonly WorkspaceConfig.RootEntry[] _entries;

        /// <summary>
        /// 默认脚本扫描面——mau 根下的 tools 目录（List 未给 dir 时的默认面；无 mau 根即空串 → 明示不可用）
        /// </summary>
        private readonly string _defaultScanDir;

        /// <summary>
        /// 根 Id → 规范路径（可写根）——支撑 <c>&lt;根id&gt;:&lt;相对&gt;</c> 寻址（与 text-* / file-* 同源）
        /// </summary>
        private readonly Dictionary<string, string> _rootIndex = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);

        /// <summary>
        /// 服务池（服务名 → 持有进程）
        /// </summary>
        private readonly Dictionary<string, StdioServiceHost> _servers = new Dictionary<string, StdioServiceHost>(StringComparer.Ordinal);

        /// <summary>
        /// 服务池锁
        /// </summary>
        private readonly object _gate = new object();

        /// <summary>
        /// 单路输出截断上限（字符）
        /// </summary>
        private const int MaxOutputChars = 60000;

        /// <summary>
        /// 默认超时毫秒（与工具执行层统一口径）
        /// </summary>
        private const int DefaultTimeoutMs = 120000;

        /// <summary>
        /// 连续拉起失败上限——达上限置「不可用」态（防崩溃风暴；需重启恢复）
        /// </summary>
        private const int MaxLaunchFailures = 5;

        /// <summary>
        /// Job 对象句柄——强杀兜底（构造时建立；不可用 0）
        /// </summary>
        private readonly IntPtr _jobHandle;

        /// <summary>
        /// 释放标志
        /// </summary>
        private bool _disposed;

        // [段2] 构造
        /// <summary>
        /// 建立服务——收可写受控根（脚本路径校验面），注册宿主退出清理钩子。
        /// </summary>
        /// <param name="roots">受控根表（只收 writable=true 的根）</param>
        public PyService(WorkspaceConfig.RootEntry[] roots)
        {
            if (roots == null || roots.Length == 0)
            {
                throw new ArgumentException("At least one workspace root is required.", "roots");
            }
            List<string> writable = new List<string>();
            List<WorkspaceConfig.RootEntry> writableEntries = new List<WorkspaceConfig.RootEntry>();
            for (int i = 0; i < roots.Length; i = i + 1)
            {
                if (!roots[i].Writable || roots[i].Path.Length == 0)
                {
                    continue;
                }
                string normalized = Path.GetFullPath(roots[i].Path).TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar);
                writable.Add(normalized);
                WorkspaceConfig.RootEntry entry = new WorkspaceConfig.RootEntry();
                entry.Id = roots[i].Id == null ? "" : roots[i].Id;
                entry.Path = normalized;
                entry.Writable = true;
                writableEntries.Add(entry);
                if (roots[i].Id != null && roots[i].Id.Length > 0)
                {
                    _rootIndex[roots[i].Id] = normalized;
                }
            }
            if (writable.Count == 0)
            {
                throw new ArgumentException("At least one writable root is required.", "roots");
            }
            _roots = writable.ToArray();
            _entries = writableEntries.ToArray();
            _defaultScanDir = ResolveDefaultScanDir(roots);
            AppDomain.CurrentDomain.ProcessExit += OnProcessExit;
            _jobHandle = EnsureJob();
        }

        /// <summary>
        /// 解析默认脚本扫描面——mau 根下的 tools 目录（py 工具线的约定面）。
        /// </summary>
        /// <param name="roots">受控根原始表</param>
        /// <returns>默认扫描面绝对路径；无 mau 根时为空串</returns>
        private static string ResolveDefaultScanDir(WorkspaceConfig.RootEntry[] roots)
        {
            for (int i = 0; i < roots.Length; i = i + 1)
            {
                if (!roots[i].Writable || roots[i].Path.Length == 0)
                {
                    continue;
                }
                if (!string.Equals(roots[i].Id, "mau", StringComparison.OrdinalIgnoreCase))
                {
                    continue;
                }
                string root = Path.GetFullPath(roots[i].Path);
                return Path.Combine(root, "tools");
            }
            return "";
        }

        // [段5] 服务自启（宿主启动链调用——接线后做）
        /// <summary>
        /// 按配置拉起 follow=true 的服务——服务清单自配置扫描得出
        /// （键 py.service.&lt;name&gt;.script 存在即视为已声明服务）。
        /// </summary>
        /// <returns>结果 JSON（started / skipped / errors 数组）或 ERR| 前缀错误文本</returns>
        public string Autostart()
        {
            SelfHealOrphans();
            ConfigStore? cfg;
            DataBox.TryResolve<ConfigStore>(out cfg);
            if (cfg == null)
            {
                return "ERR|PY_NOT_CONFIGURED|配置存储未就绪——无法读取 py 服务声明";
            }

            List<string> names = ScanServiceNames(cfg);

            List<string> started = new List<string>();
            List<string> skipped = new List<string>();
            List<string> errors = new List<string>();
            for (int i = 0; i < names.Count; i = i + 1)
            {
                string name = names[i];
                string follow = cfg.Get("py.service." + name + ".follow", "false");
                if (!string.Equals(follow, "true", StringComparison.OrdinalIgnoreCase))
                {
                    skipped.Add(name);
                    continue;
                }
                string result = Service(JsonUtil.Object(("name", name), ("action", "start")));
                if (result.StartsWith("ERR|", StringComparison.Ordinal))
                {
                    errors.Add(name);
                    LogStore.Add("PyService", 2, "服务自启失败（" + name + "）: " + result, "SYS");
                }
                else
                {
                    started.Add(name);
                }
            }

            Dictionary<string, object> payload = new Dictionary<string, object>();
            payload["started"] = started;
            payload["skipped"] = skipped;
            payload["errors"] = errors;
            return JsonUtil.Serialize(payload);
        }

        /// <summary>
        /// 扫描服务清单——键 <c>py.service.&lt;name&gt;.script</c> 存在即视为已声明服务（Autostart 与 Enumerate 共用单一实现）。
        /// </summary>
        /// <param name="cfg">配置存储</param>
        /// <returns>服务名列表（去重、保序）</returns>
        private static List<string> ScanServiceNames(ConfigStore cfg)
        {
            const string prefix = "py.service.";
            const string suffix = ".script";
            KeyValuePair<string, string>[] all = cfg.All();
            List<string> names = new List<string>();
            foreach (KeyValuePair<string, string> item in all)
            {
                if (!item.Key.StartsWith(prefix, StringComparison.Ordinal) || !item.Key.EndsWith(suffix, StringComparison.Ordinal))
                {
                    continue;
                }
                string name = item.Key.Substring(prefix.Length, item.Key.Length - prefix.Length - suffix.Length);
                if (name.Length > 0 && !names.Contains(name))
                {
                    names.Add(name);
                }
            }
            return names;
        }

        // [段6] 服务控制
        /// <summary>
        /// 服务控制——start（拉起并等 ready）/ stop（优雅退出 + 强杀兜底）/ status（查进程态）。
        /// </summary>
        /// <param name="argsJson">参数 JSON（name 必填；action 必填）</param>
        /// <returns>结果 JSON 或 ERR| 前缀错误文本</returns>
        public string Service(string argsJson)
        {
            string name = JsonArgs.Get(argsJson, "name");
            string action = JsonArgs.Get(argsJson, "action");
            if (name.Length == 0 || name == "§PARSE_FAIL§")
            {
                return "ERR|PY_BAD_ARGS|缺少参数 name";
            }
            if (action.Length == 0 || action == "§PARSE_FAIL§")
            {
                return "ERR|PY_BAD_ARGS|缺少参数 action（start|stop|status）";
            }

            lock (_gate)
            {
                StdioServiceHost? existing;
                _servers.TryGetValue(name, out existing);

                if (string.Equals(action, "status", StringComparison.Ordinal))
                {
                    return DescribeStatus(name, existing);
                }

                if (string.Equals(action, "stop", StringComparison.Ordinal))
                {
                    if (existing == null)
                    {
                        return ServiceJson(name, "stopped", "服务未启动", false);
                    }
                    string stopError = existing.Stop();
                    _servers.Remove(name);
                    RemoveAnchor(name);
                    if (stopError.Length > 0)
                    {
                        return stopError;
                    }
                    return ServiceJson(name, "stopped", "", false);
                }

                if (!string.Equals(action, "start", StringComparison.Ordinal))
                {
                    return "ERR|PY_BAD_ARGS|action 非法值: " + action + "（start|stop|status）";
                }

                // [段1] start——已存活即幂等返回
                if (existing != null && existing.IsAlive())
                {
                    return ServiceJson(name, "running", "服务已在运行", true);
                }

                string scriptError;
                string script = ResolveServiceScript(name, out scriptError);
                if (script.Length == 0)
                {
                    return scriptError;
                }
                string pythonError;
                string python = ResolvePython(name, out pythonError);
                if (python.Length == 0)
                {
                    return pythonError;
                }
                int timeoutMs = ParseServiceTimeout(name);

                string servArgs = ResolveServiceArgs(name);
                string servHome = ResolveServiceHome(name);
                StdioServiceHost server = new StdioServiceHost(name, python, script, Path.GetDirectoryName(script) ?? "", timeoutMs, servArgs, servHome,
                    "-u ", "PY", "PyService", MaxLaunchFailures);
                server.JobHandle = _jobHandle;
                _servers[name] = server;
                string startError = server.Start();
                if (startError.Length > 0)
                {
                    _servers.Remove(name);
                    return startError;
                }
                WriteAnchor(name, server.ProcessId());
                return ServiceJson(name, "running", "", true);
            }
        }

        // [段6] 服务调用
        /// <summary>
        /// 服务调用——按 stdio 严格协议发一行 JSON、读一行 JSON（单服务纯串行）。
        /// 未启动则按需拉起（follow 语义的按需分支）；进程崩溃则重拉并在回执标注。
        /// </summary>
        /// <param name="argsJson">参数 JSON（name 必填；payload 必填——请求体 JSON 文本）</param>
        /// <returns>服务回执 JSON 或 ERR| 前缀错误文本</returns>
        public string Invoke(string argsJson)
        {
            string name = JsonArgs.Get(argsJson, "name");
            string payload = JsonArgs.Get(argsJson, "payload");
            if (name.Length == 0 || name == "§PARSE_FAIL§")
            {
                return "ERR|PY_BAD_ARGS|缺少参数 name";
            }
            if (payload.Length == 0 || payload == "§PARSE_FAIL§")
            {
                return "ERR|PY_BAD_ARGS|缺少参数 payload（请求体 JSON 文本）";
            }

            lock (_gate)
            {
                StdioServiceHost? server;
                _servers.TryGetValue(name, out server);
                if (server == null)
                {
                    string scriptError;
                    string script = ResolveServiceScript(name, out scriptError);
                    if (script.Length == 0)
                    {
                        return scriptError;
                    }
                    string pythonError;
                    string python = ResolvePython(name, out pythonError);
                    if (python.Length == 0)
                    {
                        return pythonError;
                    }
                    string servArgs = ResolveServiceArgs(name);
                    string servHome = ResolveServiceHome(name);
                    server = new StdioServiceHost(name, python, script, Path.GetDirectoryName(script) ?? "", ParseServiceTimeout(name), servArgs, servHome,
                        "-u ", "PY", "PyService", MaxLaunchFailures);
                    server.JobHandle = _jobHandle;
                    _servers[name] = server;
                }
                string invokeReply = server.Invoke(payload);
                if (server.IsAlive())
                {
                    WriteAnchor(name, server.ProcessId());
                }
                return invokeReply;
            }
        }

        // [段6c] 服务枚举（A254 资源占用页签）
        /// <summary>
        /// 服务枚举——全部已声明服务（配置扫描；含未启动项）+ 运行态 + 进程内存占用。
        /// </summary>
        /// <returns>结果 JSON（services 数组：name / state / alive / pid / ws）或 ERR| 前缀错误文本</returns>
        public string Enumerate()
        {
            ConfigStore? cfg;
            DataBox.TryResolve<ConfigStore>(out cfg);
            if (cfg == null)
            {
                return "ERR|PY_NOT_CONFIGURED|配置存储未就绪——无法读取 py 服务声明";
            }

            List<string> names = ScanServiceNames(cfg);
            List<object> services = new List<object>();
            lock (_gate)
            {
                for (int i = 0; i < names.Count; i = i + 1)
                {
                    string name = names[i];
                    StdioServiceHost? server;
                    _servers.TryGetValue(name, out server);
                    bool alive = server != null && server.IsAlive();
                    int pid = 0;
                    long ws = 0;
                    if (alive && server != null)
                    {
                        pid = server.ProcessId();
                        ws = ServiceStat.ReadProcessTreeWorkingSet(pid);
                    }

                    Dictionary<string, object> item = new Dictionary<string, object>();
                    item["name"] = name;
                    item["state"] = alive ? "running" : "stopped";
                    item["alive"] = alive;
                    item["pid"] = pid;
                    item["ws"] = ws;
                    services.Add(item);
                }
            }

            Dictionary<string, object> payload = new Dictionary<string, object>();
            payload["services"] = services;
            return JsonUtil.Serialize(payload);
        }

        /// <summary>
        /// 建立 Job 对象——惰性创建（KILL_ON_JOB_CLOSE）。
        /// 非 Windows / 创建失败返回 <see cref="IntPtr.Zero"/>（强杀兜底关闭，启动前自愈仍生效，其余功能不受影响）。
        /// </summary>
        /// <returns>Job 句柄；不可用 0</returns>
        private IntPtr EnsureJob()
        {
            return ServiceJob.Ensure("PyService");
        }

        // [段7] 释放
        /// <summary>
        /// 停止全部服务进程——宿主退出时统一清理，不留孤儿。
        /// </summary>
        public void Dispose()
        {
            if (_disposed)
            {
                return;
            }
            _disposed = true;
            lock (_gate)
            {
                foreach (KeyValuePair<string, StdioServiceHost> pair in _servers)
                {
                    try
                    {
                        pair.Value.Stop();
                    }
                    catch (Exception ex)
                    {
                        LogStore.Add("PyService", 2, "服务停止异常（" + pair.Key + "）: " + ex.Message, "SYS");
                    }
                    RemoveAnchor(pair.Key);
                }
                _servers.Clear();
            }
        }

        /// <summary>
        /// 宿主退出回调——统一清理服务进程。
        /// </summary>
        /// <param name="sender">事件源</param>
        /// <param name="args">事件参数</param>
        private void OnProcessExit(object? sender, EventArgs args)
        {
            try
            {
                Dispose();
            }
            catch (Exception ex)
            {
                LogStore.Add("PyService", 3, "退出清理异常: " + ex.Message, "SYS");
            }
        }

        // [段8] 配置解析
        /// <summary>
        /// 解析解释器路径——服务级覆盖优先，回退全局；未配置 / 路径不存在即明示 ERR。
        /// </summary>
        /// <param name="serviceName">服务名（空 = 全局）</param>
        /// <param name="error">错误文本（空=成功）</param>
        /// <returns>python.exe 全路径（失败返回空串）</returns>
        private static string ResolvePython(string serviceName, out string error)
        {
            error = "";
            ConfigStore? cfg;
            DataBox.TryResolve<ConfigStore>(out cfg);
            string path = "";
            if (cfg != null && serviceName.Length > 0)
            {
                path = cfg.Get("py.service." + serviceName + ".python", "");
            }
            if (path.Length == 0 && cfg != null)
            {
                path = cfg.Get("py.python_path", "");
            }
            if (path.Length == 0)
            {
                error = "ERR|PY_NOT_CONFIGURED|Python 解释器未配置——请在配置面填写 py.python_path（python.exe 全路径；获取方式 py -0p）";
                return "";
            }
            if (!File.Exists(path))
            {
                error = "ERR|PY_PYTHON_NOT_FOUND|配置的解释器路径不存在：" + path + "——请更新 py.python_path";
                return "";
            }
            return path;
        }

        /// <summary>解析服务入口脚本——读配置 py.service.&lt;name&gt;.script（受控根寻址：mau:tools/... 或全路径）；不存在即明示 ERR（失败可见，不静默）。</summary>
        /// <param name="serviceName">服务名</param>
        /// <param name="error">错误文本（空=成功）</param>
        /// <returns>脚本全路径（失败返回空串）</returns>
        private string ResolveServiceScript(string serviceName, out string error)
        {
            error = "";
            ConfigStore? cfg;
            DataBox.TryResolve<ConfigStore>(out cfg);
            string script = cfg == null ? "" : cfg.Get("py.service." + serviceName + ".script", "");
            if (script.Length == 0)
            {
                error = "ERR|PY_SERVICE_NOT_CONFIGURED|服务未配置：" + serviceName + "——请在配置面填写 py.service." + serviceName + ".script";
                return "";
            }
            RootResolveResult rr = RootPathResolver.Expand(script, _entries, Environment.CurrentDirectory);
            if (!rr.Ok)
            {
                error = "ERR|PY_BAD_PATH|服务脚本路径非法: " + rr.Message;
                return "";
            }
            string full = rr.FullPath;
            if (!IsUnderWritableRoot(full))
            {
                error = "ERR|PY_SCRIPT_OUTSIDE_ROOT|服务脚本超出受控根：" + full + "——只允许可写受控根内的脚本";
                return "";
            }
            if (!File.Exists(full))
            {
                error = "ERR|PY_SCRIPT_NOT_FOUND|服务脚本不存在：" + full;
                return "";
            }
            return full;
        }

        /// <summary>
        /// 解析服务启动参数——py.service 服务名对应的 args（命令行附加串；空=无）。
        /// </summary>
        /// <param name="serviceName">服务名</param>
        /// <returns>附加参数串（空=无）</returns>
        private static string ResolveServiceArgs(string serviceName)
        {
            ConfigStore? cfg;
            DataBox.TryResolve<ConfigStore>(out cfg);
            string args = cfg == null ? "" : cfg.Get("py.service." + serviceName + ".args", "");
            return args;
        }
        /// <summary>
        /// 解析服务环境目录——py.service.&lt;name&gt;.home（外部环境根，如 Kokoro 的 KOKORO_HOME）。
        /// 起服务时以 --home 传入（服务脚本自行消费）；空=不传（不推导、不回落）。
        /// </summary>
        /// <param name="serviceName">服务名</param>
        /// <returns>环境目录（空=不传）</returns>
        private static string ResolveServiceHome(string serviceName)
        {
            ConfigStore? cfg;
            DataBox.TryResolve<ConfigStore>(out cfg);
            return cfg == null ? "" : cfg.Get("py.service." + serviceName + ".home", "");
        }

        /// <summary>
        /// 解析服务单次调用超时——py.service.&lt;name&gt;.timeout_ms。
        /// </summary>
        /// <param name="serviceName">服务名</param>
        /// <returns>超时毫秒</returns>
        private static int ParseServiceTimeout(string serviceName)
        {
            ConfigStore? cfg;
            DataBox.TryResolve<ConfigStore>(out cfg);
            string raw = cfg == null ? "" : cfg.Get("py.service." + serviceName + ".timeout_ms", "");
            return ParseTimeout(raw);
        }

        /// <summary>
        /// 超时解析——非法 / 空值取默认；上限与工具执行层同口径。
        /// </summary>
        /// <param name="raw">原始文本（毫秒）</param>
        /// <returns>超时毫秒</returns>
        private static int ParseTimeout(string raw)
        {
            int value;
            if (raw.Length > 0 && int.TryParse(raw, out value) && value > 0)
            {
                return value > DefaultTimeoutMs ? DefaultTimeoutMs : value;
            }
            return DefaultTimeoutMs;
        }

        // [段10] 通用工具
        /// <summary>
        /// 杀进程树——不留僵尸。
        /// </summary>
        /// <param name="proc">目标进程</param>
        private static void KillTree(Process proc)
        {
            StdioServiceHost.KillTree(proc, "PyService");
        }


        /// <summary>
        /// 追加并截断——防止超大输出拖垮内存。
        /// </summary>
        /// <param name="buffer">目标缓冲</param>
        /// <param name="line">待追加行</param>
        /// <param name="cap">上限字符数</param>
        private static void AppendCapped(StringBuilder buffer, string line, int cap)
        {
            if (buffer.Length >= cap)
            {
                return;
            }
            buffer.Append(line);
            buffer.Append('\n');
        }

        /// <summary>
        /// 行数统计——空串 0；否则 \n 计数 + 1（与前端 split 同口径）。
        /// </summary>
        /// <param name="text">源文本</param>
        /// <returns>行数</returns>
        private static int CountLines(string text)
        {
            if (text.Length == 0)
            {
                return 0;
            }
            int count = 1;
            for (int i = 0; i < text.Length; i = i + 1)
            {
                if (text[i] == '\n')
                {
                    count = count + 1;
                }
            }
            return count;
        }

        /// <summary>
        /// 组装服务态回执。
        /// </summary>
        /// <param name="name">服务名</param>
        /// <param name="state">态（running / stopped / unavailable）</param>
        /// <param name="note">备注</param>
        /// <param name="alive">进程是否存活</param>
        /// <returns>回执 JSON</returns>
        private static string ServiceJson(string name, string state, string note, bool alive)
        {
            Dictionary<string, object> payload = new Dictionary<string, object>();
            payload["name"] = name;
            payload["state"] = state;
            payload["alive"] = alive;
            payload["note"] = note;
            return JsonUtil.Serialize(payload);
        }

        /// <summary>
        /// 组装服务态描述——未启动 / 存活 / 已退出（含连续失败计数）。
        /// </summary>
        /// <param name="name">服务名</param>
        /// <param name="server">服务持有对象（可空）</param>
        /// <returns>回执 JSON</returns>
        private static string DescribeStatus(string name, StdioServiceHost? server)
        {
            if (server == null)
            {
                return ServiceJson(name, "stopped", "服务未启动", false);
            }
            if (server.IsAlive())
            {
                return ServiceJson(name, "running", "", true);
            }
            return ServiceJson(name, "stopped", "进程已退出（连续失败 " + server.FailCount() + " 次）", false);
        }

        /// <summary>
        /// 运行期落盘目录——py 服务锚文件（py_&lt;服务名&gt;.pid）落点；空 = 不写锚（自愈面关闭）。
        /// </summary>
        private string _runtimeDir = "";
        /// <summary>
        /// 配置运行期落盘目录——锚文件落点（宿主启动链注入；design-ch4-pytools §5.2 清理第③层）。
        /// </summary>
        /// <param name="runtimeDir">数据根 runtime 目录（空 = 关闭自愈面）</param>
        public void Configure(string runtimeDir)
        {
            _runtimeDir = runtimeDir == null ? "" : runtimeDir;
        }
        /// <summary>
        /// 受控根内判据——可写根子树判定（带分隔符边界，防兄弟目录误伤）。
        /// </summary>
        /// <param name="fullPath">已解析的全路径</param>
        /// <returns>true = 在某个可写受控根内</returns>
        private bool IsUnderWritableRoot(string fullPath)
        {
            if (fullPath == null || fullPath.Length == 0)
            {
                return false;
            }
            for (int i = 0; i < _roots.Length; i = i + 1)
            {
                string root = _roots[i];
                if (fullPath.Length == root.Length && string.Equals(fullPath, root, StringComparison.OrdinalIgnoreCase))
                {
                    return true;
                }
                if (fullPath.Length > root.Length && fullPath.StartsWith(root, StringComparison.OrdinalIgnoreCase))
                {
                    char boundary = fullPath[root.Length];
                    if (boundary == Path.DirectorySeparatorChar || boundary == Path.AltDirectorySeparatorChar)
                    {
                        return true;
                    }
                }
            }
            return false;
        }
        /// <summary>
        /// 启动前自愈——清理上次宿主遗留的服务进程（锚文件记录；调用时机 = 宿主启动链、本实例尚未拉起服务）。
        /// </summary>
        /// <returns>清理条数</returns>
        private int SelfHealOrphans()
        {
            return ServiceAnchor.SelfHeal(_runtimeDir, "py", "PyService");
        }
        /// <summary>
        /// 写服务锚——拉起成功后落盘（自愈唯一依据；不主动删，正常停止时 RemoveAnchor 清）。
        /// </summary>
        /// <param name="serviceName">服务名</param>
        /// <param name="pid">进程号</param>
        private void WriteAnchor(string serviceName, int pid)
        {
            ServiceAnchor.Write(_runtimeDir, "py", serviceName, pid, "PyService");
        }
        /// <summary>
        /// 删服务锚——服务正常停止后清理（异常残留由 SelfHealOrphans 兜底）。
        /// </summary>
        /// <param name="serviceName">服务名</param>
        private void RemoveAnchor(string serviceName)
        {
            ServiceAnchor.Remove(_runtimeDir, "py", serviceName, "PyService");
        }
    }
}
