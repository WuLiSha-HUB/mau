using System;
using System.Collections.Generic;
using System.IO;
using System.Text.Json;
using Microsoft.AspNetCore.Http;
using Mau.Runtime;

namespace CatHome4.Admin
{
    /// <summary>
    /// Admin 用量统计分部（A389）——扫 Data/tokennumber/&lt;猫&gt;/&lt;日&gt;.jsonl 聚合出按猫按日快照。
    /// 刷新两条入口：后台每分钟（HttpHost 周期任务注入 RefreshTokenUsageStats）+ 手动 POST /api/v1/token-stats/refresh。
    /// 快照只在刷新时重算，读端点直接返回缓存（A390 页面读它）。
    /// </summary>
    internal static partial class AdminService
    {
        /// <summary>快照锁——后台周期刷新与手动刷新并发下串行化（同一份缓存）</summary>
        private static readonly object _tokenStatsLock = new object();

        /// <summary>最近一次快照 JSON（空 = 从未刷新）</summary>
        private static string _tokenStatsJson = "";

        /// <summary>
        /// 重算用量快照——全量扫描 tokennumber 树（按猫目录 × 日文件），逐行累加请求数 / prompt / completion / cacheHit / miss。
        /// 坏行不静默：计入 badLines 一并返回；目录缺失按空快照返回（首启无用量）。
        /// </summary>
        public static void RefreshTokenUsageStats()
        {
            lock (_tokenStatsLock)
            {
                _tokenStatsJson = BuildTokenUsageStatsJson(_dataRoot);
            }
        }

        /// <summary>
        /// 扫描并聚合用量快照——数据根入参（不读写缓存），刷新与测试共用同一实现。
        /// </summary>
        /// <param name="dataRoot">数据根（CatHome4 数据目录父级；空 = 空快照）</param>
        /// <returns>快照 JSON</returns>
        public static string BuildTokenUsageStatsJson(string dataRoot)
        {
            long startedAt = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
            List<string> catItems = new List<string>();
            long totalRequests = 0;
            long totalPrompt = 0;
            long totalCompletion = 0;
            long totalCacheHit = 0;
            long totalMiss = 0;
            long badLines = 0;
            if (dataRoot != null && dataRoot.Length > 0)
            {
                string root = Path.Combine(dataRoot, "Data", "tokennumber");
                if (Directory.Exists(root))
                {
                    string[] catDirs = Directory.GetDirectories(root);
                    Array.Sort(catDirs, StringComparer.Ordinal);
                    for (int i = 0; i < catDirs.Length; i = i + 1)
                    {
                        string catKey = Path.GetFileName(catDirs[i]);
                        string[] files = Directory.GetFiles(catDirs[i], "*.jsonl");
                        Array.Sort(files, StringComparer.Ordinal);
                        List<string> dayItems = new List<string>();
                        long catRequests = 0;
                        long catPrompt = 0;
                        long catCompletion = 0;
                        long catCacheHit = 0;
                        long catMiss = 0;
                        for (int f = 0; f < files.Length; f = f + 1)
                        {
                            string day = Path.GetFileNameWithoutExtension(files[f]);
                            long dayRequests = 0;
                            long dayPrompt = 0;
                            long dayCompletion = 0;
                            long dayCacheHit = 0;
                            long dayMiss = 0;
                            long dayLastTs = 0;
                            string[] lines = File.ReadAllLines(files[f]);
                            for (int n = 0; n < lines.Length; n = n + 1)
                            {
                                if (lines[n].Length == 0)
                                {
                                    continue;
                                }
                                if (!TryParseUsageLine(lines[n], ref dayRequests, ref dayPrompt, ref dayCompletion, ref dayCacheHit, ref dayMiss, ref dayLastTs))
                                {
                                    badLines = badLines + 1;
                                }
                            }
                            if (dayRequests > 0)
                            {
                                dayItems.Add(JsonUtil.Object(
                                    ("day", day),
                                    ("requests", dayRequests),
                                    ("prompt", dayPrompt),
                                    ("completion", dayCompletion),
                                    ("cacheHit", dayCacheHit),
                                    ("miss", dayMiss),
                                    ("lastTs", dayLastTs)));
                                catRequests = catRequests + dayRequests;
                                catPrompt = catPrompt + dayPrompt;
                                catCompletion = catCompletion + dayCompletion;
                                catCacheHit = catCacheHit + dayCacheHit;
                                catMiss = catMiss + dayMiss;
                            }
                        }
                        catItems.Add(JsonUtil.Object(
                            ("catKey", catKey),
                            ("displayName", ResolveTokenStatsDisplayName(dataRoot, catKey)),
                            ("requests", catRequests),
                            ("prompt", catPrompt),
                            ("completion", catCompletion),
                            ("cacheHit", catCacheHit),
                            ("miss", catMiss),
                            ("days", JsonUtil.RawArray(dayItems.ToArray()))));
                        totalRequests = totalRequests + catRequests;
                        totalPrompt = totalPrompt + catPrompt;
                        totalCompletion = totalCompletion + catCompletion;
                        totalCacheHit = totalCacheHit + catCacheHit;
                        totalMiss = totalMiss + catMiss;
                    }
                }
            }
            long scanMs = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds() - startedAt;
            return JsonUtil.Object(
                ("ok", true),
                ("refreshedAt", startedAt),
                ("scanMs", scanMs),
                ("badLines", badLines),
                ("totals", JsonUtil.Raw(JsonUtil.Object(
                    ("requests", totalRequests),
                    ("prompt", totalPrompt),
                    ("completion", totalCompletion),
                    ("cacheHit", totalCacheHit),
                    ("miss", totalMiss)))),
                ("cats", JsonUtil.RawArray(catItems.ToArray())));
        }

