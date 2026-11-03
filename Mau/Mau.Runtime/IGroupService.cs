namespace Mau.Runtime
{
    /// <summary>
    /// 群组服务——Group 工具面（group.info / group.inbox / group.say / group.send）的宿主通道。
    /// 宿主注入实现（CatHome4.Admin.GroupService）；语料经 GROUP 类别积木触达（DataBox.TryResolve 面）。
    /// 主线程契约：投递触碰会话面与组存储（ThreadGuard 守卫），须宿主主线程调用。
    /// 分层：本接口只声明「宿主有群组面」——权限与业务语义由宿主实现承载，Runtime 不认识。
    /// </summary>
    public interface IGroupService
    {
        /// <summary>组信息——组行 JSON（成员 / 管理员 / 统计）。</summary>
        /// <param name="argsJson">工具参数 JSON（group 必填；catId 为宿主注入保留键）</param>
        /// <returns>JSON 文本；失败为 ERR| 前缀</returns>
        string Info(string argsJson);

        /// <summary>拉取消息——按 after / limit 取组内消息流。</summary>
        /// <param name="argsJson">工具参数 JSON（group 必填；after / limit 可选）</param>
        /// <returns>JSON 文本；失败为 ERR| 前缀</returns>
        string Inbox(string argsJson);

        /// <summary>发讨论消息（note）——只写组消息流，不投递。</summary>
        /// <param name="argsJson">工具参数 JSON（group / text 必填；to 可选）</param>
        /// <returns>JSON 文本；失败为 ERR| 前缀</returns>
        string Say(string argsJson);

        /// <summary>派单（delegation）——写组消息 + 以 origin 投递目标猫（同一方法内完成）。</summary>
        /// <param name="argsJson">工具参数 JSON（group / to / text 必填；refs 可选）</param>
        /// <returns>JSON 文本；失败为 ERR| 前缀</returns>
        string Send(string argsJson);
    }
}
