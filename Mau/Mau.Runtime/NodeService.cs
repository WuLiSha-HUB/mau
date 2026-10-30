using System;
using System.Collections.Generic;
using System.IO;

namespace Mau.Runtime
{
    /// <summary>
    /// Node 执行服务——node 常驻服务进程管理（design-ch4-node-services §二 / §三）。
    /// <para>
    /// 进程宿主机制归 <see cref="StdioServiceHost"/>（由 <see cref="PyService"/> 共用）；
    /// 本类只承载 node 语义：解释器读 <c>node.node_path</c>、启动参数前缀为空、配置段 <c>node.cfg</c>、
    /// 锚前缀 <c>node_</c>、错误码前缀 <c>NODE_</c>。
    /// </para>
    /// <para>
    /// 与 <see cref="PyService"/> 两侧独立——服务池 / 配置段 / 锚 / 降级码全部分离，任一侧缺失不影响另一侧。
    /// </para>
    /// </summary>
    public class NodeService : INodeService, IDisposable
    {
        /// <summary>
        /// 可写受控根——脚本路径必须落在其一之内
        /// </summary>
        private readonly string[] _roots;

        /// <summary>
        /// 可写根条目表——根寻址公共服务入参（只含 writable 根）
        /// </summary>
        private readonly WorkspaceConfig.RootEntry[] _entries;

        /// <summary>
        /// 根 Id → 规范路径（可写根）——支撑 &lt;根id&gt;:&lt;相对&gt; 寻址
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
        /// 默认超时毫秒（与工具执行层统一口径）
        /// </summary>
        private const int DefaultTimeoutMs = 120000;

        /// <summary>
        /// 连续拉起失败上限——达上限置「不可用」态（防崩溃风暴；需重启恢复）
        /// </summary>
        private int MaxLaunchFailures = 5;

        /// <summary>
        /// Job 对象句柄——强杀兜底（构造时建立；不可用 0）
        /// </summary>
        private IntPtr _jobHandle;

        /// <summary>
        /// 释放标志
        /// </summary>
        private bool _disposed;

        /// <summary>
        /// 运行期落盘目录——node 服务锚文件（node_&lt;服务名&gt;.pid）落点；空 = 不写锚（自愈面关闭）
        /// </summary>
        private string _runtimeDir = "";

        /// <summary>
        /// 建立服务——收可写受控根（脚本路径校验面），注册宿主退出清理钩子。
        /// </summary>
        /// <param name="roots">受控根表（只收 writable=true 的根）</param>
        public NodeService(WorkspaceConfig.RootEntry[] roots)
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
            AppDomain.CurrentDomain.ProcessExit += OnProcessExit;
            _jobHandle = ServiceJob.Ensure("NodeService");
        }