        /// <summary>
        /// 显示名解析——按传入数据根读 cat.cfg（不依赖 Admin 全域 _dataRoot 静态，刷新与测试同源）。
        /// </summary>
        /// <param name="dataRoot">数据根</param>
        /// <param name="catKey">猫 key</param>
        /// <returns>显示名（cat.cfg 缺失 = 空串）</returns>
        private static string ResolveTokenStatsDisplayName(string dataRoot, string catKey)
        {
            string path = Path.Combine(dataRoot, "Data", "sessions", catKey, "cat.cfg");
            CatHostService.CatCfgData cfg = CatHostService.LoadCatCfg(path);
            if (cfg == null || cfg.DisplayName == null)
            {
                return "";
            }
            return cfg.DisplayName;
        }

        /// <summary>
        /// 单行用量累加——成功返回 true；字段缺失 / 值类型不符 / JSON 非法返回 false（调用方计坏行，不静默）。
        /// </summary>
        /// <param name="line">JSONL 单行文本</param>
        /// <param name="requests">请求数累加引用</param>
        /// <param name="prompt">输入 token 累加引用</param>
        /// <param name="completion">输出 token 累加引用</param>
        /// <param name="cacheHit">缓存命中 token 累加引用</param>
        /// <param name="miss">未命中 token 累加引用</param>
        /// <param name="lastTs">最后一条记录时刻引用（取行内 ts）</param>
        /// <returns>true = 已累加</returns>
        private static bool TryParseUsageLine(string line, ref long requests, ref long prompt, ref long completion, ref long cacheHit, ref long miss, ref long lastTs)
        {
            try
            {
                using (JsonDocument doc = JsonUtil.ParseStrict(line))
                {
                    JsonElement root = doc.RootElement;
                    if (root.ValueKind != JsonValueKind.Object)
                    {
                        return false;
                    }
                    long vPrompt = 0;
                    long vCompletion = 0;
                    long vCacheHit = 0;
                    long vMiss = 0;
                    long vTs = 0;
                    JsonElement el;
                    if (root.TryGetProperty("prompt", out el) && el.ValueKind == JsonValueKind.Number)
                    {
                        vPrompt = el.GetInt64();
                    }
                    else
                    {
                        return false;
                    }
                    if (root.TryGetProperty("completion", out el) && el.ValueKind == JsonValueKind.Number)
                    {
                        vCompletion = el.GetInt64();
                    }
                    if (root.TryGetProperty("cacheHit", out el) && el.ValueKind == JsonValueKind.Number)
                    {
                        vCacheHit = el.GetInt64();
                    }
                    if (root.TryGetProperty("miss", out el) && el.ValueKind == JsonValueKind.Number)
                    {
                        vMiss = el.GetInt64();
                    }
                    if (root.TryGetProperty("ts", out el) && el.ValueKind == JsonValueKind.Number)
                    {
                        vTs = el.GetInt64();
                    }
                    requests = requests + 1;
                    prompt = prompt + vPrompt;
                    completion = completion + vCompletion;
                    cacheHit = cacheHit + vCacheHit;
                    miss = miss + vMiss;
                    if (vTs > lastTs)
                    {
                        lastTs = vTs;
                    }
                    return true;
                }
            }
            catch (Exception)
            {
                // 坏行一律计入 badLines（JsonUtil.ParseStrict 抛 InvalidDataException，原生解析抛 JsonException——统一按解析失败处置）
                return false;
            }
        }

        /// <summary>
        /// 用量快照读取——GET /api/v1/token-stats（页面读面；从未刷新则现场算一次）。
        /// </summary>
        /// <returns>快照 JSON</returns>
        internal static IResult HandleTokenStatsGet()
        {
            string json = _tokenStatsJson;
            if (json == null || json.Length == 0)
            {
                RefreshTokenUsageStats();
                json = _tokenStatsJson;
            }
            return Results.Text(json, "application/json");
        }

        /// <summary>
        /// 用量快照手动刷新——POST /api/v1/token-stats/refresh（重算后返回新快照）。
        /// </summary>
        /// <returns>快照 JSON</returns>
        internal static IResult HandleTokenStatsRefresh()
        {
            RefreshTokenUsageStats();
            return Results.Text(_tokenStatsJson, "application/json");
        }
    }
}
