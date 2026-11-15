// NRT 抑制（CSharp exp §1.16）——WS 连接面：空态字段（WS 实例 / 线程 / 令牌）在分支内判空，
// 逐处加 ? 会把连接状态机淹掉；语义判空在消费点。
#nullable disable warnings

using System;
using System.Diagnostics;
using System.Net.WebSockets;
using System.Text;
using System.Text.Json;
using System.Threading;
using Mau.Runtime;

namespace Mau.Providers
{
    /// <summary>
    /// 单 QQ 机器人 WebSocket 连接（A379 收信面）。
    /// 协议资产迁自归档 `_archive/CatHome4.QQ/QQBotConnection.cs`（A299 归档前的实现，仅保留收信链：
    /// 鉴权 / 心跳 / Resume / opcode 分发 / 事件回调；发送面与文件通道随 A380 工具组另行补）。
    /// 【协议要点】op=0 Dispatch（含 t 事件类型 + s 序列号）· op=1 Heartbeat · op=2 Identify · op=6 Resume ·
    /// op=7 Reconnect · op=9 Invalid Session（4006 无效 session / 4009 过期可重试）· op=10 Hello · op=11 ACK。
    /// 【重连策略】断线 → 有 session_id 先 Resume，4006 回落 Identify，4009 重试 Resume；心跳连续 2 次无 ACK 主动断开重连。
    /// 🔴 与归档版的差异：网关地址按环境分派（归档版硬编码正式网关，沙箱配置会连错环境）。
    /// </summary>
    internal sealed class QqBotConnection
    {
        /// <summary>op=9 错误码——session_id 无效，须全新鉴权</summary>
        private const int ErrInvalidSession = 4006;

        /// <summary>op=9 错误码——连接过期，可重试 resume</summary>
        private const int ErrExpired = 4009;

        /// <summary>心跳 ACK 超时阈值——连续无 ACK 次数超限触发重连</summary>
        private const int MaxMissedAcks = 2;

        /// <summary>事件订阅位——GROUP_AND_C2C_EVENT（私聊 + 群@ 群全量）与 INTERACTION_CREATE</summary>
        private const int Intents = (1 << 25) | (1 << 26);

        /// <summary>配置池身份（qqBotId 的 D 格式串）</summary>
        private readonly string _botKey;

        /// <summary>显示名——日志可读性</summary>
        private readonly string _displayName;

        /// <summary>QQ 开放平台 AppID</summary>
        private readonly string _appId;

        /// <summary>AppSecret——仅内存持有</summary>
        private readonly string _secret;

        /// <summary>沙箱标志——决定 API 主机与 WS 网关</summary>
        private readonly bool _sandbox;

        /// <summary>事件回调——(事件名, 原始 JSON 全文)；事件名空串表示无法解析 t 字段</summary>
        private readonly Action<string, string> _onEvent;

        /// <summary>API 主机——沙箱 / 正式双端点</summary>
        private readonly string _apiHost;

        /// <summary>WS 网关——沙箱 / 正式双端点</summary>
        private readonly string _wsHost;

        /// <summary>访问令牌——鉴权产物</summary>
        private string _accessToken = "";

        /// <summary>令牌过期时刻——Stopwatch 时间戳</summary>
        private long _tokenExpireTick = 0;

        /// <summary>WS 连接实例</summary>
        private ClientWebSocket _ws = null;

        /// <summary>WS 线程——后台常驻</summary>
        private Thread _wsThread = null;

        /// <summary>WS 运行标志——Stop 置 false 退出循环</summary>
        private volatile bool _wsRun = false;

        /// <summary>会话恢复——session_id</summary>
        private string _sessionId = "";

        /// <summary>会话恢复——最后处理序列号</summary>
        private long _lastSeq = 0;

        /// <summary>重连计数——状态面与日志标识</summary>
        private int _reconnectCount = 0;

        /// <summary>心跳 ACK 追踪——发送后递增，收到 op=11 清零（跨线程共享）</summary>
        private volatile int _missedAcks = 0;

