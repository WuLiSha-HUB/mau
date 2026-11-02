using System;
using System.Collections.Generic;
using System.Net;
using System.Net.Sockets;
using System.Threading.Tasks;
using CatHome4.Contracts;
using CatHome4.Http;
using CH4;
using Mau.Runtime;
using Microsoft.AspNetCore.Http;
using static CatHome4.Admin.AdminService;

namespace CatHome4.Admin
{
    /// <summary>
    /// 群聊宿主（A301 · 每组一端口）——每个组独占一个 HttpHost：端口、路由面、推送面各自独立。
    /// 组停用即停宿主（端口释放、通道关闭），与每猫会话机制同构；关组释放的端口可被新组复用。
    /// 端口从 9000 起向上寻路（9000–9099；与 PortBand 族段不相交）。
    /// </summary>
    internal static partial class CatHostService
    {
        /// <summary>组端口起点——向上寻路</summary>
        private const int GroupPortFrom = 9000;

        /// <summary>组端口终点（含）——全占即启动失败</summary>
        private const int GroupPortTo = 9099;

        /// <summary>组宿主表——组名 → HttpHost（仅运行中的组在表内）</summary>
        private static readonly Dictionary<string, HttpHost> GroupHosts = new Dictionary<string, HttpHost>(StringComparer.OrdinalIgnoreCase);

        /// <summary>组端口表——组名 → 监听端口（与 GroupHosts 同生同灭）</summary>
        private static readonly Dictionary<string, int> GroupPorts = new Dictionary<string, int>(StringComparer.OrdinalIgnoreCase);

        /// <summary>组宿主锁——起停与端口分配串行</summary>
        private static readonly object GroupHostLock = new object();

        /// <summary>
        /// 组端口查询——0 = 该组未运行。
        /// </summary>
        /// <param name="name">组名</param>
        /// <returns>端口</returns>
        internal static int GroupHostPort(string name)
        {
            if (name == null)
            {
                return 0;
            }
            lock (GroupHostLock)
            {
                int port;
                if (GroupPorts.TryGetValue(name, out port))
                {
                    return port;
                }
                return 0;
            }
        }

        /// <summary>
        /// 组宿主是否在运行。
        /// </summary>
        /// <param name="name">组名</param>
        /// <returns>true=运行中</returns>
        internal static bool GroupHostRunning(string name)
        {
            if (name == null)
            {
                return false;
            }
            lock (GroupHostLock)
            {
                return GroupHosts.ContainsKey(name);
            }
        }

        /// <summary>
        /// 端口分配——9000 起向上试绑（跳过组端口表已占者；试绑即释放）。
        /// </summary>
        /// <returns>可用端口；9000-9099 全占用 -1</returns>
        private static int AllocateGroupPortLocked()
        {
            for (int port = GroupPortFrom; port <= GroupPortTo; port = port + 1)
            {
                bool taken = false;
                foreach (KeyValuePair<string, int> kv in GroupPorts)
                {
                    if (kv.Value == port)
                    {
                        taken = true;
                        break;
                    }
                }
                if (taken)
                {
                    continue;
                }
                bool ok = false;
                try
                {
                    TcpListener listener = new TcpListener(IPAddress.Loopback, port);
                    listener.Start();
                    listener.Stop();
                    ok = true;
                }
                catch (Exception)
                {
                    // 试绑失败 = 端口被外部占用，继续下一端口
                    ok = false;
                }
                if (ok)
                {
                    return port;
                }
            }
            return -1;
        }

