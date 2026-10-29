using System;
using System.IO;
using System.Net;
using System.Net.Http;
using System.Net.Sockets;

namespace Mau.Providers
{
    /// <summary>
    /// 图片载荷构建——本地路径 / 外部 URL → `image_url` 可用串（单一实现，多处共用）。
    /// 消费方：vision 服务（image-analyze）· 对话请求构造（user 消息附件展开 · design-ch4-chat-images §8.2）。
    /// 本地：读取字节 → 32MiB 上限检查 → base64 data URL（media type 按魔数判定，兜底扩展名 → image/jpeg）。
    /// 回环 / 私网 URL（127.0.0.1 · localhost · 10/8 · 172.16-31 · 192.168/16 · 链路本地）：**本机抓取后内联**——
    ///   上游服务器访问不到这些地址，原样直传必被判 Failed to download images（判例 2026-10-29）。
    /// 公网 URL：直传（由上游服务器下载，上游需可达；长度 ≤8192 校验）。
    /// </summary>
    internal static class ImagePayload
    {
        /// <summary>
        /// 单图最大字节——base64 内联 32 MiB 上限（视觉 API 通用上限）。
        /// </summary>
        internal const long MaxInlineImageBytes = 32L * 1024 * 1024;

        /// <summary>
        /// 外部图片 URL 最大长度——超长改走本地路径。
        /// </summary>
        private const int MaxUrlLength = 8192;

        /// <summary>
        /// 本机抓取超时毫秒——回环 / 私网截图服务本地响应，15 秒足够（超时具名报错，不静默回落直传上游）。
        /// </summary>
        private const int FetchTimeoutMs = 15000;

        /// <summary>
        /// 本机抓取客户端——静态复用（回环抓取低频，避免每图新建套接字）；超时按 FetchTimeoutMs 兜底。
        /// </summary>
        private static readonly HttpClient FetchClient = CreateFetchClient();

        /// <summary>
        /// 构建图片载荷——本地路径 base64 内联 / 回环与私网 URL 本机抓取后内联 / 公网 URL 直传（官方文档：格式由文件实际内容判断）。
        /// </summary>
        /// <param name="imagePath">图片路径（本地绝对路径或 http(s) URL）</param>
        /// <param name="error">错误文本（空=成功；失败为 ERR| 前缀——错误可见性）</param>
        /// <returns>image_url 串（data URL 或外部 URL；失败空串）</returns>
        internal static string Resolve(string imagePath, out string error)
        {
            error = "";
            if (imagePath == null || imagePath.Length == 0)
            {
                error = "ERR|BAD_ARGS|缺少图片路径";
                return "";
            }
            string trimmed = imagePath.Trim();
            if (trimmed.StartsWith("http://", StringComparison.OrdinalIgnoreCase) || trimmed.StartsWith("https://", StringComparison.OrdinalIgnoreCase))
            {
                if (trimmed.Length > MaxUrlLength)
                {
                    error = "ERR|IMAGE_URL_TOO_LONG|外部图片 URL 超长（>" + MaxUrlLength.ToString() + " 字符——请改用本地路径）";
                    return "";
                }
                Uri? uri;
                if (!Uri.TryCreate(trimmed, UriKind.Absolute, out uri) || uri == null)
                {
                    error = "ERR|IMAGE_URL_INVALID|图片 URL 无法解析: " + trimmed;
                    return "";
                }
                if (!IsLocalOnlyHost(uri))
                {
                    return trimmed;
                }
                return FetchLocalUrl(trimmed, out error);
            }
            if (!File.Exists(trimmed))
            {
                error = "ERR|IMAGE_NOT_FOUND|图片文件不存在: " + trimmed;
                return "";
            }
            long length = new FileInfo(trimmed).Length;
            if (length > MaxInlineImageBytes)
            {
                error = "ERR|IMAGE_TOO_LARGE|图片超过 32MiB 内联上限（" + length.ToString() + " 字节——请压缩或改用 Files API）";
                return "";
            }
            byte[] bytes;
            try
            {
                bytes = File.ReadAllBytes(trimmed);
            }
            catch (Exception ex)
            {
                error = "ERR|IMAGE_READ|图片读取失败: " + ex.Message;
                return "";
            }
            string mime = SniffImageMime(bytes);
            if (mime.Length == 0)
            {
                mime = GuessImageMime(trimmed);
            }
            return "data:" + mime + ";base64," + Convert.ToBase64String(bytes);
        }