        /// <summary>最近一次事件名——状态面可观测性</summary>
        private string _lastEvent = "";

        /// <summary>最近一次事件时刻——状态面可观测性</summary>
        private string _lastEventAt = "";

        /// <summary>共享 HTTP 客户端——令牌交换复用（Authorization 走每请求头，静态客户端不持有默认头）</summary>
        private static readonly System.Net.Http.HttpClient _http = new System.Net.Http.HttpClient();

        /// <summary>
        /// 构造单机器人连接（不启动）。
        /// </summary>
        /// <param name="botKey">配置池身份</param>
        /// <param name="displayName">显示名</param>
        /// <param name="appId">AppID</param>
        /// <param name="secret">AppSecret</param>
        /// <param name="sandbox">沙箱标志</param>
        /// <param name="onEvent">事件回调（事件名, 原始 JSON）</param>
        public QqBotConnection(string botKey, string displayName, string appId, string secret, bool sandbox, Action<string, string> onEvent)
        {
            _botKey = botKey;
            _displayName = displayName;
            _appId = appId;
            _secret = secret;
            _sandbox = sandbox;
            _onEvent = onEvent;
            _apiHost = _sandbox
                ? "https://sandbox.api.sgroup.qq.com"
                : "https://api.sgroup.qq.com";
            _wsHost = _sandbox
                ? "wss://sandbox.api.sgroup.qq.com/websocket"
                : "wss://api.sgroup.qq.com/websocket";
        }

        /// <summary>配置池身份</summary>
        public string BotKey
        {
            get { return _botKey; }
        }

        /// <summary>QQ 开放平台 AppID（留档目录名与 botinfo 显示用）</summary>
        public string AppId
        {
            get { return _appId; }
        }

        /// <summary>显示名</summary>
        public string DisplayName
        {
            get { return _displayName; }
        }

        /// <summary>沙箱标志</summary>
        public bool Sandbox
        {
            get { return _sandbox; }
        }

        /// <summary>重连计数——状态面展示</summary>
        public int ReconnectCount
        {
            get { return _reconnectCount; }
        }

        /// <summary>运行标志</summary>
        public bool Running
        {
            get { return _wsRun; }
        }

        /// <summary>最近事件摘要——状态面展示（名 + 时刻）</summary>
        public string LastEventSummary
        {
            get
            {
                if (_lastEvent.Length == 0)
                {
                    return "（尚无事件）";
                }
                return _lastEvent + " @" + _lastEventAt;
            }
        }

        /// <summary>
        /// 启动连接——刷新令牌 + 拉起 WS 线程（重复调用幂等）。
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

