using System;
using System.Diagnostics;
using System.IO;
using System.Text.Json;
using Mau.Runtime;
using CatHome4.Admin;

namespace CH4
{
    /// <summary>
    /// Program 宿主重启分部——重启态判定 / 停机拒收 / 全局 Idle 闸门 / 接力拉起（SetUp relaunch）/ 启动回执注入。
    /// 规范：Project/CH4/design-ch4-host-restart.md（§三 T2-T6 / §四 4.3 / §五 参数契约）。
    /// 定位：小改动自测的宿主自更新链——调用方固定为 Majordomo 会话经 restart-full / restart-incr / restart-host 特权工具（三类分层：全链 / 纯搬运 / 原地）。
    /// </summary>
    public static partial class Program
    {
        /// <summary>重启停机态——DataBox 全局盒键（"requested" = 停机；新需求一律拒收）</summary>
        private const string RestartStateKey = "host_restart_state";

        /// <summary>重启请求载荷——DataBox 全局盒键（JSON：target/push）</summary>
        private const string RestartRequestKey = "host_restart_request";

        /// <summary>运行模式标记——DataBox 全局盒键（"interactive" = 常驻主循环；其余 = 无闸门可等，工具侧拒绝）</summary>
        private const string RunModeKey = "host_run_mode";

        /// <summary>启动回执注入串——<c>--majordomopush</c> 值（主循环首帧投递一次后清空）</summary>
        private static string _majordomoPush = "";
        /// <summary>重启闸门等待上限毫秒——判据持续不满足即撤销重启态（停机态无上限 = 无限期拒收输入，A262）</summary>
        private const int GateWaitMs = 30000;
        /// <summary>闸门卡点出声节流毫秒——等待期每满一次记一条 WARN（失败必须可见，A262）</summary>
        private const int GateWarnIntervalMs = 5000;
        /// <summary>闸门等待起点（TickCount64；0 = 未开始等待）</summary>
        private static long _gateWaitStartMs;
        /// <summary>闸门上一次卡点出声时刻（TickCount64）</summary>
        private static long _gateWarnAtMs;
        /// <summary>
        /// 重启闸门卡点明细——空串 = 全部就绪（可执行接力）；否则逐项列出未就绪判据（会话相位 / 工具组 Flow / OA 在途 / 视图待推）。
        /// 判据单一出口：AllIdle 同源委托，避免「闸门判定」与「空闲判定」两套实现漂移（A262）。
        /// </summary>
        /// <returns>卡点描述（空串 = 无阻碍）</returns>
        private static string DescribeGateBlocker()
        {
            System.Text.StringBuilder sb = new System.Text.StringBuilder();
            for (int i = 0; i < _chatBridge.Sessions.Count; i = i + 1)
            {
                ChatSession session = _chatBridge.Sessions[i];
                if (!session.IsIdle)
                {
                    AppendGateBlocker(sb, "会话「" + session.DisplayName + "」非 Idle（相位 " + session.Phase.ToString() + "）");
                }
            }
            foreach (System.Collections.Generic.KeyValuePair<string, FlowHandle> kv in _toolFlowHandles)
            {
                if (IsFlowIdle(kv.Value))
                {
                    continue;
                }
                AppendGateBlocker(sb, "Flow「" + kv.Key + "」非 Idle（" + FlowBusyLines(kv.Value) + "）");
            }
            OAView oaView = _oa.GetSnapshot();
            if (oaView.OpenCount > 0 || oaView.WorkCount > 0)
            {
                AppendGateBlocker(sb, "OA 在途（待认领 " + oaView.OpenCount.ToString() + " / 执行中 " + oaView.WorkCount.ToString() + "）");
            }
            if (AdminService.AnyViewPending())
            {
                AppendGateBlocker(sb, "视图推送仍有待推内容");
            }
            return sb.ToString();
        }
        /// <summary>
        /// 卡点条目追加——多条以分号分隔（闸门明细可读性）。
        /// </summary>
        /// <param name="sb">目标缓冲</param>
        /// <param name="text">单条卡点描述</param>
        private static void AppendGateBlocker(System.Text.StringBuilder sb, string text)
        {
            if (sb.Length > 0)
            {
                sb.Append("；");
            }
            sb.Append(text);
        }
        /// <summary>
        /// Flow 非 Idle 状态行拼接——只列非 =Idle 的行（卡点定位用）；取不到状态行时如实标注。
        /// </summary>
        /// <param name="handle">Flow 句柄</param>
        /// <returns>非空闲状态行（空格分隔）</returns>
        private static string FlowBusyLines(FlowHandle handle)
        {
            if (handle == null || handle.Flow == null)
            {
                return "句柄不可用";
            }
            FlowStatusV3 status = handle.Flow.GetStatus();
            System.Text.StringBuilder sb = new System.Text.StringBuilder();
            for (int i = 0; i < status.StateLines.Length; i = i + 1)
            {
                string line = status.StateLines[i];
                if (line.EndsWith("=Idle", StringComparison.Ordinal))
                {
                    continue;
                }
                if (sb.Length > 0)
                {
                    sb.Append(" ");
                }
                sb.Append(line);
            }
            if (sb.Length == 0)
            {
                sb.Append("无状态行");
            }
            return sb.ToString();
        }
        /// <summary>
        /// 复位闸门等待态——退出重启态 / 放行接力 / 撤销后调用（下一次重启重新起算）。
        /// </summary>
        private static void ResetGateWait()
        {
            _gateWaitStartMs = 0;
            _gateWarnAtMs = 0;
        }

