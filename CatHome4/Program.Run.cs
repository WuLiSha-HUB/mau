using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Text.Json;
using System.Threading;
using Mau.Runtime;
using CatHome4.Admin;
using CatHome4.QQ;
using CatHome4.Observe;
using CatHome4.Contracts;
using Mau.Providers;

namespace CH4
{
    /// <summary>
    /// Program 运行模式分部——自检/单指令/交互/脚本/指令路由/驱动与空闲判定。
    /// P7b partial 拆分——自 Program.cs 原样搬移，逻辑零改动。
    /// </summary>
    public static partial class Program
    {
        /// <summary>
        /// 启动自检——加载后跑 10 帧并输出三 Cat 状态（非交互验证通道）
        /// </summary>
        /// <returns>退出码</returns>
        private static int RunSelfCheck()
        {
            for (int i = 0; i < WarmupFrames; i = i + 1)
            {
                _runner.Tick();
                Thread.Sleep(FrameTargetMs);
            }
            ObserveService.PrintStatus();
            Console.WriteLine("[CMD] 自检通过");
            return 0;
        }

        /// <summary>
        /// 单指令脚本模式——投递一条 Command → 驱动至闭环 → 输出 → 退出（P2e 验证通道）
        /// </summary>
        /// <param name="command">指令行</param>
        /// <returns>退出码</returns>
        private static int RunSingle(string command)
        {
            bool ok = DispatchCommand(command);
            if (!ok)
            {
                return 2;
            }
            DriveUntilIdle();
            ObserveService.PrintStatus();
            return 0;
        }

        /// <summary>
        /// 交互模式——读行 → 指令路由 → 驱动 → 观测
        /// </summary>
        /// <returns>退出码</returns>
        private static int RunInteractive()
        {
            // 运行模式标记——工具侧读取（非常驻模式 = 无闸门可等，重启请求直接拒绝；design-ch4-host-restart §五）
            DataBox.Set<string>("global", RunModeKey, "interactive");
            // P1 托盘——独立 STA UI 线程（常驻：开机自启 / 打开数据目录 / 显示控制台 / 退出）；仅交互模式启用
            StartTray();
            // A118 外壳——启动显示一次总控窗（WinExe 形态的启动反馈；关闭即结束窗线程，再点托盘重新拉起）
            ShowShellWindow();
            int autoStartPoll = 0;
            while (true)
            {
                long loopStartTicks = Stopwatch.GetTimestamp();
                LoopTiming.BeginFrame();
                // [段1] 帧驱动——HTTP 快照推送主线程泵（ThreadGuard：快照构建须宿主主线程；空闲时也持续 Tick）
                long segTicks = Stopwatch.GetTimestamp();
                _runner.Tick();
                LoopTiming.Accumulate("tick", ElapsedMsSince(segTicks));
                segTicks = Stopwatch.GetTimestamp();
                if (_httpHost != null)
                {
                    _httpHost.PumpMainThread();
                }
                LoopTiming.Accumulate("http", ElapsedMsSince(segTicks));
                segTicks = Stopwatch.GetTimestamp();
                CatHostService.PumpAllHosts();
                LoopTiming.Accumulate("hosts", ElapsedMsSince(segTicks));
                segTicks = Stopwatch.GetTimestamp();
                _chatBridge.PumpSessions();
                LoopTiming.Accumulate("sessions", ElapsedMsSince(segTicks));
                segTicks = Stopwatch.GetTimestamp();
                AdminService.PumpInputQueue();
                LoopTiming.Accumulate("catqueue", ElapsedMsSince(segTicks));
                segTicks = Stopwatch.GetTimestamp();
                QQBotService.Tick();
                LoopTiming.Accumulate("qqbot", ElapsedMsSince(segTicks));
                // 宿主自更新——启动回执注入（一次性）+ 重启闸门（停机态 + 全局 Idle → 接力拉起）
                segTicks = Stopwatch.GetTimestamp();
                PumpMajordomoPush();
                PumpHostRestart();
                // [段1a] P1 托盘退出 + 自启轮询——退出走 break（Main finally 优雅收尾）；5s 粒度覆盖前端/config-* 写路径
                if (_trayExitRequested)
                {
                    break;
                }
                autoStartPoll = autoStartPoll + 1;
                if (autoStartPoll >= AutoStartPollFrames)
                {
                    autoStartPoll = 0;
                    CheckAutoStartPoll();
                }
                LoopTiming.Accumulate("misc", ElapsedMsSince(segTicks));
                // [段2] A118 起交互输入面退役——WinExe 无控制台（Console.KeyAvailable 会抛 InvalidOperationException）；
                // 控制台指令（status / pid / reload / Chat / quit）不再由交互形态承载，仅 --script / --run 等非交互通道保留 ExecuteLine
                // [段2a] 主循环帧耗时采样（A254）——整帧工作实际耗时（不含节拍余量）+ 逐段分解，窗口统计供资源页签
                double loopWorkMs = (Stopwatch.GetTimestamp() - loopStartTicks) * 1000.0 / Stopwatch.Frequency;
                LoopTiming.EndFrame(loopWorkMs, FrameTargetMs);
                // [段2b] 补足式节拍（2026-10-16）——睡「帧预算 − 本帧工作时长」的差额；工作已超预算则不睡
                //（该帧即「超帧」——LoopTiming 计数；固定睡会被工作时长叠加成实际周期 work + 预算）
                int loopSleepMs = FrameTargetMs - (int)loopWorkMs;
                if (loopSleepMs > 0)
                {
                    Thread.Sleep(loopSleepMs);
                }
            }
            return 0;
        }

