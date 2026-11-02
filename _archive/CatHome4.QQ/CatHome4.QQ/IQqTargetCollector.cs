using System;
using System.Collections.Generic;

namespace CatHome4.QQ
{
    /// <summary>
    /// qqbot 绑定目标收集面——QQ 域消费（输入路由注入 + 输出转发轮询），入口壳实现（默认猫 + 多猫注册表）。
    /// S3 解耦：QQBotService 不再直接引用 Program 静态面——经本接口注入获取目标。
    /// A58——绑定关系 1:1：一个 Bot 唯一对应一只猫（CollectByBot 最多返回一项；重复配置取首个 + L2 留痕）。
    /// </summary>
    public interface IQqTargetCollector
    {
        /// <summary>取绑定指定 qqbot 的猫（1:1——最多一项；空=未绑定任何猫）</summary>
        /// <param name="qqBotId">qqbot 配置身份</param>
        /// <returns>绑定目标列表（≤1 项）</returns>
        List<QqTarget> CollectByBot(Guid qqBotId);

        /// <summary>收集全部启用转发的猫——每猫各自的 Bot（多猫各自转发；1:1 约束在 Bot 侧）</summary>
        /// <returns>全部绑定目标（QqBotId 非空）</returns>
        List<QqTarget> CollectAll();
    }
}
