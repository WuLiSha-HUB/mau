using System.Diagnostics;

namespace CatHome4.Contracts
{
    /// <summary>
    /// 进程服务——外部工具进程的进程树终止面（浏览器内核 / 命令行执行器共用）。
    /// <para>
    /// 平台差异只在实现类内落（<c>CatHome4.Platform/Windows</c> / <c>Linux</c>），
    /// 调用方只认本接口，不做逐调用点环境判断（design-ch4-platform §二 R3）。
    /// </para>
    /// </summary>
    public interface IProcessService
    {
        /// <summary>
        /// 终止进程树——子进程一并终止；失败出声不抛（目标进程可能已退出）。
        /// </summary>
        /// <param name="process">目标进程</param>
        /// <param name="logTag">日志标签（调用方分类：SYS / BROWSER 等）</param>
        void KillTree(Process process, string logTag);
    }
}
