using System;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Text;
using System.Text.Json;
using System.Threading;
using Mau.Runtime;
using Xunit;

namespace Mau.Providers.Tests
{
    /// <summary>
    /// user 消息图片附件测试（design-ch4-chat-images §8.2 / §8.4）——
    /// 内容块数组形态 / 失效引用可见（不阻断请求）/ 图片载荷构建（本地 base64 · 缺失 · URL 直通 · 超长）。
    /// </summary>
    public sealed class UserMessageImagesTests
    {
        /// <summary>
        /// 建临时图片文件——内容为任意字节（载荷构建只做 base64，不校验图片结构）。
        /// </summary>
        /// <param name="extension">扩展名（含点）</param>
        /// <returns>文件绝对路径（正斜杠形态）</returns>
        private static string CreateTempImage(string extension)
        {
            string path = Path.Combine(Path.GetTempPath(), "mauimg_" + Guid.NewGuid().ToString("N") + extension);
            File.WriteAllBytes(path, new byte[] { 1, 2, 3, 4 });
            return path.Replace("\\", "/");
        }

        /// <summary>
        /// 构造带附件的 user 消息。
        /// </summary>
        /// <param name="text">正文</param>
        /// <param name="imagesJson">附件引用 JSON 数组</param>
        /// <returns>消息</returns>
        private static LlmMessage UserMessage(string text, string imagesJson)
        {
            LlmMessage m = new LlmMessage();
            m.Role = LlmRole.User;
            m.Content = text;
            m.ImagesJson = imagesJson;
            return m;
        }

        /// <summary>
        /// 带有效附件——wire content 为内容块数组（text 块 + image_url 块，data URL 内联）。
        /// </summary>
        [Fact]
        public void BuildUserMessage_WithValidImage_ProducesContentParts()
        {
            string path = CreateTempImage(".png");
            try
            {
                object wire = DeepSeekLlmRuntime.BuildUserMessageWithImages(UserMessage("看图", "[\"" + path + "\"]"));
                string json = JsonSerializer.Serialize(wire);
                Assert.Contains("\"type\":\"text\"", json, StringComparison.Ordinal);
                Assert.Contains("\"image_url\"", json, StringComparison.Ordinal);
                Assert.Contains("data:image/png;base64,", json, StringComparison.Ordinal);
            }
            finally
            {
                if (File.Exists(path))
                {
                    File.Delete(path);
                }
            }
        }

        /// <summary>
        /// 失效引用——不阻断请求：无 image_url 块，正文并入失败信息（失败必须可见）。
        /// </summary>
        [Fact]
        public void BuildUserMessage_WithMissingImage_ReportsInText()
        {
            string path = Path.Combine(Path.GetTempPath(), "mauimg_missing_" + Guid.NewGuid().ToString("N") + ".png").Replace("\\", "/");
            object wire = DeepSeekLlmRuntime.BuildUserMessageWithImages(UserMessage("看图", "[\"" + path + "\"]"));
            string json = JsonSerializer.Serialize(wire);
            Assert.Contains("IMAGE_NOT_FOUND", json, StringComparison.Ordinal);
            Assert.DoesNotContain("\"image_url\"", json, StringComparison.Ordinal);
            // 中文断言经解析（System.Text.Json 默认 Unicode 转义——判例：直接断言原文必失败）
            using (JsonDocument doc = JsonDocument.Parse(json))
            {
                string text = doc.RootElement.GetProperty("content")[0].GetProperty("text").GetString() ?? "";
                Assert.Contains("图片引用失效", text, StringComparison.Ordinal);
            }
        }

        /// <summary>
        /// 引用数组畸形——不抛异常；正文兜底可读（解析失败可见）。
        /// </summary>
        [Fact]
        public void BuildUserMessage_WithMalformedRefs_DoesNotThrow()
        {
            object wire = DeepSeekLlmRuntime.BuildUserMessageWithImages(UserMessage("看图", "{not-json"));
            string json = JsonSerializer.Serialize(wire);
            Assert.Contains("IMAGE_REF_PARSE", json, StringComparison.Ordinal);
        }