        /// <summary>
        /// 启动组宿主——独占端口 + group.html + 本组运行时端点；已运行则幂等返回。
        /// </summary>
        /// <param name="name">组名</param>
        /// <returns>是否成功启动</returns>
        internal static bool StartGroupHost(string name)
        {
            if (name == null || name.Length == 0)
            {
                return false;
            }
            GroupSummary group = GroupStore.GetGroup(name);
            if (group == null)
            {
                LogStore.Add("CatHome4", 2, "组宿主启动失败：组不存在 " + name, "GROUP");
                return false;
            }
            lock (GroupHostLock)
            {
                if (GroupHosts.ContainsKey(group.Name))
                {
                    return true;
                }
                int port = AllocateGroupPortLocked();
                if (port < 0)
                {
                    LogStore.Add("CatHome4", 2, "组宿主启动失败：端口段 " + GroupPortFrom.ToString() + "-" + GroupPortTo.ToString() + " 全占用（组 " + group.Name + "）", "GROUP");
                    StartupDegradation.Report("GROUP_PORT_EXHAUSTED", StartupDegradation.KindEnv, "群聊宿主（组 " + group.Name + "）",
                        StartupDegradation.StateUnavailable,
                        "端口段 " + GroupPortFrom.ToString() + "-" + GroupPortTo.ToString() + " 全占用",
                        "关闭占用端口的进程后重启", "S7");
                    return false;
                }
                HttpHost host;
                try
                {
                    string boundName = group.Name;
                    host = HttpHost.Start(new HttpHostOptions
                    {
                        Port = port,
                        FrontendTestPort = PortBand.FrontendTestPort,
                        SessionId = "group:" + boundName,
                        CatsBuilder = BuildCatsJson,
                        ServeChatPage = false,
                        StaticPage = "group.html",
                        RouteRegistrar = delegate (IHttpRouteSink sink) { RegisterGroupRoutes(sink, boundName); },
                        HtmlRootProvider = HtmlRoot,
                        DisplayName = "群聊 · " + boundName
                    });
                }
                catch (Exception ex)
                {
                    LogStore.Add("CatHome4", 2, "组宿主启动失败（组 " + group.Name + "）：" + ex.Message, "GROUP");
                    StartupDegradation.Report("GROUP_HOST_FAILED", StartupDegradation.KindEnv, "群聊宿主（组 " + group.Name + "）",
                        StartupDegradation.StateUnavailable, "独立端口 " + port.ToString() + " 启动失败：" + ex.Message,
                        "关闭占用端口的进程后重启", "S7");
                    return false;
                }
                GroupHosts[group.Name] = host;
                GroupPorts[group.Name] = port;
                LogStore.Add("CatHome4", 1, "组宿主已启动：" + group.Name + " | 端口 " + port.ToString(), "GROUP");
                return true;
            }
        }

        /// <summary>
        /// 停止组宿主——停 Kestrel（释放端口）+ 关通道（推送一并停止）；未运行则无操作。
        /// </summary>
        /// <param name="name">组名</param>
        internal static void StopGroupHost(string name)
        {
            if (name == null || name.Length == 0)
            {
                return;
            }
            HttpHost host = null;
            int port = 0;
            lock (GroupHostLock)
            {
                if (GroupHosts.TryGetValue(name, out host))
                {
                    GroupHosts.Remove(name);
                }
                if (GroupPorts.TryGetValue(name, out port))
                {
                    GroupPorts.Remove(name);
                }
            }
            if (host == null)
            {
                return;
            }
            try
            {
                host.Stop();
            }
            catch (Exception ex)
            {
                LogStore.Add("CatHome4", 2, "组宿主停止异常（组 " + name + "）：" + ex.Message, "GROUP");
            }
            LogStore.Add("CatHome4", 1, "组宿主已停止：" + name + " | 端口 " + port.ToString() + " 已释放", "GROUP");
        }

        /// <summary>
        /// 启动期批量拉起——enabled 的组各起一个宿主（组停用 = 不起；与每猫 running 同构）。
        /// </summary>
        internal static void StartEnabledGroupHosts()
        {
            List<GroupSummary> groups = GroupStore.ListGroups();
            int started = 0;
            for (int i = 0; i < groups.Count; i = i + 1)
            {
                if (!groups[i].Enabled)
                {
                    continue;
                }
                if (StartGroupHost(groups[i].Name))
                {
                    started = started + 1;
                }
            }
            if (started > 0)
            {
                LogStore.Add("CatHome4", 1, "组宿主批量拉起：" + started.ToString() + " 组", "GROUP");
            }
        }

        /// <summary>
        /// 本组运行时路由——端口即组身份：组名由闭包绑定，不接受跨组参数（各自走各自通道）。
        /// </summary>
        /// <param name="sink">HTTP 路由注册面</param>
        /// <param name="name">本端口所属组名</param>
        private static void RegisterGroupRoutes(IHttpRouteSink sink, string name)
        {
            // [段1] 组信息与消息流
            sink.MapGet("/api/v1/group/info", (Delegate)((Func<IResult>)(() => BuildGroupInfoResult(name))));
            sink.MapGet("/api/v1/group/messages", (Delegate)((Func<HttpContext, IResult>)((HttpContext c) => BuildGroupMessagesResult(name, c))));
            // [段2] 投递与已读推进（组名由端口决定——投递不接受 group 参数）
            sink.MapPost("/api/v1/group/messages", (Delegate)((Func<HttpContext, Task<IResult>>)((HttpContext c) => BuildGroupMessageAppend(name, c))));
            sink.MapPost("/api/v1/group/read", (Delegate)((Func<HttpContext, Task<IResult>>)((HttpContext c) => BuildGroupRead(name, c))));
        }
    }
}
