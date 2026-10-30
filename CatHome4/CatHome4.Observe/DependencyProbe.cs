using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Mau.Runtime;
using CH4;

namespace CatHome4.Observe
{
    /// <summary>
    /// 依赖探测——外部依赖的路径 / 版本 / 可用性复验（design-ch4-pytools.md §七 · A253）。
    /// 决策：D1 后端统一快照为唯一真相源 · D2 分档刷新（动态 / 静态 / 一次性）· D3 复验深度三档。
    /// 线程契约：输入采集在主线程（DataBox / 配置读面）；IO 探测在后台 Task（不阻塞主循环）；结果经锁发布，主线程只读快照。
    /// 消费侧驱动：无消费方（不构建快照）则不探测——探测启动权在 EnsureFresh 调用者。
    /// </summary>
    internal static class DependencyProbe
    {
        // [段1] 状态词与分档常量
        /// <summary>状态——可用</summary>
        internal const string StateOk = "ok";
        /// <summary>状态——未配置（本就没配——属正常态，不报红）</summary>
        internal const string StateUnconfigured = "unconfigured";
        /// <summary>状态——缺失（配了但路径 / 命令找不到）</summary>
        internal const string StateMissing = "missing";
        /// <summary>状态——不可用（存在但跑不起来）</summary>
        internal const string StateUnusable = "unusable";
        /// <summary>级别——运行必需</summary>
        internal const string LevelRequired = "required";
        /// <summary>级别——可选功能</summary>
        internal const string LevelOptional = "optional";
        /// <summary>级别——开发辅助</summary>
        internal const string LevelDev = "dev";
        /// <summary>分档——静态面（路径 / 配置 / PE 版本）</summary>
        private const int TtlStaticMs = 30000;
        /// <summary>分档——一次性（外部命令版本——手动刷新才重探）</summary>
        private const int TtlOnceMs = 2147483647;
        /// <summary>命令探测超时毫秒（超时杀进程树）</summary>
        private const int CommandTimeoutMs = 3000;

        // [段2] 数据结构
        /// <summary>
        /// 单条探测结果——快照 deps 段条目（level / name / state / detail / probedAt）。
        /// </summary>
        internal sealed class ProbeResult
        {
            /// <summary>级别——required / optional / dev</summary>
            public string Level = LevelRequired;

            /// <summary>显示名</summary>
            public string Name = "";

            /// <summary>状态——ok / unconfigured / missing / unusable</summary>
            public string State = StateUnconfigured;

            /// <summary>细节（版本 / 路径 / 缺失清单）</summary>
            public string Detail = "";

            /// <summary>探测时刻（Unix 毫秒；0 = 未探测）</summary>
            public long ProbedAtMs;

            /// <summary>本项有效期（毫秒）</summary>
            public int TtlMs = TtlStaticMs;
        }

        /// <summary>
        /// 探测任务——主线程采集的纯数据输入（后台线程只做 IO，不触碰 DataBox / 配置实例）。
        /// </summary>
        private sealed class ProbeJob
        {
            /// <summary>级别</summary>
            public string Level = "";

            /// <summary>显示名</summary>
            public string Name = "";

            /// <summary>探测类型——paths / command / filever</summary>
            public string Kind = "";

            /// <summary>目标路径 / 命令</summary>
            public string Command = "";

            /// <summary>命令参数</summary>
            public string Args = "";

            /// <summary>待检路径集（paths / filever）</summary>
            public List<string> Paths = new List<string>();

            /// <summary>有效期（毫秒）</summary>
            public int TtlMs = TtlStaticMs;
        }

        // [段3] 状态（锁保护）
        /// <summary>结果表——显示名 → 条目</summary>
        private static readonly Dictionary<string, ProbeResult> _results = new Dictionary<string, ProbeResult>(StringComparer.Ordinal);

        /// <summary>状态锁——读写互斥</summary>
        private static readonly object _gate = new object();

        /// <summary>后台批次在跑标记（单飞——同刻只跑一批）</summary>
        private static bool _running;

