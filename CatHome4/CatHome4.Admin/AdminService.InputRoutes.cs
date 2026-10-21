using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using Mau.Runtime;
using CatHome4.Http;
using Mau.Providers;
using CH4;

namespace CatHome4.Admin
{
    /// <summary>
    /// 输入目标——一条外部输入作用于哪个会话（A281：三入口共用同表，只差目标）。
    /// 数据来源：主入口 = ChatBridge 默认面；每猫端口 = CatEntry；majordomo 独立端口 = 默认面 + 本地猫控。
    /// </summary>
    internal sealed class InputTarget
    {
        /// <summary>目标会话——Chat / Note / 会话指令的作用对象</summary>
        public ChatSession Session;

        /// <summary>角色段——session.new 重注入用</summary>
        public string Persona;

        /// <summary>注入清单——session.new 重注入用</summary>
        public string[] InjectList;

        /// <summary>工具声明面——session.new 重注入用</summary>
        public ToolSpec[] ToolSpecs;

        /// <summary>本地猫控——true=接受无参 cat.pause / cat.continue（每猫与 majordomo 独立端口）</summary>
        public bool LocalCatControl;

        /// <summary>主入口——true=接受 reload 等宿主级指令（CLI / 主端口 / 工具桥）</summary>
        public bool MainEntry;

        /// <summary>排错标签（日志区分入口）</summary>
        public string Tag;
    }

    /// <summary>
    /// 输入路由项——外部输入一行分发表项（理线器形态，A281 迁入 Admin 域）。
    /// 定位：只做「查表即得」的路由声明；不承载缓存 / 移除 / 覆盖 / 预输入 / 全量监控。
    /// </summary>
    internal sealed class InputRoute
    {
        /// <summary>匹配串（不含参数）</summary>
        public string Key;

        /// <summary>匹配方式——true=前缀匹配 / false=全等匹配</summary>
        public bool Prefix;

        /// <summary>线程域——true=仅主线程执行（非主线程入统一队列）</summary>
        public bool MainThreadOnly;

        /// <summary>仅主入口可命中（reload 等宿主级指令）</summary>
        public bool MainEntryOnly;

        /// <summary>仅本地猫控目标可命中（无参 cat.pause / cat.continue）</summary>
        public bool LocalCatControlOnly;

        /// <summary>忙则延后——目标会话非 Idle 时本帧不执行，下帧重试（session.new / 会话调试指令）</summary>
        public bool WaitIdle;

        /// <summary>非主线程不排队——true=非主线程直接交执行体（由执行体当场拒绝），不投递泵</summary>
        public bool NoQueueOffMain;

        /// <summary>执行体——返回 true=已识别并受理 / false=识别但未受理（空内容等）</summary>
        public Func<InputTarget, string, bool> Handler;

        /// <summary>用途一句话——表即文档</summary>
        public string Note;

        /// <summary>
        /// 构造一条路由——全字段显式给出（避免默认值歧义）。
        /// </summary>
        /// <param name="key">匹配串</param>
        /// <param name="prefix">true=前缀匹配 / false=全等匹配</param>
        /// <param name="mainThreadOnly">true=仅主线程执行</param>
        /// <param name="handler">执行体</param>
        /// <param name="note">用途一句话</param>
        public InputRoute(string key, bool prefix, bool mainThreadOnly, Func<InputTarget, string, bool> handler, string note)
        {
            Key = key;
            Prefix = prefix;
            MainThreadOnly = mainThreadOnly;
            Handler = handler;
            Note = note;
        }
    }

    /// <summary>
    /// 输入作业——统一队列元素（目标 + 原始指令行，A281 六泵并一）。
    /// </summary>
    internal sealed class InputJob
    {
        /// <summary>目标会话选择器</summary>
        public InputTarget Target;

        /// <summary>原始指令行（含前缀与参数——主线程泵内重新分发）</summary>
        public string Line;

        /// <summary>延后提示是否已打印（忙则延后只出声一次，不刷屏）</summary>
        public bool DeferLogged;
    }