        /// <summary>
        /// 判断 URL 主机是否只在本机可达——回环 / 私网 / 链路本地地址由上游服务器访问不到，必须本机抓取后内联。
        /// </summary>
        /// <param name="uri">图片 URL</param>
        /// <returns>true=本机可达（需本机抓取）</returns>
        internal static bool IsLocalOnlyHost(Uri uri)
        {
            if (uri == null)
            {
                return false;
            }
            string host = uri.Host;
            if (host.Length == 0)
            {
                return false;
            }
            if (host.Equals("localhost", StringComparison.OrdinalIgnoreCase) || host.EndsWith(".localhost", StringComparison.OrdinalIgnoreCase))
            {
                return true;
            }
            string literal = host.StartsWith("[", StringComparison.Ordinal) && host.EndsWith("]", StringComparison.Ordinal)
                ? host.Substring(1, host.Length - 2)
                : host;
            IPAddress? address;
            if (!IPAddress.TryParse(literal, out address) || address == null)
            {
                return false;
            }
            if (address.IsIPv4MappedToIPv6)
            {
                address = address.MapToIPv4();
            }
            if (IPAddress.IsLoopback(address))
            {
                return true;
            }
            if (address.AddressFamily == AddressFamily.InterNetwork)
            {
                byte[] v4 = address.GetAddressBytes();
                return v4[0] == 10
                    || (v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31)
                    || (v4[0] == 192 && v4[1] == 168)
                    || (v4[0] == 169 && v4[1] == 254);
            }
            if (address.AddressFamily == AddressFamily.InterNetworkV6)
            {
                if (address.IsIPv6LinkLocal || address.IsIPv6SiteLocal)
                {
                    return true;
                }
                byte[] v6 = address.GetAddressBytes();
                return (v6[0] & 0xFE) == 0xFC;
            }
            return false;
        }

        /// <summary>
        /// 本机抓取回环 / 私网图片并内联为 data URL——抓取失败 / 空内容 / 非图片一律具名报错（失败可见，不回落直传）。
        /// </summary>
        /// <param name="url">图片 URL（本机可达地址）</param>
        /// <param name="error">错误文本（空=成功；失败 ERR| 前缀）</param>
        /// <returns>data URL；失败空串</returns>
        private static string FetchLocalUrl(string url, out string error)
        {
            error = "";
            try
            {
                using (HttpRequestMessage request = new HttpRequestMessage(HttpMethod.Get, url))
                using (HttpResponseMessage response = FetchClient.Send(request, HttpCompletionOption.ResponseHeadersRead))
                {
                    if (!response.IsSuccessStatusCode)
                    {
                        error = "ERR|IMAGE_FETCH|本机抓取图片失败（HTTP " + ((int)response.StatusCode).ToString() + "）: " + url;
                        return "";
                    }
                    using (Stream stream = response.Content.ReadAsStream())
                    using (MemoryStream buffer = new MemoryStream())
                    {
                        byte[] chunk = new byte[81920];
                        int read;
                        while ((read = stream.Read(chunk, 0, chunk.Length)) > 0)
                        {
                            if (buffer.Length + read > MaxInlineImageBytes)
                            {
                                error = "ERR|IMAGE_TOO_LARGE|本机抓取到的图片超过 32MiB 内联上限: " + url;
                                return "";
                            }
                            buffer.Write(chunk, 0, read);
                        }
                        byte[] bytes = buffer.ToArray();
                        if (bytes.Length == 0)
                        {
                            error = "ERR|IMAGE_FETCH|本机抓取图片返回空内容: " + url;
                            return "";
                        }
                        string mime = SniffImageMime(bytes);
                        if (mime.Length == 0)
                        {
                            mime = NormalizeMediaType(response.Content.Headers.ContentType?.MediaType);
                        }
                        if (mime.Length == 0)
                        {
                            error = "ERR|IMAGE_MIME|本机抓取到的内容不是可识别的图片（" + url + "）";
                            return "";
                        }
                        return "data:" + mime + ";base64," + Convert.ToBase64String(bytes);
                    }
                }
            }
            catch (Exception ex)
            {
                error = "ERR|IMAGE_FETCH|本机抓取图片异常: " + ex.Message + "（" + url + "）";
                return "";
            }
        }