        /// <summary>
        /// 宿主参数面校验——零容忍：未知参数 / 缺值 / 与非常驻模式互斥一律报错（design-ch4-host-restart §五）。
        /// </summary>
        /// <param name="args">命令行参数</param>
        /// <param name="nonInteractive">非交互模式（--run/--script/--selfcheck——不启动外观层与常驻附属）</param>
        /// <returns>错误文本（空=通过）</returns>
        private static string ValidateHostArgs(string[] args, out bool nonInteractive)
        {
            nonInteractive = false;
            bool hasPush = false;
            bool hasNonInteractive = false;
            // 预扫描——先定非交互形态：出错提前返回时也要能分派「脚本必须拿到退出码」（A260 R2）
            for (int i = 0; i < args.Length; i = i + 1)
            {
                if (args[i] == "--run" || args[i] == "--script" || args[i] == "--selfcheck" || args[i] == "--probe-llm")
                {
                    nonInteractive = true;
                    break;
                }
            }
            for (int i = 0; i < args.Length; i = i + 1)
            {
                string arg = args[i];
                if (arg == "-dll" || arg == "--run" || arg == "--script" || arg == "--probe-llm")
                {
                    if (i + 1 >= args.Length)
                    {
                        return "参数错误：" + arg + " 缺值。";
                    }
                    if (arg == "--run" || arg == "--script")
                    {
                        hasNonInteractive = true;
                    }
                    i = i + 1;
                }
                else if (arg == "--selfcheck")
                {
                    hasNonInteractive = true;
                }
                else if (arg == "--majordomopush")
                {
                    if (i + 1 >= args.Length || args[i + 1].Length == 0)
                    {
                        return "参数错误：--majordomopush 缺值（需注入串）。";
                    }
                    hasPush = true;
                    i = i + 1;
                }
                else if (arg.Length > 0 && arg[0] == '-')
                {
                    return "参数错误：未知参数 " + arg + "。";
                }
            }
            if (hasPush && hasNonInteractive)
            {
                return "参数错误：--majordomopush 仅用于常驻交互启动（不可与 --run/--script/--selfcheck 同用）。";
            }
            nonInteractive = hasNonInteractive;
            return "";
        }

        /// <summary>
        /// 取参数值——未出现返回空串（缺值由校验段负责报错）。
        /// </summary>
        /// <param name="args">命令行参数</param>
        /// <param name="name">参数名</param>
        /// <returns>参数值或空串</returns>
        private static string ExtractArgValue(string[] args, string name)
        {
            for (int i = 0; i < args.Length; i = i + 1)
            {
                if (args[i] == name && i + 1 < args.Length)
                {
                    return args[i + 1];
                }
            }
            return "";
        }

