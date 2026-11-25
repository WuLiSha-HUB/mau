using System;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using Mau.Contracts;
using Mau.Runtime;

namespace Mau.Providers
{
    /// <summary>
    /// 狼人杀本体对接服务（A409）——WwCat 工具组的执行面实现。
    /// 🔴 本组是一次性专用工具，零配置项：地址内定（本体端口固定 8765），凭证 = 调用者猫 key（宿主注入的 catId）。
    /// 出站直接把 catId 当 X-Werewolf-Key 打给本体，由本体自行匹配座位——CH4 侧不存座位表、不存本体 Key。
    /// 猫永远不知道自己被打出去的是什么，也拿不到任何凭证（参数面与回执面均无 Key）。
    /// 🔴 零业务判断：动作合法性一律由本体裁决；本服务只做「出站 HTTP → 回执整形」。
    /// </summary>
    public sealed class WerewolfService : IWerewolfOps
    {
        /// <summary>单次请求超时（毫秒）——本体为本地进程，秒级足够。</summary>
        private const int RequestTimeoutMs = 15000;

        /// <summary>
        /// 本体地址——一次性专用工具，零配置项：内定单点（本机固定端口 8765）。
        /// 换址改此一行；不立配置实体（专用工具配参数是画蛇添足）。
        /// </summary>
        private const string Endpoint = "http://127.0.0.1:8765";

        /// <summary>共享 HTTP 客户端（静态复用，避免每请求新建）。</summary>
        private static readonly HttpClient Client = CreateClient();

        /// <summary>创建 HTTP 客户端——超时统一在此设置。</summary>
        /// <returns>客户端实例</returns>
        private static HttpClient CreateClient()
        {
            HttpClient client = new HttpClient();
            client.Timeout = TimeSpan.FromMilliseconds(RequestTimeoutMs);
            return client;
        }

        /// <summary>
        /// 当前牌面——GET /api/v1/state，由本体按凭证匹配座位后裁剪。
        /// </summary>
        /// <param name="catId">调用者猫 key（宿主注入保留键，即出站凭证）</param>
        /// <returns>视图 JSON 文本或 ERR| 文本</returns>
        public string State(string catId)
        {
            string cred = Normalize(catId);
            if (cred.Length == 0)
            {
                return "ERR|WW_NO_CALLER|调用者身份缺失（宿主未注入 catId）";
            }
            return Get(Endpoint + "/api/v1/state", cred);
        }

        /// <summary>
        /// 我是谁——GET /api/v1/whoami。
        /// </summary>
        /// <param name="catId">调用者猫 key（宿主注入保留键，即出站凭证）</param>
        /// <returns>视图 JSON 文本或 ERR| 文本</returns>
        public string WhoAmI(string catId)
        {
            string cred = Normalize(catId);
            if (cred.Length == 0)
            {
                return "ERR|WW_NO_CALLER|调用者身份缺失（宿主未注入 catId）";
            }
            return Get(Endpoint + "/api/v1/whoami", cred);
        }

        /// <summary>
        /// 增量事件——GET /api/v1/events?after=，已由本体按视角裁剪。
        /// </summary>
        /// <param name="catId">调用者猫 key（宿主注入保留键，即出站凭证）</param>
        /// <param name="after">起始序号（不含；负值归零）</param>
        /// <returns>事件数组 JSON 文本或 ERR| 文本</returns>
        public string Events(string catId, int after)
        {
            string cred = Normalize(catId);
            if (cred.Length == 0)
            {
                return "ERR|WW_NO_CALLER|调用者身份缺失（宿主未注入 catId）";
            }
            int since = after;
            if (since < 0)
            {
                since = 0;
            }
            return Get(Endpoint + "/api/v1/events?after=" + since.ToString(), cred);
        }