        // [段4] 公共入口
        /// <summary>
        /// 确保探测新鲜——快照构建时调用（主线程）：采集配置类即时结果 + 筛出过期 IO 项交后台。
        /// 无过期项则立即返回；后台已在跑则本轮跳过（下一轮快照拿到新值）。
        /// </summary>
        internal static void EnsureFresh()
        {
            long now = NowMs();
            // [段1] 主线程采集——配置类直接结果 + IO 类待探任务
            List<ProbeResult> direct = new List<ProbeResult>();
            List<ProbeJob> jobs = new List<ProbeJob>();
            Collect(direct, jobs, now);
            // [段2] 配置类结果即时落表（廉价——每次快照刷新，保持实时）
            lock (_gate)
            {
                for (int i = 0; i < direct.Count; i++)
                {
                    _results[direct[i].Name] = direct[i];
                }
            }
            // [段3] IO 类——筛过期项
            List<ProbeJob> stale = new List<ProbeJob>();
            for (int i = 0; i < jobs.Count; i++)
            {
                bool need = false;
                lock (_gate)
                {
                    ProbeResult cached;
                    if (!_results.TryGetValue(jobs[i].Name, out cached))
                    {
                        need = true;
                    }
                    else if (now - cached.ProbedAtMs > jobs[i].TtlMs)
                    {
                        need = true;
                    }
                }
                if (need)
                {
                    stale.Add(jobs[i]);
                }
            }
            if (stale.Count == 0)
            {
                return;
            }
            // [段4] 单飞——同刻只跑一批
            bool start = false;
            lock (_gate)
            {
                if (!_running)
                {
                    _running = true;
                    start = true;
                }
            }
            if (!start)
            {
                return;
            }
            Task.Run(delegate () { RunProbeBatch(stale); });
        }

        /// <summary>
        /// 结果快照——按级别（required → optional → dev）+ 名称排序的只读拷贝（主线程消费）。
        /// </summary>
        /// <returns>条目数组</returns>
        internal static ProbeResult[] Snapshot()
        {
            lock (_gate)
            {
                List<ProbeResult> list = new List<ProbeResult>();
                foreach (KeyValuePair<string, ProbeResult> kv in _results)
                {
                    list.Add(kv.Value);
                }
                list.Sort(CompareResults);
                return list.ToArray();
            }
        }

        /// <summary>
        /// 手动刷新——重置全部条目的探测时刻（下一轮 EnsureFresh 全量重探；含一次性档）。
        /// </summary>
        internal static void Refresh()
        {
            lock (_gate)
            {
                foreach (KeyValuePair<string, ProbeResult> kv in _results)
                {
                    kv.Value.ProbedAtMs = 0;
                }
            }
        }

        // [段5] 采集（主线程）
        /// <summary>
        /// 采集探测输入——配置类直接定案（主线程读配置面），IO 类产出任务交后台。
        /// </summary>
        /// <param name="direct">配置类直接结果（输出）</param>
        /// <param name="jobs">IO 待探任务（输出）</param>
        /// <param name="now">当前时刻（Unix 毫秒）</param>
        private static void Collect(List<ProbeResult> direct, List<ProbeJob> jobs, long now)
        {
            ConfigStore cfg = null;
            DataBox.TryResolve<ConfigStore>(out cfg);
            WorkspaceConfig ws = null;
            DataBox.TryResolve<WorkspaceConfig>(out ws);
            // [段1] 受控根表——逐根目录存在性（运行必需；工作区不可用即全局失效）
            if (ws == null)
            {
                direct.Add(MakeResultAt(LevelRequired, "受控根表", StateUnusable, "工作区配置不可用（未注入）", TtlStaticMs, now));
            }
            else if (ws.Roots.Length == 0)
            {
                direct.Add(MakeResultAt(LevelRequired, "受控根表", StateUnusable, "受控根表为空", TtlStaticMs, now));
            }
            else
            {
                ProbeJob job = new ProbeJob();
                job.Level = LevelRequired;
                job.Name = "受控根表";
                job.Kind = "paths";
                job.TtlMs = TtlStaticMs;
                for (int i = 0; i < ws.Roots.Length; i++)
                {
                    job.Paths.Add(ws.Roots[i].Path);
                }
                jobs.Add(job);
            }
            // [段2] 解释器与内核——路径配置 + 真跑一次读版本（运行必需）
            AddPathCommandProbe(cfg, direct, jobs, "ps.pwsh_path", LevelRequired, "PowerShell 7", "--version", TtlStaticMs, now);
            AddPathCommandProbe(cfg, direct, jobs, "py.python_path", LevelRequired, "Python", "--version", TtlStaticMs, now);
            AddPathCommandProbe(cfg, direct, jobs, "node.node_path", LevelRequired, "Node.js", "--version", TtlStaticMs, now);
            AddPathFileVersionProbe(cfg, direct, jobs, "browser.kernel_path", LevelRequired, "浏览器内核", TtlStaticMs, now);
            // [段3] Kokoro 服务——配置完整性（不探活：探活会拉起 1.2 GB 进程，副作用不可接受）
            CollectKokoro(cfg, ws, direct, jobs, now);
            // [段3b] node 服务——配置完整性（不探活：拉起常驻进程副作用不可接受，存活态归资源页签）
            CollectNodeService(cfg, ws, direct, jobs, now);
            // [段4] LLM 端点池——**已移出 deps 段**（A253 扩展）：改为管理面板独立区块 + 手动探活
            //       （LlmEndpointProbe.ProbeAsync——常态只做配置完整性，真探活按需触发，见 design-ch4-pytools §七 D3）
            // [段5] 可选功能——配置完整性（未配置属正常态，不报红）
            AddConfigPresence(cfg, direct, "search.api_config_id", LevelOptional, "搜索 API", now);
            AddConfigPresence(cfg, direct, "vision.api_config_id", LevelOptional, "视觉 API", now);
            CollectQqBot(direct, now);
            // [段6] 开发辅助——外部命令版本（一次性档）+ 自举链文件
            AddCommandProbe(direct, jobs, "dotnet", "dotnet --version", LevelDev, "dotnet SDK", "--version", TtlOnceMs, now);
            AddCommandProbe(direct, jobs, "git", "git --version", LevelDev, "Git", "--version", TtlOnceMs, now);
            CollectRepoArtifacts(ws, direct, jobs, now);
        }