        /// <summary>
        /// 宿主是否处于重启停机态。
        /// </summary>
        /// <returns>true=停机态（拒收新需求 + 等闸门）</returns>
        private static bool IsRestarting()
        {
            string state;
            if (DataBox.TryGet<string>("global", RestartStateKey, out state))
            {
                return state == "requested";
            }
            return false;
        }

        /// <summary>
        /// 启动回执注入泵——主循环每帧调用；有注入串则投给 Majordomo 会话（来源 system），投完即清（一次性）。
        /// </summary>
        private static void PumpMajordomoPush()
        {
            if (_majordomoPush.Length == 0)
            {
                return;
            }
            string push = _majordomoPush;
            _majordomoPush = "";
            if (_chatBridge == null)
            {
                return;
            }
            // A72——回执经延迟队列投递（dueAt=now 立即注入；统一出口，可观测、落盘可查——design-ch4-delay §7.2）
            string added = DelayQueue.Add(_chatBridge.DefaultSession.Id, push, "restart", DelayQueue.Now());
            if (added.StartsWith("ERR|", StringComparison.Ordinal))
            {
                // 失败可见——回落直投（回执不丢），并出声
                LogStore.Add("CatHome4", 2, "宿主启动回执登记延迟队列失败（回落直投）: " + added, "RESTART");
                _chatBridge.PostSystemMessage(_chatBridge.DefaultSession.Id, ChatSession.SysKindSystemAuto, push);
                return;
            }
            LogStore.Add("CatHome4", 1, "宿主启动回执已登记延迟队列（到点注入 Majordomo 会话）", "RESTART");
        }

        /// <summary>
        /// 重启闸门泵——主循环每帧调用：停机态 + 全局就绪 → 执行接力（起 SetUp 后退出）。
        /// 在途轮次由停机态自然收尾；闸门等的是在途，不是等人（design-ch4-host-restart §三 T3）。
        /// A262——闸门有上限（GateWaitMs）与卡点出声（GateWarnIntervalMs）：判据持续不满足即撤销重启态并出声，禁止无限期停机。
        /// </summary>
        private static void PumpHostRestart()
        {
            if (!IsRestarting())
            {
                ResetGateWait();
                return;
            }
            // incr 握手等待态——接力者已起，等 ready 哨兵（此态下不看闸门：宿主已在停机态，等的是信号）
            if (_relayWaiting)
            {
                PumpRelayWait();
                return;
            }
            if (_gateWaitStartMs == 0)
            {
                _gateWaitStartMs = Environment.TickCount64;
            }
            // 判据单一出口——卡点明细同时承担「能否执行」与「为什么不能」两个答案（A262）
            string blocker = DescribeGateBlocker();
            if (blocker.Length == 0)
            {
                ResetGateWait();
                ExecuteRestart();
                return;
            }
            long waitedMs = Environment.TickCount64 - _gateWaitStartMs;
            // A262——闸门上限：停机态不设上限 = 无限期拒收输入（判例 2026-10-20 restart-incr 卡死，用户只能手动停宿主）
            if (waitedMs >= GateWaitMs)
            {
                AbortRestart("重启闸门等待超时（" + (waitedMs / 1000).ToString() + " s，上限 " + (GateWaitMs / 1000).ToString() + " s）——卡点：" + blocker);
                ResetGateWait();
                return;
            }
            // A262——卡点出声：节流 WARN（看不见卡在哪 = 只能事后翻 frame.txt 反推，违反失败必须可见）
            if (Environment.TickCount64 - _gateWarnAtMs >= GateWarnIntervalMs)
            {
                _gateWarnAtMs = Environment.TickCount64;
                LogStore.Add("CatHome4", 2, "重启闸门等待中（" + (waitedMs / 1000).ToString() + " s / 上限 " + (GateWaitMs / 1000).ToString() + " s）——卡点：" + blocker, "RESTART");
            }
        }

