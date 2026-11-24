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
        /// <summary>配置 schema 版本（现行 1）。</summary>
        public int SchemaVersion = 1;

        /// <summary>通道身份（稳定主键；对外不可见——外部只持 Key）。</summary>
        public Guid ChannelId;

        /// <summary>通道名（注入来源标记名；1..32 字符，全池唯一且忽略大小写）。</summary>
        public string DisplayName = "";

        /// <summary>绑定猫 key（注入目标固定为该会话，1:1）。</summary>
        public string CatKey = "";
    }
}