        /// <summary>
        /// 追加「路径配置 + 真跑一次读版本」探测——空值即未配置（不静默回落探测路径）。
        /// </summary>
        /// <param name="cfg">配置读面（可 null）</param>
        /// <param name="direct">配置类直接结果</param>
        /// <param name="jobs">IO 待探任务</param>
        /// <param name="key">配置键</param>
        /// <param name="level">级别</param>
        /// <param name="name">显示名</param>
        /// <param name="args">版本参数</param>
        /// <param name="ttlMs">有效期</param>
        /// <param name="now">当前时刻</param>
        private static void AddPathCommandProbe(ConfigStore cfg, List<ProbeResult> direct, List<ProbeJob> jobs, string key, string level, string name, string args, int ttlMs, long now)
        {
            string path = "";
            if (cfg != null)
            {
                path = cfg.Get(key, "");
            }
            if (path.Length == 0)
            {
                direct.Add(MakeResultAt(level, name, StateUnconfigured, "未配置（" + key + " 空）", ttlMs, now));
                return;
            }
            ProbeJob job = new ProbeJob();
            job.Level = level;
            job.Name = name;
            job.Kind = "command";
            job.Command = path;
            job.Args = args;
            job.TtlMs = ttlMs;
            jobs.Add(job);
        }

        /// <summary>
        /// 追加「外部命令」探测（按 PATH 解析——不校验绝对路径）。
        /// </summary>
        /// <param name="direct">配置类直接结果</param>
        /// <param name="jobs">IO 待探任务</param>
        /// <param name="exe">可执行名</param>
        /// <param name="tag">显示用命令串（Detail 回显）</param>
        /// <param name="level">级别</param>
        /// <param name="name">显示名</param>
        /// <param name="args">版本参数</param>
        /// <param name="ttlMs">有效期</param>
        /// <param name="now">当前时刻</param>
        private static void AddCommandProbe(List<ProbeResult> direct, List<ProbeJob> jobs, string exe, string tag, string level, string name, string args, int ttlMs, long now)
        {
            ProbeJob job = new ProbeJob();
            job.Level = level;
            job.Name = name;
            job.Kind = "command";
            job.Command = exe;
            job.Args = args;
            job.TtlMs = ttlMs;
            jobs.Add(job);
        }

        /// <summary>
        /// 追加「配置项非空即已配置」探测——可选功能面。
        /// </summary>
        /// <param name="cfg">配置读面（可 null）</param>
        /// <param name="direct">配置类直接结果</param>
        /// <param name="key">配置键</param>
        /// <param name="level">级别</param>
        /// <param name="name">显示名</param>
        /// <param name="now">当前时刻</param>
        private static void AddConfigPresence(ConfigStore cfg, List<ProbeResult> direct, string key, string level, string name, long now)
        {
            string value = "";
            if (cfg != null)
            {
                value = cfg.Get(key, "");
            }
            if (value.Length == 0)
            {
                direct.Add(MakeResultAt(level, name, StateUnconfigured, "未配置（" + key + " 空）", TtlStaticMs, now));
                return;
            }
            direct.Add(MakeResultAt(level, name, StateOk, "已配置", TtlStaticMs, now));
        }