        /// <summary>
        /// 执行接力——起 SetUp relaunch（detached）→ 确认进程已起 → 观测收尾 → 退出宿主。
        /// 顺序铁律：先确认接力者已起，再自杀——反序即无人拉起（design-ch4-host-restart §三 T4）。
        /// </summary>
        private static void ExecuteRestart()
        {
            int pid = Environment.ProcessId;
            string reqJson = "";
            string raw;
            if (DataBox.TryGet<string>("global", RestartRequestKey, out raw) && raw != null)
            {
                reqJson = raw;
            }
            string mode = ReadJsonField(reqJson, "mode");
            if (mode.Length == 0)
            {
                mode = "full";
            }
            if (mode != "full" && mode != "incr" && mode != "host")
            {
                AbortRestart("重启模式非法（" + mode + "）——已撤销重启态。");
                return;
            }
            string target = ReadJsonField(reqJson, "target");
            string push = ReadJsonField(reqJson, "push");
            if (target.Length == 0)
            {
                target = ResolveMauOutRoot();
            }
            if (target.Length == 0)
            {
                AbortRestart("重启请求缺少目标运行区（target 未提供且受控根 mauout 未配置）——已撤销重启态。");
                return;
            }
            string repoRoot = ResolveRepoRootForRestart();
            if (repoRoot.Length == 0)
            {
                AbortRestart("未找到仓库根（运行区向上探测失败且受控根 mau 未配置）——无法定位 SetUp.exe，已撤销重启态。");
                return;
            }
            string setupExe = Path.Combine(repoRoot, "SetUp.exe");
            if (!File.Exists(setupExe))
            {
                AbortRestart("SetUp.exe 不存在：" + setupExe + "——已撤销重启态。");
                return;
            }
            // incr 握手——先清陈旧哨兵（防上一轮残留被误当本轮就绪信号：陈旧命中是本链最隐蔽的假绿源）
            if (mode == "incr")
            {
                try
                {
                    string stale = RelayReadyPath(repoRoot);
                    if (File.Exists(stale))
                    {
                        File.Delete(stale);
                    }
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 2, "陈旧 ready 哨兵清理失败: " + ex.Message, "RESTART");
                }
            }
            // A135——报告路径带时间戳：固定名会让「本次」的结论被上一次覆盖（读者可能读到旧的"成功"）
            string report = Path.Combine(repoRoot, "CatTemp", "restart_report_" + DateTime.Now.ToString("yyyyMMdd_HHmmss") + ".json");
            try
            {
                string reportDir = Path.GetDirectoryName(report);
                if (reportDir != null && reportDir.Length > 0)
                {
                    Directory.CreateDirectory(reportDir);
                }
            }
            catch (Exception ex)
            {
                Console.WriteLine("[CMD] 警告：重启报告目录创建失败——" + ex.Message);
            }
            ProcessStartInfo psi = new ProcessStartInfo(setupExe);
            psi.WorkingDirectory = repoRoot;
            psi.UseShellExecute = false;
            psi.CreateNoWindow = true;
            psi.ArgumentList.Add("relaunch");
            // mode 显式声明——full 不传（缺省即 full，兼容旧 SetUp 的口径）；incr / host 必传
            if (mode != "full")
            {
                psi.ArgumentList.Add("--mode");
                psi.ArgumentList.Add(mode);
            }
            psi.ArgumentList.Add("--wait-pid");
            psi.ArgumentList.Add(pid.ToString());
            psi.ArgumentList.Add("--target");
            psi.ArgumentList.Add(target);
            if (push.Length > 0)
            {
                psi.ArgumentList.Add("--majordomopush");
                psi.ArgumentList.Add(push);
            }
            psi.ArgumentList.Add("--report");
            psi.ArgumentList.Add(report);
            Process relay;
            try
            {
                relay = Process.Start(psi);
            }
            catch (Exception ex)
            {
                AbortRestart("SetUp relaunch 启动失败——" + ex.Message + "（已撤销重启态）");
                return;
            }
            if (relay == null)
            {
                AbortRestart("SetUp relaunch 启动失败（Process.Start 返回空）——已撤销重启态。");
                return;
            }
            // 确认接力者确已起来——1 秒内未退出即视为存活（先起接力者，再自杀）
            // A206——分片等待：原整体阻塞 1 秒会让推送面停摆（期间待推帧无处可推——判例 2026-10-06）
            bool relayDead = false;
            long relayWaitStart = Environment.TickCount64;
            while (Environment.TickCount64 - relayWaitStart < 1000)
            {
                if (relay.WaitForExit(50))
                {
                    relayDead = true;
                    break;
                }
                AdminService.PumpAllHosts();
            }
            if (relayDead)
            {
                // A134——incr 早退不是「接力者起不来」：前置段失败会正常退出，结论落在 ready 哨兵里（先读哨兵再定性）
                if (mode == "incr")
                {
                    string relayReason = ReadRelayReason(repoRoot);
                    if (relayReason.Length > 0)
                    {
                        AbortRestart("incr 前置段未通过——" + relayReason);
                        return;
                    }
                }
                AbortRestart("SetUp relaunch 启动后立即退出（exit=" + relay.ExitCode.ToString() + "）——已撤销重启态。");
                return;
            }
            LogStore.Add("CatHome4", 1, "重启接力已拉起（mode=" + mode + "）：SetUp relaunch pid " + relay.Id.ToString() + " → 目标 " + target, "RESTART");
            Console.WriteLine("[CMD] 重启接力已拉起（mode=" + mode + "，SetUp pid " + relay.Id.ToString() + "）。");
            if (mode == "incr")
            {
                // 握手等待——等接力者落 ready 哨兵（探活前置的兑现路径）：此间宿主不退出，停机态继续拒收新需求
                _relayWaiting = true;
                _relayWaitStartMs = Environment.TickCount64;
                Console.WriteLine("[CMD] incr 握手等待中——等接力者探活结论（上限 " + (RelayWaitMs / 1000).ToString() + " s）。");
                return;
            }
            FinishRestartAndExit();
        }

