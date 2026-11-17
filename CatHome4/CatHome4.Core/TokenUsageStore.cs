using System;
using System.IO;
using System.Text;
using Mau.Runtime;

namespace CH4
{
    /// <summary>
    /// Token 用量流水存储（A388）——每次 LLM 请求一行 JSONL 追加落盘，供统计面按猫按日汇总。
    /// 落点：&lt;数据根&gt;/Data/tokennumber/&lt;catKey&gt;/&lt;yyyyMMdd&gt;.jsonl（按猫分目录、按本地日期分文件）。
    /// 契约：UTF-8 无 BOM、一行一条、只追加不改写；行字段 ts（Unix 毫秒）/ round / req / prompt / completion / cacheHit / miss。
    /// </summary>
    internal static class TokenUsageStore
    {
        /// <summary>流水目录解析——猫 key → 目录绝对路径（入口壳注入；空 = 不落盘——测试环境）</summary>
        public static Func<string, string> DirProvider;

        /// <summary>落盘编码——UTF-8 无 BOM（JSONL 契约）</summary>
        private static readonly UTF8Encoding Utf8NoBom = new UTF8Encoding(false);

        /// <summary>写锁——同进程多会话并发追加时保证整行不撕裂</summary>
        private static readonly object WriteLock = new object();

        /// <summary>
        /// 追加一条用量记录——单次请求一行（三项全零 = 无用量帧，不落行）。
        /// </summary>
        /// <param name="catKey">猫 key（目录名）</param>
        /// <param name="round">轮号</param>
        /// <param name="request">本轮请求号（含重试重发与空回复续传）</param>
        /// <param name="prompt">本次请求输入 token（含缓存命中部分）</param>
        /// <param name="completion">本次请求输出 token</param>
        /// <param name="cacheHit">本次请求缓存命中 token</param>
        /// <param name="nowMs">记录时刻——Unix 毫秒（文件名按本地日期派生）</param>
        public static void Append(string catKey, long round, int request, long prompt, long completion, long cacheHit, long nowMs)
        {
            if (DirProvider == null || catKey == null || catKey.Length == 0)
            {
                return;
            }
            if (prompt <= 0 && completion <= 0 && cacheHit <= 0)
            {
                return;
            }
            string dir = DirProvider(catKey);
            if (dir == null || dir.Length == 0)
            {
                return;
            }
            long miss = prompt - cacheHit;
            if (miss < 0)
            {
                miss = 0;
            }
            string line = JsonUtil.Object(
                ("ts", nowMs),
                ("round", round),
                ("req", request),
                ("prompt", prompt),
                ("completion", completion),
                ("cacheHit", cacheHit),
                ("miss", miss));
            try
            {
                Directory.CreateDirectory(dir);
                string day = DateTimeOffset.FromUnixTimeMilliseconds(nowMs).LocalDateTime.ToString("yyyyMMdd");
                string path = Path.Combine(dir, day + ".jsonl");
                lock (WriteLock)
                {
                    File.AppendAllText(path, line + "\n", Utf8NoBom);
                }
            }
            catch (Exception ex)
            {
                // 失败必须可见——落盘失败记 L3，不影响会话主链
                LogStore.Add("CatHome4", 3, "Token 用量落盘失败: " + catKey + " | " + ex.Message, "TOKEN");
            }
        }
    }
}