    /// <summary>
    /// 外部输入分发分部——单一分发表 + 单一队列 + 单一 Dispatcher（A281 三入口合一）。
    /// 边界：宿主内路由归本表；跨 ALC（LLM / Flow → 宿主能力）归 OA——两者正交。
    /// 线程模型：表项声明线程域（MainThreadOnly）；非主线程一律入 <see cref="_inputQueue"/>，主线程泵重放。
    /// </summary>
    internal static partial class AdminService
    {
        /// <summary>统一输入队列——按目标分组（每目标一条），HTTP / 工具桥线程投递，主线程泵消费（六泵并一）</summary>
        private static readonly Dictionary<string, ConcurrentQueue<InputJob>> _inputQueues = new Dictionary<string, ConcurrentQueue<InputJob>>();

        /// <summary>各目标忙则延后的队首作业——同目标内「前面的不执行，后面的不越过」，跨目标互不阻塞</summary>
        private static readonly Dictionary<string, InputJob> _inputHeld = new Dictionary<string, InputJob>();

        /// <summary>队列字典锁——投递线程与主线程泵并发访问（2026-10-21 并发判据：低频写 / 高频读，锁仅护字典操作）</summary>
        private static readonly object _inputQueueLock = new object();

        /// <summary>宿主停机态探针——入口壳注入（重启期间拒收一切新指令）</summary>
        public static Func<bool> IsRestartingProvider;

        /// <summary>热重载执行体——入口壳注入（宿主级能力，本域不持有实现）</summary>
        public static Func<string, string> ReloadHandler;

        /// <summary>分发表——声明序即匹配序（全等项与限定项置于前缀项之前）</summary>
        private static readonly InputRoute[] InputRoutes = BuildInputRoutes();

        /// <summary>
        /// 构建分发表——三入口原有分支的并集，逐条搬为表项（行为等价，形态由三份 if 链改为一张表）。
        /// </summary>
        /// <returns>路由表（声明序即匹配序）</returns>
        private static InputRoute[] BuildInputRoutes()
        {
            // [段1] 对话输入——内容投递目标会话
            InputRoute chat = new InputRoute("Chat ", true, true, RouteChat, "对话内容投递目标会话");

            // [段2] 本地猫控——无参形式仅每猫 / majordomo 独立端口（主入口走 cat.* 管理面需带 key）
            InputRoute pause = new InputRoute("cat.pause", false, true, RoutePause, "中止本端口会话回合（无参形式）");
            pause.LocalCatControlOnly = true;
            InputRoute cont = new InputRoute("cat.continue", false, false, RouteContinue, "继续本端口会话（无参形式）");
            cont.LocalCatControlOnly = true;

            // [段3] 会话生命周期与 Note——全等项在前（避免被前缀项吞掉）
            InputRoute sessionNew = new InputRoute("session.new", false, true, RouteSessionNew, "新会话：清前文 + 按清单重注入");
            sessionNew.WaitIdle = true;
            InputRoute sessionClear = new InputRoute("session clear", false, true, RouteSessionCmd, "会话调试：清空");
            sessionClear.WaitIdle = true;
            InputRoute sessionCount = new InputRoute("session count", false, true, RouteSessionCmd, "会话调试：计数");
            sessionCount.WaitIdle = true;
            InputRoute noteStart = new InputRoute("note.start", false, true, RouteNoteStart, "Note 计划启动（推给 LLM）");
            InputRoute noteAdd = new InputRoute("note.add ", true, true, RouteNoteAdd, "Note 手动新增一步");

            // [段4] 会话回滚 / 分支——上下文仅主线程触碰
            InputRoute rollback = new InputRoute("session.rollback ", true, true, RouteSessionCmdExec, "回滚会话到指定节点");
            InputRoute fork = new InputRoute("session.fork ", true, true, RouteSessionCmdExec, "分支新会话（播种克隆）");

            // [段5] 热重载——主入口专属（宿主级能力，注入执行体）
            InputRoute reload = new InputRoute("reload ", true, true, RouteReload, "热重载 Flow（事务三段式）");
            reload.MainEntryOnly = true;
            reload.NoQueueOffMain = true;

            // [段6] 管理指令族——统一内核执行体 HandleCatCommand
            InputRoute catCmd = new InputRoute("cat.", true, true, RouteAdminLine, "猫管理指令族（cat.*）");
            InputRoute catCfgCmd = new InputRoute("catcfg.", true, true, RouteAdminLine, "每猫配置指令族（catcfg.*）");

            // [段7] 延迟指令族——调度器内部锁保护，任意线程可直执
            InputRoute delay = new InputRoute("delay.", true, false, RouteDelayCommand, "延迟指令队列（定时输入）");

            return new InputRoute[] { chat, pause, cont, sessionNew, sessionClear, sessionCount, noteStart, noteAdd, rollback, fork, reload, catCmd, catCfgCmd, delay };
        }

