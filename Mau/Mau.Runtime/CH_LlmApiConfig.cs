using System;

namespace Mau.Runtime
{
    /// <summary>一份不包含 Key 明文的 LLM API 连接配置。</summary>
    public sealed class CH_LlmApiConfig
    {
        /// <summary>当前配置结构版本。</summary>
        public int SchemaVersion;

        /// <summary>稳定 API 配置身份。</summary>
        public Guid ApiConfigId;

        /// <summary>用户可见名称。</summary>
        public string DisplayName;

        /// <summary>决定 Provider 适配器的 API 类型。</summary>
        public string ApiType;

        /// <summary>绝对 API 端点。</summary>
        public string Endpoint;

        /// <summary>默认模型。</summary>
        public string DefaultModel;
        /// <summary>
        /// 前文最大长度（token 预算上限）——0=未设置；当前只做记录与展示，消费方后续定。
        /// </summary>
        public int MaxToken;
        /// <summary>
        /// 默认端点标记——true=未显式指定 API 的消费面（QuickCat 语料面）固定走此配置。
        /// </summary>
        public bool IsDefault;
        /// <summary>创建字段完整的空配置。</summary>
        public CH_LlmApiConfig()
        {
            SchemaVersion = 1;
            ApiConfigId = Guid.Empty;
            DisplayName = "";
            ApiType = "";
            Endpoint = "";
            DefaultModel = "";
            MaxToken = 0;
        }
    }
}