        /// <summary>
        /// Kokoro 服务探测——解释器（服务级覆盖优先）+ 入口脚本（受控根寻址展开）双双存在即就绪。
        /// 不探活：探活会按需拉起服务进程（1.2 GB / 2.6 s 冷启），副作用不可接受。
        /// </summary>
        /// <param name="cfg">配置读面（可 null）</param>
        /// <param name="ws">工作区配置（受控根寻址基准；可 null）</param>
        /// <param name="direct">配置类直接结果</param>
        /// <param name="jobs">IO 待探任务</param>
        /// <param name="now">当前时刻</param>
        private static void CollectKokoro(ConfigStore cfg, WorkspaceConfig ws, List<ProbeResult> direct, List<ProbeJob> jobs, long now)
        {
            string name = "Kokoro TTS";
            string script = "";
            string python = "";
            if (cfg != null)
            {
                script = cfg.Get("py.service.kokoro.script", "");
                python = cfg.Get("py.service.kokoro.python", "");
                if (python.Length == 0)
                {
                    python = cfg.Get("py.python_path", "");
                }
            }
            if (script.Length == 0)
            {
                direct.Add(MakeResultAt(LevelRequired, name, StateUnconfigured, "未配置（py.service.kokoro.script 空）", TtlStaticMs, now));
                return;
            }
            if (ws == null)
            {
                direct.Add(MakeResultAt(LevelRequired, name, StateUnusable, "受控根表不可用——脚本寻址无法解析", TtlStaticMs, now));
                return;
            }
            RootResolveResult rr = RootPathResolver.Expand(script, ws.Roots, Environment.CurrentDirectory);
            if (!rr.Ok)
            {
                direct.Add(MakeResultAt(LevelRequired, name, StateUnusable, "脚本寻址非法：" + rr.ErrorCode, TtlStaticMs, now));
                return;
            }
            if (python.Length == 0)
            {
                direct.Add(MakeResultAt(LevelRequired, name, StateUnconfigured, "未配置（解释器空——服务与 py.python_path 均未设）", TtlStaticMs, now));
                return;
            }
            ProbeJob job = new ProbeJob();
            job.Level = LevelRequired;
            job.Name = name;
            job.Kind = "paths";
            job.TtlMs = TtlStaticMs;
            job.Paths.Add(python);
            job.Paths.Add(rr.FullPath);
            jobs.Add(job);
        }

        /// <summary>
        /// node 服务探测——逐实例取入口脚本与解释器（服务型档；不探活——拉起常驻进程副作用不可接受）。
        /// </summary>
        /// <param name="cfg">配置读面（可 null）</param>
        /// <param name="ws">工作区配置（受控根寻址基准；可 null）</param>
        /// <param name="direct">配置类直接结果</param>
        /// <param name="jobs">IO 待探任务</param>
        /// <param name="now">当前时刻</param>
        private static void CollectNodeService(ConfigStore cfg, WorkspaceConfig ws, List<ProbeResult> direct, List<ProbeJob> jobs, long now)
        {
            CollectStdioService(cfg, ws, direct, jobs, now, "ts", "ts", "Node 服务（ts）");
        }

