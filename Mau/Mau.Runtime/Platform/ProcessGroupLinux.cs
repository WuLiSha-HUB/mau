using System.Diagnostics;
using Mau.Contracts;

namespace Mau.Runtime
{
    /// <summary>
    /// 进程组宿主·Linux 实现（A358 定案）——<b>明示不支持</b>，组生命周期由 systemd 与逐服务强杀承担。
    /// <para>
    /// Linux 侧没有 Job 对象；等价形态是「子进程自成进程组（<c>setsid</c>）+ 宿主退出时 <c>kill(-pgid)</c>」。
    /// 子进程落在哪个进程组由<b>启动侧</b>决定——目前 py / node 服务按原命令直接启动，pgid 等于宿主进程组，
    /// 此时组杀会连带杀死宿主自身（<c>kill(-pgid)</c> 会命中自身），因此不做组杀。
    /// </para>
    /// <para>
    /// 承接面（两条，均已落地）：
    /// ① <b>宿主退出连带终止</b>——systemd user unit 的 <c>KillMode=control-group</c>（见
    /// <c>CatHome4.Platform.Linux.LinuxAutoStartService</c>），覆盖前台常驻形态；
    /// ② <b>单服务终止</b>——<c>IProcessService.KillTree</c> 的逐 pid 子树终止（BCL 跨平台实现），覆盖工具面显式停止。
    /// </para>
    /// <para>
    /// <see cref="Available"/> = false 语义与旧版「非 Windows 返回 0 句柄」逐面等价：调用方走自身强杀兜底。
    /// 若后续要求「宿主被 SIGKILL 时子进程必死」且不经 systemd（如手工前台运行），正解是启动侧
    /// <c>setsid</c> + 组杀，属基座启动方式变更，另立项。
    /// </para>
    /// </summary>
    public sealed class ProcessGroupLinux : IProcessGroupHost
    {
        /// <summary>
        /// 当前环境是否支持进程组强杀——Linux 恒 false（承接面见类注释）。
        /// </summary>
        public bool Available
        {
            get
            {
                return false;
            }
        }

        /// <summary>
        /// 建立进程组宿主——不支持，返回 null（调用方据此走自身强杀兜底）。
        /// </summary>
        /// <param name="logTag">日志标签（未使用）</param>
        /// <returns>null</returns>
        public object? CreateGroup(string logTag)
        {
            return null;
        }

        /// <summary>
        /// 把进程加入组——不支持，恒 false（无组可加入）。
        /// </summary>
        /// <param name="group">组句柄（恒为 null）</param>
        /// <param name="target">目标进程（未使用）</param>
        /// <returns>false</returns>
        public bool Attach(object? group, Process target)
        {
            return false;
        }
    }
}