        /// <summary>
        /// 自起始时刻起的耗时（毫秒）——主循环分段计时用（帧内逐段工作量分解）。
        /// </summary>
        /// <param name="startTicks">起始 Stopwatch 时间戳</param>
        /// <returns>耗时毫秒</returns>
        private static double ElapsedMsSince(long startTicks)
        {
            return (Stopwatch.GetTimestamp() - startTicks) * 1000.0 / Stopwatch.Frequency;
        }
        /// <summary>
        /// Command 解析与投递——宿主做字符串值识别（语料面零值比较）；QuickCat 双参同帧投两个 key
        /// </summary>
        /// <param name="line">输入行</param>
        /// <returns>true=识别成功并已投递</returns>
        private static bool DispatchCommand(string line)
        {
            // A281 分发表迁入 Admin 域（三入口合一）——主入口目标：默认会话 + 主入口特权
            return AdminService.DispatchInput(AdminService.MainInputTarget(), line);
        }

        // A281 六泵并一——原 _chatQueue / _sessionCmdQueue 与 PumpChatQueue 已迁入 Admin 域统一队列（AdminService.PumpInputQueue）



        /// <summary>
        /// 驱动直到三 Cat 全部 Idle——指令投递后连续 Tick；帧上限兜底（LLM 60s 现实耗时 + OA 超时结算窗口）
        /// </summary>
        private static void DriveUntilIdle()
        {
            for (int i = 0; i < MaxFramesPerRun; i = i + 1)
            {
                _runner.Tick();
                if (_httpHost != null)
                {
                    _httpHost.PumpMainThread();
                }
                CatHostService.PumpAllHosts();
                _chatBridge.PumpSessions();
                AdminService.PumpInputQueue();
                QQBotService.Tick();
                if (AllIdle())
                {
                    return;
                }
                Thread.Sleep(FrameTargetMs);
            }
            Console.WriteLine("[CMD] 驱动帧上限 " + MaxFramesPerRun + " 到达——仍有未闭环活动");
        }

        /// <summary>
        /// 全局空闲判定——委托闸门卡点探测（单一出口：会话相位 / 工具组 Flow / OA 在途 / 视图待推）。
        /// A262：与重启闸门同源，避免「空闲判定」与「闸门判定」两套实现漂移。
        /// </summary>
        /// <returns>true=全部空闲</returns>
        private static bool AllIdle()
        {
            return DescribeGateBlocker().Length == 0;
        }