        /// <summary>
        /// 主入口目标——CLI / 主端口 / 工具桥（默认会话，无本地猫控）。
        /// </summary>
        /// <returns>目标选择器</returns>
        internal static InputTarget MainInputTarget()
        {
            InputTarget target = new InputTarget();
            target.Session = _chatBridge.DefaultSession;
            target.Persona = _chatBridge.DefaultPersona;
            target.InjectList = _chatBridge.DefaultInjectList;
            target.ToolSpecs = _chatBridge.DefaultToolSpecs;
            target.LocalCatControl = false;
            target.MainEntry = true;
            target.Tag = "main";
            return target;
        }

        /// <summary>
        /// majordomo 独立对话端口目标——默认会话 + 本地猫控（原 DispatchCommandForMajor 语义）。
        /// </summary>
        /// <returns>目标选择器</returns>
        internal static InputTarget MajorInputTarget()
        {
            InputTarget target = MainInputTarget();
            target.LocalCatControl = true;
            target.Tag = "major";
            return target;
        }

        /// <summary>
        /// 每猫端口目标——本猫会话 + 本地猫控（原 DispatchCommandForCat 语义）。
        /// </summary>
        /// <param name="cat">猫实体</param>
        /// <returns>目标选择器</returns>
        private static InputTarget CatInputTarget(CatEntry cat)
        {
            InputTarget target = new InputTarget();
            target.Session = cat.Session;
            target.Persona = cat.Persona;
            target.InjectList = cat.InjectList;
            target.ToolSpecs = cat.ToolSpecs;
            target.LocalCatControl = true;
            target.MainEntry = false;
            target.Tag = cat.Id;
            return target;
        }

        /// <summary>
        /// 统一分发入口——停机态 → 查表 → 线程域 → 执行（四段，全表共用）。
        /// 非主线程且表项为主线程域 → 入统一队列，下帧主线程重放（等价于原「分支手写双路」）。
        /// </summary>
        /// <param name="target">目标会话选择器</param>
        /// <param name="line">输入行</param>
        /// <returns>true=已识别并受理（含入队）；false=未识别</returns>
        internal static bool DispatchInput(InputTarget target, string line)
        {
            // [段1] 停机态——拒绝一切新指令（design-ch4-host-restart §三 T2）
            if (IsRestartingProvider != null && IsRestartingProvider())
            {
                Console.WriteLine("[CMD] 宿主重启中——指令未受理：" + line);
                return true;
            }
            // [段2] 空行——识别失败
            if (line == null || line.Length == 0)
            {
                return false;
            }
            // [段3] 查表——首个命中生效；未命中即未识别（回执契约不变：HTTP {ok:false} / 工具桥 ERR|BAD_PREFIX）
            InputRoute route = MatchRoute(target, line);
            if (route == null)
            {
                return false;
            }
            // [段4] 线程域——表字段声明；主线程专属项在非主线程入队（拒绝态项不排队，交执行体当场拒绝）
            if (route.MainThreadOnly && Environment.CurrentManagedThreadId != _mainThreadId)
            {
                if (route.NoQueueOffMain)
                {
                    return route.Handler(target, line);
                }
                PostInput(target, line);
                return true;
            }
            // [段5] 忙则延后——主线程直执路径同样受 WaitIdle 约束（与原「主线程忙则入队」等价）
            if (route.WaitIdle && !target.Session.IsIdle)
            {
                PostInput(target, line);
                return true;
            }
            return route.Handler(target, line);
        }

