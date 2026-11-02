using System;
using System.Diagnostics;
using System.IO;
using System.Net.WebSockets;
using System.Text;
using System.Text.Json;
using System.Threading;
using Mau.Runtime;

namespace CatHome4.QQ
{
    /// <summary>
    /// 单 QQ Bot WS 连接——CH1 CatQQBot.cs 协议资产移植（R2.3.3）。
    /// 鉴权（appId+secret → access_token）/ WS 全 opcode / 心跳 ACK 超时检测 / 重连+Resume。
    /// 消息到达经 OnMessage 回调管理器（路由层）；发送经 SendReply（管理器调用）。
    ///
    /// 【QQ Bot WebSocket 协议（核心 opcode，2026 官方文档校验）】
    ///   op=0  Dispatch — 服务端推送事件（含 t 事件类型 + s 序列号）
    ///   op=1  Heartbeat — 客户端心跳，d 字段携带最后处理的 s 值
    ///   op=2  Identify — 首次鉴权 / 全新连接
    ///   op=6  Resume — 断线重连恢复会话（携带 session_id + seq）
    ///   op=7  Reconnect — 服务端要求重连
    ///   op=9  Invalid Session — 鉴权/resume 参数有误（d.code: 4006=无效session, 4009=连接过期可重试）
    ///   op=10 Hello — 连接建立后第一条消息（heartbeat_interval + session_id）
    ///   op=11 Heartbeat ACK — 服务端对心跳的回应，不触发重连
    ///
    /// 【重连+Resume 策略】
    ///   断线 → 连接 WS → Hello → 若有 _sessionId:
    ///     发 op=6 Resume(token + session_id + seq)
    ///       → 成功: 收到 op=0 t=RESUMED → 正常收发
    ///       → 失败4006: 清 _sessionId → 同连接内回落 op=2 Identify
    ///       → 失败4009: 重试 op=6（连接过期但可恢复）
    ///     若无 _sessionId / resume 失败:
    ///       发 op=2 Identify → 收到 op=0 t=READY → 正常收发
    ///
    /// 【心跳 ACK 超时检测】
    ///   连续 2 次心跳未收到 op=11 → HbLoop 主动 Dispose WebSocket → 触发重连。
    ///   避免 TCP 半开连接时 ReceiveAsync 超时前（~92s）的长盲区。
    /// </summary>
    internal sealed class QQBotConnection
    {
        /// <summary>
        /// op=9 错误码——session_id 无效，须全新鉴权
        /// </summary>
        // op=9 错误码
        private const int ERR_INVALID_SESSION = 4006;  // session_id 无效，须全新鉴权
        /// <summary>
        /// op=9 错误码——连接过期，可重试 resume
        /// </summary>
        private const int ERR_EXPIRED = 4009;          // 连接过期，可重试 resume
        /// <summary>
        /// 心跳 ACK 超时阈值——连续无 ACK 次数超限触发重连
        /// </summary>

        // 心跳 ACK 超时：连续多少次无 ACK 后触发重连
        private const int MaxMissedAcks = 2;

        /// <summary>file_data 整传上限——10MB（P9 莎拍板：分片暂不做，超限拒绝）</summary>
        private const long MaxFileDataBytes = 10L * 1024 * 1024;

        /// <summary>Bot 配置身份——Bot 池条目引用</summary>
        private readonly Guid _qqBotId;

        /// <summary>Bot 显示名——日志标识</summary>
        private readonly string _displayName;

        /// <summary>QQ 开放平台 Bot 应用 ID</summary>
        private readonly string _appId;

        /// <summary>Bot 应用 Secret——Bot 池 secrets 读取</summary>
        private readonly string _secret;

        /// <summary>沙箱标志——true=沙箱端点 / false=正式端点</summary>
        private readonly bool _sandbox;

        /// <summary>消息到达回调——管理器路由层（raw JSON）</summary>
        private readonly Action<string> _onMessage;

        /// <summary>被动回复消息序号——每次发送递增（msg_seq 字段：同 msg_id 多次回复须递增，否则 40054005 去重）</summary>
        private long _msgSeq;

        /// <summary>API 主机——沙箱/正式双端点</summary>
        private readonly string _apiHost;

        /// <summary>访问令牌——鉴权产物</summary>
        private string _accessToken = "";

        /// <summary>令牌过期时刻——Stopwatch 时间戳</summary>
        private long _tokenExpireTick = 0;

        /// <summary>WS 连接实例</summary>
        private ClientWebSocket _ws = null;

