using CatHome4.Contracts;

namespace CH4
{
    /// <summary>
    /// 宿主接入面——内核与出口（桌面 / headless）的静态交汇点（design-ch4-platform §一 R2：双出口同进程托管内核，零 IPC）。
    /// 内核经 <see cref="Shell"/> 调外壳能力；外壳经 <see cref="View"/> 读内核持有的运行量。
    /// 写入权归 <see cref="HostEntry"/>——出口只读。
    /// </summary>
    public static class HostAccess
    {
        /// <summary>外壳桥——启动时由 HostEntry 设置（内核运行期只读使用）。</summary>
        public static IHostShell Shell { get; internal set; }

        /// <summary>宿主状态读面——启动时由 HostEntry 设置（外壳读端口 / 版本 / 自启开关）。</summary>
        public static IHostView View { get; internal set; }
    }
}