        /// <summary>
        /// 单 Flow 空闲判定——GetStatus 状态行无 "=Idle" 以外的值
        /// </summary>
        /// <param name="handle">Flow 句柄</param>
        /// <returns>true=该 Flow 全部状态机 Idle</returns>
        private static bool IsFlowIdle(FlowHandle handle)
        {
            if (handle == null)
            {
                return true;
            }
            if (handle.IsFaulted)
            {
                return true;
            }
            FlowStatusV3 status = handle.Flow.GetStatus();
            for (int i = 0; i < status.StateLines.Length; i = i + 1)
            {
                string line = status.StateLines[i];
                if (!line.EndsWith("=Idle", StringComparison.Ordinal))
                {
                    return false;
                }
            }
            return true;
        }

        /// <summary>
        /// 单行指令执行——交互/脚本共用（true=继续，false=结束）
        /// </summary>
        /// <param name="line">已 Trim 的指令行</param>
        /// <returns>false=结束会话</returns>
        private static bool ExecuteLine(string line)
        {
            if (line == "quit")
            {
                return false;
            }
            if (line == "status")
            {
                ObserveService.PrintStatus();
                return true;
            }

            if (line == "statusq")
            {
                ObserveService.PrintStatusShort();
                return true;
            }

            if (line == "pid")
            {
                Console.WriteLine("[CMD] pid=" + Environment.ProcessId);
                return true;
            }

            if (line.StartsWith("run ", StringComparison.Ordinal))
            {
                long frames;
                if (!long.TryParse(line.Substring(4).Trim(), out frames) || frames < 0)
                {
                    Console.WriteLine("[CMD] run 参数无效——需非负整数帧数");
                    return true;
                }

                for (long i = 0; i < frames; i = i + 1)
                {
                    _runner.Tick();
                    Thread.Sleep(FrameTargetMs);
                }

                return true;
            }
            if (line.StartsWith("reload ", StringComparison.Ordinal))
            {
                ExecuteReload(line.Substring(7).Trim());
                return true;
            }

            if (line.StartsWith("send ", StringComparison.Ordinal))
            {
                // 只投递不驱动——配合 run <n> 手动帧驱动（超时/挂单场景精确帧数控制）
                if (!DispatchCommand(line.Substring(5).Trim()))
                {
                    Console.WriteLine("[CMD] 格式: send Chat <内容>");
                }
                return true;
            }

            if (!DispatchCommand(line))
            {
                Console.WriteLine("[CMD] 格式: Chat <内容>");
                return true;
            }
            DriveUntilIdle();
            ObserveService.PrintStatusShort();
            return true;
        }

        /// <summary>
        /// 脚本模式——指令文件逐行执行（P3a 热重载实测通道：全程同进程，pid 不变可证）
        /// </summary>
        /// <param name="path">指令文件路径（# 开头行为注释）</param>
        /// <returns>退出码</returns>
        private static int RunScript(string path)
        {
            if (!File.Exists(path))
            {
                Console.WriteLine("[CMD] 脚本文件不存在: " + path);
                return 2;
            }
            string[] lines = File.ReadAllLines(path);
            Console.WriteLine("[CMD] 脚本模式 | " + lines.Length + " 行 | pid=" + Environment.ProcessId);
            for (int i = 0; i < lines.Length; i = i + 1)
            {
                string line = lines[i].Trim();
                if (line.Length == 0 || line.StartsWith("#", StringComparison.Ordinal))
                {
                    continue;
                }
                Console.WriteLine("[CMD] ── 脚本指令 " + (i + 1) + ": " + line + " ──");
                if (!ExecuteLine(line))
                {
                    break;
                }
            }
            Console.WriteLine("[CMD] 脚本结束 | pid=" + Environment.ProcessId);
            return 0;
        }
    }
}