        /// <summary>WS 线程——后台常驻</summary>
        private Thread _wsThread = null;

        /// <summary>WS 运行标志——Stop 置 false 退出循环</summary>
        private bool _wsRun = false;

        /// <summary>会话恢复——session_id</summary>
        private string _sessionId = "";

        /// <summary>会话恢复——最后处理序列号</summary>
        private long _lastSeq = 0;

        /// <summary>重连计数——日志标识</summary>
        private int _reconnectCount = 0;

        /// <summary>心跳 ACK 追踪——HbLoop 发送后递增，WsLoop 收到 op=11 时清零（volatile——跨线程共享，R6-P3-05）</summary>
        private volatile int _missedAcks = 0;
        /// <summary>共享 HTTP 客户端——连接池复用（R6-P3-02）；Authorization 走每请求头（DefaultRequestHeaders 并发不安全）</summary>
        private static readonly System.Net.Http.HttpClient _http = new System.Net.Http.HttpClient();

        /// <summary>
        /// 构造单 Bot 连接。
        /// </summary>
        /// <param name="qqBotId">Bot 配置身份</param>
        /// <param name="displayName">Bot 显示名</param>
        /// <param name="appId">QQ 开放平台 Bot 应用 ID</param>
        /// <param name="secret">Bot 应用 Secret</param>
        /// <param name="sandbox">沙箱标志</param>
        /// <param name="onMessage">消息到达回调（raw JSON）</param>
        public QQBotConnection(Guid qqBotId, string displayName, string appId, string secret, bool sandbox, Action<string> onMessage)
        {
            _qqBotId = qqBotId;
            _displayName = displayName;
            _appId = appId;
            _secret = secret;
            _sandbox = sandbox;
            _onMessage = onMessage;
            _apiHost = _sandbox
                ? "https://sandbox.api.sgroup.qq.com"
                : "https://api.sgroup.qq.com";
        }

        /// <summary>显示名——连接配置面（Refresh 配置变更检测）</summary>
        public string DisplayName
        {
            get
            {
                return _displayName;
            }
        }

        /// <summary>应用 ID——连接配置面（Refresh 配置变更检测）</summary>
        public string AppId
        {
            get
            {
                return _appId;
            }
        }

        /// <summary>沙箱标志——连接配置面（Refresh 配置变更检测）</summary>
        public bool Sandbox
        {
            get
            {
                return _sandbox;
            }
        }
        /// <summary>msg_seq 水位——转发态续接面（A33：重启后续号，避免同 msg_id+seq 被官方去重拒 40054005）</summary>
        public long MsgSeq
        {
            get { return _msgSeq; }
            set { _msgSeq = value; }
        }

        /// <summary>
        /// 启动连接——刷新 Token + 启动 WS 线程。
        /// </summary>
        public void Start()
        {
            if (_wsRun)
            {
                return;
            }
            RefreshToken();
            _wsRun = true;
            _wsThread = new Thread(WsLoop);
            _wsThread.IsBackground = true;
            _wsThread.Start();
            Log("QQBot | " + _displayName + " | 启动 " + (_sandbox ? "[沙箱]" : "[正式]") + " AppID=" + Truncate(_appId, 8), 1);
        }

        /// <summary>
        /// 停止连接——置运行标志 false + 关闭 WS。
        /// </summary>
        public void Stop()
        {
            _wsRun = false;
            try
            {
                if (_ws != null)
                {
                    _ws.CloseAsync(WebSocketCloseStatus.NormalClosure, "", CancellationToken.None).GetAwaiter().GetResult();
                }
            }
            catch (Exception ex)
            {
                Log("QQBot | " + _displayName + " | WS 关闭异常（停止链路继续）: " + ex.Message, 2);
            }
            try
            {
                if (_ws != null)
                {
                    _ws.Dispose();
                }
            }
            catch (Exception ex)
            {
                Log("QQBot | " + _displayName + " | WS 释放异常（停止链路继续）: " + ex.Message, 2);
            }
            _ws = null;
            Log("QQBot | " + _displayName + " | 已停止", 1);
        }

