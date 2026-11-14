using Mau.Runtime;

namespace CatHome4.Platform.Windows
{
    /// <summary>
    /// 命令行服务·Windows 实现——不提供（Windows 侧走 <c>powershell</c> / <c>powershell7</c> 双线）。
    /// <para>
    /// <see cref="Available"/> 恒 false：调用方据此不注册 <c>sh-exec</c> 工具（平台过滤单点在工具池注册）。
    /// </para>
    /// </summary>
    public sealed class WindowsShellService : IShellService
    {
        /// <summary>
        /// 当前环境是否提供命令行服务——Windows 恒 false。
        /// </summary>
        public bool Available
        {
            get
            {
                return false;
            }
        }

        /// <summary>
        /// 执行单行指令——Windows 侧不可用，返回具名错误（不静默回落 ps 面）。
        /// </summary>
        /// <param name="command">命令文本（未使用）</param>
        /// <param name="timeoutMs">超时毫秒（未使用）</param>
        /// <returns><c>ERR|SH_NOT_AVAILABLE|…</c></returns>
        public string Execute(string command, int timeoutMs)
        {
            return "ERR|SH_NOT_AVAILABLE|Windows 侧不提供 sh-exec——请改用 powershell / powershell7";
        }
    }
}