        // [段5] 服务自启（宿主启动链调用）
        /// <summary>
        /// 按配置拉起 follow=true 的服务——服务清单自配置扫描得出
        /// （键 node.service.&lt;name&gt;.script 存在即视为已声明服务）。
        /// </summary>
        /// <returns>结果 JSON（started / skipped / errors 数组）或 ERR| 前缀错误文本</returns>
        public string Autostart()
        {
            SelfHealOrphans();
            ConfigStore? cfg;
            DataBox.TryResolve<ConfigStore>(out cfg);
            if (cfg == null)
            {
                return "ERR|NODE_NOT_CONFIGURED|配置存储未就绪——无法读取 node 服务声明";
            }

            List<string> names = ScanServiceNames(cfg);

            List<string> started = new List<string>();
            List<string> skipped = new List<string>();
            List<string> errors = new List<string>();
            for (int i = 0; i < names.Count; i = i + 1)
            {
                string name = names[i];
                string follow = cfg.Get("node.service." + name + ".follow", "false");
                if (!string.Equals(follow, "true", StringComparison.OrdinalIgnoreCase))
                {
                    skipped.Add(name);
                    continue;
                }
                string result = Service(JsonUtil.Object(("name", name), ("action", "start")));
                if (result.StartsWith("ERR|", StringComparison.Ordinal))
                {
                    errors.Add(name);
                    LogStore.Add("NodeService", 2, "服务自启失败（" + name + "）: " + result, "SYS");
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
        /// 扫描服务清单——键 <c>node.service.&lt;name&gt;.script</c> 存在即视为已声明服务（Autostart 与 Enumerate 共用单一实现）。
        /// </summary>
        /// <param name="cfg">配置存储</param>
        /// <returns>服务名列表（去重、保序）</returns>
        private static List<string> ScanServiceNames(ConfigStore cfg)
        {
            const string prefix = "node.service.";
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
                return "ERR|NODE_BAD_ARGS|缺少参数 name";
            }
            if (action.Length == 0 || action == "§PARSE_FAIL§")
            {
                return "ERR|NODE_BAD_ARGS|缺少参数 action（start|stop|status）";
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
                    return "ERR|NODE_BAD_ARGS|action 非法值: " + action + "（start|stop|status）";
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
                string nodeError;
                string nodeExe = ResolveNode(name, out nodeError);
                if (nodeExe.Length == 0)
                {
                    return nodeError;
                }
                int timeoutMs = ParseServiceTimeout(name);

                string servArgs = ResolveServiceArgs(name);
                string servHome = ResolveServiceHome(name);
                StdioServiceHost server = new StdioServiceHost(name, nodeExe, script, Path.GetDirectoryName(script) ?? "", timeoutMs, servArgs, servHome,
                    "", "NODE", "NodeService", MaxLaunchFailures);
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
                return "ERR|NODE_BAD_ARGS|缺少参数 name";
            }
            if (payload.Length == 0 || payload == "§PARSE_FAIL§")
            {
                return "ERR|NODE_BAD_ARGS|缺少参数 payload（请求体 JSON 文本）";
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
                    string nodeError;
                    string nodeExe = ResolveNode(name, out nodeError);
                    if (nodeExe.Length == 0)
                    {
                        return nodeError;
                    }
                    string servArgs = ResolveServiceArgs(name);
                    string servHome = ResolveServiceHome(name);
                    server = new StdioServiceHost(name, nodeExe, script, Path.GetDirectoryName(script) ?? "", ParseServiceTimeout(name), servArgs, servHome,
                        "", "NODE", "NodeService", MaxLaunchFailures);
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

        // [段7] 服务枚举
        /// <summary>
        /// 服务枚举——全部已声明服务（配置扫描；含未启动项）+ 运行态 + 进程内存占用。
        /// </summary>
        /// <returns>结果 JSON（services 数组）或 ERR| 前缀错误文本</returns>
        public string Enumerate()
        {
            ConfigStore? cfg;
            DataBox.TryResolve<ConfigStore>(out cfg);
            if (cfg == null)
            {
                return "ERR|NODE_NOT_CONFIGURED|配置存储未就绪——无法读取 node 服务声明";
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

        // [段8] 配置解析
        /// <summary>
        /// 解析解释器路径——服务级覆盖优先，回退全局；未配置 / 路径不存在即明示 ERR。
        /// </summary>
        /// <param name="serviceName">服务名（空 = 全局）</param>
        /// <param name="error">错误文本（空=成功）</param>
        /// <returns>node.exe 全路径（失败返回空串）</returns>
        private static string ResolveNode(string serviceName, out string error)
        {
            error = "";
            ConfigStore? cfg;
            DataBox.TryResolve<ConfigStore>(out cfg);
            string path = "";
            if (cfg != null && serviceName.Length > 0)
            {
                path = cfg.Get("node.service." + serviceName + ".node", "");
            }
            if (path.Length == 0 && cfg != null)
            {
                path = cfg.Get("node.node_path", "");
            }
            if (path.Length == 0)
            {
                error = "ERR|NODE_NOT_CONFIGURED|Node 解释器未配置——请在配置面填写 node.node_path（node.exe 全路径；获取方式 where node）";
                return "";
            }
            if (!File.Exists(path))
            {
                error = "ERR|NODE_NODE_NOT_FOUND|配置的解释器路径不存在：" + path + "——请更新 node.node_path";
                return "";
            }
            return path;
        }

        /// <summary>解析服务入口脚本——读配置 node.service.&lt;name&gt;.script（受控根寻址：mau:tools/... 或全路径）；不存在即明示 ERR（失败可见，不静默）。</summary>
        /// <param name="serviceName">服务名</param>
        /// <param name="error">错误文本（空=成功）</param>
        /// <returns>脚本全路径（失败返回空串）</returns>
        private string ResolveServiceScript(string serviceName, out string error)
        {
            error = "";
            ConfigStore? cfg;
            DataBox.TryResolve<ConfigStore>(out cfg);
            string script = cfg == null ? "" : cfg.Get("node.service." + serviceName + ".script", "");
            if (script.Length == 0)
            {
                error = "ERR|NODE_SERVICE_NOT_CONFIGURED|服务未配置：" + serviceName + "——请在配置面填写 node.service." + serviceName + ".script";
                return "";
            }
            RootResolveResult rr = RootPathResolver.Expand(script, _entries, Environment.CurrentDirectory);
            if (!rr.Ok)
            {
                error = "ERR|NODE_BAD_PATH|服务脚本路径非法: " + rr.Message;
                return "";
            }
            string full = rr.FullPath;
            if (!IsUnderWritableRoot(full))
            {
                error = "ERR|NODE_SCRIPT_OUTSIDE_ROOT|服务脚本超出受控根：" + full + "——只允许可写受控根内的脚本";
                return "";
            }
            if (!File.Exists(full))
            {
                error = "ERR|NODE_SCRIPT_NOT_FOUND|服务脚本不存在：" + full;
                return "";
            }
            return full;
        }

        /// <summary>
        /// 解析服务启动参数——node.service 服务名对应的 args（命令行附加串；空=无）。
        /// </summary>
        /// <param name="serviceName">服务名</param>
        /// <returns>附加参数串（空=无）</returns>
        private static string ResolveServiceArgs(string serviceName)
        {
            ConfigStore? cfg;
            DataBox.TryResolve<ConfigStore>(out cfg);
            return cfg == null ? "" : cfg.Get("node.service." + serviceName + ".args", "");
        }

        /// <summary>
        /// 解析服务环境目录——node.service.&lt;name&gt;.home（外部环境根）。
        /// 起服务时以 --home 传入（服务脚本自行消费）；空=不传（不推导、不回落）。
        /// </summary>
        /// <param name="serviceName">服务名</param>
        /// <returns>环境目录（空=不传）</returns>
        private static string ResolveServiceHome(string serviceName)
        {
            ConfigStore? cfg;
            DataBox.TryResolve<ConfigStore>(out cfg);
            return cfg == null ? "" : cfg.Get("node.service." + serviceName + ".home", "");
        }

        /// <summary>
        /// 解析服务单次调用超时——node.service.&lt;name&gt;.timeout_ms。
        /// </summary>
        /// <param name="serviceName">服务名</param>
        /// <returns>超时毫秒</returns>
        private static int ParseServiceTimeout(string serviceName)
        {
            ConfigStore? cfg;
            DataBox.TryResolve<ConfigStore>(out cfg);
            string raw = cfg == null ? "" : cfg.Get("node.service." + serviceName + ".timeout_ms", "");
            int value;
            if (raw.Length > 0 && int.TryParse(raw, out value) && value > 0)
            {
                return value > DefaultTimeoutMs ? DefaultTimeoutMs : value;
            }
            return DefaultTimeoutMs;
        }

        // [段9] 回执组装
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
        /// 配置运行期落盘目录——锚文件落点（宿主启动链注入；design-ch4-node-services §八 清理第③层）。
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
            return ServiceAnchor.SelfHeal(_runtimeDir, "node", "NodeService");
        }

        /// <summary>
        /// 写服务锚——拉起成功后落盘（自愈唯一依据；不主动删，正常停止时 RemoveAnchor 清）。
        /// </summary>
        /// <param name="serviceName">服务名</param>
        /// <param name="pid">进程号</param>
        private void WriteAnchor(string serviceName, int pid)
        {
            ServiceAnchor.Write(_runtimeDir, "node", serviceName, pid, "NodeService");
        }

        /// <summary>
        /// 删服务锚——服务正常停止后清理（异常残留由 SelfHealOrphans 兜底）。
        /// </summary>
        /// <param name="serviceName">服务名</param>
        private void RemoveAnchor(string serviceName)
        {
            ServiceAnchor.Remove(_runtimeDir, "node", serviceName, "NodeService");
        }

        // [段10] 释放
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
                        LogStore.Add("NodeService", 2, "服务停止异常（" + pair.Key + "）: " + ex.Message, "SYS");
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
                LogStore.Add("NodeService", 3, "退出清理异常: " + ex.Message, "SYS");
            }
        }
    }
}