        /// <summary>
        /// 发送回复——私聊 / 群聊（管理器调用；401 自动刷新重试一次）。
        /// P8：msgId=被动回复引用（事件 d.id，空=主动消息）；isMarkdown=true=MD 通道（msg_type=2）。
        /// </summary>
        /// <param name="msgType">消息类型——private / group</param>
        /// <param name="targetId">目标 ID（群聊含 gid:mid 段）</param>
        /// <param name="text">回复文本</param>
        /// <param name="msgId">被动回复 msg_id——事件 d.id；空=不携带</param>
        /// <param name="isMarkdown">true=Markdown 通道 / false=文本通道</param>
        /// <returns>发送成功 true（≥300 或异常 false）</returns>
        public bool SendReply(string msgType, string targetId, string text, string msgId, bool isMarkdown)
        {
            string ignored = "";
            return SendReplyWithCode(msgType, targetId, text, msgId, isMarkdown, out ignored);
        }

        /// <summary>
        /// 发送回复并回传错误码（A217）——失败时解析响应体 code 字段（窗口超时 40034005 判定用）。
        /// </summary>
        /// <param name="msgType">消息类型——private / group</param>
        /// <param name="targetId">目标 ID（群聊含 gid:mid 段）</param>
        /// <param name="text">回复文本</param>
        /// <param name="msgId">被动回复 msg_id——事件 d.id；空=不携带</param>
        /// <param name="isMarkdown">true=Markdown 通道 / false=文本通道</param>
        /// <param name="errCode">出参——失败时代码串（成功 / 无 code 为空串）</param>
        /// <returns>发送成功 true（≥300 或异常 false）</returns>
        public bool SendReplyWithCode(string msgType, string targetId, string text, string msgId, bool isMarkdown, out string errCode)
        {
            errCode = "";
            try
            {
                // 端点按消息类型分派（与文件发送共用 BuildScope——单一真相源）
                string url = _apiHost + BuildScope(msgType, targetId) + "/messages";
                _msgSeq = _msgSeq + 1;
                string json = BuildBody(text, isMarkdown, msgId, _msgSeq);
                System.Net.Http.HttpResponseMessage r = PostJson(url, json, true);
                int status = (int)r.StatusCode;
                if (status >= 300)
                {
                    // 转发失败 L2 留痕——响应体随附（定位 400 精确错误码：被动回复次数/时效/格式；\n 替换空格——LogStore 单行不截断）
                    string errBody = "";
                    try
                    {
                        errBody = r.Content.ReadAsStringAsync().GetAwaiter().GetResult();
                    }
                    catch (Exception ex)
                    {
                        Log("QQBot | " + _displayName + " | 错误响应体读取失败: " + ex.Message, 2);
                    }
                    errCode = ExtractErrorCode(errBody);
                    Log("QQBot | " + _displayName + " | 发: " + msgType + ":" + targetId + " ← " + Truncate(OneLine(text), 30) + " (" + status + ") " + Truncate(OneLine(errBody), 200), 2);
                    return false;
                }
                Log("QQBot | " + _displayName + " | 发: " + msgType + ":" + targetId + " ← " + Truncate(OneLine(text), 30) + " (" + status + ")", 1);
                return true;
            }
            catch (Exception e)
            {
                Log("QQBot | " + _displayName + " | 发送失败: " + e.Message, 2);
                return false;
            }
        }

        /// <summary>
        /// 提取错误码——响应体 code 字段（失败响应如 {"code":40034005,...}）；解析失败返回空串。
        /// </summary>
        /// <param name="body">响应体</param>
        /// <returns>代码串（无 code / 解析失败 = 空串）</returns>
        private static string ExtractErrorCode(string body)
        {
            if (body == null || body.Length == 0)
            {
                return "";
            }
            try
            {
                using (JsonDocument d = JsonUtil.ParseStrict(body))
                {
                    if (d.RootElement.TryGetProperty("code", out JsonElement c))
                    {
                        if (c.ValueKind == JsonValueKind.Number)
                        {
                            return c.GetInt64().ToString();
                        }
                        return c.GetString() ?? "";
                    }
                }
            }
            catch (Exception)
            {
                return "";
            }
            return "";
        }
        /// <summary>
        /// 目标端点段构造——按消息类型分派（私聊 /v2/users/{openid} · 群聊 /v2/groups/{gid}）。
        /// 群聊 targetId 形如 `gid:mid`，取冒号前的 gid 段。文本与文件发送共用本实现（单一真相源）。
        /// </summary>
        /// <param name="msgType">消息类型——private / group</param>
        /// <param name="targetId">目标 ID——私聊 user_openid / 群聊 gid:mid</param>
        /// <returns>端点前缀（含 /v2 段，不含末尾 /messages 或 /files）</returns>
        internal static string BuildScope(string msgType, string targetId)
        {
            if (msgType == "private")
            {
                return "/v2/users/" + targetId;
            }
            int ci = targetId.IndexOf(':');
            string gid = ci > 0 ? targetId.Substring(0, ci) : targetId;
            return "/v2/groups/" + gid;
        }
        /// <summary>
        /// 构造发送请求体——文本 / Markdown 双通道 + 可选被动 msg_id（P8）。
        /// 官方互斥铁律：填写 markdown 后 content 必须为空。
        /// </summary>
        /// <param name="text">消息文本</param>
        /// <param name="isMarkdown">true=MD 通道</param>
        /// <param name="msgId">被动回复 msg_id——空=不携带</param>
        /// <param name="msgSeq">客户端消息序号——被动回复须递增（同 msg_id 多次回复去重校验；空 msg_id 忽略）</param>
        /// <returns>请求体 JSON</returns>
        private static string BuildBody(string text, bool isMarkdown, string msgId, long msgSeq)
        {
            string idPart = "";
            if (msgId.Length > 0)
            {
                idPart = ",\"msg_id\":" + JsonUtil.Str(msgId) + ",\"msg_seq\":" + msgSeq.ToString();
            }
            if (isMarkdown)
            {
                return "{\"msg_type\":2,\"markdown\":{\"content\":" + JsonUtil.Str(text) + "}" + idPart + "}";
            }
            return "{\"content\":" + JsonUtil.Str(text) + ",\"msg_type\":0" + idPart + "}";
        }

