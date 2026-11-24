using System;

namespace Mau.Runtime
{
    /// <summary>
    /// HTTP 注入通道配置——配置池中的一等实体（A405）。
    /// 一个通道绑定一只猫（1:1）；外部程序持通道 Key 请求即注入该猫会话，无需知道猫身份（Key 只传凭证，不传身份）。
    /// 普通字段落 channels.json；Key 明文落 secrets/channels.cfg（与 QQ 机器人池同构）。
    /// </summary>
    public sealed class CH_ChannelConfig
    {
        /// <summary>配置 schema 版本（1 = 仅注入；2 = 含下游出口字段 BotKey / Groups）。</summary>
        public int SchemaVersion = 2;

        /// <summary>通道身份（稳定主键；对外不可见——外部只持 Key）。</summary>
        public Guid ChannelId;

        /// <summary>通道名（注入来源标记名；1..32 字符，全池唯一且忽略大小写）。</summary>
        public string DisplayName = "";

        /// <summary>
        /// 绑定猫 key（注入目标，1:1）。A406 起可空——空 = 该通道不接注入（纯播报通道）。
        /// </summary>
        public string CatKey = "";

        /// <summary>下游出口机器人（配置池 qqBotId；空=该通道未接出口，只能注入）。A406。</summary>
        public string BotKey = "";

        /// <summary>
        /// 下游出口群白名单（别名 / 群名 / OpenID 混填）。A406。
        /// 🔴 空 = 禁发——与池 allowedGroups 的「空 = 不限」相反：空配置绝不等于全群可发。
        /// </summary>
        public string[] Groups = Array.Empty<string>();
    }
}