        /// <summary>
        /// stdio 服务实例探测——入口脚本 + 解释器路径存在即可用；存活态归资源页签（design-ch4-node-services §五）。
        /// </summary>
        /// <param name="cfg">配置读面（可 null）</param>
        /// <param name="ws">工作区配置（受控根寻址基准；可 null）</param>
        /// <param name="direct">配置类直接结果</param>
        /// <param name="jobs">IO 待探任务</param>
        /// <param name="now">当前时刻</param>
        /// <param name="serviceName">服务名（配置段 node.service.&lt;名&gt;.）</param>
        /// <param name="overrideKey">解释器覆盖键名（node）</param>
        /// <param name="displayName">页签显示名</param>
        private static void CollectStdioService(ConfigStore cfg, WorkspaceConfig ws, List<ProbeResult> direct, List<ProbeJob> jobs, long now,
            string serviceName, string overrideKey, string displayName)
        {
            string prefix = "node.service." + serviceName + ".";
            string script = "";
            string exe = "";
            if (cfg != null)
            {
                script = cfg.Get(prefix + "script", "");
                exe = cfg.Get(prefix + overrideKey, "");
                if (exe.Length == 0)
                {
                    exe = cfg.Get("node.node_path", "");
                }
            }
            if (script.Length == 0)
            {
                direct.Add(MakeResultAt(LevelRequired, displayName, StateUnconfigured, "未配置（" + prefix + "script 空）", TtlStaticMs, now));
                return;
            }
            if (ws == null)
            {
                direct.Add(MakeResultAt(LevelRequired, displayName, StateUnusable, "受控根表不可用——脚本寻址无法解析", TtlStaticMs, now));
                return;
            }
            RootResolveResult rr = RootPathResolver.Expand(script, ws.Roots, Environment.CurrentDirectory);
            if (!rr.Ok)
            {
                direct.Add(MakeResultAt(LevelRequired, displayName, StateUnusable, "脚本寻址非法：" + rr.ErrorCode, TtlStaticMs, now));
                return;
            }
            if (exe.Length == 0)
            {
                direct.Add(MakeResultAt(LevelRequired, displayName, StateUnconfigured, "未配置（解释器空——服务覆盖与 node.node_path 均未设）", TtlStaticMs, now));
                return;
            }
            ProbeJob job = new ProbeJob();
            job.Level = LevelRequired;
            job.Name = displayName;
            job.Kind = "paths";
            job.TtlMs = TtlStaticMs;
            job.Paths.Add(exe);
            job.Paths.Add(rr.FullPath);
            jobs.Add(job);
        }

        /// <summary>
        /// QQ Bot 池探测——池非空即已配置（可选功能；未配置属正常态）。
        /// </summary>
        /// <param name="direct">配置类直接结果</param>
        /// <param name="now">当前时刻</param>
        private static void CollectQqBot(List<ProbeResult> direct, long now)
        {
            string name = "QQBot";
            CH_QqBotConfigStore store = null;
            DataBox.TryResolve<CH_QqBotConfigStore>(out store);
            if (store == null)
            {
                direct.Add(MakeResultAt(LevelOptional, name, StateUnconfigured, "QQ Bot 池存储不可用（未注入）", TtlStaticMs, now));
                return;
            }
            CH_QqBotConfig[] all = store.GetAll();
            if (all.Length == 0)
            {
                direct.Add(MakeResultAt(LevelOptional, name, StateUnconfigured, "未配置（池为空）", TtlStaticMs, now));
                return;
            }
            direct.Add(MakeResultAt(LevelOptional, name, StateOk, "已配置（池 " + all.Length.ToString() + " 项）", TtlStaticMs, now));
        }

        /// <summary>
        /// 自举链产物探测——按受控根 `mau`（仓库根）定位 SetUp.exe / Mau.exe / 前端测试依赖。
        /// 根不存在则整组跳过（不报红——部署区形态下这些面本就不在）。
        /// </summary>
        /// <param name="ws">工作区配置（可 null）</param>
        /// <param name="direct">配置类直接结果</param>
        /// <param name="jobs">IO 待探任务</param>
        /// <param name="now">当前时刻</param>
        private static void CollectRepoArtifacts(WorkspaceConfig ws, List<ProbeResult> direct, List<ProbeJob> jobs, long now)
        {
            string repoRoot = "";
            if (ws != null)
            {
                for (int i = 0; i < ws.Roots.Length; i++)
                {
                    if (string.Equals(ws.Roots[i].Id, "mau", StringComparison.OrdinalIgnoreCase))
                    {
                        repoRoot = ws.Roots[i].Path;
                        break;
                    }
                }
            }
            if (repoRoot.Length == 0)
            {
                direct.Add(MakeResultAt(LevelDev, "自举链产物", StateUnconfigured, "未定位仓库根（无 `mau` 受控根）", TtlStaticMs, now));
                return;
            }
            ProbeJob setUpJob = new ProbeJob();
            setUpJob.Level = LevelDev;
            setUpJob.Name = "SetUp.exe";
            setUpJob.Kind = "filever";
            setUpJob.TtlMs = TtlStaticMs;
            setUpJob.Paths.Add(Path.Combine(repoRoot, "SetUp.exe"));
            jobs.Add(setUpJob);
            ProbeJob cliJob = new ProbeJob();
            cliJob.Level = LevelDev;
            cliJob.Name = "Mau.exe";
            cliJob.Kind = "filever";
            cliJob.TtlMs = TtlStaticMs;
            cliJob.Paths.Add(Path.Combine(repoRoot, "Mau-public", "Mau.exe"));
            jobs.Add(cliJob);
            ProbeJob nodeJob = new ProbeJob();
            nodeJob.Level = LevelDev;
            nodeJob.Name = "前端测试依赖";
            nodeJob.Kind = "paths";
            nodeJob.TtlMs = TtlStaticMs;
            nodeJob.Paths.Add(Path.Combine(repoRoot, "CatHome4", "html", "tests", "node_modules"));
            jobs.Add(nodeJob);
        }