        /// <summary>
        /// 下载附件到本地——URL 带时效签名尽快下载；403 自动带 Authorization 重试一次（P9 接收）。
        /// </summary>
        /// <param name="url">附件下载 URL</param>
        /// <param name="destPath">落盘路径</param>
        /// <returns>下载成功 true</returns>
        public bool DownloadAttachment(string url, string destPath)
        {
            try
            {
                System.Net.Http.HttpResponseMessage r = SendGet(url, false);
                if ((int)r.StatusCode == 403)
                {
                    r = SendGet(url, true);
                }
                if (!r.IsSuccessStatusCode)
                {
                    Log("QQBot | " + _displayName + " | 附件下载失败: " + (int)r.StatusCode + " | " + destPath, 2);
                    return false;
                }
                byte[] data = r.Content.ReadAsByteArrayAsync().GetAwaiter().GetResult();
                File.WriteAllBytes(destPath, data);
                return true;
            }
            catch (Exception e)
            {
                Log("QQBot | " + _displayName + " | 附件下载异常: " + e.Message, 2);
                return false;
            }
        }

        /// <summary>
        /// 发送本地文件——≤10MB file_data 上传 → msg_type=7 发送（带被动 msg_id + msg_seq；A215 私聊 / 群聊双端点）。
        /// </summary>
        /// <param name="msgType">消息类型——private / group（决定端点 /v2/users 或 /v2/groups）</param>
        /// <param name="targetId">目标 ID——私聊 user_openid / 群聊 gid:mid（取 gid 段）</param>
        /// <param name="path">本地文件路径</param>
        /// <param name="msgId">被动回复 msg_id——空=不携带</param>
        /// <returns>成功返回空串；失败返回错误文本（由调用方嵌入消息渠道）</returns>
        public string SendFile(string msgType, string targetId, string path, string msgId)
        {
            try
            {
                if (!File.Exists(path))
                {
                    return "文件发送失败: 路径不存在";
                }
                FileInfo fi = new FileInfo(path);
                if (fi.Length <= 0)
                {
                    return "文件发送失败: 空文件";
                }
                if (fi.Length > MaxFileDataBytes)
                {
                    return "文件发送失败: 超过 10MB 上传上限";
                }
                // [段1] 端点按消息类型分派（与文本发送共用 BuildScope——单一真相源）
                string scope = BuildScope(msgType, targetId);
                string fileName = Path.GetFileName(path);
                int fileType = ResolveFileType(fileName);
                byte[] data = File.ReadAllBytes(path);
                string json = JsonUtil.Object(("file_type", fileType), ("srv_send_msg", false), ("file_data", Convert.ToBase64String(data)), ("file_name", fileName));
                System.Net.Http.HttpResponseMessage r = PostJson(_apiHost + scope + "/files", json, true);
                if (!r.IsSuccessStatusCode)
                {
                    Log("QQBot | " + _displayName + " | 文件上传失败: " + (int)r.StatusCode + " | " + fileName, 2);
                    return "文件发送失败: 上传 " + (int)r.StatusCode;
                }
                string raw = r.Content.ReadAsStringAsync().GetAwaiter().GetResult();
                string fileInfo = "";
                using (JsonDocument d = JsonUtil.ParseStrict(raw))
                {
                    if (d.RootElement.TryGetProperty("file_info", out JsonElement fiE))
                    {
                        fileInfo = fiE.GetString() ?? "";
                    }
                }
                if (fileInfo.Length == 0)
                {
                    return "文件发送失败: 上传未返回 file_info";
                }
                // [段2] 发送媒体消息——msg_type=7 + 可选被动 msg_id（msg_seq 递增：同 msg_id 多次回复须递增，否则 40054005 去重拒）
                string msg = "{\"msg_type\":7,\"media\":{\"file_info\":" + JsonUtil.Str(fileInfo) + "}";
                if (msgId.Length > 0)
                {
                    _msgSeq = _msgSeq + 1;
                    msg = msg + ",\"msg_id\":" + JsonUtil.Str(msgId) + ",\"msg_seq\":" + _msgSeq.ToString();
                }
                msg = msg + "}";
                System.Net.Http.HttpResponseMessage r2 = PostJson(_apiHost + scope + "/messages", msg, true);
                if (!r2.IsSuccessStatusCode)
                {
                    Log("QQBot | " + _displayName + " | 文件消息发送失败: " + (int)r2.StatusCode + " | " + fileName, 2);
                    return "文件发送失败: 发送 " + (int)r2.StatusCode;
                }
                Log("QQBot | " + _displayName + " | 发文件: " + msgType + ":" + targetId + " ← " + fileName + " (" + fi.Length + "B)", 1);
                return "";
            }
            catch (Exception e)
            {
                Log("QQBot | " + _displayName + " | 文件发送异常: " + e.Message, 2);
                return "文件发送失败: " + e.Message;
            }
        }

