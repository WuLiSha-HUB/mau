namespace Mau.Runtime
{
    /// <summary>
    /// 命令行服务——Linux 侧的单行指令执行面（<c>sh-exec</c> 的内核侧契约）。
    /// <para>
    /// 与 <see cref="IPsService"/> 是两条契约、不共享参数面：ps 的 <c>-EncodedCommand</c> / <c>chcp 65001</c> /
    /// PSDrive 根注入 / 多层转义全是 Windows 特化，混进 bash 会变成两套半实现（design-ch4-platform §五）。
    /// Windows 侧不注册本服务（<see cref="Available"/> = false，工具定义按平台过滤后不入池）。
    /// </para>
    /// <para>
    /// 落点与 <see cref="IPsService"/> 一致（Mau.Runtime）而非 CatHome4.Contracts：消费方 <c>sh.exec</c> 积木
    /// 运行在组翻译产物（Flow dll）内，引用面只有基座程序集——CH4 侧合约层对它不可达（Q1=B 的同一判据）。
    /// </para>
    /// </summary>
    public interface IShellService
    {
        /// <summary>
        /// 当前环境是否提供命令行服务——Windows 侧恒 false（走 <c>powershell</c> 双线）。
        /// </summary>
        bool Available { get; }

        /// <summary>
        /// 执行单行指令——单行纪律与 ps 面同源（禁代码形态 / 多段 / 管道 / 块 / 过程语句）。
        /// </summary>
        /// <param name="command">命令文本</param>
        /// <param name="timeoutMs">超时毫秒（超时即终止进程树）</param>
        /// <returns>结果 JSON（exit / stdoutLines / stderrLines / truncated / timeout）或 <c>ERR|</c> 前缀错误文本</returns>
        string Execute(string command, int timeoutMs);
    }
}