        // [段6] 后台探测（IO）
        /// <summary>
        /// 跑一批探测——逐任务执行 IO，结果整批回写（锁内合并）。
        /// </summary>
        /// <param name="jobs">待探任务</param>
        private static void RunProbeBatch(List<ProbeJob> jobs)
        {
            try
            {
                List<ProbeResult> results = new List<ProbeResult>();
                for (int i = 0; i < jobs.Count; i++)
                {
                    results.Add(RunProbe(jobs[i]));
                }
                lock (_gate)
                {
                    for (int i = 0; i < results.Count; i++)
                    {
                        _results[results[i].Name] = results[i];
                    }
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "依赖探测批次异常：" + ex.Message, "DEPS");
            }
            finally
            {
                lock (_gate)
                {
                    _running = false;
                }
            }
        }

        /// <summary>
        /// 执行单条探测——按类型分派。
        /// </summary>
        /// <param name="job">探测任务</param>
        /// <returns>探测结果</returns>
        private static ProbeResult RunProbe(ProbeJob job)
        {
            if (string.Equals(job.Kind, "paths", StringComparison.Ordinal))
            {
                return RunPathsProbe(job);
            }
            if (string.Equals(job.Kind, "command", StringComparison.Ordinal))
            {
                return RunCommandProbe(job);
            }
            if (string.Equals(job.Kind, "filever", StringComparison.Ordinal))
            {
                return RunFileVersionProbe(job);
            }
            return MakeResult(job.Level, job.Name, StateUnusable, "未知探测类型：" + job.Kind, job.TtlMs);
        }

        /// <summary>
        /// 路径集探测——全部存在即可用；缺失项逐个列出（缺失面可行动）。
        /// </summary>
        /// <param name="job">探测任务</param>
        /// <returns>探测结果</returns>
        private static ProbeResult RunPathsProbe(ProbeJob job)
        {
            List<string> missing = new List<string>();
            for (int i = 0; i < job.Paths.Count; i++)
            {
                string path = job.Paths[i];
                bool found = false;
                if (path != null && path.Length > 0)
                {
                    found = File.Exists(path) || Directory.Exists(path);
                }
                if (!found)
                {
                    if (path == null)
                    {
                        missing.Add("(空)");
                    }
                    else
                    {
                        missing.Add(path);
                    }
                }
            }
            if (missing.Count == 0)
            {
                return MakeResult(job.Level, job.Name, StateOk, "路径就绪（" + job.Paths.Count.ToString() + " 项）", job.TtlMs);
            }
            return MakeResult(job.Level, job.Name, StateMissing, "缺失：" + JoinList(missing), job.TtlMs);
        }

        /// <summary>
        /// 命令探测——文件存在（绝对路径）或按 PATH 解析，跑一次读首行版本；超时杀进程树。
        /// </summary>
        /// <param name="job">探测任务</param>
        /// <returns>探测结果</returns>
        private static ProbeResult RunCommandProbe(ProbeJob job)
        {
            bool absolute = job.Command.IndexOf('\\') >= 0 || job.Command.IndexOf('/') >= 0;
            if (absolute && !File.Exists(job.Command))
            {
                return MakeResult(job.Level, job.Name, StateMissing, "文件不存在：" + job.Command, job.TtlMs);
            }
            string output = "";
            bool ran = TryRunCommand(job.Command, job.Args, out output);
            if (!ran)
            {
                return MakeResult(job.Level, job.Name, StateUnusable, "执行失败：" + job.Command, job.TtlMs);
            }
            string version = FirstLine(output);
            if (version.Length == 0)
            {
                version = "已就绪（无版本输出）";
            }
            else if (!IsTextClean(version))
            {
                LogStore.Add("CatHome4", 2, "依赖探测版本输出不可解析（编码异常）：" + job.Command, "DEPS");
                version = "已就绪（版本输出不可解析）";
            }
            return MakeResult(job.Level, job.Name, StateOk, version, job.TtlMs);
        }