        /// <summary>
        /// 文件类型判定——按扩展名映射 file_type（1 图片 / 2 视频 / 3 语音 / 4 文件）。
        /// </summary>
        /// <param name="fileName">文件名</param>
        /// <returns>file_type</returns>
        private static int ResolveFileType(string fileName)
        {
            string ext = "";
            int dot = fileName.LastIndexOf('.');
            if (dot >= 0)
            {
                ext = fileName.Substring(dot + 1).ToLowerInvariant();
            }
            if (ext == "png" || ext == "jpg" || ext == "jpeg" || ext == "gif" || ext == "webp" || ext == "bmp")
            {
                return 1;
            }
            if (ext == "mp4")
            {
                return 2;
            }
            if (ext == "silk" || ext == "mp3" || ext == "wav" || ext == "ogg")
            {
                return 3;
            }
            return 4;
        }

        /// <summary>
        /// 刷新访问令牌——POST bots.qq.com/app/getAppAccessToken（appId + clientSecret → access_token + expires_in）。
        /// </summary>
        private void RefreshToken()
        {
            try
            {
                string body = JsonUtil.Object(("appId", _appId), ("clientSecret", _secret));
                System.Net.Http.HttpResponseMessage r = SendJson("https://bots.qq.com/app/getAppAccessToken", body, false);
                string raw = r.Content.ReadAsStringAsync().GetAwaiter().GetResult();
                int expires = 7200;
                // 单次解析——access_token + expires_in 同文档取（原两次 JsonDocument.Parse 合并，R6-P3-07）
                using (JsonDocument d = JsonUtil.ParseStrict(raw))
                {
                    JsonElement tokenE;
                    if (d.RootElement.TryGetProperty("access_token", out tokenE))
                    {
                        _accessToken = tokenE.GetString() ?? "";
                    }
                    JsonElement expE;
                    if (d.RootElement.TryGetProperty("expires_in", out expE))
                    {
                        if (expE.ValueKind == JsonValueKind.Number)
                        {
                            expires = expE.GetInt32();
                        }
                        else if (expE.ValueKind == JsonValueKind.String)
                        {
                            int.TryParse(expE.GetString(), out expires);
                        }
                    }
                }
                if (_accessToken.Length == 0)
                {
                    Log("QQBot | " + _displayName + " | Token 刷新响应无 access_token", 3);
                }
                if (expires <= 0)
                {
                    expires = 7200;
                }
                _tokenExpireTick = Stopwatch.GetTimestamp() + (long)((expires - 300) * Stopwatch.Frequency);
                Log("QQBot | " + _displayName + " | Token 刷新成功 " + expires + "s", 1);
            }
            catch (Exception e)
            {
                Log("QQBot | " + _displayName + " | Token 刷新失败: " + e.Message, 3);
            }
        }

