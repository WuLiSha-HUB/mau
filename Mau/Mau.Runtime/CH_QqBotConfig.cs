using System;

namespace Mau.Runtime
{
    /// <summary>一份不包含 AppSecret 明文的 QQ 机器人配置。</summary>
    public sealed class CH_QqBotConfig
    {
        /// <summary>当前配置结构版本。</summary>
        public int SchemaVersion;

        /// <summary>稳定机器人配置身份。</summary>
        public Guid QqBotId;

        /// <summary>用户可见名称。</summary>
        public string DisplayName;

        /// <summary>QQ 开放平台 AppID（非 URL——不入端点校验）。</summary>
        public string AppId;

        /// <summary>接入环境——sandbox / production（两环境数据不互通）。</summary>
        public string ApiEnv;

        /// <summary>默认机器人标记——true 时未显式指定配置的消费面走此配置。</summary>
        public bool IsDefault;

        /// <summary>允许主动发言的群（别名或 group_openid；空数组=不限制，全部目录内群可发）。</summary>
        public string[] AllowedGroups;

        /// <summary>创建字段完整的空配置。</summary>
        public CH_QqBotConfig()
        {
            SchemaVersion = 1;
            QqBotId = Guid.Empty;
            DisplayName = "";
            AppId = "";
            ApiEnv = "";
            AllowedGroups = new string[0];
        }
    }
}