        /// <summary>
        /// PE 版本探测——文件存在 + FileVersionInfo 读取（缺失版本回落「文件就绪」）。
        /// </summary>
        /// <param name="job">探测任务</param>
        /// <returns>探测结果</returns>
        private static ProbeResult RunFileVersionProbe(ProbeJob job)
        {
            if (job.Paths.Count == 0)
            {
                return MakeResult(job.Level, job.Name, StateUnusable, "无目标路径", job.TtlMs);
            }
            string path = job.Paths[0];
            if (!File.Exists(path))
            {
                return MakeResult(job.Level, job.Name, StateMissing, "文件不存在：" + path, job.TtlMs);
            }
            try
            {
                FileVersionInfo info = FileVersionInfo.GetVersionInfo(path);
                string version = info.FileVersion;
                if (version == null || version.Length == 0)
                {
                    version = "文件就绪（无版本信息）";
                }
                return MakeResult(job.Level, job.Name, StateOk, version, job.TtlMs);
            }
            catch (Exception ex)
            {
                return MakeResult(job.Level, job.Name, StateUnusable, "版本读取失败：" + ex.Message, job.TtlMs);
            }
        }

        /// <summary>
        /// 起进程跑一次命令——异步读流防死锁 + 超时杀进程树（超时不吞：报失败）。
        /// </summary>
        /// <param name="exe">可执行路径或命令名</param>
        /// <param name="args">参数</param>
        /// <param name="output">标准输出（成功时）</param>
        /// <returns>是否正常退出</returns>
        private static bool TryRunCommand(string exe, string args, out string output)
        {
            output = "";
            Process process = null;
            try
            {
                ProcessStartInfo psi = new ProcessStartInfo(exe);
                psi.Arguments = args;
                psi.UseShellExecute = false;
                psi.CreateNoWindow = true;
                psi.RedirectStandardOutput = true;
                psi.RedirectStandardError = true;
                psi.StandardOutputEncoding = new UTF8Encoding(false);
                psi.StandardErrorEncoding = new UTF8Encoding(false);
                process = Process.Start(psi);
                if (process == null)
                {
                    return false;
                }
                Task<string> outTask = process.StandardOutput.ReadToEndAsync();
                Task<string> errTask = process.StandardError.ReadToEndAsync();
                bool exited = process.WaitForExit(CommandTimeoutMs);
                if (!exited)
                {
                    try
                    {
                        process.Kill(true);
                    }
                    catch (Exception killEx)
                    {
                        LogStore.Add("CatHome4", 2, "依赖探测超时杀进程失败：" + exe + "——" + killEx.Message, "DEPS");
                    }
                    return false;
                }
                output = outTask.Result;
                errTask.Wait(200);
                return true;
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "命令探测失败：" + exe + "——" + ex.Message, "DEPS");
                return false;
            }
            finally
            {
                if (process != null)
                {
                    process.Dispose();
                }
            }
        }

        // [段7] 结果组装与排序
        /// <summary>
        /// 构造结果条目（当前时刻）。
        /// </summary>
        /// <param name="level">级别</param>
        /// <param name="name">显示名</param>
        /// <param name="state">状态</param>
        /// <param name="detail">细节</param>
        /// <param name="ttlMs">有效期</param>
        /// <returns>结果条目</returns>
        private static ProbeResult MakeResult(string level, string name, string state, string detail, int ttlMs)
        {
            return MakeResultAt(level, name, state, detail, ttlMs, NowMs());
        }

        /// <summary>
        /// 构造结果条目（指定时刻——配置类采集与后台批次共用）。
        /// </summary>
        /// <param name="level">级别</param>
        /// <param name="name">显示名</param>
        /// <param name="state">状态</param>
        /// <param name="detail">细节</param>
        /// <param name="ttlMs">有效期</param>
        /// <param name="now">探测时刻</param>
        /// <returns>结果条目</returns>
        private static ProbeResult MakeResultAt(string level, string name, string state, string detail, int ttlMs, long now)
        {
            ProbeResult result = new ProbeResult();
            result.Level = level;
            result.Name = name;
            result.State = state;
            result.Detail = detail;
            result.TtlMs = ttlMs;
            result.ProbedAtMs = now;
            return result;
        }

        /// <summary>
        /// 结果排序——级别序（required → optional → dev）→ 名称序。
        /// </summary>
        /// <param name="a">左</param>
        /// <param name="b">右</param>
        /// <returns>比较结果</returns>
        internal static int CompareResults(ProbeResult a, ProbeResult b)
        {
            int la = LevelOrder(a.Level);
            int lb = LevelOrder(b.Level);
            if (la != lb)
            {
                return la - lb;
            }
            return string.CompareOrdinal(a.Name, b.Name);
        }