        /// <summary>
        /// 载荷构建——本地文件 → data URL（media type 按扩展名）。
        /// </summary>
        [Fact]
        public void Resolve_LocalFile_ReturnsDataUrl()
        {
            string path = CreateTempImage(".jpg");
            try
            {
                string error = "";
                string url = ImagePayload.Resolve(path, out error);
                Assert.Equal("", error);
                Assert.StartsWith("data:image/jpeg;base64,", url, StringComparison.Ordinal);
            }
            finally
            {
                if (File.Exists(path))
                {
                    File.Delete(path);
                }
            }
        }

        /// <summary>
        /// 载荷构建——文件不存在 → ERR|IMAGE_NOT_FOUND。
        /// </summary>
        [Fact]
        public void Resolve_MissingFile_ReturnsError()
        {
            string error = "";
            string url = ImagePayload.Resolve("C:/definitely/not/here.png", out error);
            Assert.Equal("", url);
            Assert.StartsWith("ERR|IMAGE_NOT_FOUND", error, StringComparison.Ordinal);
        }

        /// <summary>
        /// 载荷构建——http(s) URL 直通（长度合规）。
        /// </summary>
        [Fact]
        public void Resolve_HttpUrl_PassesThrough()
        {
            string error = "";
            string url = ImagePayload.Resolve("https://example.com/a.png", out error);
            Assert.Equal("", error);
            Assert.Equal("https://example.com/a.png", url);
        }

        /// <summary>
        /// 载荷构建——超长 URL → ERR|IMAGE_URL_TOO_LONG。
        /// </summary>
        [Fact]
        public void Resolve_TooLongUrl_ReturnsError()
        {
            string error = "";
            string longUrl = "https://example.com/" + new string('a', 9000) + ".png";
            string url = ImagePayload.Resolve(longUrl, out error);
            Assert.Equal("", url);
            Assert.StartsWith("ERR|IMAGE_URL_TOO_LONG", error, StringComparison.Ordinal);
        }

        /// <summary>
        /// 回环地址判定——127/10/172.16-31/192.168/169.254/localhost/::1 判本机，公网名与公网 IP 判直传。
        /// </summary>
        /// <param name="url">候选 URL</param>
        /// <param name="expected">期望判定</param>
        [Theory]
        [InlineData("http://127.0.0.1:18520/shot.png", true)]
        [InlineData("http://localhost:8080/a.png", true)]
        [InlineData("http://10.1.2.3/a.png", true)]
        [InlineData("http://172.20.0.1/a.png", true)]
        [InlineData("http://192.168.1.9/a.png", true)]
        [InlineData("http://169.254.1.1/a.png", true)]
        [InlineData("http://[::1]:18520/a.png", true)]
        [InlineData("https://example.com/a.png", false)]
        [InlineData("https://8.8.8.8/a.png", false)]
        [InlineData("http://172.32.0.1/a.png", false)]
        public void IsLocalOnlyHost_ClassifiesAddress(string url, bool expected)
        {
            Assert.Equal(expected, ImagePayload.IsLocalOnlyHost(new Uri(url)));
        }

        /// <summary>
        /// 图片魔数判定——PNG/JPEG/GIF/WebP/BMP 识别，非图片字节空串（媒体类型按内容而非扩展名）。
        /// </summary>
        [Fact]
        public void SniffImageMime_DetectsFormats()
        {
            Assert.Equal("image/png", ImagePayload.SniffImageMime(PngBytes()));
            Assert.Equal("image/jpeg", ImagePayload.SniffImageMime(new byte[] { 0xFF, 0xD8, 0xFF, 0xE0, 0, 0, 0, 0, 0, 0, 0, 0 }));
            Assert.Equal("image/gif", ImagePayload.SniffImageMime(new byte[] { 0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0, 0, 0, 0, 0, 0 }));
            Assert.Equal("image/webp", ImagePayload.SniffImageMime(new byte[] { 0x52, 0x49, 0x46, 0x46, 0, 0, 0, 0, 0x57, 0x45, 0x42, 0x50 }));
            Assert.Equal("image/bmp", ImagePayload.SniffImageMime(new byte[] { 0x42, 0x4D, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0 }));
            Assert.Equal("", ImagePayload.SniffImageMime(Encoding.ASCII.GetBytes("<html>not an image</html>")));
        }