        /// <summary>
        /// 响应头 media type 归一——只接受 image/* 前缀，其余返回空串（HTML 错误页不得当图片内联）。
        /// </summary>
        /// <param name="mediaType">Content-Type 的 media type</param>
        /// <returns>image/* media type；不可用空串</returns>
        private static string NormalizeMediaType(string? mediaType)
        {
            if (mediaType == null || mediaType.Length == 0)
            {
                return "";
            }
            string trimmed = mediaType.Trim();
            return trimmed.StartsWith("image/", StringComparison.OrdinalIgnoreCase) ? trimmed.ToLowerInvariant() : "";
        }

        /// <summary>
        /// 图片魔数判定——优先于扩展名与响应头（官方：格式由内容判断）。
        /// </summary>
        /// <param name="bytes">图片字节</param>
        /// <returns>media type；不可识别空串</returns>
        internal static string SniffImageMime(byte[] bytes)
        {
            if (bytes == null || bytes.Length < 12)
            {
                return "";
            }
            if (bytes[0] == 0x89 && bytes[1] == 0x50 && bytes[2] == 0x4E && bytes[3] == 0x47)
            {
                return "image/png";
            }
            if (bytes[0] == 0xFF && bytes[1] == 0xD8 && bytes[2] == 0xFF)
            {
                return "image/jpeg";
            }
            if (bytes[0] == 0x47 && bytes[1] == 0x49 && bytes[2] == 0x46 && bytes[3] == 0x38)
            {
                return "image/gif";
            }
            if (bytes[0] == 0x52 && bytes[1] == 0x49 && bytes[2] == 0x46 && bytes[3] == 0x46
                && bytes[8] == 0x57 && bytes[9] == 0x45 && bytes[10] == 0x42 && bytes[11] == 0x50)
            {
                return "image/webp";
            }
            if (bytes[0] == 0x42 && bytes[1] == 0x4D)
            {
                return "image/bmp";
            }
            return "";
        }

        /// <summary>
        /// 创建本机抓取客户端——超时按 FetchTimeoutMs（超时即具名报错）。
        /// </summary>
        /// <returns>HttpClient 实例</returns>
        private static HttpClient CreateFetchClient()
        {
            HttpClient client = new HttpClient();
            client.Timeout = TimeSpan.FromMilliseconds(FetchTimeoutMs);
            return client;
        }

        /// <summary>
        /// 图片 media type 推断——扩展名映射（官方：格式由内容判断，media type 兜底 image/jpeg）。
        /// </summary>
        /// <param name="path">图片路径</param>
        /// <returns>media type</returns>
        internal static string GuessImageMime(string path)
        {
            string ext = Path.GetExtension(path).ToLowerInvariant();
            if (ext == ".jpg" || ext == ".jpeg")
            {
                return "image/jpeg";
            }
            if (ext == ".png")
            {
                return "image/png";
            }
            if (ext == ".gif")
            {
                return "image/gif";
            }
            if (ext == ".webp")
            {
                return "image/webp";
            }
            return "image/jpeg";
        }
    }
}
