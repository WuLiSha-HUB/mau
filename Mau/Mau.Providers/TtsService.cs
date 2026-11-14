// NRT 抑制（CSharp exp §1.16）——本服务是 JSON 回包解析面（TryGetProperty 链），
// 大量「可能为 null」的局部量已在分支内判空，逐处 ? 会把解析面淹掉；语义判空在消费点。
#nullable disable warnings

using System;
using System.Collections.Generic;
using System.IO;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using System.Threading.Tasks;
using Mau.Contracts;
using Mau.Runtime;

namespace Mau.Providers
{
    /// <summary>
    /// 语音合成服务实现——云端 TTS（OpenAI 风格 JSON 端点，回 WAV 字节）。
    /// 配置面：audio.tts_endpoint（端点）+ audio.tts_api_config_id（LLM 池配置 → key）。
    /// 关键处理：云端 wav 是流式写出的占位头（RIFF / data 长度全 0xFFFFFFFF），
    /// 落盘前必须重写为实际长度，否则任何按时长解析的消费方都会读到天文数字。
    /// A350：自 Mau.Audio 迁入本工程——本服务无本地音频设备依赖，与 Windows 平台无关。
    /// </summary>
    public class TtsService : ITtsService
    {
        /// <summary>
        /// 产物目录——数据根下 Data/tts（固定落点，不做配置项：轮末总结与前端点播共用同一处）。
        /// </summary>
        private string _outputDir = "";

        /// <summary>
        /// 单次合成超时毫秒——长文实测 1590 字 / 57 秒；120 秒封顶（与工具执行层同口径）。
        /// </summary>
        private const int RequestTimeoutMs = 120000;

        /// <summary>
        /// 产物目录——宿主启动时按数据根注入；未注入为空串（调用方须出声）。
        /// </summary>
        public string OutputDir
        {
            get { return _outputDir; }
        }

        /// <summary>
        /// 配置产物目录——按数据根拼 Data/tts（宿主启动链调用一次）。
        /// </summary>
        /// <param name="dataRoot">数据根绝对路径</param>
        public void Configure(string dataRoot)
        {
            if (dataRoot == null || dataRoot.Length == 0)
            {
                _outputDir = "";
                return;
            }
            _outputDir = Path.Combine(dataRoot, "Data", "tts");
        }

        /// <summary>
        /// 占位长度值——流式 wav 头里表示「长度未知」。
        /// </summary>
        private const uint PlaceholderSize = 0xFFFFFFFF;

        /// <summary>
        /// HTTP 客户端——静态复用（超时由 Timeout 控制；合成请求并发安全）。
        /// </summary>
        private static readonly HttpClient _client = CreateClient();

        /// <summary>
        /// 合成——文本经云端转 WAV 并落盘。
        /// </summary>
        /// <param name="text">待合成文本（非空）</param>
        /// <param name="voice">音色名（空 = 服务默认音色，请求体不下发该字段）</param>
        /// <param name="outPath">产物 WAV 全路径</param>
        /// <returns>成功空串；失败 "ERR|CODE|说明"</returns>
        public string Synthesize(string text, string voice, string outPath)
        {
            if (text == null || text.Length == 0)
            {
                return "ERR|BAD_ARGS|缺少参数 text";
            }
            if (outPath == null || outPath.Length == 0)
            {
                return "ERR|BAD_ARGS|缺少产物路径 outPath";
            }
            // [段1] 配置解析——端点 + 池 key
            string endpoint = "";
            string apiKey = "";
            string configError = ResolveEndpoint(out endpoint, out apiKey);
            if (configError.Length > 0)
            {
                return configError;
            }
            // [段2] 请求——JSON 体（text + 可选 voice_id）
            byte[] bytes = null;
            string requestError = Send(endpoint, apiKey, text, voice, out bytes);
            if (requestError.Length > 0)
            {
                return requestError;
            }
            // [段3] 头修复 + 落盘
            byte[] fixedBytes = FixWavHeader(bytes);
            try
            {
                string dir = Path.GetDirectoryName(outPath);
                if (dir != null && dir.Length > 0 && !Directory.Exists(dir))
                {
                    Directory.CreateDirectory(dir);
                }
                File.WriteAllBytes(outPath, fixedBytes);
            }
            catch (Exception ex)
            {
                return "ERR|TTS_WRITE_FAIL|产物落盘失败: " + ex.Message;
            }
            return "";
        }