        /// <summary>
        /// 投递到统一队列——主线程泵下帧重放（原六个 Pending 队列的唯一替代）。
        /// </summary>
        /// <param name="target">目标会话选择器</param>
        /// <param name="line">原始指令行</param>
        internal static void PostInput(InputTarget target, string line)
        {
            InputJob job = new InputJob();
            job.Target = target;
            job.Line = line;
            string key = target.Tag != null ? target.Tag : "";
            ConcurrentQueue<InputJob> queue;
            lock (_inputQueueLock)
            {
                if (!_inputQueues.TryGetValue(key, out queue))
                {
                    queue = new ConcurrentQueue<InputJob>();
                    _inputQueues[key] = queue;
                }
            }
            queue.Enqueue(job);
        }

        /// <summary>
        /// 泵输入队列——主线程消费（主循环与 DriveUntilIdle 每帧调用）。
        /// 按目标分组逐条消费：同目标内队首忙（会话非 Idle 且表项要求 WaitIdle）即暂存、下帧重试（先后序不被越过）；
        /// 跨目标互不阻塞（A 猫忙不挡 B 猫输入——与原「每猫独立 Pending 队列」语义等价）。
        /// </summary>
        internal static void PumpInputQueue()
        {
            List<KeyValuePair<string, ConcurrentQueue<InputJob>>> snapshot;
            lock (_inputQueueLock)
            {
                snapshot = new List<KeyValuePair<string, ConcurrentQueue<InputJob>>>(_inputQueues);
            }
            for (int i = 0; i < snapshot.Count; i = i + 1)
            {
                string key = snapshot[i].Key;
                ConcurrentQueue<InputJob> queue = snapshot[i].Value;
                InputJob held;
                if (_inputHeld.TryGetValue(key, out held))
                {
                    if (!TryRunJob(held))
                    {
                        continue;
                    }
                    _inputHeld.Remove(key);
                }
                InputJob job;
                while (queue.TryDequeue(out job))
                {
                    if (!TryRunJob(job))
                    {
                        _inputHeld[key] = job;
                        break;
                    }
                }
            }
        }

        /// <summary>
        /// 执行一条作业——主线程内重新查表并执行。
        /// </summary>
        /// <param name="job">作业</param>
        /// <returns>true=已处理（可继续下一条）；false=目标忙，本帧暂存</returns>
        private static bool TryRunJob(InputJob job)
        {
            InputRoute route = MatchRoute(job.Target, job.Line);
            if (route == null)
            {
                return true;
            }
            if (route.WaitIdle && !job.Target.Session.IsIdle)
            {
                if (!job.DeferLogged)
                {
                    job.DeferLogged = true;
                    Console.WriteLine("[CMD] 会话忙——" + job.Line + " 排队执行");
                }
                return false;
            }
            route.Handler(job.Target, job.Line);
            return true;
        }

        /// <summary>
        /// 查表——声明序即匹配序；限定项（主入口 / 本地猫控）不满足即跳过。
        /// </summary>
        /// <param name="target">目标选择器</param>
        /// <param name="line">输入行</param>
        /// <returns>命中项；未命中返回 null</returns>
        private static InputRoute MatchRoute(InputTarget target, string line)
        {
            for (int i = 0; i < InputRoutes.Length; i = i + 1)
            {
                InputRoute route = InputRoutes[i];
                if (route.MainEntryOnly && !target.MainEntry)
                {
                    continue;
                }
                if (route.LocalCatControlOnly && !target.LocalCatControl)
                {
                    continue;
                }
                if (route.Prefix)
                {
                    if (line.StartsWith(route.Key, StringComparison.Ordinal))
                    {
                        return route;
                    }
                }
                else
                {
                    if (string.Equals(line, route.Key, StringComparison.Ordinal))
                    {
                        return route;
                    }
                }
            }
            return null;
        }

        /// <summary>
        /// 对话输入——取前缀后内容投递目标会话。
        /// </summary>
        /// <param name="target">目标选择器</param>
        /// <param name="line">输入行</param>
        /// <returns>true=已受理 / false=空内容</returns>
        private static bool RouteChat(InputTarget target, string line)
        {
            string content = line.Substring(5).Trim();
            if (content.Length == 0)
            {
                return false;
            }
            target.Session.PostUserMessage(content);
            return true;
        }