        /// <summary>
        /// 提交一个动作——POST /api/v1/action，合法性由本体裁决。
        /// </summary>
        /// <param name="catId">调用者猫 key（宿主注入保留键，即出站凭证）</param>
        /// <param name="action">动作名</param>
        /// <param name="targetSeat">目标座位号文本（空 = 不带 targetSeat 字段）</param>
        /// <param name="text">正文（空 = 不带 text 字段）</param>
        /// <returns>受理回执 JSON 文本或 ERR| 文本</returns>
        public string Act(string catId, string action, string targetSeat, string text)
        {
            string name = action == null ? "" : action.Trim();
            if (name.Length == 0)
            {
                return "ERR|WW_BAD_ARGS|action 为空";
            }
            string cred = Normalize(catId);
            if (cred.Length == 0)
            {
                return "ERR|WW_NO_CALLER|调用者身份缺失（宿主未注入 catId）";
            }
            StringBuilder body = new StringBuilder();
            body.Append("{\"action\":");
            body.Append(JsonUtil.Str(name));
            string seatText = targetSeat == null ? "" : targetSeat.Trim();
            if (seatText.Length > 0)
            {
                int seat;
                if (!int.TryParse(seatText, out seat))
                {
                    return "ERR|WW_BAD_ARGS|target 须为座位号: " + seatText;
                }
                body.Append(",\"targetSeat\":");
                body.Append(seat.ToString());
            }
            if (text != null && text.Length > 0)
            {
                body.Append(",\"text\":");
                body.Append(JsonUtil.Str(text));
            }
            body.Append("}");
            return Post(Endpoint + "/api/v1/action", cred, body.ToString());
        }

        /// <summary>把调用者标识归一到凭证形态（Trim；空 = 身份缺失）。</summary>
        /// <param name="catId">调用者猫 key</param>
        /// <returns>归一后的凭证文本</returns>
        private static string Normalize(string catId)
        {
            if (catId == null)
            {
                return "";
            }
            return catId.Trim();
        }

        /// <summary>
        /// GET 出站——凭证 = 调用者猫 key。
        /// </summary>
        /// <param name="url">完整地址</param>
        /// <param name="cred">出站凭证（catId）</param>
        /// <returns>响应体或 ERR| 文本</returns>
        private static string Get(string url, string cred)
        {
            try
            {
                using (HttpRequestMessage request = new HttpRequestMessage(HttpMethod.Get, url))
                {
                    request.Headers.Add("X-Werewolf-Key", cred);
                    using (HttpResponseMessage response = Client.SendAsync(request).GetAwaiter().GetResult())
                    {
                        return Shape(response);
                    }
                }
            }
            catch (Exception ex)
            {
                return "ERR|WW_UNREACHABLE|" + ex.GetType().Name + ": " + ex.Message;
            }
        }

        /// <summary>
        /// POST 出站——凭证 = 调用者猫 key。
        /// </summary>
        /// <param name="url">完整地址</param>
        /// <param name="cred">出站凭证（catId）</param>
        /// <param name="body">请求体 JSON</param>
        /// <returns>响应体或 ERR| 文本</returns>
        private static string Post(string url, string cred, string body)
        {
            try
            {
                using (HttpRequestMessage request = new HttpRequestMessage(HttpMethod.Post, url))
                {
                    request.Headers.Add("X-Werewolf-Key", cred);
                    request.Content = new StringContent(body, Encoding.UTF8, "application/json");
                    using (HttpResponseMessage response = Client.SendAsync(request).GetAwaiter().GetResult())
                    {
                        return Shape(response);
                    }
                }
            }
            catch (Exception ex)
            {
                return "ERR|WW_UNREACHABLE|" + ex.GetType().Name + ": " + ex.Message;
            }
        }

        /// <summary>
        /// 回执整形——200 直通响应体（结构化 JSON）；其余状态码解析本体错误体并直通错误码（不吞）。
        /// </summary>
        /// <param name="response">响应</param>
        /// <returns>回执文本</returns>
        private static string Shape(HttpResponseMessage response)
        {
            string body = response.Content.ReadAsStringAsync().GetAwaiter().GetResult();
            int status = (int)response.StatusCode;
            if (status == 200)
            {
                return body;
            }
            string code = "";
            string message = "";
            string parseError;
            JsonDocument doc = JsonUtil.Parse(body, "狼人杀本体", out parseError);
            if (doc != null)
            {
                using (doc)
                {
                    if (doc.RootElement.ValueKind == JsonValueKind.Object)
                    {
                        code = JsonUtil.GetStr(doc.RootElement, "code", "");
                        message = JsonUtil.GetStr(doc.RootElement, "message", "");
                    }
                }
            }
            if (code.Length == 0)
            {
                code = "HTTP_" + status.ToString();
            }
            if (message.Length == 0)
            {
                message = body == null ? "" : body.Trim();
            }
            if (message.Length == 0)
            {
                message = "本体返回 HTTP " + status.ToString();
            }
            return "ERR|WW_" + code + "|" + message;
        }
    }
}