        /// <summary>
        /// 创建 HTTP 客户端——超时统一在此设置（静态复用，避免每请求新建）。
        /// </summary>
        /// <returns>客户端实例</returns>
        private static HttpClient CreateClient()
        {
            HttpClient client = new HttpClient();
            client.Timeout = TimeSpan.FromMilliseconds(RequestTimeoutMs);
            return client;
        }

        /// <summary>
        /// 解析配置——audio.tts_endpoint + audio.tts_api_config_id → 池 key。
        /// 空配置一律明示不可用，不静默回落任何默认端点。
        /// </summary>
        /// <param name="endpoint">输出：合成端点</param>
        /// <param name="apiKey">输出：Bearer key</param>
        /// <returns>错误文本（空=通过）</returns>
        private static string ResolveEndpoint(out string endpoint, out string apiKey)
        {
            endpoint = "";
            apiKey = "";
            ConfigStore config = null;
            if (!DataBox.TryResolve<ConfigStore>(out config) || config == null)
            {
                return "ERR|API_NOT_CONFIGURED|宿主未注入 ConfigStore";
            }
            CH_LlmApiConfigStore apiStore = null;
            if (!DataBox.TryResolve<CH_LlmApiConfigStore>(out apiStore) || apiStore == null)
            {
                return "ERR|API_NOT_CONFIGURED|宿主未注入 CH_LlmApiConfigStore";
            }
            endpoint = config.Get("audio.tts_endpoint", "");
            if (endpoint == null || endpoint.Length == 0)
            {
                return "ERR|API_NOT_CONFIGURED|语音合成端点未配置（配置区 audio.tts_endpoint 为空）";
            }
            string rawId = config.Get("audio.tts_api_config_id", "");
            if (rawId == null || rawId.Length == 0)
            {
                return "ERR|API_NOT_CONFIGURED|语音合成 API 未配置（配置区 audio.tts_api_config_id 需引用 LLM 池配置）";
            }
            Guid apiConfigId;
            if (!Guid.TryParse(rawId, out apiConfigId))
            {
                return "ERR|API_NOT_CONFIGURED|语音合成配置无效（audio.tts_api_config_id 不是合法 GUID）";
            }
            CH_LlmApiConfig poolConfig;
            if (!apiStore.TryGet(apiConfigId, out poolConfig))
            {
                return "ERR|API_NOT_CONFIGURED|语音合成配置不存在（LLM 池中找不到该 apiConfigId）";
            }
            apiKey = apiStore.GetSecret(apiConfigId);
            if (apiKey == null || apiKey.Length == 0)
            {
                return "ERR|API_NOT_CONFIGURED|语音合成密钥未配置（LLM 池配置缺 key）";
            }
            return "";
        }

        /// <summary>
        /// 发送合成请求——非 200 时把服务端 error.message 原样带回（不吞细节）。
        /// </summary>
        /// <param name="endpoint">合成端点</param>
        /// <param name="apiKey">Bearer key</param>
        /// <param name="text">文本</param>
        /// <param name="voice">音色（空=不下发）</param>
        /// <param name="bytes">输出：响应字节（成功时）</param>
        /// <returns>错误文本（空=成功）</returns>
        private static string Send(string endpoint, string apiKey, string text, string voice, out byte[] bytes)
        {
            bytes = null;
            // 请求体——Dictionary + JsonSerializer（不经手写转义：值一律原样，避免二次转义）
            Dictionary<string, object> body = new Dictionary<string, object>();
            body["text"] = text;
            if (voice != null && voice.Length > 0)
            {
                body["voice_id"] = voice;
            }
            string bodyJson = JsonSerializer.Serialize(body);
            using (HttpRequestMessage request = new HttpRequestMessage(HttpMethod.Post, endpoint))
            {
                request.Headers.TryAddWithoutValidation("Authorization", "Bearer " + apiKey);
                request.Content = new StringContent(bodyJson, Encoding.UTF8, "application/json");
                HttpResponseMessage response = null;
                try
                {
                    response = _client.Send(request);
                    if (response == null)
                    {
                        return "ERR|TTS_FAIL|合成无响应";
                    }
                    bytes = response.Content.ReadAsByteArrayAsync().GetAwaiter().GetResult();
                    int status = (int)response.StatusCode;
                    if (status != 200)
                    {
                        return "ERR|TTS_FAIL|HTTP " + status.ToString() + ": " + ExtractError(bytes);
                    }
                    if (bytes == null || bytes.Length == 0)
                    {
                        return "ERR|TTS_FAIL|合成返回空响应（已开始的响应中途断开）";
                    }
                    return "";
                }
                catch (TaskCanceledException)
                {
                    return "ERR|TTS_TIMEOUT|合成超时（" + RequestTimeoutMs.ToString() + " ms）";
                }
                catch (Exception ex)
                {
                    return "ERR|TTS_FAIL|" + ex.GetType().Name + ": " + ex.Message;
                }
                finally
                {
                    if (response != null)
                    {
                        response.Dispose();
                    }
                }
            }
        }