        /// <summary>
        /// 回环 URL——本机抓取后内联为 data URL（判例 2026-10-29：回环地址直传上游必然 Failed to download images）。
        /// </summary>
        [Fact]
        public void Resolve_LoopbackUrl_InlinesLocally()
        {
            string url = ServeOnceOnLoopback(PngBytes(), "image/png");
            string error = "";
            string resolved = ImagePayload.Resolve(url, out error);
            Assert.Equal("", error);
            Assert.StartsWith("data:image/png;base64,", resolved, StringComparison.Ordinal);
        }

        /// <summary>
        /// 回环 URL 返回非图片——具名报错，不把 HTML 错误页当图片内联。
        /// </summary>
        [Fact]
        public void Resolve_LoopbackUrlNonImage_ReturnsMimeError()
        {
            string url = ServeOnceOnLoopback(Encoding.ASCII.GetBytes("<html>oops</html>"), "text/html");
            string error = "";
            string resolved = ImagePayload.Resolve(url, out error);
            Assert.Equal("", resolved);
            Assert.StartsWith("ERR|IMAGE_MIME", error, StringComparison.Ordinal);
        }

        /// <summary>
        /// 回环 URL 不可达——具名报错（失败可见，不回落直传上游）。
        /// </summary>
        [Fact]
        public void Resolve_LoopbackUrlUnreachable_ReturnsFetchError()
        {
            TcpListener probe = new TcpListener(IPAddress.Loopback, 0);
            probe.Start();
            int port = ((IPEndPoint)probe.LocalEndpoint).Port;
            probe.Stop();
            string error = "";
            string resolved = ImagePayload.Resolve("http://127.0.0.1:" + port.ToString() + "/shot.png", out error);
            Assert.Equal("", resolved);
            Assert.StartsWith("ERR|IMAGE_FETCH", error, StringComparison.Ordinal);
        }

        /// <summary>
        /// PNG 头字节——含真魔数 + 填充（魔数判定要求 ≥12 字节）。
        /// </summary>
        /// <returns>字节数组</returns>
        private static byte[] PngBytes()
        {
            return new byte[] { 0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0, 0, 0, 0, 0 };
        }

        /// <summary>
        /// 起一个只服务一次的本地 HTTP 端点（裸 TcpListener——不经 HttpListener，免 URL ACL 依赖）。
        /// </summary>
        /// <param name="body">响应体</param>
        /// <param name="contentType">响应 Content-Type</param>
        /// <returns>回环 URL</returns>
        private static string ServeOnceOnLoopback(byte[] body, string contentType)
        {
            TcpListener listener = new TcpListener(IPAddress.Loopback, 0);
            listener.Start();
            int port = ((IPEndPoint)listener.LocalEndpoint).Port;
            Thread worker = new Thread(() =>
            {
                try
                {
                    using (TcpClient client = listener.AcceptTcpClient())
                    using (NetworkStream stream = client.GetStream())
                    {
                        byte[] request = new byte[4096];
                        stream.Read(request, 0, request.Length);
                        string head = "HTTP/1.1 200 OK\r\nContent-Type: " + contentType
                            + "\r\nContent-Length: " + body.Length.ToString()
                            + "\r\nConnection: close\r\n\r\n";
                        byte[] headBytes = Encoding.ASCII.GetBytes(head);
                        stream.Write(headBytes, 0, headBytes.Length);
                        stream.Write(body, 0, body.Length);
                        stream.Flush();
                    }
                }
                finally
                {
                    listener.Stop();
                }
            });
            worker.IsBackground = true;
            worker.Start();
            return "http://127.0.0.1:" + port.ToString() + "/shot.png?target=test";
        }
    }
}