        /// <summary>
        /// 撤销重启态——接力未能拉起（或前置缺失）：清停机态与请求载荷，宿主继续运行。
        /// </summary>
        /// <param name="reason">撤销原因（日志 + 控制台）</param>
        private static void AbortRestart(string reason)
        {
            DataBox.Set<string>("global", RestartStateKey, "");
            DataBox.Remove("global", RestartRequestKey);
            _relayWaiting = false;
            LogStore.Add("CatHome4", 3, "重启已撤销：" + reason, "RESTART");
            Console.WriteLine("[CMD] " + reason);
            // A134——单点回执：所有撤销路径都出声（静默撤销违反「失败必须可见」；判例 = incr 早退无回声）
            NotifyRestartAborted(reason);
        }
        /// <summary>
        /// 重启链仓库根解析——三级锚定（与 ResolveDataRoot 同构）：
        /// 1. CH4_REPO_ROOT 环境变量（显式逃逸口——多实例/自定义落位）
        /// 2. 运行区向上探测（Mau.sln——开发/测试区形态：运行区位于仓库内）
        /// 3. 受控根 mau（workspace 配置——外部部署区形态：运行区不在仓库内，双校验 Mau.sln + SetUp.exe）
        /// </summary>
        /// <returns>仓库根绝对路径（空=三级皆未命中）</returns>
        private static string ResolveRepoRootForRestart()
        {
            string envRoot = Environment.GetEnvironmentVariable("CH4_REPO_ROOT");
            if (envRoot != null && envRoot.Length > 0 && File.Exists(Path.Combine(envRoot, "SetUp.exe")))
            {
                return envRoot;
            }
            string probed = FindRepoRoot(AppContext.BaseDirectory);
            if (probed.Length > 0 && File.Exists(Path.Combine(probed, "SetUp.exe")))
            {
                return probed;
            }
            WorkspaceConfig cfg;
            if (DataBox.TryResolve<WorkspaceConfig>(out cfg) && cfg != null && cfg.Roots != null)
            {
                for (int i = 0; i < cfg.Roots.Length; i = i + 1)
                {
                    if (!string.Equals(cfg.Roots[i].Id, "mau", StringComparison.OrdinalIgnoreCase))
                    {
                        continue;
                    }
                    string path = cfg.Roots[i].Path;
                    if (File.Exists(Path.Combine(path, "Mau.sln")) && File.Exists(Path.Combine(path, "SetUp.exe")))
                    {
                        return path;
                    }
                }
            }
            return "";
        }