        /// <summary>
        /// 抽取服务端错误文案——优先 error.message，其次原文（截断 300 字符）。
        /// </summary>
        /// <param name="bytes">响应字节</param>
        /// <returns>错误文案</returns>
        private static string ExtractError(byte[] bytes)
        {
            if (bytes == null || bytes.Length == 0)
            {
                return "(无响应体)";
            }
            string raw = Encoding.UTF8.GetString(bytes);
            JsonDocument doc = JsonUtil.Parse(raw, "tts-error");
            if (doc == null)
            {
                return raw.Length > 300 ? raw.Substring(0, 300) : raw;
            }
            try
            {
                JsonElement root = doc.RootElement;
                JsonElement error;
                if (root.ValueKind == JsonValueKind.Object && root.TryGetProperty("error", out error))
                {
                    if (error.ValueKind == JsonValueKind.Object)
                    {
                        return JsonUtil.GetStr(error, "message", raw);
                    }
                    return error.ToString();
                }
                return raw.Length > 300 ? raw.Substring(0, 300) : raw;
            }
            finally
            {
                doc.Dispose();
            }
        }

        /// <summary>
        /// WAV 头修复——流式响应的 RIFF / data 长度是占位值（0xFFFFFFFF），改写为实际长度。
        /// 已是正常值（非占位且不超过文件长度）时原样返回，保证幂等。
        /// </summary>
        /// <param name="bytes">原始 WAV 字节</param>
        /// <returns>头已修正的字节</returns>
        private static byte[] FixWavHeader(byte[] bytes)
        {
            if (bytes == null || bytes.Length < 44)
            {
                return bytes;
            }
            if (bytes[0] != (byte)'R' || bytes[1] != (byte)'I' || bytes[2] != (byte)'F' || bytes[3] != (byte)'F')
            {
                return bytes;
            }
            byte[] copy = (byte[])bytes.Clone();
            int riffSize = copy.Length - 8;
            WriteUInt32(copy, 4, (uint)riffSize);
            // [段1] 遍历 chunk 找 data（fmt 之后）
            int pos = 12;
            while (pos + 8 <= copy.Length)
            {
                uint declared = ReadUInt32(copy, pos + 4);
                bool isData = copy[pos] == (byte)'d' && copy[pos + 1] == (byte)'a'
                              && copy[pos + 2] == (byte)'t' && copy[pos + 3] == (byte)'a';
                if (isData)
                {
                    uint actual = (uint)(copy.Length - (pos + 8));
                    if (declared != actual)
                    {
                        WriteUInt32(copy, pos + 4, actual);
                    }
                    return copy;
                }
                if (declared == PlaceholderSize)
                {
                    // 非 data 块却是占位长度——结构不可信，放弃修复（原样返回，失败可见由调用方时长校验兜底）
                    return copy;
                }
                pos = pos + 8 + (int)declared;
            }
            return copy;
        }

        /// <summary>
        /// 读小端 uint32。
        /// </summary>
        /// <param name="buffer">字节数组</param>
        /// <param name="offset">偏移</param>
        /// <returns>取值</returns>
        private static uint ReadUInt32(byte[] buffer, int offset)
        {
            return (uint)(buffer[offset] | (buffer[offset + 1] << 8)
                          | (buffer[offset + 2] << 16) | (buffer[offset + 3] << 24));
        }

        /// <summary>
        /// 写小端 uint32。
        /// </summary>
        /// <param name="buffer">字节数组</param>
        /// <param name="offset">偏移</param>
        /// <param name="value">取值</param>
        private static void WriteUInt32(byte[] buffer, int offset, uint value)
        {
            buffer[offset] = (byte)(value & 0xFF);
            buffer[offset + 1] = (byte)((value >> 8) & 0xFF);
            buffer[offset + 2] = (byte)((value >> 16) & 0xFF);
            buffer[offset + 3] = (byte)((value >> 24) & 0xFF);
        }
    }
}