        /// <summary>
        /// 级别序——排序权重。
        /// </summary>
        /// <param name="level">级别</param>
        /// <returns>权重</returns>
        internal static int LevelOrder(string level)
        {
            if (string.Equals(level, LevelRequired, StringComparison.Ordinal))
            {
                return 0;
            }
            if (string.Equals(level, LevelOptional, StringComparison.Ordinal))
            {
                return 1;
            }
            return 2;
        }

        /// <summary>
        /// 当前时刻（Unix 毫秒）。
        /// </summary>
        /// <returns>毫秒时间戳</returns>
        private static long NowMs()
        {
            return DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
        }

        /// <summary>
        /// 取首行——版本输出首行（去空白）。
        /// </summary>
        /// <param name="text">原始文本</param>
        /// <returns>首行</returns>
        internal static string FirstLine(string text)
        {
            if (text == null)
            {
                return "";
            }
            string trimmed = text.Trim();
            if (trimmed.Length == 0)
            {
                return "";
            }
            int nl = trimmed.IndexOf('\n');
            if (nl >= 0)
            {
                trimmed = trimmed.Substring(0, nl);
            }
            return trimmed.Trim();
        }

        /// <summary>
        /// 拼接路径清单——分号分隔（详情行可读）。
        /// </summary>
        /// <param name="items">条目</param>
        /// <returns>拼接文本</returns>
        private static string JoinList(List<string> items)
        {
            StringBuilder sb = new StringBuilder();
            for (int i = 0; i < items.Count; i++)
            {
                if (i > 0)
                {
                    sb.Append("; ");
                }
                sb.Append(items[i]);
            }
            return sb.ToString();
        }

        // [段8] 测试入口（InternalsVisibleTo CatHome4.Core.Tests——验收探针行为与排序）
        /// <summary>
        /// 路径探针测试入口——对给定路径集跑一次探测（不涉配置面 / 不启后台 / 不写结果表）。
        /// </summary>
        /// <param name="level">级别</param>
        /// <param name="name">显示名</param>
        /// <param name="paths">路径集</param>
        /// <returns>探测结果</returns>
        internal static ProbeResult ProbePathsForTest(string level, string name, List<string> paths)
        {
            ProbeJob job = new ProbeJob();
            job.Level = level;
            job.Name = name;
            job.Kind = "paths";
            job.Paths = paths;
            job.TtlMs = TtlStaticMs;
            return RunPathsProbe(job);
        }
        /// <summary>
        /// 追加「路径配置 + PE 文件版本」探测——适用于版本行不可靠的外部程序（判例：msedge --version 无 stdout 输出、编码不可控）。
        /// </summary>
        /// <param name="cfg">配置读面（可 null）</param>
        /// <param name="direct">配置类直接结果</param>
        /// <param name="jobs">IO 待探任务</param>
        /// <param name="key">配置键</param>
        /// <param name="level">级别</param>
        /// <param name="name">显示名</param>
        /// <param name="ttlMs">有效期</param>
        /// <param name="now">当前时刻</param>
        private static void AddPathFileVersionProbe(ConfigStore cfg, List<ProbeResult> direct, List<ProbeJob> jobs, string key, string level, string name, int ttlMs, long now)
        {
            string path = "";
            if (cfg != null)
            {
                path = cfg.Get(key, "");
            }
            if (path.Length == 0)
            {
                direct.Add(MakeResultAt(level, name, StateUnconfigured, "未配置（" + key + " 空）", ttlMs, now));
                return;
            }
            ProbeJob job = new ProbeJob();
            job.Level = level;
            job.Name = name;
            job.Kind = "filever";
            job.TtlMs = ttlMs;
            job.Paths.Add(path);
            jobs.Add(job);
        }
        /// <summary>
        /// 判定文本是否可解析——含替换字符（U+FFFD）或孤立代理项即视为解码失败（编码不匹配）。
        /// </summary>
        /// <param name="text">待检文本</param>
        /// <returns>是否干净</returns>
        internal static bool IsTextClean(string text)
        {
            if (text == null)
            {
                return false;
            }
            for (int i = 0; i < text.Length; i++)
            {
                char c = text[i];
                if (c == '\uFFFD')
                {
                    return false;
                }
                if (char.IsHighSurrogate(c))
                {
                    if (i + 1 >= text.Length || !char.IsLowSurrogate(text[i + 1]))
                    {
                        return false;
                    }
                    i = i + 1;
                }
                else if (char.IsLowSurrogate(c))
                {
                    return false;
                }
            }
            return true;
        }
    }
}