        /// <summary>
        /// 解析受控根 mauout 路径——workspace 配置（Bootstrap 已 Bind）中 id=mauout 的根。
        /// </summary>
        /// <returns>运行区绝对路径（空=未配置）</returns>
        private static string ResolveMauOutRoot()
        {
            WorkspaceConfig cfg;
            if (DataBox.TryResolve<WorkspaceConfig>(out cfg) && cfg != null && cfg.Roots != null)
            {
                for (int i = 0; i < cfg.Roots.Length; i = i + 1)
                {
                    if (string.Equals(cfg.Roots[i].Id, "mauout", StringComparison.OrdinalIgnoreCase))
                    {
                        return cfg.Roots[i].Path;
                    }
                }
            }
            return "";
        }

        /// <summary>
        /// 取 JSON 对象字符串字段——防御式（非对象/缺字段/解析失败一律返回空串）。
        /// </summary>
        /// <param name="json">JSON 文本</param>
        /// <param name="field">字段名</param>
        /// <returns>字段值或空串</returns>
        private static string ReadJsonField(string json, string field)
        {
            if (json == null || json.Length == 0)
            {
                return "";
            }
            try
            {
                JsonDocument doc = JsonUtil.ParseStrict(json);
                try
                {
                    JsonElement el;
                    if (doc.RootElement.ValueKind == JsonValueKind.Object && doc.RootElement.TryGetProperty(field, out el) && el.ValueKind == JsonValueKind.String)
                    {
                        return el.GetString() ?? "";
                    }
                }
                finally
                {
                    doc.Dispose();
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "重启参数提取失败: " + ex.Message, "RESTART");
            }
            return "";
        }
        /// <summary>incr 握手等待上限毫秒——接力者须在此窗口内落 ready 哨兵，超时即撤销重启态（宿主继续运行）</summary>
        private const int RelayWaitMs = 180000;
        /// <summary>握手等待态——接力已起、等 ready 哨兵（仅 incr；full / host 不起等待）</summary>
        private static bool _relayWaiting;
        /// <summary>握手等待起始时刻（TickCount64）</summary>
        private static long _relayWaitStartMs;
        /// <summary>
        /// ready 哨兵路径——与 SetUp 侧同为仓库根 CatTemp/restart_relay.json（握手两端同源）。
        /// </summary>
        /// <param name="repoRoot">仓库根</param>
        /// <returns>哨兵文件绝对路径</returns>
        private static string RelayReadyPath(string repoRoot)
        {
            return Path.Combine(repoRoot, "CatTemp", "restart_relay.json");
        }
        /// <summary>
        /// 读 ready 哨兵失败原因（A134）——早退路径取结论用（前置段失败会正常退出，不是「接力者起不来」）。
        /// </summary>
        /// <param name="repoRoot">仓库根</param>
        /// <returns>失败原因（空=无哨兵 / 哨兵为 ok=true）</returns>
        private static string ReadRelayReason(string repoRoot)
        {
            string relay = RelayReadyPath(repoRoot);
            if (!File.Exists(relay))
            {
                return "";
            }
            string json = "";
            try
            {
                json = File.ReadAllText(relay);
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "ready 哨兵读取失败: " + ex.Message, "RESTART");
                return "";
            }
            bool ok;
            if (!ReadJsonBoolField(json, "ok", out ok) || ok)
            {
                return "";
            }
            string reason = ReadJsonField(json, "reason");
            if (reason.Length == 0)
            {
                return "（哨兵未带原因）";
            }
            return reason;
        }
        /// <summary>
        /// 握手等待泵（仅 incr）——轮询接力者落的 ready 哨兵：ok=true → 收尾退出；ok=false → 撤销重启态 + 失败原因回执；超时 → 撤销。
        /// 反序（先退出再探活）则探活无从前置（design-ch4-host-restart §三·五）。
        /// </summary>
        private static void PumpRelayWait()
        {
            string repoRoot = ResolveRepoRootForRestart();
            if (repoRoot.Length == 0)
            {
                AbortRestart("incr 握手失败：未找到仓库根（读不到 ready 哨兵）——已撤销重启态。");
                return;
            }
            string relay = RelayReadyPath(repoRoot);
            if (File.Exists(relay))
            {
                string json = "";
                try
                {
                    json = File.ReadAllText(relay);
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 2, "ready 哨兵读取失败: " + ex.Message, "RESTART");
                }
                bool ok;
                if (json.Length > 0 && ReadJsonBoolField(json, "ok", out ok))
                {
                    if (ok)
                    {
                        LogStore.Add("CatHome4", 1, "incr 前置段通过——握手完成，宿主退出交接力者", "RESTART");
                        FinishRestartAndExit();
                        return;
                    }
                    string reason = ReadJsonField(json, "reason");
                    AbortRestart("incr 前置段未通过——" + reason);
                    return;
                }
            }
            if (Environment.TickCount64 - _relayWaitStartMs > RelayWaitMs)
            {
                AbortRestart("incr 握手超时（" + (RelayWaitMs / 1000).ToString() + " s 内未见 ready 哨兵）——已撤销重启态。");
            }
        }
        /// <summary>
        /// 失败回执——把重启未执行的原因投给 Majordomo 会话（system 来源；失败必须可见，不留静默放弃）。
        /// </summary>
        /// <param name="reason">失败原因</param>
        private static void NotifyRestartAborted(string reason)
        {
            if (_chatBridge == null)
            {
                return;
            }
            string text = "[宿主自更新] 重启未执行"
                + Environment.NewLine + "原因: " + reason
                + Environment.NewLine + "宿主继续运行（运行区未改动）。";
            _chatBridge.PostSystemMessage(_chatBridge.DefaultSession.Id, ChatSession.SysKindSystemAuto, text);
        }
        /// <summary>
        /// 取 JSON 对象布尔字段——防御式（非对象 / 缺字段 / 类型不符返回 false）。
        /// </summary>
        /// <param name="json">JSON 文本</param>
        /// <param name="field">字段名</param>
        /// <param name="value">字段值</param>
        /// <returns>true=成功取到布尔值</returns>
        private static bool ReadJsonBoolField(string json, string field, out bool value)
        {
            value = false;
            if (json == null || json.Length == 0)
            {
                return false;
            }
            try
            {
                JsonDocument doc = JsonUtil.ParseStrict(json);
                try
                {
                    JsonElement el;
                    if (doc.RootElement.ValueKind == JsonValueKind.Object && doc.RootElement.TryGetProperty(field, out el))
                    {
                        if (el.ValueKind == JsonValueKind.True)
                        {
                            value = true;
                            return true;
                        }
                        if (el.ValueKind == JsonValueKind.False)
                        {
                            value = false;
                            return true;
                        }
                    }
                }
                finally
                {
                    doc.Dispose();
                }
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "重启哨兵解析失败: " + ex.Message, "RESTART");
            }
            return false;
        }
        /// <summary>
        /// 收尾并退出——转发态快照兜底 + 观测收尾 + Exit(0)（闸门退出与 incr 握手放行共用同一路径）。
        /// </summary>
        private static void FinishRestartAndExit()
        {
            // A206——SSE 收尾：先推最后一帧，再等各连接队列排空（视图帧「入队 ≠ 写出」——
            // Environment.Exit 直接终止进程会丢弃在途帧，前端停在半句；判例 2026-10-06 重启现场）
            AdminService.FlushAllHosts(800);
            // A33——转发态全量快照兜底（T4）：稳态虽已「变更即落盘」，此处再覆写一次（覆盖收尾窗口内尚未落盘的状态变更）
            CatHome4.QQ.QQBotService.SaveForwardState();
            // 观测收尾——四文件 flush（与 Main finally 同一路径）
            LogStore.CloseWriters();
            FrameStore.Close();
            Environment.Exit(0);
        }
    }
}