        /// <summary>
        /// WS 主循环——连接 + 接收 + opcode 分发；断线自动重连（Resume 优先）。
        /// </summary>
        private void WsLoop()
        {
            while (_wsRun)
            {
                try
                {
                    _ws = new ClientWebSocket();
                    _ws.ConnectAsync(new Uri("wss://api.sgroup.qq.com/websocket"), CancellationToken.None).GetAwaiter().GetResult();
                    _reconnectCount = _reconnectCount + 1;
                    string resumeTag = _sessionId.Length > 0
                        ? "  将尝试 Resume  session_id=" + Truncate(_sessionId, 16) + "...  seq=" + _lastSeq
                        : "  首次鉴权 (Identify)";
                    Log("QQBot | " + _displayName + " | WS 已连接 (重连#" + _reconnectCount + ")" + resumeTag, 1);

                    byte[] buf = new byte[65536];
                    int hbMs = 41250;
                    bool hbStarted = false;
                    bool didResume = false;
                    string pendingSessionId = _sessionId;
                    _missedAcks = 0;

                    while (_ws.State == WebSocketState.Open && _wsRun)
                    {
                        int receiveTimeoutMs = hbMs * 2 + 10000;
                        using (CancellationTokenSource cts = new CancellationTokenSource(receiveTimeoutMs))
                        {
                            WebSocketReceiveResult result;
                            try
                            {
                                result = _ws.ReceiveAsync(new ArraySegment<byte>(buf), cts.Token).GetAwaiter().GetResult();
                            }
                            catch (OperationCanceledException)
                            {
                                Log("QQBot | " + _displayName + " | ReceiveAsync 超时 " + (receiveTimeoutMs / 1000) + "s，连接坏死，主动断开重连", 2);
                                break;
                            }
                            if (result.MessageType == WebSocketMessageType.Close)
                            {
                                break;
                            }
                            string raw = Encoding.UTF8.GetString(buf, 0, result.Count);
                            using (JsonDocument d = JsonUtil.ParseStrict(raw))
                            {
                                int op = d.RootElement.GetProperty("op").GetInt32();
                                // ── op=10: Hello ──────────────────────
                                if (op == 10)
                                {
                                    JsonElement dd = d.RootElement.GetProperty("d");
                                    hbMs = dd.GetProperty("heartbeat_interval").GetInt32();
                                    if (dd.TryGetProperty("session_id", out JsonElement sid))
                                    {
                                        _sessionId = sid.GetString() ?? "";
                                    }
                                    Log("QQBot | " + _displayName + " | Hello 心跳=" + hbMs + "ms  session=" + (_sessionId.Length > 0 ? Truncate(_sessionId, 16) + "..." : "(无)"), 1);
                                    // 决定发 op=6 Resume 还是 op=2 Identify
                                    string auth;
                                    if (pendingSessionId.Length > 0)
                                    {
                                        didResume = true;
                                        auth = "{\"op\":6,\"d\":{\"token\":\"QQBot " + _accessToken + "\",\"session_id\":\"" + pendingSessionId + "\",\"seq\":" + _lastSeq + "}}";
                                    }
                                    else
                                    {
                                        didResume = false;
                                        auth = "{\"op\":2,\"d\":{\"token\":\"QQBot " + _accessToken + "\",\"intents\":" + ((1 << 25) | (1 << 26)) + ",\"shard\":[0,1]}}";
                                    }
                                    byte[] ab = Encoding.UTF8.GetBytes(auth);
                                    _ws.SendAsync(new ArraySegment<byte>(ab), WebSocketMessageType.Text, true, CancellationToken.None).GetAwaiter().GetResult();
                                    Log("QQBot | " + _displayName + " | " + (didResume ? "Resume(op=6)" : "Identify(op=2)") + " 已发送", 1);
                                    if (!hbStarted)
                                    {
                                        hbStarted = true;
                                        ClientWebSocket capturedWs = _ws;
                                        new Thread(delegate () { HbLoop(capturedWs, hbMs); }) { IsBackground = true }.Start();
                                    }
                                }
                                // ── op=0: Dispatch ────────────────────
                                else if (op == 0)
                                {
                                    string t = "";
                                    long s = 0;
                                    if (d.RootElement.TryGetProperty("t", out JsonElement tp))
                                    {
                                        t = tp.GetString() ?? "";
                                    }
                                    if (d.RootElement.TryGetProperty("s", out JsonElement se))
                                    {
                                        s = se.GetInt64();
                                    }
                                    if (s > 0)
                                    {
                                        _lastSeq = s;
                                    }
                                    bool isMsg = (t == "C2C_MESSAGE_CREATE" || t == "GROUP_AT_MESSAGE_CREATE");
                                    string stateTag = "";
                                    if (t == "RESUMED")
                                    {
                                        stateTag = " ✅会话恢复成功";
                                    }
                                    else if (t == "READY")
                                    {
                                        stateTag = " ✅鉴权完成";
                                    }
                                    Log("QQBot | " + _displayName + " ◄◄◄ op=0 s=" + s + " t=" + (t.Length > 0 ? t : "(无)") + (isMsg ? " ★消息" : "") + stateTag, 1);
                                    if (_onMessage != null)
                                    {
                                        _onMessage(raw);
                                    }
                                }
                                // ── op=9: Invalid Session ─────────────
                                else if (op == 9)
                                {
                                    int code = 0;
                                    if (d.RootElement.TryGetProperty("d", out JsonElement d9) && d9.TryGetProperty("code", out JsonElement ce))
                                    {
                                        code = ce.GetInt32();
                                    }
                                    if (code == ERR_INVALID_SESSION && didResume)
                                    {
                                        Log("QQBot | " + _displayName + " | Resume 被拒 (code=4006 session无效)，回落 Identify(op=2)", 2);
                                        didResume = false;
                                        string auth2 = "{\"op\":2,\"d\":{\"token\":\"QQBot " + _accessToken + "\",\"intents\":" + ((1 << 25) | (1 << 26)) + ",\"shard\":[0,1]}}";
                                        byte[] ab2 = Encoding.UTF8.GetBytes(auth2);
                                        _ws.SendAsync(new ArraySegment<byte>(ab2), WebSocketMessageType.Text, true, CancellationToken.None).GetAwaiter().GetResult();
                                        Log("QQBot | " + _displayName + " | Identify(op=2) 已发送（回落）", 1);
                                    }
                                    else if (code == ERR_EXPIRED && didResume)
                                    {
                                        Log("QQBot | " + _displayName + " | Resume 过期 (code=4009)，重试 op=6", 2);
                                        string auth6 = "{\"op\":6,\"d\":{\"token\":\"QQBot " + _accessToken + "\",\"session_id\":\"" + pendingSessionId + "\",\"seq\":" + _lastSeq + "}}";
                                        byte[] ab6 = Encoding.UTF8.GetBytes(auth6);
                                        _ws.SendAsync(new ArraySegment<byte>(ab6), WebSocketMessageType.Text, true, CancellationToken.None).GetAwaiter().GetResult();
                                    }
                                    else
                                    {
                                        _sessionId = "";
                                        Log("QQBot | " + _displayName + " | 会话无效 op=9 code=" + code + "，清除 session_id 后重连", 2);
                                        break;
                                    }
                                }
                                // ── op=11: Heartbeat ACK ──────────────
                                else if (op == 11)
                                {
                                    _missedAcks = 0;
                                }
                                // ── op=7: Reconnect ───────────────────
                                else if (op == 7)
                                {
                                    Log("QQBot | " + _displayName + " | 服务端要求重连 (op=7)", 1);
                                    break;
                                }
                            }
                        }
                    }
                }
                catch (Exception e)
                {
                    Log("QQBot | " + _displayName + " | WS 异常 (重连#" + _reconnectCount + "): " + e.Message, 2);
                }
                finally
                {
                    try
                    {
                        if (_ws != null)
                        {
                            _ws.Dispose();
                        }
                    }
                    catch (Exception ex)
                    {
                        Log("QQBot | " + _displayName + " | WS 释放异常（重连继续）: " + ex.Message, 2);
                    }
                    _ws = null;
                }
                if (_wsRun)
                {
                    Thread.Sleep(3000);
                    if (Stopwatch.GetTimestamp() > _tokenExpireTick)
                    {
                        RefreshToken();
                    }
                }
            }
        }

