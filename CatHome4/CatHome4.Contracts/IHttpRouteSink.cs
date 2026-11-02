using System;

namespace CatHome4.Contracts
{
    /// <summary>
    /// HTTP 路由注册面——外观层向其他域开放管理 API 路由注册。
    /// Http 域实现（HttpHost Minimal API 表），入口壳/Admin 域使用（注册 llm-apis/cat-config/workspace 等路由）。
    /// 拆分前 HttpHost 直接绑定 Program.HandleXxx 委托；拆分后经本接口注册，Http 域零依赖 Admin 域。
    /// </summary>
    public interface IHttpRouteSink
    {
        /// <summary>注册 GET 路由</summary>
        /// <param name="pattern">路由模板（如 /api/v1/llm-apis）</param>
        /// <param name="handler">处理委托（Minimal API Delegate）</param>
        void MapGet(string pattern, Delegate handler);

        /// <summary>注册 POST 路由</summary>
        /// <param name="pattern">路由模板</param>
        /// <param name="handler">处理委托（Minimal API Delegate）</param>
        void MapPost(string pattern, Delegate handler);
    }
}