        /// <summary>
        /// 本地猫控·中止——本端口会话回合中止（线程安全：内部仅置位 + 取消令牌）。
        /// </summary>
        /// <param name="target">目标选择器</param>
        /// <param name="line">输入行</param>
        /// <returns>true=已受理</returns>
        private static bool RoutePause(InputTarget target, string line)
        {
            target.Session.Pause();
            return true;
        }

        /// <summary>
        /// 本地猫控·继续——本端口会话不追加消息再发一次。
        /// </summary>
        /// <param name="target">目标选择器</param>
        /// <param name="line">输入行</param>
        /// <returns>true=已受理</returns>
        private static bool RouteContinue(InputTarget target, string line)
        {
            target.Session.Continue();
            return true;
        }

        /// <summary>
        /// 新会话——清前文 + 按目标的面重注入（WaitIdle 由泵保证）。
        /// </summary>
        /// <param name="target">目标选择器</param>
        /// <param name="line">输入行</param>
        /// <returns>true=已受理</returns>
        private static bool RouteSessionNew(InputTarget target, string line)
        {
            _chatBridge.HandleSessionNew(target.Session, target.Persona, target.InjectList, target.ToolSpecs);
            return true;
        }

        /// <summary>
        /// 会话调试指令——session clear / session count。
        /// </summary>
        /// <param name="target">目标选择器</param>
        /// <param name="line">输入行</param>
        /// <returns>true=已受理</returns>
        private static bool RouteSessionCmd(InputTarget target, string line)
        {
            _chatBridge.HandleSessionCmd(target.Session, line);
            return true;
        }

        /// <summary>
        /// Note 启动——拼接计划 + 进度推给 LLM。
        /// </summary>
        /// <param name="target">目标选择器</param>
        /// <param name="line">输入行</param>
        /// <returns>true=已受理</returns>
        private static bool RouteNoteStart(InputTarget target, string line)
        {
            target.Session.NoteStart();
            return true;
        }

        /// <summary>
        /// Note 新增——追加一步（Note 状态仅主线程触碰）。
        /// </summary>
        /// <param name="target">目标选择器</param>
        /// <param name="line">输入行</param>
        /// <returns>true=已受理</returns>
        private static bool RouteNoteAdd(InputTarget target, string line)
        {
            target.Session.NoteAdd(line.Substring(9).Trim());
            return true;
        }

        /// <summary>
        /// 会话回滚 / 分支——执行并记回执日志。
        /// </summary>
        /// <param name="target">目标选择器</param>
        /// <param name="line">输入行</param>
        /// <returns>true=已受理</returns>
        private static bool RouteSessionCmdExec(InputTarget target, string line)
        {
            LogStore.Add("CatHome4", 1, "会话指令: " + ExecuteSessionCmd(target.Session, line), "CMD", "", "", 200);
            return true;
        }

        /// <summary>
        /// 热重载——宿主级能力经注入执行体（本域不持有实现）。
        /// </summary>
        /// <param name="target">目标选择器</param>
        /// <param name="line">输入行</param>
        /// <returns>true=已受理 / false=执行体未注入</returns>
        private static bool RouteReload(InputTarget target, string line)
        {
            if (ReloadHandler == null)
            {
                return false;
            }
            Console.WriteLine("[CMD] " + ReloadHandler(line.Substring(7).Trim()));
            return true;
        }

        /// <summary>
        /// 管理指令族——统一内核执行体（前缀白名单与线程域已由分发器/入口面负责）。
        /// </summary>
        /// <param name="target">目标选择器</param>
        /// <param name="line">输入行</param>
        /// <returns>true=已受理</returns>
        private static bool RouteAdminLine(InputTarget target, string line)
        {
            string result = HandleCatCommand(line);
            LogStore.Add("CatHome4", 1, "cat 指令: " + result, "CMD");
            Console.WriteLine("[CMD] " + result);
            return true;
        }

        /// <summary>
        /// 延迟指令族——任意线程直执（调度器内部锁保护）。
        /// </summary>
        /// <param name="target">目标选择器</param>
        /// <param name="line">输入行</param>
        /// <returns>true=已受理 / false=非该族指令</returns>
        private static bool RouteDelayCommand(InputTarget target, string line)
        {
            if (DelayCommand.Handle(target.Session.Id, line))
            {
                return true;
            }
            return false;
        }
    }
}