        /// <summary>
        /// 心跳发送循环——绑定创建时的 ClientWebSocket 实例（ws 参数）。
        /// 每次发送后递增 _missedAcks；若连续 >= MaxMissedAcks 次无 ACK，主动 Dispose ws 触发 WsLoop 重连。
        /// d 字段携带 _lastSeq，使服务端知晓客户端已处理事件进度。
        /// </summary>
        /// <param name="ws">绑定的 WS 实例</param>
        /// <param name="intervalMs">心跳间隔毫秒</param>
        private void HbLoop(ClientWebSocket ws, int intervalMs)
        {
            while (_wsRun && ws.State == WebSocketState.Open)
            {
                Thread.Sleep(intervalMs);
                if (_missedAcks >= MaxMissedAcks)
                {
                    Log("QQBot | " + _displayName + " | 心跳 ACK 连续 " + _missedAcks + " 次无回应，连接坏死，主动断开", 2);
                    try
                    {
                        ws.Dispose();
                    }
                    catch (Exception ex)
                    {
                        Log("QQBot | " + _displayName + " | 心跳断开释放异常: " + ex.Message, 2);
                    }
                    break;
                }
                try
                {
                    byte[] hb = Encoding.UTF8.GetBytes("{\"op\":1,\"d\":" + _lastSeq + "}");
                    ws.SendAsync(new ArraySegment<byte>(hb), WebSocketMessageType.Text, true, CancellationToken.None).GetAwaiter().GetResult();
                    _missedAcks = _missedAcks + 1;
                }
                catch (Exception ex)
                {
                    Log("QQBot | " + _displayName + " | 心跳发送失败（断开重连）: " + ex.Message, 2);
                    break;
                }
            }
        }