        /// <summary>停止连接——置运行标志 false + 关闭并释放 WS。</summary>
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
        /// 刷新访问令牌——POST bots.qq.com/app/getAppAccessToken（appId + clientSecret → access_token + expires_in）。
        /// </summary>
        private void RefreshToken()
        {
            try
            {
                string body = JsonUtil.Object(("appId", _appId), ("clientSecret", _secret));
                System.Net.Http.HttpResponseMessage r = SendJson("https://bots.qq.com/app/getAppAccessToken", body);
                string raw = r.Content.ReadAsStringAsync().GetAwaiter().GetResult();
                int expires = 7200;
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
                    Log("QQBot | " + _displayName + " | Token 刷新响应无 access_token（原始体）: " + Truncate(OneLine(raw), 200), 3);
                    return;
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
                    _ws.ConnectAsync(new Uri(_wsHost), CancellationToken.None).GetAwaiter().GetResult();
                    _reconnectCount = _reconnectCount + 1;
                    string resumeTag = _sessionId.Length > 0
                        ? "  将尝试 Resume session_id=" + Truncate(_sessionId, 16) + "...  seq=" + _lastSeq
                        : "  首次鉴权 (Identify)";
                    Log("QQBot | " + _displayName + " | WS 已连接 " + _wsHost + " (重连#" + _reconnectCount + ")" + resumeTag, 1);

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
                            // [段1] 帧分发——按 opcode 处理（0 事件 / 10 Hello / 9 无效会话 / 11 ACK / 7 重连）
                            using (JsonDocument d = JsonUtil.ParseStrict(raw))
                            {
                                int op = d.RootElement.GetProperty("op").GetInt32();
                                if (op == 10)
                                {
                                    hbMs = HandleHello(d, pendingSessionId, ref didResume);
                                    if (!hbStarted)
                                    {
                                        hbStarted = true;
                                        ClientWebSocket capturedWs = _ws;
                                        int capturedHb = hbMs;
                                        new Thread(delegate () { HbLoop(capturedWs, capturedHb); }) { IsBackground = true }.Start();
                                    }
                                }
                                else if (op == 0)
                                {
                                    HandleDispatch(d, raw);
                                }
                                else if (op == 9)
                                {
                                    if (!HandleInvalidSession(d, ref didResume, pendingSessionId))
                                    {
                                        break;
                                    }
                                }
                                else if (op == 11)
                                {
                                    _missedAcks = 0;
                                }
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
        /// op=10 Hello——取心跳间隔 + session_id，发 Resume(op=6) 或 Identify(op=2)。
        /// </summary>
        /// <param name="d">帧文档</param>
        /// <param name="pendingSessionId">本次连接携带的旧 session_id（非空=尝试 Resume）</param>
        /// <param name="didResume">出参——本次是否走 Resume</param>
        /// <returns>心跳间隔毫秒</returns>
        private int HandleHello(JsonDocument d, string pendingSessionId, ref bool didResume)
        {
            JsonElement dd = d.RootElement.GetProperty("d");
            int hbMs = dd.GetProperty("heartbeat_interval").GetInt32();
            JsonElement sid;
            if (dd.TryGetProperty("session_id", out sid))
            {
                _sessionId = sid.GetString() ?? "";
            }
            Log("QQBot | " + _displayName + " | Hello 心跳=" + hbMs + "ms", 1);
            string auth;
            if (pendingSessionId.Length > 0)
            {
                didResume = true;
                auth = "{\"op\":6,\"d\":{\"token\":" + JsonUtil.Str("QQBot " + _accessToken) + ",\"session_id\":" + JsonUtil.Str(pendingSessionId) + ",\"seq\":" + _lastSeq.ToString() + "}}";
            }
            else
            {
                didResume = false;
                auth = BuildIdentify();
            }
            SendRaw(auth);
            Log("QQBot | " + _displayName + " | " + (didResume ? "Resume(op=6)" : "Identify(op=2)") + " 已发送", 1);
            return hbMs;
        }

        /// <summary>
        /// op=0 Dispatch——记录序列号 + 事件日志 + 事件回调（原始 JSON 交给服务层落盘）。
        /// </summary>
        /// <param name="d">帧文档</param>
        /// <param name="raw">帧原文</param>
        private void HandleDispatch(JsonDocument d, string raw)
        {
            string t = "";
            long s = 0;
            JsonElement tp;
            if (d.RootElement.TryGetProperty("t", out tp))
            {
                t = tp.GetString() ?? "";
            }
            JsonElement se;
            if (d.RootElement.TryGetProperty("s", out se))
            {
                s = se.GetInt64();
            }
            if (s > 0)
            {
                _lastSeq = s;
            }
            bool isMsg = (t == "C2C_MESSAGE_CREATE" || t == "GROUP_AT_MESSAGE_CREATE" || t == "GROUP_MESSAGE_CREATE");
            string stateTag = "";
            if (t == "RESUMED")
            {
                stateTag = " 会话恢复成功";
            }
            else if (t == "READY")
            {
                stateTag = " 鉴权完成";
            }
            Log("QQBot | " + _displayName + " | op=0 s=" + s.ToString() + " t=" + (t.Length > 0 ? t : "(无)") + (isMsg ? " ★消息" : "") + stateTag, 1);
            if (t.Length > 0 && !isMsg && t != "READY" && t != "RESUMED")
            {
                return;
            }
            if (_onEvent != null)
            {
                _onEvent(t, raw);
            }
        }

        /// <summary>
        /// op=9 Invalid Session——4006 回落 Identify / 4009 重试 Resume / 其余清会话后重连。
        /// </summary>
        /// <param name="d">帧文档</param>
        /// <param name="didResume">本次是否走 Resume</param>
        /// <param name="pendingSessionId">旧 session_id</param>
        /// <returns>true=连接可继续 / false=需断开重连</returns>
        private bool HandleInvalidSession(JsonDocument d, ref bool didResume, string pendingSessionId)
        {
            int code = 0;
            JsonElement d9;
            if (d.RootElement.TryGetProperty("d", out d9))
            {
                JsonElement ce;
                if (d9.TryGetProperty("code", out ce))
                {
                    code = ce.GetInt32();
                }
            }
            if (code == ErrInvalidSession && didResume)
            {
                Log("QQBot | " + _displayName + " | Resume 被拒 (code=4006 session 无效)，回落 Identify(op=2)", 2);
                didResume = false;
                SendRaw(BuildIdentify());
                return true;
            }
            if (code == ErrExpired && didResume)
            {
                Log("QQBot | " + _displayName + " | Resume 过期 (code=4009)，重试 op=6", 2);
                string auth = "{\"op\":6,\"d\":{\"token\":" + JsonUtil.Str("QQBot " + _accessToken) + ",\"session_id\":" + JsonUtil.Str(pendingSessionId) + ",\"seq\":" + _lastSeq.ToString() + "}}";
                SendRaw(auth);
                return true;
            }
            _sessionId = "";
            Log("QQBot | " + _displayName + " | 会话无效 op=9 code=" + code.ToString() + "，清除 session_id 后重连", 2);
            return false;
        }

        /// <summary>
        /// 心跳发送循环——绑定创建时的 WS 实例；连续 MaxMissedAcks 次无 ACK 主动断开触发重连。
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
                    Log("QQBot | " + _displayName + " | 心跳 ACK 连续 " + _missedAcks.ToString() + " 次无回应，连接坏死，主动断开", 2);
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
                    byte[] hb = Encoding.UTF8.GetBytes("{\"op\":1,\"d\":" + _lastSeq.ToString() + "}");
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

        /// <summary>
        /// 构造 Identify 鉴权帧（op=2）。
        /// </summary>
        /// <returns>鉴权帧 JSON</returns>
        private string BuildIdentify()
        {
            return "{\"op\":2,\"d\":{\"token\":" + JsonUtil.Str("QQBot " + _accessToken) + ",\"intents\":" + Intents.ToString() + ",\"shard\":[0,1]}}";
        }

        /// <summary>
        /// 发送一条消息——被动回复（带 msg_id + msg_seq）与主动消息（不带 msg_id）共用本入口（A379）。
        /// 端点：群 <c>/v2/groups/{group_openid}/messages</c> · 私聊 <c>/v2/users/{user_openid}/messages</c>。
        /// 被动窗口：群 5 分钟 5 次 / 单聊 60 分钟 4 次；主动消息受 20 qpm 与 1000 条/群/天 频控。
        /// </summary>
        /// <param name="targetKind">目标类型（group=群 / user=私聊）</param>
        /// <param name="targetOpenId">目标 OpenID（group_openid 或 user_openid）</param>
        /// <param name="text">正文（Markdown 或纯文本）</param>
        /// <param name="msgId">被动回复锚（非空=被动；空=主动消息）</param>
        /// <param name="msgSeq">回复序号（同一 msg_id 多次回复须递增，最小 1）</param>
        /// <param name="markdown">true=按 Markdown 发送（msg_type=2），失败自动降级纯文本</param>
        /// <returns>空串成功；"ERR|CODE|说明"失败（不抛异常）</returns>
        public string SendMessage(string targetKind, string targetOpenId, string text, string msgId, int msgSeq, bool markdown = true)
        {
            if (string.IsNullOrEmpty(targetOpenId))
            {
                return "ERR|BAD_ARGS|目标 OpenID 为空";
            }
            if (!EnsureToken())
            {
                return "ERR|TOKEN_FAIL|访问令牌不可用（appId/secret 或网络异常，见运行日志）";
            }
            string kind = string.Equals(targetKind, "user", StringComparison.OrdinalIgnoreCase) ? "users" : "groups";
            string url = _apiHost + "/v2/" + kind + "/" + targetOpenId + "/messages";
            if (msgSeq < 1)
            {
                msgSeq = 1;
            }
            // [段1] Markdown 优先——平台侧 Markdown 权限 / 语法不支持时不让消息彻底发不出去：失败即降级纯文本重发
            string err = PostMessage(url, kind, targetOpenId, text, msgId, msgSeq, markdown);
            if (err.Length > 0 && markdown)
            {
                Log("QQBot | " + _displayName + " | Markdown 发送失败，降级纯文本重发 | " + Truncate(OneLine(err), 120), 2);
                err = PostMessage(url, kind, targetOpenId, text, msgId, msgSeq, false);
            }
            return err;
        }
        /// <summary>
        /// 消息体发送——一次 HTTP POST（A379）。Markdown 走 msg_type=2 + markdown.content（content 不传）；
        /// 纯文本走 msg_type=0 + content；被动锚（msg_id / msg_seq）两种形态共用。
        /// </summary>
        /// <param name="url">发送端点</param>
        /// <param name="targetKind">端点口径（groups / users，日志用）</param>
        /// <param name="targetOpenId">目标 OpenID</param>
        /// <param name="text">正文（Markdown 或纯文本）</param>
        /// <param name="msgId">被动锚（空=主动消息）</param>
        /// <param name="msgSeq">回复序号</param>
        /// <param name="markdown">true=Markdown 消息（msg_type=2）</param>
        /// <returns>空串成功；"ERR|CODE|说明"失败（不抛异常）</returns>
        private string PostMessage(string url, string targetKind, string targetOpenId, string text, string msgId, int msgSeq, bool markdown)
        {
            StringBuilder body = new StringBuilder();
            if (markdown)
            {
                body.Append("{\"msg_type\":2,\"markdown\":{\"content\":").Append(JsonUtil.Str(text ?? "")).Append("}");
            }
            else
            {
                body.Append("{\"msg_type\":0,\"content\":").Append(JsonUtil.Str(text ?? ""));
            }
            if (!string.IsNullOrEmpty(msgId))
            {
                body.Append(",\"msg_id\":").Append(JsonUtil.Str(msgId));
                body.Append(",\"msg_seq\":").Append(msgSeq.ToString());
            }
            body.Append("}");
            string raw = "";
            int status = 0;
            try
            {
                System.Net.Http.HttpResponseMessage r = SendJsonAuth(url, body.ToString());
                status = (int)r.StatusCode;
                raw = r.Content.ReadAsStringAsync().GetAwaiter().GetResult();
            }
            catch (Exception ex)
            {
                Log("QQBot | " + _displayName + " | 发送异常: " + ex.Message, 3);
                return "ERR|SEND_EX|" + ex.Message;
            }
            string code = "";
            string message = "";
            try
            {
                using (JsonDocument d = JsonUtil.ParseStrict(raw))
                {
                    JsonElement e;
                    if (d.RootElement.TryGetProperty("code", out e))
                    {
                        code = e.ValueKind == JsonValueKind.Number ? e.GetRawText() : (e.GetString() ?? "");
                    }
                    if (d.RootElement.TryGetProperty("message", out e))
                    {
                        message = e.GetString() ?? "";
                    }
                }
            }
            catch (Exception ex)
            {
                Log("QQBot | " + _displayName + " | 发送响应解析失败: " + ex.Message + " | raw=" + Truncate(OneLine(raw), 200), 2);
            }
            bool ok = status >= 200 && status < 300 && code.Length == 0;
            if (!ok)
            {
                Log("QQBot | " + _displayName + " | 发送失败 " + (markdown ? "[MD]" : "[文本]") + " HTTP=" + status.ToString() + " code=" + (code.Length > 0 ? code : "(无)") + " msg=" + (message.Length > 0 ? message : "(无)") + " | raw=" + Truncate(OneLine(raw), 300), 3);
                return "ERR|SEND_FAIL|HTTP " + status.ToString() + " code=" + (code.Length > 0 ? code : "无") + " " + message;
            }
            Log("QQBot | " + _displayName + " | 发送成功 " + (string.IsNullOrEmpty(msgId) ? "[主动]" : "[被动 seq=" + msgSeq.ToString() + "]") + (markdown ? "[MD]" : "[文本]") + " → " + targetKind + " " + Truncate(targetOpenId, 12) + " | " + Truncate(OneLine(text), 60), 1);
            return "";
        }

        /// <summary>
        /// 查询机器人在指定群的状态——<c>GET /v2/groups/{group_openid}/bot_state</c>（官方接口，30 QPM）。
        /// 返回 <c>allow_proactive_msg</c>（能否主动发言）/ <c>recv_msg_setting</c>（all / only_mention / mention_and_context）/ <c>member_role</c>。
        /// 🔴 该接口仅白名单机器人可用，无权限时返回 code=11253——调用方据此降级为本地观测。
        /// </summary>
        /// <param name="groupOpenId">群 OpenID</param>
        /// <returns>空串=调用失败（原因见运行日志）；否则为原始响应 JSON</returns>
        public string GetBotStateRaw(string groupOpenId)
        {
            if (string.IsNullOrEmpty(groupOpenId))
            {
                return "";
            }
            if (!EnsureToken())
            {
                return "";
            }
            string url = _apiHost + "/v2/groups/" + groupOpenId + "/bot_state";
            try
            {
                System.Net.Http.HttpResponseMessage r = SendGetAuth(url);
                string raw = r.Content.ReadAsStringAsync().GetAwaiter().GetResult();
                Log("QQBot | " + _displayName + " | bot_state 群 " + Truncate(groupOpenId, 12) + " HTTP=" + ((int)r.StatusCode).ToString() + " | " + Truncate(OneLine(raw), 200), 1);
                return raw;
            }
            catch (Exception ex)
            {
                Log("QQBot | " + _displayName + " | bot_state 查询异常: " + ex.Message, 2);
                return "";
            }
        }

        /// <summary>
        /// 查询群基本信息——<c>GET /v2/groups/{group_openid}/info</c>（官方接口，30 QPM）。
        /// 返回 <c>group_name</c> / <c>group_finger_memo</c> / <c>group_member_num</c> 等。
        /// 🔴 同属白名单接口，无权限时返回 code=11253——调用方据此降级（群名留空，不影响发言）。
        /// </summary>
        /// <param name="groupOpenId">群 OpenID</param>
        /// <returns>空串=调用失败（原因见运行日志）；否则为原始响应 JSON</returns>
        public string GetGroupInfoRaw(string groupOpenId)
        {
            if (string.IsNullOrEmpty(groupOpenId))
            {
                return "";
            }
            if (!EnsureToken())
            {
                return "";
            }
            string url = _apiHost + "/v2/groups/" + groupOpenId + "/info";
            try
            {
                System.Net.Http.HttpResponseMessage r = SendGetAuth(url);
                string raw = r.Content.ReadAsStringAsync().GetAwaiter().GetResult();
                Log("QQBot | " + _displayName + " | group_info 群 " + Truncate(groupOpenId, 12) + " HTTP=" + ((int)r.StatusCode).ToString() + " | " + Truncate(OneLine(raw), 200), 1);
                return raw;
            }
            catch (Exception ex)
            {
                Log("QQBot | " + _displayName + " | group_info 查询异常: " + ex.Message, 2);
                return "";
            }
        }

        /// <summary>
        /// 确保访问令牌可用——空或临近过期时刷新。
        /// </summary>
        /// <returns>true=令牌可用于后续请求</returns>
        private bool EnsureToken()
        {
            if (_accessToken.Length == 0 || Stopwatch.GetTimestamp() > _tokenExpireTick)
            {
                RefreshToken();
            }
            return _accessToken.Length > 0;
        }

        /// <summary>
        /// 带鉴权的 JSON POST——Authorization: QQBot {token}（HttpRequestMessage 不可重用，每次新建）。
        /// </summary>
        /// <param name="url">目标 URL</param>
        /// <param name="json">请求体</param>
        /// <returns>响应消息</returns>
        private System.Net.Http.HttpResponseMessage SendJsonAuth(string url, string json)
        {
            System.Net.Http.HttpRequestMessage req = new System.Net.Http.HttpRequestMessage(System.Net.Http.HttpMethod.Post, url);
            req.Headers.TryAddWithoutValidation("Authorization", "QQBot " + _accessToken);
            req.Content = new System.Net.Http.StringContent(json, Encoding.UTF8, "application/json");
            return _http.SendAsync(req).GetAwaiter().GetResult();
        }

        /// <summary>
        /// 带鉴权的 GET。
        /// </summary>
        /// <param name="url">目标 URL</param>
        /// <returns>响应消息</returns>
        private System.Net.Http.HttpResponseMessage SendGetAuth(string url)
        {
            System.Net.Http.HttpRequestMessage req = new System.Net.Http.HttpRequestMessage(System.Net.Http.HttpMethod.Get, url);
            req.Headers.TryAddWithoutValidation("Authorization", "QQBot " + _accessToken);
            return _http.SendAsync(req).GetAwaiter().GetResult();
        }

        /// <summary>
        /// 发送一帧文本——异常一律留痕（发送失败由内层循环按心跳/接收超时收敛）。
        /// </summary>
        /// <param name="text">帧文本</param>
        private void SendRaw(string text)
        {
            try
            {
                byte[] ab = Encoding.UTF8.GetBytes(text);
                _ws.SendAsync(new ArraySegment<byte>(ab), WebSocketMessageType.Text, true, CancellationToken.None).GetAwaiter().GetResult();
            }
            catch (Exception ex)
            {
                Log("QQBot | " + _displayName + " | 帧发送失败: " + ex.Message, 2);
            }
        }

        /// <summary>
        /// 单次 JSON POST——独立请求消息（HttpRequestMessage 不可重用；静态客户端无并发污染）。
        /// </summary>
        /// <param name="url">目标 URL</param>
        /// <param name="json">请求体 JSON</param>
        /// <returns>响应消息</returns>
        private static System.Net.Http.HttpResponseMessage SendJson(string url, string json)
        {
            System.Net.Http.HttpRequestMessage req = new System.Net.Http.HttpRequestMessage(System.Net.Http.HttpMethod.Post, url);
            req.Content = new System.Net.Http.StringContent(json, Encoding.UTF8, "application/json");
            return _http.SendAsync(req).GetAwaiter().GetResult();
        }

        /// <summary>日志——LogStore 统一观测（module=QQBot）。</summary>
        /// <param name="message">日志正文</param>
        /// <param name="level">等级（1=INFO / 2=WARN / 3=ERR）</param>
        private void Log(string message, int level)
        {
            LogStore.Add("QQBot", level, message, "QQBOT");
        }

        /// <summary>截断文本——日志展示用。</summary>
        /// <param name="s">原文</param>
        /// <param name="max">上限</param>
        /// <returns>截断结果</returns>
        private static string Truncate(string s, int max)
        {
            return s.Length <= max ? s : s.Substring(0, max);
        }

        /// <summary>单行化——\r\n 替换空格（LogStore 单行存储，跨行内容会截断）。</summary>
        /// <param name="s">原文</param>
        /// <returns>单行文本</returns>
        private static string OneLine(string s)
        {
            if (s == null)
            {
                return "";
            }
            return s.Replace("\r", " ").Replace("\n", " ");
        }
    }
}