        /// <summary>日志——LogStore 统一观测（module=QQBot）</summary>
        private void Log(string message, int level)
        {
            LogStore.Add("QQBot", level, message, "QQBOT");
        }

        /// <summary>截断文本——日志展示</summary>
        private static string Truncate(string s, int max)
        {
            return s.Length <= max ? s : s.Substring(0, max);
        }

        /// <summary>单行化——\r\n 替换空格（日志展示：LogStore 单行存储，跨行内容被截断）</summary>
        private static string OneLine(string s)
        {
            if (s == null)
            {
                return "";
            }
            return s.Replace("\r", " ").Replace("\n", " ");
        }
        /// <summary>
        /// 单次 JSON POST——独立请求消息（HttpRequestMessage 不可重用；头走请求级，静态客户端无并发污染）。
        /// </summary>
        /// <param name="url">目标 URL</param>
        /// <param name="json">请求体 JSON</param>
        /// <param name="withAuth">true=携带 QQBot Authorization 头</param>
        /// <returns>响应消息</returns>
        private System.Net.Http.HttpResponseMessage SendJson(string url, string json, bool withAuth)
        {
            System.Net.Http.HttpRequestMessage req = new System.Net.Http.HttpRequestMessage(System.Net.Http.HttpMethod.Post, url);
            if (withAuth)
            {
                req.Headers.Add("Authorization", "QQBot " + _accessToken);
            }
            req.Content = new System.Net.Http.StringContent(json, Encoding.UTF8, "application/json");
            return _http.SendAsync(req).GetAwaiter().GetResult();
        }
        /// <summary>
        /// JSON POST + 401 自动刷新令牌重试一次——QQ 开放平台令牌过期统一处理（原 SendReply/SendFile 重复实现收拢）。
        /// </summary>
        /// <param name="url">目标 URL</param>
        /// <param name="json">请求体 JSON</param>
        /// <param name="withAuth">true=携带 Authorization 头</param>
        /// <returns>响应消息（重试后仍失败返回最后一次响应）</returns>
        private System.Net.Http.HttpResponseMessage PostJson(string url, string json, bool withAuth)
        {
            System.Net.Http.HttpResponseMessage r = SendJson(url, json, withAuth);
            if (withAuth && (int)r.StatusCode == 401)
            {
                RefreshToken();
                r = SendJson(url, json, withAuth);
            }
            return r;
        }

        /// <summary>
        /// 单次 GET——独立请求消息（附件下载面；withAuth=true 携带 Authorization 头）。
        /// </summary>
        /// <param name="url">目标 URL</param>
        /// <param name="withAuth">true=携带 Authorization 头</param>
        /// <returns>响应消息</returns>
        private System.Net.Http.HttpResponseMessage SendGet(string url, bool withAuth)
        {
            System.Net.Http.HttpRequestMessage req = new System.Net.Http.HttpRequestMessage(System.Net.Http.HttpMethod.Get, url);
            if (withAuth)
            {
                req.Headers.Add("Authorization", "QQBot " + _accessToken);
            }
            return _http.SendAsync(req).GetAwaiter().GetResult();
        }
    }
}
